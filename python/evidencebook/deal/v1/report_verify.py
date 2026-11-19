# SPDX-License-Identifier: Apache-2.0
"""A sealed v1 report read against its deal's first record, as Go's
``VerifyReport`` (``dealprofile/xdealv1/report_verify.go``; design note §9,
"Verifying a report"), held to Go by ``testdata/deal-v1/report-verify`` and
the x-deal-v1 differential (``test/interop/deal-v1``).

Go reads the payload strictly into its ``Report`` type and refuses text
that type would not write back. :func:`_exact` is that test, against the
type as ``TestTypesTable`` writes it (``gotypes.table()["report"]``):

- every member is named exactly as its field;
- a member its field leaves out when empty (``omitempty``) or zero
  (``omitzero``) is absent rather than empty or zero, and every other
  member is present;
- a null stands only where Go keeps one: a pointer, a list, a map or a raw
  member;
- each value is of its field's kind, an integer in its range.

Go reads a member in another case and then refuses the text when it writes
it back under the field's name; it is refused here at once, with the same
outcome.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any

from agent_action_capsule.canonical import jcs

from ..check import StageError
from ..names import REPORT_ACTION_ID, REPORT_TYPE
from . import gotypes, rawjson
from .check import _read_text
from .names import KIND_AGREEMENT, PROFILE, ROLE_BUYER, ROLE_SELLER, TYPE_AGREE
from .sent import _agent_input_digest, _err, _pair_members, _verify_capsule, pair, verify_book

__all__ = ["STAGE_REPORT", "ReportRead", "report_pair", "verify_report"]

#: A sealed report read against its deal's first record (Go ``StageReport``).
STAGE_REPORT = "report"

_RANGE = {"int64": (-(1 << 63), (1 << 63) - 1), "uint64": (0, (1 << 64) - 1)}
_PAIR = "report pair {record, agent_input}"


def report_pair(capsule: bytes, envelope: bytes, payload: bytes) -> bytes:
    """A sealed report as it travels (Go ``ReportPair``): the form of a sent
    pair, the payload as its agent_input."""
    return pair(capsule, envelope, payload)


@dataclass(frozen=True)
class ReportRead:
    """A verified report (Go ``ReportRead``): the report payload as a JSON
    value, its Capsule id and the key that signed it."""

    report: dict
    capsule_id: str
    key_id: str


def verify_report(report: bytes, first: bytes) -> ReportRead:
    """Checks a sealed v1 report against its deal's first record (Go
    ``VerifyReport``): ``first`` is the book pair of the record at seq 1 of
    the holder's deal book. Raises :class:`StageError` where Go refuses."""
    report = bytes(report)
    p = _pair_members(report, STAGE_REPORT, ("record", "agent_input"), _PAIR)
    try:
        top = rawjson.unmarshal_map(report)
    except rawjson.GoJSONError:
        top = None
    if top is None or len(top) != 2 or "record" not in top or "agent_input" not in top:
        raise _err(STAGE_REPORT, f"not a {_PAIR}: its members are not exactly record and agent_input")
    capsule, capsule_id, key_id = _verify_capsule(STAGE_REPORT, p["record"])
    c = json.loads(capsule)
    action_id, action_type = c.get("action_id"), c.get("action_type")
    if action_id != REPORT_ACTION_ID or action_type != "fyi":
        raise _err(STAGE_REPORT, f"not a report Capsule: action_id {action_id!r}, action_type {action_type!r}")
    try:
        doc = _read_text(rawjson._b(p["agent_input"]))
        payload = jcs(doc)
    except StageError as exc:
        raise _err(STAGE_REPORT, f"the payload: {exc.detail}") from None
    except Exception as exc:  # noqa: BLE001 - JCS refuses it
        raise _err(STAGE_REPORT, f"the payload: {exc}") from None
    if _agent_input_digest(capsule) != hashlib.sha256(payload).hexdigest():
        raise _err(STAGE_REPORT, "the payload is not the one the Capsule commits (agent_input_digest)")
    try:
        _exact(doc, gotypes.table()["report"], "")
        _nulls(doc)
    except _NotAReport as exc:
        raise _err(STAGE_REPORT, f"the payload does not read as a report: {exc}") from None
    if doc["type"] != REPORT_TYPE or doc["profile"] != PROFILE:
        raise _err(STAGE_REPORT, f"not a deal_report of {PROFILE}")
    for row in doc["rows"]:
        against = (row["ruling"] or {}).get("against")
        if against is not None and against not in (ROLE_BUYER, ROLE_SELLER):
            raise _err(STAGE_REPORT, f"row {row['id']!r}: a ruling is against buyer, seller or null, not {against!r}")
    b = verify_book(first)
    r = b.record
    if r.record_type != TYPE_AGREE or r.body["kind"] != KIND_AGREEMENT or b.seq != 1:
        raise _err(STAGE_REPORT, "the first record is not the agreement at seq 1 of a deal book")
    if r.block["deal_id"] != doc["deal_id"] or b.log_id != doc["rests_on"]["own"]["log_id"]:
        raise _err(STAGE_REPORT, "the first record is not of the book the report rests on")
    if key_id != b.key_id:
        raise _err(STAGE_REPORT, "the report is signed by another key than its deal's first record")
    return ReportRead(doc, capsule_id, key_id)


