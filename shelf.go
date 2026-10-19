// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
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
	mu       sync.Mutex
	cfg      ShelfConfig
	db       *sql.DB
	unlock   func() error
	books    map[string]*Book
	released bool
}

// lockWriter takes the shelf's writer lock. It is a unix advisory file lock
// (shelf_lock_unix.go sets it); elsewhere a shelf cannot be opened.
var lockWriter = func(context.Context, string) (func() error, error) {
	return nil, fmt.Errorf("%w: a shelf's writer lock needs a unix advisory file lock", ErrUnsupported)
}

// OpenSQLiteShelf takes the writer lock, waiting for any other holder until
// ctx is done, and opens the shelf's SQLite file, creating its tables when
// absent.
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
	if cfg.LockPath == "" {
		cfg.LockPath = cfg.Path + ".lock"
	}
	unlock, err := lockWriter(ctx, cfg.LockPath)
	if err != nil {
		return nil, err
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
	if err := initSQLiteStore(db); err != nil {
		return nil, errors.Join(err, db.Close(), unlock())
	}
	return &Shelf{cfg: cfg, db: db, unlock: unlock, books: make(map[string]*Book)}, nil
}

// Open returns the book whose log id is logID, creating it on first use. It
// is the same *Book for the same id for the shelf's lifetime.
func (s *Shelf) Open(ctx context.Context, logID string) (*Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	if book, ok := s.books[logID]; ok {
		return book, nil
	}
	substrate, err := OpenCLLSQLite(s.cfg.Path, logID, s.cfg.CheckpointKey)
	if err != nil {
		return nil, err
	}
	book, err := Open(ctx, Config{
		BookID: logID, Operator: s.cfg.Operator, Developer: s.cfg.Developer,
		Store: &sqliteStore{db: s.db, bookID: logID}, Substrate: substrate,
		Originals: s.cfg.Originals, Signer: s.cfg.Signer, Now: s.cfg.Now,
	})
	if err != nil {
		return nil, errors.Join(err, substrate.Release())
	}
	s.books[logID] = book
	return book, nil
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
	errs = append(errs, s.db.Close(), s.unlock())
	return errors.Join(errs...)
}
