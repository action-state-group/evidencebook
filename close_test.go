// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var testPeriod = ClosePeriod{Start: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}

// closeV1SchemaSHA256 pins schemas/vendor/close-v1.json, vendored from
// agent-action-capsule schemas/judge/close-v1.json at 5ce8c94 (#176).
const closeV1SchemaSHA256 = "183774e7eeb50f50aae545f5dca50c966624e14c1bc78d17f16922e2c6ae2f03"

// pinnedCloseSchema is the close/v1 schema ReadClose embeds, checked to be
// the pinned agent-action-capsule copy on disk.
func pinnedCloseSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile("schemas/vendor/close-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != closeV1SchemaSHA256 || !bytes.Equal(raw, closeV1SchemaJSON) {
		t.Fatalf("schemas/vendor/close-v1.json is not the pinned agent-action-capsule copy ReadClose embeds")
	}
	schema, err := closeV1Schema()
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func mustValidateClose(t *testing.T, schema *jsonschema.Schema, statement []byte) {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(statement))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("the sealed Close does not validate against close/v1: %v\n%s", err, statement)
	}
}

func mustReadV1(t *testing.T, record Record) *CloseStatement {
	t.Helper()
	v1, legacy, err := ReadClose(record.Header)
	if err != nil || v1 == nil || legacy != nil {
		t.Fatalf("ReadClose: v1 %v, legacy %v, err %v", v1, legacy, err)
	}
	return v1
}

func TestCloseSealsTheCloseV1Body(t *testing.T) {
	ctx := context.Background()
	schema := pinnedCloseSchema(t)
	a, b := twoBooks(t)
	if _, err := a.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	input := ReconcileInput{Peer: peerBundle(t, b), RecordType: "exchange", Compare: SamePayloadCommitments}
	peerClose := strings.Repeat("ab", 32)

	for _, tc := range []struct {
		name      string
		peerClose string
	}{{"counts only", ""}, {"citing the peer's Close", peerClose}} {
		t.Run(tc.name, func(t *testing.T) {
			size := a.Size()
			sealed, err := a.Close(ctx, CloseInput{Reconcile: input, Counterparty: "party-b", Period: testPeriod, PeerClose: tc.peerClose})
			if err != nil {
				t.Fatal(err)
			}
			mustValidateClose(t, schema, sealed.Header.Statement)
			v1 := mustReadV1(t, sealed)
			if sealed.Header.EpistemicType != ProducerClaim || v1.RecordID != "party-a#"+itoa(sealed.Seq) || v1.ClosedAt != sealed.Header.CommittedAt {
				t.Fatalf("header %+v, body %+v", sealed.Header, v1)
			}
			if !v1.Period.Start.Equal(testPeriod.Start) || !v1.Period.End.Equal(testPeriod.End) {
				t.Fatalf("period %+v", v1.Period)
			}
			// An open window is the whole log before the Close.
			all, err := a.Query(ctx, Filter{ToSeq: size})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]int{}
			for _, r := range all {
				want[r.Header.RecordType]++
			}
			if !mapsEqual(v1.CountsByKind, want) || v1.Head.Digest != all[len(all)-1].RecordID {
				t.Fatalf("counts %v head %s, want %v and %s", v1.CountsByKind, v1.Head.Digest, want, all[len(all)-1].RecordID)
			}
			if from, to, err := v1.Window(size, sealed.Seq); err != nil || from != 1 || to != size {
				t.Fatalf("window %d-%d (%v), want 1-%d", from, to, err, size)
			}
			if tc.peerClose == "" {
				if v1.Reconcile != nil || v1.EvidenceLinks != nil {
					t.Fatalf("a Close citing no peer Close seals counts only: %s", sealed.Header.Statement)
				}
				return
			}
			recon, err := a.Reconcile(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if v1.Reconcile == nil || v1.Reconcile.Status != Unilateral || v1.Reconcile.Tallies != recon.Tallies.result() {
				t.Fatalf("reconcile %+v, want %+v", v1.Reconcile, recon.Tallies)
			}
			if len(v1.EvidenceLinks) != 1 || v1.EvidenceLinks[0] != (Link{Type: Cites, Target: peerClose}) || len(sealed.Header.LinksTo(Cites)) != 0 {
				t.Fatalf("the peer Close is cited once, in the body only: body %+v header %+v", v1.EvidenceLinks, sealed.Header.Links)
			}
		})
	}
}

