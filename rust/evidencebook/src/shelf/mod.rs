//! The shelf: one subject key's books in one SQLite file (evidencebook#254
//! §2), built with the cargo feature `shelf`.
//!
//! The file holds CLL's tables (one log per book id, the cadence log and
//! every host log), capsule-emit's artifact store (the originals of every
//! book and the records of every host log, under one namespace) and the
//! shelf's own tables (`book_*`, `cadence_*`, `shelf_meta`, `shelf_logs`),
//! exactly as the Go shelf writes them, so a Go, a Rust and a Python writer
//! continue each other's files. `testdata/shelf` holds the byte vectors and
//! `test/interop/shelf` the storage interlock that check it.
//!
//! - [`Shelf`] opens the file under the shelf's writer lock or a lock the
//!   host already holds ([`HeldLock`]), and hands out one [`ShelfBook`] per
//!   log id ([`Shelf::open`]).
//! - A [`HostLog`] is a log on the shelf whose records the host seals
//!   itself, on the shelf's CLL backend and artifact store, never converted
//!   into book records ([`Shelf::host_log`], [`Shelf::legacy`]).
//! - [`Cadence`] runs the heartbeat ticks that commit every live log's
//!   checkpoint as a salted leaf of a sparse tree whose root alone reaches
//!   the witness.
//!
//! This module is the one place the crate's public API names a `cll` type:
//! a host log hands the host the log's CLL backend ([`HostLogStores`]),
//! since the host's own log layer runs on it (§2.7).

mod book;
mod cadence;
mod chain;
mod gojson;
mod history;
mod import;
mod lock;
mod log;
mod random;
mod records;
mod store;
mod time;
mod tree;

pub use book::{BookEntry, BookRecord, Indexes, ShelfBook, RECORD_HEADER_MEMBER};
pub use cadence::{
    Cadence, CadenceConfig, CadenceTick, TickResult, WitnessCheck, WitnessReceipt, WITNESS_INVALID,
    WITNESS_NOT_CHECKED, WITNESS_VERIFIED,
};
pub use chain::CADENCE_EXTENSION;
pub use history::StandardHistoryAnchor;
pub use import::LegacyCadence;
pub use log::{Checkpoint, SUBSTRATE_KIND_CLL};
pub use random::{RandomSource, SeededStream, SystemRandom};
pub use records::{ShelfRecord, ShelfRecords};
pub use store::{StoredRecord, SHELF_VERSION};
pub use time::Time;
pub use tree::{
    cadence_empty, cadence_filler_leaf, cadence_fold, cadence_leaf, cadence_tree, derived_fillers,
    padding_record_id, TickLeaf, TickLeaves, CADENCE_DEPTH,
};

use self::lock::FileLock;
use self::log::Log;
use self::random::Draws;
use self::store::{KIND_BOOK, KIND_HOST};
use crate::substrate::Signer;
use capsule_emit_artifact::Store as ArtifactStore;
use ed25519_dalek::SigningKey;
use rusqlite::Connection;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant};

