//! A book on a shelf: one log id whose records the shelf seals, stores and
//! commits (Go `book.go`: `Open`'s recovery, `Append`, `CheckpointAt`; the
//! default authenticated indexes of `index_authenticated.go`).
//!
//! A record is sealed exactly as [`crate::seal`] seals it, its row is
//! written to `book_records`, and its id is appended to the book's CLL log:
//! two writes, not one transaction. Opening the book reconciles a crash
//! between them: every stored record is checked against the log position it
//! claims, a record stored but not yet in the log is appended again at its
//! `committed_at` and must land at its `seq`, and a log entry with no stored
//! record is corrupt (evidencebook#254 §2.3).

use super::log::{Checkpoint, Log};
use super::store::{self, StoredRecord};
use super::time::Time;
use super::{ShelfError, ShelfShared};
use crate::ask::{self, Book as AskBook, Entry as AskEntry, SealError, Sealed};
use crate::canonical;
use crate::record::{record_types, EpistemicType, Link, LinkType};
use crate::retention::RetentionState;
use crate::seal::{self, Producer, DEFAULT_DEVELOPER};
use crate::store::{CheckpointedLog, ConsistencyParts, Record, RecordSource};
use crate::substrate::{Checkpoint as LogCheckpoint, StatedCheckpoint, SubstrateError};
use capsule_emit_artifact::{Artifact, Record as ArtifactRecord, RetentionState as Held};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashMap};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration;

/// Which authenticated indexes a book commits roots of (Go
/// `ShelfConfig.AuthenticatedIndexes`).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Indexes {
    /// The defaults: every record under its record id, and under its
    /// `subject_ref`. Each checkpoint first commits any changed root as an
    /// `index_root` record.
    Defaults,
    /// None: the book's checkpoints commit no `index_root` records, so its
    /// log holds its own records only.
    None,
}

/// One record to append (Go `Entry`, without field openings): the book
/// assigns its position and commit time, commits each payload by its
/// SHA-256, and keeps the payload bytes in the shelf's artifact store.
#[derive(Clone, Debug, PartialEq)]
pub struct BookEntry {
    pub record_type: String,
    pub epistemic_type: EpistemicType,
    pub links: Vec<Link>,
    pub subject_ref: Option<String>,
    pub principal_ref: Option<String>,
    pub counterparty_ref: Option<String>,
    pub correlation: BTreeMap<String, String>,
    pub event_time_claim: Option<Time>,
    /// The header's `statement`; `Value::Null` for none.
    pub statement: Value,
    /// Payload bytes, kept as originals `payload-1`, `payload-2`, ...
    pub payloads: Vec<Vec<u8>>,
}

impl BookEntry {
    /// An entry of `record_type` and `epistemic_type` with nothing else set.
    pub fn new(record_type: &str, epistemic_type: EpistemicType) -> Self {
        Self {
            record_type: record_type.to_string(),
            epistemic_type,
            links: Vec::new(),
            subject_ref: None,
            principal_ref: None,
            counterparty_ref: None,
            correlation: BTreeMap::new(),
            event_time_claim: None,
            statement: Value::Null,
            payloads: Vec::new(),
        }
    }
}

/// A committed record of a book, as the book holds it.
#[derive(Clone, Debug, PartialEq)]
pub struct BookRecord {
    pub seq: u64,
    pub record_id: String,
    /// The header's members, as stored.
    pub header: Map<String, Value>,
    /// The header's canonical bytes, as stored.
    pub header_bytes: Vec<u8>,
    /// The stored capsule (with `capsule_id`).
    pub capsule: Vec<u8>,
    /// The Producer Envelope.
    pub envelope: Vec<u8>,
    /// The capsule without `capsule_id`, in JCS: the record's octets, whose
    /// SHA-256 is the record id.
    pub body: Vec<u8>,
}

impl BookRecord {
    fn record_type(&self) -> &str {
        self.header
            .get("record_type")
            .and_then(Value::as_str)
            .unwrap_or("")
    }

    fn subject_ref(&self) -> Option<&str> {
        self.header
            .get("subject_ref")
            .and_then(Value::as_str)
            .filter(|s| !s.is_empty())
    }

