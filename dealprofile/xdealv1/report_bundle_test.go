// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bundleDeal is a buyer's shelf holding its payer leg and the seller's payee
// leg, which states nothing, so only the payment check puts it in the
// payment row's relied_on (Steven's ruling on #588), stamped, so a report
// can be made from it.
type bundleDeal struct {
	*paymentDeal
	payee, excluded []byte
	at              string
}

func newBundleDeal(t *testing.T) bundleDeal {
	t.Helper()
	d := newPaymentDeal(t, func(o *ProposeBody) { o.Witnesses = []Witness{} }, true)
	d.leg(t, true, payerBody(), d.payerMember("settled", "6000"))
	payee := d.leg(t, false, &SettleBody{Leg: LegPayee, Statements: statements()}, d.payeeMember("settled", "6000"))
	// A payee leg whose member names the payer's role: the check excludes
	// it, so no row relies on it.
	excluded := d.leg(t, false, &SettleBody{Leg: LegPayee, Statements: statements()}, d.payerMember("settled", "6000"))
	d.buyer.recUseStatus(map[string][]byte{d.offerDigest: d.offerPair})
	proof := d.seller.stamp(d.sellerAgID)
	d.buyer.receive(d.sellerAgPair, &proof)
	d.buyer.receive(payee, nil)
	d.buyer.receive(excluded, nil)
	_, err := d.buyer.recStampTick()
	require.NoError(t, err)
	return bundleDeal{paymentDeal: d, payee: payee, excluded: excluded, at: d.buyer.now.UTC().Truncate(time.Second).Format(time.RFC3339)}
}

// bundle is one copy's report pair, sealed by the buyer, and its extension.
func (d bundleDeal) bundle(t *testing.T, c ReportCopy) (Report, []byte, ReportBundle) {
	t.Helper()
	r, book, err := d.buyer.status.ReportWithBook(context.Background(), d.buyerLog, c)
	require.NoError(t, err)
	payload, err := ReportPayload(r)
	require.NoError(t, err)
	pair := sealReport(t, "buyer", payload, "")
	read, err := VerifyReport(pair, book.Agreement)
	require.NoError(t, err)
	return r, pair, ReportBundle{SealedReport: read.CapsuleID, Book: book}
}

func extJSON(t *testing.T, x any) []byte {
	t.Helper()
	raw, err := json.Marshal(x)
	require.NoError(t, err)
	return raw
}

// From a shelf, every copy's extension carries the agreement and the same
// relied_on records, the payee leg the check read among them, and each
// verifies with its report.
func TestReportWithBook(t *testing.T) {
	d := newBundleDeal(t)
	payee, err := VerifyBook(d.payee)
	require.NoError(t, err)

	keep, keepPair, keepX := d.bundle(t, keepCopy(d.at))
	cc := keepCopy(d.at)
	cc.Audience = dealprofile.AudienceCounterparty
	_, ccPair, ccX := d.bundle(t, cc)
	ac := keepCopy(d.at)
	ac.Audience, ac.Referee = dealprofile.AudienceAdjudicator, "Lamp Court"
	adj, adjPair, adjX := d.bundle(t, ac)

	require.Equal(t, RowsListed, keep.RowsState)
	row := rowByID(t, keep, "paid_as_agreed")
	assert.Equal(t, CheckPassed, row.Check)
	assert.Contains(t, row.ReliedOn, ReportSource{Record: payee.RecordID, Side: RoleSeller, Time: row.ReliedOn[len(row.ReliedOn)-1].Time,
		Witnessed: row.ReliedOn[len(row.ReliedOn)-1].Witnessed}, "the payee leg, through the check")
	assert.Equal(t, payee.RecordID, row.ReliedOn[len(row.ReliedOn)-1].Record)
	assert.Contains(t, keepX.Book.ReliedOn, payee.RecordID)
	excluded, err := VerifyBook(d.excluded)
	require.NoError(t, err)
	assert.NotContains(t, keepX.Book.ReliedOn, excluded.RecordID, "a leg the check excluded is relied on by no row")
	assert.JSONEq(t, string(d.buyerAgPair), string(keepX.Book.Agreement))

	assert.Equal(t, RowsWithheld, adj.RowsState)
	assert.Empty(t, adj.Rows)
	keys := func(x ReportBundle) []string { return slices.Sorted(maps.Keys(x.Book.ReliedOn)) }
	assert.Equal(t, keys(keepX), keys(ccX), "the counterparty's copy carries the keep copy's records")
	assert.Equal(t, keys(keepX), keys(adjX), "so does the adjudicator's, whose rows are withheld")
	assert.Equal(t, string(d.offerPair), string(keepX.Book.Offer), "the keep copy carries the offer's sent pair as held (u561)")
	assert.Equal(t, string(d.offerPair), string(ccX.Book.Offer), "so does the counterparty's copy (u499, u561)")
	assert.Nil(t, adjX.Book.Offer, "an adjudicator's copy carries no offer")

	for name, c := range map[string]struct {
		pair []byte
		x    ReportBundle
	}{"keep": {keepPair, keepX}, "counterparty": {ccPair, ccX}, "adjudicator": {adjPair, adjX}} {
		read, err := VerifyReportBundle(c.pair, extJSON(t, c.x))
		require.NoError(t, err, name)
		assert.Equal(t, d.buyer.key(), read.KeyID, name)
	}
}

