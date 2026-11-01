//! One CLL log in the shelf's file: its SQLite backend and checkpoint runner
//! (Go `substrate_cll.go`, `CLLSubstrate` over `cllsqlite`).

use super::time::Time;
use super::ShelfError;
use crate::store::ConsistencyParts;
use base64::engine::general_purpose::STANDARD;
use base64::Engine as _;
use cll::log::sqlite::SqliteStore;
use cll::log::{
    AppendInput, Backend, CheckpointStateStore, EntrySource, EntryStore, Timestamp, MAX_SCAN_LIMIT,
};
use cll::mmr::{self, Hash, MemoryNodeStore, NodeAppender};
use cll::runner::{CheckpointRunner, RunnerConfig};
use ed25519_dalek::SigningKey;
use rusqlite::{Connection, OpenFlags};
use serde_json::{json, Map, Value};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

/// The kind a shelf checkpoint and its proofs are labelled with.
pub const SUBSTRATE_KIND_CLL: &str = "cll";

/// A checkpoint as the shelf keeps it (Go `Checkpoint`): what its signed
/// statement states, and the statement. Its JSON ([`Checkpoint::to_json`])
/// is what `book_checkpoints.checkpoint` holds, member for member as Go
/// writes it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Checkpoint {
    pub kind: String,
    pub log_id: String,
    /// `"<root hex>:<mmr size>"`.
    pub id: String,
    /// The leaf count at `tree_size`.
    pub entries: u64,
    pub root: String,
    /// The MMR size.
    pub tree_size: u64,
    pub key_id: String,
    /// The statement's time, in Go's `time.RFC3339Nano` form.
    pub issued_at: String,
    /// The signed COSE statement.
    pub statement: Vec<u8>,
}

impl Checkpoint {
    /// Parse and signature-check a CLL checkpoint statement (Go
    /// `checkpointFromStatement`). Its signature is checked with strict
    /// Ed25519 first, then by the log's own offline verifier.
    pub fn from_statement(statement: &[u8]) -> Result<Self, ShelfError> {
        if !crate::ed25519::statement_verifies(statement) {
            return Err(ShelfError::Corrupt(
                "checkpoint signature does not verify".into(),
            ));
        }
        let verified = cll::checkpoint::verify_checkpoint_cose_offline(statement);
        let decoded = match verified.decoded {
            Some(d) if verified.ok => d,
            _ => {
                return Err(ShelfError::Corrupt(format!(
                    "checkpoint statement: {}",
                    verified.errors.join("; ")
                )))
            }
        };
        let entries = mmr::leaf_count(decoded.mmr_size).map_err(|_| {
            ShelfError::Corrupt(format!(
                "checkpoint size {} is not a complete MMR",
                decoded.mmr_size
            ))
        })?;
        let issued_at = Time::parse(&decoded.timestamp)
            .ok_or_else(|| ShelfError::Corrupt("checkpoint time".into()))?
            .rfc3339_nano();
        Ok(Self {
            kind: SUBSTRATE_KIND_CLL.into(),
            id: format!("{}:{}", decoded.root, decoded.mmr_size),
            log_id: decoded.log_id,
            entries,
            root: decoded.root,
            tree_size: decoded.mmr_size,
            key_id: decoded.key_id,
            issued_at,
            statement: statement.to_vec(),
        })
    }

    /// Whether `seq` is committed under this checkpoint.
    pub fn covers(&self, seq: u64) -> bool {
        seq >= 1 && seq <= self.entries
    }

    /// Go's `json.Marshal` of its `Checkpoint`.
    pub fn to_json(&self) -> Vec<u8> {
        let mut m = Map::new();
        m.insert("kind".into(), json!(self.kind));
        m.insert("log_id".into(), json!(self.log_id));
        m.insert("id".into(), json!(self.id));
        m.insert("entries".into(), json!(self.entries));
        if !self.root.is_empty() {
            m.insert("root".into(), json!(self.root));
        }
        if self.tree_size != 0 {
            m.insert("tree_size".into(), json!(self.tree_size));
        }
        if !self.key_id.is_empty() {
            m.insert("key_id".into(), json!(self.key_id));
        }
        m.insert("issued_at".into(), json!(self.issued_at));
        if !self.statement.is_empty() {
            m.insert("statement".into(), json!(STANDARD.encode(&self.statement)));
        }
        super::gojson::marshal(&Value::Object(m)).into_bytes()
    }

