// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// ER -03 §8.1.2: history_card/2 lists P(n, W), at most W + floor(log2 n) + 2
// positions, whatever the number of checkpoints.

func TestHistoryPositions(t *testing.T) {
	cases := []struct {
		n    uint64
		w    int
		want []uint64
	}{
		{0, 1, []uint64{0}},
		{0, 64, []uint64{0}},
		{1, 1, []uint64{0, 1}},
		{5, 2, []uint64{0, 1, 3, 4, 5}},
		{10, 3, []uint64{0, 2, 6, 8, 9, 10}},
		{10, 64, []uint64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{16, 4, []uint64{0, 8, 12, 13, 14, 15, 16}},
	}
	for _, c := range cases {
		if got := positionsOf(c.n, c.w); !slices.Equal(got, c.want) {
			t.Errorf("P(%d, %d) = %v, want %v", c.n, c.w, got, c.want)
		}
	}
	// A million checkpoints, 20 positions of the log, plus the window.
	if got := len(positionsOf(1_000_000, 16)); got > 16+19+2 {
		t.Errorf("a million checkpoints list %d positions", got)
	}
	huge := positionsOf(1<<62, 3)
	if len(huge) > 3+62+2 || huge[len(huge)-1] != 1<<62 {
		t.Errorf("2^62 gives %d positions ending at %d", len(huge), huge[len(huge)-1])
	}
}

// cardBook is a book with n+1 checkpoints (positions 0 through n).
func cardBook(t *testing.T, window int, n int) *Book {
	t.Helper()
	book := standardHistoryBook(t, nil, func(c *Config) { c.Responder.HistoryWindow = window })
	ctx := context.Background()
	if _, err := book.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		mustAppend(t, book, observation("c", fmt.Sprint(i)))
		if _, err := book.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return book
}

func askCard2(t *testing.T, book *Book, kind SubjectKind) Reply {
	t.Helper()
	pin := anchorPin(t, book)
	return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: kind}, Derivation: DerivationHistoryCard2, Coverage: Coverage{ExpectedPin: pin}}), "")
}

func cardPositions(t *testing.T, artifact []byte) (uint64, []uint64) {
	t.Helper()
	var card struct {
		Window  uint64 `json:"window"`
		History []struct {
			Position uint64 `json:"position"`
		} `json:"history"`
	}
	if err := json.Unmarshal(artifact, &card); err != nil {
		t.Fatal(err)
	}
	var out []uint64
	for _, e := range card.History {
		out = append(out, e.Position)
	}
	return card.Window, out
}

func TestHistoryCard2IsAnsweredToStrangersAtP(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	for _, kind := range []SubjectKind{SubjectCheckpoints, SubjectFullHistory} {
		e := mustEnvelope(t, askCard2(t, book, kind))
		if e.Derivation != DerivationHistoryCard2 || e.Anchor.Digest != pin {
			t.Fatalf("%s: envelope %s at %s", kind, e.Derivation, e.Anchor.Digest)
		}
		window, positions := cardPositions(t, e.Artifact)
		n := positions[len(positions)-1]
		if window != 3 || n != 20 || !slices.Equal(positions, positionsOf(n, 3)) {
			t.Fatalf("%s: window %d positions %v", kind, window, positions)
		}
		if err := CheckHistoryCard2(e.Artifact, e.Anchor.Digest); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}

func TestHistoryCard2SizeIsLogarithmicAndBelowCardOne(t *testing.T) {
	book := cardBook(t, 4, 70)
	two := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	one := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}}), ""))
	_, positions := cardPositions(t, two.Artifact)
	if len(positions) > 4+6+2 {
		t.Fatalf("%d positions for 71 checkpoints", len(positions))
	}
	if len(two.Artifact) >= len(one.Artifact) {
		t.Fatalf("history_card/2 is %d bytes, history_card/1 is %d", len(two.Artifact), len(one.Artifact))
	}
}

