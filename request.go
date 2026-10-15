// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"slices"
	"time"
)

// RequestStatement is the body of the record a requester seals before it
// sends a request. The responder is named here, not in counterparty_ref:
// asking a party does not make it a counterparty of this book.
type RequestStatement struct {
	Request      json.RawMessage `json:"request"`
	ResponderRef string          `json:"responder_ref,omitempty"`
}

// SentRequest is a request as transmitted: its octets (JCS JSON), their
// digest, and the id of the record of having asked. RecordResponse and
// RecordAbsence re-derive everything else from that committed record.
type SentRequest struct {
	Bytes    []byte
	Digest   string
	RecordID string
}

// Message is the request as carried.
func (s SentRequest) Message() Message {
	return Message{Octets: s.Bytes, MediaType: MediaTypeJSON}
}

// Request records that this book is asking responder for evidence and
// returns the octets to transmit: the request map under this profile (its
// profile member set to ProfileID), in JCS (profile §6.1, P-9). An empty
// nonce is filled with a fresh random one, and a nonce this book already
// used is refused. Recording happens before sending, so "I asked" is
// committed whatever comes back.
func (b *Book) Request(ctx context.Context, req EvidenceRequest, responder string) (SentRequest, error) {
	req.Profile = ProfileID
	if req.Nonce == "" {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return SentRequest{}, err
		}
		req.Nonce = hex.EncodeToString(nonce)
	}
	data, err := EncodeRequest(req, MediaTypeJSON)
	if err != nil {
		return SentRequest{}, err
	}
	// The record holds the transmitted bytes exactly: encoding/json would
	// rewrite <, > and & inside them as \u escapes, and the request read back
	// from the record would no longer be the request that was sent.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(RequestStatement{Request: data, ResponderRef: responder}); err != nil {
		return SentRequest{}, err
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	b.mu.Lock()
	defer b.mu.Unlock()
	// A unique nonce gives every request unique bytes and so a unique digest;
	// a response to an earlier identical request can never be replayed onto it.
	for _, id := range b.order {
		if b.records[id].Header.RecordType != RecordTypeRequest {
			continue
		}
		if prior, err := b.sentLocked(id); err == nil && prior.request.Nonce == req.Nonce {
			return SentRequest{}, fmt.Errorf("%w: nonce already used by request %s", ErrInvalid, id)
		}
	}
	record, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeRequest, EpistemicType: ObservedEvent, Statement: body})
	if err != nil {
		return SentRequest{}, err
	}
	return SentRequest{Bytes: data, Digest: digestBytes(data), RecordID: record.RecordID}, nil
}

// sentRequest is a request re-read from its committed record.
type sentRequest struct {
	record    Record
	bytes     []byte
	digest    string
	request   EvidenceRequest
	floorTime time.Time
}

func (b *Book) sentLocked(requestRecordID string) (sentRequest, error) {
	record, ok := b.records[requestRecordID]
	if !ok || record.Header.RecordType != RecordTypeRequest {
		return sentRequest{}, fmt.Errorf("%w: %s is not a request this book recorded", ErrNotFound, requestRecordID)
	}
	var statement RequestStatement
	if err := json.Unmarshal(record.Header.Statement, &statement); err != nil {
		return sentRequest{}, fmt.Errorf("%w: request record %s: %v", ErrCorrupt, requestRecordID, err)
	}
	check := readRequest(Message{Octets: statement.Request, MediaType: MediaTypeJSON})
	if check.reason != "" {
		return sentRequest{}, fmt.Errorf("%w: request record %s holds no well-formed request: %s", ErrCorrupt, requestRecordID, check.reason)
	}
	return sentRequest{record: record, bytes: statement.Request, digest: digestBytes(statement.Request), request: check.request, floorTime: check.floorTime}, nil
}

// ResponseStatement is the body of the record a requester seals on receipt.
// KeyID is always the pinned responder key the reply verified under.
type ResponseStatement struct {
	RequestDigest string `json:"request_digest"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason,omitempty"`
	KeyID         string `json:"key_id"`
	CheckpointKey string `json:"checkpoint_key_id"`
	Artifact      string `json:"artifact_digest,omitempty"`
	Anchor        string `json:"anchor,omitempty"`
	// NotChecked names the profile properties (§7.2) this requester could
	// not check for a recorded artifact, for example anchor_inclusion where
	// the profile defines no material kind for the subject (P-13).
	NotChecked []string `json:"not_checked,omitempty"`
	Failure    string   `json:"failure,omitempty"`
}

