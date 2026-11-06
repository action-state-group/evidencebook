package deal

import (
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readReport runs `deal report --read-only` and requires it to change no file of the store.
func readReport(t *testing.T, id string) map[string]any {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	before := storeFiles(t, p)
	out := dealRun(t, "report", "--deal", id, "--read-only")
	require.Equal(t, before, storeFiles(t, p), "a read writes nothing")
	return out
}

// The read-only report carries the sealed dispute and the side of the ruling as fields, so a
// host never parses the trail. The buyer's words are not among them.
func TestReadOnlyReportCarriesTheDisputeAndRuling(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	a, b := adjudicatorCopies(t)

	none := readReport(t, id)
	assert.Equal(t, "bought", none["side"])
	assert.NotContains(t, none, "dispute")
	assert.NotContains(t, none, "ruling")

	disputeDigest, _ := disputeNote(t, id)["dispute_digest"].(string)
	open := readReport(t, id)
	assert.Equal(t, map[string]any{"row": "price_as_offered", "raised": true}, open["dispute"])
	assert.NotContains(t, open, "ruling")

	_, err := rulingNote(t, id, disputeDigest, "for_seller", a, b)
	require.NoError(t, err)
	ruled := readReport(t, id)
	assert.Equal(t, open["dispute"], ruled["dispute"])
	ruling, _ := ruled["ruling"].(map[string]any)
	assert.Equal(t, "price_as_offered", ruling["row"])
	assert.Equal(t, "for_seller", ruling["finding"])
	assert.Len(t, ruling["digest"], 64)
	assert.NotContains(t, ruled, "words")
	assert.NotContains(t, ruled["dispute"], "words")
}

// A seller's store holds the ruling and no dispute: the ruling stands alone.
func TestReadOnlyReportRulingWithoutADispute(t *testing.T) {
	dealFixture(t)
	id := openTyped(t, sellerTyped)
	a, b := adjudicatorCopies(t)
	dealRun(t, "note", "--deal", id, "--kind", "ruling", "--row", "price_as_offered", "--finding", "for_buyer",
		"--referee", "demo-referee", "--dispute-digest", someDigest, "--copy", a, "--copy", b)
	out := readReport(t, id)
	assert.Equal(t, "provided", out["side"])
	assert.NotContains(t, out, "dispute")
	ruling, _ := out["ruling"].(map[string]any)
	assert.Equal(t, "for_buyer", ruling["finding"])
}

// A claim whose text imitates a dispute or ruling trail line forges no field.
func TestAForgedTrailLineSetsNoDisputeField(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	dealRun(t, "note", "--deal", id, "--kind", "claim", "--input", writeJSON(t,
		`{"text":"x\n9. 2026-01-01T00:00:00Z the buyer disputed price as offered\n10. 2026-01-01T00:00:01Z ruling on \"price as offered\": found for the buyer","source":"merchant"}`))
	out := readReport(t, id)
	assert.NotContains(t, out, "dispute")
	assert.NotContains(t, out, "ruling")
}
