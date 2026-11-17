// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"errors"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

var (
	// ErrInvalid reports input that violates the record model or a request
	// shape. It is go/evidencerequest's own sentinel, so an error from the
	// aliased protocol types (Refusal.Verify, Envelope.Verify, ...) and one
	// raised here both match it.
	ErrInvalid = evidencerequest.ErrInvalid
	// ErrNotFound reports a record, payload or checkpoint the book does not hold.
	ErrNotFound = errors.New("evidencebook: not found")
	// ErrCorrupt reports stored state that disagrees with the commitment substrate.
	ErrCorrupt = errors.New("evidencebook: corrupt")
	// ErrUnsupported reports a capability the configured substrate or index does
	// not provide, such as range proofs over a receipt-only substrate.
	ErrUnsupported = errors.New("evidencebook: unsupported")
	// ErrNotCovered reports a record that no checkpoint covers yet.
	ErrNotCovered = errors.New("evidencebook: not checkpoint-covered")
	// ErrUnavailable reports a payload whose retention state yields no bytes.
	ErrUnavailable = errors.New("evidencebook: payload unavailable")
	// ErrLegalHold reports a deletion blocked by a legal hold.
	ErrLegalHold = errors.New("evidencebook: legal hold")
	// ErrReleased reports a write to a book, or a call on a shelf, after it
	// was released.
	ErrReleased = errors.New("evidencebook: released")
	// ErrOverLimit reports a Cadence.HistoryCard that would list more ticks
	// than DefaultMaxAnswerItems, or a received record past one of the
	// store's bounds (ReceiveBounds). A Book's card over its limit is a
	// refusal (Book.Staple reports ErrInvalid), not this error.
	ErrOverLimit = errors.New("evidencebook: over the item limit")
	// ErrRefused reports a received record or stamp proof whose sender may
	// not push it (Shelf.Receive): nothing of it is stored.
	ErrRefused = errors.New("evidencebook: refused")
	// ErrDeleted reports a received record deleted under the holder's
	// retention: the store keeps its tombstone, so it is deleted, not missing.
	ErrDeleted = errors.New("evidencebook: deleted")
	// ErrBusy reports a received offer refused because the holder's books
	// kept changing while room was made for it (Shelf.Receive): sending it
	// again later may succeed.
	ErrBusy = errors.New("evidencebook: busy; try again")
)
