package book

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evidenceExchange is one requester (a) and one responder (b) with a book of
// one exchange, and helpers for the evidence-request round trip.
type evidenceExchange struct {
	t     *testing.T
	dir   string
	b     cli.Profile
	bKeys bookKeys
}

func newEvidenceExchange(t *testing.T) *evidenceExchange {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	a, _ := bookProfile(t, "a")
	initBook(t, a)
	b, bKeys := bookProfile(t, "b")
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(day1)
	return &evidenceExchange{t: t, dir: t.TempDir(), b: b, bKeys: bKeys}
}

func (x *evidenceExchange) ask(name, body string) cli.EvidenceRequestResult {
	path := filepath.Join(x.dir, name+".json")
	require.NoError(x.t, os.WriteFile(path, []byte(body), 0o600))
	out, err := invoke(x.t, "", "request", "--profile", "a", "--request", path, "--responder", "b", "--output", filepath.Join(x.dir, name+".sent"))
	require.NoError(x.t, err, out)
	var r cli.EvidenceRequestResult
	require.NoError(x.t, json.Unmarshal([]byte(out), &r))
	return r
}

func (x *evidenceExchange) respond(name string) evidencebook.Reply {
	return x.respondAs(name, name)
}

// respondAs answers the request sent as name, writing the reply as out.
func (x *evidenceExchange) respondAs(name, out string) evidencebook.Reply {
	_, err := invoke(x.t, "", "respond", "--profile", "b", "--request", filepath.Join(x.dir, name+".sent"), "--requester", "a", "--output", filepath.Join(x.dir, out+".response"))
	require.NoError(x.t, err)
	raw, err := os.ReadFile(filepath.Join(x.dir, out+".response"))
	require.NoError(x.t, err)
	reply, err := evidencebook.ParseReply(evidencebook.Message{Octets: raw, MediaType: evidencebook.MediaTypeJSON})
	require.NoError(x.t, err)
	return reply
}

// The witness standing `book request --response` prints (evidencebook#209):
// "witnessed" only when a receipt the reply carries (witness_receipts), from a
// witness in --witness-directory, covers the answer's anchor checkpoint;
// "unwitnessed" for a chain that verifies only against its holder's own
// signatures. A receipt that does not verify counts for nothing.
func TestBookResponsePrintsWitnessStanding(t *testing.T) {
	witnessKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{77}, ed25519.SeedSize))
	holderKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{78}, ed25519.SeedSize))
	listed := []map[string]interface{}{{"name": "test witness", "endpoint": testWitnessURL, "binding": "cll",
		"key_ids": []string{hex.EncodeToString(witnessKey.Public().(ed25519.PublicKey))}}}
	for name, tc := range map[string]struct {
		key       ed25519.PrivateKey // the receipt's signer; nil carries none
		directory []map[string]interface{}
		want      string
	}{
		"no receipt, no directory":      {nil, nil, evidencebook.Unwitnessed},
		"no receipt":                    {nil, listed, evidencebook.Unwitnessed},
		"receipt, no directory":         {witnessKey, nil, evidencebook.Unwitnessed},
		"receipt from a listed witness": {witnessKey, listed, evidencebook.Witnessed},
		"receipt forged under its name": {holderKey, listed, evidencebook.Unwitnessed},
	} {
		t.Run(name, func(t *testing.T) {
			x := newEvidenceExchange(t)
			sent := x.ask("history", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
			reply := x.respond("history")
			path := filepath.Join(x.dir, "history.response")
			if tc.key != nil {
				carryReceipt(t, path, reply, tc.key)
			}
			args := []string{"request", "--profile", "a", "--for", sent.RecordID, "--response", path,
				"--responder-key", hex.EncodeToString(x.bKeys.record), "--responder-checkpoint-key", hex.EncodeToString(x.bKeys.checkpoint)}
			if tc.directory != nil {
				args = append(args, "--witness-directory", writeDirectory(t, tc.directory...))
			}
			out, err := invoke(t, "", args...)
			require.NoError(t, err, out)
			var got struct {
				Outcome, Witness string
			}
			require.NoError(t, json.Unmarshal([]byte(out), &got), out)
			assert.Equal(t, evidencebook.OutcomeArtifact, got.Outcome)
			assert.Equal(t, tc.want, got.Witness)
		})
	}
}

