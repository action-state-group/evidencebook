//! Go's `shelf_handover_tie_test.go`, case for case: a genesis fact's tie
//! to the old witnessed tree (evidencebook#461 §2.7).

use super::cadence::Cadence;
use super::handover_tie::HandoverTie;
use super::log::Checkpoint;
use super::record_time_tests::{
    append, book_id_of, config, directory, minutes, open, tick_time, witness_at, TEST_WITNESS_URL,
};
use super::{verify_handover, Handover, Shelf, ShelfError};
use serde_json::json;
use std::collections::HashMap;

/// A handed-over shelf, the holder's key, and each log's book id.
struct Tied {
    _dir: tempfile::TempDir,
    _shelf: Shelf,
    _cadence: Cadence,
    h: Handover,
    key: String,
    books: HashMap<&'static str, String>,
}

/// A shelf whose deal/a grew after its witnessed tree tick (a tie with a
/// consistency proof), whose deal/b did not (a tie of one size), and whose
/// deal/c was never in a witnessed tick (untied), migrated, stamped and
/// handed over (Go `tiedHandover`).
fn tied_handover() -> Tied {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let c = shelf.cadence(config(directory())).unwrap();
    append(&shelf, "deal/a", "1");
    append(&shelf, "deal/b", "1");
    let tree = c.tick(tick_time()).unwrap().tick.unwrap();
    witness_at(&c, &tree, TEST_WITNESS_URL, None);
    append(&shelf, "deal/a", "2");
    append(&shelf, "deal/c", "1");
    c.migrate(minutes(1)).unwrap();
    let last = c.stamp(minutes(2)).unwrap().tick.unwrap();
    witness_at(&c, &last, TEST_WITNESS_URL, Some(minutes(3)));
    let h = c.handover(0).unwrap();
    let key = Checkpoint::from_statement(&h.anchor).unwrap().key_id;
    let books = ["deal/a", "deal/b", "deal/c"]
        .into_iter()
        .map(|log| (log, book_id_of(&shelf, log)))
        .collect();
    Tied {
        _dir: dir,
        _shelf: shelf,
        _cadence: c,
        h,
        key,
        books,
    }
}

// A genesis fact handed over with its tie verifies and is not untied; one
// whose log no witnessed tick held is untied.
#[test]
fn a_genesis_fact_with_its_tie_is_tied() {
    let Tied {
        mut h, key, books, ..
    } = tied_handover();
    assert_eq!(h.ties.len(), 2);
    let by_log: HashMap<&str, &HandoverTie> =
        h.ties.iter().map(|t| (t.log_id.as_str(), t)).collect();
    assert!(
        by_log["deal/a"].consistency.is_some(),
        "deal/a grew after its witnessed tick"
    );
    assert!(by_log["deal/b"].consistency.is_none(), "deal/b did not");
    let mut ids: Vec<&str> = h.ties.iter().map(|t| t.book_id.as_str()).collect();
    ids.sort();
    assert_eq!(
        ids,
        h.ties
            .iter()
            .map(|t| t.book_id.as_str())
            .collect::<Vec<_>>(),
        "ties are in book id order"
    );
    let got = verify_handover(&h, &key, Some(&directory())).unwrap();
    assert_eq!(got.untied, [books["deal/c"].clone()]);

    // The ties round-trip through JSON, `ties` after `receipts` and the
    // consistency proof omitted when absent, as Go writes them.
    let text = serde_json::to_string(&h).unwrap();
    assert!(text.find("\"receipts\"").unwrap() < text.find("\"ties\"").unwrap());
    let back: Handover = serde_json::from_str(&text).unwrap();
    assert_eq!(back, h);
    let b = serde_json::to_value(by_log["deal/b"]).unwrap();
    assert!(b.get("consistency").is_none());

    // Without its ties every genesis fact is untied, and still recomputed.
    h.ties.clear();
    let got = verify_handover(&h, &key, Some(&directory())).unwrap();
    assert_eq!(got.untied.len(), 3);
    // Go writes no ties as `[]` and reads `null` as none.
    let mut value = serde_json::to_value(&h).unwrap();
    assert_eq!(value["ties"], json!([]));
    value["ties"] = json!(null);
    assert_eq!(serde_json::from_value::<Handover>(value).unwrap(), h);
}