// Response outcomes as recorded by a requester. Artifact and refusal are two
// of the request draft's three outcomes; the other two values are sub-states
// of a received reply that did not verify (a response received and failed,
// profile P-21), named as the request-draft vectors name them. None is ever
// an absence.
const (
	OutcomeArtifact             = "artifact"
	OutcomeRefusal              = "refusal"
	OutcomeArtifactFailed       = "artifact_verification_failed"
	OutcomeRefusalNonconforming = "refusal_nonconformant"
)

// ResponderKeys are the responder's keys as the requester obtained them,
// independently of any reply: Signer signs refusals and envelopes,
// Checkpoint signs the checkpoints an artifact is anchored to.
type ResponderKeys struct {
	Signer     string
	Checkpoint string
	// Witnesses is the requester's witness directory, used when it checks an
	// Evidence Bundle (VerifyBundleAnswer).
	Witnesses []WitnessRow
}

// RecordResponse verifies and records the reply that came back for the
// request recorded as requestRecordID. Both responder keys are required. A
// reply that is not signed under the pinned signer key for this request's
// digest is refused and is not recorded: an unauthenticated message can
// neither become an outcome nor foreclose the real one. A reply genuinely
// signed by the responder that fails a check of profile §7.2 is recorded as
// received-and-failed, never as a grant and never as an absence.
func (b *Book) RecordResponse(ctx context.Context, requestRecordID string, reply Message, keys ResponderKeys) (Record, error) {
	if keys.Signer == "" || keys.Checkpoint == "" {
		return Record{}, fmt.Errorf("%w: pinned responder signer and checkpoint keys are required", ErrInvalid)
	}
	parsed, err := ParseReply(reply)
	if err != nil {
		return Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sent, err := b.sentLocked(requestRecordID)
	if err != nil {
		return Record{}, err
	}
	statement := ResponseStatement{RequestDigest: sent.digest, KeyID: keys.Signer, CheckpointKey: keys.Checkpoint}
	switch {
	case parsed.Refusal != nil:
		r := parsed.Refusal
		if r.RequestDigest != sent.digest || r.KeyID != keys.Signer {
			return Record{}, fmt.Errorf("%w: refusal is not for this request under the pinned responder key", ErrInvalid)
		}
		if err := r.Verify(); err != nil {
			return Record{}, asInvalid(err)
		}
		statement.Outcome, statement.Reason = OutcomeRefusal, r.Reason
		if !r.Conforming() {
			statement.Outcome = OutcomeRefusalNonconforming
		}
	case parsed.Envelope != nil:
		e := parsed.Envelope
		if e.RequestDigest != sent.digest || e.Responder != keys.Signer {
			return Record{}, fmt.Errorf("%w: envelope is not for this request under the pinned responder key", ErrInvalid)
		}
		body, err := e.SigningBody()
		if err != nil {
			return Record{}, err
		}
		sig, err := hex.DecodeString(e.Sig)
		if err != nil || len(sig) != 64 {
			return Record{}, fmt.Errorf("%w: envelope signature is malformed", ErrInvalid)
		}
		if err := verifySignature(e.Responder, body, sig); err != nil {
			return Record{}, err
		}
		statement.Outcome, statement.Artifact, statement.Anchor = OutcomeArtifact, e.ArtifactDigest, e.Anchor.Digest
		notChecked, err := verifyEnvelopeContent(sent, *e, keys)
		statement.NotChecked = notChecked
		if err != nil {
			statement.Outcome, statement.Failure = OutcomeArtifactFailed, err.Error()
		}
	}
	body, err := json.Marshal(statement)
	if err != nil {
		return Record{}, err
	}
	if b.outcomeRecordedLocked(requestRecordID) {
		return Record{}, fmt.Errorf("%w: an outcome is already recorded for request %s", ErrInvalid, sent.digest)
	}
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeResponse, EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: requestRecordID}}, Statement: body})
}

