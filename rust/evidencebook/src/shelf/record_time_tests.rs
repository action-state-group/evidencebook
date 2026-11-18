//! Go's `shelf_record_time_test.go`, case for case, and the backfill after
//! an import (Go `ImportLegacy`).

use super::cadence::{Cadence, CadenceConfig, CadenceTick, WitnessReceipt, WITNESS_VERIFIED};
use super::log::Checkpoint;
use super::{
    verify_handover, BookEntry, LegacyCadence, SeededStream, Shelf, ShelfConfig, ShelfError, Time,
    JOURNAL_LOG_ID,
};
use crate::record::EpistemicType;
use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ciborium::Value as Cbor;
use ed25519_dalek::{Signer as _, SigningKey};
use rusqlite::Connection;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

pub(super) const TEST_WITNESS_URL: &str = "https://witness.test";
pub(super) const OTHER_WITNESS_URL: &str = "https://other-witness.test";
const CADENCE_LOG: &str = "deal-cadence/0123456789abcdef";

pub(super) fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

/// The witness key of `url`: the test witness's for [`TEST_WITNESS_URL`],
/// the other witness's for any other URL.
pub(super) fn witness_key(url: &str) -> SigningKey {
    key(if url == TEST_WITNESS_URL {
        "test witness"
    } else {
        "other witness"
    })
}

pub(super) fn tick_time() -> Time {
    Time::parse("2026-10-31T12:00:30Z").unwrap()
}

pub(super) fn minutes(n: u64) -> Time {
    tick_time().plus(Duration::from_secs(60 * n))
}

fn seconds(t: Time) -> i64 {
    (t.unix_nanos() / 1_000_000_000) as i64
}

/// A directory listing both witnesses.
pub(super) fn directory() -> Value {
    let row = |url: &str| {
        json!({"name": url, "endpoint": url,
            "key_ids": [hex::encode(witness_key(url).verifying_key().to_bytes())]})
    };
    json!({"witnesses": [row(TEST_WITNESS_URL), row(OTHER_WITNESS_URL)]})
}

pub(super) fn open(path: &Path) -> Shelf {
    let signer = key("signer");
    let conn = Connection::open(path).unwrap();
    conn.busy_timeout(Duration::from_secs(5)).unwrap();
    let originals = ArtifactSqlite::new(conn, "test", [signer.verifying_key().to_bytes()]).unwrap();
    originals.init().unwrap();
    Shelf::open_sqlite(ShelfConfig {
        path: path.to_path_buf(),
        lock_path: None,
        held_lock: None,
        lock_timeout: Some(Duration::from_millis(200)),
        random: Some(Box::new(SeededStream::new("record time tests"))),
        operator: "test-operator".into(),
        developer: String::new(),
        signer: Arc::new(signer),
        checkpoint_key: key("checkpoint"),
        originals: Arc::new(originals),
        now: Some(Arc::new(tick_time)),
        authenticated_indexes: None,
        correlation_members: Vec::new(),
        receive: Default::default(),
    })
    .unwrap()
}

pub(super) fn config(witnesses: Value) -> CadenceConfig {
    CadenceConfig {
        log_id: CADENCE_LOG.into(),
        interval: Duration::from_secs(300),
        jitter: Duration::ZERO,
        no_jitter: true,
        witness_ids: Vec::new(),
        witnesses: Some(witnesses),
        pad_bucket: 1,
        ended: Arc::new(|_| Ok(false)),
        status: None,
    }
}

pub(super) fn append(shelf: &Shelf, book: &str, payload: &str) {
    let mut entry = BookEntry::new("observation", EpistemicType::ObservedEvent);
    entry.subject_ref = Some(book.into());
    entry.payloads = vec![payload.as_bytes().to_vec()];
    let book = if book == JOURNAL_LOG_ID {
        shelf.journal().unwrap()
    } else {
        shelf.open(book).unwrap()
    };
    book.append(&entry).unwrap();
}

pub(super) fn count(shelf: &Shelf, sql: &str, params: impl rusqlite::Params) -> i64 {
    shelf
        .shared()
        .db()
        .query_row(sql, params, |r| r.get(0))
        .unwrap()
}

/// The number of records a book's log holds.
pub(super) fn leaves_of(shelf: &Shelf, log_id: &str) -> u64 {
    count(
        shelf,
        "SELECT COUNT(*) FROM book_records WHERE book_id = ?",
        [log_id],
    ) as u64
}

