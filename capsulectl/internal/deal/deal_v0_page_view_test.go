// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/require"
)

// v0PageView is what an x-deal-v0 receipt page shows, read from the DOM
// capsule-viewer's x-deal-v0 receipt module draws (go/receipt, #869): the
// drawing is the viewer's and tested there (js-tests/receipt_*.test.js);
// these tests read capsulectl's real pages for what capsulectl supplies.
type v0PageView struct {
	// Module is the receipt module that drew the page ("" when none did).
	Module string `json:"module"`
	// Rungs are each [data-rung] line's text, by rung.
	Rungs map[string][]string `json:"rungs"`
	// WitnessNotice is the core's witness line under the banner, by its
	// data-witness-notice (capsule-viewer#489: "book-witnessed" is
	// capsulectl's reported witness time of the holder's own book).
	WitnessNotice map[string]string `json:"witness_notice"`
	// Attrs are the after-yes, settlement and countersign lines' states.
	AfterYes    string   `json:"after_yes"`
	Settlement  string   `json:"settlement"`
	Countersign []string `json:"countersign"`
	// Journey is "How it went"'s rows; nil when the page has no such section.
	Journey []string `json:"journey"`
	// Authority is the authority section's text, "" when none.
	Authority string `json:"authority"`
	// DealID is the title's data-deal-id.
	DealID string `json:"deal_id"`
	// Shared is the shared-copy marker's audience, "" on the holder's own.
	Shared string `json:"shared"`
	// Text is the receipt's text.
	Text   string `json:"text"`
	Errors []any  `json:"errors"`
}

const v0PageViewScript = `(() => {
  const root = document.querySelector("[data-receipt-module]");
  const rungs = {};
  for (const n of document.querySelectorAll("[data-rung]")) (rungs[n.getAttribute("data-rung")] ||= []).push(n.textContent);
  const attr = (sel, name) => { const n = document.querySelector(sel); return n ? n.getAttribute(name) || "" : ""; };
  const section = (title) => [...document.querySelectorAll("section[data-cv=section]")].find((s) => s.querySelector(".cv-section__title").textContent === title);
  const journey = section("How it went");
  const rows = journey ? [...journey.querySelector(".cv-section__body").children].map((c) =>
    c.getAttribute("data-item") ? c.querySelector("[data-sealed-text=item]").textContent : c.getAttribute("data-sealed-text") === "item" ? c.textContent : null).filter((t) => t !== null) : null;
  const authority = document.querySelector("[data-authority=sealed]") || section("Authority, as the holder's report states it");
  const notices = {};
  for (const n of document.querySelectorAll("[data-witness-notice]")) notices[n.getAttribute("data-witness-notice")] = n.textContent;
  return {
    module: root ? root.getAttribute("data-receipt-module") : "",
    rungs,
    witness_notice: notices,
    after_yes: attr("[data-after-yes]", "data-after-yes"),
    settlement: attr("[data-settlement]", "data-settlement"),
    countersign: [...document.querySelectorAll("[data-countersign]")].map((n) => n.getAttribute("data-countersign")),
    journey: rows,
    authority: authority ? authority.textContent : "",
    deal_id: attr("[data-deal-id]", "data-deal-id"),
    shared: attr("[data-shared]", "data-shared"),
    text: root ? root.textContent : "",
    errors: window.__renderErrors || [],
  };
})()`

// chromeOrSkip is the Chrome binary the page tests drive, or a skip.
func chromeOrSkip(t *testing.T) *headlessChrome {
	t.Helper()
	binary := os.Getenv("CAPSULECTL_CHROME")
	if binary == "" {
		t.Skip("CAPSULECTL_CHROME is not set")
	}
	return startHeadlessChrome(t, binary)
}

// readV0Page opens a page in Chrome and reads it.
func readV0Page(t *testing.T, chrome *headlessChrome, page string) v0PageView {
	t.Helper()
	opened := chrome.open(t, "file://"+page, presentationViews[1])
	var got v0PageView
	opened.eval(t, v0PageViewScript, &got)
	return got
}

// reportV0Page writes the active side's x-deal-v0 page for id (a shared copy
// for audience when it is set) and reads it.
func reportV0Page(t *testing.T, chrome *headlessChrome, id, audience string, extra ...string) v0PageView {
	t.Helper()
	page := filepath.Join(t.TempDir(), "page.html")
	args := []string{"report", "--deal", id, "--html", page}
	switch audience {
	case "":
	case cli.DealAudienceAdjudicator:
		args = append(args, "--share", audience, "--to", "Lamp Court")
	default:
		args = append(args, "--share", audience, "--to", "the shop")
	}
	dealRun(t, append(args, extra...)...)
	got := readV0Page(t, chrome, page)
	require.Empty(t, got.Errors)
	return got
}
