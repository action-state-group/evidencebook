// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"strings"
	"testing"
	"time"
)

// The uniform refusal policy answers policy_declined wherever a more specific
// token would say whether a subject exists, is covered yet, or may be seen
// by this requester; request-shape refusals stay specific.
func TestUniformRefusalPolicy(t *testing.T) {
	f := newFixture(t, "book-u", 3)
	cfg := f.config(t)
	cfg.Responder = ResponderPolicy{RefusalPolicy: RefusalPolicyUniform}
	book, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	held := mustAppend(t, book, observation("a", "x"))
	pin := anchorPin(t, book)
	later := mustAppend(t, book, observation("b", "y"))
	for name, c := range map[string]struct {
		requester string
		subject   Subject
		want      string
	}{
		"no such subject":  {"peer-1", Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("never"))}, ReasonPolicyDeclined},
		"beyond the pin":   {"peer-1", Subject{Kind: SubjectRecord, Digest: later.RecordID}, ReasonPolicyDeclined},
		"not authorized":   {"", Subject{Kind: SubjectRecord, Digest: held.RecordID}, ReasonPolicyDeclined},
		"held and allowed": {"peer-1", Subject{Kind: SubjectRecord, Digest: held.RecordID}, ""},
	} {
		reply := respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: c.subject, Coverage: Coverage{ExpectedPin: pin}}), c.requester)
		if c.want == "" {
			mustEnvelope(t, reply)
			continue
		}
		if reply.Refusal == nil || reply.Refusal.Reason != c.want {
			t.Fatalf("%s: got %+v, want %s", name, reply.Refusal, c.want)
		}
	}
	mustRefusal(t, respondAs(t, book, Message{Octets: []byte(`{}`), MediaType: MediaTypeJSON}, "peer-1"), ReasonRequestMalformed)
	bad := newFixture(t, "book-v", 9).config(t)
	bad.Responder = ResponderPolicy{RefusalPolicy: "sometimes"}
	if _, err := Open(context.Background(), bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("an unknown refusal policy was accepted")
	}
}

type countingLimiter struct{ allowed int }

func (l *countingLimiter) Allow(string, time.Time) bool {
	l.allowed--
	return l.allowed >= 0
}

// FR-37: rate limits, request size and answer size are the responder's cost
// policy, refused before any work; a deadline the responder cannot meet is
// answered deadline_unmet. A limit is refused for the profile the request
// asks for (limitReason): capacity_exceeded under evidence-interop/1, under
// either refusal policy (ER -02 §11), and policy_declined under /0, which
// registers no such token.
func TestResponderCostPolicy(t *testing.T) {
	for i, c := range []struct {
		policy, profile, want string
	}{
		{RefusalPolicySpecific, ProfileID, ReasonPolicyDeclined},
		{RefusalPolicySpecific, ProfileIDv1, evidencerequest.ReasonCapacityExceeded},
		{RefusalPolicyUniform, ProfileID, ReasonPolicyDeclined},
		{RefusalPolicyUniform, ProfileIDv1, evidencerequest.ReasonCapacityExceeded},
	} {
		f := newFixture(t, "book-c", byte(4+i))
		cfg := f.config(t)
		limiter := &countingLimiter{allowed: 2}
		cfg.Responder = ResponderPolicy{RefusalPolicy: c.policy, Limiter: limiter, MaxRequestBytes: 400, MaxAnswerItems: 2}
		book, err := Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = book.Release() })
		half := digestBytes([]byte("a half many records cite"))
		for range 3 {
			mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: half}}})
		}
		pin := anchorPin(t, book)
		mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectExchange, Digest: half}, Coverage: Coverage{ExpectedPin: pin}, Profile: c.profile}), "peer-1"), c.want)
		mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: Coverage{ExpectedPin: pin}, Route: strings.Repeat("r", 400), Profile: c.profile}), "peer-1"), c.want)
		limiter.allowed = 0
		before := book.Size()
		mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{ExpectedPin: pin}, Profile: c.profile}), ""), c.want)
		if book.Size() != before {
			t.Fatal("a declined anonymous request grew the log")
		}
	}
	window := NewWindowLimiter(2, time.Minute)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !window.Allow("a", now) || !window.Allow("a", now) || window.Allow("a", now) || !window.Allow("b", now) || !window.Allow("a", now.Add(time.Minute+time.Second)) {
		t.Fatal("the window limiter does not count per requester per window")
	}
}

