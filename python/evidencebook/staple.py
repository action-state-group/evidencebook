"""A History card stapled to the holder's own message (evidencebook#275),
as Go's ``Book.Staple`` and ``CheckStaple`` build and check it.

A staple is the holder's own evidence request (subject, derivation,
coverage pinned to its newest checkpoint, a fresh nonce) and the signed
envelope its book answers it with. It carries ``history_card/1`` over
``checkpoints`` or ``standard_history/1`` over ``full_history`` and nothing
else, so it never carries more than the standard card. Where it sits, and
the witness Receipts beside a card, are the carrier's; whether to staple is
the host's setting."""

from __future__ import annotations

import hashlib
import os
from dataclasses import dataclass

from agent_action_capsule import standard_history as sh
from agent_action_capsule.evidence_request import VerificationError

from .errors import InvalidError
from .request import (
    DERIVATION_HISTORY_CARD,
    MEDIA_TYPE_JSON,
    PROFILE,
    Coverage,
    EvidenceRequest,
    Message,
    Reply,
    Subject,
    encode_request,
    parse_reply,
    read_request,
    strict_json,
)

DERIVATION_STANDARD_HISTORY = sh.DERIVATION

#: The cap: each derivation a staple may carry and the one subject it is
#: answered over.
_STAPLE_SUBJECTS = {DERIVATION_HISTORY_CARD: "checkpoints", DERIVATION_STANDARD_HISTORY: "full_history"}

__all__ = ["DERIVATION_STANDARD_HISTORY", "Staple", "StapleCheck", "staple", "check_staple"]


@dataclass(frozen=True)
class Staple:
    """The holder's own request and the reply its book gave it, both JSON."""

    request: Message
    reply: Reply


@dataclass(frozen=True)
class StapleCheck:
    """What :func:`check_staple` read from a staple that passed."""

    derivation: str
    #: The digest of the holder checkpoint the card is as of.
    anchor: str
    #: The holder's nonce, for the reader's replay check.
    nonce: str
    issued_at: str
    artifact: bytes


def staple(book, derivation: str) -> Staple:
    """A staple of ``derivation`` from ``book``, as of its newest checkpoint
    (cut now if it grew). Any derivation but the two the cap allows is
    :class:`InvalidError`, as is a card the book does not serve."""
    from .book import RespondOptions  # book imports request; keep the cycle out of import time

    subject = _STAPLE_SUBJECTS.get(derivation)
    if subject is None:
        raise InvalidError(f"a staple carries {DERIVATION_HISTORY_CARD} or {DERIVATION_STANDARD_HISTORY}, not {derivation!r}")
    checkpoint = book.checkpoint()
    request = Message(encode_request(EvidenceRequest(
        Subject(subject), Coverage(expected_pin=checkpoint.anchor_digest), derivation=derivation,
        nonce=os.urandom(16).hex(), profile=PROFILE)), MEDIA_TYPE_JSON)
    # The holder answers itself; standard_history/1 is answered only to an
    # authenticated requester.
    options = RespondOptions(authenticated_requester=book.key_id) if derivation == DERIVATION_STANDARD_HISTORY \
        else RespondOptions()
    reply, _ = book.respond(request, options)
    if reply.envelope is None:
        reason = reply.refusal.reason if reply.refusal is not None else ""
        raise InvalidError(f"the book refused its own {derivation} request ({reason})")
    return Staple(request, reply)


def check_staple(s: Staple, holder: str) -> StapleCheck:
    """Check that ``s`` is presented by ``holder`` (lowercase hex Ed25519)
    and stays inside the cap, as Go's ``CheckStaple`` does. Every failure is
    :class:`InvalidError`. It does not check the anchor's checkpoint, the
    card's own properties or the nonce's freshness: those are the reader's."""
    if s.request.media_type != MEDIA_TYPE_JSON or s.reply.message.media_type != MEDIA_TYPE_JSON:
        raise InvalidError("a staple's request and reply are JSON")
    parsed = read_request(s.request)
    if parsed.reason:
        raise InvalidError(f"a staple's request is malformed: {parsed.why}")
    req = parsed.request
    if req.derivation not in _STAPLE_SUBJECTS or req.subject.form != _STAPLE_SUBJECTS[req.derivation]:
        raise InvalidError(f"a staple carries {DERIVATION_HISTORY_CARD} over checkpoints or {DERIVATION_STANDARD_HISTORY} "
                           f"over full_history, not {req.derivation!r} over {req.subject.form}")
    if req.coverage.expected_pin is None or not req.nonce:
        raise InvalidError("a staple's request pins its anchor and carries a nonce")
    e = parse_reply(s.reply.message).envelope
    if e is None:
        raise InvalidError("a staple's reply is a refusal")
    try:
        e.verify()
    except VerificationError as err:
        raise InvalidError(f"the staple's envelope: {err}") from None
    if e.responder != holder:
        raise InvalidError("the staple is not presented by its holder")
    if e.signed_members()["request_digest"] != hashlib.sha256(s.request.octets).hexdigest():
        raise InvalidError("the staple's envelope answers another request")
    if e.derivation != req.derivation or e.subject.form != req.subject.form:
        raise InvalidError("the staple's envelope signs another derivation or subject")
    if e.anchor_digest != req.coverage.expected_pin:
        raise InvalidError("the staple's envelope is as of another anchor")
    if req.derivation == DERIVATION_STANDARD_HISTORY:
        try:
            card = strict_json(e.artifact)
        except ValueError:
            card = None
        for name, status in sh.check_card(card).items():
            if status == "FAIL":
                raise InvalidError(f"the stapled card fails {name}")
    return StapleCheck(req.derivation, e.anchor_digest, req.nonce, e.issued_at, e.artifact)
