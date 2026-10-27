# SPDX-License-Identifier: Apache-2.0
"""The two authenticated indexes a book keeps by default, record_id and
subject_ref, as sorted Merkle trees (RFC 6962 shape) over ``(key, record
id)`` leaves. A book commits each changed root as an ``index_root`` record
before it checkpoints, so the roots are part of its log: they are computed
exactly as Go's ``SortedMerkleIndex`` computes them. Proofs over them are
not served by this package.
"""
from __future__ import annotations

import bisect
import hashlib
from collections.abc import Callable

from .record import Record


def _leaf_hash(key: str, record_id: str) -> bytes:
    k, r = key.encode("utf-8"), record_id.encode("utf-8")
    return hashlib.sha256(b"\x00" + len(k).to_bytes(4, "big") + k + len(r).to_bytes(4, "big") + r).digest()


def _interior(left: bytes, right: bytes) -> bytes:
    return hashlib.sha256(b"\x01" + left + right).digest()


def _split(n: int) -> int:
    k = 1
    while k << 1 < n:
        k <<= 1
    return k


def _merkle_root(leaves: list[bytes]) -> bytes:
    if not leaves:
        return hashlib.sha256(b"").digest()
    if len(leaves) == 1:
        return leaves[0]
    k = _split(len(leaves))
    return _interior(_merkle_root(leaves[:k]), _merkle_root(leaves[k:]))


def _utf8_key(leaf: tuple[str, str]) -> tuple[bytes, bytes]:
    # Go orders strings by their bytes.
    return leaf[0].encode("utf-8"), leaf[1].encode("utf-8")


class SortedMerkleIndex:
    def __init__(self, name: str, key_of: Callable[[Record], str | None]):
        self.name = name
        self._key_of = key_of
        self._leaves: list[tuple[bytes, bytes]] = []

    def add(self, record: Record) -> None:
        key = self._key_of(record)
        if key is None:
            return
        leaf = _utf8_key((key, record.record_id))
        at = bisect.bisect_left(self._leaves, leaf)
        if at < len(self._leaves) and self._leaves[at] == leaf:
            return
        self._leaves.insert(at, leaf)

    def root(self) -> dict:
        """The index root as an ``index_root`` statement names it."""
        hashes = [_leaf_hash(k.decode("utf-8"), r.decode("utf-8")) for k, r in self._leaves]
        return {"index": self.name, "root": _merkle_root(hashes).hex(), "size": len(self._leaves)}


def default_indexes() -> list[SortedMerkleIndex]:
    return [
        SortedMerkleIndex("record_id", lambda r: r.record_id),
        SortedMerkleIndex("subject_ref", lambda r: r.header.subject_ref or None),
    ]
