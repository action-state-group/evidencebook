// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genesisFork is a handshake whose acceptor (the seller) then forks its book
// at its first record: the buyer (offerer) seals its agreement with seen set
// to seen (nil: the seller's first agreement), receives the seller's
// agreement and a second, different one, and stamps. It returns the
// handshake and the second agreement's record id.
func genesisFork(t *testing.T, seen func(h *handshake) Null[string]) (*handshake, string) {
	t.Helper()
	h := newHandshake(t, nil)
	od, err := Digest(h.offer)
	require.NoError(t, err)
	h.buyer.recUseStatus(map[string][]byte{od: h.offerPair})
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(h.agreementID)
	if seen != nil {
		bb.Seen = seen(h)
	}
	h.buyer.seal(caseBuyerLog, Record{Block: bb, Body: ab})
	h.buyer.receive(h.agreementPair, &h.proof)
	other := h.agreement
	ob := journalBlock(TypeAgree, "2026-11-01T09:50:00Z", true)
	ob.Seq, ob.Seen = 1, NullOf[string]()
	other.Block = ob
	forkID, forkPair := h.seller.sealOnNewShelf(h.sellerLog, other)
	require.NotEqual(t, h.agreementID, forkID)
	h.buyer.receive(forkPair, nil)
	_, err = h.buyer.recStampTick()
	require.NoError(t, err)
	return h, forkID
}

// An offerer whose acceptor forked its book at its first record can still
// report, for every audience (evidencebook#878). The branch its own
// agreement names is read, so the deal both sides signed starts, and the
// fork stays shown (Sherlock2's option 2).
func TestReportAfterAcceptorGenesisFork(t *testing.T) {
	h, _ := genesisFork(t, nil)
	at := h.buyer.now.Format(time.RFC3339)
	for _, audience := range []string{dealprofile.AudienceKeep, dealprofile.AudienceCounterparty, dealprofile.AudienceAdjudicator} {
		r, pair, x := holderBundle(t, h.buyer, "buyer", caseBuyerLog, copyAt(at, audience))
		_, err := VerifyReportBundle(pair, extJSON(t, x))
		require.NoError(t, err, audience)
		if audience == dealprofile.AudienceAdjudicator {
			// The full adjudicator copy carries both first records and verifies.
			c := copyAt(at, audience)
			c.Full = true
			_, fpair, fx := holderBundle(t, h.buyer, "buyer", caseBuyerLog, c)
			_, full, err := VerifyFullCopy(fpair, extJSON(t, fx))
			require.NoError(t, err)
			assert.Len(t, full.Received, 2)
			continue // an adjudicator's copy names no counterparty prefix
		}
		require.NotNil(t, r.RestsOn.Counterparty, audience)
		assert.Equal(t, uint64(1), r.RestsOn.Counterparty.ForkAt, "the fork is shown")
		assert.Equal(t, uint64(1), r.RestsOn.Counterparty.Counted, "the named branch is read")
		assert.Equal(t, h.agreementID, r.RestsOn.Counterparty.Last)
		assert.Equal(t, StateStarted, r.Start.State, "the deal both sides signed starts")
	}
}

// Naming neither branch, nothing of the forked book is read: the deal reads
// as awaiting before agree_by (u217: option 1 when the offerer names neither),
// and the fork is still shown, with nothing counted.
func TestReportAfterAcceptorGenesisForkNamingNeither(t *testing.T) {
	h, _ := genesisFork(t, func(*handshake) Null[string] { return Some(strings.Repeat("ab", 32)) })
	r, pair, x := holderBundle(t, h.buyer, "buyer", caseBuyerLog, keepCopy(h.buyer.now.Format(time.RFC3339)))
	_, err := VerifyReportBundle(pair, extJSON(t, x))
	require.NoError(t, err)
	require.NotNil(t, r.RestsOn.Counterparty)
	assert.Equal(t, uint64(1), r.RestsOn.Counterparty.ForkAt)
	assert.Zero(t, r.RestsOn.Counterparty.Counted)
	assert.Equal(t, StateAwaitingAgreement, r.Start.State)
}

// The roles swapped (round 1 of #892): the offerer (the buyer) forks its book
// at its first record after the acceptor's (the seller's) delivery named the
// buyer's agreement A. The seller reads the branch its own book names, so
// its deal is started from A, the fork shown, as the adjudicator reads it.
func TestReportAfterOffererGenesisFork(t *testing.T) {
	f := newFourBooks(t)
	book := mustOpen(t, f.h.buyer, f.buyerLog)
	other := readBack(t, book, 1).Record
	other.Block.At = "2026-11-01T10:05:00Z"
	forkID, forkPair := f.h.buyer.sealOnNewShelf(f.buyerLog, other)
	require.NotEqual(t, f.b1, forkID)
	f.h.seller.receive(forkPair, nil)
	witnessedStamp(t, f.h.seller, gatherAt("13:00"))

	r, pair, x := holderBundle(t, f.h.seller, "seller", f.h.sellerLog, keepCopy(gatherAsOf))
	_, err := VerifyReportBundle(pair, extJSON(t, x))
	require.NoError(t, err)
	require.NotNil(t, r.RestsOn.Counterparty)
	assert.Equal(t, uint64(1), r.RestsOn.Counterparty.ForkAt, "the fork is shown")
	assert.Equal(t, f.b1, r.RestsOn.Counterparty.Last, "the branch the seller's delivery named")
	assert.Equal(t, StateStarted, r.Start.State)
}
