package book

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const conductPack = "asg/everyday-rules"

func writeConductPacks(t *testing.T, p cli.Profile, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(p.Connection.Database, "book", conductPacksFile), []byte(body), 0o600))
}

// appendConductResult seals an evidence_result record citing pack, linked to
// the published record, into the profile's book and checkpoints it.
func appendConductResult(t *testing.T, p cli.Profile, pack, cites string) string {
	return appendResult(t, p, pack+"@1", nil, cites)
}

// appendResult seals an evidence_result record with the given subject_ref and
// claim contract_refs.
func appendResult(t *testing.T, p cli.Profile, subject string, contractRefs []string, cites string) string {
	t.Helper()
	var id string
	withBook(t, p, func(book *evidencebook.Book) {
		var claims []map[string]string
		for _, ref := range contractRefs {
			claims = append(claims, map[string]string{"contract_ref": ref})
		}
		statement, err := json.Marshal(map[string]any{"claims": claims})
		require.NoError(t, err)
		record, err := book.Append(t.Context(), evidencebook.Entry{
			RecordType: resultRecordType, EpistemicType: evidencebook.DerivedMetric, SubjectRef: subject,
			Links:     []evidencebook.Link{{Type: evidencebook.Cites, Target: cites}},
			Statement: statement,
		})
		require.NoError(t, err)
		id = record.RecordID
		_, err = book.Checkpoint(t.Context())
		require.NoError(t, err)
	})
	return id
}

// evidencebook#413 refuses a conduct pack's Result in a bundle; the CLI
// feeds it the owner's pack ids from the profile's conduct-packs file.
func TestBundleAndDiscloseRefuseAConductResult(t *testing.T) {
	p, stored := jsonlPublished(t)
	result := appendConductResult(t, p, conductPack, recordOfCapsule(t, p, stored.CapsuleID))

	for _, verb := range []string{"disclose", "bundle", "permalink"} {
		t.Run(verb+" with the pack listed", func(t *testing.T) {
			writeConductPacks(t, p, "# private to the owner\n\n"+conductPack+"\n")
			_, err := invoke(t, "", verb, "--profile", p.Name, "--root", result)
			require.ErrorIs(t, err, cli.ErrInput)
			assert.Contains(t, cli.SafeError(err), "conduct pack "+conductPack)
		})
		t.Run(verb+" with another pack listed", func(t *testing.T) {
			writeConductPacks(t, p, "asg/seller\n")
			_, err := invoke(t, "", verb, "--profile", p.Name, "--root", result)
			require.NoError(t, err)
		})
		t.Run(verb+" with no list", func(t *testing.T) {
			require.NoError(t, os.Remove(filepath.Join(p.Connection.Database, "book", conductPacksFile)))
			_, err := invoke(t, "", verb, "--profile", p.Name, "--root", result)
			require.NoError(t, err)
		})
	}
}

// An unusable list refuses rather than protects nothing: a versioned id, an
// id with a trailing comment (it would match no pack) and an unreadable file.
func TestConductPackListIsStrict(t *testing.T) {
	p, stored := jsonlPublished(t)
	result := appendConductResult(t, p, conductPack, recordOfCapsule(t, p, stored.CapsuleID))

	writeConductPacks(t, p, conductPack+"@1\n")
	_, err := invoke(t, "", "disclose", "--profile", p.Name, "--root", result)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "without a version")

	for _, line := range []string{conductPack + " # private to the owner", conductPack + "\u00a0note"} {
		writeConductPacks(t, p, line+"\n")
		_, err = invoke(t, "", "disclose", "--profile", p.Name, "--root", result)
		require.ErrorIs(t, err, cli.ErrInput)
		assert.Contains(t, cli.SafeError(err), "is not a pack id")
	}

	require.NoError(t, os.Remove(filepath.Join(p.Connection.Database, "book", conductPacksFile)))
	require.NoError(t, os.Mkdir(filepath.Join(p.Connection.Database, "book", conductPacksFile), 0o700))
	_, err = invoke(t, "", "disclose", "--profile", p.Name, "--root", result)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "cannot read the conduct pack list")
}

