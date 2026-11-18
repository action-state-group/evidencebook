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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A party with a shelf, its x-deal-v1 receive profile and its cadence: the
// buyer and the seller of the tests below, as Appendix A runs them.
type party struct {
	t       *testing.T
	dir     string
	cfg     evidencebook.ShelfConfig
	shelf   *evidencebook.Shelf
	profile *ReceiveProfile
	cadence *evidencebook.Cadence
	now     time.Time
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
	dir := t.TempDir()
	signer, err := evidencebook.NewEd25519Signer(seed(name + " record key"))
	require.NoError(t, err)
	originals, err := evidencebook.OpenOriginalsFile(context.Background(), filepath.Join(dir, "originals.jsonl"), "x-deal-v1", signer.PublicKey())
	require.NoError(t, err)
	p := &party{t: t, dir: dir, profile: &ReceiveProfile{}, now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	p.cfg = evidencebook.ShelfConfig{Path: filepath.Join(dir, "shelf.db"), Operator: name, Signer: signer,
		CheckpointKey: seed(name + " checkpoint key"), Originals: originals, Now: func() time.Time { return p.now },
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
	c, err := shelf.Cadence(context.Background(), evidencebook.CadenceConfig{LogID: testCadenceLog,
		Ended: func(context.Context, string) (bool, error) { return false, nil }, Status: noStatus{}})
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
func (p *party) sendOffer(r Record) []byte {
	p.t.Helper()
	p.tick()
	_, sent, err := p.shelf.SealSent(context.Background(), entryOf(p.t, r), r.Block.DealID+SentSuffix)
	require.NoError(p.t, err)
	pair, err := Pair(sent.Capsule, sent.Envelope, sent.Payload)
	require.NoError(p.t, err)
	return pair
}

// seal appends r to the party's book logID and returns its record id and
// book pair.
func (p *party) seal(logID string, r Record) (string, []byte) {
	p.t.Helper()
	p.tick()
	book, err := p.shelf.Open(context.Background(), logID)
	require.NoError(p.t, err)
	rec, err := book.Append(context.Background(), entryOf(p.t, r))
	require.NoError(p.t, err)
	sealed, err := book.Sealed(context.Background(), rec.RecordID)
	require.NoError(p.t, err)
	pair, err := BookPair(sealed, jcsOf(p.t, r))
	require.NoError(p.t, err)
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
	return proof
}

func (p *party) receive(record []byte, proof *evidencebook.StampProof) evidencebook.ReceiveResult {
	p.t.Helper()
	res, err := p.shelf.Receive(context.Background(), record, proof)
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
	ok, err := h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.False(t, ok, "nothing held yet")

	res := h.buyer.receive(h.agreementPair, &h.proof)
	assert.True(t, res.Record.Stored)
	require.NotNil(t, res.Proof)
	assert.True(t, res.Proof.Accepted)
	ok, err = h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.True(t, ok)

	// duplicate-proof and restart: the same proof again is a duplicate, and
	// the gate stays open across a restart.
	res = h.buyer.receive(nil, &h.proof)
	assert.True(t, res.Proof.Duplicate)
	h.buyer.reopen()
	ok, err = h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.True(t, ok, "after a restart")
}

// Without a proof the agreement is held but the act is refused.
func TestNoProofNoAct(t *testing.T) {
	h := newHandshake(t, nil)
	h.buyer.receive(h.agreementPair, nil)
	ok, err := h.buyer.profile.CanAct(context.Background(), h.agreementID)
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
	ok, err := h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.False(t, ok)
	h.buyer.reopen()
	ok, err = h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.False(t, ok, "after a restart")
}

// A proof by another checkpoint key than the agreement names relates to no
// sender: it is refused.
func TestProofByAnotherCheckpointKey(t *testing.T) {
	h := newHandshake(t, func(a *AgreeBody) { a.CheckpointKey = hex.EncodeToString(make([]byte, 32)) })
	res, _ := h.buyer.shelf.Receive(context.Background(), h.agreementPair, &h.proof)
	require.NotNil(t, res.Proof)
	assert.ErrorIs(t, res.Proof.Refused, evidencebook.ErrRefused)
	ok, err := h.buyer.profile.CanAct(context.Background(), h.agreementID)
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
	other.Body = &ob
	other.Block.At = "2026-11-01T09:46:00Z"
	_, forkPair := h.seller.sealOnNewShelf(h.sellerLog, other)
	res := h.buyer.receive(forkPair, nil)
	assert.True(t, res.Record.Stored, "kept as a fork")
	ok, err := h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.False(t, ok)
	h.buyer.reopen()
	ok, err = h.buyer.profile.CanAct(context.Background(), h.agreementID)
	require.NoError(t, err)
	assert.False(t, ok, "after a restart")
	sender, found, err := h.buyer.profile.ProofSender(context.Background(), h.seller.checkpointKey(), h.sellerLog)
	require.NoError(t, err)
	assert.False(t, found, "a sender with two records at position 1 is refused: %s", sender)
}

// sealOnNewShelf seals r at position 1 of logID on a fresh shelf under the
// same record key: another first record of the same book, a fork.
func (p *party) sealOnNewShelf(logID string, r Record) (string, []byte) {
	p.t.Helper()
	q := newParty(p.t, "fork")
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
	sender, ok, err := h.buyer.profile.ProofSender(context.Background(), h.seller.checkpointKey(), h.sellerLog)
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
	sender, ok, err = h.seller.profile.ProofSender(context.Background(), h.buyer.checkpointKey(), buyerLog)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, h.buyer.key(), sender)
	ok, err = h.seller.profile.CanAct(context.Background(), buyerID)
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
	ok, err := h.seller.profile.Answered(context.Background(), held[0].ReceivedRecord)
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
	ok, err = h.seller.profile.Answered(context.Background(), held[0].ReceivedRecord)
	require.NoError(t, err)
	assert.False(t, ok)
}

// Admit: a stranger's deal-book record, a decline from a key the offer was
// not sent to, and a ruling from no adjudicator are refused.
func TestAdmitRefusesStrangers(t *testing.T) {
	h := newHandshake(t, nil)
	stranger := newParty(t, "stranger")
	_, pair := stranger.seal("deal/"+hex.EncodeToString(seed("stranger log")[:16]), h.agreement)
	_, err := h.buyer.shelf.Receive(context.Background(), pair, nil)
	assert.ErrorIs(t, err, evidencebook.ErrRefused)

	od := mustDigest(t, h.offer)
	decline := Record{Block: journalBlock(TypeAgree, "2026-11-01T09:40:00Z", true), Body: &AgreeBody{Kind: KindDecline, Offer: RefTo(od), Reason: ReasonOther}}
	_, err = h.buyer.shelf.Receive(context.Background(), stranger.sendOffer(decline), nil)
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
