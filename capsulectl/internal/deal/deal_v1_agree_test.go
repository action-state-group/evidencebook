package deal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1Side is one side of a deal in a test: its profile's config and the offer
// it holds.
type v1Side struct {
	party dealParty
}

func newV1Side(t *testing.T) v1Side {
	t.Helper()
	dealFixture(t)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	return v1Side{party: thisDealParty(t)}
}

func (v v1Side) act(t *testing.T) { party(t, v.party.config) }

// sealAdmit seals an admission decision in the profile's journal and returns
// its record digest, as an agreement cites it.
func sealAdmit(t *testing.T, outcome string) string {
	t.Helper()
	// A fresh nonce for every admit: the agreement cites it by digest, and
	// without one the other side could test guesses at the outcome (u528).
	nonce, err := xdealv1.NewNonce()
	require.NoError(t, err)
	rec := xdealv1.Record{
		Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, RecordType: xdealv1.TypeAdmit, At: v1Now()},
		Body: xdealv1.AdmitBody{Outcome: outcome, Tier: "standard", Rows: []string{"paid_as_agreed"}, Asks: []xdealv1.Ref{},
			PackDigest: strings.Repeat("a", 64), CardDigest: strings.Repeat("b", 64), Answers: []xdealv1.Ref{}, Nonce: nonce},
	}
	payload, digest, err := xdealv1.Encode(rec)
	require.NoError(t, err)
	withSession(t, func(s *dealSession) {
		_, _, err := s.shelf.SealSent(t.Context(), evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}}, "admit/sent")
		require.NoError(t, err)
	})
	return digest
}

func readPush(t *testing.T, path string) v1Push {
	t.Helper()
	var p v1Push
	require.NoError(t, json.Unmarshal(mustRead(t, path), &p))
	return p
}

// handshakeV1 runs the offer, the acceptor's agreement with its stamp proof
// and the offerer's agreement, between two profiles.
type handshakeV1 struct {
	buyer, seller         v1Side
	dealID, offer         string
	offerFile             string
	sellerAgree, buyerAgr string
}

// offerTo is v1OfferInput addressed to a key: the push rule lets only the key
// an offer was sent to push the answer (evidencebook design §3.3, §8.3).
func offerTo(key string) string {
	return strings.Replace(v1OfferInput, `"to": null`, `"to": "`+key+`"`, 1)
}

func newHandshakeV1(t *testing.T) *handshakeV1 {
	t.Helper()
	h := &handshakeV1{}
	h.seller = newV1Side(t)
	h.buyer = newV1Side(t)
	h.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(h.seller.party.signer)), "--out", h.offerFile)
	h.dealID, h.offer = res["deal_id"].(string), res["offer"].(string)

	h.seller.act(t)
	got := dealRun(t, "receive", h.offerFile)
	require.Equal(t, "kept, and counted", got["record"], "%v", got)
	h.sellerAgree = filepath.Join(t.TempDir(), "agreement.json")
	admit := sealAdmit(t, "GO")
	agreed := dealRun(t, "agree", h.offer, "--admit", admit, "--out", h.sellerAgree)
	require.Equal(t, true, agreed["stamp_proof"], "the acceptor's file carries its stamp proof: %v", agreed)
	return h
}

