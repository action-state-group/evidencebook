//! The whole-shelf handover and its replay (Go `shelf_handover.go`;
//! evidencebook#461, `docs/design/461-shelf-facts.md` §2.7): a reader the
//! holder chooses is handed every entry of the shelf log up to an anchor,
//! with the documents after the format switch, each book fact's status
//! opening, the book checkpoints the facts carry and the consistency proofs
//! between a book's facts, and checks that nothing was held back, left out,
//! changed or replaced. Counting a card from the latest facts is
//! standard_history/3's (AAC). A genesis fact comes with its tie to the old
//! witnessed tree when the holder has one ([`super::handover_tie`]); one
//! without is reported untied, which claims nothing about a log before the
//! coverage start.
//!
//! The JSON of [`Handover`] and [`HandoverResult`] is Go's, member for
//! member: byte strings are standard base64 with padding, as Go's
//! `encoding/json` writes `[]byte` (`testdata/shelf/handover.json` and
//! `handover-result.json`).

use super::cadence::{verify_witness_receipt, Cadence, WitnessReceipt, WITNESS_VERIFIED};
use super::handover_tie::{verify_tie, HandoverTie};
use super::log::Checkpoint;
use super::stamp::bytes_b64;
use super::ShelfError;
use crate::shelf_facts::{
    entry_id, BookFact, Opening, ENTRY_KIND_FORMAT_SWITCH, ENTRY_KIND_STAMP, FACT_KIND_BOOK,
    FACT_KIND_JOURNAL,
};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use cll::mmr;
use rusqlite::params;
use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashMap, HashSet};

/// The proof kind of a CLL consistency proof (Go `ConsistencyEvidence`).
pub(crate) const CONSISTENCY_KIND_CLL: &str = "cll-mmr-consistency";

/// The material a whole-shelf reader is handed (§2.7; Go `Handover`).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Handover {
    pub v: i64,
    /// The shelf log (the cadence log).
    pub log_id: String,
    /// Every entry of the shelf log, in order, up to the anchor: 32-byte ids
    /// in lowercase hex.
    #[serde(default, deserialize_with = "nullable")]
    pub entries: Vec<String>,
    /// The entry documents after the format switch, with the status opening
    /// of each book fact.
    #[serde(default, deserialize_with = "nullable")]
    pub documents: Vec<HandoverDocument>,
    /// The COSE statements of the book checkpoints the book facts carry.
    #[serde(default, with = "bytes_list")]
    pub book_checkpoints: Vec<Vec<u8>>,
    /// For each pair of consecutive facts of a book whose size grew, the
    /// proof between their checkpoints.
    #[serde(default, deserialize_with = "nullable")]
    pub consistency: Vec<HandoverConsistency>,
    /// The shelf checkpoint the handover runs to.
    #[serde(default, with = "bytes_b64")]
    pub anchor: Vec<u8>,
    /// The witness receipts the holder holds for the anchor
    /// (`shelf_receipts`), one per witness in witness URL order.
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub receipts: Vec<WitnessReceipt>,
    /// The shelf checkpoints before the anchor, within it, for which the
    /// holder holds receipts, each with every receipt held, in tick order
    /// (invariant C, `docs/design/461-deal-profile.md` §8.1; Go
    /// `Handover.Witnessed`): a reader resolves the record times the
    /// holder's `record_time` gives from them ([`super::HandoverTimes`]).
    #[serde(
        default,
        deserialize_with = "nullable",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub witnessed: Vec<HandoverCheckpoint>,
    /// Ties each genesis fact the holder can tie to the old witnessed tree
    /// ([`HandoverTie`]), in book id order.
    #[serde(default, deserialize_with = "nullable")]
    pub ties: Vec<HandoverTie>,
}

/// A shelf checkpoint before a handover's anchor and the witness receipts
/// held for it, one per witness in witness URL order (Go
/// `HandoverCheckpoint`).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct HandoverCheckpoint {
    #[serde(default, with = "bytes_b64")]
    pub checkpoint: Vec<u8>,
    #[serde(default, deserialize_with = "nullable")]
    pub receipts: Vec<WitnessReceipt>,
}

/// One entry document and, for a book fact, its status opening (Go
/// `HandoverDocument`).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct HandoverDocument {
    pub seq: u64,
    #[serde(default, with = "bytes_b64")]
    pub doc: Vec<u8>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub status_opening: Option<Opening>,
}

/// A consistency proof between two checkpoints of one book (Go
/// `HandoverConsistency`).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct HandoverConsistency {
    pub log_id: String,
    pub from_size: u64,
    pub to_size: u64,
    pub proof: ConsistencyEvidence,
}

