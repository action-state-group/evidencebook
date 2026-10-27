//! Answering one request as a book: the gate, the decision, the signed reply
//! and the `evidence_request_answered` record, as Go's `Respond`
//! (`interop_respond.go`).
//!
//! I-D section: *Answering a Request*. The artifact and the envelope are
//! [`crate::answer`]'s; this module is what decides whether and under which
//! anchor to build them, refuses with exactly one registered reason
//! otherwise, and seals the responder's own record of having answered an
//! authenticated requester.
//!
//! The order of the decision is the request draft's (Go
//! `evidencerequest.Decide`): the cost policy, the request reader, the
//! deadline, derivation support (at all, then for this subject), authorization,
//! coverage (an anchor the book issued, or a freshness floor), the subject,
//! and the refusal policy. A freshness floor no anchor meets is met by
//! cutting a checkpoint only for an authenticated requester whose request was
//! well formed; unauthenticated and malformed traffic never grows the log and
//! never forces a checkpoint.

use crate::answer::{self, anchor_digest, citing_half, Options, Record, Selection};
use crate::ask::{profile_time, Book, Entry, Sealed};
use crate::derivation::{Derivations, Registered};
use crate::record::{record_types, EpistemicType};
use crate::request::SubjectKind;
use crate::store::{CheckpointedLog, RecordSource};
use crate::substrate::StatedCheckpoint;
use ed25519_dalek::SigningKey;
use evidence_request::envelope::{Binding, Envelope};
use evidence_request::registry::Reason;
use evidence_request::request::{self, Coverage, Derivation, Freshness, Request, Subject};
use evidence_request::resolve::{self, ResolvedAnchor};
use evidence_request::time::{parse_utc, UtcTime};
use serde::Serialize;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::sync::Mutex;

/// Defaults of the responder's cost policy.
pub const DEFAULT_MAX_REQUEST_BYTES: usize = 64 << 10;
pub const DEFAULT_MAX_ANSWER_ITEMS: usize = 10_000;

/// The profile identifier this responder implements.
pub const PROFILE_ID: &str = crate::ask::PROFILE_ID;

/// Decides whether a requester may be answered now. The requester is the
/// authenticated identity, or `None` for every caller the transport did not
/// authenticate. A request over the limit is refused `policy_declined`.
pub trait RequestLimiter: Send + Sync {
    fn allow(&self, requester: Option<&str>, now: UtcTime) -> bool;
}

/// Allows each requester at most `n` requests in any window of `window`
/// seconds. Unauthenticated callers share the one anonymous bucket.
pub struct WindowLimiter {
    n: usize,
    window: i64,
    seen: Mutex<BTreeMap<Option<String>, Vec<UtcTime>>>,
}

impl WindowLimiter {
    pub fn new(n: usize, window_seconds: i64) -> Self {
        WindowLimiter {
            n,
            window: window_seconds,
            seen: Mutex::new(BTreeMap::new()),
        }
    }
}

impl RequestLimiter for WindowLimiter {
    fn allow(&self, requester: Option<&str>, now: UtcTime) -> bool {
        let mut seen = self.seen.lock().expect("limiter lock");
        let bucket = seen.entry(requester.map(str::to_string)).or_default();
        let cutoff = UtcTime {
            seconds: now.seconds - self.window,
            nanos: now.nanos,
        };
        bucket.retain(|t| *t > cutoff);
        if bucket.len() >= self.n {
            return false;
        }
        bucket.push(now);
        true
    }
}

/// Refusal policies a responder may declare (profile §11.4.2).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum RefusalPolicy {
    /// Answer the most specific registered token.
    #[default]
    Specific,
    /// Answer `policy_declined` wherever a more specific token would
    /// disclose what the responder's policy protects: whether a subject
    /// exists, whether it is covered yet, and whether this requester may see
    /// it (ER §4.2, §12).
    Uniform,
}

/// The responder's cost and abuse policy (FR-37). The default answers the
/// most specific token with no rate limit.
#[derive(Default)]
pub struct ResponderPolicy {
    pub refusal_policy: RefusalPolicy,
    pub limiter: Option<Box<dyn RequestLimiter>>,
    /// Caps a request's size; zero is [`DEFAULT_MAX_REQUEST_BYTES`].
    pub max_request_bytes: usize,
    /// Caps the records or checkpoints one artifact carries; a larger answer
    /// is refused `policy_declined`. Zero is [`DEFAULT_MAX_ANSWER_ITEMS`].
    pub max_answer_items: usize,
}

/// The four-switch sharing policy, carried unchanged from the host's `share.*`
/// object. Every default keys on relationship.
///
/// `history_segments` decides who may have the records an underived (or
/// shared-derivation) record or citing-records subject names: `counterparties`
/// (the default) answers only the party each of those records names as its
/// counterparty; `peers` answers any authenticated peer; `off` answers no
/// one. `prospective` is accepted and read as `counterparties`. Whatever the
/// setting, range, correlation and full_history are refused. A
/// recipient-specific derivation is the holder's grant and is not gated by
/// the setting.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct SharePolicy {
    pub record_at_completion: String,
    pub history_segments: String,
    pub adjudications: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub witness: String,
}

impl Default for SharePolicy {
    fn default() -> Self {
        SharePolicy {
            record_at_completion: "counterparty".into(),
            history_segments: "counterparties".into(),
            adjudications: "deliver_to_subjects".into(),
            witness: String::new(),
        }
    }
}

impl SharePolicy {
    /// Reject any switch outside its enumerated values.
    pub fn validate(&self) -> Result<(), RespondError> {
        let checks: [(&str, &str, &[&str]); 3] = [
            (
                "record_at_completion",
                &self.record_at_completion,
                &["counterparty", "off"],
            ),
            (
                "history_segments",
                &self.history_segments,
                &["counterparties", "prospective", "peers", "off"],
            ),
            (
                "adjudications",
                &self.adjudications,
                &["deliver_to_subjects", "off"],
            ),
        ];
        for (name, value, allowed) in checks {
            if !allowed.contains(&value) {
                return Err(RespondError::Invalid(format!(
                    "share.{name}={value:?} is not one of {allowed:?}"
                )));
            }
        }
        Ok(())
    }

