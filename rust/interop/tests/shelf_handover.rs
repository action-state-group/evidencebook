// SPDX-License-Identifier: Apache-2.0
//! The whole-shelf handover and its replay (evidencebook#461 §2.7), Go's
//! `shelf_handover_test.go` case for case on the scripted shelf after its
//! migrate phase (two books grown after migration, a witnessed stamp), and
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
    assert_eq!(got.untied.len(), 4, "no genesis fact carries a tie yet");
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
