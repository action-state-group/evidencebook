# SPDX-License-Identifier: Apache-2.0
"""Step 07 Ask, over the mailbox the asked node's commitment advertises.

A Goose node hosts no server. The commitment it signs names a Nostr mailbox
(``ask.nostr {mailbox_key, relays}``, binding nostr-mailbox-v0 §3 and §4.3):

* the **answering** side polls: :func:`answer_round` reads the evidence requests addressed to its
  mailbox key on those relays and answers each from its own node book with ``deal node respond``,
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

import hashlib
import os
from datetime import datetime, timedelta, timezone
from typing import Any, Callable

from ctl import Refused

import commitment as cm
import goose_node as gn
import nostr_mailbox as mb

#: The media type of the evidence request and reply the mailbox carries (evidence-interop/1, JSON).
MEDIA = "application/json"
#: Request events this node answered (node book), a request this node sent over a mailbox, and a
#: reply that arrived after its absence was recorded.
ANSWERED, ASKED, LATE = "goose_mailbox_answered", "goose_mailbox_asked", "goose_mailbox_late"
#: The records of an answer and of an absence (the library's record types, which a statement's
#: ``request_digest`` ties to the request).
RESPONSE, ABSENCE = "evidence_response", "recorded_absence"
#: The shortest waiting window an absence may record (the library's ``MinimumAbsenceWindow``).
MINIMUM_ABSENCE_WINDOW = timedelta(minutes=1)
#: What ``answers_within`` must exceed that by: the request is sealed a moment after its deadline is counted
#: from, and the book refuses a window that ends less than the minimum after the request was recorded.
SEAL_MARGIN = timedelta(seconds=30)
HISTORY_CARD_2 = "history_card/2"

__all__ = ["MEDIA", "ANSWERED", "ASKED", "LATE", "answer_round", "route", "ask", "poll"]


def _factory(relay_factory: Any) -> Callable[[str], mb.Relay]:
    return relay_factory or mb.Relay


def _time(text: str) -> datetime:
    return datetime.fromisoformat(text.replace("Z", "+00:00"))


def answer_round(node: gn.Node, relays: list[str], relay_factory: Any = None,
                 now: Callable[[], float] | None = None) -> list[str]:
    """One poll of the node's own mailbox: answer the requests addressed to it that this node has
    not answered. Returns the ids of the request events answered. A relay that cannot be reached
    is skipped; a request that is not an evidence request in a supported encoding gets no reply
    (binding §5.2), and the next round reads it again."""
    factory = _factory(relay_factory)
    answered = {r["statement"]["request_event_id"] for r in node.notes(ANSWERED)}

    def answer(octets: bytes, media: str) -> bytes | None:
        out = node._out("reply.bin")
        try:
            node._run("node", "respond", "--book", gn.NODE_BOOK, "--request", node._file("request.bin", octets),
                         "--output", out)
        except Refused:
            return None  # not a well-formed request: no reply (binding §5.2)
        reply = open(out, "rb").read()
        os.unlink(out)
        return reply

    done = mb.serve_round([factory(u) for u in relays], node.mailbox_secret, answer, answered,
                          now or time_now, with_media=True)
    for event_id in done:
        node.note(ANSWERED, {"request_event_id": event_id}, once="request_event_id", subject_ref=event_id)
    return done


def time_now() -> float:
    return datetime.now(timezone.utc).timestamp()


def route(commitment: Any) -> dict:
    """The mailbox route of a commitment this node trusts: its signature verified under the key it
    names, and the facts an asker needs from it. Refuses a commitment that names no mailbox."""
    cm.verify(commitment)
    nostr = commitment["ask"].get("nostr")
    if nostr is None:
        raise Refused("the commitment names no mailbox (ask.nostr): this node cannot be asked this way")
    cards = commitment.get("history_cards") or []
    if not cards or "checkpoint_key" not in cards[0]:
        raise Refused("the commitment names no checkpoint key to verify an answer under")
    import base64
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
    if not derivation and subject_form == "checkpoints" and HISTORY_CARD_2 in (
            commitment.get("answers", {}).get("derivations") or ()):
        derivation = HISTORY_CARD_2
    least = (MINIMUM_ABSENCE_WINDOW + SEAL_MARGIN).total_seconds()
    if where["answers_within"] < least:
        raise Refused("answers_within is shorter than the shortest waiting window an absence can record, with "
                      f"the time to seal the request ({int(least)}s)")
    started = (now or (lambda: datetime.now(timezone.utc)))().astimezone(timezone.utc).replace(microsecond=0)
    deadline = started + timedelta(seconds=where["answers_within"])
    coverage = {"expected_pin": expected_pin} if expected_pin else {"min_freshness": min_freshness}
    request = {"profile": "evidence-interop/0", "subject": {subject_form: subject_value}, "coverage": coverage,
               "nonce": os.urandom(16).hex(), "deadline": deadline.strftime("%Y-%m-%dT%H:%M:%SZ")}
    if derivation:
        request["derivation"] = derivation
    out = node._out("request.bin")
    sealed = node._run("node", "request", "--book", gn.NODE_BOOK, "--request", node._file("request.json", request),
                          "--responder", where["responder"], "--output", out)
    octets = open(out, "rb").read()
    os.unlink(out)
    event = mb.sign_event(mb.new_secret(), mb.REQUEST_KIND,
                          [("p", where["mailbox_key"]), ("m", MEDIA), ("expiration", str(int(deadline.timestamp())))],
                          mb.encode(octets), int(started.timestamp()))
    note = node.note(ASKED, {"request_record_id": sealed["record_id"], "request_event_id": event.id,
                             "request_digest": sealed["request_digest"], "responder": where["responder"],
                             "checkpoint_key": where["checkpoint"], "mailbox_key": where["mailbox_key"],
                             "relays": where["relays"], "deadline": int(deadline.timestamp())},
                     once="request_event_id", subject_ref=event.id, cites=(sealed["record_id"],))
    try:
        mb.publish_all([_factory(relay_factory)(u) for u in where["relays"]], event)
    except mb.MailboxError as exc:
        # The request is sealed: the next poll records its absence if it never reaches the mailbox.
        raise Refused(f"{exc}; the request is sealed as {sealed['record_id']} and polls to an absence") from exc
    return {"request_record_id": sealed["record_id"], "request_event_id": event.id, "asked_record_id": note["record_id"],
            "deadline": int(deadline.timestamp())}


def _outcome(node: gn.Node, request_digest: str) -> dict | None:
    for kind in (RESPONSE, ABSENCE):
        for record in node.notes(kind):
            if record["statement"].get("request_digest") == request_digest:
                return record
    return None


def _signed_answer(node: gn.Node, octets: bytes, digest: str, signer: str) -> bool:
    """Whether ``octets`` are an envelope or refusal ``signer`` signed for the request with ``digest``
    (the signature check a late reply gets; it is not recorded as an outcome)."""
    checked = node._run("node", "reply-check", "--reply", node._file("late.bin", octets),
                           "--request-digest", digest, "--signer", signer)
    return bool(checked.get("signed"))


def poll(node: gn.Node, request_event_id: str, relay_factory: Any = None,
         now: Callable[[], datetime] | None = None) -> dict:
    """Poll the asked node's mailbox once for the reply to ``request_event_id``.

    ``status`` is ``answered`` (a reply in time, verified under the node's listed keys and recorded;
    ``outcome`` says artifact or refusal), ``pending`` (before the deadline, nothing yet), or
    ``absent`` (the deadline passed with no recorded answer: the absence is recorded against the node), or
    ``transport_error`` (no relay could be read before an outcome was recorded: nothing is recorded,
    ``error`` says why, poll again). A recorded answer or absence is returned without reading the relays.
    ``late`` is true when a reply arrived after the deadline: it is signature-checked and kept beside
    the absence as a ``goose_mailbox_late`` record, and the absence is not rewritten."""
    asked_note = next((r for r in node.notes(ASKED) if r["statement"]["request_event_id"] == request_event_id), None)
    if asked_note is None:
        raise Refused("this node's book holds no such request")
    asked = asked_note["statement"]
    arrived = (now or (lambda: datetime.now(timezone.utc)))().astimezone(timezone.utc)
    # The window closes at the request's deadline, but a book records no window shorter than
    # MINIMUM_ABSENCE_WINDOW after the request it opened with; the request was sealed a moment before
    # this note, so the later of the two closes it.
    deadline = max(datetime.fromtimestamp(asked["deadline"], timezone.utc),
                   _time(asked_note["committed_at"]) + MINIMUM_ABSENCE_WINDOW)
    factory = _factory(relay_factory)
    outcome = _outcome(node, asked["request_digest"])
    if outcome is not None and outcome["record_type"] == RESPONSE:
        return {"status": "answered", "record_id": outcome["record_id"], "outcome": outcome["statement"]["outcome"]}
    try:
        replies = mb.poll_replies([factory(u) for u in asked["relays"]], request_event_id, MEDIA, asked["mailbox_key"])
    except mb.MailboxError as exc:
        if outcome is None:
            # No relay could be read: that says nothing about the asked node, so no outcome is recorded
            # and the next poll tries again.
            return {"status": "transport_error", "error": str(exc), "deadline": int(deadline.timestamp())}
        replies = []  # the absence is already recorded and stands; a late reply cannot be looked for now
    if outcome is None and arrived < deadline:
        for reply in replies:
            try:
                node._run("node", "request", "--book", gn.NODE_BOOK, "--for", asked["request_record_id"],
                             "--response", node._file("reply.bin", mb.decode(reply.content) or b""),
                             "--responder-key", asked["responder"], "--responder-checkpoint-key", asked["checkpoint_key"])
            except Refused:
                continue  # not a reply this responder signed for this request: a transport condition
            outcome = _outcome(node, asked["request_digest"])
            return {"status": "answered", "record_id": outcome["record_id"], "outcome": outcome["statement"]["outcome"]}
        return {"status": "pending", "deadline": int(deadline.timestamp())}
    if outcome is None:
        node._run("node", "request", "--book", gn.NODE_BOOK, "--for", asked["request_record_id"],
                     "--absent-until", deadline.strftime("%Y-%m-%dT%H:%M:%SZ"),
                     "--route", mb.nprofile(asked["mailbox_key"], asked["relays"]))
        outcome = _outcome(node, asked["request_digest"])
    seen = {r["statement"]["reply_event_id"] for r in node.notes(LATE)
            if r["statement"]["request_event_id"] == request_event_id}
    late = None
    for reply in replies:
        octets = mb.decode(reply.content) or b""
        if reply.id in seen or not _signed_answer(node, octets, asked["request_digest"], asked["responder"]):
            continue
        late = node.note(LATE, {"request_event_id": request_event_id, "reply_event_id": reply.id,
                                "request_digest": asked["request_digest"],
                                "reply_digest": hashlib.sha256(octets).hexdigest(), "deadline": int(deadline.timestamp()),
                                "seen_at": int(arrived.timestamp())},
                         once="reply_event_id", subject_ref=reply.id, cites=(outcome["record_id"],),
                         payloads=(octets,))
        break
    result = {"status": "absent", "record_id": outcome["record_id"], "deadline": int(deadline.timestamp()),
              "late": late is not None or bool(seen)}
    if late is not None:
        result["late_record_id"] = late["record_id"]
    return result