    fn links_to(&self, link_type: LinkType) -> Vec<&str> {
        links_to(&self.header, link_type)
    }
}

pub(crate) fn links_to(header: &Map<String, Value>, link_type: LinkType) -> Vec<&str> {
    header
        .get("links")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter(|l| l.get("type").and_then(Value::as_str) == Some(link_type.as_str()))
        .filter_map(|l| l.get("target").and_then(Value::as_str))
        .collect()
}

/// One of Go's default authenticated indexes (`SortedMerkleIndex`): an RFC
/// 9162 Merkle tree over sorted `(key, record id)` leaves.
struct SortedMerkleIndex {
    name: &'static str,
    key_of: fn(&BookRecord) -> Option<String>,
    leaves: Vec<(String, String)>,
}

impl SortedMerkleIndex {
    fn defaults() -> Vec<Self> {
        vec![
            SortedMerkleIndex {
                name: "record_id",
                key_of: |r| Some(r.record_id.clone()),
                leaves: Vec::new(),
            },
            SortedMerkleIndex {
                name: "subject_ref",
                key_of: |r| r.subject_ref().map(str::to_string),
                leaves: Vec::new(),
            },
        ]
    }

    fn add(&mut self, record: &BookRecord) {
        let Some(key) = (self.key_of)(record) else {
            return;
        };
        let leaf = (key, record.record_id.clone());
        // Go orders strings by their bytes, as Rust does.
        if let Err(at) = self.leaves.binary_search(&leaf) {
            self.leaves.insert(at, leaf);
        }
    }

    /// `{index, root, size}`, as Go's `IndexRoot`.
    fn root(&self) -> Value {
        let hashes: Vec<[u8; 32]> = self
            .leaves
            .iter()
            .map(|(k, r)| {
                let mut h = Sha256::new();
                h.update([0u8]);
                h.update((k.len() as u32).to_be_bytes());
                h.update(k.as_bytes());
                h.update((r.len() as u32).to_be_bytes());
                h.update(r.as_bytes());
                h.finalize().into()
            })
            .collect();
        json!({"index": self.name, "root": hex::encode(merkle_root(&hashes)), "size": self.leaves.len()})
    }
}

fn merkle_root(leaves: &[[u8; 32]]) -> [u8; 32] {
    match leaves.len() {
        0 => Sha256::digest(b"").into(),
        1 => leaves[0],
        n => {
            let mut k = 1;
            while k << 1 < n {
                k <<= 1;
            }
            let mut h = Sha256::new();
            h.update([1u8]);
            h.update(merkle_root(&leaves[..k]));
            h.update(merkle_root(&leaves[k..]));
            h.finalize().into()
        }
    }
}

pub(crate) struct BookState {
    pub(crate) records: Vec<BookRecord>,
    by_id: HashMap<String, usize>,
    indexes: Vec<SortedMerkleIndex>,
    /// The newest committed root of each index, as `{index, root, size}`.
    roots: HashMap<String, Value>,
    /// A record already stored whose log append failed: it holds the next
    /// position until the append succeeds.
    pending: Option<BookRecord>,
    pub(crate) released: bool,
}

pub(crate) struct BookCore {
    pub(crate) id: String,
    shared: Arc<ShelfShared>,
    pub(crate) log: Log,
    pub(crate) state: Mutex<BookState>,
}

/// A book on a shelf (Go `*Book` from `Shelf.Open`). Handles are cheap
/// clones of one book: the shelf keeps one per log id for its lifetime, so
/// two handles never give two records one position.
///
/// It implements [`RecordSource`], [`CheckpointedLog`] and [`ask::Book`],
/// so every answer path (`respond`, `answer`, the derivations) runs on it.
#[derive(Clone)]
pub struct ShelfBook(pub(crate) Arc<BookCore>);

