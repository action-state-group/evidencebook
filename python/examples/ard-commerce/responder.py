# SPDX-License-Identifier: Apache-2.0
"""A seller agent's answers to Maya's evidence requests, over the mailbox its commitment names.

Three kinds of request reach a seller:

* ``history_card/1`` over ``checkpoints``: answered from the node book by the Goose reference
  (:func:`mailbox_exchange.answer_round`'s own answer), signed under the node key;
* ``standard_history/1`` over ``full_history``: the node's counts card as of the anchor the request
  pins, in an envelope the node key signs for *that request* (the book itself answers this card only
  to an authenticated requester, so the seller agent answers it, as :meth:`goose_node.Node.history_staple`
  signs it for the node's own request);
* ``selection/1`` over ``full_history``: the demo's selection, the seller's signed statement of the
  disputes it has left unanswered, bound to the asker's nonce (see :func:`selection_artifact`).

Anything else is left to the book, which answers it or refuses it as the node's policy says."""
from __future__ import annotations

import json
from datetime import datetime, timezone
from typing import Any

from agent_action_capsule.evidence_request import sign_envelope
from evidencebook import MEDIA_TYPE_JSON, PROFILE as ER_PROFILE, Message, NotFoundError
from evidencebook.request import encode_envelope
from evidencebook._time import profile_time
from evidencebook.request import read_request
from evidencebook.signer import EvidenceSigner
from agent_action_capsule.canonical import jcs

import goose_node as gn
import mailbox_exchange as mx
import nostr_mailbox as mb

STANDARD = "standard_history/1"
SELECTION = "selection/1"


def card_as_of(node: gn.Node, pin: str) -> tuple[Any, dict] | None:
    """The cadence anchor and the standard card of the newest tick whose anchor is ``pin``."""
    n = 0
    while True:
        try:
            anchor = node.cadence.standard_history_anchor(n)
        except NotFoundError:
            return None
        if anchor.anchor == pin:
            return anchor, node.standard_history(anchor.tick.n)
        if anchor.tick.n <= 1:
            return None
        n = anchor.tick.n - 1


def sample_card(card: dict, did_not_hold: int) -> dict:
    """The hand-written variant of a real card: the same deals with ``did_not_hold`` of them counted
    as not holding. Written by the demo operator and signed by the seller's key; nothing recomputes it
    from records (Kestrel's flaw, until the sentinel record can show it from a real book)."""
    out = json.loads(json.dumps(card))
    band = out["bands"][0]
    decided = band["held"] + band["did_not_hold"]
    if len(out["bands"]) != 1 or band["undecided"] or did_not_hold > decided:
        raise ValueError("the sample card needs one band of decided deals")
    band.update(held=decided - did_not_hold, did_not_hold=did_not_hold)
    out["outcomes"].update(held=decided - did_not_hold, did_not_hold=did_not_hold, undecided=0)
    return out


def selection_artifact(node: gn.Node, nonce: str, now: datetime) -> bytes:
    """The demo's selection: the node's statement, bound to the asker's nonce, of the disputes with
    anyone it has left unanswered. It carries no inclusion proofs (the Goose node book keeps no such
    list); the buyer's check says so."""
    return jcs({"derivation": SELECTION, "nonce": nonce, "key_id": node.key_id,
                "unanswered_disputes": [], "as_of": profile_time(now), "inclusion_proofs": None})


def answer_round(node: gn.Node, relays: list[str], relay_factory: Any, sample: bool = False,
                 sample_counts: int | None = None) -> list[str]:
    """One poll of the seller's mailbox. Returns the ids of the requests answered."""
    with node._lock:
        answered = {st["request_event_id"] for _, st in node.notes(mx.ANSWERED)}
    book = node.shelf.open(gn.NODE_BOOK)

    def answer(octets: bytes, media: str) -> bytes | None:
        parsed = read_request(Message(octets, media))
        req = None if parsed.reason else parsed.request
        now = node._clock()
        if req is not None and req.derivation == STANDARD and req.subject.form == "full_history" \
                and req.coverage.expected_pin and req.nonce:
            found = card_as_of(node, req.coverage.expected_pin)
            if found is None:
                return None
            anchor, result = found
            card = json.loads(result["card"])
            if sample and sample_counts:
                card = sample_card(card, sample_counts)
            envelope = sign_envelope(octets, req.subject, anchor.anchor, jcs(card), signer=EvidenceSigner(node.signer),
                                     issued_at=profile_time(now), derivation=STANDARD, profile=req.profile or ER_PROFILE)
            return encode_envelope(envelope, MEDIA_TYPE_JSON).message.octets
        if req is not None and req.derivation == SELECTION and req.nonce:
            found = card_as_of(node, req.coverage.expected_pin) if req.coverage.expected_pin else None
            if found is None:
                return None  # the asker's pin is not an anchor this node has
            anchor = found[0]
            envelope = sign_envelope(octets, req.subject, anchor.anchor, selection_artifact(node, req.nonce, now),
                                     signer=EvidenceSigner(node.signer), issued_at=profile_time(now),
                                     derivation=SELECTION, profile=req.profile or ER_PROFILE)
            return encode_envelope(envelope, MEDIA_TYPE_JSON).message.octets
        try:
            with node._lock:
                reply, _ = book.respond(Message(octets, media))
        except Exception:
            return None
        return reply.message.octets

    done = mb.serve_round([relay_factory(u) for u in relays], node.mailbox_secret, answer, answered,
                          lambda: node._clock().timestamp(), with_media=True)
    for event_id in done:
        node.note(mx.ANSWERED, {"request_event_id": event_id}, subject_ref=event_id)
    return done
