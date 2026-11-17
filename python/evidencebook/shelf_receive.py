# SPDX-License-Identifier: Apache-2.0
"""The store of what the other side pushes (evidencebook#461,
docs/design/461-shelf-facts.md §3), behaviour for behaviour with Go's
``shelf_receive.go`` and the proof part of ``shelf_stamp_proof.go``.

The records a counterparty, an adjudicator or an offerer sends, and the
stamp proofs that cover them. The library knows no record format: a
:class:`ReceiveProfile` reads each record (its signature, sender, log
position, predecessor and deal) and says who may push it. The store owns the
rest: the duplicate, the counted prefix and its gaps, forks and their
evidence, the bounds, the proofs, retention and tombstones, and the deal ids
each call changed. Nothing in the store is appended to a book, and the store
needs no shelf of book facts: it works on a version 1 shelf as on a migrated
one.

Every state the calls report is recomputed from the tables (a restart
recomputes the same counted prefix). Two facts are kept rather than derived,
because deleting a record must not undo them: each conflict found (a fork at
a position, a contradicting checkpoint at a size), with its one evidence row,
and whether each held proof's root recomputed from the records
(``shelf_received_proofs.verified``).

Locks and connections. Go's shelf handle keeps a pool of connections and
Receive takes a mutex of its own (``receiveMu``), never the shelf's. This
package's shelf has one connection, guarded by a re-entrant lock held only
for the length of one read or one transaction, and the shelf's re-entrant
mutex. So that the readers (:func:`counted_prefix`,
:func:`received_record_of`, :func:`evidence`) take neither, and may be called
from a status provider during a stamp, or from a profile during a Receive,
in any thread, each opens its own short-lived connection to the file
(WAL: it reads the last committed state while a write transaction is open,
as Go's pooled readers do). Receive and Forget serialize on the shelf's own
``_receive_mu``; they call the profile and the settled check holding no
database lock and no transaction.
"""
from __future__ import annotations

import hashlib
import os
import re
import sqlite3
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from dataclasses import dataclass, field, replace
from datetime import datetime, timedelta, timezone
from typing import Any, Protocol
from urllib.parse import quote

from cll.checkpoint import core

from ._cll import Checkpoint, read_checkpoint
from ._gojson import go_json
from .errors import BusyError, CorruptError, DeletedError, InvalidError, NotFoundError, OverLimitError, \
    RefusedError, ReleasedError
from .shelf_handover import _Nodes
from .shelf_stamp_proof import StampProof, verify_stamp_proof

__all__ = [
    "DEFAULT_RECEIVE_BOUNDS", "EVIDENCE_CHECKPOINT", "EVIDENCE_CHECKPOINT_RECORDS", "EVIDENCE_FORK", "CountedPrefix",
    "Evidence", "HeldRecord", "ProofOutcome", "ReceiveBounds", "ReceiveChanges", "ReceiveConfig", "ReceiveProfile",
    "ReceiveResult", "ReceivedRecord", "RecordOutcome",
]

#: Evidence kinds (``shelf_evidence.kind``): two records a sender signed at
#: one position (a the record held first, b the other; a record whose
#: predecessor is another than the one held at that position is a fork
#: too); two book checkpoints a sender signed for one log at one size with
#: different roots (a the one held, b the other); and a book checkpoint
#: whose root the records held do not recompute (a the checkpoint, b the
#: JSON array of the digests it was recomputed over). Only the first
#: conflict at a position or size is kept.
EVIDENCE_FORK, EVIDENCE_CHECKPOINT, EVIDENCE_CHECKPOINT_RECORDS = "fork", "checkpoint", "checkpoint_records"

# Proof states (shelf_received_proofs.verified): not yet reached by the
# records held, its root recomputed from them, or contradicted by them.
_PROOF_UNCHECKED, _PROOF_RECOMPUTED, _PROOF_CONTRADICTED = 0, 1, 2

#: How many times Receive makes room for an offer while the holder's books
#: change under it before it refuses (:class:`BusyError`).
OFFER_ATTEMPTS = 3

_DIGEST = re.compile(r"[0-9a-f]{64}")
_TIME = re.compile(r"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})\.(\d{9})Z")

#: When set (tests), runs at the end of each part's transaction, before its
#: commit, with the part's name ("record" or "proof"); an exception it
#: raises rolls the part back, as a failed commit does (Go
#: ``receiveBeforeCommit``).
_before_commit: Callable[[str], None] | None = None


def _is_digest(s: Any) -> bool:
    return isinstance(s, str) and _DIGEST.fullmatch(s) is not None


# -- the profile and the configuration ------------------------------------------------


