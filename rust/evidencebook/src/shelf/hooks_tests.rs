//! The shelf hooks a deal profile reads and writes through (evidencebook#461
//! PR 6b-1), case for case with Go's `shelf_record_stamp_test.go`,
//! `shelf_sent_test.go`, the received and by-key cases of
//! `shelf_record_time_test.go`, `TestBeforeStampRunsBeforeEachStamp` and
//! `TestExistingJournalNeverCreatesIt`.

use super::record_time_tests::{
    append, book_id_of, config, count, directory, leaves_of, minutes, open, record_time,
    record_time_shelf, tick_time, witness_at, witness_key, OTHER_WITNESS_URL, TEST_WITNESS_URL,
};
use super::{
    BookEntry, BookStatus, Cadence, Shelf, ShelfError, StatusInput, StatusValues, Time,
    JOURNAL_LOG_ID,
};
use crate::canonical;
use crate::ed25519::Sign1;
use crate::record::EpistemicType;
use rusqlite::Connection;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

/// The Capsule member that holds the compute attestation.
const ATTESTATION: &str = "model_attestation"; // vocabulary-gate: AAC capsule member

fn dg(s: &str) -> String {
    hex::encode(Sha256::digest(s.as_bytes()))
}

fn key_id(url: &str) -> String {
    hex::encode(witness_key(url).verifying_key().to_bytes())
}

/// What the test book status holds and what it saw.
#[derive(Default)]
struct Held {
    status: HashMap<String, Value>,
    last_received: HashMap<String, Value>,
    fail: HashSet<String>,
    /// `<n> <log id> <records> <journal records or -1>` per call.
    calls: Vec<String>,
    before: usize,
    fail_before: bool,
    /// Whether a before_stamp ran while the receive lock was free.
    unlocked: bool,
}

/// A book status of fixed values per log, reading the handles it is
/// handed (Go's `statusValues`, `stampedValues`, `bookValues`,
/// `failingValues` and `beforeStampValues` in one).
#[derive(Clone)]
struct Values {
    held: Arc<Mutex<Held>>,
    shelf: Shelf,
}

impl Values {
    fn set(&self, log_id: &str, status: Value) {
        self.held
            .lock()
            .unwrap()
            .status
            .insert(log_id.into(), status);
    }

    fn link(&self, log_id: &str, theirs: &str, size: u64) {
        self.held.lock().unwrap().last_received.insert(
            log_id.into(),
            json!({"log_id": theirs, "size": size, "record_digest": dg(&format!("{theirs}{size}"))}),
        );
    }

    fn last_call(&self) -> String {
        self.held.lock().unwrap().calls.last().cloned().unwrap()
    }
}

impl BookStatus for Values {
    fn values(&self, input: &StatusInput<'_>) -> Result<Option<StatusValues>, String> {
        let records = input.book.records().len();
        let journal = input.journal.map_or(-1, |j| j.records().len() as i64);
        let mut held = self.held.lock().unwrap();
        held.calls
            .push(format!("{} {} {records} {journal}", input.n, input.log_id));
        if held.fail.contains(input.log_id) {
            return Err("the deal's offer is not held".into());
        }
        let Some(status) = held.status.get(input.log_id).cloned() else {
            return Ok(None);
        };
        Ok(Some(StatusValues {
            profile: "deal".into(),
            last_received: held.last_received.get(input.log_id).cloned(),
            checked_over: Vec::new(),
            status: Some(
                json!({"stamp": input.n, "records": records, "journal": journal, "was": status}),
            ),
        }))
    }

    fn before_stamp(&self, _at: Time) -> Result<(), String> {
        let mut held = self.held.lock().unwrap();
        held.before += 1;
        // It runs holding the receive lock, so no receive moves what it read.
        if self.shelf.shared().receive_mu.try_lock().is_ok() {
            held.unlocked = true;
        }
        if held.fail_before {
            held.fail_before = false;
            return Err("naming failed".into());
        }
        Ok(())
    }
}

/// A cadence on `shelf` witnessed by the directory's witnesses, with the
/// test book status.
fn stamp_cadence(shelf: &Shelf, held: Arc<Mutex<Held>>) -> (Cadence, Values) {
    let values = Values {
        held,
        shelf: shelf.clone(),
    };
    let mut cfg = config(directory());
    cfg.status = Some(Arc::new(values.clone()));
    (shelf.cadence(cfg).unwrap(), values)
}