// The Close's bundle goes to the counterparty: a conduct Result in its
// window refuses it.
func TestCloseBundleRefusesAConductResult(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	p, _ := bookProfile(t, "a")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	var halfID string
	withBook(t, p, func(book *evidencebook.Book) {
		records, err := book.Query(t.Context(), evidencebook.Filter{})
		require.NoError(t, err)
		halfID = records[0].RecordID
	})
	appendConductResult(t, p, conductPack, halfID)
	set(day1)
	out := filepath.Join(t.TempDir(), "close-bundle.json")

	writeConductPacks(t, p, conductPack+"\n")
	_, err := invoke(t, "", "close", "--profile", "a", "--period", "day", "--counterparty", "b", "--bundle-out", out)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "conduct pack "+conductPack)
	assert.NoFileExists(t, out)

	require.NoError(t, os.Remove(filepath.Join(p.Connection.Database, "book", conductPacksFile)))
	_, err = invoke(t, "", "close", "--profile", "a", "--period", "day", "--counterparty", "b", "--bundle-out", out)
	require.NoError(t, err)
	assert.FileExists(t, out)
}

// A conduct Result just after the window only places its end: it is left out
// of the Close's bundle, which still builds.
func TestCloseBundleLeavesOutAConductResultAtTheEdge(t *testing.T) {
	for name, subject := range map[string][]string{"by subject_ref": nil, "by claim contract_ref only": {conductPack + "@1"}} {
		t.Run(name, func(t *testing.T) { closeWithEdgeResult(t, subject) })
	}
}

func closeWithEdgeResult(t *testing.T, claimRefs []string) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	p, _ := bookProfile(t, "a")
	appendHalves(t, p, half{"x1", "r1", "p1"})
	var halfID string
	withBook(t, p, func(book *evidencebook.Book) {
		records, err := book.Query(t.Context(), evidencebook.Filter{})
		require.NoError(t, err)
		halfID = records[0].RecordID
	})
	set(day1)
	if claimRefs == nil {
		appendConductResult(t, p, conductPack, halfID)
	} else {
		appendResult(t, p, "other/pack@1", claimRefs, halfID)
	}
	set(day1.AddDate(0, 0, 1))
	writeConductPacks(t, p, conductPack+"\n")
	out := filepath.Join(t.TempDir(), "close-bundle.json")
	_, err := invoke(t, "", "close", "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--bundle-out", out)
	require.NoError(t, err)
	assert.FileExists(t, out)
}

func writePackSource(t *testing.T, dir, kind, packID string) {
	t.Helper()
	folder := filepath.Join(dir, workspacePacksDir, "pack-"+kind)
	require.NoError(t, os.MkdirAll(folder, 0o700))
	body := "scope:\n  kind: " + kind + "\npack_id: " + packID + "\npack_version: \"0.1.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(folder, "pack-source.yaml"), []byte(body), 0o600))
}

// evidencebook#418: the owner's pack sources decide, so a pack marked
// scope.kind conduct is protected with no conduct-packs file at all, and a
// deal pack beside it is not.
func TestWorkspaceConductPacksDefaultTheList(t *testing.T) {
	p, stored := jsonlPublished(t)
	conduct := appendConductResult(t, p, conductPack, recordOfCapsule(t, p, stored.CapsuleID))
	deal := appendConductResult(t, p, "asg/seller", recordOfCapsule(t, p, stored.CapsuleID))
	workspace := t.TempDir()
	writePackSource(t, workspace, "conduct", conductPack)
	writePackSource(t, workspace, "deal", "asg/seller")
	t.Chdir(workspace)

	got, err := conductPacks(p)
	require.NoError(t, err)
	assert.Equal(t, []string{conductPack}, got)

	for _, verb := range []string{"disclose", "bundle", "permalink"} {
		_, err := invoke(t, "", verb, "--profile", p.Name, "--root", conduct)
		require.ErrorIs(t, err, cli.ErrInput, verb)
		assert.Contains(t, cli.SafeError(err), "conduct pack "+conductPack)
		_, err = invoke(t, "", verb, "--profile", p.Name, "--root", deal)
		require.NoError(t, err, verb)
	}

	// The file adds to the workspace's packs, never replaces them.
	writeConductPacks(t, p, "asg/other\n")
	got, err = conductPacks(p)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"asg/other", conductPack}, got)
}

