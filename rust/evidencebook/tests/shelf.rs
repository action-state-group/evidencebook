//! The shelf (feature `shelf`): books that write and reopen, the open-time
//! recovery of a crash between a record's row and its log entry, the log
//! kinds `shelf_logs` keeps apart across reopens, the file version, the
//! writer lock and a host-held lock, release, host logs, the cadence's
//! configuration, ticks and receipts, the legacy import, and the answer
//! path run on a shelf book unchanged. The byte vectors and the storage
//! interlock with Go are the interop crate's (`tests/shelf_vectors.rs`,
//! bin `shelf`) and `tree.rs`'s unit test.
#![cfg(feature = "shelf")]

use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ed25519_dalek::SigningKey;
use evidence_request::envelope::Signer as EnvelopeSigner;
use evidence_request::registry::SubjectForm;
use evidence_request::request::{parse_json, Derivation};
use evidence_request::resolve::{self, resolve, Resolution, Responder};
use evidencebook::answer::{anchors, answer, MAX_RECORDS};
use evidencebook::record::EpistemicType;
use evidencebook::shelf::{
    BookEntry, Cadence, CadenceConfig, HeldLock, Indexes, LegacyCadence, SeededStream, Shelf,
    ShelfBook, ShelfConfig, ShelfError, Time, WitnessReceipt, SHELF_VERSION,
};
use evidencebook::store::{CheckpointedLog, RecordSource};
use rusqlite::Connection;
use serde_json::json;
use sha2::{Digest, Sha256};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

fn start() -> Time {
    Time::parse("2026-10-31T12:00:00Z").unwrap()
}

/// A clock a test moves by hand.
#[derive(Clone)]
struct Clock(Arc<Mutex<Time>>);

impl Clock {
    fn new() -> Self {
        Clock(Arc::new(Mutex::new(start())))
    }
    fn set(&self, t: Time) {
        *self.0.lock().unwrap() = t;
    }
}

struct Opts {
    lock_path: Option<PathBuf>,
    held_lock: Option<Box<dyn HeldLock>>,
    lock_timeout: Option<Duration>,
    indexes: Option<Indexes>,
}

impl Default for Opts {
    fn default() -> Self {
        Opts {
            lock_path: None,
            held_lock: None,
            lock_timeout: Some(Duration::from_millis(200)),
            indexes: None,
        }
    }
}

fn open_with(path: &Path, clock: &Clock, opts: Opts) -> Result<Shelf, ShelfError> {
    let signer = key("signer");
    let conn = Connection::open(path).unwrap();
    conn.busy_timeout(Duration::from_secs(5)).unwrap();
    let originals = ArtifactSqlite::new(conn, "test", [signer.verifying_key().to_bytes()]).unwrap();
    originals.init().unwrap();
    let now = clock.clone();
    Shelf::open_sqlite(ShelfConfig {
        path: path.to_path_buf(),
        lock_path: opts.lock_path,
        held_lock: opts.held_lock,
        lock_timeout: opts.lock_timeout,
        random: Some(Box::new(SeededStream::new("shelf tests"))),
        operator: "test-operator".into(),
        developer: String::new(),
        signer: Arc::new(signer),
        checkpoint_key: key("checkpoint"),
        originals: Arc::new(originals),
        now: Some(Arc::new(move || *now.0.lock().unwrap())),
        authenticated_indexes: opts.indexes.map(|i| Arc::new(move |_: &str| i) as _),
        correlation_key: Some("pair".into()),
    })
}

fn open(path: &Path, clock: &Clock) -> Shelf {
    open_with(path, clock, Opts::default()).unwrap()
}

fn observation(subject: &str, payload: &str) -> BookEntry {
    let mut entry = BookEntry::new("observation", EpistemicType::ObservedEvent);
    entry.subject_ref = Some(subject.into());
    entry.payloads = vec![payload.as_bytes().to_vec()];
    entry
}

fn cadence_config(ended: &'static [&'static str]) -> CadenceConfig {
    CadenceConfig {
        log_id: "cadence/0123456789abcdef".into(),
        interval: Duration::from_secs(300),
        jitter: Duration::ZERO,
        no_jitter: true,
        witness_ids: Vec::new(),
        witnesses: None,
        pad_bucket: 4,
        ended: Arc::new(move |id| Ok(ended.contains(&id))),
    }
}

