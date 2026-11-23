package book

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/report"
)

// A book profile's store split into one book per time window
// (evidencebook#689, docs/design/689-window-books.md §6): book/shelf.db, a
// windowed shelf whose window is fixed when the store is created. A profile
// whose store is book/log.jsonl keeps its one book.

// shelfFile is a windowed store's file.
func shelfFile(p cli.Profile) string {
	return filepath.Join(p.Connection.Database, "book", "shelf.db")
}

// isWindowedStore reports whether the profile's store is a windowed shelf.
func isWindowedStore(p cli.Profile) (bool, error) {
	_, err := os.Stat(shelfFile(p))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// noticeOut is where store init prints the window it fixed: the core's
// store init owns the command's output, so the notice goes to stderr.
var noticeOut io.Writer = os.Stderr

// defaultWindow is the window `book window init` fixes when the holder
// names none (Sherlock2, #689: month).
const defaultWindow = evidencebook.WindowMonth

// windowNotice is what store creation prints: the window cannot change once
// the store exists.
func windowNotice(period evidencebook.WindowPeriod) string {
	unit := "calendar " + string(period) + "s"
	if period == evidencebook.WindowWeek {
		unit = "ISO weeks from Monday"
	}
	return fmt.Sprintf("window: %s (%s, UTC), fixed for this store", period, unit)
}

// sealedStore reads a window book's records as sealed, the recordStore a
// JSONL book's FileStore is.
type sealedStore struct{ book *evidencebook.Book }

func (s sealedStore) GetRecord(ctx context.Context, recordID string) (evidencebook.StoredRecord, error) {
	return s.book.Sealed(ctx, recordID)
}

// windowedConfig is the shelf configuration of a profile's windowed store.
func windowedConfig(p cli.Profile, signer evidencebook.Signer, checkpointKey ed25519.PrivateKey, artifacts cli.ArtifactStore, now func() time.Time, policy *evidencebook.WindowPolicy) evidencebook.ShelfConfig {
	operator := p.Operator
	if operator == "" {
		// Creating or reading the file writes no record, so it needs no
		// operator; every writing verb refuses a profile without one before
		// it opens the store (openBookKeyed).
		operator = "unset"
	}
	return evidencebook.ShelfConfig{
		Path: shelfFile(p), Operator: operator, Signer: signer, CheckpointKey: checkpointKey,
		Originals: artifacts, Now: now, Window: policy,
	}
}

// openWindowedBook opens a windowed store and returns its current window's
// book: the only one that takes records. The clock floors at the newest
// window's last record before the shelf rolls, so a clock a little behind it
// is pinned as for a JSONL book rather than refused as stepped back.
//
// ephemeral is a read-only open with throwaway keys (openBookForReading): it
// never rolls, since a roll writes window records, and returns the newest
// window's book as it stands.
func openWindowedBook(ctx context.Context, p cli.Profile, signer evidencebook.Signer, checkpointKey ed25519.PrivateKey, artifacts cli.ArtifactStore, ephemeral bool) (openedBook, error) {
	var err error
	if artifacts == nil {
		if artifacts, err = bookOriginals(ctx, p); err != nil {
			return openedBook{}, err
		}
	} else if artifacts, err = durableOriginals(p, artifacts); err != nil {
		return openedBook{}, err
	}
	policy, err := evidencebook.ReadShelfWindow(ctx, shelfFile(p))
	if err != nil {
		return openedBook{}, err
	}
	if policy == nil {
		return openedBook{}, fmt.Errorf("%w: %s holds no window policy", evidencebook.ErrCorrupt, shelfFile(p))
	}
	clock := &monotonicClock{}
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, windowedConfig(p, signer, checkpointKey, artifacts, clock.now, policy))
	if err != nil {
		return openedBook{}, bookError(err)
	}
	fail := func(err error) (openedBook, error) { return openedBook{}, errors.Join(err, shelf.Release()) }
	windows, err := shelf.Windows(ctx)
	if err != nil {
		return fail(err)
	}
	if len(windows) > 0 {
		newest, window, err := shelf.WindowBook(ctx, windows[len(windows)-1].Label)
		if err != nil {
			return fail(err)
		}
		if ephemeral {
			return openedBook{book: newest, store: sealedStore{newest}, shelf: shelf, window: window, originals: artifacts}, nil
		}
		if err = clock.floorAtLastRecord(ctx, newest); err != nil {
			return fail(err)
		}
	} else if ephemeral {
		return fail(cli.Hint(cli.ErrInput, "the windowed store holds no window yet; a verb that writes opens the first"))
	}
	if _, reproduced, err := reproductionAt(); err != nil {
		return fail(err)
	} else if reproduced {
		// A read-only open returned above; a writing one would stamp a caller time.
		return fail(cli.InputError(SourceDateEpochEnv + " is set, but this is a live windowed store: only a JSONL book is reproduced"))
	}
	if err = clock.refuseIfAhead(p); err != nil {
		return fail(err)
	}
	book, window, err := shelf.Current(ctx)
	if err != nil {
		return fail(bookError(err))
	}
	if err = clock.floorAtLastRecord(ctx, book); err != nil {
		return fail(err)
	}
	return openedBook{book: book, store: sealedStore{book}, shelf: shelf, window: window, originals: artifacts, lastCommit: clock.floor}, nil
}

