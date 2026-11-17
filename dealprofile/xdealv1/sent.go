// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	emit "github.com/action-state-group/capsule-emit/go"
)

// A sent journal record (design §3.1, §3.5): an offer, a decline or a
// cancellation leaves the shelf whole as a standalone Capsule, appended to no
// log, whose action_id is "<deal_id>/sent", fyi, with no chain and no
// evidence_record_header, carrying the record as its payload, so that its
// agent_input_digest is the record digest. It travels as a pair,
// {"record": the Capsule with its signature and key_id, "agent_input": the
// record}, the shape v0's standalone report uses (x-deal-report/v0).

// SentSuffix ends a sent record's action_id: "<deal_id>/sent".
const SentSuffix = "/sent"

// StageSent is a sent pair read on its own (VerifySent).
const StageSent = "sent"

// Pair is a sent record's pair from its sealed Capsule, its Producer
// Envelope and its payload (the record's JCS bytes): the Capsule's members
// with signature (the envelope, lowercase hex) and key_id (the signer's
// public key, lowercase hex) added, and the record as agent_input.
func Pair(capsule, envelope, payload []byte) ([]byte, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(capsule, &members); err != nil {
		return nil, fmt.Errorf("xdealv1: the Capsule is not a JSON object: %w", err)
	}
	var id struct {
		CapsuleID string `json:"capsule_id"`
	}
	if err := json.Unmarshal(capsule, &id); err != nil {
		return nil, err
	}
	verified, err := emit.VerifyEnvelope(id.CapsuleID, envelope)
	if err != nil {
		return nil, fmt.Errorf("xdealv1: the envelope: %w", err)
	}
	sig, _ := json.Marshal(hex.EncodeToString(envelope))
	key, _ := json.Marshal(hex.EncodeToString(verified.PublicKey))
	members["signature"], members["key_id"] = sig, key
	return json.Marshal(map[string]any{"record": members, "agent_input": json.RawMessage(payload)})
}

// Sent is what a verified sent pair shows: the record, its record digest,
// the Capsule's id and the key that signed it (lowercase hex).
type Sent struct {
	Record    Record
	Digest    string
	CapsuleID string
	KeyID     string
}

// OfferContext is the offer a sent decline or cancellation answers, when the
// reader holds it: the offer record and its signer's key (lowercase hex).
type OfferContext struct {
	Offer  Record
	Signer string
}

