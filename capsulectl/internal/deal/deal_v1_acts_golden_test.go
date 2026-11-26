// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deal-book verbs of evidencebook#899's phase 2, byte for byte:
// perform, statement, dispute (with --withdraw and --challenge) and a payee
// settle. The golden file was written by the plugin before these verbs moved
// onto evidencebook's package deal, and the plugin must still produce it:
// every command's output, error text and exit code, and every file it writes,
// over one run of acts, statements, disputes and payee legs and their
// refusals. The run is deterministic as TestDealOpenGolden's is: a seeded
// global random source and a fixed deal clock.
//
// EB_WRITE_DEAL_ACTS_GOLDEN=1 rewrites the file; only an intended change to
// what these verbs print or seal does that.

const dealActsGolden = "testdata/deal-acts-golden.json"

// actsOffer is the test offer with an adjudicator and two forums, a delivery
// row with a remedy, and a named record each side writes.
func actsOffer(key string) string {
	o := disputeOffer(key, "auto:twin", "external:panel")
	o = strings.Replace(o, `"named": {}`, `"named": {"delivered": {"side": "seller", "latest": "2099-12-01T00:00:00Z"}, "paid": {"side": "buyer", "latest": "2099-12-01T00:00:00Z"}}`, 1)
	return strings.Replace(o, `"window_end": {"at": "2026-11-04T23:59:59Z"}}`, `"window_end": {"at": "2026-11-04T23:59:59Z"}},
      {"id": "item_delivered", "owed_by": "seller", "owed": {"item": "lamp"},
       "due": {"at": "2026-11-03T23:59:59Z"}, "window_end": {"at": "2026-11-06T23:59:59Z"},
       "remedies": [{"id": "replace", "within_seconds": 86400}]}`, 1)
}

