// SPDX-License-Identifier: Apache-2.0
//! The book the interop programs run: an in-memory record store over a
//! `CllSubstrate` in a temporary directory, sealing every record with
//! `evidencebook::seal`, and committing its index roots and checkpoints
//! exactly when the Go book does (evidencebook Go `book.go`,
//! `checkpointAtLocked`), so a run's records, anchors and replies are Go's
//! byte for byte. It is test code for those programs, not a store to reuse.

use std::cell::Cell;
use std::collections::BTreeMap;
use std::rc::Rc;

use ed25519_dalek::SigningKey;
use evidencebook::ask::{Book, Entry, SealError, Sealed};
use evidencebook::record::{record_types, EpistemicType, Header, LinkType};
use evidencebook::seal::{self, Producer, SealedRecord, DEFAULT_DEVELOPER};
use evidencebook::store::{CheckpointedLog, ConsistencyParts, Record, RecordSource};
use evidencebook::substrate::{CllSubstrate, StatedCheckpoint, SubstrateError};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

/// An Ed25519 test key whose seed is the SHA-256 of a public label.
pub fn seeded(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    hex::encode(Sha256::digest(bytes))
}

/// A clock several books share: seconds since the Unix epoch.
#[derive(Clone)]
pub struct Clock(Rc<Cell<i64>>);

impl Clock {
    pub fn at(seconds: i64) -> Clock {
        Clock(Rc::new(Cell::new(seconds)))
    }
    /// One second after the last reading: the Go programs' `now`.
    pub fn tick(&self) -> i64 {
        self.0.set(self.0.get() + 1);
        self.0.get()
    }
    pub fn get(&self) -> i64 {
        self.0.get()
    }
    pub fn advance(&self, seconds: i64) {
        self.0.set(self.0.get() + seconds);
    }
}

/// Seconds since the Unix epoch of a civil UTC date (H. Hinnant's algorithm).
pub fn epoch(year: i64, month: i64, day: i64) -> i64 {
    let y = if month <= 2 { year - 1 } else { year };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (month + 9) % 12;
    let doy = (153 * mp + 2) / 5 + day - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    (era * 146_097 + doe - 719_468) * 86_400
}

/// `seconds` as RFC 3339 UTC, whole seconds (Go's RFC3339Nano of a whole second).
pub fn rfc3339(seconds: i64) -> String {
    let days = seconds.div_euclid(86_400);
    let secs = seconds.rem_euclid(86_400);
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z",
        secs / 3600,
        secs / 60 % 60,
        secs % 60
    )
}

/// One of Go's default authenticated indexes (`SortedMerkleIndex`): an RFC
/// 6962-shaped Merkle tree over sorted `(key, record id)` leaves.
struct SortedMerkleIndex {
    name: &'static str,
    key_of: fn(&Header, &str) -> Option<String>,
    leaves: Vec<(String, String)>,
}

impl SortedMerkleIndex {
    fn add(&mut self, header: &Header, record_id: &str) {
        let Some(key) = (self.key_of)(header, record_id) else {
            return;
        };
        let leaf = (key, record_id.to_string());
        // Go orders strings by their bytes, as Rust does.
        if let Err(at) = self.leaves.binary_search(&leaf) {
            self.leaves.insert(at, leaf);
        }
    }

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

/// One record as the book holds it.
pub struct Held {
    pub record: Record,
    pub header: Header,
    pub sealed: SealedRecord,
}

pub struct MemBook {
    pub book_id: String,
    operator: String,
    signer: SigningKey,
    log_id: String,
    log_key: SigningKey,
    clock: Clock,
    substrate: CllSubstrate,
    _dir: tempfile::TempDir,
    pub records: Vec<Held>,
    indexes: Vec<SortedMerkleIndex>,
    roots: BTreeMap<String, Value>,
}

impl MemBook {
    pub fn open(
        book_id: &str,
        operator: &str,
        signer: SigningKey,
        log_id: &str,
        log_key: SigningKey,
        clock: Clock,
    ) -> MemBook {
        let dir = tempfile::tempdir().expect("a temporary directory");
        let (substrate, _) = CllSubstrate::open(
            &dir.path().join("mmr_nodes.dat"),
            &dir.path().join("checkpoints.jsonl"),
        )
        .expect("a new substrate");
        MemBook {
            book_id: book_id.into(),
            operator: operator.into(),
            signer,
            log_id: log_id.into(),
            log_key,
            clock,
            substrate,
            _dir: dir,
            records: Vec::new(),
            indexes: vec![
                SortedMerkleIndex {
                    name: "record_id",
                    key_of: |_, id| Some(id.to_string()),
                    leaves: Vec::new(),
                },
                SortedMerkleIndex {
                    name: "subject_ref",
                    key_of: |h, _| h.subject_ref.clone().filter(|s| !s.is_empty()),
                    leaves: Vec::new(),
                },
            ],
            roots: BTreeMap::new(),
        }
    }

