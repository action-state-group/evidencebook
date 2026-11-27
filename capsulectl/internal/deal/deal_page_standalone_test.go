package deal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifiedContextJS is a node driver's verifiedContext(context, EG, bundle):
// the verified context the page's runtime builds, the one a presentation
// module's buildModel receives. It goes through renderEvidenceGraph, the
// page's own entry point (with its no-SHA-256 gate), rather than the exported
// buildVerifiedBundleContext: renderEvidenceGraph hands the context to the
// registry it is given before it renders anything but its checking bar, and
// this registry keeps it and stops the render there.
const verifiedContextJS = `// inertElement is a DOM node that takes every call and draws nothing: the
// runtime draws its frame (header, checking bar) before the registry is asked.
function inertElement() {
  const lists = new Set(["childNodes", "children"]);
  const plain = new Set(["dataset", "style"]);
  const handler = {
    get(target, prop) {
      if (prop === "then" || typeof prop === "symbol") return undefined;
      if (lists.has(prop)) return [];
      if (plain.has(prop)) return (target[prop] ??= {});
      if (prop === "textContent" || prop === "innerHTML") return "";
      if (prop === "querySelectorAll" || prop.startsWith("getElementsBy")) return () => [];
      // No tree around it: a walk up or along ends at once.
      if (/^(parent|first|last|next|previous|closest$|querySelector$)/.test(prop)) return typeof target[prop] === "function" ? target[prop] : (prop === "closest" || prop === "querySelector" ? () => null : null);
      if (prop in target) return target[prop];
      return inertElement();
    },
    set(target, prop, value) { target[prop] = value; return true; },
    apply() { return inertElement(); },
  };
  return new Proxy(function () {}, handler);
}
async function verifiedContext(context, EG, bundle) {
  if (vm.runInContext("typeof document", context) === "undefined")
    context.document = inertElement();
  const stop = new Error("context captured");
  let captured;
  const registry = { resolve(ctx) { captured = ctx; throw stop; } };
  try {
    await EG.renderEvidenceGraph(bundle, inertElement(), undefined, { registry });
  } catch (err) {
    if (err !== stop) throw err;
  }
  // undefined: the page refused the copy before asking any module
  // (capsule-viewer#415: its own standalone report check failed).
  return captured;
}
`

