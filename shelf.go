// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ShelfConfig assembles a Shelf: one profile's books in one SQLite file.
type ShelfConfig struct {
	// Path is the SQLite file holding every book's records, checkpoints and
	// CLL log (one log per book, keyed by its id), as capsule-cli keeps a
	// profile's per-deal logs in one deal.db.
	Path string
	// LockPath is the profile's writer lock file. A host that already has
	// a writer lock for the store passes that path, so its own writers and
	// the shelf exclude each other: capsule-cli's deal store must pass
	// "<deal.db>.deal.lock" (lockDealStore). Empty means Path + ".lock",
	// which excludes only other shelves.
	LockPath string
	// HeldLock is the store's writer lock when the host already holds it
	// under its own rules (evidencebook#254 §2.4): the shelf then takes no
	// lock of its own and leaves the host's to the host, so LockPath must be
	// empty. The shelf's own lock is unix-only; a shelf on a held lock opens
	// on any platform.
	HeldLock HeldLock
	// Random is the shelf's one random source: every salt, tree position,
	// filler nonce and seed, jitter and padding nonce the cadence draws comes
	// from it, in the order the shelf vectors (testdata/shelf) state. Nil is
	// the system CSPRNG, which production uses; a fixed stream makes a run
	// deterministic, as the cross-language vectors and interlock need.
	Random io.Reader

	Operator  string
	Developer string
	// Signer seals every book's records; CheckpointKey signs every book's
	// checkpoints, as one key signs a profile's deal logs today.
	Signer        Signer
	CheckpointKey ed25519.PrivateKey
	// Originals is the profile's one artifact store (capsule-cli#236
	// decision 3), shared by every book: an artifact record is keyed by its
	// record id, which is unique across books.
	Originals ArtifactStore
	Now       func() time.Time
	// AuthenticatedIndexes gives a book its authenticated indexes when the
	// shelf opens it. A nil function, or a nil result for a log, gives that
	// book the defaults (NewRecordIDIndex, NewSubjectIndex). An empty,
	// non-nil result gives it none, so its checkpoints commit no index_root
	// records: a deal's log then holds its own steps only.
	AuthenticatedIndexes func(logID string) []AuthenticatedIndex
	// Receive is the store of what the other side pushes (Shelf.Receive):
	// the profile that reads its records, the store's bounds and the
	// holder's retention.
	Receive ReceiveConfig
	// Window, when set, splits the shelf into one book per time window
	// (evidencebook#689). It is written when the shelf's file is created
	// and must be given unchanged on every open; a file created without one
	// can never gain one. A windowed shelf holds window books only.
	Window *WindowPolicy
}

// HeldLock is a writer lock a host holds on the shelf's store. Held reports
// nil while the host holds it; the shelf asks once, when it opens, and never
// releases it.
type HeldLock interface {
	Held() error
}

// Shelf is one profile's books, each its own log (evidencebook#36 §1): a
// deal's bundle is built from its book alone, so it carries no other deal's
// records. A shelf holds the profile's writer lock from OpenSQLiteShelf to
// Release and is the profile's only writer. It keeps one Book per log id
// for its lifetime: a Book allocates positions from its own in-memory order,
// so two instances of one log could give two records one position.
//
// Each book keeps its own mutex; books of one shelf do not share one. Their
// positions are per log, so they need no common order, and their writes to
// the shared file are serialized by SQLite (WAL, a busy timeout, immediate
// CLL transactions). Operations across books, such as a cadence tick, take
// the shelf's mutex.
type Shelf struct {
	mu sync.Mutex
	// receiveMu serializes Receive and Forget, which call the receive
	// profile outside mu so the profile may read the shelf.
	receiveMu sync.Mutex
	cfg       ShelfConfig
	db        *sql.DB
	unlock    func() error
	random    randomSource
	books     map[string]*Book
	// hosts are the host logs the shelf opened (Legacy, HostLog).
	hosts map[string]*HostLog
	// cadenceLogs are the cadence logs Cadence opened, released with the
	// shelf.
	cadenceLogs []*CLLSubstrate
	cadence     *Cadence
	// windowAsks and windowRegistry are what a windowed shelf's books
	// share (evidencebook#689 §3): the unsigned-ask limiter, and the
	// derivations registered with Shelf.RegisterDerivation, held in a book
	// that is never opened as one.
	windowAsks     *WindowLimiter
	windowRegistry *Book
	released       bool
	// closed mirrors released for the lock-free readers of the received
	// store, which never take mu.
	closed atomic.Bool
}

