// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-viewer/go/presentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tamperedSignature(t *testing.T) []byte {
	return edited(t, mustRead(t, bookFormBundle), func(value map[string]interface{}) {
		record := value["records"].([]interface{})[0].(map[string]interface{})
		sig := []byte(record["signature"].(string))
		if sig[len(sig)-1] == '0' {
			sig[len(sig)-1] = '1'
		} else {
			sig[len(sig)-1] = '0'
		}
		record["signature"] = string(sig)
	})
}

// Without View.Diagnostic a bundle that does not verify still gets no page;
// with it, an INVALID or INCOMPLETE bundle gets a page that cannot be taken
// for a verified one: its title, a banner ahead of the viewer's own callout,
// and the card block all say so, and it has no permalink and no seal.
func TestBuildWritesAMarkedDiagnosticPageForABundleThatDoesNotVerify(t *testing.T) {
	cases := map[string]struct {
		bundle  []byte
		card    string
		verdict string
	}{
		"invalid":    {tamperedSignature(t), "obligation", "INVALID"},
		"incomplete": {mustRead(t, incompleteBundle), "outcome", "INCOMPLETE"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Build(c.bundle, View{Card: c.card})
			require.Error(t, err, "no diagnostic option, no page")

			page, err := Build(c.bundle, View{Card: c.card, Diagnostic: true})
			require.NoError(t, err)
			assert.True(t, page.Diagnostic)
			assert.Equal(t, c.verdict, page.Verdict)
			assert.NotEmpty(t, page.UnmetClaims)
			assert.NotEmpty(t, page.DiagnosticReasons)
			html := string(page.HTML)
			assert.Contains(t, html, "<title>"+DiagnosticTitle+"</title>")
			assert.Contains(t, html, `data-state="failed" role="alert"><strong>Diagnostic page.</strong>`)
			assert.Less(t, strings.Index(html, "Diagnostic page."), strings.Index(html, `<div id="app">`), "the notice comes before the viewer's mount point")
			embedded, err := embeddedBundle(page.HTML)
			require.NoError(t, err)
			card := embedded["extensions"].(map[string]interface{})[presentation.CardExtension].(map[string]interface{})
			assert.Equal(t, true, card["diagnostic"])
			_, err = page.Permalink("")
			require.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), "diagnostic")
			_, err = SealPage(t.Context(), newBook(t), page.HTML)
			require.ErrorIs(t, err, ErrRefused)
		})
	}
}

// A diagnostic page also stands in for a bundle that fails the held
// verification itself (a record dropped), which the page gate never sees.
func TestBuildWritesADiagnosticPageForATamperedBundle(t *testing.T) {
	dropped := edited(t, mustRead(t, bookFormBundle), func(v map[string]interface{}) {
		v["checkpoint"].(map[string]interface{})["mmr_size"] = json.Number("99")
	})
	_, err := Build(dropped, View{Card: "outcome"})
	require.ErrorIs(t, err, ErrRefused)

	page, err := Build(dropped, View{Card: "outcome", Diagnostic: true})
	require.NoError(t, err)
	assert.True(t, page.Diagnostic)
	assert.Equal(t, VerificationFailed, page.Verification)
	assert.Contains(t, strings.Join(page.DiagnosticReasons, "\n"), "does not verify")
	assert.Contains(t, string(page.HTML), DiagnosticTitle)
}

// A bundle that verifies gets a report page, never a diagnostic one: the
// option cannot be a way to mark a good report, or to unmark a bad one.
func TestBuildRefusesADiagnosticPageForAValidBundle(t *testing.T) {
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role", Diagnostic: true})
	require.ErrorIs(t, err, ErrRefused)
	assert.Nil(t, page)
	assert.Contains(t, err.Error(), "this bundle verifies")
}

