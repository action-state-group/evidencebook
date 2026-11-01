# SPDX-License-Identifier: Apache-2.0
"""Python's side of the caller-sealed record interlock, as
test/interop/caller-sealed (Go) runs it: a deal book whose records are
Capsules their producer seals itself (``Book.append_capsule``), each
carrying its header in ``evidence_record_header`` and committing three
fields, as a deal step does (x-deal-v0 §1).

    python caller_sealed.py write DIR      a new DIR/shelf.db with the deal's first step
    python caller_sealed.py continue DIR   the deal's next step on DIR/shelf.db
    python caller_sealed.py read DIR       the book's records and field openings, as JSON

Each language continues the other's file, and the two readers' output must
be equal byte for byte. The keys are the shelf vectors' test keys. Never use
them for anything else.
"""
from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path

from capsule_emit import produce
from capsule_emit.artifact import PAYLOAD_DIGEST, PRESENT, Artifact
from capsule_emit.artifact.sqlite import SqliteArtifactStore

from evidencebook import CITES, RECORD_HEADER_MEMBER, Entry, Link
from evidencebook import deal
from evidencebook.shelf import Shelf
from evidencebook.signer import Ed25519Signer

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = json.loads((ROOT / "testdata" / "shelf" / "script.json").read_text())
KEYS = SCRIPT["keys"]
HOST = SCRIPT["host_parameters"]
FILE = SCRIPT["file"]
SIGNER_SEED = bytes.fromhex(KEYS["signer_seed"])
SIGNER = Ed25519Signer(SIGNER_SEED)
CHECKPOINT_KEY = Ed25519Signer(bytes.fromhex(KEYS["checkpoint_seed"]))

BOOK_ID = "deal/deal-3f9c2a7be4d15068"
FIXTURES = ("01-baseline.json", "02-message-quote.json", "03-claim.json", "04-evidence.json")


class Env:
    def __init__(self, path: str):
        self.at = datetime.now(timezone.utc)
        self.db = sqlite3.connect(path, timeout=5.0, isolation_level=None, check_same_thread=False)
        for statement in ("PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"):
            self.db.execute(statement)
        try:
            self.originals = SqliteArtifactStore(self.db, HOST["originals"]["namespace"], [SIGNER.public_key()])
            self.originals.init()
            self.shelf = Shelf.open_sqlite(path, operator=HOST["operator"], signer=SIGNER,
                                           checkpoint_key=CHECKPOINT_KEY, originals=self.originals,
                                           now=lambda: self.at)
        except BaseException:
            self.db.close()
            raise

    def release(self) -> None:
        try:
            self.shelf.release()
        finally:
            self.db.close()


def step(path: str) -> None:
    """Seal the book's next step: the next fixture's record, chained to the
    step before it, at the record's own time."""
    env = Env(path)
    try:
        book = env.shelf.open(BOOK_ID)
        seq = book.size() + 1
        if seq > len(FIXTURES):
            raise SystemExit("the book already holds every fixture's step")
        fixture = ROOT / "dealprofile" / "x-deal-v0" / "fixtures" / "positive" / FIXTURES[seq - 1]
        record = json.loads(fixture.read_text())["record"]
        block = record["x-deal-v0"]
        payload, _ = deal.encode(record)
        env.at = datetime.strptime(block["at"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
        records = book.records()
        parent = deal.Chain(records[-1].record_id, deal.RELATION_FOLLOWS) if records else None
        capsule, header = deal.step_capsule(deal.StepInput(
            deal_id=block["deal_id"], seq=seq, operator=HOST["operator"], developer="caller-sealed-interop",
            at=env.at, parent=parent, local=f"local event {seq}"))
        entry = Entry(header.record_type, header.epistemic_type, subject_ref=header.subject_ref,
                      links=tuple(Link(CITES, cited) for cited in header.cites), fields=dict(header.fields))
        identity = produce.new_ed25519_signing_identity(SIGNER_SEED)

        def seal(_header, header_json: bytes):
            return produce.seal(produce.SealInput(
                capsule=deal.emit_input(capsule, deal.Extension(RECORD_HEADER_MEMBER, header_json)),
                identity=identity, payload=json.loads(payload)))

        book.append_capsule(entry, [Artifact("payload", PRESENT, PAYLOAD_DIGEST, payload)], seal)
    finally:
        env.release()


def read(path: str) -> bytes:
    """The book as every reader must see it (the Go program's output)."""
    env = Env(path)
    try:
        book = env.shelf.open(BOOK_ID)
        rows = []
        for record in book.records():
            held = env.originals.get(record.record_id)
            payload = next(a.content for a in held.artifacts if a.name == "payload")
            rows.append({
                "seq": record.seq, "record_id": record.record_id, "record_type": record.header.record_type,
                "subject_ref": record.header.subject_ref, "committed_at": record.header.committed_at,
                "cites": [link.target for link in record.header.links],
                "payload_digest": hashlib.sha256(payload).hexdigest(),
                "fields": {name: book.opening(record.record_id, name).text
                           for name, _ in sorted(record.header.field_commitments)},
            })
        # Go's json.MarshalIndent: two-space indent, map keys sorted, HTML
        # characters escaped (none occur here).
        return (json.dumps({"book_id": BOOK_ID, "records": rows}, indent=2, ensure_ascii=False) + "\n").encode()
    finally:
        env.release()


def main(argv: list[str]) -> None:
    if len(argv) != 2 or argv[0] not in ("write", "continue", "read"):
        raise SystemExit("usage: caller_sealed.py write|continue|read DIR")
    command, directory = argv
    path = os.path.join(directory, FILE)
    if command == "write":
        os.makedirs(directory, exist_ok=True)
        if os.path.exists(path):
            raise SystemExit(f"{path} already exists")
        step(path)
    elif command == "continue":
        step(path)
    else:
        sys.stdout.buffer.write(read(path))


if __name__ == "__main__":
    main(sys.argv[1:])
