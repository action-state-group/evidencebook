# SPDX-License-Identifier: Apache-2.0
"""The store of what the other side pushes (evidencebook#461 §3), as Go's
shelf_receive_test.go checks it."""
from __future__ import annotations

import dataclasses
import hashlib
import json
import threading
from datetime import timedelta

import pytest

from evidencebook import shelf_receive
from evidencebook._gojson import Ordered
from evidencebook.errors import (BusyError, DeletedError, InvalidError, NotFoundError, OverLimitError, RefusedError,
                                 ReleasedError)
from evidencebook.shelf import SHELF_VERSION
from evidencebook.shelf_facts import FACT_KIND_BOOK, Opening
from evidencebook.shelf_receive import (EVIDENCE_CHECKPOINT, EVIDENCE_CHECKPOINT_RECORDS, EVIDENCE_FORK, CountedPrefix,
                                        ReceiveBounds, ReceiveConfig, ReceivedRecord)
from evidencebook.shelf_stamp_proof import verify_stamp_proof
from evidencebook.cadence import CadenceConfig

from receive_support import START, ReceiveFixture
from test_shelf_stamp import Fixture, obs, script, stamp_cadence


def dg(s: str) -> str:
    return hashlib.sha256(s.encode()).hexdigest()


class Profile:
    """The test profile: JSON whose sig member must be "ok"; offers from
    anyone, other records from the senders it lists; checkpoint keys mapped
    to senders. The hooks run as a profile reading the shelf would."""

    def __init__(self, *senders: str):
        self.mu = threading.Lock()
        self.admitted = {s: True for s in senders}
        self.answered_digests: set[str] = set()
        self.proof_keys: dict[str, str] = {}
        self.reads = 0
        self.on_admit = self.on_answered = self.after_answered = None
        self.proof_err: Exception | None = None

    def read(self, record: bytes) -> ReceivedRecord:
        with self.mu:
            self.reads += 1
        r = json.loads(record)
        if r.get("sig") != "ok":
            raise ValueError("bad signature")
        return ReceivedRecord(r.get("digest", ""), r.get("sender", ""), r.get("log", ""), r.get("pos", 0),
                              r.get("prev", ""), r.get("deal", ""), r.get("offer", False))

    def admit(self, r: ReceivedRecord) -> bool:
        if self.on_admit:
            self.on_admit(r)
        with self.mu:
            return r.offer or self.admitted.get(r.sender_key, False)

    def answered(self, r: ReceivedRecord) -> bool:
        if self.on_answered:
            self.on_answered(r)
        with self.mu:
            answered = r.digest in self.answered_digests
        if self.after_answered:
            self.after_answered(r)
        return answered

    def proof_sender(self, key_id: str, log_id: str) -> tuple[str, bool]:
        with self.mu:
            if self.proof_err is not None:
                raise self.proof_err
            sender = self.proof_keys.get(key_id)
            return (sender or "", sender is not None)


def rec(digest: str, sender: str, log: str, pos: int, deal: str, prev: str = "", offer: bool = False,
        pad: str = "", sig: str = "ok") -> bytes:
    r = {"sig": sig, "digest": digest, "sender": sender, "log": log, "pos": pos, "prev": prev, "deal": deal,
         "offer": offer}
    if pad:
        r["pad"] = pad
    return json.dumps(r, separators=(",", ":")).encode()


def chain(sender: str, log: str, deal: str, pos: int, name: str, prev_name: str = "") -> bytes:
    return rec(dg(name), sender, log, pos, deal, dg(prev_name) if prev_name else "")


def offer(sender: str, name: str, pad: int = 0) -> bytes:
    return rec(dg(name), sender, "offers/" + name, 1, "offer-" + name, offer=True, pad="x" * pad)


@pytest.fixture
def make(tmp_path):
    made = []

    def new(bounds: ReceiveBounds = ReceiveBounds(), retention: timedelta = timedelta(0), senders=("alice",)):
        directory = tmp_path / f"r{len(made)}"
        directory.mkdir()
        profile = Profile(*senders)
        fx = ReceiveFixture(directory, ReceiveConfig(profile, bounds, retention))
        fx.profile = profile
        made.append(fx)
        return fx, fx.open()

    yield new
    for fx in made:
        fx.close()


def count(s, table: str = "shelf_received") -> int:
    with s._read() as db:
        return db.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]


def ok(s, record=None, proof=None):
    got = s.receive(record, proof)
    assert got.refusals == [], got.refusals
    return got


def within_time(f) -> None:
    """Fail when ``f`` does not return: a deadlock."""
    errors = []

    def run():
        try:
            f()
        except BaseException as exc:  # noqa: BLE001 -- reported below
            errors.append(exc)

    t = threading.Thread(target=run, daemon=True)
    t.start()
    t.join(20)
    assert not t.is_alive(), "deadlock: the call did not return"
    if errors:
        raise errors[0]


# -- records --------------------------------------------------------------------------


def test_a_duplicate_is_acknowledged_and_not_stored_again(make):
    _, s = make()
    r1 = chain("alice", "deal/a", "deal-1", 1, "a1")
    got = ok(s, r1)
    assert got.record.stored and got.record.counted
    assert got.changes.grew == ["deal-1"]
    again = ok(s, r1)
    assert not again.record.stored and again.record.duplicate and again.record.counted
    assert again.changes.grew == []
    assert count(s) == 1
    # Another record under a held digest is refused.
    other = rec(dg("a1"), "alice", "deal/a", 1, "deal-1", pad="x")
    assert isinstance(s.receive(other).record.refused, InvalidError)


