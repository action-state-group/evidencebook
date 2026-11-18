# SPDX-License-Identifier: Apache-2.0
"""A JSON value read as Go's ``encoding/json`` reads it into a typed value
(``json.Unmarshal``, no ``DisallowUnknownFields``), the types from the
table ``dealprofile/xdealv1``'s ``TestTypesTable`` writes by reflection
(:mod:`.gotypes`): the status books, the row books and the report books.

As Go reads them:

- a member matches its field exactly, else in another case, and a member
  no field names is ignored;
- a later member decodes into what an earlier one left: a struct, a
  pointer's value and a list's elements merge, a map's values are
  replaced;
- a null leaves a string, number, bool or struct as it was and sets a
  pointer, list or map to nil;
- a value of another type is refused, and an integer must fit its field.

A list keeps, past its length, the elements an earlier spelling of the
member decoded (Go's slice keeps them in its backing array, whose capacity
is never below the longest length decoded): a later, longer spelling
decodes into them. A null sets the list to nil and an empty array makes a
new one, so both drop what was kept.

A record (``Record.UnmarshalJSON``) and a status time
(``StatusTime.UnmarshalJSON``) have their own readers, given by the caller.
"""
from __future__ import annotations

import re
from typing import Any, Callable

from . import gotypes

_RANGE = {"int64": (-(1 << 63), (1 << 63) - 1), "uint64": (0, (1 << 64) - 1)}


class _Slice(list):
    """A decoded Go slice: its elements, and ``backing``, the elements its
    backing array holds up to the longest length decoded into it."""

    backing: list


class GoDecodeError(ValueError):
    """A value Go's ``json.Unmarshal`` refuses for its type."""


def fold(s: str) -> str:
    """Go's ``encoding/json`` name match: case-insensitive under simple
    folding, which also takes U+017F for ``s`` and U+212A for ``k``."""
    return s.replace("ſ", "s").replace("K", "k").lower()


def _fields(t: dict) -> list[dict]:
    return gotypes.table()["structs"][t["struct"]]


def zero(t: Any) -> Any:
    if t == "string":
        return ""
    if t == "bool":
        return False
    if isinstance(t, str) and t in _RANGE:
        return 0
    if isinstance(t, dict) and "struct" in t:
        return {f["name"]: zero(f["type"]) for f in _fields(t)}
    return None  # raw, ptr, slice, map, and a custom type's zero


Custom = dict[str, Callable[[Any], Any]]


def read(v: Any, t: Any, custom: Custom, cur: Any = None) -> Any:
    """``v`` read into a value of type ``t`` holding ``cur`` (its zero value
    when ``None`` for a type whose zero is not nil)."""
    if isinstance(t, str) and t in custom:
        return custom[t](v)
    if t == "raw":
        return v
    if cur is None:
        cur = zero(t)
    if v is None:
        if isinstance(t, dict) and ("ptr" in t or "slice" in t or "map" in t):
            return None
        return cur
    if t == "string":
        if not isinstance(v, str):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a Go value of type string")
        return v
    if t == "bool":
        if not isinstance(v, bool):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a Go value of type bool")
        return v
    if isinstance(t, str) and t in _RANGE:
        lo, hi = _RANGE[t]
        if isinstance(v, bool) or not isinstance(v, int) or not lo <= v <= hi:
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a Go value of type {t}")
        return v
    if "ptr" in t:
        # A pointer already set decodes into what it points at.
        return read(v, t["ptr"], custom, cur)
    if "slice" in t:
        if not isinstance(v, list):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a Go slice")
        if not v:
            out = _Slice()
            out.backing = []
            return out
        backing = cur.backing if isinstance(cur, _Slice) else list(cur or [])
        out = _Slice(read(e, t["slice"], custom, backing[i] if i < len(backing) else None) for i, e in enumerate(v))
        out.backing = list(out) + backing[len(out):]
        return out
    if "map" in t:
        if not isinstance(v, dict):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a Go map")
        out = dict(cur or {})
        for k, e in v.items():
            if t.get("key") == "uint64":
                if not re.fullmatch(r"[0-9]+", k) or int(k) >= 1 << 64:
                    raise GoDecodeError(f"json: cannot unmarshal number {k} into a Go value of type uint64")
                key: Any = int(k)
            else:
                key = k
            out[key] = read(e, t["map"], custom)  # each value decodes into a fresh zero value
        return out
    if not isinstance(v, dict):
        raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a Go struct")
    fields = {f["name"]: f["type"] for f in _fields(t)}
    out = dict(cur)
    for k, e in v.items():
        name = k if k in fields else next((f for f in fields if fold(f) == fold(k)), None)
        if name is not None:
            out[name] = read(e, fields[name], custom, out[name])
    return out


def _kind(v: Any) -> str:
    if isinstance(v, bool):
        return "bool"
    if isinstance(v, (int, float)):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "array"
    return "object"
