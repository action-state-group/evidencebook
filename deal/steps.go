// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// Step is one step of a deal's local step list (deal_v1_events): its place
// N, its Kind, the caller's own form of it (Local, stored and returned
// unread; null for a step written without one) and Digest, the digest of
// the record the step carries or names, or of its local form when it names
// none.
type Step struct {
	N      int64
	Kind   string
	Local  json.RawMessage
	Digest string
}

// StepFacts is what only the store knows of a step when it is built: its
// place N in the deal's step list, its time At (RFC 3339 UTC, whole
// seconds) and Prev, the digest of the step before it ("" for the first).
type StepFacts struct {
	N    int64
	At   string
	Prev string
}

// JournalRecord is the caller's part of an x-deal-v1 journal record (a
// record of the journal overlay or the journey overlay: intent, check,
// verdict, approval, message, claim, evidence, detail_change, agent_move,
// disclosure, counterparty_profile). Block holds the block members the
// caller owns (producer, commit_alg, refs, counterparty); the store sets
// profile, canonicalization, record_type, at (the step's), and deal_id
// where PROFILE §3 puts it. Body is the record's body; the store adds the
// nonce PROFILE §5 asks of a check, a verdict and an approval: Nonce when
// the caller drew one, else a fresh one from Config.Random. Every record is
// scanned for a raw phone number or email address and a prohibited word
// (dealprofile.CheckText); LocalValues are further raw values it must not
// carry.
type JournalRecord struct {
	RecordType  string
	Block       map[string]any
	Body        map[string]any
	Nonce       string
	LocalValues []string
}

// StepParts is a step as its builder made it: its local form (nil stores
// null), its journal record (nil for a step that is local only), and, for a
// local-only step that names a record sealed elsewhere (a deal-book act),
// that record's Digest ("" for none: the step's digest is then its local
// form's).
type StepParts struct {
	Local  json.RawMessage
	Record *JournalRecord
	Digest string
}

// JournalStep is one step to add: its Kind (the caller's own name for it;
// "open" marks a step DealJournal does not read, "intent" the deal's
// intent, which a payment-sent record is about) and Build, which makes the
// step from the facts the store gives it.
type JournalStep struct {
	Kind  string
	Build func(StepFacts) (StepParts, error)
}

// nonceTypes are the journal records whose digest can leave the holder and
// so carry a fresh nonce of their own (PROFILE §5).
var nonceTypes = map[string]bool{xdealv1.TypeCheck: true, xdealv1.TypeVerdict: true, xdealv1.TypeApproval: true}

// preparedStep is one built step: its row and, unless it is local only, its
// record's payload.
type preparedStep struct {
	step    Step
	local   []byte
	payload []byte
	record  *xdealv1.Record
}

// appendJournalHook lets a test stop a run between storing a step and
// appending its record; appendJournalAfterHook lets it fail an append after
// the record was stored, as Book.Append can.
var appendJournalHook, appendJournalAfterHook func() error

