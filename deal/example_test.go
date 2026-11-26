// SPDX-License-Identifier: Apache-2.0

package deal_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/deal"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	_ "modernc.org/sqlite"
)

// party is one side of a deal: its shelf, its cadence and its deal store,
// on a clock the example sets.
type party struct {
	key     ed25519.PrivateKey
	shelf   *evidencebook.Shelf
	cadence *evidencebook.Cadence
	store   *deal.Store
	db      *sql.DB
}

func keyOf(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("deal example\x00" + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

// openParty opens a party's shelf in dir, with the x-deal-v1 receive
// profile, and its deal store on the shelf's own file.
func openParty(ctx context.Context, dir, name string, now func() time.Time) (*party, error) {
	p := &party{key: keyOf(name + " record key")}
	signer, err := evidencebook.NewEd25519Signer(p.key)
	if err != nil {
		return nil, err
	}
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "x-deal-v1", signer.PublicKey())
	if err != nil {
		return nil, err
	}
	checkpointKey := keyOf(name + " checkpoint key")
	profile := &xdealv1.ReceiveProfile{}
	path := filepath.Join(dir, "shelf.db")
	if p.shelf, err = evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: path, Operator: name, Signer: signer, CheckpointKey: checkpointKey, Originals: originals, Now: now,
		Receive:              evidencebook.ReceiveConfig{Profile: profile},
		AuthenticatedIndexes: func(string) []evidencebook.AuthenticatedIndex { return []evidencebook.AuthenticatedIndex{} },
	}); err != nil {
		return nil, err
	}
	profile.Shelf = p.shelf
	if p.db, err = sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"); err != nil {
		return nil, err
	}
	const logID = "deal-cadence/0123456789abcdef"
	if p.store, err = deal.Open(ctx, deal.Config{
		Shelf: p.shelf, DB: p.db, SigningKey: p.key.Public().(ed25519.PublicKey),
		CheckpointKey: checkpointKey.Public().(ed25519.PublicKey), ShelfLogID: logID, Now: now, ReceiveProfile: profile,
	}); err != nil {
		return nil, err
	}
	if p.cadence, err = p.shelf.Cadence(ctx, evidencebook.CadenceConfig{LogID: logID,
		Ended:  func(context.Context, string) (bool, error) { return false, nil },
		Status: &xdealv1.StatusProvider{Shelf: p.shelf, OfferPair: p.store.OfferPair}}); err != nil {
		return nil, err
	}
	// The journal and the deal books need a shelf that keeps a log of book
	// facts: a first tick, then the migration.
	if _, err = p.cadence.Tick(ctx, now()); err != nil {
		return nil, err
	}
	return p, p.cadence.Migrate(ctx, now())
}

func (p *party) close() { _ = p.db.Close(); _ = p.shelf.Release() }

// admit seals the party's admission decision for the deal: what
// `capsule-engine admission evaluate --admit FILE` printed (a GO).
func (p *party) admit(ctx context.Context, dealID string, now time.Time) error {
	nonce := strings.Repeat("1", 64)
	if p.key[0]%2 == 0 {
		nonce = strings.Repeat("2", 64)
	}
	out := `{"verdict": "GO", "tier": "standard", "rows": [{"row": "delivered"}], "pack": {"id": "baseline", "digest": "` + strings.Repeat("a", 64) + `"},
  "admit": {"outcome": "GO", "tier": "standard", "rows": ["delivered"], "asks": [], "pack_digest": "` + strings.Repeat("a", 64) + `",
    "card_digest": "` + strings.Repeat("b", 64) + `", "answers": [], "nonce": "` + nonce + `"}, "admits_deal": true}`
	a, err := deal.ParseAdmission([]byte(out), now)
	if err != nil {
		return err
	}
	_, err = p.store.Admit(ctx, dealID, a)
	return err
}

