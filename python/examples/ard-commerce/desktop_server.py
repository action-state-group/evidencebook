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
    ard_pay            ->  the deal and the payment; the one tool that moves money (the presenter approves in a dialog)
``alder`` / ``kestrel``     a seller's window, its own node and keys:
    ard_seller_activity  ->  what its agent did: requests answered, the deal, the sale, its card
    ard_show_card / ard_show_receipt  ->  its own History card and its own receipt (every window has these two)
``all`` (default)     the whole story in one process, Maya's tools (one window).

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
import threading
import time
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
        self.paid: set[str] = set()       # payments the rail reported succeeded
        self.deal_ids: list[str] = []     # this run's deals, oldest first (the receipt shows the latest)
        self.attempted: set[str] = set()  # payments started: a failure mid-way is not a retry
        self.pay_lock = threading.Lock()  # one check, approval and payment at a time
        rail: pay.Rail = pay.from_env(os.environ.get("ARD_RAIL", "fake"))
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


def approve_payment(summary: str) -> bool:
    """The presenter's approval, out of band: a native dialog on the presenter's screen that the agent neither reads
    nor answers through the chat (a code returned in the tool's output would reach the agent, which could reuse it in
    the same turn). True only for the Pay button; Cancel, a timeout, no screen or any error is a refusal.
    ``ARD_APPROVAL=none`` turns the gate off (tests only; the tool says so in its output)."""
    import subprocess

    text = summary.replace("\\", " ").replace('"', "'")
    script = (f'display dialog "{text}" with title "ARD demo: approve this payment" buttons {{"Cancel", "Pay"}} '
              'default button "Cancel" cancel button "Cancel" with icon caution giving up after 180')
    try:
        run = subprocess.run(["osascript", "-e", script], capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=200)
    except (OSError, subprocess.SubprocessError):
        return False
    return run.returncode == 0 and "button returned:Pay" in run.stdout and "gave up:true" not in run.stdout


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


def _tool(*roles: str, description: str | None = None):
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
            return server.tool(description=description)(run)
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


if os.environ.get("ARD_RAIL") in ("lightning", "local"):
    _PAY = ("Step 6-7: open the deal for the picked seller and pay it over the real Lightning rail (289 msat for Alder). "
            "THIS MOVES REAL MONEY. A dialog appears on the presenter's screen and this call blocks, for up to 3 minutes, until "
            "the presenter clicks Pay or Cancel: do not retry or call it again while it waits, and tell the presenter to answer the "
            "dialog. Refused unless ard_pick sealed this seller.")
else:
    _PAY = ("Step 6-7: open the deal for the picked seller and pay it on the SIMULATED rail: no money moves. "
            "A dialog appears on the presenter's screen and this call blocks, for up to 3 minutes, until the presenter clicks Pay "
            "or Cancel: do not retry or call it again while it waits. Refused unless ard_pick sealed this seller.")