// A book too busy for history_card/1 under its answer cap still serves /2.
func TestHistoryCard2IsServedWhereCardOneIsOverTheCap(t *testing.T) {
	book := standardHistoryBook(t, nil, func(c *Config) { c.Responder.MaxAnswerItems = 5 })
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		mustAppend(t, book, observation("busy", fmt.Sprint(i)))
		if _, err := book.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pin := anchorPin(t, book)
	ask := func(derivation string) Reply {
		return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: derivation, Coverage: Coverage{ExpectedPin: pin}}), "")
	}
	mustRefusal(t, ask(DerivationHistoryCard), ReasonPolicyDeclined)
	e := mustEnvelope(t, ask(DerivationHistoryCard2))
	if err := CheckHistoryCard2(e.Artifact, e.Anchor.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryCard2WithOneCheckpointListsPositionZero(t *testing.T) {
	book := cardBook(t, 16, 0)
	e := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	if _, positions := cardPositions(t, e.Artifact); !slices.Equal(positions, []uint64{0}) {
		t.Fatalf("positions %v", positions)
	}
	if err := CheckHistoryCard2(e.Artifact, e.Anchor.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryCard2RefusesAnotherSubject(t *testing.T) {
	book := cardBook(t, 4, 3)
	pin := anchorPin(t, book)
	reply := respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRange, Range: [2]uint64{0, 1}}, Derivation: DerivationHistoryCard2, Coverage: Coverage{ExpectedPin: pin}}), "")
	mustRefusal(t, reply, ReasonNoSuchSubject)
}

func TestResponderPolicyRefusesAHistoryWindowOutOfRange(t *testing.T) {
	for _, w := range []int{-1, 65, 1000} {
		if err := (ResponderPolicy{HistoryWindow: w}).validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("window %d: %v", w, err)
		}
	}
	for _, w := range []int{0, 1, 64} {
		if err := (ResponderPolicy{HistoryWindow: w}).validate(); err != nil {
			t.Errorf("window %d: %v", w, err)
		}
	}
}

// mutateCard2 rewrites a valid card's JSON with edit and checks it with
// CheckHistoryCard2, as a requester would.
func mutateCard2(t *testing.T, artifact []byte, anchor string, edit func(card map[string]any)) error {
	t.Helper()
	decoded, err := evidencerequest.DecodeJSON(artifact)
	if err != nil {
		t.Fatal(err)
	}
	card := decoded.(map[string]any)
	edit(card)
	out, err := evidencerequest.EncodeJCS(card)
	if err != nil {
		t.Fatal(err)
	}
	return CheckHistoryCard2(out, anchor)
}

