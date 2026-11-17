# SPDX-License-Identifier: Apache-2.0
"""The whole-shelf handover and its replay (evidencebook#461,
docs/design/461-shelf-facts.md §2.7), member for member with Go's
``shelf_handover.go``.

A reader the holder chooses is handed every entry of the shelf log up to an
anchor, with the documents after the format switch, each book fact's status
opening, the book checkpoints the facts carry and the consistency proofs
between a book's facts, and checks that nothing was held back, left out,
changed or replaced. Counting a card from the latest facts is
standard_history/3's (AAC). A genesis fact comes with its tie to the old
witnessed tree when the holder has one (:class:`HandoverTie`, Go
``shelf_handover_tie.go``); one without is reported untied, which claims
nothing about a log before the coverage start.

The JSON form is Go's ``encoding/json`` of its types: byte strings are
standard base64 with padding, and an opening's value and a consistency
proof keep their own member order (Go ``json.RawMessage``).
"""
from __future__ import annotations

import base64
import binascii
import hashlib
import json
from dataclasses import dataclass, field
from typing import Any

from cll.checkpoint import core

from . import shelf_facts
from ._cll import Checkpoint, read_checkpoint
from ._gojson import Ordered
from .errors import CorruptError, InvalidError

__all__ = ["Handover", "HandoverConsistency", "HandoverDocument", "HandoverLatest", "HandoverResult",
           "HandoverTie", "canonical_document", "check_fact_chain", "handover_locked", "verify_handover"]


def _b64(data: bytes) -> str:
    return base64.b64encode(data).decode()


def _unb64(text: Any, what: str) -> bytes:
    if not isinstance(text, str):
        raise InvalidError(f"handover: {what} is a base64 string")
    try:
        return base64.b64decode(text, validate=True)
    except (binascii.Error, ValueError):
        raise InvalidError(f"handover: {what} is not standard base64") from None


