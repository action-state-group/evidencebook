# SPDX-License-Identifier: Apache-2.0
"""Python's side of the shelf storage interlock (evidencebook#254 §5 item
2): the scripted shelf of testdata/shelf/script.json, run with this
repository's Python package, as test/interop/shelf (Go) runs it.

    python shelf.py write DIR       the writer phase, on a new DIR/shelf.db
    python shelf.py continue DIR    the continuer phase, on DIR/shelf.db
    python shelf.py dump DIR        the file's dump on stdout (it reads a copy)
    python shelf.py hold DIR SECS   open the shelf, print "held", keep it SECS seconds
    python shelf.py try DIR MS      open the shelf, waiting at most MS ms for its
                                    lock: exit 0 if it opened, 3 if another
                                    writer holds the lock

Each language writes or continues the other's file, and every dump must
equal testdata/shelf/after-write.json or after-continue.json byte for byte.
The keys are test keys. Never use them for anything else.
"""
from __future__ import annotations

import base64
import hashlib
import json
import os
import shutil
import sqlite3
import sys
import tempfile
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

import cbor2
from agent_action_capsule.witness import checkpoint_digest
from capsule_emit.artifact.sqlite import SqliteArtifactStore
from cll.checkpoint.cose_wire import verify_checkpoint_cose_offline
from cll.log import AppendInput

from evidencebook import Entry, OBSERVED_EVENT
from evidencebook._gojson import go_json_indent
from evidencebook._random import deterministic_stream
from evidencebook.cadence import CadenceConfig, WitnessReceipt, WITNESS_VERIFIED
from evidencebook.errors import NotFoundError
from evidencebook.shelf import Shelf
from evidencebook.signer import Ed25519Signer