    /// Gates a record or citing-records subject by the records it names. It fails
    /// closed: a stranger (no authenticated requester) is never allowed,
    /// under any tier. `peers` allows any authenticated peer. `counterparties`
    /// and `prospective` allow only a counterparty of this book, and only
    /// when every record the subject names names that requester as its
    /// counterparty.
    fn may_have(
        &self,
        requester: Option<&str>,
        relationship: Relationship,
        named: &[Option<String>],
    ) -> bool {
        let Some(requester) = requester else {
            return false;
        };
        match self.history_segments.as_str() {
            "peers" => true,
            "counterparties" | "prospective" => {
                relationship == Relationship::Counterparty
                    && named.iter().all(|c| c.as_deref() == Some(requester))
            }
            _ => false,
        }
    }
}

/// The relationship of a requester to this book, checkable from local
/// records. A stranger is a caller with no authenticated identity.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Relationship {
    Stranger,
    Identified,
    Counterparty,
}

impl Relationship {
    pub fn as_str(self) -> &'static str {
        match self {
            Relationship::Stranger => "stranger",
            Relationship::Identified => "identified",
            Relationship::Counterparty => "counterparty",
        }
    }
}

/// The requester's identity and the sharing policy.
///
/// `authenticated_requester` is the identity the transport's own
/// authentication of the caller established (a verified mutual-TLS peer, a
/// verified signature over the request). Set it only from that
/// authentication. It is the only input to the relationship gate: with it
/// unset, [`respond`] answers only public questions (the checkpoints subject
/// and the `history_card/1` derivation) and refuses every other subject with
/// a signed `not_authorized` refusal, whatever the policy tier. It decides
/// whether a subject is answered, never what is answered (ER §3.7, §5).
#[derive(Clone, Debug, Default)]
pub struct RespondOptions<'a> {
    pub authenticated_requester: Option<&'a str>,
    pub policy: SharePolicy,
}

/// The body of the record a responder seals for a request it answered for
/// an authenticated requester. It names no requester, and records of this
/// type are never served to anyone, so asking leaves no trace another
/// requester can read.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct AnsweredStatement {
    pub request_digest: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub subject_kind: Option<SubjectKind>,
    pub outcome: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reason: String,
    pub relationship: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub anchor: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub artifact_digest: String,
}

/// The outcome values `AnsweredStatement.outcome` takes.
pub const OUTCOME_ARTIFACT: &str = "artifact";
pub const OUTCOME_REFUSAL: &str = "refusal";

