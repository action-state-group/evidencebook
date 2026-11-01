# SPDX-License-Identifier: Apache-2.0
"""A Goose node on one evidencebook shelf: its deals, its cadence and its
History cards. :mod:`loop_server` exposes it to Goose as MCP tools; this
module has no MCP in it, so a test or another host drives it directly.

One shelf (``<home>/shelf.db``) holds everything the node's key answers for
(design note evidencebook#254 §3.2):

- **one book per deal**, ``deal/<deal_id>``: the deal's x-deal-v0 steps
  (:mod:`deal_records`), each a Capsule the node seals itself and the book
  commits as its record (``Book.append_capsule``), carrying the deal id as
  its ``deal_id`` member so a relying party joins the two parties' halves
  on it (EB -02 ``composed/v1``, ``pre_agreed_identifier``);
- **the node book**, ``goose/node``: what the node did outside any deal
  (joining, admitting a peer, a counterparty's half it verified, the
  evidence requests it sent and their answers);
- **the imported ledger**, ``goose/ledger``: an existing capsule-emit flat
  ledger, copied once as a host log (:meth:`Node.import_ledger`), its
  records put as they were sealed and never re-signed;
- **the cadence**, ``goose/cadence``: every live log's checkpoint committed
  at each tick, the one checkpoint a witness sees, and the anchor of the
  node's ``standard_history/1`` card.

The node's keys and the deal store secret live beside the shelf, created on
first use with mode 0600. Nothing here moves money or opens a connection.
"""
from __future__ import annotations

import base64
import hashlib
import json
import os
import shutil
import sqlite3
import threading
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from agent_action_capsule import standard_history as sh
from agent_action_capsule.canonical import jcs
from capsule_emit import produce
from capsule_emit.artifact import PAYLOAD_DIGEST, PRESENT, Artifact
from capsule_emit.artifact.sqlite import SqliteArtifactStore
from capsule_emit.signing import AuthorshipVerdict, verify_capsule_signature_tristate
from capsule_emit.verification import verify_capsule
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import (
    Encoding, NoEncryption, PrivateFormat, load_pem_private_key,
)

from evidencebook import (
    CITES, DERIVATION_HISTORY_CARD, PRODUCER_CLAIM, RECORD_HEADER_MEMBER, Coverage, Ed25519Signer, Entry,
    EvidenceRequest, Link, Message, NotFoundError, Subject, deal, encode_request,
)
from evidencebook.cadence import CADENCE_EXTENSION, CadenceConfig
from evidencebook.shelf import Shelf

import deal_records as dr

__all__ = ["Node", "verify_half", "DISPUTE_RECEIVED", "DISPUTE_REFUSED", "NODE_BOOK", "LEDGER_LOG", "CADENCE_LOG", "NAMESPACE", "POLICY", "Refused"]

NODE_BOOK = "goose/node"
LEDGER_LOG = "goose/ledger"
CADENCE_LOG = "goose/cadence"
#: The shelf's one artifact store namespace (§2.5).
NAMESPACE = "goose"
#: The disclosure policy the node's card is under (SH §5.1): every deal, all
#: time. Its digest is the SHA-256 of its JCS.
POLICY = {"window": {"all": True}}
#: The kind of holder the node's key answers for: a person's agent.
HOLDER_KIND = "person"
#: The record types of the node book.
JOINED, ADMITTED, HALF_RECEIVED, ASKED_TYPE = "goose_joined", "goose_admitted", "goose_half_received", "goose_asked"
#: A referee's Result delivered into this book, and one refused on delivery
#: (step 09, evidencebook#310).
DISPUTE_RECEIVED, DISPUTE_REFUSED = "goose_dispute_received", "goose_dispute_refused"
#: A model call the node sealed (``Node.seal_model_call``).
MODEL_CALL = "goose_model_call"