impl ShelfBook {
    /// Open the book `id` on the shelf: reconcile its stored records with
    /// its log and rebuild its indexes.
    pub(crate) fn open(
        shared: Arc<ShelfShared>,
        id: &str,
        log: Log,
        indexes: Indexes,
    ) -> Result<Self, ShelfError> {
        let core = BookCore {
            id: id.to_string(),
            shared,
            log,
            state: Mutex::new(BookState {
                records: Vec::new(),
                by_id: HashMap::new(),
                indexes: match indexes {
                    Indexes::Defaults => SortedMerkleIndex::defaults(),
                    Indexes::None => Vec::new(),
                },
                roots: HashMap::new(),
                pending: None,
                released: false,
            }),
        };
        {
            let mut state = core.lock();
            core.recover(&mut state)?;
            for index in &mut state.indexes {
                index.leaves.clear();
            }
            let records = state.records.clone();
            for record in &records {
                core.index(&mut state, record);
            }
        }
        Ok(Self(Arc::new(core)))
    }

    /// The book's log id.
    pub fn id(&self) -> &str {
        &self.0.id
    }

    /// The number of committed records.
    pub fn size(&self) -> u64 {
        self.0.lock().records.len() as u64
    }

    /// Append `entry` as the next record (Go `Append`): seal it, keep its
    /// payloads in the artifact store (one put), store the record, append
    /// its id to the log, and index it, in that order. When the record is
    /// stored but its log append fails, it keeps its position and is
    /// committed first by the next write.
    pub fn append(&self, entry: &BookEntry) -> Result<BookRecord, ShelfError> {
        let mut state = self.0.lock();
        self.0.append_locked(&mut state, entry)
    }

    /// Commit any changed index roots as records, then checkpoint the log
    /// at the shelf's clock (Go `Checkpoint`).
    pub fn checkpoint(&self) -> Result<Checkpoint, ShelfError> {
        let mut state = self.0.lock();
        self.0.checkpoint_locked(&mut state, None)
    }

    /// [`ShelfBook::checkpoint`] at `at`: a cadence cuts every log at the
    /// tick's minute (Go `CheckpointAt`).
    pub fn checkpoint_at(&self, at: Time) -> Result<Checkpoint, ShelfError> {
        let mut state = self.0.lock();
        self.0.checkpoint_locked(&mut state, Some(at))
    }

    /// Every checkpoint this book has kept, oldest first.
    pub fn checkpoints(&self) -> Result<Vec<Checkpoint>, ShelfError> {
        let rows = store::checkpoints(&self.0.shared.db(), &self.0.id)?;
        rows.iter().map(|r| Checkpoint::from_json(r)).collect()
    }

    /// The record `record_id`.
    pub fn get(&self, record_id: &str) -> Result<BookRecord, ShelfError> {
        let state = self.0.lock();
        state
            .by_id
            .get(record_id)
            .map(|&i| state.records[i].clone())
            .ok_or_else(|| ShelfError::NotFound(format!("record {record_id}")))
    }

    /// Every committed record, in seq order.
    pub fn records(&self) -> Vec<BookRecord> {
        self.0.lock().records.clone()
    }
}

