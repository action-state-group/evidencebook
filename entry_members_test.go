// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"testing"

	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const termsMember = `{"amount":{"assetCode":"USD","assetScale":2,"value":"6000"},"leg":"terms","sealer_role":"payee","version":"0"}`

// An entry's Members are top-level members of the Capsule the book seals:
// the Capsule verifies, commits its header, survives a reopen and bundles.
func TestEntryMembersAreCapsuleMembers(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	book := mustOpenBook(t, shelf, "deal/a")
	entry := observation("a", "1")
	entry.Members = map[string]json.RawMessage{"settlement": json.RawMessage(termsMember), "x_note": json.RawMessage(`"n"`)}
	r := mustAppend(t, book, entry)

	sr, err := book.Sealed(ctx, r.RecordID)
	require.NoError(t, err)
	require.NoError(t, checkCapsuleCommitsHeader(sr.RecordID, sr.Capsule, sr.Header))
	var c map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sr.Capsule, &c))
	assert.JSONEq(t, termsMember, string(c["settlement"]))
	assert.JSONEq(t, `"n"`, string(c["x_note"]))
	assert.NotContains(t, string(sr.Header), "settlement", "the header does not carry the members")

	bundle, err := book.Bundle(ctx, BundleRequest{Root: r.RecordID})
	require.NoError(t, err)
	result := aacVerify(t, bundle.JSON)
	for id, capsuleResult := range result.CapsuleResults {
		assert.True(t, capsuleResult.OK, "capsule %s", id)
	}

	require.NoError(t, shelf.Release())
	again := mustOpenBook(t, fixture.open(t), "deal/a")
	sr2, err := again.Sealed(ctx, r.RecordID)
	require.NoError(t, err)
	assert.Equal(t, sr.Capsule, sr2.Capsule)
}

// The same members, in any map order, seal the same Capsule: they go in
// name order.
func TestEntryMembersSealInNameOrder(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, func() []string {
		var names []string
		for _, e := range capsuleMembers(map[string]json.RawMessage{"c": json.RawMessage(`1`), "a": json.RawMessage(`1`), "b": json.RawMessage(`1`)}) {
			names = append(names, e.Name)
		}
		return names
	}())
	assert.Empty(t, capsuleMembers(nil))
}

func TestEntryMembersRefused(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	book := mustOpenBook(t, shelf, "deal/a")
	for name, members := range map[string]map[string]json.RawMessage{
		"a base-profile member": {"chain": json.RawMessage(`{}`)},
		"the record header":     {RecordHeaderMember: json.RawMessage(`{}`)},
		"not a member name":     {"Settlement": json.RawMessage(`{}`)},
		"null":                  {"settlement": json.RawMessage(`null`)},
		"a float":               {"settlement": json.RawMessage(`{"v":1.5}`)},
		"not JSON":              {"settlement": json.RawMessage(`{`)},
		"an empty value":        {"settlement": nil},
	} {
		entry := observation("a", "1")
		entry.Members = members
		_, err := book.Append(ctx, entry)
		assert.Error(t, err, name)
	}
	assert.Equal(t, uint64(0), book.Size(), "a refused entry writes nothing")

	entry := Entry{RecordType: "observation", EpistemicType: ProducerClaim, Members: map[string]json.RawMessage{"settlement": json.RawMessage(termsMember)}}
	_, err := book.AppendCapsule(ctx, entry, nil, func(Header, []byte) (emit.Result, error) { return emit.Result{}, nil })
	assert.ErrorIs(t, err, ErrInvalid, "a caller-sealed Capsule's members are the caller's")
}

// A sent record's members go on its standalone Capsule only: the journal
// record's Capsule carries none.
func TestSealSentMembersGoOnTheSentCapsule(t *testing.T) {
	ctx := context.Background()
	shelf, _, journal := migratedWithJournal(t)
	entry := sentEntry(sentRecord)
	entry.Members = map[string]json.RawMessage{"settlement": json.RawMessage(termsMember)}
	record, sent, err := shelf.SealSent(ctx, entry, "deal-0123456789abcdef/sent")
	require.NoError(t, err)
	var c map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sent.Capsule, &c))
	assert.JSONEq(t, termsMember, string(c["settlement"]))
	sr, err := journal.Sealed(ctx, record.RecordID)
	require.NoError(t, err)
	assert.NotContains(t, string(sr.Capsule), "settlement")

	before := journal.Size()
	for name, members := range map[string]map[string]json.RawMessage{
		"the record header":     {RecordHeaderMember: json.RawMessage(`{}`)},
		"a base-profile member": {"references": json.RawMessage(`[]`)},
	} {
		entry := sentEntry(sentRecord)
		entry.Members = members
		_, _, err := shelf.SealSent(ctx, entry, "a/sent")
		assert.Error(t, err, name)
	}
	assert.Equal(t, before, journal.Size(), "a refused sent record writes nothing")
}
