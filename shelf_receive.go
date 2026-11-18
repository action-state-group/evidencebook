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
	"math"
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
// recomputes the same counted prefix). Two facts are kept rather than
// derived, because deleting a record must not undo them: each conflict found
// (a fork at a position, a contradicting checkpoint at a size), with its one
// evidence row, and whether each held proof's root recomputed from the
// records (shelf_received_proofs.verified).
//
// The readers (CountedPrefix, ReceivedRecordOf, Evidence) take no lock, so a
// StatusProvider or a ReceiveProfile may call them from inside a stamp or a
// Receive. A call racing Release may fail with database/sql's closed-database
// error rather than ErrReleased.

// ReceiveProfile is what a profile (today x-deal-v0's records) tells the
// store about received records. The store calls it outside the shelf's
// mutex and outside any transaction of its own, so it may read the shelf; it
// must not call Receive or Forget.
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
	// journal reply or our agreement). The store asks about each offer it
	// would drop before its write transaction, under a mark of the holder's
	// own books (their records and log entries); if the mark moved by the
	// time the transaction holds the write lock, an answer may have been
	// committed in between, so it asks again (ErrBusy after three tries).
	// An answered offer leaves the offer bounds and is never dropped.
	Answered(ctx context.Context, offer ReceivedRecord) (bool, error)
	// ProofSender relates a stamp proof's key (the checkpoint key id that
	// signed both its checkpoints) and the book log it names to the sender
	// key its records are held under (ReceivedRecord.SenderKey), and says
	// whether that sender may push a proof now. ok false refuses the proof.
	ProofSender(ctx context.Context, checkpointKeyID, logID string) (senderKey string, ok bool, err error)
}

// AfterReceiveProfile is a ReceiveProfile that acts on what a Receive took
// before Receive returns: x-deal-v1 names a counted ruling's arrival in the
// holder's deal book (evidencebook#527), so no host can leave it unnamed by
// skipping a call. Receive calls AfterReceive on every call, a duplicate
// included, so a record sent again retries what an earlier call failed to
// do; it runs once the parts'
// transactions committed (on a proof part's store error too, with the record
// part's result), holding only the receive lock: it may read the shelf and
// append to the holder's own books, and must not call Receive or Forget. at
// is the shelf's clock. An error is Receive's, joined with the parts', whose
// result stays committed.
type AfterReceiveProfile interface {
	ReceiveProfile
	AfterReceive(ctx context.Context, result ReceiveResult, at time.Time) error
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
	// What unsolicited offers leave behind, per sender (16) and in total
	// (1024) rows: the position of each dropped offer
	// (shelf_received_vacated) and each conflict in a log that holds only
	// offers (shelf_received_conflicts, with its evidence, at most two
	// records' bytes). An offer belongs to no deal, so past either bound no
	// row is kept: such a position may later be forgotten, and a fork there
	// ends the counted prefix only while both its records are held. Rows
	// Forget leaves and conflicts in a log holding any other record are
	// kept for good and not counted.
	MaxOfferEvidencePerSender int64
	MaxOfferEvidence          int64
}

// DefaultReceiveBounds are the store's bounds when none is set.
var DefaultReceiveBounds = ReceiveBounds{
	MaxRecordBytes: 1 << 20, MaxRecordsPerLog: 100000,
	MaxOffersPerSender: 16, MaxOfferBytesPerSender: 4 << 20,
	MaxOffers: 1024, MaxOfferBytes: 64 << 20,
	MaxOfferEvidencePerSender: 16, MaxOfferEvidence: 1024,
}

