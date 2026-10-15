// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"errors"
	"testing"
)

// The protocol types are go/evidencerequest's; their errors must still
// match evidencebook's ErrInvalid, which callers test for.
func TestProtocolTypeErrorsMatchErrInvalid(t *testing.T) {
	if err := (Refusal{}).Verify(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Refusal.Verify: %v does not match ErrInvalid", err)
	}
	if err := (Envelope{}).Verify(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Envelope.Verify: %v does not match ErrInvalid", err)
	}
	if _, err := ParseReply(Message{Octets: []byte(`{"neither":true}`), MediaType: MediaTypeJSON}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ParseReply: %v does not match ErrInvalid", err)
	}
	if _, err := EncodeRequest(EvidenceRequest{}, "text/plain"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("EncodeRequest: %v does not match ErrInvalid", err)
	}
}
