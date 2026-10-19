// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"time"

	cllsqlite "github.com/action-state-group/checkpointed-local-log/go/store/sqlite"
)

// The heartbeat cadence (evidencebook#36 §2), ported from capsule-cli's
// deal_cadence.go: on time alone, each tick checkpoints every live log of a
// shelf locally, commits those checkpoints as salted leaves of the cadence
// tree (cadence_tree.go), appends the root to the profile's cadence log and
// checkpoints that log, which alone goes to the witness. What changes from
// capsule-cli is which logs a tick cuts: the live set, not every log ever.
// A log is live while it is not ended, or while it has grown past the
// largest checkpoint of it a witnessed tick carries. Delivering the cadence
// checkpoint to the witness is the host's (its endpoint, token and receipt
// check); the host reports a verified receipt with Witnessed.

// CadenceConfig is a shelf's cadence. The defaults and limits are
// capsule-cli's: a tick every 5m, give or take 1m (288 a day).
type CadenceConfig struct {
	// LogID is the profile's cadence log, in the shelf's file
	// ("deal-cadence/<16hex>" for a deal profile).
	LogID string
	// Interval is at least 1m; Jitter is under half of it. Zero values take
	// the defaults (5m, 1m).
	Interval, Jitter time.Duration
	// PadBucket pads the cadence log to a multiple of this many entries per
	// tick, at most 1024; zero is 1 (no padding).
	PadBucket uint64
	// Ended is the profile's predicate: whether a log has ended (for a
	// deal, a close step is sealed). It is evaluated each time it is
	// needed, never stored.
	Ended func(ctx context.Context, logID string) (bool, error)
}

func (c *CadenceConfig) defaults() error {
	if c.Interval == 0 {
		c.Interval = 5 * time.Minute
	}
	if c.Jitter == 0 {
		c.Jitter = time.Minute
	}
	if c.PadBucket == 0 {
		c.PadBucket = 1
	}
	switch {
	case c.LogID == "":
		return fmt.Errorf("%w: a cadence needs its log id", ErrInvalid)
	case c.Ended == nil:
		return fmt.Errorf("%w: a cadence needs the profile's Ended predicate", ErrInvalid)
	case c.Interval < time.Minute:
		return fmt.Errorf("%w: the cadence interval must be at least 1m", ErrInvalid)
	case c.Jitter < 0 || 2*c.Jitter >= c.Interval:
		return fmt.Errorf("%w: the cadence jitter must be at least 0 and under half the interval", ErrInvalid)
	case c.PadBucket > 1024:
		return fmt.Errorf("%w: the cadence pad bucket must be at most 1024", ErrInvalid)
	}
	return nil
}

// CadenceTick is one tick as stored: capsule-cli's deal_cadence row.
type CadenceTick struct {
	N        uint64
	At       time.Time
	DueNext  time.Time
	EntrySeq uint64
	// CheckpointSize and Checkpoint are the cadence log's checkpoint (MMR
	// size and COSE statement) that carries this tick's root.
	CheckpointSize uint64
	Checkpoint     []byte
	Leaves         TickLeaves
	// Witnessed is set once the host reported the checkpoint's receipt
	// verified (Witnessed).
	Witnessed bool
}

// TickResult is what one Tick call did.
type TickResult struct {
	// Ticked is false when no tick was due; DueNext then says when one is.
	Ticked  bool
	Tick    CadenceTick
	DueNext time.Time
}

// Cadence runs a shelf's heartbeat ticks.
type Cadence struct {
	shelf *Shelf
	cfg   CadenceConfig
	log   *CLLSubstrate
}

// Cadence opens the shelf's cadence log, creating it when absent.
func (s *Shelf) Cadence(ctx context.Context, cfg CadenceConfig) (*Cadence, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, ErrReleased
	}
	// One cadence per shelf: two would cut ticks into one table from two
	// handles on one log.
	if s.cadence != nil {
		return nil, fmt.Errorf("%w: the shelf already runs a cadence (%s)", ErrInvalid, s.cadence.cfg.LogID)
	}
	log, err := OpenCLLSQLite(s.cfg.Path, cfg.LogID, s.cfg.CheckpointKey)
	if err != nil {
		return nil, err
	}
	s.cadenceLogs = append(s.cadenceLogs, log)
	s.cadence = &Cadence{shelf: s, cfg: cfg, log: log}
	return s.cadence, nil
}

