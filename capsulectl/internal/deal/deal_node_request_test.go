package deal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nodeVerb(t *testing.T, verb string, args ...string) (map[string]any, error) {
	t.Helper()
	out, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "node", verb, "--book", "goose/node"}, args...)...)
	if err != nil {
		return nil, err
	}
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	return got, nil
}

// The node asks, answers its own request and records the answer: the three
// signed records of the node book the Goose node's mailbox exchange makes, each
// a verb. The node plays both ends, so the responder keys it pins are its own.
func TestDealNodeRequestRespondAndRecordTheAnswer(t *testing.T) {
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	signer, checkpoint := p.TrustedKeys[0], p.Checkpoint.TrustedKeys[0]
	_, err = nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)

	dir := t.TempDir()
	request := writeJSON(t, `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	asked, err := nodeVerb(t, "request", "--request", request, "--responder", signer, "--output", filepath.Join(dir, "request.json"))
	require.NoError(t, err)
	assert.NotEmpty(t, asked["record_id"])
	assert.Regexp(t, `^[0-9a-f]{64}$`, asked["request_digest"])
	sent, err := os.ReadFile(filepath.Join(dir, "request.json"))
	require.NoError(t, err)
	assert.Contains(t, string(sent), "checkpoints")

	// Under the default share policy an unauthenticated requester is declined a history
	// segment; "peers" serves checkpoints to any requester the operator authenticated.
	policy := writeJSON(t, `{"record_at_completion":"counterparty","history_segments":"peers","adjudications":"deliver_to_subjects"}`)
	answered, err := nodeVerb(t, "respond", "--request", filepath.Join(dir, "request.json"), "--requester", "peer-1", "--policy", policy, "--output", filepath.Join(dir, "reply.json"))
	require.NoError(t, err)
	assert.Equal(t, asked["request_digest"], answered["request_digest"])
	assert.Equal(t, "artifact", answered["outcome"], "%v", answered["reason"])
	assert.NotEmpty(t, answered["record_id"], "the answer is a record of the node book")

	recorded, err := nodeVerb(t, "request", "--response", filepath.Join(dir, "reply.json"), "--for", asked["record_id"].(string),
		"--responder-key", signer, "--responder-checkpoint-key", checkpoint)
	require.NoError(t, err)
	assert.Equal(t, asked["request_digest"], recorded["request_digest"])
	assert.NotEmpty(t, recorded["record_id"])

	// A reply is recorded against its request once: the same reply again is refused.
	_, err = nodeVerb(t, "request", "--response", filepath.Join(dir, "reply.json"), "--for", asked["record_id"].(string),
		"--responder-key", signer, "--responder-checkpoint-key", checkpoint)
	require.Error(t, err)
}

// An answer under keys other than the ones pinned is refused and never recorded,
// so it cannot foreclose the request.
func TestDealNodeRecordAnswerRefusesAnotherSigner(t *testing.T) {
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	signer, checkpoint := p.TrustedKeys[0], p.Checkpoint.TrustedKeys[0]
	dir := t.TempDir()
	asked, err := nodeVerb(t, "request", "--request", writeJSON(t, `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`), "--responder", signer, "--output", filepath.Join(dir, "r.json"))
	require.NoError(t, err)
	policy := writeJSON(t, `{"record_at_completion":"counterparty","history_segments":"peers","adjudications":"deliver_to_subjects"}`)
	_, err = nodeVerb(t, "respond", "--request", filepath.Join(dir, "r.json"), "--requester", "peer-1", "--policy", policy, "--output", filepath.Join(dir, "a.json"))
	require.NoError(t, err)
	other := signer[:63] + "0"
	if other == signer {
		other = signer[:63] + "1"
	}
	_, err = nodeVerb(t, "request", "--response", filepath.Join(dir, "a.json"), "--for", asked["record_id"].(string), "--responder-key", other, "--responder-checkpoint-key", checkpoint)
	require.ErrorIs(t, err, cli.ErrInput)
	// The request is still open: the right keys record the answer.
	_, err = nodeVerb(t, "request", "--response", filepath.Join(dir, "a.json"), "--for", asked["record_id"].(string), "--responder-key", signer, "--responder-checkpoint-key", checkpoint)
	require.NoError(t, err)
}

func TestDealNodeRecordAbsence(t *testing.T) {
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	dir := t.TempDir()
	asked, err := nodeVerb(t, "request", "--request", writeJSON(t, `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "r.json"))
	require.NoError(t, err)

	// The window has not closed: refused, and nothing is recorded.
	_, err = nodeVerb(t, "request", "--for", asked["record_id"].(string), "--absent-until", "2099-01-01T00:00:00Z")
	require.ErrorIs(t, err, cli.ErrInput)
	// A window must end at least a minute after the request was recorded, and the
	// book records an absence only once the window has closed: two hours on.
	earlier := cli.DealClock
	cli.DealClock = func() time.Time { return earlier().Add(2 * time.Hour) }
	t.Cleanup(func() { cli.DealClock = earlier })
	got, err := nodeVerb(t, "request", "--for", asked["record_id"].(string), "--absent-until", earlier().Add(time.Hour).Format(time.RFC3339))
	require.NoError(t, err)
	assert.Equal(t, "recorded_absence", got["outcome"])
	assert.NotEmpty(t, got["record_id"])
}

