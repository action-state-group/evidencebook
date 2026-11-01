# SPDX-License-Identifier: Apache-2.0
"""Sealed reports (PROFILE.md §1, "Sealed reports are not steps"), as Go's
``dealprofile/report.go`` lays them out.

A sealed report seals one copy's readable deal text as one more Capsule:
action_id ``REPORT_ACTION_ID``, fyi, not chained, with the payload
``{"type": "deal_report", "profile", "deal_id", "audience", "nonce",
"report"}``. A standalone report, appended to no log, adds
``"deal_checkpoint"``, the deal checkpoint the copy is made at, and an
adjudicator's copy adds ``"referee"``, the referee the party hands it to.
What the report's lines say is the producer's.
"""
from __future__ import annotations

import json
import re
import unicodedata
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from agent_action_capsule.canonical import jcs

from .capsule import ACTION_TYPE_FYI, Capsule, rfc3339_seconds
from .check import _go_quote, decode
from .keys import _trim
from .names import (
    AUDIENCE_ADJUDICATOR, AUDIENCE_COUNTERPARTY, AUDIENCE_KEEP, PROFILE, REPORT_ACTION_ID, REPORT_TYPE,
)

__all__ = [
    "Checkpoint", "Report", "report_payload", "report_capsule", "normalize_referee", "RefereeError", "ReportCopy",
    "report_referee", "shared_referee", "REFEREE_ONE_COPY", "REFEREE_MALFORMED", "REFEREE_NOT_ADJUDICATOR",
    "REFEREE_NOT_NAMED", "REFEREE_ONE_PARTY", "REFEREE_TWO_DEALS", "REFEREE_DIFFERENT",
]

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
    #: The referee an adjudicator's copy is handed to, as the party names it;
    #: required on that audience and refused on the others. It is sealed NFC
    #: and trimmed, so two parties who name the same referee seal the same
    #: bytes.
    referee: str = ""


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
    referee = normalize_referee(r.referee)
    if r.audience == AUDIENCE_ADJUDICATOR:
        if not referee:
            raise ValueError("dealprofile: an adjudicator's report names the referee it is handed to")
        m["referee"] = referee
    elif r.referee:
        raise ValueError(f"dealprofile: only an adjudicator's report names a referee, not a {r.audience} copy")
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


def normalize_referee(v: str) -> str:
    """A referee's name as a report seals it: NFC, with leading and trailing
    white space removed (Go ``NormalizeReferee``)."""
    return _trim(unicodedata.normalize("NFC", v))


# A referee both sides chose (PROFILE.md §10, "A referee both sides chose"):
# each party names the referee in its own adjudicator's copy, and a reader
# says "a referee you both chose" only when both parties' copies name the
# same one. One copy names the referee that side chose, never both sides'.

REFEREE_ONE_COPY = "one_copy"
REFEREE_MALFORMED = "malformed"
REFEREE_NOT_ADJUDICATOR = "not_adjudicator"
REFEREE_NOT_NAMED = "not_named"
REFEREE_ONE_PARTY = "one_party"
REFEREE_TWO_DEALS = "two_deals"
REFEREE_DIFFERENT = "different"


class RefereeError(ValueError):
    """Why copies name no referee both sides chose; ``reason`` is one of the
    ``REFEREE_*`` reasons (Go ``RefereeError``)."""

    def __init__(self, reason: str, detail: str):
        super().__init__("dealprofile: no referee both sides chose: " + detail)
        self.reason = reason


@dataclass(frozen=True)
class ReportCopy:
    """One party's copy as its reader admitted it: the sealed report's
    disclosed payload, and the party's signing key it verified under.
    :func:`shared_referee` checks neither signature nor admission."""
    payload: bytes
    party: str


_SURROGATE = re.compile("[\ud800-\udfff]")


def _refuse_constant(name: str) -> Any:
    raise ValueError(f"{name} is not JSON")


def _read(payload: bytes) -> tuple[str, str, str | None]:
    """The deal_id, audience and referee of a deal_report payload, members
    read by their exact names, each a string where present. As Go's
    encoding/json reads them, a null member is "" and a lone surrogate is
    U+FFFD."""
    try:
        m = json.loads(payload.decode("utf-8"), parse_constant=_refuse_constant)
    except (UnicodeDecodeError, ValueError):
        raise RefereeError(REFEREE_MALFORMED, "a copy's report is not a JSON object") from None
    if not isinstance(m, dict):
        raise RefereeError(REFEREE_MALFORMED, "a copy's report is not a JSON object")
    got: dict[str, str | None] = {}
    for k in ("type", "profile", "deal_id", "audience", "referee"):
        v = m.get(k)
        if v is not None and not isinstance(v, str):
            raise RefereeError(REFEREE_MALFORMED, f"a copy's report is not a deal_report of {PROFILE}")
        got[k] = None if k not in m else _SURROGATE.sub("\ufffd", v or "")
    if got["type"] != REPORT_TYPE or got["profile"] != PROFILE or not got["deal_id"] or got["audience"] is None:
        raise RefereeError(REFEREE_MALFORMED, f"a copy's report is not a deal_report of {PROFILE}")
    return got["deal_id"], got["audience"], got["referee"]  # type: ignore[return-value]


def report_referee(payload: bytes) -> str:
    """The referee a sealed report's payload names: "" when it names none. A
    payload that is not a deal_report of this profile is refused with
    ``REFEREE_MALFORMED``."""
    return _read(payload)[2] or ""


def shared_referee(*copies: ReportCopy) -> str:
    """The referee both sides chose: exactly two copies, under two parties'
    keys, of one deal, each an adjudicator's copy naming the same referee
    byte for byte. Anything else raises :class:`RefereeError`."""
    if len(copies) != 2:
        raise RefereeError(REFEREE_ONE_COPY, f"{len(copies)} copies: both sides' copies are needed, one each")
    got = []
    for c in copies:
        deal_id, audience, referee = _read(c.payload)
        if audience != AUDIENCE_ADJUDICATOR:
            raise RefereeError(REFEREE_NOT_ADJUDICATOR, f"a copy is cut for {_go_quote(audience)}, not an adjudicator")
        if not referee:
            raise RefereeError(REFEREE_NOT_NAMED, "a copy names no referee")
        got.append((deal_id, referee))
    a, b = copies
    if not a.party or not b.party or a.party == b.party:
        raise RefereeError(REFEREE_ONE_PARTY, "both copies are one party's")
    if got[0][0] != got[1][0]:
        raise RefereeError(REFEREE_TWO_DEALS, "the copies are of different deals")
    if got[0][1] != got[1][1]:
        raise RefereeError(REFEREE_DIFFERENT, "the copies name different referees")
    return got[0][1]
