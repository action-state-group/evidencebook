//! The requester's check of a holder's answer to a derivation named by the
//! digest of its definition, and the rows it reads back.
//!
//! I-D section: *Answering a Request*. The port of the Go book's
//! `verifySetInclusion` (`request.go`) and the reading half of
//! `Book.DerivedInputs` (`derived_report.go`). A holder that registered the
//! derivation with [`crate::derivation::DerivationDefinition::proofs`]
//! answers with the anchor's `cll-checkpoint` once, one `cll-inclusion-proof`
//! per record in record order and, when the checkpoint extends an earlier
//! one, the `cll-consistency-proof`. The profile defines no such material
//! for a record set (P-13: agent-action-capsule#668); these use the kinds of
//! §6.2.1 as they are.
//!
//! The check proves that the rows are the holder's log's records at the
//! anchor, in the order and at the leaves the answer names. It never proves
//! that no row was left out, or that a field value is the record's: those
//! stay the holder's word.
//!
//! Sealing a report over the rows (Go `Book.ReportFromDerivation`) needs a
//! record store, `SealResult` and a bundle, none of which this crate has; it
//! is not ported.

use crate::answer::{citing_half, profile_time};
use crate::derivation::decode_strict;
use crate::staple::consistency_holds;
use cll::checkpoint::{verify_checkpoint_cose_offline, DecodedCheckpointCose};
use cll::mmr::{self, InclusionProof};
use evidence_request::cll_material::{
    encode_inclusion_proof, KIND_CHECKPOINT, KIND_CONSISTENCY, KIND_INCLUSION,
};
use evidence_request::envelope::{AnchorFreshness, Envelope};
use evidence_request::request::{Coverage, Derivation, Request};
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};

/// Why an answer's rows are not proven.
#[derive(Debug, thiserror::Error, PartialEq, Eq)]
#[error("anchor_inclusion: {0}")]
pub struct SetInclusionError(pub String);

fn bad<T>(why: impl Into<String>) -> Result<T, SetInclusionError> {
    Err(SetInclusionError(why.into()))
}

/// A set of rows proven under an anchor.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SetInclusion {
    /// The anchor's signed checkpoint statement.
    pub statement: Vec<u8>,
    /// The digest of the checkpoint the proofs are under: the artifact's own
    /// `anchor` member, which a request pinning its own half (a citing-records subject) keeps apart
    /// from the envelope's.
    pub anchor: String,
}

/// One row of a holder's answer: the digest and leaf of a record in the
/// holder's log, and the fields the derivation declares, as the holder
/// attested them.
#[derive(Clone, Debug, PartialEq)]
pub struct DerivedRecord {
    pub digest: String,
    pub leaf_index: u64,
    pub fields: Map<String, Value>,
}

/// What a requester holds after a holder answered a request that named a
/// derivation by the digest of its definition.
#[derive(Clone, Debug, PartialEq)]
pub struct DerivedInputs {
    /// The digest of the definition the holder answered, the one the request
    /// named.
    pub derivation: String,
    /// The digest of the checkpoint the holder answered at.
    pub anchor: String,
    /// The answer carried the anchor's checkpoint and a proof for every row,
    /// and [`verify_set_inclusion`] checked them. Never set from the mere
    /// presence of material.
    pub proven: bool,
    pub records: Vec<DerivedRecord>,
}

/// Read the rows of `envelope`, the answer to `request`, which must name a
/// derivation by the digest of its definition. When the envelope carries
/// verification material it is checked ([`verify_set_inclusion`]) and a
/// failure is the error; without any, the rows are read and `proven` is
/// false.
pub fn derived_inputs(
    envelope: &Envelope,
    request: &Request,
    checkpoint_key: &str,
) -> Result<DerivedInputs, SetInclusionError> {
    let Some(Derivation::Digest(id)) = &envelope.derivation else {
        return bad("the answer is not to a derivation named by the digest of its definition");
    };
    let proven = if envelope.verification.is_empty() {
        None
    } else {
        Some(verify_set_inclusion(envelope, request, checkpoint_key)?)
    };
    let (artifact, rows) = read_artifact(envelope)?;
    let anchor = artifact_anchor(&artifact)?;
    let mut records = Vec::with_capacity(rows.len());
    for (i, row) in rows.iter().enumerate() {
        let (digest, leaf_index) = row_leaf(row, i)?;
        let mut fields = row.as_object().cloned().unwrap_or_default();
        fields.remove("digest");
        fields.remove("leaf_index");
        records.push(DerivedRecord {
            digest,
            leaf_index,
            fields,
        });
    }
    Ok(DerivedInputs {
        derivation: id.clone(),
        anchor,
        proven: proven.is_some(),
        records,
    })
}

