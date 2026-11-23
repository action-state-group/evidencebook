// SPDX-License-Identifier: Apache-2.0

// Copied from capsule-cli v0.1.0-rc47 (d47ac556), cli/bundle_witness.go and
// cli/verify_bundle.go, Apache-2.0 (evidencebook#735): the unexported helpers
// the deal-format checks call. rc47's receiptsClaim names the receipts a
// cadence chain carries (via); rc50's takes no via, so the plugin keeps
// rc47's.

package deal

import (
	"encoding/json"
	"fmt"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
)

// nestedBundleVerdict is a nested bundle's verdict and report (a sale's
// thread, a History's deal) as rc47's BundleVerdict gave it: the core's
// assessment with the plugin's reader, so the nested deal sections are read
// as the top level's are. A reader bug reads INVALID.
func nestedBundleVerdict(value map[string]interface{}, directory *witness.Directory) (string, map[string]any) {
	verdict, _, report, err := cli.AssessBundle(value, directory, newDealReader)
	if err != nil {
		return "INVALID", map[string]any{"verdict": "INVALID", "reader_error": err.Error()}
	}
	return verdict, report
}

func receiptsClaim(entries interface{}, statement []byte, trusted *witness.Directory, via string) (aacbundle.ClaimResult, []map[string]any) {
	var signed *witness.SignedCheckpoint
	if statement != nil {
		if parsed, err := witness.ParseSignedCheckpoint(statement); err == nil {
			signed = &parsed
		}
	}
	var directory witness.Directory
	if trusted != nil {
		directory = *trusted
	}
	status, findings, results := witness.VerifyReceipts(entries, signed, statement, directory)
	receipts := make([]map[string]any, 0, len(results))
	for _, r := range results {
		receipt := map[string]any{
			"ts_url": r.TSURL, "binding": r.Binding, "status": r.Status, "reason": r.Reason,
			"key_id": r.KeyID, "registration_time": r.RegistrationTime, "registration_time_reason": r.RegistrationTimeReason,
			"registration_time_text": registrationTimeText(r),
		}
		if via != "" {
			receipt["via"] = via
		}
		receipts = append(receipts, receipt)
	}
	return aacbundle.ClaimResult{Status: status, Findings: findings}, receipts
}

func registrationTimeText(r witness.ReceiptResult) string {
	if r.RegistrationTime != nil {
		return "signed by the witness at " + time.Unix(*r.RegistrationTime, 0).UTC().Format(time.RFC3339)
	}
	reason := ""
	if r.RegistrationTimeReason != nil {
		reason = *r.RegistrationTimeReason
	}
	if reason == witness.ReasonBindingWithoutIat {
		return "Rekor entry: time not reported (" + reason + ")"
	}
	return "no signed time (" + reason + ")"
}

func sameJSONValue(a, b interface{}) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		if xi, err := x.Int64(); err == nil {
			if yi, err := y.Int64(); err == nil {
				return xi == yi
			}
		}
		xf, errX := x.Float64()
		yf, errY := y.Float64()
		return errX == nil && errY == nil && xf == yf
	case string:
		y, ok := b.(string)
		return ok && x == y
	default:
		return fmt.Sprintf("%T:%v", a, a) == fmt.Sprintf("%T:%v", b, b)
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// dealDisclosureRecord is cli.DisclosureRecord with a copy's standalone report
// (x-deal-report/v0) among what it reveals, as rc47's core recorded it
// (cli/bundle.go at rc47): a standalone report is disclosed beside the
// records, so the copy reveals its input too. rc50's core reads no deal
// format and leaves it out.
func dealDisclosureRecord(bundle map[string]interface{}) (map[string]interface{}, error) {
	record, err := cli.DisclosureRecord(bundle)
	if err != nil {
		return nil, err
	}
	exts, _ := bundle["extensions"].(map[string]interface{})
	if x, ok := exts[DealReportExtension].(map[string]interface{}); ok {
		report, _ := x["record"].(map[string]interface{})
		if id, _ := report["capsule_id"].(string); id != "" {
			digest, err := canonical.JSONDigest(x["agent_input"])
			if err != nil {
				return nil, fmt.Errorf("digest the standalone report %s: %w", id, err)
			}
			revealed, _ := record["revealed"].(map[string]interface{})
			revealed[id] = map[string]interface{}{"agent_input": digest}
		}
	}
	return record, nil
}
