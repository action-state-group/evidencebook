# SPDX-License-Identifier: Apache-2.0
"""The book: records over a Checkpointed Local Log, and the evidence
request interaction on both sides of it.

As a requester a book records that it asked (:meth:`Book.request`, an
``evidence_request`` record sealed before sending), then either the reply
that came back (:meth:`Book.record_response`, ``evidence_response``) or that
none did (:meth:`Book.record_absence`, ``recorded_absence``). As a responder
it answers a request (:meth:`Book.respond`) with a signed envelope carrying
the canonical Artifact or a signed refusal, and seals an
``evidence_request_answered`` record for an authenticated requester. Every
record, checkpoint, artifact and reply is the bytes Go's evidencebook
writes for the same calls.
"""
from __future__ import annotations

import copy
import hashlib
import json
import os
import re
import threading
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field, replace
from datetime import datetime, timedelta, timezone
from typing import Any, Protocol

from agent_action_capsule.canonical import compute_capsule_id, jcs
from agent_action_capsule.evidence_request import (
    DERIVATION_HISTORY_CARD,
    REASON_COVERAGE_UNSATISFIABLE,
    REASON_DEADLINE_UNMET,
    REASON_CAPACITY_EXCEEDED,
    REASON_POLICY_DECLINED,
    SUBJECT_FORMS,
    Envelope,
    RequestError,
    Subject,
    parse_request,
    sign_envelope,
    sign_refusal,
    verify_cll_consistency,
    verify_cll_inclusion,
    verify_refusal_offline,
    VerificationError,
)
from agent_action_capsule.interop_profile._common import is_profile_time
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

from . import _decide
from ._limits import (
    MAX_REPLY_BYTES,
    MAX_REPLY_DEPTH,
    MAX_REPLY_ITEMS,
    MAX_REPLY_RECEIPTS,
    UNWITNESSED,
    WITNESS_RECEIPTS_MEMBER,
    WITNESSED,
    WitnessRow,
    carried_receipts,
    check_envelope_limits,
    check_reply_octets,
    witness_standing,
)
from ._capsule import capsule_preimage, seal_record
from ._cll import CLLSubstrate, Checkpoint, read_checkpoint
from ._index import default_indexes
from ._time import from_nanos, nanos, parse_ns, profile_time, profile_time_ns, rfc3339_nano, utc
from .errors import CorruptError, InvalidError, NotFoundError, ReleasedError, UnavailableError
from .record import (
    AVAILABLE,
    CITES,
    COMMIT_ALG_NONCE256,
    FIELD_NAME,
    DERIVED_METRIC,
    OBSERVED_EVENT,
    PRODUCER_CLAIM,
    RECORD_TYPE_ABSENCE,
    RECORD_TYPE_DISCLOSURE,
    RECORD_TYPE_INDEX_ROOT,
    RECORD_TYPE_REQUEST,
    RECORD_TYPE_REQUEST_ANSWERED,
    RECORD_TYPE_RESPONSE,
    RECORD_TYPE_RETENTION_COMMITMENT,
    Entry,
    Header,
    Link,
    Record,
)
from .request import (
    PROFILE,
    PROFILE_V1,
    MEDIA_TYPE_CBOR,
    MEDIA_TYPE_JSON,
    EvidenceRequest,
    Message,
    Reply,
    encode_envelope,
    encode_refusal,
    encode_request,
    parse_reply,
    read_request,
    refusal_conforming,
    request_profile,
    strict_hex,
    strict_json,
    _decoded,
)
from .signer import EvidenceSigner, Signer, key_id
from .store import MemoryStore, Store, StoredRecord

__all__ = [
    "DEFAULT_DEVELOPER",
    "DEFAULT_MAX_REQUEST_BYTES",
    "DEFAULT_MAX_ANSWER_ITEMS",
    "MINIMUM_ABSENCE_WINDOW",
    "MAX_REPLY_BYTES",
    "MAX_REPLY_DEPTH",
    "MAX_REPLY_ITEMS",
    "MAX_REPLY_RECEIPTS",
    "WITNESSED",
    "UNWITNESSED",
    "WITNESS_RECEIPTS_MEMBER",
    "WitnessRow",
    "OUTCOME_ARTIFACT",
    "OUTCOME_REFUSAL",
    "OUTCOME_ARTIFACT_FAILED",
    "OUTCOME_REFUSAL_NONCONFORMING",
    "RELATIONSHIP_STRANGER",
    "RELATIONSHIP_IDENTIFIED",
    "RELATIONSHIP_COUNTERPARTY",
    "REFUSAL_POLICY_SPECIFIC",
    "REFUSAL_POLICY_UNIFORM",
    "Book",
    "CapsuleSealer",
    "Opening",
    "OPENING_PREFIX",
    "RECORD_HEADER_MEMBER",
    "new_opening",
    "verify_opening",
    "SentRequest",
    "ResponderKeys",
    "SharePolicy",
    "RespondOptions",
    "ResponderPolicy",
    "RequestLimiter",
    "WindowLimiter",
    "DerivationDefinition",
]

DEFAULT_DEVELOPER = "evidencebook"
DEFAULT_MAX_REQUEST_BYTES = 64 << 10
DEFAULT_MAX_ANSWER_ITEMS = 10_000
#: The shortest waiting window an absence may claim, from when the request was recorded.
MINIMUM_ABSENCE_WINDOW = timedelta(minutes=1)

# Response outcomes as a requester records them. A received reply that did
# not verify is one of the two failed sub-states, never an absence.
OUTCOME_ARTIFACT = "artifact"
OUTCOME_REFUSAL = "refusal"
OUTCOME_ARTIFACT_FAILED = "artifact_verification_failed"
OUTCOME_REFUSAL_NONCONFORMING = "refusal_nonconformant"

RELATIONSHIP_STRANGER = "stranger"
RELATIONSHIP_IDENTIFIED = "identified"
RELATIONSHIP_COUNTERPARTY = "counterparty"

REFUSAL_POLICY_SPECIFIC = "specific"
REFUSAL_POLICY_UNIFORM = "uniform"

KIND_CLL_CHECKPOINT = "cll-checkpoint"
KIND_CLL_INCLUSION_PROOF = "cll-inclusion-proof"
KIND_CLL_CONSISTENCY_PROOF = "cll-consistency-proof"

# The record header members a projection may declare.
_HEADER_FIELDS = ("book_id", "committed_at", "correlation", "counterparty_ref", "epistemic_type", "event_time_claim",
                  "links", "payload_commitments", "principal_ref", "record_type", "retention_state", "seq",
                  "statement", "subject_ref", "v")


def _digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


# --- policies -------------------------------------------------------------------


#: The top-level Capsule member through which a Capsule its caller seals
#: (:meth:`Book.append_capsule`) carries its own record header: the header's
#: JSON, exactly as the book canonicalized it (Go ``RecordHeaderMember``). A
#: record the book seals itself commits its header as the Capsule's
#: ``agent_input_digest`` instead, and carries no such member.
RECORD_HEADER_MEMBER = "evidence_record_header"
#: Names a record's field openings among its originals: ``opening.<field>``
#: holds JCS({nonce, text}), whose SHA-256 is that field's commitment.
OPENING_PREFIX = "opening."

#: What :meth:`Book.append_capsule` calls under the book's lock: the record's
#: header with its position and commit time assigned, and its canonical JSON.
CapsuleSealer = Callable[[Header, bytes], Any]

_NONCE = re.compile(r"[0-9a-f]{64}\Z")


@dataclass(frozen=True)
class Opening:
    """What opens one field commitment: the nonce and the text (Go
    ``Opening``). It stays with the record's originals and leaves only when
    disclosed."""

    nonce: str
    text: str

    def canonical(self) -> bytes:
        """JCS({nonce, text}), the octets the commitment hashes."""
        if not isinstance(self.nonce, str) or not _NONCE.match(self.nonce):
            raise InvalidError("an opening's nonce is 64 lowercase hex characters")
        if not isinstance(self.text, str):
            raise InvalidError("an opening's text is a string")
        try:
            self.text.encode("utf-8")
        except UnicodeEncodeError:
            raise InvalidError("an opening's text is not valid UTF-8") from None
        return jcs({"nonce": self.nonce, "text": self.text})

    def commitment(self) -> str:
        return _digest(self.canonical())


def new_opening(text: str) -> Opening:
    """An opening of ``text`` under a fresh 32-byte nonce."""
    return Opening(os.urandom(32).hex(), text)


def verify_opening(commitment: str, opening: Opening) -> bool:
    """Whether ``opening`` opens ``commitment``. Never raises: a malformed
    opening opens nothing."""
    try:
        return isinstance(commitment, str) and bool(_NONCE.match(commitment)) and opening.commitment() == commitment
    except (InvalidError, AttributeError, TypeError):
        return False


