// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"

	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
	"os"
	"sort"
	"time"
)

// The shelf log of book facts (evidencebook#461, docs/design/461-shelf-facts.md
// §2.4, §2.5 and §4): after Migrate, every tick of the cadence is a stamp. A
// stamp appends one fact for each log whose fact would differ from its last
// one, then a stamp entry, and checkpoints the cadence log, which alone goes
// to the witness. A stamp is a cadence tick whose stored leaves carry its
// batch (TickLeaves.Stamp), so the tick's storage, recovery, witnessing and
// card anchor are the cadence's own: a stamp stored but not finished is
// finished first, as the same stamp, before any checkpoint is cut.

// ShelfVersionFacts is the shelf file version once Migrate has switched the
// shelf to a log of book facts. An older build refuses it.
const ShelfVersionFacts = 2

// Migration states (shelf_migration.state).
const (
	migrationBackingUp = "backing_up"
	migrationMigrating = "migrating"
	migrationDone      = "done"
)

// StatusProvider gives a book's profile and the values its fact commits to
// (§2.4): the shelf asks it for each book at each stamp. A book no provider
// claims (ok false) has profile "none", the empty link value and {}.
type StatusProvider interface {
	Values(ctx context.Context, bookID, logID string) (profile string, link LinkValue, status json.RawMessage, ok bool, err error)
}

// BookStatusProvider is a StatusProvider handed what it reads by the shelf,
// so it reads nothing through the shelf's own lock, which a stamp holds
// while it asks for values: the stamp's tick and the book and journal
// handles the shelf already has (docs/design/461-deal-profile.md §8.1). A
// shelf calls BookValues in place of StampValues or Values when its provider
// implements it. The provider may call the handles' readers (Query,
// ResolvePayload, Sealed) and the shelf's lock-free readers (CountedPrefix,
// ReceivedRecordOf, RecordTime, RecordStamp and the rest), never another
// Shelf method.
type BookStatusProvider interface {
	StatusProvider
	BookValues(ctx context.Context, in StatusInput) (profile string, link LinkValue, status json.RawMessage, ok bool, err error)
}

// StatusInput is what a BookStatusProvider is handed for one book's fact: the
// stamp's tick n (from 1), the book's id and log id, the book itself, and the
// shelf's journal, nil when the shelf has none yet: before Migrate, or
// before the host first opens it with Shelf.Journal. A stamp never creates
// the journal.
type StatusInput struct {
	N       uint64
	BookID  string
	LogID   string
	Book    *Book
	Journal *Book
}

// BeforeStampProvider is a StatusProvider that writes, before a stamp, what
// that stamp's values must read: x-deal-v1 names each counted ruling a
// Receive left unnamed (evidencebook#527), so a naming that failed is
// retried by the holder's next stamp whether or not anything more arrives.
// Stamp, and Tick when a stamp is due, call BeforeStamp before they take the
// shelf's lock, so it may call the shelf's methods, but not Stamp or Tick. at
// is the time the stamp is taken at. Its error does not stop the stamp:
// TickResult.BeforeStamp reports it, and the next stamp calls it again.
type BeforeStampProvider interface {
	StatusProvider
	BeforeStamp(ctx context.Context, at time.Time) error
}

// beforeStamp runs the provider's BeforeStamp, if it has one, and gives its
// error as text.
func (c *Cadence) beforeStamp(ctx context.Context, now time.Time) string {
	p, ok := c.cfg.Status.(BeforeStampProvider)
	if !ok {
		return ""
	}
	if err := p.BeforeStamp(ctx, now); err != nil {
		return err.Error()
	}
	return ""
}

// StampStatusProvider is a StatusProvider that is also told the stamp it
// gives values for: n, the tick the stamp will have (from 1), which a status
// value may use to name a record its own stamp is the first to cover
// (docs/design/461-deal-profile.md §8.1, "Times"). A shelf calls StampValues
// in place of Values when its provider implements it. BookStatusProvider,
// which is also handed the book, supersedes it.
type StampStatusProvider interface {
	StatusProvider
	StampValues(ctx context.Context, n uint64, bookID, logID string) (profile string, link LinkValue, status json.RawMessage, ok bool, err error)
}

