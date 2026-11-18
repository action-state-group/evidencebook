# SPDX-License-Identifier: Apache-2.0
"""JSON text read as Go's ``encoding/json`` reads it into raw members
(``json.RawMessage``, ``map[string]json.RawMessage``), keeping each
member's text as written, for the sent and book pairs (Go ``sent.go``,
``receive.go``, ``payment.go``).

- :func:`text_check` is Go's ``checkText``: valid UTF-8, no escaped
  surrogate outside a pair, and no member given twice, over the first JSON
  value of the text, with no nesting bound (Go applies JCS's bound to each
  member it canonicalizes, counted from that member).
- :func:`strict_struct` is Go's ``strictUnmarshal`` into a struct of raw
  members: a member matches its field exactly, else in another case; a
  member no field names is refused; a ``map`` field merges each spelling
  into the map an earlier one made, and a null resets it; trailing data
  after the first value is refused.
- :func:`unmarshal_map` is ``json.Unmarshal`` into
  ``map[string]json.RawMessage``: one JSON value and nothing after it, a
  later member replacing an earlier one.
"""
from __future__ import annotations

import json
from typing import Any

from ..check import StageError, _go_quote, _refuse_constant
from . import golenient
from .check import _check_text

_WS = " \t\n\r"


class GoJSONError(ValueError):
    """Text Go's decoder refuses."""


def _skip(s: str, i: int) -> int:
    while i < len(s) and s[i] in _WS:
        i += 1
    return i


def _repeat_hook(pairs: list[tuple[str, Any]]) -> dict:
    seen: set[str] = set()
    for k, _ in pairs:
        if k in seen:
            raise _Repeat(k)
        seen.add(k)
    return {}


class _Repeat(Exception):
    def __init__(self, name: str) -> None:
        self.name = name


#: Go's scanner refuses JSON nested deeper than this (encoding/json maxNestingDepth).
_MAX_DEPTH = 10000


def _too_deep(s: str, i: int, limit: int = _MAX_DEPTH) -> bool:
    """Whether the value at ``s[i:]`` nests past Go's scanner bound, counted
    outside strings."""
    depth, in_str, esc = 0, False, False
    for c in s[i:]:
        if in_str:
            if esc:
                esc = False
            elif c == "\\":
                esc = True
            elif c == '"':
                in_str = False
        elif c == '"':
            in_str = True
        elif c in "[{":
            depth += 1
            if depth > limit:
                return True
        elif c in "]}":
            depth -= 1
            if depth == 0:
                return False
        elif depth == 0 and c not in _WS:
            return False
    return False


def _float64(text: str) -> Any:
    """A number as Go's ``Decoder.Token`` reads it, a float64: one that
    overflows it is refused."""
    v = float(text)
    if v in (float("inf"), float("-inf")):
        raise GoJSONError(f"json: cannot unmarshal number {text} into Go value of type float64")
    return v


def _value_end(s: str, i: int, hook: Any = None, numbers: bool = False) -> int:
    """The end of the one JSON value at ``s[i:]`` (Python's own reader
    checks its syntax; Go's nesting bound is checked first). With
    ``numbers``, each number is read as a float64, as Go's tokens are."""
    if _too_deep(s, i):
        raise GoJSONError("exceeded max depth")
    kw: dict[str, Any] = {"object_pairs_hook": hook, "parse_constant": _refuse_constant}
    if numbers:
        kw.update(parse_float=_float64, parse_int=_float64)
    dec = json.JSONDecoder(**kw)
    try:
        _, end = dec.raw_decode(s, i)
    except RecursionError:
        raise GoJSONError("json: exceeded max depth") from None
    except ValueError as exc:
        raise GoJSONError(str(exc)) from None
    return end


def text_check(raw: bytes) -> str:
    """Go's ``checkText`` over ``raw``; returns its text. A refusal is a
    canonicalization :class:`StageError`."""
    _check_text(raw)
    s = raw.decode("utf-8")
    try:
        _value_end(s, _skip(s, 0), _repeat_hook, numbers=True)
    except _Repeat as exc:
        raise StageError("canonicalization", f"member {_go_quote(exc.name)} given twice") from None
    except GoJSONError as exc:
        raise StageError("canonicalization", str(exc)) from None
    return s


