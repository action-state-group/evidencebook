// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	aacsettlement "github.com/action-state-group/agent-action-capsule/go/settlement"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	dealapi "github.com/action-state-group/evidencebook/deal"
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

// The statuses an observed leg takes here (AAC's statuses, less reversed:
// a refund is its own leg).
const (
	legPending = "pending"
	legSettled = "settled"
	legFailed  = "failed"
)

// equalRefTypes are the payment reference types whose value is fixed before
// paying and is the same identifier the payment settles under, so a payer
// leg's reference equals its check's (§13 item 4).
var equalRefTypes = map[string]bool{"ln.payment_hash": true, "bolt12.invoice_payment_hash": true, "acp.order_id": true, "ucp.order_id": true}

// paymentRefFlags reads --payment-ref-type, --payment-ref and the type's
// qualifiers into AAC's flat payment_ref (settlement-records-01: qualifiers at
// the top level, each required, exactly).
type paymentRefFlags struct {
	Type, Value, Network, Method, DebtorAgent, Origin string
	// emptyValue lets a merchant order's value be "": a send on a pay check
	// that has no reference before paying, or a checkout that failed and
	// left no order number (evidencebook#771). Only settleSent and the
	// payer leg of such a send set it.
	emptyValue bool
}

func (p paymentRefFlags) member() (map[string]any, error) {
	if p.Type == merchantOrderType {
		return p.merchantOrder()
	}
	t, ok := aacsettlement.PaymentRefTypes[p.Type]
	if !ok {
		names := make([]string, 0, len(aacsettlement.PaymentRefTypes)+1)
		for n := range aacsettlement.PaymentRefTypes {
			names = append(names, n)
		}
		names = append(names, merchantOrderType)
		slices.Sort(names)
		return nil, cli.InputError("--payment-ref-type must be one of " + strings.Join(names, ", "))
	}
	if p.Origin != "" {
		return nil, cli.InputError("--payment-ref-origin goes with " + merchantOrderType + " only")
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

// merchantOrder is the x-deal-v1.merchant_order payment_ref: the value (the
// order id, trimmed) and the checkout origin in normal form, nothing else.
func (p paymentRefFlags) merchantOrder() (map[string]any, error) {
	for name, v := range map[string]string{"network": p.Network, "method": p.Method, "debtor-agent": p.DebtorAgent} {
		if v != "" {
			return nil, cli.InputError("--payment-ref-" + name + " does not go with " + merchantOrderType)
		}
	}
	if p.Origin == "" {
		return nil, cli.InputError("--payment-ref-origin is required for " + merchantOrderType + ": the checkout's origin, such as https://shop.example")
	}
	origin, err := normalOrigin(p.Origin)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(p.Value)
	if value == "" && !p.emptyValue {
		return nil, cli.InputError("--payment-ref is required: the merchant's order number")
	}
	if err := refuseCardData("--payment-ref", value); err != nil {
		return nil, err
	}
	return map[string]any{"type": merchantOrderType, "value": value, "origin": origin}, nil
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

// settleIn is what `deal settle` reads from its flags.
type settleIn struct {
	// history is the counterparty's History a pay check read, given again
	// to --sent (readReceivedHistory), or nil.
	history                                               *receivedHistory
	dealID, check, status, row, reverses, supersedes, out string
	sent, noRef                                           bool
	ref                                                   paymentRefFlags
	routingFee, received, receiveFee                      int64
	hasRoutingFee, hasReceived, hasReceiveFee             bool
	statements, marks                                     []string
	remedyRow                                             string
}

const settleLong = `Seal a payment leg of an x-deal-v1 deal in your deal book: what you paid, a
refund you paid, or what you received, and write the file to send to --out.
The other side counts it with deal receive. The leg (payer or payee), its terms
and its amount come from the deal and your check, never from free input.

A payment you make takes two calls. Before you hand it to the rail, run
deal settle --sent --check DIGEST (your passing pay check, or your cancel check
for a refund): your first act waits on the other side's stamp proof, and the
check's approval and your limits are checked first, so a payment they would
refuse never leaves. From then the
amount counts toward your limits, and no record that changes the deal's
details can be sealed until the payment is settled or failed. When the rail
answers, run deal settle --check DIGEST --status settled (or failed) --out FILE.
While the rail still says pending, run nothing more for this payment: it stays
in flight, and a second --sent on the check names it. If you no longer have the
rail's answer, look the payment up on the rail by the reference --sent printed,
then run --status. A failed payment uses up its check: to try again, run deal
check for a new one.

--payment-ref-type names the rail's reference type and --payment-ref its value.
On ln.payment_hash, bolt12.invoice_payment_hash, acp.order_id and ucp.order_id
the payer leg's value is the check's payment_ref and may be left out. On x402
the leg carries the transaction (--payment-ref, with --payment-ref-network);
the link from the transaction to the checked nonce is the payer's claim unless
a verifier reads the transaction's AuthorizationUsed nonce on chain.

A card checkout has no rail reference: its payment is under x-deal-v1's own
x-deal-v1.merchant_order, the merchant's order number, with
--payment-ref-origin, the checkout's origin (such as https://shop.example).
When the checkout showed the order number before paying, the pay check's
payment_ref is that number and the payment is bound to it. When it did not,
the pay check said payment_ref_after_payment, --sent takes no --payment-ref,
and --status settled takes the order number (a failed checkout may leave it
out); the report then says the reference was recorded after payment. No
reference may be a card number: when the order number looks like one,
--status settled takes --no-payment-ref and the report says no reference was
recorded.

What you received is a payee leg: --received and --receive-fee (0 when none was
taken) in minor units, --status pending, settled or failed, and the payment's
reference. A later observation of it names the earlier one with --supersedes.
--reverses names a leg of your own book that this leg refunds. Once a refund
leg is in a book, the report no longer matches that row's payment legs against
each other, the payment's included: a refund needs its own terms, which are not
defined yet, and the report says so.

--statement row=value says how a row went; --mark and --remedy-row are as for
deal perform. Run again with the same flags, --status rewrites the same file and
seals nothing new; a second --sent on a check is refused.`

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
			in.noRef, _ = f.GetBool("no-payment-ref")
			in.ref.Type, _ = f.GetString("payment-ref-type")
			in.ref.Value, _ = f.GetString("payment-ref")
			in.ref.Network, _ = f.GetString("payment-ref-network")
			in.ref.Method, _ = f.GetString("payment-ref-method")
			in.ref.DebtorAgent, _ = f.GetString("payment-ref-debtor-agent")
			in.ref.Origin, _ = f.GetString("payment-ref-origin")
			// A reference is taken as shown, trimmed (evidencebook#771).
			in.ref.Value = strings.TrimSpace(in.ref.Value)
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
					what := "a record digest"
					if name == "--check" {
						what = "the check_id deal check printed, or the check record's digest"
					}
					return cli.InputError(name + " must be " + what + ": 64 lowercase hex characters")
				}
			}
			payer := in.check != ""
			switch {
			case in.sent && !payer:
				return cli.InputError("--sent goes with --check: the check the payment rests on")
			case in.noRef && (!payer || in.sent):
				return cli.InputError("--no-payment-ref goes only with --status settled on a card checkout whose order number came after paying")
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
			var err error
			if in.history, err = readReceivedHistory(c); err != nil {
				return err
			}
			if in.history != nil && !in.sent {
				return cli.InputError("--history goes with --sent: the payment is checked again there against the History its check read")
			}
			v1ReceiveContext(c)
			return runDeal(c, false, func(ctx context.Context, s *dealSession, _ string, _ []sealedEvent) error {
				return s.settle(ctx, c, in)
			})
		},
	}
	f := cmd.Flags()
	f.String("deal", "", "The x-deal-v1 deal (required)")
	f.String("check", "", "Your passing pay check (or cancel check, for a refund) that a payment rests on: the check_id deal check printed (or the check record's digest)")
	f.Bool("sent", false, "Before the handoff: check the payment and count it as in flight; seals no leg")
	f.StringArray("history", nil, "With --sent, when the pay check read the counterparty's History (deal check --history): the same stapled history_card/2 file, verified again, so the payment is checked against what the check read")
	f.StringSlice("counterparty-key", nil, "With --history: the same counterparty key the check used, from your deployment policy")
	f.StringSlice("checkpoint-key", nil, "With a stapled history_card/2 in --history: the same checkpoint key the check used (its verdict binds it: any other is refused)")
	f.String("status", "", "settled or failed for a payment when the rail answers; pending, settled or failed for what you received")
	f.String("row", "", "The payment row (default: the offer's only one)")
	f.String("reverses", "", "A leg of your own book that this leg refunds")
	f.String("supersedes", "", "An earlier leg of what you received that this observation replaces")
	f.String("payment-ref-type", "", "The rail's reference type, such as ln.payment_hash or x402.transaction")
	f.String("payment-ref", "", "The payment's reference on the rail")
	f.String("payment-ref-network", "", "The network, for x402.transaction")
	f.Bool("no-payment-ref", false, "With --status settled on a card checkout whose order number came after paying: record the payment with no reference (an order number that looks like a card number)")
	f.String("payment-ref-origin", "", "The checkout's origin (scheme and host, such as https://shop.example), for "+merchantOrderType+", the merchant's order number on a card checkout")
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
	if in.check == "" {
		return s.settlePayee(ctx, c, in)
	}
	st, events, _, err := s.v1ActSession(ctx, in.dealID)
	if err != nil {
		return err
	}
	// After the deal is known to be v1 and its tables exist: --check may be
	// the check_id deal check printed.
	if in.check, err = s.resolveV1Check(ctx, in.dealID, in.check); err != nil {
		return err
	}
	stmts, err := parseStatementFlags(in.statements)
	if err != nil {
		return err
	}
	if in.sent {
		return s.settleSent(ctx, c, st, events, in, stmts)
	}
	return s.settleAnswered(ctx, c, st, in, stmts)
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
	if cb.PaymentRef == "" {
		// A pay check with no reference before paying (a card checkout,
		// evidencebook#771): only the merchant's order number, given when the
		// rail answers.
		switch {
		case p.Type != merchantOrderType:
			return p, cli.InputError("check " + in.check + " records no payment_ref (the checkout showed no reference before paying), so the payment is sent as " + merchantOrderType + ": give --payment-ref-type " + merchantOrderType + " --payment-ref-origin ORIGIN")
		case p.Value != "":
			return p, cli.InputError("check " + in.check + " records no payment_ref, so the order number comes when the rail answers: leave out --payment-ref here and give it with --status")
		}
		p.emptyValue = true
		return p, nil
	}
	if equalRefTypes[p.Type] || p.Type == merchantOrderType {
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

// settleSent is `deal settle --sent`: package deal's Store.SendPayment, with
// the reference the flags and the check give, the plugin's own checks of the
// candidate leg (an approval deal note marked not applied, and the pay
// check's evaluation run again), and the send's local act event, which
// counts the amount from the send on.
func (s *dealSession) settleSent(ctx context.Context, c *cobra.Command, st *dealapi.Store, events []sealedEvent, in settleIn, stmts []xdealv1.StatementItem) error {
	var cb *xdealv1.CheckBody
	var p paymentRefFlags
	res, err := st.SendPayment(ctx, in.dealID, dealapi.SendIn{
		Check: in.check, Row: in.row, Reverses: in.reverses, Statements: stmts, Marks: in.marks, RemedyRow: in.remedyRow,
		Producer: currentProducer(),
		RefFor: func(check *xdealv1.CheckBody) (json.RawMessage, error) {
			cb = check
			var err error
			if p, err = sendRef(in, check); err != nil {
				return nil, err
			}
			m, err := p.member()
			if err != nil {
				return nil, err
			}
			return json.Marshal(m)
		},
		Before: func(_ xdealv1.Record, auth *xdealv1.ActAuthorization) error {
			if err := refuseStaleApproval(events, auth); err != nil {
				return err
			}
			if cb.Action == "pay" {
				return s.recheckAtSend(ctx, in.dealID, events, in.check, in.history)
			}
			return nil
		},
		// The local act event counts the amount from the send on; its record
		// is the journal's "payment sent" evidence, whose detail commitment
		// its "sent" nonce opens.
		Step: func(f dealapi.StepFacts, auth *xdealv1.ActAuthorization, amount int64, currency string) (json.RawMessage, string, error) {
			act := dealAct{Action: cb.Action, AmountMinor: &amount, Currency: currency, Reference: p.Value}
			if cb.Action == "pay" {
				act.Direction = "out"
			}
			if auth != nil {
				ad, err := xdealv1.Digest(auth.Approval)
				if err != nil {
					return nil, "", err
				}
				if act.AuthorizedBy = v1EventByDigest(events, ad); act.AuthorizedBy == "" {
					return nil, "", fmt.Errorf("%w: the approval %s that authorized the payment has no local step", cli.ErrConflict, ad)
				}
			}
			ev, err := s.fixStep(in.dealID, events, dealEvent{Kind: "act", Act: &act})
			if err != nil {
				return nil, "", err
			}
			if ev.N != f.N || ev.Prev != f.Prev {
				return nil, "", fmt.Errorf("%w: deal %s's local events moved while a step was sealed; run the command again", cli.ErrConflict, in.dealID)
			}
			ev.At = f.At
			nonce, err := xdealv1.NewNonce()
			if err != nil {
				return nil, "", err
			}
			if ev.Nonces == nil {
				ev.Nonces = map[string]string{}
			}
			ev.Nonces["sent"] = nonce
			local, err := json.Marshal(ev)
			return local, nonce, err
		},
	})
	if err != nil {
		return libErr(err)
	}
	result := map[string]any{"deal_id": in.dealID, "sent": res.Evidence, "check": in.check, "amount_minor": res.AmountMinor, "currency": res.Currency,
		"next": "hand the payment to the rail; when it answers, run " + statusHint(in.dealID, in.check, in.reverses, p.Type)}
	actResult(result, &res.Gate, res.Authorization)
	return cli.Output(c, result)
}

// statusHint is the command that seals a sent payment's leg when the rail
// answers, with the flags it needs.
func statusHint(dealID, check, reverses, refType string) string {
	h := "deal settle --deal " + dealID + " --check " + check
	if reverses != "" {
		h += " --reverses " + reverses
	}
	h += " --status settled|failed --out FILE"
	switch {
	case equalRefTypes[refType]:
	case refType == merchantOrderType:
		h += " [--payment-ref THE_ORDER_NUMBER, when the check recorded none]"
	case reverses == "" || refType == "x402.transaction":
		h += " --payment-ref THE_RAILS_REFERENCE"
	default:
		h += " [--payment-ref THE_RAILS_REFERENCE]"
	}
	if refType == "x402.transaction" {
		h += " (on x402, the transaction)"
	}
	return h
}

// settleAnswered is `deal settle --status`: package deal's Store.Settle
// with the check, binding the payer leg to its send, with the reference the
// flags and the send give, and a failed payment's act event rewritten to
// count no spend.
func (s *dealSession) settleAnswered(ctx context.Context, c *cobra.Command, st *dealapi.Store, in settleIn, stmts []xdealv1.StatementItem) error {
	var routing *int64
	if in.hasRoutingFee {
		routing = &in.routingFee
	}
	res, err := st.Settle(ctx, in.dealID, dealapi.SettleIn{
		Check: in.check, Row: in.row, Reverses: in.reverses, Status: in.status, RoutingFee: routing,
		Statements: stmts, Marks: in.marks, RemedyRow: in.remedyRow,
		LegRef: func(cb *xdealv1.CheckBody, send *dealapi.Send) (json.RawMessage, error) {
			ref, err := answeredRef(in, cb, send)
			if err != nil {
				return nil, err
			}
			return json.Marshal(ref)
		},
		Before:    func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refuseExistingOut(in.out) },
		CloseStep: failedActStep,
	})
	if err != nil {
		return libErr(err)
	}
	if res.AmountNotChecked {
		// A refund sent before evidencebook#743's rule, or written again,
		// with the amount its send recorded: that amount is not checked.
		if _, err := fmt.Fprintln(c.ErrOrStderr(), "note: --check "+in.check+" states a cancellation fee and the record does not say who bears it, so this refund's amount (the full cancelled amount its send recorded) is not checked (evidencebook#743)"); err != nil {
			return err
		}
	}
	result := map[string]any{"deal_id": in.dealID, "record": xdealv1.TypeSettle, "leg": xdealv1.LegPayer, "status": in.status, "digest": res.Digest, "seq": res.Seq}
	if !res.Again {
		actResult(result, res.Gate, res.Authorization)
	}
	return writePush(c, res.Pair, in.out, result)
}

