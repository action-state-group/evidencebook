// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
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
// check); the host reports the receipt with Witnessed, which verifies it.

// CadenceConfig is a shelf's cadence. The defaults and limits are
// capsule-cli's: a tick every 5m, give or take 1m (288 a day).
type CadenceConfig struct {
	// LogID is the profile's cadence log, in the shelf's file
	// ("deal-cadence/<16hex>" for a deal profile).
	LogID string
	// Interval is at least 1m; Jitter is under half of it. Zero values take
	// the defaults (5m, 1m), unless NoJitter says the jitter is zero.
	Interval, Jitter time.Duration
	// NoJitter makes every tick due exactly Interval after the last (a
	// profile configured with no jitter).
	NoJitter bool
	// WitnessIDs are the witnesses each cadence checkpoint is queued for:
	// the checkpoint and a pending delivery to each are one commit of the
	// cadence log, as capsule-cli's checkpoint runner does. The host
	// delivers them (cll's PendingWitnesses on the cadence log) and reports
	// each receipt with Witnessed. None queues nothing.
	WitnessIDs []string
	// Witnesses is the witness directory receipts are verified under: the
	// transparency services' rows and keys (a witnesses.json, parsed with
	// AAC's witness.ParseDirectory). The zero Directory trusts no witness,
	// so every receipt is NOT_CHECKED and no tick is witnessed.
	Witnesses aacwitness.Directory
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
	if c.Jitter == 0 && !c.NoJitter {
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
	case c.NoJitter && c.Jitter != 0:
		return fmt.Errorf("%w: a cadence with NoJitter has no jitter", ErrInvalid)
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
	// Witnessed is set once a receipt for the checkpoint verified
	// (Witnessed), and Receipt is that receipt. Ticks witnessed before
	// evidencebook verified receipts itself were set on the host's check.
	Witnessed bool
	Receipt   *WitnessReceipt
}

// WitnessReceipt is the witness's receipt for a tick's cadence checkpoint:
// capsule-cli's chain carries these members (cll.WitnessState's receipt).
type WitnessReceipt struct {
	TSURL     string `json:"ts_url"`
	EntryHash string `json:"entry_hash"`
	Receipt   []byte `json:"receipt"`
	LeafIndex uint64 `json:"leaf_index"`
	TreeSize  uint64 `json:"tree_size"`
}

// The states of a receipt's check (Cadence.Witnessed): it verified under
// the configured witness directory; it was not checked (its witness has no
// row or no usable key there, or AAC's verifier withholds it); or it was
// checked and rejected.
const (
	WitnessVerified   = "VERIFIED"
	WitnessNotChecked = "NOT_CHECKED"
	WitnessInvalid    = "INVALID"
)

// WitnessCheck is what checking one receipt found: its state and why. When
// AAC's verifier ran, Reason is its finding and its reason (for example
// "witness_unverified:<ts_url>: not checked: no directory row for this
// witness"); a receipt missing a member is INVALID before any check, with
// evidencebook's own reason.
type WitnessCheck struct {
	State  string
	Reason string
}

// checkWitnessReceipt verifies receipt for the signed cadence checkpoint
// statement under directory with AAC's verifier (go/witness.VerifyReceipts,
// the check a bundle's checkpoint.witnesses gets). evidencebook keeps no
// verifier of its own. An error means the checkpoint statement itself does
// not verify; the caller says whether that is stored state or input.
func checkWitnessReceipt(statement []byte, receipt WitnessReceipt, directory aacwitness.Directory) (WitnessCheck, error) {
	signed, err := aacwitness.ParseSignedCheckpoint(statement)
	if err != nil {
		return WitnessCheck{}, fmt.Errorf("the cadence checkpoint statement does not verify: %v", err)
	}
	record := map[string]interface{}{
		"ts_url":      receipt.TSURL,
		"entry_hash":  receipt.EntryHash,
		"receipt_b64": base64.StdEncoding.EncodeToString(receipt.Receipt),
		"leaf_index":  json.Number(strconv.FormatUint(receipt.LeafIndex, 10)),
		"tree_size":   json.Number(strconv.FormatUint(receipt.TreeSize, 10)),
	}
	status, findings, results := aacwitness.VerifyReceipts([]interface{}{record}, &signed, statement, directory)
	parts := findings
	if len(results) == 1 && results[0].Reason != "" {
		parts = append(parts, results[0].Reason)
	}
	reason := strings.Join(parts, ": ")
	switch status {
	case aacwitness.StatusPass:
		return WitnessCheck{State: WitnessVerified, Reason: reason}, nil
	case aacwitness.StatusWithheld:
		return WitnessCheck{State: WitnessNotChecked, Reason: reason}, nil
	}
	return WitnessCheck{State: WitnessInvalid, Reason: reason}, nil
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
	log, err := openCLLSQLite(s.cfg.Path, cfg.LogID, s.cfg.CheckpointKey, cfg.WitnessIDs)
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
	// substrate is the log's own commitments, for a bundle's consistency
	// proof from an earlier witnessed checkpoint.
	substrate() Substrate
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

func (l bookLog) substrate() Substrate { return l.book.commitments }

func (l bookLog) CutLocal(ctx context.Context, at time.Time) ([]byte, uint64, error) {
	cp, err := l.book.CheckpointAt(ctx, at)
	if err != nil {
		return nil, 0, err
	}
	return cp.Statement, cp.TreeSize, nil
}

// HostLog is a log on the shelf whose records the host seals itself, in its
// own record format, never converted into book records (evidencebook#254
// §2.7). The shelf hands the host, under its lock, the log's CLL backend on
// the shelf's file and the shelf's artifact store (Write), so the host's own
// log layer runs on the shelf's storage. A host log is in the cadence, which
// checkpoints it like a book, or outside it, when its checkpoints and witness
// rows are the host's own on that backend. shelf_logs records it as a host
// log, so Open refuses its id across reopens.
type HostLog struct {
	shelf     *Shelf
	id        string
	log       *CLLSubstrate
	inCadence bool
}

// LegacyLog is a deal's log from before the upgrade (#36 §4): a host log in
// the cadence, whose capsules are not book records, so it is never
// converted. Every write to it goes through Append, under the shelf's lock.
type LegacyLog = HostLog

// HostLogStores is what a host log's writes run on: the log's CLL backend on
// the shelf's file and the shelf's artifact store, under the shelf's one
// namespace (ShelfConfig.Originals). Both are the shelf's; the host must not
// close them or keep them past the write.
type HostLogStores struct {
	Log       cll.Backend
	Originals ArtifactStore
}

// Legacy opens an existing log of the shelf's file that is not a book and
// registers it with the cadence: it counts into the lifetime log count and
// is live until it has ended and a witnessed tick carries its final
// checkpoint. It never provisions a log. A book's id is refused (ErrInvalid).
func (s *Shelf) Legacy(ctx context.Context, logID string) (*LegacyLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostLocked(ctx, logID, true, false)
}

// HostLog opens logID as a host log, provisioning its CLL log when the file
// holds nothing of it. inCadence registers it with the cadence as Legacy
// does; otherwise the cadence never checkpoints it and the host checkpoints
// and witnesses it on its backend. It is the same *HostLog for the same id
// for the shelf's lifetime, and an id opened in the other mode, or as a book,
// is refused (ErrInvalid).
func (s *Shelf) HostLog(ctx context.Context, logID string, inCadence bool) (*HostLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostLocked(ctx, logID, inCadence, true)
}

// hostLocked opens a host log. inCadence registers it with the cadence;
// provision creates its CLL log when absent.
func (s *Shelf) hostLocked(ctx context.Context, logID string, inCadence, provision bool) (*HostLog, error) {
	if s.released {
		return nil, ErrReleased
	}
	if host, ok := s.hosts[logID]; ok {
		if host.inCadence != inCadence {
			return nil, fmt.Errorf("%w: host log %s is already open %s the cadence", ErrInvalid, logID, map[bool]string{true: "in", false: "outside"}[host.inCadence])
		}
		return host, nil
	}
	if _, ok := s.books[logID]; ok {
		return nil, fmt.Errorf("%w: %s is a book, not a host log", ErrInvalid, logID)
	}
	if s.cadence != nil && s.cadence.cfg.LogID == logID {
		return nil, fmt.Errorf("%w: %s is the cadence log", ErrInvalid, logID)
	}
	kind, err := s.logKind(ctx, logID)
	if err != nil {
		return nil, err
	}
	var records int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM book_records WHERE book_id = ?`, logID).Scan(&records); err != nil {
		return nil, err
	}
	if kind == logKindBook || records > 0 {
		return nil, fmt.Errorf("%w: %s is a book, not a host log", ErrInvalid, logID)
	}
	var log *CLLSubstrate
	if provision {
		log, err = OpenCLLSQLite(s.cfg.Path, logID, s.cfg.CheckpointKey)
	} else {
		var backend cll.Backend
		if backend, err = cllsqlite.Open(s.cfg.Path, logID); err != nil {
			return nil, fmt.Errorf("open legacy log %s: %w", logID, err)
		}
		log, err = newCLLSubstrate(backend, logID, s.cfg.CheckpointKey)
	}
	if err != nil {
		return nil, err
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := claimLogKind(ctx, tx, logID, logKindHost); err != nil {
			return err
		}
		if !inCadence {
			return nil
		}
		var known int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cadence_logs WHERE log_id = ?`, logID).Scan(&known); err != nil {
			return err
		}
		if known > 0 {
			return nil
		}
		return markLive(ctx, tx, logID)
	})
	if err != nil {
		return nil, errors.Join(err, log.Release())
	}
	host := &HostLog{shelf: s, id: logID, log: log, inCadence: inCadence}
	s.hosts[logID] = host
	return host, nil
}

