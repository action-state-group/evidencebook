// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v0OldCopies holds x-deal-v0 copies written before u225 (evidencebook#958),
// when a copy still carried its witnessed cadence chain as
// parent-log-witness/v1, with the witness directory that checks them. They
// are written once, by the code before that change
// (EB_WRITE_V0_OLD_COPIES=1), and never regenerated: they stand for copies
// already in people's hands, which must keep verifying.
const v0OldCopies = "testdata/v0-old-copies"

var v0OldCopyFiles = map[string]string{
	"report-whole.json": "a deal report whose chain covers every step",
	"report-part.json":  "a deal report whose chain covers an earlier checkpoint only",
	"sale.json":         "a sale bundle",
}

func TestWriteV0OldCopies(t *testing.T) {
	if os.Getenv("EB_WRITE_V0_OLD_COPIES") != "1" {
		t.Skip("writes the fixtures only on request; they predate u225 and are not regenerated")
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 18, 24, 0, 0, time.UTC))
	dealID := retailDeal(t)
	saleID, _ := newSale(t)
	*now = now.Add(2 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	require.NoError(t, os.MkdirAll(v0OldCopies, 0o755))

	whole := filepath.Join(v0OldCopies, "report-whole.json")
	dealRun(t, "report", "--deal", dealID, "--bundle", whole)
	dealRun(t, "sale", "bundle", "--sale", saleID, "--out", filepath.Join(v0OldCopies, "sale.json"))

	// A later step no tick has covered yet: the chain covers the earlier checkpoint.
	dealRun(t, "note", "--deal", dealID, "--kind", "act", "--input", filepath.Join(retailDemo, "act-pay.json"))
	dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(v0OldCopies, "report-part.json"))

	directory, err := json.Marshal(map[string]any{"directory_version": "1", "witnesses": []any{rawKeyRow(endpoint, public)}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(v0OldCopies, "witnesses.json"), directory, 0o644))
}

// A v0 copy written before u225 still verifies, its chain passing under the
// directory it was witnessed by, and a tampered chain fails.
func TestV0OldCopiesStillVerify(t *testing.T) {
	dealFixture(t)
	directory := filepath.Join(v0OldCopies, "witnesses.json")
	for name, what := range v0OldCopyFiles {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(v0OldCopies, name)
			b := readBundle(t, path)
			require.Contains(t, b["extensions"], DealParentWitnessExtension, what)
			result, err := verifyWithDirectory(t, path, directory)
			require.NoError(t, err, what)
			assert.Equal(t, "VALID", result["verdict"], what)
			assert.Equal(t, "pass", result["reader_witnesses"].(map[string]any)["status"], what)

			b["extensions"].(map[string]any)[DealParentWitnessExtension].(map[string]any)["salt"] = "0000"
			result, _ = verifyWithDirectory(t, writeBundle(t, b), directory)
			assert.NotEqual(t, "VALID", result["verdict"], what)
		})
	}
}

// withHolderChain writes a copy of the bundle at path with the holder's own
// witnessed chain for its checkpoint added as parent-log-witness/v1, as a
// copy carried it before u225: the chain's proof machinery is still the
// holder's own witness check and still verifies old copies, so tests of the
// chain itself run on it. It fails the test when the holder holds no
// witnessed chain for the checkpoint.
func withHolderChain(t *testing.T, p cli.Profile, dealID, path string) string {
	t.Helper()
	b := readBundle(t, path)
	statement, err := base64.RawURLEncoding.DecodeString(b["checkpoint"].(map[string]any)["cose"].(string))
	require.NoError(t, err)
	var chain map[string]interface{}
	withProfileSession(t, p, func(s *dealSession) {
		chain, err = s.dealWitnessState(t.Context(), dealID, statement)
		require.NoError(t, err)
	})
	name, block, ok := cadenceExtensionEntry(chain)
	require.True(t, ok, "the holder holds a witnessed chain: %v", chain["state"])
	raw, err := json.Marshal(block)
	require.NoError(t, err)
	var decoded any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	b["extensions"].(map[string]any)[name] = decoded
	return writeBundle(t, b)
}

func withProfileSession(t *testing.T, p cli.Profile, fn func(s *dealSession)) {
	t.Helper()
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.close()) }()
	fn(s)
}

// cadenceExtensionEntry is the extension that carried a cadence chain before
// u225 and u230 (no bundle carries one now); tests build old-copy chains with
// it (withHolderChain).
//
// It is the extension that carries a cadence chain
// (dealWitnessState's map) in a bundle: a witnessed chain as the
// parent-log-witness/v1 block, which is the chain with its "cadence" member
// named "parent" as -03 calls it. Members -03 does not define (state, extent,
// checkpoint_at, steps_witnessed, steps, rest) stay; a verifier ignores them
// and computes the extent from the checkpoints. A state that is not a
// witnessed chain (scheduled, pending, unverified, not_configured) names no
// leaf and no receipt, so it is no block a verifier could check, and no new
// bundle carries it under any name (Sherlock2, buzz 8c3ec04f: not under
// x-cadence-witness/v0, which a reader would take for a chain to check
// elsewhere). The words for such a state come from the producer's own state
// when the report is made (dealAssuranceRung's producer), not from the bundle.
// ok is false for those states.
func cadenceExtensionEntry(chain map[string]interface{}) (name string, block map[string]interface{}, ok bool) {
	parent, isObject := chain["cadence"].(map[string]interface{})
	if chain["state"] != "witnessed" || !isObject {
		return "", nil, false
	}
	block = make(map[string]interface{}, len(chain))
	for k, v := range chain {
		if k != "cadence" {
			block[k] = v
		}
	}
	block["parent"] = parent
	return DealParentWitnessExtension, block, true
}
