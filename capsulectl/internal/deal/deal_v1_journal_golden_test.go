// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The journal verbs of evidencebook#899's phase 3, byte for byte: the notes
// (intent, message), deal check, and a payment's two calls (settle --sent,
// then --status settled or failed) with their refusals. The golden file was
// written by the plugin before these verbs moved onto evidencebook's
// package deal, and the plugin must still produce it, as
// TestDealOpenGolden's and TestDealActsGolden's do.
//
// EB_WRITE_DEAL_JOURNAL_GOLDEN=1 rewrites the file; only an intended change
// to what these verbs print or seal does that.

const dealJournalGolden = "testdata/deal-journal-golden.json"

func TestDealJournalGolden(t *testing.T) {
	cryptotest.SetGlobalRandom(t, 8993)
	g := &goldenRun{t: t, root: filepath.Dir(t.TempDir())}
	seller := newV1Side(t)
	buyer := newV1Side(t)
	g.now = clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))

	// deal.open, both sides agreed and each holding the other's agreement.
	offer := g.file("offer.json")
	res := g.run(buyer, "offer", []string{"offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offer}, offer)
	require.NotNil(t, res, "%+v", g.steps)
	dealID, offerDigest := res["deal_id"].(string), res["offer"].(string)
	g.run(seller, "receive the offer", []string{"receive", offer})
	g.run(seller, "admit", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})
	sellerAgree := g.file("seller-agreement.json")
	g.run(seller, "agree", []string{"agree", offerDigest, "--out", sellerAgree}, sellerAgree)
	g.run(buyer, "receive the agreement", []string{"receive", sellerAgree})
	g.run(buyer, "admit", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})
	buyerAgree := g.file("buyer-agreement.json")
	g.run(buyer, "agree", []string{"agree", offerDigest, "--out", buyerAgree}, buyerAgree)
	g.run(seller, "receive the buyer's agreement", []string{"receive", buyerAgree})

	// Notes and a check: a check before the intent is refused; the intent
	// opens the deal's step list; a message; the pay check.
	pay := func(ref string) string { return writeJSON(t, fmt.Sprintf(payInput, `,"payment_ref":"`+ref+`"`)) }
	g.run(buyer, "check before the intent", []string{"check", "--deal", dealID, "--input", pay(v1LnHash)})
	g.run(buyer, "a message before the intent", []string{"note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"user","text":"is it still there?"}`)})
	g.run(buyer, "intent", []string{"note", "--deal", dealID, "--kind", "intent", "--input", writeJSON(t, v1BuyerIntent), "--opening", writeJSON(t, v1Opening)})
	g.run(buyer, "message", []string{"note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"yes, ships today"}`)})
	res = g.run(buyer, "pay check", []string{"check", "--deal", dealID, "--input", pay(v1LnHash)})
	require.NotNil(t, res, "%+v", g.steps)
	check, _ := res["check_id"].(string)
	require.NotEmpty(t, check, "%v", res)

	// The payment: --status before --sent is refused; the send; a second
	// send, and a details change, while it is in flight; the leg; the same
	// leg again; a send on the used check.
	leg := g.file("leg.json")
	settle := func(extra ...string) []string { return append([]string{"settle", "--deal", dealID, "--check", check}, extra...) }
	g.run(buyer, "status before the send", settle("--status", "settled", "--out", g.file("x1.json")))
	g.run(buyer, "send", settle("--sent", "--payment-ref-type", "ln.payment_hash"))
	g.run(buyer, "send again in flight", settle("--sent", "--payment-ref-type", "ln.payment_hash"))
	g.run(buyer, "a details change in flight", []string{"note", "--deal", dealID, "--kind", "change", "--input", writeJSON(t, `{"source":"checkout page","terms":{"item":"lamp","when":"2026-11-05"}}`)})
	g.run(buyer, "settled", settle("--status", "settled", "--out", leg), leg)
	g.run(buyer, "settled again", settle("--status", "settled", "--out", leg), leg)
	g.run(buyer, "failed after settled", settle("--status", "failed", "--out", g.file("x2.json")))
	g.run(buyer, "send on a used check", settle("--sent", "--payment-ref-type", "ln.payment_hash"))
	g.run(seller, "receive the leg", []string{"receive", leg})

	// A second payment that fails.
	second := strings.Repeat("cd", 32)
	res = g.run(buyer, "second pay check", []string{"check", "--deal", dealID, "--input", pay(second)})
	require.NotNil(t, res, "%+v", g.steps)
	check2, _ := res["check_id"].(string)
	failed := g.file("failed.json")
	g.run(buyer, "second send", []string{"settle", "--deal", dealID, "--check", check2, "--sent", "--payment-ref-type", "ln.payment_hash"})
	g.run(buyer, "second failed", []string{"settle", "--deal", dealID, "--check", check2, "--status", "failed", "--out", failed}, failed)
	g.run(buyer, "a details change after", []string{"note", "--deal", dealID, "--kind", "change", "--input", writeJSON(t, `{"source":"checkout page","terms":{"item":"lamp","when":"2026-11-05"}}`)})

	got, err := json.MarshalIndent(g.steps, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_DEAL_JOURNAL_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(dealJournalGolden, got, 0o644))
		return
	}
	want, err := os.ReadFile(dealJournalGolden)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}
