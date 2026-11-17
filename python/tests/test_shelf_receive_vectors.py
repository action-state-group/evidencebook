# SPDX-License-Identifier: Apache-2.0
"""testdata/shelf/receive: the received store's vectors (evidencebook#461 §3,
§7), as Go's shelf_receive_vectors_test.go runs them. The script runs on a
fresh shelf with the vector profile; every step's outcome must equal its
``expect`` and the store's dump must equal after.json byte for byte."""
from __future__ import annotations

import hashlib
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs

from evidencebook._gojson import Ordered, go_json_indent
from evidencebook.shelf_receive import ReceiveBounds, ReceiveConfig, ReceivedRecord
from evidencebook.shelf_stamp_proof import StampProof

from receive_support import ReceiveFixture, error_class, joined_class

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "shelf" / "receive"
_FIELDS = {"v": int, "sender": str, "log": str, "position": int, "prev": str, "digest": str, "deal": str, "offer": bool,
           "sig": str}


def _signature(secret: str, r: dict) -> str:
    body = {k: v for k, v in r.items() if k != "sig"}
    return hashlib.sha256(secret.encode() + jcs(body)).hexdigest()


class VectorProfile:
    """The vector profile (README.md): a test profile, not a record format."""

    def __init__(self, spec: dict):
        self.secrets = spec["secrets"]
        self.admitted = set(spec["admitted"])
        self.answered_offers = set(spec["answered"])
        self.proof_keys = spec["proof_keys"]

    def read(self, record: bytes) -> ReceivedRecord:
        value = json.loads(record)
        if not isinstance(value, dict) or not set(value) <= set(_FIELDS):
            raise ValueError("not a vector record")
        r = {}
        for name, kind in _FIELDS.items():
            got = value.get(name, {"v": 0, "position": 0, "offer": False}.get(name, ""))
            if not isinstance(got, kind) or (kind is int and isinstance(got, bool)):
                raise ValueError(f"{name} is not a {kind.__name__}")
            r[name] = got
        if not r["sig"]:
            del r["sig"]  # omitempty
        if jcs(r) != record or r["v"] != 1:
            raise ValueError("not a canonical vector record of version 1")
        if r["sender"] not in self.secrets:
            raise ValueError("an unknown sender")
        if _signature(self.secrets[r["sender"]], r) != r.get("sig"):
            raise ValueError("a bad signature")
        return ReceivedRecord(r["digest"], r["sender"], r["log"], r["position"], r["prev"], r["deal"], r["offer"])

    def admit(self, r: ReceivedRecord) -> bool:
        return r.offer or r.sender_key in self.admitted

    def answered(self, r: ReceivedRecord) -> bool:
        return r.digest in self.answered_offers

    def proof_sender(self, key_id: str, log_id: str) -> tuple[str, bool]:
        sender = self.proof_keys.get(key_id)
        return (sender or "", sender is not None)


