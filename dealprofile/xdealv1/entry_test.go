// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/internal/shelfscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// copyBundle is p's full copy of logID for audience, as `deal report
// --share ... --bundle` writes it: the book's records at the report's
// checkpoint, with the report and its x-deal-v1 extension.
func copyBundle(t *testing.T, p *party, logID, audience string) []byte {
	t.Helper()
	ctx := context.Background()
	c := copyAt(p.now.Format(time.RFC3339), audience)
	if audience == "keep" {
		c = keepCopy(p.now.Format(time.RFC3339))
	}
	r, pair, x := holderBundle(t, p, p.name, logID, c)
	book := mustOpen(t, p, logID)
	cps, err := book.Checkpoints(ctx)
	require.NoError(t, err)
	var anchor *evidencebook.Checkpoint
	for i := range cps {
		if cps[i].Root == r.DealCheckpoint.Root {
			anchor = &cps[i]
		}
	}
	require.NotNil(t, anchor, "the report's checkpoint is the book's")
	records, err := book.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: anchor.Entries})
	require.NoError(t, err)
	ids := make([]string, len(records))
	for i, rec := range records {
		ids[i] = rec.RecordID
	}
	var carried struct {
		Record     json.RawMessage `json:"record"`
		AgentInput json.RawMessage `json:"agent_input"`
	}
	require.NoError(t, json.Unmarshal(pair, &carried))
	reportExt, err := json.Marshal(map[string]json.RawMessage{"record": carried.Record, "agent_input": carried.AgentInput})
	require.NoError(t, err)
	b, err := book.Bundle(ctx, evidencebook.BundleRequest{
		Root: ids[len(ids)-1], Include: ids, Payloads: evidencebook.PayloadsAll, At: anchor, DisclosureRecordedElsewhere: true,
		Extensions: map[string]json.RawMessage{Profile: extJSON(t, x), dealprofile.ReportExtension: reportExt},
	})
	require.NoError(t, err)
	return b.JSON
}

// entryCase is caseFixtureWith plus the adjudicator's final ruling, sealed
// at rulingAt; it returns the handshake, the adjudicator, the ruling's push
// pair and digest.
func entryCase(t *testing.T, edit func(*AgreeBody), rulingAt time.Time) (*handshake, *party, []byte, string) {
	t.Helper()
	h, adjudicator, dispute, buyerCopy := caseFixtureWith(t, edit)
	c, err := CheckCase(dispute, []CaseCopy{buyerCopy}, adjudicator.key())
	require.NoError(t, err)
	sealed, err := SealRuling(context.Background(), adjudicator.shelf, c, c.RulingOf(ValueNotMet, Some(RoleSeller), nil, true, ""), rulingAt)
	require.NoError(t, err)
	return h, adjudicator, sealed.Pair, sealed.Digest
}

