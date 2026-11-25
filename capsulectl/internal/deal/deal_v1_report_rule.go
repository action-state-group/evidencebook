package deal

import (
	"encoding/json"
	"errors"

	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// verifyV1Report is the x-deal-v1 report rule the core's `verify --bundle`
// runs in the plugin's own deal-format reader (evidencebook#735; it once ran
// through capsule-cli's SetDealV1ReportVerifier, evidencebook-deal#233): the
// core checks a v1 report's neutral part and cannot import EvidenceBook, so
// without this a passing report reads report_rule_not_checked. report is the
// report pair and ext the bundle's x-deal-v1 extension, both as
// xdealv1.VerifyReportBundle takes them: it runs VerifyReport against the
// extension's seq 1 agreement and checks the records the rows rely on. The
// returned read is not needed here, the core shows the report it already read.
func verifyV1Report(report, ext []byte) error {
	_, err := xdealv1.VerifyReportBundle(report, ext)
	return err
}

// dealV1CounterpartyHalf reads a v1 copy's counterparty half
// (evidencebook#850): nil when the copy carries none (the entry then has no
// counterparty_half member, which reads absent, so a copy without one
// reads as before),
// {"status": "verified", "key": <their key's first 16 hex digits>,
// "record": <their agreement's record id>, "size": <their records the
// checkpoint covers>} when it verifies against the agreement the copy's
// book carries, and an error otherwise, which fails the copy. Only the
// holder's own copy (audience keep) may carry one. A verified half shows
// that the other party signed this deal's agreement and stamped it in its
// own book, as of their checkpoint; it says nothing of their later records.
func dealV1CounterpartyHalf(section map[string]interface{}, audience string, exts map[string]interface{}) (map[string]any, error) {
	raw, carried := exts[xdealv1.CounterpartyHalfExtension]
	if !carried {
		return nil, nil
	}
	failed := map[string]any{"status": "failed"}
	if audience != dealprofile.AudienceKeep {
		return failed, errors.New("only the holder's own copy carries the other party's half; this copy is for " + audience)
	}
	book, _ := section["book"].(map[string]interface{})
	agreement, err := json.Marshal(book["agreement"])
	if err != nil {
		return failed, err
	}
	half, err := json.Marshal(raw)
	if err != nil {
		return failed, err
	}
	read, err := xdealv1.VerifyCounterpartyHalf(agreement, half)
	if err != nil {
		return failed, err
	}
	return map[string]any{"status": "verified", "key": read.Key[:16], "record": read.RecordID, "size": read.Size}, nil
}
