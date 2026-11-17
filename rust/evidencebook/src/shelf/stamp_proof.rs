//! Stamp proofs (Go `shelf_stamp_proof.go`; evidencebook#461 §3): that a
//! holder's shelf log carries a fact of one of its books, how a holder
//! builds one ([`Cadence::stamp_proof`]), how anyone checks one
//! ([`verify_stamp_proof`]), and the proof part of [`Shelf::receive`].
//!
//! [`StampProof`]'s JSON is Go's (`testdata/shelf/receive/README.md`): byte
//! strings in standard base64 with padding, the status opening as
//! `{salt, value}` and absent when not given, and the inclusion as
//! `{kind, seq, proof}`.

use super::cadence::Cadence;
use super::log::Checkpoint;
use super::receive::{
    before_commit, note_conflict, read_log_state, report, root_recomputes, sql_int, unsigned,
    ChangeSet, LogKey, ProofOutcome, ReceiveBounds, ReceiveProfile, TxError, EVIDENCE_CHECKPOINT,
    EVIDENCE_CHECKPOINT_RECORDS, PROOF_RECOMPUTED, PROOF_UNCHECKED,
};
use super::stamp::bytes_b64;
use super::{Shelf, ShelfError};
use crate::shelf_facts::{openings_for, BookFact, Opening, FACT_KIND_BOOK, FACT_KIND_JOURNAL};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use cll::mmr;
use rusqlite::{params, OptionalExtension};
use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};

/// The kind of a CLL inclusion proof.
const INCLUSION_KIND_CLL: &str = "cll-mmr-inclusion";

/// That a holder's shelf log carries a fact of one of its books (Go
/// `StampProof`): the fact, the book checkpoint it carries, the fact's
/// inclusion in the shelf log and the shelf checkpoint that includes it.
/// One checkpoint key signs both checkpoints: the proof's key.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct StampProof {
    /// The fact document's canonical JSON, a book fact.
    #[serde(default, with = "bytes_b64")]
    pub fact: Vec<u8>,
    /// The fact's status opening, when the audience the proof was made for
    /// may have it.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub status_opening: Option<Opening>,
    /// The COSE statement of the book checkpoint the fact carries.
    #[serde(default, with = "bytes_b64")]
    pub book_checkpoint: Vec<u8>,
    /// The fact entry's inclusion proof in the shelf log, at its entry
    /// position, under `shelf_checkpoint`.
    pub inclusion: InclusionEvidence,
    /// The COSE statement of the shelf checkpoint.
    #[serde(default, with = "bytes_b64")]
    pub shelf_checkpoint: Vec<u8>,
}

/// An entry's inclusion proof (Go `InclusionEvidence`): its kind, the
/// entry's position from 1 and the proof's portable JSON.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct InclusionEvidence {
    #[serde(default, deserialize_with = "nullable")]
    pub kind: String,
    #[serde(default, deserialize_with = "nullable")]
    pub seq: u64,
    #[serde(default)]
    pub proof: Value,
}

/// What a stamp proof shows once verified (Go `VerifiedStampProof`).
#[derive(Clone, Debug, PartialEq)]
pub struct VerifiedStampProof {
    pub fact: BookFact,
    pub book_checkpoint: Checkpoint,
    pub shelf_checkpoint: Checkpoint,
    /// The checkpoint key that signed both checkpoints.
    pub key_id: String,
}

/// Go reads `null` (and a missing member) as the zero value.
fn nullable<'de, D: Deserializer<'de>, T: Deserialize<'de> + Default>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

/// The portable JSON of a CLL inclusion proof (Go `MMRInclusion`).
#[derive(Deserialize)]
struct MmrInclusion {
    #[serde(default, deserialize_with = "nullable")]
    v: u64,
    #[serde(default, deserialize_with = "nullable")]
    kind: String,
    #[serde(default, deserialize_with = "nullable")]
    size: u64,
    #[serde(default, deserialize_with = "nullable")]
    leaf_index: u64,
    #[serde(default, deserialize_with = "nullable")]
    witness: Vec<String>,
    #[serde(default, deserialize_with = "nullable")]
    peaks_left: Vec<String>,
    #[serde(default, deserialize_with = "nullable")]
    peaks_right: Vec<String>,
}

fn is_digest(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f'))
}