// CadenceLog is what a tick checkpoints: a book, or a deal's log from before
// the upgrade (LegacyLog).
type CadenceLog interface {
	LogID() string
	// MMRSize is the MMR node count of everything the log holds.
	MMRSize(ctx context.Context) (uint64, error)
	// CutLocal checkpoints everything the log holds at at, cutting a new
	// checkpoint only if the log grew, and returns the checkpoint's COSE
	// statement and MMR size. Nothing is sent to a witness.
	CutLocal(ctx context.Context, at time.Time) (statement []byte, mmrSize uint64, err error)
}

// mmrSize is the MMR node count of n leaves: 2n minus the number of peaks.
func mmrSize(leaves uint64) uint64 {
	return 2*leaves - uint64(bits.OnesCount64(leaves))
}

type bookLog struct{ book *Book }

func (l bookLog) LogID() string { return l.book.id }

func (l bookLog) MMRSize(ctx context.Context) (uint64, error) {
	leaves, err := l.book.commitments.Size(ctx)
	if err != nil {
		return 0, err
	}
	return mmrSize(leaves), nil
}

func (l bookLog) CutLocal(ctx context.Context, at time.Time) ([]byte, uint64, error) {
	cp, err := l.book.CheckpointAt(ctx, at)
	if err != nil {
		return nil, 0, err
	}
	return cp.Statement, cp.TreeSize, nil
}

// LegacyLog is a deal's log from before the upgrade (#36 §4): its capsules
// are not book records, so it is never converted. It takes part in the
// cadence like a book. Every write to it goes through Append, under the
// shelf's lock.
type LegacyLog struct {
	shelf *Shelf
	id    string
	log   *CLLSubstrate
}

// Legacy opens an existing log of the shelf's file that is not a book and
// registers it with the cadence: it counts into the lifetime log count and
// is live until it has ended and a witnessed tick carries its final
// checkpoint. It never provisions a log.
func (s *Shelf) Legacy(ctx context.Context, logID string) (*LegacyLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.legacyLocked(ctx, logID, true)
}