// failedActStep is a failed payment's act event: its approval stays used and
// it counts no spend (§13 item 9).
func failedActStep(local json.RawMessage) (json.RawMessage, error) {
	var ev dealEvent
	if err := json.Unmarshal(local, &ev); err != nil || ev.Act == nil {
		return nil, fmt.Errorf("%w: a send's local event is not an act", evidencebook.ErrCorrupt)
	}
	ev.Act.AmountMinor, ev.Act.Direction = nil, ""
	return json.Marshal(ev)
}

// answeredRef is the payer leg's payment_ref when the rail answers: the send's
// whole reference, with its type and qualifiers unchanged. Its value is the
// send's on an equal type and on a refund (whose send bound the refund's own
// reference); on x402 and the other types of a payment, where the send bound
// the reference known before paying, the leg carries the rail's own
// (--payment-ref), so it joins the payee leg.
func answeredRef(in settleIn, cb *xdealv1.CheckBody, send *dealapi.Send) (map[string]any, error) {
	var bound map[string]any
	if err := json.Unmarshal(send.RefJSON, &bound); err != nil {
		return nil, fmt.Errorf("%w: the send of check %s: %v", evidencebook.ErrCorrupt, send.Check, err)
	}
	if in.ref.Type != "" && in.ref.Type != send.RefType {
		return nil, cli.InputError("the payment was sent under " + send.RefType + ", and --payment-ref-type is " + in.ref.Type)
	}
	if in.noRef && (cb.Action != "pay" || send.RefType != merchantOrderType || send.Ref != "" || in.ref.Value != "" || in.status != legSettled) {
		return nil, cli.InputError("--no-payment-ref goes only with --status settled on a card checkout whose order number came after paying (" + merchantOrderType + "), and without --payment-ref")
	}
	for name, flag := range map[string]string{"network": in.ref.Network, "method": in.ref.Method, "debtor_agent": in.ref.DebtorAgent, "origin": in.ref.Origin} {
		if name == "origin" && flag != "" {
			if send.RefType != merchantOrderType {
				return nil, cli.InputError("--payment-ref-origin goes with " + merchantOrderType + " only, and this payment was sent under " + send.RefType)
			}
			n, err := normalOrigin(flag)
			if err != nil {
				return nil, err
			}
			flag = n
		}
		if flag != "" && flag != bound[name] {
			return nil, cli.InputError("the payment was sent with " + name + " " + fmt.Sprint(bound[name]) + ", and --payment-ref-" + strings.ReplaceAll(name, "_", "-") + " differs")
		}
	}
	// On an equal type the value is fixed before paying. On the other types
	// the rail's own reference comes back with its answer: a payment, whose
	// send bound the check's pre-payment reference, must give it; a refund,
	// whose send bound what the refunder knew then, may.
	equal := equalRefTypes[send.RefType] || (send.RefType == merchantOrderType && send.Ref != "")
	switch {
	case equal && in.ref.Value != "" && in.ref.Value != send.Ref:
		return nil, cli.InputError("on " + send.RefType + " the leg's reference is the one the payment was sent under (" + send.Ref + "), and --payment-ref differs")
	case equal:
	case in.noRef:
		// No reference at all: the order number looks like a card number
		// (Sherlock2, evidencebook#771); the report says so.
	case send.RefType == merchantOrderType && in.ref.Value != "":
		// The order number recorded after paying (evidencebook#771).
		if err := refuseCardData("--payment-ref", in.ref.Value); err != nil {
			return nil, err
		}
		bound["value"] = in.ref.Value
	case send.RefType == merchantOrderType && in.status == legFailed:
		// A checkout that failed may leave no order number: the leg carries "".
	case send.RefType == merchantOrderType:
		return nil, cli.InputError("--payment-ref is required: the merchant's order number, which the checkout shows once paid (a failed checkout may leave it out with --status failed; one that looks like a card number is left out with --no-payment-ref)")
	case in.ref.Value != "":
		bound["value"] = in.ref.Value
	case cb.Action == "pay" || send.RefType == "x402.transaction":
		return nil, cli.InputError("--payment-ref is required: on " + send.RefType + " the leg carries the rail's own reference (the transaction on x402), which comes back with the rail's answer, not the " + send.Ref + " known before paying")
	}
	return bound, nil
}

