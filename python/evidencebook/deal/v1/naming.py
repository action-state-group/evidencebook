# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 holder's naming of rulings (PROFILE §5, "statement": a
receiving library MUST; evidencebook#527), as Go's ``name_rulings.go``
(``BookEntry``, ``NameRulings``, ``NameAllRulings``), and the helpers it
shares with Go's ``status_provider.go`` (``logsOf``, ``counted``).

A counted ruling of a deal's adjudicator that the holder's deal book does
not name yet gets a statement naming it, sealed in that book by the receive
call itself (:meth:`.holder.ReceiveProfile.after_receive`), so the holder's
own status reads the ruling whichever way it went.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Callable

from agent_action_capsule.canonical import jcs

from ...errors import BusyError
from ...record import CITES, PRODUCER_CLAIM, Entry, Link
from ..names import STEP_RECORD_TYPE
from .check import Record, check, decode, digest, encode, ref_to
from .names import CANONICALIZATION, KIND_AGREEMENT, PROFILE, TYPE_AGREE, TYPE_RULING, TYPE_STATEMENT
from .sent import SENT_LOG_PREFIX, verify_book

__all__ = ["book_entry", "name_all_rulings", "name_rulings"]

#: Tests only: run after a naming statement is built and before it is
#: appended; what it raises is the naming's (Go ``nameRulingsBeforeAppend``).
_before_append: Callable[[], None] | None = None


def book_entry(record: Any, prev_record_id: str = "") -> Entry:
    """The entry that seals ``record`` in its deal book (Go ``BookEntry``,
    design §3.1): its JCS bytes as the one payload, header record_type
    ``deal_step``, the deal id as the subject and, past seq 1, a cites link
    to ``prev_record_id``. Append it with ``Book.append_at`` at the record's
    seq, and on :class:`BusyError` read the book again and rebuild it."""
    r = decode(record) if not isinstance(record, Record) else record
    payload, _ = encode(r)
    links: tuple = ()
    if r.block["seq"] > 1:
        if not prev_record_id:
            raise ValueError("xdealv1: a deal-book record past seq 1 cites the record before it")
        links = (Link(CITES, prev_record_id),)
    return Entry(STEP_RECORD_TYPE, PRODUCER_CLAIM, payloads=(payload,), subject_ref=r.block["deal_id"], links=links)


def name_rulings(shelf: Any, deal_ids: list[str], at: datetime) -> list[str]:
    """Seals, in the holder's deal book of each deal given, a statement
    naming each counted ruling of that deal's adjudicator the book does not
    name yet (Go ``NameRulings``); returns the record ids sealed, in order.
    What it raises carries the ids sealed before it as ``sealed``.
    A deal with no v1 deal book, or whose agreement names no adjudicator, is
    passed over. It takes the shelf's locks."""
    if not deal_ids:
        return []
    want = set(deal_ids)
    return _name_rulings(shelf, want.__contains__, at)


def name_all_rulings(shelf: Any, at: datetime) -> list[str]:
    """:func:`name_rulings` over every v1 deal book the shelf holds (Go
    ``NameAllRulings``), the sweep a status provider runs before a stamp."""
    return _name_rulings(shelf, lambda _: True, at)


def _name_rulings(shelf: Any, want: Callable[[str], bool], at: datetime) -> list[str]:
    sealed: list[str] = []
    try:
        _name_books(shelf, want, at, sealed)
    except Exception as exc:
        # Go returns the ids sealed before the error beside it, whatever
        # failed: the statements already sealed stay, and a retry passes
        # them over.
        exc.sealed = list(sealed)  # type: ignore[attr-defined]
        raise
    return sealed


def _name_books(shelf: Any, want: Callable[[str], bool], at: datetime, sealed: list[str]) -> None:
    from .holder import _V1, _v1_payload

    for log_id in shelf.books():
        book = shelf.open(log_id)
        first = [r for r in book.records() if r.seq == 1]
        if len(first) != 1:
            continue
        rec, kind = _v1_payload(book, first[0])
        if kind != _V1 or rec.record_type != TYPE_AGREE:
            continue
        ag = rec.body
        if ag["kind"] != KIND_AGREEMENT or not want(rec.block["deal_id"]) or not isinstance(ag["adjudicator"], dict):
            continue
        _name_rulings_in(shelf, book, ag, rec.block["deal_id"], at, sealed)


def _name_rulings_in(shelf: Any, book: Any, ag: dict, deal: str, at: datetime, out: list[str]) -> None:
    """Seals the statements one deal book needs, each from a fresh read,
    appended at the place it was built for: a write in between makes it read
    again, up to three times (Go ``nameRulingsIn``). A book with a record not
    readable now as x-deal-v1 is never written to."""
    attempt = 0
    while True:
        own, whole = _read_own_book(book)
        if not whole:
            return
        held = shelf.held_by_deal(deal)
        ruling = _first_unnamed(shelf, held, ag["adjudicator"]["key_id"], deal, own)
        if not ruling:
            return
        r, prev = _naming_statement(shelf, held, ag, deal, own, ruling, at)
        e = book_entry(r, prev)
        if _before_append is not None:
            _before_append()
        try:
            rec = book.append_at(r.block["seq"], e)
        except BusyError:
            if attempt < 3:
                attempt += 1
                continue  # the book moved: read it again
            raise
        out.append(rec.record_id)
        attempt = 0


