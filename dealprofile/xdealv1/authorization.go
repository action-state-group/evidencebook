// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/json"
	"fmt"
	"math/big"
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
	// AmountNotHeld says why the act's settled amount was not held to its
	// check, "" when it was or there was none to hold: on a cancel check
	// that states a fee_minor other than 0 and no amount_minor, who bears the fee is not
	// recorded, so the refund owed is not known (evidencebook#719,
	// Sherlock2). A caller shows it, never as a match.
	AmountNotHeld string
	// ApprovalDigest is the approval's record digest, SHA-256 over its JCS
	// bytes. It covers the approval's approves ref (the verdict's digest,
	// which covers its checks ref, the check's digest) and its
	// card_commitment, which a person's approval must carry equal to the
	// verdict's: a signature over it binds the check and the card the person
	// answered. A person-key signature over this digest (PROFILE §8:
	// the person's own key, not the agent's; its member is AAC#627's) travels
	// beside the approval record, never inside it, so adding it changes no
	// record format.
	ApprovalDigest string
	// NeedsPersonSignature says the approval is the person's (approver
	// "user", or "agent_card": the person answered a card the agent
	// composed). The approval record's Capsule is signed by the holder's
	// record key, the agent's: that signature never stands for the person's,
	// so a caller that requires the person's signature refuses the act until
	// one over ApprovalDigest, from the person's own key, is held. It is set
	// too for a standing-intent approval whose verdict carries no rules
	// block (RulesNotRecorded) or rules that did not allow the act on their
	// own (RulesNotEvaluable).
	NeedsPersonSignature bool
	// RulesNotRecorded says the act rests on a standing-intent approval
	// whose verdict carries no rules block: nothing records which rules, if
	// any, evaluated the person's caps for it. Under a standing intent the
	// caps act only through the verdict, so such an act escalates to the
	// person: it needs the person's signature over ApprovalDigest, as a
	// person's approval does, and is never allowed on the standing intent
	// alone (Sherlock2's ruling u234, ops evidence-network/inbox.md:669, 2026-11-25, after u503; evidencebook#899).
	RulesNotRecorded bool
	// RulesNotEvaluable says the act rests on a standing-intent approval
	// whose verdict's rules did not allow the act on their own: the rules
	// were not evaluated, the checker's own verdict is not allow, or a
	// measured finding is not_evaluable, whatever the rules' status
	// (RulesUnevaluated). Such an act
	// escalates to the person as RulesNotRecorded does, whatever the
	// verdict's result says (u503; Sherlock2's ruling u249, ops evidence-network/inbox.md:703, 2026-11-26;
	// evidencebook#982).
	RulesNotEvaluable bool
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

// CheckJournal refuses a journal that x-deal-v1's authorization walk
// refuses (PROFILE §8): the walk CheckActs runs over the whole journal
// before it reads any act, so a record that fails it leaves every later act
// of the deal unauthorizable. A writer runs it over the deal's journal with
// a candidate record appended, before the record is sealed.
func CheckJournal(journal []Record) error {
	_, err := walkJournal(journal)
	return err
}

// CheckAuthorizations checks every act that cites a check. journal is the
// holder's journal records of the deal's task, in journal order; acts are
// its deal-book perform and settle records, in book order. A journal record
// counts for an act when its at is not after the act's. It returns one
// ActAuthorization per act that cites a check, in order; an act that cites
// none is not gated (a payer settle always cites one, which the schema
// requires).
func CheckAuthorizations(journal, acts []Record) ([]ActAuthorization, error) {
	in := make([]ActIn, len(acts))
	for i, a := range acts {
		in[i] = ActIn{Record: a}
	}
	return CheckActs(journal, in)
}

// ActIn is an act as CheckActs reads it: its record and, for a settle, the
// Capsule's settlement member the leg travels in (PROFILE §5), which the
// record itself does not carry.
type ActIn struct {
	Record     Record
	Settlement json.RawMessage
}