    /// Read a checkpoint back from its JSON, taking every field from its
    /// signed statement.
    pub fn from_json(encoded: &[u8]) -> Result<Self, ShelfError> {
        let v: Value = serde_json::from_slice(encoded)
            .map_err(|e| ShelfError::Corrupt(format!("a stored checkpoint: {e}")))?;
        let statement = v
            .get("statement")
            .and_then(Value::as_str)
            .and_then(|s| STANDARD.decode(s).ok())
            .ok_or_else(|| ShelfError::Corrupt("a stored checkpoint has no statement".into()))?;
        Self::from_statement(&statement)
    }
}

/// One entry of a log.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct LogEntry {
    pub seq: u64,
    pub value: Vec<u8>,
    pub appended_at: Timestamp,
}

/// A CLL log in the shelf's file and its checkpoint runner.
pub(crate) struct Log {
    pub(crate) log_id: String,
    pub(crate) store: Arc<SqliteStore>,
    runner: CheckpointRunner<SqliteStore, SigningKey>,
    /// Held while a checkpoint is cut, and by a host log's write.
    pub(crate) mu: Mutex<()>,
}

impl Log {
    /// Open the log `log_id` in the file at `path`, provisioning it only
    /// when `provision` is set and the file holds nothing of it (Go
    /// `openCLLSQLite`). Each checkpoint queues a pending delivery to every
    /// witness in `witness_ids`.
    pub(crate) fn open(
        path: &Path,
        log_id: &str,
        key: &SigningKey,
        witness_ids: &[String],
        provision: bool,
    ) -> Result<Self, ShelfError> {
        cll::log::validate_identifier(log_id)
            .map_err(|e| ShelfError::Invalid(format!("log id: {e}")))?;
        let store = match SqliteStore::open(path, log_id) {
            Ok(store) => store,
            Err(err) if provision => {
                // Provision only a log the file holds nothing of: a log
                // with any state that fails to open is reported, never
                // re-provisioned over.
                if !log_absent(path, log_id)? {
                    return Err(ShelfError::Log(format!("open CLL log {log_id}: {err}")));
                }
                SqliteStore::init(path, log_id)
                    .map_err(|e| ShelfError::Log(format!("init CLL log {log_id}: {e}")))?;
                SqliteStore::open(path, log_id)
                    .map_err(|e| ShelfError::Log(format!("open CLL log {log_id}: {e}")))?
            }
            Err(err) => return Err(ShelfError::Log(format!("open log {log_id}: {err}"))),
        };
        let store = Arc::new(store);
        let mut config = RunnerConfig::new(log_id);
        // The book, not the runner, decides when to checkpoint: any pending
        // entry is due the moment the book asks.
        config.cadence_entries = 1;
        config.cadence_age = Duration::from_secs(3600);
        config.scan_limit = MAX_SCAN_LIMIT;
        config.witness_ids = witness_ids.to_vec();
        let runner = CheckpointRunner::new(config, store.clone(), key.clone())
            .map_err(|e| ShelfError::Log(e.to_string()))?;
        Ok(Self {
            log_id: log_id.to_string(),
            store,
            runner,
            mu: Mutex::new(()),
        })
    }

    /// Append `value` at `at`; its seq (an identity already present keeps
    /// its original seq).
    pub(crate) fn append(&self, value: &[u8], at: Time) -> Result<u64, ShelfError> {
        let result = self
            .store
            .append(&AppendInput {
                value: value.to_vec(),
                appended_at: timestamp(at),
            })
            .map_err(|e| ShelfError::Log(format!("CLL append: {e}")))?;
        Ok(result.entry.seq)
    }

    /// The entry count.
    pub(crate) fn size(&self) -> Result<u64, ShelfError> {
        let mut size = 0;
        loop {
            let batch = self.entries(size, MAX_SCAN_LIMIT)?;
            match batch.last() {
                None => return Ok(size),
                Some(last) => size = last.seq,
            }
        }
    }

