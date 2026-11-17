// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// The store of what the other side pushes (evidencebook#461,
// docs/design/461-shelf-facts.md §3): the records a counterparty, an
// adjudicator or an offerer sends, and the stamp proofs that cover them.
// The library knows no record format: a ReceiveProfile reads each record
// (its signature, sender, log position, predecessor and deal) and says who
// may push it. The store owns the rest: the duplicate, the counted prefix
// and its gaps, forks and their evidence, the bounds, the proofs, retention
// and tombstones, and the deal ids each call changed. Nothing in the store
// is appended to a book, and the store needs no shelf of book facts: it
// works on a version 1 shelf as on a migrated one.
//
// Every state the calls report is recomputed from the tables (a restart
// recomputes the same counted prefix): the store keeps no derived state.

// ReceiveProfile is what a profile (today x-deal-v0's records) tells the
// store about received records. The store calls it outside the shelf's
// mutex, so it may read the shelf; it must not call Receive or Forget.
type ReceiveProfile interface {
	// Read checks a received record's signature and returns what the store
	// needs of it. An error refuses the record as invalid.
	Read(ctx context.Context, record []byte) (ReceivedRecord, error)
	// Admit is the push rule (§3, "who may push"): whether this sender may
	// push this record now. An offer may come from any key; any other
	// record only from a key a book on the shelf names as its counterparty
	// or adjudicator, whose offer our side answered, or to which an offer in
	// the journal was sent. Holding someone's offer grants nothing.
	Admit(ctx context.Context, r ReceivedRecord) (bool, error)
	// Answered reports whether our side has answered a stored offer (a
	// journal reply or our agreement). An answered offer leaves the offer
	// bounds and is never dropped; the store asks again only about offers
	// it holds as unanswered.
	Answered(ctx context.Context, offer ReceivedRecord) (bool, error)
	// ProofSender relates a stamp proof's key (the checkpoint key id that
	// signed both its checkpoints) and the book log it names to the sender
	// key its records are held under (ReceivedRecord.SenderKey), and says
	// whether that sender may push a proof now. ok false refuses the proof.
	ProofSender(ctx context.Context, checkpointKeyID, logID string) (senderKey string, ok bool, err error)
}

// ReceivedRecord is what the store needs of a received record.
type ReceivedRecord struct {
	// Digest is the record's entry in its sender's book log (for a book
	// record, its record id): 64 lowercase hex characters, unique across
	// senders. A book checkpoint's root recomputes from these.
	Digest string
	// SenderKey is the key the sender signs its records with.
	SenderKey string
	// LogID and Position place the record in the sender's book log; the
	// first record is at position 1.
	LogID    string
	Position uint64
	// Prev is the digest of the record at Position-1 that this one follows,
	// "" exactly when Position is 1.
	Prev string
	// DealID is the deal the record belongs to: what Receive reports as
	// changed, and what Forget deletes by. An unsolicited offer's deal id is
	// the profile's to choose (its own id, say).
	DealID string
	// Offer marks an unsolicited offer: one that belongs to no book the
	// shelf holds. Offers have their own bounds (ReceiveBounds).
	Offer bool
}

// ReceiveConfig is the shelf's store of received records.
type ReceiveConfig struct {
	// Profile reads received records; Receive refuses every call without one.
	Profile ReceiveProfile
	// Bounds are the store's bounds; a zero member takes its default.
	Bounds ReceiveBounds
	// Retention is how long a received record is kept once its deal is
	// settled (Forget), the holder's own setting. Zero is the default: the
	// life of the shelf, so nothing is ever deleted.
	Retention time.Duration
}

// ReceiveBounds are the store's bounds (§3). A zero member takes the default
// beside it.
type ReceiveBounds struct {
	// MaxRecordBytes refuses a larger record: 1 MiB.
	MaxRecordBytes int64
	// MaxRecordsPerLog refuses a record past this many held from one sender
	// and log (one book): 100000.
	MaxRecordsPerLog int64
	// Unanswered unsolicited offers, per sender (16, 4 MiB) and in total
	// (1024, 64 MiB). Reaching one drops the oldest unanswered offer first;
	// an offer larger than a byte bound alone is refused.
	MaxOffersPerSender     int64
	MaxOfferBytesPerSender int64
	MaxOffers              int64
	MaxOfferBytes          int64
}

