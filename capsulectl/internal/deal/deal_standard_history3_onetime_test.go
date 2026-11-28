package deal

import (
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One card, one time (SH §11.1, evidencebook#1087): a /3 or /4 card reads a
// deal's disputes as of T, the anchor's time, from the same records as its
// row results. The seller holds the card; the seller owes row r, due
// 2026-11-05, contest window ending 2026-11-10, and said it met the row on
// 2026-11-04, which the buyer's book named on 2026-11-05.

const (
	otDue       = "2026-11-05T00:00:00Z"
	otWindowEnd = "2026-11-10T00:00:00Z"
	otDispute   = "2026-11-06T00:00:00Z"
	otRuling    = "2026-11-12T00:00:00Z"
)

func otStr(s string) *string { return &s }

// otDeal is the seller's status value with one row and the given records on
// it; disputes is what the status value's disputes say of them, read with
// every record its books held when the fact was written.
func otDeal(row xdealv1.StatusRowInput, against *string, disputes ...xdealv1.StatusDispute) xdealv1.StatusValue {
	base := xdealv1.StatusRowInput{
		Parties: []string{"buyer", "seller"},
		Row:     rowstatus.Row{ID: "r", OwedBy: "seller", Due: rowstatus.Date{At: otStr(otDue)}, WindowEnd: rowstatus.Date{At: otStr(otWindowEnd)}},
		Statements: []xdealv1.StatusStatement{
			{Side: "seller", Value: "met", Time: xdealv1.KnownTime("2026-11-04T00:00:00Z")},
		},
	}
	if row.Row.ID != "" {
		base.Row = row.Row
		base.Named = row.Named
	}
	base.Disputes, base.Withdrawals, base.Rulings = row.Disputes, row.Withdrawals, row.Rulings
	base.ConflictingFinalRulings = row.ConflictingFinalRulings
	return xdealv1.StatusValue{Role: "seller", Disputes: disputes, Rows: []xdealv1.StatusRow{{
		ID: "r", Input: base, RulingAgainst: against, RemedyMade: xdealv1.NoTime,
		StatementNamedByOther: []xdealv1.StatusTime{xdealv1.KnownTime(otDue)},
	}}}
}

func otBuyerDispute(outcome string) xdealv1.StatusDispute {
	return xdealv1.StatusDispute{Row: "r", Claimant: "buyer", Outcome: outcome, Forum: "auto:twin"}
}

func otAt(t *testing.T, v xdealv1.StatusValue, at string, stamps map[uint64]string) (string, []standardhistory.Dispute) {
	t.Helper()
	rows, disputes, err := rowsAndDisputes3(v, at, stamps)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	return rows[0].Status, disputes
}

func otReceived(outcome string) []standardhistory.Dispute {
	return []standardhistory.Dispute{{Side: "received", Outcome: outcome, Forum: "auto"}}
}

// A dispute whose record time is T exists at T; one after T is no entry; one
// with no time exists, whether it has none or its stamp has no time yet; a
// stamp resolved to a time after T is after T.
func TestTheCardReadsADisputeAsOfItsAnchor(t *testing.T) {
	at := "2026-11-07T00:00:00Z"
	for name, c := range map[string]struct {
		time   xdealv1.StatusTime
		stamps map[uint64]string
		want   []standardhistory.Dispute
		row    string
	}{
		"at T":                {xdealv1.KnownTime(at), nil, otReceived("open"), "in_dispute"},
		"after T":             {xdealv1.KnownTime("2026-11-07T00:00:01Z"), nil, []standardhistory.Dispute{}, "waiting"},
		"no time":             {xdealv1.NoTime, nil, otReceived("open"), "in_dispute"},
		"an unresolved stamp": {xdealv1.StampTime(3), nil, otReceived("open"), "in_dispute"},
		"a stamp after T":     {xdealv1.StampTime(3), map[uint64]string{3: "2026-11-08T00:00:00Z"}, []standardhistory.Dispute{}, "waiting"},
		"a stamp before T":    {xdealv1.StampTime(3), map[uint64]string{3: otDispute}, otReceived("open"), "in_dispute"},
	} {
		t.Run(name, func(t *testing.T) {
			v := otDeal(xdealv1.StatusRowInput{Disputes: []xdealv1.StatusRowDispute{{Side: "buyer", Time: c.time}}}, nil,
				otBuyerDispute(xdealv1.OutcomeOpen))
			row, got := otAt(t, v, at, c.stamps)
			assert.Equal(t, c.want, got)
			assert.Equal(t, c.row, row, "the row reads the same record")
		})
	}
}

// A late dispute (raised after the window ended, on a statement the claimant
// had seen in time) keeps the row out of dispute but is still a dispute on
// the card's tally.
func TestALateDisputeIsStillATallyEntry(t *testing.T) {
	v := otDeal(xdealv1.StatusRowInput{Disputes: []xdealv1.StatusRowDispute{
		{Side: "buyer", Time: xdealv1.KnownTime("2026-11-11T00:00:00Z"), SeenInTime: true}}}, nil,
		otBuyerDispute(xdealv1.OutcomeOpen))
	row, got := otAt(t, v, "2026-11-12T00:00:00Z", nil)
	assert.Equal(t, "uncontested", row)
	assert.Equal(t, otReceived("open"), got)
}

// A withdrawal counts once it exists: before T the dispute is withdrawn, after
// T it is open again, and a withdrawal before T outranks a ruling after T. A
// withdrawal earlier than the dispute never withdraws it.
func TestTheCardReadsAWithdrawalAsOfItsAnchor(t *testing.T) {
	withdrawn := func(at string, rulings ...xdealv1.StatusRuling) xdealv1.StatusValue {
		return otDeal(xdealv1.StatusRowInput{
			Disputes:    []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime(otDispute)}},
			Withdrawals: []xdealv1.StatusSideTime{{Side: "buyer", Time: xdealv1.KnownTime(at)}},
			Rulings:     rulings,
		}, otStr("seller"), otBuyerDispute(xdealv1.OutcomeWithdrawn))
	}
	_, got := otAt(t, withdrawn("2026-11-07T00:00:00Z"), "2026-11-08T00:00:00Z", nil)
	assert.Equal(t, otReceived("withdrawn"), got, "withdrawn before T")
	_, got = otAt(t, withdrawn("2026-11-09T00:00:00Z"), "2026-11-08T00:00:00Z", nil)
	assert.Equal(t, otReceived("open"), got, "withdrawn after T")
	ruled := xdealv1.StatusRuling{Final: true, Value: "not_met", Time: xdealv1.KnownTime(otRuling)}
	_, got = otAt(t, withdrawn("2026-11-07T00:00:00Z", ruled), "2026-11-10T00:00:00Z", nil)
	assert.Equal(t, otReceived("withdrawn"), got, "withdrawn before T, ruled after it")
	_, got = otAt(t, withdrawn("2026-11-09T00:00:00Z", ruled), "2026-11-13T00:00:00Z", nil)
	assert.Equal(t, otReceived("withdrawn"), got, "both held at T: the withdrawal outranks the ruling")

	// The buyer withdrew an earlier dispute, then disputed again: the earlier
	// withdrawal does not withdraw the later dispute at any anchor.
	again := otDeal(xdealv1.StatusRowInput{
		Disputes:    []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime(otDispute)}},
		Withdrawals: []xdealv1.StatusSideTime{{Side: "buyer", Time: xdealv1.KnownTime("2026-11-05T12:00:00Z")}},
	}, nil, otBuyerDispute(xdealv1.OutcomeOpen))
	_, got = otAt(t, again, "2026-11-08T00:00:00Z", nil)
	assert.Equal(t, otReceived("open"), got)
}

