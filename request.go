// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SubjectKind is one of the request draft's six subject forms.
type SubjectKind string

const (
	SubjectFullHistory SubjectKind = "full_history"
	SubjectCheckpoints SubjectKind = "checkpoints"
	SubjectRecord      SubjectKind = "record"
	SubjectRange       SubjectKind = "range"
	SubjectCorrelation SubjectKind = "correlation"
	SubjectExchange    SubjectKind = "exchange"
)

// Refusal reason tokens (the request draft's initial registry).
const (
	ReasonNotAuthorized         = "not_authorized"
	ReasonNoSuchSubject         = "no_such_subject"
	ReasonCoverageUnsatisfiable = "coverage_unsatisfiable"
	ReasonDerivationUnsupported = "derivation_unsupported"
	ReasonPolicyDeclined        = "policy_declined"
	ReasonDeadlineUnmet         = "deadline_unmet"
	ReasonRequestMalformed      = "request_malformed"
	ReasonRetentionExpired      = "retention_expired"
)

// Subject names what is asked for. Digest serves record and exchange, First
// and Last serve range, Value serves correlation.
type Subject struct {
	Kind   SubjectKind `json:"kind"`
	Digest string      `json:"digest,omitempty"`
	First  uint64      `json:"first,omitempty"`
	Last   uint64      `json:"last,omitempty"`
	Value  string      `json:"value,omitempty"`
}

// Pin names a checkpoint the requester already holds.
type Pin struct {
	Root    string `json:"root"`
	MMRSize uint64 `json:"mmr_size"`
}

// Freshness is a minimum coverage: a log size, a time, or both.
type Freshness struct {
	Size uint64 `json:"size,omitempty"`
	Time string `json:"time,omitempty"`
}

// Coverage carries exactly one of ExpectedPin or MinFreshness.
type Coverage struct {
	ExpectedPin  *Pin       `json:"expected_pin,omitempty"`
	MinFreshness *Freshness `json:"min_freshness,omitempty"`
}

// EvidenceRequest is the request map. Nonce and Route never influence the
// artifact served.
type EvidenceRequest struct {
	Subject    Subject  `json:"subject"`
	Coverage   Coverage `json:"coverage"`
	Derivation string   `json:"derivation,omitempty"`
	Deadline   string   `json:"deadline,omitempty"`
	Nonce      string   `json:"nonce,omitempty"`
	Route      string   `json:"route,omitempty"`
}

// Refusal is a signed answer declining one request. The signature covers the
// canonical JSON of request_digest, reason and issued_at.
type Refusal struct {
	RequestDigest string `json:"request_digest"`
	Reason        string `json:"reason"`
	IssuedAt      string `json:"issued_at"`
	KeyID         string `json:"key_id"`
	Sig           string `json:"sig"`
}

// SigningBody is the exact bytes a refusal signature covers.
func (r Refusal) SigningBody() ([]byte, error) {
	return canonicalJSON(map[string]string{"request_digest": r.RequestDigest, "reason": r.Reason, "issued_at": r.IssuedAt})
}

// Verify checks the refusal signature offline against its own key_id.
func (r Refusal) Verify() error {
	body, err := r.SigningBody()
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(r.Sig)
	if err != nil {
		return fmt.Errorf("%w: refusal signature is not hex", ErrInvalid)
	}
	return verifySignature(r.KeyID, body, sig)
}

// Artifact kinds an artifact response may carry.
const (
	ArtifactEvidenceBundle = "evidence-bundle/v2"
	ArtifactCheckpoints    = "checkpoints/v1"
)

// ArtifactResponse is the signed granting answer: the artifact, the anchor it
// verifies against, and a signature binding both to the request.
type ArtifactResponse struct {
	RequestDigest  string          `json:"request_digest"`
	Anchor         string          `json:"anchor"`
	ArtifactKind   string          `json:"artifact_kind"`
	Artifact       json.RawMessage `json:"artifact"`
	ArtifactDigest string          `json:"artifact_digest"`
	IssuedAt       string          `json:"issued_at"`
	KeyID          string          `json:"key_id"`
	Sig            string          `json:"sig"`
}

