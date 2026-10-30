// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The profile's own fixtures (x-deal-v0/fixtures), as the reference checker
// reads them (check_profile.py, run_fixtures).
const fixturesDir = "x-deal-v0/fixtures"

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, v))
}

type fingerprintVectors struct {
	StoreSecretHex string `json:"store_secret_hex"`
	DealID         string `json:"deal_id"`
	DealKeyHex     string `json:"deal_key_hex"`
	Vectors        []struct {
		Kind, Raw, Normalized, FP string
	} `json:"vectors"`
	Commitments []struct {
		Label, Nonce, Text, Commitment string
	} `json:"commitments"`
}

// localStoreValues are the values the checker scans every fixture for: the
// identifiers' raw values, and each committed text long enough not to
// collide with a token.
func localStoreValues(v fingerprintVectors) []string {
	var out []string
	for _, x := range v.Vectors {
		out = append(out, x.Raw)
	}
	for _, x := range v.Commitments {
		if len([]rune(x.Text)) >= 24 {
			out = append(out, x.Text)
		}
	}
	return out
}

func loadFingerprints(t *testing.T) fingerprintVectors {
	t.Helper()
	var v fingerprintVectors
	readJSON(t, filepath.Join(fixturesDir, "fingerprint-vectors.json"), &v)
	return v
}

func TestFingerprintVectors(t *testing.T) {
	v := loadFingerprints(t)
	secret, err := hex.DecodeString(v.StoreSecretHex)
	require.NoError(t, err)
	key := DealKey(secret, v.DealID)
	assert.Equal(t, v.DealKeyHex, hex.EncodeToString(key))
	for _, x := range v.Vectors {
		n, err := Normalize(x.Kind, x.Raw)
		require.NoError(t, err, x.Raw)
		assert.Equal(t, x.Normalized, n, x.Raw)
		fp, err := Fingerprint(key, x.Kind, x.Raw)
		require.NoError(t, err)
		assert.Equal(t, x.FP, fp, x.Raw)
	}
	for _, x := range v.Commitments {
		c, err := CommitText(x.Nonce, x.Text)
		require.NoError(t, err)
		assert.Equal(t, x.Commitment, c, x.Label)
	}
	_, err = Fingerprint(key, "nickname", "x")
	assert.ErrorIs(t, err, ErrNotNormalizable, "a kind outside the closed set")
	_, err = Fingerprint(key, "profile_id", "   ")
	assert.ErrorIs(t, err, ErrNotNormalizable)
}

func TestCommercialBoundsVectors(t *testing.T) {
	var v struct {
		Vectors []struct {
			Document         map[string]any `json:"document"`
			Nonce            string         `json:"nonce"`
			Text             string         `json:"text"`
			BoundsCommitment string         `json:"bounds_commitment"`
		} `json:"vectors"`
	}
	readJSON(t, filepath.Join(fixturesDir, "commercial-bounds-vectors.json"), &v)
	require.NotEmpty(t, v.Vectors)
	for _, x := range v.Vectors {
		doc, err := decode(x.Document)
		require.NoError(t, err)
		text, err := canonical.JCS(doc)
		require.NoError(t, err)
		assert.Equal(t, x.Text, string(text))
		c, err := CommitText(x.Nonce, x.Text)
		require.NoError(t, err)
		assert.Equal(t, x.BoundsCommitment, c)
	}
}

