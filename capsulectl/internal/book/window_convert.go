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
	"github.com/spf13/cobra"
)

// `capsulectl book window convert` (evidencebook#808): a profile's JSONL
// book continues in a windowed store. The book is closed for good by a
// book_close record and its final checkpoint, stays at its path with its
// records unmoved, and the store's first window starts at the first whole
// second after the close and names the book's final tree. A chain from that
// window carries the book as its predecessor.

type windowConvertResult struct {
	Profile     string                    `json:"profile"`
	Period      evidencebook.WindowPeriod `json:"period"`
	Since       string                    `json:"since"`
	Grace       string                    `json:"grace,omitempty"`
	Predecessor *evidencebook.WindowTree  `json:"predecessor"`
	Witnessed   bool                      `json:"witnessed"`
	Already     bool                      `json:"already_converted,omitempty"`
	Notice      string                    `json:"notice"`
}

func windowConvertCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "convert", Short: "Continue this profile's JSONL book in a store split by window: the book is closed for good and stays at its path, its records never move, and the first window starts now (one-way; signs; needs --yes)", Long: windowHelp, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		if !isBook(p) {
			return cli.InputError("book window convert needs a jsonl profile with log_id and operator set, and `log: book`")
		}
		raw, _ := c.Flags().GetString("period")
		period := evidencebook.WindowPeriod(raw)
		if _, err := period.ParseWindow(period.WindowAt(time.Unix(0, 0)).Label); err != nil {
			return cli.InputError("--period must be day, week, month, quarter or year")
		}
		grace := defaultGrace(period)
		if graceRaw, _ := c.Flags().GetString("grace"); graceRaw != "" {
			if grace, err = parseGrace(graceRaw); err != nil {
				return err
			}
		}
		windowed, err := isWindowedStore(p)
		if err != nil {
			return err
		}
		if windowed {
			policy, err := evidencebook.ReadShelfWindow(c.Context(), shelfFile(p))
			if err != nil {
				return err
			}
			if policy == nil || policy.Predecessor == nil {
				return cli.Hint(cli.ErrInput, "this profile's store is already split by window, created empty: there is no JSONL book to convert")
			}
			if (c.Flags().Changed("period") && period != policy.Period) || (c.Flags().Changed("grace") && grace != policy.Grace) {
				return cli.Hint(cli.ErrInput, "this profile is already converted, to "+windowNotice(policy.Period, policy.Grace)+": the window cannot change")
			}
			service, err := cli.ServiceID(p)
			if err != nil {
				return err
			}
			return cli.Output(c, windowConvertResult{Profile: p.Name, Period: policy.Period, Since: policy.Since.Format(time.RFC3339), Grace: graceText(policy.Grace), Predecessor: policy.Predecessor, Witnessed: service != "", Already: true, Notice: "already converted: " + convertNotice(p, policy, service != "")})
		}
		if _, reproduced, _ := reproductionAt(); reproduced {
			return cli.InputError(SourceDateEpochEnv + " is set: a reproduced scratch book is not converted")
		}
		if err := refuseRetiredLog(p); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(p.Connection.Database, "book", "log.jsonl")); err != nil {
			return noBookError(p)
		}
		if yes, _ := c.Flags().GetBool("yes"); !yes {
			return cli.Hint(cli.ErrInput, "converting is one-way: the JSONL book is closed for good and continues in a store split by "+string(period)+" windows; its records never move and it stays at book/log.jsonl. Seal any Close over a period before now in the book first: after converting, such a Close is refused, and a period that spans the conversion closes in two parts. Run again with --yes to convert")
		}
		policy, witnessed, err := convertBook(c.Context(), p, period, grace)
		if err != nil {
			return err
		}
		// The store starts at the first whole second after the close: wait
		// for it, so the next verb's clock is not behind the first window.
		// The wall clock is the store's (bookNow) except in tests, which set
		// a clock in the past and never wait.
		if d := time.Until(policy.Since); d > 0 && d <= 2*time.Second {
			time.Sleep(d)
		}
		notice := convertNotice(p, policy, witnessed)
		fmt.Fprintln(noticeOut, "capsulectl: "+notice)
		return cli.Output(c, windowConvertResult{Profile: p.Name, Period: policy.Period, Since: policy.Since.Format(time.RFC3339), Grace: graceText(policy.Grace), Predecessor: policy.Predecessor, Witnessed: witnessed, Notice: notice})
	}}
	cmd.Flags().String("period", string(defaultWindow), "day, week (ISO, from Monday), month, quarter or year, in UTC")
	cmd.Flags().String("grace", "", "How long after a window ends it still takes a Close over a period inside it: whole days (7d) or a duration such as 36h (no fraction of a second), shorter than the period; default 7d for month, quarter and year, 1d for week, none for day")
	cmd.Flags().Bool("yes", false, "Convert: the book is closed for good")
	return cmd
}