def _uint(value: Any, what: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value < 2**64:
        raise InvalidError(f"handover: {what} is a non-negative integer")
    return value


def _str(value: Any, what: str) -> str:
    if not isinstance(value, str):
        raise InvalidError(f"handover: {what} is a string")
    return value


def _list(value: Any, what: str) -> list:
    """A Go slice: JSON null or absent is empty."""
    if value is None:
        return []
    if not isinstance(value, list):
        raise InvalidError(f"handover: {what} is a list")
    return value


#: An opening whose ``value`` member was absent (Go: a nil json.RawMessage,
#: which never verifies).
_NO_VALUE: Any = object()


def _opening_value(opening: shelf_facts.Opening) -> Ordered:
    return Ordered(salt=opening.salt, value=None if opening.value is _NO_VALUE else opening.value)


def _opening_from(value: Any, what: str) -> shelf_facts.Opening | None:
    if value is None:
        return None
    if not isinstance(value, dict):
        raise InvalidError(f"handover: {what} is an object")
    salt = value.get("salt")
    return shelf_facts.Opening("" if salt is None else _str(salt, f"{what}'s salt"), value.get("value", _NO_VALUE))


@dataclass(frozen=True)
class HandoverDocument:
    """One entry document and, for a book fact, its status opening."""

    seq: int
    doc: bytes
    status_opening: shelf_facts.Opening | None = None

    def value(self) -> Ordered:
        out = Ordered(seq=self.seq, doc=_b64(self.doc))
        if self.status_opening is not None:
            out["status_opening"] = _opening_value(self.status_opening)
        return out

    @classmethod
    def from_value(cls, value: Any) -> HandoverDocument:
        if not isinstance(value, dict):
            raise InvalidError("handover: a document is an object")
        return cls(_uint(value.get("seq", 0), "a document's seq"), _unb64(value.get("doc", ""), "a document"),
                   _opening_from(value.get("status_opening"), "a status opening"))


@dataclass(frozen=True)
class HandoverConsistency:
    """A consistency proof between two checkpoints of one book: ``proof`` is
    Go's ``ConsistencyEvidence``, ``{kind, proof}``."""

    log_id: str
    from_size: int
    to_size: int
    proof: Any

    def value(self) -> Ordered:
        return Ordered(log_id=self.log_id, from_size=self.from_size, to_size=self.to_size, proof=self.proof)

    @classmethod
    def from_value(cls, value: Any) -> HandoverConsistency:
        if not isinstance(value, dict):
            raise InvalidError("handover: a consistency proof is an object")
        proof = value.get("proof", Ordered())
        if proof is None:
            proof = Ordered()
        if not isinstance(proof, dict):
            raise InvalidError("handover: a consistency proof's evidence is an object")
        return cls(_str(value.get("log_id", ""), "a consistency proof's log id"),
                   _uint(value.get("from_size", 0), "from_size"), _uint(value.get("to_size", 0), "to_size"), proof)


def _receipt_value(r: Any) -> Ordered:
    """Go's JSON of a ``WitnessReceipt``."""
    return Ordered(ts_url=r.ts_url, entry_hash=r.entry_hash, receipt=_b64(r.receipt), leaf_index=r.leaf_index,
                   tree_size=r.tree_size)


def _receipt_from(r: Any) -> Any:
    from .cadence import WitnessReceipt

    if not isinstance(r, dict):
        raise InvalidError("handover: a receipt is an object")
    return WitnessReceipt(_str(r.get("ts_url", ""), "a receipt's ts_url"),
                          _str(r.get("entry_hash", ""), "a receipt's entry_hash"),
                          _unb64(r.get("receipt", ""), "a receipt"),
                          _uint(r.get("leaf_index", 0), "a receipt's leaf_index"),
                          _uint(r.get("tree_size", 0), "a receipt's tree_size"))


@dataclass(frozen=True)
class HandoverTie:
    """A genesis fact's tie to the old witnessed tree (§2.7; Go
    ``HandoverTie``): the old witnessed tick's leaf for the fact's log (its
    log, size, checkpoint digest, salt and position) and its ``path`` to that
    tick's root (``CADENCE_DEPTH`` hashes, lowercase hex, from the leaf up);
    ``entry_seq``, the position from 1 of that root in the shelf log before
    the format switch; ``cadence_checkpoint``, the statement the tick cut, and
    ``receipts``, the witness receipts held for it in witness URL order;
    ``old_checkpoint``, the book checkpoint statement the leaf commits to;
    and ``consistency``, Go's ``ConsistencyEvidence`` from it to the genesis
    fact's checkpoint, ``None`` when the two are of one size (their roots
    are then equal)."""

    book_id: str
    log_id: str
    size: int
    checkpoint_sha256: str
    salt: str
    index: int
    path: list[str]
    entry_seq: int
    cadence_checkpoint: bytes
    receipts: list[Any]
    old_checkpoint: bytes
    consistency: Any = None

    def value(self) -> Ordered:
        out = Ordered(book_id=self.book_id, log_id=self.log_id, size=self.size,
                      checkpoint_sha256=self.checkpoint_sha256, salt=self.salt, index=self.index,
                      path=list(self.path), entry_seq=self.entry_seq,
                      cadence_checkpoint=_b64(self.cadence_checkpoint),
                      receipts=[_receipt_value(r) for r in self.receipts], old_checkpoint=_b64(self.old_checkpoint))
        if self.consistency is not None:
            out["consistency"] = self.consistency
        return out

    @classmethod
    def from_value(cls, value: Any) -> HandoverTie:
        if not isinstance(value, dict):
            raise InvalidError("handover: a tie is an object")
        consistency = value.get("consistency")
        if consistency is not None and not isinstance(consistency, dict):
            raise InvalidError("handover: a tie's consistency proof is an object")
        return cls(_str(value.get("book_id", ""), "a tie's book_id"), _str(value.get("log_id", ""), "a tie's log_id"),
                   _uint(value.get("size", 0), "a tie's size"),
                   _str(value.get("checkpoint_sha256", ""), "a tie's checkpoint_sha256"),
                   _str(value.get("salt", ""), "a tie's salt"), _uint(value.get("index", 0), "a tie's index"),
                   [_str(p, "a tie's path") for p in _list(value.get("path"), "a tie's path")],
                   _uint(value.get("entry_seq", 0), "a tie's entry_seq"),
                   _unb64(value.get("cadence_checkpoint", ""), "a tie's cadence checkpoint"),
                   [_receipt_from(r) for r in _list(value.get("receipts"), "a tie's receipts")],
                   _unb64(value.get("old_checkpoint", ""), "a tie's old checkpoint"), consistency)


@dataclass(frozen=True)
class HandoverCheckpoint:
    """A shelf checkpoint before a handover's anchor and the witness
    receipts held for it, one per witness and key, in URL order (Go
    ``HandoverCheckpoint``)."""

    checkpoint: bytes
    receipts: list[Any]

    def value(self) -> Ordered:
        return Ordered(checkpoint=_b64(self.checkpoint), receipts=[_receipt_value(r) for r in self.receipts])

    @classmethod
    def from_value(cls, value: Any) -> HandoverCheckpoint:
        if not isinstance(value, dict):
            raise InvalidError("handover: a witnessed checkpoint is an object")
        return cls(_unb64(value.get("checkpoint", ""), "a witnessed checkpoint"),
                   [_receipt_from(r) for r in _list(value.get("receipts"), "a witnessed checkpoint's receipts")])


@dataclass(frozen=True)
class Handover:
    """The material a whole-shelf reader is handed (§2.7). ``entries`` is
    every entry of the shelf log up to the anchor, 32-byte ids in lowercase
    hex; ``anchor`` the shelf checkpoint it runs to and ``receipts`` the
    witness receipts the holder holds for it (``shelf_receipts``), one per
    witness and key, in URL order; ``witnessed`` the shelf checkpoints before
    the anchor, within it, for which the holder holds receipts, each with
    every receipt held, in tick order (invariant C,
    ``docs/design/461-deal-profile.md`` §8.1), from which a reader resolves
    the record times the holder's ``record_time`` gives
    (:func:`verify_handover_times`); ``ties`` each genesis fact's tie the
    holder can give (:class:`HandoverTie`), in book id order."""

    v: int
    log_id: str
    entries: list[str]
    documents: list[HandoverDocument]
    book_checkpoints: list[bytes]
    consistency: list[HandoverConsistency]
    anchor: bytes
    receipts: list[Any] = field(default_factory=list)
    ties: list[HandoverTie] = field(default_factory=list)
    witnessed: list[HandoverCheckpoint] = field(default_factory=list)

    def value(self) -> Ordered:
        out = Ordered(v=self.v, log_id=self.log_id, entries=list(self.entries),
                      documents=[d.value() for d in self.documents],
                      book_checkpoints=[_b64(b) for b in self.book_checkpoints],
                      consistency=[c.value() for c in self.consistency], anchor=_b64(self.anchor))
        if self.receipts:
            out["receipts"] = [_receipt_value(r) for r in self.receipts]
        if self.witnessed:
            out["witnessed"] = [w.value() for w in self.witnessed]
        out["ties"] = [t.value() for t in self.ties]
        return out

    @classmethod
    def from_value(cls, value: Any) -> Handover:
        if not isinstance(value, dict):
            raise InvalidError("handover: not a JSON object")
        v = value.get("v", 0)
        if isinstance(v, bool) or not isinstance(v, int):
            raise InvalidError("handover: v is an integer")
        receipts = [_receipt_from(r) for r in _list(value.get("receipts"), "receipts")]
        return cls(v, _str(value.get("log_id", ""), "log_id"),
                   [_str(e, "an entry") for e in _list(value.get("entries"), "entries")],
                   [HandoverDocument.from_value(d) for d in _list(value.get("documents"), "documents")],
                   [_unb64(b, "a book checkpoint") for b in _list(value.get("book_checkpoints"), "book_checkpoints")],
                   [HandoverConsistency.from_value(c) for c in _list(value.get("consistency"), "consistency")],
                   _unb64(value.get("anchor", ""), "the anchor"), receipts,
                   [HandoverTie.from_value(t) for t in _list(value.get("ties"), "ties")],
                   [HandoverCheckpoint.from_value(w) for w in _list(value.get("witnessed"), "witnessed")])

    @classmethod
    def loads(cls, text: str | bytes) -> Handover:
        """Read a handover's JSON, keeping each raw value's member order."""
        try:
            value = json.loads(text, object_pairs_hook=Ordered, parse_constant=_no_constant)
        except RecursionError:
            raise InvalidError("handover: nested too deeply") from None
        except (ValueError, TypeError) as exc:
            raise InvalidError(f"handover: {exc}") from None
        return cls.from_value(value)


@dataclass(frozen=True)
class HandoverLatest:
    """A book's latest fact up to the anchor, and its status opening."""

    fact: dict
    status_opening: shelf_facts.Opening

    def value(self) -> Ordered:
        return Ordered(fact=Ordered((name, self.fact[name]) for name, _ in _BOOK_FACT),
                       status_opening=_opening_value(self.status_opening))


@dataclass(frozen=True)
class HandoverResult:
    """What a verified handover shows. ``recomputed`` is always true:
    :func:`verify_handover` raises rather than return a result it could not
    recompute. ``witnessed`` says a receipt from a witness in the directory
    verifies for the anchor and gives a registration time (§2.7); a receipt
    with no registration time cannot give the witnessed level. A shelf with
    no witness is a supported default and is unwitnessed. ``coverage_start`` is the shelf log size, in
    entries, at which the facts begin to be complete: the end of the
    migration's stamp. ``latest`` is each book's latest fact and ``untied``
    the books whose genesis fact carries no tie to the old witnessed tree,
    both in book id order."""

    recomputed: bool
    witnessed: bool
    coverage_start: int
    latest: list[HandoverLatest]
    untied: list[str]

    def value(self) -> Ordered:
        return Ordered(recomputed=self.recomputed, witnessed=self.witnessed, coverage_start=self.coverage_start,
                       latest=[x.value() for x in self.latest], untied=list(self.untied))


def _no_constant(name: str) -> Any:
    raise ValueError(f"{name} is not JSON")


# -- entry documents ------------------------------------------------------------------


def _int(v: Any) -> bool:
    return not isinstance(v, bool) and isinstance(v, int) and -2**63 <= v < 2**63


def _u64(v: Any) -> bool:
    return not isinstance(v, bool) and isinstance(v, int) and 0 <= v < 2**64


def _text(v: Any) -> bool:
    return isinstance(v, str)


def _logs(v: Any) -> bool:
    return v is None or (isinstance(v, list) and all(
        isinstance(x, dict) and set(x) == {"log_id", "size"} and _text(x["log_id"]) and _u64(x["size"]) for x in v))


# Go's BookFact (book and journal facts), StampEntry and FormatSwitch: each
# member and the type it decodes into.
_BOOK_FACT = (("v", _int), ("kind", _text), ("profile", _text), ("book_id", _text), ("log_id", _text),
              ("size", _u64), ("checkpoint_sha256", _text), ("prev_fact_size", _u64), ("link_commitment", _text),
              ("status_commitment", _text))
_STAMP_ENTRY = (("v", _int), ("kind", _text), ("stamp", _u64), ("facts", _u64))
_FORMAT_SWITCH = (("v", _int), ("kind", _text), ("format", _text), ("last_tick", _u64), ("logs", _logs))
_DOCUMENT_TYPES = {shelf_facts.ENTRY_KIND_STAMP: _STAMP_ENTRY, shelf_facts.ENTRY_KIND_FORMAT_SWITCH: _FORMAT_SWITCH,
                   shelf_facts.FACT_KIND_BOOK: _BOOK_FACT, shelf_facts.FACT_KIND_JOURNAL: _BOOK_FACT}


def _decode_document(doc: bytes) -> Any:
    """An entry document's JSON; one nested past the interpreter's recursion
    limit is a ValueError, as any other JSON a reader refuses."""
    try:
        return json.loads(doc, parse_constant=_no_constant)
    except RecursionError:
        raise ValueError("nested too deeply") from None


def canonical_document(doc: bytes, kind: str) -> dict:
    """Decode an entry document of ``kind`` and require it to be its type's
    canonical JSON, as Go's ``canonicalDocument`` does, so every language
    reads the same members: a duplicate, an unknown or missing member,
    another type or another spelling of a value changes the bytes."""
    members = _DOCUMENT_TYPES.get(kind)
    if members is None:
        raise InvalidError(f"no document type {kind!r}")
    try:
        value = _decode_document(doc)
    except (ValueError, TypeError) as exc:
        raise InvalidError(f"not JSON: {exc}") from None
    if not isinstance(value, dict) or set(value) != {name for name, _ in members} or \
            not all(ok(value[name]) for name, ok in members):
        raise InvalidError("not its canonical JSON")
    try:
        _, canonical = shelf_facts.entry_id(value)
    except shelf_facts.Invalid as exc:
        raise InvalidError(str(exc)) from None
    except RecursionError:
        raise InvalidError("nested too deeply") from None
    if canonical != doc:
        raise InvalidError("not its canonical JSON")
    return value


# -- the handover ---------------------------------------------------------------------


def _checkpoint(statement: bytes) -> Checkpoint:
    try:
        return read_checkpoint(statement)
    except InvalidError as exc:
        raise CorruptError(str(exc)) from None


def handover_locked(c: Any, n: int) -> Handover:
    """Go ``Cadence.Handover``, under the shelf's lock: the shelf up to tick
    ``n``'s checkpoint (the newest finished tick when ``n`` is 0), which
    must be a stamp's. Link openings and journal openings are never handed
    over."""
    shelf = c._shelf
    if n == 0:
        with shelf._read() as db:
            n = db.execute("SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0").fetchone()[0]
    tick = c._tick_locked(n)
    if tick.leaves.stamp is None or tick.checkpoint_size == 0:
        raise InvalidError(f"tick {n} is not a finished stamp")
    anchor = _checkpoint(tick.checkpoint)
    from .cadence import WitnessReceipt

    # Every receipt shelf_receipts holds for the anchor, one per witness and
    # key, in witness URL and key order.
    with shelf._read() as db:
        held = [r for (r,) in db.execute("SELECT receipt FROM shelf_receipts WHERE checkpoint_sha256 = ? "
                                         "ORDER BY witness_id, key_id", (hashlib.sha256(tick.checkpoint).hexdigest(),))]
    receipts = []
    for raw in held:
        try:
            receipts.append(WitnessReceipt.decode(raw))
        except CorruptError as exc:
            raise CorruptError(f"a stored receipt of tick {n}: {exc}") from None
    entries: list[str] = []
    after = 0
    while after < anchor.entries:
        got = c._log.entries_after(after, min(1000, anchor.entries - after))
        if not got:
            raise CorruptError(f"the shelf log holds {after} entries, its checkpoint {anchor.entries}")
        for e in got:
            # A gap, or an entry past the anchor, breaks the log's own
            # contract: stop rather than loop.
            if e.seq != after + 1:
                raise CorruptError(f"the shelf log's entry after {after} is {e.seq}")
            entries.append(bytes(e.value).hex())
            after = e.seq
    with shelf._read() as db:
        rows = db.execute("SELECT entry_seq, kind, log_id, doc, status_opening FROM shelf_entries WHERE entry_seq <= ? "
                          "ORDER BY entry_seq", (anchor.entries,)).fetchall()
    documents: list[HandoverDocument] = []
    facts: list[tuple[str, int, str]] = []
    books: list[str] = []
    for seq, kind, _, doc, so in rows:
        doc, opening = bytes(doc), None
        if kind == shelf_facts.FACT_KIND_BOOK:
            try:
                stored = json.loads(bytes(so), object_pairs_hook=Ordered)
                f = json.loads(doc)
                opening = shelf_facts.Opening(stored["salt"], stored["value"])
                facts.append((f["log_id"], int(f["size"]), f["checkpoint_sha256"]))
                books.append(f["book_id"])
            except (ValueError, TypeError, KeyError) as exc:
                raise CorruptError(f"stored fact {seq}: {exc}") from None
        documents.append(HandoverDocument(seq, doc, opening))
    # The book checkpoints the facts carry are the statements the stamps'
    # leaves kept, one per distinct checkpoint.
    statements: dict[str, bytes] = {}
    with shelf._read() as db:
        ticks = [row[0] for row in db.execute("SELECT tick FROM cadence_ticks WHERE tick <= ? ORDER BY tick", (n,))]
    for t in ticks:
        got_tick = c._tick_locked(t)
        if got_tick.leaves.stamp is None:
            continue
        for leaf in got_tick.leaves.deals.values():
            if not leaf.statement:
                continue
            try:
                statements[leaf.checkpoint_sha256] = base64.urlsafe_b64decode(
                    leaf.statement + "=" * (-len(leaf.statement) % 4))
            except (binascii.Error, ValueError) as exc:
                raise CorruptError(f"tick {t}'s statement of {leaf.log_id}: {exc}") from None
    book_checkpoints: list[bytes] = []
    consistency: list[HandoverConsistency] = []
    seen: set[str] = set()
    last: dict[str, tuple[str, int, str]] = {}
    for f in facts:
        log_id, size, sum_ = f
        if sum_ not in seen:
            if sum_ not in statements:
                raise CorruptError(f"no stored statement for {log_id}'s checkpoint {sum_}")
            book_checkpoints.append(statements[sum_])
            seen.add(sum_)
        prev = last.get(log_id)
        if prev is not None and size > prev[1]:
            older, newer = _checkpoint(statements[prev[2]]), _checkpoint(statements[sum_])
            proof = shelf._cadence_log_locked(log_id).substrate.consistency(older, newer)
            consistency.append(HandoverConsistency(log_id, prev[1], size,
                                                   Ordered(kind="cll-mmr-consistency", proof=proof)))
        last[log_id] = f
    # Each book's genesis fact (its first) with its tie, when the holder has
    # one, in book id order.
    genesis: dict[str, tuple[str, int, str]] = {}
    for book, f in zip(books, facts):
        genesis.setdefault(book, f)
    ties: list[HandoverTie] = []
    for book in sorted(genesis, key=lambda s: s.encode()):
        log_id, _, sum_ = genesis[book]
        tie = _tie_locked(c, book, log_id, statements[sum_])
        if tie is not None:
            ties.append(tie)
    return Handover(1, c._cfg.log_id, entries, documents, book_checkpoints, consistency, tick.checkpoint, receipts,
                    ties, _witnessed_before_locked(c, n, anchor, tick.checkpoint))


def _witnessed_before_locked(c: Any, n: int, anchor: Checkpoint, anchor_statement: bytes) -> list[HandoverCheckpoint]:
    """Every shelf checkpoint of a tick before tick ``n``, within the anchor,
    for which ``shelf_receipts`` holds a receipt, with those receipts, in tick
    order (Go ``witnessedBeforeLocked``). Two ticks that cut one checkpoint
    give it once."""
    with c._shelf._read() as db:
        statements = [bytes(b) for (b,) in db.execute(
            "SELECT checkpoint FROM cadence_ticks WHERE tick < ? AND checkpoint_size > 0 AND checkpoint_size <= ? "
            "ORDER BY tick", (n, anchor.tree_size))]
    out: list[HandoverCheckpoint] = []
    seen: set[bytes] = set()
    for b in statements:
        digest = hashlib.sha256(b).digest()
        if b == anchor_statement or digest in seen:
            continue
        seen.add(digest)
        receipts = _receipts_locked(c, b)
        if receipts:
            out.append(HandoverCheckpoint(b, receipts))
    return out


def _receipts_locked(c: Any, statement: bytes) -> list[Any]:
    """Every receipt ``shelf_receipts`` holds for the checkpoint statement, in
    witness URL order (Go ``receiptsLocked``)."""
    from .cadence import WitnessReceipt

    digest = hashlib.sha256(statement).hexdigest()
    with c._shelf._read() as db:
        held = [r for (r,) in db.execute("SELECT receipt FROM shelf_receipts WHERE checkpoint_sha256 = ? "
                                         "ORDER BY witness_id, key_id", (digest,))]
    out = []
    for raw in held:
        try:
            out.append(WitnessReceipt.decode(raw))
        except CorruptError as exc:
            raise CorruptError(f"a stored receipt of checkpoint {digest}: {exc}") from None
    return out


def _tie_locked(c: Any, book_id: str, log_id: str, genesis: bytes) -> HandoverTie | None:
    """Go ``tieLocked``: the tie of the genesis fact of ``log_id`` (book
    ``book_id``) whose checkpoint statement is ``genesis``, from the witnessed
    tree tick with the log's largest size not past the genesis size. ``None``
    when the log has no such tick, or its leaf keeps no checkpoint statement
    (a tick a released capsule-cli build stored): the fact is then untied."""
    shelf = c._shelf
    cp = _checkpoint(genesis)
    with shelf._read() as db:
        row = db.execute("SELECT tick FROM cadence_witnessed WHERE log_id = ? AND mmr_size <= ? "
                         "ORDER BY mmr_size DESC, tick DESC LIMIT 1", (log_id, cp.tree_size)).fetchone()
    if row is None:
        return None
    n = row[0]
    tick = c._tick_locked(n)
    leaf = tick.leaves.deals.get(log_id)
    if tick.leaves.stamp is not None or leaf is None:
        raise CorruptError(f"witnessed tick {n} holds no tree leaf of {log_id}")
    if not leaf.statement:
        return None
    try:
        old = base64.urlsafe_b64decode(leaf.statement + "=" * (-len(leaf.statement) % 4))
    except (binascii.Error, ValueError) as exc:
        raise CorruptError(f"tick {n}'s statement of {log_id}: {exc}") from None
    _, path = tick.leaves.tree(leaf.index)
    receipts = _receipts_locked(c, tick.checkpoint)
    consistency = None
    if leaf.size < cp.tree_size:
        older = _checkpoint(old)
        proof = shelf._cadence_log_locked(log_id).substrate.consistency(older, cp)
        consistency = Ordered(kind="cll-mmr-consistency", proof=proof)
    return HandoverTie(book_id, log_id, leaf.size, leaf.checkpoint_sha256, leaf.salt, leaf.index,
                       [p.hex() for p in path], tick.entry_seq, tick.checkpoint, receipts, old, consistency)


# -- the replay -----------------------------------------------------------------------


def _hex32(value: Any) -> bytes | None:
    """Go ``decodeRecordID``: 64 lowercase hex characters."""
    if not shelf_facts._digest(value):
        return None
    return bytes.fromhex(value)


def _verify_consistency(older: Checkpoint, newer: Checkpoint, evidence: Any) -> None:
    """Go ``verifyCLLConsistency``, for checkpoints read from their signed
    statements."""
    proof = evidence.get("proof") if isinstance(evidence, dict) else None
    if not isinstance(evidence, dict) or evidence.get("kind") != "cll-mmr-consistency" or not isinstance(proof, dict):
        raise InvalidError("consistency evidence is not a CLL proof")
    old_peaks, new_peaks = _list(proof.get("old_peaks"), "old_peaks"), _list(proof.get("new_peaks"), "new_peaks")
    witness = [_list(path, "a witness path") for path in _list(proof.get("witness"), "witness")]
    for value in [*old_peaks, *new_peaks, *(h for path in witness for h in path), older.root, newer.root]:
        if _hex32(value) is None:
            raise InvalidError(f"record id {value!r} is not 64 lowercase hex characters")
    # Go decodes the proof into its typed portableConsistency: JSON null or
    # an absent member is the zero value, any other type is refused.
    def member(name: str, zero: Any) -> Any:
        value = proof.get(name)
        return zero if value is None else value

    v = _uint(member("v", 0), "a consistency proof's v")
    kind = _str(member("kind", ""), "a consistency proof's kind")
    old_size, new_size = _uint(member("old_size", 0), "old_size"), _uint(member("new_size", 0), "new_size")
    if old_size != older.tree_size or new_size != newer.tree_size:
        raise InvalidError("consistency proof sizes do not match the checkpoints")
    wire = core.ConsistencyProof(v, kind, old_size, new_size, tuple(old_peaks), tuple(tuple(p) for p in witness),
                                 tuple(new_peaks))
    if not core.verify_consistency(bytes.fromhex(older.root), old_size, bytes.fromhex(newer.root), new_size, wire):
        raise InvalidError("consistency proof does not verify")


def check_fact_chain(p: dict, f: dict, statements: dict[str, Checkpoint], proofs: dict) -> None:
    """Check two consecutive facts of one book (§2.7; Go ``checkFactChain``):
    one log, ``prev_fact_size`` the previous size, no shrinking, the same
    checkpoint at the same size, and a consistency proof that verifies when
    the size grew. A break is a replaced book. ``statements`` maps a
    checkpoint statement's SHA-256 to the checkpoint, and ``proofs``
    ``(log, from, to)`` to its proof."""

    def replaced(message: str) -> InvalidError:
        return InvalidError(f"{message} (a replaced book)")

    if f["log_id"] != p["log_id"]:
        raise replaced(f"names {f['log_id']}, its previous fact {p['log_id']}")
    if f["prev_fact_size"] != p["size"]:
        raise replaced(f"names previous size {f['prev_fact_size']}, its previous fact {p['size']}")
    if f["size"] < p["size"]:
        raise replaced(f"shrinks the book from {p['size']} to {f['size']}")
    if f["size"] == p["size"] and f["checkpoint_sha256"] != p["checkpoint_sha256"]:
        raise replaced(f"keeps size {f['size']} with another checkpoint")
    if f["size"] > p["size"]:
        proof = proofs.get((f["log_id"], p["size"], f["size"]))
        if proof is None:
            raise replaced(f"no consistency proof for {f['log_id']} from {p['size']} to {f['size']}")
        older, newer = statements.get(p["checkpoint_sha256"]), statements.get(f["checkpoint_sha256"])
        if older is None or newer is None:
            raise replaced(f"a checkpoint of {f['log_id']} the handover does not hold")
        try:
            _verify_consistency(older, newer, proof)
        except InvalidError as exc:
            raise replaced(f"{f['log_id']} from {p['size']} to {f['size']}: {exc}") from None


def _verify_tie(t: HandoverTie, f: dict, genesis: Checkpoint, h: Handover, nodes: _Nodes, marker: int,
                pinned_key: str, witnesses: Any) -> None:
    """Go ``verifyTie``: check a genesis fact's tie (§2.7) against the
    handover's entries (``nodes``, the tree of them) before the format switch
    at ``marker``, under the holder's key and the reader's witness
    directory. Raises :class:`InvalidError`."""
    from .cadence import CADENCE_DEPTH, WITNESS_VERIFIED, cadence_fold, cadence_leaf, verify_witness_receipt

    if t.log_id != f["log_id"] or t.size > f["size"]:
        raise InvalidError(f"the tie names {t.log_id} at {t.size}, the genesis fact {f['log_id']} at {f['size']}")
    # The old book checkpoint: the holder's, of the log at the leaf's size,
    # hashing to the leaf's digest, and a prefix of the genesis checkpoint.
    try:
        old = read_checkpoint(t.old_checkpoint)
    except InvalidError as exc:
        raise InvalidError(f"the old checkpoint does not verify: {exc}") from None
    if old.key_id != pinned_key or old.log_id != t.log_id or old.tree_size != t.size or \
            hashlib.sha256(t.old_checkpoint).hexdigest() != t.checkpoint_sha256:
        raise InvalidError("the old checkpoint is not the leaf's")
    if t.size == f["size"]:
        if t.consistency is not None or old.root != genesis.root:
            raise InvalidError("the old checkpoint is not the genesis checkpoint's root")
    elif t.consistency is None:
        raise InvalidError("no consistency proof from the old checkpoint")
    else:
        try:
            _verify_consistency(old, genesis, t.consistency)
        except InvalidError as exc:
            raise InvalidError(f"the genesis checkpoint does not extend the old one: {exc}") from None
    # The leaf and its path recompute the tick's root.
    if t.index >= 1 << CADENCE_DEPTH or t.salt == "" or len(t.path) != CADENCE_DEPTH:
        raise InvalidError("a malformed leaf")
    path = [_hex32(p) for p in t.path]
    if any(p is None for p in path):
        raise InvalidError("a malformed path")
    try:
        leaf = cadence_leaf(t.log_id, t.size, t.checkpoint_sha256, t.salt)
    except (ValueError, TypeError) as exc:  # a string JCS cannot encode
        raise InvalidError(f"the tie's leaf: {exc}") from None
    root = cadence_fold(leaf, t.index, path)
    # The root is a handed-over entry before the format switch, and the
    # cadence checkpoint is the holder's checkpoint of the shelf log the
    # handed-over entries recompute at its size, so the receipts over it
    # witnessed that root.
    if t.entry_seq < 1 or t.entry_seq >= marker or h.entries[t.entry_seq - 1] != root.hex():
        raise InvalidError(f"the tick's root is not the shelf log's entry at {t.entry_seq}")
    try:
        cadence = read_checkpoint(t.cadence_checkpoint)
    except InvalidError as exc:
        raise InvalidError(f"the cadence checkpoint does not verify: {exc}") from None
    if cadence.key_id != pinned_key or cadence.log_id != h.log_id or cadence.entries < t.entry_seq:
        raise InvalidError("the cadence checkpoint is not the holder's shelf log at the tick's root")
    try:
        if cadence.tree_size > nodes.size():
            raise ValueError("past the handed-over entries")
        recomputed = core.root_from_peaks([nodes.node(p) for p in core.peaks(cadence.tree_size)])
    except ValueError:  # cll's InvalidArgumentError, a size that is no MMR's
        recomputed = None
    if recomputed is None or recomputed.hex() != cadence.root:
        raise InvalidError("the handed-over entries do not recompute the cadence checkpoint")
    for r in t.receipts:
        try:
            check, _ = verify_witness_receipt(t.cadence_checkpoint, r, witnesses)
        except Exception:  # Go skips a receipt whose check errs
            continue
        if check.state == WITNESS_VERIFIED:
            return
    raise InvalidError("no receipt from a named witness verifies for the tick's checkpoint")


def verify_handover(h: Handover, pinned_key: str, witnesses: Any) -> HandoverResult:
    """Replay a handover (§2.7) under the holder's checkpoint key and the
    reader's witness directory (Go ``VerifyHandover``). Raises
    :class:`InvalidError` when an entry is held back, a document is left out,
    is not its canonical JSON or does not hash to its entry, a status opening
    does not recompute its commitment, a book checkpoint is not the fact's
    or not the holder's, or a book's facts break their chain (a replaced
    book). The journal's chain is not checked and its openings are not
    asked for."""
    from .cadence import WITNESS_VERIFIED, verify_witness_receipt

    def bad(message: str) -> InvalidError:
        return InvalidError(f"handover: {message}")

    try:
        anchor = read_checkpoint(h.anchor)
    except InvalidError as exc:
        raise bad(f"the anchor does not verify: {exc}") from None
    if h.v != 1:
        raise bad(f"a handover of version {h.v}")
    if anchor.key_id != pinned_key or anchor.log_id != h.log_id:
        raise bad("the anchor is not the holder's shelf log")
    # Every entry up to the anchor, exactly: an entry held back changes the
    # count or the root.
    if len(h.entries) != anchor.entries:
        raise bad(f"{len(h.entries)} entries for an anchor of {anchor.entries}")
    nodes = _Nodes()
    for i, entry in enumerate(h.entries, start=1):
        value = _hex32(entry)
        if value is None:
            raise bad(f"entry {i}: record id {entry!r} is not 64 lowercase hex characters")
        core.add_leaf(nodes, core.leaf_hash(value))
    if anchor.tree_size > nodes.size():
        raise bad("the entries do not reach the anchor's size")
    if core.root_from_peaks([nodes.node(p) for p in core.peaks(anchor.tree_size)]).hex() != anchor.root:
        raise bad("the entries do not recompute the anchor's root")
    # Every entry after the marker has its document.
    docs: dict[int, HandoverDocument] = {}
    for d in h.documents:
        if d.seq < 1 or d.seq > anchor.entries:
            raise bad(f"a document at {d.seq}, outside the handover")
        if hashlib.sha256(d.doc).hexdigest() != h.entries[d.seq - 1]:
            raise bad(f"the document at {d.seq} does not hash to its entry")
        docs[d.seq] = d

    def kind_of(seq: int, doc: bytes) -> str:
        # Go reads {"kind"} alone first: a document that is not an object, or
        # whose kind is not a string, is refused here.
        try:
            value = _decode_document(doc)
        except (ValueError, TypeError) as exc:
            raise bad(f"the document at {seq}: {exc}") from None
        if value is None:
            return ""
        if not isinstance(value, dict) or not isinstance(value.get("kind", ""), (str, type(None))):
            raise bad(f"the document at {seq} is not an entry document")
        return value.get("kind") or ""

    marker = 0
    for seq in range(1, anchor.entries + 1):
        d = docs.get(seq)
        if d is None:
            if marker:
                raise bad(f"the entry at {seq} after the format switch has no document")
            continue
        kind = kind_of(seq, d.doc)
        if not marker:
            if kind != shelf_facts.ENTRY_KIND_FORMAT_SWITCH:
                raise bad(f"a document at {seq} before the format switch")
            marker = seq
    if not marker:
        raise bad("no format switch up to the anchor")
    statements: dict[str, Checkpoint] = {}
    for b in h.book_checkpoints:
        try:
            cp = read_checkpoint(b)
        except InvalidError as exc:
            raise bad(f"a book checkpoint does not verify: {exc}") from None
        if cp.key_id != pinned_key:
            raise bad(f"a book checkpoint of {cp.log_id} is not the holder's")
        statements[hashlib.sha256(b).hexdigest()] = cp
    proofs = {(p.log_id, p.from_size, p.to_size): p.proof for p in h.consistency}
    ties: dict[str, HandoverTie] = {}
    for t in h.ties:
        if t.book_id in ties:
            raise bad(f"two ties of {t.book_id}")
        ties[t.book_id] = t
    latest: dict[str, HandoverLatest] = {}
    untied: list[str] = []
    coverage = 0
    for seq in range(marker, anchor.entries + 1):
        d = docs[seq]
        kind = kind_of(seq, d.doc)
        if kind not in _DOCUMENT_TYPES:
            raise bad(f"the document at {seq} is a {json.dumps(kind)}")
        try:
            f = canonical_document(d.doc, kind)
        except InvalidError as exc:
            raise bad(f"the document at {seq}: {exc}") from None
        if kind == shelf_facts.ENTRY_KIND_STAMP and not coverage:
            coverage = seq
        if kind != shelf_facts.FACT_KIND_BOOK:
            continue
        if d.status_opening is None:
            raise bad(f"the fact at {seq} has no status opening")
        if d.status_opening.value is _NO_VALUE:
            raise bad(f"the fact at {seq}: an opening's value: no value")
        try:
            d.status_opening.verify(f["status_commitment"])
        except shelf_facts.Invalid as exc:
            raise bad(f"the fact at {seq}: {exc}") from None
        except RecursionError:
            # The parser takes deeper JSON than the canonicalizer's recursion.
            raise bad(f"the fact at {seq}: an opening's value is nested too deeply") from None
        cp = statements.get(f["checkpoint_sha256"])
        if cp is None:
            raise bad(f"the fact at {seq} carries a checkpoint the handover does not hold")
        if cp.log_id != f["log_id"] or cp.tree_size != f["size"]:
            raise bad(f"the fact at {seq} names {f['log_id']} at {f['size']}, its checkpoint {cp.log_id} "
                      f"at {cp.tree_size}")
        book = f["book_id"]
        prev = latest.get(book)
        if prev is None:
            if f["prev_fact_size"] != 0:
                raise bad(f"the first fact of {book} at {seq} names a previous size")
            tie = ties.pop(book, None)
            if tie is None:
                untied.append(book)
            else:
                try:
                    _verify_tie(tie, f, cp, h, nodes, marker, pinned_key, witnesses)
                except InvalidError as exc:
                    raise bad(f"the tie of {book}: {exc}") from None
        else:
            try:
                check_fact_chain(prev.fact, f, statements, proofs)
            except InvalidError as exc:
                raise bad(f"the fact of {book} at {seq}: {exc}") from None
        latest[book] = HandoverLatest(f, d.status_opening)
    if not coverage:
        raise bad("the migration's stamp is not in the handover")
    _verify_witnessed_checkpoints(h, anchor, nodes, pinned_key, bad)
    if ties:
        # The lowest book id, as Go and Rust name it.
        raise bad(f"a tie of {min(ties)}, which has no genesis fact in the handover")
    witnessed = False
    for r in h.receipts:
        try:
            # Only a receipt that gives a registration time gives the
            # witnessed level (§2.7).
            check, verified = verify_witness_receipt(h.anchor, r, witnesses)
            if check.state == WITNESS_VERIFIED and verified.registered_at is not None:
                witnessed = True
                break
        except Exception:  # Go skips a receipt whose check errs
            continue
    by_id = lambda s: s.encode()  # noqa: E731  Go sort.Strings: byte order
    return HandoverResult(True, witnessed, coverage, [latest[k] for k in sorted(latest, key=by_id)],
                          sorted(untied, key=by_id))


def _verify_witnessed_checkpoints(h: Handover, anchor: Checkpoint, nodes: _Nodes, pinned_key: str, bad: Any) -> None:
    """Check a handover's earlier witnessed checkpoints (invariant C; Go
    ``verifyWitnessedCheckpoints``): each is the holder's checkpoint of the
    shelf log, within the anchor and not the anchor, held once, carries a
    receipt, and its root is the one the handed-over entries recompute at its
    size. Whether a receipt verifies is the reader's to judge, as for the
    anchor's."""
    seen = {h.anchor}
    for i, w in enumerate(h.witnessed, start=1):
        try:
            cp = read_checkpoint(w.checkpoint)
        except InvalidError as exc:
            raise bad(f"witnessed checkpoint {i} does not verify: {exc}") from None
        if cp.key_id != pinned_key or cp.log_id != h.log_id:
            raise bad(f"witnessed checkpoint {i} is not the holder's shelf log")
        if w.checkpoint in seen:
            raise bad(f"witnessed checkpoint {i} is the anchor or given twice")
        seen.add(w.checkpoint)
        if cp.tree_size > anchor.tree_size or cp.entries > anchor.entries:
            raise bad(f"witnessed checkpoint {i} is past the anchor")
        if not w.receipts:
            raise bad(f"witnessed checkpoint {i} carries no receipt")
        if cp.tree_size > nodes.size() or \
                core.root_from_peaks([nodes.node(p) for p in core.peaks(cp.tree_size)]).hex() != cp.root:
            raise bad(f"the handed-over entries do not recompute witnessed checkpoint {i}")


class _Nodes:
    """An MMR's nodes in memory (cll's ``NodeAppender``)."""

    def __init__(self) -> None:
        self.hashes: list[bytes] = []

    def size(self) -> int:
        return len(self.hashes)

    def node(self, pos: int) -> bytes:
        return self.hashes[pos]

    def append_nodes(self, hashes: list[bytes]) -> None:
        self.hashes.extend(hashes)


class HandoverTimes:
    """Record times from a verified handover (invariant C,
    ``docs/design/461-deal-profile.md`` §8.1; Go ``HandoverTimes``): a reader
    handed the whole shelf resolves the record time of a book's position as
    the holder's ``record_time`` does, from the anchor and the earlier
    witnessed checkpoints the handover carries with their receipts. A
    checkpoint after the anchor is not handed over, so a time only a later
    checkpoint gives is not resolved. The journal's sizes are never handed
    over, so its records get no time here."""

    def __init__(self, facts: list[tuple[int, str, int]], checkpoints: list[tuple[int, dict[str, int]]]) -> None:
        self._facts = facts
        self._checkpoints = checkpoints

    def record_time(self, book_id: str, position: int, witnesses: list[str]) -> int | None:
        """The record time, in Unix seconds, of the record at ``position``
        (from 1) of the book ``book_id``, from the witnesses named (their
        URLs), as ``Shelf.record_time`` computes it; ``None`` when there is
        none. A position that is not an integer from 1 to 2^64 - 1 raises
        :class:`InvalidError`."""
        # Go and Rust take an unsigned 64-bit position, as the holder's
        # record_time does.
        if isinstance(position, bool) or not isinstance(position, int) or not 1 <= position <= 2**64 - 1:
            raise InvalidError("a record position counts from 1, to 2^64 - 1")
        from .shelf_receive import _leaf_count

        seq = next((s for s, book, size in self._facts
                    if book == book_id and (_leaf_count(size) or 0) >= position), None)
        if seq is None:
            return None
        times = [at[w] for entries, at in self._checkpoints if entries >= seq for w in witnesses if w in at]
        return min(times) if times else None


def verify_handover_times(h: Handover, pinned_key: str, witnesses: Any) -> HandoverTimes:
    """Replay ``h`` with :func:`verify_handover`, then read the registration
    times its receipts give under the reader's directory (Go
    ``VerifyHandoverTimes``): a receipt that does not verify, or gives no
    registration time, gives none."""
    from .cadence import WITNESS_VERIFIED, verify_witness_receipt

    verify_handover(h, pinned_key, witnesses)
    facts: list[tuple[int, str, int]] = []
    for d in h.documents:
        if d.status_opening is None:
            continue
        f = json.loads(d.doc)
        if f.get("kind") == shelf_facts.FACT_KIND_BOOK:
            facts.append((d.seq, f["book_id"], f["size"]))
    facts.sort(key=lambda f: f[0])
    checkpoints: list[tuple[int, dict[str, int]]] = []
    for w in [HandoverCheckpoint(h.anchor, h.receipts), *h.witnessed]:
        cp = read_checkpoint(w.checkpoint)
        at: dict[str, int] = {}
        for r in w.receipts:
            try:
                check, verified = verify_witness_receipt(w.checkpoint, r, witnesses)
            except Exception:  # Go skips a receipt whose check errs
                continue
            if check.state != WITNESS_VERIFIED or verified.registered_at is None:
                continue
            at[r.ts_url] = min(at.get(r.ts_url, verified.registered_at), verified.registered_at)
        checkpoints.append((cp.entries, at))
    return HandoverTimes(facts, checkpoints)
