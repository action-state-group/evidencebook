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
	require.NoError(t, Migrate(ctx, dir))
	afterMigrate, err := Dump(ctx, dir)
	require.NoError(t, err)
	handover, err := HandoverJSON(ctx, dir)
	require.NoError(t, err)
	handoverResult, err := VerifyHandoverJSON(handover)
	require.NoError(t, err)
	files := map[string][]byte{"script.json": script, "after-write.json": afterWrite, "after-continue.json": afterContinue, "after-migrate.json": afterMigrate}
	files["handover.json"], files["handover-result.json"] = handover, handoverResult
	recordTime, err := RecordTimeJSON(ctx, dir)
	require.NoError(t, err)
	afterRecordTime, err := Dump(ctx, dir)
	require.NoError(t, err)
	files["record-time.json"], files["after-record-time.json"] = recordTime, afterRecordTime
	// The recovery scenarios: Go plants a crash on the shelf after
	// ContinuePhase, and RecoverPhase finishes it (evidencebook#461 §7).
	for name, plant := range map[string]func(context.Context, string) error{
		"after-recover-tick.json":      PlantTick,
		"after-recover-migration.json": func(ctx context.Context, dir string) error { return PlantMigration(ctx, dir, 2) },
	} {
		dir := t.TempDir()
		require.NoError(t, Write(ctx, dir))
		require.NoError(t, Continue(ctx, dir))
		require.NoError(t, plant(ctx, dir))
		require.NoError(t, Recover(ctx, dir))
		got, err := Dump(ctx, dir)
		require.NoError(t, err)
		files[name] = got
	}
	// A crash after the migration's checkpoint is cut and before its row and
	// state record it recovers to the same shelf: the checkpoint already cut
	// is recorded, none is cut or queued again.
	dir2 := t.TempDir()
	require.NoError(t, Write(ctx, dir2))
	require.NoError(t, Continue(ctx, dir2))
	require.NoError(t, PlantMigrationCheckpointed(ctx, dir2))
	require.NoError(t, Recover(ctx, dir2))
	got, err := Dump(ctx, dir2)
	require.NoError(t, err)
	if os.Getenv("EB_WRITE_SHELF_VECTORS") != "1" {
		want, err := os.ReadFile(filepath.Join(vectorDir, "after-recover-migration.json"))
		require.NoError(t, err)
		if !bytes.Equal(got, want) {
			t.Error("a migration checkpointed before the crash recovers to after-recover-migration.json")
		}
	}
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
