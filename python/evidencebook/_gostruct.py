# SPDX-License-Identifier: Apache-2.0
"""JSON decoded into a typed value as Go's ``encoding/json`` decodes it into a
struct under ``DisallowUnknownFields`` (Go ``decodeStrict``), so a Python
reader accepts and refuses exactly what Go's does.

Go reads an object's members in order: a member name matches its field
exactly or else case-insensitively (Go's fold), a later member overwrites an
earlier one, ``null`` leaves a field as it was (its zero value), and an
unknown member is refused. The value is read from its canonical (RFC 8785)
text, as Go reads a header's statement and a bundle's extension, so members
come in canonical order and an integral number is an integer.

A spec is one of ``STRING``, ``UINT64``, ``INT``, ``BOOL``, ``TIME``,
``BYTES``, ``RAW`` (a ``json.RawMessage``: any value, :data:`JSON_NULL` for
``null``), ``List(spec)``, ``Map(spec)``, ``Ptr(spec)`` or ``Struct(fields)``, where
``fields`` maps each JSON name to its spec. A struct decodes to a dict of
every field (its zero value when absent), a ``Ptr`` to ``None`` or its value,
a ``List`` to ``None`` (Go's nil) or a list.
"""
from __future__ import annotations

import base64
import binascii
import json
import re
from dataclasses import dataclass
from typing import Any, Optional

from agent_action_capsule.canonical import jcs

from .errors import InvalidError

__all__ = ["STRING", "UINT64", "INT", "BOOL", "TIME", "BYTES", "RAW", "JSON_NULL", "List", "Map", "Ptr", "Struct", "decode", "zero"]

STRING, UINT64, INT, BOOL, TIME, BYTES, RAW = "string", "uint64", "int", "bool", "time", "bytes", "raw"


@dataclass(frozen=True)
class List:
    item: Any


@dataclass(frozen=True)
class Map:
    """A Go ``map[string]T``: member names are its keys exactly."""

    item: Any


class _JSONNull:
    """A ``json.RawMessage`` that holds the literal ``null``."""

    def __repr__(self) -> str:
        return "JSON_NULL"


#: What a ``RAW`` field decodes to when its member is ``null``: Go keeps the
#: literal in the ``json.RawMessage``, unlike an absent member (``None``).
JSON_NULL = _JSONNull()


@dataclass(frozen=True)
class Ptr:
    to: Any


@dataclass(frozen=True)
class Struct:
    fields: dict


# The characters outside ASCII whose Go fold (unicode.ToUpper(unicode.ToLower(r)))
# is an ASCII letter; any other one folds to a character no field name holds.
_FOLD_TO_ASCII = {"İ": "I", "ı": "I", "ſ": "S", "K": "K"}


def _fold(name: str) -> Optional[str]:
    out = []
    for c in name:
        if c < "\x80":
            out.append(c.upper())
        elif c in _FOLD_TO_ASCII:
            out.append(_FOLD_TO_ASCII[c])
        else:
            return None
    return "".join(out)


# Go's time.Time.UnmarshalJSON: RFC 3339 with an upper-case T and Z, any
# fraction digits, no leap second.
_TIME = re.compile(r"(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))\Z")


def _go_time(text: str) -> bool:
    m = _TIME.match(text)
    if m is None:
        return False
    year, month, day, hour, minute, second = (int(m.group(i)) for i in range(1, 7))
    if month < 1 or month > 12 or day < 1 or hour > 23 or minute > 59 or second > 59:
        return False
    days = 29 if month == 2 and year % 4 == 0 and (year % 100 != 0 or year % 400 == 0) else \
        (28 if month == 2 else 30 if month in (4, 6, 9, 11) else 31)
    if day > days:
        return False
    return m.group(7) is None or (int(m.group(7)) <= 23 and int(m.group(8)) <= 59)


def zero(spec: Any) -> Any:
    if spec in (STRING, TIME):
        return ""
    if spec in (UINT64, INT):
        return 0
    if spec == BOOL:
        return False
    if isinstance(spec, Struct):
        return {name: zero(s) for name, s in spec.fields.items()}
    return None


def _pairs_to_value(value: Any) -> Any:
    if isinstance(value, _Pairs):
        return {k: _pairs_to_value(v) for k, v in value}
    if isinstance(value, list):
        return [_pairs_to_value(v) for v in value]
    return value


class _Pairs(list):
    """An object's members as read, in order."""


def _read(spec: Any, value: Any, current: Any, where: str) -> Any:
    if spec == RAW:
        return JSON_NULL if value is None else _pairs_to_value(value)
    if value is None:
        return current
    if spec == STRING:
        if not isinstance(value, str):
            raise InvalidError(f"{where}: not a string")
        return value
    if spec in (UINT64, INT):
        lo, hi = (0, 1 << 64) if spec == UINT64 else (-(1 << 63), 1 << 63)
        if isinstance(value, bool) or not isinstance(value, int) or not lo <= value < hi:
            raise InvalidError(f"{where}: not a Go {spec}")
        return value
    if spec == BOOL:
        if not isinstance(value, bool):
            raise InvalidError(f"{where}: not a bool")
        return value
    if spec == TIME:
        if not isinstance(value, str) or not _go_time(value):
            raise InvalidError(f"{where}: not an RFC 3339 time")
        return value
    if spec == BYTES:
        if not isinstance(value, str):
            raise InvalidError(f"{where}: not a base64 string")
        try:
            return base64.b64decode(value.replace("\r", "").replace("\n", ""), validate=True)
        except (binascii.Error, ValueError):
            raise InvalidError(f"{where}: not base64") from None
    if isinstance(spec, List):
        if not isinstance(value, list) or isinstance(value, _Pairs):
            raise InvalidError(f"{where}: not an array")
        return [_read(spec.item, v, zero(spec.item), f"{where}[{i}]") for i, v in enumerate(value)]
    if isinstance(spec, Map):
        if not isinstance(value, _Pairs):
            raise InvalidError(f"{where}: not an object")
        out = {}
        for key, member in value:
            out[key] = _read(spec.item, member, zero(spec.item), f"{where}[{key!r}]")
        return out
    if isinstance(spec, Ptr):
        return _read(spec.to, value, zero(spec.to) if current is None else current, where)
    if isinstance(spec, Struct):
        if not isinstance(value, _Pairs):
            raise InvalidError(f"{where}: not an object")
        out = dict(current)
        folded = {_fold(name): name for name in spec.fields}
        for key, member in value:
            name = key if key in spec.fields else folded.get(_fold(key))
            if name is None:
                raise InvalidError(f"{where}: unknown field {key!r}")
            out[name] = _read(spec.fields[name], member, out[name], f"{where}.{name}")
        return out
    raise TypeError(f"spec {spec!r}")


def decode(value: Any, spec: Any, what: str) -> Any:
    """Decode the JSON value ``value`` into ``spec`` as Go's ``decodeStrict``
    does; ``InvalidError`` names ``what`` on a refusal."""
    try:
        canonical = json.loads(jcs(value), object_pairs_hook=_Pairs)
    except (TypeError, ValueError) as exc:
        raise InvalidError(f"{what}: {exc}") from None
    return _read(spec, canonical, zero(spec), what)