// A withdrawal made after T cannot change the tally at T: book order D1, W1,
// D2 with the same time, then W2 after T. The status value reads D2 as
// withdrawn only with W2, but at T both books read the same, D2 withdrawn by
// the held W1 of the same time, as the row rule reads it (any held withdrawal
// of the claimant withdraws its disputes on the row).
func TestAWithdrawalAfterTheAnchorDoesNotChangeTheTally(t *testing.T) {
	same := xdealv1.KnownTime(otDispute)
	disputes := []xdealv1.StatusRowDispute{{Side: "buyer", Time: same}, {Side: "buyer", Time: same}}
	w1 := xdealv1.StatusSideTime{Side: "buyer", Time: same}
	w2 := xdealv1.StatusSideTime{Side: "buyer", Time: xdealv1.KnownTime("2026-11-09T00:00:00Z")}
	without := otDeal(xdealv1.StatusRowInput{Disputes: disputes, Withdrawals: []xdealv1.StatusSideTime{w1}},
		nil, otBuyerDispute(xdealv1.OutcomeWithdrawn), otBuyerDispute(xdealv1.OutcomeOpen))
	with := otDeal(xdealv1.StatusRowInput{Disputes: disputes, Withdrawals: []xdealv1.StatusSideTime{w1, w2}},
		nil, otBuyerDispute(xdealv1.OutcomeWithdrawn), otBuyerDispute(xdealv1.OutcomeWithdrawn))
	at := "2026-11-08T00:00:00Z"
	rowWithout, gotWithout := otAt(t, without, at, nil)
	rowWith, gotWith := otAt(t, with, at, nil)
	assert.Equal(t, gotWithout, gotWith)
	assert.Equal(t, rowWithout, rowWith)
	assert.NotEqual(t, "in_dispute", rowWith)
	assert.Equal(t, []standardhistory.Dispute{
		{Side: "received", Outcome: "withdrawn", Forum: "auto"}, {Side: "received", Outcome: "withdrawn", Forum: "auto"},
	}, gotWith)
}

