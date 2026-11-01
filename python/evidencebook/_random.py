# SPDX-License-Identifier: Apache-2.0
"""A shelf's one random source (evidencebook#254 §5).

Every salt, tree position, filler nonce and seed, jitter and padding nonce a
cadence draws comes from one source, and every draw is defined on the bytes
it reads, so another implementation fed the same stream draws the same
values (testdata/shelf/README.md):

- ``bytes(n)`` reads ``n`` bytes;
- ``uniform(n)`` reads 8 bytes as a big-endian ``x``, again while ``x`` is at
  or above the largest multiple of ``n`` not over 2^64, and returns
  ``x mod n``;
- a tree position is ``uniform(2^16)``, drawn again while it is taken.

Production reads ``os.urandom``; :func:`deterministic_stream` is the stream
the vectors and the storage interlock inject, Go's ``internal/detrand``.
"""
from __future__ import annotations

import hashlib
import os
from collections.abc import Callable

from .errors import InvalidError

__all__ = ["RandomSource", "deterministic_stream"]

Reader = Callable[[int], bytes]


def deterministic_stream(seed: str) -> Reader:
    """The stream whose block ``i`` (from 0) is ``SHA-256(UTF8(seed) ‖
    uint64be(i))``, as a reader of ``n`` bytes at a time. For tests only."""
    prefix = seed.encode("utf-8")
    state = {"block": 0, "buf": b""}

    def read(n: int) -> bytes:
        out = bytearray()
        while len(out) < n:
            if not state["buf"]:
                state["buf"] = hashlib.sha256(prefix + state["block"].to_bytes(8, "big")).digest()
                state["block"] += 1
            take = state["buf"][:n - len(out)]
            state["buf"] = state["buf"][len(take):]
            out += take
        return bytes(out)

    return read


class RandomSource:
    """Draws from ``reader`` (``os.urandom`` when ``None``)."""

    def __init__(self, reader: Reader | None = None):
        self._read = reader or os.urandom

    def bytes(self, n: int) -> bytes:
        data = self._read(n)
        if len(data) != n:
            raise InvalidError("the shelf's random source returned too few bytes")
        return data

    def hex(self, n: int) -> str:
        return self.bytes(n).hex()

    def uniform(self, n: int) -> int:
        """A uniform draw in ``[0, n)``, ``0 < n < 2^64 + 1``."""
        if n <= 0:
            raise InvalidError("a uniform draw needs a positive bound")
        limit = (1 << 64) - (1 << 64) % n
        while True:
            x = int.from_bytes(self.bytes(8), "big")
            if x < limit:
                return x % n