def test_a_gap_is_stored_not_counted_and_its_fill_joins_the_prefix(make):
    fx, s = make()
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    got = ok(s, chain("alice", "deal/a", "deal-1", 3, "a3", "a2"))
    assert got.record.stored and not got.record.counted
    assert got.record.ask_for_book, "a record past the counted prefix asks for the book"
    assert got.changes.grew == []
    ok(s, chain("alice", "deal/a", "deal-1", 5, "a5", "a4"))
    assert s.counted_prefix("alice", "deal/a") == CountedPrefix(1, dg("a1"))
    fill = ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    assert fill.record.counted and fill.changes.grew == ["deal-1"]
    assert s.counted_prefix("alice", "deal/a") == CountedPrefix(3, dg("a3")), "the fill joins up to the next gap"
    # A restart recomputes the same prefix from the store.
    s.release()
    reopened = fx.open()
    assert reopened.counted_prefix("alice", "deal/a") == CountedPrefix(3, dg("a3"))
    again = ok(reopened, chain("alice", "deal/a", "deal-1", 4, "a4", "a3"))
    assert again.changes.grew == ["deal-1"]
    assert reopened.counted_prefix("alice", "deal/a") == CountedPrefix(5, dg("a5"))


def test_a_fork_keeps_both_and_ends_the_prefix_for_good(make):
    _, s = make()
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    fork = ok(s, chain("alice", "deal/a", "deal-1", 2, "a2-other", "a1"))
    assert fork.record.stored, "both records of a fork are kept"
    assert not fork.record.counted
    assert fork.changes.forked == ["deal-1"] and fork.changes.shrank == ["deal-1"]
    assert count(s) == 3
    assert s.counted_prefix("alice", "deal/a") == CountedPrefix(1, dg("a1"), 2)
    evidence = s.evidence()
    assert len(evidence) == 1 and evidence[0].kind == EVIDENCE_FORK
    assert dg("a2").encode() in evidence[0].a and dg("a2-other").encode() in evidence[0].b
    # For good: records after the fork never join the prefix.
    ok(s, chain("alice", "deal/a", "deal-1", 3, "a3", "a2"))
    assert s.counted_prefix("alice", "deal/a").size == 1


def test_a_late_fork_shrinks_the_prefix(make):
    _, s = make()
    for i, name in enumerate(("a1", "a2", "a3")):
        ok(s, chain("alice", "deal/a", "deal-1", i + 1, name, ("", "a1", "a2")[i]))
    assert s.counted_prefix("alice", "deal/a").size == 3
    # A record at 4 naming another predecessor than the one held at 3 is a fork at 3.
    late = ok(s, chain("alice", "deal/a", "deal-1", 4, "a4", "a3-other"))
    assert late.changes.forked == ["deal-1"] and late.changes.shrank == ["deal-1"]
    assert s.counted_prefix("alice", "deal/a") == CountedPrefix(2, dg("a2"), 3)
    evidence = s.evidence()
    assert len(evidence) == 1 and evidence[0].kind == EVIDENCE_FORK


def test_refuses_a_sender_and_stores_nothing(make):
    _, s = make()
    got = s.receive(chain("mallory", "deal/m", "deal-m", 1, "m1"))
    assert isinstance(got.record.refused, RefusedError) and not got.record.stored
    assert count(s) == 0
    with pytest.raises(NotFoundError):
        s.received_record_of(dg("m1"))
    # A record the profile cannot read (a bad signature) is invalid.
    bad = rec(dg("x"), "alice", "deal/a", 1, "d", sig="forged")
    assert isinstance(s.receive(bad).record.refused, InvalidError)
    # A record that names no predecessor past position 1 is invalid.
    assert isinstance(s.receive(rec(dg("y"), "alice", "deal/a", 2, "d")).record.refused, InvalidError)
    with pytest.raises(InvalidError):
        s.receive(None, None)
    assert count(s) == 0


def test_receive_without_a_profile_is_refused(tmp_path):
    fx = ReceiveFixture(tmp_path)
    try:
        with pytest.raises(InvalidError):
            fx.open().receive(b"{}")
    finally:
        fx.close()


def test_refuses_an_oversize_record_before_reading_it(make):
    fx, s = make(ReceiveBounds(max_record_bytes=64))
    got = s.receive(rec(dg("a1"), "alice", "deal/a", 1, "deal-1", pad="x" * 64))
    assert isinstance(got.record.refused, OverLimitError) and not got.record.stored
    assert fx.profile.reads == 0
    assert count(s) == 0


def test_refuses_a_record_past_the_per_book_bound(make):
    _, s = make(ReceiveBounds(max_records_per_log=2))
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    assert isinstance(s.receive(chain("alice", "deal/a", "deal-1", 3, "a3", "a2")).record.refused, OverLimitError)
    assert count(s) == 2
    # Another log of the same sender has its own bound.
    ok(s, chain("alice", "deal/b", "deal-2", 1, "b1"))


def held(s, name: str) -> bool:
    try:
        s.received_record_of(dg(name))
    except NotFoundError:
        return False
    return True