func (b ReceiveBounds) withDefaults() ReceiveBounds {
	d := DefaultReceiveBounds
	for _, m := range []struct{ v, def *int64 }{
		{&b.MaxRecordBytes, &d.MaxRecordBytes}, {&b.MaxRecordsPerLog, &d.MaxRecordsPerLog},
		{&b.MaxOffersPerSender, &d.MaxOffersPerSender}, {&b.MaxOfferBytesPerSender, &d.MaxOfferBytesPerSender},
		{&b.MaxOffers, &d.MaxOffers}, {&b.MaxOfferBytes, &d.MaxOfferBytes},
		{&b.MaxOfferEvidencePerSender, &d.MaxOfferEvidencePerSender}, {&b.MaxOfferEvidence, &d.MaxOfferEvidence},
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
	// another than the one held at that position is a fork there too. Only
	// the first conflicting pair at a position is kept.
	EvidenceFork = "fork"
	// EvidenceCheckpoint is two book checkpoints a sender signed for one
	// log at one size with different roots: a the one held, b the other.
	// Only the first contradiction at a size is kept.
	EvidenceCheckpoint = "checkpoint"
	// EvidenceCheckpointRecords is a book checkpoint whose root the records
	// held from that sender and log do not recompute: a the checkpoint, b
	// the JSON array of the records' digests it was recomputed over. Only
	// the first at a size is kept.
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
	// DealID is the deal the profile read the record as belonging to
	// (ReceivedRecord.DealID), set once it read; "" for a record it could
	// not read.
	DealID string
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
	// Grew and Shrank are deals with records that entered or left their
	// sender's counted prefix; Forked those with new evidence of a fork or
	// a contradicting checkpoint.
	Grew, Shrank, Forked []string
	// Covered are deals whose records gained coverage: a held stamp proof
	// whose size they reach and whose root they recompute, when the proof
	// arrives or when the records do. A proof past the records held grants
	// none until then.
	Covered []string
}

type changeSet struct{ grew, shrank, forked, covered map[string]bool }

func newChangeSet() changeSet {
	return changeSet{grew: map[string]bool{}, shrank: map[string]bool{}, forked: map[string]bool{}, covered: map[string]bool{}}
}

func (c changeSet) merge(o changeSet) {
	for _, pair := range [][2]map[string]bool{{c.grew, o.grew}, {c.shrank, o.shrank}, {c.forked, o.forked}, {c.covered, o.covered}} {
		for id := range pair[1] {
			pair[0][id] = true
		}
	}
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

// receivedTimeFormat is the stored time format: fixed width, so rows sort
// by it.
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
// part's refusal), with the result of the other part. Any other error is
// the store's or the profile's: one in the record part reports nothing,
// since nothing of it was committed; one in the proof part returns the
// record part's outcome and changes beside it. A call racing Release may
// fail with database/sql's closed-database error rather than ErrReleased.
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
	// Each part writes its changes to its own set, merged only once that
	// part's transaction committed.
	changes := newChangeSet()
	var result ReceiveResult
	var refusals []error
	if record != nil {
		part := newChangeSet()
		out, err := s.receiveRecord(ctx, profile, record, part)
		if err != nil {
			return ReceiveResult{}, err
		}
		changes.merge(part)
		result.Record = &out
		refusals = append(refusals, out.Refused)
	}
	if proof != nil {
		part := newChangeSet()
		out, err := s.receiveProof(ctx, profile, *proof, part)
		if err != nil {
			// The record part, if any, is committed: report it, and act on it.
			result.Changes = changes.result()
			return result, errors.Join(err, s.afterReceive(ctx, profile, result))
		}
		changes.merge(part)
		result.Proof = &out
		refusals = append(refusals, out.Refused)
	}
	result.Changes = changes.result()
	return result, errors.Join(append(refusals, s.afterReceive(ctx, profile, result))...)
}

// afterReceive runs an AfterReceiveProfile's AfterReceive on result.
func (s *Shelf) afterReceive(ctx context.Context, profile ReceiveProfile, result ReceiveResult) error {
	after, ok := profile.(AfterReceiveProfile)
	if !ok {
		return nil
	}
	now := time.Now()
	if s.cfg.Now != nil {
		now = s.cfg.Now()
	}
	if err := after.AfterReceive(ctx, result, now); err != nil {
		return fmt.Errorf("after receive: %w", err)
	}
	return nil
}

// receiveBeforeCommit, when set (tests), runs at the end of each part's
// transaction, before its commit.
var receiveBeforeCommit func(ctx context.Context, part string)

func beforeCommit(ctx context.Context, part string) {
	if receiveBeforeCommit != nil {
		receiveBeforeCommit(ctx, part)
	}
}

// live is the released check of the received store, without the shelf's
// mutex.
func (s *Shelf) live() error {
	if s.closed.Load() {
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
	case r.Position > math.MaxInt64:
		// SQLite stores a signed 64-bit integer; every language refuses
		// a position past it as invalid.
		return errors.New("its position is past 2^63 - 1")
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

// logState is what a call compares before and after for one sender's log:
// its counted prefix and how far a verified proof covers it.
type logState struct {
	prefix   receivedPrefixState
	coverage uint64
}

func readLogState(ctx context.Context, q queryer, k logKey) (logState, error) {
	p, err := receivedPrefix(ctx, q, k)
	if err != nil {
		return logState{}, err
	}
	c, err := verifiedCoverage(ctx, q, k, p)
	return logState{prefix: p, coverage: c}, err
}

// report adds what changed from before to after to changes: deals with
// records that entered or left the counted prefix, and deals with counted
// records the verified coverage newly reaches.
func report(before, after logState, changes changeSet) {
	was := map[string]bool{}
	for _, c := range before.prefix.counted {
		was[c.digest] = true
	}
	now := map[string]bool{}
	for _, c := range after.prefix.counted {
		now[c.digest] = true
		if !was[c.digest] {
			changes.grew[c.deal] = true
		}
	}
	for _, c := range before.prefix.counted {
		if !now[c.digest] {
			changes.shrank[c.deal] = true
		}
	}
	if after.coverage > before.coverage {
		for i := before.coverage; i < after.coverage; i++ {
			changes.covered[after.prefix.counted[i].deal] = true
		}
	}
}

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
	out.Digest, out.DealID = r.Digest, r.DealID
	key := logKey{r.SenderKey, r.LogID}

	if deleted, err := tombstoned(ctx, s.db, r.Digest); err != nil {
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
		p, err := snapshotPrefix(ctx, s.db, key)
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
	now := s.receiveNow()
	for attempt := 1; ; attempt++ {
		var plan offerPlan
		var mark ownMark
		if r.Offer {
			// The profile is asked about the offers to drop before the
			// transaction, never inside it, under a mark of the holder's own
			// books: an answer committed after the mark changes it.
			if mark, err = readOwnMark(ctx, s.db); err != nil {
				return RecordOutcome{}, err
			}
			var refusal error
			if plan, refusal, err = planOfferRoom(ctx, s.db, profile, bounds, r.SenderKey, int64(len(record))); err != nil {
				return RecordOutcome{}, err
			} else if refusal != nil {
				return refuse(refusal)
			}
		}
		err = s.receiveRecordTx(ctx, r, record, bounds, plan, mark, now, &out, changes)
		if !errors.Is(err, errStaleMark) {
			break
		}
		if attempt == offerAttempts {
			return refuse(fmt.Errorf("%w: the holder's books kept changing while an offer was being made room for", ErrBusy))
		}
	}
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

// offerAttempts is how many times Receive makes room for an offer while the
// holder's books change under it before it refuses (ErrBusy).
const offerAttempts = 3

// errStaleMark rolls back a transaction whose offer plan an answer
// committed since may have changed.
var errStaleMark = errors.New("the holder's books changed")

// ownMark is a monotonic mark of the holder's own books (the journal and
// every deal book): their records and log entries. Received records are not
// among them.
type ownMark struct{ records, entries int64 }

func readOwnMark(ctx context.Context, q queryer) (ownMark, error) {
	var m ownMark
	var logs int
	if err := q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM book_records), (SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'cll_entries')`).Scan(&m.records, &logs); err != nil || logs == 0 {
		// A file whose logs have no table yet has no entry.
		return m, err
	}
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM cll_entries WHERE log_id IN (SELECT log_id FROM shelf_logs WHERE kind IN ('book', 'journal'))`).Scan(&m.entries)
	return m, err
}

// receiveRecordTx stores an admitted record in one write transaction, with
// the offer plan's drops; changes are written to the part's own set.
func (s *Shelf) receiveRecordTx(ctx context.Context, r ReceivedRecord, record []byte, bounds ReceiveBounds, plan offerPlan, mark ownMark, now string, out *RecordOutcome, changes changeSet) error {
	key := logKey{r.SenderKey, r.LogID}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if r.Offer {
			if got, err := readOwnMark(ctx, tx); err != nil {
				return err
			} else if got != mark {
				return errStaleMark
			}
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ?`, r.SenderKey, r.LogID).Scan(&count); err != nil {
			return err
		}
		if count >= bounds.MaxRecordsPerLog {
			return errRefusal{fmt.Errorf("%w: %d records held from %s in %s, the store's bound", ErrOverLimit, count, r.SenderKey, r.LogID)}
		}
		before := map[logKey]logState{}
		remember := func(k logKey) error {
			if _, ok := before[k]; ok {
				return nil
			}
			st, err := readLogState(ctx, tx, k)
			before[k] = st
			return err
		}
		if err := remember(key); err != nil {
			return err
		}
		for _, digest := range plan.answered {
			if _, err := tx.ExecContext(ctx, `UPDATE shelf_received SET offer = 0 WHERE digest = ?`, digest); err != nil {
				return err
			}
		}
		for _, row := range plan.drop {
			if err := remember(logKey{row.sender, row.log}); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM shelf_received WHERE digest = ?`, row.digest); err != nil {
				return err
			}
		}
		if err := vacate(ctx, tx, plan.drop, true, bounds); err != nil {
			return err
		}
		forks, err := forkEvidence(ctx, tx, r)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO shelf_received (digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Digest, r.SenderKey, r.LogID, r.Position, r.Prev, r.DealID, r.Offer, record, now); err != nil {
			return err
		}
		// A conflict in a log of offers alone counts under the offer bounds;
		// one in a log that holds, or held, any other record is kept for good.
		offersOnly := false
		if len(forks) > 0 && r.Offer {
			var others int
			if err := tx.QueryRowContext(ctx,
				`SELECT (SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ? AND offer = 0) + (SELECT COUNT(*) FROM shelf_received_vacated WHERE sender_key = ? AND log_id = ? AND offer = 0)`,
				r.SenderKey, r.LogID, r.SenderKey, r.LogID).Scan(&others); err != nil {
				return err
			}
			offersOnly = others == 0
		}
		for _, f := range forks {
			fresh, err := noteConflict(ctx, tx, key, EvidenceFork, f.position, f.held, record, now, offersOnly, bounds)
			if err != nil {
				return err
			}
			if fresh {
				changes.forked[r.DealID] = true
				if f.deal != "" {
					changes.forked[f.deal] = true
				}
			}
		}
		keys := make([]logKey, 0, len(before))
		for k := range before {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].sender < keys[j].sender || (keys[i].sender == keys[j].sender && keys[i].log < keys[j].log)
		})
		for _, k := range keys {
			prefix, err := receivedPrefix(ctx, tx, k)
			if err != nil {
				return err
			}
			// Held proofs the prefix now reaches are checked against it.
			if err := checkHeldProofs(ctx, tx, k, prefix, now, changes); err != nil {
				return err
			}
			after, err := readLogState(ctx, tx, k)
			if err != nil {
				return err
			}
			report(before[k], after, changes)
			if k == key {
				out.Counted = after.prefix.counts(r.Position, r.Digest)
			}
		}
		out.Stored = true
		out.AskForBook = !out.Counted
		beforeCommit(ctx, "record")
		return nil
	})
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func tombstoned(ctx context.Context, q queryer, digest string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_received_tombstones WHERE digest = ?`, digest).Scan(&n)
	return n > 0, err
}

// receivedRow is a stored record's columns but its bytes.
type receivedRow struct {
	digest, sender, log, prev, deal string
	position                        uint64
	offer                           bool
}

func (r receivedRow) record() ReceivedRecord {
	return ReceivedRecord{Digest: r.digest, SenderKey: r.sender, LogID: r.log, Position: r.position, Prev: r.prev, DealID: r.deal, Offer: r.offer}
}

const receivedColumns = `digest, sender_key, log_id, position, prev, deal_id, offer`

func scanReceived(rows *sql.Rows) ([]receivedRow, error) {
	defer rows.Close()
	var out []receivedRow
	for rows.Next() {
		var r receivedRow
		if err := rows.Scan(&r.digest, &r.sender, &r.log, &r.position, &r.prev, &r.deal, &r.offer); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// offerPlan is what making room for an offer does: the offers the profile
// said were answered, which leave the offer bounds, and the unanswered ones
// to drop, oldest first.
type offerPlan struct {
	answered []string
	drop     []receivedRow
}

// planOfferRoom plans the room for one more offer of size bytes from sender
// (§3): the oldest unanswered offers are dropped until it fits its sender's
// bounds and the total ones. Each offer it would drop is first put to the
// profile's Answered, outside any transaction; an answered one leaves the
// bounds instead. Dropping an offer nobody answered is not a retention
// decision, so it leaves no tombstone. An offer larger than a byte bound
// alone is refused. Receive serializes every writer of the store, so the
// plan holds until its transaction applies it.
func planOfferRoom(ctx context.Context, q queryer, profile ReceiveProfile, b ReceiveBounds, sender string, size int64) (offerPlan, error, error) {
	if size > b.MaxOfferBytesPerSender || size > b.MaxOfferBytes {
		return offerPlan{}, fmt.Errorf("%w: an offer of %d bytes, over the store's offer bound", ErrOverLimit, size), nil
	}
	rows, err := q.QueryContext(ctx, `SELECT `+receivedColumns+`, length(record) FROM shelf_received WHERE offer = 1 ORDER BY received_at, rowid`)
	if err != nil {
		return offerPlan{}, nil, err
	}
	type held struct {
		row  receivedRow
		size int64
		gone bool
	}
	var offers []*held
	for rows.Next() {
		h := &held{}
		if err := rows.Scan(&h.row.digest, &h.row.sender, &h.row.log, &h.row.position, &h.row.prev, &h.row.deal, &h.row.offer, &h.size); err != nil {
			_ = rows.Close()
			return offerPlan{}, nil, err
		}
		offers = append(offers, h)
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return offerPlan{}, nil, err
	}
	var plan offerPlan
	for _, scope := range []struct {
		of            func(*held) bool
		count, nbytes int64
	}{
		{func(h *held) bool { return h.row.sender == sender }, b.MaxOffersPerSender, b.MaxOfferBytesPerSender},
		{func(*held) bool { return true }, b.MaxOffers, b.MaxOfferBytes},
	} {
		for {
			var count, bytes int64
			var oldest *held
			for _, h := range offers {
				if !h.gone && scope.of(h) {
					count, bytes = count+1, bytes+h.size
					if oldest == nil {
						oldest = h
					}
				}
			}
			if count+1 <= scope.count && bytes+size <= scope.nbytes {
				break
			}
			if oldest == nil {
				return offerPlan{}, nil, fmt.Errorf("%w: the offer bounds count offers the store does not list", ErrCorrupt)
			}
			oldest.gone = true
			answered, err := profile.Answered(ctx, oldest.row.record())
			if err != nil {
				return offerPlan{}, nil, err
			}
			if answered {
				plan.answered = append(plan.answered, oldest.row.digest)
			} else {
				plan.drop = append(plan.drop, oldest.row)
			}
		}
	}
	return plan, nil, nil
}

// vacate remembers the positions deleted records held (dropped offers, or
// records Forget deleted), so another record arriving there later is a fork,
// and returns every proof of their logs that reaches a deleted position to
// unchecked: its coverage needs the records again. A dropped offer's row
// (offers) counts under the offer bounds, and past them is not kept; a row
// Forget leaves is kept for good.
func vacate(ctx context.Context, tx *sql.Tx, rows []receivedRow, offers bool, bounds ReceiveBounds) error {
	lowest := map[logKey]uint64{}
	for _, r := range rows {
		keep := true
		if offers {
			var err error
			if keep, err = offerRoom(ctx, tx, r.sender, bounds); err != nil {
				return err
			}
		}
		if keep {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO shelf_received_vacated (sender_key, log_id, position, digest, deal_id, offer) VALUES (?, ?, ?, ?, ?, ?)`,
				r.sender, r.log, r.position, r.digest, r.deal, offers); err != nil {
				return err
			}
		}
		k := logKey{r.sender, r.log}
		if p, ok := lowest[k]; !ok || r.position < p {
			lowest[k] = r.position
		}
	}
	for k, position := range lowest {
		sizes, err := tx.QueryContext(ctx, `SELECT DISTINCT size FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND verified = ?`, k.sender, k.log, proofRecomputed)
		if err != nil {
			return err
		}
		var reset []uint64
		for sizes.Next() {
			var size uint64
			if err := sizes.Scan(&size); err != nil {
				_ = sizes.Close()
				return err
			}
			if leaves, _ := mmr.LeafCount(size); leaves >= position {
				reset = append(reset, size)
			}
		}
		if err := firstErr(sizes.Err(), sizes.Close()); err != nil {
			return err
		}
		for _, size := range reset {
			if _, err := tx.ExecContext(ctx, `UPDATE shelf_received_proofs SET verified = ? WHERE sender_key = ? AND log_id = ? AND size = ?`, proofUnchecked, k.sender, k.log, size); err != nil {
				return err
			}
		}
	}
	return nil
}