func TestTheAcceptorAgreesAndTheOffererReceivesItsProof(t *testing.T) {
	h := newHandshakeV1(t)
	push := readPush(t, h.sellerAgree)
	read, err := xdealv1.VerifyBook(push.Record)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), read.Seq)
	assert.True(t, strings.HasPrefix(read.LogID, "deal/"))
	assert.NotContains(t, read.LogID, strings.TrimPrefix(h.dealID, "deal-"), "the book's log id is random, never the deal id")
	ag, ok := agreeOf(read.Record)
	require.True(t, ok)
	assert.Equal(t, xdealv1.KindAgreement, ag.Kind)
	assert.Equal(t, xdealv1.SideAcceptor, ag.Side)
	assert.Equal(t, xdealv1.RoleSeller, ag.Role, "the opposite of the offer's buyer")
	assert.Equal(t, h.offer, ag.Offer.Digest)
	require.NotNil(t, push.StampProof)

	h.buyer.act(t)
	got := dealRun(t, "receive", h.sellerAgree)
	assert.Equal(t, "kept, and counted", got["record"], "%v", got)
	assert.Equal(t, "kept", got["stamp_proof"], "%v", got)
	again := dealRun(t, "receive", h.sellerAgree)
	assert.Equal(t, "already kept", again["record"])
	assert.Equal(t, "already kept", again["stamp_proof"])

	// The offerer agrees, naming the acceptor's agreement in `seen`.
	buyerAgree := filepath.Join(t.TempDir(), "buyer-agreement.json")
	admit := sealAdmit(t, "GO")
	out := dealRun(t, "agree", h.offer, "--admit", admit, "--out", buyerAgree)
	assert.Equal(t, false, out["stamp_proof"], "the offerer stamps with its first act")
	own, err := xdealv1.VerifyBook(readPush(t, buyerAgree).Record)
	require.NoError(t, err)
	ownAg, _ := agreeOf(own.Record)
	assert.Equal(t, xdealv1.SideOfferer, ownAg.Side)
	assert.Equal(t, xdealv1.RoleBuyer, ownAg.Role)
	require.NotNil(t, own.Record.Block.Seen.V)
	assert.Equal(t, read.RecordID, *own.Record.Block.Seen.V, "the offerer's agreement names the acceptor's")
	assert.NotEqual(t, read.LogID, own.LogID, "the two sides' book log ids differ")

	// Run again: the same agreement is written again, never sealed twice.
	again2 := filepath.Join(t.TempDir(), "buyer-agreement-again.json")
	dealRun(t, "agree", h.offer, "--admit", admit, "--out", again2)
	second, err := xdealv1.VerifyBook(readPush(t, again2).Record)
	require.NoError(t, err)
	assert.Equal(t, own.RecordID, second.RecordID)

	// The acceptor receives it.
	h.seller.act(t)
	got = dealRun(t, "receive", buyerAgree)
	assert.Equal(t, "kept, and counted", got["record"], "%v", got)
}

func TestAnAgreementRefusesWhatItCannotCite(t *testing.T) {
	h := newV1Side(t)
	_ = h
	buyerOffer := filepath.Join(t.TempDir(), "o.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", buyerOffer)
	digest := res["offer"].(string)

	assert.Contains(t, v1Err(t, "agree", digest, "--out", filepath.Join(t.TempDir(), "a.json")), "--admit is required")
	assert.Contains(t, v1Err(t, "agree", strings.Repeat("0", 64), "--admit", strings.Repeat("c", 64)), "holds no offer")
	assert.Contains(t, v1Err(t, "agree", digest, "--admit", strings.Repeat("c", 64)), "not a record in your journal")
	// The offerer agrees only once it holds the acceptor's agreement.
	admit := sealAdmit(t, "GO")
	assert.Contains(t, v1Err(t, "agree", digest, "--admit", admit), "has not been received")
}

func TestReceiveRefusesWhatItCannotRead(t *testing.T) {
	newV1Side(t)
	bad := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(`{"hello":"world"}`), 0o600))
	assert.Contains(t, v1Err(t, "receive", bad), "no record this profile reads")
	assert.Contains(t, v1Err(t, "receive", writeJSON(t, `[1]`)), "not a JSON object")
}

func TestAV0ProfileThatReceivesAV1OfferAnswersInV1(t *testing.T) {
	// A profile reads both versions whatever its setting: its setting only
	// governs the offers it writes (design §3).
	buyer := newV1Side(t)
	buyer.act(t)
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", offerFile)

	dealFixture(t) // a v0 profile, store migrated by the user's choice only
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	dealRun(t, "set-version", "x-deal-v0")
	got := dealRun(t, "receive", offerFile)
	assert.Equal(t, "kept, and counted", got["record"])
	admit := sealAdmit(t, "GO")
	out := dealRun(t, "agree", res["offer"].(string), "--admit", admit, "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Equal(t, "acceptor", out["side"])
}

func performAct(dealID string) xdealv1.Record {
	return xdealv1.Record{
		Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: dealID, RecordType: xdealv1.TypePerform, At: v1Now()},
		Body:  xdealv1.PerformBody{Act: "deliver", Observed: "self", Statements: []xdealv1.StatementItem{}},
	}
}

