// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// evidencebook#275: a staple carries history_card/1 or standard_history/1,
// presented by its holder, as of its newest checkpoint, with a fresh nonce,
// and never more than the standard card.

func TestStapleCarriesTheStandardCardAsOfTheNewestCheckpoint(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	holder := KeyID(book.signer)
	pin := anchorPin(t, book)
	before := book.Size()
	staple, err := book.Staple(ctx, DerivationStandardHistory)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CheckStaple(staple, holder)
	if err != nil {
		t.Fatal(err)
	}
	if got.Derivation != DerivationStandardHistory || got.Anchor != pin || got.Nonce == "" || got.IssuedAt == "" {
		t.Fatalf("staple %+v, pin %s", got, pin)
	}
	// The card is the one a signed requester asking for it gets.
	asked := mustEnvelope(t, askStandardHistory(t, book, "carol", "off", pin))
	if !bytes.Equal(got.Artifact, asked.Artifact) {
		t.Fatal("the stapled card is not the standard card a signed asker gets")
	}
	// The holder's book records that it attached its standard card.
	records, err := book.Query(ctx, Filter{RecordType: RecordTypeRequestAnswered})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || book.Size() <= before {
		t.Fatal("stapling the standard card left no evidence_request_answered record")
	}
	// Each staple has its own nonce.
	again, err := book.Staple(ctx, DerivationStandardHistory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CheckStaple(again, holder)
	if err != nil {
		t.Fatal(err)
	}
	if second.Nonce == got.Nonce {
		t.Fatal("two staples share a nonce")
	}
}

func TestStapleCarriesTheHistoryCardWithoutGrowingTheLog(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, nil, nil)
	if _, err := book.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	before := book.Size()
	staple, err := book.Staple(ctx, DerivationHistoryCard)
	if err != nil {
		t.Fatal(err)
	}
	if book.Size() != before {
		t.Fatal("a history_card/1 staple grew the log")
	}
	got, err := CheckStaple(staple, KeyID(book.signer))
	if err != nil {
		t.Fatal(err)
	}
	if got.Derivation != DerivationHistoryCard {
		t.Fatalf("derivation %s", got.Derivation)
	}
}

func TestStapleIsCappedAtTheStandardCard(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "node", Inputs: dealsOf(nil)}, nil)
	for _, derivation := range []string{"", "served_summary/1", "exchange_corroboration/1", "sha256:" + digestBytes([]byte("holder selected"))} {
		if _, err := book.Staple(ctx, derivation); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q stapled: %v", derivation, err)
		}
	}
}

func TestStapleNeedsTheStandardHistorySource(t *testing.T) {
	book := standardHistoryBook(t, nil, nil)
	if _, err := book.Staple(context.Background(), DerivationStandardHistory); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stapled standard_history/1 with no source: %v", err)
	}
}

func TestCheckStapleRefusesWhatIsNotTheHoldersStaple(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	holder := KeyID(book.signer)
	staple, err := book.Staple(ctx, DerivationStandardHistory)
	if err != nil {
		t.Fatal(err)
	}
	other := standardHistoryBookSeed(t, 33)
	foreign, err := other.Staple(ctx, DerivationStandardHistory)
	if err != nil {
		t.Fatal(err)
	}
	pin := anchorPin(t, book)
	// A full_history request with no derivation is answered with more than a
	// card; a staple never carries it.
	wide := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: Coverage{ExpectedPin: pin}, Nonce: "aa"})
	reworded := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}, Nonce: "bb"})
	unpinned := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{MinFreshness: &Freshness{Size: new(uint64)}}, Nonce: "cc"})
	refusal := askStandardHistory(t, book, "", "peers", pin)
	if _, err := CheckStaple(staple, holder); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Staple{
		"another holder":      {Request: foreign.Request, Reply: foreign.Reply},
		"another request":     {Request: reworded, Reply: staple.Reply},
		"more than the card":  {Request: wide, Reply: staple.Reply},
		"an unpinned request": {Request: unpinned, Reply: staple.Reply},
		"a refusal":           {Request: staple.Request, Reply: refusal},
		"a tampered envelope": {Request: staple.Request, Reply: tamperedReply(t, staple.Reply)},
		"a CBOR request":      {Request: Message{Octets: staple.Request.Octets, MediaType: MediaTypeCBOR}, Reply: staple.Reply},
		"a malformed request": {Request: Message{Octets: []byte(`{}`), MediaType: MediaTypeJSON}, Reply: staple.Reply},
	}
	for name, s := range cases {
		if _, err := CheckStaple(s, holder); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	if _, err := CheckStaple(foreign, KeyID(other.signer)); err != nil {
		t.Fatalf("the other holder's own staple: %v", err)
	}
}