func (s *Shelf) legacyLocked(ctx context.Context, logID string, register bool) (*LegacyLog, error) {
	if s.released {
		return nil, ErrReleased
	}
	if legacy, ok := s.legacy[logID]; ok {
		return legacy, nil
	}
	var records int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, logID).Scan(&records); err != nil {
		return nil, err
	}
	if records > 0 {
		return nil, fmt.Errorf("%w: %s is a book, not a legacy log", ErrInvalid, logID)
	}
	backend, err := cllsqlite.Open(s.cfg.Path, logID)
	if err != nil {
		return nil, fmt.Errorf("open legacy log %s: %w", logID, err)
	}
	log, err := newCLLSubstrate(backend, logID, s.cfg.CheckpointKey)
	if err != nil {
		return nil, err
	}
	if register {
		var known int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cadence_logs WHERE log_id = ?`, logID).Scan(&known); err != nil {
			return nil, errors.Join(err, log.Release())
		}
		if known == 0 {
			if err := s.inTx(ctx, func(tx *sql.Tx) error { return markLive(ctx, tx, logID) }); err != nil {
				return nil, errors.Join(err, log.Release())
			}
		}
	}
	legacy := &LegacyLog{shelf: s, id: logID, log: log}
	s.legacy[logID] = legacy
	return legacy, nil
}

// Append runs write, the profile's own step for this log (it opens its own
// handle on the log), under the shelf's lock. write must not call the shelf
// or its cadence: the lock is held. Append puts the log back in the live set: an append to a closed deal (later evidence) re-enters the
// tick like any append.
func (l *LegacyLog) Append(ctx context.Context, write func() error) error {
	l.shelf.mu.Lock()
	defer l.shelf.mu.Unlock()
	if l.shelf.released {
		return ErrReleased
	}
	// The log is live before the write, so no write, finished or not, is
	// left outside the tick; a log live without a new entry costs only one
	// unchanged checkpoint in the next tick.
	if err := l.shelf.inTx(ctx, func(tx *sql.Tx) error { return markLive(ctx, tx, l.id) }); err != nil {
		return err
	}
	return write()
}

func (l *LegacyLog) LogID() string { return l.id }

func (l *LegacyLog) MMRSize(ctx context.Context) (uint64, error) {
	leaves, err := l.log.Size(ctx)
	if err != nil {
		return 0, err
	}
	return mmrSize(leaves), nil
}

func (l *LegacyLog) CutLocal(ctx context.Context, at time.Time) ([]byte, uint64, error) {
	cp, err := l.log.Checkpoint(ctx, at)
	if err != nil {
		return nil, 0, err
	}
	return cp.Statement, cp.TreeSize, nil
}

func (s *Shelf) inTx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// cadenceLogLocked is the CadenceLog of a live log id: its book when it
// has records, else its legacy log.
func (s *Shelf) cadenceLogLocked(ctx context.Context, logID string) (CadenceLog, error) {
	var records int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, logID).Scan(&records); err != nil {
		return nil, err
	}
	if records > 0 {
		book, err := s.openLocked(ctx, logID)
		if err != nil {
			return nil, err
		}
		return bookLog{book}, nil
	}
	return s.legacyLocked(ctx, logID, false)
}

// Tick cuts a tick when one is due at now; otherwise it reports when one
// is. A tick is due when now is at or after the last tick's DueNext, and
// only then: its timing and size never depend on what the logs did.
func (c *Cadence) Tick(ctx context.Context, now time.Time) (TickResult, error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return TickResult{}, ErrReleased
	}
	last, haveLast, err := c.lastLocked(ctx)
	if err != nil {
		return TickResult{}, err
	}
	// A tick stored but not finished (a crash or an error after its row)
	// is finished first, as the same tick: its root, never a new one.
	if haveLast && last.CheckpointSize == 0 {
		return c.finishLocked(ctx, last)
	}
	if haveLast && now.Before(last.DueNext) {
		return TickResult{DueNext: last.DueNext}, nil
	}
	n := uint64(1)
	if haveLast {
		n = last.N + 1
	}
	// Times leaving this device are coarsened to the minute.
	at := now.UTC().Truncate(time.Minute)
	live, err := c.liveLocked(ctx)
	if err != nil {
		return TickResult{}, err
	}
	var lifetime uint64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cadence_logs`).Scan(&lifetime); err != nil {
		return TickResult{}, err
	}
	if len(live) >= 1<<CadenceDepth || lifetime >= 1<<CadenceDepth {
		return TickResult{}, fmt.Errorf("%w: too many logs for one cadence tree", ErrInvalid)
	}
	used := positions{}
	leaves := TickLeaves{Deals: map[string]TickLeaf{}}
	if leaves.FillerIndex, err = used.random(); err != nil {
		return TickResult{}, err
	}
	if leaves.FillerNonce, err = randomHex(32); err != nil {
		return TickResult{}, err
	}
	for _, id := range live {
		log, err := s.cadenceLogLocked(ctx, id)
		if err != nil {
			return TickResult{}, err
		}
		statement, size, err := log.CutLocal(ctx, at)
		if err != nil {
			return TickResult{}, fmt.Errorf("checkpoint %s for the tick: %w", id, err)
		}
		sum := sha256.Sum256(statement)
		leaf := TickLeaf{LogID: id, Size: size, CheckpointSHA256: hex.EncodeToString(sum[:]), Statement: base64.RawURLEncoding.EncodeToString(statement)}
		if leaf.Salt, err = randomHex(32); err != nil {
			return TickResult{}, err
		}
		if leaf.Index, err = used.random(); err != nil {
			return TickResult{}, err
		}
		leaves.Deals[id] = leaf
	}
	// The occupancy floor: H+1 occupied positions, as when every log was in
	// every tick.
	if gap := lifetime - uint64(len(live)); lifetime > uint64(len(live)) && gap > 0 {
		if leaves.FillerSeed, err = randomHex(32); err != nil {
			return TickResult{}, err
		}
		leaves.FillerCount = gap
	}
	jitter := time.Duration(0)
	if c.cfg.Jitter > 0 {
		r, err := rand.Int(rand.Reader, big.NewInt(int64(2*c.cfg.Jitter)+1))
		if err != nil {
			return TickResult{}, err
		}
		jitter = time.Duration(r.Int64()) - c.cfg.Jitter
	}
	tick := CadenceTick{N: n, At: at, DueNext: now.UTC().Add(c.cfg.Interval + jitter).Truncate(time.Second), Leaves: leaves}
	encoded, err := json.Marshal(leaves)
	if err != nil {
		return TickResult{}, err
	}
	// The tick is stored before its root reaches the cadence log, so a
	// failure after this point resumes this tick (finishLocked) rather than
	// cutting another: its leaves, salts and positions are not lost, and the
	// cadence log never grows by more than one tick's entries per tick. A
	// zero checkpoint size marks it unfinished.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)`,
		tick.N, tick.At.Format(time.RFC3339), tick.DueNext.Format(time.RFC3339), string(encoded)); err != nil {
		return TickResult{}, err
	}
	return c.finishLocked(ctx, tick)
}

