// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
)

// live16Key is stevenmih-1's announced key in live-16, which signs its
// envelopes and its checkpoints.
const live16Key = "1dd25d9938ba4841f5e127e39e8dcc71df82b2072d9186ece75e431063ab26d9"

// jcsOf is the JCS of a JSON value, as a requester transmits it.
func jcsOf(t *testing.T, raw []byte) []byte {
	t.Helper()
	value, err := evidencerequest.DecodeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	jcs, err := evidencerequest.EncodeJCS(value)
	if err != nil {
		t.Fatal(err)
	}
	return jcs
}

// TestRequestSentLive16 is evidencebook#245: the capsules plugin's sealed
// requests from live-16 07-ask (testdata/live16-ask/README.md) carry members
// the request draft does not define (audience, requester_id,
// requester_auth). Recorded as sent, each has the digest stevenmih-1 signed
// its reply over, and each reply verifies as an artifact: checkpoints and
// history_card/1 under evidence-interop/0, the exchange under /1.
func TestRequestSentLive16(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "asker", 40).open(t)
	for name, profile := range map[string]string{"checkpoints": ProfileID, "history_card": ProfileID, "exchange": evidencerequest.ProfileIDv1} {
		fixture := readJSON[struct {
			Request json.RawMessage `json:"request"`
			Reply   json.RawMessage `json:"reply"`
		}](t, "testdata/live16-ask/"+name+".json")
		octets := jcsOf(t, fixture.Request)
		for _, member := range []string{`"audience":`, `"requester_auth":`, `"requester_id":`, `"profile":"` + profile + `"`} {
			if !bytes.Contains(octets, []byte(member)) {
				t.Fatalf("%s: the fixture's request has no %s", name, member)
			}
		}
		sent, err := book.RequestSent(ctx, Message{Octets: octets, MediaType: MediaTypeJSON}, "stevenmih-1")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(sent.Bytes, octets) {
			t.Fatalf("%s: the recorded request is not the one sent", name)
		}
		var asked RequestStatement
		if err := json.Unmarshal(book.records[sent.RecordID].Header.Statement, &asked); err != nil || !asked.ComposedElsewhere || !bytes.Equal(asked.Request, octets) {
			t.Fatalf("%s: the request record %+v (%v) does not mark the request composed elsewhere", name, asked, err)
		}
		var reply struct {
			RequestDigest string `json:"request_digest"`
		}
		if err := json.Unmarshal(fixture.Reply, &reply); err != nil {
			t.Fatal(err)
		}
		if sent.Digest != reply.RequestDigest {
			t.Fatalf("%s: recorded digest %s, the reply answers %s", name, sent.Digest, reply.RequestDigest)
		}
		recorded, err := book.RecordResponse(ctx, sent.RecordID, Message{Octets: fixture.Reply, MediaType: MediaTypeJSON}, ResponderKeys{Signer: live16Key, Checkpoint: live16Key})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var statement ResponseStatement
		if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil {
			t.Fatal(err)
		}
		if statement.Outcome != OutcomeArtifact || statement.Failure != "" {
			t.Fatalf("%s: %+v, want a verified artifact", name, statement)
		}
	}
}

// TestRequestSentRefusesWhatWasNotSent: only a well-formed JSON request in
// JCS is recorded, and a nonce is never used twice; a request without one
// is recorded as sent, every time.
func TestRequestSentRefusesWhatWasNotSent(t *testing.T) {
	ctx := context.Background()
	book := newFixture(t, "asker", 41).open(t)
	sent := func(octets string, mediaType string) error {
		_, err := book.RequestSent(ctx, Message{Octets: []byte(octets), MediaType: mediaType}, "responder")
		return err
	}
	const ok = `{"audience":"a","coverage":{"min_freshness":1},"nonce":"n1","subject":{"checkpoints":null}}`
	for name, tc := range map[string]struct{ octets, mediaType string }{
		"CBOR":            {ok, MediaTypeCBOR},
		"not JCS":         {`{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}`, MediaTypeJSON},
		"whitespace":      {`{"coverage": {"min_freshness":1},"subject":{"checkpoints":null}}`, MediaTypeJSON},
		"no subject":      {`{"coverage":{"min_freshness":1}}`, MediaTypeJSON},
		"two coverages":   {`{"coverage":{"expected_pin":"` + strings.Repeat("ab", 32) + `","min_freshness":1},"subject":{"checkpoints":null}}`, MediaTypeJSON},
		"unknown profile": {`{"coverage":{"min_freshness":1},"profile":"evidence-interop/9","subject":{"checkpoints":null}}`, MediaTypeJSON},
		"not JSON":        {`{"subject":`, MediaTypeJSON},
	} {
		if err := sent(tc.octets, tc.mediaType); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if err := sent(ok, MediaTypeJSON); err != nil {
		t.Fatal(err)
	}
	if err := sent(`{"coverage":{"min_freshness":2},"nonce":"n1","subject":{"checkpoints":null}}`, MediaTypeJSON); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "nonce already used") {
		t.Fatalf("a reused nonce: %v", err)
	}
	// Book.Request refuses a nonce RequestSent used, too.
	if _, err := book.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh(), Nonce: "n1"}, "responder"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Request reused RequestSent's nonce: %v", err)
	}
	// A request the book composed is not marked.
	made, err := book.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}, "responder")
	if err != nil {
		t.Fatal(err)
	}
	if statement := book.records[made.RecordID].Header.Statement; bytes.Contains(statement, []byte("composed_elsewhere")) {
		t.Fatalf("a request the book composed is marked: %s", statement)
	}
	const noNonce = `{"coverage":{"min_freshness":1},"subject":{"checkpoints":null}}`
	for range 2 {
		if err := sent(noNonce, MediaTypeJSON); err != nil {
			t.Fatalf("a request without a nonce: %v", err)
		}
	}
}

