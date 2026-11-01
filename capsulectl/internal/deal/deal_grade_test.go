package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func asGrade(t *testing.T, v any) dealGrade {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var g dealGrade
	require.NoError(t, json.Unmarshal(raw, &g))
	return g
}

// readOnly runs `deal report --read-only` and requires that it changed no
// byte of the store's directory.
func readOnly(t *testing.T, storeDir, dealID string) map[string]any {
	t.Helper()
	snapshot := func() map[string][32]byte {
		out := map[string][32]byte{}
		entries, err := os.ReadDir(storeDir)
		require.NoError(t, err)
		for _, e := range entries {
			if !e.IsDir() {
				out[e.Name()] = sha256.Sum256(mustRead(t, filepath.Join(storeDir, e.Name())))
			}
		}
		return out
	}
	before := snapshot()
	out := dealRun(t, "report", "--deal", dealID, "--read-only")
	require.Equal(t, before, snapshot(), "a read writes nothing")
	return out
}

func setCountersignDirectory(t *testing.T, directory string) {
	t.Helper()
	_, err := invoke(t, "", "profile", "update", "--profile", "deal", "--countersign-directory", directory)
	require.NoError(t, err)
}

// TestDealReportGradeSelfAttested: with no witness and nothing stored, the
// grade is self-attested, the witness not configured and the deal not
// countersigned, in every form of the user's own report.
func TestDealReportGradeSelfAttested(t *testing.T) {
	dir := dealFixture(t)
	dealID := openJetSki(t)
	for _, out := range []map[string]any{
		dealRun(t, "report", "--deal", dealID),
		readOnly(t, dir, dealID),
	} {
		g := asGrade(t, out["grade"])
		assert.Equal(t, "self_attested", g.Rung)
		assert.Equal(t, dealGradeWitness{State: "not_configured"}, g.Witness)
		assert.Equal(t, "not_countersigned", g.Countersign.Finding)
		assert.Empty(t, g.Countersign.Entries)
	}
	out, _, _, _ := dealReportFiles(t, dealID)
	assert.Equal(t, "self_attested", asGrade(t, out["grade"]).Rung)

	// A shared copy states no grade: it carries no countersignature (§5 Q3).
	shared := dealRun(t, "report", "--deal", dealID, "--html", filepath.Join(t.TempDir(), "s.html"), "--share", "counterparty", "--to", "the shop")
	assert.NotContains(t, shared, "grade")
}

// TestDealReportGradeCarriesTheWitnessTime: a witnessed deal's grade names
// the witnessed checkpoint's time, and witnessed in part how many steps its
// receipt covers.
func TestDealReportGradeCarriesTheWitnessTime(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	p := cadenceFixture(t, endpoint, public, "1h", "0s", 0)
	now := clockAt(t, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	dealID := retailDeal(t)

	g := asGrade(t, dealRun(t, "report", "--deal", dealID)["grade"])
	assert.Equal(t, "self_attested", g.Rung)
	assert.Equal(t, "scheduled", g.Witness.State)
	assert.Empty(t, g.Witness.At)

	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	deliver(t, p, cadenceSize(t, p), key)
	storeDir := filepath.Dir(p.Connection.Database)
	for _, out := range []map[string]any{dealRun(t, "report", "--deal", dealID), readOnly(t, storeDir, dealID)} {
		g = asGrade(t, out["grade"])
		assert.Equal(t, "witnessed", g.Rung)
		assert.Equal(t, "witnessed", g.Witness.State)
		assert.Equal(t, "2026-10-04T09:00:00Z", g.Witness.At)
		assert.NotEmpty(t, g.Witness.Witness)
		assert.Zero(t, g.Witness.StepsWitnessed)
	}

	*now = now.Add(10 * time.Minute)
	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))
	g = asGrade(t, readOnly(t, storeDir, dealID)["grade"])
	assert.Equal(t, "witnessed_in_part", g.Rung)
	assert.Equal(t, "2026-10-04T09:00:00Z", g.Witness.At)
	assert.Equal(t, g.Witness.StepsWitnessed+1, g.Witness.Steps)
}

