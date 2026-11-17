# SPDX-License-Identifier: Apache-2.0
"""One deal between two of the demo's nodes, step by step, as ``scripted_run.py`` runs it over MCP.

The same function runs the 48 + 48 sample deals before the demo and Maya's one live deal; only the
payment reference differs (a sample deal seals a synthetic one, labelled so)."""
from __future__ import annotations

import json
import os
from datetime import datetime, timezone
from typing import Any

import deal_records as dr
import goose_node as gn
from evidencebook import deal
from demo_node import RECONCILED

SAMPLE_PAYMENT = "sample:no-payment"


def admit(node: gn.Node, peer: gn.Node, decision: str = "GO", reason: str = "", **members: Any) -> Any:
    """Seal this node's decision on ``peer`` (loop_admit), the record a deal opens under."""
    return node.note(gn.ADMITTED, {"peer": peer.key_id, "checkpoint_key_id": peer.checkpoint_key_id,
                                   "decision": decision, "reason": reason, **members})


def run_deal(buyer: gn.Node, seller: gn.Node, terms: dr.Terms, asked: str, payment_ref: str,
             deal_id: str = "", pay: Any = None, opened: Any = None) -> str:
    """Open, answer, pay, exchange halves and close one deal; both nodes seal their sides.

    ``opened`` is a deal chain the caller already opened (and sealed its first step) under its own rule.
    ``pay`` is called between the buyer's reconcile and its ``pay`` record when given (the live
    payment); its result is the payment reference sealed. Returns the deal id."""
    deal_id = deal_id or f"deal-{os.urandom(8).hex()}"
    now = lambda: datetime.now(timezone.utc)  # noqa: E731
    if opened is not None:  # Maya's deal, opened from her sealed pick
        deal_id = opened.deal_id
    else:
        chain = buyer.new_deal(deal_id, dr.ROLE_BUYER, seller.key_id, terms)
        buyer.open_deal(chain, chain.open(now(), asked))
    half = buyer.half(deal_id)
    verified = seller.verify_peer_half(half)
    schain = seller.new_deal(deal_id, dr.ROLE_SELLER, verified["key_id"], terms)
    steps = schain.respond(now(), f"sell {terms.item} at the listed price", verified["records"][-1]["digest"])
    seller.open_deal(schain, steps)
    _reconcile(buyer, seller.half(deal_id), seller)
    ref = pay() if pay else payment_ref
    buyer.seal(buyer.deal(deal_id), buyer.deal(deal_id).check_and_pay(now(), ref))
    for me, peer in ((buyer, seller), (seller, buyer)):
        me.ask_half(deal_id)
        _reconcile(peer, me.half(deal_id), me)
    # each side holds the other's half only after it asked for it (ask_half) and the other reconciled
    for me, peer in ((buyer, seller), (seller, buyer)):
        _reconcile(me, peer.half(deal_id), peer)
    buyer.close_deal(deal_id, "received")
    seller.close_deal(deal_id, "received")
    for me, peer in ((buyer, seller), (seller, buyer)):
        _reconcile(peer, me.half(deal_id), me)
    return deal_id


def _reconcile(node: gn.Node, half: dict, signer: gn.Node) -> dict:
    """loop_deal_reconcile: the counterparty's half must verify, be signed by the deal's peer and
    state the same terms; a matching half is held."""
    # A node that admitted the signer (the loop's rule) checks under the checkpoint key it pinned; Maya holds
    # no admission, only an assessment, so she checks under the key the signer's verified commitment names.
    if node.admission(signer.key_id) is not None:
        verified = node.verify_peer_half(half)
    else:
        verified = gn.verify_half(half, signer.checkpoint_key_id)
    chain = node.deal(verified["deal_id"])
    if verified["key_id"] != chain.peer:
        raise gn.Refused("the half is not signed by the peer the deal names")
    theirs = verified["records"][0]["payload"]["body"]
    mine = chain.terms
    if not (theirs["terms"] == {"item": mine.item, "price_minor": mine.price_minor, "currency": mine.currency}
            and theirs["recourse"] == {"rail": mine.rail, "refundable": mine.refundable}):
        raise gn.Refused("the halves state different terms")
    node.hold_half(verified, half)
    if verified["close_half_digest"] is not None:  # the half ends in its close: compare the two closes
        agreed = _agreed(node.deal_payloads(verified["deal_id"]))
        node.note(RECONCILED, {"deal_id": verified["deal_id"], "met": agreed == _agreed(
            [json.dumps(r["payload"]).encode() for r in verified["records"]]), "amount_minor": agreed[0],
            "currency": agreed[1]}, subject_ref=verified["deal_id"])
    return verified


def _agreed(payloads: list[bytes]) -> tuple[int, str]:
    """The amount and currency a deal's own commit or pay step names."""
    for p in payloads:
        body = json.loads(p).get("body", {})
        if body.get("action") in ("commit", "pay") and "amount_minor" in body:
            return body["amount_minor"], body["currency"]
    return (-1, "")


#: TEMPORARY (evidencebook#462, until #461's computed row status): the book owner's note, sealed once
#: after a deal's final close, that the outcome did not hold. It is the owner's own record: the card
#: counts it ``did_not_hold`` without showing who wrote it, and no counterparty or ruling confirms it.
OUTCOME_NOTE = "outcome_note"
REASONS = ("dispute_lost", "settled_against", "not_delivered", "other")


def note_did_not_hold(node: gn.Node, deal_id: str, reason: str = "other") -> str:
    """Seal this node's ``outcome_note`` on one of its own closed deals. Needs an evidencebook that has
    the record (#464); an older one refuses it as an unknown record type."""
    if reason not in REASONS:
        raise ValueError(f"reason must be one of {REASONS}")
    with node._lock:
        chain = node.deal(deal_id)
        close = chain._latest(deal.TYPE_CLOSE)
        at = datetime.now(timezone.utc)
        n = len(chain._built)
        chain._step(chain._untyped(OUTCOME_NOTE, at, {"outcome": "did_not_hold", "reason": reason}, [("confirms", close)]),
                    at, {OUTCOME_NOTE: reason})
        return node.seal(chain, chain._since(n))[0].record_id
