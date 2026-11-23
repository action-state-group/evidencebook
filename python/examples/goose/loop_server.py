#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The Goose loop reference: a Goose extension (an MCP server over stdio) whose node is a
capsulectl-deal profile (:mod:`goose_node`). Every tool is one or two capsulectl verbs; this
server holds no store and no key of its own.

Each deal is an x-deal-v1 deal of the profile: the tools write the next file of the exchange (the
offer, the agreement, a payment leg, an act, a statement) for you to carry to the other side, and
keep the file the other side sends back (``loop_deal_reconcile`` is ``deal receive``). The cadence
commits every log at each tick; the node answers a History card as of its newest checkpoint.

    step  tool(s)
    ----  ---------------------------------------------------------------
    01    loop_seal
    02    loop_book, loop_tick, loop_book_bundle
    03    loop_advertise (the node's signed evidence-commitment/1, for the loop's nodes/commitments/)
    04    loop_admit, loop_deal_bundle
    05    loop_deal_open, loop_deal_respond, loop_deal_agree, loop_deal_reconcile
    06    loop_settle (the buyer's pay step; no rail is driven), loop_deal_approve, loop_deal_received,
          loop_deal_deliver, loop_deal_close, loop_deal_report
    07    loop_ask, loop_ask_reply (the asker's side, over a file you carry); loop_mailbox_poll (answer the requests
          addressed to the node's mailbox), loop_ask_mailbox, loop_ask_mailbox_poll (ask a node through the mailbox
          its commitment advertises)
    09    not built on x-deal-v1 yet (evidencebook#830)
    12    loop_join, loop_probe
    cards loop_history_card, loop_standard_history, loop_register_card (opt-in)

Add to ``~/.config/goose/config.yaml``::

    extensions:
      goose_loop:
        enabled: true
        type: stdio
        name: goose_loop
        description: "Goose loop steps on a capsulectl deal profile"
        cmd: python3
        args: ["/path/to/python/examples/goose/loop_server.py"]
        timeout: 60
        envs:
          GOOSE_HOME: "~/.goose-evidence"
          GOOSE_OPERATOR: "acme-co"

Every tool returns one JSON object. A refusal is ``{"ok": false, "error": ...}`` and seals nothing.
Nothing here moves money, and nothing opens a connection unless the caller sets GOOSE_CARD_SERVICE
and calls loop_register_card: a payment reference is a value the caller observed, and the deal's
files and requests move over the caller's own channel.
"""
from __future__ import annotations

import base64
import functools
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))

try:  # mcp >= 2 renamed FastMCP to MCPServer
    from mcp.server.mcpserver import MCPServer as FastMCP
except ImportError:  # mcp < 2
    from mcp.server.fastmcp import FastMCP

import commitment as cm
import goose_node as gn
import mailbox_exchange as mx
from ctl import Refused

HOME = os.environ.get("GOOSE_HOME", "~/.goose-evidence")
#: The endpoint a commitment gives when the node is asked through its Nostr mailbox.
MAILBOX_ENDPOINT = "nostr:mailbox"
OPERATOR = os.environ.get("GOOSE_OPERATOR", "goose-user")
DEVELOPER = os.environ.get("GOOSE_DEVELOPER", "goose-loop@v1")

server = FastMCP(
    "goose-loop",
    instructions=(
        "Run the loop's steps on this node's capsulectl deal profile: loop_join opens it, loop_admit seals a GO/NO "
        "on a peer, loop_deal_* write and keep the files of an x-deal-v1 deal (an offer, an agreement, a payment "
        "leg, a delivery, a statement), loop_settle pays (a pause waits for the user's answer, loop_deal_approve), "
        "loop_tick commits every log at a cadence tick, and loop_standard_history and loop_history_card answer "
        "the node's History cards."
    ),
)

_node: gn.Node | None = None


def node() -> gn.Node:
    """The node, opened on first use and held for the server's lifetime."""
    global _node
    if _node is None:
        _node = gn.Node.open(HOME, operator=OPERATOR, developer=DEVELOPER)
    return _node


def _out(**members: Any) -> str:
    return json.dumps(members, sort_keys=True, default=str)


def _refuse(error: str) -> str:
    return _out(ok=False, error=error)


def _tool(fn):
    """Run a tool, turning a refusal or bad input into ``{"ok": false}``."""
    @functools.wraps(fn)
    def run(*args, **kwargs) -> str:
        try:
            return fn(*args, **kwargs)
        except (Refused, ValueError, OSError, KeyError, subprocess.SubprocessError) as exc:
            return _refuse(str(exc))
    return server.tool()(run)


def _write(path: str, data: bytes) -> None:
    Path(path).expanduser().write_bytes(data)


def _read_json(path: str) -> Any:
    return json.loads(Path(path).expanduser().read_text())


def _path(path: str) -> str:
    return str(Path(path).expanduser())


# -- 01 Seal ------------------------------------------------------------------------------


@_tool
def loop_seal(provider: str, model_id: str, prompt: str, completion: str, out_path: str) -> str:
    """Seal one model call Goose made: an effect-boundary record in this node's book, signed with its
    key by capsulectl (``effect_attestation`` ``runtime_claimed``), whose ``model_attestation`` names
    ``provider`` and ``model_id``. The prompt stays on this node; the completion is committed by digest
    only. The Capsule is written to ``out_path`` as a verifier reads it."""
    record, capsule = node().seal_model_call(provider, model_id, prompt, completion)
    _write(out_path, json.dumps(capsule, sort_keys=True, indent=2).encode())
    return _out(ok=True, step="01", record_id=record["record_id"], capsule_id=capsule["capsule_id"], file=out_path)


# -- 12 Join and probe ------------------------------------------------------------------


@_tool
def loop_join(node_id: str = "", commitment_dir: str = "") -> str:
    """Open this node's profile and seal its join record. Returns the node's signing key (its identity to
    peers), its checkpoint key, and its signed commitment (step 03, with the defaults ``loop_advertise``
    takes) and policy. With ``commitment_dir``, both are also written there as ``loop_advertise`` writes them."""
    n = node()
    cm.check(node_id or n.key_id, cm.Settings())  # a refusal seals no join record
    sealed = n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id}, once="key_id")
    doc, policy_bytes = cm.build(n, node_id)
    files = [str(p) for p in cm.write(commitment_dir, doc, policy_bytes)] if commitment_dir else []
    return _out(ok=True, step="12", key_id=n.key_id, checkpoint_key_id=n.checkpoint_key_id,
                home=str(n.dir), record_id=sealed["record_id"], commitment=doc,
                policy=json.loads(policy_bytes), commitment_files=files)


