package deal

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dealprofileDir is EvidenceBook's dealprofile package as this module
// requires it, and dealProfileDir the x-deal-v0 profile it ships: its
// schemas, fixtures, reference checker (check_profile.py) and test data.
var (
	dealprofileDir = func() string {
		out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/action-state-group/evidencebook").Output()
		if err != nil {
			panic("locating the evidencebook module: " + err.Error())
		}
		return filepath.Join(strings.TrimSpace(string(out)), "dealprofile")
	}()
	dealProfileDir = filepath.Join(dealprofileDir, "x-deal-v0")
)

// sameID compares identifiers as their fingerprints would.
func TestDealSameID(t *testing.T) {
	assert.True(t, sameID("phone", "+1 555 010 2000", "(555) 010-2000"))
	assert.True(t, sameID("domain", "book.coastaljetrentals.example", "https://CoastalJetRentals.example/"))
	assert.False(t, sameID("payee", "Coastal Jet Rentals LLC", "M. Torres"))
}

func TestDealScanRefusesRawIdentifiers(t *testing.T) {
	ok := map[string]interface{}{"body": map[string]interface{}{"text": "has 2 jet skis", "digest": "0123456789abcdef0123"}}
	require.NoError(t, scanRecord(ok, []string{"M. Torres"}))
	for _, text := range []string{"call (555) 010-2044", "mail bookings@coastal.example", "ask for m. torres", "seller rating 4.9"} {
		rec := map[string]interface{}{"body": map[string]interface{}{"text": text}}
		assert.ErrorIs(t, scanRecord(rec, []string{"M. Torres"}), cli.ErrInput, text)
	}
}