impl BookCore {
    pub(crate) fn lock(&self) -> MutexGuard<'_, BookState> {
        self.state.lock().unwrap_or_else(|p| p.into_inner())
    }

    fn now(&self) -> Time {
        self.shared.now()
    }

    /// Go `recover`.
    fn recover(&self, state: &mut BookState) -> Result<(), ShelfError> {
        let stored = store::records(&self.shared.db(), &self.id)?;
        for (i, sr) in stored.iter().enumerate() {
            if sr.seq != i as u64 + 1 {
                return Err(ShelfError::Corrupt(format!(
                    "stored records are not dense at seq {}",
                    sr.seq
                )));
            }
            let record = decode_stored(sr)?;
            if record.header.get("book_id").and_then(Value::as_str) != Some(self.id.as_str()) {
                return Err(ShelfError::Corrupt(format!(
                    "record {} belongs to another book",
                    record.record_id
                )));
            }
            state
                .by_id
                .insert(record.record_id.clone(), state.records.len());
            state.records.push(record);
        }
        let committed = self.log.size()?;
        if committed > state.records.len() as u64 {
            return Err(ShelfError::Corrupt(format!(
                "substrate holds {committed} identities, store holds {} records",
                state.records.len()
            )));
        }
        let mut after = 0;
        while after < committed {
            let entries = self.log.entries(after, 0)?;
            if entries.is_empty() {
                return Err(ShelfError::Corrupt(format!(
                    "substrate scan stopped at {after} of {committed}"
                )));
            }
            for entry in entries {
                if entry.seq > committed
                    || state.records[entry.seq as usize - 1].record_id != hex::encode(&entry.value)
                {
                    return Err(ShelfError::Corrupt(format!(
                        "substrate seq {} is not stored record {}",
                        entry.seq,
                        state.records[entry.seq as usize - 1].record_id
                    )));
                }
                after = entry.seq;
            }
        }
        for i in committed as usize..state.records.len() {
            let record = state.records[i].clone();
            self.commit(&record)?;
        }
        for record in &state.records {
            if record.record_type() == record_types::INDEX_ROOT {
                let s = record
                    .header
                    .get("statement")
                    .cloned()
                    .unwrap_or(Value::Null);
                let root = index_root(&s).ok_or_else(|| {
                    ShelfError::Corrupt(format!("index_root record {}", record.record_id))
                })?;
                let name = root["index"].as_str().unwrap_or_default().to_string();
                state.roots.insert(name, root);
            }
        }
        Ok(())
    }

    /// Append a stored record's id to the log at the time it says it was
    /// committed, and require it to land at its seq.
    fn commit(&self, record: &BookRecord) -> Result<(), ShelfError> {
        let at = record
            .header
            .get("committed_at")
            .and_then(Value::as_str)
            .and_then(Time::parse)
            .ok_or_else(|| ShelfError::Corrupt(format!("committed_at of {}", record.record_id)))?;
        let value = hex::decode(&record.record_id)
            .map_err(|_| ShelfError::Corrupt(format!("record id {}", record.record_id)))?;
        let seq = self.log.append(&value, at)?;
        if seq != record.seq {
            return Err(ShelfError::Corrupt(format!(
                "record {} landed at {seq}, not {}",
                record.record_id, record.seq
            )));
        }
        Ok(())
    }

    fn flush_pending(&self, state: &mut BookState) -> Result<(), ShelfError> {
        let Some(record) = state.pending.clone() else {
            return Ok(());
        };
        self.commit(&record).map_err(|e| {
            ShelfError::Log(format!(
                "record {} is stored but not yet committed: {e}",
                record.record_id
            ))
        })?;
        state.pending = None;
        self.admit(state, record);
        Ok(())
    }

    fn admit(&self, state: &mut BookState, record: BookRecord) {
        self.index(state, &record);
        state
            .by_id
            .insert(record.record_id.clone(), state.records.len());
        state.records.push(record);
    }

    fn index(&self, state: &mut BookState, record: &BookRecord) {
        // A root record is proven by the log, not by the index it commits;
        // indexing it would make every root commit the next one stale.
        if record.record_type() == record_types::INDEX_ROOT {
            return;
        }
        for index in &mut state.indexes {
            index.add(record);
        }
    }

    fn append_locked(
        &self,
        state: &mut BookState,
        entry: &BookEntry,
    ) -> Result<BookRecord, ShelfError> {
        if state.released {
            return Err(ShelfError::Released);
        }
        self.flush_pending(state)?;
        let seq = state.records.len() as u64 + 1;
        let committed_at = self.now().truncate(Duration::from_micros(1));
        let mut header = seal::header(
            &self.id,
            seq,
            &committed_at.rfc3339_nano(),
            &AskEntry {
                record_type: entry.record_type.clone(),
                epistemic_type: entry.epistemic_type,
                links: entry.links.clone(),
                statement: entry.statement.clone(),
            },
        )?;
        header.event_time_claim = entry.event_time_claim.map(Time::rfc3339_nano);
        header.subject_ref = entry.subject_ref.clone().filter(|s| !s.is_empty());
        header.principal_ref = entry.principal_ref.clone().filter(|s| !s.is_empty());
        header.counterparty_ref = entry.counterparty_ref.clone().filter(|s| !s.is_empty());
        if let Some(key) = entry
            .correlation
            .keys()
            .find(|k| !self.shared.correlation_members.contains(k))
        {
            return Err(ShelfError::Invalid(format!(
                "correlation member {key:?} is not one the profile writes"
            )));
        }
        header.correlation = entry
            .correlation
            .iter()
            .filter(|(_, v)| !v.is_empty())
            .map(|(k, v)| (k.clone(), v.clone()))
            .collect();
        header.payload_commitments = entry
            .payloads
            .iter()
            .map(|p| hex::encode(Sha256::digest(p)))
            .collect();
        if !header.payload_commitments.is_empty() {
            header.retention_state = Some(RetentionState::Available);
        }
        let developer = if self.shared.developer.is_empty() {
            DEFAULT_DEVELOPER
        } else {
            &self.shared.developer
        };
        let sealed = seal::seal_record(
            &header,
            &Producer {
                operator: &self.shared.operator,
                developer,
                signer: self.shared.signer.as_ref(),
            },
        )?;
        // Originals are written after sealing, with the record they belong
        // to, in one put: the artifact store never holds bytes no record
        // commits.
        if !entry.payloads.is_empty() {
            let artifacts = entry
                .payloads
                .iter()
                .enumerate()
                .map(|(i, p)| Artifact {
                    name: format!("payload-{}", i + 1),
                    binding: String::new(),
                    content: Some(p.clone()),
                    state: Held::Present,
                    content_sha256: String::new(),
                })
                .collect();
            self.shared
                .originals
                .put(&ArtifactRecord {
                    capsule_id: sealed.record_id.clone(),
                    capsule: sealed.capsule.clone(),
                    producer_envelope: sealed.envelope.clone(),
                    artifacts,
                })
                .map_err(|e| ShelfError::Artifact(format!("store originals: {e}")))?;
        }
        let stored = StoredRecord {
            seq,
            record_id: sealed.record_id.clone(),
            header: sealed.header.clone(),
            capsule: sealed.capsule.clone(),
            envelope: sealed.envelope.clone(),
        };
        store::put_record(&mut self.shared.db(), &self.id, &stored)?;
        let record = BookRecord {
            seq,
            record_id: sealed.record_id,
            header: header_map(&sealed.header)?,
            header_bytes: sealed.header,
            capsule: sealed.capsule,
            envelope: sealed.envelope,
            body: sealed.body,
        };
        if let Err(e) = self.commit(&record) {
            state.pending = Some(record.clone());
            return Err(ShelfError::Log(format!(
                "record {} is stored but not yet committed; it keeps seq {seq} and is retried first: {e}",
                record.record_id
            )));
        }
        self.admit(state, record.clone());
        Ok(record)
    }

    pub(crate) fn checkpoint_locked(
        &self,
        state: &mut BookState,
        at: Option<Time>,
    ) -> Result<Checkpoint, ShelfError> {
        if state.released {
            return Err(ShelfError::Released);
        }
        self.flush_pending(state)?;
        self.checkpoint_roots(state, at)
    }

    fn checkpoint_roots(
        &self,
        state: &mut BookState,
        at: Option<Time>,
    ) -> Result<Checkpoint, ShelfError> {
        for i in 0..state.indexes.len() {
            let root = state.indexes[i].root();
            let name = root["index"].as_str().unwrap_or_default().to_string();
            if state.roots.get(&name) == Some(&root) {
                continue;
            }
            let mut statement = root.clone();
            statement["as_of_seq"] = json!(state.records.len());
            let mut entry = BookEntry::new(record_types::INDEX_ROOT, EpistemicType::DerivedMetric);
            entry.statement = statement;
            self.append_locked(state, &entry)?;
            state.roots.insert(name, root);
        }
        let when = at.unwrap_or_else(|| self.now());
        let cp = self.log.checkpoint(when)?;
        store::put_checkpoint(&mut self.shared.db(), &self.id, &cp.id, &cp.to_json())?;
        Ok(cp)
    }
}

