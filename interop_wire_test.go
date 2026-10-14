// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// A request in deterministic CBOR is answered in CBOR, and its digest is over
// the octets as received: a non-deterministic encoding of the same map is a
// different request with a different digest, never re-canonicalized.
func TestCBORRequestIsAnsweredInCBORAndDigestedAsReceived(t *testing.T) {
	book, records := linkedBook(t)
	pin := anchorPin(t, book)
	req := EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{ExpectedPin: pin}, Nonce: "n"}
	deterministic := requestIn(t, req, MediaTypeCBOR)
	reply := respondAs(t, book, deterministic, "peer-1")
	e := mustEnvelope(t, reply)
	if reply.MediaType != MediaTypeCBOR || e.RequestDigest != digestBytes(deterministic.Octets) {
		t.Fatal("a CBOR request is answered in CBOR over the digest of its octets")
	}
	if _, err := decodeStrictCBOR(reply.Octets); err != nil {
		t.Fatal(err)
	}
	// The same map with a non-minimal length head for its first key.
	loose := append([]byte{deterministic.Octets[0], 0x78, deterministic.Octets[1] - 0x60}, deterministic.Octets[2:]...)
	looseReply := respondAs(t, book, Message{Octets: loose, MediaType: MediaTypeCBOR}, "peer-1")
	if got := mustEnvelope(t, looseReply); got.RequestDigest != digestBytes(loose) || got.RequestDigest == e.RequestDigest || !bytes.Equal(got.Artifact, e.Artifact) {
		t.Fatal("a non-deterministic request is digested as received and answered with the same artifact")
	}
	for name, octets := range map[string][]byte{
		"trailing byte":   append(append([]byte{}, deterministic.Octets...), 0x00),
		"duplicate key":   duplicateFirstKey(deterministic.Octets),
		"not a map":       {0x80},
		"truncated":       deterministic.Octets[:len(deterministic.Octets)-1],
		"integer map key": {0xa1, 0x01, 0x01},
	} {
		t.Run(name, func(t *testing.T) {
			mustRefusal(t, respondAs(t, book, Message{Octets: octets, MediaType: MediaTypeCBOR}, "peer-1"), ReasonRequestMalformed)
		})
	}
	if _, _, err := book.Respond(context.Background(), Message{Octets: deterministic.Octets, MediaType: "application/cbor; charset=x"}, RespondOptions{Policy: DefaultSharePolicy()}); !errors.Is(err, ErrInvalid) {
		t.Fatal("a media type is compared exactly")
	}
}

// duplicateFirstKey repeats the first member of a deterministic CBOR map of
// fewer than 23 members whose first member is a short text key and a short
// text value.
func duplicateFirstKey(m []byte) []byte {
	count := m[0] & 0x1f
	keyLen := int(m[1] & 0x1f)
	first := m[1 : 1+1+keyLen]
	value := m[1+1+keyLen:]
	valueLen := 1 + int(value[0]&0x1f)
	member := append(append([]byte{}, first...), value[:valueLen]...)
	out := []byte{0xa0 | (count + 1)}
	out = append(out, member...)
	return append(out, m[1:]...)
}

// The record artifact and its CLL material check out under the profile's
// procedure, including the consistency proof once the anchor extends an
// earlier checkpoint, and fail when any proof octet changes.
func TestRecordMaterialProvesTheArtifact(t *testing.T) {
	book, records := linkedBook(t)
	anchorPin(t, book)
	mustAppend(t, book, observation("later", "z"))
	pin := anchorPin(t, book)
	e := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[2].RecordID}, Coverage: Coverage{ExpectedPin: pin}}), "peer-1"))
	if len(e.Verification) != 3 || e.Verification[2].Kind != KindCLLConsistencyProof {
		t.Fatalf("an anchor extending an earlier checkpoint carries its consistency proof: %+v", e.Verification)
	}
	if err := verifyRecordInclusion(e, checkpointKeyID(1)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 3; i++ {
		tampered := e
		tampered.VerificationContent = slices.Clone(e.VerificationContent)
		item := slices.Clone(e.VerificationContent[i])
		item[len(item)-1] ^= 1
		tampered.VerificationContent[i] = item
		if err := verifyRecordInclusion(tampered, checkpointKeyID(1)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("a changed %s verified", e.Verification[i].Kind)
		}
	}
	// Under a size floor the envelope states the size the anchor attains, in
	// the checkpoint's own log_size, and a time floor states its time.
	sized := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[2].RecordID}, Coverage: fresh()}), "peer-1"))
	timed := mustEnvelope(t, respondAs(t, book, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[2].RecordID}, Coverage: Coverage{MinFreshness: &Freshness{Time: "2026-01-01T00:00:00Z"}}}), "peer-1"))
	if sized.Anchor.FreshnessSize == nil || timed.Anchor.FreshnessTime == "" {
		t.Fatal("an anchor resolved under min_freshness states what it attains")
	}
	for _, env := range []Envelope{sized, timed} {
		if err := verifyRecordInclusion(env, checkpointKeyID(1)); err != nil {
			t.Fatal(err)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(mustEnvelopeOctets(t, e), &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["artifact"].(string); !present {
		t.Fatal("in JSON the artifact is base64url text")
	}
}

func mustEnvelopeOctets(t *testing.T, e Envelope) []byte {
	t.Helper()
	reply, err := encodeReply(MediaTypeJSON, &e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reply.Octets
}