func TestCheckHistoryCard2RefusesWhatIsNotTheCard(t *testing.T) {
	book := cardBook(t, 3, 20)
	e := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	entries := func(c map[string]any) []any { return c["history"].([]any) }
	entry := func(c map[string]any, i int) map[string]any { return entries(c)[i].(map[string]any) }
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"window zero", func(c map[string]any) { c["window"] = uint64(0) }, "window_out_of_range"},
		{"window 65", func(c map[string]any) { c["window"] = uint64(65) }, "window_out_of_range"},
		{"window text", func(c map[string]any) { c["window"] = "3" }, "card_malformed"},
		{"window changed", func(c map[string]any) { c["window"] = uint64(5) }, "position_missing"},
		{"no window", func(c map[string]any) { delete(c, "window") }, "members"},
		{"extra member", func(c map[string]any) { c["x"] = uint64(1) }, "members"},
		{"no history", func(c map[string]any) { c["history"] = []any{} }, "positions_not_increasing_from_zero"},
		{"first not zero", func(c map[string]any) { entry(c, 0)["position"] = uint64(1) }, "positions_not_increasing_from_zero"},
		{"positions repeat", func(c map[string]any) { entry(c, 1)["position"] = uint64(0) }, "positions_not_increasing_from_zero"},
		{"position missing", func(c map[string]any) { c["history"] = slices.Delete(entries(c), 2, 3) }, "position_missing"},
		{"position added", func(c map[string]any) {
			extra := map[string]any{}
			for k, v := range entry(c, 1) {
				extra[k] = v
			}
			extra["position"] = uint64(5)
			c["history"] = slices.Insert(entries(c), 2, any(extra))
		}, "position_unexpected"},
		{"position negative", func(c map[string]any) { entry(c, 1)["position"] = evidencerequest.JSONNumber("-1") }, "card_malformed"},
		{"first carries a proof", func(c map[string]any) {
			entry(c, 0)["consistency_from_previous"] = entry(c, 1)["consistency_from_previous"]
		}, "proof_invalid"},
		{"later entry has no proof", func(c map[string]any) { entry(c, 2)["consistency_from_previous"] = nil }, "proof_invalid"},
		{"proof from another pair", func(c map[string]any) {
			entry(c, 3)["consistency_from_previous"] = entry(c, 2)["consistency_from_previous"]
		}, ""},
		{"checkpoints swapped", func(c map[string]any) {
			a, b := entry(c, 1)["checkpoint"], entry(c, 2)["checkpoint"]
			entry(c, 1)["checkpoint"], entry(c, 2)["checkpoint"] = b, a
		}, ""},
		{"receipts not a list", func(c map[string]any) { entry(c, 1)["receipts"] = "x" }, "card_malformed"},
		{"last is not the anchor", func(c map[string]any) { c["history"] = entries(c)[:len(entries(c))-1] }, ""},
		{"wrong derivation", func(c map[string]any) { c["derivation"] = DerivationHistoryCard }, "derivation"},
	}
	for _, c := range cases {
		err := mutateCard2(t, e.Artifact, e.Anchor.Digest, c.edit)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
	}
	if err := CheckHistoryCard2(e.Artifact, digestBytes([]byte("another anchor"))); err == nil {
		t.Error("another anchor accepted")
	}
}

// The seven checks of §8.1.2 are ordered: a card broken twice names the first.
func TestCheckHistoryCard2NamesTheFirstFailedCheck(t *testing.T) {
	book := cardBook(t, 3, 20)
	e := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	err := mutateCard2(t, e.Artifact, e.Anchor.Digest, func(c map[string]any) {
		c["window"] = uint64(0)
		c["history"].([]any)[1].(map[string]any)["position"] = uint64(0)
	})
	if err == nil || !strings.Contains(err.Error(), "window") {
		t.Fatalf("%v", err)
	}
}

func TestStapleCarriesHistoryCard2(t *testing.T) {
	ctx := context.Background()
	book := cardBook(t, 3, 20)
	before := book.Size()
	staple, err := book.Staple(ctx, DerivationHistoryCard2)
	if err != nil {
		t.Fatal(err)
	}
	if book.Size() != before {
		t.Fatal("a history_card/2 staple grew the log")
	}
	got, err := CheckStaple(staple, KeyID(book.signer))
	if err != nil {
		t.Fatal(err)
	}
	if got.Derivation != DerivationHistoryCard2 {
		t.Fatalf("derivation %s", got.Derivation)
	}
	// The stapled card is the one a stranger asking for it gets.
	asked := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	if string(got.Artifact) != string(asked.Artifact) {
		t.Fatal("the stapled card is not the card a stranger gets")
	}
	// A staple with a broken card is refused by the check, not by the holder.
	staple.Reply = reply2Tampered(t, book, staple)
	if _, err := CheckStaple(staple, KeyID(book.signer)); err == nil {
		t.Fatal("a staple with a card missing a sample position passed")
	}
}

