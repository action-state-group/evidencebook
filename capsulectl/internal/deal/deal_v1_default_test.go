package deal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freshProfile is a new deal profile made as a person makes one: `deal init`
// with no version setting, so x-deal-v1, the default.
func freshProfile(t *testing.T) string {
	t.Helper()
	dealTestEnv(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "deal")
	out, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", dir, "--no-witness", "--materiality", neutralMateriality)
	require.NoError(t, err, out)
	return dir
}

// A new profile writes x-deal-v1 deals with no set-version: its store starts
// in the new layout, no empty backup is left, and a later --migrate (from
// someone who reads the help) finds it already moved.
func TestANewProfileWritesV1ByDefault(t *testing.T) {
	freshProfile(t)
	p := v1Profile(t)
	v, err := dealVersionSetting(p)
	require.NoError(t, err)
	assert.Equal(t, dealVersionV1, v)
	backup, err := backupPath(p)
	require.NoError(t, err)
	_, err = os.Stat(backup)
	assert.ErrorIs(t, err, os.ErrNotExist, "the empty store's backup is removed")

	out := dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))
	assert.NotEmpty(t, out["deal_id"])
	assert.Contains(t, v1Err(t, "open", "--input", filepath.Join(retailDemo, "open.json")), "`deal open` starts a legacy x-deal-v0 deal, and this profile writes x-deal-v1 deals")
	assert.Contains(t, v1Err(t, "sale", "new", "--input", writeJSON(t, saleInput)), "`deal sale new` starts a legacy x-deal-v0 sale")

	again := dealRun(t, "set-version", "x-deal-v1", "--migrate")
	assert.Equal(t, dealVersionV1, again["deal_version"])
	assert.Nil(t, again["moved"], "already in the new layout")
}

// The settings on each kind of store: the legacy setting lets deal open and
// refuses deal offer; with no setting an old-layout store refuses deal offer
// until it is moved, and deal open until the legacy setting is chosen.
func TestTheVersionSettingOnEachStore(t *testing.T) {
	freshProfile(t)
	dealRun(t, "set-version", "x-deal-v0")
	assert.NotEmpty(t, dealRun(t, "open", "--input", filepath.Join(retailDemo, "open.json"))["deal_id"], "the legacy setting opens a v0 deal")
	assert.Contains(t, v1Err(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json")), "legacy x-deal-v0")

	// An old-layout store with no setting (one made before the flip).
	dealTestEnv(t, t.TempDir())
	legacyInit(t)
	out, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", filepath.Join(t.TempDir(), "old"), "--no-witness", "--materiality", neutralMateriality)
	require.NoError(t, err, out)
	msg := v1Err(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))
	assert.Contains(t, msg, "older layout, and x-deal-v1 deals (the default) need the new one")
	assert.Contains(t, msg, "deal set-version x-deal-v1 --migrate")
	assert.Contains(t, v1Err(t, "open", "--input", filepath.Join(retailDemo, "open.json")), "deal set-version x-deal-v0")
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	assert.NotEmpty(t, dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))["deal_id"])
}

// A failure moving the new store after the profile exists names the command
// that finishes it, and that command does, without recreating the profile.
func TestAFailedNewStoreMoveIsFinishedBySetVersion(t *testing.T) {
	real := migrateNewStoreFn
	t.Cleanup(func() { migrateNewStoreFn = real })
	migrateNewStoreFn = func(context.Context, cli.Profile) error { return errors.New("the disk is full") }
	dealTestEnv(t, t.TempDir())
	out, err := invoke(t, "", "deal", "init", "--profile", "deal", "--dir", filepath.Join(t.TempDir(), "deal"), "--no-witness", "--materiality", neutralMateriality)
	require.Error(t, err)
	assert.Contains(t, out+err.Error(), "deal set-version x-deal-v1 --migrate` to finish")
	migrateNewStoreFn = real
	moved := dealRun(t, "set-version", "x-deal-v1", "--migrate")
	assert.Equal(t, true, moved["moved"])
	assert.NotEmpty(t, dealRun(t, "offer", "--input", writeJSON(t, v1OfferInput), "--out", filepath.Join(t.TempDir(), "o.json"))["deal_id"])
}

// A deployed canary profile has no version setting: its run pins the legacy
// x-deal-v0, which its scripted deal is, says so, and ticks.
func TestACanaryWithNoSettingPinsItselfAndTicks(t *testing.T) {
	canaryFixture(t)
	p := v1Profile(t)
	dir, err := shDir(p)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, dealVersionFile)))
	got, err := canaryRun(t)
	require.NoError(t, err)
	assert.Equal(t, "ok", got["canary"])
	assert.Equal(t, "ticked", got["tick"])
	assert.Contains(t, got["version_pinned"], "set to the legacy x-deal-v0")
	v, err := dealVersionSetting(p)
	require.NoError(t, err)
	assert.Equal(t, dealVersionV0, v)
}

