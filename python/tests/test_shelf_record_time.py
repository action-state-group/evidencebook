# SPDX-License-Identifier: Apache-2.0
"""Record time and the shelf's witness receipts (evidencebook#461 §2.3, §2.6,
§2.7), as Go's shelf_record_time_test.go checks them, and
testdata/shelf/record-time.json run after the migrate phase to every step's
expect and to after-record-time.json byte for byte."""
from __future__ import annotations

import hashlib
import json
import sqlite3
from datetime import datetime, timedelta, timezone

import pytest

from evidencebook.cadence import CadenceConfig, WITNESS_VERIFIED
from evidencebook.errors import CorruptError, InvalidError, ReleasedError
from evidencebook.shelf import JOURNAL_LOG_ID
from evidencebook.shelf_handover import verify_handover
from evidencebook.signer import Ed25519Signer

from test_shelf_stamp import START, VECTORS, fx, obs, script  # noqa: F401  (fx is a fixture)

WITNESS_URL = script.CADENCE["witness_url"]
# A second witness, its own test key; never use it for anything else.
OTHER_URL = "https://other-witness.test"
OTHER_KEY = Ed25519Signer(hashlib.sha256(b"evidencebook record time tests/other witness").digest())
DIRECTORY = {"witnesses": [
    {"name": "test witness", "endpoint": WITNESS_URL, "key_ids": [script.KEYS["witness_public"]]},
    {"name": "other witness", "endpoint": OTHER_URL, "key_ids": [OTHER_KEY.public_key().hex()]},
]}
LOG_ID = "deal-cadence/0123456789abcdef"


def at(minutes: int) -> datetime:
    return START + timedelta(minutes=minutes)


def unix(moment: datetime) -> int:
    return int(moment.timestamp())


def cadence(shelf, witnesses=DIRECTORY):
    return shelf.cadence(CadenceConfig(log_id=LOG_ID, ended=lambda _: False, witnesses=witnesses,
                                       status=lambda book_id, log_id: None))


def witness_at(c, tick, url: str = WITNESS_URL, when: datetime | None = None) -> None:
    key = script.WITNESS_KEY if url == WITNESS_URL else OTHER_KEY
    receipt = script.mint_receipt(tick.checkpoint, None if when is None else unix(when), key, url)
    check = c.witnessed(tick.n, receipt)
    assert check.state == WITNESS_VERIFIED, check.reason


def scalar(shelf, query: str, *args):
    with shelf._read() as db:
        return db.execute(query, args).fetchone()[0]


def record_time_shelf(shelf):
    """A migrated shelf whose book deal/a has its genesis fact in the
    migration's stamp and a second fact, after another append, in the second
    of two stamps after it (Go ``recordTimeShelf``)."""
    c = cadence(shelf)
    a = shelf.open("deal/a")
    a.append(obs("a", "1"))
    c.tick(START)
    c.migrate(at(1))
    first = c.stamp(at(2)).tick
    a.append(obs("a", "2"))
    second = c.stamp(at(3)).tick
    book_id = scalar(shelf, "SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' LIMIT 1")
    return c, book_id, first, second


def leaves_of(shelf, log_id: str) -> int:
    return scalar(shelf, "SELECT COUNT(*) FROM book_records WHERE book_id = ?", log_id)


