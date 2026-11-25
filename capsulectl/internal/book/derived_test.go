package book

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Terms Pack served by the digest of its definition, through the real
// commands (evidencebook#606, #753): the holder lists the pack in its profile,
// `respond` answers it, the requester records the answer, runs the pack, and
// `report derive` seals the Result and writes the one bundle that `verify`
// checks and `report build` renders.

// A compiled definition. `document` reaches the book as the file's own bytes,
// large safe integer included; an unsafe one (§5.1) is refused.
const cliPackDocument = `{"pack":"cli-pack","version":1,"big":9007199254740991,"rows":["has-record"]}`

const cliPackContract = "ec:cli-pack@1"

type packExchange struct {
	*evidenceExchange
	digest string
}

func newPackExchange(t *testing.T, packFile string) *packExchange {
	t.Helper()
	x := &packExchange{evidenceExchange: newEvidenceExchange(t)}
	digest, err := documentDigest([]byte(cliPackDocument))
	require.NoError(t, err)
	x.digest = digest
	if packFile != "" {
		x.writePack("pack.json", packFile)
	}
	return x
}

func (x *packExchange) writePack(name, body string) {
	dir := derivationDir(x.b)
	require.NoError(x.t, os.MkdirAll(dir, 0o700))
	require.NoError(x.t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
}

func packFileBody(extra string) string {
	return `{"document":` + cliPackDocument + `,"recipients":["a"],"forms":["full_history"],"fields":["record_type","committed_at"]` + extra + `}`
}

// record asks for the pack, has the holder answer and records the answer; it
// returns the response record.
func (x *packExchange) record() string {
	x.t.Helper()
	sent := x.ask("pack", `{"subject":{"full_history":null},"coverage":{"min_freshness":1},"derivation":"`+x.digest+`"}`)
	x.respond("pack")
	out, err := invoke(x.t, "", "request", "--profile", "a", "--for", sent.RecordID, "--response", filepath.Join(x.dir, "pack.response"),
		"--responder-key", hex.EncodeToString(x.bKeys.record), "--responder-checkpoint-key", hex.EncodeToString(x.bKeys.checkpoint))
	require.NoError(x.t, err, out)
	var got struct {
		RecordID string `json:"record_id"`
		Outcome  string `json:"outcome"`
	}
	require.NoError(x.t, json.Unmarshal([]byte(out), &got), out)
	require.Equal(x.t, evidencebook.OutcomeArtifact, got.Outcome, out)
	return got.RecordID
}

// result is what an evaluator would write: one claim citing the response.
func (x *packExchange) result(response, title string) string {
	claim := map[string]any{
		"id": "has-record", "contract_ref": cliPackContract, "requirement_ref": "has-record", "tier": "recomputed", "grade": "self-attested",
		"sufficiency": "SATISFIED", "verdict": "met", "proofs": []any{},
		"evidence":     []any{map[string]any{"digest": response, "digest_alg": "SHA-256"}},
		"presentation": map[string]any{"kind": "disclosure", "status": "SATISFIED", "evidence": []any{map[string]any{"digest": response, "digest_alg": "SHA-256"}}},
	}
	doc := map[string]any{
		"result_version": "evidence-result-v0", "generated_at": "2026-11-22T00:00:00Z", "claims": []any{claim},
		"aggregate": map[string]any{"buckets": map[string]any{"met": []any{"has-record"}, "not_met": []any{}, "not_evaluable": []any{}},
			"coverage": map[string]any{"evaluated_population": 1, "excluded_not_applicable": 0, "unknown_count": 0}},
	}
	if title != "" {
		doc["view"] = map[string]any{"title": title, "spec_version": "presentation/v1", "producer_name": "requester"}
	}
	raw, err := json.Marshal(doc)
	require.NoError(x.t, err)
	path := filepath.Join(x.dir, "result.json")
	require.NoError(x.t, os.WriteFile(path, raw, 0o600))
	return path
}

func (x *packExchange) derive(response, resultPath, out string, extra ...string) (string, error) {
	args := append([]string{"report", "derive", "--profile", "a", "--response", response, "--pack", x.digest, "--result", resultPath, "--out", out}, extra...)
	return invoke(x.t, "", args...)
}

func TestPackServedByDigestThroughTheCommands(t *testing.T) {
	x := newPackExchange(t, packFileBody(""))
	response := x.record()

	out, err := invoke(t, "", "result", "derived-inputs", "--profile", "a", "--response", response)
	require.NoError(t, err, out)
	var inputs derivedInputsOutput
	require.NoError(t, json.Unmarshal([]byte(out), &inputs), out)
	assert.Equal(t, response, inputs.Response)
	assert.Equal(t, x.digest, inputs.Derivation)
	assert.True(t, inputs.Proven, "the pack file's proofs default to on")
	require.NotEmpty(t, inputs.Records)
	for _, r := range inputs.Records {
		assert.Len(t, r.Digest, 64)
		assert.ElementsMatch(t, []string{"record_type", "committed_at"}, keysOf(r.Fields), "only the declared fields travel")
	}
	// The printed shape is the contract with an evaluator.
	var shape map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &shape))
	for _, member := range []string{"response", "derivation", "anchor", "proven", "records"} {
		assert.Contains(t, shape, member)
	}
	for _, row := range shape["records"].([]any) {
		assert.ElementsMatch(t, []string{"digest", "leaf_index", "fields"}, keysOf(row.(map[string]any)), "each row's members are the contract with an evaluator")
	}

	bundle := filepath.Join(x.dir, "bundle.json")
	out, err = x.derive(response, x.result(response, ""), bundle)
	require.NoError(t, err, out)
	var derived reportDeriveResult
	require.NoError(t, json.Unmarshal([]byte(out), &derived), out)
	assert.NotEmpty(t, derived.ResultRecordID)
	assert.NotEmpty(t, derived.BundleDigest)
	assert.True(t, derived.Proven)

	// One file, two views: the core checks it, and the report page renders it.
	out, err = invoke(t, "", "verify", "--bundle", bundle)
	require.NoError(t, err, out)
	assert.Contains(t, out, `"verdict":"VALID"`)
	assert.Contains(t, out, "contract-report/v1")
	page := filepath.Join(x.dir, "report.html")
	out, err = invoke(t, "", "report", "build", "--bundle", bundle, "--card", "outcome", "--out", page)
	require.NoError(t, err, out)
	assert.Contains(t, out, `"verdict":"VALID"`)
	assert.Contains(t, out, `"unsupported_claims":0`)
	_, err = os.Stat(page)
	require.NoError(t, err)
}

