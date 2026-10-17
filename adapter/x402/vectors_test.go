// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The agent-action-capsule vectors for this adapter, vectors/adapters/x402/
// (one positive case and one native-check-fails case), vendored unchanged
// under testdata/aac-vectors/ once they merge. Until then aacX402Commit is
// empty and the test is skipped. To pin: copy the directory from the merge
// commit, set aacX402Commit to that commit and aacX402SumsSHA256 to the
// SHA-256 of its SHA256SUMS, then fill in the cases below from the vectors'
// expected results.
const (
	aacX402Commit     = ""
	aacX402SumsSHA256 = ""
)

func TestAACVectors(t *testing.T) {
	if aacX402Commit == "" {
		t.Skip("agent-action-capsule vectors/adapters/x402 are not merged yet (spec item adapter-x402-v0, stage 2); pin the merge commit in aacX402Commit")
	}
	dir := filepath.Join("testdata", "aac-vectors")
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if s := sha256.Sum256(sums); hex.EncodeToString(s[:]) != aacX402SumsSHA256 {
		t.Fatalf("testdata/aac-vectors/SHA256SUMS is not the one pinned at %s", aacX402Commit)
	}
	t.Fatal("vectors pinned but no cases wired: read the positive and native-check-fails cases and compare per-property results byte-for-byte")
}