fn file(dir: &tempfile::TempDir) -> PathBuf {
    dir.path().join("shelf.db")
}

#[test]
fn a_book_writes_reopens_and_lists_its_records() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let book = shelf.open("deal/a").unwrap();
    let first = book.append(&observation("a", "one")).unwrap();
    clock.set(start().plus(Duration::from_secs(1)));
    book.append(&observation("a", "two")).unwrap();
    let cp = book.checkpoint().unwrap();
    // Two observations, then the record_id and subject_ref index roots.
    assert_eq!((book.size(), cp.entries), (4, 4));
    assert_eq!(first.header["committed_at"], "2026-10-31T12:00:00Z");
    assert_eq!(first.header["retention_state"], "AVAILABLE");
    assert_eq!(shelf.books().unwrap(), ["deal/a"]);
    let listing = shelf.records(&[]).unwrap();
    assert_eq!(listing.records.len(), 4);
    assert_eq!(listing.records[0].retention, "AVAILABLE");
    assert_eq!(
        listing.records[2].retention, "",
        "an index root commits no payload"
    );
    assert!(listing.pending.is_empty());
    // The same id is the same book.
    shelf
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "three"))
        .unwrap();
    assert_eq!(book.size(), 5);
    shelf.release().unwrap();

    let shelf = open(&file(&dir), &clock);
    let book = shelf.open("deal/a").unwrap();
    assert_eq!(book.size(), 5);
    assert_eq!(book.checkpoints().unwrap(), vec![cp]);
    assert_eq!(book.get(&first.record_id).unwrap(), first);
    // A second checkpoint commits only the roots that changed.
    let cp2 = book.checkpoint().unwrap();
    assert_eq!(cp2.entries, 7);
    assert_eq!(
        book.checkpoint().unwrap(),
        cp2,
        "an unchanged log cuts nothing new"
    );
    shelf.release().unwrap();
}

#[test]
fn a_book_with_no_indexes_commits_its_own_records_only() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open_with(
        &file(&dir),
        &clock,
        Opts {
            indexes: Some(Indexes::None),
            ..Default::default()
        },
    )
    .unwrap();
    let book = shelf.open("deal/a").unwrap();
    book.append(&observation("a", "one")).unwrap();
    assert_eq!(book.checkpoint().unwrap().entries, 1);
}

#[test]
fn a_record_stored_but_not_in_its_log_is_committed_by_the_next_open() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    shelf
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "one"))
        .unwrap();
    shelf.release().unwrap();

    // A copy appends the next record; only its row comes back, as a crash
    // after the row and before the log entry leaves it.
    let copy = dir.path().join("copy.db");
    Connection::open(file(&dir))
        .unwrap()
        .execute("VACUUM INTO ?", [copy.to_str().unwrap()])
        .unwrap();
    let other = open(&copy, &clock);
    let pending = other
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "two"))
        .unwrap();
    other.release().unwrap();
    let row: (i64, String, Vec<u8>, Vec<u8>, Vec<u8>) = Connection::open(&copy)
        .unwrap()
        .query_row(
            "SELECT seq, record_id, header, capsule, envelope FROM book_records WHERE seq = 2",
            [],
            |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
        )
        .unwrap();
    Connection::open(file(&dir))
        .unwrap()
        .execute(
            "INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES ('deal/a', ?, ?, ?, ?, ?)",
            rusqlite::params![row.0, row.1, row.2, row.3, row.4],
        )
        .unwrap();

    let shelf = open(&file(&dir), &clock);
    let listing = shelf.records(&[]).unwrap();
    assert_eq!(listing.records.len(), 1, "a pending record is not listed");
    assert_eq!(listing.pending.get("deal/a"), Some(&1));
    let book = shelf.open("deal/a").unwrap();
    assert_eq!(book.size(), 2);
    assert_eq!(book.record_at(1).unwrap().digest, pending.record_id);
    assert!(shelf.records(&["deal/a"]).unwrap().pending.is_empty());
}

