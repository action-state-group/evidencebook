// SPDX-License-Identifier: Apache-2.0

package deal

import (
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
