package deal

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/action-state-group/evidencebook/rowstatus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bothAgreed runs the handshake to the point both books exist and each side
// holds the other's agreement.
func bothAgreed(t *testing.T) *handshakeV1 {
	t.Helper()
	h := newHandshakeV1(t)
	h.buyer.act(t)
	dealRun(t, "receive", h.sellerAgree)
	buyerAgree := filepath.Join(t.TempDir(), "buyer-agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", buyerAgree)
	h.seller.act(t)
	dealRun(t, "receive", buyerAgree)
	return h
}

func card3(t *testing.T, h *handshakeV1, side v1Side, at time.Time) (card map[string]any, deals []standardhistory.Deal) {
	t.Helper()
	side.act(t)
	clockAt(t, at)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	clockAt(t, at.Add(time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	withSession(t, func(s *dealSession) {
		got, err := s.standardHistory3(t.Context(), 0)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(got.Card, &card))
		deals = got.Deals
		assert.Equal(t, standardhistory.Derivation3, got.Derivation)
		assert.NotEmpty(t, got.AsOf.CoverageStartTime)
	})
	return card, deals
}

// A started deal is on the /3 card with a result for each row, counted from
// the book's latest fact as of the anchor's time. Before the row's due date
// nobody owes anything yet, so the deal is undecided and its row not due; the
// row is the counterparty's for the seller and the holder's for the buyer.
func TestTheV3CardCountsAStartedV1Deal(t *testing.T) {
	h := bothAgreed(t)
	early := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name, owedBy string
		side         v1Side
	}{{"seller", "counterparty", h.seller}, {"buyer", "holder", h.buyer}} {
		t.Run(c.name, func(t *testing.T) {
			card, deals := card3(t, h, c.side, early)
			assert.Equal(t, "standard_history/3", card["derivation"])
			require.Len(t, deals, 1)
			require.Len(t, deals[0].Rows, 1)
			assert.Equal(t, c.owedBy, deals[0].Rows[0].OwedBy)
			assert.Equal(t, "not_due", deals[0].Rows[0].Status)
			assert.Contains(t, card, "coverage_start")
			assert.EqualValues(t, 1, card["outcomes"].(map[string]any)["undecided"])
		})
	}
}

// An agreement then silence is counted (evidencebook v0.19.0, Steven u608):
// after agree_by, with both agreements joined and the offerer's untimed and
// never named, the deal has started locally. Its row is past due with no
// statement, so the deal is undecided.
func TestTheV3CardCountsADealWhoseOffererWentSilent(t *testing.T) {
	h := bothAgreed(t)
	card, deals := card3(t, h, h.seller, time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC))
	require.Len(t, deals, 1)
	assert.Equal(t, "no_statement", deals[0].Rows[0].Status)
	assert.EqualValues(t, 1, card["outcomes"].(map[string]any)["undecided"])
}