#[test]
fn a_log_entry_with_no_stored_record_is_corrupt() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let book = shelf.open("deal/a").unwrap();
    book.append(&observation("a", "one")).unwrap();
    book.append(&observation("a", "two")).unwrap();
    shelf.release().unwrap();
    Connection::open(file(&dir))
        .unwrap()
        .execute("DELETE FROM book_records WHERE seq = 2", [])
        .unwrap();
    let shelf = open(&file(&dir), &clock);
    assert!(matches!(shelf.open("deal/a"), Err(ShelfError::Corrupt(_))));
}

#[test]
fn shelf_logs_keeps_books_and_host_logs_apart_across_reopens() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    shelf
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "one"))
        .unwrap();
    let host = shelf.host_log("node/chain", false).unwrap();
    assert!(!host.in_cadence());
    assert!(matches!(
        shelf.open("node/chain"),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        shelf.host_log("deal/a", false),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        shelf.host_log("node/chain", true),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        shelf.legacy("never/written"),
        Err(ShelfError::Log(_))
    ));
    shelf.release().unwrap();

    let shelf = open(&file(&dir), &clock);
    assert!(matches!(
        shelf.open("node/chain"),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        shelf.host_log("node/chain", true),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        shelf.host_log("deal/a", true),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        shelf.cadence(CadenceConfig {
            log_id: "deal/a".into(),
            ..cadence_config(&[])
        }),
        Err(ShelfError::Invalid(_))
    ));
    shelf.host_log("node/chain", false).unwrap();
    let kinds: Vec<(String, String)> = {
        let conn = Connection::open(file(&dir)).unwrap();
        let mut stmt = conn
            .prepare("SELECT log_id, kind FROM shelf_logs ORDER BY log_id")
            .unwrap();
        let rows = stmt
            .query_map([], |r| Ok((r.get(0)?, r.get(1)?)))
            .unwrap()
            .collect::<Result<_, _>>()
            .unwrap();
        rows
    };
    assert_eq!(
        kinds,
        [
            ("deal/a".to_string(), "book".to_string()),
            ("node/chain".to_string(), "host".to_string())
        ]
    );
}

#[test]
fn a_host_log_outside_the_cadence_hands_out_its_stores() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let host = shelf.host_log("node/chain", false).unwrap();
    let stores = host.stores().unwrap();
    let appended = stores
        .log
        .append(&cll::log::AppendInput {
            value: vec![7; 32],
            appended_at: cll::log::Timestamp::from_unix_millis(0),
        })
        .unwrap();
    assert_eq!(appended.entry.seq, 1);
    assert_eq!(stores.originals.namespace(), "test");
    let in_cadence = shelf.host_log("legacy/x", true).unwrap();
    assert!(matches!(in_cadence.stores(), Err(ShelfError::Invalid(_))));
    in_cadence
        .write(|s| -> Result<(), ShelfError> {
            s.log
                .append(&cll::log::AppendInput {
                    value: vec![8; 32],
                    appended_at: cll::log::Timestamp::from_unix_millis(0),
                })
                .map_err(|e| ShelfError::Log(e.to_string()))?;
            Ok(())
        })
        .unwrap();
    shelf.release().unwrap();
    assert!(matches!(host.stores(), Err(ShelfError::Released)));
    assert!(matches!(
        in_cadence.write(|_| Ok::<(), ShelfError>(())),
        Err(ShelfError::Released)
    ));
}

#[test]
fn a_file_version_this_build_does_not_know_is_refused() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    open(&file(&dir), &clock).release().unwrap();
    let conn = Connection::open(file(&dir)).unwrap();
    let version: i64 = conn
        .query_row("SELECT version FROM shelf_meta WHERE id = 1", [], |r| {
            r.get(0)
        })
        .unwrap();
    assert_eq!(version, SHELF_VERSION);
    conn.execute("UPDATE shelf_meta SET version = 2", [])
        .unwrap();
    drop(conn);
    assert!(matches!(
        open_with(&file(&dir), &clock, Opts::default()),
        Err(ShelfError::Unsupported(_))
    ));
}

#[test]
fn a_file_from_before_shelf_logs_gets_it_filled() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    shelf
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "one"))
        .unwrap();
    shelf.host_log("legacy/x", true).unwrap();
    shelf.release().unwrap();
    let conn = Connection::open(file(&dir)).unwrap();
    conn.execute_batch("DROP TABLE shelf_meta; DROP TABLE shelf_logs;")
        .unwrap();
    drop(conn);
    let shelf = open(&file(&dir), &clock);
    assert!(matches!(
        shelf.open("legacy/x"),
        Err(ShelfError::Invalid(_))
    ));
    assert_eq!(shelf.open("deal/a").unwrap().size(), 1);
}

