// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	aacverify "github.com/action-state-group/agent-action-capsule/go/verify"
)

type parityFixture struct {
	Source struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
	} `json:"source"`
	Pairs []struct {
		Name      string          `json:"name"`
		Requester json.RawMessage `json:"requester"`
		Provider  json.RawMessage `json:"provider"`
		Python    struct {
			Outcome       string `json:"outcome"`
			JoinKey       string `json:"join_key"`
			TwinBracketID string `json:"twin_bracket_id"`
		} `json:"python"`
	} `json:"pairs"`
}

// meshHalf reads the three correlation keys from a mesh exchange capsule the
// way the mesh correlator does: exchange_id and twin_bracket_id from
// serving_provenance, request_digest from compute_attestation.agent_input_digest.
func meshHalf(t *testing.T, raw json.RawMessage) Half {
	t.Helper()
	var capsule struct {
		CapsuleID        string `json:"capsule_id"`
		ModelAttestation struct {
			ComputeAttestation struct {
				AgentInputDigest string `json:"agent_input_digest"`
				Mesh             struct {
					ServingProvenance struct {
						ExchangeID    string `json:"exchange_id"`
						TwinBracketID string `json:"twin_bracket_id"`
					} `json:"serving_provenance"`
				} `json:"x-mesh-poc-v1"`
			} `json:"compute_attestation"`
		} `json:"model_attestation"`
	}
	if err := json.Unmarshal(raw, &capsule); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	ca := capsule.ModelAttestation.ComputeAttestation
	return Half{
		RecordID: capsule.CapsuleID,
		Correlation: Correlation{
			ExchangeID: ca.Mesh.ServingProvenance.ExchangeID, RequestDigest: ca.AgentInputDigest,
			TwinBracketID: ca.Mesh.ServingProvenance.TwinBracketID,
		},
		Covered: true,
		Trusted: aacverify.Verify(decoded, nil, nil).OK,
	}
}

// TestReconcileParityWithMeshCorrelator replays real halves sealed by the
// mesh sidecar through ReconcileHalves and requires the same outcome the
// Python correlator (served_request_join.join_served_request) reached on the
// same bytes, pair by pair, plus the same tallies. The content test is not
// part of the Python correlator, so the comparator here always agrees: this
// is correlation parity, and joined pairs are expected MATCHED.
func TestReconcileParityWithMeshCorrelator(t *testing.T) {
	fixture := readJSON[parityFixture](t, "testdata/reconcile-parity/fixture.json")
	if fixture.Source.Commit == "" || len(fixture.Pairs) < 6 {
		t.Fatal("parity fixture is missing its source commit or pairs")
	}
	agree := func(Half, Half) bool { return true }
	var want, got Tallies
	for _, pair := range fixture.Pairs {
		t.Run(pair.Name, func(t *testing.T) {
			a, b := meshHalf(t, pair.Requester), meshHalf(t, pair.Provider)
			if !a.Trusted || !b.Trusted {
				t.Fatal("a real mesh half fails the Go AAC Class 1 verifier")
			}
			results, tallies := ReconcileHalves(HalfSet{Halves: []Half{a}, Complete: true}, HalfSet{Halves: []Half{b}, Complete: true}, agree)
			got.Matched += tallies.Matched
			got.AOnly += tallies.AOnly
			got.BOnly += tallies.BOnly
			got.Conflicting += tallies.Conflicting
			switch pair.Python.Outcome {
			case "joined":
				want.Matched++
				if len(results) != 1 || results[0].State != Matched || results[0].JoinKey != pair.Python.JoinKey || results[0].TwinBracketID != pair.Python.TwinBracketID {
					t.Fatalf("python joined on %q (twin %q); go produced %+v", pair.Python.JoinKey, pair.Python.TwinBracketID, results)
				}
			case "conflicting":
				want.Conflicting++
				if len(results) != 1 || results[0].State != Conflicting || results[0].JoinKey != JoinExchangeID {
					t.Fatalf("python refused as CONFLICTING; go produced %+v", results)
				}
			case "no_correlation":
				want.AOnly++
				want.BOnly++
				if len(results) != 2 || results[0].State != AOnly || results[1].State != BOnly {
					t.Fatalf("python found no correlator; go produced %+v", results)
				}
			default:
				t.Fatalf("unknown python outcome %q", pair.Python.Outcome)
			}
		})
	}
	if got != want {
		t.Fatalf("tallies differ: go %+v, python-derived %+v", got, want)
	}
}

