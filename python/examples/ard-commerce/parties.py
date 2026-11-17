# SPDX-License-Identifier: Apache-2.0
"""The demo's parties as separate processes: each of Maya, Alder and Kestrel opens only its own node
(its own keys and book under the demo home) and meets the others only through the *stage*: one private
relay and one catalogue server on localhost, plus a wire directory where the parties leave deal halves
for each other. Nothing else is shared.

* :class:`Stage` is started once (``python stage.py``, or by the first party that finds it missing).
* :class:`ProcessWorld` is :class:`world.World` for one party.
* :class:`Seller` answers Maya's evidence requests and her deal halves, step by step as the script's
  :func:`deals.run_deal` does for the seller's half, and keeps a log the seller's own agent can read.
* :class:`Buyer` runs Maya's half of the same deal and waits for the seller's halves.

The halves are the ``goose-deal-half/1`` documents of the Goose reference, checked by the same
:func:`deals._reconcile` as in one process."""
from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent / "goose"))

import ard  # noqa: E402
import deal_records as dr  # noqa: E402
import deals  # noqa: E402
import goose_node as gn  # noqa: E402
import mailbox_exchange as mx  # noqa: E402
import nostr_mailbox as mb  # noqa: E402
import responder  # noqa: E402
import screens  # noqa: E402
from demo_node import RECONCILED, DemoNode  # noqa: E402
from world import RELAY_NAME, SAMPLE_COUNTS, SELLERS, World  # noqa: E402

RELAY_PORT = int(os.environ.get("ARD_RELAY_PORT", "47480"))
HTTP_PORT = int(os.environ.get("ARD_STAGE_PORT", "47481"))


# -- the stage ------------------------------------------------------------------------------------

class Stage:
    """The relay and the catalogue server, over files under ``home/stage``: a seller's commitment
    (``commitments/<name>.json``) and its anchor (``anchors/<name>.json``)."""

    def __init__(self, home: Path):
        from relay import FakeRelay

        self.dir = home / "stage"
        for sub in ("commitments", "anchors", "wire"):
            (self.dir / sub).mkdir(parents=True, exist_ok=True)
        self.relay = FakeRelay.on(RELAY_PORT)
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_a):
                pass

            def do_GET(self) -> None:  # noqa: N802
                path = self.path.split("?")[0]
                body = outer.get(path)
                self.send_response(200 if body is not None else 404)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body or b"{}")))
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                self.wfile.write(body if body is not None else b"{}")

        self.httpd = ThreadingHTTPServer(("127.0.0.1", HTTP_PORT), Handler)
        self.url = f"http://127.0.0.1:{HTTP_PORT}"

    def commitments(self) -> dict[str, dict]:
        out = {}
        for name in SELLERS:
            f = self.dir / "commitments" / f"{name}.json"
            if f.is_file():
                out[name] = json.loads(f.read_bytes())
        return out

    def get(self, path: str) -> bytes | None:
        if path == ard.WELL_KNOWN:
            found = self.commitments()
            return json.dumps(ard.catalogue({n: SELLERS[n] for n in found}, found, self.url), sort_keys=True).encode()
        if path.startswith("/commitments/") and path.endswith(".json"):
            f = self.dir / "commitments" / path[len("/commitments/"):]
            if f.parent == self.dir / "commitments" and f.is_file():
                return f.read_bytes()
        return None

    def serve_forever(self) -> None:
        self.httpd.serve_forever()


def stage_up() -> bool:
    try:
        with socket.create_connection(("127.0.0.1", HTTP_PORT), timeout=0.5):
            return True
    except OSError:
        return False


def ensure_stage(home: Path) -> None:
    """Start the stage as its own process if none listens, so it outlives any one Goose window."""
    if stage_up():
        return
    subprocess.Popen([sys.executable, str(HERE / "stage.py"), str(home)], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                     stderr=subprocess.DEVNULL, start_new_session=True)
    for _ in range(50):
        if stage_up():
            return
        time.sleep(0.2)
    raise RuntimeError("the stage (relay and catalogue) did not start")


