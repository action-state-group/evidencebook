package book

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// derivedInputsOutput is what `result derived-inputs` prints: the contract with
// an evaluator. A holder's answer to a request that named a Terms Pack by the
// digest of its definition, as this book recorded it.
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
			responses, _ := c.Flags().GetStringArray("response")
			pack, _ := c.Flags().GetString("pack")
			resultPath, _ := c.Flags().GetString("result")
			out, _ := c.Flags().GetString("out")
			allowUnproven, _ := c.Flags().GetBool("allow-unproven")
			chainPath, _ := c.Flags().GetString("chain")
			if len(responses) == 0 || pack == "" || resultPath == "" || out == "" {
				return cli.InputError("--response, --pack, --result and --out are required")
			}
			if chainPath != "" {
				if allowUnproven {
					return cli.InputError("--allow-unproven does not apply with --chain: a report over windows needs every answer's proofs")
				}
				return deriveOverWindows(c, chainPath, responses, pack, resultPath, out)
			}
			if len(responses) > 1 {
				return cli.InputError("several --response answers need --chain: the holder's window chain they were asked over")
			}
			response := responses[0]
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
	cmd.Flags().StringArray("response", nil, "The response record of the holder's answer, as `book request --response` printed it; with --chain, one per window (repeat the flag)")
	cmd.Flags().String("chain", "", "The holder's window chain (`book window chain`) the answers were asked over, one answer per window pinned to its final checkpoint: the bundle carries it, and the windows must tile its range")
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

// reportWindowsResult is what `report derive --chain` prints.
type reportWindowsResult struct {
	Chain          string   `json:"chain_record_id"`
	Responses      []string `json:"responses"`
	From           string   `json:"from"`
	To             string   `json:"to"`
	ResultRecordID string   `json:"result_record_id,omitempty"`
	BundleDigest   string   `json:"bundle_digest,omitempty"`
	Out            string   `json:"out,omitempty"`
}

// deriveOverWindows is `report derive --chain`: the holder's window chain is
// sealed in this book, then the Result over every window's answer, and the
// bundle is written only once its coverage of the chain's range has no
// finding (evidencebook#689 §5).
func deriveOverWindows(c *cobra.Command, chainPath string, responses []string, pack, resultPath, out string) (err error) {
	if _, e := os.Lstat(out); e == nil {
		return cli.Hint(cli.ErrInput, "--out "+out+" already exists; nothing was written; pass another path")
	}
	result, err := readResultInput(c, resultPath)
	if err != nil {
		return err
	}
	rawChain, err := cli.ReadInput(chainPath)
	if err != nil {
		return err
	}
	var chain evidencebook.WindowChain
	if err = cli.DecodeJSONAs("--chain "+chainPath, rawChain, &chain); err != nil {
		return err
	}
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	conduct, err := conductPacks(p)
	if err != nil {
		return err
	}
	key, err := cli.PrivateKey(p.Signing)
	if err != nil {
		return err
	}
	opened, err := openBook(c.Context(), p, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, opened.release()) }()
	// The holder's checkpoint key is the one this book pinned when it
	// recorded the answers.
	for _, r := range responses {
		if err = requireInCurrentWindow(c.Context(), opened, r); err != nil {
			return bookError(err)
		}
	}
	first, err := opened.book.Get(c.Context(), responses[0])
	if err != nil {
		return bookError(err)
	}
	var statement evidencebook.ResponseStatement
	if err = json.Unmarshal(first.Header.Statement, &statement); err != nil {
		return err
	}
	chainOpts := evidencebook.VerifyChainOptions{CheckpointKey: statement.CheckpointKey}
	digest, err := chainDigest(chain)
	if err != nil {
		return err
	}
	chainRecord, reused, err := recordedChain(c.Context(), opened.book, digest)
	if err != nil {
		return err
	}
	if !reused {
		if chainRecord, err = opened.book.RecordWindowChain(c.Context(), chain, chainOpts); err != nil {
			return bookError(err)
		}
	}
	printed := reportWindowsResult{Chain: chainRecord.RecordID, Responses: responses, From: chain.From, To: chain.To}
	report, err := opened.book.ReportFromWindows(c.Context(), chainRecord.RecordID, responses,
		func([]evidencebook.WindowInputs) (json.RawMessage, error) { return result, nil },
		evidencebook.ReportOptions{Bundle: evidencebook.BundleRequest{ProducerKey: key.Public().(ed25519.PublicKey), ConductPacks: conduct}, Derivation: pack},
		chainOpts)
	printed.ResultRecordID = report.Result.RecordID
	if err != nil {
		return errors.Join(bookError(err), cli.Output(c, printed))
	}
	coverage, err := evidencebook.VerifyReportCoverage(report.Bundle.JSON, evidencebook.CoverageOptions{CheckpointKey: statement.CheckpointKey})
	if err != nil {
		return errors.Join(err, cli.Output(c, printed))
	}
	if len(coverage.Findings) > 0 {
		return errors.Join(cli.InputError("the report does not cover its range: "+findingsText(coverage.Findings)), cli.Output(c, printed))
	}
	if err = cli.AtomicFile(out, report.Bundle.JSON, false); err != nil {
		return errors.Join(err, cli.Output(c, printed))
	}
	printed.BundleDigest, printed.Out = report.Bundle.Digest, out
	return cli.Output(c, printed)
}

