# SPDX-License-Identifier: Apache-2.0
"""Maya's agent: ask each seller for its evidence, check what comes back, assess each seller, seal
the presenter's pick, open the deal from the pick and pay.

Nothing here decides who is paid. :func:`assess` is the evaluator's decision for one seller, sealed
in Maya's book as that seller's *assessment*; the presenter's pick (:func:`seal_pick`) is the one
control, and it is sealed before any payment (design §5). The assessment is never offered as a
loop step-4 admission: the book it sits in is not a step-4 population."""
from __future__ import annotations

import hashlib
import json
import os
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from agent_action_capsule.canonical import jcs
from evidencebook import Coverage, EvidenceRequest, Subject
from evidencebook.evidence import evaluate
from evidencebook.request import PROFILE as ER_PROFILE, Message, parse_reply

import deal_records as dr
import deals
import goose_node as gn
import mailbox_exchange as mx
import nostr_mailbox as mb
import responder
from pack import Pack, engine
from world import CURRENCY, ITEM, RAIL, World

ASSESSED = "ard_assessment"
PICKED = "ard_pick"
CARD_RECEIVED = "ard_card_received"
#: Facts the demo operator supplies (the trust roots of the demo pack name them): the platform and
#: the one party that is witness and controller. They are the operator's, not a registry's.
OPERATOR_FACTS = {"witness": "demo-witness", "controller": "demo-controller"}


@dataclass
class Check:
    name: str
    ok: bool
    detail: str
    stated: bool = False  # the seller's own statement, not checked against records


@dataclass
class Evidence:
    seller: str
    checks: list[Check] = field(default_factory=list)
    card: dict | None = None
    selection_ok: bool = False
    anchor: str = ""
    anchor_time: str = ""
    first_checkpoint_time: str = ""
    card_record: str = ""
    sample_card: bool = False
    unanswered: list = field(default_factory=list)  # the disputes the seller states it left unanswered

    def ok(self, name: str, detail: str = "") -> None:
        self.checks.append(Check(name, True, detail))

    def stated(self, name: str, detail: str) -> None:
        self.checks.append(Check(name, True, detail, stated=True))

    def fail(self, name: str, detail: str) -> None:
        self.checks.append(Check(name, False, detail))


@dataclass
class Assessment:
    seller: str
    price_msat: int
    verdict: str
    tier: str | None
    cap_msat: int | None
    pack_id: str
    pack_digest: str
    reasons: list[str]
    rows: list[dict]
    record_id: str
    sample_card: bool
    block_sha256: str


def _request(subject: Subject, derivation: str, pin: str = "") -> EvidenceRequest:
    return EvidenceRequest(subject, Coverage(expected_pin=pin or None, min_freshness=None if pin else 0),
                           derivation=derivation, nonce=os.urandom(16).hex(), profile=ER_PROFILE)


def _publish(world: World, node: gn.Node, where: dict, request: EvidenceRequest) -> tuple[str, bytes]:
    """Seal ``request`` in Maya's book and publish it to the seller's mailbox. Returns the event id
    and the request octets as sealed (the answer is bound to their digest)."""
    started = node._clock().astimezone(timezone.utc).replace(microsecond=0)
    sent = node.ask(request, where["responder"])
    event = mb.sign_event(mb.new_secret(), mb.REQUEST_KIND,
                          [("p", where["mailbox_key"]), ("m", mx.MEDIA),
                           ("expiration", str(int(started.timestamp()) + where["answers_within"]))],
                          mb.encode(sent.octets), int(started.timestamp()))
    mb.publish_all([world.factory(u) for u in where["relays"]], event)
    return event.id, sent.octets


def _await(world: World, where: dict, event_id: str, seconds: float = 20.0) -> bytes | None:
    stop = time.time() + seconds
    while time.time() < stop:
        replies = mb.poll_replies([world.factory(u) for u in where["relays"]], event_id, mx.MEDIA, where["mailbox_key"])
        if replies:
            return mb.decode(replies[0].content)
        time.sleep(0.3)
    return None


def _envelope_checks(ev: Evidence, name: str, octets: bytes, reply: bytes, seller_key: str, derivation: str) -> Any:
    """Verify ``reply`` as the staple of ``octets`` under the seller's key (signature, request digest,
    derivation, anchor and form). Returns the checked staple or ``None``."""
    try:
        checked = gn.verify_history_staple({"request": octets.decode(), "reply": reply.decode()}, seller_key, derivation)
    except Exception as exc:
        ev.fail(name, str(exc))
        return None
    ev.ok(name, f"signed by {seller_key[:8]}…, answers this request, as of anchor {checked['anchor'][:8]}…")
    return checked


