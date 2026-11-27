// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A trust policy's member names are read exactly: encoding/json v1 folded
// "PINS" onto pins, so a second spelling could replace a pin
// (evidencebook#1072). Key sets keep their octets as written
// (TestLoadTrustPolicyKeySetVerbatim).
func TestLoadTrustPolicyExactNames(t *testing.T) {
	dir := t.TempDir()
	write := func(text string) string {
		path := filepath.Join(dir, "p.json")
		require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
		return path
	}
	p, err := LoadTrustPolicy(write(`{"pins":{"policy.id":"expected"}}`))
	require.NoError(t, err)
	assert.Equal(t, "expected", p.Pin("policy.id"))
	for _, bad := range []string{
		`{"pins":{"policy.id":"expected"},"PINS":{"policy.id":"other"}}`,
		`{"Pins":{"policy.id":"other"}}`,
		`{"pins":{"policy.id":"expected"}} {}`,
	} {
		_, err := LoadTrustPolicy(write(bad))
		assert.Error(t, err, bad)
	}
}
