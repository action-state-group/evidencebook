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

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deal.open verbs, byte for byte (evidencebook#899). The golden file was
// written by the plugin before these verbs moved onto evidencebook's package
// deal, and the plugin must still produce it: every command's output, error
// text and exit code, and every file it writes, over one run of offers,
// receipts, admits, agreements, declines and cancellations and their
// refusals. The run is deterministic: a seeded global random source
// (cryptotest.SetGlobalRandom) gives every key, deal id, log id, nonce and
// shelf draw, and the deal clock is fixed.
//
// EB_WRITE_DEAL_OPEN_GOLDEN=1 rewrites the file; only an intended change to
// what these verbs print or seal does that.

const dealOpenGolden = "testdata/deal-open-golden.json"

// goldenStep is one command of the run as a person sees it.
type goldenStep struct {
	Name  string            `json:"name"`
	Args  []string          `json:"args"`
	Out   string            `json:"out"`
	Error string            `json:"error,omitempty"`
	Exit  int               `json:"exit"`
	Files map[string]string `json:"files,omitempty"`
}

type goldenRun struct {
	t     *testing.T
	root  string
	now   *time.Time
	steps []goldenStep
}

// norm replaces the test's temporary directories, the only part of the run
// that differs from one machine to the next.
func (g *goldenRun) norm(s string) string { return strings.ReplaceAll(s, g.root, "<tmp>") }

// run runs one deal command as side and records it with the files it names
// in outs.
func (g *goldenRun) run(side v1Side, name string, args []string, outs ...string) map[string]any {
	g.t.Helper()
	side.act(g.t)
	*g.now = g.now.Add(time.Minute)
	full := append([]string{"--profile", "deal", "deal"}, args...)
	out, err := invokeRaw(g.t, "", full...)
	step := goldenStep{Name: name, Out: g.norm(out), Exit: cli.ExitCode(err)}
	for _, a := range args {
		step.Args = append(step.Args, g.norm(a))
	}
	if err != nil {
		step.Error = g.norm(cli.SafeError(err))
	}
	for _, path := range outs {
		if b, rerr := os.ReadFile(path); rerr == nil {
			if step.Files == nil {
				step.Files = map[string]string{}
			}
			step.Files[g.norm(path)] = g.norm(string(b))
		}
	}
	g.steps = append(g.steps, step)
	var m map[string]any
	if err == nil {
		_ = json.Unmarshal([]byte(out), &m)
	}
	return m
}

func (g *goldenRun) file(name string) string { return filepath.Join(g.t.TempDir(), name) }

func TestDealOpenGolden(t *testing.T) {
	cryptotest.SetGlobalRandom(t, 899)
	g := &goldenRun{t: t, root: filepath.Dir(t.TempDir())}
	seller := newV1Side(t)
	buyer := newV1Side(t)
	g.now = clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))

	// One whole deal.open: the buyer offers to the seller, the seller admits
	// and agrees, the buyer admits and agrees.
	offer := g.file("offer.json")
	res := g.run(buyer, "offer", []string{"offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", offer}, offer)
	require.NotNil(t, res, "%+v", g.steps)
	dealID, offerDigest := res["deal_id"].(string), res["offer"].(string)
	g.run(seller, "receive the offer", []string{"receive", offer})
	g.run(seller, "agree before admitting", []string{"agree", offerDigest})
	g.run(seller, "admit", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})
	sellerAgree := g.file("seller-agreement.json")
	g.run(seller, "agree", []string{"agree", offerDigest, "--out", sellerAgree}, sellerAgree)
	g.run(seller, "agree again", []string{"agree", offerDigest, "--out", sellerAgree}, sellerAgree)
	g.run(buyer, "admit", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})
	g.run(buyer, "agree before the acceptor's agreement", []string{"agree", offerDigest})
	g.run(buyer, "receive the agreement", []string{"receive", sellerAgree})
	buyerAgree := g.file("buyer-agreement.json")
	g.run(buyer, "agree", []string{"agree", offerDigest, "--out", buyerAgree}, buyerAgree)
	g.run(seller, "receive the offerer's agreement", []string{"receive", buyerAgree})
	g.run(seller, "decline once agreed", []string{"offer", "decline", offerDigest})
	g.run(buyer, "decline your own offer", []string{"offer", "decline", offerDigest})
	g.run(buyer, "admit once agreed", []string{"admit", "--deal", dealID, "--input", engineOutput(t, "GO", true, nil)})

	// Refusals of an admission decision an agreement cannot cite.
	second := g.file("second.json")
	res = g.run(buyer, "a second offer", []string{"offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", second}, second)
	require.NotNil(t, res, "%+v", g.steps)
	secondDeal, secondDigest := res["deal_id"].(string), res["offer"].(string)
	g.run(seller, "receive the second offer", []string{"receive", second})
	g.run(seller, "admit a NO", []string{"admit", "--deal", secondDeal, "--input", engineOutput(t, "NO", false, func(m map[string]any) { m["admit"] = nil })})
	g.run(seller, "admit an ASK", []string{"admit", "--deal", secondDeal, "--input", engineOutput(t, "ASK", false, nil)})
	g.run(seller, "admit a body that differs", []string{"admit", "--deal", secondDeal, "--input", engineOutput(t, "GO", true, func(m map[string]any) { m["tier"] = "premium" })})
	g.run(seller, "admit no admit body", []string{"admit", "--deal", secondDeal, "--input", writeJSON(t, `{"verdict":"GO"}`)})
	g.run(seller, "admit an unknown deal", []string{"admit", "--deal", "deal-0000000000000000", "--input", engineOutput(t, "GO", true, nil)})
	g.run(seller, "agree an unknown offer", []string{"agree", strings.Repeat("0", 64)})
	g.run(seller, "agree with an admit not in the journal", []string{"agree", secondDigest, "--admit", strings.Repeat("1", 64)})
	g.run(seller, "admit the second deal", []string{"admit", "--deal", secondDeal, "--input", engineOutput(t, "GO", true, nil)})
	g.run(seller, "agree with an intent not in the journal", []string{"agree", secondDigest, "--intent", strings.Repeat("2", 64)})

	// The seller declines the second offer; the buyer takes the decline.
	decline := g.file("decline.json")
	g.run(seller, "decline with a bad reason", []string{"offer", "decline", secondDigest, "--reason", "bored"})
	g.run(seller, "decline", []string{"offer", "decline", secondDigest, "--reason", "not_selected", "--out", decline}, decline)
	g.run(seller, "decline again", []string{"offer", "decline", secondDigest, "--out", decline})
	g.run(seller, "agree a declined offer", []string{"agree", secondDigest})
	g.run(buyer, "receive the decline", []string{"receive", decline})
	g.run(buyer, "agree a declined offer", []string{"agree", secondDigest})

	// The buyer cancels a third offer.
	third := g.file("third.json")
	res = g.run(buyer, "a third offer", []string{"offer", "--input", writeJSON(t, offerTo(seller.party.signer)), "--out", third}, third)
	require.NotNil(t, res, "%+v", g.steps)
	thirdDigest := res["offer"].(string)
	g.run(seller, "receive the third offer", []string{"receive", third})
	cancel := g.file("cancel.json")
	g.run(buyer, "cancel an offer you did not write", []string{"offer", "cancel", strings.Repeat("3", 64)})
	g.run(buyer, "cancel", []string{"offer", "cancel", thirdDigest, "--out", cancel}, cancel)
	g.run(buyer, "cancel again", []string{"offer", "cancel", thirdDigest, "--out", cancel})
	g.run(seller, "receive the cancellation", []string{"receive", cancel})
	g.run(seller, "decline a cancelled offer", []string{"offer", "decline", thirdDigest})
	g.run(seller, "agree a cancelled offer", []string{"agree", thirdDigest})

	// An offer to a party that keeps no signed records: the offerer agrees
	// alone.
	shop := g.file("shop.json")
	res = g.run(buyer, "an offer to a shop", []string{"offer", "--input", writeJSON(t, v1OfferInput), "--out", shop}, shop)
	require.NotNil(t, res, "%+v", g.steps)
	g.run(buyer, "admit the shop deal", []string{"admit", "--deal", res["deal_id"].(string), "--input", engineOutput(t, "GO", true, nil)})
	shopAgree := g.file("shop-agreement.json")
	g.run(buyer, "agree alone", []string{"agree", res["offer"].(string), "--out", shopAgree}, shopAgree)

	got, err := json.MarshalIndent(g.steps, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	if os.Getenv("EB_WRITE_DEAL_OPEN_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(dealOpenGolden, got, 0o644))
		return
	}
	want, err := os.ReadFile(dealOpenGolden)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}
