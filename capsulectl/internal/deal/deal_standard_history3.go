package deal

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/action-state-group/evidencebook/rowstatus"
)

// The History card standard_history/3 (agent-action-capsule
// spec/standard-history-v0.md §11, cited as SH) over x-deal-v1 deal books
// (evidencebook-deal#213; evidencebook docs/design/461-deal-profile.md §10,
// cited as DP). A /3 card is counted from each book's latest fact on the
// shelf log as of a stamp, not from the deals' records, so the host reads the
// whole-shelf handover (Cadence.Handover, the replay VerifyHandover checks),
// takes each deal's status value from its latest fact's opening, resolves the
// times in it, computes each row's status with the library's rule
// (rowstatus.Compute) as of the anchor's time, reads its disputes as of that
// same time (SH §11.1 "One card, one time", rowsAndDisputes3), and gives the
// entries to AAC's standardhistory.DeriveAs. /1 and /2 are served as before for v0 deals.

// errNotMigratedForCard is a /3 card asked of a store still in the old
// layout, which has no shelf log of book facts to count.
var errNotMigratedForCard = errors.New("a standard_history/3 or /4 card needs a deal store in the new layout")

// standardHistory3 derives the /3 card as of tick n, or the newest finished
// tick when n is 0. The tick must be a stamp's: Handover refuses another.
func (s *dealSession) standardHistory3(ctx context.Context, tick uint64) (standardHistoryCard, error) {
	return s.standardHistoryV1(ctx, tick, standardhistory.Derivation3)
}

// standardHistoryV1 derives the card of x-deal-v1 deals under derivation:
// /3, or /4 (SH §12), which is /3 except that it leaves no deal out for want
// of a witness time and discloses such a deal with no_witness_time
// (evidencebook-deal#230; Steven u616).
func (s *dealSession) standardHistoryV1(ctx context.Context, tick uint64, derivation string) (standardHistoryCard, error) {
	settings, ok, err := readSHSettings(s.p)
	if err != nil {
		return standardHistoryCard{}, err
	}
	if !ok {
		return standardHistoryCard{}, errNoHolderKind()
	}
	policy, err := readSHPolicy(s.p)
	if err != nil {
		return standardHistoryCard{}, err
	}
	cadence, err := s.requireMigrated(ctx)
	if err != nil {
		if errors.Is(err, errNotMigratedSentinel) {
			return standardHistoryCard{}, shRefusal("a standard_history/3 or /4 card needs a deal store in the new layout: see `deal set-version --help`", errors.Join(errNotMigratedForCard, err))
		}
		return standardHistoryCard{}, err
	}
	anchor, err := cadence.StandardHistoryAnchor(ctx, tick)
	if errors.Is(err, evidencebook.ErrNotFound) {
		return standardHistoryCard{}, shNoTick(fmt.Errorf("%w (run `deal tick` first)", errNoTick))
	}
	if err != nil {
		return standardHistoryCard{}, err
	}
	if settings.changedAfter(cutOf(s.p, anchor.Tick.N, anchor.Tick.At)) {
		return standardHistoryCard{}, fmt.Errorf("%w: %w", cli.Hint(errCardNotYet{}, staleAnchorMessage(ctx, cadence)), errStaleAnchor)
	}
	// Stamp times are read from every checkpoint at or after a stamp, so they
	// agree with the handover (which holds none after its anchor) only for the
	// newest tick: an older anchor could resolve a time from a later receipt.
	if newest, err := cadence.StandardHistoryAnchor(ctx, 0); err != nil {
		return standardHistoryCard{}, err
	} else if newest.Tick.N != anchor.Tick.N {
		return standardHistoryCard{}, cli.InputError("a standard_history/3 or /4 card is as of the newest checkpoint only")
	}
	handover, err := cadence.Handover(ctx, anchor.Tick.N)
	if err != nil {
		return standardHistoryCard{}, err
	}
	_, p, err := s.cadenceOf(ctx)
	if err != nil {
		return standardHistoryCard{}, err
	}
	witnesses, err := pinnedWitnessDirectory(p)
	if err != nil {
		return standardHistoryCard{}, err
	}
	checkpointKey, err := cli.PrivateKey(p.Checkpoint.Signing)
	if err != nil {
		return standardHistoryCard{}, err
	}
	// The shelf log is the holder's: its checkpoints are signed by the
	// profile's own checkpoint key, whose key id is its lowercase hex.
	pinned := hex.EncodeToString(checkpointKey.Public().(ed25519.PublicKey))
	replay, err := evidencebook.VerifyHandover(handover, pinned, witnesses)
	if err != nil {
		return standardHistoryCard{}, err
	}
	coverage, err := s.coverageStartTime(ctx, cadence, anchor.Tick.N, replay.CoverageStart)
	if err != nil {
		return standardHistoryCard{}, err
	}
	asOf := anchor.AsOf
	asOf.CoverageStartTime = coverage

	deals, err := s.dealEntries3(ctx, replay.Latest, asOf.AnchorTime, derivation)
	if err != nil {
		return standardHistoryCard{}, err
	}
	slices.SortFunc(deals, func(a, b standardhistory.Deal) int { return strings.Compare(a.Deal, b.Deal) })
	controller := map[string]string{"person": "person_declared", "node": "node_declared"}[settings.HolderKind]
	card, err := standardhistory.DeriveAs(derivation, settings.HolderKind, controller, asOf, policy, deals)
	if err != nil {
		return standardHistoryCard{}, fmt.Errorf("%w: standard history: %v", evidencebook.ErrInvalid, err)
	}
	card, err = canonicalCard(card)
	if err != nil {
		return standardHistoryCard{}, err
	}
	properties, err := standardhistory.CheckAs(derivation, card, &policy, &asOf, deals)
	if err != nil {
		return standardHistoryCard{}, fmt.Errorf("%w: standard history: %v", evidencebook.ErrInvalid, err)
	}
	for _, property := range standardhistory.Properties {
		// counts_recompute needs the handover a verifier is given (SH §11.7);
		// the host checks the other five against the very inputs it derived from.
		if property != "counts_recompute" && properties[property] != standardhistory.Pass {
			return standardHistoryCard{}, fmt.Errorf("%w: standard history: the card fails %s against its own inputs", evidencebook.ErrInvalid, property)
		}
	}
	return standardHistoryCard{Card: card, Policy: policy, AsOf: asOf, Deals: deals, Tick: anchor.Tick, Derivation: derivation}, nil
}

