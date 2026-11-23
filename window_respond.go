// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
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
// checkpoint cannot meet is answered coverage_unsatisfiable. Book.Respond is
// the rule for everything else.
func (s *Shelf) Respond(ctx context.Context, request Message, opts RespondOptions) (Reply, Record, error) {
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
	return answering.respondInto(ctx, request, opts, false, current)
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
	halfPin := check.Request.Subject.Kind == SubjectExchange && pin == check.Request.Subject.Digest
	for _, row := range slices.Backward(rows) {
		book, err := s.openLocked(ctx, row.logID)
		if err != nil {
			return nil, err
		}
		if halfPin {
			book.mu.Lock()
			log := book.logLocked()
			book.mu.Unlock()
			if slices.ContainsFunc(log, func(r Record) bool { return slices.Contains(r.Header.LinksTo(Cites), pin) }) {
				return book, nil
			}
			continue
		}
		checkpoints, err := book.store.Checkpoints(ctx)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(checkpoints, func(cp Checkpoint) bool { return cp.Kind == SubstrateKindCLL && anchorDigest(cp) == pin }) {
			return book, nil
		}
	}
	return nil, nil
}
