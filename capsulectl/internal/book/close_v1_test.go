// SPDX-License-Identifier: Apache-2.0

package book

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/action-state-group/evidencebook"
)

var periodDay0 = period{key: "day:2026-09-24", start: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), end: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)}

// A Close against the counterparty's Close bundle cites that Close and
// seals the tallies; the peer window is the one the cited Close binds.
func TestCloseCitesThePeersCloseForThePeriod(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	a, _ := bookProfile(t, "a")
	b, bKeys := bookProfile(t, "b")
	appendHalves(t, a, half{"same", "r1", "p1"}, half{"a-only", "r3", "p3"})
	appendHalves(t, b, half{"same", "r1", "p1"}, half{"b-only", "r4", "p4"})
	set(day1)
	bundle := peerBundle(t, "b")
	peerClose, err := runClose(t, "--profile", "b", "--period", "day", "--date", "2026-09-24", "--counterparty", "a")
	require.NoError(t, err)
	require.True(t, peerClose.AlreadyClosed)

	result, err := runClose(t, "--profile", "a", "--period", "day", "--counterparty", "b", "--peer", bundle, "--peer-checkpoint-key", hex.EncodeToString(bKeys.checkpoint))
	require.NoError(t, err)
	assert.Equal(t, peerClose.RecordID, result.PeerClose)
	var body evidencebook.CloseStatement
	require.NoError(t, json.Unmarshal(result.Close, &body))
	require.NotNil(t, body.Reconcile)
	assert.Equal(t, evidencebook.ResultTallies{Matched: 1, AOnly: 1, BOnly: 1}, body.Reconcile.Tallies)
	assert.Equal(t, []evidencebook.Link{{Type: evidencebook.Cites, Target: peerClose.RecordID}}, body.EvidenceLinks)
	assert.Equal(t, evidencebook.Tallies{Matched: 1, AOnly: 1, BOnly: 1}, result.Reconciliation.Tallies)
	var peerBody evidencebook.CloseStatement
	require.NoError(t, json.Unmarshal(peerClose.Close, &peerBody))
	assert.Nil(t, peerBody.Reconcile, "the first book to close had no peer Close to cite")
}

// fullBundle verifies a bundle of every record in p's book.
func fullBundle(t *testing.T, p cli.Profile) evidencebook.VerifiedBundle {
	t.Helper()
	verified, _ := fullBundleJSON(t, p)
	return verified
}

// fullBundleJSON is fullBundle, with the bundle's bytes.
func fullBundleJSON(t *testing.T, p cli.Profile) (evidencebook.VerifiedBundle, []byte) {
	t.Helper()
	var verified evidencebook.VerifiedBundle
	var raw []byte
	withBook(t, p, func(book *evidencebook.Book) {
		anchor, err := book.Checkpoint(t.Context())
		require.NoError(t, err)
		all, err := book.Query(t.Context(), evidencebook.Filter{ToSeq: anchor.Entries})
		require.NoError(t, err)
		ids := make([]string, len(all))
		for i, r := range all {
			ids[i] = r.RecordID
		}
		bundle, err := book.Bundle(t.Context(), evidencebook.BundleRequest{Root: ids[len(ids)-1], Include: ids, At: &anchor})
		require.NoError(t, err)
		verified, err = evidencebook.VerifyBundle(bundle.JSON)
		require.NoError(t, err)
		raw = bundle.JSON
	})
	return verified, raw
}

// sealPeerClose has p's book seal a Close for day0 against counterparty a,
// superseding supersedes when it is set.
func sealPeerClose(t *testing.T, p cli.Profile, supersedes string) string {
	t.Helper()
	var id string
	withBook(t, p, func(book *evidencebook.Book) {
		sealed, err := book.Close(t.Context(), evidencebook.CloseInput{
			Reconcile:    evidencebook.ReconcileInput{FromSeq: 1, ToSeq: 1, Compare: evidencebook.SamePayloadCommitments},
			Counterparty: "a", Period: evidencebook.ClosePeriod{Start: periodDay0.start, End: periodDay0.end}, Supersedes: supersedes,
		})
		require.NoError(t, err)
		id = sealed.RecordID
	})
	return id
}

