package deal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A deal profile as `deal init` writes it can be updated: the core requires an
// operator on a profile whose log a plugin keeps, and init sets one
// (capsule-cli#428).
func TestAProfileUpdateWorksOnADealProfile(t *testing.T) {
	dealFixture(t)
	out, err := invoke(t, "", "--profile", "deal", "profile", "update", "--profile", "deal", "--log", "deal")
	require.NoError(t, err, out)
}