// CheckActs is CheckAuthorizations, and also x-deal-v0's rule 5 on the
// amount (PROFILE §8 keeps rules 5 and 6 unchanged): where both the act's
// settlement member and its check carry them, the settled amount and its
// currency equal the checked amount_minor and currency, in the currency's
// minor units (dealprofile.AssetScale). An act given no settlement member is
// read as CheckAuthorizations reads it.
func CheckActs(journal []Record, ins []ActIn) ([]ActAuthorization, error) {
	acts := make([]Record, len(ins))
	for i, in := range ins {
		acts[i] = in.Record
	}
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
		// The kind first, so an act on the wrong check is told so rather than
		// held to that check's amount.
		if want := kind.checkAction(); cb.Action != want {
			return nil, bad("the act differs from the one checked: a %s rests on a %q check, not %q", kind, want, cb.Action)
		}
		if err := settledAmountMatches(ins[i].Settlement, settleLeg(act), cb); err != nil {
			return nil, bad("%v", err)
		}
		notHeld := amountNotHeld(ins[i].Settlement, cb)
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
		a := ActAuthorization{Act: act, Check: journal[ci], Verdict: journal[vi], Approval: journal[ai], AmountNotHeld: notHeld,
			ApprovalDigest: w.digests[ai], NeedsPersonSignature: personApprover(ab.Approver)}
		if ab.Approver == "standing_intent" {
			switch r := w.verdict[vi].Rules; {
			case r == nil:
				a.RulesNotRecorded, a.NeedsPersonSignature = true, true
			case RulesUnevaluated(r):
				a.RulesNotEvaluable, a.NeedsPersonSignature = true, true
			}
			for _, j := range w.limitsAt[ai] {
				a.LimitApprovals = append(a.LimitApprovals, w.digests[j])
			}
			a.Intent = w.digests[w.intentAt[ai]]
		}
		out = append(out, a)
	}
	return out, nil
}