func standardHistoryBookSeed(t *testing.T, seed byte) *Book {
	t.Helper()
	f := newFixture(t, "book-sh-other", seed)
	c := f.config(t)
	c.StandardHistory = &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}
	book, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent})
	return book
}

// tamperedReply is reply with its artifact changed after signing.
func tamperedReply(t *testing.T, reply Reply) Reply {
	t.Helper()
	e := *reply.Envelope
	e.Artifact = append(bytes.Clone(e.Artifact[:len(e.Artifact)-1]), ' ')
	out, err := evidencerequest.EncodeReply(MediaTypeJSON, &e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Reply(out)
}

// stapleVector is one case of testdata/staple/staples.json, which the Rust
// and Python checkers read too: a staple and whether CheckStaple passes it.
type stapleVector struct {
	Name    string `json:"name"`
	Holder  string `json:"holder"`
	Request string `json:"request"`
	Reply   string `json:"reply"`
	Pass    bool   `json:"pass"`
	// Nonce and Anchor are what a passing staple reads as.
	Nonce  string `json:"nonce,omitempty"`
	Anchor string `json:"anchor,omitempty"`
}

const stapleVectorsPath = "testdata/staple/staples.json"

// TestStapleVectors checks the shared vectors. EB_WRITE_STAPLE_VECTORS=1
// writes them anew, for an intended change only: a staple carries a fresh
// nonce, so every write changes the file.
func TestStapleVectors(t *testing.T) {
	if os.Getenv("EB_WRITE_STAPLE_VECTORS") == "1" {
		writeStapleVectors(t)
	}
	raw, err := os.ReadFile(stapleVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var vectors []stapleVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 2 {
		t.Fatal("no staple vectors")
	}
	for _, v := range vectors {
		got, err := CheckStaple(Staple{Request: Message{Octets: []byte(v.Request), MediaType: MediaTypeJSON},
			Reply: Reply{Message: Message{Octets: []byte(v.Reply), MediaType: MediaTypeJSON}}}, v.Holder)
		if (err == nil) != v.Pass {
			t.Errorf("%s: pass %v, got %v", v.Name, v.Pass, err)
		}
		if err == nil && (got.Nonce != v.Nonce || got.Anchor != v.Anchor) {
			t.Errorf("%s: read %s at %s", v.Name, got.Nonce, got.Anchor)
		}
	}
}

func writeStapleVectors(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	holder := KeyID(book.signer)
	standard, err := book.Staple(ctx, DerivationStandardHistory)
	if err != nil {
		t.Fatal(err)
	}
	card, err := book.Staple(ctx, DerivationHistoryCard)
	if err != nil {
		t.Fatal(err)
	}
	other := standardHistoryBookSeed(t, 33)
	read := func(s Staple) StapleCheck {
		got, err := CheckStaple(s, holder)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	sc, cc := read(standard), read(card)
	wide := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: Coverage{ExpectedPin: sc.Anchor}, Nonce: sc.Nonce, Profile: ProfileID})
	reworded := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: sc.Anchor}, Nonce: flipped(sc.Nonce), Profile: ProfileID})
	vector := func(name string, request Message, reply Reply, who string, pass bool, c StapleCheck) stapleVector {
		v := stapleVector{Name: name, Holder: who, Request: string(request.Octets), Reply: string(reply.Octets), Pass: pass}
		if pass {
			v.Nonce, v.Anchor = c.Nonce, c.Anchor
		}
		return v
	}
	vectors := []stapleVector{
		vector("standard_history/1", standard.Request, standard.Reply, holder, true, sc),
		vector("history_card/1", card.Request, card.Reply, holder, true, cc),
		vector("another holder", standard.Request, standard.Reply, KeyID(other.signer), false, sc),
		vector("another request", reworded, standard.Reply, holder, false, sc),
		vector("more than the card", wide, standard.Reply, holder, false, sc),
		vector("a reply to another derivation", card.Request, standard.Reply, holder, false, sc),
		vector("a tampered envelope", standard.Request, tamperedReply(t, standard.Reply), holder, false, sc),
		vector("a card with more than the standard card", standard.Request, widenedCard(t, book, standard.Reply), holder, false, sc),
		emptyNonce(t, book, standard, holder),
		vector("a history card with more than the card", card.Request, widened(t, book, card.Reply, func(c map[string]any) {
			c["records"] = []any{"a record body"}
		}), holder, false, cc),
		vector("a history card that does not end at its anchor", card.Request, widened(t, book, card.Reply, func(c map[string]any) {
			h := c["history"].([]any)
			c["history"] = h[:len(h)-1]
		}), holder, false, cc),
		vector("a history card whose first entry has a proof", card.Request, widened(t, book, card.Reply, func(c map[string]any) {
			c["history"].([]any)[0].(map[string]any)["consistency_from_previous"] = "00"
		}), holder, false, cc),
		vector("a history card with uppercase hex", card.Request, widened(t, book, card.Reply, func(c map[string]any) {
			e := c["history"].([]any)[0].(map[string]any)
			e["checkpoint"] = strings.ToUpper(e["checkpoint"].(string))
		}), holder, false, cc),
		vector("a history card with no history", card.Request, widened(t, book, card.Reply, func(c map[string]any) { c["history"] = []any{} }), holder, false, cc),
		vector("a history card over another subject", card.Request, widened(t, book, card.Reply, func(c map[string]any) {
			c["subject"] = map[string]any{"full_history": nil}
		}), holder, false, cc),
		duplicateMember(t, book, card, holder),
		vector("a history card entry with more than a checkpoint", card.Request, widened(t, book, card.Reply, func(c map[string]any) {
			c["history"].([]any)[0].(map[string]any)["record"] = "a record body"
		}), holder, false, cc),
		vector("an envelope under another profile", standard.Request, resigned(t, book, standard.Reply, func(e *Envelope) { e.Profile = ProfileIDv1 }), holder, false, sc),
	}
	out, err := json.MarshalIndent(vectors, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stapleVectorsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stapleVectorsPath, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resigned is reply with its envelope changed by change and re-signed by
// the holder: a staple its own holder made wrong.
func resigned(t *testing.T, book *Book, reply Reply, change func(*Envelope)) Reply {
	t.Helper()
	e := *reply.Envelope
	change(&e)
	e, err := e.Sign(context.Background(), book.signer)
	if err != nil {
		t.Fatal(err)
	}
	out, err := evidencerequest.EncodeReply(MediaTypeJSON, &e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Reply(out)
}

// widenedCard is reply with a member the standard card does not have added
// to its card: a staple carrying more than the standard card, under the
// holder's own key.
func widenedCard(t *testing.T, book *Book, reply Reply) Reply {
	t.Helper()
	return widened(t, book, reply, func(card map[string]any) { card["counterparties"] = []any{"carol"} })
}

// widened is reply with its card changed by widen and re-signed by the
// holder.
func widened(t *testing.T, book *Book, reply Reply, widen func(map[string]any)) Reply {
	t.Helper()
	return resigned(t, book, reply, func(e *Envelope) {
		var card map[string]any
		if err := json.Unmarshal(e.Artifact, &card); err != nil {
			t.Fatal(err)
		}
		widen(card)
		artifact, err := canonicalJSON(card)
		if err != nil {
			t.Fatal(err)
		}
		e.Artifact, e.ArtifactDigest = artifact, digestBytes(artifact)
	})
}

// flipped is nonce with its first character changed, so it always differs.
func flipped(nonce string) string {
	first := "0"
	if nonce[0] == '0' {
		first = "1"
	}
	return first + nonce[1:]
}

// The holder's own staple is not counted by the book's limiter: strangers
// that used up the shared bucket cannot stop the holder stapling.
func TestStapleIsNotCountedByTheLimiter(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, func(c *Config) {
		c.Responder = ResponderPolicy{Limiter: NewWindowLimiter(1, time.Hour)}
	})
	pin := anchorPin(t, book)
	stranger := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: Coverage{ExpectedPin: pin}})
	mustEnvelope(t, respondAs(t, book, stranger, ""))
	if reply := respondAs(t, book, stranger, ""); reply.Refusal == nil {
		t.Fatal("the limiter let a second stranger through")
	}
	for _, derivation := range []string{DerivationHistoryCard, DerivationStandardHistory, DerivationHistoryCard} {
		if _, err := book.Staple(ctx, derivation); err != nil {
			t.Fatalf("%s: %v", derivation, err)
		}
	}
}

