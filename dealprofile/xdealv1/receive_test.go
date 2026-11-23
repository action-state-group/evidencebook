// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	evidencebook "github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/internal/detrand"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A party with a shelf, its x-deal-v1 receive profile and its cadence: the
// buyer and the seller of the tests below, as Appendix A runs them.
type party struct {
	t       *testing.T
	name    string
	dir     string
	cfg     evidencebook.ShelfConfig
	shelf   *evidencebook.Shelf
	profile *ReceiveProfile
	cadence *evidencebook.Cadence
	now     time.Time
	// status, when set, is the cadence's status provider (noStatus
	// otherwise); open points it at the shelf.
	status *StatusProvider
}

const testCadenceLog = "deal-cadence/0123456789abcdef"

func seed(label string) ed25519.PrivateKey {
	s := sha256.Sum256([]byte("xdealv1 receive tests\x00" + label))
	return ed25519.NewKeyFromSeed(s[:])
}

// noStatus gives no status value: the profile's StatusProvider is PR 4c's.
type noStatus struct{}

func (noStatus) Values(context.Context, string, string) (string, evidencebook.LinkValue, json.RawMessage, bool, error) {
	return "", evidencebook.LinkValue{}, nil, false, nil
}

func newParty(t *testing.T, name string) *party {
	t.Helper()
	return newPartyKeyed(t, name, seed(name+" checkpoint key"))
}

// newAdjudicator is a party that checkpoints with its record key, as an
// adjudicator does (PROFILE.md §4, "The case book").
func newAdjudicator(t *testing.T, name string) *party {
	t.Helper()
	return newPartyKeyed(t, name, seed(name+" record key"))
}

func newPartyKeyed(t *testing.T, name string, checkpointKey ed25519.PrivateKey) *party {
	t.Helper()
	dir := t.TempDir()
	signer, err := evidencebook.NewEd25519Signer(seed(name + " record key"))
	require.NoError(t, err)
	originals, err := evidencebook.OpenOriginalsFile(context.Background(), filepath.Join(dir, "originals.jsonl"), "x-deal-v1", signer.PublicKey())
	require.NoError(t, err)
	p := &party{t: t, name: name, dir: dir, profile: &ReceiveProfile{}, now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	recordStep(txOp{Op: "party", Party: name})
	p.cfg = evidencebook.ShelfConfig{Path: filepath.Join(dir, "shelf.db"), Operator: name, Signer: signer,
		CheckpointKey: checkpointKey, Originals: originals, Now: func() time.Time { return p.now },
		// A fixed random stream: the transcripts (holder_vectors_test.go)
		// are the same on every run, and Python replays them.
		Random:  detrand.New("xdealv1 receive tests\x00" + name + " random"),
		Receive: evidencebook.ReceiveConfig{Profile: p.profile},
		// A deal book's log holds its own records only, so the counterparty
		// can recompute its checkpoints from the records it receives.
		AuthenticatedIndexes: func(string) []evidencebook.AuthenticatedIndex { return []evidencebook.AuthenticatedIndex{} }}
	p.open()
	return p
}

func (p *party) open() {
	p.t.Helper()
	shelf, err := evidencebook.OpenSQLiteShelf(context.Background(), p.cfg)
	require.NoError(p.t, err)
	p.t.Cleanup(func() { _ = shelf.Release() })
	p.shelf, p.profile.Shelf = shelf, shelf
	var status evidencebook.StatusProvider = noStatus{}
	if p.status != nil {
		p.status.Shelf = shelf
		status = p.status
	}
	c, err := shelf.Cadence(context.Background(), evidencebook.CadenceConfig{LogID: testCadenceLog,
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Status: status})
	require.NoError(p.t, err)
	p.cadence = c
	// The journal and SealSent need a shelf of book facts.
	if migrated, err := c.Migrated(context.Background()); err == nil && !migrated {
		_, err := c.Tick(context.Background(), p.now)
		require.NoError(p.t, err)
		p.tick()
		require.NoError(p.t, c.Migrate(context.Background(), p.now))
	}
}

func (p *party) reopen() {
	p.t.Helper()
	recordStep(txOp{Op: "reopen", Party: p.name})
	require.NoError(p.t, p.shelf.Release())
	p.open()
}

func (p *party) key() string { return evidencebook.KeyID(p.cfg.Signer) }

func (p *party) checkpointKey() string {
	return hex.EncodeToString(p.cfg.CheckpointKey.Public().(ed25519.PublicKey))
}

func (p *party) tick() { p.now = p.now.Add(time.Minute) }

func jcsOf(t *testing.T, r Record) []byte {
	t.Helper()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	doc, err := decodeJSON(raw)
	require.NoError(t, err)
	out, err := canonical.JCS(doc)
	require.NoError(t, err)
	return out
}

func entryOf(t *testing.T, r Record) evidencebook.Entry {
	return evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.EpistemicType("producer_claim"),
		Payloads: [][]byte{jcsOf(t, r)}, SubjectRef: r.Block.DealID}
}

