# SPDX-License-Identifier: Apache-2.0
"""The demo's four Goose nodes in one process: Maya (the buyer), Alder and Kestrel (the sellers) and
the sample buyer that ran their 48 sample deals each.

Every node is a :class:`goose_node.Node` on its own shelf under one demo home, so each keeps its own
keys and book. The sellers answer Maya's evidence requests through the one private relay
(:mod:`relay`) the way the Goose reference answers over a Nostr mailbox."""
from __future__ import annotations

import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent / "goose"))
sys.path.insert(0, str(HERE))

import commitment as cm  # noqa: E402
import deal_records as dr  # noqa: E402
import goose_node as gn  # noqa: E402
import mailbox_exchange as mx  # noqa: E402
import nostr_mailbox as mb  # noqa: E402

import responder  # noqa: E402
from demo_node import DemoNode  # noqa: E402
from relay import FakeRelay  # noqa: E402

#: The relay name the commitments carry (they accept only ``wss://``); the demo maps it to the one
#: local relay, as the Goose mailbox tests do.
RELAY_NAME = "wss://ard-demo.relay.local"
ITEM = "200 conference badges, delivered by Friday"
CURRENCY = "BTC"  # amount_minor counts msat (x-deal-v0 PROFILE section 3)
RAIL = "lightning"

SELLERS = {
    "alder": {"label": "Alder Print Co.", "price_msat": 289, "item": ITEM},
    "kestrel": {"label": "Kestrel Print", "price_msat": 268, "item": ITEM},
}
DEALS_EACH = 48


@dataclass
class Seller:
    name: str
    node: gn.Node
    commitment: dict
    #: The hand-written sample card, ``None`` when the card comes from the node's own records.
    sample_card: bool = False


class World:
    """The nodes, the relay and the sellers' answering threads."""

    def __init__(self, home: str | Path):
        self.home = Path(home).expanduser()
        self.home.mkdir(parents=True, exist_ok=True)
        self.relay = FakeRelay()
        self.nodes: dict[str, gn.Node] = {}
        self.commitments: dict[str, dict] = {}
        self._stop = threading.Event()
        self._threads: list[threading.Thread] = []
        self.sample_card = {"kestrel": True}  # the switch (demo.py --kestrel-card)

    def factory(self, _url: str) -> mb.Relay:
        return mb.Relay(self.relay.url)

    def open(self) -> World:
        for name in ("maya", "alder", "kestrel", "sample-buyer"):
            n = DemoNode.open(self.home / name, operator=f"ard-{name}")
            n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id})
            self.nodes[name] = n
        return self

    def close(self) -> None:
        self._stop.set()
        for t in self._threads:
            t.join(timeout=5)
        for n in self.nodes.values():
            n.close()
        self.relay.close()

    def __enter__(self) -> World:
        return self.open()

    def __exit__(self, *exc: object) -> None:
        self.close()

    # -- the sellers answer ------------------------------------------------------------

    def advertise(self, name: str) -> dict:
        """The seller's signed commitment: it names the mailbox Maya asks on."""
        n = self.nodes[name]
        settings = cm.Settings(relays=(RELAY_NAME,), endpoint="nostr:mailbox", answers_within="PT1H", poll_interval=1)
        doc, _ = cm.build(n, settings=settings)
        self.commitments[name] = doc
        return doc

    def serve(self, name: str) -> None:
        """Answer the requests addressed to the seller's mailbox until the world closes."""
        node = self.nodes[name]

        def loop() -> None:
            while not self._stop.is_set():
                try:
                    responder.answer_round(node, [RELAY_NAME], self.factory, sample=self.sample_card.get(name, False),
                                           sample_counts=SAMPLE_COUNTS.get(name))
                except Exception as exc:  # a failed round is retried; the demo screen shows nothing from it
                    print(f"[{name}] mailbox round: {exc}", file=sys.stderr)
                self._stop.wait(0.5)

        t = threading.Thread(target=loop, name=f"serve-{name}", daemon=True)
        t.start()
        self._threads.append(t)


#: The hand-written sample card's did_not_hold count per seller. Only a seller named here
#: is answered with a hand-written card, and only while ``World.sample_card`` says so.
SAMPLE_COUNTS = {"kestrel": 3}
