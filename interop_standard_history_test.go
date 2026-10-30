// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
)

// standard_history/1 (evidencebook#157): a stranger gets history_card/1, a
// signed asker gets the card, and the card's counts recompute from the
// deal entries it was derived from.

var testPolicy = standardhistory.Policy{Digest: digestBytes([]byte("policy v1")), Window: standardhistory.Window{Months: 3}}

// dealsOf is a test host's deal entries: one per "deal_terms" record, read
// from its statement, agreed when the book committed it. What the host saw
// is kept in seen.
func dealsOf(seen *[][]Record) func(context.Context, Checkpoint, []Record) (standardhistory.Policy, []standardhistory.Deal, error) {
	return func(_ context.Context, _ Checkpoint, records []Record) (standardhistory.Policy, []standardhistory.Deal, error) {
		if seen != nil {
			*seen = append(*seen, records)
		}
		deals := []standardhistory.Deal{}
		for _, r := range records {
			if r.Header.RecordType != "deal_terms" {
				continue
			}
			var d standardhistory.Deal
			if err := json.Unmarshal(r.Header.Statement, &d); err != nil {
				return standardhistory.Policy{}, nil, err
			}
			d.Deal, d.AgreedAt = r.RecordID, r.Header.CommittedAt
			deals = append(deals, d)
		}
		return testPolicy, deals, nil
	}
}

func standardHistoryBook(t *testing.T, source *StandardHistorySource, cfg func(*Config)) *Book {
	t.Helper()
	f := newFixture(t, "book-sh", 9)
	c := f.config(t)
	c.StandardHistory = source
	if cfg != nil {
		cfg(&c)
	}
	book, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	for _, statement := range []string{
		`{"role":"provided","amount":{"value":"2500","assetCode":"USD","assetScale":2},"outcome":"held","happened":"stamped_by_both","disputes":[]}`,
		`{"role":"bought","amount":{"value":"999","assetCode":"USD","assetScale":2},"outcome":"undecided","happened":"receipt_only","disputes":[{"side":"raised","outcome":"open","forum":"rail"}]}`,
		`{"counterparty_class":"shop_preauth","role":"bought","amount":{"value":"120","assetCode":"EUR","assetScale":0},"outcome":"held","happened":"no_record","disputes":[]}`,
	} {
		mustAppend(t, book, Entry{RecordType: "deal_terms", EpistemicType: ProducerClaim, CounterpartyRef: "carol", Statement: json.RawMessage(statement)})
	}
	mustAppend(t, book, Entry{RecordType: "observation", EpistemicType: ObservedEvent})
	return book
}

func askStandardHistory(t *testing.T, book *Book, requester, tier, pin string) Reply {
	t.Helper()
	reply, _ := respondWith(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}}),
		RespondOptions{AuthenticatedRequester: requester, Policy: policyWith(tier)})
	return reply
}

func TestStandardHistoryIsAnsweredToSignedRequestersOnly(t *testing.T) {
	var seen [][]Record
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(&seen)}, nil)
	pin := anchorPin(t, book)
	// A stranger has the public questions answered and nothing else.
	mustRefusal(t, askStandardHistory(t, book, "", "peers", pin), ReasonNotAuthorized)
	mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Derivation: DerivationHistoryCard, Coverage: Coverage{ExpectedPin: pin}}), ""))
	if len(seen) != 0 {
		t.Fatal("the host's inputs were read for a stranger")
	}
	// A signed asker gets the card whatever the tier, and every asker gets
	// the same bytes.
	var card []byte
	for _, tier := range []string{"off", "counterparties", "prospective", "peers"} {
		for _, requester := range []string{"carol", "mallory"} {
			e := mustEnvelope(t, askStandardHistory(t, book, requester, tier, pin))
			if len(e.Verification) != 0 || len(e.VerificationContent) != 0 {
				t.Fatal("a card carries verification material")
			}
			if e.Derivation != DerivationStandardHistory {
				t.Fatal("the envelope signs another derivation")
			}
			if card == nil {
				card = e.Artifact
			} else if !bytes.Equal(card, e.Artifact) {
				t.Fatalf("%s under %s got another card", requester, tier)
			}
		}
	}
	var got map[string]any
	if err := json.Unmarshal(card, &got); err != nil {
		t.Fatal(err)
	}
	if got["holder_kind"] != "person" || got["controller_class"] != "person_declared" || got["anchor"] != pin || got["policy"] != testPolicy.Digest {
		t.Fatalf("card head %v", got)
	}
	if in := got["in_window"].(map[string]any); in["of"] != 3.0 || in["counted"] != 2.0 || in["not_counted"] != 1.0 {
		t.Fatalf("in_window %v", in)
	}
	// The host read the servable records the anchor covers, and only those.
	kinds := map[string]int{}
	for _, r := range seen[0] {
		kinds[r.Header.RecordType]++
	}
	if kinds["deal_terms"] != 3 || kinds["observation"] != 1 {
		t.Fatalf("the host was given %v", kinds)
	}
	for _, records := range seen {
		for _, r := range records {
			if r.Header.RecordType == RecordTypeRequestAnswered {
				t.Fatal("the host was given the responder's own bookkeeping")
			}
		}
	}
}