def gather(world: World, maya: gn.Node, entry: dict, anchor: str, wait: float = 20.0) -> Evidence:
    """Ask one seller for its history card and selection over its mailbox, and check each answer."""
    ev = Evidence(entry["id"])
    if entry.get("problem") or "commitment" not in entry:
        ev.fail("commitment", entry.get("problem", "no commitment"))
        return ev
    commitment = entry["commitment"]
    ev.ok("commitment", f"signature verifies under {commitment['key_id'][:8]}…")
    ev.ok("catalogue digest", "matches the served commitment" if entry["digest_matches"] else "differs (a carrier fact only)")
    where = mx.route(commitment)

    # 1. history_card/1 over checkpoints, through the Goose reference's own ask and poll.
    sent = mx.ask(maya, commitment, "checkpoints", derivation="history_card/1", min_freshness=0, relay_factory=world.factory)
    deadline = time.time() + wait
    got = {"status": "pending"}
    while time.time() < deadline:
        got = mx.poll(maya, sent["request_event_id"], world.factory)
        if got["status"] != "pending":
            break
        time.sleep(0.3)
    if got["status"] == "answered" and got.get("outcome") == "artifact":
        ev.ok("history_card/1", f"answered and verified under the keys the commitment lists, sealed as {got['record_id'][:8]}…")
    else:
        ev.fail("history_card/1", f"{got['status']} ({got.get('outcome', '')})")
        return ev

    # 2. standard_history/1 over full_history, pinned to the anchor the catalogue names.
    req = _request(Subject("full_history"), "standard_history/1", anchor)
    event_id, octets = _publish(world, maya, where, req)
    reply = _await(world, where, event_id, wait)
    if reply is None:
        ev.fail("standard_history/1", "no answer in time")
        return ev
    checked = _envelope_checks(ev, "standard_history/1", octets, reply, commitment["key_id"], "standard_history/1")
    if checked is None:
        return ev
    if checked["nonce"] != req.nonce:
        ev.fail("standard_history/1 nonce", "the answer is not bound to this request's nonce")
        return ev
    ev.card = checked["card"]
    ev.anchor = checked["anchor"]
    ev.card_record = maya.note(CARD_RECEIVED, {"seller": commitment["key_id"], "anchor": ev.anchor,
                                               "card_sha256": hashlib.sha256(jcs(ev.card)).hexdigest()},
                               subject_ref=commitment["key_id"]).record_id
    ev.ok("card form", f"derivation {ev.card['derivation']}, {ev.card['outcomes']['of']} deals counted")

    # 3. the selection, bound to a fresh nonce.
    sreq = _request(Subject("full_history"), "selection/1", anchor)
    sevent, soctets = _publish(world, maya, where, sreq)
    sreply = _await(world, where, sevent, wait)
    if sreply is None:
        ev.fail("selection", "no answer in time")
    else:
        try:
            env = parse_reply(Message(sreply, mx.MEDIA)).envelope
            env.verify()
            body = json.loads(env.artifact)
            good = (env.responder == commitment["key_id"] and body.get("nonce") == sreq.nonce
                    and env.signed_members()["request_digest"] == hashlib.sha256(soctets).hexdigest()
                    and env.anchor_digest == anchor and env.derivation == "selection/1"
                    and body.get("derivation") == "selection/1" and body.get("key_id") == commitment["key_id"]
                    and isinstance(body.get("unanswered_disputes"), list))
            if not good:
                raise ValueError("not this request's answer from this seller")
            ev.unanswered = body["unanswered_disputes"]
            ev.selection_ok = True
            ev.stated("selection", f"signed answer bound to this request; the seller states {len(body['unanswered_disputes'])} "
                                   "unanswered disputes (demo form: no inclusion proofs, not checked against its records)")
        except Exception as exc:
            ev.fail("selection", str(exc))
    return ev