// sendOffer seals the offer in the party's journal and its standalone
// Capsule, and returns the sent pair.
func (p *party) sendOffer(r Record) []byte { return p.sendOfferWith(r, termsMembers(p.t, r)) }

// sendOfferWith is sendOffer with further members on the sent Capsule.
func (p *party) sendOfferWith(r Record, members map[string]json.RawMessage) []byte {
	p.t.Helper()
	p.tick()
	e := entryOf(p.t, r)
	e.Members = members
	_, sent, err := p.shelf.SealSent(context.Background(), e, r.Block.DealID+SentSuffix)
	require.NoError(p.t, err)
	pair, err := Pair(sent.Capsule, sent.Envelope, sent.Payload)
	require.NoError(p.t, err)
	recordStep(txOp{Op: "send_offer", Party: p.name, Record: rawOf(p.t, r), Members: members, Out: hex.EncodeToString(pair)})
	return pair
}

// seal appends r to the party's book logID and returns its record id and
// book pair.
func (p *party) seal(logID string, r Record) (string, []byte) { return p.sealWith(logID, r, nil) }

// sealWith is seal with further members on the record's Capsule.
func (p *party) sealWith(logID string, r Record, members map[string]json.RawMessage) (string, []byte) {
	p.t.Helper()
	p.tick()
	book, err := p.shelf.Open(context.Background(), logID)
	require.NoError(p.t, err)
	e := entryOf(p.t, r)
	e.Members = members
	// A deal-book record cites the record before it (design §3.1).
	if size := book.Size(); size > 0 {
		last, err := book.Query(context.Background(), evidencebook.Filter{FromSeq: size, ToSeq: size})
		require.NoError(p.t, err)
		require.Len(p.t, last, 1)
		e.Links = []evidencebook.Link{{Type: evidencebook.Cites, Target: last[0].RecordID}}
	}
	rec, err := book.Append(context.Background(), e)
	require.NoError(p.t, err)
	sealed, err := book.Sealed(context.Background(), rec.RecordID)
	require.NoError(p.t, err)
	pair, err := BookPair(sealed, jcsOf(p.t, r))
	require.NoError(p.t, err)
	recordStep(txOp{Op: "seal", Party: p.name, Log: logID, Record: rawOf(p.t, r), Members: members,
		Out: map[string]string{"record_id": rec.RecordID, "pair": hex.EncodeToString(pair)}})
	return rec.RecordID, pair
}

// stamp takes a stamp and returns the counterparty's proof for the book
// whose first record is firstRecord.
func (p *party) stamp(firstRecord string) evidencebook.StampProof {
	p.t.Helper()
	ctx := context.Background()
	p.tick()
	_, err := p.cadence.Stamp(ctx, p.now)
	require.NoError(p.t, err)
	proof, err := p.cadence.StampProof(ctx, firstRecord, 0, evidencebook.AudienceCounterparty)
	require.NoError(p.t, err)
	recordStep(txOp{Op: "stamp", Party: p.name, First: firstRecord, Out: proof})
	return proof
}

func (p *party) receive(record []byte, proof *evidencebook.StampProof) evidencebook.ReceiveResult {
	p.t.Helper()
	res, err := p.receiveRaw(record, proof)
	require.NoError(p.t, err)
	return res
}

// handshake is Appendix A's offer and the seller's agreement with its stamp
// proof, up to the buyer receiving them (lines 11 to 17a).
type handshake struct {
	buyer, seller *party
	offer         Record
	offerPair     []byte
	sellerLog     string
	agreement     Record
	agreementID   string
	agreementPair []byte
	proof         evidencebook.StampProof
}