// canonicalCard is a derived card in its JCS octets.
func canonicalCard(derived []byte) ([]byte, error) {
	decoded, err := evidencerequest.DecodeJSON(derived)
	if err != nil {
		return nil, err
	}
	return evidencerequest.EncodeJCS(decoded)
}

// coverageStartTime is the time of the shelf checkpoint that ends the format
// switch's stamp (SH §11.5): the finished tick whose checkpoint covers
// exactly coverageEntries entries of the shelf log, in the card's time form.
func (s *dealSession) coverageStartTime(ctx context.Context, c *evidencebook.Cadence, last, coverageEntries uint64) (string, error) {
	for n := uint64(1); n <= last; n++ {
		t, err := c.Get(ctx, n)
		if errors.Is(err, evidencebook.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if t.CheckpointSize == 0 || mmrLeafCount(t.CheckpointSize) != coverageEntries {
			continue
		}
		a, err := c.StandardHistoryAnchor(ctx, n)
		if err != nil {
			return "", err
		}
		return a.AsOf.AnchorTime, nil
	}
	return "", fmt.Errorf("%w: no finished checkpoint ends the format switch's stamp (shelf log entry %d)", evidencebook.ErrCorrupt, coverageEntries)
}

// dealEntries3 makes the /3 deal entry of each v1 deal book among the latest
// facts, as of the anchor's time at (SH §11.1, §11.2). A journal fact, a book
// of another profile, and a deal that has not started give no entry.
func (s *dealSession) dealEntries3(ctx context.Context, latest []evidencebook.HandoverLatest, at, derivation string) ([]standardhistory.Deal, error) {
	var deals []standardhistory.Deal
	for _, l := range latest {
		if l.Fact.Kind != evidencebook.FactKindBook || l.Fact.Profile != xdealv1.Profile {
			continue
		}
		var status xdealv1.StatusValue
		if err := cli.DecodeJSON(l.StatusOpening.Value, &status); err != nil {
			return nil, fmt.Errorf("%w: the status opening of book %s: %v", evidencebook.ErrCorrupt, l.Fact.BookID, err)
		}
		deal, started, err := s.dealEntry3(ctx, l.Fact.BookID, status, at, derivation)
		if err != nil {
			return nil, err
		}
		if started {
			deals = append(deals, deal)
		}
	}
	return deals, nil
}

// dealEntry3 is one book's entry and whether the deal has started at at.
func (s *dealSession) dealEntry3(ctx context.Context, bookID string, v xdealv1.StatusValue, at, derivation string) (standardhistory.Deal, bool, error) {
	keys := make([]string, 0, len(v.Witnesses))
	for _, w := range v.Witnesses {
		keys = append(keys, w.KeyID)
	}
	stamps := map[uint64]string{}
	resolve := func(t xdealv1.StatusTime) error {
		if t.Stamp == 0 || t.Known != "" {
			return nil
		}
		if _, done := stamps[t.Stamp]; done {
			return nil
		}
		when, ok, err := s.shelf.StampTimeByKey(ctx, t.Stamp, keys)
		if err != nil {
			return err
		}
		if ok {
			stamps[t.Stamp] = when.UTC().Format(time.RFC3339)
		}
		return nil
	}
	times := statusTimes(v)
	for _, t := range times {
		if err := resolve(t); err != nil {
			return standardhistory.Deal{}, false, err
		}
	}
	startIn := xdealv1.StartInput{
		At: at, Side: v.Start.Side, CounterpartyLayer: v.Start.CounterpartyLayer, AgreeBy: v.Start.AgreeBy,
		CounterpartAgreementHeld: v.Start.CounterpartAgreementHeld, SameOfferAndRows: v.Start.SameOfferAndRows,
		OffererNamesAcceptor: v.Start.OffererNamesAcceptor, AcceptorNamesOfferer: v.Start.AcceptorNamesOfferer,
		CancellationNamedFirst: v.Start.CancellationNamedFirst,
	}
	if t := v.Start.OffererAgreementTime; !t.IsNone() {
		if t.Known != "" {
			startIn.OffererAgreementTime = &t.Known
		} else if w, ok := stamps[t.Stamp]; ok {
			startIn.OffererAgreementTime = &w
		}
	}
	// A deal with a counterparty that runs no layer starts at the holder's
	// first act (xdealv1.Start, Steven u611): its time, known or stamped, or
	// none yet.
	if fa := v.Start.FirstAct; fa != nil {
		startIn.FirstAct = &xdealv1.FirstAct{}
		if t := fa.Time; t.Known != "" {
			startIn.FirstAct.Time = &t.Known
		} else if w, ok := stamps[t.Stamp]; ok && !t.IsNone() {
			startIn.FirstAct.Time = &w
		}
	}
	started, err := xdealv1.Start(startIn)
	if err != nil {
		return standardhistory.Deal{}, false, fmt.Errorf("%w: book %s: %v", evidencebook.ErrCorrupt, bookID, err)
	}
	if started.State != xdealv1.StateStarted {
		return standardhistory.Deal{}, false, nil
	}
	rows, disputes, err := rowsAndDisputes3(v, at, stamps)
	if err != nil {
		return standardhistory.Deal{}, false, fmt.Errorf("%w: book %s: %v", evidencebook.ErrCorrupt, bookID, err)
	}
	deal := standardhistory.Deal{
		Deal: bookID, AgreedAt: v.AgreedAt, CounterpartyClass: v.CounterpartyKind,
		Role: map[string]string{"seller": "provided", "buyer": "bought"}[v.Role], Rows: rows,
		Happened: happened3(v), Disputes: disputes,
	}
	if a, ok := bandAmount(v.ValueBand); ok {
		deal.Amount = a
	} else {
		deal.Unpriced = true
	}
	if err := agreedNoLaterThanWitnessed(ctx, s.shelf.RecordTimeByKey, bookID, v.AgreedAt, keys); err != nil {
		return standardhistory.Deal{}, false, err
	}
	timed, err := s.witnessedTime(ctx, bookID, v, stamps, keys)
	if err != nil {
		return standardhistory.Deal{}, false, err
	}
	switch {
	case timed:
	case derivation == standardhistory.Derivation4:
		deal.NoWitnessTime = true
	case len(keys) > 0:
		// /3 leaves out a deal whose agreement names a witness that timed none
		// of its records (SH §11.2 left_out); /4 counts it.
		none := "no_witness_time"
		deal.LeftOut = &none
	}
	return deal, true, nil
}

// agreedNoLaterThanWitnessed refuses the card when a deal's agreed_at is
// later than its first witnessed time by more than xdealv1.AgreedAtSkew (SH
// §11.2 "Agreed no later than witnessed", AAC#696). The first witnessed time
// is the record time of the holder's own book's first record, its agreement,
// which a stamp covering any later record covers too (recordTime is
// Shelf.RecordTimeByKey). The other side's times are not read: it may have
// agreed first. A deal whose agreement names no witness, or that no named
// witness timed, is not checked.
func agreedNoLaterThanWitnessed(ctx context.Context, recordTime func(context.Context, string, uint64, []string) (time.Time, bool, error), bookID, agreedAt string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	first, ok, err := recordTime(ctx, bookID, 1, keys)
	if err != nil || !ok {
		return err
	}
	if err := xdealv1.CheckAgreedAt(agreedAt, first); err != nil {
		return shRefusal(fmt.Sprintf("deal %s: its agreed_at is later than its first witnessed time, so its entry cannot be filled (SH §11.2)", bookID), err)
	}
	return nil
}

// witnessedTime is whether the deal holds a time from a witness its agreement
// names. The status times may miss the holder's own agreement, the book's first
// record, which has a time whenever any later record has one (a stamp that
// covers a record covers those before it).
func (s *dealSession) witnessedTime(ctx context.Context, bookID string, v xdealv1.StatusValue, stamps map[uint64]string, keys []string) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	if holdsWitnessTime(v, stamps) {
		return true, nil
	}
	_, ok, err := s.shelf.RecordTimeByKey(ctx, bookID, 1, keys)
	return ok, err
}

