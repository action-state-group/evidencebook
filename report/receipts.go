// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/evidencebook"
)

// ReceiptModuleID is the module a page opens a plain deal receipt with
// (capsule-viewer ts/src/evidence-graph-view.ts, RECEIPT_MODULE_IDS). A page
// carrying receipts with any other module would answer every "Open receipt"
// with "no module". The viewer also opens a composed receipt with
// capsulectl.deal-view-composed/v0, but a composed bundle is rooted at the
// composer's composition record, not at the exchange capsule a claim cites,
// so there is nothing yet to bind it to its deal by: it is refused here until
// that binding exists.
const ReceiptModuleID = "capsulectl.deal-view/v0"

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
	// Module is the deal view script and its pins. Its manifest names
	// ReceiptModuleID, and the emitter checks that the manifest pins the
	// script and stylesheets. A carried bundle must also satisfy the
	// manifest's requires.extensions.required and forbids.extensions, as the
	// page's module resolution reads them, or the page could not open it.
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
	var manifest moduleManifest
	if err := json.Unmarshal(receipts.Module.Manifest, &manifest); err != nil || manifest.ID != ReceiptModuleID {
		return checkedReceipts{}, refusef("receipts: the module must carry the manifest of %s, the module a page opens a plain deal receipt with", ReceiptModuleID)
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
	deals := make([]string, 0, len(cited))
	for deal := range cited {
		deals = append(deals, deal)
	}
	sort.Strings(deals)
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
		if err := checkReceiptBundle(r, cited[r.Deal], manifest); err != nil {
			return checkedReceipts{}, err
		}
		out.texts[r.Deal] = r.Bundle
	}
	for _, deal := range deals {
		if !seen[deal] {
			return checkedReceipts{}, refusef("receipts: deal %q has no entry: carry its receipt or mark it held by policy", deal)
		}
	}
	sort.Strings(out.withheld)
	return out, nil
}

// moduleManifest is the part of a presentation manifest that decides which
// bundles the module draws (aac.presentation-manifest/v0).
type moduleManifest struct {
	ID       string `json:"id"`
	Requires struct {
		BundleKind string `json:"bundle_kind"`
		Extensions struct {
			Required []string `json:"required"`
		} `json:"extensions"`
	} `json:"requires"`
	Forbids struct {
		Extensions []string `json:"extensions"`
		Profiles   []string `json:"profiles"`
	} `json:"forbids"`
}

// accepts says whether the module's manifest takes a bundle of this kind with
// these extension blocks and root profiles (profilesOf).
func (m moduleManifest) accepts(bundleKind string, extensions map[string]interface{}, profiles map[string]bool) bool {
	if m.Requires.BundleKind != "" && m.Requires.BundleKind != bundleKind {
		return false
	}
	for _, profile := range m.Forbids.Profiles {
		if profiles[profile] {
			return false
		}
	}
	for _, kind := range m.Requires.Extensions.Required {
		if _, ok := extensions[kind]; !ok {
			return false
		}
	}
	for _, kind := range m.Forbids.Extensions {
		if _, ok := extensions[kind]; ok {
			return false
		}
	}
	return true
}

// checkReceiptBundle holds one carried receipt to what the page then shows:
// it passes the page gate (the verdict a deal page is written under), the
// module the page carries takes it, and its root is an exchange capsule the
// deal's claims cite, so a bundle filed under the wrong deal is refused.
func checkReceiptBundle(r Receipt, cited map[string]bool, module moduleManifest) error {
	value, err := cli.DecodeBundleJSON([]byte(r.Bundle))
	if err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not decode: %s", r.Deal, cli.SafeError(err))
	}
	if err := cli.PageGate(value); err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not pass the page gate, so it is not carried: %s", r.Deal, cli.SafeError(err))
	}
	extensions, _ := value["extensions"].(map[string]interface{})
	kind, _ := value["bundle_kind"].(string)
	if !module.accepts(kind, extensions, profilesOf(value)) {
		return refusef("receipts: the receipt bundle of deal %q is not one the %s module draws (its extensions do not meet the module's requires and forbids)", r.Deal, module.ID)
	}
	root, _ := value["root"].(string)
	if !cited[root] {
		return refusef("receipts: the receipt bundle of deal %q is rooted at %q, which no claim of that deal cites as evidence", r.Deal, root)
	}
	return nil
}

// profilesOf are the profiles a bundle's root declares, as the viewer's
// module resolution reads them: "spec_version:<v>" from the root's disclosed
// agent_input, and "result_version:evidence-result-v0" when the root carries a
// Result by the same rule the report's own root gate applies
// (resultCandidates: payload form in agent_output or agent_input, or book form
// in an evidence_result header), so the two never disagree on what a Result
// root is.
func profilesOf(bundle map[string]interface{}) map[string]bool {
	profiles := make(map[string]bool)
	root, _ := bundle["root"].(string)
	disclosures, _ := bundle["disclosures"].(map[string]interface{})
	members, _ := disclosures[root].(map[string]interface{})
	if input, ok := members["agent_input"].(map[string]interface{}); ok {
		if version, ok := input["spec_version"].(string); ok {
			profiles["spec_version:"+version] = true
		}
	}
	inline := false
	for _, raw := range asSlice(bundle["records"]) {
		if record, ok := raw.(map[string]interface{}); ok && record["capsule_id"] == root {
			_, inline = record[evidencebook.RecordHeaderMember]
		}
	}
	if len(resultCandidates(root, members, inline)) > 0 {
		profiles["result_version:"+cli.ResultVersion] = true
	}
	return profiles
}
