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
// composer's composition record, which names no exchange capsule, so there is
// nothing yet to bind it to its deal by: it is refused here until that
// binding exists (agent-action-capsule#674, capsule-viewer#353).
const ReceiptModuleID = "capsulectl.deal-view/v0"

// Receipts are the deal receipts a contract report page carries
// (capsule-viewer#228, #351): the receipt module the page opens them with and
// the receipts the holder hands over, each carried or held by the holder's
// share policy. The report never decides what the policy allows: the caller
// states it per receipt. Nil carries nothing, and the page is then the page
// built without the field. A deal of the Result no receipt names says
// "Receipt not carried with this page", which is true of it: nothing was
// handed over.
//
// A deal is the part of a claim id before "::" (a Result whose claim ids have
// none carries no deal, and Receipts on it is refused).
type Receipts struct {
	// Module is the deal view script and its pins. Its manifest names
	// ReceiptModuleID, and the emitter checks that the manifest pins the
	// script and stylesheets. A carried bundle must also satisfy the
	// manifest's requires.bundle_kind, requires.extensions.required,
	// forbids.extensions and forbids.profiles, as the page's module
	// resolution reads them, or the page could not open it.
	Module emitter.Module
	// Deals are the receipts handed over, at most one per deal.
	Deals []Receipt
}

// Receipt is one deal's receipt, carried or held by policy, named by the
// exchange capsule it was fetched by.
type Receipt struct {
	// Exchange is the id of an exchange capsule of the deal: the capsule a
	// node serves a deal's receipt by. The deal is the one whose claims cite
	// it as evidence. A receipt whose capsule no deal of the report cites is
	// ignored (a node hands over every exchange it holds, and the report's
	// window decides which became deals), and one the claims of two deals cite
	// is refused. That the bundle is the receipt of this capsule's deal
	// is the caller's word: nothing in a plain receipt names the exchange
	// capsule a Result cites (capsule-viewer#353 and agent-action-capsule#674
	// are to give the Result a digest to check it against).
	Exchange string
	// Bundle is the receipt bundle as the node served it (JSON text, kept
	// exactly as given). It must pass the page gate and be one the module
	// takes.
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
	// ignored are the exchange capsule ids of receipts no deal's claims cite,
	// in the order given.
	ignored []string
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
// is rendered: the module is a receipt module, a receipt's exchange capsule is
// cited by the claims of at most one deal (one no deal cites is ignored), a
// deal has at most one receipt, and each carried bundle of a deal on the page
// passes the page gate and is one the module takes.
func checkReceipts(receipts *Receipts, claims []cli.ResultClaim) (checkedReceipts, error) {
	var manifest moduleManifest
	if err := json.Unmarshal(receipts.Module.Manifest, &manifest); err != nil || manifest.ID != ReceiptModuleID {
		return checkedReceipts{}, refusef("receipts: the module must carry the manifest of %s, the module a page opens a plain deal receipt with", ReceiptModuleID)
	}
	// The deals that cite each capsule.
	citedBy := make(map[string]map[string]bool)
	named := false
	for _, claim := range claims {
		deal, ok := dealOf(claim.ID)
		if !ok {
			continue
		}
		named = true
		for _, id := range claim.Evidence {
			if citedBy[id] == nil {
				citedBy[id] = make(map[string]bool)
			}
			citedBy[id][deal] = true
		}
	}
	if !named {
		return checkedReceipts{}, refusef("receipts: no claim of the Result names a deal (an id of the form <deal>::<requirement>), so there is nothing to carry")
	}
	out := checkedReceipts{texts: make(map[string]string)}
	seen := make(map[string]string)
	for _, r := range receipts.Deals {
		dealsOf := citedBy[r.Exchange]
		switch len(dealsOf) {
		case 0:
			// A node hands over a receipt for every exchange it holds, before
			// the report decides which became deals of its window: one no deal
			// cites is not on this page, carried or held. The caller is told
			// which (Page.IgnoredReceipts), so a mis-keyed id is not silent.
			out.ignored = append(out.ignored, r.Exchange)
			continue
		case 1:
		default:
			return checkedReceipts{}, refusef("receipts: the claims of more than one deal cite %q, so its deal is ambiguous", r.Exchange)
		}
		var deal string
		for d := range dealsOf {
			deal = d
		}
		if prior, dup := seen[deal]; dup {
			return checkedReceipts{}, refusef("receipts: deal %q has more than one receipt (%q and %q)", deal, prior, r.Exchange)
		}
		seen[deal] = r.Exchange
		if r.Held == (r.Bundle != "") {
			return checkedReceipts{}, refusef("receipts: the receipt of %q (deal %q) must either carry its bundle or be held by the share policy, and not both", r.Exchange, deal)
		}
		if r.Held {
			out.withheld = append(out.withheld, deal)
			continue
		}
		if err := checkReceiptBundle(deal, r, manifest); err != nil {
			return checkedReceipts{}, err
		}
		out.texts[deal] = r.Bundle
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
// it passes the page gate (the verdict a deal page is written under) and the
// module the page carries takes it.
func checkReceiptBundle(deal string, r Receipt, module moduleManifest) error {
	value, err := cli.DecodeBundleJSON([]byte(r.Bundle))
	if err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not decode: %s", deal, cli.SafeError(err))
	}
	if err := cli.PageGate(value); err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not pass the page gate, so it is not carried: %s", deal, cli.SafeError(err))
	}
	extensions, _ := value["extensions"].(map[string]interface{})
	kind, _ := value["bundle_kind"].(string)
	if !module.accepts(kind, extensions, profilesOf(value)) {
		return refusef("receipts: the receipt bundle of deal %q is not one the %s module draws (its kind, extensions or root profile do not meet the module's requires and forbids)", deal, module.ID)
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