func newHandshake(t *testing.T, edit func(*AgreeBody)) *handshake {
	t.Helper()
	h := &handshake{buyer: newParty(t, "buyer"), seller: newParty(t, "seller"), sellerLog: "deal/" + hex.EncodeToString(seed("seller log")[:16])}
	ob := fxOffer()
	ob.To = Some(h.seller.key())
	h.offer = Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: ob}
	h.offerPair = h.buyer.sendOffer(h.offer)
	res := h.seller.receive(h.offerPair, nil)
	require.True(t, res.Record.Stored, "the seller holds the offer")

	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), nil, SideAcceptor, RoleSeller, h.buyer.key())
	ab.CheckpointKey, ab.ShelfLogID = h.seller.checkpointKey(), testCadenceLog
	if edit != nil {
		edit(ab)
	}
	b := journalBlock(TypeAgree, "2026-11-01T09:45:00Z", true)
	b.Seq, b.Seen = 1, NullOf[string]()
	h.agreement = Record{Block: b, Body: ab}
	require.NoError(t, Check(h.agreement))
	h.agreementID, h.agreementPair = h.seller.seal(h.sellerLog, h.agreement)
	h.proof = h.seller.stamp(h.agreementID)
	return h
}

func TestBookPairVerifies(t *testing.T) {
	h := newHandshake(t, nil)
	b, err := VerifyBook(h.agreementPair)
	require.NoError(t, err)
	assert.Equal(t, h.agreementID, b.RecordID)
	assert.Equal(t, h.seller.key(), b.KeyID)
	assert.Equal(t, h.sellerLog, b.LogID)
	assert.Equal(t, uint64(1), b.Seq)
	assert.Empty(t, b.Prev)

	tamper := func(f func(m map[string]json.RawMessage)) []byte {
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(h.agreementPair, &m))
		f(m)
		out, err := json.Marshal(m)
		require.NoError(t, err)
		return out
	}
	for name, pair := range map[string][]byte{
		"another payload": tamper(func(m map[string]json.RawMessage) { m["payload"] = jcsOf(t, h.offer) }),
		"another header": tamper(func(m map[string]json.RawMessage) {
			var hd map[string]any
			require.NoError(t, json.Unmarshal(m["header"], &hd))
			hd["seq"] = 2
			m["header"], _ = json.Marshal(hd)
		}),
		"no signature": tamper(func(m map[string]json.RawMessage) {
			var rec map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(m["record"], &rec))
			delete(rec, "signature")
			m["record"], _ = json.Marshal(rec)
		}),
		"a sent pair": h.offerPair,
	} {
		_, err := VerifyBook(pair)
		assert.Error(t, err, name)
	}
}

// The handshake from an offer (design §7.3, handshake-from-offer): the
// buyer holds only its journal offer; the seller's agreement and proof are
// admitted, and the gate opens.
func TestHandshakeFromOffer(t *testing.T) {
	h := newHandshake(t, nil)
	ok, err := h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok, "nothing held yet")

	res := h.buyer.receive(h.agreementPair, &h.proof)
	assert.True(t, res.Record.Stored)
	require.NotNil(t, res.Proof)
	assert.True(t, res.Proof.Accepted)
	ok, err = h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.True(t, ok)

	// The gate is of one deal: the same agreement and proof do not open it
	// for another deal, and a commitment naming another shelf log closes it.
	// A slice, not a map: each call is a transcript step, in order.
	for _, c := range []struct {
		name string
		in   CanActInput
	}{
		{"another deal", CanActInput{DealID: "deal-9999999999999999", CounterpartAgreement: h.agreementID}},
		{"another commitment shelf log", CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID, CommitmentShelfLog: "deal-cadence/ffffffffffffffff"}},
	} {
		ok, err = h.buyer.recCanAct(c.in)
		require.NoError(t, err)
		assert.False(t, ok, c.name)
	}
	ok, err = h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID, CommitmentShelfLog: testCadenceLog})
	require.NoError(t, err)
	assert.True(t, ok, "the commitment names the agreement's shelf log")

	// duplicate-proof and restart: the same proof again is a duplicate, and
	// the gate stays open across a restart.
	res = h.buyer.receive(nil, &h.proof)
	assert.True(t, res.Proof.Duplicate)
	h.buyer.reopen()
	ok, err = h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.True(t, ok, "after a restart")
}

