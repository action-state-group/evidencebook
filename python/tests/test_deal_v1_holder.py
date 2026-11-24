# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 receive profile and act gate (evidencebook#461 PR 6d-3a),
held to Go by testdata/deal-v1/holder/transcripts.json: each scenario Go's
TestHolderTranscripts recorded, replayed step by step on Python shelves
with the same keys, clock and random stream, each output compared with
Go's (the pairs and proofs a party seals, byte for byte, and every answer of
the shelf and the profile)."""
from __future__ import annotations

import hashlib
import json
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs
from capsule_emit.artifact.sqlite import SqliteArtifactStore

from evidencebook.cadence import CadenceConfig
from evidencebook.deal.v1 import holder, naming as v1_name_rulings, sent
from evidencebook.deal import v1
from evidencebook.deal.v1.check import decode
from evidencebook.deal.v1.status_provider import StatusProvider
from evidencebook.record import CITES, OBSERVED_EVENT, Entry, Link
from evidencebook.shelf import Shelf
from evidencebook.shelf_receive import ReceiveConfig
from evidencebook.shelf_stamp_proof import StampProof
from evidencebook.signer import Ed25519Signer
from evidencebook._gojson import Ordered
from evidencebook._random import deterministic_stream

from test_shelf_stamp import script

TRANSCRIPTS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "holder" / "transcripts.json"
CASES = json.loads(TRANSCRIPTS.read_text())["transcripts"]
CADENCE_LOG = "deal-cadence/0123456789abcdef"
START = datetime(2026, 11, 1, 9, 0, 0, tzinfo=timezone.utc)


def seed(label: str) -> bytes:
    return hashlib.sha256(b"xdealv1 receive tests\x00" + label.encode()).digest()


class Party:
    """A party of the Go tests (receive_test.go ``party``) on a Python shelf."""

    def __init__(self, name: str, root: Path) -> None:
        self.name = name
        self.dir = root / name.replace(" ", "_")
        self.dir.mkdir(parents=True, exist_ok=True)
        self.signer = Ed25519Signer(seed(name + " record key"))
        self.checkpoint = Ed25519Signer(seed(name + " checkpoint key"))
        self.random = deterministic_stream("xdealv1 receive tests\x00" + name + " random")
        self.path = self.dir / "shelf.db"
        self.now = START
        self.profile = holder.ReceiveProfile()
        self.status = None  # a StatusProvider once use_status gives one
        self.shelf = None
        self.open()

    def tick(self) -> None:
        self.now += timedelta(minutes=1)

    def open(self) -> None:
        db = script.open_db(str(self.path))
        originals = SqliteArtifactStore(db, "x-deal-v1", [self.signer.public_key()])
        originals.init()
        self.shelf = Shelf.open_sqlite(str(self.path), operator=self.name, signer=self.signer,
                                       checkpoint_key=self.checkpoint, originals=originals, random=self.random,
                                       now=lambda: self.now, authenticated_indexes=lambda _: [],
                                       receive=ReceiveConfig(profile=self.profile), lock_timeout=5)
        self.profile.shelf = self.shelf
        if self.status is not None:
            self.status.shelf = self.shelf
        self.cadence = self.shelf.cadence(CadenceConfig(log_id=CADENCE_LOG, ended=lambda _: False, status=self.status))
        if not self.cadence.migrated():
            self.cadence.tick(self.now)
            self.tick()
            self.cadence.migrate(self.now)

    def reopen(self) -> None:
        self.shelf.release()
        self.open()

    def reopen_as(self, other: "Party") -> None:
        # Go's sealOnNewShelf: the same party, another party's record key,
        # a new shelf file.
        self.shelf.release()
        self.signer = other.signer
        self.path = self.dir / "fork.db"
        self.open()


def go_proof(v: dict) -> StampProof:
    return StampProof.from_value(json.loads(json.dumps(v), object_pairs_hook=Ordered))


def entry_of(record: dict) -> Entry:
    return Entry("deal_step", "producer_claim", payloads=(jcs(record),), subject_ref=record["x-deal-v1"]["deal_id"])


def outcome(res) -> dict:
    out: dict = {"error": False}
    if res.record is not None:
        r = res.record
        out["record"] = {"digest": r.digest, "deal_id": r.deal_id, "stored": r.stored, "duplicate": r.duplicate,
                         "deleted": r.deleted, "counted": r.counted, "ask_for_book": r.ask_for_book,
                         "refused": r.refused is not None}
    if res.proof is not None:
        out["proof"] = {"accepted": res.proof.accepted, "duplicate": res.proof.duplicate,
                        "refused": res.proof.refused is not None}
    ch = res.changes
    out["changes"] = {"grew": ch.grew or None, "shrank": ch.shrank or None, "forked": ch.forked or None,
                      "covered": ch.covered or None}
    return out


def nulls(v):
    """Go's JSON of a ReceiveResult outcome: an empty list as null."""
    if isinstance(v, dict):
        return {k: nulls(e) for k, e in v.items()}
    if isinstance(v, list):
        return v or None
    return v


