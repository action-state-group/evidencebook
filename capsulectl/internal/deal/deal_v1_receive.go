package deal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

func dealReceiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receive FILE",
		Short: "Take a file the other side sent: an offer, a decline, a cancellation, an agreement or a record, with its stamp proof",
		Long: `Take a file the other side sent and keep it in your deal store. FILE holds one
of: an offer, a decline or a cancellation (what 'deal offer' wrote), a deal-book
record such as an agreement, or a push {record, stamp_proof} (what 'deal agree'
writes for the acceptor), or a stamp proof alone. Each record is checked on its
own before it is kept, and a stamp proof against the records you hold.

It says in plain words what it did: which records are kept, whether one is
still missing before a counted one ("counted" means every record before it is
here, so it counts toward the deal), and whether two different records came
for one place (it does not say which is right). It seals nothing and appends
nothing to a book of yours, except that a ruling of one of your deals is entered
in your deal book by a statement naming it. --out then writes that statement as
the file to send to the other side, so the other side holds it and counts your
later records.

Needs the deal store in the new layout (deal set-version x-deal-v1 --migrate).`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			raw, err := cli.ReadInput(args[0])
			if err != nil {
				return err
			}
			var push v1Push
			var members map[string]json.RawMessage
			if err := json.Unmarshal(raw, &members); err != nil {
				return cli.InputError("the file is not a JSON object: " + err.Error())
			}
			_, hasProof := members["stamp_proof"]
			_, hasRecord := members["record"]
			bareBookPair := members["payload"] != nil && members["header"] != nil
			if !bareBookPair && (hasProof || (hasRecord && members["agent_input"] == nil)) {
				if err := cli.DecodeJSONAs("the file", raw, &push); err != nil {
					return err
				}
			} else {
				push.Record = raw
			}
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				if _, err := s.requireMigrated(ctx); err != nil {
					return err
				}
				if err := s.ensureV1Tables(ctx); err != nil {
					return err
				}
				return s.receiveV1(ctx, c, push)
			})
		},
	}
	cmd.Flags().String("out", "", "With a ruling: write the statement your book seals to name it, the file to send to the other side")
	return cmd
}

// receivedInfo is what the plugin keeps of a record it verified before the
// store took it.
type receivedInfo struct {
	kind    string
	dealID  string
	sent    *xdealv1.Sent
	book    *xdealv1.BookRead
	payload []byte
	// raw is the file as received; for a sent record it is the pair.
	raw []byte
}

// readReceived tells which form a record file is in and verifies it on its
// own: a sent journal record (an offer, a decline, a cancellation) or a
// deal-book record.
func (s *dealSession) readReceived(ctx context.Context, raw []byte) (*receivedInfo, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, cli.InputError("the record is not a JSON object: " + err.Error())
	}
	switch {
	case members["agent_input"] != nil:
		first, err := xdealv1.VerifySent(raw, nil)
		if err != nil {
			return nil, cli.InputError("the sent record does not verify: " + err.Error())
		}
		// A decline or a cancellation is read against the offer it names when
		// the holder has it: its signer and its digest.
		if ag, ok := agreeOf(first.Record); ok {
			held, err := s.offerByDigest(ctx, ag.Offer.Digest)
			if err != nil {
				return nil, err
			}
			if held != nil {
				if first, err = xdealv1.VerifySent(raw, &xdealv1.OfferContext{Offer: held.Record, Signer: held.Signer}); err != nil {
					return nil, cli.InputError("the sent record does not verify against the offer it answers: " + err.Error())
				}
			}
		}
		return &receivedInfo{kind: first.Record.Block.RecordType, dealID: first.Record.Block.DealID, sent: &first, raw: raw}, nil
	case members["payload"] != nil:
		read, err := xdealv1.VerifyBook(raw)
		if err != nil {
			return nil, cli.InputError("the deal-book record does not verify: " + err.Error())
		}
		return &receivedInfo{kind: read.Record.Block.RecordType, dealID: read.Record.Block.DealID, book: &read}, nil
	}
	return nil, cli.InputError("the file holds no record this profile reads: expected a sent record (agent_input) or a deal-book record (payload)")
}

