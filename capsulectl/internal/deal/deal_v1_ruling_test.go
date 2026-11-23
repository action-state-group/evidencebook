package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
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