def inputs(ev: Evidence, price_msat: int, now: datetime, anchor_info: dict) -> dict:
    """The evaluator's Inputs from what Maya verified (the Python reading of ``StandardHistoryCounts``)."""
    card = ev.card or {}
    bands = [{"currency": b["currency"], "decade": b["decade"], "subjects": b["held"] + b["did_not_hold"], "adverse": b["did_not_hold"]}
             for b in card.get("bands", []) if b["decade"] is not None]
    received = card.get("disputes", {}).get("received", {})
    c: dict[str, Any] = {"state": "verified" if ev.card else "unanswered",
                         "first_checkpoint_at": anchor_info["first_checkpoint_time"],
                         "latest_checkpoint_at": anchor_info["anchor_time"], "bands": bands,
                         "disputes": {"open": received.get("by_outcome", {}).get("open", 0), "of": received.get("of", 0)},
                         **OPERATOR_FACTS}
    return {"now": now.strftime("%Y-%m-%dT%H:%M:%SZ"), "amount": str(price_msat), "currency": "msat",
            "platform": {"release_attestation": "valid", "owner_signed": True}, "card": c,
            "selection": {"state": "verified" if ev.selection_ok else "unanswered", "unanswered_disputes": list(ev.unanswered)},
            "own": {"exchanges": 0, "settled": 0, "held_halves": 0, "conflicting": 0, "verdicts": []},
            "operator": {"deny_listed": False, "allow_listed": True}, "subject": ev.seller}


def assess(maya: gn.Node, seller: gn.Node, ev: Evidence, price_msat: int, pack: Pack, anchor_info: dict,
           sample_card: bool) -> Assessment:
    """Decide with the evaluator under ``pack`` and seal the decision in Maya's book as an
    ``ard_assessment`` note (its own record type: not a loop admission, and nothing cites it as a GO),
    carrying the pack's id and digest and whether the card it read is the demo's hand-written sample."""
    now = datetime.now(timezone.utc)
    d = evaluate(pack.path, inputs(ev, price_msat, now, anchor_info), pack_id=pack.pack_id, digest=pack.digest, engine=engine())
    cap = pack.ceiling_msat(d.tier) if d.verdict == "LIMIT" and d.tier else None
    reasons = [r["row"] for r in d.rows if r["result"] == "fail" and r["effect"] != "none"]
    sealed = maya.note(ASSESSED, {"peer": seller.key_id,
                                  "decision": d.verdict, "pack_id": pack.pack_id, "pack_digest": pack.digest, "tier": d.tier,
                                  "block_sha256": d.block_sha256, "price_msat": price_msat, "sample_card": sample_card,
                                  "limit_msat": cap}, subject_ref=seller.key_id,
                       cites=(ev.card_record,) if ev.card_record else ())
    return Assessment(ev.seller, price_msat, d.verdict, d.tier, cap, pack.pack_id, pack.digest, reasons,
                      list(d.rows), sealed.record_id, sample_card, d.block_sha256)


def seal_pick(maya: gn.Node, seller: gn.Node, chosen: Assessment, others: list[Assessment]) -> str:
    """The presenter's pick, for Maya: one record naming the seller and the price, citing every
    assessment it was made against. Sealed before any deal or payment."""
    rec = maya.note(PICKED, {"seller": seller.key_id, "price_msat": chosen.price_msat, "currency": "msat",
                             "assessment": chosen.verdict, "assessed_against": [a.record_id for a in [chosen, *others]],
                             "sample_card": chosen.sample_card},
                    subject_ref=seller.key_id, cites=tuple(a.record_id for a in [chosen, *others]))
    return rec.record_id


def open_deal(maya: gn.Node, seller: gn.Node, pick_record: str, chosen: Assessment, asked: str) -> dr.DealChain:
    """Open Maya's side of the deal from the sealed pick: the deal's first step cites the pick (which cites
    both assessments). Refused unless the pick exists and names this seller at this price. A seller whose
    assessment is not GO can be picked and no deal opens (nothing is paid above its limit)."""
    picks = [s for r, s in maya.notes(PICKED) if r.record_id == pick_record]
    if not picks or picks[0]["price_msat"] != chosen.price_msat or picks[0]["seller"] != seller.key_id:
        raise gn.Refused("no sealed pick names this seller at this price")
    if chosen.verdict != "GO":
        raise gn.Refused(f"the assessment is {chosen.verdict}"
                         + (f": deals under {chosen.cap_msat} msat" if chosen.cap_msat else "") + "; no deal opens and nothing is paid")
    terms = dr.Terms(ITEM, chosen.price_msat, CURRENCY, RAIL, False)
    chain = maya.new_deal(f"deal-{os.urandom(8).hex()}", dr.ROLE_BUYER, seller.key_id, terms)
    maya.seal(chain, chain.open(datetime.now(timezone.utc), asked), admission=pick_record)
    return chain