// A ruling is held once its time is T or earlier: before it the dispute is
// open and the row in dispute; from it the ruling decides both, for or
// against the holder, or no finding.
func TestTheCardReadsARulingAsOfItsAnchor(t *testing.T) {
	ruled := func(against *string) xdealv1.StatusValue {
		outcome := map[bool]string{true: xdealv1.OutcomeNoFinding, false: xdealv1.OutcomeForClaimant}[against == nil]
		if against != nil && *against == "buyer" {
			outcome = xdealv1.OutcomeAgainstClaimant
		}
		return otDeal(xdealv1.StatusRowInput{
			Disputes: []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime(otDispute)}},
			Rulings: []xdealv1.StatusRuling{
				{Final: false, Value: "not_met", Time: xdealv1.KnownTime("2026-11-08T00:00:00Z")},
				{Final: true, Value: "not_met", Time: xdealv1.KnownTime(otRuling)},
			},
		}, against, otBuyerDispute(outcome))
	}
	for name, c := range map[string]struct {
		against *string
		want    string
	}{
		"against the holder": {otStr("seller"), "against"},
		"for the holder":     {otStr("buyer"), "for"},
		"no finding":         {nil, "no_finding"},
	} {
		t.Run(name, func(t *testing.T) {
			row, got := otAt(t, ruled(c.against), "2026-11-11T23:59:59Z", nil)
			assert.Equal(t, "in_dispute", row)
			assert.Equal(t, otReceived("open"), got, "a ruling after T, and a non-final one before it, resolve nothing")
			row, got = otAt(t, ruled(c.against), otRuling, nil)
			assert.Equal(t, "ruled", row)
			assert.Equal(t, otReceived(c.want), got)
		})
	}
}

// A row that is not reached yet still resolves its dispute by a ruling held
// before T: the tally reads the ruling, not the row's status.
func TestANotReachedRowsRulingResolvesItsDispute(t *testing.T) {
	v := otDeal(xdealv1.StatusRowInput{
		Row:      rowstatus.Row{ID: "r", OwedBy: "seller", Due: rowstatus.Date{After: "delivered"}, WindowEnd: rowstatus.Date{After: "delivered", Seconds: 86400}},
		Named:    map[string]xdealv1.StatusNamed{"delivered": {Time: xdealv1.NoTime, Latest: "2026-11-03T00:00:00Z"}},
		Disputes: []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime("2026-11-02T00:00:00Z")}},
		Rulings:  []xdealv1.StatusRuling{{Final: true, Value: "not_met", Time: xdealv1.KnownTime("2026-11-04T00:00:00Z")}},
	}, otStr("seller"), otBuyerDispute(xdealv1.OutcomeForClaimant))
	row, got := otAt(t, v, "2026-11-06T00:00:00Z", nil)
	assert.Equal(t, "not_reached", row)
	assert.Equal(t, otReceived("against"), got)
}

// Two forks of the case book with different final rulings leave the row in
// dispute (the row rule reads the conflict before any ruling), so the
// dispute stays open whether or not both rulings exist by T.
func TestConflictingFinalRulingsLeaveTheDisputeOpen(t *testing.T) {
	v := otDeal(xdealv1.StatusRowInput{
		Disputes: []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime(otDispute)}},
		Rulings: []xdealv1.StatusRuling{
			{Final: true, Value: "not_met", Time: xdealv1.KnownTime("2026-11-08T00:00:00Z")},
			{Final: true, Value: "met", Time: xdealv1.KnownTime(otRuling)},
		},
		ConflictingFinalRulings: true,
	}, nil, otBuyerDispute(xdealv1.OutcomeOpen))
	for _, at := range []string{"2026-11-09T00:00:00Z", "2026-11-13T00:00:00Z"} {
		row, got := otAt(t, v, at, nil)
		assert.Equal(t, "in_dispute", row, at)
		assert.Equal(t, otReceived("open"), got, at)
	}
}

