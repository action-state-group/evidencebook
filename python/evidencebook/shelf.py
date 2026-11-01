# SPDX-License-Identifier: Apache-2.0
"""The shelf: one subject key's books in one SQLite file (evidencebook#254 §2).

Each book is its own Checkpointed Local Log in the file, keyed by its book
id, with its records and checkpoints in the shelf's ``book_records`` and
``book_checkpoints``. One artifact store, capsule-emit's SQLite backend on
the same file, holds every book's originals. An exclusive advisory lock makes
the shelf the profile's only writer, or the host passes a lock it already
holds. Host logs are logs whose records the host seals itself; the cadence
(:mod:`evidencebook.cadence`) checkpoints every live log on time alone.

The file is the Go implementation's (``shelf.go``, ``store_sqlite.go``,
``substrate_cll.go``), table for table and byte for byte: a shelf any
language writes, every other opens, continues and verifies, as the storage
interlock (``test/interop/shelf``) checks. CLL's tables are CLL's
(``cll.log.sqlite``); the artifact store's are capsule-emit's.
"""
from __future__ import annotations

import base64
import json
import os
import sqlite3
import threading
import time
from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Protocol
from urllib.parse import quote

import cbor2
from agent_action_capsule.canonical import jcs
from cll.checkpoint import core
from cll.checkpoint.runner import CheckpointRunner
from cll.log import MAX_SCAN_LIMIT, AppendInput, CllError, INSERTED, validate_identifier
from cll.log import sqlite as cllsqlite

from ._cll import CLLSubstrate, Checkpoint, read_checkpoint
from ._gojson import Ordered, go_json
from ._random import RandomSource, Reader
from ._time import parse_ns, utc
from .errors import CorruptError, InvalidError, NotFoundError, ReleasedError, UnsupportedError
from .record import AVAILABLE, CITES, RECORD_TYPE_RETENTION_COMMITMENT, Header
from .signer import Signer
from .store import StoredRecord

__all__ = [
    "SHELF_VERSION", "HeldLock", "HostLog", "HostLogStores", "Shelf", "ShelfRecord", "ShelfRecords", "SqliteStore",
    "SqliteSubstrate",
]

#: The shelf file version this implementation writes and the only one it
#: opens (``shelf_meta.version``).
SHELF_VERSION = 1

_KIND_BOOK, _KIND_HOST = "book", "host"

#: ``store_sqlite.go``'s schema, statement for statement.
SCHEMA = """
CREATE TABLE IF NOT EXISTS book_records (
	book_id   TEXT    NOT NULL,
	seq       INTEGER NOT NULL,
	record_id TEXT    NOT NULL,
	header    BLOB    NOT NULL,
	capsule   BLOB    NOT NULL,
	envelope  BLOB    NOT NULL,
	PRIMARY KEY (book_id, record_id)
);
CREATE INDEX IF NOT EXISTS book_records_seq ON book_records (book_id, seq);
CREATE TABLE IF NOT EXISTS cadence_logs (log_id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS cadence_live (log_id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS cadence_witnessed (
	log_id            TEXT    NOT NULL,
	checkpoint_sha256 TEXT    NOT NULL,
	mmr_size          INTEGER NOT NULL,
	tick              INTEGER NOT NULL,
	PRIMARY KEY (log_id, checkpoint_sha256)
);
CREATE INDEX IF NOT EXISTS cadence_witnessed_size ON cadence_witnessed (log_id, mmr_size);
CREATE TABLE IF NOT EXISTS cadence_ticks (
	tick            INTEGER PRIMARY KEY,
	at              TEXT    NOT NULL,
	due_next        TEXT    NOT NULL,
	entry_seq       INTEGER NOT NULL,
	checkpoint_size INTEGER NOT NULL,
	checkpoint      BLOB    NOT NULL,
	leaves          TEXT    NOT NULL,
	witnessed       INTEGER NOT NULL DEFAULT 0,
	receipt         TEXT
);
CREATE TABLE IF NOT EXISTS book_checkpoints (
	book_id       TEXT    NOT NULL,
	n             INTEGER NOT NULL,
	checkpoint_id TEXT    NOT NULL,
	checkpoint    BLOB    NOT NULL,
	PRIMARY KEY (book_id, n),
	UNIQUE (book_id, checkpoint_id)
);
CREATE TABLE IF NOT EXISTS shelf_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS shelf_logs (log_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('book', 'host')));
"""

#: How often a waiting shelf tries the writer lock again.
LOCK_RETRY = 0.05

_COSE_ALG_EDDSA = -8


def mmr_size(leaves: int) -> int:
    """The MMR node count of ``leaves`` leaves: 2n minus the number of peaks."""
    return 2 * leaves - bin(leaves).count("1")


def _connect(path: str | os.PathLike) -> sqlite3.Connection:
    """The shelf's own handle on its file: WAL, a busy timeout of 5000 ms
    and ``synchronous=FULL`` (§2.2), autocommit with explicit transactions."""
    uri = f"file:{quote(os.path.abspath(os.fspath(path)))}?mode=rwc"
    connection = sqlite3.connect(uri, uri=True, timeout=5.0, isolation_level=None, check_same_thread=False)
    try:
        for statement in ("PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"):
            connection.execute(statement)
    except BaseException:
        connection.close()
        raise
    return connection