    /// Entries after `after`, at most `limit`.
    pub(crate) fn entries(&self, after: u64, limit: usize) -> Result<Vec<LogEntry>, ShelfError> {
        let limit = if limit == 0 || limit > MAX_SCAN_LIMIT {
            MAX_SCAN_LIMIT
        } else {
            limit
        };
        Ok(self
            .store
            .scan_entries(after, limit)
            .map_err(|e| ShelfError::Log(e.to_string()))?
            .into_iter()
            .map(|e| LogEntry {
                seq: e.seq,
                value: e.value,
                appended_at: e.appended_at,
            })
            .collect())
    }

    /// Checkpoint everything appended so far at `at`, cutting a new
    /// checkpoint only if the log grew, and return the latest one.
    pub(crate) fn checkpoint(&self, at: Time) -> Result<Checkpoint, ShelfError> {
        let _guard = self.mu.lock().unwrap_or_else(|p| p.into_inner());
        self.checkpoint_held(at)
    }

    /// [`Log::checkpoint`] with `mu` already held.
    pub(crate) fn checkpoint_held(&self, at: Time) -> Result<Checkpoint, ShelfError> {
        while self
            .runner
            .run_once(timestamp(at))
            .map_err(|e| ShelfError::Log(format!("CLL checkpoint: {e}")))?
        {}
        self.latest()?
            .ok_or_else(|| ShelfError::NotFound("no checkpoint yet".into()))
    }

    /// The newest checkpoint, `None` when there is none.
    pub(crate) fn latest(&self) -> Result<Option<Checkpoint>, ShelfError> {
        let state = self
            .store
            .load_cll()
            .map_err(|e| ShelfError::Log(e.to_string()))?;
        state
            .checkpoint
            .map(|cp| Checkpoint::from_statement(&cp.bytes))
            .transpose()
    }

    /// The MMR over every indexed node, checked to reach `at`.
    fn tree(&self, at: &Checkpoint) -> Result<MemoryNodeStore, ShelfError> {
        if at.kind != SUBSTRATE_KIND_CLL || at.log_id != self.log_id {
            return Err(ShelfError::Invalid(
                "checkpoint is not from this log".into(),
            ));
        }
        let nodes = self.nodes()?;
        if at.tree_size > mmr::NodeReader::size(&nodes) {
            return Err(ShelfError::NotCovered(format!(
                "checkpoint size {} exceeds indexed tree {}",
                at.tree_size,
                mmr::NodeReader::size(&nodes)
            )));
        }
        if hex::encode(mmr::root_from_peaks(&peak_hashes(&nodes, at.tree_size)?)) != at.root {
            return Err(ShelfError::Corrupt(
                "checkpoint root does not match this log".into(),
            ));
        }
        Ok(nodes)
    }

    /// Every indexed MMR node.
    pub(crate) fn nodes(&self) -> Result<MemoryNodeStore, ShelfError> {
        let state = self
            .store
            .load_cll()
            .map_err(|e| ShelfError::Log(e.to_string()))?;
        let mut nodes = MemoryNodeStore::new();
        let hashes: Vec<Hash> = state
            .nodes
            .iter()
            .map(|n| {
                n.as_slice()
                    .try_into()
                    .map_err(|_| ShelfError::Corrupt("an MMR node is not 32 bytes".into()))
            })
            .collect::<Result<_, _>>()?;
        nodes.append_nodes(&hashes);
        Ok(nodes)
    }

    /// The inclusion proof of `seq` under `at`, as Go's `MMRInclusion` JSON.
    pub(crate) fn prove_inclusion(&self, seq: u64, at: &Checkpoint) -> Result<Value, ShelfError> {
        if !at.covers(seq) {
            return Err(ShelfError::NotCovered(format!(
                "seq {seq} under checkpoint {}",
                at.id
            )));
        }
        let nodes = self.tree(at)?;
        let proof = mmr::inclusion_proof(&nodes, seq - 1, at.tree_size)
            .map_err(|e| ShelfError::Log(e.to_string()))?;
        let mut m = Map::new();
        m.insert("v".into(), json!(proof.v));
        m.insert("kind".into(), json!(proof.kind));
        m.insert("size".into(), json!(proof.size));
        m.insert("leaf_index".into(), json!(proof.leaf_index));
        m.insert("witness".into(), json!(proof.witness));
        m.insert("peaks_left".into(), json!(proof.peaks_left));
        m.insert("peaks_right".into(), json!(proof.peaks_right));
        Ok(Value::Object(m))
    }

