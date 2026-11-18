//! Go's `shelf_receive_test.go`, case for case: the store of received
//! records ([`Shelf::receive`], [`Shelf::forget`], the readers) and stamp
//! proofs ([`Cadence::stamp_proof`], [`verify_stamp_proof`]). The
//! concurrency cases run the call on a thread that must return within 20 s
//! (Go's `withinTime`).

use super::receive::BEFORE_COMMIT;
use super::*;
use crate::record::EpistemicType;
use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};
use std::sync::mpsc;

fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

fn tick_time() -> Time {
    Time::parse("2026-10-31T12:00:30Z").unwrap()
}

fn minutes(n: u64) -> Time {
    tick_time().plus(Duration::from_secs(60 * n))
}

fn hours(n: u64) -> Time {
    tick_time().plus(Duration::from_secs(3600 * n))
}

fn dg(s: &str) -> String {
    hex::encode(Sha256::digest(s.as_bytes()))
}

/// A shelf on `path` under the test keys and a fixed clock.
fn open_shelf(path: &Path, receive: ReceiveConfig) -> Shelf {
    let signer = key("signer");
    let conn = Connection::open(path).unwrap();
    conn.busy_timeout(Duration::from_secs(5)).unwrap();
    let originals = ArtifactSqlite::new(conn, "test", [signer.verifying_key().to_bytes()]).unwrap();
    originals.init().unwrap();
    Shelf::open_sqlite(ShelfConfig {
        path: path.to_path_buf(),
        lock_path: None,
        held_lock: None,
        lock_timeout: Some(Duration::from_secs(5)),
        random: Some(Box::new(SeededStream::new("receive tests"))),
        operator: "test-operator".into(),
        developer: String::new(),
        signer: Arc::new(signer),
        checkpoint_key: key("checkpoint"),
        originals: Arc::new(originals),
        now: Some(Arc::new(tick_time)),
        authenticated_indexes: None,
        correlation_members: Vec::new(),
        receive,
    })
    .unwrap()
}

type Hook = Arc<dyn Fn(&ReceivedRecord) + Send + Sync>;

#[derive(Default)]
struct TestState {
    admitted: HashMap<String, bool>,
    answered: HashSet<String>,
    proof_keys: HashMap<String, String>,
    reads: usize,
    proof_err: Option<String>,
    /// Each after_receive call's record part: digest, deal id, duplicate,
    /// refused.
    after: Vec<(String, String, bool, bool)>,
    after_err: Option<String>,
}

/// The test profile (Go `testReceiveProfile`): JSON records whose `sig`
/// must be "ok"; it admits offers from anyone and other records from the
/// senders it lists, and maps checkpoint keys to senders. Its hooks run
/// outside its mutex, as a profile reading the shelf would.
#[derive(Default)]
struct TestProfile {
    state: Mutex<TestState>,
    on_admit: Mutex<Option<Hook>>,
    on_answered: Mutex<Option<Hook>>,
    /// Runs once `answered` has decided, before it returns.
    after_answered: Mutex<Option<Hook>>,
}

impl TestProfile {
    fn new(senders: &[&str]) -> Arc<Self> {
        let p = Self::default();
        for s in senders {
            p.state.lock().unwrap().admitted.insert(s.to_string(), true);
        }
        Arc::new(p)
    }

    fn st(&self) -> MutexGuard<'_, TestState> {
        self.state.lock().unwrap()
    }

    fn hook(slot: &Mutex<Option<Hook>>) -> Option<Hook> {
        slot.lock().unwrap().clone()
    }
}

impl ReceiveProfile for TestProfile {
    fn read(&self, record: &[u8]) -> Result<ReceivedRecord, String> {
        self.st().reads += 1;
        let v: Value = serde_json::from_slice(record).map_err(|e| e.to_string())?;
        if v["sig"] != "ok" {
            return Err("bad signature".into());
        }
        let s = |k: &str| v[k].as_str().unwrap_or_default().to_string();
        Ok(ReceivedRecord {
            digest: s("digest"),
            sender_key: s("sender"),
            log_id: s("log"),
            position: v["pos"].as_u64().unwrap_or(0),
            prev: s("prev"),
            deal_id: s("deal"),
            offer: v["offer"].as_bool().unwrap_or(false),
        })
    }

    fn admit(&self, r: &ReceivedRecord) -> Result<bool, String> {
        if let Some(h) = Self::hook(&self.on_admit) {
            h(r);
        }
        Ok(r.offer || self.st().admitted.get(&r.sender_key) == Some(&true))
    }

    fn answered(&self, r: &ReceivedRecord) -> Result<bool, String> {
        if let Some(h) = Self::hook(&self.on_answered) {
            h(r);
        }
        let answered = self.st().answered.contains(&r.digest);
        if let Some(h) = Self::hook(&self.after_answered) {
            h(r);
        }
        Ok(answered)
    }

    fn proof_sender(&self, key_id: &str, _log_id: &str) -> Result<Option<String>, String> {
        let st = self.st();
        if let Some(e) = &st.proof_err {
            return Err(e.clone());
        }
        Ok(st.proof_keys.get(key_id).cloned())
    }

    fn after_receive(&self, result: &ReceiveResult, _at: Time) -> Result<(), String> {
        let mut st = self.st();
        if let Some(r) = &result.record {
            st.after.push((
                r.digest.clone(),
                r.deal_id.clone(),
                r.duplicate,
                r.refused.is_some(),
            ));
        }
        match &st.after_err {
            Some(e) => Err(e.clone()),
            None => Ok(()),
        }
    }
}

/// A test record (Go `testRecord`), its members in Go's order.
#[derive(Default)]
struct Rec<'a> {
    digest: String,
    sender: &'a str,
    log: &'a str,
    pos: u64,
    prev: String,
    deal: &'a str,
    offer: bool,
    pad: String,
}

fn rec(r: Rec) -> Vec<u8> {
    let mut m = serde_json::Map::new();
    m.insert("sig".into(), json!("ok"));
    m.insert("digest".into(), json!(r.digest));
    m.insert("sender".into(), json!(r.sender));
    m.insert("log".into(), json!(r.log));
    m.insert("pos".into(), json!(r.pos));
    m.insert("prev".into(), json!(r.prev));
    m.insert("deal".into(), json!(r.deal));
    m.insert("offer".into(), json!(r.offer));
    if !r.pad.is_empty() {
        m.insert("pad".into(), json!(r.pad));
    }
    serde_json::to_vec(&Value::Object(m)).unwrap()
}

/// A record of one sender's log, digests named by name.
fn chain(sender: &str, log: &str, deal: &str, pos: u64, name: &str, prev: &str) -> Vec<u8> {
    rec(Rec {
        digest: dg(name),
        sender,
        log,
        pos,
        prev: if prev.is_empty() {
            String::new()
        } else {
            dg(prev)
        },
        deal,
        ..Default::default()
    })
}

fn offer(sender: &str, name: &str, pad: usize) -> Vec<u8> {
    let log = format!("offers/{name}");
    let deal = format!("offer-{name}");
    rec(Rec {
        digest: dg(name),
        sender,
        log: &log,
        pos: 1,
        deal: &deal,
        offer: true,
        pad: "x".repeat(pad),
        ..Default::default()
    })
}

/// A shelf with the test profile, and what reopens it.
struct Fx {
    dir: tempfile::TempDir,
    profile: Arc<TestProfile>,
    bounds: ReceiveBounds,
    retention: Duration,
}

impl Fx {
    fn new(bounds: ReceiveBounds, retention: Duration) -> (Self, Shelf) {
        let fx = Fx {
            dir: tempfile::tempdir().unwrap(),
            profile: TestProfile::new(&["alice"]),
            bounds,
            retention,
        };
        let shelf = fx.open();
        (fx, shelf)
    }

    fn open(&self) -> Shelf {
        open_shelf(
            &self.dir.path().join("shelf.db"),
            ReceiveConfig {
                profile: Some(self.profile.clone()),
                bounds: self.bounds,
                retention: self.retention,
            },
        )
    }
}

