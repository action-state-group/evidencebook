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

func TestHistoryCardPositions(t *testing.T) {
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
		if got := HistoryCardPositions(c.n, c.w); !slices.Equal(got, c.want) {
			t.Errorf("P(%d, %d) = %v, want %v", c.n, c.w, got, c.want)
		}
	}
	for _, w := range []int{0, -1, 65} {
		if HistoryCardPositions(10, w) != nil {
			t.Errorf("window %d gave positions", w)
		}
	}
	// A million checkpoints, 20 positions of the log, plus the window.
	if got := len(HistoryCardPositions(1_000_000, 16)); got > 16+19+2 {
		t.Errorf("a million checkpoints list %d positions", got)
	}
	huge := HistoryCardPositions(1<<62, 3)
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
		if window != 3 || n != 20 || !slices.Equal(positions, HistoryCardPositions(n, 3)) {
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
		{"window zero", func(c map[string]any) { c["window"] = uint64(0) }, "window"},
		{"window 65", func(c map[string]any) { c["window"] = uint64(65) }, "window"},
		{"window text", func(c map[string]any) { c["window"] = "3" }, "window"},
		{"window changed", func(c map[string]any) { c["window"] = uint64(5) }, "P(n, window)"},
		{"no window", func(c map[string]any) { delete(c, "window") }, "members"},
		{"extra member", func(c map[string]any) { c["x"] = uint64(1) }, "members"},
		{"no history", func(c map[string]any) { c["history"] = []any{} }, "no history"},
		{"first not zero", func(c map[string]any) { entry(c, 0)["position"] = uint64(1) }, "strictly increase"},
		{"positions repeat", func(c map[string]any) { entry(c, 1)["position"] = uint64(0) }, "strictly increase"},
		{"position missing", func(c map[string]any) { c["history"] = slices.Delete(entries(c), 2, 3) }, "P(n, window)"},
		{"position added", func(c map[string]any) {
			extra := map[string]any{}
			for k, v := range entry(c, 1) {
				extra[k] = v
			}
			extra["position"] = uint64(5)
			c["history"] = slices.Insert(entries(c), 2, any(extra))
		}, "P(n, window)"},
		{"position negative", func(c map[string]any) { entry(c, 1)["position"] = evidencerequest.JSONNumber("-1") }, "unsigned"},
		{"first carries a proof", func(c map[string]any) {
			entry(c, 0)["consistency_from_previous"] = entry(c, 1)["consistency_from_previous"]
		}, "consistency proof"},
		{"later entry has no proof", func(c map[string]any) { entry(c, 2)["consistency_from_previous"] = nil }, "consistency proof"},
		{"proof from another pair", func(c map[string]any) {
			entry(c, 3)["consistency_from_previous"] = entry(c, 2)["consistency_from_previous"]
		}, ""},
		{"checkpoints swapped", func(c map[string]any) {
			a, b := entry(c, 1)["checkpoint"], entry(c, 2)["checkpoint"]
			entry(c, 1)["checkpoint"], entry(c, 2)["checkpoint"] = b, a
		}, ""},
		{"receipts not a list", func(c map[string]any) { entry(c, 1)["receipts"] = "x" }, "receipts"},
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
