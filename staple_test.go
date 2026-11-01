// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"errors"
	"testing"

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