// countedRecord is one record of a counted prefix.
type countedRecord struct{ digest, deal string }

// receivedPrefixState is a sender's counted prefix in one log: the longest
// run of positions from the first, each holding one record, before the
// first fork.
type receivedPrefixState struct {
	// counted are the counted records, by position from 1.
	counted []countedRecord
	// forkAt is the lowest position with a fork, 0 for none: the prefix
	// ends before it for good.
	forkAt uint64
	// deals are the deal ids of every record held from the sender in the log.
	deals []string
}

func (p receivedPrefixState) counts(position uint64, digest string) bool {
	return position >= 1 && position <= uint64(len(p.counted)) && p.counted[position-1].digest == digest
}

func (p receivedPrefixState) digests(n uint64) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = p.counted[i].digest
	}
	return out
}

// receivedPrefix computes a counted prefix from the store. The forks are
// the ones recorded (shelf_received_conflicts), which deleting a record
// never undoes, and any the records held show: a position whose claimed
// digests differ (the records held there, and the predecessor each record
// at the next position names).
// receivedPrefixBetween, when set (tests), runs between receivedPrefix's
// read of the records and its read of the recorded forks.
var receivedPrefixBetween func(ctx context.Context)

// snapshotPrefix is receivedPrefix outside a write transaction: its two
// reads run in one read-only transaction, so the records and the recorded
// forks are of one store state (evidencebook#543).
func snapshotPrefix(ctx context.Context, db *sql.DB, k logKey) (receivedPrefixState, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return receivedPrefixState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return receivedPrefix(ctx, tx, k)
}