// verifyEnvelopeContent runs the checks of profile §7.3 that follow the
// envelope signature: profile_version, issued_at_form, subject_binding,
// artifact_digest, verification_binding, anchor_match and anchor_inclusion.
// It returns the properties it could not check (never failures), and an
// error naming the first check that failed.
func verifyEnvelopeContent(sent sentRequest, e Envelope, keys ResponderKeys) ([]string, error) {
	req := sent.request
	if e.Profile != req.Profile {
		return nil, fmt.Errorf("%w: profile_version: requested %s, got %s", ErrInvalid, req.Profile, e.Profile)
	}
	if _, ok := evidencerequest.ParseProfileTime(e.IssuedAt); !ok {
		return nil, fmt.Errorf("%w: issued_at_form: %q", ErrInvalid, e.IssuedAt)
	}
	if e.Subject != req.Subject || e.Derivation != req.Derivation {
		return nil, fmt.Errorf("%w: subject_binding: the envelope answers a different subject or derivation", ErrInvalid)
	}
	if digestBytes(e.Artifact) != e.ArtifactDigest {
		return nil, fmt.Errorf("%w: artifact_digest: the artifact does not hash to the signed digest", ErrInvalid)
	}
	if len(e.Verification) != len(e.VerificationContent) {
		return nil, fmt.Errorf("%w: verification_binding: %d entries, %d items", ErrInvalid, len(e.Verification), len(e.VerificationContent))
	}
	for i, item := range e.VerificationContent {
		if digestBytes(item) != e.Verification[i].Digest {
			return nil, fmt.Errorf("%w: verification_binding: item %d", ErrInvalid, i)
		}
	}
	// anchor_match.
	if pin := req.Coverage.ExpectedPin; pin != "" {
		if e.Anchor.Digest != pin {
			return nil, fmt.Errorf("%w: anchor_match: the anchor is not the pinned one", ErrInvalid)
		}
	} else if floor := req.Coverage.MinFreshness; floor != nil {
		switch {
		case floor.Size != nil && (e.Anchor.FreshnessSize == nil || *e.Anchor.FreshnessSize < *floor.Size):
			return nil, fmt.Errorf("%w: anchor_match: the anchor does not state a size meeting the floor", ErrInvalid)
		case floor.Size == nil:
			attained, ok := evidencerequest.ParseProfileTime(e.Anchor.FreshnessTime)
			if !ok || attained.Before(sent.floorTime) {
				return nil, fmt.Errorf("%w: anchor_match: the anchor does not state a time meeting the floor", ErrInvalid)
			}
		}
	}
	return verifyAnchorInclusion(req, e, keys.Checkpoint)
}

// verifyAnchorInclusion checks the artifact against its anchor by the method
// its form allows (profile §6.2 point 4, §6.2.1).
func verifyAnchorInclusion(req EvidenceRequest, e Envelope, checkpointKey string) ([]string, error) {
	halfPin := req.Subject.Kind == SubjectExchange && req.Coverage.ExpectedPin == req.Subject.Digest
	switch {
	case req.Subject.Kind == SubjectRecord && req.Derivation == "":
		return nil, verifyRecordInclusion(e, checkpointKey)
	case req.Subject.Kind == SubjectCheckpoints || req.Derivation == DerivationHistoryCard:
		return nil, verifyCheckpointChain(e, checkpointKey)
	}
	// Record sets and registered derivations: the profile defines no material
	// kind for them (P-13). Their bodies and their anchor member are checked;
	// inclusion is not.
	artifact, err := evidencerequest.DecodeJSON(e.Artifact)
	fields, ok := artifact.(map[string]any)
	if err != nil || !ok {
		return nil, fmt.Errorf("%w: anchor_inclusion: the artifact is not a JSON object", ErrInvalid)
	}
	if anchor, _ := fields["anchor"].(string); !halfPin && anchor != e.Anchor.Digest {
		return nil, fmt.Errorf("%w: anchor_inclusion: the artifact names another anchor", ErrInvalid)
	}
	records, _ := fields["records"].([]any)
	for i, item := range records {
		entry, _ := item.(map[string]any)
		digest, _ := entry["digest"].(string)
		if body, present := entry["body"]; present {
			text, _ := body.(string)
			raw, err := hex.DecodeString(text)
			if err != nil || digestBytes(raw) != digest {
				return nil, fmt.Errorf("%w: anchor_inclusion: record %d does not hash to its digest", ErrInvalid, i)
			}
		}
	}
	return []string{"anchor_inclusion"}, nil
}

