# SPDX-License-Identifier: Apache-2.0
"""Go's ``encoding/json`` scanner (``scanner.go``), ported byte for byte
and without recursion, so a value nested as deep as Go allows reads the
same on every Python version.

:func:`scan` reads the first JSON value of a text as Go's ``Decoder``
reads it: it stops once the value is complete, without reading what
follows. It checks Go's grammar and nesting bound
(``maxNestingDepth``, 10,000). On request it also refuses an object
member given twice and a number that overflows a float64, as Go's
``Decoder.Token`` walk in ``checkText`` does, and records the members of
the top-level object, each its name and its value's byte span. It is held
to Go by ``testdata/deal-v1/rawjson/vectors.json``, which
``dealprofile/xdealv1``'s ``TestRawJSONVectors`` writes.
"""
from __future__ import annotations

import codecs
import json
from dataclasses import dataclass, field

MAX_DEPTH = 10000

_SPACE = b" \t\r\n"


def _go_replace(e: UnicodeDecodeError) -> tuple[str, int]:
    # Go's utf8.DecodeRune: each byte of an invalid sequence is one U+FFFD.
    return "\ufffd", e.start + 1


codecs.register_error("evidencebook-go-utf8", _go_replace)


def go_utf8(b: bytes) -> str:
    """``b`` as Go reads text into a string: invalid UTF-8, a byte at a
    time, as U+FFFD."""
    return b.decode("utf-8", "evidencebook-go-utf8")


def go_unquote(b: bytes) -> str:
    """A JSON string literal as Go decodes it into a string."""
    return json.loads(go_utf8(b))


class ScanError(ValueError):
    """Text Go's scanner refuses."""


@dataclass
class Scanned:
    """What :func:`scan` read: the end of the first value, and the
    top-level object's members (each its name and the start and end of its
    value) when asked for."""

    end: int
    members: list[tuple[str, int, int]] = field(default_factory=list)


def _space(c: int) -> bool:
    return c in (0x20, 0x09, 0x0D, 0x0A)


def _skip(b: bytes, i: int) -> int:
    while i < len(b) and _space(b[i]):
        i += 1
    return i


def _string_end(b: bytes, i: int) -> int:
    """The index after the string starting at ``b[i] == '"'``."""
    i += 1
    n = len(b)
    while i < n:
        c = b[i]
        if c == 0x22:
            return i + 1
        if c == 0x5C:
            if i + 1 >= n:
                break
            e = b[i + 1]
            if e in b'bfnrt\\/"':
                i += 2
                continue
            if e == 0x75:
                h = b[i + 2:i + 6]
                if len(h) < 4:
                    break
                if not all(x in b"0123456789abcdefABCDEF" for x in h):
                    raise ScanError("invalid character in \\u hexadecimal character escape")
                i += 6
                continue
            raise ScanError("invalid character in string escape code")
        if c < 0x20:
            raise ScanError("invalid character in string literal")
        i += 1
    raise ScanError("unexpected end of JSON input")


def _number_end(b: bytes, i: int) -> int:
    """The index after the number starting at ``b[i]``; Go's number
    grammar."""
    n = len(b)
    if b[i] == 0x2D:
        i += 1
        if i >= n:
            raise ScanError("unexpected end of JSON input")
    if b[i] == 0x30:
        i += 1
    elif 0x31 <= b[i] <= 0x39:
        while i < n and 0x30 <= b[i] <= 0x39:
            i += 1
    else:
        raise ScanError("invalid character in numeric literal")
    if i < n and b[i] == 0x2E:
        i += 1
        if i >= n:
            raise ScanError("unexpected end of JSON input")
        if not 0x30 <= b[i] <= 0x39:
            raise ScanError("invalid character after decimal point in numeric literal")
        while i < n and 0x30 <= b[i] <= 0x39:
            i += 1
    if i < n and b[i] in (0x65, 0x45):
        i += 1
        if i < n and b[i] in (0x2B, 0x2D):
            i += 1
        if i >= n:
            raise ScanError("unexpected end of JSON input")
        if not 0x30 <= b[i] <= 0x39:
            raise ScanError("invalid character in exponent of numeric literal")
        while i < n and 0x30 <= b[i] <= 0x39:
            i += 1
    return i


def _literal_end(b: bytes, i: int) -> int:
    for word in (b"true", b"false", b"null"):
        if b[i] == word[0]:
            for k, ch in enumerate(word):
                if i + k >= len(b):
                    raise ScanError("unexpected end of JSON input")
                if b[i + k] != ch:
                    raise ScanError("invalid character in literal")
            return i + len(word)
    raise ScanError("invalid character looking for beginning of value")


