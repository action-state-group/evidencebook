// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// strangerTransport is a node's own public path: Respond with no
// authenticated requester.
func strangerTransport(book *Book, policy SharePolicy) DoctorTransport {
	return func(ctx context.Context, request Message) (Message, error) {
		reply, record, err := book.Respond(ctx, request, RespondOptions{Policy: policy})
		if err != nil {
			return Message{}, err
		}
		if record.RecordID != "" {
			return Message{}, errors.New("a stranger's request was sealed")
		}
		return reply.Message, nil
	}
}

func findingOf(t *testing.T, findings []DoctorFinding, check string) DoctorFinding {
	t.Helper()
	for _, f := range findings {
		if f.Check == check {
			return f
		}
	}
	t.Fatalf("no %s finding in %+v", check, findings)
	return DoctorFinding{}
}

func checkpointedBook(t *testing.T, id string, seed byte) *Book {
	t.Helper()
	book := newFixture(t, id, seed).open(t)
	mustAppend(t, book, observation("deal-1", "a"))
	mustAppend(t, book, observation("deal-2", "b"))
	if _, err := book.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	return book
}

func TestDoctorHealthyNodeSealsNothing(t *testing.T) {
	ctx := context.Background()
	book := checkpointedBook(t, "healthy", 1)
	policy := DefaultSharePolicy()
	before, err := book.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	size := book.Size()

	findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: strangerTransport(book, policy)})
	if err != nil {
		t.Fatal(err)
	}
	after, err := book.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if book.Size() != size || len(after) != len(before) {
		t.Fatalf("doctor --probe grew the book: records %d -> %d, checkpoints %d -> %d", size, book.Size(), len(before), len(after))
	}

	var checks []string
	for _, f := range findings {
		checks = append(checks, f.Check)
	}
	want := []string{DoctorSelfAttested, DoctorControllerBinding, DoctorCheckpoint, DoctorWitnessed, DoctorNotChecked, DoctorUnsupported, DoctorSharing, DoctorPolicyCommitment, DoctorStrangerView, DoctorIdentifiedView, DoctorProbeCheckpoints, DoctorProbeHistoryCard}
	if !slices.Equal(checks, want) {
		t.Fatalf("checks = %v, want %v", checks, want)
	}
	anchor := anchorDigest(before[len(before)-1])
	for _, check := range []string{DoctorProbeCheckpoints, DoctorProbeHistoryCard} {
		if f := findingOf(t, findings, check); f.State != DoctorStateMatch || !slices.Equal(f.Records, []string{anchor}) {
			t.Errorf("%s = %+v, want a match at the latest checkpoint", check, f)
		}
	}
	expect := map[string]string{
		DoctorSelfAttested: DoctorStateSelfAttested, DoctorControllerBinding: DoctorStateUnbound,
		DoctorCheckpoint: DoctorStatePresent, DoctorWitnessed: DoctorStateUnwitnessed,
		DoctorSharing: DoctorStateOn, DoctorPolicyCommitment: DoctorStateNotChecked,
	}
	for check, state := range expect {
		if f := findingOf(t, findings, check); f.State != state {
			t.Errorf("%s = %+v, want state %s", check, f, state)
		}
	}
	if f := findingOf(t, findings, DoctorIdentifiedView); !strings.Contains(f.Detail, "names it as counterparty") {
		t.Errorf("identified view under the default policy = %q", f.Detail)
	}

	// A list, never a score: every finding has exactly these members.
	encoded, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, f := range decoded {
		for member := range f {
			if !slices.Contains([]string{"check", "state", "detail", "records"}, member) {
				t.Errorf("finding member %q", member)
			}
		}
	}
}

func TestDoctorSharingOff(t *testing.T) {
	ctx := context.Background()
	book := checkpointedBook(t, "off", 2)
	policy := SharePolicy{RecordAtCompletion: "off", HistorySegments: "off", Adjudications: "off"}
	findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: strangerTransport(book, policy)})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorSharing); f.State != DoctorStateOff || !strings.Contains(f.Detail, "history_segments=off") {
		t.Errorf("sharing = %+v", f)
	}
	if f := findingOf(t, findings, DoctorIdentifiedView); !strings.Contains(f.Detail, "history_segments=off") {
		t.Errorf("identified view = %+v", f)
	}
	// Commitments stay public with sharing off.
	if f := findingOf(t, findings, DoctorProbeCheckpoints); f.State != DoctorStateMatch {
		t.Errorf("probe = %+v", f)
	}

	// history_segments=peers alone turns sharing on and widens the
	// identified view.
	peers := policy
	peers.HistorySegments = "peers"
	findings, err = book.Doctor(ctx, DoctorOptions{Policy: peers})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorSharing); f.State != DoctorStateOn {
		t.Errorf("sharing = %+v", f)
	}
	if f := findingOf(t, findings, DoctorIdentifiedView); !strings.Contains(f.Detail, "by digest") {
		t.Errorf("identified view = %+v", f)
	}
}

