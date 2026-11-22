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
	"strconv"
	"strings"

	aacsettlement "github.com/action-state-group/agent-action-capsule/go/settlement"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal settle` on an x-deal-v1 deal (xdealv1-write-path §13 items 3, 4, 5
// and 9; evidencebook#626 B2 part 2). A settle record is a payment leg: the
// holder's payer leg (a payment on its pay check, or a refund on its cancel
// check) or its payee leg (what it received). The leg itself travels as the
// Capsule's settlement member (draft-mih-agent-settlement-records-01), built
// here from the deal, never from free input.
//
// A payer leg is sealed in two calls (Sherlock2, buzz 037352f4 and 44899e76).
// `deal settle --sent` runs before the agent hands the payment to the rail:
// the gate, the authorization and the deal's checks run on the candidate leg,
// then a local act event that counts the amount from that moment and a
// journal evidence record "payment sent" are stored, and the send is open.
// `deal settle --status settled|failed` runs when the rail answers and seals
// the one payer leg, bound to the send. While the rail says pending, nothing
// more is sealed, and no record that changes the deal's details is sealed
// either (sealV1Step), so the payment already sent stays authorized.

// dealV1SendsTable holds each payer leg's send: the check it rests on, the
// local act event that counts it, what the send bound (amount, currency,
// reference), and the payer leg that closed it ("" while open).
const dealV1SendsTable = `CREATE TABLE IF NOT EXISTS deal_v1_sends (
	deal_id TEXT NOT NULL,
	check_digest TEXT NOT NULL,
	event_n INTEGER NOT NULL,
	amount_minor INTEGER NOT NULL,
	currency TEXT NOT NULL,
	ref_type TEXT NOT NULL,
	ref_value TEXT NOT NULL,
	leg_digest TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (deal_id, check_digest)
)`

// The statuses an observed leg takes here (AAC's statuses, less reversed:
// a refund is its own leg).
const (
	legPending = "pending"
	legSettled = "settled"
	legFailed  = "failed"
)

// v1Send is a row of deal_v1_sends.
type v1Send struct {
	DealID, Check          string
	N, Amount              int64
	Currency, RefType, Ref string
	Leg                    string
}

func (s *dealSession) sendOf(ctx context.Context, dealID, check string) (*v1Send, error) {
	v := v1Send{DealID: dealID, Check: check}
	err := s.db.QueryRowContext(ctx, `SELECT event_n, amount_minor, currency, ref_type, ref_value, leg_digest FROM deal_v1_sends WHERE deal_id=? AND check_digest=?`, dealID, check).
		Scan(&v.N, &v.Amount, &v.Currency, &v.RefType, &v.Ref, &v.Leg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// openSends are the holder's open sends of a deal, or of every deal when
// dealID is "".
func (s *dealSession) openSends(ctx context.Context, dealID string) ([]v1Send, error) {
	q, args := `SELECT deal_id, check_digest, event_n, amount_minor, currency, ref_type, ref_value FROM deal_v1_sends WHERE leg_digest=''`, []any{}
	if dealID != "" {
		q, args = q+` AND deal_id=?`, append(args, dealID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY deal_id, event_n`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []v1Send
	for rows.Next() {
		var v v1Send
		if err := rows.Scan(&v.DealID, &v.Check, &v.N, &v.Amount, &v.Currency, &v.RefType, &v.Ref); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// refuseWhileInFlight refuses a journal record that changes the deal's details
// while one of its payments is in flight: the payer leg's authorization would
// refuse it (xdealv1.ChangesDetails, the library's own test).
func (s *dealSession) refuseWhileInFlight(ctx context.Context, dealID string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	rec, err := xdealv1.Decode(payload)
	if err != nil || !xdealv1.ChangesDetails(rec) {
		return err
	}
	open, err := s.openSends(ctx, dealID)
	if err != nil || len(open) == 0 {
		return err
	}
	return cli.InputError("a payment of deal " + dealID + " is in flight (check " + open[0].Check + "): a " + rec.Block.RecordType +
		" record that changes the deal's details would void its authorization, so it waits until the payment is settled or failed (`deal settle --check " + open[0].Check + " --status ...`)")
}

// equalRefTypes are the payment reference types whose value is fixed before
// paying and is the same identifier the payment settles under, so a payer
// leg's reference equals its check's (§13 item 4).
var equalRefTypes = map[string]bool{"ln.payment_hash": true, "bolt12.invoice_payment_hash": true, "acp.order_id": true, "ucp.order_id": true}

// paymentRefFlags reads --payment-ref-type, --payment-ref and the type's
// qualifiers into AAC's flat payment_ref (settlement-records-01: qualifiers at
// the top level, each required, exactly).
type paymentRefFlags struct {
	Type, Value, Network, Method, DebtorAgent string
}

func (p paymentRefFlags) member() (map[string]any, error) {
	t, ok := aacsettlement.PaymentRefTypes[p.Type]
	if !ok {
		names := make([]string, 0, len(aacsettlement.PaymentRefTypes))
		for n := range aacsettlement.PaymentRefTypes {
			names = append(names, n)
		}
		slices.Sort(names)
		return nil, cli.InputError("--payment-ref-type must be one of " + strings.Join(names, ", "))
	}
	if p.Value == "" {
		return nil, cli.InputError("--payment-ref is required: the payment's reference on the rail")
	}
	ref := map[string]any{"type": p.Type, "value": p.Value}
	given := map[string]string{"network": p.Network, "method": p.Method, "debtor_agent": p.DebtorAgent}
	for name, v := range given {
		wanted := slices.Contains(t.Qualifiers, name)
		switch {
		case wanted && v == "":
			return nil, cli.InputError("--payment-ref-" + strings.ReplaceAll(name, "_", "-") + " is required for " + p.Type)
		case !wanted && v != "":
			return nil, cli.InputError("--payment-ref-" + strings.ReplaceAll(name, "_", "-") + " does not go with " + p.Type)
		case wanted:
			ref[name] = v
		}
	}
	return ref, nil
}

// legAmountOf is minor units of a currency as an AAC amount.
func legAmountOf(minor int64, currency string) (map[string]any, error) {
	scale, ok := dealprofile.AssetScale(currency)
	if !ok {
		return nil, cli.InputError("currency " + currency + " has no known minor units")
	}
	if minor < 0 {
		return nil, cli.InputError("--received, --receive-fee and --routing-fee are amounts in minor units and are never negative")
	}
	return map[string]any{"value": strconv.FormatInt(minor, 10), "assetCode": currency, "assetScale": scale}, nil
}

// settleMember builds a leg's settlement member and refuses one AAC's
// structure check fails.
func settleMember(m map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	if f := aacsettlement.StructureFailures(generic); len(f) > 0 {
		return nil, cli.InputError("the settlement leg fails AAC's structure check: " + strings.Join(f, ", "))
	}
	return raw, nil
}

// offerPair is the deal's offer as sent, its pair: the offerer's own from
// deal_sent, the acceptor's as received.
func (s *dealSession) offerPair(ctx context.Context, d *v1Deal) ([]byte, error) {
	var pair []byte
	err := s.db.QueryRowContext(ctx, `SELECT pair FROM deal_sent WHERE record_digest=? AND kind=?`, d.OfferDigest, xdealv1.TypePropose).Scan(&pair)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(pair) == 0) {
		err = s.db.QueryRowContext(ctx, `SELECT pair FROM deal_v1_offers WHERE offer_digest=?`, d.OfferDigest).Scan(&pair)
	}
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(pair) == 0) {
		return nil, cli.InputError("this profile does not hold the offer of deal " + d.DealID + " as sent, which a leg names as its terms")
	}
	return pair, err
}

