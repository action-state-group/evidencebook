# SPDX-License-Identifier: Apache-2.0
"""The book's commitment substrate: a Checkpointed Local Log over the
record ids, in memory.

The MMR, its proofs and the checkpoint claims map come from
checkpointed-local-log's Python package (``cll.checkpoint``); the proof
octets from agent-action-capsule's ``evidence_request``. The log is rebuilt
from the stored records when a book opens, and its checkpoints are the
stored signed statements, so nothing here is durable on its own.

A checkpoint is issued exactly when Go's book issues one: when the log grew
since the last checkpoint, at the book's clock truncated to the
millisecond, signed by the log key in the CLL checkpoint wire form.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from datetime import datetime

import cbor2
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
from agent_action_capsule.evidence_request import encode_cll_consistency_proof, encode_cll_inclusion_proof
from cll.checkpoint import core
from cll.checkpoint.cose_wire import CLL_CHECKPOINT_CONTENT_TYPE, encode_checkpoint_claims
from cll.checkpoint.emit import CheckpointRecord
from cll.log import validate_identifier

from . import _cborread
from ._time import parse_rfc3339_ns, rfc3339_nano, truncate_ms
from .errors import CorruptError, InvalidError, NotCoveredError
from .signer import Signer, key_id, sign1

SUBSTRATE_KIND_CLL = "cll"
_COSE_ALG_EDDSA = -8
_COSE_SIGN1_TAG = 18


@dataclass(frozen=True)
class Checkpoint:
    """One signed checkpoint of the log, as read from its statement.
    ``tree_size`` is the MMR size, ``entries`` the records it covers."""

    log_id: str
    id: str
    entries: int
    root: str
    tree_size: int
    key_id: str
    #: issued_at as nanoseconds since the epoch (RFC 3339 allows year 0000,
    #: which a datetime cannot hold).
    issued_at_ns: int
    statement: bytes
    prev_size: int = 0
    peaks: tuple[bytes, ...] = field(default=(), repr=False)
    prev_peaks: tuple[bytes, ...] = field(default=(), repr=False)
    kind: str = SUBSTRATE_KIND_CLL

    def covers(self, seq: int) -> bool:
        return 1 <= seq <= self.entries

    @property
    def anchor_digest(self) -> str:
        """SHA-256 over the signed statement octets: the value a requester
        pins in ``expected_pin`` (profile §6.2.1)."""
        return hashlib.sha256(self.statement).hexdigest()


class _Refused(Exception):
    pass


_REQUIRED_CLAIMS = ("kind", "log_size", "commitment", "prev_size", "prev_commitment", "issued_at")
_UNDERSTOOD_LABELS = {1, 3, 4, 15}  # alg, content type, kid, CWT_Claims
_MAX_PORTABLE = (1 << 53) - 1  # backend-contract.md MaxPortableInteger
_MAX_CHECKPOINT_BYTES = 65536  # backend-contract.md MaxCheckpointBytes


def mmr_peak_count(size: int) -> int | None:
    """The number of peaks of a complete MMR of ``size`` nodes, or ``None``
    when ``size`` is not one. Computed here for every portable size: cll's
    ``core.peaks`` refuses sizes from 2^50 on."""
    count, remaining, height = 0, size, size.bit_length()
    while remaining > 0:
        while height > 0 and (1 << height) - 1 > remaining:
            height -= 1
        if height == 0:
            return None
        remaining -= (1 << height) - 1
        count += 1
        if remaining and ((1 << height) - 1) <= remaining:
            return None  # mountains must strictly shrink
    return count


def _uint(value: object, maximum: int = _MAX_PORTABLE) -> bool:
    return type(value) is int and 0 <= value <= maximum


def _peaks(raw: object, size: int, what: str) -> list[bytes]:
    """-01 §Commitment Representation: the peak list of the MMR at ``size``,
    one 32-byte peak per mountain, in canonical CBOR exactly as the MMR
    profile's commitment object encodes it."""
    if not isinstance(raw, bytes):
        raise _Refused(f"{what} is not a byte string")
    try:
        peaks = _cborread.read(raw)
    except _cborread.CborError as exc:
        raise _Refused(f"{what}: {exc}") from None
    if not isinstance(peaks, list) or not all(isinstance(p, bytes) and len(p) == 32 for p in peaks) or \
            core.commitment_object(peaks) != raw:
        raise _Refused(f"{what} is not a canonical peak list")
    if len(peaks) != mmr_peak_count(size):
        raise _Refused(f"{what} is not the peak list of an MMR of {size} nodes")
    return peaks