/// A consistency proof of some kind (Go `ConsistencyEvidence`): for a CLL
/// log, `cll-mmr-consistency` and the proof's portable JSON.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ConsistencyEvidence {
    pub kind: String,
    #[serde(default)]
    pub proof: Value,
}

/// What a verified handover shows (Go `HandoverResult`).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct HandoverResult {
    /// True: [`verify_handover`] fails rather than return a result it could
    /// not recompute.
    pub recomputed: bool,
    /// A receipt from a witness in the directory verifies for the anchor and
    /// gives a registration time (§2.7); a receipt with no registration time
    /// cannot give the witnessed level. A shelf with no witness is a
    /// supported default and is unwitnessed.
    pub witnessed: bool,
    /// The shelf log size, in entries, at which the facts begin to be
    /// complete: the end of the migration's stamp.
    pub coverage_start: u64,
    /// Each book's latest fact and its status opening, in book id order.
    pub latest: Vec<HandoverLatest>,
    /// The books whose genesis fact carries no tie to the old witnessed
    /// tree, in book id order.
    pub untied: Vec<String>,
}

/// A book's latest fact up to the anchor (Go `HandoverLatest`).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct HandoverLatest {
    pub fact: BookFact,
    pub status_opening: Opening,
}

/// Go reads `null` (and a missing member) as the zero value.
fn nullable<'de, D: Deserializer<'de>, T: Deserialize<'de> + Default>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

/// Go's `[][]byte` in JSON: a list of standard base64 strings.
mod bytes_list {
    use base64::engine::general_purpose::STANDARD;
    use base64::Engine as _;
    use serde::{Deserialize, Deserializer, Serializer};

    pub fn serialize<S: Serializer>(list: &[Vec<u8>], s: S) -> Result<S::Ok, S::Error> {
        s.collect_seq(list.iter().map(|b| STANDARD.encode(b)))
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Vec<Vec<u8>>, D::Error> {
        Option::<Vec<Option<String>>>::deserialize(d)?
            .unwrap_or_default()
            .into_iter()
            .map(|t| {
                STANDARD
                    .decode(t.unwrap_or_default())
                    .map_err(serde::de::Error::custom)
            })
            .collect()
    }
}

/// A stamp entry document (Go `StampEntry`), to check a document is its
/// canonical JSON.
#[derive(Serialize, Deserialize)]
struct StampEntry {
    v: i64,
    kind: String,
    stamp: u64,
    facts: u64,
}

/// One log of the lifetime table at the migration (Go `LoggedSize`).
#[derive(Serialize, Deserialize)]
struct LoggedSize {
    log_id: String,
    size: u64,
}

/// The format-switch marker (Go `FormatSwitch`); Go reads `"logs": null`
/// as no list and writes it back as `null`.
#[derive(Serialize, Deserialize)]
struct FormatSwitch {
    v: i64,
    kind: String,
    format: String,
    last_tick: u64,
    logs: Option<Vec<LoggedSize>>,
}

/// An entry document's kind, `""` when it names none.
#[derive(Deserialize)]
struct Head {
    #[serde(default, deserialize_with = "nullable")]
    kind: String,
}

/// Decode an entry document into `T` and require the document to be `T`'s
/// canonical JSON, so every language reads the same members: a duplicate,
/// an unknown member or another spelling of a value changes the bytes (Go
/// `canonicalDocument`).
pub(crate) fn canonical_document<T>(doc: &[u8]) -> Result<T, String>
where
    T: Serialize + for<'de> Deserialize<'de>,
{
    let v: T = serde_json::from_slice(doc).map_err(|e| e.to_string())?;
    let value = serde_json::to_value(&v).map_err(|e| e.to_string())?;
    let (_, canonical) = entry_id(&value).map_err(|e| e.to_string())?;
    if canonical != doc {
        return Err("not its canonical JSON".into());
    }
    Ok(v)
}

/// A record id: 64 lowercase hex characters (Go `decodeRecordID`).
fn decode_record_id(id: &str) -> Result<[u8; 32], String> {
    let bad = || format!("record id {id:?} is not 64 lowercase hex characters");
    if id.len() != 64 || !id.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f')) {
        return Err(bad());
    }
    hex::decode(id)
        .ok()
        .and_then(|b| b.try_into().ok())
        .ok_or_else(bad)
}

/// The portable JSON of a CLL consistency proof (Go `portableConsistency`).
#[derive(Deserialize)]
struct PortableConsistency {
    #[serde(default, deserialize_with = "nullable")]
    v: u64,
    #[serde(default, deserialize_with = "nullable")]
    kind: String,
    #[serde(default, deserialize_with = "nullable")]
    old_size: u64,
    #[serde(default, deserialize_with = "nullable")]
    new_size: u64,
    #[serde(default, deserialize_with = "nullable")]
    old_peaks: Vec<String>,
    #[serde(default, deserialize_with = "nullable")]
    witness: Vec<Option<Vec<String>>>,
    #[serde(default, deserialize_with = "nullable")]
    new_peaks: Vec<String>,
}