// ER §9, §4.2 item 2: once a retention commitment lapses, a subject the book
// no longer answers is refused retention_expired, never no_such_subject;
// within the commitment the honest token is still no_such_subject.
func TestLapsedRetentionCommitmentIsRetentionExpired(t *testing.T) {
	f := newFixture(t, "book-r", 5)
	book := f.open(t)
	mustAppend(t, book, observation("a", "x"))
	half := digestBytes([]byte("a half no record cites"))
	until := f.clock.t.Add(time.Hour)
	if _, err := book.CommitRetention(context.Background(), Subject{Kind: SubjectExchange, Digest: half}, until); err != nil {
		t.Fatal(err)
	}
	if _, err := book.CommitRetention(context.Background(), Subject{Kind: SubjectCorrelation, Correlation: "c"}, until); !errors.Is(err, ErrInvalid) {
		t.Fatal("a commitment naming a correlation was accepted (ERV A11)")
	}
	pin := anchorPin(t, book)
	req := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectExchange, Digest: half}, Coverage: Coverage{ExpectedPin: pin}})
	mustRefusal(t, respondAs(t, book, req, "peer-1"), ReasonNoSuchSubject)
	f.clock.t = until.Add(time.Minute)
	mustRefusal(t, respondAs(t, book, req, "peer-1"), ReasonRetentionExpired)
}

// #206 row 8: an empty RefusalPolicy answers uniformly and the manifest
// declares it; an explicit specific still answers the specific reason.
func TestZeroValueRefusalPolicyIsUniform(t *testing.T) {
	for i, c := range []struct {
		policy, want string
	}{
		{"", ReasonPolicyDeclined},
		{RefusalPolicyUniform, ReasonPolicyDeclined},
		{RefusalPolicySpecific, ReasonNoSuchSubject},
	} {
		f := newFixture(t, "book-z", byte(20+i))
		cfg := f.config(t)
		cfg.Responder = ResponderPolicy{RefusalPolicy: c.policy}
		book, err := Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = book.Release() })
		mustAppend(t, book, observation("a", "x"))
		pin := anchorPin(t, book)
		req := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("never"))}, Coverage: Coverage{ExpectedPin: pin}})
		mustRefusal(t, respondAs(t, book, req, "peer-1"), c.want)
		manifest, err := book.Manifest(ManifestOptions{Bindings: []string{BindingHTTP}})
		if err != nil {
			t.Fatal(err)
		}
		wantPolicy := RefusalPolicyUniform
		if c.policy == RefusalPolicySpecific {
			wantPolicy = RefusalPolicySpecific
		}
		if !strings.Contains(string(manifest), `"refusal_policy":"`+wantPolicy+`"`) {
			t.Fatalf("policy %q: manifest %s", c.policy, manifest)
		}
	}
}