/// The `anchor_inclusion` procedure of §6.2.1 over a record set: the
/// checkpoint is the anchor and signed under `checkpoint_key`, and each
/// row's digest is the entry at its `leaf_index` under that checkpoint. The
/// artifact's rows and the proofs are the same list in the same order, so a
/// row cannot be added, dropped or moved without a proof that fails.
///
/// The envelope is checked first (its signature, the artifact's digest and
/// each verification item's digest, [`Envelope::verify`]) and must answer
/// `request`'s subject and derivation; material that is not bound to a
/// verified envelope is never read. Whether the envelope's responder is the
/// one expected is [`Envelope::verify_for`]'s question, not this one's.
///
/// The expected anchor is the envelope's, except that a request pinning its
/// own half (a citing-records subject) is answered with the half's digest as the envelope's
/// anchor and the serving checkpoint's as the artifact's `anchor` member:
/// the proofs are then under the artifact's.
pub fn verify_set_inclusion(
    envelope: &Envelope,
    request: &Request,
    checkpoint_key: &str,
) -> Result<SetInclusion, SetInclusionError> {
    envelope
        .verify()
        .map_err(|e| SetInclusionError(format!("the envelope does not verify: {e}")))?;
    if envelope.subject != request.subject || envelope.derivation != request.derivation {
        return bad("the envelope answers a different subject or derivation");
    }
    if !matches!(&request.derivation, Some(Derivation::Digest(_))) {
        return bad("not a derivation named by the digest of its definition");
    }
    let (artifact, rows) = read_artifact(envelope)?;
    let stated = artifact_anchor(&artifact)?;
    let half_pin = matches!((citing_half(&request.subject), &request.coverage),
        (Some(half), Coverage::ExpectedPin(pin)) if pin == half);
    if !half_pin && stated != envelope.anchor.digest {
        return bad("the artifact names another anchor");
    }
    let want = if half_pin {
        stated.as_str()
    } else {
        envelope.anchor.digest.as_str()
    };

    let (mut checkpoint, mut consistency): (Option<&[u8]>, Option<&[u8]>) = (None, None);
    let mut inclusions: Vec<&[u8]> = Vec::new();
    for (entry, item) in envelope
        .verification
        .iter()
        .zip(&envelope.verification_content)
    {
        match entry.kind.as_str() {
            KIND_CHECKPOINT if checkpoint.is_none() => checkpoint = Some(item),
            KIND_CHECKPOINT => return bad("more than one checkpoint"),
            KIND_INCLUSION => inclusions.push(item),
            KIND_CONSISTENCY if consistency.is_none() => consistency = Some(item),
            KIND_CONSISTENCY => return bad("more than one consistency proof"),
            other => {
                return bad(format!(
                    "kind {other:?} is not one this requester implements"
                ))
            }
        }
    }
    let Some(checkpoint) = checkpoint.filter(|_| inclusions.len() == rows.len()) else {
        return bad("the checkpoint is absent or the proofs are not one per record");
    };
    if hex::encode(Sha256::digest(checkpoint)) != want {
        return bad("the checkpoint is not the anchor");
    }
    let cp = read_signed_checkpoint(checkpoint, checkpoint_key)?;
    check_freshness_committed(envelope, &cp)?;
    for (i, (row, proof)) in rows.iter().zip(&inclusions).enumerate() {
        let (digest, leaf) = row_leaf(row, i)?;
        check_inclusion(&cp, &digest, leaf, proof)
            .map_err(|why| SetInclusionError(format!("record {i}: {why}")))?;
    }
    if let Some(proof) = consistency {
        if cp.prev_size == 0 {
            return bad("the checkpoint extends no earlier commitment");
        }
        check_consistency(&cp, proof)?;
    }
    Ok(SetInclusion {
        statement: checkpoint.to_vec(),
        anchor: stated,
    })
}

/// The artifact's members `anchor` and `records`, which must both be there.
fn read_artifact(envelope: &Envelope) -> Result<(Value, Vec<Value>), SetInclusionError> {
    let artifact = decode_strict(&envelope.artifact)
        .map_err(|e| SetInclusionError(format!("the artifact is not JSON: {e}")))?;
    let Some(rows) = artifact.get("records").and_then(Value::as_array).cloned() else {
        return bad("the artifact holds no records list");
    };
    Ok((artifact, rows))
}

fn artifact_anchor(artifact: &Value) -> Result<String, SetInclusionError> {
    match artifact.get("anchor").and_then(Value::as_str) {
        Some(anchor) if !anchor.is_empty() => Ok(anchor.to_owned()),
        _ => bad("the artifact holds no anchor"),
    }
}

fn row_leaf(row: &Value, i: usize) -> Result<(String, u64), SetInclusionError> {
    let digest = row.get("digest").and_then(Value::as_str);
    let leaf = row.get("leaf_index").and_then(Value::as_u64);
    match (digest, leaf) {
        (Some(d), Some(l)) if !d.is_empty() => Ok((d.to_owned(), l)),
        _ => bad(format!("record {i} names no leaf and digest")),
    }
}