def case(name: str) -> dict:
    return next(c for c in CASES if c["name"] == name)


def disk_full() -> None:
    raise OSError("the disk is full")


def book_dump(book) -> list[dict]:
    """Each record's id and, for one that reads as x-deal-v1, its book pair
    (Go's recBook)."""
    out = []
    for r in book.records():
        entry = {"record_id": r.record_id}
        rec, kind = holder._v1_payload(book, r)
        if kind == holder._V1:
            stored = book._stored[r.record_id]
            entry["pair"] = sent.book_pair(jcs(stored.capsule), stored.envelope, stored.header,
                                           v1.encode(rec)[0]).hex()
        out.append(entry)
    return out


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_holder_transcript(case, tmp_path):
    try:
        replay(case, tmp_path)
    finally:
        v1_name_rulings._before_append = None


def replay(case: dict, tmp_path: Path, upto: int | None = None) -> dict[str, Party]:
    """Replays ``case``'s steps (before step ``upto``, when given) and
    returns its parties."""
    parties: dict[str, Party] = {}
    for i, step in enumerate(case["steps"][:upto]):
        op, name = step["op"], step.get("party")
        where = f"step {i} ({op} by {name})"
        if op == "party":
            parties[name] = Party(name, tmp_path / str(i))
            continue
        if op == "naming_fails":
            v1_name_rulings._before_append = disk_full if step["in"] else None
            continue
        p = parties[name]
        if op == "reopen":
            p.reopen()
        elif op == "reopen_as":
            p.reopen_as(parties[step["in"]])
        elif op == "send_offer":
            p.tick()
            members = {k: json.loads(json.dumps(v)) for k, v in (step.get("members") or {}).items()}
            got = p.shelf.seal_sent(entry_of(step["record"]), members, step["record"]["x-deal-v1"]["deal_id"] + "/sent")
            assert got.error is None, where
            pair = sent.pair(got.capsule.capsule, got.capsule.envelope, got.capsule.payload)
            assert pair.hex() == step["out"], where
        elif op == "seal":
            p.tick()
            book = p.shelf.open(step["log"])
            e = entry_of(step["record"])
            links = (Link("cites", book.records()[-1].record_id),) if book.size() > 0 else ()
            e = Entry(e.record_type, e.epistemic_type, payloads=e.payloads, subject_ref=e.subject_ref, links=links,
                      members=dict(step.get("members") or {}))
            rec = book.append(e)
            stored = book._stored[rec.record_id]
            pair = sent.book_pair(jcs(stored.capsule), stored.envelope, stored.header, jcs(step["record"]))
            assert rec.record_id == step["out"]["record_id"], where
            assert pair.hex() == step["out"]["pair"], where
        elif op == "stamp":
            p.tick()
            p.cadence.stamp(p.now)
            proof = p.cadence.stamp_proof(step["first"], 0, "counterparty")
            assert jcs(proof.value()) == jcs(step["out"]), where
        elif op == "retention":
            # Go's SetRetention: a lifecycle record citing the record.
            journal = p.shelf.open(step["log"]) if step.get("log") else p.shelf.journal()
            target = next(r for r in journal.records() if r.seq == step["in"]["seq"])
            rec = journal.append(Entry("lifecycle", OBSERVED_EVENT, links=(Link(CITES, target.record_id),),
                                       statement={"retention_state": step["in"]["state"],
                                                  "reason": step["in"]["reason"]}))
            assert rec.record_id == step["out"], where
        elif op == "use_status":
            pairs = {d: bytes.fromhex(h) for d, h in (step.get("in") or {}).items()}
            p.status = StatusProvider(offer_pair=pairs.get)
        elif op == "stamp_tick":
            p.tick()
            try:
                res = p.cadence.stamp(p.now)
                got = {"n": res.tick.n, "before_stamp": res.before_stamp, "unstated": dict(res.unstated), "error": None}
            except Exception:  # noqa: BLE001 - the stamp's error
                got = {"n": 0, "before_stamp": "", "unstated": {}, "error": True}
            assert got == step["out"], where
        elif op == "openings":
            book_id, audience = step["in"]
            got = [{"entry_seq": f.entry_seq, "fact": f.fact,
                    "link": f.link_opening.to_json() if f.link_opening is not None else None,
                    "status": f.status_opening.to_json() if f.status_opening is not None else None}
                   for f in p.shelf.openings(book_id, audience)]
            assert jcs(got) == jcs(step["out"]), where
        elif op == "report":
            try:
                r, err = p.status.report(step["log"], dict(step["in"])), None
            except Exception:  # noqa: BLE001 - the report's refusal
                r, err = None, True
            assert {"report": jcs(r) if r is not None else None, "error": err} == \
                {"report": jcs(step["out"]["report"]) if step["out"]["report"] is not None else None,
                 "error": step["out"]["error"]}, where
        elif op == "book":
            assert book_dump(p.shelf.open(step["log"])) == step["out"], where
        elif op == "name_rulings":
            try:
                if step.get("in") is None:
                    ids, err = v1_name_rulings.name_all_rulings(p.shelf, p.now), None
                else:
                    ids, err = v1_name_rulings.name_rulings(p.shelf, step["in"], p.now), None
            except Exception as exc:  # noqa: BLE001 - the naming's error, with what it sealed first
                ids, err = exc.sealed, True
            assert {"sealed": ids or None, "error": err} == step["out"], where
        elif op == "receive":
            record = bytes.fromhex(step["pair"]) if step.get("pair") else None
            proof = go_proof(step["proof"]) if step.get("proof") else None
            try:
                res = p.shelf.receive(record, proof)
                got = outcome(res)
                # Go's Receive returns an error joining the refusals.
                got["error"] = bool(res.refusals)
            except Exception as exc:  # noqa: BLE001 - the store's or the profile's error
                res = getattr(exc, "receive_result", None)
                if res is not None:
                    # An after_receive error: the parts' result stays committed.
                    got = outcome(res)
                    got["error"] = True
                else:
                    got = {"error": True, "changes": {"grew": None, "shrank": None, "forked": None, "covered": None}}
            assert nulls(got) == nulls(step["out"]), where
        elif op == "can_act":
            i_ = step["in"]
            ok = p.profile.can_act(holder.CanActInput(i_["DealID"], i_["CounterpartAgreement"],
                                                      i_["CommitmentShelfLog"]))
            assert {"ok": ok, "error": None} == step["out"], where
        elif op == "proof_sender":
            sender, ok = p.profile.proof_sender(*step["in"])
            assert {"sender": sender, "ok": ok, "error": None} == step["out"], where
        elif op == "answered":
            held = p.shelf.received_record_of(step["in"])
            assert {"ok": p.profile.answered(held.received), "error": None} == step["out"], where
        elif op == "gate":
            i_ = step["in"]
            inp = holder.ActGateInput([decode(jcs(r)) for r in i_["own"]], decode(jcs(i_["act"])),
                                      i_["counterpart_agreement"], i_["commitment_shelf_log"])
            try:
                g = p.profile.gate_act(inp)
                got = {"gate": {"gated": g.gated, "allowed": g.allowed, **({"unguarded": True} if g.unguarded else {}),
                                **({"reason": g.reason} if g.reason else {})}, "error": None}
            except ValueError:
                got = {"gate": {"gated": False, "allowed": False}, "error": True}
            assert got == step["out"], where
        else:
            raise AssertionError(f"unknown op {op}")
    return parties


