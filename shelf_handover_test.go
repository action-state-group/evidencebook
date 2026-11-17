// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handedOver is a migrated shelf with two books, one grown after migration
// and one with a received-only change, a quiet stamp and a witnessed one,
// handed over at its newest stamp.
func handedOver(t *testing.T) (*Cadence, Handover, string) {
	t.Helper()
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, values := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/b"), observation("b", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	mustAppend(t, a, observation("a", "2"))
	_, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	values.set("deal/b", `{"rows":[]}`)
	_, err = c.Stamp(ctx, tickTime.Add(3*time.Minute))
	require.NoError(t, err)
	last, err := c.Stamp(ctx, tickTime.Add(4*time.Minute))
	require.NoError(t, err)
	mustWitness(t, c, last.Tick.N)
	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	anchor, err := checkpointFromStatement(h.Anchor)
	require.NoError(t, err)
	return c, h, anchor.KeyID
}

func TestAHandoverVerifiesAndReturnsTheLatestFacts(t *testing.T) {
	_, h, key := handedOver(t)
	got, err := VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)
	assert.True(t, got.Recomputed)
	assert.True(t, got.Witnessed)
	assert.NotZero(t, got.CoverageStart)
	require.Len(t, got.Latest, 2)
	assert.Len(t, got.Untied, 2, "no genesis fact carries a tie yet")
	byLog := map[string]HandoverLatest{}
	for _, l := range got.Latest {
		byLog[l.Fact.LogID] = l
	}
	assert.Greater(t, byLog["deal/a"].Fact.Size, byLog["deal/a"].Fact.PrevFactSize, "deal/a grew after migration")
	assert.Equal(t, byLog["deal/b"].Fact.Size, byLog["deal/b"].Fact.PrevFactSize, "deal/b changed only its values")
	assert.JSONEq(t, `{"rows":[]}`, string(byLog["deal/b"].StatusOpening.Value))

	// Round trip: the handover is JSON a reader in another language reads.
	b, err := json.Marshal(h)
	require.NoError(t, err)
	var back Handover
	require.NoError(t, json.Unmarshal(b, &back))
	again, err := VerifyHandover(back, key, testWitnesses(t))
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

func TestAShelfWithNoWitnessIsRecomputedAndUnwitnessed(t *testing.T) {
	_, h, key := handedOver(t)
	h.Receipts = nil
	got, err := VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)
	assert.True(t, got.Recomputed)
	assert.False(t, got.Witnessed)
}

// Each planted failure of §2.7 fails.
func TestAHandoverRefusesEachPlantedFailure(t *testing.T) {
	_, base, key := handedOver(t)
	copyOf := func() Handover {
		b, err := json.Marshal(base)
		require.NoError(t, err)
		var h Handover
		require.NoError(t, json.Unmarshal(b, &h))
		return h
	}
	factAt := func(h Handover, log string, nth int) int {
		seen := 0
		for i, d := range h.Documents {
			var f BookFact
			require.NoError(t, json.Unmarshal(d.Doc, &f))
			if f.Kind == FactKindBook && f.LogID == log {
				if seen == nth {
					return i
				}
				seen++
			}
		}
		t.Fatalf("no fact %d of %s", nth, log)
		return -1
	}
	for name, plant := range map[string]func(*Handover){
		"an entry held back": func(h *Handover) { h.Entries = h.Entries[:len(h.Entries)-1] },
		"an entry changed":   func(h *Handover) { h.Entries[0] = h.Entries[1] },
		"a fact left out": func(h *Handover) {
			i := factAt(*h, "deal/a", 1)
			h.Documents = append(h.Documents[:i], h.Documents[i+1:]...)
		},
		"a changed opening": func(h *Handover) {
			i := factAt(*h, "deal/b", 1)
			h.Documents[i].StatusOpening.Value = json.RawMessage(`{"rows":[1]}`)
		},
		"a missing opening": func(h *Handover) { h.Documents[factAt(*h, "deal/a", 0)].StatusOpening = nil },
		"a replaced book (no proof)": func(h *Handover) {
			h.Consistency = nil
		},
		"a book checkpoint left out": func(h *Handover) { h.BookCheckpoints = h.BookCheckpoints[:len(h.BookCheckpoints)-1] },
		"another holder's anchor":    func(h *Handover) { h.LogID = "deal-cadence/ffffffffffffffff" },
	} {
		t.Run(name, func(t *testing.T) {
			h := copyOf()
			plant(&h)
			_, err := VerifyHandover(h, key, testWitnesses(t))
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
	_, err := VerifyHandover(base, "not-the-key", testWitnesses(t))
	assert.ErrorIs(t, err, ErrInvalid, "another key")
}

// A dropped book is caught by the count: the latest facts the replay returns
// count every book, so a card that leaves one out counts differently.
func TestADroppedBookIsCaughtByTheCount(t *testing.T) {
	_, h, key := handedOver(t)
	got, err := VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)
	counted := []string{}
	for _, l := range got.Latest {
		counted = append(counted, l.Fact.LogID)
	}
	assert.ElementsMatch(t, []string{"deal/a", "deal/b"}, counted, "the replay counts every book on the shelf")
	assert.NotEqual(t, len(counted), 1, "a card that dropped deal/b counts one book")
}

func TestAHandoverNeedsAFinishedStamp(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	old := mustTick(t, c, tickTime)
	_, err := c.Handover(ctx, old.N)
	assert.ErrorIs(t, err, ErrInvalid, "a tree tick is not a stamp")
}
