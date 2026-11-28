# SPDX-License-Identifier: Apache-2.0
"""JSON decoded into a typed value as Go's ``decodeStrict`` decodes it:
``encoding/json/v2`` with ``RejectUnknownMembers`` (evidencebook#1072), so a
Python reader accepts and refuses exactly what Go's does.

A member name matches its field exactly; any other name, a case variant
included, is refused, and so is a name repeated in any object, at any depth
(:func:`decode_text`). ``null`` sets a field to its zero value. A ``[]byte``
takes a standard base64 string with its padding, and a ``time.Time`` an RFC
3339 text (Go's ``time.RFC3339Nano`` layout, read strictly: :func:`go_time`).
Text with invalid UTF-8 or an unpaired surrogate escape is refused.

:func:`decode` reads a Python value; :func:`decode_text` reads JSON text.
:func:`read_text` without ``strict`` reads text as ``encoding/json`` v1 does
(members in order, duplicates kept, invalid text as U+FFFD), for Go's
lenient ``json.Unmarshal`` readers.

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
import re
from dataclasses import dataclass
from typing import Any

from .errors import InvalidError

__all__ = ["STRING", "UINT64", "INT", "BOOL", "TIME", "BYTES", "RAW", "JSON_NULL", "List", "Map", "Ptr", "Struct",
           "decode", "decode_text", "go_time", "is_object", "read_text", "zero"]

STRING, UINT64, INT, BOOL, TIME, BYTES, RAW = "string", "uint64", "int", "bool", "time", "bytes", "raw"

#: Go's decoder nests at most this deep (encoding/json maxNestingDepth, and
#: encoding/json/jsontext's limit alike).
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


# ASCII digits only: Unicode digits are not Go's.
_TIME = re.compile(r"(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-](\d{2}):(\d{2}))", re.ASCII)


def go_time(text: str) -> bool:
    """Whether Go's ``encoding/json/v2`` reads ``text`` (the string's content)
    into a ``time.Time``: ``time.RFC3339Nano`` read strictly, so two-digit
    hours, a fraction only after a period, and an offset under 24:00 with
    minutes under 60."""
    m = _TIME.fullmatch(text)
    if m is None:
        return False
    year, month, day, hour, minute, second = (int(m.group(i)) for i in range(1, 7))
    if not 1 <= month <= 12 or not 1 <= day <= _days_in(month, year) or hour > 23 or minute > 59 or second > 59:
        return False
    return m.group(9) is None or (int(m.group(9)) < 24 and int(m.group(10)) < 60)


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
        return zero(spec)
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
        if not isinstance(value, str) or len(value) % 4:
            raise InvalidError(f"{where}: not a padded base64 string")
        try:
            return base64.b64decode(value, validate=True)
        except (binascii.Error, ValueError):
            raise InvalidError(f"{where}: not base64") from None
    if isinstance(spec, List):
        if not isinstance(value, list) or isinstance(value, _Pairs):
            raise InvalidError(f"{where}: not an array")
        return [_read(spec.item, v, zero(spec.item), f"{where}[{i}]") for i, v in enumerate(value)]
    if isinstance(spec, Map):
        members = _members(value)
        if members is None:
            raise InvalidError(f"{where}: not an object")
        out = {}
        for key, member in members:
            out[key] = _read(spec.item, member, zero(spec.item), f"{where}[{key!r}]")
        return out
    if isinstance(spec, Ptr):
        return _read(spec.to, value, zero(spec.to), where)
    if isinstance(spec, Struct):
        members = _members(value)
        if members is None:
            raise InvalidError(f"{where}: not an object")
        out = dict(current)
        seen: set[str] = set()
        for key, member in members:
            if key not in spec.fields:
                raise InvalidError(f"{where}: unknown object member name {key!r}")
            if key in seen:
                raise InvalidError(f"{where}: duplicate object member name {key!r}")
            seen.add(key)
            out[key] = _read(spec.fields[key], member, out[key], f"{where}.{key}")
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


def _read_string(text: str, i: int, strict: bool = False) -> tuple[str, int]:
    # text[i] is the opening quote. Go v1 writes an unpaired surrogate escape
    # as U+FFFD, v2 (strict) refuses it; both refuse a raw control character.
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
            if strict:
                raise ValueError(f"invalid surrogate pair at {j - 6}")
            out.append("\ufffd")
        elif 0xDC00 <= code < 0xE000:
            if strict:
                raise ValueError(f"invalid surrogate pair at {j - 6}")
            out.append("\ufffd")
        else:
            out.append(chr(code))


def read_text(data: bytes, strict: bool = False) -> Any:
    """JSON text as Go's decoder reads it into a typed value, without
    recursion: members in document order, nesting at most :data:`MAX_DEPTH`,
    trailing data refused, a number out of float64 range kept as a marker
    that only an interface value refuses. As ``encoding/json`` v1 reads it,
    invalid UTF-8 and unpaired surrogate escapes become U+FFFD and repeated
    members are kept; ``strict`` reads it as v2 does, refusing all three.
    Raises ``ValueError``."""
    if strict:
        try:
            text = data.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ValueError(f"invalid UTF-8 at {exc.start}") from None
    else:
        text = data.decode("utf-8", errors="replace")
    n = len(text)
    stack: list[list] = []  # [container, the pending member name, the names seen]
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
            stack[-1][1], i = _read_string(text, i, strict)
            if strict:
                if stack[-1][1] in stack[-1][2]:
                    raise ValueError(f"duplicate object member name {stack[-1][1]!r}")
                stack[-1][2].add(stack[-1][1])
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
            stack.append([_Pairs() if c == "{" else _Array(), None, set()])
            i += 1
            while i < n and text[i] in _WS:
                i += 1
            if i < n and text[i] == ("}" if c == "{" else "]"):
                value, i = stack.pop()[0], i + 1
            else:
                state = "key" if c == "{" else "value"
                continue
        elif c == '"':
            value, i = _read_string(text, i, strict)
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
    bytes (:func:`read_text`, strictly)."""
    try:
        text = read_text(data, strict=True)
    except ValueError as exc:
        raise InvalidError(f"{what}: {exc}") from None
    return _read(spec, text, zero(spec), what)
