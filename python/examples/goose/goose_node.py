# SPDX-License-Identifier: Apache-2.0
"""A Goose node on its own capsulectl-deal profile: its deals, its node book and its History cards.
:mod:`loop_server` exposes it to Goose as MCP tools; this module has no MCP in it, so a test or another
host drives it directly.

The store is the profile's (``<home>/deal``, ``capsulectl deal init``); this module never opens it. Every
operation is one verb of the ``capsulectl-deal`` plugin (:mod:`ctl`):

- **a deal** is an x-deal-v1 deal of the profile: the offer, the admission, the user's intent, the agreement,
  the payment legs, the act and the statements are the profile's records, and what the other side needs from
  them is the file each verb writes (the offer, the agreement, ...), which this node hands over and the
  other side keeps with ``deal receive``;
- **the node book**, ``goose/node``: what the node did outside any deal (joining, its decision on a peer,
  a deal's context, a model call it sealed, the evidence requests it sent and their answers), sealed with
  ``deal node ...`` and kept on the same shelf and cadence as the deals;
- **the cadence**: ``deal tick`` commits every live log's checkpoint, the one checkpoint a witness sees.

The node's keys live in the profile's directory, created by ``deal init`` with mode 0600. Nothing here
moves money or opens a connection.
"""
from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import tempfile
import threading
import time
import uuid
from decimal import Decimal
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from ctl import Ctl, Refused, engine

__all__ = ["Node", "Refused", "NODE_BOOK", "JOINED", "ADMITTED", "DEAL_CONTEXT", "DEAL_WORDS", "MODEL_CALL", "ASKED_TYPE",
           "SETTLED", "SETTLEMENT_DISCLOSURE"]

NODE_BOOK = "goose/node"
#: The record types of the node book. They are this node's own names (capsulectl refuses a library name).
JOINED, ADMITTED, DEAL_CONTEXT, MODEL_CALL = "goose_joined", "goose_admitted", "goose_deal_context", "goose_model_call"
#: What the user asked, and the digest of the intent sealed from it: kept apart from the deal's context, which a
#: bundle may disclose (loop_deal_bundle), so the words never leave with it.
DEAL_WORDS, DEAL_INTENT, DEAL_ADMIT = "goose_deal_words", "goose_deal_intent", "goose_deal_admit"
ASKED_TYPE = "goose_asked"
#: This node's own payment leg sealed as settled (its deal, the deal's rail, which leg, the leg's digest), counted by
#: the settlement disclosure (:mod:`settlement`); and each disclosure it signed.
SETTLED, SETTLEMENT_DISCLOSURE = "goose_settled", "goose_settlement_disclosure"
#: The admission pack the node's deal admissions run (capsule-engine's ladder pack, its conformance vector).
PACK = Path(__file__).resolve().parent / "admission-pack.json"
#: The one payment row of a Goose deal, and how long after the offer the buyer has to pay and the deal to settle.
ROW, DELIVERED = "paid_as_agreed", "delivered"
PAY_WITHIN, SETTLE_WITHIN, AGREE_WITHIN = timedelta(days=1), timedelta(days=7), timedelta(days=2)


def _iso(at: datetime) -> str:
    return at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _seed_key(path: Path) -> Ed25519PrivateKey:
    return Ed25519PrivateKey.from_private_bytes(bytes.fromhex(path.read_text().strip()))


def _leg(done: dict) -> dict:
    """What a verb that seals a leg or an act says, kept whole where a reader relies on it: ``person_signature:
    missing`` means no act carries the person's own signature yet, so "settled" is read from the agent's records, which
    the person has not signed (``person_signs`` names what they would sign)."""
    return {k: done[k] for k in ("leg", "digest", "person_signature", "person_signs", "first_act_gate") if k in done}