func TestDoctorServedAnswerDisagreesWithPolicy(t *testing.T) {
	ctx := context.Background()
	policy := DefaultSharePolicy()

	t.Run("another node answers", func(t *testing.T) {
		book := checkpointedBook(t, "mine", 3)
		other := checkpointedBook(t, "other", 4)
		findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: strangerTransport(other, policy)})
		if err != nil {
			t.Fatal(err)
		}
		f := findingOf(t, findings, DoctorProbeCheckpoints)
		if f.State != DoctorStateMismatch || !strings.Contains(f.Detail, "advertised responder key") {
			t.Errorf("probe = %+v", f)
		}
	})

	t.Run("the responder declines", func(t *testing.T) {
		f := newFixture(t, "declines", 5)
		cfg := f.config(t)
		cfg.Responder = ResponderPolicy{Limiter: NewWindowLimiter(0, time.Hour)}
		book, err := Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = book.Release() })
		mustAppend(t, book, observation("deal-1", "a"))
		if _, err := book.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
		findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: strangerTransport(book, policy)})
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range []string{DoctorProbeCheckpoints, DoctorProbeHistoryCard} {
			if got := findingOf(t, findings, check); got.State != DoctorStateMismatch || !strings.Contains(got.Detail, "refused policy_declined") {
				t.Errorf("%s = %+v", check, got)
			}
		}
	})

	// With no checkpoint the refusal is what the stranger view predicts:
	// the probe checked nothing, which is not a mismatch.
	t.Run("no checkpoint", func(t *testing.T) {
		book := newFixture(t, "unanchored", 6).open(t)
		mustAppend(t, book, observation("deal-1", "a"))
		findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: strangerTransport(book, policy)})
		if err != nil {
			t.Fatal(err)
		}
		if got := findingOf(t, findings, DoctorCheckpoint); got.State != DoctorStateMissing {
			t.Errorf("checkpoint = %+v", got)
		}
		if got := findingOf(t, findings, DoctorProbeCheckpoints); got.State != DoctorStateNotChecked || !strings.Contains(got.Detail, "refused coverage_unsatisfiable: there is no checkpoint") {
			t.Errorf("probe = %+v", got)
		}
		if cps, _ := book.Checkpoints(ctx); len(cps) != 0 {
			t.Errorf("the probe issued %d checkpoints", len(cps))
		}
	})

	t.Run("a nonconforming refusal with no checkpoint", func(t *testing.T) {
		book := newFixture(t, "nonconforming", 13).open(t)
		mustAppend(t, book, observation("deal-1", "a"))
		made := func(ctx context.Context, request Message) (Message, error) {
			refusal, err := Refusal{RequestDigest: digestBytes(request.Octets), Reason: "made_up", IssuedAt: "2026-10-27T00:00:00Z", KeyID: KeyID(book.signer)}.Sign(ctx, book.signer)
			if err != nil {
				return Message{}, err
			}
			reply, err := evidencerequest.EncodeReply(request.MediaType, nil, &refusal)
			return reply.Message, err
		}
		findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: made})
		if err != nil {
			t.Fatal(err)
		}
		if got := findingOf(t, findings, DoctorProbeCheckpoints); got.State != DoctorStateMismatch || !strings.Contains(got.Detail, "made_up") {
			t.Errorf("probe = %+v", got)
		}
	})

	t.Run("the transport fails", func(t *testing.T) {
		book := checkpointedBook(t, "down", 7)
		down := func(context.Context, Message) (Message, error) { return Message{}, errors.New("connection refused") }
		findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: down})
		if err != nil {
			t.Fatal(err)
		}
		if got := findingOf(t, findings, DoctorProbeHistoryCard); got.State != DoctorStateMismatch || !strings.Contains(got.Detail, "connection refused") {
			t.Errorf("probe = %+v", got)
		}
	})
}