// DefaultReceiveBounds are the store's bounds when none is set.
var DefaultReceiveBounds = ReceiveBounds{
	MaxRecordBytes: 1 << 20, MaxRecordsPerLog: 100000,
	MaxOffersPerSender: 16, MaxOfferBytesPerSender: 4 << 20,
	MaxOffers: 1024, MaxOfferBytes: 64 << 20,
}

func (b ReceiveBounds) withDefaults() ReceiveBounds {
	d := DefaultReceiveBounds
	for _, m := range []struct{ v, def *int64 }{
		{&b.MaxRecordBytes, &d.MaxRecordBytes}, {&b.MaxRecordsPerLog, &d.MaxRecordsPerLog},
		{&b.MaxOffersPerSender, &d.MaxOffersPerSender}, {&b.MaxOfferBytesPerSender, &d.MaxOfferBytesPerSender},
		{&b.MaxOffers, &d.MaxOffers}, {&b.MaxOfferBytes, &d.MaxOfferBytes},
	} {
		if *m.v <= 0 {
			*m.v = *m.def
		}
	}
	return b
}

// Evidence kinds (shelf_evidence.kind).
const (
	// EvidenceFork is two records a sender signed at one position: a holds
	// the record held first, b the other. A record whose predecessor is
	// another than the one held at that position is a fork there too.
	EvidenceFork = "fork"
	// EvidenceCheckpoint is two book checkpoints a sender signed for one
	// log at one size with different roots: a the one held, b the other.
	EvidenceCheckpoint = "checkpoint"
	// EvidenceCheckpointRecords is a book checkpoint whose root the records
	// held from that sender and log do not recompute: a the checkpoint, b
	// the JSON array of the records' digests it was recomputed over.
	EvidenceCheckpointRecords = "checkpoint_records"
)

// ReceiveResult is what one Receive call did, part by part.
type ReceiveResult struct {
	// Record is the record part's outcome; nil when no record was given.
	Record *RecordOutcome
	// Proof is the proof part's outcome; nil when no proof was given.
	Proof *ProofOutcome
	// Changes are the deal ids whose rows the profile recomputes.
	Changes ReceiveChanges
}

// RecordOutcome is what became of a received record.
type RecordOutcome struct {
	Digest string
	// Stored is a record stored by this call.
	Stored bool
	// Duplicate is a record already held: acknowledged, not stored again.
	Duplicate bool
	// Deleted is a record held once and deleted under the holder's
	// retention: acknowledged, not stored again.
	Deleted bool
	// Counted is a held record within its sender's counted prefix.
	Counted bool
	// AskForBook is a held record past the counted prefix: the sender is
	// asked for its book.
	AskForBook bool
	// Refused is why the record was refused (ErrInvalid, ErrRefused or
	// ErrOverLimit); nothing of it is stored.
	Refused error
}

// ProofOutcome is what became of a received stamp proof.
type ProofOutcome struct {
	// Accepted is a proof the store keeps: new, or the same book checkpoint
	// under another shelf checkpoint, kept beside the first.
	Accepted bool
	// Duplicate is a proof already held: acknowledged, not stored again.
	Duplicate bool
	// Refused is why the proof was refused (ErrInvalid or ErrRefused). A
	// proof that contradicts a held one, or the records held, is refused and
	// its evidence kept.
	Refused error
}

// ReceiveChanges are the deal ids a Receive call changed, each list sorted.
type ReceiveChanges struct {
	// Grew and Shrank are deals whose sender's counted prefix grew or
	// shrank; Forked those with new evidence of a fork or a contradicting
	// checkpoint.
	Grew, Shrank, Forked []string
	// Covered are deals whose records gained coverage from an accepted
	// stamp proof.
	Covered []string
}

