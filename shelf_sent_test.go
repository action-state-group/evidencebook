// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	aacverify "github.com/action-state-group/agent-action-capsule/go/verify"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sentRecord = `{"canonicalization":"jcs","deal_id":"deal-0123456789abcdef","profile":"x-deal-v1","record_type":"propose"}`

func sentEntry(payload string) Entry {
	return Entry{RecordType: "deal_step", EpistemicType: ProducerClaim, Payloads: [][]byte{[]byte(payload)}}
}

func TestSealSentSealsTheJournalRecordAndItsStandaloneCapsule(t *testing.T) {
	ctx := context.Background()
	shelf, _, journal := migratedWithJournal(t)
	before := journal.Size()

	record, sent, err := shelf.SealSent(ctx, sentEntry(sentRecord), "deal-0123456789abcdef/sent")
	require.NoError(t, err)

	// The journal record commits the record's bytes.
	sum := sha256.Sum256([]byte(sentRecord))
	assert.Equal(t, before+1, journal.Size())
	assert.Equal(t, JournalLogID, record.Header.BookID)
	assert.Equal(t, []string{hex.EncodeToString(sum[:])}, record.Header.PayloadCommitments)

	// The standalone Capsule verifies, carries the same bytes under the
	// caller's action_id, is signed by the journal's key, and shows no place
	// in the journal.
	capsule, err := decodeJSON(sent.Capsule)
	require.NoError(t, err)
	result := aacverify.Verify(capsule, nil, nil)
	require.True(t, result.OK, "%+v", result.Findings)
	require.NotNil(t, result.CapsuleID)
	assert.Equal(t, sent.CapsuleID, *result.CapsuleID)
	assert.NotEqual(t, record.RecordID, sent.CapsuleID)
	envelope, err := emit.VerifyEnvelope(sent.CapsuleID, sent.Envelope)
	require.NoError(t, err)
	assert.Equal(t, []byte(journal.signer.PublicKey()), []byte(envelope.PublicKey))
	var binding capsuleBinding
	require.NoError(t, json.Unmarshal(sent.Capsule, &binding))
	assert.Equal(t, hex.EncodeToString(sum[:]), binding.ModelAttestation.ComputeAttestation.AgentInputDigest, "the Capsule commits the record digest")
	assert.Equal(t, sentRecord, string(sent.Payload))
	assert.Contains(t, string(sent.Capsule), `"action_id":"deal-0123456789abcdef/sent"`)
	assert.NotContains(t, string(sent.Capsule), JournalLogID+"#")
}

func TestSealSentRefusesWithoutWriting(t *testing.T) {
	ctx := context.Background()
	shelf, _, journal := migratedWithJournal(t)
	before := journal.Size()
	for name, c := range map[string]struct {
		entry    Entry
		actionID string
	}{
		"no action id":      {sentEntry(sentRecord), ""},
		"no payload":        {Entry{RecordType: "deal_step", EpistemicType: ProducerClaim}, "a/sent"},
		"two payloads":      {Entry{RecordType: "deal_step", EpistemicType: ProducerClaim, Payloads: [][]byte{[]byte(sentRecord), []byte(sentRecord)}}, "a/sent"},
		"not JSON":          {sentEntry("offer"), "a/sent"},
		"not canonical":     {sentEntry(`{"b":1, "a":2}`), "a/sent"},
		"trailing data":     {sentEntry(sentRecord + "{}"), "a/sent"},
		"an invalid header": {Entry{EpistemicType: ProducerClaim, Payloads: [][]byte{[]byte(sentRecord)}}, "a/sent"},
	} {
		_, _, err := shelf.SealSent(ctx, c.entry, c.actionID)
		assert.ErrorIs(t, err, ErrInvalid, name)
	}
	assert.Equal(t, before, journal.Size(), "a refused sent record writes nothing")
}

func TestSealSentNeedsTheJournal(t *testing.T) {
	shelf := newShelfFixture(t).open(t)
	_, _, err := shelf.SealSent(context.Background(), sentEntry(sentRecord), "a/sent")
	assert.ErrorIs(t, err, ErrInvalid, "an unmigrated shelf has no journal")
	require.NoError(t, shelf.Release())
	_, _, err = shelf.SealSent(context.Background(), sentEntry(sentRecord), "a/sent")
	assert.ErrorIs(t, err, ErrReleased)
}

// A record stored whose log append fails is pending in the journal, which
// commits it on its next write: SealSent returns it and its Capsule with the
// error, so the caller sends it rather than seal it again.
func TestSealSentReturnsAPendingRecordWithItsCapsule(t *testing.T) {
	ctx := context.Background()
	shelf, _, journal := migratedWithJournal(t)
	before := journal.Size()
	journal.commitments = &flakySubstrate{Substrate: journal.commitments, failures: 1}

	record, sent, err := shelf.SealSent(ctx, sentEntry(sentRecord), "deal-0123456789abcdef/sent")
	require.Error(t, err)
	require.NotEmpty(t, record.RecordID, "the stored record is returned")
	require.NotEmpty(t, sent.CapsuleID, "with its Capsule")
	assert.Equal(t, sentRecord, string(sent.Payload))
	assert.Equal(t, before, journal.Size(), "not committed yet")

	// The next write commits it first, at the position it was given.
	next := mustAppend(t, journal, observation("journal", "next"))
	assert.Equal(t, record.Seq+1, next.Seq)
	got, err := journal.Get(ctx, record.RecordID)
	require.NoError(t, err)
	assert.Equal(t, record.Seq, got.Seq)
}