// settlePayee seals what the holder received, a payee leg, through package
// deal (Store.Settle): the leg cites no check. A settled refund received of
// the holder's own payment is then counted as money returned (creditRefund).
func (s *dealSession) settlePayee(ctx context.Context, c *cobra.Command, in settleIn) error {
	st, events, book, err := s.v1ActSession(ctx, in.dealID)
	if err != nil {
		return err
	}
	stmts, err := parseStatementFlags(in.statements)
	if err != nil {
		return err
	}
	refMember, err := in.ref.member()
	if err != nil {
		return err
	}
	ref, err := json.Marshal(refMember)
	if err != nil {
		return err
	}
	res, err := st.Settle(ctx, in.dealID, dealapi.SettleIn{
		Row: in.row, Reverses: in.reverses, Supersedes: in.supersedes, Status: in.status,
		Received: in.received, ReceiveFee: in.receiveFee, PaymentRef: ref,
		Statements: stmts, Marks: in.marks, RemedyRow: in.remedyRow,
		Before: func(xdealv1.Record, *xdealv1.ActAuthorization) error { return refuseExistingOut(in.out) },
	})
	if err != nil {
		return libErr(err)
	}
	if !res.Again {
		currency, err := s.legCurrency(ctx, st, in.dealID, int(res.Seq))
		if err != nil {
			return err
		}
		if _, err := s.creditRefund(ctx, in.dealID, book, events, res.Record, res.Digest, in.status, in.received, currency); err != nil {
			return err
		}
	}
	result := map[string]any{"deal_id": in.dealID, "record": xdealv1.TypeSettle, "leg": xdealv1.LegPayee, "status": in.status, "digest": res.Digest, "seq": res.Seq}
	return writePush(c, res.Pair, in.out, result)
}

