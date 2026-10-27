// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/require"
)

// The bundles the tests read. The presentation fixtures are the pages
// `capsulectl book report build` writes (taken from capsule-cli's
// cli/testdata/presentation, capsule-cli#326); evidencebook-deal's
// TestPresentationGoldens measures its own copy of these pages in a browser,
// so a change here goes there too. bookFormBundle is a book-form
// Result-root bundle as `disclose` writes it, with own capsules
// (capsulectl/scripts/bookdemo/testdata); incompleteBundle is a payload-form
// one with no checkpoint or producer signatures
// (capsulectl/internal/book/testdata/result).
const (
	presentationDir  = "testdata/presentation"
	bookFormBundle   = "testdata/book-form-bundle.json"
	incompleteBundle = "testdata/incomplete-payload-bundle.json"
)

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

func decode(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	value, err := cli.DecodeBundleJSON(raw)
	require.NoError(t, err)
	return value
}

// edited is raw with edit applied to its decoded bundle.
func edited(t *testing.T, raw []byte, edit func(map[string]interface{})) []byte {
	t.Helper()
	value := decode(t, raw)
	edit(value)
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

// newBook is an empty book on disk under a fresh directory, released when
// the test ends; it cuts checkpoints only when asked.
func newBook(t *testing.T) *evidencebook.Book {
	t.Helper()
	dir := t.TempDir()
	key := func(seed byte) ed25519.PrivateKey {
		return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	}
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	require.NoError(t, err)
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), "report-test", key(2))
	require.NoError(t, err)
	signer, err := evidencebook.NewEd25519Signer(key(1))
	require.NoError(t, err)
	originals, err := evidencebook.OpenOriginalsFile(t.Context(), filepath.Join(dir, "originals.jsonl"), "report-test", signer.PublicKey())
	require.NoError(t, err)
	clock := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	book, err := evidencebook.Open(t.Context(), evidencebook.Config{
		BookID: "report-test", Operator: "report-test-operator", Store: store, Substrate: substrate, Originals: originals, Signer: signer,
		Now: func() time.Time { clock = clock.Add(time.Second); return clock },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Release() })
	return book
}