def test_a_record_time_is_the_earliest_named_registration(fx):
    with fx.open() as shelf:
        c, book_id, first, second = record_time_shelf(shelf)
        size = scalar(shelf, "SELECT json_extract(doc, '$.size') FROM shelf_entries WHERE log_id = 'deal/a' "
                             "ORDER BY entry_seq LIMIT 1")
        from cll.checkpoint import core

        in_first, last = core.leaf_count(size), leaves_of(shelf, "deal/a")
        assert in_first < last
        t1, t2 = at(10), at(20)

        assert shelf.record_time(book_id, in_first, [WITNESS_URL]) is None, "no receipt yet"
        witness_at(c, second, WITNESS_URL, t2)
        got = shelf.record_time(book_id, in_first, [WITNESS_URL])
        assert got == unix(t2), "the second stamp's checkpoint covers the first fact"
        assert shelf.record_time(book_id, last, [WITNESS_URL]) == unix(t2)

        witness_at(c, first, OTHER_URL, t1)
        assert shelf.record_time(book_id, in_first, [WITNESS_URL, OTHER_URL]) == unix(t1), "the earliest named"
        assert shelf.record_time(book_id, in_first, [WITNESS_URL]) == unix(t2), "a witness not named gives nothing"
        assert shelf.record_time(book_id, last, [OTHER_URL]) is None, \
            "the first stamp's checkpoint does not cover the second fact"
        assert shelf.record_time(book_id, in_first, ["https://unnamed.test"]) is None
        assert shelf.record_time(book_id, in_first, []) is None, "no witness named"
        assert shelf.record_time(book_id, last + 1, [WITNESS_URL]) is None, "no fact covers the position"
        assert shelf.record_time("no-such-book", 1, [WITNESS_URL]) is None
        for bad in (0, -1, 2**64, True, 1.0):
            for named in ([WITNESS_URL], []):
                with pytest.raises(InvalidError):
                    shelf.record_time(book_id, bad, named)


def test_a_record_time_needs_a_registration_time(fx):
    with fx.open() as shelf:
        c, book_id, _, second = record_time_shelf(shelf)
        last = leaves_of(shelf, "deal/a")
        witness_at(c, second, WITNESS_URL, None)
        assert shelf.record_time(book_id, last, [WITNESS_URL]) is None, "a receipt with no registration time"
        cp = hashlib.sha256(second.checkpoint).hexdigest()
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? "
                             "AND registered_at IS NULL", cp) == 1

        later, earlier = at(30), at(20)
        witness_at(c, second, WITNESS_URL, later)
        assert shelf.record_time(book_id, last, [WITNESS_URL]) == unix(later)
        witness_at(c, second, WITNESS_URL, earlier)
        witness_at(c, second, WITNESS_URL, later + timedelta(hours=1))
        witness_at(c, second, WITNESS_URL, None)
        assert shelf.record_time(book_id, last, [WITNESS_URL]) == unix(earlier), "the witness's earliest is kept"
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ?", cp) == 1, \
            "one row per witness"
        # The row holds the earliest receipt itself, and the directory key it verified under.
        with shelf._read() as db:
            receipt, key_id, registered = db.execute("SELECT receipt, key_id, registered_at FROM shelf_receipts "
                                                     "WHERE checkpoint_sha256 = ?", (cp,)).fetchone()
        assert registered == unix(earlier)
        assert receipt == script.mint_receipt(second.checkpoint, unix(earlier)).encode()
        assert key_id == script.KEYS["witness_public"]


def test_a_record_time_covers_the_journal(fx):
    with fx.open() as shelf:
        c, _, _, _ = record_time_shelf(shelf)
        shelf.journal().append(obs("journal", "1"))
        stamp = c.stamp(at(4)).tick
        book_id = scalar(shelf, "SELECT book_id FROM shelf_entries WHERE log_id = ? LIMIT 1", JOURNAL_LOG_ID)
        iat = at(60)
        witness_at(c, stamp, WITNESS_URL, iat)
        held = leaves_of(shelf, JOURNAL_LOG_ID)
        assert shelf.record_time(book_id, held, [WITNESS_URL]) == unix(iat)
        assert shelf.record_time(book_id, held + 1, [WITNESS_URL]) is None


