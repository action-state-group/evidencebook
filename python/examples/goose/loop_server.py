#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The Goose loop reference: a Goose extension (an MCP server over stdio)
whose node keeps everything on one evidencebook shelf (:mod:`goose_node`).

Each deal is its own book of x-deal-v0 steps; the cadence commits every
book at each tick; the node answers ``history_card/1`` for any book and
``standard_history/1`` as of a tick. An existing capsule-emit flat ledger
is imported once as a host log, nothing re-signed.

    step  tool(s)
    ----  ---------------------------------------------------------------
    01    loop_seal
    02    loop_book, loop_tick
    04    loop_admit
    05    loop_deal_open, loop_deal_respond, loop_deal_push, loop_deal_reconcile
    06    loop_settle (the buyer's pay step; no rail is driven), loop_deal_close
    07    loop_ask, loop_ask_reply (the asker's side)
    12    loop_join, loop_probe, loop_import
    cards loop_history_card, loop_standard_history

Add to ``~/.config/goose/config.yaml``::

    extensions:
      goose_loop:
        enabled: true
        type: stdio
        name: goose_loop
        description: "Goose loop steps on an evidencebook shelf"
        cmd: python3
        args: ["/path/to/python/examples/goose/loop_server.py"]
        timeout: 60
        envs:
          GOOSE_HOME: "~/.goose-evidence"
          GOOSE_OPERATOR: "acme-co"

Every tool returns one JSON object. A refusal is ``{"ok": false, "error": ...}``
and seals nothing. Nothing here moves money or opens a connection: a payment
reference is a value the caller observed, and halves and requests move as
files over the caller's own channel.
"""
from __future__ import annotations

import functools
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))

try:  # mcp >= 2 renamed FastMCP to MCPServer
    from mcp.server.mcpserver import MCPServer as FastMCP
except ImportError:  # mcp < 2
    from mcp.server.fastmcp import FastMCP

from capsule_emit.store_backed_ledger import LedgerError

from evidencebook import BookError, Coverage, EvidenceRequest, Message, ResponderKeys, Subject

import deal_records as dr
import goose_node as gn

HOME = os.environ.get("GOOSE_HOME", "~/.goose-evidence")
OPERATOR = os.environ.get("GOOSE_OPERATOR", "goose-user")
DEVELOPER = os.environ.get("GOOSE_DEVELOPER", "goose-loop@v1")

server = FastMCP(
    "goose-loop",
    instructions=(
        "Run the loop's steps on this node's evidencebook shelf: loop_join opens it, loop_admit seals a GO/NO "
        "on a peer, loop_deal_* seal and exchange each party's half of a deal (one book per deal), loop_settle "
        "seals the buyer's payment, loop_tick commits every book at a cadence tick, and loop_standard_history "
        "and loop_history_card answer the node's History cards."
    ),
)

_node: gn.Node | None = None


def node() -> gn.Node:
    """The node, opened on first use and held for the server's lifetime
    (the shelf's writer lock)."""
    global _node
    if _node is None:
        witnesses = os.environ.get("GOOSE_WITNESSES")
        _node = gn.Node.open(HOME, operator=OPERATOR, developer=DEVELOPER,
                             witnesses=json.loads(Path(witnesses).read_text()) if witnesses else None)
    return _node


def _out(**members: Any) -> str:
    return json.dumps(members, sort_keys=True, default=str)


def _refuse(error: str) -> str:
    return _out(ok=False, error=error)


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _tool(fn):
    """Run a tool, turning a refusal or bad input into ``{"ok": false}``."""
    @functools.wraps(fn)
    def run(*args, **kwargs) -> str:
        try:
            return fn(*args, **kwargs)
        except (gn.Refused, BookError, LedgerError, ValueError, OSError, KeyError) as exc:
            return _refuse(str(exc))
    return server.tool()(run)


def _write(path: str, data: bytes) -> None:
    Path(path).expanduser().write_bytes(data)


def _read_json(path: str) -> Any:
    return json.loads(Path(path).expanduser().read_text())


# -- 01 Seal ------------------------------------------------------------------------------


@_tool
def loop_seal(provider: str, model_id: str, prompt: str, completion: str, out_path: str) -> str:
    """Seal one model call Goose made: an effect-boundary record in this
    node's book, signed with its producer key in this process
    (``effect_attestation`` ``runtime_claimed``), whose ``model_attestation``
    names ``provider`` and ``model_id``. The prompt and completion are
    committed by digest and stay on this node. The Capsule is written to
    ``out_path`` as a verifier reads it."""
    sealed, capsule = node().seal_model_call(provider, model_id, prompt, completion, _now())
    _write(out_path, json.dumps(capsule, sort_keys=True, indent=2).encode())
    return _out(ok=True, step="01", record_id=sealed.record_id, capsule_id=capsule["capsule_id"], file=out_path)


# -- 12 Join, probe and import ----------------------------------------------------------


@_tool
def loop_join() -> str:
    """Open this node's shelf and seal its join record. Returns the node's
    signing key (its identity to peers), its checkpoint key and the shelf."""
    n = node()
    sealed = n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id})
    return _out(ok=True, step="12", key_id=n.key_id, checkpoint_key_id=n.checkpoint_key_id,
                shelf=str(n.home / "shelf.db"), record_id=sealed.record_id)


@_tool
def loop_import(ledger: str) -> str:
    """Import an existing capsule-emit flat ledger (a ledger.jsonl or a
    ledger store directory) into this shelf as a host log, once. Nothing is
    re-signed; the flat files move to the node's archive only after the
    shelf holds every record in order."""
    report = node().import_ledger(ledger)
    if report.get("reopen"):
        global _node
        _node.close()
        _node = None
        report = node().import_ledger(ledger)
    return _out(ok=True, step="12", **report)


@_tool
def loop_probe() -> str:
    """Reopen and check every log on the shelf: each book's records verify
    as their Capsules on open, and each deal's half verifies as a
    counterparty would check it."""
    n = node()
    books, failures = n.shelf.books(), []
    for book_id in books:
        if book_id.startswith("deal/") and book_id.count("/") == 1:
            try:
                gn.verify_half(n.half(book_id[len("deal/"):]), n.checkpoint_key_id)
            except gn.Refused as exc:
                failures.append({"book": book_id, "error": str(exc)})
    return _out(ok=not failures, step="12", books=books, imported=len(n.ledger_capsules()), failures=failures)


# -- 02 Book and the cadence ------------------------------------------------------------


@_tool
def loop_book(book_id: str) -> str:
    """A book's size and its newest checkpoint (cut now if the book grew)."""
    book = node().shelf.open(book_id)
    cp = book.checkpoint()
    return _out(ok=True, step="02", book_id=book_id, size=book.size(),
                checkpoint={"entries": cp.entries, "root": cp.root, "anchor_digest": cp.anchor_digest,
                            "key_id": cp.key_id})


@_tool
def loop_tick() -> str:
    """Run the cadence's tick if one is due: every live log's checkpoint is
    committed in the cadence checkpoint, the one a witness sees."""
    result = node().tick()
    tick = result.tick
    return _out(ok=True, step="02", ticked=result.ticked, due_next=result.due_next,
                tick=None if tick is None else {"n": tick.n, "checkpoint_size": tick.checkpoint_size})


# -- 04 Admit -----------------------------------------------------------------------------


@_tool
def loop_admit(peer_key_id: str, checkpoint_key_id: str, decision: str, reason: str = "") -> str:
    """Seal this node's GO or NO on a peer before any deal with it. The peer
    is named by its signing key, and its checkpoint key is pinned with it
    (both from its loop_join): its halves must be signed by the one and
    checkpointed under the other."""
    decision = decision.strip().upper()
    if decision not in ("GO", "NO"):
        raise ValueError("decision must be GO or NO")
    for key in (peer_key_id, checkpoint_key_id):
        if len(key) != 64 or key.strip("0123456789abcdef"):
            raise ValueError("peer_key_id and checkpoint_key_id are the peer's 64-hex keys")
    sealed = node().note(gn.ADMITTED, {"peer": peer_key_id, "checkpoint_key_id": checkpoint_key_id,
                                       "decision": decision, "reason": reason})
    return _out(ok=True, step="04", record_id=sealed.record_id, peer=peer_key_id, decision=decision)


def _admitted(peer: str) -> None:
    admission = node().admission(peer)
    if admission is None:
        raise gn.Refused(f"no admission sealed for {peer}: call loop_admit first")
    if admission["decision"] != "GO":
        raise gn.Refused(f"the sealed admission for {peer} is {admission['decision']}: no deal")


# -- 05 Deal --------------------------------------------------------------------------------


@_tool
def loop_deal_open(peer_key_id: str, item: str, price_minor: int, currency: str, rail: str, asked: str,
                   refundable: bool = False, deal_id: str = "") -> str:
    """Open a deal as the buyer with an admitted peer: its book's first step
    (the baseline: what was asked, of whom, on which terms). ``asked`` is the
    user's own words; it stays on this node."""
    _admitted(peer_key_id)
    n = node()
    deal_id = deal_id or f"deal-{os.urandom(8).hex()}"
    chain = n.new_deal(deal_id, dr.ROLE_BUYER, peer_key_id, dr.Terms(item, price_minor, currency, rail, refundable))
    sealed = n.seal(chain, chain.open(_now(), asked))
    return _out(ok=True, step="05", deal_id=deal_id, records=[s.record_id for s in sealed])


@_tool
def loop_deal_push(deal_id: str, out_path: str) -> str:
    """Write this node's half of a deal (every step's Capsule and record, and
    the book's checkpoint) to a file for the counterparty."""
    half = node().half(deal_id)
    _write(out_path, json.dumps(half, sort_keys=True).encode())
    return _out(ok=True, step="05", deal_id=deal_id, steps=len(half["steps"]), file=out_path)


@_tool
def loop_deal_respond(request_file: str, asked: str) -> str:
    """Answer a buyer's pushed half as the seller: the half must verify and
    come from a peer admitted with GO. Seals this node's side of the same
    deal: its baseline at the buyer's terms, the task authority, the offer,
    the buyer's acceptance (its half, by digest) and the commit. An answer
    cut short part-way is finished by calling it again."""
    n = node()
    verified = n.verify_peer_half(_read_json(request_file))
    baseline = verified["records"][0]["payload"]
    if baseline["x-deal-v0"]["record_type"] != "baseline" or baseline["body"]["intent"].get("party_role"):
        raise gn.Refused("the half does not open a deal as a buyer")
    body = baseline["body"]
    terms = dr.Terms(body["terms"]["item"], body["terms"]["price_minor"], body["terms"]["currency"],
                     body["recourse"]["rail"], body["recourse"]["refundable"])
    deal_id = verified["deal_id"]
    if n.has_deal(deal_id):
        # An answer cut short part-way: the steps it sealed stay, the rest follow.
        chain = n.deal(deal_id)
        if chain.role != dr.ROLE_SELLER or chain.peer != verified["key_id"] or chain.terms != terms:
            raise gn.Refused(f"deal {deal_id} is already open on this node with another party or other terms")
    else:
        chain = n.new_deal(deal_id, dr.ROLE_SELLER, verified["key_id"], terms)
    steps = chain.respond(_now(), asked, verified["records"][-1]["digest"])
    if not steps:
        raise gn.Refused(f"deal {deal_id} is already answered")
    sealed = n.seal(chain, steps)
    return _out(ok=True, step="05", deal_id=deal_id, records=[s.record_id for s in sealed])


@_tool
def loop_deal_reconcile(peer_file: str) -> str:
    """Check the counterparty's pushed half of a deal this node holds: it
    must verify, be signed by the peer the deal names and state the same
    terms. A half that ends in a close is recorded as received (the
    card's ``stamped_by_both``)."""
    n = node()
    verified = n.verify_peer_half(_read_json(peer_file))
    deal_id = verified["deal_id"]
    chain = n.deal(deal_id)
    if verified["key_id"] != chain.peer:
        raise gn.Refused(f"the half is signed by {verified['key_id']}, not the peer deal {deal_id} names")
    theirs = verified["records"][0]["payload"]["body"]
    mine = chain.terms
    same = (theirs["terms"] == {"item": mine.item, "price_minor": mine.price_minor, "currency": mine.currency}
            and theirs["recourse"] == {"rail": mine.rail, "refundable": mine.refundable})
    state = "matched" if same else "contradicted"
    recorded = None
    if same and verified["close_half_digest"]:
        recorded = n.note(gn.HALF_RECEIVED, {"deal_id": deal_id, "key_id": verified["key_id"],
                                             "close_half_digest": verified["close_half_digest"]},
                          subject_ref=deal_id).record_id
    return _out(ok=True, step="05", deal_id=deal_id, state=state, steps=len(verified["records"]),
                closed=verified["close_half_digest"] is not None, recorded=recorded)


# -- 06 Settle and close ----------------------------------------------------------------------


@_tool
def loop_settle(deal_id: str, payment_ref: str) -> str:
    """Seal the buyer's payment: the check, its verdict, the standing
    intent's approval and the ``pay`` act. ``payment_ref`` is the reference
    the caller observed on the rail (committed, never sealed in the clear);
    this tool drives no rail and moves no money."""
    n = node()
    chain = n.deal(deal_id)
    sealed = n.seal(chain, chain.check_and_pay(_now(), payment_ref))
    return _out(ok=True, step="06", deal_id=deal_id, records=[s.record_id for s in sealed])


@_tool
def loop_deal_close(deal_id: str, status: str) -> str:
    """End a deal: the outcome observed (``received`` or ``not_received``)
    and the close. A received close commits the agreed price and currency."""
    n = node()
    chain = n.deal(deal_id)
    sealed = n.seal(chain, chain.close(_now(), status))
    return _out(ok=True, step="06", deal_id=deal_id, records=[s.record_id for s in sealed])


# -- 07 Ask ---------------------------------------------------------------------------------


@_tool
def loop_ask(responder: str, subject_form: str, out_path: str, subject_value: str = "", expected_pin: str = "",
             min_freshness: str = "", derivation: str = "") -> str:
    """Seal an evidence request (draft-mih-agent-evidence-request) in this
    node's book before it is sent, then write its octets to ``out_path``.
    Exactly one of ``expected_pin`` or ``min_freshness`` sets the coverage."""
    content = json.loads(subject_value) if subject_value else None
    floor: int | str | None = None
    if min_freshness:
        floor = int(min_freshness) if min_freshness.isdigit() else min_freshness
    request = EvidenceRequest(Subject(subject_form, content), Coverage(expected_pin=expected_pin or None,
                                                                       min_freshness=floor),
                              derivation=derivation or None)
    sent = node().ask(request, responder)
    _write(out_path, sent.octets)
    return _out(ok=True, step="07", record_id=sent.record_id, file=out_path)


@_tool
def loop_ask_reply(request_record_id: str, reply_file: str, responder_key: str, checkpoint_key: str) -> str:
    """Record the answer to a request this node sent: the reply is verified
    under the responder's signing and checkpoint keys you trust, and sealed
    as received (an artifact or a signed refusal). A reply that does not
    verify is recorded as failing, never as an answer."""
    octets = Path(reply_file).expanduser().read_bytes()
    record = node().record_reply(request_record_id, Message(octets),
                                 ResponderKeys(signer=responder_key, checkpoint=checkpoint_key))
    statement = record.header.statement
    return _out(ok=True, step="07", record_id=record.record_id, outcome=statement.get("outcome"))


# -- History cards ----------------------------------------------------------------------------


@_tool
def loop_history_card(book_id: str, out_path: str) -> str:
    """Write ``history_card/1`` for one book (a deal's, or the node book) to
    ``out_path``: the reply a stranger's request for it gets."""
    octets = node().history_card(book_id)
    _write(out_path, octets)
    return _out(ok=True, book_id=book_id, file=out_path, bytes=len(octets))


@_tool
def loop_standard_history(out_path: str, tick: int = 0) -> str:
    """Write this node's ``standard_history/1`` card as of a cadence tick
    (the newest when 0), with the inputs a verifier recomputes it from."""
    result = node().standard_history(tick)
    _write(out_path, json.dumps(result, sort_keys=True, indent=2).encode())
    return _out(ok=True, tick=result["tick"], deals=len(result["deals"]), anchor=result["as_of"]["anchor"],
                file=out_path)


if __name__ == "__main__":
    server.run()
