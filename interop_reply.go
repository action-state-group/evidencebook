// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/hex"
	"fmt"
	"slices"
)

// Verification-material kinds (profile §6.2.1). They name the Checkpointed
// Local Log mechanism's proof formats and apply to the record subject only.
const (
	KindCLLCheckpoint       = "cll-checkpoint"
	KindCLLInclusionProof   = "cll-inclusion-proof"
	KindCLLConsistencyProof = "cll-consistency-proof"
)

// Refusal is a signed answer declining one request (ER §4.2, profile §6.3).
// The signature covers exactly the JCS of request_digest, reason and
// issued_at (ERV A7); key_id, sig and detail are outside it. A refusal
// carries no profile member, so it is the same object under the request
// draft alone and under the profile.
type Refusal struct {
	RequestDigest string `json:"request_digest"`
	Reason        string `json:"reason"`
	IssuedAt      string `json:"issued_at"`
	KeyID         string `json:"key_id"`
	Sig           string `json:"sig"`
	// Detail is optional human-readable text. The token, not the prose, is
	// the answer (ER §4.2, P-6).
	Detail string `json:"detail,omitempty"`
}

// SigningBody is the exact bytes a refusal signature covers.
func (r Refusal) SigningBody() ([]byte, error) {
	return toJCS(map[string]any{"request_digest": r.RequestDigest, "reason": r.Reason, "issued_at": r.IssuedAt})
}

// Verify checks the refusal signature offline against its own key_id. It
// does not check that the reason is registered: a valid signature over an
// unregistered token is still the responder's signed act (ERV A13).
func (r Refusal) Verify() error {
	body, err := r.SigningBody()
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(r.Sig)
	if err != nil || len(sig) != 64 || hex.EncodeToString(sig) != r.Sig {
		return fmt.Errorf("%w: refusal signature is not 128 lowercase hex characters", ErrInvalid)
	}
	return verifySignature(r.KeyID, body, sig)
}

// Conforming reports whether a verified refusal is a conforming one: exactly
// one registered reason, exact case, and well-formed members (profile §7.2
// reason_registered, issued_at_form).
func (r Refusal) Conforming() bool {
	_, timeOK := parseProfileTime(r.IssuedAt)
	return slices.Contains(RefusalReasons, r.Reason) && timeOK && hex64.MatchString(r.RequestDigest)
}

func (r Refusal) value() map[string]any {
	out := map[string]any{"request_digest": r.RequestDigest, "reason": r.Reason, "issued_at": r.IssuedAt, "key_id": r.KeyID, "sig": r.Sig}
	if r.Detail != "" {
		out["detail"] = r.Detail
	}
	return out
}

// Anchor is the resolved coverage anchor an envelope names (profile §6.2).
// Under min_freshness it also states what the anchor attains, in the type
// the request used: FreshnessSize for a size floor, FreshnessTime for a time.
type Anchor struct {
	Digest        string
	FreshnessSize *uint64
	FreshnessTime string
}

func (a Anchor) value() map[string]any {
	out := map[string]any{"digest": a.Digest}
	switch {
	case a.FreshnessSize != nil:
		out["freshness"] = *a.FreshnessSize
	case a.FreshnessTime != "":
		out["freshness"] = a.FreshnessTime
	}
	return out
}

// VerificationEntry names one item of verification material by kind and by
// the SHA-256 of its octets.
type VerificationEntry struct {
	Kind   string
	Digest string
}

// Envelope is the signed artifact response (profile §6.2). It binds the
// canonical Artifact, named by ArtifactDigest, to one request, one resolved
// anchor and the responder's key. Artifact and VerificationContent travel
// beside the signed members; their digests are what is signed.
type Envelope struct {
	Profile             string
	RequestDigest       string
	Subject             Subject
	Derivation          string
	Anchor              Anchor
	ArtifactDigest      string
	Verification        []VerificationEntry
	Responder           string
	IssuedAt            string
	Sig                 string
	Artifact            []byte
	VerificationContent [][]byte
}

