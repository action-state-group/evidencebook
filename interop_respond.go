// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
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
// transport did not authenticate. A request over the limit is refused for a
// limit: capacity_exceeded under evidence-interop/1, policy_declined under /0.
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
	// larger answer is refused for a limit, as a request over the limiter is.
	// Zero is DefaultMaxAnswerItems.
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
//
// HistorySegments decides who may have the records an underived (or
// shared-derivation) record, exchange or correlation subject names:
// "counterparties" (the default) answers only the party each of those
// records names as its counterparty; "peers" answers any authenticated
// peer; "off" answers no one. "prospective" is accepted and read as
// "counterparties", as the mesh plugin reads it. A correlation subject is
// an exact-key lookup (bookView.Correlated), so it is gated like an
// exchange. Whatever the setting, range and full_history are refused, but
// for full_history under standard_history/1 when the host serves it
// (StandardHistorySource). A recipient-specific derivation is the
// holder's grant and is not gated by the setting: it is answered to its
// recipient over every form it declares, "off" included
// (gate.responderPolicy).
type SharePolicy struct {
	RecordAtCompletion string `json:"record_at_completion"`
	HistorySegments    string `json:"history_segments"`
	Adjudications      string `json:"adjudications"`
	Witness            string `json:"witness,omitempty"`
}

