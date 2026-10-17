// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/evidencebook"
)

func fixed() time.Time { return time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC) }

func readInfo(t *testing.T) bookInfo {
	t.Helper()
	data, err := info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got bookInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// request encodes the request map by hand, as an outside requester would:
// JCS for JSON (the members below are already in JCS order), and
// deterministic CBOR for CBOR.
func request(t *testing.T, record, pin, nonce, profile, mediaType string) evidencebook.Message {
	t.Helper()
	if mediaType == evidencebook.MediaTypeJSON {
		octets := `{"coverage":{"expected_pin":"` + pin + `"},"nonce":"` + nonce + `","profile":"` + profile + `","subject":{"record":"` + record + `"}}`
		return evidencebook.Message{Octets: []byte(octets), MediaType: mediaType}
	}
	text := func(s string) []byte {
		if len(s) < 24 {
			return append([]byte{0x60 | byte(len(s))}, s...)
		}
		return append([]byte{0x78, byte(len(s))}, s...)
	}
	// Deterministic CBOR orders map keys by their encoded bytes: shorter
	// keys first, so nonce, profile, subject, coverage.
	var b []byte
	b = append(b, 0xa4)
	b = append(append(b, text("nonce")...), text(nonce)...)
	b = append(append(b, text("profile")...), text(profile)...)
	b = append(append(append(b, text("subject")...), 0xa1), append(text("record"), text(record)...)...)
	b = append(append(append(b, text("coverage")...), 0xa1), append(text("expected_pin"), text(pin)...)...)
	return evidencebook.Message{Octets: b, MediaType: mediaType}
}

func ask(t *testing.T, m evidencebook.Message, requester string, decline bool) evidencebook.Reply {
	t.Helper()
	octets, err := answer(context.Background(), m, requester, decline, fixed)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := evidencebook.ParseReply(evidencebook.Message{Octets: octets, MediaType: m.MediaType})
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// TestInfoIsStable: the book, its keys and its manifest are the same on
// every run, so a caller can pin them once.
func TestInfoIsStable(t *testing.T) {
	a, err := info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("info differs between runs:\n%s\n%s", a, b)
	}
}

// TestArtifactIsInvariant: two requesters, two nonces, JSON and CBOR, each
// in its own run, get envelopes for the same record and anchor whose
// canonical Artifact octets are identical (ER §5, FR-8).
func TestArtifactIsInvariant(t *testing.T) {
	book := readInfo(t)
	record, pin := book.Records[2], book.Checkpoints[1].Pin
	var artifacts [][]byte
	for _, v := range []struct{ requester, nonce, mediaType string }{
		{"alice", "alice-1", evidencebook.MediaTypeJSON},
		{"bob", "bob-1", evidencebook.MediaTypeCBOR},
		{"carol", "2026-10-13T00:00:00Z/carol", evidencebook.MediaTypeJSON},
	} {
		m := request(t, record, pin, v.nonce, evidencebook.ProfileID, v.mediaType)
		reply := ask(t, m, v.requester, false)
		if reply.Envelope == nil {
			t.Fatalf("%s: no envelope: %+v", v.requester, reply.Refusal)
		}
		e := reply.Envelope
		if err := e.Verify(); err != nil {
			t.Fatalf("%s: %v", v.requester, err)
		}
		sum := sha256.Sum256(m.Octets)
		if e.RequestDigest != hex.EncodeToString(sum[:]) || e.Anchor.Digest != pin || e.Responder != book.ResponderKey {
			t.Fatalf("%s: envelope does not bind the request, pin and key: %+v", v.requester, e)
		}
		artifacts = append(artifacts, e.Artifact)
	}
	for i := 1; i < len(artifacts); i++ {
		if !bytes.Equal(artifacts[0], artifacts[i]) {
			t.Fatalf("the canonical Artifact depends on the requester, nonce or encoding:\n%s\n%s", artifacts[0], artifacts[i])
		}
	}
	// A record's canonical Artifact is the record itself: its digest is the
	// record digest -info lists.
	if sum := sha256.Sum256(artifacts[0]); hex.EncodeToString(sum[:]) != record {
		t.Fatalf("the Artifact's digest %x is not the record %s", sum, record)
	}
}

// TestRefusals: the refusals the third-party guide sends for the declined,
// missing-evidence and unsupported-version classes, and not_authorized
// without an authenticated requester.
func TestRefusals(t *testing.T) {
	book := readInfo(t)
	record, pin := book.Records[2], book.Checkpoints[1].Pin
	missing := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	for _, v := range []struct {
		name, record, profile, requester string
		decline                          bool
		reason                           string
	}{
		{"declined", record, evidencebook.ProfileID, "alice", true, evidencebook.ReasonPolicyDeclined},
		{"no such subject", missing, evidencebook.ProfileID, "alice", false, evidencebook.ReasonNoSuchSubject},
		{"unsupported version", record, "evidence-interop/9", "alice", false, evidencebook.ReasonRequestMalformed},
		{"unauthenticated", record, evidencebook.ProfileID, "", false, evidencebook.ReasonNotAuthorized},
	} {
		reply := ask(t, request(t, v.record, pin, "n", v.profile, evidencebook.MediaTypeJSON), v.requester, v.decline)
		if reply.Refusal == nil || reply.Refusal.Reason != v.reason {
			t.Fatalf("%s: want refusal %s, got %+v %+v", v.name, v.reason, reply.Refusal, reply.Envelope)
		}
		if err := reply.Refusal.Verify(); err != nil || reply.Refusal.KeyID != book.ResponderKey {
			t.Fatalf("%s: refusal does not verify under the responder key: %v", v.name, err)
		}
	}
}

// TestGuideQuotesThisBook: every key, pin and record the third-party guide
// quotes is the one this command serves, and the CBOR request it spells out
// is the deterministic encoding of its JSON twin.
func TestGuideQuotesThisBook(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "THIRD-PARTY-VERIFIER.md"))
	if err != nil {
		t.Fatal(err)
	}
	book := readInfo(t)
	// §6.4 names the responder key by its first eight digits.
	quoted := []string{book.ResponderKey, "the responder key `" + book.ResponderKey[:8] + "…`"}
	for _, cp := range book.Checkpoints {
		quoted = append(quoted, cp.Pin, cp.LogKey.Kid, "`"+cp.LogKey.Iss+"`", "`"+cp.LogKey.Sub+"`")
	}
	quoted = append(quoted, book.Records...)
	for _, q := range quoted {
		if !strings.Contains(string(guide), q) {
			t.Errorf("the guide does not quote %s", q)
		}
	}
	if !strings.Contains(string(guide), "PIN="+book.Checkpoints[1].Pin) || !strings.Contains(string(guide), "REC="+book.Records[2]) {
		t.Error("the guide's PIN and REC are not checkpoint 2 and record 3")
	}
	cbor := request(t, book.Records[2], book.Checkpoints[1].Pin, "bob-1", evidencebook.ProfileID, evidencebook.MediaTypeCBOR).Octets
	sum := sha256.Sum256(cbor)
	if !strings.Contains(string(guide), hex.EncodeToString(sum[:])) || !strings.Contains(string(guide), "these 211 bytes") || len(cbor) != 211 {
		t.Errorf("the guide's CBOR request digest or length is not %x (%d bytes)", sum, len(cbor))
	}
	if encoded, err := evidencebook.EncodeRequest(evidencebook.EvidenceRequest{
		Subject: evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: book.Records[2]}, Coverage: evidencebook.Coverage{ExpectedPin: book.Checkpoints[1].Pin},
		Nonce: "bob-1", Profile: evidencebook.ProfileID,
	}, evidencebook.MediaTypeCBOR); err != nil || !bytes.Equal(encoded, cbor) {
		t.Errorf("the hand-made CBOR request is not the deterministic encoding (%v)", err)
	}
}
