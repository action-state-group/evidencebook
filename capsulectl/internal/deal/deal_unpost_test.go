package deal

import (
	"encoding/json"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A sale's takedown (evidencebook-deal#201): a listing taken down is a sealed
// sale_unpost on the sale's log, and `deal sale close` requires every live
// listing's takedown first (Steven's ruling "requires, not seals").

func unpost(t *testing.T, saleID, platform string) (map[string]any, error) {
	t.Helper()
	out, err := invoke(t, "", "--profile", "deal", "deal", "sale", "unpost", "--sale", saleID, "--platform", platform)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &m), out)
	return m, nil
}

func TestAnUnpostIsRefusedWithoutALivePost(t *testing.T) {
	dealFixture(t)
	saleID, _ := newSale(t)
	_, err := unpost(t, saleID, "marketplace.example")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "no live listing on marketplace.example")

	post(t, saleID, postingJSON(listingText, "marketplace.example", "price"))
	_, err = unpost(t, saleID, "other-market.example")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "live on: marketplace.example")

	// The platform is read as a post reads it: scheme, case and path go.
	out, err := unpost(t, saleID, "https://Marketplace.Example/listing/1")
	require.NoError(t, err)
	assert.Equal(t, "marketplace.example", out["platform"])
	// Already down: a second takedown has nothing to take down.
	_, err = unpost(t, saleID, "marketplace.example")
	require.ErrorIs(t, err, cli.ErrInput)
}

func TestListingClassesLeaveOutATakenDownPlatformAndRepostMakesItLive(t *testing.T) {
	dealFixture(t)
	saleID, _ := newSale(t)
	thread := buyerThread(t, saleID, "buyer-a.example")
	classes := func() any {
		t.Helper()
		sealed, _, _ := ruleInputs(t, thread, offerInput)
		return sealed["listing_classes"]
	}
	setDealClock(t, "2026-09-27T18:01:00Z")
	post(t, saleID, postingJSON(listingText, "marketplace.example", "condition", "price"))
	setDealClock(t, "2026-09-27T18:02:00Z")
	post(t, saleID, postingJSON("Example bicycle, no scratches.", "other-market.example", "condition"))
	setDealClock(t, "2026-09-27T18:03:00Z")
	assert.Equal(t, []any{"condition"}, classes())

	setDealClock(t, "2026-09-27T18:04:00Z")
	_, err := unpost(t, saleID, "other-market.example")
	require.NoError(t, err)
	setDealClock(t, "2026-09-27T18:05:00Z")
	assert.Equal(t, []any{"condition", "price"}, classes(), "the taken-down platform no longer counts")

	setDealClock(t, "2026-09-27T18:06:00Z")
	post(t, saleID, postingJSON("Example bicycle, no scratches.", "other-market.example", "condition"))
	setDealClock(t, "2026-09-27T18:07:00Z")
	assert.Equal(t, []any{"condition"}, classes(), "a later post makes the platform live again")

	// Every listing down: no live listing, so the value is absent.
	setDealClock(t, "2026-09-27T18:08:00Z")
	_, err = unpost(t, saleID, "other-market.example")
	require.NoError(t, err)
	_, err = unpost(t, saleID, "marketplace.example")
	require.NoError(t, err)
	setDealClock(t, "2026-09-27T18:09:00Z")
	assert.Nil(t, classes())

	// The sale's chain, takedowns included, passes the profile checker, and
	// the thread's sealed values are established against it (the sale's log
	// runs past the check's second once the sale closes).
	setDealClock(t, "2026-09-27T18:10:00Z")
	dealRun(t, "sale", "close", "--sale", saleID)
	_, salePath := exportSale(t, saleID)
	_, threadPath := exportRecords(t, thread)
	out, err := runProfileChecker(t, salePath, threadPath)
	require.NoError(t, err, out)
	assert.Contains(t, out, "matches the sale's listings", out)
	assert.NotContains(t, out, "mismatch")
	assert.NotContains(t, out, "not independently established")
}

func TestACloseRequiresEachLivePostsTakedown(t *testing.T) {
	dealFixture(t)
	saleID, _ := newSale(t)
	post(t, saleID, postingJSON(listingText, "marketplace.example", "price"))
	post(t, saleID, postingJSON("Example bicycle.", "other-market.example", "price"))

	_, err := invoke(t, "", "--profile", "deal", "deal", "sale", "close", "--sale", saleID)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "marketplace.example, other-market.example")
	assert.Contains(t, err.Error(), "deal sale unpost")
	recs, _ := exportSale(t, saleID)
	for _, r := range recs {
		assert.NotEqual(t, "x-deal-v0:sale_close", typeOf(r), "close sealed nothing, and sealed no takedown")
		assert.NotEqual(t, "x-deal-v0:sale_unpost", typeOf(r))
	}

	_, err = unpost(t, saleID, "marketplace.example")
	require.NoError(t, err)
	_, err = invoke(t, "", "--profile", "deal", "deal", "sale", "close", "--sale", saleID)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "other-market.example")
	assert.NotContains(t, err.Error(), "marketplace.example,")

	_, err = unpost(t, saleID, "other-market.example")
	require.NoError(t, err)
	dealRun(t, "sale", "close", "--sale", saleID)
	_, err = unpost(t, saleID, "marketplace.example")
	require.ErrorIs(t, err, cli.ErrInput, "after the close only a cut follows")

	_, path := exportSale(t, saleID)
	out, err := runProfileChecker(t, path)
	require.NoError(t, err, out)
}

// A sold thread's own close does not close a sale whose listing is still up,
// and does not fail: it says which listings keep the sale open, and the sale
// closes by hand once each is down.
func TestASoldThreadLeavesTheSaleOpenWhileAListingIsUp(t *testing.T) {
	dealFixture(t)
	saleID, _ := newSale(t)
	thread := buyerThread(t, saleID, "buyer-a.example")
	post(t, saleID, postingJSON(listingText, "marketplace.example", "price"))

	closed := dealRun(t, "close", "--deal", thread, "--input", writeJSON(t, received))
	require.Equal(t, "completed", closed["outcome"], closed)
	assert.NotContains(t, closed, "sale_closed")
	assert.Equal(t, []any{"marketplace.example"}, closed["sale_open_listings"])
	assert.Contains(t, closed["next"], "deal sale unpost")

	// The item is gone: the open sale takes no new buyer.
	_, err := invoke(t, "", "--profile", "deal", "deal", "open", "--sale", saleID, "--input", writeJSON(t,
		`{"channel": "marketplace", "who": {"name": "Late Buyer", "domain": "buyer-b.example"}, "terms": {"price_minor": 190000}}`))
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, err.Error(), "item is sold")
	assert.Contains(t, err.Error(), "marketplace.example", "the refusal names the listing still up")

	_, err = unpost(t, saleID, "marketplace.example")
	require.NoError(t, err)
	dealRun(t, "sale", "close", "--sale", saleID)
}