// Append runs write, the profile's own step for this log (it opens its own
// handle on the log), under the shelf's lock. write must not call the shelf
// or its cadence: the lock is held. For a log in the cadence, Append puts it
// back in the live set: an append to a closed deal (later evidence) re-enters
// the tick like any append.
func (l *HostLog) Append(ctx context.Context, write func() error) error {
	return l.Write(ctx, func(HostLogStores) error { return write() })
}

// Write runs write on the log's stores (HostLogStores) under the shelf's
// lock, as Append does. write must not call the shelf or its cadence.
func (l *HostLog) Write(ctx context.Context, write func(HostLogStores) error) error {
	l.shelf.mu.Lock()
	defer l.shelf.mu.Unlock()
	if l.shelf.released {
		return ErrReleased
	}
	// The log is live before the write, so no write, finished or not, is
	// left outside the tick; a log live without a new entry costs only one
	// unchanged checkpoint in the next tick.
	if l.inCadence {
		if err := l.shelf.inTx(ctx, func(tx *sql.Tx) error { return markLive(ctx, tx, l.id) }); err != nil {
			return err
		}
	}
	l.log.mu.Lock()
	defer l.log.mu.Unlock()
	return write(HostLogStores{Log: l.log.log, Originals: l.shelf.cfg.Originals})
}