class _NotAReport(ValueError):
    pass


def _int(t: Any) -> bool:
    return isinstance(t, str) and t in _RANGE


def _fields(t: dict) -> list[dict]:
    return gotypes.table()["structs"][t["struct"]]


def _empty(v: Any, t: Any) -> bool:
    """Go's omitempty: false, 0, "", and a nil or empty pointer, list or map."""
    if v is None:
        return True
    if t in ("string", "bool") or _int(t):
        return not v
    if isinstance(t, dict) and ("slice" in t or "map" in t):
        return len(v) == 0
    return False


def _zero(v: Any, t: Any) -> bool:
    """Go's omitzero: the type's zero value (a nil list, not an empty one)."""
    if v is None:
        return True
    if t in ("string", "bool") or _int(t):
        return not v
    if isinstance(t, dict) and "struct" in t:
        return isinstance(v, dict) and all(f["name"] in v and _zero(v[f["name"]], f["type"]) for f in _fields(t))
    return False


def _exact(v: Any, t: Any, at: str) -> None:
    """Refuses ``v`` unless Go reads it as type ``t`` and writes it back as
    it is."""
    where = at or "the payload"
    if t == "raw":
        return
    if isinstance(t, dict) and ("ptr" in t or "slice" in t or "map" in t) and v is None:
        return
    if t == "string":
        if not isinstance(v, str):
            raise _NotAReport(f"{where} is not a string")
        return
    if t == "bool":
        if not isinstance(v, bool):
            raise _NotAReport(f"{where} is not a bool")
        return
    if _int(t):
        lo, hi = _RANGE[t]
        if isinstance(v, bool) or not isinstance(v, int) or not lo <= v <= hi:
            raise _NotAReport(f"{where} is not a Go {t}")
        return
    if "ptr" in t:
        _exact(v, t["ptr"], at)
        return
    if "slice" in t:
        if not isinstance(v, list):
            raise _NotAReport(f"{where} is not a list")
        for i, e in enumerate(v):
            _exact(e, t["slice"], f"{at}[{i}]")
        return
    if "map" in t:
        if not isinstance(v, dict):
            raise _NotAReport(f"{where} is not an object")
        for k, e in v.items():
            _exact(e, t["map"], f"{at}.{k}")
        return
    # A struct: a null leaves it zero, which writes back as a whole object.
    if not isinstance(v, dict):
        raise _NotAReport(f"{where} is not an object")
    fields = {f["name"]: f for f in _fields(t)}
    for k in v:
        if k not in fields:
            raise _NotAReport(f"json: unknown field {json.dumps(k)}")
    for name, f in fields.items():
        path = f"{at}.{name}" if at else name
        if name not in v:
            if not (f.get("omitempty") or f.get("omitzero")):
                raise _NotAReport(f"{path} is missing")
            continue
        # Its kind first: the empty and zero tests read a value of it.
        _exact(v[name], f["type"], path)
        if f.get("omitempty") and _empty(v[name], f["type"]) or f.get("omitzero") and _zero(v[name], f["type"]):
            raise _NotAReport(f"{path} is given empty, which the report leaves out")


def _nulls(doc: dict) -> None:
    """Go's ``reportNulls``: what the round trip keeps but ``BuildReport``
    never writes."""
    if doc["rows"] is None:
        raise _NotAReport("rows is null")
    if doc["report"] is None:
        raise _NotAReport("report is null")
    if doc.get("rulings_unnamed", 0) < 0:
        raise _NotAReport("rulings_unnamed is negative")
    for row in doc["rows"]:
        if row["relied_on"] is None:
            raise _NotAReport(f"row {row['id']!r}: relied_on is null")
        if row["late"]["statements"] is None or row["late"]["disputes"] is None:
            raise _NotAReport(f"row {row['id']!r}: a late list is null")
        if not isinstance(row["owed"], dict):
            raise _NotAReport(f"row {row['id']!r}: owed is not an object")
        for src in row["relied_on"]:
            if src["witnessed"] != (src["time"] is not None):
                raise _NotAReport(
                    f"row {row['id']!r}: relied_on {src['record']!r} is witnessed exactly when it has a time")