func TestDealNodeRequestAndRespondInputErrors(t *testing.T) {
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	dir := t.TempDir()
	good := writeJSON(t, `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	for name, args := range map[string][]string{
		"no responder":              {"--request", good, "--output", filepath.Join(dir, "x")},
		"a subject with two forms":  {"--request", writeJSON(t, `{"subject":{"checkpoints":null,"full_history":null},"coverage":{"min_freshness":1}}`), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "x")},
		"an unknown subject form":   {"--request", writeJSON(t, `{"subject":{"nope":null},"coverage":{"min_freshness":1}}`), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "x")},
		"a null range position":     {"--request", writeJSON(t, `{"subject":{"range":[null,1]},"coverage":{"min_freshness":1}}`), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "x")},
		"a bad min_freshness":       {"--request", writeJSON(t, `{"subject":{"checkpoints":null},"coverage":{"min_freshness":null}}`), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "x")},
		"a witness directory alone": {"--request", good, "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "x"), "--witness-directory", writeJSON(t, `{}`)},
	} {
		_, err := nodeVerb(t, "request", args...)
		require.Error(t, err, name)
	}
	_, err = invoke(t, "", "--profile", "deal", "deal", "node", "request", "--request", good, "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "x"))
	require.ErrorIs(t, err, cli.ErrInput, "--book is required")
	for _, book := range []string{"deal/deal-0000000000000000", "journal", p.LogID} {
		_, err := invoke(t, "", "--profile", "deal", "deal", "node", "respond", "--book", book, "--request", good, "--output", filepath.Join(dir, "y"))
		require.ErrorIs(t, err, cli.ErrInput, book)
	}
	_, statErr := os.Stat(filepath.Join(dir, "x"))
	assert.True(t, os.IsNotExist(statErr), "a refused request writes no file")
}

func replyCheck(t *testing.T, args ...string) map[string]any {
	t.Helper()
	out, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "node", "reply-check"}, args...)...)
	require.NoError(t, err, out)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	return got
}

// The check a late reply gets: signed by the named responder for the named
// request, an artifact or a refusal; anything else is signed false, not an error.
func TestDealNodeReplyCheck(t *testing.T) {
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	signer := p.TrustedKeys[0]
	dir := t.TempDir()
	request := writeJSON(t, `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	asked, err := nodeVerb(t, "request", "--request", request, "--responder", signer, "--output", filepath.Join(dir, "r.json"))
	require.NoError(t, err)
	digest := asked["request_digest"].(string)
	policy := writeJSON(t, `{"record_at_completion":"counterparty","history_segments":"peers","adjudications":"deliver_to_subjects"}`)
	// Under the default policy an unauthenticated requester gets a signed refusal.
	refused, err := nodeVerb(t, "respond", "--request", filepath.Join(dir, "r.json"), "--output", filepath.Join(dir, "refusal.json"))
	require.NoError(t, err)
	require.Equal(t, "refusal", refused["outcome"])
	_, err = nodeVerb(t, "respond", "--request", filepath.Join(dir, "r.json"), "--requester", "peer-1", "--policy", policy, "--output", filepath.Join(dir, "artifact.json"))
	require.NoError(t, err)

	got := replyCheck(t, "--reply", filepath.Join(dir, "artifact.json"), "--request-digest", digest, "--signer", signer)
	assert.Equal(t, true, got["signed"])
	assert.Equal(t, "artifact", got["kind"])
	got = replyCheck(t, "--reply", filepath.Join(dir, "refusal.json"), "--request-digest", digest, "--signer", signer)
	assert.Equal(t, true, got["signed"])
	assert.Equal(t, "refusal", got["kind"])

	other := signer[:63] + "0"
	if other == signer {
		other = signer[:63] + "1"
	}
	for name, args := range map[string][]string{
		"another signer":           {"--reply", filepath.Join(dir, "artifact.json"), "--request-digest", digest, "--signer", other},
		"another request":          {"--reply", filepath.Join(dir, "artifact.json"), "--request-digest", digest[:63] + "0", "--signer", signer},
		"a refusal of another":     {"--reply", filepath.Join(dir, "refusal.json"), "--request-digest", digest[:63] + "0", "--signer", signer},
		"a refusal by another":     {"--reply", filepath.Join(dir, "refusal.json"), "--request-digest", digest, "--signer", other},
		"a reply that is no reply": {"--reply", writeJSON(t, `{"nope":1}`), "--request-digest", digest, "--signer", signer},
	} {
		assert.Equal(t, false, replyCheck(t, args...)["signed"], name)
	}
	_, err = invoke(t, "", "--profile", "deal", "deal", "node", "reply-check", "--reply", filepath.Join(dir, "artifact.json"))
	require.ErrorIs(t, err, cli.ErrInput)
}
