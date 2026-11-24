// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Window books (evidencebook#689, docs/design/689-window-books.md §2): a
// shelf with a WindowPolicy keeps one book per time window. Each book opens
// with a window_open record that names its window and the previous window's
// final tree, and ends with a window_close record and a final checkpoint
// right after it, so a run of window books checks as one unbroken chain.

// The record types of a window book's first and last records, and of the
// record that places a book in a journal window (evidencebook#797).
const (
	RecordTypeWindowOpen  = "window_open"
	RecordTypeWindowClose = "window_close"
	RecordTypeWindowPlace = "window_place"
)

// The record_version of each window record body. A journal window's close
// is version 2, which lists the books placed in it; an older verifier
// refuses it by its version rather than drop the list.
const (
	WindowOpenVersion    = "window_open/v1"
	WindowCloseVersion   = "window_close/v1"
	WindowCloseVersionV2 = "window_close/v2"
	WindowPlaceVersion   = "window_place/v1"
)

func isWindowRecord(recordType string) bool {
	return recordType == RecordTypeWindowOpen || recordType == RecordTypeWindowClose
}

// WindowTree names a window book's final tree: its log, its leaf count and
// its root. Two signed checkpoints of one tree differ in their issue time,
// so a window is named by its tree, not by one checkpoint's digest.
type WindowTree struct {
	LogID   string `json:"log_id"`
	Entries uint64 `json:"entries"`
	Root    string `json:"root"`
}

// WindowOpenStatement is the body of a window_open record.
type WindowOpenStatement struct {
	RecordVersion string       `json:"record_version"`
	Period        WindowPeriod `json:"period"`
	Label         string       `json:"label"`
	Start         string       `json:"start"`
	End           string       `json:"end"`
	// Previous is the previous window's boundary tree when it had a grace
	// (evidencebook#807), else its final tree; in the shelf's first window
	// it is the predecessor book's final tree for a store that continues one
	// (evidencebook#808), and absent otherwise.
	Previous *WindowTree `json:"previous,omitempty"`
}

// WindowCloseStatement is the body of a window_close record: the window,
// and its records before the close by record_type.
type WindowCloseStatement struct {
	RecordVersion string         `json:"record_version"`
	Period        WindowPeriod   `json:"period"`
	Label         string         `json:"label"`
	Start         string         `json:"start"`
	End           string         `json:"end"`
	CountsByKind  map[string]int `json:"counts_by_kind"`
	// Boundary is the tree the window held when it ended and entered its
	// grace (evidencebook#807): the final tree, which the close record ends,
	// binds it, so a chain cannot swap or drop it. Absent with no grace.
	Boundary *WindowTree `json:"boundary,omitempty"`
	// Placed lists, in a journal window's close (window_close/v2), the books
	// placed in the window that hold their first record at the close, each
	// with its tree then, sorted by log id (evidencebook#797 §3): an answer
	// for the deal must be at that tree or a later one. Absent in version 1.
	Placed []WindowTree `json:"placed,omitempty"`
}

// WindowPlaceStatement is the body of a window_place record: the book it
// places in the journal window that holds the record.
type WindowPlaceStatement struct {
	RecordVersion string `json:"record_version"`
	LogID         string `json:"log_id"`
}

func windowTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// bookWindow is what a window book holds of its window. The shelf sets it
// when it opens the book and changes it under the book's lock.
type bookWindow struct {
	Window
	// graceEnd is when the window's grace ends: its end with no grace. From
	// its end until graceEnd the book takes only a close/v1 Close for a
	// period inside the window (evidencebook#807).
	graceEnd time.Time
	// sealing is set while the shelf appends the window's own open or close
	// record: no other caller can append either.
	sealing bool
	closed  bool
	// bounded is set once the window took its boundary checkpoint: from
	// then on only a Close for its period enters, whatever the clock reads.
	bounded bool
	// final is the window's final checkpoint once it is closed.
	final Checkpoint
}