// A store in the old layout has no shelf log of facts to count: the card is
// refused with the way forward, never built from nothing.
func TestTheV3CardNeedsTheNewLayout(t *testing.T) {
	dealFixture(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	dealRun(t, "tick")
	err := dealRunErr(t, "standard-history", "card", "--derivation", "standard_history/3", "--out", filepath.Join(t.TempDir(), "c.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new layout")
}

func TestTheCardDerivationIsOneOfTheTwo(t *testing.T) {
	dealFixture(t)
	err := dealRunErr(t, "standard-history", "card", "--derivation", "standard_history/2", "--out", filepath.Join(t.TempDir(), "c.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--derivation")
}

// DP §10's words: a row is the holder's when its owing role is the status
// value's role; a dispute is raised or received by its claimant's role, and a
// ruling is for or against the holder.
func TestTheV3MappingUsesTheHoldersWords(t *testing.T) {
	row := xdealv1.StatusRow{Input: xdealv1.StatusRowInput{Row: rowstatus.Row{OwedBy: "seller"}}}
	assert.Equal(t, "holder", rowResult3("seller", row, rowstatus.Result{Status: "not_due"}).OwedBy)
	assert.Equal(t, "counterparty", rowResult3("buyer", row, rowstatus.Result{Status: "not_due"}).OwedBy)

	v := xdealv1.StatusValue{Role: "seller", Disputes: []xdealv1.StatusDispute{
		{Row: "r", Claimant: "seller", Outcome: xdealv1.OutcomeForClaimant, Forum: "f"},
		{Row: "r", Claimant: "buyer", Outcome: xdealv1.OutcomeForClaimant, Forum: "f"},
		{Row: "r", Claimant: "buyer", Outcome: xdealv1.OutcomeAgainstClaimant, Forum: "f"},
		{Row: "r", Claimant: "buyer", Outcome: xdealv1.OutcomeWithdrawn, Forum: "f"},
	}}
	assert.Equal(t, []standardhistory.Dispute{
		{Side: "raised", Outcome: "for", Forum: "f"}, {Side: "received", Outcome: "against", Forum: "f"},
		{Side: "received", Outcome: "for", Forum: "f"}, {Side: "received", Outcome: "withdrawn", Forum: "f"},
	}, disputes3(v))
}

// An unmet row that is not ruled is remedied when the owing side's book holds
// a remedy of it (SH-16); a ruled row takes the rule's remedy.
func TestTheV3RemedyOfAnUnmetRow(t *testing.T) {
	unmet := rowstatus.Result{Status: "agreed", Value: "not_met"}
	row := xdealv1.StatusRow{Input: xdealv1.StatusRowInput{Row: rowstatus.Row{OwedBy: "buyer"}}}
	assert.Equal(t, "none", rowResult3("buyer", row, unmet).Remedy)
	row.RemedyMade = xdealv1.KnownTime("2026-11-02T00:00:00Z")
	assert.Equal(t, "made", rowResult3("buyer", row, unmet).Remedy)
	assert.Equal(t, "due", rowResult3("buyer", row, rowstatus.Result{Status: "ruled", Value: "not_met", Remedy: "due"}).Remedy)
	assert.Empty(t, rowResult3("buyer", row, rowstatus.Result{Status: "agreed", Value: "met"}).Remedy)
}

// A band reads back as the band it came from, for any decade and currency.
func TestTheV3BandAmountKeepsTheBand(t *testing.T) {
	for _, decade := range []int{-6, -1, 0, 1, 4} {
		a, ok := bandAmount(&xdealv1.ValueBand{Currency: "USD", Decade: decade})
		require.True(t, ok)
		digits := len(a.Value)
		assert.Equal(t, decade, digits-a.AssetScale-1, "decade %d", decade)
	}
	_, ok := bandAmount(nil)
	assert.False(t, ok)
}

// A stamp commits a v1 deal book's status value: its fact names the profile
// x-deal-v1 and the opening holds the status, never profile "none".
func TestAV1BooksStampedFactCarriesItsStatus(t *testing.T) {
	h := bothAgreed(t)
	h.seller.act(t)
	clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	withSession(t, func(s *dealSession) {
		c, _, err := s.cadenceOf(t.Context())
		require.NoError(t, err)
		ho, err := c.Handover(t.Context(), 0)
		require.NoError(t, err)
		var seen int
		for _, d := range ho.Documents {
			if d.StatusOpening == nil {
				continue
			}
			var f struct {
				Kind, Profile string
			}
			require.NoError(t, json.Unmarshal(d.Doc, &f))
			if f.Kind != "book_fact" {
				continue
			}
			seen++
			assert.Equal(t, xdealv1.Profile, f.Profile)
			var v xdealv1.StatusValue
			require.NoError(t, json.Unmarshal(d.StatusOpening.Value, &v))
			assert.Equal(t, "seller", v.Role)
			require.Len(t, v.Rows, 1)
		}
		assert.NotZero(t, seen)
	})
}

// A dispute's forum reaches the card as its class: x-deal-v1 forums carry a
// prefix, and the card only accepts the class (SH §5.4).
func TestTheV3ForumIsItsClassAndTheCardDerives(t *testing.T) {
	assert.Equal(t, "auto", forumClass("auto:twin"))
	assert.Equal(t, "rail", forumClass("rail:program"))
	assert.Equal(t, "external", forumClass("external"))
	deals := []standardhistory.Deal{{
		Deal: "b", AgreedAt: "2026-10-30T00:00:00Z", Role: "provided", Happened: "receipt_only",
		Amount: standardhistory.Amount{Value: "1", AssetCode: "USD"},
		Rows:   []standardhistory.Row{{OwedBy: "holder", Status: "not_due"}},
		Disputes: disputes3(xdealv1.StatusValue{Role: "seller", Disputes: []xdealv1.StatusDispute{
			{Row: "r", Claimant: "buyer", Outcome: xdealv1.OutcomeOpen, Forum: "auto:twin"}}}),
	}}
	asOf := standardhistory.AsOf{Anchor: strings.Repeat("a", 64), AnchorTime: "2026-11-01T00:00:00Z", FirstCheckpointTime: "2026-10-01T00:00:00Z", CoverageStartTime: "2026-10-01T00:00:00Z"}
	policy := standardhistory.Policy{Digest: strings.Repeat("b", 64), Window: standardhistory.Window{Months: 3}}
	_, err := standardhistory.DeriveAs(standardhistory.Derivation3, "node", "node_declared", asOf, policy, deals)
	require.NoError(t, err)
}

// A remedy of a row the counterparty owed is the counterparty's, not the
// holder's own, so the row result says none (SH §11.2).
func TestTheV3RemedyIsTheHoldersOwnOnly(t *testing.T) {
	row := xdealv1.StatusRow{Input: xdealv1.StatusRowInput{Row: rowstatus.Row{OwedBy: "buyer"}}, RemedyMade: xdealv1.KnownTime("2026-11-02T00:00:00Z")}
	unmet := rowstatus.Result{Status: "agreed_so_far", Value: "not_met"}
	assert.Equal(t, "none", rowResult3("seller", row, unmet).Remedy)
	assert.Equal(t, "made", rowResult3("buyer", row, unmet).Remedy)
	assert.Equal(t, "made", rowResult3("buyer", row, rowstatus.Result{Status: "uncontested", Value: "not_met"}).Remedy)
}

// A tick that stales a book's status is said, as a count and never a reason.
func TestATickSaysHowManyBooksKeptAStaleStatus(t *testing.T) {
	h := bothAgreed(t)
	h.seller.act(t)
	clockAt(t, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	out := dealRun(t, "tick")
	assert.NotContains(t, out, "books_with_stale_status", "a healthy tick says nothing")
}

// A /3 card is as of the newest checkpoint: an older tick's stamp times
// could be read from a later receipt, so it is refused.
func TestTheV3CardIsAsOfTheNewestTickOnly(t *testing.T) {
	h := bothAgreed(t)
	h.seller.act(t)
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	clockAt(t, at)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	clockAt(t, at.Add(2*time.Hour))
	first := dealRun(t, "tick")["tick"]
	clockAt(t, at.Add(4*time.Hour))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	withSession(t, func(s *dealSession) {
		_, err := s.standardHistory3(t.Context(), uint64(first.(float64)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "newest checkpoint")
		_, err = s.standardHistory3(t.Context(), 0)
		require.NoError(t, err)
	})
}
