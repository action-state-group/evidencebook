# SPDX-License-Identifier: Apache-2.0
"""Record time (evidencebook#461, docs/design/461-shelf-facts.md §2.3 and
§2.6), as Go's ``shelf_record_time.go``: every receipt that verifies for a
shelf checkpoint is kept in ``shelf_receipts``, one per witness, with the
registration time the witness put in it (the ``iat`` of its protected CWT
claims) or none. A witness is named by its receipt's URL (``ts_url``), the
name a witness directory row is found by, so a witness an agreement names is
that URL. Each row also keeps the directory key id the receipt verified
under: a URL is a locator, and the key id lets a later naming by key read the
rows already held."""
from __future__ import annotations

import hashlib
import json
import sqlite3
from datetime import datetime, timezone
from typing import Any, Sequence

from . import shelf_facts
from .errors import CorruptError, InvalidError

__all__ = ["record_time"]

# A receipt from the same witness replaces the held one only when it gives an
# earlier registration time, or the held one gives none: a witness's earliest
# registration is the one a record time reads (Go upsertReceipt).
_UPSERT_RECEIPT = (
    "INSERT INTO shelf_receipts (checkpoint_sha256, witness_id, key_id, receipt, registered_at) "
    "VALUES (?, ?, ?, ?, ?) "
    "ON CONFLICT (checkpoint_sha256, witness_id) DO UPDATE SET key_id = excluded.key_id, receipt = excluded.receipt, "
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
            present = db.execute("SELECT COUNT(*) FROM shelf_receipts WHERE checkpoint_sha256 = ? AND witness_id = ?",
                                 (hashlib.sha256(statement).hexdigest(), receipt.ts_url)).fetchone()[0]
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
    """A stored size, as Go's uint64 field reads it."""
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
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
            if not isinstance(fact, dict):
                raise ValueError("not a JSON object")
            size = _size(fact.get("size", 0))
        except (ValueError, TypeError) as exc:
            raise CorruptError(f"stored fact {seq}: {exc}") from None
        if kind == shelf_facts.FACT_KIND_JOURNAL:
            try:
                size = _size(json.loads(bytes(so))["value"]["size"])
            except (ValueError, TypeError, KeyError) as exc:
                raise CorruptError(f"stored journal fact {seq}: {exc}") from None
        leaves = _leaf_count(size)
        if leaves is not None and leaves >= position:
            return seq
    return None


def record_time(shelf: Any, book_id: str, position: int, witnesses: Sequence[str]) -> datetime | None:
    """Go ``Shelf.RecordTime``; see :meth:`evidencebook.shelf.Shelf.record_time`."""
    from .shelf import mmr_size
    from .shelf_receive import _live, _reader

    _live(shelf)
    if position == 0:
        raise InvalidError("a record position counts from 1")
    if not witnesses:
        return None
    named = set(witnesses)
    with _reader(shelf) as db:
        # One read transaction: a stamp being finished is seen whole or not at all.
        db.execute("BEGIN")
        try:
            seq = _first_covering_fact(db, book_id, position)
            if seq is None:
                return None
            # A shelf checkpoint covers the fact's entry when its tree holds
            # that entry: its MMR size is at least the size of seq leaves.
            covering = {hashlib.sha256(bytes(cp)).hexdigest() for (cp,) in db.execute(
                "SELECT checkpoint FROM cadence_ticks WHERE checkpoint_size >= ? ORDER BY tick", (mmr_size(seq),))}
            earliest = None
            for cp, witness, at in db.execute("SELECT checkpoint_sha256, witness_id, registered_at FROM shelf_receipts "
                                              "WHERE registered_at IS NOT NULL"):
                if cp in covering and witness in named and (earliest is None or at < earliest):
                    earliest = at
        finally:
            db.execute("ROLLBACK")
    if earliest is None:
        return None
    return datetime.fromtimestamp(earliest, tz=timezone.utc)