// admits checks that a record, committed at at, may enter the book. A book
// outside a windowed shelf takes no window record; a window book takes its
// open record only first and only from the shelf, its close record only
// from the shelf, the index roots its own checkpoints commit, every other
// record only inside its window, and in its grace only a close/v1 Close for
// a period inside the window.
func (w *bookWindow) admits(h Header, at time.Time, checkpointing bool) error {
	recordType, seq := h.RecordType, h.Seq
	if w == nil {
		if isWindowRecord(recordType) || recordType == RecordTypeWindowPlace {
			return fmt.Errorf("%w: a %s record belongs to a window book, which a windowed shelf writes", ErrInvalid, recordType)
		}
		return nil
	}
	if w.closed {
		return fmt.Errorf("%w: window %s is closed", ErrWindowClosed, w.Label)
	}
	switch recordType {
	case RecordTypeWindowOpen:
		if !w.sealing || seq != 1 {
			return fmt.Errorf("%w: a window book's open record is its first, and only its shelf writes it", ErrInvalid)
		}
		return nil
	case RecordTypeWindowClose:
		if !w.sealing || seq == 1 {
			return fmt.Errorf("%w: only its shelf closes a window book", ErrInvalid)
		}
		return nil
	case RecordTypeWindowPlace:
		if !w.sealing || seq == 1 {
			return fmt.Errorf("%w: only its shelf places a book in a window", ErrInvalid)
		}
		return nil
	case RecordTypeIndexRoot:
		// A checkpoint's index roots carry their real commit time: the
		// checkpoints that end and close a window commit them after its
		// end. Only the book's own checkpoint commits one outside it.
		if seq == 1 {
			return fmt.Errorf("%w: window %s's book begins with its open record", ErrInvalid, w.Label)
		}
		if checkpointing {
			return nil
		}
	}
	if seq == 1 {
		return fmt.Errorf("%w: window %s's book begins with its open record", ErrInvalid, w.Label)
	}
	if w.sealing {
		// The policy the shelf carries into a window as it opens it, which a
		// window filled in late commits after its end.
		return nil
	}
	if w.Contains(at) && !w.bounded {
		return nil
	}
	if recordType == RecordTypeClose && (w.bounded || !at.Before(w.End)) && at.Before(w.graceEnd) {
		return w.closeInside(h)
	}
	if w.bounded {
		return fmt.Errorf("%w: window %s has ended and takes only a Close for a period inside it", ErrWindowClosed, w.Label)
	}
	if !at.Before(w.End) && at.Before(w.graceEnd) {
		return fmt.Errorf("%w: window %s is in its grace until %s, which takes only a Close for a period inside it", ErrWindowClosed, w.Label, windowTime(w.graceEnd))
	}
	return fmt.Errorf("%w: %s is outside window %s [%s, %s)", ErrWindowClosed, at.Format(time.RFC3339Nano), w.Label, windowTime(w.Start), windowTime(w.End))
}

// closeInside checks that a Close is a close/v1 whose period lies inside
// the window, the one record a window takes in its grace.
func (w *bookWindow) closeInside(h Header) error {
	v1, _, err := ReadClose(h)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWindowClosed, err)
	}
	if v1 == nil {
		return fmt.Errorf("%w: window %s takes only a close/v1 Close in its grace", ErrWindowClosed, w.Label)
	}
	if v1.Period.Start.Before(w.Start) || v1.Period.End.After(w.End) {
		return fmt.Errorf("%w: the Close's period [%s, %s) is not inside window %s [%s, %s)", ErrWindowClosed, windowTime(v1.Period.Start), windowTime(v1.Period.End), w.Label, windowTime(w.Start), windowTime(w.End))
	}
	return nil
}