/// Why a request could not be answered at all (as distinct from being
/// refused): nothing was sent and, unless the error says otherwise, nothing
/// recorded.
#[derive(Debug, thiserror::Error)]
pub enum RespondError {
    #[error("invalid: {0}")]
    Invalid(String),
    #[error(transparent)]
    Seal(#[from] crate::ask::SealError),
    #[error(transparent)]
    Substrate(#[from] crate::substrate::SubstrateError),
    #[error("answer: {0}")]
    Answer(#[from] answer::AnswerError),
    #[error("sign: {0}")]
    Sign(String),
}

/// What a responder is configured with besides the book: who signs, the
/// registered derivations and the cost policy.
pub struct Responder<'a> {
    pub signer: &'a SigningKey,
    pub derivations: &'a Derivations,
    pub policy: &'a ResponderPolicy,
}

fn sha256_hex(bytes: &[u8]) -> String {
    hex::encode(Sha256::digest(bytes))
}

/// Answer one request, received as `octets` in `binding`, with a signed
/// envelope carrying the canonical artifact or a signed refusal with exactly
/// one registered reason, encoded in the request's encoding (profile §6,
/// §11.4.1 rule 3). The artifact is a deterministic function of (subject,
/// resolved anchor, derivation) only: the requester, nonce, route and
/// encoding never change its bytes (ER §5, FR-8).
///
/// For an authenticated requester whose request was well formed, an
/// `evidence_request_answered` record is sealed and returned; otherwise the
/// returned record is `None`. Malformed and unauthenticated traffic never
/// grows the log and never forces a checkpoint (FR-37).
pub fn respond<B: Book + CheckpointedLog>(
    book: &mut B,
    responder: &Responder<'_>,
    octets: &[u8],
    binding: Binding,
    opts: &RespondOptions<'_>,
) -> Result<(Vec<u8>, Option<Sealed>), RespondError> {
    opts.policy.validate()?;
    let now_text = book.now();
    if parse_utc(&now_text).is_none() {
        return Err(RespondError::Invalid(format!(
            "the book's clock read {now_text:?}, not an RFC 3339 UTC time"
        )));
    }
    let mut run = Run {
        responder,
        octets,
        binding,
        opts,
        now_text,
        digest: sha256_hex(octets),
        statement: AnsweredStatement::default(),
        seal: false,
    };
    let reply = run.run(book)?;
    if !run.seal {
        return Ok((reply, None));
    }
    let statement =
        serde_json::to_value(&run.statement).map_err(|e| RespondError::Invalid(e.to_string()))?;
    let sealed = book.seal(&Entry {
        record_type: record_types::REQUEST_ANSWERED.into(),
        epistemic_type: EpistemicType::ObservedEvent,
        links: Vec::new(),
        statement,
    })?;
    Ok((reply, Some(sealed)))
}

struct Run<'a, 'b> {
    responder: &'a Responder<'a>,
    octets: &'a [u8],
    binding: Binding,
    opts: &'a RespondOptions<'b>,
    now_text: String,
    digest: String,
    statement: AnsweredStatement,
    seal: bool,
}

impl Run<'_, '_> {
    fn now(&self) -> UtcTime {
        parse_utc(&self.now_text).expect("a book's clock reads RFC 3339 UTC")
    }

    fn refuse(&mut self, reason: Reason, detail: Option<&str>) -> Result<Vec<u8>, RespondError> {
        let refusal = evidence_request::refusal::sign(
            &self.digest,
            reason,
            &profile_time(&self.now_text),
            self.responder.signer,
        )
        .map_err(|e| RespondError::Sign(e.to_string()))?;
        self.statement.outcome = OUTCOME_REFUSAL.into();
        self.statement.reason = reason.token().into();
        Ok(encode_refusal(&refusal, detail, self.binding))
    }

    fn run<B: Book + CheckpointedLog>(&mut self, book: &mut B) -> Result<Vec<u8>, RespondError> {
        let policy = self.responder.policy;
        let limit = match policy.max_request_bytes {
            0 => DEFAULT_MAX_REQUEST_BYTES,
            n => n,
        };
        let requester = self.opts.authenticated_requester;
        // Cost policy first: an oversized or over-limit request is declined
        // before any parsing or resolution work (FR-37).
        if self.octets.len() > limit {
            return self.refuse(Reason::PolicyDeclined, None);
        }
        if let Some(limiter) = &policy.limiter {
            if !limiter.allow(requester, self.now()) {
                return self.refuse(Reason::PolicyDeclined, None);
            }
        }
        let req = match read_request(self.octets, self.binding) {
            Ok(req) => req,
            Err((reason, detail)) => return self.refuse(reason, detail),
        };
        let relationship = relationship(book, requester);
        self.statement = AnsweredStatement {
            request_digest: self.digest.clone(),
            subject_kind: Some(subject_kind(&req.subject)),
            relationship: relationship.as_str().into(),
            ..Default::default()
        };
        self.seal = relationship != Relationship::Stranger;
        let gate = Gate {
            requester,
            relationship,
            share: &self.opts.policy,
            derivations: self.responder.derivations,
            uniform: policy.refusal_policy == RefusalPolicy::Uniform,
        };
        let mut view = View::of(book)?;
        let mut decision = decide(
            &view,
            &req,
            &gate,
            &|r, reg| authorized(&view, r, &gate, reg),
            self.now(),
            self.seal,
        );
        if matches!(decision, Decision::NeedAnchor) {
            book.checkpoint()?;
            view = View::of(book)?;
            decision = decide(
                &view,
                &req,
                &gate,
                &|r, reg| authorized(&view, r, &gate, reg),
                self.now(),
                false,
            );
            if matches!(decision, Decision::NeedAnchor) {
                decision = Decision::Refuse(gate.disclose(Reason::CoverageUnsatisfiable));
            }
        }
        let (anchor_at, half_pin, leaves) = match decision {
            Decision::Refuse(reason) => return self.refuse(reason, None),
            Decision::NeedAnchor => unreachable!("a second NeedAnchor is refused above"),
            Decision::Serve {
                anchor,
                half_pin,
                leaves,
            } => (anchor, half_pin, leaves),
        };
        let max_items = match policy.max_answer_items {
            0 => DEFAULT_MAX_ANSWER_ITEMS,
            n => n,
        };
        let anchor = &view.checkpoints[anchor_at];
        let chain = anchor_at + 1;
        let over = leaves.len() > max_items
            || (matches!(req.subject, Subject::Checkpoints) || is_history_card(&req))
                && chain > max_items;
        if over {
            return self.refuse(Reason::PolicyDeclined, None);
        }
        let resolved = if half_pin {
            ResolvedAnchor::ExchangeHalf(match &req.coverage {
                Coverage::ExpectedPin(pin) => pin.clone(),
                Coverage::MinFreshness(_) => unreachable!("a half pin is an expected_pin"),
            })
        } else {
            ResolvedAnchor::Anchor(resolve::Anchor {
                digest: anchor_digest(anchor),
                size: anchor.checkpoint.leaf_count()?,
                issued_at: anchor.checkpoint.timestamp.clone(),
            })
        };
        let book: &B = book;
        let envelope = match answer::build(
            book,
            book,
            &req,
            self.octets,
            &resolved,
            &Selection {
                at: anchor_at,
                records: leaves,
            },
            Options {
                derivations: Some(self.responder.derivations),
                requester,
                ..Options::default()
            },
            self.responder.signer,
            &profile_time(&self.now_text),
        ) {
            Ok(envelope) => envelope,
            Err(answer::AnswerError::OverLimit) => {
                return self.refuse(Reason::PolicyDeclined, None)
            }
            Err(answer::AnswerError::NotAuthorized) => {
                return self.refuse(gate.disclose(Reason::NotAuthorized), None)
            }
            Err(e) => return Err(e.into()),
        };
        if let Some(deadline) = req.deadline {
            if parse_utc(&book.now()).is_some_and(|now| now > deadline) {
                return self.refuse(Reason::DeadlineUnmet, None);
            }
        }
        self.statement.outcome = OUTCOME_ARTIFACT.into();
        self.statement.anchor = envelope.anchor.digest.clone();
        self.statement.artifact_digest = envelope.artifact_digest.clone();
        Ok(encode_envelope(&envelope, self.binding))
    }
}

fn is_history_card(req: &Request) -> bool {
    matches!(&req.derivation, Some(Derivation::Token(t)) if t == answer::DERIVATION_HISTORY_CARD)
}

fn encode_envelope(envelope: &Envelope, binding: Binding) -> Vec<u8> {
    envelope.encode(binding)
}

/// A refusal in the request's encoding. `detail` is the optional unsigned
/// human-readable text a refusal of an unsupported profile carries (outside
/// the signed members); it is sent, but the token is what is read.
fn encode_refusal(
    refusal: &evidence_request::refusal::Refusal,
    detail: Option<&str>,
    binding: Binding,
) -> Vec<u8> {
    match (binding, detail) {
        (Binding::Json, None) => evidence_request::jcs::to_string(&refusal.to_json())
            .expect("text members canonicalize")
            .into_bytes(),
        (Binding::Json, Some(detail)) => {
            let mut object = refusal.to_json();
            object["detail"] = json!(detail);
            evidence_request::jcs::to_string(&object)
                .expect("text members canonicalize")
                .into_bytes()
        }
        (Binding::Cbor, None) => refusal.to_cbor(),
        (Binding::Cbor, Some(detail)) => {
            use ciborium::Value as C;
            let text = |s: &str| C::Text(s.to_string());
            let encoded = |v: &C| {
                let mut out = Vec::new();
                ciborium::into_writer(v, &mut out).expect("writing to a Vec never fails");
                out
            };
            let mut entries = vec![
                (text("issued_at"), text(&refusal.issued_at)),
                (text("reason"), text(refusal.reason.token())),
                (text("request_digest"), text(&refusal.request_digest)),
                (text("key_id"), text(&refusal.key_id)),
                (text("sig"), text(&refusal.sig)),
                (text("detail"), text(detail)),
            ];
            entries.sort_by_cached_key(|(k, _)| encoded(k));
            encoded(&C::Map(entries))
        }
    }
}

fn subject_kind(subject: &Subject) -> SubjectKind {
    match subject {
        Subject::FullHistory => SubjectKind::FullHistory,
        Subject::Checkpoints => SubjectKind::Checkpoints,
        Subject::Record(_) => SubjectKind::Record,
        Subject::Range(..) => SubjectKind::Range,
        Subject::Correlation(_) => SubjectKind::Correlation,
        _ => SubjectKind::CitingRecords,
    }
}

/// The `profile` member of a request: absent, text, or present but not text
/// (profile §6.1 step 5). The request already parsed, so the octets decode.
enum NamedProfile {
    Absent,
    Text(String),
    NotText,
}

fn named_profile(octets: &[u8], binding: Binding) -> NamedProfile {
    match binding {
        Binding::Json => match serde_json::from_slice::<Value>(octets)
            .ok()
            .and_then(|v| v.get("profile").cloned())
        {
            None => NamedProfile::Absent,
            Some(Value::String(s)) => NamedProfile::Text(s),
            Some(_) => NamedProfile::NotText,
        },
        Binding::Cbor => {
            let Ok(ciborium::Value::Map(entries)) = ciborium::from_reader(octets) else {
                return NamedProfile::Absent;
            };
            for (k, v) in entries {
                if matches!(&k, ciborium::Value::Text(k) if k == "profile") {
                    return match v {
                        ciborium::Value::Text(v) => NamedProfile::Text(v),
                        _ => NamedProfile::NotText,
                    };
                }
            }
            NamedProfile::Absent
        }
    }
}

/// Refuses a JSON text that repeats a member name at any depth: the JSON
/// reader of `evidence-request` does not detect them (its own doc says so),
/// and Go's reader refuses them as a map that is not well formed.
struct NoDuplicates;

impl<'de> serde::Deserialize<'de> for NoDuplicates {
    fn deserialize<D: serde::Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct Visitor;
        impl<'de> serde::de::Visitor<'de> for Visitor {
            type Value = NoDuplicates;
            fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str("JSON with no repeated member name")
            }
            fn visit_map<A: serde::de::MapAccess<'de>>(
                self,
                mut map: A,
            ) -> Result<NoDuplicates, A::Error> {
                let mut seen = std::collections::BTreeSet::new();
                while let Some(key) = map.next_key::<String>()? {
                    if !seen.insert(key.clone()) {
                        return Err(serde::de::Error::custom(format!("repeated member {key:?}")));
                    }
                    map.next_value::<NoDuplicates>()?;
                }
                Ok(NoDuplicates)
            }
            fn visit_seq<A: serde::de::SeqAccess<'de>>(
                self,
                mut seq: A,
            ) -> Result<NoDuplicates, A::Error> {
                while seq.next_element::<NoDuplicates>()?.is_some() {}
                Ok(NoDuplicates)
            }
            fn visit_bool<E>(self, _: bool) -> Result<NoDuplicates, E> {
                Ok(NoDuplicates)
            }
            fn visit_i64<E>(self, _: i64) -> Result<NoDuplicates, E> {
                Ok(NoDuplicates)
            }
            fn visit_u64<E>(self, _: u64) -> Result<NoDuplicates, E> {
                Ok(NoDuplicates)
            }
            fn visit_f64<E>(self, _: f64) -> Result<NoDuplicates, E> {
                Ok(NoDuplicates)
            }
            fn visit_str<E>(self, _: &str) -> Result<NoDuplicates, E> {
                Ok(NoDuplicates)
            }
            fn visit_unit<E>(self) -> Result<NoDuplicates, E> {
                Ok(NoDuplicates)
            }
        }
        d.deserialize_any(Visitor)
    }
}

