package deal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// A staple is the holder's own History card attached to its message, so a
// counterparty that cannot ask back can still read it (evidencebook#275).
// Offer staples always carry history_card/2: a sampled chain of the
// profile's cadence checkpoints with no deal counts, at any length. It
// replaces the history_card/1 staple and its standard_history/1 fallback
// (evidencebook#354). The card a counterparty gets on a signed ask is
// unchanged (`deal respond`). evidencebook.CheckStaple accepts the staple
// unchanged. The caller decides when to staple.

// stapleOutput is the verb's stdout: request and reply are the staple's JSON
// objects, embedded.
type stapleOutput struct {
	Derivation string         `json:"derivation"`
	Staple     stapleMessages `json:"staple"`
}

type stapleMessages struct {
	Request json.RawMessage `json:"request"`
	Reply   json.RawMessage `json:"reply"`
}

const shStapleLong = `Build the staple the host attaches to its message: the profile's History card
history_card/2, a sampled chain of its cadence checkpoints with no deal counts,
as of the newest finished tick, and the holder's own signed reply. It prints the
holder's request and the reply. It works at any length and seals nothing.

A staple always carries history_card/2. --derivation, when given, must be
history_card/2; history_card/1 and standard_history/1 are no longer stapled.
The standard card stays behind a signed ask (` + "`deal respond`" + `).`

func shStapleCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "staple", Short: "Build the staple the host attaches to its message: the profile's history_card/2, sampled and with no deal counts", Long: shStapleLong, Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		// --derivation stays, accepting history_card/2 only, so a caller that
		// probes the help for it finds the staple builder and a caller still
		// asking for /1 is refused with the reason, not silently given /2.
		derivation, _ := c.Flags().GetString("derivation")
		if err := stapleDerivation(derivation); err != nil {
			return err
		}
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		s, err := openDealSession(c.Context(), p)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		out := stapleOutput{Derivation: derivation}
		if out.Staple, err = s.stapleHistoryCard2(c.Context()); err != nil {
			return err
		}
		return cli.Output(c, out)
	}}
	cmd.Flags().String("derivation", evidencebook.DerivationHistoryCard2, "history_card/2, the only derivation a staple carries")
	return cmd
}

// stapleHistoryCard2 builds the history_card/2 staple over the profile's
// cadence log, as of the newest finished tick, with the default window. It
// seals nothing.
func (s *dealSession) stapleHistoryCard2(ctx context.Context) (stapleMessages, error) {
	cadence, _, err := s.cadenceOf(ctx)
	if err != nil {
		return stapleMessages{}, err
	}
	st, err := cadence.HistoryCard2(ctx, 0, 0)
	if errors.Is(err, evidencebook.ErrNotFound) {
		return stapleMessages{}, shNoTick(fmt.Errorf("%w (run `deal tick` first)", errNoTick))
	}
	if err != nil {
		return stapleMessages{}, err
	}
	return stapleMessages{Request: st.Request.Octets, Reply: st.Reply.Octets}, nil
}

// stapleDerivation refuses any --derivation but history_card/2, with the
// reason for the two a staple used to carry.
func stapleDerivation(derivation string) error {
	switch derivation {
	case evidencebook.DerivationHistoryCard2:
		return nil
	case evidencebook.DerivationHistoryCard, evidencebook.DerivationStandardHistory:
		return cli.InputError("a staple carries " + evidencebook.DerivationHistoryCard2 + " only: " + derivation +
			" is no longer stapled, and the standard card stays behind a signed ask (`deal respond`)")
	}
	return cli.InputError(fmt.Sprintf("--derivation %q is not a derivation a staple carries: a staple carries %s only", derivation, evidencebook.DerivationHistoryCard2))
}
