package deal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deal profile is written by capsulectl deal only (capsule-cli#242): a
// core write on it is refused, so it never lands on the witnessed cadence
// log or a deal's log behind the deal store's back, while the deal's own
// steps still seal and core reads still work.
func TestCoreRefusesWritesOnADealProfile(t *testing.T) {
	dealFixture(t)
	dealID := retailDeal(t)
	step := dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"user","text":"sealed by deal"}`))
	capsule := filepath.Join(t.TempDir(), "step.json")
	_, err := invoke(t, "", "get", "--profile", "deal", "--capsule-id", step["capsule_id"].(string), "--raw", "--output", capsule)
	require.NoError(t, err, "a core read of a deal's record still works")
	before, err := invoke(t, "", "cll", "list", "--profile", "deal", "--log-id", cli.DealLogID(dealID))
	require.NoError(t, err, "a core read of a deal's log still works")

	for _, args := range [][]string{
		{"publish", "--profile", "deal", "--request", sealRequestFile(t, "stray")},
		{"cll", "append", "--profile", "deal", "--capsule", capsule},
		{"store", "init", "--profile", "deal"},
	} {
		_, err = invoke(t, "", args...)
		assert.ErrorIs(t, err, cli.ErrPluginStoreWrite, "%v", args)
	}
	after, err := invoke(t, "", "cll", "list", "--profile", "deal", "--log-id", cli.DealLogID(dealID))
	require.NoError(t, err)
	assert.Equal(t, before, after, "the deal's log is unchanged")

	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"user","text":"still sealing"}`))
}

// The deal session is a writer while it holds the exclusive lock, and no
// longer once closed; a read session under the shared lock never is.
func TestDealSessionHoldsTheStoreWhileOpen(t *testing.T) {
	dealFixture(t)
	retailDeal(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { _ = s.close() }()
	assert.True(t, s.HoldsPluginStore())
	require.NoError(t, s.close())
	assert.False(t, s.HoldsPluginStore(), "a closed session")
	_, err = openOwnedTarget(t.Context(), p, cli.UseCLL, s)
	assert.ErrorIs(t, err, cli.ErrPluginStoreWrite)

	r, err := openDealStoreRead(t.Context(), p)
	require.NoError(t, err)
	defer func() { require.NoError(t, r.close()) }()
	assert.False(t, r.HoldsPluginStore(), "a read under the shared lock")
	_, err = openOwnedTarget(t.Context(), p, cli.UseCLL, r)
	assert.ErrorIs(t, err, cli.ErrPluginStoreWrite)
}

// A deal profile edited to look plain (its namespace cleared) still cannot
// be written by core: the store carries the shelf's marker.
func TestCoreRefusesWritesOnAShelfStoreWhateverTheProfileSays(t *testing.T) {
	dealFixture(t)
	retailDeal(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Log, p.Namespace = "", "plain"
	require.NoError(t, cli.SaveProfile(p, true))
	require.Empty(t, p.LogOwner(), "the profile no longer names the plugin")
	_, err = invoke(t, "", "publish", "--profile", "deal", "--request", sealRequestFile(t, "stray"))
	assert.ErrorIs(t, err, cli.ErrPluginStoreWrite)
}

func sealRequestFile(t *testing.T, action string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), action+".json")
	body := fmt.Sprintf(`{"spec_version":"capsule-seal-request/v1","capsule":{"ActionID":%q,"ActionType":"fyi","Operator":"test-operator","Developer":"test-developer","Timestamp":"2026-09-08T00:00:00Z"},"payload":{"a":1},"agent_output":{"ok":true}}`, action)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// deal init writes log: deal into the profile, and the store records the deal
// plugin as its owner (the core's plugin_owned marker), so the core refuses it
// even through a profile edited to look like a plain one of the core's.
func TestADealStoreRecordsItsOwner(t *testing.T) {
	dealFixture(t)
	retailDeal(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	assert.Equal(t, Name, p.Log, "deal init names the plugin as the log owner")

	db, err := sql.Open("sqlite", "file:"+p.Connection.Database+"?mode=ro")
	require.NoError(t, err)
	var owner string
	require.NoError(t, db.QueryRow(`SELECT owner FROM plugin_owned LIMIT 1`).Scan(&owner))
	require.NoError(t, db.Close())
	assert.Equal(t, Name, owner)

	// Edited to look like a plain profile of the core's, the store is still
	// refused: by the marker, and by the older shelf table that sits beside it
	// (TestCoreRefusesWritesOnAShelfStoreWhateverTheProfileSays).
	p.Namespace, p.Log = "plain", cli.LogCLL
	require.NoError(t, cli.SaveProfile(p, true))
	_, err = invoke(t, "", "publish", "--profile", "deal", "--request", sealRequestFile(t, "stray"))
	assert.ErrorIs(t, err, cli.ErrPluginStoreWrite)
}

// A store from before the owner marker gets it on the first write session, even
// one that opens no target of its own (a tick without a witness writes only the
// shelf); a read session leaves it as it is.
func TestAWriteSessionMarksAnOlderStore(t *testing.T) {
	dealFixture(t)
	retailDeal(t)
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	marker := func() (owner string, present bool) {
		db, err := sql.Open("sqlite", "file:"+p.Connection.Database+"?mode=ro")
		require.NoError(t, err)
		defer db.Close()
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'plugin_owned'`).Scan(&n))
		if n == 0 {
			return "", false
		}
		require.NoError(t, db.QueryRow(`SELECT owner FROM plugin_owned LIMIT 1`).Scan(&owner))
		return owner, true
	}
	rw, err := sql.Open("sqlite", "file:"+p.Connection.Database)
	require.NoError(t, err)
	_, err = rw.Exec(`DROP TABLE plugin_owned`)
	require.NoError(t, err)
	require.NoError(t, rw.Close())
	_, present := marker()
	require.False(t, present, "an older store has no owner marker")

	r, err := openDealStoreRead(t.Context(), p)
	require.NoError(t, err)
	require.NoError(t, r.close())
	_, present = marker()
	assert.False(t, present, "a read session writes nothing")

	s, err := openDealSession(t.Context(), p)
	require.NoError(t, err)
	defer func() { _ = s.close() }()
	require.NoError(t, s.close())
	owner, present := marker()
	assert.True(t, present)
	assert.Equal(t, Name, owner)
}
