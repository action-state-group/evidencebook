// SPDX-License-Identifier: Apache-2.0

// Package shelfscript is the scripted shelf of the cross-language shelf
// conformance (evidencebook#254 §5): one set of host parameters, a writer
// phase and a continuer phase, and a dump of everything a shelf file holds
// that the contract fixes. Its vectors are testdata/shelf/script.json (the
// host parameters and both phases, Script) and after-write.json and
// after-continue.json (the dumps after each phase). The storage interlock
// (test/interop/shelf) runs the phases in separate processes, one language
// each, and compares each dump with them byte for byte. Rust and Python run
// the same script from script.json.
package shelfscript

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
	artifactsqlite "github.com/action-state-group/capsule-emit/go/artifact/sqlite"
	"github.com/action-state-group/checkpointed-local-log/go/cll"

	evidencebook "github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/internal/detrand"
)

// The host parameters (evidencebook#254 §2.2): every process that writes the
// scripted shelf, in any language, uses these.
const (
	FileName       = "shelf.db"
	Operator       = "shelf-vectors-operator"
	Namespace      = "shelf"
	CadenceLogID   = "deal-cadence/0123456789abcdef"
	WitnessURL     = "https://witness.test"
	WitnessID      = "test-witness"
	PadBucket      = 4
	Interval       = 5 * time.Minute
	Jitter         = time.Minute
	WriteStream    = "evidencebook shelf vectors/write"
	ContinueStream = "evidencebook shelf vectors/continue"
)

// Ended is the profile's Ended predicate: these logs have ended.
var Ended = []string{"deal/b", "legacy/old"}

// Start is the writer phase's first time; every step's time is an offset
// from it, and the clock does not move between steps.
var Start = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// seed32 is the 32-byte seed whose byte i is first + i.
func seed32(first byte) []byte {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = first + byte(i)
	}
	return s
}

// The keys: the record signer, the checkpoint key and the witness.
var (
	SignerKey     = ed25519.NewKeyFromSeed(seed32(0x01))
	CheckpointKey = ed25519.NewKeyFromSeed(seed32(0x11))
	WitnessKey    = ed25519.NewKeyFromSeed(seed32(0x21))
)

// clock is the script's clock: it reads the step's time and never advances
// on its own, so every implementation stamps the same times whatever number
// of reads it makes.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// env is one open shelf of the script.
type env struct {
	clock   *clock
	db      *sql.DB
	shelf   *evidencebook.Shelf
	cadence *evidencebook.Cadence
}

func openDB(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// As the shelf's own handle: every transaction begins IMMEDIATE.
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: absolute, RawQuery: "_txlock=immediate"}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := db.Exec(statement); err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	return db, nil
}

// Directory is the cadence's witness directory: the script's one witness.
func Directory() (aacwitness.Directory, error) {
	raw := fmt.Sprintf(`{"witnesses": [{"name": "test witness", "endpoint": %q, "key_ids": [%q]}]}`,
		WitnessURL, hex.EncodeToString(WitnessKey.Public().(ed25519.PublicKey)))
	return aacwitness.ParseDirectory([]byte(raw))
}

func open(ctx context.Context, path, stream string, at time.Time) (*env, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	originals, err := artifactsqlite.New(db, Namespace, []ed25519.PublicKey{SignerKey.Public().(ed25519.PublicKey)})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := originals.Init(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	signer, err := evidencebook.NewEd25519Signer(SignerKey)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	c := &clock{t: at}
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, evidencebook.ShelfConfig{
		Path: path, Operator: Operator, Signer: signer, CheckpointKey: CheckpointKey,
		Originals: originals, Now: c.Now, Random: detrand.New(stream),
	})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	directory, err := Directory()
	if err != nil {
		return nil, errors.Join(err, shelf.Release(), db.Close())
	}
	cadence, err := shelf.Cadence(ctx, evidencebook.CadenceConfig{
		LogID: CadenceLogID, Interval: Interval, Jitter: Jitter, PadBucket: PadBucket,
		WitnessIDs: []string{WitnessID}, Witnesses: directory,
		Ended: func(_ context.Context, id string) (bool, error) { return slices.Contains(Ended, id), nil },
	})
	if err != nil {
		return nil, errors.Join(err, shelf.Release(), db.Close())
	}
	return &env{clock: c, db: db, shelf: shelf, cadence: cadence}, nil
}