// appendWindowRecord appends a record the shelf writes as it opens or
// closes a window: the open or close record, or the carried policy.
func (b *Book) appendWindowRecord(ctx context.Context, recordType string, statement any) (Record, error) {
	body, err := canonicalJSON(statement)
	if err != nil {
		return Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.window.sealing = true
	defer func() { b.window.sealing = false }()
	return b.appendLocked(ctx, Entry{RecordType: recordType, EpistemicType: ProducerClaim, Links: []Link{}, Statement: body})
}

// The window tables, created with a windowed shelf (§1, §2): the policy, and
// each window's book and state.
const windowSchema = `
CREATE TABLE IF NOT EXISTS shelf_window (
	id     INTEGER PRIMARY KEY CHECK (id = 1),
	period TEXT NOT NULL,
	base   TEXT NOT NULL,
	since  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS shelf_window_books (
	label         TEXT PRIMARY KEY,
	log_id        TEXT NOT NULL UNIQUE,
	start         TEXT NOT NULL,
	state         TEXT NOT NULL CHECK (state IN ('open', 'closed')),
	final_entries INTEGER,
	final_root    TEXT
);
`

// ShelfVersionWindow is the shelf file version of a windowed shelf: a
// build or port that does not know windows refuses it, so no writer ignores
// the window.
const ShelfVersionWindow = 3

// ShelfVersionWindowGrace is the shelf file version of a windowed shelf
// with a grace (evidencebook#807): a build that knows windows but not grace
// refuses it rather than close its windows when they end.
const ShelfVersionWindowGrace = 4

// ShelfVersionWindowPredecessor is the shelf file version of a windowed
// shelf that continues a predecessor book (evidencebook#808), with a grace
// or not: a build that does not know predecessors refuses it rather than
// read its partial first window as a whole period.
const ShelfVersionWindowPredecessor = 5

// ShelfVersionWindowJournal is the shelf file version of a shelf whose
// windows are its journal (evidencebook#797): a build that does not know it
// refuses the file rather than refuse its deal books one by one.
const ShelfVersionWindowJournal = 6

// shelfVersionFor is the version a shelf created with policy has.
func shelfVersionFor(policy *WindowPolicy) int64 {
	switch {
	case policy.Journal:
		return ShelfVersionWindowJournal
	case policy.Predecessor != nil:
		return ShelfVersionWindowPredecessor
	case policy.Grace > 0:
		return ShelfVersionWindowGrace
	}
	return ShelfVersionWindow
}

const (
	windowOpen   = "open"
	windowClosed = "closed"
)

// initWindow records policy in a shelf file created in this transaction
// (fresh, so it holds nothing yet), or checks a windowed file's recorded
// policy against it.
func initWindow(ctx context.Context, tx *sql.Tx, policy *WindowPolicy, version int64, fresh bool) error {
	windowed := version == ShelfVersionWindow || version == ShelfVersionWindowGrace || version == ShelfVersionWindowPredecessor || version == ShelfVersionWindowJournal
	switch {
	case windowed && policy == nil:
		return fmt.Errorf("%w: this shelf is windowed; open it with its window policy", ErrInvalid)
	case windowed:
		if err := addWindowColumns(ctx, tx); err != nil {
			return err
		}
		stored, err := readWindowPolicy(ctx, tx)
		if err != nil {
			return fmt.Errorf("%w: windowed shelf without its policy: %v", ErrCorrupt, err)
		}
		if !stored.sameAs(policy) {
			return fmt.Errorf("%w: this shelf's window is %s from %s under %q with a grace of %s%s%s, fixed when it was created; it cannot change", ErrInvalid, stored.Period, windowTime(stored.Since), stored.Base, stored.Grace, stored.predecessorText(), stored.journalText())
		}
		if want := shelfVersionFor(stored); version != want {
			return fmt.Errorf("%w: shelf version %d does not match its window policy (version %d)", ErrCorrupt, version, want)
		}
		return nil
	case policy == nil:
		return nil
	case !fresh:
		return fmt.Errorf("%w: a window is fixed when a shelf is created; this file already holds a shelf without one", ErrInvalid)
	}
	if err := policy.validate(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, windowSchema); err != nil {
		return err
	}
	if err := addWindowColumns(ctx, tx); err != nil {
		return err
	}
	var predLog, predRoot, predPolicy any
	var predEntries any
	if p := policy.Predecessor; p != nil {
		predLog, predEntries, predRoot = p.LogID, int64(p.Entries), p.Root
		if policy.PredecessorPolicy != nil {
			body, err := canonicalJSON(policy.PredecessorPolicy)
			if err != nil {
				return err
			}
			predPolicy = string(body)
		}
	}
	journal := 0
	if policy.Journal {
		journal = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO shelf_window (id, period, base, since, grace, pred_log_id, pred_entries, pred_root, pred_policy, journal) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, string(policy.Period), policy.Base, windowTime(policy.Since), int64(policy.Grace/time.Second), predLog, predEntries, predRoot, predPolicy, journal); err != nil {
		return err
	}
	if !policy.Journal {
		return nil
	}
	// A journal-windowed shelf is created on book facts (evidencebook#797
	// §1): its cadence log has no tree root to carry over, so there is
	// nothing for Migrate to do, and its journal needs a migrated shelf.
	if _, err := tx.ExecContext(ctx, windowPlacedSchema); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, ?, '')`, migrationDone)
	return err
}

// windowPlacedSchema is the window each book of a journal-windowed shelf is
// placed in (evidencebook#797 §3): written pending before its window_place
// record and marked placed after it, so the book's next Place finishes a
// placement a crash interrupted.
const windowPlacedSchema = `
CREATE TABLE IF NOT EXISTS shelf_window_placed (
	log_id TEXT PRIMARY KEY,
	label  TEXT NOT NULL,
	state  TEXT NOT NULL CHECK (state IN ('pending', 'placed'))
);
`

// queryRower is what readWindowPolicy reads from: a transaction or a
// read-only handle.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// windowPolicyQuery reads every window column of a current shelf.
const windowPolicyQuery = `SELECT period, base, since, grace, pred_log_id, pred_entries, pred_root, pred_policy, journal FROM shelf_window WHERE id = 1`

// readWindowPolicy reads a shelf's stored policy with query, which selects
// the columns of windowPolicyQuery in order.
func readWindowPolicyWith(ctx context.Context, db queryRower, query string) (*WindowPolicy, error) {
	var period, base, since string
	var grace, journal int64
	var predLog, predRoot, predPolicy sql.NullString
	var predEntries sql.NullInt64
	if err := db.QueryRowContext(ctx, query).Scan(&period, &base, &since, &grace, &predLog, &predEntries, &predRoot, &predPolicy, &journal); err != nil {
		return nil, err
	}
	start, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return nil, fmt.Errorf("%w: the shelf's window since %q: %v", ErrCorrupt, since, err)
	}
	out := &WindowPolicy{Period: WindowPeriod(period), Base: base, Since: start, Grace: time.Duration(grace) * time.Second, Journal: journal != 0}
	if predLog.Valid {
		out.Predecessor = &WindowTree{LogID: predLog.String, Entries: uint64(predEntries.Int64), Root: predRoot.String}
		if predPolicy.Valid {
			var policy DisclosurePolicy
			if err := decodeStrict([]byte(predPolicy.String), &policy); err != nil {
				return nil, fmt.Errorf("%w: the shelf's predecessor policy: %v", ErrCorrupt, err)
			}
			out.PredecessorPolicy = &policy
		}
	}
	return out, nil
}

func readWindowPolicy(ctx context.Context, db queryRower) (*WindowPolicy, error) {
	return readWindowPolicyWith(ctx, db, windowPolicyQuery)
}

// sameAs reports whether a stored policy is the one a caller opens with.
func (wp *WindowPolicy) sameAs(other *WindowPolicy) bool {
	if wp.Period != other.Period || wp.Base != other.Base || windowTime(wp.Since) != windowTime(other.Since) || wp.Grace != other.Grace || wp.Journal != other.Journal {
		return false
	}
	if (wp.Predecessor == nil) != (other.Predecessor == nil) || (wp.Predecessor != nil && *wp.Predecessor != *other.Predecessor) {
		return false
	}
	a, errA := policyDigest(wp.PredecessorPolicy)
	b, errB := policyDigest(other.PredecessorPolicy)
	return errA == nil && errB == nil && a == b
}

func policyDigest(p *DisclosurePolicy) (string, error) {
	if p == nil {
		return "", nil
	}
	return p.Digest()
}

func (wp *WindowPolicy) journalText() string {
	if !wp.Journal {
		return ""
	}
	return ", as its journal"
}

func (wp *WindowPolicy) predecessorText() string {
	if wp.Predecessor == nil {
		return ""
	}
	return fmt.Sprintf(", continuing %s at %d entries", wp.Predecessor.LogID, wp.Predecessor.Entries)
}

// addWindowColumns adds the grace columns (evidencebook#807) to a windowed
// shelf created before them: the policy's grace, read as 0, and each
// window's boundary tree.
func addWindowColumns(ctx context.Context, tx *sql.Tx) error {
	for _, c := range []struct{ table, column, ddl string }{
		{"shelf_window", "grace", "ALTER TABLE shelf_window ADD COLUMN grace INTEGER NOT NULL DEFAULT 0"},
		{"shelf_window_books", "boundary_entries", "ALTER TABLE shelf_window_books ADD COLUMN boundary_entries INTEGER"},
		{"shelf_window_books", "boundary_root", "ALTER TABLE shelf_window_books ADD COLUMN boundary_root TEXT"},
		// The predecessor (evidencebook#808): absent on every shelf created
		// before it, and on any created empty.
		{"shelf_window", "pred_log_id", "ALTER TABLE shelf_window ADD COLUMN pred_log_id TEXT"},
		{"shelf_window", "pred_entries", "ALTER TABLE shelf_window ADD COLUMN pred_entries INTEGER"},
		{"shelf_window", "pred_root", "ALTER TABLE shelf_window ADD COLUMN pred_root TEXT"},
		{"shelf_window", "pred_policy", "ALTER TABLE shelf_window ADD COLUMN pred_policy TEXT"},
		// The journal flag (evidencebook#797): 0 on every shelf created
		// before it.
		{"shelf_window", "journal", "ALTER TABLE shelf_window ADD COLUMN journal INTEGER NOT NULL DEFAULT 0"},
	} {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := tx.ExecContext(ctx, c.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// windowRow is one row of shelf_window_books.
type windowRow struct {
	Window
	logID   string
	state   string
	entries uint64
	root    string
	// boundaryEntries and boundaryRoot are the tree the window held when it
	// ended and entered its grace (zero entries: none taken).
	boundaryEntries uint64
	boundaryRoot    string
}

// inGrace reports whether the row's window has ended and is in its grace.
func (r windowRow) inGrace() bool { return r.state == windowOpen && r.boundaryEntries > 0 }

// tree is the tree the next window's open record names: the boundary when
// the window took one, else its final tree.
func (r windowRow) tree() *WindowTree {
	if r.boundaryEntries > 0 {
		return &WindowTree{LogID: r.logID, Entries: r.boundaryEntries, Root: r.boundaryRoot}
	}
	return &WindowTree{LogID: r.logID, Entries: r.entries, Root: r.root}
}

func (s *Shelf) windowRowsLocked(ctx context.Context) ([]windowRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT label, log_id, state, COALESCE(final_entries, 0), COALESCE(final_root, ''), COALESCE(boundary_entries, 0), COALESCE(boundary_root, '') FROM shelf_window_books ORDER BY start`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []windowRow
	for rows.Next() {
		var r windowRow
		var label string
		if err := rows.Scan(&label, &r.logID, &r.state, &r.entries, &r.root, &r.boundaryEntries, &r.boundaryRoot); err != nil {
			return nil, err
		}
		if r.Window, err = s.cfg.Window.window(label); err != nil {
			return nil, fmt.Errorf("%w: window row %q: %v", ErrCorrupt, label, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Shelf) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// errNotWindowBook marks a log id that is no window book of a windowed
// shelf, so WindowBook can say the window has none.
var errNotWindowBook = errors.New("is not a window book of this windowed shelf")

// windowed reports ErrInvalid for a shelf with no window policy.
func (s *Shelf) windowed() error {
	if s.cfg.Window == nil {
		return fmt.Errorf("%w: this shelf has no window policy", ErrInvalid)
	}
	return nil
}

// Current rolls the shelf's windows (Roll) and returns the book of the
// window holding the shelf's clock, the one writable book of a windowed
// shelf. It refuses (ErrClockBehind) when the clock reads earlier than the
// newest window's start: it never reopens a window it has moved past.
func (s *Shelf) Current(ctx context.Context) (*Book, Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLocked(ctx)
}

func (s *Shelf) currentLocked(ctx context.Context) (*Book, Window, error) {
	if s.released {
		return nil, Window{}, ErrReleased
	}
	if err := s.windowed(); err != nil {
		return nil, Window{}, err
	}
	latest, err := s.rollLocked(ctx)
	if err != nil {
		return nil, Window{}, err
	}
	now := s.now()
	if now.Before(latest.Start) {
		return nil, Window{}, fmt.Errorf("%w: the clock reads %s, before window %s began (%s)", ErrClockBehind, now.UTC().Format(time.RFC3339), latest.Label, windowTime(latest.Start))
	}
	book, err := s.openLocked(ctx, latest.logID)
	if err != nil {
		return nil, Window{}, err
	}
	return book, latest.Window, nil
}

// Roll closes every window that has ended, oldest first, and opens the
// book of each window after it up to the one holding the shelf's clock,
// empty ones included, so the run has no hole. It also finishes a roll a
// crash interrupted. It returns the newest window.
func (s *Shelf) Roll(ctx context.Context) (Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return Window{}, ErrReleased
	}
	if err := s.windowed(); err != nil {
		return Window{}, err
	}
	latest, err := s.rollLocked(ctx)
	return latest.Window, err
}

func (s *Shelf) rollLocked(ctx context.Context) (windowRow, error) {
	policy := s.cfg.Window
	rows, err := s.windowRowsLocked(ctx)
	if err != nil {
		return windowRow{}, err
	}
	var latest windowRow
	if len(rows) == 0 {
		if now := s.now(); now.Before(policy.Since) {
			return windowRow{}, fmt.Errorf("%w: the window policy starts at %s; the clock reads %s", ErrClockBehind, windowTime(policy.Since), now.UTC().Format(time.RFC3339))
		}
		if latest, err = s.openWindowLocked(ctx, policy.first(), policy.Predecessor); err != nil {
			return windowRow{}, err
		}
	} else {
		latest = rows[len(rows)-1]
		// A crash may have left the newest window's book without its open
		// record: write it now (openWindowLocked is idempotent).
		previous := policy.Predecessor
		if len(rows) > 1 {
			previous = rows[len(rows)-2].tree()
		}
		if latest.state == windowOpen && !latest.inGrace() {
			if latest, err = s.openWindowLocked(ctx, latest.Window, previous); err != nil {
				return windowRow{}, err
			}
		}
	}
	now := s.now()
	// Every window whose grace has ended closes, oldest first.
	for _, row := range rows {
		if row.inGrace() && !now.Before(row.End.Add(policy.Grace)) && row.logID != latest.logID {
			if _, err := s.closeWindowLocked(ctx, row); err != nil {
				return windowRow{}, err
			}
		}
	}
	for latest.state == windowClosed || latest.inGrace() || !now.Before(latest.End) {
		switch {
		case latest.inGrace() && !now.Before(latest.End.Add(policy.Grace)):
			if latest, err = s.closeWindowLocked(ctx, latest); err != nil {
				return windowRow{}, err
			}
		case latest.state == windowOpen && !latest.inGrace():
			// The window has ended: with no grace, or past it, it closes
			// now; otherwise it takes its boundary and enters its grace.
			if policy.Grace == 0 || !now.Before(latest.End.Add(policy.Grace)) {
				latest, err = s.closeWindowLocked(ctx, latest)
			} else {
				latest, err = s.boundaryLocked(ctx, latest)
			}
			if err != nil {
				return windowRow{}, err
			}
		}
		next := policy.Period.Next(latest.Window)
		if latest, err = s.openWindowLocked(ctx, next, latest.tree()); err != nil {
			return windowRow{}, err
		}
	}
	return latest, nil
}

// boundaryLocked takes an ended window's boundary checkpoint and records it,
// entering the window's grace: the next window's open record names this
// tree, and a chain proves the final tree extends it (evidencebook#807).
func (s *Shelf) boundaryLocked(ctx context.Context, row windowRow) (windowRow, error) {
	book, err := s.openLocked(ctx, row.logID)
	if err != nil {
		return windowRow{}, err
	}
	// A Close admitted after this checkpoint is in the window's tail, so no
	// size check races it.
	cp, err := book.Checkpoint(ctx)
	if err != nil {
		return windowRow{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE shelf_window_books SET boundary_entries = ?, boundary_root = ? WHERE label = ?`, cp.Entries, cp.Root, row.Label); err != nil {
		return windowRow{}, err
	}
	book.mu.Lock()
	book.window.bounded = true
	book.mu.Unlock()
	row.boundaryEntries, row.boundaryRoot = cp.Entries, cp.Root
	return row, nil
}

// openWindowLocked makes sure w has its row and its book its open record.
func (s *Shelf) openWindowLocked(ctx context.Context, w Window, previous *WindowTree) (windowRow, error) {
	logID := w.LogID(s.cfg.Window.Base)
	if err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO shelf_window_books (label, log_id, start, state) VALUES (?, ?, ?, ?) ON CONFLICT (label) DO NOTHING`, w.Label, logID, windowTime(w.Start), windowOpen); err != nil {
			return err
		}
		if !s.cfg.Window.Journal {
			return nil
		}
		// A journal window's book is of the journal's kind (evidencebook#797
		// §2), before its first record, so it opens as a journal and its
		// facts are journal facts.
		return claimLogKind(ctx, tx, logID, logKindJournal)
	}); err != nil {
		return windowRow{}, err
	}
	book, err := s.openLocked(ctx, logID)
	if err != nil {
		return windowRow{}, err
	}
	row := windowRow{Window: w, logID: logID, state: windowOpen}
	if book.Size() > 0 {
		if book.Size() == 1 {
			return row, s.carryPolicyLocked(ctx, book, previous)
		}
		return row, nil
	}
	if _, err = book.appendWindowRecord(ctx, RecordTypeWindowOpen, WindowOpenStatement{
		RecordVersion: WindowOpenVersion, Period: s.cfg.Window.Period, Label: w.Label,
		Start: windowTime(w.Start), End: windowTime(w.End), Previous: previous,
	}); err != nil {
		return row, err
	}
	return row, s.carryPolicyLocked(ctx, book, previous)
}

// carryPolicyLocked re-states, as the new window's second record, the
// disclosure policy in force at the end of the previous window: a book's
// answers read the policy from its own log (policyInForce), so without it a
// new window would answer under none. A crash between the open record and
// this one is finished by carryPolicyLocked again on the next roll.
func (s *Shelf) carryPolicyLocked(ctx context.Context, book *Book, previous *WindowTree) error {
	if previous == nil {
		return nil
	}
	var policy DisclosurePolicy
	if p := s.cfg.Window.Predecessor; p != nil && *previous == *p {
		// The predecessor is no book of this shelf: its policy came with
		// the window policy (evidencebook#808).
		if s.cfg.Window.PredecessorPolicy == nil {
			return nil
		}
		policy = *s.cfg.Window.PredecessorPolicy
	} else {
		prev, err := s.openLocked(ctx, previous.LogID)
		if err != nil {
			return err
		}
		prev.mu.Lock()
		inForce, ok, err := policyInForce(prev.logLocked())
		prev.mu.Unlock()
		if err != nil || !ok {
			return err
		}
		policy = inForce
	}
	book.mu.Lock()
	_, carried, err := policyInForce(book.logLocked())
	book.mu.Unlock()
	if err != nil || carried {
		return err
	}
	digest, err := policy.Digest()
	if err != nil {
		return err
	}
	_, err = book.appendWindowRecord(ctx, RecordTypePolicy, policyStatement{Kind: "adopted", Policy: policy, Digest: digest})
	return err
}

// closeWindowLocked closes an open window's book: a checkpoint that commits
// any pending index roots, the close record, then the final checkpoint,
// whose tree ends at the close record. Each step is skipped when a crash
// left it done.
func (s *Shelf) closeWindowLocked(ctx context.Context, row windowRow) (windowRow, error) {
	book, err := s.openLocked(ctx, row.logID)
	if err != nil {
		return windowRow{}, err
	}
	book.mu.Lock()
	log := book.logLocked()
	book.mu.Unlock()
	if len(log) == 0 || log[len(log)-1].Header.RecordType != RecordTypeWindowClose {
		if _, err := book.Checkpoint(ctx); err != nil {
			return windowRow{}, err
		}
		book.mu.Lock()
		log = book.logLocked()
		book.mu.Unlock()
		counts := map[string]int{}
		for _, record := range log {
			counts[record.Header.RecordType]++
		}
		statement := WindowCloseStatement{
			RecordVersion: WindowCloseVersion, Period: s.cfg.Window.Period, Label: row.Label,
			Start: windowTime(row.Start), End: windowTime(row.End), CountsByKind: counts,
		}
		if row.boundaryEntries > 0 {
			statement.Boundary = row.tree()
		}
		if s.cfg.Window.Journal {
			if statement.Placed, err = s.placedLocked(ctx, row.Label); err != nil {
				return windowRow{}, err
			}
			statement.RecordVersion = WindowCloseVersionV2
		}
		if _, err := book.appendWindowRecord(ctx, RecordTypeWindowClose, statement); err != nil {
			return windowRow{}, err
		}
	}
	final, err := book.Checkpoint(ctx)
	if err != nil {
		return windowRow{}, err
	}
	if size := book.Size(); final.Entries != size {
		return windowRow{}, fmt.Errorf("%w: window %s's final checkpoint covers %d of %d records", ErrCorrupt, row.Label, final.Entries, size)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE shelf_window_books SET state = ?, final_entries = ?, final_root = ? WHERE label = ?`, windowClosed, final.Entries, final.Root, row.Label); err != nil {
		return windowRow{}, err
	}
	book.mu.Lock()
	book.window.closed, book.window.final = true, final
	book.mu.Unlock()
	row.state, row.entries, row.root = windowClosed, final.Entries, final.Root
	return row, nil
}

// attachWindow gives a window book its window, and on a windowed shelf
// refuses any other book: a windowed shelf holds window books only. It runs
// in openLocked before the book is cached.
func (s *Shelf) attachWindow(ctx context.Context, book *Book, logID string) error {
	if s.cfg.Window == nil {
		return nil
	}
	var label, state, root string
	var entries uint64
	err := s.db.QueryRowContext(ctx, `SELECT label, state, COALESCE(final_entries, 0), COALESCE(final_root, '') FROM shelf_window_books WHERE log_id = ?`, logID).Scan(&label, &state, &entries, &root)
	if errors.Is(err, sql.ErrNoRows) {
		if s.cfg.Window.Journal && logID == JournalLogID {
			return fmt.Errorf("%w: this shelf's journal is its windows; %s is no book", ErrInvalid, JournalLogID)
		}
		if s.cfg.Window.Journal && !strings.HasPrefix(logID, JournalLogID+"/") {
			// A journal-windowed shelf holds its other books as an
			// unwindowed shelf does (evidencebook#797 §3), and a deal book
			// answers a pack the shelf registered as a window does (§4).
			return s.shareRegistry(book)
		}
		return fmt.Errorf("%w: %s %w", ErrInvalid, logID, errNotWindowBook)
	}
	if err != nil {
		return err
	}
	w, err := s.cfg.Window.window(label)
	if err != nil {
		return fmt.Errorf("%w: window row %q: %v", ErrCorrupt, label, err)
	}
	bw := &bookWindow{Window: w, graceEnd: w.End.Add(s.cfg.Window.Grace)}
	var boundary uint64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(boundary_entries, 0) FROM shelf_window_books WHERE log_id = ?`, logID).Scan(&boundary); err != nil {
		return err
	}
	bw.bounded = boundary > 0
	if state == windowClosed {
		final, err := finalCheckpoint(ctx, book, entries, root)
		if err != nil {
			return err
		}
		bw.closed, bw.final = true, final
	}
	book.window = bw
	return s.shareRegistry(book)
}

// shareRegistry gives book what the shelf shares across the books that
// answer for it (§3): the unsigned-ask limiter and the derivations
// registered with Shelf.RegisterDerivation.
func (s *Shelf) shareRegistry(book *Book) error {
	book.unsignedAsks = s.windowAsks
	s.windowRegistry.mu.Lock()
	defs := make([]DerivationDefinition, 0, len(s.windowRegistry.derivations))
	for _, entry := range s.windowRegistry.derivations {
		defs = append(defs, entry.def)
	}
	s.windowRegistry.mu.Unlock()
	for _, def := range defs {
		if _, err := book.RegisterDerivation(def); err != nil {
			return err
		}
	}
	return nil
}

// finalCheckpoint is the newest stored checkpoint of book's final tree.
func finalCheckpoint(ctx context.Context, book *Book, entries uint64, root string) (Checkpoint, error) {
	stored, err := book.store.Checkpoints(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	for i := len(stored) - 1; i >= 0; i-- {
		if stored[i].Entries == entries && stored[i].Root == root {
			return stored[i], nil
		}
	}
	return Checkpoint{}, fmt.Errorf("%w: closed window book %s holds no checkpoint of its final tree", ErrCorrupt, book.id)
}

// WindowBook opens the book of the window label names, open or closed.
func (s *Shelf) WindowBook(ctx context.Context, label string) (*Book, Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, Window{}, ErrReleased
	}
	if err := s.windowed(); err != nil {
		return nil, Window{}, err
	}
	w, err := s.cfg.Window.window(label)
	if err != nil {
		return nil, Window{}, err
	}
	book, err := s.openLocked(ctx, w.LogID(s.cfg.Window.Base))
	if err != nil {
		if errors.Is(err, errNotWindowBook) {
			return nil, Window{}, fmt.Errorf("%w: window %s has no book", ErrNotFound, label)
		}
		return nil, Window{}, err
	}
	return book, w, nil
}

// WindowState is one window of a windowed shelf as Windows lists it.
type WindowState struct {
	Window
	LogID  string `json:"log_id"`
	Closed bool   `json:"closed"`
	// InGrace is set for a window that has ended and is in its grace;
	// ClosesAt is when the window closes: its end plus the store's grace.
	InGrace  bool      `json:"in_grace,omitempty"`
	ClosesAt time.Time `json:"closes_at"`
	// Partial is set for the first window of a store that continues a
	// predecessor book: it starts at the conversion, not its period's start.
	Partial bool `json:"partial,omitempty"`
	// Final is the window's final tree once it is closed.
	Final *WindowTree `json:"final,omitempty"`
}

// Windows lists the shelf's windows, oldest first. It does not roll.
func (s *Shelf) Windows(ctx context.Context) ([]WindowState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	if err := s.windowed(); err != nil {
		return nil, err
	}
	rows, err := s.windowRowsLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WindowState, len(rows))
	for i, r := range rows {
		out[i] = WindowState{Window: r.Window, LogID: r.logID, Closed: r.state == windowClosed, InGrace: r.inGrace(), ClosesAt: r.End.Add(s.cfg.Window.Grace), Partial: s.cfg.Window.Period.Partial(r.Window)}
		if out[i].Closed {
			out[i].Final = &WindowTree{LogID: r.logID, Entries: r.entries, Root: r.root}
		}
	}
	return out, nil
}

