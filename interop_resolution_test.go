// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// vectorView is the resolution.json responder state as an evidencerequest.View:
// records at positions 0, 1, 2 of a 42-entry log, two anchors (sizes 40 and
// 42), the declared correlation and exchange citations.
type vectorView struct {
	logSize     uint64
	anchorList  []evidencerequest.AnchorView
	records     []string
	correlation map[string][]string
	citations   map[string][]string
}

func (v vectorView) Anchors() []evidencerequest.AnchorView { return v.anchorList }
func (v vectorView) LogEntries() uint64                    { return v.logSize }
func (v vectorView) LeafOf(d string) (uint64, bool) {
	i := slices.Index(v.records, d)
	return uint64(i), i >= 0
}
func (v vectorView) Servable(leaf uint64) bool { return leaf < uint64(len(v.records)) }
func (v vectorView) positions(ds []string) []uint64 {
	var out []uint64
	for _, d := range ds {
		if leaf, ok := v.LeafOf(d); ok {
			out = append(out, leaf)
		}
	}
	return out
}
func (v vectorView) Correlated(id string) []uint64  { return v.positions(v.correlation[id]) }
func (v vectorView) Citing(half string) []uint64    { return v.positions(v.citations[half]) }
func (v vectorView) Lapsed(Subject, time.Time) bool { return false }

type resolutionVector struct {
	ID          string         `json:"id"`
	RequestJSON string         `json:"request_json"`
	Overrides   map[string]any `json:"responder_overrides"`
	Expect      struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	} `json:"expect"`
}

// TestResolutionVectors runs resolution.json against the responder's
// resolution logic, with the corpus's declared responder state as the
// evidence view, after the request reader and the derivation and refusal
// policy steps Respond applies. The one case whose responder defines no
// positional ordering cannot be a state of this responder (a Checkpointed
// Local Log always has positions) and is skipped with that reason.
func TestResolutionVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(erVectors, "resolution.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Responder struct {
			LogSize uint64 `json:"log_size"`
			Anchors []struct {
				Digest   string `json:"digest"`
				Size     uint64 `json:"size"`
				IssuedAt string `json:"issued_at"`
			} `json:"anchors"`
			Records               []string            `json:"records"`
			Correlations          map[string][]string `json:"correlations"`
			ExchangeCitations     map[string][]string `json:"exchange_citations"`
			SupportedDerivations  map[string][]string `json:"supported_derivations"`
			IssuesNewCommitments  bool                `json:"issues_new_commitments"`
			UniformPolicyDeclined bool                `json:"uniform_policy_declined"`
		} `json:"responder"`
		Cases []resolutionVector `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	state := corpus.Responder
	if forms := state.SupportedDerivations[DerivationHistoryCard]; len(state.SupportedDerivations) != 1 || !slices.Equal(forms, []string{"full_history", "checkpoints"}) {
		t.Fatalf("the corpus's derivations differ from this responder's (history_card/1 over full_history and checkpoints): %v", state.SupportedDerivations)
	}
	view := vectorView{logSize: state.LogSize, records: state.Records, correlation: state.Correlations, citations: state.ExchangeCitations}
	for _, a := range state.Anchors {
		issued, err := time.Parse(time.RFC3339, a.IssuedAt)
		if err != nil {
			t.Fatal(err)
		}
		view.anchorList = append(view.anchorList, evidencerequest.AnchorView{Digest: a.Digest, Entries: a.Size, IssuedAt: issued})
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ran := 0
	for _, c := range corpus.Cases {
		t.Run(c.ID, func(t *testing.T) {
			if ordering, set := c.Overrides["positional_ordering"]; set && ordering == false {
				t.Skip("a Checkpointed Local Log always defines positions; this responder state cannot occur")
			}
			uniform := state.UniformPolicyDeclined
			if u, set := c.Overrides["uniform_policy_declined"].(bool); set {
				uniform = u
			}
			got := decideForVector(view, []byte(c.RequestJSON), now, state.IssuesNewCommitments, uniform)
			want := c.Expect.Reason
			if c.Expect.Outcome == "artifact" {
				want = ""
			}
			if got != want {
				t.Fatalf("got %q, want %q (outcome %s)", got, want, c.Expect.Outcome)
			}
			ran++
		})
	}
	if ran != 18 {
		t.Fatalf("ran %d of 19 resolution cases, want 18", ran)
	}
}

// decideForVector runs Respond's own decision policy over a declared state,
// with every request authorized: the vectors fix resolution, and the
// requester gate has its own tests. It returns the refusal reason, or ""
// for an artifact.
func decideForVector(view evidencerequest.View, request []byte, now time.Time, mayIssue bool, uniform bool) string {
	check := readRequest(Message{Octets: request, MediaType: MediaTypeJSON})
	if check.reason != "" {
		return check.reason
	}
	g := gate{requester: "peer", policy: policyWith("peers"), refusalPolicy: RefusalPolicySpecific, cll: true}
	if uniform {
		g.refusalPolicy = RefusalPolicyUniform
	}
	p := g.responderPolicy(mayIssue)
	p.Authorize = nil
	d := evidencerequest.Decide(view, check.request, p, now)
	if d.NeedAnchor {
		return g.uniform(ReasonCoverageUnsatisfiable)
	}
	return d.Reason
}
