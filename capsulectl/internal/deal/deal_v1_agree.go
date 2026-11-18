package deal

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal agree` seals the holder's agreement, which opens its deal book, and
// `deal receive` takes what the other side sent (docs/design/
// xdealv1-write-path.md §4.3, §5, slice 2). The plugin has no transport:
// what leaves is a file, and what comes in is a file.

// v1Push is the file a stamped record travels in: the book record as a pair
// and the stamp proof that covers it (design §7.2). Either part may be absent.
type v1Push struct {
	Record     json.RawMessage          `json:"record,omitempty"`
	StampProof *evidencebook.StampProof `json:"stamp_proof,omitempty"`
}

func proposeOf(r xdealv1.Record) (*xdealv1.ProposeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.ProposeBody:
		return b, true
	case xdealv1.ProposeBody:
		return &b, true
	}
	return nil, false
}

func agreeOf(r xdealv1.Record) (*xdealv1.AgreeBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.AgreeBody:
		return b, true
	case xdealv1.AgreeBody:
		return &b, true
	}
	return nil, false
}

func oppositeRole(role string) string {
	if role == xdealv1.RoleBuyer {
		return xdealv1.RoleSeller
	}
	return xdealv1.RoleBuyer
}

// offerHeld is an offer the holder has: its record, its signer's key and
// whether the holder sent it.
type offerHeld struct {
	Record xdealv1.Record
	Signer string
	Own    bool
}

// ownKeyID is the key this profile's records are signed under, as the other
// side sees it (lowercase hex).
func (s *dealSession) ownKeyID() string {
	return hex.EncodeToString(s.key.Public().(ed25519.PublicKey))
}

// offerByDigest finds an offer by its record digest among the offers this
// profile sent and the ones it received.
func (s *dealSession) offerByDigest(ctx context.Context, digest string) (*offerHeld, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM deal_sent WHERE record_digest=? AND kind=?`, digest, xdealv1.TypePropose).Scan(&payload)
	own := true
	signer := s.ownKeyID()
	if errors.Is(err, sql.ErrNoRows) {
		own = false
		err = s.db.QueryRowContext(ctx, `SELECT payload, signer_key FROM deal_v1_offers WHERE offer_digest=?`, digest).Scan(&payload, &signer)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return nil, err
	}
	return &offerHeld{Record: rec, Signer: signer, Own: own}, nil
}

func dealAgreeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agree OFFER_DIGEST",
		Short: "Agree to an offer: seal your agreement, which opens your deal book, and write the file to send",
		Long: `Agree to an offer you sent or received (OFFER_DIGEST is the offer's record
digest): seal your agreement as the first record of your deal book and write
it to --out (standard output with none). --admit is the digest of your own
admission decision in your journal, which an agreement cites; --intent is the
digest of your journal's intent for the task, when you sealed one. The
agreement takes the offer's rows, witnesses, adjudicator and forums as they
are.

The acceptor stamps right after and the file carries its stamp proof: the
offerer does not act until it holds that proof. The offerer agrees only once
it holds the acceptor's agreement (deal receive), and stamps with its first
act. Needs the deal store in the new layout (deal set-version x-deal-v1
--migrate).`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			offerDigest := args[0]
			admit, _ := c.Flags().GetString("admit")
			intent, _ := c.Flags().GetString("intent")
			out, _ := c.Flags().GetString("out")
			if admit == "" {
				return cli.InputError("--admit is required: the digest of your journal's admission decision for this deal, which an agreement cites")
			}
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				cad, err := s.requireMigrated(ctx)
				if err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				offer, err := s.offerByDigest(ctx, offerDigest)
				if err != nil {
					return err
				}
				if offer == nil {
					return cli.InputError("this profile holds no offer with the digest " + offerDigest + ": `deal receive FILE` takes one")
				}
				prop, _ := proposeOf(offer.Record)
				dealID := offer.Record.Block.DealID
				d, err := s.v1DealOf(ctx, dealID)
				if err != nil {
					return err
				}
				if d == nil {
					return cli.InputError("deal " + dealID + " is not an x-deal-v1 deal this profile holds")
				}
				if d.State == "cancelled" || d.State == "declined" {
					return cli.InputError("this offer is " + d.State + ": there is no deal to agree to")
				}
				// An agreement already sealed is written again, never sealed twice.
				if d.OwnLogID != "" {
					if err := s.repairIfStopped(ctx, d); err != nil {
						return err
					}
				}
				if d.OwnAgreementID != "" {
					if _, err := s.db.ExecContext(ctx, `UPDATE deal_versions SET state='agreed' WHERE deal_id=? AND state='agreeing'`, dealID); err != nil {
						return err
					}
					return s.writeAgreement(ctx, c, cad, d, out)
				}
				for _, want := range []struct{ flag, digest, typ, what string }{
					{"--admit", admit, xdealv1.TypeAdmit, "an admission decision"},
					{"--intent", intent, xdealv1.TypeIntent, "an intent"},
				} {
					if want.digest == "" {
						continue
					}
					typ, held, err := s.journalRecordType(ctx, want.digest)
					if err != nil {
						return err
					}
					if !held {
						return cli.InputError(want.flag + " " + want.digest + " is not a record in your journal")
					}
					if typ == "" {
						return cli.InputError(want.flag + " " + want.digest + " is a record in your journal that is not an x-deal-v1 record, so not " + want.what)
					}
					if typ != want.typ {
						return cli.InputError(want.flag + " " + want.digest + " is a record of type " + typ + " in your journal, not " + want.what)
					}
				}
				side, role := xdealv1.SideOfferer, prop.Role
				cptyKey, heldAcceptor := d.CounterpartyKey, ""
				if !offer.Own {
					side, role = xdealv1.SideAcceptor, oppositeRole(prop.Role)
					cptyKey = offer.Signer
				} else if d.CounterpartyAgreement == "" {
					return cli.InputError("the acceptor's agreement has not been received: `deal receive FILE` takes it, and you agree after")
				} else {
					heldAcceptor = d.CounterpartyAgreement
				}
				rowsDigest, err := xdealv1.RowsDigest(prop.TermsRows)
				if err != nil {
					return err
				}
				ckey, err := s.checkpointKeyID()
				if err != nil {
					return err
				}
				_, cp, err := s.cadenceOf(ctx)
				if err != nil {
					return err
				}
				layer := true
				body := xdealv1.AgreeBody{
					Kind: xdealv1.KindAgreement, Offer: xdealv1.RefTo(offerDigest), RowsDigest: rowsDigest,
					Side: side, Role: role, Admit: refOf(admit), Intent: refOf(intent),
					Witnesses: prop.Witnesses, Adjudicator: prop.Adjudicator, Forums: forumsOf(prop),
					CounterpartyLayer: &layer, CounterpartyKey: xdealv1.Some(cptyKey),
					CheckpointKey: ckey, ShelfLogID: cp.LogID,
				}
				if _, err := s.db.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, '', '', '', '') ON CONFLICT(deal_id) DO NOTHING`, dealID, side, role, offerDigest); err != nil {
					return err
				}
				if _, err := s.db.ExecContext(ctx, `UPDATE deal_v1 SET side=?, role=?, counterparty_key=? WHERE deal_id=?`, side, role, cptyKey, dealID); err != nil {
					return err
				}
				d.Side, d.Role, d.CounterpartyKey = side, role, cptyKey
				// The writer fills the chain; the finished agreement is what is
				// checked against the offer it cites, before anything is stored.
				validate := func(rec xdealv1.Record) error {
					if err := xdealv1.CheckAgreement(rec, offer.Record, heldAcceptor); err != nil {
						return cli.InputError("the agreement does not pass the x-deal-v1 checks: " + err.Error())
					}
					return nil
				}
				if _, err := s.sealBookV1(ctx, dealID, bookRecordIn{Body: body, Seen: heldAcceptor, Validate: validate}); err != nil {
					return err
				}
				d, err = s.v1DealOf(ctx, dealID)
				if err != nil {
					return err
				}
				return s.writeAgreement(ctx, c, cad, d, out)
			})
		},
	}
	cmd.Flags().String("admit", "", "The digest of your journal's admission decision for this deal (required)")
	cmd.Flags().String("intent", "", "The digest of your journal's intent for the task, when you sealed one")
	cmd.Flags().String("out", "", "Where to write the file to send (default: standard output)")
	return cmd
}

func refOf(digest string) *xdealv1.Ref {
	if digest == "" {
		return nil
	}
	r := xdealv1.RefTo(digest)
	return &r
}

// forumsOf is the offer's forums as the agreement carries them: never nil
// (an agreement with a null adjudicator carries an empty list).
func forumsOf(p *xdealv1.ProposeBody) []string {
	if p.Forums == nil {
		return []string{}
	}
	return p.Forums
}

// repairIfStopped records an agreement a stopped run appended.
func (s *dealSession) repairIfStopped(ctx context.Context, d *v1Deal) error {
	if d.OwnAgreementID != "" {
		return nil
	}
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return err
	}
	if book.Size() == 0 {
		return nil
	}
	return s.repairOpening(ctx, d, book)
}

// writeAgreement writes the holder's agreement as the file to send: the
// acceptor's carries its stamp proof, taken right after the agreement (design
// §7.2); the offerer stamps after its first act.
func (s *dealSession) writeAgreement(ctx context.Context, c *cobra.Command, cad *evidencebook.Cadence, d *v1Deal, out string) error {
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return err
	}
	sealed, err := book.Sealed(ctx, d.OwnAgreementID)
	if err != nil {
		return err
	}
	payload, err := book.ResolvePayload(ctx, d.OwnAgreementID, d.OwnAgreementDigest)
	if err != nil {
		return err
	}
	pair, err := xdealv1.BookPair(sealed, payload)
	if err != nil {
		return err
	}
	push := v1Push{Record: pair}
	if d.Side == xdealv1.SideAcceptor {
		// The stamp is taken once, right after the agreement: a re-run reuses the
		// newest proof that covers it and stamps only when none exists yet.
		proof, err := cad.StampProof(ctx, d.OwnAgreementID, 0, evidencebook.AudienceCounterparty)
		if err != nil {
			if _, err := cad.Stamp(ctx, cli.DealClock()); err != nil {
				return err
			}
			if proof, err = cad.StampProof(ctx, d.OwnAgreementID, 0, evidencebook.AudienceCounterparty); err != nil {
				return err
			}
		}
		push.StampProof = &proof
	}
	data, err := json.Marshal(push)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if out == "" {
		_, err = c.OutOrStdout().Write(data)
		return err
	}
	if err := cli.AtomicFile(out, data, false); err != nil {
		// A file already there is the earlier run's only if it holds the same record.
		prev, rerr := os.ReadFile(out)
		var was v1Push
		if rerr != nil || json.Unmarshal(prev, &was) != nil || string(was.Record) != string(pair) {
			return err
		}
	}
	return cli.Output(c, map[string]any{"deal_id": d.DealID, "agreement": d.OwnAgreementDigest, "side": d.Side, "file": out, "stamp_proof": push.StampProof != nil})
}