/// Why a shelf operation failed.
#[derive(Debug, thiserror::Error)]
pub enum ShelfError {
    /// The call or its configuration is not valid.
    #[error("invalid: {0}")]
    Invalid(String),
    /// Stored state is inconsistent.
    #[error("corrupt: {0}")]
    Corrupt(String),
    /// What was asked for does not exist.
    #[error("not found: {0}")]
    NotFound(String),
    /// A position is not covered by the checkpoint asked about.
    #[error("not covered: {0}")]
    NotCovered(String),
    /// The file's version, or the platform, is not one this build supports.
    #[error("unsupported: {0}")]
    Unsupported(String),
    /// The shelf was released.
    #[error("the shelf is released")]
    Released,
    /// Another writer holds the shelf's lock past the caller's deadline.
    #[error("{0}: another writer holds it")]
    LockHeld(String),
    /// The CLL log failed.
    #[error("log: {0}")]
    Log(String),
    /// The artifact store failed.
    #[error("originals: {0}")]
    Artifact(String),
    /// The random source failed.
    #[error("{0}")]
    Random(String),
    /// Sealing a record failed.
    #[error(transparent)]
    Seal(#[from] crate::seal::SealRecordError),
    /// The shelf's SQLite file failed.
    #[error("sqlite: {0}")]
    Sqlite(#[from] rusqlite::Error),
    /// A file failed.
    #[error("io: {0}")]
    Io(#[from] std::io::Error),
}

/// A writer lock the host holds on the shelf's store under its own rules
/// (evidencebook#254 §2.4). [`HeldLock::held`] reports `Ok` while the host
/// holds it; the shelf asks once, when it opens, and never releases it.
pub trait HeldLock: Send + Sync {
    /// `Ok` while the host holds the lock.
    fn held(&self) -> Result<(), String>;
}

/// The shelf's clock.
pub type Clock = Arc<dyn Fn() -> Time + Send + Sync>;

/// Which authenticated indexes each book gets, by log id.
pub type IndexChoice = Arc<dyn Fn(&str) -> Indexes + Send + Sync>;

/// What a shelf is opened with (Go `ShelfConfig`). The signer, checkpoint
/// key, each book's indexes, the originals' namespace and the cadence's
/// configuration are host parameters: every process that writes one shelf,
/// in any language, must use the same values (§2.2).
pub struct ShelfConfig {
    /// The SQLite file.
    pub path: PathBuf,
    /// The writer lock file; `None` is `<path>.lock`. A host whose other
    /// writers already lock a file of their own passes that one, so they
    /// and the shelf exclude each other. Must be `None` with `held_lock`.
    pub lock_path: Option<PathBuf>,
    /// The store's writer lock when the host already holds it: the shelf
    /// then takes no lock of its own, and opens on any platform.
    pub held_lock: Option<Box<dyn HeldLock>>,
    /// How long to wait for another holder of the shelf's own lock;
    /// `None` waits until it is free.
    pub lock_timeout: Option<Duration>,
    /// The shelf's one random source; `None` is the system CSPRNG.
    pub random: Option<Box<dyn RandomSource>>,
    pub operator: String,
    /// The capsules' `developer`; empty is `evidencebook`.
    pub developer: String,
    /// Seals every book's records.
    pub signer: Arc<dyn Signer + Send + Sync>,
    /// Signs every log's checkpoints.
    pub checkpoint_key: SigningKey,
    /// The shelf's one artifact store, which must trust `signer`'s key: the
    /// originals of every book and the records of every host log. For the
    /// contract's file it is capsule-emit's SQLite store on `path` (§2.5).
    pub originals: Arc<dyn ArtifactStore + Send + Sync>,
    /// The clock; `None` is the system clock.
    pub now: Option<Clock>,
    /// Each book's authenticated indexes; `None` gives every book the
    /// defaults.
    pub authenticated_indexes: Option<IndexChoice>,
    /// The profile's correlation key: the `correlation` member under which
    /// a book's [`crate::store::RecordSource::correlation`] holds a record
    /// that carries it. A record that carries none, or every record when
    /// this is `None`, is held under its `subject_ref`. The key's name is
    /// the profile's, never this crate's.
    pub correlation_key: Option<String>,
}

/// What every book and host log of a shelf shares.
pub(crate) struct ShelfShared {
    pub(crate) path: PathBuf,
    pub(crate) operator: String,
    pub(crate) developer: String,
    pub(crate) signer: Arc<dyn Signer + Send + Sync>,
    pub(crate) checkpoint_key: SigningKey,
    pub(crate) originals: Arc<dyn ArtifactStore + Send + Sync>,
    now: Option<Clock>,
    authenticated: Option<IndexChoice>,
    pub(crate) correlation_key: Option<String>,
    db: Mutex<Connection>,
}

impl ShelfShared {
    pub(crate) fn now(&self) -> Time {
        self.now.as_ref().map_or_else(Time::now, |f| f())
    }

    pub(crate) fn db(&self) -> MutexGuard<'_, Connection> {
        self.db.lock().unwrap_or_else(|p| p.into_inner())
    }
}

/// A host log's state on its shelf.
pub(crate) struct HostCore {
    pub(crate) id: String,
    pub(crate) log: Arc<Log>,
    pub(crate) in_cadence: bool,
}

pub(crate) struct ShelfState {
    pub(crate) books: HashMap<String, ShelfBook>,
    pub(crate) hosts: HashMap<String, Arc<HostCore>>,
    /// The cadence log's id and log, once a cadence is opened.
    pub(crate) cadence: Option<(String, Arc<Log>)>,
    pub(crate) random: Draws,
    pub(crate) released: bool,
    lock: Option<FileLock>,
}

pub(crate) struct ShelfInner {
    pub(crate) shared: Arc<ShelfShared>,
    pub(crate) state: Mutex<ShelfState>,
}

/// One subject key's books in one SQLite file (Go `Shelf`). The shelf holds
/// the writer lock from [`Shelf::open_sqlite`] to [`Shelf::release`] and is
/// the file's only writer. Handles are cheap clones of one shelf.
///
/// Each book keeps its own lock; operations across books, such as a
/// cadence tick, take the shelf's.
#[derive(Clone)]
pub struct Shelf(pub(crate) Arc<ShelfInner>);

impl Shelf {
    /// Take the writer lock (waiting up to `lock_timeout`) or check the
    /// host's held lock, and open the shelf's file, creating its tables when
    /// absent. A file whose `shelf_meta` version this build does not know is
    /// refused; one with no row is a fresh file or a shelf from before the
    /// table existed, and gets version 1 with `shelf_logs` filled from what
    /// it holds (§2.2).
    pub fn open_sqlite(cfg: ShelfConfig) -> Result<Shelf, ShelfError> {
        if cfg.path.as_os_str().is_empty() || cfg.operator.is_empty() {
            return Err(ShelfError::Invalid("path and operator are required".into()));
        }
        let lock = match &cfg.held_lock {
            Some(held) => {
                if cfg.lock_path.is_some() {
                    return Err(ShelfError::Invalid(
                        "a shelf on a host-held lock takes no lock path".into(),
                    ));
                }
                held.held().map_err(|e| {
                    ShelfError::Invalid(format!("the host's writer lock is not held: {e}"))
                })?;
                None
            }
            None => {
                let path = cfg.lock_path.clone().unwrap_or_else(|| {
                    let mut p = cfg.path.clone().into_os_string();
                    p.push(".lock");
                    PathBuf::from(p)
                });
                Some(FileLock::acquire(
                    &path,
                    cfg.lock_timeout.map(|t| Instant::now() + t),
                )?)
            }
        };
        let mut conn = Connection::open(&cfg.path)?;
        conn.busy_timeout(Duration::from_millis(5000))?;
        conn.pragma_update(None, "journal_mode", "WAL")?;
        conn.pragma_update(None, "synchronous", "FULL")?;
        store::init(&mut conn)?;
        let shared = Arc::new(ShelfShared {
            path: cfg.path,
            operator: cfg.operator,
            developer: cfg.developer,
            signer: cfg.signer,
            checkpoint_key: cfg.checkpoint_key,
            originals: cfg.originals,
            now: cfg.now,
            authenticated: cfg.authenticated_indexes,
            correlation_key: cfg.correlation_key,
            db: Mutex::new(conn),
        });
        Ok(Shelf(Arc::new(ShelfInner {
            shared,
            state: Mutex::new(ShelfState {
                books: HashMap::new(),
                hosts: HashMap::new(),
                cadence: None,
                random: Draws(cfg.random.unwrap_or_else(|| Box::new(SystemRandom))),
                released: false,
                lock,
            }),
        })))
    }

    pub(crate) fn lock(&self) -> MutexGuard<'_, ShelfState> {
        self.0.state.lock().unwrap_or_else(|p| p.into_inner())
    }

    pub(crate) fn shared(&self) -> &Arc<ShelfShared> {
        &self.0.shared
    }

    /// The shelf's file.
    pub fn path(&self) -> &Path {
        &self.0.shared.path
    }

    /// The book whose log id is `log_id`, created on first use: the same
    /// book for the same id for the shelf's lifetime. A host log's id is
    /// refused, in this process or as `shelf_logs` records it.
    pub fn open(&self, log_id: &str) -> Result<ShelfBook, ShelfError> {
        let mut state = self.lock();
        self.open_locked(&mut state, log_id)
    }

    pub(crate) fn open_locked(
        &self,
        state: &mut ShelfState,
        log_id: &str,
    ) -> Result<ShelfBook, ShelfError> {
        if state.released {
            return Err(ShelfError::Released);
        }
        if let Some(book) = state.books.get(log_id) {
            return Ok(book.clone());
        }
        let shared = self.shared();
        if state.hosts.contains_key(log_id)
            || store::log_kind(&shared.db(), log_id)?.as_deref() == Some(KIND_HOST)
        {
            return Err(ShelfError::Invalid(format!(
                "{log_id} is a host log, not a book"
            )));
        }
        let log = Log::open(&shared.path, log_id, &shared.checkpoint_key, &[], true)?;
        let indexes = shared
            .authenticated
            .as_ref()
            .map_or(Indexes::Defaults, |f| f(log_id));
        let book = ShelfBook::open(shared.clone(), log_id, log, indexes)?;
        state.books.insert(log_id.to_string(), book.clone());
        Ok(book)
    }

    /// The ids of the books that hold records, in id order.
    pub fn books(&self) -> Result<Vec<String>, ShelfError> {
        let state = self.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let db = self.shared().db();
        let mut stmt = db.prepare("SELECT DISTINCT book_id FROM book_records ORDER BY book_id")?;
        let ids = stmt.query_map([], |r| r.get(0))?;
        Ok(ids.collect::<Result<_, _>>()?)
    }

    /// Open an existing log of the shelf's file that is not a book and
    /// register it with the cadence (Go `Legacy`): it counts into the
    /// lifetime log count and is live until it has ended and a witnessed
    /// tick carries its final checkpoint. It never provisions a log. A
    /// book's id is refused.
    pub fn legacy(&self, log_id: &str) -> Result<HostLog, ShelfError> {
        let mut state = self.lock();
        self.host_locked(&mut state, log_id, true, false)
    }

    /// Open `log_id` as a host log, provisioning its CLL log when the file
    /// holds nothing of it (Go `HostLog`). With `in_cadence` the cadence
    /// checkpoints it like a book; otherwise the cadence never does, and the
    /// host checkpoints and witnesses it on its backend. It is the same host
    /// log for the same id for the shelf's lifetime. An id opened before in
    /// the other mode, in this process or as the file records it, or a
    /// book's id, is refused.
    pub fn host_log(&self, log_id: &str, in_cadence: bool) -> Result<HostLog, ShelfError> {
        let mut state = self.lock();
        self.host_locked(&mut state, log_id, in_cadence, true)
    }

    pub(crate) fn host_locked(
        &self,
        state: &mut ShelfState,
        log_id: &str,
        in_cadence: bool,
        provision: bool,
    ) -> Result<HostLog, ShelfError> {
        if state.released {
            return Err(ShelfError::Released);
        }
        if let Some(host) = state.hosts.get(log_id) {
            if host.in_cadence != in_cadence {
                return Err(ShelfError::Invalid(format!(
                    "host log {log_id} is already open {} the cadence",
                    if host.in_cadence { "in" } else { "outside" }
                )));
            }
            return Ok(HostLog {
                shelf: self.clone(),
                core: host.clone(),
            });
        }
        if state.books.contains_key(log_id) {
            return Err(ShelfError::Invalid(format!(
                "{log_id} is a book, not a host log"
            )));
        }
        if state.cadence.as_ref().is_some_and(|(id, _)| id == log_id) {
            return Err(ShelfError::Invalid(format!("{log_id} is the cadence log")));
        }
        let shared = self.shared();
        {
            let db = shared.db();
            if store::log_kind(&db, log_id)?.as_deref() == Some(KIND_BOOK)
                || store::record_count(&db, log_id)? > 0
            {
                return Err(ShelfError::Invalid(format!(
                    "{log_id} is a book, not a host log"
                )));
            }
        }
        let log = Log::open(&shared.path, log_id, &shared.checkpoint_key, &[], provision)?;
        let claimed = (|| -> Result<(), ShelfError> {
            let mut db = shared.db();
            let tx = db.transaction()?;
            let held = store::log_kind(&tx, log_id)?;
            let known: i64 = tx.query_row(
                "SELECT COUNT(*) FROM cadence_logs WHERE log_id = ?",
                [log_id],
                |r| r.get(0),
            )?;
            // The mode is durable without a column of its own: a host log in
            // the cadence is in cadence_logs from the transaction that first
            // opens it (rows are never deleted), and one outside it never is.
            if !in_cadence && known > 0 {
                return Err(ShelfError::Invalid(format!(
                    "host log {log_id} is in the cadence"
                )));
            }
            if in_cadence && held.as_deref() == Some(KIND_HOST) && known == 0 {
                return Err(ShelfError::Invalid(format!(
                    "host log {log_id} is outside the cadence"
                )));
            }
            store::claim_kind(&tx, log_id, KIND_HOST)?;
            if in_cadence && known == 0 {
                store::mark_live(&tx, log_id)?;
            }
            tx.commit()?;
            Ok(())
        })();
        if let Err(e) = claimed {
            let _ = log.release();
            return Err(e);
        }
        let core = Arc::new(HostCore {
            id: log_id.to_string(),
            log: Arc::new(log),
            in_cadence,
        });
        state.hosts.insert(log_id.to_string(), core.clone());
        Ok(HostLog {
            shelf: self.clone(),
            core,
        })
    }

    /// Release every book and log the shelf opened, close its file and give
    /// up the writer lock. A book or host log from this shelf refuses writes
    /// afterwards. Idempotent.
    pub fn release(&self) -> Result<(), ShelfError> {
        let mut state = self.lock();
        if state.released {
            return Ok(());
        }
        state.released = true;
        let mut first: Option<ShelfError> = None;
        let mut keep = |r: Result<(), ShelfError>| {
            if let Err(e) = r {
                first.get_or_insert(e);
            }
        };
        for book in state.books.values() {
            book.0.lock().released = true;
            keep(book.0.log.release());
        }
        for host in state.hosts.values() {
            keep(host.log.release());
        }
        if let Some((_, log)) = &state.cadence {
            keep(log.release());
        }
        // Close the shelf's own connection by replacing it with an
        // in-memory one: the file is closed before the lock is given up.
        {
            let mut db = self.shared().db();
            match Connection::open_in_memory() {
                Ok(empty) => {
                    let old = std::mem::replace(&mut *db, empty);
                    keep(old.close().map_err(|(_, e)| e.into()));
                }
                Err(e) => keep(Err(e.into())),
            }
        }
        if let Some(lock) = state.lock.take() {
            keep(lock.release());
        }
        match first {
            Some(e) => Err(e),
            None => Ok(()),
        }
    }
}

/// What a host log's writes run on (Go `HostLogStores`): the log's CLL
/// backend on the shelf's file and the shelf's artifact store, under the
/// shelf's one namespace. Both are the shelf's; they close with it.
pub struct HostLogStores {
    pub log: Arc<dyn cll::log::Backend + Send + Sync>,
    pub originals: Arc<dyn ArtifactStore + Send + Sync>,
}

/// A log on the shelf whose records the host seals itself, in its own
/// record format, never converted into book records (evidencebook#254
/// §2.7; Go `HostLog`). `shelf_logs` records it as a host log, so
/// [`Shelf::open`] refuses its id across reopens and languages.
#[derive(Clone)]
pub struct HostLog {
    shelf: Shelf,
    core: Arc<HostCore>,
}

impl HostLog {
    /// The log's id.
    pub fn log_id(&self) -> &str {
        &self.core.id
    }

