package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAdjudicatorSide is a v1 profile that checkpoints with its record key,
// as `deal ruling` needs (PROFILE.md §4, "The case book").
func newAdjudicatorSide(t *testing.T) v1Side {
	t.Helper()
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Checkpoint.Signing = p.Signing
	require.NoError(t, cli.SaveProfile(p, true))
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	return v1Side{party: thisDealParty(t)}
}

// adjudicatedDeal is an agreed v1 deal whose offer names adj as its
// adjudicator, with the forum auto:twin.
func adjudicatedDeal(t *testing.T) (*handshakeV1, v1Side) {
	t.Helper()
	adj := newAdjudicatorSide(t)
	h := agreedBoth(t, func(key string) string {
		return strings.Replace(disputeOffer(key, "auto:twin"), `"key_id": "adjudicator-key"`, `"key_id": "`+adj.party.signer+`"`, 1)
	})
	return h, adj
}

// adjudicatorCopy has side write its adjudicator copy of the deal, after a
// tick so the book's checkpoint covers its records.
func adjudicatorCopy(t *testing.T, side v1Side, dealID string) string {
	t.Helper()
	side.act(t)
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	file := filepath.Join(t.TempDir(), "copy.json")
	dealRun(t, "report", "--deal", dealID, "--share", "adjudicator", "--to", "Lamp Court", "--bundle", file)
	return file
}

// A dispute is ruled by the adjudicator the agreement names, the ruling is
// entered in both parties' books by deal receive, and its stamp proof is
// kept; the same ruling again seals nothing; a different final ruling on the
// row is refused.
func TestARulingIsSealedAndEnteredInBothBooks(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)
	sellerCopy := adjudicatorCopy(t, h.seller, h.dealID)

	adj.act(t)
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	args := []string{"ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerCopy, "--copy", sellerCopy}
	out := dealRun(t, append(args, "--out", rulingFile)...)
	assert.Equal(t, true, out["sealed"])
	assert.Equal(t, float64(1), out["seq"])
	push := readPush(t, rulingFile)
	require.NotNil(t, push.StampProof, "the ruling travels with its case book's stamp proof")
	read, err := xdealv1.VerifyBook(push.Record)
	require.NoError(t, err)
	rb, ok := read.Record.Body.(*xdealv1.RulingBody)
	require.True(t, ok)
	assert.Equal(t, h.buyer.party.signer, rb.Keys.Buyer)
	assert.Equal(t, h.seller.party.signer, rb.Keys.Seller)
	assert.Equal(t, "auto:twin", rb.Forum, "the dispute's forum")
	require.NotNil(t, rb.Read.Buyer.V)
	require.NotNil(t, rb.Read.Seller.V)
	assert.True(t, strings.HasPrefix(out["case_log"].(string), xdealv1.CaseLogPrefix))

	// Both parties enter it.
	for _, side := range []v1Side{h.buyer, h.seller} {
		side.act(t)
		got := dealRun(t, "receive", rulingFile)
		assert.Equal(t, "kept, and counted", got["record"], "%v", got)
		assert.Equal(t, out["ruling"], got["ruling"], "receive prints the digest a challenge takes")
		assert.NotContains(t, got["stamp_proof"], "refused", "%v", got)
		withSession(t, func(s *dealSession) {
			d, err := s.v1DealOf(t.Context(), h.dealID)
			require.NoError(t, err)
			book, err := s.ownBookV1(t.Context(), d)
			require.NoError(t, err)
			last, ok := book[len(book)-1].Body.(*xdealv1.StatementBody)
			require.True(t, ok, "the receive named the ruling in the party's book")
			require.NotNil(t, last.Names)
			assert.Equal(t, out["ruling"], last.Names.Ruling.Digest)
		})
	}

	// The same ruling again: the same record, nothing sealed.
	adj.act(t)
	rerun := dealRun(t, append(args, "--out", filepath.Join(t.TempDir(), "again.json"))...)
	assert.Equal(t, false, rerun["sealed"])
	assert.Equal(t, out["ruling"], rerun["ruling"])

	// A different final ruling on the row is refused.
	other := append([]string{}, args...)
	other[6] = "met"
	other[8] = "buyer"
	assert.Contains(t, v1Err(t, other...), "already has your final ruling")
}

