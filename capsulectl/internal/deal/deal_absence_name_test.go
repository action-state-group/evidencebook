package deal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyAbsence records an absence for a request as capsulectl-deal wrote
// one before the custody and record-kind profile v0 (evidencebook#116):
// typed evidence_absence, with the fields dealRecordAbsence writes.
func legacyAbsence(t *testing.T, recordID string, windowEnd time.Time) {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	sent, err := s.sentRequest(t.Context(), recordID)
	require.NoError(t, err)
	_, err = s.recordExchange(t.Context(), sent.row.dealID, dealExchangeAbsenceLegacy, recordID, map[string]interface{}{
		"request_record": recordID, "request_digest": sent.digest,
		"window_start": sent.row.record["at"], "window_end": windowEnd.UTC().Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
}

// absenceRow is the stored kind and the signed record's type of a request's
// one recorded outcome.
func absenceRow(t *testing.T, recordID string) (kind, typ string) {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	outcome, err := s.outcomeOf(t.Context(), recordID)
	require.NoError(t, err)
	require.NotNil(t, outcome, "request %s has a recorded outcome", recordID)
	typ, _ = outcome.record["type"].(string)
	return outcome.kind, typ
}

// capsulectl-deal writes the profile's name, recorded_absence; an absence
// written under the old name, evidence_absence, still reads as an absence:
// an old book, a new one and a mixed one compose the same, and an old
// absence still refuses a second outcome for its request.
func TestADealAbsenceIsWrittenAsRecordedAbsenceAndReadUnderBothNames(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
	buyer := thisDealParty(t)
	own, _ := pinnedCopy(t, buyer, buyer, id)

	party(t, buyer.config)
	ask := func() string {
		sent := runJSON(t, "deal", "request", "--request", writeJSONFile(t, "ask.json", map[string]any{
			"subject": map[string]any{"kind": "correlation", "value": id}, "coverage": map[string]any{"min_freshness": map[string]any{"size": 1}},
		}), "--responder", "silent party", "--output", filepath.Join(t.TempDir(), "request.json"))
		return sent["record_id"].(string)
	}
	asked := cli.DealClock()
	fresh, old := ask(), ask()
	windowEnd := asked.Add(2 * time.Minute)
	clock := cli.DealClock
	cli.DealClock = func() time.Time { return asked.Add(time.Hour) }
	t.Cleanup(func() { cli.DealClock = clock })

	absent := runJSON(t, "deal", "request", "--absent-until", windowEnd.Format(time.RFC3339), "--for", fresh)
	assert.Equal(t, "recorded_absence", absent["outcome"])
	legacyAbsence(t, old, windowEnd)

	kind, typ := absenceRow(t, fresh)
	assert.Equal(t, "recorded_absence", kind, "a new absence is stored under the profile's name")
	assert.Equal(t, "recorded_absence", typ, "and its record is typed with it")
	kind, typ = absenceRow(t, old)
	assert.Equal(t, "evidence_absence", kind, "an old absence is never rewritten")
	assert.Equal(t, "evidence_absence", typ)

	compose := func(members ...string) map[string]any {
		out := filepath.Join(t.TempDir(), "composed.json")
		args := []string{"--profile", "deal", "deal", "compose", "--deal", id, "--output", out, "--member", own.recordID + "=" + own.responsePath}
		for _, m := range members {
			args = append(args, "--member", m)
		}
		_, err := invoke(t, "", args...)
		require.NoError(t, err)
		result, err := verifyWithDirectory(t, out, "")
		require.NoError(t, err)
		assert.Equal(t, "VALID", result["verdict"], result)
		return composedBlock(t, out)
	}
	newBook, oldBook, mixed := compose(fresh), compose(old), compose(fresh, old)
	for name, tc := range map[string]struct {
		record string
		alone  map[string]any
	}{
		"new": {fresh, newBook},
		"old": {old, oldBook},
	} {
		member := memberByID(t, tc.alone, tc.record)
		assert.Equal(t, "absence", member["outcome"], name)
		assert.Equal(t, buyer.signer, member["observer"], name)
		assert.Equal(t, member, memberByID(t, mixed, tc.record), "%s: the absence composes the same alone and in a mixed book", name)
	}
	a, b := memberByID(t, newBook, fresh)["absence"].(map[string]any), memberByID(t, oldBook, old)["absence"].(map[string]any)
	assert.Equal(t, windowEnd.UTC().Format(time.RFC3339Nano), a["window"].(map[string]any)["to"])
	assert.Equal(t, a["window"].(map[string]any)["to"], b["window"].(map[string]any)["to"], "both names compose one absence shape")
	assert.Equal(t, len(a), len(b))

	_, err := invoke(t, "", "--profile", "deal", "deal", "request", "--absent-until", windowEnd.Format(time.RFC3339), "--for", old)
	assert.ErrorContains(t, err, "an outcome is already recorded", "an old absence refuses a second absence")
	_, err = invoke(t, "", "--profile", "deal", "deal", "request", "--response", own.responsePath, "--for", old,
		"--responder-key", buyer.signer, "--responder-checkpoint-key", buyer.checkpoint)
	assert.ErrorContains(t, err, "an outcome is already recorded", "and a response")
}
