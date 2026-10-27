package book

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sealedBookBundle is a book and a book-form Result-root bundle disclosed
// from it.
func sealedBookBundle(t *testing.T) (cli.Profile, string) {
	t.Helper()
	b := newResultBook(t)
	built, _, err := runResultBuild(t, b.profile, resultDoc(testClaim("claim-1", "met", b.capsules[0]), testClaim("claim-2", "not_evaluable", b.capsules[1])))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "bundle.json")
	_, err = invoke(t, "", "disclose", "--profile", b.profile.Name, "--root", built.RecordID, "--payloads", "selected", "--out", path)
	require.NoError(t, err)
	return b.profile, path
}

func checkPageFile(t *testing.T, p cli.Profile, page string) (reportCheckResult, error) {
	t.Helper()
	stdout, err := invoke(t, "", "report", "check", "--profile", p.Name, "--page", page)
	var result reportCheckResult
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		require.NoError(t, json.Unmarshal([]byte(stdout), &result), stdout)
	}
	return result, err
}

// editedPage writes a copy of page with the first occurrence of old replaced
// by replacement, which must be the same length: a one-byte-class edit.
func editedPage(t *testing.T, page, old, replacement string) string {
	t.Helper()
	raw, err := os.ReadFile(page)
	require.NoError(t, err)
	require.Equal(t, len(old), len(replacement))
	require.Contains(t, string(raw), old)
	out := filepath.Join(t.TempDir(), "edited.html")
	require.NoError(t, os.WriteFile(out, []byte(strings.Replace(string(raw), old, replacement, 1)), 0o600))
	return out
}

func TestReportSealMatchesTheUntouchedPage(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	built, page, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal")
	require.NoError(t, err)
	require.NotNil(t, built.PageSeal)
	assert.NotEmpty(t, built.PageSeal.RecordID)
	assert.NotZero(t, built.PageSeal.Checkpoint, "the seal is covered by a checkpoint at once")

	// The record carries the page digest, the bundle digest and the Result
	// root, and nothing else.
	withBook(t, p, func(book *evidencebook.Book) {
		record, err := book.Get(t.Context(), built.PageSeal.RecordID)
		require.NoError(t, err)
		assert.Equal(t, report.RecordTypePageSeal, record.Header.RecordType)
		var statement map[string]string
		require.NoError(t, json.Unmarshal(record.Header.Statement, &statement))
		assert.Equal(t, map[string]string{"page_digest": report.Digest(mustRead(t, page)), "bundle_digest": built.BundleDigest, "result_root": built.Root}, statement)
	})

	result, err := checkPageFile(t, p, page)
	require.NoError(t, err)
	assert.Equal(t, report.PageMatch, result.Page)
	assert.Equal(t, "VALID", result.Verification)
	assert.Equal(t, built.PageSeal.RecordID, result.RecordID)

	// A repeat of the same page seals no second record.
	again, _, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal")
	require.NoError(t, err)
	assert.True(t, again.PageSeal.AlreadySealed)
	assert.Equal(t, built.PageSeal.RecordID, again.PageSeal.RecordID)
}

func TestReportSealMismatchesAChangedPageWhileTheEvidenceStillVerifies(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	_, page, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal")
	require.NoError(t, err)
	for name, edited := range map[string]string{
		"shell text": editedPage(t, page, "<!doctype html>", "<!DOCTYPE html>"),
		"page code":  editedPage(t, page, "\"use strict\";", "\"use strict\":"),
	} {
		result, err := checkPageFile(t, p, edited)
		require.ErrorIs(t, err, cli.ErrConflict, name)
		assert.Equal(t, 5, cli.ExitCode(err), name)
		assert.Equal(t, report.PageMismatch, result.Page, name)
		assert.Equal(t, "VALID", result.Verification, name+": the embedded evidence is untouched")
	}
}

func TestReportCheckReportsAFailedEvidenceVerificationApartFromTheSeal(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	_, page, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal")
	require.NoError(t, err)
	// A change inside the embedded bundle is a different bundle: no seal for
	// it, and the evidence is what fails.
	raw := mustRead(t, page)
	i := strings.Index(string(raw), "window.__BUNDLE__ = ")
	require.NotEqual(t, -1, i)
	j := i + strings.Index(string(raw[i:]), "\"records\"")
	broken := append(append([]byte{}, raw[:j]...), append([]byte("\"recordz\""), raw[j+len("\"records\""):]...)...)
	out := filepath.Join(t.TempDir(), "broken.html")
	require.NoError(t, os.WriteFile(out, broken, 0o600))
	result, err := checkPageFile(t, p, out)
	require.Error(t, err)
	assert.NotErrorIs(t, err, cli.ErrConflict)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Equal(t, "INVALID", result.Verification)
	assert.Equal(t, report.PageNoSeal, result.Page)
}