// Without a proof the agreement is held but the act is refused.
func TestNoProofNoAct(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, nil)
	ok, err := h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok)
}

// A proof over another shelf log than the agreement names leaves the gate
// shut, before and after a restart (wrong-shelf-then-restart).
func TestProofUnderAnotherShelfLog(t *testing.T) {
	h := newHandshake(t, func(a *AgreeBody) { a.ShelfLogID = "deal-cadence/ffffffffffffffff" })
	h.buyer.receive(h.agreementPair, &h.proof)
	proofs, err := h.buyer.shelf.HeldProofs(context.Background(), h.seller.key(), h.sellerLog)
	require.NoError(t, err)
	require.Len(t, proofs, 1)
	require.Equal(t, uint64(1), proofs[0].Covers, "the proof is held and covers the agreement; only its shelf log differs")
	ok, err := h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok)
	h.buyer.reopen()
	ok, err = h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok, "after a restart")
}

// A proof by another checkpoint key than the agreement names relates to no
// sender: it is refused.
func TestProofByAnotherCheckpointKey(t *testing.T) {
	h := newHandshake(t, func(a *AgreeBody) { a.CheckpointKey = hex.EncodeToString(make([]byte, 32)) })
	res, _ := h.buyer.receiveRaw(h.agreementPair, &h.proof)
	require.NotNil(t, res.Proof)
	assert.ErrorIs(t, res.Proof.Refused, evidencebook.ErrRefused)
	ok, err := h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok)
}

// A second record from the seller at position 1 of the same log, after the
// proof was accepted, forks the genesis: the act is refused, before and
// after a restart, and a proof for that log is refused from then on
// (genesis-fork-after-proof, fork-at-position-1).
func TestGenesisForkAfterProof(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, &h.proof)
	other := h.agreement
	ob := *h.agreement.Body.(*AgreeBody)
	ob.Role = RoleSeller
	ob.ShelfLogID = testCadenceLog
	// The forked agreement names another checkpoint key: filtering by key
	// would keep the first; the fork is refused before any filter.
	ob.CheckpointKey = hex.EncodeToString(make([]byte, 32))
	other.Body = &ob
	other.Block.At = "2026-11-01T09:46:00Z"
	_, forkPair := h.seller.sealOnNewShelf(h.sellerLog, other)
	res := h.buyer.receive(forkPair, nil)
	assert.True(t, res.Record.Stored, "kept as a fork")
	ok, err := h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok)
	h.buyer.reopen()
	ok, err = h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: h.agreementID})
	require.NoError(t, err)
	assert.False(t, ok, "after a restart")
	sender, found, err := h.buyer.recProofSender(h.seller.checkpointKey(), h.sellerLog)
	require.NoError(t, err)
	assert.False(t, found, "a sender with two records at position 1 is refused: %s", sender)
}

// sealOnNewShelf seals r at position 1 of logID on a fresh shelf under the
// same record key: another first record of the same book, a fork.
func (p *party) sealOnNewShelf(logID string, r Record) (string, []byte) {
	p.t.Helper()
	q := newParty(p.t, "fork")
	recordStep(txOp{Op: "reopen_as", Party: "fork", In: p.name})
	q.cfg.Signer = p.cfg.Signer
	originals, err := evidencebook.OpenOriginalsFile(context.Background(), filepath.Join(q.dir, "originals2.jsonl"), "x-deal-v1", p.cfg.Signer.PublicKey())
	require.NoError(p.t, err)
	q.cfg.Originals = originals
	q.cfg.Path = filepath.Join(q.dir, "fork.db")
	q.open()
	return q.seal(logID, r)
}