    pub fn signer(&self) -> &SigningKey {
        &self.signer
    }

    /// Append a record: `fill` sets the members `Entry` does not carry.
    pub fn append_with(
        &mut self,
        entry: &Entry,
        fill: impl FnOnce(&mut Header),
    ) -> Result<Sealed, SealError> {
        let committed_at = rfc3339(self.clock.tick());
        let mut header = seal::header(
            &self.book_id,
            self.records.len() as u64 + 1,
            &committed_at,
            entry,
        )
        .map_err(|e| SealError(e.to_string()))?;
        fill(&mut header);
        let producer = Producer {
            operator: &self.operator,
            developer: DEFAULT_DEVELOPER,
            signer: &self.signer,
        };
        let sealed = seal::seal_record(&header, &producer).map_err(|e| SealError(e.to_string()))?;
        sealed
            .verify(&self.signer.verifying_key().to_bytes())
            .map_err(|e| SealError(e.to_string()))?;
        let id: [u8; 32] = hex::decode(&sealed.record_id).unwrap().try_into().unwrap();
        let leaf_index = self
            .substrate
            .append_leaf(&id)
            .map_err(|e| SealError(e.to_string()))?;
        if header.record_type != record_types::INDEX_ROOT {
            // A root record is proven by the log, not by the index it commits.
            for index in &mut self.indexes {
                index.add(&header, &sealed.record_id);
            }
        }
        let committed_at = header.committed_at.clone();
        self.records.push(Held {
            record: Record {
                leaf_index,
                digest: sealed.record_id.clone(),
                body: sealed.body.clone(),
            },
            header,
            sealed: sealed.clone(),
        });
        Ok(Sealed {
            record_id: sealed.record_id,
            committed_at,
        })
    }

    /// Commit any changed index roots, then checkpoint at the clock,
    /// truncated to the millisecond, when the log grew since the last one.
    pub fn cut(&mut self) -> Result<StatedCheckpoint, SealError> {
        for i in 0..self.indexes.len() {
            let root = self.indexes[i].root();
            let name = root["index"].as_str().unwrap().to_string();
            if self.roots.get(&name) == Some(&root) {
                continue;
            }
            let mut statement = root.clone();
            statement["as_of_seq"] = json!(self.records.len());
            self.append_with(
                &Entry {
                    record_type: record_types::INDEX_ROOT.into(),
                    epistemic_type: EpistemicType::DerivedMetric,
                    links: Vec::new(),
                    statement,
                },
                |_| {},
            )?;
            self.roots.insert(name, root);
        }
        let when = rfc3339(self.clock.tick());
        let count = self
            .substrate
            .leaf_count()
            .map_err(|e| SealError(e.to_string()))?;
        let grown = self
            .substrate
            .last_checkpoint()
            .is_none_or(|cp| cp.leaf_count().ok() != Some(count));
        if grown {
            let prepared = self
                .substrate
                .prepare_checkpoint(count, &self.log_id, &when, &self.log_key)
                .map_err(|e| SealError(e.to_string()))?;
            self.substrate
                .commit_checkpoint(prepared)
                .map_err(|e| SealError(e.to_string()))?;
        }
        let mut stated = self
            .substrate
            .stated_checkpoints()
            .map_err(|e| SealError(e.to_string()))?;
        stated
            .pop()
            .ok_or_else(|| SealError("no checkpoint".into()))
    }

    pub fn checkpoints(&self) -> Vec<StatedCheckpoint> {
        self.substrate
            .stated_checkpoints()
            .expect("stated checkpoints")
    }

