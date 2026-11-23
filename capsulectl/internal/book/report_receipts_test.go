package book

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	stdhtml "html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `book report build --receipts` (evidencebook#764): the settlement report
// carries the deals' receipts. The Result is report/testdata/settlement-receipts
// (d1:: and, in shared-bundle.json, d2:: claims citing one capsule), the
// receipts are real deal receipts (a deal view fixture) paired to that capsule
// by the caller's file, as a node's settlement-report.sh pairs them.
const (
	settlementDir = "../../../report/testdata/settlement-receipts"
	dealReceipt   = "../../testdata/presentation/2-unilateral-receipt/bundle.json"
)

// settlementExchange is the capsule the settlement Result's claims cite.
func settlementExchange(t *testing.T) string {
	t.Helper()
	var root struct {
		Root string `json:"root"`
	}
	require.NoError(t, json.Unmarshal(mustRead(t, filepath.Join(settlementDir, "receipt.json")), &root))
	return root.Root
}

// receiptsFile writes a --receipts file of entries and returns its path.
func receiptsFile(t *testing.T, entries any) string {
	t.Helper()
	raw, err := json.Marshal(entries)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "receipts.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// buildSettlement runs `report build` on the settlement bundle with args and
// returns the page and the command's output.
func buildSettlement(t *testing.T, bundle string, args ...string) ([]byte, string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// --receipts runs the deal plugin's verify through the capsulectl that
	// launched the book plugin.
	t.Setenv("CAPSULECTL_BIN", filepath.Join(installedCapsulectl(t), "capsulectl"))
	out := filepath.Join(t.TempDir(), "page.html")
	full := []string{"report", "build", "--bundle", filepath.Join(settlementDir, bundle), "--card", "settlement", "--out", out}
	output, err := invoke(t, "", append(full, args...)...)
	if err != nil {
		return nil, output, err
	}
	return mustRead(t, out), output, nil
}

// pageOptions is the render options the page passes to renderEvidenceGraph.
func pageOptions(t *testing.T, page []byte) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`renderEvidenceGraph\(window\.__BUNDLE__, document\.getElementById\("app"\), undefined, (\{.*\})\);`).FindSubmatch(page)
	require.NotNil(t, m, "the page passes render options")
	var options map[string]any
	require.NoError(t, json.Unmarshal(m[1], &options))
	return options
}

func TestACarriedDealReceiptReachesTheSettlementPage(t *testing.T) {
	receipt := mustRead(t, dealReceipt)
	// The bundle is written as an object in the file; the page carries its
	// text as written.
	var object json.RawMessage = receipt
	page, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "receipt": object}}))
	require.NoError(t, err)
	receipts, ok := pageOptions(t, page)["receipts"].(map[string]any)
	require.True(t, ok)
	require.Len(t, receipts, 1)
	var carried, want map[string]any
	require.NoError(t, json.Unmarshal([]byte(receipts["d1"].(string)), &carried))
	require.NoError(t, json.Unmarshal(receipt, &want))
	assert.Equal(t, want, carried, "d1 carries the receipt bundle")
	// The deal view module is in the page, its stylesheet in the CSP.
	assert.Contains(t, string(page), "capsulectl.deal-view/v0")
	style := sha256.Sum256([]byte(dealViewCSS(t)))
	assert.Contains(t, string(page), "'sha256-"+base64.StdEncoding.EncodeToString(style[:])+"'")

	// The same receipt as a JSON string holding its text is the same page.
	text, err := json.Marshal(string(receipt))
	require.NoError(t, err)
	var asString json.RawMessage = text
	again, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "receipt": asString}}))
	require.NoError(t, err)
	assert.Equal(t, string(receipts["d1"].(string)), pageOptions(t, again)["receipts"].(map[string]any)["d1"])
}