// Example_aDealFromOfferToStatus opens an x-deal-v1 deal and seals an act
// with this module alone: the buyer offers, each side admits and agrees,
// the seller checks its delivery, the person approves it, the seller
// performs it, and both read the deal's status.
func Example_aDealFromOfferToStatus() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "deal-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	clock := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	tick := func() { clock = clock.Add(time.Minute) }
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	open := func(name string) *party {
		must(os.MkdirAll(filepath.Join(dir, name), 0o700))
		p, err := openParty(ctx, filepath.Join(dir, name), name, now)
		must(err)
		tick()
		return p
	}
	buyer, seller := open("buyer"), open("seller")
	defer buyer.close()
	defer seller.close()

	// deal.open: the buyer offers one delivery row to the seller's key.
	sellerKey := hex.EncodeToString(seller.key.Public().(ed25519.PublicKey))
	var offer xdealv1.ProposeBody
	must(json.Unmarshal([]byte(`{"role": "buyer", "to": "`+sellerKey+`", "terms": {"item": "lamp", "currency": "USD"},
  "pack_digest": "`+strings.Repeat("a", 64)+`", "agree_by": "2026-11-02T10:00:00Z",
  "terms_rows": {"rows": [{"id": "delivered", "owed_by": "seller", "owed": {"item": "lamp"},
    "due": {"at": "2026-11-03T23:59:59Z"}, "window_end": {"at": "2026-11-06T23:59:59Z"}}], "named": {}},
  "adjudicator": null, "forums": [], "witnesses": []}`), &offer))
	sent, err := buyer.store.Offer(ctx, offer, "offer-1")
	must(err)
	must(buyer.store.MarkWritten(ctx, sent))
	fmt.Println("offer sealed:", sent.DealID != "")

	// The seller receives the offer, admits the deal and agrees.
	read, err := xdealv1.VerifySent(sent.Pair, nil)
	must(err)
	_, err = seller.shelf.Receive(ctx, sent.Pair, nil)
	must(err)
	must(seller.store.NoteReceived(ctx, deal.Received{Sent: &read, Pair: sent.Pair}))
	must(seller.admit(ctx, sent.DealID, now()))
	tick()
	sa, err := seller.store.Agree(ctx, seller.cadence, sent.Digest, deal.AgreeOptions{})
	must(err)
	fmt.Println("seller agreed as the", sa.Side)

	// The buyer takes the seller's agreement with its stamp proof, admits
	// and agrees; the seller takes the buyer's.
	take := func(p *party, ag deal.Agreement) {
		book, err := xdealv1.VerifyBook(ag.Pair)
		must(err)
		proof := ag.StampProof
		_, err = p.shelf.Receive(ctx, ag.Pair, &proof)
		must(err)
		must(p.store.NoteReceived(ctx, deal.Received{Book: &book}))
	}
	take(buyer, sa)
	must(buyer.admit(ctx, sent.DealID, now()))
	tick()
	ba, err := buyer.store.Agree(ctx, buyer.cadence, sent.Digest, deal.AgreeOptions{})
	must(err)
	fmt.Println("buyer agreed as the", ba.Side)
	take(seller, ba)

	// deal.check: the seller's own evaluation of its delivery, sealed with
	// its verdict; the person's answer to that verdict, sealed after it.
	tick()
	card := strings.Repeat("c", 64)
	checked, err := seller.store.Check(ctx, sent.DealID, deal.CheckIn{
		Check: xdealv1.CheckBody{CheckBody: dealprofile.CheckBody{Action: "commit"}},
		Verdict: xdealv1.VerdictBody{VerdictBody: dealprofile.VerdictBody{Result: "pass", Differences: []dealprofile.Difference{}, Options: []string{},
			CardCommitment: card, Rules: &dealprofile.VerdictBodyRules{Status: "not_configured"}}},
	})
	must(err)
	tick()
	verdict := xdealv1.RefTo(checked.Verdict)
	_, err = seller.store.SealJournal(ctx, sent.DealID, deal.JournalStep{Kind: xdealv1.TypeApproval, Build: func(deal.StepFacts) (deal.StepParts, error) {
		return deal.StepParts{Record: &deal.JournalRecord{RecordType: xdealv1.TypeApproval,
			Block: map[string]any{"refs": []any{map[string]any{"rel": "approves", "type": verdict.Type, "digest_alg": verdict.DigestAlg, "digest": verdict.Digest}}},
			Body:  map[string]any{"choice": "proceed", "proceed": true, "approver": "user", "said_commitment": strings.Repeat("d", 64), "card_commitment": card}}}, nil
	}})
	must(err)
	fmt.Println("checked and approved")

	// deal.perform: the delivery, resting on that check.
	tick()
	act, err := seller.store.Perform(ctx, sent.DealID, deal.PerformIn{Check: checked.Check, Act: "deliver", Observed: "carrier_scan",
		Statements: []xdealv1.StatementItem{{Row: "delivered", Value: "met"}}})
	must(err)
	fmt.Println("performed at place", act.Seq, "of the seller's deal book; first act allowed:", act.Gate.Allowed)
	fmt.Println("the person signs before anyone relies on it:", act.Authorization.NeedsPersonSignature)

	// deal.status: the seller's view of its own deal book.
	status, err := seller.store.Status(ctx, sent.DealID)
	must(err)
	fmt.Println("status read by the", status.Role)
	// Output:
	// offer sealed: true
	// seller agreed as the acceptor
	// buyer agreed as the offerer
	// checked and approved
	// performed at place 2 of the seller's deal book; first act allowed: true
	// the person signs before anyone relies on it: true
	// status read by the seller
}
