//! The shelf log of book facts (Go `shelf_stamp.go`; evidencebook#461,
//! `docs/design/461-shelf-facts.md` §2.4, §2.5 and §4): after
//! [`Cadence::migrate`], every tick of the cadence is a stamp. A stamp
//! appends one fact for each log whose fact would differ from its last one,
//! then a stamp entry, and checkpoints the cadence log, which alone goes to
//! the witness. A stamp is a cadence tick whose stored leaves carry its
//! batch ([`TickLeaves::stamp`]), so the tick's storage, recovery,
//! witnessing and card anchor are the cadence's own: a stamp stored but not
//! finished is finished first, as the same stamp, before any checkpoint is
//! cut.
//!
//! The batch's JSON, the `shelf_entries` rows and every random draw are
//! Go's, byte for byte: `testdata/shelf/after-migrate.json` is the dump of
//! the scripted shelf after its migrate phase.

use super::cadence::{Cadence, CadenceLog, CadenceTick, TickResult};
use super::store::{self, SHELF_VERSION_FACTS};
use super::time::Time;
use super::tree::{TickLeaf, TickLeaves};
use super::{Shelf, ShelfError, ShelfState};
use crate::shelf_facts::{
    self, book_fact, entry_id, format_switch, journal_fact, openings_for, stamp_entry, FactInput,
    Opening, ENTRY_KIND_FORMAT_SWITCH, ENTRY_KIND_STAMP, FACT_KIND_BOOK, FACT_KIND_JOURNAL,
    PROFILE_NONE,
};
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine as _;
use rusqlite::{params, Connection, OptionalExtension};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

/// Migration states (`shelf_migration.state`).
const MIGRATION_BACKING_UP: &str = "backing_up";
const MIGRATION_MIGRATING: &str = "migrating";
pub(crate) const MIGRATION_DONE: &str = "done";

/// The values a book's fact commits to at a stamp (§2.4), as the profile
/// gives them: its profile name, the link value's members and its status
/// object (`None` is `{}`).
#[derive(Clone, Debug, Default, PartialEq)]
pub struct StatusValues {
    pub profile: String,
    /// `{log_id, size, record_digest}` of the last record held from the
    /// other side, or `None`.
    pub last_received: Option<Value>,
    /// The digests the recomputed checks ran over (any order).
    pub checked_over: Vec<String>,
    pub status: Option<Value>,
}

/// Gives a book's fact values, by its book id and log id (Go's
/// `StatusProvider`). The shelf asks it for each book at each stamp;
/// `Ok(None)` (no profile claims the book) is profile `none`, the empty link
/// value and `{}`. It must not call the shelf: the shelf's lock is held.
pub type BookStatus = Arc<dyn Fn(&str, &str) -> Result<Option<StatusValues>, String> + Send + Sync>;

/// A stamp's stored batch: its entry documents in append order, the stamp
/// entry last (Go `StampBatch`). Bytes are standard base64 in JSON.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct StampBatch {
    pub v: u64,
    pub entries: Vec<BatchEntry>,
}

/// One entry of a batch: its document's RFC 8785 bytes and, for a fact, the
/// openings of its two commitments as stored (Go `BatchEntry`).
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct BatchEntry {
    pub kind: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub book_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub log_id: String,
    #[serde(with = "bytes_b64")]
    pub doc: Vec<u8>,
    #[serde(default, with = "bytes_b64", skip_serializing_if = "Vec::is_empty")]
    pub link_opening: Vec<u8>,
    #[serde(default, with = "bytes_b64", skip_serializing_if = "Vec::is_empty")]
    pub status_opening: Vec<u8>,
}

/// Go's `[]byte` in JSON: standard base64, `null` for none.
pub(super) mod bytes_b64 {
    use super::*;

    pub fn serialize<S: Serializer>(b: &[u8], s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(&STANDARD.encode(b))
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Vec<u8>, D::Error> {
        let text = Option::<String>::deserialize(d)?.unwrap_or_default();
        STANDARD.decode(text).map_err(serde::de::Error::custom)
    }
}

/// One fact of a book with the openings an audience may have (`None` where
/// it may not), as [`Shelf::openings`] hands them out.
#[derive(Clone, Debug, PartialEq)]
pub struct AudienceFact {
    pub entry_seq: u64,
    /// The fact document.
    pub fact: Value,
    pub link_opening: Option<Opening>,
    pub status_opening: Option<Opening>,
}

/// The migration's state, `""` before it starts.
pub(crate) fn migration_state(conn: &Connection) -> Result<String, ShelfError> {
    Ok(conn
        .query_row("SELECT state FROM shelf_migration WHERE id = 1", [], |r| {
            r.get(0)
        })
        .optional()?
        .unwrap_or_default())
}

fn invalid(e: shelf_facts::Invalid) -> ShelfError {
    ShelfError::Invalid(e.to_string())
}

fn sha256_hex(b: &[u8]) -> String {
    hex::encode(Sha256::digest(b))
}

fn file_sha256(path: &Path) -> std::io::Result<String> {
    Ok(sha256_hex(&std::fs::read(path)?))
}

fn with_suffix(path: &Path, suffix: &str) -> PathBuf {
    let mut p = path.as_os_str().to_os_string();
    p.push(suffix);
    PathBuf::from(p)
}

/// An opening as stored: the RFC 8785 bytes of `{"salt", "value"}`, as
/// every entry document is.
fn opening_bytes(o: &Opening) -> Result<Vec<u8>, ShelfError> {
    crate::canonical::jcs(&o.to_json()).map_err(|e| ShelfError::Invalid(e.to_string()))
}

/// The canonical bytes of an opening's value, as stored.
fn opening_value(stored: &[u8]) -> Result<Value, ShelfError> {
    let v: Value = serde_json::from_slice(stored)
        .map_err(|e| ShelfError::Corrupt(format!("a stored opening: {e}")))?;
    v.get("value")
        .cloned()
        .ok_or_else(|| ShelfError::Corrupt("a stored opening without its value".into()))
}

fn parse_opening(stored: &[u8]) -> Result<Opening, ShelfError> {
    let v: Value = serde_json::from_slice(stored)
        .map_err(|e| ShelfError::Corrupt(format!("a stored opening: {e}")))?;
    match (v.get("salt").and_then(Value::as_str), v.get("value")) {
        (Some(salt), Some(value)) => Ok(Opening {
            salt: salt.to_string(),
            value: value.clone(),
        }),
        _ => Err(ShelfError::Corrupt(
            "a stored opening is {salt, value}".into(),
        )),
    }
}

/// One log's fact before its salts are drawn.
struct PendingFact {
    profile: String,
    book_id: String,
    log_id: String,
    size: u64,
    checkpoint_sha256: String,
    prev_fact_size: u64,
    last_received: Option<Value>,
    checked_over: Vec<String>,
    status: Option<Value>,
    /// The log's new checkpoint, when it grew.
    statement: Option<Vec<u8>>,
    grown: bool,
    /// The journal's fact (§2.2): its size, checkpoint digest and previous
    /// size go inside its status commitment.
    journal: bool,
}

/// The newest fact the shelf log holds for a log.
struct LastFact {
    size: u64,
    checkpoint_sha256: String,
    link: Value,
    status: Value,
}

impl PendingFact {
    /// Whether this fact would differ from `last` (§2.4): the size grew, or
    /// a committed value differs. Values are compared, never commitments,
    /// since the salts differ every time.
    fn changed(&self, last: Option<&LastFact>) -> Result<bool, ShelfError> {
        let Some(last) = last else {
            return Ok(true);
        };
        if self.grown {
            return Ok(true);
        }
        if self.journal {
            // The journal's values follow its size: unchanged unless it grew.
            return Ok(false);
        }
        let link = shelf_facts::link_value(self.last_received.clone(), self.checked_over.clone())
            .map_err(invalid)?;
        let status = self.status.clone().unwrap_or_else(|| json!({}));
        if !status.is_object() {
            return Err(ShelfError::Invalid(
                "a fact's status is a JSON object".into(),
            ));
        }
        let jcs =
            |v: &Value| crate::canonical::jcs(v).map_err(|e| ShelfError::Invalid(e.to_string()));
        Ok(jcs(&link)? != jcs(&last.link)? || jcs(&status)? != jcs(&last.status)?)
    }
}

impl Shelf {
    /// Every log of the lifetime table, sorted.
    fn cadence_log_ids(&self) -> Result<Vec<String>, ShelfError> {
        let db = self.shared().db();
        let mut stmt = db.prepare("SELECT log_id FROM cadence_logs ORDER BY log_id")?;
        let ids = stmt.query_map([], |r| r.get(0))?;
        Ok(ids.collect::<Result<_, _>>()?)
    }