@_tool
def loop_probe() -> str:
    """The deals this profile holds with their state, and the node book's newest checkpoint."""
    n = node()
    return _out(ok=True, step="12", deals=n.ctl.run("list", "--json"), node_book=n.checkpoint(gn.NODE_BOOK))


# -- 02 Book and the cadence ------------------------------------------------------------


@_tool
def loop_book(book_id: str) -> str:
    """A node book's newest checkpoint (cut now if the book grew)."""
    cp = node().checkpoint(book_id)
    return _out(ok=True, step="02", book_id=book_id, size=cp["size"],
                checkpoint={"entries": cp["entries"], "root": cp["root"], "key_id": cp["key_id"], "id": cp["id"]})


@_tool
def loop_tick() -> str:
    """Run the cadence's tick if one is due: every live log's checkpoint is committed in the cadence
    checkpoint, the one a witness sees."""
    done = node().tick()
    return _out(ok=True, step="02", state=done.get("state"), due=done.get("due"),
                published=done.get("published_this_run"), witness=done.get("witness"))


@_tool
def loop_book_bundle(book_id: str, record_id: str, out_path: str, disclose: bool = False,
                     witness_receipt: str = "", witness_directory: str = "") -> str:
    """Write an Evidence Bundle (``evidence-bundle/v2``) for one record of a node book to ``out_path``: the
    record, its position and inclusion proof, and the book's checkpoint covering it. ``capsulectl verify
    --bundle`` reads it. With ``disclose`` it also reveals the headers of the record and of the records it
    cites, with their payloads. A witness's receipt for the checkpoint is the host's to fetch (submit the
    checkpoint statement, ``loop_book`` writes it); give the receipt file and the witness directory and the
    bundle carries it, checked under the directory."""
    n = node()
    done = n.bundle(book_id, record_id, _path(out_path), disclose=disclose,
                    witness_receipt=_path(witness_receipt) if witness_receipt else "",
                    witness_directory=_path(witness_directory) if witness_directory else "")
    return _out(ok=True, step="02", book_id=book_id, record_id=record_id, file=out_path, **{
        k: v for k, v in done.items() if k in ("checkpoint", "witnessed")})