def test_ours_result_is_a_copy(tmp_path):
    # Go's TestOursResultIsACopy: the buyer's offer withheld, a cold profile
    # reads none, then the offer read again once available; an earlier
    # result is unchanged.
    c = case("withheld-offer-read-again")
    first = next(i for i, s in enumerate(c["steps"]) if s["op"] == "retention")
    buyer = replay(c, tmp_path, first + 1)["buyer"]
    cold = holder.ReceiveProfile(buyer.shelf)
    before = cold._ours()
    assert before.journal == [] and cold._cache.retry
    journal = buyer.shelf.journal()
    offer = next(r for r in journal.records() if r.seq == c["steps"][first]["in"]["seq"])
    journal.append(Entry("lifecycle", OBSERVED_EVENT, links=(Link(CITES, offer.record_id),),
                         statement={"retention_state": "AVAILABLE"}))
    after = cold._ours()
    assert len(after.journal) == 1 and not cold._cache.retry
    assert before.journal == []


def test_a_book_opening_with_two_payloads_is_not_an_agreement(tmp_path):
    # Go's v1Payload: a record committing two payloads is not read as an
    # x-deal-v1 record, so its book opens with no agreement of ours.
    c = case("handshake-from-offer")
    parties = replay(c, tmp_path)
    seal = next(s for s in c["steps"] if s["op"] == "seal")
    p = parties[seal["party"]]
    agreements = len(p.profile._ours().agreements)
    book = p.shelf.open("deal/" + hashlib.sha256(b"two payloads").hexdigest()[:32])
    p.tick()
    book.append(Entry("deal_step", "producer_claim", payloads=(jcs(seal["record"]), b"{}"),
                      subject_ref=seal["record"]["x-deal-v1"]["deal_id"]))
    assert len(p.profile._ours().agreements) == agreements
    p.tick()
    other = p.shelf.open("deal/" + hashlib.sha256(b"one payload").hexdigest()[:32])
    other.append(entry_of(seal["record"]))
    assert len(p.profile._ours().agreements) == agreements + 1, "the same record as one payload is read"