/// A migrated shelf at `path` with the test book status, deal/a's first record
/// in its migration's stamp, and deal/a's status set.
fn migrated(path: &Path) -> (Shelf, Cadence, Values) {
    let shelf = open(path);
    let (c, values) = stamp_cadence(&shelf, Arc::default());
    values.set("deal/a", json!({"row": "open"}));
    append(&shelf, "deal/a", "1");
    c.tick(tick_time()).unwrap();
    c.migrate(minutes(1)).unwrap();
    (shelf, c, values)
}

/// deal/a linking deal/theirs at size 2 in its first stamp after the
/// migration and at size 5 in its second (Go's received record cases).
struct Linked {
    _dir: tempfile::TempDir,
    shelf: Shelf,
    cadence: Cadence,
    book_id: String,
    s1: super::CadenceTick,
    s2: super::CadenceTick,
}

fn linked() -> Linked {
    let dir = tempfile::tempdir().unwrap();
    let (shelf, c, values) = migrated(&dir.path().join("shelf.db"));
    values.link("deal/a", "deal/theirs", 2);
    append(&shelf, "deal/a", "2");
    let s1 = c.stamp(minutes(2)).unwrap().tick.unwrap();
    values.link("deal/a", "deal/theirs", 5);
    append(&shelf, "deal/a", "3");
    let s2 = c.stamp(minutes(3)).unwrap().tick.unwrap();
    let book_id = book_id_of(&shelf, "deal/a");
    Linked {
        _dir: dir,
        shelf,
        cadence: c,
        book_id,
        s1,
        s2,
    }
}

fn names(witnesses: &[&str]) -> Vec<String> {
    witnesses.iter().map(|w| w.to_string()).collect()
}

// A received record's time is the earliest named registration of a shelf
// checkpoint covering the first fact whose link value reaches it.
#[test]
fn a_received_record_time_is_the_first_fact_that_links_it() {
    let f = linked();
    let (shelf, c, book_id) = (&f.shelf, &f.cadence, f.book_id.as_str());
    let received = |log: &str, position: u64, witnesses: &[&str]| {
        shelf
            .received_record_time(book_id, log, position, &names(witnesses))
            .unwrap()
    };
    assert_eq!(
        received("deal/theirs", 1, &[TEST_WITNESS_URL]),
        None,
        "no receipt yet"
    );
    let (t1, t2) = (minutes(30), minutes(20));
    witness_at(c, &f.s1, TEST_WITNESS_URL, Some(t1));
    assert_eq!(
        received("deal/theirs", 2, &[TEST_WITNESS_URL]),
        Some(t1),
        "the first stamp's fact reaches position 2"
    );
    assert_eq!(
        received("deal/theirs", 3, &[TEST_WITNESS_URL]),
        None,
        "position 3 is first reached at the second stamp, not yet witnessed"
    );
    // The second stamp's checkpoint covers both facts and registered
    // earlier, so it gives both positions their time.
    witness_at(c, &f.s2, TEST_WITNESS_URL, Some(t2));
    for p in [1, 2, 5] {
        assert_eq!(
            received("deal/theirs", p, &[TEST_WITNESS_URL]),
            Some(t2),
            "{p}"
        );
    }
    assert_eq!(
        received("deal/theirs", 6, &[TEST_WITNESS_URL]),
        None,
        "a position no fact reaches"
    );
    assert_eq!(
        received("deal/other", 1, &[TEST_WITNESS_URL]),
        None,
        "another log"
    );
    assert_eq!(
        received("deal/theirs", 1, &["https://other.test"]),
        None,
        "a witness not named"
    );
    assert_eq!(received("deal/theirs", 1, &[]), None, "no witness named");
    assert_eq!(
        shelf
            .received_record_time(
                "no-such-book",
                "deal/theirs",
                1,
                &names(&[TEST_WITNESS_URL])
            )
            .unwrap(),
        None,
        "another book"
    );
    for (log, position) in [("deal/theirs", 0), ("", 1)] {
        assert!(matches!(
            shelf.received_record_time(book_id, log, position, &names(&[TEST_WITNESS_URL])),
            Err(ShelfError::Invalid(_))
        ));
    }
    shelf.release().unwrap();
    assert!(matches!(
        shelf.received_record_time(book_id, "deal/theirs", 1, &names(&[TEST_WITNESS_URL])),
        Err(ShelfError::Released)
    ));
}

