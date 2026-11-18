// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile"
)

// Authorization (design §3.2, invariant D): an act is a deal-book perform or
// settle that cites, by digest, the check in the holder's journal it rests
// on. v0's authorization conditions hold unchanged (x-deal-v0 PROFILE.md §6,
// rules 5 and 6; dealprofile chain_steps.go and chain.go), with that citation
// in place of v0's authorized_by. The journal is read in order, as v0 reads a
// chain: the limits in force change only by an intent that narrows them and
// by a confirmed proposal, and each approval is judged at its own position.

// StageAuthorization is the authorization of acts (CheckAuthorizations).
const StageAuthorization = "authorization"

// ActAuthorization is what authorizes one act.
type ActAuthorization struct {
	Act, Check, Verdict, Approval Record
	// ApprovalDigest is the approval's record digest, SHA-256 over its JCS
	// bytes. It covers the approval's approves ref (the verdict's digest,
	// which covers its checks ref, the check's digest) and its
	// card_commitment, which a person's approval must carry equal to the
	// verdict's: a signature over it binds the check and the card the person
	// answered. A person-key signature over this digest (Steven db13b75b,
	// u599; its member is AAC#627's) travels beside the approval record,
	// never inside it, so adding it changes no record format.
	ApprovalDigest string
	// NeedsPersonSignature says the approval is the person's (approver
	// "user", or "agent_card": the person answered a card the agent
	// composed). The approval record's Capsule is signed by the holder's
	// record key, the agent's: that signature never stands for the person's,
	// so a caller that requires the person's signature refuses the act until
	// one over ApprovalDigest, from the person's own key, is held.
	NeedsPersonSignature bool
	// LimitApprovals are the record digests of the confirm_limits approvals
	// whose confirmed proposals put in force the allowed a standing-intent
	// approval relied on. Each is the person's own approval and needs the
	// person's signature over its digest, as ApprovalDigest does: otherwise
	// the agent could widen its own limits.
	LimitApprovals []string
	// Intent is the record digest of the first intent a standing-intent
	// approval rests on, the person's own words and limits: Steven ruled that
	// such an act is refused until that intent carries the person's
	// signature over its digest (evidencebook#500, option b), as
	// ApprovalDigest does. "" for an approval that is not a standing intent.
	Intent string
}

// CheckAuthorizations checks every act that cites a check. journal is the
// holder's journal records of the deal's task, in journal order; acts are
// its deal-book perform and settle records, in book order. A journal record
// counts for an act when its at is not after the act's. It returns one
// ActAuthorization per act that cites a check, in order; an act that cites
// none is not gated (a payer settle always cites one, which the schema
// requires).
func CheckAuthorizations(journal, acts []Record) ([]ActAuthorization, error) {
	w, err := walkJournal(journal)
	if err != nil {
		return nil, err
	}
	used := map[int]int{}
	var out []ActAuthorization
	for i, act := range acts {
		bad := func(format string, args ...any) error {
			return stageErr(StageAuthorization, "act %d: %s", i+1, fmt.Sprintf(format, args...))
		}
		checkRef, kind, ok := actCheck(act)
		if !ok {
			return nil, bad("a %s is not an act", act.Block.RecordType)
		}
		if checkRef == nil {
			continue
		}
		actAt, err := parseAt(act)
		if err != nil {
			return nil, bad("%v", err)
		}
		ci := slices.Index(w.digests, checkRef.Digest)
		if ci < 0 || journal[ci].Block.RecordType != TypeCheck {
			return nil, bad("it cites no check the journal holds")
		}
		if journal[ci].Block.DealID != act.Block.DealID {
			return nil, bad("its check serves another deal")
		}
		cb := w.check[ci]
		if want := kind.checkAction(); cb.Action != want {
			return nil, bad("the act differs from the one checked: a %s rests on a %q check, not %q", kind, want, cb.Action)
		}
		vi, ok := w.verdictOf[ci]
		if !ok {
			return nil, bad("its check has no verdict")
		}
		ai, ok := w.firstAnswer[vi]
		if !ok {
			return nil, bad("its verdict has no sealed approval")
		}
		ab := w.approval[ai]
		approvalAt, _ := parseAt(journal[ai])
		switch {
		case approvalAt.After(actAt):
			return nil, bad("its approval was sealed after it")
		case ab.Choice == ChoiceConfirm:
			return nil, bad("a confirm_limits approval authorizes no act")
		case !ab.Proceed:
			return nil, bad("its verdict's first approval does not proceed")
		case w.verdict[vi].Result == "deny":
			return nil, bad("its verdict denies")
		}
		if prev, dup := used[ai]; dup {
			return nil, bad("its approval already authorized act %d", prev+1)
		}
		used[ai] = i
		for k := ci + 1; k < len(journal); k++ {
			at, _ := parseAt(journal[k])
			if !at.After(actAt) && w.changesDetails(k) {
				return nil, bad("journal record %d (%s) changed the details after its check: a new check is needed", k+1, journal[k].Block.RecordType)
			}
		}
		a := ActAuthorization{Act: act, Check: journal[ci], Verdict: journal[vi], Approval: journal[ai],
			ApprovalDigest: w.digests[ai], NeedsPersonSignature: personApprover(ab.Approver)}
		if ab.Approver == "standing_intent" {
			for _, j := range w.limitsAt[ai] {
				a.LimitApprovals = append(a.LimitApprovals, w.digests[j])
			}
			a.Intent = w.digests[w.intentAt[ai]]
		}
		out = append(out, a)
	}
	return out, nil
}