// A first perform is refused until the other side's stamp proof covers its
// agreement (u610); a proof alone, sent later, opens it (the same file the
// acceptor wrote, with no record); a commitment naming another shelf log
// closes it; a statement is never gated.
func TestTheFirstActWaitsForTheOtherSidesStampProof(t *testing.T) {
	h := newHandshakeV1(t)
	push := readPush(t, h.sellerAgree)
	recordOnly := filepath.Join(t.TempDir(), "record-only.json")
	data, err := json.Marshal(v1Push{Record: push.Record})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(recordOnly, data, 0o600))
	proofOnly := filepath.Join(t.TempDir(), "proof-only.json")
	data, err = json.Marshal(v1Push{StampProof: push.StampProof})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(proofOnly, data, 0o600))

	h.buyer.act(t)
	got := dealRun(t, "receive", recordOnly)
	assert.Equal(t, "kept, and counted", got["record"])
	assert.Nil(t, got["stamp_proof"])
	buyerAgree := filepath.Join(t.TempDir(), "b.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", buyerAgree)

	gate := func(shelfLog string) xdealv1.ActGate {
		var g xdealv1.ActGate
		ctx := context.WithValue(t.Context(), v1ReceiveKey{}, true)
		s, err := openDealSession(ctx, dealProfile(t))
		require.NoError(t, err)
		defer func() { require.NoError(t, s.close()) }()
		g, err = s.gateFirstAct(ctx, h.dealID, performAct(h.dealID), shelfLog)
		require.NoError(t, err)
		return g
	}
	g := gate("")
	assert.True(t, g.Gated)
	assert.False(t, g.Allowed, "no stamp proof yet")
	assert.Equal(t, xdealv1.GateNoProof, g.Reason)

	got = dealRun(t, "receive", proofOnly)
	assert.Equal(t, "kept", got["stamp_proof"])
	assert.Nil(t, got["record"])
	g = gate("")
	assert.True(t, g.Allowed, "the proof covers the acceptor's agreement: %+v", g)

	assert.False(t, gate("deal-cadence/ffffffffffffffff").Allowed, "a commitment naming another shelf log closes it")
	sellerAgreement, err := xdealv1.VerifyBook(push.Record)
	require.NoError(t, err)
	named, _ := agreeOf(sellerAgreement.Record)
	assert.True(t, gate(named.ShelfLogID).Allowed, "a commitment naming the agreement's own shelf log keeps it open")

	// The acceptor's own first act is gated on the offerer's proof too, which
	// the offerer has not sent: refused.
	h.seller.act(t)
	dealRun(t, "receive", buyerAgree)
	ctx := context.WithValue(t.Context(), v1ReceiveKey{}, true)
	s, err := openDealSession(ctx, dealProfile(t))
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	g, err = s.gateFirstAct(ctx, h.dealID, performAct(h.dealID), "")
	require.NoError(t, err)
	assert.False(t, g.Allowed)
	assert.Equal(t, xdealv1.GateNoProof, g.Reason)
}

