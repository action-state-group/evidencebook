# SPDX-License-Identifier: Apache-2.0
"""The signing seam, and the COSE_Sign1 messages a book signs.

A book signs through any object with ``public_key()`` (the raw 32-byte
Ed25519 key) and ``sign(message)`` (a raw Ed25519 signature over exactly the
bytes given, never a pre-hash), so a host can plug in a key custodian that
never hands over its key. :class:`Ed25519Signer` is the in-process one.

The COSE_Sign1 wrapper is built here rather than with ``scitt_cose``: its
signers need the private key as PEM, which a custodian need not give, and
its statement builder orders the protected header differently from Go's
deterministic encoding, so the checkpoint statements, and every anchor
digest over them, would not be the bytes Go's book signs.
"""
from __future__ import annotations

from collections.abc import Mapping
from typing import Protocol

import cbor2
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

from .errors import InvalidError

__all__ = ["Signer", "Ed25519Signer", "key_id", "sign1"]

_COSE_SIGN1_TAG = 18


class Signer(Protocol):
    def public_key(self) -> bytes: ...

    def sign(self, message: bytes) -> bytes: ...


class Ed25519Signer:
    """An in-process Ed25519 key."""

    def __init__(self, private_key: Ed25519PrivateKey | bytes):
        if isinstance(private_key, (bytes, bytearray)):
            # A 32-byte seed, or Go's 64-byte ed25519.PrivateKey (the seed, then the public key).
            raw = bytes(private_key)
            if len(raw) not in (32, 64):
                raise InvalidError("an Ed25519 private key is a 32-byte seed or 64 bytes")
            private_key = Ed25519PrivateKey.from_private_bytes(raw[:32])
            if len(raw) == 64 and raw[32:] != private_key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw):
                raise InvalidError("the 64-byte private key's public half is not its seed's")
        if not isinstance(private_key, Ed25519PrivateKey):
            raise InvalidError("an Ed25519Signer takes an Ed25519 private key, its seed or its 64-byte form")
        self._key = private_key
        self._public = private_key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)

    def public_key(self) -> bytes:
        return self._public

    def sign(self, message: bytes) -> bytes:
        return self._key.sign(message)


def key_id(signer: Signer) -> str:
    """The lowercase hex of the signer's raw public key."""
    return signer.public_key().hex()


class EvidenceSigner:
    """A :class:`Signer` as ``agent_action_capsule.evidence_request.Signer``:
    ``key_id`` and ``sign(body) -> (sig_hex, key_id)``."""

    def __init__(self, signer: Signer):
        self._signer = signer
        self.key_id = key_id(signer)

    def sign(self, body: bytes) -> tuple[str, str]:
        return self._signer.sign(body).hex(), self.key_id


def sign1(signer: Signer, protected: Mapping[int, object], payload: bytes) -> bytes:
    """A COSE_Sign1 message (RFC 9052 §4.2) over ``payload``, its protected
    header encoded in the order ``protected`` lists it, its unprotected
    header empty."""
    protected_bstr = cbor2.dumps(dict(protected))
    to_be_signed = cbor2.dumps(["Signature1", protected_bstr, b"", payload])
    signature = signer.sign(to_be_signed)
    return cbor2.dumps(cbor2.CBORTag(_COSE_SIGN1_TAG, [protected_bstr, {}, payload, signature]))
