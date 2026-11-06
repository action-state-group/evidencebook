package deal

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconcileOfListing is the deal's `reconcile` member in `deal list --json`.
func reconcileOfListing(t *testing.T, id string, extra ...string) any {
	t.Helper()
	text, err := invoke(t, "", append([]string{"--profile", "deal", "deal", "list", "--json"}, extra...)...)
	require.NoError(t, err, text)
	var list []map[string]any
	if extra == nil {
		require.NoError(t, json.Unmarshal([]byte(text), &list), text)
	} else {
		var ranged struct {
			Deals []map[string]any `json:"deals"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &ranged), text)
		list = ranged.Deals
	}
	for _, l := range list {
		if l["deal_id"] == id {
			require.Contains(t, l, "reconcile")
			return l["reconcile"]
		}
	}
	require.Failf(t, "deal not listed", "%s in %s", id, text)
	return nil
}

// A deal with no sealed Result states nothing: null, never false.
func TestListReconcileIsNullBeforeAnyResult(t *testing.T) {
	pair := closedPair(t, received, received)
	assert.Nil(t, reconcileOfListing(t, pair.id))
}

// Matching halves: the counterparty's half is held and nothing conflicts.
func TestListReconcileHeldAndNotConflicting(t *testing.T) {
	pair := closedPair(t, received, received)
	_, _, _, err := runTermsResult(t, pair.id, pair.response, "--openings", pair.sellerFile)
	require.NoError(t, err)
	got, _ := reconcileOfListing(t, pair.id).(map[string]any)
	assert.Equal(t, true, got["counterparty_half_held"])
	assert.Equal(t, false, got["halves_conflict"])
	_, perr := time.Parse(time.RFC3339, got["at"].(string))
	assert.NoError(t, perr)
	assert.NotNil(t, reconcileOfListing(t, pair.id, "--since", "2000-01-01"), "a ranged listing carries it too")
	assert.NotNil(t, reconcileOfListing(t, pair.id, "--until", "2100-01-01"))
}

// Halves that disagree on the amount: held, and conflicting.
func TestListReconcileHeldAndConflicting(t *testing.T) {
	pair := closedPair(t, received, `{"status":"received","delivered":{"price_minor":185000}}`)
	_, _, _, err := runTermsResult(t, pair.id, pair.response, "--openings", pair.sellerFile)
	require.NoError(t, err)
	got, _ := reconcileOfListing(t, pair.id).(map[string]any)
	assert.Equal(t, true, got["counterparty_half_held"])
	assert.Equal(t, true, got["halves_conflict"])
}

// A Result made without the counterparty's openings cites only the holder's
// half: the counterparty's is not held.
func TestListReconcileOwnHalfOnly(t *testing.T) {
	pair := closedPair(t, received, received)
	_, _, _, err := runTermsResult(t, pair.id, pair.response)
	require.NoError(t, err)
	got, _ := reconcileOfListing(t, pair.id).(map[string]any)
	assert.Equal(t, false, got["counterparty_half_held"])
	assert.Nil(t, got["halves_conflict"], "a Result over one half compared nothing")
}

// Several Results: held is true once any verified Result cited both halves,
// `at` is the newest's, conflict is the newest two-half Result's, and a ranged listing reads only the
// Results sealed before --until (exclusive).
func TestListReconcileNewestResultAndUntil(t *testing.T) {
	pair := closedPair(t, received, `{"status":"received","delivered":{"price_minor":185000}}`)
	first := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	second := first.Add(time.Hour)
	now := clockAt(t, first)
	_, _, _, err := runTermsResult(t, pair.id, pair.response, "--openings", pair.sellerFile)
	require.NoError(t, err)
	*now = second
	_, _, _, err = runTermsResult(t, pair.id, pair.response)
	require.NoError(t, err)

	facts := func(extra ...string) map[string]any {
		got, _ := reconcileOfListing(t, pair.id, extra...).(map[string]any)
		return got
	}
	at := func(m map[string]any) string { return m["at"].(string) }
	newest := facts()
	assert.Equal(t, true, newest["counterparty_half_held"], "an earlier Result cited both halves")
	assert.Equal(t, true, newest["halves_conflict"], "the newest cites one half: it compared nothing and leaves the conflict found before")
	assert.Equal(t, second.Format(time.RFC3339), at(newest))

	earlier := facts("--until", second.Format(time.RFC3339))
	assert.Equal(t, true, earlier["halves_conflict"], "the second Result is sealed at --until and is not read")
	assert.Equal(t, first.Format(time.RFC3339), at(earlier))
	assert.Equal(t, second.Format(time.RFC3339), at(facts("--until", second.Add(time.Second).Format(time.RFC3339))))
	assert.Nil(t, reconcileOfListing(t, pair.id, "--until", first.Format(time.RFC3339)), "no Result is sealed before the first")
}

// A Result the profile cannot verify (here its signer is no longer trusted)
// fails the listing loudly: a plugin never reads a zero for a fact it could
// not check.
func TestListReconcileFailsOnAResultItCannotVerify(t *testing.T) {
	pair := closedPair(t, received, received)
	_, _, _, err := runTermsResult(t, pair.id, pair.response, "--openings", pair.sellerFile)
	require.NoError(t, err)

	p := dealProfile(t)
	other, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	p.TrustedKeys = []string{hex.EncodeToString(other)}
	require.NoError(t, cli.SaveProfile(p, true))
	text, err := invoke(t, "", "--profile", "deal", "deal", "list", "--json")
	require.ErrorIs(t, err, cli.ErrConflict, text)
}

// Both halves held but nothing comparable (a side that did not receive
// commits no money terms): held, and the conflict is unknown, not false.
func TestListReconcileHeldButNotComparable(t *testing.T) {
	pair := closedPair(t, `{"status":"not_received","reason":"not_delivered"}`, received)
	_, _, _, err := runTermsResult(t, pair.id, pair.response, "--openings", pair.sellerFile)
	require.NoError(t, err)
	got, _ := reconcileOfListing(t, pair.id).(map[string]any)
	assert.Equal(t, true, got["counterparty_half_held"])
	assert.Nil(t, got["halves_conflict"])
}