// A refusal has no witness standing, and a directory that cannot be read
// records nothing, so the genuine answer still records after it.
func TestBookResponseWitnessEdges(t *testing.T) {
	x := newEvidenceExchange(t)
	sent := x.ask("history", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	x.respond("history")
	path := filepath.Join(x.dir, "history.response")
	keys := []string{"--responder-key", hex.EncodeToString(x.bKeys.record), "--responder-checkpoint-key", hex.EncodeToString(x.bKeys.checkpoint)}
	base := append([]string{"request", "--profile", "a", "--for", sent.RecordID, "--response", path}, keys...)

	_, err := invoke(t, "", append(base, "--witness-directory", filepath.Join(t.TempDir(), "no-such-witnesses.json"))...)
	assert.ErrorIs(t, err, cli.ErrInput)
	bad := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	_, err = invoke(t, "", append(base, "--witness-directory", bad)...)
	assert.ErrorIs(t, err, cli.ErrInput)
	out, err := invoke(t, "", base...)
	require.NoError(t, err, out, "the answer still records after the refused directories")
	assert.Contains(t, out, `"witness":"unwitnessed"`)

	// Without --requester a protected subject is refused: the refusal records
	// with no witness member.
	refused := x.ask("records", `{"subject":{"full_history":null},"coverage":{"min_freshness":1}}`)
	_, err = invoke(t, "", "respond", "--profile", "b", "--request", filepath.Join(x.dir, "records.sent"), "--output", filepath.Join(x.dir, "records.response"))
	require.NoError(t, err)
	out, err = invoke(t, "", append([]string{"request", "--profile", "a", "--for", refused.RecordID, "--response", filepath.Join(x.dir, "records.response")}, keys...)...)
	require.NoError(t, err, out)
	assert.Contains(t, out, `"outcome":"`+evidencebook.OutcomeRefusal+`"`)
	assert.NotContains(t, out, `"witness"`)

	_, err = invoke(t, "", "request", "--profile", "a", "--for", sent.RecordID, "--absent-until", day1.Format(time.RFC3339), "--witness-directory", writeDirectory(t))
	assert.ErrorIs(t, err, cli.ErrInput, "--witness-directory goes with --response only")
}

// A fixed pin: the same pinned request, answered before and after the log
// grows by unrelated records and a newer checkpoint, gets the same artifact
// bytes: the pin is the anchor, never the latest checkpoint.
func TestFixedPinArtifactSurvivesLogGrowth(t *testing.T) {
	x := newEvidenceExchange(t)
	x.ask("first", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	first := x.respond("first")
	require.NotNil(t, first.Envelope)
	anchor := first.Envelope.Anchor.Digest
	x.ask("pinned", `{"subject":{"checkpoints":null},"coverage":{"expected_pin":"`+anchor+`"}}`)
	before := x.respond("pinned").Envelope
	require.NotNil(t, before)

	appendHalves(t, x.b, half{"x2", "r2", "p2"}, half{"x3", "r3", "p3"})
	x.ask("grow", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":`+strconv.FormatUint(bookSize(t, x.b), 10)+`}}`)
	grown := x.respond("grow").Envelope
	require.NotNil(t, grown)
	require.NotEqual(t, anchor, grown.Anchor.Digest, "the log grew and a newer checkpoint was issued")

	after := x.respondAs("pinned", "pinned-again").Envelope
	require.NotNil(t, after)
	require.Equal(t, before.Anchor.Digest, after.Anchor.Digest)
	require.Equal(t, string(before.Artifact), string(after.Artifact), "a fixed pin's artifact does not change as the log grows")
}

// Without --requester the request's sender is unauthenticated: a protected
// subject is refused not_authorized and nothing is recorded, while the
// public checkpoints subject is still answered.
func TestRespondWithoutRequesterAnswersOnlyPublicSubjects(t *testing.T) {
	x := newEvidenceExchange(t)
	unauthenticated := func(name string) cli.EvidenceRespondResult {
		out, err := invoke(t, "", "respond", "--profile", "b", "--request", filepath.Join(x.dir, name+".sent"), "--output", filepath.Join(x.dir, name+".response"))
		require.NoError(t, err, out)
		var r cli.EvidenceRespondResult
		require.NoError(t, json.Unmarshal([]byte(out), &r))
		return r
	}
	sent := x.ask("history", `{"subject":{"full_history":null},"coverage":{"min_freshness":1}}`)
	size := bookSize(t, x.b)
	got := unauthenticated("history")
	assert.Equal(t, evidencebook.OutcomeRefusal, got.Outcome)
	assert.Equal(t, evidencebook.ReasonNotAuthorized, got.Reason)
	assert.Equal(t, sent.RequestDigest, got.RequestDigest)
	assert.Empty(t, got.RecordID, "unauthenticated traffic is not recorded")
	assert.Equal(t, size, bookSize(t, x.b))

	// An unauthenticated request never makes the book checkpoint: it is
	// answered from a checkpoint an authenticated answer already cut.
	x.ask("cut", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	x.respond("cut")
	x.ask("checkpoints", `{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`)
	size = bookSize(t, x.b)
	got = unauthenticated("checkpoints")
	assert.Equal(t, evidencebook.OutcomeArtifact, got.Outcome, got.Reason)
	assert.Empty(t, got.RecordID)
	assert.Equal(t, size, bookSize(t, x.b))
}

// request --request reads the request map's wire shape; a subject that is
// not one form, or a coverage value of the wrong type, is an input error,
// and values are checked by the book.
func TestBookRequestInput(t *testing.T) {
	digest := hex.EncodeToString(make([]byte, 32))
	size := uint64(3)
	for name, tc := range map[string]struct {
		body string
		want evidencebook.EvidenceRequest
	}{
		"full history": {`{"subject":{"full_history":null},"coverage":{"min_freshness":3}}`,
			evidencebook.EvidenceRequest{Subject: evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, Coverage: evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Size: &size}}}},
		"record, pinned": {`{"subject":{"record":"` + digest + `"},"coverage":{"expected_pin":"` + digest + `"},"nonce":"n","route":"r"}`,
			evidencebook.EvidenceRequest{Subject: evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: digest}, Coverage: evidencebook.Coverage{ExpectedPin: digest}, Nonce: "n", Route: "r"}},
		"range, by time": {`{"subject":{"range":[0,2]},"coverage":{"min_freshness":"2026-09-25T00:00:00Z"},"derivation":"history_card/1"}`,
			evidencebook.EvidenceRequest{Subject: evidencebook.Subject{Kind: evidencebook.SubjectRange, Range: [2]uint64{0, 2}}, Coverage: evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Time: "2026-09-25T00:00:00Z"}}, Derivation: "history_card/1"}},
		"correlation": {`{"subject":{"correlation":"deal-1"},"coverage":{"min_freshness":3},"profile":"evidence-interop/0"}`,
			evidencebook.EvidenceRequest{Subject: evidencebook.Subject{Kind: evidencebook.SubjectCorrelation, Correlation: "deal-1"}, Coverage: evidencebook.Coverage{MinFreshness: &evidencebook.Freshness{Size: &size}}, Profile: evidencebook.ProfileID}},
	} {
		got, err := parseBookRequest("--request", []byte(tc.body))
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)
	}
	for name, body := range map[string]string{
		"old shape":          `{"subject":{"kind":"full_history"},"coverage":{"min_freshness":{"size":1}}}`,
		"two forms":          `{"subject":{"full_history":null,"checkpoints":null},"coverage":{"min_freshness":1}}`,
		"no subject":         `{"coverage":{"min_freshness":1}}`,
		"unknown form":       `{"subject":{"everything":null},"coverage":{"min_freshness":1}}`,
		"content on history": `{"subject":{"full_history":"x"},"coverage":{"min_freshness":1}}`,
		"range not a pair":   `{"subject":{"range":[1]},"coverage":{"min_freshness":1}}`,
		"null floor":         `{"subject":{"full_history":null},"coverage":{"min_freshness":null}}`,
		"negative floor":     `{"subject":{"full_history":null},"coverage":{"min_freshness":-1}}`,
		"unknown member":     `{"subject":{"full_history":null},"coverage":{"min_freshness":1},"extra":1}`,
	} {
		_, err := parseBookRequest("--request", []byte(body))
		assert.ErrorIs(t, err, cli.ErrInput, name)
	}

	// A value the wire shape admits but the profile does not is refused by
	// the book, before anything is recorded.
	x := newEvidenceExchange(t)
	path := writeJSON(t, `{"subject":{"record":"not a digest"},"coverage":{"min_freshness":1}}`)
	_, err := invoke(t, "", "request", "--profile", "a", "--request", path, "--responder", "b", "--output", filepath.Join(x.dir, "bad.sent"))
	assert.ErrorIs(t, err, evidencebook.ErrInvalid)
}

