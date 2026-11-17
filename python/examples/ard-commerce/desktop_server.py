#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The ARD two-seller demo as a Goose extension (an MCP server over stdio), so a party's Goose in
Goose Desktop runs its steps itself. It drives the same :class:`demo.Run` the script demo runs; each
tool returns the text that run prints, so the screen says what the script demo says (:mod:`screens`).

``ARD_ROLE`` picks who this server is:

``maya``     the buyer's window (needs the sellers' windows running):
    ard_find_sellers   ->  Maya's ask and ARD discovery
    ard_show_history   ->  each seller's signed answers, checked, and the History cards
    ard_assess         ->  the assessment (the on-stage edit is its ``max_adverse_permille``)
    ard_pick           ->  the pick (a seller over its limit ends here: no deal opens, nothing is paid)
    ard_pay            ->  the deal and the payment; the one tool that moves money (Goose asks first)
``alder`` / ``kestrel``     a seller's window, its own node and keys:
    ard_seller_activity  ->  what its agent did: requests answered, the deal, the sale, its card
``all`` (default)     the whole story in one process, the five buyer tools (one window).

Environment: ``CAPSULE_ENGINE``, ``ARD_SKILLS`` and ``ARD_HOME`` as for ``demo.py``; ``ARD_RAIL``
(``fake`` or ``lightning``, default ``fake``) and ``ARD_KESTREL_CARD`` (``sentinel`` or ``sample``, the
same in every window). Seed the home first with ``demo.py seed``. See README.md ("In Goose Desktop")."""
from __future__ import annotations

import contextlib
import fcntl
import functools
import inspect
import io
import os
import sys
import types
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

try:  # mcp >= 2 renamed FastMCP to MCPServer
    from mcp.server.mcpserver import MCPServer as FastMCP
except ImportError:  # mcp < 2
    from mcp.server.fastmcp import FastMCP

import demo  # noqa: E402  (puts ../goose on the path and sets CAPSULE_WITNESS)
import buyer  # noqa: E402
import pack as packs  # noqa: E402
import parties  # noqa: E402
import pay  # noqa: E402
import screens  # noqa: E402
from datetime import datetime, timezone  # noqa: E402
from world import SELLERS  # noqa: E402

ROLE = os.environ.get("ARD_ROLE", "all")
if ROLE not in ("all", "maya", *SELLERS):
    raise SystemExit(f"ARD_ROLE must be all, maya, alder or kestrel, not {ROLE!r}")

server = FastMCP("ard_demo" if ROLE == "all" else f"ard_{ROLE}")
DEFAULT_PERMILLE = demo.DEFAULT_PERMILLE


def _home() -> Path:
    return Path(os.environ.get("ARD_HOME", demo.DEFAULT_HOME)).expanduser()


def _lock(role: str) -> object:
    """One server per party and demo home: a second Goose chat's server would wait on the first's shelf locks."""
    home = _home()
    home.mkdir(parents=True, exist_ok=True)
    handle = open(home / f".desktop-{role}.lock", "w")
    try:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        raise RuntimeError(f"another Goose chat already runs the {role} side of the ard demo on this demo home: "
                           "use that chat, or close it first") from None
    return handle


class Session:
    """The buyer's run (``all`` and ``maya``)."""

    def __init__(self) -> None:
        packs.engine(), packs.skills_checkout()
        self._lock = _lock(ROLE)
        self.start: packs.Pack | None = None
        self.assessed: dict | None = None
        self.pack: packs.Pack | None = None
        self.picked: dict[str, str] = {}
        self.paid: set[str] = set()
        rail: pay.Rail = pay.LightningRail() if os.environ.get("ARD_RAIL") == "lightning" else pay.FakeRail()
        kestrel_card = os.environ.get("ARD_KESTREL_CARD", "sentinel")
        if ROLE == "maya":
            self.world = parties.ProcessWorld(_home(), ("maya",)).open()
            self.world.sample_card = {"kestrel": True} if kestrel_card == "sample" else {}
            parties.ensure_stage(_home())
            self.bworld = parties.BuyerWorld(self.world, {})
            self.server = parties.NoServer(f"http://127.0.0.1:{parties.HTTP_PORT}")
            self.run = demo.Run(self.bworld, self.server, rail, True, False)
            return
        args = types.SimpleNamespace(home=str(_home()), port=0, kestrel_card=kestrel_card)
        self.world, self.server = demo.prepare(args)
        try:
            if any(sum(1 for b in self.world.nodes[n].shelf.books() if b.startswith("deal/") and b.count("/") == 1) < demo.DEALS_EACH
                   for n in SELLERS):
                raise RuntimeError("no sample history yet: run `python demo.py seed` first")
            self.run = demo.Run(self.world, self.server, rail, True, False)
        except BaseException:
            self.server.close()
            self.world.close()
            raise


_session: Session | None = None
_seller: parties.Seller | None = None


def session() -> Session:
    global _session
    if _session is None:
        _session = Session()
    return _session


def seller() -> parties.Seller:
    global _seller
    if _seller is None:
        packs.engine()
        _lock(ROLE)
        world = parties.ProcessWorld(_home(), (ROLE,)).open()
        n = world.nodes[ROLE]
        if sum(1 for b in n.shelf.books() if b.startswith("deal/") and b.count("/") == 1) < demo.DEALS_EACH:
            world.close()
            raise RuntimeError("no sample history yet: run `python demo.py seed` first")
        parties.ensure_stage(_home())
        _seller = parties.Seller(world, ROLE, os.environ.get("ARD_KESTREL_CARD", "sentinel"))
    return _seller


