// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// JournalReader is the shelf's journal as its readers see it: one book on a
// shelf without windows, or the journal windows read in chain order on a
// shelf whose windows are its journal (evidencebook#797 §2). Its seqs run
// from 1 across the whole journal: window N's records follow window N-1's
// close record. The records Query returns carry each window book's own seq
// in their headers.
type JournalReader interface {
	Size() uint64
	Query(ctx context.Context, filter Filter) ([]Record, error)
	ResolvePayload(ctx context.Context, recordID, digest string) ([]byte, error)
}

// journalView is a journal-windowed shelf's journal windows, oldest first.
// A window closes before the next opens and a closed window takes no
// record (no grace on such a shelf), so only the newest book grows and a
// seq never moves.
type journalView struct {
	db    *sql.DB
	books []*Book
}

func (v *journalView) Size() uint64 {
	var n uint64
	for _, b := range v.books {
		n += b.Size()
	}
	return n
}

// Query runs filter on each window book over the part of its seq range
// that falls in the window, and returns the records in journal order.
func (v *journalView) Query(ctx context.Context, filter Filter) ([]Record, error) {
	var out []Record
	var offset uint64
	for _, b := range v.books {
		size := b.Size()
		lo, hi := offset+1, offset+size
		offset += size
		if size == 0 || (filter.ToSeq != 0 && filter.ToSeq < lo) || filter.FromSeq > hi {
			continue
		}
		local := filter
		local.FromSeq, local.ToSeq = 1, size
		if filter.FromSeq > lo {
			local.FromSeq = filter.FromSeq - lo + 1
		}
		if filter.ToSeq != 0 && filter.ToSeq < hi {
			local.ToSeq = filter.ToSeq - lo + 1
		}
		records, err := b.Query(ctx, local)
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
	}
	return out, nil
}

// ResolvePayload resolves the payload from the window book that holds the
// record.
func (v *journalView) ResolvePayload(ctx context.Context, recordID, digest string) ([]byte, error) {
	var bookID string
	switch err := v.db.QueryRowContext(ctx, `SELECT book_id FROM book_records WHERE record_id = ?`, recordID).Scan(&bookID); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("%w: record %s is not in the journal", ErrNotFound, recordID)
	case err != nil:
		return nil, err
	}
	for _, b := range v.books {
		if b.id == bookID {
			return b.ResolvePayload(ctx, recordID, digest)
		}
	}
	return nil, fmt.Errorf("%w: record %s is not in the journal", ErrNotFound, recordID)
}

// JournalView returns the shelf's journal read in order: on a shelf whose
// windows are its journal, every journal window's book, each opened as the
// journal is (so a record a crash left stored but not yet in its log is
// recovered), without rolling; elsewhere the journal itself, created on
// first use as Journal creates it.
func (s *Shelf) JournalView(ctx context.Context) (JournalReader, error) {
	if s.cfg.Window == nil || !s.cfg.Window.Journal {
		return s.Journal(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	return s.journalViewLocked(ctx)
}

// ExistingJournalView is JournalView that never creates the journal: nil
// when the shelf has none yet.
func (s *Shelf) ExistingJournalView(ctx context.Context) (JournalReader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	return s.existingJournalViewLocked(ctx)
}

// existingJournalViewLocked is the journal as a JournalReader, a true nil
// interface when the shelf has none: a stamp hands it to a provider and
// must not create or roll anything.
func (s *Shelf) existingJournalViewLocked(ctx context.Context) (JournalReader, error) {
	if s.cfg.Window != nil && s.cfg.Window.Journal {
		return s.journalViewLocked(ctx)
	}
	book, err := s.existingJournalLocked(ctx)
	if err != nil || book == nil {
		return nil, err
	}
	return book, nil
}

func (s *Shelf) journalViewLocked(ctx context.Context) (JournalReader, error) {
	rows, err := s.windowRowsLocked(ctx)
	if err != nil {
		return nil, err
	}
	view := &journalView{db: s.db}
	for _, r := range rows {
		book, err := s.openLocked(ctx, r.logID)
		if err != nil {
			return nil, err
		}
		view.books = append(view.books, book)
	}
	return view, nil
}