// The #466 rerun: at the anchor just before the ruling's time the card's row
// is in dispute and its dispute open; at the next anchor the row is ruled and
// the dispute against the holder. A card that counted the ruling at the
// earlier anchor, as the status value alone reads it, fails
// counts_recompute against the entries formed from the handover (Sherlock2,
// agreement-action-capsule#719 review).
func TestACardThatCountsALaterRulingFailsCountsRecompute(t *testing.T) {
	v := otDeal(xdealv1.StatusRowInput{
		Disputes: []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime(otDispute)}},
		Rulings:  []xdealv1.StatusRuling{{Final: true, Value: "not_met", Time: xdealv1.KnownTime(otRuling)}},
	}, otStr("seller"), otBuyerDispute(xdealv1.OutcomeForClaimant))

	before := "2026-11-11T23:59:59Z"
	rows, disputes, err := rowsAndDisputes3(v, before, nil)
	require.NoError(t, err)
	assert.Equal(t, "in_dispute", rows[0].Status)
	assert.Equal(t, otReceived("open"), disputes)
	row, later := otAt(t, v, otRuling, nil)
	assert.Equal(t, "ruled", row)
	assert.Equal(t, otReceived("against"), later)

	entry := func(d []standardhistory.Dispute) []standardhistory.Deal {
		return []standardhistory.Deal{{
			Deal: "b", AgreedAt: "2026-10-30T00:00:00Z", Role: "provided", Happened: "receipt_only",
			Amount: standardhistory.Amount{Value: "1", AssetCode: "USD"}, Rows: rows, Disputes: d,
		}}
	}
	asOf := standardhistory.AsOf{Anchor: strings.Repeat("a", 64), AnchorTime: before, FirstCheckpointTime: "2026-10-01T00:00:00Z", CoverageStartTime: "2026-10-01T00:00:00Z"}
	policy := standardhistory.Policy{Digest: strings.Repeat("b", 64), Window: standardhistory.Window{Months: 3}}

	stale, err := standardhistory.DeriveAs(standardhistory.Derivation3, "node", "node_declared", asOf, policy, entry(disputes3(v.Role, v.Disputes)))
	require.NoError(t, err)
	got, err := standardhistory.CheckAs(standardhistory.Derivation3, stale, &policy, &asOf, entry(disputes))
	require.NoError(t, err)
	assert.Equal(t, standardhistory.Fail, got["counts_recompute"], "the tally counts a ruling that does not exist at its anchor")

	card, err := standardhistory.DeriveAs(standardhistory.Derivation3, "node", "node_declared", asOf, policy, entry(disputes))
	require.NoError(t, err)
	got, err = standardhistory.CheckAs(standardhistory.Derivation3, card, &policy, &asOf, entry(disputes))
	require.NoError(t, err)
	assert.Equal(t, standardhistory.Pass, got["counts_recompute"])
}

// A status value whose disputes are not its rows' dispute records, one for
// one in order, is corrupt; so is a held final ruling after one not yet held
// in an unforked case book, whose side the status value does not carry.
func TestTheCardRefusesDisputesItCannotTime(t *testing.T) {
	rowDispute := xdealv1.StatusRowInput{Disputes: []xdealv1.StatusRowDispute{{Side: "buyer", Time: xdealv1.KnownTime(otDispute)}}}
	for name, v := range map[string]xdealv1.StatusValue{
		"one too few":      otDeal(rowDispute, nil),
		"one too many":     otDeal(rowDispute, nil, otBuyerDispute("open"), otBuyerDispute("open")),
		"another claimant": otDeal(rowDispute, nil, xdealv1.StatusDispute{Row: "r", Claimant: "seller", Outcome: "open"}),
		"another row":      otDeal(rowDispute, nil, xdealv1.StatusDispute{Row: "q", Claimant: "buyer", Outcome: "open"}),
		"a later final ruling held first": otDeal(xdealv1.StatusRowInput{
			Disputes: rowDispute.Disputes,
			Rulings: []xdealv1.StatusRuling{
				{Final: true, Value: "met", Time: xdealv1.KnownTime(otRuling)},
				{Final: true, Value: "not_met", Time: xdealv1.NoTime},
			},
		}, otStr("buyer"), otBuyerDispute(xdealv1.OutcomeAgainstClaimant)),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := rowsAndDisputes3(v, "2026-11-11T00:00:00Z", nil)
			assert.Error(t, err)
		})
	}
	_, err := disputesAsOf(otDeal(rowDispute, nil, otBuyerDispute("open")), nil, "2026-11-11T00:00:00Z")
	assert.Error(t, err, "row inputs and rows differ in number")
}