pub(super) fn book_id_of(shelf: &Shelf, log_id: &str) -> String {
    shelf
        .shared()
        .db()
        .query_row(
            "SELECT book_id FROM shelf_entries WHERE log_id = ? LIMIT 1",
            [log_id],
            |r| r.get(0),
        )
        .unwrap()
}

/// The witness at `url`'s receipt for the checkpoint `statement` (as Go's
/// `receiptAt`): an RFC 9162 COSE receipt over a one-entry log holding the
/// checkpoint, with the registration time `iat` in its protected CWT claims,
/// none when `iat` is `None`.
fn receipt_at(statement: &[u8], url: &str, iat: Option<Time>) -> WitnessReceipt {
    receipt_under(statement, url, &witness_key(url), iat)
}

/// [`receipt_at`] signed by `signer`, one of the keys the witness at `url`
/// lists (Go `receiptUnder`).
fn receipt_under(
    statement: &[u8],
    url: &str,
    signer: &SigningKey,
    iat: Option<Time>,
) -> WitnessReceipt {
    let verified = cll::checkpoint::verify_checkpoint_cose_offline(statement);
    let decoded = verified.decoded.filter(|_| verified.ok).unwrap();
    let digest = agent_action_capsule::witness::checkpoint_digest(
        &agent_action_capsule::witness::SignedCheckpoint::from(&decoded),
    );
    let entry: [u8; 32] = Sha256::digest(hex::decode(digest).unwrap()).into();
    let encode = |value: &Cbor| {
        let mut out = Vec::new();
        ciborium::into_writer(value, &mut out).unwrap();
        out
    };
    let int = |n: i64| Cbor::Integer(n.into());
    let proof = encode(&Cbor::Array(vec![int(1), int(0), Cbor::Array(Vec::new())]));
    let mut leaf_input = vec![0u8];
    leaf_input.extend_from_slice(&entry);
    let leaf = Sha256::digest(&leaf_input).to_vec();
    let mut headers = vec![(int(1), int(-8))];
    if let Some(iat) = iat {
        headers.push((int(15), Cbor::Map(vec![(int(6), int(seconds(iat)))])));
    }
    headers.push((int(395), int(1)));
    let protected = encode(&Cbor::Map(headers));
    let to_be_signed = encode(&Cbor::Array(vec![
        Cbor::Text("Signature1".into()),
        Cbor::Bytes(protected.clone()),
        Cbor::Bytes(Vec::new()),
        Cbor::Bytes(leaf),
    ]));
    let signature = signer.sign(&to_be_signed).to_bytes().to_vec();
    let receipt = encode(&Cbor::Tag(
        18,
        Box::new(Cbor::Array(vec![
            Cbor::Bytes(protected),
            Cbor::Map(vec![(
                int(396),
                Cbor::Map(vec![(int(-1), Cbor::Array(vec![Cbor::Bytes(proof)]))]),
            )]),
            Cbor::Null,
            Cbor::Bytes(signature),
        ])),
    ));
    WitnessReceipt {
        ts_url: url.into(),
        entry_hash: hex::encode(entry),
        receipt,
        leaf_index: 0,
        tree_size: 1,
    }
}

pub(super) fn witness_at(c: &Cadence, tick: &CadenceTick, url: &str, iat: Option<Time>) {
    let check = c
        .witnessed(tick.n, receipt_at(&tick.checkpoint, url, iat))
        .unwrap();
    assert_eq!(check.state, WITNESS_VERIFIED, "{}", check.reason);
}

pub(super) fn record_time(
    shelf: &Shelf,
    book_id: &str,
    position: u64,
    witnesses: &[&str],
) -> Option<Time> {
    let witnesses: Vec<String> = witnesses.iter().map(|w| w.to_string()).collect();
    shelf.record_time(book_id, position, &witnesses).unwrap()
}

/// A migrated shelf whose book deal/a has its genesis fact in the
/// migration's stamp and a second fact, after another append, in the second
/// of two stamps after it (Go `recordTimeShelf`).
pub(super) struct Fixture {
    pub(super) _dir: tempfile::TempDir,
    pub(super) shelf: Shelf,
    pub(super) cadence: Cadence,
    pub(super) book_id: String,
    pub(super) first: CadenceTick,
    pub(super) second: CadenceTick,
}

