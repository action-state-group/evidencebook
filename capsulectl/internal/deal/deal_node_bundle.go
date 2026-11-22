package deal

import (
	"encoding/json"
	"errors"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// `deal node checkpoint` and `deal node bundle` are a node book's newest
// checkpoint and the Evidence Bundle that proves one of its records under a
// checkpoint (evidencebook#624; the node's book_checkpoint and book_bundle).
// The cryptography and the verification are Go's. The witness is the host's
// transport: the host asks `checkpoint` for the statement, submits it to its
// witness, and gives the receipt back to `bundle`, which carries it only when
// it verifies under the witness directory the host names.

// cadenceExtension is the bundle extension that carries the witnessed cadence
// chain for a checkpoint.
const cadenceExtension = "x-cadence-witness/v0"

func dealNodeCheckpointCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "A node book's newest checkpoint, cut now if the book grew (signs a checkpoint)",
		Long: `Print a node book's newest checkpoint: its id (the SHA-256 of its signed statement), the number of records it
covers, its root and the key that signed it. It is cut now when the book has grown since the last one. --out writes the
signed statement (COSE) to a new file, for a host to submit to its witness; deal node bundle --at names the checkpoint by
its id.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			book, _ := c.Flags().GetString("book")
			if err = nodeBookID(p, book); err != nil {
				return err
			}
			out, _ := c.Flags().GetString("out")
			var dest *newFile
			if out != "" {
				if dest, err = claimNewFile(out); err != nil {
					return err
				}
				defer dest.release()
			}
			ctx := c.Context()
			s, err := openDealSession(ctx, p)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, s.close()) }()
			b, err := s.shelf.Open(ctx, book)
			if err != nil {
				return err
			}
			cp, err := b.Checkpoint(ctx)
			if err != nil {
				return err
			}
			result := map[string]any{"book": book, "size": b.Size(), "id": cp.ID, "entries": cp.Entries, "root": cp.Root, "key_id": cp.KeyID}
			if dest != nil {
				if err := dest.commit(cp.Statement); err != nil {
					return err
				}
				result["statement_file"] = out
			}
			return cli.Output(c, result)
		},
	}
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Flags().String("out", "", "Write the checkpoint's signed statement (COSE) to this new file")
	return cmd
}

func dealNodeBundleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "An Evidence Bundle proving one record of a node book under a checkpoint, verified before it is written (records what it disclosed)",
		Long: `Write an Evidence Bundle (evidence-bundle/v2) that proves --record is in the node book --book under a checkpoint
(--at names one by its id; by default the book's newest, cut now if the book grew), and verify it before writing it to a
new file --out. It carries the record, its inclusion proof and the interval from the book's first record to that checkpoint.

By default the bundle carries the record and what it commits, and withholds the headers and payloads of the records it
discloses, each by its digest. With --disclose it reveals the header of the record and of the records it cites, and their
payloads. Disclosing is a record of the book: the book commits a disclosure record naming what the bundle disclosed, so the
book's own history says it was shown.

--witness-receipt FILE (a checkpoint.witnesses entry: ts_url, entry_hash, receipt_b64, leaf_index, tree_size) with
--witness-directory FILE (witnesses.json) and --at carries the witness's receipt for that checkpoint; the receipt must
verify under the directory, or no bundle is written. When a witnessed cadence tick carries the checkpoint, the cadence
chain travels in the bundle's x-cadence-witness/v0 extension. What a bundle shows is that the record is in the book at that
position; it does not show the record's content is true.`,
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			p, err := cli.Selected(c)
			if err != nil {
				return err
			}
			book, _ := c.Flags().GetString("book")
			if err = nodeBookID(p, book); err != nil {
				return err
			}
			record, _ := c.Flags().GetString("record")
			out, _ := c.Flags().GetString("out")
			if record == "" || out == "" {
				return cli.InputError("--record and --out are required")
			}
			at, _ := c.Flags().GetString("at")
			disclose, _ := c.Flags().GetBool("disclose")
			receiptPath, _ := c.Flags().GetString("witness-receipt")
			directoryPath, _ := c.Flags().GetString("witness-directory")
			if (receiptPath != "") != (directoryPath != "") {
				return cli.InputError("--witness-receipt and --witness-directory go together")
			}
			if receiptPath != "" && at == "" {
				return cli.InputError("--witness-receipt needs --at: the receipt was made for one checkpoint")
			}
			req := evidencebook.BundleRequest{Root: record, Payloads: evidencebook.PayloadsSelected}
			if !disclose {
				req.WithholdPayloads = true
				req.Suppress = []string{evidencebook.HeaderMember}
			}
			if receiptPath != "" {
				raw, e := cli.ReadInput(receiptPath)
				if e != nil {
					return e
				}
				if !json.Valid(raw) {
					return cli.InputError("--witness-receipt is not JSON")
				}
				req.Witnesses = []json.RawMessage{raw}
				dir, e := cli.ReadInput(directoryPath)
				if e != nil {
					return e
				}
				if req.WitnessDirectory, e = evidencebook.ParseWitnessDirectory(dir); e != nil {
					return cli.InputError("--witness-directory: " + e.Error())
				}
			}
			dest, err := claimNewFile(out)
			if err != nil {
				return err
			}
			defer dest.release()
			ctx := c.Context()
			s, err := openDealSession(ctx, p)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, s.close()) }()
			b, err := s.shelf.Open(ctx, book)
			if err != nil {
				return err
			}
			if at != "" {
				checkpoints, e := b.Checkpoints(ctx)
				if e != nil {
					return e
				}
				for i := range checkpoints {
					if checkpoints[i].ID == at {
						req.At = &checkpoints[i]
					}
				}
				if req.At == nil {
					return cli.InputError("--at " + at + " is not a checkpoint of this book: `deal node checkpoint` prints them")
				}
			} else {
				cp, e := b.Checkpoint(ctx)
				if e != nil {
					return e
				}
				req.At = &cp
			}
			// The witnessed cadence chain for the checkpoint, when a witnessed tick carries it.
			extensions := map[string]json.RawMessage{}
			cadence, _, e := s.cadenceOf(ctx)
			if e != nil {
				return e
			}
			var cadenceExtent any
			if chain, e := cadence.Chain(ctx, req.At.Statement); e == nil {
				raw, e := json.Marshal(chain)
				if e != nil {
					return e
				}
				extensions[cadenceExtension] = raw
				cadenceExtent = chain["extent"]
			} else if !errors.Is(e, evidencebook.ErrNotFound) {
				return e
			}
			if len(extensions) > 0 {
				req.Extensions = extensions
			}
			bundle, err := b.Bundle(ctx, req)
			if err != nil {
				return nodeBookError(err)
			}
			if _, err = evidencebook.VerifyBundle(bundle.JSON); err != nil {
				return nodeBookError(err)
			}
			if err = dest.commit(bundle.JSON); err != nil {
				return errors.Join(errors.New("the bundle was built and verified, and the book recorded what it disclosed, but the file was not saved"), err)
			}
			result := map[string]any{"book": book, "record": record, "file": out, "checkpoint": req.At.ID, "entries": req.At.Entries,
				"witnessed": receiptPath != "", "disclosed": disclose}
			if cadenceExtent != nil {
				result["cadence_extent"] = cadenceExtent
			}
			if bundle.Disclosure.RecordID != "" {
				result["disclosure_record"] = bundle.Disclosure.RecordID
			}
			return cli.Output(c, result)
		},
	}
	cmd.Flags().String("book", "", "The node's book on the shelf (required)")
	cmd.Flags().String("record", "", "The record the bundle proves (required)")
	cmd.Flags().String("out", "", "Where to write the bundle: a new file (required)")
	cmd.Flags().String("at", "", "The id of the checkpoint to prove it under (default: the newest, cut now if the book grew)")
	cmd.Flags().Bool("disclose", false, "Reveal the header of the record and of the records it cites, and their payloads")
	cmd.Flags().String("witness-receipt", "", "A witness receipt for the --at checkpoint (a checkpoint.witnesses entry as JSON)")
	cmd.Flags().String("witness-directory", "", "The witness directory (witnesses.json) the receipt is verified under")
	return cmd
}