type changeSet struct{ grew, shrank, forked, covered map[string]bool }

func newChangeSet() changeSet {
	return changeSet{grew: map[string]bool{}, shrank: map[string]bool{}, forked: map[string]bool{}, covered: map[string]bool{}}
}

func (c changeSet) result() ReceiveChanges {
	list := func(m map[string]bool) []string {
		out := make([]string, 0, len(m))
		for id := range m {
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}
	return ReceiveChanges{Grew: list(c.grew), Shrank: list(c.shrank), Forked: list(c.forked), Covered: list(c.covered)}
}

// receivedTime is the stored time format: fixed width, so rows sort by it.
const receivedTimeFormat = "2006-01-02T15:04:05.000000000Z"

func (s *Shelf) receiveNow() string {
	now := time.Now()
	if s.cfg.Now != nil {
		now = s.cfg.Now()
	}
	return now.UTC().Format(receivedTimeFormat)
}

// Receive takes a pushed record, a stamp proof, or both (§3), and handles
// each part on its own, the record first: a record sent again with a proof
// it lacked the first time still delivers the proof. A part refused is
// reported in its outcome and in the returned error (errors.Join of each
// part's refusal), with the result of the other part; any other error is
// the store's, and nothing is reported.
func (s *Shelf) Receive(ctx context.Context, record []byte, proof *StampProof) (ReceiveResult, error) {
	profile := s.cfg.Receive.Profile
	if profile == nil {
		return ReceiveResult{}, fmt.Errorf("%w: the shelf has no receive profile", ErrInvalid)
	}
	if record == nil && proof == nil {
		return ReceiveResult{}, fmt.Errorf("%w: nothing to receive", ErrInvalid)
	}
	s.receiveMu.Lock()
	defer s.receiveMu.Unlock()
	if err := s.live(); err != nil {
		return ReceiveResult{}, err
	}
	changes := newChangeSet()
	var result ReceiveResult
	var refusals []error
	if record != nil {
		out, err := s.receiveRecord(ctx, profile, record, changes)
		if err != nil {
			return ReceiveResult{}, err
		}
		result.Record = &out
		refusals = append(refusals, out.Refused)
	}
	if proof != nil {
		out, err := s.receiveProof(ctx, profile, *proof, changes)
		if err != nil {
			return ReceiveResult{}, err
		}
		result.Proof = &out
		refusals = append(refusals, out.Refused)
	}
	result.Changes = changes.result()
	return result, errors.Join(refusals...)
}

func (s *Shelf) live() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return ErrReleased
	}
	return nil
}

func (r ReceivedRecord) validate() error {
	switch {
	case !isDigest(r.Digest):
		return errors.New("its digest is not 64 lowercase hex characters")
	case r.SenderKey == "" || r.LogID == "" || r.DealID == "":
		return errors.New("it names no sender, log or deal")
	case r.Position == 0:
		return errors.New("its position is 0; the first is 1")
	case r.Position == 1 && r.Prev != "":
		return errors.New("the first record names a predecessor")
	case r.Position > 1 && !isDigest(r.Prev):
		return errors.New("it names no predecessor digest")
	}
	return nil
}

// logKey is one sender's book log.
type logKey struct{ sender, log string }

// errRefusal carries a refusal out of a transaction, which it rolls back.
type errRefusal struct{ err error }

func (e errRefusal) Error() string { return e.err.Error() }

