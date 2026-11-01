//! The heartbeat cadence (Go `cadence.go`; evidencebook#254 §2.6): on time
//! alone, each tick checkpoints every live log of the shelf locally, commits
//! those checkpoints as salted leaves of the cadence tree, appends the root
//! to the cadence log and checkpoints that log, which alone goes to the
//! witness. A log is live while it is not ended, or while it has grown past
//! the largest checkpoint of it a witnessed tick carries. Delivering the
//! cadence checkpoint to the witness is the host's; the host reports the
//! receipt with [`Cadence::witnessed`], which verifies it with AAC's
//! witness verifier.

use super::log::{Checkpoint, Log};
use super::store;
use super::time::Time;
use super::tree::{padding_record_id, random_position, TickLeaf, TickLeaves, CADENCE_DEPTH};
use super::{Shelf, ShelfError, ShelfState};
use agent_action_capsule::witness;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine as _;
use rusqlite::{params, OptionalExtension};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashSet};
use std::sync::Arc;
use std::time::Duration;

/// The profile's predicate: whether a log has ended (for a deal, a close
/// step is sealed). Evaluated each time it is needed, never stored; it must
/// not call the shelf.
pub type Ended = Arc<dyn Fn(&str) -> Result<bool, String> + Send + Sync>;

/// A shelf's cadence (Go `CadenceConfig`). The defaults and limits are
/// capsule-cli's: a tick every 5 m, give or take 1 m.
#[derive(Clone)]
pub struct CadenceConfig {
    /// The cadence log, in the shelf's file.
    pub log_id: String,
    /// At least 1 m; zero is 5 m.
    pub interval: Duration,
    /// Under half the interval; zero is 1 m unless `no_jitter`.
    pub jitter: Duration,
    /// Every tick is due exactly `interval` after the last.
    pub no_jitter: bool,
    /// The witnesses each cadence checkpoint is queued for, in the same
    /// commit (the host delivers them and reports each receipt).
    pub witness_ids: Vec<String>,
    /// The witness directory receipts are verified under (a parsed
    /// `witnesses.json`); `None` trusts no witness.
    pub witnesses: Option<Value>,
    /// Pad the cadence log to a multiple of this many entries per tick, at
    /// most 1024; zero is 1 (no padding).
    pub pad_bucket: u64,
    pub ended: Ended,
}

impl CadenceConfig {
    fn defaults(&mut self) -> Result<(), ShelfError> {
        if self.interval.is_zero() {
            self.interval = Duration::from_secs(300);
        }
        if self.jitter.is_zero() && !self.no_jitter {
            self.jitter = Duration::from_secs(60);
        }
        if self.pad_bucket == 0 {
            self.pad_bucket = 1;
        }
        let invalid = |why: &str| Err(ShelfError::Invalid(why.to_string()));
        if self.log_id.is_empty() {
            return invalid("a cadence needs its log id");
        }
        if self.interval < Duration::from_secs(60) {
            return invalid("the cadence interval must be at least 1m");
        }
        if self.no_jitter && !self.jitter.is_zero() {
            return invalid("a cadence with no_jitter has no jitter");
        }
        if 2 * self.jitter >= self.interval {
            return invalid("the cadence jitter must be at least 0 and under half the interval");
        }
        if self.pad_bucket > 1024 {
            return invalid("the cadence pad bucket must be at most 1024");
        }
        Ok(())
    }
}

/// The witness's receipt for a tick's cadence checkpoint (Go
/// `WitnessReceipt`; capsule-cli's chain carries these members).
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct WitnessReceipt {
    pub ts_url: String,
    pub entry_hash: String,
    #[serde(with = "base64_std")]
    pub receipt: Vec<u8>,
    pub leaf_index: u64,
    pub tree_size: u64,
}

mod base64_std {
    use base64::engine::general_purpose::STANDARD;
    use base64::Engine as _;
    use serde::{Deserialize, Deserializer, Serializer};

