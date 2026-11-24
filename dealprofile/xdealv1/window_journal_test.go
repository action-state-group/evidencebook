// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On a shelf whose windows are its journal (evidencebook#797), the holder's
// offer sits in an earlier window than its agreement: the status fact still
// names the deal's profile, and the deal's report still reads its offer.
func TestAWindowedJournalReadsTheOfferFromAnEarlierWindow(t *testing.T) {
	ctx := context.Background()
	partyWindow = &evidencebook.WindowPolicy{Period: evidencebook.WindowDay, Base: evidencebook.JournalLogID, Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Journal: true}
	t.Cleanup(func() { partyWindow = nil })
	d := &paymentDeal{buyer: newParty(t, "buyer"), seller: newParty(t, "seller"),
		buyerLog: "deal/" + hex.EncodeToString(seed("buyer log")[:16]), sellerLog: "deal/" + hex.EncodeToString(seed("seller log")[:16])}
	ob := fxOffer()
	ob.To = Some(d.seller.key())
	ob.Witnesses = []Witness{}
	d.offer = Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: ob}
	leg, err := TermsLeg(d.offer)
	require.NoError(t, err)
	d.offerPair = d.buyer.sendOfferWith(d.offer, map[string]json.RawMessage{SettlementMember: leg})
	sent, err := VerifySent(d.offerPair, nil)
	require.NoError(t, err)
	d.offerDigest, d.capsuleID = sent.Digest, sent.CapsuleID

	od := RefTo(d.offerDigest)
	sa := fxAgreement(&od, ref('e'), nil, SideAcceptor, RoleSeller, d.buyer.key())
	sa.CheckpointKey, sa.ShelfLogID = d.seller.checkpointKey(), testCadenceLog
	sb := journalBlock(TypeAgree, "2026-11-01T09:45:00Z", true)
	sb.Seq, sb.Seen = 1, NullOf[string]()
	d.sellerAg = Record{Block: sb, Body: sa}
	d.sellerAgID, d.sellerAgPair = d.seller.seal(d.sellerLog, d.sellerAg)

	// The buyer agrees the next day: its offer is in the 2026-11-01 window.
	d.buyer.now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	ba := fxAgreement(&od, ref('e'), ref('f'), SideOfferer, RoleBuyer, d.seller.key())
	ba.CheckpointKey, ba.ShelfLogID = d.buyer.checkpointKey(), testCadenceLog
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(d.sellerAgID)
	d.buyerAg = Record{Block: bb, Body: ba}
	d.buyerAgID, d.buyerAgPair = d.buyer.seal(d.buyerLog, d.buyerAg)
	d.buyerLast, d.sellerLast, d.buyerSeq, d.sellerSeq = d.buyerAg, d.sellerAg, 1, 1
	// The host's next journal write rolls the windows; here the host rolls.
	_, err = d.buyer.shelf.Roll(ctx)
	require.NoError(t, err)

	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, &SettleBody{Leg: LegPayee, Statements: statements()}, d.payeeMember("settled", "6000"))
	d.buyer.recUseStatus(map[string][]byte{d.offerDigest: d.offerPair})
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	d.buyer.receive(payee, nil)
	_, err = d.buyer.recStampTick()
	require.NoError(t, err)

	windows, err := d.buyer.shelf.Windows(ctx)
	require.NoError(t, err)
	require.Len(t, windows, 2)
	assert.True(t, windows[0].Closed, "the offer's window has closed")
	view, err := d.buyer.shelf.ExistingJournalView(ctx)
	require.NoError(t, err)
	first, err := view.Query(ctx, evidencebook.Filter{FromSeq: 1, ToSeq: view.Size()})
	require.NoError(t, err)
	offers := 0
	for _, r := range first {
		if r.Header.BookID == windows[0].LogID && len(r.Header.PayloadCommitments) == 1 {
			offers++
		}
	}
	assert.Equal(t, 1, offers, "the offer is in the earlier window")

	at := d.buyer.now.UTC().Truncate(time.Second).Format(time.RFC3339)
	s, err := d.buyer.status.ReportSnapshot(ctx, d.buyerLog, keepCopy(at))
	require.NoError(t, err)
	assert.Equal(t, CheckPassed, rowByID(t, s.Report, "paid_as_agreed").Check, "the report reads the offer across windows")
	profile, _, _, ok, err := d.buyer.status.BookValues(ctx, evidencebook.StatusInput{LogID: d.buyerLog, Book: mustBook(t, d.buyer, d.buyerLog), Journals: view})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, Profile, profile, "the deal book's fact names its profile")
}

func mustBook(t *testing.T, p *party, logID string) *evidencebook.Book {
	t.Helper()
	b, err := p.shelf.Open(context.Background(), logID)
	require.NoError(t, err)
	return b
}