func TestRecordTypesAreTheSchemas(t *testing.T) {
	var s struct {
		Defs struct {
			RecordType struct {
				Enum []string `json:"enum"`
			} `json:"record_type"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(ProfileSchema(), &s))
	assert.Equal(t, s.Defs.RecordType.Enum, RecordTypes())
	RecordTypes()[0] = "changed"
	assert.Equal(t, TypeIntent, RecordTypes()[0], "a caller cannot change the closed set")
	for _, typ := range recordSchemaTypes {
		_, err := RecordSchema(typ)
		assert.NoError(t, err, typ)
	}
	_, err := RecordSchema("payment/v0")
	assert.Error(t, err)
}

// positiveChains are the fixtures' four positive chains.
var positiveChains = []string{"positive", "positive-typed", "positive-typed-seller", "positive-sale"}

func TestPositiveFixtures(t *testing.T) {
	store := localStoreValues(loadFingerprints(t))
	digests := expectedDigests(t)
	n := 0
	for _, dir := range positiveChains {
		files, err := filepath.Glob(filepath.Join(fixturesDir, dir, "*.json"))
		require.NoError(t, err)
		require.NotEmpty(t, files, dir)
		sort.Strings(files)
		for _, f := range files {
			var fx struct {
				Record         json.RawMessage `json:"record"`
				ExpectedDigest string          `json:"expected_digest"`
			}
			readJSON(t, f, &fx)
			name := dir + "/" + filepath.Base(f)
			payload, digest, err := Encode([]byte(fx.Record), store)
			require.NoError(t, err, name)
			assert.Equal(t, fx.ExpectedDigest, digest, name)
			if want, ok := digests[name]; ok {
				assert.Equal(t, want, digest, name+" in expected-digests.txt")
			}
			assert.NoError(t, CheckDigest(payload, digest))
			n++
		}
	}
	t.Logf("%d positive records pass every record-level stage", n)
}

// expectedDigests reads expected-digests.txt: "<digest>  <dir>/<file>".
func expectedDigests(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(fixturesDir, "expected-digests.txt"))
	require.NoError(t, err)
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		require.Len(t, fields, 2, line)
		out[fields[1]] = fields[0]
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, out)
	return out
}

// schemaWordingOnly maps the schema-stage negatives whose expected text is
// the reference validator's own wording (jsonschema 4.23), which no member
// name or location in another validator's report carries, to the text this
// package's validator reports for the same failure.
var schemaWordingOnly = map[string]string{
	"neg-agent-card-with-words.json":                        "/body: 'not' failed",                 // there: "should not be valid under ... said_commitment"
	"neg-typed-seller-offer-no-proposal-beside-source.json": "/body: 'not' failed",                 // there: "should not be valid under"
	"neg-typed-seller-source-on-a-commit.json":              "/body/action: value must be 'offer'", // there: "'offer' was expected"
}

// TestNegativeFixtures checks that every negative the reference checker
// rejects before the chain stage is rejected here at the same stage, for the
// same reason: its expected text is in the refusal (at the schema stage, in
// one of the failed leaves, which name the member that failed). A negative
// it rejects at the chain stage passes every record-level stage here, as
// there: this package has no chain stage, and CI runs the reference checker
// over the chains Go builds (TestBaselineSteps, DEALPROFILE_BUILT_CHAINS).
func TestNegativeFixtures(t *testing.T) {
	store := localStoreValues(loadFingerprints(t))
	files, err := filepath.Glob(filepath.Join(fixturesDir, "negative", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	chain := 0
	for _, f := range files {
		var fx struct {
			Record                json.RawMessage `json:"record"`
			ExpectedStage         string          `json:"expected_stage"`
			ExpectedErrorContains string          `json:"expected_error_contains"`
			ClaimedDigest         *string         `json:"claimed_digest"`
		}
		readJSON(t, f, &fx)
		name := filepath.Base(f)
		err := Check([]byte(fx.Record), store)
		if err == nil && fx.ClaimedDigest != nil {
			err = CheckDigest([]byte(fx.Record), *fx.ClaimedDigest)
		}
		if fx.ExpectedStage == "chain" {
			assert.NoError(t, err, "%s passes the record-level stages", name)
			chain++
			continue
		}
		var se *StageError
		if !assert.ErrorAs(t, err, &se, name) {
			continue
		}
		assert.Equal(t, fx.ExpectedStage, se.Stage, "%s: %v", name, err)
		want := fx.ExpectedErrorContains
		if goText, ok := schemaWordingOnly[name]; ok && fx.ExpectedStage == StageSchema {
			want = goText
		}
		assert.Contains(t, se.Error()+"\n"+strings.Join(se.Causes, "\n"), want, name)
	}
	t.Logf("%d negatives rejected at the record-level stages; %d are the chain stage's, which the reference checker runs", len(files)-chain, chain)
}