/// `{index, root, size}` of an `index_root` statement (Go's `IndexRoot`).
fn index_root(statement: &Value) -> Option<Value> {
    Some(json!({
        "index": statement.get("index")?.as_str()?,
        "root": statement.get("root")?.as_str()?,
        "size": statement.get("size")?.as_u64()?,
    }))
}

fn header_map(header: &[u8]) -> Result<Map<String, Value>, ShelfError> {
    match serde_json::from_slice(header) {
        Ok(Value::Object(m)) => Ok(m),
        _ => Err(ShelfError::Corrupt(
            "a record header is not a JSON object".into(),
        )),
    }
}

/// The top-level Capsule member through which a capsule its caller sealed
/// carries its own record header (Go `RecordHeaderMember`).
pub const RECORD_HEADER_MEMBER: &str = "evidence_record_header";

/// Re-derive a record from its durable form and check that the header bytes
/// are canonical and are what the capsule commits (Go `decodeStored`): as
/// its `agent_input_digest` (a record the book sealed) or carried in
/// [`RECORD_HEADER_MEMBER`] (a record its caller sealed), never both.
pub(crate) fn decode_stored(sr: &StoredRecord) -> Result<BookRecord, ShelfError> {
    let corrupt = |why: &str| ShelfError::Corrupt(format!("record {}: {why}", sr.record_id));
    let header_value: Value =
        serde_json::from_slice(&sr.header).map_err(|_| corrupt("header is not JSON"))?;
    if canonical::jcs(&header_value).ok().as_deref() != Some(sr.header.as_slice()) {
        return Err(corrupt("header is not canonical"));
    }
    let header = match header_value {
        Value::Object(m) => m,
        _ => return Err(corrupt("header is not an object")),
    };
    if header.get("seq").and_then(Value::as_u64) != Some(sr.seq) {
        return Err(corrupt("header seq disagrees with store"));
    }
    let capsule: Value =
        serde_json::from_slice(&sr.capsule).map_err(|_| corrupt("capsule is not JSON"))?;
    // The capsule verifies as Go's capsule-emit `VerifyCapsule` checks it: AAC
    // format 4 under JCS only, then Class 1 verification.
    if capsule.get("format_version").and_then(Value::as_str) != Some(seal::FORMAT_VERSION)
        || capsule.get("canonicalization_id").and_then(Value::as_str)
            != Some(canonical::CANONICALIZATION_JCS)
    {
        return Err(corrupt("capsule is not AAC format 4 under JCS"));
    }
    let strict = agent_action_capsule::verify::decode_capsule_json(&sr.capsule)
        .map_err(|_| corrupt("capsule is not strict JSON"))?;
    let class1 = agent_action_capsule::verify::verify(&strict, None);
    if !class1.ok {
        let codes: Vec<&str> = class1.findings.iter().map(|f| f.code.as_str()).collect();
        return Err(corrupt(&format!(
            "capsule fails Class 1 verification: {}",
            codes.join(", ")
        )));
    }
    let id = canonical::compute_capsule_id(&capsule).map_err(|_| corrupt("capsule"))?;
    if capsule.get("capsule_id").and_then(Value::as_str) != Some(sr.record_id.as_str())
        || id != sr.record_id
    {
        return Err(corrupt("capsule does not verify as that id"));
    }
    let header_digest = hex::encode(Sha256::digest(&sr.header));
    let attestation = capsule.get("model_attestation"); // vocabulary-gate: AAC capsule member
    let committed = attestation
        .and_then(|a| a.pointer("/compute_attestation/agent_input_digest"))
        .and_then(Value::as_str)
        == Some(header_digest.as_str());
    let inline = capsule.get(RECORD_HEADER_MEMBER);
    match (inline, committed) {
        (Some(_), true) => return Err(corrupt("capsule commits its header twice")),
        (Some(carried), false) => {
            if canonical::jcs(carried).ok().as_deref() != Some(sr.header.as_slice()) {
                return Err(corrupt("capsule carries a header other than its record's"));
            }
        }
        (None, false) => return Err(corrupt("capsule does not commit its header")),
        (None, true) => {}
    }
    let mut body = capsule;
    if let Some(m) = body.as_object_mut() {
        m.remove("capsule_id");
        for field in canonical::LOCAL_ONLY_FIELDS {
            m.remove(*field);
        }
    }
    Ok(BookRecord {
        seq: sr.seq,
        record_id: sr.record_id.clone(),
        header,
        header_bytes: sr.header.clone(),
        capsule: sr.capsule.clone(),
        envelope: sr.envelope.clone(),
        body: canonical::jcs(&body).map_err(|_| corrupt("capsule"))?,
    })
}