def _go_time(ns: int) -> str:
    """Go's ``time.RFC3339Nano`` of a UTC instant in nanoseconds: the
    fraction's trailing zeros dropped, none when it is zero."""
    seconds, fraction = divmod(ns, 1_000_000_000)
    text = datetime.fromtimestamp(seconds, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    if fraction:
        text += "." + f"{fraction:09d}".rstrip("0")
    return text + "Z"


# -- the writer lock ------------------------------------------------------------------


class HeldLock(Protocol):
    """A writer lock the host holds on the shelf's store (§2.4). ``held``
    raises while it is not held; the shelf asks once, when it opens, and
    never releases it."""

    def held(self) -> None: ...


def _flock(path: str, timeout: float | None) -> Callable[[], None]:
    """An exclusive advisory lock on ``path`` (``flock(LOCK_EX|LOCK_NB)``,
    retried every 50 ms), as Go's ``flockWriter`` takes, so a Go, a Rust and
    a Python writer of one shelf exclude each other. Waits for another holder
    until ``timeout`` seconds pass (forever when ``None``), then raises
    :class:`TimeoutError`."""
    try:
        import fcntl
    except ImportError:
        raise UnsupportedError("a shelf's writer lock needs a unix advisory file lock") from None
    fd = os.open(path, os.O_RDWR | os.O_CREAT, 0o600)
    deadline = None if timeout is None else time.monotonic() + timeout
    while True:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            break
        except BlockingIOError:
            if deadline is not None and time.monotonic() >= deadline:
                os.close(fd)
                raise TimeoutError(f"waiting for the shelf's writer lock {path}") from None
            time.sleep(LOCK_RETRY)
        except BaseException:
            os.close(fd)
            raise

    def unlock() -> None:
        try:
            fcntl.flock(fd, fcntl.LOCK_UN)
        finally:
            os.close(fd)

    return unlock


# -- a book's commitments on the shelf's file ---------------------------------------


class _RunnerSigner:
    """A :class:`Signer` as CLL's checkpoint runner signs: deterministic
    protected header, the raw public key as ``kid`` (``Ed25519CheckpointSigner``)."""

    def __init__(self, signer: Signer):
        self._signer = signer
        self._public = signer.public_key()
        self.key_id = self._public.hex()

    def sign_cose_statement(self, payload: bytes, *, content_type: str, issuer: str, subject: str) -> bytes:
        protected = cbor2.dumps({1: _COSE_ALG_EDDSA, 3: content_type, 4: self._public, 15: {1: issuer, 2: subject}},
                                canonical=True)
        signature = self._signer.sign(cbor2.dumps(["Signature1", protected, b"", payload], canonical=True))
        return cbor2.dumps(cbor2.CBORTag(18, [protected, {}, payload, signature]), canonical=True)


def _log_absent(path: str, log_id: str) -> bool:
    """Whether the file holds no state at all for ``log_id``: no file, or no
    meta row, entry or node of that log. Reads only."""
    if not os.path.exists(path):
        return True
    uri = f"file:{quote(os.path.abspath(path))}?mode=ro"
    connection = sqlite3.connect(uri, uri=True)
    try:
        for table in ("cll_meta", "cll_entries", "cll_nodes"):
            if not connection.execute("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
                                      (table,)).fetchone()[0]:
                continue
            if connection.execute(f"SELECT COUNT(*) FROM {table} WHERE log_id = ?", (log_id,)).fetchone()[0]:
                return False
        return True
    finally:
        connection.close()


class SqliteSubstrate(CLLSubstrate):
    """A book's log, durable: CLL's SQLite backend (``cll.log.sqlite``) on
    the shelf's file and CLL's checkpoint runner over it, as Go's
    ``OpenCLLSQLite``. Proofs are served from the MMR rebuilt from the
    backend's entries, kept in step with every write, including a host's own
    writes to its backend (:class:`HostLog`). The mirror has its own lock:
    a cadence tick and a chain read a book's log outside the book's lock."""

    def __init__(self, backend: Any, log_id: str, checkpoint_key: Signer, witness_ids: Sequence[str] = ()):
        super().__init__(log_id, checkpoint_key)
        self._mirror = threading.RLock()
        self._backend = backend
        # The book, not the runner, decides when to checkpoint: any pending
        # entry is due the moment the book asks.
        self._runner = CheckpointRunner(backend, _RunnerSigner(checkpoint_key), log_id, witness_ids=tuple(witness_ids),
                                        cadence_entries=1, cadence_age=timedelta(hours=1), scan_limit=MAX_SCAN_LIMIT)
        self._sync()
        self._latest = self._stored_checkpoint()

    @classmethod
    def open(cls, path: str, log_id: str, checkpoint_key: Signer, witness_ids: Sequence[str] = (),
             provision: bool = True) -> SqliteSubstrate:
        """The log ``log_id`` in the file at ``path``, provisioned when the
        file holds nothing of it and ``provision`` is set. A log with any
        state that fails to open is reported, never provisioned over; one the
        file holds nothing of, when not provisioned, is not found."""
        if not validate_identifier(log_id):
            raise InvalidError(f"log id {log_id!r} is not a CLL identifier")
        try:
            backend = cllsqlite.open(path, log_id)
        except (CllError, sqlite3.Error) as exc:
            absent = _log_absent(path, log_id)
            if not provision and absent:
                raise NotFoundError(f"the file holds no CLL log {log_id}") from None
            if not absent:
                raise CorruptError(f"open CLL log {log_id}: {exc}") from None
            cllsqlite.init(path, log_id)
            backend = cllsqlite.open(path, log_id)
        try:
            return cls(backend, log_id, checkpoint_key, witness_ids)
        except BaseException:
            backend.close()
            raise

    @property
    def backend(self) -> Any:
        return self._backend

    def _sync(self) -> None:
        """Index entries the backend holds beyond the in-memory MMR."""
        with self._mirror:
            while True:
                entries = self._backend.scan_entries(len(self._ids), MAX_SCAN_LIMIT)
                if not entries:
                    return
                for entry in entries:
                    if entry.seq != len(self._ids) + 1:
                        raise CorruptError(f"CLL log {self.log_id} is not dense at seq {entry.seq}")
                    self._add(entry.value.hex())

    def _stored_checkpoint(self) -> Checkpoint | None:
        state = self._backend.load_cll()
        if state.checkpoint is None:
            return None
        try:
            return read_checkpoint(state.checkpoint.bytes)
        except InvalidError as exc:
            raise CorruptError(f"the stored checkpoint of {self.log_id}: {exc}") from None

    @property
    def size(self) -> int:
        with self._mirror:
            self._sync()
            return len(self._ids)

    def entry_ids(self) -> list[str]:
        with self._mirror:
            self._sync()
            return list(self._ids)

    def entries_after(self, after_seq: int, limit: int = MAX_SCAN_LIMIT) -> list:
        """The backend's entries after ``after_seq``, at most ``limit``."""
        return self._backend.scan_entries(after_seq, min(max(limit, 1), MAX_SCAN_LIMIT))

    def append(self, record_id: str, at: datetime | None = None) -> int:
        try:
            value = bytes.fromhex(record_id)
        except ValueError:
            raise InvalidError(f"record id {record_id!r} is not hex") from None
        if len(value) != 32 or value.hex() != record_id:
            raise InvalidError(f"record id {record_id!r} is not 64 lowercase hex characters")
        with self._mirror:
            self._sync()
            result = self._backend.append(AppendInput(value, utc(at or datetime.now(timezone.utc))))
            if result.outcome == INSERTED:
                self._sync()
            return result.entry.seq

    def restore(self, checkpoint: Checkpoint) -> None:
        """Check a stored checkpoint against the log; the latest is the
        backend's own."""
        with self._mirror:
            self._sync()
            latest = self._latest
            super().restore(checkpoint)
            self._latest = latest

    def checkpoint(self, now: datetime) -> Checkpoint:
        with self._mirror:
            self._sync()
            while self._runner.run_once(utc(now)):
                pass
            latest = self._stored_checkpoint()
            if latest is None:
                raise NotFoundError(f"log {self.log_id} has no checkpoint yet")
            self._latest = latest
            return latest

    def inclusion_proof(self, seq: int, at: Checkpoint) -> bytes:
        with self._mirror:
            self._sync()
            return super().inclusion_proof(seq, at)

    def consistency_proof(self, old_size: int, new_size: int, at: Checkpoint) -> bytes:
        with self._mirror:
            self._sync()
            return super().consistency_proof(old_size, new_size, at)

    def _tree_at(self, at: Checkpoint) -> None:
        """Check the MMR reaches ``at`` and has its root."""
        self._sync()
        self._check(at)

    def inclusion(self, seq: int, at: Checkpoint) -> Ordered:
        """The JSON inclusion proof of ``seq`` under ``at``, member for
        member as Go's ``ProveInclusion`` writes it."""
        if not at.covers(seq):
            raise InvalidError(f"seq {seq} is not under checkpoint {at.id}")
        with self._mirror:
            self._tree_at(at)
            proof = core.inclusion_proof(self._nodes, seq - 1, at.tree_size)
        return Ordered(v=proof.v, kind=proof.kind, size=proof.size, leaf_index=proof.leaf_index,
                       witness=list(proof.witness), peaks_left=list(proof.peaks_left),
                       peaks_right=list(proof.peaks_right))

    def consistency(self, older: Checkpoint, newer: Checkpoint) -> Ordered:
        """The JSON consistency proof from ``older`` to ``newer``, as Go's
        ``ProveConsistency`` writes it."""
        if older.tree_size > newer.tree_size:
            raise InvalidError("the older checkpoint is larger than the newer")
        with self._mirror:
            self._tree_at(newer)
            proof = core.consistency_proof(self._nodes, older.tree_size, newer.tree_size)
        return Ordered(v=proof.v, kind=proof.kind, old_size=proof.size_a, new_size=proof.size_b,
                       old_peaks=list(proof.old_peaks), witness=[list(p) for p in proof.witness],
                       new_peaks=list(proof.new_peaks))

    def close(self) -> None:
        self._backend.close()


# -- a book's records on the shelf's file ---------------------------------------------


def _checkpoint_json(cp: Checkpoint) -> bytes:
    """Go's ``json.Marshal`` of its ``Checkpoint``, as ``book_checkpoints``
    holds it."""
    return go_json(Ordered(
        kind=cp.kind, log_id=cp.log_id, id=cp.id, entries=cp.entries, root=cp.root, tree_size=cp.tree_size,
        key_id=cp.key_id, issued_at=_go_time(cp.issued_at_ns), statement=base64.b64encode(cp.statement).decode(),
    )).encode("utf-8")


class SqliteStore:
    """One book's :class:`~evidencebook.store.Store` in the shelf's file,
    every row keyed by its book id. FileStore's semantics: a record is
    stored once (the same record again is a no-op, another under its id is
    corrupt), records list in seq order, checkpoints in the order kept, and
    every write is one transaction, durable when it returns. A record's
    first row claims its id as a book (``shelf_logs``) and puts the book in
    the cadence's live set in the same transaction."""

    def __init__(self, shelf: Shelf, book_id: str):
        self._shelf = shelf
        self._book_id = book_id

    def _get(self, record_id: str) -> tuple[int, bytes] | None:
        with self._shelf._read() as db:
            row = db.execute("SELECT seq, capsule FROM book_records WHERE book_id = ? AND record_id = ?",
                             (self._book_id, record_id)).fetchone()
        return None if row is None else (row[0], bytes(row[1]))

    def put_record(self, record: StoredRecord) -> None:
        capsule = jcs(record.capsule)
        existing = self._get(record.record_id)
        if existing is not None:
            if existing != (record.seq, capsule):
                raise CorruptError(f"record {record.record_id} already stored with different content")
            return
        with self._shelf._tx() as db:
            db.execute("INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) "
                       "VALUES (?, ?, ?, ?, ?, ?)",
                       (self._book_id, record.seq, record.record_id, bytes(record.header), capsule,
                        bytes(record.envelope)))
            _claim_kind(db, self._book_id, _KIND_BOOK)
            _mark_live(db, self._book_id)

    def records(self) -> list[StoredRecord]:
        with self._shelf._read() as db:
            rows = db.execute("SELECT seq, record_id, header, capsule, envelope FROM book_records "
                              "WHERE book_id = ? ORDER BY seq", (self._book_id,)).fetchall()
        out = []
        for seq, record_id, header, capsule, envelope in rows:
            try:
                decoded = json.loads(bytes(capsule))
            except ValueError as exc:
                raise CorruptError(f"capsule of {record_id} in {self._book_id}: {exc}") from None
            out.append(StoredRecord(seq, record_id, bytes(header), decoded, bytes(envelope)))
        return out

    def put_checkpoint(self, statement: bytes) -> None:
        cp = read_checkpoint(statement)
        with self._shelf._tx() as db:
            if db.execute("SELECT COUNT(*) FROM book_checkpoints WHERE book_id = ? AND checkpoint_id = ?",
                          (self._book_id, cp.id)).fetchone()[0]:
                return
            n = db.execute("SELECT COALESCE(MAX(n), 0) + 1 FROM book_checkpoints WHERE book_id = ?",
                           (self._book_id,)).fetchone()[0]
            db.execute("INSERT INTO book_checkpoints (book_id, n, checkpoint_id, checkpoint) VALUES (?, ?, ?, ?)",
                       (self._book_id, n, cp.id, _checkpoint_json(cp)))

    def checkpoints(self) -> list[bytes]:
        with self._shelf._read() as db:
            rows = db.execute("SELECT checkpoint FROM book_checkpoints WHERE book_id = ? ORDER BY n",
                              (self._book_id,)).fetchall()
        out = []
        for (encoded,) in rows:
            try:
                out.append(base64.b64decode(json.loads(bytes(encoded))["statement"], validate=True))
            except (ValueError, KeyError, TypeError) as exc:
                raise CorruptError(f"a stored checkpoint of {self._book_id}: {exc}") from None
        return out

    def close(self) -> None:
        """Nothing to release: the shelf owns the file."""


def _kind_of(db: sqlite3.Connection, log_id: str) -> str:
    row = db.execute("SELECT kind FROM shelf_logs WHERE log_id = ?", (log_id,)).fetchone()
    return "" if row is None else row[0]


def _claim_kind(db: sqlite3.Connection, log_id: str, kind: str) -> None:
    """Record ``log_id`` as ``kind``, refusing a log already held as the other."""
    db.execute("INSERT INTO shelf_logs (log_id, kind) VALUES (?, ?) ON CONFLICT (log_id) DO NOTHING", (log_id, kind))
    held = _kind_of(db, log_id)
    if held != kind:
        raise InvalidError(f"{log_id} is a {held} log, not a {kind} log")


def _mark_live(db: sqlite3.Connection, log_id: str) -> None:
    """Put ``log_id`` in the live set, counting it into the lifetime log count
    on its first append."""
    db.execute("INSERT OR IGNORE INTO cadence_logs (log_id) VALUES (?)", (log_id,))
    db.execute("INSERT OR IGNORE INTO cadence_live (log_id) VALUES (?)", (log_id,))


def _record_count(db: sqlite3.Connection, log_id: str) -> int:
    return db.execute("SELECT COUNT(*) FROM book_records WHERE book_id = ?", (log_id,)).fetchone()[0]


class _SerialOriginals:
    """The shelf's one artifact store as every book and host log uses it:
    one call at a time, since books append concurrently and a store on one
    ``sqlite3`` connection takes one transaction at a time."""

    def __init__(self, store: Any):
        self._store = store
        self._lock = threading.Lock()

    @property
    def namespace(self) -> str:
        return self._store.namespace

    def put(self, record: Any) -> None:
        with self._lock:
            self._store.put(record)

    def get(self, capsule_id: str) -> Any:
        with self._lock:
            return self._store.get(capsule_id)

    def purge(self, capsule_id: str) -> None:
        with self._lock:
            self._store.purge(capsule_id)

    def __getattr__(self, name: str) -> Any:
        return getattr(self._store, name)


# -- host logs -------------------------------------------------------------------------


@dataclass(frozen=True)
class HostLogStores:
    """What a host log's writes run on: the log's CLL backend on the shelf's
    file and the shelf's artifact store, under its one namespace. Both are
    the shelf's; the host must not close them or keep them past the write."""

    log: Any
    originals: Any


class HostLog:
    """A log on the shelf whose records the host seals itself, in its own
    format, never converted into book records (§2.7). In the cadence it is
    checkpointed like a book; outside it, its checkpoints and witness rows
    are the host's own on its backend. ``shelf_logs`` records it as a host
    log, so :meth:`Shelf.open` refuses its id across reopens."""

    def __init__(self, shelf: Shelf, log_id: str, log: SqliteSubstrate, in_cadence: bool):
        self._shelf = shelf
        self._id = log_id
        self._log = log
        self._in_cadence = in_cadence
        self._lock = threading.Lock()

    @property
    def log_id(self) -> str:
        return self._id

    @property
    def in_cadence(self) -> bool:
        return self._in_cadence

    def write(self, write: Callable[[HostLogStores], Any]) -> Any:
        """Run ``write`` on the log's stores under the shelf's lock. ``write``
        must not call the shelf or its cadence. A log in the cadence is live
        before the write, so no write, finished or not, is left outside the
        next tick."""
        shelf = self._shelf
        with shelf._mu:
            shelf._check_open()
            if self._in_cadence:
                with shelf._tx() as db:
                    _mark_live(db, self._id)
            with self._lock:
                return write(HostLogStores(self._log.backend, shelf._originals))

    def append(self, write: Callable[[], Any]) -> Any:
        """:meth:`write` for a step that opens its own handle on the log."""
        return self.write(lambda _: write())

    def mmr_size(self) -> int:
        return mmr_size(self._log.size)

    def cut_local(self, at: datetime) -> Checkpoint:
        """Checkpoint everything the log holds at ``at``, cutting a new
        checkpoint only if it grew. Nothing goes to a witness."""
        return self._log.checkpoint(at)

    @property
    def substrate(self) -> SqliteSubstrate:
        return self._log


class _BookLog:
    """A book as the cadence checkpoints it."""

    def __init__(self, book: Any):
        self._book = book

    @property
    def log_id(self) -> str:
        return self._book.log_id

    def mmr_size(self) -> int:
        return mmr_size(self._book._substrate.size)

    def cut_local(self, at: datetime) -> Checkpoint:
        return self._book.checkpoint(at)

    @property
    def substrate(self) -> SqliteSubstrate:
        return self._book._substrate


# -- Records --------------------------------------------------------------------------


@dataclass(frozen=True)
class ShelfRecord:
    """A committed record of one of a shelf's books, as :meth:`Shelf.records`
    lists it. ``retention`` is its retention state now, empty for a record
    that commits no payload or field."""

    book_id: str
    seq: int
    record_id: str
    header: Header
    retention: str = ""


@dataclass(frozen=True)
class ShelfRecords:
    """The committed records, and how many records each book holds above
    its committed entry count (a crash between the record's row and its log
    entry, which the book's next open recovers)."""

    records: list[ShelfRecord] = field(default_factory=list)
    pending: dict[str, int] = field(default_factory=dict)


_LIFECYCLE = "lifecycle"
_LEGAL_HOLD, _DELETED, _PARTIAL = "LEGAL_HOLD", "DELETED", "PARTIAL"


def _held_by(headers: list[Header], ids: list[str], record_id: str, header: Header, now_ns: int) -> bool:
    """A retention commitment covers the record and has not expired: it
    names it by id, a range of leaf indexes holding it, or an exchange it
    cites."""
    for h in headers:
        if h.record_type != RECORD_TYPE_RETENTION_COMMITMENT or not isinstance(h.statement, dict):
            continue
        subject, until = h.statement.get("subject"), parse_ns(h.statement.get("until"))
        if until is None or not isinstance(subject, dict) or len(subject) != 1 or now_ns > until:
            continue
        ((form, content),) = subject.items()
        if form == "record" and content == record_id:
            return True
        if form == "range" and isinstance(content, list) and len(content) == 2 and \
                all(type(v) is int for v in content) and content[0] <= header.seq - 1 <= content[1]:
            return True
        if form == "exchange" and content in header.links_to(CITES):
            return True
    return False


def _retention(headers: list[Header], ids: list[str], i: int, now_ns: int) -> str:
    """Go's ``retentionOf``: the header's state folded through every later
    lifecycle record citing it; an AVAILABLE or PARTIAL record under a live
    retention commitment reads LEGAL_HOLD."""
    header, record_id = headers[i], ids[i]
    state = header.retention_state
    for later in headers[header.seq:]:
        if later.record_type != _LIFECYCLE or record_id not in later.links_to(CITES):
            continue
        # As Go reads it into a LifecycleStatement: no statement, or no
        # retention_state, is the empty state; another shape is corrupt.
        statement = {} if later.statement is None else later.statement
        if not isinstance(statement, dict) or not isinstance(statement.get("retention_state", ""), (str, type(None))):
            raise CorruptError(f"lifecycle record {ids[later.seq - 1]} holds no lifecycle statement")
        state = statement.get("retention_state") or ""
    if state == _DELETED:
        return state
    held = state == _LEGAL_HOLD or _held_by(headers, ids, record_id, header, now_ns)
    return _LEGAL_HOLD if held and state in (AVAILABLE, _PARTIAL) else state


# -- the shelf ------------------------------------------------------------------------


class Shelf:
    """One subject key's books in one SQLite file (§2).

    The shelf holds the profile's writer lock from :meth:`open_sqlite` to
    :meth:`release` and is the profile's only writer. It keeps one book per
    log id for its lifetime: a book allocates positions from its own order,
    so two handles on one log could give two records one position.

    ``signer`` seals every book's records and ``checkpoint_key`` signs every
    log's checkpoints. ``originals`` is the shelf's one artifact store
    (capsule-emit's SQLite backend on the shelf's file, under one namespace).
    ``authenticated_indexes`` gives a book its indexes when it opens: ``None``
    (or a ``None`` result) the defaults, an empty list none. ``random`` is
    the shelf's one random source (``os.urandom`` when ``None``); ``now`` the
    clock every book reads.
    """

    def __init__(self, *, path: str, db: sqlite3.Connection, unlock: Callable[[], None], operator: str,
                 developer: str, signer: Signer, checkpoint_key: Signer, originals: Any, random: RandomSource,
                 now: Callable[[], datetime] | None,
                 authenticated_indexes: Callable[[str], Sequence[Any] | None] | None):
        self._path = path
        self._db = db
        self._db_lock = threading.RLock()
        self._unlock = unlock
        self._operator, self._developer = operator, developer
        self._signer, self._checkpoint_key = signer, checkpoint_key
        self._originals = _SerialOriginals(originals)
        self.random = random
        self._now = now
        self._authenticated = authenticated_indexes
        self._mu = threading.RLock()
        self._books: dict[str, Any] = {}
        self._hosts: dict[str, HostLog] = {}
        self._cadence_logs: list[SqliteSubstrate] = []
        self._cadence = None
        self._released = False

    @classmethod
    def open_sqlite(cls, path: str | os.PathLike, *, operator: str, signer: Signer, checkpoint_key: Signer,
                    originals: Any, developer: str = "", lock_path: str | None = None,
                    held_lock: HeldLock | None = None, random: Reader | None = None,
                    now: Callable[[], datetime] | None = None,
                    authenticated_indexes: Callable[[str], Sequence[Any] | None] | None = None,
                    lock_timeout: float | None = None) -> Shelf:
        """Take the writer lock, waiting up to ``lock_timeout`` seconds for
        another holder (forever when ``None``), or check the host's
        ``held_lock``; open the file and create the shelf's tables when absent.
        ``lock_path`` defaults to ``<path>.lock``; a host whose own writers
        lock another file passes it. A file whose ``shelf_meta`` version is
        unknown is refused (:class:`UnsupportedError`); one with no row is a
        fresh file or a shelf from v0.12.0 or earlier and gets version 1,
        with ``shelf_logs`` filled from what it holds (§2.2)."""
        path = os.fspath(path)
        if not path or not operator or signer is None or checkpoint_key is None or originals is None:
            raise InvalidError("path, operator, signer, checkpoint key and originals are required")
        if held_lock is not None:
            if lock_path:
                raise InvalidError("a shelf on a host-held lock takes no lock path")
            try:
                held_lock.held()
            except Exception as exc:
                raise InvalidError(f"the host's writer lock is not held: {exc}") from None
            unlock: Callable[[], None] = lambda: None
        else:
            unlock = _flock(lock_path or path + ".lock", lock_timeout)
        try:
            db = _connect(path)
        except BaseException:
            unlock()
            raise
        try:
            _init(db)
        except BaseException:
            db.close()
            unlock()
            raise
        return cls(path=path, db=db, unlock=unlock, operator=operator, developer=developer, signer=signer,
                   checkpoint_key=checkpoint_key, originals=originals, random=RandomSource(random), now=now,
                   authenticated_indexes=authenticated_indexes)

    # -- the file -----------------------------------------------------------------------

    @contextmanager
    def _tx(self) -> Iterator[sqlite3.Connection]:
        with self._db_lock:
            self._db.execute("BEGIN")
            try:
                yield self._db
            except BaseException:
                self._db.execute("ROLLBACK")
                raise
            self._db.execute("COMMIT")

    @contextmanager
    def _read(self) -> Iterator[sqlite3.Connection]:
        with self._db_lock:
            yield self._db

    def _check_open(self) -> None:
        if self._released:
            raise ReleasedError("the shelf is released")

    @property
    def path(self) -> str:
        return self._path

    @property
    def originals(self) -> Any:
        return self._originals

    # -- books ----------------------------------------------------------------------------

    def open(self, log_id: str):
        """The book whose log id is ``log_id``, created on first use; the same
        book for the shelf's lifetime. A host log's id is refused."""
        with self._mu:
            return self._open_locked(log_id)

    def _open_locked(self, log_id: str):
        from .book import Book

        self._check_open()
        if log_id in self._books:
            return self._books[log_id]
        with self._read() as db:
            kind = _kind_of(db, log_id)
        if log_id in self._hosts or kind == _KIND_HOST:
            raise InvalidError(f"{log_id} is a host log, not a book")
        substrate = SqliteSubstrate.open(self._path, log_id, self._checkpoint_key)
        try:
            book = Book(log_id, self._operator, signer=self._signer, substrate=substrate,
                        store=SqliteStore(self, log_id), originals=self._originals, developer=self._developer,
                        now=self._now, indexes=self._authenticated(log_id) if self._authenticated else None)
        except BaseException:
            substrate.close()
            raise
        self._books[log_id] = book
        return book

    def books(self) -> list[str]:
        """The ids of the books that hold records, in id order."""
        with self._mu:
            self._check_open()
            with self._read() as db:
                return [row[0] for row in db.execute("SELECT DISTINCT book_id FROM book_records ORDER BY book_id")]

    def records(self, *book_ids: str) -> ShelfRecords:
        """The committed records of the shelf's books (or of ``book_ids``),
        in book id then seq order, without opening a book: nothing is
        recovered, verified or written. A record is committed when ``1 <= seq
        <= its book's log entry count``; records above it are counted as
        pending."""
        with self._mu:
            self._check_open()
            query, args = "SELECT book_id, seq, record_id, header FROM book_records", list(book_ids)
            if book_ids:
                query += " WHERE book_id IN (" + ", ".join("?" * len(book_ids)) + ")"
            with self._read() as db:
                rows = db.execute(query + " ORDER BY book_id, seq", args).fetchall()
                entries = dict(db.execute("SELECT log_id, COUNT(*) FROM cll_entries GROUP BY log_id").fetchall()) \
                    if db.execute("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'cll_entries'"
                                  ).fetchone()[0] else {}
            now = self._now() if self._now else datetime.now(timezone.utc)
            now_ns = int(utc(now).timestamp()) * 1_000_000_000 + utc(now).microsecond * 1_000
            out = ShelfRecords()
            i = 0
            while i < len(rows):
                book, committed = rows[i][0], entries.get(rows[i][0], 0)
                headers: list[Header] = []
                ids: list[str] = []
                while i < len(rows) and rows[i][0] == book:
                    _, seq, record_id, header = rows[i]
                    i += 1
                    if seq < 1 or seq > committed:
                        out.pending[book] = out.pending.get(book, 0) + 1
                        continue
                    if seq != len(ids) + 1:
                        raise CorruptError(f"{book} holds record {record_id} at seq {seq} after {len(ids)} records")
                    try:
                        headers.append(Header.from_value(json.loads(bytes(header))))
                    except (ValueError, TypeError, KeyError, AttributeError) as exc:
                        raise CorruptError(f"header of {record_id} in {book}: {exc}") from None
                    ids.append(record_id)
                for j, header in enumerate(headers):
                    retention = ""
                    if header.payload_commitments or header.field_commitments:
                        retention = _retention(headers, ids, j, now_ns)
                    out.records.append(ShelfRecord(book, header.seq, ids[j], header, retention))
            return out

    # -- host logs ----------------------------------------------------------------------

    def legacy(self, log_id: str) -> HostLog:
        """Open an existing log of the file that is not a book and register it
        with the cadence. It never provisions a log; a book's id is refused."""
        with self._mu:
            return self._host_locked(log_id, in_cadence=True, provision=False)

    def host_log(self, log_id: str, in_cadence: bool) -> HostLog:
        """Open ``log_id`` as a host log, provisioning its CLL log when the
        file holds nothing of it. ``in_cadence`` registers it with the cadence
        as :meth:`legacy` does; otherwise the host checkpoints and witnesses it
        on its backend. The same host log for the shelf's lifetime; an id
        opened before in the other mode, here or as the file records it, or a
        book's id, is refused."""
        with self._mu:
            return self._host_locked(log_id, in_cadence=in_cadence, provision=True)

    def _host_locked(self, log_id: str, *, in_cadence: bool, provision: bool) -> HostLog:
        self._check_open()
        host = self._hosts.get(log_id)
        if host is not None:
            if host.in_cadence != in_cadence:
                raise InvalidError(f"host log {log_id} is already open {'in' if host.in_cadence else 'outside'} the cadence")
            return host
        if log_id in self._books:
            raise InvalidError(f"{log_id} is a book, not a host log")
        if self._cadence is not None and self._cadence.log_id == log_id:
            raise InvalidError(f"{log_id} is the cadence log")
        with self._read() as db:
            if _kind_of(db, log_id) == _KIND_BOOK or _record_count(db, log_id):
                raise InvalidError(f"{log_id} is a book, not a host log")
        log = SqliteSubstrate.open(self._path, log_id, self._checkpoint_key, provision=provision)
        try:
            with self._tx() as db:
                held = _kind_of(db, log_id)
                known = db.execute("SELECT COUNT(*) FROM cadence_logs WHERE log_id = ?", (log_id,)).fetchone()[0]
                # The mode is durable without a column of its own: a host log in
                # the cadence is in cadence_logs from the transaction that first
                # opens it (rows are never deleted), and one outside it never is.
                if not in_cadence and known:
                    raise InvalidError(f"host log {log_id} is in the cadence")
                if in_cadence and held == _KIND_HOST and not known:
                    raise InvalidError(f"host log {log_id} is outside the cadence")
                _claim_kind(db, log_id, _KIND_HOST)
                if in_cadence and not known:
                    _mark_live(db, log_id)
        except BaseException:
            log.close()
            raise
        host = HostLog(self, log_id, log, in_cadence)
        self._hosts[log_id] = host
        return host

    def _cadence_log_locked(self, log_id: str):
        """The cadence's view of a live log: its book when it has records,
        else its host log."""
        with self._read() as db:
            records = _record_count(db, log_id)
        if records:
            return _BookLog(self._open_locked(log_id))
        return self._host_locked(log_id, in_cadence=True, provision=False)

    # -- the cadence --------------------------------------------------------------------

    def cadence(self, config):
        """Open the shelf's cadence (:class:`evidencebook.cadence.CadenceConfig`),
        creating its log when absent. One cadence per shelf; its log is never
        a book or a host log."""
        from .cadence import Cadence

        config = config.checked()
        with self._mu:
            self._check_open()
            if self._cadence is not None:
                raise InvalidError(f"the shelf already runs a cadence ({self._cadence.log_id})")
            with self._read() as db:
                kind = _kind_of(db, config.log_id)
            book = config.log_id in self._books or kind == _KIND_BOOK
            if book or config.log_id in self._hosts or kind:
                raise InvalidError(f"{config.log_id} is a {'book' if book else 'host'} log, not the cadence log")
            log = SqliteSubstrate.open(self._path, config.log_id, self._checkpoint_key, config.witness_ids)
            self._cadence_logs.append(log)
            self._cadence = Cadence(self, config, log)
            return self._cadence

    # -- release ------------------------------------------------------------------------

    def release(self) -> None:
        """Release every book and log the shelf opened, close its file and
        give up the writer lock. Books from this shelf refuse writes after."""
        with self._mu:
            if self._released:
                return
            self._released = True
            errors: list[BaseException] = []
            closers = [book.close for book in self._books.values()]
            closers += [host.substrate.close for host in self._hosts.values()]
            closers += [log.close for log in self._cadence_logs]
            closers += [self._db.close, self._unlock]
            for close in closers:
                try:
                    close()
                except BaseException as exc:  # noqa: BLE001 -- every handle is released
                    errors.append(exc)
            if errors:
                raise errors[0]

    def __enter__(self) -> Shelf:
        return self

    def __exit__(self, *exc: object) -> None:
        self.release()


def _init(db: sqlite3.Connection) -> None:
    """Create the shelf's tables when absent, in one transaction, and apply
    §2.2's fill rules; refuse an unknown version before writing anything."""
    db.execute("BEGIN")
    try:
        def has(table: str) -> bool:
            return bool(db.execute("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
                                   (table,)).fetchone()[0])

        versioned = False
        if has("shelf_meta"):
            row = db.execute("SELECT version FROM shelf_meta WHERE id = 1").fetchone()
            if row is not None:
                if row[0] != SHELF_VERSION:
                    raise UnsupportedError(f"shelf file version {row[0]} (this build opens version {SHELF_VERSION})")
                versioned = True
        had_logs = has("shelf_logs")
        for statement in SCHEMA.split(";"):
            if statement.strip():
                db.execute(statement)
        if not versioned:
            db.execute("INSERT INTO shelf_meta (id, version) VALUES (1, ?)", (SHELF_VERSION,))
        if not had_logs:
            db.execute("INSERT INTO shelf_logs (log_id, kind) SELECT DISTINCT book_id, 'book' FROM book_records")
            db.execute("INSERT INTO shelf_logs (log_id, kind) SELECT log_id, 'host' FROM cadence_logs "
                       "WHERE log_id NOT IN (SELECT book_id FROM book_records)")
    except BaseException:
        db.execute("ROLLBACK")
        raise
    db.execute("COMMIT")
