// SPDX-License-Identifier: Apache-2.0

package report

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/evidencebook"
)

// ReceiptModuleIDs returns the modules a page opens a plain deal receipt with:
// capsule-viewer's receipt modules for an x-deal-v0 copy, the owner's or a
// seller's (capsule-viewer go/receipt, the one receipt renderer,
// capsule-viewer#383), which the page runtime's RECEIPT_MODULE_IDS lists from
// capsule-viewer v0.26.0. A page carrying receipts with any other module
// would answer every "Open receipt" with "no module". A composed receipt is
// rooted at the composer's composition record, which names no exchange
// capsule, so there is nothing yet to bind it to its deal by: it is refused
// here until that binding exists (agent-action-capsule#674,
// capsule-viewer#353).
func ReceiptModuleIDs() []string {
	return append([]string(nil), receiptModuleIDs...)
}

var receiptModuleIDs = []string{
	"capsuleviewer.receipt.unilateral/v0",
	"capsuleviewer.receipt.seller/v0",
}

// Receipts are the deal receipts a contract report page carries
// (capsule-viewer#228, #351): the receipt modules the page opens them with and
// the receipts the holder hands over, each carried, held by the holder's
// share policy, or not closed. The report never decides what the policy allows: the caller
// states it per receipt. Nil carries nothing, and the page is then the page
// built without the field. A deal of the Result no receipt names says
// "Receipt not carried with this page", which is true of it: nothing was
// handed over.
//
// A claim's deal is its subject (the subject convention v0's readers,
// evidencebook#116 design note section 4): the subject_id of a subject_root
// that a record the claim cites is a member of, read from the bundle's
// verified headers (evidencebook.SubjectsOfRecord), else the part of its id
// before "::". A Result none of whose claims has a deal carries none, and
// Receipts on it is refused; so is one whose claim cites members of two
// subjects. capsule-viewer's page picks the same deal (ts/src/claim-subject.ts).
type Receipts struct {
	// Modules are the receipt modules the page carries, each a script and
	// its pins (capsule-viewer go/receipt's Module). Each manifest names one
	// of ReceiptModuleIDs(), at most once, and the emitter checks that it pins
	// the script and stylesheets. A carried bundle must satisfy one module's
	// requires.bundle_kind, requires.extensions.required, forbids.extensions
	// and forbids.profiles, as the page's module resolution reads them, or the
	// page could not open it.
	Modules []emitter.Module
	// Deals are the receipts handed over, at most one per deal.
	Deals []Receipt
	// Gate, when set, is the caller's own check of each carried receipt
	// bundle, run after the page gate: a non-nil error refuses the receipt.
	// The page gate is capsule-cli's core, which reads no deal section
	// (capsule-cli#609), so the caller that defines the receipts' format
	// (capsulectl-book, through the deal plugin's verify) checks it here; this
	// package stays generic and links no format's code (evidencebook#735).
	// Its error is shown through cli.SafeError: return a cli.InputError for
	// its text to reach the refusal.
	Gate func(bundle []byte) error
}

// Receipt is one deal's receipt, carried, held by policy or not closed, named by the
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
	// exactly as given). It must pass the verdict of the page that opens it
	// (receiptPageGate) and be one that one of the page's receipt modules takes.
	Bundle string
	// Held says the holder's disclosure policy keeps this deal's receipt off
	// the page; the page says so on its claims. Exactly one of Bundle, Held
	// and NotClosed is set.
	Held bool
	// NotClosed says the deal had not closed when the page was built, so it
	// has no receipt yet (an invoiced exchange with no settlement or delivery
	// recorded). The page says so apart from a held receipt and from one not
	// carried (capsule-viewer#358). Only the holder knows this: the Result
	// cannot tell a deal that never closes from one still in progress.
	NotClosed bool
}

