# SPDX-License-Identifier: Apache-2.0
"""A CBOR reader for received checkpoint statements.

It accepts every well-formed encoding RFC 8949 allows a decoder to accept
(indefinite lengths, non-preferred heads, any map order) and refuses what
the checkpoint needs refused: a map key repeated (RFC 8949 §5.6; RFC 9052
§3 forbids parsing a repeated header label) or of a type other than an
integer or a text or byte string, and trailing octets. Neither cbor2 (which
keeps the last of repeated keys) nor agent-action-capsule's decoder (which
refuses indefinite lengths) reads it that way.
"""
from __future__ import annotations

import struct
from dataclasses import dataclass
from typing import Any

_MAX_DEPTH = 64


class CborError(ValueError):
    pass


@dataclass(frozen=True)
class Tag:
    number: int
    value: Any


_BREAK = object()


class _Reader:
    def __init__(self, data: bytes):
        self.data, self.i = data, 0

    def byte(self) -> int:
        if self.i >= len(self.data):
            raise CborError("truncated")
        self.i += 1
        return self.data[self.i - 1]

    def take(self, n: int) -> bytes:
        if self.i + n > len(self.data):
            raise CborError("truncated")
        self.i += n
        return self.data[self.i - n:self.i]

    def argument(self, info: int) -> int | None:
        if info < 24:
            return info
        if info in (24, 25, 26, 27):
            return int.from_bytes(self.take(1 << (info - 24)), "big")
        if info == 31:
            return None  # indefinite length
        raise CborError("reserved additional information")

    def item(self, depth: int = 0) -> Any:
        if depth > _MAX_DEPTH:
            raise CborError("nesting too deep")
        initial = self.byte()
        major, info = initial >> 5, initial & 31
        if major == 7:
            if info == 31:
                return _BREAK
            if info in (25, 26, 27):
                width = 1 << (info - 24)
                return struct.unpack(">" + {2: "e", 4: "f", 8: "d"}[width], self.take(width))[0]
            value = info if info < 24 else self.argument(info)
            return {20: False, 21: True, 22: None}.get(value, Tag(-1, value))  # other simple values
        arg = self.argument(info)
        if major == 0 and arg is not None:
            return arg
        if major == 1 and arg is not None:
            return -1 - arg
        if major in (2, 3):
            if arg is not None:
                raw = self.take(arg)
            else:
                chunks = []
                while True:
                    chunk = self.item(depth + 1)
                    if chunk is _BREAK:
                        break
                    if type(chunk) is not (bytes if major == 2 else str):
                        raise CborError("an indefinite string holds a chunk of another type")
                    chunks.append(chunk.encode("utf-8") if major == 3 else chunk)
                raw = b"".join(chunks)
            if major == 2:
                return bytes(raw)
            try:
                return raw.decode("utf-8")
            except UnicodeDecodeError:
                raise CborError("text is not UTF-8") from None
        if major == 4:
            out = []
            while arg is None or len(out) < arg:
                value = self.item(depth + 1)
                if value is _BREAK:
                    if arg is not None:
                        raise CborError("unexpected break")
                    break
                out.append(value)
            return out
        if major == 5:
            result: dict = {}
            seen: set = set()
            while arg is None or len(seen) < arg:
                key = self.item(depth + 1)
                if key is _BREAK:
                    if arg is not None:
                        raise CborError("unexpected break")
                    break
                if type(key) not in (int, str, bytes):
                    raise CborError("a map key is not an integer or a text or byte string")
                if (type(key), key) in seen:
                    raise CborError(f"map key {key!r} is repeated")
                seen.add((type(key), key))
                value = self.item(depth + 1)
                if value is _BREAK:
                    raise CborError("a map ends between a key and its value")
                result[key] = value
            return result
        if major == 6 and arg is not None:
            return Tag(arg, self.item(depth + 1))
        raise CborError("malformed head")


def read(data: bytes) -> Any:
    """Exactly one data item; raises :class:`CborError` otherwise."""
    reader = _Reader(bytes(data))
    value = reader.item()
    if value is _BREAK:
        raise CborError("a break outside an indefinite item")
    if reader.i != len(reader.data):
        raise CborError("trailing octets after the data item")
    return value
