# SPDX-License-Identifier: Apache-2.0
"""The status of one term row of a deal (evidencebook#461, acceptance 6).

Computed from the two sides' statements on the row and the dates; no record
holds it and nobody declares it. This is Go's ``rowstatus`` package and the
Rust crate's ``row_status`` module, result for result, over the shared
vectors in ``testdata/row-status``; the Go package comment states the rule.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Optional

__all__ = ["Invalid", "Outcome", "compute", "parse_input"]


class Invalid(ValueError):
    """Inputs the rule cannot be applied to."""


_TIME = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z")
_MAX_OFFSET = 100 * 366 * 24 * 3600
_MET, _NOT_MET, _CANNOT_TELL = "met", "not_met", "cannot_tell"


def _time(s: Any) -> datetime:
    if not isinstance(s, str) or not _TIME.fullmatch(s):
        raise Invalid(f"time {s!r} is not YYYY-MM-DDTHH:MM:SSZ")
    try:
        t = datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
    except ValueError as e:
        raise Invalid(f"time {s!r}: {e}") from None
    if t.second == 60:  # strptime accepts a leap second the others refuse
        raise Invalid(f"time {s!r} is not a valid time")
    return t


def _fmt(t: Optional[datetime]) -> str:
    return "" if t is None else t.strftime("%Y-%m-%dT%H:%M:%SZ")


# The members each object may hold, and their JSON types. Go, Rust and
# Python refuse the same inputs: unknown, differently cased or repeated
# members, and members of the wrong type.
_STR, _BOOL, _INT, _OPT_STR = "str", "bool", "int", "opt_str"
_DATE = {"at": _OPT_STR, "after": _STR, "seconds": _INT}
_SHAPES: dict[str, dict[str, Any]] = {
    "input": {
        "at": _STR, "parties": ("list", _STR), "row": "row", "named": ("map", "named"),
        "no_concession": _BOOL, "statements": ("list", "statement"), "check": _STR,
        "disputes": ("list", "dispute"), "withdrawals": ("list", "withdrawal"),
        "rulings": ("list", "ruling"), "conflicting_final_rulings": _BOOL,
        "remedies": ("list", "remedy"),
    },
    "row": {"id": _STR, "owed_by": _STR, "due": "date", "window_end": "date", "recomputed": _BOOL},
    "date": _DATE,
    "named": {"time": _OPT_STR, "latest": _STR},
    "statement": {"side": _STR, "value": _STR, "time": _OPT_STR},
    "dispute": {"side": _STR, "time": _OPT_STR, "seen_in_time": _BOOL},
    "withdrawal": {"side": _STR, "time": _OPT_STR},
    "ruling": {"final": _BOOL, "value": _STR, "time": _OPT_STR, "remedy_due": _OPT_STR},
    "remedy": {"time": _OPT_STR},
}


def _check(v: Any, shape: Any, where: str) -> None:
    if v is None and shape != _OPT_STR:
        raise Invalid(f"{where} is null where a value is required")
    if isinstance(shape, tuple):
        kind, inner = shape
        if kind == "list":
            if not isinstance(v, list):
                raise Invalid(f"{where} is not an array")
            for i, x in enumerate(v):
                _check(x, inner, f"{where}[{i}]")
        else:
            if not isinstance(v, dict):
                raise Invalid(f"{where} is not an object")
            for k, x in v.items():
                _check(x, inner, f"{where}.{k}")
        return
    if isinstance(v, str):
        try:
            v.encode("utf-8")
        except UnicodeEncodeError:
            raise Invalid(f"{where} holds an unpaired surrogate") from None
    if shape == _STR:
        if not isinstance(v, str):
            raise Invalid(f"{where} is not a string")
    elif shape == _OPT_STR:
        if v is not None and not isinstance(v, str):
            raise Invalid(f"{where} is not a string or null")
    elif shape == _BOOL:
        if not isinstance(v, bool):
            raise Invalid(f"{where} is not a boolean")
    elif shape == _INT:
        if isinstance(v, bool) or not isinstance(v, int):
            raise Invalid(f"{where} is not an integer")
    else:
        if not isinstance(v, dict):
            raise Invalid(f"{where} is not an object")
        members = _SHAPES[shape]
        for k, x in v.items():
            if k not in members:
                raise Invalid(f"{where}: unknown member {k!r}")
            _check(x, members[k], f"{where}.{k}")


def _no_repeats(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for k, v in pairs:
        try:
            k.encode("utf-8")
        except UnicodeEncodeError:
            raise Invalid("a member name holds an unpaired surrogate") from None
        if k in out:
            raise Invalid(f"member {k!r} repeated")
        out[k] = v
    return out


def parse_input(data: bytes | str) -> dict[str, Any]:
    """Reads one input as the vectors write it: UTF-8 only, as Go and Rust read it."""
    if isinstance(data, (bytes, bytearray)):
        try:
            data = bytes(data).decode("utf-8")
        except UnicodeDecodeError:
            raise Invalid("input is not UTF-8") from None
    try:
        v = json.loads(data, object_pairs_hook=_no_repeats)
    except json.JSONDecodeError as e:
        raise Invalid(str(e)) from None
    if not isinstance(v, dict):
        raise Invalid("an input is an object")
    _check(v, "input", "input")
    if not isinstance(v.get("row"), dict):
        raise Invalid("input.row is required")
    return v


@dataclass(frozen=True)
class Outcome:
    """A row's status and the value stated, read together."""

    status: str
    value: str = ""
    remedy: str = ""
    local: bool = False
    due: str = ""
    window_end: str = ""

    def to_json(self) -> dict[str, Any]:
        out: dict[str, Any] = {"status": self.status, "local": self.local}
        for k in ("value", "remedy", "due", "window_end"):
            if getattr(self, k):
                out[k] = getattr(self, k)
        return out