func half(id string, c Correlation, payload string) Half {
	return Half{RecordID: id, Correlation: c, PayloadCommitments: []string{digestBytes([]byte(payload))}, Covered: true, Trusted: true}
}

func TestReconcileSixStates(t *testing.T) {
	a := HalfSet{Complete: true, Halves: []Half{
		half("a-matched", Correlation{ExchangeID: "x1"}, "same"),
		half("a-conflicting", Correlation{ExchangeID: "x2"}, "mine"),
		half("a-only", Correlation{ExchangeID: "x3"}, "lonely"),
		{RecordID: "a-uncovered", Correlation: Correlation{ExchangeID: "x4"}, Covered: false, Trusted: true},
		{RecordID: "a-untrusted", Correlation: Correlation{ExchangeID: "x5"}, Covered: true, Trusted: false},
	}}
	b := HalfSet{Complete: true, Halves: []Half{
		half("b-matched", Correlation{ExchangeID: "x1"}, "same"),
		half("b-conflicting", Correlation{ExchangeID: "x2"}, "theirs"),
		half("b-uncovered-pair", Correlation{ExchangeID: "x4"}, "p"),
		half("b-untrusted-pair", Correlation{ExchangeID: "x5"}, "q"),
		half("b-only", Correlation{ExchangeID: "x6"}, "alone"),
	}}
	results, tallies := ReconcileHalves(a, b, SamePayloadCommitments)
	want := Tallies{Matched: 1, AOnly: 1, BOnly: 1, Conflicting: 1, Insufficient: 1, Unresolved: 1}
	if tallies != want {
		t.Fatalf("tallies %+v, want %+v; results %+v", tallies, want, results)
	}
	byA := make(map[string]ExchangeState)
	for _, r := range results {
		byA[r.A+"|"+r.B] = r.State
	}
	for key, state := range map[string]ExchangeState{
		"a-matched|b-matched": Matched, "a-conflicting|b-conflicting": Conflicting, "a-only|": AOnly,
		"a-uncovered|b-uncovered-pair": Insufficient, "a-untrusted|b-untrusted-pair": Unresolved, "|b-only": BOnly,
	} {
		if byA[key] != state {
			t.Fatalf("%s = %s, want %s", key, byA[key], state)
		}
	}
}

// TestOneHalfUnavailableIsNotDisagreement guards the rule most likely to be
// collapsed: a half with no counterpart is A_ONLY / B_ONLY (or INSUFFICIENT
// when the other side's account is incomplete) and is never CONFLICTING, no
// matter what the comparator would say.
func TestOneHalfUnavailableIsNotDisagreement(t *testing.T) {
	disagree := func(Half, Half) bool { return false }
	for _, complete := range []bool{true, false} {
		a := HalfSet{Complete: complete, Halves: []Half{half("a1", Correlation{RequestDigest: "r1"}, "x")}}
		b := HalfSet{Complete: complete, Halves: []Half{half("b1", Correlation{RequestDigest: "r2"}, "y")}}
		_, tallies := ReconcileHalves(a, b, disagree)
		if tallies.Conflicting != 0 {
			t.Fatalf("complete=%v: an unpaired half was counted as CONFLICTING: %+v", complete, tallies)
		}
		wantOnly := 0
		if complete {
			wantOnly = 1
		}
		if tallies.AOnly != wantOnly || tallies.BOnly != wantOnly || tallies.Insufficient != 2-2*wantOnly {
			t.Fatalf("complete=%v: tallies %+v", complete, tallies)
		}
	}
}

// twoBooks builds the double-entry picture: each party records its own half
// of the same three exchanges plus one the other never saw.
func twoBooks(t *testing.T) (*Book, *Book) {
	t.Helper()
	a := newFixture(t, "party-a", 1).open(t)
	b := newFixture(t, "party-b", 50).open(t)
	for i := range 3 {
		request := digestBytes([]byte(fmt.Sprintf("request-%d", i)))
		response := fmt.Sprintf(`{"reservation":"R%d","status":"cancelled"}`, i)
		mustAppend(t, a, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "party-b", Correlation: Correlation{ExchangeID: fmt.Sprintf("a-%d", i), RequestDigest: request}, Payloads: [][]byte{[]byte(response)}})
		mustAppend(t, b, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "party-a", Correlation: Correlation{ExchangeID: fmt.Sprintf("b-%d", i), RequestDigest: request}, Payloads: [][]byte{[]byte(response)}})
	}
	mustAppend(t, a, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "party-b", Correlation: Correlation{ExchangeID: "a-only", RequestDigest: digestBytes([]byte("only-a"))}, Payloads: [][]byte{[]byte("a")}})
	return a, b
}

