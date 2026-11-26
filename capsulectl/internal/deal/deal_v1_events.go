// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	dealapi "github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// The local event list of an x-deal-v1 deal (xdealv1-write-path §12) is
// package deal's step list (Store.Steps, Store.SealJournal). A v1 deal's
// journal records hold commitments of the values its checks read (the
// person's verbatim words, the bounds), so the plugin keeps each deal's plain
// events as the steps' local form, in the same dealEvent form a v0 deal
// folds, and runs the unchanged v0 note and check code over them. The store
// writes each step and the record it carries together, so a run that stops
// between the two is finished by the next read. The list never leaves this
// store: no export, bundle, share or disclosure carries it.

// v1JournalTypes maps the v0 record type a dealEvent builds to the x-deal-v1
// journal record of the same body (PROFILE §4: the journal overlay and the
// journey overlay keep v0's bodies). An event whose v0 record is none of these
// has no v1 note form.
var v1JournalTypes = map[string]bool{
	"intent": true, "check": true, "verdict": true, "approval": true,
	"message": true, "claim": true, "evidence": true, "detail_change": true, "agent_move": true,
	"disclosure": true, "counterparty_profile": true,
}

// v1NonceTypes are the journal records whose digest can leave the holder and
// so carry a fresh nonce of their own (PROFILE §5).
var v1NonceTypes = map[string]bool{"check": true, "verdict": true, "approval": true}

// v1NoteKinds are the `deal note --kind` values a v1 deal takes; the rest name
// their v1 verb (v1NoteRefusal).
var v1NoteKinds = map[string]bool{
	"intent": true, "approval": true, "message": true, "claim": true, "evidence": true, "change": true, "disclosure": true,
	"searched": true, "options": true, "recommended": true, "chose": true, "couldnt": true,
}

// v1NoteRefusal is what a note kind with no v1 form says on a v1 deal, naming
// the verb that records it, or the one that will once it exists.
func v1NoteRefusal(kind, dealID string) error {
	head := "deal " + dealID + " is an x-deal-v1 deal: `deal note --kind " + kind + "` "
	switch kind {
	case "act":
		return cli.InputError(head + "has no x-deal-v1 form; an act is recorded with `deal perform`, and a payment with `deal settle`")
	case "payment_received":
		return cli.InputError(head + "has no x-deal-v1 form; what you received is recorded with `deal settle`")
	case "delivered":
		return cli.InputError(head + "has no x-deal-v1 form; it is recorded with `deal perform`")
	case noteKindDispute:
		return cli.InputError(head + "has no x-deal-v1 form; a dispute is recorded with `deal dispute`")
	case noteKindRuling:
		return cli.InputError(head + "has no x-deal-v1 form; a ruling is the adjudicator's own record, in its case book")
	case "acceptance", "platform_approval":
		return cli.InputError(head + "has no x-deal-v1 form; on x-deal-v1 a sale is an agreed offer (`deal agree`), and the other buyers' offers are declined with `deal offer decline`")
	}
	return cli.InputError(head + "has no x-deal-v1 form and is not recorded on this deal")
}

// v1Events reads a v1 deal's local event list in order (Store.Steps, which
// first appends any record a stopped run stored but did not append).
func (s *dealSession) v1Events(ctx context.Context, dealID string) ([]sealedEvent, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	steps, err := st.Steps(ctx, dealID)
	if err != nil {
		return nil, libErr(err)
	}
	return v1EventsOf(dealID, steps)
}

// v1StoredEvents reads a v1 deal's local event list in order, as stored,
// writing nothing (Store.StoredSteps).
func (s *dealSession) v1StoredEvents(ctx context.Context, dealID string) ([]sealedEvent, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	steps, err := st.StoredSteps(ctx, dealID)
	if err != nil {
		return nil, libErr(err)
	}
	return v1EventsOf(dealID, steps)
}

// v1EventsOf reads the store's steps as this plugin's events. A step written
// without this plugin's local form (by a program that calls package deal
// directly) has none of the values the card reads, so the deal's card is
// refused rather than read short.
func v1EventsOf(dealID string, steps []dealapi.Step) ([]sealedEvent, error) {
	out := make([]sealedEvent, 0, len(steps))
	for _, st := range steps {
		if string(st.Local) == "null" {
			return nil, cli.InputError("deal " + dealID + "'s step " + strconv.FormatInt(st.N, 10) +
				" was written by a program that calls evidencebook's package deal directly, without this command line's local form: its card cannot be read here, so continue the deal with that program")
		}
		var ev dealEvent
		if err := json.Unmarshal(st.Local, &ev); err != nil {
			return nil, fmt.Errorf("%w: deal %s's local event %d: %v", evidencebook.ErrCorrupt, dealID, st.N, err)
		}
		out = append(out, sealedEvent{CapsuleID: v1EventID(dealID, st.N), Digest: st.Digest, Sequence: uint64(st.N), Event: ev})
	}
	return out, nil
}

