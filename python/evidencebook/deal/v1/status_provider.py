# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 deal book's status provider (design §8.1, §8.2), as Go's
``dealprofile/xdealv1`` ``status_provider.go`` (``StatusProvider``) and the
report it computes from a shelf (``report.go``, ``StatusProvider.Report``).

At each stamp the shelf asks it, through :meth:`StatusProvider.book_values`,
for a v1 deal book's profile, link value and status value; before each
stamp it names every counted ruling still unnamed
(:meth:`StatusProvider.before_stamp`). :meth:`StatusProvider.report` is a
copy of the v1 report of a deal book from what the shelf holds now.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any, Callable

from agent_action_capsule.canonical import jcs

from ...errors import BusyError, CorruptError
from .check import Record, StageError, digest, encode
from .names import KIND_AGREEMENT, PROFILE, TYPE_AGREE, TYPE_PROPOSE, TYPE_RULING, TYPE_STATEMENT
from .naming import counted, counted_with, logs_of, name_all_rulings
from .payment import PaymentInput, payment_check
from .report import build_report_rows
from .report_bundle import ReportBook
from .sent import SENT_LOG_PREFIX, book_pair, payment_rows, verify_book, verify_sent
from .status import build_status

__all__ = ["StatusProvider"]

_SIDE_OFFERER = "offerer"


@dataclass
class _Own:
    """A record of the holder's book (Go ``ownRecord``): the status record
    and its book pair."""

    rec: dict
    pair: bytes