type Refused = (Reason, Option<&'static str>);

/// Read request octets (profile §6.1 steps 1 to 5).
fn read_request(octets: &[u8], binding: Binding) -> Result<Request, Refused> {
    let parsed = match binding {
        Binding::Json => {
            if serde_json::from_slice::<NoDuplicates>(octets).is_err() {
                return Err((Reason::RequestMalformed, None));
            }
            request::parse_json(octets)
        }
        Binding::Cbor => request::parse_cbor(octets),
    };
    let req = parsed.map_err(|e| (e.reason(), None))?;
    if !crate::ask::derivation_is_well_formed(&req) {
        return Err((Reason::RequestMalformed, None));
    }
    // Step 5: a profile this responder does not implement (§3.3 step 4). A
    // present member that is not text is not a well-formed request; an empty
    // one names nothing, as an absent one.
    match named_profile(octets, binding) {
        NamedProfile::Absent => {}
        NamedProfile::NotText => return Err((Reason::RequestMalformed, None)),
        NamedProfile::Text(profile) => {
            if !profile.is_empty() && profile != PROFILE_ID {
                return Err((Reason::RequestMalformed, Some(const_concat_profiles())));
            }
        }
    }
    Ok(req)
}

fn const_concat_profiles() -> &'static str {
    "profiles supported: evidence-interop/0"
}

/// Classify an authenticated requester by `counterparty_ref` on the book's
/// own records. It is only ever given the authenticated identity: a
/// `counterparty_ref` value is not a credential, so knowing one grants
/// nothing. Records of answered requests name no requester, so asking never
/// makes a stranger a counterparty.
fn relationship<B: Book>(book: &B, authenticated: Option<&str>) -> Relationship {
    let Some(authenticated) = authenticated else {
        return Relationship::Stranger;
    };
    for leaf in 0..book.len() {
        let counterparty = book
            .record_at(leaf)
            .and_then(|r| book.header_of(&r))
            .and_then(|h| {
                h.get("counterparty_ref")
                    .and_then(Value::as_str)
                    .map(str::to_string)
            });
        if counterparty.as_deref() == Some(authenticated) {
            return Relationship::Counterparty;
        }
    }
    Relationship::Identified
}