# -- 03 Advertise ---------------------------------------------------------------------------


@_tool
def loop_advertise(out_dir: str, node_id: str = "", endpoint: str = "", bindings: str = "",
                   answers_within: str = "", human_gated: bool = True, relays: str = "",
                   poll_interval: int = 30) -> str:
    """Write this node's signed ``evidence-commitment/1`` and its signed disclosure policy to ``out_dir`` as
    ``<node_id>.json`` and ``policies/<node_id>.json``: add both under the loop repository's
    ``nodes/commitments/`` by pull request. ``node_id`` is the node's id in the loop's list (its key id when
    empty). ``endpoint`` and ``bindings`` (comma-separated) say how it is asked; ``answers_within`` (ISO 8601,
    default PT24H) is how long an asker waits before recording absence. It staples the profile's
    ``history_card/2`` and carries no counts.

    ``relays`` (comma-separated ``wss://`` URLs) are the relays the node reads its Nostr mailbox on: the
    commitment then names ``ask.nostr`` and the node is askable with no listener, by calling
    ``loop_mailbox_poll`` at least every ``poll_interval`` seconds. ``answers_within`` may not be shorter
    than ``poll_interval``."""
    defaults = cm.Settings()
    mailbox = tuple(r.strip() for r in relays.split(",") if r.strip())
    settings = cm.Settings(
        relays=mailbox, poll_interval=int(poll_interval),
        endpoint=endpoint or (MAILBOX_ENDPOINT if mailbox else defaults.endpoint),
        bindings=tuple(b.strip() for b in bindings.split(",") if b.strip()) or defaults.bindings,
        answers_within=answers_within or defaults.answers_within, human_gated=human_gated)
    n = node()
    doc, policy_bytes = cm.build(n, node_id, settings)
    files = cm.write(out_dir, doc, policy_bytes)
    if mailbox:
        _write(str(n.home / "mailbox.json"), json.dumps({"relays": list(mailbox), "poll_interval": settings.poll_interval}).encode())
    return _out(ok=True, step="03", node_id=doc["node_id"], key_id=doc["key_id"], profile=doc["profile"],
                streams=[c["stream"] for c in doc["history_cards"]], files=[str(p) for p in files])


# -- 04 Admit -----------------------------------------------------------------------------


@_tool
def loop_admit(peer_key_id: str, decision: str, reason: str = "") -> str:
    """Seal this node's GO or NO on a peer before any deal with it. The peer is named by its signing key
    (from its loop_join). A NO stops every deal with it; a GO lets this node run capsule-engine's admission
    on a deal with the peer, whose decision is what the deal's agreement cites."""
    decision = decision.strip().upper()
    if decision not in ("GO", "NO"):
        raise ValueError("decision must be GO or NO")
    if len(peer_key_id) != 64 or peer_key_id.strip("0123456789abcdef"):
        raise ValueError("peer_key_id is the peer's 64-hex signing key")
    sealed = node().note(gn.ADMITTED, {"peer": peer_key_id, "decision": decision, "reason": reason})
    return _out(ok=True, step="04", record_id=sealed["record_id"], peer=peer_key_id, decision=decision)


@_tool
def loop_deal_bundle(deal_id: str, out_path: str) -> str:
    """Write an Evidence Bundle of this node's record of a deal's context to ``out_path``: that record, the
    admission it cites (this node's GO, sealed before the deal) and the rest of the node book up to the
    checkpoint, so a verifier reads that no deal record cites a NO. The deal's own records stay in the
    deal's book."""
    n = node()
    n.bundle(gn.NODE_BOOK, n.context(deal_id)["record_id"], _path(out_path))
    return _out(ok=True, step="04", deal_id=deal_id, file=out_path)