// reply2Tampered re-signs the staple's envelope over a card that drops one
// sampled entry, so only the card check can catch it.
func reply2Tampered(t *testing.T, book *Book, s Staple) Reply {
	t.Helper()
	parsed, err := ParseReply(s.Reply.Message)
	if err != nil || parsed.Envelope == nil {
		t.Fatal(err)
	}
	e := *parsed.Envelope
	decoded, err := evidencerequest.DecodeJSON(e.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	card := decoded.(map[string]any)
	history := card["history"].([]any)
	card["history"] = slices.Delete(history, 1, 2)
	if e.Artifact, err = evidencerequest.EncodeJCS(card); err != nil {
		t.Fatal(err)
	}
	e.ArtifactDigest = digestBytes(e.Artifact)
	if e, err = e.Sign(context.Background(), book.signer); err != nil {
		t.Fatal(err)
	}
	reply, err := evidencerequest.EncodeReply(MediaTypeJSON, &e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Reply{Message: reply.Message, Envelope: &e}
}

func positionsOf(n uint64, w int) []uint64 { return evidencerequest.HistoryPositions(n, uint64(w)) }

// recordFor has a fresh requester ask responder for req, as a stranger would,
// and returns the outcome the requester recorded.
func recordFor(t *testing.T, responder *Book, req EvidenceRequest) ResponseStatement {
	t.Helper()
	ctx := context.Background()
	requester := newFixture(t, "requester-hc2", 31).open(t)
	sent, err := requester.Request(ctx, req, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	reply := respondAs(t, responder, sent.Message(), "")
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 9))
	if err != nil {
		t.Fatal(err)
	}
	var statement ResponseStatement
	if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	return statement
}

func TestARequesterVerifiesHistoryCard2(t *testing.T) {
	book := cardBook(t, 3, 20)
	got := recordFor(t, book, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard2, Coverage: Coverage{ExpectedPin: anchorPin(t, book)}})
	if got.Outcome != OutcomeArtifact {
		t.Fatalf("recorded %s: %s", got.Outcome, got.Failure)
	}
}

// ER -03 §8.1.3: the checkpoints at positions a through b.
func TestARangedCheckpointsRequestIsAnsweredAndVerified(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	ask := func(a, b uint64) EvidenceRequest {
		return EvidenceRequest{Subject: Subject{Kind: SubjectCheckpointRange, Range: [2]uint64{a, b}}, Coverage: Coverage{ExpectedPin: pin}}
	}
	for _, r := range [][2]uint64{{4, 12}, {0, 20}, {7, 7}} {
		reply := respondAs(t, book, jsonRequest(t, ask(r[0], r[1])), "")
		e := mustEnvelope(t, reply)
		var answer struct {
			History []struct {
				Position uint64 `json:"position"`
				Proof    any    `json:"consistency_from_previous"`
			} `json:"history"`
		}
		if err := json.Unmarshal(e.Artifact, &answer); err != nil {
			t.Fatal(err)
		}
		if uint64(len(answer.History)) != r[1]-r[0]+1 || answer.History[0].Position != r[0] || answer.History[0].Proof != nil {
			t.Fatalf("range %v: %d entries", r, len(answer.History))
		}
		var tail struct {
			ToAnchor any `json:"consistency_to_anchor"`
		}
		if err := json.Unmarshal(e.Artifact, &tail); err != nil || (tail.ToAnchor == nil) != (r[1] == 20) {
			t.Fatalf("range %v: consistency_to_anchor %v (null only when b is the anchor's position)", r, tail.ToAnchor)
		}
		if got := recordFor(t, book, ask(r[0], r[1])); got.Outcome != OutcomeArtifact {
			t.Fatalf("range %v recorded %s: %s", r, got.Outcome, got.Failure)
		}
	}
	// The entries are the ones the card lists at those positions.
	card := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	var listed struct {
		History []struct {
			Position   uint64 `json:"position"`
			Checkpoint string `json:"checkpoint"`
		} `json:"history"`
	}
	if err := json.Unmarshal(card.Artifact, &listed); err != nil {
		t.Fatal(err)
	}
	ranged := mustEnvelope(t, respondAs(t, book, jsonRequest(t, ask(4, 12)), ""))
	var got struct {
		History []struct {
			Position   uint64 `json:"position"`
			Checkpoint string `json:"checkpoint"`
		} `json:"history"`
	}
	if err := json.Unmarshal(ranged.Artifact, &got); err != nil {
		t.Fatal(err)
	}
	for _, c := range listed.History {
		if c.Position >= 4 && c.Position <= 12 && got.History[c.Position-4].Checkpoint != c.Checkpoint {
			t.Fatalf("position %d differs from the card's", c.Position)
		}
	}
}