// Each change to a tie fails the handover, naming the tie.
#[test]
fn a_tie_that_does_not_verify_fails_the_handover() {
    let Tied { h, key, books, .. } = tied_handover();
    let index = |log: &str| h.ties.iter().position(|t| t.log_id == log).unwrap();
    let (a, b) = (index("deal/a"), index("deal/b"));
    fn flip(s: &str) -> String {
        let first = if s.starts_with('0') { "1" } else { "0" };
        format!("{first}{}", &s[1..])
    }
    let (book_b, anchor) = (books["deal/b"].clone(), h.anchor.clone());
    type Plant = Box<dyn Fn(&mut Handover)>;
    let plants: Vec<(&str, Plant)> = vec![
        (
            "salt",
            Box::new(move |h| h.ties[a].salt = flip(&h.ties[a].salt)),
        ),
        ("index", Box::new(move |h| h.ties[a].index ^= 1)),
        (
            "path",
            Box::new(move |h| h.ties[a].path[3] = flip(&h.ties[a].path[3])),
        ),
        (
            "short path",
            Box::new(move |h| {
                h.ties[a].path.remove(0);
            }),
        ),
        ("entry", Box::new(move |h| h.ties[a].entry_seq += 1)),
        ("size", Box::new(move |h| h.ties[b].size += 1)),
        (
            "old checkpoint",
            Box::new(move |h| h.ties[a].old_checkpoint = h.ties[b].old_checkpoint.clone()),
        ),
        (
            "no consistency",
            Box::new(move |h| h.ties[a].consistency = None),
        ),
        (
            "consistency on a size",
            Box::new(move |h| h.ties[b].consistency = h.ties[a].consistency.clone()),
        ),
        (
            "cadence checkpoint",
            Box::new(move |h| h.ties[a].cadence_checkpoint = anchor.clone()),
        ),
        ("no receipt", Box::new(move |h| h.ties[a].receipts.clear())),
        (
            "another book",
            Box::new(move |h| h.ties[a].book_id = book_b.clone()),
        ),
        (
            "no genesis",
            Box::new(move |h| h.ties[a].book_id = "no-such-book".into()),
        ),
        // Not in Go's table; VerifyHandover refuses it before any tie is
        // checked.
        (
            "two ties of one book",
            Box::new(move |h| h.ties.push(h.ties[a].clone())),
        ),
    ];
    for (name, plant) in plants {
        let mut changed = h.clone();
        plant(&mut changed);
        match verify_handover(&changed, &key, Some(&directory())) {
            Err(ShelfError::Invalid(m)) => assert!(m.contains("tie"), "{name}: {m}"),
            other => panic!("{name}: {other:?}"),
        }
    }
    // A reader whose directory names no witness verifies no tie.
    let got = verify_handover(&h, &key, Some(&json!({"witnesses": []})));
    assert!(matches!(got, Err(ShelfError::Invalid(_))), "{got:?}");
}

// A cadence checkpoint the holder signed and a named witness witnessed, but
// of another shelf history (another root at the tie's position), fails the
// tie on its root, not on its receipts.
#[test]
fn a_tie_under_another_historys_checkpoint_fails() {
    let Tied { mut h, key, .. } = tied_handover();
    // Another shelf under the same checkpoint key and cadence log id, whose
    // first tick is witnessed.
    let dir = tempfile::tempdir().unwrap();
    let other = open(&dir.path().join("shelf.db"));
    let c = other.cadence(config(directory())).unwrap();
    append(&other, "deal/z", "1");
    let forged = c.tick(tick_time()).unwrap().tick.unwrap();
    let cp = Checkpoint::from_statement(&forged.checkpoint).unwrap();
    assert!(cp.entries >= h.ties[0].entry_seq);
    witness_at(&c, &forged, TEST_WITNESS_URL, None);
    let receipt = c.get(forged.n).unwrap().receipt.unwrap();
    h.ties[0].cadence_checkpoint = forged.checkpoint.clone();
    h.ties[0].receipts = vec![receipt];
    match verify_handover(&h, &key, Some(&directory())) {
        Err(ShelfError::Invalid(m)) => {
            assert!(m.contains("do not recompute the cadence checkpoint"), "{m}")
        }
        other => panic!("{other:?}"),
    }
}

// A witnessed tree tick whose leaf kept no checkpoint statement (as a
// released capsule-cli build stored it) gives that book no tie: it is
// untied, and the handover verifies.
#[test]
fn a_leaf_with_no_statement_leaves_its_book_untied() {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let c = shelf.cadence(config(directory())).unwrap();
    append(&shelf, "deal/a", "1");
    append(&shelf, "deal/b", "1");
    let tree = c.tick(tick_time()).unwrap().tick.unwrap();
    witness_at(&c, &tree, TEST_WITNESS_URL, None);
    shelf
        .shared()
        .db()
        .execute(
            r#"UPDATE cadence_ticks SET leaves = json_remove(leaves, '$.deals."deal/b".statement') WHERE tick = ?"#,
            [tree.n as i64],
        )
        .unwrap();
    c.migrate(minutes(1)).unwrap();
    c.stamp(minutes(2)).unwrap();
    let h = c.handover(0).unwrap();
    assert_eq!(h.ties.len(), 1);
    assert_eq!(h.ties[0].log_id, "deal/a");
    let key = Checkpoint::from_statement(&h.anchor).unwrap().key_id;
    let got = verify_handover(&h, &key, Some(&directory())).unwrap();
    assert_eq!(got.untied, [book_id_of(&shelf, "deal/b")]);
}