// The buyer's copy after it received and named the ruling shows it
// entered, unwitnessed without a receipt; a copy whose report predates the
// ruling shows nothing; one after the ruling with no naming statement shows
// it not entered as of the checkpoint.
func TestEntryOf(t *testing.T) {
	early := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	h, adjudicator, rulingPair, ruling := entryCase(t, nil, early)
	at := early.Format(time.RFC3339)

	// The buyer's copy taken before it received the ruling, after the
	// ruling's time: not entered as of its checkpoint.
	before := copyBundle(t, h.buyer, caseBuyerLog, dealprofile.AudienceAdjudicator)
	e, err := EntryOf(EntryInput{Bundle: before, Ruling: ruling, RulingAt: at, AdjudicatorKey: adjudicator.key()})
	require.NoError(t, err)
	assert.Equal(t, EntryNotEntered, e.State)
	assert.Equal(t, RoleBuyer, e.Role)
	assert.Equal(t, h.buyer.key(), e.Key)
	assert.NotEmpty(t, e.CheckpointAt)

	// The same copy against a ruling dated after its report: not shown.
	e, err = EntryOf(EntryInput{Bundle: before, Ruling: ruling, RulingAt: "2027-01-01T00:00:00Z", AdjudicatorKey: adjudicator.key()})
	require.NoError(t, err)
	assert.Equal(t, EntryNotShown, e.State)

	// The buyer receives the ruling, which names it in its book.
	h.buyer.receive(rulingPair, nil)
	_, err = h.buyer.recStampTick()
	require.NoError(t, err)
	after := copyBundle(t, h.buyer, caseBuyerLog, dealprofile.AudienceAdjudicator)
	e, err = EntryOf(EntryInput{Bundle: after, Ruling: ruling, RulingAt: at, AdjudicatorKey: adjudicator.key()})
	require.NoError(t, err)
	assert.Equal(t, EntryUnwitnessed, e.State, "no receipt")
	assert.NotEmpty(t, e.Statement)
	// A statement naming this ruling does not name another one.
	e, err = EntryOf(EntryInput{Bundle: after, Ruling: hx('6'), RulingAt: at, AdjudicatorKey: adjudicator.key()})
	require.NoError(t, err)
	assert.Equal(t, EntryNotEntered, e.State, "another ruling")

	// Refusals: another adjudicator, and a bundle that is not a copy.
	_, err = EntryOf(EntryInput{Bundle: after, Ruling: ruling, RulingAt: at, AdjudicatorKey: h.seller.key()})
	assert.ErrorContains(t, err, "does not name this adjudicator")
	_, err = EntryOf(EntryInput{Bundle: []byte(`{}`), Ruling: ruling, RulingAt: at, AdjudicatorKey: adjudicator.key()})
	assert.Error(t, err)
}

// A receipt from a witness the agreement names, over the cadence
// checkpoint carrying the copy's book checkpoint, makes the entry
// witnessed; the same receipt counts for nothing when the agreement does not
// name that witness, and a stamp proof of another checkpoint gives none.
func TestEntryOfWitnessed(t *testing.T) {
	early := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	scriptWitness := Witness{KeyID: hex.EncodeToString(shelfscript.WitnessKey.Public().(ed25519.PublicKey)), URL: shelfscript.WitnessURL}
	dir, err := shelfscript.Directory()
	require.NoError(t, err)
	for _, named := range []bool{true, false} {
		edit := func(ab *AgreeBody) {
			if named {
				ab.Witnesses = []Witness{scriptWitness}
			}
		}
		h, adjudicator, rulingPair, ruling := entryCase(t, edit, early)
		book := mustOpen(t, h.buyer, caseBuyerLog)
		first := readBack(t, book, 1).RecordID
		earlier, err := h.buyer.cadence.StampProof(context.Background(), first, 0, evidencebook.AudienceCounterparty)
		require.NoError(t, err)
		h.buyer.receive(rulingPair, nil)
		_, err = h.buyer.recStampTick()
		require.NoError(t, err)
		bundle := copyBundle(t, h.buyer, caseBuyerLog, dealprofile.AudienceAdjudicator)
		proof, err := h.buyer.cadence.StampProof(context.Background(), first, 0, evidencebook.AudienceCounterparty)
		require.NoError(t, err)
		receipt, err := shelfscript.MintReceipt(proof.ShelfCheckpoint)
		require.NoError(t, err)
		in := EntryInput{Bundle: bundle, Ruling: ruling, RulingAt: early.Format(time.RFC3339), AdjudicatorKey: adjudicator.key(),
			StampProof: &proof, Receipts: []evidencebook.WitnessReceipt{receipt}, Witnesses: dir}
		e, err := EntryOf(in)
		require.NoError(t, err)
		if named {
			assert.Equal(t, EntryEntered, e.State)
			// The buyer's proof of its checkpoint before the ruling, with a
			// valid receipt over it, carries another book checkpoint.
			earlierReceipt, err := shelfscript.MintReceipt(earlier.ShelfCheckpoint)
			require.NoError(t, err)
			in.StampProof, in.Receipts = &earlier, []evidencebook.WitnessReceipt{earlierReceipt}
			e, err = EntryOf(in)
			require.NoError(t, err)
			assert.Equal(t, EntryUnwitnessed, e.State)
		} else {
			assert.Equal(t, EntryUnwitnessed, e.State, "a witness the agreement does not name")
		}
	}
}