@_tool("maya", description=_PAY)
def ard_pay(seller: str) -> None:
    """Step 6-7: open the deal for the picked seller and pay it. Refused unless ard_pick sealed this seller."""
    s = session()
    key = _seller_key(seller)
    if key not in s.picked:
        raise RuntimeError(f"no sealed pick of {SELLERS[key]['label']} within its limit: call ard_pick first")
    with s.pay_lock:  # a second call waits here, then sees the seller attempted or paid
        if key in s.paid:
            raise RuntimeError(f"{SELLERS[key]['label']} was already paid in this run")
        if key in s.attempted:
            raise RuntimeError(f"a payment to {SELLERS[key]['label']} was already attempted in this run and is not confirmed: "
                               "check the wallet before trying again")
        rail_name = os.environ.get("ARD_RAIL", "fake")
        real = rail_name in ("lightning", "local")
        summary = (f"Pay {SELLERS[key]['label']} {screens.msat(s.assessed[key].price_msat)} on the "
                   + ("REAL Lightning rail: this moves real money, and it settles to the demo's shared test node or wallet."
                      if real else "simulated rail: no money moves."))
        if os.environ.get("ARD_APPROVAL") == "none":
            print("(approval gate off: ARD_APPROVAL=none)")
        elif not approve_payment(summary):
            raise RuntimeError("the presenter did not approve this payment (the approval dialog was cancelled, timed out or "
                               "could not be shown): nothing was paid. Do not retry on your own; if Maya asks again, confirm with her first.")
        s.attempted.add(key)  # before the payment: a failure mid-way must not be retried blindly (check the wallet)
    if ROLE == "all":
        s.run.pay_deal(s.assessed, key, s.picked[key])
        s.paid.add(key)
        return
    a, peer, run = s.assessed[key], s.bworld.nodes[key], s.run
    chain = buyer.open_deal(run.maya, peer, s.picked[key], a, demo.ASK)
    print(f"deal opened with {SELLERS[key]['label']}: waiting for its agent's half")
    s.deal_ids.append(chain.deal_id)
    payment = parties.buyer_deal(run.maya, peer, chain, run.rail, a.price_msat,
                                 on_paid=lambda p: s.paid.add(key) if p.status == "succeeded" else None)
    who = ("the buyer's test node paid it" if run.banner == screens.BANNER else "the buyer's wallet paid it") if run.banner != screens.BANNER_FAKE else "the simulated rail paid it (no money moved)"
    print(f"invoice {screens.msat(payment.invoice_msat)}; {who}: {payment.status}"
          + screens.fee_note(payment.fee_msat, run.banner != screens.BANNER_FAKE))
    if payment.status == "succeeded":
        s.paid.add(key)
    print(f"deal closed with {SELLERS[key]['label']}: both parties' records of the deal compared.")
    # Maya's checkpoint over her own records, taken now so her card shows this deal right away. The profile allows one a
    # minute at the soonest, so this waits for it if the last one is that recent (it is due in the demo's story).
    waited = 0
    while waited < 60:
        if run.maya.tick(datetime.now(timezone.utc)).ticked:
            print("Maya's checkpoint taken over her records: her History card now counts this deal.")
            break
        if waited == 0:
            print("Maya's last checkpoint was under a minute ago: waiting for the next one (at most a minute).")
        time.sleep(5)
        waited += 5
    else:
        print("Maya's checkpoint is not due yet: her card shows this deal after it.")
    print(f"{SELLERS[key]['label']}'s agent takes its own checkpoint after the close; its card follows.")


@_tool("alder", "kestrel")
def ard_seller_activity() -> None:
    """What this seller's agent has done: the evidence requests it answered, any deal asked of it and how it went,
    the sale and payment, and its own History card now."""
    sel = seller()
    print(f"{sel.label}: its own node, keys and book (this window only)")
    for line in list(sel.log):
        print("  " + line)
    print(f"now: {sel.card_line()}")


def _own_node():
    """This window's own node, and its label."""
    if ROLE in SELLERS:
        sel = seller()
        return sel.node, sel.label
    s = session()
    return s.run.maya, "Maya"


@_tool("maya", "alder", "kestrel")
def ard_show_card() -> None:
    """Show THIS window's own History card (standard_history/1), computed from its own sealed records: its deals
    counted, held, did not hold. Call it before the deal and again after it: a sale counts after the party's next
    checkpoint. Each window shows only its own card."""
    node, label = _own_node()
    print(parties.card_view(node, label))


@_tool("maya", "alder", "kestrel")
def ard_show_receipt() -> None:
    """Show THIS window's own receipt for the deal it took part in, read from its own sealed deal book: terms,
    steps, the payment's commitment, how it closed and its check of the other party's half."""
    node, label = _own_node()
    stored = sorted(b[len("deal/"):] for b in node.shelf.books() if b.startswith("deal/") and b.count("/") == 1)
    # This run's deals in order. A seller's book also holds its 48 sample deals, so a seller shows only deals made since
    # its window opened (a restarted window shows none); the buyer's book holds only its own deals.
    ids = seller().deal_ids if ROLE in SELLERS else (session().deal_ids or stored)
    if not ids:
        print(f"{label}: no deal yet, so no receipt")
        return
    print(parties.receipt_view(node, label, ids[-1], seller=ROLE in SELLERS,
                               real=os.environ.get("ARD_RAIL", "fake") in ("lightning", "local")))


def main() -> None:
    if ROLE in SELLERS:
        seller()  # a seller's agent runs from the moment its window opens, so the buyer finds it
    server.run()


if __name__ == "__main__":
    main()
