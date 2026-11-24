package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// A deal store whose journal is split by window (`deal init --window`,
// evidencebook#797, docs/design/797-deal-window.md §4-§5): the verbs a holder
// runs on its windows, and the respond a requester's report over windows
// asks of it. Each deal's book stays whole, placed in the window its
// agreement was sealed in.

func dealWindowCommands() *cobra.Command {
	window := &cobra.Command{Use: "window", Short: "A windowed deal store's journal windows: list, roll, chain, and answer a Terms Pack over a window or a placed deal"}
	window.AddCommand(dealWindowListCommand(), dealWindowRollCommand(), dealWindowChainCommand(), dealWindowRespondCommand())
	return window
}

// windowedSession runs fn on a session of a store created with a window.
func windowedSession(c *cobra.Command, fn func(ctx context.Context, s *dealSession) error) error {
	return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
		if s.shelf.WindowPolicy() == nil {
			return cli.InputError("this deal store has no window: a window is chosen when the store is created (`deal init --window`)")
		}
		return fn(ctx, s)
	})
}

// dealWindowState is one window as `deal window list` prints it.
type dealWindowState struct {
	evidencebook.WindowState
	// Placed are the deal books placed in the window, by log id.
	Placed []string `json:"placed"`
}

func dealWindowListCommand() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List the store's journal windows, oldest first, each with the deal books placed in it (does not roll)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return windowedSession(c, func(ctx context.Context, s *dealSession) error {
			windows, err := s.shelf.Windows(ctx)
			if err != nil {
				return err
			}
			placed, err := s.placedByWindow(ctx)
			if err != nil {
				return err
			}
			out := make([]dealWindowState, len(windows))
			for i, w := range windows {
				out[i] = dealWindowState{WindowState: w, Placed: placed[w.Label]}
				if out[i].Placed == nil {
					out[i].Placed = []string{}
				}
			}
			return cli.Output(c, map[string]any{"window": dealWindowNotice(s.shelf.WindowPolicy()), "windows": out})
		})
	}}
}

// placedByWindow is every placed book's log id by the label of its window.
func (s *dealSession) placedByWindow(ctx context.Context) (map[string][]string, error) {
	logs, err := s.shelf.Books(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, logID := range logs {
		w, ok, err := s.shelf.Placement(ctx, logID)
		if err != nil {
			return nil, err
		}
		if ok {
			out[w.Label] = append(out[w.Label], logID)
		}
	}
	return out, nil
}

func dealWindowRollCommand() *cobra.Command {
	return &cobra.Command{Use: "roll", Short: "Close every journal window that has ended and open the next ones, so last period's final checkpoint exists without a new record", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return windowedSession(c, func(ctx context.Context, s *dealSession) error {
			current, err := s.shelf.Roll(ctx)
			if err != nil {
				return err
			}
			return cli.Output(c, map[string]any{"current": current})
		})
	}}
}

func dealWindowChainCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "chain", Short: "Write the window chain (window_chain/v1) over closed journal windows --from to --to: each window's open and close, its placed deals and the proof each deal's book extends its tree at the close", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		from, _ := c.Flags().GetString("from")
		to, _ := c.Flags().GetString("to")
		out, _ := c.Flags().GetString("out")
		if from == "" || to == "" || out == "" {
			return cli.InputError("--from, --to and --out are required: the first and last window labels, and the file to write")
		}
		return windowedSession(c, func(ctx context.Context, s *dealSession) error {
			chain, err := s.shelf.WindowChain(ctx, from, to, evidencebook.WindowChainOptions{})
			if err != nil {
				return err
			}
			raw, err := json.Marshal(chain)
			if err != nil {
				return err
			}
			if err := cli.AtomicFile(out, append(raw, '\n'), false); err != nil {
				return err
			}
			return cli.Output(c, map[string]any{"chain": out, "from": chain.From, "to": chain.To, "windows": len(chain.Windows)})
		})
	}}
	cmd.Flags().String("from", "", "The first window's label, such as 2026-11")
	cmd.Flags().String("to", "", "The last window's label; every window from --from to it must be closed")
	cmd.Flags().String("out", "", "The file the chain is written to")
	return cmd
}