// v1EventID is a local event's id, which the v0 record builder uses to find
// the event a later one refers to (a verdict's check, an approval's verdict).
// It is 64 lowercase hex, the form of a Capsule id, since the rules checker's
// v0-shaped record chains on it (v1CapsuleWithInput).
func v1EventID(dealID string, n int64) string {
	return digestBytesHex([]byte(fmt.Sprintf("%s/v1/%d", dealID, n)))
}

// sealV1Step is s.seal for a v1 deal: the event is fixed as v0 fixes a step,
// its v0 record is built and rewrapped as the x-deal-v1 journal record of the
// same body, and package deal's writer stores the plain event with the record
// and appends the record to the journal (with a nonce where PROFILE §5 asks
// for one, and deal_id where §3 puts it). The opening has no record: it is
// local.
func (s *dealSession) sealV1Step(ctx context.Context, dealID string, events []sealedEvent, ev dealEvent) (sealedEvent, error) {
	return s.sealV1(ctx, dealID, events, ev, false)
}

// sealV1 is sealV1Step; opening marks the intent the opening carries, which is
// sealed as a plain intent (no journey move), since it repeats the opening's
// own intent and moves nothing: v0's fold then narrows the opening's intent by
// itself, which changes nothing.
func (s *dealSession) sealV1(ctx context.Context, dealID string, events []sealedEvent, ev dealEvent, opening bool) (sealedEvent, error) {
	sealed, err := s.storeV1(ctx, dealID, s.v1Step(dealID, events, ev, opening))
	if err != nil {
		return sealedEvent{}, err
	}
	return sealed[0], nil
}

// v1PendingStep is a step for package deal's writer and the event it fixed
// once built.
type v1PendingStep struct {
	step dealapi.JournalStep
	ev   *dealEvent
}

// v1Step fixes ev as v0 fixes a step, at the place and time the store gives
// it, and builds its record: none for the opening or a check's
// counterparty profile, which stay local (an x-deal-v1 counterparty_profile
// record has no member for the payee's fingerprint; only the checker and the
// cross-deal memory read it).
func (s *dealSession) v1Step(dealID string, events []sealedEvent, ev dealEvent, opening bool) v1PendingStep {
	fixed := new(dealEvent)
	return v1PendingStep{ev: fixed, step: dealapi.JournalStep{Kind: ev.Kind, Build: func(f dealapi.StepFacts) (dealapi.StepParts, error) {
		e, err := s.fixStep(dealID, events, ev)
		if err != nil {
			return dealapi.StepParts{}, err
		}
		if e.N != f.N || e.Prev != f.Prev {
			return dealapi.StepParts{}, fmt.Errorf("%w: deal %s's local events moved while a step was sealed; run the command again", cli.ErrConflict, dealID)
		}
		e.At = f.At
		if opening {
			e.Journey = ""
		}
		local, err := json.Marshal(e)
		if err != nil {
			return dealapi.StepParts{}, err
		}
		*fixed = e
		parts := dealapi.StepParts{Local: local}
		if e.Kind != "open" && e.Kind != "counterparty_profile" {
			if parts.Record, err = s.v1Record(events, e); err != nil {
				return dealapi.StepParts{}, err
			}
		}
		return parts, nil
	}}}
}

// v1LocalStep is a step that is local only and names a record sealed
// elsewhere by its digest (a deal-book act, a refund received): ev is fixed
// at the store's place and keeps the act's own time at.
func (s *dealSession) v1LocalStep(dealID string, events []sealedEvent, ev dealEvent, at, digest string) v1PendingStep {
	fixed := new(dealEvent)
	return v1PendingStep{ev: fixed, step: dealapi.JournalStep{Kind: ev.Kind, Build: func(f dealapi.StepFacts) (dealapi.StepParts, error) {
		e, err := s.fixStep(dealID, events, ev)
		if err != nil {
			return dealapi.StepParts{}, err
		}
		if e.N != f.N || e.Prev != f.Prev {
			return dealapi.StepParts{}, fmt.Errorf("%w: deal %s's local events moved while a step was sealed; run the command again", cli.ErrConflict, dealID)
		}
		e.At = at
		local, err := json.Marshal(e)
		if err != nil {
			return dealapi.StepParts{}, err
		}
		*fixed = e
		return dealapi.StepParts{Local: local, Digest: digest}, nil
	}}}
}