// DefaultSharePolicy returns the documented defaults.
func DefaultSharePolicy() SharePolicy {
	return SharePolicy{RecordAtCompletion: "counterparty", HistorySegments: "counterparties", Adjudications: "deliver_to_subjects"}
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

// mayHave gates a record, exchange or correlation subject by the records
// it names. It
// fails closed: a stranger (no authenticated requester) is never allowed,
// under any tier. "peers" allows any authenticated peer. "counterparties"
// and "prospective" allow only a counterparty of this book, and only when
// every record the subject names names that requester as its
// counterparty; a requester no record names is refused before anything
// about the subject is read, so it learns nothing of what is held. A
// counterparty asking for a record not held is let through to resolution,
// which answers no_such_subject or retention_expired; the uniform refusal
// policy folds those and not_authorized into one answer (ER §4.2, §12).
func (p SharePolicy) mayHave(requester, relationship string, named []Header) bool {
	if requester == "" {
		return false
	}
	switch p.HistorySegments {
	case "peers":
		return true
	case "counterparties", "prospective":
		return relationship == RelationshipCounterparty &&
			!slices.ContainsFunc(named, func(h Header) bool { return h.CounterpartyRef != requester })
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
//
// Respond answers from a snapshot of the log and holds the book's lock only
// to take it, to issue an anchor an authenticated requester's freshness
// floor needs, and to seal the evidence_request_answered record. Answering,
// however many requests arrive at once, never holds Append, Checkpoint or
// any other writer for longer than that (evidencebook#208).
func (b *Book) Respond(ctx context.Context, request Message, opts RespondOptions) (Reply, Record, error) {
	if err := opts.Policy.Validate(); err != nil {
		return Reply{}, Record{}, err
	}
	if request.MediaType != MediaTypeJSON && request.MediaType != MediaTypeCBOR {
		return Reply{}, Record{}, fmt.Errorf("%w: a request is carried as %s or %s, not %q", ErrInvalid, MediaTypeJSON, MediaTypeCBOR, request.MediaType)
	}
	b.answering.RLock()
	defer b.answering.RUnlock()
	a := answer{book: b, ctx: ctx, request: request, opts: opts, digest: digestBytes(request.Octets)}
	reply, err := a.run()
	if err != nil || !a.seal {
		return reply, Record{}, err
	}
	body, err := json.Marshal(a.statement)
	if err != nil {
		return Reply{}, Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	record, err := b.appendLocked(ctx, Entry{RecordType: RecordTypeRequestAnswered, EpistemicType: ObservedEvent, Links: []Link{}, Statement: body})
	if err != nil {
		return Reply{}, Record{}, err
	}
	return reply, record, nil
}

// answer is one request being answered, from a snapshot of the book's log.
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

// limitReason is the reason a request asking for profile is refused for a
// limit of the book's own: its size, the rate limiter or the answer's item
// cap. evidence-interop/1 (ER -02 §4.2, §11) refuses load shedding
// capacity_exceeded whatever the refusal policy, so policy_declined stays a
// statement about policy; /0 registers no such token and keeps
// policy_declined, as does a profile the book does not implement.
func limitReason(profile string) string {
	if profile == evidencerequest.ProfileIDv1 {
		return evidencerequest.ReasonCapacityExceeded
	}
	return ReasonPolicyDeclined
}

// requestProfile is the profile a request refused for a limit asks for, so
// the refusal conforms under it: the profile of a request the book reads as
// well formed, ProfileID for any other (§3.3 step 2). It runs only once the
// cost checks have declined the request, and reads it as an answer would, so
// a request is read under one definition of well formed (readRequest) and
// no resolution work runs; the carriages bound a message before it reaches
// the book.
func requestProfile(m Message) string {
	if check := readRequest(m); check.reason == "" {
		return check.request.Profile
	}
	return ProfileID
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
	requester    string // the authenticated requester, or ""
	relationship string
	policy       SharePolicy
	derivations  map[string]derivationEntry
	// standardHistory: the host serves standard_history/1.
	standardHistory bool
	refusalPolicy   string
	// cll: the book's substrate is a Checkpointed Local Log, the only one
	// whose anchors and proofs are served.
	cll bool
	// named is the servable records a record, exchange or correlation
	// subject names.
	named func(Subject) []Header
}

// uniform applies the uniform refusal policy to a reason that would
// disclose what the policy protects.
func (g gate) uniform(reason string) string {
	return evidencerequest.Policy{Uniform: g.refusalPolicy == RefusalPolicyUniform}.Disclose(reason)
}

// responderPolicy is the gate as go/evidencerequest's responder policy: the
// registered derivations, who may have a request answered, the refusal
// policy, and whether the substrate serves anchors at all.
func (g gate) responderPolicy(mayIssue bool) evidencerequest.Policy {
	return evidencerequest.Policy{
		Derivation: func(name string) ([]SubjectKind, bool) {
			if name == DerivationStandardHistory {
				return []SubjectKind{SubjectFullHistory}, g.standardHistory
			}
			entry, ok := g.derivations[name]
			return entry.def.Forms, ok
		},
		// Authorization decides whether, never what (ER §3.7, §5). It is
		// decided before the subject is resolved, from the records the
		// subject names.
		Authorize: func(req EvidenceRequest) bool {
			switch {
			case req.Subject.Kind == SubjectCheckpoints && req.Derivation == "", req.Derivation == DerivationHistoryCard:
				return true // public: commitments only
			case req.Derivation == DerivationStandardHistory:
				// Counts only, the same for every requester it is answered
				// to (SH §3): any authenticated requester, whatever the
				// tier; never a stranger.
				return g.requester != ""
			case req.Derivation != "" && g.derivations[req.Derivation].def.Recipient != "":
				// The holder's grant to the one requester it names.
				return g.requester == g.derivations[req.Derivation].def.Recipient
			case req.Subject.Kind == SubjectRecord, req.Subject.Kind == SubjectExchange, req.Subject.Kind == SubjectCorrelation:
				// A correlation subject matches its key exactly, never by
				// prefix or range, so it names a deal's records the way
				// an exchange does (Steven's Q1 amendment on #114).
				return g.policy.mayHave(g.requester, g.relationship, g.named(req.Subject))
			default:
				// range and full_history reach records no one requester is
				// party to; nothing but a recipient-specific derivation
				// grants them, or the counts-only standard_history/1 card
				// (above).
				return false
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
	// The lock is held only for what must be serialized with the book's
	// writers: its clock, the limiter (which the book has always called
	// under its lock) and the snapshot.
	b.mu.Lock()
	a.now = b.now()
	// Cost policy first: an oversized or over-limit request is declined
	// before it is resolved (FR-37); it is read only to pick the reason
	// (requestProfile).
	oversized := len(a.request.Octets) > limit
	limited := !oversized && b.responder.Limiter != nil && !b.responder.Limiter.Allow(a.opts.AuthenticatedRequester, a.now)
	log, derivations := b.logLocked(), maps.Clone(b.derivations)
	b.mu.Unlock()
	if oversized || limited {
		return a.refuse(limitReason(requestProfile(a.request)), "")
	}
	// The book answers evidence-interop/0 and /1 (evidencerequest.Profiles),
	// and an unknown profile's detail lists both (§3.3 step 4).
	check := readRequest(a.request)
	if check.reason != "" {
		return a.refuse(check.reason, check.detail)
	}
	req := check.request
	view, err := b.view(a.ctx, log)
	if err != nil {
		return Reply{}, err
	}
	a.statement = AnsweredStatement{RequestDigest: a.digest, SubjectKind: req.Subject.Kind, Relationship: view.relationship(a.opts.AuthenticatedRequester)}
	a.seal = a.statement.Relationship != RelationshipStranger
	_, isCLL := b.commitments.(cllMaterialSource)
	g := gate{requester: a.opts.AuthenticatedRequester, relationship: a.statement.Relationship, policy: a.opts.Policy, derivations: derivations, standardHistory: b.standardHistory != nil, refusalPolicy: b.responder.RefusalPolicy, cll: isCLL, named: view.named}
	d := decide(view, check, g, a.now, a.seal)
	if d.NeedAnchor {
		b.mu.Lock()
		err := b.issueAnchorLocked(a.ctx, check)
		log = b.logLocked()
		b.mu.Unlock()
		if err != nil {
			return Reply{}, err
		}
		if view, err = b.view(a.ctx, log); err != nil {
			return Reply{}, err
		}
		// The new snapshot may hold records sealed since the first, among
		// them one naming the requester as a counterparty.
		a.statement.Relationship = view.relationship(a.opts.AuthenticatedRequester)
		g.relationship, g.named = a.statement.Relationship, view.named
		if d = decide(view, check, g, a.now, false); d.NeedAnchor {
			d.Reason = g.uniform(ReasonCoverageUnsatisfiable)
		}
	}
	if d.Reason != "" {
		return a.refuse(d.Reason, "")
	}
	r, def := d.Resolution, d.def
	anchor := view.checkpoints[r.Anchor]
	built, err := b.buildArtifact(a.ctx, view, req, anchor, r.Leaves, def)
	if err != nil {
		return Reply{}, err
	}
	if built.overLimit {
		return a.refuse(limitReason(req.Profile), "")
	}
	if built.declined {
		return a.refuse(ReasonPolicyDeclined, "")
	}
	if req.Deadline != "" {
		if deadline, _ := evidencerequest.ParseRequestTime(req.Deadline); b.clock().After(deadline) {
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

// issueAnchorLocked issues an anchor meeting check's freshness floor unless one
// already does: answers decide from their own snapshots, so several may
// find the floor unmet at once, and only the first issues one.
func (b *Book) issueAnchorLocked(ctx context.Context, check requestCheck) error {
	stored, err := b.store.Checkpoints(ctx)
	if err != nil {
		return err
	}
	for _, cp := range slices.Backward(stored) {
		if cp.Kind == SubstrateKindCLL {
			if evidencerequest.AnchorMeets(evidencerequest.AnchorView{Entries: cp.Entries, IssuedAt: cp.IssuedAt}, check.request.Coverage.MinFreshness, check.floorTime) {
				return nil
			}
			break
		}
	}
	_, err = b.checkpointLocked(ctx)
	return err
}

// relationship classifies an authenticated requester by counterparty_ref
// on the book's own records. It is only ever given the authenticated
// identity: a counterparty_ref value is not a credential, so knowing one
// grants nothing. Records of answered requests name no requester, so asking
// never makes a stranger a counterparty.
func (v *bookView) relationship(authenticated string) string {
	if authenticated == "" {
		return RelationshipStranger
	}
	for _, record := range v.log {
		if record.Header.CounterpartyRef == authenticated {
			return RelationshipCounterparty
		}
	}
	return RelationshipIdentified
}

// clock reads the book's clock under its lock: Config.Now has only ever
// been called with the lock held.
func (b *Book) clock() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.now()
}

// bookView is the Book's evidencerequest.View over one snapshot of its log
// (Book.logLocked). It reads nothing the book changes after the snapshot,
// so it needs no lock. Its checkpoints are the stored ones that cover no
// record past the snapshot: every anchor it resolves, and so every proof
// built at that anchor's size, is about records the snapshot holds, however
// far the log has grown since (evidencebook#208).
type bookView struct {
	log         []Record
	checkpoints []Checkpoint
	views       []evidencerequest.AnchorView
	leaves      map[string]uint64
	commitments []retentionCommitment
}

func (b *Book) view(ctx context.Context, log []Record) (*bookView, error) {
	history, err := b.store.Checkpoints(ctx)
	if err != nil {
		return nil, err
	}
	v := &bookView{log: log, leaves: make(map[string]uint64, len(log))}
	for _, cp := range history {
		if cp.Kind != SubstrateKindCLL || cp.Entries > uint64(len(log)) {
			continue
		}
		v.checkpoints = append(v.checkpoints, cp)
		v.views = append(v.views, evidencerequest.AnchorView{Digest: anchorDigest(cp), Entries: cp.Entries, IssuedAt: cp.IssuedAt})
	}
	for i, record := range log {
		v.leaves[record.RecordID] = uint64(i)
		if record.Header.RecordType == RecordTypeRetentionCommitment {
			if c, ok := readRetentionCommitment(record.Header.Statement); ok {
				v.commitments = append(v.commitments, c)
			}
		}
	}
	return v, nil
}

// record is the snapshot's record with id, if it holds one.
func (v *bookView) record(id string) (Record, bool) {
	leaf, ok := v.leaves[id]
	if !ok {
		return Record{}, false
	}
	return v.log[leaf], true
}

func (v *bookView) Anchors() []evidencerequest.AnchorView { return v.views }
func (v *bookView) LogEntries() uint64                    { return uint64(len(v.log)) }

func (v *bookView) LeafOf(digest string) (uint64, bool) {
	leaf, ok := v.leaves[digest]
	return leaf, ok && v.Servable(leaf)
}

// servable: the responder's own bookkeeping of who asked and what it
// disclosed is never served, so one requester's asking or receiving can
// never be read by another (NFR-2, NFR-4).
func (v *bookView) Servable(leaf uint64) bool {
	if leaf >= uint64(len(v.log)) {
		return false
	}
	switch v.log[leaf].Header.RecordType {
	case RecordTypeRequestAnswered, RecordTypeDisclosure:
		return false
	}
	return true
}

func (v *bookView) matching(match func(Header) bool) []uint64 {
	var out []uint64
	for i, record := range v.log {
		if match(record.Header) && v.Servable(uint64(i)) {
			out = append(out, uint64(i))
		}
	}
	return out
}

// Correlated is a correlation subject's records. A record that carries the
// header's correlation identifier (correlation.exchange_id) is held under
// that identifier and no other. Only a record that carries no exchange_id is held
// under its subject_ref, as before. A host can then keep subject_ref for
// its own use, for example one value per step of a deal, and still answer
// for the deal as a whole (Personal-Agent History Profile v0 §1).
func (v *bookView) Correlated(id string) []uint64 {
	return v.matching(func(h Header) bool { return correlationOf(h) == id })
}

// correlationOf is the correlation identifier a record is held under.
func correlationOf(h Header) string {
	if h.Correlation != nil && h.Correlation.ExchangeID != "" {
		return h.Correlation.ExchangeID
	}
	return h.SubjectRef
}

// named is the servable records a record, exchange or correlation subject
// names, whether or not the anchor covers them yet: the record itself,
// every record citing the half, or every record held under the key.
func (v *bookView) named(subject Subject) []Header {
	var leaves []uint64
	switch subject.Kind {
	case SubjectRecord:
		if leaf, ok := v.LeafOf(subject.Digest); ok {
			leaves = append(leaves, leaf)
		}
	case SubjectExchange:
		leaves = v.Citing(subject.Digest)
	case SubjectCorrelation:
		leaves = v.Correlated(subject.Correlation)
	}
	headers := make([]Header, len(leaves))
	for i, leaf := range leaves {
		headers[i] = v.log[leaf].Header
	}
	return headers
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
