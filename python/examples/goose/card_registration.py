# SPDX-License-Identifier: Apache-2.0
# Demo-only stand-in for capsulectl card registration; deleted by evidencebook-skills#185.
"""Registering a Standard History card on a SCITT transparency service, for the Goose demo
(a module of the demo, not of the ``evidencebook`` package; agent-action-capsule ``spec/standard-history-v0.md`` §13, cited as SH), byte
for byte with Go's ``card_registration.go``.

Registration is opt-in and has no default: nothing here names or reaches a
service. The host supplies a registrar (its own client for the service it
chose) and the relying party's transparency policy. What it shows is
existence at a time and nothing else (SH §13): never that the counts are
right, never ``anchor_inclusion`` (it stays NOT_CHECKED for a card), never
that the holder registered no other card. A failed registration changes
nothing about the card, and carriage of the card statement beside the card is
the host's (SH §13.3): a card statement is never an entry of an envelope's
``verification``.

A service the holder operates confers nothing (SH §13.2), and a card
statement's ``iss`` and ``sub`` tell the service, and anyone who reads its
log, which holder key registered a card for which anchor and when. The log is
append-only, so a registration can never be withdrawn, or the link it makes
between the key and the anchor (SH §13.5). The statement carries the card's
digest, not the card, but a digest does not hide a card from a party that can
guess it: a party that holds the anchor, the policy digest and ``since`` can
test guesses of the counts of a small card against it. A holder registers
knowing that, and only on a service whose published policy it accepts. Not
registering is no finding against a card or its holder.
"""
from __future__ import annotations

import hashlib
import re
from collections.abc import Mapping, Sequence
from typing import Any, Protocol

import cbor2
from agent_action_capsule import witness as _witness


class InvalidError(ValueError):
    """The statement, the Receipt or the call is not what SH §13 allows."""


class Signer(Protocol):
    """The holder's key: ``public_key()`` (the raw 32-byte Ed25519 key) and ``sign(message)``."""

    def public_key(self) -> bytes: ...

    def sign(self, message: bytes) -> bytes: ...


def key_id(signer: Signer) -> str:
    """The lowercase hex of the signer's raw public key, the holder key identifier."""
    return signer.public_key().hex()


__all__ = [
    "CardStatementRegistrar", "InvalidError", "Signer", "key_id", "build_card_statement", "attach_card_receipt", "register_card",
    "check_card_registered", "MAX_CARD_RECEIPTS", "MAX_CARD_STATEMENT_BYTES",
]

_COSE_SIGN1_TAG = 18
_LABEL_ALG, _LABEL_CWT_CLAIMS, _LABEL_RECEIPTS = 1, 15, 394
_ALG_EDDSA = -8
#: SH §13.1: one to 16 Receipts per card statement, and at most 10 MiB.
MAX_CARD_RECEIPTS = 16
MAX_CARD_STATEMENT_BYTES = 10485760
_HEX64 = re.compile(r"[0-9a-f]{64}")


class CardStatementRegistrar(Protocol):
    """The host's client for the one transparency service it chose.
    ``register_statement`` posts the card statement's octets and returns the
    octets of the Receipt the service issued for it."""

    def register_statement(self, statement: bytes) -> bytes: ...


def _encode(value: Any) -> bytes:
    return cbor2.dumps(value, canonical=True)


def _statement(protected: bytes, receipts: Sequence[bytes], payload: bytes, signature: bytes) -> bytes:
    return _encode(cbor2.CBORTag(
        _COSE_SIGN1_TAG, [protected, {_LABEL_RECEIPTS: list(receipts)} if receipts else {}, payload, signature]))


def _sig_structure(protected: bytes, payload: bytes) -> bytes:
    return _encode(["Signature1", protected, b"", payload])


def build_card_statement(signer: Signer, card: bytes, anchor: str) -> bytes:
    """Sign a card statement (SH §13.1) over ``card``, the card's octets, as
    of ``anchor`` (the card's anchor digest, the envelope's ``anchor.digest``).
    The statement carries no Receipt yet: its unprotected map is empty, which
    is the form to submit to a service. A Receipt added later changes neither
    the statement entry nor the signature."""
    if signer is None:
        raise InvalidError("a card statement needs the holder's signer")
    if not card:
        raise InvalidError("a card statement needs the card octets")
    if not isinstance(anchor, str) or not _HEX64.fullmatch(anchor):
        raise InvalidError("the anchor is not 64 lowercase hex characters")
    protected = _encode({_LABEL_ALG: _ALG_EDDSA, _LABEL_CWT_CLAIMS: {1: key_id(signer), 2: anchor}})
    payload = hashlib.sha256(bytes(card)).digest()
    signature = signer.sign(_sig_structure(protected, payload))
    return _statement(protected, [], payload, signature)


