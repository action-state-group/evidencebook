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

from .book import (OBSERVED_EVENT, OUTCOME_ARTIFACT, OUTCOME_ARTIFACT_FAILED, OUTCOME_REFUSAL,
                   OUTCOME_REFUSAL_NONCONFORMING, Book, ResponderKeys)
from .errors import InvalidError
from .record import Entry, Record
from .request import EvidenceRequest, Message, parse_reply, strict_json
from .staple import DERIVATION_STANDARD_HISTORY, Staple, check_staple

__all__ = ["seal", "serve", "find", "ask", "history", "evaluate", "Outcome", "History", "NotYetAvailable",
           "ARTIFACT", "ARTIFACT_FAILED", "REFUSAL", "REFUSAL_NONCONFORMANT", "ABSENCE", "PENDING", "STATUS_UNKNOWN"]

# An outcome state. The first three are the evidence request's own (ER §4);
# STATUS_UNKNOWN is a reading of a refusal or window that says nothing about
# the responder's history (below); PENDING is a window still open, resumed by passing the Outcome back to ask.
ARTIFACT = "artifact"
ARTIFACT_FAILED = "artifact_verification_failed"
REFUSAL = "refusal"
REFUSAL_NONCONFORMANT = "refusal_nonconformant"
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
    for it (none while ``PENDING``); ``request_id`` the id of the request
    record the book committed before sending."""

    state: str
    reason: str = ""
    artifact: bytes = b""
    record: Record | None = None
    request_id: str = ""


def ask(book: Book, request: EvidenceRequest | Outcome, responder: str, keys: ResponderKeys,
        send: Callable[[Message], Message | None], window_end: datetime | None = None) -> Outcome:
    """Ask ``responder`` and record what came back. Wraps
    :meth:`Book.request` (the request is committed before it is sent),
    ``send`` (the caller's transport: the request message to the reply
    message, or ``None``/an ``OSError`` for no answer), :meth:`Book.record_response`
    and :meth:`Book.record_absence`.

    A verified artifact is ``ARTIFACT``; a signed refusal ``REFUSAL`` (or
    ``REFUSAL_NONCONFORMANT``), except ``capacity_exceeded`` and
    ``request_unauthenticated``, which are ``STATUS_UNKNOWN`` (they say
    nothing about the responder's history); no answer is ``PENDING`` until
    ``window_end`` is given, then a recorded ``ABSENCE``; a window that is
    still open or shorter than ``MINIMUM_ABSENCE_WINDOW`` stays ``PENDING``.
    To close a ``PENDING`` request, pass its Outcome as ``request`` with a
    ``window_end``: nothing is sent again, and :class:`InvalidError` is
    raised while that window cannot be closed. A reply the book refuses
    raises :class:`InvalidError` with ``request_id`` set to the request, which
    stays open."""
    if isinstance(request, Outcome):
        if request.state != PENDING or window_end is None:
            raise InvalidError("only a pending outcome with a window_end can be resumed")
        return Outcome(ABSENCE, record=book.record_absence(request.request_id, window_end),
                       request_id=request.request_id)
    sent = book.request(request, responder)
    try:
        reply = send(sent.message)
    except OSError:
        reply = None
    if reply is None:
        if window_end is None:
            return Outcome(PENDING, request_id=sent.record_id)
        try:
            absence = book.record_absence(sent.record_id, window_end)
        except InvalidError:  # a request just sent has no outcome, so the window is open or too short
            return Outcome(PENDING, request_id=sent.record_id)
        return Outcome(ABSENCE, record=absence, request_id=sent.record_id)
    try:
        record = book.record_response(sent.record_id, reply, keys)
    except InvalidError as err:  # a refused reply leaves the request open; keep its id for the caller
        err.request_id = sent.record_id
        raise
    statement = record.header.statement
    outcome, reason = statement["outcome"], statement.get("reason", "")
    if outcome == OUTCOME_ARTIFACT:
        return Outcome(ARTIFACT, artifact=parse_reply(reply).envelope.artifact, record=record, request_id=sent.record_id)
    if outcome == OUTCOME_ARTIFACT_FAILED:
        return Outcome(ARTIFACT_FAILED, reason=statement.get("failure", ""), record=record, request_id=sent.record_id)
    if outcome not in (OUTCOME_REFUSAL, OUTCOME_REFUSAL_NONCONFORMING):
        raise InvalidError(f"unexpected recorded outcome {outcome!r}")
    if reason in _UNKNOWN_REASONS:
        state = STATUS_UNKNOWN
    else:
        state = REFUSAL if outcome == OUTCOME_REFUSAL else REFUSAL_NONCONFORMANT
    return Outcome(state, reason=reason, record=record, request_id=sent.record_id)


@dataclass(frozen=True)
class History:
    """A holder's presented history, read. ``properties`` is the six card
    properties of Standard History §6 for a ``standard_history/1`` card
    (``PASS`` or ``NOT_CHECKED``: a ``FAIL`` is refused, no score) and ``None`` for a
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
    checked = check_staple(instance, holder)  # refuses any derivation but the two, and a card that FAILs
    properties = None
    if checked.derivation == DERIVATION_STANDARD_HISTORY:
        properties = sh.check_card(strict_json(checked.artifact))
    return History(checked.derivation, checked.anchor, checked.nonce, checked.issued_at, checked.artifact,
                   properties)