// A record time by key id reads the key id each receipt verified under: the
// same receipts give the same time as by URL, and a key no receipt verified
// under, or a URL, gives none.
#[test]
fn a_record_time_by_key() {
    let f = record_time_shelf();
    let (shelf, c, book_id) = (&f.shelf, &f.cadence, f.book_id.as_str());
    witness_at(c, &f.first, TEST_WITNESS_URL, Some(minutes(10)));
    witness_at(c, &f.second, TEST_WITNESS_URL, Some(minutes(20)));
    let key = key_id(TEST_WITNESS_URL);
    let last = leaves_of(shelf, "deal/a");
    for p in 1..=last + 1 {
        let by_url = record_time(shelf, book_id, p, &[TEST_WITNESS_URL]);
        let by_key = shelf
            .record_time_by_key(book_id, p, &names(&[key.as_str()]))
            .unwrap();
        assert_eq!(by_url, by_key, "{p}");
        assert_eq!(
            shelf
                .record_time_by_key(
                    book_id,
                    p,
                    &names(&[key_id(OTHER_WITNESS_URL).as_str(), TEST_WITNESS_URL])
                )
                .unwrap(),
            None,
            "a URL is not a key id, and the other key verified no receipt"
        );
    }
    assert!(by_key_some(shelf, book_id, last, &key));
    assert!(matches!(
        shelf.record_time_by_key(book_id, 0, &names(&[key.as_str()])),
        Err(ShelfError::Invalid(_))
    ));
}

fn by_key_some(shelf: &Shelf, book_id: &str, position: u64, key: &str) -> bool {
    shelf
        .record_time_by_key(book_id, position, &names(&[key]))
        .unwrap()
        .is_some()
}

// received_record_time_by_key names witnesses by key id as
// record_time_by_key does.
#[test]
fn a_received_record_time_by_key() {
    let f = linked();
    let t = minutes(30);
    witness_at(&f.cadence, &f.s1, TEST_WITNESS_URL, Some(t));
    let key = key_id(TEST_WITNESS_URL);
    assert_eq!(
        f.shelf
            .received_record_time_by_key(&f.book_id, "deal/theirs", 2, &names(&[key.as_str()]))
            .unwrap(),
        Some(t)
    );
    assert_eq!(
        f.shelf
            .received_record_time_by_key(&f.book_id, "deal/theirs", 2, &names(&[TEST_WITNESS_URL]))
            .unwrap(),
        None,
        "a URL is not a key id"
    );
}

