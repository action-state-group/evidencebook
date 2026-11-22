package deal

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	emit "github.com/action-state-group/capsule-emit/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sealModelCall(t *testing.T, extra ...string) (map[string]any, error) {
	t.Helper()
	args := append([]string{"--book", "goose/node", "--type", "goose_model_call", "--provider", "acme", "--model-id", "m-1", "--developer", "test-dev",
		"--prompt", writeJSON(t, `{"messages":[{"role":"user","content":"hi"}]}`), "--completion", writeJSON(t, `{"text":"hello"}`)}, extra...)
	out, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "node", "seal-model-call"}, args...)...)
	if err != nil {
		return nil, err
	}
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	return got, nil
}

// verifiedAsGooseCapsule is the check test/interop/goose-capsule makes of a
// Capsule the Python node sealed: class 1 verification, the producer envelope
// under the key_id the Capsule carries, and the effect and model members the
// loop's step 01 reads.
func verifiedAsGooseCapsule(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var capsule map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&capsule))
	signature, _ := capsule["signature"].(string)
	keyID, _ := capsule["key_id"].(string)
	capsuleID, _ := capsule["capsule_id"].(string)
	delete(capsule, "signature")
	delete(capsule, "key_id")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(capsule))
	_, err := emit.VerifyCapsule(bytes.TrimSpace(buf.Bytes()))
	require.NoError(t, err, "class 1")
	envelope, err := hex.DecodeString(signature)
	require.NoError(t, err)
	require.NotEmpty(t, signature)
	verified, err := emit.VerifyEnvelope(capsuleID, envelope)
	require.NoError(t, err)
	assert.Equal(t, keyID, hex.EncodeToString(verified.PublicKey), "signed under the key id the Capsule carries")
	capsule["key_id"] = keyID
	return capsule
}

func TestDealNodeSealModelCall(t *testing.T) {
	dealFixture(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "capsule.json")
	got, err := sealModelCall(t, "--out", out)
	require.NoError(t, err)
	assert.Equal(t, out, got["file"])

	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	capsule := verifiedAsGooseCapsule(t, raw)
	assert.Equal(t, p.TrustedKeys[0], capsule["key_id"], "the profile's key signs it")
	effect := capsule["effect"].(map[string]any)
	assert.Equal(t, "runtime_claimed", effect["effect_attestation"])
	assert.Equal(t, "inference_completion", effect["type"])
	assert.Equal(t, "confirmed", effect["status"])
	assert.Equal(t, "two_way", effect["irreversibility_class"])
	model := capsule["model_attestation"].(map[string]any)
	assert.Equal(t, "acme", model["provider"])
	assert.Equal(t, "m-1", model["model_id"])
	assert.Equal(t, "fyi", capsule["action_type"])
	assert.Equal(t, got["capsule_id"], capsule["capsule_id"])

	records := nodeRecords(t, "--book", "goose/node", "--type", "goose_model_call")
	require.Len(t, records, 1)
	assert.Equal(t, capsule["action_id"], records[0].SubjectRef)
	assert.Regexp(t, `^model-call-[0-9a-f]{16}$`, records[0].SubjectRef)
}

// The prompt stays on the node as the record's payload; the completion is only
// committed by digest, never stored.
func TestDealNodeSealModelCallKeepsThePromptAndNotTheCompletion(t *testing.T) {
	dealFixture(t)
	got, err := sealModelCall(t)
	require.NoError(t, err)
	capsule, err := json.Marshal(got["capsule"])
	require.NoError(t, err)
	verifiedAsGooseCapsule(t, capsule)
	record := got["record"].(map[string]any)
	assert.NotEmpty(t, record["record_id"])
	assert.NotContains(t, string(capsule), "hello", "the completion is not in the Capsule")
	assert.NotContains(t, string(capsule), "\"hi\"", "nor is the prompt: the Capsule carries digests only")
	assert.Contains(t, string(capsule), "test-dev", "the developer the caller named")

	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	held, err := s.originals.Get(t.Context(), got["capsule_id"].(string))
	require.NoError(t, err)
	names := map[string]string{}
	for _, a := range held.Artifacts {
		names[a.Name] = string(a.Content)
	}
	assert.JSONEq(t, `{"messages":[{"role":"user","content":"hi"}]}`, names["payload"])
	assert.NotContains(t, names, "agent_output", "the completion is kept by digest only")
}

func TestDealNodeSealModelCallRefusals(t *testing.T) {
	dealFixture(t)
	existing := writeJSON(t, `keep`)
	for name, extra := range map[string][]string{
		"an existing --out":             {"--out", existing},
		"a library type":                {"--type", "index_root"},
		"no provider":                   {"--provider", ""},
		"no model id":                   {"--model-id", ""},
		"a prompt that is not JSON":     {"--prompt", writeJSON(t, `nope`)},
		"a deal's book":                 {"--book", "deal/deal-0000000000000000"},
		"no developer":                  {"--developer", ""},
		"a null prompt":                 {"--prompt", writeJSON(t, `null`)},
		"a null completion":             {"--completion", writeJSON(t, `null`)},
		"trailing data in a prompt":     {"--prompt", writeJSON(t, `{"a":1} garbage`)},
		"two values in a completion":    {"--completion", writeJSON(t, `{"a":1}`+"\n"+`{"b":2}`)},
		"a completion that is not JSON": {"--completion", writeJSON(t, `nope`)},
	} {
		_, err := sealModelCall(t, extra...)
		require.Error(t, err, name)
	}
	kept, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(kept))
	assert.Empty(t, nodeRecords(t, "--book", "goose/node"), "a refused call seals nothing")
}