// shelfConnections is the size of the shelf handle's connection pool.
const shelfConnections = 4

// lockWriter takes the shelf's writer lock. It is a unix advisory file lock
// (shelf_lock_unix.go sets it); elsewhere a shelf cannot be opened.
var lockWriter = func(context.Context, string) (func() error, error) {
	return nil, fmt.Errorf("%w: a shelf's writer lock needs a unix advisory file lock", ErrUnsupported)
}

// OpenSQLiteShelf takes the writer lock, waiting for any other holder until
// ctx is done (or checks the host's held lock), and opens the shelf's SQLite
// file, creating its tables when absent. A file whose shelf_meta version it
// does not know is refused (ErrUnsupported); one with no shelf_meta row is a
// fresh file or a shelf from v0.12.0 or earlier, and gets version 1, with
// shelf_logs filled from what it holds (evidencebook#254 §2.2).
//
// The file has two kinds of handle: the shelf's own (records and
// checkpoints of every book) and one per book's CLL log. Both use WAL and a
// busy timeout, and transactions on both begin IMMEDIATE, so two writers
// wait under the busy timeout rather than fail. A record's append spans two handles (its log leaf,
// then its row) and is not one transaction; that is the shape of the JSONL
// journal and FileStore too, and Open's recovery reconciles a crash between
// them. Do not fold the handles into one connection to "fix" it: the CLL
// store owns its own.
func OpenSQLiteShelf(ctx context.Context, cfg ShelfConfig) (*Shelf, error) {
	if cfg.Path == "" || cfg.Operator == "" || cfg.Signer == nil || cfg.Originals == nil || len(cfg.CheckpointKey) == 0 {
		return nil, fmt.Errorf("%w: path, operator, signer, checkpoint key and originals are required", ErrInvalid)
	}
	// A window policy is checked before anything touches the disk, so a
	// refused one leaves no file behind (evidencebook#827). A stored policy
	// passed these checks when its shelf was created, so a policy that fails
	// them matches none, except a since off a period start by under a second,
	// which the stored form (whole seconds) once matched and is now refused.
	if cfg.Window != nil {
		if err := cfg.Window.validate(); err != nil {
			return nil, err
		}
	}
	var unlock func() error
	if cfg.HeldLock != nil {
		if cfg.LockPath != "" {
			return nil, fmt.Errorf("%w: a shelf on a host-held lock takes no lock path", ErrInvalid)
		}
		if err := cfg.HeldLock.Held(); err != nil {
			return nil, fmt.Errorf("%w: the host's writer lock is not held: %v", ErrInvalid, err)
		}
		unlock = func() error { return nil }
	} else {
		if cfg.LockPath == "" {
			cfg.LockPath = cfg.Path + ".lock"
		}
		var err error
		if unlock, err = lockWriter(ctx, cfg.LockPath); err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(cfg.Path)
	if err != nil {
		return nil, errors.Join(err, unlock())
	}
	// Every transaction on the shelf's handle begins IMMEDIATE: most read
	// and then write, and under WAL a deferred one reads a snapshot, so a
	// commit by another connection to the file (a book's CLL log, another
	// process) before its first write fails that write at once: with
	// SQLITE_BUSY_SNAPSHOT (517) when the other connection committed after
	// the snapshot, or SQLITE_BUSY (5) when it holds the write lock then. The
	// busy timeout covers neither. An immediate one waits for the write lock
	// under the busy timeout instead.
	//
	// The handle keeps a few connections, each set up by the DSN's pragmas
	// (busy_timeout and synchronous are per connection), so a reader can run
	// while a write transaction is open: the received store's readers from
	// inside a StatusProvider or ReceiveProfile callback (shelf_receive.go),
	// and a profile's reads from inside Receive's transaction. Writers still
	// serialize on SQLite's write lock, under the busy timeout.
	query := url.Values{
		"_txlock": {"immediate"},
		"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"},
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: absolute, RawQuery: query.Encode()}).String())
	if err != nil {
		return nil, errors.Join(err, unlock())
	}
	db.SetMaxOpenConns(shelfConnections)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(err, db.Close(), unlock())
	}
	if err := initSQLiteStore(ctx, db, cfg.Window); err != nil {
		return nil, errors.Join(err, db.Close(), unlock())
	}
	shelf := &Shelf{
		cfg: cfg, db: db, unlock: unlock, random: newRandomSource(cfg.Random),
		books: make(map[string]*Book), hosts: make(map[string]*HostLog),
	}
	if cfg.Window != nil {
		policy := *cfg.Window
		policy.Since = policy.Since.UTC()
		shelf.cfg.Window = &policy
		shelf.windowAsks = NewWindowLimiter(0, 7*24*time.Hour)
		shelf.windowRegistry = &Book{derivations: make(map[string]derivationEntry)}
	}
	return shelf, nil
}

