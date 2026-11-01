// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var historyStart = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

// fixtureHistory is one fixture directory's records as a deal's log, each
// committed a minute after the last.
func fixtureHistory(t *testing.T, dir string) []HistoryRecord {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixturesDir, dir, "*.json"))
	require.NoError(t, err)
	sort.Strings(paths)
	var out []HistoryRecord
	for i, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var f struct {
			Record json.RawMessage `json:"record"`
		}
		require.NoError(t, json.Unmarshal(raw, &f))
		out = append(out, HistoryRecord{Payload: f.Record, CommittedAt: historyStart.Add(time.Duration(i) * time.Minute)})
	}
	return out
}

// classic builds a buyer's or seller's x-deal-v0 deal by hand, each record
// naming the one before it by digest, as a producer chains them.
type classic struct {
	t       *testing.T
	records []HistoryRecord
	digests map[string]string
	// top is a record's extra top-level members, by the name add is given.
	top map[string]map[string]any
}

func (c *classic) add(name, recordType string, refs map[string]string, body map[string]any) {
	c.t.Helper()
	var rels []map[string]string
	for rel, target := range refs {
		rels = append(rels, map[string]string{"rel": rel, "type": "deal-record", "digest_alg": "SHA-256", "digest": c.digests[target]})
	}
	block := map[string]any{"profile": Profile, "canonicalization": "jcs", "deal_id": "deal-0123456789abcdef", "record_type": recordType, "seq": len(c.records) + 1, "at": "2026-10-06T09:00:00Z"}
	if rels != nil {
		block["refs"] = rels
	}
	rec := map[string]any{"x-deal-v0": block, "body": body}
	for k, v := range c.top[name] {
		rec[k] = v
	}
	raw, err := json.Marshal(rec)
	require.NoError(c.t, err)
	digest, err := Digest(json.RawMessage(raw))
	require.NoError(c.t, err)
	c.digests[name] = digest
	c.records = append(c.records, HistoryRecord{Payload: raw, CommittedAt: historyStart.Add(time.Duration(len(c.records)) * time.Hour)})
}

func classicDeal(t *testing.T, role string, check map[string]any) *classic {
	c := &classic{t: t, digests: map[string]string{}}
	intent := map[string]any{"verbatim_commitment": "00"}
	if role != "" {
		intent["party_role"] = role
	}
	c.add("baseline", TypeBaseline, nil, map[string]any{"deal_type": "purchase", "intent": intent})
	c.add("check", TypeCheck, nil, check)
	c.add("verdict", TypeVerdict, map[string]string{"checks": "check"}, map[string]any{"result": "pass"})
	c.add("approval", TypeApproval, map[string]string{"approves": "verdict"}, map[string]any{"choice": "proceed", "proceed": true})
	c.add("action", TypeAction, map[string]string{"authorized_by": "approval"}, map[string]any{"action": "commit"})
	return c
}

func result(verified bool, verdicts ...string) HistoryResult {
	claims := []map[string]string{}
	for _, v := range verdicts {
		claims = append(claims, map[string]string{"verdict": v})
	}
	raw, _ := json.Marshal(map[string]any{"result_version": "evidence-result-v0", "claims": claims})
	return HistoryResult{Result: raw, Verified: verified}
}

// A typed deal's pay rests on its proposed action through the evaluation:
// the entry takes that step's time and the proposal's amount.
func TestATypedDealsEntry(t *testing.T) {
	records := fixtureHistory(t, "positive-typed")
	deal, ok, err := StandardHistoryDeal(HistoryInput{Records: records})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, standardhistory.Deal{
		Deal: "deal-7a1e5c0d9b2f4e68", AgreedAt: "2026-10-06T09:09:00Z", Role: "bought",
		Amount:  standardhistory.Amount{Value: "55880", AssetCode: "USD", AssetScale: 2},
		Outcome: "undecided", Happened: "no_record", Disputes: []standardhistory.Dispute{},
	}, deal)

	both, _, err := StandardHistoryDeal(HistoryInput{Records: records, CounterpartyHalfVerified: true})
	require.NoError(t, err)
	assert.Equal(t, "stamped_by_both", both.Happened)

	// Without the proposal the pay rests on, the deal has no amount: the
	// card is refused, never built without it.
	var cut []HistoryRecord
	for i, r := range records {
		if i != 4 { // 05-proposed-action
			cut = append(cut, r)
		}
	}
	_, _, err = StandardHistoryDeal(HistoryInput{Records: cut})
	require.ErrorIs(t, err, ErrUnpriced)
	assert.ErrorContains(t, err, "deal deal-7a1e5c0d9b2f4e68: ")
}