func TestCitablePeerCloseDropsSupersededClosesAndRefusesAmbiguity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	b, _ := bookProfile(t, "b")
	appendHalves(t, b, half{"x1", "r1", "p1"})
	set(day1)

	cited, _, _, err := citablePeerClose(fullBundle(t, b), "a", periodDay0)
	require.NoError(t, err)
	assert.Nil(t, cited, "no Close disclosed: the Close states counts only")

	first := sealPeerClose(t, b, "")
	second := sealPeerClose(t, b, first)
	cited, from, to, err := citablePeerClose(fullBundle(t, b), "a", periodDay0)
	require.NoError(t, err)
	require.NotNil(t, cited)
	assert.Equal(t, second, cited.record.RecordID, "the superseded Close is dropped")
	assert.Equal(t, [2]uint64{1, 1}, [2]uint64{from, to}, "the peer window is the cited Close's")

	// The window is placed from the cited Close's head: a bundle that does
	// not disclose it, or counts its head cannot hold, are refused.
	withoutHead := fullBundle(t, b)
	var head string
	for _, r := range withoutHead.Records {
		if r.RecordID == second {
			var body evidencebook.CloseStatement
			require.NoError(t, json.Unmarshal(r.Header.Statement, &body))
			head = body.Head.Digest
		}
	}
	kept := withoutHead.Records[:0]
	for _, r := range withoutHead.Records {
		if r.RecordID != head {
			kept = append(kept, r)
		}
	}
	withoutHead.Records = kept
	_, _, _, err = citablePeerClose(withoutHead, "a", periodDay0)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.ErrorContains(t, err, "does not disclose the head")

	overcounted := fullBundle(t, b)
	for i, r := range overcounted.Records {
		if r.RecordID == second {
			var body map[string]any
			require.NoError(t, json.Unmarshal(r.Header.Statement, &body))
			body["counts_by_kind"] = map[string]int{"exchange": 5}
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			header := *r.Header
			header.Statement = raw
			overcounted.Records[i].Header = &header
		}
	}
	_, _, _, err = citablePeerClose(overcounted, "a", periodDay0)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.ErrorContains(t, err, "states a window its head cannot hold")

	cited, _, _, err = citablePeerClose(fullBundle(t, b), "c", periodDay0)
	require.NoError(t, err)
	assert.Nil(t, cited, "a Close against another book is not this book's peer Close")
	other := periodDay0
	other.start, other.end = other.start.AddDate(0, 0, 1), other.end.AddDate(0, 0, 1)
	cited, _, _, err = citablePeerClose(fullBundle(t, b), "a", other)
	require.NoError(t, err)
	assert.Nil(t, cited, "a Close for another period is not cited")

	third := sealPeerClose(t, b, "")
	_, _, _, err = citablePeerClose(fullBundle(t, b), "a", periodDay0)
	require.ErrorIs(t, err, cli.ErrInput)
	assert.ErrorContains(t, err, "2 Closes for this period that none supersedes")
	assert.ErrorContains(t, err, third)
}