// SigningBody is the exact bytes an artifact response signature covers.
func (a ArtifactResponse) SigningBody() ([]byte, error) {
	return canonicalJSON(map[string]string{"request_digest": a.RequestDigest, "anchor": a.Anchor, "artifact_kind": a.ArtifactKind, "artifact_digest": a.ArtifactDigest, "issued_at": a.IssuedAt})
}

// Verify checks the signature and that the artifact bytes hash to the signed digest.
func (a ArtifactResponse) Verify() error {
	if digestBytes(a.Artifact) != a.ArtifactDigest {
		return fmt.Errorf("%w: artifact does not hash to its signed digest", ErrInvalid)
	}
	body, err := a.SigningBody()
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(a.Sig)
	if err != nil {
		return fmt.Errorf("%w: artifact signature is not hex", ErrInvalid)
	}
	return verifySignature(a.KeyID, body, sig)
}

// Response is exactly one of the two answers a responder can send.
type Response struct {
	Artifact *ArtifactResponse `json:"artifact,omitempty"`
	Refusal  *Refusal          `json:"refusal,omitempty"`
}

// SharePolicy is the four-switch sharing policy, carried unchanged from the
// mesh plugin's share.* object. Every default keys on relationship.
type SharePolicy struct {
	RecordAtCompletion string `json:"record_at_completion"`
	HistorySegments    string `json:"history_segments"`
	Adjudications      string `json:"adjudications"`
	Witness            string `json:"witness,omitempty"`
}

// DefaultSharePolicy returns the documented defaults.
func DefaultSharePolicy() SharePolicy {
	return SharePolicy{RecordAtCompletion: "counterparty", HistorySegments: "prospective", Adjudications: "deliver_to_subjects"}
}

// Validate rejects any switch outside its enumerated values.
func (p SharePolicy) Validate() error {
	checks := []struct {
		name, value string
		allowed     []string
	}{
		{"record_at_completion", p.RecordAtCompletion, []string{"counterparty", "off"}},
		{"history_segments", p.HistorySegments, []string{"counterparties", "prospective", "peers", "off"}},
		{"adjudications", p.Adjudications, []string{"deliver_to_subjects", "off"}},
	}
	for _, check := range checks {
		if !slices.Contains(check.allowed, check.value) {
			return fmt.Errorf("%w: share.%s=%q is not one of %v", ErrInvalid, check.name, check.value, check.allowed)
		}
	}
	return nil
}

// Relationship of a requester to this book, checkable from local records.
const (
	RelationshipStranger     = "stranger"
	RelationshipIdentified   = "identified"
	RelationshipCounterparty = "counterparty"
)

func (p SharePolicy) allows(relationship string) bool {
	switch p.HistorySegments {
	case "peers":
		return true
	case "prospective":
		return relationship != RelationshipStranger
	case "counterparties":
		return relationship == RelationshipCounterparty
	}
	return false
}

// RespondOptions carries the requester's declared identity and the policy.
// RequesterID is whatever the transport supplied; this package does not
// authenticate it. The relationship gate built on it is scope reduction, not
// access control: a caller that can claim another party's identity gets that
// party's answers, so authenticate the requester at the transport when the
// difference matters.
type RespondOptions struct {
	RequesterID string
	Policy      SharePolicy
}

// AnsweredStatement is the body of the record a responder seals for every
// inbound request, whatever the outcome.
type AnsweredStatement struct {
	RequestDigest string      `json:"request_digest"`
	SubjectKind   SubjectKind `json:"subject_kind,omitempty"`
	Outcome       string      `json:"outcome"`
	Reason        string      `json:"reason,omitempty"`
	RequesterRef  string      `json:"requester_ref,omitempty"`
	Relationship  string      `json:"relationship"`
	Anchor        string      `json:"anchor,omitempty"`
}

