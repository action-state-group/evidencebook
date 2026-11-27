// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A statement is read the same way at admission and after reopen, and by
// exact member names (evidencebook#1072). Before the fix, a subject_root
// stating {"subject_id":"b","SUBJECT_ID":"a"} was admitted as subject "a"
// (encoding/json v1 folds case and keeps the last), but its canonical header
// (SUBJECT_ID sorts first) read as "b" after reopen: ProveKeyRange("a") went
// from 3 matches to 0 and members under "a" were refused.
func TestSubjectRootRefusesCaseVariantNames(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "holder", 1)
	book := f.open(t)
	for _, statement := range []string{
		`{"subject_kind":"exchange","subject_id":"b","SUBJECT_ID":"a","membership":"field:/correlation/exchange_id"}`,
		`{"subject_kind":"exchange","SUBJECT_ID":"a","membership":"field:/correlation/exchange_id"}`,
		`{"subject_kind":"exchange","subject_id":"a","membership":"field:/correlation/exchange_id","Membership":"log"}`,
		`{"subject_kind":"exchange","ſubject_id":"a","subject_id":"b","membership":"field:/correlation/exchange_id"}`,
		`{"subject_kind":"exchange","subject_id":"a","subject_id":"b","membership":"field:/correlation/exchange_id"}`,
	} {
		_, err := book.Append(ctx, Entry{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Links: []Link{}, Statement: json.RawMessage(statement)})
		assert.ErrorIs(t, err, ErrInvalid, statement)
	}
	assert.Zero(t, book.Size(), "nothing was stored")

	// The exact spelling is admitted, and reads the same after reopen.
	root, err := book.Append(ctx, Entry{RecordType: RecordTypeSubjectRoot, EpistemicType: ProducerClaim, Links: []Link{},
		Statement: json.RawMessage(`{"subject_kind":"exchange","subject_id":"a","membership":"field:/correlation/exchange_id"}`)})
	require.NoError(t, err)
	mustAppend(t, book, member(root, "a", "one"))
	mustAppend(t, book, member(root, "a", "two"))
	before, err := book.ProveKeyRange(ctx, SubjectIDIndexName, "a", "a")
	require.NoError(t, err)
	require.Len(t, before.Proof.Matches, 3)
	require.NoError(t, book.Release())
	again := f.open(t)
	after, err := again.ProveKeyRange(ctx, SubjectIDIndexName, "a", "a")
	require.NoError(t, err)
	assert.Len(t, after.Proof.Matches, 3)
	mustAppend(t, again, member(root, "a", "three"))
}

// Any statement is held in its canonical form, so a reader that folds case
// (retention's lifecycle reader) sees the same statement before reopen and
// after; a repeated member name is refused rather than reduced to one value.
func TestStatementReadsTheSameAfterReopen(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "holder", 1)
	book := f.open(t)
	target := mustAppend(t, book, observation("s", "p"))
	lifecycle := func(statement string) (Record, error) {
		return book.Append(ctx, Entry{RecordType: RecordTypeLifecycle, EpistemicType: ProducerClaim,
			Links: []Link{{Type: Cites, Target: target.RecordID}}, Statement: json.RawMessage(statement)})
	}
	_, err := lifecycle(`{"retention_state":"LEGAL_HOLD","retention_state":"AVAILABLE"}`)
	assert.ErrorIs(t, err, ErrInvalid, "a repeated member name")
	_, err = lifecycle("{\"retention_state\":\"AVAILABLE\",\"reason\":\"\xff\"}")
	assert.ErrorIs(t, err, ErrInvalid, "invalid UTF-8")
	for _, number := range []string{`1.5`, `9007199254740993`} {
		_, err = lifecycle(`{"retention_state":"AVAILABLE","n":` + number + `}`)
		assert.ErrorIs(t, err, ErrInvalid, "a number JCS cannot carry: %s", number)
	}

	sealed, err := lifecycle(`{"retention_state":"AVAILABLE", "RETENTION_STATE":"LEGAL_HOLD"}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"RETENTION_STATE":"LEGAL_HOLD","retention_state":"AVAILABLE"}`, string(sealed.Header.Statement))
	assert.Equal(t, `{"RETENTION_STATE":"LEGAL_HOLD","retention_state":"AVAILABLE"}`, string(sealed.Header.Statement), "the record Append returns holds the canonical statement")
	state, err := book.Retention(ctx, target.RecordID)
	require.NoError(t, err)
	require.NoError(t, book.Release())
	again := f.open(t)
	reopened, err := again.Retention(ctx, target.RecordID)
	require.NoError(t, err)
	assert.Equal(t, state, reopened)
}

