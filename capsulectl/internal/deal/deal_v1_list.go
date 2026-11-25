package deal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// v1 deal states in `deal list` (evidencebook#626 B4a). A deal with no own
// book passes deal_versions.state through (offered, offer_received,
// agreeing, declined, cancelled). A deal with a book is settled or open as
// the report computed now says, or not_checkpointed while the book has
// records after its last checkpoint, which a report rests on.
const (
	v1ListSettled         = "settled"
	v1ListOpen            = "open"
	v1ListNotCheckpointed = "not_checkpointed"
)

// v1DealIDs lists the store's x-deal-v1 deals in id order: none on a store
// with no deal_versions table yet.
func (s *dealSession) v1DealIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT deal_id FROM deal_versions WHERE version=? ORDER BY deal_id`, dealVersionV1)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// v1Listings lists the store's x-deal-v1 deals, in deal id order. It
// writes nothing: a deal's own book is opened only once the shelf shows it
// holds records (Shelf.Open creates a missing log), and its status is a
// keep copy computed now and never sealed. With in set (a --since/--until
// range), only the deals with a record in it are listed, and no state is
// computed for the others.
func (s *dealSession) v1Listings(ctx context.Context, p cli.Profile, in *dealListBounds) ([]dealListing, error) {
	ids, err := s.v1DealIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	books, err := s.shelf.Books(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dealListing, 0, len(ids))
	for _, id := range ids {
		l, ok, err := s.v1ListingOf(ctx, p, id, books, in)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, l)
		}
	}
	return out, nil
}

// v1ListingOf is one x-deal-v1 deal's listing: v0's columns (kind deal; the
// opened time is the offer's; the last record time is the own book's last
// record's, or the offer's with no book; the log id is empty with no book),
// and for --json the version, side, role, counterparty key and the offer's
// summary. books are the shelf's logs that hold records. ok is false for a
// deal with no record in in, when in is set; its state is not computed.
func (s *dealSession) v1ListingOf(ctx context.Context, p cli.Profile, dealID string, books []string, in *dealListBounds) (_ dealListing, ok bool, err error) {
	d, err := s.v1DealOf(ctx, dealID)
	if err != nil {
		return dealListing{}, false, err
	}
	l := dealListing{DealID: dealID, Kind: "deal", Version: dealVersionV1, Side: d.Side, Role: d.Role, CounterpartyKey: d.CounterpartyKey, State: d.State}
	if fp := shortKey(d.CounterpartyKey); fp != "" {
		// The plain line's counterparty column shows the counterparty's
		// key, short (Sherlock2, #733); --json carries it whole.
		l.CounterpartyFingerprint = &fp
	}
	sum := dealSummary{What: dealNotRecorded, Counterparty: dealNotRecorded, Outcome: d.State}
	if d.OfferDigest != "" {
		offer, err := s.offerByDigest(ctx, d.OfferDigest)
		if err != nil {
			return dealListing{}, false, err
		}
		if offer != nil {
			l.OpenedAt, l.LastRecordAt = offer.Record.Block.At, offer.Record.Block.At
			l.times = append(l.times, offer.Record.Block.At)
			if of, ok := proposeOf(offer.Record); ok {
				v1OfferSummary(&l, &sum, of)
			}
		}
	}
	var hasBook bool
	if d.OwnLogID != "" && slices.Contains(books, d.OwnLogID) {
		book, err := s.ownBookV1(ctx, d)
		if err != nil {
			return dealListing{}, false, err
		}
		if len(book) > 0 {
			hasBook = true
			l.LogID, l.LastRecordAt = d.OwnLogID, book[len(book)-1].Block.At
			for _, r := range book {
				l.times = append(l.times, r.Block.At)
			}
			legs, err := s.v1OwnLegs(ctx, d, book)
			if err != nil {
				return dealListing{}, false, fmt.Errorf("deal %s: %w", dealID, err)
			}
			l.OwnLegs = &legs
		}
	}
	if in != nil {
		if ok, err = v1InRange(l, *in); err != nil || !ok {
			return dealListing{}, false, err
		}
	}
	if hasBook {
		state, asOf, err := s.v1ListState(ctx, d)
		if err != nil {
			// One deal's report failure names the deal, in plain words
			// where they are known (a clock off), as deal report does.
			if plain := v1ReportError(dealID, p, err); plain != err {
				return dealListing{}, false, plain
			}
			return dealListing{}, false, fmt.Errorf("deal %s: %w", dealID, err)
		}
		l.State, sum.Outcome, l.OutcomeAsOf = state, state, asOf
		switch state {
		case v1ListSettled:
			l.SettledNote = v1ListSettledNote
		case v1ListNotCheckpointed:
			l.CheckpointNote = v1CheckpointNote(p)
		}
	}
	noLayer, err := s.v1NoLayer(ctx, d)
	if err != nil {
		return dealListing{}, false, err
	}
	sum.Text = v1SummaryText(sum, l.PaymentRows, noLayer)
	l.Summary = sum
	return l, true, nil
}

// v1OfferSummary fills what the offer says: the item, and the amount only
// when the offer has exactly one payment row (xdealv1.PaymentRows), else
// payment_rows.
func v1OfferSummary(l *dealListing, sum *dealSummary, of *xdealv1.ProposeBody) {
	if item := strings.TrimSpace(of.Terms.Item); item != "" {
		sum.What = item
	}
	rows := xdealv1.PaymentRows(of.TermsRows)
	if len(rows) != 1 {
		n := len(rows)
		l.PaymentRows = &n
		return
	}
	for _, row := range of.TermsRows.Rows {
		if row.ID != rows[0] {
			continue
		}
		var owed struct {
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		}
		if json.Unmarshal(row.Owed, &owed) == nil {
			sum.Amount = &dealSummaryAmount{AmountMinor: owed.AmountMinor, Currency: strings.ToUpper(owed.Currency), Basis: "offer_terms"}
		}
	}
}

// v1ListState computes the deal's status now from a keep copy that is never
// sealed: settled or open, or not_checkpointed while the book is past its
// checkpoint. Any other failure (a corrupt book, a record timed after now)
// is an error, never a state.
func (s *dealSession) v1ListState(ctx context.Context, d *v1Deal) (string, string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	asOf := cli.DealClock().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
	prov := &xdealv1.StatusProvider{Shelf: s.shelf, OfferPair: s.sentOfferPair}
	snap, err := v1ListSnapshot(ctx, prov, d.OwnLogID, xdealv1.ReportCopy{Audience: dealprofile.AudienceKeep, Nonce: hex.EncodeToString(nonce), AsOf: asOf})
	if err != nil {
		if strings.Contains(err.Error(), "has no checkpoint at its size") {
			return v1ListNotCheckpointed, asOf, nil
		}
		return "", "", err
	}
	if snap.Report.Settled {
		return v1ListSettled, asOf, nil
	}
	return v1ListOpen, asOf, nil
}

// v1SummaryText is the summary's line, as dealSummaryOf writes v0's.
func v1SummaryText(sum dealSummary, paymentRows *int, noLayer bool) string {
	amount := "amount not recorded"
	switch {
	case sum.Amount != nil:
		amount = formatMoney(sum.Amount.AmountMinor, sum.Amount.Currency) + " in the offer"
	case paymentRows != nil && *paymentRows > 1:
		amount = "several payments in the offer"
	case paymentRows != nil:
		amount = "no payment row in the offer"
	}
	who := "counterparty: no name recorded (key only)"
	if noLayer {
		who = "counterparty: keeps no signed records"
	}
	return strings.Join([]string{sum.What, who, amount, sum.Outcome}, " · ")
}

// v1InRange says whether any of an x-deal-v1 deal's records (its offer, its
// own book) falls in the range, Since inclusive and Until exclusive.
func v1InRange(l dealListing, b dealListBounds) (bool, error) {
	for _, at := range l.times {
		ts, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return false, cli.Hint(cli.ErrConflict, "deal "+l.DealID+" holds a record whose time does not read: "+at)
		}
		if b.contains(ts) {
			return true, nil
		}
	}
	return false, nil
}

// v1RangeNote is the range object's word on x-deal-v1 deals (#626 B4a): a
// historical v1 report cannot be computed, so each is listed with its state
// now, and none is in the totals or the disclosures yet (evidencebook#732).
const v1RangeNote = "x-deal-v1 deals are listed when any of their records falls in the range, each with its state now (outcome_as_of); they are not yet counted in the totals or the disclosures"

// mergeListings puts v1 listings among v0's, in deal id order.
func mergeListings(v0, v1 []dealListing) []dealListing {
	out := append(append([]dealListing{}, v0...), v1...)
	slices.SortStableFunc(out, func(a, b dealListing) int { return strings.Compare(a.DealID, b.DealID) })
	return out
}

// v1ListSnapshot is the provider's ReportSnapshot, a seam for tests.
var v1ListSnapshot = func(ctx context.Context, p *xdealv1.StatusProvider, logID string, c xdealv1.ReportCopy) (xdealv1.ReportSnapshot, error) {
	return p.ReportSnapshot(ctx, logID, c)
}

// v1ListSettledNote is said beside a settled v1 deal (Sherlock2, #733, as
// #727's settled_note): settled is computed from the agent's records, which
// the person has not signed. evidencebook#729 replaces it.
const v1ListSettledNote = "settled: computed from the agent's records; no act carries the person's own signature yet (agent-action-capsule#627)"

// v1CheckpointNote explains not_checkpointed with the profile's cadence, as
// deal report's refusal does (#727).
func v1CheckpointNote(p cli.Profile) string {
	every := "about every 5m"
	if cfg, err := dealCadenceOf(p); err == nil {
		every = "about every " + ShortDuration(cfg.Interval)
	}
	return "not_checkpointed: records since the last checkpoint; the next cadence tick covers it (" + every + "), then list again"
}

// v1ListFooter is the plain list's footer for its v1 rows: the settled note
// when any row is settled, the checkpoint note when any is not
// checkpointed. It goes to stderr, so the tab-separated lines stay a table.
func v1ListFooter(list []dealListing) []string {
	var out []string
	for _, note := range []func(dealListing) string{
		func(l dealListing) string { return l.SettledNote },
		func(l dealListing) string { return l.CheckpointNote },
	} {
		for _, l := range list {
			if n := note(l); n != "" {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// shortKey is a key's first 16 characters, the plain list's counterparty
// column for an x-deal-v1 deal; empty while no counterparty key is known.
func shortKey(k string) string {
	if len(k) > 16 {
		return k[:16]
	}
	return k
}

// v1OwnLeg is one of the profile's own payment legs on an x-deal-v1 deal:
// which leg (payer or payee), its status (pending, settled or failed) and
// its payment reference's type, as the leg's settlement member records them.
// It carries no reference value, amount or counterparty (Sherlock2,
// evidencebook#921: the listing stays local and never enters a disclosure,
// share or bundle). A host counts its own settlements from these: the
// Goose loop node's settlement disclosure maps the type to a rail.
type v1OwnLeg struct {
	Leg            string `json:"leg"`
	Status         string `json:"status"`
	PaymentRefType string `json:"payment_ref_type"`
}

// v1OwnLegs are the latest observation of each payment the deal's own book
// (book, in sequence order) holds a leg for: a leg a later own leg
// supersedes is left out, and so are refund and remedy legs (reverses,
// remedy_for), which are not the deal's payment. It reads the book only,
// so it needs no checkpoint.
func (s *dealSession) v1OwnLegs(ctx context.Context, d *v1Deal, book []xdealv1.Record) ([]v1OwnLeg, error) {
	superseded := map[string]bool{}
	for _, r := range book {
		if b, ok := settleOf(r); ok && b.Supersedes != nil {
			superseded[b.Supersedes.Digest] = true
		}
	}
	out := []v1OwnLeg{}
	for i, r := range book {
		b, ok := settleOf(r)
		if !ok || b.Reverses != nil || b.RemedyFor != nil {
			continue
		}
		digest, err := xdealv1.Digest(r)
		if err != nil {
			return nil, err
		}
		if superseded[digest] {
			continue
		}
		raw, err := s.ownSettleMember(ctx, d, i+1)
		if err != nil {
			return nil, err
		}
		var m struct {
			Status     string `json:"status"`
			PaymentRef *struct {
				Type string `json:"type"`
			} `json:"payment_ref"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%w: the leg at %d of the deal book has no settlement member that reads", cli.ErrConflict, i+1)
		}
		leg := v1OwnLeg{Leg: b.Leg, Status: m.Status}
		if m.PaymentRef != nil {
			leg.PaymentRefType = m.PaymentRef.Type
		}
		out = append(out, leg)
	}
	return out, nil
}
