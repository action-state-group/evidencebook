// SPDX-License-Identifier: Apache-2.0
//! The whole-shelf handover and its replay (evidencebook#461 §2.7), Go's
//! `shelf_handover_test.go` case for case on the scripted shelf after its
//! migrate phase (two books grown after migration, a witnessed stamp, every
//! genesis fact tied to the old witnessed tree), and
//! the vectors `testdata/shelf/handover.json` and `handover-result.json`.

use evidencebook::shelf::{verify_handover, Handover, HandoverResult, ShelfError};
use evidencebook_interop::shelf;
use serde_json::{json, Value};

fn vector(name: &str) -> String {
    let path = format!("{}/../../testdata/shelf/{name}", env!("CARGO_MANIFEST_DIR"));
    std::fs::read_to_string(path).unwrap()
}

/// The scripted shelf after its migrate phase, handed over at its newest
/// stamp.
fn handed_over() -> Handover {
    let dir = tempfile::tempdir().unwrap();
    shelf::write(dir.path()).unwrap();
    shelf::continue_(dir.path()).unwrap();
    shelf::migrate(dir.path()).unwrap();
    let env = shelf::hold(dir.path(), None).unwrap();
    let h = env.cadence.handover(0);
    env.release().unwrap();
    h.unwrap()
}

fn verify(h: &Handover) -> Result<HandoverResult, ShelfError> {
    verify_handover(
        h,
        &shelf::checkpoint_key_id(),
        Some(&shelf::witness_directory()),
    )
}

/// The index in `h.documents` of the `nth` (from 0) book fact of `log`.
fn fact_at(h: &Handover, log: &str, nth: usize) -> usize {
    h.documents
        .iter()
        .enumerate()
        .filter(|(_, d)| {
            let f: Value = serde_json::from_slice(&d.doc).unwrap();
            f["kind"] == "book_fact" && f["log_id"] == log
        })
        .nth(nth)
        .unwrap_or_else(|| panic!("no fact {nth} of {log}"))
        .0
}

#[test]
fn a_handover_verifies_and_returns_the_latest_facts() {
    let h = handed_over();
    let got = verify(&h).unwrap();
    assert!(got.recomputed);
    // The scripted receipts carry no registration time, which alone gives
    // the witnessed level (§2.7); the record time phase witnesses a stamp
    // with one (`shelf_vectors.rs`, record-time.json's last handover).
    assert!(!got.witnessed, "a receipt with no registration time");
    assert_eq!(h.receipts.len(), 1, "the anchor's receipt is handed over");
    assert_ne!(got.coverage_start, 0);
    let logs: Vec<&str> = got.latest.iter().map(|l| l.fact.log_id.as_str()).collect();
    let mut sorted = logs.clone();
    sorted.sort();
    assert_eq!(sorted, ["deal/a", "deal/b", "deal/c", "legacy/old"]);
    // Every book's log was in a witnessed tree tick before the migration,
    // so every genesis fact carries its tie (evidencebook#461 §2.7).
    assert_eq!(h.ties.len(), 4, "a tie per book");
    assert!(got.untied.is_empty(), "every genesis fact is tied");
    let a = got
        .latest
        .iter()
        .find(|l| l.fact.log_id == "deal/a")
        .unwrap();
    assert!(
        a.fact.size > a.fact.prev_fact_size,
        "deal/a grew after migration"
    );
    let b = got
        .latest
        .iter()
        .find(|l| l.fact.log_id == "deal/b")
        .unwrap();
    assert_eq!(b.fact.prev_fact_size, 0, "deal/b has only its genesis fact");
    assert_eq!(b.status_opening.value, json!({}));

    // Round trip: the handover is JSON a reader in another language reads.
    let back: Handover = serde_json::from_slice(&serde_json::to_vec(&h).unwrap()).unwrap();
    assert_eq!(back, h);
    assert_eq!(verify(&back).unwrap(), got);
}

#[test]
fn a_shelf_with_no_witness_is_recomputed_and_unwitnessed() {
    let mut h = handed_over();
    h.receipts.clear();
    let got = verify(&h).unwrap();
    assert!(got.recomputed);
    assert!(!got.witnessed);
}

// Each planted failure of §2.7 fails.
#[test]
fn a_handover_refuses_each_planted_failure() {
    let base = handed_over();
    type Plant = fn(&mut Handover);
    let plants: [(&str, Plant); 8] = [
        ("an entry held back", |h| {
            h.entries.pop();
        }),
        ("an entry changed", |h| h.entries[0] = h.entries[1].clone()),
        ("a fact left out", |h| {
            let i = fact_at(h, "deal/a", 1);
            h.documents.remove(i);
        }),
        ("a changed opening", |h| {
            let i = fact_at(h, "deal/a", 1);
            h.documents[i].status_opening.as_mut().unwrap().value = json!({"rows": [1]});
        }),
        ("a missing opening", |h| {
            let i = fact_at(h, "deal/a", 0);
            h.documents[i].status_opening = None;
        }),
        ("a replaced book (no proof)", |h| h.consistency.clear()),
        ("a book checkpoint left out", |h| {
            h.book_checkpoints.pop();
        }),
        ("another holder's anchor", |h| {
            h.log_id = "deal-cadence/ffffffffffffffff".into()
        }),
    ];
    for (name, plant) in plants {
        let mut h = base.clone();
        plant(&mut h);
        assert!(
            matches!(verify(&h), Err(ShelfError::Invalid(_))),
            "{name}: {:?}",
            verify(&h)
        );
    }
    let another_key = verify_handover(&base, "not-the-key", Some(&shelf::witness_directory()));
    assert!(
        matches!(another_key, Err(ShelfError::Invalid(_))),
        "another key"
    );
}

