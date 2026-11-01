# SPDX-License-Identifier: Apache-2.0
"""The public suffix list Go's ``dealprofile`` reads, by the same lookup.

A domain's fingerprint is taken over its registrable domain (eTLD+1), so the
Go, Rust and Python implementations must agree on every public suffix. The
tables in ``_data/publicsuffix`` are golang.org/x/net v0.59.0's
``publicsuffix/data`` files, byte for byte (the version the Go module
requires; publicsuffix.org's list at git revision
d6c92f1bbb7433e5db7b8405c25d4035fb8ff376), and the lookup is a port of its
``list.go``. Both are under the Go authors' BSD-3-Clause license
(``_data/publicsuffix/LICENSE``). Moving the Go module to another x/net
version means replacing these files with that version's.
"""
from __future__ import annotations

import ipaddress
from pathlib import Path

_DIR = Path(__file__).resolve().parent / "_data" / "publicsuffix"
_TEXT = (_DIR / "text").read_bytes()
_NODES = (_DIR / "nodes").read_bytes()
_CHILDREN = (_DIR / "children").read_bytes()

# x/net v0.59.0 publicsuffix/table.go.
_NODES_BITS_CHILDREN = 10
_NODES_BITS_ICANN = 1
_NODES_BITS_TEXT_OFFSET = 16
_NODES_BITS_TEXT_LENGTH = 6
_CHILDREN_BITS_WILDCARD = 1
_CHILDREN_BITS_NODE_TYPE = 2
_CHILDREN_BITS_HI = 14
_CHILDREN_BITS_LO = 14
_NODE_TYPE_NORMAL = 0
_NODE_TYPE_EXCEPTION = 1
_NUM_TLD = 1450


def _children(i: int) -> int:
    return int.from_bytes(_CHILDREN[i * 4:i * 4 + 4], "big")


def _node(i: int) -> int:
    return int.from_bytes(_NODES[i * 5:i * 5 + 5], "big")


def _node_label(i: int) -> bytes:
    x = _node(i)
    length = x & ((1 << _NODES_BITS_TEXT_LENGTH) - 1)
    offset = (x >> _NODES_BITS_TEXT_LENGTH) & ((1 << _NODES_BITS_TEXT_OFFSET) - 1)
    return _TEXT[offset:offset + length]


def _find(label: bytes, lo: int, hi: int) -> int | None:
    while lo < hi:
        mid = lo + (hi - lo) // 2
        got = _node_label(mid)
        if got < label:
            lo = mid + 1
        elif got == label:
            return mid
        else:
            hi = mid
    return None


def public_suffix(domain: str) -> str:
    """The public suffix of ``domain`` (Go ``publicsuffix.PublicSuffix``,
    without its ICANN flag, which nothing here reads)."""
    try:
        ipaddress.ip_address(domain)
        return domain
    except ValueError:
        pass
    d = domain.encode("utf-8")
    lo, hi = 0, _NUM_TLD
    s = d
    suffix = len(d)
    wildcard = False
    while True:
        dot = s.rfind(b".")
        after = 0 if dot < 0 else dot + 1
        if wildcard:
            suffix = after
        if lo == hi:
            break
        f = _find(s[after:], lo, hi)
        if f is None:
            break
        u = _node(f) >> (_NODES_BITS_TEXT_OFFSET + _NODES_BITS_TEXT_LENGTH)
        u >>= _NODES_BITS_ICANN
        u = _children(u & ((1 << _NODES_BITS_CHILDREN) - 1))
        lo = u & ((1 << _CHILDREN_BITS_LO) - 1)
        u >>= _CHILDREN_BITS_LO
        hi = u & ((1 << _CHILDREN_BITS_HI) - 1)
        u >>= _CHILDREN_BITS_HI
        node_type = u & ((1 << _CHILDREN_BITS_NODE_TYPE) - 1)
        if node_type == _NODE_TYPE_NORMAL:
            suffix = after
        elif node_type == _NODE_TYPE_EXCEPTION:
            suffix = 1 + len(s)
            break
        u >>= _CHILDREN_BITS_NODE_TYPE
        wildcard = bool(u & ((1 << _CHILDREN_BITS_WILDCARD) - 1))
        if dot < 0:
            break
        s = s[:dot]
    if suffix == len(d):
        # If no rules match, the prevailing rule is "*".
        return d[d.rfind(b".") + 1:].decode("utf-8")
    return d[suffix:].decode("utf-8")


def effective_tld_plus_one(domain: str) -> str | None:
    """The effective top-level domain plus one more label (Go
    ``publicsuffix.EffectiveTLDPlusOne``), or None where Go returns an
    error."""
    if domain.startswith(".") or domain.endswith(".") or ".." in domain:
        return None
    suffix = public_suffix(domain)
    if len(domain) <= len(suffix):
        return None
    i = len(domain) - len(suffix) - 1
    if domain[i] != ".":
        return None
    return domain[domain.rfind(".", 0, i) + 1:]
