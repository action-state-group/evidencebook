#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""A scripted Goose run: two Goose nodes, a buyer and a seller, each its own
``loop_server.py`` process spoken to over MCP stdio as Goose speaks to it,
run one deal end to end and write their History cards.

    python scripted_run.py OUT

OUT receives, for each node (``buyer``, ``seller``): ``<node>/`` (its home:
shelf, keys), ``<node>-card.json`` (its ``standard_history/1`` card and the
inputs it was derived from), ``<node>-history-card.json`` (``history_card/1``
for its deal's book) and ``<node>.key`` (its signing key), ``commitments/<key_id>.json`` with
``commitments/policies/<key_id>.json`` (its signed commitment and policy,
loop_advertise), ``buyer-model-call.json`` (a model call the buyer
sealed, loop_seal) and ``buyer-model-call-bundle.json`` (that record proven in the node book,
loop_book_bundle); ``buyer-opening-bundle.json`` is the buyer's admission and its record
of the deal, one bundle (loop_deal_bundle). Go's check of a
card is ``go run ./test/interop/goose-card OUT/<node>/shelf.db
OUT/<node>-card.json $(cat OUT/<node>.key)``.
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


async def run(out: Path) -> None:
    out.mkdir(parents=True, exist_ok=True)

    def params(name: str) -> StdioServerParameters:
        env = {**os.environ, "GOOSE_HOME": str(out / name), "GOOSE_OPERATOR": f"goose-{name}"}
        return StdioServerParameters(command=sys.executable, args=[str(SERVER)], env=env)

    async with stdio_client(params("buyer")) as (br, bw), stdio_client(params("seller")) as (sr, sw):
        async with ClientSession(br, bw) as bs, ClientSession(sr, sw) as ss:
            await bs.initialize()
            await ss.initialize()
            buyer, seller = Goose(bs, "buyer"), Goose(ss, "seller")
            b, s = await buyer.call("loop_join"), await seller.call("loop_join")
            await buyer.call("loop_admit", peer_key_id=s["key_id"], checkpoint_key_id=s["checkpoint_key_id"],
                             decision="GO", reason="known inference seller")
            await seller.call("loop_admit", peer_key_id=b["key_id"], checkpoint_key_id=b["checkpoint_key_id"],
                              decision="GO", reason="paying customer")

            # 01: the buyer seals a model call it made; the Capsule is the file a
            # verifier reads.
            sealed = await buyer.call("loop_seal", provider="rehearsal", model_id="rehearsal-model",
                             prompt="buy 1k tokens of inference", completion="the seller's offer, summarised",
                             out_path=str(out / "buyer-model-call.json"))

            # 05: the buyer opens the deal and pushes its half; the seller
            # answers it (offer, acceptance, commit) and pushes its own.
            opened = await buyer.call("loop_deal_open", peer_key_id=s["key_id"], item="inference: 1k tokens",
                                      price_minor=500, currency="USD", rail="lightning",
                                      asked="buy 1k tokens of inference from the seller")
            deal_id = opened["deal_id"]
            await buyer.call("loop_deal_push", deal_id=deal_id, out_path=str(out / "buyer-half.json"))
            # 04: the buyer's admission and its record of the deal that cites it, as one bundle.
            await buyer.call("loop_deal_bundle", deal_id=deal_id, out_path=str(out / "buyer-opening-bundle.json"))
            await seller.call("loop_deal_respond", request_file=str(out / "buyer-half.json"),
                              asked="sell 1k tokens of inference at the listed price")
            await seller.call("loop_deal_push", deal_id=deal_id, out_path=str(out / "seller-half.json"))
            await buyer.call("loop_deal_reconcile", peer_file=str(out / "seller-half.json"))

            # 06: the buyer pays (the reference is the caller's; no rail is
            # driven), and both close as received.
            await buyer.call("loop_settle", deal_id=deal_id, payment_ref="ln:payment-hash-of-the-invoice")
            # The exchange ends: each side asks for the other's half and shares its own;
            # the close cites the checkpoint of the half each holds.
            for me, peer in ((buyer, seller), (seller, buyer)):
                path = str(out / f"{me.name}-exchange-half.json")
                await me.call("loop_deal_end_exchange", deal_id=deal_id, out_path=path)
                await peer.call("loop_deal_reconcile", peer_file=path)
            await buyer.call("loop_deal_close", deal_id=deal_id, status="received")
            await seller.call("loop_deal_close", deal_id=deal_id, status="received")

            # Each final half to the other side: a verified close is the
            # card's stamped_by_both.
            for me, peer in ((buyer, seller), (seller, buyer)):
                path = str(out / f"{me.name}-final-half.json")
                await me.call("loop_deal_push", deal_id=deal_id, out_path=path)
                await peer.call("loop_deal_reconcile", peer_file=path)

            # 02, then the cards.
            for node, key in ((buyer, b), (seller, s)):
                await node.call("loop_tick")
                if node is buyer:  # 02: the model call, proven in the node book
                    await node.call("loop_book_bundle", book_id="goose/node", record_id=sealed["record_id"],
                                    out_path=str(out / "buyer-model-call-bundle.json"))
                await node.call("loop_probe")
                await node.call("loop_standard_history", out_path=str(out / f"{node.name}-card.json"))
                await node.call("loop_history_card", book_id=f"deal/{deal_id}",
                                out_path=str(out / f"{node.name}-history-card.json"))
                (out / f"{node.name}.key").write_text(key["key_id"])
                # 03: the node's signed commitment, laid out for the loop's nodes/commitments/.
                await node.call("loop_advertise", out_dir=str(out / "commitments"))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: scripted_run.py OUT")
    asyncio.run(run(Path(sys.argv[1])))
