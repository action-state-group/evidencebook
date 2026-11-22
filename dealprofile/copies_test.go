// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type copyVectors struct {
	Cases []struct {
		Name              string            `json:"name"`
		ChainDir          string            `json:"chain_dir"`
		Records           []json.RawMessage `json:"records"`
		Withheld          []int             `json:"withheld"`
		RequiredDisclosed []int             `json:"required_disclosed"`
	} `json:"cases"`
}

func loadCopyVectors(t *testing.T) copyVectors {
	t.Helper()
	var v copyVectors
	readJSON(t, filepath.Join(fixturesDir, "counterparty-copy-vectors.json"), &v)
	require.NotEmpty(t, v.Cases)
	return v
}

// chainRecords reads a fixture chain directory's records in file order.
func chainRecords(t *testing.T, dir string) []any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixturesDir, dir, "*.json"))
	require.NoError(t, err)
	sort.Strings(paths)
	var out []any
	for _, p := range paths {
		var f struct {
			Record json.RawMessage `json:"record"`
		}
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &f))
		out = append(out, []byte(f.Record))
	}
	return out
}

// Rule 12 "Copies" (evidencebook#613): Go gives the reference checker's sets
// on every vector, each of which is a valid deal.
func TestCounterpartyCopyVectors(t *testing.T) {
	for _, c := range loadCopyVectors(t).Cases {
		var records []any
		if c.ChainDir != "" {
			records = chainRecords(t, c.ChainDir)
		} else {
			for _, r := range c.Records {
				records = append(records, []byte(r))
			}
		}
		require.NoError(t, CheckChain(records), c.Name)
		withheld, err := CounterpartyWithheld(records)
		require.NoError(t, err, c.Name)
		assert.Equal(t, c.Withheld, orEmpty(withheld), c.Name)
		required, err := RequiredDisclosed(records)
		require.NoError(t, err, c.Name)
		assert.Equal(t, c.RequiredDisclosed, orEmpty(required), c.Name)
		for _, i := range required {
			assert.NotContains(t, withheld, i, "%s: position %d is both withheld and required", c.Name, i)
		}
	}
}

func orEmpty(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

// The twins differ only in whether the ordered claim was authorized: no
// record a counterparty copy discloses carries a verdict member or names the
// gate's finding, and their disclosed records are the same, kind by kind and
// body by body once digests, commitments and times are set aside, but for
// the steps that rest on the held offer (the offer act and the buyer's
// acceptance) and the end outcome, which compares with the terms in force
// without it: gap 3's stated limit.
func TestCounterpartyCopyTwinsCarryNoVerdict(t *testing.T) {
	disclosed := map[string][]string{}
	for _, c := range loadCopyVectors(t).Cases {
		if c.Name != "authorized" && c.Name != "unauthorized" {
			continue
		}
		var records []any
		for _, r := range c.Records {
			records = append(records, []byte(r))
		}
		withheld, err := CounterpartyWithheld(records)
		require.NoError(t, err)
		docs, err := readableDocs(records)
		require.NoError(t, err)
		for i, d := range docs {
			if containsInt(withheld, i) {
				continue
			}
			raw, err := json.Marshal(d)
			require.NoError(t, err)
			for _, s := range []string{"required_representation_missing", "unchecked", `"authority":"none"`, "recipient_told_before"} {
				assert.NotContains(t, string(raw), s, "%s: disclosed position %d", c.Name, i)
			}
			k := str(block(d)["record_type"])
			if k == TypeClaim {
				// A claim's verdict members; an authorized act's own
				// authority_basis is the act's, not a claim's.
				assert.False(t, claimCarriesVerdict(mp(d["body"])), "%s: disclosed claim %d", c.Name, i)
			}
			if ty := str(block(d)["typed"]); ty != "" {
				k = ty
			}
			switch {
			case c.Name == "authorized" && (k == TypeActionRecord || k == TypeActionApproval):
				continue // the held offer and its acceptance (gap 3)
			case k == TypeActionOutcome:
				disclosed[c.Name] = append(disclosed[c.Name], k) // its outcome differs (gap 3)
				continue
			}
			nb, err := json.Marshal(normalized(mp(d["body"])))
			require.NoError(t, err)
			disclosed[c.Name] = append(disclosed[c.Name], k+" "+string(nb))
		}
	}
	assert.Equal(t, disclosed["authorized"], disclosed["unauthorized"],
		"the twins disclose the same records apart from gap 3's")
}

// normalized drops what differs between any two deals: digests, refs,
// commitments and times.
func normalized(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, w := range x {
			if strings.HasSuffix(k, "_commitment") || strings.HasSuffix(k, "_ref") || strings.HasSuffix(k, "_digest") && k != "pack_digest" || k == "ref" {
				continue
			}
			out[k] = normalized(w)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, w := range x {
			out[i] = normalized(w)
		}
		return out
	}
	return v
}

// The vectors' negatives: each chain is refused at the chain stage with the
// stated text (a terms_pack of a pack not of kind deal, or after rules that
// did not run).
func TestCounterpartyCopyVectorNegatives(t *testing.T) {
	var v struct {
		Negatives []struct {
			Name     string            `json:"name"`
			Records  []json.RawMessage `json:"records"`
			Contains string            `json:"expected_error_contains"`
		} `json:"negatives"`
	}
	readJSON(t, filepath.Join(fixturesDir, "counterparty-copy-vectors.json"), &v)
	require.NotEmpty(t, v.Negatives)
	for _, c := range v.Negatives {
		var records []any
		for _, r := range c.Records {
			records = append(records, []byte(r))
		}
		err := CheckChain(records)
		require.Error(t, err, c.Name)
		assert.Contains(t, err.Error(), c.Contains, c.Name)
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A deal not under rule 12, and records that are not JSON objects.
func TestCounterpartyWithheldOutsideRule12(t *testing.T) {
	got, err := CounterpartyWithheld(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = RequiredDisclosed(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	_, err = CounterpartyWithheld([]any{[]byte(`[1]`)})
	assert.Error(t, err)
	_, err = RequiredDisclosed([]any{[]byte(`not json`)})
	assert.Error(t, err)
	// ordered_representations null is no rule 12 deal.
	base := []byte(`{"x-deal-v0":{"record_type":"baseline"},"body":{"intent":{"party_role":"seller","ordered_representations":null}}}`)
	close := []byte(`{"x-deal-v0":{"record_type":"close"},"body":{}}`)
	got, err = CounterpartyWithheld([]any{base, close})
	require.NoError(t, err)
	assert.Empty(t, got)
	// An empty list is: the deal follows rule 12 with nothing ordered.
	base = []byte(`{"x-deal-v0":{"record_type":"baseline"},"body":{"intent":{"party_role":"seller","ordered_representations":[]}}}`)
	got, err = CounterpartyWithheld([]any{base, close})
	require.NoError(t, err)
	assert.Equal(t, []int{1}, got)
}