func TestPackFilesAreReadExactly(t *testing.T) {
	cases := map[string]string{
		"unknown member":      packFileBody(`,"extra":1`),
		"duplicate member":    packFileBody(`,"proofs":true,"proofs":false`),
		"case variant":        packFileBody(`,"Proofs":false`),
		"proofs null":         packFileBody(`,"proofs":null`),
		"recipients null":     strings.Replace(packFileBody(""), `"recipients":["a"]`, `"recipients":null`, 1),
		"no document":         `{"forms":["full_history"],"fields":["record_type"]}`,
		"no forms":            `{"document":{"a":1},"fields":["record_type"]}`,
		"not an object":       `["document"]`,
		"data after":          packFileBody("") + ` {}`,
		"a field that is not": strings.Replace(packFileBody(""), `"record_type"`, `"nosuchmember"`, 1),
		"an unsafe integer":   strings.Replace(packFileBody(""), "9007199254740991", "9007199254740993", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			x := newPackExchange(t, body)
			x.ask("pack", `{"subject":{"full_history":null},"coverage":{"min_freshness":1}}`)
			out, err := invoke(t, "", "respond", "--profile", "b", "--request", filepath.Join(x.dir, "pack.sent"), "--requester", "a", "--output", filepath.Join(x.dir, "pack.response"))
			require.Error(t, err, out)
			assert.Contains(t, err.Error()+out, "pack.json", "the file is named")
			_, statErr := os.Stat(filepath.Join(x.dir, "pack.response"))
			assert.ErrorIs(t, statErr, os.ErrNotExist, "nothing was answered")
		})
	}
}

func TestPackFilesForOneDigestAgree(t *testing.T) {
	x := newPackExchange(t, packFileBody(""))
	x.writePack("again.json", packFileBody(""))
	x.ask("pack", `{"subject":{"full_history":null},"coverage":{"min_freshness":1}}`)
	_, err := invoke(t, "", "respond", "--profile", "b", "--request", filepath.Join(x.dir, "pack.sent"), "--requester", "a", "--output", filepath.Join(x.dir, "pack.response"))
	require.NoError(t, err, "a repeated declaration is the same registration")

	x.writePack("differs.json", packFileBody(`,"proofs":false`))
	out, err := invoke(t, "", "respond", "--profile", "b", "--request", filepath.Join(x.dir, "pack.sent"), "--requester", "a", "--output", filepath.Join(x.dir, "other.response"))
	require.Error(t, err, out)
	assert.Contains(t, err.Error(), "again.json")
	assert.Contains(t, err.Error(), "differs.json", "both files are named")
}