@dataclass
class _Stmt:
    side: int
    value: str
    t: Optional[datetime]
    floor: Optional[datetime]
    #: The statement's index in the input's statements.
    idx: int = -1


@dataclass
class _Calc:
    inp: dict[str, Any]
    at: datetime
    parties: list[str]
    due: Optional[datetime] = None
    end: Optional[datetime] = None
    unset: bool = False
    reached: bool = True
    local: bool = False
    stmts: list[_Stmt] = field(default_factory=list)
    disputes: list[tuple[int, Optional[datetime], bool]] = field(default_factory=list)
    #: Each held dispute's index in the input's disputes.
    d_idx: list[int] = field(default_factory=list)
    rulings: list[tuple[dict[str, Any], Optional[datetime]]] = field(default_factory=list)
    remedies: list[Optional[datetime]] = field(default_factory=list)
    withdrawn: list[bool] = field(default_factory=lambda: [False, False])
    w_timed: list[bool] = field(default_factory=lambda: [False, False])

    def side(self, s: Any) -> int:
        if s == self.parties[0]:
            return 0
        if s == self.parties[1]:
            return 1
        raise Invalid(f"{s!r} is not a party")

    def held(self, s: Optional[str]) -> tuple[bool, Optional[datetime]]:
        """(exists as of T, its time); a record with no time exists."""
        if s is None:
            return True, None
        t = _time(s)
        if t > self.at:
            return False, None
        return True, t

    def date(self, d: Optional[dict[str, Any]]) -> Optional[datetime]:
        d = d or {}
        at, after, seconds = d.get("at"), d.get("after") or "", d.get("seconds") or 0
        if at is not None and after == "" and seconds == 0:
            return _time(at)
        if not (at is None and after != ""):
            raise Invalid("a date is either at or after")
        if seconds < 0 or seconds > _MAX_OFFSET:
            raise Invalid("a date's offset is out of range")
        named = self.inp.get("named") or {}
        if after not in named:
            raise Invalid(f"the date hangs on {after!r}, which is not named")
        n = named[after] or {}
        latest = _time(n.get("latest") or "")
        t = None
        if n.get("time") is not None:
            v = _time(n["time"])
            if v <= latest and v <= self.at:
                t = v
        if t is None:
            if self.at > latest:
                self.reached = False
            self.unset = True
            return None
        try:
            return t + timedelta(seconds=seconds)
        except OverflowError:
            raise Invalid("a date past year 9999") from None

    def use(self, t: Optional[datetime]) -> None:
        if t is None:
            self.local = True

    def late(self, s: _Stmt) -> bool:
        assert self.end is not None
        if s.t is not None:
            return s.t > self.end
        return s.floor is not None and s.floor > self.end

    def values(self) -> list[str]:
        idx = [-1, -1]
        for i, s in enumerate(self.stmts):
            if not self.late(s):
                idx[s.side] = i
        v = ["", ""]
        for side, i in enumerate(idx):
            if i < 0:
                continue
            self.use(self.stmts[i].t)
            if self.stmts[i].value != _CANNOT_TELL:
                v[side] = self.stmts[i].value
        return v

    def concede(self, v: list[str]) -> list[str]:
        assert self.end is not None
        v = list(v)
        if self.inp.get("no_concession") or not self.at > self.end:
            return v
        first = [-1, -1]
        for i, s in enumerate(self.stmts):
            other = v[1 - s.side]
            if first[s.side] >= 0 or s.t is None or not self.late(s) or other == "":
                continue
            if s.value == other and v[s.side] != other:
                first[s.side] = i
        a, b = first
        if a < 0 and b < 0:
            return v
        if a >= 0 and b >= 0:
            ta, tb = self.stmts[a].t, self.stmts[b].t
            if ta == tb:
                return v
            if tb < ta:
                a = -1
            else:
                b = -1
        s = self.stmts[a if a >= 0 else b]
        v[s.side] = s.value
        return v

    def dispute_late(self, t: Optional[datetime], seen: bool, in_time: list[str], failed: bool) -> bool:
        assert self.end is not None
        if t is None or not t > self.end or failed or not seen:
            return False
        a, b = in_time
        return (a != "" and b != "" and a == b) or ((a == "") != (b == ""))

    def remedy(self, r: dict[str, Any]) -> str:
        if r.get("remedy_due") is None:
            return "none"
        due = _time(r["remedy_due"])
        for t in self.remedies:
            if t is None:
                self.local = True
                return "made"
            if t <= due:
                return "made"
        return "not_honoured" if self.at > due else "due"

    def compute(self) -> Outcome:
        due, end = _fmt(self.due), _fmt(self.end)

        def finish(status: str, value: str = "", remedy: str = "") -> Outcome:
            return Outcome(status, value, remedy, self.local, due, end)

        if not self.reached:
            return finish("not_reached")
        conflicting = bool(self.inp.get("conflicting_final_rulings"))
        if not conflicting:
            for r, t in self.rulings:
                if r.get("final"):
                    self.use(t)
                    rem = self.remedy(r)
                    return finish("ruled", r["value"], rem)
        if self.unset:
            return finish("not_due")
        assert self.due is not None and self.end is not None
        in_time = self.values()
        stated = self.concede(in_time)
        failed = self.inp.get("check") == "fail"
        after = self.at > self.end
        if conflicting:
            return finish("in_dispute")
        # A withdrawal with no time is relied on only when no other dispute
        # counts: it is what keeps the row out of dispute.
        rests_on_untimed_withdrawal = False
        for side, t, seen in self.disputes:
            if self.dispute_late(t, seen, in_time, failed):
                continue
            if self.withdrawn[side]:
                rests_on_untimed_withdrawal = rests_on_untimed_withdrawal or not self.w_timed[side]
                continue
            self.use(t)
            return finish("in_dispute")
        if rests_on_untimed_withdrawal:
            self.local = True
        a, b = stated
        if failed or (a and b and a != b):
            return finish("in_disagreement")
        if a and b:
            return finish("agreed" if after else "agreed_so_far", a)
        if a or b:
            return finish("uncontested" if after else "waiting", a + b)
        if self.at > self.due:
            return finish("no_statement")
        return finish("not_due")


