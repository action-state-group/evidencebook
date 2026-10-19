// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"

	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
)

// For every input the Capsule profile of RFC 8785 accepts, the full
// serializer writes the same bytes.
func TestJCSAgreesWithCapsuleProfile(t *testing.T) {
	v, err := ijson.Parse(positiveCheckout(t))
	if err != nil {
		t.Fatal(err)
	}
	ours, err := ijson.JCS(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(positiveCheckout(t))))
	dec.UseNumber()
	var w any
	if err := dec.Decode(&w); err != nil {
		t.Fatal(err)
	}
	theirs, err := canonical.JCS(w)
	if err != nil {
		t.Fatal(err)
	}
	if string(ours) != string(theirs) {
		t.Fatalf("differ:\n%s\n%s", ours, theirs)
	}
}
