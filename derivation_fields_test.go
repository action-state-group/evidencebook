// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A declared field is a header member or a path into one; the projection
// reveals that member only (#606, the Plan IR's field-path grammar).
func TestDerivationFieldPaths(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "book-a", 1).open(t)
	mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "o-1",
		Statement: []byte(`{"generated_at":"2026-11-01T00:00:00Z","secret":"s3cret","nested":{"a":1,"b":"hidden"}}`)})
	mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "o-2", Statement: []byte(`"just text"`)})
	mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent, SubjectRef: "o-3"})
	id, err := book.RegisterDerivation(DerivationDefinition{
		Document: []byte(`{"pack":"paths"}`), Recipient: "alice", Forms: []SubjectKind{SubjectFullHistory},
		Fields: []string{"record_type", "statement.generated_at", "statement.nested.a"},
	})
	require.NoError(t, err)
	reply, _, err := book.Respond(ctx, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: id, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}), RespondOptions{AuthenticatedRequester: "alice", Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	rows := artifactRecords(t, mustEnvelope(t, reply))
	var seen int
	for _, row := range rows {
		if row["record_type"] != "observation" {
			continue
		}
		seen++
		switch statement := row["statement"].(type) {
		case map[string]any:
			assert.Equal(t, map[string]any{"generated_at": "2026-11-01T00:00:00Z", "nested": map[string]any{"a": float64(1)}}, statement, "only the declared paths of the statement")
		case nil:
			// A statement that is text, or none, has no such member to reveal.
		default:
			t.Fatalf("a statement that is not an object was revealed: %v", statement)
		}
	}
	assert.Equal(t, 3, seen)
}

func TestDerivationFieldSetRefusals(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	register := func(fields ...string) error {
		_, err := book.RegisterDerivation(DerivationDefinition{Document: []byte(`{"f":"` + hex.EncodeToString([]byte(fields[0])) + `"}`), Recipient: "alice", Forms: []SubjectKind{SubjectFullHistory}, Fields: fields})
		return err
	}
	assert.ErrorIs(t, register("statement", "statement.generated_at"), ErrInvalid, "a path and a field it extends say two things about one member")
	assert.ErrorIs(t, register("statement.generated_at", "statement"), ErrInvalid)
	assert.ErrorIs(t, register("nosuchmember.x"), ErrInvalid)
	assert.ErrorIs(t, register("statement..x"), ErrInvalid)
	assert.ErrorIs(t, register("statement.1a"), ErrInvalid)
	assert.ErrorIs(t, register("statement."), ErrInvalid)
	assert.NoError(t, register("statement.a", "statement.b", "record_type", "record_type"))
}

func TestWithinPaths(t *testing.T) {
	paths := [][]string{{"record_type"}, {"statement", "a"}, {"statement", "b", "c"}}
	cases := []struct {
		name  string
		entry map[string]any
		want  bool
	}{
		{"declared", map[string]any{"digest": "d", "leaf_index": 1, "record_type": "x", "statement": map[string]any{"a": 1, "b": map[string]any{"c": 2}}}, true},
		{"a declared whole member may hold anything", map[string]any{"record_type": map[string]any{"anything": 1}}, true},
		{"undeclared top member", map[string]any{"seq": 1}, false},
		{"undeclared sibling", map[string]any{"statement": map[string]any{"a": 1, "secret": 2}}, false},
		{"undeclared deep sibling", map[string]any{"statement": map[string]any{"b": map[string]any{"c": 1, "d": 2}}}, false},
		{"a whole member where a path is declared", map[string]any{"statement": "text"}, false},
		{"a prefix reached whole", map[string]any{"statement": map[string]any{"b": "text"}}, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, withinPaths(c.entry, paths, true), c.name)
	}
}

