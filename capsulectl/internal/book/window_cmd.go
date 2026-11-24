package book

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/checkpointed-local-log/go/cll"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `capsulectl book window`: a book store split into one book per time window
// (evidencebook#689). The window is fixed when the store is created.

const windowHelp = "Windows are calendar periods in UTC (a month runs from the 1st 00:00 UTC to the next 1st). A window's grace is how long after it ends it still takes a Close over a period inside it, then closes and takes its final checkpoint (default 7 days for month, quarter and year, 1 day for week, none for day). A store's window and grace are fixed when it is created and can never change: records are never moved between windows."

type windowInitResult struct {
	Profile string                    `json:"profile"`
	Period  evidencebook.WindowPeriod `json:"period"`
	Since   string                    `json:"since,omitempty"`
	Grace   string                    `json:"grace,omitempty"`
	Notice  string                    `json:"notice"`
}

type windowListResult struct {
	Profile string                     `json:"profile"`
	Period  evidencebook.WindowPeriod  `json:"period"`
	Windows []evidencebook.WindowState `json:"windows"`
}

type windowChainResult struct {
	Profile string `json:"profile"`
	From    string `json:"from"`
	To      string `json:"to"`
	Windows int    `json:"windows"`
	Out     string `json:"out"`
}

type windowPublishResult struct {
	Window string         `json:"window"`
	LogID  string         `json:"log_id"`
	Kind   string         `json:"kind"`
	Size   uint64         `json:"checkpoint"`
	State  map[string]any `json:"witness"`
}

