// SPDX-License-Identifier: Apache-2.0

package evidencebook

import "errors"

var (
	// ErrInvalid reports input that violates the record model or a request shape.
	ErrInvalid = errors.New("evidencebook: invalid")
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