// termsRef is the Capsule id of the deal's offer as sent, which carries the
// terms leg every observed leg names (§13 item 3).
func (s *dealSession) termsRef(ctx context.Context, d *v1Deal) (string, error) {
	pair, err := s.offerPair(ctx, d)
	if err != nil {
		return "", err
	}
	sent, err := xdealv1.VerifySent(pair, nil)
	if err != nil {
		return "", err
	}
	return sent.CapsuleID, nil
}

// paymentRow is the row a leg settles: --row, or the offer's one payment row
// (a row whose owed carries an amount).
func (a *v1ActDeal) paymentRow(id string) (*xdealv1.Row, string, error) {
	if id != "" {
		r, err := a.row(id)
		if err != nil {
			return nil, "", err
		}
		cur, ok := owedCurrency(r)
		if !ok {
			return nil, "", cli.InputError("row " + id + " owes no amount: a leg settles a payment row")
		}
		return r, cur, nil
	}
	var found *xdealv1.Row
	var cur string
	for i := range a.offer.TermsRows.Rows {
		r := &a.offer.TermsRows.Rows[i]
		if c, ok := owedCurrency(r); ok {
			if found != nil {
				return nil, "", cli.InputError("the offer has more than one payment row: name it with --row")
			}
			found, cur = r, c
		}
	}
	if found == nil {
		return nil, "", cli.InputError("the offer of deal " + a.d.DealID + " has no payment row")
	}
	return found, cur, nil
}