// personApprover says an approver is the person: "user" answers in their
// own words, "agent_card" answers a card the agent composed. A standing
// intent is the agent applying the person's limits, with no act of the
// person's at the time.
func personApprover(approver string) bool { return approver == "user" || approver == "agent_card" }

// actKind is what an act does, and so the action its check names.
type actKind string

const (
	actPay     actKind = "payer settle"
	actRefund  actKind = "refund"
	actPerform actKind = "perform"
)

// checkAction is the check action an act rests on: a payer leg on a pay
// check, a refund (a payer leg that reverses a payment) on a cancel check,
// and a perform on a commit check.
func (k actKind) checkAction() string {
	switch k {
	case actPay:
		return "pay"
	case actRefund:
		return "cancel"
	}
	return "commit"
}

// actCheck is the check an act cites (nil for none) and its kind; ok is
// false for a record that is not an act.
func actCheck(r Record) (check *Ref, kind actKind, ok bool) {
	if b, isPerform := bodyOf[PerformBody](r.Body); isPerform {
		return b.Check, actPerform, true
	}
	if b, isSettle := bodyOf[SettleBody](r.Body); isSettle {
		if b.Reverses != nil {
			return b.Check, actRefund, true
		}
		return b.Check, actPay, true
	}
	return nil, "", false
}

func parseAt(r Record) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, r.Block.At)
	if err != nil {
		return time.Time{}, fmt.Errorf("its at %q: %v", r.Block.At, err)
	}
	return t, nil
}

// journalWalk is the journal read in order, as v0 reads a chain.
type journalWalk struct {
	journal []Record
	digests []string
	check   map[int]*CheckBody
	verdict map[int]*dealprofile.VerdictBody
	// approval holds each approval's body.
	approval map[int]*dealprofile.ApprovalBody
	// verdictOf is a check's one verdict; firstAnswer a verdict's first
	// approval.
	verdictOf, firstAnswer map[int]int
	// limitsAt is, for each standing-intent approval, the confirm_limits
	// approvals whose proposals were in force at it.
	limitsAt map[int][]int
	// intentAt is, for each standing-intent approval, the first intent of
	// the journal before it.
	intentAt map[int]int
}