// StampBatch is a stamp's stored batch: its entry documents in append order,
// the stamp entry last. Bytes are base64 in JSON.
type StampBatch struct {
	V       int          `json:"v"`
	Entries []BatchEntry `json:"entries"`
}

// BatchEntry is one entry of a batch: its document and, for a fact, the
// openings of its two commitments.
type BatchEntry struct {
	Kind          string `json:"kind"`
	BookID        string `json:"book_id,omitempty"`
	LogID         string `json:"log_id,omitempty"`
	Doc           []byte `json:"doc"`
	LinkOpening   []byte `json:"link_opening,omitempty"`
	StatusOpening []byte `json:"status_opening,omitempty"`
}

// Migrated reports whether the shelf's cadence log is a log of book facts.
func (c *Cadence) Migrated(ctx context.Context) (bool, error) {
	state, err := migrationState(ctx, c.shelf.db)
	return state == migrationDone, err
}

func migrationState(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	var state string
	err := db.QueryRowContext(ctx, `SELECT state FROM shelf_migration WHERE id = 1`).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}

// Migrate switches the shelf to a log of book facts (§4). It is explicit and
// one-way, and recoverable at every step: a call after a crash resumes from
// the stored state. It first sets the file to version 2, so no older build
// writes it from then on, and then backs it up to <path>.v1.bak, a version 1
// file an older build opens. It refuses a <path>.v1.bak that is not this
// migration's backup, naming that path, and leaves the file at version 2.
func (c *Cadence) Migrate(ctx context.Context, now time.Time) error {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return ErrReleased
	}
	state, err := migrationState(ctx, s.db)
	if err != nil {
		return err
	}
	if state == migrationDone {
		return nil
	}
	if state == "" {
		// Step 1: no tree root is ever appended after the marker, so a tick
		// stored but not finished is finished by today's code first.
		if last, ok, err := c.lastLocked(ctx); err != nil {
			return err
		} else if ok && last.CheckpointSize == 0 {
			if _, err := c.finishLocked(ctx, last); err != nil {
				return err
			}
		}
		// Step 2, first transaction: from here no older build writes the file.
		if err := s.inTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE shelf_meta SET version = ? WHERE id = 1`, ShelfVersionFacts); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, ?, '')`, migrationBackingUp)
			return err
		}); err != nil {
			return err
		}
		state = migrationBackingUp
	}
	if state == migrationBackingUp {
		if err := c.backupLocked(ctx); err != nil {
			return err
		}
		if err := c.storeMigrationBatchLocked(ctx, now); err != nil {
			return err
		}
	}
	// Step 4: append through the cadence log's own handle, as a stamp is.
	last, ok, err := c.lastLocked(ctx)
	if err != nil {
		return err
	}
	if !ok || last.Leaves.Stamp == nil {
		return fmt.Errorf("%w: the migration's stamp is not stored", ErrCorrupt)
	}
	if last.CheckpointSize == 0 {
		if _, err := c.finishStampLocked(ctx, last); err != nil {
			return err
		}
	}
	// Step 5.
	_, err = s.db.ExecContext(ctx, `UPDATE shelf_migration SET state = ? WHERE id = 1`, migrationDone)
	return err
}