/// Check a stamp proof on its own (§3, "the proof"; Go `VerifyStampProof`):
/// both checkpoints verify and are signed by one key; the fact is a
/// canonical book fact whose log and size are the book checkpoint's, and
/// whose `checkpoint_sha256` is the statement's digest; a status opening
/// given recomputes its commitment; and the fact's entry is included under
/// the shelf checkpoint. What the proof says against the records held is
/// the store's ([`Shelf::receive`]).
pub fn verify_stamp_proof(p: &StampProof) -> Result<VerifiedStampProof, ShelfError> {
    fn bad<T>(m: String) -> Result<T, ShelfError> {
        Err(ShelfError::Invalid(format!("stamp proof: {m}")))
    }
    let book = match Checkpoint::from_statement(&p.book_checkpoint) {
        Ok(cp) => cp,
        Err(e) => return bad(format!("the book checkpoint: {e}")),
    };
    let shelf = match Checkpoint::from_statement(&p.shelf_checkpoint) {
        Ok(cp) => cp,
        Err(e) => return bad(format!("the shelf checkpoint: {e}")),
    };
    if book.key_id != shelf.key_id {
        return bad(format!(
            "the book checkpoint is signed by {}, the shelf checkpoint by {}",
            book.key_id, shelf.key_id
        ));
    }
    let f: BookFact = match super::handover::canonical_document(&p.fact) {
        Ok(f) => f,
        Err(e) => return bad(format!("the fact: {e}")),
    };
    if f.v != 1 || f.kind != FACT_KIND_BOOK {
        return bad(format!(
            "a {:?} of version {}, not a book fact",
            f.kind, f.v
        ));
    }
    if f.log_id != book.log_id || f.size != book.tree_size {
        return bad(format!(
            "the fact names {} at {}, its book checkpoint {} at {}",
            f.log_id, f.size, book.log_id, book.tree_size
        ));
    }
    if hex::encode(Sha256::digest(&p.book_checkpoint)) != f.checkpoint_sha256 {
        return bad("the book checkpoint does not hash to the fact's checkpoint_sha256".into());
    }
    if let Some(o) = &p.status_opening {
        if let Err(e) = o.verify(&f.status_commitment) {
            return bad(format!("the status opening: {e}"));
        }
    }
    if p.inclusion.kind != INCLUSION_KIND_CLL || !shelf.covers(p.inclusion.seq) {
        return bad("no CLL inclusion proof of an entry the shelf checkpoint covers".into());
    }
    let wire: MmrInclusion = match serde_json::from_value(p.inclusion.proof.clone()) {
        Ok(w) => w,
        Err(e) => return bad(format!("the inclusion proof: {e}")),
    };
    if !wire
        .witness
        .iter()
        .chain(&wire.peaks_left)
        .chain(&wire.peaks_right)
        .chain(std::iter::once(&shelf.root))
        .all(|d| is_digest(d))
    {
        return bad("the inclusion proof is not hex digests".into());
    }
    let root: mmr::Hash = hex::decode(&shelf.root)
        .ok()
        .and_then(|b| b.try_into().ok())
        .expect("a checked digest");
    let entry: mmr::Hash = Sha256::digest(&p.fact).into();
    let proof = mmr::InclusionProof {
        v: u32::try_from(wire.v).unwrap_or(0),
        kind: wire.kind,
        size: wire.size,
        leaf_index: wire.leaf_index,
        witness: wire.witness,
        peaks_left: wire.peaks_left,
        peaks_right: wire.peaks_right,
    };
    if !mmr::verify_inclusion(&root, shelf.tree_size, p.inclusion.seq - 1, &entry, &proof) {
        return bad("the fact's entry is not included under the shelf checkpoint".into());
    }
    let key_id = book.key_id.clone();
    Ok(VerifiedStampProof {
        fact: f,
        book_checkpoint: book,
        shelf_checkpoint: shelf,
        key_id,
    })
}