func (s *Shelf) receiveRecord(ctx context.Context, profile ReceiveProfile, record []byte, changes changeSet) (RecordOutcome, error) {
	bounds := s.cfg.Receive.Bounds.withDefaults()
	var out RecordOutcome
	refuse := func(err error) (RecordOutcome, error) {
		out.Refused = err
		return out, nil
	}
	if int64(len(record)) > bounds.MaxRecordBytes {
		return refuse(fmt.Errorf("%w: a received record of %d bytes, over the store's %d", ErrOverLimit, len(record), bounds.MaxRecordBytes))
	}
	r, err := profile.Read(ctx, record)
	if err != nil {
		return refuse(fmt.Errorf("%w: a received record: %v", ErrInvalid, err))
	}
	if err := r.validate(); err != nil {
		return refuse(fmt.Errorf("%w: a received record: %v", ErrInvalid, err))
	}
	out.Digest = r.Digest
	key := logKey{r.SenderKey, r.LogID}

	if deleted, err := s.tombstoned(ctx, s.db, r.Digest); err != nil {
		return RecordOutcome{}, err
	} else if deleted {
		out.Deleted = true
		return out, nil
	}
	var held []byte
	switch err := s.db.QueryRowContext(ctx, `SELECT record FROM shelf_received WHERE digest = ?`, r.Digest).Scan(&held); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return RecordOutcome{}, err
	default:
		if !bytes.Equal(held, record) {
			return refuse(fmt.Errorf("%w: another record is held under digest %s", ErrInvalid, r.Digest))
		}
		out.Duplicate = true
		p, err := receivedPrefix(ctx, s.db, key)
		if err != nil {
			return RecordOutcome{}, err
		}
		out.Counted = p.counts(r.Position, r.Digest)
		out.AskForBook = !out.Counted
		return out, nil
	}
	if ok, err := profile.Admit(ctx, r); err != nil {
		return RecordOutcome{}, err
	} else if !ok {
		return refuse(fmt.Errorf("%w: %s may not push this record", ErrRefused, r.SenderKey))
	}
	var answered []string
	if r.Offer {
		if answered, err = s.answeredOffers(ctx, profile); err != nil {
			return RecordOutcome{}, err
		}
	}
	now := s.receiveNow()
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		for _, digest := range answered {
			if _, err := tx.ExecContext(ctx, `UPDATE shelf_received SET offer = 0 WHERE digest = ?`, digest); err != nil {
				return err
			}
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ?`, r.SenderKey, r.LogID).Scan(&count); err != nil {
			return err
		}
		if count >= bounds.MaxRecordsPerLog {
			return errRefusal{fmt.Errorf("%w: %d records held from %s in %s, the store's bound", ErrOverLimit, count, r.SenderKey, r.LogID)}
		}
		before := map[logKey]receivedPrefixState{}
		remember := func(k logKey) error {
			if _, ok := before[k]; ok {
				return nil
			}
			p, err := receivedPrefix(ctx, tx, k)
			before[k] = p
			return err
		}
		if err := remember(key); err != nil {
			return err
		}
		dropped := map[logKey][]string{}
		if r.Offer {
			if err := makeRoomForOffer(ctx, tx, bounds, r.SenderKey, int64(len(record)), func(row receivedRow) error {
				k := logKey{row.sender, row.log}
				dropped[k] = append(dropped[k], row.deal)
				return remember(k)
			}); err != nil {
				return err
			}
		}
		forks, err := forkEvidence(ctx, tx, r, record)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO shelf_received (digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Digest, r.SenderKey, r.LogID, r.Position, r.Prev, r.DealID, r.Offer, record, now); err != nil {
			return err
		}
		for _, f := range forks {
			if err := noteEvidence(ctx, tx, EvidenceFork, f.held, record, now); err != nil {
				return err
			}
			changes.forked[r.DealID] = true
			changes.forked[f.deal] = true
		}
		keys := make([]logKey, 0, len(before))
		for k := range before {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].sender < keys[j].sender || (keys[i].sender == keys[j].sender && keys[i].log < keys[j].log)
		})
		for _, k := range keys {
			after, err := receivedPrefix(ctx, tx, k)
			if err != nil {
				return err
			}
			old := before[k]
			if len(after.digests) == len(old.digests) {
				continue
			}
			deals := append(append([]string{}, after.deals...), dropped[k]...)
			target := changes.grew
			if len(after.digests) < len(old.digests) {
				target = changes.shrank
			}
			for _, d := range deals {
				target[d] = true
			}
			if len(after.digests) > len(old.digests) {
				// The prefix now covers proofs it did not: their roots must
				// recompute from the records.
				if err := checkHeldProofs(ctx, tx, k, after, uint64(len(old.digests)), now, changes); err != nil {
					return err
				}
			}
		}
		p, err := receivedPrefix(ctx, tx, key)
		if err != nil {
			return err
		}
		out.Stored = true
		out.Counted = p.counts(r.Position, r.Digest)
		out.AskForBook = !out.Counted
		return nil
	})
	var refusal errRefusal
	if errors.As(err, &refusal) {
		out.Stored, out.Counted, out.AskForBook = false, false, false
		return refuse(refusal.err)
	}
	if err != nil {
		return RecordOutcome{}, err
	}
	return out, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type execQueryer interface {
	queryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Shelf) tombstoned(ctx context.Context, q queryer, digest string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_received_tombstones WHERE digest = ?`, digest).Scan(&n)
	return n > 0, err
}

