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
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs
from capsule_emit.artifact.sqlite import SqliteArtifactStore

from evidencebook.cadence import CadenceConfig
from evidencebook.deal.v1 import holder, naming as v1_name_rulings, sent
from evidencebook.deal import v1
from evidencebook.deal.v1.check import decode
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
        self.cadence = self.shelf.cadence(CadenceConfig(log_id=CADENCE_LOG, ended=lambda _: False))
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
            if book.size() > 0:
                last = book.records()[-1]
                e = Entry(e.record_type, e.epistemic_type, payloads=e.payloads, subject_ref=e.subject_ref,
                          links=(Link("cites", last.record_id),))
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
        elif op == "book":
            assert book_dump(p.shelf.open(step["log"])) == step["out"], where
        elif op == "name_rulings":
            try:
                if step.get("in") is None:
                    ids, err = v1_name_rulings.name_all_rulings(p.shelf, p.now), None
                else:
                    ids, err = v1_name_rulings.name_rulings(p.shelf, step["in"], p.now), None
            except Exception:  # noqa: BLE001 - the naming's error
                ids, err = [], True
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