// A record's stamp is the tick of the first fact that covers it: the
// migration's genesis fact for the first record, the second stamp's for the
// record appended after it, none for a record no fact covers yet.
#[test]
fn a_record_stamp_is_the_first_covering_fact() {
    let f = record_time_shelf();
    let (shelf, book_id) = (&f.shelf, f.book_id.as_str());
    let (size, genesis): (i64, i64) = shelf
        .shared()
        .db()
        .query_row(
            "SELECT json_extract(doc, '$.size'), tick FROM shelf_entries WHERE log_id = 'deal/a' ORDER BY entry_seq LIMIT 1",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .unwrap();
    let in_first = cll::mmr::leaf_count(size as u64).unwrap();
    let last = leaves_of(shelf, "deal/a");
    assert!((genesis as u64) < f.first.n);
    for (position, want) in [
        (in_first, Some(genesis as u64)),
        (last, Some(f.second.n)),
        (last + 1, None),
    ] {
        assert_eq!(
            shelf.record_stamp(book_id, position).unwrap(),
            want,
            "{position}"
        );
    }
    assert_eq!(shelf.record_stamp("no-such-book", 1).unwrap(), None);
    assert!(matches!(
        shelf.record_stamp(book_id, 0),
        Err(ShelfError::Invalid(_))
    ));
    shelf.release().unwrap();
    assert!(matches!(
        shelf.record_stamp(book_id, 1),
        Err(ShelfError::Released)
    ));
}

// A received record's stamp is the tick of the first fact whose link value
// reaches it, as received_record_time finds that fact.
#[test]
fn a_received_record_stamp_is_the_first_fact_that_links_it() {
    let f = linked();
    for (log, position, want) in [
        ("deal/theirs", 1, Some(f.s1.n)),
        ("deal/theirs", 2, Some(f.s1.n)),
        ("deal/theirs", 3, Some(f.s2.n)),
        ("deal/theirs", 5, Some(f.s2.n)),
        ("deal/theirs", 6, None),
        ("deal/other", 1, None),
    ] {
        assert_eq!(
            f.shelf
                .received_record_stamp(&f.book_id, log, position)
                .unwrap(),
            want,
            "{log} {position}"
        );
    }
    for (log, position) in [("", 1), ("deal/theirs", 0)] {
        assert!(matches!(
            f.shelf.received_record_stamp(&f.book_id, log, position),
            Err(ShelfError::Invalid(_))
        ));
    }
}

/// The status opening of deal/a's fact at tick `n`.
fn status_at(shelf: &Shelf, n: u64) -> String {
    let opening: Vec<u8> = shelf
        .shared()
        .db()
        .query_row(
            "SELECT status_opening FROM shelf_entries WHERE log_id = 'deal/a' AND tick = ?",
            [n as i64],
            |r| r.get(0),
        )
        .unwrap();
    String::from_utf8(opening).unwrap()
}

// A book status is handed the stamp's tick (the migration's included),
// the book and the journal, and reads them under the stamp: the migration's
// stamp has no journal; a stamp before the host opens the journal has none
// either (a stamp never creates it); once it exists, and after a reopen, the
// book status reads its records, and the fact commits what it returned.
#[test]
fn a_book_status_reads_the_stamp_the_book_and_the_journal() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shelf.db");
    let (shelf, c, values) = migrated(&path);
    let migration = values.last_call();
    assert!(
        migration.ends_with(" -1"),
        "the migration's stamp: no journal ({migration})"
    );

    append(&shelf, "deal/a", "2");
    let size = shelf.open("deal/a").unwrap().size();
    let s = c.stamp(minutes(2)).unwrap().tick.unwrap();
    assert_eq!(
        values.last_call(),
        format!("{} deal/a {size} -1", s.n),
        "no journal opened yet, and the stamp does not create one"
    );
    let first: u64 = migration.split(' ').next().unwrap().parse().unwrap();
    assert_eq!(first + 1, s.n, "the migration's stamp is the one before");
    assert!(status_at(&shelf, s.n).contains(&format!("\"stamp\":{}", s.n)));
    assert_eq!(
        super::store::log_kind(&shelf.shared().db(), JOURNAL_LOG_ID).unwrap(),
        None,
        "the stamp claimed no journal log"
    );

    append(&shelf, JOURNAL_LOG_ID, "j1");
    shelf.release().unwrap();
    drop(c);

    // Reopened: the journal is not open yet, so the stamp opens it.
    let shelf = open(&path);
    let (c, values) = stamp_cadence(&shelf, values.held.clone());
    append(&shelf, "deal/a", "3");
    let size = shelf.open("deal/a").unwrap().size();
    let s = c.stamp(minutes(3)).unwrap().tick.unwrap();
    let call = values.last_call();
    let journal: i64 = call.rsplit(' ').next().unwrap().parse().unwrap();
    assert!(
        call.starts_with(&format!("{} deal/a {size} ", s.n)),
        "{call}"
    );
    assert!(
        journal >= 1,
        "the book status reads the journal's records ({call})"
    );
    assert!(
        status_at(&shelf, s.n).contains(&format!("\"records\":{size}")),
        "the fact commits what the book status read"
    );
}

fn facts_of(shelf: &Shelf, log_id: &str) -> i64 {
    count(
        shelf,
        "SELECT COUNT(*) FROM shelf_entries WHERE log_id = ? AND kind = ?",
        [log_id, crate::shelf_facts::FACT_KIND_BOOK],
    )
}