fn fx() -> (Fx, Shelf) {
    Fx::new(ReceiveBounds::default(), Duration::ZERO)
}

fn must(s: &Shelf, record: Option<&[u8]>, proof: Option<&StampProof>) -> ReceiveResult {
    s.receive(record, proof).unwrap()
}

fn must_record(s: &Shelf, record: &[u8]) -> ReceiveResult {
    must(s, Some(record), None)
}

fn count(s: &Shelf, sql: &str) -> i64 {
    s.shared().db().query_row(sql, [], |r| r.get(0)).unwrap()
}

fn received_count(s: &Shelf) -> i64 {
    count(s, "SELECT COUNT(*) FROM shelf_received")
}

fn proof_rows(s: &Shelf) -> i64 {
    count(s, "SELECT COUNT(*) FROM shelf_received_proofs")
}

fn prefix(s: &Shelf, sender: &str, log: &str) -> CountedPrefix {
    s.counted_prefix(sender, log).unwrap()
}

fn cp(size: u64, last: &str, fork_at: u64) -> CountedPrefix {
    CountedPrefix {
        size,
        last: dg(last),
        fork_at,
    }
}

/// Whether any of a failed call's errors matches.
fn fails(e: &ReceiveError, f: fn(&ShelfError) -> bool) -> bool {
    e.errors().into_iter().any(f)
}

fn is_invalid(e: &ShelfError) -> bool {
    matches!(e, ShelfError::Invalid(_))
}

fn is_refused(e: &ShelfError) -> bool {
    matches!(e, ShelfError::Refused(_))
}

fn is_over_limit(e: &ShelfError) -> bool {
    matches!(e, ShelfError::OverLimit(_))
}

fn strs(v: &[&str]) -> Vec<String> {
    v.iter().map(|s| s.to_string()).collect()
}

/// `f` must return within 20 s: otherwise a deadlock.
fn within_time<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    std::thread::spawn(move || {
        let _ = tx.send(f());
    });
    match rx.recv_timeout(Duration::from_secs(20)) {
        Ok(v) => v,
        Err(mpsc::RecvTimeoutError::Timeout) => panic!("deadlock: the call did not return"),
        Err(mpsc::RecvTimeoutError::Disconnected) => panic!("the call panicked"),
    }
}

fn observation(subject: &str, payload: &str) -> BookEntry {
    let mut entry = BookEntry::new("observation", EpistemicType::ObservedEvent);
    entry.subject_ref = Some(subject.into());
    entry.payloads = vec![payload.as_bytes().to_vec()];
    entry
}

type Values = Arc<Mutex<HashMap<String, Value>>>;

/// A cadence whose book statuses come from `values`.
fn stamp_cadence(shelf: &Shelf, values: &Values) -> Cadence {
    let held = values.clone();
    shelf
        .cadence(CadenceConfig {
            log_id: "deal-cadence/0123456789abcdef".into(),
            interval: Duration::from_secs(300),
            jitter: Duration::ZERO,
            no_jitter: true,
            witness_ids: Vec::new(),
            witnesses: None,
            pad_bucket: 1,
            ended: Arc::new(|_| Ok(false)),
            status: Some(Arc::new(move |_: &str, log_id: &str| {
                Ok(held.lock().unwrap().get(log_id).map(|status| StatusValues {
                    profile: "deal".into(),
                    status: Some(status.clone()),
                    ..Default::default()
                }))
            })),
        })
        .unwrap()
}

/// A holder's shelf with one deal book, migrated and stamped: the side that
/// pushes records and stamp proofs (Go `proofHolder`).
struct Holder {
    _dir: tempfile::TempDir,
    shelf: Shelf,
    cadence: Cadence,
    values: Values,
    book_id: String,
}

fn holder(payloads: &[&str]) -> Holder {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open_shelf(&dir.path().join("shelf.db"), ReceiveConfig::default());
    let values: Values = Arc::default();
    let cadence = stamp_cadence(&shelf, &values);
    values
        .lock()
        .unwrap()
        .insert("deal/a".into(), json!({"row": "open"}));
    let book = shelf.open("deal/a").unwrap();
    for p in payloads {
        book.append(&observation("a", p)).unwrap();
    }
    cadence.tick(tick_time()).unwrap();
    cadence.migrate(minutes(1)).unwrap();
    let book_id: String = shelf
        .shared()
        .db()
        .query_row(
            "SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' AND kind = 'book_fact' LIMIT 1",
            [],
            |r| r.get(0),
        )
        .unwrap();
    Holder {
        _dir: dir,
        shelf,
        cadence,
        values,
        book_id,
    }
}

impl Holder {
    fn proof(&self, audience: &str) -> StampProof {
        self.cadence
            .stamp_proof(&self.book_id, 0, audience)
            .unwrap()
    }

    /// The holder's book log entries as the test profile's records.
    fn records(&self, sender: &str, deal: &str) -> Vec<Vec<u8>> {
        let rows: Vec<(i64, Vec<u8>)> = {
            let db = self.shelf.shared().db();
            let mut stmt = db
                .prepare("SELECT seq, value FROM cll_entries WHERE log_id = 'deal/a' ORDER BY seq")
                .unwrap();
            let rows = stmt.query_map([], |r| Ok((r.get(0)?, r.get(1)?))).unwrap();
            rows.collect::<Result<_, _>>().unwrap()
        };
        let mut prev = String::new();
        rows.into_iter()
            .map(|(seq, value)| {
                let digest = hex::encode(value);
                rec(Rec {
                    digest: digest.clone(),
                    sender,
                    log: "deal/a",
                    pos: seq as u64,
                    prev: std::mem::replace(&mut prev, digest),
                    deal,
                    ..Default::default()
                })
            })
            .collect()
    }
}

fn key_of(p: &StampProof) -> String {
    Checkpoint::from_statement(&p.book_checkpoint)
        .unwrap()
        .key_id
}

// after_receive runs on every call, a duplicate and a refusal included, with
// the deal the profile read each record as; its error fails the call, and the
// parts' result stays committed (Go `AfterReceive`).
#[test]
fn after_receive_runs_on_every_call() {
    let (f, s) = fx();
    let r1 = chain("alice", "deal/a", "deal-1", 1, "a1", "");
    must_record(&s, &r1);
    must_record(&s, &r1);
    let e = s
        .receive(
            Some(&chain("mallory", "deal/m", "deal-m", 1, "m1", "")),
            None,
        )
        .unwrap_err();
    assert!(fails(&e, is_refused) && e.after.is_none());
    assert_eq!(
        f.profile.st().after,
        vec![
            (dg("a1"), "deal-1".to_string(), false, false),
            (dg("a1"), "deal-1".to_string(), true, false),
            (dg("m1"), "deal-m".to_string(), false, true),
        ]
    );

    f.profile.st().after_err = Some("naming failed".into());
    let e = s
        .receive(
            Some(&chain("alice", "deal/a", "deal-1", 2, "a2", "a1")),
            None,
        )
        .unwrap_err();
    assert!(e.error.is_none());
    assert!(matches!(&e.after, Some(ShelfError::Profile(m)) if m.contains("naming failed")));
    assert_eq!(e.errors().len(), 1);
    assert!(
        e.result.record.as_ref().unwrap().stored,
        "the parts' result stays committed"
    );
    assert_eq!(received_count(&s), 2);
}

#[test]
fn a_duplicate_is_acknowledged_and_not_stored_again() {
    let (_f, s) = fx();
    let r1 = chain("alice", "deal/a", "deal-1", 1, "a1", "");
    let got = must_record(&s, &r1);
    let out = got.record.unwrap();
    assert!(out.stored && out.counted);
    assert_eq!(got.changes.grew, strs(&["deal-1"]));

    let again = must_record(&s, &r1);
    let out = again.record.unwrap();
    assert!(!out.stored && out.duplicate && out.counted);
    assert!(again.changes.grew.is_empty());
    assert_eq!(received_count(&s), 1);

    // Another record under a held digest is refused.
    let other = rec(Rec {
        digest: dg("a1"),
        sender: "alice",
        log: "deal/a",
        pos: 1,
        deal: "deal-1",
        pad: "x".into(),
        ..Default::default()
    });
    assert!(fails(
        &s.receive(Some(&other), None).unwrap_err(),
        is_invalid
    ));
}

