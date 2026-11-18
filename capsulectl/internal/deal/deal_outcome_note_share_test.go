package deal

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u597 (evidencebook-deal#218): the temporary outcome_note is never in a
// shared copy. A record that confirms the close would say what the note says
// even with its payload withheld (it has one value), and a copy is one log's
// chain, so no copy of a log holding the note is shared; the holder's own copy
// is not affected. Sherlock2 (buzz d3a751ce).
func TestNoSharedCopyIsMadeOfADealThatHoldsAnOutcomeNote(t *testing.T) {
	id := closedUnderPack(t, packID, "")
	dealRun(t, "outcome-note", "seal", "--deal", id, "--reason", "dispute_lost")

	for _, audience := range []string{cli.DealAudienceCounterparty, cli.DealAudienceAdjudicator} {
		out := filepath.Join(t.TempDir(), "shared.json")
		_, err := invoke(t, "", "--profile", "deal", "disclose", "--deal", id, "--share", audience, "--to", "x", "--out", out)
		require.Error(t, err, audience)
		assert.Contains(t, err.Error(), "outcome note", audience)
		assert.Contains(t, err.Error(), "u597", audience)
		assert.NoFileExists(t, out, audience)

		page := filepath.Join(t.TempDir(), "page.html")
		_, err = invoke(t, "", "--profile", "deal", "deal", "report", "--deal", id, "--share", audience, "--to", "x", "--html", page)
		require.Error(t, err, audience)
		assert.Contains(t, err.Error(), "u597", audience)
		assert.NoFileExists(t, page, audience)
	}

	// A shared history may not pick it either; the holder's own history may.
	h := filepath.Join(t.TempDir(), "h.json")
	_, err := invoke(t, "", "--profile", "deal", "deal", "history", "--out", h, "--deal", id, "--audience", "history", "--to", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "u597")
	assert.NoFileExists(t, h)

	// The holder's own copy is not a shared copy.
	page := filepath.Join(t.TempDir(), "own.html")
	dealRun(t, "report", "--deal", id, "--html", page)
	raw, rerr := os.ReadFile(page)
	require.NoError(t, rerr)
	assert.NotEmpty(t, raw)
}

// A deal with no note shares as before.
func TestADealWithNoOutcomeNoteStillSharesItsCopy(t *testing.T) {
	id := closedUnderPack(t, packID, "")
	b, _ := sharedCopy(t, id, cli.DealAudienceCounterparty, "the other side")
	assert.NotEmpty(t, b["records"])
}

// A node's answer to a History request (standard_history/1) does not go
// through a shared copy of a deal: it is a computed count, so a noted deal
// still answers, and the answer and the shared card carry no note.
func TestANodeStillAnswersTheCardWithANotedDeal(t *testing.T) {
	dealFixture(t)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	dealRun(t, "outcome-note", "seal", "--deal", id, "--reason", "dispute_lost")
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(2 * time.Minute)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])

	reply := respondCard(t, cardRequest(t, fullHistory))
	require.NotNil(t, reply.Envelope, "an envelope, not a refusal")
	require.NoError(t, reply.Envelope.Verify())

	shared := filepath.Join(t.TempDir(), "shared.json")
	dealRun(t, "standard-history", "card", "--out", shared, "--shared")
	raw, err := os.ReadFile(shared)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "dispute_lost")
	assert.NotContains(t, string(raw), "outcome_note")
	assert.NotContains(t, shBlock(t, readBundle(t, shared)), "deals", "no deal entry leaves in the shared card")
}