// initWindowedStore creates a profile's windowed store, its window fixed
// from the start of the current period. It writes no record and needs no
// signing key: the shelf's file holds the policy, and the first window's
// book is opened by the first verb that writes.
func initWindowedStore(ctx context.Context, p cli.Profile, period evidencebook.WindowPeriod) error {
	if err := os.MkdirAll(filepath.Dir(shelfFile(p)), 0o700); err != nil {
		return err
	}
	_, scratchKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	signer, err := evidencebook.NewEd25519Signer(scratchKey)
	if err != nil {
		return err
	}
	policy := &evidencebook.WindowPolicy{Period: period, Base: p.LogID, Since: period.WindowAt(bookNow()).Start}
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, windowedConfig(p, signer, scratchKey, noOriginals{profile: p.Name}, bookNow, policy))
	if err != nil {
		return bookError(err)
	}
	return shelf.Release()
}

// windowedShelfForReading opens a windowed store with throwaway keys, for
// a verb that only reads it: a write would sign with a key no trusted_keys
// holds. It takes the store's writer lock while open.
func windowedShelfForReading(ctx context.Context, p cli.Profile) (*evidencebook.Shelf, error) {
	policy, err := evidencebook.ReadShelfWindow(ctx, shelfFile(p))
	if err != nil {
		return nil, err
	}
	_, scratchKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(scratchKey)
	if err != nil {
		return nil, err
	}
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, windowedConfig(p, signer, scratchKey, noOriginals{profile: p.Name}, bookNow, policy))
	return shelf, bookError(err)
}

// listWindowedBook is `cll list` on a windowed store: the newest window's
// records. It does not roll.
func listWindowedBook(ctx context.Context, p cli.Profile, after, through uint64, limit int, all bool) ([]bookEntry, uint64, error) {
	shelf, err := windowedShelfForReading(ctx, p)
	if err != nil {
		return nil, 0, err
	}
	defer shelf.Release()
	windows, err := shelf.Windows(ctx)
	if err != nil || len(windows) == 0 {
		return []bookEntry{}, after, err
	}
	held, err := shelf.Records(ctx, windows[len(windows)-1].LogID)
	if err != nil {
		return nil, 0, err
	}
	records := make([]evidencebook.Record, len(held.Records))
	for i, r := range held.Records {
		records[i] = evidencebook.Record{RecordID: r.RecordID, Seq: r.Seq, Header: r.Header}
	}
	items, next := listEntries(records, after, through, limit, all)
	return items, next, nil
}

// windowWitnessStore keeps a windowed store's witness deliveries per window
// log, in book/witness/<label>.json: the core's delivery state is keyed by
// checkpoint size, which repeats across window logs.
func windowWitnessStore(p cli.Profile, label string) bookWitnessStore {
	return bookWitnessStore{path: filepath.Join(p.Connection.Database, "book", "witness", label+".json")}
}