// finishLocked puts a stored tick's root in the cadence log (once: a root
// already appended after the previous tick's entry is found, not appended
// again), pads the log to the bucket, checkpoints it, and completes the
// tick's row.
func (c *Cadence) finishLocked(ctx context.Context, tick CadenceTick) (TickResult, error) {
	s := c.shelf
	root, _, err := tick.Leaves.Tree(tick.Leaves.FillerIndex)
	if err != nil {
		return TickResult{}, err
	}
	rootID := hex.EncodeToString(root)
	var after uint64
	if tick.N > 1 {
		previous, err := c.tickLocked(ctx, tick.N-1)
		if err != nil {
			return TickResult{}, err
		}
		after = previous.EntrySeq
	}
	// Read only what follows the previous tick's entry: at most this
	// tick's root and padding, never the log's whole history. end is the
	// log's last position.
	entrySeq, end := uint64(0), after
	for {
		entries, err := c.log.Entries(ctx, end, 1000)
		if err != nil {
			return TickResult{}, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if entry.RecordID == rootID && entrySeq == 0 {
				entrySeq = entry.Seq
			}
		}
		end = entries[len(entries)-1].Seq
	}
	if entrySeq == 0 {
		if entrySeq, err = c.log.Append(ctx, rootID, tick.At); err != nil {
			return TickResult{}, err
		}
		end = entrySeq
	}
	// Padding records bring the leaf count to a multiple of the bucket.
	for count := end; count%c.cfg.PadBucket != 0; count++ {
		id, err := paddingRecordID()
		if err != nil {
			return TickResult{}, err
		}
		if _, err := c.log.Append(ctx, id, tick.At); err != nil {
			return TickResult{}, err
		}
	}
	cp, err := c.log.Checkpoint(ctx, tick.At)
	if err != nil {
		return TickResult{}, err
	}
	tick.EntrySeq, tick.CheckpointSize, tick.Checkpoint = entrySeq, cp.TreeSize, cp.Statement
	if _, err := s.db.ExecContext(ctx,
		`UPDATE cadence_ticks SET entry_seq = ?, checkpoint_size = ?, checkpoint = ? WHERE tick = ?`,
		tick.EntrySeq, tick.CheckpointSize, tick.Checkpoint, tick.N); err != nil {
		return TickResult{}, err
	}
	return TickResult{Ticked: true, Tick: tick, DueNext: tick.DueNext}, nil
}

// paddingRecordID is the id of an Evidence Layer padding record, as
// capsule-cli's dealPaddingRecordID: the JCS digest of {"record_type":
// "padding", "epistemic_type": "producer_claim", "store_nonce": <fresh 256
// bits>}. Its only content is the nonce.
func paddingRecordID() (string, error) {
	nonce, err := randomHex(32)
	if err != nil {
		return "", err
	}
	encoded, err := canonicalJSON(map[string]any{"record_type": "padding", "epistemic_type": "producer_claim", "store_nonce": nonce})
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

// Witnessed records that tick n's cadence checkpoint has a verified witness
// receipt. In one transaction, under the shelf's lock: each log checkpoint
// the tick carries gets a cadence_witnessed row, and each of those logs that
// has ended and has not grown past its largest witnessed checkpoint leaves
// the live set. Rows are only inserted and each compares its own size, so a
// receipt for an older tick reported late never moves a log back.
func (c *Cadence) Witnessed(ctx context.Context, n uint64) error {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return ErrReleased
	}
	tick, err := c.tickLocked(ctx, n)
	if err != nil {
		return err
	}
	if tick.CheckpointSize == 0 {
		return fmt.Errorf("%w: tick %d is not finished, so no witness has seen its root", ErrInvalid, n)
	}
	ended := map[string]bool{}
	for id := range tick.Leaves.Deals {
		if ended[id], err = c.cfg.Ended(ctx, id); err != nil {
			return err
		}
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE cadence_ticks SET witnessed = 1 WHERE tick = ?`, n); err != nil {
			return err
		}
		for id, leaf := range tick.Leaves.Deals {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO cadence_witnessed (log_id, checkpoint_sha256, mmr_size, tick) VALUES (?, ?, ?, ?)`,
				id, leaf.CheckpointSHA256, leaf.Size, n); err != nil {
				return err
			}
			var witnessed uint64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(mmr_size), 0) FROM cadence_witnessed WHERE log_id = ?`, id).Scan(&witnessed); err != nil {
				return err
			}
			// The log's size is read in this transaction from what it holds:
			// a book's stored records (stored before their log append, so a
			// pending append counts) or a legacy log's entries. An append
			// committed after this transaction re-enters the live set.
			size, err := heldSizeTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if ended[id] && size <= witnessed {
				if _, err := tx.ExecContext(ctx, `DELETE FROM cadence_live WHERE log_id = ?`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// heldSizeTx is the MMR size of everything a log holds, read in tx: a
// book's stored records, else a legacy log's CLL entries.
func heldSizeTx(ctx context.Context, tx *sql.Tx, logID string) (uint64, error) {
	var leaves uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, logID).Scan(&leaves); err != nil {
		return 0, err
	}
	if leaves == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cll_entries WHERE log_id = ?`, logID).Scan(&leaves); err != nil {
			return 0, err
		}
	}
	return mmrSize(leaves), nil
}