    pub fn serialize<S: Serializer>(b: &[u8], s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(&STANDARD.encode(b))
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Vec<u8>, D::Error> {
        let text = Option::<String>::deserialize(d)?.unwrap_or_default();
        STANDARD.decode(text).map_err(serde::de::Error::custom)
    }
}

impl WitnessReceipt {
    /// As `cadence_ticks.receipt` stores it (Go's `json.Marshal`).
    pub fn to_json(&self) -> String {
        super::gojson::marshal(&serde_json::to_value(self).expect("a receipt serializes"))
    }
}

/// One tick as stored (capsule-cli's `deal_cadence` row).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CadenceTick {
    pub n: u64,
    pub at: Time,
    pub due_next: Time,
    pub entry_seq: u64,
    /// The cadence log's checkpoint (MMR size and COSE statement) that
    /// carries this tick's root; zero and empty while the tick is
    /// unfinished.
    pub checkpoint_size: u64,
    pub checkpoint: Vec<u8>,
    pub leaves: TickLeaves,
    /// Set once a receipt for the checkpoint verified.
    pub witnessed: bool,
    pub receipt: Option<WitnessReceipt>,
}

/// A receipt verified under the configured witness directory.
pub const WITNESS_VERIFIED: &str = "VERIFIED";
/// A receipt not checked (its witness has no row or no usable key).
pub const WITNESS_NOT_CHECKED: &str = "NOT_CHECKED";
/// A receipt checked and rejected.
pub const WITNESS_INVALID: &str = "INVALID";

/// What checking one receipt found.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WitnessCheck {
    pub state: &'static str,
    pub reason: String,
}

/// Verify `receipt` for the signed cadence checkpoint `statement` under
/// `directory` with AAC's verifier (Go `checkWitnessReceipt`); the shelf
/// keeps no verifier of its own. An error means the statement itself does
/// not verify.
pub(crate) fn check_witness_receipt(
    statement: &[u8],
    receipt: &WitnessReceipt,
    directory: Option<&Value>,
) -> Result<WitnessCheck, ShelfError> {
    let verified = cll::checkpoint::verify_checkpoint_cose_offline(statement);
    let decoded = match verified.decoded {
        Some(d) if verified.ok && crate::ed25519::statement_verifies(statement) => d,
        _ => {
            return Err(ShelfError::Corrupt(
                "the cadence checkpoint statement does not verify".into(),
            ))
        }
    };
    let signed = witness::SignedCheckpoint::from(&decoded);
    let record = json!([{
        "ts_url": receipt.ts_url,
        "entry_hash": receipt.entry_hash,
        "receipt_b64": STANDARD.encode(&receipt.receipt),
        "leaf_index": receipt.leaf_index,
        "tree_size": receipt.tree_size,
    }]);
    let report =
        witness::verify_witness_receipts(Some(&record), Some(&signed), Some(statement), directory);
    let mut parts = report.findings.clone();
    if let [only] = report.receipts.as_slice() {
        if !only.reason.is_empty() {
            parts.push(only.reason.clone());
        }
    }
    let state = match report.status.as_str() {
        "pass" => WITNESS_VERIFIED,
        "withheld" => WITNESS_NOT_CHECKED,
        _ => WITNESS_INVALID,
    };
    Ok(WitnessCheck {
        state,
        reason: parts.join(": "),
    })
}

/// What one [`Cadence::tick`] did.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TickResult {
    /// False when no tick was due; `due_next` then says when one is.
    pub ticked: bool,
    pub tick: Option<CadenceTick>,
    pub due_next: Time,
}

/// A shelf's heartbeat ticks (Go `Cadence`).
#[derive(Clone)]
pub struct Cadence {
    pub(crate) shelf: Shelf,
    pub(crate) cfg: CadenceConfig,
    pub(crate) log: Arc<Log>,
}

/// The MMR node count of `leaves` leaves.
pub(crate) fn mmr_size(leaves: u64) -> u64 {
    2 * leaves - u64::from(leaves.count_ones())
}

/// What a tick checkpoints: a book, or a host log in the cadence.
pub(crate) enum CadenceLog {
    Book(super::ShelfBook),
    Host(super::HostLog),
}

impl CadenceLog {
    pub(crate) fn mmr_size(&self) -> Result<u64, ShelfError> {
        Ok(mmr_size(match self {
            CadenceLog::Book(b) => b.0.log.size()?,
            CadenceLog::Host(h) => h.log().size()?,
        }))
    }

    /// Checkpoint everything the log holds at `at`, cutting a new checkpoint
    /// only if it grew.
    pub(crate) fn cut_local(&self, at: Time) -> Result<Checkpoint, ShelfError> {
        match self {
            CadenceLog::Book(b) => b.checkpoint_at(at),
            CadenceLog::Host(h) => h.log().checkpoint(at),
        }
    }

