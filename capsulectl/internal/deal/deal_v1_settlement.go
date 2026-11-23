package deal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/spf13/cobra"
)

// `deal settlement` on an x-deal-v1 deal (evidencebook#626 B4d). A v1 deal
// reads the other side's legs from the counted prefix it holds, so v0's
// --leg, --object, --payer-key and --payee-key do not apply. It prints, for
// each row whose owed is an amount, the library's payment result as the
// status read it (xdealv1.ReportSnapshot.Payments), or why the check was not
// run (PaymentsNotRun), beside the report row's check word. Nothing is
// computed here, and nothing is written: the read is a keep copy computed
// now and never sealed.

// v1SettlementRow is one row of `deal settlement` on a v1 deal.
type v1SettlementRow struct {
	ID string `json:"id"`
	// ReportCheck is the report row's verdict (passed, failed, not_run,
	// not_recomputed), the report's word for the same check Payment.Check
	// gives as pass or fail.
	ReportCheck string `json:"report_check"`
	// Payment is the payment check on the legs, exactly as the library
	// gives it; null when the status ran no check for the row (see
	// not_run). A check that ran without a verdict has check "" and a
	// reason (several_payments, a refund leg, ...).
	Payment *xdealv1.PaymentResult `json:"payment"`
	// NotRun says why the payment check was not run: not_recomputed,
	// no_offer_pair or no_acceptor_key; NotRunNote says it in plain words
	// (v1NotRunNotes).
	NotRun     string `json:"not_run,omitempty"`
	NotRunNote string `json:"not_run_note,omitempty"`
	// Notes say what a reader must know about the row's payment record:
	// one-sided, reference recorded after payment, the private reference
	// type (v1PaymentRowNotes, evidencebook#771).
	Notes []string `json:"notes,omitempty"`
}

// v1NotRunNotes say why a payment check was not run and what the user can
// do (Sherlock2, #736). not_recomputed needs none: the row's verdict says it.
var v1NotRunNotes = map[string]string{
	xdealv1.NotRunNoOfferPair:   "this store holds the offer but not its signed copy, so the payment check cannot run here; receive the offer file again (deal receive) to keep it",
	xdealv1.NotRunNoAcceptorKey: "your agreement names no key for the other side: it keeps no signed records of its own, so this record is one-sided and the payment check, which matches both sides' legs, cannot run on this deal",
}

// v1SettlementFlags are v0's flags that a v1 deal does not take.
var v1SettlementFlags = []string{"leg", "object", "payer-key", "payee-key"}

func runV1Settlement(c *cobra.Command) (err error) {
	for _, name := range v1SettlementFlags {
		if c.Flags().Changed(name) {
			return cli.InputError("--" + name + " names an x-deal-v0 deal's legs; an x-deal-v1 deal reads the other side's legs from the records you have received (`deal receive`)")
		}
	}
	dealID, _ := c.Flags().GetString("deal")
	p, err := cli.Selected(c)
	if err != nil {
		return err
	}
	ctx := c.Context()
	s, err := openDealStoreRead(ctx, p)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return err
	}
	if d == nil {
		return cli.InputError("unknown deal: " + dealID)
	}
	if d.OwnAgreementID == "" {
		return cli.InputError("deal " + dealID + " has no agreement of yours recorded yet, so no settlement to read: your deal book opens at your agreement (`deal agree`)")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	asOf := cli.DealClock().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
	prov := &xdealv1.StatusProvider{Shelf: s.shelf, OfferPair: s.sentOfferPair}
	snap, err := prov.ReportSnapshot(ctx, d.OwnLogID, xdealv1.ReportCopy{Audience: dealprofile.AudienceKeep, Nonce: hex.EncodeToString(nonce), AsOf: asOf})
	if err != nil {
		return v1ReportError(dealID, p, err)
	}
	var ids []string
	for _, r := range snap.Report.Rows {
		if _, ok := snap.Payments[r.ID]; ok || snap.PaymentsNotRun[r.ID] != "" {
			ids = append(ids, r.ID)
		}
	}
	notes, err := s.v1PaymentRowNotes(ctx, d, ids, snap.Payments)
	if err != nil {
		return err
	}
	rows := []v1SettlementRow{}
	for _, r := range snap.Report.Rows {
		row := v1SettlementRow{ID: r.ID, ReportCheck: r.Check, NotRun: snap.PaymentsNotRun[r.ID]}
		row.NotRunNote = v1NotRunNotes[row.NotRun]
		row.Notes = notes[r.ID]
		if res, ok := snap.Payments[r.ID]; ok {
			res := res
			row.Payment = &res
		} else if row.NotRun == "" {
			// No owed amount: not a payment row.
			continue
		}
		rows = append(rows, row)
	}
	out := map[string]any{"deal_id": dealID, "as_of": asOf, "rows_state": snap.Report.RowsState, "rows": rows, "settled_note": personSignatureNote}
	if snap.Report.RowsState != xdealv1.RowsListed {
		// Before the deal starts the report lists no rows: say so, rather
		// than an empty list that reads as no payment rows.
		out["rows_note"] = "the deal has not started (" + snap.Report.Start.State + "), so its rows are not listed yet"
	}
	return cli.Output(c, out)
}