func (s *dealSession) receiveV1(ctx context.Context, c *cobra.Command, push v1Push) error {
	var info *receivedInfo
	if len(push.Record) > 0 {
		var err error
		if info, err = s.readReceived(ctx, push.Record); err != nil {
			return err
		}
		if err = s.refuseOwnDeal(ctx, info); err != nil {
			return err
		}
		// An offer to a counterparty with no layer (to null) stays with its
		// offerer and is never sent (PROFILE §4.7): no one can answer it.
		if info.sent != nil && info.kind == xdealv1.TypePropose {
			if prop, ok := proposeOf(info.sent.Record); ok && prop.To.V == nil {
				return cli.InputError("this offer names no key (to is null): it is an offer to a party that keeps no records of its own, and it stays with whoever made it. Nothing can answer it, so it is not kept")
			}
		}
	}
	if info == nil && push.StampProof == nil {
		return cli.InputError("the file holds neither a record nor a stamp proof")
	}
	res, rerr := receiveFromStore(ctx, s.shelf, push.Record, push.StampProof)
	out := map[string]any{}
	if info != nil {
		out["record_type"], out["deal_id"] = info.kind, info.dealID
		// A ruling's record digest is what a statement names it by and what
		// deal dispute --challenge takes.
		if info.book != nil && info.kind == xdealv1.TypeRuling {
			if d, err := xdealv1.Digest(info.book.Record); err == nil {
				out["ruling"] = d
			}
		}
	}
	if res.Record != nil {
		out["record"] = recordWords(res.Record)
		if res.Record.Stored || res.Record.Duplicate {
			if err := s.noteReceived(ctx, info); err != nil {
				return errors.Join(rerr, err)
			}
		}
	}
	if res.Proof != nil {
		out["stamp_proof"] = proofWords(res.Proof)
	}
	if out["record"] == storeFault || out["stamp_proof"] == storeFault {
		_ = cli.Output(c, out)
		return errors.Join(rerr, fmt.Errorf("%w: %s", cli.ErrConflict, storeFault))
	}
	if rerr != nil {
		out["refused"] = rerr.Error()
		_ = cli.Output(c, out)
		return rerr
	}
	if path, _ := c.Flags().GetString("out"); path != "" {
		if info == nil || info.kind != xdealv1.TypeRuling {
			return cli.InputError("--out goes with a ruling: it writes the statement that names it")
		}
		if err := s.writeNaming(ctx, c, info, path, out); err != nil {
			return err
		}
	}
	return cli.Output(c, out)
}

// writeNaming writes the statement this holder's book holds naming the
// received ruling, as the file to send to the other side: without it the
// other side holds a gap at that place and counts none of the holder's
// later records.
func (s *dealSession) writeNaming(ctx context.Context, c *cobra.Command, info *receivedInfo, path string, out map[string]any) error {
	ruling, err := xdealv1.Digest(info.book.Record)
	if err != nil {
		return err
	}
	d, err := s.v1DealOf(ctx, info.dealID)
	if err != nil {
		return err
	}
	if d == nil || d.OwnAgreementID == "" {
		return cli.InputError("deal " + info.dealID + " has no book of yours, so the ruling is entered in none")
	}
	book, err := s.ownBookV1(ctx, d)
	if err != nil {
		return err
	}
	for i := len(book) - 1; i >= 0; i-- {
		st, ok := book[i].Body.(*xdealv1.StatementBody)
		if !ok || st.Names == nil || st.Names.Ruling == nil || st.Names.Ruling.Digest != ruling {
			continue
		}
		pair, err := s.bookPairAt(ctx, d, i+1)
		if err != nil {
			return err
		}
		data, err := json.Marshal(v1Push{Record: pair})
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := cli.AtomicFile(path, data, false); err != nil {
			prev, rerr := os.ReadFile(path)
			if rerr != nil || !bytes.Equal(prev, data) {
				return err
			}
		}
		out["naming_statement"], out["file"] = i+1, path
		return nil
	}
	return cli.InputError("your book holds no statement naming this ruling yet; run deal receive on it again")
}

// refuseOwnDeal refuses a record that claims a deal this profile made the
// offer of, from anyone but the other side's own book: an offer for a deal id
// this profile already holds is never taken over.
func (s *dealSession) refuseOwnDeal(ctx context.Context, info *receivedInfo) error {
	if info.sent == nil || info.kind != xdealv1.TypePropose {
		return nil
	}
	d, err := s.v1DealOf(ctx, info.dealID)
	if err != nil {
		return err
	}
	if d != nil {
		held, err := s.offerByDigest(ctx, info.sent.Digest)
		if err != nil {
			return err
		}
		if held == nil || held.Own {
			return cli.InputError("this profile already holds deal " + info.dealID + ": an offer for a deal id it holds is refused")
		}
	}
	return nil
}

// storeFault is what is said when the store returns an outcome that is none of
// the outcomes it defines: a fault, never a quiet "not kept" (Sherlock2, buzz
// 694e4902). The verb exits non-zero on it.
const storeFault = "not kept: the store returned no outcome, which is a fault; report it"

// receiveFromStore is Shelf.Receive; a test replaces it to make the store
// return an outcome with nothing set.
var receiveFromStore = func(ctx context.Context, shelf *evidencebook.Shelf, record []byte, proof *evidencebook.StampProof) (evidencebook.ReceiveResult, error) {
	return shelf.Receive(ctx, record, proof)
}

func recordWords(o *evidencebook.RecordOutcome) string {
	switch {
	case o.Refused != nil:
		return "refused: " + o.Refused.Error()
	case o.Duplicate:
		return "already kept"
	case o.Deleted:
		return "kept once, since deleted under your retention"
	case o.Stored && o.Counted:
		return "kept, and counted"
	case o.Stored && o.AskForBook:
		return "kept, but an earlier record from the other side is missing: ask them to send it again"
	case o.Stored:
		return "kept"
	}
	return storeFault
}