def write_atomic(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_bytes(data)
    tmp.replace(path)


def wait_file(path: Path, seconds: float) -> dict:
    stop = time.time() + seconds
    while time.time() < stop:
        if path.is_file():
            return json.loads(path.read_bytes())
        time.sleep(0.3)
    raise gn.Refused(f"no answer in {int(seconds)} s: {path.name}")


# -- one party's world ----------------------------------------------------------------------------

class ProcessWorld(World):
    """:class:`world.World` for one party: its own node, the stage's relay."""

    def __init__(self, home: str | Path, names: tuple[str, ...]):
        self.home = Path(home).expanduser()
        self.home.mkdir(parents=True, exist_ok=True)
        self.relay = None
        self.names = names
        self.nodes: dict = {}
        self.commitments: dict[str, dict] = {}
        self._stop = threading.Event()
        self._threads: list[threading.Thread] = []
        self.sample_card = {}

    def factory(self, _url: str) -> mb.Relay:
        return mb.Relay(f"ws://127.0.0.1:{RELAY_PORT}")

    def open(self) -> ProcessWorld:
        for name in self.names:
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


# -- Maya's view of a seller -----------------------------------------------------------------------

@dataclass
class Peer:
    """What Maya knows of a seller from its verified commitment and the catalogue's anchor: its keys.
    It stands where the one-process demo passes the seller's node."""

    id: str
    key_id: str
    checkpoint_key_id: str
    home: Path

    @property
    def cadence(self):
        return self

    def standard_history_anchor(self, _tick: int = 0):
        f = self.home / "stage" / "anchors" / f"{self.id}.json"
        if not f.is_file():
            raise gn.Refused(f"{self.id} has published no anchor: its agent is not running")
        return SimpleNamespace(**json.loads(f.read_bytes()))


def peers_from(stage_url: str, home: Path) -> dict[str, Peer]:
    out = {}
    for e in ard.lookup(stage_url):
        if "commitment" in e:
            c = e["commitment"]
            out[e["id"]] = Peer(e["id"], c["key_id"], mx.route(c)["checkpoint"], home)
    return out


class BuyerWorld:
    """The ``world`` :class:`demo.Run` reads in Maya's process: her node, the sellers as :class:`Peer`s, the stage's relay."""

    def __init__(self, world: ProcessWorld, peers: dict[str, Peer]):
        self.nodes = {"maya": world.nodes["maya"], **peers}
        self.factory = world.factory
        self.sample_card = world.sample_card


class NoServer:
    """Stands in for the popup server: the Goose chat is the screen."""

    def __init__(self, url: str):
        self.url = url

    def finish(self, _message: str) -> None:
        pass

    def close(self) -> None:
        pass


# -- the seller's side ----------------------------------------------------------------------------

class Seller:
    """A seller's agent: it advertises, answers the mailbox, and answers Maya's deal halves."""

    def __init__(self, world: ProcessWorld, name: str, kestrel_card: str):
        self.w, self.name, self.label = world, name, SELLERS[name]["label"]
        self.node = world.nodes[name]
        self.dir = world.home / "stage"
        self.log: list[str] = []
        self.handled: set[str] = set()
        self.deal_ids: list[str] = []
        self._lock = threading.Lock()
        world.sample_card = {"kestrel": True} if kestrel_card == "sample" else {}
        doc = world.advertise(name)
        write_atomic(self.dir / "commitments" / f"{name}.json", ard.commitment_octets(doc))
        self.publish_anchor()
        self.say(f"advertised: commitment signed by {doc['key_id'][:8]}…, price {screens.msat(SELLERS[name]['price_msat'])}")
        for target in (self._mailbox, self._deals):
            t = threading.Thread(target=target, daemon=True)
            t.start()
            world._threads.append(t)

    def say(self, line: str) -> None:
        with self._lock:
            self.log.append(f"{datetime.now().strftime('%H:%M:%S')}  {line}")

    def publish_anchor(self) -> None:
        a = self.node.cadence.standard_history_anchor(0)
        write_atomic(self.dir / "anchors" / f"{self.name}.json", json.dumps(
            {"anchor": a.anchor, "anchor_time": a.anchor_time, "first_checkpoint_time": a.first_checkpoint_time}).encode())

    def _mailbox(self) -> None:
        w = self.w
        while not w._stop.is_set():
            try:
                for _ in responder.answer_round(self.node, [RELAY_NAME], w.factory, sample=w.sample_card.get(self.name, False),
                                                sample_counts=SAMPLE_COUNTS.get(self.name)):
                    self.say("answered a signed evidence request from a buyer")
            except Exception as exc:
                print(f"[{self.name}] mailbox round: {exc}", file=sys.stderr)
            w._stop.wait(0.5)

    def _deals(self) -> None:
        inbox = self.dir / "wire" / self.name
        while not self.w._stop.is_set():
            for f in sorted(inbox.glob("*.m1.json")) if inbox.is_dir() else []:
                if f.name in self.handled:
                    continue
                self.handled.add(f.name)
                try:
                    self.deal(f.name[: -len(".m1.json")], json.loads(f.read_bytes()))
                except Exception as exc:
                    self.say(f"deal refused or failed: {exc}")
            self.w._stop.wait(0.5)

    def deal(self, deal_id: str, m1: dict) -> None:
        """The seller's half of :func:`deals.run_deal`, one message at a time."""
        node, now = self.node, lambda: datetime.now(timezone.utc)  # noqa: E731
        outbox, inbox = self.dir / "wire" / "maya", self.dir / "wire" / self.name
        maya = SimpleNamespace(key_id=m1["steps"][0]["capsule"]["key_id"], checkpoint_key_id=m1["checkpoint"]["key_id"])
        terms_in = m1["steps"][0]["payload"]["body"]
        terms = dr.Terms(terms_in["terms"]["item"], terms_in["terms"]["price_minor"], terms_in["terms"]["currency"],
                         terms_in["recourse"]["rail"], terms_in["recourse"]["refundable"])
        listed = SELLERS[self.name]["price_msat"]
        if terms.price_minor != listed:
            raise gn.Refused(f"asked {terms.price_minor} msat, not the listed {listed}: no deal")
        self.say(f"a buyer asks to buy {terms.item} at {screens.msat(terms.price_minor)}: accepted for this deal")
        deals.admit(node, maya, reason="buyer who picked this seller (demo)")
        verified = node.verify_peer_half(m1)
        schain = node.new_deal(deal_id, dr.ROLE_SELLER, verified["key_id"], terms)
        self.deal_ids.append(deal_id)
        node.open_deal(schain, schain.respond(now(), f"sell {terms.item} at the listed price", verified["records"][-1]["digest"]))
        write_atomic(outbox / f"{deal_id}.s1.json", json.dumps(node.half(deal_id)).encode())
        self.say("deal opened: its terms checked, its first half sent")
        m2 = wait_file(inbox / f"{deal_id}.m2.json", 180)
        deals._reconcile(node, m2, maya)
        self.say("the buyer's records after payment checked and stored")
        node.ask_half(deal_id)
        write_atomic(outbox / f"{deal_id}.s2.json", json.dumps(node.half(deal_id)).encode())
        deals._reconcile(node, m2, maya)
        node.close_deal(deal_id, "received")
        write_atomic(outbox / f"{deal_id}.s3.json", json.dumps(node.half(deal_id)).encode())
        m3 = wait_file(inbox / f"{deal_id}.m3.json", 180)
        deals._reconcile(node, m3, maya)
        self.say(f"deal closed: both closes name {screens.msat(terms.price_minor)}; the sale is recorded in this seller's own records")
        while not self.w._stop.is_set():  # the card counts the sale after the next checkpoint
            tick = node.tick(now())
            if tick.ticked:
                break
            self.w._stop.wait(5)
        self.publish_anchor()
        self.say(f"checkpoint taken: {self.card_line()}")

    def card_line(self) -> str:
        o = self.node.standard_history()["deals"]
        held = sum(1 for d in o if d["outcome"] == "held")
        return f"{self.label}'s card counts {len(o)} deals ({held} held)"



# -- what each party shows of its own records --------------------------------------------------------------

def card_view(node: gn.Node, label: str) -> str:
    """The party's own ``standard_history/1`` card, computed from its own sealed records as of its newest
    checkpoint (a sale counts only after that party's next checkpoint)."""
    node.tick(datetime.now(timezone.utc))  # takes the checkpoint if one is due; otherwise a no-op
    h = node.standard_history()
    card = json.loads(h["card"])
    o, when = card["outcomes"], h["as_of"]["anchor_time"]
    lines = [f"{label}: its own History card, computed from its own sealed records (checkpoint {h['tick']} at {when})",
             f"  deals counted {o['of']}: held {o['held']}, did not hold {o['did_not_hold']}, undecided {o['undecided']}"]
    both = card.get("happened", {}).get("stamped_by_both")
    if both is not None:
        lines.append(f"  deals stamped by both parties: {both} of {card['happened']['of']}")
    return "\n".join(lines)


_MILESTONES = {"offer": "offer", "commit": "commit (agreement accepted)", "pay": "payment"}


def receipt_view(node: gn.Node, label: str, deal_id: str) -> str:
    """The party's receipt for one deal, read from its own deal book and its own notes: the terms, the steps it
    sealed, the payment's commitment, how it closed and what it checked against the other party's half."""
    payloads = [json.loads(p) for p in node.deal_payloads(deal_id)]
    first = payloads[0]["body"]
    t = first["terms"]
    lines = [f"{label}: its receipt for {deal_id}, from its own sealed deal book ({len(payloads)} records)",
             f"  terms: {t['item']} at {t['price_minor']} msat, rail {first['recourse']['rail']}"]
    simulated = " (simulated rail: no money moved)" if os.environ.get("ARD_RAIL", "fake") == "fake" else ""
    for d in payloads:
        body = d.get("body", {})
        meta = d.get("x-deal-v0")
        if meta is not None:
            kind, at = meta["record_type"], meta["at"]
        else:
            kind, at = d.get("type", "record"), d.get("at", "")
        action = body.get("action")
        if kind in ("close",):
            lines.append(f"  {at}  close: outcome {body.get('outcome')}")
        elif action == "pay" and "reference_commitment" in body:  # the act, not the check before it
            lines.append(f"  {at}  payment of {body['amount_minor']} msat sent{simulated}; the payment reference is kept only as a hash "
                         f"{body['reference_commitment'][:12]}…")
        elif action in _MILESTONES and action != "pay" and meta is None and d.get("type", "").startswith("action"):
            lines.append(f"  {at}  {_MILESTONES[action]}, {body.get('amount_minor')} msat")
        elif d.get("type") == "action-outcome/v0":
            lines.append(f"  {at}  outcome: {body.get('status')}")
    ok = [s for r, s in node.notes(RECONCILED) if s["deal_id"] == deal_id]
    got = [s for r, s in node.notes(gn.HALF_RECEIVED) if s["deal_id"] == deal_id]
    lines.append(f"  the other party's records checked {len(got)} time(s); both parties' closing records state "
                 + (f"{ok[-1]['amount_minor']} msat: {'match' if ok[-1]['met'] else 'do NOT match'}" if ok else "no comparison yet"))
    return "\n".join(lines)


# -- Maya's half of the deal ----------------------------------------------------------------------

def buyer_deal(maya: gn.Node, peer: Peer, chain, rail, price_msat: int, seller_wait: float = 120) -> object:
    """Maya's half of :func:`deals.run_deal`: open, wait for the seller's half, pay, exchange halves, close.
    Returns the payment."""
    wire = peer.home / "stage" / "wire"
    now = lambda: datetime.now(timezone.utc)  # noqa: E731
    deal_id = chain.deal_id
    write_atomic(wire / peer.id / f"{deal_id}.m1.json", json.dumps(maya.half(deal_id)).encode())
    s1 = wait_file(wire / "maya" / f"{deal_id}.s1.json", seller_wait)
    deals._reconcile(maya, s1, peer)
    payment = rail.pay(price_msat)
    maya.seal(maya.deal(deal_id), maya.deal(deal_id).check_and_pay(now(), payment.reference))
    maya.ask_half(deal_id)
    write_atomic(wire / peer.id / f"{deal_id}.m2.json", json.dumps(maya.half(deal_id)).encode())
    s2 = wait_file(wire / "maya" / f"{deal_id}.s2.json", seller_wait)
    deals._reconcile(maya, s2, peer)
    deals._reconcile(maya, s2, peer)
    maya.close_deal(deal_id, "received")
    write_atomic(wire / peer.id / f"{deal_id}.m3.json", json.dumps(maya.half(deal_id)).encode())
    s3 = wait_file(wire / "maya" / f"{deal_id}.s3.json", seller_wait)
    deals._reconcile(maya, s3, peer)
    return payment