    /// The consistency proof from `older` to `newer`, as Go's
    /// `portableConsistency` JSON.
    pub(crate) fn prove_consistency(
        &self,
        older: &Checkpoint,
        newer: &Checkpoint,
    ) -> Result<Value, ShelfError> {
        if older.tree_size > newer.tree_size {
            return Err(ShelfError::Invalid(
                "older checkpoint is larger than newer".into(),
            ));
        }
        let nodes = self.tree(newer)?;
        let proof = mmr::consistency_proof(&nodes, older.tree_size, newer.tree_size)
            .map_err(|e| ShelfError::Log(e.to_string()))?;
        let mut m = Map::new();
        m.insert("v".into(), json!(proof.v));
        m.insert("kind".into(), json!(proof.kind));
        m.insert("old_size".into(), json!(proof.size_a));
        m.insert("new_size".into(), json!(proof.size_b));
        m.insert("old_peaks".into(), json!(proof.old_peaks));
        m.insert("witness".into(), json!(proof.witness));
        m.insert("new_peaks".into(), json!(proof.new_peaks));
        Ok(Value::Object(m))
    }

    /// The inclusion path of the leaf at `leaf_index` in the MMR of
    /// `mmr_size` nodes.
    pub(crate) fn inclusion_path(
        &self,
        leaf_index: u64,
        mmr_size: u64,
    ) -> Result<Vec<[u8; 32]>, ShelfError> {
        let nodes = self.nodes()?;
        let proof = mmr::inclusion_proof(&nodes, leaf_index, mmr_size)
            .map_err(|e| ShelfError::Log(e.to_string()))?;
        hashes(&proof.witness)
    }

    /// The parts of the consistency proof from `from` to `to` nodes.
    pub(crate) fn consistency_parts(
        &self,
        from: u64,
        to: u64,
    ) -> Result<ConsistencyParts, ShelfError> {
        let nodes = self.nodes()?;
        let proof =
            mmr::consistency_proof(&nodes, from, to).map_err(|e| ShelfError::Log(e.to_string()))?;
        Ok((
            hashes(&proof.old_peaks)?,
            proof
                .witness
                .iter()
                .map(|p| hashes(p))
                .collect::<Result<_, _>>()?,
            hashes(&proof.new_peaks)?,
        ))
    }

    /// Close the log's connection.
    pub(crate) fn release(&self) -> Result<(), ShelfError> {
        self.store
            .close()
            .map_err(|e| ShelfError::Log(e.to_string()))
    }
}

fn peak_hashes(nodes: &MemoryNodeStore, size: u64) -> Result<Vec<Hash>, ShelfError> {
    Ok(mmr::peaks(size)
        .map_err(|e| ShelfError::Corrupt(e.to_string()))?
        .iter()
        .map(|&p| mmr::NodeReader::node(nodes, p))
        .collect())
}

fn hashes(values: &[String]) -> Result<Vec<[u8; 32]>, ShelfError> {
    values
        .iter()
        .map(|v| {
            hex::decode(v)
                .ok()
                .and_then(|b| b.try_into().ok())
                .ok_or_else(|| ShelfError::Corrupt("a proof hash is not 32 bytes of hex".into()))
        })
        .collect()
}

/// `at` as the log's millisecond time.
pub(crate) fn timestamp(at: Time) -> Timestamp {
    Timestamp::from_unix_millis(at.unix_nanos().div_euclid(1_000_000) as i64)
}

/// Whether the file holds no state at all for `log_id`: no file, or no meta
/// row, entry or node of that log (Go `cllSQLiteLogAbsent`).
fn log_absent(path: &Path, log_id: &str) -> Result<bool, ShelfError> {
    if !path.exists() {
        return Ok(true);
    }
    let conn = Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY)?;
    for table in ["cll_meta", "cll_entries", "cll_nodes"] {
        let exists: i64 = conn.query_row(
            "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
            [table],
            |r| r.get(0),
        )?;
        if exists == 0 {
            continue;
        }
        let rows: i64 = conn.query_row(
            &format!("SELECT COUNT(*) FROM {table} WHERE log_id = ?"),
            [log_id],
            |r| r.get(0),
        )?;
        if rows > 0 {
            return Ok(false);
        }
    }
    Ok(true)
}