def compute(inp: dict[str, Any]) -> Outcome:
    """Applies the rule to one row as of ``inp["at"]``."""
    return _new_calc(inp).compute()


def compute_late(inp: dict[str, Any]) -> dict[str, list[int]]:
    """What the rule set aside as late on a row as of ``inp["at"]`` (Go
    ``ComputeLate``): indices into the input's statements and disputes, in
    input order, as ``{"late_statements": [...], "late_disputes": [...]}``.
    Both are empty while the row is not reached or either of its dates is
    unset, since lateness is read against the contest window's end. A late
    statement is one whose time is after the window's end, or an untimed one
    after a timed statement of its side that already is; a late dispute is
    one rule 2 does not count because it is late."""
    c = _new_calc(inp)
    out: dict[str, list[int]] = {"late_statements": [], "late_disputes": []}
    if not c.reached or c.unset:
        return out
    out["late_statements"] = [s.idx for s in c.stmts if c.late(s)]
    in_time = c.values()
    failed = c.inp.get("check") == "fail"
    for (side, t, seen), i in zip(c.disputes, c.d_idx):
        if c.dispute_late(t, seen, in_time, failed):
            out["late_disputes"].append(i)
    return out


def _new_calc(inp: dict[str, Any]) -> _Calc:
    at = _time(inp.get("at") or "")
    parties = inp.get("parties") or []
    if len(parties) != 2 or not parties[0] or not parties[1] or parties[0] == parties[1]:
        raise Invalid("two distinct parties are required")
    row = inp.get("row")
    if not isinstance(row, dict):
        raise Invalid("input.row is required")
    c = _Calc(inp, at, parties)
    c.side(row.get("owed_by") or "")
    if (inp.get("check") or "") not in ("", "pass", "fail"):
        raise Invalid("check is pass, fail or empty")
    if inp.get("check") and not row.get("recomputed"):
        raise Invalid("a check result on a row its terms do not mark recomputed")
    c.due = c.date(row.get("due"))
    c.end = c.date(row.get("window_end"))
    if c.due is not None and c.end is not None and c.end < c.due:
        raise Invalid("the contest window ends before the due date")
    floors: list[Optional[datetime]] = [None, None]
    for si, s in enumerate(inp.get("statements") or []):
        s = s or {}
        i = c.side(s.get("side") or "")
        if s.get("value") not in (_MET, _NOT_MET, _CANNOT_TELL):
            raise Invalid(f"statement value {s.get('value')!r}")
        t = None if s.get("time") is None else _time(s["time"])
        floor = floors[i]
        if t is not None and (floors[i] is None or t > floors[i]):
            floors[i] = t
        if (t is not None and t > at) or (t is None and floor is not None and floor > at):
            continue
        c.stmts.append(_Stmt(i, s["value"], t, floor, si))
    for w in inp.get("withdrawals") or []:
        w = w or {}
        i = c.side(w.get("side") or "")
        ok, t = c.held(w.get("time"))
        if ok:
            c.withdrawn[i] = True
            c.w_timed[i] = c.w_timed[i] or t is not None
    for di, d in enumerate(inp.get("disputes") or []):
        d = d or {}
        i = c.side(d.get("side") or "")
        ok, t = c.held(d.get("time"))
        if ok:
            c.disputes.append((i, t, bool(d.get("seen_in_time"))))
            c.d_idx.append(di)
    for r in inp.get("rulings") or []:
        r = r or {}
        if r.get("value") not in (_MET, _NOT_MET):
            raise Invalid("a ruling's value is met or not_met")
        if r.get("remedy_due") is not None:
            _time(r["remedy_due"])
        ok, t = c.held(r.get("time"))
        if ok:
            c.rulings.append((r, t))
    for r in inp.get("remedies") or []:
        ok, t = c.held((r or {}).get("time"))
        if ok:
            c.remedies.append(t)
    return c