// checkedReceipts are Receipts that passed checkReceipts, in the emitter's
// terms.
type checkedReceipts struct {
	texts    map[string]string
	withheld []string
	// notClosed are the deal ids that had not closed, sorted.
	notClosed []string
	// ignored are the exchange capsule ids of receipts no deal's claims cite,
	// in the order given.
	ignored []string
}

// dealOf is a claim's deal, as Receipts describes it: the one subject the
// records it cites are members of, by the bundle's verified records, else
// its id's part before "::". A claim citing members of two subjects is
// refused, and so is a deal the page could not look up ("__proto__").
func dealOf(claim cli.ResultClaim, records []evidencebook.PeerRecord) (string, bool, error) {
	var subjects []string
	for _, id := range claim.Evidence {
		for _, subject := range evidencebook.SubjectsOfRecord(records, id) {
			if !slices.Contains(subjects, subject) {
				subjects = append(subjects, subject)
			}
		}
	}
	deal := ""
	switch len(subjects) {
	case 0:
		at := strings.Index(claim.ID, "::")
		if at <= 0 {
			return "", false, nil
		}
		deal = claim.ID[:at]
	case 1:
		deal = subjects[0]
	default:
		sort.Strings(subjects)
		return "", false, refusef("receipts: claim %q cites members of more than one subject (%s), so its deal is ambiguous", claim.ID, strings.Join(subjects, ", "))
	}
	if deal == "__proto__" {
		return "", false, refusef("receipts: claim %q belongs to the deal %q, which a page cannot carry a receipt for", claim.ID, deal)
	}
	return deal, true, nil
}