def test_offer_bounds_drop_the_oldest_unanswered_offer(make):
    fx, s = make(ReceiveBounds(max_offers_per_sender=2, max_offers=3, max_offer_bytes_per_sender=1000))
    for name in ("o1", "o2", "o3"):
        assert ok(s, offer("bob", name)).record.stored  # offers come from any key
    assert not held(s, "o1"), "the oldest unanswered offer is dropped at the per-sender bound"
    with pytest.raises(NotFoundError):  # a dropped offer leaves no tombstone
        s.received_record_of(dg("o1"))
    assert held(s, "o2") and held(s, "o3")
    # An answered offer leaves the bounds and is never dropped.
    fx.profile.answered_digests.add(dg("o2"))
    ok(s, offer("bob", "o4"))
    assert held(s, "o2") and held(s, "o3") and held(s, "o4")
    # The total bound drops the oldest unanswered offer of any sender.
    ok(s, offer("carol", "c1"))
    assert held(s, "o3")
    ok(s, offer("carol", "c2"))
    assert not held(s, "o3")
    assert all(held(s, n) for n in ("o2", "o4", "c1", "c2"))
    # An offer over a byte bound alone is refused.
    assert isinstance(s.receive(offer("dave", "d1", 1000)).record.refused, OverLimitError)
    assert not held(s, "d1")


def test_offer_bytes_bound_drops_the_oldest(make):
    one = len(offer("bob", "o1", 150))
    _, s = make(ReceiveBounds(max_offer_bytes_per_sender=2 * one + 10))
    ok(s, offer("bob", "o1", 150))
    ok(s, offer("bob", "o2", 150))
    assert held(s, "o1")
    ok(s, offer("bob", "o3", 150))
    assert not held(s, "o1")


# -- stamp proofs ---------------------------------------------------------------------


class Holder:
    """A holder's shelf with one deal book, migrated and stamped: the side
    that pushes records and stamp proofs."""

    def __init__(self, directory, *payloads: str):
        directory.mkdir()
        self.fx = Fixture(directory)
        self.shelf = self.fx.open()
        self.cadence, self.values = stamp_cadence(self.shelf)
        self.values.status["deal/a"] = {"row": "open"}
        book = self.shelf.open("deal/a")
        for p in payloads:
            book.append(obs("a", p))
        self.cadence.tick(START)
        self.cadence.migrate(START + timedelta(minutes=1))
        self.book_id = json.loads(self.proof().fact)["book_id"]

    def proof(self, audience: str = "counterparty"):
        with self.shelf._read() as db:
            book_id = db.execute("SELECT book_id FROM shelf_entries WHERE log_id = 'deal/a' AND kind = ? LIMIT 1",
                                 (FACT_KIND_BOOK,)).fetchone()[0]
        return self.cadence.stamp_proof(book_id, 0, audience)

    def records(self, sender: str, deal: str) -> list[bytes]:
        out, prev = [], ""
        with self.shelf._read() as db:
            rows = db.execute("SELECT seq, value FROM cll_entries WHERE log_id = 'deal/a' ORDER BY seq").fetchall()
        for seq, value in rows:
            digest = bytes(value).hex()
            out.append(rec(digest, sender, "deal/a", seq, deal, prev))
            prev = digest
        return out

    def close(self) -> None:
        self.shelf.release()
        self.fx.close()


@pytest.fixture
def holder(tmp_path):
    made = []

    def new(*payloads: str) -> Holder:
        h = Holder(tmp_path / f"h{len(made)}", *payloads)
        made.append(h)
        return h

    yield new
    for h in made:
        h.close()


def key_of(p) -> str:
    return verify_stamp_proof(p).key_id


def test_stamp_proof_verifies_and_hands_openings_by_audience(holder):
    h = holder("1", "2")
    p = h.proof()
    v = verify_stamp_proof(p)
    assert v.fact["log_id"] == "deal/a"
    assert v.book_checkpoint.key_id == v.shelf_checkpoint.key_id
    assert p.status_opening is not None and p.status_opening.value == {"row": "open"}
    assert h.proof("chosen_reader").status_opening is not None
    assert h.proof("adjudicator").status_opening is None
    with pytest.raises(InvalidError):
        h.cadence.stamp_proof(h.book_id, 0, "stranger")
    with pytest.raises(NotFoundError):
        h.cadence.stamp_proof(dg("no such book"), 0, "counterparty")
    tampers = {
        "fact": lambda q: dataclasses.replace(q, fact=q.fact[:-1] + b" }"),
        "opening": lambda q: dataclasses.replace(q, status_opening=Opening(q.status_opening.salt, {"row": "closed"})),
        "inclusion": lambda q: dataclasses.replace(q, inclusion=Ordered(q.inclusion, seq=q.inclusion["seq"] + 1)),
        "book": lambda q: dataclasses.replace(q, book_checkpoint=q.shelf_checkpoint),
        "shelf": lambda q: dataclasses.replace(q, shelf_checkpoint=q.book_checkpoint),
    }
    for name, tamper in tampers.items():
        with pytest.raises(InvalidError):
            verify_stamp_proof(tamper(h.proof()))
            pytest.fail(name)
    verify_stamp_proof(dataclasses.replace(h.proof(), status_opening=None))  # no opening


def test_a_valid_proof_covers_the_records(make, holder):
    h = holder("1", "2", "3")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    for r in h.records("alice", "deal-1"):
        ok(s, r)
    got = ok(s, None, p)
    assert got.proof.accepted and got.changes.covered == ["deal-1"]
    assert count(s, "shelf_received_proofs") == 1
    # The same proof again is no contradiction, and is not kept twice.
    again = ok(s, None, p)
    assert again.proof.duplicate and not again.proof.accepted and again.changes.covered == []
    assert count(s, "shelf_received_proofs") == 1
    assert s.evidence() == []
    # A proof from a key the profile does not relate to a sender is refused.
    del fx.profile.proof_keys[key_of(p)]
    assert isinstance(s.receive(None, p).proof.refused, RefusedError)


