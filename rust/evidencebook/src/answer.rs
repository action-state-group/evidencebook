//! Building the artifact response to an evidence request over the book's
//! Checkpointed Local Log, in the interop profile's form
//! (`evidence-interop/0`, agent-action-capsule `spec/interop-profile-v0.md`
//! §6.2 and §6.2.1).
//!
//! I-D section: *Answering a Request*. The request protocol itself (parsing,
//! resolution, refusals, the envelope and its verification) is the
//! `evidence-request` crate's; this module is the store side: it finds what
//! the subject names under the resolved anchor, builds the canonical
//! artifact, proves it with the log's material, and signs the envelope.
//!
//! The canonical artifact is a function of (subject, resolved anchor,
//! derivation) only, the same for every requester (-00 §5). Its forms are
//! those of the Go implementation (`interop_artifact.go`):
//!
//! - `record`, no derivation: the record's own octets, whose SHA-256 is the
//!   record id, so the envelope's material proves it (profile §6.2.1): the
//!   anchor checkpoint statement, the record's inclusion proof, and, when the
//!   checkpoint extends an earlier one, the consistency proof;
//! - `full_history`, `range`, `correlation`, citing records: RFC 8785 of
//!   `{anchor, evidence_stream, records, subject}`, each record
//!   `{body, digest, leaf_index}` with `body` the record octets in hex;
//! - `checkpoints`: RFC 8785 of `{anchor, checkpoints, evidence_stream,
//!   subject}`, each checkpoint its signed statement in hex;
//! - `history_card/1`: RFC 8785 of `{anchor, derivation, evidence_stream,
//!   history, subject}`, each entry `{checkpoint, consistency_from_previous}`
//!   (-00 §8.1), the consistency proof in hex or null.
//!
//! The profile defines no material for record sets, the checkpoint list or a
//! derivation (P-13), so those envelopes carry none: their records carry
//! their bodies, which hash to their digests.
//!
//! An anchor is a checkpoint with a COSE wire statement
//! ([`CllSubstrate::stated_checkpoints`]), identified by the SHA-256 of that
//! statement, the value a requester pins. [`anchors`] lists them for
//! `evidence_request::resolve`.

use crate::substrate::{CllSubstrate, StatedCheckpoint, SubstrateError};
use evidence_request::cll_material::{
    encode_consistency_proof, encode_inclusion_proof, KIND_CHECKPOINT, KIND_CONSISTENCY,
    KIND_INCLUSION,
};
use evidence_request::envelope::{sign, Anchor, AnchorFreshness, Envelope, EnvelopeParts, Signer};
use evidence_request::request::{Coverage, Derivation, Freshness, Request, Subject};
use evidence_request::resolve::{self, ResolvedAnchor};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

/// The token of the one derivation -00 registers (§8.1).
pub const DERIVATION_HISTORY_CARD: &str = "history_card/1";

/// The most records (or checkpoints) one answer carries, whatever `limit` a
/// caller passes: answer a larger subject with a `policy_declined` refusal.
pub const MAX_RECORDS: u64 = 10_000;

/// One record of the book, as the answer serves it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Record {
    /// 0-based position in the log.
    pub leaf_index: u64,
    /// The record id, 64 lowercase hex: the SHA-256 of `body`, the entry the
    /// log commits.
    pub digest: String,
    /// The record's octets.
    pub body: Vec<u8>,
}

/// The records an answer is built from. The book's record store implements
/// it; the substrate holds only their ids.
pub trait RecordSource {
    /// The record with exactly this id.
    fn record_by_digest(&self, digest: &str) -> Option<Record>;
    /// The record at this leaf index.
    fn record_at(&self, leaf_index: u64) -> Option<Record>;
    /// The ids of the records carrying exactly this correlation identifier.
    fn correlation(&self, id: &str) -> Vec<String>;
    /// The ids of the records citing exactly this interaction half.
    fn citing(&self, half_digest: &str) -> Vec<String>;
}

