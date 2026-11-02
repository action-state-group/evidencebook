# SPDX-License-Identifier: Apache-2.0
"""The shelf (evidencebook#254 §2) and its cadence: the byte vectors Go
writes in testdata/shelf, the scripted shelf run in this process, and the
contract's rules, as Go's shelf_*_test.go and cadence_*_test.go check them."""
from __future__ import annotations

import base64
import hashlib
import importlib.util
import json
import sqlite3
import threading
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from capsule_emit.artifact.sqlite import SqliteArtifactStore
from cll.log import AppendInput

from evidencebook import Entry, OBSERVED_EVENT
from evidencebook._gojson import go_json_indent
from evidencebook._random import RandomSource, deterministic_stream
from evidencebook.cadence import (
    CADENCE_DEPTH, EMPTY, CadenceConfig, CadenceTick, LegacyCadence, TickLeaf, TickLeaves, WitnessReceipt,
    WitnessRejected, WITNESS_NOT_CHECKED, WITNESS_VERIFIED, cadence_fold, cadence_leaf, derived_fillers, filler_leaf,
    padding_record_id, random_position,
)
from evidencebook.errors import CorruptError, InvalidError, NotFoundError, ReleasedError, UnsupportedError
from evidencebook.shelf import SHELF_VERSION, Shelf

ROOT = Path(__file__).resolve().parents[2]
VECTORS = ROOT / "testdata" / "shelf"


