//! The requester's side of a request: the record of having asked, sealed
//! before the request is sent, and the record that nothing came back in the
//! waiting window.
//!
//! I-D section: *Answering a Request* (the requester's double entry; the
//! outcome a requester records when no reply arrives is *recorded absence*,
//! never a refusal). These are the Go book's `Request` and `RecordAbsence`
//! (`request.go`); the record kinds, statements and refusals match them
//! member for member.
//!
//! The crate does not own a record store. A caller's [`Book`] seals the
//! entries this module builds, with the position and commit time it assigns,
//! exactly as it seals every other record.

use crate::record::{EpistemicType, Link, LinkType};
use crate::store::RecordSource;
use evidence_request::time::parse_utc;
use rand_core::{OsRng, RngCore};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

/// The profile identifier a request carries (`evidence-interop/0`).
pub const PROFILE_ID: &str = "evidence-interop/0";

/// The shortest waiting window an absence may claim, measured from when the
/// request was recorded (Go `MinimumAbsenceWindow`).
pub const MINIMUM_ABSENCE_WINDOW_SECONDS: i64 = 60;

/// One record to seal: what the host's [`Book::seal`] turns into a record by
/// assigning the next position and the commit time.
#[derive(Clone, Debug, PartialEq)]
pub struct Entry {
    pub record_type: String,
    pub epistemic_type: EpistemicType,
    pub links: Vec<Link>,
    /// The header's `statement` member.
    pub statement: Value,
}

/// A record the book sealed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Sealed {
    /// The record id: the entry the log commits for it.
    pub record_id: String,
    /// The commit time the book assigned (RFC 3339 UTC).
    pub committed_at: String,
}

/// Why a book could not seal an entry.
#[derive(Debug, thiserror::Error)]
#[error("seal: {0}")]
pub struct SealError(pub String);

/// The writable book a requester and a responder act on: its records (read
/// through [`RecordSource`]), its clock, and the two things only the book
/// can do, seal a record and cut a checkpoint.
///
/// An implementation is the caller's own store. `seal` and `checkpoint` are
/// called under whatever exclusion the implementation needs; this crate
/// calls them from one thread at a time through `&mut self`.
pub trait Book: RecordSource {
    /// The number of records committed.
    fn len(&self) -> u64;
    /// Now, as RFC 3339 UTC (`YYYY-MM-DDTHH:MM:SS[.f]Z`).
    fn now(&self) -> String;
    /// Seal `entry` as the next record and commit it.
    fn seal(&mut self, entry: &Entry) -> Result<Sealed, SealError>;
    /// Cut a checkpoint over everything committed.
    fn checkpoint(&mut self) -> Result<(), SealError>;