#[test]
fn two_writers_of_one_file_exclude_each_other() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let first = open(&file(&dir), &clock);
    assert!(matches!(
        open_with(&file(&dir), &clock, Opts::default()),
        Err(ShelfError::LockHeld(_))
    ));
    // A host's own lock file is the shelf's when it names it.
    let host_lock = dir.path().join("host.lock");
    let other = open_with(
        &file(&dir),
        &clock,
        Opts {
            lock_path: Some(host_lock.clone()),
            ..Default::default()
        },
    )
    .unwrap();
    assert!(matches!(
        open_with(
            &file(&dir),
            &clock,
            Opts {
                lock_path: Some(host_lock),
                ..Default::default()
            }
        ),
        Err(ShelfError::LockHeld(_))
    ));
    other.release().unwrap();
    first.release().unwrap();
    open(&file(&dir), &clock).release().unwrap();
}

struct Held(bool);

impl HeldLock for Held {
    fn held(&self) -> Result<(), String> {
        if self.0 {
            Ok(())
        } else {
            Err("not held".into())
        }
    }
}

#[test]
fn a_shelf_on_a_host_held_lock_takes_none_of_its_own() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let held = |h| Opts {
        held_lock: Some(Box::new(Held(h))),
        ..Default::default()
    };
    let shelf = open_with(&file(&dir), &clock, held(true)).unwrap();
    // The shelf's own lock file is free.
    open(&file(&dir), &clock).release().unwrap();
    shelf.release().unwrap();
    assert!(matches!(
        open_with(&file(&dir), &clock, held(false)),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        open_with(
            &file(&dir),
            &clock,
            Opts {
                lock_path: Some(dir.path().join("x.lock")),
                ..held(true)
            }
        ),
        Err(ShelfError::Invalid(_))
    ));
}

#[test]
fn a_released_shelf_refuses_writes() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let book = shelf.open("deal/a").unwrap();
    let cadence = shelf.cadence(cadence_config(&[])).unwrap();
    shelf.release().unwrap();
    shelf.release().unwrap();
    assert!(matches!(
        book.append(&observation("a", "x")),
        Err(ShelfError::Released)
    ));
    assert!(matches!(book.checkpoint(), Err(ShelfError::Released)));
    assert!(matches!(shelf.open("deal/b"), Err(ShelfError::Released)));
    assert!(matches!(shelf.books(), Err(ShelfError::Released)));
    assert!(matches!(shelf.records(&[]), Err(ShelfError::Released)));
    assert!(matches!(cadence.tick(start()), Err(ShelfError::Released)));
}

#[test]
fn a_cadence_configuration_is_checked() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let bad = [
        CadenceConfig {
            log_id: String::new(),
            ..cadence_config(&[])
        },
        CadenceConfig {
            interval: Duration::from_secs(59),
            ..cadence_config(&[])
        },
        CadenceConfig {
            jitter: Duration::from_secs(1),
            ..cadence_config(&[])
        },
        CadenceConfig {
            no_jitter: false,
            jitter: Duration::from_secs(150),
            ..cadence_config(&[])
        },
        CadenceConfig {
            pad_bucket: 1025,
            ..cadence_config(&[])
        },
    ];
    for cfg in bad {
        assert!(matches!(shelf.cadence(cfg), Err(ShelfError::Invalid(_))));
    }
    shelf.cadence(cadence_config(&[])).unwrap();
    assert!(matches!(
        shelf.cadence(cadence_config(&[])),
        Err(ShelfError::Invalid(_))
    ));
}

fn tick(cadence: &Cadence, clock: &Clock) -> evidencebook::shelf::CadenceTick {
    let now = cadence
        .last()
        .unwrap()
        .map_or(start().plus(Duration::from_secs(30)), |t| t.due_next);
    clock.set(now);
    let result = cadence.tick(now).unwrap();
    assert!(result.ticked);
    result.tick.unwrap()
}

