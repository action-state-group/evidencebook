# SPDX-License-Identifier: Apache-2.0
"""Step 07 Ask, over the mailbox the asked node's commitment advertises.

A Goose node hosts no server. The commitment it signs names a Nostr mailbox
(``ask.nostr {mailbox_key, relays}``, binding nostr-mailbox-v0 §3 and §4.3):

* the **answering** side polls: :func:`answer_round` reads the evidence requests addressed to its
  mailbox key on those relays and answers each from its own node book with ``Book.respond``,
  under the node key. A request outside the node's disclosure policy gets a signed refusal, never
  silence. An answered request is sealed in the node book, so a restart does not answer it again;
* the **asking** side seals the request before sending (:func:`ask`), publishes it to the asked
  node's advertised mailbox with a ``deadline`` of the commitment's ``answers_within``, and polls
  for the reply (:func:`poll`): an answer in time is verified under the node's listed keys and
  recorded; no answer by the deadline is a recorded absence against the node; an answer that
  arrives after the absence is kept beside it, recorded as late, and never rewrites it
  (binding §5.3).

Arrival is judged by the asker's clock at the poll that first sees the reply, never by the event's
``created_at`` (binding §5.3). That is why ``answers_within`` may not be shorter than the
interval at which a node polls (:mod:`commitment`).

The events are not encrypted (binding §8, N-3): use a relay that authenticates its readers."""
from __future__ import annotations

import base64
import hashlib
from datetime import datetime, timedelta, timezone
from typing import Any, Callable

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
from agent_action_capsule.canonical import jcs
from agent_action_capsule.evidence_request import verify_refusal_offline
from evidencebook._time import from_nanos, parse_ns
from evidencebook import (
    CITES, MINIMUM_ABSENCE_WINDOW, RECORD_TYPE_ABSENCE, RECORD_TYPE_RESPONSE, Coverage, EvidenceRequest, InvalidError,
    Message, ResponderKeys, Subject, parse_reply,
)

import commitment as cm
import goose_node as gn
import nostr_mailbox as mb

#: The media type of the evidence request and reply the mailbox carries (evidence-interop/1, JSON).
MEDIA = "application/json"
#: Request events this node answered (node book), a request this node sent over a mailbox, and a
#: reply that arrived after its absence was recorded.
ANSWERED, ASKED, LATE = "goose_mailbox_answered", "goose_mailbox_asked", "goose_mailbox_late"

__all__ = ["MEDIA", "ANSWERED", "ASKED", "LATE", "answer_round", "route", "ask", "poll"]


def _factory(relay_factory: Any) -> Callable[[str], mb.Relay]:
    return relay_factory or mb.Relay


def answer_round(node: gn.Node, relays: list[str], relay_factory: Any = None,
                 now: Callable[[], float] | None = None) -> list[str]:
    """One poll of the node's own mailbox: answer the requests addressed to it that this node has
    not answered. Returns the ids of the request events answered. A relay that cannot be reached
    is skipped; a request that is not an evidence request in a supported encoding gets no reply
    (binding §5.2), and the next round reads it again."""
    factory = _factory(relay_factory)
    with node._lock:
        answered = {st["request_event_id"] for _, st in node.notes(ANSWERED)}
    book = node.shelf.open(gn.NODE_BOOK)

    def answer(octets: bytes, media: str) -> bytes | None:
        try:
            with node._lock:
                reply, _ = book.respond(Message(octets, media))
        except InvalidError:
            return None
        return reply.message.octets

    done = mb.serve_round([factory(u) for u in relays], node.mailbox_secret, answer, answered,
                          now or (lambda: node._clock().timestamp()), with_media=True)
    for event_id in done:
        node.note(ANSWERED, {"request_event_id": event_id}, subject_ref=event_id)
    return done