pub(super) fn record_time_shelf() -> Fixture {
    let dir = tempfile::tempdir().unwrap();
    let shelf = open(&dir.path().join("shelf.db"));
    let cadence = shelf.cadence(config(directory())).unwrap();
    append(&shelf, "deal/a", "1");
    cadence.tick(tick_time()).unwrap();
    cadence.migrate(minutes(1)).unwrap();
    let first = cadence.stamp(minutes(2)).unwrap().tick.unwrap();
    append(&shelf, "deal/a", "2");
    let second = cadence.stamp(minutes(3)).unwrap().tick.unwrap();
    let book_id = book_id_of(&shelf, "deal/a");
    Fixture {
        _dir: dir,
        shelf,
        cadence,
        book_id,
        first,
        second,
    }
}

// A record's time is the earliest registration time, from a named witness,
// of a shelf checkpoint covering the first fact whose size covers it: a
// later checkpoint covers earlier facts too, and a witness not named gives
// nothing.
#[test]
fn a_record_time_is_the_earliest_named_registration() {
    let f = record_time_shelf();
    let (shelf, c, book_id) = (&f.shelf, &f.cadence, f.book_id.as_str());
    // The first fact of deal/a (the migration's genesis fact), its tick, and
    // the last record it covers: its size's leaves.
    let (size, n): (i64, i64) = shelf
        .shared()
        .db()
        .query_row(
            "SELECT json_extract(doc, '$.size'), tick FROM shelf_entries WHERE log_id = 'deal/a' ORDER BY entry_seq LIMIT 1",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .unwrap();
    let in_first = cll::mmr::leaf_count(size as u64).unwrap();
    let first = c.get(n as u64).unwrap();
    let last = leaves_of(shelf, "deal/a");
    assert!(in_first < last);
    let (t1, t2) = (minutes(10), minutes(20));

    assert_eq!(
        record_time(shelf, book_id, in_first, &[TEST_WITNESS_URL]),
        None,
        "no receipt yet"
    );

    witness_at(c, &f.second, TEST_WITNESS_URL, Some(t2));
    assert_eq!(
        record_time(shelf, book_id, in_first, &[TEST_WITNESS_URL]),
        Some(t2),
        "the second stamp's checkpoint covers the first fact"
    );
    assert_eq!(
        record_time(shelf, book_id, last, &[TEST_WITNESS_URL]),
        Some(t2)
    );

    witness_at(c, &first, OTHER_WITNESS_URL, Some(t1));
    assert_eq!(
        record_time(
            shelf,
            book_id,
            in_first,
            &[TEST_WITNESS_URL, OTHER_WITNESS_URL]
        ),
        Some(t1),
        "the earliest among the named witnesses"
    );
    assert_eq!(
        record_time(shelf, book_id, in_first, &[TEST_WITNESS_URL]),
        Some(t2),
        "a witness not named gives nothing"
    );
    assert_eq!(
        record_time(shelf, book_id, last, &[OTHER_WITNESS_URL]),
        None,
        "the first stamp's checkpoint does not cover the second fact"
    );
    assert_eq!(
        record_time(shelf, book_id, in_first, &["https://unnamed.test"]),
        None
    );
    assert_eq!(
        record_time(shelf, book_id, in_first, &[]),
        None,
        "no witness named"
    );
    assert_eq!(
        record_time(shelf, book_id, last + 1, &[TEST_WITNESS_URL]),
        None,
        "no fact covers the position"
    );
    assert_eq!(
        record_time(shelf, "no-such-book", 1, &[TEST_WITNESS_URL]),
        None
    );

    assert!(matches!(
        shelf.record_time(book_id, 0, &[TEST_WITNESS_URL.to_string()]),
        Err(ShelfError::Invalid(_))
    ));
    // Position 0 is invalid even with no witness named.
    assert!(matches!(
        shelf.record_time(book_id, 0, &[]),
        Err(ShelfError::Invalid(_))
    ));
    // The first stamp is not the tree tick before the migration.
    assert_ne!(f.first.n, first.n);
}

// A receipt with no registration time gives no time; the same witness's
// later receipt with a time does, and of two times the earlier is kept.
#[test]
fn a_record_time_needs_a_registration_time() {
    let f = record_time_shelf();
    let (shelf, c, book_id) = (&f.shelf, &f.cadence, f.book_id.as_str());
    let last = leaves_of(shelf, "deal/a");
    witness_at(c, &f.second, TEST_WITNESS_URL, None);
    assert_eq!(
        record_time(shelf, book_id, last, &[TEST_WITNESS_URL]),
        None,
        "a receipt with no registration time"
    );
    let cp = hex::encode(Sha256::digest(&f.second.checkpoint));
    assert_eq!(
        count(
            shelf,
            "SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND registered_at IS NULL",
            [&cp]
        ),
        1
    );

    let (later, earlier) = (minutes(30), minutes(20));
    witness_at(c, &f.second, TEST_WITNESS_URL, Some(later));
    assert_eq!(
        record_time(shelf, book_id, last, &[TEST_WITNESS_URL]),
        Some(later)
    );
    witness_at(c, &f.second, TEST_WITNESS_URL, Some(earlier));
    witness_at(c, &f.second, TEST_WITNESS_URL, Some(minutes(90)));
    witness_at(c, &f.second, TEST_WITNESS_URL, None);
    assert_eq!(
        record_time(shelf, book_id, last, &[TEST_WITNESS_URL]),
        Some(earlier),
        "the witness's earliest registration is kept"
    );
    assert_eq!(
        count(
            shelf,
            "SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ?",
            [&cp]
        ),
        1,
        "one row per witness"
    );
    // The row kept is the earliest receipt's text.
    let kept: (String, i64) = shelf
        .shared()
        .db()
        .query_row(
            "SELECT receipt, registered_at FROM shelf_receipts WHERE checkpoint_sha256 = ?",
            [&cp],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .unwrap();
    assert_eq!(
        kept,
        (
            receipt_at(&f.second.checkpoint, TEST_WITNESS_URL, Some(earlier)).to_json(),
            seconds(earlier)
        )
    );
}

// The journal's facts carry its size in their status opening, which a
// record time reads.
#[test]
fn a_record_time_covers_the_journal() {
    let f = record_time_shelf();
    let (shelf, c) = (&f.shelf, &f.cadence);
    append(shelf, JOURNAL_LOG_ID, "1");
    let stamp = c.stamp(minutes(4)).unwrap().tick.unwrap();
    let book_id = book_id_of(shelf, JOURNAL_LOG_ID);
    let iat = minutes(60);
    witness_at(c, &stamp, TEST_WITNESS_URL, Some(iat));
    let held = leaves_of(shelf, JOURNAL_LOG_ID);
    assert_eq!(
        record_time(shelf, &book_id, held, &[TEST_WITNESS_URL]),
        Some(iat)
    );
    assert_eq!(
        record_time(shelf, &book_id, held + 1, &[TEST_WITNESS_URL]),
        None
    );
}

// Every verified receipt is kept, a tree tick's included; a witnessed tick
// whose receipt is missing from shelf_receipts (a shelf written before the
// table) gets it back at the cadence's next open, unless it no longer
// verifies under the configured directory.
#[test]
fn receipts_of_witnessed_ticks_are_kept_and_backfilled() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shelf.db");
    let shelf = open(&path);
    let c = shelf.cadence(config(directory())).unwrap();
    append(&shelf, "deal/a", "1");
    let tree = c.tick(tick_time()).unwrap().tick.unwrap();
    witness_at(&c, &tree, TEST_WITNESS_URL, Some(minutes(1)));
    c.migrate(minutes(1)).unwrap();
    let stamp = c.stamp(minutes(2)).unwrap().tick.unwrap();
    witness_at(&c, &stamp, TEST_WITNESS_URL, Some(minutes(3)));
    let with_time = "SELECT COUNT(*) FROM shelf_receipts WHERE registered_at IS NOT NULL";
    assert_eq!(count(&shelf, with_time, []), 2);
    shelf
        .shared()
        .db()
        .execute("DELETE FROM shelf_receipts", [])
        .unwrap();
    shelf.release().unwrap();

    // A directory that no longer lists the witness backfills nothing.
    let shelf = open(&path);
    shelf.cadence(config(json!({"witnesses": []}))).unwrap();
    assert_eq!(count(&shelf, "SELECT COUNT(*) FROM shelf_receipts", []), 0);
    shelf.release().unwrap();

    let shelf = open(&path);
    let c = shelf.cadence(config(directory())).unwrap();
    let test_key = hex::encode(witness_key(TEST_WITNESS_URL).verifying_key().to_bytes());
    assert_eq!(
        count(
            &shelf,
            "SELECT COUNT(*) FROM shelf_receipts WHERE registered_at IS NOT NULL AND witness_id = ? AND key_id = ?",
            [TEST_WITNESS_URL, &test_key]
        ),
        2,
        "each row keeps the directory key it verified under"
    );

    // An import fills in what is missing too, once it has committed.
    shelf
        .shared()
        .db()
        .execute("DELETE FROM shelf_receipts", [])
        .unwrap();
    c.import_legacy(&LegacyCadence::default()).unwrap();
    assert_eq!(count(&shelf, with_time, []), 2);
    shelf.release().unwrap();
}

// A stored receipt that is not JSON is corrupt state: the cadence does not
// open over it.
#[test]
fn a_stored_receipt_that_does_not_decode_is_corrupt() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shelf.db");
    let shelf = open(&path);
    let c = shelf.cadence(config(directory())).unwrap();
    append(&shelf, "deal/a", "1");
    let tree = c.tick(tick_time()).unwrap().tick.unwrap();
    witness_at(&c, &tree, TEST_WITNESS_URL, Some(minutes(1)));
    {
        let db = shelf.shared().db();
        db.execute("DELETE FROM shelf_receipts", []).unwrap();
        db.execute("UPDATE cadence_ticks SET receipt = 'not json'", [])
            .unwrap();
    }
    shelf.release().unwrap();
    let shelf = open(&path);
    assert!(matches!(
        shelf.cadence(config(directory())),
        Err(ShelfError::Corrupt(_))
    ));
    shelf.release().unwrap();
}