impl ShelfBook {
    fn held(&self, i: usize) -> Option<Record> {
        let state = self.0.lock();
        state.records.get(i).map(|r| Record {
            leaf_index: r.seq - 1,
            digest: r.record_id.clone(),
            body: r.body.clone(),
        })
    }
}

impl RecordSource for ShelfBook {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        let i = *self.0.lock().by_id.get(digest)?;
        self.held(i)
    }

    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        self.held(usize::try_from(leaf_index).ok()?)
    }

    fn correlation(&self, id: &str) -> Vec<u64> {
        // A record carrying the profile's first correlation member is held
        // under it alone; only one carrying none is held under its
        // subject_ref (Go's correlation index, keyed by the profile).
        let key = self
            .0
            .shared
            .correlation_members
            .first()
            .map(String::as_str);
        self.0
            .lock()
            .records
            .iter()
            .filter(|r| {
                let held = key
                    .and_then(|k| r.header.get("correlation")?.get(k))
                    .and_then(Value::as_str)
                    .filter(|e| !e.is_empty())
                    .or_else(|| r.subject_ref());
                held == Some(id)
            })
            .map(|r| r.seq - 1)
            .collect()
    }

    fn citing(&self, half: &str) -> Vec<u64> {
        self.0
            .lock()
            .records
            .iter()
            .filter(|r| r.links_to(LinkType::Cites).contains(&half))
            .map(|r| r.seq - 1)
            .collect()
    }

    fn header_of(&self, record: &Record) -> Option<Map<String, Value>> {
        let state = self.0.lock();
        let held = state
            .records
            .get(usize::try_from(record.leaf_index).ok()?)?;
        (held.record_id == record.digest).then(|| held.header.clone())
    }
}