func (l *HostLog) LogID() string { return l.id }

func (l *HostLog) MMRSize(ctx context.Context) (uint64, error) {
	leaves, err := l.log.Size(ctx)
	if err != nil {
		return 0, err
	}
	return mmrSize(leaves), nil
}

func (l *HostLog) substrate() Substrate { return l.log }

func (l *HostLog) CutLocal(ctx context.Context, at time.Time) ([]byte, uint64, error) {
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
	return s.hostLocked(ctx, logID, true, false)
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
	// Every draw is from the shelf's one source, in this order: filler 0's
	// position and nonce; each live log's salt and position, in log id
	// order; the derived fillers' seed when there are any; the jitter; then
	// each padding record's nonce (finishLocked).
	random := s.random
	used := positions{}
	leaves := TickLeaves{Deals: map[string]TickLeaf{}}
	if leaves.FillerIndex, err = used.random(random); err != nil {
		return TickResult{}, err
	}
	if leaves.FillerNonce, err = random.hex(32); err != nil {
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
		if leaf.Salt, err = random.hex(32); err != nil {
			return TickResult{}, err
		}
		if leaf.Index, err = used.random(random); err != nil {
			return TickResult{}, err
		}
		leaves.Deals[id] = leaf
	}
	// The occupancy floor: H+1 occupied positions, as when every log was in
	// every tick.
	if gap := lifetime - uint64(len(live)); lifetime > uint64(len(live)) && gap > 0 {
		if leaves.FillerSeed, err = random.hex(32); err != nil {
			return TickResult{}, err
		}
		leaves.FillerCount = gap
	}
	// The jitter is uniform over [-Jitter, +Jitter] in nanoseconds.
	jitter := time.Duration(0)
	if c.cfg.Jitter > 0 {
		r, err := random.uniform(uint64(2*c.cfg.Jitter) + 1)
		if err != nil {
			return TickResult{}, err
		}
		jitter = time.Duration(r) - c.cfg.Jitter
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
		id, err := paddingRecordID(s.random)
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
func paddingRecordID(random randomSource) (string, error) {
	nonce, err := random.hex(32)
	if err != nil {
		return "", err
	}
	return paddingRecordIDFor(nonce)
}

// paddingRecordIDFor is the padding record id for nonce (64 lowercase hex).
func paddingRecordIDFor(nonce string) (string, error) {
	encoded, err := canonicalJSON(map[string]any{"record_type": "padding", "epistemic_type": "producer_claim", "store_nonce": nonce})
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

// indexWitnessed records that a witnessed tick carries a log checkpoint. A
// log that has not grown carries the same checkpoint in several ticks: the
// row keeps the newest witnessed tick that carried it, whatever order the
// receipts arrive in.
const indexWitnessed = `INSERT INTO cadence_witnessed (log_id, checkpoint_sha256, mmr_size, tick) VALUES (?, ?, ?, ?)
ON CONFLICT (log_id, checkpoint_sha256) DO UPDATE SET tick = excluded.tick WHERE excluded.tick > cadence_witnessed.tick`

// Witnessed checks a witness's receipt for tick n's cadence checkpoint
// under the configured witness directory (CadenceConfig.Witnesses) and
// returns what it found. Only a verified receipt witnesses the tick: in one
// transaction, under the shelf's lock, each log checkpoint the tick carries
// gets a cadence_witnessed row, and each of those logs that has ended and
// has not grown past its largest witnessed checkpoint leaves the live set.
// Each log compares its own size, so a receipt for an older tick reported
// late never moves a log back. A receipt that is NOT_CHECKED changes
// nothing, and the host may report it again once its witness is in the
// directory; one that is INVALID changes nothing and is an ErrInvalid
// naming the reason.
func (c *Cadence) Witnessed(ctx context.Context, n uint64, receipt WitnessReceipt) (WitnessCheck, error) {
	// capsule-cli carries the witness's URL without a trailing slash
	// (trimEndpoint), and a verifier finds the witness's row by it.
	for len(receipt.TSURL) > 0 && receipt.TSURL[len(receipt.TSURL)-1] == '/' {
		receipt.TSURL = receipt.TSURL[:len(receipt.TSURL)-1]
	}
	if receipt.TSURL == "" || receipt.EntryHash == "" || len(receipt.Receipt) == 0 {
		reason := "a witnessed tick needs its receipt, its entry hash and its witness's URL"
		return WitnessCheck{State: WitnessInvalid, Reason: reason}, fmt.Errorf("%w: %s", ErrInvalid, reason)
	}
	encodedReceipt, err := json.Marshal(receipt)
	if err != nil {
		return WitnessCheck{}, err
	}
	s := c.shelf
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return WitnessCheck{}, ErrReleased
	}
	tick, err := c.tickLocked(ctx, n)
	if err != nil {
		return WitnessCheck{}, err
	}
	if tick.CheckpointSize == 0 {
		return WitnessCheck{}, fmt.Errorf("%w: tick %d is not finished, so no witness has seen its root", ErrInvalid, n)
	}
	check, err := checkWitnessReceipt(tick.Checkpoint, receipt, c.cfg.Witnesses)
	switch {
	case err != nil:
		return WitnessCheck{}, fmt.Errorf("%w: tick %d: %v", ErrCorrupt, n, err)
	case check.State == WitnessInvalid:
		return check, fmt.Errorf("%w: tick %d's witness receipt is rejected: %s", ErrInvalid, n, check.Reason)
	case check.State != WitnessVerified:
		return check, nil
	}
	ended := map[string]bool{}
	for id := range tick.Leaves.Deals {
		if ended[id], err = c.cfg.Ended(ctx, id); err != nil {
			return WitnessCheck{}, err
		}
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE cadence_ticks SET witnessed = 1, receipt = COALESCE(receipt, ?) WHERE tick = ?`, string(encodedReceipt), n); err != nil {
			return err
		}
		for id, leaf := range tick.Leaves.Deals {
			if _, err := tx.ExecContext(ctx,
				indexWitnessed,
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
	if err != nil {
		return WitnessCheck{}, err
	}
	return check, nil
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
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	if c.shelf.released {
		return "", CadenceTick{}, TickLeaf{}, ErrReleased
	}
	return c.witnessedTickLocked(ctx, logID, checkpointSHA256, mmrSize)
}

func (c *Cadence) witnessedTickLocked(ctx context.Context, logID, checkpointSHA256 string, mmrSize uint64) (extent string, tick CadenceTick, leaf TickLeaf, err error) {
	s := c.shelf
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
	var receipt sql.NullString
	err := c.shelf.db.QueryRowContext(ctx,
		`SELECT at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt FROM cadence_ticks WHERE tick = ?`, n).
		Scan(&at, &due, &tick.EntrySeq, &tick.CheckpointSize, &tick.Checkpoint, &leaves, &witnessed, &receipt)
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
	if receipt.Valid {
		tick.Receipt = &WitnessReceipt{}
		if err := json.Unmarshal([]byte(receipt.String), tick.Receipt); err != nil {
			return CadenceTick{}, fmt.Errorf("%w: tick %d receipt: %v", ErrCorrupt, n, err)
		}
	}
	return tick, nil
}

// Last returns the cadence's newest stored tick; ok is false when it has
// none. A host reads a log's pending standing from it (the newest tick
// holding the log, capsule-cli#242 §2.5) and maps a delivered cadence
// checkpoint back to its tick, walking back with Get.
func (c *Cadence) Last(ctx context.Context) (tick CadenceTick, ok bool, err error) {
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	if c.shelf.released {
		return CadenceTick{}, false, ErrReleased
	}
	return c.lastLocked(ctx)
}

// Get returns stored tick n.
func (c *Cadence) Get(ctx context.Context, n uint64) (CadenceTick, error) {
	c.shelf.mu.Lock()
	defer c.shelf.mu.Unlock()
	return c.tickLocked(ctx, n)
}