#[test]
fn a_gap_is_stored_not_counted_and_its_fill_joins_the_prefix() {
    let (f, s) = fx();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    let got = must_record(&s, &chain("alice", "deal/a", "deal-1", 3, "a3", "a2"));
    let out = got.record.unwrap();
    assert!(out.stored && !out.counted);
    assert!(
        out.ask_for_book,
        "a record past the counted prefix asks for the book"
    );
    assert!(got.changes.grew.is_empty());
    must_record(&s, &chain("alice", "deal/a", "deal-1", 5, "a5", "a4"));
    assert_eq!(prefix(&s, "alice", "deal/a"), cp(1, "a1", 0));

    let fill = must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    assert!(fill.record.unwrap().counted);
    assert_eq!(fill.changes.grew, strs(&["deal-1"]));
    assert_eq!(
        prefix(&s, "alice", "deal/a"),
        cp(3, "a3", 0),
        "the fill joins the prefix up to the next gap"
    );

    // A restart recomputes the same prefix from the store.
    s.release().unwrap();
    let reopened = f.open();
    assert_eq!(prefix(&reopened, "alice", "deal/a"), cp(3, "a3", 0));
    let again = must_record(
        &reopened,
        &chain("alice", "deal/a", "deal-1", 4, "a4", "a3"),
    );
    assert_eq!(again.changes.grew, strs(&["deal-1"]));
    assert_eq!(prefix(&reopened, "alice", "deal/a"), cp(5, "a5", 0));
}

#[test]
fn a_fork_keeps_both_and_ends_the_prefix_for_good() {
    let (_f, s) = fx();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    let fork = must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2-other", "a1"));
    let out = fork.record.unwrap();
    assert!(out.stored, "both records of a fork are kept");
    assert!(!out.counted);
    assert_eq!(fork.changes.forked, strs(&["deal-1"]));
    assert_eq!(fork.changes.shrank, strs(&["deal-1"]));
    assert_eq!(received_count(&s), 3);
    assert_eq!(prefix(&s, "alice", "deal/a"), cp(1, "a1", 2));

    let evidence = s.evidence().unwrap();
    assert_eq!(evidence.len(), 1);
    assert_eq!(evidence[0].kind, EVIDENCE_FORK);
    assert!(String::from_utf8_lossy(&evidence[0].a).contains(&dg("a2")));
    assert!(String::from_utf8_lossy(&evidence[0].b).contains(&dg("a2-other")));

    // For good: records after the fork never join the prefix.
    must_record(&s, &chain("alice", "deal/a", "deal-1", 3, "a3", "a2"));
    assert_eq!(prefix(&s, "alice", "deal/a").size, 1);
}

#[test]
fn a_late_fork_shrinks_the_prefix() {
    let (_f, s) = fx();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 3, "a3", "a2"));
    assert_eq!(prefix(&s, "alice", "deal/a").size, 3);
    // A record at 4 naming another predecessor than the one held at 3 is a
    // fork at 3.
    let late = must_record(&s, &chain("alice", "deal/a", "deal-1", 4, "a4", "a3-other"));
    assert_eq!(late.changes.forked, strs(&["deal-1"]));
    assert_eq!(late.changes.shrank, strs(&["deal-1"]));
    assert_eq!(prefix(&s, "alice", "deal/a"), cp(2, "a2", 3));
    let evidence = s.evidence().unwrap();
    assert_eq!(evidence.len(), 1);
    assert_eq!(evidence[0].kind, EVIDENCE_FORK);
}

#[test]
fn a_sender_is_refused_and_nothing_stored() {
    let (_f, s) = fx();
    let e = s
        .receive(
            Some(&chain("mallory", "deal/m", "deal-m", 1, "m1", "")),
            None,
        )
        .unwrap_err();
    assert!(e.error.is_none() && fails(&e, is_refused));
    let out = e.result.record.as_ref().unwrap();
    assert!(out.refused.as_ref().is_some_and(is_refused));
    assert!(!out.stored);
    assert_eq!(received_count(&s), 0);
    assert!(matches!(
        s.received_record_of(&dg("m1")),
        Err(ShelfError::NotFound(_))
    ));

    // A record the profile cannot read (a bad signature) is invalid.
    let bad = serde_json::to_vec(
        &json!({"sig": "forged", "digest": dg("x"), "sender": "alice",
        "log": "deal/a", "pos": 1, "prev": "", "deal": "d", "offer": false}),
    )
    .unwrap();
    assert!(fails(&s.receive(Some(&bad), None).unwrap_err(), is_invalid));
    // A record that names no predecessor past position 1 is invalid.
    let no_prev = rec(Rec {
        digest: dg("y"),
        sender: "alice",
        log: "deal/a",
        pos: 2,
        deal: "d",
        ..Default::default()
    });
    assert!(fails(
        &s.receive(Some(&no_prev), None).unwrap_err(),
        is_invalid
    ));
    assert!(fails(&s.receive(None, None).unwrap_err(), is_invalid));
    assert_eq!(received_count(&s), 0);
}

#[test]
fn receive_without_a_profile_is_refused() {
    let dir = tempfile::tempdir().unwrap();
    let s = open_shelf(&dir.path().join("shelf.db"), ReceiveConfig::default());
    assert!(fails(
        &s.receive(Some(b"{}"), None).unwrap_err(),
        is_invalid
    ));
}

#[test]
fn an_oversize_record_is_refused_before_it_is_read() {
    let (f, s) = Fx::new(
        ReceiveBounds {
            max_record_bytes: 64,
            ..Default::default()
        },
        Duration::ZERO,
    );
    let big = rec(Rec {
        digest: dg("a1"),
        sender: "alice",
        log: "deal/a",
        pos: 1,
        deal: "deal-1",
        pad: "x".repeat(64),
        ..Default::default()
    });
    let e = s.receive(Some(&big), None).unwrap_err();
    assert!(fails(&e, is_over_limit));
    assert!(!e.result.record.unwrap().stored);
    assert_eq!(f.profile.st().reads, 0);
    assert_eq!(received_count(&s), 0);
}

#[test]
fn a_record_past_the_per_book_bound_is_refused() {
    let (_f, s) = Fx::new(
        ReceiveBounds {
            max_records_per_log: 2,
            ..Default::default()
        },
        Duration::ZERO,
    );
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    let e = s
        .receive(
            Some(&chain("alice", "deal/a", "deal-1", 3, "a3", "a2")),
            None,
        )
        .unwrap_err();
    assert!(fails(&e, is_over_limit));
    assert_eq!(received_count(&s), 2);
    // Another log of the same sender has its own bound.
    must_record(&s, &chain("alice", "deal/b", "deal-2", 1, "b1", ""));
}

fn held(s: &Shelf, name: &str) -> bool {
    match s.received_record_of(&dg(name)) {
        Ok(_) => true,
        Err(ShelfError::NotFound(_)) => false,
        Err(e) => panic!("{e}"),
    }
}

