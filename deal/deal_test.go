// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/action-state-group/evidencebook/internal/detrand"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

const testCadenceLog = "deal-cadence/0123456789abcdef"

func seed(label string) ed25519.PrivateKey {
	s := sha256.Sum256([]byte("deal store tests\x00" + label))
	return ed25519.NewKeyFromSeed(s[:])
}

// side is one party: its shelf, cadence and store, on a clock the test moves.
type side struct {
	t       *testing.T
	name    string
	now     time.Time
	key     ed25519.PrivateKey
	shelf   *evidencebook.Shelf
	cadence *evidencebook.Cadence
	store   *Store
	db      *sql.DB
}

func newSide(t *testing.T, name string) *side {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	p := &side{t: t, name: name, now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), key: seed(name + " record key")}
	signer, err := evidencebook.NewEd25519Signer(p.key)
	require.NoError(t, err)
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "x-deal-v1", signer.PublicKey())
	require.NoError(t, err)
	checkpointKey := seed(name + " checkpoint key")
	profile := &xdealv1.ReceiveProfile{}
	path := filepath.Join(dir, "shelf.db")
	clock := func() time.Time { return p.now }
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: path, Operator: name, Signer: signer, CheckpointKey: checkpointKey, Originals: originals, Now: clock,
		Random:  detrand.New("deal store tests\x00" + name + " shelf"),
		Receive: evidencebook.ReceiveConfig{Profile: profile},
		AuthenticatedIndexes: func(string) []evidencebook.AuthenticatedIndex {
			return []evidencebook.AuthenticatedIndex{}
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shelf.Release() })
	profile.Shelf = shelf
	p.shelf = shelf
	p.db, err = sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.db.Close() })
	p.store, err = Open(ctx, Config{
		Shelf: shelf, DB: p.db, SigningKey: p.key.Public().(ed25519.PublicKey),
		CheckpointKey: checkpointKey.Public().(ed25519.PublicKey), ShelfLogID: testCadenceLog,
		Now: clock, Random: detrand.New("deal store tests\x00" + name + " store"),
	})
	require.NoError(t, err)
	p.cadence, err = shelf.Cadence(ctx, evidencebook.CadenceConfig{LogID: testCadenceLog,
		Ended:  func(context.Context, string) (bool, error) { return false, nil },
		Status: &xdealv1.StatusProvider{Shelf: shelf, OfferPair: p.store.OfferPair}})
	require.NoError(t, err)
	// The journal and SealSent need a shelf of book facts.
	_, err = p.cadence.Tick(ctx, p.now)
	require.NoError(t, err)
	p.tick()
	require.NoError(t, p.cadence.Migrate(ctx, p.now))
	return p
}

func (p *side) tick()         { p.now = p.now.Add(time.Minute) }
func (p *side) keyID() string { return hex.EncodeToString(p.key.Public().(ed25519.PublicKey)) }

// offerTo is a buyer's offer of one payment row to key (null: to a party
// that keeps no records).
func offerTo(t *testing.T, key *string) xdealv1.ProposeBody {
	t.Helper()
	to := "null"
	if key != nil {
		to = `"` + *key + `"`
	}
	raw := `{
  "role": "buyer", "to": ` + to + `,
  "terms": {"item": "lamp", "currency": "USD"},
  "pack_digest": "` + strings.Repeat("a", 64) + `",
  "agree_by": "2026-11-02T10:00:00Z",
  "terms_rows": {"rows": [{"id": "paid_as_agreed", "owed_by": "buyer", "owed": {"amount_minor": 6000, "currency": "USD"},
    "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"}}], "named": {}},
  "adjudicator": null, "forums": [], "witnesses": []
}`
	var body xdealv1.ProposeBody
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	return body
}

// engineOutput is what `capsule-engine admission evaluate --admit FILE`
// prints for a decision; edit changes it before it is encoded.
func engineOutput(t *testing.T, verdict string, admitsDeal bool, edit func(map[string]any)) []byte {
	t.Helper()
	nonce, err := xdealv1.NewNonce()
	require.NoError(t, err)
	pack := strings.Repeat("a", 64)
	out := map[string]any{
		"verdict": verdict, "tier": "standard",
		"rows": []any{map[string]any{"row": "paid_as_agreed"}}, "pack": map[string]any{"id": "baseline", "digest": pack},
		"admit": map[string]any{
			"outcome": verdict, "tier": "standard", "rows": []string{"paid_as_agreed"}, "asks": []any{},
			"pack_digest": pack, "card_digest": strings.Repeat("b", 64), "answers": []any{}, "nonce": nonce,
		},
		"admits_deal": admitsDeal,
	}
	if edit != nil {
		edit(out)
	}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	return raw
}

