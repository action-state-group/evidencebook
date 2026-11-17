# SPDX-License-Identifier: Apache-2.0
"""The shelf log of book facts (evidencebook#461): Migrate and stamps, as Go's
shelf_stamp_test.go checks them, and the stored stamps of
testdata/shelf/after-migrate.json read and written back byte for byte."""
from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import sqlite3
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from capsule_emit.artifact.sqlite import SqliteArtifactStore
from cll.log import AppendInput

from evidencebook import Entry, OBSERVED_EVENT
from evidencebook import shelf_stamp
from evidencebook.cadence import CadenceConfig, TickLeaves, WITNESS_VERIFIED
from evidencebook.cadence import _rfc3339 as rfc3339
from evidencebook.errors import InvalidError
from evidencebook.shelf import SHELF_VERSION, SHELF_VERSION_FACTS, Shelf
from evidencebook.shelf_facts import (ENTRY_KIND_FORMAT_SWITCH, ENTRY_KIND_STAMP, FACT_KIND_BOOK, openings_for,
                                      verify_openings)

ROOT = Path(__file__).resolve().parents[2]
VECTORS = ROOT / "testdata" / "shelf"
START = datetime(2026, 10, 19, 12, 0, 30, tzinfo=timezone.utc)


def _load_script():
    spec = importlib.util.spec_from_file_location("shelfscript_stamp",
                                                  ROOT / "test" / "interop" / "python-evidencebook" / "shelf.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


script = _load_script()


class Values:
    """A test status provider: profile "deal" and a fixed status per log,
    claiming only the logs given one."""

    def __init__(self):
        self.status: dict[str, dict] = {}

    def __call__(self, book_id: str, log_id: str):
        if log_id not in self.status:
            return None
        return "deal", None, self.status[log_id]


class Fixture:
    def __init__(self, directory: Path):
        self.path = str(directory / "shelf.db")
        self.dbs: list[sqlite3.Connection] = []

    def open(self) -> Shelf:
        db = script.open_db(self.path)
        self.dbs.append(db)
        originals = SqliteArtifactStore(db, "shelf", [script.SIGNER.public_key()])
        originals.init()
        return Shelf.open_sqlite(self.path, operator="op", signer=script.SIGNER, checkpoint_key=script.CHECKPOINT_KEY,
                                 originals=originals, now=lambda: START, lock_timeout=5)

    def close(self) -> None:
        for db in self.dbs:
            db.close()


@pytest.fixture
def fx(tmp_path):
    f = Fixture(tmp_path)
    yield f
    f.close()


def stamp_cadence(shelf: Shelf):
    values = Values()
    c = shelf.cadence(CadenceConfig(log_id="deal-cadence/0123456789abcdef", ended=lambda _: False,
                                    witnesses=script.DIRECTORY, status=values))
    return c, values


def obs(subject: str, payload: str) -> Entry:
    return Entry("observation", OBSERVED_EVENT, subject_ref=subject, payloads=(payload.encode(),))


def entries_of_tick(shelf: Shelf, n: int) -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    with shelf._read() as db:
        for kind, log in db.execute("SELECT kind, log_id FROM shelf_entries WHERE tick = ? ORDER BY entry_seq", (n,)):
            out.setdefault(kind, []).append(log)
    return out


def version(path: str) -> int:
    db = sqlite3.connect(path)
    try:
        return db.execute("SELECT version FROM shelf_meta WHERE id = 1").fetchone()[0]
    finally:
        db.close()


def test_stored_stamps_are_gos():
    """Every tick of after-migrate.json reads and writes back byte for byte."""
    ticks = json.loads((VECTORS / "after-migrate.json").read_text())["cadence_ticks"]
    stamps = 0
    for tick in ticks:
        leaves = TickLeaves.decode(tick["leaves"])
        assert leaves.encode() == tick["leaves"]
        stamps += leaves.stamp is not None
    assert stamps == 4


def test_migrate_then_stamp_writes_facts_only_on_change(fx):
    with fx.open() as shelf:
        c, values = stamp_cadence(shelf)
        a = shelf.open("deal/a")
        a.append(obs("a", "1"))
        b = shelf.open("deal/b")
        b.append(obs("b", "1"))
        old = c.tick(START).tick

        # Stamp before migrate is refused; migrate writes the marker, a genesis
        # fact for each log and a stamp entry, as the tick after the last one.
        with pytest.raises(InvalidError):
            c.stamp(START)
        c.migrate(START + timedelta(minutes=1))
        assert c.migrated()
        assert version(fx.path) == SHELF_VERSION_FACTS
        genesis = entries_of_tick(shelf, old.n + 1)
        assert genesis[ENTRY_KIND_FORMAT_SWITCH] == [""]
        assert sorted(genesis[FACT_KIND_BOOK]) == ["deal/a", "deal/b"]
        assert len(genesis[ENTRY_KIND_STAMP]) == 1
        c.migrate(START + timedelta(minutes=2))  # again: a no-op
        assert c.last().n == old.n + 1

        # The backup is the version 1 file an older build opens.
        bak = sqlite3.connect(fx.path + ".v1.bak")
        assert bak.execute("SELECT version FROM shelf_meta").fetchone()[0] == SHELF_VERSION
        assert bak.execute("SELECT COUNT(*) FROM shelf_migration").fetchone()[0] == 0
        assert bak.execute("PRAGMA journal_mode").fetchone()[0] == "delete"
        bak.close()
        assert not os.path.exists(fx.path + ".v1.bak.tmp")

        # A quiet stamp: no fact, but a stamp entry and a new checkpoint.
        quiet = c.stamp(START + timedelta(minutes=3)).tick
        got = entries_of_tick(shelf, quiet.n)
        assert FACT_KIND_BOOK not in got and len(got[ENTRY_KIND_STAMP]) == 1
        assert quiet.checkpoint_size > c.get(quiet.n - 1).checkpoint_size, "an empty stamp still grows the log"

        # A grown book gets a fact; a quiet one does not.
        a.append(obs("a", "2"))
        grown = c.stamp(START + timedelta(minutes=4)).tick
        assert entries_of_tick(shelf, grown.n)[FACT_KIND_BOOK] == ["deal/a"]
        assert grown.leaves.deals["deal/a"].statement

        # A changed status value with no new record (received material) gets
        # a fact with the same size and checkpoint, and no new statement.
        values.status["deal/b"] = {"rows": [{"id": "paid_as_agreed"}]}
        received = c.stamp(START + timedelta(minutes=5)).tick
        assert entries_of_tick(shelf, received.n)[FACT_KIND_BOOK] == ["deal/b"]
        last = shelf_stamp._last_fact_of(shelf, "deal/b")
        assert last.fact["size"] == last.fact["prev_fact_size"]
        assert last.fact["profile"] == "deal"
        assert last.status == {"rows": [{"id": "paid_as_agreed"}]}
        assert received.leaves.deals["deal/b"].statement == ""
        again = c.stamp(START + timedelta(minutes=6)).tick
        assert FACT_KIND_BOOK not in entries_of_tick(shelf, again.n), "the same values again: no fact"

        # The heartbeat stamps after the switch.
        beat = c.tick(again.due_next).tick
        assert beat.leaves.stamp is not None

        # /1's anchor still counts every book, the record added after migration included.
        before = c.standard_history_anchor(old.n)
        anchor = c.standard_history_anchor(0)
        for book in (a, b):
            assert anchor.extents[book.log_id] == book.size()
        assert anchor.extents["deal/a"] > before.extents["deal/a"]
        assert anchor.extents["deal/b"] == before.extents["deal/b"]


def test_a_stamp_stored_but_not_finished_is_finished_first(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        a = shelf.open("deal/a")
        a.append(obs("a", "1"))
        c.migrate(START)
        a.append(obs("a", "2"))

        # Store a stamp and append only its first entry, as a crash would leave it.
        with shelf._mu:
            last = c._last_locked()
            log_id = shelf_stamp._cadence_log_ids(shelf)[0]
            log = shelf._cadence_log_locked(log_id)
            prev = shelf_stamp._last_fact_of(shelf, log_id)
            f = shelf_stamp._fact_for(c, log, START, prev)
            tick = shelf_stamp._build_stamp_locked(c, last.n + 1, START, START, [f], [])
            with shelf._tx() as db:
                db.execute(shelf_stamp._INSERT_TICK, (tick.n, rfc3339(tick.at), rfc3339(tick.due_next),
                                                      tick.leaves.encode()))
            c._log.append(hashlib.sha256(tick.leaves.stamp.entries[0].doc).hexdigest(), tick.at)

        # The next stamp finishes that one, appending only what is missing.
        result = c.stamp(START + timedelta(hours=1))
        assert result.tick.n == tick.n
        got = entries_of_tick(shelf, tick.n)
        assert got[FACT_KIND_BOOK] == ["deal/a"] and len(got[ENTRY_KIND_STAMP]) == 1
        ids = [bytes(e.value).hex() for e in c._log.entries_after(0, 1000)]
        assert len(ids) == len(set(ids)), "every entry appended once"
        assert c.stamp(START + timedelta(hours=2)).tick.n == tick.n + 1


def test_a_heartbeat_finishes_an_unfinished_stamp(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        c.migrate(START)
        real = c._log.checkpoint

        def crash(at):
            raise OSError("crash")

        c._log.checkpoint = crash
        with pytest.raises(OSError):
            c.stamp(START + timedelta(minutes=1))
        n = c.last().n
        assert c.get(n).checkpoint_size == 0
        c._log.checkpoint = real
        result = c.tick(START + timedelta(minutes=2))
        assert result.tick.n == n and result.tick.checkpoint_size > 0


def test_migrate_resumes_after_a_backup_and_refuses_a_foreign_one(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))

        # A foreign <path>.v1.bak: migrate refuses and writes nothing past the
        # version switch.
        Path(fx.path + ".v1.bak").write_bytes(b"not this shelf")
        with pytest.raises(InvalidError):
            c.migrate(START)
        assert shelf_stamp.migration_state(shelf) == shelf_stamp.MIGRATION_BACKING_UP
        assert not c.migrated()

        # Moved aside, migrate resumes from where it stopped; a leftover
        # temporary file from an unfinished attempt is replaced.
        os.remove(fx.path + ".v1.bak")
        Path(fx.path + ".v1.bak.tmp").write_bytes(b"partial")
        c.migrate(START)
        assert c.migrated()
        assert not os.path.exists(fx.path + ".v1.bak.tmp")
        assert version(fx.path + ".v1.bak") == SHELF_VERSION


def test_migrate_reuses_a_backup_it_recorded(fx):
    """A crash after the digest is recorded and before the rename: the
    temporary file is renamed, not made again; one already renamed is kept."""
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        real = os.rename
        calls = []

        def crash(src, dst):
            calls.append(src)
            raise OSError("crash")

        shelf_stamp.os.rename = crash
        try:
            with pytest.raises(OSError):
                c.migrate(START)
        finally:
            shelf_stamp.os.rename = real
        tmp = fx.path + ".v1.bak.tmp"
        digest = hashlib.sha256(Path(tmp).read_bytes()).hexdigest()
        c.migrate(START)
        assert c.migrated()
        assert hashlib.sha256(Path(fx.path + ".v1.bak").read_bytes()).hexdigest() == digest


def test_a_migrated_shelf_reopens(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        c.migrate(START)
    with fx.open() as shelf:
        assert version(fx.path) == SHELF_VERSION_FACTS
        c, _ = stamp_cadence(shelf)
        assert c.migrated()
        c.stamp(START + timedelta(minutes=1))


def test_a_stamp_is_witnessed_like_a_tick(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        a = shelf.open("deal/a")
        a.append(obs("a", "1"))
        c.migrate(START)
        a.append(obs("a", "2"))
        stamp = c.stamp(START + timedelta(minutes=1)).tick
        check = c.witnessed(stamp.n, script.mint_receipt(stamp.checkpoint))
        assert check.state == WITNESS_VERIFIED
        assert c.get(stamp.n).witnessed
        leaf = stamp.leaves.deals["deal/a"]
        with shelf._read() as db:
            n = db.execute("SELECT COUNT(*) FROM cadence_witnessed WHERE log_id = ? AND checkpoint_sha256 = ?",
                           ("deal/a", leaf.checkpoint_sha256)).fetchone()[0]
        assert n == 0, "a stamp leaves the tree's witnessed index alone"


AUDIENCES = ("counterparty", "adjudicator", "asker", "chosen_reader", "owner", "auditor")


def test_each_audience_gets_exactly_its_openings(fx):
    with fx.open() as shelf:
        c, values = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        values.status["deal/a"] = {"rows": []}
        c.migrate(START)
        last = shelf_stamp._last_fact_of(shelf, "deal/a")
        for audience in AUDIENCES:
            facts = shelf.openings(last.fact["book_id"], audience)
            assert len(facts) == 1, audience
            want_link, want_status = openings_for(FACT_KIND_BOOK, audience)
            f = facts[0]
            assert (f.link_opening is not None) == want_link, audience
            assert (f.status_opening is not None) == want_status, audience
            verify_openings(f.fact, f.link_opening, f.status_opening)
        with pytest.raises(InvalidError):
            shelf.openings(last.fact["book_id"], "stranger")


def test_no_tick_while_a_migration_is_under_way(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        Path(fx.path + ".v1.bak").write_bytes(b"not this shelf")
        with pytest.raises(InvalidError):
            c.migrate(START)
        with pytest.raises(InvalidError):
            c.tick(START + timedelta(hours=1))
        os.remove(fx.path + ".v1.bak")
        c.migrate(START)
        beat = c.tick(START + timedelta(hours=2))
        assert beat.ticked and beat.tick.leaves.stamp is not None


def test_checkpoint_whole_cuts_one_checkpoint_over_a_long_batch(tmp_path):
    """A batch longer than one runner scan (MAX_SCAN_LIMIT) gets one
    checkpoint, never one over part of it: the first pending witness delivery
    is at the full size (Go TestCheckpointWholeCutsOneCheckpointOverALongBatch)."""
    from cll.log import MAX_SCAN_LIMIT

    from evidencebook.shelf import SqliteSubstrate, mmr_size

    log = SqliteSubstrate.open(str(tmp_path / "log.db"), "deal-cadence/0123456789abcdef", script.CHECKPOINT_KEY,
                               ["test-witness"])
    try:
        n = 2500
        assert n > 2 * MAX_SCAN_LIMIT
        for i in range(n):
            log.append(f"{i + 1:064x}", START)
        cp = log.checkpoint_whole(START)
        assert cp.tree_size == mmr_size(n)
        pending = log.backend.pending_witnesses(START + timedelta(hours=1), 1)
        assert len(pending) == 1
        assert pending[0].checkpoint_size == cp.tree_size, \
            "the first delivery queued is the whole batch's checkpoint, not one over part of it"
    finally:
        log.close()


def test_only_a_book_is_asked_for_its_status(fx):
    """A status function that answers for every log changes a book's fact and
    never a host log's: profile "none", the empty link value and {}."""
    asked = []

    def every(book_id: str, log_id: str):
        asked.append(log_id)
        return "deal", {"last_received": None, "checked_over": ["ab" * 32]}, {"rows": [{"id": "x"}]}

    with fx.open() as shelf:
        c = shelf.cadence(CadenceConfig(log_id="deal-cadence/0123456789abcdef", ended=lambda _: False,
                                        witnesses=script.DIRECTORY, status=every))
        shelf.open("deal/a").append(obs("a", "1"))
        shelf.host_log("legacy/x", True).write(
            lambda stores: stores.log.append(AppendInput(hashlib.sha256(b"x").digest(), START)))
        c.migrate(START)
        assert asked == ["deal/a"]
        book, host = shelf_stamp._last_fact_of(shelf, "deal/a"), shelf_stamp._last_fact_of(shelf, "legacy/x")
        assert book.fact["profile"] == "deal" and book.status == {"rows": [{"id": "x"}]}
        assert book.link == {"last_received": None, "checked_over": ["ab" * 32]}
        assert host.fact["profile"] == "none"
        assert host.link == {"last_received": None, "checked_over": []} and host.status == {}
        # The same answers again: no new fact for either.
        quiet = c.stamp(START + timedelta(minutes=1)).tick
        assert FACT_KIND_BOOK not in entries_of_tick(shelf, quiet.n)


# -- the recovery scenarios (testdata/shelf/after-recover-*.json, #461 §7) --------------
#
# Go's test/interop/shelf plants these for the cross-language interlock; the
# plants are ported here (internal/shelfscript/recover.go, step for step) so the
# vectors are checked without Go. A plant runs the interrupted operation on a
# copy and copies back only what the crash would have left.

PLANT_STREAM, PLANT_AT = "evidencebook shelf vectors/plant", script.START + timedelta(seconds=10800)


def _on_copy(path: str, act, apply) -> None:
    scratch = Path(path).parent / "plant"
    scratch.mkdir()
    copy = str(scratch / script.FILE)
    original = script.open_db(path)
    try:
        original.execute("VACUUM INTO ?", (copy,))
        act(copy)
        copied = script.open_db(copy)
        try:
            apply(original, copied)
        finally:
            copied.close()
    finally:
        original.close()


def _copy_last_tick(path: str, original: sqlite3.Connection, copied: sqlite3.Connection, appended: int) -> None:
    from cll.log import sqlite as cllsqlite

    n, at, due, leaves = copied.execute("SELECT tick, at, due_next, leaves FROM cadence_ticks ORDER BY tick DESC "
                                        "LIMIT 1").fetchone()
    original.execute("INSERT INTO cadence_ticks (tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves) "
                     "VALUES (?, ?, ?, 0, 0, x'', ?)", (n, at, due, leaves))
    if appended == 0:
        return
    entries = copied.execute("SELECT entry_seq, kind, book_id, log_id, doc, link_opening, status_opening FROM "
                             "shelf_entries WHERE tick = ? ORDER BY entry_seq LIMIT ?", (n, appended)).fetchall()
    when = datetime.strptime(at, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
    log = cllsqlite.open(path, script.CADENCE["log_id"])
    try:
        for seq, kind, book, log_id, doc, lo, so in entries:
            result = log.append(AppendInput(hashlib.sha256(bytes(doc)).digest(), when))
            assert result.entry.seq == seq, f"planted entry {seq} landed at {result.entry.seq}"
            original.execute("INSERT INTO shelf_entries (entry_seq, kind, book_id, log_id, doc, link_opening, "
                             "status_opening, tick) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                             (seq, kind, book, log_id, doc, lo, so, n))
    finally:
        log.close()


def plant_tick(path: str) -> None:
    """An empty host log in the cadence and a tree tick stored but not finished."""
    env = script.Env(path, PLANT_STREAM, PLANT_AT)
    try:
        env.shelf.host_log("legacy/empty", True)
    finally:
        env.release()

    def act(copy: str) -> None:
        c = script.Env(copy, PLANT_STREAM, PLANT_AT)
        try:
            c.tick()
        finally:
            c.release()

    _on_copy(path, act, lambda original, copied: _copy_last_tick(path, original, copied, 0))


def plant_migration(path: str, appended: int = 2) -> None:
    """A migration stopped in its step 4: version 2, state migrating, its
    backup recorded and in place, its batch stored, its first entries appended."""
    def act(copy: str) -> None:
        c = script.Env(copy, PLANT_STREAM, PLANT_AT)
        try:
            c.cadence.migrate(c.clock())
        finally:
            c.release()
        with open(copy + ".v1.bak", "rb") as src, open(path + ".v1.bak", "xb") as dst:
            dst.write(src.read())

    def apply(original: sqlite3.Connection, copied: sqlite3.Connection) -> None:
        digest = copied.execute("SELECT backup_sha256 FROM shelf_migration WHERE id = 1").fetchone()[0]
        original.execute("UPDATE shelf_meta SET version = 2 WHERE id = 1")
        original.execute("INSERT INTO shelf_migration (id, state, backup_sha256) VALUES (1, 'migrating', ?)", (digest,))
        _copy_last_tick(path, original, copied, appended)

    _on_copy(path, act, apply)


@pytest.mark.parametrize("scenario,plant", [("tick", plant_tick), ("migration", plant_migration)])
def test_a_planted_crash_recovers_to_the_vector(tmp_path, scenario, plant):
    path = str(tmp_path / script.FILE)
    script.run(path, script.SCRIPT["write_phase"])
    script.run(path, script.SCRIPT["continue_phase"])
    plant(path)
    db = sqlite3.connect(path)
    try:
        unfinished = db.execute("SELECT COUNT(*) FROM cadence_ticks WHERE checkpoint_size = 0").fetchone()[0]
        state = db.execute("SELECT state FROM shelf_migration").fetchall()
    finally:
        db.close()
    assert unfinished == 1
    assert state == ([("migrating",)] if scenario == "migration" else [])
    script.run(path, script.SCRIPT["recover_phase"])
    assert script.dump(str(tmp_path)) == (VECTORS / f"after-recover-{scenario}.json").read_bytes()
