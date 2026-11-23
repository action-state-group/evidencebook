# SPDX-License-Identifier: Apache-2.0
"""The v1 deal book's status value (design §8.1) and the row inputs it is
built from (§6.1), as Go's ``dealprofile/xdealv1`` ``status_rows.go``,
``status_select.go`` and ``status_value.go``.

Books, records and values are JSON values in the shape Go's types write
(the vectors under ``testdata/deal-v1/status`` are such values). A record is
read as :func:`.check.decode` reads it. A refusal raises
:class:`StatusError`.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .. import standard_history
from ... import row_status
from .check import Record, _record_of
from . import golenient, gotypes
from .gotypes import GoTypeError, NIL_RAW
from .names import (
    KIND_AGREEMENT, KIND_DISPUTE, KIND_WITHDRAWAL, ROLE_BUYER, ROLE_SELLER, SIDE_OFFERER, TYPE_AGREE,
    TYPE_DISPUTE, TYPE_PERFORM, TYPE_PROPOSE, TYPE_RULING, TYPE_SETTLE, TYPE_STATEMENT,
)
from .start import BookRecord, StartBooks, _start_input

__all__ = [
    "OUTCOME_OPEN", "OUTCOME_FOR_CLAIMANT", "OUTCOME_AGAINST_CLAIMANT", "OUTCOME_NO_FINDING", "OUTCOME_WITHDRAWN",
    "StatusError", "TimedRecord", "build_status", "resolve_row", "row_inputs", "status_time",
]

OUTCOME_OPEN = "open"
OUTCOME_FOR_CLAIMANT = "for_claimant"
OUTCOME_AGAINST_CLAIMANT = "against_claimant"
OUTCOME_NO_FINDING = "no_finding"
OUTCOME_WITHDRAWN = "withdrawn"


class StatusError(ValueError):
    """Books a status value, its row inputs or a report refuses."""


# -- times -----------------------------------------------------------------------------


def status_time(v: Any) -> Any:
    """A status time as Go's ``StatusTime.UnmarshalJSON`` reads it: null for
    no such record; a string, its known time (an empty one is no record);
    else ``{"stamp": n}`` read strictly (no other member, the member in any
    case, the later spelling winning, a null clearing it) with ``n >= 1``.
    It is returned as it is written: the string, ``None``, or
    ``{"stamp": n}``."""
    if v is None or v == "":
        return None
    if isinstance(v, str):
        return v
    bad = StatusError('xdealv1: a status time is a string, null or {"stamp": n} with n >= 1')
    if not isinstance(v, dict):
        raise bad
    n = None
    for k, e in v.items():
        if golenient.fold(k) != "stamp":
            raise bad
        if e is None:
            n = None
        elif isinstance(e, bool) or not isinstance(e, int) or not 0 <= e < 1 << 64:
            raise bad
        else:
            n = e
    if not n:
        raise bad
    return {"stamp": n}


def _known(t: Any) -> str | None:
    """A status time as rowstatus reads one: its string, or ``None``."""
    return t if isinstance(t, str) else None


def resolve_time(t: Any, stamps: dict[int, str]) -> str | None:
    """A status time as a reader resolves it: a known time, a stamp's time
    from ``stamps``, or no time (Go ``resolveTime``)."""
    if isinstance(t, str):
        return t
    if isinstance(t, dict):
        return stamps.get(t["stamp"])
    return None


# -- the books -------------------------------------------------------------------------


@dataclass
class TimedRecord:
    """A record of a deal book or case book as the holder holds it, with
    its time: a known time, a stamp, or ``None`` (Go ``TimedRecord`` and
    ``StatusRecord``)."""

    record: Record
    record_id: str
    time: Any = None


def _record(v: Any) -> Record:
    """A record inside books, read as Go's ``Record.UnmarshalJSON`` reads
    it: strictly, a null refused."""
    if isinstance(v, Record):
        return v
    if v is None:
        raise StatusError("xdealv1: a record has an x-deal-v1 block and a body")
    try:
        return _record_of(v)
    except GoTypeError as exc:
        raise StatusError(str(exc)) from None


def _zero_record() -> Record:
    """Go's zero ``Record``: a member left out. Its record type is none, so
    no reader takes it for a record of a type."""
    return Record(gotypes.zero(gotypes.block_type()), {})


_CUSTOM: golenient.Custom = {"record": lambda v: _record(v), "status_time": lambda v: status_time(v)}


def _decode(v: Any, books: str) -> dict:
    """Books as Go's ``json.Unmarshal`` reads them into its type: books is
    ``status_books``, ``row_books`` or ``report_books``."""
    try:
        return golenient.read(v, gotypes.table()[books], _CUSTOM)
    except golenient.GoDecodeError as exc:
        raise StatusError(str(exc)) from None


def _records(rs: Any) -> list[TimedRecord]:
    return [TimedRecord(r["record"] if r["record"] is not None else _zero_record(), r["record_id"], r["time"])
            for r in rs or ()]


@dataclass
class _Books:
    offer: Record
    own: list[TimedRecord]
    counterpart: list[TimedRecord]
    case_book: list[TimedRecord]
    conflicting: list[str] = field(default_factory=list)
    payments: dict[str, dict] = field(default_factory=dict)
    checks: dict[str, str] = field(default_factory=dict)
    proof_held: bool = False
    rulings_unnamed: int = 0


def _read_books(b: Any) -> _Books:
    """Status books as Go's ``json.Unmarshal`` into ``StatusBooks`` reads them."""
    if isinstance(b, _Books):
        return b
    return _books_of(_decode(b, "status_books"))