func (e *env) release() error {
	return errors.Join(e.shelf.Release(), e.db.Close())
}

// observation is the script's book record: an observed event about subject
// with one payload.
func observation(subject, payload string) evidencebook.Entry {
	return evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: subject, Payloads: [][]byte{[]byte(payload)}}
}

// pendingEntry is the record a crash leaves stored and not in its log. It
// carries no payload, so no original is left behind in the artifact store.
func pendingEntry(subject string) evidencebook.Entry {
	return evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: subject}
}

func (e *env) append(ctx context.Context, bookID string, entry evidencebook.Entry) error {
	book, err := e.shelf.Open(ctx, bookID)
	if err != nil {
		return err
	}
	_, err = book.Append(ctx, entry)
	return err
}

// HostValue is the n-th (from 1) entry the script appends to its host log
// in the cadence: SHA-256("legacy/old" ‖ uint8(n)).
func HostValue(n byte) []byte {
	sum := sha256.Sum256(append([]byte("legacy/old"), n))
	return sum[:]
}

func (e *env) appendHost(ctx context.Context, n byte) error {
	host, err := e.shelf.HostLog(ctx, "legacy/old", true)
	if err != nil {
		return err
	}
	return host.Write(ctx, func(stores evidencebook.HostLogStores) error {
		_, err := stores.Log.Append(ctx, cll.AppendInput{Value: HostValue(n), AppendedAt: e.clock.Now().UTC()})
		return err
	})
}

// tick cuts the next tick at its due time (the first at Start + 30s).
func (e *env) tick(ctx context.Context) (evidencebook.CadenceTick, error) {
	now := Start.Add(30 * time.Second)
	if last, ok, err := e.cadence.Last(ctx); err != nil {
		return evidencebook.CadenceTick{}, err
	} else if ok {
		now = last.DueNext
	}
	e.clock.set(now)
	result, err := e.cadence.Tick(ctx, now)
	if err != nil {
		return evidencebook.CadenceTick{}, err
	}
	if !result.Ticked {
		return evidencebook.CadenceTick{}, fmt.Errorf("no tick due at %s (due %s)", now, result.DueNext)
	}
	return result.Tick, nil
}

func (e *env) witness(ctx context.Context, tick evidencebook.CadenceTick) error {
	receipt, err := MintReceipt(tick.Checkpoint)
	if err != nil {
		return err
	}
	check, err := e.cadence.Witnessed(ctx, tick.N, receipt)
	if err != nil {
		return err
	}
	if check.State != evidencebook.WitnessVerified {
		return fmt.Errorf("tick %d's receipt is %s: %s", tick.N, check.State, check.Reason)
	}
	return nil
}

// Step is one step of the script, as the vector's script member lists it:
//
//   - "open": open the shelf with the random stream Stream and the clock at
//     Start + At seconds;
//   - "release": release it;
//   - "host_append": append HostValue(N) to the host log "legacy/old" in the
//     cadence (HostLog), at the clock;
//   - "append": append an observation about Subject with the one payload
//     Payload to Book, at the clock;
//   - "tick": set the clock to the last tick's due time (the first tick:
//     Start + 30 s) and tick; with Witness, report MintReceipt's receipt for
//     it, which must verify;
//   - "plant_pending": with the shelf released, store Book's next record (an
//     observation about Subject with no payload, sealed At seconds after the
//     clock) without its log entry, as a crash between the two writes leaves
//     it (plantPending).
type Step struct {
	Op      string `json:"op"`
	Stream  string `json:"stream,omitempty"`
	At      int64  `json:"at,omitempty"`
	N       byte   `json:"n,omitempty"`
	Book    string `json:"book,omitempty"`
	Subject string `json:"subject,omitempty"`
	Payload string `json:"payload,omitempty"`
	Witness bool   `json:"witness,omitempty"`
}

