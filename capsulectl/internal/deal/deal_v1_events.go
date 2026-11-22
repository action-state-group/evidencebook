// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// The local event list of an x-deal-v1 deal (xdealv1-write-path §12). A v1
// deal's journal records hold commitments of the values its checks read (the
// person's verbatim words, the bounds), so the plugin keeps each deal's plain
// events here, in the same dealEvent form a v0 deal folds, and runs the
// unchanged v0 note and check code over them. Each event that has a v1 record
// carries that record's digest and payload; the row is written before the
// record is sealed, so a run that stops between the two is finished by the
// next read (v1Events). The list never leaves this store: no export, bundle,
// share or disclosure carries it.
const dealV1EventsTable = `CREATE TABLE IF NOT EXISTS deal_v1_events (
	deal_id TEXT NOT NULL,
	n INTEGER NOT NULL,
	kind TEXT NOT NULL,
	local TEXT NOT NULL,
	record_digest TEXT NOT NULL,
	payload BLOB,
	PRIMARY KEY (deal_id, n)
)`

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
	"intent": true, "message": true, "claim": true, "evidence": true, "change": true, "disclosure": true,
	"searched": true, "options": true, "recommended": true, "chose": true, "couldnt": true,
}

// v1NoteRefusal is what a note kind with no v1 form says on a v1 deal.
func v1NoteRefusal(kind, dealID string) error {
	verb := map[string]string{
		"act": "`deal perform` or `deal settle`", "payment_received": "`deal settle`", "delivered": "`deal perform`",
		noteKindDispute: "`deal dispute`", noteKindRuling: "the adjudicator's own case book",
		"acceptance": "the sale verbs", "platform_approval": "the sale verbs",
		"approval": "`deal note --kind approval` once `deal check` serves x-deal-v1 deals (evidencebook#626)",
	}[kind]
	if verb == "" {
		verb = "no note"
	}
	return cli.InputError("deal " + dealID + " is an x-deal-v1 deal: `deal note --kind " + kind + "` has no x-deal-v1 form; it is recorded with " + verb)
}

// v1Events reads a v1 deal's local event list in order, sealing any record a
// stopped run stored but did not seal.
func (s *dealSession) v1Events(ctx context.Context, dealID string) ([]sealedEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n, local, record_digest, payload FROM deal_v1_events WHERE deal_id=? ORDER BY n`, dealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sealedEvent
	var pending [][]byte
	var pendingDigests []string
	for rows.Next() {
		var (
			n       int64
			local   string
			digest  string
			payload []byte
		)
		if err := rows.Scan(&n, &local, &digest, &payload); err != nil {
			return nil, err
		}
		var ev dealEvent
		if err := json.Unmarshal([]byte(local), &ev); err != nil {
			return nil, fmt.Errorf("%w: deal %s's local event %d: %v", evidencebook.ErrCorrupt, dealID, n, err)
		}
		out = append(out, sealedEvent{CapsuleID: v1EventID(dealID, n), Digest: digest, Sequence: uint64(n), Event: ev})
		if len(payload) > 0 {
			pending, pendingDigests = append(pending, payload), append(pendingDigests, digest)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, payload := range pending {
		held, err := s.journalHolds(ctx, pendingDigests[i])
		if err != nil {
			return nil, err
		}
		if !held {
			if err := s.appendV1Journal(ctx, payload); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// v1EventID is a local event's id, which the v0 record builder uses to find
// the event a later one refers to (a verdict's check, an approval's verdict).
func v1EventID(dealID string, n int64) string { return fmt.Sprintf("%s/v1/%d", dealID, n) }

// sealV1Step is s.seal for a v1 deal: the event is fixed as v0 fixes a step,
// its v0 record is built and rewrapped as the x-deal-v1 journal record of the
// same body (with a nonce where PROFILE §5 asks for one, and deal_id where §3
// puts it), the plain event and the record's payload are stored, and the
// record is appended to the journal. The opening has no record: it is local.
func (s *dealSession) sealV1Step(ctx context.Context, dealID string, events []sealedEvent, ev dealEvent) (sealedEvent, error) {
	return s.sealV1(ctx, dealID, events, ev, false)
}

// sealV1 is sealV1Step; opening marks the intent the opening carries, which is
// sealed as a plain intent (no journey move), since it repeats the opening's
// own intent and moves nothing: v0's fold then narrows the opening's intent by
// itself, which changes nothing.
func (s *dealSession) sealV1(ctx context.Context, dealID string, events []sealedEvent, ev dealEvent, opening bool) (sealedEvent, error) {
	ev, err := s.fixStep(dealID, events, ev)
	if err != nil {
		return sealedEvent{}, err
	}
	if opening {
		ev.Journey = ""
	}
	var payload []byte
	digest := ""
	if ev.Kind != "open" {
		if payload, digest, err = s.v1Record(events, ev); err != nil {
			return sealedEvent{}, err
		}
	}
	local, err := json.Marshal(ev)
	if err != nil {
		return sealedEvent{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, ?, ?, ?, ?)`,
		dealID, ev.N, ev.Kind, string(local), digest, payload); err != nil {
		return sealedEvent{}, err
	}
	if payload != nil {
		if err := s.appendV1Journal(ctx, payload); err != nil {
			return sealedEvent{}, err
		}
	}
	return sealedEvent{CapsuleID: v1EventID(dealID, ev.N), Digest: digest, Sequence: uint64(ev.N), Event: ev}, nil
}