#[test]
fn the_offer_bounds_drop_the_oldest_unanswered_offer() {
    let (f, s) = Fx::new(
        ReceiveBounds {
            max_offers_per_sender: 2,
            max_offers: 3,
            max_offer_bytes_per_sender: 1000,
            ..Default::default()
        },
        Duration::ZERO,
    );
    // Offers come from any key, admit says.
    for name in ["o1", "o2", "o3"] {
        assert!(
            must_record(&s, &offer("bob", name, 0))
                .record
                .unwrap()
                .stored
        );
    }
    assert!(
        !held(&s, "o1"),
        "the oldest unanswered offer is dropped at the per-sender bound"
    );
    assert!(
        matches!(
            s.received_record_of(&dg("o1")),
            Err(ShelfError::NotFound(_))
        ),
        "a dropped offer leaves no tombstone"
    );
    assert!(held(&s, "o2") && held(&s, "o3"));

    // An answered offer leaves the bounds and is never dropped.
    f.profile.st().answered.insert(dg("o2"));
    must_record(&s, &offer("bob", "o4", 0));
    assert!(held(&s, "o2") && held(&s, "o3") && held(&s, "o4"));

    // The total bound drops the oldest unanswered offer of any sender.
    must_record(&s, &offer("carol", "c1", 0));
    assert!(held(&s, "o3"));
    must_record(&s, &offer("carol", "c2", 0));
    assert!(!held(&s, "o3"));
    assert!(held(&s, "o2") && held(&s, "o4") && held(&s, "c1") && held(&s, "c2"));

    // An offer over a byte bound alone is refused.
    let e = s
        .receive(Some(&offer("dave", "d1", 1000)), None)
        .unwrap_err();
    assert!(fails(&e, is_over_limit));
    assert!(!held(&s, "d1"));
}

#[test]
fn the_offer_bytes_bound_drops_the_oldest() {
    let one = offer("bob", "o1", 150).len() as u64;
    let (_f, s) = Fx::new(
        ReceiveBounds {
            max_offer_bytes_per_sender: 2 * one + 10,
            ..Default::default()
        },
        Duration::ZERO,
    );
    must_record(&s, &offer("bob", "o1", 150));
    must_record(&s, &offer("bob", "o2", 150));
    assert!(held(&s, "o1"));
    must_record(&s, &offer("bob", "o3", 150));
    assert!(!held(&s, "o1"));
}

#[test]
fn a_stamp_proof_verifies_and_hands_openings_by_audience() {
    let h = holder(&["1", "2"]);
    let p = h.proof("counterparty");
    let v = verify_stamp_proof(&p).unwrap();
    assert_eq!(v.fact.log_id, "deal/a");
    assert_eq!(v.book_checkpoint.key_id, v.shelf_checkpoint.key_id);
    assert_eq!(
        p.status_opening.as_ref().unwrap().value,
        json!({"row": "open"})
    );

    assert!(h.proof("chosen_reader").status_opening.is_some());
    assert!(h.proof("adjudicator").status_opening.is_none());
    assert!(matches!(
        h.cadence.stamp_proof(&h.book_id, 0, "stranger"),
        Err(ShelfError::Invalid(_))
    ));
    assert!(matches!(
        h.cadence
            .stamp_proof(&dg("no such book"), 0, "counterparty"),
        Err(ShelfError::NotFound(_))
    ));

    // Each part of a proof is checked.
    type Tamper = fn(&mut StampProof);
    let tampers: [(&str, Tamper); 5] = [
        ("fact", |p| {
            p.fact.pop();
            p.fact.extend_from_slice(b" }");
        }),
        ("opening", |p| {
            p.status_opening.as_mut().unwrap().value = json!({"row": "closed"});
        }),
        ("inclusion", |p| p.inclusion.seq += 1),
        ("book", |p| p.book_checkpoint = p.shelf_checkpoint.clone()),
        ("shelf", |p| p.shelf_checkpoint = p.book_checkpoint.clone()),
    ];
    for (name, tamper) in tampers {
        let mut q = h.proof("counterparty");
        tamper(&mut q);
        assert!(
            matches!(verify_stamp_proof(&q), Err(ShelfError::Invalid(_))),
            "{name}"
        );
    }
    let mut q = h.proof("counterparty");
    q.status_opening = None;
    verify_stamp_proof(&q).expect("no opening");
}

