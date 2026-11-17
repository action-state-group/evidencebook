# SPDX-License-Identifier: Apache-2.0
"""The sample history, run before the demo (design §7): 48 deals between each seller and the sample
buyer, real seals at real times, a synthetic payment reference labelled sample, no money moved."""
from __future__ import annotations

from datetime import datetime, timezone

import deal_records as dr
import deals
from world import CURRENCY, DEALS_EACH, ITEM, RAIL, SELLERS, World


def seed(world: World, say=print) -> None:
    buyer = world.nodes["sample-buyer"]
    for name, seller in SELLERS.items():
        node = world.nodes[name]
        if len(node.shelf.books()) > 8:
            say(f"{name}: sample history already there ({len(node.shelf.books())} books)")
            continue
        deals.admit(buyer, node, reason="sample buyer, run by the demo operator")
        deals.admit(node, buyer, reason="sample buyer, run by the demo operator")
        terms = dr.Terms(ITEM, seller["price_msat"], CURRENCY, RAIL, False)
        for i in range(DEALS_EACH):
            deals.run_deal(buyer, node, terms, "sample deal", deals.SAMPLE_PAYMENT)
            if (i + 1) % 12 == 0:
                say(f"{name}: {i + 1}/{DEALS_EACH} sample deals")
        node.tick(datetime.now(timezone.utc))
        buyer.tick(datetime.now(timezone.utc))