def test_a_duplicate_record_still_delivers_the_proof_it_lacked(make, holder):
    h = holder("1")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    records = h.records("alice", "deal-1")
    for r in records:
        ok(s, r)
    got = ok(s, records[-1], p)
    assert got.record.duplicate and got.proof.accepted
    assert count(s, "shelf_received_proofs") == 1
    # A refused record does not stop its proof either.
    fx.profile.admitted["alice"] = False
    got = s.receive(chain("alice", "deal/z", "deal-z", 1, "z1"), h.proof("chosen_reader"))
    assert [type(e) for e in got.refusals] == [RefusedError]
    assert isinstance(got.record.refused, RefusedError)
    assert got.proof.duplicate


def test_a_contradicting_proof_is_refused_and_kept_as_evidence(make, holder):
    h, other = holder("1", "2"), holder("x", "y")  # the same key, another book at the same size
    fx, s = make()
    p, q = h.proof(), other.proof()
    assert key_of(p) == key_of(q)
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)
    got = s.receive(None, q)
    assert isinstance(got.proof.refused, InvalidError) and not got.proof.accepted
    assert count(s, "shelf_received_proofs") == 1
    evidence = s.evidence()
    assert len(evidence) == 1 and evidence[0].kind == EVIDENCE_CHECKPOINT
    assert evidence[0].a == p.book_checkpoint and evidence[0].b == q.book_checkpoint
    # The held proof survives a restart, and the contradiction is found
    # against it again, its evidence kept once.
    s.release()
    reopened = fx.open()
    assert isinstance(reopened.receive(None, q).proof.refused, InvalidError)
    assert len(reopened.evidence()) == 1


def test_a_proof_the_records_held_do_not_recompute_is_refused(make, holder):
    h, other = holder("1", "2"), holder("x", "y")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    for r in other.records("alice", "deal-1"):
        ok(s, r)
    got = s.receive(None, p)
    assert isinstance(got.proof.refused, InvalidError)
    assert got.changes.forked == ["deal-1"]
    assert count(s, "shelf_received_proofs") == 0
    evidence = s.evidence()
    assert len(evidence) == 1 and evidence[0].kind == EVIDENCE_CHECKPOINT_RECORDS


def test_records_that_contradict_a_held_proof_are_evidence(make, holder):
    h, other = holder("1", "2"), holder("x", "y")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)  # before any record: nothing to check it against
    last = None
    for r in other.records("alice", "deal-1"):
        last = ok(s, r)
    assert last.changes.forked == ["deal-1"]
    evidence = s.evidence()
    assert len(evidence) == 1 and evidence[0].kind == EVIDENCE_CHECKPOINT_RECORDS


def test_one_book_checkpoint_under_two_shelf_checkpoints_keeps_both(make, holder):
    h = holder("1")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)
    # A received-only change: a new fact at the same size and checkpoint,
    # under a new shelf checkpoint.
    h.values.status["deal/a"] = {"row": "agreed"}
    h.cadence.stamp(START + timedelta(minutes=2))
    q = h.proof()
    assert p.book_checkpoint == q.book_checkpoint and p.shelf_checkpoint != q.shelf_checkpoint
    got = ok(s, None, q)
    assert got.proof.accepted
    assert got.changes.covered == [], "the same size covers no new record"
    assert count(s, "shelf_received_proofs") == 2


# -- Forget ---------------------------------------------------------------------------


def test_forget_waits_for_the_deal_to_settle_and_leaves_tombstones(make):
    fx, s = make(retention=timedelta(hours=1))
    r1 = chain("alice", "deal/a", "deal-1", 1, "a1")
    ok(s, r1)
    ok(s, chain("alice", "deal/b", "deal-2", 1, "b1"))
    later = fx.clock.t + timedelta(hours=2)
    settled = [False]

    def is_settled(deal: str) -> bool:
        return deal == "deal-1" and settled[0]

    with pytest.raises(InvalidError):  # nothing of a deal is deleted before it is settled
        s.forget("deal-1", later, is_settled)
    assert count(s) == 2
    settled[0] = True
    assert s.forget("deal-1", fx.clock.t, is_settled) == [], "records younger than the retention stay"
    assert s.forget("deal-1", later, is_settled) == [dg("a1")]
    with pytest.raises(DeletedError):  # deleted, not missing
        s.received_record_of(dg("a1"))
    s.received_record_of(dg("b1"))
    # Sent again, it is acknowledged as deleted, not stored again.
    again = ok(s, r1)
    assert again.record.deleted and not again.record.stored
    assert count(s) == 1


def test_forget_is_refused_when_the_holder_keeps_records_for_the_shelfs_life(make):
    fx, s = make()
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    with pytest.raises(InvalidError):
        s.forget("deal-1", fx.clock.t + timedelta(hours=1000), lambda _: True)
    assert count(s) == 1


def test_receive_works_on_a_version_one_shelf(make):
    _, s = make()
    with s._read() as db:
        assert db.execute("SELECT version FROM shelf_meta").fetchone()[0] == SHELF_VERSION
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))


def test_receive_refuses_after_release(make):
    _, s = make()
    s.release()
    with pytest.raises(ReleasedError):
        s.receive(chain("alice", "deal/a", "deal-1", 1, "a1"))
    with pytest.raises(ReleasedError):
        s.counted_prefix("alice", "deal/a")