// A subject_root an earlier release admitted under case-folded names no
// longer reads as one; the book refuses to open rather than drop the
// subject (and the close that bounds it) silently.
func TestRefuseLegacySubjectRoot(t *testing.T) {
	root := func(statement string) Record {
		return Record{RecordID: dg("root"), Seq: 2, Header: Header{RecordType: RecordTypeSubjectRoot, Statement: json.RawMessage(statement)}}
	}
	assert.ErrorIs(t, refuseLegacySubjectRoot(root(`{"SUBJECT_ID":"a","membership":"field:/correlation/exchange_id","subject_kind":"exchange"}`)), ErrCorrupt)
	assert.ErrorIs(t, refuseLegacySubjectRoot(root(`{"SUBJECT_ID":"a","membership":"log","subject_id":"b","subject_kind":"exchange"}`)), ErrCorrupt)
	assert.NoError(t, refuseLegacySubjectRoot(root(`{"membership":"log","subject_id":"a","subject_kind":"deal"}`)), "a subject_root that reads exactly")
	assert.NoError(t, refuseLegacySubjectRoot(root(`{"note":"a caller's own record type of this name"}`)), "never a subject_root under any reading")
	assert.NoError(t, refuseLegacySubjectRoot(root(`{"SUBJECT_ID":"","membership":"log","subject_kind":"deal"}`)), "not a valid subject_root under the old reading either")
	other := root(`{"SUBJECT_ID":"a","membership":"log","subject_kind":"deal"}`)
	other.Header.RecordType = "observation"
	assert.NoError(t, refuseLegacySubjectRoot(other))
}

// A verifier reads an index_root statement and a disclosed header by exact
// member names, each once.
func TestVerifierReadsExactNames(t *testing.T) {
	h := Header{RecordType: RecordTypeIndexRoot}
	for statement, ok := range map[string]bool{
		`{"as_of_seq":3,"index":"subject_id","root":"r","size":1}`:            true,
		`{"as_of_seq":3,"index":"subject_id","ROOT":"r","size":1}`:            false,
		`{"as_of_seq":3,"index":"subject_id","root":"r","Root":"x","size":1}`: false,
		`{"as_of_seq":3,"index":"subject_id","root":"r","root":"x","size":1}`: false,
	} {
		h.Statement = json.RawMessage(statement)
		committed, err := readIndexRoot(h)
		if ok {
			require.NoError(t, err, statement)
			assert.Equal(t, committedIndexRoot{IndexRoot: IndexRoot{Index: "subject_id", Root: "r", Size: 1}, AsOfSeq: 3}, committed)
		} else {
			assert.ErrorIs(t, err, ErrInvalid, statement)
		}
	}

	f := newFixture(t, "holder", 1)
	book := f.open(t)
	record := mustAppend(t, book, observation("s", "p"))
	raw, err := canonicalJSON(record.Header)
	require.NoError(t, err)
	_, err = strictHeader(raw)
	require.NoError(t, err)
	repeated := append([]byte(`{"seq":1,`), raw[1:]...)
	_, err = strictHeader(repeated)
	assert.ErrorIs(t, err, ErrInvalid, "a header repeating seq, with the value it states")
}