// backupLocked writes <path>.v1.bak (§4 step 2): a consistent snapshot by
// VACUUM INTO, set back to version 1 with no migration row, renamed into
// place only once its digest is recorded, and reused on a restart.
func (c *Cadence) backupLocked(ctx context.Context) error {
	s := c.shelf
	final, tmp := s.cfg.Path+".v1.bak", s.cfg.Path+".v1.bak.tmp"
	var recorded string
	if err := s.db.QueryRowContext(ctx, `SELECT backup_sha256 FROM shelf_migration WHERE id = 1`).Scan(&recorded); err != nil {
		return err
	}
	if recorded != "" {
		if got, err := fileSHA256(final); err == nil {
			if got != recorded {
				return fmt.Errorf("%w: %s does not match the backup this migration recorded; move it aside", ErrInvalid, final)
			}
			return nil
		}
		if got, err := fileSHA256(tmp); err == nil && got == recorded {
			return os.Rename(tmp, final)
		}
	}
	if _, err := os.Stat(final); err == nil {
		return fmt.Errorf("%w: %s exists and is not this migration's backup; move it aside", ErrInvalid, final)
	}
	// A temporary file from an attempt that did not finish: VACUUM INTO
	// refuses an existing file.
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("back up the shelf: %w", err)
	}
	if err := resetBackup(ctx, tmp); err != nil {
		return err
	}
	digest, err := fileSHA256(tmp)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE shelf_migration SET backup_sha256 = ? WHERE id = 1`, digest); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// resetBackup makes the snapshot the version 1 file it was before step 2.
func resetBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, statement := range []string{
		`UPDATE shelf_meta SET version = 1 WHERE id = 1`,
		`DELETE FROM shelf_migration`,
		`PRAGMA journal_mode=DELETE`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("reset the backup to version 1: %w", err)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// storeMigrationBatchLocked stores the marker, the genesis facts and a stamp
// entry as the migration's stamp (§4 step 3), with state migrating, in one
// transaction.
func (c *Cadence) storeMigrationBatchLocked(ctx context.Context, now time.Time) error {
	s := c.shelf
	last, ok, err := c.lastLocked(ctx)
	if err != nil {
		return err
	}
	var lastTick uint64
	if ok {
		lastTick = last.N
	}
	logs, err := s.cadenceLogIDs(ctx)
	if err != nil {
		return err
	}
	at := now.UTC().Truncate(time.Minute)
	var sizes []LoggedSize
	var facts []pendingFact
	for _, id := range logs {
		log, err := s.cadenceLogLocked(ctx, id)
		if err != nil {
			return err
		}
		size, err := log.MMRSize(ctx)
		if err != nil {
			return err
		}
		sizes = append(sizes, LoggedSize{LogID: id, Size: size})
		if size == 0 {
			continue
		}
		f, err := c.factFor(ctx, lastTick+1, log, at, nil)
		if err != nil {
			return err
		}
		facts = append(facts, f)
	}
	marker, err := newFormatSwitch(lastTick, sizes)
	if err != nil {
		return err
	}
	markerDoc, err := canonicalJSON(marker)
	if err != nil {
		return err
	}
	tick, err := c.buildStampLocked(ctx, lastTick+1, at, now, facts, []BatchEntry{{Kind: EntryKindFormatSwitch, Doc: markerDoc}})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(tick.Leaves)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)`,
			tick.N, tick.At.Format(time.RFC3339), tick.DueNext.Format(time.RFC3339), string(encoded)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE shelf_migration SET state = ? WHERE id = 1`, migrationMigrating)
		return err
	})
}

// pendingFact is one log's fact before its salts are drawn.
type pendingFact struct {
	input     FactInput
	statement []byte
	grown     bool
	// journal marks the journal's fact (§2.2): its size, checkpoint digest
	// and previous size go inside its status commitment.
	journal bool
}

// cadenceLogIDs is every log of the lifetime table, sorted.
func (s *Shelf) cadenceLogIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT log_id FROM cadence_logs ORDER BY log_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// lastFact is the newest fact the shelf log holds for a log.
type lastFact struct {
	fact         BookFact
	link, status json.RawMessage
	found        bool
}

func (s *Shelf) lastFactOf(ctx context.Context, logID string) (lastFact, error) {
	var doc, lo, so []byte
	var kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, doc, link_opening, status_opening FROM shelf_entries WHERE log_id = ? AND kind IN (?, ?) ORDER BY entry_seq DESC LIMIT 1`,
		logID, FactKindBook, FactKindJournal).Scan(&kind, &doc, &lo, &so)
	if errors.Is(err, sql.ErrNoRows) {
		return lastFact{}, nil
	}
	if err != nil {
		return lastFact{}, err
	}
	var out lastFact
	var link, status FactOpening
	if err := errors.Join(json.Unmarshal(doc, &out.fact), json.Unmarshal(lo, &link), json.Unmarshal(so, &status)); err != nil {
		return lastFact{}, fmt.Errorf("%w: a stored fact of %s: %v", ErrCorrupt, logID, err)
	}
	out.link, out.status, out.found = link.Value, status.Value, true
	if kind == FactKindJournal {
		// The journal's true size and checkpoint are its status value's.
		var js JournalStatus
		if err := json.Unmarshal(status.Value, &js); err != nil {
			return lastFact{}, fmt.Errorf("%w: a stored journal fact: %v", ErrCorrupt, err)
		}
		out.fact.Size, out.fact.CheckpointSHA256, out.fact.PrevFactSize = js.Size, js.CheckpointSHA256, js.PrevFactSize
	}
	return out, nil
}