// A receipt whose deal section does not verify is refused: capsule-cli's
// page gate no longer reads deal sections (rc50, #609), so the deal plugin's
// verify does (evidencebook#735). Here one line of the sealed report is
// edited; the page gate still passes, and deal verify calls it INVALID.
func TestAReceiptWhoseDealSectionFailsIsRefused(t *testing.T) {
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, dealReceipt), &bundle))
	ext, _ := bundle["extensions"].(map[string]any)
	carrier, _ := ext["x-deal-report/v0"].(map[string]any)
	input, _ := carrier["agent_input"].(map[string]any)
	report, _ := input["report"].(map[string]any)
	require.NotNil(t, report, "the fixture carries a sealed standalone report")
	report["did_line"] = "A line nobody sealed."
	_, out, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "receipt": bundle}}))
	require.Error(t, err)
	assert.Contains(t, out+err.Error(), "deal verify --bundle calls it INVALID")
	assert.Contains(t, out+err.Error(), "sealed_report_unverified")
}

// Without the deal plugin's verify (here a capsulectl that does not exist),
// a carried receipt is refused and the refusal says --receipts needs the
// deal plugin.
func TestAReceiptIsRefusedWhenItsDealVerifyCannotRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CAPSULECTL_BIN", filepath.Join(installedCapsulectl(t), "capsulectl"))
	err := dealVerify(mustRead(t, dealReceipt))
	require.NoError(t, err, "the real deal verify, through CAPSULECTL_BIN, calls the receipt VALID")
	t.Setenv("CAPSULECTL_BIN", filepath.Join(t.TempDir(), "no-capsulectl"))
	err = dealVerify(mustRead(t, dealReceipt))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--receipts needs the deal plugin installed")
}

func TestAHeldReceiptAndAnUncitedOneLeaveTheRestAlone(t *testing.T) {
	page, output, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{
		{"exchange_capsule_id": settlementExchange(t), "held": true},
		{"exchange_capsule_id": "an-exchange-outside-the-window", "held": true},
	}))
	require.NoError(t, err)
	receipts := pageOptions(t, page)["receipts"].(map[string]any)
	held, present := receipts["d1"]
	assert.True(t, present)
	assert.Nil(t, held, "held by policy is a null entry")
	assert.Len(t, receipts, 1, "the uncited receipt is not on the page")
	assert.NotContains(t, string(page), "an-exchange-outside-the-window")
	assert.Equal(t, []string{"an-exchange-outside-the-window"}, ignoredReceipts(t, output), "the build output names the receipt that reached no deal")
}

func TestANotClosedEntryIsAnEntryAndOnlyOneStateAnEntry(t *testing.T) {
	page, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "not_closed": true}}))
	require.NoError(t, err)
	receipts := pageOptions(t, page)["receipts"].(map[string]any)
	_, present := receipts["d1"]
	assert.True(t, present, "a deal not closed is an entry, never a missing one")

	refused := func(entry map[string]any, want string) {
		t.Helper()
		_, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{entry}))
		require.Error(t, err)
		assert.ErrorContains(t, err, want)
	}
	refused(map[string]any{"exchange_capsule_id": settlementExchange(t), "not_closed": true, "held": true}, "exactly one of them")
	refused(map[string]any{"exchange_capsule_id": settlementExchange(t), "not_closed": true, "receipt": json.RawMessage(mustRead(t, dealReceipt))}, "exactly one of them")
	refused(map[string]any{"exchange_capsule_id": settlementExchange(t), "notclosed": true}, "is not one of")
}