    pub(crate) fn log(&self) -> &Log {
        match self {
            CadenceLog::Book(b) => &b.0.log,
            CadenceLog::Host(h) => h.log(),
        }
    }
}

impl super::HostLog {
    pub(crate) fn log(&self) -> &Log {
        &self.core.log
    }
}

impl Shelf {
    /// Open the shelf's cadence log, creating it when absent (Go
    /// `Shelf.Cadence`). One cadence per shelf; its log is never a book or
    /// a host log.
    pub fn cadence(&self, mut cfg: CadenceConfig) -> Result<Cadence, ShelfError> {
        cfg.defaults()?;
        let mut state = self.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        if let Some((id, _)) = &state.cadence {
            return Err(ShelfError::Invalid(format!(
                "the shelf already runs a cadence ({id})"
            )));
        }
        let book = state.books.contains_key(&cfg.log_id);
        let host = state.hosts.contains_key(&cfg.log_id);
        let kind = store::log_kind(&self.shared().db(), &cfg.log_id)?;
        if book || host || kind.is_some() {
            let what = if book || kind.as_deref() == Some(store::KIND_BOOK) {
                "book"
            } else {
                "host"
            };
            return Err(ShelfError::Invalid(format!(
                "{} is a {what} log, not the cadence log",
                cfg.log_id
            )));
        }
        let shared = self.shared();
        let log = Arc::new(Log::open(
            &shared.path,
            &cfg.log_id,
            &shared.checkpoint_key,
            &cfg.witness_ids,
            true,
        )?);
        state.cadence = Some((cfg.log_id.clone(), log.clone()));
        Ok(Cadence {
            shelf: self.clone(),
            cfg,
            log,
        })
    }

    /// The cadence log of a live log id: its book when it has records, else
    /// its legacy log.
    pub(crate) fn cadence_log_locked(
        &self,
        state: &mut ShelfState,
        log_id: &str,
    ) -> Result<CadenceLog, ShelfError> {
        if store::record_count(&self.shared().db(), log_id)? > 0 {
            return Ok(CadenceLog::Book(self.open_locked(state, log_id)?));
        }
        Ok(CadenceLog::Host(
            self.host_locked(state, log_id, true, false)?,
        ))
    }
}

/// The SQL that records a witnessed tick carrying a log checkpoint, keeping
/// the newest witnessed tick that carried it.
pub(crate) const INDEX_WITNESSED: &str = "INSERT INTO cadence_witnessed (log_id, checkpoint_sha256, mmr_size, tick) VALUES (?, ?, ?, ?)
ON CONFLICT (log_id, checkpoint_sha256) DO UPDATE SET tick = excluded.tick WHERE excluded.tick > cadence_witnessed.tick";

/// The MMR size of everything a log holds, read in `conn`: a book's stored
/// records, else a host log's CLL entries.
pub(crate) fn held_size(conn: &rusqlite::Connection, log_id: &str) -> Result<u64, ShelfError> {
    let mut leaves = store::record_count(conn, log_id)?;
    if leaves == 0 {
        let n: i64 = conn.query_row(
            "SELECT COUNT(*) FROM cll_entries WHERE log_id = ?",
            [log_id],
            |r| r.get(0),
        )?;
        leaves = n as u64;
    }
    Ok(mmr_size(leaves))
}

impl Cadence {
    /// The cadence log's id.
    pub fn log_id(&self) -> &str {
        &self.cfg.log_id
    }