// Steps reads a deal's local step list in order. A record a stopped run
// stored but did not append to the journal is appended first, once.
func (s *Store) Steps(ctx context.Context, dealID string) ([]Step, error) {
	out, pending, err := s.storedSteps(ctx, dealID)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		held, err := s.journalHolds(ctx, p.step.Digest)
		if err != nil {
			return nil, err
		}
		if !held {
			if err := s.appendJournal(ctx, p.payload); err != nil {
				return nil, err
			}
		}
		if err := s.stepAppended(ctx, dealID, p.step.Digest); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// StoredSteps reads a deal's local step list as stored, writing nothing: a
// record a stopped run stored and did not append stays pending (Steps
// appends it). For a reader that must not write, such as an audit.
func (s *Store) StoredSteps(ctx context.Context, dealID string) ([]Step, error) {
	out, _, err := s.storedSteps(ctx, dealID)
	return out, err
}

// storedSteps reads the step list as stored, writing nothing, with the
// steps whose record is still pending an append.
func (s *Store) storedSteps(ctx context.Context, dealID string) (out []Step, pending []preparedStep, err error) {
	rows, err := s.cfg.DB.QueryContext(ctx, `SELECT n, kind, local, record_digest, payload FROM deal_v1_events WHERE deal_id=? ORDER BY n`, dealID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st Step
		var local string
		var payload []byte
		if err := rows.Scan(&st.N, &st.Kind, &local, &st.Digest, &payload); err != nil {
			return nil, nil, err
		}
		st.Local = json.RawMessage(local)
		out = append(out, st)
		if len(payload) > 0 {
			pending = append(pending, preparedStep{step: st, payload: payload})
		}
	}
	return out, pending, rows.Err()
}

// SealJournal adds steps to a deal's local step list and seals the journal
// records they carry, in one batch: the steps are stored in one transaction,
// then their records are appended to the journal. Before anything is
// stored, each record is built, given its deal_id, at and nonce, held to the
// profile's checks (xdealv1.CheckSealed), to the caller's local values
// (dealprofile.CheckText), refused while a payment of the deal is in flight
// when it changes the deal's details (CodeInFlight), and held, with the
// deal's journal and the batch's earlier records, to x-deal-v1's
// authorization walk (xdealv1.CheckJournal), so no record it seals leaves
// the deal's later acts unauthorizable.
//
// An append that fails while the call runs withdraws the batch when none of
// its records reached the journal, so the same call made again seals it
// once; otherwise every step stays and the next Steps appends what is
// missing. A step that a stopped run stored is finished the same way.
func (s *Store) SealJournal(ctx context.Context, dealID string, steps ...JournalStep) ([]Step, error) {
	existing, err := s.Steps(ctx, dealID)
	if err != nil {
		return nil, err
	}
	n, prev := int64(len(existing)), ""
	if len(existing) > 0 {
		n, prev = existing[len(existing)-1].N, existing[len(existing)-1].Digest
	}
	var journal []xdealv1.Record
	var prepared []preparedStep
	for _, js := range steps {
		n++
		facts := StepFacts{N: n, At: s.now().Truncate(time.Second).Format(time.RFC3339), Prev: prev}
		parts, err := js.Build(facts)
		if err != nil {
			return nil, err
		}
		p := preparedStep{step: Step{N: n, Kind: js.Kind}, local: parts.Local}
		if p.local == nil {
			p.local = []byte("null")
		}
		p.step.Local = p.local
		switch {
		case parts.Record != nil:
			rec, payload, digest, err := s.buildJournalRecord(dealID, facts, parts.Record)
			if err != nil {
				return nil, err
			}
			if err := s.refuseWhileInFlight(ctx, dealID, rec); err != nil {
				return nil, err
			}
			if journal == nil {
				if journal, err = s.DealJournal(ctx, dealID); err != nil {
					return nil, err
				}
			}
			journal = append(journal, rec)
			if err := xdealv1.CheckJournal(journal); err != nil {
				return nil, inputError("the " + rec.Block.RecordType + " record would leave the deal's journal one x-deal-v1's authorization refuses: " + err.Error())
			}
			p.payload, p.step.Digest, p.record = payload, digest, &rec
		case parts.Digest != "":
			p.step.Digest = parts.Digest
		default:
			sum := sha256.Sum256(p.local)
			p.step.Digest = hex.EncodeToString(sum[:])
		}
		prev = p.step.Digest
		prepared = append(prepared, p)
	}
	if err := s.storeSteps(ctx, dealID, prepared); err != nil {
		return nil, err
	}
	out := make([]Step, len(prepared))
	for i, p := range prepared {
		out[i] = p.step
	}
	return out, nil
}

// buildJournalRecord builds a step's journal record from the caller's part.
func (s *Store) buildJournalRecord(dealID string, f StepFacts, in *JournalRecord) (xdealv1.Record, []byte, string, error) {
	b := map[string]any{}
	for _, k := range []string{"producer", "commit_alg", "refs", "counterparty"} {
		if v, ok := in.Block[k]; ok {
			b[k] = v
		}
	}
	for k := range in.Block {
		if _, ok := b[k]; !ok {
			return xdealv1.Record{}, nil, "", fieldError("block", " member "+k+" is the store's, not the caller's")
		}
	}
	b["profile"], b["canonicalization"], b["record_type"], b["at"] = xdealv1.Profile, xdealv1.Canonicalization, in.RecordType, f.At
	// deal_id where PROFILE §3 puts it: never on an intent, a disclosure, a
	// counterparty profile or a confirm_limits approval, which span deals;
	// on every other journal record of an x-deal-v1 deal, whose offer
	// exists.
	switch {
	case in.RecordType == xdealv1.TypeIntent, in.RecordType == "disclosure", in.RecordType == "counterparty_profile":
	case in.RecordType == xdealv1.TypeApproval && in.Body["choice"] == xdealv1.ChoiceConfirm:
	default:
		b["deal_id"] = dealID
	}
	body := make(map[string]any, len(in.Body)+1)
	for k, v := range in.Body {
		body[k] = v
	}
	if nonceTypes[in.RecordType] {
		nonce := in.Nonce
		if nonce == "" {
			var err error
			if nonce, err = s.randomHex(32); err != nil {
				return xdealv1.Record{}, nil, "", err
			}
		}
		body["nonce"] = nonce
	} else if in.Nonce != "" {
		return xdealv1.Record{}, nil, "", inputError("a " + in.RecordType + " record carries no nonce of its own")
	}
	raw, err := json.Marshal(map[string]any{xdealv1.Profile: b, "body": body})
	if err != nil {
		return xdealv1.Record{}, nil, "", err
	}
	if err := xdealv1.CheckSealed(raw, xdealv1.SealContext{OfferExists: true}); err != nil {
		return xdealv1.Record{}, nil, "", inputError("the x-deal-v1 " + in.RecordType + " record does not pass the profile's checks: " + err.Error())
	}
	{
		if err := dealprofile.CheckText(raw, in.LocalValues); err != nil {
			return xdealv1.Record{}, nil, "", inputError("the x-deal-v1 " + in.RecordType + " record carries what it may not: " + err.Error())
		}
	}
	rec, err := xdealv1.Decode(raw)
	if err != nil {
		return xdealv1.Record{}, nil, "", err
	}
	if xdealv1.PlacementOf(rec) != xdealv1.PlacementJournal {
		return xdealv1.Record{}, nil, "", inputError("a " + in.RecordType + " record is not sealed in the journal")
	}
	payload, digest, err := xdealv1.Encode(raw)
	if err != nil {
		return xdealv1.Record{}, nil, "", err
	}
	return rec, payload, digest, nil
}

// refuseWhileInFlight refuses a journal record that changes the deal's
// details while one of its payments is in flight: the payer leg's
// authorization would refuse it (xdealv1.ChangesDetails).
func (s *Store) refuseWhileInFlight(ctx context.Context, dealID string, rec xdealv1.Record) error {
	if !xdealv1.ChangesDetails(rec) {
		return nil
	}
	var check, reverses, refType string
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT check_digest, reverses, ref_type FROM deal_v1_sends WHERE leg_digest='' AND deal_id=? ORDER BY event_n LIMIT 1`, dealID).Scan(&check, &reverses, &refType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return coded(CodeInFlight, "", "a payment of deal "+dealID+" is in flight (check "+check+"): a "+rec.Block.RecordType+
		" record that changes the deal's details would void its authorization, so it waits until the payment is settled or failed (Store.Settle)",
		dealID, check, reverses, refType, rec.Block.RecordType)
}

// storeSteps stores prepared steps in one transaction, then appends their
// records. An append that fails while the call runs withdraws the batch
// unless a record of it reached the journal (settleFailed).
func (s *Store) storeSteps(ctx context.Context, dealID string, steps []preparedStep) error {
	tx, err := s.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, p := range steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, ?, ?, ?, ?)`,
			dealID, p.step.N, p.step.Kind, string(p.local), p.step.Digest, p.payload); err != nil {
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
		if err := s.appendJournal(ctx, p.payload); err != nil {
			return errors.Join(err, s.settleFailed(ctx, dealID, steps))
		}
		if err := s.stepAppended(ctx, dealID, p.step.Digest); err != nil {
			return err
		}
	}
	return nil
}

