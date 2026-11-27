# SPDX-License-Identifier: Apache-2.0
"""The authenticated indexes a book keeps by default, record_id, subject_ref
and subject_id (the subject convention v0, :mod:`evidencebook.subject`), as
sorted Merkle trees (RFC 6962 shape) over ``(key, record id)`` leaves. A book
commits each changed root as an ``index_root`` record before it checkpoints,
so the roots are part of its log: they are computed exactly as Go's
``SortedMerkleIndex`` computes them. A lazy index (subject_id) has its root
committed only once it holds a leaf. :meth:`SortedMerkleIndex.prove_range`
and :func:`verify_key_range` are Go's key range proof and its check; a book
serves no proof over the wire from this package.
"""
from __future__ import annotations

import bisect
import hashlib
import re
from collections.abc import Callable
from typing import Any

from .errors import InvalidError
from .record import Record


_HEX = re.compile(r"(?:[0-9a-fA-F]{2})*")


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


def _audit_path(m: int, leaves: list[bytes]) -> list[bytes]:
    """The RFC 9162 inclusion path PATH(m, D[n])."""
    if len(leaves) <= 1:
        return []
    k = _split(len(leaves))
    if m < k:
        return _audit_path(m, leaves[:k]) + [_merkle_root(leaves[k:])]
    return _audit_path(m - k, leaves[k:]) + [_merkle_root(leaves[:k])]


def _verify_audit_path(index: int, size: int, leaf: bytes, path: list[bytes], root: bytes) -> bool:
    """RFC 9162 §2.1.3.2 inclusion verification."""
    if index >= size:
        return False
    fn, sn = index, size - 1
    r = leaf
    for p in path:
        if sn == 0:
            return False
        if fn & 1 == 1 or fn == sn:
            r = _interior(p, r)
            while fn & 1 == 0 and fn != 0:
                fn >>= 1
                sn >>= 1
        else:
            r = _interior(r, p)
        fn >>= 1
        sn >>= 1
    return sn == 0 and r == root


class SortedMerkleIndex:
    """A sorted list of ``(key, record id)`` leaves under an RFC 9162 Merkle
    tree. ``lazy``: a checkpoint commits its root only once it holds a leaf
    (Go ``lazyIndex``)."""

    def __init__(self, name: str, key_of: Callable[[Record], str | None], *, lazy: bool = False):
        self.name = name
        self._key_of = key_of
        self.lazy = lazy
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

    def _proof(self, hashes: list[bytes], i: int) -> dict:
        k, r = self._leaves[i]
        return {"key": k.decode("utf-8"), "record_id": r.decode("utf-8"), "index": i,
                "path": [p.hex() for p in _audit_path(i, hashes)]}

    def prove_range(self, lo: str, hi: str) -> dict:
        """The proof of exactly which records carry a key in ``[lo, hi]``, as
        Go's ``ProveRange`` writes it (a ``KeyRangeProof`` JSON value)."""
        lo_b, hi_b = lo.encode("utf-8"), hi.encode("utf-8")
        if lo_b > hi_b:
            raise InvalidError("range lower bound exceeds upper bound")
        hashes = [_leaf_hash(k.decode("utf-8"), r.decode("utf-8")) for k, r in self._leaves]
        first = next((i for i, (k, _) in enumerate(self._leaves) if k >= lo_b), len(self._leaves))
        end = next((i for i, (k, _) in enumerate(self._leaves) if k > hi_b), len(self._leaves))
        proof: dict[str, Any] = {"lo": lo, "hi": hi, "first": first,
                                 "matches": [self._proof(hashes, i) for i in range(first, end)]}
        if first > 0:
            proof["left"] = self._proof(hashes, first - 1)
        if end < len(self._leaves):
            proof["right"] = self._proof(hashes, end)
        return proof