// peerBundle is the peer's full account: every record it holds, every
// header disclosed, so its account is complete and absence is provable.
func peerBundle(t *testing.T, book *Book) VerifiedBundle {
	t.Helper()
	return peerAll(t, book)
}

func TestReconcileAndCloseAcrossTwoBooks(t *testing.T) {
	ctx := context.Background()
	a, b := twoBooks(t)
	if _, err := a.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	recon, err := a.Reconcile(ctx, ReconcileInput{Peer: peerBundle(t, b), RecordType: "exchange", Compare: SamePayloadCommitments})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tallies{Matched: 3, AOnly: 1}); recon.Tallies != want {
		t.Fatalf("tallies %+v, want %+v (pairs %+v)", recon.Tallies, want, recon.Pairs)
	}
	for _, pair := range recon.Pairs {
		if pair.State == Matched && pair.JoinKey != JoinRequestDigest {
			t.Fatalf("distinct exchange ids must join on request_digest, got %+v", pair)
		}
	}
	input := ReconcileInput{Peer: peerBundle(t, b), RecordType: "exchange", Compare: SamePayloadCommitments}
	closeA, err := a.Close(ctx, CloseInput{Reconcile: input, Counterparty: "party-b", Profile: "example-profile/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(closeA.Header.LinksTo(Closes)) != 4 {
		t.Fatalf("Close must bind every own record it placed: %+v", closeA.Header.Links)
	}

	// Status is read from the counterparty's links, never from the Close.
	key := checkpointKeyID(50)
	if got := mustStatus(t, closeA.RecordID, key, peerAll(t, b)); got != Unilateral {
		t.Fatalf("before any acknowledgement: %s", got)
	}
	if _, err := b.Acknowledge(ctx, closeA.RecordID, "party-a"); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, closeA.RecordID, key, peerAll(t, b)); got != Agreed {
		t.Fatalf("after acknowledgement: %s", got)
	}
	if _, err := b.Rebut(ctx, closeA.RecordID, "party-a", json.RawMessage(`{"reason":"late record"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := StatusOfClose(closeA.RecordID, "party-b", checkpointKeyID(1), peerAll(t, b)); !errors.Is(err, ErrInvalid) {
		t.Fatal("a bundle under a key other than the counterparty's pinned checkpoint key must not decide the status")
	}
	if got := mustStatus(t, closeA.RecordID, key, peerAll(t, b)); got != Contested {
		t.Fatalf("after rebuttal: %s", got)
	}

	// An adjustment supersedes, never rewrites.
	adjusted, err := a.Close(ctx, CloseInput{Reconcile: input, Counterparty: "party-b", Profile: "example-profile/v1", Supersedes: closeA.RecordID, Reason: "late evidence"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := a.Get(ctx, closeA.RecordID)
	if err != nil || original.Header.Seq != closeA.Seq || len(adjusted.Header.LinksTo(Supersedes)) != 1 {
		t.Fatalf("adjustment must add a superseding record and leave the original: %v", err)
	}
}

// peerAll bundles every record in book so a counterparty can read its links.
func peerAll(t *testing.T, book *Book) VerifiedBundle {
	t.Helper()
	ctx := context.Background()
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, err := book.Query(ctx, Filter{ToSeq: anchor.Entries})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(all))
	for i, r := range all {
		ids[i] = r.RecordID
	}
	bundle, err := book.Bundle(ctx, BundleRequest{Root: ids[len(ids)-1], Include: ids, At: &anchor})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBundle(bundle.JSON)
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func TestReconcileWithoutAnOwnCheckpointIsInsufficient(t *testing.T) {
	a, b := twoBooks(t)
	recon, err := a.Reconcile(context.Background(), ReconcileInput{Peer: peerBundle(t, b), RecordType: "exchange", Compare: SamePayloadCommitments})
	if err != nil {
		t.Fatal(err)
	}
	if recon.Tallies.Insufficient != 4 || recon.Tallies.Matched != 0 || recon.Tallies.AOnly != 0 || recon.Tallies.Conflicting != 0 {
		t.Fatalf("uncheckpointed own halves must be INSUFFICIENT, got %+v", recon.Tallies)
	}
}

func checkpointKeyID(seed byte) string {
	return hex.EncodeToString(seededKey(seed + 100)[32:])
}

func mustStatus(t *testing.T, closeID, key string, bundle VerifiedBundle) CloseStatus {
	t.Helper()
	status, err := StatusOfClose(closeID, "party-b", key, bundle)
	if err != nil {
		t.Fatal(err)
	}
	return status
}