// WritePhase is the writer phase, on a fresh file: two books and a host log
// in the cadence, three ticks of which the first and the third are
// witnessed, and, after the release, a crash left pending.
var WritePhase = []Step{
	{Op: "open", Stream: WriteStream},
	{Op: "host_append", N: 1},
	{Op: "host_append", N: 2},
	{Op: "append", Book: "deal/a", Subject: "a", Payload: "a1"},
	{Op: "append", Book: "deal/a", Subject: "a", Payload: "a2"},
	{Op: "append", Book: "deal/b", Subject: "b", Payload: "b1"},
	{Op: "tick", Witness: true},
	{Op: "append", Book: "deal/a", Subject: "a", Payload: "a3"},
	{Op: "tick"},
	{Op: "append", Book: "deal/b", Subject: "b", Payload: "b2"},
	{Op: "host_append", N: 3},
	{Op: "tick", Witness: true},
	{Op: "release"},
	{Op: "plant_pending", Book: "deal/a", Subject: "a4", At: 60},
}

// ContinuePhase is the continuer phase, on the file the writer left: the
// first open of deal/a recovers its pending record, the books and the host
// log grow, a new book starts, a fourth tick is witnessed; after a reopen
// deal/a grows past what that tick carries and a fifth tick is cut, not
// witnessed, so its chain for deal/a is a "part" chain.
var ContinuePhase = []Step{
	{Op: "open", Stream: ContinueStream, At: 3600},
	{Op: "append", Book: "deal/a", Subject: "a", Payload: "a5"},
	{Op: "append", Book: "deal/c", Subject: "c", Payload: "c1"},
	{Op: "host_append", N: 4},
	{Op: "tick", Witness: true},
	{Op: "release"},
	{Op: "open", Stream: ContinueStream + "/reopen", At: 7200},
	{Op: "append", Book: "deal/a", Subject: "a", Payload: "a6"},
	{Op: "tick"},
	{Op: "release"},
}

// Write runs WritePhase on a new dir/shelf.db.
func Write(ctx context.Context, dir string) error {
	path := filepath.Join(dir, FileName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s exists", path)
	}
	return Run(ctx, path, WritePhase)
}

// Continue runs ContinuePhase on dir/shelf.db.
func Continue(ctx context.Context, dir string) error {
	return Run(ctx, filepath.Join(dir, FileName), ContinuePhase)
}

// Run runs steps on the shelf file at path.
func Run(ctx context.Context, path string, steps []Step) error {
	var e *env
	var now time.Time
	for i, step := range steps {
		err := func() error {
			if (e == nil) != (step.Op == "open" || step.Op == "plant_pending") {
				return fmt.Errorf("%s with the shelf %s", step.Op, map[bool]string{true: "released", false: "open"}[e == nil])
			}
			switch step.Op {
			case "open":
				var err error
				e, err = open(ctx, path, step.Stream, Start.Add(time.Duration(step.At)*time.Second))
				return err
			case "release":
				now = e.clock.Now()
				err := e.release()
				e = nil
				return err
			case "host_append":
				return e.appendHost(ctx, step.N)
			case "append":
				return e.append(ctx, step.Book, observation(step.Subject, step.Payload))
			case "tick":
				tick, err := e.tick(ctx)
				if err != nil || !step.Witness {
					return err
				}
				return e.witness(ctx, tick)
			case "plant_pending":
				return plantPending(ctx, path, step.Book, pendingEntry(step.Subject), now.Add(time.Duration(step.At)*time.Second))
			}
			return fmt.Errorf("unknown step %q", step.Op)
		}()
		if err != nil {
			if e != nil {
				err = errors.Join(err, e.release())
			}
			return fmt.Errorf("step %d (%s): %w", i+1, step.Op, err)
		}
	}
	if e != nil {
		return errors.Join(errors.New("the script left the shelf open"), e.release())
	}
	return nil
}

// Hold opens the scripted shelf in dir, holding its writer lock until the
// returned release is called; it waits for another holder until ctx is done.
func Hold(ctx context.Context, dir string) (release func() error, err error) {
	e, err := open(ctx, filepath.Join(dir, FileName), "evidencebook shelf vectors/hold", Start)
	if err != nil {
		return nil, err
	}
	return e.release, nil
}