// A handover carries every witness's receipt for its anchor, and is
// witnessed only by one with a registration time.
#[test]
fn a_handover_is_witnessed_only_with_a_registration_time() {
    let f = record_time_shelf();
    let c = &f.cadence;
    witness_at(c, &f.second, TEST_WITNESS_URL, None);
    let h = c.handover(0).unwrap();
    assert_eq!(h.receipts.len(), 1);
    let anchor = Checkpoint::from_statement(&h.anchor).unwrap();
    let got = verify_handover(&h, &anchor.key_id, Some(&directory())).unwrap();
    assert!(!got.witnessed, "a receipt with no registration time");

    witness_at(c, &f.second, OTHER_WITNESS_URL, Some(minutes(60)));
    let h = c.handover(0).unwrap();
    assert_eq!(h.receipts.len(), 2);
    assert_eq!(
        h.receipts[0].ts_url, OTHER_WITNESS_URL,
        "in witness URL order"
    );
    // Each witness's row keeps the key its directory row binds to its URL.
    for url in [TEST_WITNESS_URL, OTHER_WITNESS_URL] {
        let key_id: String = f
            .shelf
            .shared()
            .db()
            .query_row(
                "SELECT key_id FROM shelf_receipts WHERE witness_id = ?",
                [url],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            key_id,
            hex::encode(witness_key(url).verifying_key().to_bytes())
        );
    }
    let got = verify_handover(&h, &anchor.key_id, Some(&directory())).unwrap();
    assert!(got.witnessed);
}

