// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"errors"
	"testing"
)

// FR-4, profile §11.4.2 and §11.4.3: the manifest says what can be asked,
// never what exists, and is the same before and after any history.
func TestManifestIsHistoryFree(t *testing.T) {
	book := newFixture(t, "book-m", 6).open(t)
	options := ManifestOptions{Bindings: []string{BindingHTTP, BindingStream, BindingHTTP}}
	empty, err := book.Manifest(options)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"bindings":["evidence-request/1","http"],"derivations":["history_card/1"],"drafts":["draft-mih-agent-evidence-request-00","draft-mih-agent-evidence-request-01","draft-mih-agent-evidence-request-02"],"profiles":["evidence-interop/0","evidence-interop/1"],"refusal_policy":"specific","subject_forms":["checkpoints","correlation","exchange","full_history","range","record"]}`
	if string(empty) != want {
		t.Fatalf("manifest\n got %s\nwant %s", empty, want)
	}
	for i := range 3 {
		mustAppend(t, book, observation("m", string(rune('a'+i))))
	}
	anchorPin(t, book)
	respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}), "peer-1")
	later, err := book.Manifest(options)
	if err != nil || !bytes.Equal(empty, later) {
		t.Fatalf("the manifest changed with history: %s (%v)", later, err)
	}
	for _, bad := range [][]string{nil, {"grpc"}} {
		if _, err := book.Manifest(ManifestOptions{Bindings: bad}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bindings %v accepted", bad)
		}
	}
}