def _books_of(d: dict) -> _Books:
    """Status books already read into Go's ``StatusBooks`` (:func:`_decode`)."""
    payments = d["payments"] or {}
    return _Books(
        offer=d["offer"] if d["offer"] is not None else _zero_record(),
        own=_records(d["own"]), counterpart=_records(d["counterpart"]), case_book=_records(d["case_book"]),
        conflicting=d["conflicting_final_rulings"] or [], payments=payments,
        checks={row: p["check"] for row, p in payments.items() if p["check"]},
        proof_held=d["proof_held"], rulings_unnamed=d["rulings_unnamed"],
    )


def _agreement(own: list[TimedRecord], what: str) -> dict:
    if not own:
        raise StatusError(f"xdealv1: {what}: the holder's book is empty")
    first = own[0].record
    if first.record_type != TYPE_AGREE or first.body["kind"] != KIND_AGREEMENT:
        raise StatusError(f"xdealv1: {what}: the holder's book does not open with its agreement")
    return first.body


def _other(role: str) -> str:
    return ROLE_SELLER if role == ROLE_BUYER else ROLE_BUYER


# -- row inputs (status_rows.go) -------------------------------------------------------

_OWN, _COUNTERPART, _CASE = "o", "c", "k"


@dataclass
class _Sources:
    """For one row's input, the record each element came from, in the
    input's order (Go ``rowSources``)."""

    named: dict[str, tuple[str, int]]
    statements: list[tuple[str, int]] = field(default_factory=list)
    disputes: list[tuple[str, int]] = field(default_factory=list)
    withdrawals: list[tuple[str, int]] = field(default_factory=list)
    rulings: list[tuple[str, int]] = field(default_factory=list)
    remedies: list[tuple[str, int]] = field(default_factory=list)


def _statement_items(r: Record) -> list[dict]:
    if r.record_type in (TYPE_PERFORM, TYPE_SETTLE, TYPE_STATEMENT):
        return r.body["statements"] or []
    return []


def _project_date(d: dict) -> dict:
    out: dict[str, Any] = {}
    if d["at"] != "":
        out["at"] = d["at"]
    if d["after"] != "":
        out["after"] = d["after"]
    if d["seconds"] is not None and d["seconds"] != 0:
        out["seconds"] = d["seconds"]
    return out


def _row_value(row: dict) -> dict:
    """A terms row as rowstatus's ``Row`` writes it."""
    out = {"id": row["id"], "owed_by": row["owed_by"], "due": _project_date(row["due"]),
           "window_end": _project_date(row["window_end"])}
    if row["recomputed"]:
        out["recomputed"] = True
    return out