// A pack source whose kind cannot be read refuses, like an unreadable list.
func TestWorkspaceConductPacksRefuseAnUnreadableSource(t *testing.T) {
	p, _ := jsonlPublished(t)
	workspace := t.TempDir()
	folder := filepath.Join(workspace, workspacePacksDir, "broken")
	require.NoError(t, os.MkdirAll(folder, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "pack-source.yaml"), []byte("scope: [unclosed\n"), 0o600))
	t.Chdir(workspace)

	_, err := conductPacks(p)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "cannot parse the pack source")

	require.NoError(t, os.WriteFile(filepath.Join(folder, "pack-source.yaml"), []byte("scope:\n  kind: conduct\n"), 0o600))
	_, err = conductPacks(p)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "without a pack_id")
}

// A record of any type that cites a listed pack is the one an edge Close
// leaves out, not only a Result.
func TestCitesConductPackForAnyRecordType(t *testing.T) {
	h := evidencebook.Header{RecordType: "observation", SubjectRef: conductPack + "@1"}
	assert.True(t, citesConductPack(h, []string{conductPack}))
	assert.False(t, citesConductPack(h, []string{"asg/seller"}))
	assert.False(t, citesConductPack(h, nil))
}

// A packs directory that cannot be read refuses, as an unreadable source
// does; filepath.Glob would have returned no packs without saying so.
func TestWorkspaceConductPacksRefuseAnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	p, _ := jsonlPublished(t)
	workspace := t.TempDir()
	writePackSource(t, workspace, "conduct", conductPack)
	t.Chdir(workspace)
	packs := filepath.Join(workspace, workspacePacksDir)
	require.NoError(t, os.Chmod(packs, 0o000))
	t.Cleanup(func() { _ = os.Chmod(packs, 0o700) })

	_, err := conductPacks(p)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "cannot read the packs directory")

	require.NoError(t, os.Chmod(packs, 0o700))
	folder := filepath.Join(packs, "pack-conduct")
	require.NoError(t, os.Chmod(folder, 0o000))
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })
	_, err = conductPacks(p)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "cannot read the pack source")
}

// A dangling pack-source.yaml symlink has a kind that cannot be told, so it
// refuses; a pack folder with no source is skipped.
func TestWorkspaceConductPacksRefuseADanglingSource(t *testing.T) {
	p, _ := jsonlPublished(t)
	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, workspacePacksDir, "empty"), 0o700))
	t.Chdir(workspace)
	got, err := conductPacks(p)
	require.NoError(t, err)
	assert.Empty(t, got)

	folder := filepath.Join(workspace, workspacePacksDir, "dangling")
	require.NoError(t, os.MkdirAll(folder, 0o700))
	require.NoError(t, os.Symlink(filepath.Join(workspace, "missing.yaml"), filepath.Join(folder, "pack-source.yaml")))
	_, err = conductPacks(p)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "cannot read the pack source")

	require.NoError(t, os.RemoveAll(folder))
	require.NoError(t, os.Symlink(filepath.Join(workspace, "gone"), filepath.Join(workspace, workspacePacksDir, "moved")))
	_, err = conductPacks(p)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.Contains(t, cli.SafeError(err), "cannot read the pack folder")
}
