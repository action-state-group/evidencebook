# SPDX-License-Identifier: Apache-2.0
"""A JSON value read as Go's ``encoding/json`` reads it into a plain struct
(``json.Unmarshal``, no ``DisallowUnknownFields``): a member matches its
field exactly, else in another case (the later spelling wins); a member no
field names is ignored; a null leaves a string, number, bool or struct as
it was and sets a list or map to nil; a value of another type is refused.

The status and report books are read so (Go ``StatusBooks``,
``ReportBooks``, ``PaymentResult``). A record inside them is read strictly
(:func:`.check.decode`), as Go's ``Record.UnmarshalJSON`` is.
"""
from __future__ import annotations

from typing import Any, Callable

_INT64 = (-(1 << 63), (1 << 63) - 1)
_UINT64 = (0, (1 << 64) - 1)


class GoDecodeError(ValueError):
    """A value Go's ``json.Unmarshal`` refuses for its field's type."""


def _fold(s: str) -> str:
    """Go's ``encoding/json`` name match: case-insensitive under simple
    folding, which also takes U+017F for ``s`` and U+212A for ``k``."""
    return s.replace("ſ", "s").replace("K", "k").lower()


#: A field kind: "str", "bool", "int", "uint", "raw", ("list", kind),
#: ("map", kind), ("struct", fields), ("ptr", kind), or a function of the
#: value (called for a present member, a null included) for a type with its
#: own UnmarshalJSON.
Kind = Any


def zero(kind: Kind) -> Any:
    if kind == "str":
        return ""
    if kind == "bool":
        return False
    if kind in ("int", "uint"):
        return 0
    if isinstance(kind, tuple) and kind[0] == "struct":
        return {name: zero(k) for name, k in kind[1].items()}
    return None


def read(v: Any, kind: Kind, cur: Any = None, where: str = "") -> Any:
    """``v`` read into a field of ``kind`` holding ``cur``."""
    if callable(kind):
        return kind(v)
    if cur is None and not (isinstance(kind, tuple) and kind[0] in ("list", "map", "ptr")) and kind != "raw":
        cur = zero(kind)
    if kind == "raw":
        return v
    if v is None:
        if isinstance(kind, tuple) and kind[0] in ("list", "map", "ptr"):
            return None
        return cur
    if kind == "str":
        if not isinstance(v, str):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a string{where}")
        return v
    if kind == "bool":
        if not isinstance(v, bool):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a bool{where}")
        return v
    if kind in ("int", "uint"):
        lo, hi = _INT64 if kind == "int" else _UINT64
        if isinstance(v, bool) or not isinstance(v, int) or not lo <= v <= hi:
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into an {kind}64{where}")
        return v
    tag, inner = kind
    if tag == "ptr":
        return read(v, inner, None, where)
    if tag == "list":
        if not isinstance(v, list):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a list{where}")
        return [read(e, inner, None, where) for e in v]
    if tag == "map":
        if not isinstance(v, dict):
            raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a map{where}")
        out = dict(cur or {})
        for k, e in v.items():
            out[k] = read(e, inner, None, where)
        return out
    if not isinstance(v, dict):
        raise GoDecodeError(f"json: cannot unmarshal {_kind(v)} into a struct{where}")
    out = dict(cur)
    for k, e in v.items():
        name = k if k in inner else next((f for f in inner if _fold(f) == _fold(k)), None)
        if name is not None:
            out[name] = read(e, inner[name], out[name], f" (field {name}){where}")
    return out


def struct(**fields: Kind) -> tuple[str, dict[str, Kind]]:
    return ("struct", fields)


def custom(fn: Callable[[Any], Any]) -> Callable[[Any], Any]:
    return fn


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
