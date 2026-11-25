// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// halfOf builds the seller's half for the buyer from the handshake.
func halfOf(t *testing.T, h *handshake) []byte {
	t.Helper()
	ext, err := MakeCounterpartyHalf(context.Background(), [][]byte{h.agreementPair}, h.proof)
	require.NoError(t, err)
	return ext
}

// The half is what the History card reads for "stamped by both": the
// counterparty's agreement under the stamp proof that covers it, here
// checked from the copy's extension alone (evidencebook#850).
func TestCounterpartyHalfVerifies(t *testing.T) {
	h := newHandshake(t, nil)
	_, _, holder := gateBook(t, h, true)
	read, err := VerifyCounterpartyHalf(holder, halfOf(t, h))
	require.NoError(t, err)
	assert.Equal(t, HalfRead{Key: h.seller.key(), RecordID: h.agreementID, LogID: h.sellerLog, Size: 1}, read)
}

func TestCounterpartyHalfRefusals(t *testing.T) {
	h := newHandshake(t, nil)
	_, _, holder := gateBook(t, h, true)
	ext := halfOf(t, h)
	edit := func(f func(*CounterpartyHalf)) []byte {
		var half CounterpartyHalf
		require.NoError(t, json.Unmarshal(ext, &half))
		f(&half)
		out, err := json.Marshal(half)
		require.NoError(t, err)
		return out
	}
	editBundle := func(f func(map[string]any)) []byte {
		return edit(func(half *CounterpartyHalf) {
			var b map[string]any
			require.NoError(t, json.Unmarshal(half.Bundle, &b))
			f(b)
			half.Bundle, _ = json.Marshal(b)
		})
	}

	// Another agreement of the seller's, sealed and stamped on its own.
	other := newHandshake(t, func(a *AgreeBody) { a.Admit = ref('9') })
	// The buyer's agreement on another offer.
	otherOffer := func() []byte {
		ab := fxAgreement(ref('8'), ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
		ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
		b := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
		b.Seq, b.Seen = 1, Some(h.agreementID)
		r := Record{Block: b, Body: ab}
		require.NoError(t, Check(r))
		_, pair := h.buyer.seal("deal/b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", r)
		return pair
	}()
	// Both agreements of one role.
	sameRole := newHandshake(t, func(a *AgreeBody) { a.Role = RoleBuyer })
	_, _, sameRoleHolder := gateBook(t, sameRole, true)
	// A proof of another record (the other handshake's agreement).
	foreignProof := edit(func(half *CounterpartyHalf) { half.StampProof = other.proof })
	// The bundle's anchor and the proof's book checkpoint differ.
	// A later proof of the same book: it verifies and is of their
	// agreement, but its book checkpoint is not the bundle's anchor.
	later := newHandshake(t, nil)
	_, _, laterHolder := gateBook(t, later, true)
	laterExt := halfOf(t, later)
	sealSecond(t, later)
	anchorOff := func() []byte {
		var half CounterpartyHalf
		require.NoError(t, json.Unmarshal(laterExt, &half))
		half.StampProof = later.seller.stamp(later.agreementID)
		out, err := json.Marshal(half)
		require.NoError(t, err)
		return out
	}()
	// The root's payload withheld: the bundle no longer discloses it.
	withheld := editBundle(func(b map[string]any) { delete(b["extensions"].(map[string]any), evidencebook.ExtensionPayloads) })
	// A membership claim the producer did not make.
	tampered := editBundle(func(b map[string]any) { b["root"] = "00" + b["root"].(string)[2:] })

	for name, c := range map[string]struct{ holder, ext []byte }{
		"no layer":             {func() []byte { _, _, p := gateBook(t, newHandshake(t, nil), false); return p }(), ext},
		"another offer":        {otherOffer, ext},
		"not the other role":   {sameRoleHolder, halfOf(t, sameRole)},
		"a proof of another":   {holder, foreignProof},
		"anchor is not proof":  {laterHolder, anchorOff},
		"payload withheld":     {holder, withheld},
		"tampered bundle":      {holder, tampered},
		"an extra member":      {holder, []byte(`{"bundle":{},"stamp_proof":{},"x":1}`)},
		"not an object":        {holder, []byte(`[]`)},
		"the holder's half":    {ext, ext},
		"the seller's holding": {h.agreementPair, ext},
	} {
		_, err := VerifyCounterpartyHalf(c.holder, c.ext)
		assert.Error(t, err, name)
	}
}

// A half is built only from the counterparty's records from seq 1 that
// recompute the proof's checkpoint.
func TestMakeCounterpartyHalfRefusesRecordsTheProofDoesNotCover(t *testing.T) {
	h := newHandshake(t, nil)
	other := newHandshake(t, func(a *AgreeBody) { a.Admit = ref('9') })
	_, err := MakeCounterpartyHalf(context.Background(), [][]byte{other.agreementPair}, h.proof)
	assert.Error(t, err, "another record")
	_, err = MakeCounterpartyHalf(context.Background(), nil, h.proof)
	assert.Error(t, err, "no record")
	_, err = MakeCounterpartyHalf(context.Background(), [][]byte{h.agreementPair, h.agreementPair}, h.proof)
	assert.Error(t, err, "more records than the checkpoint")
}

// sealSecond seals the seller's second record, a statement, in its deal
// book, and returns its pair.
func sealSecond(t *testing.T, h *handshake) []byte {
	t.Helper()
	stmt := gateAct(2, &StatementBody{Statements: statements(item("delivered", ValueMet))})
	d, err := Digest(h.agreement)
	require.NoError(t, err)
	prev := RefTo(d)
	stmt.Block.Prev, stmt.Block.AgreementRef, stmt.Block.Seen = &prev, &prev, NullOf[string]()
	require.NoError(t, Check(stmt))
	_, pair := h.seller.seal(h.sellerLog, stmt)
	return pair
}

// A half of a book of two records carries both, the second withheld, and
// verifies under the proof that covers both.
func TestCounterpartyHalfOfALongerBook(t *testing.T) {
	h := newHandshake(t, nil)
	_, _, holder := gateBook(t, h, true)
	second := sealSecond(t, h)
	proof := h.seller.stamp(h.agreementID)
	ext, err := MakeCounterpartyHalf(context.Background(), [][]byte{h.agreementPair, second}, proof)
	require.NoError(t, err)
	read, err := VerifyCounterpartyHalf(holder, ext)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), read.Size)
	var half CounterpartyHalf
	require.NoError(t, json.Unmarshal(ext, &half))
	var b struct {
		Disclosures map[string]json.RawMessage `json:"disclosures"`
	}
	require.NoError(t, json.Unmarshal(half.Bundle, &b))
	assert.Len(t, b.Disclosures, 1, "only the agreement is disclosed")
	_, err = MakeCounterpartyHalf(context.Background(), [][]byte{h.agreementPair}, proof)
	assert.Error(t, err, "fewer records than the checkpoint")
}