// verifyRecordInclusion is the §6.2.1 procedure, steps 2 to 13, under the
// pinned checkpoint key.
func verifyRecordInclusion(e Envelope, checkpointKey string) error {
	items := map[string][]byte{}
	for i, entry := range e.Verification {
		if _, repeated := items[entry.Kind]; repeated {
			return fmt.Errorf("%w: anchor_inclusion: kind %s appears more than once", ErrInvalid, entry.Kind)
		}
		if !slices.Contains([]string{KindCLLCheckpoint, KindCLLInclusionProof, KindCLLConsistencyProof}, entry.Kind) {
			return fmt.Errorf("%w: anchor_inclusion: kind %q is not one this requester implements", ErrInvalid, entry.Kind)
		}
		items[entry.Kind] = e.VerificationContent[i]
	}
	if items[KindCLLCheckpoint] == nil || items[KindCLLInclusionProof] == nil {
		return fmt.Errorf("%w: anchor_inclusion: the checkpoint or the inclusion proof is not present", ErrInvalid)
	}
	if e.Subject.Digest != e.ArtifactDigest {
		return fmt.Errorf("%w: anchor_inclusion: the record is not named by its artifact digest", ErrInvalid)
	}
	if digestBytes(items[KindCLLCheckpoint]) != e.Anchor.Digest {
		return fmt.Errorf("%w: anchor_inclusion: the checkpoint is not the anchor", ErrInvalid)
	}
	cp, err := readSignedCheckpoint(items[KindCLLCheckpoint], checkpointKey)
	if err != nil {
		return fmt.Errorf("anchor_inclusion: %w", err)
	}
	if e.Anchor.FreshnessSize != nil && *e.Anchor.FreshnessSize != cp.TreeSize ||
		e.Anchor.FreshnessTime != "" && e.Anchor.FreshnessTime != evidencerequest.ProfileTime(cp.IssuedAt) {
		return fmt.Errorf("%w: anchor_inclusion: anchor.freshness is not what the checkpoint commits", ErrInvalid)
	}
	entry, err := hex.DecodeString(e.ArtifactDigest)
	if err != nil {
		return fmt.Errorf("%w: anchor_inclusion: artifact digest", ErrInvalid)
	}
	if err := checkCLLInclusion(cp, entry, items[KindCLLInclusionProof], items[KindCLLConsistencyProof]); err != nil {
		return fmt.Errorf("anchor_inclusion: %w", err)
	}
	return nil
}

// verifyCheckpointChain checks a checkpoints or history_card/1 artifact,
// which is its own verification material (ER §4.1): every checkpoint signed
// under the pinned key for one log, sizes strictly growing, each consistency
// proof present in a history card reaching the next commitment, and the
// last checkpoint the envelope's anchor.
func verifyCheckpointChain(e Envelope, checkpointKey string) error {
	artifact, err := evidencerequest.DecodeJSON(e.Artifact)
	fields, ok := artifact.(map[string]any)
	if err != nil || !ok {
		return fmt.Errorf("%w: anchor_inclusion: the artifact is not a JSON object", ErrInvalid)
	}
	var statements, proofs []string
	if history, isCard := fields["history"].([]any); isCard {
		for _, item := range history {
			entry, _ := item.(map[string]any)
			statement, _ := entry["checkpoint"].(string)
			proof, _ := entry["consistency_from_previous"].(string)
			statements, proofs = append(statements, statement), append(proofs, proof)
		}
	} else {
		list, _ := fields["checkpoints"].([]any)
		for _, item := range list {
			statement, _ := item.(string)
			statements, proofs = append(statements, statement), append(proofs, "")
		}
	}
	if len(statements) == 0 {
		return fmt.Errorf("%w: anchor_inclusion: no checkpoints", ErrInvalid)
	}
	var previous signedCheckpoint
	for i, text := range statements {
		raw, err := hex.DecodeString(text)
		if err != nil {
			return fmt.Errorf("%w: anchor_inclusion: checkpoint %d is not hex", ErrInvalid, i)
		}
		cp, err := readSignedCheckpoint(raw, checkpointKey)
		if err != nil {
			return fmt.Errorf("anchor_inclusion: checkpoint %d: %w", i, err)
		}
		if i > 0 {
			if cp.LogID != previous.LogID || cp.TreeSize <= previous.TreeSize {
				return fmt.Errorf("%w: anchor_inclusion: checkpoint %d does not extend the log", ErrInvalid, i)
			}
			if proofs[i] != "" {
				proof, err := hex.DecodeString(proofs[i])
				if err != nil {
					return fmt.Errorf("%w: anchor_inclusion: consistency proof %d is not hex", ErrInvalid, i)
				}
				if err := checkCLLConsistency(previous.commitment, cp.commitment, proof); err != nil {
					return fmt.Errorf("anchor_inclusion: checkpoint %d: %w", i, err)
				}
			} else if _, isCard := fields["history"]; isCard {
				return fmt.Errorf("%w: anchor_inclusion: history entry %d has no consistency proof", ErrInvalid, i)
			}
		}
		if i == len(statements)-1 && digestBytes(raw) != e.Anchor.Digest {
			return fmt.Errorf("%w: anchor_inclusion: the last checkpoint is not the anchor", ErrInvalid)
		}
		previous = cp
	}
	return nil
}