def _field_originals(fields: dict[str, str]) -> tuple[list[Any], tuple[tuple[str, str], ...]]:
    """An entry's committed texts as their openings, held as originals, and
    the header's field_commitments in name order."""
    from capsule_emit.artifact import PRESENT, Artifact

    originals, commitments = [], []
    for name in sorted(fields or {}):
        if not isinstance(name, str) or not FIELD_NAME.match(name):
            raise InvalidError(f"field name {name!r} is not 1-56 of [a-z0-9_], starting with a letter or digit")
        content = new_opening(fields[name]).canonical()
        originals.append(Artifact(name=OPENING_PREFIX + name, state=PRESENT, content=content))
        commitments.append((name, _digest(content)))
    return originals, tuple(commitments)


def _header_commitment(record_id: str, capsule: Any, header: bytes) -> str:
    """Why ``capsule`` is not record ``record_id`` committing ``header``, or
    "" when it is: it verifies as that id and commits the header as its
    ``agent_input_digest`` (a record the book sealed) or carries it in
    :data:`RECORD_HEADER_MEMBER` (a record its caller sealed), never both
    (Go ``checkCapsuleCommitsHeader``)."""
    from capsule_emit.produce import verify_capsule

    if not isinstance(capsule, dict):
        return "the capsule is not a JSON object"
    try:
        # As Go's emit.VerifyCapsule: AAC format 4 under JCS, Class 1, and the id.
        if verify_capsule(jcs(capsule)).capsule_id != record_id or capsule.get("capsule_id") != record_id:
            return "it does not verify as that id"
    except Exception as exc:  # Class1Error, a malformed capsule: it is not this record
        return f"it does not verify as that id: {exc}"
    attestation = capsule.get("model_attestation")
    attestation = attestation.get("compute_attestation") if isinstance(attestation, dict) else None
    committed = isinstance(attestation, dict) and attestation.get("agent_input_digest") == _digest(header)
    inline = RECORD_HEADER_MEMBER in capsule
    if inline and committed:
        return "it commits its header twice"
    if inline:
        try:
            carried = jcs(capsule[RECORD_HEADER_MEMBER])
        except (TypeError, ValueError) as exc:
            return f"its {RECORD_HEADER_MEMBER}: {exc}"
        if carried != header:
            return "it carries a header other than its record's"
    elif not committed:
        return "it does not commit its header"
    return ""


class RequestLimiter(Protocol):
    """Decides whether a requester may be answered now; ``requester`` is the
    authenticated identity, or "" for every caller not authenticated."""

    def allow(self, requester: str, now: datetime) -> bool: ...


class WindowLimiter:
    """At most ``n`` requests per requester in any window of ``window``.
    Unauthenticated callers share the one "" bucket."""

    def __init__(self, n: int, window: timedelta):
        self._n, self._window = n, window
        self._seen: dict[str, list[datetime]] = {}
        self._lock = threading.Lock()

    def allow(self, requester: str, now: datetime) -> bool:
        with self._lock:
            recent = [t for t in self._seen.get(requester, ()) if t > now - self._window]
            if len(recent) >= self._n:
                self._seen[requester] = recent
                return False
            self._seen[requester] = recent + [now]
            return True


@dataclass(frozen=True)
class ResponderPolicy:
    """The responder's cost and abuse policy. The default answers the most
    specific token with no rate limit."""

    refusal_policy: str = REFUSAL_POLICY_SPECIFIC  # "" reads as specific, as in Go
    limiter: RequestLimiter | None = None
    max_request_bytes: int = DEFAULT_MAX_REQUEST_BYTES
    max_answer_items: int = DEFAULT_MAX_ANSWER_ITEMS

    def validate(self) -> None:
        if self.refusal_policy not in ("", REFUSAL_POLICY_SPECIFIC, REFUSAL_POLICY_UNIFORM):
            raise InvalidError(f"refusal policy {self.refusal_policy!r} is neither specific nor uniform")
        if self.max_request_bytes < 0 or self.max_answer_items < 0:
            raise InvalidError("responder limits must not be negative")


@dataclass(frozen=True)
class SharePolicy:
    """The four-switch sharing policy. ``history_segments`` decides who may
    have the records a record, exchange or correlation subject names:
    "counterparties" (only the party each names as its counterparty), "peers"
    (any authenticated peer) or "off"; "prospective" reads as
    "counterparties". A correlation subject is an exact-key lookup, so it is
    gated like an exchange. range and full_history are refused whatever the
    setting, except to the recipient of a recipient-specific derivation."""

    record_at_completion: str = "counterparty"
    history_segments: str = "counterparties"
    adjudications: str = "deliver_to_subjects"
    witness: str = ""

    def validate(self) -> None:
        for name, allowed in (("record_at_completion", ("counterparty", "off")),
                              ("history_segments", ("counterparties", "prospective", "peers", "off")),
                              ("adjudications", ("deliver_to_subjects", "off"))):
            if getattr(self, name) not in allowed:
                raise InvalidError(f"share.{name}={getattr(self, name)!r} is not one of {list(allowed)}")

    def may_have(self, requester: str, relationship: str, named: Sequence[Header]) -> bool:
        """Fails closed: a stranger is never allowed."""
        if not requester:
            return False
        if self.history_segments == "peers":
            return True
        if self.history_segments in ("counterparties", "prospective"):
            return relationship == RELATIONSHIP_COUNTERPARTY and all(h.counterparty_ref == requester for h in named)
        return False


@dataclass(frozen=True)
class RespondOptions:
    """``authenticated_requester`` is the identity the transport's own
    authentication established (a verified mTLS peer, a verified signature
    over the request), or "" when the caller is not authenticated. It is the
    only input to the relationship gate, and decides whether a subject is
    answered, never what is answered."""

    authenticated_requester: str = ""
    policy: SharePolicy = field(default_factory=SharePolicy)


@dataclass(frozen=True)
class DerivationDefinition:
    """A derivation served by the digest of its definition (ER §3.3).
    ``document`` is a JSON object; the derivation's identifier is SHA-256
    over its JCS. Only ``recipient`` is answered, when set, over every form
    in ``forms``; ``fields`` are the header members the projection reveals."""

    document: bytes
    forms: tuple[str, ...]
    fields: tuple[str, ...]
    recipient: str = ""


@dataclass(frozen=True)
class SentRequest:
    """A request as transmitted: its octets (JCS JSON), their digest, and the
    id of the record of having asked."""

    octets: bytes
    digest: str
    record_id: str

    @property
    def message(self) -> Message:
        return Message(self.octets, MEDIA_TYPE_JSON)


@dataclass(frozen=True)
class ResponderKeys:
    """The responder's keys as the requester obtained them, independently of
    any reply: ``signer`` signs refusals and envelopes, ``checkpoint`` the
    checkpoints an artifact is anchored to (lowercase hex). ``witnesses`` is
    the requester's witness directory: the witnesses whose receipts it
    counts on a reply's anchor."""

    signer: str
    checkpoint: str
    witnesses: tuple[WitnessRow, ...] = ()


@dataclass(frozen=True)
class _Sent:
    record: Record
    octets: bytes
    digest: str
    request: EvidenceRequest
    floor_time: int


# --- the book -----------------------------------------------------------------------