func windowCommands() *cobra.Command {
	group := &cobra.Command{Use: "window", Short: "A book store split into one book per time window: create it, list, roll and hand over its windows", Long: windowHelp}

	initCmd := &cobra.Command{Use: "init", Short: "Create this profile's book store with the window you choose (instead of `store init`, which splits a new store by month); the window is fixed for good", Long: windowHelp, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		if !isBook(p) {
			return cli.InputError("book window init needs a jsonl profile with log_id and operator set, and `log: book`")
		}
		raw, _ := c.Flags().GetString("period")
		period := evidencebook.WindowPeriod(raw)
		if raw != "none" {
			if _, err := period.ParseWindow(period.WindowAt(time.Unix(0, 0)).Label); err != nil {
				return cli.InputError("--period must be day, week, month, quarter, year or none")
			}
		}
		graceRaw, _ := c.Flags().GetString("grace")
		grace := defaultGrace(period)
		if graceRaw != "" {
			if raw == "none" {
				return cli.InputError("--grace applies to a store split by window, not --period none")
			}
			var err error
			if grace, err = parseGrace(graceRaw); err != nil {
				return err
			}
		}
		if err := refuseRetiredLog(p); err != nil {
			return err
		}
		for _, existing := range []string{shelfFile(p), filepath.Join(p.Connection.Database, "book", "log.jsonl")} {
			if _, err := os.Stat(existing); err == nil {
				return cli.Hint(cli.ErrInput, "this profile already has a book store ("+existing+"); a store's window is fixed when it is created")
			}
		}
		if raw == "none" {
			// One JSONL book for the whole history, as store init made
			// before new stores were windowed.
			if err := initJSONLBook(c.Context(), p); err != nil {
				return err
			}
			return cli.Output(c, windowInitResult{Profile: p.Name, Period: "none", Notice: "no window: one book for this store's whole history, fixed for this store"})
		}
		if err := initWindowedStore(c.Context(), p, period, grace); err != nil {
			if graceRaw != "" && errors.Is(err, evidencebook.ErrInvalid) {
				return cli.InputError("--grace " + graceRaw + ": a " + string(period) + " window's grace is shorter than the period")
			}
			return err
		}
		policy, err := evidencebook.ReadShelfWindow(c.Context(), shelfFile(p))
		if err != nil {
			return err
		}
		return cli.Output(c, windowInitResult{Profile: p.Name, Period: period, Since: policy.Since.Format(time.RFC3339), Grace: graceText(policy.Grace), Notice: windowNotice(period, policy.Grace)})
	}}
	initCmd.Flags().String("period", string(defaultWindow), "day, week (ISO, from Monday), month, quarter or year, in UTC; or none for one book over the store's whole history")
	initCmd.Flags().String("grace", "", "How long after a window ends it still takes a Close over a period inside it, then closes: whole days (7d) or a duration such as 36h (no fraction of a second), shorter than the period; default 7d for month, quarter and year, 1d for week, none for day")

	list := &cobra.Command{Use: "list", Short: "List the store's windows, oldest first: when each closes, which are in their grace, and each closed window's final tree (reads only)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		p, shelf, err := selectedShelfForReading(c)
		if err != nil {
			return err
		}
		defer shelf.Release()
		windows, err := shelf.Windows(c.Context())
		if err != nil {
			return err
		}
		return cli.Output(c, windowListResult{Profile: p.Name, Period: shelf.WindowPolicy().Period, Windows: windows})
	}}

	roll := &cobra.Command{Use: "roll", Short: "Close every window that has ended and open the current one, so last period's final checkpoint exists without a new record (signs)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, opened, err := selectedWindowedBook(c)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		windows, err := opened.shelf.Windows(c.Context())
		if err != nil {
			return err
		}
		if err = seedFinals(c.Context(), p, opened.shelf, windows); err != nil {
			return err
		}
		return cli.Output(c, windowListResult{Profile: p.Name, Period: opened.shelf.WindowPolicy().Period, Windows: windows})
	}}

	chain := &cobra.Command{Use: "chain", Short: "Write the window chain (window_chain/v1) of closed windows --from through --to: what a requester or auditor is handed for a period; without witness receipts it shows continuity only as the holder signed it (signs: the store rolls as it opens)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		from, _ := c.Flags().GetString("from")
		to, _ := c.Flags().GetString("to")
		out, _ := c.Flags().GetString("out")
		if from == "" || to == "" || out == "" {
			return cli.InputError("--from, --to and --out are required")
		}
		if _, e := os.Lstat(out); e == nil {
			return cli.Hint(cli.ErrInput, "--out "+out+" already exists; nothing was written; pass another path")
		}
		p, opened, err := selectedWindowedBook(c)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		chainOpts := evidencebook.WindowChainOptions{Receipts: receiptsOf(p)}
		if policy := opened.shelf.WindowPolicy(); policy.Predecessor != nil && from == policy.Period.WindowAt(policy.Since).Label {
			if chainOpts.Predecessor, err = predecessorEntry(c.Context(), p); err != nil {
				return err
			}
		}
		document, err := opened.shelf.WindowChain(c.Context(), from, to, chainOpts)
		if err != nil {
			return bookError(err)
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		if err = cli.AtomicFile(out, encoded, false); err != nil {
			return err
		}
		return cli.Output(c, windowChainResult{Profile: p.Name, From: from, To: to, Windows: len(document.Windows), Out: out})
	}}
	chain.Flags().String("from", "", "The first window's label (2026-01, 2026-Q1, 2026, 2026-W05, 2026-01-31)")
	chain.Flags().String("to", "", "The last window's label")
	chain.Flags().String("out", "", "Write the chain here (new file)")

	publish := &cobra.Command{Use: "publish", Short: "Deliver each closed window's final checkpoint, and each boundary checkpoint a window took as it entered its grace, to the profile's witness and print the receipts; a closed window's other checkpoints are superseded by its final one (signs; the core's `cll checkpoint publish` delivers the current window's checkpoints)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, opened, err := selectedWindowedBook(c)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		service, err := cli.ServiceID(p)
		if err != nil {
			return err
		}
		if service == "" {
			return cli.InputError("the profile names no witness: set checkpoint.endpoint and checkpoint.public_key")
		}
		windows, err := opened.shelf.Windows(c.Context())
		if err != nil {
			return err
		}
		if err = seedFinals(c.Context(), p, opened.shelf, windows); err != nil {
			return err
		}
		due, err := witnessedCheckpoints(c.Context(), opened.shelf, windows)
		if err != nil {
			return err
		}
		var results []windowPublishResult
		for _, d := range due {
			size, err := mmrSize(p, d.checkpoint)
			if err != nil {
				return err
			}
			state, err := cli.DeliverWitness(c.Context(), p, windowWitnessStore(p, d.window.Label), service, size)
			if err != nil {
				return err
			}
			results = append(results, windowPublishResult{Window: d.window.Label, LogID: d.window.LogID, Kind: d.kind, Size: size, State: witnessEntry(p, state)})
		}
		if policy := opened.shelf.WindowPolicy(); policy.Predecessor != nil {
			// The predecessor book's final checkpoint, like a window's
			// (evidencebook#808), from book/witness.json.
			result, err := publishPredecessor(c.Context(), p, policy, service)
			if err != nil {
				return err
			}
			results = append([]windowPublishResult{result}, results...)
		}
		return cli.Output(c, results)
	}}

	group.AddCommand(initCmd, list, roll, chain, publish, windowConvertCommand())
	return group
}