    fn last_fact_of(&self, log_id: &str) -> Result<Option<LastFact>, ShelfError> {
        type Row = (String, Vec<u8>, Option<Vec<u8>>, Option<Vec<u8>>);
        let row: Option<Row> = self
            .shared()
            .db()
            .query_row(
                "SELECT kind, doc, link_opening, status_opening FROM shelf_entries WHERE log_id = ? AND kind IN (?, ?) ORDER BY entry_seq DESC LIMIT 1",
                params![log_id, FACT_KIND_BOOK, FACT_KIND_JOURNAL],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
            )
            .optional()?;
        let Some((kind, doc, lo, so)) = row else {
            return Ok(None);
        };
        let corrupt = || ShelfError::Corrupt(format!("a stored fact of {log_id}"));
        let mut fact: Value = serde_json::from_slice(&doc).map_err(|_| corrupt())?;
        let status = opening_value(&so.unwrap_or_default())?;
        if kind == FACT_KIND_JOURNAL {
            // The journal's true size and checkpoint are its status value's.
            fact = status.clone();
        }
        Ok(Some(LastFact {
            size: fact
                .get("size")
                .and_then(Value::as_u64)
                .ok_or_else(corrupt)?,
            checkpoint_sha256: fact
                .get("checkpoint_sha256")
                .and_then(Value::as_str)
                .ok_or_else(corrupt)?
                .to_string(),
            link: opening_value(&lo.unwrap_or_default())?,
            status,
        }))
    }

    /// A log's book id (§2.2): its first record's id for a book, its first
    /// CLL entry's id for a host log.
    fn book_id_of(&self, log: &CadenceLog) -> Result<String, ShelfError> {
        match log {
            CadenceLog::Book(b) => Ok(self.shared().db().query_row(
                "SELECT record_id FROM book_records WHERE book_id = ? ORDER BY seq LIMIT 1",
                [&b.0.id],
                |r| r.get(0),
            )?),
            CadenceLog::Host(h) => {
                let entries = h.log().entries(0, 1)?;
                let first = entries.first().ok_or_else(|| {
                    ShelfError::Invalid(format!("log {} has no entry", h.log().log_id))
                })?;
                Ok(hex::encode(&first.value))
            }
        }
    }

    /// Every fact of a book (by its book id) with the openings `audience`
    /// may have, as [`shelf_facts::openings_for`] says (§2.2; Go
    /// `Openings`). It is the only call that hands openings out.
    pub fn openings(&self, book_id: &str, audience: &str) -> Result<Vec<AudienceFact>, ShelfError> {
        let state = self.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        // An unknown audience is refused first, whatever the book holds.
        openings_for(FACT_KIND_BOOK, audience).map_err(|e| ShelfError::Invalid(e.to_string()))?;
        type Row = (i64, String, Vec<u8>, Option<Vec<u8>>, Option<Vec<u8>>);
        let rows: Vec<Row> = {
            let db = self.shared().db();
            let mut stmt = db.prepare(
                "SELECT entry_seq, kind, doc, link_opening, status_opening FROM shelf_entries WHERE book_id = ? AND kind IN (?, ?) ORDER BY entry_seq",
            )?;
            let rows = stmt
                .query_map(params![book_id, FACT_KIND_BOOK, FACT_KIND_JOURNAL], |r| {
                    Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?))
                })?;
            rows.collect::<Result<_, _>>()?
        };
        let mut out = Vec::with_capacity(rows.len());
        for (seq, kind, doc, lo, so) in rows {
            let (link, status) = openings_for(&kind, audience).map_err(invalid)?;
            let fact = serde_json::from_slice(&doc)
                .map_err(|e| ShelfError::Corrupt(format!("stored fact {seq}: {e}")))?;
            let opening = |want: bool, stored: Option<Vec<u8>>| {
                want.then(|| parse_opening(&stored.unwrap_or_default()))
                    .transpose()
            };
            out.push(AudienceFact {
                entry_seq: seq as u64,
                fact,
                link_opening: opening(link, lo)?,
                status_opening: opening(status, so)?,
            });
        }
        Ok(out)
    }
}

impl Cadence {
    /// Whether the shelf's cadence log is a log of book facts (Go
    /// `Migrated`).
    pub fn migrated(&self) -> Result<bool, ShelfError> {
        let _state = self.shelf.lock();
        Ok(migration_state(&self.shelf.shared().db())? == MIGRATION_DONE)
    }

    /// Switch the shelf to a log of book facts (§4; Go `Migrate`). It is
    /// explicit and one-way, and recoverable at every step: a call after a
    /// crash resumes from the stored state. It backs the file up first to
    /// `<path>.v1.bak`, a version 1 file an older build opens.
    pub fn migrate(&self, now: Time) -> Result<(), ShelfError> {
        let mut state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let mut current = migration_state(&self.shelf.shared().db())?;
        if current == MIGRATION_DONE {
            return Ok(());
        }
        if current.is_empty() {
            // Step 1: no tree root is ever appended after the marker, so a
            // tick stored but not finished is finished by the tree's code
            // first.
            if let Some(last) = self.last_locked()? {
                if last.checkpoint_size == 0 {
                    self.finish_locked(&mut state, last)?;
                }
            }
            // Step 2, first transaction: from here no older build writes the
            // file.
            let mut db = self.shelf.shared().db();
            let tx = store::write_tx(&mut db)?;
            tx.execute(
                "UPDATE shelf_meta SET version = ? WHERE id = 1",
                [SHELF_VERSION_FACTS],
            )?;
            tx.execute(
                "INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, ?, '')",
                [MIGRATION_BACKING_UP],
            )?;
            tx.commit()?;
            current = MIGRATION_BACKING_UP.to_string();
        }
        if current == MIGRATION_BACKING_UP {
            self.backup_locked()?;
            self.store_migration_batch_locked(&mut state, now)?;
        }
        // Step 4: append through the cadence log's own handle, as a stamp
        // is.
        match self.last_locked()? {
            Some(last) if last.leaves.stamp.is_some() => {
                if last.checkpoint_size == 0 {
                    self.finish_stamp_locked(last)?;
                }
            }
            _ => {
                return Err(ShelfError::Corrupt(
                    "the migration's stamp is not stored".into(),
                ))
            }
        }
        // Step 5.
        self.shelf.shared().db().execute(
            "UPDATE shelf_migration SET state = ? WHERE id = 1",
            [MIGRATION_DONE],
        )?;
        Ok(())
    }