class Book:
    """An evidence book over a Checkpointed Local Log.

    ``signer`` signs record envelopes, envelopes and refusals; ``log_key``
    signs the log's checkpoints and may be another key. ``now`` is the clock
    every commit, checkpoint and answer reads.

    By default the log is in memory, rebuilt from ``store`` when the book
    opens. A durable ``substrate`` (a shelf's :class:`~evidencebook.shelf.SqliteSubstrate`)
    replaces ``log_id`` and ``log_key``: the open then checks every stored
    record against the log position it claims and appends a record stored
    but not yet in the log (a crash between the two writes) at its
    ``committed_at``, where it must land at its seq. ``originals`` is an
    artifact store (``capsule_emit.artifact``) that takes each record's
    payloads instead of the store, as Go's book keeps them. ``indexes`` are
    the book's authenticated indexes; ``None`` gives the defaults.
    """

    def __init__(
        self,
        book_id: str,
        operator: str,
        *,
        signer: Signer,
        log_id: str | None = None,
        log_key: Signer | None = None,
        store: Store | None = None,
        developer: str = DEFAULT_DEVELOPER,
        now: Callable[[], datetime] | None = None,
        responder: ResponderPolicy | None = None,
        substrate: CLLSubstrate | None = None,
        originals: Any = None,
        indexes: Sequence[Any] | None = None,
    ):
        if not book_id or not operator.strip() or not (developer or DEFAULT_DEVELOPER).strip():
            raise InvalidError("book id, operator and developer must not be empty")
        self._responder = responder or ResponderPolicy()
        self._responder.validate()
        self._id, self._operator, self._developer = book_id, operator, developer or DEFAULT_DEVELOPER
        self._signer = signer
        if substrate is None:
            if log_id is None or log_key is None:
                raise InvalidError("a book needs its log id and log key, or a substrate")
            substrate = CLLSubstrate(log_id, log_key)
        elif log_id is not None or log_key is not None:
            raise InvalidError("a book on a substrate takes no log id or log key")
        self._substrate = substrate
        self._store = store if store is not None else MemoryStore()
        self._originals = originals
        self._now = now or (lambda: datetime.now(timezone.utc))
        self._lock = threading.Lock()
        self._records: dict[str, Record] = {}
        self._stored: dict[str, StoredRecord] = {}
        self._order: list[str] = []
        self._indexes = default_indexes() if indexes is None else list(indexes)
        # A record stored whose log append failed: it keeps its seq and is
        # appended first, before anything new (Go's flushPendingLocked).
        self._pending: tuple[Record, StoredRecord] | None = None
        self._roots: dict[str, dict] = {}
        self._checkpoints: list[Checkpoint] = []
        self._derivations: dict[str, DerivationDefinition] = {}
        self._released = False
        self._recover()

    # -- opening ------------------------------------------------------------------

    def _recover(self) -> None:
        recovered: list[tuple[Record, StoredRecord]] = []
        for i, stored in enumerate(self._store.records()):
            if stored.seq != i + 1:
                raise CorruptError(f"stored records are not dense at seq {stored.seq}")
            try:
                header = Header.from_value(json.loads(stored.header))
            except (ValueError, TypeError, KeyError, AttributeError) as exc:
                raise CorruptError(f"header of {stored.record_id}: {exc}") from None
            if jcs(header.value()) != stored.header or header.seq != stored.seq:
                raise CorruptError(f"header of {stored.record_id} is not canonical or disagrees with the store")
            if header.book_id != self._id:
                raise CorruptError(f"record {stored.record_id} belongs to book {header.book_id!r}")
            detail = _header_commitment(stored.record_id, stored.capsule, stored.header)
            if detail:
                raise CorruptError(f"capsule for {stored.record_id} does not commit its header: {detail}")
            recovered.append((Record(stored.record_id, stored.seq, header), stored))
        # The log holds a prefix of the stored records; a record above it was
        # stored and not appended (a crash between the two writes), and is
        # appended at its committed_at, where it must land at its seq. An
        # in-memory log holds nothing yet, so every record is appended.
        committed = self._substrate.entry_ids()
        if len(committed) > len(recovered):
            raise CorruptError(f"the log holds {len(committed)} identities, the store {len(recovered)} records")
        for seq, record_id in enumerate(committed, start=1):
            if recovered[seq - 1][0].record_id != record_id:
                raise CorruptError(f"log seq {seq} is not stored record {recovered[seq - 1][0].record_id}")
        for record, _ in recovered[len(committed):]:
            self._commit(record)
        for record, stored in recovered:
            header = record.header
            self._admit(record, stored)
            if header.record_type == RECORD_TYPE_INDEX_ROOT:
                root = header.statement
                if not isinstance(root, dict) or not isinstance(root.get("index"), str):
                    raise CorruptError(f"index_root record {record.record_id} holds no index root")
                self._roots[root["index"]] = {k: root.get(k) for k in ("index", "root", "size")}
        for statement in self._store.checkpoints():
            try:
                cp = read_checkpoint(statement)
            except InvalidError as exc:
                raise CorruptError(f"stored checkpoint: {exc}") from None
            self._substrate.restore(cp)
            self._checkpoints.append(cp)

    def _commit(self, record: Record) -> None:
        """Append a stored record's id to the log at the time it says it was
        committed, requiring it to land at its seq."""
        at = parse_ns(record.header.committed_at)
        if at is None:
            raise CorruptError(f"committed_at of {record.record_id} is not an RFC 3339 time")
        seq = self._substrate.append(record.record_id, from_nanos(at))
        if seq != record.seq:
            raise CorruptError(f"record {record.record_id} landed at {seq}, not {record.seq}")

    def _flush_pending(self) -> None:
        if self._pending is None:
            return
        record, stored = self._pending
        try:
            self._commit(record)
        except Exception as exc:
            raise CorruptError(f"record {record.record_id} is stored but not yet committed: {exc}") from exc
        self._pending = None
        self._admit(record, stored)

    def _payloads(self, record_id: str) -> tuple[bytes, ...]:
        """A record's payloads: kept with it, or held by the originals."""
        payloads = self._stored[record_id].payloads
        if payloads or self._originals is None:
            return payloads
        from capsule_emit.artifact import NotFoundError as NoOriginals

        try:
            held = self._originals.get(record_id)
        except NoOriginals:
            return ()
        named = {a.name: a.content for a in held.artifacts if a.state == "present" and a.content is not None}
        out = []
        while f"payload-{len(out) + 1}" in named:
            out.append(named[f"payload-{len(out) + 1}"])
        return tuple(out)

    def _admit(self, record: Record, stored: StoredRecord) -> None:
        self._records[record.record_id] = record
        self._stored[record.record_id] = stored
        self._order.append(record.record_id)
        if record.header.record_type != RECORD_TYPE_INDEX_ROOT:
            # A root record is proven by the log, not by the index it commits.
            for index in self._indexes:
                index.add(record)

    # -- the basic surface --------------------------------------------------------------

    @property
    def book_id(self) -> str:
        return self._id

    @property
    def key_id(self) -> str:
        """The book signer's key (lowercase hex)."""
        return key_id(self._signer)

    @property
    def log_id(self) -> str:
        return self._substrate.log_id

    def size(self) -> int:
        with self._lock:
            return len(self._order)

    def records(self) -> list[Record]:
        with self._lock:
            return [_detached(self._records[i]) for i in self._order]

    def get(self, record_id: str) -> Record:
        with self._lock:
            record = self._records.get(record_id)
            if record is None:
                raise NotFoundError(f"record {record_id}")
            return _detached(record)

    def checkpoints(self) -> list[Checkpoint]:
        """Every checkpoint this book issued, oldest first."""
        with self._lock:
            return list(self._checkpoints)

    def close(self) -> None:
        """Release the store. Every later write raises :class:`ReleasedError`."""
        with self._lock:
            if not self._released:
                self._released = True
                try:
                    self._store.close()
                finally:
                    self._substrate.close()

    def append(self, entry: Entry) -> Record:
        """Seal ``entry`` as a record, store it with its payloads, and commit
        its id to the log."""
        with self._lock:
            return self._append(entry)

    def append_capsule(self, entry: Entry, originals: Sequence[Any], seal: CapsuleSealer) -> Record:
        """Commit a Capsule the caller seals itself as the record (Go
        ``Book.AppendCapsule``): the Capsule is the record and its id the log
        entry, with no record of the book's own around it.

        Under the book's lock the book takes the next position, builds the
        header (``payload_commitments`` the SHA-256 of each present original
        that is not a field opening, in order; ``field_commitments`` from
        ``entry.fields``) and calls ``seal(header, header_json)``. The Capsule
        it returns (an object with ``capsule_id``, ``payload``, the Capsule's
        JCS octets, and ``envelope``, as ``capsule_emit.produce.seal``
        returns) must carry ``header_json`` as its
        :data:`RECORD_HEADER_MEMBER` member, must not also commit it as its
        ``agent_input_digest``, and must be signed by the book's signer, or
        nothing is written. The Capsule, its envelope and ``originals``
        (``capsule_emit.artifact.Artifact``) then go to the artifact store in
        one put, the record to the store, and its id to the log. ``seal``
        must not call back into the book.

        ``entry.payloads`` must be empty: a caller-sealed record's originals
        are named, and may be bound to the Capsule's own digests, so they are
        passed as ``originals``."""
        if seal is None:
            raise InvalidError("a sealer is required")
        if entry.payloads:
            raise InvalidError("a caller-sealed record's payloads are passed as originals")
        with self._lock:
            return self._append(entry, list(originals), seal)

    def opening(self, record_id: str, name: str) -> Opening:
        """The opening of a record's field commitment ``name``, read from the
        artifact store and checked against the commitment its header carries
        (Go ``Book.Opening``). :class:`UnavailableError` when the record is
        withheld or deleted."""
        from capsule_emit.artifact import NotFoundError as NoOriginals
        from capsule_emit.artifact import PRESENT

        with self._lock:
            record = self._records.get(record_id)
            commitment = dict(record.header.field_commitments).get(name) if record is not None else None
            if commitment is None:
                raise NotFoundError(f"record {record_id} commits no field {name!r}")
            # Go's retention fold (shelf.py holds the port): a withheld or
            # deleted record's openings are not given out.
            from .shelf import _retention

            stated = ["statement" in json.loads(self._stored[i].header) for i in self._order]
            now = utc(self._now())
            state = _retention([self._records[i].header for i in self._order], list(self._order), stated,
                               record.seq - 1, nanos(now))
            if state in ("WITHHELD", "DELETED"):
                raise UnavailableError(f"record {record_id} is {state}")
            if self._originals is None:
                raise CorruptError(f"record {record_id} commits field {name!r} but the book has no artifact store")
            try:
                held = self._originals.get(record_id)
            except NoOriginals:
                raise CorruptError(f"the artifact store holds none of record {record_id}'s originals") from None
            for original in held.artifacts:
                if original.name != OPENING_PREFIX + name:
                    continue
                if original.state != PRESENT or original.content is None:
                    raise CorruptError(f"record {record_id} holds the opening of its field {name!r} purged")
                try:
                    value = json.loads(bytes(original.content))
                    found = Opening(value["nonce"], value["text"])
                except (ValueError, TypeError, KeyError) as exc:
                    raise CorruptError(f"opening {original.name} of {record_id}: {exc}") from None
                if not verify_opening(commitment, found):
                    raise CorruptError(f"opening {original.name} of {record_id} does not open its field commitment")
                return found
            raise CorruptError(f"the artifact store holds no opening of field {name!r} of record {record_id}")

    def _append(self, entry: Entry, originals: list[Any] | None = None, caller_seal: CapsuleSealer | None = None) -> Record:
        from capsule_emit.artifact import NEVER_RETAINED, PRESENT, Artifact, Record as Held

        if self._released:
            raise ReleasedError("the book is released")
        self._flush_pending()
        for original in originals or ():
            if original.name.startswith(OPENING_PREFIX):
                raise InvalidError(f"original {original.name!r} uses the name of a field opening")
        if caller_seal is None:
            originals = [Artifact(name=f"payload-{i}", state=PRESENT, content=bytes(p))
                         for i, p in enumerate(entry.payloads, start=1)]
        openings, field_commitments = _field_originals(entry.fields)
        if (openings or caller_seal is not None) and self._originals is None:
            raise InvalidError("field openings and caller-sealed records need the book's artifact store")
        originals = list(originals) + openings
        names: set[str] = set()
        for original in originals:
            present = original.state == PRESENT and original.content is not None
            if not (present or (original.state == NEVER_RETAINED and original.content is None)):
                raise InvalidError(f"original {original.name!r} must be present with content, or never retained without")
            if original.name in names:
                raise InvalidError(f"original {original.name!r} is given more than once")
            names.add(original.name)
        seq = len(self._order) + 1
        committed_at = utc(self._now())
        payload_commitments = tuple(_digest(bytes(o.content)) for o in originals
                                    if o.state == PRESENT and not o.name.startswith(OPENING_PREFIX))
        header = Header(
            book_id=self._id, seq=seq, record_type=entry.record_type, epistemic_type=entry.epistemic_type,
            committed_at=rfc3339_nano(committed_at), links=tuple(entry.links),
            event_time_claim=rfc3339_nano(entry.event_time_claim) if entry.event_time_claim else "",
            payload_commitments=payload_commitments,
            retention_state=AVAILABLE if payload_commitments or field_commitments else "",
            subject_ref=entry.subject_ref, principal_ref=entry.principal_ref, counterparty_ref=entry.counterparty_ref,
            correlation=None if entry.correlation.empty() else entry.correlation, statement=copy.deepcopy(entry.statement),
            field_commitments=field_commitments, commit_alg=COMMIT_ALG_NONCE256 if field_commitments else "",
        )
        header.validate()
        try:
            header_bytes = jcs(header.value())
        except (TypeError, ValueError) as exc:
            raise InvalidError(f"header: {exc}") from None
        if caller_seal is None:
            record_id, capsule, envelope = seal_record(header, header_bytes, operator=self._operator,
                                                       developer=self._developer, at=committed_at, signer=self._signer)
            capsule_bytes = jcs(capsule)
        else:
            record_id, capsule, capsule_bytes, envelope = self._check_caller_sealed(caller_seal, header, header_bytes)
        if originals and self._originals is not None:
            # Originals are written after sealing, with the record they belong
            # to, in one put: the artifact store never holds bytes no record
            # commits.
            self._originals.put(Held(record_id, capsule_bytes, envelope, originals))
        elif caller_seal is not None:
            self._originals.put(Held(record_id, capsule_bytes, envelope, []))
        payloads = tuple(bytes(p) for p in entry.payloads)
        stored = StoredRecord(seq, record_id, header_bytes, capsule, envelope, payloads)
        self._store.put_record(stored)
        record = Record(record_id, seq, header)
        try:
            self._commit(record)
        except Exception as exc:
            self._pending = (record, stored)
            raise CorruptError(f"record {record_id} is stored but not yet committed; it keeps seq {seq} "
                               f"and is retried first: {exc}") from exc
        self._admit(record, stored)
        return _detached(record)

    def _check_caller_sealed(self, seal: CapsuleSealer, header: Header,
                             header_bytes: bytes) -> tuple[str, dict, bytes, bytes]:
        """Run a caller's sealer and accept its Capsule only as a record of
        this book: it verifies, carries exactly this header, and its envelope
        is signed by the book's signer (Go ``checkCallerSealed``)."""
        from capsule_emit.produce import verify_envelope

        # A sealer's error propagates as raised, as Go's "seal record: %w"
        # keeps its identity: a signer's I/O failure is not bad input.
        sealed = seal(copy.deepcopy(header), bytes(header_bytes))
        try:
            capsule_id, payload, envelope = sealed.capsule_id, bytes(sealed.payload), bytes(sealed.envelope)
        except (AttributeError, TypeError) as exc:
            raise InvalidError(f"the sealer returned no sealed capsule: {exc}") from None
        try:
            capsule = strict_json(payload)
        except Exception as exc:
            raise InvalidError(f"the sealed capsule is not this record: {exc}") from None
        if not isinstance(capsule, dict) or jcs(capsule) != payload:
            raise InvalidError("the sealed capsule is not this record: its octets are not a JCS object")
        detail = _header_commitment(capsule_id, capsule, header_bytes)
        if detail:
            raise InvalidError(f"the sealed capsule is not this record: {detail}")
        if RECORD_HEADER_MEMBER not in capsule:
            raise InvalidError(f"the sealed capsule does not carry its record header in {RECORD_HEADER_MEMBER}")
        try:
            verified = verify_envelope(capsule_id, envelope)
        except Exception as exc:
            raise InvalidError(f"the sealed capsule's envelope: {exc}") from None
        if bytes(verified.public_key or b"") != bytes(self._signer.public_key()):
            raise InvalidError("the sealed capsule is not signed by the book's signer")
        return capsule_id, capsule, payload, envelope

    def checkpoint(self, at: datetime | None = None) -> Checkpoint:
        """Commit any changed index roots as records, then checkpoint the log
        at ``at``, or at the book's clock."""
        with self._lock:
            return self._checkpoint(at)

    def _checkpoint(self, at: datetime | None = None) -> Checkpoint:
        if self._released:
            raise ReleasedError("the book is released")
        self._flush_pending()
        for index in self._indexes:
            root = index.root()
            if self._roots.get(root["index"]) == root:
                continue
            self._append(Entry(RECORD_TYPE_INDEX_ROOT, DERIVED_METRIC, statement=dict(root, as_of_seq=len(self._order))))
            self._roots[root["index"]] = root
        when = utc(self._now()) if at is None else utc(at)
        cp = self._substrate.checkpoint(when)
        if not any(held.id == cp.id for held in self._checkpoints):
            self._store.put_checkpoint(cp.statement)
            self._checkpoints.append(cp)
        return cp

    # -- the requester ----------------------------------------------------------------

    def request(self, request: EvidenceRequest, responder: str = "") -> SentRequest:
        """Record that this book is asking ``responder`` and return the octets
        to transmit: the request under this profile in JCS. An empty nonce is
        filled with a fresh random one; a nonce this book already used is
        refused. Recording happens before sending, so "I asked" is committed
        whatever comes back."""
        request = replace(request, profile=PROFILE, nonce=request.nonce or os.urandom(16).hex())
        octets = encode_request(request, MEDIA_TYPE_JSON)
        return self._record_request(octets, json.loads(octets), request.nonce, responder)

    def request_sent(self, request: Message, responder: str = "") -> SentRequest:
        """Record a request that another tool composed and sent to
        ``responder``, exactly as given: every member is kept, including
        those the request draft does not define (a deployment profile's
        ``audience`` or requester authentication, ER §3), and no nonce or
        profile is added. The octets must be a well-formed request (profile
        §6.1 steps 1 to 5) in JCS, the form a requester transmits JSON in
        (P-9), so the recorded digest is the one the responder signs its
        reply over. A nonce this book already used is refused; a request
        without one has nothing to compare. :meth:`record_response` and
        :meth:`record_absence` then work on the record as on one
        :meth:`request` made."""
        if request.media_type != MEDIA_TYPE_JSON:
            raise InvalidError(f"a sent request is recorded in JSON, not {request.media_type!r}")
        check = read_request(request)
        if check.reason:
            raise InvalidError(f"not a well-formed request ({check.reason}): {check.why}")
        # The record's statement embeds the octets as JSON, so octets in any
        # other form than JCS would not survive as sent.
        try:
            value = strict_json(request.octets)
        except ValueError as err:
            raise InvalidError(str(err)) from None
        if jcs(value) != request.octets:
            raise InvalidError("a sent request must be in JCS (RFC 8785), as it was transmitted")
        octets = bytes(request.octets)
        return self._record_request(octets, value, check.request.nonce, responder, composed_elsewhere=True)

    def _record_request(self, octets: bytes, value: dict, nonce: str | None, responder: str,
                        composed_elsewhere: bool = False) -> SentRequest:
        statement: dict[str, Any] = {"request": value}
        if responder:
            statement["responder_ref"] = responder
        if composed_elsewhere:
            # Another tool composed the request and the book checked only that it is
            # well formed, so the record does not vouch for members it keeps unchecked.
            statement["composed_elsewhere"] = True
        with self._lock:
            for record_id in self._order:
                if not nonce or self._records[record_id].header.record_type != RECORD_TYPE_REQUEST:
                    continue
                try:
                    prior = self._sent(record_id)
                except CorruptError:
                    continue
                if prior.request.nonce == nonce:
                    raise InvalidError(f"nonce already used by request {record_id}")
            record = self._append(Entry(RECORD_TYPE_REQUEST, OBSERVED_EVENT, statement=statement))
        return SentRequest(octets, _digest(octets), record.record_id)

    def _sent(self, request_record_id: str) -> _Sent:
        record = self._records.get(request_record_id)
        if record is None or record.header.record_type != RECORD_TYPE_REQUEST:
            raise NotFoundError(f"{request_record_id} is not a request this book recorded")
        statement = record.header.statement
        if not isinstance(statement, dict) or "request" not in statement:
            raise CorruptError(f"request record {request_record_id} holds no request")
        octets = jcs(statement["request"])
        check = read_request(Message(octets, MEDIA_TYPE_JSON))
        if check.reason:
            raise CorruptError(f"request record {request_record_id} holds no well-formed request: {check.reason}")
        floor = check.request.coverage.min_freshness
        floor_time = parse_ns(floor) if isinstance(floor, str) else 0
        return _Sent(record, octets, _digest(octets), check.request, floor_time or 0)

    def _outcome_recorded(self, request_record_id: str) -> bool:
        return any(
            self._records[i].header.record_type in (RECORD_TYPE_RESPONSE, RECORD_TYPE_ABSENCE)
            and request_record_id in self._records[i].header.links_to(CITES)
            for i in self._order
        )

    def record_response(self, request_record_id: str, reply: Message, keys: ResponderKeys) -> Record:
        """Verify and record the reply that came back for the request
        recorded as ``request_record_id``, keeping its octets as the
        record's payload. A reply not signed under the pinned signer key for
        this request's digest is refused and not recorded; a genuinely signed
        reply that fails a check of profile §7.2 is recorded as received and
        failed, never as a grant and never as an absence. The requester's
        limits (:data:`MAX_REPLY_BYTES` and the rest) apply first: over one
        on the reply's unauthenticated octets, the reply is refused and the
        request stays open; over one on what the responder signed, the
        answer is received and failed, naming the limit."""
        if not keys.signer or not keys.checkpoint:
            raise InvalidError("pinned responder signer and checkpoint keys are required")
        check_reply_octets(reply.octets, reply.media_type == MEDIA_TYPE_JSON)
        parsed = parse_reply(reply)
        # The carrier's size is refused on any reply, a refusal too.
        carried = carried_receipts(_decoded(reply), parsed.envelope.anchor_digest if parsed.envelope else "")
        with self._lock:
            sent = self._sent(request_record_id)
            statement = _verify_reply(sent, parsed, keys, carried)
            if self._outcome_recorded(request_record_id):
                raise InvalidError(f"an outcome is already recorded for request {sent.digest}")
            return self._append(Entry(RECORD_TYPE_RESPONSE, OBSERVED_EVENT, links=(Link(CITES, request_record_id),),
                                      statement=statement, payloads=(reply.octets,)))

    def record_absence(self, request_record_id: str, window_end: datetime, route: str = "") -> Record:
        """Commit that no reply arrived for the recorded request between its
        recording and ``window_end`` (ER §4.4, profile §6.4). Refused while
        the request is pending (the window not closed, shorter than
        :data:`MINIMUM_ABSENCE_WINDOW`, or closing before the request's own
        deadline) and once any outcome is recorded. A route the request named
        wins; another route is refused."""
        window_end = utc(window_end)
        with self._lock:
            sent = self._sent(request_record_id)
            now = utc(self._now())
            if window_end > now:
                raise InvalidError("the waiting window has not closed; the request is pending")
            asked = parse_ns(sent.record.header.committed_at)
            if asked is None:
                raise CorruptError(f"committed_at of request {request_record_id}")
            if nanos(window_end) < asked + int(MINIMUM_ABSENCE_WINDOW.total_seconds()) * 1_000_000_000:
                raise InvalidError(f"a waiting window must end at least {MINIMUM_ABSENCE_WINDOW} after the request")
            if sent.request.deadline is not None:
                deadline = parse_ns(sent.request.deadline)
                if deadline is None:
                    raise CorruptError(f"committed deadline {sent.request.deadline!r}")
                if nanos(window_end) < deadline:
                    raise InvalidError("the window closes before the request's own deadline")
            if sent.request.route:
                if route and route != sent.request.route:
                    raise InvalidError(f"the request named route {sent.request.route!r}, not {route!r}")
                route = sent.request.route
            if self._outcome_recorded(request_record_id):
                raise InvalidError(f"an outcome is already recorded for request {sent.digest}")
            absence: dict[str, Any] = {
                "outcome": RECORD_TYPE_ABSENCE, "request_digest": sent.digest,
                "window": {"opened": profile_time(from_nanos(asked)), "closed": profile_time(window_end)},
                "recorded_at": profile_time(now),
            }
            if route:
                absence["route"] = route
            return self._append(Entry(RECORD_TYPE_ABSENCE, OBSERVED_EVENT, links=(Link(CITES, request_record_id),),
                                      statement=absence))

    def reply_octets(self, response_record_id: str) -> bytes:
        """The reply a response record observed, as received."""
        with self._lock:
            record = self._records.get(response_record_id)
            if record is None or record.header.record_type != RECORD_TYPE_RESPONSE:
                raise NotFoundError(f"{response_record_id} is not a response this book recorded")
            payloads = self._payloads(response_record_id)
            if not payloads:
                raise NotFoundError(f"response {response_record_id} was recorded without its reply octets")
            return payloads[0]

    # -- the responder ------------------------------------------------------------------

    def register_derivation(self, definition: DerivationDefinition) -> str:
        """Make a derivation answerable by its identifier, which it returns.
        Registrations are held in memory, never written into the history.
        Registering the same document again with the same recipient, forms
        and fields returns the same identifier; with anything else it is
        :class:`InvalidError`."""
        try:
            document = strict_json(definition.document)
        except ValueError as exc:
            raise InvalidError(f"derivation definition: {exc}") from None
        if not isinstance(document, dict):
            raise InvalidError("a derivation definition is a JSON object")
        if not definition.forms or not definition.fields:
            raise InvalidError("a derivation declares its subject forms and its field set")
        for form in definition.forms:
            if form not in SUBJECT_FORMS or form == "checkpoints":
                raise InvalidError(f"a derivation over records cannot be defined over {form!r}")
        for name in definition.fields:
            if name not in _HEADER_FIELDS:
                raise InvalidError(f"{name!r} is not a record header member")
        identifier = _digest(jcs(document))
        with self._lock:
            prev = self._derivations.get(identifier)
            if prev is not None:
                if (prev.recipient == definition.recipient and set(prev.forms) == set(definition.forms)
                        and set(prev.fields) == set(definition.fields)):
                    return identifier
                raise InvalidError(f"derivation {identifier} is registered with a different recipient, forms or fields")
            self._derivations[identifier] = definition
        return identifier

    def commit_retention(self, subject: Subject, until: datetime) -> Record:
        """Seal a retention commitment: ``subject`` (a record, an exchange or
        a range) stays answerable until ``until``. After it, a request the
        book no longer answers is refused retention_expired."""
        if subject.form not in ("record", "exchange", "range"):
            raise InvalidError("a retention commitment names a record, an exchange or a range")
        try:
            parse_request(jcs({"subject": subject.to_wire(), "coverage": {"expected_pin": "0" * 64}}))
        except RequestError:
            raise InvalidError("the subject's content does not conform") from None
        statement = {"subject": subject.to_wire(), "until": profile_time(until)}
        with self._lock:
            return self._append(Entry(RECORD_TYPE_RETENTION_COMMITMENT, PRODUCER_CLAIM, statement=statement))

    def respond(self, request: Message, options: RespondOptions | None = None) -> tuple[Reply, Record | None]:
        """Answer one request with a signed envelope carrying the canonical
        Artifact, or a signed refusal with exactly one registered reason,
        encoded in the request's encoding. The artifact is a function of
        (subject, resolved anchor, derivation) only.

        For an authenticated requester whose request was well formed this
        seals an ``evidence_request_answered`` record and returns it;
        otherwise the record is ``None``. Malformed and unauthenticated
        traffic never grows the log."""
        options = options or RespondOptions()
        options.policy.validate()
        if request.media_type not in (MEDIA_TYPE_JSON, MEDIA_TYPE_CBOR):
            raise InvalidError(f"a request is carried as {MEDIA_TYPE_JSON} or {MEDIA_TYPE_CBOR}, not {request.media_type!r}")
        with self._lock:
            answer = _Answer(self, request, options)
            reply = answer.run()
            if not answer.seal:
                return reply, None
            record = self._append(Entry(RECORD_TYPE_REQUEST_ANSWERED, OBSERVED_EVENT, statement=answer.statement))
            return reply, record

    def _relationship(self, authenticated: str) -> str:
        if not authenticated:
            return RELATIONSHIP_STRANGER
        if any(self._records[i].header.counterparty_ref == authenticated for i in self._order):
            return RELATIONSHIP_COUNTERPARTY
        return RELATIONSHIP_IDENTIFIED