// dealPageStandaloneDriver runs a deal page's own runtime and deal view
// under node, with node's WebCrypto, after an optional edit to its bundle,
// and prints whether the view refused the report or showed it.
const dealPageStandaloneDriver = `
const fs = require("fs");
const vm = require("vm");
const { webcrypto } = require("crypto");
const [page, edit] = process.argv.slice(2);
// "no-crypto" runs the page as a plain-http or file: page sees it: no crypto global.
const html = fs.readFileSync(page, "utf8");
const scripts = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
const bundleJSON = scripts.find((s) => html.slice(0, html.indexOf(s)).endsWith('<script type="application/json">'));
const bundleScript = "window.__BUNDLE__ = JSON.parse(" + JSON.stringify(bundleJSON) + ");";
const bundleParser = scripts.find((s) => s.startsWith("window.__BUNDLE__ = JSON.parse("));
const runtime = scripts.find((s) => s.includes("var EvidenceGraph = "));
const bootstrap = scripts.find((s) => s.includes('document.getElementById("app")'));
const modules = scripts.filter((s) => s !== bundleJSON && s !== bundleParser && s !== runtime && s !== bootstrap);
const sandbox = { console, TextEncoder, TextDecoder, atob, btoa, URL, setTimeout, clearTimeout, queueMicrotask };
if (edit !== "no-crypto") sandbox.crypto = webcrypto;
const context = vm.createContext(sandbox);
vm.runInContext("globalThis.window = globalThis;", context);
vm.runInContext(bundleScript, context);
vm.runInContext(runtime, context);
for (const source of modules) vm.runInContext(source, context);
// The bootstrap's first statement sets the page's data, its words included.
vm.runInContext(bootstrap.slice(0, bootstrap.indexOf("\nrenderEvidenceGraph(")), context);
const EG = vm.runInContext("EvidenceGraph", context);
` + verifiedContextJS + `const bundle = vm.runInContext("window.__BUNDLE__", context);
const x = (bundle.extensions || {})["x-deal-report/v0"] || {};
const flip = (h) => h.slice(0, -2) + (h.endsWith("00") ? "01" : "00");
if (edit === "step-key") {
  const step = bundle.records.find((r) => r.evidence_record_header && r.evidence_record_header.record_type === "deal_step");
  step.key_id = x.record.key_id === step.key_id ? flip(step.key_id) : x.record.key_id;
}
if (edit === "report-signature") x.record.signature = flip(x.record.signature);
if (edit === "report-text") x.agent_input.report.scope = "edited";
if (edit === "downgraded-in-log") {
  const id = bundle.extensions["x-deal-v0"].sealed_report;
  bundle.extensions["x-deal-v0"] = Object.assign({}, bundle.disclosures[id].agent_input.report, { did_line: "A line nobody sealed." });
  delete bundle.disclosures[id];
}
if (edit === "v1-only") {
  bundle.extensions["x-deal-v1"] = { sealed_report: x.record.capsule_id };
  delete bundle.extensions["x-deal-v0"];
}
if (edit === "stripped") {
  bundle.extensions["x-deal-v0"] = x.agent_input.report;
  delete bundle.extensions["x-deal-report/v0"];
}
(async () => {
  const ctx = await verifiedContext(context, EG, bundle);
  if (ctx === undefined) {
    process.stdout.write(JSON.stringify({ presentation: "refused by the page", refusal: "refused by the page before any module" }));
    return;
  }
  const resolution = await EG.resolvePresentation(ctx);
  const v = ctx.verification;
  const out = {
    crypto: vm.runInContext("typeof crypto", context),
    ed25519: String(EG.ed25519Available()),
    checks: [v.graphClosure.status, v.intervalCoverage.status, v.perRecordMembership.status].join(","),
    records: Object.values(v.capsuleResults).every((r) => r.ok) ? "ok" : "failed",
    findings: Object.values(v.capsuleResults).flatMap((r) => r.findings.map((f) => f.code)).filter((c) => c !== "chain_check_store_level").join(","),
    disclosures: v.disclosures.every((d) => d.status === "disclosure_match" || d.status === "withheld") ? "ok" : "failed",
    presentation: resolution.kind,
  };
  if (resolution.kind === "module") {
    out.module = String((resolution.module && resolution.module.manifest && resolution.module.manifest.id) || "");
    const view = await resolution.module.buildModel(ctx);
    out.refusal = (view && view.refusal) || "";
    out.scope = view && view.report ? view.report.scope : "";
  }
  process.stdout.write(JSON.stringify(out));
})();
`

// The page's own check of a copy's standalone report (a step key that is not
// the one its envelope verified under, a report signature or text that does
// not verify, a copy stripped to inline text, a downgraded in-log receipt)
// is capsule-viewer's, tested there (ts/test/deal-standalone-report.test.ts),
// as is a page opened without WebCrypto or its Ed25519
// (ts/test/no-webcrypto.test.ts), since capsule-viewer's x-deal-v0 module draws capsulectl's pages (#869).

// dealPageUnderNode writes a retail deal's counterparty page and returns a
// runner that loads it under node after the named edit.
func dealPageUnderNode(t *testing.T) func(edit string) map[string]string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dealFixture(t)
	id := retailDeal(t)
	dir := t.TempDir()
	page := filepath.Join(dir, "shared.html")
	dealRun(t, "report", "--deal", id, "--share", cli.DealAudienceCounterparty, "--to", "the shop", "--html", page)
	driver := filepath.Join(dir, "driver.js")
	require.NoError(t, os.WriteFile(driver, []byte(dealPageStandaloneDriver), 0o600))
	return func(edit string) map[string]string {
		out, err := exec.Command(node, driver, page, edit).CombinedOutput()
		require.NoError(t, err, string(out))
		var got map[string]string
		require.NoError(t, json.Unmarshal(out, &got), string(out))
		return got
	}
}

// An x-deal-v1 deal has no lifecycle (evidencebook#461), and deal-view.js
// reads one. The page never meets a v1 file: the module's manifest requires
// the x-deal-v0 extension, which a v1 bundle does not carry (its extension is
// x-deal-v1), so the runtime selects another module and the receipt page (not the composed
// module, which needs composed/v1) is not run (evidencebook-deal#214). capsule-viewer#322 shows a v1 file.
func TestAV1OnlyBundleDoesNotSelectTheDealPage(t *testing.T) {
	run := dealPageUnderNode(t)
	assert.Equal(t, "capsuleviewer.receipt.unilateral/v0", run("")["module"], "control: the v0 file selects the viewer's x-deal-v0 module")
	got := run("v1-only")
	assert.NotEqual(t, "capsuleviewer.receipt.unilateral/v0", got["module"], "got %v", got)
}
