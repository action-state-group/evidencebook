#!/usr/bin/env bash
# check-deal-pages.sh -- a deal receipt's page, opened in headless Chrome, says
# what a reader must see: its sealed text rendered and checked; an edited
# line, or the report dropped for inline text, refused; and the verdicts
# agreeing with each: `capsulectl verify --bundle` VALID (exit 0) on the
# receipt, and `capsulectl deal verify --bundle` INVALID (exit 1) on the
# edited and the downgraded copy (see the deal-receipt-edited case for why
# the plugin's verb). The deal is the synthetic retail-checkout demo. Until the plugins
# left capsule-cli (capsule-cli#326) this was the second half of its
# scripts/check-html-render.sh; the page-check helpers are still capsule-cli's
# (scripts/lib/page-checks.sh), read from the module this plugin builds
# against.
#
#   scripts/check-deal-pages.sh            # uses CAPSULECTL if set, else builds
#
# Needs bash, awk, jq, perl and Chrome or Chromium (CHROME, or google-chrome,
# chromium, chromium-browser, or macOS's Google Chrome). Touches nothing
# outside its own temp directory.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# The x-deal-v0 profile's example materiality predicate, as EvidenceBook
# ships it (dealprofile/x-deal-v0) at the version this module requires;
# go mod download extracts the module when the cache is cold.
neutral="$(cd "$root" && go mod download -json github.com/action-state-group/evidencebook | jq -er .Dir)/dealprofile/x-deal-v0/materiality-predicate/neutral.json"
# go mod download extracts the module when the cache is cold (go list -m
# alone would print an empty directory).
core="$(cd "$root" && go mod download -json github.com/action-state-group/capsule-cli | jq -er .Dir)"
[[ -d "$core" ]] || { echo "the capsule-cli module this plugin builds against could not be found" >&2; exit 2; }
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export XDG_CONFIG_HOME="$work/config"
# shellcheck source=/dev/null
source "$core/scripts/lib/page-checks.sh"

# The reasons the page names for the checks it did not run: the parenthesised
# codes of its banner detail. A page whose records and checkpoint verify runs
# every check (capsule-viewer 0.4.0: the browser verifies the checkpoint
# signature), so it names none; a regression that skips a check would put its
# code here.
unrun_reasons() { grep -o '<p class="cv-banner-detail" data-banner-detail="">[^<]*</p>' "$1" | grep -o '([a-z_][a-z_]*)' | tr '\n' ' '; }
no_check_unrun() { [[ -z "$(unrun_reasons "$1")" ]]; }
find_chrome || { echo "check-deal-pages: no Chrome or Chromium found (set CHROME)" >&2; exit 2; }

mkdir -p "$work/bin"
if [[ -n "${CAPSULECTL:-}" ]]; then
  ln -s "$CAPSULECTL" "$work/bin/capsulectl"
else
  (cd "$root" && go build -o "$work/bin/capsulectl" github.com/action-state-group/capsule-cli/cmd/capsulectl)
fi
export PATH="$work/bin:$PATH"
if [[ -z "${CAPSULECTL_PLUGIN_ROOTS:-}" ]]; then
  mkdir -m 0700 "$work/plugins"
  (cd "$root" && go build -o "$work/plugins/capsulectl-deal" ./cmd/capsulectl-deal)
  export CAPSULECTL_PLUGIN_ROOTS="$work/plugins"
fi
cd "$work"

