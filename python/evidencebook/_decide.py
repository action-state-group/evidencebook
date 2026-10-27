# SPDX-License-Identifier: Apache-2.0
"""A responder's decision for one well-formed request.

This is Go's ``evidencerequest.Decide`` (agent-action-capsule
``go/evidencerequest/resolve.go``), step for step: the deadline, derivation
support, authorization, the mechanism, coverage, the subject, then the
refusal policy. agent-action-capsule's Python ``resolve`` checks fewer
steps (no authorization, retention lapse, half pin or anchor issuing), so a
Python responder built on it would refuse differently from Go's.
"""
from __future__ import annotations

from collections.abc import Callable, Collection, Sequence
from dataclasses import dataclass, replace
from typing import Protocol

from agent_action_capsule.evidence_request import (
    DERIVATION_HISTORY_CARD,
    REASON_COVERAGE_UNSATISFIABLE,
    REASON_DEADLINE_UNMET,
    REASON_DERIVATION_UNSUPPORTED,
    REASON_NO_SUCH_SUBJECT,
    REASON_NOT_AUTHORIZED,
    REASON_POLICY_DECLINED,
    REASON_REQUEST_MALFORMED,
    REASON_RETENTION_EXPIRED,
)

from ._time import parse_ns
from .request import EvidenceRequest

_SECOND = 1_000_000_000


@dataclass(frozen=True)
class AnchorView:
    """One coverage anchor issued: its digest, the log entries it covers,
    and when it was issued (nanoseconds since the epoch)."""

    digest: str
    entries: int
    issued_at: int


class View(Protocol):
    """What resolution reads of a responder's evidence; positions are
    zero-based. Every lookup is exact."""

    def anchors(self) -> Sequence[AnchorView]: ...

    def log_entries(self) -> int: ...

    def leaf_of(self, digest: str) -> int | None: ...

    def servable(self, leaf: int) -> bool: ...

    def correlated(self, correlation_id: str) -> list[int]: ...

    def citing(self, half: str) -> list[int]: ...

    def lapsed(self, subject_value: dict, now: int) -> bool: ...


@dataclass(frozen=True)
class Resolution:
    anchor: int = -1
    half_pin: bool = False
    leaves: tuple[int, ...] = ()
    reason: str = ""
    need_anchor: bool = False


@dataclass(frozen=True)
class Policy:
    derivation: Callable[[str], Collection[str] | None] | None = None
    authorize: Callable[[EvidenceRequest], bool] | None = None
    uniform: bool = False
    unanchored: bool = False
    unpositioned: bool = False
    may_issue: bool = False

    def disclose(self, reason: str) -> str:
        if self.uniform and reason in (REASON_NOT_AUTHORIZED, REASON_NO_SUCH_SUBJECT, REASON_COVERAGE_UNSATISFIABLE):
            return REASON_POLICY_DECLINED
        return reason


_refuse = lambda reason: Resolution(reason=reason)  # noqa: E731


def anchor_meets(anchor: AnchorView, floor: int | str, floor_time: int) -> bool:
    if isinstance(floor, int):
        return anchor.entries >= floor
    return anchor.issued_at - anchor.issued_at % _SECOND >= floor_time


def resolve_coverage(view: View, req: EvidenceRequest, now: int, may_issue: bool) -> Resolution:
    anchors = view.anchors()
    pin = req.coverage.expected_pin
    if pin is not None:
        for i, anchor in enumerate(anchors):
            if anchor.digest == pin:
                return Resolution(anchor=i)
        if req.subject.form == "exchange" and pin == req.subject.content:
            return Resolution(half_pin=True)
        return _refuse(REASON_COVERAGE_UNSATISFIABLE)
    floor = req.coverage.min_freshness
    if floor is None:
        return _refuse(REASON_COVERAGE_UNSATISFIABLE)
    floor_time = 0
    if isinstance(floor, str):
        parsed = parse_ns(floor)
        if parsed is None:
            return _refuse(REASON_REQUEST_MALFORMED)
        floor_time = parsed
    for i in range(len(anchors) - 1, -1, -1):
        if anchor_meets(anchors[i], floor, floor_time):
            return Resolution(anchor=i)
    # A floor no anchor could meet yet is refused without doing any work.
    if isinstance(floor, int) and floor > view.log_entries() or isinstance(floor, str) and floor_time > now:
        return _refuse(REASON_COVERAGE_UNSATISFIABLE)
    if not may_issue or view.log_entries() == 0:
        return _refuse(REASON_COVERAGE_UNSATISFIABLE)
    return Resolution(need_anchor=True)


