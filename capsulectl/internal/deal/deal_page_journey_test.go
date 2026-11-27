package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dealPageJourneyDriver renders a deal page's deal view under node, with
// node's WebCrypto and a DOM just large enough for it, and prints the label
// of each "How it went" row, in order, and the section's heading if shown.
const dealPageJourneyDriver = `
const fs = require("fs");
const vm = require("vm");
const { webcrypto } = require("crypto");
let [pagePath, edit] = process.argv.slice(2);
const html = fs.readFileSync(pagePath, "utf8");
const scripts = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
const bundleJSON = scripts.find((s) => html.slice(0, html.indexOf(s)).endsWith('<script type="application/json">'));
const bundleScript = "window.__BUNDLE__ = JSON.parse(" + JSON.stringify(bundleJSON) + ");";
const bundleParser = scripts.find((s) => s.startsWith("window.__BUNDLE__ = JSON.parse("));
const runtime = scripts.find((s) => s.includes("var EvidenceGraph = "));
const bootstrap = scripts.find((s) => s.includes('document.getElementById("app")'));
const modules = scripts.filter((s) => s !== bundleJSON && s !== bundleParser && s !== runtime && s !== bootstrap);
class Node {
  constructor(tag) { this.tag = tag; this.children = []; this.dataset = {}; this.className = ""; this._text = ""; }
  append(...nodes) { this.children.push(...nodes.map((n) => (typeof n === "string" ? Object.assign(new Node("#text"), { _text: n }) : n))); }
  set textContent(t) { this._text = t; this.children = []; }
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(""); }
  all(pred, out = []) { if (pred(this)) out.push(this); this.children.forEach((c) => c.all(pred, out)); return out; }
}
const document = { head: new Node("head"), createElement: (tag) => new Node(tag), getElementById: () => null };
const sandbox = { console, TextEncoder, TextDecoder, atob, btoa, URL, setTimeout, clearTimeout, queueMicrotask, crypto: webcrypto, document };
const context = vm.createContext(sandbox);
vm.runInContext("globalThis.window = globalThis;", context);
vm.runInContext(bundleScript, context);
vm.runInContext(runtime, context);
for (const source of modules) vm.runInContext(source, context);
// The bootstrap's first statement sets the page's data; the rest renders.
vm.runInContext(bootstrap.slice(0, bootstrap.indexOf("\nrenderEvidenceGraph(")), context);
const EG = vm.runInContext("EvidenceGraph", context);
` + verifiedContextJS + `const bundle = vm.runInContext("window.__BUNDLE__", context);
const words = vm.runInContext("globalThis.capsulectlDealPage.words", context) || {};
// "v1:<edit>": the bundle carries a parent-log-witness/v1 block (a stand-in:
// the page reads only its presence, the runtime's answer is the check), then
// <edit> as below (evidencebook#737).
if (edit && edit.startsWith("v1:")) {
  edit = edit.slice(3);
  bundle.extensions["parent-log-witness/v1"] = {};
}
// "withhold": the switch step's disclosure is dropped from the file.
if (edit === "withhold") {
  const report = bundle.extensions["x-deal-report/v0"].agent_input.report;
  const step = report.journey.find((m) => m.move === "switched").step;
  delete bundle.disclosures[step];
}
// "withhold-approval": each approval's disclosure is dropped from the file.
if (edit === "withhold-approval") {
  for (const [id, d] of Object.entries(bundle.disclosures)) {
    if ((((d || {}).agent_input || {})["x-deal-v0"] || {}).record_type === "approval") delete bundle.disclosures[id];
  }
}
// "witness:S1,S2": the runtime's cadence check answers S1 for the first context
// it is asked about, S2 for the next (the last repeats); "preimage:S": its
// preimage check answers S for every leg; "no-checks": the runtime has neither
// (a page built on a viewer before 0.10.0). The checks themselves are the
// viewer's to test (capsule-viewer#214); the page's draw of each answer is ours.
// The runtime object is frozen: the module reads the global, so the global is
// replaced by a copy with the stubbed checks.
const patched = { ...EG };
if (edit && edit.startsWith("witness:")) {
  const answers = edit.slice(8).split(",");
  let asked = 0;
  patched.readerCadenceWitness = async () => {
    const status = answers[Math.min(asked++, answers.length - 1)];
    return status === "PASS"
      ? { status, finding: null, witness: "Acme Witness", witnessed_time: 1790000000 }
      : { status, finding: status === "NOT_PRESENT" ? null : "stub", witness: null, witnessed_time: null };
  };
}
if (edit && edit.startsWith("preimage:")) patched.paymentPreimageCheck = async () => edit.slice(9);
if (edit === "no-checks") {
  delete patched.readerCadenceWitness;
  delete patched.paymentPreimageCheck;
}
context.EvidenceGraph = patched;
(async () => {
  const ctx = await verifiedContext(context, EG, bundle);
  const resolution = await EG.resolvePresentation(ctx);
  const model = await resolution.module.buildModel(ctx);
  const L0 = new Node("div");
  resolution.module.render(model, { L0 });
  const rows = L0.all((n) => n.dataset.journey !== undefined);
  const banner = L0.all((n) => n.dataset.checked !== undefined)[0];
  process.stdout.write(JSON.stringify({
    banner: banner ? banner.dataset.checked : "",
    bannerText: banner ? banner.children[0].textContent : "",
    stamps: L0.all((n) => n.dataset.stamp !== undefined).map((n) => n.dataset.stamp),
    stampText: L0.all((n) => n.dataset.stamp !== undefined).map((n) => n.textContent),
    close: (L0.all((n) => n.dataset.close !== undefined)[0] || { dataset: {} }).dataset.close || "",
    pill: (L0.all((n) => /\bdeal-pill\b/.test(n.className))[0] || { textContent: "" }).textContent,
    restsNote: L0.all((n) => n.dataset.sealed !== undefined || n.dataset.unchecked !== undefined).length,
    refusal: model.refusal || "",
    pageText: L0.textContent,
    shown: rows.length > 0,
    labels: rows.map((n) => n.dataset.journey),
    rows: rows.map((n) => n.textContent),
    words: Object.keys(words).filter((k) => k.startsWith("journey.") || k.startsWith("class.")),
  }));
})();
`