// Respond answers one request received as requestBytes with a signed artifact
// or a signed refusal, and seals a record of the request and its outcome.
// The artifact depends only on (subject, coverage): nonce, route and the
// requester's identity never change it.
func (b *Book) Respond(ctx context.Context, requestBytes []byte, opts RespondOptions) (Response, Record, error) {
	if err := opts.Policy.Validate(); err != nil {
		return Response{}, Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	requestDigest := digestBytes(requestBytes)
	statement := AnsweredStatement{RequestDigest: requestDigest, RequesterRef: opts.RequesterID, Relationship: b.relationshipLocked(opts.RequesterID)}
	response, err := b.answerLocked(ctx, requestBytes, requestDigest, opts, &statement)
	if err != nil {
		return Response{}, Record{}, err
	}
	body, err := json.Marshal(statement)
	if err != nil {
		return Response{}, Record{}, err
	}
	record, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeRequestAnswered, EpistemicType: ObservedEvent, Links: []Link{}, Statement: body})
	if err != nil {
		return Response{}, Record{}, err
	}
	return response, record, nil
}

// relationshipLocked classifies by counterparty_ref on the book's own records.
// Records of answered requests carry the requester in their statement, not in
// counterparty_ref, so asking never makes a stranger a counterparty.
func (b *Book) relationshipLocked(requesterID string) string {
	if requesterID == "" {
		return RelationshipStranger
	}
	for _, id := range b.order {
		if b.records[id].Header.CounterpartyRef == requesterID {
			return RelationshipCounterparty
		}
	}
	return RelationshipIdentified
}