// The card given on a signed ask defaults to standard_history/3 (Steven
// u536). On an old-layout store with no --derivation it says both ways on;
// /1 stays an explicit choice; a store holding an x-deal-v0 deal gets the
// note naming /1.
func TestTheCardDefaultsToStandardHistory3(t *testing.T) {
	dealFixture(t) // an old-layout store with x-deal-v0 deals
	now := clockAt(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	id := retailDeal(t)
	dealRun(t, "close", "--deal", id, "--input", writeJSON(t, `{"status":"not_received","reason":"not_delivered"}`))
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	*now = now.Add(48 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	msg := v1Err(t, "standard-history", "card", "--out", filepath.Join(t.TempDir(), "c.json"))
	assert.Contains(t, msg, "counts x-deal-v1 deals by default (standard_history/3)")
	assert.Contains(t, msg, "--derivation standard_history/1")
	assert.Contains(t, msg, "deal set-version x-deal-v1 --migrate")
	v0 := dealRun(t, "standard-history", "card", "--derivation", "standard_history/1", "--out", filepath.Join(t.TempDir(), "c1.json"))
	assert.Nil(t, v0["note"], "the /1 card needs no note")

	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	*now = now.Add(48 * time.Hour)
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	card := dealRun(t, "standard-history", "card", "--out", filepath.Join(t.TempDir(), "c3.json"))
	assert.Equal(t, "standard_history/3", card["derivation"])
	assert.Contains(t, card["note"], "your x-deal-v0 deals are counted on --derivation standard_history/1")
}

// An x-deal-v0 deal reads as before once the default is x-deal-v1, on its
// old-layout store and after the move: its report, export and bundle.
func TestAV0DealReadsTheSameAfterTheFlip(t *testing.T) {
	dealFixture(t)
	id := retailDeal(t)
	read := func() (string, string) {
		report, err := invoke(t, "", "--profile", "deal", "deal", "report", "--deal", id, "--read-only")
		require.NoError(t, err, report)
		export := filepath.Join(t.TempDir(), "e.json")
		dealRun(t, "export", "--deal", id, "--output", export)
		raw, err := os.ReadFile(export)
		require.NoError(t, err)
		return report, string(raw)
	}
	report, export := read()
	// The setting removed: the default (x-deal-v1) governs new deals only.
	p := v1Profile(t)
	dir, err := shDir(p)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, dealVersionFile)))
	r2, e2 := read()
	assert.Equal(t, report, r2)
	assert.Equal(t, export, e2)
	dealRun(t, "set-version", "x-deal-v1", "--migrate")
	r3, e3 := read()
	assert.Equal(t, export, e3, "the sealed records are unchanged by the move")
	assert.NotEmpty(t, r3)
	assert.Equal(t, report, r3, "its report reads the same after the move")
	bundle := filepath.Join(t.TempDir(), "b.json")
	dealRun(t, "report", "--deal", id, "--bundle", bundle)
	result, err := verifyWithDirectory(t, bundle, "")
	require.NoError(t, err)
	assert.Equal(t, "VALID", result["verdict"], "the v0 bundle verifies after the move")
}

// A buyer's thread of an x-deal-v0 sale opened before the flip follows the
// sale's version: a profile with no setting still opens it.
func TestAV0SaleOpenBeforeTheFlipTakesABuyer(t *testing.T) {
	dealFixture(t)
	saleID, _ := newSale(t)
	p := v1Profile(t)
	dir, err := shDir(p)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, dealVersionFile)))
	assert.NotEmpty(t, buyerThread(t, saleID, "buyer-a.example"), "the sale's thread opens on the default")
	assert.Contains(t, v1Err(t, "sale", "new", "--input", writeJSON(t, saleInput)), "legacy x-deal-v0 sale")
}

// On a fresh profile, a card asked for right after declaring says when the
// next checkpoint can be cut, which covers the declaration.
func TestAFreshProfilesCardSaysWhenToAskAgain(t *testing.T) {
	freshProfile(t)
	dealRun(t, "standard-history", "declare", "--holder-kind", "node")
	msg := v1Err(t, "standard-history", "card", "--out", filepath.Join(t.TempDir(), "c.json"))
	assert.Contains(t, msg, "run `deal tick` then and ask again")
}

// The flip leaves the staple alone: it is history_card/2 (Steven u539).
func TestTheStapleIsUnchangedByTheFlip(t *testing.T) {
	freshProfile(t)
	cmd := shStapleCommand()
	assert.Equal(t, "history_card/2", cmd.Flags().Lookup("derivation").DefValue)
}