func (p *side) admit(dealID string) string {
	p.t.Helper()
	a, err := ParseAdmission(engineOutput(p.t, "GO", true, nil), p.now)
	require.NoError(p.t, err)
	digest, err := p.store.Admit(p.t.Context(), dealID, a)
	require.NoError(p.t, err)
	return digest
}

// receiveSent takes a record the other side sent whole.
func (p *side) receiveSent(pair []byte, offer *xdealv1.OfferContext) {
	p.t.Helper()
	sent, err := xdealv1.VerifySent(pair, offer)
	require.NoError(p.t, err)
	res, err := p.shelf.Receive(p.t.Context(), pair, nil)
	require.NoError(p.t, err)
	require.True(p.t, res.Record.Stored || res.Record.Duplicate)
	require.NoError(p.t, p.store.NoteReceived(p.t.Context(), Received{Sent: &sent, Pair: pair}))
}

// receiveAgreement takes the other side's agreement with its stamp proof.
func (p *side) receiveAgreement(ag Agreement) {
	p.t.Helper()
	read, err := xdealv1.VerifyBook(ag.Pair)
	require.NoError(p.t, err)
	proof := ag.StampProof
	res, err := p.shelf.Receive(p.t.Context(), ag.Pair, &proof)
	require.NoError(p.t, err)
	require.True(p.t, res.Record.Stored, "%+v", res.Record)
	require.NoError(p.t, p.store.NoteReceived(p.t.Context(), Received{Book: &read}))
}

func inputReason(t *testing.T, err error) string {
	t.Helper()
	var ie *InputError
	require.True(t, errors.As(err, &ie), "want an InputError, got %v", err)
	return ie.Error()
}

// handshake runs a whole deal.open: the buyer offers to the seller, the
// seller admits and agrees, and the buyer, holding the seller's agreement,
// admits and agrees.
func handshake(t *testing.T) (buyer, seller *side, offer Sent, sa, ba Agreement) {
	t.Helper()
	ctx := t.Context()
	buyer, seller = newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	var err error
	offer, err = buyer.store.Offer(ctx, offerTo(t, &sk), "offer-1")
	require.NoError(t, err)
	require.NoError(t, buyer.store.MarkWritten(ctx, offer))
	seller.receiveSent(offer.Pair, nil)

	seller.admit(offer.DealID)
	sa, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)

	buyer.receiveAgreement(sa)
	buyer.admit(offer.DealID)
	ba, err = buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	return buyer, seller, offer, sa, ba
}

func TestDealOpenEndToEnd(t *testing.T) {
	ctx := t.Context()
	buyer, seller, offer, sa, ba := handshake(t)

	assert.Equal(t, xdealv1.SideAcceptor, sa.Side)
	assert.Equal(t, xdealv1.SideOfferer, ba.Side)
	assert.False(t, sa.NoLayer)
	assert.NotEmpty(t, sa.StampProof.Fact, "the agreement comes with its stamp proof")

	for _, p := range []*side{buyer, seller} {
		d, err := p.store.Deal(ctx, offer.DealID)
		require.NoError(t, err)
		require.NotNil(t, d)
		assert.Equal(t, "agreed", d.State, p.name)
		book, err := p.store.OwnBook(ctx, d)
		require.NoError(t, err)
		require.Len(t, book, 1, "the agreement is the book's first record")
		ag, ok := agreeOf(book[0])
		require.True(t, ok)
		assert.Equal(t, offer.Digest, ag.Offer.Digest)
		assert.Equal(t, testCadenceLog, ag.ShelfLogID)
	}
	// The offerer's agreement names the acceptor's.
	sellerRead, err := xdealv1.VerifyBook(sa.Pair)
	require.NoError(t, err)
	buyerRead, err := xdealv1.VerifyBook(ba.Pair)
	require.NoError(t, err)
	require.NotNil(t, buyerRead.Record.Block.Seen.V)
	assert.Equal(t, sellerRead.RecordID, *buyerRead.Record.Block.Seen.V)

	// Agreeing again returns the same agreement and seals nothing.
	again, err := buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	assert.Equal(t, ba.Digest, again.Digest)
	assert.Equal(t, ba.Pair, again.Pair)

	// The status provider's offer pair is the pair sent, on both sides.
	for _, p := range []*side{buyer, seller} {
		pair, err := p.store.OfferPair(ctx, offer.Digest)
		require.NoError(t, err)
		assert.Equal(t, offer.Pair, pair, p.name)
	}
}