    /// Cut a tick when one is due at `now`; otherwise report when one is
    /// (Go `Tick`). A tick is due when `now` is at or after the last tick's
    /// `due_next`, and only then. A tick stored but not finished is finished
    /// first, as the same tick.
    pub fn tick(&self, now: Time) -> Result<TickResult, ShelfError> {
        let mut state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let last = self.last_locked()?;
        if let Some(last) = &last {
            if last.checkpoint_size == 0 {
                return self.finish_locked(&mut state, last.clone());
            }
            if now < last.due_next {
                return Ok(TickResult {
                    ticked: false,
                    tick: None,
                    due_next: last.due_next,
                });
            }
        }
        let n = last.as_ref().map_or(1, |l| l.n + 1);
        // Times leaving this device are coarsened to the minute.
        let at = now.truncate(Duration::from_secs(60));
        let live = self.live_locked()?;
        let lifetime: i64 =
            self.shelf
                .shared()
                .db()
                .query_row("SELECT COUNT(*) FROM cadence_logs", [], |r| r.get(0))?;
        let lifetime = lifetime as u64;
        if live.len() as u64 >= 1 << CADENCE_DEPTH || lifetime >= 1 << CADENCE_DEPTH {
            return Err(ShelfError::Invalid(
                "too many logs for one cadence tree".into(),
            ));
        }
        // Every draw is from the shelf's one source, in this order: filler
        // 0's position and nonce; each live log's salt and position, in log
        // id order; the derived fillers' seed when there are any; the
        // jitter; then each padding record's nonce (finish_locked).
        let mut used = HashSet::new();
        let mut leaves = TickLeaves {
            filler_index: random_position(&mut used, &mut state.random)?,
            ..Default::default()
        };
        leaves.filler_nonce = state.random.hex(32)?;
        for id in &live {
            let log = self.shelf.cadence_log_locked(&mut state, id)?;
            // A host log opened in the cadence but not yet written holds no
            // entry, so it has no checkpoint to carry: the tick skips it,
            // and the occupancy floor counts it like a log the tick dropped.
            if log.mmr_size()? == 0 {
                continue;
            }
            let cp = log
                .cut_local(at)
                .map_err(|e| ShelfError::Log(format!("checkpoint {id} for the tick: {e}")))?;
            let salt = state.random.hex(32)?;
            let index = random_position(&mut used, &mut state.random)?;
            leaves.deals.insert(
                id.clone(),
                TickLeaf {
                    log_id: id.clone(),
                    size: cp.tree_size,
                    checkpoint_sha256: hex::encode(Sha256::digest(&cp.statement)),
                    salt,
                    index,
                    statement: URL_SAFE_NO_PAD.encode(&cp.statement),
                },
            );
        }
        // The occupancy floor: H+1 occupied positions, as when every log was
        // in every tick.
        let carried = leaves.deals.len() as u64;
        if lifetime > carried {
            leaves.filler_seed = state.random.hex(32)?;
            leaves.filler_count = lifetime - carried;
        }
        // The jitter is uniform over [-jitter, +jitter] in nanoseconds.
        let mut jitter: i128 = 0;
        if !self.cfg.jitter.is_zero() {
            let j = self.cfg.jitter.as_nanos() as u64;
            jitter = i128::from(state.random.uniform(2 * j + 1)?) - i128::from(j);
        }
        let due_next = now
            .add(self.cfg.interval)
            .add_nanos(jitter)
            .truncate(Duration::from_secs(1));
        let tick = CadenceTick {
            n,
            at,
            due_next,
            entry_seq: 0,
            checkpoint_size: 0,
            checkpoint: Vec::new(),
            leaves,
            witnessed: false,
            receipt: None,
        };
        // The tick is stored before its root reaches the cadence log, so a
        // failure after this point resumes this tick rather than cutting
        // another. A zero checkpoint size marks it unfinished.
        self.shelf.shared().db().execute(
            "INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)",
            params![tick.n as i64, tick.at.rfc3339(), tick.due_next.rfc3339(), tick.leaves.to_json()],
        )?;
        self.finish_locked(&mut state, tick)
    }

    /// Put a stored tick's root in the cadence log (once), pad the log to
    /// the bucket, checkpoint it, and complete the tick's row (Go
    /// `finishLocked`).
    fn finish_locked(
        &self,
        state: &mut ShelfState,
        mut tick: CadenceTick,
    ) -> Result<TickResult, ShelfError> {
        let (root, _) = tick.leaves.tree(tick.leaves.filler_index)?;
        let root_id = hex::encode(root);
        let after = if tick.n > 1 {
            self.tick_locked(tick.n - 1)?.entry_seq
        } else {
            0
        };
        // Read only what follows the previous tick's entry: at most this
        // tick's root and padding.
        let mut entry_seq = 0;
        let mut end = after;
        loop {
            let entries = self.log.entries(end, 1000)?;
            let Some(last) = entries.last() else {
                break;
            };
            for entry in &entries {
                if hex::encode(&entry.value) == root_id && entry_seq == 0 {
                    entry_seq = entry.seq;
                }
            }
            end = last.seq;
        }
        if entry_seq == 0 {
            entry_seq = self.log.append(&root, tick.at)?;
            end = entry_seq;
        }
        // Padding records bring the leaf count to a multiple of the bucket.
        let mut count = end;
        while count % self.cfg.pad_bucket != 0 {
            let nonce = state.random.hex(32)?;
            let id = padding_record_id(&nonce);
            self.log
                .append(&hex::decode(id).expect("a digest"), tick.at)?;
            count += 1;
        }
        let cp = self.log.checkpoint(tick.at)?;
        tick.entry_seq = entry_seq;
        tick.checkpoint_size = cp.tree_size;
        tick.checkpoint = cp.statement;
        self.shelf.shared().db().execute(
            "UPDATE cadence_ticks SET entry_seq = ?, checkpoint_size = ?, checkpoint = ? WHERE tick = ?",
            params![tick.entry_seq as i64, tick.checkpoint_size as i64, tick.checkpoint, tick.n as i64],
        )?;
        Ok(TickResult {
            ticked: true,
            due_next: tick.due_next,
            tick: Some(tick),
        })
    }

