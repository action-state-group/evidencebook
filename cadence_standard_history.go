// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/checkpointed-local-log/go/mmr"
)

// A shelf's standard_history/1 card (SH) counts the deals of every book on
// the shelf, so it is as of a cadence tick: the profile's cadence checkpoint
// commits every book's checkpoint (cadence_tree.go), and it is the one
// checkpoint the witness sees. A single book's checkpoint covers one deal
// only, and is never a shelf card's anchor. The host reads the books'
// records under the tick (Extents) and makes the deal entries; it derives
// the card with DeriveStandardHistory.

// StandardHistoryAnchor is a cadence tick as the anchor of a shelf's card.
type StandardHistoryAnchor struct {
	Tick CadenceTick
	// AsOf names the tick's cadence checkpoint (the SHA-256 of its signed
	// statement), its time, and the time of the cadence log's first
	// checkpoint.
	AsOf standardhistory.AsOf
	// Extents is, for every log a tick up to and including this one
	// carried, how many of its records (seq 1 to the extent) the tick
	// commits: the largest checkpoint of the log any of those ticks
	// carries. A log a tick drops once it is ended stays committed at the
	// checkpoint its last tick carried.
	Extents map[string]uint64
}

// StandardHistoryAnchor is the anchor of tick n, or of the newest finished
// tick when n is 0. ErrNotFound when the cadence has no such finished tick.
func (c *Cadence) StandardHistoryAnchor(ctx context.Context, n uint64) (StandardHistoryAnchor, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return StandardHistoryAnchor{}, ErrReleased
	}
	if n == 0 {
		// An unfinished tick (checkpoint_size 0) has no checkpoint yet.
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0`).Scan(&n); err != nil {
			return StandardHistoryAnchor{}, err
		}
		if n == 0 {
			return StandardHistoryAnchor{}, fmt.Errorf("%w: the cadence has no finished tick", ErrNotFound)
		}
	}
	tick, err := c.tickLocked(ctx, n)
	if err != nil {
		return StandardHistoryAnchor{}, err
	}
	if tick.CheckpointSize == 0 {
		return StandardHistoryAnchor{}, fmt.Errorf("%w: tick %d is not finished", ErrNotFound, n)
	}
	at, err := checkpointFromStatement(tick.Checkpoint)
	if err != nil {
		return StandardHistoryAnchor{}, err
	}
	var first []byte
	err = s.db.QueryRowContext(ctx, `SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size > 0 ORDER BY tick LIMIT 1`).Scan(&first)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("%w: the cadence has no finished tick", ErrCorrupt)
	}
	if err != nil {
		return StandardHistoryAnchor{}, err
	}
	firstAt, err := checkpointFromStatement(first)
	if err != nil {
		return StandardHistoryAnchor{}, err
	}
	extents, err := c.extentsLocked(ctx, n)
	if err != nil {
		return StandardHistoryAnchor{}, err
	}
	return StandardHistoryAnchor{
		Tick: tick,
		AsOf: standardhistory.AsOf{
			Anchor:              anchorDigest(at),
			AnchorTime:          evidencerequest.ProfileTime(at.IssuedAt),
			FirstCheckpointTime: evidencerequest.ProfileTime(firstAt.IssuedAt),
		},
		Extents: extents,
	}, nil
}

// extentsLocked reads every finished tick up to n and keeps, per log, the
// largest MMR size carried, as a record count.
func (c *Cadence) extentsLocked(ctx context.Context, n uint64) (map[string]uint64, error) {
	rows, err := c.shelf.db.QueryContext(ctx,
		`SELECT j.key, MAX(json_extract(j.value, '$.size')) FROM cadence_ticks AS t, json_each(t.leaves, '$.deals') AS j
		 WHERE t.tick <= ? AND t.checkpoint_size > 0 GROUP BY j.key`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	extents := map[string]uint64{}
	for rows.Next() {
		var id string
		var size uint64
		if err := rows.Scan(&id, &size); err != nil {
			return nil, err
		}
		leaves, ok := mmr.LeafCount(size)
		if !ok {
			return nil, fmt.Errorf("%w: a tick carries %s at size %d, not a complete MMR", ErrCorrupt, id, size)
		}
		extents[id] = leaves
	}
	return extents, rows.Err()
}