def _at(text: str) -> datetime:
    return datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def _dump(shelf) -> str:
    """Go's ``dumpReceived``: the store's tables, TEXT and INTEGER as they
    are, every BLOB as lowercase hex, then each sender's counted prefix."""
    def table(query: str, *blobs: str) -> list[dict]:
        with shelf._read() as db:
            cursor = db.execute(query)
            columns = [c[0] for c in cursor.description]
            rows = cursor.fetchall()
        return [{c: (v.hex() if c in blobs and v is not None else v) for c, v in zip(columns, row)} for row in rows]

    d = Ordered(
        shelf_received=table("SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at "
                             "FROM shelf_received ORDER BY digest", "record"),
        shelf_received_proofs=table(
            "SELECT sender_key, log_id, size, shelf_checkpoint_sha256, book_checkpoint, fact, status_opening, "
            "inclusion_proof, shelf_checkpoint, received_at, verified FROM shelf_received_proofs "
            "ORDER BY sender_key, log_id, size, shelf_checkpoint_sha256",
            "book_checkpoint", "fact", "status_opening", "inclusion_proof", "shelf_checkpoint"),
        shelf_received_tombstones=table("SELECT digest, sender_key, received_at, deleted_at FROM "
                                        "shelf_received_tombstones ORDER BY digest"),
        shelf_evidence=table("SELECT id, kind, a, b, noted_at FROM shelf_evidence ORDER BY id", "a", "b"),
        shelf_received_conflicts=table("SELECT sender_key, log_id, kind, at, evidence_id, offer FROM "
                                       "shelf_received_conflicts ORDER BY sender_key, log_id, kind, at"),
        shelf_received_vacated=table("SELECT sender_key, log_id, position, digest, deal_id, offer FROM "
                                     "shelf_received_vacated ORDER BY sender_key, log_id, position, digest"),
    )
    keys = {(r["sender_key"], r["log_id"]) for name in ("shelf_received", "shelf_received_conflicts",
                                                         "shelf_received_vacated") for r in d[name]}
    prefixes = []
    for sender, log in sorted(keys, key=lambda k: (k[0].encode(), k[1].encode())):
        p = shelf.counted_prefix(sender, log)
        prefixes.append({"sender_key": sender, "log_id": log, "size": p.size, "last": p.last, "fork_at": p.fork_at})
    d["counted_prefixes"] = prefixes
    return go_json_indent(d)


def _changes(c) -> dict:
    return Ordered(grew=c.grew, shrank=c.shrank, forked=c.forked, covered=c.covered)


@pytest.fixture
def vectors():
    return json.loads((VECTORS / "script.json").read_text(), object_pairs_hook=Ordered)


def test_stamp_proofs_encode_as_go_does(vectors):
    """Each proof of script.json reads and writes back byte for byte: the
    script with its proofs re-encoded is the file."""
    raw = (VECTORS / "script.json").read_text()
    for name, value in vectors["proofs"].items():
        vectors["proofs"][name] = StampProof.from_value(value).value()
    assert go_json_indent(vectors) == raw


def test_the_script_produces_each_expect_and_the_dump(tmp_path, vectors):
    b = vectors["bounds"]
    config = ReceiveConfig(VectorProfile(vectors["profile"]), ReceiveBounds(**b),
                           timedelta(seconds=vectors["retention_seconds"]))
    fx = ReceiveFixture(tmp_path, config)
    try:
        shelf = fx.open()
        proofs = {name: StampProof.from_value(v) for name, v in vectors["proofs"].items()}
        for i, step in enumerate(vectors["steps"]):
            fx.clock.t = _at(step["at"])
            want = json.loads(json.dumps(step["expect"]))
            got: dict = {"error": ""}
            if step["op"] == "receive":
                record = vectors["records"][step["record"]].encode() if step.get("record") else None
                proof = proofs[step["proof"]] if step.get("proof") else None
                result = shelf.receive(record, proof)
                got["error"] = joined_class(result.refusals)
                if result.record is not None:
                    r = result.record
                    got["record"] = {"digest": r.digest, "stored": r.stored, "duplicate": r.duplicate,
                                     "deleted": r.deleted, "counted": r.counted, "ask_for_book": r.ask_for_book,
                                     "refused": error_class(r.refused)}
                if result.proof is not None:
                    p = result.proof
                    got["proof"] = {"accepted": p.accepted, "duplicate": p.duplicate, "refused": error_class(p.refused)}
                got["changes"] = dict(_changes(result.changes))
            elif step["op"] == "forget":
                settled = bool(step.get("settled"))
                try:
                    deleted = shelf.forget(step["deal"], fx.clock.t, lambda _deal: settled)
                except Exception as exc:  # noqa: BLE001 -- classed as Go classes it
                    got["error"], deleted = error_class(exc), []
                if deleted:
                    got["deleted"] = deleted
            elif step["op"] == "reopen":
                shelf.release()
                shelf = fx.open()
            else:
                raise AssertionError(f"step {i}: unknown op {step['op']!r}")
            assert got == want, f"step {i} ({step['op']} at {step['at']})"
        assert _dump(shelf) == (VECTORS / "after.json").read_text()
    finally:
        fx.close()