// TestBookRequestSent is evidencebook#245: the capsules plugin's sealed
// requests from live-16 07-ask carry members `--request` does not define
// (audience, requester_id, requester_auth), so they are refused without
// --sent. With it each is recorded as sent, under the digest stevenmih-1
// answered, and the reply it got verifies as an artifact.
func TestBookRequestSent(t *testing.T) {
	x := newEvidenceExchange(t)
	const key = "1dd25d9938ba4841f5e127e39e8dcc71df82b2072d9186ece75e431063ab26d9"
	for _, name := range []string{"checkpoints", "history_card", "exchange"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "live16-ask", name+".json"))
		require.NoError(t, err)
		var fixture struct {
			Request json.RawMessage `json:"request"`
			Reply   json.RawMessage `json:"reply"`
		}
		require.NoError(t, json.Unmarshal(raw, &fixture))
		var reply struct {
			RequestDigest string `json:"request_digest"`
		}
		require.NoError(t, json.Unmarshal(fixture.Reply, &reply))
		request, output := writeJSON(t, string(fixture.Request)), filepath.Join(x.dir, name+".sent")
		_, err = invoke(t, "", "request", "--profile", "a", "--request", request, "--responder", "stevenmih-1", "--output", output)
		assert.ErrorIs(t, err, cli.ErrInput, name)

		out, err := invoke(t, "", "request", "--profile", "a", "--request", request, "--sent", "--responder", "stevenmih-1", "--output", output)
		require.NoError(t, err, out)
		var asked cli.EvidenceRequestResult
		require.NoError(t, json.Unmarshal([]byte(out), &asked))
		assert.Equal(t, reply.RequestDigest, asked.RequestDigest, name)
		written, err := os.ReadFile(output)
		require.NoError(t, err)
		assert.Contains(t, string(written), `"audience":`, name)

		answer := writeJSON(t, string(fixture.Reply))
		out, err = invoke(t, "", "request", "--profile", "a", "--for", asked.RecordID, "--response", answer, "--responder-key", key, "--responder-checkpoint-key", key)
		require.NoError(t, err, out)
		var answered bookResponseResult
		require.NoError(t, json.Unmarshal([]byte(out), &answered))
		assert.Equal(t, evidencebook.OutcomeArtifact, answered.Outcome, name)
	}

	// --sent records nothing but a request, and only a well-formed one.
	_, err := invoke(t, "", "request", "--profile", "a", "--sent", "--for", "x", "--response", writeJSON(t, `{}`), "--responder-key", key, "--responder-checkpoint-key", key)
	assert.ErrorIs(t, err, cli.ErrInput, "--sent without --request")
	for name, tc := range map[string]struct {
		body string
		want error
	}{
		"not JSON":        {`{"subject":`, cli.ErrInput},
		"no subject":      {`{"coverage":{"min_freshness":1},"audience":"a"}`, evidencebook.ErrInvalid},
		"unknown profile": {`{"subject":{"checkpoints":null},"coverage":{"min_freshness":1},"profile":"evidence-interop/9"}`, evidencebook.ErrInvalid},
	} {
		_, err := invoke(t, "", "request", "--profile", "a", "--request", writeJSON(t, tc.body), "--sent", "--responder", "b", "--output", filepath.Join(x.dir, "bad.sent"))
		assert.ErrorIs(t, err, tc.want, name)
	}
}