def _detached(record: Record) -> Record:
    """``record`` with its own copy of the statement, so a caller holding it
    cannot change the book's record: the book reads its own statements back
    (a request's nonce and coverage, a retention commitment's subject)."""
    if record.header.statement is None:
        return record
    return replace(record, header=replace(record.header, statement=copy.deepcopy(record.header.statement)))


# --- answering ---------------------------------------------------------------------


class _View:
    """The book as the decision reads it, under the book's lock."""

    def __init__(self, book: Book):
        self.book = book
        self.checkpoints = [cp for cp in book._checkpoints if cp.kind == "cll"]
        self._anchors = [_decide.AnchorView(cp.anchor_digest, cp.entries, cp.issued_at_ns) for cp in self.checkpoints]
        self._leaves = {record_id: i for i, record_id in enumerate(book._order)}
        self._commitments = []
        for record_id in book._order:
            h = book._records[record_id].header
            if h.record_type == RECORD_TYPE_RETENTION_COMMITMENT and isinstance(h.statement, dict):
                until = h.statement.get("until")
                if is_profile_time(until) and isinstance(h.statement.get("subject"), dict):
                    self._commitments.append((h.statement["subject"], parse_ns(until)))

    def _header(self, leaf: int) -> Header:
        return self.book._records[self.book._order[leaf]].header

    def anchors(self) -> list[_decide.AnchorView]:
        return self._anchors

    def log_entries(self) -> int:
        return len(self.book._order)

    def servable(self, leaf: int) -> bool:
        # The responder's own bookkeeping of who asked and what it disclosed
        # is never served.
        return 0 <= leaf < len(self.book._order) and \
            self._header(leaf).record_type not in (RECORD_TYPE_REQUEST_ANSWERED, RECORD_TYPE_DISCLOSURE)

    def leaf_of(self, digest: str) -> int | None:
        leaf = self._leaves.get(digest)
        return leaf if leaf is not None and self.servable(leaf) else None

    def _matching(self, match: Callable[[Header], bool]) -> list[int]:
        return [i for i in range(len(self.book._order)) if match(self._header(i)) and self.servable(i)]

    def correlated(self, correlation_id: str) -> list[int]:
        # A record carrying correlation.exchange_id is held under it alone;
        # only one carrying no exchange_id is held under its subject_ref.
        def held_under(h: Header) -> str:
            if h.correlation is not None and h.correlation.exchange_id:
                return h.correlation.exchange_id
            return h.subject_ref

        return self._matching(lambda h: held_under(h) == correlation_id)

    def citing(self, half: str) -> list[int]:
        return self._matching(lambda h: half in h.links_to(CITES))

    def lapsed(self, subject_value: dict, now: int) -> bool:
        return any(subject == subject_value and now > until for subject, until in self._commitments)

    def named(self, subject: Subject) -> list[Header]:
        if subject.form == "record":
            leaf = self.leaf_of(subject.content)
            leaves = [] if leaf is None else [leaf]
        elif subject.form == "exchange":
            leaves = self.citing(subject.content)
        elif subject.form == "correlation":
            leaves = self.correlated(subject.content)
        else:
            leaves = []
        return [self._header(leaf) for leaf in leaves]


