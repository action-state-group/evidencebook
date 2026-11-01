// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/shelf/correlation.json: which record positions a correlation
// subject answers with, by correlationOf, for records carrying each
// correlation member or none. The Rust and Python shelves read the same file.

type correlationVectorRecord struct {
	SubjectRef  string            `json:"subject_ref"`
	Correlation map[string]string `json:"correlation,omitempty"`
}

type correlationVectors struct {
	Description string                    `json:"description"`
	Records     []correlationVectorRecord `json:"records"`
	// Answers maps every identifier the records mention to the positions of
	// the records held under it, in order; an identifier held by none is [].
	Answers map[string][]int `json:"answers"`
}

func (r correlationVectorRecord) header() Header {
	h := Header{SubjectRef: r.SubjectRef}
	if len(r.Correlation) > 0 {
		h.Correlation = &Correlation{
			ExchangeID:    r.Correlation["exchange_id"],
			RequestDigest: r.Correlation["request_digest"],
			TwinBracketID: r.Correlation["twin_bracket_id"],
		}
	}
	return h
}

func buildCorrelationVectors() correlationVectors {
	digest := strings.Repeat("ab", 32)
	v := correlationVectors{
		Description: "Which record positions a correlation subject answers with (Personal-Agent History Profile v0 §1), written by shelf_correlation_vectors_test.go (EB_WRITE_SHELF_VECTORS=1) from correlationOf. A record carrying exchange_id is held under it alone; any other, including one whose exchange_id is empty, is held under its subject_ref, whatever else it carries. The Rust and Python shelves read this file.",
		Records: []correlationVectorRecord{
			{SubjectRef: "s-1", Correlation: map[string]string{"request_digest": digest}},
			{SubjectRef: "s-2", Correlation: map[string]string{"exchange_id": "x-1"}},
			{SubjectRef: "s-3", Correlation: map[string]string{"twin_bracket_id": "t-1"}},
			{SubjectRef: "s-4"},
			{SubjectRef: "x-1"},
			{SubjectRef: "s-5", Correlation: map[string]string{"exchange_id": "x-1", "request_digest": digest}},
			{SubjectRef: "s-4", Correlation: map[string]string{"twin_bracket_id": "t-2"}},
			{SubjectRef: "s-6", Correlation: map[string]string{"exchange_id": ""}},
		},
		Answers: map[string][]int{},
	}
	ids := map[string]bool{"absent": true}
	for _, r := range v.Records {
		ids[r.SubjectRef] = true
		for _, m := range r.Correlation {
			ids[m] = true
		}
	}
	for id := range ids {
		answer := []int{}
		for i, r := range v.Records {
			if correlationOf(r.header()) == id {
				answer = append(answer, i)
			}
		}
		v.Answers[id] = answer
	}
	return v
}

func TestShelfCorrelationVectors(t *testing.T) {
	path := filepath.Join("testdata", "shelf", "correlation.json")
	got, err := json.MarshalIndent(buildCorrelationVectors(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_SHELF_VECTORS") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from correlationOf; regenerate only for an intended change (EB_WRITE_SHELF_VECTORS=1)", path)
	}
}
