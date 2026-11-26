// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
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

// No bundle builder writes a cadence chain (u225, u230): no production file
// names the chain's extension as something to write. A guard against a
// writer coming back.
func TestNoBundleBuilderWritesACadenceChain(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	write := regexp.MustCompile(`\[(DealParentWitnessExtension|DealCadenceExtension)\]\s*=`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		assert.Empty(t, write.FindAllIndex(mustRead(t, f), -1), f)
		assert.NotContains(t, string(mustRead(t, f)), "cadenceExtensionEntry(", f)
	}
}

// A pinned evidence request whose head is a report sealed with
// witness_coverage gets a signed refusal (coverage_unsatisfiable), never the
// copy, and no report of the responder's moves the pinned head.
func TestAPinnedRequestForACoverageReportIsRefused(t *testing.T) {
	inLogReports(t)
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
	buyer := thisDealParty(t)
	dealFixture(t)
	dealRun(t, "open", "--records", "typed", "--deal-id", id, "--input", writeJSON(t, sellerTyped))
	seller := thisDealParty(t)
	party(t, seller.config)
	var pin map[string]any
	withSession(t, func(s *dealSession) {
		ctx := t.Context()
		require.NoError(t, s.useDeal(ctx, id, false))
		events, err := s.loadBook(ctx, id)
		require.NoError(t, err)
		report, err := s.dealReportFor(ctx, events)
		require.NoError(t, err)
		ext, err := s.dealReportExtension(events, report, dealCopyAudience, dealVerifyCommand("bundle.json"), dealPrivate{}, map[string]bool{})
		require.NoError(t, err)
		ext["witness_coverage"] = map[string]any{"steps_witnessed": json.Number("1"), "steps": json.Number("2")}
		_, err = s.dealSealReport(ctx, id, dealCopyAudience, "", ext)
		require.NoError(t, err)
		cp, err := cli.CutCheckpoint(ctx, localOnly(s.dp), s.t.Log)
		require.NoError(t, err)
		record, err := checkpoint.ParseRecord(cp.Bytes)
		require.NoError(t, err)
		pin = map[string]any{"root": record.Root, "mmr_size": record.MMRSize}
	})
	before := len(bookRecords(t, id))
	asked := askFor(t, buyer, seller, id, map[string]any{"expected_pin": pin})
	assert.Equal(t, "refusal", asked.outcome)
	assert.Contains(t, fmt.Sprint(asked.response), "coverage_unsatisfiable")
	party(t, seller.config)
	assert.Equal(t, before, len(bookRecords(t, id)), "no new report moves the pinned head")
}

// A deal witnessed through an earlier checkpoint that already held every
// step (a report sealed after it moved the checkpoint, adding no step): the
// holder's line says every step is witnessed through that earlier
// checkpoint, never this checkpoint.
func TestEveryStepWitnessedThroughAnEarlierCheckpoint(t *testing.T) {
	inLogReports(t)
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealID := retailDeal(t)
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "first.json"))
	a := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "second.json"))["assurance"].(map[string]any)
	held := a["holder_witnessed"].(map[string]any)
	require.Equal(t, "part", held["extent"], "the sealed reports moved the checkpoint past the witnessed tick")
	assert.Equal(t, held["steps_witnessed"], held["steps"])
	assert.Contains(t, a["text"], "steps witnessed by")
	assert.Contains(t, a["text"], "through an earlier checkpoint")
	assert.NotContains(t, a["text"], "this checkpoint witnessed")
}

// --from-bundle on a file whose checkpoint a later step has passed, after a
// tick witnessed the later checkpoint: a state read now does not hold for
// the file's checkpoint, so it says not_carried, not scheduled.
func TestReportFromAnOlderFileSaysNotCarried(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealID := retailDeal(t)
	_, older, _, _ := dealReportFiles(t, dealID)
	*now = now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	from, _, _, _ := dealReportFiles(t, dealID, "--from-bundle", older)
	assert.Equal(t, "not_carried", from["assurance"].(map[string]any)["witness_state"])
	assert.NotContains(t, from["assurance"].(map[string]any)["text"], "Witness pending")
}

// A copy that carries a receipt on its own checkpoint (checkpoint.witnesses)
// is witnessed by that receipt alone: nothing of the holder's shelf (its
// tick time, its covered steps) describes it, even when the holder's own
// state is witnessed in part through the cadence.
func TestADirectReceiptIsReadWithoutTheShelf(t *testing.T) {
	b := map[string]interface{}{"checkpoint": map[string]interface{}{"witnesses": []interface{}{
		map[string]interface{}{"ts_url": "https://witness.example/checkpoints", "entry_hash": "ab", "receipt_b64": "AA=="},
	}}}
	producer := map[string]interface{}{
		"state": "witnessed", "extent": "part", "steps_witnessed": json.Number("2"), "steps": json.Number("5"),
		"checkpoint_at": "2026-10-04T09:00:00Z",
		"cadence":       map[string]interface{}{"witnesses": []interface{}{map[string]interface{}{"ts_url": "https://shelf-witness.example"}}},
	}
	a := dealAssuranceRung(b, producer)
	assert.Equal(t, "witnessed", a["rung"])
	assert.Equal(t, "witness.example", a["witness"])
	assert.NotContains(t, a, "checkpoint_at")
	assert.NotContains(t, a, "steps_witnessed")
	assert.NotContains(t, a, "holder_witnessed")
	assert.NotContains(t, a["text"], "2026-10-04T09:00:00Z")
}

// A receipt on the copy's own checkpoint still travels and verifies, and the
// standalone report's as_of still says not witnessed: it claims less than
// the copy carries, never more (u225).
func TestADirectReceiptVerifiesAndAsOfStaysNotWitnessed(t *testing.T) {
	dealFixture(t)
	setDealClock(t, "2026-09-27T18:00:00Z")
	id := retailDeal(t)
	b, _ := sharedCopy(t, id, cli.DealAudienceCounterparty, "the shop")
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	witnessed(t, key)(b)
	path := writeBundle(t, b)
	result, err := verifyWithDirectory(t, path, writeDirectory(t, rawKeyRow(testWitness, public)))
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"])
	assert.Equal(t, "pass", result["witnesses"].(map[string]any)["status"], "the direct receipt verifies")
	found := false
	for _, raw := range result["extensions"].([]any) {
		if e := raw.(map[string]any); e["kind"] == "x-deal-v0" {
			found = true
			assert.Equal(t, "as of the deal's last step at 2026-09-27T18:00:00Z (the producer's time); not witnessed", e["as_of"])
		}
	}
	assert.True(t, found, "the x-deal-v0 entry")
}