/// What a decision reads besides the evidence: who asks, and under which
/// policies.
struct Gate<'a> {
    requester: Option<&'a str>,
    relationship: Relationship,
    share: &'a SharePolicy,
    derivations: &'a Derivations,
    uniform: bool,
}

impl Gate<'_> {
    /// Applies the uniform refusal policy to a reason that would disclose
    /// what the policy protects.
    fn disclose(&self, reason: Reason) -> Reason {
        if self.uniform
            && matches!(
                reason,
                Reason::NotAuthorized | Reason::NoSuchSubject | Reason::CoverageUnsatisfiable
            )
        {
            Reason::PolicyDeclined
        } else {
            reason
        }
    }
}

/// One coverage anchor a book has issued: its digest, the number of log
/// entries it covers, and when it was issued.
struct AnchorFacts {
    digest: String,
    entries: u64,
    issued: UtcTime,
}

/// What a decision reads of a book's evidence (Go `evidencerequest.View`).
/// Positions are zero-based log positions; matching is exact.
trait Evidence {
    /// The anchors issued, oldest first.
    fn anchors(&self) -> &[AnchorFacts];
    /// The entries the log holds now, covered or not.
    fn entries(&self) -> u64;
    /// The position of a servable record.
    fn leaf_of(&self, digest: &str) -> Option<u64>;
    /// Whether the record at `leaf` may be served at all.
    fn servable(&self, leaf: u64) -> bool;
    /// The positions of the servable records carrying a correlation
    /// identifier, as the host's store keys them.
    fn correlated(&self, id: &str) -> Vec<u64>;
    /// The positions of the servable records citing a half.
    fn citing(&self, half: &str) -> Vec<u64>;
    /// The counterparty the record at `leaf` names.
    fn counterparty(&self, leaf: u64) -> Option<String>;
    /// A retention commitment for exactly this subject whose `until` has
    /// passed (ER §9).
    fn lapsed(&self, subject: &Subject, now: UtcTime) -> bool;

    /// The counterparty of each servable record a record or citing-records
    /// subject names, whether or not the anchor covers them yet.
    fn named(&self, subject: &Subject) -> Vec<Option<String>> {
        let leaves: Vec<u64> = match (subject, citing_half(subject)) {
            (Subject::Record(d), _) => self.leaf_of(d).into_iter().collect(),
            (_, Some(half)) => self.citing(half),
            _ => Vec::new(),
        };
        leaves.into_iter().map(|l| self.counterparty(l)).collect()
    }
}

/// The book as resolution reads it: its stated checkpoints, and for every
/// leaf its record type, counterparty and servability.
struct View<'a> {
    checkpoints: Vec<StatedCheckpoint>,
    anchors: Vec<AnchorFacts>,
    entries: u64,
    leaves: Vec<LeafFacts>,
    commitments: Vec<(Subject, UtcTime)>,
    by_entry: BTreeMap<String, u64>,
    src: &'a dyn RecordSource,
}

struct LeafFacts {
    servable: bool,
    counterparty: Option<String>,
}

impl<'a> View<'a> {
    fn of<B: Book + CheckpointedLog>(book: &'a B) -> Result<View<'a>, RespondError> {
        let checkpoints = book.stated_checkpoints()?;
        let mut anchors = Vec::with_capacity(checkpoints.len());
        for cp in &checkpoints {
            let issued = parse_utc(&cp.checkpoint.timestamp).ok_or_else(|| {
                RespondError::Invalid(format!("checkpoint time {:?}", cp.checkpoint.timestamp))
            })?;
            anchors.push(AnchorFacts {
                digest: anchor_digest(cp),
                entries: cp.checkpoint.leaf_count()?,
                issued,
            });
        }
        let entries = book.len();
        let mut leaves = Vec::with_capacity(entries as usize);
        let mut commitments = Vec::new();
        let mut by_entry = BTreeMap::new();
        for leaf in 0..entries {
            let record: Option<Record> = book.record_at(leaf);
            let header = record.as_ref().and_then(|r| book.header_of(r));
            // A record whose type cannot be read cannot be known servable:
            // serving it could hand out the responder's own bookkeeping.
            if header.is_none() {
                return Err(RespondError::Invalid(format!(
                    "the book holds no header for the record at leaf {leaf}; a responder needs RecordSource::header_of"
                )));
            }
            let kind = header
                .as_ref()
                .and_then(|h| h.get("record_type").and_then(Value::as_str))
                .unwrap_or("");
            // The responder's own bookkeeping of who asked and what it
            // disclosed is never served, so one requester's asking or
            // receiving can never be read by another (NFR-2, NFR-4).
            let servable = !matches!(
                kind,
                record_types::REQUEST_ANSWERED | record_types::DISCLOSURE
            );
            let counterparty = header
                .as_ref()
                .and_then(|h| h.get("counterparty_ref").and_then(Value::as_str))
                .map(str::to_string);
            if let Some(record) = &record {
                by_entry.entry(record.digest.clone()).or_insert(leaf);
            }
            if kind == "retention_commitment" {
                if let Some(c) = header
                    .as_ref()
                    .and_then(|h| h.get("statement"))
                    .and_then(read_commitment)
                {
                    commitments.push(c);
                }
            }
            leaves.push(LeafFacts {
                servable,
                counterparty,
            });
        }
        Ok(View {
            checkpoints,
            anchors,
            entries,
            leaves,
            commitments,
            by_entry,
            src: book,
        })
    }
}

impl Evidence for View<'_> {
    fn servable(&self, leaf: u64) -> bool {
        self.leaves.get(leaf as usize).is_some_and(|l| l.servable)
    }

    fn leaf_of(&self, digest: &str) -> Option<u64> {
        self.by_entry
            .get(digest)
            .copied()
            .filter(|&l| self.servable(l))
    }

    fn correlated(&self, id: &str) -> Vec<u64> {
        let mut leaves = self.src.correlation(id);
        leaves.retain(|&l| self.servable(l));
        leaves
    }