// statusTimes are the times a status value names, for the stamps to resolve.
func statusTimes(v xdealv1.StatusValue) []xdealv1.StatusTime {
	out := []xdealv1.StatusTime{v.Start.OffererAgreementTime}
	if v.Start.FirstAct != nil {
		out = append(out, v.Start.FirstAct.Time)
	}
	for _, r := range v.Rows {
		out = append(out, r.RemedyMade)
		out = append(out, r.StatementNamedByOther...)
		for _, n := range r.Input.Named {
			out = append(out, n.Time)
		}
		for _, st := range r.Input.Statements {
			out = append(out, st.Time)
		}
		for _, d := range r.Input.Disputes {
			out = append(out, d.Time)
		}
		for _, w := range r.Input.Withdrawals {
			out = append(out, w.Time)
		}
		for _, ru := range r.Input.Rulings {
			out = append(out, ru.Time)
		}
		for _, m := range r.Input.Remedies {
			out = append(out, m.Time)
		}
	}
	return out
}

// holdsWitnessTime is whether any record time of the deal is known from a
// witness the agreement names: a known time, or a stamp that resolved
// (SH §11.2 left_out).
func holdsWitnessTime(v xdealv1.StatusValue, stamps map[uint64]string) bool {
	for _, t := range statusTimes(v) {
		if t.Known != "" {
			return true
		}
		if _, ok := stamps[t.Stamp]; ok && t.Stamp != 0 {
			return true
		}
	}
	return false
}