// A Close sealed before close/v1 still makes a repeat a repeat, and
// --since-last starts after the window it sealed.
func TestALegacyCloseStillAnswersRepeatsAndSinceLast(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	a, _ := bookProfile(t, "a")
	appendHalves(t, a, half{"x1", "r1", "p1"})
	set(day1)
	var legacyID string
	withBook(t, a, func(book *evidencebook.Book) {
		body, err := json.Marshal(evidencebook.LegacyCloseStatement{Counterparty: "b", Profile: closeProfile(periodDay0), Reconciliation: evidencebook.Reconciliation{FromSeq: 1, ToSeq: 1, Tallies: evidencebook.Tallies{Insufficient: 1}}})
		require.NoError(t, err)
		record, err := book.Append(t.Context(), evidencebook.Entry{RecordType: evidencebook.RecordTypeClose, EpistemicType: evidencebook.DerivedMetric, CounterpartyRef: "b", Links: []evidencebook.Link{}, Statement: body})
		require.NoError(t, err)
		legacyID = record.RecordID
	})

	again, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b")
	require.NoError(t, err)
	assert.True(t, again.AlreadyClosed)
	assert.Equal(t, legacyID, again.RecordID)
	require.NotNil(t, again.Reconciliation, "a legacy Close prints the reconciliation it sealed")
	assert.Equal(t, 1, again.Reconciliation.Tallies.Insufficient)

	appendHalves(t, a, half{"x2", "r2", "p2"})
	set(day1.AddDate(0, 0, 1))
	next, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-25", "--counterparty", "b", "--since-last")
	require.NoError(t, err)
	assert.False(t, next.AlreadyClosed)
	assert.Equal(t, uint64(2), next.Reconciliation.FromSeq, "the window starts just after the legacy Close's to_seq")
}

// With a citable peer Close the peer window is the one that Close binds,
// never derived from the period: here every peer record was committed after
// the period ended, so the period places nothing in the peer's log.
func TestCloseTakesThePeerWindowFromTheCitedCloseNotThePeriod(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	a, _ := bookProfile(t, "a")
	b, bKeys := bookProfile(t, "b")
	appendHalves(t, a, half{"x1", "r1", "p1"})
	set(day1)
	appendHalves(t, b, half{"x1", "r1", "p1"})
	peerClose := sealPeerClose(t, b, "")
	_, raw := fullBundleJSON(t, b)
	path := filepath.Join(t.TempDir(), "b-bundle.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	result, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--peer", path, "--peer-checkpoint-key", hex.EncodeToString(bKeys.checkpoint))
	require.NoError(t, err)
	assert.Equal(t, peerClose, result.PeerClose)
	assert.Equal(t, [2]uint64{1, 1}, [2]uint64{result.Reconciliation.PeerFromSeq, result.Reconciliation.PeerToSeq})
	assert.Equal(t, 1, result.Reconciliation.Tallies.Matched)
}

func TestCitablePeerCloseReadsALegacyCloseAndSkipsUnverifiedOnes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	b, _ := bookProfile(t, "b")
	appendHalves(t, b, half{"x1", "r1", "p1"}, half{"x2", "r2", "p2"})
	set(day1)
	var legacyID string
	withBook(t, b, func(book *evidencebook.Book) {
		body, err := json.Marshal(evidencebook.LegacyCloseStatement{Counterparty: "a", Profile: closeProfile(periodDay0), Reconciliation: evidencebook.Reconciliation{FromSeq: 1, ToSeq: 2}})
		require.NoError(t, err)
		record, err := book.Append(t.Context(), evidencebook.Entry{RecordType: evidencebook.RecordTypeClose, EpistemicType: evidencebook.DerivedMetric, CounterpartyRef: "a", Links: []evidencebook.Link{}, Statement: body})
		require.NoError(t, err)
		legacyID = record.RecordID
	})
	bundle := fullBundle(t, b)
	cited, from, to, err := citablePeerClose(bundle, "a", periodDay0)
	require.NoError(t, err)
	require.NotNil(t, cited)
	assert.Equal(t, legacyID, cited.record.RecordID)
	assert.Equal(t, [2]uint64{1, 2}, [2]uint64{from, to}, "the window a legacy Close sealed")

	for _, unverified := range []func(r *evidencebook.PeerRecord){
		func(r *evidencebook.PeerRecord) { r.HeaderVerified = false },
		func(r *evidencebook.PeerRecord) { r.CapsuleOK = false },
	} {
		tampered := fullBundle(t, b)
		for i := range tampered.Records {
			if tampered.Records[i].RecordID == legacyID {
				unverified(&tampered.Records[i])
			}
		}
		cited, _, _, err = citablePeerClose(tampered, "a", periodDay0)
		require.NoError(t, err)
		assert.Nil(t, cited, "a Close the bundle did not verify is never cited")
	}
	// A Close past the interval the authenticated checkpoint covers is not
	// cited either.
	uncovered := fullBundle(t, b)
	for _, r := range uncovered.Records {
		if r.RecordID == legacyID {
			uncovered.IntervalLast = r.Seq - 1
		}
	}
	cited, _, _, err = citablePeerClose(uncovered, "a", periodDay0)
	require.NoError(t, err)
	assert.Nil(t, cited, "a Close the checkpoint does not cover is never cited")
}