func TestARangedCheckpointsRequestIsRefusedByReason(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	ranged := func(a, b uint64, derivation string) Reply {
		return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpointRange, Range: [2]uint64{a, b}}, Derivation: derivation, Coverage: Coverage{ExpectedPin: pin}}), "")
	}
	mustRefusal(t, ranged(5, 21, ""), ReasonCoverageUnsatisfiable)
	mustRefusal(t, ranged(5, 5000, ""), ReasonCoverageUnsatisfiable)
	mustRefusal(t, ranged(1, 2, DerivationHistoryCard), ReasonNoSuchSubject)
	mustRefusal(t, ranged(1, 2, DerivationHistoryCard2), ReasonNoSuchSubject)
	// a > b is a malformed request, which the request reader refuses.
	bad := []byte(`{"coverage":{"expected_pin":"` + pin + `"},"nonce":"n","profile":"evidence-interop/0","subject":{"checkpoint_range":[5,2]}}`)
	reply, _, err := book.Respond(context.Background(), Message{Octets: bad, MediaType: MediaTypeJSON}, RespondOptions{Policy: policyWith("peers")})
	if err != nil {
		t.Fatal(err)
	}
	mustRefusal(t, reply, ReasonRequestMalformed)
}

func TestARangedCheckpointsRequestOverTheCapIsDeclined(t *testing.T) {
	book := standardHistoryBook(t, nil, func(c *Config) { c.Responder.MaxAnswerItems = 3 })
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		mustAppend(t, book, observation("cap", fmt.Sprint(i)))
		if _, err := book.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pin := anchorPin(t, book)
	ask := func(a, b uint64) Reply {
		return respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpointRange, Range: [2]uint64{a, b}}, Coverage: Coverage{ExpectedPin: pin}}), "")
	}
	mustEnvelope(t, ask(2, 4))
	mustRefusal(t, ask(0, 7), ReasonPolicyDeclined)
}

// A ranged answer whose consistency_to_anchor is dropped, or taken from
// another range, is not verified (ER -03 §8.1.3).
func TestARangedAnswerMustReachItsAnchor(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	req := EvidenceRequest{Subject: Subject{Kind: SubjectCheckpointRange, Range: [2]uint64{4, 12}}, Coverage: Coverage{ExpectedPin: pin}}
	e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, req), ""))
	other := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpointRange, Range: [2]uint64{4, 14}}, Coverage: Coverage{ExpectedPin: pin}}), ""))
	key := responderKeys(book, 9).Checkpoint
	if _, err := verifyCheckpointRange(req, e, key); err != nil {
		t.Fatal(err)
	}
	decode := func(a []byte) map[string]any {
		v, err := evidencerequest.DecodeJSON(a)
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)
	}
	reencode := func(m map[string]any) Envelope {
		out := e
		var err error
		if out.Artifact, err = evidencerequest.EncodeJCS(m); err != nil {
			t.Fatal(err)
		}
		return out
	}
	dropped := decode(e.Artifact)
	dropped["consistency_to_anchor"] = nil
	if _, err := verifyCheckpointRange(req, reencode(dropped), key); err == nil {
		t.Error("an answer ending before its anchor with a null proof passed")
	}
	swapped := decode(e.Artifact)
	swapped["consistency_to_anchor"] = decode(other.Artifact)["consistency_to_anchor"]
	if _, err := verifyCheckpointRange(req, reencode(swapped), key); err == nil {
		t.Error("another range's consistency_to_anchor passed")
	}
	bare := e
	bare.Verification, bare.VerificationContent = nil, nil
	if _, err := verifyCheckpointRange(req, bare, key); err == nil {
		t.Error("an answer without the anchor checkpoint passed")
	}
}