// plantPending leaves bookID's next record stored and not in its log, as a
// crash between the two writes does. The record is the one bookID's next
// append at `at` would seal, made on a copy of the file; only its row (and
// the standing the same transaction writes) is copied back.
func plantPending(ctx context.Context, path, bookID string, entry evidencebook.Entry, at time.Time) error {
	scratch, err := os.MkdirTemp(filepath.Dir(path), "pending-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	copyPath := filepath.Join(scratch, FileName)
	db, err := openDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, copyPath); err != nil {
		return err
	}
	e, err := open(ctx, copyPath, "evidencebook shelf vectors/pending", at)
	if err != nil {
		return err
	}
	if err := e.append(ctx, bookID, entry); err != nil {
		return errors.Join(err, e.release())
	}
	if err := e.release(); err != nil {
		return err
	}
	copied, err := openDB(copyPath)
	if err != nil {
		return err
	}
	defer copied.Close()
	var seq int64
	var recordID string
	var header, capsule, envelope []byte
	if err := copied.QueryRowContext(ctx,
		`SELECT seq, record_id, header, capsule, envelope FROM book_records WHERE book_id = ? ORDER BY seq DESC LIMIT 1`, bookID).
		Scan(&seq, &recordID, &header, &capsule, &envelope); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES (?, ?, ?, ?, ?, ?)`, []any{bookID, seq, recordID, header, capsule, envelope}},
		{`INSERT OR IGNORE INTO shelf_logs (log_id, kind) VALUES (?, 'book')`, []any{bookID}},
		{`INSERT OR IGNORE INTO cadence_logs (log_id) VALUES (?)`, []any{bookID}},
		{`INSERT OR IGNORE INTO cadence_live (log_id) VALUES (?)`, []any{bookID}},
	} {
		if _, err := tx.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Script is the vector's script member: the host parameters and both
// phases, which an implementation in another language runs as Run does.
func Script() ([]byte, error) {
	public := func(key ed25519.PrivateKey) string { return hex.EncodeToString(key.Public().(ed25519.PublicKey)) }
	return marshal(map[string]any{
		"description": "The scripted shelf of evidencebook#254 §5, written by internal/shelfscript (EB_WRITE_SHELF_VECTORS=1 go test ./internal/shelfscript). Run write_phase on a new file, then continue_phase; after-write.json and after-continue.json are the dumps (Dump) after each. Random streams are internal/detrand's: block i of stream s is SHA-256(UTF8(s) ‖ uint64be(i)). The witness receipt for each witnessed tick is in the dumps' cadence_ticks.receipt.",
		"file":        FileName,
		"start":       Start.Format(time.RFC3339),
		"keys": map[string]any{
			"signer_seed": hex.EncodeToString(SignerKey.Seed()), "signer_public": public(SignerKey),
			"checkpoint_seed": hex.EncodeToString(CheckpointKey.Seed()), "checkpoint_public": public(CheckpointKey),
			"witness_seed": hex.EncodeToString(WitnessKey.Seed()), "witness_public": public(WitnessKey),
		},
		"host_parameters": map[string]any{
			"operator": Operator, "developer": "", "authenticated_indexes": "defaults",
			"originals": map[string]any{"store": "capsule-emit artifact store, SQLite backend, on the shelf's file", "namespace": Namespace},
			"cadence": map[string]any{
				"log_id": CadenceLogID, "interval_s": int64(Interval / time.Second), "jitter_s": int64(Jitter / time.Second),
				"pad_bucket": PadBucket, "witness_ids": []string{WitnessID}, "witness_url": WitnessURL, "ended": Ended,
			},
			"host_log":  map[string]any{"log_id": "legacy/old", "in_cadence": true, "value": "SHA-256(UTF8(\"legacy/old\") ‖ uint8(n))"},
			"lock_path": FileName + ".lock",
			"clock":     "the time a step sets; reads never advance it",
			"record":    "observation (epistemic_type observed_event), subject_ref the step's subject, one payload: the step's payload in UTF-8 (none for plant_pending)",
		},
		"write_phase":    WritePhase,
		"continue_phase": ContinuePhase,
	})
}

// base64url is unpadded base64url, as the shelf stores statements.
func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// rawJSON keeps stored JSON text as it is when the dump is indented: a
// string, so its bytes are compared, not its meaning.
func rawJSON(b []byte) any {
	if b == nil {
		return nil
	}
	return string(b)
}

// marshal is a dump's bytes: indented JSON with sorted keys and a final
// newline.
func marshal(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