// The reconcile outcome is the newest verified Result's: every claim met is
// held, any not_met did_not_hold, and anything else undecided.
func TestTheOutcomeIsTheNewestVerifiedResult(t *testing.T) {
	records := fixtureHistory(t, "positive-typed")
	for name, tc := range map[string]struct {
		results []HistoryResult
		want    string
	}{
		"no Result":               {nil, "undecided"},
		"every claim met":         {[]HistoryResult{result(true, "met", "met")}, "held"},
		"one claim not met":       {[]HistoryResult{result(true, "met", "not_met")}, "did_not_hold"},
		"met and undecided":       {[]HistoryResult{result(true, "met", "undecided")}, "undecided"},
		"no claim":                {[]HistoryResult{result(true)}, "undecided"},
		"the newest counts":       {[]HistoryResult{result(true, "not_met"), result(true, "met")}, "held"},
		"an unverified is none":   {[]HistoryResult{result(false, "met")}, "undecided"},
		"skips the unverified":    {[]HistoryResult{result(true, "not_met"), result(false, "met")}, "did_not_hold"},
		"an unreadable is undec.": {[]HistoryResult{{Result: []byte("{"), Verified: true}}, "undecided"},
	} {
		deal, ok, err := StandardHistoryDeal(HistoryInput{Records: records, Results: tc.results})
		require.NoError(t, err, name)
		require.True(t, ok, name)
		assert.Equal(t, tc.want, deal.Outcome, name)
	}
}

// The host bounds Results by the anchor: it passes only those within the
// results book's extent under the anchor, so a Result sealed after the
// anchor never changes the card (PROFILE.md section 11).
func TestAResultSealedAfterTheAnchorIsNotPassed(t *testing.T) {
	records := fixtureHistory(t, "positive-typed")
	sealed := []HistoryResult{result(true, "not_met"), result(true, "met")}
	const underAnchor = 1 // the results book's extent under the anchor, in records
	at, _, err := StandardHistoryDeal(HistoryInput{Records: records, Results: sealed[:underAnchor]})
	require.NoError(t, err)
	assert.Equal(t, "did_not_hold", at.Outcome)
	later, _, err := StandardHistoryDeal(HistoryInput{Records: records, Results: sealed})
	require.NoError(t, err)
	assert.Equal(t, "held", later.Outcome, "unbounded, the later Result changes the card")
}

// agreed_at is coarsened to the minute, as a tick's anchor time is, so a
// step sealed within the anchor's minute never sorts after the anchor.
func TestAgreedAtIsTheMinute(t *testing.T) {
	c := classicDeal(t, "", map[string]any{"action": "commit", "amount_minor": 100, "currency": "USD"})
	c.records[len(c.records)-1].CommittedAt = time.Date(2026, 10, 6, 12, 0, 30, 123456000, time.UTC)
	deal, ok, err := StandardHistoryDeal(HistoryInput{Records: c.records})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-10-06T12:00:00Z", deal.AgreedAt)
}

