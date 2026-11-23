package deal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1List runs deal list --json (with args) and returns the listings by deal
// id, or the range object when args hold --since or --until.
func v1List(t *testing.T, args ...string) (map[string]map[string]any, map[string]any) {
	t.Helper()
	out, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "list", "--json"}, args...)...)
	require.NoError(t, err, out)
	var raw any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	var deals []any
	var r map[string]any
	switch v := raw.(type) {
	case []any:
		deals = v
	case map[string]any:
		r, deals = v, v["deals"].([]any)
	}
	byID := map[string]map[string]any{}
	for _, d := range deals {
		m := d.(map[string]any)
		byID[m["deal_id"].(string)] = m
	}
	return byID, r
}

// v1StoreFiles are the store's bytes, to show a command wrote nothing.
func v1StoreFiles(t *testing.T) map[string][]byte {
	t.Helper()
	db := v1Profile(t).Connection.Database
	got := map[string][]byte{}
	for _, f := range []string{db, db + "-wal"} {
		if b, err := os.ReadFile(f); err == nil {
			got[f] = b
		}
	}
	return got
}

// An x-deal-v1 deal is listed on each side as it stands: offered by the
// offerer, offer_received by the acceptor, then open once agreed (computed
// now, with the time it was computed), with the version, side, role,
// counterparty key and the offer's item and one payment row's amount. The
// plain line keeps v0's columns. Listing writes nothing.
func TestAV1DealIsListed(t *testing.T) {
	h := &handshakeV1{}
	h.seller = newV1Side(t)
	h.buyer = newV1Side(t)
	h.offerFile = filepath.Join(t.TempDir(), "offer.json")
	res := dealRun(t, "offer", "--input", writeJSON(t, offerTo(h.seller.party.signer)), "--out", h.offerFile)
	h.dealID, h.offer = res["deal_id"].(string), res["offer"].(string)

	before := v1StoreFiles(t)
	deals, _ := v1List(t)
	assert.Equal(t, before, v1StoreFiles(t), "listing writes nothing")
	l := deals[h.dealID]
	require.NotNil(t, l, "%v", deals)
	assert.Equal(t, "offered", l["state"])
	assert.Equal(t, "x-deal-v1", l["version"])
	assert.Equal(t, "offerer", l["side"])
	assert.Equal(t, "buyer", l["role"])
	assert.Equal(t, "", l["log_id"], "no book yet")
	assert.Equal(t, l["opened_at"], l["last_record_at"])
	sum := l["summary"].(map[string]any)
	assert.Equal(t, "lamp", sum["what"])
	assert.Nil(t, sum["amount"], "the offer's one row is not recomputed, so it is no payment row")
	assert.Equal(t, float64(0), l["payment_rows"])
	assert.Contains(t, sum["text"], "no payment row in the offer")

	h.seller.act(t)
	dealRun(t, "receive", h.offerFile)
	deals, _ = v1List(t)
	assert.Equal(t, "offer_received", deals[h.dealID]["state"])
	assert.Equal(t, "acceptor", deals[h.dealID]["side"])
	assert.Equal(t, "seller", deals[h.dealID]["role"])
	assert.NotEmpty(t, deals[h.dealID]["counterparty_key"])

	h.sellerAgree = filepath.Join(t.TempDir(), "agreement.json")
	dealRun(t, "agree", h.offer, "--admit", sealAdmit(t, "GO"), "--out", h.sellerAgree)
	before = v1StoreFiles(t)
	deals, _ = v1List(t)
	assert.Equal(t, before, v1StoreFiles(t), "computing the state writes nothing")
	l = deals[h.dealID]
	assert.Equal(t, "open", l["state"])
	assert.Equal(t, "open", l["summary"].(map[string]any)["outcome"])
	assert.NotEmpty(t, l["outcome_as_of"])
	assert.True(t, strings.HasPrefix(l["log_id"].(string), "deal/"))

	out, err := invoke(t, "", "--profile", "deal", "deal", "list")
	require.NoError(t, err, out)
	var line []string
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(ln, h.dealID+"\t") {
			line = strings.Split(ln, "\t")
		}
	}
	require.Len(t, line, 7, out)
	assert.Equal(t, []string{h.dealID, "deal", "open"}, line[:3])
	assert.Equal(t, shortKey(l["counterparty_key"].(string)), line[5], "a v1 counterparty's column is its key, short")
	assert.Len(t, line[5], 16)

	// A book past its checkpoint is not_checkpointed, never a guess.
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	deals, _ = v1List(t)
	assert.Equal(t, "not_checkpointed", deals[h.dealID]["state"])
	assert.Contains(t, deals[h.dealID]["checkpoint_note"], "the next cadence tick covers it (about every 5m)")
	assert.Contains(t, deals[h.dealID]["summary"].(map[string]any)["text"], "counterparty: no name recorded (key only)")
	stdout, stderr := invokeSplit(t, "--profile", "deal", "deal", "list")
	assert.NotContains(t, stdout, "not_checkpointed:", "the table stays a table")
	assert.Contains(t, stderr, "not_checkpointed: records since the last checkpoint")
}