def _read(statement: bytes) -> Checkpoint:
    """A CLL checkpoint read under draft-mih-scitt-checkpointed-local-log-01,
    the revision of the wire form deployed implementations write (spec
    §The Checkpoint, §Commitment Representation, §Required constraints,
    §Witnessing), RFC 9052, RFC 8949 and RFC 3339; where the draft is
    silent, as Go's reader reads it. test/interop/python-evidencebook/
    checkpoint_parity.py holds every rule, and the cases where Go departs
    from the draft."""
    if len(statement) > _MAX_CHECKPOINT_BYTES:
        raise _Refused(f"the statement is over {_MAX_CHECKPOINT_BYTES} bytes")
    try:
        message = _cborread.read(statement)
    except _cborread.CborError as exc:
        raise _Refused(f"not CBOR: {exc}") from None
    # RFC 9052 §4.2 COSE_Sign1, tagged (the draft is silent on the untagged
    # form; Go requires the tag).
    if not isinstance(message, _cborread.Tag) or message.number != _COSE_SIGN1_TAG or \
            not isinstance(message.value, list) or len(message.value) != 4:
        raise _Refused("not a tagged COSE_Sign1 message")
    protected_bstr, unprotected, payload, signature = message.value
    if not all(isinstance(x, bytes) for x in (protected_bstr, payload, signature)) or not isinstance(unprotected, dict):
        raise _Refused("a COSE_Sign1 member has the wrong type")
    # The unprotected header may carry a witness's countersignature
    # (§Witnessing); its labels are integers or texts (RFC 9052 §3).
    if any(type(label) not in (int, str) for label in unprotected):
        raise _Refused("an unprotected header label is not an integer or a text")
    try:
        protected = _cborread.read(protected_bstr) if protected_bstr else {}
        claims = _cborread.read(payload)
    except _cborread.CborError as exc:
        raise _Refused(f"the protected header or the claims: {exc}") from None
    if not isinstance(protected, dict) or any(type(label) not in (int, str) for label in protected):
        raise _Refused("the protected header is not a map of integer or text labels")
    # RFC 9052 §3.1 crit: every label it lists must be present and understood.
    crit = protected.get(2)
    if 2 in protected and not (isinstance(crit, list) and crit and all(
            type(label) in (int, str) and label in protected and label in _UNDERSTOOD_LABELS for label in crit)):
        raise _Refused("the protected header lists a critical label not understood")
    kid, cwt = protected.get(4), protected.get(15)
    # The draft: the protected header carries the algorithm and the key
    # identifier. Silent on their values and on the content type, so Go's:
    # EdDSA, the raw 32-byte public key, application/cll-checkpoint+cbor.
    if type(protected.get(1)) is not int or protected.get(1) != _COSE_ALG_EDDSA or \
            protected.get(3) != CLL_CHECKPOINT_CONTENT_TYPE or not isinstance(kid, bytes) or len(kid) != 32:
        raise _Refused("the protected header does not carry EdDSA, the content type and a 32-byte kid")
    # §Witnessing: CWT_Claims with iss and sub (texts, RFC 8392); other
    # claims allowed. The draft does not define sub's form; Go's is
    # "<iss>#<log_size>", with iss a CLL identifier.
    if not isinstance(cwt, dict) or not isinstance(cwt.get(1), str) or not isinstance(cwt.get(2), str):
        raise _Refused("the CWT_Claims do not carry iss and sub as texts")
    # §The Checkpoint: a CBOR map with these claims; others MUST be ignored.
    if not isinstance(claims, dict) or any(name not in claims for name in _REQUIRED_CLAIMS):
        raise _Refused("the claims are not a map of the required claims")
    log_size, prev_size, issued_at = claims["log_size"], claims["prev_size"], claims["issued_at"]
    if claims["kind"] != "cll-checkpoint":
        raise _Refused("kind is not cll-checkpoint")
    # log_size is the MMR's node count (a complete size), as deployed and as
    # -02 states; portable integers (backend-contract MaxPortableInteger).
    if not _uint(log_size) or log_size == 0 or mmr_peak_count(log_size) is None:
        raise _Refused(f"log_size {log_size!r} is not a complete MMR size")
    # §Required constraints 2: log_size >= prev_size.
    if not _uint(prev_size) or prev_size > log_size or prev_size and mmr_peak_count(prev_size) is None:
        raise _Refused(f"prev_size {prev_size!r} is not a complete MMR size up to log_size")
    if "cadence" in claims and not (_uint(claims["cadence"]) and claims["cadence"] > 0):
        raise _Refused("cadence is not a positive portable integer")
    if cwt[2] != f"{cwt[1]}#{log_size}" or not validate_identifier(cwt[1]):
        raise _Refused("the CWT subject is not <log id>#<log_size> under a CLL identifier")
    peaks = _peaks(claims["commitment"], log_size, "the commitment")
    # prev_commitment: zero-length for the first, else the peak list at prev_size.
    if prev_size == 0:
        if claims["prev_commitment"] != b"":
            raise _Refused("a first checkpoint's prev_commitment is not zero-length")
        prev_peaks: list[bytes] = []
    else:
        prev_peaks = _peaks(claims["prev_commitment"], prev_size, "the previous commitment")
    # The consistency relation at equal sizes is equality. At a greater size
    # it is checked with the proof conveyed beside the checkpoint (profile
    # §6.2.1 cll-consistency-proof), never with a consistency_proof claim:
    # -01 defines none, so the claim is ignored.
    if prev_size == log_size and prev_peaks != peaks:
        raise _Refused("equal sizes carry different commitments")
    issued = parse_rfc3339_ns(issued_at)
    if issued is None:
        raise _Refused(f"issued_at {issued_at!r} is not an RFC 3339 date-time")
    try:
        Ed25519PublicKey.from_public_bytes(kid).verify(
            signature, cbor2.dumps(["Signature1", protected_bstr, b"", payload]))
    except (InvalidSignature, ValueError):
        raise _Refused("the signature does not verify") from None
    root = core.root_from_peaks(peaks)
    return Checkpoint(
        log_id=cwt[1], id=f"{root.hex()}:{log_size}", entries=_leaf_count(log_size), root=root.hex(),
        tree_size=log_size, key_id=kid.hex(), issued_at_ns=issued, statement=bytes(statement), prev_size=prev_size,
        peaks=tuple(peaks), prev_peaks=tuple(prev_peaks),
    )