// ProofSender on both sides: the seller's proof at the buyer, and the
// buyer's proof at the seller, each give the proof author's record key
// (proof-sender-both-sides).
func TestProofSenderBothSides(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, &h.proof)
	sender, ok, err := h.buyer.recProofSender(h.seller.checkpointKey(), h.sellerLog)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, h.seller.key(), sender)

	// The buyer's agreement, naming the seller's, and its proof.
	od, err := Digest(h.offer)
	require.NoError(t, err)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: od}, ref('e'), ref('f'), SideOfferer, RoleBuyer, h.seller.key())
	ab.CheckpointKey, ab.ShelfLogID = h.buyer.checkpointKey(), testCadenceLog
	b := journalBlock(TypeAgree, "2026-11-01T10:00:00Z", true)
	b.Seq, b.Seen = 1, Some(h.agreementID)
	buyerAg := Record{Block: b, Body: ab}
	require.NoError(t, CheckAgreement(buyerAg, h.offer, h.agreementID))
	buyerLog := "deal/" + hex.EncodeToString(seed("buyer log")[:16])
	buyerID, buyerPair := h.buyer.seal(buyerLog, buyerAg)
	buyerProof := h.buyer.stamp(buyerID)
	res := h.seller.receive(buyerPair, &buyerProof)
	assert.True(t, res.Record.Stored)
	require.NotNil(t, res.Proof)
	assert.True(t, res.Proof.Accepted)
	sender, ok, err = h.seller.recProofSender(h.buyer.checkpointKey(), buyerLog)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, h.buyer.key(), sender)
	ok, err = h.seller.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: buyerID})
	require.NoError(t, err)
	assert.True(t, ok)
}

// Answered: an offer the seller agreed to stays answered; one it holds and
// did not answer is not.
func TestAnswered(t *testing.T) {
	h := newHandshake(t, nil)
	held, err := h.seller.shelf.HeldFirst(context.Background(), SentLogPrefix+mustDigest(t, h.offer))
	require.NoError(t, err)
	require.Len(t, held, 1)
	ok, err := h.seller.recAnswered(held[0].ReceivedRecord)
	require.NoError(t, err)
	assert.True(t, ok, "the seller's agreement cites it")

	ob := fxOffer()
	ob.To = Some(h.seller.key())
	other := Record{Block: journalBlock(TypePropose, "2026-11-01T09:31:00Z", true), Body: ob}
	other.Block.DealID = "deal-1111111111111111"
	h.seller.receive(h.buyer.sendOffer(other), nil)
	held, err = h.seller.shelf.HeldFirst(context.Background(), SentLogPrefix+mustDigest(t, other))
	require.NoError(t, err)
	require.Len(t, held, 1)
	ok, err = h.seller.recAnswered(held[0].ReceivedRecord)
	require.NoError(t, err)
	assert.False(t, ok)
}

// Admit: a stranger's deal-book record, a decline from a key the offer was
// not sent to, and a ruling from no adjudicator are refused.
func TestAdmitRefusesStrangers(t *testing.T) {
	h := newHandshake(t, nil)
	stranger := newParty(t, "stranger")
	_, pair := stranger.seal("deal/"+hex.EncodeToString(seed("stranger log")[:16]), h.agreement)
	_, err := h.buyer.receiveRaw(pair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)

	od := mustDigest(t, h.offer)
	decline := Record{Block: journalBlock(TypeAgree, "2026-11-01T09:40:00Z", true), Body: &AgreeBody{Kind: KindDecline, Offer: RefTo(od), Reason: ReasonOther}}
	_, err = h.buyer.receiveRaw(stranger.sendOffer(decline), nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused, "a decline from a key the offer was not sent to")

	res := h.buyer.receive(h.seller.sendOffer(decline), nil)
	assert.True(t, res.Record.Stored, "the decline from the key the offer was sent to")
}

func mustDigest(t *testing.T, r Record) string {
	t.Helper()
	d, err := Digest(r)
	require.NoError(t, err)
	return d
}

func settleRecord(t *testing.T, seq int64, prev, agreement Record, seen string) Record {
	t.Helper()
	b := journalBlock(TypeSettle, "2026-11-01T10:20:00Z", true)
	pr, ag := RefTo(mustDigest(t, prev)), RefTo(mustDigest(t, agreement))
	b.Seq, b.Prev, b.AgreementRef, b.Seen = seq, &pr, &ag, Some(seen)
	r := Record{Block: b, Body: &SettleBody{Leg: LegPayee, Statements: statements(item("paid_as_agreed", ValueMet))}}
	require.NoError(t, Check(r))
	return r
}

