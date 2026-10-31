// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
)

// TestReconcileAskedNamesExactlyTheUnsealed is evidencebook#206 row 9: a
// provider serves N exchanges and seals only some. Every answer it gives
// verifies, and the records it does hold number without a gap, so its
// answers alone look complete. The requester's reconcile over its own N
// halves names exactly the exchanges the provider never sealed, whichever
// way the provider says it holds none.
func TestReconcileAskedNamesExactlyTheUnsealed(t *testing.T) {
	const n = 10
	sealed := map[int]bool{0: true, 3: true, 4: true, 8: true}
	for _, mode := range []string{"signed no_such_subject", "verified answer without the record"} {
		t.Run(mode, func(t *testing.T) {
			var asked []AskedExchange
			var want []string
			seq := uint64(0)
			for i := range n {
				own := AskedHalf{
					RecordID:       fmt.Sprintf("own-%d", i),
					RequestDigest:  fmt.Sprintf("%064x", 1000+i),
					ResponseDigest: fmt.Sprintf("%064x", 2000+i),
				}
				a := AskedExchange{Own: own}
				switch {
				case sealed[i]:
					// The provider numbers only what it seals.
					var prev *uint64
					if seq > 0 {
						p := seq
						prev = &p
					}
					seq++
					a.Outcome, a.Covers = AskAnswered, true
					a.Records = []AskedRecord{{RecordID: fmt.Sprintf("prov-%d", i), RequestDigest: own.RequestDigest,
						ResponseDigest: own.ResponseDigest, Seq: seq, PrevSeq: prev}}
				case mode == "signed no_such_subject":
					want = append(want, own.RecordID)
					a.Outcome, a.Reason, a.Covers = AskRefused, string(ReasonNoSuchSubject), true
				default:
					want = append(want, own.RecordID)
					a.Outcome, a.Covers = AskAnswered, true
				}
				asked = append(asked, a)
			}
			got := ReconcileAsked(asked)
			if got.Verdict != OmissionsFound {
				t.Fatalf("verdict %s, want %s", got.Verdict, OmissionsFound)
			}
			if !slices.Equal(got.Omitted, want) {
				t.Fatalf("omitted %v, want exactly the unsealed %v", got.Omitted, want)
			}
			if !got.Run.Gapless || !slices.Equal(got.Run.Seqs, []uint64{1, 2, 3, 4}) {
				t.Fatalf("run %+v, want the provider's own gapless 1..4", got.Run)
			}
			for i, r := range got.Results {
				if wantState := map[bool]AskedState{true: AskedLogged, false: AskedOmitted}[sealed[i]]; r.State != wantState {
					t.Errorf("half %d: %s, want %s", i, r.State, wantState)
				}
			}
		})
	}
}

// TestReconcileAskedVectors runs testdata/reconcile-asked-v0, which includes
// the S08 dry run's capture.
func TestReconcileAskedVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/reconcile-asked-v0/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name  string          `json:"name"`
			Asked []AskedExchange `json:"asked"`
			Want  struct {
				Verdict AskedVerdict `json:"verdict"`
				Omitted []string     `json:"omitted"`
				States  []AskedState `json:"states"`
				Run     SeqRun       `json:"run"`
			} `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := ReconcileAsked(c.Asked)
			if got.Verdict != c.Want.Verdict {
				t.Errorf("verdict %s, want %s", got.Verdict, c.Want.Verdict)
			}
			if !slices.Equal(got.Omitted, c.Want.Omitted) {
				t.Errorf("omitted %v, want %v", got.Omitted, c.Want.Omitted)
			}
			var states []AskedState
			for _, r := range got.Results {
				states = append(states, r.State)
			}
			if !slices.Equal(states, c.Want.States) {
				t.Errorf("states %v, want %v", states, c.Want.States)
			}
			if got.Run.Gapless != c.Want.Run.Gapless || !slices.Equal(got.Run.Seqs, c.Want.Run.Seqs) {
				t.Errorf("run %+v, want %+v", got.Run, c.Want.Run)
			}
		})
	}
}

// TestReconcileAskedIsNeverComplete: no input yields a verdict that reads as
// complete. The verdicts are exactly the three below.
func TestReconcileAskedIsNeverComplete(t *testing.T) {
	inputs := [][]AskedExchange{
		nil,
		{{Own: AskedHalf{RecordID: "a", RequestDigest: "r", ResponseDigest: "s"}, Outcome: AskAnswered,
			Records: []AskedRecord{{RecordID: "p", RequestDigest: "r", ResponseDigest: "s", Seq: 1}}}},
		{{Own: AskedHalf{RecordID: "a"}, Outcome: AskAnswered}},
	}
	for _, in := range inputs {
		switch v := ReconcileAsked(in).Verdict; v {
		case OmissionsFound, NoOmissionFound, AskedInconclusive:
		default:
			t.Errorf("verdict %q is not one of the three", v)
		}
	}
	if got := ReconcileAsked(inputs[2]); got.Results[0].State != AskedUnresolved {
		t.Errorf("a half with no digests is %s, want UNRESOLVED", got.Results[0].State)
	}
}

// TestReconcileAskedMatchingIgnoresOrder: a record taken by one half is
// reassigned when another half can only match that record, so the input's
// order never decides how many halves are OMITTED (round-1 review of #229).
func TestReconcileAskedMatchingIgnoresOrder(t *testing.T) {
	rec := func(id string) AskedRecord {
		return AskedRecord{RecordID: id, RequestDigest: "r", ResponseDigest: "s", Seq: map[string]uint64{"p": 1, "q": 2}[id]}
	}
	a := AskedExchange{Own: AskedHalf{RecordID: "a", RequestDigest: "r", ResponseDigest: "s"}, Outcome: AskAnswered, Covers: true,
		Records: []AskedRecord{rec("p"), rec("q")}}
	b := AskedExchange{Own: AskedHalf{RecordID: "b", RequestDigest: "r", ResponseDigest: "s"}, Outcome: AskAnswered, Covers: true,
		Records: []AskedRecord{rec("p")}}
	for _, in := range [][]AskedExchange{{a, b}, {b, a}} {
		got := ReconcileAsked(in)
		if got.Verdict != NoOmissionFound || len(got.Omitted) != 0 {
			t.Errorf("order %s,%s: verdict %s, omitted %v; want both matched", in[0].Own.RecordID, in[1].Own.RecordID, got.Verdict, got.Omitted)
		}
	}

	// Shuffle a case with more equal halves than records, and the records
	// inside each answer: the number omitted stays the same.
	base := []AskedExchange{}
	for i := range 5 {
		x := AskedExchange{Own: AskedHalf{RecordID: fmt.Sprint("h", i), RequestDigest: "r", ResponseDigest: "s"}, Outcome: AskAnswered, Covers: true}
		for j := range i % 3 {
			x.Records = append(x.Records, AskedRecord{RecordID: fmt.Sprint("p", j), RequestDigest: "r", ResponseDigest: "s", Seq: uint64(j + 1)})
		}
		base = append(base, x)
	}
	want := len(ReconcileAsked(base).Omitted)
	if want != 3 {
		t.Fatalf("base case omits %d, want 3 (two records p0, p1 for five equal halves)", want)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		in := slices.Clone(base)
		rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
		for k := range in {
			in[k].Records = slices.Clone(in[k].Records)
			rng.Shuffle(len(in[k].Records), func(i, j int) { in[k].Records[i], in[k].Records[j] = in[k].Records[j], in[k].Records[i] })
		}
		if got := len(ReconcileAsked(in).Omitted); got != want {
			t.Fatalf("a shuffled input omits %d, want %d", got, want)
		}
	}
}