func TestUnregisteredOrUngrantedPackIsRefused(t *testing.T) {
	x := newPackExchange(t, "")
	sent := x.ask("pack", `{"subject":{"full_history":null},"coverage":{"min_freshness":1},"derivation":"`+x.digest+`"}`)
	reply := x.respond("pack")
	require.NotNil(t, reply.Refusal, "no pack file: not opted in")
	assert.Equal(t, evidencebook.ReasonDerivationUnsupported, reply.Refusal.Reason)
	_ = sent

	x.writePack("pack.json", packFileBody(""))
	c := testCommand()
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"respond", "--profile", "b", "--request", filepath.Join(x.dir, "pack.sent"), "--requester", "stranger", "--output", filepath.Join(x.dir, "stranger.response")})
	require.NoError(t, c.ExecuteContext(t.Context()))
	// The holder's operator sees the reason the gate decided on, on stderr
	// only; the result, the reply and the record carry policy_declined
	// (evidencebook#580).
	assert.Contains(t, stderr.String(), "refused "+evidencebook.ReasonPolicyDeclined+"; the gate decided "+evidencebook.ReasonNotAuthorized)
	assert.Contains(t, stdout.String(), `"reason":"`+evidencebook.ReasonPolicyDeclined+`"`)
	raw, err := os.ReadFile(filepath.Join(x.dir, "stranger.response"))
	require.NoError(t, err)
	refusal, err := evidencebook.ParseReply(evidencebook.Message{Octets: raw, MediaType: evidencebook.MediaTypeJSON})
	require.NoError(t, err)
	require.NotNil(t, refusal.Refusal, "a requester the pack is not granted to")
	// The CLI answers under the uniform refusal policy, which folds
	// not_authorized into policy_declined (ER -03 §4.2).
	assert.Equal(t, evidencebook.ReasonPolicyDeclined, refusal.Refusal.Reason)
	for _, leaked := range [][]byte{stdout.Bytes(), raw, storeBytes(t, x.b)} {
		assert.NotContains(t, string(leaked), evidencebook.ReasonNotAuthorized, "the decided reason leaves only on stderr")
	}

	// A refusal whose sent reason is the decided one prints nothing extra:
	// derivation_unsupported once the pack is no longer opted in.
	require.NoError(t, os.Remove(filepath.Join(derivationDir(x.b), "pack.json")))
	stdout.Reset()
	stderr.Reset()
	c = testCommand()
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"respond", "--profile", "b", "--request", filepath.Join(x.dir, "pack.sent"), "--requester", "a", "--output", filepath.Join(x.dir, "unsupported.response")})
	require.NoError(t, c.ExecuteContext(t.Context()))
	assert.Contains(t, stdout.String(), `"reason":"`+evidencebook.ReasonDerivationUnsupported+`"`)
	assert.Empty(t, stderr.String())
}

func TestDoctorListsThePacksItServes(t *testing.T) {
	x := newPackExchange(t, strings.Replace(packFileBody(`,"proofs":false`), `"recipients":["a"],`, "", 1))
	out, err := invoke(t, "", "doctor", "--profile", "b")
	require.NoError(t, err, out)
	assert.Contains(t, out, x.digest)
	assert.Contains(t, out, "no recipient")
	assert.Contains(t, out, "no proofs")
}

