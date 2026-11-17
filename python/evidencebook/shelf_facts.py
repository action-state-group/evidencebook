# SPDX-License-Identifier: Apache-2.0
"""The entry documents of a shelf log of book facts (evidencebook#461).

Byte for byte with Go's ``shelf_facts.go`` and the Rust crate's
``shelf_facts``: after the format-switch marker every shelf log entry is the
SHA-256 of one document's RFC 8785 bytes; a fact commits to its link and
status values as ``hex(SHA-256(salt || JCS(value)))``, each under its own
32-byte salt, and an opening ``{salt, value}`` shows one value
(docs/design/461-shelf-facts.md section 2). ``testdata/shelf/facts`` holds
the shared vectors.
"""

from __future__ import annotations

import hashlib
import hmac
import json
from dataclasses import dataclass
from typing import Any, Optional

from agent_action_capsule.canonical import jcs

__all__ = [
    "ENTRY_KIND_FORMAT_SWITCH",
    "ENTRY_KIND_STAMP",
    "FACT_KIND_BOOK",
    "FACT_KIND_JOURNAL",
    "Fact",
    "Invalid",
    "Opening",
    "book_fact",
    "commit",
    "entry_id",
    "format_switch",
    "journal_fact",
    "openings_for",
    "stamp_entry",
]

FACT_KIND_BOOK = "book_fact"
FACT_KIND_JOURNAL = "journal_fact"
ENTRY_KIND_STAMP = "stamp"
ENTRY_KIND_FORMAT_SWITCH = "format_switch"
SHELF_FACTS_FORMAT = "book_facts/1"
PROFILE_NONE = "none"


class Invalid(ValueError):
    """An input or opening the construction cannot take."""


@dataclass(frozen=True)
class Opening:
    salt: str
    value: Any

    def to_json(self) -> dict[str, Any]:
        return {"salt": self.salt, "value": self.value}

    def verify(self, commitment: str) -> None:
        try:
            salt = bytes.fromhex(self.salt)
        except ValueError:
            raise Invalid("an opening's salt is hex") from None
        if len(salt) != 32 or salt.hex() != self.salt:
            raise Invalid("an opening's salt is 32 bytes in lowercase hex")
        if not hmac.compare_digest(commit(salt, self.value)[0], commitment):
            raise Invalid("the opening does not recompute the commitment")


def commit(salt: bytes, value: Any) -> tuple[str, Opening]:
    """``hex(SHA-256(salt || JCS(value)))`` and the opening."""
    if len(salt) != 32:
        raise Invalid("a commitment's salt is 32 bytes")
    body = jcs(value)
    return hashlib.sha256(salt + body).hexdigest(), Opening(salt.hex(), json.loads(body))


@dataclass(frozen=True)
class Fact:
    doc: dict[str, Any]
    link_opening: Opening
    status_opening: Opening


def _link(last_received: Optional[dict[str, Any]], checked_over: list[str]) -> dict[str, Any]:
    over = sorted(checked_over)
    if len(set(over)) != len(over):
        raise Invalid("a digest checked over twice")
    return {"last_received": last_received, "checked_over": over}


def book_fact(
    *,
    profile: str,
    book_id: str,
    log_id: str,
    size: int,
    checkpoint_sha256: str,
    prev_fact_size: int,
    last_received: Optional[dict[str, Any]] = None,
    checked_over: Optional[list[str]] = None,
    status: Optional[dict[str, Any]] = None,
    salts: tuple[bytes, bytes],
) -> Fact:
    """A book fact; ``salts`` is the link salt, then the status salt."""
    if not (profile and book_id and log_id and checkpoint_sha256):
        raise Invalid("a fact needs its profile, book, log and checkpoint")
    if size < prev_fact_size:
        raise Invalid("a fact's size is below its previous fact's")
    status = {} if status is None else status
    if not isinstance(status, dict):
        raise Invalid("a fact's status is a JSON object")
    lc, lo = commit(salts[0], _link(last_received, checked_over or []))
    sc, so = commit(salts[1], status)
    doc = {"v": 1, "kind": FACT_KIND_BOOK, "profile": profile, "book_id": book_id, "log_id": log_id,
           "size": size, "checkpoint_sha256": checkpoint_sha256, "prev_fact_size": prev_fact_size,
           "link_commitment": lc, "status_commitment": sc}
    return Fact(doc, lo, so)


def journal_fact(log_id: str, size: int, checkpoint_sha256: str, prev_fact_size: int, salts: tuple[bytes, bytes]) -> Fact:
    """The journal's fact: book id SHA-256(log id); its sizes and digest only in its status."""
    if not (log_id and checkpoint_sha256) or size < prev_fact_size:
        raise Invalid("a journal fact needs its log, checkpoint and sizes")
    lc, lo = commit(salts[0], _link(None, []))
    sc, so = commit(salts[1], {"size": size, "checkpoint_sha256": checkpoint_sha256, "prev_fact_size": prev_fact_size})
    doc = {"v": 1, "kind": FACT_KIND_JOURNAL, "profile": PROFILE_NONE,
           "book_id": hashlib.sha256(log_id.encode()).hexdigest(), "log_id": log_id, "size": 0,
           "checkpoint_sha256": "", "prev_fact_size": 0, "link_commitment": lc, "status_commitment": sc}
    return Fact(doc, lo, so)


def stamp_entry(stamp: int, facts: int) -> dict[str, Any]:
    """The entry that ends every stamp."""
    return {"v": 1, "kind": ENTRY_KIND_STAMP, "stamp": stamp, "facts": facts}


def format_switch(last_tick: int, logs: list[tuple[str, int]]) -> dict[str, Any]:
    """The migration's marker, its logs sorted by log id (UTF-8 byte order)."""
    ordered = sorted(logs, key=lambda x: x[0].encode())
    return {"v": 1, "kind": ENTRY_KIND_FORMAT_SWITCH, "format": SHELF_FACTS_FORMAT, "last_tick": last_tick,
            "logs": [{"log_id": i, "size": s} for i, s in ordered]}


def entry_id(doc: dict[str, Any]) -> tuple[bytes, bytes]:
    """A shelf log entry: SHA-256 of the document's RFC 8785 bytes, and those bytes."""
    body = jcs(doc)
    return hashlib.sha256(body).digest(), body


_BOOK = {"counterparty": (True, True), "owner": (True, True), "adjudicator": (True, False),
         "asker": (True, False), "chosen_reader": (False, True), "auditor": (False, False)}
_JOURNAL = {"owner": (True, True), "auditor": (True, True), "counterparty": (False, False),
            "adjudicator": (False, False), "asker": (False, False), "chosen_reader": (False, False)}


def openings_for(kind: str, audience: str) -> tuple[bool, bool]:
    """Which of a fact's openings an audience may have: (link, status); see Go's ``OpeningsFor``."""
    table = {FACT_KIND_BOOK: _BOOK, FACT_KIND_JOURNAL: _JOURNAL}.get(kind)
    if table is None:
        raise Invalid(f"no openings for {kind!r}")
    if audience not in table:
        raise Invalid(f"unknown audience {audience!r}")
    return table[audience]
