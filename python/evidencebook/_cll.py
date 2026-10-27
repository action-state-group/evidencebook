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
from agent_action_capsule.evidence_request import encode_cll_consistency_proof, encode_cll_inclusion_proof
from cll.checkpoint import core
from cll.checkpoint.cose_wire import CLL_CHECKPOINT_CONTENT_TYPE, encode_checkpoint_claims, \
    verify_checkpoint_cose_offline
from cll.checkpoint.emit import CheckpointRecord
from cll.log import validate_identifier

from ._time import from_nanos, parse_ns, rfc3339_nano, truncate_ms
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
    issued_at: datetime
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


def _wire_profile(statement: bytes) -> str:
    """The checks Go's ``checkpoint.ParseRecord`` makes beyond the signature:
    a canonical COSE_Sign1 with an empty unprotected header and exactly the
    protected alg (EdDSA), content type, 32-byte kid and CWT claims holding
    exactly iss and sub, sub being ``<iss>#<log_size>``. Empty when it conforms."""
    try:
        message = cbor2.loads(statement)
        protected_bstr, unprotected, payload, signature = message.value
        protected = cbor2.loads(protected_bstr)
        claims = cbor2.loads(payload)
    except Exception:  # noqa: BLE001 -- any malformed statement is refused
        return "not a COSE_Sign1 statement"
    if not isinstance(message, cbor2.CBORTag) or message.tag != _COSE_SIGN1_TAG:
        return "not a COSE_Sign1 statement"
    if cbor2.dumps(message) != statement:
        return "the statement is not canonical"
    if unprotected != {} or not isinstance(protected, dict) or set(protected) != {1, 3, 4, 15}:
        return "the COSE headers are not the checkpoint profile's"
    cwt, kid = protected[15], protected[4]
    if protected[1] != _COSE_ALG_EDDSA or protected[3] != CLL_CHECKPOINT_CONTENT_TYPE or \
            not isinstance(kid, bytes) or len(kid) != 32:
        return "the COSE headers are not the checkpoint profile's"
    why = _claims_profile(claims, payload)
    if why:
        return why
    if not isinstance(cwt, dict) or set(cwt) != {1, 2} or not isinstance(cwt[1], str) or \
            not isinstance(claims, dict) or cwt[2] != f"{cwt[1]}#{claims.get('log_size')}":
        return "the CWT claims are not the checkpoint profile's"
    return ""


_CLAIMS = {"kind", "log_size", "commitment", "prev_size", "prev_commitment", "issued_at"}
_OPTIONAL_CLAIMS = {"cadence", "consistency_proof"}
_MAX_UINT64 = (1 << 64) - 1
_MAX_PORTABLE = (1 << 53) - 1
_PROOF_MEMBERS = {"size_a", "size_b", "witness", "old_peaks", "new_peaks"}


def _canonical_commitment(raw: object) -> bool:
    try:
        peaks = cbor2.loads(raw)
        return isinstance(peaks, list) and core.commitment_object(peaks) == raw
    except Exception:  # noqa: BLE001
        return False


def _claims_profile(claims: object, payload: bytes) -> str:
    """Go's ``decodeWireClaims``: exactly the claims it defines, in canonical
    CBOR (so no indefinite length and no duplicate), each commitment in its
    canonical form, a positive log size and cadence."""
    if not isinstance(claims, dict) or not _CLAIMS <= set(claims) or not set(claims) <= _CLAIMS | _OPTIONAL_CLAIMS:
        return "the claims are not the checkpoint profile's"
    # Present means a proof: Go's omitempty re-encoding drops a null one, so it is refused.
    proof = claims.get("consistency_proof")
    if "consistency_proof" in claims and (not isinstance(proof, dict) or set(proof) != _PROOF_MEMBERS):
        return "the claims are not the checkpoint profile's"
    if cbor2.dumps(claims, canonical=True) != payload:
        return "the claims are not canonical"
    sizes_ok = all(type(claims[n]) is int and 0 <= claims[n] <= _MAX_UINT64 for n in ("log_size", "prev_size"))
    cadence_ok = "cadence" not in claims or type(claims["cadence"]) is int and 0 < claims["cadence"] <= _MAX_PORTABLE
    if claims["kind"] != "cll-checkpoint" or not sizes_ok or claims["log_size"] == 0 or not claims["issued_at"] or \
            not cadence_ok:
        return "the claims are not the checkpoint profile's"
    if not _canonical_commitment(claims["commitment"]) or \
            claims["prev_commitment"] != b"" and not _canonical_commitment(claims["prev_commitment"]):
        return "a commitment is not canonical"
    return ""


def read_checkpoint(statement: bytes) -> Checkpoint:
    """Parse and signature-check a CLL checkpoint statement (under its own kid)."""
    why = _wire_profile(statement)
    if why:
        raise InvalidError(f"checkpoint statement: {why}")
    result = verify_checkpoint_cose_offline(statement)
    if not result.ok or result.decoded is None:
        raise InvalidError(f"checkpoint statement: {'; '.join(result.errors)}")
    d = result.decoded
    issued = parse_ns(d.timestamp)
    if issued is None:
        raise InvalidError(f"checkpoint issued_at {d.timestamp!r}")
    try:
        entries = core.leaf_count(d.mmr_size)
    except core.InvalidArgumentError:
        raise InvalidError(f"checkpoint size {d.mmr_size} is not a complete MMR") from None
    return Checkpoint(
        log_id=d.log_id, id=f"{d.root}:{d.mmr_size}", entries=entries, root=d.root, tree_size=d.mmr_size,
        key_id=d.key_id, issued_at=from_nanos(issued), statement=bytes(statement), prev_size=d.prev_size,
        peaks=tuple(d.new_peak_hashes), prev_peaks=tuple(d.prev_peak_hashes),
    )


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