func owedCurrency(r *xdealv1.Row) (string, bool) {
	var owed struct {
		AmountMinor *int64 `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if json.Unmarshal(r.Owed, &owed) != nil || owed.AmountMinor == nil || owed.Currency == "" {
		return "", false
	}
	return owed.Currency, true
}

// settleOf is a settle record's body.
func settleOf(r xdealv1.Record) (*xdealv1.SettleBody, bool) {
	switch b := r.Body.(type) {
	case *xdealv1.SettleBody:
		return b, true
	case xdealv1.SettleBody:
		return &b, true
	}
	return nil, false
}

// legFor is the leg the holder seals (§13 item 3): payer when its party owes
// the row; with reverses, the mirror of the leg it reverses, which must be a
// settle of the holder's own book that no other refund reverses (unless this
// leg supersedes that refund's earlier observation).
func (a *v1ActDeal) legFor(row *xdealv1.Row, reverses, supersedes string) (string, error) {
	if reverses == "" {
		if row.OwedBy == a.d.Role {
			return xdealv1.LegPayer, nil
		}
		return xdealv1.LegPayee, nil
	}
	var target *xdealv1.SettleBody
	for _, r := range a.book {
		b, ok := settleOf(r)
		if !ok {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return "", err
		}
		if d == reverses {
			target = b
		}
	}
	if target == nil {
		return "", cli.InputError("--reverses must name a settle record of your own deal book: " + reverses + " is not one")
	}
	// A later observation of the same refund supersedes it (checked against
	// that refund's reverses by settlePayee); a second, independent refund of
	// the same leg is refused.
	for _, r := range a.book {
		if b, ok := settleOf(r); ok && supersedes == "" && b.Reverses != nil && b.Reverses.Digest == reverses && b.Supersedes == nil {
			return "", cli.InputError("leg " + reverses + " is already refunded: a later observation of that refund names it with --supersedes")
		}
	}
	if target.Leg == xdealv1.LegPayer {
		return xdealv1.LegPayee, nil
	}
	return xdealv1.LegPayer, nil
}

// checkRecord is the deal's journal check of digest, with its body.
func (s *dealSession) checkRecord(ctx context.Context, dealID, digest string) (*xdealv1.CheckBody, error) {
	journal, err := s.v1DealJournal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	for _, r := range journal {
		if r.Block.RecordType != xdealv1.TypeCheck {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return nil, err
		}
		if d != digest {
			continue
		}
		switch b := r.Body.(type) {
		case *xdealv1.CheckBody:
			return b, nil
		case xdealv1.CheckBody:
			return &b, nil
		}
	}
	return nil, cli.InputError("--check " + digest + " is not a check of deal " + dealID + " in your journal")
}

// settleIn is what `deal settle` reads from its flags.
type settleIn struct {
	dealID, check, status, row, reverses, supersedes, out string
	sent                                                  bool
	ref                                                   paymentRefFlags
	routingFee, received, receiveFee                      int64
	hasRoutingFee, hasReceived, hasReceiveFee             bool
	statements, marks                                     []string
	remedyRow                                             string
}

const settleLong = `Seal a payment leg in your deal book of an x-deal-v1 deal: what you paid, a
refund you paid, or what you received, and write the file to send to --out.
The other side counts it with deal receive. The leg (payer or payee), its terms
and its amount come from the deal and your check, never from free input.

A payment you make takes two calls. Before you hand it to the rail, run
deal settle --sent --check DIGEST (your passing pay check, or your cancel check
for a refund): the first-act rule, the check's approval and your limits are
checked first, so a payment they would refuse never leaves. From then the
amount counts toward your limits, and no record that changes the deal's
details can be sealed until the payment is settled or failed. When the rail
answers, run deal settle --check DIGEST --status settled (or failed) --out FILE.
While the rail still says pending, run nothing more: the payment stays in
flight, and a second --sent on the check names it.

--payment-ref-type names the rail's reference type and --payment-ref its value.
On ln.payment_hash, bolt12.invoice_payment_hash, acp.order_id and ucp.order_id
the payer leg's value is the check's payment_ref and may be left out. On x402
the leg carries the transaction (--payment-ref, with --payment-ref-network);
the link from the transaction to the checked nonce is the payer's claim unless
a verifier reads the transaction's AuthorizationUsed nonce on chain.

What you received is a payee leg: --received and --receive-fee (0 when none was
taken) in minor units, --status pending, settled or failed, and the payment's
reference. A later observation of it names the earlier one with --supersedes.
--reverses names a leg of your own book that this leg refunds. A refund's legs
are shown, and the payment row's check is not run for them.

--statement row=value says how a row went; --mark and --remedy-row are as for
deal perform. Run again with the same flags, it rewrites the same file and
seals nothing new.`

func dealSettleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settle",
		Short: "Seal a payment leg of an x-deal-v1 deal (a payment, a refund or what you received) and write the file to send",
		Long:  settleLong,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			var in settleIn
			f := c.Flags()
			in.dealID, _ = f.GetString("deal")
			in.check, _ = f.GetString("check")
			in.status, _ = f.GetString("status")
			in.row, _ = f.GetString("row")
			in.reverses, _ = f.GetString("reverses")
			in.supersedes, _ = f.GetString("supersedes")
			in.out, _ = f.GetString("out")
			in.sent, _ = f.GetBool("sent")
			in.ref.Type, _ = f.GetString("payment-ref-type")
			in.ref.Value, _ = f.GetString("payment-ref")
			in.ref.Network, _ = f.GetString("payment-ref-network")
			in.ref.Method, _ = f.GetString("payment-ref-method")
			in.ref.DebtorAgent, _ = f.GetString("payment-ref-debtor-agent")
			in.routingFee, _ = f.GetInt64("routing-fee")
			in.received, _ = f.GetInt64("received")
			in.receiveFee, _ = f.GetInt64("receive-fee")
			in.hasRoutingFee, in.hasReceived, in.hasReceiveFee = f.Changed("routing-fee"), f.Changed("received"), f.Changed("receive-fee")
			in.statements, _ = f.GetStringArray("statement")
			in.marks, _ = f.GetStringArray("mark")
			in.remedyRow, _ = f.GetString("remedy-row")
			if !cli.DealIDPattern.MatchString(in.dealID) {
				return cli.InputError("--deal must be a deal id as printed by `deal offer`: deal- followed by 16 hex characters")
			}
			for name, v := range map[string]string{"--check": in.check, "--reverses": in.reverses, "--supersedes": in.supersedes} {
				if v != "" && !cli.Hex64.MatchString(v) {
					return cli.InputError(name + " must be a record digest: 64 lowercase hex characters")
				}
			}
			payer := in.check != ""
			switch {
			case in.sent && !payer:
				return cli.InputError("--sent goes with --check: the check the payment rests on")
			case in.sent && (in.status != "" || in.out != ""):
				return cli.InputError("--sent seals no leg and writes no file: run it before the handoff, then --status when the rail answers")
			case payer && !in.sent && in.status != legSettled && in.status != legFailed:
				return cli.InputError("--status settled or failed, when the rail answers (run --sent before the handoff); while the rail says pending, run nothing more")
			case payer && (in.hasReceived || in.hasReceiveFee || in.supersedes != ""):
				return cli.InputError("--received, --receive-fee and --supersedes go with what you received, which cites no --check")
			case !payer && in.hasRoutingFee:
				return cli.InputError("--routing-fee goes with a payment you make (--check)")
			case !payer && !slices.Contains([]string{legPending, legSettled, legFailed}, in.status):
				return cli.InputError("--status is pending, settled or failed")
			case !payer && (!in.hasReceived || !in.hasReceiveFee):
				return cli.InputError("--received and --receive-fee are required on what you received, in minor units (--receive-fee 0 when none was taken)")
			case !in.sent && in.out == "":
				return cli.InputError("--out is required: the file to send")
			}
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				return s.settle(ctx, c, in)
			})
		},
	}
	f := cmd.Flags()
	f.String("deal", "", "The x-deal-v1 deal (required)")
	f.String("check", "", "Your passing pay check (or cancel check, for a refund) that a payment rests on")
	f.Bool("sent", false, "Before the handoff: check the payment and count it as in flight; seals no leg")
	f.String("status", "", "settled or failed for a payment when the rail answers; pending, settled or failed for what you received")
	f.String("row", "", "The payment row (default: the offer's only one)")
	f.String("reverses", "", "A leg of your own book that this leg refunds")
	f.String("supersedes", "", "An earlier leg of what you received that this observation replaces")
	f.String("payment-ref-type", "", "The rail's reference type, such as ln.payment_hash or x402.transaction")
	f.String("payment-ref", "", "The payment's reference on the rail")
	f.String("payment-ref-network", "", "The network, for x402.transaction")
	f.String("payment-ref-method", "", "The method, for mpp.reference")
	f.String("payment-ref-debtor-agent", "", "The debtor agent, for iso20022.end_to_end_id")
	f.Int64("routing-fee", 0, "A routing fee you paid, in minor units")
	f.Int64("received", 0, "What you received, in minor units, net of any receive fee")
	f.Int64("receive-fee", 0, "A fee taken from what you received, in minor units (0 when none)")
	f.StringArray("statement", nil, "row=value[:remedy], how a row went (repeatable)")
	f.StringArray("mark", nil, "A named record of the offer that this leg is (repeatable)")
	f.String("remedy-row", "", "Make the leg a remedy for this row")
	f.String("out", "", "Where to write the file to send (required with --status)")
	return cmd
}

func (s *dealSession) settle(ctx context.Context, c *cobra.Command, in settleIn) error {
	a, err := s.loadV1ActDeal(ctx, in.dealID)
	if err != nil {
		return err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return err
	}
	row, currency, err := a.paymentRow(in.row)
	if err != nil {
		return err
	}
	leg, err := a.legFor(row, in.reverses, in.supersedes)
	if err != nil {
		return err
	}
	stmts, err := a.parseStatements(in.statements)
	if err != nil {
		return err
	}
	marks, err := a.parseMarks(in.marks)
	if err != nil {
		return err
	}
	body := xdealv1.SettleBody{Leg: leg, Statements: stmts}
	if in.reverses != "" {
		r := xdealv1.RefTo(in.reverses)
		body.Reverses = &r
	}
	if in.remedyRow != "" {
		if _, err := a.row(in.remedyRow); err != nil {
			return err
		}
		body.RemedyFor = &xdealv1.RemedyFor{Row: in.remedyRow}
	}
	events, err := s.reconcileV1Acts(ctx, in.dealID, a.book)
	if err != nil {
		return err
	}
	termsRef, err := s.termsRef(ctx, a.d)
	if err != nil {
		return err
	}
	if in.check == "" {
		return s.settlePayee(ctx, c, a, in, body, marks, currency, termsRef, events)
	}
	if leg != xdealv1.LegPayer {
		return cli.InputError("you do not owe row " + row.ID + " of deal " + in.dealID + ": what you received is a payee leg, which cites no --check")
	}
	cb, err := s.checkRecord(ctx, in.dealID, in.check)
	if err != nil {
		return err
	}
	switch {
	case in.reverses == "" && cb.Action != "pay":
		return cli.InputError("a payment rests on a pay check, and --check " + in.check + " is a " + cb.Action + " check")
	case in.reverses != "" && cb.Action != "cancel":
		return cli.InputError("a refund rests on a cancel check, and --check " + in.check + " is a " + cb.Action + " check")
	}
	// A seller's cancel check records the refund as cancelled_amount_minor
	// (sealCancelAmount): the refund is that amount.
	if cb.AmountMinor == nil && cb.Action == "cancel" && cb.CancelledAmountMinor != nil {
		amount := *cb.CancelledAmountMinor
		cb.AmountMinor = &amount
	}
	if cb.AmountMinor == nil {
		return cli.InputError("--check " + in.check + " records no amount: a payment rests on a check of its amount")
	}
	if cb.Currency != "" {
		currency = cb.Currency
	}
	ref := xdealv1.RefTo(in.check)
	body.Check = &ref
	if in.sent {
		return s.settleSent(ctx, c, a, in, body, marks, cb, currency, termsRef, events)
	}
	return s.settleAnswered(ctx, c, a, in, body, marks, cb, currency, termsRef)
}

// payerMember is a payer leg's settlement member.
func payerMember(termsRef string, amount int64, currency string, ref map[string]any, status string, routingFee *int64) (json.RawMessage, error) {
	amt, err := legAmountOf(amount, currency)
	if err != nil {
		return nil, err
	}
	m := map[string]any{"version": aacsettlement.Version, "leg": "payer_observed", "sealer_role": "payer", "terms_ref": termsRef,
		"amount": amt, "payment_ref": ref, "status": status, "observed_at": v1Now()}
	if routingFee != nil {
		fee, err := legAmountOf(*routingFee, currency)
		if err != nil {
			return nil, err
		}
		m["routing_fee"] = fee
	}
	return settleMember(m)
}

// sendRef is the payment reference a send binds: the check's on an equal type,
// else the flags'.
func sendRef(in settleIn, cb *xdealv1.CheckBody) (paymentRefFlags, error) {
	p := in.ref
	if cb.Action != "pay" {
		// A refund rests on a cancel check, which records no payment_ref: its
		// reference is the refund's own.
		return p, nil
	}
	if equalRefTypes[p.Type] {
		if p.Value == "" {
			p.Value = cb.PaymentRef
		}
		if p.Value != cb.PaymentRef {
			return p, cli.InputError("on " + p.Type + " the payment's reference is the check's payment_ref (" + cb.PaymentRef + "), and --payment-ref differs")
		}
	}
	if p.Value == "" {
		// On x402 and the other types the send binds the reference known
		// before paying, the check's (the x402 authorization nonce).
		p.Value = cb.PaymentRef
	}
	return p, nil
}

func (s *dealSession) settleSent(ctx context.Context, c *cobra.Command, a *v1ActDeal, in settleIn, body xdealv1.SettleBody, marks []string, cb *xdealv1.CheckBody, currency, termsRef string, events []sealedEvent) error {
	if prev, err := s.sendOf(ctx, in.dealID, in.check); err != nil {
		return err
	} else if prev != nil {
		return cli.InputError("a payment on check " + in.check + " is in flight (" + strconv.FormatInt(prev.Amount, 10) + " " + prev.Currency + ", " + prev.RefType + " " + prev.Ref + "): run deal settle --check " + in.check + " --status settled or failed when the rail answers")
	}
	for _, r := range v1Acts(a.book) {
		if b, ok := settleOf(r); ok && b.Check != nil && b.Check.Digest == in.check {
			return cli.InputError("check " + in.check + " already rests a payer leg: a check authorizes one payment")
		}
	}
	p, err := sendRef(in, cb)
	if err != nil {
		return err
	}
	refMember, err := p.member()
	if err != nil {
		return err
	}
	member, err := payerMember(termsRef, *cb.AmountMinor, currency, refMember, legSettled, nil)
	if err != nil {
		return err
	}
	// The candidate leg is checked as `--status` will seal it: the gate, the
	// authorization and rule 5's amount, so a payment they would refuse never
	// reaches the rail.
	cand := xdealv1.Record{Block: xdealv1.Block{Profile: xdealv1.Profile, Canonicalization: xdealv1.Canonicalization, DealID: in.dealID,
		RecordType: xdealv1.TypeSettle, At: v1Now(), Seq: int64(len(a.book)) + 1, Marks: marks}, Body: &body}
	gate, err := s.gateFirstAct(ctx, in.dealID, cand, "")
	if err != nil {
		return err
	}
	if gate.Gated && !gate.Allowed {
		return cli.InputError("your first act on deal " + in.dealID + " waits on the other side's stamp proof: " + gate.Reason)
	}
	auth, err := s.authorizeV1ActWith(ctx, in.dealID, v1Acts(a.book), cand, member)
	if err != nil {
		return err
	}
	// The local act event counts the amount from the send on; its record is the
	// journal's "payment sent" evidence, which names the deal's intent and
	// commits to what the send bound.
	act := dealAct{Action: cb.Action, AmountMinor: cb.AmountMinor, Currency: currency, Reference: p.Value}
	if cb.Action == "pay" {
		act.Direction = "out"
	}
	if auth != nil {
		ad, err := xdealv1.Digest(auth.Approval)
		if err != nil {
			return err
		}
		if act.AuthorizedBy = v1EventByDigest(events, ad); act.AuthorizedBy == "" {
			return fmt.Errorf("%w: the approval %s that authorized the payment has no local step", cli.ErrConflict, ad)
		}
	}
	ev, err := s.fixStep(in.dealID, events, dealEvent{Kind: "act", Act: &act})
	if err != nil {
		return err
	}
	payload, digest, err := s.sentEvidence(in.dealID, events, ev, in.check, *cb.AmountMinor, currency, p)
	if err != nil {
		return err
	}
	local, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := s.storeSend(ctx, in.dealID, ev.N, local, digest, payload, v1Send{Check: in.check, Amount: *cb.AmountMinor, Currency: currency, RefType: p.Type, Ref: p.Value}); err != nil {
		return err
	}
	result := map[string]any{"deal_id": in.dealID, "sent": digest, "check": in.check, "amount_minor": *cb.AmountMinor, "currency": currency,
		"next": "hand the payment to the rail; when it answers, run deal settle --deal " + in.dealID + " --check " + in.check + " --status settled|failed --out FILE"}
	actResult(result, &gate, auth)
	return cli.Output(c, result)
}

// sentEvidence is the journal "payment sent" evidence: about the deal's intent,
// carrying no counterparty fields (so it changes no details), with a
// detail_commitment over what the send bound, opened by the event's nonce.
func (s *dealSession) sentEvidence(dealID string, events []sealedEvent, ev dealEvent, check string, amount int64, currency string, p paymentRefFlags) ([]byte, string, error) {
	if len(events) < 2 {
		return nil, "", cli.InputError("deal " + dealID + " has no intent yet")
	}
	text, err := json.Marshal(map[string]any{"check": check, "amount_minor": amount, "currency": currency, "ref_type": p.Type, "ref": p.Value})
	if err != nil {
		return nil, "", err
	}
	nonce, err := xdealv1.NewNonce()
	if err != nil {
		return nil, "", err
	}
	commitment, err := dealprofile.CommitText(nonce, string(text))
	if err != nil {
		return nil, "", err
	}
	if ev.Nonces == nil {
		ev.Nonces = map[string]string{}
	}
	ev.Nonces["sent"] = nonce
	rec := map[string]any{
		xdealv1.Profile: map[string]any{"profile": xdealv1.Profile, "canonicalization": xdealv1.Canonicalization, "record_type": xdealv1.TypeEvidence,
			"at": ev.At, "deal_id": dealID, "producer": ev.Producer, "commit_alg": dealprofile.CommitAlg,
			"refs": []any{map[string]any{"rel": "about", "type": xdealv1.RecordRef, "digest_alg": xdealv1.DigestAlg, "digest": events[1].Digest}}},
		"body": map[string]any{"source": "payment_sent", "verified": false, "detail_commitment": commitment},
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, "", err
	}
	if err := xdealv1.CheckSealed(raw, xdealv1.SealContext{OfferExists: true}); err != nil {
		return nil, "", cli.InputError("the payment-sent evidence does not pass the profile's checks: " + err.Error())
	}
	return xdealv1.Encode(raw)
}

// storeSend stores the send's local act event and its row in one transaction,
// then appends the evidence; a failed append withdraws both unless the record
// was stored, as storeV1 does.
func (s *dealSession) storeSend(ctx context.Context, dealID string, n int64, local []byte, digest string, payload []byte, v v1Send) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_events (deal_id, n, kind, local, record_digest, payload) VALUES (?, ?, 'act', ?, ?, ?)`, dealID, n, string(local), digest, payload); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_sends (deal_id, check_digest, event_n, amount_minor, currency, ref_type, ref_value) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		dealID, v.Check, n, v.Amount, v.Currency, v.RefType, v.Ref); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.appendV1Journal(ctx, payload); err != nil {
		held, herr := s.journalHolds(ctx, digest)
		if herr != nil || held {
			return errors.Join(err, herr)
		}
		_, derr := s.db.ExecContext(ctx, `DELETE FROM deal_v1_sends WHERE deal_id=? AND check_digest=?`, dealID, v.Check)
		_, eerr := s.db.ExecContext(ctx, `DELETE FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, n)
		return errors.Join(err, derr, eerr)
	}
	return s.v1Appended(ctx, dealID, digest)
}

func (s *dealSession) settleAnswered(ctx context.Context, c *cobra.Command, a *v1ActDeal, in settleIn, body xdealv1.SettleBody, marks []string, cb *xdealv1.CheckBody, currency, termsRef string) error {
	send, err := s.sendOf(ctx, in.dealID, in.check)
	if err != nil {
		return err
	}
	if send == nil {
		return cli.InputError("no payment on check " + in.check + " was sent: run deal settle --deal " + in.dealID + " --check " + in.check + " --sent before the handoff")
	}
	result := map[string]any{"deal_id": in.dealID, "record": xdealv1.TypeSettle, "leg": xdealv1.LegPayer}
	if send.Leg != "" {
		for i, r := range a.book {
			if d, err := xdealv1.Digest(r); err == nil && d == send.Leg {
				return s.writeSealedAt(ctx, c, a.d, i+1, r, in.out, result)
			}
		}
		return fmt.Errorf("%w: the payer leg %s of check %s is not in your deal book", cli.ErrConflict, send.Leg, in.check)
	}
	if err := refuseExistingOut(in.out); err != nil {
		return err
	}
	p := in.ref
	if p.Type == "" {
		p.Type = send.RefType
	}
	if p.Type != send.RefType {
		return cli.InputError("the payment was sent under " + send.RefType + ", and --payment-ref-type is " + p.Type)
	}
	if equalRefTypes[p.Type] || p.Value == "" {
		if p.Value != "" && p.Value != send.Ref {
			return cli.InputError("on " + p.Type + " the leg's reference is the one the payment was sent under (" + send.Ref + "), and --payment-ref differs")
		}
		p.Value = send.Ref
	}
	refMember, err := p.member()
	if err != nil {
		return err
	}
	var routing *int64
	if in.hasRoutingFee {
		routing = &in.routingFee
	}
	if send.Amount != *cb.AmountMinor || send.Currency != currency {
		return fmt.Errorf("%w: the payment was sent as %d %s, and check %s records %d %s", cli.ErrConflict, send.Amount, send.Currency, in.check, *cb.AmountMinor, currency)
	}
	member, err := payerMember(termsRef, send.Amount, currency, refMember, in.status, routing)
	if err != nil {
		return err
	}
	earlier := v1Acts(a.book)
	var gate xdealv1.ActGate
	var auth *xdealv1.ActAuthorization
	validate := func(rec xdealv1.Record) error {
		g, err := s.gateFirstAct(ctx, in.dealID, rec, "")
		if err != nil {
			return err
		}
		if g.Gated && !g.Allowed {
			return cli.InputError("your first act on deal " + in.dealID + " waits on the other side's stamp proof: " + g.Reason)
		}
		gate = g
		auth, err = s.authorizeV1ActWith(ctx, in.dealID, earlier, rec, member)
		return err
	}
	sealed, err := s.sealBookV1(ctx, in.dealID, bookRecordIn{Body: body, Marks: marks, Members: map[string]json.RawMessage{"settlement": member}, Validate: validate})
	if err != nil {
		return err
	}
	if err := s.closeSend(ctx, in.dealID, in.check, sealed.Digest, in.status); err != nil {
		return err
	}
	result["digest"], result["seq"], result["status"] = sealed.Digest, sealed.Seq, in.status
	actResult(result, &gate, auth)
	return writePush(c, sealed.Pair, in.out, result)
}

// closeSend records the payer leg that closed a send. A failed payment counts
// no spend: its act event keeps its approval used and loses its amount
// (§13 item 9).
func (s *dealSession) closeSend(ctx context.Context, dealID, check, leg, status string) error {
	send, err := s.sendOf(ctx, dealID, check)
	if err != nil || send == nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deal_v1_sends SET leg_digest=? WHERE deal_id=? AND check_digest=?`, leg, dealID, check); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if status == legFailed {
		var local string
		if err := tx.QueryRowContext(ctx, `SELECT local FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, send.N).Scan(&local); err != nil {
			return errors.Join(err, tx.Rollback())
		}
		var ev dealEvent
		if err := json.Unmarshal([]byte(local), &ev); err != nil || ev.Act == nil {
			return errors.Join(fmt.Errorf("%w: deal %s's send event %d is not an act", evidencebook.ErrCorrupt, dealID, send.N), tx.Rollback())
		}
		ev.Act.AmountMinor, ev.Act.Direction = nil, ""
		b, err := json.Marshal(ev)
		if err != nil {
			return errors.Join(err, tx.Rollback())
		}
		if _, err := tx.ExecContext(ctx, `UPDATE deal_v1_events SET local=? WHERE deal_id=? AND n=?`, string(b), dealID, send.N); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}

func (s *dealSession) settlePayee(ctx context.Context, c *cobra.Command, a *v1ActDeal, in settleIn, body xdealv1.SettleBody, marks []string, currency, termsRef string, events []sealedEvent) error {
	if body.Leg != xdealv1.LegPayee {
		return cli.InputError("you owe this row: a payment you make rests on your pay check (--check), sent with --sent first")
	}
	if in.supersedes != "" {
		ok := false
		for _, r := range a.book {
			if b, isSettle := settleOf(r); isSettle && b.Leg == xdealv1.LegPayee {
				if d, err := xdealv1.Digest(r); err == nil && d == in.supersedes {
					ok = (b.Reverses == nil) == (body.Reverses == nil) && (b.Reverses == nil || b.Reverses.Digest == body.Reverses.Digest)
				}
			}
		}
		if !ok {
			return cli.InputError("--supersedes must name an earlier leg of what you received in your own book, of the same payment")
		}
		r := xdealv1.RefTo(in.supersedes)
		body.Supersedes = &r
	}
	refMember, err := in.ref.member()
	if err != nil {
		return err
	}
	received, err := legAmountOf(in.received, currency)
	if err != nil {
		return err
	}
	fee, err := legAmountOf(in.receiveFee, currency)
	if err != nil {
		return err
	}
	member, err := settleMember(map[string]any{"version": aacsettlement.Version, "leg": "payee_observed", "sealer_role": "payee", "terms_ref": termsRef,
		"received": received, "receive_fee": fee, "payment_ref": refMember, "status": in.status, "observed_at": v1Now()})
	if err != nil {
		return err
	}
	result := map[string]any{"deal_id": in.dealID, "record": xdealv1.TypeSettle, "leg": xdealv1.LegPayee, "status": in.status}
	if seq, err := s.sealedPayeeLike(ctx, a, body, marks, member); err != nil {
		return err
	} else if seq > 0 {
		return s.writeSealedAt(ctx, c, a.d, seq, a.book[seq-1], in.out, result)
	}
	if err := refuseExistingOut(in.out); err != nil {
		return err
	}
	sealed, err := s.sealBookV1(ctx, in.dealID, bookRecordIn{Body: body, Marks: marks, Members: map[string]json.RawMessage{"settlement": member}})
	if err != nil {
		return err
	}
	if _, err := s.creditRefund(ctx, in.dealID, a.book, events, sealed.Record, sealed.Digest, in.status, in.received+in.receiveFee, currency); err != nil {
		return err
	}
	result["digest"], result["seq"] = sealed.Digest, sealed.Seq
	return writePush(c, sealed.Pair, in.out, result)
}

// sealedPayeeLike is the seq of a payee leg a re-run already sealed: the same
// body and marks, and a member equal but for its observed time.
func (s *dealSession) sealedPayeeLike(ctx context.Context, a *v1ActDeal, body xdealv1.SettleBody, marks []string, member json.RawMessage) (int, error) {
	seq, err := sealedLike(a.book, xdealv1.TypeSettle, body, marks, body.Statements)
	if err != nil || seq == 0 {
		return seq, err
	}
	held, err := s.ownSettleMember(ctx, a.d, seq)
	if err != nil {
		return 0, err
	}
	if !sameButObservedAt(held, member) {
		return 0, nil
	}
	return seq, nil
}

func sameButObservedAt(a, b json.RawMessage) bool {
	var ma, mb map[string]any
	if json.Unmarshal(a, &ma) != nil || json.Unmarshal(b, &mb) != nil {
		return false
	}
	delete(ma, "observed_at")
	delete(mb, "observed_at")
	ja, _ := json.Marshal(ma)
	jb, _ := json.Marshal(mb)
	return bytes.Equal(ja, jb)
}

// ownSettleMember is the settlement member of the record at seq of the
// holder's book: the Capsule carries it, the record does not.
func (s *dealSession) ownSettleMember(ctx context.Context, d *v1Deal, seq int) (json.RawMessage, error) {
	book, err := s.shelf.Open(ctx, d.OwnLogID)
	if err != nil {
		return nil, err
	}
	rows, err := book.Query(ctx, evidencebook.Filter{FromSeq: uint64(seq), ToSeq: uint64(seq)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("%w: the deal book has no record %d", cli.ErrConflict, seq)
	}
	sealed, err := book.Sealed(ctx, rows[0].RecordID)
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(sealed.Capsule, &members); err != nil {
		return nil, err
	}
	return members["settlement"], nil
}

// creditRefund writes the buyer's refund received as money returned (§13 item
// 9): a payee leg that reverses the holder's own payer leg, once it is
// settled, once per reversed payment, as v0's act event with Action cancel,
// Direction in and Reverses the reversed payment's act event. It writes
// nothing for any other leg, for a pending or failed one, or for a payment
// already credited. ok says whether it wrote.
func (s *dealSession) creditRefund(ctx context.Context, dealID string, book []xdealv1.Record, events []sealedEvent, rec xdealv1.Record, digest, status string, amount int64, currency string) (bool, error) {
	b, ok := settleOf(rec)
	if !ok || b.Leg != xdealv1.LegPayee || b.Reverses == nil || status != legSettled {
		return false, nil
	}
	if v1EventByDigest(events, digest) != "" {
		return false, nil
	}
	var reversedCheck string
	for _, r := range book {
		if rb, ok := settleOf(r); ok && rb.Leg == xdealv1.LegPayer && rb.Check != nil {
			if d, err := xdealv1.Digest(r); err == nil && d == b.Reverses.Digest {
				reversedCheck = rb.Check.Digest
			}
		}
	}
	if reversedCheck == "" {
		return false, nil
	}
	send, err := s.sendOf(ctx, dealID, reversedCheck)
	if err != nil || send == nil {
		return false, err
	}
	payAct := v1EventID(dealID, send.N)
	for _, se := range events {
		if se.Event.Act != nil && se.Event.Act.Reverses == payAct {
			return false, nil
		}
	}
	ev, err := s.fixStep(dealID, events, dealEvent{Kind: "act", Act: &dealAct{Action: "cancel", AmountMinor: &amount, Currency: currency, Direction: "in", Reverses: payAct}})
	if err != nil {
		return false, err
	}
	ev.At = rec.Block.At
	local, err := json.Marshal(ev)
	if err != nil {
		return false, err
	}
	return true, s.storeV1(ctx, dealID, preparedV1{se: sealedEvent{CapsuleID: v1EventID(dealID, ev.N), Digest: digest, Sequence: uint64(ev.N), Event: ev}, local: local})
}

// reconcileV1Settle finishes a settle record a stopped run sealed but did not
// record locally: a payer leg closes its send (its act event was written at
// the send), and a settled refund received is credited once.
func (s *dealSession) reconcileV1Settle(ctx context.Context, dealID string, book []xdealv1.Record, events []sealedEvent, rec xdealv1.Record, b *xdealv1.SettleBody, digest string) ([]sealedEvent, error) {
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil || d == nil {
		return events, err
	}
	seq := int(rec.Block.Seq)
	member, err := s.ownSettleMember(ctx, d, seq)
	if err != nil {
		return nil, err
	}
	var m struct {
		Status   string `json:"status"`
		Received struct {
			Value string `json:"value"`
		} `json:"received"`
		ReceiveFee struct {
			Value string `json:"value"`
		} `json:"receive_fee"`
		Currency string
	}
	if err := json.Unmarshal(member, &m); err != nil {
		return nil, fmt.Errorf("%w: the settlement member of record %d: %v", evidencebook.ErrCorrupt, seq, err)
	}
	if b.Leg == xdealv1.LegPayer && b.Check != nil {
		send, err := s.sendOf(ctx, dealID, b.Check.Digest)
		if err != nil {
			return nil, err
		}
		if send == nil {
			return nil, fmt.Errorf("%w: deal %s's payer leg %s has no send recorded", cli.ErrConflict, dealID, digest)
		}
		if send.Leg == "" {
			if err := s.closeSend(ctx, dealID, b.Check.Digest, digest, m.Status); err != nil {
				return nil, err
			}
			return s.v1Events(ctx, dealID)
		}
		return events, nil
	}
	var received, fee int64
	if m.Received.Value != "" {
		if received, err = strconv.ParseInt(m.Received.Value, 10, 64); err != nil {
			return nil, err
		}
	}
	if m.ReceiveFee.Value != "" {
		if fee, err = strconv.ParseInt(m.ReceiveFee.Value, 10, 64); err != nil {
			return nil, err
		}
	}
	var cur struct {
		Received struct {
			AssetCode string `json:"assetCode"`
		} `json:"received"`
	}
	_ = json.Unmarshal(member, &cur)
	wrote, err := s.creditRefund(ctx, dealID, book, events, rec, digest, m.Status, received+fee, cur.Received.AssetCode)
	if err != nil || !wrote {
		return events, err
	}
	return s.v1Events(ctx, dealID)
}