@pytest.mark.parametrize("raw,want", [
    ('{"x-deal-v0": {"profile": "x-deal-v0"}, "body": {}}', "x-deal-v0"),
    ('{"type": "task-authority/v0", "body": {}}', "x-deal-v0"),
    ('{"x-deal-v7": {"profile": "x-deal-v7"}, "body": {}}', "x-deal-v7"),
    ('{"x-deal-v1": {"PROFILE": "x-deal-v1"}}', "x-deal-v1"),
    ('{"x-deal-v1": {"profile": "x-deal-v1"}, "type": 7}', "x-deal-v1"),
    ('{"x-deal-v0": {"profile": "x-deal-v0"}, "x-deal-v1": {"profile": "x-deal-v1"}}', ""),
    ('{"x-deal-v1": {"profile": "x-deal-v1"}, "type": "action-record/v0"}', ""),
    ('{"body": {}}', ""),
    ('{"x-deal-v1": {"profile": "x-deal-v0"}}', ""),
    ('{"x-deal-v1": {"profile": 1}}', ""),
    ('{"x-deal-v1\n": {"profile": "x-deal-v1\n"}}', ""),
    ("[1]", ""),
    ("null", ""),
    ('{"type": "deal_report"}', ""),
])
def test_version(raw, want):
    # Go's TestVersion table, and how json.Unmarshal reads a member in
    # another case, a type of another kind and a name ending in a newline.
    assert holder.version(raw.encode()) == want


def test_version_of_a_v1_record():
    step = next(s for s in case("handshake-from-offer")["steps"] if s["op"] == "seal")
    assert holder.version(jcs(step["record"])) == "x-deal-v1"


def test_book_entry():
    # Go's TestBookEntry: past seq 1 a record cites the one before it; the
    # one payload is the record's JCS, whose digest is the record digest;
    # the header's record_type is deal_step (design §3.1).
    seal = next(s for s in case("later-records")["steps"] if s["op"] == "seal" and
                s["record"]["x-deal-v1"]["seq"] > 1)
    r = decode(jcs(seal["record"]))
    with pytest.raises(ValueError, match="cites the record before it"):
        v1.book_entry(r)
    e = v1.book_entry(r, "ab" * 32)
    assert e.links == (Link(CITES, "ab" * 32),)
    assert e.record_type == "deal_step" and e.epistemic_type == "producer_claim"
    assert e.subject_ref == r.block["deal_id"]
    payload, d = v1.encode(r)
    assert e.payloads == (payload,) and v1.digest(r) == d
    first = next(s for s in case("later-records")["steps"] if s["op"] == "seal")
    assert v1.book_entry(decode(jcs(first["record"]))).links == ()


def _seller_before_the_ruling(tmp_path):
    """The name-rulings scenario up to the seller receiving the ruling: the
    parties, the ruling's pair, and a settle at seq 2 of the seller's book
    (Go's settleRecord, as the part-book scenario sealed it)."""
    c = case("name-rulings")
    at = next(i for i, s in enumerate(c["steps"]) if s["op"] == "receive" and i > 4)
    parties = replay(c, tmp_path, at)
    settle = next(s for s in case("name-rulings-leaves-a-part-book")["steps"]
                  if s["op"] == "seal" and s["party"] == "seller" and s["record"]["x-deal-v1"]["seq"] == 2)
    return parties, bytes.fromhex(c["steps"][at]["pair"]), decode(jcs(settle["record"]))


