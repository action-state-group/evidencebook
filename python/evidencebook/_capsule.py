# SPDX-License-Identifier: Apache-2.0
"""The record capsule a book seals for each record.

The capsule is the one Go's book seals through capsule-emit's ``emit.Seal``:
an Agent Action Capsule (format 4, -05) of action type ``fyi`` whose
``model_attestation.compute_attestation.agent_input_digest`` is the SHA-256
of the header's JCS, with each link also a ``references[]`` entry. Its id,
computed with ``agent_action_capsule.canonical``, is the record id the log
commits. capsule-emit's Python ``build_capsule`` is not used: it attaches
``compute_attestation`` only beside a model and adds a default
``disposition`` to an ``fyi`` action, so its capsule is not Go's.
"""
from __future__ import annotations

import hashlib
from datetime import datetime

from agent_action_capsule.canonical import LOCAL_ONLY_FIELDS, compute_capsule_id, jcs
from agent_action_capsule.media_types import CAPSULE_ID_MEDIA_TYPE
from agent_action_capsule.versions import DEFAULT_FORMAT_VERSION, DEFAULT_SPEC_VERSION

from ._time import capsule_timestamp
from .errors import CorruptError
from .record import Header
from .signer import Signer, sign1

_COSE_ALG_EDDSA = -8


def seal_record(header: Header, header_bytes: bytes, *, operator: str, developer: str, at: datetime,
                signer: Signer) -> tuple[str, dict, bytes]:
    """The record capsule for ``header`` (whose JCS is ``header_bytes``), its
    id, and its Producer Envelope: ``(capsule_id, capsule, envelope)``."""
    capsule: dict = {
        "spec_version": DEFAULT_SPEC_VERSION,
        "format_version": DEFAULT_FORMAT_VERSION,
        "canonicalization_id": "jcs",
        "action_id": f"{header.book_id}#{header.seq}",
        "action_type": "fyi",
        "operator": operator,
        "developer": developer,
        "timestamp": capsule_timestamp(at),
        "assurance": {"attestation_mode": "self_attested", "effect_mode": "not_applicable", "ledger_mode": "standalone"},
        "model_attestation": {"compute_attestation": {"agent_input_digest": hashlib.sha256(header_bytes).hexdigest()}},
    }
    if header.links:
        capsule["references"] = [
            {"type": "agent-action-capsule", "digest_alg": "SHA-256", "digest": link.target} for link in header.links
        ]
    capsule_id = compute_capsule_id(capsule)
    capsule["capsule_id"] = capsule_id
    # The Producer Envelope signs the raw 32-byte id; its protected header is
    # in capsule-emit's order (content type, kid, then alg).
    envelope = sign1(signer, {3: CAPSULE_ID_MEDIA_TYPE, 4: signer.public_key(), 1: _COSE_ALG_EDDSA},
                     bytes.fromhex(capsule_id))
    return capsule_id, capsule, envelope


def capsule_preimage(capsule: dict, record_id: str) -> bytes:
    """A record's octets as the log commits them: the JCS of the capsule
    without ``capsule_id`` and the local-only producer fields, whose SHA-256
    is the record id."""
    body = jcs({k: v for k, v in capsule.items() if k != "capsule_id" and k not in LOCAL_ONLY_FIELDS})
    if hashlib.sha256(body).hexdigest() != record_id:
        raise CorruptError(f"capsule body does not hash to record id {record_id}")
    return body


# The top-level member names the base profile defines (capsule-emit Go
# baseMembers): no extension may use one.
_BASE_MEMBERS = frozenset({
    "spec_version", "format_version", "canonicalization_id", "capsule_id", "action_id", "action_type",
    "operator", "developer", "timestamp", "epoch_id", "domain", "provenance", "provenance_mode", "disposition",
    "effect", "chain", "assurance", "cross_party", "model_attestation", "references", "constraints",
    "compliance", "signature", "key_id",
})


def _extension_name(name: str) -> bool:
    # capsule-emit's ^[a-z][a-z0-9_]*$
    return bool(name) and "a" <= name[0] <= "z" and all("a" <= c <= "z" or "0" <= c <= "9" or c == "_" for c in name)


def seal_payload(action_id: str, payload: bytes, extensions: dict, *, operator: str, developer: str,
                 at: datetime, signer: Signer) -> tuple[str, bytes, bytes]:
    """A standalone ``fyi`` Capsule carrying ``payload``, a JSON document's
    canonical JSON (JCS) bytes, as Go's shelf ``SealSent`` seals it through
    ``emit.Seal``: its ``agent_input_digest`` is the payload's SHA-256, it
    has no chain and no references, and ``extensions`` are further top-level
    members, each named ``[a-z][a-z0-9_]*``, not a base member, not null, and
    with no float or unsafe integer. Returns ``(capsule_id, capsule JCS,
    envelope)``; raises :class:`ValueError` for input it refuses."""
    import json

    if not action_id.strip():
        raise ValueError("action id is required")
    if not operator.strip() or not developer.strip():
        raise ValueError("operator and developer must not be empty")
    try:
        decoded = json.loads(payload)
    except ValueError as exc:
        raise ValueError(f"the payload is not JSON: {exc}") from None
    if jcs(decoded) != bytes(payload):
        raise ValueError("the payload is not its canonical JSON (JCS) bytes")
    capsule: dict = {
        "spec_version": DEFAULT_SPEC_VERSION,
        "format_version": DEFAULT_FORMAT_VERSION,
        "canonicalization_id": "jcs",
        "action_id": action_id,
        "action_type": "fyi",
        "operator": operator,
        "developer": developer,
        "timestamp": capsule_timestamp(at),
        "assurance": {"attestation_mode": "self_attested", "effect_mode": "not_applicable", "ledger_mode": "standalone"},
        "model_attestation": {"compute_attestation": {"agent_input_digest": hashlib.sha256(payload).hexdigest()}},
    }
    for name in sorted(extensions):
        value = extensions[name]
        if not _extension_name(name):
            raise ValueError(f"extension name {name!r} must be lowercase letters, digits and underscores, "
                             "starting with a letter")
        if name in _BASE_MEMBERS:
            raise ValueError(f"extension {name!r} is a base-profile member")
        if value is None:
            raise ValueError(f"extension {name!r} must not be null")
        try:
            jcs(value)
        except (TypeError, ValueError) as exc:
            raise ValueError(f"extension {name!r}: {exc}") from None
        capsule[name] = value
    capsule_id = compute_capsule_id(capsule)
    capsule["capsule_id"] = capsule_id
    envelope = sign1(signer, {3: CAPSULE_ID_MEDIA_TYPE, 4: signer.public_key(), 1: _COSE_ALG_EDDSA},
                     bytes.fromhex(capsule_id))
    return capsule_id, jcs(capsule), envelope