def members(s: str, i: int = 0) -> list[tuple[str, str]]:
    """The members of the JSON object at ``s[i:]``, in order, each its name
    and its value's text; every occurrence of a name is kept."""
    i = _skip(s, i)
    if i >= len(s) or s[i] != "{":
        raise GoJSONError("not a JSON object")
    end = _value_end(s, i)
    out: list[tuple[str, str]] = []
    i = _skip(s, i + 1)
    while i < end - 1:
        key, i = json.decoder.scanstring(s, i + 1)
        i = _skip(s, i) + 1  # the colon
        i = _skip(s, i)
        vend = _value_end(s, i)
        out.append((key, s[i:vend]))
        i = _skip(s, vend)
        if s[i] == ",":
            i = _skip(s, i + 1)
    return out


def unmarshal_map(text: str) -> dict[str, str] | None:
    """``json.Unmarshal`` of ``text`` into ``map[string]json.RawMessage``:
    ``None`` for null; a later member replaces an earlier."""
    i = _skip(text, 0)
    end = _value_end(text, i)
    if _skip(text, end) != len(text):
        raise GoJSONError("invalid character after top-level value")
    v = text[i:end]
    if v == "null":
        return None
    if not v.startswith("{"):
        raise GoJSONError("json: cannot unmarshal into a map")
    return dict(members(v))


def valid(text: str, depth: int = _MAX_DEPTH) -> bool:
    """Go's ``json.Valid``: exactly one JSON value, white space around it,
    nested at most ``depth`` deep. A raw member that ``json.Marshal`` writes
    inside a map may nest one level less than the map's own bound."""
    try:
        i = _skip(text, 0)
        if _too_deep(text, i, depth):
            return False
        return _skip(text, _value_end(text, i)) == len(text)
    except GoJSONError:
        return False


def strict_struct(s: str, fields: dict[str, str]) -> dict[str, Any]:
    """Go's ``strictUnmarshal`` of ``s``'s first value into a struct whose
    fields are raw members: ``fields`` maps each name to ``"raw"`` (a
    ``json.RawMessage``) or ``"map"`` (a ``map[string]json.RawMessage``). A
    field left out is ``None``. Raises :class:`GoJSONError` where Go does."""
    i = _skip(s, 0)
    end = _value_end(s, i)
    v = s[i:end]
    out: dict[str, Any] = {name: None for name in fields}
    err: GoJSONError | None = None
    if v != "null":
        if not v.startswith("{"):
            raise GoJSONError("json: cannot unmarshal into a struct")
        for k, e in members(v):
            name = k if k in fields else next((f for f in fields if golenient.fold(f) == golenient.fold(k)), None)
            if name is None:
                raise GoJSONError(f"json: unknown field {json.dumps(k)}")
            if fields[name] == "raw":
                out[name] = e
            elif e == "null":
                out[name] = None
            elif e.startswith("{"):
                merged = dict(out[name] or {})
                merged.update(members(e))
                out[name] = merged
            elif err is None:
                # Go records the type error, decodes the rest, and returns it.
                err = GoJSONError(f"json: cannot unmarshal into Go struct field .{name} of type map")
    if err is not None:
        raise err
    if _skip(s, end) != len(s):
        raise GoJSONError("xdealv1: trailing data after the JSON value")
    return out


def string_member(text: str, name: str) -> str:
    """``json.Unmarshal(text, &struct{Name string})``'s ``Name``: each
    occurrence in order, the member in any case, a later one replacing an
    earlier, a null leaving it as it was; a value of another type refused
    once the rest is read."""
    out, err = "", None
    for k, e in members(text):
        if k == name or golenient.fold(k) == golenient.fold(name):
            if e == "null":
                continue
            v = json.loads(e, parse_constant=_refuse_constant)
            if isinstance(v, str):
                out = v
            elif err is None:
                err = GoJSONError(f"json: cannot unmarshal into Go struct field .{name} of type string")
    if err is not None:
        raise err
    return out