def test_naming_after_a_concurrent_write(tmp_path):
    # Go's TestNameRulingsAfterAConcurrentWrite: a write landing between the
    # read and the append moves the statement to the next place; it is
    # built again there and lands valid.
    parties, pair, settle = _seller_before_the_ruling(tmp_path)
    seller = parties["seller"]
    log = next(lid for lid in seller.shelf.books())
    book = seller.shelf.open(log)
    first = book.records()[0].record_id
    wrote = []

    def write_once():
        if not wrote:
            wrote.append(book.append(v1.book_entry(settle, first)).record_id)

    v1_name_rulings._before_append = write_once
    try:
        seller.shelf.receive(pair, None)
    finally:
        v1_name_rulings._before_append = None
    recs = book.records()
    assert len(wrote) == 1 and len(recs) == 3, "the host's record, then the naming statement"
    rec, kind = holder._v1_payload(book, recs[2])
    assert kind == holder._V1 and rec.record_type == "statement" and rec.block["seq"] == 3
    assert rec.block["prev"]["digest"] == v1.digest(settle), "built again for its place"


def test_naming_gives_up_after_four_tries(tmp_path):
    # The book moving at every append: read again three times, then the
    # naming's error is BusyError, and nothing is sealed.
    from evidencebook.errors import BusyError

    parties, pair, _ = _seller_before_the_ruling(tmp_path)
    seller = parties["seller"]
    tries = []
    real_open = seller.shelf.open

    class Moving:
        def __init__(self, book):
            self._book = book

        def __getattr__(self, name):
            return getattr(self._book, name)

        def append_at(self, seq, entry):
            tries.append(seq)
            raise BusyError("the book moved")

    seller.shelf.open = lambda log_id: Moving(real_open(log_id))
    v1_name_rulings._before_append = None
    with pytest.raises(BusyError) as exc:
        seller.shelf.receive(pair, None)
    assert len(tries) == 4 and exc.value.sealed == []
    assert exc.value.receive_result.record.stored, "the ruling stays held"


def test_naming_keeps_what_it_sealed_before_an_error(monkeypatch):
    # Go's NameRulings returns the ids sealed before an error beside it.
    calls = []

    def name_in(shelf, book, ag, deal, at, out):
        calls.append(deal)
        if len(calls) == 1:
            out.append("a" * 64)
            return
        raise OSError("the disk is full")

    class Book:
        def __init__(self, deal):
            self.deal = deal

        def records(self):
            return [type("R", (), {"seq": 1})()]

    deals = {"deal/1": "deal-1111111111111111", "deal/2": "deal-2222222222222222"}

    class Shelf:
        def books(self):
            return list(deals)

        def open(self, log_id):
            return Book(deals[log_id])

    def payload(book, r):
        rec = type("Rec", (), {"record_type": "agree", "block": {"deal_id": book.deal},
                               "body": {"kind": "agreement", "adjudicator": {"key_id": "k"}}})()
        return rec, holder._V1

    monkeypatch.setattr(v1_name_rulings, "_name_rulings_in", name_in)
    monkeypatch.setattr(holder, "_v1_payload", payload)
    with pytest.raises(OSError) as exc:
        v1_name_rulings.name_all_rulings(Shelf(), START)
    assert exc.value.sealed == ["a" * 64]

    # And when opening the next book fails, after the first was named.
    calls.clear()
    monkeypatch.setattr(v1_name_rulings, "_name_rulings_in", lambda shelf, book, ag, deal, at, out: out.append("b" * 64))

    class FailingOpen(Shelf):
        def open(self, log_id):
            if log_id == "deal/2":
                raise OSError("the disk is gone")
            return super().open(log_id)

    with pytest.raises(OSError) as exc:
        v1_name_rulings.name_all_rulings(FailingOpen(), START)
    assert exc.value.sealed == ["b" * 64]


def test_earlier_naming():
    # Go's TestRulingNamedByTheCounterparty, its earlier table: a known time
    # before a stamp, else the earlier known time or the lower stamp.
    from evidencebook.deal.v1.status_provider import _earlier

    assert _earlier("2026-11-20T00:00:00Z", {"stamp": 1})
    assert _earlier("2026-11-19T00:00:00Z", "2026-11-20T00:00:00Z")
    assert _earlier({"stamp": 2}, {"stamp": 3})
    assert not _earlier({"stamp": 3}, "2026-11-20T00:00:00Z")


def test_entry_members_seal_into_the_capsule(tmp_path):
    # Go's Entry.Members: further top-level members of the record's Capsule,
    # covered by its id and signature; the header's own member name and a
    # base-profile name are refused.
    from evidencebook.errors import InvalidError

    p = Party("buyer", tmp_path)
    book = p.shelf.open("deal/" + "ab" * 16)
    rec = book.append(Entry("deal_step", "producer_claim", payloads=(b"{}",), members={"settlement": {"leg": "x"}}))
    assert book._stored[rec.record_id].capsule["settlement"] == {"leg": "x"}
    for bad in ({"evidence_record_header": {}}, {"action_id": "x"}, {"Settlement": 1}):
        with pytest.raises(InvalidError):
            book.append(Entry("deal_step", "producer_claim", payloads=(b"{}",), members=bad))


