# SPDX-License-Identifier: Apache-2.0
"""A copy of the v1 report (design §9), as Go's ``dealprofile/xdealv1``
``report.go`` builds it from books: start and each row as the start rule and
rowstatus give them, settled, and the members the copy's audience may have.

Reading the books from a shelf (Go ``StatusProvider.Report``) is the
shelf's part. Books and the report are JSON values in the shape Go writes
(the vectors under ``testdata/deal-v1/report``). A refusal raises
:class:`.status.StatusError`.
"""
from __future__ import annotations

import re
from typing import Any

from agent_action_capsule.canonical import jcs

from ... import row_status
from ..names import AUDIENCE_ADJUDICATOR, AUDIENCE_COUNTERPARTY, AUDIENCE_KEEP, REPORT_TYPE
from ..report import normalize_referee
from .check import utc_second
from . import golenient
from .gotypes import NIL_RAW
from .names import PROFILE, ROLE_BUYER, ROLE_SELLER, TYPE_PERFORM, TYPE_SETTLE, VALUE_NOT_MET
from .start import STATE_STARTED, FirstAct, StartInput, start
from .status import (
    StatusError, TimedRecord, _Books, _compute, _first_remedy, _read_books, _row_inputs, _Sources, build_status,
    resolve_time,
)

__all__ = [
    "ROWS_LISTED", "ROWS_NOT_STARTED", "ROWS_WITHHELD", "PROOF_HELD", "PROOF_NOT_YET", "PROOF_NO_LAYER",
    "REMEDY_NOT_COUNTED", "CHECK_PASSED", "CHECK_FAILED", "CHECK_NOT_RUN", "CHECK_NOT_RECOMPUTED", "NOT_SET",
    "SIDE_ADJUDICATOR", "build_report", "report_payload", "settled",
]

ROWS_LISTED = "listed"
ROWS_NOT_STARTED = "not_started"
ROWS_WITHHELD = "withheld"

PROOF_HELD = "held"
PROOF_NOT_YET = "not_yet"
PROOF_NO_LAYER = "no_layer"

REMEDY_NOT_COUNTED = "not_counted"

CHECK_PASSED = "passed"
CHECK_FAILED = "failed"
CHECK_NOT_RUN = "not_run"
CHECK_NOT_RECOMPUTED = "not_recomputed"

NOT_SET = "not_set"

#: A case book record's side in relied_on.
SIDE_ADJUDICATOR = "adjudicator"

_NONCE = re.compile(r"[0-9a-f]{64}\Z")


def _bad(detail: str) -> StatusError:
    return StatusError("xdealv1: report: " + detail)


def _stamps(v: Any) -> dict[int, str]:
    """``Stamps`` as Go reads a ``map[uint64]string``: each key a decimal
    uint64, a null value the empty string."""
    out: dict[int, str] = {}
    for k, t in (v or {}).items():
        if not re.fullmatch(r"[0-9]+", k) or int(k) >= 1 << 64:
            raise StatusError(f"xdealv1: report: stamp key {_q(k)} is not a uint64")
        out[int(k)] = t
    return out


_LOG = golenient.struct(log_id="str", size="uint", last="str")
_COUNTERPART_LOG = golenient.struct(log_id="str", counted="uint", last="str", fork_at="uint")
_CHECKPOINT = golenient.struct(log_id="str", mmr_size="uint", root="str")
_FACT = golenient.struct(current="bool")
#: Go's ReportBooks: its fields carry no JSON tags, so each matches its Go
#: name in any case.
_REPORT_BOOKS = golenient.struct(
    Books="raw", Stamps=("map", "str"), Own=_LOG, Counterpart=("ptr", _COUNTERPART_LOG), Checkpoint=_CHECKPOINT,
    Fact=("ptr", _FACT))
_COPY = golenient.struct(audience="str", nonce="str", referee="str", as_of="str",
                         text=golenient.custom(lambda v: _text(v)))


def _text(v: Any) -> Any:
    if v is not None and not isinstance(v, dict):
        raise StatusError("xdealv1: report: a copy's text is an object")
    return v


