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
	"intent": true, "approval": true, "message": true, "claim": true, "evidence": true, "change": true, "disclosure": true,
	"searched": true, "options": true, "recommended": true, "chose": true, "couldnt": true,
}

// v1NoteRefusal is what a note kind with no v1 form says on a v1 deal, naming
// the verb that records it, or the one that will once it exists.
func v1NoteRefusal(kind, dealID string) error {
	head := "deal " + dealID + " is an x-deal-v1 deal: `deal note --kind " + kind + "` "
	switch kind {
	case "act":
		return cli.InputError(head + "has no x-deal-v1 form; an act is recorded with `deal perform`, and a payment will be with `deal settle` (not yet available: evidencebook#626)")
	case "payment_received":
		return cli.InputError(head + "has no x-deal-v1 form; it will be recorded with `deal settle` (not yet available: evidencebook#626)")
	case "delivered":
		return cli.InputError(head + "has no x-deal-v1 form; it is recorded with `deal perform`")
	case noteKindDispute:
		return cli.InputError(head + "has no x-deal-v1 form; a dispute is recorded with `deal dispute`")
	case noteKindRuling:
		return cli.InputError(head + "has no x-deal-v1 form; a ruling is the adjudicator's own record, in its case book")
	case "acceptance", "platform_approval":
		return cli.InputError(head + "has no x-deal-v1 form; a sale is recorded with `deal sale ...`")
	}
	return cli.InputError(head + "has no x-deal-v1 form and is not recorded on this deal")
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
		if err := s.v1Appended(ctx, dealID, pendingDigests[i]); err != nil {
			return nil, err
		}
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
	prepared, err := s.prepareV1(dealID, events, ev, opening)
	if err != nil {
		return sealedEvent{}, err
	}
	if err := s.storeV1(ctx, dealID, prepared); err != nil {
		return sealedEvent{}, err
	}
	return prepared.se, nil
}

// preparedV1 is one fixed step: its sealed event, its plain JSON and, unless it
// is the opening, its record's payload.
type preparedV1 struct {
	se      sealedEvent
	local   []byte
	payload []byte
}

// prepareV1 fixes a step and builds its record, storing nothing.
func (s *dealSession) prepareV1(dealID string, events []sealedEvent, ev dealEvent, opening bool) (preparedV1, error) {
	ev, err := s.fixStep(dealID, events, ev)
	if err != nil {
		return preparedV1{}, err
	}
	if opening {
		ev.Journey = ""
	}
	local, err := json.Marshal(ev)
	if err != nil {
		return preparedV1{}, err
	}
	var payload []byte
	var digest string
	if ev.Kind == "open" || ev.Kind == "counterparty_profile" {
		// The opening has no record. It gets a local digest so the v0
		// record builder's references to it are well formed; v1Record maps
		// any reference to it onto the deal's first journal record. A check's
		// companion (the payee's fingerprint) stays local too: an x-deal-v1
		// counterparty_profile record has no member to carry the fingerprint
		// (PROFILE §2 block, v0's empty body), and only the checker and the
		// cross-deal memory read it.
		digest = digestBytesHex(local)
	} else if payload, digest, err = s.v1Record(events, ev); err != nil {
		return preparedV1{}, err
	}
	return preparedV1{se: sealedEvent{CapsuleID: v1EventID(dealID, ev.N), Digest: digest, Sequence: uint64(ev.N), Event: ev}, local: local, payload: payload}, nil
}

// storeV1 stores prepared steps in one transaction, then appends their records.
// An append that fails while the process lives withdraws the steps it did not
// append, as v0's seal withdraws its index row, so a retry seals the step once;
// a process that dies leaves them stored, and the next read appends them.
func (s *dealSession) storeV1(ctx context.Context, dealID string, steps ...preparedV1) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, p := range steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, ?, ?, ?, ?)`,
			dealID, p.se.Event.N, p.se.Event.Kind, string(p.local), p.se.Digest, p.payload); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, p := range steps {
		if p.payload == nil {
			continue
		}
		if err := s.appendV1Journal(ctx, p.payload); err != nil {
			return errors.Join(err, s.settleFailedV1(ctx, dealID, steps))
		}
		if err := s.v1Appended(ctx, dealID, p.se.Digest); err != nil {
			return err
		}
	}
	return nil
}

// settleFailedV1 decides a batch whose append failed while the command ran.
// An append can fail after its record was stored, so the batch's records are
// asked for: if none is in the journal, every step of the batch is withdrawn
// in one transaction, the opening included, and a retry seals the batch once;
// if any is held, or the answer is uncertain, every step stays and the next
// command that loads the deal appends what is missing. A deal is never left
// with an intent but no opening, or an opening but no intent.
func (s *dealSession) settleFailedV1(ctx context.Context, dealID string, steps []preparedV1) error {
	for _, q := range steps {
		if q.payload == nil {
			continue
		}
		held, err := s.journalHolds(ctx, q.se.Digest)
		if err != nil || held {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, `DELETE FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, q.se.Event.N); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}

// v1Appended clears a stored payload once its record is in the journal, so a
// read only looks at true leftovers.
func (s *dealSession) v1Appended(ctx context.Context, dealID, digest string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deal_v1_events SET payload=NULL WHERE deal_id=? AND record_digest=?`, dealID, digest)
	return err
}

// v1Record builds an event's x-deal-v1 journal record: v0's record builder
// gives the body, and the block is the v1 one.
func (s *dealSession) v1Record(events []sealedEvent, ev dealEvent) ([]byte, string, error) {
	// The v0 record is checked as a v0 step is, before anything is stored:
	// the profile's privacy and wording scans against the deal's local values
	// (checkSealable), so a v1 record never carries what a v0 one may not.
	v0payload, _, err := encodeDealRecord(ev, events, s.dkey)
	if err != nil {
		return nil, "", err
	}
	if err := checkSealable(v0payload, events, ev); err != nil {
		return nil, "", err
	}
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
	if rtype == "check" && ev.Snapshot != nil && ev.Snapshot.PaymentRef != "" {
		nb["payment_ref"] = ev.Snapshot.PaymentRef
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

// appendV1JournalAfterHook lets a test fail an append after its record was
// stored, as Book.Append can.
var appendV1JournalAfterHook func() error

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
	if err == nil && appendV1JournalAfterHook != nil {
		err = appendV1JournalAfterHook()
	}
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
	opened, err := s.prepareV1(dealID, nil, dealEvent{Kind: "open", Open: &open}, false)
	if err != nil {
		return nil, err
	}
	intent := open.Intent
	first, err := s.prepareV1(dealID, []sealedEvent{opened.se}, dealEvent{Kind: "intent", Intent: &intent}, true)
	if err != nil {
		return nil, err
	}
	if err := s.storeV1(ctx, dealID, opened, first); err != nil {
		return nil, err
	}
	return []sealedEvent{opened.se, first.se}, nil
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