    /// True when no record is committed.
    fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

/// Why asking or recording an absence failed.
#[derive(Debug, thiserror::Error)]
pub enum AskError {
    /// The request or the call is not valid: nothing was recorded.
    #[error("invalid: {0}")]
    Invalid(String),
    /// The id is not a request this book recorded.
    #[error("not found: {0}")]
    NotFound(String),
    /// A committed record does not read back as what it should be.
    #[error("corrupt: {0}")]
    Corrupt(String),
    #[error(transparent)]
    Seal(#[from] SealError),
}

/// A request to send: the six-form subject and coverage as the request
/// draft's JSON members, so a caller states exactly what travels.
///
/// `subject` and `coverage` are the members' JSON values (`{"record":
/// "<digest>"}`, `{"expected_pin": "<digest>"}`); `derivation`, `deadline`
/// and `route` are carried when set.
#[derive(Clone, Debug, PartialEq)]
pub struct AskedRequest {
    pub subject: Value,
    pub coverage: Value,
    pub derivation: Option<String>,
    pub deadline: Option<String>,
    pub route: Option<String>,
    /// Empty means a fresh random nonce.
    pub nonce: Option<String>,
}

/// A request as transmitted: its octets (JCS JSON), their digest, and the id
/// of the record of having asked.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SentRequest {
    pub bytes: Vec<u8>,
    pub digest: String,
    pub record_id: String,
}

fn sha256_hex(bytes: &[u8]) -> String {
    hex::encode(Sha256::digest(bytes))
}

/// Record that this book is asking `responder` for evidence and return the
/// octets to transmit: the request map under this profile, in JCS. Recording
/// happens before sending, so "I asked" is committed whatever comes back. A
/// nonce this book already used is refused: a unique nonce gives every
/// request unique bytes, so a response to an earlier identical request can
/// never be replayed onto it.
pub fn request(
    book: &mut dyn Book,
    asked: &AskedRequest,
    responder: &str,
) -> Result<SentRequest, AskError> {
    let nonce = match asked.nonce.as_deref().filter(|n| !n.is_empty()) {
        Some(n) => n.to_string(),
        None => {
            let mut raw = [0u8; 16];
            OsRng.fill_bytes(&mut raw);
            hex::encode(raw)
        }
    };
    let mut map = Map::new();
    map.insert("subject".into(), asked.subject.clone());
    map.insert("coverage".into(), asked.coverage.clone());
    if let Some(d) = asked.derivation.as_deref().filter(|d| !d.is_empty()) {
        map.insert("derivation".into(), json!(d));
    }
    if let Some(d) = asked.deadline.as_deref().filter(|d| !d.is_empty()) {
        map.insert("deadline".into(), json!(d));
    }
    if let Some(r) = asked.route.as_deref().filter(|r| !r.is_empty()) {
        map.insert("route".into(), json!(r));
    }
    map.insert("nonce".into(), json!(nonce));
    map.insert("profile".into(), json!(PROFILE_ID));
    let value = Value::Object(map);
    let bytes = evidence_request::jcs::to_string(&value)
        .map_err(|e| AskError::Invalid(format!("request: {e}")))?
        .into_bytes();
    // The same reader the responder uses: a request that would not be read
    // as well formed is never sent.
    parse_request(&bytes).map_err(|why| AskError::Invalid(format!("request: {why}")))?;
    record_request(book, value, bytes, Some(nonce), responder)
}

/// Record a request that another tool composed and sent to `responder`,
/// exactly as given (Go `Book.RequestSent`): every member is kept, including
/// those the request draft does not define (a deployment profile's
/// `audience` or requester authentication, ER §3), and no nonce or profile
/// is added. The octets must be a well-formed request (profile §6.1 steps 1
/// to 5) in JCS, the form a requester transmits JSON in (P-9), so the
/// recorded digest is the one the responder signs its reply over. A nonce
/// this book already used is refused; a request without one has nothing to
/// compare.
pub fn request_sent(
    book: &mut dyn Book,
    octets: &[u8],
    responder: &str,
) -> Result<SentRequest, AskError> {
    let req = crate::respond::read_sent_request(octets).map_err(|reason| {
        AskError::Invalid(format!("not a well-formed request ({})", reason.token()))
    })?;
    // The record's statement embeds the octets as JSON, so octets in any
    // other form than JCS would not survive as sent.
    let value: Value =
        serde_json::from_slice(octets).map_err(|e| AskError::Invalid(format!("request: {e}")))?;
    let jcs = evidence_request::jcs::to_string(&value)
        .map_err(|e| AskError::Invalid(format!("request: {e}")))?;
    if jcs.as_bytes() != octets {
        return Err(AskError::Invalid(
            "a sent request must be in JCS (RFC 8785), as it was transmitted".into(),
        ));
    }
    let nonce = req.nonce.filter(|n| !n.is_empty());
    record_request(book, value, octets.to_vec(), nonce, responder)
}

/// Commit the request `value` (whose JCS is `bytes`), as transmitted to
/// `responder`, refusing a nonce this book already used.
fn record_request(
    book: &mut dyn Book,
    value: Value,
    bytes: Vec<u8>,
    nonce: Option<String>,
    responder: &str,
) -> Result<SentRequest, AskError> {
    for leaf in 0..book.len() {
        let Some(nonce) = nonce.as_deref() else {
            break;
        };
        let Some(record) = book.record_at(leaf) else {
            continue;
        };
        let Some(header) = book.header_of(&record) else {
            continue;
        };
        if header.get("record_type").and_then(Value::as_str)
            != Some(crate::record::record_types::REQUEST)
        {
            continue;
        }
        if let Ok(prior) = sent_request(book, &record.digest) {
            if prior.nonce.as_deref() == Some(nonce) {
                return Err(AskError::Invalid(format!(
                    "nonce already used by request {}",
                    record.digest
                )));
            }
        }
    }

    let mut statement = Map::new();
    statement.insert("request".into(), value);
    if !responder.is_empty() {
        statement.insert("responder_ref".into(), json!(responder));
    }
    let sealed = book.seal(&Entry {
        record_type: crate::record::record_types::REQUEST.into(),
        epistemic_type: EpistemicType::ObservedEvent,
        links: Vec::new(),
        statement: Value::Object(statement),
    })?;
    Ok(SentRequest {
        digest: sha256_hex(&bytes),
        bytes,
        record_id: sealed.record_id,
    })
}

fn parse_request(bytes: &[u8]) -> Result<evidence_request::request::Request, String> {
    let req = evidence_request::request::parse_json(bytes).map_err(|e| e.to_string())?;
    if !derivation_is_well_formed(&req) {
        return Err("derivation is neither a registered token form nor a definition digest".into());
    }
    Ok(req)
}

/// A derivation is a token `name/N` (lowercase letters, digits and underscore,
/// then a decimal version) or a definition digest, as Go's `ValidDerivation`
/// (profile §5.2). The `evidence-request` reader accepts any name before the
/// last `/`, which would answer `derivation_unsupported` where Go refuses the
/// request as malformed.
pub(crate) fn derivation_is_well_formed(req: &evidence_request::request::Request) -> bool {
    use evidence_request::request::Derivation;
    match &req.derivation {
        Some(Derivation::Token(t)) => derivation_text_is_well_formed(t),
        _ => true,
    }
}

/// [`derivation_is_well_formed`] for the member's text: a token or a digest.
pub(crate) fn derivation_text_is_well_formed(text: &str) -> bool {
    crate::record::is_digest(text)
        || text.split_once('/').is_some_and(|(name, version)| {
            !name.is_empty()
                && name
                    .bytes()
                    .all(|b| matches!(b, b'a'..=b'z' | b'0'..=b'9' | b'_'))
                && !version.is_empty()
                && version.bytes().all(|b| b.is_ascii_digit())
        })
}

/// A request re-read from its committed record.
struct Sent {
    digest: String,
    committed_at: String,
    deadline: Option<String>,
    route: Option<String>,
    nonce: Option<String>,
}

fn sent_request(book: &dyn Book, record_id: &str) -> Result<Sent, AskError> {
    let not_a_request =
        || AskError::NotFound(format!("{record_id} is not a request this book recorded"));
    let record = book.record_by_digest(record_id).ok_or_else(not_a_request)?;
    let header = book.header_of(&record).ok_or_else(not_a_request)?;
    if header.get("record_type").and_then(Value::as_str)
        != Some(crate::record::record_types::REQUEST)
    {
        return Err(not_a_request());
    }
    let committed_at = header
        .get("committed_at")
        .and_then(Value::as_str)
        .ok_or_else(|| {
            AskError::Corrupt(format!("request record {record_id} has no committed_at"))
        })?
        .to_string();
    let request = header
        .get("statement")
        .and_then(|s| s.get("request"))
        .ok_or_else(|| AskError::Corrupt(format!("request record {record_id} holds no request")))?;
    let bytes = evidence_request::jcs::to_string(request)
        .map_err(|e| AskError::Corrupt(format!("request record {record_id}: {e}")))?
        .into_bytes();
    parse_request(&bytes).map_err(|why| {
        AskError::Corrupt(format!(
            "request record {record_id} holds no well-formed request: {why}"
        ))
    })?;
    let text = |name: &str| {
        request
            .get(name)
            .and_then(Value::as_str)
            .map(str::to_string)
    };
    Ok(Sent {
        digest: sha256_hex(&bytes),
        committed_at,
        deadline: text("deadline"),
        route: text("route"),
        nonce: text("nonce"),
    })
}

/// The requester's own record that neither an envelope nor a refusal arrived
/// in its waiting window (`recorded_absence`; request draft §4.4, profile
/// §6.4).
fn outcome_recorded(book: &dyn Book, request_record_id: &str) -> bool {
    (0..book.len()).any(|leaf| {
        let Some(record) = book.record_at(leaf) else {
            return false;
        };
        let Some(header) = book.header_of(&record) else {
            return false;
        };
        let kind = header.get("record_type").and_then(Value::as_str);
        matches!(
            kind,
            Some(crate::record::record_types::RESPONSE)
                | Some(crate::record::record_types::RECORDED_ABSENCE)
        ) && header
            .get("links")
            .and_then(Value::as_array)
            .is_some_and(|links| {
                links.iter().any(|l| {
                    l.get("type").and_then(Value::as_str) == Some(LinkType::Cites.as_str())
                        && l.get("target").and_then(Value::as_str) == Some(request_record_id)
                })
            })
    })
}

/// Commit that no reply arrived for the recorded request between its
/// recording and `window_end`. The request digest, its deadline and the
/// window's opening come from the committed request record, and so does the
/// route when the request named one: a different route is refused. When the
/// request named none, `route` is the route the binding attempted, if any.
/// It refuses while the request is still pending (window not closed, or
/// closing before the request's own deadline) and once any reply has been
/// recorded, so an outcome is never converted into another.
pub fn record_absence(
    book: &mut dyn Book,
    request_record_id: &str,
    window_end: &str,
    route: &str,
) -> Result<Sealed, AskError> {
    let sent = sent_request(book, request_record_id)?;
    let now = book.now();
    let end = parse_utc(window_end).ok_or_else(|| {
        AskError::Invalid(format!(
            "window end {window_end:?} is not an RFC 3339 UTC time"
        ))
    })?;
    let now_t = parse_utc(&now)
        .ok_or_else(|| AskError::Corrupt(format!("the book's clock read {now:?}")))?;
    if end > now_t {
        return Err(AskError::Invalid(
            "the waiting window has not closed; the request is pending".into(),
        ));
    }
    let asked = parse_utc(&sent.committed_at).ok_or_else(|| {
        AskError::Corrupt(format!(
            "committed_at of request {request_record_id}: {:?}",
            sent.committed_at
        ))
    })?;
    if end.seconds < asked.seconds + MINIMUM_ABSENCE_WINDOW_SECONDS
        || (end.seconds == asked.seconds + MINIMUM_ABSENCE_WINDOW_SECONDS
            && end.nanos < asked.nanos)
    {
        return Err(AskError::Invalid(format!(
            "a waiting window must end at least {MINIMUM_ABSENCE_WINDOW_SECONDS}s after the request was recorded"
        )));
    }
    if let Some(deadline) = &sent.deadline {
        let deadline = parse_utc(deadline)
            .ok_or_else(|| AskError::Corrupt(format!("committed deadline {deadline:?}")))?;
        if end < deadline {
            return Err(AskError::Invalid(
                "the window closes before the request's own deadline".into(),
            ));
        }
    }
    let route = match &sent.route {
        Some(committed) => {
            if !route.is_empty() && route != committed {
                return Err(AskError::Invalid(format!(
                    "the request named route {committed:?}, not {route:?}"
                )));
            }
            committed.clone()
        }
        None => route.to_string(),
    };
    if outcome_recorded(book, request_record_id) {
        return Err(AskError::Invalid(format!(
            "an outcome is already recorded for request {}",
            sent.digest
        )));
    }
    let mut statement = Map::new();
    statement.insert(
        "outcome".into(),
        json!(crate::record::record_types::RECORDED_ABSENCE),
    );
    statement.insert("request_digest".into(), json!(sent.digest));
    if !route.is_empty() {
        statement.insert("route".into(), json!(route));
    }
    statement.insert(
        "window".into(),
        json!({"opened": profile_time(&sent.committed_at), "closed": profile_time(window_end)}),
    );
    statement.insert("recorded_at".into(), json!(profile_time(&now)));
    Ok(book.seal(&Entry {
        record_type: crate::record::record_types::RECORDED_ABSENCE.into(),
        epistemic_type: EpistemicType::ObservedEvent,
        links: vec![Link {
            link_type: LinkType::Cites,
            target: request_record_id.to_string(),
        }],
        statement: Value::Object(statement),
    })?)
}

/// A time in the profile's form: UTC, any fraction dropped (profile §5.2).
pub(crate) fn profile_time(timestamp: &str) -> String {
    match timestamp.split_once('.') {
        Some((seconds, _)) => format!("{seconds}Z"),
        None => timestamp.to_string(),
    }
}
