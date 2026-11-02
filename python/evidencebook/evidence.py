# SPDX-License-Identifier: Apache-2.0
"""The ``evidence`` facade (evidencebook#332): six calls an agent builder
uses to admit a stranger from that stranger's history without reading the
specs. Each call is glue over a function that already exists and cites it;
none adds a rule. There is no score: an outcome is a state.

``seal``, ``ask`` and ``history`` are available. ``serve``, ``find`` and
``evaluate`` wait on the work they wrap and raise :class:`NotYetAvailable`
naming it (see each docstring).
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any, Callable, Mapping

from agent_action_capsule import standard_history as sh
from agent_action_capsule.evidence_request import (REASON_CAPACITY_EXCEEDED, REASON_REQUEST_UNAUTHENTICATED)

from .book import (OBSERVED_EVENT, OUTCOME_ARTIFACT, OUTCOME_ARTIFACT_FAILED, OUTCOME_REFUSAL, Book,
                   ResponderKeys)
from .errors import InvalidError
from .record import Entry, Record
from .request import DERIVATION_HISTORY_CARD, EvidenceRequest, Message, parse_reply, strict_json
from .staple import DERIVATION_STANDARD_HISTORY, Staple, check_staple

__all__ = ["seal", "serve", "find", "ask", "history", "evaluate", "Outcome", "History", "NotYetAvailable",
           "ARTIFACT", "ARTIFACT_FAILED", "REFUSAL", "ABSENCE", "PENDING", "STATUS_UNKNOWN"]

# An outcome state. The first three are the evidence request's own (ER §4);
# STATUS_UNKNOWN is a reading of a refusal or window that says nothing about
# the responder's history (below); PENDING is a window still open.
ARTIFACT = "artifact"
ARTIFACT_FAILED = "artifact_verification_failed"
REFUSAL = "refusal"
ABSENCE = "recorded_absence"
PENDING = "pending"
STATUS_UNKNOWN = "status_unknown"

# Refusal reasons that describe the exchange, not the responder's history.
_UNKNOWN_REASONS = frozenset({REASON_CAPACITY_EXCEEDED, REASON_REQUEST_UNAUTHENTICATED})


class NotYetAvailable(NotImplementedError):
    """A facade call whose wrapped function does not exist yet."""


def seal(book: Book, record_type: str, statement: Any = None, **entry: Any) -> Record:
    """Seal ``statement`` as a ``record_type`` record of ``book`` and commit
    it. Wraps :meth:`Book.append`; ``entry`` is any other :class:`Entry`
    member (``payloads``, ``links``, ``subject_ref``, ...). The epistemic type
    is an observed event unless ``entry`` says otherwise."""
    entry.setdefault("epistemic_type", OBSERVED_EVENT)
    return book.append(Entry(record_type, statement=statement, **entry))


def serve(manifest: Any, policy: Any, pack: Any = None) -> Any:
    """Serve a book's evidence under ``manifest`` and ``policy`` (and a
    ``pack``). Not yet available: it wraps the responder behind the fixed
    manifest path of agent-action-capsule#554 and the spec card served by
    capsule-emit-mesh#237."""
    raise NotYetAvailable("serve waits on agent-action-capsule#554 and capsule-emit-mesh#237 (evidencebook#332)")


def find(target: Any) -> Any:
    """Find a counterparty's evidence manifest from a URL, an A2A agent card
    or an MCP server. Not yet available: it wraps the fixed manifest path of
    agent-action-capsule#554 and inline verification of capsule-cli#523."""
    raise NotYetAvailable("find waits on agent-action-capsule#554 and capsule-cli#523 (evidencebook#332)")


def evaluate(pack: Any, card: Any) -> Any:
    """Decide admission under an admission pack. Not yet available: the
    pack evaluator is Go-only (capsule-engine ``go/admission``); the binding
    is capsule-engine#278, over the node pack of capsule-engine#276."""
    raise NotYetAvailable("evaluate waits on capsule-engine#278 and #276 (evidencebook#332)")


@dataclass(frozen=True)
class Outcome:
    """What asking came to. ``state`` is one of the module's state names;
    ``reason`` is a refusal's reason; ``artifact`` the verified artifact's
    octets (state ``ARTIFACT`` only); ``record`` the record the book sealed
    for it (none while ``PENDING``)."""

    state: str
    reason: str = ""
    artifact: bytes = b""
    record: Record | None = None


def ask(book: Book, request: EvidenceRequest, responder: str, keys: ResponderKeys,
        send: Callable[[Message], Message | None], window_end: datetime | None = None) -> Outcome:
    """Ask ``responder`` and record what came back. Wraps
    :meth:`Book.request` (the request is committed before it is sent),
    ``send`` (the caller's transport: the request message to the reply
    message, or ``None``/an ``OSError`` for no answer), :meth:`Book.record_response`
    and :meth:`Book.record_absence`.

    A verified artifact is ``ARTIFACT``; a signed refusal ``REFUSAL``, except
    ``capacity_exceeded`` and ``request_unauthenticated``, which are
    ``STATUS_UNKNOWN`` (they say nothing about the responder's history); no
    answer is ``PENDING`` until ``window_end`` is given, then a recorded
    ``ABSENCE`` (refused with :class:`InvalidError` while the window is
    shorter than ``MINIMUM_ABSENCE_WINDOW``)."""
    sent = book.request(request, responder)
    try:
        reply = send(sent.message)
    except OSError:
        reply = None
    if reply is None:
        if window_end is None:
            return Outcome(PENDING)
        return Outcome(ABSENCE, record=book.record_absence(sent.record_id, window_end))
    record = book.record_response(sent.record_id, reply, keys)
    statement = record.header.statement
    outcome = statement["outcome"]
    if outcome == OUTCOME_ARTIFACT:
        return Outcome(ARTIFACT, artifact=parse_reply(reply).envelope.artifact, record=record)
    if outcome == OUTCOME_ARTIFACT_FAILED:
        return Outcome(ARTIFACT_FAILED, reason=statement.get("failure", ""), record=record)
    reason = statement.get("reason", "")
    state = STATUS_UNKNOWN if reason in _UNKNOWN_REASONS else REFUSAL
    return Outcome(state, reason=reason, record=record)


@dataclass(frozen=True)
class History:
    """A holder's presented history, read. ``properties`` is the six card
    properties of Standard History §6 for a ``standard_history/1`` card
    (``PASS``/``FAIL``/``NOT_CHECKED``, no score) and ``None`` for a
    ``history_card/1``, whose form is all :func:`history` checks."""

    derivation: str
    anchor: str
    nonce: str
    issued_at: str
    artifact: bytes
    properties: Mapping[str, str] | None


def history(instance: Staple, holder: str) -> History:
    """Read a stapled history presented by ``holder`` (lowercase hex
    Ed25519). Wraps :func:`check_staple` (the holder, the signed envelope and
    the card's form; :class:`InvalidError` on any failure) and, for a
    ``standard_history/1`` card, ``agent_action_capsule.standard_history.check_card``.
    The anchor's checkpoint and the nonce's freshness remain the reader's."""
    checked = check_staple(instance, holder)
    properties = None
    if checked.derivation == DERIVATION_STANDARD_HISTORY:
        try:
            properties = sh.check_card(strict_json(checked.artifact))
        except ValueError as err:
            raise InvalidError(f"the stapled card: {err}") from None
    elif checked.derivation != DERIVATION_HISTORY_CARD:
        raise InvalidError(f"not a history: {checked.derivation}")
    return History(checked.derivation, checked.anchor, checked.nonce, checked.issued_at, checked.artifact,
                   properties)