    /// Check a witness's receipt for tick `n`'s cadence checkpoint under the
    /// configured directory (Go `Witnessed`). Only a verified receipt
    /// witnesses the tick: in one transaction, each log checkpoint the tick
    /// carries gets a `cadence_witnessed` row, and each of those logs that
    /// has ended and has not grown past its largest witnessed checkpoint
    /// leaves the live set. A `NOT_CHECKED` receipt changes nothing; an
    /// `INVALID` one changes nothing and is an error naming the reason.
    pub fn witnessed(
        &self,
        n: u64,
        mut receipt: WitnessReceipt,
    ) -> Result<WitnessCheck, ShelfError> {
        // capsule-cli carries the witness's URL without a trailing slash.
        while receipt.ts_url.ends_with('/') {
            receipt.ts_url.pop();
        }
        if receipt.ts_url.is_empty() || receipt.entry_hash.is_empty() || receipt.receipt.is_empty()
        {
            return Err(ShelfError::Invalid(
                "a witnessed tick needs its receipt, its entry hash and its witness's URL".into(),
            ));
        }
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let tick = self.tick_locked(n)?;
        if tick.checkpoint_size == 0 {
            return Err(ShelfError::Invalid(format!(
                "tick {n} is not finished, so no witness has seen its root"
            )));
        }
        let check = check_witness_receipt(&tick.checkpoint, &receipt, self.cfg.witnesses.as_ref())
            .map_err(|e| ShelfError::Corrupt(format!("tick {n}: {e}")))?;
        if check.state == WITNESS_INVALID {
            return Err(ShelfError::Invalid(format!(
                "tick {n}'s witness receipt is rejected: {}",
                check.reason
            )));
        }
        if check.state != WITNESS_VERIFIED {
            return Ok(check);
        }
        let mut ended = BTreeMap::new();
        for id in tick.leaves.deals.keys() {
            ended.insert(
                id.clone(),
                (self.cfg.ended)(id).map_err(ShelfError::Invalid)?,
            );
        }
        let mut db = self.shelf.shared().db();
        let tx = db.transaction()?;
        tx.execute(
            "UPDATE cadence_ticks SET witnessed = 1, receipt = COALESCE(receipt, ?) WHERE tick = ?",
            params![receipt.to_json(), n as i64],
        )?;
        for (id, leaf) in &tick.leaves.deals {
            tx.execute(
                INDEX_WITNESSED,
                params![id, leaf.checkpoint_sha256, leaf.size as i64, n as i64],
            )?;
            let witnessed: i64 = tx.query_row(
                "SELECT COALESCE(MAX(mmr_size), 0) FROM cadence_witnessed WHERE log_id = ?",
                [id],
                |r| r.get(0),
            )?;
            // The log's size is read in this transaction from what it holds;
            // an append committed after it re-enters the live set.
            if ended[id] && held_size(&tx, id)? <= witnessed as u64 {
                tx.execute("DELETE FROM cadence_live WHERE log_id = ?", [id])?;
            }
        }
        tx.commit()?;
        Ok(check)
    }

    /// The witnessed tick a bundle of `log_id`'s checkpoint (its
    /// statement's SHA-256 and MMR size) is anchored by (Go
    /// `WitnessedTick`): `"all"` when a witnessed tick carries that very
    /// checkpoint, else `"part"` with the newest witnessed tick carrying an
    /// earlier checkpoint of the log. `NotFound` when none does.
    pub fn witnessed_tick(
        &self,
        log_id: &str,
        checkpoint_sha256: &str,
        mmr_size: u64,
    ) -> Result<(&'static str, CadenceTick, TickLeaf), ShelfError> {
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        self.witnessed_tick_locked(log_id, checkpoint_sha256, mmr_size)
    }