# -- 05 Deal --------------------------------------------------------------------------------


@_tool
def loop_deal_open(peer_key_id: str, item: str, price_minor: int, currency: str, rail: str, asked: str,
                   out_path: str, refundable: bool = False) -> str:
    """Open a deal as the buyer with an admitted peer: the offer at these terms (one payment row, due in a day),
    written to ``out_path`` for the seller. ``asked`` is the user's own words; they stay on this node. The
    price is in minor units of ``currency`` (a 3-letter code), and the admission pack decides which prices
    this node deals in."""
    done = node().open_deal(peer_key_id, item, price_minor, currency, rail, asked, refundable, _path(out_path))
    return _out(ok=True, step="05", file=out_path, **done)


@_tool
def loop_deal_respond(request_file: str, asked: str, out_path: str) -> str:
    """Answer a buyer's offer as the seller: keep it, run the admission on the buyer (it must be admitted with
    GO), seal what the user asked and agree. The agreement is written to ``out_path`` for the buyer."""
    done = node().respond(_path(request_file), asked, _path(out_path))
    return _out(ok=True, step="05", file=out_path, **done)


@_tool
def loop_deal_agree(deal_id: str, out_path: str) -> str:
    """As the buyer, once the seller's agreement is kept (``loop_deal_reconcile``): run the admission on the
    seller, seal what the user asked and agree. The agreement is written to ``out_path`` for the seller."""
    done = node().agree(deal_id, _path(out_path))
    return _out(ok=True, step="05", file=out_path, **done)


@_tool
def loop_deal_reconcile(peer_file: str) -> str:
    """Keep a file the other side sent (an offer, an agreement, a payment leg, a delivery, a statement) and say
    whether it counts yet. What you have not kept does not count for you."""
    done = node().receive(_path(peer_file))
    return _out(ok=True, step="05", **{k: v for k, v in done.items() if k not in ("spec_version", "voice")})


# -- 06 Settle and close ----------------------------------------------------------------------


@_tool
def loop_settle(deal_id: str, payment_ref: str, out_path: str, check_id: str = "") -> str:
    """The buyer's payment: the final review (``deal check``) and, when it proceeds, the payer leg written to
    ``out_path`` for the seller. ``payment_ref`` is the reference the caller observed on the rail (a Lightning
    payment hash); this tool drives no rail and moves no money. A check that pauses returns the card the user
    must see (``say``) and ``paused: true``: show it as it is, then seal their answer with ``loop_deal_approve``
    and call again with the ``check_id``."""
    done = node().pay(deal_id, payment_ref, _path(out_path), check_id)
    return _out(ok=True, step="06", file=out_path, **done)


@_tool
def loop_deal_approve(deal_id: str, check_id: str, choice: str, said: str) -> str:
    """Seal the user's answer to a check that paused (``loop_settle`` or ``loop_deal_deliver``): ``choice`` is
    ``proceed`` or ``hold``, ``said`` their words. Only their answer goes on: never seal one they did not give."""
    done = node().approve(deal_id, check_id, choice, said)
    return _out(ok=True, step="06", deal_id=deal_id, proceed=done.get("proceed"), say=done.get("say"))


@_tool
def loop_deal_received(deal_id: str, amount_minor: int, payment_ref: str, out_path: str) -> str:
    """The seller's payee leg: what it received (an amount in minor units and the payment reference it observed),
    written to ``out_path`` for the buyer. Keep the buyer's payer leg first (``loop_deal_reconcile``)."""
    done = node().received(deal_id, amount_minor, payment_ref, _path(out_path))
    return _out(ok=True, step="06", file=out_path, **done)


@_tool
def loop_deal_deliver(deal_id: str, out_path: str, observed: str = "delivered", check_id: str = "") -> str:
    """The seller's delivery: a commit review, then the act, written to ``out_path`` for the buyer. A review that
    pauses is answered as for ``loop_settle``."""
    done = node().deliver(deal_id, _path(out_path), observed, check_id)
    return _out(ok=True, step="06", file=out_path, **done)


