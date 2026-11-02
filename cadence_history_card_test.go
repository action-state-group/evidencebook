// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cadence's History card is a staple CheckStaple accepts: one entry per
// finished tick through the anchor tick, each checkpoint signed under the
// shelf's checkpoint key and linked to the one before by a consistency proof
// verifyCheckpointChain checks.
func TestCadenceHistoryCardIsAStapleOverTheTicks(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, _ := newCadence(t, shelf)
	holder := KeyID(fixture.cfg.Signer)

	_, err := c.HistoryCard(ctx, 0)
	require.ErrorIs(t, err, ErrNotFound, "no tick yet")

	book := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, book, observation("a", "1"))
	first := mustTick(t, c, tickTime)
	mustAppend(t, book, observation("a", "2"))
	second := mustTick(t, c, first.DueNext)
	third := mustTick(t, c, second.DueNext)

	for _, tc := range []struct {
		n       uint64
		entries int
		anchor  CadenceTick
	}{{0, 3, third}, {3, 3, third}, {2, 2, second}, {1, 1, first}} {
		staple, err := c.HistoryCard(ctx, tc.n)
		require.NoError(t, err, "tick %d", tc.n)
		got, err := CheckStaple(staple, holder)
		require.NoError(t, err, "tick %d", tc.n)
		assert.Equal(t, DerivationHistoryCard, got.Derivation)
		assert.Equal(t, digestBytes(tc.anchor.Checkpoint), got.Anchor)

		reply, err := ParseReply(staple.Reply.Message)
		require.NoError(t, err)
		last, err := verifyCheckpointChain(*reply.Envelope, checkpointKeyID(7))
		require.NoError(t, err, "tick %d", tc.n)
		assert.Equal(t, tc.anchor.Checkpoint, last)

		card, err := evidencerequest.DecodeJSON(got.Artifact)
		require.NoError(t, err)
		fields := card.(map[string]any)
		assert.Equal(t, "deal-cadence/0123456789abcdef", fields["evidence_stream"])
		history := fields["history"].([]any)
		require.Len(t, history, tc.entries)
		assert.Nil(t, history[0].(map[string]any)["consistency_from_previous"])
		for _, item := range history[1:] {
			proof, _ := item.(map[string]any)["consistency_from_previous"].(string)
			_, err := hex.DecodeString(proof)
			assert.NoError(t, err)
			assert.NotEmpty(t, proof)
		}
	}
}

func TestCadenceHistoryCardRefusals(t *testing.T) {
	ctx := context.Background()
	fixture := newShelfFixture(t)
	shelf := fixture.open(t)
	c, _ := newCadence(t, shelf)
	mustTick(t, c, tickTime)

	_, err := c.HistoryCard(ctx, 2)
	assert.True(t, errors.Is(err, ErrNotFound), "a tick not yet cut: %v", err)

	staple, err := c.HistoryCard(ctx, 1)
	require.NoError(t, err)
	_, err = CheckStaple(staple, hex.EncodeToString(make([]byte, 32)))
	assert.ErrorIs(t, err, ErrInvalid, "another holder's key")

	require.NoError(t, shelf.Release())
	_, err = c.HistoryCard(ctx, 1)
	assert.ErrorIs(t, err, ErrReleased)
}