def route(commitment: Any) -> dict:
    """The mailbox route of a commitment this node trusts: its signature verified under the key it
    names, and the facts an asker needs from it. Refuses a commitment that names no mailbox."""
    cm.verify(commitment)
    nostr = commitment["ask"].get("nostr")
    if nostr is None:
        raise gn.Refused("the commitment names no mailbox (ask.nostr): this node cannot be asked this way")
    cards = commitment.get("history_cards") or []
    if not cards or "checkpoint_key" not in cards[0]:
        raise gn.Refused("the commitment names no checkpoint key to verify an answer under")
    checkpoint = base64.urlsafe_b64decode(cards[0]["checkpoint_key"] + "=" * (-len(cards[0]["checkpoint_key"]) % 4))
    return {"responder": commitment["key_id"], "checkpoint": checkpoint[-32:].hex(),
            "mailbox_key": nostr["mailbox_key"], "relays": list(nostr["relays"]),
            "answers_within": cm.seconds(commitment["answers_within"])}


def ask(node: gn.Node, commitment: Any, subject_form: str, subject_value: Any = None, derivation: str = "",
        expected_pin: str = "", min_freshness: int | str | None = None, relay_factory: Any = None,
        now: Callable[[], datetime] | None = None) -> dict:
    """Seal an evidence request in the node book, then publish it to the mailbox the asked node's
    ``commitment`` advertises. Its deadline is ``answers_within`` from now. Poll with :func:`poll`."""
    where = route(commitment)
    if where["answers_within"] < MINIMUM_ABSENCE_WINDOW.total_seconds():
        raise gn.Refused("answers_within is shorter than the shortest waiting window an absence can record "
                         f"({int(MINIMUM_ABSENCE_WINDOW.total_seconds())}s)")
    started = (now or node._clock)().astimezone(timezone.utc).replace(microsecond=0)
    deadline = started + timedelta(seconds=where["answers_within"])
    request = EvidenceRequest(Subject(subject_form, subject_value),
                              Coverage(expected_pin=expected_pin or None, min_freshness=min_freshness),
                              derivation=derivation or None, deadline=deadline.strftime("%Y-%m-%dT%H:%M:%SZ"))
    sent = node.ask(request, where["responder"])
    event = mb.sign_event(mb.new_secret(), mb.REQUEST_KIND,
                          [("p", where["mailbox_key"]), ("m", MEDIA), ("expiration", str(int(deadline.timestamp())))],
                          mb.encode(sent.octets), int(started.timestamp()))
    note = node.note(ASKED, {"request_record_id": sent.record_id, "request_event_id": event.id,
                             "request_digest": sent.digest, "responder": where["responder"],
                             "checkpoint_key": where["checkpoint"], "mailbox_key": where["mailbox_key"],
                             "relays": where["relays"], "deadline": int(deadline.timestamp())},
                     subject_ref=event.id, cites=(sent.record_id,))
    factory = _factory(relay_factory)
    try:
        mb.publish_all([factory(u) for u in where["relays"]], event)
    except mb.MailboxError as exc:
        # The request is sealed: the next poll records its absence if it never reaches the mailbox.
        raise gn.Refused(f"{exc}; the request is sealed as {sent.record_id} and polls to an absence") from exc
    return {"request_record_id": sent.record_id, "request_event_id": event.id, "asked_record_id": note.record_id,
            "deadline": int(deadline.timestamp())}


def _outcome(node: gn.Node, request_record_id: str) -> Any:
    for record in node.shelf.open(gn.NODE_BOOK).records():
        if (record.header.record_type in (RECORD_TYPE_RESPONSE, RECORD_TYPE_ABSENCE)
                and request_record_id in record.header.links_to(CITES)):
            return record
    return None


def _signed_answer(octets: bytes, digest: str, signer: str) -> bool:
    """Whether ``octets`` are an envelope or refusal ``signer`` signed for the request with ``digest``
    (the signature check a late reply gets; it is not recorded as an outcome)."""
    try:
        reply = parse_reply(Message(octets, MEDIA))
    except InvalidError:
        return False
    if reply.refusal is not None:
        r = reply.refusal
        return r.request_digest == digest and r.key_id == signer and verify_refusal_offline(r)
    e = reply.envelope
    if e is None or e.request_digest != digest or e.responder != signer:
        return False
    try:
        Ed25519PublicKey.from_public_bytes(bytes.fromhex(e.responder)).verify(bytes.fromhex(e.sig), e.signing_body())
    except (ValueError, InvalidSignature):
        return False
    return True


