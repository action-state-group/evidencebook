// SPDX-License-Identifier: Apache-2.0

// Copied from capsule-cli v0.1.0-rc47 (d47ac556), cli/verify_bundle.go (the
// deal entries: DealReportEntry, dealV1ReportEntry, CheckDealStandaloneReport),
// Apache-2.0 (evidencebook#735).

package deal

import (
	"errors"
	"slices"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/disclosure"
	"github.com/action-state-group/capsule-cli/cli"
)

// dealReportEntry is verify --bundle's entry for a deal bundle's x-deal-v0
// extension. A deal report seals each copy's readable text (its summary
// lines, step lines, amounts and what was told) as a deal_report record on
// the deal's log, and the extension names it (sealed_report); its text is
// then that record's disclosed input, which the disclosure check binds:
//   - pass: the extension names a deal_report record whose input is
//     disclosed and matches, and no other deal_report's input is disclosed;
//   - fail: it names anything else, the bundle discloses a deal_report the
//     extension does not name, or the bundle holds a deal_report record at
//     all and the extension names none (it was replaced or removed, whether
//     or not the record's input was dropped);
//   - a bundle written before reports were sealed carries the text in the
//     extension itself, which no record seals: uninterpreted, with the
//     finding extension_unbound (editing it leaves the verdict VALID).
//
// CheckDealStandaloneReport runs a copy's standalone report checks
// (capsule-cli#294) as verify --bundle does, for a producer or a reader that
// admits a copy without the full verify: a standalone report must pass, and
// a copy that carries report text instead of naming the sealed report it
// should (sealed_report_not_named, the report stripped and text put inline)
// fails. An in-log report's disclosure is checked by the full verify only.
//
// For an x-deal-v1 copy it also runs the profile's report rule (the plugin's
// own, dealV1ReportVerifier, which rc47's core took by registration).
func CheckDealStandaloneReport(value map[string]interface{}) error {
	exts, _ := value["extensions"].(map[string]interface{})
	entry := DealReportEntry(value, aacbundle.VerificationResult{})
	// A v1 copy that fails names its report (or lacks the one it names)
	// even when the carrier is absent.
	_, carried := exts[DealReportExtension]
	if findings, _ := entry["findings"].([]string); slices.Contains(findings, DealV1ReportRuleFailed) {
		note, _ := entry["note"].(string)
		return errors.Join(cli.ErrBundleInvalid, errors.New("the copy's standalone report fails the x-deal-v1 report rule ("+DealV1ReportRuleFailed+"): "+note))
	}
	if (carried || entry["kind"] == DealProfileV1) && entry["status"] == "fail" || carried && entry["status"] != "pass" {
		return errors.Join(cli.ErrBundleInvalid, errors.New("the copy's standalone report does not verify (sealed_report_unverified)"))
	}
	if findings, _ := entry["findings"].([]string); slices.Contains(findings, "sealed_report_not_named") {
		return errors.Join(cli.ErrBundleInvalid, errors.New("the copy carries report text instead of naming its sealed report (sealed_report_not_named)"))
	}
	return nil
}

// dealV1ReportEntry is DealReportEntry for a bundle whose extension is
// x-deal-v1. A v1 report is always standalone (design note 461-deal-profile.md
// §9): appended to no log, so the section names the report in
// x-deal-report/v0 by its sealed_report pointer (a copy's section also carries
// the book the report is read against, which only a registered rule reads), and a pointer without that carrier, or a
// carrier without the pointer, fails. A section naming no report in a copy
// that holds deal steps is a stripped copy (sealed_report_not_named), as in
// v0; otherwise it is a book's own bundle, which shows no report text.
func dealV1ReportEntry(value, ext, section map[string]interface{}) map[string]any {
	fail := func(finding string) map[string]any {
		return map[string]any{"kind": DealProfileV1, "status": "fail", "findings": []string{finding}}
	}
	id, sealed := section[DealReportPointer].(string)
	raw, carried := ext[DealReportExtension]
	x, _ := raw.(map[string]interface{})
	switch {
	case sealed && carried && x != nil:
		return standaloneReportEntry(value, x, id, DealProfileV1, section)
	case sealed || carried:
		return fail("sealed_report_unverified")
	case holdsDealSteps(value):
		return fail("sealed_report_not_named")
	}
	return map[string]any{
		"kind": DealProfileV1, "status": "uninterpreted", "findings": []string{"extension_unbound"},
		"note": "a section naming no report: there is no report text to verify",
	}
}