def _tool(*roles: str):
    """Register a tool for these roles (and for ``all``, which is the buyer's five). A tool returns the text the
    run printed; a refusal is a line starting ``REFUSED:`` and changes nothing."""
    def deco(fn):
        @functools.wraps(fn)
        def run(*args, **kwargs) -> str:
            buf = io.StringIO()
            try:
                with contextlib.redirect_stdout(buf):
                    fn(*args, **kwargs)
            except (SystemExit, Exception) as exc:  # the demo's own refusals raise SystemExit with a message
                return (buf.getvalue() + f"\nREFUSED: {exc}").strip()
            return buf.getvalue().strip()
        run.__signature__ = inspect.signature(fn).replace(return_annotation=str)  # the result is the text, not what fn returns
        run.__annotations__ = {**fn.__annotations__, "return": str}
        del run.__wrapped__
        if ROLE in roles or (ROLE == "all" and "maya" in roles):
            return server.tool()(run)
        return run
    return deco


def _seller_key(name: str) -> str:
    key = name.strip().lower().split()[0] if name.strip() else ""
    if key not in SELLERS:
        raise ValueError(f"unknown seller {name!r}; the sellers found are " + ", ".join(SELLERS[k]["label"] for k in SELLERS))
    return key


@_tool("maya")
def ard_find_sellers() -> None:
    """Step 1-2: Maya's ask and ARD discovery. Lists the sellers found, each with its offer and whether its signed
    commitment verified. Call this first."""
    s = session()
    s.run.discover()
    if ROLE == "maya":
        s.bworld.nodes.update(parties.peers_from(s.server.url, _home()))
        if len(s.run.entries) < len(SELLERS):
            print("only " + ", ".join(e["name"] for e in s.run.entries) + " answered: the other seller's window is not running")


@_tool("maya")
def ard_show_history() -> None:
    """Step 3: ask each seller found for its evidence (history card and selection, over its mailbox), check each
    signed answer, and show the History cards. Call after ard_find_sellers."""
    s = session()
    if not s.run.entries:
        raise RuntimeError("call ard_find_sellers first")
    s.run.collect()
    print("\nHistory cards")
    for e in s.run.entries:
        ev = s.run.evidence[e["id"]]
        print(f"  {e['name']:18} {screens.card_line(ev, ev.sample_card)}")


@_tool("maya")
def ard_assess(max_adverse_permille: int = DEFAULT_PERMILLE) -> None:
    """Step 4: assess each seller under the demo pack, first as written, then with the rule tightened to at most
    ``max_adverse_permille`` deals in every 1,000 not holding (placeholder thresholds, for this demo only).
    Call after ard_show_history."""
    s = session()
    if not s.run.evidence:
        raise RuntimeError("call ard_show_history first")
    s.start = s.start or packs.starting()
    s.run.assessments(s.start, "4  Assessment under the starting pack")
    s.pack = s.run.edit(s.start, max_adverse_permille)
    s.assessed = s.run.assessments(s.pack, "4b  Assessment again under the edited pack")


@_tool("maya")
def ard_pick(seller: str) -> None:
    """Step 5-6: seal Maya's pick of ``seller`` ("alder" or "kestrel"), citing both assessments. A seller over its
    limit ends here: no deal opens and nothing is paid. For a seller within its limit, ard_pay pays it next.
    Call after ard_assess."""
    s = session()
    if s.assessed is None:
        raise RuntimeError("call ard_assess first")
    key = _seller_key(seller)
    pick = s.run.seal(s.assessed, key)
    if pick is not None:
        s.picked[key] = pick
        print(f"{SELLERS[key]['label']} is within its limit. Call ard_pay to open the deal and pay it.")


@_tool("maya")
def ard_pay(seller: str) -> None:
    """Step 6-7: open the deal for the picked seller and pay it on the demo's rail. THIS MOVES MONEY (289 msat for
    Alder on the Lightning rail): the presenter must approve this call. Refused unless ard_pick sealed this seller."""
    s = session()
    key = _seller_key(seller)
    if key not in s.picked:
        raise RuntimeError(f"no sealed pick of {SELLERS[key]['label']} within its limit: call ard_pick first")
    if key in s.paid:
        raise RuntimeError(f"{SELLERS[key]['label']} was already paid in this run")
    s.paid.add(key)  # before the payment: a failure mid-way must not be retried blindly (check Brattain's balance)
    if ROLE == "all":
        s.run.pay_deal(s.assessed, key, s.picked[key])
        return
    a, peer, run = s.assessed[key], s.bworld.nodes[key], s.run
    chain = buyer.open_deal(run.maya, peer, s.picked[key], a, demo.ASK)
    print(f"deal opened with {SELLERS[key]['label']}: waiting for its agent's half")
    payment, before = parties.buyer_deal(run.maya, peer, chain, run.rail, a.price_msat)
    who = "Brattain's wallet paid it" if run.banner != screens.BANNER_FAKE else "the simulated rail paid it (no money moved)"
    print(f"invoice {screens.msat(payment.invoice_msat)}; {who}: {payment.status} (fee {payment.fee_msat} msat)")
    print(f"operator check, not shown as the price: balances {before} → {run.rail.balances()}")
    print(f"deal closed with {SELLERS[key]['label']}: both halves compared. Its card counts the sale after its next checkpoint.")
    run.maya.tick(datetime.now(timezone.utc))


@_tool("alder", "kestrel")
def ard_seller_activity() -> None:
    """What this seller's agent has done: the evidence requests it answered, any deal asked of it and how it went,
    the sale and payment, and its own History card now."""
    sel = seller()
    print(f"{sel.label}: its own node, keys and book (this window only)")
    for line in list(sel.log):
        print("  " + line)
    print(f"now: {sel.card_line()}")


def main() -> None:
    if ROLE in SELLERS:
        seller()  # a seller's agent runs from the moment its window opens, so the buyer finds it
    server.run()


if __name__ == "__main__":
    main()
