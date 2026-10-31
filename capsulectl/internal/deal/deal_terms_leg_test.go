package deal

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func termsLegRun(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	out, err := invoke(t, "", append([]string{"deal", "terms-leg"}, args...)...)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &m), out)
	return m, nil
}

func TestATermsLegRoundTripsIntoTheSellersNotes(t *testing.T) {
	id, dueStep := sellerDealWithDueDelivery(t)
	path := filepath.Join(t.TempDir(), "terms.json")
	out, err := termsLegRun(t, "--ephemeral", "--amount", "1500000", "--content-digest", sha256Hex(deliveredContent), "--out", path)
	require.NoError(t, err)
	assert.Equal(t, path, out["terms_leg"])
	assert.Equal(t, true, out["ephemeral"])

	notePaymentReceived(t, id, path, x402Settle(true))
	dealRun(t, "note", "--deal", id, "--kind", "delivered", "--terms", path,
		"--content-digest", sha256Hex(deliveredContent), "--resolves", dueStep)

	got := dealSettlement(t, "--deal", id, "--leg", path)
	require.True(t, got.Settlement.Conforming, "%+v", got.Settlement.Failures)
	require.Len(t, got.Settlement.Settlements, 1)
	assert.Equal(t, "payee_stated", got.Settlement.Settlements[0].PaymentState)

	// The id it reports is the one the buyer's later legs name, and the
	// price it wrote equals what the buyer's own payer leg observed.
	buyer := newBuyerSide(t)
	both := dealSettlement(t, "--deal", id, "--leg", path, "--leg", buyer.payerObserved(out["capsule_id"].(string)),
		"--object", writeFile(t, "payload.json", x402PaymentPayload))
	require.True(t, both.Settlement.Conforming, "%+v", both.Settlement.Failures)
	assert.Equal(t, "equal", both.Settlement.Settlements[0].TermsAmount)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"value":"1500000"`)
}

func TestATermsLegIsSignedWithTheProfileKeyUnlessEphemeral(t *testing.T) {
	dealFixture(t)
	dir := t.TempDir()
	digest := sha256Hex(deliveredContent)
	profile := func(name string) map[string]any {
		out, err := invoke(t, "", "--profile", "deal", "deal", "terms-leg", "--amount", "1", "--content-digest", digest, "--out", filepath.Join(dir, name))
		require.NoError(t, err, out)
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &m))
		return m
	}
	profileKey := dealRunInitKey(t)
	first, second := profile("a.json"), profile("b.json")
	assert.Equal(t, profileKey, first["key_id"], "the leg is signed by the profile's own key")
	assert.Equal(t, first["key_id"], second["key_id"], "the profile's key signs every leg")
	assert.Equal(t, false, first["ephemeral"])

	one, err := termsLegRun(t, "--ephemeral", "--amount", "1", "--content-digest", digest, "--out", filepath.Join(dir, "c.json"))
	require.NoError(t, err)
	two, err := termsLegRun(t, "--ephemeral", "--amount", "1", "--content-digest", digest, "--out", filepath.Join(dir, "d.json"))
	require.NoError(t, err)
	assert.NotEqual(t, one["key_id"], first["key_id"])
	assert.NotEqual(t, one["key_id"], two["key_id"], "an ephemeral key is made for each run")

	// Both verify as terms legs.
	for _, name := range []string{"a.json", "c.json"} {
		_, err = termsLegID(filepath.Join(dir, name))
		assert.NoError(t, err, name)
	}
}

func TestATermsLegNamesItsAsset(t *testing.T) {
	dir := t.TempDir()
	digest := sha256Hex(deliveredContent)
	path := filepath.Join(dir, "eth.json")
	_, err := termsLegRun(t, "--ephemeral", "--amount", "5", "--asset", "eip155:1/slip44:60", "--asset-scale", "18", "--content-digest", digest, "--out", path)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"assetScale":18`)
	assert.Contains(t, string(raw), "eip155:1/slip44:60")
}

func TestATermsLegRefusesWhatItCannotStand(t *testing.T) {
	dir := t.TempDir()
	digest := sha256Hex(deliveredContent)
	out := filepath.Join(dir, "terms.json")
	run := func(args ...string) error {
		_, err := termsLegRun(t, append([]string{"--ephemeral"}, args...)...)
		return err
	}
	assert.ErrorContains(t, run("--content-digest", digest, "--out", out), "needs --amount")
	assert.ErrorContains(t, run("--amount", "1", "--out", out), "needs --amount")
	assert.ErrorContains(t, run("--amount", "1", "--content-digest", digest), "needs --amount")
	assert.ErrorContains(t, run("--amount", "1.5", "--content-digest", digest, "--out", out), "atomic units")
	assert.ErrorContains(t, run("--amount", "-1", "--content-digest", digest, "--out", out), "atomic units")
	assert.ErrorContains(t, run("--amount", "01", "--content-digest", digest, "--out", out), "atomic units")
	assert.ErrorContains(t, run("--amount", "1", "--content-digest", "ABC", "--out", out), "64 lowercase hex")
	assert.ErrorContains(t, run("--amount", "1", "--asset", "eip155:1/slip44:60", "--content-digest", digest, "--out", out), "--asset-scale")
	assert.ErrorContains(t, run("--amount", "1", "--asset-scale", "18", "--content-digest", digest, "--out", out), "goes with --asset")
	assert.ErrorContains(t, run("--amount", "1", "--asset", "not an asset", "--asset-scale", "6", "--content-digest", digest, "--out", out), "CAIP-19")
	_, err := os.Stat(out)
	assert.True(t, os.IsNotExist(err), "no file is written on an input error")

	// A leg the verifier refuses (a scale it cannot read as exact) is not left behind.
	err = run("--amount", "1", "--asset", "eip155:1/slip44:60", "--asset-scale", "99999999999", "--content-digest", digest, "--out", out)
	assert.ErrorContains(t, err, "not a valid settlement leg")
	_, statErr := os.Stat(out)
	assert.True(t, os.IsNotExist(statErr), "the refused leg is removed")

	// Neither --ephemeral nor a profile: no key to sign with.
	_, err = invoke(t, "", "deal", "terms-leg", "--amount", "1", "--content-digest", digest, "--out", out)
	assert.ErrorContains(t, err, "--profile")
	_, err = invoke(t, "", "--profile", "deal", "deal", "terms-leg", "--ephemeral", "--amount", "1", "--content-digest", digest, "--out", out)
	assert.ErrorContains(t, err, "drop --profile")
}

func TestATermsLegDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terms.json")
	require.NoError(t, os.WriteFile(path, []byte("mine"), 0o600))
	_, err := termsLegRun(t, "--ephemeral", "--amount", "1", "--content-digest", sha256Hex(deliveredContent), "--out", path)
	assert.ErrorContains(t, err, "exists")
	got, _ := os.ReadFile(path)
	assert.Equal(t, "mine", string(got))
}

// dealRunInitKey is the public key of the profile dealFixture made.
func dealRunInitKey(t *testing.T) string {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	key, err := cli.PrivateKey(p.Signing)
	require.NoError(t, err)
	return hex.EncodeToString(key.Public().(ed25519.PublicKey))
}