// The holder's shelf gives the half once it holds their agreement and a
// proof that covers it, and nothing before: the copy then reads absent.
func TestHeldCounterpartyHalf(t *testing.T) {
	ctx := context.Background()
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, nil)
	_, _, holder := gateBook(t, h, true)
	ext, err := HeldCounterpartyHalf(ctx, h.buyer.shelf, holder)
	require.NoError(t, err)
	assert.Nil(t, ext, "no proof held yet")

	h.buyer.receive(h.agreementPair, &h.proof)
	ext, err = HeldCounterpartyHalf(ctx, h.buyer.shelf, holder)
	require.NoError(t, err)
	require.NotNil(t, ext)
	read, err := VerifyCounterpartyHalf(holder, ext)
	require.NoError(t, err)
	assert.Equal(t, h.agreementID, read.RecordID)

	// A proof over a record the holder does not hold (never received, or
	// forgotten under retention): no half from it, never a failure.
	g := newHandshake(t, nil)
	sealSecond(t, g)
	wide := g.seller.stamp(g.agreementID)
	g.buyer.receive(g.agreementPair, &wide)
	_, _, gHolder := gateBook(t, g, true)
	ext, err = HeldCounterpartyHalf(ctx, g.buyer.shelf, gHolder)
	require.NoError(t, err)
	assert.Nil(t, ext, "the proof reaches a record not held")

	// Their agreement of the same role as the holder's: the shelf does not
	// hold it (evidencebook#884), so no half, and the holder's report is not
	// blocked by it.
	sr := newHandshake(t, func(a *AgreeBody) { a.Role = RoleBuyer })
	_, err = sr.buyer.receiveRaw(sr.agreementPair, &sr.proof)
	assert.ErrorContains(t, err, "may not push this record")
	_, _, srHolder := gateBook(t, sr, true)
	ext, err = HeldCounterpartyHalf(ctx, sr.buyer.shelf, srHolder)
	require.NoError(t, err)
	assert.Nil(t, ext, "their agreement is not of the other role")

	// A deal whose counterparty keeps no book has no half.
	n := newHandshake(t, nil)
	_, _, noLayer := gateBook(t, n, false)
	ext, err = HeldCounterpartyHalf(ctx, n.buyer.shelf, noLayer)
	require.NoError(t, err)
	assert.Nil(t, ext)
}

// The proof a half carries is the largest that covers their agreement and
// reaches no record past the counted prefix: a larger one past a fork does
// not hide a smaller one that fits, and a proof of another record, key or
// shelf log, or one covering nothing yet, is never taken.
func TestCoveringProof(t *testing.T) {
	ag := AgreeBody{CheckpointKey: "ck", ShelfLogID: "shelf"}
	proof := func(book, key, shelf string, entries, covers uint64) evidencebook.HeldProof {
		var hp evidencebook.HeldProof
		hp.Verified.Fact.BookID, hp.Verified.KeyID, hp.Verified.ShelfCheckpoint.LogID = book, key, shelf
		hp.Verified.BookCheckpoint.Entries, hp.Covers = entries, covers
		return hp
	}
	proofs := []evidencebook.HeldProof{
		proof("ag", "ck", "shelf", 2, 2),
		proof("ag", "ck", "shelf", 5, 3), // past a fork at 4: counted is 3
		proof("ag", "ck", "shelf", 3, 3),
		proof("other", "ck", "shelf", 3, 3),
		proof("ag", "other", "shelf", 3, 3),
		proof("ag", "ck", "other", 3, 3),
		proof("ag", "ck", "shelf", 3, 0),
	}
	got := coveringProof(proofs, "ag", ag, 3)
	require.NotNil(t, got)
	assert.Same(t, &proofs[2], got)
	assert.Nil(t, coveringProof(proofs[3:], "ag", ag, 3))
	assert.Nil(t, coveringProof(proofs[:1], "ag", ag, 1), "the only proof reaches past the counted records")
}