func walkJournal(journal []Record) (*journalWalk, error) {
	w := &journalWalk{journal: journal, check: map[int]*CheckBody{}, verdict: map[int]*dealprofile.VerdictBody{},
		approval: map[int]*dealprofile.ApprovalBody{}, verdictOf: map[int]int{}, firstAnswer: map[int]int{}, limitsAt: map[int][]int{}, intentAt: map[int]int{}}
	byDigest := map[string]int{}
	for i, r := range journal {
		d, err := Digest(r)
		if err != nil {
			return nil, err
		}
		w.digests = append(w.digests, d)
		byDigest[d] = i
	}
	var lim limits
	confirmed := map[int]bool{}
	var confirmations []int
	firstIntent := -1
	var last time.Time
	nonces := map[string]int{}
	for i, r := range journal {
		bad := func(format string, args ...any) error {
			return stageErr(StageAuthorization, "journal record %d: %s", i+1, fmt.Sprintf(format, args...))
		}
		// A nonce is drawn fresh for each record (PROFILE §5): two records
		// sharing one show it was not, and the citing records' digests
		// may again test guesses.
		if n := nonceOf(r); n != "" {
			if j, seen := nonces[n]; seen {
				return nil, bad("its nonce is journal record %d's: a nonce is drawn fresh for each record", j+1)
			}
			nonces[n] = i
		}
		// at never decreases (v0 rule 3), so a record the act relies on is
		// never sealed after a later record that predates the act.
		at, err := parseAt(r)
		if err != nil {
			return nil, bad("%v", err)
		}
		if at.Before(last) {
			return nil, bad("its at is earlier than the record before it: at never decreases")
		}
		last = at
		// one is the one earlier record r cites with rel, of type t.
		one := func(rel, t string) (int, error) {
			var js []int
			for _, ref := range r.Block.Refs {
				if ref.Rel == rel {
					j, ok := byDigest[ref.Digest]
					if !ok || j >= i || journal[j].Block.RecordType != t {
						return 0, bad("its %s ref names no earlier %s", rel, t)
					}
					js = append(js, j)
				}
			}
			if len(js) != 1 {
				return 0, bad("a %s carries exactly one %s ref", r.Block.RecordType, rel)
			}
			return js[0], nil
		}
		switch r.Block.RecordType {
		case TypeIntent:
			b, _ := bodyOf[dealprofile.IntentBody](r.Body)
			lim.narrow(b)
			if firstIntent < 0 {
				firstIntent = i
			}
		case TypeCheck:
			b, _ := bodyOf[CheckBody](r.Body)
			w.check[i] = b
		case TypeVerdict:
			j, err := one("checks", TypeCheck)
			if err != nil {
				return nil, err
			}
			if journal[j].Block.DealID != r.Block.DealID {
				return nil, bad("a verdict answers a check of its own deal")
			}
			if _, dup := w.verdictOf[j]; dup {
				return nil, bad("a check has one verdict")
			}
			w.verdictOf[j] = i
			w.verdict[i], _ = bodyOf[dealprofile.VerdictBody](r.Body)
		case TypeApproval:
			b, _ := bodyOf[dealprofile.ApprovalBody](r.Body)
			w.approval[i] = b
			if b.Choice == ChoiceConfirm {
				j, err := one("approves", TypeIntent)
				if err != nil {
					return nil, err
				}
				prop, _ := bodyOf[dealprofile.IntentBody](journal[j].Body)
				if err := lim.confirm(b, prop, confirmed[j] || intentBetween(journal, j, i)); err != nil {
					return nil, bad("%v", err)
				}
				if b.Proceed && !confirmed[j] && !intentBetween(journal, j, i) {
					confirmed[j] = true
					confirmations = append(confirmations, i)
				}
				continue
			}
			j, err := one("approves", TypeVerdict)
			if err != nil {
				return nil, err
			}
			if journal[j].Block.DealID != r.Block.DealID {
				return nil, bad("an approval answers a verdict of its own deal")
			}
			if _, answered := w.firstAnswer[j]; !answered {
				w.firstAnswer[j] = i
			}
			v := w.verdict[j]
			chk := w.checkOfVerdict(j)
			switch {
			case b.Approver == "standing_intent":
				if v.Result != "pass" {
					return nil, bad("a standing-intent approval needs a passing verdict; a pause needs the person's answer")
				}
				if chk != nil && !lim.allows(chk.Action) {
					return nil, bad("standing-intent approval for an action the limits in force do not allow")
				}
				if firstIntent < 0 {
					return nil, bad("a standing-intent approval rests on an intent; the journal holds none before it")
				}
				w.limitsAt[i] = slices.Clone(confirmations)
				w.intentAt[i] = firstIntent
			case v.Result == "pause" || v.Result == "deny":
				if !slices.Contains(v.Options, b.Choice) {
					return nil, bad("the choice is not one of the verdict's options")
				}
			}
			// The card the answer was given on: equal commitments mean the
			// card shown is the card checked. The person's answer must show
			// which card it answered.
			switch {
			case b.CardCommitment != "" && v.CardCommitment == "":
				return nil, bad("the approval commits to a shown card, but the verdict it answers rendered none")
			case b.CardCommitment != "" && b.CardCommitment != v.CardCommitment:
				return nil, bad("the card shown is not the card checked: the approval's card_commitment differs from the verdict's")
			case personApprover(b.Approver) && b.CardCommitment == "":
				return nil, bad("the person's approval commits to the card it answered (card_commitment)")
			}
		}
	}
	return w, nil
}