// bookIDOf is a log's book id (§2.2): its first record's id for a book, its
// first CLL entry's id for a host log.
func (s *Shelf) bookIDOf(ctx context.Context, log CadenceLog) (string, error) {
	if b, ok := log.(bookLog); ok {
		var id string
		err := s.db.QueryRowContext(ctx, `SELECT record_id FROM book_records WHERE book_id = ? ORDER BY seq LIMIT 1`, b.book.id).Scan(&id)
		return id, err
	}
	entries, err := log.substrate().(*CLLSubstrate).Entries(ctx, 0, 1)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("%w: log %s has no entry", ErrInvalid, log.LogID())
	}
	return entries[0].RecordID, nil
}

// factFor is what a log's fact would be now (§2.4); last is its previous
// fact, or nil for a genesis fact. grown says whether its checkpoint grew.
func (c *Cadence) factFor(ctx context.Context, n uint64, log CadenceLog, at time.Time, last *lastFact) (pendingFact, error) {
	s := c.shelf
	journal := false
	if _, ok := log.(bookLog); ok && log.LogID() == JournalLogID {
		kind, err := s.logKind(ctx, log.LogID())
		if err != nil {
			return pendingFact{}, err
		}
		journal = kind == logKindJournal
	}
	var bookID string
	var err error
	if journal {
		sum := sha256.Sum256([]byte(log.LogID()))
		bookID = hex.EncodeToString(sum[:])
	} else if bookID, err = s.bookIDOf(ctx, log); err != nil {
		return pendingFact{}, err
	}
	profile, link, status := ProfileNone, LinkValue{}, json.RawMessage(nil)
	// Only a book is described by a profile; a host log's fact is always
	// profile "none" with the empty link value and {} (§2.4, §4).
	if _, isBook := log.(bookLog); isBook && !journal && c.cfg.Status != nil {
		var p string
		var l LinkValue
		var st json.RawMessage
		var ok bool
		switch sp := c.cfg.Status.(type) {
		case BookStatusProvider:
			in := StatusInput{N: n, BookID: bookID, LogID: log.LogID(), Book: log.(bookLog).book}
			if in.Journal, err = s.existingJournalLocked(ctx); err != nil {
				return pendingFact{}, err
			}
			p, l, st, ok, err = sp.BookValues(ctx, in)
		case StampStatusProvider:
			p, l, st, ok, err = sp.StampValues(ctx, n, bookID, log.LogID())
		default:
			p, l, st, ok, err = c.cfg.Status.Values(ctx, bookID, log.LogID())
		}
		if err != nil {
			return pendingFact{}, &statusError{err}
		}
		if ok {
			profile, link, status = p, l, st
		}
	}
	size, err := log.MMRSize(ctx)
	if err != nil {
		return pendingFact{}, err
	}
	in := FactInput{Profile: profile, BookID: bookID, LogID: log.LogID(), Size: size, Link: link, Status: status}
	if last != nil && last.found {
		in.PrevFactSize = last.fact.Size
	}
	grown := last == nil || !last.found || size > last.fact.Size
	var statement []byte
	if grown {
		statement, size, err = log.CutLocal(ctx, at)
		if err != nil {
			return pendingFact{}, fmt.Errorf("checkpoint %s for the stamp: %w", log.LogID(), err)
		}
		in.Size = size
		sum := sha256.Sum256(statement)
		in.CheckpointSHA256 = hex.EncodeToString(sum[:])
	} else {
		in.CheckpointSHA256 = last.fact.CheckpointSHA256
	}
	return pendingFact{input: in, statement: statement, grown: grown, journal: journal}, nil
}