func DealReportEntry(value map[string]interface{}, result aacbundle.VerificationResult) map[string]any {
	reports := map[string]bool{}
	records, _ := value["records"].([]interface{})
	for _, raw := range records {
		r, _ := raw.(map[string]interface{})
		if id, _ := r["capsule_id"].(string); id != "" && r["action_id"] == DealReportActionID {
			reports[id] = true
		}
	}
	matched := map[string]bool{}
	var disclosed []string
	for _, d := range result.Disclosures {
		if d.Member != "agent_input" || !reports[d.CapsuleID] || d.Status == "withheld" {
			continue
		}
		disclosed = append(disclosed, d.CapsuleID)
		if d.Status == disclosure.Match {
			matched[d.CapsuleID] = true
		}
	}
	ext, _ := value["extensions"].(map[string]interface{})
	deal, _ := ext[DealProfile].(map[string]interface{})
	fail := func(finding string) map[string]any {
		return map[string]any{"kind": DealProfile, "status": "fail", "findings": []string{finding}}
	}
	// x-deal-v1 beside x-deal-v0 names no version a reader can take; a v1
	// section is read by dealV1ReportEntry.
	_, v0 := ext[DealProfile]
	if v1, ok := ext[DealProfileV1]; ok {
		if v0 {
			return map[string]any{"kind": DealProfileV1, "status": "fail", "findings": []string{"sealed_report_unverified"}}
		}
		section, _ := v1.(map[string]interface{})
		return dealV1ReportEntry(value, ext, section)
	}
	id, sealed := deal[DealReportPointer].(string)
	// A standalone report (capsule-cli#294) is named by a pointer-only
	// section; the extension without one, or beside any other section,
	// fails.
	if raw, standalone := ext[DealReportExtension]; standalone {
		x, _ := raw.(map[string]interface{})
		if !sealed || len(deal) != 1 || x == nil {
			return fail("sealed_report_unverified")
		}
		return standaloneReportEntry(value, x, id, DealProfile, nil)
	}
	switch {
	case sealed && (!matched[id] || len(disclosed) != 1):
		return fail("sealed_report_unverified")
	case sealed:
		return map[string]any{"kind": DealProfile, "status": "pass", DealReportPointer: id}
	case len(deal) == 2 && deal["audience"] == DealAudienceHistory && deal["deal_id"] != nil && len(disclosed) == 0:
		// A history copy (deal history) withholds every payload and carries
		// no report text, so naming no sealed report is not a downgrade.
		return map[string]any{
			"kind": DealProfile, "status": "uninterpreted", "findings": []string{"extension_unbound"},
			"note": "a history copy: every record's payload is withheld and the section carries no report text",
		}
	case len(reports) > 0:
		// A bundle that holds a sealed report was written after reports were
		// sealed: text in the extension instead of a pointer is a downgrade
		// (the record kept, its disclosure dropped, edited text inline).
		return fail("sealed_report_not_named")
	}
	if _, sale := ext[DealSaleExtension]; sale {
		return map[string]any{
			"kind": DealProfile, "status": "uninterpreted", "findings": []string{"extension_unbound"},
			"note": "a sale's section: its sale_threads openings are checked against the sealed registrations under " + DealSaleExtension,
		}
	}
	// A deal on a book sealed its reports in its log until reports moved off
	// it (capsule-cli#294), so a copy of a book deal whose log holds none
	// names a standalone report: text in the section instead is a stripped
	// copy. Without an x-deal-v0 section at all, a copy is steps only and
	// shows no report text, which a book's own bundle may be.
	if deal != nil && holdsDealSteps(value) {
		return fail("sealed_report_not_named")
	}
	return map[string]any{
		"kind": DealProfile, "status": "uninterpreted", "findings": []string{"extension_unbound"},
		"note": "written by the producer when the bundle was made and sealed by no record: its text is not verified, and editing it does not change the verdict",
	}
}