// A receipt this requester cannot check is refused, and a stated freshness
// the anchor's checkpoint does not commit is refused (ER -03 §8.1.3).
func TestAHistoryCard2OrRangeAnswerCannotMisstateFreshnessOrCarryUnreadReceipts(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	key := responderKeys(book, 9).Checkpoint
	req := EvidenceRequest{Subject: Subject{Kind: SubjectCheckpointRange, Range: [2]uint64{4, 12}}, Coverage: Coverage{ExpectedPin: pin}}
	e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, req), ""))
	card := mustEnvelope(t, askCard2(t, book, SubjectCheckpoints))
	lie := func(e Envelope) Envelope {
		size := uint64(1 << 40)
		e.Anchor.FreshnessSize = &size
		return e
	}
	if _, err := verifyCheckpointRange(req, lie(e), key); err == nil {
		t.Error("a range answer stating a size its anchor does not commit passed")
	}
	cardReq := EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard2, Coverage: Coverage{ExpectedPin: pin}}
	if _, _, err := verifyAnchorInclusion(cardReq, lie(card), key); err == nil {
		t.Error("a card stating a size its anchor does not commit passed")
	}
	if _, _, err := verifyAnchorInclusion(cardReq, card, key); err != nil {
		t.Errorf("the honest card: %v", err)
	}
	decoded, err := evidencerequest.DecodeJSON(e.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	m := decoded.(map[string]any)
	m["history"].([]any)[1].(map[string]any)["receipts"] = []any{map[string]any{"checkpoint": "00", "witness": "w", "sig": "00"}}
	withReceipt := e
	if withReceipt.Artifact, err = evidencerequest.EncodeJCS(m); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCheckpointRange(req, withReceipt, key); err == nil {
		t.Error("a range answer with a receipt this requester cannot read passed")
	}
}

// The history_card/1 and unranged checkpoints answers bind a stated freshness
// to the anchor's signed checkpoint too (#521): a size or a time the checkpoint
// does not commit is refused, so a stale chain cannot meet a min_freshness floor.
func TestACardOneOrCheckpointsAnswerCannotMisstateFreshness(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	key := responderKeys(book, 9).Checkpoint
	for name, req := range map[string]EvidenceRequest{
		"history_card/1": {Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: Coverage{ExpectedPin: pin}},
		"checkpoints":    {Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{ExpectedPin: pin}},
	} {
		e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, req), ""))
		if _, _, err := verifyAnchorInclusion(req, e, key); err != nil {
			t.Fatalf("%s, honest: %v", name, err)
		}
		size := uint64(1 << 40)
		bigger := e
		bigger.Anchor.FreshnessSize = &size
		if _, _, err := verifyAnchorInclusion(req, bigger, key); err == nil || !strings.Contains(err.Error(), "anchor.freshness") {
			t.Errorf("%s: an answer stating a size its anchor does not commit passed", name)
		}
		later := e
		later.Anchor.FreshnessSize, later.Anchor.FreshnessTime = nil, "2099-01-01T00:00:00Z"
		if _, _, err := verifyAnchorInclusion(req, later, key); err == nil || !strings.Contains(err.Error(), "anchor.freshness") {
			t.Errorf("%s: an answer stating a time its anchor does not commit passed", name)
		}
	}
}

// Evidence Request -03: the ranged form is checkpoint_range on the wire, a
// range on checkpoints is malformed, and an answered statement records the
// form as checkpoints (the Rust book does the same).
func TestCheckpointRangeWireFormAndStatementKind(t *testing.T) {
	book := cardBook(t, 3, 20)
	pin := anchorPin(t, book)
	wire := func(subject string) Message {
		return Message{Octets: []byte(`{"coverage":{"expected_pin":"` + pin + `"},"nonce":"n","profile":"evidence-interop/0","subject":` + subject + `}`), MediaType: MediaTypeJSON}
	}
	reply, statement := respondWith(t, book, wire(`{"checkpoint_range":[4,12]}`), RespondOptions{AuthenticatedRequester: "peer-1", Policy: policyWith("peers")})
	mustEnvelope(t, reply)
	if statement.SubjectKind != SubjectCheckpoints {
		t.Fatalf("statement subject kind %q, want %q", statement.SubjectKind, SubjectCheckpoints)
	}
	for _, old := range []string{`{"checkpoints":[4,12]}`, `{"checkpoint_range":null}`, `{"checkpoint_range":[1]}`} {
		mustRefusal(t, respondAs(t, book, wire(old), ""), ReasonRequestMalformed)
	}
}
