// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `deal report` on a v1 deal prints the holder's own copy and seals nothing;
// --out seals the report, whose pair verifies against the deal's first
// record; --read-only prints too.
func TestAV1ReportPrintsAndSealsOnOut(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	out := dealRun(t, "report", "--deal", h.dealID)
	report, ok := out["report"].(map[string]any)
	require.True(t, ok, "%v", out)
	assert.Equal(t, "keep", report["audience"])
	assert.Nil(t, out["sealed_report"], "printing seals nothing")

	file := filepath.Join(t.TempDir(), "report.json")
	sealed := dealRun(t, "report", "--deal", h.dealID, "--out", file)
	require.NotEmpty(t, sealed["sealed_report"])
	pair, err := os.ReadFile(file)
	require.NoError(t, err)
	read, err := xdealv1.VerifyReport(pair, readPush(t, h.buyerAgr).Record)
	require.NoError(t, err)
	assert.Equal(t, sealed["sealed_report"], read.CapsuleID)
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--out", file), "exists")

	ro := dealRun(t, "report", "--deal", h.dealID, "--read-only")
	assert.NotNil(t, ro["report"])
}

// A book with records after its last checkpoint has no report yet, and the
// refusal names the cadence; a deal with no book names deal agree; the v0
// flags with no v1 form are refused, each with why.
func TestAV1ReportRefusals(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID), "the next `deal tick` covers it")
	for flag, want := range map[string]string{"--html": "has no page yet", "--share": "not served yet", "--bundle": "not served yet"} {
		arg := filepath.Join(t.TempDir(), "x")
		if flag == "--share" {
			arg = "counterparty"
		}
		assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, flag, arg), want, flag)
	}
	assert.Contains(t, v1Err(t, "report", "--deal", h.dealID, "--read-only", "--out", filepath.Join(t.TempDir(), "r.json")), "writes nothing")

	r := receivedOffer(t)
	assert.Contains(t, v1Err(t, "report", "--deal", r.dealID), "has no deal book yet")
}

// --out is an x-deal-v1 report's; an x-deal-v0 deal refuses it.
func TestAV0ReportRefusesOut(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, v0BuyerOpen))["deal_id"].(string)
	assert.Contains(t, v1Err(t, "report", "--deal", id, "--out", filepath.Join(t.TempDir(), "r.json")), "an x-deal-v0 deal's report goes to --html or --bundle")
}