def attach_card_receipt(card: bytes, holder: str, anchor: str, statement: bytes, receipt: bytes, policy: Any) -> bytes:
    """Add ``receipt``, the octets of one service Receipt, to ``statement`` and
    return the statement with it. The Receipt is first checked alone, on the
    statement, under the relying party's transparency ``policy`` with the card,
    the holder key identifier and the anchor, and refused unless that makes the
    statement PASS: ``card_registered`` passes when any one Receipt does, so
    checking only the whole statement would keep a bad Receipt beside a passing
    one. The whole statement must still pass afterwards. A card statement holds
    1 to 16 Receipts (SH §13.1); a seventeenth is refused, and a holder
    registers a second statement instead."""
    if len(statement) > MAX_CARD_STATEMENT_BYTES:
        raise InvalidError(f"the card statement is over {MAX_CARD_STATEMENT_BYTES} octets")
    try:
        tagged = cbor2.loads(statement)
        if not (isinstance(tagged, cbor2.CBORTag) and tagged.tag == _COSE_SIGN1_TAG):
            raise ValueError("not tag 18")
        protected, unprotected, payload, signature = tagged.value
        if not (isinstance(protected, bytes) and isinstance(unprotected, Mapping) and isinstance(payload, bytes)
                and isinstance(signature, bytes)):
            raise ValueError("not a COSE_Sign1")
    except Exception as exc:  # any decode failure is "not a card statement"
        raise InvalidError(f"not a card statement (tag 18 around a COSE_Sign1): {exc}") from exc
    if any(label != _LABEL_RECEIPTS for label in unprotected):
        raise InvalidError("the card statement has an unprotected label besides receipts")
    existing = list(unprotected.get(_LABEL_RECEIPTS, []))
    if len(existing) >= MAX_CARD_RECEIPTS:
        raise InvalidError(f"a card statement holds at most {MAX_CARD_RECEIPTS} Receipts")
    alone = _statement(protected, [receipt], payload, signature)
    report = _witness.check_card_statement(card, holder, anchor, alone, policy)
    if report.status != "PASS":
        raise InvalidError(f"the Receipt does not make the card statement pass ({report.status}): {report.detail}")
    candidate = _statement(protected, existing + [receipt], payload, signature)
    if len(candidate) > MAX_CARD_STATEMENT_BYTES:
        raise InvalidError(f"the card statement would be over {MAX_CARD_STATEMENT_BYTES} octets")
    report = _witness.check_card_statement(card, holder, anchor, candidate, policy)
    if report.status != "PASS":
        raise InvalidError(f"the card statement would not pass ({report.status}): {report.detail}")
    return candidate


def register_card(signer: Signer, card: bytes, envelope: Any, registrar: CardStatementRegistrar | None,
                  policy: Any) -> bytes:
    """Build the card statement for ``card``, the artifact of ``envelope`` (the
    answer it was derived in), have the host's ``registrar`` post it to the
    service the host chose, and return the statement with the service's Receipt
    attached, checked under ``policy``. The signer must be the envelope's
    responder: a statement under another key would be logged for good, append
    only, and then fail SH §13.3 step 2 at every reader, so it is refused
    before anything is posted. It is a call of its own: a card is complete
    without it, and an error here (the service is down, or its Receipt does
    not pass) leaves the card exactly as it was. The host keeps the returned
    octets beside the card (SH §13.3)."""
    if registrar is None:
        raise InvalidError("no transparency service is configured; registration is opt-in and has no default")
    if signer is None or key_id(signer) != envelope.responder:
        raise InvalidError("the signer is not the envelope's responder, the holder key")
    statement = build_card_statement(signer, card, envelope.anchor_digest)
    receipt = registrar.register_statement(statement)
    return attach_card_receipt(card, envelope.responder, envelope.anchor_digest, statement, receipt, policy)


def check_card_registered(card: bytes, envelope: Any, statements: Sequence[bytes] | None, policy: Any):
    """``card_registered`` (SH §13.3) over the card statements received beside
    ``card``, the octets of the card's artifact, under the envelope's responder
    (the holder key) and anchor and the relying party's transparency
    ``policy``: AAC's report, unchanged. NOT_PRESENT when there is no
    statement. A verifier may say a PASS means the holder registered this card
    with the service named, as the policy names it, at the registration time
    (or with no time when the Receipt has none); it must not say the service
    checked the counts, the deals or the anchor, or that this is the holder's
    only card (SH §13.4)."""
    return _witness.check_card_registered(card, envelope.responder, envelope.anchor_digest, statements, policy)
