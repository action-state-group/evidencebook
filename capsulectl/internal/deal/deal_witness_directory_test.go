package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinnedWitnessDirectoryTrustsOnlyTheProfilesWitness(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var p cli.Profile
	p.Checkpoint.Endpoint = "https://witness.example/"
	p.Checkpoint.PublicKey = hex.EncodeToString(public)

	directory, err := pinnedWitnessDirectory(p)
	require.NoError(t, err)
	row, ok := directory.RowFor("https://witness.example")
	require.True(t, ok, "the pinned endpoint has a row, trailing slash or not")
	keys, err := row.Keys()
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, public, keys[0])
	_, ok = directory.RowFor("https://other.example")
	assert.False(t, ok, "no other witness is trusted")
}

// A scrapi+https:// witness's row is under the scrapi binding, so its
// Receipts are checked as SCITT Receipts over the checkpoint statement, and
// no cll receipt from the same host is trusted.
func TestPinnedWitnessDirectoryKeepsTheEndpointsBinding(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var p cli.Profile
	p.Checkpoint.Endpoint = "scrapi+https://ts.example/"
	p.Checkpoint.PublicKey = hex.EncodeToString(public)

	directory, err := pinnedWitnessDirectory(p)
	require.NoError(t, err)
	row, ok := directory.RowFor("scrapi+https://ts.example")
	require.True(t, ok)
	assert.Equal(t, "scrapi", row.Binding)
	_, ok = directory.RowFor("https://ts.example")
	assert.False(t, ok, "the same host under the cll binding is not trusted")
}

func TestPinnedWitnessDirectoryOfAnUnpinnedProfileTrustsNoWitness(t *testing.T) {
	directory, err := pinnedWitnessDirectory(cli.Profile{})
	require.NoError(t, err)
	_, ok := directory.RowFor("https://witness.agentactioncapsule.org")
	assert.False(t, ok, "an unverified receipt must never read as witnessed (evidencebook#115)")
}

// A profile's pinned key may be uppercase hex (cli.ParseKeys accepts it);
// the receipt still verifies and the deal reads as witnessed, while a receipt
// signed by another key stays pending (TestDealReceiptUnderAnotherKeyStaysPending).
func TestAnUppercasePinnedKeyStillWitnessesTheCadence(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	p.Checkpoint.PublicKey = strings.ToUpper(p.Checkpoint.PublicKey)
	require.NoError(t, cli.SaveProfile(p, true))
	dealID := retailDeal(t)
	dealRun(t, "tick")
	deliver(t, p, cadenceSize(t, p), key)
	report := dealRun(t, "report", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	assert.Equal(t, "witnessed", report["assurance"].(map[string]any)["rung"])
}