func (b *Book) outcomeRecordedLocked(requestRecordID string) bool {
	for _, id := range b.order {
		h := b.records[id].Header
		if (h.RecordType == RecordTypeResponse || h.RecordType == RecordTypeAbsence) && slices.Contains(h.LinksTo(Cites), requestRecordID) {
			return true
		}
	}
	return false
}

// AbsenceRecord is the requester's own record that neither an envelope nor a
// refusal arrived in its waiting window (ER §4.4, profile §6.4). It is the
// statement of a recorded_absence record, so the book's signature over the
// record attributes it to the requester; it carries no responder act.
type AbsenceRecord struct {
	Outcome       string        `json:"outcome"`
	RequestDigest string        `json:"request_digest"`
	Route         string        `json:"route,omitempty"`
	Window        AbsenceWindow `json:"window"`
	RecordedAt    string        `json:"recorded_at"`
}

// AbsenceWindow is when the request was transmitted and when the waiting
// window closed.
type AbsenceWindow struct {
	Opened string `json:"opened"`
	Closed string `json:"closed"`
}

// MinimumAbsenceWindow is the shortest waiting window an absence may claim,
// measured from when the request was recorded.
const MinimumAbsenceWindow = time.Minute

// RecordAbsence commits that no reply arrived for the recorded request
// between its recording and windowEnd. The request digest, its deadline and
// the window's opening come from the committed request record, and so does
// the route when the request named one: a different route is refused. When
// the request named none, route is the route the binding attempted, if any.
// It refuses while the request is still pending (window not closed, or
// closing before the request's own deadline) and once any reply has been
// recorded, so an outcome is never converted into another. The record's
// statement is the profile's absence record (AbsenceRecord), in JCS.
func (b *Book) RecordAbsence(ctx context.Context, requestRecordID string, windowEnd time.Time, route string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sent, err := b.sentLocked(requestRecordID)
	if err != nil {
		return Record{}, err
	}
	now := b.now()
	if windowEnd.After(now) {
		return Record{}, fmt.Errorf("%w: the waiting window has not closed; the request is pending", ErrInvalid)
	}
	asked, err := time.Parse(time.RFC3339Nano, sent.record.Header.CommittedAt)
	if err != nil {
		return Record{}, fmt.Errorf("%w: committed_at of request %s: %v", ErrCorrupt, requestRecordID, err)
	}
	if windowEnd.Before(asked.Add(MinimumAbsenceWindow)) {
		return Record{}, fmt.Errorf("%w: a waiting window must end at least %s after the request was recorded", ErrInvalid, MinimumAbsenceWindow)
	}
	if sent.request.Deadline != "" {
		deadline, ok := evidencerequest.ParseRequestTime(sent.request.Deadline)
		if !ok {
			return Record{}, fmt.Errorf("%w: committed deadline %q", ErrCorrupt, sent.request.Deadline)
		}
		if windowEnd.Before(deadline) {
			return Record{}, fmt.Errorf("%w: the window closes before the request's own deadline", ErrInvalid)
		}
	}
	if committed := sent.request.Route; committed != "" {
		if route != "" && route != committed {
			return Record{}, fmt.Errorf("%w: the request named route %q, not %q", ErrInvalid, committed, route)
		}
		route = committed
	}
	if b.outcomeRecordedLocked(requestRecordID) {
		return Record{}, fmt.Errorf("%w: an outcome is already recorded for request %s", ErrInvalid, sent.digest)
	}
	record := AbsenceRecord{
		Outcome: RecordTypeAbsence, RequestDigest: sent.digest, Route: route,
		Window:     AbsenceWindow{Opened: evidencerequest.ProfileTime(asked), Closed: evidencerequest.ProfileTime(windowEnd)},
		RecordedAt: evidencerequest.ProfileTime(now),
	}
	body, err := canonicalJSON(record)
	if err != nil {
		return Record{}, err
	}
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeAbsence, EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: requestRecordID}}, Statement: body})
}