/// A checkpoint read from its signed statement: the signature checked
/// strictly, the key required to be the pinned one.
fn read_signed_checkpoint(
    statement: &[u8],
    pinned_key: &str,
) -> Result<DecodedCheckpointCose, SetInclusionError> {
    // The strict signature check comes first (#108): the log crate's own
    // verification is lenient.
    if !crate::ed25519::statement_verifies(statement) {
        return bad("the checkpoint does not verify");
    }
    let verified = verify_checkpoint_cose_offline(statement);
    let cp = match verified.decoded {
        Some(cp) if verified.ok => cp,
        _ => return bad("the checkpoint does not verify"),
    };
    if pinned_key.is_empty() || cp.key_id != pinned_key {
        return bad(format!(
            "the checkpoint is signed by {}, not the pinned key",
            cp.key_id
        ));
    }
    Ok(cp)
}

/// The freshness an envelope states for its anchor must be what the
/// anchor's signed checkpoint commits, so a responder cannot sign a newer
/// size or time than its checkpoint holds.
fn check_freshness_committed(
    envelope: &Envelope,
    cp: &DecodedCheckpointCose,
) -> Result<(), SetInclusionError> {
    let ok = match &envelope.anchor.freshness {
        None => true,
        // The size a responder states is the checkpoint's `mmr_size`, as
        // [`crate::answer::build`] states it.
        Some(AnchorFreshness::Size(n)) => *n == cp.mmr_size,
        Some(AnchorFreshness::Time(t)) => *t == profile_time(&cp.timestamp),
    };
    if ok {
        Ok(())
    } else {
        bad("anchor.freshness is not what the checkpoint commits")
    }
}

fn hash_of(hex_text: &str) -> Option<mmr::Hash> {
    hex::decode(hex_text).ok()?.try_into().ok()
}

/// Step 12: `digest` is the entry at `leaf` under the checkpoint, by the
/// proof octets `[node index, path]`. The octets must be the deterministic
/// encoding of the path for that leaf, which also fixes the index.
fn check_inclusion(
    cp: &DecodedCheckpointCose,
    digest: &str,
    leaf: u64,
    octets: &[u8],
) -> Result<(), String> {
    use ciborium::value::Value as Cbor;
    let entry: mmr::Hash = hash_of(digest).ok_or("the digest is not a 32-octet hash")?;
    let mut rest = octets;
    let Ok(Cbor::Array(parts)) = ciborium::from_reader::<Cbor, _>(&mut rest) else {
        return Err("the inclusion proof is not [index, path]".into());
    };
    let [_, Cbor::Array(items)] = parts.as_slice() else {
        return Err("the inclusion proof is not [index, path]".into());
    };
    if !rest.is_empty() {
        return Err("the inclusion proof is not [index, path]".into());
    }
    let path = items
        .iter()
        .map(|item| match item {
            Cbor::Bytes(b) => mmr::Hash::try_from(b.as_slice()).ok(),
            _ => None,
        })
        .collect::<Option<Vec<mmr::Hash>>>()
        .ok_or("the inclusion path is not a list of hashes")?;
    if encode_inclusion_proof(leaf, &path).as_deref() != Some(octets) {
        return Err(format!(
            "the inclusion proof is not the deterministic proof for leaf {leaf}"
        ));
    }
    let size = cp.mmr_size;
    let peak_positions = mmr::peaks(size).map_err(|e| e.to_string())?;
    let leaf_pos = mmr::leaf_index_to_pos(leaf).map_err(|e| e.to_string())?;
    // The peaks are ordered left to right, so the leaf is under the first
    // peak at or after its position.
    let at = peak_positions
        .iter()
        .position(|&p| p >= leaf_pos)
        .ok_or("the leaf is under no peak")?;
    if cp.new_peak_hashes.len() != peak_positions.len() {
        return Err("the checkpoint's peaks do not fit its size".into());
    }
    let proof = InclusionProof {
        v: 1,
        kind: "inclusion".into(),
        size,
        leaf_index: leaf,
        witness: path.iter().map(hex::encode).collect(),
        peaks_left: cp.new_peak_hashes[..at].iter().map(hex::encode).collect(),
        peaks_right: cp.new_peak_hashes[at + 1..]
            .iter()
            .map(hex::encode)
            .collect(),
    };
    let root = hash_of(&cp.root).ok_or("the checkpoint's root is not a hash")?;
    if mmr::verify_inclusion(&root, size, leaf, &entry, &proof) {
        Ok(())
    } else {
        Err("the path reaches no peak of the checkpoint".into())
    }
}

/// Step 13: the checkpoint's commitment extends its own previous one, by
/// the consistency proof octets.
fn check_consistency(cp: &DecodedCheckpointCose, octets: &[u8]) -> Result<(), SetInclusionError> {
    let prev = DecodedCheckpointCose {
        mmr_size: cp.prev_size,
        root: cp.prev_root.clone(),
        new_peak_hashes: cp.prev_peak_hashes.clone(),
        ..cp.clone()
    };
    if consistency_holds(&prev, cp, octets) {
        Ok(())
    } else {
        bad("the consistency proof does not reach the checkpoint's commitment")
    }
}