const reportBundleVectors = "../../testdata/deal-v1/report-bundle/vectors.json"

// bundleCase is one VerifyReportBundle vector: a report pair (hex) and the
// x-deal-v1 extension carrying it, and what VerifyReportBundle gives (the
// key, or the stage and a fragment of its error).
type bundleCase struct {
	Name      string          `json:"name"`
	Report    string          `json:"report"`
	Extension json.RawMessage `json:"extension"`
	Want      struct {
		KeyID string `json:"key_id,omitempty"`
		Stage string `json:"stage,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"want"`
}

// The VerifyReportBundle vectors: each copy's bundle from a shelf, which
// verifies, and extensions that do not match their report or carry a record
// the deal's parties did not sign where the report says, which are refused.
func TestReportBundleVectors(t *testing.T) {
	d := newBundleDeal(t)
	keep, pair, x := d.bundle(t, keepCopy(d.at))
	payee, err := VerifyBook(d.payee)
	require.NoError(t, err)
	var cases []bundleCase
	for _, audience := range []string{dealprofile.AudienceKeep, dealprofile.AudienceCounterparty, dealprofile.AudienceAdjudicator} {
		c := keepCopy(d.at)
		c.Audience = audience
		if audience == dealprofile.AudienceAdjudicator {
			c.Referee = "Lamp Court"
		}
		_, p, bx := d.bundle(t, c)
		bc := bundleCase{Name: audience + "-copy", Report: hex.EncodeToString(p), Extension: extJSON(t, bx)}
		read, err := VerifyReportBundle(p, bc.Extension)
		require.NoError(t, err, audience)
		bc.Want.KeyID = read.KeyID
		cases = append(cases, bc)
	}
	clone := func() ReportBundle {
		c := x
		c.Book.ReliedOn = maps.Clone(x.Book.ReliedOn)
		return c
	}
	refuse := func(name string, ext []byte, report []byte, want string) {
		t.Helper()
		if report == nil {
			report = pair
		}
		_, err := VerifyReportBundle(report, ext)
		require.ErrorContains(t, err, want, name)
		var se *StageError
		require.ErrorAs(t, err, &se, name)
		bc := bundleCase{Name: name, Report: hex.EncodeToString(report), Extension: ext}
		bc.Want.Stage, bc.Want.Error = se.Stage, want
		cases = append(cases, bc)
	}
	// resealed is x naming report as its sealed report, for a report
	// changed from keep and sealed again.
	resealed := func(r Report) ([]byte, []byte) {
		t.Helper()
		payload, err := ReportPayload(r)
		require.NoError(t, err)
		p := sealReport(t, "buyer", payload, "")
		read, err := VerifyReport(p, x.Book.Agreement)
		require.NoError(t, err)
		c := x
		c.SealedReport = read.CapsuleID
		return p, extJSON(t, c)
	}

	c := clone()
	c.SealedReport = "ffff"
	refuse("wrong sealed_report", extJSON(t, c), nil, "is not the report Capsule's id")

	c = clone()
	delete(c.Book.ReliedOn, payee.RecordID)
	refuse("a relied-on record missing", extJSON(t, c), nil, "which relied_on does not carry")

	c = clone()
	c.Book.ReliedOn[d.sellerAgID] = d.sellerAgPair
	refuse("a record no row relies on", extJSON(t, c), nil, "on which no row relies")

	c = clone()
	c.Book.ReliedOn["0000"] = d.payee
	delete(c.Book.ReliedOn, payee.RecordID)
	refuse("a key that is not its pair's id", extJSON(t, c), nil, "holds the record")

	c = clone()
	c.Book.Agreement = d.sellerAgPair
	refuse("the counterparty's agreement as the first record", extJSON(t, c), nil, "first record")

	// A record of the deal signed by a key that is no party's.
	stranger := newParty(t, "stranger")
	_, strangerPair := stranger.seal(d.sellerLog, d.sellerAg)
	strangerRead, err := VerifyBook(strangerPair)
	require.NoError(t, err)
	c = clone()
	c.Book.ReliedOn[strangerRead.RecordID] = strangerPair
	refuse("signed by no party", extJSON(t, c), nil, "signed by no party of the agreement")

	// A record the seller signed in another deal.
	otherDeal := d.sellerAg
	otherDeal.Block.DealID = "deal-0123456789abcdef"
	otherID, otherPair := d.seller.seal("deal/"+strings.Repeat("7", 32), otherDeal)
	c = clone()
	c.Book.ReliedOn[otherID] = otherPair
	refuse("a record of another deal", extJSON(t, c), nil, "not the report's")

	// The holder's own record outside the book the report rests on: the
	// report's rests_on.own claims a shorter book than the record's seq.
	short := keep
	short.RestsOn.Own.Size = 1
	rp, rx := resealed(short)
	refuse("the holder's record past rests_on.own.size", rx, rp, "not in the holder's book")

	// The counterparty's record past the prefix the report counted.
	short = keep
	cp := *keep.RestsOn.Counterparty
	cp.Counted = 1
	short.RestsOn.Counterparty = &cp
	rp, rx = resealed(short)
	refuse("the counterparty's record past counted", rx, rp, "not in the counterparty's prefix")

	// A row naming a record as the other side's.
	wrongSide := keep
	wrongSide.Rows = slices.Clone(keep.Rows)
	for i := range wrongSide.Rows {
		if wrongSide.Rows[i].ID == "paid_as_agreed" {
			srcs := slices.Clone(wrongSide.Rows[i].ReliedOn)
			srcs[len(srcs)-1].Side = RoleBuyer
			wrongSide.Rows[i].ReliedOn = srcs
		}
	}
	rp, rx = resealed(wrongSide)
	refuse("a row naming the wrong side", rx, rp, "and it is signed by the seller")

	// An adjudicator's copy that lists rows.
	listedAdj := keep
	listedAdj.Audience, listedAdj.Referee = dealprofile.AudienceAdjudicator, "Lamp Court"
	rp, rx = resealed(listedAdj)
	refuse("an adjudicator's copy listing rows", rx, rp, "only it, is withheld")

	// A keep copy that is withheld.
	withheld := keep
	withheld.RowsState, withheld.Rows = RowsWithheld, []ReportRow{}
	rp, rx = resealed(withheld)
	refuse("a keep copy withheld", rx, rp, "only it, is withheld")

	// The extension's form.
	refuse("an unknown member", []byte(`{"sealed_report":"`+x.SealedReport+`","book":{"agreement":`+string(x.Book.Agreement)+`,"relied_on":{}},"extra":1}`), nil, "unknown field")
	refuse("a null relied_on", []byte(`{"sealed_report":"`+x.SealedReport+`","book":{"agreement":`+string(x.Book.Agreement)+`,"relied_on":null}}`), nil, "a relied_on object")
	refuse("no book", []byte(`{"sealed_report":"`+x.SealedReport+`"}`), nil, "a relied_on object")

	// The offer the copy carries (book.offer): the sent pair of the offer the
	// agreement cites, signed by the offerer, never on an adjudicator's copy.
	require.NotNil(t, x.Book.Offer, "the keep copy carries the offer")
	c = clone()
	c.Book.Offer = nil
	_, err = VerifyReportBundle(pair, extJSON(t, c))
	require.NoError(t, err, "a copy whose holder holds no offer pair carries none, and verifies")
	otherOffer := d.offer
	otherBody := *d.offer.Body.(*ProposeBody)
	otherBody.AgreeBy = "2026-11-02T09:30:00Z"
	otherOffer.Body = &otherBody
	c = clone()
	c.Book.Offer = d.buyer.sendOffer(otherOffer)
	refuse("an offer the agreement does not cite", extJSON(t, c), nil, "is not the offer")
	c = clone()
	c.Book.Offer = d.seller.sendOffer(d.offer)
	refuse("the offer signed by the acceptor", extJSON(t, c), nil, "not signed by the deal's offerer")
	c = clone()
	c.Book.Offer = d.seller.sendOfferWith(Record{Block: journalBlock(TypeAgree, "2026-11-01T09:40:00Z", true),
		Body: &AgreeBody{Kind: KindDecline, Offer: RefTo(d.offerDigest), Reason: ReasonOther}}, nil)
	refuse("a decline as the offer", extJSON(t, c), nil, "not a propose")
	var tampered map[string]any
	require.NoError(t, json.Unmarshal(d.offerPair, &tampered))
	tampered["agent_input"].(map[string]any)["body"].(map[string]any)["agree_by"] = "2026-11-03T09:30:00Z"
	c = clone()
	c.Book.Offer = extJSON(t, tampered)
	refuse("an offer whose record is not the one its Capsule sealed", extJSON(t, c), nil, "the offer:")
	tampered["agent_input"].(map[string]any)["body"].(map[string]any)["agree_by"] = 1.5
	c = clone()
	c.Book.Offer = extJSON(t, tampered)
	refuse("an offer whose record holds a number JCS does not take", extJSON(t, c), nil, "the offer:")
	ac := keepCopy(d.at)
	ac.Audience, ac.Referee = dealprofile.AudienceAdjudicator, "Lamp Court"
	_, adjPair, adjOffer := d.bundle(t, ac)
	adjOffer.Book.Offer = d.offerPair
	refuse("an adjudicator's copy carrying the offer", extJSON(t, adjOffer), adjPair, "an adjudicator's copy carries no offer")

	// Copies with a case book record a row relies on, and copies of a deal
	// not started, which carry their computed rows' records.
	valid := func(name string, p *party, signer, logID string, c ReportCopy) (Report, []byte, ReportBundle) {
		t.Helper()
		r, rp, rx := holderBundle(t, p, signer, logID, c)
		bc := bundleCase{Name: name, Report: hex.EncodeToString(rp), Extension: extJSON(t, rx)}
		read, err := VerifyReportBundle(rp, bc.Extension)
		require.NoError(t, err, name)
		bc.Want.KeyID = read.KeyID
		cases = append(cases, bc)
		return r, rp, rx
	}
	h, buyerLog, _ := rulingHolder(t)
	hat := h.buyer.now.UTC().Truncate(time.Second).Format(time.RFC3339)
	valid("ruling-keep-copy", h.buyer, "buyer", buyerLog, copyAt(hat, dealprofile.AudienceKeep))
	valid("ruling-adjudicator-copy", h.buyer, "buyer", buyerLog, copyAt(hat, dealprofile.AudienceAdjudicator))
	ns, _ := notStartedHolder(t)
	nat := ns.seller.now.UTC().Truncate(time.Second).Format(time.RFC3339)
	nsKeep, _, nsX := valid("not-started-keep-copy", ns.seller, "seller", ns.sellerLog, copyAt(nat, dealprofile.AudienceKeep))
	assert.Equal(t, string(ns.offerPair), string(nsX.Book.Offer), "the acceptor's copy carries the offer it received, signed by the offerer")
	valid("not-started-adjudicator-copy", ns.seller, "seller", ns.sellerLog, copyAt(nat, dealprofile.AudienceAdjudicator))

	// The not-started rules, on the not-started keep copy sealed again.
	nsResealed := func(r Report) ([]byte, []byte) {
		t.Helper()
		payload, err := ReportPayload(r)
		require.NoError(t, err)
		p := sealReport(t, "seller", payload, "")
		read, err := VerifyReport(p, nsX.Book.Agreement)
		require.NoError(t, err)
		c := nsX
		c.SealedReport = read.CapsuleID
		return p, extJSON(t, c)
	}
	listedNotStarted := nsKeep
	listedNotStarted.RowsState = RowsListed
	rp, rx = nsResealed(listedNotStarted)
	refuse("listed while not started", rx, rp, "listed exactly when its deal has started")
	rowsNotStarted := nsKeep
	rowsNotStarted.Rows = keep.Rows
	rp, rx = nsResealed(rowsNotStarted)
	refuse("a not_started copy listing rows", rx, rp, "copy lists no rows")
	unknownState := nsKeep
	unknownState.RowsState = "partial"
	rp, rx = nsResealed(unknownState)
	refuse("an unknown rows_state", rx, rp, "is not one of listed, not_started, withheld")

	// An adjudicator's copy naming no referee.
	noReferee := keep
	noReferee.Audience, noReferee.RowsState, noReferee.Rows = dealprofile.AudienceAdjudicator, RowsWithheld, []ReportRow{}
	rp, rx = resealed(noReferee)
	refuse("an adjudicator's copy naming no referee", rx, rp, "names a referee")

	// A keep copy that counted none of the counterparty's book carries a
	// counterparty record.
	noPrefix := keep
	noPrefix.RestsOn.Counterparty = nil
	rp, rx = resealed(noPrefix)
	refuse("a counterparty record with no counted prefix", rx, rp, "counted none of its book")

	// An adjudicator's copy whose counterparty records sit in two logs.
	adjCopy := keep
	adjCopy.Audience, adjCopy.Referee, adjCopy.RowsState, adjCopy.Rows = dealprofile.AudienceAdjudicator, "Lamp Court", RowsWithheld, []ReportRow{}
	adjCopy.RestsOn.Counterparty, adjCopy.RestsOn.Witnesses, adjCopy.Disputes = nil, nil, nil
	twoLogs := clone()
	twoLogs.Book.Offer = nil
	elsewhere := d.sellerAg
	elsewhereID, elsewherePair := d.seller.seal("deal/"+strings.Repeat("8", 32), elsewhere)
	twoLogs.Book.ReliedOn[elsewhereID] = elsewherePair
	payload, err := ReportPayload(adjCopy)
	require.NoError(t, err)
	ap := sealReport(t, "buyer", payload, "")
	aread, err := VerifyReport(ap, x.Book.Agreement)
	require.NoError(t, err)
	twoLogs.SealedReport = aread.CapsuleID
	refuse("an adjudicator's copy with counterparty records in two logs", extJSON(t, twoLogs), ap, "in another counterparty book")

	writeOrCompare(t, reportBundleVectors, map[string]any{
		"description": "x-deal-v1 bundle extension (evidencebook#588, docs/design/461-deal-profile.md §9, PROFILE §10): VerifyReportBundle(report, extension) gives want: the report's signing key, or a refusal at stage with error in its text. report is the report pair's hex; extension is the x-deal-v1 extension as a bundle carries it. Written by dealprofile/xdealv1's TestReportBundleVectors (EB_WRITE_DEAL_V1_VECTORS=1).",
		"cases":       cases,
	})
}

func TestReportBundleVectorsReplay(t *testing.T) {
	raw, err := os.ReadFile(reportBundleVectors)
	require.NoError(t, err)
	var f struct {
		Cases []bundleCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Cases)
	for _, c := range f.Cases {
		report, err := hex.DecodeString(c.Report)
		require.NoError(t, err, c.Name)
		read, err := VerifyReportBundle(report, c.Extension)
		if c.Want.KeyID != "" {
			require.NoError(t, err, c.Name)
			assert.Equal(t, c.Want.KeyID, read.KeyID, c.Name)
			continue
		}
		var se *StageError
		require.ErrorAs(t, err, &se, c.Name)
		assert.Equal(t, c.Want.Stage, se.Stage, c.Name)
		assert.ErrorContains(t, err, c.Want.Error, c.Name)
	}
}

// rulingHolder is the buyer of a handshake whose agreements name an
// adjudicator: it holds its agreement, the seller's, and the adjudicator's
// final ruling against the seller, which the receive call names; stamped.
// Its report is started, and the as_described row relies on the ruling, a
// case book record.
func rulingHolder(t *testing.T) (*handshake, string, BookRead) {
	t.Helper()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	adj := fxAdjudicator
	adj.KeyID = adjudicator.key()
	ab.Adjudicator = Some(adj)
	bb := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	bb.Seq, bb.Seen = 1, Some(h.agreementID)
	buyerLog := "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"
	h.buyer.seal(buyerLog, Record{Block: bb, Body: ab})
	h.buyer.recUseStatus(map[string][]byte{od: h.offerPair})
	h.buyer.receive(h.agreementPair, &h.proof)
	_, pair := adjudicator.seal(caseLog, ruling)
	h.buyer.receive(pair, nil)
	_, err = h.buyer.recStampTick()
	require.NoError(t, err)
	read, err := VerifyBook(pair)
	require.NoError(t, err)
	return h, buyerLog, read
}

// notStartedHolder is the seller of a handshake holding its own agreement
// and the adjudicator's ruling against it, which the receive call names,
// and not the buyer's agreement: its deal has not started, so its copies
// list no rows, and their book still carries the ruling a row relies on.
func notStartedHolder(t *testing.T) (*handshake, BookRead) {
	t.Helper()
	h, adjudicator, ruling, caseLog := rulingCase(t)
	od, err := Digest(h.offer)
	require.NoError(t, err)
	h.seller.recUseStatus(map[string][]byte{od: h.offerPair})
	_, pair := adjudicator.seal(caseLog, ruling)
	h.seller.receive(pair, nil)
	_, err = h.seller.recStampTick()
	require.NoError(t, err)
	read, err := VerifyBook(pair)
	require.NoError(t, err)
	return h, read
}

// holderBundle is one copy of p's report of logID, sealed under name's
// record key, and its extension.
func holderBundle(t *testing.T, p *party, name, logID string, c ReportCopy) (Report, []byte, ReportBundle) {
	t.Helper()
	r, book, err := p.status.ReportWithBook(context.Background(), logID, c)
	require.NoError(t, err)
	payload, err := ReportPayload(r)
	require.NoError(t, err)
	pair := sealReport(t, name, payload, "")
	read, err := VerifyReport(pair, book.Agreement)
	require.NoError(t, err)
	return r, pair, ReportBundle{SealedReport: read.CapsuleID, Book: book}
}

func copyAt(at, audience string) ReportCopy {
	c := keepCopy(at)
	c.Audience = audience
	if audience == dealprofile.AudienceAdjudicator {
		c.Referee = "Lamp Court"
	}
	return c
}

// A case book record a row relies on travels in every copy's book, signed
// by the agreement's adjudicator; and a deal not started carries the
// records its computed rows rely on though its copies list none.
func TestReportWithBookRulingAndNotStarted(t *testing.T) {
	h, buyerLog, ruling := rulingHolder(t)
	at := h.buyer.now.UTC().Truncate(time.Second).Format(time.RFC3339)
	for _, audience := range []string{dealprofile.AudienceKeep, dealprofile.AudienceCounterparty, dealprofile.AudienceAdjudicator} {
		r, pair, x := holderBundle(t, h.buyer, "buyer", buyerLog, copyAt(at, audience))
		assert.Contains(t, x.Book.ReliedOn, ruling.RecordID, audience)
		if audience != dealprofile.AudienceAdjudicator {
			require.Equal(t, RowsListed, r.RowsState, audience)
			var sides []string
			for _, src := range rowByID(t, r, "as_described").ReliedOn {
				sides = append(sides, src.Record+"/"+src.Side)
			}
			assert.Contains(t, sides, ruling.RecordID+"/"+SideAdjudicator, audience)
		}
		_, err := VerifyReportBundle(pair, extJSON(t, x))
		require.NoError(t, err, audience)
	}

	s, sRuling := notStartedHolder(t)
	at = s.seller.now.UTC().Truncate(time.Second).Format(time.RFC3339)
	for _, audience := range []string{dealprofile.AudienceKeep, dealprofile.AudienceAdjudicator} {
		r, pair, x := holderBundle(t, s.seller, "seller", s.sellerLog, copyAt(at, audience))
		assert.Empty(t, r.Rows, audience)
		if audience == dealprofile.AudienceKeep {
			assert.Equal(t, RowsNotStarted, r.RowsState)
		}
		assert.Contains(t, x.Book.ReliedOn, sRuling.RecordID, audience)
		_, err := VerifyReportBundle(pair, extJSON(t, x))
		require.NoError(t, err, audience)
	}
}