func TestOfferIsSealedOnceForARequest(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	sk := newSide(t, "seller").keyID()
	first, err := buyer.store.Offer(ctx, offerTo(t, &sk), "same")
	require.NoError(t, err)
	assert.False(t, first.Resumed)
	assert.Regexp(t, `^deal-[0-9a-f]{16}$`, first.DealID)

	// Before it is delivered, the same request returns the same offer, even
	// on a later clock.
	buyer.tick()
	again, err := buyer.store.Offer(ctx, offerTo(t, &sk), "same")
	require.NoError(t, err)
	assert.True(t, again.Resumed)
	assert.Equal(t, first.Digest, again.Digest)
	assert.Equal(t, first.Pair, again.Pair)

	// Once delivered, the same request makes a new offer under a new deal id.
	require.NoError(t, buyer.store.MarkWritten(ctx, again))
	next, err := buyer.store.Offer(ctx, offerTo(t, &sk), "same")
	require.NoError(t, err)
	assert.NotEqual(t, first.DealID, next.DealID)

	d, err := buyer.store.Deal(ctx, first.DealID)
	require.NoError(t, err)
	assert.Equal(t, "offered", d.State)
	assert.Equal(t, xdealv1.SideOfferer, d.Side)

	// An offer that fails the profile's checks is refused before anything is
	// sealed.
	bad := offerTo(t, &sk)
	bad.Role = "broker"
	_, err = buyer.store.Offer(ctx, bad, "bad")
	assert.Contains(t, inputReason(t, err), "the offer does not pass the x-deal-v1 checks")
}

func TestCancelOffer(t *testing.T) {
	ctx := t.Context()
	buyer, seller := newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)

	_, err = buyer.store.CancelOffer(ctx, strings.Repeat("0", 64), "c0")
	assert.Contains(t, inputReason(t, err), "this profile wrote no offer with the digest")

	c, err := buyer.store.CancelOffer(ctx, offer.Digest, "c")
	require.NoError(t, err)
	again, err := buyer.store.CancelOffer(ctx, offer.Digest, "c")
	require.NoError(t, err)
	assert.Equal(t, c.Digest, again.Digest, "a cancellation not yet delivered is returned again")
	require.NoError(t, buyer.store.MarkWritten(ctx, c))
	_, err = buyer.store.CancelOffer(ctx, offer.Digest, "c")
	assert.Contains(t, inputReason(t, err), "this offer is already cancelled")

	d, err := buyer.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", d.State)
	_, err = buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "this offer is cancelled")

	// The seller holding the offer takes the cancellation and the deal ends
	// there too.
	seller.receiveSent(offer.Pair, nil)
	held, err := seller.store.HeldOffer(ctx, offer.Digest)
	require.NoError(t, err)
	seller.receiveSent(c.Pair, &xdealv1.OfferContext{Offer: held.Record, Signer: held.Signer})
	sd, err := seller.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", sd.State)
}

func TestDeclineOffer(t *testing.T) {
	ctx := t.Context()
	buyer, seller := newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)

	_, err = seller.store.DeclineOffer(ctx, offer.Digest, "", "d")
	assert.Contains(t, inputReason(t, err), "receive it first")
	_, err = buyer.store.DeclineOffer(ctx, offer.Digest, "", "d")
	assert.Contains(t, inputReason(t, err), "you wrote this offer")
	_, err = seller.store.DeclineOffer(ctx, offer.Digest, "bored", "d")
	var ie *InputError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, "reason", ie.Field)

	seller.receiveSent(offer.Pair, nil)
	seller.admit(offer.DealID)
	dec, err := seller.store.DeclineOffer(ctx, offer.Digest, xdealv1.ReasonNotSel, "d")
	require.NoError(t, err)
	// A decline sealed and not delivered is returned as it was sealed.
	again, err := seller.store.DeclineOffer(ctx, offer.Digest, xdealv1.ReasonOther, "d")
	require.NoError(t, err)
	assert.True(t, again.Resumed)
	assert.Equal(t, dec.Payload, again.Payload)

	// A declined offer has no deal to agree to.
	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "this offer is declined")

	require.NoError(t, seller.store.MarkWritten(ctx, again))
	_, err = seller.store.DeclineOffer(ctx, offer.Digest, "", "d")
	assert.Contains(t, inputReason(t, err), "already declined")

	// The offerer takes the decline: its offer is declined.
	held, err := buyer.store.HeldOffer(ctx, offer.Digest)
	require.NoError(t, err)
	buyer.receiveSent(dec.Pair, &xdealv1.OfferContext{Offer: held.Record, Signer: held.Signer})
	d, err := buyer.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	assert.Equal(t, "declined", d.State)
}