// receivedRow is a stored record's columns but its bytes.
type receivedRow struct {
	digest, sender, log, prev, deal string
	position                        uint64
	offer                           bool
	size                            int64
}

func (r receivedRow) record() ReceivedRecord {
	return ReceivedRecord{Digest: r.digest, SenderKey: r.sender, LogID: r.log, Position: r.position, Prev: r.prev, DealID: r.deal, Offer: r.offer}
}

const receivedColumns = `digest, sender_key, log_id, position, prev, deal_id, offer, length(record)`

func scanReceived(rows *sql.Rows) ([]receivedRow, error) {
	defer rows.Close()
	var out []receivedRow
	for rows.Next() {
		var r receivedRow
		if err := rows.Scan(&r.digest, &r.sender, &r.log, &r.position, &r.prev, &r.deal, &r.offer, &r.size); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// answeredOffers asks the profile about every offer held as unanswered and
// returns those it says were answered.
func (s *Shelf) answeredOffers(ctx context.Context, profile ReceiveProfile) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+receivedColumns+` FROM shelf_received WHERE offer = 1 ORDER BY received_at, rowid`)
	if err != nil {
		return nil, err
	}
	offers, err := scanReceived(rows)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range offers {
		answered, err := profile.Answered(ctx, o.record())
		if err != nil {
			return nil, err
		}
		if answered {
			out = append(out, o.digest)
		}
	}
	return out, nil
}

// makeRoomForOffer drops the oldest unanswered offers until one more of
// size bytes from sender fits both its sender's bounds and the total ones
// (§3): dropping an offer nobody answered is not a retention decision, so it
// leaves no tombstone. An offer larger than a byte bound alone is refused.
func makeRoomForOffer(ctx context.Context, tx *sql.Tx, b ReceiveBounds, sender string, size int64, dropping func(receivedRow) error) error {
	if size > b.MaxOfferBytesPerSender || size > b.MaxOfferBytes {
		return errRefusal{fmt.Errorf("%w: an offer of %d bytes, over the store's offer bound", ErrOverLimit, size)}
	}
	for _, scope := range []struct {
		where         string
		args          []any
		count, nbytes int64
	}{
		{`offer = 1 AND sender_key = ?`, []any{sender}, b.MaxOffersPerSender, b.MaxOfferBytesPerSender},
		{`offer = 1`, nil, b.MaxOffers, b.MaxOfferBytes},
	} {
		for {
			var count, held int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(record)), 0) FROM shelf_received WHERE `+scope.where, scope.args...).Scan(&count, &held); err != nil {
				return err
			}
			if count+1 <= scope.count && held+size <= scope.nbytes {
				break
			}
			rows, err := tx.QueryContext(ctx, `SELECT `+receivedColumns+` FROM shelf_received WHERE `+scope.where+` ORDER BY received_at, rowid LIMIT 1`, scope.args...)
			if err != nil {
				return err
			}
			oldest, err := scanReceived(rows)
			if err != nil {
				return err
			}
			if len(oldest) == 0 {
				return fmt.Errorf("%w: the offer bounds count offers the store does not list", ErrCorrupt)
			}
			if err := dropping(oldest[0]); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM shelf_received WHERE digest = ?`, oldest[0].digest); err != nil {
				return err
			}
		}
	}
	return nil
}

