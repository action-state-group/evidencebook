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
//! derivation (P-13), so by default those envelopes carry none: their records
//! carry their bodies, which hash to their digests. A requester that checks
//! more than the profile asks for can be sent more with
//! [`Options::prove_beyond_profile`]: the anchor checkpoint statement for a
//! record set and a profile derivation, plus one inclusion proof per record
//! of a set in the artifact's order. A profile verifier reports those
//! subjects `NOT_CHECKED` before it reads any material (§6.2.1 step 1), so
//! the extra items change nothing for it.
//!
//! An anchor is a checkpoint with a COSE wire statement
//! ([`CheckpointedLog::stated_checkpoints`]), identified by the SHA-256 of
//! that statement, the value a requester pins. [`anchors`] lists them for
//! `evidence_request::resolve`.
//!
//! **Two steps.** [`select`] finds the serving checkpoint and the records the
//! artifact carries; [`build`] proves and signs that selection. [`answer`] is
//! both. A responder that applies its own policy to the records first (who
//! may have them, its own limits) selects, decides, then builds.
//!
//! Everything is read through the store traits ([`CheckpointedLog`] and
//! [`RecordSource`]), so a caller whose checkpointer owns the log files
//! answers from its own index; [`crate::substrate::CllSubstrate`] is the
//! reference [`CheckpointedLog`].

use crate::store::CheckpointedLog;
pub use crate::store::{Record, RecordSource};
use crate::substrate::{StatedCheckpoint, SubstrateError};
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
    #[error("a record's octets do not recompute its committed entry")]
    RecordDigest,
    #[error("the derivation has no answer path")]
    DerivationUnsupported,
    /// The subject covers more records (or checkpoints) than the limit.
    /// Answer with a `policy_declined` refusal.
    #[error("the answer would carry more than the limit of records or checkpoints")]
    OverLimit,
    /// The artifact has no RFC 8785 form (a derived value holds a fraction).
    #[error("the artifact cannot be canonicalized: {0}")]
    Artifact(String),
    #[error("the envelope could not be signed: {0}")]
    Sign(#[from] evidence_request::envelope::SignError),
    #[error(transparent)]
    Substrate(#[from] SubstrateError),
}

/// A derivation the calling profile answers beyond `history_card/1`: given
/// the derivation token, the subject and the serving checkpoint, the
/// artifact member that carries the derived value and the value, or `None`
/// when the profile has no answer for that token and subject. The artifact
/// is RFC 8785 of `{anchor, derivation, evidence_stream, <member>, subject}`.
pub type Derive<'a> = &'a dyn Fn(&str, &Subject, &StatedCheckpoint) -> Option<(String, Value)>;

/// How [`build`] proves and what it answers beyond the profile. The default
/// is the profile alone, as the Go responder answers.
#[derive(Clone, Copy, Default)]
pub struct Options<'a> {
    /// Also send material for what the profile defines none for (P-13): the
    /// anchor checkpoint statement for a record set and a [`Derive`]d
    /// artifact, and one inclusion proof per record of a set, in the
    /// artifact's order.
    pub prove_beyond_profile: bool,
    /// The calling profile's own derivations.
    pub derive: Option<Derive<'a>>,
}