// The writer chains every later record of the deal book: seq, prev (the record
// digest before it), agreement_ref (the book's agreement), `seen` (the last
// record held from the other side) and one cites link; the pair verifies on
// its own and the other side counts it. A verb that seals a statement, a
// perform, a settle or a dispute adds none of this itself.
func TestTheBookWriterChainsALaterRecord(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	buyerAgree := filepath.Join(t.TempDir(), "b.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", buyerAgree)

	var sealed bookSealed
	var agreementDigest string
	ctx := context.WithValue(t.Context(), v1ReceiveKey{}, true)
	s, err := openDealSession(ctx, dealProfile(t))
	require.NoError(t, err)
	d, err := s.v1DealOf(ctx, h.dealID)
	require.NoError(t, err)
	agreementDigest = d.OwnAgreementDigest
	sealed, err = s.sealBookV1(ctx, h.dealID, bookRecordIn{Body: xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}}})
	require.NoError(t, err)
	require.NoError(t, s.close())

	assert.Equal(t, uint64(2), sealed.Seq)
	read, err := xdealv1.VerifyBook(sealed.Pair)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), read.Seq)
	b := read.Record.Block
	require.NotNil(t, b.Prev)
	assert.Equal(t, agreementDigest, b.Prev.Digest, "prev is the record digest of seq-1")
	require.NotNil(t, b.AgreementRef)
	assert.Equal(t, agreementDigest, b.AgreementRef.Digest)
	require.NotNil(t, b.Seen.V, "seen names the acceptor's agreement the offerer holds")
	assert.NotEmpty(t, read.Prev, "the record cites the one before it")

	// The sealed record reads back from the book, in order, for the gate.
	withSession(t, func(s *dealSession) {
		d, err := s.v1DealOf(t.Context(), h.dealID)
		require.NoError(t, err)
		book, err := s.ownBookV1(t.Context(), d)
		require.NoError(t, err)
		require.Len(t, book, 2)
		assert.Equal(t, xdealv1.TypeAgree, book[0].Block.RecordType)
		assert.Equal(t, xdealv1.TypeStatement, book[1].Block.RecordType)
	})

	// No second record opens the book: only an agreement does, and a record
	// before an agreement is refused without making a book.
	withSession(t, func(s *dealSession) {
		_, err := s.sealBookV1(t.Context(), h.dealID, bookRecordIn{Body: xdealv1.AgreeBody{Kind: xdealv1.KindAgreement}})
		require.Error(t, err, "an agreement that fails the checks is not sealed")
	})
}

func TestAStatementBeforeAnAgreementOpensNoBook(t *testing.T) {
	v := newV1Side(t)
	_ = v
	res := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))
	withSession(t, func(s *dealSession) {
		_, err := s.sealBookV1(t.Context(), res["deal_id"].(string), bookRecordIn{Body: xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "opens at your agreement")
		d, err := s.v1DealOf(t.Context(), res["deal_id"].(string))
		require.NoError(t, err)
		assert.Empty(t, d.OwnLogID, "no log id was made")
	})
}

// An agreement appended by a run that stopped before recording it is found
// again, never sealed twice, and its file is written.
func TestAnAgreementAStoppedRunAppendedIsFoundAgain(t *testing.T) {
	h := newHandshakeV1(t)
	first := readPush(t, h.sellerAgree)
	// Forget what the run recorded after appending.
	_, err := openWritableDB(t).Exec(`UPDATE deal_v1 SET own_agreement_id='', own_agreement_digest='' WHERE deal_id=?`, h.dealID)
	require.NoError(t, err)
	again := filepath.Join(t.TempDir(), "again.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", again)
	a, err := xdealv1.VerifyBook(first.Record)
	require.NoError(t, err)
	b, err := xdealv1.VerifyBook(readPush(t, again).Record)
	require.NoError(t, err)
	assert.Equal(t, a.RecordID, b.RecordID, "the same record, not a second agreement")
	assert.Equal(t, a.LogID, b.LogID, "the same book")
}

// A bare deal-book pair is received as it is (the form `sealBookV1` returns),
// and the offerer's agreement names the acceptor's agreement in `seen` even
// when a later record of the acceptor was received before it agreed.
func TestTheOfferersAgreementNamesTheAcceptorsAgreementNotALaterRecord(t *testing.T) {
	h := newHandshakeV1(t)
	// The acceptor seals a statement after its agreement and sends it bare.
	h.seller.act(t)
	var later bookSealed
	withSession(t, func(s *dealSession) {
		var err error
		later, err = s.sealBookV1(t.Context(), h.dealID, bookRecordIn{Body: xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}}})
		require.NoError(t, err)
	})
	laterFile := filepath.Join(t.TempDir(), "later.json")
	require.NoError(t, os.WriteFile(laterFile, later.Pair, 0o600))

	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	got := dealRun(t, "receive", laterFile)
	assert.Equal(t, "kept, and counted", got["record"], "a bare book pair is received: %v", got)

	out := filepath.Join(t.TempDir(), "b.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", out)
	own, err := xdealv1.VerifyBook(readPush(t, out).Record)
	require.NoError(t, err)
	acceptor, err := xdealv1.VerifyBook(readPush(t, h.sellerAgree).Record)
	require.NoError(t, err)
	require.NotNil(t, own.Record.Block.Seen.V)
	assert.Equal(t, acceptor.RecordID, *own.Record.Block.Seen.V, "the acceptor's agreement, not the statement after it")
	assert.NotEqual(t, later.RecordID, *own.Record.Block.Seen.V)
}

