// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	evidencebook "github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The earliest-receipt-not-anchor vector replays: a reader of its handover
// gets every query's time, the holder's RecordTime, and without the
// witnessed checkpoints before the anchor it would not.
func TestEarliestReceiptVectorReplays(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(vectorDir, "handover", "earliest-receipt-not-anchor.json"))
	require.NoError(t, err)
	var v struct {
		PinnedKey string                          `json:"pinned_key"`
		Handover  evidencebook.Handover           `json:"handover"`
		Queries   []EarliestQuery                 `json:"queries"`
		Forked    evidencebook.HandoverCheckpoint `json:"forked"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	directory, err := Directory()
	require.NoError(t, err)
	times, err := evidencebook.VerifyHandoverTimes(v.Handover, v.PinnedKey, directory)
	require.NoError(t, err)
	anchorOnly := v.Handover
	anchorOnly.Witnessed = nil
	without, err := evidencebook.VerifyHandoverTimes(anchorOnly, v.PinnedKey, directory)
	require.NoError(t, err)
	differs := false
	for _, q := range v.Queries {
		got, ok, err := times.RecordTime(q.BookID, q.Position, q.Witnesses)
		require.NoError(t, err)
		if q.Time == nil {
			assert.False(t, ok, "%s at %d", q.LogID, q.Position)
			continue
		}
		require.True(t, ok, "%s at %d", q.LogID, q.Position)
		assert.Equal(t, *q.Time, got.Format(time.RFC3339), "%s at %d", q.LogID, q.Position)
		anchorTime, ok, err := without.RecordTime(q.BookID, q.Position, q.Witnesses)
		require.NoError(t, err)
		differs = differs || !ok || !anchorTime.Equal(got)
	}
	assert.True(t, differs, "some record's time comes from a checkpoint before the anchor")

	// The holder's key, the shelf log, a size within the anchor and a
	// receipt, but another root: refused.
	forked := v.Handover
	forked.Witnessed = append([]evidencebook.HandoverCheckpoint{v.Forked}, v.Handover.Witnessed[1:]...)
	_, err = evidencebook.VerifyHandover(forked, v.PinnedKey, directory)
	assert.ErrorIs(t, err, evidencebook.ErrInvalid)
	assert.ErrorContains(t, err, "do not recompute witnessed checkpoint", "refused for its root, not an earlier check")
	_, err = evidencebook.VerifyHandoverTimes(forked, v.PinnedKey, directory)
	assert.ErrorIs(t, err, evidencebook.ErrInvalid)
}