// Result v0 section 4.1: a claim's close_ref cites a close/v1 Close. result
// build refuses a close or reconcile claim on a Close sealed before close/v1,
// even one whose period and tallies the claim states correctly.
func TestResultBuildRefusesALegacyClose(t *testing.T) {
	b := newResultBook(t)
	appendHalves(t, b.profile, half{"x1", "r1", "p1"})
	b.set(day1)
	var closeID string
	withBook(t, b.profile, func(book *evidencebook.Book) {
		_, err := book.Checkpoint(t.Context())
		require.NoError(t, err)
		body, err := json.Marshal(evidencebook.LegacyCloseStatement{Counterparty: "peer-b", Profile: closeProfile(periodDay0), Reconciliation: evidencebook.Reconciliation{FromSeq: 1, ToSeq: 3, Tallies: evidencebook.Tallies{Insufficient: 1}}})
		require.NoError(t, err)
		record, err := book.Append(t.Context(), evidencebook.Entry{RecordType: evidencebook.RecordTypeClose, EpistemicType: evidencebook.DerivedMetric, CounterpartyRef: "peer-b", Links: []evidencebook.Link{}, Statement: body})
		require.NoError(t, err)
		closeID = record.RecordID
	})
	before := bookSize(t, b.profile)
	sealed := map[string]any{"matched": 0, "a_only": 0, "b_only": 0, "conflicting": 0, "insufficient": 1, "unresolved": 0}
	for name, claim := range map[string]map[string]any{
		"close":     closeClaim("close-1", "UNILATERAL", "peer-b", "", closeID),
		"reconcile": reconcileClaim("rec-1", "peer-b", sealed, closeID),
	} {
		_, _, err := runResultBuild(t, b.profile, resultDoc(claim))
		require.ErrorIs(t, err, cli.ErrInput, name)
		assert.Contains(t, cli.SafeError(err), "cites Close "+closeID+", a legacy Close sealed before close/v1", name)
		assert.Contains(t, cli.SafeError(err), "seal a new Close for the period with book close", name)
	}
	assert.Equal(t, before, bookSize(t, b.profile), "nothing sealed")
}

// A repeat with --bundle-out discloses the window the Close sealed, though
// the log has grown since.
func TestARepeatedBundleOutDisclosesTheSameWindow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	set := bookClock(t, day0)
	a, _ := bookProfile(t, "a")
	appendHalves(t, a, half{"x1", "r1", "p1"}, half{"x2", "r2", "p2"})
	set(day1)
	disclosed := func(path string) []string {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		verified, err := evidencebook.VerifyBundle(raw)
		require.NoError(t, err)
		var ids []string
		for _, r := range verified.Records {
			if r.Header != nil {
				ids = append(ids, r.RecordID)
			}
		}
		slices.Sort(ids)
		return ids
	}
	first := filepath.Join(t.TempDir(), "first.json")
	_, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--bundle-out", first)
	require.NoError(t, err)
	appendHalves(t, a, half{"x3", "r3", "p3"})
	again := filepath.Join(t.TempDir(), "again.json")
	repeat, err := runClose(t, "--profile", "a", "--period", "day", "--date", "2026-09-24", "--counterparty", "b", "--bundle-out", again)
	require.NoError(t, err)
	require.True(t, repeat.AlreadyClosed)
	assert.Equal(t, disclosed(first), disclosed(again))
}