// legCurrency is the currency of the leg at seq of the holder's book: its
// settlement member's received amount's.
func (s *dealSession) legCurrency(ctx context.Context, st *dealapi.Store, dealID string, seq int) (string, error) {
	d, err := st.Deal(ctx, dealID)
	if err != nil || d == nil {
		return "", err
	}
	member, err := st.SettlementAt(ctx, d, seq)
	if err != nil {
		return "", libErr(err)
	}
	var m struct {
		Received struct {
			AssetCode string `json:"assetCode"`
		} `json:"received"`
	}
	if err := json.Unmarshal(member, &m); err != nil {
		return "", fmt.Errorf("%w: the settlement member of record %d: %v", evidencebook.ErrCorrupt, seq, err)
	}
	return m.Received.AssetCode, nil
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
// Direction in and Reverses the reversed payment's act event. The amount is
// what was received (net of the receive fee, which the buyer did not get
// back), at most what the payment counted. It writes
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
	st, err := s.dealStore(ctx)
	if err != nil {
		return false, err
	}
	send, err := st.SendOf(ctx, dealID, reversedCheck)
	if err != nil || send == nil {
		return false, libErr(err)
	}
	payAct := v1EventID(dealID, send.N)
	var paid *int64
	for _, se := range events {
		if se.Event.Act != nil && se.Event.Act.Reverses == payAct {
			return false, nil
		}
		if se.CapsuleID == payAct && se.Event.Act != nil {
			paid = se.Event.Act.AmountMinor
		}
	}
	// A payment that moved no money (failed) returns none, and a refund
	// returns at most what was paid.
	if paid == nil {
		return false, nil
	}
	if amount > *paid {
		amount = *paid
	}
	_, err = s.storeV1(ctx, dealID, s.v1LocalStep(dealID, events, dealEvent{Kind: "act", Act: &dealAct{Action: "cancel", AmountMinor: &amount, Currency: currency, Direction: "in", Reverses: payAct}}, rec.Block.At, digest))
	return err == nil, err
}