def row_inputs(books: Any, at: str) -> list[dict]:
    """One rowstatus input per agreed row, as of ``at`` (design §4.2, §4.4,
    §6.1; Go ``RowInputs``). ``books`` is Go's ``RowBooks``: ``offer``,
    ``own``, ``counterpart`` and ``case_book`` (each record with its time, a
    known time or null), ``conflicting_final_rulings``, and ``checks``, the
    recomputed rows' check results by row id."""
    d = _decode(books, "row_books")
    b = _Books(d["offer"] if d["offer"] is not None else _zero_record(), _records(d["own"]),
               _records(d["counterpart"]), _records(d["case_book"]), d["conflicting_final_rulings"] or [], {},
               d["checks"] or {})
    return _row_inputs(b, at)[0]


def _row_inputs(b: _Books, at: str) -> tuple[list[dict], list[_Sources]]:
    if b.offer.record_type != TYPE_PROPOSE:
        raise StatusError(f"xdealv1: row inputs: the offer is a {b.offer.record_type} record")
    if not b.own:
        raise StatusError("xdealv1: row inputs: the holder's book is empty")
    first = b.own[0].record
    if first.record_type != TYPE_AGREE or first.body["kind"] != KIND_AGREEMENT:
        raise StatusError("xdealv1: row inputs: the holder's book does not open with its agreement")
    own = first.body
    own_role = own["role"]
    other_role = _other(own_role) if own_role == ROLE_BUYER else ROLE_BUYER
    books = {own_role: b.own, other_role: b.counterpart}
    book_of = {own_role: _OWN, other_role: _COUNTERPART}
    tr = b.offer.body["terms_rows"]

    named: dict[str, dict] = {}
    named_src: dict[str, tuple[str, int]] = {}
    for name, n in (tr["named"] or {}).items():
        t = None
        for i, r in enumerate(books.get(n["side"], [])):
            if name in (r.record.block["marks"] or ()):
                t = r.time
                named_src[name] = (book_of[n["side"]], i)
                break
        named[name] = {"time": t, "latest": n["latest"]}

    rulings: list[tuple[TimedRecord, int]] = []
    if isinstance(own["adjudicator"], dict):
        for i, r in enumerate(b.case_book):
            if r.record.record_type == TYPE_RULING and r.record.block["deal_id"] == b.offer.block["deal_id"]:
                rulings.append((r, i))

    rows = tr["rows"] or []
    for rid in b.checks:
        if not any(r["id"] == rid for r in rows):
            raise StatusError(f"xdealv1: row inputs: a check given for row {_q(rid)}, which the terms do not have")
    out, srcs = [], []
    for row in rows:
        src = _Sources(named_src)
        inp: dict[str, Any] = {"at": at, "parties": [ROLE_BUYER, ROLE_SELLER], "row": _row_value(row)}
        if named:
            inp["named"] = named
        conflicting = row["id"] in b.conflicting
        if conflicting:
            inp["conflicting_final_rulings"] = True
        if row["id"] in b.checks:
            if not row["recomputed"]:
                raise StatusError(f"xdealv1: row inputs: a check given for row {_q(row['id'])}, which is not "
                                  "recomputed")
            inp["check"] = b.checks[row["id"]]
        stmts, disputes, withdrawals = [], [], []
        for side in (ROLE_BUYER, ROLE_SELLER):
            for i, r in enumerate(books.get(side, [])):
                ref = (book_of[side], i)
                for it in _statement_items(r.record):
                    if it["row"] == row["id"]:
                        stmts.append({"side": side, "value": it["value"], "time": r.time})
                        src.statements.append(ref)
                if r.record.record_type == TYPE_DISPUTE and r.record.body["row"] == row["id"]:
                    if r.record.body["kind"] == KIND_DISPUTE:
                        disputes.append({"side": side, "time": r.time, "seen_in_time": False})
                        src.disputes.append(ref)
                    elif r.record.body["kind"] == KIND_WITHDRAWAL:
                        withdrawals.append({"side": side, "time": r.time})
                        src.withdrawals.append(ref)
        rul: list[dict] = []
        effective = None
        for r, k in rulings:
            rb = r.record.body
            if rb["row"] != row["id"]:
                continue
            ru: dict[str, Any] = {"final": rb["final"], "value": rb["value"], "time": r.time}
            if rb["remedy"] is not None:
                ru["remedy_due"] = rb["remedy"]["due"]
            rul.append(ru)
            src.rulings.append((_CASE, k))
            if rb["final"] and effective is None and not conflicting:
                effective = rb
        remedies: list[dict] = []
        against = effective["against"] if effective is not None else None
        if isinstance(against, str) and effective["remedy"] is not None:
            for i, r in enumerate(books.get(against, [])):
                p = r.record.body
                if r.record.record_type == TYPE_PERFORM and p["remedy_for"] is not None \
                        and p["remedy_for"]["row"] == row["id"] and p["act"] == effective["remedy"]["kind"]:
                    remedies.append({"time": r.time})
                    src.remedies.append((book_of[against], i))
        for k, v in (("statements", stmts), ("disputes", disputes), ("withdrawals", withdrawals),
                     ("rulings", rul), ("remedies", remedies)):
            if v:
                inp[k] = v
        _seen_in_time(inp, books, row["id"])
        out.append(inp)
        srcs.append(src)
    return out, srcs