def poll(node: gn.Node, request_event_id: str, relay_factory: Any = None,
         now: Callable[[], datetime] | None = None) -> dict:
    """Poll the asked node's mailbox once for the reply to ``request_event_id``.

    ``status`` is ``answered`` (a reply in time, verified under the node's listed keys and recorded;
    ``outcome`` says artifact or refusal), ``pending`` (before the deadline, nothing yet), or
    ``absent`` (the deadline passed with no recorded answer: the absence is recorded against the node).
    ``late`` is true when a reply arrived after the deadline: it is signature-checked and kept beside
    the absence as a ``goose_mailbox_late`` record, and the absence is not rewritten."""
    asked = next((st for _, st in node.notes(ASKED) if st["request_event_id"] == request_event_id), None)
    if asked is None:
        raise gn.Refused("this node's book holds no such request")
    clock = now or node._clock
    arrived = clock().astimezone(timezone.utc)
    request_record = asked["request_record_id"]
    # The window closes at the request's deadline, but a book records no window shorter than
    # MINIMUM_ABSENCE_WINDOW after the request it opened with; the request was sealed a moment before
    # its deadline was counted from, so the later of the two closes it.
    sealed = from_nanos(parse_ns(node.shelf.open(gn.NODE_BOOK).get(request_record).header.committed_at))
    deadline = max(datetime.fromtimestamp(asked["deadline"], timezone.utc), sealed + MINIMUM_ABSENCE_WINDOW)
    factory = _factory(relay_factory)
    replies = mb.poll_replies([factory(u) for u in asked["relays"]], request_event_id, MEDIA, asked["mailbox_key"])
    book = node.shelf.open(gn.NODE_BOOK)
    keys = ResponderKeys(signer=asked["responder"], checkpoint=asked["checkpoint_key"])
    outcome = _outcome(node, request_record)
    if outcome is not None and outcome.header.record_type == RECORD_TYPE_RESPONSE:
        return {"status": "answered", "record_id": outcome.record_id, "outcome": outcome.header.statement["outcome"]}
    if outcome is None and arrived < deadline:
        for reply in replies:
            try:
                with node._lock:
                    record = book.record_response(request_record, Message(mb.decode(reply.content) or b"", MEDIA), keys)
            except InvalidError:
                continue  # not a reply this responder signed for this request: a transport condition
            return {"status": "answered", "record_id": record.record_id, "outcome": record.header.statement["outcome"]}
        return {"status": "pending", "deadline": int(deadline.timestamp())}
    if outcome is None:
        with node._lock:
            outcome = book.record_absence(request_record, deadline, mb.nprofile(asked["mailbox_key"], asked["relays"]))
    seen = {st["reply_event_id"] for _, st in node.notes(LATE) if st["request_event_id"] == request_event_id}
    late = None
    for reply in replies:
        octets = mb.decode(reply.content) or b""
        if reply.id in seen or not _signed_answer(octets, asked["request_digest"], asked["responder"]):
            continue
        late = node.note(LATE, {"request_event_id": request_event_id, "reply_event_id": reply.id,
                                "request_digest": asked["request_digest"],
                                "reply_digest": hashlib.sha256(octets).hexdigest(), "deadline": int(deadline.timestamp()),
                                "seen_at": int(arrived.timestamp())},
                         subject_ref=reply.id, cites=(outcome.record_id,), payloads=(octets,))
        break
    result = {"status": "absent", "record_id": outcome.record_id, "deadline": int(deadline.timestamp()),
              "late": late is not None or bool(seen)}
    if late is not None:
        result["late_record_id"] = late.record_id
    return result
