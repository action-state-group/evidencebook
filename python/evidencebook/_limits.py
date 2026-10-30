# SPDX-License-Identifier: Apache-2.0
"""The requester's stated limits on what it will check, and the witness
standing of a verified answer, as Go's ``request_limits.go`` states them.

Where a limit falls on octets nothing has authenticated yet (the reply's
size and nesting, and the unsigned ``witness_receipts`` carrier), exceeding
it refuses the reply and records nothing, so the request stays open. Where
it falls on what the responder signed, the envelope signature is checked
first and exceeding it records the answer as received and failed, naming
the limit, before any checkpoint signature, proof or receipt is checked."""

from __future__ import annotations

import base64
import binascii
import hashlib
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from .errors import InvalidError

#: A reply's octets, and an Evidence Bundle's: the most a record's originals
#: may hold, so a larger reply could never be recorded anyway.
MAX_REPLY_BYTES = 8 << 20
#: How deeply a JSON reply or its artifact nests arrays and objects; the
#: outermost value is depth 1.
MAX_REPLY_DEPTH = 32
#: Each list an answer carries: its verification entries, and the artifact's
#: checkpoints, history entries or records.
MAX_REPLY_ITEMS = 10_000
#: The witness receipts a reply carries in ``witness_receipts``.
MAX_REPLY_RECEIPTS = 64

WITNESSED = "witnessed"
UNWITNESSED = "unwitnessed"
#: The reply member that carries witness receipts beside the envelope. Not
#: part of the interop profile, and nothing signs it.
WITNESS_RECEIPTS_MEMBER = "witness_receipts"


def json_depth_over(data: bytes, limit: int) -> bool:
    """Whether JSON text nests arrays and objects deeper than ``limit``,
    scanned without decoding it."""
    depth, in_string, escaped = 0, False, False
    for c in data:
        if in_string:
            if escaped:
                escaped = False
            elif c == 0x5C:  # backslash
                escaped = True
            elif c == 0x22:  # quote
                in_string = False
        elif c == 0x22:
            in_string = True
        elif c in (0x5B, 0x7B):
            depth += 1
            if depth > limit:
                return True
        elif c in (0x5D, 0x7D):
            depth -= 1
    return False


def check_reply_octets(octets: bytes, json_text: bool) -> None:
    """The limits on a reply's unauthenticated octets; raises naming one."""
    if len(octets) > MAX_REPLY_BYTES:
        raise InvalidError(f"limit: reply bytes: {len(octets)} over {MAX_REPLY_BYTES}")
    if json_text and json_depth_over(octets, MAX_REPLY_DEPTH):
        raise InvalidError(f"limit: reply depth: over {MAX_REPLY_DEPTH}")


def check_envelope_limits(bare_record: bool, artifact: bytes, verification_count: int) -> None:
    """The limits on what the responder signed, before any check whose cost
    they bound. A bare record's artifact is hashed, never decoded."""
    if verification_count > MAX_REPLY_ITEMS:
        raise InvalidError(f"limit: verification: {verification_count} over {MAX_REPLY_ITEMS}")
    if bare_record:
        return
    if json_depth_over(artifact, MAX_REPLY_DEPTH):
        raise InvalidError(f"limit: artifact depth: over {MAX_REPLY_DEPTH}")
    from .request import strict_json  # request imports this module

    try:
        # As Go's DecodeJSON reads it: strict UTF-8 (json.loads would detect
        # UTF-16, whose NULs the byte scan above cannot see through), no
        # duplicate member.
        fields = strict_json(artifact)
    except (ValueError, UnicodeDecodeError):
        return  # not JSON: the content checks report it
    if not isinstance(fields, dict):
        return
    for name in ("checkpoints", "history", "records"):
        items = fields.get(name)
        if isinstance(items, list) and len(items) > MAX_REPLY_ITEMS:
            raise InvalidError(f"limit: {name}: {len(items)} over {MAX_REPLY_ITEMS}")


def carried_receipts(root: Any, anchor: str) -> list[dict]:
    """The reply's ``witness_receipts`` entries for ``anchor``, each a
    checkpoint.witnesses entry. Malformed entries are skipped, since nothing
    signs them; only the list's size is refused."""
    listed = root.get(WITNESS_RECEIPTS_MEMBER) if isinstance(root, dict) else None
    if not isinstance(listed, list):
        return []
    if len(listed) > MAX_REPLY_RECEIPTS:
        raise InvalidError(f"limit: witness receipts: {len(listed)} over {MAX_REPLY_RECEIPTS}")
    return [entry["witness"] for entry in listed
            if isinstance(entry, dict) and entry.get("checkpoint") == anchor and isinstance(entry.get("witness"), dict)]


