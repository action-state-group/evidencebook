// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type headerSchema struct {
	Defs struct {
		EpistemicType  struct{ Enum []string } `json:"epistemicType"`
		LinkType       struct{ Enum []string } `json:"linkType"`
		RetentionState struct{ Enum []string } `json:"retentionState"`
	} `json:"$defs"`
}

func sorted(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

// TestEpistemicTypeThreeWayParity: one value set, three places. The vendored
// JSON is a byte-for-byte copy of the file the other implementations' parity
// tests read (the Rust evidencebook crate vendors the same file); the Go
// constants and the published header schema must both equal it. The vendored copy is upper-case and the wire form lower-case, so the
// set is compared case-folded; the wire casing itself is pinned by the
// schema enum and the Go constants, which must match exactly.
func TestEpistemicTypeThreeWayParity(t *testing.T) {
	vendored := readJSON[struct{ Values []string }](t, "schemas/vendor/epistemic-types.json").Values
	var folded []string
	for _, v := range vendored {
		folded = append(folded, strings.ToLower(v))
	}
	var implementation []string
	for _, v := range EpistemicTypes() {
		implementation = append(implementation, string(v))
	}
	schema := readJSON[headerSchema](t, "schemas/record-header.schema.json")
	if !slices.Equal(sorted(folded), sorted(implementation)) {
		t.Fatalf("Go EpistemicTypes %v drifted from vendored set %v", implementation, folded)
	}
	if !slices.Equal(sorted(schema.Defs.EpistemicType.Enum), sorted(implementation)) {
		t.Fatalf("header schema enum %v drifted from Go EpistemicTypes %v", schema.Defs.EpistemicType.Enum, implementation)
	}
	for _, v := range implementation {
		if !EpistemicType(v).Valid() {
			t.Fatalf("%s is listed but not Valid", v)
		}
	}
	if EpistemicType("verified_fact").Valid() {
		t.Fatal("an unregistered epistemic type validated")
	}
}

func TestLinkAndRetentionVocabularyParity(t *testing.T) {
	schema := readJSON[headerSchema](t, "schemas/record-header.schema.json")
	var links []string
	for _, v := range LinkTypes() {
		links = append(links, string(v))
	}
	if !slices.Equal(sorted(schema.Defs.LinkType.Enum), sorted(links)) {
		t.Fatalf("schema link types %v != Go %v", schema.Defs.LinkType.Enum, links)
	}
	for _, v := range schema.Defs.RetentionState.Enum {
		if !RetentionState(v).Valid() {
			t.Fatalf("schema retention state %s is not Valid in Go", v)
		}
	}
	if len(schema.Defs.RetentionState.Enum) != 5 || RetentionState("ARCHIVED").Valid() {
		t.Fatal("retention states are not exactly the five registered values")
	}
}