class StatusProvider:
    """A v1 deal book's values at each stamp (Go ``StatusProvider``).

    ``shelf`` is the shelf it serves. ``offer_pair(offer_digest)`` returns
    the offer's sent pair as the host sent or received it (the library keeps
    no sent Capsule); ``None``, or a ``None`` pair, leaves a payment row's
    check not run."""

    def __init__(self, shelf: Any = None, offer_pair: Callable[[str], bytes | None] | None = None) -> None:
        self.shelf = shelf
        self.offer_pair = offer_pair

    # -- what a stamp asks ---------------------------------------------------------------

    def before_stamp(self, at: datetime) -> None:
        """Names every counted ruling the holder holds that its deal book
        does not name yet (Go ``BeforeStamp``, :func:`.naming.name_all_rulings`)."""
        if self.shelf is None:
            raise ValueError("xdealv1: the status provider has no shelf")
        name_all_rulings(self.shelf, at)

    def book_values(self, inp: Any) -> tuple[str, dict, dict] | None:
        """A v1 deal book's ``(profile, link value, status value)``, ``None``
        for any other book (Go ``BookValues``)."""
        if self.shelf is None:
            raise ValueError("xdealv1: the status provider has no shelf")
        got = self._status_books(inp)
        if got is None:
            return None
        books, link, _, _ = got
        return PROFILE, link, build_status(books)

    # -- the books -----------------------------------------------------------------------

    def _status_books(self, inp: Any) -> tuple[dict, dict, dict | None, dict[str, bytes]] | None:
        """What the status value is built from, as Go's ``StatusBooks``
        writes it, with the link value and the counterparty's counted prefix
        a report rests on and the book pair of each record read, by record id:
        the holder's own records and every record held for the deal (Go
        ``statusBooks``); ``None`` for a book that is not a v1 deal book."""
        own = self._own_book(inp)
        if not own:
            return None
        first = own[0].rec["record"]
        ag, deal = first.body, first.block["deal_id"]
        offer = self._offer(inp.journal, ag["offer"]["digest"])
        keys = [w["key_id"] for w in ag["witnesses"] or []]
        for i, o in enumerate(own):
            o.rec["time"] = self._own_time(inp, i + 1, keys)
        held = self.shelf.held_by_deal(deal)
        link: dict = {"last_received": None, "checked_over": []}
        theirs: list = []
        their_log, prefix = "", None
        counterpart: list[dict] = []
        proof_held = False
        key = ag["counterparty_key"]
        if isinstance(key, str):
            logs = logs_of(held, key, lambda r: r.record_type == TYPE_AGREE and r.body["kind"] == KIND_AGREEMENT)
            if logs:
                their_log = logs[0]
                theirs, prefix = counted_with(self.shelf, held, key, their_log)
            for i, t in enumerate(theirs):
                counterpart.append({"record": t.record, "record_id": t.record_id,
                                    "time": self._received_time(inp, their_log, i + 1, keys)})
            if theirs:
                last = theirs[-1]
                link["last_received"] = {"log_id": their_log, "size": len(theirs), "record_digest": last.record_id}
                from .holder import proof_covers

                proof_held = proof_covers(self.shelf, key, their_log, theirs[0].record_id, theirs[0].record.body)
        case_book: list[dict] = []
        conflicting: list[str] = []
        unnamed = 0
        adj = ag["adjudicator"]
        if isinstance(adj, dict):
            case_book, conflicting, unnamed = _case_book(self.shelf, held, adj["key_id"], [o.rec for o in own],
                                                         counterpart)
        payments = self._payments(offer, ag, own, theirs)
        over = sorted({d for r in payments.values() for d in r.checked_over})
        link["checked_over"] = over
        books: dict = {"offer": _value(offer), "own": [_status_record(o.rec) for o in own],
                       "counterpart": [_status_record(r) for r in counterpart],
                       "case_book": [_status_record(r) for r in case_book], "proof_held": proof_held}
        if conflicting:
            books["conflicting_final_rulings"] = conflicting
        if payments:
            books["payments"] = {row: r.value() for row, r in payments.items()}
        if unnamed:
            books["rulings_unnamed"] = unnamed
        cp = None
        if theirs:
            cp = {"log_id": their_log, "counted": len(theirs), "last": theirs[-1].record_id, "fork_at": prefix.fork_at}
        pairs = {o.rec["record_id"]: o.pair for o in own}
        for h in held:
            pairs.setdefault(h.received.digest, h.record)
        return books, link, cp, pairs

    def _own_book(self, inp: Any) -> list[_Own]:
        """The holder's book through its handle, ``[]`` when it is not opened
        by a v1 agreement; a record not readable now ends the read (Go
        ``ownBook``)."""
        from .holder import _V1, _v1_payload

        if inp.book is None or inp.journal is None:
            return []
        out: list[_Own] = []
        for i, r in enumerate(inp.book.records()):
            rec, kind = _v1_payload(inp.book, r)
            if kind != _V1:
                if i == 0:
                    return []
                break
            if i == 0 and (rec.record_type != TYPE_AGREE or rec.body["kind"] != KIND_AGREEMENT):
                return []
            stored = inp.book._stored[r.record_id]
            pair = book_pair(jcs(stored.capsule), stored.envelope, stored.header, encode(rec)[0])
            out.append(_Own({"record": rec, "record_id": r.record_id, "time": None}, pair))
        return out

    def _offer(self, journal: Any, d: str) -> Record:
        """The offer whose record digest is ``d``: the holder's own, in its
        journal, or the one it received sent whole (Go ``offer``)."""
        from .holder import _V1, _v1_payload

        for r in journal.records():
            rec, kind = _v1_payload(journal, r)
            if kind == _V1 and rec.record_type == TYPE_PROPOSE and digest(rec) == d:
                return rec
        for h in self.shelf.held_first(SENT_LOG_PREFIX + d):
            try:
                s = verify_sent(h.record)
            except (ValueError, StageError):
                continue
            if s.digest == d:
                return s.record
        raise ValueError(f"xdealv1: status: the deal's offer {d} is not held")

    def _own_time(self, inp: Any, seq: int, keys: list[str]) -> Any:
        """The time of the holder's record at ``seq`` (Go ``ownTime``): from a
        named witness, else the first stamp covering it, else this stamp."""
        t = self.shelf.record_time_by_key(inp.book_id, seq, keys)
        if t is not None:
            return _known(t)
        n = self.shelf.record_stamp(inp.book_id, seq)
        return _stamp(n if n is not None else inp.n)

    def _received_time(self, inp: Any, log_id: str, pos: int, keys: list[str]) -> Any:
        """The time the holder's stamps give the counterparty's record at
        ``pos`` (Go ``receivedTime``)."""
        t = self.shelf.received_record_time_by_key(inp.book_id, log_id, pos, keys)
        if t is not None:
            return _known(t)
        n = self.shelf.received_record_stamp(inp.book_id, log_id, pos)
        return _stamp(n if n is not None else inp.n)

    def _payments(self, offer: Record, ag: dict, own: list[_Own], theirs: list) -> dict:
        """Each payment row's check over both books, when the host gives the
        offer's sent pair (Go ``payments``)."""
        rows = payment_rows(offer.body["terms_rows"])
        if self.offer_pair is None or not rows:
            return {}
        od = digest(offer)
        pair = self.offer_pair(od)
        if pair is None:
            return {}
        acceptor = ""
        if ag["side"] == _SIDE_OFFERER:
            if isinstance(ag["counterparty_key"], str):
                acceptor = ag["counterparty_key"]
        else:
            try:
                acceptor = verify_book(own[0].pair).key_id
            except (ValueError, StageError):
                acceptor = ""
        legs = [o.pair for o in own] + [t.pair for t in theirs]
        out = {}
        for row in rows:
            try:
                res = payment_check(PaymentInput(pair, od, acceptor, row, legs))
            except StageError:
                if acceptor == "":
                    continue  # no acceptor key known yet: not run
                raise
            out[row] = res
        return out

    # -- the report ----------------------------------------------------------------------

    def report(self, log_id: str, copy: dict) -> dict:
        """A copy of the v1 report of the holder's deal book ``log_id`` (Go
        ``StatusProvider.Report``), from what the shelf holds now, at the
        book's latest checkpoint, which must cover the whole book. It writes
        nothing; it takes the shelf's locks, so it is never called from a
        stamp or a receive."""
        return self.report_with_book(log_id, copy)[0]

    def report_with_book(self, log_id: str, copy: dict) -> tuple[dict, ReportBook]:
        """:meth:`report` with the book pairs the report rests on, read in
        the same snapshot (Go ``StatusProvider.ReportWithBook``; design §9,
        "The bundle extension"): the holder's agreement at seq 1 and every
        record the report's rows rely on, whether the copy lists its rows or
        not. The host carries them as the x-deal-v1 bundle extension's book
        (:class:`.report_bundle.ReportBundle`) beside the sealed report."""
        from .holder import _V1, _v1_payload

        if self.shelf is None:
            raise ValueError("xdealv1: the status provider has no shelf")
        book = self.shelf.open(log_id)
        first = [r for r in book.records() if r.seq == 1]
        not_v1 = ValueError(f"xdealv1: report: book {log_id} is not an x-deal-v1 deal book")
        if len(first) != 1:
            raise not_v1
        rec, kind = _v1_payload(book, first[0])
        if kind != _V1 or rec.record_type != TYPE_AGREE or rec.body["kind"] != KIND_AGREEMENT:
            raise not_v1
        journal = self.shelf.existing_journal()
        if journal is None:
            raise ValueError("xdealv1: report: the shelf has no journal, which holds the deal's offer")
        book_id = first[0].record_id
        for _ in range(3):
            got = self._report_books(log_id, book_id, book, journal)
            if got is not None:
                return _report_with_book(*got, copy)
        raise BusyError(f"report: book {log_id} kept changing while it was read")

    def _report_books(self, log_id: str, book_id: str, book: Any, journal: Any) -> tuple[dict, dict[str, bytes]] | None:
        """One snapshot of what a report rests on, as Go's ``ReportBooks``
        writes it with the book pair of each record read, by record id;
        ``None`` when the book moved during the read (Go ``reportBooks``)."""
        from ...shelf_stamp import StatusInput

        cps = book.checkpoints()
        size = book.size()
        if not cps or cps[-1].entries != size:
            raise ValueError(f"xdealv1: report: book {log_id} has no checkpoint at its size {size}; "
                             "a stamp checkpoints it")
        cp = cps[-1]
        got = self._status_books(StatusInput(0, book_id, log_id, book, journal))
        if got is None:
            raise ValueError(f"xdealv1: report: book {log_id} is not an x-deal-v1 deal book")
        books, _, counterpart, pairs = got
        if book.size() != size:
            return None
        if len(books["own"]) != size:
            raise ValueError(f"xdealv1: report: book {log_id} holds a record past seq {len(books['own'])} "
                             "that is not a readable x-deal-v1 record")
        return {"Books": books, "Stamps": None, "Counterpart": counterpart,
                "Own": {"log_id": log_id, "size": size, "last": books["own"][-1]["record_id"]},
                "Checkpoint": {"log_id": log_id, "mmr_size": cp.tree_size, "root": cp.root},
                "Fact": self._fact_current(book_id, books)}, pairs

    def _fact_current(self, book_id: str, books: dict) -> dict | None:
        """The book's last status fact against the status value the books
        build now, in JCS form; ``None`` before its first fact (Go
        ``factCurrent``)."""
        last = None
        for f in self.shelf.openings(book_id, "owner"):
            if f.status_opening is not None:
                last = f.status_opening
        if last is None:
            return None
        now = jcs(build_status(books))
        try:
            was = jcs(last.value)
        except Exception as exc:  # noqa: BLE001 - a stored opening JCS refuses
            raise CorruptError(f"book {book_id}'s last status opening: {exc}") from None
        return {"current": now == was}


