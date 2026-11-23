// SPDX-License-Identifier: Apache-2.0

package report

import (
	"path/filepath"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Build gates a Result root with capsule-cli's CheckResult (report.go), so
// the capsule-cli this module requires must take the claim member `reason`
// (AAC Evidence Result section 4.4; capsule-cli v0.1.0-rc48). The vector is
// capsule-cli's own positive, vendored from capsulectl's testdata. A
// capsule-cli whose schema predates the member refuses it as an additional
// property.
func TestTheRequiredCapsuleCLITakesAClaimReason(t *testing.T) {
	raw := mustRead(t, filepath.Join("testdata", "result", "pos-example-org-reason-result.json"))
	doc, err := cli.DecodeBundleJSON(raw) // integers stay literals, as in a sealed Result
	require.NoError(t, err)
	checked, err := cli.CheckResult(doc, "")
	require.NoError(t, err)
	assert.Len(t, checked.Claims, 3)
}
