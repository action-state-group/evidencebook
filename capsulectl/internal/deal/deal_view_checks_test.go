package deal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The deal view states no verification of its own: no list of what was
// checked, no signature line and no witness line. The core's lists and its
// one verdict stand alone on the page (Steven's banner ruling, 2026-10-21,
// on capsule-cli#341).
func TestTheDealViewStatesNoVerificationOfItsOwn(t *testing.T) {
	script := DealViewJS
	for _, own := range []string{"Checked on this page", "Checked only by capsulectl verify", "The agent's signature on every record", "Witnessed through", "Sealed by my agent", "Not witnessed"} {
		assert.NotContains(t, script, own)
	}
}