// WindowPolicy is the shelf's window policy, nil when it has none.
func (s *Shelf) WindowPolicy() *WindowPolicy {
	if s.cfg.Window == nil {
		return nil
	}
	policy := *s.cfg.Window
	return &policy
}

// RegisterDerivation registers def on every window book of a windowed
// shelf, those opened later included, so a pack stays answerable across a
// window boundary (§3). Book.RegisterDerivation is its rule.
func (s *Shelf) RegisterDerivation(def DerivationDefinition) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return "", ErrReleased
	}
	if err := s.windowed(); err != nil {
		return "", err
	}
	// Checked first against the shelf's own registry, so a definition
	// refused there is registered on no book.
	id, err := s.windowRegistry.RegisterDerivation(def)
	if err != nil {
		return "", err
	}
	for _, book := range s.books {
		if _, err := book.RegisterDerivation(def); err != nil {
			return "", err
		}
	}
	return id, nil
}

// ReadShelfWindow reads the window policy a shelf file was created with,
// without opening the shelf or taking its lock, so a host that keeps no copy
// of the policy can open the shelf with it (OpenSQLiteShelf requires it
// unchanged). It returns nil for a file with no window policy, and
// ErrNotFound for no file.
func ReadShelfWindow(ctx context.Context, path string) (*WindowPolicy, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: no shelf file at %s", ErrNotFound, path)
		}
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}).String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'shelf_window'`).Scan(&tables); err != nil {
		return nil, err
	}
	if tables == 0 {
		return nil, nil
	}
	// A shelf created before grace or predecessors lacks their columns:
	// they read as none.
	columns := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('shelf_window')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	column := func(name, absent string) string {
		if columns[name] {
			return name
		}
		return absent
	}
	query := fmt.Sprintf(`SELECT period, base, since, %s, %s, %s, %s, %s, %s FROM shelf_window WHERE id = 1`,
		column("grace", "0"), column("pred_log_id", "NULL"), column("pred_entries", "NULL"), column("pred_root", "NULL"), column("pred_policy", "NULL"), column("journal", "0"))
	policy, err := readWindowPolicyWith(ctx, db, query)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return policy, nil
}