// reconcileV1Settle finishes a settle record a stopped run sealed but did not
// record locally: a payer leg closes its send (its act event was written at
// the send), and a settled refund received is credited once. A record with
// nothing left to do returns before its Capsule is read.
func (s *dealSession) reconcileV1Settle(ctx context.Context, dealID string, book []xdealv1.Record, events []sealedEvent, rec xdealv1.Record, b *xdealv1.SettleBody, digest string) ([]sealedEvent, error) {
	st, err := s.dealStore(ctx)
	if err != nil {
		return nil, err
	}
	var send *dealapi.Send
	switch {
	case b.Leg == xdealv1.LegPayer && b.Check != nil:
		if send, err = st.SendOf(ctx, dealID, b.Check.Digest); err != nil {
			return nil, libErr(err)
		}
		if send == nil {
			return nil, fmt.Errorf("%w: deal %s's payer leg %s has no send recorded", cli.ErrConflict, dealID, digest)
		}
		if send.Leg != "" {
			return events, nil
		}
	case b.Leg == xdealv1.LegPayee && b.Reverses != nil:
		if v1EventByDigest(events, digest) != "" {
			return events, nil
		}
	default:
		return events, nil
	}
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil || d == nil {
		return events, err
	}
	member, err := s.ownSettleMember(ctx, d, int(rec.Block.Seq))
	if err != nil {
		return nil, err
	}
	var m struct {
		Status   string `json:"status"`
		Received struct {
			Value     string `json:"value"`
			AssetCode string `json:"assetCode"`
		} `json:"received"`
	}
	if err := json.Unmarshal(member, &m); err != nil {
		return nil, fmt.Errorf("%w: the settlement member of record %d: %v", evidencebook.ErrCorrupt, rec.Block.Seq, err)
	}
	if send != nil {
		if err := st.CloseSend(ctx, dealID, b.Check.Digest, digest, m.Status, failedActStep); err != nil {
			return nil, libErr(err)
		}
		return s.v1Events(ctx, dealID)
	}
	received, err := strconv.ParseInt(m.Received.Value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: the received amount of record %d: %v", evidencebook.ErrCorrupt, rec.Block.Seq, err)
	}
	wrote, err := s.creditRefund(ctx, dealID, book, events, rec, digest, m.Status, received, m.Received.AssetCode)
	if err != nil || !wrote {
		return events, err
	}
	return s.v1Events(ctx, dealID)
}

// resolveV1Check is the record digest --check names: the check id `deal
// check` printed (the local id of its check step), or the check record's
// digest itself. The check step names its snapshot step, whose record is the
// journal check record, so the id is followed to that record's digest. A
// person copies the id the check printed, so both are taken; any other value
// passes through, and "" stays "".
func (s *dealSession) resolveV1Check(ctx context.Context, dealID, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	events, err := s.v1Events(ctx, dealID)
	if err != nil {
		return "", err
	}
	if v, _, ok := checkedCard(events, ref); ok {
		for _, se := range events {
			if se.CapsuleID == v.Event.Check.Snapshot && se.Digest != "" {
				return se.Digest, nil
			}
		}
	}
	return ref, nil
}