@dataclass(frozen=True)
class WitnessRow:
    """One row of a requester's witness directory (Go ``WitnessRow``): the
    witness's endpoint, its binding (cll when empty) and its keys. A key id
    is a raw Ed25519 key in hex, or the SHA-256 of a DER key listed in
    ``public_keys`` (standard base64)."""

    endpoint: str
    key_ids: Sequence[str] = ()
    public_keys: Sequence[str] = ()
    binding: str = ""
    name: str = ""

    @classmethod
    def from_json(cls, row: dict) -> WitnessRow:
        return cls(endpoint=row.get("endpoint", ""), key_ids=tuple(row.get("key_ids") or ()),
                   public_keys=tuple(row.get("public_keys") or ()), binding=row.get("binding", ""),
                   name=row.get("name", ""))

    def keys(self) -> list[bytes]:
        """The row's raw Ed25519 keys in ``key_ids`` order."""
        from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
        from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat, load_der_public_key

        by_hash: dict[str, bytes] = {}
        for text in self.public_keys:
            try:
                der = base64.b64decode(text, validate=True)
                key = load_der_public_key(der)
            except (binascii.Error, ValueError):
                continue
            if isinstance(key, Ed25519PublicKey):
                by_hash[hashlib.sha256(der).hexdigest()] = key.public_bytes(Encoding.Raw, PublicFormat.Raw)
        keys = []
        for key_id in self.key_ids:
            if key_id in by_hash:
                keys.append(by_hash[key_id])
            else:
                try:
                    raw = bytes.fromhex(key_id)
                except ValueError:
                    continue
                if len(raw) == 32:
                    keys.append(raw)
        return keys


def _binding(url: str) -> str:
    scheme, found, _ = url.partition("://")
    if not found:
        return "cll"
    scheme = scheme.lower()
    for binding in ("rekor", "scrapi"):
        if scheme.startswith(binding + "+"):
            return binding
    return "cll"


def _endpoint(url: str) -> str:
    if _binding(url) != "cll":
        url = url.partition("+")[2]
    return url.rstrip("/")


_ASCII_INT = re.compile(r"[+-]?[0-9]+")


def _receipt_int(value: Any) -> int:
    if isinstance(value, bool):
        raise ValueError("not an integer")
    if isinstance(value, int):
        return value
    # ASCII digits only, as Go's strconv.ParseInt reads them: str.isdigit
    # and int() would also take "５" or "٥".
    if isinstance(value, str) and _ASCII_INT.fullmatch(value):
        return int(value)
    raise ValueError("not an integer")


def _receipt_shape(receipt: bytes) -> bool:
    """The receipt shape Go's checkpointed-local-log verifier accepts, which
    scitt-cose alone is looser about: a size within the witness response
    limit, an RFC 9162 (vds 1) EdDSA COSE_Sign1 with exactly one inclusion
    proof."""
    import cbor2
    from cll.log import MAX_WITNESS_RESPONSE_BYTES

    if not 1 <= len(receipt) <= MAX_WITNESS_RESPONSE_BYTES:
        return False
    try:
        message = cbor2.loads(receipt)
        if isinstance(message, cbor2.CBORTag):
            if message.tag != 18:
                return False
            message = message.value
        # cbor2 decodes arrays as lists or tuples and maps as dicts or
        # frozendicts, depending on where they sit.
        if not isinstance(message, (list, tuple)) or len(message) != 4:
            return False
        protected, unprotected = cbor2.loads(message[0]), message[1]
        vdp = unprotected.get(396) if isinstance(unprotected, Mapping) else None
        proofs = vdp.get(-1) if isinstance(vdp, Mapping) else None
    except (cbor2.CBORDecodeError, ValueError, TypeError, RecursionError):
        return False
    return (isinstance(protected, Mapping) and protected.get(395) == 1 and protected.get(1) == -8
            and isinstance(proofs, (list, tuple)) and len(proofs) == 1)


def _receipt_covers(statement: bytes, entry: dict, directory: Sequence[WitnessRow]) -> bool:
    """Whether one checkpoint.witnesses entry is a receipt over
    ``statement`` from a witness in ``directory``, under its key."""
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
    from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
    from cll.checkpoint.cose_wire import verify_checkpoint_cose_offline
    from scitt_cose import verify_receipt

    url, entry_hash, encoded = entry.get("ts_url"), entry.get("entry_hash"), entry.get("receipt_b64")
    if not (isinstance(url, str) and url and isinstance(entry_hash, str) and entry_hash
            and isinstance(encoded, str) and encoded):
        return False
    try:
        receipt = base64.b64decode(encoded, validate=True)
        leaf_index, tree_size = _receipt_int(entry.get("leaf_index")), _receipt_int(entry.get("tree_size"))
    except (binascii.Error, ValueError):
        return False
    if not _receipt_shape(receipt):
        return False
    binding, endpoint = _binding(url), _endpoint(url)
    row = next((r for r in directory if (r.binding or "cll") == binding and r.endpoint.rstrip("/") == endpoint), None)
    if row is None or binding != "cll":
        return False
    verified = verify_checkpoint_cose_offline(statement)
    if not verified.ok or verified.decoded is None:
        return False
    expected = hashlib.sha256(bytes.fromhex(verified.decoded.to_checkpoint_record().digest())).hexdigest()
    if entry_hash != expected:
        return False
    for key in row.keys():
        try:
            pem = Ed25519PublicKey.from_public_bytes(key).public_bytes(Encoding.PEM, PublicFormat.SubjectPublicKeyInfo)
            result = verify_receipt(receipt, leaf_entry_hex=expected, log_public_key_pem=pem)
        except Exception:  # a malformed receipt or key is not a covering one
            continue
        if result.ok and result.tree_size == tree_size and result.leaf_index == leaf_index:
            return True
    return False


def witness_standing(statement: bytes | None, carried: Sequence[dict], directory: Sequence[WitnessRow]) -> str:
    """``witnessed`` when at least one carried receipt covers the anchor's
    signed checkpoint statement under a witness in ``directory``;
    ``unwitnessed`` otherwise, including when no statement is in hand."""
    if statement is None:
        return UNWITNESSED
    for entry in carried:
        if isinstance(entry, dict) and _receipt_covers(statement, entry, directory):
            return WITNESSED
    return UNWITNESSED