// The counts recompute from the deal entries (SH §6 counts_recompute); a
// recipient without them sees the counts as the holder's statement. Nothing
// a host hands in turns into a ratio: the card's form is closed.
func TestStandardHistoryCountsRecomputeFromTheDeals(t *testing.T) {
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "node", Inputs: dealsOf(nil)}, nil)
	pin := anchorPin(t, book)
	e := mustEnvelope(t, askStandardHistory(t, book, "carol", "counterparties", pin))
	checkpoints, err := book.Checkpoints(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	anchor := checkpoints[len(checkpoints)-1]
	asOf := standardhistory.AsOf{Anchor: pin, AnchorTime: evidencerequest.ProfileTime(anchor.IssuedAt), FirstCheckpointTime: evidencerequest.ProfileTime(checkpoints[0].IssuedAt)}
	var records []Record
	for _, id := range book.order {
		records = append(records, book.records[id])
	}
	_, deals, err := dealsOf(nil)(context.Background(), anchor, records)
	if err != nil {
		t.Fatal(err)
	}
	full, err := standardhistory.Check(e.Artifact, &testPolicy, &asOf, deals)
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range standardhistory.Properties {
		if full[property] != standardhistory.Pass {
			t.Fatalf("%s is %s with every input", property, full[property])
		}
	}
	bare, err := standardhistory.Check(e.Artifact, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !standardhistory.IsStandard(bare) || bare["counts_recompute"] != standardhistory.NotChecked || bare["window_from_policy"] != standardhistory.NotChecked {
		t.Fatalf("without the deals: %v", bare)
	}
	// Other deals do not recompute to this card.
	if other, _ := standardhistory.Check(e.Artifact, &testPolicy, &asOf, deals[:1]); other["counts_recompute"] != standardhistory.Fail {
		t.Fatal("the counts recompute from deals they were not derived from")
	}
	for _, ratio := range []string{"%", ".", "percent", "share", "ratio"} {
		if bytes.Contains(e.Artifact, []byte(ratio)) {
			t.Fatalf("the card carries %q", ratio)
		}
	}
}

// Without a source the token is not served; a source the card cannot be
// derived from is an error, never an answer; and the item cap, which
// bounds record lists, does not decline a card.
func TestStandardHistoryConfiguration(t *testing.T) {
	t.Run("not served", func(t *testing.T) {
		book := standardHistoryBook(t, nil, nil)
		mustRefusal(t, askStandardHistory(t, book, "carol", "peers", anchorPin(t, book)), ReasonDerivationUnsupported)
		manifest, err := book.Manifest(ManifestOptions{Bindings: []string{BindingStream}})
		if err != nil || bytes.Contains(manifest, []byte(DerivationStandardHistory)) {
			t.Fatalf("the manifest lists a derivation the book does not serve: %s %v", manifest, err)
		}
	})
	t.Run("served", func(t *testing.T) {
		book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, func(c *Config) { c.Responder.MaxAnswerItems = 1 })
		mustEnvelope(t, askStandardHistory(t, book, "carol", "peers", anchorPin(t, book)))
		manifest, err := book.Manifest(ManifestOptions{Bindings: []string{BindingStream}})
		if err != nil || !bytes.Contains(manifest, []byte(`"derivations":["history_card/1","standard_history/1"]`)) {
			t.Fatalf("manifest %s %v", manifest, err)
		}
		// Only over full_history.
		mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRange, Range: [2]uint64{0, 1}}, Derivation: DerivationStandardHistory, Coverage: fresh()}), "carol"), ReasonNoSuchSubject)
	})
	t.Run("bad inputs", func(t *testing.T) {
		broken := errors.New("policy store unavailable")
		for name, inputs := range map[string]func(context.Context, Checkpoint, []Record) (standardhistory.Policy, []standardhistory.Deal, error){
			"host error": func(context.Context, Checkpoint, []Record) (standardhistory.Policy, []standardhistory.Deal, error) {
				return standardhistory.Policy{}, nil, broken
			},
			"no window": func(context.Context, Checkpoint, []Record) (standardhistory.Policy, []standardhistory.Deal, error) {
				return standardhistory.Policy{Digest: testPolicy.Digest}, nil, nil
			},
			"a deal not of SH §5.2": func(context.Context, Checkpoint, []Record) (standardhistory.Policy, []standardhistory.Deal, error) {
				return testPolicy, []standardhistory.Deal{{Deal: "d", AgreedAt: "2026-09-26T12:00:00Z", Role: "both"}}, nil
			},
		} {
			t.Run(name, func(t *testing.T) {
				book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: inputs}, nil)
				pin := anchorPin(t, book)
				_, _, err := book.Respond(context.Background(), jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}}),
					RespondOptions{AuthenticatedRequester: "carol", Policy: DefaultSharePolicy()})
				if err == nil || name == "host error" && !errors.Is(err, broken) {
					t.Fatalf("got %v", err)
				}
			})
		}
	})
	for _, source := range []*StandardHistorySource{{HolderKind: "agent", Inputs: dealsOf(nil)}, {HolderKind: "person"}} {
		c := newFixture(t, "book-bad", 10).config(t)
		c.StandardHistory = source
		if _, err := Open(context.Background(), c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Open took %+v: %v", source, err)
		}
	}
}