def _compute(inp: dict) -> row_status.Outcome:
    try:
        return row_status.compute(inp)
    except row_status.Invalid as exc:
        raise StatusError(f"rowstatus: {exc}") from None


def _seen_in_time(inp: dict, books: dict[str, list[TimedRecord]], row_id: str) -> None:
    """Each dispute's seen_in_time (design §6.2; Go ``seenInTime``)."""
    if not inp.get("disputes"):
        return
    plain = {k: v for k, v in inp.items() if k not in ("disputes", "withdrawals", "rulings", "remedies")}
    end = _compute(plain).window_end
    if end == "":
        return
    for d in inp["disputes"]:
        other = ROLE_SELLER if d["side"] == ROLE_BUYER else ROLE_BUYER
        standing, floor = -1, None
        book = books.get(other, [])
        for j, r in enumerate(book):
            if not any(it["row"] == row_id for it in _statement_items(r.record)):
                continue
            late = (r.time is not None and r.time > end) or (r.time is None and floor is not None and floor > end)
            if not late:
                standing = j
            if r.time is not None and (floor is None or r.time > floor):
                floor = r.time
        if standing < 0:
            continue
        ids = {r.record_id: j for j, r in enumerate(book)}
        for r in books.get(d["side"], []):
            s = r.record.block["seen"]
            if not isinstance(s, str) or r.time is None or r.time > end:
                continue
            j = ids.get(s)
            if j is not None and j >= standing:
                d["seen_in_time"] = True
                break


# -- the statements a fact carries (status_select.go) ----------------------------------


def _carried(stmts: list[dict], window_end: str) -> list[int]:
    """The indexes of the statements of one row a fact carries (design
    §8.1, invariant A; Go ``carriedStatements``)."""
    if window_end == "":
        return list(range(len(stmts)))
    keep = [False] * len(stmts)
    possible: dict[str, set[str]] = {}
    for side in (ROLE_BUYER, ROLE_SELLER):
        possible[side] = set()
        latest_in = -1
        for i, s in enumerate(stmts):
            if s["side"] != side:
                continue
            if s["time"] is None:
                keep[i] = True
                possible[side].add(s["value"])
                best = -1
                for j in range(i):
                    t = stmts[j]["time"]
                    if stmts[j]["side"] == side and t is not None and (best < 0 or t >= stmts[best]["time"]):
                        best = j
                if best >= 0:
                    keep[best] = True
            elif s["time"] <= window_end:
                latest_in = i
        if latest_in >= 0:
            keep[latest_in] = True
            possible[side].add(stmts[latest_in]["value"])
    for i, s in enumerate(stmts):
        if s["time"] is None or s["time"] <= window_end:
            continue
        other = ROLE_SELLER if s["side"] == ROLE_BUYER else ROLE_BUYER
        if s["value"] in possible.get(other, set()):
            keep[i] = True
    return [i for i, k in enumerate(keep) if k]


# -- the status value (status_value.go) ------------------------------------------------


def _q(s: str) -> str:
    from ..check import _go_quote

    return _go_quote(s)


def value_band(terms: dict) -> dict | None:
    """The agreed price's band: its currency and decade, ``None`` for a
    zero or missing price or a currency with no known scale (Go
    ``valueBand``)."""
    minor, currency = terms.get("price_minor"), terms.get("currency") or ""
    if minor is None or minor <= 0 or currency == "":
        return None
    amount = owed_amount({"amount_minor": minor, "currency": currency})
    if amount is None:
        return None
    return {"currency": currency, "decade": len(amount["value"]) - amount["assetScale"] - 1}