# -- review findings ------------------------------------------------------------------


def test_received_store_readers_run_inside_a_stamp(make):
    """P1-1: the store's readers take no lock of the shelf's, so a status
    provider calls them during a stamp, which holds the shelf's mutex; here
    the stamp runs in another thread than the shelf was opened in."""
    _, s = make()
    reads = [0]

    def status(book_id: str, log_id: str):
        p = s.counted_prefix("alice", "deal/a")
        s.evidence()
        s.received_record_of(dg("a1"))
        reads[0] += 1
        link = {"last_received": None, "checked_over": []}
        if p.size:
            link["last_received"] = {"log_id": "deal/a", "size": p.size, "record_digest": p.last}
        return "deal", link, {}

    c = s.cadence(CadenceConfig(log_id="deal-cadence/0123456789abcdef", ended=lambda _: False,
                                witnesses=script.DIRECTORY, status=status))
    s.open("deal/ours").append(obs("a", "1"))
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    c.tick(START)

    def stamp():
        c.migrate(START + timedelta(minutes=1))
        s.open("deal/ours").append(obs("a", "2"))
        c.stamp(START + timedelta(minutes=2))

    within_time(stamp)
    assert reads[0] > 0


def test_an_answer_committed_before_the_drop_keeps_the_offer(make):
    """P1-2: an answer committed between a pushed offer's read and the drop
    keeps the answered offer; a profile reading the shelf while asked does
    not deadlock."""
    fx, s = make(ReceiveBounds(max_offers_per_sender=1))
    ok(s, offer("bob", "o1"))

    def on_admit(r):
        if r.digest == dg("o2"):
            with fx.profile.mu:
                fx.profile.answered_digests.add(dg("o1"))

    asked = [0]

    def on_answered(_r):
        asked[0] += 1
        s.counted_prefix("bob", "offers/o1")
        s.received_record_of(dg("o1"))

    fx.profile.on_admit, fx.profile.on_answered = on_admit, on_answered
    within_time(lambda: ok(s, offer("bob", "o2")))
    assert asked[0] == 1
    s.received_record_of(dg("o1"))  # the answered offer is kept
    s.received_record_of(dg("o2"))


def test_a_fork_survives_forget_and_restart(make):
    """P1-3: a fork's position ends the counted prefix for good, whatever is
    deleted later, also after a restart."""
    fx, s = make(retention=timedelta(hours=1))
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    ok(s, chain("alice", "deal/a", "deal-x", 2, "a2-other", "a1"))
    want = CountedPrefix(1, dg("a1"), 2)
    assert s.counted_prefix("alice", "deal/a") == want
    assert s.forget("deal-x", fx.clock.t + timedelta(hours=2), lambda _: True) == [dg("a2-other")]
    assert s.counted_prefix("alice", "deal/a") == want, "deleting one record of a fork does not undo it"
    s.release()
    assert fx.open().counted_prefix("alice", "deal/a") == want


def test_repeated_forks_at_one_position_keep_one_evidence_row(make):
    """P1-4."""
    _, s = make()
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    assert ok(s, chain("alice", "deal/a", "deal-1", 2, "a2-b", "a1")).changes.forked == ["deal-1"]
    for name in ("a2-c", "a2-d", "a2-e"):
        assert ok(s, chain("alice", "deal/a", "deal-1", 2, name, "a1")).changes.forked == [], \
            "a fork already recorded at the position is not news"
    assert len(s.evidence()) == 1


def test_a_proof_past_the_records_grants_no_coverage_until_they_recompute_it(make, holder):
    """P1-5: once the records arrive, a smaller proof they contradict is
    evidence and the larger one they recompute covers them."""
    small, large = holder("1", "2"), holder("x", "y", "z")
    fx, s = make()
    a, b = small.proof(), large.proof()
    fx.profile.proof_keys[key_of(a)] = "alice"
    assert ok(s, None, a).proof.accepted
    got = ok(s, None, b)
    assert got.proof.accepted and got.changes.covered == [], "a proof past the records held grants no coverage"
    covered, forked = [], []
    for r in large.records("alice", "deal-1"):
        got = ok(s, r)
        covered += got.changes.covered
        forked += got.changes.forked
    assert covered == ["deal-1"] and forked == ["deal-1"]
    evidence = s.evidence()
    assert len(evidence) == 1 and evidence[0].kind == EVIDENCE_CHECKPOINT_RECORDS
    assert evidence[0].a == a.book_checkpoint


def test_a_proof_error_keeps_the_records_outcome(make, holder):
    """P2-6: a store or profile error in the proof part still returns the
    record part's committed outcome and changes."""
    h = holder("1")
    fx, s = make()
    boom = RuntimeError("profile store down")
    fx.profile.proof_err = boom
    with pytest.raises(RuntimeError) as caught:
        s.receive(chain("alice", "deal/a", "deal-1", 1, "a1"), h.proof())
    assert caught.value is boom
    got = caught.value.receive_result
    assert got.record is not None and got.record.stored
    assert got.proof is None
    assert got.changes.grew == ["deal-1"]


def test_a_proof_held_first_covers_when_a_gap_is_filled_last(make, holder):
    """P2-7."""
    h = holder("1", "2", "3")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    assert ok(s, None, p).changes.covered == []
    records = h.records("alice", "deal-1")
    assert len(records) > 2
    for i, r in enumerate(records):
        if i != 1:
            assert ok(s, r).changes.covered == []
    got = ok(s, records[1])
    assert got.changes.covered == ["deal-1"] and got.changes.grew == ["deal-1"]


