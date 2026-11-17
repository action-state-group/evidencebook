# SPDX-License-Identifier: Apache-2.0
"""The sample history, run before the demo (design §7): 48 deals between each seller and the sample
buyer, real seals at real times, a synthetic payment reference labelled sample, no money moved."""
from __future__ import annotations

from datetime import datetime, timezone

import deal_records as dr
import deals
from world import CURRENCY, DEALS_EACH, ITEM, RAIL, SELLERS, World


SENTINEL_DEALS = 3


def seed(world: World, say=print, kestrel_card: str = "sample") -> None:
    buyer = world.nodes["sample-buyer"]
    for name, seller in SELLERS.items():
        node = world.nodes[name]
        have = sum(1 for b in node.shelf.books() if b.startswith("deal/") and b.count("/") == 1)
        if have >= DEALS_EACH:
            say(f"{name}: sample history already there ({have} deals)")
            continue
        if have == 0:
            deals.admit(buyer, node, reason="sample buyer, run by the demo operator")
            deals.admit(node, buyer, reason="sample buyer, run by the demo operator")
        terms = dr.Terms(ITEM, seller["price_msat"], CURRENCY, RAIL, False)
        for i in range(have, DEALS_EACH):
            deals.run_deal(buyer, node, terms, "sample deal", deals.SAMPLE_PAYMENT)
            if (i + 1) % 12 == 0:
                say(f"{name}: {i + 1}/{DEALS_EACH} sample deals")
        if name == "kestrel" and kestrel_card == "sentinel":
            ids = sorted(b[len("deal/"):] for b in node.shelf.books() if b.startswith("deal/") and b.count("/") == 1)
            for deal_id in ids[:SENTINEL_DEALS]:
                deals.note_did_not_hold(node, deal_id, "other")
            say(f"{name}: {SENTINEL_DEALS} deals carry the owner's own outcome_note (sample)")
        node.tick(datetime.now(timezone.utc))
        buyer.tick(datetime.now(timezone.utc))
