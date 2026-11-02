package deal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// A staple is the holder's own standard card attached to its message, so a
// counterparty that cannot ask back can still read it (evidencebook#275,
// Book.Staple). This plugin holds no profile-level Book, so the staple is
// built from the same pieces `deal respond` answers with: the holder's
// request, pinned to its newest cadence checkpoint with a fresh nonce, and the
// holder's own signed envelope. evidencebook.CheckStaple accepts it unchanged.
// The verb has no default and no setting: the caller decides when to staple
// and which derivation (Steven's 2026-11-01 ruling is Muse's to apply).

// stapleOutput is the verb's stdout: request and reply are the staple's JSON
// objects, embedded.
type stapleOutput struct {
	Derivation string         `json:"derivation"`
	DealID     string         `json:"deal_id,omitempty"`
	Staple     stapleMessages `json:"staple"`
}

type stapleMessages struct {
	Request json.RawMessage `json:"request"`
	Reply   json.RawMessage `json:"reply"`
}

func shStapleCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "staple", Short: "Build a staple of the standard card, for the host to attach to its message: `deal standard-history staple --derivation history_card/1|standard_history/1` prints the holder's request and its signed reply (no default; the host decides when to staple)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		derivation, _ := c.Flags().GetString("derivation")
		dealID, _ := c.Flags().GetString("deal")
		if derivation != evidencebook.DerivationHistoryCard && derivation != evidencebook.DerivationStandardHistory {
			return cli.InputError("--derivation is required and is " + evidencebook.DerivationHistoryCard + " or " + evidencebook.DerivationStandardHistory + ": a staple never carries more than the standard card")
		}
		if dealID != "" && !cli.DealIDPattern.MatchString(dealID) {
			return cli.InputError("--deal is not a deal id")
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
		if dealID != "" {
			var holds int
			if err = s.db.QueryRowContext(c.Context(), `SELECT COUNT(*) FROM deal_keys WHERE deal_id=?`, dealID).Scan(&holds); err != nil {
				return err
			}
			if holds == 0 {
				return cli.InputError("--deal " + dealID + " is not a deal in this profile's store")
			}
		}
		out := stapleOutput{Derivation: derivation}
		if derivation == evidencebook.DerivationStandardHistory {
			out.DealID = dealID
			if out.Staple, err = s.stapleStandardHistory(c.Context(), dealID); err != nil {
				return err
			}
		} else if out.Staple, err = s.stapleHistoryCard(c.Context()); err != nil {
			return err
		}
		return cli.Output(c, out)
	}}
	cmd.Flags().String("derivation", "", "history_card/1 or standard_history/1 (required)")
	cmd.Flags().String("deal", "", "The deal a standard_history/1 staple is attached to, recorded in the holder's answered record (ignored for history_card/1)")
	return cmd
}

// stapleStandardHistory builds a standard_history/1 staple over full_history
// and seals the holder's evidence_request_answered record for it, naming the
// deal when there is one. The holder must be declared and answer the card: a
// staple shows the card to whoever reads the message, so it follows the same
// gate `deal respond` has.
func (s *dealSession) stapleStandardHistory(ctx context.Context, dealID string) (stapleMessages, error) {
	settings, declared, err := readSHSettings(s.p)
	if err != nil {
		return stapleMessages{}, err
	}
	if !declared {
		return stapleMessages{}, errNoHolderKind()
	}
	if !settings.answers() {
		return stapleMessages{}, cli.InputError("this profile answers no standard card: `deal standard-history declare --holder-kind person --answer` lets it staple one")
	}
	cadence, _, err := s.cadenceOf(ctx)
	if err != nil {
		return stapleMessages{}, err
	}
	pin, tickN, err := stapleAnchor(ctx, cadence)
	if err != nil {
		return stapleMessages{}, err
	}
	signer, err := evidencebook.NewEd25519Signer(s.key)
	if err != nil {
		return stapleMessages{}, err
	}
	request, err := stapleRequest(evidencebook.DerivationStandardHistory, evidencebook.SubjectFullHistory, pin)
	if err != nil {
		return stapleMessages{}, err
	}
	check := evidencerequest.ParseRequest(request)
	if check.Reason != "" {
		return stapleMessages{}, errors.New("the staple's own request is malformed: " + check.Why)
	}
	reply, answered, err := s.answerStandardHistory(ctx, check, request.Octets, evidencebook.KeyID(signer), signer)
	if err != nil {
		return stapleMessages{}, err
	}
	if answered["outcome"] != evidencebook.OutcomeArtifact {
		// The pin is the newest checkpoint, so the card itself is what cannot be
		// built (an unpriced or legacy deal): name that, not a tick.
		if _, herr := s.standardHistory(ctx, tickN); herr != nil {
			return stapleMessages{}, fmt.Errorf("the standard card cannot be built: %w", herr)
		}
		reason, _ := answered["reason"].(string)
		return stapleMessages{}, errors.New("the book refused its own standard_history/1 request (" + reason + ")")
	}
	if _, err = s.recordExchange(ctx, dealID, dealExchangeAnswered, "", answered); err != nil {
		return stapleMessages{}, err
	}
	return stapleMessages{Request: request.Octets, Reply: reply}, nil
}

// stapleHistoryCard builds a history_card/1 staple: the chain of the profile's
// cadence checkpoints with the consistency proof from each to the next, as of
// the newest finished tick. It seals nothing.
func (s *dealSession) stapleHistoryCard(ctx context.Context) (stapleMessages, error) {
	cadence, _, err := s.cadenceOf(ctx)
	if err != nil {
		return stapleMessages{}, err
	}
	st, err := cadence.HistoryCard(ctx, 0)
	if errors.Is(err, evidencebook.ErrNotFound) {
		return stapleMessages{}, fmt.Errorf("%w (run `deal tick` first)", errNoTick)
	}
	if err != nil {
		return stapleMessages{}, err
	}
	return stapleMessages{Request: st.Request.Octets, Reply: st.Reply.Octets}, nil
}

// stapleAnchor is the digest of the newest finished cadence tick's checkpoint,
// the staple's as-of, and that tick's number.
func stapleAnchor(ctx context.Context, cadence *evidencebook.Cadence) (string, uint64, error) {
	tick, ok, err := cadence.Last(ctx)
	if err != nil {
		return "", 0, err
	}
	for n := tick.N; ok && n >= 1; n-- {
		t, err := cadence.Get(ctx, n)
		if errors.Is(err, evidencebook.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		if t.CheckpointSize > 0 {
			return evidencerequest.Digest(t.Checkpoint), n, nil
		}
	}
	return "", 0, fmt.Errorf("%w (run `deal tick` first)", errNoTick)
}

// stapleRequest is the holder's request for its own card: the derivation over
// its subject, pinned to anchor, with a fresh nonce so a reader can tell a
// replayed staple.
func stapleRequest(derivation string, subject evidencebook.SubjectKind, anchor string) (evidencebook.Message, error) {
	nonce, err := stapleNonce()
	if err != nil {
		return evidencebook.Message{}, err
	}
	octets, err := evidencebook.EncodeRequest(evidencebook.EvidenceRequest{
		Subject: evidencebook.Subject{Kind: subject}, Derivation: derivation,
		Coverage: evidencebook.Coverage{ExpectedPin: anchor}, Nonce: nonce, Profile: evidencebook.ProfileID,
	}, evidencebook.MediaTypeJSON)
	if err != nil {
		return evidencebook.Message{}, err
	}
	return evidencebook.Message{Octets: octets, MediaType: evidencebook.MediaTypeJSON}, nil
}

// stapleNonce is 16 fresh random bytes, hex.
func stapleNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
