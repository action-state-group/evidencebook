// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// Respond answers an Evidence Request on a windowed shelf (evidencebook#689
// §4). The answer comes from one window book, so its proofs stay inside that
// window: the book that issued the checkpoint expected_pin names, or, for a
// pin on the requester's own exchange half, the newest book holding a record
// that cites it; any other request (a freshness floor, or one that cannot
// be read) goes to the current window's book. The evidence_request_answered
// record is sealed in the current window's book, where the answer happened.
// A closed window's book issues no new anchor, so a floor its final
// checkpoint cannot meet is answered coverage_unsatisfiable. A window in its
// grace (evidencebook#807) may issue anchors; its answers are not at its
// final tree, so a report over windows cannot use them until it closes. Book.Respond is
// the rule for everything else.
func (s *Shelf) Respond(ctx context.Context, request Message, opts RespondOptions) (Reply, Record, error) {
	for attempt := 0; ; attempt++ {
		s.mu.Lock()
		current, _, err := s.currentLocked(ctx)
		if err != nil {
			s.mu.Unlock()
			return Reply{}, Record{}, err
		}
		answering, err := s.answeringBookLocked(ctx, request)
		s.mu.Unlock()
		if err != nil {
			return Reply{}, Record{}, err
		}
		if answering == nil {
			answering = current
		}
		reply, record, err := answering.respondInto(ctx, request, opts, false, current)
		// A roll may close the window that was current between choosing it
		// and recording the answer in it: answer once more in the next one.
		// The retry counts the ask against the limiters again, so near a
		// limit such an ask may be refused one early; it happens only when a
		// roll lands mid-answer.
		if errors.Is(err, ErrWindowClosed) && attempt == 0 {
			continue
		}
		return reply, record, err
	}
}

// answeringBookLocked is the window book a request's pin names, nil when it
// names none (the current window then answers, and refuses an unknown pin
// coverage_unsatisfiable as any book does).
func (s *Shelf) answeringBookLocked(ctx context.Context, request Message) (*Book, error) {
	check := evidencerequest.ParseRequest(request)
	pin := check.Request.Coverage.ExpectedPin
	if check.Reason != "" || pin == "" {
		return nil, nil
	}
	rows, err := s.windowRowsLocked(ctx)
	if err != nil {
		return nil, err
	}
	if check.Request.Subject.Kind != SubjectExchange || pin != check.Request.Subject.Digest {
		// A checkpoint pin: the window whose stored checkpoints hold it,
		// read from the store without opening every book.
		for _, row := range slices.Backward(rows) {
			checkpoints, err := (&sqliteStore{db: s.db, bookID: row.logID}).Checkpoints(ctx)
			if err != nil {
				return nil, err
			}
			if slices.ContainsFunc(checkpoints, func(cp Checkpoint) bool { return cp.Kind == SubstrateKindCLL && anchorDigest(cp) == pin }) {
				return s.openLocked(ctx, row.logID)
			}
		}
		// On a shelf whose windows are its journal, a deal book placed in a
		// window answers for itself (evidencebook#797 §4).
		return s.placedBookPinnedLocked(ctx, pin)
	}
	for _, row := range slices.Backward(rows) {
		book, err := s.openLocked(ctx, row.logID)
		if err != nil {
			return nil, err
		}
		book.mu.Lock()
		log := book.logLocked()
		book.mu.Unlock()
		if slices.ContainsFunc(log, func(r Record) bool { return slices.Contains(r.Header.LinksTo(Cites), pin) }) {
			return book, nil
		}
	}
	return nil, nil
}

// placedBookPinnedLocked is the placed book whose stored checkpoints hold
// pin, nil when none does or the shelf's windows are not its journal.
func (s *Shelf) placedBookPinnedLocked(ctx context.Context, pin string) (*Book, error) {
	if !s.cfg.Window.Journal {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT log_id FROM shelf_window_placed WHERE state = ? ORDER BY log_id`, placePlaced)
	if err != nil {
		return nil, err
	}
	var placed []string
	for rows.Next() {
		var logID string
		if err := rows.Scan(&logID); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		placed = append(placed, logID)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, logID := range placed {
		checkpoints, err := (&sqliteStore{db: s.db, bookID: logID}).Checkpoints(ctx)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(checkpoints, func(cp Checkpoint) bool { return cp.Kind == SubstrateKindCLL && anchorDigest(cp) == pin }) {
			return s.openLocked(ctx, logID)
		}
	}
	return nil, nil
}