// One book's status error never stops the shelf's stamp: that book keeps
// its last fact and is reported unstated, the others get theirs, and the
// next stamp asks again.
#[test]
fn a_status_error_leaves_the_books_last_fact() {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let (c, values) = stamp_cadence(&shelf, Arc::default());
    values.set("deal/a", json!({"row": "a"}));
    values.set("deal/b", json!({"row": "b"}));
    append(&shelf, "deal/a", "1");
    append(&shelf, "deal/b", "1");
    c.tick(tick_time()).unwrap();
    c.migrate(minutes(1)).unwrap();
    let (before_a, before_b) = (facts_of(&shelf, "deal/a"), facts_of(&shelf, "deal/b"));

    values.held.lock().unwrap().fail.insert("deal/a".into());
    append(&shelf, "deal/a", "2");
    append(&shelf, "deal/b", "2");
    let s = c.stamp(minutes(2)).expect("the stamp goes on");
    assert_eq!(
        s.unstated,
        BTreeMap::from([(
            "deal/a".to_string(),
            "the deal's offer is not held".to_string()
        )])
    );
    assert_eq!(
        facts_of(&shelf, "deal/a"),
        before_a,
        "deal/a keeps its last fact"
    );
    assert_eq!(
        facts_of(&shelf, "deal/b"),
        before_b + 1,
        "deal/b gets its new fact"
    );

    values.held.lock().unwrap().fail.clear();
    let s = c.stamp(minutes(3)).unwrap();
    assert!(s.unstated.is_empty());
    assert_eq!(
        facts_of(&shelf, "deal/a"),
        before_a + 1,
        "the next stamp asks again"
    );
}

// before_stamp runs before each stamp `stamp` takes and before a due tick's
// stamp, never before a tick that cuts nothing or before the migration; its
// error is reported and the stamp is still taken (evidencebook#527).
#[test]
fn before_stamp_runs_before_each_stamp() {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let (c, values) = stamp_cadence(&shelf, Arc::default());
    append(&shelf, "deal/a", "1");
    c.tick(tick_time()).unwrap();
    let calls = || values.held.lock().unwrap().before;
    assert_eq!(calls(), 0, "no before_stamp before the shelf is migrated");
    c.migrate(minutes(1)).unwrap();

    values.held.lock().unwrap().fail_before = true;
    let res = c.stamp(minutes(2)).unwrap();
    assert_eq!(calls(), 1);
    assert_eq!(
        res.before_stamp.as_deref(),
        Some("naming failed"),
        "reported, and the stamp taken"
    );
    assert!(res.ticked);

    let early = c.tick(minutes(3)).unwrap();
    assert!(!early.ticked);
    assert_eq!(calls(), 1, "a tick that cuts nothing runs no before_stamp");

    let due = c.tick(res.tick.unwrap().due_next).unwrap();
    assert!(due.ticked);
    assert_eq!(calls(), 2, "a due tick's stamp runs it");
    assert_eq!(due.before_stamp, None);
    assert!(
        !values.held.lock().unwrap().unlocked,
        "always under the receive lock"
    );
}

// existing_journal never creates the journal: none on a shelf without one,
// which a later journal() still creates; the journal once it exists.
#[test]
fn existing_journal_never_creates_it() {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let (c, _) = stamp_cadence(&shelf, Arc::default());
    append(&shelf, "deal/a", "1");
    c.tick(tick_time()).unwrap();
    assert!(
        shelf.existing_journal().unwrap().is_none(),
        "before migrate"
    );
    c.migrate(minutes(1)).unwrap();
    assert!(
        shelf.existing_journal().unwrap().is_none(),
        "migrated, no journal yet"
    );
    assert_eq!(
        super::store::log_kind(&shelf.shared().db(), JOURNAL_LOG_ID).unwrap(),
        None,
        "nothing registered"
    );
    let journal = shelf.journal().unwrap();
    let got = shelf.existing_journal().unwrap().unwrap();
    assert!(Arc::ptr_eq(&journal.0, &got.0), "the same handle");
    shelf.release().unwrap();
    assert!(matches!(
        shelf.existing_journal(),
        Err(ShelfError::Released)
    ));
}

const SENT_RECORD: &str = r#"{"canonicalization":"jcs","deal_id":"deal-0123456789abcdef","profile":"x-deal-v1","record_type":"propose"}"#;

fn sent_entry(payload: &str) -> BookEntry {
    let mut entry = BookEntry::new("deal_step", EpistemicType::ProducerClaim);
    entry.payloads = vec![payload.as_bytes().to_vec()];
    entry
}