def _leaf_proof(value: Any, what: str) -> dict:
    """A ``LeafProof`` JSON value, its members typed as Go decodes them."""
    if not isinstance(value, dict):
        raise InvalidError(f"{what} is not a leaf proof")
    key, record_id, index, path = value.get("key", ""), value.get("record_id", ""), value.get("index", 0), value.get("path")
    if not isinstance(key, str) or not isinstance(record_id, str) or isinstance(index, bool) or \
            not isinstance(index, int) or index < 0 or not (path is None or isinstance(path, list)):
        raise InvalidError(f"{what} is not a leaf proof")
    return {"key": key, "record_id": record_id, "index": index, "path": path or []}


def verify_leaf(root: dict, proof: dict) -> None:
    """Check one leaf proof against a committed root (Go ``VerifyLeaf``)."""
    try:
        expected = bytes.fromhex(root["root"]) if isinstance(root.get("root"), str) else None
    except ValueError:
        expected = None
    if expected is None or not _HEX.fullmatch(root["root"]):
        raise InvalidError("index root is not hex")
    path = []
    for i, node in enumerate(proof["path"]):
        if not isinstance(node, str) or not _HEX.fullmatch(node) or len(node) != 64:
            raise InvalidError(f"audit path node {i}")
        path.append(bytes.fromhex(node))
    leaf = _leaf_hash(proof["key"], proof["record_id"])
    if not _verify_audit_path(proof["index"], root["size"], leaf, path, expected):
        raise InvalidError(f"leaf {proof['index']} does not verify under index root")


def verify_key_range(root: dict, lo: str, hi: str, proof: Any) -> list[str]:
    """Check a key range proof against a committed root and return the record
    ids it proves are exactly the leaves with keys in ``[lo, hi]`` (Go
    ``VerifyKeyRange``, check for check). ``root`` is an ``IndexRoot`` value
    (``index``, ``root``, ``size``)."""
    lo_b, hi_b = lo.encode("utf-8"), hi.encode("utf-8")
    if not isinstance(proof, dict) or lo_b > hi_b or proof.get("lo", "") != lo or proof.get("hi", "") != hi:
        raise InvalidError("proof is for a different range")
    first, raw_matches = proof.get("first", 0), proof.get("matches")
    if isinstance(first, bool) or not isinstance(first, int) or first < 0 or not (raw_matches is None or isinstance(raw_matches, list)):
        raise InvalidError("proof is not a key range proof")
    matches = [_leaf_proof(m, f"match {i}") for i, m in enumerate(raw_matches or [])]
    ids = []
    for i, match in enumerate(matches):
        key = match["key"].encode("utf-8")
        if match["index"] != first + i or key < lo_b or key > hi_b:
            raise InvalidError(f"match {i} is out of place or out of range")
        if i > 0 and not _utf8_key((matches[i - 1]["key"], matches[i - 1]["record_id"])) < _utf8_key((match["key"], match["record_id"])):
            raise InvalidError("matches are not strictly ordered")
        verify_leaf(root, match)
        ids.append(match["record_id"])
    left = None if proof.get("left") is None else _leaf_proof(proof["left"], "left neighbour")
    right = None if proof.get("right") is None else _leaf_proof(proof["right"], "right neighbour")
    if first == 0:
        if left is not None:
            raise InvalidError("left neighbour at tree edge")
    else:
        if left is None or left["index"] != first - 1 or left["key"].encode("utf-8") >= lo_b:
            raise InvalidError("left neighbour missing or inside range")
        verify_leaf(root, left)
    end = first + len(matches)
    size = root["size"]
    if end > size:
        raise InvalidError("range runs past the tree")
    if end == size:
        if right is not None:
            raise InvalidError("right neighbour at tree edge")
    else:
        if right is None or right["index"] != end or right["key"].encode("utf-8") <= hi_b:
            raise InvalidError("right neighbour missing or inside range")
        verify_leaf(root, right)
    return ids


def default_indexes() -> list[SortedMerkleIndex]:
    """record_id, subject_ref and subject_id, as Go's ``Open`` defaults them."""
    from .subject import new_subject_id_index

    return [
        SortedMerkleIndex("record_id", lambda r: r.record_id),
        SortedMerkleIndex("subject_ref", lambda r: r.header.subject_ref or None),
        new_subject_id_index(),
    ]