@dataclass(frozen=True)
class ReceivedRecord:
    """What the store needs of a received record (Go ``ReceivedRecord``).

    ``digest`` is the record's entry in its sender's book log (64 lowercase
    hex, unique across senders); ``sender_key`` the key the sender signs
    with; ``log_id`` and ``position`` place it in the sender's book log, the
    first record at 1; ``prev`` the digest of the record at ``position - 1``
    it follows, ``""`` exactly at position 1; ``deal_id`` the deal it belongs
    to (what Receive reports as changed and Forget deletes by); ``offer``
    marks an unsolicited offer, which has its own bounds."""

    digest: str
    sender_key: str
    log_id: str
    position: int
    prev: str
    deal_id: str
    offer: bool = False

    def _validate(self) -> str:
        if not _is_digest(self.digest):
            return "its digest is not 64 lowercase hex characters"
        if not all(isinstance(v, str) and v for v in (self.sender_key, self.log_id, self.deal_id)):
            return "it names no sender, log or deal"
        if isinstance(self.position, bool) or not isinstance(self.position, int) or not 0 <= self.position < 2**63:
            return "its position is not an integer"
        if not isinstance(self.prev, str) or not isinstance(self.offer, bool):
            return "its predecessor or offer mark is of another type"
        if self.position == 0:
            return "its position is 0; the first is 1"
        if self.position == 1 and self.prev != "":
            return "the first record names a predecessor"
        if self.position > 1 and not _is_digest(self.prev):
            return "it names no predecessor digest"
        return ""


class ReceiveProfile(Protocol):
    """What a profile (today x-deal-v0's records) tells the store about
    received records (Go ``ReceiveProfile``). The store calls it outside the
    shelf's mutex and outside any transaction of its own, so it may read the
    shelf; it must not call ``receive`` or ``forget``."""

    def read(self, record: bytes) -> ReceivedRecord:
        """Check a received record's signature and return what the store
        needs of it. An exception refuses the record as invalid."""

    def admit(self, r: ReceivedRecord) -> bool:
        """The push rule (§3, "who may push"): whether this sender may push
        this record now. An offer may come from any key; any other record
        only from a key a book on the shelf names as its counterparty or
        adjudicator, whose offer our side answered, or to which an offer in
        the journal was sent."""

    def answered(self, offer: ReceivedRecord) -> bool:
        """Whether our side has answered a stored offer. The store asks about
        each offer it would drop before its write transaction, under a mark
        of the holder's own books; if the mark moved by the time the
        transaction holds the write lock, it asks again (:class:`BusyError`
        after three tries). An answered offer leaves the offer bounds and is
        never dropped."""

    def proof_sender(self, checkpoint_key_id: str, log_id: str) -> tuple[str, bool]:
        """Relate a stamp proof's key and the book log it names to the
        sender key its records are held under, and say whether that sender
        may push a proof now: ``(sender_key, ok)``; ok false refuses it."""


@dataclass(frozen=True)
class ReceiveBounds:
    """The store's bounds (§3; Go ``ReceiveBounds``). A member of 0 takes
    its default (:data:`DEFAULT_RECEIVE_BOUNDS`): a record's bytes (1 MiB);
    records held from one sender and log (100000); unanswered unsolicited
    offers per sender (16, 4 MiB) and in total (1024, 64 MiB), reaching one
    dropping the oldest unanswered offer first; and what unsolicited offers
    leave behind, per sender (16) and in total (1024) rows."""

    max_record_bytes: int = 0
    max_records_per_log: int = 0
    max_offers_per_sender: int = 0
    max_offer_bytes_per_sender: int = 0
    max_offers: int = 0
    max_offer_bytes: int = 0
    max_offer_evidence_per_sender: int = 0
    max_offer_evidence: int = 0

    def with_defaults(self) -> ReceiveBounds:
        d = DEFAULT_RECEIVE_BOUNDS
        return ReceiveBounds(**{name: getattr(self, name) if getattr(self, name) > 0 else getattr(d, name)
                                for name in self.__dataclass_fields__})


DEFAULT_RECEIVE_BOUNDS = ReceiveBounds(
    max_record_bytes=1 << 20, max_records_per_log=100000, max_offers_per_sender=16, max_offer_bytes_per_sender=4 << 20,
    max_offers=1024, max_offer_bytes=64 << 20, max_offer_evidence_per_sender=16, max_offer_evidence=1024)


@dataclass(frozen=True)
class ReceiveConfig:
    """The shelf's store of received records (Go ``ReceiveConfig``):
    ``profile`` reads its records (``receive`` refuses every call without
    one), ``bounds`` are its bounds, and ``retention`` is how long a received
    record is kept once its deal is settled, the holder's own setting; zero
    is the life of the shelf, so nothing is ever deleted."""

    profile: ReceiveProfile | None = None
    bounds: ReceiveBounds = field(default_factory=ReceiveBounds)
    retention: timedelta = timedelta(0)


# -- results --------------------------------------------------------------------------


@dataclass
class RecordOutcome:
    """What became of a received record (Go ``RecordOutcome``): stored by
    this call, a duplicate or deleted record acknowledged, counted within
    its sender's counted prefix or asking for the sender's book; ``refused``
    is why it was refused (:class:`InvalidError`, :class:`RefusedError`,
    :class:`OverLimitError` or :class:`BusyError`), nothing of it stored."""

    digest: str = ""
    stored: bool = False
    duplicate: bool = False
    deleted: bool = False
    counted: bool = False
    ask_for_book: bool = False
    refused: Exception | None = None


@dataclass
class ProofOutcome:
    """What became of a received stamp proof (Go ``ProofOutcome``)."""

    accepted: bool = False
    duplicate: bool = False
    refused: Exception | None = None


