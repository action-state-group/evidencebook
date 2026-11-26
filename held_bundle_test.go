// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heldOf reads the held_bundle record a Close cites in its header.
func heldOf(t *testing.T, book *Book, closeRecord Record) (Record, HeldBundleStatement) {
	t.Helper()
	cites := closeRecord.Header.LinksTo(Cites)
	require.Len(t, cites, 1, "a Close citing a peer Close cites its held bundle, once")
	held, err := book.Get(context.Background(), cites[0])
	require.NoError(t, err)
	require.Equal(t, RecordTypeHeldBundle, held.Header.RecordType)
	var statement HeldBundleStatement
	require.NoError(t, json.Unmarshal(held.Header.Statement, &statement))
	return held, statement
}

func heldCount(t *testing.T, book *Book) int {
	t.Helper()
	all, err := book.Query(context.Background(), Filter{})
	require.NoError(t, err)
	n := 0
	for _, r := range all {
		if r.Header.RecordType == RecordTypeHeldBundle {
			n++
		}
	}
	return n
}

// The acceptance "held in full": a Close citing a peer Close first seals the
// peer bundle's octets as a held_bundle record and cites it; the Close body
// and its window are what they were.
func TestACloseCitingAPeerCloseHoldsThePeerBundleInFull(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	_, err := a.Checkpoint(ctx)
	require.NoError(t, err)
	peer := peerBundle(t, b)
	size := a.Size()
	input := ReconcileInput{Peer: peer, RecordType: "exchange", Compare: SamePayloadCommitments}
	sealed, err := a.Close(ctx, CloseInput{Reconcile: input, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")})
	require.NoError(t, err)

	held, statement := heldOf(t, a, sealed)
	assert.Equal(t, size+1, held.Seq, "the held bundle is appended after the window and before the Close")
	assert.Equal(t, size+2, sealed.Seq)
	assert.Equal(t, HeldBundleStatement{BundleDigest: peer.Digest, Peer: peer.LogID, ReliedBy: HeldBundleReliedByClose}, statement)
	assert.Equal(t, "party-b", held.Header.CounterpartyRef)
	assert.Equal(t, []string{digestBytes(peer.Bytes)}, held.Header.PayloadCommitments)
	got, err := a.ResolvePayload(ctx, held.RecordID, digestBytes(peer.Bytes))
	require.NoError(t, err)
	assert.Equal(t, peer.Bytes, got, "held in full: the octets as received")

	v1 := mustReadV1(t, sealed)
	from, to, err := v1.Window(size, sealed.Seq)
	require.NoError(t, err)
	assert.Equal(t, [2]uint64{1, size}, [2]uint64{from, to}, "the window ends before the held bundle")
	assert.Equal(t, []Link{{Type: Cites, Target: dg("peer-close")}}, v1.EvidenceLinks)
	recon, err := a.Reconcile(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, recon.Tallies.result(), v1.Reconcile.Tallies)
}

// The digest a copy is held by is SHA-256 of its octets, which for a bundle
// sent with other whitespace is not its bundle_digest; the record carries
// both.
func TestAHeldBundleIsFoundByItsOctetsNotItsBundleDigest(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	peer := peerBundle(t, b)
	var indented bytes.Buffer
	require.NoError(t, json.Indent(&indented, peer.Bytes, "", "  "))
	reformatted, err := VerifyBundle(indented.Bytes())
	require.NoError(t, err)
	require.Equal(t, peer.Digest, reformatted.Digest, "whitespace does not change the bundle digest")

	sealed, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Peer: reformatted, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")})
	require.NoError(t, err)
	held, statement := heldOf(t, a, sealed)
	assert.Equal(t, peer.Digest, statement.BundleDigest)
	assert.Equal(t, []string{digestBytes(indented.Bytes())}, held.Header.PayloadCommitments)
	assert.NotEqual(t, statement.BundleDigest, held.Header.PayloadCommitments[0])
}

// A Close holds only the bundle its tallies were computed from: no bytes,
// or bytes of another bundle than the one it names, are refused before
// anything is written.
func TestACloseRefusesAPeerBundleItCannotHold(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	peer := peerBundle(t, b)
	other := peerBundle(t, a)
	noBytes := peer
	noBytes.Bytes = nil
	swapped := peer
	swapped.Bytes = other.Bytes
	notABundle := peer
	notABundle.Bytes = []byte(`{"not":"a bundle"}`)
	size := a.Size()
	for name, p := range map[string]VerifiedBundle{"no bytes": noBytes, "another bundle's bytes": swapped, "bytes that do not verify": notABundle} {
		_, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Peer: p, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")})
		assert.ErrorIs(t, err, ErrInvalid, name)
		assert.Equal(t, size, a.Size(), "%s: nothing is written", name)
	}
}

// The acceptance "a reconcile that seals nothing holds nothing": Reconcile,
// and a Close that names no peer Close (it seals no tallies).
func TestAReconcileThatSealsNothingHoldsNothing(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	input := ReconcileInput{Peer: peerBundle(t, b), RecordType: "exchange", Compare: SamePayloadCommitments}
	size := a.Size()
	_, err := a.Reconcile(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, size, a.Size())
	sealed, err := a.Close(ctx, CloseInput{Reconcile: input, Counterparty: "party-b", Period: testPeriod})
	require.NoError(t, err)
	assert.Equal(t, size+1, sealed.Seq)
	assert.Empty(t, sealed.Header.LinksTo(Cites))
	assert.Zero(t, heldCount(t, a))
}

// The acceptance "deletion refused while cited and allowed after the
// relying record is deleted". A Close has no retention state, so the bundle
// a Close relied on is kept for good; a relying record that commits a
// payload can be deleted, and then the bundle can.
func TestAHeldBundleIsDeletedOnlyOnceNothingKeptCitesIt(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	peer := peerBundle(t, b)
	sealed, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Peer: peer, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")})
	require.NoError(t, err)
	byClose, _ := heldOf(t, a, sealed)
	_, err = a.SetRetention(ctx, byClose.RecordID, Deleted, "")
	assert.ErrorIs(t, err, ErrHeldCited, "a Close keeps the bundle it relied on")
	assert.ErrorContains(t, err, sealed.RecordID)
	// Withholding is not deleting.
	_, err = a.SetRetention(ctx, byClose.RecordID, Withheld, "")
	require.NoError(t, err)

	statement, err := json.Marshal(HeldBundleStatement{BundleDigest: peer.Digest, Peer: peer.LogID, ReliedBy: "observation"})
	require.NoError(t, err)
	held := mustAppend(t, a, Entry{RecordType: RecordTypeHeldBundle, EpistemicType: ObservedEvent, Payloads: [][]byte{peer.Bytes}, Statement: statement})
	relying := mustAppend(t, a, Entry{RecordType: "observation", EpistemicType: ObservedEvent, Payloads: [][]byte{[]byte("relies")}, Links: []Link{{Type: Cites, Target: held.RecordID}}})
	_, err = a.SetRetention(ctx, held.RecordID, Deleted, "")
	assert.ErrorIs(t, err, ErrHeldCited)
	_, err = a.SetRetention(ctx, relying.RecordID, Deleted, "")
	require.NoError(t, err)
	_, err = a.SetRetention(ctx, held.RecordID, Deleted, "")
	require.NoError(t, err, "nothing kept cites it now; the lifecycle records citing it do not count")
	_, err = a.ResolvePayload(ctx, held.RecordID, digestBytes(peer.Bytes))
	assert.ErrorIs(t, err, ErrUnavailable)
	got, err := a.ResolvePayload(ctx, byClose.RecordID, digestBytes(peer.Bytes))
	assert.ErrorIs(t, err, ErrUnavailable, "the Close's copy is withheld, not deleted")
	assert.Nil(t, got)
	state, err := a.Retention(ctx, byClose.RecordID)
	require.NoError(t, err)
	assert.Equal(t, Withheld, state)
}

// A Close that failed after its held bundle was stored is retried without
// a second copy: the uncited held_bundle is reused and cited.
func TestARetriedCloseReusesItsUncitedHeldBundle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "party-a", 1)
	cfg := f.config(t)
	flaky := &flakySubstrate{Substrate: cfg.Substrate}
	cfg.Substrate = flaky
	a, err := Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Release() })
	mustAppend(t, a, observation("s", "own"))
	_, b := twoBooks(t)
	in := CloseInput{Reconcile: ReconcileInput{Peer: peerBundle(t, b), Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")}

	flaky.failures = 1 // the held bundle's log append
	_, err = a.Close(ctx, in)
	require.Error(t, err)
	sealed, err := a.Close(ctx, in)
	require.NoError(t, err)
	held, _ := heldOf(t, a, sealed)
	assert.Equal(t, 1, heldCount(t, a), "one copy, reused")
	assert.Equal(t, sealed.Seq-1, held.Seq)
	// The open window still ends before the reused held bundle: the head is
	// the record before it, and it is not counted.
	v1 := mustReadV1(t, sealed)
	assert.Equal(t, map[string]int{"observation": 1}, v1.CountsByKind)
	assert.NotEqual(t, held.RecordID, v1.Head.Digest)
	from, to, err := v1.Window(held.Seq-1, sealed.Seq)
	require.NoError(t, err)
	assert.Equal(t, [2]uint64{1, held.Seq - 1}, [2]uint64{from, to})
	require.NoError(t, a.Release())
	reopened, err := Open(ctx, f.config(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Release() })
	again0, err := reopened.Get(ctx, sealed.RecordID)
	require.NoError(t, err)
	assert.Equal(t, v1, mustReadV1(t, again0), "the Close reads the same after reopen")
	a = reopened

	// A second Close of the same bundle holds its own copy: the first is
	// cited already.
	again, err := a.Close(ctx, in)
	require.NoError(t, err)
	second, _ := heldOf(t, a, again)
	assert.NotEqual(t, held.RecordID, second.RecordID)
	assert.Equal(t, 2, heldCount(t, a))
}

// A bundle rooted at the Close discloses no more of the counterparty than
// before: the held bundle's payload is carried only when DiscloseHeld asks
// for it; otherwise payloads=selected withholds it by digest and
// payloads=all, which cannot withhold, is refused.
func TestABundleOfTheCloseWithholdsTheHeldBundleUnlessAsked(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	peer := peerBundle(t, b)
	sealed, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Peer: peer, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")})
	require.NoError(t, err)
	held, _ := heldOf(t, a, sealed)
	digest := digestBytes(peer.Bytes)

	for _, tc := range []struct {
		name             string
		req              BundleRequest
		carried, refused bool
	}{
		{"by default", BundleRequest{}, false, false},
		{"asked for", BundleRequest{DiscloseHeld: true}, true, false},
		{"payloads=all, not asked for", BundleRequest{Payloads: PayloadsAll}, false, true},
		{"payloads=all, asked for", BundleRequest{Payloads: PayloadsAll, DiscloseHeld: true}, true, false},
		{"asked for but every payload withheld", BundleRequest{DiscloseHeld: true, WithholdPayloads: true}, false, false},
		{"asked for but this one withheld", BundleRequest{DiscloseHeld: true, Withhold: []string{digest}}, false, false},
		{"payloads=all withholding every payload", BundleRequest{Payloads: PayloadsAll, DiscloseHeld: true, WithholdPayloads: true}, false, true},
		{"payloads=all withholding this one", BundleRequest{Payloads: PayloadsAll, DiscloseHeld: true, Withhold: []string{digest}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			req.Root = sealed.RecordID
			size := a.Size()
			bundle, err := a.Bundle(ctx, req)
			if tc.refused {
				assert.ErrorIs(t, err, ErrInvalid, "payloads=all cannot withhold")
				after, err := a.Query(ctx, Filter{FromSeq: size + 1})
				require.NoError(t, err)
				for _, r := range after {
					assert.NotEqual(t, RecordTypeDisclosure, r.Header.RecordType, "a refused bundle records no disclosure")
				}
				return
			}
			require.NoError(t, err)
			verified, err := VerifyBundle(bundle.JSON)
			require.NoError(t, err)
			require.NoError(t, verified.FullyVerified())
			disclosed := false
			for _, r := range verified.Records {
				disclosed = disclosed || r.RecordID == held.RecordID
			}
			assert.True(t, disclosed, "the held bundle's header is in the Close's closure")
			_, carried := verified.Payloads[digest]
			assert.Equal(t, tc.carried, carried)
		})
	}
}

// The acceptance "two copies of one digest, one purged, reported per copy",
// and Held reading one copy by its citing record.
func TestHeldByReportsEachCopyOfADigest(t *testing.T) {
	ctx := context.Background()
	s := newShelfFixture(t).open(t)
	one, two := mustOpenBook(t, s, "deal/one"), mustOpenBook(t, s, "deal/two")
	r1 := mustAppend(t, one, observation("x", "the same bytes"))
	r2 := mustAppend(t, two, observation("x", "the same bytes"))
	digest := digestBytes([]byte("the same bytes"))
	_, err := one.SetRetention(ctx, r1.RecordID, Deleted, "purged")
	require.NoError(t, err)

	copies, err := s.HeldBy(ctx, digest)
	require.NoError(t, err)
	assert.Equal(t, []HeldCopy{
		{BookID: "deal/one", RecordID: r1.RecordID, State: Deleted, Present: false},
		{BookID: "deal/two", RecordID: r2.RecordID, State: Available, Present: true},
	}, copies)

	_, err = s.Held(ctx, r1.RecordID, digest)
	var unavailable *UnavailableError
	require.True(t, errors.As(err, &unavailable), "%v", err)
	assert.Equal(t, Deleted, unavailable.State)
	got, err := s.Held(ctx, r2.RecordID, digest)
	require.NoError(t, err)
	assert.Equal(t, []byte("the same bytes"), got)

	_, err = s.Held(ctx, r2.RecordID, digestBytes([]byte("other")))
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.Held(ctx, dg("no such record"), digest)
	assert.ErrorIs(t, err, ErrNotFound)
	none, err := s.HeldBy(ctx, digestBytes([]byte("never held")))
	require.NoError(t, err)
	assert.Empty(t, none)
	_, err = s.HeldBy(ctx, "not a digest")
	assert.ErrorIs(t, err, ErrInvalid)
}

// Held and HeldBy read the journal's originals too, which Shelf.Open refuses
// to open.
func TestHeldReadsTheJournalsOriginals(t *testing.T) {
	ctx := context.Background()
	s := newShelfFixture(t).open(t)
	c, _ := newStampCadence(t, s)
	mustAppend(t, mustOpenBook(t, s, "deal/ours"), observation("a", "1"))
	mustTick(t, c, tickTime)
	require.NoError(t, c.Migrate(ctx, tickTime.Add(time.Minute)))
	journal, err := s.Journal(ctx)
	require.NoError(t, err)
	r := mustAppend(t, journal, observation("j", "journal bytes"))
	digest := digestBytes([]byte("journal bytes"))

	got, err := s.Held(ctx, r.RecordID, digest)
	require.NoError(t, err)
	assert.Equal(t, []byte("journal bytes"), got)
	copies, err := s.HeldBy(ctx, digest)
	require.NoError(t, err)
	assert.Equal(t, []HeldCopy{{BookID: JournalLogID, RecordID: r.RecordID, State: Available, Present: true}}, copies)
}

// The acceptance "a tombstoned received record reported as deleted".
func TestHeldByReportsATombstonedReceivedRecordAsDeleted(t *testing.T) {
	ctx := context.Background()
	f, s := newReceiveFixture(t, ReceiveConfig{Retention: time.Hour})
	mustReceive(t, s, chainRecord(t, "alice", "deal/a", "deal-1", 1, "a1", ""), nil)
	mustReceive(t, s, chainRecord(t, "alice", "deal/b", "deal-2", 1, "b1", ""), nil)
	_, err := s.Forget(ctx, "deal-1", f.clock.t.Add(2*time.Hour), func(context.Context, string) (bool, error) { return true, nil })
	require.NoError(t, err)

	copies, err := s.HeldBy(ctx, dg("a1"))
	require.NoError(t, err)
	assert.Equal(t, []HeldCopy{{Received: true, State: Deleted}}, copies)
	copies, err = s.HeldBy(ctx, dg("b1"))
	require.NoError(t, err)
	assert.Equal(t, []HeldCopy{{Received: true, State: Available, Present: true}}, copies)
}

// failOn fails exactly the n-th log append (from 1), then behaves.
type failOn struct {
	Substrate
	n, calls int
}

func (f *failOn) Append(ctx context.Context, recordID string, at time.Time) (uint64, error) {
	f.calls++
	if f.calls == f.n {
		return 0, errors.New("substrate unavailable")
	}
	return f.Substrate.Append(ctx, recordID, at)
}

// A Close stored but not yet appended still guards the bundle it cites: the
// deletion commits it first, and is then refused.
func TestAPendingCloseStillGuardsItsHeldBundle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "party-a", 1)
	cfg := f.config(t)
	_, b := twoBooks(t)
	peer := peerBundle(t, b)
	flaky := &failOn{Substrate: cfg.Substrate}
	cfg.Substrate = flaky
	a, err := Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Release() })
	mustAppend(t, a, observation("s", "own"))
	flaky.n = flaky.calls + 2 // the held bundle's append succeeds, the Close's fails
	_, err = a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Peer: peer, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod, PeerClose: dg("peer-close")})
	require.Error(t, err)
	all, err := a.Query(ctx, Filter{})
	require.NoError(t, err)
	held := all[len(all)-1]
	require.Equal(t, RecordTypeHeldBundle, held.Header.RecordType)

	_, err = a.SetRetention(ctx, held.RecordID, Deleted, "")
	assert.ErrorIs(t, err, ErrHeldCited, "the pending Close is committed first and cites it")
	got, err := a.ResolvePayload(ctx, held.RecordID, digestBytes(peer.Bytes))
	require.NoError(t, err)
	assert.Equal(t, peer.Bytes, got)
}