func proofWords(o *evidencebook.ProofOutcome) string {
	switch {
	case o.Refused != nil:
		return "refused: " + o.Refused.Error()
	case o.Duplicate:
		return "already kept"
	case o.Accepted:
		return "kept"
	}
	return storeFault
}

// noteReceived records what the plugin needs of a record the store took: the
// offer's payload to answer it, the deal's row, the other side's book and its
// agreement.
func (s *dealSession) noteReceived(ctx context.Context, info *receivedInfo) error {
	if info == nil {
		return nil
	}
	switch {
	case info.sent != nil && info.kind == xdealv1.TypePropose:
		prop, _ := proposeOf(info.sent.Record)
		payload, digest, err := xdealv1.Encode(info.sent.Record)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err = tx.ExecContext(ctx, `INSERT INTO deal_versions (deal_id, version, own_log_id, counterparty_log_id, state) VALUES (?, ?, '', '', 'offer_received') ON CONFLICT(deal_id) DO NOTHING`, info.dealID, dealVersionV1); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, ?, '', '', '') ON CONFLICT(deal_id) DO NOTHING`,
			info.dealID, xdealv1.SideAcceptor, oppositeRole(prop.Role), digest, info.sent.KeyID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO deal_v1_offers (offer_digest, deal_id, signer_key, payload, pair) VALUES (?, ?, ?, ?, ?) ON CONFLICT(offer_digest) DO UPDATE SET pair=excluded.pair WHERE deal_v1_offers.pair IS NULL`, digest, info.dealID, info.sent.KeyID, payload, info.raw); err != nil {
			return err
		}
		if err := v1NoteStops(); err != nil {
			return err
		}
		return tx.Commit()
	case info.sent != nil:
		state := "declined"
		if ag, ok := agreeOf(info.sent.Record); ok && ag.Kind == xdealv1.KindCancellation {
			state = "cancelled"
		}
		_, err := s.db.ExecContext(ctx, `UPDATE deal_versions SET state=? WHERE deal_id=? AND state IN ('offered','offer_received')`, state, info.dealID)
		return err
	case info.book != nil:
		d, err := s.v1DealOf(ctx, info.dealID)
		if err != nil || d == nil {
			return err
		}
		if err = s.ensureOfferedRow(ctx, d); err != nil {
			return err
		}
		// A deal with no layer on the other side has no counterparty: a
		// record received for it (a referee's ruling) is kept, and no one is
		// registered as the other side.
		if noLayer, err := s.v1NoLayer(ctx, d); err != nil || noLayer {
			return err
		}
		if d.CounterpartyLogID == "" {
			if _, err = s.db.ExecContext(ctx, `UPDATE deal_versions SET counterparty_log_id=? WHERE deal_id=?`, info.book.LogID, info.dealID); err != nil {
				return err
			}
		}
		if d.CounterpartyKey == "" || d.CounterpartyKey == info.book.KeyID {
			if _, err = s.db.ExecContext(ctx, `UPDATE deal_v1 SET counterparty_key=? WHERE deal_id=?`, info.book.KeyID, info.dealID); err != nil {
				return err
			}
		}
		if ag, ok := agreeOf(info.book.Record); ok && ag.Kind == xdealv1.KindAgreement && info.book.Seq == 1 && info.book.LogID == firstNonEmpty(d.CounterpartyLogID, info.book.LogID) {
			_, err = s.db.ExecContext(ctx, `UPDATE deal_v1 SET counterparty_agreement=? WHERE deal_id=? AND counterparty_agreement=''`, info.book.RecordID, info.dealID)
		}
		return err
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// v1NoteStops is a seam for tests: it stops the bookkeeping of a received offer
// after its rows were written and before they are committed, as a crash there
// would.
var v1NoteStops = func() error { return nil }

// ensureOfferedRow gives an offer this profile sent before the deal's own row
// existed (slice 1 wrote only deal_versions) the row `deal agree` and the
// receive bookkeeping update, rebuilt from the stored offer.
func (s *dealSession) ensureOfferedRow(ctx context.Context, d *v1Deal) error {
	if d.Side != "" {
		return nil
	}
	var payload []byte
	var digest string
	err := s.db.QueryRowContext(ctx, `SELECT payload, record_digest FROM deal_sent WHERE deal_id=? AND kind=? ORDER BY id LIMIT 1`, d.DealID, xdealv1.TypePropose).Scan(&payload, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rec, err := xdealv1.Decode(payload)
	if err != nil {
		return err
	}
	prop, ok := proposeOf(rec)
	if !ok {
		return nil
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO deal_v1 (deal_id, side, role, offer_digest, counterparty_key, counterparty_agreement, own_agreement_id, own_agreement_digest) VALUES (?, ?, ?, ?, '', '', '', '') ON CONFLICT(deal_id) DO NOTHING`,
		d.DealID, xdealv1.SideOfferer, prop.Role, digest); err != nil {
		return err
	}
	d.Side, d.Role, d.OfferDigest = xdealv1.SideOfferer, prop.Role, digest
	return nil
}
