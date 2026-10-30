package book

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/action-state-group/evidencebook"
	"github.com/fxamacker/cbor/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/veraison/go-cose"
)

// The book package's test helpers. Until the plugins split (capsule-cli#326)
// they were shared with the deal tests; each package now keeps the ones it
// uses.

func profileFixture(t *testing.T) (cli.Profile, ed25519.PrivateKey) {
	t.Helper()
	public, private, e := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, e)
	p := cli.Profile{Name: "test", Type: "mysql", LogID: "test-log", Log: Name, Namespace: "capsule", Operator: "test-operator"}
	p.Connection.Host = "127.0.0.1"
	p.Connection.Port = 3306
	p.Connection.Database = "capsule_cli_test"
	p.Connection.TLS = "false"
	p.Credentials.Username = "root"
	p.Signing.Value = hex.EncodeToString(private.Seed())
	p.TrustedKeys = []string{hex.EncodeToString(public)}
	p.Checkpoint.Signing = p.Signing
	p.Checkpoint.TrustedKeys = p.TrustedKeys
	return p, private
}

func requestFixture(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"spec_version":"capsule-seal-request/v1","capsule":{"ActionID":"test-action","ActionType":"fyi","Operator":"test-operator","Developer":"test-developer","Timestamp":"2026-09-08T00:00:00Z"},"payload":{"a": 1},"agent_output":{"ok":true}}`)
}

func invoke(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	c := testCommand()
	var out bytes.Buffer
	c.SetIn(strings.NewReader(input))
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	e := c.ExecuteContext(t.Context())
	return out.String(), e
}

// testCommand is the capsulectl tree as a user reaches it with the book
// plugin installed, in one process: the core commands; the book plugin's
// own commands at the top level, where its binary has them (replacing the
// transitional aliases); and `book ...` as the core dispatches it.
func testCommand() *cobra.Command {
	book := &cobra.Command{Use: Name}
	book.AddCommand(append(BookCommands(), BookAliases()...)...)
	return cli.NewCommandWith(append(BookCommands(), book)...)
}

func writeDirectory(t *testing.T, rows ...map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "witnesses.json")
	data, err := json.Marshal(map[string]interface{}{"directory_version": "1", "witnesses": rows})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func verifyBundleOutput(t *testing.T, path string) (map[string]interface{}, error) {
	t.Helper()
	out, err := invoke(t, "", "verify", "--bundle", path)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out), &result), out)
	return result, err
}

func status(result map[string]interface{}, claim string) string {
	return result[claim].(map[string]interface{})["status"].(string)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

func writeJSON(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// coreDir is the capsule-cli module this plugin builds against, as go
// resolves it: its files are read, never written.
var coreDir = sync.OnceValue(func() string {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/action-state-group/capsule-cli").Output()
	if err != nil {
		panic("go list -m github.com/action-state-group/capsule-cli: " + err.Error())
	}
	return strings.TrimSpace(string(out))
})

// installed is capsulectl with the book plugin installed, as an operator has
// it: the core binary in dir, and capsulectl-book in dir/plugins, a trusted
// plugin root. They are built once per test binary.
var installed struct {
	once sync.Once
	dir  string
	err  error
	out  string
}

// installedCapsulectl builds the binaries once and returns the directory
// holding capsulectl. It points CAPSULECTL_PLUGIN_ROOTS at the plugins, so a
// capsulectl the test runs finds them.
func installedCapsulectl(t *testing.T) string {
	t.Helper()
	installed.once.Do(func() {
		installed.dir, installed.err = os.MkdirTemp("", "capsulectl-bin-")
		if installed.err != nil {
			return
		}
		plugins := filepath.Join(installed.dir, "plugins")
		if installed.err = os.Mkdir(plugins, 0o700); installed.err != nil {
			return
		}
		for path, pkg := range map[string]string{
			filepath.Join(installed.dir, "capsulectl"): "github.com/action-state-group/capsule-cli/cmd/capsulectl",
			filepath.Join(plugins, "capsulectl-book"):  "github.com/action-state-group/evidencebook/capsulectl/cmd/capsulectl-book",
		} {
			out, err := exec.Command("go", "build", "-o", path, pkg).CombinedOutput()
			if err != nil {
				installed.err, installed.out = err, string(out)
				return
			}
		}
	})
	require.NoError(t, installed.err, installed.out)
	t.Setenv("CAPSULECTL_PLUGIN_ROOTS", filepath.Join(installed.dir, "plugins"))
	return installed.dir
}

// TestMain runs the tests as capsulectl-book runs: the book backend
// installed in the core tree. It gives the package a private HOME, so a test
// that sets no HOME of its own never touches the real one, and removes the
// built binaries afterwards.
func TestMain(m *testing.M) {
	keepGoCaches()
	home, err := os.MkdirTemp("", "capsulectl-home-")
	if err == nil {
		err = os.Chmod(home, 0o755)
	}
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	cli.RegisterLogBackend(Name, BookBackend{})
	code := m.Run()
	if installed.dir != "" {
		_ = os.RemoveAll(installed.dir)
	}
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// keepGoCaches pins the go env file and the Go build and module caches, as
// go resolves them under the real HOME, before a test HOME replaces it.
func keepGoCaches() {
	out, err := exec.Command("go", "env", "-json", "GOENV", "GOPATH", "GOCACHE", "GOMODCACHE").Output()
	var env map[string]string
	if err != nil || json.Unmarshal(out, &env) != nil {
		return
	}
	for name, value := range env {
		if value != "" && os.Getenv(name) == "" {
			os.Setenv(name, value)
		}
	}
}

const testWitnessURL = "https://witness.test"

// carryReceipt adds to the reply file at path a witness_receipts carrier
// holding one receipt over the reply's anchor checkpoint, signed by key
// under testWitnessURL.
func carryReceipt(t *testing.T, path string, reply evidencebook.Reply, key ed25519.PrivateKey) {
	t.Helper()
	require.NotNil(t, reply.Envelope)
	var artifact struct {
		Checkpoints []string `json:"checkpoints"`
	}
	require.NoError(t, json.Unmarshal(reply.Envelope.Artifact, &artifact))
	require.NotEmpty(t, artifact.Checkpoints)
	statement, err := hex.DecodeString(artifact.Checkpoints[len(artifact.Checkpoints)-1])
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var wire map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &wire))
	wire[evidencebook.WitnessReceiptsMember] = []interface{}{map[string]interface{}{
		"checkpoint": reply.Envelope.Anchor.Digest, "witness": mintReceipt(t, statement, key),
	}}
	raw, err = json.Marshal(wire)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

// mintReceipt is a checkpoint.witnesses entry: a genuine RFC 9162 COSE
// receipt over the checkpoint statement from a single-entry witness log,
// signed by key. The same receipt evidencebook's own tests mint
// (receipt_binding_test.go, adapted from cll-go's witness/verify_test.go).
func mintReceipt(t *testing.T, statement []byte, key ed25519.PrivateKey) map[string]interface{} {
	t.Helper()
	record, err := checkpoint.ParseRecord(statement)
	require.NoError(t, err)
	entry, err := record.EntryHash()
	require.NoError(t, err)
	proof, err := cbor.Marshal([]interface{}{int64(1), int64(0), [][]byte{}})
	require.NoError(t, err)
	leaf := sha256.Sum256(append([]byte{0}, entry...))
	message := cose.NewSign1Message()
	message.Headers.Protected.SetAlgorithm(cose.AlgorithmEdDSA)
	message.Headers.Protected[int64(395)] = int64(1)
	message.Headers.Unprotected[int64(396)] = map[interface{}]interface{}{int64(-1): []interface{}{proof}}
	message.Payload = leaf[:]
	signer, err := cose.NewSigner(cose.AlgorithmEdDSA, key)
	require.NoError(t, err)
	require.NoError(t, message.Sign(rand.Reader, nil, signer))
	message.Payload = nil
	raw, err := message.MarshalCBOR()
	require.NoError(t, err)
	return map[string]interface{}{
		"ts_url": testWitnessURL, "entry_hash": hex.EncodeToString(entry),
		"receipt_b64": base64.StdEncoding.EncodeToString(raw), "leaf_index": 0, "tree_size": 1,
	}
}