// WitnessedTick finds the witnessed tick a bundle of logID's checkpoint
// (its statement's SHA-256 and MMR size) is anchored by, from the index:
// "all" when a witnessed tick carries that very checkpoint, else "part"
// with the newest witnessed tick carrying an earlier checkpoint of the log
// (a bundle then adds a consistency proof from it). ErrNotFound when no
// witnessed tick carries any checkpoint of the log up to that size.
func (c *Cadence) WitnessedTick(ctx context.Context, logID, checkpointSHA256 string, mmrSize uint64) (extent string, tick CadenceTick, leaf TickLeaf, err error) {
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return "", CadenceTick{}, TickLeaf{}, ErrReleased
	}
	var n uint64
	err = s.db.QueryRowContext(ctx, `SELECT tick FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ?`, logID, checkpointSHA256).Scan(&n)
	extent = "all"
	if errors.Is(err, sql.ErrNoRows) {
		extent = "part"
		err = s.db.QueryRowContext(ctx,
			`SELECT tick FROM cadence_witnessed WHERE log_id = ? AND mmr_size < ? ORDER BY mmr_size DESC, tick DESC LIMIT 1`, logID, mmrSize).Scan(&n)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", CadenceTick{}, TickLeaf{}, fmt.Errorf("%w: no witnessed tick carries %s", ErrNotFound, logID)
	}
	if err != nil {
		return "", CadenceTick{}, TickLeaf{}, err
	}
	if tick, err = c.tickLocked(ctx, n); err != nil {
		return "", CadenceTick{}, TickLeaf{}, err
	}
	return extent, tick, tick.Leaves.Deals[logID], nil
}

// Live lists the live set, in log id order.
func (c *Cadence) Live(ctx context.Context) ([]string, error) {
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	return c.liveLocked(ctx)
}

func (c *Cadence) liveLocked(ctx context.Context) ([]string, error) {
	rows, err := c.shelf.db.QueryContext(ctx, `SELECT log_id FROM cadence_live ORDER BY log_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (c *Cadence) lastLocked(ctx context.Context) (CadenceTick, bool, error) {
	var n uint64
	err := c.shelf.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks`).Scan(&n)
	if err != nil || n == 0 {
		return CadenceTick{}, false, err
	}
	tick, err := c.tickLocked(ctx, n)
	return tick, err == nil, err
}

func (c *Cadence) tickLocked(ctx context.Context, n uint64) (CadenceTick, error) {
	tick := CadenceTick{N: n}
	var at, due, leaves string
	var witnessed int
	err := c.shelf.db.QueryRowContext(ctx,
		`SELECT at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed FROM cadence_ticks WHERE tick = ?`, n).
		Scan(&at, &due, &tick.EntrySeq, &tick.CheckpointSize, &tick.Checkpoint, &leaves, &witnessed)
	if errors.Is(err, sql.ErrNoRows) {
		return CadenceTick{}, fmt.Errorf("%w: tick %d", ErrNotFound, n)
	}
	if err != nil {
		return CadenceTick{}, err
	}
	if tick.At, err = time.Parse(time.RFC3339, at); err != nil {
		return CadenceTick{}, fmt.Errorf("%w: tick %d time: %v", ErrCorrupt, n, err)
	}
	if tick.DueNext, err = time.Parse(time.RFC3339, due); err != nil {
		return CadenceTick{}, fmt.Errorf("%w: tick %d due time: %v", ErrCorrupt, n, err)
	}
	if err := json.Unmarshal([]byte(leaves), &tick.Leaves); err != nil {
		return CadenceTick{}, fmt.Errorf("%w: tick %d leaves: %v", ErrCorrupt, n, err)
	}
	tick.Witnessed = witnessed != 0
	return tick, nil
}

// Get returns stored tick n.
func (c *Cadence) Get(ctx context.Context, n uint64) (CadenceTick, error) {
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	return c.tickLocked(ctx, n)
}