// changed reports whether f's fact would differ from last (§2.4): the size
// grew, or a committed value differs. Values are compared, never
// commitments, since the salts differ every time.
func (f pendingFact) changed(last lastFact) (bool, error) {
	if !last.found || f.grown {
		return true, nil
	}
	if f.journal {
		// The journal's values follow its size: unchanged unless it grew.
		return false, nil
	}
	link, err := linkValue(f.input.Link)
	if err != nil {
		return false, err
	}
	status, err := statusValue(f.input.Status)
	if err != nil {
		return false, err
	}
	a, err := canonicalJSON(link)
	if err != nil {
		return false, err
	}
	b, err := canonicalJSON(status)
	if err != nil {
		return false, err
	}
	lastLink, err := decodeJSON(last.link)
	if err != nil {
		return false, err
	}
	lastStatus, err := decodeJSON(last.status)
	if err != nil {
		return false, err
	}
	la, err := canonicalJSON(lastLink)
	if err != nil {
		return false, err
	}
	lb, err := canonicalJSON(lastStatus)
	if err != nil {
		return false, err
	}
	return string(a) != string(la) || string(b) != string(lb), nil
}

// buildStampLocked draws each fact's salts in append order (facts in book id
// order), then the jitter, and returns the stamp as a tick to store.
func (c *Cadence) buildStampLocked(ctx context.Context, n uint64, at, now time.Time, facts []pendingFact, head []BatchEntry) (CadenceTick, error) {
	random := c.shelf.random
	sort.Slice(facts, func(i, j int) bool {
		a, b := facts[i].input, facts[j].input
		return a.BookID < b.BookID || (a.BookID == b.BookID && a.LogID < b.LogID)
	})
	batch := StampBatch{V: 1, Entries: head}
	leaves := TickLeaves{Deals: map[string]TickLeaf{}}
	for _, f := range facts {
		kind := FactKindBook
		var fact BookFact
		var lo, so FactOpening
		var err error
		if f.journal {
			kind = FactKindJournal
			fact, lo, so, err = newJournalFact(f.input.LogID, JournalStatus{Size: f.input.Size, CheckpointSHA256: f.input.CheckpointSHA256, PrevFactSize: f.input.PrevFactSize}, random)
		} else {
			fact, lo, so, err = newBookFact(f.input, random)
		}
		if err != nil {
			return CadenceTick{}, err
		}
		doc, err := canonicalJSON(fact)
		if err != nil {
			return CadenceTick{}, err
		}
		// Openings are stored as their RFC 8785 bytes, as every entry document is.
		loB, err := canonicalJSON(lo)
		if err != nil {
			return CadenceTick{}, err
		}
		soB, err := canonicalJSON(so)
		if err != nil {
			return CadenceTick{}, err
		}
		batch.Entries = append(batch.Entries, BatchEntry{Kind: kind, BookID: fact.BookID, LogID: fact.LogID, Doc: doc, LinkOpening: loB, StatusOpening: soB})
		// A journal fact's leaf keeps its true size and checkpoint, which
		// only the holder reads: the cards' extents leave the journal out.
		leaf := TickLeaf{LogID: fact.LogID, Size: f.input.Size, CheckpointSHA256: f.input.CheckpointSHA256}
		if f.statement != nil {
			leaf.Statement = base64.RawURLEncoding.EncodeToString(f.statement)
		}
		leaves.Deals[fact.LogID] = leaf
	}
	stampDoc, err := canonicalJSON(StampEntry{V: 1, Kind: EntryKindStamp, Stamp: n, Facts: uint64(len(facts))})
	if err != nil {
		return CadenceTick{}, err
	}
	batch.Entries = append(batch.Entries, BatchEntry{Kind: EntryKindStamp, Doc: stampDoc})
	jitter := time.Duration(0)
	if c.cfg.Jitter > 0 {
		r, err := random.uniform(uint64(2*c.cfg.Jitter) + 1)
		if err != nil {
			return CadenceTick{}, err
		}
		jitter = time.Duration(r) - c.cfg.Jitter
	}
	leaves.Stamp = &batch
	return CadenceTick{N: n, At: at, DueNext: now.UTC().Add(c.cfg.Interval + jitter).Truncate(time.Second), Leaves: leaves}, nil
}

