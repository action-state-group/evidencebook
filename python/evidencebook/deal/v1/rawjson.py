# SPDX-License-Identifier: Apache-2.0
"""JSON text read and written as Go's ``encoding/json`` reads and writes
raw members (``json.RawMessage``, ``map[string]json.RawMessage``), keeping
each member's text as written, for the sent and book pairs (Go
``sent.go``, ``receive.go``, ``payment.go``). Every syntax and nesting
question is :mod:`.goscan`'s, Go's scanner ported, and the whole is held to
Go by ``testdata/deal-v1/rawjson/vectors.json`` (``TestRawJSONVectors``).

- :func:`text_check` is Go's ``checkText``: valid UTF-8, no escaped
  surrogate outside a pair, then a ``Decoder.Token`` walk of the first
  value, which refuses a member given twice and a number that overflows a
  float64.
- :func:`strict_struct` is Go's ``strictUnmarshal`` into a struct of raw
  members: a member matches its field exactly, else in another case; a
  member no field names is refused; a ``map`` field merges each spelling
  into the map an earlier one made, and a null resets it; then
  ``Decoder.More`` refuses what follows, unless it is white space, ``]`` or
  ``}``.
- :func:`unmarshal_map` is ``json.Unmarshal`` into
  ``map[string]json.RawMessage``: the whole text one value, a later member
  replacing an earlier.
- :func:`valid` is Go's ``json.Valid``; :func:`string_member` reads one
  string field as ``json.Unmarshal`` into a struct does.
"""
from __future__ import annotations

import json
from typing import Any

from ..check import StageError
from . import golenient, goscan
from .check import _check_text


class GoJSONError(ValueError):
    """Text Go's decoder refuses."""


def _b(v: Any) -> bytes:
    """Text as its bytes: a str holds raw bytes as surrogate escapes."""
    return v.encode("utf-8", "surrogateescape") if isinstance(v, str) else bytes(v)


def raw_text(b: bytes) -> str:
    """Raw member bytes as a str that keeps every byte, invalid UTF-8
    included (as surrogate escapes): Go's ``json.RawMessage`` keeps them."""
    return b.decode("utf-8", "surrogateescape")


def _scan(b: bytes, i: int = 0, **kw: Any) -> goscan.Scanned:
    try:
        return goscan.scan(b, i, **kw)
    except goscan.ScanError as exc:
        raise GoJSONError(str(exc)) from None


def text_check(raw: bytes) -> str:
    """Go's ``checkText`` over ``raw``; returns its text. A refusal is a
    canonicalization :class:`StageError`."""
    _check_text(raw)
    try:
        goscan.scan(bytes(raw), 0, tokens=True)
    except goscan.ScanError as exc:
        raise StageError("canonicalization", str(exc)) from None
    return raw.decode("utf-8")


def members(text: Any, i: int = 0) -> list[tuple[str, str]]:
    """The members of the JSON object that is the first value at
    ``text[i:]``, in order, each its name and its value's text; every
    occurrence of a name is kept."""
    b = _b(text)
    j = goscan._skip(b, i)
    if j >= len(b) or b[j] != 0x7B:
        raise GoJSONError("not a JSON object")
    sc = _scan(b, j, members=True)
    return [(name, raw_text(b[s:e])) for name, s, e in sc.members]


def _whole(b: bytes) -> tuple[int, int]:
    """The span of the one value ``json.Unmarshal`` reads (Go's checkValid
    first: the whole text, white space around it)."""
    start = goscan._skip(b, 0)
    end = _scan(b, start).end
    if goscan._skip(b, end) != len(b):
        raise GoJSONError("invalid character after top-level value")
    return start, end


def unmarshal_map(text: Any) -> dict[str, str] | None:
    """``json.Unmarshal`` of ``text`` into ``map[string]json.RawMessage``:
    ``None`` for null; a later member replaces an earlier."""
    b = _b(text)
    start, end = _whole(b)
    v = b[start:end]
    if v == b"null":
        return None
    if not v.startswith(b"{"):
        raise GoJSONError("json: cannot unmarshal into a map")
    return dict(members(v))


def valid(text: Any, depth: int = goscan.MAX_DEPTH) -> bool:
    """Go's ``json.Valid``, nested at most ``depth`` deep."""
    return goscan.valid(_b(text), depth)


def strict_struct(text: Any, fields: dict[str, str]) -> dict[str, Any]:
    """Go's ``strictUnmarshal`` of ``text``'s first value into a struct
    whose fields are raw members: ``fields`` maps each name to ``"raw"`` (a
    ``json.RawMessage``) or ``"map"`` (a ``map[string]json.RawMessage``). A
    field left out is ``None``. Raises :class:`GoJSONError` where Go does."""
    b = _b(text)
    start = goscan._skip(b, 0)
    end = _scan(b, start).end
    v = b[start:end]
    out: dict[str, Any] = {name: None for name in fields}
    err: GoJSONError | None = None
    if v != b"null":
        if not v.startswith(b"{"):
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
    if goscan.more(b, end):
        raise GoJSONError("xdealv1: trailing data after the JSON value")
    return out


def string_member(text: Any, name: str) -> str:
    """``json.Unmarshal(text, &struct{Name string})``'s ``Name``: each
    occurrence in order, the member in any case, a later one replacing an
    earlier, a null leaving it as it was; a value of another type refused
    once the rest is read."""
    b = _b(text)
    start, end = _whole(b)
    v = b[start:end]
    if v == b"null":
        return ""
    if not v.startswith(b"{"):
        raise GoJSONError("json: cannot unmarshal into a struct")
    out, err = "", None
    for k, e in members(v):
        if k == name or golenient.fold(k) == golenient.fold(name):
            if e == "null":
                continue
            if e.startswith('"'):
                out = goscan.go_unquote(_b(e))
            elif err is None:
                err = GoJSONError(f"json: cannot unmarshal into Go struct field .{name} of type string")
    if err is not None:
        raise err
    return out





def string_value(text: Any) -> str:
    """``json.Unmarshal(text, &s)`` into a string: a null leaves it empty;
    a value of another type is refused."""
    b = _b(text)
    start, end = _whole(b)
    v = b[start:end]
    if v == b"null":
        return ""
    if not v.startswith(b'"'):
        raise GoJSONError("json: cannot unmarshal into a Go value of type string")
    return goscan.go_unquote(v)