// The holder's last final close decides the receipt: an open close is not
// final, and a later close of a received outcome is not undone by an
// earlier one; a typed action-outcome counts as an outcome.
func TestHappenedFollowsTheLastFinalClose(t *testing.T) {
	for name, tc := range map[string]struct {
		outcomeType string
		closes      []string // the status of the outcome each close names, "" for an open close
		want        string
	}{
		"received then open":         {TypeOutcome, []string{"received", ""}, "receipt_only"},
		"received then not received": {TypeOutcome, []string{"received", "not_received"}, "no_record"},
		"typed received":             {TypeActionOutcome, []string{"received"}, "receipt_only"},
	} {
		c := classicDeal(t, "", map[string]any{"action": "commit", "amount_minor": 100, "currency": "USD"})
		for i, status := range tc.closes {
			if status == "" {
				c.add(fmt.Sprintf("close%d", i), TypeClose, nil, map[string]any{"outcome": "open"})
				continue
			}
			name := fmt.Sprintf("outcome%d", i)
			c.add(name, tc.outcomeType, nil, map[string]any{"status": status, "outcome": "completed"})
			c.add(fmt.Sprintf("close%d", i), TypeClose, map[string]string{"outcome": name}, map[string]any{"outcome": "completed"})
		}
		deal, ok, err := StandardHistoryDeal(HistoryInput{Records: c.records})
		require.NoError(t, err, name)
		require.True(t, ok, name)
		assert.Equal(t, tc.want, deal.Happened, name)
	}
}

// A seller's commit: the role is provided, the check's amount is scaled by
// its currency, and a received final close is the holder's receipt.
func TestAClassicSellersEntry(t *testing.T) {
	c := classicDeal(t, "seller", map[string]any{"action": "commit", "amount_minor": 1500, "currency": "JPY"})
	c.add("outcome", TypeOutcome, nil, map[string]any{"status": "received", "outcome": "completed"})
	// A sealed report on the deal's log is no step, and is passed over.
	c.records = append(c.records, HistoryRecord{Payload: []byte(`{"type":"deal_report","profile":"x-deal-v0","deal_id":"deal-0123456789abcdef"}`), CommittedAt: historyStart.Add(10 * time.Hour)})
	c.add("close", TypeClose, map[string]string{"outcome": "outcome"}, map[string]any{"outcome": "completed"})
	deal, ok, err := StandardHistoryDeal(HistoryInput{Records: c.records})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "provided", deal.Role)
	assert.Equal(t, "2026-10-06T13:00:00Z", deal.AgreedAt)
	assert.Equal(t, standardhistory.Amount{Value: "1500", AssetCode: "JPY", AssetScale: 0}, deal.Amount)
	assert.Equal(t, "receipt_only", deal.Happened)

	// A close of an outcome not received is no receipt; an open close is
	// not final.
	c = classicDeal(t, "", map[string]any{"action": "commit", "amount_minor": 1500, "currency": "KWD"})
	c.add("outcome", TypeOutcome, nil, map[string]any{"status": "not_received", "outcome": "mismatch"})
	c.add("close", TypeClose, map[string]string{"outcome": "outcome"}, map[string]any{"outcome": "mismatch"})
	deal, _, err = StandardHistoryDeal(HistoryInput{Records: c.records})
	require.NoError(t, err)
	assert.Equal(t, "bought", deal.Role)
	assert.Equal(t, 3, deal.Amount.AssetScale)
	assert.Equal(t, "no_record", deal.Happened)
}

func TestADealWithNoAmountIsRefused(t *testing.T) {
	for name, check := range map[string]map[string]any{
		"no amount":        {"action": "commit", "currency": "USD"},
		"no currency":      {"action": "commit", "amount_minor": 100},
		"unknown currency": {"action": "commit", "amount_minor": 100, "currency": "ZZZ"},
		"a metal":          {"action": "commit", "amount_minor": 100, "currency": "XAU"},
	} {
		_, _, err := StandardHistoryDeal(HistoryInput{Records: classicDeal(t, "", check).records})
		require.ErrorIs(t, err, ErrUnpriced, name)
		assert.ErrorContains(t, err, "deal deal-0123456789abcdef: ", name)
	}
}