// VerifySent checks a sent pair on its own (§3.5, "How a third party checks
// a declined buyer's copy"): the Capsule verifies and its signature is the
// key_id's; it is a standalone sent Capsule (action_id "<deal_id>/sent",
// fyi, no references, no evidence_record_header); its agent_input_digest is
// SHA-256 over the JCS bytes of agent_input; and agent_input is a v1 propose,
// decline or cancellation that passes Check and whose deal_id is the
// action_id's. Given the offer, a decline must cite its record digest and be
// signed by the key the offer was sent to, and a cancellation must cite it
// and be signed by the offer's signer.
func VerifySent(pair []byte, offer *OfferContext) (Sent, error) {
	bad := func(format string, args ...any) (Sent, error) {
		return Sent{}, stageErr(StageSent, format, args...)
	}
	// Text read other than as written (a member given twice, a lone
	// surrogate) would let two readers see two records in one pair.
	if err := checkText(pair); err != nil {
		return Sent{}, err
	}
	var p struct {
		Record     map[string]json.RawMessage `json:"record"`
		AgentInput json.RawMessage            `json:"agent_input"`
	}
	if err := strictUnmarshal(pair, &p); err != nil || p.Record == nil || p.AgentInput == nil {
		return bad("not a pair {record, agent_input}: %v", err)
	}
	var sigHex, keyID string
	if err := errors.Join(json.Unmarshal(p.Record["signature"], &sigHex), json.Unmarshal(p.Record["key_id"], &keyID)); err != nil || sigHex == "" || keyID == "" {
		return bad("the Capsule carries no signature and key_id")
	}
	delete(p.Record, "signature")
	delete(p.Record, "key_id")
	capsule, err := json.Marshal(p.Record)
	if err != nil {
		return Sent{}, err
	}
	doc, err := decodeJSON(capsule)
	if err != nil {
		return bad("the Capsule: %v", err)
	}
	if capsule, err = canonical.JCS(doc); err != nil {
		return bad("the Capsule: %v", err)
	}
	result, err := emit.VerifyCapsule(capsule)
	if err != nil || !result.OK || result.CapsuleID == nil {
		return bad("the Capsule does not verify: %v", err)
	}
	envelope, err := hex.DecodeString(sigHex)
	if err != nil {
		return bad("the signature is not hex")
	}
	verified, err := emit.VerifyEnvelope(*result.CapsuleID, envelope)
	if err != nil {
		return bad("the signature does not verify: %v", err)
	}
	if hex.EncodeToString(verified.PublicKey) != keyID {
		return bad("the Capsule is signed by another key than its key_id")
	}
	var c struct {
		ActionID   string `json:"action_id"`
		ActionType string `json:"action_type"`
		References []any  `json:"references"`
		Model      struct {
			Compute struct {
				AgentInputDigest string `json:"agent_input_digest"`
			} `json:"compute_attestation"`
		} `json:"model_attestation"`
	}
	if err := json.Unmarshal(capsule, &c); err != nil {
		return Sent{}, err
	}
	_, header := p.Record["evidence_record_header"]
	chain, chained := p.Record["chain"]
	chained = chained && string(chain) != "null"
	if header || chained || c.ActionType != string(emit.ActionTypeFYI) || len(c.References) > 0 {
		return bad("not a standalone sent Capsule: fyi, no chain, no references, no evidence_record_header")
	}
	digest, err := Digest([]byte(p.AgentInput))
	if err != nil {
		return bad("agent_input: %v", err)
	}
	if c.Model.Compute.AgentInputDigest != digest {
		return bad("agent_input is not the record the Capsule commits (agent_input_digest)")
	}
	if err := Check([]byte(p.AgentInput)); err != nil {
		return Sent{}, err
	}
	r, err := Decode(p.AgentInput)
	if err != nil {
		return Sent{}, err
	}
	if c.ActionID != r.Block.DealID+SentSuffix {
		return bad("action_id %q is not the record's deal_id followed by %s", c.ActionID, SentSuffix)
	}
	ag, isAgree := asAgree(r.Body)
	switch {
	case r.Block.RecordType == TypePropose:
	case isAgree && (ag.Kind == KindDecline || ag.Kind == KindCancellation):
	default:
		return bad("a %s is not sent whole: only an offer, a decline or a cancellation is", r.Block.RecordType)
	}
	out := Sent{Record: r, Digest: digest, CapsuleID: *result.CapsuleID, KeyID: keyID}
	if offer == nil || !isAgree {
		return out, nil
	}
	if err := Check(offer.Offer); err != nil {
		return Sent{}, err
	}
	of, ok := asPropose(offer.Offer.Body)
	if !ok {
		return bad("the offer given is a %s", offer.Offer.Block.RecordType)
	}
	od, err := Digest(offer.Offer)
	if err != nil {
		return Sent{}, err
	}
	if ag.Offer != RefTo(od) || r.Block.DealID != offer.Offer.Block.DealID {
		return bad("the %s does not answer this offer", ag.Kind)
	}
	switch ag.Kind {
	case KindDecline:
		if !of.To.Present || of.To.V == nil || *of.To.V != keyID {
			return bad("a decline is signed by the key the offer was sent to")
		}
	case KindCancellation:
		if offer.Signer != keyID {
			return bad("a cancellation is signed by the offer's signer")
		}
	}
	return out, nil
}