def _report_with_book(rb: dict, pairs: dict[str, bytes], copy: dict) -> tuple[dict, ReportBook]:
    """The report and its book from ``pairs``, the book pair of each record
    the snapshot read, by record id (Go ``reportWithBook``)."""
    r, rows = build_report_rows(rb, copy)

    def pair(rid: str) -> bytes:
        if rid not in pairs:
            raise ValueError(f"xdealv1: report: no book pair read for record {rid}")
        return pairs[rid]

    agreement = pair(rb["Books"]["own"][0]["record_id"])
    relied = {src["record"]: pair(src["record"]) for row in rows for src in row["relied_on"]}
    return r, ReportBook(agreement, relied)


def _stamp(n: int) -> Any:
    """Go's ``StampTime(n)`` as it marshals: ``{"stamp": n}``, or null for
    0 (no stamp: a report reads no stamp of its own)."""
    return {"stamp": n} if n else None


def _known(unix_seconds: int) -> str:
    """Go's ``time.Unix(t, 0).UTC().Format(time.RFC3339)`` for any integer,
    a year past 9999 or before 1 included (Go's ``2006`` layout: at least
    four digits, a minus sign before a negative year)."""
    days, rem = divmod(unix_seconds, 86400)
    # Howard Hinnant's civil_from_days, on the proleptic Gregorian calendar.
    z = days + 719468
    era = z // 146097
    doe = z - era * 146097
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    y = yoe + era * 400
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    d = doy - (153 * mp + 2) // 5 + 1
    m = mp + 3 if mp < 10 else mp - 9
    y += m <= 2
    year = f"-{-y:04d}" if y < 0 else f"{y:04d}"
    hh, rem = divmod(rem, 3600)
    mm, ss = divmod(rem, 60)
    return f"{year}-{m:02d}-{d:02d}T{hh:02d}:{mm:02d}:{ss:02d}Z"


