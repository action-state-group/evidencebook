package deal

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The verb returns each stored record as sealed: its JCS digest is the one
// the share printed and the disclosure log holds, in the order sealed.
func TestDealDisclosuresReadsBackSealedRecords(t *testing.T) {
	dealFixture(t)
	dealID := openPrivateDeal(t)

	empty := dealRun(t, "disclosures", "--deal", dealID)
	assert.Empty(t, empty["disclosures"], "a deal that shared nothing has no records")

	dir := t.TempDir()
	first := shareRun(t, dealID, "counterparty", filepath.Join(dir, "a.html"))
	second := shareRun(t, dealID, "adjudicator", filepath.Join(dir, "b.html"))
	stored, onLog := sharedRecords(t, dealID)
	require.Len(t, stored, 2)

	out := dealRun(t, "disclosures", "--deal", dealID)
	list := out["disclosures"].([]any)
	require.Len(t, list, 2)
	for i, item := range list {
		row := item.(map[string]any)
		record := row["record"].(map[string]any)
		assert.Equal(t, stored[i], record, "the record is the one the store sealed")
		digest, err := canonical.JSONDigest(record)
		require.NoError(t, err)
		assert.Equal(t, digest, row["disclosure_record"], "the digest is the record's own")
		assert.Equal(t, onLog[i], row["disclosure_record"], "and the one on the disclosure log")
		assert.Equal(t, float64(i+1), row["n"])
		assert.Equal(t, dealDisclosureLogID(dealID), row["log_id"])
	}
	assert.Equal(t, first["disclosure_record"], list[0].(map[string]any)["disclosure_record"])
	assert.Equal(t, second["disclosure_record"], list[1].(map[string]any)["disclosure_record"])
	assert.Greater(t, list[1].(map[string]any)["sequence"].(float64), list[0].(map[string]any)["sequence"].(float64), "log sequences ascend")

	one := dealRun(t, "disclosures", "--deal", dealID, "--record", second["disclosure_record"].(string))
	require.Len(t, one["disclosures"], 1)
	assert.Equal(t, second["disclosure_record"], one["disclosures"].([]any)[0].(map[string]any)["disclosure_record"])
}

func TestDealDisclosuresRefusals(t *testing.T) {
	dealFixture(t)
	dealID := openPrivateDeal(t)
	assert.Error(t, dealRunErr(t, "disclosures"), "--deal is required")
	assert.Error(t, dealRunErr(t, "disclosures", "--deal", "nope"))
	assert.Error(t, dealRunErr(t, "disclosures", "--deal", "deal-0000000000000000"), "an unknown deal")
	assert.Error(t, dealRunErr(t, "disclosures", "--deal", dealID, "--record", "XYZ"))
	assert.Error(t, dealRunErr(t, "disclosures", "--deal", dealID, "--record", "00000000000000000000000000000000000000000000000000000000000000aa"), "no such record")
}

// The read is read-only: it adds no row to the store and no entry to the log.
func TestDealDisclosuresWritesNothing(t *testing.T) {
	dealFixture(t)
	dealID := openPrivateDeal(t)
	shareRun(t, dealID, "counterparty", filepath.Join(t.TempDir(), "a.html"))
	before := dealRun(t, "checkpoint", "status", "--deal", dealID)
	dealRun(t, "disclosures", "--deal", dealID)
	assert.Equal(t, before, dealRun(t, "checkpoint", "status", "--deal", dealID))
	stored, _ := sharedRecords(t, dealID)
	assert.Len(t, stored, 1)
}

// A row that was never confirmed on the log (sequence 0: recordShare inserts
// before it appends) is not listed.
func TestDealDisclosuresSkipsAnUnconfirmedRow(t *testing.T) {
	dealFixture(t)
	dealID := openPrivateDeal(t)
	share := shareRun(t, dealID, "counterparty", filepath.Join(t.TempDir(), "a.html"))
	updateDisclosureRow(t, `UPDATE deal_disclosures SET cll_sequence=0 WHERE deal_id=?`, dealID)
	assert.Empty(t, dealRun(t, "disclosures", "--deal", dealID)["disclosures"])
	_, err := invoke(t, "", "--profile", "deal", "deal", "disclosures", "--deal", dealID, "--record", share["disclosure_record"].(string))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no disclosure record")
}

// A stored row that is not the record whose digest was sealed is a conflict,
// and nothing is printed.
func TestDealDisclosuresRefusesATamperedRow(t *testing.T) {
	dealFixture(t)
	dealID := openPrivateDeal(t)
	share := shareRun(t, dealID, "counterparty", filepath.Join(t.TempDir(), "a.html"))
	digest := share["disclosure_record"].(string)

	updateDisclosureRow(t, `UPDATE deal_disclosures SET record=replace(record, 'the shop''s support desk', 'someone else') WHERE deal_id=?`, dealID)
	out, err := invoke(t, "", "--profile", "deal", "deal", "disclosures", "--deal", dealID)
	assert.ErrorIs(t, err, cli.ErrConflict)
	assert.NotContains(t, out, "someone else")
	_, err = invoke(t, "", "--profile", "deal", "deal", "disclosures", "--deal", dealID, "--record", digest)
	assert.ErrorIs(t, err, cli.ErrConflict)

	updateDisclosureRow(t, `UPDATE deal_disclosures SET record='not json' WHERE deal_id=?`, dealID)
	_, err = invoke(t, "", "--profile", "deal", "deal", "disclosures", "--deal", dealID)
	assert.ErrorIs(t, err, cli.ErrConflict)
}

// A read_only profile reads the records, and the read changes no file of the
// store or its logs.
func TestDealDisclosuresOnAReadOnlyProfileWritesNothing(t *testing.T) {
	dealFixture(t)
	dealID := openPrivateDeal(t)
	shareRun(t, dealID, "counterparty", filepath.Join(t.TempDir(), "a.html"))
	writable := dealRun(t, "disclosures", "--deal", dealID)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.ReadOnly = true
	require.NoError(t, cli.SaveProfile(p, true))
	before := storeFiles(t, p)
	assert.Equal(t, writable, dealRun(t, "disclosures", "--deal", dealID))
	_, err = invoke(t, "", "--profile", "deal", "deal", "disclosures", "--deal", "deal-0000000000000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown deal")
	_, err = invoke(t, "", "--profile", "deal", "deal", "disclosures", "--deal", dealID, "--record", strings.Repeat("0", 64))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no disclosure record")
	assert.Equal(t, before, storeFiles(t, p))
}

func updateDisclosureRow(t *testing.T, stmt, dealID string) {
	t.Helper()
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	db, _, err := cli.SqliteConnection(p)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(stmt, dealID)
	require.NoError(t, err)
}