// The refusals: the profile does not checkpoint with its record key; the
// dispute names another row; a copy of another side twice; one copy only is
// enough when its agreement names the other side's key.
func TestARulingRefusals(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)

	adj.act(t)
	base := []string{"ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "met", "--against", "none", "--dispute", dispute}
	assert.Contains(t, v1Err(t, "ruling", "--deal", h.dealID, "--row", "other_row", "--value", "met", "--against", "none", "--dispute", dispute, "--copy", buyerCopy), "the dispute is on row paid_as_agreed")
	assert.Contains(t, v1Err(t, append(base, "--copy", buyerCopy, "--copy", buyerCopy)...), "both copies are the buyer's")
	assert.Contains(t, v1Err(t, base...), "give one or two --copy files")
	assert.Contains(t, v1Err(t, append(base, "--copy", buyerCopy, "--remedy-due", "2026-12-01T00:00:00Z")...), "go with --remedy-kind")

	one := dealRun(t, append(base, "--copy", buyerCopy, "--out", filepath.Join(t.TempDir(), "r.json"))...)
	assert.Equal(t, true, one["sealed"], "one copy is enough: the buyer's agreement names the seller's key")

	// An --out that holds another record is refused before anything is
	// sealed: the case book does not grow.
	other := filepath.Join(t.TempDir(), "other.json")
	require.NoError(t, os.WriteFile(other, []byte(`{"record":{}}`), 0o600))
	assert.Contains(t, v1Err(t, append(base, "--copy", buyerCopy, "--value", "not_met", "--out", other)...), "holds another record")
	// The refused ruling was not sealed: run with a new --out, it is sealed
	// now, as the case book's second ruling.
	notMet := dealRun(t, append(base, "--copy", buyerCopy, "--value", "not_met", "--out", filepath.Join(t.TempDir(), "r2.json"))...)
	assert.Equal(t, true, notMet["sealed"], "the refused run sealed nothing")
	assert.Equal(t, float64(2), notMet["seq"])

	// The seller's profile checkpoints with a key of its own, so it cannot
	// rule.
	h.seller.act(t)
	assert.Contains(t, v1Err(t, append(base, "--copy", buyerCopy)...), "checkpoints with its record key")
	// Switching that profile's checkpoint key to its record key afterwards
	// does not help: its store checkpointed with the old key, so the ruling
	// is refused before anything is sealed, with the way that works
	// (evidencebook#814).
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Checkpoint.Signing = p.Signing
	require.NoError(t, cli.SaveProfile(p, true))
	msg := v1Err(t, append(base, "--copy", buyerCopy)...)
	assert.Contains(t, msg, "checkpointed with another key")
	assert.Contains(t, msg, "deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY")
	withSession(t, func(s *dealSession) {
		books, err := s.shelf.Books(t.Context())
		require.NoError(t, err)
		for _, b := range books {
			assert.False(t, strings.HasPrefix(b, "case/"), "no case book was opened: %s", b)
		}
	})
}