func TestReportCheckWithNoSealedRecord(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	_, page, err := runReportBuild(t, bundle, "--card", "outcome")
	require.NoError(t, err)
	result, err := checkPageFile(t, p, page)
	require.ErrorIs(t, err, cli.ErrPartial)
	assert.Equal(t, 3, cli.ExitCode(err))
	assert.Equal(t, report.PageNoSeal, result.Page)
	assert.Equal(t, "VALID", result.Verification)

	// A dry run seals nothing, even with --seal.
	_, draft, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal", "--dry-run")
	require.NoError(t, err)
	result, err = checkPageFile(t, p, draft)
	require.ErrorIs(t, err, cli.ErrPartial)
	assert.Equal(t, report.PageNoSeal, result.Page)
}

func TestReportCheckWithASealNoCheckpointCoversYet(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	built, page, err := runReportBuild(t, bundle, "--card", "outcome")
	require.NoError(t, err)
	// The seal as an interrupted `--seal` leaves it: appended, not yet
	// checkpointed.
	withBook(t, p, func(book *evidencebook.Book) {
		body, err := json.Marshal(map[string]string{"page_digest": report.Digest(mustRead(t, page)), "bundle_digest": built.BundleDigest, "result_root": built.Root})
		require.NoError(t, err)
		_, err = book.Append(t.Context(), evidencebook.Entry{RecordType: report.RecordTypePageSeal, EpistemicType: evidencebook.ProducerClaim, SubjectRef: built.BundleDigest, Statement: body})
		require.NoError(t, err)
		covered, err := book.CheckpointCovers(t.Context(), 1<<62)
		require.NoError(t, err)
		require.False(t, covered)
	})
	result, err := checkPageFile(t, p, page)
	require.ErrorIs(t, err, cli.ErrPartial)
	assert.Equal(t, report.PageNotCovered, result.Page)
	assert.NotEmpty(t, result.RecordID)
}

func TestReportCheckRefusesAFileThatIsNotAReportPage(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	_, err := checkPageFile(t, p, bundle)
	require.ErrorIs(t, err, cli.ErrInput)
}

func TestReportBuildSealNeedsABookProfile(t *testing.T) {
	_, bundle := sealedBookBundle(t)
	p, _ := profileFixture(t) // a mysql profile, not a book
	_, _, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal")
	require.ErrorIs(t, err, cli.ErrInput)
}

// A page whose embedded record signature was damaged is INVALID evidence, as
// `report build` and `verify --bundle` call it, even though the page is
// otherwise well formed.
func TestReportCheckJudgesTheEmbeddedSignatures(t *testing.T) {
	p, bundle := sealedBookBundle(t)
	_, page, err := runReportBuild(t, bundle, "--card", "outcome", "--profile", p.Name, "--seal")
	require.NoError(t, err)
	record := reportEmbeddedBundle(t, string(mustRead(t, page)))["records"].([]interface{})[0].(map[string]interface{})
	sig := record["signature"].(string)
	flipped := sig[:len(sig)-1] + map[bool]string{true: "1", false: "0"}[sig[len(sig)-1] == '0']
	result, err := checkPageFile(t, p, editedPage(t, page, sig, flipped))
	require.ErrorIs(t, err, cli.ErrBundleInvalid)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Equal(t, "INVALID", result.Verification)
}

// A VALID bundle whose report would show fewer rows than it has is refused by
// the page gate, which is not a verdict on its evidence: `report check` on a
// page embedding it says VALID and exits with the gate's code.
func TestReportCheckKeepsTheVerdictApartFromTheRowGate(t *testing.T) {
	dir, out, err := runReportVariant(t, func(s string) string {
		return replaced(t, s, `references: [cite($permissions)]`, `references: [cite("`+strings.Repeat("0", 64)+`")]`, readmeDiscloseOK, readmeOutOnly)
	})
	require.NoError(t, err, out)
	bundle := mustRead(t, filepath.Join(dir, "bundle.json"))
	page := filepath.Join(t.TempDir(), "page.html")
	require.NoError(t, os.WriteFile(page, []byte("<!doctype html><script>window.__BUNDLE__ = "+strings.TrimSpace(string(bundle))+";</script>"), 0o600))
	p, _ := sealedBookBundle(t)
	result, err := checkPageFile(t, p, page)
	assert.Equal(t, "VALID", result.Verification)
	assert.Equal(t, report.PageNoSeal, result.Page)
	require.ErrorIs(t, err, cli.ErrPartial)
	assert.Equal(t, 3, cli.ExitCode(err))
	assert.Contains(t, cli.SafeError(err), "rows")
}
