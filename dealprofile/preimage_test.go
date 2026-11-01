// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreimageVectors(t *testing.T) {
	var v struct {
		Vectors []struct {
			Label, Preimage string
			PaymentHash     string `json:"payment_hash"`
			Matches         bool
		}
	}
	readJSON(t, filepath.Join(fixturesDir, "preimage-vectors.json"), &v)
	require.NotEmpty(t, v.Vectors)
	for _, x := range v.Vectors {
		assert.Equal(t, x.Matches, PreimageMatches(x.Preimage, x.PaymentHash), x.Label)
	}
}

// A preimage rides only on evidence of a payment the user received
// (received_to), as 64 lowercase hex.
func TestEvidencePreimageSchema(t *testing.T) {
	var f struct {
		Record map[string]any `json:"record"`
	}
	readJSON(t, filepath.Join(fixturesDir, "positive", "04-evidence.json"), &f)
	with := func(body map[string]any) map[string]any {
		raw, err := json.Marshal(f.Record)
		require.NoError(t, err)
		var rec map[string]any
		require.NoError(t, json.Unmarshal(raw, &rec))
		rec["body"] = body
		return rec
	}
	receivedTo := map[string]any{"fp_alg": "hmac-sha256-deal-key", "payee": "d147f30a815fa71535d1a5249e87a8762defb852d4cf64aa10c0d24622a0a9a6"}
	pre := "abababababababababababababababababababababababababababababababab"
	require.Len(t, pre, 64)
	assert.NoError(t, Check(with(map[string]any{"source": "lightning_wallet", "verified": false, "received_to": receivedTo, "preimage": pre}), nil))
	var stage *StageError
	err := Check(with(map[string]any{"source": "lightning_wallet", "verified": false, "preimage": pre}), nil)
	require.ErrorAs(t, err, &stage)
	assert.Equal(t, "schema", stage.Stage, err.Error())
	err = Check(with(map[string]any{"source": "lightning_wallet", "verified": false, "received_to": receivedTo, "preimage": "AB" + pre[2:]}), nil)
	require.ErrorAs(t, err, &stage)
	assert.Equal(t, "schema", stage.Stage, err.Error())
}
