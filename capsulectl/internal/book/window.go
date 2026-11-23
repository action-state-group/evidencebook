package book

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
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

// defaultWindow is the window `book window init` fixes when the holder
// names none (Sherlock2, #689: month).
const defaultWindow = evidencebook.WindowMonth

// windowNotice is what store creation prints: the window cannot change once
// the store exists.
func windowNotice(period evidencebook.WindowPeriod) string {
	return fmt.Sprintf("window: %s (calendar %ss, UTC), fixed for this store", period, period)
}

// sealedStore reads a window book's records as sealed, the recordStore a
// JSONL book's FileStore is.
type sealedStore struct{ book *evidencebook.Book }

func (s sealedStore) GetRecord(ctx context.Context, recordID string) (evidencebook.StoredRecord, error) {
	return s.book.Sealed(ctx, recordID)
}

// windowedConfig is the shelf configuration of a profile's windowed store.
func windowedConfig(p cli.Profile, signer evidencebook.Signer, checkpointKey ed25519.PrivateKey, artifacts cli.ArtifactStore, now func() time.Time, policy *evidencebook.WindowPolicy) evidencebook.ShelfConfig {
	return evidencebook.ShelfConfig{
		Path: shelfFile(p), Operator: p.Operator, Signer: signer, CheckpointKey: checkpointKey,
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
	if _, reproduced, err := reproductionAt(); err != nil {
		return openedBook{}, err
	} else if reproduced {
		return openedBook{}, cli.InputError(SourceDateEpochEnv + " is set, but this is a live windowed store: only a JSONL book is reproduced")
	}
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
	windowed := false
	for _, r := range verified.Records {
		if r.Header != nil && r.Header.RecordType == evidencebook.RecordTypeWindowChain {
			windowed = true
		}
	}
	if !windowed {
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
