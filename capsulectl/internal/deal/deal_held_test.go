package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func heldList(t *testing.T) []any {
	t.Helper()
	return runJSON(t, "deal", "held")["held"].([]any)
}

// The holder lists a held request, declines it with a signed policy_declined
// refusal, and it leaves the list; declining again rewrites the refusal and
// records nothing more (#178).
func TestDealHeldListAndDecline(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
	assert.Empty(t, heldList(t))
	request := writeJSONFile(t, "req.json", map[string]any{"subject": map[string]any{"kind": "correlation", "value": id},
		"coverage": map[string]any{"min_freshness": map[string]any{"size": 1}}, "derivation": "outcome/1"})
	scratch := t.TempDir()
	reply := filepath.Join(scratch, "response.json")
	held := runJSON(t, "deal", "respond", "--request", request, "--requester", "x", "--output", reply)
	digest := held["request_digest"].(string)

	list := heldList(t)
	require.Len(t, list, 1)
	item := list[0].(map[string]any)
	assert.Equal(t, digest, item["request_digest"])
	assert.Equal(t, "x", item["requester"])
	assert.Equal(t, "outcome/1", item["derivation"])
	assert.Equal(t, id, item["receipt_id"])
	assert.Equal(t, "held, can be declined", item["state"])

	out := filepath.Join(scratch, "refusal.json")
	got := runJSON(t, "deal", "decline", "--request-digest", digest, "--output", out)
	assert.Equal(t, "refusal", got["outcome"])
	assert.Equal(t, "policy_declined", got["reason"])
	assert.NotEmpty(t, got["record_id"])
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	var resp struct {
		Refusal struct {
			RequestDigest string `json:"request_digest"`
			Reason        string `json:"reason"`
			Sig           string `json:"sig"`
		} `json:"refusal"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	assert.Equal(t, digest, resp.Refusal.RequestDigest)
	assert.Equal(t, "policy_declined", resp.Refusal.Reason)
	assert.NotEmpty(t, resp.Refusal.Sig)
	assert.Empty(t, heldList(t), "a declined request is no longer pending")

	again := runJSON(t, "deal", "decline", "--request-digest", digest, "--output", filepath.Join(scratch, "refusal2.json"))
	assert.Empty(t, again["record_id"], "declining twice records nothing more")
	require.Error(t, dealRunErr(t, "respond", "--request", request, "--requester", "x", "--output", reply), "a declined request is not held again")
}

func TestDealDeclineInteropShapeAndErrors(t *testing.T) {
	dealFixture(t)
	scratch := t.TempDir()
	interop := writeFile(t, "interop.json", []byte(`{"subject":{"full_history":null},"coverage":{"min_freshness":1},"derivation":"outcome/1","profile":"evidence-interop/0"}`))
	digest := runJSON(t, "deal", "respond", "--request", interop, "--requester", "x", "--output", filepath.Join(scratch, "r.json"))["request_digest"].(string)
	out := filepath.Join(scratch, "refusal.json")
	assert.Equal(t, "policy_declined", runJSON(t, "deal", "decline", "--request-digest", digest, "--output", out)["reason"])
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "policy_declined")
	assert.Contains(t, string(raw), digest)
	// The refusal conforms to the profile and verifies for the request, even
	// when the clock has a fraction of a second.
	old := cli.DealClock
	cli.DealClock = func() time.Time { return time.Date(2026, 9, 27, 18, 0, 0, 123456789, time.UTC) }
	t.Cleanup(func() { cli.DealClock = old })
	frac := filepath.Join(scratch, "frac.json")
	other := writeFile(t, "other.json", []byte(`{"subject":{"full_history":null},"coverage":{"min_freshness":1},"derivation":"outcome/2","profile":"evidence-interop/0"}`))
	d2 := runJSON(t, "deal", "respond", "--request", other, "--requester", "x", "--output", filepath.Join(scratch, "r2.json"))["request_digest"].(string)
	runJSON(t, "deal", "decline", "--request-digest", d2, "--output", frac)
	fracRaw, err := os.ReadFile(frac)
	require.NoError(t, err)
	reply, err := evidencerequest.ParseReply(evidencerequest.Message{Octets: fracRaw, MediaType: evidencerequest.MediaTypeJSON})
	require.NoError(t, err)
	require.NotNil(t, reply.Refusal)
	assert.NoError(t, reply.Refusal.VerifyFor(reply.Refusal.KeyID, d2))

	require.Error(t, dealRunErr(t, "decline", "--request-digest", "0000", "--output", out), "not a held request")
	require.Error(t, dealRunErr(t, "decline", "--output", out), "digest required")
	require.Error(t, dealRunErr(t, "decline", "--request-digest", digest), "output required")
}

// A refusal that cannot be written (the path exists) is reported with the
// signed response inline, the decision is on record, and a retry elsewhere
// records nothing twice.
func TestDealDeclineWriteFailureKeepsTheDecision(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, crossPartyBuyerOpen))["deal_id"].(string)
	request := writeJSONFile(t, "req.json", map[string]any{"subject": map[string]any{"kind": "correlation", "value": id},
		"coverage": map[string]any{"min_freshness": map[string]any{"size": 1}}, "derivation": "outcome/1"})
	scratch := t.TempDir()
	digest := runJSON(t, "deal", "respond", "--request", request, "--requester", "x", "--output", filepath.Join(scratch, "r.json"))["request_digest"].(string)
	taken := writeFile(t, "taken.json", []byte("keep"))
	outText, err := dealInvoke(t, "", "--profile", "deal", "deal", "decline", "--request-digest", digest, "--output", taken)
	require.Error(t, err)
	assert.Contains(t, outText, "policy_declined")
	assert.Contains(t, outText, `"response"`, "the signed refusal is returned inline")
	kept, _ := os.ReadFile(taken)
	assert.Equal(t, "keep", string(kept), "the existing file is untouched")
	assert.Empty(t, heldList(t), "the decision is on record")
	again := runJSON(t, "deal", "decline", "--request-digest", digest, "--output", filepath.Join(scratch, "retry.json"))
	assert.Empty(t, again["record_id"])
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	rows, err := s.exchangeRows(t.Context(), `kind=?`, dealExchangeAnswered)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "one decision however often it is written")
}