def scan(b: bytes, i: int = 0, *, depth: int = MAX_DEPTH, tokens: bool = False, members: bool = False) -> Scanned:
    """Reads the first JSON value at ``b[i:]`` (after white space) as Go's
    ``Decoder`` does. With ``tokens``, also refuses a member given twice in
    one object and a number that overflows a float64 (Go's ``Token`` walk);
    with ``members``, records the top-level object's members."""
    i = _skip(b, i)
    if i >= len(b):
        raise ScanError("unexpected end of JSON input")
    out = Scanned(0)
    # The stack holds, per open container, its kind and, for an object, the
    # names it has seen (with tokens) and whether a name or a value is next.
    stack: list[list] = []
    expect_value = True
    while True:
        if expect_value:
            i = _skip(b, i)
            if i >= len(b):
                raise ScanError("unexpected end of JSON input")
            c = b[i]
            start = i
            if c in (0x7B, 0x5B):
                if len(stack) + 1 > depth:
                    raise ScanError("exceeded max depth")
                stack.append(["{" if c == 0x7B else "[", set(), start])
                i = _skip(b, i + 1)
                if i < len(b) and b[i] == (0x7D if c == 0x7B else 0x5D):
                    stack.pop()
                    i += 1
                    expect_value = False
                    if members and len(stack) == 1 and stack[0][0] == "{" and out.members and out.members[-1][2] == -1:
                        out.members[-1] = (out.members[-1][0], start, i)
                    if not stack:
                        out.end = i
                        return out
                    continue
                if c == 0x7B:
                    i = _read_key(b, i, stack, tokens, out if members and len(stack) == 1 else None)
                    continue
                continue
            if c == 0x22:
                i = _string_end(b, i)
            elif c == 0x2D or 0x30 <= c <= 0x39:
                i = _number_end(b, i)
                if tokens:
                    v = float(b[start:i])
                    if v in (float("inf"), float("-inf")):
                        raise ScanError(f"json: cannot unmarshal number {b[start:i].decode()} into Go value of type float64")
            else:
                i = _literal_end(b, i)
            if members and len(stack) == 1 and stack[0][0] == "{" and out.members and out.members[-1][2] == -1:
                name, _, _ = out.members[-1]
                out.members[-1] = (name, start, i)
            if not stack:
                out.end = i
                return out
            expect_value = False
            continue
        # After a value inside a container: a comma or its close.
        i = _skip(b, i)
        if i >= len(b):
            raise ScanError("unexpected end of JSON input")
        top = stack[-1]
        c = b[i]
        if c == 0x2C:
            i += 1
            if top[0] == "{":
                i = _read_key(b, _skip(b, i), stack, tokens, out if members and len(stack) == 1 else None)
            expect_value = True
            continue
        if c == (0x7D if top[0] == "{" else 0x5D):
            stack.pop()
            i += 1
            if members and len(stack) == 1 and stack[0][0] == "{" and out.members and out.members[-1][2] == -1:
                name, _, _ = out.members[-1]
                out.members[-1] = (name, top[2], i)
            if not stack:
                out.end = i
                return out
            continue
        raise ScanError("invalid character after array element" if top[0] == "[" else "invalid character after object key:value pair")


def _read_key(b: bytes, i: int, stack: list[list], tokens: bool, out: Scanned | None) -> int:
    """Reads an object member's name and its colon; returns the index of
    its value."""
    if i >= len(b):
        raise ScanError("unexpected end of JSON input")
    if b[i] != 0x22:
        raise ScanError("invalid character looking for beginning of object key string")
    end = _string_end(b, i)
    name = None
    if tokens or out is not None:
        name = go_unquote(b[i:end])
    if tokens:
        seen = stack[-1][1]
        if name in seen:
            raise ScanError(f'member "{name}" given twice')
        seen.add(name)
    i = _skip(b, end)
    if i >= len(b):
        raise ScanError("unexpected end of JSON input")
    if b[i] != 0x3A:
        raise ScanError("invalid character after object key")
    if out is not None:
        out.members.append((name, -1, -1))
    return i + 1


def valid(b: bytes, depth: int = MAX_DEPTH) -> bool:
    """Go's ``json.Valid``: one value, white space around it."""
    try:
        end = scan(b, 0, depth=depth).end
    except ScanError:
        return False
    return _skip(b, end) == len(b)


def more(b: bytes, i: int) -> bool:
    """Go's ``Decoder.More`` after a value ending at ``i``: another byte
    follows that is not white space, ``]`` or ``}``."""
    i = _skip(b, i)
    return i < len(b) and b[i] not in (0x5D, 0x7D)