// rowResult3 is a row's result (SH §11.2): who owed it, as holder or
// counterparty (DP §10); its status and value; and its remedy.
func rowResult3(role string, r xdealv1.StatusRow, res rowstatus.Result) standardhistory.Row {
	out := standardhistory.Row{OwedBy: "counterparty", Status: res.Status, Value: res.Value, Local: res.Local}
	if r.Input.Row.OwedBy == role {
		out.OwedBy = "holder"
	}
	switch {
	case res.Status == "ruled":
		out.Remedy = res.Remedy
	case res.Value == "not_met" && (res.Status == "agreed_so_far" || res.Status == "agreed" || res.Status == "uncontested"):
		// An unmet row that is not ruled: its remedy is the host profile's,
		// made when the owing side's book holds a remedy of the row (SH-16).
		out.Remedy = rowstatus.RemedyNone
		if out.OwedBy == "holder" && !r.RemedyMade.IsNone() {
			out.Remedy = rowstatus.RemedyMade
		}
	}
	return out
}

// happened3 is how the deal happened (SH §5.5) from the status value: the
// other side's half held under an accepted stamp proof is stamped by both,
// the holder's own record of the agreement otherwise (DP §10).
func happened3(v xdealv1.StatusValue) string {
	if v.CounterpartyLayer && v.CounterpartyProofHeld {
		return "stamped_by_both"
	}
	return "receipt_only"
}