// storeV1 stores steps in one batch through package deal's writer
// (Store.SealJournal: one transaction, then the records' appends, and a
// batch whose append failed before any record reached the journal is
// withdrawn) and returns their events.
func (s *dealSession) storeV1(ctx context.Context, dealID string, steps ...v1PendingStep) ([]sealedEvent, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	js := make([]dealapi.JournalStep, len(steps))
	for i, p := range steps {
		js[i] = p.step
	}
	sealed, err := st.SealJournal(ctx, dealID, js...)
	if err != nil {
		return nil, libErr(err)
	}
	out := make([]sealedEvent, len(sealed))
	for i, step := range sealed {
		out[i] = sealedEvent{CapsuleID: v1EventID(dealID, step.N), Digest: step.Digest, Sequence: uint64(step.N), Event: *steps[i].ev}
	}
	return out, nil
}

// v1Record builds an event's x-deal-v1 journal record: v0's record builder
// gives the body, and the block is the v1 one.
func (s *dealSession) v1Record(events []sealedEvent, ev dealEvent) (*dealapi.JournalRecord, error) {
	// The v0 record is checked as a v0 step is, before anything is stored:
	// the profile's privacy and wording scans against the deal's local values
	// (checkSealable), so a v1 record never carries what a v0 one may not.
	v0payload, _, err := encodeDealRecord(ev, events, s.dkey)
	if err != nil {
		return nil, err
	}
	if err := checkSealable(v0payload, events, ev); err != nil {
		return nil, err
	}
	v0, err := buildDealRecord(ev, events, s.dkey)
	if err != nil {
		return nil, err
	}
	block, _ := v0["x-deal-v0"].(map[string]interface{})
	body, _ := v0["body"].(map[string]interface{})
	rtype, _ := block["record_type"].(string)
	if !v1JournalTypes[rtype] {
		return nil, cli.InputError("this step has no x-deal-v1 journal record (" + rtype + ")")
	}
	// The block members the record keeps from v0's builder; package deal
	// sets its profile, canonicalization, record type, time and deal_id.
	b := map[string]any{}
	for _, k := range []string{"producer", "commit_alg", "refs", "counterparty"} {
		if v, ok := block[k]; ok {
			b[k] = v
		}
	}
	// A reference to the opening (an evidence record's default `about`) is a
	// reference to the deal's first journal record, its intent: the opening
	// has no record of its own.
	if refs, ok := b["refs"].([]interface{}); ok && len(events) > 1 {
		opening, intent := events[0].Digest, events[1].Digest
		for _, r := range refs {
			if m, ok := r.(map[string]interface{}); ok && m["digest"] == opening {
				m["digest"] = intent
			}
		}
	}
	nb := make(map[string]any, len(body)+1)
	for k, v := range body {
		nb[k] = v
	}
	if rtype == "check" && ev.Snapshot != nil && ev.Snapshot.PaymentRef != "" {
		nb["payment_ref"] = ev.Snapshot.PaymentRef
	}
	// The counterparty's History the rules read, by digest (`deal check
	// --history`): v1's own member beside v0's closed body_verdict.
	if rtype == "verdict" && ev.Check != nil && ev.Check.Rules != nil && ev.Check.Rules.ReceivedHistory != nil {
		nb["received_history"] = ev.Check.Rules.ReceivedHistory
	}
	out := &dealapi.JournalRecord{RecordType: rtype, Block: b, Body: nb}
	// The record's nonce, drawn here, where it always was, so a step's draws
	// keep their order.
	if v1NonceTypes[rtype] {
		nonce, err := xdealv1.NewNonce()
		if err != nil {
			return nil, err
		}
		out.Nonce = nonce
	}
	return out, nil
}

// v1OpeningInput is the first intent note's --opening on a v1 deal: the parts
// of v0's `deal open` input that are not the intent (--input) or the terms
// (the offer's).
type v1OpeningInput struct {
	Type     string       `json:"type"`
	Channel  string       `json:"channel,omitempty"`
	Who      dealWho      `json:"who"`
	Recourse dealRecourse `json:"recourse"`
}