// emptyNonce is the holder's staple re-signed over a request whose nonce is
// the empty text: a staple with nothing for the reader's replay check.
func emptyNonce(t *testing.T, book *Book, s Staple, holder string) stapleVector {
	t.Helper()
	got, err := CheckStaple(s, holder)
	if err != nil {
		t.Fatal(err)
	}
	request := strings.Replace(string(s.Request.Octets), `"nonce":"`+got.Nonce+`"`, `"nonce":""`, 1)
	reply := resigned(t, book, s.Reply, func(e *Envelope) { e.RequestDigest = digestBytes([]byte(request)) })
	return stapleVector{Name: "an empty nonce", Holder: holder, Request: request, Reply: string(reply.Octets)}
}

// duplicateMember is the holder's history_card/1 staple with its card
// re-signed after a second "anchor" member is put before the first: JSON a
// reader that keeps either member would read differently from one that
// keeps the other, so every checker refuses it.
func duplicateMember(t *testing.T, book *Book, s Staple, holder string) stapleVector {
	t.Helper()
	reply := resigned(t, book, s.Reply, func(e *Envelope) {
		e.Artifact = append([]byte(`{"anchor":"`+strings.Repeat("0", 64)+`",`), e.Artifact[1:]...)
		e.ArtifactDigest = digestBytes(e.Artifact)
	})
	return stapleVector{Name: "a history card with a duplicate member", Holder: holder, Request: string(s.Request.Octets), Reply: string(reply.Octets)}
}