// A later record of an admitted book is admitted, placed after the record it
// cites; a later record of a log whose first record was never admitted is
// refused.
func TestLaterRecords(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, &h.proof)
	next := settleRecord(t, 2, h.agreement, h.agreement, h.agreementID)
	id, pair := h.seller.seal(h.sellerLog, next)
	b, err := VerifyBook(pair)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), b.Seq)
	assert.Equal(t, h.agreementID, b.Prev, "it cites the record before it")
	res := h.buyer.receive(pair, nil)
	assert.True(t, res.Record.Stored && res.Record.Counted)
	assert.Equal(t, id, res.Record.Digest)

	// Another log of the seller's, whose agreement the buyer never received.
	otherLog := "deal/" + hex.EncodeToString(seed("seller other log")[:16])
	otherID, _ := h.seller.seal(otherLog, h.agreement)
	_ = otherID
	_, orphan := h.seller.seal(otherLog, settleRecord(t, 2, h.agreement, h.agreement, h.agreementID))
	_, err = h.buyer.receiveRaw(orphan, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused, "no admitted first record on that log")
}

// An agreement citing our counter-offer, not our first offer of the deal,
// is admitted.
func TestAgreementOnACounterOffer(t *testing.T) {
	buyer, seller := newParty(t, "buyer"), newParty(t, "seller")
	first := fxOffer()
	first.To = Some(seller.key())
	o1 := Record{Block: journalBlock(TypePropose, "2026-11-01T09:30:00Z", true), Body: first}
	seller.receive(buyer.sendOffer(o1), nil)
	counter := fxOffer()
	counter.To = Some(seller.key())
	c := RefTo(label("the seller's counter-offer"))
	counter.CounterTo = &c
	counter.Terms.PriceMinor = amount(5500)
	o2 := Record{Block: journalBlock(TypePropose, "2026-11-01T09:35:00Z", true), Body: counter}
	seller.receive(buyer.sendOffer(o2), nil)
	ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: mustDigest(t, o2)}, ref('e'), nil, SideAcceptor, RoleSeller, buyer.key())
	ab.CheckpointKey, ab.ShelfLogID = seller.checkpointKey(), testCadenceLog
	b := journalBlock(TypeAgree, "2026-11-01T09:45:00Z", true)
	b.Seq, b.Seen = 1, NullOf[string]()
	_, pair := seller.seal("deal/"+hex.EncodeToString(seed("counter log")[:16]), Record{Block: b, Body: ab})
	res := buyer.receive(pair, nil)
	assert.True(t, res.Record.Stored, "the agreement on our counter-offer")
}

// A cancellation from the offer's signer, of an offer of this deal, is
// admitted; one citing an offer of another deal is refused.
func TestCancellation(t *testing.T) {
	h := newHandshake(t, nil)
	od := mustDigest(t, h.offer)
	cancel := Record{Block: journalBlock(TypeAgree, "2026-11-01T09:50:00Z", true), Body: &AgreeBody{Kind: KindCancellation, Offer: RefTo(od)}}
	res := h.seller.receive(h.buyer.sendOffer(cancel), nil)
	assert.True(t, res.Record.Stored)

	ob := fxOffer()
	ob.To = Some(h.seller.key())
	other := Record{Block: journalBlock(TypePropose, "2026-11-01T09:31:00Z", true), Body: ob}
	other.Block.DealID = "deal-2222222222222222"
	h.seller.receive(h.buyer.sendOffer(other), nil)
	wrong := Record{Block: journalBlock(TypeAgree, "2026-11-01T09:51:00Z", true), Body: &AgreeBody{Kind: KindCancellation, Offer: RefTo(mustDigest(t, other))}}
	_, err := h.seller.receiveRaw(h.buyer.sendOffer(wrong), nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused, "a cancellation of another deal's offer")
}

// A ruling is admitted from our agreement's adjudicator only.
func TestRulingFromTheAdjudicator(t *testing.T) {
	adjudicator := newParty(t, "adjudicator")
	h := newHandshake(t, func(a *AgreeBody) {
		adj := fxAdjudicator
		adj.KeyID = adjudicator.key()
		a.Adjudicator = Some(adj)
	})
	rb := journalBlock(TypeRuling, "2026-11-13T10:00:00Z", true)
	rb.Seq = 1
	ruling := Record{Block: rb, Body: &RulingBody{Row: "as_described", Keys: RulingKeys{Buyer: h.buyer.key(), Seller: h.seller.key()},
		Against: Some(RoleSeller), Value: ValueNotMet, Read: RulingRead{Buyer: NullOf[string](), Seller: Some(h.agreementID)},
		Forum: fxForum, RulesDigest: fxAdjudicator.RulesDigest, Final: false}}
	require.NoError(t, Check(ruling))
	caseLog := caseLogOf(t, h, adjudicator)
	_, pair := adjudicator.seal(caseLog, ruling)
	res := h.seller.receive(pair, nil)
	assert.True(t, res.Record.Stored)

	stranger := newParty(t, "stranger")
	_, forged := stranger.seal(caseLog, ruling)
	_, err := h.seller.receiveRaw(forged, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)
}

