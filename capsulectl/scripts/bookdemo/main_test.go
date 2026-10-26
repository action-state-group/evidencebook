package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixture is a disclosed book bundle rooted at a Result that cites two
// published capsules (own_capsule records), whose headers carry integer
// seq and v. aac-verify must read those as integers, not float64, or AAC
// cannot recompute the record ids and the citations dangle.
func TestAACVerifyPassesABundleHoldingOwnCapsules(t *testing.T) {
	fixture := filepath.Join("testdata", "own-capsule-bundle.json")
	raw, err := os.ReadFile(fixture)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "own_capsule", "the fixture holds an own_capsule record")
	assert.NoError(t, aacVerify([]string{fixture}))
}

func TestAACVerifyRefusesATamperedBundle(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "own-capsule-bundle.json"))
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(raw, &value))
	value["records"] = value["records"].([]any)[1:]
	tampered, err := json.Marshal(value)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "tampered.json")
	require.NoError(t, os.WriteFile(path, tampered, 0o600))
	assert.Error(t, aacVerify([]string{path}))
}
