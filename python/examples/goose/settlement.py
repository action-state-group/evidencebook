# SPDX-License-Identifier: Apache-2.0
"""Step 06 Settle: the node's signed count of the final settlements it is a party to on a rail.

The loop's step 06 (evidence-network/loop ``steps/06-settle``) compares each node's disclosed settlement count with
the rail's own count: ``settlement-disclosure/v1``, signed with the node's key (its ``key_id``, the producer key) over
its RFC 8785 (JCS) form without ``sig``, exactly the members the step reads. The count here comes from the node's own
deal books, never from the rail: a deal book counts once as a final settlement on ``rail`` when its opening names that
rail (``recourse.rail``) and, for a buyer, its own book holds the ``pay`` act (the rail's object exists once it
paid, whatever the outcome later says), for a seller, its own steps close it ``received`` (the only settlement
evidence a seller's book holds). The count covers every book the node holds when it is called, so
``through_sequence`` is a label, not a filter: the caller passes the rail's latest ledger sequence it observed.
This tool reads no rail and moves no money.
"""
from __future__ import annotations

import json

from evidencebook.book import OPENING_PREFIX
from evidencebook.deal.names import LOCAL_FIELD

import goose_node as gn
from commitment import _signed

PROFILE = "settlement-disclosure/v1"


def settled_deals(node: gn.Node, rail: str) -> list[str]:
    """The ids of the deals this node's own books count as final settlements on ``rail``, in book order."""
    out = []
    with node._lock:
        for book_id in sorted(node.shelf.books()):
            if not book_id.startswith("deal/") or book_id.count("/") != 1:
                continue
            payloads = [json.loads(p) for p in node._payloads(node.shelf.open(book_id))]
            bodies = [p.get("body", {}) for p in payloads]
            if not bodies or bodies[0].get("recourse", {}).get("rail") != rail:
                continue
            received = any(b.get("status") == "received" for b in bodies)
            closed = any("unchecked_actions" in b for b in bodies)
            bought = bodies[0].get("intent", {}).get("party_role", "buyer") == "buyer"
            paid = any(b.get("action") == "pay" and "reference_commitment" in b for b in bodies)
            if paid if bought else received and closed:
                out.append(book_id[len("deal/"):])
    return out


def disclosure(node: gn.Node, rail: str, through_sequence: int) -> dict:
    """The node's signed ``settlement-disclosure/v1`` for ``rail``, labelled with the rail's latest ledger
    ``through_sequence`` the caller observed (the count itself covers all the node's books)."""
    if not rail or not isinstance(rail, str):
        raise gn.Refused("rail is the rail's name, e.g. lightning")
    if isinstance(through_sequence, bool) or not isinstance(through_sequence, int) or through_sequence < 0:
        raise gn.Refused("through_sequence is the rail's ledger sequence the node observed, a whole number >= 0")
    return _signed(node, {"type": PROFILE, "node": node.operator, "key_id": node.key_id, "rail": rail,
                          "through_sequence": through_sequence, "settlements": len(settled_deals(node, rail))})


def pay_opening(node: gn.Node, deal_id: str) -> dict:
    """The opening of the buyer's ``pay`` act's ``deal_local`` field commitment, with the record it opens.

    The pay record commits its payment reference twice: the record's header commits the act's local event
    (``field_commitments.deal_local``) and the event's own nonce commits the reference
    (``body.reference_commitment``). Handing over ``{nonce, text}`` of that field lets a checker recompute both:
    ``sha256(JCS(opening))`` is the header's commitment, and the text's ``act.reference`` under the text's
    ``nonces.reference`` is the payload's ``reference_commitment``. The reference is what the caller observed on the
    rail (a settlement id or payment hash); the node discloses it only by calling this tool. Nothing here checks that it is not a preimage: pass the payment
    hash or settlement id, never a preimage.
    """
    with node._lock:
        book_id = f"deal/{deal_id}"
        if book_id not in node.shelf.books():
            raise gn.Refused(f"this node holds no deal {deal_id}")
        book = node.shelf.open(book_id)
        for record, payload in zip(book.records(), node._payloads(book)):
            body = json.loads(payload).get("body", {})
            if body.get("action") == "pay" and "reference_commitment" in body:
                held = node.originals.get(record.record_id)
                opening = next((bytes(a.content) for a in getattr(held, "artifacts", ())
                                if a.name == OPENING_PREFIX + LOCAL_FIELD), None)
                if opening is None:
                    raise gn.Refused(f"the pay record of deal {deal_id} holds no opening of its local event")
                return {"type": "pay-opening/v1", "deal_id": deal_id, "record_id": record.record_id,
                        "field": LOCAL_FIELD, "opening": json.loads(opening)}
    raise gn.Refused(f"deal {deal_id} holds no pay record: this node has not paid")