#[test]
fn ticks_carry_live_logs_pad_the_cadence_log_and_anchor_a_card() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let cadence = shelf.cadence(cadence_config(&["deal/b"])).unwrap();
    shelf
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "1"))
        .unwrap();
    shelf
        .open("deal/b")
        .unwrap()
        .append(&observation("b", "1"))
        .unwrap();
    // A host log opened in the cadence and not yet written is skipped.
    shelf.host_log("legacy/x", true).unwrap();
    let first = tick(&cadence, &clock);
    assert_eq!(first.at, start(), "a tick's time is floored to the minute");
    assert_eq!(first.due_next, start().plus(Duration::from_secs(330)));
    assert_eq!(
        first.leaves.deals.keys().collect::<Vec<_>>(),
        ["deal/a", "deal/b"]
    );
    assert_eq!(
        first.leaves.filler_count, 1,
        "the skipped host log is a derived filler"
    );
    assert_eq!(first.entry_seq, 1);
    assert_eq!(first.checkpoint_size, 7, "padded to 4 entries");
    let (root, _) = first.leaves.tree(first.leaves.filler_index).unwrap();
    assert_eq!(root.len(), 32);
    // Not due before due_next.
    let early = cadence.tick(start().plus(Duration::from_secs(60))).unwrap();
    assert!(!early.ticked);
    assert_eq!(early.due_next, first.due_next);
    let anchor = cadence.standard_history_anchor(0).unwrap();
    assert_eq!(anchor.tick.n, 1);
    assert_eq!(anchor.anchor_time, "2026-10-31T12:00:00Z");
    assert_eq!(
        anchor.extents.into_iter().collect::<Vec<_>>(),
        [("deal/a".to_string(), 3), ("deal/b".to_string(), 3)]
    );
    // No witness has seen a tick yet.
    let a = shelf.open("deal/a").unwrap().checkpoints().unwrap();
    assert!(matches!(
        cadence.chain(&a[0].statement),
        Err(ShelfError::NotFound(_))
    ));
    assert_eq!(cadence.live().unwrap(), ["deal/a", "deal/b", "legacy/x"]);
    let second = tick(&cadence, &clock);
    assert_eq!(second.n, 2);
    assert_eq!(second.entry_seq, 5);
    assert!(matches!(
        cadence.standard_history_anchor(9),
        Err(ShelfError::NotFound(_))
    ));
}

#[test]
fn a_receipt_that_does_not_verify_witnesses_nothing() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let cadence = shelf.cadence(cadence_config(&[])).unwrap();
    shelf
        .open("deal/a")
        .unwrap()
        .append(&observation("a", "1"))
        .unwrap();
    let t = tick(&cadence, &clock);
    let receipt = WitnessReceipt {
        ts_url: "https://witness.test/".into(),
        entry_hash: "00".repeat(32),
        receipt: vec![1, 2, 3],
        leaf_index: 0,
        tree_size: 1,
    };
    // No directory trusts the witness: not checked, nothing changes.
    let check = cadence.witnessed(t.n, receipt.clone()).unwrap();
    assert_eq!(check.state, evidencebook::shelf::WITNESS_NOT_CHECKED);
    assert!(!cadence.get(1).unwrap().witnessed);
    let empty = WitnessReceipt {
        receipt: Vec::new(),
        ..receipt.clone()
    };
    assert!(matches!(
        cadence.witnessed(t.n, empty),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        cadence.witnessed(9, receipt),
        Err(ShelfError::NotFound(_))
    ));
}

