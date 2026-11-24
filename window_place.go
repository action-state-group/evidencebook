// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Placed books (evidencebook#797, docs/design/797-deal-window.md §3): on a
// shelf whose windows are its journal, a deal's book is one book for the
// whole deal, placed in the window its agreement is sealed in. Placing it
// seals a window_place record in that window's journal, and the window's
// close (window_close/v2) lists every book placed in it that holds its first
// record, so a chain of journal windows names the deals of each window.

// The states of a shelf_window_placed row: written before the window_place
// record (pending) and marked once the record is in the journal (placed), so
// a crash between the two is finished by the next Place of the book.
const (
	placePending = "pending"
	placePlaced  = "placed"
)

// journalWindowed reports ErrInvalid for a shelf whose windows are not its
// journal.
func (s *Shelf) journalWindowed() error {
	if s.cfg.Window == nil || !s.cfg.Window.Journal {
		return fmt.Errorf("%w: this shelf's windows are not its journal", ErrInvalid)
	}
	return nil
}

// Place returns the book logID, placed in the window holding at. It rolls
// first, and refuses (ErrWindowClosed) when at is not in the current window:
// a book is placed only as its first record is sealed, and that record's
// time is at. A book placed before and holding records returns its window
// unchanged; one placed before but still empty (a crash between Place and
// its first record) is placed again in the current window, and the latest
// placement counts. The journal's window books and host logs are refused.
func (s *Shelf) Place(ctx context.Context, logID string, at time.Time) (*Book, Window, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, Window{}, ErrReleased
	}
	if err := s.journalWindowed(); err != nil {
		return nil, Window{}, err
	}
	if logID == "" || logID == JournalLogID || strings.HasPrefix(logID, JournalLogID+"/") {
		return nil, Window{}, fmt.Errorf("%w: %q is the journal's, not a book to place", ErrInvalid, logID)
	}
	journal, current, err := s.currentLocked(ctx)
	if err != nil {
		return nil, Window{}, err
	}
	if !current.Contains(at) {
		return nil, Window{}, fmt.Errorf("%w: %s is outside the current window %s [%s, %s)", ErrWindowClosed, at.UTC().Format(time.RFC3339Nano), current.Label, windowTime(current.Start), windowTime(current.End))
	}
	if kind, err := s.logKind(ctx, logID); err != nil {
		return nil, Window{}, err
	} else if kind == logKindJournal || kind == logKindHost {
		return nil, Window{}, fmt.Errorf("%w: %s is a %s log, not a book to place", ErrInvalid, logID, kind)
	}
	book, err := s.openLocked(ctx, logID)
	if err != nil {
		return nil, Window{}, err
	}
	var label, state string
	switch err := s.db.QueryRowContext(ctx, `SELECT label, state FROM shelf_window_placed WHERE log_id = ?`, logID).Scan(&label, &state); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, Window{}, err
	case label != current.Label && book.Size() > 0:
		w, err := s.cfg.Window.window(label)
		return book, w, err
	case label == current.Label && state == placePlaced:
		return book, current, nil
	case label == current.Label:
		// A crash after the row: the record may be in the journal already.
		held, err := placeRecorded(ctx, journal, logID)
		if err != nil {
			return nil, Window{}, err
		}
		if held {
			return book, current, s.markPlaced(ctx, logID)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO shelf_window_placed (log_id, label, state) VALUES (?, ?, ?)
ON CONFLICT (log_id) DO UPDATE SET label = excluded.label, state = excluded.state`, logID, current.Label, placePending); err != nil {
		return nil, Window{}, err
	}
	if _, err := journal.appendWindowRecord(ctx, RecordTypeWindowPlace, WindowPlaceStatement{RecordVersion: WindowPlaceVersion, LogID: logID}); err != nil {
		return nil, Window{}, err
	}
	return book, current, s.markPlaced(ctx, logID)
}

func (s *Shelf) markPlaced(ctx context.Context, logID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shelf_window_placed SET state = ? WHERE log_id = ?`, placePlaced, logID)
	return err
}

// placeRecords is each window_place record of a journal window's book, by
// the log id it places.
func placeRecords(ctx context.Context, journal *Book) ([]string, error) {
	records, err := journal.Query(ctx, Filter{RecordType: RecordTypeWindowPlace})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range records {
		var st WindowPlaceStatement
		if err := decodeStrict(r.Header.Statement, &st); err != nil || st.RecordVersion != WindowPlaceVersion || st.LogID == "" {
			return nil, fmt.Errorf("%w: window_place record %s does not carry its body", ErrCorrupt, r.RecordID)
		}
		out = append(out, st.LogID)
	}
	return out, nil
}

func placeRecorded(ctx context.Context, journal *Book, logID string) (bool, error) {
	placed, err := placeRecords(ctx, journal)
	return slices.Contains(placed, logID), err
}

// placedLocked is a journal window's placed list as its close states it:
// the books its window_place records name that hold their first record,
// sorted, each once. A book placed again in a later window was still empty
// when this one closed, so it is not listed here.
func (s *Shelf) placedLocked(ctx context.Context, label string) ([]string, error) {
	journal, err := s.openLocked(ctx, s.cfg.Window.Base+"/"+label)
	if err != nil {
		return nil, err
	}
	named, err := placeRecords(ctx, journal)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, logID := range named {
		if slices.Contains(out, logID) {
			continue
		}
		book, err := s.openLocked(ctx, logID)
		if err != nil {
			return nil, err
		}
		if book.Size() > 0 {
			out = append(out, logID)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Placement is the window a book is placed in, and whether it is placed.
func (s *Shelf) Placement(ctx context.Context, logID string) (Window, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return Window{}, false, ErrReleased
	}
	if err := s.journalWindowed(); err != nil {
		return Window{}, false, err
	}
	var label, state string
	switch err := s.db.QueryRowContext(ctx, `SELECT label, state FROM shelf_window_placed WHERE log_id = ?`, logID).Scan(&label, &state); {
	case errors.Is(err, sql.ErrNoRows):
		return Window{}, false, nil
	case err != nil:
		return Window{}, false, err
	case state != placePlaced:
		return Window{}, false, nil
	}
	w, err := s.cfg.Window.window(label)
	return w, err == nil, err
}

// WindowEnded reports whether logID is the book of a closed window: a
// cadence host's Ended predicate asks it, so a closed window leaves the live
// set once its final checkpoint is witnessed (evidencebook#689 §2).
func (s *Shelf) WindowEnded(ctx context.Context, logID string) (bool, error) {
	if s.cfg.Window == nil {
		return false, nil
	}
	var state string
	switch err := s.db.QueryRowContext(ctx, `SELECT state FROM shelf_window_books WHERE log_id = ?`, logID).Scan(&state); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	return state == windowClosed, nil
}