// --admit and --intent must be records of those types: another journal record,
// such as the holder's own offer, is refused.
func TestAnAgreementCitesAnAdmissionDecisionAndAnIntentOnly(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	admit := sealAdmit(t, "GO")
	// The buyer's own offer is a journal record, not an admission decision.
	msg := v1Err(t, "agree", h.offer, "--admit", h.offer)
	assert.Contains(t, msg, "is a record of type propose in your journal, not an admission decision")
	msg = v1Err(t, "agree", h.offer, "--admit", admit, "--intent", admit)
	assert.Contains(t, msg, "is a record of type admit in your journal, not an intent")
}

// A store outcome with nothing set is a fault the verb says in words and
// exits non-zero on, for a record and for a proof (Sherlock2, buzz 694e4902).
func TestAnOutcomeNoneOfTheDefinedIsAFault(t *testing.T) {
	h := newHandshakeV1(t)
	real := receiveFromStore
	t.Cleanup(func() { receiveFromStore = real })
	receiveFromStore = func(context.Context, *evidencebook.Shelf, []byte, *evidencebook.StampProof) (evidencebook.ReceiveResult, error) {
		return evidencebook.ReceiveResult{Record: &evidencebook.RecordOutcome{}, Proof: &evidencebook.ProofOutcome{}}, nil
	}
	h.buyer.act(t)
	out, err := invoke(t, "", "--profile", "deal", "deal", "receive", h.sellerAgree)
	require.Error(t, err, "a fault exits non-zero")
	assert.Contains(t, out+err.Error(), "not kept: the store returned no outcome, which is a fault; report it")
	assert.Equal(t, 2, strings.Count(out, "not kept: the store returned no outcome, which is a fault; report it"), "for the record and for the proof: %s", out)
}

