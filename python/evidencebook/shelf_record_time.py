# SPDX-License-Identifier: Apache-2.0
"""Record time (evidencebook#461, docs/design/461-shelf-facts.md §2.3 and
§2.6), as Go's ``shelf_record_time.go``: every receipt that verifies for a
shelf checkpoint is kept in ``shelf_receipts``, one per witness and key it
verified under (evidencebook#492), with the registration time the witness put in it (the ``iat`` of its protected CWT
claims) or none. A witness is named by its receipt's URL (``ts_url``), the
name a witness directory row is found by, so a witness an agreement names is
that URL. Each row also keeps the directory key id the receipt verified
under: a URL is a locator, and the key id lets a later naming by key read the
rows already held."""
from __future__ import annotations

import hashlib
import json
import sqlite3
from typing import Any, Sequence

from . import shelf_facts
from .errors import CorruptError, InvalidError

__all__ = ["record_time"]

# A receipt from the same witness under the same key replaces the held one only when it gives an
# earlier registration time, or the held one gives none: a witness's earliest
# registration is the one a record time reads (Go upsertReceipt).
_UPSERT_RECEIPT = (
    "INSERT INTO shelf_receipts (checkpoint_sha256, witness_id, key_id, receipt, registered_at) "
    "VALUES (?, ?, ?, ?, ?) "
    "ON CONFLICT (checkpoint_sha256, witness_id, key_id) DO UPDATE SET receipt = excluded.receipt, "
    "registered_at = excluded.registered_at "
    "WHERE excluded.registered_at IS NOT NULL AND (shelf_receipts.registered_at IS NULL OR "
    "excluded.registered_at < shelf_receipts.registered_at)")


def keep_receipt(db: sqlite3.Connection, statement: bytes, ts_url: str, encoded: str, verified: Any) -> None:
    """Store a receipt that verified for ``statement`` (Go ``keepReceipt``),
    in the caller's transaction. ``encoded`` is the text
    ``cadence_ticks.receipt`` holds; ``verified`` the
    :class:`~evidencebook.cadence.VerifiedReceipt` its check gave."""
    db.execute(_UPSERT_RECEIPT, (hashlib.sha256(statement).hexdigest(), ts_url, verified.key_id, encoded,
                                 verified.registered_at))


def backfill_receipts_locked(cadence: Any) -> None:
    """Keep, in ``shelf_receipts``, the receipt of every witnessed tick that
    has none there (Go ``backfillReceiptsLocked``): a tick witnessed before the
    table existed or imported (``import_legacy``). A receipt that no longer
    verifies under the configured directory is left out and tried again at
    the next open. The caller holds the shelf's mutex."""
    from .cadence import WITNESS_VERIFIED, WitnessReceipt, verify_witness_receipt

    shelf = cadence._shelf
    with shelf._read() as db:
        ticks = [(n, bytes(cp), receipt) for n, cp, receipt in db.execute(
            "SELECT tick, checkpoint, receipt FROM cadence_ticks WHERE witnessed = 1 AND receipt IS NOT NULL "
            "ORDER BY tick")]
    if not ticks:
        return
    with shelf._tx() as db:
        for n, statement, encoded in ticks:
            try:
                receipt = WitnessReceipt.decode(encoded)
            except CorruptError as exc:
                raise CorruptError(f"tick {n} receipt: {exc}") from None
            # The same receipt already held needs no second verification; a
            # held receipt of another key of the same witness does not cover it.
            present = db.execute("SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND witness_id = ? "
                                 "AND receipt = ?", (hashlib.sha256(statement).hexdigest(), receipt.ts_url,
                                                     encoded)).fetchone()[0]
            if present:
                continue
            try:
                check, verified = verify_witness_receipt(statement, receipt, cadence._cfg.witnesses)
            except InvalidError:
                continue
            if check.state != WITNESS_VERIFIED:
                continue
            keep_receipt(db, statement, receipt.ts_url, encoded, verified)


def _size(value: Any) -> int:
    """A stored size, as Go's uint64 field reads it: absent or null is 0,
    and anything not an integer from 0 to 2^64 - 1 is corrupt."""
    if value is None:
        return 0
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= 2**64 - 1:
        raise ValueError(f"size {value!r} is not a count")
    return value