// A header committing two payloads is refused: one record id would read as
// either record.
func TestBookPairWithTwoPayloads(t *testing.T) {
	seller := newParty(t, "seller")
	h := newHandshake(t, nil)
	book, err := seller.shelf.Open(context.Background(), "deal/"+hex.EncodeToString(seed("two payloads")[:16]))
	require.NoError(t, err)
	e := entryOf(t, h.agreement)
	e.Payloads = append(e.Payloads, jcsOf(t, h.offer))
	rec, err := book.Append(context.Background(), e)
	require.NoError(t, err)
	sealed, err := book.Sealed(context.Background(), rec.RecordID)
	require.NoError(t, err)
	pair, err := BookPair(sealed, jcsOf(t, h.agreement))
	require.NoError(t, err)
	_, err = VerifyBook(pair)
	assert.ErrorContains(t, err, "one payload")
}

// Two sellers whose books share one log id (shared-log-id): ProofSender
// returns the one agreement under the proof's checkpoint key, and refuses
// when both name it.
func TestSharedLogID(t *testing.T) {
	for _, both := range []bool{false, true} {
		h := newHandshake(t, nil)
		h.buyer.receive(h.agreementPair, &h.proof)
		seller2 := newParty(t, "seller two")
		ob := fxOffer()
		ob.To = Some(seller2.key())
		o2 := Record{Block: journalBlock(TypePropose, "2026-11-01T09:32:00Z", true), Body: ob}
		o2.Block.DealID = "deal-3333333333333333"
		seller2.receive(h.buyer.sendOffer(o2), nil)
		ab := fxAgreement(&Ref{Type: RecordRef, DigestAlg: DigestAlg, Digest: mustDigest(t, o2)}, ref('e'), nil, SideAcceptor, RoleSeller, h.buyer.key())
		ab.CheckpointKey, ab.ShelfLogID = seller2.checkpointKey(), testCadenceLog
		if both {
			ab.CheckpointKey = h.seller.checkpointKey()
		}
		b := journalBlock(TypeAgree, "2026-11-01T09:46:00Z", true)
		b.Seq, b.Seen, b.DealID = 1, NullOf[string](), o2.Block.DealID
		_, pair := seller2.seal(h.sellerLog, Record{Block: b, Body: ab})
		res := h.buyer.receive(pair, nil)
		require.True(t, res.Record.Stored, "the second seller's agreement, on the same log id")
		sender, ok, err := h.buyer.recProofSender(h.seller.checkpointKey(), h.sellerLog)
		require.NoError(t, err)
		if both {
			assert.False(t, ok, "two candidates name the key: refused rather than pick one")
		} else {
			require.True(t, ok)
			assert.Equal(t, h.seller.key(), sender)
		}
	}
}

// A journal offer withheld under retention is read again once it is
// available: a decline of it is refused while it is withheld, and admitted
// after, though the profile read the journal in between.
func TestWithheldOfferReadAgain(t *testing.T) {
	ctx := context.Background()
	h := newHandshake(t, nil)
	journal, err := h.buyer.shelf.Journal(ctx)
	require.NoError(t, err)
	offers, err := journal.Query(ctx, evidencebook.Filter{})
	require.NoError(t, err)
	require.NotEmpty(t, offers)
	offerSeq := offers[len(offers)-1].Header.Seq
	h.buyer.recRetention(offerSeq, evidencebook.Withheld)

	decline := Record{Block: journalBlock(TypeAgree, "2026-11-01T09:40:00Z", true),
		Body: &AgreeBody{Kind: KindDecline, Offer: RefTo(mustDigest(t, h.offer)), Reason: ReasonOther}}
	pair := h.seller.sendOffer(decline)
	_, err = h.buyer.receiveRaw(pair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused, "the offer cannot be read while withheld")

	h.buyer.recRetention(offerSeq, evidencebook.Available)
	res := h.buyer.receive(pair, nil)
	assert.True(t, res.Record.Stored, "read again once available")
}

