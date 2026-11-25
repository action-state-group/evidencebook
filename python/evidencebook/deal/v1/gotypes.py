# SPDX-License-Identifier: Apache-2.0
"""A JSON value read as Go's ``encoding/json`` reads it into the x-deal-v1
types, and written back as ``json.Marshal`` writes it.

Go's ``xdealv1.Decode`` refuses a record text that its types would not
encode back to: a member the types do not name, a member name in another
case, a null where a value is held, a zero value a member's ``omitzero``
leaves out. :func:`round_trips` is that test. The types are read from
``_data/x-deal-v1-types.json``, which ``dealprofile/xdealv1``'s
``TestTypesTable`` writes from the Go types by reflection and checks in CI.

A member name in another case is refused here at once, where Go reads it
and then refuses the text because it encodes back under the field's own
name: the outcome is the same.
"""
from __future__ import annotations

import json
from functools import lru_cache
from pathlib import Path
from typing import Any

_TABLE = Path(__file__).resolve().parent.parent / "_data" / "x-deal-v1-types.json"

_INT64_MIN, _INT64_MAX = -(1 << 63), (1 << 63) - 1


class GoTypeError(ValueError):
    """A value Go's decoder refuses for its type."""


class _AbsentType:
    """A ``Null[T]`` member not present: its zero value."""

    def __repr__(self) -> str:
        return "ABSENT"


ABSENT = _AbsentType()


class _NilRawType:
    """A nil ``json.RawMessage``: a raw member not present. A raw member
    read from ``null`` holds the text ``null`` (``None`` here), which is not
    zero, so ``omitzero`` keeps it."""

    def __repr__(self) -> str:
        return "NIL_RAW"


NIL_RAW = _NilRawType()


@lru_cache(maxsize=None)
def table() -> dict:
    return json.loads(_TABLE.read_bytes())


def body_type(record_type: str) -> Any:
    """The Go type of a record type's body, ``None`` for no x-deal-v1 type."""
    return table()["bodies"].get(record_type)


def block_type() -> Any:
    return table()["block"]


def terms_rows_type() -> Any:
    return table()["terms_rows"]


def _fields(t: dict) -> list[dict]:
    return table()["structs"][t["struct"]]


def zero(t: Any) -> Any:
    """Go's zero value of type ``t``."""
    if t == "string":
        return ""
    if t == "int64":
        return 0
    if t == "bool":
        return False
    if isinstance(t, dict) and "struct" in t:
        return {f["name"]: zero(f["type"]) for f in _fields(t)}
    if isinstance(t, dict) and "null" in t:
        return ABSENT
    if t == "raw":
        return NIL_RAW
    return None  # ptr, slice, map: nil


def is_zero(v: Any, t: Any) -> bool:
    """Whether ``omitzero`` leaves the member out."""
    if isinstance(t, dict) and "struct" in t:
        return all(is_zero(v[f["name"]], f["type"]) for f in _fields(t))
    return v == zero(t) and type(v) is type(zero(t))


def read(v: Any, t: Any, cur: Any = None) -> Any:
    """``v`` decoded into a Go value of type ``t`` held at ``cur`` (its zero
    value when ``None`` is given for a non-pointer type). A null leaves a
    string, number, bool or struct as it was and sets a pointer, slice, map
    or raw member to nil (a raw member holds the text ``null``)."""
    if cur is None:
        cur = zero(t)
    if t == "raw":
        return v
    if isinstance(t, dict) and "null" in t:
        # Null[T].UnmarshalJSON: present, and the value read into *T.
        return None if v is None else read(v, t["null"])
    if v is None:
        if isinstance(t, dict) and ("ptr" in t or "slice" in t or "map" in t):
            return None
        return cur
    if t == "string":
        if not isinstance(v, str):
            raise GoTypeError(f"cannot read {_kind(v)} into a Go string")
        return v
    if t == "bool":
        if not isinstance(v, bool):
            raise GoTypeError(f"cannot read {_kind(v)} into a Go bool")
        return v
    if t == "int64":
        if isinstance(v, bool) or not isinstance(v, int) or not _INT64_MIN <= v <= _INT64_MAX:
            raise GoTypeError(f"cannot read {_kind(v)} into a Go int64")
        return v
    if "ptr" in t:
        return read(v, t["ptr"], cur if cur is not None else zero(t["ptr"]))
    if "slice" in t:
        if not isinstance(v, list):
            raise GoTypeError(f"cannot read {_kind(v)} into a Go slice")
        return [read(e, t["slice"]) for e in v]
    if "map" in t:
        if not isinstance(v, dict):
            raise GoTypeError(f"cannot read {_kind(v)} into a Go map")
        out = dict(cur) if cur is not None else {}
        for k, e in v.items():
            out[k] = read(e, t["map"])
        return out
    if not isinstance(v, dict):
        raise GoTypeError(f"cannot read {_kind(v)} into a Go struct")
    fields = {f["name"]: f["type"] for f in _fields(t)}
    out = dict(cur)
    for k, e in v.items():
        if k not in fields:
            raise GoTypeError(f"json: unknown field {json.dumps(k)}")
        out[k] = read(e, fields[k], out[k])
    return out


def write(v: Any, t: Any) -> Any:
    """A Go value of type ``t`` as ``json.Marshal`` writes it, as a JSON value."""
    if t == "raw":
        return None if v is NIL_RAW else v  # a nil RawMessage writes null
    if isinstance(t, dict) and "null" in t:
        return None if v is ABSENT or v is None else write(v, t["null"])
    if t in ("string", "int64", "bool"):
        return v
    if v is None:
        return None
    if "ptr" in t:
        return write(v, t["ptr"])
    if "slice" in t:
        return [write(e, t["slice"]) for e in v]
    if "map" in t:
        return {k: write(e, t["map"]) for k, e in v.items()}
    out = {}
    for f in _fields(t):
        if f.get("omitzero") and f["name"] not in v:
            continue  # a member the caller never set is Go's zero value, which omitzero leaves out
        fv = v[f["name"]]
        if f.get("omitzero") and is_zero(fv, f["type"]):
            continue
        out[f["name"]] = write(fv, f["type"])
    return out


def _kind(v: Any) -> str:
    if v is None:
        return "null"
    if isinstance(v, bool):
        return "bool"
    if isinstance(v, (int, float)):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "array"
    return "object"
