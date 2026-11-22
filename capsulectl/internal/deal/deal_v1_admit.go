// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// dealV1AdmitTable binds each admit sealed by `deal admit` to the one deal it
// was sealed for (evidencebook#626, B1). The record carries no deal id: an
// admit is a journal record that x-deal-v1 PROFILE §3 keeps free of one, so
// the binding is the plugin's own. One admit per deal; `deal agree` cites
// only the deal's own admit.
const dealV1AdmitTable = `CREATE TABLE IF NOT EXISTS deal_v1_admits (
	deal_id TEXT PRIMARY KEY,
	admit_digest TEXT NOT NULL UNIQUE,
	payload BLOB NOT NULL
)`

// engineDecision is the part of `capsule-engine admission evaluate --admit`'s
// output that `deal admit` reads (capsule-engine go/cmd/capsule-engine/main.go,
// output). The rest of that object is the engine's and is not read here.
type engineDecision struct {
	Verdict string  `json:"verdict"`
	Tier    *string `json:"tier"`
	Rows    []struct {
		Row string `json:"row"`
	} `json:"rows"`
	Pack *struct {
		Digest string `json:"digest"`
	} `json:"pack"`
	Admit      json.RawMessage `json:"admit"`
	AdmitsDeal *bool           `json:"admits_deal"`
}

const dealAdmitLong = `Seal your admission decision for an x-deal-v1 deal in your journal, so that
` + "`deal agree`" + ` can cite it. --input is the whole output of
` + "`capsule-engine admission evaluate --admit FILE`" + ` for this deal's counterparty;
the decision is capsule-engine's, and this command evaluates nothing.

It refuses a decision capsule-engine says does not admit the deal
("admits_deal": false: an ASK still waiting for answers, a NO, or a LIMIT
whose tier does not hold the deal), an output without an admit body, and an
admit body that is not exactly the one capsule-engine prints. It cannot tell
which counterparty or amount the decision was made for: pass the output of the
run on this deal's own counterparty and amount.

Each deal has one admission decision, and ` + "`deal agree`" + ` cites it. A second
decision for the same deal is refused, and so is a deal already agreed; to
decide again, start a new deal. Running it again with the same --input prints
the same digest and seals nothing. One capsule-engine output is sealed for one
deal only: its admit's nonce is drawn for one record.

The admit record stays in your journal; only its digest leaves you, in your
agreement. Needs a profile on x-deal-v1 (deal set-version x-deal-v1 --migrate)
and the deal's offer (one you made, or one ` + "`deal receive`" + ` took).`

func dealAdmitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admit",
		Short: "Seal capsule-engine's admission decision for an x-deal-v1 deal, for `deal agree` to cite (--deal ID --input FILE)",
		Long:  dealAdmitLong,
		Args:  cli.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dealID, _ := c.Flags().GetString("deal")
			if !cli.DealIDPattern.MatchString(dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer` or `deal receive`: deal- followed by 16 hex characters")
			}
			path, _ := c.Flags().GetString("input")
			if path == "" {
				return cli.InputError("--input is required: the output of `capsule-engine admission evaluate --admit FILE` for this deal")
			}
			raw, err := cli.ReadInput(path)
			if err != nil {
				return err
			}
			payload, err := admitPayloadOf(raw)
			if err != nil {
				return err
			}
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				d, err := s.v1DealOf(ctx, dealID)
				if err != nil {
					return err
				}
				if d == nil {
					return cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds: make or receive its offer first (`deal offer`, `deal receive`)")
				}
				if d.State == "cancelled" || d.State == "declined" {
					return cli.InputError("this deal's offer is " + d.State + ": there is no deal to admit")
				}
				// An agreement a stopped `deal agree` appended but did not
				// record is found first: it cites its own admit.
				if d.OwnLogID != "" {
					if err := s.repairIfStopped(ctx, d); err != nil {
						return err
					}
					if d, err = s.v1DealOf(ctx, dealID); err != nil {
						return err
					}
				}
				if d.OwnAgreementID != "" {
					return cli.InputError("deal " + dealID + " is already agreed, and its agreement cites the admission decision it was agreed on: a deal's decision cannot change after it")
				}
				digest, err := s.sealAdmit(ctx, dealID, payload)
				if err != nil {
					return err
				}
				return cli.Output(c, map[string]any{"deal_id": dealID, "admit": digest})
			})
		},
	}
	cmd.Flags().String("deal", "", "The x-deal-v1 deal this decision is for")
	cmd.Flags().String("input", "", "The output of `capsule-engine admission evaluate --admit FILE`, as a JSON file")
	return cmd
}

// admitPayloadOf reads capsule-engine's output and returns the payload of the
// admit record its admit body makes, refusing what an agreement cannot cite.
// The refusal follows the engine's own admits_deal, never the outcome word:
// whether a LIMIT's tier holds the deal is the engine's to say (it has the
// pack's tiers), so the plugin never judges it differently.
func admitPayloadOf(raw []byte) ([]byte, error) {
	var out engineDecision
	// The engine's object has more members than these, which are its own,
	// so the outer object is read leniently; the admit body inside it is
	// read strictly below.
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, cli.InputError("--input is not the JSON output of `capsule-engine admission evaluate --admit FILE`: " + err.Error())
	}
	if out.AdmitsDeal == nil || out.Admit == nil {
		return nil, cli.InputError("--input has no admit body: run `capsule-engine admission evaluate` with --admit FILE, which prints the decision as an x-deal-v1 admit")
	}
	if bytes.Equal(bytes.TrimSpace(out.Admit), []byte("null")) {
		return nil, cli.InputError("capsule-engine's decision has no tier (a NO), so it is not sealed and no agreement can cite it: this deal is not admitted")
	}
	if !*out.AdmitsDeal {
		return nil, cli.InputError(fmt.Sprintf("capsule-engine says this decision (%s) does not admit the deal: an agreement cites only one that does. If it asks for answers, answer them and run `capsule-engine admission evaluate --admit FILE` again with them; then run `deal admit` with the new output. A LIMIT whose tier does not hold the deal admits a smaller deal only", out.Verdict))
	}
	// The body is sealed exactly as the engine printed it: Decode refuses a
	// member the profile does not name, and Check runs the profile's schema
	// and record checks on the record it makes.
	doc := []byte(`{"x-deal-v1":{"profile":"` + xdealv1.Profile + `","canonicalization":"` + xdealv1.Canonicalization + `","record_type":"` + xdealv1.TypeAdmit + `","at":"` + v1Now() + `"},"body":`)
	doc = append(append(doc, out.Admit...), '}')
	rec, err := xdealv1.Decode(doc)
	if err != nil {
		return nil, cli.InputError("--input's admit is not an x-deal-v1 admit body: " + err.Error())
	}
	body, ok := rec.Body.(*xdealv1.AdmitBody)
	if !ok {
		return nil, cli.InputError("--input's admit is not an x-deal-v1 admit body")
	}
	// The engine's verdict and pack are the body's own outcome and pack: a
	// body that disagrees with the output it came in was not printed with it.
	// So are its tier and its rows, which the engine copies from the same
	// decision (capsule-engine go/admission/admit.go, Admit).
	rows := make([]string, len(out.Rows))
	for i, r := range out.Rows {
		rows[i] = r.Row
	}
	if body.Outcome != out.Verdict || out.Pack == nil || body.PackDigest != out.Pack.Digest ||
		out.Tier == nil || body.Tier != *out.Tier || !slices.Equal(body.Rows, rows) {
		return nil, cli.InputError("--input's admit body does not match the decision it came with (its verdict, tier, rows or pack differ): pass capsule-engine's output unchanged")
	}
	payload, _, err := xdealv1.Encode(doc)
	if err != nil {
		return nil, cli.InputError("--input's admit does not pass the x-deal-v1 checks: " + err.Error())
	}
	return payload, nil
}

// sealAdmit seals the admit for dealID once. The binding is stored before
// the record is sealed, so a run that stops between the two is finished by
// the next run, which seals the stored payload (same time, same digest) when
// the journal does not hold it; a binding whose record the journal never
// took has left nothing with anyone, so a new decision replaces it. The same
// body again returns the digest already sealed; another body for a deal
// whose admit is sealed is refused, so `deal agree` never has two to choose
// from. A body whose nonce an admit in the journal already carries is
// refused: one engine output is one record (PROFILE §5, a nonce is drawn
// for each record), and a journal holding two would fail
// xdealv1.CheckAuthorizations for good.
func (s *dealSession) sealAdmit(ctx context.Context, dealID string, payload []byte) (string, error) {
	var (
		digest string
		stored []byte
	)
	err := s.db.QueryRowContext(ctx, `SELECT admit_digest, payload FROM deal_v1_admits WHERE deal_id=?`, dealID).Scan(&digest, &stored)
	switch {
	case err == nil:
		held, err := s.journalHolds(ctx, digest)
		if err != nil {
			return "", err
		}
		switch {
		case sameAdmitBody(stored, payload):
			payload = stored
		case held:
			return "", fmt.Errorf("%w: deal %s already has an admission decision, %s, and `deal agree` cites that one: a deal has one, so to decide again, start a new deal", cli.ErrConflict, dealID, digest)
		default:
			if digest, err = s.bindAdmit(ctx, dealID, payload, true); err != nil {
				return "", err
			}
		}
	case errors.Is(err, sql.ErrNoRows):
		if digest, err = s.bindAdmit(ctx, dealID, payload, false); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	held, err := s.journalHolds(ctx, digest)
	if err != nil || held {
		return digest, err
	}
	rec, _, err := s.shelf.SealSent(ctx, evidencebook.Entry{RecordType: "deal_step", EpistemicType: evidencebook.ProducerClaim, Payloads: [][]byte{payload}}, "admit/sent")
	if err != nil && rec.RecordID == "" {
		return "", err
	}
	return digest, err
}

// bindAdmit stores the binding of payload to dealID (replacing the deal's
// unsealed one when replace is set), after refusing a nonce another admit
// already carries.
func (s *dealSession) bindAdmit(ctx context.Context, dealID string, payload []byte, replace bool) (string, error) {
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return "", err
	}
	nonce := rec.Body.(*xdealv1.AdmitBody).Nonce
	if other, err := s.admitWithNonce(ctx, nonce); err != nil {
		return "", err
	} else if other != "" {
		return "", fmt.Errorf("%w: this capsule-engine output is already sealed, as admit %s: one output is one record, so run `capsule-engine admission evaluate --admit FILE` again for this deal", cli.ErrConflict, other)
	}
	_, digest, err := xdealv1.Encode(payload)
	if err != nil {
		return "", err
	}
	if replace {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM deal_v1_admits WHERE deal_id=?`, dealID); err != nil {
			return "", err
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO deal_v1_admits (deal_id, admit_digest, payload) VALUES (?, ?, ?)`, dealID, digest, payload); err != nil {
		return "", err
	}
	return digest, nil
}

// admitWithNonce is the digest of an admit that carries nonce: one in the
// journal (however it was sealed), or one bound by `deal admit` and not yet
// sealed; "" when there is none.
func (s *dealSession) admitWithNonce(ctx context.Context, nonce string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT admit_digest, payload FROM deal_v1_admits`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			digest  string
			payload []byte
		)
		if err := rows.Scan(&digest, &payload); err != nil {
			return "", err
		}
		if r, err := xdealv1.Decode(payload); err == nil {
			if b, ok := r.Body.(*xdealv1.AdmitBody); ok && b.Nonce == nonce {
				return digest, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	j, err := s.shelf.Journal(ctx)
	if err != nil {
		return "", err
	}
	read, err := s.shelf.Records(ctx, evidencebook.JournalLogID)
	if err != nil {
		return "", err
	}
	for _, r := range read.Records {
		for _, c := range r.Header.PayloadCommitments {
			payload, err := j.ResolvePayload(ctx, r.RecordID, c)
			var gone *evidencebook.UnavailableError
			if errors.As(err, &gone) {
				// A withheld or deleted payload cannot be read and holds no
				// nonce this check can see; any other error is a failed read,
				// which must not pass the check.
				continue
			}
			if err != nil {
				return "", err
			}
			if rec, err := xdealv1.Decode(payload); err == nil {
				if b, ok := rec.Body.(*xdealv1.AdmitBody); ok && b.Nonce == nonce {
					return c, nil
				}
			}
		}
	}
	return "", nil
}

// sameAdmitBody reports whether two admit payloads hold the same body: a
// re-run seals at another time, so the blocks differ and only the bodies are
// compared.
func sameAdmitBody(a, b []byte) bool {
	ra, err := xdealv1.Decode(a)
	if err != nil {
		return false
	}
	rb, err := xdealv1.Decode(b)
	if err != nil {
		return false
	}
	ja, err := json.Marshal(ra.Body)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(rb.Body)
	return err == nil && bytes.Equal(ja, jb)
}

// dealAdmit is the admit `deal admit` bound to dealID, or "" when it bound
// none.
func (s *dealSession) dealAdmit(ctx context.Context, dealID string) (string, error) {
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT admit_digest FROM deal_v1_admits WHERE deal_id=?`, dealID).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return digest, err
}

// admitDealOf is the deal an admit was sealed for by `deal admit`, or "".
func (s *dealSession) admitDealOf(ctx context.Context, digest string) (string, error) {
	var dealID string
	err := s.db.QueryRowContext(ctx, `SELECT deal_id FROM deal_v1_admits WHERE admit_digest=?`, digest).Scan(&dealID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return dealID, err
}
