# SPDX-License-Identifier: Apache-2.0
"""A step's Capsule members and its record header fields, as Go's
``dealprofile/step.go`` lays them out.

Each step of a deal is sealed as one Capsule (PROFILE.md §1) whose payload is
the step's record: action_id ``"<deal_id>/<seq>"``, chained to the step before
it, with the deal id as its top-level ``deal_id`` member. On a book it also
carries an evidence record header of type ``STEP_RECORD_TYPE`` whose
subject_ref is the action_id, whose one link cites the parent, and whose
field commitments are the step's local event and the deal's closed money
terms. The producer decides what the step says; :func:`step_capsule` only
lays it out.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from .capsule import (
    ACTION_TYPE_DECIDE, ACTION_TYPE_FYI, ATTESTATION_RUNTIME_CLAIMED, EFFECT_DISPATCHED, RELATION_CONFIRMS,
    RELATION_FOLLOWS, Capsule, Chain, Disposition, Effect, Extension, rfc3339_seconds,
)
from .check import _go_quote
from .names import AMOUNT_FIELD, CURRENCY_FIELD, DEAL_ID_MEMBER, LOCAL_FIELD, STEP_RECORD_TYPE

__all__ = ["EPISTEMIC_PRODUCER_CLAIM", "Decide", "StepInput", "StepHeader", "step_capsule"]

#: The epistemic type of every step and report record: the producer's own
#: claim.
EPISTEMIC_PRODUCER_CLAIM = "producer_claim"


@dataclass(frozen=True)
class Decide:
    """Marks a step as a decide Capsule: an action taken under a sealed
    approval whose action has an effect type. The producer decides that, and
    how the action was disposed and how recoverable it is; the effect is
    always the agent's own report that it acted, with no response in hand."""
    disposition: Disposition
    effect_type: str
    irreversibility: str


@dataclass(frozen=True)
class StepInput:
    """What a step's Capsule is made from."""
    deal_id: str
    seq: int
    operator: str
    #: The producer's own developer name (the capsulectl deal plugin names
    #: "capsulectl-deal").
    developer: str
    #: The step's seal time, the record's block "at"; taken to the second.
    at: datetime
    #: The Capsule the step chains to (the step before it, or the close it
    #: confirms); None for the first step.
    parent: Chain | None = None
    #: A settlement leg the step carries, as built and stored with the step;
    #: it rides as the Capsule's top-level settlement member.
    settlement: Any = None
    #: Set for a decide step, None for fyi.
    decide: Decide | None = None
    #: The values the header's field commitments commit: the producer's local
    #: event (opaque here), and the deal's closed amount and currency ("" when
    #: none).
    local: str = ""
    amount: str = ""
    currency: str = ""


@dataclass(frozen=True)
class StepHeader:
    """The evidence record header fields a step carries on a book: all but
    what the book assigns. A step names no correlation."""
    record_type: str
    epistemic_type: str
    #: The step's seal time (RFC 3339 UTC, to the second), the record's
    #: committed_at.
    committed_at: str
    subject_ref: str
    #: The parent Capsule, which the header's one cites link targets, when the
    #: step has one.
    cites: tuple[str, ...] = field(default_factory=tuple)
    fields: dict[str, str] = field(default_factory=dict)


def step_capsule(inp: StepInput) -> tuple[Capsule, StepHeader]:
    """A step's Capsule members and its record header fields. The producer
    seals the Capsule with the record's payload as JSON and its own key."""
    if not inp.deal_id or inp.seq < 1:
        raise ValueError("dealprofile: a step needs a deal id and a seq >= 1")
    if not inp.developer:
        raise ValueError("dealprofile: a step needs the producer's developer name")
    p = inp.parent
    if p is not None and p.relation not in (RELATION_FOLLOWS, RELATION_CONFIRMS):
        raise ValueError(f"dealprofile: chain relation {_go_quote(p.relation)} is not follows or confirms")
    at = rfc3339_seconds(inp.at)
    action_id = f"{inp.deal_id}/{inp.seq}"
    extensions: list[Extension] = []
    if inp.settlement is not None and inp.settlement != b"":
        extensions.append(Extension("settlement", inp.settlement))
    extensions.append(Extension(DEAL_ID_MEMBER, inp.deal_id))
    action_type, disposition, effect = ACTION_TYPE_FYI, None, None
    if inp.decide is not None:
        d = inp.decide
        action_type, disposition = ACTION_TYPE_DECIDE, d.disposition
        effect = Effect(d.effect_type, EFFECT_DISPATCHED, d.irreversibility, ATTESTATION_RUNTIME_CLAIMED)
    capsule = Capsule(action_id, action_type, inp.operator, inp.developer, at, disposition, effect, p,
                      tuple(extensions))
    header = StepHeader(
        STEP_RECORD_TYPE, EPISTEMIC_PRODUCER_CLAIM, at, action_id,
        (p.parent_capsule_id,) if p is not None else (),
        {LOCAL_FIELD: inp.local, AMOUNT_FIELD: inp.amount, CURRENCY_FIELD: inp.currency},
    )
    return capsule, header