// ignoredReceipts reads the ignored_receipts the build reports.
func ignoredReceipts(t *testing.T, output string) []string {
	t.Helper()
	var result struct {
		Ignored []string `json:"ignored_receipts"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &result), output)
	return result.Ignored
}

func TestWithoutReceiptsThePageCarriesNone(t *testing.T) {
	page, _, err := buildSettlement(t, "bundle.json")
	require.NoError(t, err)
	assert.Contains(t, string(page), `renderEvidenceGraph(window.__BUNDLE__, document.getElementById("app"))`, "no options: nothing carried by default")
}

func TestReceiptsFlagRefusals(t *testing.T) {
	good := mustRead(t, dealReceipt)
	entry := map[string]any{"exchange_capsule_id": settlementExchange(t), "receipt": json.RawMessage(good)}
	refused := func(bundle, file, want string, extra ...string) {
		t.Helper()
		_, output, err := buildSettlement(t, bundle, append([]string{"--receipts", file}, extra...)...)
		require.Error(t, err)
		assert.Contains(t, output+err.Error(), want)
	}
	text := func(s string) string {
		path := filepath.Join(t.TempDir(), "receipts.json")
		require.NoError(t, os.WriteFile(path, []byte(s), 0o600))
		return path
	}
	refused("bundle.json", text(`not json`), "--receipts must be a JSON list")
	refused("bundle.json", text(`[{"exchange_capsule_id":"x","held":true,"bogus":1}]`), "--receipts must be a JSON list")
	refused("bundle.json", text(`[{"held":true}]`), "has no exchange_capsule_id")
	refused("bundle.json", text(`[] []`), "one JSON list")
	refused("bundle.json", text(`null`), "it is not a list")
	refused("bundle.json", text(`{"exchange_capsule_id":"x","held":true}`), "it is not a list")
	// A field written twice cannot turn a held receipt into a carried one.
	refused("bundle.json", text(`[{"exchange_capsule_id":"`+settlementExchange(t)+`","held":true,"held":false,"receipt":`+string(good)+`}]`), `the key "held" is written twice`)
	refused("bundle.json", text(`[{"exchange_capsule_id":"x","Held":true,"held":false}]`), `the key "Held" is not one of`)
	// Go folds a long s (U+017F) onto s, so these two keys would both fill exchange_capsule_id.
	refused("bundle.json", text(`[{"exchange_capsule_id":"A","exchange_cap\u017fule_id":"B","held":true}]`), "is not one of")
	refused("bundle.json", text(`[{"exchange_capsule_id":"a","exchange_capsule_id":"b","held":true}]`), "is written twice")
	refused("bundle.json", text(`[null]`), "it is not an object")
	refused("bundle.json", receiptsFile(t, []map[string]any{entry, entry}), `deal "d1" has more than one receipt`)
	refused("bundle.json", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t)}}), "exactly one of them")
	refused("shared-bundle.json", receiptsFile(t, []map[string]any{entry}), "more than one deal cite")
	// A bundle that is no deal receipt is refused: the deal view would not draw it.
	refused("bundle.json", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "receipt": json.RawMessage(mustRead(t, filepath.Join(settlementDir, "receipt.json")))}}), "is not one the capsulectl.deal-view/v0 module draws")
	refused("bundle.json", receiptsFile(t, []map[string]any{entry}), "--permalink cannot be used with --receipts", "--permalink")
	refused("bundle.json", receiptsFile(t, []map[string]any{entry}), "--diagnostic cannot be used with --receipts", "--diagnostic")
}

// The deal view's stylesheet, whose digest the page's CSP lists.
func dealViewCSS(t *testing.T) string {
	t.Helper()
	return string(mustRead(t, "../deal/assets/deal-view.css"))
}

// An x-deal-v1 deal's bundle seals no Result, so `book report build` refuses
// it before verifying anything (evidencebook#767: capsulectl pinned a report
// module older than that refusal).
func TestReportBuildRefusesAnXDealV1Bundle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var bundle map[string]any
	require.NoError(t, json.Unmarshal(mustRead(t, filepath.Join(settlementDir, "bundle.json")), &bundle))
	extensions, _ := bundle["extensions"].(map[string]any)
	if extensions == nil {
		extensions = map[string]any{}
	}
	extensions["x-deal-v1"] = map[string]any{"sealed_report": "c"}
	bundle["extensions"] = extensions
	raw, err := json.Marshal(bundle)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "bundle.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	out := filepath.Join(t.TempDir(), "page.html")
	output, err := invoke(t, "", "report", "build", "--bundle", path, "--card", "settlement", "--out", out)
	require.Error(t, err)
	assert.Contains(t, output+err.Error(), "x-deal-v1")
	assert.Contains(t, output+err.Error(), "v1 deal report")
	_, statErr := os.Stat(out)
	assert.True(t, os.IsNotExist(statErr), "no page was written")
}

// clickedReceiptText renders page in headless Chrome after clicking the first
// "Open receipt" control, and returns the document's visible text. The click is
// a test-only script; with EVIDENCEBOOK_CHROME unset the test is skipped.
func clickedReceiptText(t *testing.T, page []byte) string {
	t.Helper()
	chrome := os.Getenv("EVIDENCEBOOK_CHROME")
	if chrome == "" {
		t.Skip("EVIDENCEBOOK_CHROME is not set; the receipt control is drawn and opened by the page's runtime in a browser")
	}
	click := `setTimeout(function(){var b=document.querySelector('[data-receipt="open"]');if(b)b.click();},3000);`
	// Every claim is listed (depth L2), and the click is added: both change
	// the page's scripts, whose hashes the content security policy pins, so
	// the policy is dropped from this render-only copy, as the reason test does.
	require.Contains(t, string(page), `{"audience":"*"`)
	html := strings.Replace(string(page), `{"audience":"*"`, `{"audience":"*","depth":"L2"`, 1)
	html = regexp.MustCompile(`<meta http-equiv="Content-Security-Policy"[^>]*>`).ReplaceAllString(html, "")
	html = strings.Replace(html, "</body>", "<script>"+click+"</script></body>", 1)
	dir := t.TempDir()
	file := filepath.Join(dir, "page.html")
	require.NoError(t, os.WriteFile(file, []byte(html), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-first-run", "--no-sandbox",
		"--use-mock-keychain", "--password-store=basic", "--user-data-dir="+filepath.Join(dir, "profile"), "--disable-dev-shm-usage", "--window-size=1280,1000", "--virtual-time-budget=30000",
		"--dump-dom", "file://"+file)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil && !strings.HasSuffix(strings.TrimSpace(string(out)), "</html>") {
		require.NoError(t, err, "headless Chrome")
	}
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(regexp.MustCompile(`(?s)<(script|style)\b.*?</(script|style)>`).ReplaceAllString(string(out), ""), "\n")
	return strings.Join(strings.Fields(stdhtml.UnescapeString(text)), " ")
}

// The real deal view, in a real browser, opens the carried receipt under the
// claim of its deal and verifies it, and a receipt the share policy holds
// reads as held: Sherlock2's settled sentence, not "no record".
func TestTheBrowserOpensACarriedReceiptAndReadsAHeldOne(t *testing.T) {
	carried, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "receipt": json.RawMessage(mustRead(t, dealReceipt))}}))
	require.NoError(t, err)
	text := clickedReceiptText(t, carried)
	assert.Contains(t, text, "Close receipt", "the control opened")
	assert.Contains(t, text, "Receipt · deal-134fae85882ad8bf", "the deal view drew the carried receipt")
	assert.Contains(t, text, "Your copy only · not fully checked", "at the strength the receipt has: the other side's record is not in it")
	assert.Contains(t, text, "10 of 10 checks passed · 6 top-level records", "the page verified the receipt bundle it carries")
	assert.NotContains(t, text, "Receipt not carried with this page")

	held, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "held": true}}))
	require.NoError(t, err)
	assert.Contains(t, clickedReceiptText(t, held), "The holder's disclosure policy keeps this receipt off this page.")

	notClosed, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{{"exchange_capsule_id": settlementExchange(t), "not_closed": true}}))
	require.NoError(t, err)
	closedText := clickedReceiptText(t, notClosed)
	assert.Contains(t, closedText, "No receipt: this deal had not closed when this page was built.", "Sherlock2's locked line (capsule-viewer#358), not the built-wrong one")
	assert.NotContains(t, closedText, "Receipt not carried with this page")

	none, _, err := buildSettlement(t, "bundle.json", "--receipts", receiptsFile(t, []map[string]any{}))
	require.NoError(t, err)
	assert.Contains(t, clickedReceiptText(t, none), "Receipt not carried with this page.")
}