// currentWitnessStore is the witness store of the window holding this
// clock, the one the core's verbs open and checkpoint.
func currentWitnessStore(p cli.Profile) (bookWitnessStore, error) {
	policy, err := evidencebook.ReadShelfWindow(context.Background(), shelfFile(p))
	if err != nil {
		return bookWitnessStore{}, err
	}
	if policy == nil {
		return bookWitnessStore{}, fmt.Errorf("%w: %s holds no window policy", evidencebook.ErrCorrupt, shelfFile(p))
	}
	return windowWitnessStore(p, policy.Period.WindowAt(bookNow()).Label), nil
}

// periodInWindow refuses, on a windowed store, a close or reconcile period
// outside the current window: its records are in another window's book,
// which a Close in this one could not link (a Close over an earlier window
// is a follow-up of evidencebook#689).
func (o openedBook) periodInWindow(start, end time.Time) error {
	if o.shelf == nil {
		return nil
	}
	if start.Before(o.window.Start) || end.After(o.window.End) {
		return cli.Hint(cli.ErrInput, fmt.Sprintf("this store is split by window, and the period [%s, %s) is not inside the current window %s [%s, %s): a Close covers the records of one window and is sealed in it; a Close over an earlier window's period is not supported yet", start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), o.window.Label, o.window.Start.Format(time.RFC3339), o.window.End.Format(time.RFC3339)))
	}
	return nil
}

// windowCoverage is what `report build` gives report.Build to check a
// report over windows with: the holder's checkpoint key and the witness
// directory. A bundle whose Result cites no window chain needs none and gets
// none. report.Build checks the coverage from the bundle itself and refuses
// a range with any gap, every gap named, so no page is built over a hole.
func windowCoverage(bundle []byte, holderKey, witnessDirectory string, diagnostic, permalink bool) (*report.Coverage, error) {
	verified, err := evidencebook.VerifyBundle(bundle)
	if err != nil {
		// report.Build refuses it with the page gate's own reason.
		return nil, nil
	}
	if !citesWindowChain(verified) {
		if holderKey != "" || witnessDirectory != "" {
			return nil, cli.InputError("--holder-checkpoint-key and --witness-directory apply to a report over a holder's windows, and this bundle's Result cites no window chain")
		}
		return nil, nil
	}
	switch {
	case diagnostic:
		return nil, cli.InputError("--diagnostic does not apply to a report over windows whose bundle verifies: check its coverage with `report coverage`")
	case permalink:
		return nil, cli.InputError("--permalink cannot be used with a report over windows: the permalink carries the bundle alone, and the viewer would state no range")
	case holderKey == "":
		return nil, cli.InputError("this is a report over a holder's windows: --holder-checkpoint-key is required, the holder's key obtained independently of the bundle")
	}
	coverage := &report.Coverage{HolderCheckpointKey: holderKey}
	if witnessDirectory != "" {
		rows, err := os.ReadFile(witnessDirectory)
		if err != nil {
			return nil, cli.InputError("--witness-directory: " + err.Error())
		}
		if coverage.Witnesses, err = evidencebook.ParseWitnessDirectory(rows); err != nil {
			return nil, cli.InputError("--witness-directory: " + err.Error())
		}
	}
	return coverage, nil
}

// checkPageInEarlierWindows looks for a page's seal in the windows before
// the newest, newest first, and returns the first check that finds one, or
// the newest window's own when none does.
func checkPageInEarlierWindows(ctx context.Context, opened openedBook, page []byte, newest report.Check) (report.Check, error) {
	windows, err := opened.shelf.Windows(ctx)
	if err != nil {
		return report.Check{}, err
	}
	for i := len(windows) - 2; i >= 0; i-- {
		book, _, err := opened.shelf.WindowBook(ctx, windows[i].Label)
		if err != nil {
			return report.Check{}, err
		}
		check, err := report.CheckPage(ctx, book, page)
		if err != nil {
			return report.Check{}, err
		}
		if check.Outcome != report.PageNoSeal {
			return check, nil
		}
	}
	return newest, nil
}

