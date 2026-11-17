#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Open the three Goose Desktop windows of the ARD demo: Maya (buyer), Alder and Kestrel (sellers).

    python goose/open-windows.py [--rail fake|lightning] [--print]

Each window is a recipe with its own embedded extension (``desktop_server.py`` with ``ARD_ROLE``), so each
party's Goose has its own keys and data directory. It opens ``goose://recipe?config=...`` links, which
Goose Desktop answers with one window each (it asks once per recipe: Trust and Execute). Open the sellers
first. Environment: ``CAPSULE_ENGINE``, ``ARD_SKILLS`` (as for ``demo.py``), ``ARD_HOME`` (optional).
``--print`` prints the links instead of opening them."""
from __future__ import annotations

import argparse
import base64
import json
import os
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
SERVER = HERE.parent / "desktop_server.py"

COMMON = """Write in English only.
Use only the tools of your own extension. Do not use any other tool: no terminal, no file tools, no session or agent listing, no tool search except to load your own extension's tools.
"""

PARTIES = {
    "alder": {
        "title": "Alder Print Co. (seller)",
        "instructions": "You are the selling agent of Alder Print Co., which sells 200 conference badges at 289 msat.\n" + COMMON +
        "Your one tool, ard_seller_activity, shows what you did: the evidence requests you answered, any deal asked of you "
        "and how it went, the sale and payment, and your History card. Call it when the user asks what happened on your side, "
        "then say in at most two short plain sentences what it showed. Do not repeat its output: the audience reads it in the tool call.",
        "prompt": "Reply in English only. Call your ard_seller_activity tool now and tell me in two short sentences what it shows.",
    },
    "kestrel": {
        "title": "Kestrel Print (seller)",
        "instructions": "You are the selling agent of Kestrel Print, which sells 200 conference badges at 268 msat.\n" + COMMON +
        "Your one tool, ard_seller_activity, shows what you did: the evidence requests you answered, any deal asked of you "
        "and how it went, and your History card. Call it when the user asks what happened on your side, "
        "then say in at most two short plain sentences what it showed. Do not repeat its output: the audience reads it in the tool call.",
        "prompt": "Reply in English only. Call your ard_seller_activity tool now and tell me in two short sentences what it shows.",
    },
    "maya": {
        "title": "Maya buys conference badges (buyer)",
        "instructions": "You are Maya's buying agent. Maya needs 200 conference badges by Friday, under 300 msat.\n" + COMMON +
        "Your tools are ard_find_sellers, ard_show_history, ard_assess, ard_pick and ard_pay. "
        "Call one tool per message from Maya, and only the one she asks for. After the tool returns, say in at most two short "
        "plain sentences what it showed. Do not repeat the tool's output: the audience reads it in the tool call. "
        "Before calling ard_pay, stop and ask Maya to confirm, in one sentence that names the seller and the amount and says {PAYS}. "
        "Call ard_pay only after she answers yes in her next message, only for the seller she named, and never in the same message as ard_pick. "
        "If a tool answers REFUSED, say so in one sentence and do not retry it.",
        "prompt": "Reply in English only. I am Maya. I need 200 conference badges by Friday, under 300 msat. Find sellers for me.",
    },
}


def link(role: str, rail: str) -> str:
    env = [f"ARD_ROLE={role}", f"ARD_RAIL={rail}", "CAPSULE_WITNESS=off",
           f"CAPSULE_ENGINE={os.environ['CAPSULE_ENGINE']}", f"ARD_SKILLS={os.environ['ARD_SKILLS']}"]
    if os.environ.get("ARD_HOME"):
        env.append(f"ARD_HOME={os.environ['ARD_HOME']}")
    p = PARTIES[role]
    pays = "that this moves real money over Lightning" if rail == "lightning" else "that no money moves, the rail is simulated"
    p = {**p, "instructions": p["instructions"].replace("{PAYS}", pays)}
    recipe = {"version": "1.0.0", "title": p["title"], "description": f"The ARD two-seller demo: {role}.",
              "instructions": p["instructions"], "prompt": p["prompt"],
              "extensions": [{"type": "stdio", "name": f"ard_{role}", "description": f"The {role} side of the ARD demo",
                              "cmd": "/usr/bin/env", "args": [*env, sys.executable, str(SERVER)], "timeout": 300}]}
    return "goose://recipe?config=" + base64.urlsafe_b64encode(json.dumps(recipe).encode()).decode()


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--rail", choices=["fake", "lightning"], default="fake")
    ap.add_argument("--print", action="store_true", dest="show")
    ap.add_argument("--only", choices=["alder", "kestrel", "maya"], help="open one window only")
    args = ap.parse_args(argv)
    for var in ("CAPSULE_ENGINE", "ARD_SKILLS"):
        if not os.environ.get(var):
            print(f"set {var} (see README.md)", file=sys.stderr)
            return 2
    for role in ([args.only] if args.only else ["alder", "kestrel", "maya"]):
        if args.show:
            print(link(role, args.rail))
        else:
            subprocess.run(["open", link(role, args.rail)], check=True)
            time.sleep(3)
    return 0


if __name__ == "__main__":
    sys.exit(main())