/// Whether `evidence` proves `newer` append-only extends `older` (Go
/// `verifyCLLConsistency`).
pub(crate) fn verify_cll_consistency(
    older: &Checkpoint,
    newer: &Checkpoint,
    evidence: &ConsistencyEvidence,
) -> Result<(), String> {
    let wire = (evidence.kind == CONSISTENCY_KIND_CLL)
        .then(|| serde_json::from_value::<PortableConsistency>(evidence.proof.clone()).ok())
        .flatten()
        .ok_or("consistency evidence is not a CLL proof")?;
    let check = |values: &[String]| -> Result<Vec<String>, String> {
        values
            .iter()
            .map(|v| decode_record_id(v).map(|_| v.clone()))
            .collect()
    };
    let old_peaks = check(&wire.old_peaks)?;
    let new_peaks = check(&wire.new_peaks)?;
    let witness = wire
        .witness
        .iter()
        .map(|p| check(p.as_deref().unwrap_or_default()))
        .collect::<Result<Vec<_>, _>>()?;
    let old_root = decode_record_id(&older.root)?;
    let new_root = decode_record_id(&newer.root)?;
    if wire.old_size != older.tree_size || wire.new_size != newer.tree_size {
        return Err("consistency proof sizes do not match the checkpoints".into());
    }
    let proof = mmr::ConsistencyProof {
        v: u32::try_from(wire.v).unwrap_or(0),
        kind: wire.kind,
        size_a: wire.old_size,
        size_b: wire.new_size,
        old_peaks,
        witness,
        new_peaks,
    };
    if !mmr::verify_consistency(&old_root, wire.old_size, &new_root, wire.new_size, &proof) {
        return Err("consistency proof does not verify".into());
    }
    Ok(())
}

/// A fact as the handover collects it: its book, log, size and checkpoint.
struct HandedFact {
    book: String,
    log: String,
    size: u64,
    sum: String,
}