/// What an answer is served under and carries: the position of the serving
/// checkpoint in [`CheckpointedLog::stated_checkpoints`], and the leaf
/// indices of the records the artifact carries, ascending and distinct, all
/// under that checkpoint (empty for a checkpoint list or a derivation).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Selection {
    pub at: usize,
    pub records: Vec<u64>,
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
pub fn anchors(log: &dyn CheckpointedLog) -> Result<Vec<resolve::Anchor>, SubstrateError> {
    log.stated_checkpoints()?
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
/// resolved it: [`select`], then [`build`] under the profile alone. For a
/// citing-records subject pinned by the requester's own half, the records
/// are proven under the earliest checkpoint covering every checkpointed
/// record citing the half, never simply the latest, so growth that adds no
/// citing record does not change the artifact (-00 §5). Its envelope names
/// the pin, the half, as its anchor (profile §7.1 `anchor_match`); the
/// artifact names the serving checkpoint. When no stated checkpoint covers a
/// citing record yet, [`AnswerError::CitingNotAnchored`].
///
/// `limit` caps the records (or checkpoints) the answer carries, clamped to
/// [`MAX_RECORDS`]; over it, [`AnswerError::OverLimit`]. It counts the
/// distinct records under the anchor, so growth past the anchor never makes
/// a pinned answer unavailable.
#[allow(clippy::too_many_arguments)]
pub fn answer(
    log: &dyn CheckpointedLog,
    records: &dyn RecordSource,
    request: &Request,
    request_bytes: &[u8],
    anchor: &ResolvedAnchor,
    limit: u64,
    signer: &dyn Signer,
    issued_at: &str,
) -> Result<Envelope, AnswerError> {
    let selection = select(log, records, request, anchor, limit)?;
    build(
        log,
        records,
        request,
        request_bytes,
        anchor,
        &selection,
        Options::default(),
        signer,
        issued_at,
    )
}

/// The position of the checkpoint an answer to `anchor` is served under, in
/// `checkpoints` (the store's stated checkpoints). For an anchor, the
/// checkpoint it names ([`AnswerError::AnchorNotFound`] when none). For a
/// citing-records subject pinned by the requester's own half, the earliest
/// checkpoint covering the last record citing the half among those any
/// checkpoint covers: a citing record no checkpoint covers yet cannot be
/// proven, so it does not move the anchor ([`AnswerError::CitingNotAnchored`]
/// when no checkpointed record cites the half).
pub fn serving_checkpoint(
    records: &dyn RecordSource,
    checkpoints: &[StatedCheckpoint],
    anchor: &ResolvedAnchor,
) -> Result<usize, AnswerError> {
    match anchor {
        ResolvedAnchor::Anchor(a) => checkpoints
            .iter()
            .position(|cp| anchor_digest(cp) == a.digest)
            .ok_or(AnswerError::AnchorNotFound),
        ResolvedAnchor::ExchangeHalf(half) => {
            citing_anchor(records, checkpoints, half)?.ok_or(AnswerError::CitingNotAnchored)
        }
    }
}

/// The serving checkpoint and the records an answer to `request` under
/// `anchor` carries, checked against `limit` (clamped to [`MAX_RECORDS`]) as
/// [`answer`] describes. A derivation selects no records; whether it has an
/// answer path is [`build`]'s to say.
pub fn select(
    log: &dyn CheckpointedLog,
    records: &dyn RecordSource,
    request: &Request,
    anchor: &ResolvedAnchor,
    limit: u64,
) -> Result<Selection, AnswerError> {
    let checkpoints = log.stated_checkpoints()?;
    let at = serving_checkpoint(records, &checkpoints, anchor)?;
    let covered = checkpoints[at].checkpoint.leaf_count()?;
    let limit = limit.min(MAX_RECORDS);
    let listed = at as u64 + 1;

    let leaves = match (&request.derivation, &request.subject) {
        (Some(Derivation::Token(t)), _) if t == DERIVATION_HISTORY_CARD => {
            if listed > limit {
                return Err(AnswerError::OverLimit);
            }
            Vec::new()
        }
        (Some(_), _) => Vec::new(),
        (None, Subject::Checkpoints) => {
            if listed > limit {
                return Err(AnswerError::OverLimit);
            }
            Vec::new()
        }
        (None, Subject::Record(_)) if limit == 0 => return Err(AnswerError::OverLimit),
        (None, Subject::Record(d)) => {
            let leaf = records
                .leaf_index_of(d)
                .filter(|&i| i < covered)
                .ok_or(AnswerError::RecordNotFound)?;
            vec![leaf]
        }
        (None, Subject::Range(a, b)) => range_leaves(*a, *b, covered, limit)?,
        (None, Subject::FullHistory) => {
            if covered == 0 {
                return Err(AnswerError::NoRecords);
            }
            range_leaves(0, covered - 1, covered, limit)?
        }
        (None, Subject::Correlation(id)) => leaves_under(records.correlation(id), covered, limit)?,
        // The one form left: records citing the requester's half.
        (None, _) => {
            let half = citing_half(&request.subject).expect("every other form is matched above");
            leaves_under(records.citing(half), covered, limit)?
        }
    };
    Ok(Selection {
        at,
        records: leaves,
    })
}

/// Build and sign the envelope for a [`select`]ed answer to `request`
/// (received as `request_bytes`), resolved to `anchor`. Every record is read
/// again and checked against its committed entry
/// ([`RecordSource::entry_of`]); one that does not hold is
/// [`AnswerError::RecordDigest`], never served.
#[allow(clippy::too_many_arguments)]
pub fn build(
    log: &dyn CheckpointedLog,
    records: &dyn RecordSource,
    request: &Request,
    request_bytes: &[u8],
    anchor: &ResolvedAnchor,
    selection: &Selection,
    options: Options<'_>,
    signer: &dyn Signer,
    issued_at: &str,
) -> Result<Envelope, AnswerError> {
    let checkpoints = log.stated_checkpoints()?;
    let at = checkpoints
        .get(selection.at)
        .ok_or(AnswerError::AnchorNotFound)?;
    let chain = &checkpoints[..=selection.at];

    let mut head = Map::new();
    head.insert("anchor".into(), json!(anchor_digest(at)));
    head.insert("evidence_stream".into(), json!(at.checkpoint.log_id));
    head.insert(
        "subject".into(),
        evidence_request::envelope::subject_value(&request.subject),
    );
    let mut material: Vec<(String, Vec<u8>)> = Vec::new();

    let derived = match (&request.derivation, options.derive) {
        (Some(Derivation::Token(t)), Some(derive)) if t != DERIVATION_HISTORY_CARD => {
            derive(t, &request.subject, at).map(|d| (t.clone(), d))
        }
        _ => None,
    };

    let artifact = match (&request.derivation, &request.subject) {
        (Some(Derivation::Token(t)), _) if t == DERIVATION_HISTORY_CARD => {
            let mut history = Vec::with_capacity(chain.len());
            for (i, cp) in chain.iter().enumerate() {
                let consistency = match i.checked_sub(1).map(|p| &chain[p]) {
                    Some(prev) if prev.checkpoint.mmr_size < cp.checkpoint.mmr_size => {
                        json!(hex::encode(consistency(
                            log,
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
            jcs(head)?
        }
        (Some(_), _) => {
            let (token, (member, value)) = derived.ok_or(AnswerError::DerivationUnsupported)?;
            head.insert("derivation".into(), json!(token));
            head.insert(member, value);
            if options.prove_beyond_profile {
                material.push((KIND_CHECKPOINT.into(), at.statement.clone()));
            }
            jcs(head)?
        }
        (None, Subject::Checkpoints) => {
            let list = chain
                .iter()
                .map(|cp| json!(hex::encode(&cp.statement)))
                .collect();
            head.insert("checkpoints".into(), Value::Array(list));
            jcs(head)?
        }
        (None, Subject::Record(d)) => {
            let [leaf] = selection.records[..] else {
                return Err(AnswerError::RecordNotFound);
            };
            let record = record_at(records, leaf)?;
            if record.digest != *d {
                return Err(AnswerError::RecordNotFound);
            }
            let path = log.inclusion_path(record.leaf_index, at.checkpoint.mmr_size)?;
            material.push((KIND_CHECKPOINT.into(), at.statement.clone()));
            material.push((KIND_INCLUSION.into(), inclusion(record.leaf_index, &path)?));
            if at.checkpoint.prev_size > 0 {
                material.push((
                    KIND_CONSISTENCY.into(),
                    consistency(log, at.checkpoint.prev_size, at.checkpoint.mmr_size)?,
                ));
            }
            record.body
        }
        (None, _) => {
            if selection.records.is_empty() {
                return Err(AnswerError::NoRecords);
            }
            let set = selection
                .records
                .iter()
                .map(|&i| record_at(records, i))
                .collect::<Result<Vec<_>, _>>()?;
            if options.prove_beyond_profile {
                material.push((KIND_CHECKPOINT.into(), at.statement.clone()));
                for r in &set {
                    let path = log.inclusion_path(r.leaf_index, at.checkpoint.mmr_size)?;
                    material.push((KIND_INCLUSION.into(), inclusion(r.leaf_index, &path)?));
                }
            }
            head.insert("records".into(), records_value(&set));
            jcs(head)?
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

/// RFC 8785 of an artifact. The crate's own members are text, maps, arrays
/// and integers; a [`Derive`]d value may hold what the profile's JCS refuses
/// (a fraction), which is [`AnswerError::Artifact`], never a panic.
fn jcs(object: Map<String, Value>) -> Result<Vec<u8>, AnswerError> {
    evidence_request::jcs::to_string(&Value::Object(object))
        .map(String::into_bytes)
        .map_err(|e| AnswerError::Artifact(e.to_string()))
}

fn consistency(log: &dyn CheckpointedLog, from: u64, to: u64) -> Result<Vec<u8>, AnswerError> {
    let (old_peaks, paths, new_peaks) = log.consistency_parts(from, to)?;
    encode_consistency_proof(from, to, &old_peaks, &paths, &new_peaks).ok_or_else(|| {
        AnswerError::Substrate(SubstrateError::Mmr(format!(
            "the log's consistency proof from {from} to {to} does not reach its own peaks"
        )))
    })
}

fn inclusion(leaf_index: u64, path: &[[u8; 32]]) -> Result<Vec<u8>, AnswerError> {
    encode_inclusion_proof(leaf_index, path).ok_or_else(|| {
        AnswerError::Substrate(SubstrateError::Mmr(format!(
            "leaf {leaf_index} has no inclusion proof the profile can carry"
        )))
    })
}

/// The record at `leaf_index`, checked against its committed entry.
fn record_at(records: &dyn RecordSource, leaf_index: u64) -> Result<Record, AnswerError> {
    let record = records
        .record_at(leaf_index)
        .filter(|r| r.leaf_index == leaf_index)
        .ok_or(AnswerError::RecordNotFound)?;
    if records.entry_of(&record.body).as_deref() != Some(record.digest.as_str()) {
        return Err(AnswerError::RecordDigest);
    }
    Ok(record)
}

fn records_value(records: &[Record]) -> Value {
    Value::Array(
        records
            .iter()
            .map(|r| json!({"body": hex::encode(&r.body), "digest": r.digest, "leaf_index": r.leaf_index}))
            .collect(),
    )
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
        .into_iter()
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

fn range_leaves(a: u64, b: u64, covered: u64, limit: u64) -> Result<Vec<u64>, AnswerError> {
    if b >= covered || a > b {
        return Err(AnswerError::RecordNotFound);
    }
    // b - a + 1 > limit, without overflow.
    if b - a >= limit {
        return Err(AnswerError::OverLimit);
    }
    Ok((a..=b).collect())
}

fn leaves_under(leaves: Vec<u64>, covered: u64, limit: u64) -> Result<Vec<u64>, AnswerError> {
    let mut found: Vec<u64> = leaves.into_iter().filter(|&i| i < covered).collect();
    found.sort_unstable();
    found.dedup();
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