// rowsAndDisputes3 is a deal's row results and its disputes as of at (SH
// §11.1 "One card, one time"): both read the same records, each row's input
// resolved once with the stamps' times (xdealv1.ResolveRow), so a record
// whose time is after at exists for neither.
func rowsAndDisputes3(v xdealv1.StatusValue, at string, stamps map[uint64]string) ([]standardhistory.Row, []standardhistory.Dispute, error) {
	rows := make([]standardhistory.Row, 0, len(v.Rows))
	inputs := make([]rowstatus.Input, 0, len(v.Rows))
	for _, r := range v.Rows {
		in, err := xdealv1.ResolveRow(r, at, stamps)
		if err != nil {
			return nil, nil, fmt.Errorf("row %s: %v", r.ID, err)
		}
		res, err := rowstatus.Compute(in)
		if err != nil {
			return nil, nil, fmt.Errorf("row %s: %v", r.ID, err)
		}
		rows = append(rows, rowResult3(v.Role, r, res))
		inputs = append(inputs, in)
	}
	disputes, err := disputesAsOf(v, inputs, at)
	if err != nil {
		return nil, nil, err
	}
	return rows, disputes3(v.Role, disputes), nil
}

// disputesAsOf is the status value's disputes as they stand at at, from the
// rows' inputs resolved as of at (SH §11.1 "One card, one time"). The status
// value is written with its fact, before any anchor, so its disputes read
// every record the books held then; the card reads only those that exist as
// of at, by rowstatus's test: a record whose time is after at does not yet
// exist, and one with no time exists.
//
// The status value builds its disputes and each row's input.disputes in one
// loop and one order (per row, the buyer's book then the seller's, each in
// book order), so the k-th dispute of a row is its k-th input dispute, which
// carries the record's time; any other shape is a corrupt status value.
//
// A dispute whose time is after at is no entry. One the status value has
// withdrawn is withdrawn as of at when a withdrawal of its claimant on the
// row exists by then and is not earlier than the dispute (a book's times
// never decrease). The status value does not say which withdrawal withdrew
// it, so a held withdrawal that came before the dispute in its book with
// the same time, or with no time on one of the two, also reads as
// withdrawing it; that is how the row rule reads every held withdrawal of
// the claimant (rowstatus withdraws all its disputes on the row), so the
// tally and the row still agree. A withdrawal after the dispute in its book
// is never missed. Otherwise it is resolved by the ruling rowstatus's rule 1
// reads, the row's first final ruling held as of at, none while the case
// book forked: against whom is the row's ruling_against, the profile keeping
// one final ruling per row in an unforked case book (xdealv1.SealRuling);
// with none held, the dispute is open.
func disputesAsOf(v xdealv1.StatusValue, inputs []rowstatus.Input, at string) ([]xdealv1.StatusDispute, error) {
	if len(inputs) != len(v.Rows) {
		return nil, fmt.Errorf("%d row inputs for %d rows", len(inputs), len(v.Rows))
	}
	out := []xdealv1.StatusDispute{}
	next := 0
	for k, row := range v.Rows {
		in := inputs[k]
		ruled, err := heldFinalRuling(row.ID, in, at)
		if err != nil {
			return nil, err
		}
		for _, d := range in.Disputes {
			if next >= len(v.Disputes) {
				return nil, fmt.Errorf("row %s has more dispute records than the status value's disputes", row.ID)
			}
			sd := v.Disputes[next]
			next++
			if sd.Row != row.ID || sd.Claimant != d.Side {
				return nil, fmt.Errorf("the status value's dispute %d (row %s, claimant %s) is not row %s's dispute by the %s", next-1, sd.Row, sd.Claimant, row.ID, d.Side)
			}
			if !heldAt(d.Time, at) {
				continue
			}
			outcome := xdealv1.OutcomeOpen
			switch {
			case sd.Outcome == xdealv1.OutcomeWithdrawn && withdrawnAt(in.Withdrawals, d, at):
				outcome = xdealv1.OutcomeWithdrawn
			case !ruled:
			case row.RulingAgainst == nil:
				outcome = xdealv1.OutcomeNoFinding
			case *row.RulingAgainst == d.Side:
				outcome = xdealv1.OutcomeAgainstClaimant
			default:
				outcome = xdealv1.OutcomeForClaimant
			}
			sd.Outcome = outcome
			out = append(out, sd)
		}
	}
	if next != len(v.Disputes) {
		return nil, fmt.Errorf("the status value has %d disputes, its rows %d dispute records", len(v.Disputes), next)
	}
	return out, nil
}