    /// Whether the cadence checkpoints it.
    pub fn in_cadence(&self) -> bool {
        self.core.in_cadence
    }

    /// Run `write` on the log's stores under the shelf's lock (Go `Write`).
    /// For a log in the cadence the log is put back in the live set first,
    /// so no write, finished or not, is left outside the next tick. `write`
    /// must not call the shelf or its cadence: the lock is held.
    pub fn write<T, E>(&self, write: impl FnOnce(&HostLogStores) -> Result<T, E>) -> Result<T, E>
    where
        E: From<ShelfError>,
    {
        let state = self.shelf.lock();
        if state.released {
            return Err(ShelfError::Released.into());
        }
        if self.core.in_cadence {
            let mut db = self.shelf.shared().db();
            let tx = db.transaction().map_err(ShelfError::from)?;
            store::mark_live(&tx, &self.core.id)?;
            tx.commit().map_err(ShelfError::from)?;
        }
        let _log = self.core.log.mu.lock().unwrap_or_else(|p| p.into_inner());
        let result = write(&self.stores_unchecked());
        drop(state);
        result
    }

    /// The log's stores, for a host log outside the cadence whose host keeps
    /// them open for as long as it runs (its own log layer and checkpoint
    /// state on them, under the host's own held lock). The cadence
    /// never touches such a log, so the host's writes need no shelf lock. A
    /// log in the cadence is refused: every write to it goes through
    /// [`HostLog::write`].
    pub fn stores(&self) -> Result<HostLogStores, ShelfError> {
        if self.core.in_cadence {
            return Err(ShelfError::Invalid(format!(
                "host log {} is in the cadence; write to it through HostLog::write",
                self.core.id
            )));
        }
        if self.shelf.lock().released {
            return Err(ShelfError::Released);
        }
        Ok(self.stores_unchecked())
    }

    fn stores_unchecked(&self) -> HostLogStores {
        HostLogStores {
            log: self.core.log.store.clone(),
            originals: self.shelf.shared().originals.clone(),
        }
    }
}