def owed_amount(owed: Any) -> dict | None:
    """A row's owed as a settlement amount: ``amount_minor`` as the value,
    the currency as the assetCode, and its assetScale (Go ``owedAmount``):
    exactly those two members, by their exact names, ``amount_minor`` a
    non-negative integer and ``currency`` a string with a known scale."""
    if owed is NIL_RAW or not isinstance(owed, dict) or len(owed) != 2 or owed.get("amount_minor") is None \
            or owed.get("currency") is None:
        return None
    minor, currency = owed["amount_minor"], owed["currency"]
    if isinstance(minor, bool) or not isinstance(minor, int) or not -(1 << 63) <= minor < 1 << 63 or minor < 0:
        return None
    if not isinstance(currency, str):
        return None
    scale = standard_history._history_asset_scale(currency)
    if scale is None:
        return None
    return {"value": str(minor), "assetCode": currency, "assetScale": scale}


def _witnesses(ws: Any) -> list[dict]:
    out = []
    for w in ws or ():
        v = {"key_id": w["key_id"]}
        if w["url"] != "":
            v["url"] = w["url"]
        out.append(v)
    return out


def build_status(books: Any) -> dict:
    """The status value from the books (design §8.1; Go ``BuildStatus``),
    as Go writes it. It refuses (:class:`StatusError`) books that
    :func:`row_inputs` refuses or that the start's derivation refuses."""
    b = _read_books(books)
    if b.offer.record_type != TYPE_PROPOSE:
        raise StatusError(f"xdealv1: status: the offer is a {b.offer.record_type} record")
    own = _agreement(b.own, "status")
    rb = _Books(b.offer, [TimedRecord(r.record, r.record_id, _known(r.time)) for r in b.own],
                [TimedRecord(r.record, r.record_id, _known(r.time)) for r in b.counterpart],
                [TimedRecord(r.record, r.record_id, _known(r.time)) for r in b.case_book],
                b.conflicting, b.payments, b.checks)
    agreed_at = b.own[0].record.block["at"]
    as_of = agreed_at
    for rs in (b.own, b.counterpart, b.case_book):
        for r in rs:
            k = _known(r.time)
            if k is not None and k > as_of:
                as_of = k
    ins, srcs = _row_inputs(rb, as_of)
    layer = own["counterparty_layer"] is True
    adj = own["adjudicator"]
    v: dict[str, Any] = {
        "v": 1, "role": own["role"], "side": own["side"], "agreed_at": agreed_at, "counterparty_layer": layer,
        "counterparty_kind": None, "value_band": value_band(b.offer.body["terms"]),
        "witnesses": _witnesses(own["witnesses"]),
        "adjudicator": {"key_id": adj["key_id"], "rules_digest": adj["rules_digest"]} if isinstance(adj, dict)
        else None,
        "counterparty_proof_held": b.proof_held, "start": _status_start(b), "rows": [], "disputes": [],
    }
    if b.rulings_unnamed:
        v["rulings_unnamed"] = b.rulings_unnamed
    sbooks = {_OWN: b.own, _COUNTERPART: b.counterpart}
    own_role = own["role"]
    other_role = ROLE_SELLER if own_role == ROLE_BUYER else ROLE_BUYER
    book_of_role = {own_role: _OWN, other_role: _COUNTERPART}

    def time_of(ref: tuple[str, int]) -> Any:
        book, i = ref
        return (b.own if book == _OWN else b.counterpart if book == _COUNTERPART else b.case_book)[i].time

    for inp, src in zip(ins, srcs):
        res = _compute(inp)
        stmts = inp.get("statements", [])
        kept = _carried(stmts, res.window_end)
        rin: dict[str, Any] = {"parties": inp["parties"], "row": inp["row"]}
        if inp.get("named"):
            rin["named"] = {name: {"time": time_of(src.named[name]) if name in src.named else None,
                                   "latest": n["latest"]} for name, n in inp["named"].items()}
        if kept:
            rin["statements"] = [{"side": stmts[i]["side"], "value": stmts[i]["value"],
                                  "time": time_of(src.statements[i])} for i in kept]
        if inp.get("check"):
            rin["check"] = inp["check"]
        if inp.get("disputes"):
            rin["disputes"] = [{"side": d["side"], "time": time_of(src.disputes[i]), "seen_in_time": d["seen_in_time"]}
                               for i, d in enumerate(inp["disputes"])]
        if inp.get("withdrawals"):
            rin["withdrawals"] = [{"side": w["side"], "time": time_of(src.withdrawals[i])}
                                  for i, w in enumerate(inp["withdrawals"])]
        if inp.get("rulings"):
            rin["rulings"] = []
            for i, r in enumerate(inp["rulings"]):
                ru = {"final": r["final"], "value": r["value"], "time": time_of(src.rulings[i])}
                if "remedy_due" in r:
                    ru["remedy_due"] = r["remedy_due"]
                rin["rulings"].append(ru)
        if inp.get("conflicting_final_rulings"):
            rin["conflicting_final_rulings"] = True
        if inp.get("remedies"):
            rin["remedies"] = [{"time": time_of(src.remedies[i])} for i in range(len(inp["remedies"]))]
        row_id = inp["row"]["id"]
        effective = _effective_ruling(b, row_id, bool(inp.get("conflicting_final_rulings")))
        against = effective["against"] if effective is not None else None
        row: dict[str, Any] = {
            "id": row_id, "input": rin,
            "statement_named_by_other": [_named_by_other(sbooks, src.statements[i]) for i in kept],
            "ruling_against": against if isinstance(against, str) else None,
            "remedy_made": _remedy_made(sbooks.get(book_of_role.get(inp["row"]["owed_by"]), []), row_id),
        }
        p = b.payments.get(row_id)
        if p is not None:
            if p.get("reason"):
                row["check_reason"] = p["reason"]
            if p.get("excluded"):
                row["check_excluded"] = len(p["excluded"])
            if p.get("not_checked"):
                row["check_not_checked"] = len(p["not_checked"])
        v["rows"].append(row)
        v["disputes"] += _disputes_of(sbooks, book_of_role, row_id, effective)
    return v


