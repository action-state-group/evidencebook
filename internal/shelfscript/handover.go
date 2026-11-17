// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
)

// The whole-shelf handover (evidencebook#461, docs/design/461-shelf-facts.md
// §2.7) of the scripted shelf after MigratePhase: every language hands the
// shelf over to the same bytes (testdata/shelf/handover.json), and replays
// any language's handover to the same result (handover-result.json).

// HandoverJSON is the handover of the shelf at dir/shelf.db at its newest
// finished stamp, in marshal's encoding. It reads a copy, as Dump does: a
// handover opens the books whose consistency it proves.
func HandoverJSON(ctx context.Context, dir string) ([]byte, error) {
	scratch, err := os.MkdirTemp("", "shelf-handover-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	path := filepath.Join(scratch, FileName)
	original, err := openDB(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	_, err = original.ExecContext(ctx, `VACUUM INTO ?`, path)
	if err := errors.Join(err, original.Close()); err != nil {
		return nil, err
	}
	e, err := open(ctx, path, "evidencebook shelf vectors/handover", Start.Add(3*time.Hour))
	if err != nil {
		return nil, err
	}
	h, err := e.cadence.Handover(ctx, 0)
	if err := errors.Join(err, e.release()); err != nil {
		return nil, err
	}
	return marshal(h)
}

// VerifyHandoverJSON replays a handover under the script's checkpoint key
// and witness directory, and returns its result in marshal's encoding.
func VerifyHandoverJSON(raw []byte) ([]byte, error) {
	var h evidencebook.Handover
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	directory, err := Directory()
	if err != nil {
		return nil, err
	}
	result, err := evidencebook.VerifyHandover(h, hex.EncodeToString(CheckpointKey.Public().(ed25519.PublicKey)), directory)
	if err != nil {
		return nil, err
	}
	return marshal(result)
}