func TestDealActsGolden(t *testing.T) {
	cryptotest.SetGlobalRandom(t, 8992)
	g := &goldenRun{t: t, root: filepath.Dir(t.TempDir())}
	seller := newV1Side(t)
	buyer := newV1Side(t)
	g.now = clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))

	// deal.open, both sides agreed and each holding the other's agreement.
	offer := g.file("offer.json")
	res := g.run(buyer, "offer", []string{"offer", "--input", writeJSON(t, actsOffer(seller.party.signer)), "--out", offer}, offer)
	require.NotNil(t, res, "%+v", g.steps)
	dealID, offerDigest := res["deal_id"].(string), res["offer"].(string)
	g.run(seller, "receive the offer", []string{"receive", offer})
	g.run(seller, "admit", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})
	sellerAgree := g.file("seller-agreement.json")
	g.run(seller, "agree", []string{"agree", offerDigest, "--out", sellerAgree}, sellerAgree)

	// Before the offerer agrees, its deal book has no record to add to.
	g.run(buyer, "statement before agreeing", []string{"statement", "--deal", dealID, "--statement", "paid_as_agreed=met"})
	g.run(buyer, "dispute before agreeing", []string{"dispute", "--deal", dealID, "--row", "paid_as_agreed"})

	g.run(buyer, "receive the agreement", []string{"receive", sellerAgree})
	g.run(buyer, "admit", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})
	buyerAgree := g.file("buyer-agreement.json")
	g.run(buyer, "agree", []string{"agree", offerDigest, "--out", buyerAgree}, buyerAgree)

	// The seller's first act waits on the buyer's stamp proof, which comes
	// with the buyer's agreement.
	g.run(seller, "intent", []string{"note", "--deal", dealID, "--kind", "intent", "--input", writeJSON(t, v1Intent), "--opening", writeJSON(t, v1Opening)})
	res = g.run(seller, "commit check", []string{"check", "--deal", dealID, "--input", writeJSON(t, v1SellerCommit)})
	require.NotNil(t, res, "%+v", g.steps)
	checkID, _ := res["check_id"].(string)
	require.NotEmpty(t, checkID, "%v", res)
	g.run(seller, "perform before the proof", []string{"perform", "--deal", dealID, "--check", checkID, "--act", "deliver", "--observed", "carrier_scan", "--out", g.file("early.json")})
	g.run(seller, "receive the buyer's agreement", []string{"receive", buyerAgree})

	// Perform refusals, then the act, then the same act again.
	perform := func(extra ...string) []string {
		return append([]string{"perform", "--deal", dealID, "--check", checkID, "--act", "deliver", "--observed", "carrier_scan"}, extra...)
	}
	g.run(seller, "perform an unknown row", perform("--statement", "nope=met", "--out", g.file("x1.json")))
	g.run(seller, "perform a bad value", perform("--statement", "item_delivered=fine", "--out", g.file("x2.json")))
	g.run(seller, "perform a remedy the row lacks", perform("--statement", "item_delivered=not_met:refund", "--out", g.file("x3.json")))
	g.run(seller, "perform a row twice", perform("--statement", "item_delivered=met", "--statement", "item_delivered=not_met", "--out", g.file("x4.json")))
	g.run(seller, "perform the other side's mark", perform("--mark", "paid", "--out", g.file("x5.json")))
	g.run(seller, "perform an unknown mark", perform("--mark", "shipped", "--out", g.file("x6.json")))
	g.run(seller, "perform a mark twice", perform("--mark", "delivered", "--mark", "delivered", "--out", g.file("x7.json")))
	g.run(seller, "perform an unknown remedy row", perform("--remedy-row", "nope", "--out", g.file("x8.json")))
	g.run(seller, "perform a check not held", []string{"perform", "--deal", dealID, "--check", strings.Repeat("0", 64), "--act", "deliver", "--observed", "carrier_scan", "--out", g.file("x9.json")})
	taken := g.file("taken.json")
	require.NoError(t, os.WriteFile(taken, []byte("{}"), 0o600))
	g.run(seller, "perform to a file that exists", perform("--out", taken))
	performed := g.file("perform.json")
	g.run(seller, "perform", perform("--statement", "item_delivered=met", "--mark", "delivered", "--remedy-row", "item_delivered", "--out", performed), performed)
	g.run(seller, "perform again", perform("--statement", "item_delivered=met", "--mark", "delivered", "--remedy-row", "item_delivered", "--out", performed), performed)
	g.run(seller, "another act on the same check", perform("--act", "redeliver", "--out", g.file("x10.json")))
	g.run(buyer, "receive the perform", []string{"receive", performed})

	// Statements.
	stated := g.file("statement.json")
	g.run(buyer, "statement", []string{"statement", "--deal", dealID, "--statement", "item_delivered=not_met:replace", "--out", stated}, stated)
	g.run(buyer, "statement again", []string{"statement", "--deal", dealID, "--statement", "item_delivered=not_met:replace", "--out", stated}, stated)
	g.run(buyer, "statement to standard output", []string{"statement", "--deal", dealID, "--statement", "paid_as_agreed=cannot_tell"})
	g.run(buyer, "statement an unknown row", []string{"statement", "--deal", dealID, "--statement", "nope=met"})
	g.run(buyer, "statement of an unknown deal", []string{"statement", "--deal", "deal-0000000000000000", "--statement", "paid_as_agreed=met"})

	// Disputes, a withdrawal, and a challenge of a ruling not held.
	g.run(buyer, "dispute with no forum", []string{"dispute", "--deal", dealID, "--row", "item_delivered"})
	g.run(buyer, "dispute an unknown row", []string{"dispute", "--deal", dealID, "--row", "nope", "--forum", "auto:twin"})
	g.run(buyer, "withdraw with none open", []string{"dispute", "--deal", dealID, "--row", "item_delivered", "--withdraw"})
	disputed := g.file("dispute.json")
	g.run(buyer, "dispute", []string{"dispute", "--deal", dealID, "--row", "item_delivered", "--forum", "auto:twin", "--out", disputed}, disputed)
	g.run(buyer, "dispute again", []string{"dispute", "--deal", dealID, "--row", "item_delivered", "--out", disputed}, disputed)
	g.run(buyer, "dispute in another forum", []string{"dispute", "--deal", dealID, "--row", "item_delivered", "--forum", "external:panel"})
	withdrawn := g.file("withdraw.json")
	g.run(buyer, "withdraw", []string{"dispute", "--deal", dealID, "--row", "item_delivered", "--withdraw", "--out", withdrawn}, withdrawn)
	g.run(buyer, "withdraw again", []string{"dispute", "--deal", dealID, "--row", "item_delivered", "--withdraw", "--out", withdrawn}, withdrawn)
	grounds := g.file("grounds.txt")
	require.NoError(t, os.WriteFile(grounds, []byte("the lamp arrived broken"), 0o600))
	g.run(buyer, "challenge a ruling not held", []string{"dispute", "--deal", dealID, "--challenge", strings.Repeat("7", 64), "--grounds", grounds})

	// What the seller received: a payee leg, observed again, superseded.
	ref := []string{"--payment-ref-type", "ln.payment_hash", "--payment-ref", strings.Repeat("c", 64)}
	payee := func(status string, extra ...string) []string {
		return append(append([]string{"settle", "--deal", dealID, "--received", "5900", "--receive-fee", "100", "--status", status}, ref...), extra...)
	}
	pending := g.file("pending.json")
	res = g.run(seller, "payee pending", payee("pending", "--out", pending), pending)
	require.NotNil(t, res, "%+v", g.steps)
	pendingDigest, _ := res["digest"].(string)
	g.run(seller, "payee pending again", payee("pending", "--out", pending), pending)
	settled := g.file("settled.json")
	g.run(seller, "payee settled", payee("settled", "--supersedes", pendingDigest, "--out", settled), settled)
	g.run(seller, "payee superseded again", payee("failed", "--supersedes", pendingDigest, "--out", g.file("x11.json")))
	g.run(seller, "payee supersedes no leg", payee("failed", "--supersedes", strings.Repeat("9", 64), "--out", g.file("x12.json")))
	g.run(seller, "payee a row that owes no amount", payee("settled", "--row", "item_delivered", "--out", g.file("x13.json")))
	g.run(seller, "payee reverses no leg", payee("settled", "--reverses", strings.Repeat("9", 64), "--out", g.file("x14.json")))
	g.run(seller, "payee refund of its own payee leg", payee("settled", "--reverses", pendingDigest, "--out", g.file("x15.json")))
	g.run(buyer, "a payment as a payee leg", payee("settled", "--out", g.file("x16.json")))

	got, err := json.MarshalIndent(g.steps, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_DEAL_ACTS_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(dealActsGolden, got, 0o644))
		return
	}
	want, err := os.ReadFile(dealActsGolden)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}