// seal_sent seals the journal record and its standalone Capsule: the record
// commits the bytes, and the Capsule carries the same bytes under the
// caller's action_id, signed by the journal's key, showing no place in the
// journal.
#[test]
fn seal_sent_seals_the_journal_record_and_its_standalone_capsule() {
    let dir = tempfile::tempdir().unwrap();
    let (shelf, _c, _) = migrated(&dir.path().join("shelf.db"));
    let journal = shelf.journal().unwrap();
    let before = journal.size();
    let members = BTreeMap::from([("deal_carried".to_string(), json!({"n": 1}))]);
    let sent = shelf
        .seal_sent(
            &sent_entry(SENT_RECORD),
            &members,
            "deal-0123456789abcdef/sent",
        )
        .unwrap();
    assert!(sent.error.is_none());
    let digest = dg(SENT_RECORD);
    assert_eq!(journal.size(), before + 1);
    assert_eq!(sent.record.header["book_id"], json!(JOURNAL_LOG_ID));
    assert_eq!(sent.record.header["payload_commitments"], json!([digest]));

    let capsule = &sent.capsule;
    assert_eq!(capsule.payload, SENT_RECORD.as_bytes());
    let doc: Value = serde_json::from_slice(&capsule.capsule).unwrap();
    assert_eq!(canonical::jcs(&doc).unwrap(), capsule.capsule, "its JCS");
    let mut body = doc.clone();
    body.as_object_mut().unwrap().remove("capsule_id");
    assert_eq!(
        dg_bytes(&canonical::jcs(&body).unwrap()),
        capsule.capsule_id
    );
    assert_eq!(doc["capsule_id"], json!(capsule.capsule_id));
    assert_ne!(capsule.capsule_id, sent.record.record_id);
    assert_eq!(doc["action_id"], json!("deal-0123456789abcdef/sent"));
    assert_eq!(doc["action_type"], json!("fyi"));
    assert_eq!(
        doc[ATTESTATION]["compute_attestation"]["agent_input_digest"],
        json!(digest),
        "the Capsule commits the record digest"
    );
    assert_eq!(doc["deal_carried"], json!({"n": 1}), "the members travel");
    assert!(doc.get("chain").is_none() && doc.get("references").is_none());
    let text = String::from_utf8(capsule.capsule.clone()).unwrap();
    assert!(!text.contains(&format!("{JOURNAL_LOG_ID}#")));
    // Whole seconds, as Go truncates the journal's clock.
    assert_eq!(doc["timestamp"], json!("2026-10-31T12:00:30Z"));
    let envelope = Sign1::decode(&capsule.envelope).unwrap();
    assert_eq!(hex::encode(&envelope.payload), capsule.capsule_id);
    let signer = super::record_time_tests::key("signer")
        .verifying_key()
        .to_bytes();
    assert_eq!(envelope.kid(), Some(signer));
    assert!(
        envelope.verifies_under(&signer),
        "signed by the journal's key"
    );
    // Go's `SealSent` seals these bytes for the same inputs (its
    // `emit.Seal` with the same key, operator, clock, action_id, payload and
    // member; Ed25519 signs deterministically).
    assert_eq!(
        capsule.capsule_id,
        "b23e34ad49c092d272ebd6686428078d43f7c95d07e8af38b1cee448e40263c0"
    );
    assert_eq!(
        dg_bytes(&capsule.capsule),
        "825d6696bc13399d31736d93fd938a44b19aaf4d760010dea696edf4036c6810"
    );
    assert_eq!(
        dg_bytes(&capsule.envelope),
        "196a9c1db5de93811cb9e1f69dae6e47b0a0a773f57322d9c802d70c8d9f38eb"
    );
    // The journal record's own Capsule carries none of the members.
    let stored = String::from_utf8(sent.record.capsule.clone()).unwrap();
    assert!(!stored.contains("deal_carried"));
}

fn dg_bytes(b: &[u8]) -> String {
    hex::encode(Sha256::digest(b))
}

