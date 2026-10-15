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
)
