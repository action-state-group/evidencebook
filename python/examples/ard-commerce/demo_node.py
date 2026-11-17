# SPDX-License-Identifier: Apache-2.0
"""A Goose node whose card counts a deal as *held* when this node checked, at reconcile, that both
closes name the same amount and currency (the claim of ``ec:deal-reconcile@1``, deal profile
``terms-result``).

The Goose reference seals no Result, so every deal on its card is ``undecided``. The Python deal
profile has no Result producer yet, so this node seals its own check as a node-book note
(:data:`RECONCILED`) and hands the card a Result document of the same shape the profile reads
(``claims[].verdict``). The Result is this node's own check, not a signed record of the results
book; the demo says so wherever it shows the card."""
from __future__ import annotations

import hashlib
import json
from datetime import timedelta
from typing import Any

from agent_action_capsule.canonical import jcs

import goose_node as gn
from evidencebook import deal

#: A note: this node compared its close with the counterparty's and found them the same (or not).
RECONCILED = "ard_deal_reconciled"


class DemoNode(gn.Node):
    #: The cadence's tick interval: the shortest the profile allows, so a card counts a sale one
    #: minute after the last tick instead of five (the stage waits for the next checkpoint).
    CADENCE_OPTIONS = {"interval": timedelta(minutes=1), "no_jitter": True}

    def _results(self, deal_id: str, extent: int) -> tuple[deal.HistoryResult, ...]:
        """The Results this node holds for ``deal_id`` among its node book's first ``extent`` records."""
        found = []
        for record, statement in self.notes(RECONCILED):
            if record.seq <= extent and statement["deal_id"] == deal_id:
                doc = {"contract": "ec:deal-reconcile@1", "deal_id": deal_id,
                       "claims": [{"claim": "closes name the same amount and currency",
                                   "verdict": "met" if statement["met"] else "not_met"}]}
                found.append(deal.HistoryResult(json.dumps(doc, sort_keys=True).encode(), True))
        return tuple(found)

    def deal_payloads(self, deal_id: str) -> list[bytes]:
        """The JCS payloads of this node's own deal book, in order."""
        return self._payloads(self.shelf.open(f"deal/{deal_id}"))

    def standard_history(self, tick: int = 0) -> dict:
        """As :meth:`goose_node.Node.standard_history`, with the Results of :meth:`_results`."""
        with self._lock:
            anchor = self.cadence.standard_history_anchor(tick)
            node_extent = anchor.extents.get(gn.NODE_BOOK, 0)
            received = self._halves_received(node_extent)
            deals = []
            for book_id, extent in sorted(anchor.extents.items()):
                if not book_id.startswith("deal/") or book_id.count("/") != 1:
                    continue
                book = self.shelf.open(book_id)
                records = book.records()[:extent]
                payloads = self._payloads(book)[:extent]
                deal_id = book_id[len("deal/"):]
                inputs = deal.HistoryInput(
                    tuple(deal.HistoryRecord(p, gn._parse_time(r.header.committed_at)) for p, r in zip(payloads, records)),
                    self._results(deal_id, node_extent), deal_id in received)
                entry = deal.standard_history_deal(inputs)
                if entry is not None:
                    deals.append(entry)
            as_of = {"anchor": anchor.anchor, "anchor_time": anchor.anchor_time,
                     "first_checkpoint_time": anchor.first_checkpoint_time}
            policy = {"digest": hashlib.sha256(jcs(gn.POLICY)).hexdigest(), "window": gn.POLICY["window"]}
            card = gn.sh.derive_card(holder_kind=gn.HOLDER_KIND, controller_class=gn.sh.CONTROLLER_CLASSES[gn.HOLDER_KIND],
                                     policy=policy, deals=deals, **as_of)
            properties = gn.sh.check_card(card, policy=policy, as_of=as_of, deals=deals)
            if not all(v == "PASS" for v in properties.values()):
                raise gn.Refused(f"the card fails its own inputs: {properties}")
            return {"card": jcs(card).decode(), "tick": anchor.tick.n, "as_of": as_of, "policy": policy,
                    "deals": deals, "properties": properties}
