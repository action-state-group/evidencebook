#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""A scripted Goose run: two Goose nodes, a buyer and a seller, each its own
``loop_server.py`` process spoken to over MCP stdio as Goose speaks to it,
run one x-deal-v1 deal end to end and write their cards.

    python scripted_run.py OUT

Each node is a capsulectl-deal profile (``capsulectl-deal`` and ``capsule-engine`` must be
installed; ``GOOSE_CAPSULECTL_DEAL`` and ``GOOSE_CAPSULE_ENGINE`` name them). OUT receives, for each
node (``buyer``, ``seller``): ``<node>/`` (its home: profile, store, keys), ``<node>-card.json`` (its
``standard_history/3`` card as a bundle), ``<node>-history-card.json`` (its ``history_card/2`` staple)
and ``<node>.key`` (its signing key id), ``commitments/<key_id>.json`` with
``commitments/policies/<key_id>.json`` (its signed commitment and policy, loop_advertise),
``buyer-model-call.json`` (a model call the buyer sealed, loop_seal) and
``buyer-model-call-bundle.json`` (that record proven in the node book, loop_book_bundle);
``buyer-opening-bundle.json`` is the buyer's admission and its record of the deal, one bundle
(loop_deal_bundle); and the deal's files in the order they are exchanged: ``offer.json``,
``seller-agreement.json``, ``buyer-agreement.json``, ``buyer-leg.json``, ``seller-leg.json``,
``seller-delivery.json``, ``<node>-statement.json``, ``<node>-report.json``. The scripted run answers
the user's check for them (loop_deal_approve), as a person would. With GOOSE_CARD_SERVICE set to a
service file (card-service.example.json), each node also registers its card on that service
(loop_register_card) and writes ``<node>-card-statement.json``.
"""
from __future__ import annotations

import asyncio
import json
import os
import sys
from pathlib import Path

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

SERVER = Path(__file__).resolve().parent / "loop_server.py"


class Goose:
    """One node's MCP session, as Goose holds it."""

    def __init__(self, session: ClientSession, name: str):
        self.session, self.name = session, name

    async def call(self, tool: str, **arguments) -> dict:
        result = await self.session.call_tool(tool, arguments)
        text = "".join(c.text for c in result.content if getattr(c, "type", "") == "text")
        out = json.loads(text)
        if not out.get("ok"):
            raise SystemExit(f"{self.name} {tool}: {out.get('error')}")
        print(f"{self.name:6} {tool:22} {json.dumps({k: v for k, v in out.items() if k != 'ok'}, sort_keys=True)[:160]}")
        return out


async def _try_call(self, tool: str, **arguments) -> dict | None:
    """Like :meth:`Goose.call`, but a refusal is printed and returns None instead of ending the run."""
    result = await self.session.call_tool(tool, arguments)
    out = json.loads("".join(c.text for c in result.content if getattr(c, "type", "") == "text"))
    if not out.get("ok"):
        print(f"{self.name:6} {tool:22} NOT DONE (the run continues): {out.get('error')}")
        return None
    print(f"{self.name:6} {tool:22} {json.dumps({k: v for k, v in out.items() if k != 'ok'}, sort_keys=True)[:160]}")
    return out


Goose.try_call = _try_call