// checkOfVerdict is the check a verdict answers.
func (w *journalWalk) checkOfVerdict(vi int) *CheckBody {
	for ci, v := range w.verdictOf {
		if v == vi {
			return w.check[ci]
		}
	}
	return nil
}

// intentBetween says an intent was sealed after the proposal at j and
// before i: the proposal was replaced.
func intentBetween(journal []Record, j, i int) bool {
	for k := j + 1; k < i; k++ {
		if journal[k].Block.RecordType == TypeIntent {
			return true
		}
	}
	return false
}

// changesDetails is a journal record after which no earlier check covers an
// act (v0 §6 rules 5 and 13; dealprofile chain.go changesDetails): a
// detail_change; a message or evidence carrying counterparty identifiers or
// counterparty facts; an intent that starts a leg (a counterparty block) or
// picks the item; an agent's chose move.
func (w *journalWalk) changesDetails(k int) bool {
	r := w.journal[k]
	switch r.Block.RecordType {
	case TypeDetailChange:
		return true
	case TypeMessage:
		return r.Block.Counterparty != nil
	case TypeEvidence:
		b, _ := bodyOf[dealprofile.EvidenceBody](r.Body)
		return r.Block.Counterparty != nil || (b != nil && b.CounterpartyFacts != nil)
	case TypeIntent:
		b, _ := bodyOf[dealprofile.IntentBody](r.Body)
		return r.Block.Counterparty != nil || (b != nil && b.Move != "" && b.Asked != nil && b.Asked.Item != "")
	case TypeAgentMove:
		b, _ := bodyOf[dealprofile.AgentMoveBody](r.Body)
		return b != nil && b.Move == "chose"
	}
	return false
}

// limits are the limits in force (v0 rule 6; dealprofile chain.go
// limitsInForce): the first intent's, narrowed by later intents, and
// widened only by a confirmed proposal. A floor is held only as its
// commitment (bounds), and an intent that states another one makes the floor
// in force unknown (boundsKnown false) until a confirmation states it.
type limits struct {
	seen        bool
	allowed     []string
	set         bool
	maxTotal    *int64
	delegated   []string
	bounds      string
	boundsKnown bool
}

func (l *limits) narrow(b *dealprofile.IntentBody) {
	if b == nil {
		return
	}
	if !l.seen {
		l.seen, l.boundsKnown = true, true
		l.allowed, l.set, l.maxTotal = slices.Clone(b.Allowed), b.Allowed != nil, b.MaxTotalMinor
		l.delegated, l.bounds = slices.Clone(b.Delegated), b.BoundsCommitment
		return
	}
	if b.MaxTotalMinor != nil && (l.maxTotal == nil || *b.MaxTotalMinor < *l.maxTotal) {
		l.maxTotal = b.MaxTotalMinor
	}
	if b.BoundsCommitment != "" && b.BoundsCommitment != l.bounds {
		l.boundsKnown = false
	}
	if b.Allowed != nil {
		next := []string{}
		for _, a := range b.Allowed {
			if !l.set || slices.Contains(l.allowed, a) {
				next = append(next, a)
			}
		}
		l.allowed, l.set = next, true
	}
	// Naming the item, or picking an option, takes the item back from the
	// agent's delegation.
	if (b.Asked != nil && b.Asked.Item != "") || b.Move == "picked" {
		l.delegated = slices.DeleteFunc(slices.Clone(l.delegated), func(d string) bool { return d == "item" })
	}
}