def _leaf_count(size: int) -> int:
    """Leaves of a complete MMR of ``size`` nodes (each mountain of 2^h - 1
    nodes holds 2^(h-1) leaves)."""
    leaves, remaining, height = 0, size, size.bit_length()
    while remaining:
        while (1 << height) - 1 > remaining:
            height -= 1
        remaining -= (1 << height) - 1
        leaves += 1 << (height - 1)
    return leaves


def read_checkpoint(statement: bytes) -> Checkpoint:
    """Parse and signature-check a CLL checkpoint statement under its own
    kid, by draft-mih-scitt-checkpointed-local-log-01 (see :func:`_read`).
    No ASG Python package reads it so: cll's ``verify_checkpoint_cose_offline``
    refuses what the draft allows (other claims, a proof under its own sizes)
    and skips what it requires, and agent-action-capsule's reader checks
    only the profile's §6.2.1 rules."""
    try:
        return _read(statement)
    except _Refused as exc:
        raise InvalidError(f"checkpoint statement: {exc}") from None


class _Nodes:
    def __init__(self) -> None:
        self.hashes: list[bytes] = []

    def size(self) -> int:
        return len(self.hashes)

    def node(self, pos: int) -> bytes:
        return self.hashes[pos]

    def append_nodes(self, hashes: list[bytes]) -> None:
        self.hashes.extend(hashes)