def _load_script():
    spec = importlib.util.spec_from_file_location("shelfscript", ROOT / "test" / "interop" / "python-evidencebook" / "shelf.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


script = _load_script()

# -- the byte vectors -------------------------------------------------------------------

TREE = json.loads((VECTORS / "tree.json").read_text())


def test_tree_vectors():
    assert TREE["depth"] == CADENCE_DEPTH
    assert [h.hex() for h in EMPTY] == TREE["empty_roots"]
    for leaf in TREE["leaves"]:
        assert cadence_leaf(leaf["log_id"], leaf["size"], leaf["checkpoint_sha256"], leaf["salt"]).hex() == leaf["leaf"]
    for filler in TREE["fillers"]:
        assert filler_leaf(bytes.fromhex(filler["nonce"])).hex() == filler["leaf"]
    for case in TREE["derived_fillers"]:
        got = derived_fillers(bytes.fromhex(case["seed"]), case["count"], set(case["used"]))
        assert [{"position": p, "leaf": got[p].hex()} for p in sorted(got)] == case["fillers"]
    for tree in TREE["trees"]:
        leaves = TickLeaves.decode(tree["leaves"])
        assert leaves.encode() == tree["leaves"]
        assert leaves.tree(leaves.filler_index)[0].hex() == tree["root"]
        for p in tree["paths"]:
            root, path = leaves.tree(p["index"])
            assert [node.hex() for node in path] == p["path"]
            assert root.hex() == tree["root"]
    for padding in TREE["padding"]:
        assert padding_record_id(padding["nonce"]) == padding["record_id"]


def test_random_draws():
    random, used = RandomSource(deterministic_stream(TREE["draw_stream"])), set()
    for draw in TREE["draws"]:
        if draw["draw"] == "bytes":
            assert random.hex(draw["n"]) == draw["value"]
        elif draw["draw"] == "uniform":
            assert random.uniform(draw["n"]) == draw["value"]
        else:
            assert sorted(draw.get("used", [])) == sorted(used)
            assert random_position(random, used) == draw["value"]


def test_capsule_cli_tree():
    v = json.loads((VECTORS / "cadence" / "capsule-cli-7a6b101.json").read_text())
    assert EMPTY[CADENCE_DEPTH].hex() == v["empty_root"]
    assert filler_leaf(bytes.fromhex(v["filler_nonce"])).hex() == v["filler_leaf"]
    deals = {}
    for leaf in v["leaves"]:
        assert cadence_leaf(leaf["log_id"], leaf["size"], leaf["checkpoint_sha256"], leaf["salt"]).hex() == leaf["leaf"]
        deals[leaf["log_id"]] = TickLeaf(leaf["log_id"], leaf["size"], leaf["checkpoint_sha256"], leaf["salt"],
                                         leaf["index"])
    stored = TickLeaves(deals, v["filler_index"], v["filler_nonce"])
    for leaf in v["leaves"]:
        root, path = stored.tree(leaf["index"])
        assert root.hex() == v["root"] and [p.hex() for p in path] == leaf["path"]
        assert cadence_fold(bytes.fromhex(leaf["leaf"]), leaf["index"], path) == root
    # A tick a released build stored (no filler_seed) reads unchanged.
    legacy = TickLeaves.decode('{"deals":{},"filler_index":3,"filler_nonce":"' + v["filler_nonce"] + '"}')
    assert legacy.filler_count == 0


def test_tick_leaves_refuse_out_of_range():
    with pytest.raises(CorruptError):
        TickLeaves({}, 1 << 16, "00" * 32).tree(0)
    with pytest.raises(CorruptError):
        TickLeaves({"a": TickLeaf("a", 1, "00" * 32, "00" * 32, 7)}, 7, "00" * 32).tree(7)
    with pytest.raises(CorruptError):
        TickLeaves({}, 0, "00" * 32, "beef", 2).tree(0)


def test_the_scripted_shelf_is_gos(tmp_path):
    """Both phases, in this process, give Go's dumps byte for byte; a dump
    leaves the shelf as it was (it recovers the pending record on a copy)."""
    script.run(str(tmp_path / "shelf.db"), script.SCRIPT["write_phase"])
    assert script.dump(str(tmp_path)) == (VECTORS / "after-write.json").read_bytes()
    assert script.dump(str(tmp_path)) == (VECTORS / "after-write.json").read_bytes()
    script.run(str(tmp_path / "shelf.db"), script.SCRIPT["continue_phase"])
    assert script.dump(str(tmp_path)) == (VECTORS / "after-continue.json").read_bytes()


def test_minted_receipts_are_the_vectors():
    for name in ("after-write.json", "after-continue.json"):
        for tick in json.loads((VECTORS / name).read_text())["cadence_ticks"]:
            if tick["receipt"] is not None:
                statement = base64.urlsafe_b64decode(tick["checkpoint"] + "=" * (-len(tick["checkpoint"]) % 4))
                assert script.mint_receipt(statement).encode() == tick["receipt"]


def test_run_refuses_a_step_out_of_place(tmp_path):
    with pytest.raises(RuntimeError, match="with the shelf released"):
        script.run(str(tmp_path / "shelf.db"), [{"op": "tick"}])


# -- the contract ---------------------------------------------------------------------

START = datetime(2026, 10, 31, 12, 0, tzinfo=timezone.utc)
SIGNER, LOG_KEY = script.SIGNER, script.CHECKPOINT_KEY


class Fixture:
    def __init__(self, directory: Path):
        self.path = str(directory / "shelf.db")
        self.at = START
        self.dbs: list[sqlite3.Connection] = []

    def originals(self) -> SqliteArtifactStore:
        db = script.open_db(self.path)
        self.dbs.append(db)
        store = SqliteArtifactStore(db, "shelf", [SIGNER.public_key()])
        store.init()
        return store

    def open(self, **kwargs) -> Shelf:
        kwargs.setdefault("lock_timeout", 5)
        return Shelf.open_sqlite(self.path, operator="op", signer=SIGNER, checkpoint_key=LOG_KEY,
                                 originals=self.originals(), now=lambda: self.at, **kwargs)

    def close(self) -> None:
        for db in self.dbs:
            db.close()


@pytest.fixture
def fx(tmp_path):
    f = Fixture(tmp_path)
    yield f
    f.close()


def cadence(shelf: Shelf, ended=(), witnesses=script.DIRECTORY, **kwargs):
    return shelf.cadence(CadenceConfig(log_id="deal-cadence/0000000000000001", ended=lambda i: i in ended,
                                       witness_ids=["test-witness"], witnesses=witnesses, **kwargs))


def obs(payload: str = "x", subject: str = "s") -> Entry:
    return Entry("observation", OBSERVED_EVENT, subject_ref=subject, payloads=(payload.encode(),))


def witness(c, tick: CadenceTick):
    return c.witnessed(tick.n, script.mint_receipt(tick.checkpoint))


def test_a_shelf_keeps_each_book_in_its_own_log_and_is_durable(fx):
    with fx.open() as shelf:
        a, b = shelf.open("deal/a"), shelf.open("deal/b")
        assert shelf.open("deal/a") is a
        a.append(obs("1"))
        a.append(obs("2"))
        b.append(obs("1"))
        a_cp = a.checkpoint()
        assert shelf.books() == ["deal/a", "deal/b"]
    with fx.open() as shelf:
        a = shelf.open("deal/a")
        assert [r.seq for r in a.records()] == [1, 2, 3, 4]  # two index roots
        assert a.checkpoints()[-1].statement == a_cp.statement
        assert a._payloads(a.records()[0].record_id) == (b"1",)
        a.append(obs("3"))
        assert a.size() == 5


def test_a_fresh_shelf_is_version_one_and_an_unknown_version_is_refused(fx):
    fx.open().release()
    db = sqlite3.connect(fx.path)
    assert db.execute("SELECT version FROM shelf_meta").fetchall() == [(SHELF_VERSION,)]
    db.execute("UPDATE shelf_meta SET version = 2")
    db.commit()
    db.close()
    with pytest.raises(UnsupportedError):
        fx.open()
    db = sqlite3.connect(fx.path)
    db.execute("UPDATE shelf_meta SET version = 1")
    db.commit()
    db.close()
    fx.open().release()


def test_a_pre_version_shelf_is_filled_on_open(fx):
    with fx.open() as shelf:
        shelf.open("deal/a").append(obs())
        shelf.host_log("legacy/x", True).write(
            lambda s: s.log.append(AppendInput(hashlib.sha256(b"x").digest(), START)))
    db = sqlite3.connect(fx.path)
    db.execute("DROP TABLE shelf_meta")
    db.execute("DROP TABLE shelf_logs")
    db.commit()
    db.close()
    with fx.open() as shelf:
        with pytest.raises(InvalidError):
            shelf.open("legacy/x")
    db = sqlite3.connect(fx.path)
    assert db.execute("SELECT version FROM shelf_meta").fetchall() == [(1,)]
    assert db.execute("SELECT log_id, kind FROM shelf_logs ORDER BY log_id").fetchall() == [
        ("deal/a", "book"), ("legacy/x", "host")]
    db.close()


def test_book_and_host_log_ids_stay_apart(fx):
    with fx.open() as shelf:
        shelf.open("deal/a").append(obs())
        shelf.host_log("host/h", False)
        with pytest.raises(InvalidError):
            shelf.open("host/h")
        with pytest.raises(InvalidError):
            shelf.host_log("deal/a", False)
        with pytest.raises(InvalidError):
            shelf.legacy("deal/a")
        with pytest.raises(InvalidError):
            shelf.host_log("host/h", True)
        with pytest.raises(InvalidError):
            shelf.cadence(CadenceConfig(log_id="deal/a", ended=lambda i: False))
    with fx.open() as shelf:
        with pytest.raises(InvalidError):
            shelf.open("host/h")
        with pytest.raises(InvalidError, match="outside the cadence"):
            shelf.host_log("host/h", True)
        with pytest.raises(InvalidError):
            shelf.cadence(CadenceConfig(log_id="host/h", ended=lambda i: False))
        shelf.host_log("host/h", False)


def test_a_host_log_outside_the_cadence_is_the_hosts(fx):
    with fx.open() as shelf:
        c = cadence(shelf)
        host = shelf.host_log("host/h", False)
        host.write(lambda s: s.log.append(AppendInput(hashlib.sha256(b"h").digest(), START)))
        shelf.open("deal/a").append(obs())
        tick = c.tick(START).tick
        assert sorted(tick.leaves.deals) == ["deal/a"]
        assert c.live() == ["deal/a"]
        assert host.cut_local(START).entries == 1


def test_a_shelf_is_the_only_writer_and_takes_the_hosts_lock(fx):
    shelf = fx.open()
    with pytest.raises(TimeoutError):
        fx.open(lock_timeout=0.1)
    shelf.release()
    fx.open().release()
    with fx.open(lock_path=fx.path + ".deal.lock") as held:
        with pytest.raises(TimeoutError):
            fx.open(lock_path=fx.path + ".deal.lock", lock_timeout=0.1)
        fx.open(lock_timeout=0.1).release()  # another lock file
        assert held.books() == []


def test_a_shelf_on_a_host_held_lock_takes_none(fx):
    class Held:
        def __init__(self, ok):
            self.ok = ok

        def held(self):
            if not self.ok:
                raise RuntimeError("not held")

    with fx.open(held_lock=Held(True)):
        # The shelf took no lock of its own: the default lock is free.
        fx.open(lock_timeout=0.1).release()
    with pytest.raises(InvalidError):
        fx.open(held_lock=Held(False))
    with pytest.raises(InvalidError):
        fx.open(held_lock=Held(True), lock_path=fx.path + ".x")


def test_a_released_shelf_refuses_every_write(fx):
    shelf = fx.open()
    book = shelf.open("deal/a")
    shelf.release()
    shelf.release()
    with pytest.raises(ReleasedError):
        book.append(obs())
    with pytest.raises(ReleasedError):
        shelf.open("deal/b")


def test_a_corrupt_log_is_never_reprovisioned(fx):
    with fx.open() as shelf:
        shelf.open("deal/a").append(obs())
    db = sqlite3.connect(fx.path)
    db.execute("DELETE FROM cll_meta WHERE log_id = 'deal/a'")
    db.commit()
    db.close()
    with fx.open() as shelf:
        with pytest.raises(CorruptError):
            shelf.open("deal/a")
    db = sqlite3.connect(fx.path)
    assert db.execute("SELECT COUNT(*) FROM cll_entries WHERE log_id = 'deal/a'").fetchone()[0] == 1
    db.close()


def test_a_crash_between_the_writes_is_recovered(fx):
    with fx.open() as shelf:
        shelf.open("deal/a").append(obs("1"))
    script.plant_pending(fx.path, "deal/a", "s", START + timedelta(minutes=1))
    with fx.open() as shelf:
        listing = shelf.records()
        assert [r.seq for r in listing.records] == [1] and listing.pending == {"deal/a": 1}
        book = shelf.open("deal/a")
        assert book.size() == 2
        assert shelf.records().pending == {}
    # A log entry with no stored record is corrupt.
    db = sqlite3.connect(fx.path)
    db.execute("DELETE FROM book_records WHERE book_id = 'deal/a' AND seq = 2")
    db.commit()
    db.close()
    with fx.open() as shelf:
        with pytest.raises(CorruptError):
            shelf.open("deal/a")


def test_a_failed_log_append_is_retried_first(fx):
    with fx.open() as shelf:
        book = shelf.open("deal/a")
        real = book._substrate.append
        calls = {"n": 0}

        def failing(record_id, at=None):
            calls["n"] += 1
            if calls["n"] == 1:
                raise sqlite3.OperationalError("disk I/O error")
            return real(record_id, at)

        book._substrate.append = failing
        with pytest.raises(CorruptError, match="retried first"):
            book.append(obs("1"))
        assert book.size() == 0
        book.append(obs("2"))
        assert [r.seq for r in book.records()] == [1, 2]


def test_a_shelf_chooses_its_books_indexes(fx):
    with fx.open(authenticated_indexes=lambda log_id: [] if log_id == "deal/bare" else None) as shelf:
        bare, full = shelf.open("deal/bare"), shelf.open("deal/full")
        for book in (bare, full):
            book.append(obs())
            book.checkpoint()
        assert bare.size() == 1 and full.size() == 3


def test_records_read_without_opening(fx):
    with fx.open() as shelf:
        a = shelf.open("deal/a")
        first = a.append(obs("1"))
        shelf.open("deal/b").append(obs("1"))
        from agent_action_capsule.evidence_request import Subject
        a.commit_retention(Subject("record", first.record_id), START + timedelta(days=1))
        listing = shelf.records("deal/a")
        assert [(r.book_id, r.seq, r.retention) for r in listing.records] == [
            ("deal/a", 1, "LEGAL_HOLD"), ("deal/a", 2, "")]
        fx.at = START + timedelta(days=2)
        assert shelf.records("deal/a").records[0].retention == "AVAILABLE"
        assert [r.book_id for r in shelf.records().records] == ["deal/a", "deal/a", "deal/b"]


def test_concurrent_appends_to_two_books(fx):
    with fx.open() as shelf:
        books = [shelf.open(f"deal/{i}") for i in range(4)]
        errors = []

        def work(book):
            try:
                for n in range(10):
                    book.append(obs(str(n)))
            except Exception as exc:  # noqa: BLE001
                errors.append(exc)

        threads = [threading.Thread(target=work, args=(b,)) for b in books]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        assert not errors
        assert [b.size() for b in books] == [10] * 4


# -- the cadence ---------------------------------------------------------------------


def test_a_tick_is_due_on_time_alone(fx):
    with fx.open(random=deterministic_stream("t")) as shelf:
        c = cadence(shelf)
        first = c.tick(START + timedelta(seconds=30))
        assert first.ticked and first.tick.at == START
        assert not c.tick(first.due_next - timedelta(seconds=1)).ticked
        second = c.tick(first.due_next)
        assert second.ticked and second.tick.n == 2
        assert timedelta(minutes=4) <= second.due_next - first.due_next <= timedelta(minutes=6)


def test_no_jitter_is_exact(fx):
    with fx.open() as shelf:
        c = cadence(shelf, no_jitter=True)
        result = c.tick(START + timedelta(seconds=30))
        assert result.due_next == START + timedelta(minutes=5, seconds=30)
    with pytest.raises(InvalidError):
        CadenceConfig(log_id="c", ended=lambda i: False, no_jitter=True, jitter=timedelta(seconds=1)).checked()
    with pytest.raises(InvalidError):
        CadenceConfig(log_id="c", ended=lambda i: False, interval=timedelta(seconds=30)).checked()
    with pytest.raises(InvalidError):
        CadenceConfig(log_id="c", ended=lambda i: False, jitter=timedelta(minutes=3)).checked()


def test_ended_logs_leave_the_tick_once_witnessed(fx):
    with fx.open() as shelf:
        c = cadence(shelf, ended=("deal/b",))
        shelf.open("deal/a").append(obs())
        b = shelf.open("deal/b")
        b.append(obs())
        tick = c.tick(START).tick
        assert witness(c, tick).state == WITNESS_VERIFIED
        assert c.live() == ["deal/a"]
        b.append(obs("later"))
        assert c.live() == ["deal/a", "deal/b"]


def test_a_receipt_no_directory_key_covers_is_not_checked(fx):
    with fx.open() as shelf:
        c = cadence(shelf, witnesses=None, ended=("deal/a",))
        shelf.open("deal/a").append(obs())
        tick = c.tick(START).tick
        assert witness(c, tick).state == WITNESS_NOT_CHECKED
        assert not c.get(tick.n).witnessed and c.live() == ["deal/a"]


def test_a_receipt_that_fails_is_rejected(fx):
    with fx.open() as shelf:
        c = cadence(shelf)
        shelf.open("deal/a").append(obs())
        tick = c.tick(START).tick
        good = script.mint_receipt(tick.checkpoint)
        with pytest.raises(WitnessRejected) as rejected:
            c.witnessed(tick.n, WitnessReceipt(good.ts_url, "00" * 32, good.receipt, 0, 1))
        assert rejected.value.check.state == "INVALID"
        with pytest.raises(WitnessRejected):
            c.witnessed(tick.n, WitnessReceipt("", good.entry_hash, good.receipt))
        assert not c.get(tick.n).witnessed


def test_an_unfinished_tick_is_finished_not_repeated(fx):
    with fx.open(random=deterministic_stream("u")) as shelf:
        c = cadence(shelf, pad_bucket=4)
        shelf.open("deal/a").append(obs())
        real = c._log.checkpoint

        def crash(at):
            raise OSError("crash")

        c._log.checkpoint = crash
        with pytest.raises(OSError):
            c.tick(START)
        assert c.get(1).checkpoint_size == 0
        with pytest.raises(InvalidError):
            c.witnessed(1, WitnessReceipt("https://witness.test", "00" * 32, b"x"))
        with pytest.raises(NotFoundError):
            c.standard_history_anchor(1)
        c._log.checkpoint = real
        result = c.tick(START + timedelta(hours=1))
        assert result.tick.n == 1 and result.tick.at == START and result.tick.checkpoint_size > 0
        assert c._log.size == 4  # the root and three padding records, once


def test_the_chain_is_all_then_part(fx):
    with fx.open() as shelf:
        c = cadence(shelf)
        a = shelf.open("deal/a")
        a.append(obs("1"))
        tick = c.tick(START).tick
        statement = base64.urlsafe_b64decode(tick.leaves.deals["deal/a"].statement + "==")
        with pytest.raises(NotFoundError):
            c.chain(statement)
        witness(c, tick)
        chain = c.chain(statement)
        assert chain["extent"] == "all" and chain["index"] == tick.leaves.deals["deal/a"].index
        assert set(chain) == {"state", "extent", "checkpoint_at", "log_id", "size", "salt", "index", "path", "cadence"}
        a.append(obs("2"))
        later = c.chain(a.checkpoint().statement)
        assert later["extent"] == "part" and set(later["earlier"]) == {"checkpoint", "consistency_proof"}
        go_json_indent(later)  # the chain is JSON as Go writes it
        with pytest.raises(CorruptError):
            c.chain(b"not a statement")


def test_the_standard_history_anchor(fx):
    with fx.open() as shelf:
        c = cadence(shelf, ended=("deal/b",))
        with pytest.raises(NotFoundError):
            c.standard_history_anchor()
        shelf.open("deal/a").append(obs())
        shelf.open("deal/b").append(obs())
        first = c.tick(START).tick
        witness(c, first)
        shelf.open("deal/a").append(obs("2"))
        second = c.tick(first.due_next).tick
        anchor = c.standard_history_anchor()
        assert anchor.tick.n == 2 and anchor.anchor == hashlib.sha256(second.checkpoint).hexdigest()
        assert anchor.first_checkpoint_time == "2026-10-31T12:00:00Z"
        # deal/b left the live set after the witnessed tick and stays at its extent.
        assert anchor.extents == {"deal/a": 6, "deal/b": 3}


def test_the_cadence_history_card_is_a_staple_over_the_ticks(fx):
    from evidencebook.signer import key_id
    from evidencebook.staple import check_staple

    with fx.open() as shelf:
        c = cadence(shelf)
        with pytest.raises(NotFoundError):
            c.history_card()
        book = shelf.open("deal/a")
        book.append(obs())
        first = c.tick(START).tick
        book.append(obs("2"))
        second = c.tick(first.due_next).tick
        third = c.tick(second.due_next).tick
        holder = key_id(shelf._signer)
        for n, entries, anchor in ((0, 3, third), (3, 3, third), (2, 2, second), (1, 1, first)):
            got = check_staple(c.history_card(n), holder)
            assert got.anchor == hashlib.sha256(anchor.checkpoint).hexdigest()
            history = json.loads(got.artifact)["history"]
            assert len(history) == entries and history[0]["consistency_from_previous"] is None
            assert all(item["consistency_from_previous"] for item in history[1:])
        with pytest.raises(NotFoundError):
            c.history_card(4)
        with pytest.raises(InvalidError):
            check_staple(c.history_card(1), "00" * 32)
        with pytest.raises(InvalidError):
            c.history_card(-1)
        # An unfinished newest tick is no anchor; n=0 takes the newest finished one.
        with shelf._tx() as db:
            db.execute("UPDATE cadence_ticks SET checkpoint_size = 0 WHERE tick = 3")
        with pytest.raises(NotFoundError):
            c.history_card(3)
        assert check_staple(c.history_card(), holder).anchor == hashlib.sha256(second.checkpoint).hexdigest()
        with shelf._tx() as db:
            db.execute("UPDATE cadence_ticks SET checkpoint_size = 0 WHERE tick = 1")
            db.execute("UPDATE cadence_ticks SET checkpoint_size = 7 WHERE tick = 3")
        with pytest.raises(CorruptError):
            c.history_card(3)


def test_a_legacy_log_joins_the_cadence(fx):
    with fx.open() as shelf:
        shelf.host_log("legacy/x", False)
    with fx.open() as shelf:
        with pytest.raises(InvalidError):
            shelf.legacy("legacy/x")  # outside the cadence
        with pytest.raises(NotFoundError):
            shelf.legacy("legacy/absent")  # never provisioned
        with pytest.raises(InvalidError):
            shelf.open("not a log id")
        shelf.open("deal/a").append(obs())
        with pytest.raises(InvalidError):
            shelf.legacy("deal/a")


def _legacy_cadence(fx):
    """A cadence written by one shelf over a legacy log, read back as a
    pre-shelf writer's ticks."""
    with fx.open() as shelf:
        c = cadence(shelf, ended=("legacy/x",))
        host = shelf.host_log("legacy/x", True)
        host.write(lambda s: s.log.append(AppendInput(hashlib.sha256(b"x").digest(), START)))
        first = c.tick(START).tick
        witness(c, first)
        ticks = [replace_tick(c.get(1))]
    db = sqlite3.connect(fx.path)
    for table in ("cadence_ticks", "cadence_witnessed", "cadence_live", "cadence_logs", "shelf_logs"):
        db.execute(f"DELETE FROM {table}")
    db.commit()
    db.close()
    return ticks


def replace_tick(tick: CadenceTick) -> CadenceTick:
    return CadenceTick(0, tick.at, tick.due_next, tick.entry_seq, tick.checkpoint_size, tick.checkpoint, tick.leaves,
                       tick.witnessed, tick.receipt)


def test_import_legacy_continues_the_cadence_and_is_rerunnable(fx):
    ticks = _legacy_cadence(fx)
    with fx.open() as shelf:
        c = cadence(shelf, ended=("legacy/x",))
        c.import_legacy(LegacyCadence(["legacy/x"], ticks))
        c.import_legacy(LegacyCadence(["legacy/x"], ticks))
        assert c.last().n == 1 and c.get(1).witnessed
        assert c.live() == []  # ended and witnessed at its size
        with pytest.raises(InvalidError):
            shelf.open("legacy/x")
        second = c.tick(c.last().due_next)
        assert second.tick.n == 2
        other = CadenceTick(0, ticks[0].at, ticks[0].due_next, ticks[0].entry_seq, ticks[0].checkpoint_size,
                            b"other", ticks[0].leaves)
        with pytest.raises(InvalidError):
            c.import_legacy(LegacyCadence(["legacy/x"], [other]))
        with pytest.raises(InvalidError):
            c.import_legacy(LegacyCadence(["deal-cadence/0000000000000001"], []))


def test_import_legacy_refuses_a_receipt_that_does_not_verify(fx):
    ticks = _legacy_cadence(fx)
    bad = ticks[0].receipt
    forged = replace_tick(ticks[0])
    forged = CadenceTick(0, forged.at, forged.due_next, forged.entry_seq, forged.checkpoint_size, forged.checkpoint,
                         forged.leaves, True, WitnessReceipt(bad.ts_url, "00" * 32, bad.receipt, 0, 1))
    with fx.open() as shelf:
        c = cadence(shelf, ended=("legacy/x",))
        with pytest.raises(InvalidError, match="INVALID"):
            c.import_legacy(LegacyCadence(["legacy/x"], [forged]))
        assert c.last() is None


def test_a_record_with_field_commitments_opens(fx):
    """A book Go wrote with field commitments (x-deal-v0 steps) opens and
    lists here: the header keeps them, so its bytes stay canonical."""
    from agent_action_capsule.canonical import jcs

    from evidencebook._capsule import seal_record
    from evidencebook.record import Header
    from evidencebook.store import StoredRecord

    with fx.open() as shelf:
        book = shelf.open("deal/f")
        header = Header(book_id="deal/f", seq=1, record_type="deal_step", epistemic_type=OBSERVED_EVENT,
                        committed_at="2026-10-31T12:00:00Z", retention_state="AVAILABLE",
                        field_commitments=(("amount", "ab" * 32), ("currency", "cd" * 32)), commit_alg="nonce256")
        header_bytes = jcs(header.value())
        assert b'"field_commitments":{"amount"' in header_bytes and b'"commit_alg":"nonce256"' in header_bytes
        record_id, capsule, envelope = seal_record(header, header_bytes, operator="op", developer="eb", at=START,
                                                   signer=SIGNER)
        book._store.put_record(StoredRecord(1, record_id, header_bytes, capsule, envelope))
        book._substrate.append(record_id, START)
    with fx.open() as shelf:
        assert shelf.open("deal/f").get(record_id).header.field_commitments[0] == ("amount", "ab" * 32)
        assert [r.retention for r in shelf.records().records] == ["AVAILABLE"]


def test_a_tick_and_a_chain_run_beside_appends_to_the_same_book(fx):
    """A cadence reads a book's log outside the book's lock; the log's
    mirror keeps its own."""
    with fx.open() as shelf:
        c = cadence(shelf)
        book = shelf.open("deal/a")
        book.append(obs("0"))
        first = c.tick(START).tick
        witness(c, first)
        statement = base64.urlsafe_b64decode(first.leaves.deals["deal/a"].statement + "==")
        errors, stop = [], threading.Event()

        def appends():
            try:
                for n in range(60):
                    book.append(obs(str(n)))
            except Exception as exc:  # noqa: BLE001
                errors.append(exc)
            finally:
                stop.set()

        writer = threading.Thread(target=appends)
        writer.start()
        due = first.due_next
        while not stop.is_set():
            try:
                c.chain(statement)
                result = c.tick(due)
                due = result.due_next
                shelf.records("deal/a")
            except Exception as exc:  # noqa: BLE001
                errors.append(exc)
                break
        writer.join()
        assert not errors
        assert book._substrate.entry_ids() == [r.record_id for r in book.records()]


def test_lifecycle_statements_list_as_go_reads_them(fx):
    from evidencebook.record import CITES, Link

    with fx.open() as shelf:
        book = shelf.open("deal/a")
        first = book.append(obs())
        cites = (Link(CITES, first.record_id),)
        book.append(Entry("lifecycle", "producer_claim", links=cites, statement={}))
        assert shelf.records().records[0].retention == ""
        book.append(Entry("lifecycle", "producer_claim", links=cites, statement={"retention_state": "WITHHELD"}))
        assert shelf.records().records[0].retention == "WITHHELD"
        book.append(Entry("lifecycle", "producer_claim", links=cites, statement={"retention_state": None}))
        assert shelf.records().records[0].retention == ""
        # Go's records fails on a lifecycle record with no statement member,
        # or with one that is not an object.
        for bad in ({}, {"statement": [1]}):
            other = shelf.open(f"deal/bad{len(bad)}")
            cited = other.append(obs())
            other.append(Entry("lifecycle", "producer_claim", links=(Link(CITES, cited.record_id),), **bad))
            with pytest.raises(CorruptError):
                shelf.records(other.book_id)


def test_a_shelf_transaction_holds_the_write_lock_from_its_start(fx):
    """A shelf transaction reads, then writes: under WAL a deferred one fails
    (SQLITE_BUSY_SNAPSHOT) when another connection commits between the two,
    which the busy timeout does not cover. It starts IMMEDIATE, so the other
    writer waits instead."""
    with fx.open() as shelf:
        other = sqlite3.connect(fx.path, timeout=5.0, isolation_level=None, check_same_thread=False)
        errors, started = [], threading.Event()

        def write():
            started.set()
            try:
                other.execute("INSERT INTO cadence_logs (log_id) VALUES ('other/log')")
            except Exception as exc:  # noqa: BLE001
                errors.append(exc)

        with shelf._tx() as db:
            db.execute("SELECT COUNT(*) FROM cadence_logs").fetchone()
            writer = threading.Thread(target=write)
            writer.start()
            started.wait()
            writer.join(0.3)
            db.execute("INSERT INTO cadence_logs (log_id) VALUES ('mine/log')")
        writer.join()
        other.close()
        assert not errors
        with shelf._read() as db:
            assert {r[0] for r in db.execute("SELECT log_id FROM cadence_logs")} == {"other/log", "mine/log"}


def test_a_book_checkpoints_while_another_appends(fx):
    with fx.open() as shelf:
        a, b = shelf.open("deal/a"), shelf.open("deal/b")
        errors = []

        def run(f):
            try:
                for n in range(25):
                    f(n)
            except Exception as exc:  # noqa: BLE001
                errors.append(exc)

        threads = [threading.Thread(target=run, args=(lambda n: (a.append(obs(str(n))), a.checkpoint()),)),
                   threading.Thread(target=run, args=(lambda n: b.append(obs(str(n))),))]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        assert not errors


def test_a_correlation_subject_is_answered_as_go_answers_it(fx):
    from evidencebook.book import _View
    from evidencebook.record import Correlation

    v = json.loads((VECTORS / "correlation.json").read_text())
    with fx.open() as shelf:
        book = shelf.open("deal/a")
        for r in v["records"]:
            book.append(Entry("observation", OBSERVED_EVENT, subject_ref=r["subject_ref"],
                              correlation=Correlation(**r.get("correlation", {})), payloads=(b"x",)))
        view = _View(book)
        assert len(v["answers"]) > 1
        for key, want in v["answers"].items():
            assert [view._header(i).subject_ref for i in view.correlated(key)] == [v["records"][p]["subject_ref"] for p in want], key
