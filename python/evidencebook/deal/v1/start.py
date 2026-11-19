# SPDX-License-Identifier: Apache-2.0
"""When a deal has started (design §5), as Go's ``dealprofile/xdealv1``
``start.go`` and ``start_books.go``.

A deal between two sides with the layer has started when the two
agreements cite the same offer and rows digest; the offerer's agreement
names the acceptor's in seen, and the acceptor's book names no cancellation
of that offer unless it named the offerer's agreement first; and the
offerer's agreement has a record time not after agree_by, or the acceptor's
book names it. Until then the deal's rows have no status, no dispute on it
counts and no card counts it.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from ..check import _go_quote
from .check import Record, _record_of, decode, digest, utc_second
from .names import KIND_AGREEMENT, SIDE_ACCEPTOR, SIDE_OFFERER, TYPE_AGREE, TYPE_PERFORM, TYPE_PROPOSE, TYPE_SETTLE, TYPE_STATEMENT

__all__ = [
    "STATE_STARTED", "STATE_AWAITING_AGREEMENT", "STATE_NOT_STARTED", "BY_OFFERER_IN_TIME", "BY_ACCEPTOR_NAMED",
    "BY_FIRST_ACT", "FirstAct", "StartInput", "StartResult", "BookRecord", "StartBooks", "start",
    "start_from_books",
]

STATE_STARTED = "started"
STATE_AWAITING_AGREEMENT = "awaiting_agreement"
STATE_NOT_STARTED = "not_started"

BY_OFFERER_IN_TIME = "offerer_in_time"
BY_ACCEPTOR_NAMED = "acceptor_named"
BY_FIRST_ACT = "first_act"


@dataclass(frozen=True)
class FirstAct:
    """The holder's first perform or settle record, for a deal whose
    counterparty runs no layer: ``time`` is its record time, or ``None``
    when it has none yet."""

    time: str | None = None


@dataclass(frozen=True)
class StartInput:
    """What :func:`start` reads (design §5.2), the status value's start
    member and the time it is computed at. A ``bool | None`` is ``None``
    when it does not apply: the agreements' match when the counterpart's
    agreement is not held."""

    at: str = ""
    side: str = ""
    counterparty_layer: bool = False
    agree_by: str = ""
    counterpart_agreement_held: bool = False
    same_offer_and_rows: bool | None = None
    offerer_names_acceptor: bool | None = None
    offerer_agreement_time: str | None = None
    acceptor_names_offerer: bool = False
    cancellation_named_first: bool = False
    first_act: FirstAct | None = None

    def value(self) -> dict:
        """The input as Go's ``json.Marshal`` writes it."""
        return {
            "at": self.at, "side": self.side, "counterparty_layer": self.counterparty_layer,
            "agree_by": self.agree_by, "counterpart_agreement_held": self.counterpart_agreement_held,
            "same_offer_and_rows": self.same_offer_and_rows, "offerer_names_acceptor": self.offerer_names_acceptor,
            "offerer_agreement_time": self.offerer_agreement_time,
            "acceptor_names_offerer": self.acceptor_names_offerer,
            "cancellation_named_first": self.cancellation_named_first,
            "first_act": None if self.first_act is None else {"time": self.first_act.time},
        }


@dataclass(frozen=True)
class StartResult:
    """Whether the deal has started (design §5.2). ``local`` says the result
    rests on what the holder holds and no witnessed time; ``by`` names what
    started it, ``None`` when nothing has yet."""

    state: str
    local: bool = False
    by: str | None = None

    def value(self) -> dict:
        return {"state": self.state, "local": self.local, "by": self.by}


def _start_time(name: str, s: str) -> tuple[int, ...]:
    t = utc_second(s)
    if t is None:
        raise ValueError(f"xdealv1: start: {name} {_go_quote(s)} is not a UTC time to the second")
    return t