func TestDerivationGrants(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	grants := []string{"carol", "bob"}
	def := DerivationDefinition{Document: []byte(`{"g":1}`), Recipient: "alice", Recipients: grants, Forms: []SubjectKind{SubjectFullHistory}, Fields: []string{"record_type"}}
	id, err := book.RegisterDerivation(def)
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "bob", "carol"}, book.derivations[id].grants, "Recipient and Recipients are one sorted set")
	grants[0] = "mallory"
	assert.Equal(t, []string{"alice", "bob", "carol"}, book.derivations[id].grants, "a caller's later edit does not change the grant")

	// The same grant in another order and spelling is the same registration.
	again := def
	again.Recipient, again.Recipients = "", []string{"carol", "alice", "bob", "alice"}
	got, err := book.RegisterDerivation(again)
	require.NoError(t, err)
	assert.Equal(t, id, got)
	// A different grant, or an empty name, is not.
	again.Recipients = []string{"alice", "bob"}
	_, err = book.RegisterDerivation(again)
	assert.ErrorIs(t, err, ErrInvalid)
	blank := def
	blank.Document, blank.Recipients = []byte(`{"g":2}`), []string{"bob", ""}
	_, err = book.RegisterDerivation(blank)
	assert.ErrorIs(t, err, ErrInvalid)
	// Proofs is part of what the digest answers.
	proofs := def
	proofs.Recipient, proofs.Recipients, proofs.Proofs = "", []string{"alice", "bob", "carol"}, true
	_, err = book.RegisterDerivation(proofs)
	assert.ErrorIs(t, err, ErrInvalid)
}

// The requester checks the proofs it is sent: a proof for another leaf, one
// dropped, and one swapped for a neighbour's are each refused.
func TestSetInclusionRefusesAlteredMaterial(t *testing.T) {
	f := newPackFixture(t, true)
	_, reply := f.ask(t, "requester-1")
	e := mustEnvelope(t, reply)
	key := responderKeys(f.holder, 1).Checkpoint
	rows := artifactRecordsAny(t, e)
	_, err := verifySetInclusion(e, rows, e.Anchor.Digest, key)
	require.NoError(t, err, "the unaltered answer verifies")

	first := slices.Index(proofKinds(e), KindCLLInclusionProof)
	swapped := cloneEnvelope(e)
	swapped.VerificationContent[first], swapped.VerificationContent[first+1] = swapped.VerificationContent[first+1], swapped.VerificationContent[first]
	_, err = verifySetInclusion(swapped, rows, e.Anchor.Digest, key)
	assert.ErrorIs(t, err, ErrInvalid, "proofs of two other leaves")

	dropped := cloneEnvelope(e)
	dropped.Verification = slices.Delete(dropped.Verification, first, first+1)
	dropped.VerificationContent = slices.Delete(dropped.VerificationContent, first, first+1)
	_, err = verifySetInclusion(dropped, rows, e.Anchor.Digest, key)
	assert.ErrorIs(t, err, ErrInvalid, "one proof short")

	_, err = verifySetInclusion(e, rows[:len(rows)-1], e.Anchor.Digest, key)
	assert.ErrorIs(t, err, ErrInvalid, "a row the holder dropped from the answer leaves a proof with no row")

	_, err = verifySetInclusion(e, rows, e.Anchor.Digest, hex.EncodeToString(make([]byte, 32)))
	assert.Error(t, err, "a checkpoint not signed under the pinned key")

	forged := cloneEnvelope(e)
	forgedRows := slices.Clone(rows)
	row := map[string]any{}
	for k, v := range forgedRows[0].(map[string]any) {
		row[k] = v
	}
	row["digest"] = digestBytes([]byte("a record the holder never sealed"))
	forgedRows[0] = row
	_, err = verifySetInclusion(forged, forgedRows, e.Anchor.Digest, key)
	assert.Error(t, err, "a digest that is not at the leaf it names")
}

func TestDoctorListsServedDerivationDigests(t *testing.T) {
	f := newPackFixture(t, true)
	findings, err := f.holder.Doctor(context.Background(), DoctorOptions{Policy: DefaultSharePolicy()})
	require.NoError(t, err)
	assert.Contains(t, findingOf(t, findings, DoctorUnsupported).Detail, f.id)
}

func artifactRecordsAny(t *testing.T, e Envelope) []any {
	t.Helper()
	decoded, err := evidencerequest.DecodeJSON(e.Artifact)
	require.NoError(t, err)
	rows, _ := decoded.(map[string]any)["records"].([]any)
	return rows
}

func proofKinds(e Envelope) []string {
	kinds := make([]string, len(e.Verification))
	for i, entry := range e.Verification {
		kinds[i] = entry.Kind
	}
	return kinds
}

func cloneEnvelope(e Envelope) Envelope {
	e.Verification = slices.Clone(e.Verification)
	e.VerificationContent = slices.Clone(e.VerificationContent)
	return e
}