// A log that never reached a commit or pay, and a sale's own log, are not
// deals on the card.
func TestLogsThatAreNotAgreedDeals(t *testing.T) {
	for _, dir := range []string{"positive", "positive-typed-seller", "positive-sale"} {
		_, ok, err := StandardHistoryDeal(HistoryInput{Records: fixtureHistory(t, dir)})
		require.NoError(t, err, dir)
		assert.False(t, ok, dir)
	}
	_, ok, err := StandardHistoryDeal(HistoryInput{})
	require.NoError(t, err)
	assert.False(t, ok)
	_, _, err = StandardHistoryDeal(HistoryInput{Records: []HistoryRecord{{Payload: []byte("not json")}}})
	require.Error(t, err)
}

func TestMinorUnits(t *testing.T) {
	for code, want := range map[string]int{"USD": 2, "EUR": 2, "JPY": 0, "KRW": 0, "BHD": 3, "CLF": 4, "XCG": 2} {
		got, ok := MinorUnits(code)
		assert.True(t, ok, code)
		assert.Equal(t, want, got, code)
	}
	for _, code := range []string{"", "usd", "XAU", "BTC", "US", "USDX", "AED AFN"} {
		_, ok := MinorUnits(code)
		assert.False(t, ok, code)
	}
}

// counterparty_class is read from the one pay check that carries it (SH §7):
// anything else is absent and counted in class_missing, never guessed.
func TestTheCounterpartyClassIsThePayChecksOnly(t *testing.T) {
	pay := map[string]any{"action": "pay", "amount_minor": 1500, "currency": "USD"}
	for name, tc := range map[string]struct {
		check map[string]any
		top   map[string]map[string]any
		want  *string
	}{
		"no member":             {pay, nil, nil},
		"person_agent":          {pay, map[string]map[string]any{"check": {"counterparty_class": "person_agent"}}, ptr("person_agent")},
		"business_agent":        {pay, map[string]map[string]any{"check": {"counterparty_class": "business_agent"}}, ptr("business_agent")},
		"shop_preauth":          {pay, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}}, ptr("shop_preauth")},
		"an unknown value":      {pay, map[string]map[string]any{"check": {"counterparty_class": "shop"}}, nil},
		"a non-string":          {pay, map[string]map[string]any{"check": {"counterparty_class": 1}}, nil},
		"an empty string":       {pay, map[string]map[string]any{"check": {"counterparty_class": ""}}, nil},
		"a commit check":        {map[string]any{"action": "commit", "amount_minor": 1500, "currency": "USD"}, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}}, nil},
		"a record, not a check": {pay, map[string]map[string]any{"action": {"counterparty_class": "shop_preauth"}}, nil},
		"two carriers":          {pay, map[string]map[string]any{"check": {"counterparty_class": "shop_preauth"}, "action": {"counterparty_class": "shop_preauth"}}, nil},
	} {
		c := rebuildClassic(t, tc.check, tc.top)
		deal, ok, err := StandardHistoryDeal(HistoryInput{Records: c.records})
		require.NoError(t, err, name)
		require.True(t, ok, name)
		assert.Equal(t, tc.want, deal.CounterpartyClass, name)
	}
}

func ptr(s string) *string { return &s }

// rebuildClassic is classicDeal with extra top-level members on named records.
func rebuildClassic(t *testing.T, check map[string]any, top map[string]map[string]any) *classic {
	c := &classic{t: t, digests: map[string]string{}, top: top}
	c.add("baseline", TypeBaseline, nil, map[string]any{"deal_type": "purchase", "intent": map[string]any{"verbatim_commitment": "00"}})
	c.add("check", TypeCheck, nil, check)
	c.add("verdict", TypeVerdict, map[string]string{"checks": "check"}, map[string]any{"result": "pass"})
	c.add("approval", TypeApproval, map[string]string{"approves": "verdict"}, map[string]any{"choice": "proceed", "proceed": true})
	c.add("action", TypeAction, map[string]string{"authorized_by": "approval"}, map[string]any{"action": "commit"})
	return c
}
