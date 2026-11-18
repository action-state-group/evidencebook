# SPDX-License-Identifier: Apache-2.0
"""Readers a profile calls from a stamp or a receive (evidencebook#461,
design §7.2 and §8.3), as Go's ``shelf_held.go`` gives them: the received
records held at a log's first position, the received records held for a
deal, and the stamp proofs held from a sender, each set against the
sender's counted prefix now. They take no lock of the shelf's."""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from . import shelf_facts
from .errors import CorruptError
from .shelf_receive import (
    _COLUMNS,
    _PROOF_CONTRADICTED,
    _PROOF_RECOMPUTED,
    _PROOF_UNCHECKED,
    HeldRecord,
    _parse,
    _prefix,
    _reader,
    _row,
)
from .shelf_stamp_proof import StampProof, VerifiedStampProof, verify_stamp_proof


@dataclass(frozen=True)
class HeldProof:
    """A stamp proof the store holds from a sender, as it stands against
    that sender's counted prefix now (Go ``HeldProof``): ``recomputed`` when
    the records held recomputed its book checkpoint's root,
    ``contradicted`` when they did not, and ``covers`` how many of the
    sender's currently counted records it covers (its book checkpoint's
    entries, capped to the counted prefix now, once recomputed; 0
    otherwise)."""

    proof: StampProof
    verified: VerifiedStampProof
    recomputed: bool
    contradicted: bool
    covers: int
    received_at: datetime


def _held(shelf: Any, clause: str, arg: str) -> list[HeldRecord]:
    # clause is one of the constant clauses below, never input.
    with _reader(shelf) as db:
        rows = db.execute(f"SELECT {_COLUMNS}, record, received_at FROM shelf_received WHERE {clause}",
                          (arg,)).fetchall()
    out = []
    for row in rows:
        received = _row(row).record()
        out.append(HeldRecord(received, bytes(row[7]), _parse(row[8], f"received record {received.digest}")))
    return out


def held_first(shelf: Any, log_id: str) -> list[HeldRecord]:
    """Every received record held at position 1 of a log with id ``log_id``,
    across senders and both records of a fork there, by sender key then
    digest (Go ``HeldFirst``)."""
    return _held(shelf, "log_id = ? AND position = 1 ORDER BY sender_key, digest", log_id)


def held_by_deal(shelf: Any, deal_id: str) -> list[HeldRecord]:
    """Every received record held for the deal ``deal_id``, across senders
    and logs, forks included, by sender key, log id, position and digest (Go
    ``HeldByDeal``)."""
    return _held(shelf, "deal_id = ? ORDER BY sender_key, log_id, position, digest", deal_id)


def held_proofs(shelf: Any, sender_key: str, log_id: str) -> list[HeldProof]:
    """Every stamp proof held from ``sender_key`` for ``log_id``, by size and
    then shelf checkpoint, each re-verified from the bytes kept and set
    against the sender's counted prefix in one read (Go ``HeldProofs``)."""

    def corrupt(what: str, e: object) -> CorruptError:
        return CorruptError(f"a held stamp proof of {log_id} from {sender_key}: {what}: {e}")

    # One read transaction, as Go's: the counted prefix and the proofs'
    # states are read from one snapshot of the store.
    with _reader(shelf) as db:
        db.execute("BEGIN")
        try:
            prefix = _prefix(db, sender_key, log_id)
            rows = db.execute(
                "SELECT book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, "
                "verified FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? "
                "ORDER BY size, shelf_checkpoint_sha256", (sender_key, log_id)).fetchall()
        finally:
            db.execute("ROLLBACK")
    out = []
    for book_checkpoint, fact, opening, inclusion, shelf_checkpoint, at, state in rows:
        status_opening = None
        if opening is not None:
            try:
                o = json.loads(bytes(opening))
                status_opening = shelf_facts.Opening(o["salt"], o["value"])
            except (ValueError, KeyError, TypeError) as e:
                raise corrupt("its status opening", e) from None
        try:
            inc = json.loads(bytes(inclusion))
        except ValueError as e:
            raise corrupt("its inclusion proof", e) from None
        proof = StampProof(fact=bytes(fact), book_checkpoint=bytes(book_checkpoint), inclusion=inc,
                           shelf_checkpoint=bytes(shelf_checkpoint), status_opening=status_opening)
        try:
            verified = verify_stamp_proof(proof)
        except Exception as e:  # noqa: BLE001 - any refusal of kept bytes is corruption
            raise corrupt("it no longer verifies", e) from None
        received_at = _parse(at, "a held stamp proof")
        recomputed = contradicted = False
        covers = 0
        if state == _PROOF_UNCHECKED:
            pass
        elif state == _PROOF_RECOMPUTED:
            recomputed = True
            covers = min(verified.book_checkpoint.entries, len(prefix.counted))
        elif state == _PROOF_CONTRADICTED:
            contradicted = True
        else:
            raise corrupt("its state", state)
        out.append(HeldProof(proof, verified, recomputed, contradicted, covers, received_at))
    return out