func (l *limits) allows(action string) bool { return !l.set || slices.Contains(l.allowed, action) }

// inForce is the limits in force as a confirmation's limits.previous states
// them.
func (l *limits) inForce() dealprofile.LimitSet {
	out := dealprofile.LimitSet{MaxTotalMinor: l.maxTotal, Allowed: l.allowedOrNil()}
	if len(l.delegated) > 0 {
		out.Delegated = l.delegated
	}
	if l.boundsKnown {
		out.BoundsCommitment = l.bounds
	}
	return out
}

// withoutUnknownBounds drops a stated floor while the floor in force is
// unknown: it cannot be compared.
func (l *limits) withoutUnknownBounds(set dealprofile.LimitSet) dealprofile.LimitSet {
	if !l.boundsKnown {
		set.BoundsCommitment = ""
	}
	return set
}

// confirm applies a confirm_limits approval of the proposal prop (v0
// chain_steps.go confirmLimits): a late answer (the proposal replaced or
// already confirmed) does not proceed and changes nothing; otherwise it
// proceeds, its limits.previous are the limits in force, its limits.new
// are what the proposal asked, and the proposal asks for more.
func (l *limits) confirm(b *dealprofile.ApprovalBody, prop *dealprofile.IntentBody, late bool) error {
	if late {
		if b.Proceed {
			return fmt.Errorf("a proposal replaced by a later intent, or already confirmed, cannot be confirmed (proceed must be false)")
		}
		return nil
	}
	if !b.Proceed {
		return fmt.Errorf("a confirmation of a standing proposal proceeds")
	}
	if b.Limits == nil || prop == nil {
		return fmt.Errorf("a confirmation states the limits it replaces and puts in force")
	}
	if !sameJSON(l.withoutUnknownBounds(b.Limits.Previous), l.inForce()) {
		return fmt.Errorf("limits.previous is not the limits in force")
	}
	next := l.inForce()
	if prop.MaxTotalMinor != nil {
		next.MaxTotalMinor = prop.MaxTotalMinor
	}
	if prop.Allowed != nil {
		next.Allowed = prop.Allowed
	}
	if prop.BoundsCommitment != "" {
		next.BoundsCommitment = prop.BoundsCommitment
	}
	var added []string
	for _, d := range prop.Delegated {
		if !slices.Contains(l.delegated, d) {
			added = append(added, d)
		}
	}
	if len(added) > 0 {
		next.Delegated = append(slices.Clone(l.delegated), added...)
	}
	got := b.Limits.New
	if prop.BoundsCommitment == "" {
		got = l.withoutUnknownBounds(got)
	}
	if !sameJSON(got, next) {
		return fmt.Errorf("limits.new is not what the intent proposed")
	}
	more := (prop.MaxTotalMinor != nil && l.maxTotal != nil && *prop.MaxTotalMinor > *l.maxTotal) ||
		(prop.Allowed != nil && l.set && !subset(prop.Allowed, l.allowed)) ||
		(prop.BoundsCommitment != "" && prop.BoundsCommitment != l.bounds) ||
		len(added) > 0
	if !more {
		return fmt.Errorf("the intent asks for no higher limit and no new action: nothing to confirm")
	}
	l.maxTotal, l.allowed, l.set = next.MaxTotalMinor, slices.Clone(next.Allowed), next.Allowed != nil
	l.delegated = slices.Clone(next.Delegated)
	l.bounds, l.boundsKnown = b.Limits.New.BoundsCommitment, true
	return nil
}

func (l *limits) allowedOrNil() []string {
	if !l.set {
		return nil
	}
	if l.allowed == nil {
		return []string{}
	}
	return l.allowed
}

func sameJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// nonceOf is a journal record's nonce, "" for a type that carries none.
func nonceOf(r Record) string {
	if b, ok := bodyOf[AdmitBody](r.Body); ok {
		return b.Nonce
	}
	if b, ok := bodyOf[CheckBody](r.Body); ok {
		return b.Nonce
	}
	return ""
}