// heldAt says whether a record with time t exists as of at: times are in
// rowstatus's one form, YYYY-MM-DDTHH:MM:SSZ, so they compare as strings.
func heldAt(t rowstatus.Time, at string) bool {
	return t == nil || *t <= at
}

// heldFinalRuling says whether the row is resolved as of at by the ruling
// rowstatus's rule 1 reads: its first final ruling held then, none while its
// case book forked. A held final ruling that is not the row's first final one
// is refused: the status value's ruling_against is the first one's.
func heldFinalRuling(row string, in rowstatus.Input, at string) (bool, error) {
	if in.ConflictingFinalRulings {
		return false, nil
	}
	first := true
	for _, r := range in.Rulings {
		if !r.Final {
			continue
		}
		if heldAt(r.Time, at) {
			if !first {
				return false, fmt.Errorf("row %s: a final ruling held at %s follows one that is not, in an unforked case book", row, at)
			}
			return true, nil
		}
		first = false
	}
	return false, nil
}

// withdrawnAt says whether a withdrawal of d's claimant on the row exists as
// of at and is not earlier than d.
func withdrawnAt(ws []rowstatus.Withdrawal, d rowstatus.Dispute, at string) bool {
	return slices.ContainsFunc(ws, func(w rowstatus.Withdrawal) bool {
		return w.Side == d.Side && heldAt(w.Time, at) && (w.Time == nil || d.Time == nil || *w.Time >= *d.Time)
	})
}

// disputes3 maps a deal's disputes to SH §5.4's entries with DP §10's words:
// the side is raised or received by the claimant's role (role is the
// holder's), and a ruling's outcome is for or against the holder.
func disputes3(role string, disputes []xdealv1.StatusDispute) []standardhistory.Dispute {
	out := []standardhistory.Dispute{}
	for _, d := range disputes {
		raised := d.Claimant == role
		side, outcome := "received", d.Outcome
		if raised {
			side = "raised"
		}
		switch d.Outcome {
		case xdealv1.OutcomeForClaimant:
			outcome = map[bool]string{true: "for", false: "against"}[raised]
		case xdealv1.OutcomeAgainstClaimant:
			outcome = map[bool]string{true: "against", false: "for"}[raised]
		}
		out = append(out, standardhistory.Dispute{Side: side, Outcome: outcome, Forum: forumClass(d.Forum)})
	}
	return out
}

// bandAmount is the band's floor as an amount, since a status value
// carries the band and no amount (DP §12 Q15): 10^decade of the currency's
// unit, whose decade is the band's (SH §5.3). The card shows the band only, so
// no other digit of the price is claimed or kept. The entry is NOT a price:
// the holder's own copy carries these entries beside the card, and a reader
// of them must not take the floor for what was paid. The deal entry has no
// member to say so, and none is invented here: until evidencebook design Q15
// rules (DP section 12), a /3 entry's amount is a band floor (Sherlock,
// evidencebook-deal#213), and this function is what Q15's ruling replaces.
// A status value has one null band for a zero or an unknown price, so a zero
// price is not told apart and is counted unpriced.
func bandAmount(b *xdealv1.ValueBand) (standardhistory.Amount, bool) {
	if b == nil {
		return standardhistory.Amount{}, false
	}
	if b.Decade >= 0 {
		return standardhistory.Amount{Value: "1" + strings.Repeat("0", b.Decade), AssetCode: b.Currency, AssetScale: 0}, true
	}
	return standardhistory.Amount{Value: "1", AssetCode: b.Currency, AssetScale: -b.Decade}, true
}

// forumClass is the class SH §5.4 counts a forum by: x-deal-v1 forums are
// auto:<x>, network:<id>, rail:<program> or external:<class>, and the card
// counts the prefix.
func forumClass(forum string) string {
	class, _, _ := strings.Cut(forum, ":")
	return class
}