// A requester records the card as an answer whose inclusion it could not
// check (IP P-13).
func TestStandardHistoryIsRecordedByTheRequester(t *testing.T) {
	ctx := context.Background()
	responder := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(nil)}, nil)
	pin := anchorPin(t, responder)
	requester := newFixture(t, "requester", 31).open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationStandardHistory, Coverage: Coverage{ExpectedPin: pin}}, "book-sh")
	if err != nil {
		t.Fatal(err)
	}
	reply := respondAs(t, responder, sent.Message(), "requester")
	recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 9))
	if err != nil {
		t.Fatal(err)
	}
	statement := string(recorded.Header.Statement)
	if !strings.Contains(statement, `"not_checked":["anchor_inclusion"]`) || !strings.Contains(statement, `"outcome":"artifact"`) {
		t.Fatalf("recorded %s", statement)
	}
}

// The card is as of its anchor: the host is given the records the anchor
// covers, never the responder's own bookkeeping even once an anchor covers
// it, and a deal after the anchor changes only a later card.
func TestStandardHistoryIsAsOfItsAnchor(t *testing.T) {
	var seen [][]Record
	book := standardHistoryBook(t, &StandardHistorySource{HolderKind: "person", Inputs: dealsOf(&seen)}, nil)
	first := anchorPin(t, book)
	before := mustEnvelope(t, askStandardHistory(t, book, "carol", "peers", first)).Artifact
	mustAppend(t, book, Entry{RecordType: "deal_terms", EpistemicType: ProducerClaim, Statement: json.RawMessage(
		`{"role":"provided","amount":{"value":"5","assetCode":"USD","assetScale":0},"outcome":"held","happened":"no_record","disputes":[]}`)})
	// The asking above sealed an evidence_request_answered record; the new
	// anchor covers it.
	second := anchorPin(t, book)
	covered := false
	for _, id := range book.order {
		covered = covered || book.records[id].Header.RecordType == RecordTypeRequestAnswered
	}
	if !covered {
		t.Fatal("the book holds no answered record for the new anchor to cover")
	}
	seen = nil
	after := mustEnvelope(t, askStandardHistory(t, book, "carol", "peers", second)).Artifact
	for _, r := range seen[0] {
		if r.Header.RecordType == RecordTypeRequestAnswered || r.Header.RecordType == RecordTypeDisclosure {
			t.Fatalf("the host was given a %s record", r.Header.RecordType)
		}
	}
	if again := mustEnvelope(t, askStandardHistory(t, book, "carol", "peers", first)).Artifact; !bytes.Equal(again, before) {
		t.Fatal("a deal after the anchor changed the card as of it")
	}
	var a, b map[string]any
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
		t.Fatal("cards do not read")
	}
	if a["in_window"].(map[string]any)["of"] != 3.0 || b["in_window"].(map[string]any)["of"] != 4.0 {
		t.Fatalf("in_window before %v, after %v", a["in_window"], b["in_window"])
	}
}
