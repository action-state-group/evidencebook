// SPDX-License-Identifier: Apache-2.0

package report

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/envelope"
	"github.com/action-state-group/capsule-viewer/go/pageruntime"
	"golang.org/x/net/html"
)

// DiagnosticTitle is the document title of a diagnostic page: what a browser
// tab, a bookmark and a search result show, so the page cannot be taken for a
// verified report from outside it.
const DiagnosticTitle = "DIAGNOSTIC PAGE: not a verified report"

// diagnosticBanner is the notice a diagnostic page opens with, above the
// viewer's own failed-bundle callout. It uses the shell's own banner classes,
// so it needs no style of its own under the page's CSP.
const diagnosticBanner = `<div class="aac-banner" data-state="failed" role="alert"><strong>Diagnostic page.</strong> This page was built from a bundle that did not verify. It is not a verified report. Do not rely on it, and do not publish or forward it as a report.</div>`

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

// StatusLine is the one line a build prints beside the page path: counts with
// their denominators, never a percentage, e.g. "787 of 812 records verified ·
// 3 of 12 claims not evaluable". A diagnostic page says so first.
func (p *Page) StatusLine() string {
	parts := []string{
		fmt.Sprintf("%d of %d %s verified", p.RecordsVerified, p.Records, plural(p.Records, "record", "records")),
		fmt.Sprintf("%d of %d %s not evaluable", p.UnsupportedClaims, p.Claims, plural(p.Claims, "claim", "claims")),
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

// checkSecurity is the "no foreign script" check, read from the page's bytes:
// the policy is parsed, every inline script is matched to a hash in it, and
// the markup is walked element by element.
func checkSecurity(page string) error {
	match := cspPattern.FindStringSubmatch(page)
	if match == nil {
		return refusal("page check failed: the page carries no Content-Security-Policy; nothing was written")
	}
	policy := map[string][]string{}
	for _, directive := range strings.Split(match[1], ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		// A browser keeps the first of a repeated directive, a reader of
		// the last: refuse the ambiguity rather than pick one.
		name := strings.ToLower(fields[0])
		if _, repeated := policy[name]; repeated {
			return refusef("page check failed: the page's Content-Security-Policy repeats %s; nothing was written", name)
		}
		policy[name] = fields[1:]
	}
	if d := policy["default-src"]; len(d) != 1 || d[0] != "'none'" {
		return refusal("page check failed: the page's Content-Security-Policy does not deny by default; nothing was written")
	}
	// Every source is a hash of something in the page, 'none', or (images)
	// data: URLs; a host, a scheme, a wildcard or an 'unsafe-' keyword is not.
	for name, sources := range policy {
		for _, source := range sources {
			ok := source == "'none'" || strings.HasPrefix(source, "'sha256-") && strings.HasSuffix(source, "'") || name == "img-src" && source == "data:"
			if !ok {
				return refusef("page check failed: the page's %s admits %s, more than its own hashed content; nothing was written", name, source)
			}
		}
	}
	tokens := html.NewTokenizer(strings.NewReader(page))
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			if err := tokens.Err(); err != io.EOF {
				return refusef("page check failed: the page's markup cannot be read: %v; nothing was written", err)
			}
			return nil
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := tokens.TagName()
		tag := string(name)
		attrs := map[string]string{}
		for hasAttr {
			var key, value []byte
			key, value, hasAttr = tokens.TagAttr()
			attrs[string(key)] = string(value)
		}
		switch tag {
		case "link", "iframe", "object", "embed", "base", "form", "frame", "frameset", "applet":
			return refusef("page check failed: the page holds a <%s> element; nothing was written", tag)
		case "meta":
			if strings.EqualFold(attrs["http-equiv"], "refresh") {
				return refusal("page check failed: the page redirects; nothing was written")
			}
		case "script":
			if _, ok := attrs["src"]; ok {
				return refusal("page check failed: a script loads from a source outside the page; nothing was written")
			}
			if strings.EqualFold(strings.TrimSpace(attrs["type"]), "application/json") {
				continue
			}
			// The tokenizer yields a script's body as one text token.
			if tokens.Next() != html.TextToken {
				return refusal("page check failed: an inline script is empty; nothing was written")
			}
			sum := sha256.Sum256(tokens.Raw())
			if !slices.Contains(policy["script-src"], "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
				return refusal("page check failed: an inline script is not pinned by the page's Content-Security-Policy; nothing was written")
			}
			continue
		}
		for key, value := range attrs {
			switch key {
			case "src", "href", "action", "srcset", "poster", "data", "formaction", "background", "xlink:href":
				value = strings.ToLower(strings.TrimSpace(value))
				if value != "" && !strings.HasPrefix(value, "#") && !strings.HasPrefix(value, "data:image/") {
					return refusef("page check failed: the page's %s attribute reaches outside the page; nothing was written", key)
				}
			}
		}
	}
}

// checkChartText requires a description (the chart spec's text alternative)
// on every chart spec the page draws.
func checkChartText(charts []json.RawMessage) error {
	for i, raw := range charts {
		var entry map[string]interface{}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return refusef("page check failed: charts[%d] is not a JSON object; nothing was written", i)
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