class CLLSubstrate:
    """A Checkpointed Local Log named ``log_id`` whose checkpoints
    ``checkpoint_key`` signs; it may differ from the book's signer."""

    def __init__(self, log_id: str, checkpoint_key: Signer):
        if not validate_identifier(log_id):
            raise InvalidError(f"log id {log_id!r} is not a CLL identifier")
        self.log_id = log_id
        self._key = checkpoint_key
        self._nodes = _Nodes()
        self._leaf_sizes: list[int] = []  # the MMR size after each leaf
        self._latest: Checkpoint | None = None

    @property
    def size(self) -> int:
        return len(self._leaf_sizes)

    def append(self, record_id: str) -> int:
        try:
            value = bytes.fromhex(record_id)
        except ValueError:
            raise InvalidError(f"record id {record_id!r} is not hex") from None
        if len(value) != 32 or value.hex() != record_id:
            raise InvalidError(f"record id {record_id!r} is not 64 lowercase hex characters")
        core.add_leaf(self._nodes, core.leaf_hash(value))
        self._leaf_sizes.append(self._nodes.size())
        return len(self._leaf_sizes)

    def restore(self, checkpoint: Checkpoint) -> None:
        """Take a stored checkpoint of this log as the latest, after checking
        it against the log rebuilt from the records."""
        if checkpoint.log_id != self.log_id or checkpoint.key_id != key_id(self._key):
            raise CorruptError("a stored checkpoint is not this log's")
        if checkpoint.tree_size > self._nodes.size() or self._peaks(checkpoint.tree_size) != list(checkpoint.peaks):
            raise CorruptError(f"stored checkpoint {checkpoint.id} does not match the log")
        self._latest = checkpoint

    @property
    def latest(self) -> Checkpoint | None:
        return self._latest

    def _peaks(self, size: int) -> list[bytes]:
        return [self._nodes.node(p) for p in core.peaks(size)]

    def checkpoint(self, now: datetime) -> Checkpoint:
        """A checkpoint over everything appended: a new one when the log grew
        since the last, otherwise the last. Raises when nothing was ever appended."""
        size = self._nodes.size()
        prev = self._latest
        if prev is not None and prev.tree_size == size:
            return prev
        if size == 0:
            raise InvalidError("an empty log has no checkpoint")
        peaks = self._peaks(size)
        record = CheckpointRecord(
            v=1, kind="mmr_checkpoint", log_id=self.log_id, mmr_size=size, root=core.root_from_peaks(peaks).hex(),
            prev_size=prev.tree_size if prev else 0, prev_root=prev.root if prev else "", key_id=key_id(self._key),
            timestamp=rfc3339_nano(truncate_ms(now)), signature="",
        )
        claims = encode_checkpoint_claims(
            record, peaks,
            prev_peak_hashes=list(prev.peaks) if prev else None,
            consistency_proof=core.consistency_proof(self._nodes, prev.tree_size, size) if prev else None,
        )
        protected = {1: _COSE_ALG_EDDSA, 3: CLL_CHECKPOINT_CONTENT_TYPE, 4: self._key.public_key(),
                     15: {1: self.log_id, 2: f"{self.log_id}#{size}"}}
        self._latest = read_checkpoint(sign1(self._key, protected, cbor2.dumps(claims, canonical=True)))
        return self._latest

    def _check(self, at: Checkpoint) -> None:
        if at.kind != SUBSTRATE_KIND_CLL or at.log_id != self.log_id:
            raise InvalidError("checkpoint is not from this log")
        if at.tree_size > self._nodes.size():
            raise NotCoveredError(f"checkpoint size {at.tree_size} exceeds the log")
        if core.root_from_peaks(self._peaks(at.tree_size)).hex() != at.root:
            raise CorruptError("checkpoint root does not match this log")

    def inclusion_proof(self, seq: int, at: Checkpoint) -> bytes:
        """The ``cll-inclusion-proof`` octets for the record at ``seq`` under ``at``."""
        if not at.covers(seq):
            raise NotCoveredError(f"seq {seq} under checkpoint {at.id}")
        self._check(at)
        proof = core.inclusion_proof(self._nodes, seq - 1, at.tree_size)
        return encode_cll_inclusion_proof(seq - 1, [bytes.fromhex(h) for h in proof.witness])

    def consistency_proof(self, old_size: int, new_size: int, at: Checkpoint) -> bytes:
        """The ``cll-consistency-proof`` octets: the MMR of ``new_size`` nodes
        extends the one of ``old_size``."""
        self._check(at)
        if old_size == 0 or old_size > new_size or new_size > at.tree_size:
            raise InvalidError(f"consistency from {old_size} to {new_size} under {at.id}")
        proof = core.consistency_proof(self._nodes, old_size, new_size)
        return encode_cll_consistency_proof(
            old_size, new_size, [bytes.fromhex(h) for h in proof.old_peaks],
            [[bytes.fromhex(h) for h in path] for path in proof.witness], [bytes.fromhex(h) for h in proof.new_peaks],
        )