// v1Record builds an event's x-deal-v1 journal record: v0's record builder
// gives the body, and the block is the v1 one.
func (s *dealSession) v1Record(events []sealedEvent, ev dealEvent) ([]byte, string, error) {
	v0, err := buildDealRecord(ev, events, s.dkey)
	if err != nil {
		return nil, "", err
	}
	block, _ := v0["x-deal-v0"].(map[string]interface{})
	body, _ := v0["body"].(map[string]interface{})
	rtype, _ := block["record_type"].(string)
	if !v1JournalTypes[rtype] {
		return nil, "", cli.InputError("this step has no x-deal-v1 journal record (" + rtype + ")")
	}
	b := map[string]interface{}{"profile": xdealv1.Profile, "canonicalization": xdealv1.Canonicalization, "record_type": rtype, "at": ev.At}
	for _, k := range []string{"producer", "commit_alg", "refs", "counterparty"} {
		if v, ok := block[k]; ok {
			b[k] = v
		}
	}
	// deal_id where PROFILE §3 puts it: never on an intent, a disclosure, a
	// counterparty profile or a confirm_limits approval, which span deals;
	// on every other journal record of a v1 deal, whose offer exists.
	switch {
	case rtype == "intent", rtype == "disclosure", rtype == "counterparty_profile":
	case rtype == "approval" && body["choice"] == "confirm_limits":
	default:
		b["deal_id"] = ev.DealID
	}
	nb := make(map[string]interface{}, len(body)+1)
	for k, v := range body {
		nb[k] = v
	}
	if v1NonceTypes[rtype] {
		nonce, err := xdealv1.NewNonce()
		if err != nil {
			return nil, "", err
		}
		nb["nonce"] = nonce
	}
	rec := map[string]interface{}{xdealv1.Profile: b, "body": nb}
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, "", err
	}
	if err := xdealv1.CheckSealed(raw, xdealv1.SealContext{OfferExists: true}); err != nil {
		return nil, "", cli.InputError("the x-deal-v1 " + rtype + " record does not pass the profile's checks: " + err.Error())
	}
	return xdealv1.Encode(raw)
}

// appendV1JournalHook lets a test stop a run between storing a local event and
// appending its record.
var appendV1JournalHook func() error

// appendV1Journal appends one journal record. A record stored but not yet in
// the log is recovered by the next Journal open (Shelf.Journal).
func (s *dealSession) appendV1Journal(ctx context.Context, payload []byte) error {
	if appendV1JournalHook != nil {
		if err := appendV1JournalHook(); err != nil {
			return err
		}
	}
	j, err := s.shelf.Journal(ctx)
	if err != nil {
		return err
	}
	_, err = j.Append(ctx, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}})
	return err
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
	prop, _ := proposeOf(offer.Record)
	open := dealOpen{Type: in.Type, Channel: in.Channel, Intent: intentIn, Who: in.Who, Terms: dealTermsOf(prop.Terms), Recourse: in.Recourse}
	if err := open.validate(); err != nil {
		return nil, err
	}
	predicate, err := cli.PinnedMateriality(s.p)
	if err != nil {
		return nil, err
	}
	open.Materiality = predicate.Ref()
	opened, err := s.sealV1Step(ctx, dealID, nil, dealEvent{Kind: "open", Open: &open})
	if err != nil {
		return nil, err
	}
	events := []sealedEvent{opened}
	intent := open.Intent
	se, err := s.sealV1(ctx, dealID, events, dealEvent{Kind: "intent", Intent: &intent}, true)
	if err != nil {
		return nil, err
	}
	return append(events, se), nil
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