#[test]
fn a_record_time_on_a_released_shelf() {
    let f = record_time_shelf();
    f.shelf.release().unwrap();
    assert!(matches!(
        f.shelf
            .record_time(&f.book_id, 1, &[TEST_WITNESS_URL.to_string()]),
        Err(ShelfError::Released)
    ));
}

/// A second key the test witness's directory row lists (a key rotation): one
/// witness, one URL, two keys.
fn rotated_key() -> SigningKey {
    key("test witness, rotated")
}

/// [`directory`] with the test witness listing two keys.
fn two_key_directory() -> Value {
    let hex_of = |k: SigningKey| hex::encode(k.verifying_key().to_bytes());
    json!({"witnesses": [{"name": TEST_WITNESS_URL, "endpoint": TEST_WITNESS_URL,
        "key_ids": [hex_of(witness_key(TEST_WITNESS_URL)), hex_of(rotated_key())]}]})
}

/// A migrated shelf under a cadence whose directory lists two keys for the
/// test witness, with deal/a's book id and the stamp after migration.
fn two_key_shelf(path: &Path) -> (Shelf, Cadence, String, CadenceTick) {
    let shelf = open(path);
    let c = shelf.cadence(config(two_key_directory())).unwrap();
    append(&shelf, "deal/a", "1");
    c.tick(tick_time()).unwrap();
    c.migrate(minutes(1)).unwrap();
    let stamp = c.stamp(minutes(2)).unwrap().tick.unwrap();
    let book_id = book_id_of(&shelf, "deal/a");
    (shelf, c, book_id, stamp)
}

