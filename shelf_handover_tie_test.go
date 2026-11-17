// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"testing"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tiedHandover is a shelf whose deal/a grew after its witnessed tree tick
// (a tie with a consistency proof), whose deal/b did not (a tie of one
// size), and whose deal/c was never in a witnessed tick (untied), migrated,
// stamped and handed over.
func tiedHandover(t *testing.T) (Handover, string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	a := mustOpenBook(t, shelf, "deal/a")
	mustAppend(t, a, observation("a", "1"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/b"), observation("b", "1"))
	tree := mustTick(t, c, tickTime)
	mustWitness(t, c, tree.N)
	mustAppend(t, a, observation("a", "2"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/c"), observation("c", "1"))
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	last, err := c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	witnessAt(t, c, last.Tick, testWitnessURL, tickTime.Add(3*time.Minute))
	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	anchor, err := checkpointFromStatement(h.Anchor)
	require.NoError(t, err)
	books := map[string]string{}
	rows, err := shelf.db.Query(`SELECT DISTINCT log_id, book_id FROM shelf_entries WHERE kind = ?`, FactKindBook)
	require.NoError(t, err)
	for rows.Next() {
		var log, book string
		require.NoError(t, rows.Scan(&log, &book))
		books[log] = book
	}
	require.NoError(t, rows.Close())
	return h, anchor.KeyID, books
}

// A genesis fact handed over with its tie verifies and is not untied; one
// whose log no witnessed tick held is untied.
func TestAGenesisFactWithItsTieIsTied(t *testing.T) {
	h, key, books := tiedHandover(t)
	require.Len(t, h.Ties, 2)
	byLog := map[string]HandoverTie{}
	for _, tie := range h.Ties {
		byLog[tie.LogID] = tie
	}
	assert.NotNil(t, byLog["deal/a"].Consistency, "deal/a grew after its witnessed tick")
	assert.Nil(t, byLog["deal/b"].Consistency, "deal/b did not")
	got, err := VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)
	assert.Equal(t, []string{books["deal/c"]}, got.Untied)

	// Without its ties every genesis fact is untied, and still recomputed.
	h.Ties = nil
	got, err = VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)
	assert.Len(t, got.Untied, 3)
}

// Each change to a tie fails the handover, naming the tie.
func TestATieThatDoesNotVerifyFailsTheHandover(t *testing.T) {
	h, key, books := tiedHandover(t)
	index := map[string]int{}
	for i, tie := range h.Ties {
		index[tie.LogID] = i
	}
	a, b := index["deal/a"], index["deal/b"]
	flip := func(s string) string {
		if s[0] == '0' {
			return "1" + s[1:]
		}
		return "0" + s[1:]
	}
	for name, change := range map[string]func(h *Handover){
		"salt":                  func(h *Handover) { h.Ties[a].Salt = flip(h.Ties[a].Salt) },
		"index":                 func(h *Handover) { h.Ties[a].Index ^= 1 },
		"path":                  func(h *Handover) { h.Ties[a].Path[3] = flip(h.Ties[a].Path[3]) },
		"short path":            func(h *Handover) { h.Ties[a].Path = h.Ties[a].Path[1:] },
		"entry":                 func(h *Handover) { h.Ties[a].EntrySeq++ },
		"size":                  func(h *Handover) { h.Ties[b].Size++ },
		"old checkpoint":        func(h *Handover) { h.Ties[a].OldCheckpoint = h.Ties[b].OldCheckpoint },
		"no consistency":        func(h *Handover) { h.Ties[a].Consistency = nil },
		"consistency on a size": func(h *Handover) { h.Ties[b].Consistency = h.Ties[a].Consistency },
		"cadence checkpoint":    func(h *Handover) { h.Ties[a].CadenceCheckpoint = h.Anchor },
		"no receipt":            func(h *Handover) { h.Ties[a].Receipts = nil },
		"another book":          func(h *Handover) { h.Ties[a].BookID = books["deal/b"] },
		"no genesis":            func(h *Handover) { h.Ties[a].BookID = "no-such-book" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := h
			changed.Ties = make([]HandoverTie, len(h.Ties))
			copy(changed.Ties, h.Ties)
			changed.Ties[a].Path = append([]string(nil), h.Ties[a].Path...)
			change(&changed)
			_, err := VerifyHandover(changed, key, testWitnesses(t))
			require.ErrorIs(t, err, ErrInvalid)
			assert.Contains(t, err.Error(), "tie")
		})
	}
	// A reader whose directory names no witness verifies no tie.
	_, err := VerifyHandover(h, key, aacwitness.Directory{})
	require.ErrorIs(t, err, ErrInvalid)
}

// A cadence checkpoint the holder signed and a named witness witnessed, but
// of another shelf history (another root at the tie's position), fails the
// tie on its root, not on its receipts.
func TestATieUnderAnotherHistorysCheckpointFails(t *testing.T) {
	h, key, _ := tiedHandover(t)
	// Another shelf under the same checkpoint key and cadence log id, whose
	// first tick is witnessed.
	other := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, other)
	mustAppend(t, mustOpenBook(t, other, "deal/z"), observation("z", "1"))
	forged := mustTick(t, c, tickTime)
	cp, err := checkpointFromStatement(forged.Checkpoint)
	require.NoError(t, err)
	require.GreaterOrEqual(t, cp.Entries, h.Ties[0].EntrySeq)
	h.Ties[0].CadenceCheckpoint = forged.Checkpoint
	h.Ties[0].Receipts = []WitnessReceipt{receiptFor(t, forged.Checkpoint, testWitnessURL)}
	_, err = VerifyHandover(h, key, testWitnesses(t))
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "do not recompute the cadence checkpoint")
}

// A witnessed tree tick whose leaf kept no checkpoint statement (as a
// released capsule-cli build stored it) gives that book no tie: it is
// untied, and the handover verifies.
func TestALeafWithNoStatementLeavesItsBookUntied(t *testing.T) {
	ctx := context.Background()
	shelf := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, shelf)
	mustAppend(t, mustOpenBook(t, shelf, "deal/a"), observation("a", "1"))
	mustAppend(t, mustOpenBook(t, shelf, "deal/b"), observation("b", "1"))
	tree := mustTick(t, c, tickTime)
	mustWitness(t, c, tree.N)
	_, err := shelf.db.Exec(`UPDATE cadence_ticks SET leaves = json_remove(leaves, '$.deals."deal/b".statement') WHERE tick = ?`, tree.N)
	require.NoError(t, err)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	_, err = c.Stamp(ctx, tickTime.Add(2*time.Minute))
	require.NoError(t, err)
	h, err := c.Handover(ctx, 0)
	require.NoError(t, err)
	require.Len(t, h.Ties, 1)
	assert.Equal(t, "deal/a", h.Ties[0].LogID)
	anchor, err := checkpointFromStatement(h.Anchor)
	require.NoError(t, err)
	got, err := VerifyHandover(h, anchor.KeyID, testWitnesses(t))
	require.NoError(t, err)
	var bookB string
	require.NoError(t, shelf.db.QueryRow(`SELECT book_id FROM shelf_entries WHERE log_id = 'deal/b' LIMIT 1`).Scan(&bookB))
	assert.Equal(t, []string{bookB}, got.Untied)
}