// A request that names a noted deal is declined with the reason word alone,
// before the log is weighed, and the refusal's fields are those of any
// refusal; the citation is the holder's side only (Sherlock2, buzz dd3c5ae7).
// A deal with no note still answers.
func TestARequestForANotedDealIsDeclinedWithTheReasonWordAlone(t *testing.T) {
	id := closedUnderPack(t, packID, "")
	ask := func(coverage map[string]any) map[string]any {
		out := filepath.Join(t.TempDir(), "response.json")
		runJSON(t, "deal", "respond", "--request", writeJSONFile(t, "req.json", map[string]any{
			"subject": map[string]any{"kind": "correlation", "value": id}, "coverage": coverage,
		}), "--requester", "x", "--output", out)
		var m map[string]any
		require.NoError(t, json.Unmarshal(mustRead(t, out), &m))
		return m
	}
	fresh := map[string]any{"min_freshness": map[string]any{"size": 1}}
	future := map[string]any{"min_freshness": map[string]any{"time": "2099-01-01T00:00:00Z"}}
	both := map[string]any{"min_freshness": map[string]any{"size": 1}, "expected_pin": map[string]any{"root": strings.Repeat("a", 64), "mmr_size": 1}}
	before := ask(future)
	assert.Equal(t, "coverage_unsatisfiable", refusalOf(t, before)["reason"], "a deal with no note answers as before")
	before = ask(fresh)
	assert.Nil(t, before["refusal"], "a deal with no note answers")

	dealRun(t, "outcome-note", "seal", "--deal", id, "--reason", "dispute_lost")
	for name, coverage := range map[string]map[string]any{"fresh": fresh, "a floor past the log": {"min_freshness": map[string]any{"size": 999}}, "a future time": future, "both coverages": both} {
		got := ask(coverage)
		refusal, ok := got["refusal"].(map[string]any)
		require.True(t, ok, name+": a signed refusal, not an error: %v", got)
		assert.Equal(t, "policy_declined", refusal["reason"], name)
		assert.NotContains(t, string(mustJSON(t, got)), "u597", name+": the wire says the reason word only")
		assert.NotContains(t, string(mustJSON(t, got)), "dispute_lost", name)
	}
	// A request above the standard card (a derivation) on a noted deal is declined,
	// not held: no pending item is left for the holder.
	derivation := writeJSONFile(t, "deriv.json", map[string]any{"subject": map[string]any{"kind": "correlation", "value": id},
		"coverage": fresh, "derivation": "outcome/1"})
	dout := filepath.Join(t.TempDir(), "deriv-response.json")
	d := runJSON(t, "deal", "respond", "--request", derivation, "--requester", "x", "--output", dout)
	assert.Equal(t, "refusal", d["outcome"])
	assert.Equal(t, "policy_declined", d["reason"])
	assert.Empty(t, heldList(t), "nothing is held for the holder")
	// The citation is kept on the holder's side: in the local record of the answer.
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	db, err := sql.Open("sqlite", "file:"+p.Connection.Database+"?mode=ro")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var rec string
	require.NoError(t, db.QueryRow(`SELECT record FROM deal_requests WHERE kind=? AND record LIKE '%u597%' LIMIT 1`, dealExchangeAnswered).Scan(&rec))
	assert.Contains(t, rec, "outcome note")
	// Uniform with the holder's other policy refusal of a deal copy (a held
	// request it declines): the same token and the same members, so a noted
	// deal's refusal differs from it only in ids, digests and times (the twin's
	// condition, ER -02: no differentiation).
	heldReq := writeJSONFile(t, "held.json", map[string]any{"subject": map[string]any{"kind": "correlation", "value": retailDeal(t)},
		"coverage": map[string]any{"min_freshness": map[string]any{"size": 1}}, "derivation": "outcome/1"})
	held := runJSON(t, "deal", "respond", "--request", heldReq, "--requester", "x", "--output", filepath.Join(t.TempDir(), "held-reply.json"))
	declinedPath := filepath.Join(t.TempDir(), "declined.json")
	runJSON(t, "deal", "decline", "--request-digest", held["request_digest"].(string), "--output", declinedPath)
	var declined map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, declinedPath), &declined))
	noted := ask(fresh)
	assert.Equal(t, valueShape(declined), valueShape(noted), "a noted deal's refusal has the shape of a holder's decline")
}

func refusalOf(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	r, ok := response["refusal"].(map[string]any)
	require.True(t, ok, "%v", response)
	return r
}

// valueShape is a JSON value with every string and number replaced by its
// type: the members and nesting, not the ids, digests and times.
func valueShape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = valueShape(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = valueShape(e)
		}
		return out
	case string:
		return "string"
	case float64:
		return "number"
	}
	return v
}