// selectedShelfForReading opens the selected profile's windowed store for
// reading.
func selectedShelfForReading(c *cobra.Command) (cli.Profile, *evidencebook.Shelf, error) {
	p, err := cli.Selected(c)
	if err != nil {
		return cli.Profile{}, nil, err
	}
	if err := requireWindowed(p); err != nil {
		return cli.Profile{}, nil, err
	}
	shelf, err := windowedShelfForReading(c.Context(), p)
	return p, shelf, err
}

// selectedWindowedBook opens the selected profile's windowed store with its
// signing keys: the shelf rolls as it opens.
func selectedWindowedBook(c *cobra.Command) (cli.Profile, openedBook, error) {
	p, err := cli.Selected(c)
	if err != nil {
		return cli.Profile{}, openedBook{}, err
	}
	if err := requireWindowed(p); err != nil {
		return cli.Profile{}, openedBook{}, err
	}
	opened, err := openBook(c.Context(), p, false)
	return p, opened, err
}

func requireWindowed(p cli.Profile) error {
	windowed, err := isWindowedStore(p)
	if err != nil {
		return err
	}
	if !windowed {
		return cli.Hint(cli.ErrInput, "this profile's book store is not split by window: `book window init` creates one, for a profile with no store yet")
	}
	return nil
}

// witnessed is a checkpoint of a window that the profile's witness takes:
// a closed window's final checkpoint and, for a window that had a grace, the
// boundary checkpoint it took when it ended (evidencebook#807), which a
// window chain carries with its own receipts.
type witnessed struct {
	window     evidencebook.WindowState
	kind       string // "boundary" or "final"
	checkpoint evidencebook.Checkpoint
}

