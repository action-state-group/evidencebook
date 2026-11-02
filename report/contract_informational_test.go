// SPDX-License-Identifier: Apache-2.0

package report

import (
	"context"
	stdhtml "html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// informationalDir is a month of eight support conversations where the one
// informational rule (communication.no_invented_terms) is not met in three of
// them (1, 2 and 4), every must-have rule being met there; conversation 3
// misses a must-have rule and conversation 6 has one the judge could not
// decide. It is built the way fixture 4 is (evidencebook-deal's
// scripts/presentation-goldens/month.py with those verdicts), and sealed
// through the real CLI. The Result carries the informational not_met rows;
// evidencebook-skills#68 (merged as #103) sets such rows aside in the
// resolved answer a pack's Results produce, and capsule-viewer#201's
// contract-report module sets them aside in the lines the page derives.
const informationalDir = "4b-monthly-outcome-informational"

var (
	resolvedLine = regexp.MustCompile(`\d+ of \d+ conversations resolved: every rule that applied was met\. \d+ missed at least one\. \d+ could not be decided\.`)
	sealedNote   = "The sealed result counts informational terms too; the lines this report derives leave them out."
)

// pageText renders html in headless Chrome, as the viewer's own browser
// checks do, and returns the document's visible text. EVIDENCEBOOK_CHROME
// names the browser; with it unset the test is skipped.
func pageText(t *testing.T, html []byte) string {
	t.Helper()
	chrome := os.Getenv("EVIDENCEBOOK_CHROME")
	if chrome == "" {
		t.Skip("EVIDENCEBOOK_CHROME is not set; the page's derived lines are read in headless Chrome")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "page.html")
	require.NoError(t, os.WriteFile(file, html, 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-first-run", "--no-sandbox",
		"--user-data-dir="+filepath.Join(dir, "profile"), "--disable-dev-shm-usage", "--window-size=1280,1000", "--virtual-time-budget=30000",
		"--dump-dom", "file://"+file)
	cmd.Stdin = nil
	out, err := cmd.Output()
	require.NoError(t, err, "headless Chrome")
	return visibleText(string(out))
}

var (
	codeBlocks = regexp.MustCompile(`(?s)<(script|style)\b.*?</(script|style)>`)
	markup     = regexp.MustCompile(`<[^>]*>`)
)

// visibleText is the document's text with its script and style source and
// its tags dropped, one line per element.
func visibleText(dom string) string {
	text := markup.ReplaceAllString(codeBlocks.ReplaceAllString(dom, ""), "\n")
	return stdhtml.UnescapeString(text)
}

// An informational not-met does not change the must-have answer
// (evidencebook#259, the #204 follow-up). The Result's claims keep every
// verdict; with the compiled contract the page marks the rule informational
// and counts six conversations resolved, the three whose only miss is the
// informational rule among them. Without a contract no row is set aside, so
// the same bundle reads three resolved.
func TestAnInformationalNotMetKeepsTheMustHaveAnswer(t *testing.T) {
	raw := mustRead(t, filepath.Join(presentationDir, informationalDir, "bundle.json"))
	withContract, err := Build(raw, View{Card: "outcome", Contract: pinned(outcomeContract), ContractReport: true})
	require.NoError(t, err)
	assert.Equal(t, "VALID", withContract.Verdict)
	assert.Equal(t, map[string]string{
		"resolution.done_in_full":         TierMustHave,
		"resolution.right_change":         TierMustHave,
		"policy.confirmed_before_acting":  TierMustHave,
		"communication.no_invented_terms": TierInformational,
	}, withContract.Tiers)
	assert.Equal(t, 32, withContract.Claims, "the Result still carries every claim, the informational not_met rows included")

	withoutContract, err := Build(raw, View{Card: "outcome", ContractReport: true})
	require.NoError(t, err)
	assert.Equal(t, withContract.BundleDigest, withoutContract.BundleDigest, "the contract is a page input, not part of the bundle")

	with := pageText(t, withContract.HTML)
	assert.Contains(t, with, sealedNote)
	assert.Equal(t, []string{"6 of 8 conversations resolved: every rule that applied was met. 1 missed at least one. 1 could not be decided."}, resolvedLine.FindAllString(with, -1))
	assert.Contains(t, with, "Informational terms · 1")
	assert.Contains(t, with, "Must-have terms · 3")

	without := pageText(t, withoutContract.HTML)
	assert.NotContains(t, without, sealedNote)
	assert.Equal(t, []string{"3 of 8 conversations resolved: every rule that applied was met. 4 missed at least one. 1 could not be decided."}, resolvedLine.FindAllString(without, -1))
}