def verify_half(half: Any, checkpoint_key_id: str) -> dict:
    """Check a counterparty's half (:meth:`Node.half`) on its own bytes:
    each step's Capsule passes Class 1 and its producer envelope verifies
    under the half's one key; it is that deal's step ``n`` (its ``action_id``
    and ``deal_id``), carries its record header, chains to the step before
    it, and commits its payload, which passes the profile's record checks
    (x-deal-v0 §8); and the half's checkpoint is signed by
    ``checkpoint_key_id``, the counterparty's checkpoint key the caller
    pinned (x-deal-v0 §10, "Admission"), and commits exactly these steps (the
    MMR over their ids has its peaks). Returns the deal id,
    the signing key, the steps' records and the digest of the final close
    half (x-deal-v0 §10), or raises :class:`Refused`."""
    from cll.checkpoint import core

    from evidencebook._cll import read_checkpoint

    try:
        deal_id, steps = half["deal_id"], half["steps"]
        if half.get("half") != "goose-deal-half/1" or not isinstance(steps, list) or not steps:
            raise Refused("not a goose-deal-half/1 with steps")
        key_id = steps[0]["capsule"].get("key_id")
        records, nodes, parent = [], _Nodes(), None
        for n, step in enumerate(steps, start=1):
            capsule, payload = dict(step["capsule"]), step["payload"]
            signature, signer = capsule.pop("signature", None), capsule.pop("key_id", None)
            produce.verify_capsule(jcs(capsule))
            verified = produce.verify_envelope(capsule["capsule_id"], bytes.fromhex(signature))
            if signer != key_id or bytes(verified.public_key).hex() != key_id:
                raise Refused(f"step {n} is not signed by the half's key")
            if capsule.get("action_id") != f"{deal_id}/{n}" or capsule.get("deal_id") != deal_id:
                raise Refused(f"step {n} is not step {n} of deal {deal_id}")
            header = capsule.get(RECORD_HEADER_MEMBER)
            if not isinstance(header, dict) or header.get("record_type") != deal.STEP_RECORD_TYPE \
                    or header.get("seq") != n or header.get("subject_ref") != f"{deal_id}/{n}":
                raise Refused(f"step {n} does not carry its deal step's record header")
            chained = (capsule.get("chain") or {}).get("parent_capsule_id")
            if chained != parent:
                raise Refused(f"step {n} does not chain to the step before it")
            digest = deal.digest(payload)
            if capsule["model_attestation"]["compute_attestation"].get("agent_input_digest") != digest \
                    or header.get("payload_commitments") != [digest]:
                raise Refused(f"step {n}'s Capsule does not commit its payload")
            deal.check(payload)
            core.add_leaf(nodes, core.leaf_hash(bytes.fromhex(capsule["capsule_id"])))
            records.append({"capsule": step["capsule"], "payload": payload, "digest": digest})
            parent = capsule["capsule_id"]
        # The profile's chain stage (§6), which the record checks leave out:
        # the reference checker's own, as the step-09 referee runs it.
        try:
            _profile_checker().check_chain([r["payload"] for r in records])
        except Exception as exc:  # its StageError, or a chain it cannot read
            raise Refused(f"the half's steps are not a deal the profile allows: {exc}") from None
        checkpoint = read_checkpoint(bytes.fromhex(half["checkpoint"]["statement"]))
        if checkpoint.key_id != checkpoint_key_id:
            raise Refused("the half's checkpoint is not signed by the counterparty's pinned checkpoint key")
        if checkpoint.entries != len(steps):
            raise Refused("the half's checkpoint does not cover exactly its steps")
        if [nodes.hashes[p] for p in core.peaks(nodes.size())] != list(checkpoint.peaks):
            raise Refused("the half's checkpoint does not commit these steps")
    except Refused:
        raise
    except Exception as exc:  # a malformed half: any step or member that does not check out
        raise Refused(f"the half does not verify: {exc}") from None
    closes = [r for r in records if r["payload"].get("x-deal-v0", {}).get("record_type") == deal.TYPE_CLOSE]
    final = closes[-1]["capsule"] if closes else None
    return {"deal_id": deal_id, "key_id": key_id, "records": records,
            "close_half_digest": hashlib.sha256(jcs(final)).hexdigest() if final else None}


def _profile_checker():
    """The x-deal-v0 reference checker (``check_profile.py``), loaded once
    from this repository, or from ``$GOOSE_CHECK_PROFILE`` in a copy of the
    example outside it."""
    global _CHECKER
    if _CHECKER is None:
        import importlib.util

        path = os.environ.get("GOOSE_CHECK_PROFILE") or str(
            Path(__file__).resolve().parents[3] / "dealprofile" / "x-deal-v0" / "check_profile.py")
        spec = importlib.util.spec_from_file_location("check_profile", path)
        if spec is None or not os.path.exists(path):
            raise Refused(f"the profile's reference checker is not at {path}: set GOOSE_CHECK_PROFILE")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _CHECKER = module
    return _CHECKER


_CHECKER = None


class _Nodes:
    """An MMR's nodes, for :func:`cll.checkpoint.core.add_leaf`."""

    def __init__(self) -> None:
        self.hashes: list[bytes] = []

    def size(self) -> int:
        return len(self.hashes)

    def node(self, pos: int) -> bytes:
        return self.hashes[pos]

    def append_nodes(self, hashes: list[bytes]) -> None:
        self.hashes.extend(hashes)


def _authentic(capsule: Any) -> bool:
    """The capsule recomputes and its signature verifies under the key_id it carries."""
    return (isinstance(capsule, dict) and verify_capsule(capsule).ok
            and verify_capsule_signature_tristate(capsule)[0] is AuthorshipVerdict.AUTHORED)


def _referee() -> Any:
    """capsule-engine's referee, which holds the Result's receive check. It is
    this example's one extra dependency, imported on first use."""
    try:
        from capsule_engine import referee
    except ImportError:
        raise Refused("receiving a referee's Result needs a capsule-engine that has capsule_engine.referee "
                      "installed beside this node") from None
    return referee