@_tool
def loop_deal_close(deal_id: str, status: str, out_path: str) -> str:
    """Say how the deal's payment row went on this side: ``received`` (met) or ``not_received`` (not met). The
    statement is written to ``out_path`` for the other side."""
    done = node().statement(deal_id, status, _path(out_path))
    return _out(ok=True, step="06", file=out_path, **done)


@_tool
def loop_deal_report(deal_id: str, out_path: str, wait_seconds: int = 0) -> str:
    """Write this node's report of the deal as an Evidence Bundle (``deal report --bundle``) to ``out_path``.
    A report rests on a checkpoint: with ``wait_seconds`` the tool waits for the cadence tick that covers the
    deal (a tick is due about every 5 minutes)."""
    done = node().report(deal_id, _path(out_path), float(wait_seconds))
    return _out(ok=True, step="06", **done)


# -- 07 Ask ---------------------------------------------------------------------------------


@_tool
def loop_ask(responder: str, subject_form: str, out_path: str, subject_value: str = "", expected_pin: str = "",
             min_freshness: str = "", derivation: str = "") -> str:
    """Seal an evidence request (draft-mih-agent-evidence-request) in this node's book before it is sent, then
    write its octets to ``out_path``. Exactly one of ``expected_pin`` or ``min_freshness`` sets the coverage."""
    request: dict[str, Any] = {
        "profile": "evidence-interop/0",
        "subject": {subject_form: json.loads(subject_value) if subject_value else None},
        "coverage": {"expected_pin": expected_pin} if expected_pin else
        {"min_freshness": int(min_freshness) if min_freshness.isdigit() else min_freshness},
        "nonce": os.urandom(16).hex()}
    if derivation:
        request["derivation"] = derivation
    n = node()
    sealed = n.ctl.run("node", "request", "--book", gn.NODE_BOOK, "--request", n._file("request.json", request),
                       "--responder", responder, "--output", _path(out_path))
    return _out(ok=True, step="07", record_id=sealed["record_id"], file=out_path)


@_tool
def loop_ask_reply(request_record_id: str, reply_file: str, responder_key: str, checkpoint_key: str) -> str:
    """Record the answer to a request this node sent: the reply is verified under the responder's signing and
    checkpoint keys you trust, and sealed as received (an artifact or a signed refusal). A reply that does not
    verify is recorded as failing, never as an answer."""
    done = node().ctl.run("node", "request", "--book", gn.NODE_BOOK, "--for", request_record_id,
                          "--response", _path(reply_file), "--responder-key", responder_key,
                          "--responder-checkpoint-key", checkpoint_key)
    record = done.get("record", done)
    return _out(ok=True, step="07", record_id=record.get("record_id"),
                outcome=(record.get("statement") or {}).get("outcome", done.get("outcome")))


@_tool
def loop_mailbox_poll(relays: str = "") -> str:
    """Answer the evidence requests addressed to this node's mailbox, once.

    Reads the requests addressed to the mailbox key on ``relays`` (comma-separated; by default the ones
    ``loop_advertise`` named), answers each from this node's book under the node key and posts the answer back,
    or a signed refusal for a request outside the disclosure policy. Call it at least as often as the
    ``poll_interval`` you advertised."""
    names = [r.strip() for r in relays.split(",") if r.strip()]
    if not names:
        try:
            names = _read_json(str(node().home / "mailbox.json"))["relays"]
        except (OSError, KeyError, ValueError):
            raise Refused("no relays: pass relays, or advertise with relays first") from None
    answered = mx.answer_round(node(), names)
    return _out(ok=True, step="07", answered=answered)


@_tool
def loop_ask_mailbox(commitment_file: str, subject_form: str, subject_value: str = "", expected_pin: str = "",
                     min_freshness: str = "", derivation: str = "") -> str:
    """Ask a node through the mailbox its signed commitment advertises (``ask.nostr``).

    The request is sealed in this node's book before it is published, with a deadline of the commitment's
    ``answers_within``. Exactly one of ``expected_pin`` or ``min_freshness`` sets the coverage. Poll with
    ``loop_ask_mailbox_poll``."""
    content = json.loads(subject_value) if subject_value else None
    floor: int | str | None = None
    if min_freshness:
        floor = int(min_freshness) if min_freshness.isdigit() else min_freshness
    sent = mx.ask(node(), _read_json(commitment_file), subject_form, content, derivation, expected_pin, floor)
    return _out(ok=True, step="07", **sent)