// witnessedCheckpoints lists the boundary and final checkpoints of the
// store's windows, oldest first. A window's boundary is the tree the next
// window's open record names, when that is not its final tree.
func witnessedCheckpoints(ctx context.Context, shelf *evidencebook.Shelf, windows []evidencebook.WindowState) ([]witnessed, error) {
	var out []witnessed
	for i, w := range windows {
		book, _, err := shelf.WindowBook(ctx, w.Label)
		if err != nil {
			return nil, err
		}
		if i+1 < len(windows) {
			previous, err := previousTree(ctx, shelf, windows[i+1].Label)
			if err != nil {
				return nil, err
			}
			if previous != nil && (w.Final == nil || *previous != *w.Final) {
				checkpoints, err := book.Checkpoints(ctx)
				if err != nil {
					return nil, err
				}
				found := false
				for _, cp := range checkpoints {
					if cp.Entries == previous.Entries && cp.Root == previous.Root {
						out, found = append(out, witnessed{window: w, kind: "boundary", checkpoint: cp}), true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("%w: window %s holds no checkpoint at the boundary window %s names", evidencebook.ErrCorrupt, w.Label, windows[i+1].Label)
				}
			}
		}
		if w.Closed {
			final, err := book.Checkpoint(ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, witnessed{window: w, kind: "final", checkpoint: final})
		}
	}
	return out, nil
}

// previousTree is the tree a window's open record names.
func previousTree(ctx context.Context, shelf *evidencebook.Shelf, label string) (*evidencebook.WindowTree, error) {
	book, _, err := shelf.WindowBook(ctx, label)
	if err != nil {
		return nil, err
	}
	opens, err := book.Query(ctx, evidencebook.Filter{RecordType: evidencebook.RecordTypeWindowOpen})
	if err != nil {
		return nil, err
	}
	if len(opens) == 0 {
		return nil, fmt.Errorf("%w: window %s holds no open record", evidencebook.ErrCorrupt, label)
	}
	var open evidencebook.WindowOpenStatement
	if err := json.Unmarshal(opens[0].Header.Statement, &open); err != nil {
		return nil, fmt.Errorf("%w: window %s's open record: %v", evidencebook.ErrCorrupt, label, err)
	}
	return open.Previous, nil
}

// mmrSize is a window checkpoint's MMR size, the key its witness delivery
// is kept under.
func mmrSize(p cli.Profile, cp evidencebook.Checkpoint) (uint64, error) {
	record, err := cli.VerifyCheckpoint(windowProfile(p, cp.LogID), cp.Statement)
	if err != nil {
		return 0, err
	}
	return record.MMRSize, nil
}

// seedFinals records each window's boundary and final checkpoints as due
// for the profile's witness, once: where the agreement names a witness, a
// window's final checkpoint is the one it witnesses (Sherlock2, #689), and
// its boundary is witnessed too (evidencebook#807).
func seedFinals(ctx context.Context, p cli.Profile, shelf *evidencebook.Shelf, windows []evidencebook.WindowState) error {
	service, err := cli.ServiceID(p)
	if err != nil || service == "" {
		return err
	}
	due, err := witnessedCheckpoints(ctx, shelf, windows)
	if err != nil {
		return err
	}
	for _, d := range due {
		size, err := mmrSize(p, d.checkpoint)
		if err != nil {
			return err
		}
		if err = windowWitnessStore(p, d.window.Label).seed(service, size, d.checkpoint.Statement, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

// receiptsOf reads the receipts the profile's witness returned for a
// window's final checkpoint, in the bundle form a window chain carries.
func receiptsOf(p cli.Profile) func(context.Context, evidencebook.Checkpoint) ([]json.RawMessage, error) {
	return func(_ context.Context, final evidencebook.Checkpoint) ([]json.RawMessage, error) {
		store := newBookWitnessStore(p) // the predecessor book's (evidencebook#808)
		if label, ok := strings.CutPrefix(final.LogID, p.LogID+"/"); ok {
			store = windowWitnessStore(p, label)
		} else if final.LogID != p.LogID {
			return nil, nil
		}
		states, err := store.load()
		if err != nil {
			return nil, err
		}
		var out []json.RawMessage
		for _, state := range states {
			entry := witnessEntry(p, state)
			if string(state.Checkpoint) != string(final.Statement) || entry["receipt"] == nil {
				continue
			}
			raw, err := json.Marshal(entry["receipt"])
			if err != nil {
				return nil, err
			}
			out = append(out, raw)
		}
		return out, nil
	}
}

// witnessEntry is a delivery state as the core prints it, its receipt (once
// verified) in the checkpoint.witnesses form a bundle and a chain carry.
func witnessEntry(p cli.Profile, state cll.WitnessState) map[string]any {
	status := "pending"
	if state.Permanent {
		status = "failed"
	}
	out := map[string]any{"state": status, "attempts": state.Attempts}
	if r := state.Receipt; r != nil && r.LeafIndex != nil && r.TreeSize != nil {
		out["state"] = "verified"
		out["receipt"] = map[string]any{
			"ts_url": strings.TrimRight(p.Checkpoint.Endpoint, "/"), "entry_hash": r.EntryHash,
			"receipt_b64": base64.StdEncoding.EncodeToString(r.Bytes),
			"leaf_index":  *r.LeafIndex, "tree_size": *r.TreeSize,
		}
	}
	return out
}

// windowProfile is p as the core's checkpoint checks read a window log's
// checkpoint: the same keys, the window's log id.
func windowProfile(p cli.Profile, logID string) cli.Profile {
	p.LogID = logID
	return p
}
