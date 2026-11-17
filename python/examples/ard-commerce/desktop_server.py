#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The ARD two-seller demo as a Goose extension (an MCP server over stdio), so the buyer's Goose in
Goose Desktop runs the steps itself: find the sellers, show their history cards, assess them, pick one
and pay it. It drives the same :class:`demo.Run` the script demo runs; each tool returns the text that
run prints, so the screen says what the script demo says (:mod:`screens`).

    find_sellers   ->  ard_find_sellers
    evidence       ->  ard_show_history
    assessment     ->  ard_assess        (the on-stage edit is its ``max_adverse_permille``)
    pick           ->  ard_pick          (a seller over its limit ends here: no deal opens, nothing is paid)
    pay            ->  ard_pay           (the one tool that moves money; Goose asks the presenter first)

Environment: ``CAPSULE_ENGINE``, ``ARD_SKILLS`` and ``ARD_HOME`` as for ``demo.py``; ``ARD_RAIL``
(``fake`` or ``lightning``, default ``fake``) and ``ARD_KESTREL_CARD`` (``sentinel`` or ``sample``).
Seed the home first with ``demo.py seed``. See README.md ("In Goose Desktop")."""
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
import pack as packs  # noqa: E402
import pay  # noqa: E402
import screens  # noqa: E402
from world import SELLERS  # noqa: E402

server = FastMCP("ard_demo")
DEFAULT_PERMILLE = demo.DEFAULT_PERMILLE


class Session:
    """One run's state: the world, the run, what each step produced."""

    def __init__(self) -> None:
        args = types.SimpleNamespace(home=os.environ.get("ARD_HOME", demo.DEFAULT_HOME), port=0,
                                     kestrel_card=os.environ.get("ARD_KESTREL_CARD", "sentinel"))
        packs.engine(), packs.skills_checkout()
        # One server per demo home: a second Goose chat's server would wait on the first's shelf locks for minutes.
        home = Path(args.home).expanduser()
        home.mkdir(parents=True, exist_ok=True)
        self._lock = open(home / ".desktop-server.lock", "w")
        try:
            fcntl.flock(self._lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            raise RuntimeError("another Goose chat already runs the ard_demo extension on this demo home: "
                               "use that chat, or close it first") from None
        self.world, self.server = demo.prepare(args)
        try:
            if any(sum(1 for b in self.world.nodes[n].shelf.books() if b.startswith("deal/") and b.count("/") == 1) < demo.DEALS_EACH
                   for n in SELLERS):
                raise RuntimeError("no sample history yet: run `python demo.py seed` first")
            rail: pay.Rail = pay.LightningRail() if os.environ.get("ARD_RAIL") == "lightning" else pay.FakeRail()
            self.run = demo.Run(self.world, self.server, rail, True, False)
        except BaseException:
            self._lock.close()
            self.server.close()
            self.world.close()
            raise
        self.start: packs.Pack | None = None
        self.assessed: dict | None = None
        self.pack: packs.Pack | None = None
        self.picked: dict[str, str] = {}
        self.paid: set[str] = set()


_session: Session | None = None


def session() -> Session:
    global _session
    if _session is None:
        _session = Session()
    return _session


def _tool(fn):
    """A tool returns the text the run printed; a refusal is a line starting ``REFUSED:`` and changes nothing."""
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
    return server.tool()(run)


def _seller(name: str) -> str:
    key = name.strip().lower().split()[0] if name.strip() else ""
    if key not in SELLERS:
        raise ValueError(f"unknown seller {name!r}; the sellers found are " + ", ".join(SELLERS[k]["label"] for k in SELLERS))
    return key


@_tool
def ard_find_sellers() -> None:
    """Step 1-2: Maya's ask and ARD discovery. Lists the sellers found, each with its offer and whether its signed
    commitment verified. Call this first."""
    s = session()
    s.run.discover()


@_tool
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


@_tool
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


@_tool
def ard_pick(seller: str) -> None:
    """Step 5-6: seal Maya's pick of ``seller`` ("alder" or "kestrel"), citing both assessments. A seller over its
    limit ends here: no deal opens and nothing is paid. For a seller within its limit, ard_pay pays it next.
    Call after ard_assess."""
    s = session()
    if s.assessed is None:
        raise RuntimeError("call ard_assess first")
    key = _seller(seller)
    pick = s.run.seal(s.assessed, key)
    if pick is not None:
        s.picked[key] = pick
        print(f"{SELLERS[key]['label']} is within its limit. Call ard_pay to open the deal and pay it.")


@_tool
def ard_pay(seller: str) -> None:
    """Step 6-7: open the deal for the picked seller and pay it on the demo's rail. THIS MOVES MONEY (289 msat for
    Alder on the Lightning rail): the presenter must approve this call. Refused unless ard_pick sealed this seller."""
    s = session()
    key = _seller(seller)
    if key not in s.picked:
        raise RuntimeError(f"no sealed pick of {SELLERS[key]['label']} within its limit: call ard_pick first")
    if key in s.paid:
        raise RuntimeError(f"{SELLERS[key]['label']} was already paid in this run")
    s.paid.add(key)  # before the payment: a failure mid-way must not be retried blindly (check Brattain's balance)
    s.run.pay_deal(s.assessed, key, s.picked[key])


def main() -> None:
    server.run()


if __name__ == "__main__":
    main()
