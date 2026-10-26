// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
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
// policy, answered policy_declined before any work; a deadline the responder
// cannot meet is answered deadline_unmet.
func TestResponderCostPolicy(t *testing.T) {
	f := newFixture(t, "book-c", 4)
	cfg := f.config(t)
	limiter := &countingLimiter{allowed: 2}
	cfg.Responder = ResponderPolicy{Limiter: limiter, MaxRequestBytes: 400, MaxAnswerItems: 2}
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
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectExchange, Digest: half}, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"), ReasonPolicyDeclined)
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: Coverage{ExpectedPin: pin}, Route: strings.Repeat("r", 400)}), "peer-1"), ReasonPolicyDeclined)
	limiter.allowed = 0
	before := book.Size()
	mustRefusal(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: Coverage{ExpectedPin: pin}}), ""), ReasonPolicyDeclined)
	if book.Size() != before {
		t.Fatal("a declined anonymous request grew the log")
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
