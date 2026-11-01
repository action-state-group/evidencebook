// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// vectorDir is where the scripted shelf's vectors live.
var vectorDir = filepath.Join("..", "..", "testdata", "shelf")

// TestShelfVectors runs the script, and its dumps and script must equal
// testdata/shelf byte for byte; EB_WRITE_SHELF_VECTORS=1 rewrites them.
func TestShelfVectors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	script, err := Script()
	require.NoError(t, err)
	require.NoError(t, Write(ctx, dir))
	afterWrite, err := Dump(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, Continue(ctx, dir))
	afterContinue, err := Dump(ctx, dir)
	require.NoError(t, err)
	files := map[string][]byte{"script.json": script, "after-write.json": afterWrite, "after-continue.json": afterContinue}
	for name, got := range files {
		path := filepath.Join(vectorDir, name)
		if os.Getenv("EB_WRITE_SHELF_VECTORS") == "1" {
			require.NoError(t, os.WriteFile(path, got, 0o644))
			continue
		}
		want, err := os.ReadFile(path)
		require.NoError(t, err)
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from this build's run; regenerate only for an intended change (EB_WRITE_SHELF_VECTORS=1)", name)
		}
	}
}

// A dump changes nothing: the file it reads is the same afterwards, pending
// record and all.
func TestADumpLeavesTheShelfAsItWas(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, Write(ctx, dir))
	first, err := Dump(ctx, dir)
	require.NoError(t, err)
	second, err := Dump(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
	require.Contains(t, string(first), `"deal/a": 1`, "the pending record is listed")
}

// The lock exchange: an open shelf holds its lock, and a second writer
// waits until its context ends.
func TestHoldExcludesASecondWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, Write(ctx, dir))
	release, err := Hold(ctx, dir)
	require.NoError(t, err)
	short, cancel := context.WithTimeout(ctx, 100_000_000)
	defer cancel()
	_, err = Hold(short, dir)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, release())
	again, err := Hold(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, again())
}

func TestRunRefusesAStepOutOfPlace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), FileName)
	require.ErrorContains(t, Run(ctx, path, []Step{{Op: "append", Book: "deal/a"}}), "with the shelf released")
	require.ErrorContains(t, Run(ctx, path, []Step{{Op: "open", Stream: "s"}}), "left the shelf open")
	require.ErrorContains(t, Run(ctx, path, []Step{{Op: "open", Stream: "s"}, {Op: "nope"}}), "unknown step")
	require.ErrorContains(t, Write(ctx, filepath.Dir(path)), "exists")
}