// With no own book the state is deal_versions' as it is (each word the
// write path stores), and a book with no record at seq 1 (a run stopped
// between choosing the log id and the first record) is no book: agreeing
// passes through and the listing writes nothing.
func TestAV1ListPassesTheStateThroughWithNoBook(t *testing.T) {
	h := newHandshakeV1(t)
	h.buyer.act(t)
	for _, state := range []string{"offered", "offer_received", "agreeing", "declined", "cancelled"} {
		_, err := openWritableDB(t).Exec(`UPDATE deal_versions SET state=?, own_log_id='deal/00000000000000000000000000000000' WHERE deal_id=?`, state, h.dealID)
		require.NoError(t, err)
		before := v1StoreFiles(t)
		deals, _ := v1List(t)
		assert.Equal(t, before, v1StoreFiles(t), state)
		assert.Equal(t, state, deals[h.dealID]["state"])
		assert.Equal(t, "", deals[h.dealID]["log_id"], state)
	}
	books, err := func() ([]string, error) {
		s, err := openDealStoreRead(context.Background(), v1Profile(t))
		if err != nil {
			return nil, err
		}
		defer s.close()
		return s.shelf.Books(context.Background())
	}()
	require.NoError(t, err)
	assert.NotContains(t, books, "deal/00000000000000000000000000000000", "no log was created")
}

// settled is the report's word, computed now; any failure other than a
// missing checkpoint is an error, not a state.
func TestAV1ListSettledAndErrors(t *testing.T) {
	h := agreedBoth(t, offerTo)
	h.buyer.act(t)
	orig := v1ListSnapshot
	t.Cleanup(func() { v1ListSnapshot = orig })
	v1ListSnapshot = func(context.Context, *xdealv1.StatusProvider, string, xdealv1.ReportCopy) (xdealv1.ReportSnapshot, error) {
		return xdealv1.ReportSnapshot{Report: xdealv1.Report{Settled: true}}, nil
	}
	deals, _ := v1List(t)
	assert.Equal(t, "settled", deals[h.dealID]["state"])
	assert.Contains(t, deals[h.dealID]["settled_note"], "no act carries the person's own signature yet")
	_, stderr := invokeSplit(t, "--profile", "deal", "deal", "list")
	assert.Contains(t, stderr, "settled: computed from the agent's records")

	v1ListSnapshot = func(context.Context, *xdealv1.StatusProvider, string, xdealv1.ReportCopy) (xdealv1.ReportSnapshot, error) {
		return xdealv1.ReportSnapshot{}, errors.New("xdealv1: report: a record is timed after as_of")
	}
	out, err := invoke(t, "", "--profile", "deal", "deal", "list", "--json")
	require.Error(t, err, out)
	assert.Contains(t, err.Error(), "deal "+h.dealID, "the error names the deal")
	assert.Equal(t, 1, strings.Count(err.Error(), h.dealID), "once")
	assert.Contains(t, err.Error(), "this computer's clock", "in plain words, as deal report says it")

	// A range that leaves the deal out computes no state for it, so its
	// failure does not stop the list.
	_, r := v1List(t, "--since", "2030-01-01")
	assert.Nil(t, r["x_deal_v1_note"])
}

