// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	// A receipt with a registration time: only such a receipt gives the
	// handover the witnessed level (§2.7).
	check, err := c.Witnessed(ctx, last.Tick.N, receiptAt(t, last.Tick.Checkpoint, testWitnessURL, tickTime.Add(5*time.Minute)))
	require.NoError(t, err)
	require.Equal(t, WitnessVerified, check.State)
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
	assert.Len(t, got.Untied, 2, "no witnessed tree tick held either log, so neither genesis fact is tied")
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

// A document the holder signed must be its canonical JSON, so no language
// reads a member another does not: a duplicate, an unknown member or another
// spelling is refused.
func TestAnEntryDocumentMustBeCanonical(t *testing.T) {
	_, h, _ := handedOver(t)
	var doc []byte
	for _, d := range h.Documents {
		if d.StatusOpening != nil {
			doc = d.Doc
			break
		}
	}
	require.NotNil(t, doc)
	require.NoError(t, canonicalDocument(doc, &BookFact{}))
	body := string(doc[1 : len(doc)-1])
	for name, changed := range map[string]string{
		"a duplicate member": `{"v":2,` + body + `}`,
		"an unknown member":  `{"extra":1,` + body + `}`,
		"whitespace":         `{ ` + body + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, canonicalDocument([]byte(changed), &BookFact{}))
		})
	}
}

// The chain of one book's facts (§2.7), on deal/a's two facts of a real
// handover: each break is a replaced book.
func TestAFactChainRefusesAReplacedBook(t *testing.T) {
	_, h, key := handedOver(t)
	_, err := VerifyHandover(h, key, testWitnesses(t))
	require.NoError(t, err)
	statements := map[string]Checkpoint{}
	for _, b := range h.BookCheckpoints {
		cp, err := checkpointFromStatement(b)
		require.NoError(t, err)
		sum := sha256.Sum256(b)
		statements[hex.EncodeToString(sum[:])] = cp
	}
	proofs := map[string]ConsistencyEvidence{}
	for _, p := range h.Consistency {
		proofs[fmt.Sprintf("%s %d %d", p.LogID, p.FromSize, p.ToSize)] = p.Proof
	}
	var a []BookFact
	for _, d := range h.Documents {
		var f BookFact
		require.NoError(t, json.Unmarshal(d.Doc, &f))
		if f.Kind == FactKindBook && f.LogID == "deal/a" {
			a = append(a, f)
		}
	}
	require.Len(t, a, 2)
	prev, next := a[0], a[1]
	require.Greater(t, next.Size, prev.Size)
	require.NoError(t, checkFactChain(prev, next, statements, proofs))
	same := next
	same.PrevFactSize = next.Size
	require.NoError(t, checkFactChain(next, same, statements, proofs), "a later fact at the same size and checkpoint (a changed value)")

	wrongProof := map[string]ConsistencyEvidence{}
	for k, p := range proofs {
		wrongProof[k] = p
	}
	key2 := fmt.Sprintf("deal/a %d %d", prev.Size, next.Size)
	forged := proofs[key2]
	var wire map[string]any
	require.NoError(t, json.Unmarshal(forged.Proof, &wire))
	peaks := wire["old_peaks"].([]any)
	peaks[0] = strings.Repeat("00", 32)
	forged.Proof, err = json.Marshal(wire)
	require.NoError(t, err)
	wrongProof[key2] = forged

	for name, c := range map[string]struct {
		p, f   BookFact
		proofs map[string]ConsistencyEvidence
	}{
		"another log":           {prev, func() BookFact { f := next; f.LogID = "deal/b"; return f }(), proofs},
		"a wrong previous size": {prev, func() BookFact { f := next; f.PrevFactSize = prev.Size + 1; return f }(), proofs},
		"a shrinking book":      {next, func() BookFact { f := prev; f.PrevFactSize = next.Size; return f }(), proofs},
		"the same size, another checkpoint": {prev, func() BookFact {
			f := prev
			f.PrevFactSize = prev.Size
			f.CheckpointSHA256 = next.CheckpointSHA256
			return f
		}(), proofs},
		"no proof":      {prev, next, map[string]ConsistencyEvidence{}},
		"a wrong proof": {prev, next, wrongProof},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkFactChain(c.p, c.f, statements, c.proofs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "a replaced book")
		})
	}
}

func TestAHandoverOfAnotherVersionIsRefused(t *testing.T) {
	_, h, key := handedOver(t)
	for _, v := range []int{0, 2} {
		h.V = v
		_, err := VerifyHandover(h, key, testWitnesses(t))
		assert.ErrorIs(t, err, ErrInvalid, "version %d", v)
	}
}