class Node:
    """One Goose node: a directory (``home``) that is its capsulectl-deal profile's ``HOME``."""

    def __init__(self, home: Path, operator: str, developer: str, ctl: Ctl) -> None:
        self.home, self.operator, self.developer, self.ctl = home, operator, developer, ctl
        self.dir = home / "deal"
        self._tmp = Path(tempfile.mkdtemp(prefix="goose-", dir=home))
        self._lock = threading.RLock()  # one verb at a time: the node's scratch files and its store are one writer's

    @classmethod
    def open(cls, home: str | os.PathLike, *, operator: str, developer: str = "goose-loop@v1") -> Node:
        """The node at ``home``, created (``deal init``) on first use. ``operator`` is the profile's name and
        the AAC operator of what the node seals. ``GOOSE_NO_WITNESS=1`` makes a profile with no public
        witness; ``GOOSE_SIGNING_KEY_FILE`` and ``GOOSE_CHECKPOINT_KEY_FILE`` keep an existing node's keys
        (PKCS#8 PEM or a hex seed), so its key ids stay."""
        path = Path(home).expanduser()
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
        if not operator:
            raise Refused("an operator name is required")
        node = cls(path, operator, developer, Ctl(path, operator))
        if not (node.dir / "signing.seed").exists():
            args = ["init", "--dir", str(node.dir)]
            if os.environ.get("GOOSE_NO_WITNESS"):
                args.append("--no-witness")
            for flag, env in (("--signing-key-file", "GOOSE_SIGNING_KEY_FILE"),
                              ("--checkpoint-key-file", "GOOSE_CHECKPOINT_KEY_FILE")):
                if os.environ.get(env):
                    args += [flag, os.environ[env]]
            node._run(*args)
            # a Goose node is a person's agent; this is declared once, and `deal respond` then answers no card request
            node._run("standard-history", "declare", "--holder-kind", "person")
        return node

    def close(self) -> None:
        """Remove the node's scratch directory (it holds the user's words while a verb runs)."""
        shutil.rmtree(self._tmp, ignore_errors=True)

    def __enter__(self) -> Node:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    # -- keys -------------------------------------------------------------------------

    @property
    def _signing(self) -> Ed25519PrivateKey:
        return _seed_key(self.dir / "signing.seed")

    @property
    def key_id(self) -> str:
        return self._signing.public_key().public_bytes_raw().hex()

    @property
    def checkpoint_key_id(self) -> str:
        return _seed_key(self.dir / "checkpoint.seed").public_key().public_bytes_raw().hex()

    def sign(self, data: bytes) -> bytes:
        """The node key's signature (the commitment and its policy are signed in this process, as the loop's
        checker reads them; the key is the profile's own, so the id is the one the deals carry)."""
        return self._signing.sign(data)

    @property
    def mailbox_secret(self) -> bytes:
        """The secp256k1 key the node's Nostr mailbox is addressed to and replies are signed with
        (binding nostr-mailbox-v0 §3), created with mode 0600 on first use. A route, not an identity."""
        import nostr_mailbox as mb  # this example's transport, imported on first use
        path = self.home / "mailbox.key"
        if not path.exists():
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "wb") as fh:
                fh.write(mb.new_secret())
        return path.read_bytes()

    @property
    def mailbox_key(self) -> str:
        import nostr_mailbox as mb
        return mb.public_key(self.mailbox_secret)

    # -- files ------------------------------------------------------------------------

    def _file(self, name: str, content: bytes | str | Any) -> str:
        """A scratch file a verb reads: a new path for every call (two tool calls never share one), removed by
        :meth:`_run` once the verb has run."""
        path = self._tmp / f"in-{uuid.uuid4().hex}-{name}"
        if isinstance(content, (dict, list)):
            content = json.dumps(content, sort_keys=True)
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as fh:
            fh.write(content.encode() if isinstance(content, str) else content)
        return str(path)

    def _out(self, name: str) -> str:
        """A new path for a file a verb writes (the caller reads it, then removes it)."""
        return str(self._tmp / f"out-{uuid.uuid4().hex}-{name}")

    def _run(self, *args: str) -> Any:
        """One verb of the node's profile, one at a time; the scratch files made for it are removed after."""
        with self._lock:
            try:
                return self.ctl.run(*args)
            finally:
                for a in args:
                    if a.startswith(str(self._tmp / "in-")):
                        Path(a).unlink(missing_ok=True)

    # -- the node book -----------------------------------------------------------------

    def note(self, record_type: str, statement: dict, *, once: str = "", cites: tuple[str, ...] = (),
             subject_ref: str = "", payloads: tuple[bytes, ...] = ()) -> dict:
        """Seal one record of the node book. ``once`` names a member of ``statement`` that identifies it:
        a second call with the same value finds the record instead of sealing another."""
        args = ["node", "append", "--book", NODE_BOOK, "--type", record_type,
                "--statement", self._file("statement.json", statement)]
        if once:
            args += ["--once", once]
        if subject_ref:
            args += ["--subject-ref", subject_ref]
        for cite in cites:
            args += ["--cite", cite]
        for i, payload in enumerate(payloads):
            args += ["--payload", self._file(f"payload-{i}.bin", payload)]
        return self._run(*args)["record"]

    def notes(self, record_type: str = "") -> list[dict]:
        """The node book's records of ``record_type`` (all when empty), oldest first, with their statements."""
        args = ["node", "records", "--book", NODE_BOOK]
        if record_type:
            args += ["--type", record_type]
        return self._run(*args)["records"]

    def seal_model_call(self, provider: str, model_id: str, prompt: Any, completion: Any) -> tuple[dict, dict]:
        """Seal one model call Goose made as a record of the node book (its prompt kept, its completion by
        digest). Returns the record and the Capsule as a verifier reads it."""
        out = self._out("model-call.json")
        done = self._run("node", "seal-model-call", "--book", NODE_BOOK, "--type", MODEL_CALL,
                            "--provider", provider, "--model-id", model_id, "--developer", self.developer,
                            "--prompt", self._file("prompt.json", json.dumps(prompt)),
                            "--completion", self._file("completion.json", json.dumps(completion)), "--out", out)
        capsule = json.loads(Path(out).read_text())
        Path(out).unlink()
        return done["record"], capsule

    def checkpoint(self, book_id: str, out: str = "") -> dict:
        """A book's newest checkpoint, cut now if it grew; with ``out`` its COSE statement is written there."""
        args = ["node", "checkpoint", "--book", book_id]
        if out:
            args += ["--out", out]
        return self._run(*args)

    def checkpoint_statement(self, book_id: str) -> bytes:
        """The COSE statement of a book's newest checkpoint."""
        out = self._out("checkpoint.cose")
        self.checkpoint(book_id, out)
        data = Path(out).read_bytes()
        Path(out).unlink()
        return data

    def bundle(self, book_id: str, record_id: str, out: str, *, at: str = "", disclose: bool = False,
               witness_receipt: str = "", witness_directory: str = "") -> dict:
        """An Evidence Bundle of one record of a node book under a checkpoint, verified before it is written."""
        args = ["node", "bundle", "--book", book_id, "--record", record_id, "--out", out]
        if at:
            args += ["--at", at]
        if disclose:
            args.append("--disclose")
        if witness_receipt:
            args += ["--witness-receipt", witness_receipt, "--witness-directory", witness_directory]
        return self._run(*args)

    def admission(self, peer: str) -> dict | None:
        """The newest decision this node sealed for ``peer``."""
        found = None
        for record in self.notes(ADMITTED):
            if record["statement"].get("peer") == peer:
                found = {"record_id": record["record_id"], **record["statement"]}
        return found

    def tick(self) -> dict:
        return self._run("tick")

    # -- deals --------------------------------------------------------------------------

    def context(self, deal_id: str) -> dict:
        """The node's own account of a deal it holds: its role, peer, terms, and the words it was asked in."""
        for record in self.notes(DEAL_CONTEXT):
            if record["statement"].get("deal_id") == deal_id:
                words = next((r["statement"]["asked"] for r in self.notes(DEAL_WORDS)
                              if r["statement"]["deal_id"] == deal_id), "")
                return {"record_id": record["record_id"], "asked": words, **record["statement"]}
        raise Refused(f"this node holds no deal {deal_id}")

    def _remember(self, deal_id: str, role: str, peer: str, terms: dict, asked: str, rail: str, admission: str,
                  offer: str) -> dict:
        self.note(DEAL_WORDS, {"deal_id": deal_id, "asked": asked}, once="deal_id", subject_ref=deal_id)
        return self.note(DEAL_CONTEXT, {"deal_id": deal_id, "role": role, "peer": peer, **terms, "rail": rail,
                                        "offer": offer}, once="deal_id", cites=(admission,), subject_ref=deal_id)

    def _decision(self, peer: str, terms: dict) -> dict:
        """The engine's decision on ``peer`` for these terms, and the pack's digest. Seals nothing."""
        gate = self.admission(peer)
        if gate is None:
            raise Refused(f"no decision sealed for {peer}: call loop_admit first")
        if gate["decision"] != "GO":
            raise Refused(f"the sealed decision for {peer} is {gate['decision']}: no deal")
        now = _iso(datetime.now(timezone.utc))
        inputs = {"now": now, "amount": str((Decimal(terms["price_minor"]) / 100).normalize()), "currency": terms["currency"],
                  "platform": {"release_attestation": "valid"}, "card": {},
                  "own": {"exchanges": 0, "settled": 0, "held_halves": 0, "conflicting": 0, "verdicts": []},
                  "operator": {"deny_listed": False}, "vouches": [], "subject": "", "judged": {}, "asks": {},
                  "asks_made": 0}
        # the amount is in major units: this node's deals are in currencies with two minor digits
        card = hashlib.sha256(b"{}").hexdigest()  # no card was read: the digest of the empty document
        refs = {"asks": [], "answers": [], "card_digest": card}
        files = (self._file("engine-in.json", inputs), self._file("engine-refs.json", refs))
        try:
            return engine("admission", "evaluate", "--pack", str(PACK), "--input", files[0], "--admit", files[1])
        finally:
            for f in files:
                Path(f).unlink(missing_ok=True)

    def _admit(self, deal_id: str, peer: str, terms: dict) -> str:
        """Seal the engine's decision for the deal, once: a retry finds it (the engine's output differs at every
        run, and ``deal admit`` takes one decision per deal)."""
        with self._lock:
            return self._admit_once(deal_id, peer, terms)

    def _admit_once(self, deal_id: str, peer: str, terms: dict) -> str:
        done = next((r["statement"]["admit"] for r in self.notes(DEAL_ADMIT) if r["statement"]["deal_id"] == deal_id), None)
        if done:
            return done
        decided = self._decision(peer, terms)
        if not decided.get("admits_deal"):
            raise Refused(f"the engine's decision on this deal is {decided['verdict']}"
                          f"{' with asks to answer in capsule-engine' if decided.get('asks') else ''}: no deal")
        try:
            admit = self._run("admit", "--deal", deal_id, "--input", self._file("admission.json", decided))["admit"]
        except Refused as exc:
            if "already has an admission decision" not in str(exc):
                raise
            return ""  # sealed by a call that stopped before it noted it: the agreement cites the deal's own
        self.note(DEAL_ADMIT, {"deal_id": deal_id, "admit": admit}, once="deal_id", subject_ref=deal_id)
        return admit

    def open_deal(self, peer: str, item: str, price_minor: int, currency: str, rail: str, asked: str,
                  refundable: bool, out: str) -> dict:
        """Open a deal as the buyer: the offer to ``peer`` at these terms, written to ``out`` for the peer to
        keep with ``deal receive``. The user's words stay on this node."""
        now = datetime.now(timezone.utc)
        terms = {"item": item, "price_minor": price_minor, "currency": currency, "refundable": refundable}
        decided = self._decision(peer, terms)
        if not decided.get("admits_deal"):
            raise Refused(f"the engine's decision on this deal is {decided['verdict']}: no deal")
        # The agreed price goes in the terms as well as in the payment row: the History card's value band reads it
        # from the terms (x-deal-v1 status value), and an offer without it counts as unpriced (evidencebook#888).
        offer = {"role": "buyer", "to": peer, "terms": {"item": item, "price_minor": price_minor, "currency": currency},
                 "pack_digest": decided["pack"]["digest"], "agree_by": _iso(now + AGREE_WITHIN),
                 "terms_rows": {"rows": [{"id": ROW, "owed_by": "buyer",
                                          "owed": {"amount_minor": price_minor, "currency": currency},
                                          "due": {"at": _iso(now + PAY_WITHIN)},
                                          "window_end": {"at": _iso(now + SETTLE_WITHIN)}},
                                         {"id": DELIVERED, "owed_by": "seller", "owed": {},
                                          "due": {"at": _iso(now + PAY_WITHIN)},
                                          "window_end": {"at": _iso(now + SETTLE_WITHIN)}}], "named": {}},
                 "adjudicator": None, "forums": [], "witnesses": []}
        done = self._run("offer", "--input", self._file("offer.json", offer), "--out", out)
        gate = self.admission(peer)
        self._remember(done["deal_id"], "buyer", peer, terms, asked, rail, gate["record_id"], done["offer"])
        return {"deal_id": done["deal_id"], "offer": done["offer"]}

    def _intent(self, deal_id: str, asked: str, rail: str, who: str, kind: str, allowed: list[str],
                limit: int | None, refundable: bool = False) -> str:
        with self._lock:
            return self._intent_once(deal_id, asked, rail, who, kind, allowed, limit, refundable)

    def _intent_once(self, deal_id: str, asked: str, rail: str, who: str, kind: str, allowed: list[str],
                     limit: int | None, refundable: bool) -> str:
        sealed = next((r["statement"]["intent"] for r in self.notes(DEAL_INTENT)
                       if r["statement"]["deal_id"] == deal_id), None)
        if sealed:
            return sealed  # a retry: the intent is sealed once
        said = {"verbatim": asked, "allowed": allowed}
        if limit is not None:
            said["max_total_minor"] = limit
        opening = {"type": kind, "who": {"name": who, "domain": f"{who[:12]}.invalid"},
                   "recourse": {"rail": rail, "refundable": refundable}}
        try:
            intent = self._run("note", "--deal", deal_id, "--kind", "intent", "--input", self._file("intent.json", said),
                               "--opening", self._file("opening.json", opening))["intent"]
        except Refused as exc:
            if "already open" not in str(exc):
                raise
            # a call stopped after sealing the deal's first intent and before noting its digest, which capsulectl
            # does not print again (a second intent with the same words changes nothing, and is refused): the
            # intent is sealed, so the agreement goes without citing it
            return ""
        self.note(DEAL_INTENT, {"deal_id": deal_id, "intent": intent}, once="deal_id", subject_ref=deal_id)
        return intent

    def respond(self, offer_file: str, asked: str, out: str) -> dict:
        """As the seller: keep the buyer's offer, decide on it, seal what the user asked and agree. The
        agreement is written to ``out`` for the buyer."""
        try:
            offer = json.loads(Path(offer_file).read_text())
            terms, peer = self._offer_terms(offer), offer["record"]["key_id"]
        except Refused:
            raise
        except (KeyError, IndexError, TypeError, ValueError):
            raise Refused("not an x-deal-v1 offer file: the offer the buyer's loop_deal_open wrote") from None
        decided = self._decision(peer, terms)  # a refusal here keeps and seals nothing
        if not decided.get("admits_deal"):
            raise Refused(f"the engine's decision on this deal is {decided['verdict']}: no deal")
        deal_id = self._run("receive", offer_file)["deal_id"]
        self._admit(deal_id, peer, terms)
        gate = self.admission(peer)
        self._remember(deal_id, "seller", peer, terms, asked, "lightning", gate["record_id"], self._digest(offer))
        self._intent(deal_id, asked, "lightning", "the buyer", "service", ["commit"], None)
        done = self._run("agree", self._digest(offer), "--out", out)
        return {"deal_id": deal_id, "agreement": done["agreement"]}

    def agree(self, deal_id: str, out: str) -> dict:
        """As the buyer, once the seller's agreement is kept: decide, seal what the user asked, agree."""
        ctx = self.context(deal_id)
        terms = {k: ctx[k] for k in ("item", "price_minor", "currency", "refundable")}
        self._admit(deal_id, ctx["peer"], terms)
        intent = self._intent(deal_id, ctx["asked"], ctx["rail"], "the seller", "purchase", ["pay"], ctx["price_minor"],
                              ctx["refundable"])
        done = self._run("agree", ctx["offer"], *(["--intent", intent] if intent else []), "--out", out)
        return {"deal_id": deal_id, "agreement": done["agreement"]}

    @staticmethod
    def _digest(offer: dict) -> str:
        """An offer's digest, the one ``deal offer`` prints: the SHA-256 of the JCS of the Capsule's
        ``agent_input`` (RFC 8785; the members here are strings, integers and null)."""
        raw = json.dumps(offer["agent_input"], sort_keys=True, separators=(",", ":"), ensure_ascii=False)
        return hashlib.sha256(raw.encode()).hexdigest()

    @staticmethod
    def _offer_terms(offer: dict) -> dict:
        body = offer["agent_input"]["body"]
        row = body["terms_rows"]["rows"][0]
        price = row["owed"]["amount_minor"]
        if body["terms"].get("price_minor") != price:
            # the card's value band reads the terms' price, the payment the row's: an offer whose two differ is refused
            # rather than counted at a price the deal is not paid at (evidencebook#888)
            raise Refused("the offer's terms.price_minor is not the amount its payment row owes")
        return {"item": body["terms"]["item"], "price_minor": price, "currency": body["terms"]["currency"],
                "refundable": False}

    def receive(self, file: str) -> dict:
        """Keep a file the other side sent (an offer, an agreement, a leg, an act, a statement) and say
        whether it counts yet."""
        return self._run("receive", file)

    def _shown(self, check_id: str) -> dict:
        """What the node kept beside the card of a check it paused."""
        side = self.home / "cards" / f"{check_id}.json"
        return json.loads(side.read_text()) if side.exists() else {}

    def approve(self, deal_id: str, check_id: str, choice: str, said: str) -> dict:
        """The user's answer to a check that paused, sealed with the card they were shown."""
        if not re.fullmatch(r"[0-9a-f]{64}", check_id):
            raise Refused("check_id is the 64-hex id the check printed")
        card = self.home / "cards" / f"{check_id}.txt"
        if not card.exists():
            raise Refused(f"no card was shown for check {check_id}")
        return self._run("note", "--deal", deal_id, "--kind", "approval", "--check", check_id,
                            "--choice", choice, "--said", said, "--shown-card", str(card))

    def _gate(self, deal_id: str, action: str, check: dict, check_id: str) -> tuple[dict | None, str]:
        """The final review of an act. With no ``check_id`` the check runs: a pause or a deny comes back as
        ``({"paused": ...}, "")`` for the user (the card they must see is kept for :meth:`approve`), and the
        act waits. A check that proceeds, or one the user answered (``check_id``), gives ``(None, check_id)``."""
        if check_id:
            return None, check_id
        done = self._run("check", "--deal", deal_id,
                            "--input", self._file("check.json", {"action": action, **check}))
        if done.get("proceed"):
            return None, done["check_id"]
        cards = self.home / "cards"
        cards.mkdir(exist_ok=True, mode=0o700)
        (cards / f"{done['check_id']}.txt").write_text(done["card"])
        (cards / f"{done['check_id']}.json").write_text(json.dumps({"payment_ref": check.get("payment_ref")}))
        return {"paused": True, "verdict": done["verdict"], "check_id": done["check_id"], "say": done["approval_text"],
                "options": [o["id"] for o in done.get("options", [])]}, ""

    def pay(self, deal_id: str, payment_ref: str, out: str, check_id: str = "") -> dict:
        """The buyer's payment: the final review, then the payer leg written to ``out`` for the seller. This
        drives no rail: ``payment_ref`` is the reference the caller observed. A check that pauses returns
        the card for the user; answer it (:meth:`approve`) and call again with its ``check_id``."""
        ctx = self.context(deal_id)
        if ctx["rail"] != "lightning":  # before the check seals anything
            raise Refused(f"this node pays on lightning only; the deal's rail is {ctx['rail']}")
        if check_id and self._shown(check_id).get("payment_ref") != payment_ref:
            raise Refused("payment_ref is not the reference this check was made for")
        paused, check_id = self._gate(deal_id, "pay", {
            "amount_minor": ctx["price_minor"],
            "terms": {"item": ctx["item"], "price_minor": ctx["price_minor"], "currency": ctx["currency"]},
            "who": {"name": "the seller", "domain": "the-seller.invalid"},
            "recourse": {"rail": ctx["rail"], "refundable": ctx["refundable"]}, "payment_ref": payment_ref}, check_id)
        if paused:
            return paused
        try:
            self._run("settle", "--deal", deal_id, "--sent", "--check", check_id, "--payment-ref-type", "ln.payment_hash")
        except Refused as exc:
            if not any(w in str(exc) for w in ("in flight", "is used")):
                raise  # else a retry: the payment is counted as in flight, or its leg is sealed and is written again
        leg = self._run("settle", "--deal", deal_id, "--check", check_id, "--status", "settled", "--out", out)
        self._settled(deal_id, ctx["rail"], leg)
        return {"deal_id": deal_id, **_leg(leg)}

    def received(self, deal_id: str, amount_minor: int, payment_ref: str, out: str) -> dict:
        """The seller's payee leg: what it received, written to ``out`` for the buyer."""
        leg = self._run("settle", "--deal", deal_id, "--received", str(amount_minor), "--receive-fee", "0",
                           "--status", "settled", "--payment-ref-type", "ln.payment_hash",
                           "--payment-ref", payment_ref, "--out", out)
        self._settled(deal_id, self.context(deal_id)["rail"], leg)
        return {"deal_id": deal_id, **_leg(leg)}

    def _settled(self, deal_id: str, rail: str, leg: dict) -> None:
        """Note this node's own leg sealed as settled; a retry that writes the same leg again finds the note."""
        self.note(SETTLED, {"deal_id": deal_id, "rail": rail, "leg": leg["leg"], "digest": leg["digest"]},
                  once="digest", subject_ref=deal_id)

    def deliver(self, deal_id: str, out: str, observed: str = "delivered", check_id: str = "") -> dict:
        """The seller's act (delivery): a commit review, then the act, written to ``out`` for the buyer."""
        ctx = self.context(deal_id)
        paused, check_id = self._gate(deal_id, "commit", {
            "terms": {"item": ctx["item"], "price_minor": ctx["price_minor"], "currency": ctx["currency"]},
            "who": {"name": "the buyer", "domain": "the-buyer.invalid"},
            "recourse": {"rail": ctx["rail"], "refundable": ctx["refundable"]}}, check_id)
        if paused:
            return paused
        done = self._run("perform", "--deal", deal_id, "--act", "delivered", "--check", check_id,
                            "--observed", observed, "--out", out)
        return {"deal_id": deal_id, **_leg(done)}

    def statement(self, deal_id: str, status: str, out: str) -> dict:
        """How the deal's rows went on this side. ``received``: the buyer got what it paid for and the seller was
        paid (both rows met). ``not_received``: the buyer paid and got nothing (``delivered`` not met), or the seller
        was not paid (``paid_as_agreed`` not met)."""
        if status not in ("received", "not_received"):
            raise Refused("status is received or not_received")
        buyer = self.context(deal_id)["role"] == "buyer"
        rows = {ROW: "met", DELIVERED: "met"}
        if status == "not_received":
            rows["delivered" if buyer else ROW] = "not_met"
        args = ["statement", "--deal", deal_id, "--out", out]
        for row, value in sorted(rows.items()):
            args += ["--statement", f"{row}={value}"]
        done = self._run(*args)
        return {"deal_id": deal_id, "digest": done["digest"], "rows": rows}

    def report(self, deal_id: str, out: str, wait: float = 0) -> dict:
        """The deal's report as an Evidence Bundle written to ``out``. A report rests on a checkpoint:
        with ``wait`` seconds it waits for the cadence tick that covers the deal."""
        deadline = time.monotonic() + wait
        while True:
            ticked = self.tick()
            try:
                self._run("report", "--deal", deal_id, "--bundle", out)
                return {"deal_id": deal_id, "file": out, "tick": ticked.get("state")}
            except Refused as exc:
                if "after its last checkpoint" not in str(exc) or time.monotonic() >= deadline:
                    raise
                time.sleep(min(15.0, max(1.0, deadline - time.monotonic())))