// receivedPrefixState is a sender's counted prefix in one log: the longest
// run of positions from the first, each holding one record no fork
// contradicts.
type receivedPrefixState struct {
	// digests are the counted records, by position from 1.
	digests []string
	// forkAt is the lowest position with a fork, 0 for none: the prefix ends
	// before it for good.
	forkAt uint64
	// deals are the deal ids of every record held from the sender in the log.
	deals []string
}

func (p receivedPrefixState) counts(position uint64, digest string) bool {
	return position >= 1 && position <= uint64(len(p.digests)) && p.digests[position-1] == digest
}

// receivedPrefix computes a counted prefix from the store. A position is
// forked when the digests claimed for it differ: the records held there,
// and the predecessor each record at the next position names.
func receivedPrefix(ctx context.Context, q queryer, k logKey) (receivedPrefixState, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+receivedColumns+` FROM shelf_received WHERE sender_key = ? AND log_id = ? ORDER BY position, digest`, k.sender, k.log)
	if err != nil {
		return receivedPrefixState{}, err
	}
	held, err := scanReceived(rows)
	if err != nil {
		return receivedPrefixState{}, err
	}
	at := map[uint64][]string{}
	claims := map[uint64]map[string]bool{}
	claim := func(p uint64, d string) {
		if claims[p] == nil {
			claims[p] = map[string]bool{}
		}
		claims[p][d] = true
	}
	deals := map[string]bool{}
	for _, r := range held {
		at[r.position] = append(at[r.position], r.digest)
		claim(r.position, r.digest)
		if r.position > 1 {
			claim(r.position-1, r.prev)
		}
		deals[r.deal] = true
	}
	var out receivedPrefixState
	for p, c := range claims {
		if len(c) > 1 && (out.forkAt == 0 || p < out.forkAt) {
			out.forkAt = p
		}
	}
	for p := uint64(1); len(at[p]) == 1 && (out.forkAt == 0 || p < out.forkAt); p++ {
		out.digests = append(out.digests, at[p][0])
	}
	for d := range deals {
		out.deals = append(out.deals, d)
	}
	sort.Strings(out.deals)
	return out, nil
}

// heldFork is a held record a new one forks from.
type heldFork struct {
	held []byte
	deal string
}

// forkEvidence finds the held records r contradicts (§3): another record at
// its position, or a record at the next position naming another
// predecessor; and, at the position before, a record other than the
// predecessor r names, or another record there naming another one.
func forkEvidence(ctx context.Context, tx *sql.Tx, r ReceivedRecord, record []byte) ([]heldFork, error) {
	type claim struct {
		position uint64
		digest   string
	}
	checks := []claim{{r.Position, r.Digest}}
	if r.Position > 1 {
		checks = append(checks, claim{r.Position - 1, r.Prev})
	}
	var out []heldFork
	for _, c := range checks {
		// The records that claim a digest at c.position: those held there,
		// and those at the next position, by the predecessor they name.
		rows, err := tx.QueryContext(ctx,
			`SELECT digest, prev, position, deal_id, record FROM shelf_received WHERE sender_key = ? AND log_id = ? AND position IN (?, ?) ORDER BY position, digest`,
			r.SenderKey, r.LogID, c.position, c.position+1)
		if err != nil {
			return nil, err
		}
		var found *heldFork
		for rows.Next() {
			var digest, prev, deal string
			var position uint64
			var b []byte
			if err := rows.Scan(&digest, &prev, &position, &deal, &b); err != nil {
				_ = rows.Close()
				return nil, err
			}
			claimed := digest
			if position == c.position+1 {
				claimed = prev
			}
			if claimed != c.digest && found == nil {
				found = &heldFork{held: b, deal: deal}
			}
		}
		if err := firstErr(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		if found != nil {
			out = append(out, *found)
		}
	}
	return out, nil
}

// noteEvidence keeps one row of evidence, once.
func noteEvidence(ctx context.Context, q execQueryer, kind string, a, b []byte, now string) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_evidence WHERE kind = ? AND a = ? AND b = ?`, kind, a, b).Scan(&n); err != nil || n > 0 {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO shelf_evidence (kind, a, b, noted_at) VALUES (?, ?, ?, ?)`, kind, a, b, now)
	return err
}