SCRIPT = json.loads((Path(__file__).resolve().parents[3] / "testdata" / "shelf" / "script.json").read_text())
HOST = SCRIPT["host_parameters"]
CADENCE = HOST["cadence"]
KEYS = SCRIPT["keys"]
FILE = SCRIPT["file"]
START = datetime.strptime(SCRIPT["start"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
HOST_LOG = HOST["host_log"]["log_id"]

SIGNER = Ed25519Signer(bytes.fromhex(KEYS["signer_seed"]))
CHECKPOINT_KEY = Ed25519Signer(bytes.fromhex(KEYS["checkpoint_seed"]))
WITNESS_KEY = Ed25519Signer(bytes.fromhex(KEYS["witness_seed"]))
DIRECTORY = {"witnesses": [{"name": "test witness", "endpoint": CADENCE["witness_url"],
                            "key_ids": [KEYS["witness_public"]]}]}


class Clock:
    """The script's clock: the time a step sets; reads never advance it."""

    def __init__(self, at: datetime):
        self.at = at

    def __call__(self) -> datetime:
        return self.at


def open_db(path: str) -> sqlite3.Connection:
    db = sqlite3.connect(path, timeout=5.0, isolation_level=None, check_same_thread=False)
    for statement in ("PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL",
                      "PRAGMA foreign_keys=ON"):
        db.execute(statement)
    return db


class Env:
    """One open shelf of the script: its own artifact store handle on the file."""

    def __init__(self, path: str, stream: str, at: datetime, lock_timeout: float | None = None):
        self.db = open_db(path)
        try:
            originals = SqliteArtifactStore(self.db, HOST["originals"]["namespace"], [SIGNER.public_key()])
            originals.init()
            self.clock = Clock(at)
            self.shelf = Shelf.open_sqlite(path, operator=HOST["operator"], signer=SIGNER, checkpoint_key=CHECKPOINT_KEY,
                                           originals=originals, now=self.clock, random=deterministic_stream(stream),
                                           lock_timeout=lock_timeout)
        except BaseException:
            self.db.close()
            raise
        try:
            self.cadence = self.shelf.cadence(CadenceConfig(
                log_id=CADENCE["log_id"], interval=timedelta(seconds=CADENCE["interval_s"]),
                jitter=timedelta(seconds=CADENCE["jitter_s"]), pad_bucket=CADENCE["pad_bucket"],
                witness_ids=CADENCE["witness_ids"], witnesses=DIRECTORY, ended=lambda log_id: log_id in CADENCE["ended"]))
        except BaseException:
            self.release()
            raise

    def release(self) -> None:
        try:
            self.shelf.release()
        finally:
            self.db.close()

    def append(self, book: str, entry: Entry) -> None:
        self.shelf.open(book).append(entry)

    def host_append(self, n: int) -> None:
        value = hashlib.sha256(HOST_LOG.encode() + bytes([n])).digest()
        self.shelf.host_log(HOST_LOG, True).write(
            lambda stores: stores.log.append(AppendInput(value, self.clock())))

    def tick(self):
        last = self.cadence.last()
        now = START + timedelta(seconds=30) if last is None else last.due_next
        self.clock.at = now
        result = self.cadence.tick(now)
        if not result.ticked:
            raise RuntimeError(f"no tick due at {now} (due {result.due_next})")
        return result.tick


def mint_receipt(statement: bytes) -> WitnessReceipt:
    """The script's witness receipt for a cadence checkpoint, as Go's
    shelfscript.MintReceipt makes it: an RFC 9162 COSE receipt from the
    witness key over a one-entry log holding the checkpoint, so its
    inclusion path is empty. Ed25519 is deterministic, so its bytes depend on
    the statement alone."""
    decoded = verify_checkpoint_cose_offline(statement).decoded
    signed = {name: getattr(decoded, name) for name in
              ("log_id", "mmr_size", "root", "key_id", "timestamp", "prev_size", "prev_root")}
    entry = hashlib.sha256(bytes.fromhex(checkpoint_digest(signed))).digest()
    leaf = hashlib.sha256(b"\x00" + entry).digest()
    protected = cbor2.dumps({1: -8, 395: 1}, canonical=True)
    proof = cbor2.dumps([1, 0, []])
    signature = WITNESS_KEY.sign(cbor2.dumps(["Signature1", protected, b"", leaf]))
    receipt = cbor2.dumps(cbor2.CBORTag(18, [protected, {396: {-1: [proof]}}, None, signature]), canonical=True)
    return WitnessReceipt(CADENCE["witness_url"], entry.hex(), receipt, 0, 1)


def observation(subject: str, payload: str | None) -> Entry:
    return Entry("observation", OBSERVED_EVENT, subject_ref=subject,
                 payloads=() if payload is None else (payload.encode("utf-8"),))


def run(path: str, steps: list[dict]) -> None:
    env, now = None, START
    for i, step in enumerate(steps, start=1):
        op = step["op"]
        try:
            if (env is None) != (op in ("open", "plant_pending")):
                raise RuntimeError(f"{op} with the shelf {'released' if env is None else 'open'}")
            if op == "open":
                env = Env(path, step["stream"], START + timedelta(seconds=step.get("at", 0)))
            elif op == "release":
                now = env.clock()
                env, closing = None, env
                closing.release()
            elif op == "host_append":
                env.host_append(step["n"])
            elif op == "append":
                env.append(step["book"], observation(step["subject"], step["payload"]))
            elif op == "tick":
                tick = env.tick()
                if step.get("witness"):
                    check = env.cadence.witnessed(tick.n, mint_receipt(tick.checkpoint))
                    if check.state != WITNESS_VERIFIED:
                        raise RuntimeError(f"tick {tick.n}'s receipt is {check.state}: {check.reason}")
            elif op == "plant_pending":
                plant_pending(path, step["book"], step["subject"], now + timedelta(seconds=step["at"]))
            else:
                raise RuntimeError(f"unknown step {op!r}")
        except BaseException as exc:
            if env is not None:
                env.release()
            raise RuntimeError(f"step {i} ({op}): {exc}") from exc
    if env is not None:
        env.release()
        raise RuntimeError("the script left the shelf open")


def plant_pending(path: str, book: str, subject: str, at: datetime) -> None:
    """Leave ``book``'s next record stored and not in its log, as a crash
    between the two writes does: the record its next append at ``at`` would
    seal, made on a copy of the file; only its row is copied back."""
    scratch = tempfile.mkdtemp(prefix="pending-", dir=os.path.dirname(os.path.abspath(path)))
    try:
        copy = os.path.join(scratch, FILE)
        db = open_db(path)
        db.execute("VACUUM INTO ?", (copy,))
        env = Env(copy, "evidencebook shelf vectors/pending", at)
        try:
            env.append(book, observation(subject, None))
        finally:
            env.release()
        copied = open_db(copy)
        row = copied.execute("SELECT seq, record_id, header, capsule, envelope FROM book_records WHERE book_id = ? "
                             "ORDER BY seq DESC LIMIT 1", (book,)).fetchone()
        copied.close()
        db.execute("BEGIN")
        db.execute("INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES (?, ?, ?, ?, ?, ?)",
                   (book, *row))
        db.execute("INSERT OR IGNORE INTO shelf_logs (log_id, kind) VALUES (?, 'book')", (book,))
        db.execute("INSERT OR IGNORE INTO cadence_logs (log_id) VALUES (?)", (book,))
        db.execute("INSERT OR IGNORE INTO cadence_live (log_id) VALUES (?)", (book,))
        db.execute("COMMIT")
        db.close()
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def dump(directory: str) -> bytes:
    """Everything the contract fixes in the scripted shelf's file, as Go's
    shelfscript.Dump writes it. It reads a copy, so it never changes the shelf."""
    scratch = tempfile.mkdtemp(prefix="shelf-dump-")
    try:
        path = os.path.join(scratch, FILE)
        original = open_db(os.path.join(directory, FILE))
        original.execute("VACUUM INTO ?", (path,))
        original.close()
        db = open_db(path)
        state: dict = {"shelf_meta": {"version": db.execute("SELECT version FROM shelf_meta WHERE id = 1").fetchone()[0]}}
        sha = lambda b: hashlib.sha256(bytes(b)).hexdigest()  # noqa: E731
        text = lambda b: None if b is None else bytes(b).decode("utf-8")  # noqa: E731
        state["shelf_logs"] = [{"log_id": i, "kind": k} for i, k in
                               db.execute("SELECT log_id, kind FROM shelf_logs ORDER BY log_id")]
        state["book_records"] = [
            {"book_id": b, "seq": s, "record_id": r, "header": text(h), "capsule_sha256": sha(c), "envelope_sha256": sha(e)}
            for b, s, r, h, c, e in db.execute("SELECT book_id, seq, record_id, header, capsule, envelope FROM book_records "
                                               "ORDER BY book_id, seq")]
        state["book_checkpoints"] = [
            {"book_id": b, "n": n, "checkpoint_id": i, "checkpoint": text(c)}
            for b, n, i, c in db.execute("SELECT book_id, n, checkpoint_id, checkpoint FROM book_checkpoints "
                                         "ORDER BY book_id, n")]
        state["cadence_logs"] = [r[0] for r in db.execute("SELECT log_id FROM cadence_logs ORDER BY log_id")]
        state["cadence_live"] = [r[0] for r in db.execute("SELECT log_id FROM cadence_live ORDER BY log_id")]
        state["cadence_witnessed"] = [
            {"log_id": i, "checkpoint_sha256": c, "mmr_size": s, "tick": t}
            for i, c, s, t in db.execute("SELECT log_id, checkpoint_sha256, mmr_size, tick FROM cadence_witnessed "
                                         "ORDER BY log_id, checkpoint_sha256")]
        state["cadence_ticks"] = [
            {"tick": n, "at": a, "due_next": d, "entry_seq": e, "checkpoint_size": s, "checkpoint": b64url(bytes(c)),
             "leaves": l, "witnessed": w, "receipt": r}
            for n, a, d, e, s, c, l, w, r in db.execute(
                "SELECT tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt "
                "FROM cadence_ticks ORDER BY tick")]
        state["cll_entries"] = [
            {"log_id": i, "seq": s, "value": bytes(v).hex(), "appended_at": a}
            for i, s, v, a in db.execute("SELECT log_id, seq, value, appended_at FROM cll_entries ORDER BY log_id, seq")]
        nodes: dict[str, bytearray] = {}
        for log_id, node in db.execute("SELECT log_id, node FROM cll_nodes ORDER BY log_id, position"):
            nodes.setdefault(log_id, bytearray()).extend(bytes(node))
        state["cll_nodes_sha256"] = {log_id: sha(b) for log_id, b in nodes.items()}
        db.close()
        state.update(derive(path))
        return go_json_indent(state).encode("utf-8")
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def derive(path: str) -> dict:
    """What the shelf derives, on the dump's copy: Records first, so a
    pending record is listed before a chain's book open recovers it."""
    env = Env(path, "evidencebook shelf vectors/dump", START + timedelta(hours=3))
    try:
        listing = env.shelf.records()
        records = {
            "records": [{"book_id": r.book_id, "seq": r.seq, "record_id": r.record_id,
                         "record_type": r.header.record_type, "retention": r.retention} for r in listing.records],
            "pending": dict(listing.pending),
        }
        last = env.cadence.last()
        if last is None:
            raise RuntimeError("the scripted cadence has no tick")
        anchors, roots, chains = [], [], {}
        for n in range(1, last.n + 1):
            tick = env.cadence.get(n)
            roots.append(tick.leaves.tree(tick.leaves.filler_index)[0].hex())
            anchor = env.cadence.standard_history_anchor(n)
            anchors.append({"tick": anchor.tick.n, "anchor": anchor.anchor, "anchor_time": anchor.anchor_time,
                            "first_checkpoint_time": anchor.first_checkpoint_time, "extents": anchor.extents})
            for log_id in sorted(tick.leaves.deals):
                statement = tick.leaves.deals[log_id].statement
                statement = base64.urlsafe_b64decode(statement + "=" * (-len(statement) % 4))
                try:
                    chains[f"{n} {log_id}"] = env.cadence.chain(statement)
                except NotFoundError:
                    chains[f"{n} {log_id}"] = {"error": "not_found"}
        return {"records": records, "tick_roots": roots, "standard_history_anchors": anchors, "chains": chains}
    finally:
        env.release()


def main(args: list[str]) -> int:
    if len(args) < 2:
        print("usage: shelf.py write|continue|dump|hold|try DIR [ARG]", file=sys.stderr)
        return 1
    command, directory = args[0], args[1]
    path = os.path.join(directory, FILE)
    if command == "write":
        os.makedirs(directory, mode=0o700, exist_ok=True)
        if os.path.exists(path):
            raise RuntimeError(f"{path} exists")
        run(path, SCRIPT["write_phase"])
    elif command == "continue":
        run(path, SCRIPT["continue_phase"])
    elif command == "dump":
        sys.stdout.buffer.write(dump(directory))
    elif command in ("hold", "try"):
        n = int(args[2])
        try:
            env = Env(path, "evidencebook shelf vectors/hold", START, lock_timeout=None if command == "hold" else n / 1000)
        except TimeoutError as exc:
            print(f"shelf: {exc}", file=sys.stderr)
            return 3
        if command == "hold":
            print("held", flush=True)
            time.sleep(n)
        env.release()
    else:
        print(f"unknown command {command!r}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