// A refused sent record writes nothing.
#[test]
fn seal_sent_refuses_without_writing() {
    let dir = tempfile::tempdir().unwrap();
    let (shelf, _c, _) = migrated(&dir.path().join("shelf.db"));
    let journal = shelf.journal().unwrap();
    let before = journal.size();
    let none = BTreeMap::new();
    let mut two = sent_entry(SENT_RECORD);
    two.payloads.push(SENT_RECORD.as_bytes().to_vec());
    let invalid: [(&str, BookEntry, &str, &BTreeMap<String, Value>); 6] = [
        ("no action id", sent_entry(SENT_RECORD), "", &none),
        (
            "no payload",
            BookEntry::new("deal_step", EpistemicType::ProducerClaim),
            "a/sent",
            &none,
        ),
        ("two payloads", two, "a/sent", &none),
        ("not JSON", sent_entry("offer"), "a/sent", &none),
        (
            "not canonical",
            sent_entry(r#"{"b":1, "a":2}"#),
            "a/sent",
            &none,
        ),
        (
            "trailing data",
            sent_entry(&format!("{SENT_RECORD}{{}}")),
            "a/sent",
            &none,
        ),
    ];
    for (name, entry, action_id, members) in invalid {
        assert!(
            matches!(
                shelf.seal_sent(&entry, members, action_id),
                Err(ShelfError::Invalid(_))
            ),
            "{name}"
        );
    }
    let header = BTreeMap::from([(super::RECORD_HEADER_MEMBER.to_string(), json!({}))]);
    let bad_name = BTreeMap::from([("Bad".to_string(), json!(1))]);
    let base = BTreeMap::from([("operator".to_string(), json!("x"))]);
    let null = BTreeMap::from([("deal_x".to_string(), Value::Null)]);
    for (name, members) in [
        ("an inline record header", &header),
        ("a name capsule-emit refuses", &bad_name),
        ("a base-profile member", &base),
        ("a null member", &null),
    ] {
        assert!(
            matches!(
                shelf.seal_sent(&sent_entry(SENT_RECORD), members, "a/sent"),
                Err(ShelfError::Invalid(_))
            ),
            "{name}"
        );
    }
    let mut no_type = sent_entry(SENT_RECORD);
    no_type.record_type = String::new();
    assert!(
        shelf.seal_sent(&no_type, &none, "a/sent").is_err(),
        "an invalid header"
    );
    assert_eq!(
        journal.size(),
        before,
        "a refused sent record writes nothing"
    );
}

// seal_sent needs the journal: an unmigrated shelf has none, and a released
// shelf refuses.
#[test]
fn seal_sent_needs_the_journal() {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let none = BTreeMap::new();
    assert!(matches!(
        shelf.seal_sent(&sent_entry(SENT_RECORD), &none, "a/sent"),
        Err(ShelfError::Invalid(_))
    ));
    shelf.release().unwrap();
    assert!(matches!(
        shelf.seal_sent(&sent_entry(SENT_RECORD), &none, "a/sent"),
        Err(ShelfError::Released)
    ));
}

// A record stored whose log append fails is pending in the journal, which
// commits it on its next write: seal_sent returns it and its Capsule with
// the error, so the caller sends it rather than seal it again.
#[test]
fn seal_sent_returns_a_pending_record_with_its_capsule() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shelf.db");
    let (shelf, _c, _) = migrated(&path);
    let journal = shelf.journal().unwrap();
    let before = journal.size();
    let side = Connection::open(&path).unwrap();
    side.busy_timeout(Duration::from_secs(5)).unwrap();
    side.execute_batch(
        "CREATE TRIGGER block BEFORE INSERT ON cll_entries BEGIN SELECT RAISE(ABORT, 'blocked'); END;",
    )
    .unwrap();
    let sent = shelf
        .seal_sent(
            &sent_entry(SENT_RECORD),
            &BTreeMap::new(),
            "deal-0123456789abcdef/sent",
        )
        .expect("the stored record is returned");
    side.execute_batch("DROP TRIGGER block;").unwrap();
    assert!(matches!(sent.error, Some(ShelfError::Log(_))));
    assert!(!sent.capsule.capsule_id.is_empty(), "with its Capsule");
    assert_eq!(sent.capsule.payload, SENT_RECORD.as_bytes());
    assert_eq!(journal.size(), before, "not committed yet");

    // The next write commits it first, at the position it was given.
    let mut next = BookEntry::new("observation", EpistemicType::ObservedEvent);
    next.payloads = vec![b"next".to_vec()];
    let next = journal.append(&next).unwrap();
    assert_eq!(next.seq, sent.record.seq + 1);
    assert_eq!(
        journal.get(&sent.record.record_id).unwrap().seq,
        sent.record.seq
    );
}