// checkReceipts judges receipts against the Result's claims before anything
// is rendered: each module is a receipt module, a receipt's exchange capsule is
// cited by the claims of at most one deal (one no deal cites is ignored), a
// deal has at most one receipt, and each carried bundle of a deal on the page
// passes the page gate and is one that one of the page's receipt modules takes.
// records are the bundle's records as evidencebook.VerifyBundle read them.
func checkReceipts(receipts *Receipts, claims []cli.ResultClaim, records []evidencebook.PeerRecord) (checkedReceipts, error) {
	var manifests []moduleManifest
	for _, module := range receipts.Modules {
		var manifest moduleManifest
		if err := json.Unmarshal(module.Manifest, &manifest); err != nil || !isReceiptModule(manifest.ID) {
			return checkedReceipts{}, refusef("receipts: each module must carry the manifest of one of %s, the modules a page opens a plain deal receipt with", strings.Join(receiptModuleIDs, ", "))
		}
		for _, prior := range manifests {
			if prior.ID == manifest.ID {
				return checkedReceipts{}, refusef("receipts: the module %s is given twice", manifest.ID)
			}
		}
		manifests = append(manifests, manifest)
	}
	if len(manifests) == 0 {
		return checkedReceipts{}, refusef("receipts: no receipt module is given, so no receipt could open")
	}
	// The deals that cite each capsule.
	citedBy := make(map[string]map[string]bool)
	named := false
	for _, claim := range claims {
		deal, ok, err := dealOf(claim, records)
		if err != nil {
			return checkedReceipts{}, err
		}
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
		return checkedReceipts{}, refusef("receipts: no claim of the Result names a deal (cites a member of a subject, or has an id of the form <deal>::<requirement>), so there is nothing to carry")
	}
	out := checkedReceipts{texts: make(map[string]string)}
	seen := make(map[string]string)
	for _, r := range receipts.Deals {
		dealsOf := citedBy[r.Exchange]
		switch len(dealsOf) {
		case 0:
			// A node hands over a receipt for every exchange it holds, before
			// the report decides which became deals of its window: one no deal
			// cites is not on this page, carried, held or not closed. The caller is told
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
		states := 0
		for _, set := range []bool{r.Bundle != "", r.Held, r.NotClosed} {
			if set {
				states++
			}
		}
		if states != 1 {
			return checkedReceipts{}, refusef("receipts: the receipt of %q (deal %q) must either carry its bundle, be held by the share policy, or be not closed, and exactly one of them", r.Exchange, deal)
		}
		if r.NotClosed {
			out.notClosed = append(out.notClosed, deal)
			continue
		}
		if r.Held {
			out.withheld = append(out.withheld, deal)
			continue
		}
		if err := checkReceiptBundle(deal, r, manifests); err != nil {
			return checkedReceipts{}, err
		}
		if receipts.Gate != nil {
			if err := receipts.Gate([]byte(r.Bundle)); err != nil {
				return checkedReceipts{}, refusef("receipts: the receipt bundle of deal %q does not pass the caller's check, so it is not carried: %s", deal, cli.SafeError(err))
			}
		}
		out.texts[deal] = r.Bundle
	}
	sort.Strings(out.withheld)
	sort.Strings(out.notClosed)
	return out, nil
}

// moduleManifest is the part of a presentation manifest that decides which
// bundles a module draws (aac.presentation-manifest/v0).
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

// isReceiptModule says whether id is one of ReceiptModuleIDs().
func isReceiptModule(id string) bool {
	for _, known := range receiptModuleIDs {
		if id == known {
			return true
		}
	}
	return false
}

// checkReceiptBundle holds one carried receipt to what the page then shows:
// it passes the page gate (the verdict a deal page is written under) and a
// module the page carries takes it.
func checkReceiptBundle(deal string, r Receipt, modules []moduleManifest) error {
	value, err := cli.DecodeBundleJSON([]byte(r.Bundle))
	if err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not decode: %s", deal, cli.SafeError(err))
	}
	if err := receiptPageGate(value); err != nil {
		return refusef("receipts: the receipt bundle of deal %q does not pass the page gate, so it is not carried: %s", deal, cli.SafeError(err))
	}
	extensions, _ := value["extensions"].(map[string]interface{})
	kind, _ := value["bundle_kind"].(string)
	profiles := profilesOf(value)
	ids := make([]string, 0, len(modules))
	for _, module := range modules {
		if module.accepts(kind, extensions, profiles) {
			return nil
		}
		ids = append(ids, module.ID)
	}
	return refusef("receipts: the receipt bundle of deal %q is not one the page's receipt modules (%s) draw (its kind, extensions or root profile do not meet their requires and forbids)", deal, strings.Join(ids, ", "))
}

// receiptPageGate is the verdict of the page that opens a carried receipt:
// capsule-cli's page gate, or, for a producer-selected bundle only (a Mesh
// node's receipt: its interval coverage is withheld as
// selection_not_checkable, by design), agent-action-capsule's page verdict,
// which the page runtime and the receipt modules apply
// (bundle.VerifyBundleForPage and bundle.Verified; capsule-viewer#386). Any
// other bundle the page gate refuses stays refused, with its reason.
func receiptPageGate(value map[string]interface{}) error {
	gate := cli.PageGate(value)
	if gate == nil {
		return nil
	}
	if !admittedSelection(aacbundle.VerifyBundleForPage(value)) {
		return gate
	}
	if shown, total, ok := cli.ReportRowsShown(value); ok && (total == 0 || shown < total) {
		return gate
	}
	return nil
}

// admittedSelection says whether a page verdict admits a bundle the page
// gate refused: only a producer selection (interval coverage withheld as
// selection_not_checkable, as agent-action-capsule's producerSelection reads
// it) that the page verdict calls verified. A bundle that is not a selection
// is never admitted here, whatever else the page verdict tolerates.
func admittedSelection(result aacbundle.VerificationResult) bool {
	coverage := result.IntervalCoverage
	if coverage.Status != "withheld" {
		return false
	}
	selection := false
	for _, finding := range coverage.Findings {
		if finding == "selection_not_checkable" {
			selection = true
		}
	}
	return selection && aacbundle.Verified(result)
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