func TestDoctorControllerBindingAndWitness(t *testing.T) {
	ctx := context.Background()
	book := checkpointedBook(t, "bound", 8)
	signer := KeyID(book.signer)
	binding := json.RawMessage(`{"binding_version":"controller-binding/0","claim":"same_controller_asserted","keys":["` + signer + `","ff"],"nonce":"n","sigs":[]}`)
	unrelated := json.RawMessage(`{"binding_version":"controller-binding/0","keys":["aa","bb"]}`)
	cps, err := book.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest := cps[len(cps)-1]

	findings, err := book.Doctor(ctx, DoctorOptions{
		Policy: DefaultSharePolicy(), ControllerBindings: []json.RawMessage{unrelated, binding, json.RawMessage(`not json`)},
		Witnesses: []DoctorWitness{{Witness: "w1", TreeSize: latest.TreeSize, Receipt: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := findingOf(t, findings, DoctorControllerBinding)
	if f.State != DoctorStateNotChecked || len(f.Records) != 1 || len(f.Records[0]) != 64 {
		t.Errorf("controller binding = %+v", f)
	}
	for _, word := range []string{"registry", "KYB", "Authority", "vouch"} {
		if strings.Contains(f.Detail, word) {
			t.Errorf("controller binding says who vouched: %q", f.Detail)
		}
	}
	if w := findingOf(t, findings, DoctorWitnessed); w.State != DoctorStateWitnessed || !strings.Contains(w.Detail, "w1") {
		t.Errorf("witness = %+v", w)
	}

	// A receipt for an older checkpoint leaves the latest unwitnessed.
	findings, err = book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy(), Witnesses: []DoctorWitness{{Witness: "w1", TreeSize: latest.TreeSize - 1, Receipt: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if w := findingOf(t, findings, DoctorWitnessed); w.State != DoctorStateUnwitnessed {
		t.Errorf("witness = %+v", w)
	}
}

func TestDoctorRejectsAnInvalidPolicy(t *testing.T) {
	book := checkpointedBook(t, "invalid", 9)
	if _, err := book.Doctor(context.Background(), DoctorOptions{Policy: SharePolicy{HistorySegments: "everyone"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestDoctorListsReceivedNotChecked(t *testing.T) {
	ctx := context.Background()
	book := checkpointedBook(t, "asker", 10)
	findings, err := book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingOf(t, findings, DoctorNotChecked); f.State != DoctorStateNone || len(f.Records) != 0 {
		t.Errorf("not_checked with no answers = %+v", f)
	}
	statement, _ := json.Marshal(ResponseStatement{RequestDigest: strings.Repeat("a", 64), Outcome: OutcomeArtifact, NotChecked: []string{"anchor_inclusion"}})
	response := mustAppend(t, book, Entry{RecordType: RecordTypeResponse, EpistemicType: ObservedEvent, Statement: statement})
	if findings, err = book.Doctor(ctx, DoctorOptions{Policy: DefaultSharePolicy()}); err != nil {
		t.Fatal(err)
	}
	f := findingOf(t, findings, DoctorNotChecked)
	if f.State != DoctorStateNotChecked || !slices.Equal(f.Records, []string{response.RecordID}) || !strings.Contains(f.Detail, "anchor_inclusion") {
		t.Errorf("not_checked = %+v", f)
	}
	if c := findingOf(t, findings, DoctorCheckpoint); !strings.Contains(c.Detail, "rest are under no checkpoint") {
		t.Errorf("checkpoint = %+v", c)
	}
}

// A checkpoint cut between doctor's snapshot and the responder's answer is
// still this book's own: the probe matches.
func TestDoctorProbeAcceptsACheckpointCutMeanwhile(t *testing.T) {
	ctx := context.Background()
	policy := DefaultSharePolicy()
	for name, book := range map[string]*Book{
		"after a checkpoint": checkpointedBook(t, "busy", 11),
		// The book's first checkpoint, cut after doctor's snapshot.
		"the first checkpoint": newFixture(t, "first", 12).open(t),
	} {
		t.Run(name, func(t *testing.T) {
			answer := strangerTransport(book, policy)
			busy := func(ctx context.Context, request Message) (Message, error) {
				mustAppend(t, book, observation("deal-3", "c"))
				if _, err := book.Checkpoint(ctx); err != nil {
					return Message{}, err
				}
				return answer(ctx, request)
			}
			findings, err := book.Doctor(ctx, DoctorOptions{Policy: policy, Probe: busy})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []string{DoctorProbeCheckpoints, DoctorProbeHistoryCard} {
				if f := findingOf(t, findings, check); f.State != DoctorStateMatch {
					t.Errorf("%s = %+v", check, f)
				}
			}
		})
	}
}

// A book on a substrate that serves no anchors answers no one, so neither
// view promises records, whatever the policy.
func TestDoctorUnanchoredSubstrate(t *testing.T) {
	book := scittBook(t)
	mustAppend(t, book, observation("deal-1", "a"))
	findings, err := book.Doctor(context.Background(), DoctorOptions{Policy: policyWith("peers")})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{DoctorStrangerView, DoctorIdentifiedView} {
		if f := findingOf(t, findings, check); !strings.HasPrefix(f.Detail, "refusals only") {
			t.Errorf("%s = %+v", check, f)
		}
	}
	if f := findingOf(t, findings, DoctorCheckpoint); f.State != DoctorStateMissing || !strings.Contains(f.Detail, "not a Checkpointed Local Log") {
		t.Errorf("checkpoint = %+v", f)
	}
	if f := findingOf(t, findings, DoctorUnsupported); !strings.Contains(f.Detail, "serves no anchors") {
		t.Errorf("unsupported = %+v", f)
	}
}