class _BookRecords:
    """A book's records as agent-action-capsule's bundle producer reads a
    record store: ``get(capsule_id)`` is the Capsule's JCS and its Producer
    Envelope, ``not_found`` for any other id."""

    def __init__(self, book) -> None:
        self._book = book

    def get(self, capsule_id: str):
        from agent_action_capsule.bundle import StoredRecord
        from capsule_emit.artifact import NotFoundError as NoRecord

        held = self._book._stored.get(capsule_id)
        if held is None:
            raise NoRecord(capsule_id)
        return StoredRecord(jcs(held.capsule), held.envelope)


class Refused(ValueError):
    """A tool call the node refuses: nothing was sealed."""


@dataclass(frozen=True)
class SealedStep:
    record_id: str
    seq: int
    record_type: str


def _key(path: Path) -> Ed25519PrivateKey:
    """The Ed25519 key in ``path`` (PKCS#8 PEM, capsule-emit's
    ``LocalKeypairSigner`` format), created with mode 0600 if absent."""
    if path.exists():
        key = load_pem_private_key(path.read_bytes(), password=None)
        if not isinstance(key, Ed25519PrivateKey):
            raise Refused(f"{path} does not hold an Ed25519 key")
        return key
    key = Ed25519PrivateKey.generate()
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as fh:
        fh.write(key.private_bytes(Encoding.PEM, PrivateFormat.PKCS8, NoEncryption()))
    return key


def _secret(path: Path) -> bytes:
    if not path.exists():
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as fh:
            fh.write(os.urandom(32))
    return path.read_bytes()


def _raw(key: Ed25519PrivateKey) -> bytes:
    return key.private_bytes(Encoding.Raw, PrivateFormat.Raw, NoEncryption())