// openV1 seals a v1 deal's opening from the first intent note: the synthetic
// opening, local only, and the journal intent it carries (§12 item 1). The
// party role is the deal's own side; the terms are the offer's.
func (s *dealSession) openV1(ctx context.Context, dealID string, raw, opening []byte) ([]sealedEvent, error) {
	if len(opening) == 0 {
		return nil, cli.InputError("the first intent note on an x-deal-v1 deal opens it: --opening FILE gives the deal's type, who and recourse (the terms are the offer's)")
	}
	var in v1OpeningInput
	if err := cli.DecodeJSONAs("--opening", opening, &in); err != nil {
		return nil, err
	}
	var intentIn dealIntent
	if err := cli.DecodeJSONAs("--input", raw, &intentIn); err != nil {
		return nil, err
	}
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
	}
	role := d.Role
	if intentIn.PartyRole != "" && intentIn.PartyRole != role {
		return nil, cli.InputError("intent.party_role is " + intentIn.PartyRole + ", but this profile is the " + role + " in deal " + dealID)
	}
	intentIn.PartyRole = role
	offer, err := s.offerByDigest(ctx, d.OfferDigest)
	if err != nil {
		return nil, err
	}
	if offer == nil {
		return nil, cli.InputError("deal " + dealID + "'s offer is not held here")
	}
	prop, ok := proposeOf(offer.Record)
	if !ok {
		return nil, fmt.Errorf("%w: deal %s's offer is not an x-deal-v1 offer", evidencebook.ErrCorrupt, dealID)
	}
	open := dealOpen{Type: in.Type, Channel: in.Channel, Intent: intentIn, Who: in.Who, Terms: dealTermsOf(prop.Terms), Recourse: in.Recourse}
	if err := normalizeOpen(&open); err != nil {
		return nil, err
	}
	if err := open.validate(); err != nil {
		return nil, err
	}
	predicate, err := cli.PinnedMateriality(s.p)
	if err != nil {
		return nil, err
	}
	open.Materiality = predicate.Ref()
	// The opening and its intent are prepared first and stored together, so
	// a deal never holds an opening without its journal intent.
	opened := s.v1Step(dealID, nil, dealEvent{Kind: "open", Open: &open}, false)
	intent := open.Intent
	// The intent is built after the opening, on the events the opening
	// leaves: its own digest, the digest of its local form.
	var afterOpening []sealedEvent
	first := v1PendingStep{ev: new(dealEvent)}
	first.step = dealapi.JournalStep{Kind: "intent", Build: func(f dealapi.StepFacts) (dealapi.StepParts, error) {
		local, err := json.Marshal(*opened.ev)
		if err != nil {
			return dealapi.StepParts{}, err
		}
		afterOpening = []sealedEvent{{CapsuleID: v1EventID(dealID, opened.ev.N), Digest: digestBytesHex(local), Sequence: uint64(opened.ev.N), Event: *opened.ev}}
		p := s.v1Step(dealID, afterOpening, dealEvent{Kind: "intent", Intent: &intent}, true)
		parts, err := p.step.Build(f)
		*first.ev = *p.ev
		return parts, err
	}}
	return s.storeV1(ctx, dealID, opened, first)
}

// dealTermsOf maps an offer's terms to the plain terms the v0 checks read.
func dealTermsOf(t dealprofile.Terms) dealTerms {
	out := dealTerms{Item: t.Item, PriceMinor: t.PriceMinor, DepositMinor: t.DepositMinor, Currency: t.Currency,
		When: t.When, Place: t.Place, Handover: t.Handover}
	if t.Quantity != nil {
		out.Quantity = *t.Quantity
	}
	if len(t.Conditions) > 0 {
		out.Conditions = make(map[string]string, len(t.Conditions))
		for k, v := range t.Conditions {
			out.Conditions[k] = v
		}
	}
	return out
}

// useV1Note readies a session for `deal note` on a v1 deal: the deal key, the
// local events, and the opening sealed by the first intent note.
func (s *dealSession) useV1Note(ctx context.Context, dealID, kind string, raw, opening []byte) (events []sealedEvent, opened bool, err error) {
	if !v1NoteKinds[kind] {
		return nil, false, v1NoteRefusal(kind, dealID)
	}
	if _, err := s.requireMigrated(ctx); err != nil {
		return nil, false, err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return nil, false, err
	}
	s.dkey = dealprofile.DealKey(s.secret, dealID)
	s.v1 = true
	if events, err = s.v1Events(ctx, dealID); err != nil {
		return nil, false, err
	}
	if len(events) > 0 {
		return events, false, nil
	}
	if kind != "intent" {
		return nil, false, cli.InputError("deal " + dealID + " has no intent yet: seal the user's intent first, with `deal note --deal " + dealID + " --kind intent --input FILE`")
	}
	events, err = s.openV1(ctx, dealID, raw, opening)
	return events, true, err
}

// v1MemorySteps is every v1 deal's local events of kinds, in deal and step
// order, for the cross-deal counterparty memory. A store that never held a v1
// deal has no table and gives none.
func (s *dealSession) v1MemorySteps(ctx context.Context, kinds []string) ([]indexedStep, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='deal_v1_events'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id, n, kind, local, record_digest FROM deal_v1_events ORDER BY deal_id, n`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []indexedStep
	for rows.Next() {
		var st indexedStep
		if err := rows.Scan(&st.DealID, &st.N, &st.Kind, &st.Local, &st.Digest); err != nil {
			return nil, err
		}
		if slices.Contains(kinds, st.Kind) {
			out = append(out, st)
		}
	}
	return out, rows.Err()
}

// digestBytesHex is the SHA-256 of b, hex.
func digestBytesHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