@dataclass(frozen=True)
class _Own:
    record: Record
    record_id: str


def _read_own_book(book: Any) -> tuple[list[_Own], bool]:
    """The holder's book as records, and whether every record read as
    x-deal-v1 (Go ``readOwnBook``)."""
    from .holder import _V1, _v1_payload

    own = []
    for r in book.records():
        rec, kind = _v1_payload(book, r)
        if kind != _V1:
            return [], False
        own.append(_Own(rec, r.record_id))
    return own, bool(own)


def _first_unnamed(shelf: Any, held: list, adjudicator: str, deal: str, own: list[_Own]) -> str:
    """The record digest of the first counted ruling of the deal by the
    adjudicator, over every case book, that ``own`` does not name; ``""``
    for none (Go ``firstUnnamed``)."""
    named = set()
    for o in own:
        if o.record.record_type == TYPE_STATEMENT:
            names = o.record.body.get("names")
            ruling = names.get("ruling") if isinstance(names, dict) else None
            if isinstance(ruling, dict):
                named.add(ruling["digest"])
    for log_id in logs_of(held, adjudicator, lambda r: r.record_type == TYPE_RULING):
        for h in counted(shelf, held, adjudicator, log_id):
            if h.record.block["deal_id"] != deal:
                continue
            d = digest(h.record)
            if d not in named:
                return d
    return ""


def _naming_statement(shelf: Any, held: list, ag: dict, deal: str, own: list[_Own], ruling: str,
                      at: datetime) -> tuple[Record, str]:
    """The statement naming ``ruling`` as the book's next record, and the
    record id it cites (Go ``namingStatement``): seen is the last record of
    the counterparty's counted prefix held now (PROFILE §6)."""
    seen = None
    key = ag["counterparty_key"]
    if isinstance(key, str):
        logs = logs_of(held, key, lambda r: r.record_type == TYPE_AGREE and r.body["kind"] == KIND_AGREEMENT)
        if logs:
            theirs = counted(shelf, held, key, logs[0])
            if theirs:
                seen = theirs[-1].record_id
    last = own[-1]
    block = {"profile": PROFILE, "canonicalization": CANONICALIZATION, "record_type": TYPE_STATEMENT,
             "deal_id": deal, "at": at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "seq": len(own) + 1,
             "prev": ref_to(digest(last.record)), "agreement_ref": ref_to(digest(own[0].record)), "seen": seen}
    r = decode(jcs({PROFILE: block, "body": {"statements": [], "names": {"ruling": ref_to(ruling)}}}))
    check(r)
    return r, last.record_id


@dataclass(frozen=True)
class HeldBookRecord:
    """A counted book record held from a sender (Go ``heldBookRecord``):
    the record, its record id and its book pair."""

    record: Record
    record_id: str
    pair: bytes


def logs_of(held: list, sender: str, first: Callable[[Record], bool]) -> list[str]:
    """The logs of ``sender``'s records held for the deal whose held first
    record passes ``first``, sorted (Go ``logsOf``)."""
    out: list[str] = []
    for h in held:
        rr = h.received
        if rr.sender_key != sender or rr.position != 1 or rr.log_id.startswith(SENT_LOG_PREFIX) or rr.log_id in out:
            continue
        try:
            b = verify_book(h.record)
        except ValueError:
            continue
        if first(b.record):
            out.append(rr.log_id)
    return sorted(out)


def counted(shelf: Any, held: list, sender: str, log_id: str) -> list[HeldBookRecord]:
    """``sender``'s counted prefix of ``log_id`` as far as the deal's held
    records reach it (Go ``counted``): from position 1, each the one held
    record that follows the last, up to the prefix's size and before any
    fork. It stops, never fails, at a counted record not held for the
    deal."""
    return counted_with(shelf, held, sender, log_id)[0]


def counted_with(shelf: Any, held: list, sender: str, log_id: str) -> tuple[list[HeldBookRecord], Any]:
    """:func:`counted` and the counted prefix it walked (Go ``countedWith``)."""
    prefix = shelf.counted_prefix(sender, log_id)
    limit = prefix.size
    if prefix.fork_at > 0 and prefix.fork_at - 1 < limit:
        limit = prefix.fork_at - 1
    following: dict[str, list] = {}
    for h in held:
        if h.received.sender_key == sender and h.received.log_id == log_id:
            following.setdefault(h.received.prev, []).append(h)
    out: list[HeldBookRecord] = []
    prev = ""
    for pos in range(1, limit + 1):
        cands = following.get(prev, [])
        if len(cands) != 1 or cands[0].received.position != pos:
            break
        h = cands[0]
        try:
            b = verify_book(h.record)
        except ValueError:
            break
        out.append(HeldBookRecord(b.record, h.received.digest, h.record))
        prev = h.received.digest
    return out, prefix