// A cancellation the offerer sends is received against the offer it names; the
// state follows, and the deal is not agreed to.
func TestACancellationIsReceivedAgainstItsOffer(t *testing.T) {
	seller := newV1Side(t)
	buyer := newV1Side(t)
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offerFile)
	cancelFile := filepath.Join(t.TempDir(), "cancel.json")
	dealRun(t, "offer", "cancel", res["offer"].(string), "--out", cancelFile)

	// The offerer holds its own offer: echoed back to it, the offer is refused.
	assert.Contains(t, v1Err(t, "receive", offerFile), "already holds deal")

	seller.act(t)
	dealRun(t, "receive", offerFile)
	again := dealRun(t, "receive", offerFile)
	assert.Equal(t, "already kept", again["record"], "an offer received twice is a duplicate, not a refusal")
	got := dealRun(t, "receive", cancelFile)
	assert.Equal(t, "agree", got["record_type"])
	var state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT state FROM deal_versions WHERE deal_id=?`, res["deal_id"]).Scan(&state))
	assert.Equal(t, "cancelled", state)
	assert.Contains(t, v1Err(t, "agree", res["offer"].(string), "--admit", sealAdmit(t, "GO")), "is cancelled")
	_ = buyer
}

// The acceptor's stamp is taken once: running `deal agree` again writes the
// same stamp proof.
func TestARerunOfTheAcceptorsAgreeTakesNoSecondStamp(t *testing.T) {
	h := newHandshakeV1(t)
	h.seller.act(t)
	again := filepath.Join(t.TempDir(), "again.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", again)
	first, second := readPush(t, h.sellerAgree), readPush(t, again)
	require.NotNil(t, second.StampProof)
	assert.Equal(t, first.StampProof.ShelfCheckpoint, second.StampProof.ShelfCheckpoint, "the same shelf checkpoint: no second stamp")
}

// A book record for a deal the profile does not hold is not taken.
func TestABookRecordForADealNotHeldIsNotTaken(t *testing.T) {
	h := newHandshakeV1(t)
	_ = h
	newV1Side(t) // a third profile that holds no such deal
	assert.Error(t, errOf(t, "receive", h.sellerAgree))
}

func errOf(t *testing.T, args ...string) error {
	t.Helper()
	_, err := invoke(t, "", append([]string{"--profile", "deal", "deal"}, args...)...)
	return err
}

// An offer written by slice 1 (no deal_v1 row) is rebuilt from the stored
// offer when the acceptor's agreement arrives, and the offerer can agree.
func TestAnOfferWithoutADealRowIsRebuiltWhenTheAgreementArrives(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	_, err := openWritableDB(t).Exec(`DELETE FROM deal_v1 WHERE deal_id=?`, h.dealID)
	require.NoError(t, err)
	got := dealRun(t, "receive", h.sellerAgree)
	assert.Equal(t, "kept, and counted", got["record"])
	out := dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, "offerer", out["side"])
}

// A stop after the store took the offer and before the local rows were
// committed leaves none of them; receiving the same file again finds the
// offer already kept, writes the rows, and the deal can be agreed.
func TestAnOfferWhoseRowsDidNotCommitIsRecoveredByReceivingItAgain(t *testing.T) {
	seller := newV1Side(t)
	buyer := newV1Side(t)
	offerFile := filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offerFile)

	seller.act(t)
	t.Cleanup(func() { v1NoteStops = func() error { return nil } })
	v1NoteStops = func() error { return errors.New("stopped before the rows were committed") }
	require.Error(t, errOf(t, "receive", offerFile))
	var n int
	require.NoError(t, v1DB(t).QueryRow(`SELECT (SELECT COUNT(*) FROM deal_versions) + (SELECT COUNT(*) FROM deal_v1) + (SELECT COUNT(*) FROM deal_v1_offers)`).Scan(&n))
	assert.Zero(t, n, "all three rows rolled back together")

	v1NoteStops = func() error { return nil }
	got := dealRun(t, "receive", offerFile)
	assert.Equal(t, "already kept", got["record"], "the store kept it the first time")
	out := dealRun(t, "agree", res["offer"].(string), "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "a.json"))
	assert.Equal(t, "acceptor", out["side"])
	_ = buyer
}

// An agreement recorded while the deal's state still says `agreeing` (a stop
// between the two updates) is repaired by running `deal agree` again.
func TestADealLeftAgreeingIsRepairedByAgreeingAgain(t *testing.T) {
	h := newHandshakeV1(t)
	h.seller.act(t)
	_, err := openWritableDB(t).Exec(`UPDATE deal_versions SET state='agreeing' WHERE deal_id=?`, h.dealID)
	require.NoError(t, err)
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", filepath.Join(t.TempDir(), "again.json"))
	var state string
	require.NoError(t, v1DB(t).QueryRow(`SELECT state FROM deal_versions WHERE deal_id=?`, h.dealID).Scan(&state))
	assert.Equal(t, "agreed", state)
}

// A journal record that is not an x-deal-v1 record (a v0 record on a migrated
// store) is said so, not described by an empty type.
func TestAnAdmitThatIsNotAV1RecordIsSaidSo(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	var digest string
	withSession(t, func(s *dealSession) {
		payload := []byte(`{"not":"a record"}`)
		_, _, err := s.shelf.SealSent(t.Context(), evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}}, "x/sent")
		require.NoError(t, err)
		digest = hexSHA256(payload)
	})
	msg := v1Err(t, "agree", h.offer, "--admit", digest)
	assert.Contains(t, msg, "is not an x-deal-v1 record")
	assert.NotContains(t, msg, "type  in")
}

// Two admission decisions with the same outcome have different digests: each
// carries its own fresh nonce, so a counterparty cannot test a guess at the
// outcome against the digest an agreement cites (u528, evidencebook v0.17.0).
func TestEachAdmitCarriesItsOwnNonce(t *testing.T) {
	newV1Side(t)
	assert.NotEqual(t, sealAdmit(t, "GO"), sealAdmit(t, "GO"))
}