@_tool
def loop_ask_mailbox_poll(request_event_id: str) -> str:
    """Poll once for the reply to a request sent with ``loop_ask_mailbox``.

    ``status`` is ``answered`` (verified under the node's listed keys and recorded: an artifact or a signed
    refusal), ``pending`` (before the deadline) or ``absent`` (the deadline passed with no recorded answer; the
    absence is recorded against the node) or ``transport_error`` (no relay could be read and no outcome is
    recorded yet: nothing is written, poll again). ``late`` marks a reply that came after the absence: it is kept
    beside it and the absence stays as written."""
    return _out(ok=True, step="07", **mx.poll(node(), request_event_id))


# -- History cards ----------------------------------------------------------------------------


@_tool
def loop_history_card(out_path: str) -> str:
    """Write this profile's ``history_card/2`` to ``out_path``: the staple (the holder's own request and its signed
    reply), a sampled chain of its cadence checkpoints with no deal counts, as of the newest finished tick."""
    staple = node().ctl.run("standard-history", "staple")
    _write(out_path, json.dumps(staple["staple"], sort_keys=True, indent=2).encode())
    return _out(ok=True, derivation=staple["derivation"], file=out_path)


@_tool
def loop_standard_history(out_path: str, shared: bool = False) -> str:
    """Write this node's ``standard_history/3`` card (a count of its x-deal-v1 deals as of its newest checkpoint, with
    no deal's content) as a bundle ``capsulectl verify --bundle`` checks. ``shared`` writes the copy to send: the
    card, its policy and its anchor, without the deal entries your own copy has. It needs a finished tick."""
    args = ["standard-history", "card", "--out", _path(out_path)]
    if shared:
        args.append("--shared")
    done = node().ctl.run(*args)
    return _out(ok=True, file=out_path, **{k: v for k, v in done.items() if k not in ("spec_version", "voice", "file")})


@_tool
def loop_register_card(out_path: str) -> str:
    """Register this node's ``history_card/2`` on the transparency service named in ``GOOSE_CARD_SERVICE`` (a
    service file: name, operator, url and the keys its Receipts verify under) and write the card statement with
    its Receipt to ``out_path``. Opt-in: with ``GOOSE_CARD_SERVICE`` unset nothing is sent and the tool refuses;
    the card is complete without it, and a failed registration changes nothing.

    Call it only when the user has asked to register this card, and tell them first that it is permanent and
    that the service learns which of their keys registered a card for which anchor, and when. Say who runs the
    service as the result names it. What a passing Receipt shows is what ``capsulectl book card register --help``
    says: that this card existed by the Receipt's time, not that its counts are right, and ``anchor_inclusion``
    stays NOT_CHECKED."""
    service = os.environ.get("GOOSE_CARD_SERVICE")
    if not service:
        raise Refused("no transparency service is configured: set GOOSE_CARD_SERVICE to a service file; "
                      "registration is opt-in and has no default")
    n = node()
    staple = n.ctl.run("standard-history", "staple")["staple"]["reply"]
    card = n._file("card.json", json.dumps(staple, sort_keys=True, separators=(",", ":")))
    book = os.environ.get("GOOSE_CAPSULECTL_BOOK", "capsulectl-book")
    done = subprocess.run([book, "--profile", n.operator, "card", "register", "--card", card,
                           "--anchor", staple["anchor"]["digest"], "--holder", n.key_id,
                           "--service", _path(service), "--out", _path(out_path)],
                          env={**os.environ, "HOME": str(n.home)}, capture_output=True, timeout=300, check=False)
    if done.returncode != 0:
        try:
            why = json.loads(done.stdout).get("error")
        except ValueError:
            why = None
        raise Refused(why or done.stderr.decode("utf-8", "replace").strip() or "card register failed")
    return _out(ok=True, anchor=staple["anchor"]["digest"], file=out_path)


if __name__ == "__main__":
    server.run()
