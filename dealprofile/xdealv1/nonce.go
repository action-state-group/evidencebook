// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewNonce draws a fresh nonce for a journal record whose digest leaves the
// holder (PROFILE §5): 256 bits from crypto/rand, as 64 lowercase hex. Draw
// one for each record and never reuse it; a constant, a counter or a value
// derived from a time passes every check and still lets the citing record's
// digest test guesses at the record.
func NewNonce() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("xdealv1: draw a nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
