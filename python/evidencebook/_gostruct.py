# SPDX-License-Identifier: Apache-2.0
"""JSON decoded into a typed value as Go's ``encoding/json`` decodes it into a
struct under ``DisallowUnknownFields`` (Go ``decodeStrict``), so a Python
reader accepts and refuses exactly what Go's does.

Go reads an object's members in order: a member name matches its field
exactly or else under Go's fold (:mod:`evidencebook._gofold`), a later member
overwrites an earlier one, and an unknown member is refused. ``null`` sets a
slice, map or pointer to nil and leaves any other field as it was. An object
decoded into a map that already holds one adds to it, and an array decoded
into a slice decodes into the elements it already has. A ``[]byte`` takes a
base64 string or an array of bytes, and a ``time.Time`` an RFC 3339 text as
Go's ``time.Parse`` reads it.

:func:`decode` reads a Python value (members in their dict order: the order
a caller gave them, or the text's own order when the value was read from
JSON); :func:`decode_text` reads JSON text, keeping duplicate members.

A spec is one of ``STRING``, ``UINT64``, ``INT``, ``BOOL``, ``TIME``,
``BYTES``, ``RAW`` (a ``json.RawMessage``: the value as read, or
:data:`JSON_NULL` for ``null``), ``List(spec)``, ``Map(spec)``, ``Ptr(spec)``
or ``Struct(fields)``, where ``fields`` maps each JSON name to its spec. A
struct decodes to a dict of every field (its zero value when absent), a
``Ptr``, ``List`` or ``Map`` to ``None`` when nil.
"""
from __future__ import annotations

import base64
import binascii
from dataclasses import dataclass
from typing import Any

from ._gofold import fold_key
from .errors import InvalidError

__all__ = ["STRING", "UINT64", "INT", "BOOL", "TIME", "BYTES", "RAW", "JSON_NULL", "List", "Map", "Ptr", "Struct",
           "decode", "decode_text", "go_time", "is_object", "read_text", "zero"]

STRING, UINT64, INT, BOOL, TIME, BYTES, RAW = "string", "uint64", "int", "bool", "time", "bytes", "raw"

#: Go's decoder nests at most this deep (encoding/json maxNestingDepth).
MAX_DEPTH = 10000


@dataclass(frozen=True)
class List:
    item: Any


@dataclass(frozen=True)
class Map:
    """A Go ``map[string]T``: member names are its keys exactly."""

    item: Any


@dataclass(frozen=True)
class Ptr:
    to: Any


@dataclass(frozen=True)
class Struct:
    fields: dict


class _JSONNull:
    """A ``json.RawMessage`` that holds the literal ``null``."""

    def __repr__(self) -> str:
        return "JSON_NULL"


#: What a ``RAW`` field decodes to when its member is ``null``: Go keeps the
#: literal in the ``json.RawMessage``, unlike an absent member (``None``).
JSON_NULL = _JSONNull()


class _Pairs(list):
    """An object's members as read from text, in order, duplicates kept."""


class GoSlice(list):
    """A decoded Go slice; ``backing`` is its backing array, which a later
    member of the same name decodes into (Go keeps elements past a slice's
    length)."""

    backing: list = []


def _grown(cap: int, length: int) -> int:
    """The capacity Go's growslice gives a slice of capacity ``cap`` grown
    to ``length`` (nextslicecap; small slices of pointer-sized or
    power-of-two elements, whose size classes add nothing)."""
    double = cap + cap
    if length > double:
        return length
    if cap < 256:
        return double
    new = cap
    while new < length:
        new += (new + 3 * 256) >> 2
    return new


def is_object(value: Any) -> bool:
    """Whether ``value``, read by :func:`read_text`, is a JSON object."""
    return isinstance(value, _Pairs)


def _members(value: Any) -> list | None:
    if isinstance(value, _Pairs):
        return value
    if isinstance(value, dict):
        return list(value.items())
    return None


def _days_in(month: int, year: int) -> int:
    if month == 2:
        return 29 if year % 4 == 0 and (year % 100 != 0 or year % 400 == 0) else 28
    return 30 if month in (4, 6, 9, 11) else 31