impl Cadence {
    /// Hand over the shelf up to tick `n`'s checkpoint (the newest finished
    /// tick when `n` is 0), which must be a stamp's: the material a reader
    /// the holder chooses replays (§2.7; Go `Handover`). Link openings and
    /// journal openings are never handed over.
    pub fn handover(&self, n: u64) -> Result<Handover, ShelfError> {
        let mut state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let n = if n == 0 {
            let max: i64 = self.shelf.shared().db().query_row(
                "SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0",
                [],
                |r| r.get(0),
            )?;
            max as u64
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
        let mut out = Handover {
            v: 1,
            log_id: self.cfg.log_id.clone(),
            entries: Vec::new(),
            documents: Vec::new(),
            book_checkpoints: Vec::new(),
            consistency: Vec::new(),
            anchor: tick.checkpoint.clone(),
            receipts: Vec::new(),
            witnessed: Vec::new(),
            ties: Vec::new(),
        };
        out.receipts = self.receipts_locked(&tick.checkpoint)?;
        out.witnessed = self.witnessed_before_locked(n, &anchor, &tick.checkpoint)?;
        let mut after = 0;
        while after < anchor.entries {
            let want = (anchor.entries - after).min(1000) as usize;
            let entries = self.log.entries(after, want)?;
            if entries.is_empty() {
                return Err(ShelfError::Corrupt(format!(
                    "the shelf log holds {after} entries, its checkpoint {}",
                    anchor.entries
                )));
            }
            for e in entries {
                // A gap, or an entry past the anchor, is a log that breaks
                // its own contract: stop rather than loop.
                if e.seq != after + 1 {
                    return Err(ShelfError::Corrupt(format!(
                        "the shelf log's entry after {after} is {}",
                        e.seq
                    )));
                }
                out.entries.push(hex::encode(&e.value));
                after = e.seq;
            }
        }
        type Row = (i64, String, Vec<u8>, Option<Vec<u8>>);
        let rows: Vec<Row> = {
            let db = self.shelf.shared().db();
            let mut stmt = db.prepare(
                "SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE entry_seq <= ? ORDER BY entry_seq",
            )?;
            let rows = stmt.query_map(params![anchor.entries as i64], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?))
            })?;
            rows.collect::<Result<_, _>>()?
        };
        let mut facts = Vec::new();
        for (seq, kind, doc, so) in rows {
            let mut d = HandoverDocument {
                seq: seq as u64,
                doc,
                status_opening: None,
            };
            if kind == FACT_KIND_BOOK {
                let corrupt = |e: serde_json::Error| {
                    ShelfError::Corrupt(format!("stored fact {}: {e}", d.seq))
                };
                let opening: Opening =
                    serde_json::from_slice(&so.unwrap_or_default()).map_err(corrupt)?;
                let f: BookFact = serde_json::from_slice(&d.doc).map_err(corrupt)?;
                facts.push(HandedFact {
                    book: f.book_id,
                    log: f.log_id,
                    size: f.size,
                    sum: f.checkpoint_sha256,
                });
                d.status_opening = Some(opening);
            }
            out.documents.push(d);
        }
        // The book checkpoints the facts carry are the statements the
        // stamps' leaves kept, one per distinct checkpoint.
        let ticks: Vec<i64> = {
            let db = self.shelf.shared().db();
            let mut stmt =
                db.prepare("SELECT tick FROM cadence_ticks WHERE tick <= ? ORDER BY tick")?;
            let rows = stmt.query_map([n as i64], |r| r.get(0))?;
            rows.collect::<Result<_, _>>()?
        };
        let mut statements: HashMap<String, Vec<u8>> = HashMap::new();
        for t in ticks {
            let got = self.tick_locked(t as u64)?;
            if got.leaves.stamp.is_none() {
                continue;
            }
            for leaf in got.leaves.deals.values() {
                if leaf.statement.is_empty() {
                    continue;
                }
                let b = URL_SAFE_NO_PAD.decode(&leaf.statement).map_err(|e| {
                    ShelfError::Corrupt(format!("tick {t}'s statement of {}: {e}", leaf.log_id))
                })?;
                statements.insert(leaf.checkpoint_sha256.clone(), b);
            }
        }
        let statement_of = |f: &HandedFact| {
            statements.get(&f.sum).ok_or_else(|| {
                ShelfError::Corrupt(format!(
                    "no stored statement for {}'s checkpoint {}",
                    f.log, f.sum
                ))
            })
        };
        let mut seen = HashSet::new();
        let mut last: HashMap<&str, &HandedFact> = HashMap::new();
        for f in &facts {
            if seen.insert(f.sum.clone()) {
                out.book_checkpoints.push(statement_of(f)?.clone());
            }
            if let Some(prev) = last.get(f.log.as_str()) {
                if f.size > prev.size {
                    let older = Checkpoint::from_statement(statement_of(prev)?)?;
                    let newer = Checkpoint::from_statement(statement_of(f)?)?;
                    let log = self.shelf.cadence_log_locked(&mut state, &f.log)?;
                    let proof = log.log().prove_consistency(&older, &newer)?;
                    out.consistency.push(HandoverConsistency {
                        log_id: f.log.clone(),
                        from_size: prev.size,
                        to_size: f.size,
                        proof: ConsistencyEvidence {
                            kind: CONSISTENCY_KIND_CLL.into(),
                            proof,
                        },
                    });
                }
            }
            last.insert(&f.log, f);
        }
        // Each book's genesis fact (its first) with its tie, when the holder
        // has one, in book id order.
        let mut genesis: BTreeMap<&str, &HandedFact> = BTreeMap::new();
        for f in &facts {
            genesis.entry(&f.book).or_insert(f);
        }
        for (book, f) in genesis {
            if let Some(tie) = self.tie_locked(&mut state, book, &f.log, statement_of(f)?)? {
                out.ties.push(tie);
            }
        }
        Ok(out)
    }
}

impl Cadence {
    /// Every shelf checkpoint of a tick before tick `n`, within the anchor,
    /// for which `shelf_receipts` holds a receipt, with those receipts, in
    /// tick order (Go `witnessedBeforeLocked`). Two ticks that cut one
    /// checkpoint give it once.
    fn witnessed_before_locked(
        &self,
        n: u64,
        anchor: &Checkpoint,
        anchor_statement: &[u8],
    ) -> Result<Vec<HandoverCheckpoint>, ShelfError> {
        let statements: Vec<Vec<u8>> = {
            let db = self.shelf.shared().db();
            let mut stmt = db.prepare(
                "SELECT checkpoint FROM cadence_ticks WHERE tick < ? AND checkpoint_size > 0 AND checkpoint_size <= ? ORDER BY tick",
            )?;
            let rows = stmt.query_map(params![n as i64, anchor.tree_size as i64], |r| {
                r.get::<_, Vec<u8>>(0)
            })?;
            rows.collect::<Result<_, _>>()?
        };
        let mut seen = HashSet::new();
        let mut out = Vec::new();
        for b in statements {
            if b == anchor_statement || !seen.insert(Sha256::digest(&b).to_vec()) {
                continue;
            }
            let receipts = self.receipts_locked(&b)?;
            if !receipts.is_empty() {
                out.push(HandoverCheckpoint {
                    checkpoint: b,
                    receipts,
                });
            }
        }
        Ok(out)
    }
}

