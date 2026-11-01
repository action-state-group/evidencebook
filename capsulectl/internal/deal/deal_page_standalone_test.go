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
const verifiedContextJS = `async function verifiedContext(context, EG, bundle) {
  if (vm.runInContext("typeof document", context) === "undefined")
    vm.runInContext("globalThis.document = { createElement: () => ({ dataset: {}, append() {} }) };", context);
  const stop = new Error("context captured");
  let captured;
  const registry = { resolve(ctx) { captured = ctx; throw stop; } };
  try {
    await EG.renderEvidenceGraph(bundle, { childNodes: [], replaceChildren() {} }, undefined, { registry });
  } catch (err) {
    if (err !== stop) throw err;
  }
  if (captured === undefined) throw new Error("the runtime built no verified context");
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
if (edit === "stripped") {
  bundle.extensions["x-deal-v0"] = x.agent_input.report;
  delete bundle.extensions["x-deal-report/v0"];
}
(async () => {
  const ctx = await verifiedContext(context, EG, bundle);
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
    const view = await resolution.module.buildModel(ctx);
    out.refusal = view.refusal || "";
    out.scope = view.report ? view.report.scope : "";
  }
  process.stdout.write(JSON.stringify(out));
})();
`

// The deal page checks a standalone report itself (capsule-cli#294): the
// page as written shows it; a step whose key_id is not the key its envelope
// verified under, a report signature that does not verify, an edited
// report, and a copy stripped to inline text are each refused.
func TestTheDealPageChecksAStandaloneReport(t *testing.T) {
	run := dealPageUnderNode(t)
	shown := run("")
	assert.Empty(t, shown["refusal"], "the page as written shows its report")
	assert.NotEmpty(t, shown["scope"])
	for _, edit := range []string{"step-key", "report-signature", "report-text", "stripped"} {
		got := run(edit)
		assert.NotEmpty(t, got["refusal"], "%s: the deal view refuses it, not shown", edit)
	}
}

// A receipt sealed on the deal's log, as capsulectl wrote before
// capsule-cli#294, with its report's disclosure dropped and edited text put
// inline: the page refuses it rather than show it under the "not checked"
// label. capsulectl no longer writes such a copy, so scripts/check-html-render.sh
// cannot; this keeps the page's in-log path covered.
func TestTheDealPageRefusesADowngradedInLogReceipt(t *testing.T) {
	inLogReports(t)
	run := dealPageUnderNode(t)
	assert.Empty(t, run("")["refusal"], "the page as written shows its report")
	assert.Equal(t, "⚠️ The deal's text could not be checked against its sealed record. Do not rely on it.", run("downgraded-in-log")["refusal"])
}

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