func itoa(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// A bounded window's counts and head are that window's, and an empty one
// still names a head: the record just before it.
func TestCloseCountsAndHeadFollowTheWindow(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	peer := peerBundle(t, b)
	all, err := a.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to uint64
		counts   map[string]int
	}{
		{2, 3, map[string]int{"exchange": 2}},
		{3, 2, map[string]int{}},
	} {
		sealed, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Peer: peer, FromSeq: tc.from, ToSeq: tc.to, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod})
		if err != nil {
			t.Fatal(err)
		}
		v1 := mustReadV1(t, sealed)
		if !mapsEqual(v1.CountsByKind, tc.counts) || v1.Head.Digest != all[tc.to-1].RecordID {
			t.Fatalf("window %d-%d: counts %v head %s", tc.from, tc.to, v1.CountsByKind, v1.Head.Digest)
		}
		if from, to, err := v1.Window(tc.to, sealed.Seq); err != nil || from != tc.from || to != tc.to {
			t.Fatalf("window %d-%d recovered as %d-%d (%v)", tc.from, tc.to, from, to, err)
		}
	}
}

// Close resolves an open window for its counts and head only: its tallies
// are Reconcile's for the same input, records past the last checkpoint
// included.
func TestCloseKeepsReconcilesOpenWindowRule(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	// An exchange only the peer holds is B_ONLY exactly when this book's
	// account counts as complete, which is what the open-window rule decides.
	mustAppend(t, b, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "party-a", Correlation: Correlation{ExchangeID: "b-only", RequestDigest: digestBytes([]byte("only-b"))}, Payloads: [][]byte{[]byte("b")}})
	if _, err := a.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, a, observation("s", "after the checkpoint"))
	input := ReconcileInput{Peer: peerBundle(t, b), RecordType: "exchange", Compare: SamePayloadCommitments}
	recon, err := a.Reconcile(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if recon.Tallies.BOnly != 1 {
		t.Fatalf("the peer-only exchange must be B_ONLY under an open window: %+v", recon.Tallies)
	}
	sealed, err := a.Close(ctx, CloseInput{Reconcile: input, Counterparty: "party-b", Period: testPeriod, PeerClose: strings.Repeat("cd", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadV1(t, sealed).Reconcile.Tallies; got != recon.Tallies.result() {
		t.Fatalf("Close tallied %+v, Reconcile %+v", got, recon.Tallies)
	}
}

// A record stored but not yet appended is committed before the Close takes
// its window, so the Close counts it and names it as head.
func TestCloseFlushesThePendingRecordFirst(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	flaky := &flakySubstrate{Substrate: cfg.Substrate}
	cfg.Substrate = flaky
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	mustAppend(t, book, observation("s", "one"))
	flaky.failures = 1
	if _, err := book.Append(ctx, observation("s", "two")); err == nil {
		t.Fatal("the failing append reported success")
	}
	sealed, err := book.Close(ctx, CloseInput{Reconcile: ReconcileInput{Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod})
	if err != nil {
		t.Fatal(err)
	}
	second, err := book.Query(ctx, Filter{FromSeq: 2, ToSeq: 2})
	if err != nil || len(second) != 1 {
		t.Fatalf("the pending record was not committed: %v", err)
	}
	v1 := mustReadV1(t, sealed)
	if sealed.Seq != 3 || v1.CountsByKind["observation"] != 2 || v1.Head.Digest != second[0].RecordID {
		t.Fatalf("seq %d counts %v head %s", sealed.Seq, v1.CountsByKind, v1.Head.Digest)
	}
}

func TestCloseRefusesWhatClosev1CannotState(t *testing.T) {
	ctx := context.Background()
	a, _ := twoBooks(t)
	empty := newFixture(t, "empty", 9).open(t)
	compare := ReconcileInput{Compare: SamePayloadCommitments}
	size := a.Size()
	for _, tc := range []struct {
		name string
		book *Book
		in   CloseInput
	}{
		{"no counterparty", a, CloseInput{Reconcile: compare, Period: testPeriod}},
		{"no period", a, CloseInput{Reconcile: compare, Counterparty: "party-b"}},
		{"a period ending at its start", a, CloseInput{Reconcile: compare, Counterparty: "party-b", Period: ClosePeriod{Start: testPeriod.Start, End: testPeriod.Start}}},
		{"a peer Close that is not a record id", a, CloseInput{Reconcile: compare, Counterparty: "party-b", Period: testPeriod, PeerClose: "close-1"}},
		{"a window past the log", a, CloseInput{Reconcile: ReconcileInput{ToSeq: size + 1, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod}},
		{"a window starting two past its end", a, CloseInput{Reconcile: ReconcileInput{FromSeq: 4, ToSeq: 2, Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod}},
		{"an empty book", empty, CloseInput{Reconcile: compare, Counterparty: "party-b", Period: testPeriod}},
	} {
		if _, err := tc.book.Close(ctx, tc.in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: got %v, want ErrInvalid", tc.name, err)
		}
	}
	if a.Size() != size {
		t.Fatalf("a refused Close wrote a record: size %d, was %d", a.Size(), size)
	}
}

// A Close sealed before close/v1 still verifies and reads as legacy.
// testdata/legacy-close/statement.json is the statement Book.Close sealed at
// 05ef665, over twoBooks' first four records; the record carrying it is
// appended as that Book.Close appended it, as derived_metric.
func TestReadCloseReadsLegacyAndV1(t *testing.T) {
	ctx := context.Background()
	a, _ := twoBooks(t)
	body, err := os.ReadFile("testdata/legacy-close/statement.json")
	if err != nil {
		t.Fatal(err)
	}
	old := mustAppend(t, a, Entry{RecordType: RecordTypeClose, EpistemicType: DerivedMetric, CounterpartyRef: "party-b", Links: []Link{}, Statement: body})
	v1, legacy, err := ReadClose(old.Header)
	if err != nil || v1 != nil || legacy == nil {
		t.Fatalf("legacy Close: v1 %v, legacy %+v, err %v", v1, legacy, err)
	}
	r := legacy.Reconciliation
	if legacy.Counterparty != "party-b" || legacy.Profile != "capsulectl-close/v1 day:2026-09-26 comparator=same_payload_commitments" || legacy.Reason != "golden" ||
		r.FromSeq != 1 || r.ToSeq != 4 || len(r.Pairs) != 4 || r.Pairs[0].JoinKey != JoinRequestDigest || r.Tallies != (Tallies{Matched: 3, AOnly: 1}) {
		t.Fatalf("the legacy wire names did not all read: %+v", legacy)
	}
	bundle := peerAll(t, a)
	if err := bundle.FullyVerified(); err != nil {
		t.Fatalf("a book holding a legacy Close verifies: %v", err)
	}
	sealed, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod})
	if err != nil {
		t.Fatal(err)
	}
	mustReadV1(t, sealed)
}

func TestReadCloseRefusesABodyItsHeaderContradicts(t *testing.T) {
	ctx := context.Background()
	a, _ := twoBooks(t)
	sealed, err := a.Close(ctx, CloseInput{Reconcile: ReconcileInput{Compare: SamePayloadCommitments}, Counterparty: "party-b", Period: testPeriod})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(change func(map[string]any)) Header {
		var body map[string]any
		if err := json.Unmarshal(sealed.Header.Statement, &body); err != nil {
			t.Fatal(err)
		}
		change(body)
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		h := sealed.Header
		h.Statement = raw
		return h
	}
	exchange, err := a.Query(ctx, Filter{RecordType: "exchange"})
	if err != nil {
		t.Fatal(err)
	}
	derived := sealed.Header
	derived.EpistemicType = DerivedMetric
	for name, h := range map[string]Header{
		"a non-Close record":     exchange[0].Header,
		"another record_version": edit(func(b map[string]any) { b["record_version"] = "close/v2" }),
		"another record_id":      edit(func(b map[string]any) { b["record_id"] = "party-a#1" }),
		"another closed_at":      edit(func(b map[string]any) { b["closed_at"] = "2026-09-26T00:00:00Z" }),
		"an unknown member":      edit(func(b map[string]any) { b["counterparty"] = "party-b" }),
		"a negative count":       edit(func(b map[string]any) { b["counts_by_kind"] = map[string]int{"exchange": -1} }),
		"reconcile without cites": edit(func(b map[string]any) {
			b["reconcile"] = map[string]any{"tallies": ResultTallies{}, "status": "UNILATERAL"}
		}),
		"a derived_metric header": derived,
		"a null record_version":   edit(func(b map[string]any) { b["record_version"] = nil }),
		"a null count":            edit(func(b map[string]any) { b["counts_by_kind"] = map[string]any{"exchange": nil} }),
		"a missing head":          edit(func(b map[string]any) { delete(b, "head") }),
		"an empty reconcile block": edit(func(b map[string]any) {
			b["reconcile"] = map[string]any{}
			b["x-evidence-links"] = []Link{{Type: Cites, Target: strings.Repeat("ab", 32)}}
		}),
	} {
		if _, _, err := ReadClose(h); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

// Window refuses counts a head cannot hold, whatever signed them.
func TestCloseWindowRefusesCountsThatDoNotFitTheHead(t *testing.T) {
	for name, tc := range map[string]struct {
		counts            map[string]int
		headSeq, closeSeq uint64
	}{
		"a negative count":              {map[string]int{"a": -1}, 5, 9},
		"more records than the head":    {map[string]int{"a": 4, "b": 3}, 5, 9},
		"a sum that would overflow":     {map[string]int{"a": math.MaxInt, "b": math.MaxInt, "c": math.MaxInt}, 5, 9},
		"a head at the Close":           {map[string]int{}, 9, 9},
		"a head at position zero":       {map[string]int{}, 0, 9},
		"a sum that wraps near the top": {map[string]int{"a": math.MaxInt, "b": math.MaxInt, "c": math.MaxInt}, math.MaxUint64 - 1, math.MaxUint64},
	} {
		if _, _, err := (CloseStatement{CountsByKind: tc.counts}).Window(tc.headSeq, tc.closeSeq); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if from, to, err := (CloseStatement{CountsByKind: map[string]int{"a": 5}}).Window(5, 9); err != nil || from != 1 || to != 5 {
		t.Fatalf("a window filling the log: %d-%d (%v)", from, to, err)
	}
}