# A deal receipt: its deal text (summary lines, step lines, amounts) is
# sealed as a standalone signed report the copy carries in x-deal-report/v0
# (capsule-cli rc49 cli/deal_sealed_report.go). The page renders it and says
# it checked it; a copy with one sealed line edited, or its report stripped to
# inline text, is refused and shows none of it, and deal verify --bundle calls
# it INVALID. The deal is the synthetic retail-checkout demo. The page's refusal
# of a downgraded receipt sealed on the deal's log, as capsulectl wrote before
# capsule-cli#294, is capsule-viewer's to check (ts/test/deal-standalone-report.test.ts).
demo="$root/skills/deal/demo/retail-checkout"
mkdir deal-receipt
(
  cd deal-receipt
  export XDG_CONFIG_HOME="$work/deal-receipt/config" CAPSULE_DEAL_CHECK_URL=
  capsulectl deal init --profile deal --dir ./deal --no-witness --materiality "$neutral" >/dev/null
  # x-deal-v1 is the default; this script walks an x-deal-v0 deal (evidencebook#626, #656).
  capsulectl --profile deal deal set-version x-deal-v0 >/dev/null
  id=$(capsulectl --profile deal deal open --input "$demo/open.json" | jq -r .deal_id)
  capsulectl --profile deal deal check --deal "$id" --input "$demo/check-pay.json" >/dev/null
  capsulectl --profile deal deal note --deal "$id" --kind act --input "$demo/act-pay.json" >/dev/null
  capsulectl --profile deal deal report --deal "$id" --html receipt.html >/dev/null
) >deal-receipt/log 2>&1 || { echo "FAIL deal-receipt: the demo deal's receipt was written" >&2; cat deal-receipt/log >&2; exit 1; }
cp deal-receipt/receipt.html receipt.html
render receipt.html
check "deal-receipt: no console.error, uncaught error or unhandled rejection" no_render_errors receipt.html.dom
# capsule-viewer's x-deal-v0 receipt module draws the page (evidencebook#869).
check "deal-receipt: the page draws the receipt with capsule-viewer's module" contains receipt.html.dom 'class="cv-kit" data-receipt-module="capsuleviewer.receipt.unilateral/v0">'
check "deal-receipt: the page says it checked the sealed report itself" contains receipt.html.dom 'data-sealed="x-deal-report"'
capsulectl verify --bundle receipt.html >receipt-verify.json && verdict=0 || verdict=$?
check "deal-receipt: verify --bundle: VALID, exit 0" test "$verdict" -eq 0
page_bundle receipt.html >receipt.json
did_line=$(jq -r '.extensions["x-deal-report/v0"].agent_input.report.did_line' receipt.json)
check "deal-receipt: the page renders its sealed summary line" contains receipt.html.dom "<p class=\"cv-muted\" data-sealed-text=\"did-line\">$did_line</p>"

# deal-receipt-rebuilt: the receipt's bundle built into a page by the host
# capsulectl's own presentation build, so the page and its runtime are the
# host's, not the ones this plugin embeds. Against capsule-cli main
# (core-main.yml) this is main rendering what the newest deal build produces.
# It renders as an evidence page that verified (the browser checks the
# checkpoint signature, capsule-viewer 0.4.0, so no check is left unrun); the
# deal section itself comes only from this plugin's `deal report`.
capsulectl presentation build --bundle receipt.json --out receipt-rebuilt.html >/dev/null
render receipt-rebuilt.html
check "deal-receipt-rebuilt: no console.error, uncaught error or unhandled rejection" no_render_errors receipt-rebuilt.html.dom
check "deal-receipt-rebuilt: the page says the bundle verified" contains receipt-rebuilt.html.dom 'class="cv-pill" data-verify="verified"'
check "deal-receipt-rebuilt: the page ran every check" no_check_unrun receipt-rebuilt.html.dom
# The core's page verbs read no deal section on capsule-cli rc50 (#609), so
# the page must say so on its face: each deal kind is listed as covered by
# the file's fingerprint and not interpreted by the page's checks (Sherlock2's
# condition for evidencebook#809). A regression here would show a failing
# deal as a clean page.
for kind in x-deal-v0 x-deal-report/v0; do
  check "deal-receipt-rebuilt: the page lists $kind as not interpreted" contains receipt-rebuilt.html.dom "data-extension-id=\"$kind\" data-integrity=\"covered\" data-semantics=\"uninterpreted\""
done