// settleFailed decides a batch whose append failed while the call ran. An
// append can fail after its record was stored, so the batch's records are
// asked for: if none is in the journal, every step of the batch is
// withdrawn in one transaction, and the same call made again seals the
// batch once; if any is held, or the answer is uncertain, every step stays
// and the next Steps appends what is missing.
func (s *Store) settleFailed(ctx context.Context, dealID string, steps []preparedStep) error {
	for _, q := range steps {
		if q.payload == nil {
			continue
		}
		held, err := s.journalHolds(ctx, q.step.Digest)
		if err != nil || held {
			return err
		}
	}
	tx, err := s.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, `DELETE FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, q.step.N); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}

// stepAppended clears a stored payload once its record is in the journal,
// so a read only looks at true leftovers.
func (s *Store) stepAppended(ctx context.Context, dealID, digest string) error {
	_, err := s.cfg.DB.ExecContext(ctx, `UPDATE deal_v1_events SET payload=NULL WHERE deal_id=? AND record_digest=?`, dealID, digest)
	return err
}

// appendJournal appends one journal record. A record stored but not yet in
// the log is recovered by the next Journal open (Shelf.Journal).
func (s *Store) appendJournal(ctx context.Context, payload []byte) error {
	if appendJournalHook != nil {
		if err := appendJournalHook(); err != nil {
			return err
		}
	}
	j, err := s.cfg.Shelf.Journal(ctx)
	if err != nil {
		return err
	}
	_, err = j.Append(ctx, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}})
	if err == nil && appendJournalAfterHook != nil {
		err = appendJournalAfterHook()
	}
	return err
}