    pub fn header_value(&self, leaf: usize) -> Map<String, Value> {
        match serde_json::to_value(&self.records[leaf].header).unwrap() {
            Value::Object(map) => map,
            _ => unreachable!(),
        }
    }
}

impl RecordSource for MemBook {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        self.records
            .iter()
            .find(|h| h.record.digest == digest)
            .map(|h| h.record.clone())
    }
    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        self.records
            .get(leaf_index as usize)
            .map(|h| h.record.clone())
    }
    fn correlation(&self, id: &str) -> Vec<u64> {
        // A record carrying correlation.exchange_id is held under it alone;
        // only one carrying none is held under its subject_ref (Go's
        // correlation index).
        self.records
            .iter()
            .filter(|h| {
                let held = h
                    .header
                    .correlation
                    .get("exchange_id")
                    .filter(|e| !e.is_empty())
                    .or(h.header.subject_ref.as_ref());
                held.map(String::as_str) == Some(id)
            })
            .map(|h| h.record.leaf_index)
            .collect()
    }
    fn citing(&self, half: &str) -> Vec<u64> {
        self.records
            .iter()
            .filter(|h| h.header.links_to(LinkType::Cites).contains(&half))
            .map(|h| h.record.leaf_index)
            .collect()
    }
    fn header_of(&self, record: &Record) -> Option<Map<String, Value>> {
        (record.leaf_index < self.records.len() as u64)
            .then(|| self.header_value(record.leaf_index as usize))
    }
}

impl CheckpointedLog for MemBook {
    fn leaf_count(&self) -> Result<u64, SubstrateError> {
        self.substrate.leaf_count()
    }
    fn stated_checkpoints(&self) -> Result<Vec<StatedCheckpoint>, SubstrateError> {
        self.substrate.stated_checkpoints()
    }
    fn inclusion_path(
        &self,
        leaf_index: u64,
        mmr_size: u64,
    ) -> Result<Vec<[u8; 32]>, SubstrateError> {
        self.substrate.inclusion_path(leaf_index, mmr_size)
    }
    fn consistency_parts(&self, from: u64, to: u64) -> Result<ConsistencyParts, SubstrateError> {
        self.substrate.consistency_parts(from, to)
    }
}

impl Book for MemBook {
    fn len(&self) -> u64 {
        self.records.len() as u64
    }
    fn now(&self) -> String {
        rfc3339(self.clock.tick())
    }
    fn seal(&mut self, entry: &Entry) -> Result<Sealed, SealError> {
        self.append_with(entry, |_| {})
    }
    fn checkpoint(&mut self) -> Result<(), SealError> {
        self.cut().map(|_| ())
    }
}

/// Go's `json.MarshalIndent(v, "", indent)`: members in the order given,
/// `&`, `<`, `>`, U+2028 and U+2029 escaped, and every control character
/// but `\n`, `\r` and `\t` as `\u00XX`.
pub fn go_marshal_indent(value: &Value, indent: &str) -> String {
    let mut out = String::new();
    write_value(value, indent, 0, &mut out);
    out
}

fn write_value(value: &Value, indent: &str, depth: usize, out: &mut String) {
    let pad = |out: &mut String, depth: usize| {
        out.push('\n');
        for _ in 0..depth {
            out.push_str(indent);
        }
    };
    match value {
        Value::Array(items) if !items.is_empty() => {
            out.push('[');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                pad(out, depth + 1);
                write_value(item, indent, depth + 1, out);
            }
            pad(out, depth);
            out.push(']');
        }
        Value::Object(members) if !members.is_empty() => {
            out.push('{');
            for (i, (k, v)) in members.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                pad(out, depth + 1);
                write_string(k, out);
                out.push_str(": ");
                write_value(v, indent, depth + 1, out);
            }
            pad(out, depth);
            out.push('}');
        }
        Value::String(s) => write_string(s, out),
        other => out.push_str(&other.to_string()),
    }
}

fn write_string(s: &str, out: &mut String) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '&' | '<' | '>' | '\u{2028}' | '\u{2029}' => {
                out.push_str(&format!("\\u{:04x}", c as u32))
            }
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
}