# deal-receipt-edited: one line of the signed report edited. The bundle's own
# records still verify, but the page's own check of the standalone report
# fails, so the page refuses the copy before any receipt is drawn
# (capsule-viewer#415): the refused banner, and the condition named under
# "Failed on this page".
jq -c '.extensions["x-deal-report/v0"].agent_input.report.did_line = "A line nobody sealed."' receipt.json >receipt-edited.json
# The bundle is data in a JSON element (capsule-viewer 0.4.0), so the edit
# needs no change to the page's CSP, which pins only executable scripts.
replace_bundle receipt.html receipt-edited.json >receipt-edited.html
check "deal-receipt-edited: the page carries the edited line" contains receipt-edited.html 'A line nobody sealed.'
render receipt-edited.html
check "deal-receipt-edited: the page refuses the copy" contains receipt-edited.html.dom 'data-verify="failed" data-verdict="invalid"'
check "deal-receipt-edited: the page names the report that does not match" contains receipt-edited.html.dom "data-check=\"deal-report-capsule\">Didn't verify: the deal report carried outside the log does not match its Capsule."
# The receipt module draws into <div class="cv-kit" data-receipt-module=...>: the valid
# receipt has it, the refused one must not.
check "deal-receipt-edited: no receipt is drawn" lacks receipt-edited.html.dom 'class="cv-kit" data-receipt-module='
check "deal-receipt-edited: the page shows no edited line" lacks receipt-edited.html.dom '>A line nobody sealed.</p>'
check "deal-receipt-edited: no \"not checked\" label" lacks receipt-edited.html.dom 'data-unchecked="x-deal-v0"'
# The deal-format verdicts come from the plugin (`capsulectl deal verify
# --bundle`), whose own reader holds the deal checks (evidencebook#735): the
# core no longer checks a deal's report (capsule-cli#609).
capsulectl deal verify --bundle receipt-edited.html >receipt-edited-verify.json 2>/dev/null && verdict=0 || verdict=$?
check "deal-receipt-edited: deal verify --bundle: INVALID, exit 1" test "$verdict" -eq 1
check "deal-receipt-edited: deal verify --bundle names the report that does not verify" contains receipt-edited-verify.json 'sealed_report_unverified'

# deal-receipt-downgraded: the standalone report dropped and edited text put
# inline in x-deal-v0, as a receipt from before reports were sealed carries
# it. The receipt module draws, in place of the receipt, that its text could
# not be checked and what deal verify --bundle says of it (capsule-viewer#453),
# never the inline text and never under a "not checked" label; deal verify
# --bundle calls it INVALID: the copy names no sealed report.
jq -c '.extensions["x-deal-report/v0"].agent_input.report as $text
  | .extensions["x-deal-v0"] = ($text | .did_line = "A line nobody sealed.") | del(.extensions["x-deal-report/v0"])' receipt.json >receipt-downgraded.json
replace_bundle receipt.html receipt-downgraded.json >receipt-downgraded.html
check "deal-receipt-downgraded: the page carries the inline line" contains receipt-downgraded.html 'A line nobody sealed.'
render receipt-downgraded.html
check "deal-receipt-downgraded: the page says the text could not be checked" contains receipt-downgraded.html.dom '<div class="cv-tone--caution" data-cannot-check="true" data-cannot-check-reason="downgraded">'
check "deal-receipt-downgraded: and why, in that block" contains receipt-downgraded.html.dom "data-cannot-check-reason=\"downgraded\"><p>⚠️ The receipt's text is not shown: this copy names no sealed report, so its text could not be checked.</p><p>"
check "deal-receipt-downgraded: and what deal verify says of it, the command as code" contains receipt-downgraded.html.dom '<p><code class="cv-mono">capsulectl deal verify --bundle &lt;file&gt;</code> reports this copy as INVALID (sealed_report_not_named).</p></div>'
check "deal-receipt-downgraded: the page shows no inline line" lacks receipt-downgraded.html.dom '>A line nobody sealed.</p>'
check "deal-receipt-downgraded: no \"not checked\" label" lacks receipt-downgraded.html.dom 'data-unchecked="x-deal-v0"'
capsulectl deal verify --bundle receipt-downgraded.html >receipt-downgraded-verify.json 2>/dev/null && verdict=0 || verdict=$?
check "deal-receipt-downgraded: deal verify --bundle: INVALID, exit 1" test "$verdict" -eq 1
check "deal-receipt-downgraded: deal verify --bundle names the missing sealed report" contains receipt-downgraded-verify.json 'sealed_report_not_named'

exit "$fail"
