// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"fmt"
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
					a.Outcome = AskAnswered
					a.Records = []AskedRecord{{RecordID: fmt.Sprintf("prov-%d", i), RequestDigest: own.RequestDigest,
						ResponseDigest: own.ResponseDigest, Seq: seq, PrevSeq: prev}}
				case mode == "signed no_such_subject":
					want = append(want, own.RecordID)
					a.Outcome, a.Reason = AskRefused, string(ReasonNoSuchSubject)
				default:
					want = append(want, own.RecordID)
					a.Outcome = AskAnswered
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
