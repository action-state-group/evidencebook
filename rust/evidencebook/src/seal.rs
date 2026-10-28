//! Sealing a record: the header a book commits, the record capsule that
//! commits it, and that capsule's Producer Envelope.
//!
//! I-D section: *Record header* (anchor `record`). The capsule is the one the
//! Go book seals for each record through capsule-emit's `emit.Seal`
//! (evidencebook Go `book.go`, `seal`): an Agent Action Capsule (format 4,
//! `draft-mih-scitt-agent-action-capsule-05`) of action type `fyi` whose
//! compute attestation's `agent_input_digest` is the SHA-256 of the header's
//! JCS, with each link also a `references[]` entry. Its
//! `capsule_id` is the record id the log commits. The Producer Envelope is a
//! COSE_Sign1 over the raw 32-byte id, its protected header in capsule-emit's
//! order (content type, kid, alg). For the same header, operator, developer
//! and key, [`seal_record`] returns the bytes Go's book stores, which the
//! personal-history and interop-profile runs check byte for byte. A Go
//! header that carries field commitments (`field_commitments`, `commit_alg`)
//! has no [`Header`] here, so such records are not sealed by this module.
//!
//! The crate owns no record store. A caller's [`crate::ask::Book::seal`]
//! assigns the position and commit time, builds the header with [`header`],
//! seals it here, stores what [`SealedRecord`] holds and appends
//! [`SealedRecord::record_id`] to its log. A store that serves
//! [`SealedRecord::body`] as the record's octets keeps
//! [`crate::store::RecordSource::entry_of`]'s default: the SHA-256 of the
//! body is the record id.

use crate::ask::Entry;
use crate::canonical::{self, JcsError};
use crate::ed25519::{self, Sign1};
use crate::record::{Header, RecordError, HEADER_VERSION};
use crate::substrate::Signer;
use ciborium::Value as Cbor;
use evidence_request::time::parse_utc;
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

/// The `spec_version` of every record capsule (capsule-emit Go `SpecVersion`).
pub const SPEC_VERSION: &str = "draft-mih-scitt-agent-action-capsule-05";
/// The `format_version` of every record capsule (capsule-emit Go `FormatVersion`).
pub const FORMAT_VERSION: &str = "4";
/// The Producer Envelope's payload media type: the raw capsule id.
pub const CAPSULE_ID_MEDIA_TYPE: &str = "application/agent-action-capsule-id";
/// The capsule member that holds the compute attestation.
const ATTESTATION: &str = "model_attestation"; // vocabulary-gate: AAC capsule member

/// The `developer` a Go book writes when none is configured.
pub const DEFAULT_DEVELOPER: &str = "evidencebook";

/// Who seals a book's records: the capsule's `operator` and `developer`, and
/// the key whose Producer Envelope signs each one.
pub struct Producer<'a> {
    pub operator: &'a str,
    pub developer: &'a str,
    pub signer: &'a dyn Signer,
}

/// A sealed record: everything a book stores for it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SealedRecord {
    /// The record id: the capsule's `capsule_id`, 64 lowercase hex.
    pub record_id: String,
    /// The header's JCS, which the capsule commits.
    pub header: Vec<u8>,
    /// The capsule's JCS, `capsule_id` included: Go's stored capsule.
    pub capsule: Vec<u8>,
    /// The record's octets as the log commits them: the capsule's JCS
    /// without `capsule_id` (and without the local-only producer fields,
    /// which a record capsule never has). Its SHA-256 is the record id.
    pub body: Vec<u8>,
    /// The Producer Envelope (tagged COSE_Sign1) over the raw record id.
    pub envelope: Vec<u8>,
}

