// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authDir = "../../testdata/deal-v1/authorization"

// authFlow is one task's journal and acts, built step by step: each record
// gets the next minute as its at, so journal order and time agree.
type authFlow struct {
	t       *testing.T
	minute  int
	journal []Record
	acts    []Record
	// cards are each verdict's card commitment, by verdict digest.
	cards map[string]string
}

func (f *authFlow) at() string {
	f.minute++
	return "2026-11-01T10:" + twoDigits(f.minute) + ":00Z"
}

func twoDigits(n int) string { return string([]byte{byte('0' + n/10), byte('0' + n%10)}) }

func (f *authFlow) seal(t string, dealScoped bool, body Body, refs ...RelRef) Ref {
	f.t.Helper()
	b := blk(t)
	if !dealScoped {
		b.DealID = ""
	}
	b.At, b.Refs = f.at(), refs
	r := record(b, body)
	if err := Check(r); err != nil {
		f.t.Fatalf("%s: %v", t, err)
	}
	f.journal = append(f.journal, r)
	d, err := Digest(r)
	require.NoError(f.t, err)
	return RefTo(d)
}

func rel(name string, r Ref) RelRef {
	return RelRef{Rel: name, Type: r.Type, DigestAlg: r.DigestAlg, Digest: r.Digest}
}

func (f *authFlow) intent(allowed ...string) Ref {
	return f.seal(TypeIntent, false, &dealprofile.IntentBody{VerbatimCommitment: hx('a'), Allowed: allowed})
}

func (f *authFlow) check(action string) Ref {
	return f.seal(TypeCheck, true, &CheckBody{CheckBody: dealprofile.CheckBody{Action: action}, PaymentRef: "invoice-17"})
}

func (f *authFlow) verdict(check Ref, result string) Ref {
	vb := &dealprofile.VerdictBody{Result: result, Differences: []dealprofile.Difference{}, Options: []string{}, CardCommitment: cardOf(check)}
	if result != "pass" {
		vb.Differences = []dealprofile.Difference{{Question: "terms", Rule: "limit"}}
		vb.Options = []string{"hold", "proceed"}
		if result == "deny" {
			vb.Options = []string{"hold"}
			vb.Rules = &dealprofile.VerdictBodyRules{Status: "not_configured"}
		}
	}
	v := f.seal(TypeVerdict, true, vb, rel("checks", check))
	if f.cards == nil {
		f.cards = map[string]string{}
	}
	f.cards[v.Digest] = vb.CardCommitment
	return v
}

// cardOf is the card commitment a verdict on check renders: one per check.
func cardOf(check Ref) string { return check.Digest[:32] + check.Digest[:32] }

func (f *authFlow) approve(verdict Ref, approver string, proceed bool) Ref {
	choice := "hold"
	if proceed {
		choice = "proceed"
	}
	a := &dealprofile.ApprovalBody{Choice: choice, Proceed: proceed, Approver: approver}
	if approver == "user" {
		a.SaidCommitment = hx('f')
	}
	if approver != "standing_intent" {
		a.CardCommitment = f.cards[verdict.Digest]
	}
	return f.seal(TypeApproval, true, a, rel("approves", verdict))
}

// confirm seals the person's confirm_limits approval of the proposal at
// intent, from allowed previous to allowed next.
func (f *authFlow) confirm(intent Ref, previous, next []string) Ref {
	a := &dealprofile.ApprovalBody{Choice: ChoiceConfirm, Proceed: true, Approver: "user", SaidCommitment: hx('f'),
		Limits: &dealprofile.ApprovalBodyLimits{Previous: dealprofile.LimitSet{Allowed: previous}, New: dealprofile.LimitSet{Allowed: next}}}
	return f.seal(TypeApproval, false, a, rel("approves", intent))
}

// confirmSets is confirm with whole limit sets.
func (f *authFlow) confirmSets(intent Ref, previous, next dealprofile.LimitSet) Ref {
	a := &dealprofile.ApprovalBody{Choice: ChoiceConfirm, Proceed: true, Approver: "user", SaidCommitment: hx('f'),
		Limits: &dealprofile.ApprovalBodyLimits{Previous: previous, New: next}}
	return f.seal(TypeApproval, false, a, rel("approves", intent))
}

func (f *authFlow) perform(check Ref) {
	b := inBook(TypePerform, int64(len(f.acts)+2))
	b.At = f.at()
	f.acts = append(f.acts, record(b, &PerformBody{Act: "deliver", Observed: "carrier_scan", Statements: []StatementItem{}, Check: &check}))
}