def _getnum(s: str, fixed: bool) -> tuple[int, str] | None:
    # Go time.getnum: one or two ASCII digits, two when fixed.
    if not s or not "0" <= s[0] <= "9":
        return None
    if len(s) < 2 or not "0" <= s[1] <= "9":
        return None if fixed else (int(s[0]), s[1:])
    return int(s[:2]), s[2:]


def go_time(text: str) -> bool:
    """Whether Go's ``time.Time.UnmarshalJSON`` reads ``text`` (the string's
    content): ``time.Parse(time.RFC3339, text)`` as Go 1.27 runs it."""
    s = text
    # 2006: four ASCII digits.
    if len(s) < 4 or not all("0" <= c <= "9" for c in s[:4]):
        return False
    year, s = int(s[:4]), s[4:]
    if not s.startswith("-"):
        return False
    got = _getnum(s[1:], True)
    if got is None or not 1 <= got[0] <= 12:
        return False
    month, s = got
    if not s.startswith("-"):
        return False
    got = _getnum(s[1:], True)
    if got is None:
        return False
    day, s = got
    if not s.startswith("T"):
        return False
    got = _getnum(s[1:], False)
    if got is None or not 0 <= got[0] < 24:
        return False
    s = got[1]
    for limit in (60, 60):
        if not s.startswith(":"):
            return False
        got = _getnum(s[1:], True)
        if got is None or not 0 <= got[0] < limit:
            return False
        s = got[1]
    # A fraction the layout does not name, after a period or a comma.
    if len(s) >= 2 and s[0] in ".," and "0" <= s[1] <= "9":
        n = 1
        while n < len(s) and "0" <= s[n] <= "9":
            n += 1
        s = s[n:]
    if s.startswith("Z"):
        s = s[1:]
    else:
        if len(s) < 6 or s[3] != ":" or s[0] not in "+-":
            return False
        hh, mm = _getnum(s[1:3], True), _getnum(s[4:6], True)
        if hh is None or mm is None or hh[1] or mm[1] or hh[0] > 24 or mm[0] > 60:
            return False
        s = s[6:]
    if s:
        return False
    return 1 <= day <= _days_in(month, year)


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


def _read(spec: Any, value: Any, current: Any, where: str) -> Any:
    if spec == RAW:
        # An interface value: a number out of float64 range fails there.
        if value is _OVERFLOW or getattr(value, "overflow", False):
            raise InvalidError(f"{where}: a number out of range")
        return JSON_NULL if value is None else value
    if value is None:
        return None if isinstance(spec, (List, Map, Ptr)) or spec == BYTES else current
    if spec == STRING:
        if not isinstance(value, str):
            raise InvalidError(f"{where}: not a string")
        return value
    if spec in (UINT64, INT):
        lo, hi = (0, 1 << 64) if spec == UINT64 else (-(1 << 63), 1 << 63)
        if isinstance(value, bool) or not isinstance(value, int) or not lo <= value < hi or \
                (spec == UINT64 and isinstance(value, NegativeZero)):
            raise InvalidError(f"{where}: not a Go {spec}")
        return value
    if spec == BOOL:
        if not isinstance(value, bool):
            raise InvalidError(f"{where}: not a bool")
        return value
    if spec == TIME:
        if not isinstance(value, str) or not go_time(value):
            raise InvalidError(f"{where}: not an RFC 3339 time")
        return value
    if spec == BYTES:
        if isinstance(value, list) and not isinstance(value, _Pairs):
            out = bytearray()
            for i, item in enumerate(value):
                if item is not None and (isinstance(item, bool) or not isinstance(item, int) or not 0 <= item < 256):
                    raise InvalidError(f"{where}[{i}]: not a byte")
                out.append(item or 0)
            return bytes(out)
        if not isinstance(value, str):
            raise InvalidError(f"{where}: not a base64 string")
        try:
            return base64.b64decode(value.replace("\r", "").replace("\n", ""), validate=True)
        except (binascii.Error, ValueError):
            raise InvalidError(f"{where}: not base64") from None
    if isinstance(spec, List):
        if not isinstance(value, list) or isinstance(value, _Pairs):
            raise InvalidError(f"{where}: not an array")
        if not value:
            return GoSlice([])
        # Go decodes into the elements the slice's backing array already
        # holds, those past its length included, and grows it as append does.
        backing = list(getattr(current, "backing", current or []))
        for i, v in enumerate(value):
            if i >= len(backing):
                backing = backing[:i] + [zero(spec.item)] * (_grown(len(backing), i + 1) - i)
            backing[i] = _read(spec.item, v, backing[i], f"{where}[{i}]")
        out = GoSlice(backing[:len(value)])
        out.backing = backing
        return out
    if isinstance(spec, Map):
        members = _members(value)
        if members is None:
            raise InvalidError(f"{where}: not an object")
        out = dict(current) if current is not None else {}
        for key, member in members:
            out[key] = _read(spec.item, member, zero(spec.item), f"{where}[{key!r}]")
        return out
    if isinstance(spec, Ptr):
        return _read(spec.to, value, zero(spec.to) if current is None else current, where)
    if isinstance(spec, Struct):
        members = _members(value)
        if members is None:
            raise InvalidError(f"{where}: not an object")
        out = dict(current)
        folded = {fold_key(name): name for name in spec.fields}
        for key, member in members:
            name = key if key in spec.fields else folded.get(fold_key(key))
            if name is None:
                raise InvalidError(f"{where}: unknown field {key!r}")
            out[name] = _read(spec.fields[name], member, out[name], f"{where}.{name}")
        return out
    raise TypeError(f"spec {spec!r}")