// An offer with one payment row (recomputed, an amount) gives its amount;
// one with several gives none, and says how many.
func TestAV1ListSeveralPaymentRows(t *testing.T) {
	newV1Side(t)
	one := strings.Replace(v1OfferInput, `"window_end": {"at": "2026-11-04T23:59:59Z"}}`, `"window_end": {"at": "2026-11-04T23:59:59Z"}, "recomputed": true}`, 1)
	res1 := dealRun(t, "offer", "--input", writeJSON(t, one), "--out", filepath.Join(t.TempDir(), "o1.json"))
	deals1, _ := v1List(t)
	l1 := deals1[res1["deal_id"].(string)]
	assert.Equal(t, map[string]any{"amount_minor": float64(6000), "currency": "USD", "basis": "offer_terms"}, l1["summary"].(map[string]any)["amount"])
	assert.NotContains(t, l1, "payment_rows")
	two := strings.Replace(v1OfferInput, `"window_end": {"at": "2026-11-04T23:59:59Z"}}`, `"window_end": {"at": "2026-11-04T23:59:59Z"}},
      {"id": "deposit", "owed_by": "buyer", "owed": {"amount_minor": 1000, "currency": "USD"},
       "due": {"at": "2026-11-01T23:59:59Z"}, "window_end": {"at": "2026-11-04T23:59:59Z"}}`, 1)
	two = strings.ReplaceAll(two, `"window_end": {"at": "2026-11-04T23:59:59Z"}}`, `"window_end": {"at": "2026-11-04T23:59:59Z"}, "recomputed": true}`)
	res := dealRun(t, "offer", "--input", writeJSON(t, two), "--out", filepath.Join(t.TempDir(), "o.json"))
	deals, _ := v1List(t)
	l := deals[res["deal_id"].(string)]
	require.NotNil(t, l)
	assert.Nil(t, l["summary"].(map[string]any)["amount"])
	assert.Equal(t, float64(2), l["payment_rows"])
}

// v0 and v1 deals are listed together in deal id order; a range lists a v1
// deal when one of its records falls in it, says how, and counts it in no
// total.
func TestAV1ListMixedAndRange(t *testing.T) {
	newV1Side(t)
	res := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))
	v1ID := res["deal_id"].(string)
	legacyV0(t) // a v0 deal beside it, under the legacy setting
	v0ID := openJetSki(t)
	deals, _ := v1List(t)
	assert.Contains(t, deals, v1ID)
	assert.Contains(t, deals, v0ID)
	assert.NotContains(t, deals[v0ID], "version", "v0 carries no version member")

	in, r := v1List(t, "--since", "2026-09-27", "--until", "2026-09-28")
	assert.Contains(t, in, v1ID)
	assert.Contains(t, r["x_deal_v1_note"], "not yet counted in the totals")
	_, stderr := invokeSplit(t, "--profile", "deal", "deal", "list", "--since", "2026-09-27", "--until", "2026-09-28")
	assert.Contains(t, stderr, "not yet counted in the totals", "the plain range says it too")
	out, _ := v1List(t, "--since", "2026-09-28")
	assert.NotContains(t, out, v1ID)
}

// invokeSplit runs the command with stdout and stderr apart.
func invokeSplit(t *testing.T, args ...string) (string, string) {
	t.Helper()
	c := testCommand()
	var out, errOut bytes.Buffer
	c.SetIn(strings.NewReader(""))
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	require.NoError(t, c.ExecuteContext(t.Context()), errOut.String())
	return out.String(), errOut.String()
}

// A range picks a v1 deal by a later own-book record when its offer is
// outside it, Since inclusive and Until exclusive, and the deal changes
// neither the totals nor the disclosures.
func TestAV1ListRangeByABookRecord(t *testing.T) {
	h := newHandshakeV1(t) // the offer and the acceptor's agreement at the fixture's clock
	h.seller.act(t)
	deals, _ := v1List(t)
	agreedAt := deals[h.dealID]["last_record_at"].(string)
	opened := deals[h.dealID]["opened_at"].(string)
	clockAt(t, v1Time(t, agreedAt).Add(72*time.Hour))
	dealRun(t, "statement", "--deal", h.dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "s.json"))
	deals, _ = v1List(t)
	last := deals[h.dealID]["last_record_at"].(string)
	require.NotEqual(t, opened, last)

	at := v1Time(t, last)
	in, r := v1List(t, "--since", at.Format(time.RFC3339), "--until", at.Add(time.Second).Format(time.RFC3339))
	assert.Contains(t, in, h.dealID, "the book record is in the range though the offer is not")
	assert.Equal(t, last, in[h.dealID]["last_record_at"])
	assert.Equal(t, float64(0), r["totals"].(map[string]any)["deals"], "a v1 deal is not counted in the totals")
	for _, d := range r["disclosures"].([]any) {
		assert.Equal(t, float64(0), d.(map[string]any)["recipients"])
	}
	out, _ := v1List(t, "--since", at.Add(time.Second).Format(time.RFC3339))
	assert.NotContains(t, out, h.dealID, "since is inclusive of the record's second only")
	before, _ := v1List(t, "--until", v1Time(t, opened).Format(time.RFC3339))
	assert.NotContains(t, before, h.dealID, "until is exclusive")
}

func v1Time(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return ts
}