def start(inp: StartInput) -> StartResult:
    """Whether a deal has started from the values in ``inp``, in design
    §5.2's order (Go ``Start``). It refuses (``ValueError``) a time that is
    not RFC 3339 UTC to the second and an unknown side; with a counterparty
    that runs the layer, also agree_by that is not such a time, a held
    counterpart agreement whose same_offer_and_rows or
    offerer_names_acceptor is ``None``, and an offerer that holds no
    acceptor's agreement, or one citing another offer or rows."""
    at = _start_time("at", inp.at)
    if inp.side not in (SIDE_OFFERER, SIDE_ACCEPTOR):
        raise ValueError(f"xdealv1: start: side {_go_quote(inp.side)} is neither offerer nor acceptor")
    not_started = StartResult(STATE_NOT_STARTED)
    # 1. A counterparty with no layer: the holder's first act starts it.
    if not inp.counterparty_layer:
        if inp.first_act is None:
            return not_started
        if inp.first_act.time is not None:
            _start_time("first_act.time", inp.first_act.time)
        return StartResult(STATE_STARTED, inp.first_act.time is None, BY_FIRST_ACT)
    agree_by = _start_time("agree_by", inp.agree_by)
    late = at > agree_by
    awaiting = not_started if late else StartResult(STATE_AWAITING_AGREEMENT)
    # 2. The acceptor named a cancellation first: never, whatever comes later.
    if inp.cancellation_named_first:
        return not_started
    if inp.counterpart_agreement_held and (inp.same_offer_and_rows is None or inp.offerer_names_acceptor is None):
        raise ValueError("xdealv1: start: a held counterpart agreement gives same_offer_and_rows and "
                         "offerer_names_acceptor")
    # 3. No counterpart agreement, or one about another deal.
    if not inp.counterpart_agreement_held or inp.same_offer_and_rows is False:
        if inp.side == SIDE_OFFERER:
            raise ValueError("xdealv1: start: an offerer writes its agreement after the acceptor's it holds")
        return awaiting
    # 4. The offerer's agreement does not join the acceptor's.
    if not inp.offerer_names_acceptor:
        return awaiting
    # 5. The acceptor's book names the offerer's agreement.
    if inp.acceptor_names_offerer:
        return StartResult(STATE_STARTED, False, BY_ACCEPTOR_NAMED)
    # 6. The offerer's agreement has a time in time.
    if inp.offerer_agreement_time is not None:
        t = _start_time("offerer_agreement_time", inp.offerer_agreement_time)
        if t <= agree_by:
            return StartResult(STATE_STARTED, False, BY_OFFERER_IN_TIME)
        return not_started
    # 7. No time and never named: started, locally, by nothing yet, also after
    # agree_by (the deal counts; a witnessed time after agree_by, or a naming,
    # settles it).
    return StartResult(STATE_STARTED, True)


@dataclass(frozen=True)
class BookRecord:
    """One record of a deal book as the holder holds it: the record, its
    record id (the Capsule id of the sealed book record), and the time each
    witness gave a shelf checkpoint covering it, by the witness's key id."""

    record: Any
    record_id: str
    times: dict = field(default_factory=dict)


@dataclass(frozen=True)
class StartBooks:
    """What :func:`start_from_books` reads: the holder's own deal book, the
    counted prefix of the counterparty's deal book the holder holds, and
    the offer both agreements cite. Each book is in order from its
    agreement. A record is a :class:`Record` or its JSON."""

    at: str
    offer: Any
    own: list = field(default_factory=list)
    counterpart: list = field(default_factory=list)


def _rec(r: Any) -> Record:
    return r if isinstance(r, Record) else _record_of(r) if isinstance(r, dict) else decode(r)


def start_from_books(b: StartBooks) -> tuple[StartInput, StartResult]:
    """Derives :func:`start`'s input from the two books (design §5.2) and
    computes it (Go ``StartFromBooks``). The offerer's agreement's record
    time is the earliest time a witness the agreements name gave it. It
    refuses (``ValueError``) books out of order, of another deal, a
    holder's book not opened by an agreement citing the offer, and a
    counterpart's book not opened by an agreement."""
    inp = _start_input(b)
    return inp, start(inp)