def _first_covering_fact(db: sqlite3.Connection, book_id: str, position: int) -> int | None:
    """The entry position of the first fact of ``book_id`` whose size covers
    ``position``; a journal fact's size is its status opening's."""
    from .shelf_receive import _leaf_count

    rows = db.execute("SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE book_id = ? "
                      "AND kind IN (?, ?) ORDER BY entry_seq",
                      (book_id, shelf_facts.FACT_KIND_BOOK, shelf_facts.FACT_KIND_JOURNAL)).fetchall()
    for seq, kind, doc, so in rows:
        try:
            fact = json.loads(bytes(doc))
            # JSON null decodes into Go's struct as all zero, as here.
            if fact is None:
                fact = {}
            if not isinstance(fact, dict):
                raise ValueError("not a JSON object")
            size = _size(fact.get("size"))
        except (ValueError, TypeError) as exc:
            raise CorruptError(f"stored fact {seq}: {exc}") from None
        if kind == shelf_facts.FACT_KIND_JOURNAL:
            try:
                # As Go decodes it: the opening must carry a value; a null
                # value, or a missing or null size, reads as 0.
                opening = json.loads(bytes(so))
                if not isinstance(opening, dict):
                    raise ValueError("the status opening is not a JSON object")
                value = opening["value"]
                if value is None:
                    value = {}
                if not isinstance(value, dict):
                    raise ValueError("the journal status is not a JSON object")
                size = _size(value.get("size"))
            except (ValueError, TypeError, KeyError) as exc:
                raise CorruptError(f"stored journal fact {seq}: {exc}") from None
        leaves = _leaf_count(size)
        if leaves is not None and leaves >= position:
            return seq
    return None


# How a record time names its witnesses: the shelf_receipts column it
# matches (Go byURL and byKey). Each is one of these constants, never input.
_BY_URL = "witness_id"
_BY_KEY = "key_id"


def _check_position(position: Any) -> None:
    # Go and Rust take an unsigned 64-bit position.
    if isinstance(position, bool) or not isinstance(position, int) or not 1 <= position <= 2**64 - 1:
        raise InvalidError("a record position counts from 1, to 2^64 - 1")


def _check_received_log(received_log_id: str) -> None:
    if not received_log_id:
        raise InvalidError("a received record names its log")


def _first_linking_fact(db: sqlite3.Connection, book_id: str, log_id: str, position: int) -> int | None:
    """The entry position of the first book fact of ``book_id`` whose link
    value's ``last_received`` names ``log_id`` at a size that reaches
    ``position`` (Go ``firstLinkingFact``)."""
    rows = db.execute("SELECT entry_seq, link_opening FROM shelf_entries WHERE book_id = ? AND kind = ? "
                      "ORDER BY entry_seq", (book_id, shelf_facts.FACT_KIND_BOOK)).fetchall()
    for seq, lo in rows:
        if lo is None:
            continue
        try:
            opening = json.loads(bytes(lo))
            if not isinstance(opening, dict):
                raise ValueError("the link opening is not a JSON object")
            # Go reads the opening's value into a LinkValue: an absent value
            # is refused; a null one, or a null last_received, names nothing.
            value = opening["value"]
            if value is None:
                continue
            if not isinstance(value, dict):
                raise ValueError("the link value is not a JSON object")
            last = value.get("last_received")
            if last is None:
                continue
            if not isinstance(last, dict):
                raise ValueError("last_received is not a JSON object")
            named = last.get("log_id")
            if named is None:
                named = ""
            if not isinstance(named, str):
                raise ValueError("last_received's log_id is not a string")
            size = _size(last.get("size"))
        except (ValueError, TypeError, KeyError) as exc:
            raise CorruptError(f"stored fact {seq}'s link opening: {exc}") from None
        if named == log_id and size >= position:
            return seq
    return None


