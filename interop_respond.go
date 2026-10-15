// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"slices"
	"sync"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
)

// Refusal policies a responder may declare (profile §11.4.2 refusal_policy).
const (
	// RefusalPolicySpecific answers the most specific registered token.
	RefusalPolicySpecific = "specific"
	// RefusalPolicyUniform answers policy_declined wherever a more specific
	// token would disclose what the responder's policy protects: whether a
	// subject exists, whether it is covered yet, and whether this requester
	// may see it (ER §4.2, §12).
	RefusalPolicyUniform = "uniform"
)

// Defaults of the responder's cost policy.
const (
	DefaultMaxRequestBytes = 64 << 10
	DefaultMaxAnswerItems  = 10_000
)

// RequestLimiter decides whether a requester may be answered now. The
// requester is the authenticated identity, or "" for every caller the
// transport did not authenticate. A request over the limit is refused
// policy_declined.
type RequestLimiter interface {
	Allow(requester string, now time.Time) bool
}

// ResponderPolicy is the responder's cost and abuse policy (FR-37). The zero
// value answers the most specific token with no rate limit.
type ResponderPolicy struct {
	// RefusalPolicy is RefusalPolicySpecific (the default when empty) or
	// RefusalPolicyUniform.
	RefusalPolicy string
	// Limiter rate-limits requests per requester; nil sets no limit.
	Limiter RequestLimiter
	// MaxRequestBytes caps a request's size; zero is DefaultMaxRequestBytes.
	MaxRequestBytes int
	// MaxAnswerItems caps the records or checkpoints one artifact carries; a
	// larger answer is refused policy_declined. Zero is DefaultMaxAnswerItems.
	MaxAnswerItems int
}

func (p ResponderPolicy) validate() error {
	if p.RefusalPolicy != "" && p.RefusalPolicy != RefusalPolicySpecific && p.RefusalPolicy != RefusalPolicyUniform {
		return fmt.Errorf("%w: refusal policy %q is neither %s nor %s", ErrInvalid, p.RefusalPolicy, RefusalPolicySpecific, RefusalPolicyUniform)
	}
	if p.MaxRequestBytes < 0 || p.MaxAnswerItems < 0 {
		return fmt.Errorf("%w: responder limits must not be negative", ErrInvalid)
	}
	return nil
}

// WindowLimiter allows each requester at most N requests in any window of
// Window. Unauthenticated callers share the one "" bucket.
type WindowLimiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	seen   map[string][]time.Time
}

// NewWindowLimiter returns a limiter of n requests per window.
func NewWindowLimiter(n int, window time.Duration) *WindowLimiter {
	return &WindowLimiter{n: n, window: window, seen: make(map[string][]time.Time)}
}