impl Cadence {
    /// The proof of a book's latest fact at stamp `n` (the newest finished
    /// stamp when `n` is 0), for an audience (Go `StampProof`): the status
    /// opening is included when [`openings_for`] lets that audience have it,
    /// and the link opening never is. The journal's facts are proved to
    /// nobody.
    pub fn stamp_proof(
        &self,
        book_id: &str,
        n: u64,
        audience: &str,
    ) -> Result<StampProof, ShelfError> {
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let (_, with_status) = openings_for(FACT_KIND_BOOK, audience)
            .map_err(|e| ShelfError::Invalid(e.to_string()))?;
        let n = if n == 0 {
            self.shelf.shared().db().query_row(
                "SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0",
                [],
                |r| unsigned(r, 0),
            )?
        } else {
            n
        };
        let tick = self.tick_locked(n)?;
        if tick.leaves.stamp.is_none() || tick.checkpoint_size == 0 {
            return Err(ShelfError::Invalid(format!(
                "tick {n} is not a finished stamp"
            )));
        }
        let anchor = Checkpoint::from_statement(&tick.checkpoint)?;
        type Row = (u64, String, Vec<u8>, Option<Vec<u8>>);
        let row: Option<Row> = self
            .shelf
            .shared()
            .db()
            .query_row(
                "SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) AND entry_seq <= ? ORDER BY entry_seq DESC LIMIT 1",
                params![book_id, FACT_KIND_BOOK, FACT_KIND_JOURNAL, sql_int(anchor.entries)?],
                |r| Ok((unsigned(r, 0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
            )
            .optional()?;
        let Some((seq, kind, doc, so)) = row else {
            return Err(ShelfError::NotFound(format!(
                "no fact of book {book_id} at stamp {n}"
            )));
        };
        if kind != FACT_KIND_BOOK {
            return Err(ShelfError::Invalid(
                "the journal's facts are proved to nobody".into(),
            ));
        }
        let f: BookFact = serde_json::from_slice(&doc)
            .map_err(|e| ShelfError::Corrupt(format!("stored fact {seq}: {e}")))?;
        let status_opening = if with_status {
            Some(
                serde_json::from_slice(&so.unwrap_or_default()).map_err(|e| {
                    ShelfError::Corrupt(format!("stored status opening {seq}: {e}"))
                })?,
            )
        } else {
            None
        };
        // The statement is in the leaves of the stamp that cut the
        // checkpoint: the newest one up to n that names it.
        let mut book_checkpoint = None;
        let mut t = n;
        while t >= 1 && book_checkpoint.is_none() {
            let got = self.tick_locked(t)?;
            if let Some(leaf) = got.leaves.deals.get(&f.log_id) {
                if got.leaves.stamp.is_some()
                    && leaf.checkpoint_sha256 == f.checkpoint_sha256
                    && !leaf.statement.is_empty()
                {
                    book_checkpoint =
                        Some(URL_SAFE_NO_PAD.decode(&leaf.statement).map_err(|e| {
                            ShelfError::Corrupt(format!(
                                "tick {t}'s statement of {}: {e}",
                                f.log_id
                            ))
                        })?);
                }
            }
            t -= 1;
        }
        let book_checkpoint = book_checkpoint.ok_or_else(|| {
            ShelfError::Corrupt(format!(
                "no stored statement for {}'s checkpoint {}",
                f.log_id, f.checkpoint_sha256
            ))
        })?;
        let proof = self.log.prove_inclusion(seq, &anchor)?;
        Ok(StampProof {
            fact: doc,
            status_opening,
            book_checkpoint,
            inclusion: InclusionEvidence {
                kind: INCLUSION_KIND_CLL.into(),
                seq,
                proof,
            },
            shelf_checkpoint: tick.checkpoint,
        })
    }
}

/// `v`'s RFC 8785 bytes.
fn canonical(v: &impl Serialize) -> Result<Vec<u8>, ShelfError> {
    let value = serde_json::to_value(v).map_err(|e| ShelfError::Invalid(e.to_string()))?;
    crate::canonical::jcs(&value).map_err(|e| ShelfError::Invalid(e.to_string()))
}

impl Shelf {
    /// A [`Shelf::receive`] call's proof part (§3; Go `receiveProof`): verify
    /// the proof, ask the profile whose records it covers, refuse one that
    /// contradicts a held proof at the same size or the records held
    /// (keeping the evidence, once per size), and keep every proof accepted,
    /// two proofs of one book checkpoint under different shelf checkpoints
    /// both. A proof grants coverage only once the records held reach its
    /// size and recompute its root; one past them is kept, and checked when
    /// they arrive.
    pub(super) fn receive_proof(
        &self,
        profile: &dyn ReceiveProfile,
        p: &StampProof,
        changes: &mut ChangeSet,
    ) -> Result<ProofOutcome, ShelfError> {
        let mut out = ProofOutcome::default();
        let v = match verify_stamp_proof(p) {
            Ok(v) => v,
            Err(e) => {
                out.refused = Some(e);
                return Ok(out);
            }
        };
        let Some(sender) = profile
            .proof_sender(&v.key_id, &v.fact.log_id)
            .map_err(ShelfError::Profile)?
        else {
            out.refused = Some(ShelfError::Refused(format!(
                "{} may not push a stamp proof of {}",
                v.key_id, v.fact.log_id
            )));
            return Ok(out);
        };
        let key: LogKey = (sender.clone(), v.fact.log_id.clone());
        let shelf_digest = hex::encode(Sha256::digest(&p.shelf_checkpoint));
        // Stored as its canonical JSON, so every language keeps the same
        // bytes.
        let inclusion = canonical(&p.inclusion)?;
        let opening = p.status_opening.as_ref().map(canonical).transpose()?;
        let now = self.receive_now();
        let mut part = ChangeSet::default();
        let done = self.receive_tx(|tx| {
            let before = read_log_state(tx, &key)?;
            let prefix = &before.prefix;
            let size = v.fact.size;
            let same: Vec<(String, Vec<u8>)> = {
                let mut stmt = tx.prepare(
                    "SELECT shelf_checkpoint_sha256, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND size = ? ORDER BY received_at, shelf_checkpoint_sha256",
                )?;
                let rows = stmt.query_map(params![sender, v.fact.log_id, sql_int(size)?], |r| {
                    Ok((r.get(0)?, r.get(1)?))
                })?;
                rows.collect::<Result<_, _>>()?
            };
            for (_, held) in &same {
                let cp = Checkpoint::from_statement(held)?;
                if cp.root != v.book_checkpoint.root {
                    // Refused, and the evidence kept: the transaction commits.
                    out.refused = Some(ShelfError::Invalid(format!(
                        "the book checkpoint of {} at {size} contradicts one held from {sender}",
                        v.fact.log_id
                    )));
                    if note_conflict(
                        tx,
                        &key,
                        EVIDENCE_CHECKPOINT,
                        size,
                        held,
                        &p.book_checkpoint,
                        &now,
                        false,
                        &ReceiveBounds::default(),
                    )? {
                        part.forked.extend(prefix.deals.iter().cloned());
                    }
                    return Ok(());
                }
            }
            let mut state = PROOF_UNCHECKED;
            let entries = v.book_checkpoint.entries;
            if entries <= prefix.counted.len() as u64 {
                let (matched, over) = root_recomputes(&v.book_checkpoint, &prefix.digests(entries))?;
                if !matched {
                    out.refused = Some(ShelfError::Invalid(format!(
                        "the records held from {sender} in {} do not recompute the book checkpoint at {size}",
                        v.fact.log_id
                    )));
                    if note_conflict(
                        tx,
                        &key,
                        EVIDENCE_CHECKPOINT_RECORDS,
                        size,
                        &p.book_checkpoint,
                        &over,
                        &now,
                        false,
                        &ReceiveBounds::default(),
                    )? {
                        part.forked.extend(prefix.deals.iter().cloned());
                    }
                    return Ok(());
                }
                state = PROOF_RECOMPUTED;
            }
            if same.iter().any(|(shelf, _)| *shelf == shelf_digest) {
                out.duplicate = true;
                return Ok(());
            }
            tx.execute(
                "INSERT INTO shelf_received_proofs (sender_key, log_id, size, shelf_checkpoint_sha256, book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                params![sender, v.fact.log_id, sql_int(size)?, shelf_digest, p.book_checkpoint, p.fact, opening, inclusion, p.shelf_checkpoint, now, state],
            )?;
            out.accepted = true;
            let after = read_log_state(tx, &key)?;
            report(&before, &after, &mut part);
            before_commit("proof")
        });
        match done {
            Ok(()) => {
                changes.merge(part);
                Ok(out)
            }
            Err(TxError::Store(e) | TxError::Refusal(e)) => Err(e),
            Err(TxError::Stale) => unreachable!("a proof plans no offer"),
        }
    }
}
