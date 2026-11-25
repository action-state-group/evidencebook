package deal

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `deal tick --now` stamps at once, whatever the schedule: a statement sealed
// just after a tick is stamped without waiting for the next one, and the
// next scheduled tick is due an interval after the stamp.
func TestDealTickNowStampsAtOnce(t *testing.T) {
	dealID := noLayerAgreed(t)
	clockAt(t, time.Date(2026, 11, 4, 10, 20, 0, 0, time.UTC))
	require.Equal(t, "ticked", dealRun(t, "tick")["state"])
	dealRun(t, "statement", "--deal", dealID, "--statement", "paid_as_agreed=met", "--out", filepath.Join(t.TempDir(), "st.json"))

	clockAt(t, time.Date(2026, 11, 4, 10, 20, 5, 0, time.UTC))
	assert.Equal(t, "not_due", dealRun(t, "tick")["state"], "a scheduled tick is not due five seconds after the last")
	before := len(publishedTicks(t))
	now := dealRun(t, "tick", "--now")
	require.Equal(t, "ticked", now["state"], "%v", now)
	assert.Equal(t, true, now["stamped_on_demand"])
	assert.Len(t, publishedTicks(t), before+1, "one stamp, at once")
	due, err := time.Parse(time.RFC3339, now["due"].(string))
	require.NoError(t, err)
	assert.True(t, due.After(time.Date(2026, 11, 4, 10, 20, 5, 0, time.UTC)), "the next tick is due after the stamp: %v", due)
}

// --now takes one stamp and never waits, so it refuses --wait-up-to.
func TestDealTickNowRefusesAWait(t *testing.T) {
	noLayerAgreed(t)
	_, err := invoke(t, "", "--profile", "deal", "deal", "tick", "--now", "--wait-up-to", "5m")
	require.Error(t, err)
}

// On a shelf with no x-deal-v1 book yet, --now runs the scheduled tick and
// says so.
func TestDealTickNowOnAnUnmigratedShelfSaysItDidNotStamp(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	endpoint, _ := countingWitness(t)
	cadenceFixture(t, endpoint, public, "1m", "0s", 0)
	clockAt(t, time.Date(2026, 11, 4, 10, 20, 0, 0, time.UTC))
	dealRun(t, "tick")
	out := dealRun(t, "tick", "--now")
	assert.Equal(t, false, out["stamped_on_demand"], "%v", out)
	assert.Equal(t, "not_due", out["state"], "%v", out)
}
