package deal

import (
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/stretchr/testify/assert"
)

// capsulectl's core reads deal bundles by the x-deal-v0 names it keeps for
// its verifier, and may not depend on EvidenceBook (capsule-cli#326,
// scripts/check-neutral-deps.sh). This plugin depends on both, so it holds
// each such name to the profile's, the one source (evidencebook#93).
func TestCoreDealNamesAreTheProfiles(t *testing.T) {
	assert.Equal(t, dealprofile.Profile, cli.DealProfile)
	assert.Equal(t, dealprofile.ReportActionID, cli.DealReportActionID)
	assert.Equal(t, dealprofile.ReportPointer, cli.DealReportPointer)
	assert.Equal(t, dealprofile.ReportExtension, cli.DealReportExtension)
	assert.Equal(t, dealprofile.TypeTaskAuthority, cli.TypeTaskAuthority)
	assert.Equal(t, dealprofile.MaterialityModePredicate, cli.MaterialityModePredicate)
	assert.Equal(t, dealprofile.MaterialityModeNone, cli.MaterialityModeNone)
	assert.Equal(t, dealprofile.AudienceKeep, cli.DealAudienceKeep)
	assert.Equal(t, dealprofile.AudienceCounterparty, cli.DealAudienceCounterparty)
	assert.Equal(t, dealprofile.AudienceAdjudicator, cli.DealAudienceAdjudicator)
	for _, text := range []string{"", "a thread id", "Ünïcode text"} {
		nonce := "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
		want, err := dealprofile.CommitText(nonce, text)
		assert.NoError(t, err)
		got, err := cli.CommitText(nonce, text)
		assert.NoError(t, err)
		assert.Equal(t, want, got, text)
	}
}