func TestReportDeriveRefusals(t *testing.T) {
	t.Run("an answer without proofs", func(t *testing.T) {
		x := newPackExchange(t, packFileBody(`,"proofs":false`))
		response := x.record()
		bundle := filepath.Join(x.dir, "bundle.json")
		before := x.resultRecords()
		out, err := x.derive(response, x.result(response, ""), bundle)
		require.Error(t, err, out)
		assert.ErrorIs(t, err, cli.ErrInput, "a refusal is an input error")
		assert.NotContains(t, out, "result_record_id", "nothing was sealed")
		assert.Equal(t, before, x.resultRecords(), "no Result was appended")
		out, err = x.derive(response, x.result(response, ""), bundle, "--allow-unproven")
		require.Error(t, err, out)
		assert.Contains(t, err.Error(), evidencebook.UnprovenInputsTitle, "the title is required")
		out, err = x.derive(response, x.result(response, evidencebook.UnprovenInputsTitle+"CLI pack"), bundle, "--allow-unproven")
		require.NoError(t, err, out)
		assert.Contains(t, out, `"proven":false`)
	})
	t.Run("the wrong pack, a claim citing something else, an existing out", func(t *testing.T) {
		x := newPackExchange(t, packFileBody(""))
		response := x.record()
		bundle := filepath.Join(x.dir, "bundle.json")
		out, err := invoke(t, "", "report", "derive", "--profile", "a", "--response", response, "--pack", strings.Repeat("0", 64), "--result", x.result(response, ""), "--out", bundle)
		require.Error(t, err, out)
		other := x.result(strings.Repeat("1", 64), "")
		out, err = x.derive(response, other, bundle)
		require.Error(t, err, out)
		assert.NoFileExists(t, bundle)
		require.NoError(t, os.WriteFile(bundle, []byte("kept"), 0o600))
		out, err = x.derive(response, x.result(response, ""), bundle)
		require.Error(t, err, out)
		assert.Contains(t, err.Error(), "already exists")
		assert.NotContains(t, out, "result_record_id", "refused before anything was sealed")
		kept, _ := os.ReadFile(bundle)
		assert.Equal(t, "kept", string(kept))
	})
	t.Run("a conduct pack's Result is sealed for the owner and never bundled", func(t *testing.T) {
		x := newPackExchange(t, packFileBody(""))
		response := x.record()
		require.NoError(t, os.MkdirAll(filepath.Join(x.a().Connection.Database, "book"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(x.a().Connection.Database, "book", conductPacksFile), []byte("ec:cli-pack\n"), 0o600))
		bundle := filepath.Join(x.dir, "bundle.json")
		out, err := x.derive(response, x.result(response, ""), bundle)
		require.Error(t, err, out)
		assert.Contains(t, out, "result_record_id", "the sealed Result's id is reported with the failure")
		assert.Contains(t, out, `"proven":true`, "what the answer proved is reported on a late failure too")
		assert.NoFileExists(t, bundle)
		assert.Equal(t, 1, x.resultRecords(), "the Result is sealed for the owner")
	})
	t.Run("an output that cannot be written after the seal", func(t *testing.T) {
		x := newPackExchange(t, packFileBody(""))
		response := x.record()
		blocker := filepath.Join(x.dir, "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("a file, so nothing can be created under it"), 0o600))
		out, err := x.derive(response, x.result(response, ""), filepath.Join(blocker, "bundle.json"))
		require.Error(t, err, out)
		var failed reportDeriveResult
		require.NoError(t, json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &failed), out)
		require.NotEmpty(t, failed.ResultRecordID, "the committed Result's id comes back with the failure")
		// The retry seals nothing new and returns the same Result.
		retried, err := x.derive(response, x.result(response, ""), filepath.Join(x.dir, "bundle.json"))
		require.NoError(t, err, retried)
		assert.Contains(t, retried, failed.ResultRecordID)
		assert.Equal(t, 1, x.resultRecords())
	})
	t.Run("the Result on stdin", func(t *testing.T) {
		x := newPackExchange(t, packFileBody(""))
		response := x.record()
		raw, err := os.ReadFile(x.result(response, ""))
		require.NoError(t, err)
		out, err := invoke(t, string(raw), "report", "derive", "--profile", "a", "--response", response, "--pack", x.digest, "--result", "-", "--out", filepath.Join(x.dir, "bundle.json"))
		require.NoError(t, err, out)
		assert.FileExists(t, filepath.Join(x.dir, "bundle.json"))
	})
}

// resultRecords counts the Evidence Results in the requester's book.
func (x *packExchange) resultRecords() int {
	x.t.Helper()
	opened, err := openBook(x.t.Context(), x.a(), false)
	require.NoError(x.t, err)
	defer func() { require.NoError(x.t, opened.release()) }()
	records, err := opened.book.Query(x.t.Context(), evidencebook.Filter{RecordType: evidencebook.RecordTypeEvidenceResult})
	require.NoError(x.t, err)
	return len(records)
}

// storeBytes is every file under the profile's store, concatenated: the book
// and anything sealed or cut beside it.
func storeBytes(t *testing.T, p cli.Profile) []byte {
	t.Helper()
	var all []byte
	require.NoError(t, filepath.WalkDir(p.Connection.Database, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		all = append(all, b...)
		return err
	}))
	require.NotEmpty(t, all)
	return all
}

func (x *packExchange) a() cli.Profile {
	p, err := cli.LoadProfile("a")
	require.NoError(x.t, err)
	return p
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