@dataclass(frozen=True)
class ReceiveChanges:
    """The deal ids a Receive call changed, each list sorted: records that
    entered (``grew``) or left (``shrank``) their sender's counted prefix,
    new fork or checkpoint evidence (``forked``), and records a held proof
    newly covers (``covered``)."""

    grew: list[str] = field(default_factory=list)
    shrank: list[str] = field(default_factory=list)
    forked: list[str] = field(default_factory=list)
    covered: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class ReceiveResult:
    """What one Receive call did, part by part: ``record`` and ``proof`` are
    ``None`` for a part not given."""

    record: RecordOutcome | None = None
    proof: ProofOutcome | None = None
    changes: ReceiveChanges = field(default_factory=ReceiveChanges)

    @property
    def refusals(self) -> list[Exception]:
        """Each part's refusal, the record's first: what Go joins into
        Receive's returned error."""
        return [o.refused for o in (self.record, self.proof) if o is not None and o.refused is not None]


@dataclass(frozen=True)
class CountedPrefix:
    """A sender's counted prefix in one book log: ``size`` records from
    position 1, the last one's digest (``""`` for none), and the lowest
    position with a fork (0 for none), before which it ends for good."""

    size: int = 0
    last: str = ""
    fork_at: int = 0


@dataclass(frozen=True)
class HeldRecord:
    """A received record the store holds."""

    received: ReceivedRecord
    record: bytes
    received_at: datetime


@dataclass(frozen=True)
class Evidence:
    """One row of the store's evidence."""

    kind: str
    a: bytes
    b: bytes
    noted_at: datetime


class _ChangeSet:
    def __init__(self) -> None:
        self.grew: set[str] = set()
        self.shrank: set[str] = set()
        self.forked: set[str] = set()
        self.covered: set[str] = set()

    def merge(self, other: _ChangeSet) -> None:
        for name in ("grew", "shrank", "forked", "covered"):
            getattr(self, name).update(getattr(other, name))

    def result(self) -> ReceiveChanges:
        order = lambda s: sorted(s, key=lambda d: d.encode())  # noqa: E731  Go sort.Strings
        return ReceiveChanges(order(self.grew), order(self.shrank), order(self.forked), order(self.covered))


# -- time -----------------------------------------------------------------------------


def _format(moment: datetime) -> str:
    """The stored time form, fixed width so rows sort by it."""
    moment = moment.astimezone(timezone.utc) if moment.tzinfo else moment.replace(tzinfo=timezone.utc)
    return moment.strftime("%Y-%m-%dT%H:%M:%S") + f".{moment.microsecond * 1000:09d}Z"


