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
	dealapi "github.com/action-state-group/evidencebook/deal"
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
// local act event that counts it, what the send bound (amount, currency, the
// whole payment reference, the leg a refund reverses), and the payer leg that
// closed it ("" while open).
const dealV1SendsTable = `CREATE TABLE IF NOT EXISTS deal_v1_sends (
	deal_id TEXT NOT NULL,
	check_digest TEXT NOT NULL,
	event_n INTEGER NOT NULL,
	amount_minor INTEGER NOT NULL,
	currency TEXT NOT NULL,
	ref_type TEXT NOT NULL,
	ref_value TEXT NOT NULL,
	ref_json TEXT NOT NULL,
	reverses TEXT NOT NULL DEFAULT '',
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
	RefJSON, Reverses      string
	Leg                    string
}

func (s *dealSession) sendOf(ctx context.Context, dealID, check string) (*v1Send, error) {
	v := v1Send{DealID: dealID, Check: check}
	err := s.db.QueryRowContext(ctx, `SELECT event_n, amount_minor, currency, ref_type, ref_value, ref_json, reverses, leg_digest FROM deal_v1_sends WHERE deal_id=? AND check_digest=?`, dealID, check).
		Scan(&v.N, &v.Amount, &v.Currency, &v.RefType, &v.Ref, &v.RefJSON, &v.Reverses, &v.Leg)
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
	q, args := `SELECT deal_id, check_digest, event_n, amount_minor, currency, ref_type, ref_value, reverses FROM deal_v1_sends WHERE leg_digest=''`, []any{}
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
		if err := rows.Scan(&v.DealID, &v.Check, &v.N, &v.Amount, &v.Currency, &v.RefType, &v.Ref, &v.Reverses); err != nil {
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
		" record that changes the deal's details would void its authorization, so it waits until the payment is settled or failed (`" + statusHint(dealID, open[0].Check, open[0].Reverses, open[0].RefType) + "`)")
}

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
// settle of the holder's own book that did not fail. Whether the leg is
// already refunded is refundTaken's, asked only for a new record, after a
// re-run has been found.
func (s *dealSession) legFor(ctx context.Context, a *v1ActDeal, row *xdealv1.Row, reverses string) (string, error) {
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
	// A payee leg may be superseded by a later observation of the same
	// payment: the latest ones say whether money moved.
	failed, err := s.chainFailed(ctx, a.d, a.book, reverses)
	if err != nil {
		return "", err
	}
	if failed {
		return "", cli.InputError("leg " + reverses + " failed: no money moved, so there is nothing to refund")
	}
	if target.Leg == xdealv1.LegPayer {
		return xdealv1.LegPayee, nil
	}
	return xdealv1.LegPayer, nil
}

// legStatusAt is the status of the settle record at seq of the holder's book,
// from its Capsule's settlement member.
func (s *dealSession) legStatusAt(ctx context.Context, d *v1Deal, seq int) (string, error) {
	member, err := s.ownSettleMember(ctx, d, seq)
	if err != nil {
		return "", err
	}
	var m struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(member, &m); err != nil {
		return "", fmt.Errorf("%w: the settlement member of record %d: %v", evidencebook.ErrCorrupt, seq, err)
	}
	return m.Status, nil
}

// refundTaken refuses a new refund of a leg that is already being refunded.
// The leg is its whole supersedes chain (a payee leg's later observations of
// one payment): a payer refund is refused when another send reverses any leg
// of the chain (open, or closed by a leg that did not fail); a payee refund
// received is refused when a refund received of the chain, at its latest
// observation, did not fail (a later observation of that refund names it
// with --supersedes instead).
func (s *dealSession) refundTaken(ctx context.Context, a *v1ActDeal, target string, payer bool) error {
	taken := cli.InputError("leg " + target + " is already refunded: a later observation of that refund names it with --supersedes, and a failed refund leaves the leg free to refund again")
	chain := legChain(a.book, target)
	if payer {
		rows, err := s.db.QueryContext(ctx, `SELECT reverses, leg_digest FROM deal_v1_sends WHERE deal_id=?`, a.d.DealID)
		if err != nil {
			return err
		}
		var legs []string
		for rows.Next() {
			var rev, l string
			if err := rows.Scan(&rev, &l); err != nil {
				rows.Close()
				return err
			}
			if chain[rev] {
				legs = append(legs, l)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, l := range legs {
			if l == "" {
				return taken
			}
			for i, r := range a.book {
				if d, err := xdealv1.Digest(r); err == nil && d == l {
					if st, err := s.legStatusAt(ctx, a.d, i+1); err != nil {
						return err
					} else if st != legFailed {
						return taken
					}
				}
			}
		}
		return nil
	}
	for _, r := range a.book {
		b, ok := settleOf(r)
		if !ok || b.Leg != xdealv1.LegPayee || b.Reverses == nil || !chain[b.Reverses.Digest] {
			continue
		}
		d, err := xdealv1.Digest(r)
		if err != nil {
			return err
		}
		if failed, err := s.chainFailed(ctx, a.d, a.book, d); err != nil {
			return err
		} else if !failed {
			return taken
		}
	}
	return nil
}

// legChain is the set of digests of the settle records linked to digest by
// supersedes, in either direction: one payment's observations.
func legChain(book []xdealv1.Record, digest string) map[string]bool {
	links := map[string]string{} // record -> the record it supersedes
	for _, r := range book {
		if b, ok := settleOf(r); ok && b.Supersedes != nil {
			if d, err := xdealv1.Digest(r); err == nil {
				links[d] = b.Supersedes.Digest
			}
		}
	}
	chain := map[string]bool{digest: true}
	for changed := true; changed; {
		changed = false
		for from, to := range links {
			if chain[from] != chain[to] {
				chain[from], chain[to], changed = true, true, true
			}
		}
	}
	return chain
}

// chainTips are the places (seq) of the settle records of digest's chain that
// no record supersedes: its latest observations. A chain normally has one;
// a fork (two records superseding one) has several, each still standing.
func chainTips(book []xdealv1.Record, digest string) []int {
	chain := legChain(book, digest)
	superseded := map[string]bool{}
	for _, r := range book {
		if b, ok := settleOf(r); ok && b.Supersedes != nil {
			superseded[b.Supersedes.Digest] = true
		}
	}
	var tips []int
	for i, r := range book {
		if d, err := xdealv1.Digest(r); err == nil && chain[d] && !superseded[d] {
			tips = append(tips, i+1)
		}
	}
	return tips
}

// chainFailed says every latest observation of digest's chain failed.
func (s *dealSession) chainFailed(ctx context.Context, d *v1Deal, book []xdealv1.Record, digest string) (bool, error) {
	tips := chainTips(book, digest)
	for _, seq := range tips {
		st, err := s.legStatusAt(ctx, d, seq)
		if err != nil {
			return false, err
		}
		if st != legFailed {
			return false, nil
		}
	}
	return len(tips) > 0, nil
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
	a, err := s.loadV1ActDeal(ctx, in.dealID)
	if err != nil {
		return err
	}
	if err := s.ensureV1Tables(ctx); err != nil {
		return err
	}
	// After the deal is known to be v1 and its tables exist: --check may be
	// the check_id deal check printed.
	if in.check, err = s.resolveV1Check(ctx, in.dealID, in.check); err != nil {
		return err
	}
	row, currency, err := a.paymentRow(in.row)
	if err != nil {
		return err
	}
	leg, err := s.legFor(ctx, a, row, in.reverses)
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
	if leg != xdealv1.LegPayer {
		if in.reverses != "" {
			return cli.InputError("a refund you receive is a payee leg, which cites no --check: give --received, --receive-fee and --status")
		}
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
	// (sealCancelAmount): the refund is that amount. With a cancellation fee
	// beside it, the record does not say who bears the fee, so the refund owed
	// is not known, and no amount is picked (Sherlock2 on #742; #743).
	if cb.AmountMinor == nil && cb.Action == "cancel" && cb.CancelledAmountMinor != nil && cb.FeeMinor != nil && *cb.FeeMinor != 0 {
		if in.sent && refundFeeGuard {
			return cli.InputError("--check " + in.check + " is a cancel that states a cancellation fee, and the record does not say who bears it, so the refund owed is not known and no refund is sent for it (evidencebook#743)")
		}
		// A refund sent before this rule completes (or is written again)
		// with the amount its send recorded, which was the full cancelled
		// amount; that amount is not checked, and the user is told so.
		if !in.sent {
			if _, err := fmt.Fprintln(c.ErrOrStderr(), "note: --check "+in.check+" states a cancellation fee and the record does not say who bears it, so this refund's amount (the full cancelled amount its send recorded) is not checked (evidencebook#743)"); err != nil {
				return err
			}
		}
	}
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

func (s *dealSession) settleSent(ctx context.Context, c *cobra.Command, a *v1ActDeal, in settleIn, body xdealv1.SettleBody, marks []string, cb *xdealv1.CheckBody, currency, termsRef string, events []sealedEvent) error {
	if prev, err := s.sendOf(ctx, in.dealID, in.check); err != nil {
		return err
	} else if prev != nil && prev.Leg != "" {
		return cli.InputError("check " + in.check + " is used: its payment was sealed as leg " + prev.Leg + ", and a check authorizes one payment")
	} else if prev != nil {
		ref := prev.RefType + " " + prev.Ref
		if prev.Ref == "" {
			ref = prev.RefType + ", the order number to come with --status"
		}
		return cli.InputError("a payment on check " + in.check + " is in flight (" + strconv.FormatInt(prev.Amount, 10) + " " + prev.Currency + ", " + ref + "): when the rail answers, run " + statusHint(in.dealID, in.check, prev.Reverses, prev.RefType))
	}
	if in.reverses != "" {
		if err := s.refundTaken(ctx, a, in.reverses, true); err != nil {
			return err
		}
	}
	for _, r := range v1Acts(a.book) {
		if b, ok := settleOf(r); ok && b.Check != nil && b.Check.Digest == in.check {
			return cli.InputError("check " + in.check + " already has a payer leg: a check authorizes one payment")
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
	if err := refuseStaleApproval(events, auth); err != nil {
		return err
	}
	if cb.Action == "pay" {
		if err := s.recheckAtSend(ctx, in.dealID, events, in.check, in.history); err != nil {
			return err
		}
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
	refJSON, err := json.Marshal(refMember)
	if err != nil {
		return err
	}
	if err := s.storeSend(ctx, in.dealID, ev.N, local, digest, payload, v1Send{Check: in.check, Amount: *cb.AmountMinor, Currency: currency, RefType: p.Type, Ref: p.Value, RefJSON: string(refJSON), Reverses: in.reverses}); err != nil {
		return err
	}
	result := map[string]any{"deal_id": in.dealID, "sent": digest, "check": in.check, "amount_minor": *cb.AmountMinor, "currency": currency,
		"next": "hand the payment to the rail; when it answers, run " + statusHint(in.dealID, in.check, in.reverses, p.Type)}
	actResult(result, &gate, auth)
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO deal_v1_sends (deal_id, check_digest, event_n, amount_minor, currency, ref_type, ref_value, ref_json, reverses) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dealID, v.Check, n, v.Amount, v.Currency, v.RefType, v.Ref, v.RefJSON, v.Reverses); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if afterSendStoredHook != nil {
		// A process that dies here leaves the send and its payload stored;
		// the next read appends the evidence (v1Events).
		if err := afterSendStoredHook(); err != nil {
			return err
		}
	}
	if err := s.appendV1Journal(ctx, payload); err != nil {
		held, herr := s.journalHolds(ctx, digest)
		if herr != nil || held {
			return errors.Join(err, herr)
		}
		// Both rows go in one transaction, so a send never outlives its act
		// event or the reverse; a failure keeps both for the next read.
		dtx, derr := s.db.BeginTx(ctx, nil)
		if derr != nil {
			return errors.Join(err, derr)
		}
		if _, derr = dtx.ExecContext(ctx, `DELETE FROM deal_v1_sends WHERE deal_id=? AND check_digest=?`, dealID, v.Check); derr == nil {
			_, derr = dtx.ExecContext(ctx, `DELETE FROM deal_v1_events WHERE deal_id=? AND n=?`, dealID, n)
		}
		if derr != nil {
			return errors.Join(err, derr, dtx.Rollback())
		}
		return errors.Join(err, dtx.Commit())
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
	if in.reverses != send.Reverses {
		if send.Reverses == "" {
			return cli.InputError("the payment on check " + in.check + " was sent as a payment, not a refund: leave out --reverses")
		}
		return cli.InputError("the refund on check " + in.check + " was sent as reversing leg " + send.Reverses + ": give --reverses " + send.Reverses)
	}
	if send.Amount != *cb.AmountMinor || send.Currency != currency {
		return fmt.Errorf("%w: the payment was sent as %d %s, and check %s records %d %s", cli.ErrConflict, send.Amount, send.Currency, in.check, *cb.AmountMinor, currency)
	}
	refMember, err := answeredRef(in, cb, send)
	if err != nil {
		return err
	}
	var routing *int64
	if in.hasRoutingFee {
		routing = &in.routingFee
	}
	member, err := payerMember(termsRef, send.Amount, currency, refMember, in.status, routing)
	if err != nil {
		return err
	}
	result := map[string]any{"deal_id": in.dealID, "record": xdealv1.TypeSettle, "leg": xdealv1.LegPayer, "status": in.status}
	if send.Leg != "" {
		// A re-run rewrites the sealed leg only when its inputs are the ones
		// sealed (§13 item 8): a payer leg is never sealed twice for one check.
		for i, r := range a.book {
			d, err := xdealv1.Digest(r)
			if err != nil || d != send.Leg {
				continue
			}
			held, err := s.ownSettleMember(ctx, a.d, i+1)
			if err != nil {
				return err
			}
			sameBody, err := sameRecordInput(r, body, marks)
			if err != nil {
				return err
			}
			if !sameBody || !sameButObservedAt(held, member) {
				return cli.InputError("the payment on check " + in.check + " is already sealed as leg " + send.Leg + " with other inputs; a check authorizes one payment, and a payer leg is never sealed again")
			}
			return s.writeSealedAt(ctx, c, a.d, i+1, r, in.out, result)
		}
		return fmt.Errorf("%w: the payer leg %s of check %s is not in your deal book", cli.ErrConflict, send.Leg, in.check)
	}
	if err := refuseExistingOut(in.out); err != nil {
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
	if afterLegSealedHook != nil {
		if err := afterLegSealedHook(); err != nil {
			return err
		}
	}
	if err := s.closeSend(ctx, in.dealID, in.check, sealed.Digest, in.status); err != nil {
		return err
	}
	result["digest"], result["seq"] = sealed.Digest, sealed.Seq
	actResult(result, &gate, auth)
	return writePush(c, sealed.Pair, in.out, result)
}

// afterSendStoredHook lets a test stop a run as a dying process would, after a
// send is stored and before its evidence is appended.
var afterSendStoredHook func() error

// afterLegSealedHook lets a test stop a run between sealing a payer leg and
// closing its send.
var afterLegSealedHook func() error

// answeredRef is the payer leg's payment_ref when the rail answers: the send's
// whole reference, with its type and qualifiers unchanged. Its value is the
// send's on an equal type and on a refund (whose send bound the refund's own
// reference); on x402 and the other types of a payment, where the send bound
// the reference known before paying, the leg carries the rail's own
// (--payment-ref), so it joins the payee leg.
func answeredRef(in settleIn, cb *xdealv1.CheckBody, send *v1Send) (map[string]any, error) {
	var bound map[string]any
	if err := json.Unmarshal([]byte(send.RefJSON), &bound); err != nil {
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

// sameRecordInput says a sealed record carries the body and marks a command
// would build.
func sameRecordInput(r xdealv1.Record, body xdealv1.Body, marks []string) (bool, error) {
	want, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	got, err := json.Marshal(r.Body)
	if err != nil {
		return false, err
	}
	return bytes.Equal(got, want) && slices.Equal(r.Block.Marks, marks), nil
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
	send, err := s.sendOf(ctx, dealID, reversedCheck)
	if err != nil || send == nil {
		return false, err
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
// the send), and a settled refund received is credited once. A record with
// nothing left to do returns before its Capsule is read.
func (s *dealSession) reconcileV1Settle(ctx context.Context, dealID string, book []xdealv1.Record, events []sealedEvent, rec xdealv1.Record, b *xdealv1.SettleBody, digest string) ([]sealedEvent, error) {
	var send *v1Send
	switch {
	case b.Leg == xdealv1.LegPayer && b.Check != nil:
		var err error
		if send, err = s.sendOf(ctx, dealID, b.Check.Digest); err != nil {
			return nil, err
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
		if err := s.closeSend(ctx, dealID, b.Check.Digest, digest, m.Status); err != nil {
			return nil, err
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

// refundFeeGuard refuses a new refund send on a cancel check that states a
// fee; a seam, so a test can make a send as one made before the rule.
var refundFeeGuard = true

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