def test_a_dropped_offer_is_reported_shrank_only(make):
    """P2-8: a dropped offer's deal shrank; it never grew, also when dropping
    it leaves one record where two forked."""
    _, s = make(ReceiveBounds(max_offers_per_sender=1))
    ok(s, offer("bob", "o1"))
    got = ok(s, offer("bob", "o2"))
    assert got.changes.shrank == ["offer-o1"] and got.changes.grew == ["offer-o2"]
    _, s = make(ReceiveBounds(max_offers_per_sender=2))
    for name in ("f1", "f2"):
        ok(s, rec(dg(name), "bob", "offers/shared", 1, "offer-" + name, offer=True))
    got = ok(s, offer("bob", "o3"))
    assert "offer-f1" not in got.changes.grew and "offer-f2" not in got.changes.grew
    assert s.counted_prefix("bob", "offers/shared").fork_at == 1


def test_grew_names_only_deals_whose_records_were_counted(make):
    """P3-9."""
    _, s = make()
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-2", 3, "a3", "a2"))
    ok(s, chain("alice", "deal/a", "deal-3", 5, "a5", "a4"))
    got = ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    assert got.changes.grew == ["deal-1", "deal-2"], "deal-3 is past the next gap"
    assert got.changes.shrank == []


def test_a_proof_does_not_cover_records_replacing_deleted_ones(make, holder):
    """R2-1: a proof verified against records Forget deleted grants no
    coverage to other records arriving at the deleted positions: they are a
    fork. A dropped offer's position is remembered the same way."""
    h, other = holder("1", "2"), holder("x", "y")
    fx, s = make(retention=timedelta(hours=1))
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    for r in h.records("alice", "deal-1"):
        ok(s, r)
    assert ok(s, None, p).changes.covered == ["deal-1"]
    s.forget("deal-1", fx.clock.t + timedelta(hours=2), lambda _: True)
    covered, forked = [], []
    for r in other.records("alice", "deal-2"):
        got = ok(s, r)
        covered += got.changes.covered
        forked += got.changes.forked
    assert covered == [], "the proof was checked against records no longer held"
    assert "deal-2" in forked
    assert s.counted_prefix("alice", "deal/a").fork_at == 1, "another record at a deleted position is a fork"

    _, s2 = make(ReceiveBounds(max_offers_per_sender=1))
    ok(s2, rec(dg("o1"), "bob", "offers/x", 1, "offer-o1", offer=True))
    ok(s2, offer("bob", "o2"))  # drops o1
    ok(s2, rec(dg("o1-other"), "bob", "offers/x", 1, "offer-o3", offer=True))
    assert s2.counted_prefix("bob", "offers/x").fork_at == 1


def test_offer_forks_across_rotated_logs_keep_bounded_evidence(make):
    """R2-2: a sender rotating log ids, each with two conflicting offers at
    position 1, keeps no more evidence than the offer evidence bounds allow;
    each fork still ends its log's counted prefix."""
    _, s = make(ReceiveBounds(max_offers_per_sender=4, max_offer_evidence_per_sender=2, max_offer_evidence=3))

    def push(sender: str, log: str, name: str) -> None:
        ok(s, rec(dg(name), sender, log, 1, "offer-" + name, offer=True, pad="x" * 100))

    for i in range(6):
        log = "offers/rot-" + "abcdef"[i]
        push("bob", log, log + "-1")
        push("bob", log, log + "-2")
        assert s.counted_prefix("bob", log).fork_at == 1
    for i in range(3):
        log = "offers/carol-" + "abc"[i]
        push("carol", log, log + "-1")
        push("carol", log, log + "-2")
    evidence = s.evidence()
    assert len(evidence) == 3, "two of bob's, one of carol's, then the total bound"
    assert sum(len(e.a) + len(e.b) for e in evidence) <= 3 * 2 * 600
    with s._read() as db:
        vacated = db.execute("SELECT COUNT(*) FROM shelf_received_vacated").fetchone()[0]
        conflicts = db.execute("SELECT COUNT(*) FROM shelf_received_conflicts").fetchone()[0]
        bobs = db.execute("SELECT (SELECT COUNT(*) FROM shelf_received_vacated WHERE sender_key = 'bob') + "
                          "(SELECT COUNT(*) FROM shelf_received_conflicts WHERE sender_key = 'bob')").fetchone()[0]
    assert vacated + conflicts <= 3
    assert bobs <= 2
    # A fork kept within the bounds ends its log's prefix for good, though
    # both its offers were dropped; one past them is forgotten once its
    # offers are dropped.
    with pytest.raises(NotFoundError):
        s.received_record_of(dg("offers/rot-a-1"))
    assert s.counted_prefix("bob", "offers/rot-a").fork_at == 1
    with pytest.raises(NotFoundError):
        s.received_record_of(dg("offers/rot-c-2"))
    assert s.counted_prefix("bob", "offers/rot-c").fork_at == 0