func (f *authFlow) pay(check Ref) {
	b := inBook(TypeSettle, int64(len(f.acts)+2))
	b.At = f.at()
	f.acts = append(f.acts, record(b, &SettleBody{Leg: LegPayer, Statements: []StatementItem{}, Check: &check}))
}

// approval-migration (design §3.2, invariant D; REQUIRED, review round 3):
// v0's authorization conditions hold for a v1 act, which cites its check
// instead of an approval. Written to
// testdata/deal-v1/authorization/approval-migration.json
// (EB_WRITE_DEAL_V1_VECTORS=1).
func TestApprovalMigration(t *testing.T) {
	type vcase struct {
		Name    string   `json:"name"`
		Why     string   `json:"why"`
		Journal []Record `json:"journal"`
		Acts    []Record `json:"acts"`
		Valid   bool     `json:"valid"`
		// PersonSignature is, per authorized act, whether its approval is
		// the person's own.
		PersonSignature []bool `json:"person_signature,omitempty"`
		// LimitApprovals are, per authorized act, how many confirm_limits
		// approvals its standing intent rested on.
		LimitApprovals []int `json:"limit_approvals,omitempty"`
		// IntentSignature is, per authorized act, whether it rests on a
		// first intent the person must have signed (a standing intent;
		// evidencebook#500, Steven's option b).
		IntentSignature []bool `json:"intent_signature,omitempty"`
	}
	var cases []vcase
	run := func(name, why string, f *authFlow, wantOK bool) {
		t.Helper()
		got, err := CheckAuthorizations(f.journal, f.acts)
		c := vcase{Name: name, Why: why, Journal: f.journal, Acts: f.acts, Valid: wantOK}
		if wantOK {
			require.NoError(t, err, name)
			for _, a := range got {
				c.PersonSignature = append(c.PersonSignature, a.NeedsPersonSignature)
				c.LimitApprovals = append(c.LimitApprovals, len(a.LimitApprovals))
				c.IntentSignature = append(c.IntentSignature, a.Intent != "")
				if a.Intent != "" {
					// The first intent of the journal, never a later
					// narrowing intent or a proposal.
					for _, r := range f.journal {
						if r.Block.RecordType == TypeIntent {
							assert.Equal(t, r.digestRef(t).Digest, a.Intent, "%s: the first intent", name)
							break
						}
					}
				}
			}
		} else {
			require.Error(t, err, name)
			assert.Equal(t, StageAuthorization, stageOf(err), "%s: %v", name, err)
		}
		cases = append(cases, c)
	}
	newFlow := func() *authFlow { return &authFlow{t: t} }

	f := newFlow()
	c := f.check("pay")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.pay(c)
	run("user-approves-a-pass", "a passing check, its verdict and the user's proceeding approval authorize the act; it needs the person's signature", f, true)

	f = newFlow()
	f.intent("pay")
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.pay(c)
	run("standing-intent-in-allowed", "a standing intent answers a passing verdict for an action in the allowed in force; the approval needs no person signature, the first intent it rests on does", f, true)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.pay(c)
	run("standing-intent-without-intent", "a standing-intent approval rests on an intent the person signs; with none before it, it authorizes nothing", f, false)

	f = newFlow()
	c = f.check("pay")
	f.verdict(c, "pass")
	f.pay(c)
	run("pass-without-approval", "a pass with no sealed approval authorizes nothing", f, false)

	f = newFlow()
	c = f.check("pay")
	v := f.verdict(c, "pause")
	f.approve(v, "user", false)
	f.approve(v, "user", true)
	f.pay(c)
	run("first-approval-refuses", "the verdict's first approval is a refusal; a later one that proceeds never authorizes", f, false)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.pay(c)
	f.pay(c)
	run("approval-reused", "one approval authorizes at most one act", f, false)

	f = newFlow()
	f.intent("commit")
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.pay(c)
	run("standing-intent-outside-allowed", "a standing intent approves only an action in the allowed in force", f, false)

	f = newFlow()
	f.intent("pay")
	f.intent("pay", "commit")
	c = f.check("commit")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.perform(c)
	run("wider-intent-is-a-proposal", "a later intent that adds an action is a proposal, not in force, until confirmed", f, false)

	f = newFlow()
	f.intent("pay")
	p := f.intent("pay", "commit")
	f.confirm(p, []string{"pay"}, []string{"pay", "commit"})
	c = f.check("commit")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.perform(c)
	run("confirmed-proposal", "a proposal the person confirmed is in force; the standing-intent act lists that confirmation as needing the person's signature", f, true)

	f = newFlow()
	f.intent("pay")
	p = f.intent("pay", "commit")
	f.confirm(p, []string{"pay", "commit"}, []string{"pay", "commit"})
	run("confirmation-with-wrong-previous", "a confirmation's limits.previous is the limits in force", f, false)

	f = newFlow()
	f.intent("pay")
	f.intent("commit")
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.pay(c)
	run("intent-replaces-with-other-action", "an intent narrows by intersection: replacing pay with commit leaves nothing allowed", f, false)

	f = newFlow()
	f.intent("pay")
	c = f.check("pay")
	v = f.verdict(c, "pass")
	f.seal(TypeIntent, false, &dealprofile.IntentBody{VerbatimCommitment: hx('a'), Allowed: []string{}})
	f.approve(v, "standing_intent", true)
	f.pay(c)
	run("allowed-revoked-before-approval", "a standing intent is judged at the approval: the allowed was emptied after the check", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pass")
	f.pay(c)
	f.approve(v, "user", true)
	run("approval-after-act", "an approval sealed after the act does not authorize it", f, false)

	f = newFlow()
	c = f.check("commit")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.pay(c)
	run("action-mismatch", "a payer settle rests on a pay check, not a commit check", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pause")
	a := &dealprofile.ApprovalBody{Choice: "verify_contact", Proceed: false, Approver: "user", SaidCommitment: hx('f'), CardCommitment: f.cards[v.Digest]}
	f.seal(TypeApproval, true, a, rel("approves", v))
	f.pay(c)
	run("choice-not-offered", "the person's choice is one of the verdict's options", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pass")
	a = &dealprofile.ApprovalBody{Choice: "proceed", Proceed: true, Approver: "user", SaidCommitment: hx('f'), CardCommitment: hx('3')}
	f.seal(TypeApproval, true, a, rel("approves", v))
	f.pay(c)
	run("card-not-the-checked-card", "the person's approval commits to the card the verdict rendered", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pass")
	a = &dealprofile.ApprovalBody{Choice: "proceed", Proceed: true, Approver: "user", SaidCommitment: hx('f')}
	f.seal(TypeApproval, true, a, rel("approves", v))
	f.pay(c)
	run("person-approval-without-card", "the person's approval states which card it answered", f, false)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "agent_card", true)
	f.pay(c)
	run("agent-card-needs-person", "an agent_card answer is still the person's approval: it needs the person's signature", f, true)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.seal(TypeEvidence, true, v0Body[dealprofile.EvidenceBody](t, `{"source": "listing_photo", "verified": false, "counterparty_facts": {"domain_age_days": 3}}`))
	f.pay(c)
	run("counterparty-facts-after-check", "evidence with counterparty facts between the check and the act needs a new check", f, false)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "pause"), "standing_intent", true)
	f.pay(c)
	run("standing-intent-on-a-pause", "a standing intent answers only a passing verdict", f, false)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "deny"), "user", true)
	f.pay(c)
	run("deny", "a denying verdict authorizes no act", f, false)

	f = newFlow()
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.seal(TypeDetailChange, true, v0Body[dealprofile.DetailChangeBody](t, `{"source": "counterparty", "changed": ["rail"], "recourse": {"rail": "zelle", "refundable": false}}`))
	f.pay(c)
	run("identifier-changed-after-check", "a detail_change between the check and the act needs a new check", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pass")
	f.verdict(c, "pass")
	f.approve(v, "user", true)
	f.pay(c)
	run("two-verdicts", "a check has exactly one verdict", f, false)

	f = newFlow()
	c = f.check("pay")
	c2 := f.check("pay")
	vb := &dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{}, CardCommitment: cardOf(c)}
	f.seal(TypeVerdict, true, vb, rel("checks", c), rel("checks", c2))
	run("verdict-checks-two", "a verdict answers exactly one check", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pass")
	other := &dealprofile.ApprovalBody{Choice: "proceed", Proceed: true, Approver: "user", SaidCommitment: hx('f'), CardCommitment: f.cards[v.Digest]}
	ob := blk(TypeApproval)
	ob.DealID, ob.At, ob.Refs = "deal-fedcba9876543210", f.at(), []RelRef{rel("approves", v)}
	f.journal = append(f.journal, record(ob, other))
	f.pay(c)
	run("approval-of-another-deal", "an approval answers a verdict of its own deal", f, false)

	f = newFlow()
	f.intent("pay")
	f.seal(TypeIntent, false, &dealprofile.IntentBody{VerbatimCommitment: hx('a'), BoundsCommitment: hx('b')})
	p = f.journal[len(f.journal)-1].digestRef(t)
	f.confirmSets(p, dealprofile.LimitSet{Allowed: []string{"pay"}}, dealprofile.LimitSet{Allowed: []string{"pay"}, BoundsCommitment: hx('b')})
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.pay(c)
	run("floor-confirmed", "a proposal of another floor (bounds_commitment) is confirmed as v0 confirms one", f, true)

	f = newFlow()
	f.intent("pay")
	f.seal(TypeIntent, false, &dealprofile.IntentBody{VerbatimCommitment: hx('a'), Delegated: []string{"item"}, DelegatedCommitment: hx('d')})
	p = f.journal[len(f.journal)-1].digestRef(t)
	f.confirmSets(p, dealprofile.LimitSet{Allowed: []string{"pay"}}, dealprofile.LimitSet{Allowed: []string{"pay"}, Delegated: []string{"item"}})
	c = f.check("pay")
	f.approve(f.verdict(c, "pass"), "standing_intent", true)
	f.pay(c)
	run("delegation-confirmed", "a proposal that hands the agent another choice is confirmed with the delegation in force", f, true)

	f = newFlow()
	f.intent("pay")
	p = f.intent("pay", "commit")
	f.confirmSets(p, dealprofile.LimitSet{Allowed: []string{"pay"}}, dealprofile.LimitSet{Allowed: []string{"pay", "commit"}, OrderedRepresentations: []string{"price"}})
	run("confirmation-adds-a-member", "limits.new is exactly what the proposal asked: a member it did not ask for is refused", f, false)

	f = newFlow()
	c = f.check("pay")
	v = f.verdict(c, "pass")
	ab := &dealprofile.ApprovalBody{Choice: "proceed", Proceed: true, Approver: "user", SaidCommitment: hx('f'), CardCommitment: f.cards[v.Digest]}
	back := blk(TypeApproval)
	back.At, back.Refs = "2026-11-01T10:00:30Z", []RelRef{rel("approves", v)}
	f.journal = append(f.journal, record(back, ab))
	f.pay(c)
	run("at-goes-back", "a journal record's at never decreases, so nothing it relies on is sealed after the act", f, false)

	f = newFlow()
	f.pay(RefTo(hx('9')))
	run("check-not-held", "an act citing a check the journal does not hold is refused", f, false)

	got, err := json.MarshalIndent(map[string]any{
		"description": "approval-migration (evidencebook#461, docs/design/461-deal-profile.md §3.2, invariant D): CheckAuthorizations(journal, acts) must pass the valid cases, giving person_signature per authorized act (its approval is the person's: approver user or agent_card), limit_approvals (how many confirm_limits approvals, each also needing the person's signature, its standing intent rested on) and intent_signature (whether it rests on a first intent the person must have signed: evidencebook#500), and refuse the others at stage authorization. Written by dealprofile/xdealv1's TestApprovalMigration (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	}, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(authDir, "approval-migration.json")
	if os.Getenv("EB_WRITE_DEAL_V1_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(authDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, got), "approval-migration.json differs from this build's; regenerate only for an intended change")
}

// The approval digest binds the check: another check gives another verdict
// digest, so another approval digest.
func TestApprovalDigestBindsTheCheck(t *testing.T) {
	digestFor := func(action string) string {
		f := &authFlow{t: t}
		c := f.check(action)
		f.approve(f.verdict(c, "pass"), "user", true)
		if action == "pay" {
			f.pay(c)
		} else {
			f.perform(c)
		}
		got, err := CheckAuthorizations(f.journal, f.acts)
		require.NoError(t, err)
		require.Len(t, got, 1)
		d, err := Digest(got[0].Approval)
		require.NoError(t, err)
		assert.Equal(t, d, got[0].ApprovalDigest)
		return d
	}
	assert.NotEqual(t, digestFor("pay"), digestFor("commit"))
}

// A body held as a value is read as one held by pointer.
func TestCheckAuthorizationsTakesValueBodies(t *testing.T) {
	f := &authFlow{t: t}
	c := f.check("pay")
	f.approve(f.verdict(c, "pass"), "user", true)
	f.pay(c)
	for i, r := range f.journal {
		switch b := r.Body.(type) {
		case *CheckBody:
			f.journal[i].Body = *b
		case *dealprofile.VerdictBody:
			f.journal[i].Body = *b
		case *dealprofile.ApprovalBody:
			f.journal[i].Body = *b
		}
	}
	f.acts[0].Body = *f.acts[0].Body.(*SettleBody)
	got, err := CheckAuthorizations(f.journal, f.acts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].NeedsPersonSignature)
}

// digestRef is the digest reference to r.
func (r Record) digestRef(t *testing.T) Ref {
	t.Helper()
	d, err := Digest(r)
	require.NoError(t, err)
	return RefTo(d)
}