    /// Write `<path>.v1.bak` (§4 step 2): a consistent snapshot by `VACUUM
    /// INTO`, set back to version 1 with no migration row, renamed into
    /// place only once its digest is recorded, and reused on a restart.
    fn backup_locked(&self) -> Result<(), ShelfError> {
        let path = &self.shelf.shared().path;
        let (fin, tmp) = (
            with_suffix(path, ".v1.bak"),
            with_suffix(path, ".v1.bak.tmp"),
        );
        let recorded: String = self.shelf.shared().db().query_row(
            "SELECT backup_sha256 FROM shelf_migration WHERE id = 1",
            [],
            |r| r.get(0),
        )?;
        if !recorded.is_empty() {
            if let Ok(got) = file_sha256(&fin) {
                if got != recorded {
                    return Err(ShelfError::Invalid(format!(
                        "{} does not match the backup this migration recorded; move it aside",
                        fin.display()
                    )));
                }
                return Ok(());
            }
            if file_sha256(&tmp).is_ok_and(|got| got == recorded) {
                std::fs::rename(&tmp, &fin)?;
                return Ok(());
            }
        }
        if fin.exists() {
            return Err(ShelfError::Invalid(format!(
                "{} exists and is not this migration's backup; move it aside",
                fin.display()
            )));
        }
        // A temporary file from an attempt that did not finish: VACUUM INTO
        // refuses an existing file.
        match std::fs::remove_file(&tmp) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
            _ => {}
        }
        let tmp_text = tmp
            .to_str()
            .ok_or_else(|| ShelfError::Invalid(format!("{} is not UTF-8", tmp.display())))?;
        self.shelf
            .shared()
            .db()
            .execute("VACUUM INTO ?", [tmp_text])?;
        reset_backup(&tmp)?;
        let digest = file_sha256(&tmp)?;
        self.shelf.shared().db().execute(
            "UPDATE shelf_migration SET backup_sha256 = ? WHERE id = 1",
            [&digest],
        )?;
        std::fs::rename(&tmp, &fin)?;
        Ok(())
    }

    /// Store the marker, the genesis facts and a stamp entry as the
    /// migration's stamp (§4 step 3), with state `migrating`, in one
    /// transaction.
    fn store_migration_batch_locked(
        &self,
        state: &mut ShelfState,
        now: Time,
    ) -> Result<(), ShelfError> {
        let last_tick = self.last_locked()?.map_or(0, |l| l.n);
        let at = now.truncate(Duration::from_secs(60));
        let mut sizes = Vec::new();
        let mut facts = Vec::new();
        for id in self.shelf.cadence_log_ids()? {
            let log = self.shelf.cadence_log_locked(state, &id)?;
            let size = log.mmr_size()?;
            sizes.push((id, size));
            if size == 0 {
                continue;
            }
            facts.push(self.fact_for(&log, at, None)?);
        }
        let marker = format_switch(last_tick, sizes).map_err(invalid)?;
        let (_, marker_doc) = entry_id(&marker).map_err(invalid)?;
        let head = vec![BatchEntry {
            kind: ENTRY_KIND_FORMAT_SWITCH.into(),
            book_id: String::new(),
            log_id: String::new(),
            doc: marker_doc,
            link_opening: Vec::new(),
            status_opening: Vec::new(),
        }];
        let tick = self.build_stamp_locked(state, last_tick + 1, at, now, facts, head)?;
        let mut db = self.shelf.shared().db();
        let tx = store::write_tx(&mut db)?;
        insert_tick(&tx, &tick)?;
        tx.execute(
            "UPDATE shelf_migration SET state = ? WHERE id = 1",
            [MIGRATION_MIGRATING],
        )?;
        tx.commit()?;
        Ok(())
    }

    /// What a log's fact would be now (§2.4); `last` is its previous fact,
    /// `None` for a genesis fact.
    fn fact_for(
        &self,
        log: &CadenceLog,
        at: Time,
        last: Option<&LastFact>,
    ) -> Result<PendingFact, ShelfError> {
        let log_id = log.log().log_id.clone();
        // The journal (§2.2): a book of kind journal under its log id, whose
        // book id is SHA-256 of its log id and which no profile describes.
        let journal = matches!(log, CadenceLog::Book(_))
            && log_id == super::JOURNAL_LOG_ID
            && store::log_kind(&self.shelf.shared().db(), &log_id)?.as_deref()
                == Some(store::KIND_JOURNAL);
        let book_id = if journal {
            sha256_hex(log_id.as_bytes())
        } else {
            self.shelf.book_id_of(log)?
        };
        // Only a book is described by a profile; a host log's fact is always
        // profile `none` with the empty link value and {} (§2.4, §4).
        let values = match (&self.cfg.status, log) {
            (Some(status), CadenceLog::Book(_)) if !journal => {
                status(&book_id, &log_id).map_err(ShelfError::Invalid)?
            }
            _ => None,
        };
        let values = values.unwrap_or_else(|| StatusValues {
            profile: PROFILE_NONE.into(),
            ..Default::default()
        });
        let mut size = log.mmr_size()?;
        let grown = last.is_none_or(|l| size > l.size);
        let (checkpoint_sha256, statement) = if grown {
            let cp = log
                .cut_local(at)
                .map_err(|e| ShelfError::Log(format!("checkpoint {log_id} for the stamp: {e}")))?;
            size = cp.tree_size;
            (sha256_hex(&cp.statement), Some(cp.statement))
        } else {
            (
                last.expect("not grown has a last fact")
                    .checkpoint_sha256
                    .clone(),
                None,
            )
        };
        Ok(PendingFact {
            profile: values.profile,
            book_id,
            log_id,
            size,
            checkpoint_sha256,
            prev_fact_size: last.map_or(0, |l| l.size),
            last_received: values.last_received,
            checked_over: values.checked_over,
            status: values.status,
            statement,
            grown,
            journal,
        })
    }

    /// Draw each fact's salts in append order (facts in book id, then log
    /// id, byte order), then
    /// the jitter, and return the stamp as a tick to store.
    fn build_stamp_locked(
        &self,
        state: &mut ShelfState,
        n: u64,
        at: Time,
        now: Time,
        mut facts: Vec<PendingFact>,
        head: Vec<BatchEntry>,
    ) -> Result<CadenceTick, ShelfError> {
        facts.sort_by(|a, b| (&a.book_id, &a.log_id).cmp(&(&b.book_id, &b.log_id)));
        let mut batch = StampBatch {
            v: 1,
            entries: head,
        };
        let mut leaves = TickLeaves::default();
        for f in &facts {
            let input = FactInput {
                profile: &f.profile,
                book_id: &f.book_id,
                log_id: &f.log_id,
                size: f.size,
                checkpoint_sha256: &f.checkpoint_sha256,
                prev_fact_size: f.prev_fact_size,
                last_received: f.last_received.clone(),
                checked_over: f.checked_over.clone(),
                status: f.status.clone(),
            };
            // Checked before its salts are drawn, as Go's newBookFact and
            // newJournalFact do, so a refused input consumes no random bytes.
            if f.journal {
                shelf_facts::check_journal_fact(
                    &f.log_id,
                    f.size,
                    &f.checkpoint_sha256,
                    f.prev_fact_size,
                )
            } else {
                shelf_facts::check_book_fact(&input)
            }
            .map_err(invalid)?;
            let link_salt = state.random.bytes(32)?;
            let status_salt = state.random.bytes(32)?;
            let salts = [link_salt.as_slice(), status_salt.as_slice()];
            let (kind, fact) = if f.journal {
                let fact = journal_fact(
                    &f.log_id,
                    f.size,
                    &f.checkpoint_sha256,
                    f.prev_fact_size,
                    salts,
                );
                (FACT_KIND_JOURNAL, fact)
            } else {
                (FACT_KIND_BOOK, book_fact(input, salts))
            };
            let fact = fact.map_err(invalid)?;
            let (_, doc) = entry_id(&fact.doc).map_err(invalid)?;
            batch.entries.push(BatchEntry {
                kind: kind.into(),
                book_id: f.book_id.clone(),
                log_id: f.log_id.clone(),
                doc,
                link_opening: opening_bytes(&fact.link_opening)?,
                status_opening: opening_bytes(&fact.status_opening)?,
            });
            // A journal fact's leaf keeps its true size and checkpoint, which
            // only the holder reads: the cards' extents leave the journal out.
            leaves.deals.insert(
                f.log_id.clone(),
                TickLeaf {
                    log_id: f.log_id.clone(),
                    size: f.size,
                    checkpoint_sha256: f.checkpoint_sha256.clone(),
                    salt: String::new(),
                    index: 0,
                    statement: f
                        .statement
                        .as_ref()
                        .map(|s| URL_SAFE_NO_PAD.encode(s))
                        .unwrap_or_default(),
                },
            );
        }
        let (_, stamp_doc) = entry_id(&stamp_entry(n, facts.len() as u64)).map_err(invalid)?;
        batch.entries.push(BatchEntry {
            kind: ENTRY_KIND_STAMP.into(),
            book_id: String::new(),
            log_id: String::new(),
            doc: stamp_doc,
            link_opening: Vec::new(),
            status_opening: Vec::new(),
        });
        // The jitter is uniform over [-jitter, +jitter] in nanoseconds, as a
        // tick's.
        let mut jitter: i128 = 0;
        if !self.cfg.jitter.is_zero() {
            let j = self.cfg.jitter.as_nanos() as u64;
            jitter = i128::from(state.random.uniform(2 * j + 1)?) - i128::from(j);
        }
        leaves.stamp = Some(batch);
        Ok(CadenceTick {
            n,
            at,
            due_next: now
                .plus(self.cfg.interval)
                .add_nanos(jitter)
                .truncate(Duration::from_secs(1)),
            entry_seq: 0,
            checkpoint_size: 0,
            checkpoint: Vec::new(),
            leaves,
            witnessed: false,
            receipt: None,
        })
    }

    /// Take a stamp now (§2.5; Go `Stamp`): on demand, whatever the
    /// heartbeat's schedule. A stamp stored but not finished is finished
    /// first, as the same stamp. It needs a migrated shelf.
    pub fn stamp(&self, now: Time) -> Result<TickResult, ShelfError> {
        let mut state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        self.stamp_locked(&mut state, now)
    }

    pub(crate) fn stamp_locked(
        &self,
        state: &mut ShelfState,
        now: Time,
    ) -> Result<TickResult, ShelfError> {
        if migration_state(&self.shelf.shared().db())? != MIGRATION_DONE {
            return Err(ShelfError::Invalid(
                "the shelf is not a log of book facts yet; call migrate".into(),
            ));
        }
        let last = self.last_locked()?;
        if let Some(last) = &last {
            if last.checkpoint_size == 0 {
                return self.finish_stamp_locked(last.clone());
            }
        }
        let n = last.as_ref().map_or(1, |l| l.n + 1);
        let at = now.truncate(Duration::from_secs(60));
        let mut facts = Vec::new();
        for id in self.shelf.cadence_log_ids()? {
            let log = self.shelf.cadence_log_locked(state, &id)?;
            if log.mmr_size()? == 0 {
                continue;
            }
            let prev = self.shelf.last_fact_of(&id)?;
            let f = self.fact_for(&log, at, prev.as_ref())?;
            if f.changed(prev.as_ref())? {
                facts.push(f);
            }
        }
        let tick = self.build_stamp_locked(state, n, at, now, facts, Vec::new())?;
        // Stored before anything is appended, so a crash resumes this stamp.
        insert_tick(&self.shelf.shared().db(), &tick)?;
        self.finish_stamp_locked(tick)
    }

    /// Append the batch's entries not yet in the cadence log, in order,
    /// record each in `shelf_entries`, then cut the checkpoint and complete
    /// the tick's row (Go `finishStampLocked`). Entries already appended (a
    /// crash after some) are found after the previous tick's checkpoint,
    /// never appended again.
    pub(crate) fn finish_stamp_locked(
        &self,
        mut tick: CadenceTick,
    ) -> Result<TickResult, ShelfError> {
        let batch = tick
            .leaves
            .stamp
            .clone()
            .ok_or_else(|| ShelfError::Corrupt(format!("tick {} is not a stamp", tick.n)))?;
        let mut after = 0;
        if tick.n > 1 {
            let previous = self.tick_locked(tick.n - 1)?;
            // Every entry the previous tick's checkpoint covers, an old
            // tick's padding included.
            after = cll::mmr::leaf_count(previous.checkpoint_size).map_err(|_| {
                ShelfError::Corrupt(format!(
                    "tick {}'s checkpoint size {} is not a complete MMR",
                    previous.n, previous.checkpoint_size
                ))
            })?;
        }
        let held = self.log.entries(after, batch.entries.len() + 1)?;
        let mut last_seq = 0;
        for (i, e) in batch.entries.iter().enumerate() {
            let id: [u8; 32] = Sha256::digest(&e.doc).into();
            let seq = match held.get(i) {
                Some(h) => {
                    if h.value != id {
                        return Err(ShelfError::Corrupt(format!(
                            "the cadence log holds another entry where stamp {}'s entry {i} belongs",
                            tick.n
                        )));
                    }
                    h.seq
                }
                None => self.log.append(&id, tick.at)?,
            };
            let opening = |b: &Vec<u8>| (!b.is_empty()).then(|| b.clone());
            self.shelf.shared().db().execute(
                "INSERT OR IGNORE INTO shelf_entries (entry_seq, kind, book_id, log_id, doc, link_opening, status_opening, tick) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                params![seq as i64, e.kind, e.book_id, e.log_id, e.doc, opening(&e.link_opening), opening(&e.status_opening), tick.n as i64],
            )?;
            last_seq = seq;
        }
        // One checkpoint over the whole batch, however long.
        let cp = self.log.checkpoint_whole(tick.at)?;
        tick.entry_seq = last_seq;
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
}