@dataclass(frozen=True)
class _Declared:
    """The field set an artifact form may carry; the disclosure scan refuses
    any member outside it."""

    top: tuple[str, ...]
    entry: tuple[str, ...] | None
    list: str


_RECORD_SET_FIELDS = _Declared(("anchor", "evidence_stream", "records", "subject"), ("body", "digest", "leaf_index"), "records")
_CHECKPOINTS_FIELDS = _Declared(("anchor", "checkpoints", "evidence_stream", "subject"), None, "checkpoints")
_HISTORY_FIELDS = _Declared(("anchor", "derivation", "evidence_stream", "history", "subject"),
                            ("checkpoint", "consistency_from_previous"), "history")


class _OverLimit:
    """An answer refused for the cost policy's item cap (see _Answer._build)."""


_OVER_LIMIT = _OverLimit()


def _limit_reason(profile: str) -> str:
    """The reason a request asking for ``profile`` is refused for a limit of
    the book's own: its size, the rate limiter or the answer's item cap.
    ``evidence-interop/1`` (ER -02 §4.2, §11) refuses load shedding
    ``capacity_exceeded`` whatever the refusal policy, so ``policy_declined``
    stays a statement about policy; ``/0`` registers no such token and keeps
    ``policy_declined``, as does a profile the book does not implement."""
    return REASON_CAPACITY_EXCEEDED if profile == PROFILE_V1 else REASON_POLICY_DECLINED


