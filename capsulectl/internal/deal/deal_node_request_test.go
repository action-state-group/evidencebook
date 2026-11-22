package deal

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
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

	other := flipLast(signer)
	for name, args := range map[string][]string{
		"another signer":           {"--reply", filepath.Join(dir, "artifact.json"), "--request-digest", digest, "--signer", other},
		"another request":          {"--reply", filepath.Join(dir, "artifact.json"), "--request-digest", flipLast(digest), "--signer", signer},
		"a refusal of another":     {"--reply", filepath.Join(dir, "refusal.json"), "--request-digest", flipLast(digest), "--signer", signer},
		"a refusal by another":     {"--reply", filepath.Join(dir, "refusal.json"), "--request-digest", digest, "--signer", other},
		"a reply that is no reply": {"--reply", writeJSON(t, `{"nope":1}`), "--request-digest", digest, "--signer", signer},
	} {
		assert.Equal(t, false, replyCheck(t, args...)["signed"], name)
	}
	_, err = invoke(t, "", "--profile", "deal", "deal", "node", "reply-check", "--reply", filepath.Join(dir, "artifact.json"))
	require.ErrorIs(t, err, cli.ErrInput)
	_, err = invoke(t, "", "--profile", "deal", "deal", "node", "reply-check", "--reply", filepath.Join(dir, "artifact.json"), "--request-digest", digest, "--signer", "not-a-key")
	require.ErrorIs(t, err, cli.ErrInput, "a mistyped signer is an input error, not signed: false")
	assert.Equal(t, true, replyCheck(t, "--reply", filepath.Join(dir, "artifact.json"), "--request-digest", digest, "--signer", strings.ToUpper(signer))["signed"], "the signer key is read as a key, so its case does not matter")

	// signed is the signature and the request only: a refusal with a reason that is not a registered token is still
	// the responder's signed refusal.
	key, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	edSigner, err := evidencebook.NewEd25519Signer(key)
	require.NoError(t, err)
	refusal, err := evidencerequest.Refusal{RequestDigest: digest, Reason: "made_up_reason", IssuedAt: "2026-09-27T18:00:00Z", KeyID: signer}.Sign(t.Context(), edSigner)
	require.NoError(t, err)
	refusalJSON, err := json.Marshal(refusal)
	require.NoError(t, err)
	nonconforming := writeJSON(t, string(refusalJSON))
	assert.Equal(t, true, replyCheck(t, "--reply", nonconforming, "--request-digest", digest, "--signer", signer)["signed"], "a nonconforming reason is still the responder's signed refusal")
	// An envelope whose artifact no longer matches its signed digest does not parse as a reply at all.
	var envelope map[string]any
	artifactReply, err := os.ReadFile(filepath.Join(dir, "artifact.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(artifactReply, &envelope))
	envelope["artifact"] = base64.StdEncoding.EncodeToString([]byte("not the signed artifact"))
	altered, err := json.Marshal(envelope)
	require.NoError(t, err)
	assert.Equal(t, false, replyCheck(t, "--reply", writeJSON(t, string(altered)), "--request-digest", digest, "--signer", signer)["signed"])
}

// flipLast is s with its last character changed, so it is never s.
func flipLast(s string) string {
	if s[len(s)-1] == '0' {
		return s[:len(s)-1] + "1"
	}
	return s[:len(s)-1] + "0"
}

// Under a policy that serves peers, a record of the node book is answered with its Capsule (digests) and not
// its payload originals: a sealed prompt does not leave with it.
func TestDealNodeRespondUnderPeersDoesNotCarryAPrompt(t *testing.T) {
	dealFixture(t)
	got, err := sealModelCall(t)
	require.NoError(t, err)
	record := got["record"].(map[string]any)["record_id"].(string)
	dir := t.TempDir()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	_, err = nodeVerb(t, "request", "--request", writeJSON(t, `{"subject":{"record":"`+record+`"},"coverage":{"min_freshness":1}}`), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "r.json"))
	require.NoError(t, err)
	policy := writeJSON(t, `{"record_at_completion":"counterparty","history_segments":"peers","adjudications":"deliver_to_subjects"}`)
	answered, err := nodeVerb(t, "respond", "--request", filepath.Join(dir, "r.json"), "--requester", "peer-1", "--policy", policy, "--output", filepath.Join(dir, "a.json"))
	require.NoError(t, err)
	require.Equal(t, "artifact", answered["outcome"], "peers are served a record by its subject")
	reply, err := os.ReadFile(filepath.Join(dir, "a.json"))
	require.NoError(t, err)
	var envelope struct {
		Artifact string `json:"artifact"`
	}
	require.NoError(t, json.Unmarshal(reply, &envelope))
	capsule, err := base64.StdEncoding.DecodeString(envelope.Artifact)
	require.NoError(t, err)
	assert.NotContains(t, string(reply), "messages", "the prompt text is not in the reply")
	assert.NotContains(t, string(capsule), "messages")
	assert.Contains(t, string(capsule), "agent_input_digest", "the Capsule carries the prompt's digest")
}