func TestDeclineOfferRefusesOnceAgreed(t *testing.T) {
	_, seller, offer, _, _ := handshake(t)
	_, err := seller.store.DeclineOffer(t.Context(), offer.Digest, "", "late")
	assert.Contains(t, inputReason(t, err), "you agreed to this offer")
}

func TestParseAdmissionRefusesWhatAnAgreementCannotCite(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, want string
		raw        []byte
	}{
		{"not json", "is not the JSON output", []byte("{")},
		{"no admit body", "has no admit body", []byte(`{"verdict":"GO"}`)},
		{"no tier", "has no tier (a NO)", engineOutput(t, "NO", false, func(m map[string]any) { m["admit"] = nil })},
		{"not admitted", "does not admit the deal", engineOutput(t, "ASK", false, nil)},
		{"body differs", "does not match the decision it came with", engineOutput(t, "GO", true, func(m map[string]any) { m["tier"] = "premium" })},
		{"unknown member", "is not an x-deal-v1 admit body", engineOutput(t, "GO", true, func(m map[string]any) {
			m["admit"].(map[string]any)["extra"] = 1
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAdmission(tc.raw, at)
			assert.Contains(t, inputReason(t, err), tc.want)
		})
	}
}

func TestAdmit(t *testing.T) {
	ctx := t.Context()
	buyer, seller := newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	seller.receiveSent(offer.Pair, nil)

	raw := engineOutput(t, "GO", true, nil)
	a, err := ParseAdmission(raw, seller.now)
	require.NoError(t, err)
	_, err = seller.store.Admit(ctx, "deal-0000000000000000", a)
	assert.Contains(t, inputReason(t, err), "make or receive its offer first")

	first, err := seller.store.Admit(ctx, offer.DealID, a)
	require.NoError(t, err)
	// The same decision again, at a later time, is the same record.
	seller.tick()
	same, err := ParseAdmission(raw, seller.now)
	require.NoError(t, err)
	again, err := seller.store.Admit(ctx, offer.DealID, same)
	require.NoError(t, err)
	assert.Equal(t, first, again)
	own, err := seller.store.dealAdmit(ctx, offer.DealID)
	require.NoError(t, err)
	assert.Equal(t, first, own)

	// Another decision for the deal is a conflict.
	other, err := ParseAdmission(engineOutput(t, "GO", true, nil), seller.now)
	require.NoError(t, err)
	_, err = seller.store.Admit(ctx, offer.DealID, other)
	var ce *ConflictError
	require.ErrorAs(t, err, &ce)

	// Agreed, the decision cannot change.
	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	_, err = seller.store.Admit(ctx, offer.DealID, same)
	assert.Contains(t, inputReason(t, err), "is already agreed")
}

func TestAgreeRefusals(t *testing.T) {
	ctx := t.Context()
	buyer, seller, other := newSide(t, "buyer"), newSide(t, "seller"), newSide(t, "other")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)

	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "holds no offer with the digest")

	// The offerer agrees only once it holds the acceptor's agreement.
	buyer.admit(offer.DealID)
	_, err = buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "the acceptor's agreement has not been received")

	// An offer to another key is not agreed to.
	other.receiveSent(offer.Pair, nil)
	other.admit(offer.DealID)
	_, err = other.store.Agree(ctx, other.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "this offer was sent to another key")

	// No admission decision: the agreement has nothing to cite.
	seller.receiveSent(offer.Pair, nil)
	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "has no admission decision")

	// An admit or an intent that is not in the journal, or is another type.
	seller.admit(offer.DealID)
	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{Intent: strings.Repeat("1", 64)})
	var ie *InputError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, "intent", ie.Field)
	assert.Contains(t, ie.Reason, "is not a record in your journal")
	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{Admit: strings.Repeat("2", 64)})
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, "admit", ie.Field)
	assert.Contains(t, ie.Reason, "is not this deal's admission decision")
}

func TestAnOfferToAPartyWithNoLayer(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	offer, err := buyer.store.Offer(ctx, offerTo(t, nil), "o")
	require.NoError(t, err)
	buyer.admit(offer.DealID)
	ag, err := buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)
	assert.True(t, ag.NoLayer)
	read, err := xdealv1.VerifyBook(ag.Pair)
	require.NoError(t, err)
	assert.Nil(t, read.Record.Block.Seen.V, "seen is null throughout a deal with no layer")
}

