package book

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// derivedInputsOutput is what `result derived-inputs` prints: the contract with
// an evaluator. A holder's answer to a request that named a Terms Pack by the
// digest of its definition, as this book recorded and verified it.
type derivedInputsOutput struct {
	// Response is the response record the Result must cite as the evidence of
	// every claim.
	Response string `json:"response"`
	// Derivation is the digest of the pack the holder answered.
	Derivation string `json:"derivation"`
	// Anchor is the digest of the checkpoint the holder answered at.
	Anchor string `json:"anchor"`
	// Proven says the rows are records of the holder's log at that checkpoint
	// (each digest's inclusion proof was checked). The field values are the
	// holder's word either way.
	Proven  bool                  `json:"proven"`
	Records []derivedRecordOutput `json:"records"`
}

type derivedRecordOutput struct {
	Digest    string         `json:"digest"`
	LeafIndex uint64         `json:"leaf_index"`
	Fields    map[string]any `json:"fields"`
}

func resultDerivedInputsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "derived-inputs",
		Short: "Print what a holder answered to a Terms Pack requested by digest, as this book recorded it, for the evaluator that runs the pack (seals nothing)",
		Long: "Prints the holder's answer to a request that named a Terms Pack by the digest of its definition, as `book request --response` recorded it: the pack digest, the anchor, `proven`, and each row's digest, leaf and declared fields. " +
			"`proven` means each row's digest was checked in the holder's log at a signed checkpoint; the field values are the holder's word, and no proof shows that no row was left out. " +
			"The response id is what every claim of the Result must cite as its evidence.",
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			response, _ := c.Flags().GetString("response")
			if response == "" {
				return cli.InputError("--response is required: the response record `book request --response` printed as record_id")
			}
			p, e := cli.Selected(c)
			if e != nil {
				return e
			}
			opened, e := openBook(c.Context(), p, false)
			if e != nil {
				return e
			}
			defer func() { err = errors.Join(err, opened.release()) }()
			inputs, e := opened.book.DerivedInputs(c.Context(), response)
			if e != nil {
				return bookError(e)
			}
			out := derivedInputsOutput{Response: inputs.Response, Derivation: inputs.Derivation, Anchor: inputs.Anchor, Proven: inputs.Proven, Records: make([]derivedRecordOutput, len(inputs.Records))}
			for i, r := range inputs.Records {
				out.Records[i] = derivedRecordOutput{Digest: r.Digest, LeafIndex: r.LeafIndex, Fields: r.Fields}
			}
			return cli.Output(c, out)
		},
	}
	cmd.Flags().String("response", "", "The response record of the answer, as `book request --response` printed it")
	return cmd
}

// reportDeriveResult is what `report derive` prints. ResultRecordID is set as
// soon as the Result is sealed, so a later failure never loses it.
type reportDeriveResult struct {
	Response       string `json:"response"`
	ResultRecordID string `json:"result_record_id,omitempty"`
	BundleDigest   string `json:"bundle_digest,omitempty"`
	Out            string `json:"out,omitempty"`
	Proven         bool   `json:"proven"`
}

func reportDeriveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "derive",
		Short: "Seal the Evidence Result an evaluator produced over a holder's answer to a Terms Pack, and write the Evidence Bundle (contract-report/v1) that verify checks and report build renders",
		Long: "The Result is yours, signed by this book's key, over the rows the holder sent; the holder signed its answer, not the Result. " +
			"It must be a valid Evidence Result v0 whose every claim cites exactly the response record (`result derived-inputs` prints its id) as its evidence. " +
			"Proven rows are in the holder's log at a signed checkpoint; their field values are the holder's word, and no proof shows that no row was left out. " +
			"This command runs no pack: --result is what your evaluator produced.",
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) (err error) {
			response, _ := c.Flags().GetString("response")
			pack, _ := c.Flags().GetString("pack")
			resultPath, _ := c.Flags().GetString("result")
			out, _ := c.Flags().GetString("out")
			allowUnproven, _ := c.Flags().GetBool("allow-unproven")
			if response == "" || pack == "" || resultPath == "" || out == "" {
				return cli.InputError("--response, --pack, --result and --out are required")
			}
			if _, e := os.Lstat(out); e == nil {
				return cli.Hint(cli.ErrInput, "--out "+out+" already exists; nothing was written; pass another path")
			}
			result, e := readResultInput(c, resultPath)
			if e != nil {
				return e
			}
			p, e := cli.Selected(c)
			if e != nil {
				return e
			}
			// A Result that cites a private conduct pack is never bundled, and
			// the bundle declares the profile's key, as `close` and `bundle` do.
			conduct, e := conductPacks(p)
			if e != nil {
				return e
			}
			key, e := cli.PrivateKey(p.Signing)
			if e != nil {
				return e
			}
			opened, e := openBook(c.Context(), p, false)
			if e != nil {
				return e
			}
			defer func() { err = errors.Join(err, opened.release()) }()
			// What the answer proved is read before anything is sealed, so a
			// failure after the seal still reports it.
			inputs, e := opened.book.DerivedInputs(c.Context(), response)
			if e != nil {
				return bookError(e)
			}
			report, e := opened.book.ReportFromDerivation(c.Context(), response,
				func(evidencebook.DerivedInputs) (json.RawMessage, error) { return result, nil },
				evidencebook.ReportOptions{
					Bundle:        evidencebook.BundleRequest{ProducerKey: key.Public().(ed25519.PublicKey), ConductPacks: conduct},
					Derivation:    pack,
					AllowUnproven: allowUnproven,
				})
			printed := reportDeriveResult{Response: response, ResultRecordID: report.Result.RecordID, Proven: inputs.Proven}
			if e != nil {
				// A Result sealed before the failure is reported with it.
				if printed.ResultRecordID != "" {
					return errors.Join(bookError(e), cli.Output(c, printed))
				}
				return bookError(e)
			}
			if _, e = evidencebook.VerifyBundle(report.Bundle.JSON); e != nil {
				return errors.Join(e, cli.Output(c, printed))
			}
			if e = cli.AtomicFile(out, report.Bundle.JSON, false); e != nil {
				return errors.Join(e, cli.Output(c, printed))
			}
			printed.BundleDigest, printed.Out = report.Bundle.Digest, out
			return cli.Output(c, printed)
		},
	}
	cmd.Flags().String("response", "", "The response record of the holder's answer, as `book request --response` printed it")
	cmd.Flags().String("pack", "", "The digest of the Terms Pack your evaluator ran: it must be the one the holder answered (no default: the check is that you name it)")
	cmd.Flags().String("result", "", "The Evidence Result v0 your evaluator produced (a file, or - for stdin)")
	cmd.Flags().String("out", "", "Write the Evidence Bundle here (new file); `book report build --bundle` renders it and `capsulectl verify --bundle` checks it")
	cmd.Flags().Bool("allow-unproven", false, "Allow a report over an answer that carries no proofs (a holder that registered the pack without proofs). The Result's view title must then begin with the sentence \""+evidencebook.UnprovenInputsTitle+"\" (the final space included), so the page never reads as a report over proven inputs")
	return cmd
}

// readResultInput reads the Result file, or stdin for "-", within the input
// size limit as `book policy change --change -` does (cli.ReadInput opens a
// file named "-").
func readResultInput(c *cobra.Command, path string) ([]byte, error) {
	if path != "-" {
		return cli.ReadInput(path)
	}
	raw, err := io.ReadAll(io.LimitReader(c.InOrStdin(), cli.MaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > cli.MaxInput {
		return nil, cli.InputError("the Result exceeds the input size limit")
	}
	return raw, nil
}