// A party challenges a ruling it holds: a dispute of kind challenge naming
// the ruling and the SHA-256 of the grounds, sent like a dispute; the same
// challenge again seals nothing; a ruling it does not hold is unavailable.
func TestAChallengeOfAHeldRuling(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)
	adj.act(t)
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	ruled := dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerCopy, "--out", rulingFile)
	digest := ruled["ruling"].(string)

	h.seller.act(t)
	grounds := filepath.Join(t.TempDir(), "grounds.txt")
	require.NoError(t, os.WriteFile(grounds, []byte("the parcel was delivered on time"), 0o600))
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--challenge", digest, "--grounds", grounds), "unavailable")
	naming := filepath.Join(t.TempDir(), "naming.json")
	got := dealRun(t, "receive", rulingFile, "--out", naming)
	assert.Equal(t, naming, got["file"], "the statement naming the ruling is written to send")

	file := filepath.Join(t.TempDir(), "challenge.json")
	out := dealRun(t, "dispute", "--deal", h.dealID, "--challenge", digest, "--grounds", grounds, "--out", file)
	assert.Equal(t, xdealv1.KindChallenge, out["kind"])
	read, err := xdealv1.VerifyBook(readPush(t, file).Record)
	require.NoError(t, err)
	body, ok := disputeOf(read.Record)
	require.True(t, ok)
	require.NotNil(t, body.Ruling)
	assert.Equal(t, digest, body.Ruling.Digest)
	sum := sha256.Sum256([]byte("the parcel was delivered on time"))
	assert.Equal(t, hex.EncodeToString(sum[:]), body.GroundsCommitment)

	again := dealRun(t, "dispute", "--deal", h.dealID, "--challenge", digest, "--grounds", grounds, "--out", file)
	assert.Equal(t, out["dispute"], again["dispute"], "the same challenge is written again, not sealed twice")

	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--challenge", digest, "--grounds", grounds, "--row", "paid_as_agreed"), "takes no --row")
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--challenge", digest), "needs --grounds")
	assert.Contains(t, v1Err(t, "dispute", "--deal", h.dealID, "--challenge", "abc", "--grounds", grounds), "64 lowercase hex")

	// The buyer, the other side, takes the seller's naming statement first,
	// then counts the challenge after it.
	h.buyer.act(t)
	assert.Equal(t, "kept, and counted", dealRun(t, "receive", naming)["record"])
	assert.Equal(t, "kept, and counted", dealRun(t, "receive", file)["record"])
	// --out goes with a ruling only.
	assert.Contains(t, v1Err(t, "receive", file, "--out", filepath.Join(t.TempDir(), "x.json")), "--out goes with a ruling")
}

// The whole flow: a dispute, a ruling entered in both books, and the
// adjudicator's report of each party's entry from its copy; copies taken
// before the ruling show nothing, since no witness timed it; a party takes
// the other side's copy, ruling included.
func TestRulingEntriesFromBothCopies(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerBefore := adjudicatorCopy(t, h.buyer, h.dealID)
	sellerBefore := adjudicatorCopy(t, h.seller, h.dealID)

	adj.act(t)
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	ruled := dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerBefore, "--copy", sellerBefore, "--out", rulingFile)
	digest := ruled["ruling"].(string)
	before := dealRun(t, "ruling", "entries", "--deal", h.dealID, "--ruling", digest, "--copy", buyerBefore, "--copy", sellerBefore)
	for _, e := range before["entries"].([]any) {
		row := e.(map[string]any)
		assert.Equal(t, "not shown entered (the ruling has no witnessed time)", row["words"], "%v", row)
		assert.Contains(t, row["sent"], "(adjudicator's statement)")
	}

	for _, side := range []v1Side{h.buyer, h.seller} {
		side.act(t)
		dealRun(t, "receive", rulingFile)
	}
	buyerAfter := adjudicatorCopy(t, h.buyer, h.dealID)
	buyerProof := filepath.Join(t.TempDir(), "buyer-proof.json")
	proofOut := dealRun(t, "checkpoint", "proof", "--deal", h.dealID, "--copy", buyerAfter, "--out", buyerProof)
	assert.Equal(t, float64(0), proofOut["witness_receipts"], "no witness is configured in these tests")
	again := dealRun(t, "checkpoint", "proof", "--deal", h.dealID, "--copy", buyerAfter, "--out", buyerProof)
	assert.Equal(t, buyerProof, again["file"], "the same file again is a re-run")
	sellerAfter := adjudicatorCopy(t, h.seller, h.dealID)
	assert.Contains(t, v1Err(t, "checkpoint", "proof", "--deal", h.dealID, "--copy", buyerAfter, "--out", filepath.Join(t.TempDir(), "x.json")), "not of your deal book")
	adj.act(t)
	after := dealRun(t, "ruling", "entries", "--deal", h.dealID, "--ruling", digest, "--copy", buyerAfter, "--copy", sellerAfter, "--proof", buyerProof)
	parties := map[string]string{}
	for _, e := range after["entries"].([]any) {
		row := e.(map[string]any)
		parties[row["party"].(string)] = row["words"].(string)
	}
	assert.Equal(t, map[string]string{"buyer": "entered, not witnessed", "seller": "entered, not witnessed"}, parties)
	assert.Contains(t, v1Err(t, "ruling", "entries", "--deal", h.dealID, "--ruling", strings.Repeat("a", 64), "--copy", buyerAfter), "holds no ruling")

	// The buyer takes the seller's counterparty copy: the ruling it carries
	// is the one the buyer holds.
	h.seller.act(t)
	cp := filepath.Join(t.TempDir(), "cp.json")
	dealRun(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--bundle", cp)
	h.buyer.act(t)
	got := dealRun(t, "receive", cp)
	found := false
	for _, r := range got["records"].([]any) {
		row := r.(map[string]any)
		if row["ruling"] == digest {
			found = true
			assert.Equal(t, "already kept", row["record"], "%v", row)
		}
	}
	assert.True(t, found, "the copy carries the ruling: %v", got)
}

