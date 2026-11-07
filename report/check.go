// SPDX-License-Identifier: Apache-2.0

package report

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/envelope"
	"github.com/action-state-group/capsule-viewer/go/pageruntime"
)

// DiagnosticTitle is the document title of a diagnostic page: what a browser
// tab, a bookmark and a search result show, so the page cannot be taken for a
// verified report from outside it.
const DiagnosticTitle = "DIAGNOSTIC PAGE: not a verified report"

// diagnosticBanner is the notice a diagnostic page opens with, above the
// viewer's own failed-bundle callout. It uses the shell's own banner classes,
// so it needs no style of its own under the page's CSP.
const diagnosticBanner = `<div class="aac-banner" data-state="failed" role="alert"><strong>Diagnostic page.</strong> This page was built from a bundle that did not verify. It is not a verified report: do not rely on it, publish it or send it on as one.</div>`

const appElement = `<div id="app"></div>`

// markDiagnostic puts the diagnostic notice ahead of the viewer's mount point.
func markDiagnostic(html string) (string, error) {
	if strings.Count(html, appElement) != 1 {
		return "", fmt.Errorf("the page shell has no single %s to mark as a diagnostic page", appElement)
	}
	return strings.Replace(html, appElement, diagnosticBanner+appElement, 1), nil
}

// countRecords counts the records the bundle holds and those that verify:
// the record's signature is the producer's own (authored) and its capsule
// verifies, as `verify --bundle` reports them in report.
func countRecords(value, report map[string]any) (total, verified int) {
	signatures, _ := report["record_signatures"].(map[string]string)
	for _, raw := range asSlice(value["records"]) {
		record, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		total++
		if id, _ := record["capsule_id"].(string); id != "" && signatures[id] == envelope.Authored {
			verified++
		}
	}
	return total, verified
}

// ClaimsNotEvaluable is the claims the page shows unsupported or unmet: those
// whose cited evidence is not all in the bundle.
func (p *Page) ClaimsNotEvaluable() int { return p.UnsupportedClaims }

// StatusLine is the one line a build prints beside the page path: counts with
// their denominators, never a percentage, e.g. "787 of 812 records verified ·
// 3 of 12 claims not evaluable". A diagnostic page says so first.
func (p *Page) StatusLine() string {
	parts := []string{
		fmt.Sprintf("%d of %d %s verified", p.RecordsVerified, p.Records, plural(p.Records, "record", "records")),
		fmt.Sprintf("%d of %d %s not evaluable", p.ClaimsNotEvaluable(), p.Claims, plural(p.Claims, "claim", "claims")),
	}
	if p.Diagnostic {
		parts = append([]string{"diagnostic page, not a verified report"}, parts...)
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

var (
	cspPattern      = regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]*)"`)
	scriptPattern   = regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)
	stylePattern    = regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	foreignTag      = regexp.MustCompile(`(?i)<(link|iframe|object|embed|base|form|frame|applet)[\s>/]`)
	urlAttribute    = regexp.MustCompile(`(?i)\s(src|href|action|srcset|poster|data)\s*=\s*["']?\s*([^"'\s>]*)`)
	htmlLangPattern = regexp.MustCompile(`(?i)<html[^>]*\slang="[a-zA-Z-]+"`)
	viewportPattern = regexp.MustCompile(`(?i)<meta name="viewport" content="[^"]*width=device-width`)
	titlePattern    = regexp.MustCompile(`(?s)<title>\s*[^<\s][^<]*</title>`)
)