func receivedPrefix(ctx context.Context, q queryer, k logKey) (receivedPrefixState, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+receivedColumns+` FROM shelf_received WHERE sender_key = ? AND log_id = ? ORDER BY position, digest`, k.sender, k.log)
	if err != nil {
		return receivedPrefixState{}, err
	}
	held, err := scanReceived(rows)
	if err != nil {
		return receivedPrefixState{}, err
	}
	if receivedPrefixBetween != nil {
		receivedPrefixBetween(ctx)
	}
	var out receivedPrefixState
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MIN(at), 0) FROM shelf_received_conflicts WHERE sender_key = ? AND log_id = ? AND kind = ?`, k.sender, k.log, EvidenceFork).Scan(&out.forkAt); err != nil {
		return receivedPrefixState{}, err
	}
	at := map[uint64][]receivedRow{}
	claims := map[uint64]map[string]bool{}
	claim := func(p uint64, d string) {
		if claims[p] == nil {
			claims[p] = map[string]bool{}
		}
		claims[p][d] = true
	}
	deals := map[string]bool{}
	for _, r := range held {
		at[r.position] = append(at[r.position], r)
		claim(r.position, r.digest)
		if r.position > 1 {
			claim(r.position-1, r.prev)
		}
		deals[r.deal] = true
	}
	for p, c := range claims {
		if len(c) > 1 && (out.forkAt == 0 || p < out.forkAt) {
			out.forkAt = p
		}
	}
	for p := uint64(1); len(at[p]) == 1 && (out.forkAt == 0 || p < out.forkAt); p++ {
		out.counted = append(out.counted, countedRecord{digest: at[p][0].digest, deal: at[p][0].deal})
	}
	for d := range deals {
		out.deals = append(out.deals, d)
	}
	sort.Strings(out.deals)
	return out, nil
}

// heldFork is a held record a new one forks from, at a position.
type heldFork struct {
	position uint64
	// held is the held record, or the digest of a deleted one.
	held []byte
	deal string
}

// forkEvidence finds the held records r contradicts (§3): another record at
// its position, or a record at the next position naming another
// predecessor; and, at the position before, a record other than the
// predecessor r names, or another record there naming another one. A
// record deleted at either position (shelf_received_vacated) claims its
// digest there still, so another record arriving there is a fork.
func forkEvidence(ctx context.Context, tx *sql.Tx, r ReceivedRecord) ([]heldFork, error) {
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
				found = &heldFork{position: c.position, held: b, deal: deal}
			}
		}
		if err := firstErr(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		if found == nil {
			var digest, deal string
			switch err := tx.QueryRowContext(ctx,
				`SELECT digest, deal_id FROM shelf_received_vacated WHERE sender_key = ? AND log_id = ? AND position = ? AND digest != ? ORDER BY digest LIMIT 1`,
				r.SenderKey, r.LogID, c.position, c.digest).Scan(&digest, &deal); {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return nil, err
			default:
				found = &heldFork{position: c.position, held: []byte(digest), deal: deal}
			}
		}
		if found != nil {
			out = append(out, *found)
		}
	}
	return out, nil
}

// offerRoom reports whether one more row an unsolicited offer leaves
// behind fits the offer bounds: the dropped offers' positions and the
// conflicts in logs of offers alone, per sender and in total.
func offerRoom(ctx context.Context, tx *sql.Tx, sender string, bounds ReceiveBounds) (bool, error) {
	var mine, all int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(sender_key = ?), 0), COUNT(*) FROM (
		SELECT sender_key FROM shelf_received_vacated WHERE offer = 1
		UNION ALL SELECT sender_key FROM shelf_received_conflicts WHERE offer = 1)`, sender).Scan(&mine, &all); err != nil {
		return false, err
	}
	return mine < bounds.MaxOfferEvidencePerSender && all < bounds.MaxOfferEvidence, nil
}

