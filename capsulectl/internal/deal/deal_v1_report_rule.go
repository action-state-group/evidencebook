package deal

import (
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// verifyV1Report is the x-deal-v1 report rule the core's `verify --bundle`
// runs through cli.SetDealV1ReportVerifier (evidencebook-deal#233): the
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
