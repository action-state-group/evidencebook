package deal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TEMPORARY (evidencebook#462): the holder's own outcome_note makes the
// History card count the deal did_not_hold; without it the card says undecided.
func TestAnOutcomeNoteMakesTheCardCountTheDealDidNotHold(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")

	out := dealRun(t, "outcome-note", "seal", "--deal", id, "--reason", "dispute_lost")
	assert.Equal(t, "did_not_hold", out["outcome"])
	records := recordsOfType(t, id, "outcome_note")
	require.Len(t, records, 1)
	assert.Equal(t, map[string]any{"outcome": "did_not_hold", "reason": "dispute_lost"}, bodyOf(records[0]))

	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	own := filepath.Join(t.TempDir(), "own.json")
	dealRun(t, "standard-history", "card", "--out", own)
	deals := shBlock(t, readBundle(t, own))["deals"].([]any)
	require.Len(t, deals, 1)
	assert.Equal(t, "did_not_hold", deals[0].(map[string]any)["outcome"])

	// A deal takes one note, and it is in no shared copy.
	_, err := invoke(t, "", "--profile", "deal", "deal", "outcome-note", "seal", "--deal", id, "--reason", "other")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already")
	assert.NotContains(t, bundleText(t, id, "--share", "counterparty", "--to", "the other side"), "dispute_lost")
}

func TestAnOutcomeNoteRefusesWhatTheProfileDoesNotAllow(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	for name, args := range map[string][]string{
		"a reason of another kind": {"outcome-note", "seal", "--deal", id, "--reason", "sad"},
		"no reason":                {"outcome-note", "seal", "--deal", id},
	} {
		_, err := invoke(t, "", append([]string{"--profile", "deal", "deal"}, args...)...)
		require.Error(t, err, name)
	}
}

// The note is sealed only after the final close: an open deal and an `open` close are refused, and
// nothing is appended; a sealed note confirms the final close.
func TestAnOutcomeNoteIsSealedOnlyAfterTheFinalClose(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	seal := func() error {
		_, err := invoke(t, "", "--profile", "deal", "deal", "outcome-note", "seal", "--deal", id, "--reason", "other")
		return err
	}
	err := seal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close")
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"pending","reason":"awaiting_delivery"}`))
	err = seal()
	require.Error(t, err, "a close that is not final is no close for the note")
	assert.Empty(t, recordsOfType(t, id, "outcome_note"))

	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	require.NoError(t, seal())
	events, recs := recordsOf(t, id)
	closeAt := finalClose(events)
	notes := recordsOfType(t, id, "outcome_note")
	require.Len(t, notes, 1)
	refs := notes[0][dealprofile.Profile].(map[string]any)["refs"].([]any)
	require.Len(t, refs, 1)
	assert.Equal(t, events[closeAt].Digest, refs[0].(map[string]any)["digest"], "it confirms the final close")
	assert.Equal(t, "outcome_note", recs[len(recs)-1][dealprofile.Profile].(map[string]any)["record_type"])
}