    fn citing(&self, half: &str) -> Vec<u64> {
        let mut leaves = self.src.citing(half);
        leaves.retain(|&l| self.servable(l));
        leaves
    }

    fn lapsed(&self, subject: &Subject, now: UtcTime) -> bool {
        self.commitments
            .iter()
            .any(|(s, until)| s == subject && now > *until)
    }

    fn counterparty(&self, leaf: u64) -> Option<String> {
        self.leaves.get(leaf as usize)?.counterparty.clone()
    }

    fn anchors(&self) -> &[AnchorFacts] {
        &self.anchors
    }

    fn entries(&self) -> u64 {
        self.entries
    }
}

fn read_commitment(statement: &Value) -> Option<(Subject, UtcTime)> {
    let subject = request::subject_from_value(statement.get("subject")?).ok()?;
    let until = evidence_request::envelope::is_profile_time(statement.get("until")?.as_str()?)
        .then(|| parse_utc(statement["until"].as_str()?))
        .flatten()?;
    Some((subject, until))
}

enum Decision {
    Refuse(Reason),
    /// Issue a fresh anchor and decide again.
    NeedAnchor,
    Serve {
        anchor: usize,
        half_pin: bool,
        leaves: Vec<u64>,
    },
}

/// Respond's steps that follow the request reader, in the request draft's
/// order: the deadline, derivation support (at all, then for this subject; ER
/// §3.3), authorization, coverage, the subject, and the refusal policy.
/// `may_issue` says whether the responder will issue an anchor to meet a
/// freshness floor.
fn decide(
    view: &dyn Evidence,
    req: &Request,
    gate: &Gate<'_>,
    authorize: &dyn Fn(&Request, Option<&Registered>) -> bool,
    now: UtcTime,
    may_issue: bool,
) -> Decision {
    decide_inner(view, req, gate, authorize, now, may_issue)
}

fn decide_inner(
    view: &dyn Evidence,
    req: &Request,
    gate: &Gate<'_>,
    authorize: &dyn Fn(&Request, Option<&Registered>) -> bool,
    now: UtcTime,
    may_issue: bool,
) -> Decision {
    use Decision::Refuse;
    if let Some(deadline) = req.deadline {
        if now >= deadline {
            return Refuse(Reason::DeadlineUnmet);
        }
    }
    let form = req.subject.form();
    let mut registered: Option<&Registered> = None;
    match &req.derivation {
        Some(Derivation::Token(t)) if t == answer::DERIVATION_HISTORY_CARD => {
            if !matches!(req.subject, Subject::FullHistory | Subject::Checkpoints) {
                return Refuse(Reason::NoSuchSubject);
            }
        }
        Some(Derivation::Token(_)) => return Refuse(Reason::DerivationUnsupported),
        Some(Derivation::Digest(id)) => match gate.derivations.get(id) {
            Some(r) => registered = Some(r),
            None => return Refuse(Reason::DerivationUnsupported),
        },
        None => {}
    }
    // From here the refusal policy applies; the deadline, derivation support
    // and a history card over another subject are answered as they are (Go
    // `evidencerequest.Decide`).
    if !authorize(req, registered) {
        return Refuse(gate.disclose(Reason::NotAuthorized));
    }
    if let Some(r) = registered {
        if !r.declares(form) {
            return Refuse(gate.disclose(Reason::NoSuchSubject));
        }
    }
    let (anchor, half_pin) = match resolve_coverage(view, req, now, may_issue) {
        Ok(chosen) => chosen,
        Err(Refuse(reason)) => return Refuse(gate.disclose(reason)),
        Err(decision) => return decision,
    };
    match resolve_subject(view, req, anchor, half_pin, now) {
        Refuse(reason) => Refuse(gate.disclose(reason)),
        other => other,
    }
}

/// Authorization decides whether, never what (ER §3.7, §5). It is decided
/// before the subject is resolved, from the records the subject names.
fn authorized(
    view: &dyn Evidence,
    req: &Request,
    gate: &Gate<'_>,
    registered: Option<&Registered>,
) -> bool {
    let public = (matches!(req.subject, Subject::Checkpoints) && req.derivation.is_none())
        || is_history_card(req);
    if public {
        return true; // commitments only
    }
    if let Some(recipient) = registered.and_then(Registered::recipient) {
        // The holder's grant to the one requester it names.
        return gate.requester == Some(recipient);
    }
    match &req.subject {
        Subject::Record(_) => {}
        subject if citing_half(subject).is_some() => {}
        // range, correlation and full_history reach records no one
        // requester is party to; nothing but a recipient-specific
        // derivation grants them.
        _ => return false,
    }
    gate.share
        .may_have(gate.requester, gate.relationship, &view.named(&req.subject))
}

/// Picks the anchor (ER §3.2, profile §6.1 step 6). A pin must name an
/// anchor this responder issued, or, for a citing-records subject, the
/// requester's own half. A freshness floor is met by the newest
/// anchor that meets it; when none does, `may_issue` says whether the
/// responder will do the work of issuing one.
fn resolve_coverage(
    view: &dyn Evidence,
    req: &Request,
    now: UtcTime,
    may_issue: bool,
) -> Result<(usize, bool), Decision> {
    let unsatisfiable = || Decision::Refuse(Reason::CoverageUnsatisfiable);
    match &req.coverage {
        Coverage::ExpectedPin(pin) => {
            if let Some(i) = view.anchors().iter().position(|a| a.digest == *pin) {
                return Ok((i, false));
            }
            if citing_half(&req.subject) == Some(pin.as_str()) {
                return Ok((usize::MAX, true));
            }
            Err(unsatisfiable())
        }
        Coverage::MinFreshness(floor) => {
            for i in (0..view.anchors().len()).rev() {
                let AnchorFacts {
                    entries, issued, ..
                } = view.anchors()[i];
                let meets = match floor {
                    Freshness::Size(n) => entries >= *n,
                    // Seconds precision, profile §5.2.
                    Freshness::Time(t) => {
                        UtcTime {
                            seconds: issued.seconds,
                            nanos: 0,
                        } >= *t
                    }
                };
                if meets {
                    return Ok((i, false));
                }
            }
            // No anchor meets the floor. A floor no anchor could meet yet is
            // refused without doing any work: a time in the future, or more
            // entries than the log holds.
            let beyond = match floor {
                Freshness::Size(n) => *n > view.entries(),
                Freshness::Time(t) => *t > now,
            };
            if beyond || !may_issue || view.entries() == 0 {
                return Err(unsatisfiable());
            }
            Err(Decision::NeedAnchor)
        }
    }
}