// checkPage runs the checks every page passes before it is published; a page
// that fails one is not written. Chart-to-data consistency is enforced where
// the charts are filled (chartspec.Check recomputes every statement from the
// sealed dataset and refuses a mismatch), so this checks what that cannot:
//
//   - security: the page's own Content-Security-Policy denies everything but
//     the hashed inline scripts and styles it carries, no script loads from
//     anywhere else, and the markup holds no tag or URL that reaches outside
//     the page;
//   - a text alternative: every chart carries a description;
//   - the core strip: the viewer's core runtime, the one that draws the
//     coverage strip and claim counts above every module, is in the page at
//     its pinned digest and is what the page runs;
//   - the page frame the browser checks stand on: a language, the phone-width
//     viewport, a title and a print stylesheet.
//
// Accessibility, phone-width and print layout are browser checks. A Go build
// cannot run them on each page; they are capsule-viewer's renderer tests, and
// the page records the runtime digest they covered (CoreRuntimeSHA256).
func checkPage(html string, charts []json.RawMessage) error {
	if err := checkSecurity(html); err != nil {
		return err
	}
	if err := checkChartText(charts); err != nil {
		return err
	}
	if !strings.Contains(html, string(pageruntime.IIFE)) || !strings.Contains(html, "renderEvidenceGraph(") {
		return refusal("page check failed: the page does not carry the viewer's core runtime, so it would draw no coverage strip; nothing was written")
	}
	for _, check := range []struct {
		ok   bool
		what string
	}{
		{htmlLangPattern.MatchString(html), "a language on the html element"},
		{viewportPattern.MatchString(html), "the device-width viewport"},
		{titlePattern.MatchString(html), "a title"},
		{strings.Contains(html, "@media print"), "a print stylesheet"},
	} {
		if !check.ok {
			return refusef("page check failed: the page has no %s; nothing was written", check.what)
		}
	}
	return nil
}

// checkSecurity is the "no foreign script" check, read from the page's bytes.
func checkSecurity(html string) error {
	match := cspPattern.FindStringSubmatch(html)
	if match == nil {
		return refusal("page check failed: the page carries no Content-Security-Policy; nothing was written")
	}
	policy := match[1]
	if !strings.Contains(policy, "default-src 'none'") {
		return refusal("page check failed: the page's Content-Security-Policy does not deny by default; nothing was written")
	}
	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "script-src") && (strings.Contains(directive, "'unsafe-") || strings.Contains(directive, "http") || strings.Contains(directive, "*")) {
			return refusal("page check failed: the page's script-src admits more than its own hashed scripts; nothing was written")
		}
	}
	for _, script := range scriptPattern.FindAllStringSubmatch(html, -1) {
		attrs, body := script[1], script[2]
		if strings.Contains(strings.ToLower(attrs), "src=") {
			return refusal("page check failed: a script loads from a source outside the page; nothing was written")
		}
		if strings.Contains(attrs, "application/json") {
			continue
		}
		sum := sha256.Sum256([]byte(body))
		if !strings.Contains(policy, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
			return refusal("page check failed: an inline script is not pinned by the page's Content-Security-Policy; nothing was written")
		}
	}
	markup := stylePattern.ReplaceAllString(scriptPattern.ReplaceAllString(html, ""), "")
	if tag := foreignTag.FindStringSubmatch(markup); tag != nil {
		return refusef("page check failed: the page holds a <%s> element; nothing was written", strings.ToLower(tag[1]))
	}
	for _, attr := range urlAttribute.FindAllStringSubmatch(markup, -1) {
		if value := strings.ToLower(attr[2]); value != "" && !strings.HasPrefix(value, "#") && !strings.HasPrefix(value, "data:image/") {
			return refusef("page check failed: the page's %s attribute reaches outside the page; nothing was written", strings.ToLower(attr[1]))
		}
	}
	return nil
}

// checkChartText requires a description (the chart spec's text alternative)
// on every chart spec the page draws.
func checkChartText(charts []json.RawMessage) error {
	for i, raw := range charts {
		var entry map[string]interface{}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return refusef("page check failed: charts[%d] is not a JSON object", i)
		}
		specs := chartSpecsOf(entry)
		if len(specs) == 0 {
			specs = []map[string]interface{}{entry}
		}
		for _, spec := range specs {
			if description, _ := spec["description"].(string); strings.TrimSpace(description) == "" {
				return refusef("page check failed: charts[%d] has no description, the text alternative a chart needs; nothing was written", i)
			}
		}
	}
	return nil
}