// A copy's receive refuses --out, and exits non-zero when a ruling the copy
// carries is refused: a fork is what the copy is for, never a quiet success.
func TestReceiveOfACopyReportsARefusedRuling(t *testing.T) {
	h, adj := adjudicatedDeal(t)
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerCopy := adjudicatorCopy(t, h.buyer, h.dealID)
	adj.act(t)
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerCopy, "--out", rulingFile)
	h.seller.act(t)
	dealRun(t, "receive", rulingFile)
	cp := filepath.Join(t.TempDir(), "cp.json")
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	dealRun(t, "report", "--deal", h.dealID, "--share", "counterparty", "--to", "Kettle Shop", "--bundle", cp)

	h.buyer.act(t)
	assert.Contains(t, v1Err(t, "receive", cp, "--out", filepath.Join(t.TempDir(), "x.json")), "goes with a ruling file, not a copy")
	old := receiveFromStore
	t.Cleanup(func() { receiveFromStore = old })
	receiveFromStore = func(ctx context.Context, shelf *evidencebook.Shelf, record []byte, proof *evidencebook.StampProof) (evidencebook.ReceiveResult, error) {
		if read, err := xdealv1.VerifyBook(record); err == nil && read.Record.Block.RecordType == xdealv1.TypeRuling {
			return evidencebook.ReceiveResult{}, fmt.Errorf("%w: two different rulings at one place", evidencebook.ErrRefused)
		}
		return old(ctx, shelf, record, proof)
	}
	assert.Contains(t, v1Err(t, "receive", cp), "a ruling the copy carries was refused")
}

// Only a cadence log id of the form a shelf gives is one of the deal's own
// ids: an agreement that names anything else, such as a card number, does
// not take it out of the gate.
func TestV1OwnIDsTakesOnlyACadenceLogID(t *testing.T) {
	assert.True(t, shelfLogIDForm.MatchString("deal-cadence/9261641132323880"))
	for _, v := range []string{"4111111111111111", "deal-cadence/4111", "deal-cadence/9261641132323880x", "case/9261641132323880"} {
		assert.False(t, shelfLogIDForm.MatchString(v), v)
	}
}