/// Finds the servable records the subject names under the anchor coverage
/// chose (ER §3.1). A subject that exists only beyond the anchor is
/// `coverage_unsatisfiable`, never `no_such_subject`: the responder does
/// hold it. Matching is exact.
fn resolve_subject(
    view: &dyn Evidence,
    req: &Request,
    anchor: usize,
    half_pin: bool,
    now: UtcTime,
) -> Decision {
    use Decision::Refuse;
    let no_such = || {
        Refuse(if view.lapsed(&req.subject, now) {
            Reason::RetentionExpired
        } else {
            Reason::NoSuchSubject
        })
    };
    let mut anchor = anchor;
    if half_pin {
        // The requester pinned its own half: the anchor is the earliest one
        // covering every anchored record that cites it, so growth that adds
        // no citing record never changes the artifact (ER §3.2, §5).
        let half = citing_half(&req.subject).expect("a half pin is a citing-records subject");
        let cited = view.citing(half);
        if cited.is_empty() {
            return no_such();
        }
        let latest = view.anchors().last().map_or(0, |a| a.entries);
        let Some(last) = cited.iter().copied().filter(|&l| l < latest).max() else {
            return Refuse(Reason::CoverageUnsatisfiable);
        };
        match view.anchors().iter().position(|a| a.entries > last) {
            Some(i) => anchor = i,
            None => return Refuse(Reason::CoverageUnsatisfiable),
        }
    }
    if anchor >= view.anchors().len() {
        return Refuse(Reason::CoverageUnsatisfiable);
    }
    let covered = view.anchors()[anchor].entries;
    let serve = |leaves: Vec<u64>| Decision::Serve {
        anchor,
        half_pin,
        leaves,
    };
    match &req.subject {
        Subject::Checkpoints => serve(Vec::new()),
        Subject::FullHistory => {
            let leaves: Vec<u64> = (0..covered).filter(|&l| view.servable(l)).collect();
            if leaves.is_empty() && req.derivation.is_none() {
                return Refuse(Reason::NoSuchSubject);
            }
            serve(leaves)
        }
        Subject::Record(d) => match view.leaf_of(d) {
            None => no_such(),
            Some(leaf) if leaf >= covered => Refuse(Reason::CoverageUnsatisfiable),
            Some(leaf) => serve(vec![leaf]),
        },
        Subject::Range(a, b) => {
            if *b >= covered {
                if *b < view.entries() {
                    return Refuse(Reason::CoverageUnsatisfiable);
                }
                return no_such();
            }
            let leaves: Vec<u64> = (*a..=*b).filter(|&l| view.servable(l)).collect();
            if leaves.is_empty() {
                return no_such();
            }
            serve(leaves)
        }
        subject => {
            let all = match (subject, citing_half(subject)) {
                (Subject::Correlation(id), _) => view.correlated(id),
                (_, Some(half)) => view.citing(half),
                _ => unreachable!("every other form is matched above"),
            };
            let mut leaves: Vec<u64> = all.iter().copied().filter(|&l| l < covered).collect();
            leaves.sort_unstable();
            leaves.dedup();
            if !leaves.is_empty() {
                return serve(leaves);
            }
            if !all.is_empty() {
                return Refuse(Reason::CoverageUnsatisfiable);
            }
            no_such()
        }
    }
}

/// The request-draft conformance vectors (`testdata/evidence-request`, vendored
/// byte for byte from agent-action-capsule `vectors/evidence-request/`), run
/// through this responder's own request reader and decision, as Go's
/// `interop_vectors_test.go` and `interop_resolution_test.go` run them.
#[cfg(test)]
mod vectors {
    use super::*;
    use std::collections::BTreeMap;
    use std::path::PathBuf;

    fn corpus(name: &str) -> Value {
        let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../testdata/evidence-request")
            .join(name);
        serde_json::from_slice(&std::fs::read(&path).unwrap_or_else(|e| panic!("{path:?}: {e}")))
            .unwrap()
    }

    fn cases(corpus: &Value) -> &Vec<Value> {
        corpus["cases"].as_array().unwrap()
    }

    #[test]
    fn the_request_vectors_in_both_bindings() {
        let corpus = corpus("request.json");
        assert_eq!(cases(&corpus).len(), 33);
        let mut cbor = 0;
        for case in cases(&corpus) {
            let id = case["id"].as_str().unwrap();
            let expect = &case["expect"];
            let mut bindings = vec![(
                Binding::Json,
                case["request_json"].as_str().unwrap().as_bytes().to_vec(),
                case["request_json_digest"].as_str(),
            )];
            if let Some(h) = case["request_cbor_hex"].as_str().filter(|h| !h.is_empty()) {
                bindings.push((
                    Binding::Cbor,
                    hex::decode(h).unwrap(),
                    case["request_cbor_digest"].as_str(),
                ));
                cbor += 1;
            }
            for (binding, octets, digest) in bindings {
                if let Some(d) = digest.filter(|d| !d.is_empty()) {
                    assert_eq!(
                        sha256_hex(&octets),
                        d,
                        "{id}: the digest is over the octets"
                    );
                }
                let got = read_request(&octets, binding);
                if expect["well_formed"] == json!(true) {
                    assert!(got.is_ok(), "{id} {binding:?}: refused {:?}", got.err());
                } else {
                    let (reason, _) = got
                        .err()
                        .unwrap_or_else(|| panic!("{id} {binding:?}: read"));
                    assert_eq!(reason.token(), expect["reason"], "{id} {binding:?}");
                }
            }
        }
        assert!(cbor > 0);
    }