// Open returns the book whose log id is logID, creating it on first use. It
// is the same *Book for the same id for the shelf's lifetime. A host log's
// id is refused (ErrInvalid), in this process or as shelf_logs records it.
func (s *Shelf) Open(ctx context.Context, logID string) (*Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	if kind, err := s.logKind(ctx, logID); err != nil {
		return nil, err
	} else if kind == logKindJournal {
		return nil, fmt.Errorf("%w: %s is the shelf's journal; open it with Journal", ErrInvalid, logID)
	}
	return s.openLocked(ctx, logID)
}

// JournalLogID is the log id of the shelf's journal.
const JournalLogID = "journal"

// Journal returns the shelf's journal (evidencebook#461 §3): a book of kind
// journal, one per shelf, opened on first use, whose records are full
// records. Its facts are journal facts: a whole-shelf reader sees that it
// changed at a stamp, never its size, and its openings go only to the owner
// and an auditor it appoints. It needs a shelf of book facts (Migrate), and
// it refuses a shelf on which a book or host log already has its log id.
func (s *Shelf) Journal(ctx context.Context) (*Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	if book, ok := s.books[JournalLogID]; ok {
		// Opened before: as the journal, or as a book that holds its id.
		if kind, err := s.logKind(ctx, JournalLogID); err != nil {
			return nil, err
		} else if kind == logKindJournal {
			return book, nil
		}
		return nil, fmt.Errorf("%w: %s is already a book, not the journal", ErrInvalid, JournalLogID)
	}
	if state, err := migrationState(ctx, s.db); err != nil {
		return nil, err
	} else if state != migrationDone {
		return nil, fmt.Errorf("%w: the journal needs a shelf of book facts; call Migrate", ErrInvalid)
	}
	if _, ok := s.hosts[JournalLogID]; ok {
		return nil, fmt.Errorf("%w: %s is a host log, not the journal", ErrInvalid, JournalLogID)
	}
	if s.cadence != nil && s.cadence.cfg.LogID == JournalLogID {
		return nil, fmt.Errorf("%w: %s is the cadence log, not the journal", ErrInvalid, JournalLogID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var records int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, JournalLogID).Scan(&records); err != nil {
		return nil, err
	}
	kind, err := logKindOf(ctx, tx, JournalLogID)
	if err != nil {
		return nil, err
	}
	if kind != logKindJournal && (kind != "" || records > 0) {
		return nil, fmt.Errorf("%w: %s is already a %s log, not the journal", ErrInvalid, JournalLogID, map[bool]string{true: kind, false: logKindBook}[kind != ""])
	}
	// A log with entries and no kind (the cadence log, which shelf_logs never
	// records, or one this build does not know) is not the journal either:
	// claiming it would leave its owner refused on the next open.
	if kind == "" {
		var entries int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cll_entries WHERE log_id = ?`, JournalLogID).Scan(&entries); err != nil {
			return nil, err
		}
		if entries > 0 {
			return nil, fmt.Errorf("%w: %s already holds another log's entries, not the journal", ErrInvalid, JournalLogID)
		}
	}
	if err := allowJournalKind(ctx, tx); err != nil {
		return nil, err
	}
	if err := claimLogKind(ctx, tx, JournalLogID, logKindJournal); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.openLocked(ctx, JournalLogID)
}

func (s *Shelf) openLocked(ctx context.Context, logID string) (*Book, error) {
	if s.released {
		return nil, ErrReleased
	}
	if book, ok := s.books[logID]; ok {
		return book, nil
	}
	if _, ok := s.hosts[logID]; ok {
		return nil, fmt.Errorf("%w: %s is a host log, not a book", ErrInvalid, logID)
	}
	kind, err := s.logKind(ctx, logID)
	if err != nil {
		return nil, err
	}
	if kind == logKindHost {
		return nil, fmt.Errorf("%w: %s is a host log, not a book", ErrInvalid, logID)
	}
	storeKind := logKindBook
	if kind == logKindJournal {
		storeKind = logKindJournal
	}
	substrate, err := OpenCLLSQLite(s.cfg.Path, logID, s.cfg.CheckpointKey)
	if err != nil {
		return nil, err
	}
	book, err := Open(ctx, Config{
		BookID: logID, Operator: s.cfg.Operator, Developer: s.cfg.Developer,
		Store: &sqliteStore{db: s.db, bookID: logID, kind: storeKind}, Substrate: substrate,
		Originals: s.cfg.Originals, Signer: s.cfg.Signer, Now: s.cfg.Now,
		Authenticated: s.authenticated(logID),
	})
	if err != nil {
		return nil, errors.Join(err, substrate.Release())
	}
	if err := s.attachWindow(ctx, book, logID); err != nil {
		return nil, errors.Join(err, book.Release())
	}
	s.books[logID] = book
	return book, nil
}

// logKind is logID's kind as shelf_logs records it (logKindOf).
func (s *Shelf) logKind(ctx context.Context, logID string) (string, error) {
	return logKindOf(ctx, s.db, logID)
}

func (s *Shelf) authenticated(logID string) []AuthenticatedIndex {
	if s.cfg.AuthenticatedIndexes == nil {
		return nil
	}
	return s.cfg.AuthenticatedIndexes(logID)
}

// Books lists the ids of the books that hold records, in id order. The
// journal is not among them: it counts toward no completeness and no tally
// (evidencebook#461 §2.7).
func (s *Shelf) Books(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT book_id FROM book_records WHERE book_id NOT IN (SELECT log_id FROM shelf_logs WHERE kind = 'journal') ORDER BY book_id`)
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

// Release releases every book the shelf opened, closes its file and gives
// up the writer lock. A book from this shelf refuses writes afterwards
// (ErrReleased).
func (s *Shelf) Release() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	s.released = true
	s.closed.Store(true)
	var errs []error
	for _, book := range s.books {
		errs = append(errs, book.Release())
	}
	for _, host := range s.hosts {
		errs = append(errs, host.log.Release())
	}
	for _, log := range s.cadenceLogs {
		errs = append(errs, log.Release())
	}
	errs = append(errs, s.db.Close(), s.unlock())
	return errors.Join(errs...)
}