/// Why no envelope was built. Each is answered with a refusal, not an
/// artifact.
#[derive(Debug, thiserror::Error)]
pub enum AnswerError {
    #[error("the resolved anchor is not one of the stream's stated checkpoints")]
    AnchorNotFound,
    /// Records cite the requester's half, but no stated checkpoint covers
    /// one yet. Answer with a `coverage_unsatisfiable` refusal, as the Go
    /// responder does.
    #[error("no stated checkpoint covers a record citing the half yet")]
    CitingNotAnchored,
    #[error("a record the subject names is not in the log under the anchor")]
    RecordNotFound,
    #[error("the subject resolves to no records under the anchor")]
    NoRecords,
    #[error("a record's octets do not hash to its id")]
    RecordDigest,
    #[error("only the history_card/1 derivation has an answer path")]
    DerivationUnsupported,
    /// The subject covers more records (or checkpoints) than the limit.
    /// Answer with a `policy_declined` refusal.
    #[error("the answer would carry more than the limit of records or checkpoints")]
    OverLimit,
    #[error("the envelope could not be signed: {0}")]
    Sign(#[from] evidence_request::envelope::SignError),
    #[error(transparent)]
    Substrate(#[from] SubstrateError),
}

fn sha256_hex(bytes: &[u8]) -> String {
    hex::encode(Sha256::digest(bytes))
}

/// The SHA-256 of a checkpoint's wire statement: its identity as an anchor.
pub fn anchor_digest(checkpoint: &StatedCheckpoint) -> String {
    sha256_hex(&checkpoint.statement)
}

/// The book's anchors as `evidence_request::resolve` sees them: one per
/// stated checkpoint, sized by the records it covers.
pub fn anchors(substrate: &CllSubstrate) -> Result<Vec<resolve::Anchor>, SubstrateError> {
    substrate
        .stated_checkpoints()?
        .iter()
        .map(|cp| {
            Ok(resolve::Anchor {
                digest: anchor_digest(cp),
                size: cp.checkpoint.leaf_count()?,
                issued_at: cp.checkpoint.timestamp.clone(),
            })
        })
        .collect()
}

/// Build and sign the envelope answering `request` (received as
/// `request_bytes`) under `anchor`, as `evidence_request::resolve::resolve`
/// resolved it. For a citing-records subject pinned by the requester's own half,
/// the records are proven under the earliest checkpoint covering every
/// checkpointed record citing the half, never simply the latest, so growth
/// that adds no citing record does not change the artifact (-00 §5). Its
/// envelope names the pin, the half, as its anchor (profile §7.1
/// `anchor_match`); the artifact names the serving checkpoint. When no
/// stated checkpoint covers a citing record yet,
/// [`AnswerError::CitingNotAnchored`].
///
/// `limit` caps the records (or checkpoints) the answer carries, clamped to
/// [`MAX_RECORDS`]; over it, [`AnswerError::OverLimit`]. It counts the
/// distinct records under the anchor, so growth past the anchor never makes
/// a pinned answer unavailable.
#[allow(clippy::too_many_arguments)]
pub fn answer(
    substrate: &CllSubstrate,
    records: &dyn RecordSource,
    request: &Request,
    request_bytes: &[u8],
    anchor: &ResolvedAnchor,
    limit: u64,
    signer: &dyn Signer,
    issued_at: &str,
) -> Result<Envelope, AnswerError> {
    let checkpoints = substrate.stated_checkpoints()?;
    let anchor_index = match anchor {
        ResolvedAnchor::Anchor(a) => checkpoints
            .iter()
            .position(|cp| anchor_digest(cp) == a.digest),
        ResolvedAnchor::ExchangeHalf(half) => Some(
            citing_anchor(records, &checkpoints, half)?.ok_or(AnswerError::CitingNotAnchored)?,
        ),
    }
    .ok_or(AnswerError::AnchorNotFound)?;
    let at = &checkpoints[anchor_index];
    let covered = at.checkpoint.leaf_count()?;
    let limit = limit.min(MAX_RECORDS);
    let chain = &checkpoints[..=anchor_index];

    let mut head = Map::new();
    head.insert("anchor".into(), json!(anchor_digest(at)));
    head.insert("evidence_stream".into(), json!(at.checkpoint.log_id));
    head.insert(
        "subject".into(),
        evidence_request::envelope::subject_value(&request.subject),
    );
    let mut material: Vec<(String, Vec<u8>)> = Vec::new();

    let artifact = match (&request.derivation, &request.subject) {
        (Some(Derivation::Token(t)), _) if t == DERIVATION_HISTORY_CARD => {
            if chain.len() as u64 > limit {
                return Err(AnswerError::OverLimit);
            }
            let mut history = Vec::with_capacity(chain.len());
            for (i, cp) in chain.iter().enumerate() {
                let consistency = match i.checked_sub(1).map(|p| &chain[p]) {
                    Some(prev) if prev.checkpoint.mmr_size < cp.checkpoint.mmr_size => {
                        json!(hex::encode(consistency(
                            substrate,
                            prev.checkpoint.mmr_size,
                            cp.checkpoint.mmr_size
                        )?))
                    }
                    _ => Value::Null,
                };
                history.push(json!({
                    "checkpoint": hex::encode(&cp.statement),
                    "consistency_from_previous": consistency,
                }));
            }
            head.insert("derivation".into(), json!(DERIVATION_HISTORY_CARD));
            head.insert("history".into(), Value::Array(history));
            jcs(head)
        }
        (Some(_), _) => return Err(AnswerError::DerivationUnsupported),
        (None, Subject::Checkpoints) => {
            if chain.len() as u64 > limit {
                return Err(AnswerError::OverLimit);
            }
            let list = chain
                .iter()
                .map(|cp| json!(hex::encode(&cp.statement)))
                .collect();
            head.insert("checkpoints".into(), Value::Array(list));
            jcs(head)
        }
        (None, Subject::Record(_)) if limit == 0 => return Err(AnswerError::OverLimit),
        (None, Subject::Record(d)) => {
            let record = records
                .record_by_digest(d)
                .filter(|r| r.leaf_index < covered && r.digest == *d)
                .ok_or(AnswerError::RecordNotFound)?;
            if sha256_hex(&record.body) != record.digest {
                return Err(AnswerError::RecordDigest);
            }
            let path = substrate.inclusion_path(record.leaf_index, at.checkpoint.mmr_size)?;
            material.push((KIND_CHECKPOINT.into(), at.statement.clone()));
            let inclusion = encode_inclusion_proof(record.leaf_index, &path).ok_or_else(|| {
                AnswerError::Substrate(SubstrateError::Mmr(format!(
                    "leaf {} has no inclusion proof the profile can carry",
                    record.leaf_index
                )))
            })?;
            material.push((KIND_INCLUSION.into(), inclusion));
            if at.checkpoint.prev_size > 0 {
                material.push((
                    KIND_CONSISTENCY.into(),
                    consistency(substrate, at.checkpoint.prev_size, at.checkpoint.mmr_size)?,
                ));
            }
            record.body
        }
        (None, Subject::Range(a, b)) => {
            head.insert(
                "records".into(),
                records_value(&range_records(records, *a, *b, covered, limit)?)?,
            );
            jcs(head)
        }
        (None, Subject::FullHistory) => {
            if covered == 0 {
                return Err(AnswerError::NoRecords);
            }
            head.insert(
                "records".into(),
                records_value(&range_records(records, 0, covered - 1, covered, limit)?)?,
            );
            jcs(head)
        }
        (None, Subject::Correlation(id)) => {
            head.insert(
                "records".into(),
                records_value(&records_under(
                    records,
                    records.correlation(id),
                    covered,
                    limit,
                )?)?,
            );
            jcs(head)
        }
        // The one form left: records citing the requester's half.
        (None, _) => {
            let half = citing_half(&request.subject).expect("every other form is matched above");
            head.insert(
                "records".into(),
                records_value(&records_under(
                    records,
                    records.citing(half),
                    covered,
                    limit,
                )?)?,
            );
            jcs(head)
        }
    };

    let freshness = match &request.coverage {
        Coverage::MinFreshness(Freshness::Size(_)) => {
            Some(AnchorFreshness::Size(at.checkpoint.mmr_size))
        }
        Coverage::MinFreshness(Freshness::Time(_)) => Some(AnchorFreshness::Time(profile_time(
            &at.checkpoint.timestamp,
        ))),
        Coverage::ExpectedPin(_) => None,
    };
    Ok(sign(
        request_bytes,
        EnvelopeParts {
            subject: request.subject.clone(),
            derivation: request.derivation.clone(),
            // Under a half pin the envelope names the pin, the requester's
            // own half (profile §7.1 anchor_match), as the Go responder does;
            // the artifact's `anchor` member names the serving checkpoint.
            anchor: Anchor {
                digest: anchor.digest().to_string(),
                freshness,
            },
            artifact,
            verification: material,
            issued_at: issued_at.to_string(),
        },
        signer,
    )?)
}

/// A checkpoint time in the profile's form: UTC, any fraction dropped
/// (profile §5.2). The substrate writes `YYYY-MM-DDTHH:MM:SS[.f]Z`.
fn profile_time(timestamp: &str) -> String {
    match timestamp.split_once('.') {
        Some((seconds, _)) => format!("{seconds}Z"),
        None => timestamp.to_string(),
    }
}

fn jcs(object: Map<String, Value>) -> Vec<u8> {
    evidence_request::jcs::to_string(&Value::Object(object))
        .expect("an artifact holds only text, maps, arrays and integers")
        .into_bytes()
}

fn consistency(substrate: &CllSubstrate, from: u64, to: u64) -> Result<Vec<u8>, AnswerError> {
    let (old_peaks, paths, new_peaks) = substrate.consistency_parts(from, to)?;
    encode_consistency_proof(from, to, &old_peaks, &paths, &new_peaks).ok_or_else(|| {
        AnswerError::Substrate(SubstrateError::Mmr(format!(
            "the log's consistency proof from {from} to {to} does not reach its own peaks"
        )))
    })
}

fn records_value(records: &[Record]) -> Result<Value, AnswerError> {
    for r in records {
        if sha256_hex(&r.body) != r.digest {
            return Err(AnswerError::RecordDigest);
        }
    }
    Ok(Value::Array(
        records
            .iter()
            .map(|r| json!({"body": hex::encode(&r.body), "digest": r.digest, "leaf_index": r.leaf_index}))
            .collect(),
    ))
}

/// The half a citing-records subject names (the request draft's subject form
/// for records that cite one interaction half).
#[rustfmt::skip]
fn citing_half(subject: &Subject) -> Option<&str> {
    match subject { Subject::Exchange(half) => Some(half), _ => None } // the "exchange" subject form; vocabulary-gate: request-draft wire token
}

/// The anchor for a citing-records subject pinned by the requester's own half: the
/// earliest checkpoint covering the last record citing `half` among those any
/// checkpoint covers. A citing record no checkpoint covers yet cannot be
/// proven, so it does not move the anchor. `None` when no checkpointed record
/// cites the half.
fn citing_anchor(
    records: &dyn RecordSource,
    checkpoints: &[StatedCheckpoint],
    half: &str,
) -> Result<Option<usize>, SubstrateError> {
    let Some(latest) = checkpoints.last() else {
        return Ok(None);
    };
    let latest = latest.checkpoint.leaf_count()?;
    let Some(last_citing) = records
        .citing(half)
        .iter()
        .filter_map(|d| records.record_by_digest(d))
        .map(|r| r.leaf_index)
        .filter(|&i| i < latest)
        .max()
    else {
        return Ok(None);
    };
    for (i, cp) in checkpoints.iter().enumerate() {
        if cp.checkpoint.leaf_count()? > last_citing {
            return Ok(Some(i));
        }
    }
    Ok(None)
}

fn range_records(
    records: &dyn RecordSource,
    a: u64,
    b: u64,
    covered: u64,
    limit: u64,
) -> Result<Vec<Record>, AnswerError> {
    if b >= covered || a > b {
        return Err(AnswerError::RecordNotFound);
    }
    // b - a + 1 > limit, without overflow.
    if b - a >= limit {
        return Err(AnswerError::OverLimit);
    }
    (a..=b)
        .map(|i| {
            records
                .record_at(i)
                .filter(|r| r.leaf_index == i)
                .ok_or(AnswerError::RecordNotFound)
        })
        .collect()
}

fn records_under(
    records: &dyn RecordSource,
    digests: Vec<String>,
    covered: u64,
    limit: u64,
) -> Result<Vec<Record>, AnswerError> {
    let mut found: Vec<Record> = digests
        .iter()
        .filter_map(|d| records.record_by_digest(d))
        .filter(|r| r.leaf_index < covered)
        .collect();
    found.sort_by_key(|r| r.leaf_index);
    found.dedup_by_key(|r| r.leaf_index);
    if found.is_empty() {
        return Err(AnswerError::NoRecords);
    }
    // The limit counts the distinct records under the anchor, as the Go
    // responder counts its resolved leaves: growth past the anchor never
    // makes a pinned answer unavailable.
    if found.len() as u64 > limit {
        return Err(AnswerError::OverLimit);
    }
    Ok(found)
}