FORKED = json.loads((TRANSCRIPTS.parents[1] / "forked-finals" / "vectors.json").read_text())["cases"]


@pytest.mark.parametrize("case", FORKED, ids=[c["name"] for c in FORKED])
def test_forked_finals_vector(case):
    # Go's TestForkedFinals, through testdata/deal-v1/forked-finals.
    from types import SimpleNamespace

    from evidencebook.deal.v1.status_provider import _forked_finals

    held = [SimpleNamespace(received=SimpleNamespace(digest=h["digest"], sender_key=h["sender"], log_id=h["log_id"],
                                                     position=h["position"], prev=h["prev"]),
                            record=bytes.fromhex(h["pair"])) for h in case["held"]]
    assert _forked_finals(held, case["key"], case["log"]) == case["want"]


@pytest.mark.parametrize("unix, want", [
    # Go's time.Unix(t, 0).UTC().Format(time.RFC3339) for each, from a probe.
    (0, "1970-01-01T00:00:00Z"), (1795049519, "2026-11-19T00:51:59Z"),
    (253402300799, "9999-12-31T23:59:59Z"), (253402300800, "10000-01-01T00:00:00Z"),
    (9007199254740991, "285428751-11-12T07:36:31Z"), (-62135596800, "0001-01-01T00:00:00Z"),
    (-62135596801, "0000-12-31T23:59:59Z"), (-62167219200, "0000-01-01T00:00:00Z"),
    (-62198755201, "-0002-12-31T23:59:59Z"), (-100000000000, "-1199-02-15T14:13:20Z"),
])
def test_a_witness_time_formats_as_go_does(unix, want):
    from evidencebook.deal.v1.status_provider import _known

    assert _known(unix) == want


def test_times_take_a_witness_first_then_a_stamp(monkeypatch):
    # Go's ownTime and receivedTime: a named witness's time, else the first
    # stamp covering the record, else this stamp (null for none).
    from types import SimpleNamespace

    from evidencebook.deal.v1.status_provider import StatusProvider

    class Shelf:
        def __init__(self, t, n):
            self.t, self.n = t, n

        def record_time_by_key(self, *a):
            return self.t

        def received_record_time_by_key(self, *a):
            return self.t

        def record_stamp(self, *a):
            return self.n

        def received_record_stamp(self, *a):
            return self.n

    inp = SimpleNamespace(book_id="b", n=7)
    for t, n, want in ((1795049519, 3, "2026-11-19T00:51:59Z"), (None, 3, {"stamp": 3}), (None, None, {"stamp": 7})):
        sp = StatusProvider(Shelf(t, n))
        assert sp._own_time(inp, 1, ["k"]) == want
        assert sp._received_time(inp, "deal/x", 1, ["k"]) == want
    assert StatusProvider(Shelf(None, None))._own_time(SimpleNamespace(book_id="b", n=0), 1, []) is None


def test_entry_members_are_copied_when_sealed(tmp_path):
    # Go copies a member's bytes: changing the caller's value after append
    # leaves the sealed Capsule as signed.
    p = Party("buyer", tmp_path)
    book = p.shelf.open("deal/" + "cd" * 16)
    leg = {"leg": "payer_observed", "amount": {"value": "1"}}
    rec = book.append(Entry("deal_step", "producer_claim", payloads=(b"{}",), members={"settlement": leg}))
    leg["amount"]["value"] = "999"
    assert book._stored[rec.record_id].capsule["settlement"]["amount"]["value"] == "1"


def _report_scenario(tmp_path):
    """The report scenario up to its first report after the stamp: the
    buyer, its book's log, and the copy."""
    c = case("status-provider-report")
    at = [i for i, s in enumerate(c["steps"]) if s["op"] == "report"][1]
    parties = replay(c, tmp_path, at)
    return parties["buyer"], c["steps"][at]["log"], dict(c["steps"][at]["in"])


def test_report_reads_again_when_the_book_moves(tmp_path, monkeypatch):
    # Go's Report: a book that grows during the read is read again; grown
    # past its checkpoint, the next read refuses it rather than report a
    # stale snapshot.
    buyer, log, cp = _report_scenario(tmp_path)
    sp = buyer.status
    real = sp._status_books
    calls = []

    def moving(inp):
        out = real(inp)
        if not calls:
            buyer.shelf.open(log).append(Entry("note", "producer_claim", payloads=(b"{}",)))
        calls.append(1)
        return out

    monkeypatch.setattr(sp, "_status_books", moving)
    with pytest.raises(ValueError, match="no checkpoint at its size"):
        sp.report(log, cp)
    assert len(calls) == 1, "the moved snapshot is not reported"