// checkHeldProofs checks every held proof of k whose size the prefix now
// covers, and did not at covered records, against the records: a root they
// do not recompute is kept as evidence and its deals reported forked.
func checkHeldProofs(ctx context.Context, tx *sql.Tx, k logKey, p receivedPrefixState, covered uint64, now string, changes changeSet) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT size, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? ORDER BY size`, k.sender, k.log)
	if err != nil {
		return err
	}
	type held struct {
		size uint64
		cp   []byte
	}
	var proofs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.size, &h.cp); err != nil {
			_ = rows.Close()
			return err
		}
		proofs = append(proofs, h)
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, h := range proofs {
		leaves, ok := mmr.LeafCount(h.size)
		if !ok || leaves <= covered || leaves > uint64(len(p.digests)) {
			continue
		}
		cp, err := checkpointFromStatement(h.cp)
		if err != nil {
			return err
		}
		if match, over, err := rootRecomputes(cp, p.digests); err != nil {
			return err
		} else if !match {
			if err := noteEvidence(ctx, tx, EvidenceCheckpointRecords, h.cp, over, now); err != nil {
				return err
			}
			for _, d := range p.deals {
				changes.forked[d] = true
			}
		}
	}
	return nil
}

// rootRecomputes reports whether the first records of a sender's log
// recompute a book checkpoint's root, and returns the digests it ran over
// as a JSON array.
func rootRecomputes(cp Checkpoint, digests []string) (bool, []byte, error) {
	over := digests[:cp.Entries]
	encoded, err := json.Marshal(over)
	if err != nil {
		return false, nil, err
	}
	tree, err := mmr.New(nil)
	if err != nil {
		return false, nil, err
	}
	for _, d := range over {
		value, err := hex.DecodeString(d)
		if err != nil {
			return false, nil, err
		}
		if _, err := tree.Append(value); err != nil {
			return false, nil, err
		}
	}
	peaks, err := tree.PeakHashesAt(cp.TreeSize)
	if err != nil {
		return false, nil, err
	}
	return hex.EncodeToString(mmr.RootFromPeaks(peaks)) == cp.Root, encoded, nil
}

// CountedPrefix is a sender's counted prefix in one book log, as the store
// computes it now.
type CountedPrefix struct {
	// Size is how many records, from position 1, are counted.
	Size uint64
	// Last is the digest of the last counted record ("" for none): what a
	// record's seen field names.
	Last string
	// ForkAt is the lowest position with a fork, 0 for none; the prefix
	// ends before it for good.
	ForkAt uint64
}

// CountedPrefix computes a sender's counted prefix in one log from the store.
func (s *Shelf) CountedPrefix(ctx context.Context, senderKey, logID string) (CountedPrefix, error) {
	if err := s.live(); err != nil {
		return CountedPrefix{}, err
	}
	p, err := receivedPrefix(ctx, s.db, logKey{senderKey, logID})
	if err != nil {
		return CountedPrefix{}, err
	}
	out := CountedPrefix{Size: uint64(len(p.digests)), ForkAt: p.forkAt}
	if len(p.digests) > 0 {
		out.Last = p.digests[len(p.digests)-1]
	}
	return out, nil
}

// HeldRecord is a received record the store holds.
type HeldRecord struct {
	ReceivedRecord
	Record     []byte
	ReceivedAt time.Time
}

// ReceivedRecordOf reads a received record by its digest. A record deleted
// under retention reports ErrDeleted, one never held ErrNotFound.
func (s *Shelf) ReceivedRecordOf(ctx context.Context, digest string) (HeldRecord, error) {
	if err := s.live(); err != nil {
		return HeldRecord{}, err
	}
	var h HeldRecord
	var at string
	err := s.db.QueryRowContext(ctx,
		`SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at FROM shelf_received WHERE digest = ?`, digest).
		Scan(&h.Digest, &h.SenderKey, &h.LogID, &h.Position, &h.Prev, &h.DealID, &h.Offer, &h.Record, &at)
	if errors.Is(err, sql.ErrNoRows) {
		var deletedAt string
		switch err := s.db.QueryRowContext(ctx, `SELECT deleted_at FROM shelf_received_tombstones WHERE digest = ?`, digest).Scan(&deletedAt); {
		case errors.Is(err, sql.ErrNoRows):
			return HeldRecord{}, fmt.Errorf("%w: received record %s", ErrNotFound, digest)
		case err != nil:
			return HeldRecord{}, err
		}
		return HeldRecord{}, fmt.Errorf("%w: received record %s, at %s", ErrDeleted, digest, deletedAt)
	}
	if err != nil {
		return HeldRecord{}, err
	}
	if h.ReceivedAt, err = time.Parse(receivedTimeFormat, at); err != nil {
		return HeldRecord{}, fmt.Errorf("%w: received record %s's time: %v", ErrCorrupt, digest, err)
	}
	return h, nil
}

// Forget deletes a deal's received records under the holder's retention
// (§3, retention): only when the shelf's Retention is set (by default
// nothing is ever deleted), only once settled reports the deal settled
// (every row agreed, uncontested, or ruled with its remedy done, as the row
// status function computes it), and only the records received at least
// Retention before now. Each deleted record leaves a tombstone, so reading
// it reports it deleted, not missing. The deal's proofs and evidence are
// kept. It returns the digests deleted.
func (s *Shelf) Forget(ctx context.Context, dealID string, now time.Time, settled func(ctx context.Context, dealID string) (bool, error)) ([]string, error) {
	if dealID == "" || settled == nil {
		return nil, fmt.Errorf("%w: Forget needs a deal id and its settled check", ErrInvalid)
	}
	retention := s.cfg.Receive.Retention
	if retention <= 0 {
		return nil, fmt.Errorf("%w: the holder keeps received records for the life of the shelf", ErrInvalid)
	}
	s.receiveMu.Lock()
	defer s.receiveMu.Unlock()
	if err := s.live(); err != nil {
		return nil, err
	}
	if ok, err := settled(ctx, dealID); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: deal %s is not settled; nothing of it is deleted", ErrInvalid, dealID)
	}
	cutoff := now.UTC().Add(-retention).Format(receivedTimeFormat)
	deletedAt := s.receiveNow()
	var deleted []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT digest, sender_key, received_at FROM shelf_received WHERE deal_id = ? AND received_at <= ? ORDER BY digest`, dealID, cutoff)
		if err != nil {
			return err
		}
		type row struct{ digest, sender, at string }
		var all []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.digest, &r.sender, &r.at); err != nil {
				_ = rows.Close()
				return err
			}
			all = append(all, r)
		}
		if err := firstErr(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, r := range all {
			if _, err := tx.ExecContext(ctx, `INSERT INTO shelf_received_tombstones (digest, sender_key, received_at, deleted_at) VALUES (?, ?, ?, ?)`, r.digest, r.sender, r.at, deletedAt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM shelf_received WHERE digest = ?`, r.digest); err != nil {
				return err
			}
			deleted = append(deleted, r.digest)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// Evidence is one row of the store's evidence.
type Evidence struct {
	Kind    string
	A, B    []byte
	NotedAt time.Time
}

// Evidence lists the store's evidence of forks and contradicting
// checkpoints, oldest first.
func (s *Shelf) Evidence(ctx context.Context) ([]Evidence, error) {
	if err := s.live(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, a, b, noted_at FROM shelf_evidence ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var at string
		if err := rows.Scan(&e.Kind, &e.A, &e.B, &at); err != nil {
			return nil, err
		}
		if e.NotedAt, err = time.Parse(receivedTimeFormat, at); err != nil {
			return nil, fmt.Errorf("%w: evidence time: %v", ErrCorrupt, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
