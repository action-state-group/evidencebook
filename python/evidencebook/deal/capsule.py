# SPDX-License-Identifier: Apache-2.0
"""The Capsule members a step or a report is sealed with: plain data, one
member for each field Go's ``dealprofile`` fills in capsule-emit's
``emit.Input``. The producer seals them with the payload as the agent input,
with its own sealer and key; nothing here seals.

What matches Go byte for byte is the payload, its digest and these members,
not a sealed Capsule: the Capsule's own layout is the sealer's.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

__all__ = [
    "ACTION_TYPE_FYI", "ACTION_TYPE_DECIDE", "EFFECT_DISPATCHED", "ATTESTATION_RUNTIME_CLAIMED",
    "RELATION_FOLLOWS", "RELATION_CONFIRMS", "Capsule", "Disposition", "Effect", "Chain", "Extension",
    "rfc3339_seconds", "emit_input",
]

ACTION_TYPE_FYI = "fyi"
ACTION_TYPE_DECIDE = "decide"
#: A decide step's effect status: the agent's own report that it acted, with
#: no response in hand.
EFFECT_DISPATCHED = "dispatched"
ATTESTATION_RUNTIME_CLAIMED = "runtime_claimed"
#: A step follows the one before it (ordering only).
RELATION_FOLLOWS = "follows"
#: A record sealed after a terminal close confirms that close.
RELATION_CONFIRMS = "confirms"


@dataclass(frozen=True)
class Disposition:
    """How a decision was disposed (Go ``emit.Disposition``)."""
    decision: str
    approver: str
    human_disposed: bool
    #: Empty when the decision names none; emit then omits the member.
    verdict_class: str = ""
    #: Empty when the decision names none; emit then omits the member.
    reason_digest: str = ""


@dataclass(frozen=True)
class Effect:
    """The effect a decide Capsule records (Go ``emit.Effect``, the fields a
    step sets)."""
    type: str
    status: str
    irreversibility_class: str
    effect_attestation: str


@dataclass(frozen=True)
class Chain:
    """A Capsule's link to its parent (Go ``emit.Chain``)."""
    parent_capsule_id: str
    #: ``RELATION_FOLLOWS`` or ``RELATION_CONFIRMS`` for a step.
    relation: str


@dataclass(frozen=True)
class Extension:
    """One further top-level Capsule member (Go ``emit.Extension``)."""
    name: str
    value: Any


@dataclass(frozen=True)
class Capsule:
    """A Capsule's members before sealing (Go ``emit.Input``, the fields a
    step or report sets)."""
    action_id: str
    #: ``ACTION_TYPE_FYI`` or ``ACTION_TYPE_DECIDE``.
    action_type: str
    operator: str
    developer: str
    #: RFC 3339 UTC, to the second (``YYYY-MM-DDTHH:MM:SSZ``).
    timestamp: str
    disposition: Disposition | None = None
    effect: Effect | None = None
    chain: Chain | None = None
    #: Further top-level members, in order (Go ``emit.Input.Extensions``).
    extensions: tuple[Extension, ...] = field(default_factory=tuple)


def rfc3339_seconds(at: datetime) -> str:
    """``at`` as RFC 3339 UTC, to the second (sub-second parts are dropped,
    as Go's ``Truncate(time.Second)`` does). A naive datetime is UTC."""
    if at.tzinfo is None:
        at = at.replace(tzinfo=timezone.utc)
    return at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def emit_input(capsule: Capsule, *extensions: Extension) -> Any:
    """``capsule`` as ``capsule_emit.produce.Input``, with ``extensions``
    after its own (a book record's ``evidence_record_header``, say): what
    Go's ``dealprofile`` hands ``emit.Seal``. Sealing it stays the
    producer's, with its own key."""
    import json

    from capsule_emit import produce

    disposition = effect = chain = None
    if capsule.disposition is not None:
        d = capsule.disposition
        disposition = produce.Disposition(d.decision, d.approver, d.human_disposed, d.verdict_class, d.reason_digest)
    if capsule.effect is not None:
        e = capsule.effect
        effect = produce.Effect(e.type, e.status, e.irreversibility_class, e.effect_attestation)
    if capsule.chain is not None:
        chain = produce.Chain(capsule.chain.parent_capsule_id, capsule.chain.relation)
    # An Extension's value is a JSON value, or bytes already JSON text (Go's
    # json.RawMessage, such as a record header's canonical octets).
    members = [produce.Extension(x.name, x.value if isinstance(x.value, bytes)
                                 else json.dumps(x.value, ensure_ascii=False, separators=(",", ":")))
               for x in (*capsule.extensions, *extensions)]
    return produce.Input(action_id=capsule.action_id, action_type=capsule.action_type, operator=capsule.operator,
                         developer=capsule.developer, timestamp=capsule.timestamp, disposition=disposition,
                         effect=effect, chain=chain, extensions=tuple(members))