def decode(value: Any, spec: Any, what: str) -> Any:
    """Decode the JSON value ``value`` into ``spec`` as Go's ``decodeStrict``
    decodes the same text; ``InvalidError`` names ``what`` on a refusal."""
    return _read(spec, value, zero(spec), what)


class _Overflow:
    """A number Go cannot hold as a float64 (strconv.ParseFloat's range
    error): decoding it into an interface value fails."""


_OVERFLOW = _Overflow()


class NegativeZero(int):
    """The literal ``-0``: zero, but not a Go unsigned integer."""


class _Array(list):
    """An array read from text."""


_WS = " \t\n\r"
_ESCAPES = {'"': '"', "\\": "\\", "/": "/", "b": "\b", "f": "\f", "n": "\n", "r": "\r", "t": "\t"}
_NUMBER_START = "-0123456789"


def _read_number(text: str, i: int) -> tuple[Any, int]:
    j = i
    if text[j] == "-":
        j += 1
    if j >= len(text) or not "0" <= text[j] <= "9":
        raise ValueError(f"invalid number at {i}")
    if text[j] == "0":
        j += 1
    else:
        while j < len(text) and "0" <= text[j] <= "9":
            j += 1
    fraction = False
    if j < len(text) and text[j] == ".":
        j += 1
        if j >= len(text) or not "0" <= text[j] <= "9":
            raise ValueError(f"invalid number at {i}")
        while j < len(text) and "0" <= text[j] <= "9":
            j += 1
        fraction = True
    if j < len(text) and text[j] in "eE":
        j += 1
        if j < len(text) and text[j] in "+-":
            j += 1
        if j >= len(text) or not "0" <= text[j] <= "9":
            raise ValueError(f"invalid number at {i}")
        while j < len(text) and "0" <= text[j] <= "9":
            j += 1
        fraction = True
    token = text[i:j]
    if fraction:
        value = float(token)
        return (_OVERFLOW if value in (float("inf"), float("-inf")) else value), j
    if len(token) > 4300:
        # Beyond Python's int conversion limit: no Go integer, and a float64
        # only while it is in range, which a 4300-digit integer is not.
        return _OVERFLOW, j
    value = int(token)
    if token == "-0":
        return NegativeZero(0), j
    if abs(value) > 1.7976931348623157e308:
        return _OVERFLOW, j
    return value, j