/// Check a handover's earlier witnessed checkpoints (invariant C; Go
/// `verifyWitnessedCheckpoints`): each is the holder's checkpoint of the
/// shelf log, within the anchor and not the anchor, held once, carries a
/// receipt, and its root is the one the handed-over entries recompute at its
/// size. Whether a receipt verifies is the reader's to judge, as for the
/// anchor's.
fn verify_witnessed_checkpoints(
    h: &Handover,
    anchor: &Checkpoint,
    tree: &mmr::MemoryNodeStore,
    pinned_key: &str,
) -> Result<(), String> {
    let mut seen: HashSet<&[u8]> = HashSet::new();
    seen.insert(&h.anchor);
    for (i, w) in h.witnessed.iter().enumerate() {
        let i = i + 1;
        let cp = Checkpoint::from_statement(&w.checkpoint)
            .map_err(|e| format!("witnessed checkpoint {i} does not verify: {e}"))?;
        if cp.key_id != pinned_key || cp.log_id != h.log_id {
            return Err(format!(
                "witnessed checkpoint {i} is not the holder's shelf log"
            ));
        }
        if !seen.insert(&w.checkpoint) {
            return Err(format!(
                "witnessed checkpoint {i} is the anchor or given twice"
            ));
        }
        if cp.tree_size > anchor.tree_size || cp.entries > anchor.entries {
            return Err(format!("witnessed checkpoint {i} is past the anchor"));
        }
        if w.receipts.is_empty() {
            return Err(format!("witnessed checkpoint {i} carries no receipt"));
        }
        if root_at(tree, cp.tree_size).as_deref() != Some(cp.root.as_str()) {
            return Err(format!(
                "the handed-over entries do not recompute witnessed checkpoint {i}"
            ));
        }
    }
    Ok(())
}

/// The root of the MMR `tree` at `size` (Go `PeakHashesAt` then
/// `RootFromPeaks`), lowercase hex; `None` when `size` is not an MMR size
/// the tree reaches.
pub(crate) fn root_at(tree: &mmr::MemoryNodeStore, size: u64) -> Option<String> {
    let peaks = if size == 0 {
        Vec::new()
    } else {
        match mmr::peaks(size) {
            Ok(p) if size <= mmr::NodeReader::size(tree) => p,
            _ => return None,
        }
    };
    let peaks: Vec<mmr::Hash> = peaks
        .iter()
        .map(|&p| mmr::NodeReader::node(tree, p))
        .collect();
    Some(hex::encode(mmr::root_from_peaks(&peaks)))
}

/// A handover's consistency proofs by `(log id, from size, to size)`.
type Proofs = HashMap<(String, u64, u64), ConsistencyEvidence>;

/// Check two consecutive facts `p` then `f` of one book (§2.7; Go
/// `checkFactChain`): one log, `prev_fact_size` the previous size, no
/// shrinking, the same checkpoint at the same size, and a consistency proof
/// that verifies when the size grew. A break is a replaced book.
/// `statements` maps a checkpoint statement's SHA-256 to the checkpoint.
pub(crate) fn check_fact_chain(
    p: &BookFact,
    f: &BookFact,
    statements: &HashMap<String, Checkpoint>,
    proofs: &Proofs,
) -> Result<(), String> {
    let replaced = |m: String| Err(format!("{m} (a replaced book)"));
    if f.log_id != p.log_id {
        return replaced(format!(
            "names {}, its previous fact {}",
            f.log_id, p.log_id
        ));
    }
    if f.prev_fact_size != p.size {
        return replaced(format!(
            "names previous size {}, its previous fact {}",
            f.prev_fact_size, p.size
        ));
    }
    if f.size < p.size {
        return replaced(format!("shrinks the book from {} to {}", p.size, f.size));
    }
    if f.size == p.size && f.checkpoint_sha256 != p.checkpoint_sha256 {
        return replaced(format!("keeps size {} with another checkpoint", f.size));
    }
    if f.size > p.size {
        let Some(proof) = proofs.get(&(f.log_id.clone(), p.size, f.size)) else {
            return replaced(format!(
                "no consistency proof for {} from {} to {}",
                f.log_id, p.size, f.size
            ));
        };
        let (Some(older), Some(newer)) = (
            statements.get(&p.checkpoint_sha256),
            statements.get(&f.checkpoint_sha256),
        ) else {
            return replaced(format!(
                "a checkpoint of {} the handover does not hold",
                f.log_id
            ));
        };
        if let Err(e) = verify_cll_consistency(older, newer, proof) {
            return replaced(format!("{} from {} to {}: {e}", f.log_id, p.size, f.size));
        }
    }
    Ok(())
}