// A diagnostic page still has to be a safe page: a root that cannot be
// rendered at all stays refused.
func TestBuildRefusesADiagnosticPageWithoutAResult(t *testing.T) {
	_, err := Build([]byte(`{}`), View{Card: "outcome", Diagnostic: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was rendered")
}

func TestStatusLineGivesCountsWithDenominators(t *testing.T) {
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role"})
	require.NoError(t, err)
	line := page.StatusLine()
	assert.Regexp(t, `^\d+ of \d+ records? verified · \d+ of \d+ claims? not evaluable$`, line)
	assert.NotContains(t, line, "%")
	assert.Equal(t, len(decode(t, mustRead(t, bookFormBundle))["records"].([]interface{})), page.Records)
	assert.Equal(t, page.Records, page.RecordsVerified, "a VALID bundle's every record verifies")

	diagnostic, err := Build(tamperedSignature(t), View{Card: "obligation", Diagnostic: true})
	require.NoError(t, err)
	assert.Less(t, diagnostic.RecordsVerified, diagnostic.Records, "the tampered record does not verify")
	assert.True(t, strings.HasPrefix(diagnostic.StatusLine(), "diagnostic page, not a verified report · "))
}

func TestStatusLineSingular(t *testing.T) {
	p := &Page{Records: 1, RecordsVerified: 1, Claims: 1, UnsupportedClaims: 1}
	assert.Equal(t, "1 of 1 record verified · 1 of 1 claim not evaluable", p.StatusLine())
}

// The checks every page passes, each failing on a page made to fail it.
func TestCheckPageBlocksAPageThatFailsAPrePublishCheck(t *testing.T) {
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role"})
	require.NoError(t, err)
	good := string(page.HTML)
	require.NoError(t, checkPage(good, nil))

	cases := map[string]struct {
		mutate func(string) string
		want   string
	}{
		"a script from elsewhere": {func(h string) string {
			return strings.Replace(h, `<div id="app">`, `<script src="https://example.com/x.js"></script><div id="app">`, 1)
		}, "outside the page"},
		"an unpinned inline script": {func(h string) string {
			return strings.Replace(h, `<div id="app">`, `<script>alert(1)</script><div id="app">`, 1)
		}, "not pinned"},
		"an image from elsewhere": {func(h string) string {
			return strings.Replace(h, `<div id="app">`, `<img src="https://example.com/p.png"><div id="app">`, 1)
		}, "reaches outside"},
		"a stylesheet link": {func(h string) string {
			return strings.Replace(h, `<div id="app">`, `<link rel="stylesheet" href="x.css"><div id="app">`, 1)
		}, "<link>"},
		"a spaced script src hiding behind a json-looking attribute": {func(h string) string {
			return strings.Replace(h, `<div id="app">`, `<script src = "https://evil.example/x.js" data-note="application/json"></script><div id="app">`, 1)
		}, "outside the page"},
		"a host in script-src": {func(h string) string {
			return strings.Replace(h, "script-src ", "script-src evil.example ", 1)
		}, "admits evil.example"},
		"a meta refresh": {func(h string) string {
			return strings.Replace(h, `<div id="app">`, `<meta http-equiv="refresh" content="0;url=https://evil.example"><div id="app">`, 1)
		}, "redirects"},
		"an open policy": {func(h string) string {
			return strings.Replace(h, "default-src 'none'", "default-src *", 1)
		}, "does not deny"},
		"no policy": {func(h string) string {
			return strings.Replace(h, `http-equiv="Content-Security-Policy"`, `http-equiv="X-Other"`, 1)
		}, "Content-Security-Policy"},
		"no language": {func(h string) string {
			return strings.Replace(h, `<html lang="en">`, `<html>`, 1)
		}, "language"},
		"no viewport": {func(h string) string {
			return strings.Replace(h, `width=device-width`, `width=1024`, 1)
		}, "viewport"},
		"no title": {func(h string) string {
			return strings.Replace(h, "<title>", "<title></title><!--", 1)
		}, "title"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := c.mutate(good)
			require.NotEqual(t, good, mutated, "the mutation must change the page")
			err := checkPage(mutated, nil)
			require.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), c.want)
			assert.Contains(t, err.Error(), "nothing was written")
		})
	}
}

func TestCheckPageRequiresATextAlternativePerChart(t *testing.T) {
	page, err := Build(mustRead(t, bookFormBundle), View{Card: "human_role"})
	require.NoError(t, err)
	chart := func(description string) []json.RawMessage {
		entry, err := json.Marshal(map[string]interface{}{"lanes": []interface{}{map[string]interface{}{"chart": map[string]interface{}{"description": description}}}})
		require.NoError(t, err)
		return []json.RawMessage{entry}
	}
	require.NoError(t, checkPage(string(page.HTML), chart("Failures per day in September")))
	for _, blank := range []string{"", "   "} {
		err := checkPage(string(page.HTML), chart(blank))
		require.ErrorIs(t, err, ErrRefused)
		assert.Contains(t, err.Error(), "text alternative")
	}
}

// A bundle that already carries this build's own card block (without the
// mark) still gets a diagnostic page; a different block still refuses.
func TestBuildMarksADiagnosticPageWhenTheBundleAlreadyCarriesItsCardBlock(t *testing.T) {
	withCard := func(card string) []byte {
		return edited(t, tamperedSignature(t), func(v map[string]interface{}) {
			ext, _ := v["extensions"].(map[string]interface{})
			if ext == nil {
				ext = map[string]interface{}{}
				v["extensions"] = ext
			}
			ext[presentation.CardExtension] = map[string]interface{}{"card": card}
		})
	}
	page, err := Build(withCard("obligation"), View{Card: "obligation", Diagnostic: true})
	require.NoError(t, err)
	assert.True(t, page.Diagnostic)
	_, err = Build(withCard("outcome"), View{Card: "obligation", Diagnostic: true})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "already carries a different")
}

// SealPage refuses a diagnostic page by its mark, not only because the page
// gate happens to refuse its bundle.
func TestSealPageRefusesAPageMarkedDiagnostic(t *testing.T) {
	page, err := Build(tamperedSignature(t), View{Card: "obligation", Diagnostic: true})
	require.NoError(t, err)
	_, err = SealPage(t.Context(), newBook(t), page.HTML)
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "diagnostic page is never sealed")
}