def build_report(books: dict, copy: dict) -> dict:
    """One copy of the v1 report from ``books`` at ``copy["as_of"]`` (design
    §9; Go ``BuildReport``). ``books`` holds ``Books`` (status books),
    ``Stamps``, ``Own``, ``Counterpart``, ``Checkpoint`` and ``Fact``;
    ``copy`` holds ``audience``, ``nonce``, ``referee``, ``as_of`` and,
    optionally, ``text``. It refuses what :func:`.status.build_status`
    refuses, an unknown audience, a nonce that is not 64 lowercase hex, a
    referee on a copy that is not an adjudicator's or none on one that is,
    an as_of that is not RFC 3339 UTC to the second, and books holding a
    record timed after as_of."""
    try:
        rbooks = golenient.read(books, _REPORT_BOOKS)
        c = golenient.read(copy, _COPY)
    except golenient.GoDecodeError as exc:
        raise StatusError(str(exc)) from None
    audience, nonce, as_of = c["audience"], c["nonce"], c["as_of"]
    if audience not in (AUDIENCE_KEEP, AUDIENCE_COUNTERPARTY, AUDIENCE_ADJUDICATOR):
        raise _bad(f"audience {_q(audience)} is not a copy audience")
    if not _NONCE.match(nonce):
        raise _bad("the nonce must be 64 lowercase hex")
    referee = normalize_referee(c["referee"])
    if audience == AUDIENCE_ADJUDICATOR and referee == "":
        raise _bad("an adjudicator's copy names the referee it is handed to")
    if audience != AUDIENCE_ADJUDICATOR and referee != "":
        raise _bad("only an adjudicator's copy names a referee")
    if utc_second(as_of) is None:
        raise _bad(f"as_of {_q(as_of)} is not RFC 3339 UTC to the second")
    sb = _read_books(rbooks["Books"])
    v = build_status(sb)
    stamps = _stamps(rbooks["Stamps"])
    own = sb.own[0].record.body

    def resolve(rs: list[TimedRecord]) -> list[TimedRecord]:
        return [TimedRecord(r.record, r.record_id, resolve_time(r.time, stamps)) for r in rs]

    rb = _Books(sb.offer, resolve(sb.own), resolve(sb.counterpart), resolve(sb.case_book), sb.conflicting,
                sb.payments, sb.checks)
    for rs in (rb.own, rb.counterpart, rb.case_book):
        for r in rs:
            if r.time is not None and r.time > as_of:
                raise _bad(f"record {r.record_id} is timed {r.time}, after as_of {as_of}")
    ins, srcs = _row_inputs(rb, as_of)
    st = v["start"]
    try:
        started = start(StartInput(
            at=as_of, side=st["side"], counterparty_layer=st["counterparty_layer"], agree_by=st["agree_by"],
            counterpart_agreement_held=st["counterpart_agreement_held"],
            same_offer_and_rows=st["same_offer_and_rows"], offerer_names_acceptor=st["offerer_names_acceptor"],
            offerer_agreement_time=resolve_time(st["offerer_agreement_time"], stamps),
            acceptor_names_offerer=st["acceptor_names_offerer"],
            cancellation_named_first=st["cancellation_named_first"],
            first_act=_first_act(rb, st["counterparty_layer"])))
    except ValueError as exc:
        raise StatusError(str(exc)) from None
    text = c["text"]
    r: dict[str, Any] = {
        "type": REPORT_TYPE, "profile": PROFILE, "deal_id": sb.offer.block["deal_id"], "audience": audience,
        "nonce": nonce, "as_of": as_of, "deal_checkpoint": rbooks["Checkpoint"],
        "pack_digest": sb.offer.body["pack_digest"], "start": started.value(), "settled": False, "rows_state": "",
        "rows": [],
        "rests_on": {"own": rbooks["Own"], "counterparty_layer": v["counterparty_layer"],
                     "counterparty_kind": v["counterparty_kind"], "counterparty_proof": _counterparty_proof(v)},
        "status_fact": rbooks["Fact"], "report": text if text is not None else {},
    }
    if referee:
        r["referee"] = referee
    rows = [_report_row(rb, agreed, inp, src)
            for agreed, inp, src in zip(sb.offer.body["terms_rows"]["rows"] or [], ins, srcs)]
    r["settled"] = settled(r["start"], rows)
    if audience == AUDIENCE_ADJUDICATOR:
        r["rows_state"] = ROWS_WITHHELD
    elif started.state != STATE_STARTED:
        r["rows_state"] = ROWS_NOT_STARTED
    else:
        r["rows_state"] = ROWS_LISTED
        r["rows"] = rows
    if audience != AUDIENCE_ADJUDICATOR:
        if rbooks["Counterpart"] is not None:
            r["rests_on"]["counterparty"] = rbooks["Counterpart"]
        ws = [{"key_id": w["key_id"], **({"url": w["url"]} if w["url"] else {})} for w in own["witnesses"] or ()]
        if ws:
            r["rests_on"]["witnesses"] = ws
        if v["disputes"]:
            r["disputes"] = v["disputes"]
        if v.get("rulings_unnamed"):
            r["rulings_unnamed"] = v["rulings_unnamed"]
    return r


def report_payload(report: dict) -> bytes:
    """The report's payload bytes: its JCS form (Go ``ReportPayload``)."""
    return jcs(report)