// windowPackFile is a --pack file: a Terms Pack the holder agrees to answer
// by the digest of its compiled definition, in capsulectl-book's pack-file
// format.
type windowPackFile struct {
	Document   json.RawMessage `json:"document"`
	Recipients []string        `json:"recipients"`
	Forms      []string        `json:"forms"`
	Fields     []string        `json:"fields"`
	Proofs     *bool           `json:"proofs"`
}

func readWindowPack(path string) (evidencebook.DerivationDefinition, error) {
	raw, err := cli.ReadInput(path)
	if err != nil {
		return evidencebook.DerivationDefinition{}, err
	}
	var file windowPackFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil || len(file.Document) == 0 {
		return evidencebook.DerivationDefinition{}, cli.InputError(fmt.Sprintf("--pack %s: a pack file is an object with document, recipients, forms, fields and proofs: %v", path, err))
	}
	def := evidencebook.DerivationDefinition{Document: file.Document, Recipients: file.Recipients, Fields: file.Fields, Proofs: file.Proofs == nil || *file.Proofs}
	for _, form := range file.Forms {
		def.Forms = append(def.Forms, evidencebook.SubjectKind(form))
	}
	return def, nil
}

func dealWindowRespondCommand() *cobra.Command {
	cmd := cli.EvidenceRespondCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRespondInput) (err error) {
		packs, _ := c.Flags().GetStringArray("pack")
		var defs []evidencebook.DerivationDefinition
		for _, path := range packs {
			def, err := readWindowPack(path)
			if err != nil {
				return err
			}
			defs = append(defs, def)
		}
		policy := evidencebook.DefaultSharePolicy()
		if in.PolicyPath != "" {
			raw, e := cli.ReadInput(in.PolicyPath)
			if e != nil {
				return e
			}
			if e = cli.DecodeJSONAs("--policy "+in.PolicyPath, raw, &policy); e != nil {
				return e
			}
		}
		ctx := c.Context()
		s, err := openDealSession(ctx, p)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		if s.shelf.WindowPolicy() == nil {
			return cli.InputError("this deal store has no window: a window is chosen when the store is created (`deal init --window`)")
		}
		for _, def := range defs {
			if _, err := s.shelf.RegisterDerivation(def); err != nil {
				return cli.InputError("a --pack file: " + err.Error())
			}
		}
		// The window a request pins, or the placed deal book whose checkpoint
		// it pins, answers; the answered record goes in the current window.
		reply, record, e := s.shelf.Respond(ctx, evidencebook.Message{Octets: in.Request, MediaType: evidencebook.MediaTypeJSON},
			evidencebook.RespondOptions{AuthenticatedRequester: in.Requester, Policy: policy})
		if e != nil {
			return e
		}
		result := cli.EvidenceRespondResult{RecordID: record.RecordID, Output: in.Output}
		switch {
		case record.RecordID != "":
			var st evidencebook.AnsweredStatement
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			result.RequestDigest, result.Outcome, result.Reason, result.Relationship = st.RequestDigest, st.Outcome, st.Reason, st.Relationship
		case reply.Refusal != nil:
			result.RequestDigest, result.Outcome, result.Reason = reply.Refusal.RequestDigest, evidencebook.OutcomeRefusal, reply.Refusal.Reason
		case reply.Envelope != nil:
			result.RequestDigest, result.Outcome = reply.Envelope.RequestDigest, evidencebook.OutcomeArtifact
		}
		if e = cli.AtomicFile(in.Output, reply.Octets, false); e != nil {
			// The reply is signed (and, when record_id is set, on record)
			// already: report it rather than lose it with the file.
			result.Output, result.Response = "", reply.Octets
			return errors.Join(e, cli.Output(c, result))
		}
		return cli.Output(c, result)
	})
	cmd.Short = "Answer an Evidence Request on a windowed deal store: from the journal window or placed deal book its pin names, recording the answer in the current window"
	cmd.Flags().StringArray("pack", nil, "A Terms Pack this answer may serve, by the digest of its compiled definition (a pack file: document, recipients, forms, fields, proofs); repeat for more")
	return cmd
}
