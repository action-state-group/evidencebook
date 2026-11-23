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
use crate::derivation::{decode_strict, decode_syntax_exact};
use crate::staple::consistency_holds;
use cll::checkpoint::{verify_checkpoint_cose_offline, DecodedCheckpointCose};
use cll::mmr::{self, InclusionProof};
use evidence_request::cll_material::{
    encode_consistency_proof, encode_inclusion_proof, KIND_CHECKPOINT, KIND_CONSISTENCY,
    KIND_INCLUSION,
};
use evidence_request::envelope::{AnchorFreshness, Envelope};
use evidence_request::request::{Coverage, Derivation, Freshness, Request};
use evidence_request::time::parse_utc;
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
    let answer = check_answer(envelope, request)?;
    let proven = !envelope.verification.is_empty();
    if proven {
        prove(envelope, &answer, checkpoint_key)?;
    }
    let mut records = Vec::with_capacity(answer.rows.len());
    for (i, row) in answer.rows.iter().enumerate() {
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
        anchor: answer.anchor,
        proven,
        records,
    })
}

/// The most records, and the most verification entries, an answer may carry:
/// the answer item limit a requester applies to what the responder signed
/// (Go `MaxReplyItems`), checked before any proof is read.
pub const MAX_ITEMS: usize = crate::answer::MAX_RECORDS as usize;

/// What an answer's envelope and artifact are once the checks that do not
/// read the verification material have passed.
struct Answer {
    anchor: String,
    rows: Vec<Value>,
    /// The anchor the proofs are under: the envelope's, or for a request
    /// pinning its own half the artifact's.
    want: String,
}

/// The envelope-level checks of profile §7.3 that precede `anchor_inclusion`
/// (Go `verifyEnvelopeContent`), for an answer to `request` that names a
/// derivation by the digest of its definition: the envelope's own signature
/// and digests, its subject and derivation, the item limits, `anchor_match`
/// and the artifact's `anchor` and `records` (a row's `body`, when it has
/// one, must be its record).
fn check_answer(envelope: &Envelope, request: &Request) -> Result<Answer, SetInclusionError> {
    envelope
        .verify()
        .map_err(|e| SetInclusionError(format!("the envelope does not verify: {e}")))?;
    if envelope.subject != request.subject || envelope.derivation != request.derivation {
        return bad("the envelope answers a different subject or derivation");
    }
    if !matches!(&request.derivation, Some(Derivation::Digest(_))) {
        return bad("not a derivation named by the digest of its definition");
    }
    if envelope.verification.len() > MAX_ITEMS {
        return bad(format!(
            "limit: verification: {} over {MAX_ITEMS}",
            envelope.verification.len()
        ));
    }
    check_anchor_match(envelope, request)?;
    // Numbers are read as written: a field's fraction is the holder's value,
    // not a refusal, as Go's artifact reader reads it.
    let artifact = decode_syntax_exact(&envelope.artifact)
        .map_err(|e| SetInclusionError(format!("the artifact is not JSON: {e}")))?;
    let Some(rows) = artifact.get("records").and_then(Value::as_array).cloned() else {
        return bad("the artifact holds no records list");
    };
    if rows.len() > MAX_ITEMS {
        return bad(format!("limit: records: {} over {MAX_ITEMS}", rows.len()));
    }
    let stated = artifact_anchor(&artifact)?;
    let half_pin = matches!((citing_half(&request.subject), &request.coverage),
        (Some(half), Coverage::ExpectedPin(pin)) if pin == half);
    if !half_pin && stated != envelope.anchor.digest {
        return bad("the artifact names another anchor");
    }
    for (i, row) in rows.iter().enumerate() {
        let digest = row
            .get("digest")
            .and_then(Value::as_str)
            .unwrap_or_default();
        if let Some(body) = row.get("body") {
            let matches = body
                .as_str()
                .and_then(|text| hex::decode(text).ok())
                .is_some_and(|raw| record_body_matches(&raw, digest));
            if !matches {
                return bad(format!("record {i} does not hash to its digest"));
            }
        }
    }
    let want = if half_pin {
        stated.clone()
    } else {
        envelope.anchor.digest.clone()
    };
    Ok(Answer {
        anchor: stated,
        rows,
        want,
    })
}

/// `anchor_match` (profile §7.1): the envelope's anchor is the pinned one,
/// or states a size or time meeting the floor.
fn check_anchor_match(envelope: &Envelope, request: &Request) -> Result<(), SetInclusionError> {
    match &request.coverage {
        Coverage::ExpectedPin(pin) if envelope.anchor.digest != *pin => {
            bad("anchor_match: the anchor is not the pinned one")
        }
        Coverage::ExpectedPin(_) => Ok(()),
        Coverage::MinFreshness(Freshness::Size(floor)) => match &envelope.anchor.freshness {
            Some(AnchorFreshness::Size(n)) if n >= floor => Ok(()),
            _ => bad("anchor_match: the anchor does not state a size meeting the floor"),
        },
        Coverage::MinFreshness(Freshness::Time(floor)) => match &envelope.anchor.freshness {
            Some(AnchorFreshness::Time(t)) if parse_utc(t).is_some_and(|t| t >= *floor) => Ok(()),
            _ => bad("anchor_match: the anchor does not state a time meeting the floor"),
        },
    }
}