def _q(s: str) -> str:
    from ..check import _go_quote

    return _go_quote(s)


def _first_act(b: _Books, layer: bool) -> FirstAct | None:
    if layer:
        return None
    for r in b.own:
        if r.record.record_type in (TYPE_PERFORM, TYPE_SETTLE):
            return FirstAct(r.time)
    return None


def _counterparty_proof(v: dict) -> str:
    if not v["counterparty_layer"]:
        return PROOF_NO_LAYER
    return PROOF_HELD if v["counterparty_proof_held"] else PROOF_NOT_YET


def _report_check(agreed: dict, check: str) -> str:
    if not agreed["recomputed"]:
        return CHECK_NOT_RECOMPUTED
    if check == "pass":
        return CHECK_PASSED
    if check == "fail":
        return CHECK_FAILED
    return CHECK_NOT_RUN


def _report_row(b: _Books, agreed: dict, inp: dict, src: _Sources) -> dict:
    res = _compute(inp)
    try:
        late = row_status.compute_late(inp)
    except row_status.Invalid as exc:
        raise StatusError(f"rowstatus: {exc}") from None
    own_role = b.own[0].record.body["role"]

    def rec(ref: tuple[str, int]) -> tuple[TimedRecord, str]:
        book, i = ref
        if book == "o":
            return b.own[i], own_role
        if book == "c":
            return b.counterpart[i], ROLE_SELLER if own_role == ROLE_BUYER else ROLE_BUYER
        return b.case_book[i], SIDE_ADJUDICATOR

    owed = agreed["owed"]
    row: dict[str, Any] = {
        "id": agreed["id"], "owed_by": agreed["owed_by"], "owed": None if owed is NIL_RAW else owed,
        "due": res.due or NOT_SET, "window_end": res.window_end or NOT_SET, "status": res.status,
        "value": res.value or None, "remedy": res.remedy or None, "remedy_made": False, "local": res.local,
        "check": _report_check(agreed, inp.get("check") or ""),
        "late": {"statements": [], "disputes": []}, "ruling": None, "relied_on": [],
    }
    stmts, disputes = inp.get("statements") or [], inp.get("disputes") or []
    for i in late["late_statements"]:
        r, _ = rec(src.statements[i])
        row["late"]["statements"].append({"side": stmts[i]["side"], "value": stmts[i]["value"],
                                          "record": r.record_id})
    for i in late["late_disputes"]:
        r, _ = rec(src.disputes[i])
        row["late"]["disputes"].append({"side": disputes[i]["side"], "record": r.record_id})
    if res.status == "ruled":
        for k, ru in enumerate(inp.get("rulings") or []):
            if not ru["final"]:
                continue
            r, _ = rec(src.rulings[k])
            body = r.record.body
            against = body["against"]
            row["ruling"] = {"record": r.record_id, "value": ru["value"],
                             "against": against if isinstance(against, str) else None, "final": True,
                             "remedy_due": ru.get("remedy_due")}
            if body["remedy"] is not None and body["remedy"]["amount_minor"] is not None:
                row["remedy"] = REMEDY_NOT_COUNTED
            break
    owing, owing_book = b.counterpart, "c"
    if own_role == agreed["owed_by"]:
        owing, owing_book = b.own, "o"
    remedy_ref = None
    i = _first_remedy([r.record for r in owing], agreed["id"])
    if i >= 0:
        row["remedy_made"] = True
        remedy_ref = (owing_book, i)
    refs = [src.named[n] for n in sorted(src.named)]
    for lst in (src.statements, src.disputes, src.withdrawals, src.rulings, src.remedies):
        refs += lst
    if remedy_ref is not None:
        refs.append(remedy_ref)
    seen: set[tuple[str, int]] = set()
    for ref in refs:
        if ref in seen:
            continue
        seen.add(ref)
        r, side = rec(ref)
        row["relied_on"].append({"record": r.record_id, "side": side, "time": r.time,
                                 "witnessed": r.time is not None})
    return row


def settled(start: Any, rows: list[dict]) -> bool:
    """Design §9's settled: the deal has started, and every row that is not
    not_reached is agreed (met, or not met with a remedy made), uncontested,
    or ruled with remedy none or made (Go ``settled``)."""
    state = start["state"] if isinstance(start, dict) else start.state
    if state != STATE_STARTED:
        return False
    for r in rows:
        status = r["status"]
        if status in ("not_reached", "uncontested"):
            continue
        if status == "agreed":
            if r["value"] == VALUE_NOT_MET and not r["remedy_made"]:
                return False
        elif status == "ruled":
            if r["remedy"] not in ("none", "made"):
                return False
        else:
            return False
    return True