// TestDealCountersignatureIsStoredWithItsBundle: a countersignature `deal
// countersign` verified is kept with the exact bundle it covers. A later
// report, with more steps, states it against that stored bundle (through
// step k of n, the signer resolved in the profile's directory, its
// recomputed_at) and never attaches it to its own bundle; the directory a
// caller passes does not change it.
func TestDealCountersignatureIsStoredWithItsBundle(t *testing.T) {
	dir := dealFixture(t)
	dealID := openJetSki(t)
	_, bundlePath, _, _ := dealReportFiles(t, dealID)
	covered := readBundleFile(t, bundlePath)
	digest, err := aacbundle.BundleDigest(covered)
	require.NoError(t, err)
	entry, signerKey := countersignerEntry(t, digest, []cli.CountersignCheck{{Name: "range membership", Result: "established"}})
	directory := countersignerDirectoryFile(t, "Example Countersigner", signerKey)

	// Without a directory in the profile, --directory says who signed for
	// this check; the stored countersignature is unchecked in a report.
	dealRun(t, "countersign", "--deal", dealID, "--bundle", bundlePath, "--entry", writeEntry(t, entry), "--directory", directory)
	before := asGrade(t, dealRun(t, "report", "--deal", dealID)["grade"])
	assert.Equal(t, "self_attested", before.Rung)
	assert.Equal(t, "unchecked", before.Countersign.Finding)
	require.Len(t, before.Countersign.Entries, 1)
	assert.Equal(t, "unchecked", before.Countersign.Entries[0].State)
	assert.Equal(t, "no_directory", before.Countersign.Entries[0].UncheckedReason)

	// Its bundle holds every step so far: countersigned.
	setCountersignDirectory(t, directory)
	full := asGrade(t, readOnly(t, dir, dealID)["grade"])
	assert.Equal(t, "countersigned", full.Rung)
	require.Len(t, full.Countersign.Entries, 1)
	assert.Equal(t, full.Countersign.Entries[0].Steps, full.Countersign.Entries[0].ThroughStep)

	// A directory that cannot be read is said so.
	setCountersignDirectory(t, filepath.Join(t.TempDir(), "missing.json"))
	gone := asGrade(t, readOnly(t, dir, dealID)["grade"])
	assert.Equal(t, "unchecked", gone.Countersign.Finding)
	assert.Equal(t, "directory_unreadable", gone.Countersign.Entries[0].UncheckedReason)
	setCountersignDirectory(t, directory)

	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))

	other := countersignerDirectoryFile(t, "Someone Else", signerKey)
	for i, out := range []map[string]any{
		dealRun(t, "report", "--deal", dealID),
		readOnly(t, dir, dealID),
		dealRun(t, "report", "--deal", dealID, "--from-bundle", bundlePath, "--directory", other),
	} {
		args := i
		g := asGrade(t, out["grade"])
		// The step after its bundle is not countersigned.
		assert.Equal(t, "countersigned_in_part", g.Rung, args)
		assert.Equal(t, "countersigned_in_part", g.Countersign.Rung, args)
		assert.True(t, cli.IsRungValue(g.Rung), "a ladder rung in part")
		assert.Equal(t, "Example Countersigner", g.Countersign.Signer, args)
		assert.Equal(t, entry.Statement.RecomputedAt, g.Countersign.RecomputedAt, args)
		assert.Equal(t, g.Countersign.Steps-1, g.Countersign.ThroughStep, args)
		assert.Equal(t, directory, g.Countersign.Directory, args)
		require.Len(t, g.Countersign.Entries, 1, args)
		e := g.Countersign.Entries[0]
		assert.Equal(t, "resolved", e.State, args)
		assert.Equal(t, "Example Countersigner", e.Signer, "resolved in the profile's directory, never the caller's")
		assert.Equal(t, entry.Statement.RecomputedAt, e.RecomputedAt)
		assert.Equal(t, digest, e.BundleDigest)
		assert.Less(t, e.ThroughStep, e.Steps, "it covers the steps up to its bundle, not the step after")
		assert.Positive(t, e.ThroughStep)
	}

	// A file named by a relative path (written by hand) resolves nothing.
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	p.Countersign.Directory = filepath.Base(directory)
	require.NoError(t, cli.SaveProfile(p, true))
	t.Chdir(filepath.Dir(directory))
	g := asGrade(t, readOnly(t, dir, dealID)["grade"])
	assert.Equal(t, "unchecked", g.Countersign.Finding)
	assert.Equal(t, "no_directory", g.Countersign.Entries[0].UncheckedReason)
	assert.Empty(t, g.Countersign.Directory)
	setCountersignDirectory(t, directory)

	// A later bundle carries no countersignature: it is never re-attached.
	_, later, _, _ := dealReportFiles(t, dealID)
	assert.NotContains(t, readBundleFile(t, later), "countersignatures")
}