def test_report_gives_up_on_a_book_that_keeps_moving(tmp_path, monkeypatch):
    # Three reads, each seeing the book move, end in BusyError.
    from evidencebook.errors import BusyError

    buyer, log, cp = _report_scenario(tmp_path)
    sp = buyer.status
    book = buyer.shelf.open(log)
    real_open, real_size = buyer.shelf.open, book.size
    sizes = []

    class Moving:
        def __getattr__(self, name):
            return getattr(book, name)

        def size(self):
            sizes.append(1)
            # Before the read: the checkpointed size; after it: one more.
            return real_size() + (len(sizes) % 2 == 0)

    monkeypatch.setattr(buyer.shelf, "open", lambda log_id: Moving() if log_id == log else real_open(log_id))
    with pytest.raises(BusyError):
        sp.report(log, cp)
    assert len(sizes) >= 6, "three reads, each sized before and after"


# -- the case book (evidencebook#615, Go's case_book_test.go) --------------------------------------

def _case_scenario(tmp_path, monkeypatch, upto: int = 8):
    """The name-rulings scenario with an adjudicator that checkpoints with
    its record key (Go's newAdjudicator): the parties after the first ``upto``
    steps (8 ends with the adjudicator's ruling sealed, 7 before it), the
    ruling's step and the seller's agreement."""
    real = seed
    monkeypatch.setattr(sys.modules[__name__], "seed",
                        lambda label: real("adjudicator record key") if label == "adjudicator checkpoint key"
                        else real(label))
    c = case("name-rulings")
    parties = replay(c, tmp_path, upto)
    return parties, c["steps"][7], c["steps"][5]


def test_case_log_id():
    offer, other = "a" * 64, "b" * 64
    key, other_key = "1" * 64, "2" * 64
    i = holder.case_log_id(offer, key)
    assert i == holder.case_log_id(offer, key)
    assert i.startswith(holder.CASE_LOG_PREFIX) and len(i) == len("case/") + 32
    bytes.fromhex(i[len("case/"):])
    assert i != holder.case_log_id(other, key)
    assert i != holder.case_log_id(offer, other_key)
    assert holder.case_log_id("ab", "c") != holder.case_log_id("a", "bc")
    # Go's pinned value (case_book_test.go).
    assert i == "case/fe02461334fc17ef81a8494d07d28608"


def test_the_transcripts_case_log_is_the_computed_one(tmp_path, monkeypatch):
    parties, ruling, agreement = _case_scenario(tmp_path, monkeypatch)
    ag = agreement["record"]["body"]
    key = ag["adjudicator"]["key_id"]
    assert ruling["log"] == holder.case_log_id(ag["offer"]["digest"], key)
    assert key == parties["adjudicator"].signer.public_key().hex()


def test_a_ruling_on_another_log_is_refused(tmp_path, monkeypatch):
    parties, ruling, _ = _case_scenario(tmp_path, monkeypatch, 7)
    adj, seller = parties["adjudicator"], parties["seller"]
    adj.tick()
    entry = entry_of(ruling["record"])
    book = adj.shelf.open("case/" + hashlib.sha256(b"another case log").hexdigest()[:32])
    rec = book.append(entry)
    stored = book._stored[rec.record_id]
    pair = sent.book_pair(jcs(stored.capsule), stored.envelope, stored.header, jcs(ruling["record"]))
    res = seller.shelf.receive(pair, None)
    assert res.refusals and not res.record.stored


def test_a_case_books_proof_binds_to_the_adjudicator_key(tmp_path, monkeypatch):
    parties, ruling, _ = _case_scenario(tmp_path, monkeypatch, 7)
    adj, seller = parties["adjudicator"], parties["seller"]
    adj.tick()
    replay_step = case("name-rulings")["steps"][7]
    book = adj.shelf.open(replay_step["log"])
    rec = book.append(entry_of(replay_step["record"]))
    stored = book._stored[rec.record_id]
    assert rec.record_id == replay_step["out"]["record_id"]
    pair = sent.book_pair(jcs(stored.capsule), stored.envelope, stored.header, jcs(replay_step["record"]))
    adj.tick()
    adj.cadence.stamp(adj.now)
    proof = adj.cadence.stamp_proof(rec.record_id, 0, "counterparty")
    res = seller.shelf.receive(pair, StampProof.from_value(json.loads(jcs(proof.value()))))
    assert res.proof is not None and res.proof.accepted, res.proof.refused if res.proof else None
    sender, ok = seller.profile.proof_sender(adj.signer.public_key().hex(), replay_step["log"])
    assert ok and sender == adj.signer.public_key().hex()
    # Under a checkpoint key that is not the adjudicator's record key, it binds to nothing.
    assert seller.profile.proof_sender("0" * 64, replay_step["log"]) == ("", False)