/// Why a record could not be sealed, or does not verify as sealed.
#[derive(Debug, thiserror::Error)]
pub enum SealRecordError {
    /// The header or producer is not one a book seals.
    #[error("invalid: {0}")]
    Invalid(String),
    /// The header breaks a record rule.
    #[error(transparent)]
    Record(#[from] RecordError),
    /// The header does not canonicalize.
    #[error("header: {0}")]
    Canonical(#[from] JcsError),
    /// The signer failed.
    #[error(transparent)]
    Sign(#[from] crate::substrate::SignError),
    /// A stored capsule or envelope is not the seal of this record.
    #[error("not sealed: {0}")]
    Mismatch(String),
}

fn invalid(why: impl Into<String>) -> SealRecordError {
    SealRecordError::Invalid(why.into())
}

/// The header of the record at `seq` committed at `committed_at`, for an
/// entry a requester or responder asks a book to seal (Go
/// `commitEntryLocked`). `committed_at` is an RFC 3339 UTC time; it is
/// truncated to the microsecond and written in Go's `time.RFC3339Nano` form,
/// as the Go book writes it. A null statement is left out.
///
/// The members [`Entry`] does not carry (`event_time_claim`, the refs,
/// `correlation`, payload commitments) are the caller's to set on the
/// returned header before sealing it. Write `event_time_claim` with
/// [`rfc3339_nano`], and leave an empty ref or correlation value unset:
/// [`seal_record`] refuses anything else, since Go's header would differ.
pub fn header(
    book_id: &str,
    seq: u64,
    committed_at: &str,
    entry: &Entry,
) -> Result<Header, SealRecordError> {
    let committed_at = go_time(committed_at, 6).ok_or_else(|| {
        invalid(format!(
            "committed_at {committed_at:?} is not an RFC 3339 UTC time"
        ))
    })?;
    Ok(Header {
        version: HEADER_VERSION,
        book_id: book_id.to_string(),
        seq,
        record_type: entry.record_type.clone(),
        epistemic_type: entry.epistemic_type,
        committed_at,
        event_time_claim: None,
        payload_commitments: Vec::new(),
        retention_state: None,
        links: entry.links.clone(),
        subject_ref: None,
        principal_ref: None,
        counterparty_ref: None,
        correlation: Default::default(),
        statement: (!entry.statement.is_null()).then(|| entry.statement.clone()),
    })
}

/// An RFC 3339 UTC time in Go's `time.RFC3339Nano` form, which the Go book
/// writes for `event_time_claim`: the fraction with trailing zeros dropped,
/// and none when it is zero. `None` when `text` is not
/// `YYYY-MM-DDTHH:MM:SS[.fraction]Z`.
pub fn rfc3339_nano(text: &str) -> Option<String> {
    go_time(text, 9)
}

/// `text` with its fraction cut to `digits` and written as RFC3339Nano.
fn go_time(text: &str, digits: usize) -> Option<String> {
    parse_utc(text)?;
    let (seconds, fraction) = split_fraction(text);
    let kept = &fraction[..fraction.len().min(digits)];
    let kept = kept.trim_end_matches('0');
    Some(if kept.is_empty() {
        format!("{seconds}Z")
    } else {
        format!("{seconds}.{kept}Z")
    })
}

/// `YYYY-MM-DDTHH:MM:SS` and the fraction's digits of a parsed UTC time.
fn split_fraction(text: &str) -> (&str, &str) {
    let seconds = &text[..19];
    let fraction = text[19..]
        .strip_prefix('.')
        .map_or("", |f| f.trim_end_matches('Z'));
    (seconds, fraction)
}

/// capsule-emit's timestamp form of a header's `committed_at` (Go
/// `formatTimestamp`): whole seconds bare, otherwise six fraction digits.
fn capsule_timestamp(committed_at: &str) -> String {
    let (seconds, fraction) = split_fraction(committed_at);
    if fraction.is_empty() {
        format!("{seconds}Z")
    } else {
        format!("{seconds}.{fraction:0<6}Z")
    }
}

/// The Producer Envelope's protected header for `public_key`, byte for byte
/// as capsule-emit writes it: `{3: content type, 4: kid, 1: EdDSA}`.
fn producer_protected(public_key: &[u8; 32]) -> Vec<u8> {
    let mut out = vec![0xa3, 0x03, 0x78, CAPSULE_ID_MEDIA_TYPE.len() as u8];
    out.extend_from_slice(CAPSULE_ID_MEDIA_TYPE.as_bytes());
    out.extend_from_slice(&[0x04, 0x58, 0x20]);
    out.extend_from_slice(public_key);
    out.extend_from_slice(&[0x01, 0x27]);
    out
}

fn public_key_of(signer: &dyn Signer) -> Result<[u8; 32], SealRecordError> {
    hex::decode(signer.key_id())
        .ok()
        .and_then(|k| k.try_into().ok())
        .ok_or_else(|| invalid("the signer's key id is not a 32-byte Ed25519 public key in hex"))
}

fn sha256_hex(bytes: &[u8]) -> String {
    hex::encode(Sha256::digest(bytes))
}

/// The record's header in JCS, after the checks a Go book makes before
/// sealing: the record rules ([`Header::validate`]), its times in the form
/// Go writes them, and no member Go's header would leave out as empty.
fn canonical_header(header: &Header) -> Result<Vec<u8>, SealRecordError> {
    header.validate()?;
    if go_time(&header.committed_at, 6).as_deref() != Some(header.committed_at.as_str()) {
        return Err(invalid(format!(
            "committed_at {:?} is not a UTC time in microseconds, written as Go writes it",
            header.committed_at
        )));
    }
    if let Some(claim) = &header.event_time_claim {
        if rfc3339_nano(claim).as_deref() != Some(claim.as_str()) {
            return Err(invalid(format!(
                "event_time_claim {claim:?} is not a UTC time written as Go writes it"
            )));
        }
    }
    // Go omits an empty ref or correlation key (`omitempty`); one written
    // here would give the header, and so the record id, other bytes.
    let refs = [
        ("subject_ref", &header.subject_ref),
        ("principal_ref", &header.principal_ref),
        ("counterparty_ref", &header.counterparty_ref),
    ];
    if let Some((name, _)) = refs.iter().find(|(_, r)| r.as_deref() == Some("")) {
        return Err(invalid(format!("{name} is empty; leave it unset")));
    }
    if let Some((key, _)) = header.correlation.iter().find(|(_, v)| v.is_empty()) {
        return Err(invalid(format!(
            "correlation key {key:?} is empty; leave it unset"
        )));
    }
    let value = serde_json::to_value(header).map_err(|e| invalid(e.to_string()))?;
    Ok(canonical::jcs(&value)?)
}

/// The record capsule committing `header` (whose JCS is `header_bytes`):
/// its committed body, its id and its JCS with `capsule_id`.
fn record_capsule(
    header: &Header,
    header_bytes: &[u8],
    operator: &str,
    developer: &str,
) -> Result<(Vec<u8>, String, Vec<u8>), SealRecordError> {
    let mut capsule = Map::new();
    capsule.insert("spec_version".into(), json!(SPEC_VERSION));
    capsule.insert("format_version".into(), json!(FORMAT_VERSION));
    capsule.insert(
        "canonicalization_id".into(),
        json!(canonical::CANONICALIZATION_JCS),
    );
    capsule.insert(
        "action_id".into(),
        json!(format!("{}#{}", header.book_id, header.seq)),
    );
    capsule.insert("action_type".into(), json!("fyi"));
    capsule.insert("operator".into(), json!(operator));
    capsule.insert("developer".into(), json!(developer));
    capsule.insert(
        "timestamp".into(),
        json!(capsule_timestamp(&header.committed_at)),
    );
    capsule.insert(
        "assurance".into(),
        json!({"attestation_mode": "self_attested", "effect_mode": "not_applicable", "ledger_mode": "standalone"}),
    );
    let attestation =
        json!({"compute_attestation": {"agent_input_digest": sha256_hex(header_bytes)}});
    capsule.insert(ATTESTATION.into(), attestation);
    // Each link is also a reference, with no citation_purpose: a link type
    // is an Evidence Layer link type, carried and committed in the header.
    if !header.links.is_empty() {
        let references: Vec<Value> = header
            .links
            .iter()
            .map(|l| json!({"type": "agent-action-capsule", "digest_alg": "SHA-256", "digest": l.target}))
            .collect();
        capsule.insert("references".into(), Value::Array(references));
    }
    let body = canonical::jcs(&Value::Object(capsule.clone()))?;
    let record_id = sha256_hex(&body);
    capsule.insert("capsule_id".into(), json!(record_id));
    let capsule = canonical::jcs(&Value::Object(capsule))?;
    Ok((body, record_id, capsule))
}

/// Seal `header` as a record of `producer`'s book: its record capsule, id
/// and Producer Envelope. The header is checked first: the record rules
/// ([`Header::validate`]), `committed_at` in the form [`header`] writes, any
/// `event_time_claim` in [`rfc3339_nano`]'s, and no empty ref or
/// correlation value (Go would leave it out).
pub fn seal_record(
    header: &Header,
    producer: &Producer<'_>,
) -> Result<SealedRecord, SealRecordError> {
    if producer.operator.trim().is_empty() || producer.developer.trim().is_empty() {
        return Err(invalid("operator and developer must not be empty"));
    }
    let header_bytes = canonical_header(header)?;
    let public_key = public_key_of(producer.signer)?;
    let (body, record_id, capsule) =
        record_capsule(header, &header_bytes, producer.operator, producer.developer)?;

    let protected = producer_protected(&public_key);
    let id = hex::decode(&record_id).expect("a SHA-256 hex digest decodes");
    let to_be_signed = ed25519::to_be_signed(&protected, &id);
    let signature = producer.signer.sign(&to_be_signed)?;
    if !ed25519::verify(&public_key, &to_be_signed, &signature) {
        return Err(invalid(
            "the signer's signature does not verify under its key id",
        ));
    }
    let envelope = ed25519::encode(&Cbor::Tag(
        ed25519::COSE_SIGN1_TAG,
        Box::new(Cbor::Array(vec![
            Cbor::Bytes(protected),
            Cbor::Map(Vec::new()),
            Cbor::Bytes(id),
            Cbor::Bytes(signature.to_vec()),
        ])),
    ));
    Ok(SealedRecord {
        record_id,
        header: header_bytes,
        capsule,
        body,
        envelope,
    })
}

impl SealedRecord {
    /// Check that this is a record [`seal_record`] seals under
    /// `public_key`: the header is a canonical header [`seal_record`]
    /// accepts, the capsule and body are exactly the record capsule that
    /// commits it (with the `operator` and `developer` the capsule names)
    /// and `record_id` is its id, and the envelope is the Producer Envelope
    /// over that id, signed by `public_key` and verified strictly. Any other
    /// capsule fails, including one that also carries its header inline (Go
    /// `AppendCapsule`), which a Go book refuses as committing its header
    /// twice.
    pub fn verify(&self, public_key: &[u8; 32]) -> Result<(), SealRecordError> {
        let mismatch =
            |why: &str| SealRecordError::Mismatch(format!("record {}: {why}", self.record_id));
        let header: Header = serde_json::from_slice(&self.header)
            .map_err(|_| mismatch("the header is not a record header"))?;
        if canonical_header(&header).ok().as_deref() != Some(self.header.as_slice()) {
            return Err(mismatch("the header is not one a book seals, in JCS"));
        }
        let capsule: Value = serde_json::from_slice(&self.capsule)
            .map_err(|_| mismatch("the capsule is not JSON"))?;
        let named = |member: &str| capsule.get(member).and_then(Value::as_str).unwrap_or("");
        let (body, record_id, expected) =
            record_capsule(&header, &self.header, named("operator"), named("developer"))?;
        if expected != self.capsule || body != self.body || record_id != self.record_id {
            return Err(mismatch(
                "the capsule is not the record capsule of this header",
            ));
        }
        let message = Sign1::decode(&self.envelope)
            .ok_or_else(|| mismatch("the envelope is not a COSE_Sign1"))?;
        if message.protected != producer_protected(public_key)
            || !message.unprotected.is_empty()
            || hex::encode(&message.payload) != self.record_id
        {
            return Err(mismatch(
                "the envelope is not the Producer Envelope of this record and key",
            ));
        }
        if !message.verifies_under(public_key) {
            return Err(mismatch("the envelope's signature does not verify"));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn times_are_written_as_go_writes_them() {
        assert_eq!(
            go_time("2026-10-15T00:00:01Z", 6).unwrap(),
            "2026-10-15T00:00:01Z"
        );
        assert_eq!(
            go_time("2026-10-15T00:00:01.000Z", 6).unwrap(),
            "2026-10-15T00:00:01Z"
        );
        assert_eq!(
            go_time("2026-10-15T00:00:01.1234567Z", 6).unwrap(),
            "2026-10-15T00:00:01.123456Z"
        );
        assert_eq!(
            go_time("2026-10-15T00:00:01.120Z", 6).unwrap(),
            "2026-10-15T00:00:01.12Z"
        );
        assert_eq!(
            rfc3339_nano("2026-10-15T00:00:01.123456789Z").unwrap(),
            "2026-10-15T00:00:01.123456789Z"
        );
        assert!(go_time("2026-10-15T00:00:01+00:00", 6).is_none());
        assert!(go_time("2026-13-15T00:00:01Z", 6).is_none());
        assert_eq!(
            capsule_timestamp("2026-10-15T00:00:01Z"),
            "2026-10-15T00:00:01Z"
        );
        assert_eq!(
            capsule_timestamp("2026-10-15T00:00:01.12Z"),
            "2026-10-15T00:00:01.120000Z"
        );
    }
}