/// Whether a row's body is the record its digest names: its SHA-256, or a
/// capsule as a ledger holds it (carrying that id as `capsule_id`, with the
/// signature and key id that are no part of it), whose preimage hashes to it
/// (Go `recordBodyMatches`).
fn record_body_matches(body: &[u8], digest: &str) -> bool {
    if hex::encode(Sha256::digest(body)) == digest {
        return true;
    }
    // A capsule's numbers have a canonical form or the capsule has no id.
    let Ok(Value::Object(mut capsule)) = decode_strict(body) else {
        return false;
    };
    if capsule.get("capsule_id").and_then(Value::as_str) != Some(digest) {
        return false;
    }
    capsule.retain(|k, _| {
        k != "capsule_id" && !agent_action_capsule::canonical::is_local_only_field(k)
    });
    crate::canonical::jcs(&Value::Object(capsule))
        .is_ok_and(|preimage| hex::encode(Sha256::digest(preimage)) == digest)
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
    let answer = check_answer(envelope, request)?;
    prove(envelope, &answer, checkpoint_key)
}

/// The material of `envelope` against `answer`'s rows ([`verify_set_inclusion`]).
fn prove(
    envelope: &Envelope,
    answer: &Answer,
    checkpoint_key: &str,
) -> Result<SetInclusion, SetInclusionError> {
    let (rows, want) = (&answer.rows, answer.want.as_str());
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
        anchor: answer.anchor.clone(),
    })
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
    // The proof is the one for this leaf, whatever the width of its integers
    // (Go reads a proof that need not be deterministic): its index is the
    // leaf's node index, which is what a path for another leaf would not be.
    let expected = encode_inclusion_proof(leaf, &path)
        .and_then(|e| ciborium::from_reader::<Cbor, _>(e.as_slice()).ok());
    if expected != Some(Cbor::Array(parts)) {
        return Err(format!(
            "the inclusion proof is not the proof for leaf {leaf}"
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
/// the consistency proof octets: `[size1, size2, paths, right peaks]` with
/// exactly the right peaks the paths leave out (Go `VerifyCLLConsistency`),
/// which reach the checkpoint's commitment.
fn check_consistency(cp: &DecodedCheckpointCose, octets: &[u8]) -> Result<(), SetInclusionError> {
    use ciborium::value::Value as Cbor;
    let malformed = || SetInclusionError("the consistency proof is malformed".into());
    let mut rest = octets;
    let Ok(Cbor::Array(parts)) = ciborium::from_reader::<Cbor, _>(&mut rest) else {
        return Err(malformed());
    };
    let uint = |v: &Cbor| match v {
        Cbor::Integer(n) => u64::try_from(*n).ok(),
        _ => None,
    };
    let [size1, size2, Cbor::Array(raw_paths), _] = parts.as_slice() else {
        return Err(malformed());
    };
    let (Some(size1), Some(size2)) = (uint(size1), uint(size2)) else {
        return Err(malformed());
    };
    let hashes = |v: &Cbor| -> Option<Vec<mmr::Hash>> {
        let Cbor::Array(items) = v else { return None };
        items
            .iter()
            .map(|i| match i {
                Cbor::Bytes(b) => mmr::Hash::try_from(b.as_slice()).ok(),
                _ => None,
            })
            .collect()
    };
    let paths: Option<Vec<Vec<mmr::Hash>>> = raw_paths.iter().map(hashes).collect();
    let (true, Some(paths)) = (rest.is_empty(), paths) else {
        return Err(malformed());
    };
    if size1 != cp.prev_size || size2 != cp.mmr_size {
        return bad("the consistency proof is not between the checkpoint and its previous one");
    }
    // The proof the encoder builds from the paths is exactly the right peaks
    // the paths leave out; the same member for member as this one.
    let expected = encode_consistency_proof(
        size1,
        size2,
        &cp.prev_peak_hashes,
        &paths,
        &cp.new_peak_hashes,
    )
    .and_then(|e| ciborium::from_reader::<Cbor, _>(e.as_slice()).ok());
    if expected != Some(Cbor::Array(parts)) {
        return bad("the consistency proof does not reach the checkpoint's commitment");
    }
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_body_is_its_digests_record_by_either_rule() {
        let bytes = br#"{"a":1}"#;
        let digest = hex::encode(Sha256::digest(bytes));
        assert!(record_body_matches(bytes, &digest));
        assert!(!record_body_matches(b"{}", &digest));
        // A capsule as a ledger holds it: its id, signature and key id are no
        // part of the preimage the id is the digest of.
        let id = hex::encode(Sha256::digest(br#"{"a":1,"b":[true]}"#));
        let capsule =
            format!(r#"{{"b":[true],"a":1,"capsule_id":"{id}","signature":"s","key_id":"k"}}"#);
        assert!(record_body_matches(capsule.as_bytes(), &id));
        let other = hex::encode(Sha256::digest(b"another"));
        assert!(!record_body_matches(capsule.as_bytes(), &other));
        let wrong = capsule.replace(r#""a":1"#, r#""a":2"#);
        assert!(!record_body_matches(wrong.as_bytes(), &id));
        assert!(!record_body_matches(b"not json", &id));
        // A number with no canonical form makes no capsule id: `1e400` is not
        // read as the `null` whose capsule hashes to the digest.
        let nulled = hex::encode(Sha256::digest(br#"{"a":null}"#));
        let overflow = format!(r#"{{"a":1e400,"capsule_id":"{nulled}"}}"#);
        assert!(!record_body_matches(overflow.as_bytes(), &nulled));
        let fraction = format!(r#"{{"a":1.5,"capsule_id":"{nulled}"}}"#);
        assert!(!record_body_matches(fraction.as_bytes(), &nulled));
    }
}
