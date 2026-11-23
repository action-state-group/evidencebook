package book

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

const windowHelp = "Windows are calendar periods in UTC (a month runs from the 1st 00:00 UTC to the next 1st). A store's window is fixed when it is created and can never change: records are never moved between windows."

type windowInitResult struct {
	Profile string                    `json:"profile"`
	Period  evidencebook.WindowPeriod `json:"period"`
	Since   string                    `json:"since"`
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
	Size   uint64         `json:"checkpoint"`
	State  map[string]any `json:"witness"`
}

func windowCommands() *cobra.Command {
	group := &cobra.Command{Use: "window", Short: "A book store split into one book per time window: create it, list, roll and hand over its windows", Long: windowHelp}

	initCmd := &cobra.Command{Use: "init", Short: "Create this profile's book store split by a time window (instead of `store init`); the window is fixed for good", Long: windowHelp, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		if !isBook(p) {
			return cli.InputError("book window init needs a jsonl profile with log_id and operator set, and `log: book`")
		}
		raw, _ := c.Flags().GetString("period")
		period := evidencebook.WindowPeriod(raw)
		if _, err := period.ParseWindow(period.WindowAt(time.Unix(0, 0)).Label); err != nil {
			return cli.InputError("--period must be day, week, month, quarter or year")
		}
		if err := refuseRetiredLog(p); err != nil {
			return err
		}
		for _, existing := range []string{shelfFile(p), filepath.Join(p.Connection.Database, "book", "log.jsonl")} {
			if _, err := os.Stat(existing); err == nil {
				return cli.Hint(cli.ErrInput, "this profile already has a book store ("+existing+"); a store's window is fixed when it is created")
			}
		}
		if err := initWindowedStore(c.Context(), p, period); err != nil {
			return err
		}
		policy, err := evidencebook.ReadShelfWindow(c.Context(), shelfFile(p))
		if err != nil {
			return err
		}
		return cli.Output(c, windowInitResult{Profile: p.Name, Period: period, Since: policy.Since.Format(time.RFC3339), Notice: windowNotice(period)})
	}}
	initCmd.Flags().String("period", string(defaultWindow), "day, week (ISO, from Monday), month, quarter or year, in UTC")

	list := &cobra.Command{Use: "list", Short: "List the store's windows, oldest first, with each closed window's final tree (reads only)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
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
		document, err := opened.shelf.WindowChain(c.Context(), from, to, evidencebook.WindowChainOptions{Receipts: receiptsOf(p)})
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

	publish := &cobra.Command{Use: "publish", Short: "Deliver each closed window's final checkpoint to the profile's witness and print the receipts; a closed window's earlier checkpoints are superseded by its final one (signs; the core's `cll checkpoint publish` delivers only the profile's own log)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
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
		var results []windowPublishResult
		for _, w := range windows {
			if !w.Closed {
				continue
			}
			size, err := finalSize(c.Context(), p, opened.shelf, w)
			if err != nil {
				return err
			}
			state, err := cli.DeliverWitness(c.Context(), p, windowWitnessStore(p, w.Label), service, size)
			if err != nil {
				return err
			}
			results = append(results, windowPublishResult{Window: w.Label, LogID: w.LogID, Size: size, State: witnessEntry(p, state)})
		}
		return cli.Output(c, results)
	}}

	group.AddCommand(initCmd, list, roll, chain, publish)
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

// finalSize is a closed window's final checkpoint's MMR size, the key its
// witness delivery is kept under.
func finalSize(ctx context.Context, p cli.Profile, shelf *evidencebook.Shelf, w evidencebook.WindowState) (uint64, error) {
	book, _, err := shelf.WindowBook(ctx, w.Label)
	if err != nil {
		return 0, err
	}
	final, err := book.Checkpoint(ctx)
	if err != nil {
		return 0, err
	}
	record, err := cli.VerifyCheckpoint(windowProfile(p, final.LogID), final.Statement)
	if err != nil {
		return 0, err
	}
	return record.MMRSize, nil
}

// seedFinals records each closed window's final checkpoint as due for the
// profile's witness, once: where the agreement names a witness, a window's
// final checkpoint is the one it witnesses (Sherlock2, #689).
func seedFinals(ctx context.Context, p cli.Profile, shelf *evidencebook.Shelf, windows []evidencebook.WindowState) error {
	service, err := cli.ServiceID(p)
	if err != nil || service == "" {
		return err
	}
	for _, w := range windows {
		if !w.Closed {
			continue
		}
		book, _, err := shelf.WindowBook(ctx, w.Label)
		if err != nil {
			return err
		}
		final, err := book.Checkpoint(ctx)
		if err != nil {
			return err
		}
		record, err := cli.VerifyCheckpoint(windowProfile(p, final.LogID), final.Statement)
		if err != nil {
			return err
		}
		if err = windowWitnessStore(p, w.Label).seed(service, record.MMRSize, final.Statement, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

// receiptsOf reads the receipts the profile's witness returned for a
// window's final checkpoint, in the bundle form a window chain carries.
func receiptsOf(p cli.Profile) func(context.Context, evidencebook.Checkpoint) ([]json.RawMessage, error) {
	return func(_ context.Context, final evidencebook.Checkpoint) ([]json.RawMessage, error) {
		label, ok := strings.CutPrefix(final.LogID, p.LogID+"/")
		if !ok {
			return nil, nil
		}
		states, err := windowWitnessStore(p, label).load()
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