// renderJourney writes the deal's page (a shared copy when share is set) and
// renders it under node.
func renderJourney(t *testing.T, id string, share bool, edit ...string) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	args := []string{"report", "--deal", id, "--html", page}
	if share {
		args = append(args, "--share", cli.DealAudienceCounterparty, "--to", "the shop")
	}
	dealRun(t, args...)
	return renderPageUnderNode(t, node, page, edit...)
}

// renderPageUnderNode renders a deal page's deal view (any page the plugin
// writes) under node with the journey driver, after the driver's edit.
func renderPageUnderNode(t *testing.T, node, page string, edit ...string) map[string]any {
	t.Helper()
	driver := filepath.Join(t.TempDir(), "driver.js")
	require.NoError(t, os.WriteFile(driver, []byte(dealPageJourneyDriver), 0o600))
	out, err := exec.Command(node, append([]string{driver, page}, edit...)...).CombinedOutput()
	require.NoError(t, err, string(out))
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got), string(out))
	return got
}

// The sealed report's journey is facts only: no word from a wording pack,
// so the same bundle words alike under any pack.
func TestTheSealedJourneyHoldsNoWords(t *testing.T) {
	dealFixture(t)
	id, _, _ := fawnDeal(t)
	path := filepath.Join(t.TempDir(), "b.json")
	dealRun(t, "report", "--deal", id, "--bundle", path)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var b map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &b))
	report, _, _ := DealSealedReport(b)
	journey := report["journey"].([]interface{})
	require.Len(t, journey, 13)
	for _, m := range journey {
		assert.NotContains(t, m, "label")
	}
	couldnt := journey[4].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"who": "AliExpress", "reason": "sign_in_required"}, couldnt["facts"])
	assert.Equal(t, "journey.agent.couldnt.who", couldnt["key"])
	told := journey[9].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"who": "eBay", "classes": []interface{}{"name", "address"}}, told["facts"])
	// The user's OK to share is sealed by its classes, and the chosen item
	// as itself (capsule-cli#410).
	okShare := journey[8].(map[string]interface{})
	assert.Equal(t, "journey.user.approved.share_contact", okShare["key"])
	assert.Equal(t, map[string]interface{}{"classes": []interface{}{"name", "address"}}, okShare["facts"])
	chose := journey[6].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"item": "Fawn Sticker"}, chose["facts"])
}