// With a witness the agreement names timing the adjudicator's stamp that
// covers the ruling, a party's copy checkpointed after that time and without
// the ruling shows it not entered; a copy from before it shows nothing; and
// a copy with the ruling shows it entered.
func TestRulingEntriesByAWitnessedTime(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyID := hex.EncodeToString(public)
	endpoint, _ := countingWitness(t)
	adj := newAdjudicatorSide(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Checkpoint.Endpoint, p.Checkpoint.PublicKey = endpoint, keyID
	require.NoError(t, cli.SaveProfile(p, true))
	h := agreedBoth(t, func(k string) string {
		o := strings.Replace(disputeOffer(k, "auto:twin"), `"key_id": "adjudicator-key"`, `"key_id": "`+adj.party.signer+`"`, 1)
		return strings.Replace(o, `"witnesses": []`, `"witnesses": [{"key_id": "`+keyID+`"}]`, 1)
	})
	// The seller's shelf is witnessed by the same witness, for its proof.
	h.seller.act(t)
	sp, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	sp.Checkpoint.Endpoint, sp.Checkpoint.PublicKey = endpoint, keyID
	require.NoError(t, cli.SaveProfile(sp, true))
	h.buyer.act(t)
	dispute := filepath.Join(t.TempDir(), "dispute.json")
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--out", dispute)
	buyerBefore := adjudicatorCopy(t, h.buyer, h.dealID)
	sellerBefore := adjudicatorCopy(t, h.seller, h.dealID)

	adj.act(t)
	rulingFile := filepath.Join(t.TempDir(), "ruling.json")
	digest := dealRun(t, "ruling", "--deal", h.dealID, "--row", "paid_as_agreed", "--value", "not_met", "--against", "seller", "--final",
		"--dispute", dispute, "--copy", buyerBefore, "--copy", sellerBefore, "--out", rulingFile)["ruling"].(string)
	clockAt(t, cli.DealClock().Add(time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	iat := cli.DealClock().Add(time.Minute).UTC().Truncate(time.Second)
	witnessLastStamp(t, endpoint, key, iat)

	// Only the seller enters the ruling; the buyer, without it, seals a
	// record after the ruling's witnessed time, so its checkpoint is later.
	h.seller.act(t)
	dealRun(t, "receive", rulingFile)
	h.buyer.act(t)
	dealRun(t, "dispute", "--deal", h.dealID, "--row", "paid_as_agreed", "--withdraw", "--out", filepath.Join(t.TempDir(), "withdraw.json"))
	buyerAfter := adjudicatorCopy(t, h.buyer, h.dealID)
	sellerAfter := adjudicatorCopy(t, h.seller, h.dealID)
	adj.act(t)
	words := func(copies ...string) (map[string]string, map[string]any) {
		args := []string{"ruling", "entries", "--deal", h.dealID, "--ruling", digest}
		for _, c := range copies {
			args = append(args, "--copy", c)
		}
		out := dealRun(t, args...)
		got := map[string]string{}
		for _, e := range out["entries"].([]any) {
			row := e.(map[string]any)
			got[row["party"].(string)] = row["words"].(string)
		}
		return got, out
	}
	got, out := words(buyerBefore, sellerBefore)
	for _, e := range out["entries"].([]any) {
		assert.Equal(t, iat.Format(time.RFC3339), e.(map[string]any)["ruling_witnessed_at"], "the ruling's time is the named witness's")
	}
	assert.Equal(t, map[string]string{
		"buyer":  "not shown entered (no checkpoint after the ruling's witnessed time)",
		"seller": "not shown entered (no checkpoint after the ruling's witnessed time)",
	}, got)
	got, _ = words(buyerAfter, sellerAfter)
	assert.Equal(t, "entered, not witnessed", got["seller"])
	assert.True(t, strings.HasPrefix(got["buyer"], "ruling not entered by the buyer as of its checkpoint at "), got["buyer"])

	// The seller's proof, with the named witness's receipt for the stamp its
	// copy rests on, makes its entry "entered".
	h.seller.act(t)
	sellerProof := filepath.Join(t.TempDir(), "seller-proof.json")
	before := dealRun(t, "checkpoint", "proof", "--deal", h.dealID, "--copy", sellerAfter, "--out", sellerProof)
	assert.Equal(t, float64(0), before["named_witness_receipts"])
	assert.Contains(t, before["note"], "the file changes nothing")
	witnessLastStamp(t, endpoint, key, cli.DealClock().UTC().Truncate(time.Second))
	// A later stamp of the unchanged book that no witness registered, then a
	// tick with no stamp batch and an interrupted tick: the proof is still the
	// registered stamp's.
	clockAt(t, cli.DealClock().Add(72*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	withSession(t, func(s *dealSession) {
		var last, size int64
		var leaves string
		var statement []byte
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT tick, leaves, checkpoint_size, checkpoint FROM cadence_ticks ORDER BY tick DESC LIMIT 1`).Scan(&last, &leaves, &size, &statement))
		at, due := cli.DealClock().UTC().Format(time.RFC3339), cli.DealClock().UTC().Add(time.Hour).Format(time.RFC3339)
		// A finished tick with no stamp batch, as a shelf's ticks from before
		// its stamps are, then an interrupted tick.
		var unstamped map[string]any
		require.NoError(t, json.Unmarshal([]byte(leaves), &unstamped))
		delete(unstamped, "stamp")
		raw, err := json.Marshal(unstamped)
		require.NoError(t, err)
		_, err = s.db.ExecContext(t.Context(), `INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, ?, ?, ?)`,
			last+1, at, due, size, statement, string(raw))
		require.NoError(t, err)
		_, err = s.db.ExecContext(t.Context(), `INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)`,
			last+2, at, due, leaves)
		require.NoError(t, err)
	})
	// The run replaces its own earlier proof file.
	after := dealRun(t, "checkpoint", "proof", "--deal", h.dealID, "--copy", sellerAfter, "--out", sellerProof)
	assert.Equal(t, float64(1), after["named_witness_receipts"])
	assert.NotContains(t, after, "note")
	var written struct {
		StampProof map[string]any `json:"stamp_proof"`
	}
	require.NoError(t, json.Unmarshal(mustRead(t, sellerProof), &written))
	require.NotEmpty(t, written.StampProof)
	assert.NotContains(t, written.StampProof, "status_opening", "the adjudicator's audience gets no status opening")
	notProof := filepath.Join(t.TempDir(), "other.json")
	require.NoError(t, os.WriteFile(notProof, []byte(`{"other": true}`), 0o600))
	assert.Contains(t, v1Err(t, "checkpoint", "proof", "--deal", h.dealID, "--copy", sellerAfter, "--out", notProof), "is not a proof file")
	adj.act(t)
	out = dealRun(t, "ruling", "entries", "--deal", h.dealID, "--ruling", digest, "--copy", buyerAfter, "--copy", sellerAfter, "--proof", sellerProof)
	got = map[string]string{}
	for _, e := range out["entries"].([]any) {
		row := e.(map[string]any)
		got[row["party"].(string)] = row["words"].(string)
	}
	assert.Equal(t, "entered", got["seller"])
	assert.True(t, strings.HasPrefix(got["buyer"], "ruling not entered by the buyer"), got["buyer"])
}

// witnessLastStamp records, for the selected profile's last stamp, a receipt
// from the witness at endpoint signed by key with registration time iat.
func witnessLastStamp(t *testing.T, endpoint string, key ed25519.PrivateKey, iat time.Time) {
	t.Helper()
	withSession(t, func(s *dealSession) {
		c, _, err := s.cadenceOf(t.Context())
		require.NoError(t, err)
		last, ok, err := c.Last(t.Context())
		require.NoError(t, err)
		require.True(t, ok)
		record, err := checkpoint.ParseRecord(last.Checkpoint)
		require.NoError(t, err)
		entry, err := record.EntryHash()
		require.NoError(t, err)
		check, err := c.Witnessed(t.Context(), last.N, evidencebook.WitnessReceipt{TSURL: endpoint, EntryHash: hex.EncodeToString(entry),
			Receipt: mintReceiptAt(t, entry, key, iat), LeafIndex: 0, TreeSize: 1})
		require.NoError(t, err)
		require.Equal(t, evidencebook.WitnessVerified, check.State, check.Reason)
	})
}
