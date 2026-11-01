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
	mu     sync.Mutex
	cfg    ShelfConfig
	db     *sql.DB
	unlock func() error
	random randomSource
	books  map[string]*Book
	// hosts are the host logs the shelf opened (Legacy, HostLog).
	hosts map[string]*HostLog
	// cadenceLogs are the cadence logs Cadence opened, released with the
	// shelf.
	cadenceLogs []*CLLSubstrate
	cadence     *Cadence
	released    bool
}

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
// busy timeout, and CLL writes take an immediate transaction, so two writers
// wait rather than fail. A record's append spans two handles (its log leaf,
// then its row) and is not one transaction; that is the shape of the JSONL
// journal and FileStore too, and Open's recovery reconciles a crash between
// them. Do not fold the handles into one connection to "fix" it: the CLL
// store owns its own.
func OpenSQLiteShelf(ctx context.Context, cfg ShelfConfig) (*Shelf, error) {
	if cfg.Path == "" || cfg.Operator == "" || cfg.Signer == nil || cfg.Originals == nil || len(cfg.CheckpointKey) == 0 {
		return nil, fmt.Errorf("%w: path, operator, signer, checkpoint key and originals are required", ErrInvalid)
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
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: absolute}).String())
	if err != nil {
		return nil, errors.Join(err, unlock())
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return nil, errors.Join(err, db.Close(), unlock())
		}
	}
	if err := initSQLiteStore(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close(), unlock())
	}
	return &Shelf{
		cfg: cfg, db: db, unlock: unlock, random: newRandomSource(cfg.Random),
		books: make(map[string]*Book), hosts: make(map[string]*HostLog),
	}, nil
}

// Open returns the book whose log id is logID, creating it on first use. It
// is the same *Book for the same id for the shelf's lifetime. A host log's
// id is refused (ErrInvalid), in this process or as shelf_logs records it.
func (s *Shelf) Open(ctx context.Context, logID string) (*Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openLocked(ctx, logID)
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
	substrate, err := OpenCLLSQLite(s.cfg.Path, logID, s.cfg.CheckpointKey)
	if err != nil {
		return nil, err
	}
	book, err := Open(ctx, Config{
		BookID: logID, Operator: s.cfg.Operator, Developer: s.cfg.Developer,
		Store: &sqliteStore{db: s.db, bookID: logID}, Substrate: substrate,
		Originals: s.cfg.Originals, Signer: s.cfg.Signer, Now: s.cfg.Now,
		Authenticated: s.authenticated(logID),
	})
	if err != nil {
		return nil, errors.Join(err, substrate.Release())
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

// Books lists the ids of the books that hold records, in id order.
func (s *Shelf) Books(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT book_id FROM book_records ORDER BY book_id`)
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