// Under the default share policy a requester the operator authenticated is not
// served a node book's record by its id either: serving a node book takes a policy that says so.
func TestDealNodeRespondDoesNotServeARecordUnderTheDefaultPolicy(t *testing.T) {
	dealFixture(t)
	appended, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	dir := t.TempDir()
	request := writeJSON(t, `{"subject":{"record":"`+appended.Record.RecordID+`"},"coverage":{"min_freshness":1}}`)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	_, err = nodeVerb(t, "request", "--request", request, "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "r.json"))
	require.NoError(t, err)
	for _, requester := range []string{"", "peer-1"} {
		args := []string{"--request", filepath.Join(dir, "r.json"), "--output", filepath.Join(dir, "a-"+requester+".json")}
		if requester != "" {
			args = append(args, "--requester", requester)
		}
		got, err := nodeVerb(t, "respond", args...)
		require.NoError(t, err)
		assert.Equal(t, "refusal", got["outcome"], "requester %q", requester)
	}
}

// What respond answers from a node book under the default policy, as its help says it does.
func TestDealNodeRespondDefaultPolicyBoundary(t *testing.T) {
	dealFixture(t)
	_, err := nodeAppend(t, "--book", "goose/node", "--type", "goose_joined", "--statement", writeJSON(t, `{"node":"n1"}`))
	require.NoError(t, err)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	cases := []struct {
		name, request, anyone, authenticated string
	}{
		{"checkpoints", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`, "policy_declined", "artifact"},
		{"checkpoint_range", `{"subject":{"checkpoint_range":[1,1]},"coverage":{"min_freshness":1}}`, "policy_declined", "policy_declined"},
		{"history_card/1", `{"subject":{"checkpoints":null},"derivation":"history_card/1","coverage":{"min_freshness":1}}`, "artifact", "artifact"},
		{"history_card/2", `{"subject":{"checkpoints":null},"derivation":"history_card/2","coverage":{"min_freshness":1}}`, "artifact", "artifact"},
		{"standard_history/1", `{"subject":{"checkpoints":null},"derivation":"standard_history/1","coverage":{"min_freshness":1}}`, "derivation_unsupported", "derivation_unsupported"},
		{"standard_history/3", `{"subject":{"checkpoints":null},"derivation":"standard_history/3","coverage":{"min_freshness":1}}`, "derivation_unsupported", "derivation_unsupported"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		_, err := nodeVerb(t, "request", "--request", writeJSON(t, c.request), "--responder", p.TrustedKeys[0], "--output", filepath.Join(dir, "r.json"))
		require.NoError(t, err, c.name)
		for _, who := range []struct{ requester, want string }{{"", c.anyone}, {"peer-1", c.authenticated}} {
			args := []string{"--request", filepath.Join(dir, "r.json"), "--output", filepath.Join(dir, "a-"+who.requester+".json")}
			if who.requester != "" {
				args = append(args, "--requester", who.requester)
			}
			got, err := nodeVerb(t, "respond", args...)
			require.NoError(t, err, c.name)
			want := who.want
			if want == "artifact" {
				assert.Equal(t, "artifact", got["outcome"], "%s requester %q", c.name, who.requester)
			} else {
				assert.Equal(t, "refusal", got["outcome"], "%s requester %q", c.name, who.requester)
				assert.Equal(t, want, got["reason"], "%s requester %q", c.name, who.requester)
			}
		}
	}
}