// Allow records a request at now and reports whether it is within the limit.
func (l *WindowLimiter) Allow(requester string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := slices.DeleteFunc(l.seen[requester], func(t time.Time) bool { return !t.After(now.Add(-l.window)) })
	if len(recent) >= l.n {
		l.seen[requester] = recent
		return false
	}
	l.seen[requester] = append(recent, now)
	return true
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

// Relationship of a requester to this book, checkable from local records. A
// stranger is a caller with no authenticated identity.
const (
	RelationshipStranger     = "stranger"
	RelationshipIdentified   = "identified"
	RelationshipCounterparty = "counterparty"
)

// allows gates protected subjects. It fails closed: a stranger (no
// authenticated requester) is never allowed, under any tier; "peers" means
// any authenticated peer.
func (p SharePolicy) allows(relationship string) bool {
	if relationship == RelationshipStranger {
		return false
	}
	switch p.HistorySegments {
	case "peers", "prospective":
		return true
	case "counterparties":
		return relationship == RelationshipCounterparty
	}
	return false
}

// RespondOptions carries the requester's identity and the sharing policy.
//
// AuthenticatedRequester is the requester identity established by the
// transport's own authentication of the caller (for example a verified
// mutual-TLS peer or a verified signature over the request). Set it only from
// that authentication; leave it empty when the caller is not authenticated.
// It is the only input to the relationship gate: with it empty, Respond
// answers only public questions (the checkpoints subject and the
// history_card/1 derivation) and refuses every other subject with a signed
// not_authorized refusal, whatever the policy tier. It decides whether a
// subject is answered, never what is answered (ER §3.7, §5).
//
// RequesterID is the identity the caller declared. It is never trusted: it
// does not select a relationship, does not grant any scope, and is not
// recorded. A handle, route, Agent Card or connection is not authorization
// either; only AuthenticatedRequester is read.
type RespondOptions struct {
	AuthenticatedRequester string
	// Deprecated: a declared identity is not authentication. Respond ignores
	// it; set AuthenticatedRequester from the transport's authentication.
	RequesterID string
	Policy      SharePolicy
}

// AnsweredStatement is the body of the record a responder seals for a
// request it answered for an authenticated requester. It names no
// requester, and records of this type are never served to anyone (they are
// not servable), so asking leaves no trace another requester can read.
type AnsweredStatement struct {
	RequestDigest  string      `json:"request_digest"`
	SubjectKind    SubjectKind `json:"subject_kind,omitempty"`
	Outcome        string      `json:"outcome"`
	Reason         string      `json:"reason,omitempty"`
	Relationship   string      `json:"relationship"`
	Anchor         string      `json:"anchor,omitempty"`
	ArtifactDigest string      `json:"artifact_digest,omitempty"`
}

// Respond answers one request, received as request (octets and media type),
// with a signed envelope carrying the canonical Artifact or a signed refusal
// with exactly one registered reason, encoded in the request's encoding
// (profile §6, §11.4.1 rule 3). The artifact is a deterministic function of
// (subject, resolved anchor, derivation) only: the requester, nonce, route,
// profile member and encoding never change its bytes (ER §5, FR-8).
//
// For an authenticated requester whose request was well formed, Respond
// seals an evidence_request_answered record and returns it; otherwise the
// returned Record is zero. Malformed and unauthenticated traffic never
// grows the log and never forces a checkpoint (FR-37).
func (b *Book) Respond(ctx context.Context, request Message, opts RespondOptions) (Reply, Record, error) {
	if err := opts.Policy.Validate(); err != nil {
		return Reply{}, Record{}, err
	}
	if request.MediaType != MediaTypeJSON && request.MediaType != MediaTypeCBOR {
		return Reply{}, Record{}, fmt.Errorf("%w: a request is carried as %s or %s, not %q", ErrInvalid, MediaTypeJSON, MediaTypeCBOR, request.MediaType)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := answer{book: b, ctx: ctx, request: request, opts: opts, now: b.now(), digest: digestBytes(request.Octets)}
	reply, err := a.run()
	if err != nil || !a.seal {
		return reply, Record{}, err
	}
	body, err := json.Marshal(a.statement)
	if err != nil {
		return Reply{}, Record{}, err
	}
	record, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeRequestAnswered, EpistemicType: ObservedEvent, Links: []Link{}, Statement: body})
	if err != nil {
		return Reply{}, Record{}, err
	}
	return reply, record, nil
}

// answer is one request being answered, under the book's lock.
type answer struct {
	book      *Book
	ctx       context.Context
	request   Message
	opts      RespondOptions
	now       time.Time
	digest    string
	statement AnsweredStatement
	seal      bool
}

func (a *answer) refuse(reason, detail string) (Reply, error) {
	b := a.book
	refusal := Refusal{RequestDigest: a.digest, Reason: reason, IssuedAt: evidencerequest.ProfileTime(a.now), KeyID: KeyID(b.signer), Detail: detail}
	refusal, err := refusal.Sign(a.ctx, b.signer)
	if err != nil {
		return Reply{}, err
	}
	a.statement.Outcome, a.statement.Reason = OutcomeRefusal, reason
	return evidencerequest.EncodeReply(a.request.MediaType, nil, &refusal)
}

// gate is what a decision reads besides the evidence: who asks, and under
// which policies.
type gate struct {
	requester     string // the authenticated requester, or ""
	relationship  string
	policy        SharePolicy
	derivations   map[string]derivationEntry
	refusalPolicy string
	// cll: the book's substrate is a Checkpointed Local Log, the only one
	// whose anchors and proofs are served.
	cll bool
}

// uniform applies the uniform refusal policy to a reason that would
// disclose what the policy protects.
func (g gate) uniform(reason string) string {
	return g.responderPolicy(false).Disclose(reason)
}

// responderPolicy is the gate as go/evidencerequest's responder policy: the
// registered derivations, who may have a request answered, the refusal
// policy, and whether the substrate serves anchors at all.
func (g gate) responderPolicy(mayIssue bool) evidencerequest.Policy {
	return evidencerequest.Policy{
		Derivation: func(name string) ([]SubjectKind, bool) {
			entry, ok := g.derivations[name]
			return entry.def.Forms, ok
		},
		// Authorization decides whether, never what (ER §3.7, §5).
		Authorize: func(req EvidenceRequest) bool {
			switch {
			case req.Subject.Kind == SubjectCheckpoints && req.Derivation == "", req.Derivation == DerivationHistoryCard:
				return true // public: commitments only
			case req.Derivation != "" && g.derivations[req.Derivation].def.Recipient != "":
				return g.requester == g.derivations[req.Derivation].def.Recipient
			default:
				return g.policy.allows(g.relationship)
			}
		},
		Uniform:    g.refusalPolicy == RefusalPolicyUniform,
		Unanchored: !g.cll,
		MayIssue:   mayIssue,
	}
}