def test_receive_retries_the_drop_when_the_journal_changes_under_it(make):
    """R2-3: answered never runs inside Receive's transaction. A journal
    reply appended after answered said no, and before the transaction, moves
    the mark: the drop is planned again, keeps the answered offer, and the
    append does not wait on Receive's write lock."""
    fx, s = make(ReceiveBounds(max_offers_per_sender=1))
    c, _ = stamp_cadence(s)
    s.open("deal/ours").append(obs("a", "1"))
    c.tick(START)
    c.migrate(START + timedelta(minutes=1))
    journal = s.journal()
    ok(s, offer("bob", "o1"))
    asked = [0]
    append_errors = []

    def after_answered(r):
        asked[0] += 1
        if asked[0] == 1:
            try:  # our reply to o1, committed after answered said no
                journal.append(obs("reply", r.digest))
            except Exception as exc:  # noqa: BLE001 -- asserted below
                append_errors.append(exc)
            with fx.profile.mu:
                fx.profile.answered_digests.add(r.digest)

    fx.profile.after_answered = after_answered
    within_time(lambda: ok(s, offer("bob", "o2")))
    assert append_errors == [], "the journal append does not wait on Receive"
    assert asked[0] == 2, "the drop was planned again"
    s.received_record_of(dg("o1"))  # the answered offer is kept


def test_receive_refuses_busy_when_the_mark_keeps_moving(make):
    """The third stale mark refuses the offer as busy, storing nothing (Go's
    offerAttempts)."""
    fx, s = make(ReceiveBounds(max_offers_per_sender=1))
    c, _ = stamp_cadence(s)
    s.open("deal/ours").append(obs("a", "1"))
    c.tick(START)
    c.migrate(START + timedelta(minutes=1))
    journal = s.journal()
    ok(s, offer("bob", "o1"))
    fx.profile.after_answered = lambda r: journal.append(obs("reply", dg(str(count(s, "book_records")))))
    got = s.receive(offer("bob", "o2"))
    assert isinstance(got.record.refused, BusyError)
    assert not held(s, "o2") and held(s, "o1")


def test_a_failed_proof_commit_reports_no_proof_changes(make, holder, monkeypatch):
    """R2-4: a proof part whose transaction does not commit adds no changes;
    the committed record part's changes stay."""
    h = holder("1", "2")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    records = h.records("alice", "deal-1")
    for r in records[:-1]:
        ok(s, r)

    class Cancelled(Exception):
        pass

    def before_commit(part: str) -> None:
        if part == "proof":
            raise Cancelled()

    monkeypatch.setattr(shelf_receive, "_before_commit", before_commit)
    with pytest.raises(Cancelled) as caught:
        s.receive(records[-1], p)
    got = caught.value.receive_result
    assert got.changes.grew == ["deal-1"], "the record part committed"
    assert got.changes.covered == [], "the proof part did not"
    assert count(s, "shelf_received_proofs") == 0


def test_the_inclusion_is_stored_as_its_canonical_json(make, holder):
    """The proof's inclusion is kept as its RFC 8785 bytes, whatever member
    order it arrived in."""
    from agent_action_capsule.canonical import jcs

    h = holder("1")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    reordered = dataclasses.replace(p, inclusion=Ordered(reversed(list(p.inclusion.items()))))
    ok(s, None, reordered)
    with s._read() as db:
        stored = bytes(db.execute("SELECT inclusion_proof FROM shelf_received_proofs").fetchone()[0])
    assert stored == jcs(dict(p.inclusion))


# -- held readers (Go shelf_held_test.go) -----------------------------------------------


def test_held_first_returns_every_record_at_position_one_across_senders(make):
    fx, s = make(senders=("alice", "bob"))
    assert s.held_first("deal/a") == []
    ok(s, chain("bob", "deal/a", "deal-2", 1, "b1"))
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    ok(s, chain("alice", "deal/b", "deal-3", 1, "c1"))
    # A fork at the genesis: alice's second record at position 1 of deal/a.
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1-other"))
    got = s.held_first("deal/a")
    keys = [(h.received.sender_key, h.received.digest) for h in got]
    for h in got:
        assert h.received.log_id == "deal/a" and h.received.position == 1 and h.record
    want = sorted([("alice", dg("a1")), ("alice", dg("a1-other"))]) + [("bob", dg("b1"))]
    assert keys == want, "both forked records and the other sender's, by sender then digest"
    s.release()
    with pytest.raises(ReleasedError):
        s.held_first("deal/a")


def test_held_by_deal_returns_every_record_of_the_deal(make):
    fx, s = make(senders=("alice", "bob"))
    assert s.held_by_deal("deal-1") == []
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    ok(s, chain("bob", "deal/c", "deal-1", 1, "c1"))
    ok(s, chain("alice", "deal/b", "deal-3", 1, "b1"))
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1-other"))
    keys = [f"{h.received.sender_key} {h.received.log_id} {h.received.position}" for h in s.held_by_deal("deal-1")]
    assert all(h.received.deal_id == "deal-1" for h in s.held_by_deal("deal-1"))
    assert keys == ["alice deal/a 1", "alice deal/a 1", "alice deal/a 2", "bob deal/c 1"]
    s.release()
    with pytest.raises(ReleasedError):
        s.held_by_deal("deal-1")