func (b *Book) answerLocked(ctx context.Context, requestBytes []byte, requestDigest string, opts RespondOptions, statement *AnsweredStatement) (Response, error) {
	refuse := func(reason string) (Response, error) {
		refusal, err := b.refusalLocked(ctx, requestDigest, reason)
		statement.Outcome, statement.Reason = "refusal", reason
		return Response{Refusal: &refusal}, err
	}
	var req EvidenceRequest
	decoder := json.NewDecoder(bytes.NewReader(requestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || !validSubject(req.Subject) {
		return refuse(ReasonRequestMalformed)
	}
	statement.SubjectKind = req.Subject.Kind
	if (req.Coverage.ExpectedPin == nil) == (req.Coverage.MinFreshness == nil) {
		return refuse(ReasonCoverageUnsatisfiable)
	}
	var notBefore time.Time
	if floor := req.Coverage.MinFreshness; floor != nil && floor.Time != "" {
		var err error
		if notBefore, err = time.Parse(time.RFC3339Nano, floor.Time); err != nil {
			return refuse(ReasonRequestMalformed)
		}
		if notBefore.After(b.now()) {
			// No checkpoint can be issued in the future; refuse without
			// doing any work on the requester's behalf.
			return refuse(ReasonCoverageUnsatisfiable)
		}
	}
	if req.Derivation != "" {
		return refuse(ReasonDerivationUnsupported)
	}
	if req.Subject.Kind != SubjectCheckpoints && !opts.Policy.allows(statement.Relationship) {
		return refuse(ReasonNotAuthorized)
	}
	anchor, ok, err := b.coverageLocked(ctx, req.Coverage, notBefore)
	if err != nil {
		return Response{}, err
	}
	if !ok {
		return refuse(ReasonCoverageUnsatisfiable)
	}
	statement.Anchor = anchor.ID
	var kind string
	var artifact []byte
	if req.Subject.Kind == SubjectCheckpoints {
		kind = ArtifactCheckpoints
		if artifact, err = b.checkpointsArtifactLocked(ctx, anchor); err != nil {
			return Response{}, err
		}
	} else {
		matched, reason := b.resolveSubjectLocked(req.Subject, anchor)
		if reason != "" {
			return refuse(reason)
		}
		kind = ArtifactEvidenceBundle
		subject, err := json.Marshal(struct {
			Subject Subject  `json:"subject"`
			Matched []string `json:"matched"`
		}{req.Subject, matched})
		if err != nil {
			return Response{}, err
		}
		// Headers only: an answer discloses what was matched and what it
		// cites, and never payload bytes.
		bundle, err := b.bundleLocked(ctx, BundleRequest{
			Root: matched[len(matched)-1], Include: matched, ClosureDepth: 1, Payloads: PayloadsSelected,
			WithholdPayloads: true, At: &anchor, Extensions: map[string]json.RawMessage{ExtensionSubject: subject},
		})
		if err != nil {
			return Response{}, err
		}
		artifact = bundle.JSON
	}
	response := ArtifactResponse{RequestDigest: requestDigest, Anchor: anchor.ID, ArtifactKind: kind, Artifact: artifact, ArtifactDigest: digestBytes(artifact), IssuedAt: b.now().UTC().Format(time.RFC3339Nano), KeyID: KeyID(b.signer)}
	body, err := response.SigningBody()
	if err != nil {
		return Response{}, err
	}
	sig, err := b.signer.Sign(ctx, body)
	if err != nil {
		return Response{}, err
	}
	response.Sig = hex.EncodeToString(sig)
	statement.Outcome = "artifact"
	return Response{Artifact: &response}, nil
}

func validSubject(s Subject) bool {
	switch s.Kind {
	case SubjectFullHistory, SubjectCheckpoints:
		return s.Digest == "" && s.Value == "" && s.First == 0 && s.Last == 0
	case SubjectRecord, SubjectExchange:
		return hex64.MatchString(s.Digest)
	case SubjectRange:
		return s.First >= 1 && s.Last >= s.First
	case SubjectCorrelation:
		return s.Value != ""
	}
	return false
}

func (b *Book) refusalLocked(ctx context.Context, requestDigest, reason string) (Refusal, error) {
	refusal := Refusal{RequestDigest: requestDigest, Reason: reason, IssuedAt: b.now().UTC().Format(time.RFC3339Nano), KeyID: KeyID(b.signer)}
	body, err := refusal.SigningBody()
	if err != nil {
		return Refusal{}, err
	}
	sig, err := b.signer.Sign(ctx, body)
	if err != nil {
		return Refusal{}, err
	}
	refusal.Sig = hex.EncodeToString(sig)
	return refusal, nil
}

// coverageLocked resolves the anchor. A pin must name a checkpoint this book
// issued; a freshness floor may trigger a new checkpoint. Weaker coverage is
// never substituted for the coverage asked for.
func (b *Book) coverageLocked(ctx context.Context, coverage Coverage, notBefore time.Time) (Checkpoint, bool, error) {
	history, err := b.store.Checkpoints(ctx)
	if err != nil {
		return Checkpoint{}, false, err
	}
	if pin := coverage.ExpectedPin; pin != nil {
		for _, cp := range history {
			if cp.Root == pin.Root && cp.TreeSize == pin.MMRSize {
				return cp, true, nil
			}
		}
		return Checkpoint{}, false, nil
	}
	floor := coverage.MinFreshness
	if floor.Size > uint64(len(b.order)) {
		return Checkpoint{}, false, nil
	}
	fresh := func(cp Checkpoint) bool { return cp.Entries >= floor.Size && !cp.IssuedAt.Before(notBefore) }
	if n := len(history); n > 0 && fresh(history[n-1]) {
		return history[n-1], true, nil
	}
	if len(b.order) == 0 {
		return Checkpoint{}, false, nil
	}
	cp, err := b.checkpointLocked(ctx)
	if err != nil {
		return Checkpoint{}, false, err
	}
	return cp, fresh(cp), nil
}

// resolveSubjectLocked returns the records the subject names under anchor,
// or a refusal reason. A subject that exists only beyond the anchor is
// coverage_unsatisfiable, never no_such_subject: the book does hold it.
func (b *Book) resolveSubjectLocked(s Subject, anchor Checkpoint) ([]string, string) {
	if s.Kind == SubjectRange && s.Last > anchor.Entries {
		if s.Last <= uint64(len(b.order)) {
			return nil, ReasonCoverageUnsatisfiable
		}
		return nil, ReasonNoSuchSubject
	}
	var matched []string
	beyond := false
	for _, id := range b.order {
		record := b.records[id]
		var hit bool
		switch s.Kind {
		case SubjectFullHistory:
			hit = true
		case SubjectRecord:
			hit = id == s.Digest
		case SubjectRange:
			hit = record.Seq >= s.First && record.Seq <= s.Last
		case SubjectCorrelation:
			hit = record.Header.SubjectRef == s.Value
		case SubjectExchange:
			hit = slices.Contains(record.Header.LinksTo(Cites), s.Digest)
		}
		switch {
		case hit && anchor.Covers(record.Seq):
			matched = append(matched, id)
		case hit:
			beyond = true
		}
	}
	switch {
	case len(matched) > 0:
		return matched, ""
	case beyond:
		return nil, ReasonCoverageUnsatisfiable
	}
	return nil, ReasonNoSuchSubject
}

type checkpointsArtifact struct {
	Checkpoints []Checkpoint          `json:"checkpoints"`
	Consistency []ConsistencyEvidence `json:"consistency"`
}

func (b *Book) checkpointsArtifactLocked(ctx context.Context, anchor Checkpoint) ([]byte, error) {
	history, err := b.store.Checkpoints(ctx)
	if err != nil {
		return nil, err
	}
	artifact := checkpointsArtifact{Consistency: []ConsistencyEvidence{}}
	for _, cp := range history {
		if cp.Entries > anchor.Entries {
			continue
		}
		if n := len(artifact.Checkpoints); n > 0 {
			proof, err := b.commitments.ProveConsistency(ctx, artifact.Checkpoints[n-1], cp)
			if err != nil && !errors.Is(err, ErrUnsupported) {
				return nil, err
			}
			if err == nil {
				artifact.Consistency = append(artifact.Consistency, proof)
			}
		}
		artifact.Checkpoints = append(artifact.Checkpoints, cp)
	}
	return canonicalJSON(artifact)
}

// RequestStatement is the body of the record a requester seals before it
// sends a request. The responder is named here, not in counterparty_ref:
// asking a party does not make it a counterparty of this book.
type RequestStatement struct {
	Request      json.RawMessage `json:"request"`
	ResponderRef string          `json:"responder_ref,omitempty"`
}

// SentRequest is a request as transmitted: its canonical bytes, their
// digest, and the id of the record of having asked. RecordResponse and
// RecordAbsence re-derive everything else from that committed record.
type SentRequest struct {
	Bytes    []byte
	Digest   string
	RecordID string
}

// Request records that this book is asking responder for evidence and
// returns the canonical bytes to transmit. Recording happens before sending,
// so "I asked" is committed whatever comes back.
func (b *Book) Request(ctx context.Context, req EvidenceRequest, responder string) (SentRequest, error) {
	if !validSubject(req.Subject) || (req.Coverage.ExpectedPin == nil) == (req.Coverage.MinFreshness == nil) {
		return SentRequest{}, fmt.Errorf("%w: request needs a valid subject and exactly one coverage member", ErrInvalid)
	}
	if req.Deadline != "" {
		if _, err := time.Parse(time.RFC3339Nano, req.Deadline); err != nil {
			return SentRequest{}, fmt.Errorf("%w: deadline: %v", ErrInvalid, err)
		}
	}
	data, err := canonicalJSON(req)
	if err != nil {
		return SentRequest{}, err
	}
	body, err := json.Marshal(RequestStatement{Request: data, ResponderRef: responder})
	if err != nil {
		return SentRequest{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	record, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeRequest, EpistemicType: ObservedEvent, Statement: body})
	if err != nil {
		return SentRequest{}, err
	}
	return SentRequest{Bytes: data, Digest: digestBytes(data), RecordID: record.RecordID}, nil
}

// sentLocked re-reads a request from its committed record.
type sentRequest struct {
	record  Record
	bytes   []byte
	digest  string
	request EvidenceRequest
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
	out := sentRequest{record: record, bytes: statement.Request, digest: digestBytes(statement.Request)}
	if err := json.Unmarshal(statement.Request, &out.request); err != nil {
		return sentRequest{}, fmt.Errorf("%w: request record %s: %v", ErrCorrupt, requestRecordID, err)
	}
	return out, nil
}

// ResponseStatement is the body of the record a requester seals on receipt.
type ResponseStatement struct {
	RequestDigest string `json:"request_digest"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason,omitempty"`
	KeyID         string `json:"key_id,omitempty"`
	KeyPinned     bool   `json:"key_pinned"`
	ArtifactKind  string `json:"artifact_kind,omitempty"`
	Artifact      string `json:"artifact_digest,omitempty"`
	Anchor        string `json:"anchor,omitempty"`
	Failure       string `json:"failure,omitempty"`
}

// Response outcomes as recorded by a requester.
const (
	OutcomeArtifact       = "artifact"
	OutcomeRefusal        = "refusal"
	OutcomeArtifactFailed = "artifact_failed_verification"
)

// RecordResponse verifies and records what came back for the request
// recorded as requestRecordID. responderKeyID, when set, is the key the
// requester expects the responder to sign with; a response under any other
// key is refused. An artifact that fails verification is recorded as
// received-and-failed, never as a grant and never as an absence.
func (b *Book) RecordResponse(ctx context.Context, requestRecordID string, resp Response, responderKeyID string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sent, err := b.sentLocked(requestRecordID)
	if err != nil {
		return Record{}, err
	}
	statement := ResponseStatement{RequestDigest: sent.digest, KeyPinned: responderKeyID != ""}
	switch {
	case resp.Refusal != nil && resp.Artifact == nil:
		if resp.Refusal.RequestDigest != sent.digest {
			return Record{}, fmt.Errorf("%w: refusal names a different request", ErrInvalid)
		}
		if err := resp.Refusal.Verify(); err != nil {
			return Record{}, err
		}
		statement.Outcome, statement.Reason, statement.KeyID = OutcomeRefusal, resp.Refusal.Reason, resp.Refusal.KeyID
	case resp.Artifact != nil && resp.Refusal == nil:
		a := resp.Artifact
		statement.Outcome, statement.KeyID, statement.ArtifactKind, statement.Artifact, statement.Anchor = OutcomeArtifact, a.KeyID, a.ArtifactKind, a.ArtifactDigest, a.Anchor
		if err := verifyArtifact(sent, *a); err != nil {
			statement.Outcome, statement.Failure = OutcomeArtifactFailed, err.Error()
		}
	default:
		return Record{}, fmt.Errorf("%w: a response carries exactly one of artifact or refusal", ErrInvalid)
	}
	if responderKeyID != "" && statement.KeyID != responderKeyID {
		return Record{}, fmt.Errorf("%w: response is signed by %s, not the expected responder key", ErrInvalid, statement.KeyID)
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

// verifyArtifact checks the signature, then that the artifact answers the
// request that was recorded: same subject, the pinned anchor or one at
// least as fresh as asked, and proofs that verify.
func verifyArtifact(sent sentRequest, a ArtifactResponse) error {
	if a.RequestDigest != sent.digest {
		return fmt.Errorf("%w: artifact names a different request", ErrInvalid)
	}
	if err := a.Verify(); err != nil {
		return err
	}
	coverage := sent.request.Coverage
	var anchor Pin
	var entries uint64
	switch a.ArtifactKind {
	case ArtifactCheckpoints:
		if sent.request.Subject.Kind != SubjectCheckpoints {
			return fmt.Errorf("%w: a checkpoints artifact does not answer a %s subject", ErrInvalid, sent.request.Subject.Kind)
		}
		var artifact checkpointsArtifact
		if err := json.Unmarshal(a.Artifact, &artifact); err != nil {
			return fmt.Errorf("%w: checkpoints artifact: %v", ErrInvalid, err)
		}
		if err := VerifyCheckpoints(artifact.Checkpoints, artifact.Consistency); err != nil {
			return err
		}
		last := artifact.Checkpoints[len(artifact.Checkpoints)-1]
		if last.ID != a.Anchor {
			return fmt.Errorf("%w: checkpoints do not end at the signed anchor", ErrInvalid)
		}
		anchor, entries = Pin{Root: last.Root, MMRSize: last.TreeSize}, last.Entries
	case ArtifactEvidenceBundle:
		verified, err := VerifyBundle(a.Artifact)
		if err != nil {
			return err
		}
		if !verified.AnchorAuthenticated || fmt.Sprintf("%s:%d", verified.Anchor.Root, verified.Anchor.MMRSize) != a.Anchor {
			return fmt.Errorf("%w: bundle is not covered by the signed anchor", ErrInvalid)
		}
		var subject struct {
			Subject Subject `json:"subject"`
		}
		if err := json.Unmarshal(verified.Extensions[ExtensionSubject], &subject); err != nil || subject.Subject != sent.request.Subject {
			return fmt.Errorf("%w: bundle answers a different subject", ErrInvalid)
		}
		anchor, entries = Pin{Root: verified.Anchor.Root, MMRSize: verified.Anchor.MMRSize}, verified.IntervalLast
	default:
		return fmt.Errorf("%w: unknown artifact kind %q", ErrInvalid, a.ArtifactKind)
	}
	if pin := coverage.ExpectedPin; pin != nil && *pin != anchor {
		return fmt.Errorf("%w: artifact anchor is not the pinned checkpoint", ErrInvalid)
	}
	if floor := coverage.MinFreshness; floor != nil && entries < floor.Size {
		return fmt.Errorf("%w: artifact anchor covers %d records, fewer than the %d asked for", ErrInvalid, entries, floor.Size)
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

// AbsenceStatement is the body of a recorded absence: the requester's own
// signed statement that no answer arrived in its window. It states nothing
// about the responder's intent.
type AbsenceStatement struct {
	RequestDigest string `json:"request_digest"`
	Route         string `json:"route,omitempty"`
	WindowStart   string `json:"window_start"`
	WindowEnd     string `json:"window_end"`
}

// RecordAbsence commits that no response arrived for the recorded request
// between its recording and windowEnd. The request, its deadline and the
// window start come from the committed request record. It refuses while the
// request is still pending (window not closed, or closing before the
// request's own deadline) and once any response has been recorded, so an
// outcome is never converted into another.
func (b *Book) RecordAbsence(ctx context.Context, requestRecordID string, windowEnd time.Time, route string) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sent, err := b.sentLocked(requestRecordID)
	if err != nil {
		return Record{}, err
	}
	if windowEnd.After(b.now()) {
		return Record{}, fmt.Errorf("%w: the waiting window has not closed; the request is pending", ErrInvalid)
	}
	if sent.request.Deadline != "" {
		deadline, err := time.Parse(time.RFC3339Nano, sent.request.Deadline)
		if err != nil {
			return Record{}, fmt.Errorf("%w: committed deadline: %v", ErrCorrupt, err)
		}
		if windowEnd.Before(deadline) {
			return Record{}, fmt.Errorf("%w: the window closes before the request's own deadline", ErrInvalid)
		}
	}
	if b.outcomeRecordedLocked(requestRecordID) {
		return Record{}, fmt.Errorf("%w: an outcome is already recorded for request %s", ErrInvalid, sent.digest)
	}
	body, err := json.Marshal(AbsenceStatement{RequestDigest: sent.digest, Route: route, WindowStart: sent.record.Header.CommittedAt, WindowEnd: windowEnd.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return Record{}, err
	}
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeAbsence, EpistemicType: ObservedEvent, Links: []Link{{Type: Cites, Target: requestRecordID}}, Statement: body})
}

// ParsePin parses the "<root-hex>:<mmr_size>" checkpoint identity form.
func ParsePin(id string) (Pin, error) {
	root, size, ok := strings.Cut(id, ":")
	n, err := strconv.ParseUint(size, 10, 64)
	if !ok || err != nil || !hex64.MatchString(root) {
		return Pin{}, fmt.Errorf("%w: checkpoint id %q", ErrInvalid, id)
	}
	return Pin{Root: root, MMRSize: n}, nil
}