func findingsText(findings []evidencebook.ChainFinding) string {
	texts := make([]string, len(findings))
	for i, f := range findings {
		texts[i] = f.Text
	}
	return strings.Join(texts, "; ")
}

// reportCoverageResult is what `report coverage` prints: the range, the
// windows proved, and every finding. Covered is true only with no finding.
type reportCoverageResult struct {
	Covered    bool                          `json:"covered"`
	From       string                        `json:"from"`
	To         string                        `json:"to"`
	Derivation string                        `json:"derivation,omitempty"`
	Windows    []evidencebook.VerifiedWindow `json:"windows"`
	// Deals are the coverage lines of the deal books placed in the windows,
	// on a deal store's report (evidencebook#797): each deal's answer covers
	// its whole book, records after its window included.
	Deals    []string `json:"deals,omitempty"`
	Findings []string `json:"findings"`
	// Limits is what a covered range does not show.
	Limits []string `json:"limits,omitempty"`
}

func reportCoverageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Check that a report over a holder's windows covers its range: the chain links, the windows tile the range, and every window has its answer (reads only; needs no profile)",
		Long: "Checks the bundle `report derive --chain` wrote, under the holder's checkpoint key you obtained yourself (never one read from the bundle). Each gap is named (\"window 2026-03 has no answer\", \"chain breaks at 2026-04\"); a range with any finding is not covered, and the command exits non-zero. " +
			"On a deal store's report, every deal placed in a window needs its own answer too (\"deal deal/… of window 2026-03 has no answer\"), and each covered deal is printed with its line: its answer covers its whole book, records after its window included. " +
			"Without --witness-directory, the windows' continuity is as the holder signed it. Each answer proves its rows are in the holder's log; the rows' field values are the holder's word; no proof shows that no row was left out.",
		Args: cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			bundlePath, _ := c.Flags().GetString("bundle")
			holderKey, _ := c.Flags().GetString("holder-checkpoint-key")
			directory, _ := c.Flags().GetString("witness-directory")
			if bundlePath == "" || holderKey == "" {
				return cli.InputError("--bundle and --holder-checkpoint-key are required")
			}
			raw, err := cli.ReadInput(bundlePath)
			if err != nil {
				return err
			}
			opts := evidencebook.CoverageOptions{CheckpointKey: holderKey}
			if directory != "" {
				rows, e := os.ReadFile(directory)
				if e != nil {
					return cli.InputError("--witness-directory: " + e.Error())
				}
				if opts.Witnesses, e = evidencebook.ParseWitnessDirectory(rows); e != nil {
					return cli.InputError("--witness-directory: " + e.Error())
				}
			}
			coverage, err := evidencebook.VerifyReportCoverage(raw, opts)
			if err != nil {
				return bookError(err)
			}
			printed := reportCoverageResult{Covered: len(coverage.Findings) == 0, From: coverage.From, To: coverage.To, Derivation: coverage.Derivation, Windows: coverage.Windows, Findings: []string{}}
			for _, f := range coverage.Findings {
				printed.Findings = append(printed.Findings, f.Text)
			}
			for _, d := range coverage.Deals {
				printed.Deals = append(printed.Deals, d.Line)
			}
			if !printed.Covered {
				return errors.Join(cli.InputError("the report does not cover its range: "+findingsText(coverage.Findings)), cli.Output(c, printed))
			}
			if directory == "" {
				printed.Limits = append(printed.Limits, "Without --witness-directory, the windows' continuity is as the holder signed it.")
			}
			printed.Limits = append(printed.Limits, coverage.Limit)
			return cli.Output(c, printed)
		},
	}
	cmd.Flags().String("bundle", "", "The report bundle `report derive --chain` wrote")
	cmd.Flags().String("holder-checkpoint-key", "", "The holder's checkpoint key (64 hex), obtained independently of the bundle")
	cmd.Flags().String("witness-directory", "", "A witness directory: every window's final checkpoint must then carry a receipt that verifies under it")
	return cmd
}