def _status_start(b: _Books) -> dict:
    try:
        inp = _start_input(StartBooks(b.own[0].record.block["at"], b.offer,
                                      [BookRecord(r.record, r.record_id) for r in b.own],
                                      [BookRecord(r.record, r.record_id) for r in b.counterpart]))
    except ValueError as exc:
        raise StatusError(str(exc)) from None
    out = {"side": inp.side, "counterparty_layer": inp.counterparty_layer, "agree_by": inp.agree_by,
           "counterpart_agreement_held": inp.counterpart_agreement_held,
           "same_offer_and_rows": inp.same_offer_and_rows, "offerer_names_acceptor": inp.offerer_names_acceptor,
           "offerer_agreement_time": None, "acceptor_names_offerer": inp.acceptor_names_offerer,
           "cancellation_named_first": inp.cancellation_named_first, "first_act": None}
    if not inp.counterparty_layer:
        for r in b.own:
            if r.record.record_type in (TYPE_PERFORM, TYPE_SETTLE):
                out["first_act"] = {"time": r.time}
                break
    if inp.side == SIDE_OFFERER:
        out["offerer_agreement_time"] = b.own[0].time
    elif b.counterpart:
        out["offerer_agreement_time"] = b.counterpart[0].time
    return out


def _named_by_other(books: dict[str, list[TimedRecord]], ref: tuple[str, int]) -> Any:
    book, i = ref
    mine = books[book]
    other = books[_OWN] if book == _COUNTERPART else books[_COUNTERPART]
    at = {r.record_id: j for j, r in enumerate(mine)}
    for r in other:
        s = r.record.block["seen"]
        if isinstance(s, str) and s in at and at[s] >= i:
            return r.time
    return None


def _effective_ruling(b: _Books, row: str, conflicting: bool) -> dict | None:
    if conflicting:
        return None
    for r in b.case_book:
        rb = r.record.body
        if r.record.record_type == TYPE_RULING and rb["row"] == row and rb["final"] \
                and r.record.block["deal_id"] == b.offer.block["deal_id"]:
            return rb
    return None