/// Replay a handover (§2.7; Go `VerifyHandover`) under the holder's
/// checkpoint key `pinned_key` (hex) and the reader's witness directory
/// (AAC's directory JSON, as [`super::CadenceConfig::witnesses`]). It fails
/// when an entry is held back, a document is left out, is not its canonical
/// JSON or does not hash to its entry, a status opening does not recompute
/// its commitment, a book checkpoint is not the fact's or not the holder's,
/// a book's facts break their chain (a replaced book), or a genesis fact's
/// tie ([`HandoverTie`]) does not verify, is one of two of a book, or names
/// a book with no genesis fact in the handover. The journal's
/// chain is not checked and its openings are not asked for.
pub fn verify_handover(
    h: &Handover,
    pinned_key: &str,
    witnesses: Option<&Value>,
) -> Result<HandoverResult, ShelfError> {
    fn bad<T>(m: String) -> Result<T, ShelfError> {
        Err(ShelfError::Invalid(format!("handover: {m}")))
    }
    let anchor = match Checkpoint::from_statement(&h.anchor) {
        Ok(a) => a,
        Err(e) => return bad(format!("the anchor does not verify: {e}")),
    };
    if h.v != 1 {
        return bad(format!("a handover of version {}", h.v));
    }
    if anchor.key_id != pinned_key || anchor.log_id != h.log_id {
        return bad("the anchor is not the holder's shelf log".into());
    }
    // Every entry up to the anchor, exactly: an entry held back changes the
    // count or the root.
    if h.entries.len() as u64 != anchor.entries {
        return bad(format!(
            "{} entries for an anchor of {}",
            h.entries.len(),
            anchor.entries
        ));
    }
    let mut tree = mmr::MemoryNodeStore::new();
    for (i, id) in h.entries.iter().enumerate() {
        let value = match decode_record_id(id) {
            Ok(v) => v,
            Err(e) => return bad(format!("entry {}: {e}", i + 1)),
        };
        mmr::add_leaf(&mut tree, mmr::leaf_hash(&value))
            .map_err(|e| ShelfError::Invalid(format!("handover: {e}")))?;
    }
    let Some(root) = root_at(&tree, anchor.tree_size) else {
        return bad(format!(
            "the entries do not reach the anchor's size {}",
            anchor.tree_size
        ));
    };
    if root != anchor.root {
        return bad("the entries do not recompute the anchor's root".into());
    }
    // Every entry after the marker has its document.
    let mut docs: BTreeMap<u64, &HandoverDocument> = BTreeMap::new();
    for d in &h.documents {
        if d.seq < 1 || d.seq > anchor.entries {
            return bad(format!("a document at {}, outside the handover", d.seq));
        }
        if hex::encode(Sha256::digest(&d.doc)) != h.entries[(d.seq - 1) as usize] {
            return bad(format!(
                "the document at {} does not hash to its entry",
                d.seq
            ));
        }
        docs.insert(d.seq, d);
    }
    let mut marker = 0;
    for seq in 1..=anchor.entries {
        let Some(d) = docs.get(&seq) else {
            if marker != 0 {
                return bad(format!(
                    "the entry at {seq} after the format switch has no document"
                ));
            }
            continue;
        };
        let head: Head = match serde_json::from_slice(&d.doc) {
            Ok(h) => h,
            Err(e) => return bad(format!("the document at {seq}: {e}")),
        };
        if marker == 0 {
            if head.kind != ENTRY_KIND_FORMAT_SWITCH {
                return bad(format!("a document at {seq} before the format switch"));
            }
            marker = seq;
        }
    }
    if marker == 0 {
        return bad("no format switch up to the anchor".into());
    }
    let mut statements: HashMap<String, Checkpoint> = HashMap::new();
    for b in &h.book_checkpoints {
        let cp = match Checkpoint::from_statement(b) {
            Ok(cp) => cp,
            Err(e) => return bad(format!("a book checkpoint does not verify: {e}")),
        };
        if cp.key_id != pinned_key {
            return bad(format!(
                "a book checkpoint of {} is not the holder's",
                cp.log_id
            ));
        }
        statements.insert(hex::encode(Sha256::digest(b)), cp);
    }
    let proofs: Proofs = h
        .consistency
        .iter()
        .map(|p| ((p.log_id.clone(), p.from_size, p.to_size), p.proof.clone()))
        .collect();
    let mut ties: BTreeMap<&str, &HandoverTie> = BTreeMap::new();
    for t in &h.ties {
        if ties.insert(&t.book_id, t).is_some() {
            return bad(format!("two ties of {}", t.book_id));
        }
    }
    let mut untied = Vec::new();
    let mut latest: BTreeMap<String, HandoverLatest> = BTreeMap::new();
    let mut coverage = 0;
    for seq in marker..=anchor.entries {
        let d = docs[&seq];
        let head: Head = match serde_json::from_slice(&d.doc) {
            Ok(h) => h,
            Err(e) => return bad(format!("the document at {seq}: {e}")),
        };
        let decoded = match head.kind.as_str() {
            ENTRY_KIND_STAMP => {
                if coverage == 0 {
                    coverage = seq;
                }
                canonical_document::<StampEntry>(&d.doc).map(|_| None)
            }
            ENTRY_KIND_FORMAT_SWITCH => canonical_document::<FormatSwitch>(&d.doc).map(|_| None),
            FACT_KIND_JOURNAL | FACT_KIND_BOOK => canonical_document::<BookFact>(&d.doc).map(Some),
            other => return bad(format!("the document at {seq} is a {other:?}")),
        };
        let f = match decoded {
            Ok(f) => f,
            Err(e) => return bad(format!("the document at {seq}: {e}")),
        };
        let Some(f) = f.filter(|_| head.kind == FACT_KIND_BOOK) else {
            continue;
        };
        let Some(opening) = &d.status_opening else {
            return bad(format!("the fact at {seq} has no status opening"));
        };
        if let Err(e) = opening.verify(&f.status_commitment) {
            return bad(format!("the fact at {seq}: {e}"));
        }
        let Some(cp) = statements.get(&f.checkpoint_sha256) else {
            return bad(format!(
                "the fact at {seq} carries a checkpoint the handover does not hold"
            ));
        };
        if cp.log_id != f.log_id || cp.tree_size != f.size {
            return bad(format!(
                "the fact at {seq} names {} at {}, its checkpoint {} at {}",
                f.log_id, f.size, cp.log_id, cp.tree_size
            ));
        }
        match latest.get(&f.book_id) {
            None => {
                if f.prev_fact_size != 0 {
                    return bad(format!(
                        "the first fact of {} at {seq} names a previous size",
                        f.book_id
                    ));
                }
                match ties.remove(f.book_id.as_str()) {
                    None => untied.push(f.book_id.clone()),
                    Some(t) => {
                        if let Err(e) =
                            verify_tie(t, &f, cp, h, &tree, marker, pinned_key, witnesses)
                        {
                            return bad(format!("the tie of {}: {e}", f.book_id));
                        }
                    }
                }
            }
            Some(prev) => {
                if let Err(e) = check_fact_chain(&prev.fact, &f, &statements, &proofs) {
                    return bad(format!("the fact of {} at {seq}: {e}", f.book_id));
                }
            }
        }
        latest.insert(
            f.book_id.clone(),
            HandoverLatest {
                fact: f,
                status_opening: opening.clone(),
            },
        );
    }
    if coverage == 0 {
        return bad("the migration's stamp is not in the handover".into());
    }
    if let Err(e) = verify_witnessed_checkpoints(h, &anchor, &tree, pinned_key) {
        return bad(e);
    }
    if let Some(id) = ties.keys().next() {
        return bad(format!(
            "a tie of {id}, which has no genesis fact in the handover"
        ));
    }
    untied.sort();
    // Only a receipt that gives a registration time gives the witnessed
    // level (§2.7).
    let witnessed = h.receipts.iter().any(|r| {
        verify_witness_receipt(&h.anchor, r, witnesses)
            .is_ok_and(|(c, v)| c.state == WITNESS_VERIFIED && v.registered_at.is_some())
    });
    Ok(HandoverResult {
        recomputed: true,
        witnessed,
        coverage_start: coverage,
        latest: latest.into_values().collect(),
        untied,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::shelf_facts::{book_fact, FactInput};
    use serde_json::json;

    fn fact_doc() -> Vec<u8> {
        let f = book_fact(
            FactInput {
                profile: "none",
                book_id: &"a".repeat(64),
                log_id: "deal/a",
                size: 3,
                checkpoint_sha256: &"b".repeat(64),
                prev_fact_size: 0,
                last_received: None,
                checked_over: Vec::new(),
                status: None,
            },
            [&[1; 32], &[2; 32]],
        )
        .unwrap();
        entry_id(&f.doc).unwrap().1
    }

    // Go's TestAnEntryDocumentMustBeCanonical: a document the holder signed
    // must be its canonical JSON, so no language reads a member another does
    // not.
    #[test]
    fn an_entry_document_must_be_canonical() {
        let doc = fact_doc();
        canonical_document::<BookFact>(&doc).unwrap();
        let body = std::str::from_utf8(&doc[1..doc.len() - 1]).unwrap();
        for (name, changed) in [
            ("a duplicate member", format!("{{\"v\":2,{body}}}")),
            ("an unknown member", format!("{{\"extra\":1,{body}}}")),
            ("whitespace", format!("{{ {body}}}")),
        ] {
            assert!(
                canonical_document::<BookFact>(changed.as_bytes()).is_err(),
                "{name}"
            );
        }
    }

    /// `testdata/shelf/handover.json`, the scripted shelf's handover.
    fn vector() -> Handover {
        let path = format!(
            "{}/../../testdata/shelf/handover.json",
            env!("CARGO_MANIFEST_DIR")
        );
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
    }

    // Go's TestAFactChainRefusesAReplacedBook, on deal/a's two facts of the
    // scripted handover: each break is a replaced book.
    #[test]
    fn a_fact_chain_refuses_a_replaced_book() {
        let h = vector();
        let statements: HashMap<String, Checkpoint> = h
            .book_checkpoints
            .iter()
            .map(|b| {
                (
                    hex::encode(Sha256::digest(b)),
                    Checkpoint::from_statement(b).unwrap(),
                )
            })
            .collect();
        let proofs: Proofs = h
            .consistency
            .iter()
            .map(|p| ((p.log_id.clone(), p.from_size, p.to_size), p.proof.clone()))
            .collect();
        let a: Vec<BookFact> = h
            .documents
            .iter()
            .filter_map(|d| serde_json::from_slice::<BookFact>(&d.doc).ok())
            .filter(|f| f.kind == FACT_KIND_BOOK && f.log_id == "deal/a")
            .collect();
        assert_eq!(a.len(), 2);
        let (prev, next) = (&a[0], &a[1]);
        assert!(next.size > prev.size);
        check_fact_chain(prev, next, &statements, &proofs).unwrap();
        let mut same = next.clone();
        same.prev_fact_size = next.size;
        check_fact_chain(next, &same, &statements, &proofs)
            .expect("a later fact at the same size and checkpoint (a changed value)");

        let mut wrong_proof = proofs.clone();
        let key = ("deal/a".to_string(), prev.size, next.size);
        wrong_proof.get_mut(&key).unwrap().proof["old_peaks"][0] = json!("00".repeat(32));

        let another_log = BookFact {
            log_id: "deal/b".into(),
            ..next.clone()
        };
        let wrong_previous = BookFact {
            prev_fact_size: prev.size + 1,
            ..next.clone()
        };
        let shrinking = BookFact {
            prev_fact_size: next.size,
            ..prev.clone()
        };
        let another_checkpoint = BookFact {
            prev_fact_size: prev.size,
            checkpoint_sha256: next.checkpoint_sha256.clone(),
            ..prev.clone()
        };
        let none = Proofs::new();
        for (name, p, f, proofs) in [
            ("another log", prev, &another_log, &proofs),
            ("a wrong previous size", prev, &wrong_previous, &proofs),
            ("a shrinking book", next, &shrinking, &proofs),
            (
                "the same size, another checkpoint",
                prev,
                &another_checkpoint,
                &proofs,
            ),
            ("no proof", prev, next, &none),
            ("a wrong proof", prev, next, &wrong_proof),
        ] {
            let err = check_fact_chain(p, f, &statements, proofs).expect_err(name);
            assert!(err.contains("a replaced book"), "{name}: {err}");
        }
    }

    // Go's TestAHandoverOfAnotherVersionIsRefused (the anchor parses first;
    // the key is the anchor's own, so only the version is wrong).
    #[test]
    fn a_handover_of_another_version_is_refused() {
        let mut h = vector();
        let key = Checkpoint::from_statement(&h.anchor).unwrap().key_id;
        for v in [0, 2] {
            h.v = v;
            let err = verify_handover(&h, &key, None).expect_err("another version");
            assert!(
                matches!(&err, ShelfError::Invalid(m) if m.contains("version")),
                "version {v}: {err}"
            );
        }
    }

    #[test]
    fn a_consistency_proof_of_another_kind_is_refused() {
        let cp = Checkpoint {
            kind: "cll".into(),
            log_id: "x".into(),
            id: String::new(),
            entries: 0,
            root: "0".repeat(64),
            tree_size: 0,
            key_id: String::new(),
            issued_at: String::new(),
            statement: Vec::new(),
        };
        let evidence = ConsistencyEvidence {
            kind: "other".into(),
            proof: Value::Null,
        };
        assert!(verify_cll_consistency(&cp, &cp, &evidence).is_err());
    }
}