#[test]
fn a_valid_proof_covers_the_records() {
    let h = holder(&["1", "2", "3"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    for r in h.records("alice", "deal-1") {
        must_record(&s, &r);
    }
    let got = must(&s, None, Some(&p));
    assert!(got.proof.unwrap().accepted);
    assert_eq!(got.changes.covered, strs(&["deal-1"]));
    assert_eq!(proof_rows(&s), 1);

    // The same proof again is no contradiction, and is not kept twice.
    let again = must(&s, None, Some(&p));
    let out = again.proof.unwrap();
    assert!(out.duplicate && !out.accepted);
    assert!(again.changes.covered.is_empty());
    assert_eq!(proof_rows(&s), 1);
    assert!(s.evidence().unwrap().is_empty());

    // A proof from a key the profile does not relate to a sender is refused.
    f.profile.st().proof_keys.remove(&key_of(&p));
    assert!(fails(&s.receive(None, Some(&p)).unwrap_err(), is_refused));
}

#[test]
fn a_duplicate_record_still_delivers_the_proof_it_lacked() {
    let h = holder(&["1"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    let records = h.records("alice", "deal-1");
    for r in &records {
        must_record(&s, r);
    }
    let got = must(&s, records.last().map(Vec::as_slice), Some(&p));
    assert!(got.record.unwrap().duplicate);
    assert!(got.proof.unwrap().accepted);
    assert_eq!(proof_rows(&s), 1);

    // A refused record does not stop its proof either.
    f.profile.st().admitted.insert("alice".into(), false);
    let q = h.proof("chosen_reader");
    let e = s
        .receive(
            Some(&chain("alice", "deal/z", "deal-z", 1, "z1", "")),
            Some(&q),
        )
        .unwrap_err();
    assert!(fails(&e, is_refused));
    assert!(e
        .result
        .record
        .as_ref()
        .unwrap()
        .refused
        .as_ref()
        .is_some_and(is_refused));
    assert!(e.result.proof.as_ref().unwrap().duplicate);
}

#[test]
fn a_contradicting_proof_is_refused_and_kept_as_evidence() {
    let h = holder(&["1", "2"]);
    let other = holder(&["x", "y"]); // the same key, another book at the same size
    let (f, s) = fx();
    let (p, q) = (h.proof("counterparty"), other.proof("counterparty"));
    assert_eq!(key_of(&p), key_of(&q));
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    must(&s, None, Some(&p));

    let e = s.receive(None, Some(&q)).unwrap_err();
    assert!(fails(&e, is_invalid));
    let out = e.result.proof.as_ref().unwrap();
    assert!(out.refused.as_ref().is_some_and(is_invalid));
    assert!(!out.accepted);
    assert_eq!(proof_rows(&s), 1);
    let evidence = s.evidence().unwrap();
    assert_eq!(evidence.len(), 1);
    assert_eq!(evidence[0].kind, EVIDENCE_CHECKPOINT);
    assert_eq!(evidence[0].a, p.book_checkpoint);
    assert_eq!(evidence[0].b, q.book_checkpoint);

    // The held proof survives a restart, and the contradiction is found
    // against it again, its evidence kept once.
    s.release().unwrap();
    let reopened = f.open();
    assert!(fails(
        &reopened.receive(None, Some(&q)).unwrap_err(),
        is_invalid
    ));
    assert_eq!(reopened.evidence().unwrap().len(), 1);
}

#[test]
fn a_proof_the_records_held_do_not_recompute_is_refused() {
    let h = holder(&["1", "2"]);
    let other = holder(&["x", "y"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    for r in other.records("alice", "deal-1") {
        must_record(&s, &r);
    }
    let e = s.receive(None, Some(&p)).unwrap_err();
    assert!(fails(&e, is_invalid));
    assert_eq!(e.result.changes.forked, strs(&["deal-1"]));
    assert_eq!(proof_rows(&s), 0);
    let evidence = s.evidence().unwrap();
    assert_eq!(evidence.len(), 1);
    assert_eq!(evidence[0].kind, EVIDENCE_CHECKPOINT_RECORDS);
}

#[test]
fn records_that_contradict_a_held_proof_are_evidence() {
    let h = holder(&["1", "2"]);
    let other = holder(&["x", "y"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    must(&s, None, Some(&p)); // before any record: nothing to check it against
    let mut last = None;
    for r in other.records("alice", "deal-1") {
        last = Some(must_record(&s, &r));
    }
    assert_eq!(last.unwrap().changes.forked, strs(&["deal-1"]));
    let evidence = s.evidence().unwrap();
    assert_eq!(evidence.len(), 1);
    assert_eq!(evidence[0].kind, EVIDENCE_CHECKPOINT_RECORDS);
}

#[test]
fn one_book_checkpoint_under_two_shelf_checkpoints_keeps_both() {
    let h = holder(&["1"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    must(&s, None, Some(&p));

    // A received-only change: a new fact at the same size and checkpoint,
    // under a new shelf checkpoint.
    h.values
        .lock()
        .unwrap()
        .insert("deal/a".into(), json!({"row": "agreed"}));
    h.cadence.stamp(minutes(2)).unwrap();
    let q = h.proof("counterparty");
    assert_eq!(p.book_checkpoint, q.book_checkpoint);
    assert_ne!(p.shelf_checkpoint, q.shelf_checkpoint);
    let got = must(&s, None, Some(&q));
    assert!(got.proof.unwrap().accepted);
    assert!(
        got.changes.covered.is_empty(),
        "the same size covers no new record"
    );
    assert_eq!(proof_rows(&s), 2);
}

#[test]
fn forget_waits_for_the_deal_to_settle_and_leaves_tombstones() {
    let (_f, s) = Fx::new(ReceiveBounds::default(), Duration::from_secs(3600));
    let r1 = chain("alice", "deal/a", "deal-1", 1, "a1", "");
    must_record(&s, &r1);
    must_record(&s, &chain("alice", "deal/b", "deal-2", 1, "b1", ""));
    let later = hours(2);
    let is_settled = |settled: bool| move |deal: &str| Ok(deal == "deal-1" && settled);

    assert!(
        matches!(
            s.forget("deal-1", later, is_settled(false)),
            Err(ShelfError::Invalid(_))
        ),
        "nothing of a deal is deleted before it is settled"
    );
    assert_eq!(received_count(&s), 2);

    // Records younger than the retention stay.
    assert!(s
        .forget("deal-1", tick_time(), is_settled(true))
        .unwrap()
        .is_empty());
    assert_eq!(
        s.forget("deal-1", later, is_settled(true)).unwrap(),
        strs(&[&dg("a1")])
    );
    assert!(
        matches!(s.received_record_of(&dg("a1")), Err(ShelfError::Deleted(_))),
        "a deleted record is reported deleted, not missing"
    );
    s.received_record_of(&dg("b1")).unwrap();

    // Sent again, it is acknowledged as deleted, not stored again.
    let again = must_record(&s, &r1).record.unwrap();
    assert!(again.deleted && !again.stored);
    assert_eq!(received_count(&s), 1);
}

#[test]
fn forget_is_refused_when_the_holder_keeps_records_for_the_shelfs_life() {
    let (_f, s) = fx();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    assert!(matches!(
        s.forget("deal-1", hours(1000), |_| Ok(true)),
        Err(ShelfError::Invalid(_))
    ));
    assert_eq!(received_count(&s), 1);
}

#[test]
fn receive_works_on_a_version_one_shelf() {
    let (_f, s) = fx();
    assert_eq!(count(&s, "SELECT version FROM shelf_meta"), SHELF_VERSION);
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
}

#[test]
fn receive_is_refused_after_release() {
    let (_f, s) = fx();
    s.release().unwrap();
    let e = s
        .receive(Some(&chain("alice", "deal/a", "deal-1", 1, "a1", "")), None)
        .unwrap_err();
    assert!(matches!(e.error, Some(ShelfError::Released)));
}

// P1-1: the store's readers take no lock, so a status callback calls them
// during a stamp, which holds the shelf's lock.
#[test]
fn the_received_stores_readers_run_inside_a_stamp() {
    let (_f, s) = fx();
    let reads = Arc::new(Mutex::new(0usize));
    let (reader, counter) = (s.clone(), reads.clone());
    let c = s
        .cadence(CadenceConfig {
            log_id: "deal-cadence/0123456789abcdef".into(),
            interval: Duration::from_secs(300),
            jitter: Duration::ZERO,
            no_jitter: true,
            witness_ids: Vec::new(),
            witnesses: None,
            pad_bucket: 1,
            ended: Arc::new(|_| Ok(false)),
            status: Some(Arc::new(move |_: &str, _: &str| {
                let p = reader
                    .counted_prefix("alice", "deal/a")
                    .map_err(|e| e.to_string())?;
                reader.evidence().map_err(|e| e.to_string())?;
                reader
                    .received_record_of(&dg("a1"))
                    .map_err(|e| e.to_string())?;
                *counter.lock().unwrap() += 1;
                Ok(Some(StatusValues {
                    profile: "deal".into(),
                    last_received: (p.size > 0).then(
                        || json!({"log_id": "deal/a", "size": p.size, "record_digest": p.last}),
                    ),
                    checked_over: Vec::new(),
                    status: Some(json!({})),
                }))
            })),
        })
        .unwrap();
    s.open("deal/ours")
        .unwrap()
        .append(&observation("a", "1"))
        .unwrap();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    c.tick(tick_time()).unwrap();
    let (shelf, cadence) = (s.clone(), c.clone());
    within_time(move || {
        cadence.migrate(minutes(1)).unwrap();
        shelf
            .open("deal/ours")
            .unwrap()
            .append(&observation("a", "2"))
            .unwrap();
        cadence.stamp(minutes(2)).unwrap();
    });
    assert!(*reads.lock().unwrap() > 0);
}

// P1-2: an answer committed between a pushed offer's read and the drop
// keeps the answered offer, and a profile reading the shelf in `answered`
// does not deadlock.
#[test]
fn an_answer_committed_before_the_drop_keeps_the_offer() {
    let (f, s) = Fx::new(
        ReceiveBounds {
            max_offers_per_sender: 1,
            ..Default::default()
        },
        Duration::ZERO,
    );
    must_record(&s, &offer("bob", "o1", 0));
    let profile = f.profile.clone();
    *f.profile.on_admit.lock().unwrap() = Some(Arc::new(move |r: &ReceivedRecord| {
        if r.digest == dg("o2") {
            profile.st().answered.insert(dg("o1"));
        }
    }));
    let asked = Arc::new(Mutex::new(0));
    let (reader, counter) = (s.clone(), asked.clone());
    *f.profile.on_answered.lock().unwrap() = Some(Arc::new(move |_: &ReceivedRecord| {
        *counter.lock().unwrap() += 1;
        reader.counted_prefix("bob", "offers/o1").unwrap();
        reader.received_record_of(&dg("o1")).unwrap();
    }));
    let shelf = s.clone();
    within_time(move || {
        must_record(&shelf, &offer("bob", "o2", 0));
    });
    assert_eq!(*asked.lock().unwrap(), 1);
    assert!(held(&s, "o1"), "the answered offer is kept");
    assert!(held(&s, "o2"));
}

// P1-3: a fork's position ends the counted prefix for good, whatever is
// deleted later, also after a restart.
#[test]
fn a_fork_survives_forget_and_restart() {
    let (f, s) = Fx::new(ReceiveBounds::default(), Duration::from_secs(3600));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    must_record(&s, &chain("alice", "deal/a", "deal-x", 2, "a2-other", "a1"));
    let want = cp(1, "a1", 2);
    assert_eq!(prefix(&s, "alice", "deal/a"), want);
    let gone = s.forget("deal-x", hours(2), |_| Ok(true)).unwrap();
    assert_eq!(gone, strs(&[&dg("a2-other")]));
    assert_eq!(
        prefix(&s, "alice", "deal/a"),
        want,
        "deleting one record of a fork does not undo it"
    );
    s.release().unwrap();
    assert_eq!(prefix(&f.open(), "alice", "deal/a"), want);
}

// P1-4: repeated forks at one position keep one evidence row.
#[test]
fn repeated_forks_at_one_position_keep_one_evidence_row() {
    let (_f, s) = fx();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    let first = must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2-b", "a1"));
    assert_eq!(first.changes.forked, strs(&["deal-1"]));
    for name in ["a2-c", "a2-d", "a2-e"] {
        let got = must_record(&s, &chain("alice", "deal/a", "deal-1", 2, name, "a1"));
        assert!(
            got.changes.forked.is_empty(),
            "a fork already recorded at the position is not news"
        );
    }
    assert_eq!(s.evidence().unwrap().len(), 1);
}

// P1-5: a proof past the records held grants no coverage; once the records
// arrive, a smaller proof they contradict is evidence and the larger one
// they recompute covers them.
#[test]
fn a_proof_past_the_records_grants_no_coverage_until_they_recompute_it() {
    let small = holder(&["1", "2"]);
    let large = holder(&["x", "y", "z"]);
    let (f, s) = fx();
    let (a, b) = (small.proof("counterparty"), large.proof("counterparty"));
    f.profile.st().proof_keys.insert(key_of(&a), "alice".into());
    assert!(must(&s, None, Some(&a)).proof.unwrap().accepted);
    let got = must(&s, None, Some(&b));
    assert!(got.proof.unwrap().accepted);
    assert!(
        got.changes.covered.is_empty(),
        "a proof past the records held grants no coverage"
    );

    let (mut covered, mut forked) = (Vec::new(), Vec::new());
    for r in large.records("alice", "deal-1") {
        let got = must_record(&s, &r);
        covered.extend(got.changes.covered);
        forked.extend(got.changes.forked);
    }
    assert_eq!(covered, strs(&["deal-1"]));
    assert_eq!(forked, strs(&["deal-1"]));
    let evidence = s.evidence().unwrap();
    assert_eq!(evidence.len(), 1);
    assert_eq!(evidence[0].kind, EVIDENCE_CHECKPOINT_RECORDS);
    assert_eq!(evidence[0].a, a.book_checkpoint);
}

// P2-6: a store or profile error in the proof part still returns the record
// part's committed outcome and changes.
#[test]
fn a_proof_error_keeps_the_records_outcome() {
    let h = holder(&["1"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_err = Some("profile store down".into());
    let e = s
        .receive(
            Some(&chain("alice", "deal/a", "deal-1", 1, "a1", "")),
            Some(&p),
        )
        .unwrap_err();
    assert!(matches!(&e.error, Some(ShelfError::Profile(m)) if m == "profile store down"));
    assert!(e.result.record.as_ref().unwrap().stored);
    assert!(e.result.proof.is_none());
    assert_eq!(e.result.changes.grew, strs(&["deal-1"]));
}

// P2-7: a proof held first covers the deal when the record that completes
// the counted prefix up to its size arrives last.
#[test]
fn a_proof_held_first_covers_when_a_gap_is_filled_last() {
    let h = holder(&["1", "2", "3"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    assert!(must(&s, None, Some(&p)).changes.covered.is_empty());
    let records = h.records("alice", "deal-1");
    assert!(records.len() > 2);
    for (i, r) in records.iter().enumerate() {
        if i == 1 {
            continue;
        }
        assert!(must_record(&s, r).changes.covered.is_empty());
    }
    let got = must_record(&s, &records[1]);
    assert_eq!(got.changes.covered, strs(&["deal-1"]));
    assert_eq!(got.changes.grew, strs(&["deal-1"]));
}

// P2-8: a dropped offer's deal shrank; it never grew, also when dropping it
// leaves one record where two forked.
#[test]
fn a_dropped_offer_is_reported_shrank_only() {
    let one = ReceiveBounds {
        max_offers_per_sender: 1,
        ..Default::default()
    };
    let (_f, s) = Fx::new(one, Duration::ZERO);
    must_record(&s, &offer("bob", "o1", 0));
    let got = must_record(&s, &offer("bob", "o2", 0));
    assert_eq!(got.changes.shrank, strs(&["offer-o1"]));
    assert_eq!(got.changes.grew, strs(&["offer-o2"]));

    // Two offers at one position fork; dropping the older one does not
    // resolve the fork, and its deal is not reported grown.
    let two = ReceiveBounds {
        max_offers_per_sender: 2,
        ..Default::default()
    };
    let (_f, s) = Fx::new(two, Duration::ZERO);
    let fork_offer = |name: &str| {
        let deal = format!("offer-{name}");
        rec(Rec {
            digest: dg(name),
            sender: "bob",
            log: "offers/shared",
            pos: 1,
            deal: &deal,
            offer: true,
            ..Default::default()
        })
    };
    must_record(&s, &fork_offer("f1"));
    must_record(&s, &fork_offer("f2"));
    let got = must_record(&s, &offer("bob", "o3", 0));
    assert!(!got.changes.grew.contains(&"offer-f1".to_string()));
    assert!(!got.changes.grew.contains(&"offer-f2".to_string()));
    assert_eq!(prefix(&s, "bob", "offers/shared").fork_at, 1);
}

// P3-9: grew names the deals whose records entered the counted prefix, not
// every deal held in the log.
#[test]
fn grew_names_only_deals_whose_records_were_counted() {
    let (_f, s) = fx();
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-2", 3, "a3", "a2"));
    must_record(&s, &chain("alice", "deal/a", "deal-3", 5, "a5", "a4"));
    let got = must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    assert_eq!(
        got.changes.grew,
        strs(&["deal-1", "deal-2"]),
        "deal-3 is past the next gap"
    );
    assert!(got.changes.shrank.is_empty());
}

// R2-1: a proof verified against records forget deleted grants no coverage
// to other records arriving at the deleted positions: they are a fork.
#[test]
fn a_proof_does_not_cover_records_replacing_deleted_ones() {
    let h = holder(&["1", "2"]);
    let other = holder(&["x", "y"]);
    let (f, s) = Fx::new(ReceiveBounds::default(), Duration::from_secs(3600));
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    for r in h.records("alice", "deal-1") {
        must_record(&s, &r);
    }
    assert_eq!(must(&s, None, Some(&p)).changes.covered, strs(&["deal-1"]));
    s.forget("deal-1", hours(2), |_| Ok(true)).unwrap();

    let (mut covered, mut forked) = (Vec::new(), Vec::new());
    for r in other.records("alice", "deal-2") {
        let got = must_record(&s, &r);
        covered.extend(got.changes.covered);
        forked.extend(got.changes.forked);
    }
    assert!(
        covered.is_empty(),
        "the proof was checked against records no longer held"
    );
    assert!(forked.contains(&"deal-2".to_string()));
    assert_eq!(
        prefix(&s, "alice", "deal/a").fork_at,
        1,
        "another record at a deleted position is a fork"
    );

    // A dropped offer's position is remembered the same way, within the
    // offer bounds (here the defaults).
    let (_f2, s2) = Fx::new(
        ReceiveBounds {
            max_offers_per_sender: 1,
            ..Default::default()
        },
        Duration::ZERO,
    );
    let at_x = |name: &str, deal: &str| {
        rec(Rec {
            digest: dg(name),
            sender: "bob",
            log: "offers/x",
            pos: 1,
            deal,
            offer: true,
            ..Default::default()
        })
    };
    must_record(&s2, &at_x("o1", "offer-o1"));
    must_record(&s2, &offer("bob", "o2", 0)); // drops o1
    must_record(&s2, &at_x("o1-other", "offer-o3"));
    assert_eq!(prefix(&s2, "bob", "offers/x").fork_at, 1);
}

// R2-2: a sender rotating log ids, each with two conflicting offers at
// position 1, keeps no more evidence than the offer evidence bounds allow;
// each fork still ends its log's counted prefix.
#[test]
fn offer_forks_across_rotated_logs_keep_bounded_evidence() {
    let (_f, s) = Fx::new(
        ReceiveBounds {
            max_offers_per_sender: 4,
            max_offer_evidence_per_sender: 2,
            max_offer_evidence: 3,
            ..Default::default()
        },
        Duration::ZERO,
    );
    let push = |sender: &str, log: &str, name: &str| {
        let deal = format!("offer-{name}");
        must_record(
            &s,
            &rec(Rec {
                digest: dg(name),
                sender,
                log,
                pos: 1,
                deal: &deal,
                offer: true,
                pad: "x".repeat(100),
                ..Default::default()
            }),
        );
    };
    for i in 0..6u8 {
        let log = format!("offers/rot-{}", (b'a' + i) as char);
        push("bob", &log, &format!("{log}-1"));
        push("bob", &log, &format!("{log}-2"));
        assert_eq!(prefix(&s, "bob", &log).fork_at, 1);
    }
    for i in 0..3u8 {
        let log = format!("offers/carol-{}", (b'a' + i) as char);
        push("carol", &log, &format!("{log}-1"));
        push("carol", &log, &format!("{log}-2"));
    }
    let evidence = s.evidence().unwrap();
    assert_eq!(
        evidence.len(),
        3,
        "two of bob's, one of carol's, then the total bound"
    );
    let bytes: usize = evidence.iter().map(|e| e.a.len() + e.b.len()).sum();
    assert!(bytes <= 3 * 2 * 600);

    // What offers leave behind, the dropped offers' positions and the
    // conflicts in logs of offers alone, stays within the same bounds: two
    // rows for bob, three in all.
    let vacated = count(&s, "SELECT COUNT(*) FROM shelf_received_vacated");
    let conflicts = count(&s, "SELECT COUNT(*) FROM shelf_received_conflicts");
    let bobs = count(
        &s,
        "SELECT (SELECT COUNT(*) FROM shelf_received_vacated WHERE sender_key = 'bob') + (SELECT COUNT(*) FROM shelf_received_conflicts WHERE sender_key = 'bob')",
    );
    assert!(vacated + conflicts <= 3);
    assert!(bobs <= 2);
    // A fork kept within the bounds ends its log's prefix for good, though
    // both its offers were dropped; one past them is forgotten once its
    // offers are dropped.
    assert!(!held(&s, "offers/rot-a-1"));
    assert_eq!(prefix(&s, "bob", "offers/rot-a").fork_at, 1);
    assert!(!held(&s, "offers/rot-c-2"));
    assert_eq!(prefix(&s, "bob", "offers/rot-c").fork_at, 0);
}

// R2-3: answered never runs inside receive's transaction. A journal reply
// appended after answered said no, and before the transaction, moves the
// mark: the drop is planned again, keeps the answered offer, and the append
// does not wait on receive's write lock.
#[test]
fn the_drop_is_retried_when_the_journal_changes_under_it() {
    let (f, s) = Fx::new(
        ReceiveBounds {
            max_offers_per_sender: 1,
            ..Default::default()
        },
        Duration::ZERO,
    );
    let values: Values = Arc::default();
    let c = stamp_cadence(&s, &values);
    s.open("deal/ours")
        .unwrap()
        .append(&observation("a", "1"))
        .unwrap();
    c.tick(tick_time()).unwrap();
    c.migrate(minutes(1)).unwrap();
    let journal = s.journal().unwrap();

    must_record(&s, &offer("bob", "o1", 0));
    let asked = Arc::new(Mutex::new(0));
    let append_err: Arc<Mutex<Option<String>>> = Arc::default();
    let (profile, counter, failed) = (f.profile.clone(), asked.clone(), append_err.clone());
    *f.profile.after_answered.lock().unwrap() = Some(Arc::new(move |r: &ReceivedRecord| {
        let mut n = counter.lock().unwrap();
        *n += 1;
        if *n == 1 {
            // Our reply to o1, committed after answered said no.
            if let Err(e) = journal.append(&observation("reply", &r.digest)) {
                *failed.lock().unwrap() = Some(e.to_string());
            }
            profile.st().answered.insert(r.digest.clone());
        }
    }));
    let shelf = s.clone();
    within_time(move || {
        must_record(&shelf, &offer("bob", "o2", 0));
    });
    assert_eq!(
        *append_err.lock().unwrap(),
        None,
        "the journal append does not wait on receive"
    );
    assert_eq!(*asked.lock().unwrap(), 2, "the drop was planned again");
    assert!(held(&s, "o1"), "the answered offer is kept");
}

// R2-4: a proof part whose transaction does not commit adds no changes; the
// committed record part's changes stay.
#[test]
fn a_failed_proof_commit_reports_no_proof_changes() {
    let h = holder(&["1", "2"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    let records = h.records("alice", "deal-1");
    for r in &records[..records.len() - 1] {
        must_record(&s, r);
    }
    BEFORE_COMMIT.with(|hook| *hook.borrow_mut() = Some(Box::new(|part| part == "proof")));
    let got = s.receive(records.last().map(Vec::as_slice), Some(&p));
    BEFORE_COMMIT.with(|hook| *hook.borrow_mut() = None);
    let e = got.unwrap_err();
    assert!(e.error.is_some());
    assert_eq!(
        e.result.changes.grew,
        strs(&["deal-1"]),
        "the record part committed"
    );
    assert!(
        e.result.changes.covered.is_empty(),
        "the proof part did not"
    );
    assert_eq!(proof_rows(&s), 0);
}

// Round 4: a position past SQLite's signed 64-bit range is refused as
// invalid before anything is stored; i64::MAX is a position like another.
#[test]
fn a_position_past_i64_max_is_refused() {
    let (_f, s) = fx();
    let at = |pos: u64, name: &str| {
        rec(Rec {
            digest: dg(name),
            sender: "alice",
            log: "deal/far",
            pos,
            prev: dg("before"),
            deal: "deal-far",
            ..Default::default()
        })
    };
    let got = must_record(&s, &at(i64::MAX as u64, "max")).record.unwrap();
    assert!(got.stored && !got.counted && got.ask_for_book);
    assert_eq!(
        s.received_record_of(&dg("max")).unwrap().received.position,
        i64::MAX as u64
    );
    for (pos, name) in [(1u64 << 63, "past"), (u64::MAX, "last")] {
        let e = s.receive(Some(&at(pos, name)), None).unwrap_err();
        assert!(
            matches!(&e.result.record.as_ref().unwrap().refused, Some(ShelfError::Invalid(m)) if m.contains("2^63 - 1")),
            "{pos}: {e}"
        );
        assert!(!held(&s, name), "{pos}: nothing is stored");
    }
    assert_eq!(received_count(&s), 1);
}

// Round 4: an inclusion proof's members read null as Go reads it, the zero
// value: leaf_index 0, absent and null verify alike.
#[test]
fn an_inclusion_member_reads_null_as_zero() {
    let h = holder(&["1"]);
    let outcome = |leaf_index: Option<Value>| {
        let mut p = h.proof("counterparty");
        let proof = p.inclusion.proof.as_object_mut().unwrap();
        match leaf_index {
            Some(v) => proof.insert("leaf_index".into(), v),
            None => proof.remove("leaf_index"),
        };
        // Through the JSON encoding, as a reader in another language gets it.
        let p: StampProof = serde_json::from_slice(&serde_json::to_vec(&p).unwrap()).unwrap();
        verify_stamp_proof(&p)
            .map(|_| ())
            .map_err(|e| e.to_string())
    };
    let zero = outcome(Some(json!(0)));
    assert_eq!(outcome(None), zero, "absent");
    assert_eq!(outcome(Some(Value::Null)), zero, "null");
    // A real fact is never the shelf log's first entry, so leaf 0 does not
    // place it; the refusal is the inclusion's, not the decoding's.
    let e = zero.unwrap_err();
    assert!(e.contains("is not included"), "{e}");
    // seq reads null as 0 as well: no entry the checkpoint covers.
    let mut p = serde_json::to_value(h.proof("counterparty")).unwrap();
    p["inclusion"]["seq"] = Value::Null;
    let p: StampProof = serde_json::from_value(p).unwrap();
    let e = verify_stamp_proof(&p).unwrap_err().to_string();
    assert!(e.contains("covers"), "{e}");
}

// Round 4: the holder's books keep changing while room is made for an
// offer: three plans, then a busy refusal; nothing dropped, nothing stored.
#[test]
fn receive_refuses_busy_when_the_mark_keeps_moving() {
    let (f, s) = Fx::new(
        ReceiveBounds {
            max_offers_per_sender: 1,
            ..Default::default()
        },
        Duration::ZERO,
    );
    let values: Values = Arc::default();
    let c = stamp_cadence(&s, &values);
    s.open("deal/ours")
        .unwrap()
        .append(&observation("a", "1"))
        .unwrap();
    c.tick(tick_time()).unwrap();
    c.migrate(minutes(1)).unwrap();
    let journal = s.journal().unwrap();

    must_record(&s, &offer("bob", "o1", 0));
    let asked = Arc::new(Mutex::new(0));
    let counter = asked.clone();
    *f.profile.after_answered.lock().unwrap() = Some(Arc::new(move |_: &ReceivedRecord| {
        let mut n = counter.lock().unwrap();
        *n += 1;
        journal
            .append(&observation("busy", &n.to_string()))
            .unwrap();
    }));
    let shelf = s.clone();
    let e = within_time(move || {
        shelf
            .receive(Some(&offer("bob", "o2", 0)), None)
            .unwrap_err()
    });
    assert!(fails(&e, |e| matches!(e, ShelfError::Busy(_))));
    assert!(matches!(
        e.result.record.as_ref().unwrap().refused,
        Some(ShelfError::Busy(_))
    ));
    assert_eq!(*asked.lock().unwrap(), 3, "three plans, then the refusal");
    assert!(held(&s, "o1"), "nothing was dropped");
    assert!(!held(&s, "o2"), "nothing was stored");
}

/// held_first: every record at position 1 of a log id, across senders and
/// both records of a genesis fork, by sender then digest (Go
/// `TestHeldFirstReturnsEveryRecordAtPositionOneAcrossSenders`).
#[test]
fn held_first_returns_every_record_at_position_one_across_senders() {
    let (f, s) = fx();
    f.profile.st().admitted.insert("bob".into(), true);
    assert!(s.held_first("deal/a").unwrap().is_empty());
    must_record(&s, &chain("bob", "deal/a", "deal-2", 1, "b1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    must_record(&s, &chain("alice", "deal/b", "deal-3", 1, "c1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1-other", ""));
    let got = s.held_first("deal/a").unwrap();
    let keys: Vec<(String, String)> = got
        .iter()
        .map(|h| {
            assert_eq!(h.received.log_id, "deal/a");
            assert_eq!(h.received.position, 1);
            assert!(!h.record.is_empty());
            (h.received.sender_key.clone(), h.received.digest.clone())
        })
        .collect();
    let mut want = vec![
        ("alice".to_string(), dg("a1")),
        ("alice".to_string(), dg("a1-other")),
        ("bob".to_string(), dg("b1")),
    ];
    if dg("a1-other") < dg("a1") {
        want.swap(0, 1);
    }
    assert_eq!(keys, want);
}

/// held_by_deal: every record held for a deal across senders and logs,
/// forks included (Go `TestHeldByDealReturnsEveryRecordOfTheDeal`).
#[test]
fn held_by_deal_returns_every_record_of_the_deal() {
    let (f, s) = fx();
    f.profile.st().admitted.insert("bob".into(), true);
    assert!(s.held_by_deal("deal-1").unwrap().is_empty());
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 2, "a2", "a1"));
    must_record(&s, &chain("bob", "deal/c", "deal-1", 1, "c1", ""));
    must_record(&s, &chain("alice", "deal/b", "deal-3", 1, "b1", ""));
    must_record(&s, &chain("alice", "deal/a", "deal-1", 1, "a1-other", ""));
    let keys: Vec<String> = s
        .held_by_deal("deal-1")
        .unwrap()
        .iter()
        .map(|h| {
            assert_eq!(h.received.deal_id, "deal-1");
            format!(
                "{} {} {}",
                h.received.sender_key, h.received.log_id, h.received.position
            )
        })
        .collect();
    assert_eq!(
        keys,
        [
            "alice deal/a 1",
            "alice deal/a 1",
            "alice deal/a 2",
            "bob deal/c 1"
        ]
    );
}

/// held_proofs: a proof recomputed over the sender's records covers them,
/// and one from another sender or log is none.
#[test]
fn held_proofs_cover_the_counted_records() {
    let h = holder(&["1", "2", "3"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    assert!(s.held_proofs("alice", "deal/a").unwrap().is_empty());
    for r in h.records("alice", "deal-1") {
        must_record(&s, &r);
    }
    must(&s, None, Some(&p));
    let held = s.held_proofs("alice", "deal/a").unwrap();
    assert_eq!(held.len(), 1);
    assert!(held[0].recomputed && !held[0].contradicted);
    let counted = prefix(&s, "alice", "deal/a").size;
    assert!(counted > 0);
    assert_eq!(
        held[0].covers,
        held[0].verified.book_checkpoint.entries.min(counted)
    );
    assert_eq!(held[0].proof, p);
    assert!(s.held_proofs("bob", "deal/a").unwrap().is_empty());
    assert!(s.held_proofs("alice", "deal/b").unwrap().is_empty());
    s.release().unwrap();
    assert!(matches!(
        s.held_proofs("alice", "deal/a"),
        Err(ShelfError::Released)
    ));
}

/// genesis-fork-after-proof (docs/design/461-deal-profile.md §7.3; Go
/// `TestHeldProofsCoverNothingAfterAGenesisFork`): a proof past the records
/// held is unchecked and covers nothing; recomputed, it covers them; a fork
/// at position 1 empties the counted prefix, so it stays recomputed and
/// covers nothing, before and after a restart, with its bindings.
#[test]
fn held_proofs_cover_nothing_after_a_genesis_fork() {
    let h = holder(&["1", "2", "3"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    must(&s, None, Some(&p));
    let got = s.held_proofs("alice", "deal/a").unwrap();
    assert_eq!(got.len(), 1);
    assert!(!got[0].recomputed && !got[0].contradicted);
    assert_eq!(got[0].covers, 0);

    let records = h.records("alice", "deal-1");
    for r in &records {
        must_record(&s, r);
    }
    let got = s.held_proofs("alice", "deal/a").unwrap();
    let held = &got[0];
    assert!(held.recomputed);
    assert_eq!(held.covers, records.len() as u64);
    assert_eq!(held.verified.book_checkpoint.entries, held.covers);
    assert_eq!(held.verified.fact.book_id, h.book_id);
    assert_eq!(held.verified.key_id, key_of(&p));
    assert_eq!(held.verified.shelf_checkpoint.log_id, h.cadence.log_id());
    assert_eq!(held.proof.fact, p.fact);

    must_record(
        &s,
        &chain("alice", "deal/a", "deal-1", 1, "other-genesis", ""),
    );
    let check = |s: &Shelf| {
        let got = s.held_proofs("alice", "deal/a").unwrap();
        assert_eq!(got.len(), 1);
        assert!(got[0].recomputed, "the proof stays recomputed");
        assert_eq!(
            got[0].covers, 0,
            "but covers nothing once the prefix is empty"
        );
        assert_eq!(got[0].verified.fact.book_id, h.book_id);
        assert_eq!(got[0].verified.shelf_checkpoint.log_id, h.cadence.log_id());
    };
    check(&s);
    s.release().unwrap();
    check(&f.open());
}

/// A proof the records held do not recompute is contradicted and covers
/// nothing (Go `TestHeldProofsReportAContradictedProof`).
#[test]
fn held_proofs_report_a_contradicted_proof() {
    let h = holder(&["1", "2"]);
    let (f, s) = fx();
    let p = h.proof("counterparty");
    f.profile.st().proof_keys.insert(key_of(&p), "alice".into());
    must(&s, None, Some(&p));
    // As many records as the proof's checkpoint holds, which do not
    // recompute its root.
    let mut prev = String::new();
    for i in 0..h.records("alice", "deal-1").len() {
        let name = format!("x{}", i + 1);
        let _ = s.receive(
            Some(&chain(
                "alice",
                "deal/a",
                "deal-1",
                i as u64 + 1,
                &name,
                &prev,
            )),
            None,
        );
        prev = name;
    }
    let got = s.held_proofs("alice", "deal/a").unwrap();
    assert_eq!(got.len(), 1);
    assert!(got[0].contradicted && !got[0].recomputed);
    assert_eq!(got[0].covers, 0);
}