// evidencebook#334: the holder reads which deals the standard card left the
// book for, from the records its own D1 staples sealed.

func TestStandardCardDealsListsTheDealsAStapleWasAttachedFor(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	got, err := book.StandardCardDeals(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("a book with no staple lists %v, %v", got, err)
	}
	holder := KeyID(book.signer)
	for _, deal := range []string{"deal-b", "deal-a", "deal-b"} {
		staple, err := book.StapleForDeal(ctx, DerivationStandardHistory, deal)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CheckStaple(staple, holder); err != nil {
			t.Fatalf("a staple for a deal is still a staple: %v", err)
		}
	}
	// A staple with no deal names no deal.
	if _, err := book.Staple(ctx, DerivationStandardHistory); err != nil {
		t.Fatal(err)
	}
	got, err = book.StandardCardDeals(ctx)
	if err != nil || !slices.Equal(got, []string{"deal-b", "deal-a"}) {
		t.Fatalf("deals %v, %v", got, err)
	}
}

func TestStapleForDealRefusesWhatItCannotRecord(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	before := book.Size()
	for name, c := range map[string]struct{ derivation, deal string }{
		"history card seals no record": {DerivationHistoryCard, "deal-a"},
		"control character":            {DerivationStandardHistory, "deal\n1"},
		"too long":                     {DerivationStandardHistory, strings.Repeat("d", 257)},
		"invalid UTF-8":                {DerivationStandardHistory, "deal\xff"},
		"bidi override":                {DerivationStandardHistory, "deal\u202e1"},
	} {
		if _, err := book.StapleForDeal(ctx, c.derivation, c.deal); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v, want ErrInvalid", name, err)
		}
	}
	if book.Size() != before {
		t.Fatal("a refused staple grew the log")
	}
}

// A refused request still seals its record, with the deal; it must not read
// as a standard card that left the book.
func TestStandardCardDealsSkipsARefusedStaple(t *testing.T) {
	ctx := context.Background()
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	request := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory,
		Coverage: Coverage{ExpectedPin: strings.Repeat("0", 64)}})
	reply, record, err := book.respond(ctx, request, RespondOptions{AuthenticatedRequester: KeyID(book.signer), Policy: DefaultSharePolicy(), stapleDeal: "deal-x"}, true)
	if err != nil || reply.Refusal == nil || record.RecordID == "" {
		t.Fatalf("reply %+v, record %+v, err %v: want a sealed refusal", reply, record, err)
	}
	var st AnsweredStatement
	if err := json.Unmarshal(record.Header.Statement, &st); err != nil || st.Deal != "deal-x" || st.Outcome != OutcomeRefusal {
		t.Fatalf("statement %+v, err %v", st, err)
	}
	if got, err := book.StandardCardDeals(ctx); err != nil || len(got) != 0 {
		t.Fatalf("a refused staple lists %v, %v", got, err)
	}
}