    pub(crate) fn witnessed_tick_locked(
        &self,
        log_id: &str,
        checkpoint_sha256: &str,
        mmr_size: u64,
    ) -> Result<(&'static str, CadenceTick, TickLeaf), ShelfError> {
        let db = self.shelf.shared().db();
        let mut extent = "all";
        let mut n: Option<i64> = db
            .query_row(
                "SELECT tick FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ?",
                params![log_id, checkpoint_sha256],
                |r| r.get(0),
            )
            .optional()?;
        if n.is_none() {
            extent = "part";
            n = db
                .query_row(
                    "SELECT tick FROM cadence_witnessed WHERE log_id = ? AND mmr_size < ? ORDER BY mmr_size DESC, tick DESC LIMIT 1",
                    params![log_id, mmr_size as i64],
                    |r| r.get(0),
                )
                .optional()?;
        }
        drop(db);
        let n =
            n.ok_or_else(|| ShelfError::NotFound(format!("no witnessed tick carries {log_id}")))?;
        let tick = self.tick_locked(n as u64)?;
        let leaf = tick.leaves.deals.get(log_id).cloned().ok_or_else(|| {
            ShelfError::Corrupt(format!("witnessed tick {n} holds no leaf of {log_id}"))
        })?;
        Ok((extent, tick, leaf))
    }

    /// The live set, in log id order.
    pub fn live(&self) -> Result<Vec<String>, ShelfError> {
        let _state = self.shelf.lock();
        self.live_locked()
    }

    fn live_locked(&self) -> Result<Vec<String>, ShelfError> {
        let db = self.shelf.shared().db();
        let mut stmt = db.prepare("SELECT log_id FROM cadence_live ORDER BY log_id")?;
        let ids = stmt.query_map([], |r| r.get(0))?;
        Ok(ids.collect::<Result<_, _>>()?)
    }

    pub(crate) fn last_locked(&self) -> Result<Option<CadenceTick>, ShelfError> {
        let n: i64 = self.shelf.shared().db().query_row(
            "SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks",
            [],
            |r| r.get(0),
        )?;
        if n == 0 {
            return Ok(None);
        }
        self.tick_locked(n as u64).map(Some)
    }

    pub(crate) fn tick_locked(&self, n: u64) -> Result<CadenceTick, ShelfError> {
        type Row = (
            String,
            String,
            i64,
            i64,
            Vec<u8>,
            String,
            i64,
            Option<String>,
        );
        let row: Option<Row> = self
            .shelf
            .shared()
            .db()
            .query_row(
                "SELECT at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt FROM cadence_ticks WHERE tick = ?",
                [n as i64],
                |r| {
                    Ok((
                        r.get(0)?,
                        r.get(1)?,
                        r.get(2)?,
                        r.get(3)?,
                        r.get(4)?,
                        r.get(5)?,
                        r.get(6)?,
                        r.get(7)?,
                    ))
                },
            )
            .optional()?;
        let (at, due, entry_seq, size, checkpoint, leaves, witnessed, receipt) =
            row.ok_or_else(|| ShelfError::NotFound(format!("tick {n}")))?;
        let corrupt = |what: &str| ShelfError::Corrupt(format!("tick {n} {what}"));
        Ok(CadenceTick {
            n,
            at: Time::parse(&at).ok_or_else(|| corrupt("time"))?,
            due_next: Time::parse(&due).ok_or_else(|| corrupt("due time"))?,
            entry_seq: entry_seq as u64,
            checkpoint_size: size as u64,
            checkpoint,
            leaves: serde_json::from_str(&leaves).map_err(|_| corrupt("leaves"))?,
            witnessed: witnessed != 0,
            receipt: receipt
                .map(|r| serde_json::from_str(&r).map_err(|_| corrupt("receipt")))
                .transpose()?,
        })
    }

    /// The cadence's newest stored tick, `None` when it has none.
    pub fn last(&self) -> Result<Option<CadenceTick>, ShelfError> {
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        self.last_locked()
    }

    /// Stored tick `n`.
    pub fn get(&self, n: u64) -> Result<CadenceTick, ShelfError> {
        let _state = self.shelf.lock();
        self.tick_locked(n)
    }
}