// RulesUnevaluated says a verdict's rules did not allow the act on their own
// (PROFILE §8): their status is not_evaluated; or they were evaluated
// and the checker's own verdict is not allow; or, whatever their status, a
// measured finding is not_evaluable. A finding is measured when it names its
// check: a rule its ruleset only declares, which no check measures, names
// none, and its not_evaluable never moves the verdict. Sealed findings carry
// no reason, so the check is the one member that tells the two apart.
func RulesUnevaluated(r *dealprofile.VerdictBodyRules) bool {
	if r == nil {
		return false
	}
	for _, f := range r.Findings {
		if f.Verdict == "not_evaluable" && f.Check != nil && *f.Check != "" {
			return true
		}
	}
	return r.Status == "not_evaluated" || r.Status == "evaluated" && r.Verdict != "allow"
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
				return nil, bad("its nonce (or verbatim_commitment) is journal record %d's: a nonce is drawn fresh for each record", j+1)
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
			lim.narrow(intentV0(r.Body))
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
			if b, ok := bodyOf[VerdictBody](r.Body); ok {
				w.verdict[i] = &b.VerdictBody
			}
		case TypeApproval:
			v1, ok := bodyOf[ApprovalBody](r.Body)
			if !ok {
				return nil, bad("an approval's body is not an approval")
			}
			b := &v1.ApprovalBody
			w.approval[i] = b
			if b.Choice == ChoiceConfirm {
				j, err := one("approves", TypeIntent)
				if err != nil {
					return nil, err
				}
				prop := intentV0(journal[j].Body)
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

// changesDetails is ChangesDetails for the walk's journal record k.
func (w *journalWalk) changesDetails(k int) bool { return ChangesDetails(w.journal[k]) }

// ChangesDetails says a journal record is one after which no earlier check
// covers an act (v0 §6 rules 5 and 13; dealprofile chain.go changesDetails):
// a detail_change; a message or evidence carrying counterparty identifiers or
// counterparty facts; an intent that starts a leg (a counterparty block) or
// picks the item; an agent's chose move. CheckAuthorizations refuses an act
// after such a record sealed after its check, so a writer holding a payment
// in flight refuses to seal one (capsulectl write-path §13 item 5).
func ChangesDetails(r Record) bool {
	switch r.Block.RecordType {
	case TypeDetailChange:
		return true
	case TypeMessage:
		return r.Block.Counterparty != nil
	case TypeEvidence:
		b, _ := bodyOf[dealprofile.EvidenceBody](r.Body)
		return r.Block.Counterparty != nil || (b != nil && b.CounterpartyFacts != nil)
	case TypeIntent:
		b := intentV0(r.Body)
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

// nonceOf is a journal record's own secret: its nonce, or an intent's
// verbatim_commitment, whose opening holds a fresh nonce; "" for a type that
// carries none.
func nonceOf(r Record) string {
	if b := intentV0(r.Body); b != nil {
		return b.VerbatimCommitment
	}
	if b, ok := bodyOf[AdmitBody](r.Body); ok {
		return b.Nonce
	}
	if b, ok := bodyOf[CheckBody](r.Body); ok {
		return b.Nonce
	}
	if b, ok := bodyOf[VerdictBody](r.Body); ok {
		return b.Nonce
	}
	if b, ok := bodyOf[ApprovalBody](r.Body); ok {
		return b.Nonce
	}
	return ""
}

// settledAmountMatches is rule 5's amount condition for one act, where both
// the leg and the check carry the field. The leg's asset code equals the
// check's currency. The amount the leg settles equals the check's
// amount_minor, compared exactly as AAC compares amounts (in integers at the
// larger scale, draft-mih-agent-settlement-records-01), with amount_minor at
// the currency's minor units (dealprofile.AssetScale). A payer leg settles
// its amount; a payee leg settles received plus receive_fee, AAC's amount
// rule, since received is net of the fee; a payee leg that states no
// receive_fee is refused. A member carrying the other leg's amount field is
// refused.
func settledAmountMatches(member json.RawMessage, leg string, cb *CheckBody) error {
	if cb == nil {
		return nil
	}
	checkedMinor := checkedAmount(cb)
	if len(member) == 0 || (checkedMinor == nil && cb.Currency == "") {
		return nil
	}
	var m struct {
		Amount     *legAmount `json:"amount"`
		Received   *legAmount `json:"received"`
		ReceiveFee *legAmount `json:"receive_fee"`
	}
	if err := json.Unmarshal(member, &m); err != nil {
		return fmt.Errorf("its settlement member: %v", err)
	}
	parts := []*legAmount{m.Amount}
	switch {
	case leg == LegPayer && (m.Received != nil || m.ReceiveFee != nil),
		leg == LegPayee && m.Amount != nil:
		return fmt.Errorf("its settlement member carries the other leg's amount field for a %s leg", leg)
	case leg == LegPayee:
		parts = []*legAmount{m.Received, m.ReceiveFee}
	}
	// A payee leg with no receive_fee settles received plus a fee it does
	// not state, so its settled amount is unknown. AAC reads a missing fee as
	// zero only where it is shown not to apply (an x402 exact scheme, read
	// from wrapped objects this member does not carry), so it is refused
	// here rather than guessed.
	if leg == LegPayee && m.ReceiveFee == nil && m.Received != nil && checkedMinor != nil {
		return fmt.Errorf("its settlement member states no receive_fee, so the amount it settles cannot be held to the checked one: state the fee, 0 when none was taken")
	}
	if parts[0] == nil {
		return nil
	}
	sum, scale := new(big.Int), 0
	for _, a := range parts {
		if a == nil {
			continue
		}
		if cb.Currency != "" && a.AssetCode != cb.Currency {
			return fmt.Errorf("the settled currency %s differs from the checked %s: a new check is needed", a.AssetCode, cb.Currency)
		}
		if a.AssetCode != parts[0].AssetCode {
			return fmt.Errorf("its settlement member mixes %s and %s", parts[0].AssetCode, a.AssetCode)
		}
		v, ok := a.scaled(max(scale, a.AssetScale))
		if !ok {
			return fmt.Errorf("its settlement member's amount %q is not a whole number", a.Value)
		}
		if a.AssetScale > scale {
			sum.Mul(sum, pow10(a.AssetScale-scale))
			scale = a.AssetScale
		}
		sum.Add(sum, v)
	}
	if checkedMinor == nil {
		return nil
	}
	checkScale, ok := dealprofile.AssetScale(parts[0].AssetCode)
	if !ok {
		return fmt.Errorf("the checked amount's currency %s has no known minor units, so the settled amount cannot be held to it", parts[0].AssetCode)
	}
	checked := new(big.Int).SetInt64(*checkedMinor)
	if checkScale > scale {
		sum.Mul(sum, pow10(checkScale-scale))
	} else {
		checked.Mul(checked, pow10(scale-checkScale))
	}
	if sum.Cmp(checked) != 0 {
		return fmt.Errorf("the settled amount differs from the checked %d %s: a new check is needed", *checkedMinor, parts[0].AssetCode)
	}
	return nil
}

// checkedAmount is the amount a settled leg is held to: the check's
// amount_minor, or, on a cancel check that states none and no fee_minor,
// its cancelled_amount_minor (evidencebook#719). A seller's outgoing cancel
// records the amount there (x-deal-v0's sealCancelAmount moves it there when
// the cancel returns no payment of the holder's own), and the refund leg
// answering it settles that amount, as capsulectl-deal builds it. With a
// fee_minor beside it, whether the refund is net of the fee is not settled
// (x-deal-v0 keeps a cancellation fee apart from the cancelled amount), so
// no amount is held then.
func checkedAmount(cb *CheckBody) *int64 {
	if cb.AmountMinor == nil && cb.Action == "cancel" && !hasFee(cb) {
		return cb.CancelledAmountMinor
	}
	return cb.AmountMinor
}

// hasFee says a check states a fee other than zero (fee_minor 0 is no fee).
func hasFee(cb *CheckBody) bool { return cb.FeeMinor != nil && *cb.FeeMinor != 0 }

// notHeldFee is ActAuthorization.AmountNotHeld's reason on a cancel check
// that states a fee and no amount_minor.
const notHeldFee = "fee present: who bears it is not recorded, so the refund owed is not known and its amount is not checked"

// amountNotHeld is ActAuthorization.AmountNotHeld for a settlement member
// that states an amount, on cb.
func amountNotHeld(member json.RawMessage, cb *CheckBody) string {
	if len(member) == 0 || cb == nil || cb.Action != "cancel" || cb.AmountMinor != nil || !hasFee(cb) || cb.CancelledAmountMinor == nil {
		return ""
	}
	var m struct {
		Amount   *legAmount `json:"amount"`
		Received *legAmount `json:"received"`
	}
	if json.Unmarshal(member, &m) != nil || (m.Amount == nil && m.Received == nil) {
		return ""
	}
	return notHeldFee
}

// legAmount is a settlement leg's amount (draft-mih-agent-settlement-records-01).
type legAmount struct {
	Value      string `json:"value"`
	AssetCode  string `json:"assetCode"`
	AssetScale int    `json:"assetScale"`
}

// scaled is the amount as an integer at scale, which is at least its own.
func (a *legAmount) scaled(scale int) (*big.Int, bool) {
	v, ok := new(big.Int).SetString(a.Value, 10)
	if !ok || v.Sign() < 0 || a.AssetScale < 0 || a.AssetScale > 255 {
		return nil, false
	}
	return v.Mul(v, pow10(scale-a.AssetScale)), true
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

// settleLeg is a settle record's leg; empty for any other record.
func settleLeg(r Record) string {
	if b, ok := bodyOf[SettleBody](r.Body); ok {
		return b.Leg
	}
	return ""
}

// intentV0 is an intent record's v0 body, the part the limits, the
// confirmations and the detail changes read; nil for any other body. A
// caller may still build an intent record in memory with v0's body, which
// encodes as the same intent, so it is read too: dropping it would leave
// the limits unset, which allows everything.
func intentV0(b Body) *dealprofile.IntentBody {
	if v1, ok := bodyOf[IntentBody](b); ok {
		return &v1.IntentBody
	}
	if v0, ok := bodyOf[dealprofile.IntentBody](b); ok {
		return v0
	}
	return nil
}