// TestRefusalReasonsUnderTheRequestedProfile: a refusal names no profile,
// so its reason conforms under the one the request asked for (v1 §6.3):
// capacity_exceeded, which ER -02 adds, is a conforming refusal to an
// evidence-interop/1 request and a nonconforming one to a /0 request.
func TestRefusalReasonsUnderTheRequestedProfile(t *testing.T) {
	ctx := context.Background()
	responder := newFixture(t, "responder", 42).open(t)
	requester := newFixture(t, "requester", 43).open(t)
	for profile, want := range map[string]string{evidencerequest.ProfileIDv1: OutcomeRefusal, ProfileID: OutcomeRefusalNonconforming} {
		octets := []byte(`{"coverage":{"min_freshness":1},"nonce":"` + profile + `","profile":"` + profile + `","subject":{"checkpoints":null}}`)
		sent, err := requester.RequestSent(ctx, Message{Octets: octets, MediaType: MediaTypeJSON}, "responder")
		if err != nil {
			t.Fatal(err)
		}
		refusal, err := Refusal{RequestDigest: sent.Digest, Reason: evidencerequest.ReasonCapacityExceeded, IssuedAt: "2026-10-31T12:00:00Z", KeyID: KeyID(responder.signer)}.Sign(ctx, responder.signer)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := evidencerequest.EncodeReply(MediaTypeJSON, nil, &refusal)
		if err != nil {
			t.Fatal(err)
		}
		recorded, err := requester.RecordResponse(ctx, sent.RecordID, reply.Message, responderKeys(responder, 42))
		if err != nil {
			t.Fatal(err)
		}
		var statement ResponseStatement
		if err := json.Unmarshal(recorded.Header.Statement, &statement); err != nil {
			t.Fatal(err)
		}
		if statement.Outcome != want || statement.Reason != evidencerequest.ReasonCapacityExceeded {
			t.Fatalf("%s: %+v, want %s", profile, statement, want)
		}
	}
}

// TestRespondAnswersEvidenceInterop0Only: agent-action-capsule reads
// evidence-interop/1 requests, but the book answers /0 only, so a /1
// request is refused as an unknown profile is, naming /0 alone.
func TestRespondAnswersEvidenceInterop0Only(t *testing.T) {
	book := newFixture(t, "responder", 44).open(t)
	for _, profile := range []string{evidencerequest.ProfileIDv1, "evidence-interop/9"} {
		octets := []byte(`{"coverage":{"min_freshness":1},"profile":"` + profile + `","subject":{"checkpoints":null}}`)
		refusal := mustRefusal(t, respondAs(t, book, Message{Octets: octets, MediaType: MediaTypeJSON}, ""), ReasonRequestMalformed)
		if refusal.Detail != "profiles supported: "+ProfileID {
			t.Fatalf("%s: detail %q", profile, refusal.Detail)
		}
	}
	// /0, named or not, is answered.
	for _, octets := range []string{`{"coverage":{"min_freshness":1},"profile":"evidence-interop/0","subject":{"checkpoints":null}}`, `{"coverage":{"min_freshness":1},"subject":{"checkpoints":null}}`} {
		mustAppend(t, book, observation("s", "p"))
		if _, err := book.Checkpoint(context.Background()); err != nil {
			t.Fatal(err)
		}
		mustEnvelope(t, respondAs(t, book, Message{Octets: []byte(octets), MediaType: MediaTypeJSON}, ""))
	}
}

// TestRecordBodyMatches: a record-set entry's body is the record its digest
// names when it is the capsule preimage, or the capsule as a ledger holds
// it with that digest as its capsule_id; a changed capsule is neither.
func TestRecordBodyMatches(t *testing.T) {
	fixture := readJSON[struct {
		Reply struct {
			Artifact []byte `json:"artifact"`
		} `json:"reply"`
	}](t, "testdata/live16-ask/exchange.json")
	var artifact struct {
		Records []struct {
			Body   string `json:"body"`
			Digest string `json:"digest"`
		} `json:"records"`
	}
	if err := json.Unmarshal(fixture.Reply.Artifact, &artifact); err != nil || len(artifact.Records) != 1 {
		t.Fatalf("%v %d", err, len(artifact.Records))
	}
	capsule, err := hex.DecodeString(artifact.Records[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	digest := artifact.Records[0].Digest
	preimage, err := capsulePreimage(capsule, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !recordBodyMatches(capsule, digest) || !recordBodyMatches(preimage, digest) {
		t.Fatal("the held capsule and its preimage must both match their digest")
	}
	changed := bytes.Replace(capsule, []byte(`"action_type":"fyi"`), []byte(`"action_type":"fyj"`), 1)
	if bytes.Equal(changed, capsule) {
		t.Fatal("the fixture capsule has no action_type to change")
	}
	other := strings.Repeat("ab", 32)
	for name, tc := range map[string]struct {
		body   []byte
		digest string
	}{
		"a changed capsule":           {changed, digest},
		"another digest":              {capsule, other},
		"a preimage under another id": {preimage, other},
		"not JSON":                    {[]byte("not json"), digest},
	} {
		if recordBodyMatches(tc.body, tc.digest) {
			t.Fatalf("%s matched", name)
		}
	}
}