/// Store a tick unfinished: a zero checkpoint size marks it.
fn insert_tick(conn: &Connection, tick: &CadenceTick) -> Result<(), ShelfError> {
    conn.execute(
        "INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) VALUES (?, ?, ?, 0, 0, x'', ?)",
        params![tick.n as i64, tick.at.rfc3339(), tick.due_next.rfc3339(), tick.leaves.to_json()],
    )?;
    Ok(())
}

/// Make the snapshot the version 1 file it was before step 2.
fn reset_backup(path: &Path) -> Result<(), ShelfError> {
    let conn = Connection::open(path)?;
    let reset = (|| -> rusqlite::Result<()> {
        conn.execute("UPDATE shelf_meta SET version = 1 WHERE id = 1", [])?;
        conn.execute("DELETE FROM shelf_migration", [])?;
        conn.pragma_update(None, "journal_mode", "DELETE")?;
        Ok(())
    })();
    reset.map_err(|e| ShelfError::Invalid(format!("reset the backup to version 1: {e}")))?;
    conn.close().map_err(|(_, e)| e)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    //! Go's `shelf_stamp_test.go`, case for case (the witnessed stamp is
    //! the storage interlock's: the migrate phase witnesses one).
    use super::*;
    use crate::record::EpistemicType;
    use crate::shelf::{BookEntry, CadenceConfig, SeededStream, ShelfConfig, SHELF_VERSION};
    use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
    use capsule_emit_artifact::Store as _;
    use ed25519_dalek::SigningKey;
    use std::collections::{BTreeMap, HashMap};
    use std::sync::Mutex;

    fn key(label: &str) -> SigningKey {
        SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
    }

    fn tick_time() -> Time {
        Time::parse("2026-10-31T12:00:30Z").unwrap()
    }

    fn open(path: &Path) -> Shelf {
        let signer = key("signer");
        let conn = Connection::open(path).unwrap();
        conn.busy_timeout(Duration::from_secs(5)).unwrap();
        let originals =
            ArtifactSqlite::new(conn, "test", [signer.verifying_key().to_bytes()]).unwrap();
        originals.init().unwrap();
        Shelf::open_sqlite(ShelfConfig {
            path: path.to_path_buf(),
            lock_path: None,
            held_lock: None,
            lock_timeout: Some(Duration::from_millis(200)),
            random: Some(Box::new(SeededStream::new("stamp tests"))),
            operator: "test-operator".into(),
            developer: String::new(),
            signer: Arc::new(signer),
            checkpoint_key: key("checkpoint"),
            originals: Arc::new(originals),
            now: Some(Arc::new(tick_time)),
            authenticated_indexes: None,
            correlation_members: Vec::new(),
        })
        .unwrap()
    }

    /// A fixed profile and status per log.
    type Values = Arc<Mutex<HashMap<String, Value>>>;

    fn stamp_cadence(shelf: &Shelf) -> (Cadence, Values) {
        let values: Values = Arc::default();
        let held = values.clone();
        let cadence = shelf
            .cadence(CadenceConfig {
                log_id: "deal-cadence/0123456789abcdef".into(),
                interval: Duration::from_secs(300),
                jitter: Duration::ZERO,
                no_jitter: true,
                witness_ids: Vec::new(),
                witnesses: None,
                pad_bucket: 1,
                ended: Arc::new(|_| Ok(false)),
                status: Some(Arc::new(move |_, log_id| {
                    Ok(held.lock().unwrap().get(log_id).map(|status| StatusValues {
                        profile: "deal".into(),
                        status: Some(status.clone()),
                        ..Default::default()
                    }))
                })),
            })
            .unwrap();
        (cadence, values)
    }

    fn append(shelf: &Shelf, book: &str, payload: &str) {
        let mut entry = BookEntry::new("observation", EpistemicType::ObservedEvent);
        entry.subject_ref = Some(book.into());
        entry.payloads = vec![payload.as_bytes().to_vec()];
        shelf.open(book).unwrap().append(&entry).unwrap();
    }

    /// The `shelf_entries` rows a tick appended: log ids by kind.
    fn entries_of_tick(shelf: &Shelf, n: u64) -> BTreeMap<String, Vec<String>> {
        let db = shelf.shared().db();
        let mut stmt = db
            .prepare("SELECT kind, log_id FROM shelf_entries WHERE tick = ? ORDER BY entry_seq")
            .unwrap();
        let mut out: BTreeMap<String, Vec<String>> = BTreeMap::new();
        for row in stmt
            .query_map([n as i64], |r| Ok((r.get::<_, String>(0)?, r.get(1)?)))
            .unwrap()
        {
            let (kind, log) = row.unwrap();
            out.entry(kind).or_default().push(log);
        }
        out
    }

    fn version(conn: &Connection) -> i64 {
        conn.query_row("SELECT version FROM shelf_meta WHERE id = 1", [], |r| {
            r.get(0)
        })
        .unwrap()
    }

    fn minutes(n: u64) -> Time {
        tick_time().plus(Duration::from_secs(60 * n))
    }

    #[test]
    fn migrate_then_stamp_writes_facts_only_on_change() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("shelf.db");
        let shelf = open(&path);
        let (c, values) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        append(&shelf, "deal/b", "1");
        let old = c.tick(tick_time()).unwrap().tick.unwrap();

        // A stamp before migrate is refused; migrate writes the marker, a
        // genesis fact for each log and a stamp entry, as the tick after the
        // last one.
        assert!(matches!(c.stamp(tick_time()), Err(ShelfError::Invalid(_))));
        c.migrate(minutes(1)).unwrap();
        assert!(c.migrated().unwrap());
        assert_eq!(version(&shelf.shared().db()), SHELF_VERSION_FACTS);
        let genesis = entries_of_tick(&shelf, old.n + 1);
        assert_eq!(genesis[ENTRY_KIND_FORMAT_SWITCH], [""]);
        let mut facts = genesis[FACT_KIND_BOOK].clone();
        facts.sort();
        assert_eq!(facts, ["deal/a", "deal/b"]);
        assert_eq!(genesis[ENTRY_KIND_STAMP].len(), 1);
        c.migrate(minutes(2)).unwrap(); // again: a no-op

        // The backup is the version 1 file an older build opens.
        let bak = Connection::open(with_suffix(&path, ".v1.bak")).unwrap();
        assert_eq!(version(&bak), SHELF_VERSION);
        let rows: i64 = bak
            .query_row("SELECT COUNT(*) FROM shelf_migration", [], |r| r.get(0))
            .unwrap();
        assert_eq!(rows, 0);

        // A quiet stamp: no fact, but a stamp entry and a new checkpoint.
        let quiet = c.stamp(minutes(3)).unwrap().tick.unwrap();
        let got = entries_of_tick(&shelf, quiet.n);
        assert!(!got.contains_key(FACT_KIND_BOOK));
        assert_eq!(got[ENTRY_KIND_STAMP].len(), 1);
        let previous = c.get(quiet.n - 1).unwrap();
        assert!(
            quiet.checkpoint_size > previous.checkpoint_size,
            "an empty stamp still grows the log"
        );

        // A grown book gets a fact; a quiet one does not.
        append(&shelf, "deal/a", "2");
        let grown = c.stamp(minutes(4)).unwrap().tick.unwrap();
        assert_eq!(entries_of_tick(&shelf, grown.n)[FACT_KIND_BOOK], ["deal/a"]);
        assert!(!grown.leaves.deals["deal/a"].statement.is_empty());

        // A changed status value with no new record (received material)
        // gets a fact with the same size and checkpoint.
        values
            .lock()
            .unwrap()
            .insert("deal/b".into(), json!({"rows": [{"id": "paid_as_agreed"}]}));
        let received = c.stamp(minutes(5)).unwrap().tick.unwrap();
        assert_eq!(
            entries_of_tick(&shelf, received.n)[FACT_KIND_BOOK],
            ["deal/b"]
        );
        assert!(
            received.leaves.deals["deal/b"].statement.is_empty(),
            "no new checkpoint"
        );
        let doc: Vec<u8> = shelf
            .shared()
            .db()
            .query_row(
                "SELECT doc FROM shelf_entries WHERE log_id = 'deal/b' AND kind = ? ORDER BY entry_seq DESC LIMIT 1",
                [FACT_KIND_BOOK],
                |r| r.get(0),
            )
            .unwrap();
        let fact: Value = serde_json::from_slice(&doc).unwrap();
        assert_eq!(fact["size"], fact["prev_fact_size"]);
        assert_eq!(fact["profile"], "deal");
        let again = c.stamp(minutes(6)).unwrap().tick.unwrap();
        assert!(
            !entries_of_tick(&shelf, again.n).contains_key(FACT_KIND_BOOK),
            "the same values again: no fact"
        );

        // The heartbeat stamps after the switch; before it is due, nothing.
        assert!(!c.tick(minutes(7)).unwrap().ticked);
        let beat = c.tick(again.due_next).unwrap().tick.unwrap();
        assert!(beat.leaves.stamp.is_some());
        assert_eq!(beat.n, again.n + 1);

        // /1's anchor still counts every book, the record added after
        // migration included.
        let before = c.standard_history_anchor(old.n).unwrap();
        let anchor = c.standard_history_anchor(0).unwrap();
        for book in ["deal/a", "deal/b"] {
            let leaves = shelf.open(book).unwrap().0.log.size().unwrap();
            assert_eq!(anchor.extents[book], leaves, "{book}: every leaf it holds");
        }
        assert!(anchor.extents["deal/a"] > before.extents["deal/a"]);
        assert_eq!(anchor.extents["deal/b"], before.extents["deal/b"]);
        shelf.release().unwrap();
    }

    #[test]
    fn a_stamp_stored_but_not_finished_is_finished_first() {
        let dir = tempfile::tempdir().unwrap();
        let shelf = open(&dir.path().join("shelf.db"));
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        c.migrate(tick_time()).unwrap();
        append(&shelf, "deal/a", "2");

        // Store a stamp and append only its first entry, as a crash would
        // leave it.
        let tick = {
            let mut state = shelf.lock();
            let last = c.last_locked().unwrap().unwrap();
            let id = &shelf.cadence_log_ids().unwrap()[0];
            let log = shelf.cadence_log_locked(&mut state, id).unwrap();
            let prev = shelf.last_fact_of(id).unwrap();
            let f = c.fact_for(&log, tick_time(), prev.as_ref()).unwrap();
            let tick = c
                .build_stamp_locked(
                    &mut state,
                    last.n + 1,
                    tick_time(),
                    tick_time(),
                    vec![f],
                    Vec::new(),
                )
                .unwrap();
            insert_tick(&shelf.shared().db(), &tick).unwrap();
            let first = &tick.leaves.stamp.as_ref().unwrap().entries[0];
            c.log.append(&Sha256::digest(&first.doc), tick.at).unwrap();
            tick
        };

        // The next stamp finishes that one, appending only what is missing.
        let result = c.stamp(minutes(60)).unwrap().tick.unwrap();
        assert_eq!(result.n, tick.n);
        assert_eq!(result.leaves, tick.leaves);
        let got = entries_of_tick(&shelf, tick.n);
        assert_eq!(got[FACT_KIND_BOOK], ["deal/a"]);
        assert_eq!(got[ENTRY_KIND_STAMP].len(), 1);
        let entries = c.log.entries(0, 1000).unwrap();
        let mut seen = HashMap::new();
        for e in &entries {
            *seen.entry(e.value.clone()).or_insert(0) += 1;
        }
        assert!(seen.values().all(|&n| n == 1), "each entry appended once");
        // Every entry of the stamp is recorded at its position.
        let recorded: i64 = shelf
            .shared()
            .db()
            .query_row(
                "SELECT COUNT(*) FROM shelf_entries WHERE tick = ?",
                [tick.n as i64],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(recorded, 2);
        shelf.release().unwrap();
    }

    #[test]
    fn migrate_resumes_after_a_backup_and_refuses_a_foreign_one() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("shelf.db");
        let shelf = open(&path);
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");

        // A foreign <path>.v1.bak: migrate refuses and writes nothing past
        // the version switch.
        let (bak, tmp) = (
            with_suffix(&path, ".v1.bak"),
            with_suffix(&path, ".v1.bak.tmp"),
        );
        std::fs::write(&bak, b"not this shelf").unwrap();
        assert!(matches!(
            c.migrate(tick_time()),
            Err(ShelfError::Invalid(_))
        ));
        assert_eq!(
            migration_state(&shelf.shared().db()).unwrap(),
            MIGRATION_BACKING_UP
        );
        assert!(c.last().unwrap().is_none(), "no stamp is stored");
        // While a migration is under way neither a tree tick nor a stamp is
        // cut.
        assert!(matches!(c.tick(tick_time()), Err(ShelfError::Invalid(_))));
        assert!(matches!(c.stamp(tick_time()), Err(ShelfError::Invalid(_))));

        // Moved aside, migrate resumes from where it stopped; a leftover
        // temporary file from an unfinished attempt is replaced.
        std::fs::remove_file(&bak).unwrap();
        std::fs::write(&tmp, b"partial").unwrap();
        c.migrate(tick_time()).unwrap();
        assert!(c.migrated().unwrap());
        assert!(!tmp.exists());
        let recorded: String = shelf
            .shared()
            .db()
            .query_row(
                "SELECT backup_sha256 FROM shelf_migration WHERE id = 1",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(file_sha256(&bak).unwrap(), recorded);
        shelf.release().unwrap();
    }

    #[test]
    fn a_migrated_shelf_reopens_and_stamps() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("shelf.db");
        let shelf = open(&path);
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        c.migrate(tick_time()).unwrap();
        shelf.release().unwrap();
        let reopened = open(&path);
        assert_eq!(version(&reopened.shared().db()), SHELF_VERSION_FACTS);
        let (c2, _) = stamp_cadence(&reopened);
        assert!(c2.migrated().unwrap());
        let stamp = c2.stamp(minutes(1)).unwrap().tick.unwrap();
        assert_eq!(stamp.n, 2);
        // The stored leaves read back as written.
        assert_eq!(c2.get(stamp.n).unwrap().leaves, stamp.leaves);
        reopened.release().unwrap();
    }

    #[test]
    fn each_audience_gets_exactly_its_openings() {
        let dir = tempfile::tempdir().unwrap();
        let shelf = open(&dir.path().join("shelf.db"));
        let (c, values) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        values
            .lock()
            .unwrap()
            .insert("deal/a".into(), json!({"rows": []}));
        c.migrate(tick_time()).unwrap();
        let book_id: String = shelf
            .shared()
            .db()
            .query_row(
                "SELECT book_id FROM shelf_entries WHERE kind = ?",
                [FACT_KIND_BOOK],
                |r| r.get(0),
            )
            .unwrap();
        for audience in [
            "counterparty",
            "adjudicator",
            "asker",
            "chosen_reader",
            "owner",
            "auditor",
        ] {
            let facts = shelf.openings(&book_id, audience).unwrap();
            assert_eq!(facts.len(), 1, "{audience}");
            let (link, status) = openings_for(FACT_KIND_BOOK, audience).unwrap();
            let f = &facts[0];
            assert_eq!(f.link_opening.is_some(), link, "{audience} link");
            assert_eq!(f.status_opening.is_some(), status, "{audience} status");
            shelf_facts::verify_openings(
                &f.fact,
                f.link_opening.as_ref(),
                f.status_opening.as_ref(),
            )
            .unwrap();
        }
        assert!(matches!(
            shelf.openings(&book_id, "stranger"),
            Err(ShelfError::Invalid(_))
        ));
        shelf.release().unwrap();
    }

    #[test]
    fn checkpoint_whole_cuts_one_checkpoint_over_a_long_batch() {
        use cll::log::WitnessStateStore as _;
        let dir = tempfile::tempdir().unwrap();
        let log = crate::shelf::log::Log::open(
            &dir.path().join("log.db"),
            "deal-cadence/0123456789abcdef",
            &key("checkpoint"),
            &["https://witness.example".to_string()],
            true,
        )
        .unwrap();
        for i in 0..2500u64 {
            let mut value = [0u8; 32];
            value[24..].copy_from_slice(&(i + 1).to_be_bytes());
            log.append(&value, tick_time()).unwrap();
        }
        let cp = log.checkpoint_whole(tick_time()).unwrap();
        assert_eq!(cp.tree_size, crate::shelf::cadence::mmr_size(2500));
        let pending = log
            .store
            .pending_witnesses(crate::shelf::log::timestamp(minutes(60)), 1)
            .unwrap();
        assert_eq!(pending.len(), 1);
        assert_eq!(
            pending[0].checkpoint_size, cp.tree_size,
            "the first delivery queued is the whole batch's checkpoint, not one over part of it"
        );
        log.release().unwrap();
    }

    #[test]
    fn a_refused_fact_draws_no_salt() {
        let a = tempfile::tempdir().unwrap();
        let shelf = open(&a.path().join("shelf.db"));
        let expected = shelf.lock().random.bytes(32).unwrap();
        shelf.release().unwrap();

        let b = tempfile::tempdir().unwrap();
        let shelf = open(&b.path().join("shelf.db"));
        let (c, _) = stamp_cadence(&shelf);
        let refused = PendingFact {
            profile: "deal".into(),
            book_id: "not a digest".into(),
            log_id: "deal/a".into(),
            size: 1,
            checkpoint_sha256: "00".repeat(32),
            prev_fact_size: 0,
            last_received: None,
            checked_over: Vec::new(),
            status: None,
            statement: None,
            grown: true,
            journal: false,
        };
        let mut state = shelf.lock();
        let built = c.build_stamp_locked(
            &mut state,
            1,
            tick_time(),
            tick_time(),
            vec![refused],
            Vec::new(),
        );
        assert!(matches!(built, Err(ShelfError::Invalid(_))));
        assert_eq!(
            state.random.bytes(32).unwrap(),
            expected,
            "the stream is where it was"
        );
        drop(state);
        shelf.release().unwrap();
    }

    #[test]
    fn migrate_reuses_a_backup_it_recorded() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("shelf.db");
        let shelf = open(&path);
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        // Steps 1 and 2 by hand, stopping before the rename.
        let tmp = with_suffix(&path, ".v1.bak.tmp");
        let digest = {
            let db = shelf.shared().db();
            db.execute(
                "UPDATE shelf_meta SET version = ? WHERE id = 1",
                [SHELF_VERSION_FACTS],
            )
            .unwrap();
            db.execute(
                "INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, ?, '')",
                [MIGRATION_BACKING_UP],
            )
            .unwrap();
            db.execute("VACUUM INTO ?", [tmp.to_str().unwrap()])
                .unwrap();
            reset_backup(&tmp).unwrap();
            let digest = file_sha256(&tmp).unwrap();
            db.execute(
                "UPDATE shelf_migration SET backup_sha256 = ? WHERE id = 1",
                [&digest],
            )
            .unwrap();
            digest
        };

        c.migrate(tick_time()).unwrap();
        assert_eq!(
            file_sha256(&with_suffix(&path, ".v1.bak")).unwrap(),
            digest,
            "the recorded backup is the one in place"
        );
        assert!(!tmp.exists());
        assert!(c.migrated().unwrap());
        shelf.release().unwrap();
    }

    #[test]
    fn a_host_log_is_never_asked_for_its_status() {
        let dir = tempfile::tempdir().unwrap();
        let shelf = open(&dir.path().join("shelf.db"));
        let (c, values) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        let host = shelf.host_log("legacy/old", true).unwrap();
        host.write(|stores| -> Result<(), ShelfError> {
            use cll::log::EntryStore as _;
            stores
                .log
                .append(&cll::log::AppendInput {
                    value: Sha256::digest(b"legacy").to_vec(),
                    appended_at: crate::shelf::log::timestamp(tick_time()),
                })
                .map_err(|e| ShelfError::Log(e.to_string()))?;
            Ok(())
        })
        .unwrap();
        // The status function answers for every log, the host log included.
        for id in ["deal/a", "legacy/old"] {
            values
                .lock()
                .unwrap()
                .insert(id.into(), json!({"rows": [{"id": "x"}]}));
        }
        c.migrate(tick_time()).unwrap();
        let fact_of = |log_id: &str| -> (Value, Value, Value) {
            let (doc, lo, so): (Vec<u8>, Vec<u8>, Vec<u8>) = shelf
                .shared()
                .db()
                .query_row(
                    "SELECT doc, link_opening, status_opening FROM shelf_entries WHERE log_id = ? AND kind = ?",
                    params![log_id, FACT_KIND_BOOK],
                    |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
                )
                .unwrap();
            (
                serde_json::from_slice(&doc).unwrap(),
                opening_value(&lo).unwrap(),
                opening_value(&so).unwrap(),
            )
        };
        let (book, _, book_status) = fact_of("deal/a");
        assert_eq!(book["profile"], "deal");
        assert_eq!(book_status, json!({"rows": [{"id": "x"}]}));
        let (host_fact, link, status) = fact_of("legacy/old");
        assert_eq!(host_fact["profile"], PROFILE_NONE);
        assert_eq!(link, json!({"checked_over": [], "last_received": null}));
        assert_eq!(status, json!({}));
        shelf.release().unwrap();
    }

    #[test]
    fn a_batch_and_an_opening_are_stored_as_go_stores_them() {
        let entry = BatchEntry {
            kind: ENTRY_KIND_STAMP.into(),
            book_id: String::new(),
            log_id: String::new(),
            doc: b"{}".to_vec(),
            link_opening: Vec::new(),
            status_opening: Vec::new(),
        };
        let leaves = TickLeaves {
            stamp: Some(StampBatch {
                v: 1,
                entries: vec![entry],
            }),
            ..Default::default()
        };
        assert_eq!(
            leaves.to_json(),
            r#"{"deals":{},"filler_index":0,"filler_nonce":"","stamp":{"v":1,"entries":[{"kind":"stamp","doc":"e30="}]}}"#
        );
        let back: TickLeaves = serde_json::from_str(&leaves.to_json()).unwrap();
        assert_eq!(back, leaves);
        let opening = Opening {
            salt: "00".repeat(32),
            value: json!({"b": "<&>", "a": [1]}),
        };
        // An opening is stored as RFC 8785 bytes, with no HTML escaping.
        assert_eq!(
            String::from_utf8(opening_bytes(&opening).unwrap()).unwrap(),
            format!(
                "{{\"salt\":\"{}\",\"value\":{{\"a\":[1],\"b\":\"<&>\"}}}}",
                "00".repeat(32)
            )
        );
    }

    // Go's shelf_journal_test.go, case for case.

    /// A migrated shelf with one deal book and a journal holding one record,
    /// stamped once after the journal's first record.
    fn migrated_with_journal(dir: &Path) -> (Shelf, Cadence, crate::shelf::ShelfBook) {
        let shelf = open(&dir.join("shelf.db"));
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        c.tick(tick_time()).unwrap();
        c.migrate(minutes(1)).unwrap();
        let journal = shelf.journal().unwrap();
        journal_append(&journal, "1");
        c.stamp(minutes(2)).unwrap();
        (shelf, c, journal)
    }

    fn journal_append(journal: &crate::shelf::ShelfBook, payload: &str) {
        let mut entry = BookEntry::new("observation", EpistemicType::ObservedEvent);
        entry.subject_ref = Some("journal".into());
        entry.payloads = vec![payload.as_bytes().to_vec()];
        journal.append(&entry).unwrap();
    }

    fn journal_facts(shelf: &Shelf) -> Vec<shelf_facts::BookFact> {
        let db = shelf.shared().db();
        let mut stmt = db
            .prepare("SELECT doc FROM shelf_entries WHERE kind = ? ORDER BY entry_seq")
            .unwrap();
        let rows = stmt
            .query_map([FACT_KIND_JOURNAL], |r| r.get::<_, Vec<u8>>(0))
            .unwrap();
        rows.map(|doc| serde_json::from_slice(&doc.unwrap()).unwrap())
            .collect()
    }

    #[test]
    fn the_journal_gets_a_journal_fact_that_hides_its_size() {
        let dir = tempfile::tempdir().unwrap();
        let (shelf, _, _) = migrated_with_journal(dir.path());
        let facts = journal_facts(&shelf);
        assert_eq!(facts.len(), 1);
        let f = &facts[0];
        assert_eq!(f.log_id, crate::shelf::JOURNAL_LOG_ID);
        assert_eq!(f.size, 0);
        assert_eq!(f.checkpoint_sha256, "");
        assert_eq!(f.prev_fact_size, 0);
        assert_eq!(f.profile, PROFILE_NONE);

        // The owner's opening holds the true size and checkpoint.
        let got = shelf.openings(&f.book_id, "owner").unwrap();
        assert_eq!(got.len(), 1);
        let status = &got[0].status_opening.as_ref().unwrap().value;
        assert_ne!(status["size"].as_u64().unwrap(), 0);
        let sum = status["checkpoint_sha256"].as_str().unwrap();
        assert!(sum.len() == 64 && sum.bytes().all(|c| c.is_ascii_hexdigit()));
        shelf_facts::verify_openings(
            &got[0].fact,
            got[0].link_opening.as_ref(),
            got[0].status_opening.as_ref(),
        )
        .unwrap();
        shelf.release().unwrap();
    }

    // The journal follows the write rule: a fact only when it grew.
    #[test]
    fn the_journal_gets_a_fact_only_when_it_grows() {
        let dir = tempfile::tempdir().unwrap();
        let (shelf, c, journal) = migrated_with_journal(dir.path());
        c.stamp(minutes(3)).unwrap();
        assert_eq!(
            journal_facts(&shelf).len(),
            1,
            "a quiet journal gets no fact"
        );
        journal_append(&journal, "2");
        c.stamp(minutes(4)).unwrap();
        let facts = journal_facts(&shelf);
        assert_eq!(facts.len(), 2);

        let got = shelf.openings(&facts[0].book_id, "owner").unwrap();
        assert_eq!(got.len(), 2);
        let first = &got[0].status_opening.as_ref().unwrap().value;
        let second = &got[1].status_opening.as_ref().unwrap().value;
        assert_eq!(first["size"], second["prev_fact_size"]);
        assert!(second["size"].as_u64() > first["size"].as_u64());
        shelf.release().unwrap();
    }

    // The journal counts toward no completeness and no tally: it is not one
    // of the shelf's books, has no extent on a card, and a handover returns
    // no latest fact for it.
    #[test]
    fn the_journal_is_in_no_count_and_no_handover() {
        let dir = tempfile::tempdir().unwrap();
        let (shelf, c, _) = migrated_with_journal(dir.path());
        assert_eq!(shelf.books().unwrap(), ["deal/a"]);
        let last = c.last().unwrap().unwrap();
        let anchor = c.standard_history_anchor(last.n).unwrap();
        assert!(!anchor.extents.contains_key(crate::shelf::JOURNAL_LOG_ID));
        assert!(anchor.extents.contains_key("deal/a"));

        let h = c.handover(0).unwrap();
        let key = super::super::log::Checkpoint::from_statement(&h.anchor)
            .unwrap()
            .key_id;
        let got = crate::shelf::verify_handover(&h, &key, None).unwrap();
        assert!(got
            .latest
            .iter()
            .all(|l| l.fact.log_id != crate::shelf::JOURNAL_LOG_ID));
        let mut journal_docs = 0;
        for d in &h.documents {
            let f: Value = serde_json::from_slice(&d.doc).unwrap();
            if f["kind"] == FACT_KIND_JOURNAL {
                journal_docs += 1;
                assert!(
                    d.status_opening.is_none(),
                    "the journal's opening is never handed over"
                );
            }
        }
        assert_eq!(journal_docs, 1);
        shelf.release().unwrap();
    }

    #[test]
    fn the_journal_is_opened_only_as_the_journal() {
        let dir = tempfile::tempdir().unwrap();
        let (shelf, _, journal) = migrated_with_journal(dir.path());
        let again = shelf.journal().unwrap();
        assert!(Arc::ptr_eq(&journal.0, &again.0), "the same journal");
        assert!(
            matches!(
                shelf.open(crate::shelf::JOURNAL_LOG_ID),
                Err(ShelfError::Invalid(_))
            ),
            "open refuses the journal"
        );
        assert!(
            matches!(
                shelf.host_log(crate::shelf::JOURNAL_LOG_ID, true),
                Err(ShelfError::Invalid(_))
            ),
            "a host log cannot take the journal's id"
        );
        shelf.release().unwrap();
    }

    #[test]
    fn the_journal_needs_a_shelf_of_book_facts() {
        let dir = tempfile::tempdir().unwrap();
        let shelf = open(&dir.path().join("shelf.db"));
        assert!(matches!(shelf.journal(), Err(ShelfError::Invalid(_))));
        shelf.release().unwrap();
    }

    #[test]
    fn the_journal_refuses_a_log_that_already_has_its_id() {
        let dir = tempfile::tempdir().unwrap();
        let shelf = open(&dir.path().join("shelf.db"));
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, crate::shelf::JOURNAL_LOG_ID, "1");
        c.tick(tick_time()).unwrap();
        c.migrate(minutes(1)).unwrap();
        assert!(matches!(shelf.journal(), Err(ShelfError::Invalid(_))));
        shelf.release().unwrap();
    }

    // A file whose shelf_logs predates the journal's kind is rebuilt with
    // the same rows when the journal is first opened.
    #[test]
    fn an_older_shelf_logs_table_takes_the_journal() {
        let dir = tempfile::tempdir().unwrap();
        let shelf = open(&dir.path().join("shelf.db"));
        let (c, _) = stamp_cadence(&shelf);
        append(&shelf, "deal/a", "1");
        c.tick(tick_time()).unwrap();
        c.migrate(minutes(1)).unwrap();
        shelf
            .shared()
            .db()
            .execute_batch(
                "CREATE TABLE shelf_logs_old (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host')));
                 INSERT INTO shelf_logs_old SELECT log_id, kind FROM shelf_logs;
                 DROP TABLE shelf_logs;
                 ALTER TABLE shelf_logs_old RENAME TO shelf_logs;",
            )
            .unwrap();
        shelf.journal().unwrap();
        let db = shelf.shared().db();
        assert_eq!(
            store::log_kind(&db, "deal/a").unwrap().as_deref(),
            Some(store::KIND_BOOK)
        );
        assert_eq!(
            store::log_kind(&db, crate::shelf::JOURNAL_LOG_ID)
                .unwrap()
                .as_deref(),
            Some(store::KIND_JOURNAL)
        );
        drop(db);
        shelf.release().unwrap();
    }
}