#[test]
fn a_handover_needs_a_finished_stamp() {
    let dir = tempfile::tempdir().unwrap();
    shelf::write(dir.path()).unwrap();
    let env = shelf::hold(dir.path(), None).unwrap();
    let got = env.cadence.handover(0);
    env.release().unwrap();
    assert!(
        matches!(got, Err(ShelfError::Invalid(_))),
        "a tree tick is not a stamp: {got:?}"
    );
}

#[test]
fn the_handover_and_its_replay_are_the_vectors() {
    let dir = tempfile::tempdir().unwrap();
    shelf::write(dir.path()).unwrap();
    shelf::continue_(dir.path()).unwrap();
    shelf::migrate(dir.path()).unwrap();
    assert!(
        shelf::handover(dir.path()).unwrap() == vector("handover.json"),
        "handover.json differs"
    );
    assert!(
        shelf::verify_handover(vector("handover.json").as_bytes()).unwrap()
            == vector("handover-result.json"),
        "handover-result.json differs"
    );
}

/// earliest-receipt-not-anchor (invariant C, `docs/design/461-deal-profile.md`
/// §8.1): a reader of the vector's handover gets every query's time, the
/// holder's `record_time`, and without the witnessed checkpoints before the
/// anchor some time would differ.
#[test]
fn the_earliest_receipt_vector_replays() {
    use evidencebook::shelf::verify_handover_times;
    let v: Value =
        serde_json::from_str(&vector("handover/earliest-receipt-not-anchor.json")).unwrap();
    let h: Handover = serde_json::from_value(v["handover"].clone()).unwrap();
    let key = v["pinned_key"].as_str().unwrap();
    assert_eq!(key, shelf::checkpoint_key_id());
    let directory = shelf::witness_directory();
    let times = verify_handover_times(&h, key, Some(&directory)).unwrap();
    let mut anchor_only = h.clone();
    anchor_only.witnessed.clear();
    let without = verify_handover_times(&anchor_only, key, Some(&directory)).unwrap();
    let mut differs = false;
    for q in v["queries"].as_array().unwrap() {
        let book = q["book_id"].as_str().unwrap();
        let position = q["position"].as_u64().unwrap();
        let witnesses: Vec<String> = q["witnesses"]
            .as_array()
            .unwrap()
            .iter()
            .map(|w| w.as_str().unwrap().to_string())
            .collect();
        let got = times.record_time(book, position, &witnesses).unwrap();
        let want = q["time"]
            .as_str()
            .map(|t| evidencebook::shelf::Time::parse(t).unwrap());
        assert_eq!(got, want, "{} at {position}", q["log_id"]);
        differs |= without.record_time(book, position, &witnesses).unwrap() != got;
    }
    assert!(
        differs,
        "some record's time comes from a checkpoint before the anchor"
    );
    assert!(matches!(
        times.record_time("any", 0, &[]),
        Err(ShelfError::Invalid(_))
    ));
}

/// A witnessed checkpoint that is the anchor, given twice, carries no
/// receipt, is not a checkpoint, or has a root the entries do not recompute
/// (the vector's `forked`) is refused (Go
/// `TestAHandoverRefusesABadWitnessedCheckpoint`).
#[test]
fn a_bad_witnessed_checkpoint_is_refused() {
    let v: Value =
        serde_json::from_str(&vector("handover/earliest-receipt-not-anchor.json")).unwrap();
    let h: Handover = serde_json::from_value(v["handover"].clone()).unwrap();
    assert!(verify(&h).is_ok());
    assert!(h.witnessed.len() >= 2);
    let forked: evidencebook::shelf::HandoverCheckpoint =
        serde_json::from_value(v["forked"].clone()).unwrap();
    type Edit = Box<dyn Fn(&mut Handover)>;
    let edits: Vec<(&str, Edit)> = vec![
        (
            "the anchor again",
            Box::new(|h| h.witnessed[0].checkpoint = h.anchor.clone()),
        ),
        (
            "one checkpoint twice",
            Box::new(|h| h.witnessed[1] = h.witnessed[0].clone()),
        ),
        ("no receipt", Box::new(|h| h.witnessed[0].receipts.clear())),
        (
            "not a checkpoint",
            Box::new(|h| h.witnessed[0].checkpoint = b"x".to_vec()),
        ),
        (
            "another root at a size within the anchor",
            Box::new(move |h| h.witnessed[0] = forked.clone()),
        ),
        (
            "the holder's key, another log",
            Box::new(|h| h.witnessed[0].checkpoint = h.book_checkpoints[0].clone()),
        ),
    ];
    for (name, edit) in edits {
        let mut bad = h.clone();
        edit(&mut bad);
        assert!(
            matches!(verify(&bad), Err(ShelfError::Invalid(_))),
            "{name}"
        );
    }
}