// Stamp takes a stamp now (§2.5): on demand, whatever the heartbeat's
// schedule. A stamp stored but not finished is finished first, as the same
// stamp. It needs a migrated shelf.
func (c *Cadence) Stamp(ctx context.Context, now time.Time) (TickResult, error) {
	before := c.beforeStamp(ctx, now)
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return TickResult{}, ErrReleased
	}
	res, err := c.stampLocked(ctx, now)
	res.BeforeStamp = before
	return res, err
}

func (c *Cadence) stampLocked(ctx context.Context, now time.Time) (TickResult, error) {
	s := c.shelf
	if state, err := migrationState(ctx, s.db); err != nil {
		return TickResult{}, err
	} else if state != migrationDone {
		return TickResult{}, fmt.Errorf("%w: the shelf is not a log of book facts yet; call Migrate", ErrInvalid)
	}
	last, ok, err := c.lastLocked(ctx)
	if err != nil {
		return TickResult{}, err
	}
	if ok && last.CheckpointSize == 0 {
		return c.finishStampLocked(ctx, last)
	}
	n := uint64(1)
	if ok {
		n = last.N + 1
	}
	at := now.UTC().Truncate(time.Minute)
	logs, err := s.cadenceLogIDs(ctx)
	if err != nil {
		return TickResult{}, err
	}
	var facts []pendingFact
	var unstated map[string]string
	for _, id := range logs {
		log, err := s.cadenceLogLocked(ctx, id)
		if err != nil {
			return TickResult{}, err
		}
		if size, err := log.MMRSize(ctx); err != nil {
			return TickResult{}, err
		} else if size == 0 {
			continue
		}
		prev, err := s.lastFactOf(ctx, id)
		if err != nil {
			return TickResult{}, err
		}
		f, err := c.factFor(ctx, n, log, at, &prev)
		var se *statusError
		if errors.As(err, &se) {
			if unstated == nil {
				unstated = map[string]string{}
			}
			unstated[id] = se.err.Error()
			continue
		}
		if err != nil {
			return TickResult{}, err
		}
		if changed, err := f.changed(prev); err != nil {
			return TickResult{}, err
		} else if changed {
			facts = append(facts, f)
		}
	}
	tick, err := c.buildStampLocked(ctx, n, at, now, facts, nil)
	if err != nil {
		return TickResult{}, err
	}
	encoded, err := json.Marshal(tick.Leaves)
	if err != nil {
		return TickResult{}, err
	}
	// Stored before anything is appended, so a crash resumes this stamp.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)`,
		tick.N, tick.At.Format(time.RFC3339), tick.DueNext.Format(time.RFC3339), string(encoded)); err != nil {
		return TickResult{}, err
	}
	res, err := c.finishStampLocked(ctx, tick)
	res.Unstated = unstated
	return res, err
}

// statusError is a status provider's error for one book: a stamp keeps that
// book's last fact rather than fail (TickResult.Unstated).
type statusError struct{ err error }

func (e *statusError) Error() string { return "status values: " + e.err.Error() }
func (e *statusError) Unwrap() error { return e.err }

