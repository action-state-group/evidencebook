package deal

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pemOf(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func writeKeyFile(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

func initWith(t *testing.T, args ...string) (map[string]any, string, error) {
	t.Helper()
	dealTestEnv(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "deal")
	out, err := invoke(t, "", append([]string{"deal", "init", "--profile", "deal", "--dir", dir, "--no-witness"}, args...)...)
	var got map[string]any
	if err == nil {
		require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	}
	return got, dir, err
}

// An existing key, as a Python Goose node keeps it (PKCS#8 PEM), becomes the
// profile's signing and checkpoint key: the key ids stay, and the key is copied
// into --dir as a hex seed, leaving the source as it was.
func TestDealInitKeepsAnExistingKeyFromPEM(t *testing.T) {
	signingPub, signing, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	checkpointPub, checkpoint, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signingPEM, checkpointPEM := pemOf(t, signing), pemOf(t, checkpoint)
	signingFile, checkpointFile := writeKeyFile(t, signingPEM), writeKeyFile(t, checkpointPEM)

	out, dir, err := initWith(t, "--signing-key-file", signingFile, "--checkpoint-key-file", checkpointFile)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(signingPub), out["public_key"], "the signing key id is unchanged")

	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	assert.Equal(t, []string{hex.EncodeToString(signingPub)}, p.TrustedKeys)
	assert.Equal(t, []string{hex.EncodeToString(checkpointPub)}, p.Checkpoint.TrustedKeys, "the checkpoint key id is unchanged")

	seed, err := os.ReadFile(filepath.Join(dir, "signing.seed"))
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(signing.Seed()), string(seed))
	info, err := os.Stat(filepath.Join(dir, "signing.seed"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	cpSeed, err := os.ReadFile(filepath.Join(dir, "checkpoint.seed"))
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(checkpoint.Seed()), string(cpSeed))

	after, err := os.ReadFile(signingFile)
	require.NoError(t, err)
	assert.Equal(t, signingPEM, after, "the source key file is not changed")

	// The profile works: a deal seals under the kept key.
	opened := dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))
	assert.NotEmpty(t, opened["deal_id"])
}

func TestDealInitKeepsAnExistingKeyFromAHexSeed(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	file := writeKeyFile(t, []byte(hex.EncodeToString(key.Seed())+"\n"))
	out, dir, err := initWith(t, "--signing-key-file", file)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(pub), out["public_key"])
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	assert.NotEqual(t, []string{hex.EncodeToString(pub)}, p.Checkpoint.TrustedKeys, "a checkpoint key not given is made fresh")
	cpSeed, err := os.ReadFile(filepath.Join(dir, "checkpoint.seed"))
	require.NoError(t, err)
	cpKey := ed25519.NewKeyFromSeed(mustHexBytes(t, string(cpSeed)))
	assert.Equal(t, []string{hex.EncodeToString(cpKey.Public().(ed25519.PublicKey))}, p.Checkpoint.TrustedKeys, "the fresh checkpoint seed is the profile's checkpoint key")
}

func TestDealInitRefusesAKeyThatIsNotAnEd25519Key(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	for name, body := range map[string][]byte{
		"an EC key":        pemOf(t, ecKey),
		"text":             []byte("not a key"),
		"a short hex seed": []byte("abcd"),
		"empty":            {},
		"a PEM of junk":    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")}),
	} {
		_, dir, err := initWith(t, "--signing-key-file", writeKeyFile(t, body))
		require.ErrorIs(t, err, cli.ErrInput, name)
		_, loadErr := cli.LoadProfile("deal")
		require.Error(t, loadErr, "%s: no profile is made", name)
		_, statErr := os.Stat(filepath.Join(dir, "signing.seed"))
		assert.True(t, os.IsNotExist(statErr), "%s: no seed is written", name)
	}
	_, dir, err := initWith(t, "--signing-key-file", "/nonexistent/key")
	require.ErrorIs(t, err, cli.ErrInput)
	_, statErr := os.Stat(filepath.Join(dir, "signing.seed"))
	assert.True(t, os.IsNotExist(statErr))
}

// Every key is checked before anything is written: a good signing key with a
// bad checkpoint key leaves no seed behind, so the corrected retry works.
func TestDealInitChecksEveryKeyBeforeWritingAny(t *testing.T) {
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	good := writeKeyFile(t, pemOf(t, signing))
	bad := writeKeyFile(t, []byte("not a key"))
	dealTestEnv(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "deal")
	_, err = invoke(t, "", "deal", "init", "--profile", "deal", "--dir", dir, "--no-witness", "--signing-key-file", good, "--checkpoint-key-file", bad)
	require.ErrorIs(t, err, cli.ErrInput)
	for _, name := range []string{"signing.seed", "checkpoint.seed"} {
		_, statErr := os.Stat(filepath.Join(dir, name))
		assert.True(t, os.IsNotExist(statErr), name)
	}
	_, loadErr := cli.LoadProfile("deal")
	require.Error(t, loadErr)
	out, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", dir, "--no-witness", "--signing-key-file", good)
	require.NoError(t, err, out)
}

// A seed that cannot be created removes the seed this call already wrote, so
// the retry is not blocked by it.
func TestDealInitRemovesTheFirstSeedWhenTheSecondCannotBeWritten(t *testing.T) {
	dealTestEnv(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "deal")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checkpoint.seed"), []byte("taken"), 0o600))
	_, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", dir, "--no-witness")
	require.ErrorIs(t, err, cli.ErrInput)
	_, statErr := os.Stat(filepath.Join(dir, "signing.seed"))
	assert.True(t, os.IsNotExist(statErr), "the signing seed written first is removed")
	kept, readErr := os.ReadFile(filepath.Join(dir, "checkpoint.seed"))
	require.NoError(t, readErr)
	assert.Equal(t, "taken", string(kept), "a file that was already there is left alone")
}

func TestDealInitRefusesAnEncryptedKeyWithItsReason(t *testing.T) {
	block := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("x")})
	_, _, err := initWith(t, "--signing-key-file", writeKeyFile(t, block))
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "encrypted")
}

// The result says where a kept key now lives and what to do about the second copy.
func TestDealInitSaysWhereAKeptKeyNowLives(t *testing.T) {
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	file := writeKeyFile(t, pemOf(t, signing))
	out, dir, err := initWith(t, "--signing-key-file", file)
	require.NoError(t, err)
	assert.Equal(t, []any{file + " -> " + filepath.Join(dir, "signing.seed")}, out["kept_keys"])
	assert.Contains(t, out["kept_keys_note"], "two places")
	fresh, _, err := initWith(t)
	require.NoError(t, err)
	assert.NotContains(t, fresh, "kept_keys", "a profile with no kept key says nothing of the kind")
}

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}
