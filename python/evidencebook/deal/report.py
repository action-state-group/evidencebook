# SPDX-License-Identifier: Apache-2.0
"""Sealed reports (PROFILE.md §1, "Sealed reports are not steps"), as Go's
``dealprofile/report.go`` lays them out.

A sealed report seals one copy's readable deal text as one more Capsule:
action_id ``REPORT_ACTION_ID``, fyi, not chained, with the payload
``{"type": "deal_report", "profile", "deal_id", "audience", "nonce",
"report"}``. A standalone report, appended to no log, adds
``"deal_checkpoint"``, the deal checkpoint the copy is made at. What the
report's lines say is the producer's.
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from agent_action_capsule.canonical import jcs

from .capsule import ACTION_TYPE_FYI, Capsule, rfc3339_seconds
from .check import _go_quote, decode
from .names import (
    AUDIENCE_ADJUDICATOR, AUDIENCE_COUNTERPARTY, AUDIENCE_KEEP, PROFILE, REPORT_ACTION_ID, REPORT_TYPE,
)

__all__ = ["Checkpoint", "Report", "report_payload", "report_capsule"]

_NONCE_HEX = re.compile(r"[0-9a-f]{64}\Z")


@dataclass(frozen=True)
class Checkpoint:
    """The deal checkpoint a standalone report binds."""
    log_id: str
    mmr_size: int
    root: str


@dataclass(frozen=True)
class Report:
    """A sealed report's payload before it is encoded."""
    deal_id: str
    audience: str
    #: A fresh 256-bit random value (64 lowercase hex), so the Capsule's digest
    #: confirms no guess at the text.
    nonce: str
    #: The copy's deal text, already cut for its audience.
    body: dict[str, Any]
    #: Set for a standalone report only.
    checkpoint: Checkpoint | None = None


def _whole_floats_as_integers(v: Any) -> Any:
    """Go writes the body as JSON and reads its numbers back, and a float64
    that is a whole number writes as an integer (``1.0`` is ``1``); a
    fraction, or a whole number beyond ±2^53, stays a float, which JCS
    refuses. Numbers read from JSON text (:func:`decode`) are kept as
    written, so ``1.0`` there is a float, as Go's ``json.Number`` is."""
    if isinstance(v, float) and type(v) is float and v.is_integer() and abs(v) < 2 ** 53:
        return int(v)
    if isinstance(v, dict):
        return {k: _whole_floats_as_integers(x) for k, x in v.items()}
    if isinstance(v, (list, tuple)):
        return [_whole_floats_as_integers(x) for x in v]
    return v


def report_payload(r: Report) -> bytes:
    """A report's payload: its JCS bytes."""
    if r.audience not in (AUDIENCE_KEEP, AUDIENCE_COUNTERPARTY, AUDIENCE_ADJUDICATOR):
        raise ValueError(f"dealprofile: report audience {_go_quote(r.audience)} is not a copy audience")
    if not _NONCE_HEX.match(r.nonce):
        raise ValueError("dealprofile: report nonce must be 64 lowercase hex")
    m: dict[str, Any] = {
        "type": REPORT_TYPE, "profile": PROFILE, "deal_id": r.deal_id, "audience": r.audience,
        "nonce": r.nonce, "report": _whole_floats_as_integers(r.body),
    }
    if r.checkpoint is not None:
        c = r.checkpoint
        m["deal_checkpoint"] = {"log_id": c.log_id, "mmr_size": c.mmr_size, "root": c.root}
    return jcs(decode(m))


def report_capsule(operator: str, developer: str, at: datetime) -> Capsule:
    """A report's Capsule members, sealed at ``at`` (to the second) by
    ``operator``, naming the producer's own developer (the capsulectl deal
    plugin names "capsulectl-deal"). The producer seals it with the payload as
    JSON and its own key; on a book, the record header it carries is
    ``REPORT_RECORD_TYPE``, with no subject_ref, link or field commitment. An
    empty developer is refused."""
    if not developer:
        raise ValueError("dealprofile: a report needs the producer's developer name")
    return Capsule(REPORT_ACTION_ID, ACTION_TYPE_FYI, operator, developer, rfc3339_seconds(at))