def test_held_ruling(tmp_path, monkeypatch):
    parties, ruling, _ = _case_scenario(tmp_path, monkeypatch)
    seller, buyer = parties["seller"], parties["buyer"]
    adj_key = parties["adjudicator"].signer.public_key().hex()
    rec = decode(jcs(ruling["record"]))
    d = v1.digest(rec)
    deal = rec.block["deal_id"]
    assert holder.held_ruling(seller.shelf, deal, adj_key, d) == (None, False), "not received yet: unavailable"
    seller.shelf.receive(bytes.fromhex(case("name-rulings")["steps"][8]["pair"]), None)
    read, found = holder.held_ruling(seller.shelf, deal, adj_key, d)
    assert found and read.record.record_type == "ruling"
    assert holder.held_ruling(seller.shelf, deal, buyer.signer.public_key().hex(), d) == (None, False)
    assert holder.held_ruling(seller.shelf, "deal-ffffffffffffffff", adj_key, d) == (None, False)


def test_the_adjudicator_takes_the_parties_books_after_its_ruling(tmp_path, monkeypatch):
    c = case("name-rulings")
    parties, ruling, agreement = _case_scenario(tmp_path, monkeypatch, 7)
    adj = parties["adjudicator"]
    seller_agreement = bytes.fromhex(agreement["out"]["pair"])
    # Another adjudicator, which ruled on nothing, takes none of it.
    stranger = Party("stranger", tmp_path / "stranger")
    assert stranger.shelf.receive(seller_agreement, None).refusals
    # Not yet ruled: the seller's agreement is refused.
    assert adj.shelf.receive(seller_agreement, None).refusals
    # Seal the ruling, then the seller's agreement is taken (and a third key's is not).
    adj.tick()
    book = adj.shelf.open(c["steps"][7]["log"])
    book.append(entry_of(ruling["record"]))
    # With its stamp proof: the proof's sender is the party the case book names.
    res = adj.shelf.receive(seller_agreement, go_proof(c["steps"][6]["out"]))
    assert not res.refusals and res.record.stored and res.proof.accepted
    intruder = Party("intruder", tmp_path / "intruder")
    ibook = intruder.shelf.open("deal/c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0")
    rec = ibook.append(entry_of(agreement["record"]))
    st = ibook._stored[rec.record_id]
    pair = sent.book_pair(jcs(st.capsule), st.envelope, st.header, jcs(agreement["record"]))
    assert adj.shelf.receive(pair, None).refusals, "a key the ruling does not name"


def test_a_forked_case_book_makes_no_checkpoint_claim(tmp_path, monkeypatch):
    """Go's TestForwardedRulingShowsAFork, its proof-sender half: a judge
    that gave the holder two different first rulings of the case log is
    forked, and no proof of that log binds to it."""
    parties, ruling, _ = _case_scenario(tmp_path, monkeypatch, 7)
    adj, seller = parties["adjudicator"], parties["seller"]
    key = adj.signer.public_key().hex()
    log = ruling["log"]

    def sealed(rec: dict) -> bytes:
        adj.tick()
        book = adj.shelf.open(log)
        r = book.append(entry_of(rec))
        st = book._stored[r.record_id]
        return sent.book_pair(jcs(st.capsule), st.envelope, st.header, jcs(rec))

    pair_a = sealed(ruling["record"])
    adj.tick()
    adj.cadence.stamp(adj.now)
    first_id = adj.shelf.open(log).records()[0].record_id
    proof = StampProof.from_value(json.loads(jcs(adj.cadence.stamp_proof(first_id, 0, "counterparty").value())))
    res = seller.shelf.receive(pair_a, proof)
    assert res.proof.accepted
    assert seller.profile.proof_sender(key, log) == (key, True)

    other = json.loads(json.dumps(ruling["record"]))
    other["body"].update({"against": "buyer", "value": "met"})
    adj.reopen_as(adj)  # the same key on a new shelf: the fork
    pair_b = sealed(other)
    seller.shelf.receive(pair_b, None)
    assert seller.profile.proof_sender(key, log) == ("", False)