def _value(r: Record) -> Any:
    return r.value


def _status_record(r: dict) -> dict:
    """A status record as Go's ``StatusRecord`` writes it."""
    return {"record": _value(r["record"]), "record_id": r["record_id"], "time": r["time"]}


def _case_book(shelf: Any, held: list, adjudicator: str, own: list[dict], theirs: list[dict]) -> tuple[list, list, int]:
    """The adjudicator's rulings either side has named arriving, timed by the
    earliest naming; the rows whose case book forked with different first
    final rulings; and how many counted rulings neither book names (Go
    ``caseBook``)."""
    rulings: list[dict] = []
    conflicting: set[str] = set()
    for log_id in logs_of(held, adjudicator, lambda r: r.record_type == TYPE_RULING):
        for h in counted(shelf, held, adjudicator, log_id):
            rulings.append({"record": h.record, "record_id": h.record_id, "time": None})
        conflicting.update(_forked_finals(held, adjudicator, log_id))
    out = _named_rulings(rulings, own, theirs)
    return out, sorted(conflicting), len(rulings) - len(out)


def _named_rulings(rulings: list[dict], own: list[dict], theirs: list[dict]) -> list[dict]:
    """The rulings either book names arriving, in case book order, each
    timed by the earliest naming (Go ``namedRulings``)."""
    named_at: dict[str, Any] = {}
    for book in (own, theirs):
        for r in book:
            rec = r["record"]
            if rec.record_type != TYPE_STATEMENT:
                continue
            names = rec.body.get("names")
            ruling = names.get("ruling") if isinstance(names, dict) else None
            if isinstance(ruling, dict):
                d = ruling["digest"]
                if d not in named_at or _earlier(r["time"], named_at[d]):
                    named_at[d] = r["time"]
    out = []
    for r in rulings:
        d = digest(r["record"])
        if d in named_at:
            out.append({**r, "time": named_at[d]})
    return out


