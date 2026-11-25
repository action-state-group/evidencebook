package deal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The host refuses a /3 or /4 card whose deal's agreed_at is later than its
// first witnessed time by more than the allowance (SH §11.2 "Agreed no later
// than witnessed", AAC#696). It reads the holder's own book's first record,
// and checks nothing when no named witness timed it or none is named.
func TestTheCardIsRefusedWhenAgreedAtIsLaterThanTheFirstWitnessedTime(t *testing.T) {
	witnessed := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var asked []string
	timedAt := func(at time.Time, ok bool, err error) func(context.Context, string, uint64, []string) (time.Time, bool, error) {
		return func(_ context.Context, book string, position uint64, keys []string) (time.Time, bool, error) {
			asked = append(asked, book)
			assert.EqualValues(t, 1, position, "the holder's own first record, its agreement")
			assert.Equal(t, []string{"ab"}, keys)
			return at, ok, err
		}
	}
	keys := []string{"ab"}

	assert.NoError(t, agreedNoLaterThanWitnessed(t.Context(), timedAt(witnessed, true, nil), "book", "2026-11-05T11:00:00Z", keys))
	assert.NoError(t, agreedNoLaterThanWitnessed(t.Context(), timedAt(witnessed, true, nil), "book", "2026-11-05T12:05:00Z", keys), "inside the 300-second allowance")

	err := agreedNoLaterThanWitnessed(t.Context(), timedAt(witnessed, true, nil), "book", "2026-12-05T12:00:00Z", keys)
	require.Error(t, err)
	assert.ErrorIs(t, err, xdealv1.ErrAgreedAfterWitnessed)
	assert.ErrorIs(t, err, errCardNotBuilt)
	assert.Contains(t, err.Error(), "deal book")

	assert.NoError(t, agreedNoLaterThanWitnessed(t.Context(), timedAt(time.Time{}, false, nil), "book", "2026-12-05T12:00:00Z", keys), "no witnessed time: not checked")

	boom := errors.New("store")
	assert.ErrorIs(t, agreedNoLaterThanWitnessed(t.Context(), timedAt(time.Time{}, false, boom), "book", "2026-11-05T11:00:00Z", keys), boom)

	asked = nil
	assert.NoError(t, agreedNoLaterThanWitnessed(t.Context(), timedAt(witnessed, true, nil), "book", "2026-12-05T12:00:00Z", nil), "an agreement that names no witness")
	assert.Empty(t, asked, "no witness named: the shelf is not read")
}