def test_held_proofs_cover_nothing_after_a_genesis_fork(make, holder):
    # genesis-fork-after-proof (design §7.3): unchecked before the records,
    # recomputed and covering them after, and covering nothing once a fork at
    # position 1 empties the counted prefix, before and after a restart.
    h = holder("1", "2", "3")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)
    got = s.held_proofs("alice", "deal/a")
    assert len(got) == 1 and not got[0].recomputed and not got[0].contradicted and got[0].covers == 0
    records = h.records("alice", "deal-1")
    for r in records:
        ok(s, r)
    held = s.held_proofs("alice", "deal/a")[0]
    assert held.recomputed and held.covers == len(records) == held.verified.book_checkpoint.entries
    assert held.verified.fact["book_id"] == h.book_id
    assert held.verified.key_id == key_of(p)
    assert held.verified.shelf_checkpoint.log_id == h.cadence.log_id
    assert held.proof.fact == p.fact
    ok(s, chain("alice", "deal/a", "deal-1", 1, "other-genesis"))

    def check(shelf):
        got = shelf.held_proofs("alice", "deal/a")
        assert len(got) == 1
        assert got[0].recomputed, "the proof stays recomputed"
        assert got[0].covers == 0, "but covers nothing once the prefix is empty"
        assert got[0].verified.fact["book_id"] == h.book_id
        assert got[0].verified.shelf_checkpoint.log_id == h.cadence.log_id

    check(s)
    s.release()
    check(fx.open())


def test_held_proofs_of_another_sender_or_log_are_none(make, holder):
    h = holder("1")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)
    assert s.held_proofs("bob", "deal/a") == []
    assert s.held_proofs("alice", "deal/b") == []
    s.release()
    with pytest.raises(ReleasedError):
        s.held_proofs("alice", "deal/a")


def test_held_proofs_report_a_contradicted_proof(make, holder):
    h = holder("1", "2")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)
    prev = ""
    for i in range(len(h.records("alice", "deal-1"))):
        name = f"x{i + 1}"
        s.receive(chain("alice", "deal/a", "deal-1", i + 1, name, prev))
        prev = name
    got = s.held_proofs("alice", "deal/a")
    assert len(got) == 1 and got[0].contradicted and not got[0].recomputed and got[0].covers == 0


def test_after_receive_runs_on_every_call(make):
    # Go AfterReceive (#528): on every call, a duplicate and a refusal
    # included, with the deal the profile read each record as; what it raises
    # is the call's, and the parts' result stays committed.
    fx, s = make()
    seen = []
    fail: list[Exception] = []

    def after(result, at):
        r = result.record
        if r is not None:
            seen.append((r.digest, r.deal_id, r.duplicate, r.refused is not None))
        if fail:
            raise fail[0]

    fx.profile.after_receive = after
    r1 = chain("alice", "deal/a", "deal-1", 1, "a1")
    ok(s, r1)
    ok(s, r1)
    got = s.receive(chain("mallory", "deal/m", "deal-m", 1, "m1"))
    assert isinstance(got.record.refused, RefusedError)
    assert seen == [(dg("a1"), "deal-1", False, False), (dg("a1"), "deal-1", True, False),
                    (dg("m1"), "deal-m", False, True)]
    fail.append(RuntimeError("naming failed"))
    with pytest.raises(RuntimeError, match="naming failed") as e:
        s.receive(chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
    assert e.value.receive_result.record.stored, "the parts' result stays committed"
    assert count(s) == 2


def test_held_proofs_read_one_snapshot(make, holder, monkeypatch):
    # The counted prefix and the proofs' states come from one read: a
    # receive that commits between them is not seen by either.
    from evidencebook import shelf_held

    h = holder("1", "2", "3")
    fx, s = make()
    p = h.proof()
    fx.profile.proof_keys[key_of(p)] = "alice"
    ok(s, None, p)
    records = h.records("alice", "deal-1")
    for r in records[:-1]:
        ok(s, r)
    real = shelf_held._prefix
    landed = []

    def prefix_then_write(db, sender, log):
        out = real(db, sender, log)
        if not landed:
            landed.append(True)
            done = []
            t = threading.Thread(target=lambda: done.append(ok(s, records[-1])))
            t.start()
            t.join(20)
            assert done, "the write landed"
        return out

    monkeypatch.setattr(shelf_held, "_prefix", prefix_then_write)
    got = s.held_proofs("alice", "deal/a")
    assert len(got) == 1 and not got[0].recomputed and got[0].covers == 0, "the state before the write, whole"
    monkeypatch.setattr(shelf_held, "_prefix", real)
    after = s.held_proofs("alice", "deal/a")[0]
    assert after.recomputed and after.covers == len(records)


def test_counted_prefix_reads_one_snapshot(make, monkeypatch):
    # evidencebook#543: receives that commit between the read of the records
    # and the read of the recorded forks (the prefix grows to a2, then two
    # records at 5 fork it) are seen whole or not at all; two snapshots would
    # give the old size with the new fork, a state the store never held.
    from evidencebook import shelf_receive

    fx, s = make()
    ok(s, chain("alice", "deal/a", "deal-1", 1, "a1"))
    real = shelf_receive._row
    landed = []

    def row_then_write(values):
        if not landed:
            landed.append(True)
            ok(s, chain("alice", "deal/a", "deal-1", 2, "a2", "a1"))
            ok(s, chain("alice", "deal/a", "deal-1", 5, "a5", "a4"))
            ok(s, chain("alice", "deal/a", "deal-1", 5, "a5-other", "a4"))
        return real(values)

    monkeypatch.setattr(shelf_receive, "_row", row_then_write)
    got = s.counted_prefix("alice", "deal/a")
    monkeypatch.setattr(shelf_receive, "_row", real)
    assert landed
    assert (got.size, got.last, got.fork_at) == (1, dg("a1"), 0), "the state before the receives"
    after = s.counted_prefix("alice", "deal/a")
    assert (after.size, after.last, after.fork_at) == (2, dg("a2"), 5), "and after them"