def _earlier(a: Any, b: Any) -> bool:
    """Whether naming time ``a`` is earlier than ``b`` (Go ``earlier``): a
    known time before a stamp, else the earlier known time or the lower
    stamp."""
    ak, bk = isinstance(a, str), isinstance(b, str)
    if ak and bk:
        return a < b
    if ak or bk:
        return ak
    return (a or {}).get("stamp", 0) < (b or {}).get("stamp", 0)


def _forked_finals(held: list, adjudicator: str, log_id: str) -> list[str]:
    """The rows whose first final ruling differs between two branches of the
    adjudicator's case book held (Go ``forkedFinals``): each branch, from
    position 1 to a record no other held record follows, unbroken, applies
    its own first final ruling per row."""
    nodes: dict[str, tuple[str, dict | None]] = {}
    followed: set[str] = set()
    for h in held:
        rr = h.received
        if rr.sender_key != adjudicator or rr.log_id != log_id:
            continue
        try:
            b = verify_book(h.record)
        except (ValueError, StageError):
            continue
        ruling = b.record.body if b.record.record_type == TYPE_RULING else None
        nodes[rr.digest] = (rr.prev, ruling)
        followed.add(rr.prev)

    def decision(rb: dict) -> bytes:
        # Go compares {value, against, remedy} as it marshals them; any
        # faithful encoding compares the same.
        return jcs({"value": rb["value"], "against": rb["against"], "remedy": _go_value(rb["remedy"])})

    firsts: dict[str, set[bytes]] = {}
    for leaf in nodes:
        if leaf in followed:
            continue
        chain = []
        whole = False
        d = leaf
        while d in nodes:
            n = nodes[d]
            chain.append(n)
            if n[0] == "":
                whole = True
                break
            d = n[0]
        if not whole:
            continue
        seen: set[str] = set()
        for prev, rb in reversed(chain):
            if rb is None or not rb["final"] or rb["row"] in seen:
                continue
            seen.add(rb["row"])
            firsts.setdefault(rb["row"], set()).add(decision(rb))
    return sorted(row for row, ds in firsts.items() if len(ds) > 1)



def _go_value(v: Any) -> Any:
    """A Go value of the types table as JSON, absent members left out."""
    from .gotypes import ABSENT

    if isinstance(v, dict):
        return {k: _go_value(e) for k, e in v.items() if e is not ABSENT}
    if isinstance(v, list):
        return [_go_value(e) for e in v]
    return None if v is ABSENT else v