// pinJourneyPack pins a copy of the built-in pack, edited, as the deal
// profile's wording.
func pinJourneyPack(t *testing.T, edit func(entries map[string]any)) {
	t.Helper()
	doc := fullPackDoc(t)
	edit(doc["entries"].(map[string]any))
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "pack.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	p, err := cli.LoadProfile("deal")
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	p.Wording = cli.ProfileWording{Path: path, SHA256: hex.EncodeToString(sum[:])}
	require.NoError(t, cli.SaveProfile(p, true))
}

// A pinned pack words the report's journey; one without the journey keys is
// not used, and it falls back to the built-in words, as every other line
// does. The page's words are capsule-viewer's own pack whatever the profile
// pins (Sherlock2 dropped the deal view's profile words, line 10,
// capsule-viewer#383 comment 7176241436), so the page is not compared here.
func TestTheReportWordsTheJourneyFromThePinnedPack(t *testing.T) {
	dealFixture(t)
	id, _, _ := fawnDeal(t)
	labels := func() []any {
		var out []any
		for _, s := range dealRun(t, "report", "--deal", id)["journey"].([]any) {
			out = append(out, s.(map[string]any)["label"])
		}
		return out
	}
	pinJourneyPack(t, func(e map[string]any) {
		// A class or reason line that says nothing falls back as the
		// report's does: the class's own word, an empty reason.
		e["class.name"] = "{nothing}"
		e["journey.reason.sign_in_required"] = "{nothing}"
	})
	want := labels()
	assert.Contains(t, want, "Told eBay your name and address")
	assert.Contains(t, want, "Couldn't (AliExpress )")

	pinJourneyPack(t, func(e map[string]any) {
		e["journey.user.switched"] = "You moved the errand to {who}"
		e["journey.reason.sign_in_required"] = "needed a sign-in"
		e["class.address"] = "street address"
	})
	want = labels()
	assert.Contains(t, want, "You moved the errand to eBay")
	assert.Contains(t, want, "Couldn't (AliExpress needed a sign-in)")
	assert.Contains(t, want, "Told eBay your name and street address")

	pinJourneyPack(t, func(e map[string]any) {
		for k := range e {
			if strings.HasPrefix(k, "journey.") {
				delete(e, k)
			}
		}
	})
	want = labels()
	assert.Contains(t, want, "Switched to eBay", "the built-in words")
}

// An "other" reason is worded by its own detail, on the page as in the
// report (a pinned pack words only the report).
func TestTheJourneyWordsAnOtherReasonByItsDetail(t *testing.T) {
	dealFixture(t)
	id := dealRun(t, "open", "--input", writeJSON(t, fawnErrand))["deal_id"].(string)
	note(t, id, "couldnt", `{"reason":"other","detail":"the checkout page kept reloading"}`)
	var labels []any
	for _, s := range dealRun(t, "report", "--deal", id)["journey"].([]any) {
		labels = append(labels, s.(map[string]any)["label"])
	}
	assert.Contains(t, labels, "Couldn't (the checkout page kept reloading)")
	assertPageJourneyIsTheReports(t, id)

	// A detail is a reason's value, not a placeholder of its own.
	pinJourneyPack(t, func(e map[string]any) { e["journey.agent.couldnt"] = "Couldn't ({detail})" })
	labels = nil
	for _, s := range dealRun(t, "report", "--deal", id)["journey"].([]any) {
		labels = append(labels, s.(map[string]any)["label"])
	}
	assert.Contains(t, labels, "")
}
