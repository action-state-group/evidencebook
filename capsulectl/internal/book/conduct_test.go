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
