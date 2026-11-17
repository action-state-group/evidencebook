#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The ARD two-seller demo: Maya buys 200 conference badges from one of two sellers found through ARD.

    python demo.py seed            # once: the sample history (48 + 48 deals), about a minute
    python demo.py run [--rail lightning]
    python demo.py reset           # forget the demo home (keys, books, sample history)

See README.md for the run sheet. Environment: ``CAPSULE_ENGINE`` (a capsule-engine binary) and
``ARD_SKILLS`` (an evidencebook-skills checkout, for compiling the pack), ``ARD_HOME`` (the demo home)."""
from __future__ import annotations

import argparse
import os
import shutil
import sys
import time
import webbrowser
from datetime import datetime, timezone
from pathlib import Path

os.environ.setdefault("CAPSULE_WITNESS", "off")  # the demo runs no witness: the operator is the witness it names

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent / "goose"))

import ard  # noqa: E402
import buyer  # noqa: E402
import deals  # noqa: E402
import pack as packs  # noqa: E402
import pay  # noqa: E402
import screens  # noqa: E402
import seed as seeding  # noqa: E402
from server import Server  # noqa: E402
from world import DEALS_EACH, ITEM, SELLERS, World  # noqa: E402

DEFAULT_HOME = "~/.config/ard-commerce-demo"
ASK = f"Maya: I need 200 conference badges by Friday, under 300 msat. ({ITEM})"
DEFAULT_PERMILLE = 50


def rule(title: str) -> None:
    print(f"\n── {title} " + "─" * max(4, 70 - len(title)))


def pause(prompt: str, auto: bool) -> None:
    if not auto:
        input(f"\n[{prompt}] ")


class Run:
    def __init__(self, world: World, server: Server, rail: pay.Rail, auto: bool, open_browser: bool):
        self.w, self.srv, self.rail, self.auto, self.open_browser = world, server, rail, auto, open_browser
        self.maya = world.nodes["maya"]
        self.banner = screens.banner("lightning" if isinstance(rail, pay.LightningRail) else "fake")
        self.started_payment = False
        self.entries: list[dict] = []
        self.evidence: dict[str, buyer.Evidence] = {}
        self.info: dict[str, dict] = {}

    # -- 1-2 ask and discover -------------------------------------------------------------

    def discover(self) -> None:
        print(self.banner)
        rule("1  Maya asks her agent")
        print(ASK)
        rule("2  ARD discovery")
        self.entries = ard.lookup(self.srv.url)
        for e in self.entries:
            print(f"  found: {e['name']:18} {screens.msat(e['offer']['price_msat']):>10}   commitment "
                  f"{'verified' if 'commitment' in e else 'REFUSED: ' + e['problem']}")

    # -- 3 evidence -----------------------------------------------------------------------

    def collect(self) -> None:
        rule("3  Evidence: Maya's library asks each seller, and checks each signed answer")
        for e in self.entries:
            node = self.w.nodes[e["id"]]
            anchor = node.cadence.standard_history_anchor(0)
            self.info[e["id"]] = {"first_checkpoint_time": anchor.first_checkpoint_time, "anchor_time": anchor.anchor_time}
            print(f"\n{e['name']}")
            ev = buyer.gather(self.w, self.maya, e, anchor.anchor)
            ev.sample_card = self.w.sample_card.get(e["id"], False)
            self.evidence[e["id"]] = ev
            if e["id"] == "kestrel" and not ev.sample_card and ev.card and ev.card["outcomes"]["did_not_hold"] == 0:
                raise SystemExit("Kestrel's card shows no deal that did not hold: seed this home with `seed --kestrel-card sentinel`")
            print("\n".join("  " + line for line in screens.check_lines(ev)))

    # -- 4 assessment ---------------------------------------------------------------------

    def assess(self, p: packs.Pack) -> dict[str, buyer.Assessment]:
        out = {}
        for e in self.entries:
            ev = self.evidence[e["id"]]
            if ev.card is None:
                raise SystemExit(f"{e['name']}: no verified card, nothing to assess")
            a = buyer.assess(self.maya, self.w.nodes[e["id"]], ev, e["offer"]["price_msat"], p, self.info[e["id"]], ev.sample_card)
            out[e["id"]] = a
            ceiling = p.ceiling_msat(a.tier) if a.tier else None
            print(f"  {e['name']:18} {screens.decision_line(a, ev, p.max_adverse_permille, ceiling)}")
            print(f"  {'':18} history: {screens.card_line(ev, ev.sample_card)}")
        return out

    def assessments(self, p: packs.Pack, heading: str) -> dict[str, buyer.Assessment]:
        rule(heading)
        print(f"pack {p.version} · digest {p.digest[:12]}… · {screens.PACK_NOTE}")
        print(f"({screens.CLAIMS_NOTE}; {screens.ASSESSMENT_NOTE})\n")
        return self.assess(p)

    # -- 4b the on-stage edit ---------------------------------------------------------------

    def edit(self, p: packs.Pack, permille: int | None) -> packs.Pack:
        rule("4b  The presenter tightens the rule (placeholder thresholds, for this demo only)")
        print(f"The pack's limit is {p.max_adverse_permille} in every 1,000 deals did not hold (placeholder), in pack {p.version}.")
        if permille is None:
            raw = input(f"New value [{DEFAULT_PERMILLE}]: ").strip() if not self.auto else ""
            permille = int(raw or DEFAULT_PERMILLE)
        if permille >= p.max_adverse_permille:
            raise SystemExit("the edit must tighten the rule (a lower value)")
        major, minor, _ = p.version.split(".")
        version = f"{major}.{int(minor) + 1}.0"
        new = packs.compile_source(packs.edited(p.source, permille, version), version)
        print(f"recompiled: pack {p.version}  digest {p.digest[:12]}…  →  pack {new.version}  digest {new.digest[:12]}…")
        print("both assessments rerun on the evidence already collected: no new request is sent")
        return new

    # -- 5 popup and pick -------------------------------------------------------------------

    def options(self, p: packs.Pack, made: dict[str, buyer.Assessment]) -> list[dict]:
        opts = []
        for e in self.entries:
            a, ev = made[e["id"]], self.evidence[e["id"]]
            ceiling = p.ceiling_msat(a.tier) if a.tier else None
            opts.append({"id": e["id"], "name": e["name"], "price": "Listed price " + screens.msat(a.price_msat), "verdict": a.verdict,
                         "decision": screens.decision_line(a, ev, p.max_adverse_permille, ceiling),
                         "card": screens.card_line(ev, ev.sample_card),
                         "button": screens.button(e["name"], a)})
        return opts

    def popup(self, p: packs.Pack, made: dict[str, buyer.Assessment]) -> str:
        rule("5  The popup: the presenter picks for Maya")
        header = {"banner": self.banner, "title": "Maya: 200 conference badges by Friday, under 300 msat. Pick a seller.",
                  "pack": f"Assessed under pack {p.version} · digest {p.digest[:12]}… · {screens.PACK_NOTE}",
                  "note": f"{screens.CLAIMS_NOTE}. {screens.ASSESSMENT_NOTE}."}
        self.srv.popup(self.options(p, made), header)
        print(f"popup: {self.srv.url}/")
        if self.open_browser:
            webbrowser.open(self.srv.url + "/")
        while not self.srv.pick_event.wait(0.5):
            pass
        return self.srv.picked  # type: ignore[return-value]

    # -- 6-7 deal, payment, close, card -----------------------------------------------------

    def settle(self, p: packs.Pack, made: dict[str, buyer.Assessment], chosen: str) -> None:
        a = made[chosen]
        seller = self.w.nodes[chosen]
        name = SELLERS[chosen]["label"]
        pick = buyer.seal_pick(self.maya, seller, a, [m for k, m in made.items() if k != chosen])
        rule("6  Pick sealed, deal and payment")
        print(f"pick sealed before any payment: {name} at {screens.msat(a.price_msat)}, citing both assessments ({pick[:10]}…)")
        if a.verdict != "GO":
            line = screens.limit_outcome(a)
            print(line)
            self.srv.finish(line)
            return
        deals.admit(seller, self.maya, reason="buyer Maya, picked by the presenter")
        chain = buyer.open_deal(self.maya, seller, pick, a, ASK)
        before = self.rail.balances()
        paid: list[pay.Payment] = []

        def send() -> str:
            self.started_payment = True
            payment = self.rail.pay(a.price_msat)
            paid.append(payment)
            who = "Brattain's wallet paid it" if self.banner != screens.BANNER_FAKE else "the simulated rail paid it (no money moved)"
            print(f"invoice {screens.msat(payment.invoice_msat)}; {who}: {payment.status} (fee {payment.fee_msat} msat)")
            return payment.reference

        deals.run_deal(self.maya, seller, chain.terms, ASK, "", opened=chain, pay=send)
        after = self.rail.balances()
        print(f"operator check, not shown as the price: balances {before} → {after}")
        rule("7  Both halves compared, deal closed, card after the next checkpoint")
        for node in (self.maya, seller):
            while True:
                tick = node.tick(datetime.now(timezone.utc))
                if tick.ticked:
                    break
                wait = (tick.due_next - datetime.now(timezone.utc)).total_seconds()
                print(f"  waiting for the next checkpoint ({max(0, int(wait))} s)")
                time.sleep(min(max(wait, 1), 10))
        card = seller.standard_history()
        o = card["deals"]
        live = len(o) - DEALS_EACH
        summary = f"{name}'s card now counts {len(o)} deals: {DEALS_EACH} sample, {live} run live ({sum(1 for d in o if d['outcome'] == 'held')} held)"
        print(summary)
        self.srv.finish(f"Paid {name} {screens.msat(paid[0].invoice_msat)} (the invoice amount). {summary}.")


def prepare(args) -> tuple[World, Server]:
    home = Path(os.environ.get("ARD_HOME", args.home)).expanduser()
    world = World(home).open()
    world.sample_card = {"kestrel": True} if args.kestrel_card == "sample" else {}
    for name in SELLERS:
        world.advertise(name)
        world.serve(name)
    server = Server(args.port)
    server.files = {f"/commitments/{n}.json": ard.commitment_octets(world.commitments[n]) for n in SELLERS}
    server.files[ard.WELL_KNOWN] = __import__("json").dumps(ard.catalogue(SELLERS, world.commitments, server.url), sort_keys=True).encode()
    return world, server


def cmd_seed(args) -> int:
    with World(Path(os.environ.get("ARD_HOME", args.home)).expanduser()) as world:
        seeding.seed(world, kestrel_card=args.kestrel_card)
    return 0


def cmd_reset(args) -> int:
    home = Path(os.environ.get("ARD_HOME", args.home)).expanduser()
    if home.exists():
        shutil.rmtree(home)
    print(f"removed {home}")
    return 0


def cmd_run(args) -> int:
    packs.engine(), packs.skills_checkout()
    world, server = prepare(args)
    run = None
    try:
        if any(sum(1 for b in world.nodes[n].shelf.books() if b.startswith("deal/") and b.count("/") == 1) < 48 for n in SELLERS):
            print("no sample history yet: run `python demo.py seed` first")
            return 2
        rail: pay.Rail = pay.LightningRail() if args.rail == "lightning" else pay.FakeRail()
        run = Run(world, server, rail, args.auto, not args.no_browser)
        run.discover()
        pause("Enter: collect the evidence", args.auto)
        run.collect()
        start = packs.starting()
        made = run.assessments(start, "4  Assessment under the starting pack")
        pause("Enter: tighten the rule on stage", args.auto)
        tight = run.edit(start, args.permille)
        made = run.assessments(tight, "4b  Assessment again under the edited pack")
        pause("Enter: open the popup", args.auto)
        chosen = run.popup(tight, made)
        run.settle(tight, made, chosen)
        return 0
    except KeyboardInterrupt:
        if run is not None and run.started_payment:
            print("\ninterrupted after the payment started: it may have settled. Check Brattain's balance "
                  "(`pay-lightning control balance`) before running again; the sample history is kept")
        else:
            print("\ninterrupted before any payment: nothing was paid; run again to start over (the sample history is kept)")
        return 130
    finally:
        server.close()
        world.close()


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("command", choices=["seed", "run", "reset"])
    ap.add_argument("--home", default=DEFAULT_HOME)
    ap.add_argument("--rail", choices=["fake", "lightning"], default="fake")
    ap.add_argument("--port", type=int, default=0)
    ap.add_argument("--auto", action="store_true", help="no pauses and the default edit (tests)")
    ap.add_argument("--no-browser", action="store_true", help="print the popup URL, do not open it")
    ap.add_argument("--permille", type=int, default=None, help="the on-stage threshold (default: asked)")
    ap.add_argument("--kestrel-card", choices=["sample", "sentinel"], default="sample")
    sys.stdout.reconfigure(line_buffering=True)
    args = ap.parse_args(argv)
    return {"seed": cmd_seed, "run": cmd_run, "reset": cmd_reset}[args.command](args)


if __name__ == "__main__":
    sys.exit(main())