class _Answer:
    def __init__(self, book: Book, request: Message, options: RespondOptions):
        self.book = book
        self.request = request
        self.options = options
        self.now = utc(book._now())
        self.digest = _digest(request.octets)
        self.statement: dict[str, Any] = {}
        self.seal = False

    def refuse(self, reason: str, detail: str = "") -> Reply:
        signer = EvidenceSigner(self.book._signer)
        refusal = sign_refusal(self.digest, reason, signer=signer, issued_at=profile_time(self.now))
        self.statement["outcome"], self.statement["reason"] = OUTCOME_REFUSAL, reason
        return encode_refusal(refusal, self.request.media_type, detail)

    def run(self) -> Reply:
        book, policy = self.book, self.book._responder
        requester = self.options.authenticated_requester
        # Cost policy first: an oversized or over-limit request is declined
        # before it is resolved; it is read only to pick the reason
        # (request_profile).
        if len(self.request.octets) > (policy.max_request_bytes or DEFAULT_MAX_REQUEST_BYTES):
            return self.refuse(_limit_reason(request_profile(self.request)))
        if policy.limiter is not None and not policy.limiter.allow(requester, self.now):
            return self.refuse(_limit_reason(request_profile(self.request)))
        # The book answers evidence-interop/0 and /1, and an unknown
        # profile's detail lists both (§3.3 step 4).
        check = read_request(self.request)
        if check.reason:
            return self.refuse(check.reason, check.detail)
        req = check.request
        relationship = book._relationship(requester)
        self.statement = {"request_digest": self.digest, "subject_kind": req.subject.form, "relationship": relationship}
        self.seal = relationship != RELATIONSHIP_STRANGER
        view = _View(book)
        d = self._decide(view, req, may_issue=self.seal)
        if d.need_anchor:
            book._checkpoint()
            view = _View(book)
            d = self._decide(view, req, may_issue=False)
            if d.need_anchor:
                d = replace(d, reason=self._policy(view, req, False).disclose(REASON_COVERAGE_UNSATISFIABLE))
        if d.reason:
            return self.refuse(d.reason)
        anchor = view.checkpoints[d.anchor]
        built = self._build(view, req, anchor, d.leaves)
        if built is _OVER_LIMIT:
            return self.refuse(_limit_reason(req.profile))
        if built is None:
            return self.refuse(REASON_POLICY_DECLINED)
        artifact, material = built
        if req.deadline is not None and nanos(utc(book._now())) > parse_ns(req.deadline):
            return self.refuse(REASON_DEADLINE_UNMET)
        freshness: int | str | None = None
        floor = req.coverage.min_freshness
        if floor is not None:
            freshness = anchor.tree_size if isinstance(floor, int) else profile_time_ns(anchor.issued_at_ns)
        envelope = sign_envelope(
            self.request.octets, req.subject, req.coverage.expected_pin if d.half_pin else anchor.anchor_digest,
            artifact, signer=EvidenceSigner(book._signer), issued_at=profile_time(self.now),
            derivation=req.derivation, freshness=freshness, verification=material, profile=req.profile,
        )
        self.statement.update(outcome=OUTCOME_ARTIFACT, anchor=envelope.anchor_digest,
                              artifact_digest=_digest(envelope.artifact))
        return encode_envelope(envelope, self.request.media_type)

    def _policy(self, view: _View, req: EvidenceRequest, may_issue: bool) -> _decide.Policy:
        book = self.book
        requester = self.options.authenticated_requester
        relationship = self.statement.get("relationship", RELATIONSHIP_STRANGER)
        derivations = book._derivations

        def derivation(name: str):
            entry = derivations.get(name)
            return None if entry is None else entry.forms

        def authorize(r: EvidenceRequest) -> bool:
            if r.subject.form == "checkpoints" and r.derivation is None or r.derivation == DERIVATION_HISTORY_CARD:
                return True  # public: commitments only
            entry = derivations.get(r.derivation) if r.derivation is not None else None
            if entry is not None and entry.recipient:
                return requester == entry.recipient  # the holder's grant to the one requester it names
            if r.subject.form in ("record", "exchange", "correlation"):
                # A correlation matches its key exactly, so it names a deal's
                # records as an exchange does (Steven's Q1 amendment on #114).
                return self.options.policy.may_have(requester, relationship, view.named(r.subject))
            # range and full_history reach records no one requester is
            # party to; only a recipient-specific derivation grants them.
            return False

        return _decide.Policy(derivation=derivation, authorize=authorize,
                              uniform=book._responder.refusal_policy == REFUSAL_POLICY_UNIFORM, may_issue=may_issue)

    def _decide(self, view: _View, req: EvidenceRequest, may_issue: bool) -> _decide.Resolution:
        return _decide.decide(view, req, self._policy(view, req, may_issue), nanos(self.now))

    # -- the canonical Artifact ---------------------------------------------------------

    def _build(self, view: _View, req: EvidenceRequest, anchor: Checkpoint,
               leaves: Sequence[int]) -> tuple[bytes, list[tuple[str, bytes]]] | _OverLimit | None:
        """The canonical Artifact and the material proving it, ``_OVER_LIMIT``
        when the answer has more items than the cost policy allows, or
        ``None`` when it fails the disclosure scan."""
        book = self.book
        max_items = book._responder.max_answer_items or DEFAULT_MAX_ANSWER_ITEMS
        if len(leaves) > max_items:
            return _OVER_LIMIT
        head: dict[str, Any] = {"anchor": anchor.anchor_digest, "evidence_stream": anchor.log_id,
                                "subject": req.subject.to_wire()}
        definition = book._derivations.get(req.derivation) if req.derivation is not None else None
        if req.derivation == DERIVATION_HISTORY_CARD or req.subject.form == "checkpoints":
            chain = _checkpoints_through(view.checkpoints, anchor)
            if len(chain) > max_items:
                return _OVER_LIMIT
            if req.derivation == DERIVATION_HISTORY_CARD:
                head["derivation"], head["history"], declared = DERIVATION_HISTORY_CARD, \
                    self._history_card(chain, anchor), _HISTORY_FIELDS
            else:
                head["checkpoints"], declared = [cp.statement.hex() for cp in chain], _CHECKPOINTS_FIELDS
        elif definition is not None:
            head["derivation"], head["records"] = req.derivation, self._project(leaves, definition)
            declared = _Declared(("anchor", "derivation", "evidence_stream", "records", "subject"),
                                 ("digest", "leaf_index", *definition.fields), "records")
        elif req.subject.form == "record":
            record_id = book._order[leaves[0]]
            body = capsule_preimage(book._stored[record_id].capsule, record_id)
            if not self._clean(body, None):
                return None
            return body, self._cll_material(leaves[0] + 1, anchor)
        else:
            records = []
            for leaf in leaves:
                record_id = book._order[leaf]
                body = capsule_preimage(book._stored[record_id].capsule, record_id)
                records.append({"leaf_index": leaf, "digest": record_id, "body": body.hex()})
            head["records"], declared = records, _RECORD_SET_FIELDS
        artifact = jcs(head)
        if not self._clean(artifact, declared):
            return None
        return artifact, []

    def _cll_material(self, seq: int, anchor: Checkpoint) -> list[tuple[str, bytes]]:
        """The §6.2.1 material for the record at ``seq``: the anchor, the
        record's inclusion proof, and, when the anchor extends an earlier
        checkpoint, the proof that it does."""
        substrate = self.book._substrate
        material = [(KIND_CLL_CHECKPOINT, anchor.statement),
                    (KIND_CLL_INCLUSION_PROOF, substrate.inclusion_proof(seq, anchor))]
        if anchor.prev_size > 0:
            material.append((KIND_CLL_CONSISTENCY_PROOF,
                             substrate.consistency_proof(anchor.prev_size, anchor.tree_size, anchor)))
        return material

    def _history_card(self, chain: list[Checkpoint], anchor: Checkpoint) -> list[dict]:
        """history_card/1 (ER §8.1): each checkpoint from the first to the
        anchor, with the consistency proof from the previous one."""
        history = []
        for i, cp in enumerate(chain):
            consistency = None
            if i > 0 and chain[i - 1].tree_size < cp.tree_size:
                consistency = self.book._substrate.consistency_proof(chain[i - 1].tree_size, cp.tree_size, anchor).hex()
            history.append({"checkpoint": cp.statement.hex(), "consistency_from_previous": consistency})
        return history

    def _project(self, leaves: Sequence[int], definition: DerivationDefinition) -> list[dict]:
        records = []
        for leaf in leaves:
            record_id = self.book._order[leaf]
            fields = json.loads(self.book._stored[record_id].header)
            entry: dict[str, Any] = {"digest": record_id, "leaf_index": leaf}
            for name in definition.fields:
                if name in fields:
                    entry[name] = fields[name]
            records.append(entry)
        return records

    def _clean(self, artifact: bytes, declared: _Declared | None) -> bool:
        """The disclosure scan run on every artifact before release: only
        declared members, no principal or counterparty reference this book
        holds in any record body, and no bookkeeping record as an entry."""
        book = self.book
        identities = set()
        for record_id in book._order:
            h = book._records[record_id].header
            identities.update(ref for ref in (h.principal_ref, h.counterparty_ref) if ref)
        if declared is None:
            return not _mentions_any(artifact, identities)
        try:
            root = json.loads(artifact)
        except ValueError:
            return False
        if not isinstance(root, dict) or any(name not in declared.top for name in root):
            return False
        entries = root.get(declared.list)
        for item in entries if isinstance(entries, list) else ():
            if not isinstance(item, dict):
                # checkpoints: a hex statement, nothing else.
                if not isinstance(item, str) or declared.entry is not None:
                    return False
                continue
            if any(name not in (declared.entry or ()) for name in item):
                return False
            digest = item.get("digest")
            held = book._records.get(digest) if isinstance(digest, str) else None
            if held is not None and held.header.record_type in (RECORD_TYPE_REQUEST_ANSWERED, RECORD_TYPE_DISCLOSURE):
                return False
            body = item.get("body")
            if isinstance(body, str):
                raw = strict_hex(body)
                if raw is None or _mentions_any(raw, identities):
                    return False
        return True