def test_receipts_of_witnessed_ticks_are_kept_and_backfilled(fx):
    with fx.open() as shelf:
        c = cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        tree = c.tick(START).tick
        witness_at(c, tree, WITNESS_URL, at(1))
        c.migrate(at(1))
        stamp = c.stamp(at(2)).tick
        witness_at(c, stamp, WITNESS_URL, at(3))
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts WHERE registered_at IS NOT NULL") == 2, \
            "a tree tick's receipt and a stamp's"
        with shelf._tx() as db:
            db.execute("DELETE FROM shelf_receipts")

    # A directory that no longer lists the witness backfills nothing.
    with fx.open() as shelf:
        shelf.cadence(CadenceConfig(log_id=LOG_ID, ended=lambda _: False, witnesses={"witnesses": []}))
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts") == 0

    with fx.open() as shelf:
        cadence(shelf)
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts WHERE registered_at IS NOT NULL AND "
                             "witness_id = ? AND key_id = ?", WITNESS_URL, script.KEYS["witness_public"]) == 2, \
            "each row keeps the directory key it verified under"


def test_a_corrupt_stored_receipt_fails_the_backfill(fx):
    with fx.open() as shelf:
        c = cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        witness_at(c, c.tick(START).tick, WITNESS_URL, at(1))
        with shelf._tx() as db:
            db.execute("DELETE FROM shelf_receipts")
            db.execute("UPDATE cadence_ticks SET receipt = 'not json' WHERE witnessed = 1")
    with fx.open() as shelf:
        with pytest.raises(CorruptError, match="tick 1 receipt"):
            cadence(shelf)


def test_a_handover_is_witnessed_only_with_a_registration_time(fx):
    with fx.open() as shelf:
        c, _, _, second = record_time_shelf(shelf)
        witness_at(c, second, WITNESS_URL, None)
        h = c.handover(0)
        assert len(h.receipts) == 1
        key = script.CHECKPOINT_KEY.public_key().hex()
        assert not verify_handover(h, key, DIRECTORY).witnessed, "a receipt with no registration time"

        witness_at(c, second, OTHER_URL, at(60))
        h = c.handover(0)
        assert [r.ts_url for r in h.receipts] == [OTHER_URL, WITNESS_URL], "in witness URL order"
        assert verify_handover(h, key, DIRECTORY).witnessed


def test_a_corrupt_stored_fact_or_receipt_is_corrupt(fx):
    with fx.open() as shelf:
        c, book_id, _, second = record_time_shelf(shelf)
        witness_at(c, second, WITNESS_URL, at(5))
        with shelf._tx() as db:
            db.execute("UPDATE shelf_receipts SET receipt = 'not json'")
        with pytest.raises(CorruptError, match="a stored receipt of tick"):
            c.handover(0)
        with shelf._tx() as db:
            db.execute("UPDATE shelf_entries SET doc = CAST('{\"size\": -1}' AS BLOB) WHERE book_id = ?", (book_id,))
        with pytest.raises(CorruptError, match="stored fact"):
            shelf.record_time(book_id, 1, [WITNESS_URL])
        with shelf._tx() as db:
            db.execute("UPDATE shelf_entries SET doc = CAST('{\"size\": 18446744073709551616}' AS BLOB) WHERE book_id = ?", (book_id,))
        with pytest.raises(CorruptError, match="stored fact"):
            shelf.record_time(book_id, 1, [WITNESS_URL])


# Go decodes a stored fact of JSON null, or a missing or null size, as size 0,
# and so does a journal status value of null: no fact covers, no time.
@pytest.mark.parametrize("doc", ["null", "{}", '{"size": null}'])
def test_a_stored_fact_with_no_size_covers_nothing(fx, doc):
    with fx.open() as shelf:
        c, book_id, _, second = record_time_shelf(shelf)
        witness_at(c, second, WITNESS_URL, at(5))
        with shelf._tx() as db:
            db.execute("UPDATE shelf_entries SET doc = CAST(? AS BLOB) WHERE book_id = ?", (doc, book_id))
        assert shelf.record_time(book_id, 1, [WITNESS_URL]) is None