// recordedChain is the window_chain record this book already holds for the
// chain whose digest is digest, so a rerun of `report derive --chain` over
// the same chain reuses it rather than sealing another.
func recordedChain(ctx context.Context, book *evidencebook.Book, digest string) (evidencebook.Record, bool, error) {
	records, err := book.Query(ctx, evidencebook.Filter{RecordType: evidencebook.RecordTypeWindowChain})
	if err != nil {
		return evidencebook.Record{}, false, err
	}
	for _, r := range records {
		var st evidencebook.WindowChainStatement
		if json.Unmarshal(r.Header.Statement, &st) == nil && st.ChainDigest == digest {
			return r, true, nil
		}
	}
	return evidencebook.Record{}, false, nil
}

// requireInCurrentWindow refuses, on a windowed requester store, a record
// that is not in the current window's book: a Result is sealed in the
// current window and cites only records of that book.
func requireInCurrentWindow(ctx context.Context, opened openedBook, recordID string) error {
	if _, err := opened.book.Get(ctx, recordID); err == nil || opened.shelf == nil {
		return err
	}
	return cli.Hint(cli.ErrInput, fmt.Sprintf("record %s is not in this store's current window %s: on a windowed store, record the answers and derive the report in one window", recordID, opened.window.Label))
}

// chainDigest is the digest a window_chain record names its chain by: SHA-256
// over the chain's JCS, as Book.RecordWindowChain computes it.
func chainDigest(chain evidencebook.WindowChain) (string, error) {
	encoded, err := json.Marshal(chain)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	jcs, err := canonical.JCS(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(jcs)
	return hex.EncodeToString(sum[:]), nil
}

// failingWitnessStore is the witness store of a store whose windowed state
// could not be read: every call reports why.
type failingWitnessStore struct{ err error }

func (f failingWitnessStore) PendingWitnesses(context.Context, time.Time, int) ([]cll.WitnessState, error) {
	return nil, f.err
}
func (f failingWitnessStore) PendingWitnessesFor(context.Context, string, time.Time, int) ([]cll.WitnessState, error) {
	return nil, f.err
}
func (f failingWitnessStore) GetWitness(context.Context, string, uint64) (cll.WitnessState, error) {
	return cll.WitnessState{}, f.err
}
func (f failingWitnessStore) CommitWitness(context.Context, uint32, cll.WitnessState) error {
	return f.err
}

// answeredRecords are the evidence_request_answered records of a book, or of
// every window's book of a windowed store, oldest window first.
func answeredRecords(ctx context.Context, opened openedBook) ([]evidencebook.Record, error) {
	filter := evidencebook.Filter{RecordType: evidencebook.RecordTypeRequestAnswered}
	if opened.shelf == nil {
		return opened.book.Query(ctx, filter)
	}
	windows, err := opened.shelf.Windows(ctx)
	if err != nil {
		return nil, err
	}
	var out []evidencebook.Record
	for _, w := range windows {
		book, _, err := opened.shelf.WindowBook(ctx, w.Label)
		if err != nil {
			return nil, err
		}
		records, err := book.Query(ctx, filter)
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
	}
	return out, nil
}

// refuseRetiredLog refuses to create a windowed store for a profile whose
// pre-book cll.jsonl still holds entries: they move into a JSONL book with
// `book migrate`, which a windowed store does not take.
func refuseRetiredLog(p cli.Profile) error {
	if retired, err := scanRetiredLog(p); err == nil && (len(retired.entries) > 0 || retired.checkpoint != nil) {
		return cli.Hint(cli.ErrInput, "this profile's cll.jsonl holds entries not yet in a book: run `store init` and `book migrate --log-id <new-log-id>` (a JSONL book); a windowed store does not take them")
	}
	return nil
}

// citesWindowChain reports whether a verified bundle's Evidence Result cites
// a window_chain record: a report over a holder's windows.
func citesWindowChain(verified evidencebook.VerifiedBundle) bool {
	types := make(map[string]string, len(verified.Records))
	var result *evidencebook.Header
	for _, r := range verified.Records {
		if r.Header == nil || !r.HeaderVerified {
			continue
		}
		types[r.RecordID] = r.Header.RecordType
		if r.Header.RecordType == evidencebook.RecordTypeEvidenceResult {
			result = r.Header
		}
	}
	if result == nil {
		return false
	}
	for _, target := range result.LinksTo(evidencebook.Cites) {
		if types[target] == evidencebook.RecordTypeWindowChain {
			return true
		}
	}
	return false
}