def _with_retention(view: View, req: EvidenceRequest, now: int, r: Resolution) -> Resolution:
    if r.reason == REASON_NO_SUCH_SUBJECT and view.lapsed(req.subject.to_wire(), now):
        return replace(r, reason=REASON_RETENTION_EXPIRED)
    return r


def resolve_subject(view: View, req: EvidenceRequest, r: Resolution, now: int) -> Resolution:
    anchors = view.anchors()
    subject = req.subject
    if r.half_pin:
        # The earliest anchor covering every anchored record citing the half,
        # so growth that adds no citing record never changes the artifact.
        cited = view.citing(subject.content)
        if not cited:
            return _with_retention(view, req, now, _refuse(REASON_NO_SUCH_SUBJECT))
        latest = anchors[-1].entries if anchors else 0
        anchored = [leaf for leaf in cited if leaf < latest]
        if not anchored:
            return _refuse(REASON_COVERAGE_UNSATISFIABLE)
        last = max(anchored)
        for i, anchor in enumerate(anchors):
            if anchor.entries > last:
                r = replace(r, anchor=i)
                break
    if r.anchor < 0 or r.anchor >= len(anchors):
        return _refuse(REASON_COVERAGE_UNSATISFIABLE)
    covered = anchors[r.anchor].entries
    form = subject.form
    if form == "checkpoints":
        return r
    if form == "full_history":
        leaves = tuple(leaf for leaf in range(covered) if view.servable(leaf))
        r = replace(r, leaves=leaves)
        if not leaves and req.derivation is None:
            r = replace(r, reason=REASON_NO_SUCH_SUBJECT)
        return r
    if form == "record":
        leaf = view.leaf_of(subject.content)
        if leaf is None:
            return _with_retention(view, req, now, _refuse(REASON_NO_SUCH_SUBJECT))
        if leaf >= covered:
            return _refuse(REASON_COVERAGE_UNSATISFIABLE)
        return replace(r, leaves=(leaf,))
    if form == "range":
        a, b = subject.content
        if b >= covered:
            if b < view.log_entries():
                return _refuse(REASON_COVERAGE_UNSATISFIABLE)
            return _with_retention(view, req, now, _refuse(REASON_NO_SUCH_SUBJECT))
        leaves = tuple(leaf for leaf in range(a, b + 1) if view.servable(leaf))
        if not leaves:
            return _with_retention(view, req, now, _refuse(REASON_NO_SUCH_SUBJECT))
        return replace(r, leaves=leaves)
    if form in ("correlation", "exchange"):
        every = view.correlated(subject.content) if form == "correlation" else view.citing(subject.content)
        leaves = tuple(sorted({leaf for leaf in every if leaf < covered}))
        if leaves:
            return replace(r, leaves=leaves)
        if every:
            return _refuse(REASON_COVERAGE_UNSATISFIABLE)
        return _with_retention(view, req, now, _refuse(REASON_NO_SUCH_SUBJECT))
    return _refuse(REASON_REQUEST_MALFORMED)


def decide(view: View, req: EvidenceRequest, p: Policy, now: int) -> Resolution:
    """The decision for ``req`` at ``now`` (nanoseconds). A ``need_anchor``
    decision asks the caller to issue an anchor and decide again."""
    if req.deadline is not None:
        deadline = parse_ns(req.deadline)
        if deadline is None:
            return _refuse(REASON_REQUEST_MALFORMED)
        if now >= deadline:
            return _refuse(REASON_DEADLINE_UNMET)
    forms: Collection[str] = ()
    registered = False
    if req.derivation == DERIVATION_HISTORY_CARD:
        if req.subject.form not in ("full_history", "checkpoints"):
            return _refuse(REASON_NO_SUCH_SUBJECT)
    elif req.derivation is not None:
        found = p.derivation(req.derivation) if p.derivation is not None else None
        if found is None:
            return _refuse(REASON_DERIVATION_UNSUPPORTED)
        forms, registered = found, True
    if p.authorize is not None and not p.authorize(req):
        return _refuse(p.disclose(REASON_NOT_AUTHORIZED))
    if registered and req.subject.form not in forms:
        return _refuse(p.disclose(REASON_NO_SUCH_SUBJECT))
    if p.unanchored or p.unpositioned and req.subject.form == "range":
        return _refuse(p.disclose(REASON_COVERAGE_UNSATISFIABLE))
    r = resolve_coverage(view, req, now, p.may_issue)
    if not r.reason and not r.need_anchor:
        r = resolve_subject(view, req, r, now)
    return replace(r, reason=p.disclose(r.reason))


__all__ = ["AnchorView", "View", "Resolution", "Policy", "decide", "resolve_coverage", "resolve_subject",
           "anchor_meets"]