// signedValue is the object of the signed members that are present.
func (e Envelope) signedValue() map[string]any {
	out := map[string]any{
		"profile": e.Profile, "request_digest": e.RequestDigest, "subject": e.Subject.value(),
		"anchor": e.Anchor.value(), "artifact_digest": e.ArtifactDigest, "responder": e.Responder, "issued_at": e.IssuedAt,
	}
	if e.Derivation != "" {
		out["derivation"] = e.Derivation
	}
	if len(e.Verification) > 0 {
		entries := make([]any, len(e.Verification))
		for i, entry := range e.Verification {
			entries[i] = map[string]any{"kind": entry.Kind, "digest": entry.Digest}
		}
		out["verification"] = entries
	}
	return out
}

// SigningBody is UTF-8 of JCS over the signed members present (profile
// §6.2): the same bytes whether the envelope travels as JSON or CBOR.
func (e Envelope) SigningBody() ([]byte, error) {
	return toJCS(e.signedValue())
}

func (e Envelope) value() map[string]any {
	out := e.signedValue()
	out["sig"] = e.Sig
	out["artifact"] = e.Artifact
	if len(e.VerificationContent) > 0 {
		items := make([]any, len(e.VerificationContent))
		for i, item := range e.VerificationContent {
			items[i] = item
		}
		out["verification_content"] = items
	}
	return out
}

// Verify checks the envelope signature under its own responder key, that
// the artifact hashes to the signed digest, and that each verification item
// hashes to the digest at its position. It does not decide whether the key
// is trusted or whether the artifact answers the request.
func (e Envelope) Verify() error {
	body, err := e.SigningBody()
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(e.Sig)
	if err != nil || len(sig) != 64 || hex.EncodeToString(sig) != e.Sig {
		return fmt.Errorf("%w: envelope signature is not 128 lowercase hex characters", ErrInvalid)
	}
	if err := verifySignature(e.Responder, body, sig); err != nil {
		return err
	}
	if digestBytes(e.Artifact) != e.ArtifactDigest {
		return fmt.Errorf("%w: artifact does not hash to its signed digest", ErrInvalid)
	}
	if len(e.Verification) != len(e.VerificationContent) {
		return fmt.Errorf("%w: %d verification entries but %d items", ErrInvalid, len(e.Verification), len(e.VerificationContent))
	}
	for i, item := range e.VerificationContent {
		if digestBytes(item) != e.Verification[i].Digest {
			return fmt.Errorf("%w: verification item %d does not hash to its signed digest", ErrInvalid, i)
		}
	}
	return nil
}

// Reply is what a responder sends for one request: exactly one of an
// envelope or a refusal, encoded in the request's own encoding and labelled
// with its media type (profile §11.4.1 rule 3).
type Reply struct {
	Message
	Envelope *Envelope
	Refusal  *Refusal
}

func encodeReply(mediaType string, envelope *Envelope, refusal *Refusal) (Reply, error) {
	var value map[string]any
	switch {
	case envelope != nil && refusal == nil:
		value = envelope.value()
	case refusal != nil && envelope == nil:
		value = refusal.value()
	default:
		return Reply{}, fmt.Errorf("%w: a reply is exactly one of an envelope or a refusal", ErrInvalid)
	}
	octets, err := encodeIn(mediaType, value)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Message: Message{Octets: octets, MediaType: mediaType}, Envelope: envelope, Refusal: refusal}, nil
}

// ParseReply reads reply octets as carried (profile §1 "Message type"): a
// map carrying artifact_digest is an envelope, one carrying reason is a
// refusal. A map with a duplicate member, one carrying both or neither, or
// one whose members have the wrong types is not a reply this package can
// authenticate, and ParseReply returns an error.
func ParseReply(m Message) (Reply, error) {
	root, err := decodeMessage(m)
	if err != nil {
		return Reply{}, fmt.Errorf("%w: reply: %v", ErrInvalid, err)
	}
	fields, ok := root.(map[string]any)
	if !ok {
		return Reply{}, fmt.Errorf("%w: reply is not a map", ErrInvalid)
	}
	_, isEnvelope := fields["artifact_digest"]
	_, isRefusal := fields["reason"]
	switch {
	case isEnvelope && isRefusal:
		return Reply{}, fmt.Errorf("%w: reply carries both artifact_digest and reason", ErrInvalid)
	case isRefusal:
		refusal, err := readRefusal(fields)
		if err != nil {
			return Reply{}, err
		}
		return Reply{Message: m, Refusal: &refusal}, nil
	case isEnvelope:
		envelope, err := readEnvelope(fields, m.MediaType)
		if err != nil {
			return Reply{}, err
		}
		return Reply{Message: m, Envelope: &envelope}, nil
	}
	return Reply{}, fmt.Errorf("%w: reply is neither an envelope nor a refusal; a transport condition", ErrInvalid)
}