def _checkpoints_through(history: list[Checkpoint], anchor: Checkpoint) -> list[Checkpoint]:
    out = []
    for cp in history:
        if cp.entries <= anchor.entries and cp.tree_size <= anchor.tree_size:
            out.append(cp)
        if cp.id == anchor.id:
            break
    return out


def _mentions_any(document: bytes, strings: set[str]) -> bool:
    """Whether a JSON document carries any of ``strings`` as a string value
    or member name; a document that is not JSON is scanned as bytes."""
    if not strings:
        return False
    try:
        root = json.loads(document)
    except ValueError:
        text = document.decode("utf-8", "replace")
        return any(s in text for s in strings)
    stack = [root]
    while stack:
        value = stack.pop()
        if isinstance(value, str):
            if value in strings:
                return True
        elif isinstance(value, list):
            stack.extend(value)
        elif isinstance(value, dict):
            if any(key in strings for key in value):
                return True
            stack.extend(value.values())
    return False


# --- receiving --------------------------------------------------------------------


def _verify_reply(sent: _Sent, parsed: Reply, keys: ResponderKeys, carried: list[dict]) -> dict:
    """The response record's statement for a parsed reply, under the pinned
    responder keys. A reply not signed under the pinned signer key for this
    request's digest raises :class:`InvalidError`; a genuinely signed
    artifact that fails a check of profile §7.2, or is over a limit, is
    received-and-failed. ``carried`` is the witness receipts the reply
    carries for its anchor."""
    statement: dict[str, Any] = {"request_digest": sent.digest, "key_id": keys.signer,
                                 "checkpoint_key_id": keys.checkpoint}
    if parsed.refusal is not None:
        r = parsed.refusal
        if r.request_digest != sent.digest or r.key_id != keys.signer:
            raise InvalidError("refusal is not for this request under the pinned responder key")
        if not verify_refusal_offline(r):
            raise InvalidError("refusal signature does not verify")
        statement["outcome"] = OUTCOME_REFUSAL if refusal_conforming(r, sent.request.profile) else OUTCOME_REFUSAL_NONCONFORMING
        if r.reason:
            statement["reason"] = r.reason
        return statement
    e = parsed.envelope
    if e.request_digest != sent.digest or e.responder != keys.signer:
        raise InvalidError("envelope is not for this request under the pinned responder key")
    try:
        sig = strict_hex(e.sig)
        if sig is None or len(sig) != 64:
            raise ValueError
        Ed25519PublicKey.from_public_bytes(bytes.fromhex(e.responder)).verify(sig, e.signing_body())
    except (ValueError, InvalidSignature):
        raise InvalidError("envelope signature does not verify") from None
    statement["outcome"] = OUTCOME_ARTIFACT
    statement["artifact_digest"] = e.artifact_digest
    statement["anchor"] = e.anchor_digest
    try:
        not_checked, anchor = _verify_envelope_content(sent, e, keys)
    except InvalidError as failure:
        statement["outcome"], statement["failure"] = OUTCOME_ARTIFACT_FAILED, str(failure)
    else:
        if not_checked:
            statement["not_checked"] = not_checked
        statement["witness"] = witness_standing(anchor, carried, keys.witnesses)
    return {k: v for k, v in statement.items() if v != ""}


