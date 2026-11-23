// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/action-state-group/evidencebook/report"
)

// testdata/derived-report/bundle.json is the bundle the root package's
// TestTermsPackReportEndToEnd builds (evidencebook#606): a requester's Result
// over a holder's answer to a Terms Pack requested by digest, with the
// contract-report/v1 extension, and that test holds this file to what the
// producer builds. Regenerate it with EB_WRITE_DERIVED_REPORT_FIXTURE=1 go test
// -run TestTermsPackReportEndToEnd . from the repository root. The same bundle
// that verifies renders as the report page, with every claim resolved.
func TestDerivedReportBundleRendersAsTheReportPage(t *testing.T) {
	bundle, err := os.ReadFile("testdata/derived-report/bundle.json")
	require.NoError(t, err)
	page, err := report.Build(bundle, report.View{Card: "outcome", ContractReport: true})
	require.NoError(t, err)
	assert.Equal(t, "VALID", page.Verdict)
	assert.Equal(t, 5, page.Claims)
	assert.Zero(t, page.UnsupportedClaims, "each claim cites the response record the bundle carries")
	for _, row := range []string{"has-observation", "has-claim", "has-ruling", "has-exchange", "all-committed"} {
		assert.Contains(t, string(page.HTML), row)
	}
}
