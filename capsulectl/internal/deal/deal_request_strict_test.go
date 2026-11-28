// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A request's member names are read exactly and once (evidencebook#1072).
func TestParseDealRequestExactNames(t *testing.T) {
	req, err := parseDealRequest([]byte(`{"subject":{"kind":"deal","value":"d"},"coverage":{},"nonce":"n"}`))
	require.NoError(t, err)
	assert.Equal(t, "n", req.Nonce)
	for _, bad := range []string{
		`{"subject":{"kind":"deal","value":"d"},"coverage":{},"NONCE":"n"}`,
		`{"subject":{"kind":"deal","value":"d","value":"e"},"coverage":{}}`,
		`{"subject":{"kind":"deal","value":"d"},"coverage":{}} {}`,
	} {
		_, err := parseDealRequest([]byte(bad))
		assert.Error(t, err, bad)
	}
}

// An answer file is one JSON value whose member names are read exactly and
// once (evidencebook#1072).
func TestReadAnswerFileExactNames(t *testing.T) {
	for _, text := range []string{
		`{"request":{},"bundle":{}} {}`,
		`{"request":{},"bundle":{},"FACTS":{}}`,
		`{"request":{},"bundle":{},"bundle":{}}`,
	} {
		path := filepath.Join(t.TempDir(), "answer.json")
		require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
		_, err := readAnswerFile(path, "deal-1")
		require.Error(t, err, text)
		assert.Contains(t, err.Error(), "is not an answer file", text)
	}
}