func TestSealInBookRetriesATakenPlace(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	offer, err := buyer.store.Offer(ctx, offerTo(t, nil), "o")
	require.NoError(t, err)
	buyer.admit(offer.DealID)
	_, err = buyer.store.Agree(ctx, buyer.cadence, offer.Digest, AgreeOptions{})
	require.NoError(t, err)

	// A statement whose place another write takes once is built again.
	d, err := buyer.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	statement := xdealv1.StatementBody{Statements: []xdealv1.StatementItem{{Row: "paid_as_agreed", Value: "met"}}}
	taken := false
	bookAppendHook = func() {
		if taken {
			return
		}
		taken = true
		_, err := buyer.store.SealInBook(ctx, offer.DealID, BookRecord{Body: statement})
		require.NoError(t, err)
	}
	t.Cleanup(func() { bookAppendHook = func() {} })
	got, err := buyer.store.SealInBook(ctx, offer.DealID, BookRecord{Body: statement})
	require.NoError(t, err)
	assert.Equal(t, uint64(3), got.Seq)
	book, err := buyer.store.OwnBook(ctx, d)
	require.NoError(t, err)
	assert.Len(t, book, 3)

	// With no try left the writer reports a conflict, and seals nothing.
	bookAppendTries = 1
	taken = false
	t.Cleanup(func() { bookAppendTries = 8 })
	_, err = buyer.store.SealInBook(ctx, offer.DealID, BookRecord{Body: statement})
	var ce *ConflictError
	require.ErrorAs(t, err, &ce)
	assert.Contains(t, ce.Reason, "kept changing")

	// A record other than an agreement does not open a book.
	other, err := buyer.store.Offer(ctx, offerTo(t, nil), "o2")
	require.NoError(t, err)
	_, err = buyer.store.SealInBook(ctx, other.DealID, BookRecord{Body: statement})
	assert.Contains(t, inputReason(t, err), "has no deal book yet")
}

func TestAStoppedSendIsFinishedOnce(t *testing.T) {
	ctx := t.Context()
	buyer := newSide(t, "buyer")
	sk := newSide(t, "seller").keyID()
	real := sealSent
	t.Cleanup(func() { sealSent = real })
	sealSent = func(context.Context, *evidencebook.Shelf, evidencebook.Entry, string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		return evidencebook.Record{}, evidencebook.SentCapsule{}, errors.New("the disk is full")
	}
	_, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.ErrorContains(t, err, "the disk is full")
	sealSent = real
	buyer.tick()
	got, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	assert.True(t, got.Resumed, "the offer the stopped run began is the one sealed")
	var n int
	require.NoError(t, buyer.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deal_sent`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestNoteReceivedStopsBeforeCommit(t *testing.T) {
	ctx := t.Context()
	buyer, seller := newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	sent, err := xdealv1.VerifySent(offer.Pair, nil)
	require.NoError(t, err)
	noteStops = func() error { return errors.New("stopped") }
	t.Cleanup(func() { noteStops = func() error { return nil } })
	require.Error(t, seller.store.NoteReceived(ctx, Received{Sent: &sent, Pair: offer.Pair}))
	d, err := seller.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	assert.Nil(t, d, "nothing of a stopped note is kept")
	noteStops = func() error { return nil }
	require.NoError(t, seller.store.NoteReceived(ctx, Received{Sent: &sent, Pair: offer.Pair}))
	d, err = seller.store.Deal(ctx, offer.DealID)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, "offer_received", d.State)
	assert.Equal(t, xdealv1.SideAcceptor, d.Side)
	assert.Equal(t, xdealv1.RoleSeller, d.Role)
}

func TestAStoppedDeclineExcludesAnAgreement(t *testing.T) {
	ctx := t.Context()
	buyer, seller := newSide(t, "buyer"), newSide(t, "seller")
	sk := seller.keyID()
	offer, err := buyer.store.Offer(ctx, offerTo(t, &sk), "o")
	require.NoError(t, err)
	seller.receiveSent(offer.Pair, nil)
	seller.admit(offer.DealID)
	real := sealSent
	t.Cleanup(func() { sealSent = real })
	sealSent = func(context.Context, *evidencebook.Shelf, evidencebook.Entry, string) (evidencebook.Record, evidencebook.SentCapsule, error) {
		return evidencebook.Record{}, evidencebook.SentCapsule{}, errors.New("stopped")
	}
	_, err = seller.store.DeclineOffer(ctx, offer.Digest, "", "d")
	require.ErrorContains(t, err, "stopped")
	sealSent = real
	_, err = seller.store.Agree(ctx, seller.cadence, offer.Digest, AgreeOptions{})
	assert.Contains(t, inputReason(t, err), "you began to decline this offer")
	// Running the decline again finishes it.
	dec, err := seller.store.DeclineOffer(ctx, offer.Digest, "", "d")
	require.NoError(t, err)
	assert.True(t, dec.Resumed)
}