// finishStampLocked appends the batch's entries not yet in the cadence log,
// in order, records each in shelf_entries, then cuts the checkpoint and
// completes the tick's row. Entries already appended (a crash after some)
// are found after the previous tick's checkpoint, never appended again.
func (c *Cadence) finishStampLocked(ctx context.Context, tick CadenceTick) (TickResult, error) {
	s := c.shelf
	batch := tick.Leaves.Stamp
	if batch == nil || len(batch.Entries) == 0 {
		return TickResult{}, fmt.Errorf("%w: tick %d is not a stamp", ErrCorrupt, tick.N)
	}
	var after uint64
	if tick.N > 1 {
		previous, err := c.tickLocked(ctx, tick.N-1)
		if err != nil {
			return TickResult{}, err
		}
		// Every entry the previous tick's checkpoint covers, an old tick's
		// padding included.
		leaves, ok := mmr.LeafCount(previous.CheckpointSize)
		if !ok {
			return TickResult{}, fmt.Errorf("%w: tick %d's checkpoint size %d is not a complete MMR", ErrCorrupt, previous.N, previous.CheckpointSize)
		}
		after = leaves
	}
	held, err := c.log.Entries(ctx, after, len(batch.Entries)+1)
	if err != nil {
		return TickResult{}, err
	}
	var lastSeq uint64
	for i, e := range batch.Entries {
		sum := sha256.Sum256(e.Doc)
		id := hex.EncodeToString(sum[:])
		var seq uint64
		if i < len(held) {
			if held[i].RecordID != id {
				return TickResult{}, fmt.Errorf("%w: the cadence log holds another entry where stamp %d's entry %d belongs", ErrCorrupt, tick.N, i)
			}
			seq = held[i].Seq
		} else if seq, err = c.log.Append(ctx, id, tick.At); err != nil {
			return TickResult{}, err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO shelf_entries (entry_seq, kind, book_id, log_id, doc, link_opening, status_opening, tick) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			seq, e.Kind, e.BookID, e.LogID, e.Doc, e.LinkOpening, e.StatusOpening, tick.N); err != nil {
			return TickResult{}, err
		}
		lastSeq = seq
	}
	cp, err := c.log.CheckpointWhole(ctx, tick.At)
	if err != nil {
		return TickResult{}, err
	}
	tick.EntrySeq, tick.CheckpointSize, tick.Checkpoint = lastSeq, cp.TreeSize, cp.Statement
	if _, err := s.db.ExecContext(ctx,
		`UPDATE cadence_ticks SET entry_seq = ?, checkpoint_size = ?, checkpoint = ? WHERE tick = ?`,
		tick.EntrySeq, tick.CheckpointSize, tick.Checkpoint, tick.N); err != nil {
		return TickResult{}, err
	}
	return TickResult{Ticked: true, Tick: tick, DueNext: tick.DueNext}, nil
}

// AudienceFact is one fact of a book with the openings an audience may have
// (nil where it may not).
type AudienceFact struct {
	EntrySeq      uint64
	Fact          BookFact
	LinkOpening   *FactOpening
	StatusOpening *FactOpening
}

// Openings is every fact of a book (by its book id) with the openings the
// audience may have, as OpeningsFor says (§2.2). It is the only call that
// hands openings out.
func (s *Shelf) Openings(ctx context.Context, bookID string, audience Audience) ([]AudienceFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	if _, _, err := OpeningsFor(FactKindBook, audience); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_seq, kind, doc, link_opening, status_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) ORDER BY entry_seq`,
		bookID, FactKindBook, FactKindJournal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AudienceFact
	for rows.Next() {
		var seq uint64
		var kind string
		var doc, lo, so []byte
		if err := rows.Scan(&seq, &kind, &doc, &lo, &so); err != nil {
			return nil, err
		}
		link, status, err := OpeningsFor(kind, audience)
		if err != nil {
			return nil, err
		}
		f := AudienceFact{EntrySeq: seq}
		if err := json.Unmarshal(doc, &f.Fact); err != nil {
			return nil, fmt.Errorf("%w: stored fact %d: %v", ErrCorrupt, seq, err)
		}
		if link {
			f.LinkOpening = &FactOpening{}
			if err := json.Unmarshal(lo, f.LinkOpening); err != nil {
				return nil, fmt.Errorf("%w: stored link opening %d: %v", ErrCorrupt, seq, err)
			}
		}
		if status {
			f.StatusOpening = &FactOpening{}
			if err := json.Unmarshal(so, f.StatusOpening); err != nil {
				return nil, fmt.Errorf("%w: stored status opening %d: %v", ErrCorrupt, seq, err)
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// existingJournalLocked is the shelf's journal if the shelf has one, nil
// otherwise: a stamp hands it to a BookStatusProvider and must not create it.
func (s *Shelf) existingJournalLocked(ctx context.Context) (*Book, error) {
	if state, err := migrationState(ctx, s.db); err != nil || state != migrationDone {
		return nil, err
	}
	kind, err := s.logKind(ctx, JournalLogID)
	if err != nil || kind != logKindJournal {
		return nil, err
	}
	return s.openLocked(ctx, JournalLogID)
}
