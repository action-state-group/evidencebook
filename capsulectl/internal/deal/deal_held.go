package deal

import (
	"encoding/hex"
	"errors"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// The holder's side of "ask me" (F4.10, #178): `deal held` lists the requests
// `deal respond` held, `deal decline` answers one with a signed
// policy_declined refusal. The reply is evidencebook's own Refusal, so
// nothing on the wire changes. A held item keeps no request octets and no held
// derivation is servable, so there is nothing to serve yet.

// heldItem is a held request that no decision followed.
type heldItem struct {
	RequestDigest string `json:"request_digest"`
	Requester     string `json:"requester"`
	Derivation    string `json:"derivation"`
	SubjectKind   string `json:"subject_kind,omitempty"`
	ReceiptID     string `json:"receipt_id,omitempty"`
	HeldAt        string `json:"held_at"`
	State         string `json:"state"`
}

// heldRows is every held row, with whether a decision followed it.
func (s *dealSession) heldRows(c *cobra.Command) (held []dealExchangeRow, decided map[string]bool, err error) {
	ctx := c.Context()
	if held, err = s.exchangeRows(ctx, `kind=?`, dealExchangeHeld); err != nil {
		return nil, nil, err
	}
	answered, err := s.exchangeRows(ctx, `kind=?`, dealExchangeAnswered)
	if err != nil {
		return nil, nil, err
	}
	decided = map[string]bool{}
	for _, a := range answered {
		if d, ok := a.record["request_digest"].(string); ok {
			decided[d] = true
		}
	}
	return held, decided, nil
}

func dealHeldCommand() *cobra.Command {
	return &cobra.Command{Use: "held", Short: "List the requests above the standard card that `deal respond` held for you and that you have not declined", Long: "List the requests above the standard card that `deal respond` held for you and that you have not declined. Each is held, can be declined with `deal decline`.\n\nServing a held request is not built yet: it comes with the first servable held derivation.", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		s, err := openDealSession(c.Context(), p)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		rows, decided, err := s.heldRows(c)
		if err != nil {
			return err
		}
		items := []heldItem{}
		for _, r := range rows {
			digest, _ := r.record["request_digest"].(string)
			if decided[digest] {
				continue
			}
			item := heldItem{RequestDigest: digest, ReceiptID: r.dealID, State: "held, can be declined"}
			item.Requester, _ = r.record["requester"].(string)
			item.Derivation, _ = r.record["derivation"].(string)
			item.SubjectKind, _ = r.record["subject_kind"].(string)
			item.HeldAt, _ = r.record["at"].(string)
			items = append(items, item)
		}
		return cli.Output(c, map[string]any{"held": items})
	}}
}

func dealDeclineCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "decline", Short: "Decline a held request: write a signed policy_declined refusal for the requester and record it", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		digest, _ := c.Flags().GetString("request-digest")
		out, _ := c.Flags().GetString("output")
		if digest == "" || out == "" {
			return cli.InputError("--request-digest (from `deal held`) and --output are required")
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
		return s.declineHeld(c, digest, out)
	}}
	cmd.Flags().String("request-digest", "", "The held request's digest, as `deal held` lists it")
	cmd.Flags().String("output", "", "Where to write the signed refusal, to send to the requester")
	return cmd
}

// declineHeld signs and writes the refusal for a held request, and records it
// once: declining a declined request writes a fresh refusal and records
// nothing more.
func (s *dealSession) declineHeld(c *cobra.Command, digest, path string) error {
	ctx := c.Context()
	held, decided, err := s.heldRows(c)
	if err != nil {
		return err
	}
	var row *dealExchangeRow
	for i := range held {
		if held[i].record["request_digest"] == digest {
			row = &held[i]
		}
	}
	if row == nil {
		return cli.InputError("no held request has digest " + digest + ": `deal held` lists them")
	}
	signer, err := evidencebook.NewEd25519Signer(s.key)
	if err != nil {
		return err
	}
	now := cli.DealClock().UTC()
	refusal := evidencebook.Refusal{RequestDigest: digest, Reason: evidencebook.ReasonPolicyDeclined, KeyID: evidencebook.KeyID(signer)}
	var encoded []byte
	if row.dealID == "" {
		// Held in evidence-interop's request shape: answered in its reply,
		// whose issued_at is the profile's whole-second form (as
		// answerStandardHistory writes it).
		refusal.IssuedAt = evidencerequest.ProfileTime(now)
		if refusal, err = refusal.Sign(ctx, signer); err != nil {
			return err
		}
		reply, e := evidencerequest.EncodeReply(evidencerequest.MediaTypeJSON, nil, &refusal)
		if e != nil {
			return e
		}
		encoded = reply.Octets
	} else {
		refusal.IssuedAt = now.Format(time.RFC3339Nano)
		body, e := refusal.SigningBody()
		if e != nil {
			return e
		}
		sig, e := signer.Sign(ctx, body)
		if e != nil {
			return e
		}
		refusal.Sig = hex.EncodeToString(sig)
		if encoded, err = marshalDealRefusal(refusal); err != nil {
			return err
		}
	}
	if !decided[digest] {
		answered := map[string]interface{}{"request_digest": digest, "requester": row.record["requester"], "outcome": evidencebook.OutcomeRefusal, "reason": evidencebook.ReasonPolicyDeclined}
		if k, ok := row.record["subject_kind"]; ok {
			answered["subject_kind"] = k
		}
		return s.writeAnswerBytes(c, row.dealID, answered, encoded, path)
	}
	// Declined before: a fresh refusal is written and nothing more recorded.
	result := cli.EvidenceRespondResult{RequestDigest: digest, Outcome: evidencebook.OutcomeRefusal, Reason: evidencebook.ReasonPolicyDeclined, Relationship: evidencebook.RelationshipIdentified, Output: path}
	if err = cli.AtomicFile(path, encoded, false); err != nil {
		result.Output, result.Response = "", encoded
		return errors.Join(err, cli.Output(c, result))
	}
	return cli.Output(c, result)
}