// TestDealStoredCountersignaturesMerge: countersigning the same bundle again,
// from a fresh copy of it, keeps every countersignature stored before.
func TestDealStoredCountersignaturesMerge(t *testing.T) {
	dealFixture(t)
	dealID := openJetSki(t)
	_, bundlePath, _, _ := dealReportFiles(t, dealID)
	pristine := mustRead(t, bundlePath)
	digest, err := aacbundle.BundleDigest(readBundleFile(t, bundlePath))
	require.NoError(t, err)
	first, firstKey := countersignerEntry(t, digest, nil)
	second, secondKey := countersignerEntry(t, digest, nil)
	directory := filepath.Join(t.TempDir(), "countersigners.json")
	require.NoError(t, os.WriteFile(directory, []byte(`[{"name":"First","key_ids":["`+firstKey+`"]},{"name":"Second","key_ids":["`+secondKey+`"]}]`), 0o600))
	setCountersignDirectory(t, directory)

	dealRun(t, "countersign", "--deal", dealID, "--bundle", bundlePath, "--entry", writeEntry(t, first))
	fresh := filepath.Join(t.TempDir(), "fresh.json")
	require.NoError(t, os.WriteFile(fresh, pristine, 0o600))
	dealRun(t, "countersign", "--deal", dealID, "--bundle", fresh, "--entry", writeEntry(t, second))

	g := asGrade(t, dealRun(t, "report", "--deal", dealID, "--read-only")["grade"])
	assert.Equal(t, "countersigned", g.Rung)
	require.Len(t, g.Countersign.Entries, 2)
	var signers []string
	for _, e := range g.Countersign.Entries {
		signers = append(signers, e.Signer)
	}
	assert.ElementsMatch(t, []string{"First", "Second"}, signers)
}

// TestDealReportReadOnlyWritesNothing: `deal report --read-only` changes no
// byte of the store, and takes no flag that writes.
func TestDealReportReadOnlyWritesNothing(t *testing.T) {
	dir := dealFixture(t)
	dealID := openJetSki(t)
	assert.Equal(t, dealID, readOnly(t, dir, dealID)["deal_id"])

	for _, extra := range [][]string{
		{"--html", filepath.Join(t.TempDir(), "r.html")},
		{"--bundle", filepath.Join(t.TempDir(), "b.json")},
		{"--email", filepath.Join(t.TempDir(), "r.eml")},
		{"--from-bundle", filepath.Join(t.TempDir(), "b.json")},
		{"--share", "counterparty", "--to", "x", "--html", filepath.Join(t.TempDir(), "s.html")},
	} {
		_, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "report", "--deal", dealID, "--read-only"}, extra...)...)
		require.ErrorIs(t, err, cli.ErrInput, extra)
		assert.Contains(t, cli.SafeError(err), "--read-only writes nothing", extra)
	}

	// A deal with steps but no key row (an open that stopped) is unknown,
	// as a writer says.
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	db, _, err := cli.SqliteConnection(p)
	require.NoError(t, err)
	var key []byte
	require.NoError(t, db.QueryRow(`SELECT deal_key FROM deal_keys WHERE deal_id=?`, dealID).Scan(&key))
	_, err = db.Exec(`DELETE FROM deal_keys WHERE deal_id=?`, dealID)
	require.NoError(t, err)
	_, err = invoke(t, "", "--profile", "deal", "deal", "report", "--deal", dealID, "--read-only")
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "unknown deal: "+dealID)
	_, err = db.Exec(`INSERT INTO deal_keys (deal_id, deal_key) VALUES (?, ?)`, dealID, key)
	require.NoError(t, errors.Join(err, db.Close()))

	// deal countersign with no --directory and none in the profile says
	// what to set.
	_, err = invoke(t, "", "--profile", "deal", "deal", "countersign", "--deal", dealID, "--bundle", filepath.Join(t.TempDir(), "b.json"))
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "--directory is required when the profile names no countersigner directory")
}

// TestDealReportReadOnlyOnALogDeal: a deal whose reports are sealed on its
// own log has no checkpoint covering a step taken since its last report; a
// read cuts none, so it states the witness unchecked, where a writer's
// report cuts one.
func TestDealReportReadOnlyOnALogDeal(t *testing.T) {
	old := dealReportsOffLog
	dealReportsOffLog = false
	t.Cleanup(func() { dealReportsOffLog = old })
	dir := dealFixture(t)
	dealID := openJetSki(t)
	dealRun(t, "report", "--deal", dealID, "--html", filepath.Join(t.TempDir(), "r.html"))
	dealRun(t, "note", "--deal", dealID, "--kind", "message", "--input", writeJSON(t, `{"from":"counterparty","text":"Your order has shipped."}`))

	read := readOnly(t, dir, dealID)
	g := asGrade(t, read["grade"])
	assert.Equal(t, "unchecked", g.Witness.State)
	assert.Equal(t, "self_attested", g.Rung)
	assert.NotContains(t, read, "assurance")

	assert.Equal(t, "not_configured", asGrade(t, dealRun(t, "report", "--deal", dealID)["grade"]).Witness.State)
}