def _start_input(b: StartBooks) -> StartInput:
    def bad(detail: str) -> ValueError:
        return ValueError("xdealv1: start from books: " + detail)

    offer = _rec(b.offer)
    if offer.record_type != TYPE_PROPOSE:
        raise bad(f"the offer is not a propose but a {offer.record_type} record")
    offer_digest = digest(offer)
    books = {"own": [(_rec(r.record), r) for r in b.own],
             "counterpart": [(_rec(r.record), r) for r in b.counterpart]}
    # Go ranges over a map here: which book's refusal comes first is not
    # fixed; this reads the holder's first.
    for name, book in books.items():
        for i, (r, _) in enumerate(book):
            if r.block["seq"] != i + 1:
                raise bad(f"the {name} book's record {i + 1} has seq {r.block['seq']}")
            if r.block["deal_id"] != offer.block["deal_id"]:
                raise bad(f"the {name} book's record {i + 1} is of another deal")
    own_book, cp_book = books["own"], books["counterpart"]
    if not own_book:
        raise bad("the holder's book is empty")
    own_rec = own_book[0][0]
    if own_rec.record_type != TYPE_AGREE or own_rec.body["kind"] != KIND_AGREEMENT:
        raise bad("the holder's book does not open with its agreement")
    own = own_rec.body
    if own["offer"]["digest"] != offer_digest:
        raise bad("the holder's agreement does not cite the offer")
    layer = own["counterparty_layer"] is True
    fields: dict[str, Any] = {"at": b.at, "side": own["side"], "agree_by": offer.body["agree_by"],
                              "counterparty_layer": layer}
    if not layer:
        for r, held in own_book:
            if r.record_type in (TYPE_PERFORM, TYPE_SETTLE):
                fields["first_act"] = FirstAct(_record_time(held, own["witnesses"]))
                break
        return StartInput(**fields)
    offerer_book, acceptor_book = own_book, cp_book
    if own["side"] == SIDE_ACCEPTOR:
        offerer_book, acceptor_book = cp_book, own_book
    offerer_ids = [held.record_id for _, held in offerer_book]
    names_offerer = cancellation_first = False
    for r, _ in acceptor_book:
        if r.record_type == TYPE_STATEMENT and r.body["names"] is not None \
                and r.body["names"]["cancellation"] is not None and not names_offerer:
            cancellation_first = True
        seen = r.block["seen"]
        if isinstance(seen, str) and seen in offerer_ids:
            names_offerer = True
    fields["acceptor_names_offerer"] = names_offerer
    fields["cancellation_named_first"] = cancellation_first
    if not cp_book:
        return StartInput(**fields)
    cp_rec = cp_book[0][0]
    if cp_rec.record_type != TYPE_AGREE or cp_rec.body["kind"] != KIND_AGREEMENT:
        raise bad("the counterpart's book does not open with its agreement")
    cp = cp_rec.body
    fields["counterpart_agreement_held"] = True
    fields["same_offer_and_rows"] = cp["offer"]["digest"] == offer_digest and cp["rows_digest"] == own["rows_digest"]
    seen = offerer_book[0][0].block["seen"]
    fields["offerer_names_acceptor"] = isinstance(seen, str) and seen == acceptor_book[0][1].record_id
    offerer = offerer_book[0][0].body
    fields["offerer_agreement_time"] = _record_time(offerer_book[0][1], offerer["witnesses"])
    return StartInput(**fields)


def _record_time(r: BookRecord, witnesses: Any) -> str | None:
    """A record's record time from the witnesses named: the earliest time
    one of them gave it, or ``None``. A time from a witness not named is no
    time, and is not read."""
    earliest = None
    for w in witnesses or ():
        t = (r.times or {}).get(w["key_id"])
        if t is None:
            continue
        _start_time(f"the time witness {w['key_id']} gave record {r.record_id}", t)
        if earliest is None or t < earliest:
            earliest = t
    return earliest
