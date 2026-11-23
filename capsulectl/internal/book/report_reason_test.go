package book

import (
	"context"
	stdhtml "html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reasonBundle seals a Result whose claims carry the optional claim member
// `reason` (AAC Evidence Result section 4.4, capsule-cli#612), discloses it
// as a book-form bundle and returns the bundle's path.
func reasonBundle(t *testing.T) (bundlePath string) {
	t.Helper()
	b := newResultBook(t)
	met := testClaim("claim-1", "met", b.capsules[0])
	met["reason"] = "counterparty_leg_signer_not_trusted"
	gap := testClaim("claim-2", "not_evaluable", b.capsules[1])
	gap["reason"] = "capsule_signature_did_not_verify"
	built, _, err := runResultBuild(t, b.profile, resultDoc(met, gap), "--contract", testContract)
	require.NoError(t, err, "the pinned capsule-cli seals a Result carrying reason")
	bundlePath = filepath.Join(t.TempDir(), "bundle.json")
	_, err = invoke(t, "", "disclose", "--profile", b.profile.Name, "--root", built.RecordID, "--payloads", "selected", "--out", bundlePath)
	require.NoError(t, err)
	verified, err := verifyBundleOutput(t, bundlePath)
	require.NoError(t, err)
	require.Equal(t, "VALID", verified["verdict"])
	return bundlePath
}

// A Result sealed by `result build` with a reason on its claims, disclosed
// and built into a page by the report SDK with the contract report selected
// (View.ContractReport; `report build` has no flag for it yet), shows each
// reason in the viewer runtime's words: the pack's
// `row.<ref>.reason.<code>` entry where it has one, the core words for the
// four core codes otherwise (capsule-viewer#345, #348).
func TestContractReportDrawsAClaimReason(t *testing.T) {
	bundlePath := reasonBundle(t)
	pack := `{"wording_pack_version":"aac.wording-pack/v0","id":"example.host/v0","locale":"en","entries":{"row.req-claim-1.reason.counterparty_leg_signer_not_trusted":"The other side signs with a key we have not been given."}}`
	_, digest := wordingFixture(t, pack)
	// The SDK draws the generic Result page, which carries the reasons.
	built, err := report.Build(mustRead(t, bundlePath), report.View{Card: "outcome", ContractReport: true, Wording: &report.Wording{Pack: pack, SHA256: digest}})
	require.NoError(t, err)
	assert.Equal(t, "VALID", built.Verdict)
	page := string(built.HTML)
	assert.Contains(t, page, "counterparty_leg_signer_not_trusted", "the page carries the sealed reason")
	assert.Contains(t, page, "generic.reason.token.capsule_signature_did_not_verify", "the runtime holds core words for the reason codes")

	// The runtime builds a row's claims when the row opens; a depth of L2 in
	// the render options opens them all, so the dump holds the reason lines.
	require.Contains(t, page, `{"audience":"*"`)
	opened := strings.Replace(page, `{"audience":"*"`, `{"audience":"*","depth":"L2"`, 1)
	// The edit changes the script's hash, which the page's content security
	// policy pins; the policy is dropped from this render-only copy.
	opened = regexp.MustCompile(`<meta http-equiv="Content-Security-Policy"[^>]*>`).ReplaceAllString(opened, "")
	text := reasonPageText(t, []byte(opened))
	assert.Contains(t, text, "The report's producer gives this reason: The other side signs with a key we have not been given.", "the pack's row words for the reason")
	assert.Contains(t, text, "The report's producer gives this reason: A record this claim needed has a signature that did not verify, so the record was not used.", "the core words where the pack has none")
}

var (
	reasonCodeBlocks = regexp.MustCompile(`(?s)<(script|style)\b.*?</(script|style)>`)
	reasonMarkup     = regexp.MustCompile(`<[^>]*>`)
)

// reasonPageText renders the page in headless Chrome, as the viewer's own
// checks do, and returns its text with every row and claim opened. With
// EVIDENCEBOOK_CHROME unset the test is skipped.
func reasonPageText(t *testing.T, page []byte) string {
	t.Helper()
	chrome := os.Getenv("EVIDENCEBOOK_CHROME")
	if chrome == "" {
		t.Skip("EVIDENCEBOOK_CHROME is not set; the reason words are drawn by the page's runtime in a browser")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "page.html")
	require.NoError(t, os.WriteFile(file, page, 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-first-run", "--no-sandbox",
		"--use-mock-keychain", "--password-store=basic", "--user-data-dir="+filepath.Join(dir, "profile"), "--disable-dev-shm-usage", "--window-size=1280,1000", "--virtual-time-budget=30000",
		"--dump-dom", "file://"+file)
	cmd.Stdin = nil
	out, err := cmd.Output()
	// Chrome can fail its own teardown after it has dumped the document;
	// the dump, not the exit status, is what this reads.
	if err != nil && !strings.HasSuffix(strings.TrimSpace(string(out)), "</html>") {
		require.NoError(t, err, "headless Chrome")
	}
	text := reasonMarkup.ReplaceAllString(reasonCodeBlocks.ReplaceAllString(string(out), ""), "\n")
	return strings.Join(strings.Fields(stdhtml.UnescapeString(text)), " ")
}
