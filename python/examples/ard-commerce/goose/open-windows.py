#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Open the three Goose Desktop windows of the ARD demo: Maya (buyer), Alder and Kestrel (sellers).

    python goose/open-windows.py [--rail fake|lightning] [--print]

Each window is a recipe with its own embedded extension (``desktop_server.py`` with ``ARD_ROLE``), so each
party's Goose has its own keys and data directory. It opens ``goose://recipe?config=...`` links, which
Goose Desktop answers with one window each (it asks once per recipe: Trust and Execute). Open the sellers
first. Environment: ``CAPSULE_ENGINE``, ``ARD_SKILLS`` (as for ``demo.py``), ``ARD_HOME`` (optional); for ``--rail local``
``ARD_WALLET_BUYER`` and ``ARD_WALLET_SELLER`` (wallet directories) and optionally ``ARD_PAY_LOCAL`` (the ln-pay-local binary).
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

#: Each window's recipe is a thin wrapper: the party's own facts, then the skill it follows, read from the
#: evidencebook-skills checkout (ARD_SKILLS) at the commit the run sheet pins (evidencebook-skills#169).
PARTIES = {
    "alder": {
        "title": "Alder Print Co. (seller)", "skill": "sell-with-evidence",
        "facts": "You are the selling agent of Alder Print Co., which sells 200 conference badges at 289 msat.",
        "prompt": "Reply in English only. Call your ard_seller_activity tool now and tell me in two short sentences what it shows.",
    },
    "kestrel": {
        "title": "Kestrel Print (seller)", "skill": "sell-with-evidence",
        "facts": "You are the selling agent of Kestrel Print, which sells 200 conference badges at 268 msat.",
        "prompt": "Reply in English only. Call your ard_seller_activity tool now and tell me in two short sentences what it shows.",
    },
    "maya": {
        "title": "Maya buys conference badges (buyer)", "skill": "buy-with-evidence",
        "facts": "You are Maya's buying agent. Maya needs 200 conference badges by Friday, under 300 msat. "
                 "Your admission pack is the demo's, packs/ard-demo-buyer.",
        "prompt": "Reply in English only. I am Maya. I need 200 conference badges by Friday, under 300 msat (a msat is a thousandth of a satoshi: a token amount for this demo). Find sellers for me.",
    },
}


def skill_text(name: str) -> str:
    """The skill's ``SKILL.md`` from the ARD_SKILLS checkout, without its front matter, and the commit it is at."""
    root = Path(os.environ["ARD_SKILLS"])
    text = (root / "skills" / name / "SKILL.md").read_text()
    if text.startswith("---\n"):
        text = text.split("\n---\n", 1)[1]
    run = subprocess.run(["git", "-C", str(root), "rev-parse", "--short", "HEAD"], capture_output=True, text=True,
                         stdin=subprocess.DEVNULL)
    return text.strip(), (run.stdout.strip() if run.returncode == 0 else "")


def instructions(role: str, rail: str) -> str:
    """The recipe's instructions: the party's facts, this run's rail, and the skill it follows."""
    p = PARTIES[role]
    body, commit = skill_text(p["skill"])
    pays = ("This run pays over real Lightning: real money moves." if rail in ("lightning", "local")
            else "This run's payment rail is simulated: no money moves.")
    at = f" at evidencebook-skills {commit}" if commit else ""
    return f"{p['facts']}\nWrite in English only. {pays}\nFollow the {p['skill']} skill below{at}.\n\n{body}\n"


def link(role: str, rail: str) -> str:
    env = [f"ARD_ROLE={role}", f"ARD_RAIL={rail}", "CAPSULE_WITNESS=off",
           f"CAPSULE_ENGINE={os.environ['CAPSULE_ENGINE']}", f"ARD_SKILLS={os.environ['ARD_SKILLS']}"]
    if os.environ.get("ARD_HOME"):
        env.append(f"ARD_HOME={os.environ['ARD_HOME']}")
    if rail == "local" and role == "maya":  # only the buyer's window pays
        for var in ("ARD_WALLET_BUYER", "ARD_WALLET_SELLER", "ARD_PAY_LOCAL"):
            if os.environ.get(var):
                env.append(f"{var}={os.environ[var]}")
    p = PARTIES[role]
    recipe = {"version": "1.0.0", "title": p["title"], "description": f"The ARD two-seller demo: {role}, following the {p['skill']} skill.",
              "instructions": instructions(role, rail), "prompt": p["prompt"],
              "extensions": [{"type": "stdio", "name": f"ard_{role}", "description": f"The {role} side of the ARD demo",
                              "cmd": "/usr/bin/env", "args": [*env, sys.executable, str(SERVER)], "timeout": 300}]}
    return "goose://recipe?config=" + base64.urlsafe_b64encode(json.dumps(recipe).encode()).decode()


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--rail", choices=["fake", "lightning", "local"], default="fake")
    ap.add_argument("--print", action="store_true", dest="show")
    ap.add_argument("--only", choices=["alder", "kestrel", "maya"], help="open one window only")
    args = ap.parse_args(argv)
    needed = ["CAPSULE_ENGINE", "ARD_SKILLS"] + (["ARD_WALLET_BUYER", "ARD_WALLET_SELLER"] if args.rail == "local" else [])
    for var in needed:
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