async def run(out: Path) -> None:
    out.mkdir(parents=True, exist_ok=True)

    def params(name: str) -> StdioServerParameters:
        env = {**os.environ, "GOOSE_HOME": str(out / name), "GOOSE_OPERATOR": f"goose-{name}",
               "GOOSE_NO_WITNESS": os.environ.get("GOOSE_NO_WITNESS", "1")}
        return StdioServerParameters(command=sys.executable, args=[str(SERVER)], env=env)

    async with stdio_client(params("buyer")) as (br, bw), stdio_client(params("seller")) as (sr, sw):
        async with ClientSession(br, bw) as bs, ClientSession(sr, sw) as ss:
            await bs.initialize()
            await ss.initialize()
            buyer, seller = Goose(bs, "buyer"), Goose(ss, "seller")
            b, s = await buyer.call("loop_join"), await seller.call("loop_join")
            await buyer.call("loop_admit", peer_key_id=s["key_id"], decision="GO", reason="known inference seller")
            await seller.call("loop_admit", peer_key_id=b["key_id"], decision="GO", reason="paying customer")

            # 01: the buyer seals a model call it made; the Capsule is the file a verifier reads.
            sealed = await buyer.call("loop_seal", provider="rehearsal", model_id="rehearsal-model",
                                      prompt="buy 1k tokens of inference", completion="the seller's offer, summarised",
                                      out_path=str(out / "buyer-model-call.json"))

            # 05: the buyer offers; the seller answers with its agreement; the buyer agrees back.
            opened = await buyer.call("loop_deal_open", peer_key_id=s["key_id"], item="inference: 1k tokens",
                                      price_minor=50, currency="USD", rail="lightning",
                                      asked="buy 1k tokens of inference from the seller", out_path=str(out / "offer.json"))
            deal_id = opened["deal_id"]
            # 04: the buyer's admission and its record of the deal that cites it, as one bundle.
            await buyer.call("loop_deal_bundle", deal_id=deal_id, out_path=str(out / "buyer-opening-bundle.json"))
            await seller.call("loop_deal_respond", request_file=str(out / "offer.json"),
                              asked="sell 1k tokens of inference at the listed price",
                              out_path=str(out / "seller-agreement.json"))
            await buyer.call("loop_deal_reconcile", peer_file=str(out / "seller-agreement.json"))
            await buyer.call("loop_deal_agree", deal_id=deal_id, out_path=str(out / "buyer-agreement.json"))
            await seller.call("loop_deal_reconcile", peer_file=str(out / "buyer-agreement.json"))

            # 06: the buyer pays (the reference is the caller's; no rail is driven). The final review pauses, as
            # it does for a price the agent picked and a rail with no card protection; the user answers it.
            paused = await buyer.call("loop_settle", deal_id=deal_id, payment_ref="payment-hash-of-the-invoice",
                                      out_path=str(out / "buyer-leg.json"))
            assert paused.get("paused"), paused
            await buyer.call("loop_deal_approve", deal_id=deal_id, check_id=paused["check_id"], choice="proceed", said="yes")
            await buyer.call("loop_settle", deal_id=deal_id, payment_ref="payment-hash-of-the-invoice",
                             out_path=str(out / "buyer-leg.json"), check_id=paused["check_id"])
            await seller.call("loop_deal_reconcile", peer_file=str(out / "buyer-leg.json"))
            await seller.call("loop_deal_received", deal_id=deal_id, amount_minor=50,
                              payment_ref="payment-hash-of-the-invoice", out_path=str(out / "seller-leg.json"))
            await buyer.call("loop_deal_reconcile", peer_file=str(out / "seller-leg.json"))
            # The seller delivers (its commit review pauses too) and the buyer keeps the act.
            delivery = await seller.call("loop_deal_deliver", deal_id=deal_id, out_path=str(out / "seller-delivery.json"))
            assert delivery.get("paused"), delivery
            await seller.call("loop_deal_approve", deal_id=deal_id, check_id=delivery["check_id"], choice="proceed", said="yes")
            await seller.call("loop_deal_deliver", deal_id=deal_id, out_path=str(out / "seller-delivery.json"),
                              check_id=delivery["check_id"])
            await buyer.call("loop_deal_reconcile", peer_file=str(out / "seller-delivery.json"))
            # Each side says how the payment row went, and keeps the other's statement.
            for me in (buyer, seller):
                await me.call("loop_deal_close", deal_id=deal_id, status="received",
                              out_path=str(out / f"{me.name}-statement.json"))
            for me, peer in ((buyer, seller), (seller, buyer)):
                await peer.call("loop_deal_reconcile", peer_file=str(out / f"{me.name}-statement.json"))

            # 02, then the cards. A report rests on a checkpoint: each waits for the cadence tick that covers it.
            await asyncio.gather(*(node.call("loop_deal_report", deal_id=deal_id,
                                             out_path=str(out / f"{node.name}-report.json"), wait_seconds=600)
                                   for node in (buyer, seller)))
            for node, key in ((buyer, b), (seller, s)):
                if node is buyer:  # 02: the model call, proven in the node book
                    await node.call("loop_book_bundle", book_id="goose/node", record_id=sealed["record_id"],
                                    out_path=str(out / "buyer-model-call-bundle.json"))
                await node.call("loop_probe")
                await node.call("loop_standard_history", out_path=str(out / f"{node.name}-card.json"))
                if os.environ.get("GOOSE_CARD_SERVICE"):
                    # Opt-in: only a host that named a service registers its card. A registration that
                    # fails is reported and never stops the run: the card is complete without it.
                    await node.try_call("loop_register_card", out_path=str(out / f"{node.name}-card-statement.json"))
                await node.call("loop_history_card", out_path=str(out / f"{node.name}-history-card.json"))
                (out / f"{node.name}.key").write_text(key["key_id"])
                # 03: the node's signed commitment, laid out for the loop's nodes/commitments/.
                await node.call("loop_advertise", out_dir=str(out / "commitments"))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: scripted_run.py OUT")
    asyncio.run(run(Path(sys.argv[1])))