// decision is Respond's answer to a well-formed request before any
// artifact is built: the resolution, and the registered derivation named.
type decision struct {
	evidencerequest.Resolution
	def *derivationEntry
}

// decide applies Respond's steps that follow the request reader, in the
// request draft's order (evidencerequest.Decide): the deadline, derivation
// support (at all, then for this subject; ER §3.3), authorization, the
// substrate, coverage, the subject, and the refusal policy. mayIssue says
// whether the responder will issue an anchor to meet a freshness floor;
// when that is what is needed, the decision says NeedAnchor and the caller
// decides again after issuing one.
func decide(view evidencerequest.View, check requestCheck, g gate, now time.Time, mayIssue bool) decision {
	d := decision{Resolution: evidencerequest.Decide(view, check.request, g.responderPolicy(mayIssue), now)}
	if entry, ok := g.derivations[check.request.Derivation]; ok && d.Reason == "" {
		d.def = &entry
	}
	return d
}

func (a *answer) run() (Reply, error) {
	b := a.book
	limit := b.responder.MaxRequestBytes
	if limit == 0 {
		limit = DefaultMaxRequestBytes
	}
	// Cost policy first: an oversized or over-limit request is declined
	// before any parsing or resolution work (FR-37).
	if len(a.request.Octets) > limit {
		return a.refuse(ReasonPolicyDeclined, "")
	}
	if l := b.responder.Limiter; l != nil && !l.Allow(a.opts.AuthenticatedRequester, a.now) {
		return a.refuse(ReasonPolicyDeclined, "")
	}
	check := readRequest(a.request)
	if check.reason != "" {
		return a.refuse(check.reason, check.detail)
	}
	req := check.request
	a.statement = AnsweredStatement{RequestDigest: a.digest, SubjectKind: req.Subject.Kind, Relationship: b.relationshipLocked(a.opts.AuthenticatedRequester)}
	a.seal = a.statement.Relationship != RelationshipStranger
	view, err := b.viewLocked(a.ctx)
	if err != nil {
		return Reply{}, err
	}
	_, isCLL := b.commitments.(cllMaterialSource)
	g := gate{requester: a.opts.AuthenticatedRequester, relationship: a.statement.Relationship, policy: a.opts.Policy, derivations: b.derivations, refusalPolicy: b.responder.RefusalPolicy, cll: isCLL}
	d := decide(view, check, g, a.now, a.seal)
	if d.NeedAnchor {
		if _, err := b.checkpointLocked(a.ctx); err != nil {
			return Reply{}, err
		}
		if view, err = b.viewLocked(a.ctx); err != nil {
			return Reply{}, err
		}
		if d = decide(view, check, g, a.now, false); d.NeedAnchor {
			d.Reason = g.uniform(ReasonCoverageUnsatisfiable)
		}
	}
	if d.Reason != "" {
		return a.refuse(d.Reason, "")
	}
	r, def := d.Resolution, d.def
	anchor := view.checkpoints[r.Anchor]
	built, err := b.buildArtifactLocked(a.ctx, view, req, anchor, r.Leaves, def)
	if err != nil {
		return Reply{}, err
	}
	if built.declined {
		return a.refuse(ReasonPolicyDeclined, "")
	}
	if req.Deadline != "" {
		if deadline, _ := evidencerequest.ParseRequestTime(req.Deadline); b.now().After(deadline) {
			return a.refuse(ReasonDeadlineUnmet, "")
		}
	}
	envelope := Envelope{
		Profile: req.Profile, RequestDigest: a.digest, Subject: req.Subject, Derivation: req.Derivation,
		Anchor: Anchor{Digest: anchorDigest(anchor)}, ArtifactDigest: digestBytes(built.artifact),
		Verification: built.verification, Responder: KeyID(b.signer), IssuedAt: evidencerequest.ProfileTime(a.now),
		Artifact: built.artifact, VerificationContent: built.material,
	}
	if r.HalfPin {
		envelope.Anchor.Digest = req.Coverage.ExpectedPin
	}
	if floor := req.Coverage.MinFreshness; floor != nil {
		if floor.Size != nil {
			size := anchor.TreeSize
			envelope.Anchor.FreshnessSize = &size
		} else {
			envelope.Anchor.FreshnessTime = evidencerequest.ProfileTime(anchor.IssuedAt)
		}
	}
	envelope, err = envelope.Sign(a.ctx, b.signer)
	if err != nil {
		return Reply{}, err
	}
	a.statement.Outcome, a.statement.Anchor, a.statement.ArtifactDigest = OutcomeArtifact, envelope.Anchor.Digest, envelope.ArtifactDigest
	return evidencerequest.EncodeReply(a.request.MediaType, &envelope, nil)
}