def test_a_journal_status_with_no_size_covers_nothing(fx):
    with fx.open() as shelf:
        c, _, _, _ = record_time_shelf(shelf)
        shelf.journal().append(obs("journal", "1"))
        stamp = c.stamp(at(4)).tick
        witness_at(c, stamp, WITNESS_URL, at(60))
        book_id = scalar(shelf, "SELECT book_id FROM shelf_entries WHERE log_id = 'journal' LIMIT 1")
        assert shelf.record_time(book_id, 1, [WITNESS_URL]) == unix(at(60))
        with shelf._tx() as db:
            db.execute("UPDATE shelf_entries SET status_opening = CAST('{\"salt\": \"00\", \"value\": null}' AS BLOB) WHERE log_id = 'journal'")
        assert shelf.record_time(book_id, 1, [WITNESS_URL]) is None
        with shelf._tx() as db:
            db.execute("UPDATE shelf_entries SET status_opening = CAST('{\"salt\": \"00\"}' AS BLOB) WHERE log_id = 'journal'")
        with pytest.raises(CorruptError, match="stored journal fact"):
            shelf.record_time(book_id, 1, [WITNESS_URL])


# A registration time past datetime's year 9999 is returned as Rust and Go
# return it, in Unix seconds.
def test_a_registration_time_past_year_9999(fx):
    with fx.open() as shelf:
        c, book_id, _, second = record_time_shelf(shelf)
        far = 2**53 - 1
        receipt = script.mint_receipt(second.checkpoint, far, script.WITNESS_KEY, WITNESS_URL)
        assert c.witnessed(second.n, receipt).state == WITNESS_VERIFIED
        assert shelf.record_time(book_id, 1, [WITNESS_URL]) == far


def test_a_record_time_on_a_released_shelf(fx):
    shelf = fx.open()
    _, book_id, _, _ = record_time_shelf(shelf)
    shelf.release()
    with pytest.raises(ReleasedError):
        shelf.record_time(book_id, 1, [WITNESS_URL])


# -- the vectors (testdata/shelf/record-time.json) -------------------------------------

RECORD_TIME = json.loads((VECTORS / "record-time.json").read_text())


def test_the_record_time_phase_is_gos(tmp_path):
    path = str(tmp_path / script.FILE)
    for phase in ("write_phase", "continue_phase", "migrate_phase"):
        script.run(path, script.SCRIPT[phase])
    steps = RECORD_TIME["steps"]
    got = script.run_record_time(path, steps)
    assert got == [s.get("expect") for s in steps]
    assert script.dump(str(tmp_path)) == (VECTORS / "after-record-time.json").read_bytes()


def test_the_record_time_receipts_are_minted_as_go_mints_them(tmp_path):
    """The receipts the vector carries are Go's MintReceiptAt with the phase's
    registration times (14520 and 14490 seconds after the script's start, and
    none), so Python mints the same bytes."""
    path = str(tmp_path / script.FILE)
    for phase in ("write_phase", "continue_phase", "migrate_phase"):
        script.run(path, script.SCRIPT[phase])
    script.run_record_time(path, RECORD_TIME["steps"])
    witnessed = [s for s in RECORD_TIME["steps"] if s["op"] == "witness"]
    db = sqlite3.connect(path)
    try:
        statement = bytes(db.execute("SELECT checkpoint FROM cadence_ticks WHERE tick = ?",
                                     (witnessed[0]["expect"]["tick"],)).fetchone()[0])
    finally:
        db.close()
    for step, offset in zip(witnessed, (14520, 14490, None)):
        iat = None if offset is None else unix(script.START + timedelta(seconds=offset))
        minted = script.mint_receipt(statement, iat)
        assert json.loads(minted.encode()) == step["receipt"]


# A second key the test witness's directory row lists (a key rotation): one
# witness, one URL, two keys; never use it for anything else.
ROTATED_KEY = Ed25519Signer(hashlib.sha256(b"evidencebook record time tests/rotated witness key").digest())
TWO_KEYS = {"witnesses": [{"name": "test witness", "endpoint": WITNESS_URL,
                           "key_ids": [script.KEYS["witness_public"], ROTATED_KEY.public_key().hex()]}]}