#[test]
fn a_legacy_cadence_imports_once_and_refuses_a_rewrite() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    // A pre-shelf writer's ticks: those of another shelf over a legacy log.
    let source = open(&dir.path().join("source.db"), &clock);
    let source_cadence = source.cadence(cadence_config(&[])).unwrap();
    source
        .host_log("legacy/x", true)
        .unwrap()
        .write(|s| -> Result<(), ShelfError> {
            s.log
                .append(&cll::log::AppendInput {
                    value: vec![9; 32],
                    appended_at: cll::log::Timestamp::from_unix_millis(0),
                })
                .map_err(|e| ShelfError::Log(e.to_string()))?;
            Ok(())
        })
        .unwrap();
    let t1 = tick(&source_cadence, &clock);
    let t2 = tick(&source_cadence, &clock);
    source.release().unwrap();

    let shelf = open(&file(&dir), &clock);
    let cadence = shelf.cadence(cadence_config(&[])).unwrap();
    let legacy = LegacyCadence {
        logs: vec!["legacy/x".into()],
        ticks: vec![t1.clone(), t2.clone()],
    };
    cadence.import_legacy(&legacy).unwrap();
    cadence.import_legacy(&legacy).unwrap();
    assert_eq!(cadence.last().unwrap().unwrap().n, 2);
    assert_eq!(cadence.get(2).unwrap().checkpoint, t2.checkpoint);
    assert_eq!(cadence.live().unwrap(), ["legacy/x"]);
    assert!(matches!(
        shelf.open("legacy/x"),
        Err(ShelfError::Invalid(_))
    ));

    let mut rewritten = t2.clone();
    rewritten.checkpoint = t1.checkpoint.clone();
    let refuse = |ticks: Vec<_>| {
        cadence.import_legacy(&LegacyCadence {
            logs: vec!["legacy/x".into()],
            ticks,
        })
    };
    assert!(matches!(
        refuse(vec![rewritten]),
        Err(ShelfError::Invalid(_))
    ));
    let mut witnessed = t2.clone();
    witnessed.witnessed = true;
    assert!(matches!(
        refuse(vec![witnessed]),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        refuse(vec![t2.clone(), t1.clone()]),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        cadence.import_legacy(&LegacyCadence {
            logs: vec![],
            ticks: vec![t1],
        }),
        Err(ShelfError::Invalid(_))
    ));
}

/// A shelf book as a responder resolves requests against it.
struct Responding<'a>(&'a ShelfBook);

impl Responder for Responding<'_> {
    fn holds_record(&self, digest: &str) -> bool {
        self.0.record_by_digest(digest).is_some()
    }
    fn holds_correlation(&self, id: &str) -> bool {
        !self.0.correlation(id).is_empty()
    }
    #[rustfmt::skip]
    fn cites_exchange(&self, half: &str) -> bool { !self.0.citing(half).is_empty() } // the "exchange" subject form; vocabulary-gate: request-draft wire token
    fn positional_ordering(&self) -> bool {
        true
    }
    fn anchors(&self) -> Vec<resolve::Anchor> {
        anchors(self.0).unwrap()
    }
    fn derivation_forms(&self, _: &Derivation) -> Option<Vec<SubjectForm>> {
        None
    }
}

#[test]
fn the_answer_path_runs_on_a_shelf_book_unchanged() {
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock::new();
    let shelf = open(&file(&dir), &clock);
    let book = shelf.open("deal/a").unwrap();
    let mut paired = observation("a", "one");
    paired.correlation.insert("pair".into(), "p-1".into());
    let record = book.append(&paired).unwrap();
    book.append(&observation("b", "two")).unwrap();
    book.checkpoint().unwrap();
    assert_eq!(book.leaf_count().unwrap(), 4);
    assert_eq!(book.stated_checkpoints().unwrap().len(), 1);
    assert_eq!(book.correlation("p-1"), [0]);
    assert_eq!(
        book.correlation("b"),
        [1],
        "a record without the key is held under its subject"
    );
    assert!(
        book.correlation("a").is_empty(),
        "a keyed record is held under its key alone"
    );
    let stored = book.record_by_digest(&record.record_id).unwrap();
    assert_eq!(book.entry_of(&stored.body).unwrap(), record.record_id);
    assert_eq!(book.header_of(&stored).unwrap(), record.header);

    let digest = anchors(&book).unwrap()[0].digest.clone();
    let bytes = serde_json::to_vec(&json!({
        "subject": {"record": record.record_id},
        "coverage": {"expected_pin": digest},
    }))
    .unwrap();
    let request = parse_json(&bytes).unwrap();
    let Resolution::Artifact(anchor) = resolve(&request, &Responding(&book)) else {
        panic!("expected an artifact");
    };
    let responder = key("responder");
    let envelope = answer(
        &book,
        &book,
        &request,
        &bytes,
        &anchor,
        MAX_RECORDS,
        &responder as &dyn EnvelopeSigner,
        "2026-10-31T13:00:00Z",
    )
    .unwrap();
    assert_eq!(envelope.artifact, stored.body);
    envelope
        .verify_for(&responder.verifying_key(), &bytes)
        .unwrap();
}
