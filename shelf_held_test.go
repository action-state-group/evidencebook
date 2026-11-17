// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeldFirstReturnsEveryRecordAtPositionOneAcrossSenders(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{})
	f.profile.admitted["bob"] = true
	empty, err := s.HeldFirst(ctx, "deal/a")
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	mustReceive(t, s, chainRecord(t, "bob", "deal/a", "deal-2", 1, "b1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 2, "a2", "a1"), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/b", "deal-3", 1, "c1", ""), nil)
	// A fork at the genesis: alice's second record at position 1 of deal/a.
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1-other", ""), nil)

	got, err := s.HeldFirst(ctx, "deal/a")
	require.NoError(t, err)
	var keys [][2]string
	for _, h := range got {
		keys = append(keys, [2]string{h.SenderKey, h.Digest})
		assert.Equal(t, "deal/a", h.LogID)
		assert.Equal(t, uint64(1), h.Position)
		assert.NotEmpty(t, h.Record)
		assert.False(t, h.ReceivedAt.IsZero())
	}
	want := [][2]string{{"alice", dg("a1")}, {"alice", dg("a1-other")}, {"bob", dg("b1")}}
	if dg("a1-other") < dg("a1") {
		want[0], want[1] = want[1], want[0]
	}
	assert.Equal(t, want, keys, "both forked records and the other sender's, by sender then digest")

	require.NoError(t, s.Release())
	_, err = s.HeldFirst(ctx, "deal/a")
	assert.ErrorIs(t, err, ErrReleased)
}

// genesis-fork-after-proof (docs/design/461-deal-profile.md §7.3): a proof
// recomputed over the sender's records keeps its state when a fork at
// position 1 empties the counted prefix, so HeldProofs reports it covering
// nothing, before and after a restart.
func TestHeldProofsCoverNothingAfterAGenesisFork(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2", "3")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"

	// A proof past the records held is kept unchecked and covers nothing.
	mustReceive(t, s, nil, &p)
	got, err := s.HeldProofs(ctx, "alice", "deal/a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Recomputed)
	assert.False(t, got[0].Contradicted)
	assert.Zero(t, got[0].Covers)

	records := h.records(t, "alice", "deal-1")
	for _, r := range records {
		mustReceive(t, s, r, nil)
	}
	got, err = s.HeldProofs(ctx, "alice", "deal/a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	held := got[0]
	assert.True(t, held.Recomputed)
	assert.Equal(t, uint64(len(records)), held.Covers)
	assert.Equal(t, held.Verified.BookCheckpoint.Entries, held.Covers)
	// The bindings a profile checks: the fact's book id, the checkpoint key
	// and the shelf log.
	assert.Equal(t, h.bookID, held.Verified.Fact.BookID)
	assert.Equal(t, keyOf(t, p), held.Verified.KeyID)
	assert.Equal(t, h.cadence.cfg.LogID, held.Verified.ShelfCheckpoint.LogID)
	assert.Equal(t, p.Fact, held.Proof.Fact)

	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "other-genesis", ""), nil)
	check := func(s *Shelf) {
		t.Helper()
		got, err := s.HeldProofs(ctx, "alice", "deal/a")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.True(t, got[0].Recomputed, "the proof stays recomputed")
		assert.Zero(t, got[0].Covers, "but covers nothing once the prefix is empty")
		// The bindings survive a restart, so a proof over another shelf log
		// or book stays refusable after it (wrong-shelf-then-restart).
		assert.Equal(t, h.bookID, got[0].Verified.Fact.BookID)
		assert.Equal(t, h.cadence.cfg.LogID, got[0].Verified.ShelfCheckpoint.LogID)
	}
	check(s)
	require.NoError(t, s.Release())
	check(f.open(t))
}

func TestHeldProofsOfAnotherSenderOrLogAreNone(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	mustReceive(t, s, nil, &p)
	for _, k := range [][2]string{{"bob", "deal/a"}, {"alice", "deal/b"}} {
		got, err := s.HeldProofs(ctx, k[0], k[1])
		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	}
	require.NoError(t, s.Release())
	_, err := s.HeldProofs(ctx, "alice", "deal/a")
	assert.ErrorIs(t, err, ErrReleased)
}

func TestHeldProofsReportAContradictedProof(t *testing.T) {
	ctx := context.Background()
	h := newProofHolder(t, "1", "2")
	f, s := newReceiveFixture(t, ReceiveConfig{})
	p := h.proof(t, AudienceCounterparty)
	f.profile.proofKeys[keyOf(t, p)] = "alice"
	mustReceive(t, s, nil, &p)
	// As many records as the proof's checkpoint holds, which do not
	// recompute its root.
	prev := ""
	for i := range h.records(t, "alice", "deal-1") {
		name := fmt.Sprint("x", i+1)
		mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", uint64(i+1), name, prev), nil)
		prev = name
	}
	got, err := s.HeldProofs(ctx, "alice", "deal/a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Contradicted)
	assert.False(t, got[0].Recomputed)
	assert.Zero(t, got[0].Covers)
}