fn witness_under(c: &Cadence, tick: &CadenceTick, signer: &SigningKey, iat: Time) {
    let check = c
        .witnessed(
            tick.n,
            receipt_under(&tick.checkpoint, TEST_WITNESS_URL, signer, Some(iat)),
        )
        .unwrap();
    assert_eq!(check.state, WITNESS_VERIFIED, "{}", check.reason);
}

// evidencebook#492 (Go TestTwoKeysOfOneWitnessKeepTheirReceipts): two keys of
// one witness that register the same checkpoint both keep their receipts, in
// either order; the record time by URL is the earliest, and the handover
// carries both.
#[test]
fn two_keys_of_one_witness_keep_their_receipts() {
    for new_key_first in [false, true] {
        let dir = tempfile::tempdir().unwrap();
        let (shelf, c, book_id, stamp) = two_key_shelf(&dir.path().join("shelf.db"));
        let mut steps = vec![
            (witness_key(TEST_WITNESS_URL), minutes(20)),
            (rotated_key(), minutes(10)),
        ];
        if new_key_first {
            steps.reverse();
        }
        for (signer, at) in &steps {
            witness_under(&c, &stamp, signer, *at);
        }
        assert_eq!(count(&shelf, "SELECT COUNT(*) FROM shelf_receipts", []), 2);
        assert_eq!(
            record_time(&shelf, &book_id, 1, &[TEST_WITNESS_URL]),
            Some(minutes(10)),
            "by URL, the earliest across the witness's keys"
        );
        c.stamp(minutes(3)).unwrap();
        let h = c.handover(0).unwrap();
        assert_eq!(h.witnessed.len(), 1);
        assert_eq!(h.witnessed[0].receipts.len(), 2);
        shelf.release().unwrap();
    }
}

// A shelf written before #492 keyed shelf_receipts by checkpoint and URL
// alone; opening it rebuilds the table and keeps every row. The tick keeps
// the old key's receipt, the old table only the new key's: the backfill at
// the cadence's open restores the old key's beside it (Go
// TestAShelfReceiptsTableKeyedByURLIsMigrated).
#[test]
fn a_shelf_receipts_table_keyed_by_url_is_migrated() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shelf.db");
    let (shelf, c, book_id, stamp) = two_key_shelf(&path);
    witness_under(&c, &stamp, &witness_key(TEST_WITNESS_URL), minutes(20));
    witness_under(&c, &stamp, &rotated_key(), minutes(10));
    let rotated_hex = hex::encode(rotated_key().verifying_key().to_bytes());
    shelf
        .shared()
        .db()
        .execute_batch(&format!(
            "CREATE TABLE old_receipts (checkpoint_sha256 TEXT NOT NULL, witness_id TEXT NOT NULL, key_id TEXT NOT NULL, receipt TEXT NOT NULL, registered_at INTEGER, {});
             INSERT INTO old_receipts SELECT * FROM shelf_receipts WHERE key_id = '{rotated_hex}';
             DROP TABLE shelf_receipts;
             ALTER TABLE old_receipts RENAME TO shelf_receipts;",
            super::store::RECEIPTS_KEYED_BY_URL
        ))
        .unwrap();
    shelf.release().unwrap();

    let shelf = open(&path);
    let ddl: String = shelf
        .shared()
        .db()
        .query_row(
            "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'shelf_receipts'",
            [],
            |r| r.get(0),
        )
        .unwrap();
    assert!(ddl.contains("PRIMARY KEY (checkpoint_sha256, witness_id, key_id)"));
    assert_eq!(count(&shelf, "SELECT COUNT(*) FROM shelf_receipts", []), 1);
    assert_eq!(
        record_time(&shelf, &book_id, 1, &[TEST_WITNESS_URL]),
        Some(minutes(10))
    );
    shelf.cadence(config(two_key_directory())).unwrap();
    let old_hex = hex::encode(witness_key(TEST_WITNESS_URL).verifying_key().to_bytes());
    assert_eq!(
        count(
            &shelf,
            "SELECT COUNT(*) FROM shelf_receipts WHERE key_id = ? AND registered_at = ?",
            rusqlite::params![old_hex, seconds(minutes(20))]
        ),
        1,
        "the old key's receipt is backfilled beside it"
    );
    assert_eq!(count(&shelf, "SELECT COUNT(*) FROM shelf_receipts", []), 2);
    shelf.release().unwrap();

    let shelf = open(&path);
    assert_eq!(count(&shelf, "SELECT COUNT(*) FROM shelf_receipts", []), 2);
    shelf.release().unwrap();
}