// A proof whose book checkpoint the records held do not recompute is
// refused and kept as evidence, and the gate stays shut: the buyer holds
// another first record of the seller's log than the one the proof covers.
func TestProofTheRecordsDoNotRecompute(t *testing.T) {
	ctx := context.Background()
	h := newHandshake(t, nil)
	other := h.agreement
	ob := *h.agreement.Body.(*AgreeBody)
	ob.ShelfLogID = testCadenceLog
	other.Body = &ob
	other.Block.At = "2026-11-01T09:44:00Z"
	otherID, otherPair := h.seller.sealOnNewShelf(h.sellerLog, other)
	require.NotEqual(t, h.agreementID, otherID)
	res := h.buyer.receive(otherPair, nil)
	require.True(t, res.Record.Stored)
	res, _ = h.buyer.receiveRaw(nil, &h.proof)
	require.NotNil(t, res.Proof)
	assert.ErrorIs(t, res.Proof.Refused, evidencebook.ErrInvalid, "the held records do not recompute the proof's root")
	evidence, err := h.buyer.shelf.Evidence(ctx)
	require.NoError(t, err)
	kinds := map[string]bool{}
	for _, e := range evidence {
		kinds[e.Kind] = true
	}
	assert.True(t, kinds[evidencebook.EvidenceCheckpointRecords], "the checkpoint is kept as evidence: %v", kinds)
	ok, err := h.buyer.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: otherID})
	require.NoError(t, err)
	assert.False(t, ok)
}

// A journal record withheld when the cache first read the journal, and made
// available after a later record was read, is inserted before that later
// record in the cache's slice, in place when the slice has room. A result
// ours returned before must not change: each caller gets its own copy.
func TestOursResultIsACopy(t *testing.T) {
	ctx := context.Background()
	h := newHandshake(t, nil)
	journal, err := h.buyer.shelf.Journal(ctx)
	require.NoError(t, err)
	offers, err := journal.Query(ctx, evidencebook.Filter{})
	require.NoError(t, err)
	_, err = journal.SetRetention(ctx, offers[0].RecordID, evidencebook.Withheld, "test")
	require.NoError(t, err)

	cold := &ReceiveProfile{Shelf: h.buyer.shelf}
	cold.cache.journal = make([]Record, 0, 8) // room for an insert in place
	o, err := cold.ours(ctx)
	require.NoError(t, err)
	require.Empty(t, o.journal, "the offer is withheld")
	require.NotEmpty(t, cold.cache.retry)

	ob := fxOffer()
	ob.To = Some(h.seller.key())
	later := Record{Block: journalBlock(TypePropose, "2026-11-01T09:31:00Z", true), Body: ob}
	later.Block.DealID = "deal-4444444444444444"
	h.buyer.sendOffer(later)
	before, err := cold.ours(ctx)
	require.NoError(t, err)
	require.Len(t, before.journal, 1)
	want := mustDigest(t, before.journal[0])

	_, err = journal.SetRetention(ctx, offers[0].RecordID, evidencebook.Available, "test")
	require.NoError(t, err)
	after, err := cold.ours(ctx)
	require.NoError(t, err)
	require.Len(t, after.journal, 2, "the offer is read again and inserted first")
	assert.Equal(t, mustDigest(t, h.offer), mustDigest(t, after.journal[0]))
	assert.Equal(t, want, mustDigest(t, before.journal[0]), "an earlier result is unchanged")
}

// A deal whose counterparty runs no layer holds no counterpart agreement:
// CanAct is false, and the gate does not apply to it (design §4.7).
func TestCanActWithoutALayerIsFalse(t *testing.T) {
	h := newHandshake(t, nil)
	ok, err := h.seller.recCanAct(CanActInput{DealID: fxDealID, CounterpartAgreement: ""})
	require.NoError(t, err)
	assert.False(t, ok)
}

// caseLogOf is the case log id of h's deal for adjudicator (CaseLogID).
func caseLogOf(t *testing.T, h *handshake, adjudicator *party) string {
	t.Helper()
	od, err := Digest(h.offer)
	require.NoError(t, err)
	return CaseLogID(od, adjudicator.key())
}
