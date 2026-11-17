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
	vb := &dealprofile.VerdictBody{Result: result, Differences: []dealprofile.Difference{}, Options: []string{}}
	if result != "pass" {
		vb.Differences = []dealprofile.Difference{{Question: "terms", Rule: "limit"}}
		vb.Options = []string{"hold", "proceed"}
		if result == "deny" {
			vb.Options = []string{"hold"}
			vb.Rules = &dealprofile.VerdictBodyRules{Status: "not_configured"}
		}
	}
	return f.seal(TypeVerdict, true, vb, rel("checks", check))
}

func (f *authFlow) approve(verdict Ref, approver string, proceed bool) Ref {
	choice := "hold"
	if proceed {
		choice = "proceed"
	}
	a := &dealprofile.ApprovalBody{Choice: choice, Proceed: proceed, Approver: approver}
	if approver == "user" {
		a.SaidCommitment = hx('f')
	}
	return f.seal(TypeApproval, true, a, rel("approves", verdict))
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
	run("standing-intent-in-allowed", "a standing intent answers a passing verdict for an action in the allowed in force; no person signature", f, true)

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
	f.pay(c)
	run("wider-intent-is-a-proposal", "a later intent that adds an action is a proposal, not in force, until confirmed", f, false)

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
	f.pay(RefTo(hx('9')))
	run("check-not-held", "an act citing a check the journal does not hold is refused", f, false)

	got, err := json.MarshalIndent(map[string]any{
		"description": "approval-migration (evidencebook#461, docs/design/461-deal-profile.md §3.2, invariant D): CheckAuthorizations(journal, acts) must pass the valid cases, giving person_signature per authorized act (its approval is the person's own, approver user), and refuse the others at stage authorization. Written by dealprofile/xdealv1's TestApprovalMigration (EB_WRITE_DEAL_V1_VECTORS=1).",
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
		f.pay(c)
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