func textMembers(fields map[string]any, required []string, optional []string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range append(slices.Clone(required), optional...) {
		raw, present := fields[name]
		if !present {
			if slices.Contains(required, name) {
				return nil, fmt.Errorf("%w: %s is missing", ErrInvalid, name)
			}
			continue
		}
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s is not a text string", ErrInvalid, name)
		}
		out[name] = text
	}
	return out, nil
}

func readRefusal(fields map[string]any) (Refusal, error) {
	text, err := textMembers(fields, []string{"request_digest", "reason", "issued_at", "key_id", "sig"}, []string{"detail"})
	if err != nil {
		return Refusal{}, err
	}
	return Refusal{RequestDigest: text["request_digest"], Reason: text["reason"], IssuedAt: text["issued_at"], KeyID: text["key_id"], Sig: text["sig"], Detail: text["detail"]}, nil
}

func readEnvelope(fields map[string]any, mediaType string) (Envelope, error) {
	text, err := textMembers(fields, []string{"profile", "request_digest", "artifact_digest", "responder", "issued_at", "sig"}, []string{"derivation"})
	if err != nil {
		return Envelope{}, err
	}
	e := Envelope{Profile: text["profile"], RequestDigest: text["request_digest"], ArtifactDigest: text["artifact_digest"], Responder: text["responder"], IssuedAt: text["issued_at"], Sig: text["sig"], Derivation: text["derivation"]}
	if _, present := fields["derivation"]; present && e.Derivation == "" {
		return Envelope{}, fmt.Errorf("%w: derivation is present and empty", ErrInvalid)
	}
	subject, ok := parseSubject(fields["subject"])
	if !ok {
		return Envelope{}, fmt.Errorf("%w: envelope subject is not one form with conforming content", ErrInvalid)
	}
	e.Subject = subject
	anchor, ok := fields["anchor"].(map[string]any)
	if !ok {
		return Envelope{}, fmt.Errorf("%w: anchor is not a map", ErrInvalid)
	}
	if e.Anchor.Digest, ok = anchor["digest"].(string); !ok {
		return Envelope{}, fmt.Errorf("%w: anchor.digest is not a text string", ErrInvalid)
	}
	if freshness, present := anchor["freshness"]; present {
		if t, isText := freshness.(string); isText {
			e.Anchor.FreshnessTime = t
		} else if size, ok := uintValue(freshness); ok {
			e.Anchor.FreshnessSize = &size
		} else {
			return Envelope{}, fmt.Errorf("%w: anchor.freshness is neither a size nor a time", ErrInvalid)
		}
	}
	for name := range anchor {
		if name != "digest" && name != "freshness" {
			return Envelope{}, fmt.Errorf("%w: anchor member %q is not digest or freshness", ErrInvalid, name)
		}
	}
	if raw, present := fields["verification"]; present {
		entries, ok := raw.([]any)
		if !ok {
			return Envelope{}, fmt.Errorf("%w: verification is not an array", ErrInvalid)
		}
		for _, item := range entries {
			entry, ok := item.(map[string]any)
			kind, kindOK := entry["kind"].(string)
			digest, digestOK := entry["digest"].(string)
			if !ok || !kindOK || !digestOK || len(entry) != 2 {
				return Envelope{}, fmt.Errorf("%w: a verification entry is not {kind, digest}", ErrInvalid)
			}
			e.Verification = append(e.Verification, VerificationEntry{Kind: kind, Digest: digest})
		}
	}
	if e.Artifact, ok = opaqueBytes(fields["artifact"], mediaType); !ok {
		return Envelope{}, fmt.Errorf("%w: artifact is not opaque bytes in this encoding", ErrInvalid)
	}
	if raw, present := fields["verification_content"]; present {
		items, ok := raw.([]any)
		if !ok {
			return Envelope{}, fmt.Errorf("%w: verification_content is not an array", ErrInvalid)
		}
		for _, item := range items {
			octets, ok := opaqueBytes(item, mediaType)
			if !ok {
				return Envelope{}, fmt.Errorf("%w: a verification_content item is not opaque bytes", ErrInvalid)
			}
			e.VerificationContent = append(e.VerificationContent, octets)
		}
	}
	return e, nil
}
