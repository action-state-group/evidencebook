// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// subjectVector is one subject bundle in testdata/subject and what
// VerifySubjectBundle makes of it.
type subjectVector struct {
	File        string `json:"file"`
	Case        string `json:"case"`
	Valid       bool   `json:"valid"`
	Form        string `json:"form,omitempty"`
	Level       string `json:"level,omitempty"`
	SubjectID   string `json:"subject_id,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
	Closed      bool   `json:"closed,omitempty"`
}

type subjectVectors struct {
	Description string          `json:"description"`
	Vectors     []subjectVector `json:"vectors"`
}

// buildSubjectVectors writes the subject bundles the vectors hold, from
// books with fixed keys and clocks.
func buildSubjectVectors(t *testing.T) (subjectVectors, map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	files := map[string][]byte{}
	v := subjectVectors{Description: "Subject bundles (subject_bundle/0, the subject convention v0, evidencebook#116 design note section 4) and what a verifier makes of each, written by subject_vectors_test.go (EB_WRITE_SUBJECT_VECTORS=1) with VerifySubjectBundle. A valid full form names its level: holder_commitment (the subject_id index's key range proof) or log_prefix (every record from seq 1 to the anchor disclosed). A projection is the holder's assertion. An invalid vector must be refused."}
	add := func(file, name string, data []byte) {
		files[file] = data
		vector := subjectVector{File: file, Case: name}
		if got, err := VerifySubjectBundle(data); err == nil {
			vector.Valid, vector.Form, vector.Level, vector.SubjectID, vector.MemberCount, vector.Closed = true, got.Form, got.Level, got.Subject.SubjectID, got.MemberCount, got.Closed
		}
		v.Vectors = append(v.Vectors, vector)
	}

	// Case (i): a field subject with two members and a close.
	book := newFixture(t, "holder", 1).open(t)
	root := subjectRoot(t, book, testSubject)
	mustAppend(t, book, observation("unrelated", "x"))
	mustAppend(t, book, member(root, testSubject, "one"))
	mustAppend(t, book, member(root, testSubject, "two"))
	_, err := book.CloseSubject(ctx, root.RecordID)
	require.NoError(t, err)
	full, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, OwnerCopy: true})
	require.NoError(t, err)
	add("full-field.json", "(i) root, two members, close: holder_commitment", full.JSON)
	add("full-field-omits-member.json", "(i) the same with M2 left out of the members: refused, the close counts 2", editExtension(t, full.JSON, func(ext *SubjectExtension) { ext.Members = ext.Members[:1] }))
	prefix, err := book.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, OwnerCopy: true, DiscloseLog: true})
	require.NoError(t, err)
	add("full-field-log-prefix.json", "(i) with the log prefix disclosed: log_prefix", prefix.JSON)

	// Case (ii): an open subject whose holder left M2 out of its index.
	var leaveOut string
	dishonest := dishonestBook(t, &leaveOut)
	droot := subjectRoot(t, dishonest, testSubject)
	mustAppend(t, dishonest, member(droot, testSubject, "one"))
	m2 := mustAppend(t, dishonest, member(droot, testSubject, "two"))
	leaveOut = m2.RecordID
	require.NoError(t, dishonest.rebuildIndexes(ctx))
	add("dishonest-first-level.json", "(ii) M2 left out of the index: passes the first level, as the index's limit allows", dishonestBundle(t, dishonest, m2.RecordID, false))
	add("dishonest-log-prefix.json", "(ii) the same with the log prefix disclosed: refused, the prefix shows M2", dishonestBundle(t, dishonest, m2.RecordID, true))

	// Case (vi): a projection, the same with and without a protected member.
	def, err := SubjectBundleDefinition(SubjectBundleProjection, []string{"peer"}, []string{"record_type", "committed_at", "correlation", "payload_commitments", "statement"})
	require.NoError(t, err)
	pbook := projectionBook(t, 1)
	id, err := pbook.RegisterDerivation(def)
	require.NoError(t, err)
	projection, err := pbook.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: testSubject, Derivation: id, Requester: "peer", ConductPacks: []string{"conduct-pack"}})
	require.NoError(t, err)
	add("projection.json", "(vi) a projection: two visible members, the protected one absent, holder_assertion", projection.JSON)

	// A per-subject book: a log root at seq 1.
	deal := newFixture(t, "deal-book", 1).open(t)
	lroot, err := deal.SealSubjectRoot(ctx, SubjectRootStatement{SubjectKind: "deal", SubjectID: "deal-1", Membership: MembershipLog}, Entry{})
	require.NoError(t, err)
	mustAppend(t, deal, observation("deal-1", "offer"))
	mustAppend(t, deal, observation("deal-1", "accept"))
	_, err = deal.CloseSubject(ctx, lroot.RecordID)
	require.NoError(t, err)
	log, err := deal.SubjectBundle(ctx, SubjectBundleRequest{SubjectID: "deal-1", OwnerCopy: true})
	require.NoError(t, err)
	add("full-log.json", "a per-subject book: log_prefix", log.JSON)
	return v, files
}

func TestSubjectVectors(t *testing.T) {
	dir := filepath.Join("testdata", "subject")
	v, files := buildSubjectVectors(t)
	manifest, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	files["vectors.json"] = append(manifest, '\n')
	if os.Getenv("EB_WRITE_SUBJECT_VECTORS") == "1" {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for name, data := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o644))
		}
	}
	for name, data := range files {
		want, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		if !bytes.Equal(data, want) {
			t.Fatalf("%s differs; regenerate only for an intended change (EB_WRITE_SUBJECT_VECTORS=1)", name)
		}
	}
	for _, vector := range v.Vectors {
		data, err := os.ReadFile(filepath.Join(dir, vector.File))
		require.NoError(t, err)
		_, err = VerifySubjectBundle(data)
		require.Equal(t, vector.Valid, err == nil, "%s: %v", vector.File, err)
	}
	valid := map[string]bool{}
	for _, vector := range v.Vectors {
		valid[vector.File] = vector.Valid
	}
	require.Equal(t, map[string]bool{
		"full-field.json": true, "full-field-omits-member.json": false, "full-field-log-prefix.json": true,
		"dishonest-first-level.json": true, "dishonest-log-prefix.json": false, "projection.json": true, "full-log.json": true,
	}, valid)
}