// noteConflict records a conflict of one kind at a position or size of a
// sender's log the first time only, with its evidence: a sender sending ever
// new conflicts at one place adds no evidence. A conflict in a log of
// unsolicited offers alone (offers) counts under the offer bounds; past
// them neither the conflict nor its evidence is kept, so the counted prefix
// ends there only while both records are held. It reports whether the
// conflict is new: recorded by this call, or past the bounds.
func noteConflict(ctx context.Context, tx *sql.Tx, k logKey, kind string, at uint64, a, b []byte, now string, offers bool, bounds ReceiveBounds) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelf_received_conflicts WHERE sender_key = ? AND log_id = ? AND kind = ? AND at = ?`, k.sender, k.log, kind, at).Scan(&n); err != nil || n > 0 {
		return false, err
	}
	if offers {
		if keep, err := offerRoom(ctx, tx, k.sender, bounds); err != nil || !keep {
			return err == nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO shelf_evidence (kind, a, b, noted_at) VALUES (?, ?, ?, ?)`, kind, a, b, now)
	if err != nil {
		return false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO shelf_received_conflicts (sender_key, log_id, kind, at, evidence_id, offer) VALUES (?, ?, ?, ?, ?, ?)`, k.sender, k.log, kind, at, id, offers)
	return err == nil, err
}

// Proof states (shelf_received_proofs.verified): not yet reached by the
// records held, its root recomputed from them, or contradicted by them.
const (
	proofUnchecked    = 0
	proofRecomputed   = 1
	proofContradicted = 2
)

// checkHeldProofs checks every unchecked held proof of k whose size the
// prefix now reaches against the records: one whose root they recompute
// grants coverage, one whose root they do not is kept as evidence and its
// deals reported forked.
func checkHeldProofs(ctx context.Context, tx *sql.Tx, k logKey, p receivedPrefixState, now string, changes changeSet) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT size, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND verified = ? ORDER BY size`, k.sender, k.log, proofUnchecked)
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
		cp, err := checkpointFromStatement(h.cp)
		if err != nil {
			return err
		}
		if cp.Entries > uint64(len(p.counted)) {
			continue
		}
		match, over, err := rootRecomputes(cp, p.digests(cp.Entries))
		if err != nil {
			return err
		}
		state := proofRecomputed
		if !match {
			state = proofContradicted
			fresh, err := noteConflict(ctx, tx, k, EvidenceCheckpointRecords, h.size, h.cp, over, now, false, ReceiveBounds{})
			if err != nil {
				return err
			}
			if fresh {
				for _, d := range p.deals {
					changes.forked[d] = true
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE shelf_received_proofs SET verified = ? WHERE sender_key = ? AND log_id = ? AND size = ?`, state, k.sender, k.log, h.size); err != nil {
			return err
		}
	}
	return nil
}

// verifiedCoverage is how many counted records of k a held proof whose
// root they recomputed covers.
func verifiedCoverage(ctx context.Context, q queryer, k logKey, p receivedPrefixState) (uint64, error) {
	var size uint64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(size), 0) FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND verified = ?`, k.sender, k.log, proofRecomputed).Scan(&size); err != nil {
		return 0, err
	}
	leaves, ok := mmr.LeafCount(size)
	if !ok {
		return 0, fmt.Errorf("%w: a held proof's size %d is not a complete MMR", ErrCorrupt, size)
	}
	return min(leaves, uint64(len(p.counted))), nil
}

// rootRecomputes reports whether records recompute a book checkpoint's
// root, given exactly its entries' digests, and returns them as a JSON array.
func rootRecomputes(cp Checkpoint, digests []string) (bool, []byte, error) {
	encoded, err := json.Marshal(digests)
	if err != nil {
		return false, nil, err
	}
	tree, err := mmr.New(nil)
	if err != nil {
		return false, nil, err
	}
	for _, d := range digests {
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

// CountedPrefix computes a sender's counted prefix in one log from the
// store. It takes no lock: a StatusProvider or ReceiveProfile may call it.
func (s *Shelf) CountedPrefix(ctx context.Context, senderKey, logID string) (CountedPrefix, error) {
	if err := s.live(); err != nil {
		return CountedPrefix{}, err
	}
	p, err := snapshotPrefix(ctx, s.db, logKey{senderKey, logID})
	if err != nil {
		return CountedPrefix{}, err
	}
	out := CountedPrefix{Size: uint64(len(p.counted)), ForkAt: p.forkAt}
	if len(p.counted) > 0 {
		out.Last = p.counted[len(p.counted)-1].digest
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
// under retention reports ErrDeleted, one never held ErrNotFound. It takes
// no lock: a StatusProvider or ReceiveProfile may call it.
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
// it reports it deleted, not missing. The deal's proofs, evidence and the
// forks found stay: a fork's position still ends its sender's counted
// prefix, another record arriving at a deleted position is a fork there,
// and a proof reaching a deleted position grants no coverage until the
// records it was checked against are held again. It returns the digests deleted. A call racing Release may fail
// with database/sql's closed-database error rather than ErrReleased.
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
		rows, err := tx.QueryContext(ctx, `SELECT `+receivedColumns+`, received_at FROM shelf_received WHERE deal_id = ? AND received_at <= ? ORDER BY digest`, dealID, cutoff)
		if err != nil {
			return err
		}
		type row struct {
			receivedRow
			at string
		}
		var all []row
		var gone []receivedRow
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.digest, &r.sender, &r.log, &r.position, &r.prev, &r.deal, &r.offer, &r.at); err != nil {
				_ = rows.Close()
				return err
			}
			all = append(all, r)
			gone = append(gone, r.receivedRow)
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
		return vacate(ctx, tx, gone, false, ReceiveBounds{})
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
// checkpoints, oldest first. It takes no lock: a StatusProvider or
// ReceiveProfile may call it.
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