// OnRefusal tells the holder's operator the reason the gate decided, which the
// uniform policy folds into policy_declined (evidencebook#580). The reply is
// the same bytes with or without the callback; under the specific policy the
// two reasons agree; an answered request does not call it.
func TestOnRefusalReportsDecidedReason(t *testing.T) {
	open := func(name, policy string) (*Book, string, Record) {
		f := newFixture(t, name, 3)
		cfg := f.config(t)
		cfg.Responder = ResponderPolicy{RefusalPolicy: policy}
		book, err := Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = book.Release() })
		held := mustAppend(t, book, observation("a", "x"))
		return book, anchorPin(t, book), held
	}
	wantSent := func(policy, decided string) string {
		if policy == RefusalPolicySpecific {
			return decided
		}
		return ReasonPolicyDeclined
	}
	type seen struct{ digest, decided, sent string }
	for _, policy := range []string{RefusalPolicyUniform, RefusalPolicySpecific} {
		book, pin, held := open("book-r-"+policy, policy)
		for name, c := range map[string]struct {
			requester string
			subject   Subject
			decided   string
		}{
			"no such subject": {"peer-1", Subject{Kind: SubjectRecord, Digest: digestBytes([]byte("never"))}, ReasonNoSuchSubject},
			"not authorized":  {"", Subject{Kind: SubjectRecord, Digest: held.RecordID}, ReasonNotAuthorized},
			"unmet coverage":  {"peer-1", Subject{Kind: SubjectRecord, Digest: held.RecordID}, ReasonCoverageUnsatisfiable},
		} {
			cov := Coverage{ExpectedPin: pin}
			if name == "unmet coverage" {
				cov = Coverage{MinFreshness: &Freshness{Size: new(uint64)}}
				*cov.MinFreshness.Size = 1 << 40
			}
			msg := jsonRequest(t, EvidenceRequest{Subject: c.subject, Coverage: cov})
			plain := respondAs(t, book, msg, c.requester)
			var got []seen
			reply, record, err := book.Respond(context.Background(), msg, RespondOptions{
				AuthenticatedRequester: c.requester,
				Policy:                 policyWith("peers"),
				OnRefusal:              func(d, decided, sent string) { got = append(got, seen{d, decided, sent}) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if plain.Refusal == nil || reply.Refusal == nil {
				t.Fatalf("%s/%s: not refused", policy, name)
			}
			if len(got) != 1 {
				t.Fatalf("%s/%s: callback called %d times", policy, name, len(got))
			}
			if reply.Refusal.Detail != plain.Refusal.Detail || reply.Refusal.RequestDigest != plain.Refusal.RequestDigest || reply.Refusal.KeyID != plain.Refusal.KeyID {
				t.Fatalf("%s/%s: the refusal differs with the callback: %+v vs %+v", policy, name, reply.Refusal, plain.Refusal)
			}
			if c.requester != "" {
				// The sealed record carries the sent reason only.
				var st AnsweredStatement
				if err := json.Unmarshal(record.Header.Statement, &st); err != nil || st.Reason != reply.Refusal.Reason {
					t.Fatalf("%s/%s: sealed reason %q, sent %q (%v)", policy, name, st.Reason, reply.Refusal.Reason, err)
				}
				if c.decided != wantSent(policy, c.decided) && bytes.Contains(record.Header.Statement, []byte(c.decided)) {
					t.Fatalf("%s/%s: the sealed record names the decided reason", policy, name)
				}
			}
			if got[0].decided != c.decided || got[0].sent != reply.Refusal.Reason || got[0].digest != reply.Refusal.RequestDigest {
				t.Fatalf("%s/%s: got %+v, refusal %+v, want decided %s", policy, name, got[0], reply.Refusal, c.decided)
			}
			if want := wantSent(policy, c.decided); reply.Refusal.Reason != want || plain.Refusal.Reason != want {
				t.Fatalf("%s/%s: sent %s, want %s", policy, name, reply.Refusal.Reason, want)
			}
		}
		// A request refused for its shape is never folded: decided is sent.
		var shape []seen
		if _, _, err := book.Respond(context.Background(), Message{Octets: []byte(`{}`), MediaType: MediaTypeJSON},
			RespondOptions{AuthenticatedRequester: "peer-1", Policy: policyWith("peers"), OnRefusal: func(d, decided, sent string) { shape = append(shape, seen{d, decided, sent}) }}); err != nil {
			t.Fatal(err)
		}
		if len(shape) != 1 || shape[0].decided != ReasonRequestMalformed || shape[0].sent != ReasonRequestMalformed {
			t.Fatalf("%s: malformed request reported %+v", policy, shape)
		}
		called := false
		reply, _, err := book.Respond(context.Background(), jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: held.RecordID}, Coverage: Coverage{ExpectedPin: pin}}),
			RespondOptions{AuthenticatedRequester: "peer-1", Policy: policyWith("peers"), OnRefusal: func(string, string, string) { called = true }})
		if err != nil || reply.Envelope == nil || called {
			t.Fatalf("%s: an answered request called OnRefusal (%v) or failed: %v", policy, called, err)
		}
	}
}