def _first_remedy(records: list[Record], row: str) -> int:
    """The index of the first record that is a settle or perform whose
    remedy_for is the row, -1 for none (Go ``firstRemedy``)."""
    for i, r in enumerate(records):
        if r.record_type in (TYPE_PERFORM, TYPE_SETTLE) and r.body["remedy_for"] is not None \
                and r.body["remedy_for"]["row"] == row:
            return i
    return -1


def _remedy_made(book: list[TimedRecord], row: str) -> Any:
    i = _first_remedy([r.record for r in book], row)
    return book[i].time if i >= 0 else None


def _disputes_of(books: dict[str, list[TimedRecord]], book_of_role: dict[str, str], row: str,
                 effective: dict | None) -> list[dict]:
    out = []
    for side in (ROLE_BUYER, ROLE_SELLER):
        book = books.get(book_of_role.get(side), [])
        for i, r in enumerate(book):
            d = r.record.body
            if r.record.record_type != TYPE_DISPUTE or d["kind"] != KIND_DISPUTE or d["row"] != row:
                continue
            withdrawn = any(w.record.record_type == TYPE_DISPUTE and w.record.body["kind"] == KIND_WITHDRAWAL
                            and w.record.body["row"] == row for w in book[i + 1:])
            against = effective["against"] if effective is not None else None
            if withdrawn:
                outcome = OUTCOME_WITHDRAWN
            elif effective is not None and not isinstance(against, str):
                outcome = OUTCOME_NO_FINDING
            elif effective is not None and against == side:
                outcome = OUTCOME_AGAINST_CLAIMANT
            elif effective is not None:
                outcome = OUTCOME_FOR_CLAIMANT
            else:
                outcome = OUTCOME_OPEN
            out.append({"row": row, "claimant": side, "outcome": outcome, "forum": d["forum"]})
    return out


def resolve_row(row: dict, at: str, stamps: dict[int, str]) -> dict:
    """A status row's input as a reader computes it at ``at``, once each
    ``{"stamp": n}`` it can resolve has a time (Go ``ResolveRow``): every
    time resolved, and each dispute's seen_in_time recomputed from those
    times; while the window end is not known the fact's value stands."""
    def r(t: Any) -> str | None:
        return resolve_time(status_time(t), stamps)

    rin = row["input"]
    out: dict[str, Any] = {"at": at, "parties": rin["parties"], "row": rin["row"]}
    if rin.get("check"):
        out["check"] = rin["check"]
    if rin.get("conflicting_final_rulings"):
        out["conflicting_final_rulings"] = True
    if rin.get("named") is not None:
        out["named"] = {k: {"time": r(n["time"]), "latest": n["latest"]} for k, n in rin["named"].items()}
    if rin.get("statements"):
        out["statements"] = [{"side": s["side"], "value": s["value"], "time": r(s["time"])} for s in rin["statements"]]
    if rin.get("withdrawals"):
        out["withdrawals"] = [{"side": w["side"], "time": r(w["time"])} for w in rin["withdrawals"]]
    if rin.get("rulings"):
        out["rulings"] = []
        for ru in rin["rulings"]:
            v = {"final": ru["final"], "value": ru["value"], "time": r(ru["time"])}
            if ru.get("remedy_due") is not None:
                v["remedy_due"] = ru["remedy_due"]
            out["rulings"].append(v)
    if rin.get("remedies"):
        out["remedies"] = [{"time": r(m["time"])} for m in rin["remedies"]]
    if not rin.get("disputes"):
        return out
    plain = {k: v for k, v in out.items() if k not in ("withdrawals", "rulings", "remedies")}
    end = _compute(plain).window_end
    named_by_other = row.get("statement_named_by_other") or []
    disputes = []
    for d in rin["disputes"]:
        seen = bool(d["seen_in_time"])
        if end != "":
            seen = False
            standing, floor = -1, None
            for i, s in enumerate(out.get("statements", [])):
                if s["side"] == d["side"]:
                    continue
                t = s["time"]
                late = (t is not None and t > end) or (t is None and floor is not None and floor > end)
                if not late:
                    standing = i
                if t is not None and (floor is None or t > floor):
                    floor = t
            if 0 <= standing < len(named_by_other):
                t = r(named_by_other[standing])
                if t is not None and t <= end:
                    seen = True
        disputes.append({"side": d["side"], "time": r(d["time"]), "seen_in_time": seen})
    out["disputes"] = disputes
    return out

