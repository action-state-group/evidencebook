# SPDX-License-Identifier: Apache-2.0
"""A sent journal record (Go ``shelf_sent.go``; evidencebook#461, design
§3.1): a journal record that leaves the shelf whole, such as an offer, must
not show its place in the journal, which a book record's Capsule does. So
it is sealed twice, in one call: as a journal record whose one payload
commitment is the SHA-256 of the record's bytes, and as a standalone
Capsule, appended to no log, that carries the same bytes as its payload
under the caller's ``action_id``, ``fyi``, with no chain, signed by the
journal's key. The standalone Capsule is what travels."""

from __future__ import annotations

import json
from dataclasses import dataclass, replace
from datetime import timezone
from typing import Any

from ._capsule import seal_payload
from .book import RECORD_HEADER_MEMBER, _detached
from .errors import InvalidError, ReleasedError


@dataclass(frozen=True)
class SentCapsule:
    """A sent record's standalone Capsule (Go ``SentCapsule``): its id, its
    JCS bytes, its Producer Envelope and the payload it commits."""

    capsule_id: str
    capsule: bytes
    envelope: bytes
    payload: bytes


@dataclass(frozen=True)
class SealedSent:
    """A sent record in the journal and its Capsule. ``error`` is the log
    append that failed after the record was stored: the record is in the
    journal all the same (its append is retried first), so the caller sends
    the Capsule and does not seal the record again."""

    record: Any
    capsule: SentCapsule
    error: Exception | None = None


def seal_sent(shelf: Any, entry: Any, members: dict, action_id: str) -> SealedSent:
    """Go ``Shelf.SealSent``; see :meth:`evidencebook.shelf.Shelf.seal_sent`."""
    if not action_id:
        raise InvalidError("a sent record needs an action_id")
    if len(entry.payloads) != 1:
        raise InvalidError("a sent record carries exactly one payload, its record")
    if RECORD_HEADER_MEMBER in members:
        raise InvalidError(f"a sent Capsule carries no {RECORD_HEADER_MEMBER}")
    payload = bytes(entry.payloads[0])
    from agent_action_capsule.canonical import jcs

    try:
        canonical = jcs(json.loads(payload))
    except (ValueError, TypeError) as exc:
        raise InvalidError(f"a sent record's payload is JSON: {exc}") from None
    if canonical != payload:
        raise InvalidError("a sent record's payload is its canonical JSON (JCS) bytes")
    journal = shelf.journal()
    with journal._lock:
        if journal._released:
            raise ReleasedError("the book is released")
        # An earlier record left pending is committed first, apart from this
        # one: its failure is raised, and what follows classifies this
        # record's append alone.
        journal._flush_pending()
        at = journal._now().astimezone(timezone.utc).replace(microsecond=0)
        try:
            capsule_id, capsule, envelope = seal_payload(
                action_id, payload, members, operator=journal._operator, developer=journal._developer, at=at,
                signer=journal._signer)
        except ValueError as exc:
            raise InvalidError(f"seal the sent record: {exc}") from None
        sent = SentCapsule(capsule_id, capsule, envelope, payload)
        pending_before = journal._pending
        size_before = len(journal._order)
        try:
            record = journal._append(replace(entry, payloads=(payload,)))
        except Exception as exc:
            pending = journal._pending
            if pending is not None and pending is not pending_before:
                # Stored, but its log append failed: the journal keeps it
                # pending and commits it first on its next write or open.
                return SealedSent(_detached(pending[0]), sent, exc)
            if len(journal._order) > size_before:
                # Committed, and a later step (its indexing) failed: the
                # record is in the journal, so it comes back with its Capsule.
                return SealedSent(_detached(journal._records[journal._order[-1]]), sent, exc)
            raise
    return SealedSent(record, sent)
