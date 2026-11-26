// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cadenceNames are every name a cadence chain was ever carried under.
var cadenceNames = append([]string{DealParentWitnessExtension, DealCadenceExtension}, earlierCadenceExtensions...)

func assertNoWitnessChain(t *testing.T, raw []byte, what string) {
	t.Helper()
	text := string(raw)
	for _, name := range cadenceNames {
		assert.NotContains(t, text, `"`+name+`"`, what)
	}
	assert.NotContains(t, text, "witness_coverage", what)
}

// A witnessed x-deal-v0 deal's new copies carry no witness chain and no
// witness_coverage (u225, evidencebook#958): the deal report, own and for
// each audience, a History's deal members and a sale's bundle. Each still
// verifies, and the report's as_of says not witnessed.
func TestNewV0CopiesCarryNoWitnessChain(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealID := retailDeal(t)
	saleID, _ := newSale(t)
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	// A later step: the holder's chain covers an earlier checkpoint only,
	// which is when witness_coverage used to be written.
	dealRun(t, "note", "--deal", dealID, "--kind", "act", "--input", filepath.Join(retailDemo, "act-pay.json"))
	directory := writeDirectory(t, rawKeyRow(endpoint, public))
	dir := t.TempDir()

	own := filepath.Join(dir, "own.json")
	out := dealRun(t, "report", "--deal", dealID, "--bundle", own)
	assertNoWitnessChain(t, mustRead(t, own), "own report")
	a := out["assurance"].(map[string]any)
	assert.Equal(t, "self_attested", a["rung"])
	assert.Equal(t, "part", a["holder_witnessed"].(map[string]any)["extent"])
	result, err := verifyWithDirectory(t, own, directory)
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"])
	assert.NotContains(t, result, "reader_witnesses")

	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		b, page := sharedCopy(t, dealID, audience, "the other side")
		raw, err := json.Marshal(b)
		require.NoError(t, err)
		assertNoWitnessChain(t, raw, audience)
		assert.NotContains(t, page, "Your own profile reports", audience+": the holder's line stays off the page")
		result, _ := verifyWithDirectory(t, writeBundle(t, b), directory)
		assert.Equal(t, "VALID", result["verdict"], audience)
		for _, x := range result["extensions"].([]any) {
			if e := x.(map[string]any); e["as_of"] != nil {
				assert.Contains(t, e["as_of"], "not witnessed", audience)
			}
		}
	}

	sale := filepath.Join(dir, "sale.json")
	dealRun(t, "sale", "bundle", "--sale", saleID, "--out", sale)
	assertNoWitnessChain(t, mustRead(t, sale), "sale")

	history, _ := writeHistory(t, []string{dealID}, sentTo, dealID)
	assertNoWitnessChain(t, mustRead(t, history), "history")
}

// --from-bundle on a new copy of a witnessed deal says what the holder's own
// profile reports, not not_carried.
func TestReportFromANewCopyReadsTheHoldersOwnState(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealID := retailDeal(t)
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	_, bundlePath, _, _ := dealReportFiles(t, dealID)
	from, _, _, _ := dealReportFiles(t, dealID, "--from-bundle", bundlePath)
	a := from["assurance"].(map[string]any)
	assert.Equal(t, "not_carried", a["witness_state"])
	assert.Equal(t, "all", a["holder_witnessed"].(map[string]any)["extent"])
	assert.Contains(t, a["text"], "Your own profile reports this checkpoint witnessed by")
}

// A report sealed before u225 that states witness_coverage is never shown in
// a new copy: the History refuses with the way on, and the error a pinned
// evidence response maps to a signed refusal is the same one.
func TestAReportSealedWithWitnessCoverageIsNotShown(t *testing.T) {
	inLogReports(t)
	dealFixture(t)
	dealID := retailDeal(t)
	// A keep report as one sealed before u225 was: it states a coverage.
	withSession(t, func(s *dealSession) {
		require.NoError(t, s.useDeal(t.Context(), dealID, false))
		events, err := s.loadBook(t.Context(), dealID)
		require.NoError(t, err)
		report, err := s.dealReportFor(t.Context(), events)
		require.NoError(t, err)
		ext, err := s.dealReportExtension(events, report, cli.DealAudienceKeep, dealVerifyCommand("bundle.json"), dealPrivate{}, map[string]bool{})
		require.NoError(t, err)
		ext["witness_coverage"] = map[string]any{"steps_witnessed": json.Number("1"), "steps": json.Number("3")}
		_, err = s.dealSealReport(t.Context(), dealID, cli.DealAudienceKeep, "", ext)
		require.NoError(t, err)
	})
	// A copy for someone else cuts the checkpoint that indexes it, and seals
	// no keep report of its own.
	sharedCopy(t, dealID, cli.DealAudienceCounterparty, "the shop")
	path := filepath.Join(t.TempDir(), "history.json")
	out, err := invoke(t, "", "--profile", "deal", "deal", "history", "--out", path, "--deal", dealID, "--audience", "keep")
	require.Error(t, err, out)
	assert.Contains(t, err.Error()+out, "seal a current report")
	assert.ErrorIs(t, err, errHeldReportWitnessCoverage)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "no copy is written")

	// The evidence response's refusal reads the same error.
	assert.True(t, errors.Is(heldReportCoverageError(dealID), errHeldReportWitnessCoverage))
	assert.True(t, errors.Is(heldReportCoverageError(dealID), cli.ErrConflict))
}

func TestSealedReportCarriesWitnessCoverage(t *testing.T) {
	b := func(input map[string]any) map[string]interface{} {
		return map[string]interface{}{"disclosures": map[string]interface{}{"r": map[string]interface{}{"agent_input": input}}}
	}
	assert.True(t, sealedReportCarriesWitnessCoverage(b(map[string]any{"report": map[string]any{"witness_coverage": map[string]any{}}}), "r"))
	assert.True(t, sealedReportCarriesWitnessCoverage(b(map[string]any{"witness_coverage": map[string]any{}}), "r"))
	assert.False(t, sealedReportCarriesWitnessCoverage(b(map[string]any{"report": map[string]any{}}), "r"))
	assert.False(t, sealedReportCarriesWitnessCoverage(b(map[string]any{"report": map[string]any{"witness_coverage": map[string]any{}}}), "other"))
}

// Only `deal node bundle` builds a cadence chain into a bundle; no deal copy
// builder does (u225). A guard against a writer coming back.
func TestOnlyTheNodeBundleWritesACadenceChain(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	call := regexp.MustCompile(`cadenceExtensionEntry\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "deal_verify_names.go" {
			continue
		}
		n := len(call.FindAllIndex(mustRead(t, f), -1))
		if f == "deal_node_bundle.go" {
			assert.Equal(t, 1, n, f)
			continue
		}
		assert.Zero(t, n, f)
	}
}