def _parse(text: str, what: str) -> datetime:
    m = _TIME.fullmatch(text) if isinstance(text, str) else None
    if m is None:
        raise CorruptError(f"{what}: {text!r} is not the stored time form")
    base = datetime.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S").replace(tzinfo=timezone.utc)
    return base + timedelta(microseconds=int(m.group(2)) // 1000)


def _now(shelf: Any) -> str:
    return _format(shelf._now() if shelf._now else datetime.now(timezone.utc))


# -- the readers' connections ---------------------------------------------------------


def _live(shelf: Any) -> None:
    """The released check of the received store, without the shelf's mutex."""
    if shelf._released:
        raise ReleasedError("the shelf is released")


@contextmanager
def _reader(shelf: Any) -> Iterator[sqlite3.Connection]:
    """A connection of its own on the shelf's file, for one read, taking no
    lock of the shelf's."""
    _live(shelf)
    uri = f"file:{quote(os.path.abspath(shelf.path))}?mode=rw"
    db = sqlite3.connect(uri, uri=True, timeout=5.0, isolation_level=None, check_same_thread=False)
    try:
        db.execute("PRAGMA busy_timeout=5000")
        yield db
    finally:
        db.close()


# -- the counted prefix ---------------------------------------------------------------


@dataclass(frozen=True)
class _Row:
    """A stored record's columns but its bytes."""

    digest: str
    sender: str
    log: str
    position: int
    prev: str
    deal: str
    offer: bool

    def record(self) -> ReceivedRecord:
        return ReceivedRecord(self.digest, self.sender, self.log, self.position, self.prev, self.deal, self.offer)


_COLUMNS = "digest, sender_key, log_id, position, prev, deal_id, offer"


def _row(values: tuple) -> _Row:
    digest, sender, log, position, prev, deal, offer = values[:7]
    return _Row(digest, sender, log, position, prev, deal, bool(offer))


@dataclass(frozen=True)
class _Prefix:
    """A sender's counted prefix in one log: the counted records (digest,
    deal) by position from 1, the lowest fork position (0 for none) and the
    deal ids of every record held from the sender in the log."""

    counted: list[tuple[str, str]]
    fork_at: int
    deals: list[str]

    def counts(self, position: int, digest: str) -> bool:
        return 1 <= position <= len(self.counted) and self.counted[position - 1][0] == digest

    def digests(self, n: int) -> list[str]:
        return [d for d, _ in self.counted[:n]]


def _prefix(db: sqlite3.Connection, sender: str, log: str) -> _Prefix:
    """Go ``receivedPrefix``: the forks are the ones recorded, which deleting
    a record never undoes, and any the records held show: a position whose
    claimed digests differ (the records held there, and the predecessor each
    record at the next position names)."""
    held = [_row(r) for r in db.execute(f"SELECT {_COLUMNS} FROM shelf_received WHERE sender_key = ? AND log_id = ? "
                                        "ORDER BY position, digest", (sender, log))]
    fork_at = db.execute("SELECT COALESCE(MIN(at), 0) FROM shelf_received_conflicts WHERE sender_key = ? AND log_id = ? "
                         "AND kind = ?", (sender, log, EVIDENCE_FORK)).fetchone()[0]
    at: dict[int, list[_Row]] = {}
    claims: dict[int, set[str]] = {}
    deals: set[str] = set()
    for r in held:
        at.setdefault(r.position, []).append(r)
        claims.setdefault(r.position, set()).add(r.digest)
        if r.position > 1:
            claims.setdefault(r.position - 1, set()).add(r.prev)
        deals.add(r.deal)
    for p, c in claims.items():
        if len(c) > 1 and (fork_at == 0 or p < fork_at):
            fork_at = p
    counted = []
    p = 1
    while len(at.get(p, ())) == 1 and (fork_at == 0 or p < fork_at):
        counted.append((at[p][0].digest, at[p][0].deal))
        p += 1
    return _Prefix(counted, fork_at, sorted(deals, key=lambda d: d.encode()))


def _leaf_count(size: int) -> int | None:
    try:
        return core.leaf_count(size)
    except Exception:  # not a complete MMR
        return None


def _coverage(db: sqlite3.Connection, sender: str, log: str, p: _Prefix) -> int:
    """How many counted records a held proof whose root they recomputed covers."""
    size = db.execute("SELECT COALESCE(MAX(size), 0) FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? "
                      "AND verified = ?", (sender, log, _PROOF_RECOMPUTED)).fetchone()[0]
    leaves = _leaf_count(size)
    if leaves is None:
        raise CorruptError(f"a held proof's size {size} is not a complete MMR")
    return min(leaves, len(p.counted))


@dataclass(frozen=True)
class _LogState:
    prefix: _Prefix
    coverage: int


def _log_state(db: sqlite3.Connection, key: tuple[str, str]) -> _LogState:
    p = _prefix(db, *key)
    return _LogState(p, _coverage(db, *key, p))


def _report(before: _LogState, after: _LogState, changes: _ChangeSet) -> None:
    """Add what changed from before to after: deals with records that
    entered or left the counted prefix, and deals with counted records the
    verified coverage newly reaches."""
    was = {d for d, _ in before.prefix.counted}
    now = {d for d, _ in after.prefix.counted}
    for d, deal in after.prefix.counted:
        if d not in was:
            changes.grew.add(deal)
    for d, deal in before.prefix.counted:
        if d not in now:
            changes.shrank.add(deal)
    for i in range(before.coverage, after.coverage):
        changes.covered.add(after.prefix.counted[i][1])


def _root_recomputes(cp: Checkpoint, digests: list[str]) -> tuple[bool, bytes]:
    """Whether records recompute a book checkpoint's root, given exactly its
    entries' digests, and the digests as a JSON array."""
    nodes = _Nodes()
    for d in digests:
        core.add_leaf(nodes, core.leaf_hash(bytes.fromhex(d)))
    if cp.tree_size > nodes.size():
        raise CorruptError(f"{len(digests)} records do not reach a checkpoint of size {cp.tree_size}")
    root = core.root_from_peaks([nodes.node(p) for p in core.peaks(cp.tree_size)])
    return root.hex() == cp.root, go_json(digests).encode()


def _read_cp(statement: bytes) -> Checkpoint:
    try:
        return read_checkpoint(bytes(statement))
    except InvalidError as exc:
        raise CorruptError(f"a held checkpoint: {exc}") from None


# -- conflicts, vacated positions and held proofs --------------------------------------


def _offer_room(db: sqlite3.Connection, sender: str, bounds: ReceiveBounds) -> bool:
    """Whether one more row an unsolicited offer leaves behind fits the
    offer bounds: the dropped offers' positions and the conflicts in logs of
    offers alone, per sender and in total."""
    mine, total = db.execute(
        "SELECT COALESCE(SUM(sender_key = ?), 0), COUNT(*) FROM ("
        "SELECT sender_key FROM shelf_received_vacated WHERE offer = 1 "
        "UNION ALL SELECT sender_key FROM shelf_received_conflicts WHERE offer = 1)", (sender,)).fetchone()
    return mine < bounds.max_offer_evidence_per_sender and total < bounds.max_offer_evidence


def _note_conflict(db: sqlite3.Connection, key: tuple[str, str], kind: str, at: int, a: bytes, b: bytes, now: str,
                   offers: bool, bounds: ReceiveBounds) -> bool:
    """Record a conflict of one kind at a position or size of a sender's log
    the first time only, with its evidence. A conflict in a log of offers
    alone counts under the offer bounds; past them neither the conflict nor
    its evidence is kept. Whether the conflict is new: recorded by this call,
    or past the bounds."""
    if db.execute("SELECT COUNT(*) FROM shelf_received_conflicts WHERE sender_key = ? AND log_id = ? AND kind = ? "
                  "AND at = ?", (*key, kind, at)).fetchone()[0]:
        return False
    if offers and not _offer_room(db, key[0], bounds):
        return True
    cursor = db.execute("INSERT INTO shelf_evidence (kind, a, b, noted_at) VALUES (?, ?, ?, ?)",
                        (kind, bytes(a), bytes(b), now))
    db.execute("INSERT INTO shelf_received_conflicts (sender_key, log_id, kind, at, evidence_id, offer) "
               "VALUES (?, ?, ?, ?, ?, ?)", (*key, kind, at, cursor.lastrowid, int(offers)))
    return True


def _vacate(db: sqlite3.Connection, rows: list[_Row], offers: bool, bounds: ReceiveBounds) -> None:
    """Remember the positions deleted records held, so another record
    arriving there later is a fork, and return every proof of their logs
    that reaches a deleted position to unchecked. A dropped offer's row
    counts under the offer bounds, and past them is not kept; a row Forget
    leaves is kept for good."""
    lowest: dict[tuple[str, str], int] = {}
    for r in rows:
        if not offers or _offer_room(db, r.sender, bounds):
            db.execute("INSERT OR IGNORE INTO shelf_received_vacated (sender_key, log_id, position, digest, deal_id, "
                       "offer) VALUES (?, ?, ?, ?, ?, ?)", (r.sender, r.log, r.position, r.digest, r.deal, int(offers)))
        key = (r.sender, r.log)
        if key not in lowest or r.position < lowest[key]:
            lowest[key] = r.position
    for (sender, log), position in lowest.items():
        sizes = [s for (s,) in db.execute("SELECT DISTINCT size FROM shelf_received_proofs WHERE sender_key = ? "
                                          "AND log_id = ? AND verified = ?", (sender, log, _PROOF_RECOMPUTED))]
        for size in sizes:
            if (_leaf_count(size) or 0) >= position:
                db.execute("UPDATE shelf_received_proofs SET verified = ? WHERE sender_key = ? AND log_id = ? "
                           "AND size = ?", (_PROOF_UNCHECKED, sender, log, size))


def _check_held_proofs(db: sqlite3.Connection, key: tuple[str, str], p: _Prefix, now: str, changes: _ChangeSet) -> None:
    """Check every unchecked held proof whose size the prefix now reaches
    against the records: one whose root they recompute grants coverage, one
    whose root they do not is kept as evidence and its deals reported
    forked."""
    held = db.execute("SELECT DISTINCT size, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? "
                      "AND log_id = ? AND verified = ? ORDER BY size", (*key, _PROOF_UNCHECKED)).fetchall()
    for size, statement in held:
        cp = _read_cp(statement)
        if cp.entries > len(p.counted):
            continue
        match, over = _root_recomputes(cp, p.digests(cp.entries))
        state = _PROOF_RECOMPUTED
        if not match:
            state = _PROOF_CONTRADICTED
            if _note_conflict(db, key, EVIDENCE_CHECKPOINT_RECORDS, size, statement, over, now, False, ReceiveBounds()):
                changes.forked.update(p.deals)
        db.execute("UPDATE shelf_received_proofs SET verified = ? WHERE sender_key = ? AND log_id = ? AND size = ?",
                   (state, *key, size))


@dataclass(frozen=True)
class _HeldFork:
    position: int
    held: bytes
    deal: str


def _fork_evidence(db: sqlite3.Connection, r: ReceivedRecord) -> list[_HeldFork]:
    """The held records ``r`` contradicts (§3): another record at its
    position, or a record at the next position naming another predecessor;
    and, at the position before, a record other than the predecessor ``r``
    names, or another record there naming another one. A record deleted at
    either position claims its digest there still."""
    checks = [(r.position, r.digest)]
    if r.position > 1:
        checks.append((r.position - 1, r.prev))
    out = []
    for position, digest in checks:
        found = None
        for d, prev, at, deal, b in db.execute(
                "SELECT digest, prev, position, deal_id, record FROM shelf_received WHERE sender_key = ? AND log_id = ? "
                "AND position IN (?, ?) ORDER BY position, digest", (r.sender_key, r.log_id, position, position + 1)):
            claimed = prev if at == position + 1 else d
            if claimed != digest and found is None:
                found = _HeldFork(position, bytes(b), deal)
        if found is None:
            row = db.execute("SELECT digest, deal_id FROM shelf_received_vacated WHERE sender_key = ? AND log_id = ? "
                             "AND position = ? AND digest != ? ORDER BY digest LIMIT 1",
                             (r.sender_key, r.log_id, position, digest)).fetchone()
            if row is not None:
                found = _HeldFork(position, row[0].encode(), row[1])
        if found is not None:
            out.append(found)
    return out


# -- offers ---------------------------------------------------------------------------


@dataclass(frozen=True)
class _Mark:
    """A monotonic mark of the holder's own books (the journal and every
    deal book): their records and log entries."""

    records: int
    entries: int


def _own_mark(db: sqlite3.Connection) -> _Mark:
    records, logs = db.execute("SELECT (SELECT COUNT(*) FROM book_records), (SELECT COUNT(*) FROM sqlite_master "
                               "WHERE type = 'table' AND name = 'cll_entries')").fetchone()
    if not logs:
        return _Mark(records, 0)  # a file whose logs have no table yet has no entry
    entries = db.execute("SELECT COUNT(*) FROM cll_entries WHERE log_id IN "
                         "(SELECT log_id FROM shelf_logs WHERE kind IN ('book', 'journal'))").fetchone()[0]
    return _Mark(records, entries)


@dataclass
class _OfferPlan:
    """The offers the profile said were answered, which leave the offer
    bounds, and the unanswered ones to drop, oldest first."""

    answered: list[str] = field(default_factory=list)
    drop: list[_Row] = field(default_factory=list)


def _plan_offer_room(shelf: Any, profile: ReceiveProfile, b: ReceiveBounds, sender: str,
                     size: int) -> tuple[_OfferPlan, Exception | None]:
    """Go ``planOfferRoom``: the oldest unanswered offers are dropped until
    one more of ``size`` bytes from ``sender`` fits its sender's bounds and
    the total ones. Each offer it would drop is first put to the profile's
    ``answered``, holding no database lock; an answered one leaves the bounds
    instead. An offer larger than a byte bound alone is refused."""
    if size > b.max_offer_bytes_per_sender or size > b.max_offer_bytes:
        return _OfferPlan(), OverLimitError(f"an offer of {size} bytes, over the store's offer bound")
    with shelf._read() as db:
        rows = db.execute(f"SELECT {_COLUMNS}, length(record) FROM shelf_received WHERE offer = 1 "
                          "ORDER BY received_at, rowid").fetchall()
    offers = [[_row(r), r[7], False] for r in rows]  # row, size, gone
    plan = _OfferPlan()
    for of, count_bound, bytes_bound in ((lambda h: h[0].sender == sender, b.max_offers_per_sender,
                                          b.max_offer_bytes_per_sender),
                                         (lambda h: True, b.max_offers, b.max_offer_bytes)):
        while True:
            live = [h for h in offers if not h[2] and of(h)]
            if len(live) + 1 <= count_bound and sum(h[1] for h in live) + size <= bytes_bound:
                break
            if not live:
                raise CorruptError("the offer bounds count offers the store does not list")
            oldest = live[0]
            oldest[2] = True
            if profile.answered(oldest[0].record()):
                plan.answered.append(oldest[0].digest)
            else:
                plan.drop.append(oldest[0])
    return plan, None


# -- Receive --------------------------------------------------------------------------


class _Refusal(Exception):
    """Carries a refusal out of a transaction, which it rolls back."""

    def __init__(self, err: Exception):
        super().__init__(str(err))
        self.err = err


class _StaleMark(Exception):
    """Rolls back a transaction whose offer plan an answer committed since
    may have changed."""


def receive(shelf: Any, record: bytes | None, proof: StampProof | None) -> ReceiveResult:
    """Go ``Shelf.Receive``; see :meth:`Shelf.receive`."""
    profile = shelf._receive.profile
    if profile is None:
        raise InvalidError("the shelf has no receive profile")
    if record is None and proof is None:
        raise InvalidError("nothing to receive")
    with shelf._receive_mu:
        _live(shelf)
        # Each part writes its changes to its own set, merged only once that
        # part's transaction committed.
        changes = _ChangeSet()
        record_out = proof_out = None
        if record is not None:
            part = _ChangeSet()
            record_out = _receive_record(shelf, profile, bytes(record), part)
            changes.merge(part)
        if proof is not None:
            part = _ChangeSet()
            try:
                proof_out = _receive_proof(shelf, profile, proof, part)
            except Exception as exc:
                # The record part, if any, is committed: report it beside the error.
                exc.receive_result = ReceiveResult(record_out, None, changes.result())  # type: ignore[attr-defined]
                raise
            changes.merge(part)
        return ReceiveResult(record_out, proof_out, changes.result())


def _receive_record(shelf: Any, profile: ReceiveProfile, record: bytes, changes: _ChangeSet) -> RecordOutcome:
    bounds = shelf._receive.bounds.with_defaults()
    out = RecordOutcome()

    def refuse(err: Exception) -> RecordOutcome:
        out.refused = err
        return out

    if len(record) > bounds.max_record_bytes:
        return refuse(OverLimitError(f"a received record of {len(record)} bytes, over the store's "
                                     f"{bounds.max_record_bytes}"))
    try:
        r = profile.read(record)
    except Exception as exc:  # the profile's refusal: an invalid record
        return refuse(InvalidError(f"a received record: {exc}"))
    if not isinstance(r, ReceivedRecord):
        return refuse(InvalidError("a received record: the profile read no ReceivedRecord"))
    problem = r._validate()
    if problem:
        return refuse(InvalidError(f"a received record: {problem}"))
    out.digest = r.digest
    key = (r.sender_key, r.log_id)
    with shelf._read() as db:
        deleted = db.execute("SELECT COUNT(*) FROM shelf_received_tombstones WHERE digest = ?", (r.digest,)).fetchone()[0]
        held = None if deleted else db.execute("SELECT record FROM shelf_received WHERE digest = ?",
                                               (r.digest,)).fetchone()
        prefix = _prefix(db, *key) if held is not None and bytes(held[0]) == record else None
    if deleted:
        out.deleted = True
        return out
    if held is not None:
        if bytes(held[0]) != record:
            return refuse(InvalidError(f"another record is held under digest {r.digest}"))
        out.duplicate = True
        out.counted = prefix.counts(r.position, r.digest)
        out.ask_for_book = not out.counted
        return out
    if not profile.admit(r):
        return refuse(RefusedError(f"{r.sender_key} may not push this record"))
    now = _now(shelf)
    attempt = 0
    while True:
        attempt += 1
        plan, mark = _OfferPlan(), None
        if r.offer:
            # The profile is asked about the offers to drop before the
            # transaction, never inside it, under a mark of the holder's own
            # books: an answer committed after the mark changes it.
            with shelf._read() as db:
                mark = _own_mark(db)
            plan, refusal = _plan_offer_room(shelf, profile, bounds, r.sender_key, len(record))
            if refusal is not None:
                return refuse(refusal)
        attempt_out, part = replace(out), _ChangeSet()
        try:
            _receive_record_tx(shelf, r, record, bounds, plan, mark, now, attempt_out, part)
        except _StaleMark:
            if attempt == OFFER_ATTEMPTS:
                return refuse(BusyError("the holder's books kept changing while an offer was being made room for"))
            continue
        except _Refusal as refusal:
            return refuse(refusal.err)
        changes.merge(part)
        return attempt_out


def _receive_record_tx(shelf: Any, r: ReceivedRecord, record: bytes, bounds: ReceiveBounds, plan: _OfferPlan,
                       mark: _Mark | None, now: str, out: RecordOutcome, changes: _ChangeSet) -> None:
    """Store an admitted record in one write transaction, with the offer
    plan's drops; changes go to the part's own set."""
    key = (r.sender_key, r.log_id)
    with shelf._tx() as db:
        if r.offer and _own_mark(db) != mark:
            raise _StaleMark()
        count = db.execute("SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ?", key).fetchone()[0]
        if count >= bounds.max_records_per_log:
            raise _Refusal(OverLimitError(f"{count} records held from {r.sender_key} in {r.log_id}, the store's bound"))
        before: dict[tuple[str, str], _LogState] = {}

        def remember(k: tuple[str, str]) -> None:
            if k not in before:
                before[k] = _log_state(db, k)

        remember(key)
        for digest in plan.answered:
            db.execute("UPDATE shelf_received SET offer = 0 WHERE digest = ?", (digest,))
        for row in plan.drop:
            remember((row.sender, row.log))
            db.execute("DELETE FROM shelf_received WHERE digest = ?", (row.digest,))
        _vacate(db, plan.drop, True, bounds)
        forks = _fork_evidence(db, r)
        db.execute("INSERT INTO shelf_received (digest, sender_key, log_id, position, prev, deal_id, offer, record, "
                   "received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                   (r.digest, r.sender_key, r.log_id, r.position, r.prev, r.deal_id, int(r.offer), record, now))
        # A conflict in a log of offers alone counts under the offer bounds;
        # one in a log that holds, or held, any other record is kept for good.
        offers_only = False
        if forks and r.offer:
            others = db.execute(
                "SELECT (SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ? AND offer = 0) + "
                "(SELECT COUNT(*) FROM shelf_received_vacated WHERE sender_key = ? AND log_id = ? AND offer = 0)",
                (*key, *key)).fetchone()[0]
            offers_only = others == 0
        for f in forks:
            if _note_conflict(db, key, EVIDENCE_FORK, f.position, f.held, record, now, offers_only, bounds):
                changes.forked.add(r.deal_id)
                if f.deal:
                    changes.forked.add(f.deal)
        for k in sorted(before, key=lambda k: (k[0].encode(), k[1].encode())):
            prefix = _prefix(db, *k)
            # Held proofs the prefix now reaches are checked against it.
            _check_held_proofs(db, k, prefix, now, changes)
            after = _log_state(db, k)
            _report(before[k], after, changes)
            if k == key:
                out.counted = after.prefix.counts(r.position, r.digest)
        out.stored = True
        out.ask_for_book = not out.counted
        if _before_commit is not None:
            _before_commit("record")


def _receive_proof(shelf: Any, profile: ReceiveProfile, p: StampProof, changes: _ChangeSet) -> ProofOutcome:
    """Go ``receiveProof``: verify the proof, ask the profile whose records
    it covers, refuse one that contradicts a held proof at the same size or
    the records held (keeping the evidence, once per size), and keep every
    proof accepted. A proof grants coverage only once the records held reach
    its size and recompute its root; one past them is kept, and checked when
    they arrive."""
    from agent_action_capsule.canonical import jcs

    out = ProofOutcome()
    try:
        v = verify_stamp_proof(p)
    except InvalidError as exc:
        out.refused = exc
        return out
    sender, ok = profile.proof_sender(v.key_id, v.fact["log_id"])
    if not ok:
        out.refused = RefusedError(f"{v.key_id} may not push a stamp proof of {v.fact['log_id']}")
        return out
    log_id, size = v.fact["log_id"], v.fact["size"]
    key = (sender, log_id)
    shelf_digest = hashlib.sha256(p.shelf_checkpoint).hexdigest()
    # Stored as its canonical JSON, so every language keeps the same bytes.
    try:
        inclusion = jcs({"kind": p.inclusion.get("kind"), "seq": p.inclusion.get("seq"), "proof": p.inclusion.get("proof")})
        opening = None if p.status_opening is None else jcs(
            {"salt": p.status_opening.salt, "value": p.status_opening.value})
    except Exception as exc:  # the canonicalizer's refusal (a float, say)
        raise InvalidError(f"stamp proof: canonical JSON: {exc}") from None
    now = _now(shelf)
    part = _ChangeSet()
    with shelf._tx() as db:
        before = _log_state(db, key)
        prefix = before.prefix
        same = db.execute("SELECT shelf_checkpoint_sha256, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? "
                          "AND log_id = ? AND size = ? ORDER BY received_at, shelf_checkpoint_sha256",
                          (sender, log_id, size)).fetchall()
        refused = False
        for _, held in same:
            if _read_cp(held).root != v.book_checkpoint.root:
                # Refused, and the evidence kept: the transaction commits.
                out.refused = InvalidError(f"the book checkpoint of {log_id} at {size} contradicts one held from {sender}")
                if _note_conflict(db, key, EVIDENCE_CHECKPOINT, size, held, p.book_checkpoint, now, False,
                                  ReceiveBounds()):
                    part.forked.update(prefix.deals)
                refused = True
                break
        state = _PROOF_UNCHECKED
        if not refused and v.book_checkpoint.entries <= len(prefix.counted):
            match, over = _root_recomputes(v.book_checkpoint, prefix.digests(v.book_checkpoint.entries))
            if not match:
                out.refused = InvalidError(f"the records held from {sender} in {log_id} do not recompute the book "
                                           f"checkpoint at {size}")
                if _note_conflict(db, key, EVIDENCE_CHECKPOINT_RECORDS, size, p.book_checkpoint, over, now, False,
                                  ReceiveBounds()):
                    part.forked.update(prefix.deals)
                refused = True
            state = _PROOF_RECOMPUTED
        if not refused and any(h == shelf_digest for h, _ in same):
            out.duplicate = True
        elif not refused:
            db.execute("INSERT INTO shelf_received_proofs (sender_key, log_id, size, shelf_checkpoint_sha256, "
                       "book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, verified) "
                       "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                       (sender, log_id, size, shelf_digest, p.book_checkpoint, p.fact, opening, inclusion,
                        p.shelf_checkpoint, now, state))
            out.accepted = True
            _report(before, _log_state(db, key), part)
            if _before_commit is not None:
                _before_commit("proof")
    changes.merge(part)
    return out


# -- Forget and the readers -----------------------------------------------------------


def forget(shelf: Any, deal_id: str, now: datetime, settled: Callable[[str], bool]) -> list[str]:
    """Go ``Shelf.Forget``; see :meth:`Shelf.forget`."""
    if not deal_id or settled is None:
        raise InvalidError("forget needs a deal id and its settled check")
    retention = shelf._receive.retention
    if retention <= timedelta(0):
        raise InvalidError("the holder keeps received records for the life of the shelf")
    with shelf._receive_mu:
        _live(shelf)
        if not settled(deal_id):
            raise InvalidError(f"deal {deal_id} is not settled; nothing of it is deleted")
        moment = now.astimezone(timezone.utc) if now.tzinfo else now.replace(tzinfo=timezone.utc)
        cutoff = _format(moment - retention)
        deleted_at = _now(shelf)
        deleted = []
        with shelf._tx() as db:
            rows = db.execute(f"SELECT {_COLUMNS}, received_at FROM shelf_received WHERE deal_id = ? AND received_at <= ? "
                              "ORDER BY digest", (deal_id, cutoff)).fetchall()
            gone = [_row(r) for r in rows]
            for r, values in zip(gone, rows):
                db.execute("INSERT INTO shelf_received_tombstones (digest, sender_key, received_at, deleted_at) "
                           "VALUES (?, ?, ?, ?)", (r.digest, r.sender, values[7], deleted_at))
                db.execute("DELETE FROM shelf_received WHERE digest = ?", (r.digest,))
                deleted.append(r.digest)
            _vacate(db, gone, False, ReceiveBounds())
        return deleted


def counted_prefix(shelf: Any, sender_key: str, log_id: str) -> CountedPrefix:
    with _reader(shelf) as db:
        p = _prefix(db, sender_key, log_id)
    return CountedPrefix(len(p.counted), p.counted[-1][0] if p.counted else "", p.fork_at)


def received_record_of(shelf: Any, digest: str) -> HeldRecord:
    with _reader(shelf) as db:
        row = db.execute(f"SELECT {_COLUMNS}, record, received_at FROM shelf_received WHERE digest = ?",
                         (digest,)).fetchone()
        tomb = None if row is not None else db.execute(
            "SELECT deleted_at FROM shelf_received_tombstones WHERE digest = ?", (digest,)).fetchone()
    if row is None:
        if tomb is None:
            raise NotFoundError(f"received record {digest}")
        raise DeletedError(f"received record {digest}, at {tomb[0]}")
    return HeldRecord(_row(row).record(), bytes(row[7]), _parse(row[8], f"received record {digest}'s time"))


def evidence(shelf: Any) -> list[Evidence]:
    with _reader(shelf) as db:
        rows = db.execute("SELECT kind, a, b, noted_at FROM shelf_evidence ORDER BY id").fetchall()
    return [Evidence(kind, bytes(a), bytes(b), _parse(at, "evidence time")) for kind, a, b, at in rows]