def _verify_envelope_content(sent: _Sent, e: Envelope, keys: ResponderKeys) -> tuple[list[str], bytes | None]:
    """The checks of profile §7.3 that follow the envelope signature. Returns
    the properties it could not check and the anchor's signed checkpoint
    statement when the material carried and verified it; raises naming the
    first that failed."""
    req = sent.request
    if e.profile != req.profile:
        raise InvalidError(f"profile_version: requested {req.profile}, got {e.profile}")
    if not is_profile_time(e.issued_at):
        raise InvalidError(f"issued_at_form: {e.issued_at!r}")
    if e.subject.to_wire() != req.subject.to_wire() or e.derivation != req.derivation:
        raise InvalidError("subject_binding: the envelope answers a different subject or derivation")
    if _digest(e.artifact) != e.artifact_digest:
        raise InvalidError("artifact_digest: the artifact does not hash to the signed digest")
    # The artifact is now what the responder signed: a limit it exceeds is
    # received-and-failed, before any proof is checked.
    check_envelope_limits(req.subject.form == "record" and req.derivation is None, e.artifact, len(e.verification))
    digests = e.verification_digests or ()
    if len(digests) != len(e.verification):
        raise InvalidError(f"verification_binding: {len(digests)} entries, {len(e.verification)} items")
    for i, ((_, item), digest) in enumerate(zip(e.verification, digests, strict=True)):
        if _digest(item) != digest:
            raise InvalidError(f"verification_binding: item {i}")
    pin, floor = req.coverage.expected_pin, req.coverage.min_freshness
    if pin is not None:
        if e.anchor_digest != pin:
            raise InvalidError("anchor_match: the anchor is not the pinned one")
    elif isinstance(floor, int):
        if not (type(e.freshness) is int and e.freshness >= floor):
            raise InvalidError("anchor_match: the anchor does not state a size meeting the floor")
    elif floor is not None:
        attained = parse_ns(e.freshness) if is_profile_time(e.freshness) else None
        if attained is None or attained < sent.floor_time:
            raise InvalidError("anchor_match: the anchor does not state a time meeting the floor")
    return _verify_anchor_inclusion(req, e, keys.checkpoint)


def _verify_anchor_inclusion(req: EvidenceRequest, e: Envelope, checkpoint_key: str) -> tuple[list[str], bytes | None]:
    half_pin = req.subject.form == "exchange" and req.coverage.expected_pin == req.subject.content
    if req.subject.form == "record" and req.derivation is None:
        return [], _verify_record_inclusion(e, checkpoint_key)
    if req.subject.form == "checkpoints" or req.derivation == DERIVATION_HISTORY_CARD:
        return [], _verify_checkpoint_chain(e, checkpoint_key)
    # Record sets and registered derivations: the profile defines no
    # material kind for them (P-13). Their bodies and anchor member are
    # checked; inclusion is not.
    fields = _json_object(e.artifact)
    if not half_pin and fields.get("anchor") != e.anchor_digest:
        raise InvalidError("anchor_inclusion: the artifact names another anchor")
    records = fields.get("records")
    for i, entry in enumerate(records if isinstance(records, list) else ()):
        if isinstance(entry, dict) and "body" in entry:
            raw = strict_hex(entry["body"])
            if raw is None or not _record_body_matches(raw, entry.get("digest")):
                raise InvalidError(f"anchor_inclusion: record {i} does not hash to its digest")
    return ["anchor_inclusion"], None


def _record_body_matches(body: bytes, digest: Any) -> bool:
    """Whether a record-set entry's body is the record its digest names. A
    book serves the capsule preimage, whose SHA-256 is the record id; the
    capsules plugin serves the capsule as its ledger holds it, carrying that
    id as ``capsule_id`` with the signature and key_id that are no part of
    it. Either way the body hashes to the digest by the capsule_id rule, and
    nothing else is accepted."""
    if _digest(body) == digest:
        return True
    try:
        capsule = strict_json(body)
    except ValueError:
        return False
    if not isinstance(capsule, dict) or capsule.get("capsule_id") != digest:
        return False
    try:
        return _digest(capsule_preimage(capsule, digest)) == digest
    except CorruptError:
        return False


def _json_object(octets: bytes) -> dict:
    try:
        fields = strict_json(octets)
    except ValueError:
        fields = None
    if not isinstance(fields, dict):
        raise InvalidError("anchor_inclusion: the artifact is not a JSON object")
    return fields


def _signed_checkpoint(statement: bytes, pinned: str) -> Checkpoint:
    try:
        cp = read_checkpoint(statement)
    except InvalidError as exc:
        raise InvalidError(f"anchor_inclusion: {exc}") from None
    if not pinned or cp.key_id != pinned:
        raise InvalidError(f"anchor_inclusion: checkpoint is signed by {cp.key_id}, not the pinned key")
    return cp


def _verify_record_inclusion(e: Envelope, checkpoint_key: str) -> bytes:
    """The §6.2.1 procedure under the pinned checkpoint key. Returns the
    anchor's checkpoint statement."""
    items: dict[str, bytes] = {}
    for kind, octets in e.verification:
        if kind in items:
            raise InvalidError(f"anchor_inclusion: kind {kind} appears more than once")
        if kind not in (KIND_CLL_CHECKPOINT, KIND_CLL_INCLUSION_PROOF, KIND_CLL_CONSISTENCY_PROOF):
            raise InvalidError(f"anchor_inclusion: kind {kind!r} is not one this requester implements")
        items[kind] = octets
    if KIND_CLL_CHECKPOINT not in items or KIND_CLL_INCLUSION_PROOF not in items:
        raise InvalidError("anchor_inclusion: the checkpoint or the inclusion proof is not present")
    if e.subject.content != e.artifact_digest:
        raise InvalidError("anchor_inclusion: the record is not named by its artifact digest")
    if _digest(items[KIND_CLL_CHECKPOINT]) != e.anchor_digest:
        raise InvalidError("anchor_inclusion: the checkpoint is not the anchor")
    cp = _signed_checkpoint(items[KIND_CLL_CHECKPOINT], checkpoint_key)
    if type(e.freshness) is int and e.freshness != cp.tree_size or \
            isinstance(e.freshness, str) and e.freshness != profile_time_ns(cp.issued_at_ns):
        raise InvalidError("anchor_inclusion: anchor.freshness is not what the checkpoint commits")
    try:
        verify_cll_inclusion(cp.peaks, bytes.fromhex(e.artifact_digest), items[KIND_CLL_INCLUSION_PROOF])
        if KIND_CLL_CONSISTENCY_PROOF in items:
            if cp.prev_size == 0:
                raise InvalidError("anchor_inclusion: the checkpoint extends no earlier commitment")
            verify_cll_consistency(cp.prev_peaks, cp.peaks, items[KIND_CLL_CONSISTENCY_PROOF])
    except (VerificationError, ValueError) as exc:
        if isinstance(exc, InvalidError):
            raise
        raise InvalidError(f"anchor_inclusion: {exc}") from None
    return items[KIND_CLL_CHECKPOINT]


def _verify_checkpoint_chain(e: Envelope, checkpoint_key: str) -> bytes:
    """A checkpoints or history_card/1 artifact is its own material: every
    checkpoint signed under the pinned key for one log, sizes strictly
    growing, each consistency proof a history card carries reaching the next
    commitment, and the last checkpoint the envelope's anchor, whose
    statement it returns."""
    fields = _json_object(e.artifact)
    card = isinstance(fields.get("history"), list)
    if card:
        pairs = [(entry.get("checkpoint") if isinstance(entry, dict) else None,
                  entry.get("consistency_from_previous") if isinstance(entry, dict) else None)
                 for entry in fields["history"]]
    else:
        listed = fields.get("checkpoints")
        pairs = [(item, None) for item in (listed if isinstance(listed, list) else ())]
    if not pairs:
        raise InvalidError("anchor_inclusion: no checkpoints")
    previous: Checkpoint | None = None
    for i, (text, proof) in enumerate(pairs):
        raw = strict_hex(text)
        if raw is None:
            raise InvalidError(f"anchor_inclusion: checkpoint {i} is not hex")
        cp = _signed_checkpoint(raw, checkpoint_key)
        if previous is not None:
            if cp.log_id != previous.log_id or cp.tree_size <= previous.tree_size:
                raise InvalidError(f"anchor_inclusion: checkpoint {i} does not extend the log")
            if isinstance(proof, str) and proof:
                octets = strict_hex(proof)
                if octets is None:
                    raise InvalidError(f"anchor_inclusion: consistency proof {i} is not hex")
                try:
                    verify_cll_consistency(previous.peaks, cp.peaks, octets)
                except (VerificationError, ValueError) as exc:
                    raise InvalidError(f"anchor_inclusion: checkpoint {i}: {exc}") from None
            elif card:
                raise InvalidError(f"anchor_inclusion: history entry {i} has no consistency proof")
        if i == len(pairs) - 1 and _digest(raw) != e.anchor_digest:
            raise InvalidError("anchor_inclusion: the last checkpoint is not the anchor")
        previous = cp
    return raw