@contextmanager
def _flat_ledger_lock(source: Path):
    """capsule-emit's writer lock on a flat ledger: an exclusive ``flock`` on
    ``<ledger>.lock`` beside it, the file every capsule-emit writer of that
    ledger takes. ``import_flat`` requires it held, so no line is appended
    between the copy, its check and the move. A held lock refuses: stop the
    ledger's writer (an older Goose server) first. A ledger store directory
    is locked at its real path, as capsule-emit resolves it
    (``ledger_store.store_root``), so a symlink to it locks the same file."""
    import fcntl

    from capsule_emit.ledger_store import store_root

    locked = store_root(source) if source.is_dir() else source
    with open(locked.with_name(locked.name + ".lock"), "a+", encoding="utf-8") as fh:
        try:
            fcntl.flock(fh.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise Refused(f"another writer holds {source}'s lock: stop it, then import") from None
        try:
            yield
        finally:
            fcntl.flock(fh.fileno(), fcntl.LOCK_UN)


def _parse_time(text: str) -> datetime:
    return datetime.fromisoformat(text.replace("Z", "+00:00"))


class Node:
    """One Goose node's shelf. Open it with :meth:`open`; one process holds
    a home at a time (the shelf's writer lock)."""

    def __init__(self, home: Path, operator: str, developer: str, now, witnesses: Any, witness_client: Any = None):
        self.home, self.operator, self.developer = home, operator, developer
        self._witnesses, self._witness_client = witnesses, witness_client
        self._now = now or (lambda: datetime.now(timezone.utc))
        self._inflight: datetime | None = None
        self._lock = threading.RLock()
        self._producer = _key(home / "producer.key")
        self._identity = produce.new_ed25519_signing_identity(_raw(self._producer))
        self.signer = Ed25519Signer(self._producer)
        self._checkpoint_key = Ed25519Signer(_key(home / "checkpoint.key"))
        self._store_secret = _secret(home / "store.secret")
        self._db = sqlite3.connect(home / "shelf.db", timeout=5.0, isolation_level=None, check_same_thread=False)
        try:
            for statement in ("PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"):
                self._db.execute(statement)
            # The witness receipt a book checkpoint got, by the checkpoint's digest: asked once.
            self._db.execute("CREATE TABLE IF NOT EXISTS goose_book_receipts (book_id TEXT NOT NULL, "
                             "checkpoint_sha256 TEXT NOT NULL, receipt TEXT NOT NULL, PRIMARY KEY (book_id, checkpoint_sha256))")
            self.originals = SqliteArtifactStore(self._db, NAMESPACE, [self.signer.public_key(), *self._trusted()])
            self.originals.init()
            self.shelf = Shelf.open_sqlite(home / "shelf.db", operator=operator, developer=developer,
                                           signer=self.signer, checkpoint_key=self._checkpoint_key,
                                           originals=self.originals, now=self._clock, lock_timeout=10,
                                           # A deal's log holds its steps only, so no book keeps
                                           # authenticated indexes, whose roots would be records too.
                                           authenticated_indexes=lambda _: [])
        except BaseException:
            self._db.close()
            raise
        try:
            self.cadence = self.shelf.cadence(CadenceConfig(CADENCE_LOG, ended=lambda _: False, witnesses=witnesses))
        except BaseException:
            self.close()
            raise

    @classmethod
    def open(cls, home: str | os.PathLike, *, operator: str, developer: str = "goose-loop@v1", now=None,
             witnesses: Any = None, witness_client: Any = None) -> Node:
        """The node at ``home``, created on first use. ``witnesses`` is the
        witness directory receipts are checked under (a ``witnesses.json``
        mapping), or ``None``. With one witness in it, a book bundle carries
        that witness's receipt for the book's own checkpoint, asked of
        ``witness_client(endpoint)`` (a ``cll.checkpoint.witness`` submitter;
        ``HttpWitnessClient`` when not given)."""
        path = Path(home).expanduser()
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
        if not operator:
            raise Refused("an operator name is required")
        return cls(path, operator, developer, now, witnesses, witness_client)

    def close(self) -> None:
        try:
            self.shelf.release()
        finally:
            self._db.close()

    def __enter__(self) -> Node:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def _clock(self) -> datetime:
        return self._inflight or self._now()

    def _trusted(self) -> list[bytes]:
        """Keys whose records the artifact store holds besides the node's
        own: the signers of an imported ledger."""
        path = self.home / "trusted-keys.json"
        return [bytes.fromhex(k) for k in json.loads(path.read_text())] if path.exists() else []

    @property
    def key_id(self) -> str:
        return self.signer.public_key().hex()

    @property
    def checkpoint_key_id(self) -> str:
        return self._checkpoint_key.public_key().hex()

    # -- the node book ---------------------------------------------------------------

    def note(self, record_type: str, statement: dict, subject_ref: str = "") -> SealedStep:
        """Seal one record of the node book."""
        with self._lock:
            record = self.shelf.open(NODE_BOOK).append(
                Entry(record_type, PRODUCER_CLAIM, subject_ref=subject_ref, statement=statement))
            return SealedStep(record.record_id, record.seq, record_type)

    def notes(self, record_type: str) -> list[tuple[Any, dict]]:
        """The node book's records of ``record_type``, oldest first, with their statements."""
        with self._lock:
            return [(r, r.header.statement) for r in self.shelf.open(NODE_BOOK).records()
                    if r.header.record_type == record_type]

    def seal_model_call(self, provider: str, model_id: str, prompt: Any, completion: Any,
                        at: datetime | None = None) -> tuple[SealedStep, dict]:
        """Seal one model call Goose made as an effect-boundary record of the
        node book: a Capsule the node signs in its own process, so its effect
        claims ``runtime_claimed`` and no more, its ``model_attestation``
        names ``provider`` and ``model_id``. ``prompt`` and ``completion`` (JSON
        values) are committed by digest; the node keeps the prompt as the
        record's payload and the completion by its digest only. Returns the record and the
        Capsule as a verifier reads it (with its ``signature`` and ``key_id``)."""
        if not provider or not model_id:
            raise Refused("a model call names its provider and model_id")
        at = (at or self._now()).replace(microsecond=0)
        action_id = f"model-call-{os.urandom(8).hex()}"
        payload = json.dumps(prompt, sort_keys=True).encode()
        with self._lock:
            entry = Entry(MODEL_CALL, PRODUCER_CLAIM, subject_ref=action_id)

            def seal(_header, header_json: bytes):
                return produce.seal(produce.SealInput(
                    capsule=produce.Input(
                        action_id=action_id, action_type=produce.ActionType.FYI, operator=self.operator,
                        developer=self.developer, timestamp=at,
                        effect=produce.Effect("inference_completion", produce.EffectStatus.CONFIRMED,
                                              produce.IrreversibilityClass.TWO_WAY,
                                              produce.EffectAttestation.RUNTIME_CLAIMED,
                                              request_digest=produce.digest_json(prompt),
                                              response_digest=produce.digest_json(completion)),
                        extensions=(produce.Extension(RECORD_HEADER_MEMBER, header_json),)),
                    identity=self._identity, payload=json.loads(payload), agent_output=completion,
                    model=produce.Model(provider, model_id)))

            self._inflight = at
            try:
                record = self.shelf.open(NODE_BOOK).append_capsule(
                    entry, [Artifact("payload", PRESENT, PAYLOAD_DIGEST, payload)], seal)
            finally:
                self._inflight = None
            return SealedStep(record.record_id, record.seq, MODEL_CALL), self.capsule(record.record_id)

    def capsule(self, record_id: str) -> dict:
        """The Capsule a caller-sealed record is, with its producer
        ``signature`` and ``key_id`` (the form a verifier reads)."""
        with self._lock:
            held = self.originals.get(record_id)
            capsule = json.loads(held.capsule)
            capsule["signature"], capsule["key_id"] = bytes(held.producer_envelope).hex(), self.key_id
            return capsule

    def book_checkpoint(self, book_id: str) -> bytes:
        """The COSE statement of the checkpoint :meth:`book_bundle` builds
        on: the book's newest."""
        with self._lock:
            return self.shelf.open(book_id).checkpoint().statement

    def _book_receipt(self, book_id: str, checkpoint) -> dict:
        """The directory's witness's receipt for ``checkpoint``, in a bundle's
        ``checkpoint.witnesses`` form; asked of the witness once per
        checkpoint and kept."""
        rows = self._witnesses.get("witnesses") if isinstance(self._witnesses, dict) else self._witnesses
        if not isinstance(rows, list) or len(rows) != 1 or not rows[0].get("endpoint"):
            raise Refused("a book receipt needs a witness directory with exactly one witness and its endpoint")
        endpoint = rows[0]["endpoint"]
        digest = hashlib.sha256(checkpoint.statement).hexdigest()
        with self._lock:
            held = self._db.execute("SELECT receipt FROM goose_book_receipts WHERE book_id = ? AND checkpoint_sha256 = ?",
                                    (book_id, digest)).fetchone()
            if held:
                return json.loads(held[0])
            from cll.checkpoint.witness import HttpWitnessClient
            from cll.log import ContentionError, RejectedError

            client = (self._witness_client or HttpWitnessClient)(endpoint)
            try:
                got = client.submit(checkpoint.statement)
            except (ContentionError, RejectedError) as exc:
                raise Refused(f"the witness at {endpoint} gave no receipt for {book_id}: {exc}") from None
            receipt = {"ts_url": endpoint, "entry_hash": got.entry_hash,
                       "receipt_b64": base64.b64encode(bytes(got.bytes)).decode(), "leaf_index": got.leaf_index,
                       "tree_size": got.tree_size}
            self._db.execute("INSERT INTO goose_book_receipts VALUES (?, ?, ?)", (book_id, digest, json.dumps(receipt)))
            return receipt

    def book_bundle(self, book_id: str, record_id: str) -> dict:
        """An Evidence Bundle (``evidence-bundle/v2``) proving ``record_id``
        is in ``book_id`` at its position under the book's newest checkpoint
        (cut now if the book grew): the record, its inclusion proof and the
        interval from the book's first record to that checkpoint, built by
        agent-action-capsule's producer and checked by its strict verifier.
        With a witness directory the witness's receipt for that checkpoint
        is on the bundle's ``checkpoint.witnesses`` and must verify under the
        directory. When a witnessed cadence tick also carries it, the cadence
        chain travels as the ``x-cadence-witness/v0`` extension."""
        from agent_action_capsule import bundle as aac

        with self._lock:
            if book_id not in self.shelf.books():
                raise Refused(f"this node holds no book {book_id}")
            book = self.shelf.open(book_id)
            if record_id not in {r.record_id for r in book.records()}:
                raise Refused(f"book {book_id} holds no record {record_id}")
            checkpoint = book.checkpoint()
            extensions = {}
            try:
                extensions[CADENCE_EXTENSION] = self.cadence.chain(checkpoint.statement)
            except NotFoundError:
                pass  # no witnessed tick carries this checkpoint yet
            try:
                built = aac.build_evidence_bundle(
                    aac.proof_source_from_cll(book._substrate.backend, _BookRecords(book)),
                    root=record_id, interval_from=1, extensions=extensions or None)
            except aac.BundleBuildError as exc:
                raise Refused(f"no bundle: {exc}") from None
            if self._witnesses is not None:
                built["checkpoint"]["witnesses"] = [self._book_receipt(book_id, checkpoint)]
            result = aac.verify_bundle(built, strict=True, witness_directory=self._witnesses)
            if self._witnesses is not None and [r.status for r in result.witness_receipts] != ["pass"]:
                raise Refused(f"the witness receipt does not verify under the directory: {result.witness_receipts}")
            if str(result.verdict) not in ("VALID", "Verdict.VALID"):
                raise Refused(f"the bundle does not verify: {result.verdict}")
            return built

    def admission(self, peer: str) -> dict | None:
        """The newest admission sealed for ``peer``."""
        found = None
        for record, statement in self.notes(ADMITTED):
            if statement.get("peer") == peer:
                found = {"record_id": record.record_id, **statement}
        return found

    def ask(self, request: Any, responder: str) -> Any:
        """Seal an evidence request in the node book before it is sent
        (``Book.request``); returns what to send."""
        with self._lock:
            return self.shelf.open(NODE_BOOK).request(request, responder)

    def record_reply(self, request_record_id: str, reply: Any, keys: Any) -> Any:
        """Verify and seal the reply to a request the node book sent
        (``Book.record_response``)."""
        with self._lock:
            return self.shelf.open(NODE_BOOK).record_response(request_record_id, reply, keys)

    def verify_peer_half(self, half: Any) -> dict:
        """:func:`verify_half` under the checkpoint key this node's admission
        of the half's signer pinned; a signer admitted with anything but GO,
        or not at all, is refused."""
        try:
            signer = half["steps"][0]["capsule"]["key_id"]
        except (KeyError, IndexError, TypeError):
            raise Refused("not a goose-deal-half/1 with steps") from None
        admission = self.admission(signer)
        if admission is None:
            raise Refused(f"no admission sealed for {signer}: call loop_admit first")
        if admission["decision"] != "GO":
            raise Refused(f"the sealed admission for {signer} is {admission['decision']}: no deal")
        return verify_half(half, admission["checkpoint_key_id"])

    # -- deals -----------------------------------------------------------------------

    def _deal_key(self, deal_id: str) -> bytes:
        return deal.deal_key(self._store_secret, deal_id)

    def _payloads(self, book) -> list[bytes]:
        out = []
        for record in book.records():
            held = self.originals.get(record.record_id)
            out.append(next(bytes(a.content) for a in held.artifacts if a.name == "payload"))
        return out

    def deal(self, deal_id: str) -> dr.DealChain:
        """The chain of a deal this node holds, rebuilt from its book."""
        book_id = f"deal/{deal_id}"
        with self._lock:
            if book_id not in self.shelf.books():
                raise Refused(f"this node holds no deal {deal_id}")
            book = self.shelf.open(book_id)
            payloads = self._payloads(book)
            baseline = json.loads(payloads[0])
            body = baseline["body"]
            role = body["intent"].get("party_role", dr.ROLE_BUYER)
            terms = dr.Terms(body["terms"]["item"], body["terms"]["price_minor"], body["terms"]["currency"],
                             body["recourse"]["rail"], body["recourse"]["refundable"])
            first = book.records()[0].record_id
            peer = json.loads(book.opening(first, deal.LOCAL_FIELD).text)["open"]["peer"]
            return dr.DealChain.resume(deal_id, role, self._deal_key(deal_id), peer, terms, payloads)

    def has_deal(self, deal_id: str) -> bool:
        with self._lock:
            return f"deal/{deal_id}" in self.shelf.books()

    def new_deal(self, deal_id: str, role: str, peer: str, terms: dr.Terms) -> dr.DealChain:
        if f"deal/{deal_id}" in self.shelf.books():
            raise Refused(f"deal {deal_id} is already open on this node")
        return dr.DealChain(deal_id, role, self._deal_key(deal_id), peer, terms)

    def seal(self, chain: dr.DealChain, steps: list[dr.Step]) -> list[SealedStep]:
        """Seal ``steps`` of ``chain``, in order, on the deal's book."""
        sealed = []
        with self._lock:
            book = self.shelf.open(f"deal/{chain.deal_id}")
            for step in steps:
                records = book.records()
                parent = None
                if records:
                    relation = deal.RELATION_FOLLOWS
                    parent = deal.Chain(records[-1].record_id, relation)
                capsule, header = deal.step_capsule(deal.StepInput(
                    chain.deal_id, len(records) + 1, self.operator, self.developer, step.at, parent, None,
                    step.decide, step.local, step.amount, step.currency))
                entry = Entry(header.record_type, header.epistemic_type, subject_ref=header.subject_ref,
                              links=tuple(Link(CITES, c) for c in header.cites), fields=dict(header.fields))

                def seal(_header, header_json: bytes, capsule=capsule, step=step):
                    return produce.seal(produce.SealInput(
                        capsule=deal.emit_input(capsule, deal.Extension(RECORD_HEADER_MEMBER, header_json)),
                        identity=self._identity, payload=json.loads(step.payload)))

                self._inflight = step.at.replace(microsecond=0)
                try:
                    record = book.append_capsule(entry, [Artifact("payload", PRESENT, PAYLOAD_DIGEST, step.payload)],
                                                 seal)
                finally:
                    self._inflight = None
                sealed.append(SealedStep(record.record_id, record.seq, step.record_type))
        return sealed

    def half(self, deal_id: str) -> dict:
        """This node's half of a deal, as a counterparty receives it: every
        step's Capsule (with its producer ``signature`` and ``key_id``) and
        payload, in order, and the book's checkpoint covering them."""
        with self._lock:
            book = self.shelf.open(f"deal/{deal_id}")
            checkpoint = book.checkpoint()
            steps = []
            for record in book.records():
                held = self.originals.get(record.record_id)
                capsule = self.capsule(record.record_id)
                payload = next(bytes(a.content) for a in held.artifacts if a.name == "payload")
                steps.append({"capsule": capsule, "payload": json.loads(payload)})
            return {"half": "goose-deal-half/1", "deal_id": deal_id, "book_id": book.book_id, "steps": steps,
                    "checkpoint": {"key_id": self.checkpoint_key_id, "statement": checkpoint.statement.hex()}}

    # -- 09 Dispute ------------------------------------------------------------------

    def receive_dispute_result(self, capsule: Any, octets: bytes, parties: Any) -> SealedStep:
        """Seal a referee's Result into this book as received.

        *octets* are the Result document exactly as the referee wrote them (its
        JCS); *capsule* is the referee's sealed ``referee.result``; *parties*
        is ``{"buyer": {"half", "action", "terms"}, "seller": {...}}``, each
        half with the opening of its commitment, as the referee's
        ``Party`` takes them. The Result must verify, be signed by the referee
        both halves' terms name, name these two halves, and be about a deal this
        node's key sealed one half of. Anything else is refused, and the refusal
        is sealed with its code (never silence). The record's ``result_digest``
        is the SHA-256 of *octets*, so every party's book holds the same one."""
        referee = _referee()
        digest = hashlib.sha256(octets).hexdigest()
        with self._lock:  # check and seal as one step, or two deliveries of one Result both seal
            try:
                try:
                    self._check_dispute_result(referee, capsule, octets, parties)
                except (ValueError, TypeError, AttributeError, KeyError) as exc:
                    if isinstance(exc, referee.RefereeError):
                        raise
                    # A float in the octets, terms that are not an object: a delivery that is no Result.
                    raise referee.RefereeError("not_a_result", f"the delivery cannot be read: {exc}") from exc
            except referee.RefereeError as exc:
                if not self._noted(DISPUTE_REFUSED, digest, code=exc.code):
                    self.note(DISPUTE_REFUSED, {"result_digest": digest, "code": exc.code}, subject_ref=digest)
                raise Refused(str(exc)) from exc
            if self._noted(DISPUTE_RECEIVED, digest):
                raise Refused("this Result is already received in this book")
            halves = (parties["buyer"]["half"]["capsule_id"], parties["seller"]["half"]["capsule_id"])
            return self.note(DISPUTE_RECEIVED, {
                "result_digest": digest, "result_capsule_id": capsule["capsule_id"], "referee": capsule["key_id"],
                "buyer_half": halves[0], "seller_half": halves[1],
            }, subject_ref=digest)

    def _noted(self, record_type: str, digest: str, **members: Any) -> bool:
        return any(st.get("result_digest") == digest and all(st.get(k) == v for k, v in members.items())
                   for _, st in self.notes(record_type))

    def _check_dispute_result(self, referee: Any, capsule: Any, octets: bytes, parties: Any) -> None:
        err = referee.RefereeError
        try:
            roles = {role: parties[role] for role in ("buyer", "seller")}
            doc = json.loads(octets)
            halves = {r: p["half"] for r, p in roles.items()}
            terms = {r: p["terms"] for r, p in roles.items()}
            actions = {r: p["action"] for r, p in roles.items()}
        except (KeyError, TypeError, ValueError):
            raise err("not_a_result", "the delivery is not a Result document with both parties' openings") from None
        if jcs(doc) != octets:
            raise err("result_mismatch", "the Result octets are not the referee's JCS bytes")
        for role in roles:
            # verify_referee_result takes the openings as given: bind each to
            # its signed half here, or a forged terms file could name its own referee.
            half = halves[role]
            if not _authentic(half):
                raise err("half_unverified", f"the {role}'s half does not verify under the key it carries")
            committed = ((half.get("model_attestation") or {}).get("compute_attestation") or {}).get("action_digest")
            if committed != referee.action_digest(actions[role], terms[role]):
                raise err("opening_mismatch", f"the {role}'s opening does not recompute its half's action_digest")
        if self.key_id not in {halves["buyer"].get("key_id"), halves["seller"].get("key_id")}:
            raise err("not_a_party", "this node's key sealed neither half the Result judges")
        if halves["buyer"].get("key_id") == halves["seller"].get("key_id"):
            raise err("halves_unpaired", "both halves are signed by one key")
        referee.verify_referee_result(capsule, doc, buyer_half=halves["buyer"], seller_half=halves["seller"],
                                      buyer_terms=terms["buyer"], seller_terms=terms["seller"])

    # -- the imported ledger ---------------------------------------------------------

    def import_ledger(self, source: str | os.PathLike) -> dict:
        """Copy an existing capsule-emit flat ledger into the host log
        ``goose/ledger`` (design note §8): each line put as it was sealed and
        appended in order, nothing re-signed or dropped, re-runnable, under
        the flat ledger's writer lock. Only after the log holds exactly the
        flat ledger's records in order are its files moved to ``<home>/archive/pre-shelf/``; on any mismatch they
        stay where they are and nothing is moved."""
        source = Path(source).expanduser()
        if not source.exists():
            raise Refused(f"no flat ledger at {source}")
        with _flat_ledger_lock(source):
            return self._import_locked(source)

    def _import_locked(self, source: Path) -> dict:
        from capsule_emit import ledger as flat
        from capsule_emit.store_backed_ledger import import_flat

        keys = {e["key_id"] for e in flat.read_ledger_entries(str(source))
                if isinstance(e, dict) and isinstance(e.get("key_id"), str)}
        missing = sorted(keys - {self.key_id} - {k.hex() for k in self._trusted()})
        if missing:
            # The artifact store verifies every record against its trusted
            # keys, so the ledger's own signers are trusted first; a restart
            # opens the store with them (the caller reopens the node).
            (self.home / "trusted-keys.json").write_text(json.dumps(sorted({*missing,
                                                                           *(k.hex() for k in self._trusted())})))
            return {"reopen": True, "trusted": missing}
        with self._lock:
            host = self.shelf.host_log(LEDGER_LOG, in_cadence=True)
            ledger, report = host.write(lambda stores: import_flat(source, stores.log, stores.originals))
            capsules = [e for e in flat.read_ledger_entries(str(source))
                        if isinstance(e, dict) and e.get("capsule_id") and e.get("kind") is None]
            unique = list(dict.fromkeys(e["capsule_id"] for e in capsules))
            if ledger.capsule_ids_in_order() != unique:
                raise Refused("the host log does not hold the flat ledger's records in order; the flat files are kept")
        archive = self.home / "archive" / "pre-shelf"
        archive.mkdir(parents=True, exist_ok=True)
        moved = archive / f"{source.name}.{datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')}"
        shutil.move(str(source), moved)
        return {"records": report.records, "resumed": report.resumed, "skipped": report.skipped,
                "chain_head": report.chain_head, "archived_to": str(moved)}

    def ledger_capsules(self) -> list[dict]:
        """The imported ledger's records, in order."""
        from capsule_emit.store_backed_ledger import Ledger

        with self._lock:
            if LEDGER_LOG not in self._host_logs():
                return []
            host = self.shelf.host_log(LEDGER_LOG, in_cadence=True)
            return host.write(lambda stores: [
                ledger.read_line(i) for ledger in [Ledger.open_stores(stores.log, stores.originals, linear=False,
                                                                       require_statements=False)[0]]
                for i in range(1, ledger.entries + 1)])

    def _host_logs(self) -> set[str]:
        with self.shelf._read() as db:
            return {r[0] for r in db.execute("SELECT log_id FROM shelf_logs WHERE kind = 'host'")}

    # -- the cadence and the cards ---------------------------------------------------

    def tick(self, now: datetime | None = None):
        """Run the cadence's tick if one is due."""
        with self._lock:
            return self.cadence.tick(now or self._now())

    def history_card(self, book_id: str) -> bytes:
        """``history_card/1`` for one book, as of its newest checkpoint: the
        reply octets a stranger's request gets (ER -02 §8.1)."""
        with self._lock:
            book = self.shelf.open(book_id)
            checkpoint = book.checkpoint()
            request = EvidenceRequest(Subject("full_history"), Coverage(expected_pin=checkpoint.anchor_digest),
                                      derivation=DERIVATION_HISTORY_CARD)
            reply, _ = book.respond(Message(encode_request(request)))
            if reply.envelope is None:
                raise Refused(f"the book refused its own history_card/1 request: {reply.message.octets[:200]!r}")
            return reply.message.octets

    def standard_history(self, tick: int = 0) -> dict:
        """``standard_history/1`` as of a cadence tick (the newest when 0):
        one x-deal-v0 deal entry per deal book (x-deal-v0 §11), each from the
        records the tick commits. Returns the card's JCS text and the inputs
        a verifier recomputes it from."""
        with self._lock:
            anchor = self.cadence.standard_history_anchor(tick)
            received = self._halves_received(anchor.extents.get(NODE_BOOK, 0))
            deals = []
            for book_id, extent in sorted(anchor.extents.items()):
                if not book_id.startswith("deal/") or book_id.count("/") != 1:
                    continue
                book = self.shelf.open(book_id)
                records = book.records()[:extent]
                payloads = self._payloads(book)[:extent]
                inputs = deal.HistoryInput(
                    tuple(deal.HistoryRecord(p, _parse_time(r.header.committed_at)) for p, r in zip(payloads, records)),
                    (), book_id[len("deal/"):] in received)
                entry = deal.standard_history_deal(inputs)
                if entry is not None:
                    deals.append(entry)
            as_of = {"anchor": anchor.anchor, "anchor_time": anchor.anchor_time,
                     "first_checkpoint_time": anchor.first_checkpoint_time}
            policy = {"digest": hashlib.sha256(jcs(POLICY)).hexdigest(), "window": POLICY["window"]}
            card = sh.derive_card(holder_kind=HOLDER_KIND, controller_class=sh.CONTROLLER_CLASSES[HOLDER_KIND],
                                  policy=policy, deals=deals, **as_of)
            properties = sh.check_card(card, policy=policy, as_of=as_of, deals=deals)
            if not all(v == "PASS" for v in properties.values()):
                raise Refused(f"the card fails its own inputs: {properties}")
            return {"card": jcs(card).decode(), "tick": anchor.tick.n, "as_of": as_of, "policy": policy,
                    "deals": deals, "properties": properties}

    def _halves_received(self, extent: int) -> set[str]:
        """The deals whose counterparty half this node verified, among the
        node book's first ``extent`` records."""
        return {s["deal_id"] for r, s in self.notes(HALF_RECEIVED) if r.seq <= extent}