def _earliest_named_time(db: sqlite3.Connection, seq: int, witnesses: Sequence[str], by: str) -> int | None:
    """The earliest registration time, among the receipts from the
    witnesses named (by ``by``: their URL or their key id), of a shelf
    checkpoint whose tree holds the entry at ``seq`` (Go
    ``earliestNamedTime``)."""
    from .shelf import mmr_size

    # A shelf checkpoint covers the fact's entry when its tree holds that
    # entry: its MMR size is at least the size of seq leaves.
    covering = {hashlib.sha256(bytes(cp)).hexdigest() for (cp,) in db.execute(
        "SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size >= ? ORDER BY tick", (mmr_size(seq),))}
    named = set(witnesses)
    earliest = None
    for cp, witness, at in db.execute(f"SELECT checkpoint_sha256, {by}, registered_at FROM shelf_receipts "
                                      "WHERE registered_at IS NOT NULL"):
        if cp in covering and witness in named and (earliest is None or at < earliest):
            earliest = at
    return earliest


def _read(shelf: Any, first: Any, then: Any) -> Any:
    """``then(db, seq)`` over the entry ``first(db)`` finds, in one read
    transaction: a stamp being finished is seen whole or not at all."""
    from .shelf_receive import _reader

    with _reader(shelf) as db:
        db.execute("BEGIN")
        try:
            seq = first(db)
            return None if seq is None else then(db, seq)
        finally:
            db.execute("ROLLBACK")


def _record_time(shelf: Any, book_id: str, position: int, witnesses: Sequence[str], by: str) -> int | None:
    from .shelf_receive import _live

    _live(shelf)
    _check_position(position)
    if not witnesses:
        return None
    return _read(shelf, lambda db: _first_covering_fact(db, book_id, position),
                 lambda db, seq: _earliest_named_time(db, seq, witnesses, by))


def record_time(shelf: Any, book_id: str, position: int, witnesses: Sequence[str]) -> int | None:
    """Go ``Shelf.RecordTime``; see :meth:`evidencebook.shelf.Shelf.record_time`.
    Unix seconds, not a datetime: a registration time may be any integer to
    2^53 - 1 (AAC's receipt verifier), past datetime's year 9999."""
    return _record_time(shelf, book_id, position, witnesses, _BY_URL)


def record_time_by_key(shelf: Any, book_id: str, position: int, key_ids: Sequence[str]) -> int | None:
    """Go ``Shelf.RecordTimeByKey``."""
    return _record_time(shelf, book_id, position, key_ids, _BY_KEY)


def _received_record_time(shelf: Any, book_id: str, received_log_id: str, position: int,
                          witnesses: Sequence[str], by: str) -> int | None:
    from .shelf_receive import _live

    _live(shelf)
    _check_position(position)
    _check_received_log(received_log_id)
    if not witnesses:
        return None
    return _read(shelf, lambda db: _first_linking_fact(db, book_id, received_log_id, position),
                 lambda db, seq: _earliest_named_time(db, seq, witnesses, by))


def received_record_time(shelf: Any, book_id: str, received_log_id: str, position: int,
                         witnesses: Sequence[str]) -> int | None:
    """Go ``Shelf.ReceivedRecordTime``."""
    return _received_record_time(shelf, book_id, received_log_id, position, witnesses, _BY_URL)


def received_record_time_by_key(shelf: Any, book_id: str, received_log_id: str, position: int,
                                key_ids: Sequence[str]) -> int | None:
    """Go ``Shelf.ReceivedRecordTimeByKey``."""
    return _received_record_time(shelf, book_id, received_log_id, position, key_ids, _BY_KEY)


def _record_stamp(shelf: Any, position: int, first: Any) -> int | None:
    from .shelf_receive import _live

    _live(shelf)
    _check_position(position)
    return _read(shelf, first, lambda db, seq: db.execute(
        "SELECT tick FROM shelf_entries WHERE entry_seq = ?", (seq,)).fetchone()[0])


def record_stamp(shelf: Any, book_id: str, position: int) -> int | None:
    """Go ``Shelf.RecordStamp``."""
    return _record_stamp(shelf, position, lambda db: _first_covering_fact(db, book_id, position))


def received_record_stamp(shelf: Any, book_id: str, received_log_id: str, position: int) -> int | None:
    """Go ``Shelf.ReceivedRecordStamp``."""
    _check_received_log(received_log_id)
    return _record_stamp(shelf, position,
                         lambda db: _first_linking_fact(db, book_id, received_log_id, position))