def two_key_shelf(shelf):
    """A migrated shelf under a cadence whose directory lists two keys for the
    test witness, with deal/a's book id and the stamp after migration."""
    c = cadence(shelf, TWO_KEYS)
    shelf.open("deal/a").append(obs("a", "1"))
    c.tick(START)
    c.migrate(at(1))
    stamp = c.stamp(at(2)).tick
    book_id = scalar(shelf, "SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' LIMIT 1")
    return c, book_id, stamp


def witness_under(c, tick, key, when: datetime) -> None:
    check = c.witnessed(tick.n, script.mint_receipt(tick.checkpoint, unix(when), key, WITNESS_URL))
    assert check.state == WITNESS_VERIFIED, check.reason


@pytest.mark.parametrize("new_key_first", [False, True])
def test_two_keys_of_one_witness_keep_their_receipts(fx, new_key_first):
    """evidencebook#492 (Go TestTwoKeysOfOneWitnessKeepTheirReceipts): two keys
    of one witness that register the same checkpoint both keep their receipts,
    in either order; the record time by URL is the earliest, and the handover
    carries both."""
    with fx.open() as shelf:
        c, book_id, stamp = two_key_shelf(shelf)
        steps = [(script.WITNESS_KEY, at(20)), (ROTATED_KEY, at(10))]
        for key, when in reversed(steps) if new_key_first else steps:
            witness_under(c, stamp, key, when)
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts") == 2
        assert shelf.record_time(book_id, 1, [WITNESS_URL]) == unix(at(10)), \
            "by URL, the earliest across the witness's keys"
        c.stamp(at(3))
        h = c.handover(0)
        assert len(h.witnessed) == 1 and len(h.witnessed[0].receipts) == 2


def test_a_shelf_receipts_table_keyed_by_url_is_migrated(fx):
    """A shelf written before #492 keyed shelf_receipts by checkpoint and URL
    alone; opening it rebuilds the table and keeps every row. The tick keeps
    the old key's receipt, the old table only the new key's: the backfill at
    the cadence's open restores the old key's beside it (Go
    TestAShelfReceiptsTableKeyedByURLIsMigrated)."""
    from evidencebook.shelf import RECEIPTS_KEYED_BY_URL

    with fx.open() as shelf:
        c, book_id, stamp = two_key_shelf(shelf)
        witness_under(c, stamp, script.WITNESS_KEY, at(20))
        witness_under(c, stamp, ROTATED_KEY, at(10))
        with shelf._tx() as db:
            db.execute("CREATE TABLE old_receipts (checkpoint_sha256 TEXT NOT NULL, witness_id TEXT NOT NULL, "
                       "key_id TEXT NOT NULL, receipt TEXT NOT NULL, registered_at INTEGER, "
                       f"{RECEIPTS_KEYED_BY_URL})")
            db.execute("INSERT INTO old_receipts SELECT * FROM shelf_receipts WHERE key_id = ?",
                       (ROTATED_KEY.public_key().hex(),))
            db.execute("DROP TABLE shelf_receipts")
            db.execute("ALTER TABLE old_receipts RENAME TO shelf_receipts")
    with fx.open() as shelf:
        ddl = scalar(shelf, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'shelf_receipts'")
        assert "PRIMARY KEY (checkpoint_sha256, witness_id, key_id)" in ddl
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts") == 1, "the row is kept"
        assert shelf.record_time(book_id, 1, [WITNESS_URL]) == unix(at(10))
        cadence(shelf, TWO_KEYS)
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts WHERE key_id = ? AND registered_at = ?",
                      script.KEYS["witness_public"], unix(at(20))) == 1, "the old key's receipt is backfilled beside it"
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts") == 2
    with fx.open() as shelf:
        assert scalar(shelf, "SELECT COUNT(*) FROM shelf_receipts") == 2