def _read_string(text: str, i: int) -> tuple[str, int]:
    # text[i] is the opening quote. Go writes an unpaired surrogate escape as
    # U+FFFD, and refuses a raw control character.
    out = []
    j = i + 1
    while True:
        if j >= len(text):
            raise ValueError("unterminated string")
        c = text[j]
        if c == '"':
            return "".join(out), j + 1
        if c < " ":
            raise ValueError(f"control character in string at {j}")
        if c != "\\":
            out.append(c)
            j += 1
            continue
        if j + 1 >= len(text):
            raise ValueError("unterminated string")
        e = text[j + 1]
        if e in _ESCAPES:
            out.append(_ESCAPES[e])
            j += 2
            continue
        if e != "u":
            raise ValueError(f"invalid escape at {j}")
        h = text[j + 2:j + 6]
        if len(h) != 4 or any(x not in "0123456789abcdefABCDEF" for x in h):
            raise ValueError(f"invalid escape at {j}")
        code = int(h, 16)
        j += 6
        if 0xD800 <= code < 0xDC00:
            low = text[j + 2:j + 6] if text[j:j + 2] == "\\u" else ""
            if len(low) == 4 and all(x in "0123456789abcdefABCDEF" for x in low) and 0xDC00 <= int(low, 16) < 0xE000:
                out.append(chr(0x10000 + ((code - 0xD800) << 10) + (int(low, 16) - 0xDC00)))
                j += 6
                continue
            out.append("\ufffd")
        elif 0xDC00 <= code < 0xE000:
            out.append("\ufffd")
        else:
            out.append(chr(code))


def read_text(data: bytes) -> Any:
    """JSON text as Go's decoder reads it into a typed value, without
    recursion: invalid UTF-8 and unpaired surrogate escapes as U+FFFD,
    members in document order with duplicates kept, nesting at most
    :data:`MAX_DEPTH`, trailing data refused, a number out of float64 range
    kept as a marker that only an interface value refuses. Raises
    ``ValueError``."""
    text = data.decode("utf-8", errors="replace")
    n = len(text)
    stack: list[list] = []  # [container, the pending member name]
    i, state = 0, "value"   # value, key, after
    while True:
        while i < n and text[i] in _WS:
            i += 1
        if i >= n:
            raise ValueError("unexpected end of JSON input")
        c = text[i]
        value: Any = None
        if state == "key":
            if c != '"':
                raise ValueError(f"expected a member name at {i}")
            stack[-1][1], i = _read_string(text, i)
            while i < n and text[i] in _WS:
                i += 1
            if i >= n or text[i] != ":":
                raise ValueError(f"expected : at {i}")
            i, state = i + 1, "value"
            continue
        if state == "after":
            obj = isinstance(stack[-1][0], _Pairs)
            if c == ",":
                i, state = i + 1, "key" if obj else "value"
                continue
            if c != ("}" if obj else "]"):
                raise ValueError(f"expected , or {'}' if obj else ']'} at {i}")
            value, i = stack.pop()[0], i + 1
        elif c in "{[":
            if len(stack) >= MAX_DEPTH:
                raise ValueError(f"exceeded max depth {MAX_DEPTH}")
            stack.append([_Pairs() if c == "{" else _Array(), None])
            i += 1
            while i < n and text[i] in _WS:
                i += 1
            if i < n and text[i] == ("}" if c == "{" else "]"):
                value, i = stack.pop()[0], i + 1
            else:
                state = "key" if c == "{" else "value"
                continue
        elif c == '"':
            value, i = _read_string(text, i)
        elif c in _NUMBER_START:
            value, i = _read_number(text, i)
        elif text.startswith("true", i):
            value, i = True, i + 4
        elif text.startswith("false", i):
            value, i = False, i + 5
        elif text.startswith("null", i):
            value, i = None, i + 4
        else:
            raise ValueError(f"invalid character at {i}")
        # Attach the value read.
        if not stack:
            while i < n and text[i] in _WS:
                i += 1
            if i != n:
                raise ValueError(f"trailing data at {i}")
            return value
        top = stack[-1]
        if isinstance(top[0], _Pairs):
            top[0].append((top[1], value))
        else:
            top[0].append(value)
        if value is _OVERFLOW or getattr(value, "overflow", False):
            top[0].overflow = True
        state = "after"


def decode_text(data: bytes, spec: Any, what: str) -> Any:
    """Decode JSON text into ``spec`` as Go's ``decodeStrict`` decodes raw
    bytes (:func:`read_text`)."""
    try:
        text = read_text(data)
    except ValueError as exc:
        raise InvalidError(f"{what}: {exc}") from None
    return _read(spec, text, zero(spec), what)