// relationshipLocked classifies an authenticated requester by counterparty_ref
// on the book's own records. It is only ever given the authenticated
// identity: a counterparty_ref value is not a credential, so knowing one
// grants nothing. Records of answered requests name no requester, so asking
// never makes a stranger a counterparty.
func (b *Book) relationshipLocked(authenticated string) string {
	if authenticated == "" {
		return RelationshipStranger
	}
	for _, id := range b.order {
		if b.records[id].Header.CounterpartyRef == authenticated {
			return RelationshipCounterparty
		}
	}
	return RelationshipIdentified
}

// bookView is the Book's evidencerequest.View, built under the book's lock.
type bookView struct {
	book        *Book
	checkpoints []Checkpoint
	views       []evidencerequest.AnchorView
	leaves      map[string]uint64
	commitments []retentionCommitment
}

func (b *Book) viewLocked(ctx context.Context) (*bookView, error) {
	history, err := b.store.Checkpoints(ctx)
	if err != nil {
		return nil, err
	}
	v := &bookView{book: b, leaves: make(map[string]uint64, len(b.order))}
	for _, cp := range history {
		if cp.Kind != SubstrateKindCLL {
			continue
		}
		v.checkpoints = append(v.checkpoints, cp)
		v.views = append(v.views, evidencerequest.AnchorView{Digest: anchorDigest(cp), Entries: cp.Entries, IssuedAt: cp.IssuedAt})
	}
	for i, id := range b.order {
		v.leaves[id] = uint64(i)
		if b.records[id].Header.RecordType == RecordTypeRetentionCommitment {
			if c, ok := readRetentionCommitment(b.records[id].Header.Statement); ok {
				v.commitments = append(v.commitments, c)
			}
		}
	}
	return v, nil
}

func (v *bookView) Anchors() []evidencerequest.AnchorView { return v.views }
func (v *bookView) LogEntries() uint64                    { return uint64(len(v.book.order)) }

func (v *bookView) LeafOf(digest string) (uint64, bool) {
	leaf, ok := v.leaves[digest]
	return leaf, ok && v.Servable(leaf)
}

// servable: the responder's own bookkeeping of who asked and what it
// disclosed is never served, so one requester's asking or receiving can
// never be read by another (NFR-2, NFR-4).
func (v *bookView) Servable(leaf uint64) bool {
	if leaf >= uint64(len(v.book.order)) {
		return false
	}
	switch v.book.records[v.book.order[leaf]].Header.RecordType {
	case RecordTypeRequestAnswered, RecordTypeDisclosure:
		return false
	}
	return true
}

func (v *bookView) matching(match func(Header) bool) []uint64 {
	var out []uint64
	for i, id := range v.book.order {
		if match(v.book.records[id].Header) && v.Servable(uint64(i)) {
			out = append(out, uint64(i))
		}
	}
	return out
}

func (v *bookView) Correlated(id string) []uint64 {
	return v.matching(func(h Header) bool { return h.SubjectRef == id })
}

func (v *bookView) Citing(half string) []uint64 {
	return v.matching(func(h Header) bool { return slices.Contains(h.LinksTo(Cites), half) })
}

func (v *bookView) Lapsed(subject Subject, now time.Time) bool {
	for _, c := range v.commitments {
		if c.subject == subject && now.After(c.until) {
			return true
		}
	}
	return false
}

// capsulePreimage is a record's octets as the log commits them: UTF-8 of
// JCS of the record capsule without capsule_id and the local-only producer
// fields, whose SHA-256 is the record id (the logged entry).
func capsulePreimage(capsule []byte, recordID string) ([]byte, error) {
	decoded, err := decodeJSON(capsule)
	if err != nil {
		return nil, fmt.Errorf("%w: capsule %s: %v", ErrCorrupt, recordID, err)
	}
	fields, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: capsule %s is not an object", ErrCorrupt, recordID)
	}
	delete(fields, "capsule_id")
	for name := range canonical.LocalOnlyFields {
		delete(fields, name)
	}
	body, err := canonical.JCS(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: capsule %s: %v", ErrCorrupt, recordID, err)
	}
	if digestBytes(body) != recordID {
		return nil, fmt.Errorf("%w: capsule body does not hash to record id %s", ErrCorrupt, recordID)
	}
	return body, nil
}
