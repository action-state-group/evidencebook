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
from evidencebook.deal.v1 import holder, sent
from evidencebook.deal.v1.check import decode
from evidencebook.record import Entry, Link
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


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_holder_transcript(case, tmp_path):
    parties: dict[str, Party] = {}
    for i, step in enumerate(case["steps"]):
        op, name = step["op"], step.get("party")
        where = f"step {i} ({op} by {name})"
        if op == "party":
            parties[name] = Party(name, tmp_path / str(i))
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
        elif op == "receive":
            record = bytes.fromhex(step["pair"]) if step.get("pair") else None
            proof = go_proof(step["proof"]) if step.get("proof") else None
            try:
                res = p.shelf.receive(record, proof)
                got = outcome(res)
                # Go's Receive returns an error joining the refusals.
                got["error"] = bool(res.refusals)
            except Exception:  # noqa: BLE001 - the store's own error, with no result
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
            held = next(h for h in [p.shelf.received_record_of(step["in"])])
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