fn substrate_error(e: ShelfError) -> SubstrateError {
    SubstrateError::Store(e.to_string())
}

impl CheckpointedLog for ShelfBook {
    fn leaf_count(&self) -> Result<u64, SubstrateError> {
        Ok(self.size())
    }

    fn stated_checkpoints(&self) -> Result<Vec<StatedCheckpoint>, SubstrateError> {
        let checkpoints = self.checkpoints().map_err(substrate_error)?;
        checkpoints
            .into_iter()
            .map(|cp| {
                let verified = cll::checkpoint::verify_checkpoint_cose_offline(&cp.statement);
                let decoded = verified.decoded.filter(|_| verified.ok).ok_or_else(|| {
                    SubstrateError::Store("a kept checkpoint does not verify".into())
                })?;
                Ok(StatedCheckpoint {
                    checkpoint: LogCheckpoint::from_log(decoded.to_checkpoint_record()),
                    statement: cp.statement,
                })
            })
            .collect()
    }

    fn inclusion_path(
        &self,
        leaf_index: u64,
        mmr_size: u64,
    ) -> Result<Vec<[u8; 32]>, SubstrateError> {
        self.0
            .log
            .inclusion_path(leaf_index, mmr_size)
            .map_err(substrate_error)
    }

    fn consistency_parts(&self, from: u64, to: u64) -> Result<ConsistencyParts, SubstrateError> {
        self.0
            .log
            .consistency_parts(from, to)
            .map_err(substrate_error)
    }
}

impl AskBook for ShelfBook {
    fn len(&self) -> u64 {
        self.size()
    }

    fn now(&self) -> String {
        self.0.now().rfc3339_nano()
    }

    fn seal(&mut self, entry: &ask::Entry) -> Result<Sealed, SealError> {
        let mut book_entry = BookEntry::new(&entry.record_type, entry.epistemic_type);
        book_entry.links = entry.links.clone();
        book_entry.statement = entry.statement.clone();
        let record = self
            .append(&book_entry)
            .map_err(|e| SealError(e.to_string()))?;
        Ok(Sealed {
            committed_at: record
                .header
                .get("committed_at")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_string(),
            record_id: record.record_id,
        })
    }

    fn checkpoint(&mut self) -> Result<(), SealError> {
        ShelfBook::checkpoint(self)
            .map(drop)
            .map_err(|e| SealError(e.to_string()))
    }
}