    /// The `resolution.json` responder state as evidence: records at positions
    /// 0, 1, 2 of a 42-entry log, two anchors, the declared correlation and
    /// citations.
    struct Vector {
        log_size: u64,
        anchors: Vec<AnchorFacts>,
        records: Vec<String>,
        correlation: BTreeMap<String, Vec<String>>,
        citations: BTreeMap<String, Vec<String>>,
    }

    impl Vector {
        fn positions(&self, digests: Option<&Vec<String>>) -> Vec<u64> {
            digests
                .into_iter()
                .flatten()
                .filter_map(|d| self.leaf_of(d))
                .collect()
        }
    }

    impl Evidence for Vector {
        fn anchors(&self) -> &[AnchorFacts] {
            &self.anchors
        }
        fn entries(&self) -> u64 {
            self.log_size
        }
        fn leaf_of(&self, digest: &str) -> Option<u64> {
            self.records
                .iter()
                .position(|r| r == digest)
                .map(|i| i as u64)
        }
        fn servable(&self, leaf: u64) -> bool {
            leaf < self.records.len() as u64
        }
        fn correlated(&self, id: &str) -> Vec<u64> {
            self.positions(self.correlation.get(id))
        }
        fn citing(&self, half: &str) -> Vec<u64> {
            self.positions(self.citations.get(half))
        }
        fn counterparty(&self, _: u64) -> Option<String> {
            None
        }
        fn lapsed(&self, _: &Subject, _: UtcTime) -> bool {
            false
        }
    }

    fn strings(v: &Value) -> Vec<String> {
        v.as_array()
            .unwrap()
            .iter()
            .map(|s| s.as_str().unwrap().to_string())
            .collect()
    }

    fn map_of(v: &Value) -> BTreeMap<String, Vec<String>> {
        v.as_object()
            .unwrap()
            .iter()
            .map(|(k, v)| (k.clone(), strings(v)))
            .collect()
    }

    /// Respond's own decision over a declared state, with every request
    /// authorized: the vectors fix resolution, and the requester gate has
    /// its own tests. The refusal reason, or `""` for an artifact.
    fn decide_for_vector(
        view: &Vector,
        request: &str,
        now: UtcTime,
        may_issue: bool,
        uniform: bool,
    ) -> String {
        let req = match read_request(request.as_bytes(), Binding::Json) {
            Ok(req) => req,
            Err((reason, _)) => return reason.token().to_string(),
        };
        let derivations = Derivations::new();
        let share = SharePolicy::default();
        let gate = Gate {
            requester: Some("peer"),
            relationship: Relationship::Identified,
            share: &share,
            derivations: &derivations,
            uniform,
        };
        match decide(view, &req, &gate, &|_, _| true, now, may_issue) {
            Decision::Refuse(reason) => reason.token().to_string(),
            Decision::NeedAnchor => gate.disclose(Reason::CoverageUnsatisfiable).token().into(),
            Decision::Serve { .. } => String::new(),
        }
    }

    #[test]
    fn the_resolution_vectors() {
        let corpus = corpus("resolution.json");
        let state = &corpus["responder"];
        assert_eq!(
            state["supported_derivations"],
            json!({"history_card/1": ["full_history", "checkpoints"]}),
            "the corpus's derivations differ from this responder's"
        );
        let view = Vector {
            log_size: state["log_size"].as_u64().unwrap(),
            anchors: state["anchors"]
                .as_array()
                .unwrap()
                .iter()
                .map(|a| AnchorFacts {
                    digest: a["digest"].as_str().unwrap().into(),
                    entries: a["size"].as_u64().unwrap(),
                    issued: parse_utc(a["issued_at"].as_str().unwrap()).unwrap(),
                })
                .collect(),
            records: strings(&state["records"]),
            correlation: map_of(&state["correlations"]),
            // The corpus names this member for the citing-records subject by
            // its request-draft name; read it by its suffix.
            citations: map_of(
                state
                    .as_object()
                    .unwrap()
                    .iter()
                    .find(|(name, _)| name.ends_with("_citations"))
                    .map(|(_, v)| v)
                    .expect("the corpus declares the citing records"),
            ),
        };
        let now = parse_utc("2026-09-28T12:00:00Z").unwrap();
        let may_issue = state["issues_new_commitments"] == json!(true);
        let (mut ran, mut skipped) = (0, 0);
        for case in cases(&corpus) {
            let id = case["id"].as_str().unwrap();
            let overrides = &case["responder_overrides"];
            if overrides["positional_ordering"] == json!(false) {
                // A Checkpointed Local Log always defines positions; this
                // responder state cannot occur.
                skipped += 1;
                continue;
            }
            let uniform = overrides["uniform_policy_declined"]
                .as_bool()
                .unwrap_or(state["uniform_policy_declined"] == json!(true));
            let got = decide_for_vector(
                &view,
                case["request_json"].as_str().unwrap(),
                now,
                may_issue,
                uniform,
            );
            let expect = &case["expect"];
            let want = if expect["answer"] == "artifact" {
                ""
            } else {
                expect["reason"].as_str().unwrap()
            };
            assert_eq!(got, want, "{id}");
            ran += 1;
        }
        assert_eq!((ran, skipped), (18, 1), "ran {ran} of 19 resolution cases");
    }

    #[test]
    fn the_refusal_vectors() {
        let corpus = corpus("refusal.json");
        assert_eq!(cases(&corpus).len(), 21);
        for case in cases(&corpus) {
            let id = case["id"].as_str().unwrap();
            let object: Value = match case["file"].as_str() {
                Some(file) => serde_json::from_slice(
                    &std::fs::read(
                        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
                            .join("../../testdata/evidence-request")
                            .join(file),
                    )
                    .unwrap(),
                )
                .unwrap(),
                None => case["refusal"].clone(),
            };
            let check = evidence_request::refusal::check(&object);
            let expect = &case["expect"];
            assert_eq!(
                check.signature_valid,
                expect["signature"] == "valid",
                "{id}: signature"
            );
            assert_eq!(
                check.conformant,
                expect["conformant"] == json!(true),
                "{id}: conformant"
            );
            if let Some(registered) = expect["reason_registered"].as_bool() {
                assert_eq!(check.reason_registered, registered, "{id}: reason");
            }
        }
    }
}
