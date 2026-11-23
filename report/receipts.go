// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
)

// The module ids the page opens a deal receipt with (capsule-viewer
// ts/src/evidence-graph-view.ts, RECEIPT_MODULE_IDS). A page carrying
// receipts with any other module would answer every "Open receipt" with
// "no module".
const (
	ReceiptModuleID         = "capsulectl.deal-view/v0"
	ReceiptModuleComposedID = "capsulectl.deal-view-composed/v0"
)

// Receipts are the deal receipts a contract report page carries
// (capsule-viewer#228, #351): the receipt module the page opens them with and,
// for each deal the Result's claims name, either that deal's receipt bundle or
// the fact that the holder's share policy keeps it off the page. The report
// never decides what the policy allows: the caller states it, and a deal it
// states neither way is refused, so nothing is carried by default and a deal
// is never left to read as "built wrong".
//
// A deal is the part of a claim id before "::" (a Result whose claim ids have
// none carries no deal, and Receipts on it is refused).
type Receipts struct {
	// Module is the deal view script and its pins. Its manifest names one of
	// the two receipt module ids, and the emitter checks that the manifest
	// pins the script and stylesheets.
	Module emitter.Module
	// Deals has one entry for every deal the Result's claims name.
	Deals []Receipt
}

// Receipt is one deal's receipt, carried or held by policy.
type Receipt struct {
	// Deal is the claim ids' part before "::".
	Deal string
	// Bundle is the receipt bundle as the node served it (JSON text, kept
	// exactly as given). It must verify (VALID), and its root must be a
	// capsule one of the deal's claims cites as evidence: the exchange
	// capsule the Result names for that deal.
	Bundle string
	// Held says the holder's disclosure policy keeps this deal's receipt off
	// the page; the page says so on its claims. Exactly one of Bundle and
	// Held is set.
	Held bool
}

// checkedReceipts are Receipts that passed checkReceipts, in the emitter's
// terms.
type checkedReceipts struct {
	texts    map[string]string
	withheld []string
}

// dealOf is a claim id's deal: the part before "::", as the viewer reads it.
func dealOf(claimID string) (string, bool) {
	at := strings.Index(claimID, "::")
	if at <= 0 {
		return "", false
	}
	return claimID[:at], true
}

// checkReceipts judges receipts against the Result's claims before anything
// is rendered: the module is a receipt module, every deal the claims name has
// exactly one entry and no entry names another deal, and each carried bundle
// verifies and is rooted at a capsule its deal's claims cite.
func checkReceipts(receipts *Receipts, claims []cli.ResultClaim) (checkedReceipts, error) {
	var manifest struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(receipts.Module.Manifest, &manifest); err != nil || (manifest.ID != ReceiptModuleID && manifest.ID != ReceiptModuleComposedID) {
		return checkedReceipts{}, refusef("receipts: the module must carry the manifest of %s or %s, the modules a page opens a receipt with", ReceiptModuleID, ReceiptModuleComposedID)
	}
	cited := make(map[string]map[string]bool)
	for _, claim := range claims {
		deal, ok := dealOf(claim.ID)
		if !ok {
			continue
		}
		if cited[deal] == nil {
			cited[deal] = make(map[string]bool)
		}
		for _, id := range claim.Evidence {
			cited[deal][id] = true
		}
	}
	if len(cited) == 0 {
		return checkedReceipts{}, refusef("receipts: no claim of the Result names a deal (an id of the form <deal>::<requirement>), so there is nothing to carry")
	}
	out := checkedReceipts{texts: make(map[string]string)}
	seen := make(map[string]bool)
	for _, r := range receipts.Deals {
		if cited[r.Deal] == nil {
			return checkedReceipts{}, refusef("receipts: %q is not a deal any claim of the Result names", r.Deal)
		}
		if seen[r.Deal] {
			return checkedReceipts{}, refusef("receipts: deal %q has more than one entry", r.Deal)
		}
		seen[r.Deal] = true
		if r.Held == (r.Bundle != "") {
			return checkedReceipts{}, refusef("receipts: deal %q must either carry its receipt bundle or be held by the share policy, and not both", r.Deal)
		}
		if r.Held {
			out.withheld = append(out.withheld, r.Deal)
			continue
		}
		if err := checkReceiptBundle(r, cited[r.Deal]); err != nil {
			return checkedReceipts{}, err
		}
		out.texts[r.Deal] = r.Bundle
	}
	for deal := range cited {
		if !seen[deal] {
			return checkedReceipts{}, refusef("receipts: deal %q has no entry: carry its receipt or mark it held by policy", deal)
		}
	}
	sort.Strings(out.withheld)
	return out, nil
}

// checkReceiptBundle holds one carried receipt to what the page then shows:
// it verifies as `verify --bundle` reads it, and its root is an exchange
// capsule the deal's claims cite, so a bundle filed under the wrong deal is
// refused.
func checkReceiptBundle(r Receipt, cited map[string]bool) error {
	value, err := cli.DecodeBundleJSON([]byte(r.Bundle))
	if err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not decode: %s", r.Deal, cli.SafeError(err))
	}
	if verdict, _ := cli.BundleVerdict(value, nil); verdict != "VALID" {
		return refusef("receipts: the receipt bundle of deal %q is %s by verify --bundle, so it is not carried", r.Deal, verdict)
	}
	root, _ := value["root"].(string)
	if !cited[root] {
		return refusef("receipts: the receipt bundle of deal %q is rooted at %q, which no claim of that deal cites as evidence", r.Deal, root)
	}
	return nil
}