// convertBook closes the profile's JSONL book into a windowed store and
// creates the store, resuming a conversion a crash interrupted (the book
// closed, the store not yet in place). witnessed reports that the book's
// final checkpoint is queued for the profile's witness.
func convertBook(ctx context.Context, p cli.Profile, period evidencebook.WindowPeriod, grace time.Duration) (*evidencebook.WindowPolicy, bool, error) {
	opened, err := openBookKeyedAt(ctx, p, false, nil, false, true)
	if err != nil {
		return nil, false, err
	}
	policy, err := opened.book.CloseForWindows(ctx, p.LogID, period, grace)
	if err != nil {
		return nil, false, errors.Join(bookError(err), opened.release())
	}
	witnessed, err := seedPredecessor(ctx, p, opened.book, policy)
	if err == nil {
		// Under the book's lock, so a second convert cannot race the store's
		// creation.
		err = createShelf(ctx, p, policy)
	}
	if err = errors.Join(err, opened.release()); err != nil {
		return nil, false, err
	}
	return policy, witnessed, nil
}

// createShelf creates the windowed store at a temporary path and moves it
// into place once its transaction committed, so no half-made store is
// ever taken for a converted one.
func createShelf(ctx context.Context, p cli.Profile, policy *evidencebook.WindowPolicy) error {
	if err := os.MkdirAll(filepath.Dir(shelfFile(p)), 0o700); err != nil {
		return err
	}
	tmp := shelfFile(p) + ".converting"
	for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
		if err := os.Remove(tmp + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	shelf, err := windowedShelfAt(ctx, p, policy, tmp)
	if err != nil {
		return err
	}
	if err := shelf.Release(); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(tmp + suffix); err == nil {
			return fmt.Errorf("%w: the new store left %s behind", evidencebook.ErrCorrupt, tmp+suffix)
		}
	}
	if err := os.Rename(tmp, shelfFile(p)); err != nil {
		return err
	}
	// The lock file of the temporary path; a crash before this leaves it,
	// and `book window init` or the next convert removes it.
	if err := os.Remove(tmp + ".lock"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// seedPredecessor queues the closed book's final checkpoint for the
// profile's witness in book/witness.json, like a window's final; with no
// witness named it reports false: the predecessor is unwitnessed.
func seedPredecessor(ctx context.Context, p cli.Profile, book *evidencebook.Book, policy *evidencebook.WindowPolicy) (bool, error) {
	service, err := cli.ServiceID(p)
	if err != nil || service == "" {
		return false, err
	}
	final, err := predecessorFinal(ctx, book, policy.Predecessor)
	if err != nil {
		return false, err
	}
	size, err := mmrSize(p, final)
	if err != nil {
		return false, err
	}
	return true, newBookWitnessStore(p).seed(service, size, final.Statement, time.Now())
}

// predecessorFinal is the closed book's checkpoint of the tree the store
// names.
func predecessorFinal(ctx context.Context, book *evidencebook.Book, tree *evidencebook.WindowTree) (evidencebook.Checkpoint, error) {
	checkpoints, err := book.Checkpoints(ctx)
	if err != nil {
		return evidencebook.Checkpoint{}, err
	}
	// The first checkpoint of the tree, the one the root's chain carries.
	for _, cp := range checkpoints {
		if cp.Entries == tree.Entries && cp.Root == tree.Root {
			return cp, nil
		}
	}
	return evidencebook.Checkpoint{}, fmt.Errorf("%w: the predecessor book holds no checkpoint of the tree the store names", evidencebook.ErrCorrupt)
}

// predecessorEntry builds the chain's predecessor entry from the profile's
// closed JSONL book, read with throwaway keys (it writes nothing).
func predecessorEntry(ctx context.Context, p cli.Profile) (*evidencebook.ChainPredecessor, error) {
	opened, err := openBookKeyedAt(ctx, p, false, nil, true, true)
	if err != nil {
		return nil, err
	}
	entry, err := evidencebook.ChainPredecessorFromBook(ctx, opened.book, receiptsOf(p))
	return entry, errors.Join(err, opened.release())
}

func convertNotice(p cli.Profile, policy *evidencebook.WindowPolicy, witnessed bool) string {
	first := policy.Period.WindowAt(policy.Since)
	witness := "unwitnessed: the profile names no witness, so the predecessor's final checkpoint carries no receipt"
	if witnessed {
		witness = "the predecessor's final checkpoint is queued for the witness (book window publish delivers it)"
	}
	return fmt.Sprintf("converted: the JSONL book %s is closed at %d records and stays at book/log.jsonl; its records never move. %s; the first window %s starts at %s (partial). The same signing key continues; this is not a clear. No verb serves the old book's records now (a chain's predecessor entry and bundles made before carry them), requests open in it cannot be answered, a Close over a period before the conversion is refused, and capsulectl builds before window books must not write to this profile. %s",
		policy.Predecessor.LogID, policy.Predecessor.Entries, windowNotice(policy.Period, policy.Grace), first.Label, policy.Since.Format(time.RFC3339), witness)
}

// windowedShelfAt creates a windowed store with policy at path, with
// throwaway keys: creating it writes no record.
func windowedShelfAt(ctx context.Context, p cli.Profile, policy *evidencebook.WindowPolicy, path string) (*evidencebook.Shelf, error) {
	_, scratchKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(scratchKey)
	if err != nil {
		return nil, err
	}
	cfg := windowedConfig(p, signer, scratchKey, noOriginals{profile: p.Name}, bookNow, policy)
	cfg.Path = path
	shelf, err := evidencebook.OpenSQLiteShelf(ctx, cfg)
	return shelf, bookError(err)
}

// publishPredecessor delivers the predecessor book's final checkpoint to
// the profile's witness.
func publishPredecessor(ctx context.Context, p cli.Profile, policy *evidencebook.WindowPolicy, service string) (windowPublishResult, error) {
	opened, err := openBookKeyedAt(ctx, p, false, nil, true, true)
	if err != nil {
		return windowPublishResult{}, err
	}
	final, err := predecessorFinal(ctx, opened.book, policy.Predecessor)
	if err = errors.Join(err, opened.release()); err != nil {
		return windowPublishResult{}, err
	}
	size, err := mmrSize(p, final)
	if err != nil {
		return windowPublishResult{}, err
	}
	store := newBookWitnessStore(p)
	if err := store.seed(service, size, final.Statement, time.Now()); err != nil {
		return windowPublishResult{}, err
	}
	state, err := cli.DeliverWitness(ctx, p, store, service, size)
	if err != nil {
		return windowPublishResult{}, err
	}
	return windowPublishResult{LogID: policy.Predecessor.LogID, Kind: "predecessor", Size: size, State: witnessEntry(p, state)}, nil
}

// requeuePredecessor queues the predecessor book's final checkpoint for the
// witness again, as `book window roll` does each window's final: once per
// witness and size, so a lost book/witness.json is restored.
func requeuePredecessor(ctx context.Context, p cli.Profile, policy *evidencebook.WindowPolicy) error {
	if policy == nil || policy.Predecessor == nil {
		return nil
	}
	service, err := cli.ServiceID(p)
	if err != nil || service == "" {
		return err
	}
	opened, err := openBookKeyedAt(ctx, p, false, nil, true, true)
	if err != nil {
		return err
	}
	_, err = seedPredecessor(ctx, p, opened.book, policy)
	return errors.Join(err, opened.release())
}
