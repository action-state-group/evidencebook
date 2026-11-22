# SPDX-License-Identifier: Apache-2.0
"""Card registration (SH §13), byte for byte with Go's card_registration_test.go:
the AAC card-registration-v0 vectors in testdata/card-registration, a statement
and Receipt recorded from a real capsule-anchor run, and the refusals of
attach_card_receipt and register_card."""
from __future__ import annotations

import hashlib
import json
import sys
from dataclasses import dataclass
from pathlib import Path

import cbor2
import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

from evidencebook import Ed25519Signer

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from card_registration import (  # noqa: E402
    MAX_CARD_RECEIPTS, InvalidError, attach_card_receipt, build_card_statement, check_card_registered, key_id,
    register_card,
)

DATA = Path(__file__).resolve().parents[4] / "testdata" / "card-registration"
VECTORS = json.loads((DATA / "vectors.json").read_text())
CASES = {c["name"]: c for c in VECTORS["cases"]}


@dataclass
class Env:
    responder: str
    anchor_digest: str


def policy_of(case):
    policy = case["policy"]
    return VECTORS["policies"][policy] if isinstance(policy, str) else policy


def env_of(case):
    return Env(case["holder"], case["anchor"])


def receipts_of(statement: bytes) -> tuple[list[bytes], bytes]:
    """A vector statement's Receipts, and the statement with an empty unprotected map."""
    protected, unprotected, payload, signature = cbor2.loads(statement).value
    bare = cbor2.dumps(cbor2.CBORTag(18, [protected, {}, payload, signature]), canonical=True)
    return list(unprotected[394]), bare


def holder_signer() -> Ed25519Signer:
    return Ed25519Signer(hashlib.sha256(b"card-registration-v0 key holder").digest())


@pytest.mark.parametrize("name", sorted(CASES))
def test_card_registered_vectors(name):
    case = CASES[name]
    got = check_card_registered(
        bytes.fromhex(case["card"]), env_of(case), [bytes.fromhex(s) for s in case["statements"]], policy_of(case)).to_dict()
    want = case["expected"]
    assert got["status"] == want["status"]
    assert got["service"] == want["service"] and got["registration_time"] == want["registration_time"]
    assert [(s["status"], s["service"], s["registration_time"]) for s in got["statements"]] == \
        [(s["status"], s["service"], s["registration_time"]) for s in want["statements"]]


def test_build_matches_the_vector_byte_for_byte():
    case = CASES["pass-eddsa-time"]
    card, vector = bytes.fromhex(case["card"]), bytes.fromhex(case["statements"][0])
    receipts, bare = receipts_of(vector)
    signer = holder_signer()
    assert key_id(signer) == case["holder"]
    built = build_card_statement(signer, card, case["anchor"])
    assert built == bare
    assert attach_card_receipt(card, case["holder"], case["anchor"], built, receipts[0], policy_of(case)) == vector


def test_build_gives_the_bytes_Go_builds():
    """Go's BuildCardStatement over the same vector card, holder key and anchor wrote this golden
    file (card_registration_test.go TestBuildCardStatementMatchesGolden): the demo's bytes are Go's."""
    case = CASES["pass-eddsa-time"]
    golden = bytes.fromhex((DATA / "built-pass-eddsa-time.hex").read_text().strip())
    assert build_card_statement(holder_signer(), bytes.fromhex(case["card"]), case["anchor"]) == golden


def test_build_errors():
    signer, anchor = holder_signer(), "00" * 32
    for args in ((None, b"card", anchor), (signer, b"", anchor), (signer, b"card", "NOT-HEX")):
        with pytest.raises(InvalidError):
            build_card_statement(*args)


def test_attach_refuses():
    case = CASES["pass-eddsa-time"]
    card, policy = bytes.fromhex(case["card"]), policy_of(case)
    receipts, bare = receipts_of(bytes.fromhex(case["statements"][0]))

    def attach(receipt=receipts[0], statement=bare, card=card, policy=policy):
        return attach_card_receipt(card, case["holder"], case["anchor"], statement, receipt, policy)

    for other in ("receipt-unlisted-service", "receipt-over-card-digest"):
        foreign, _ = receipts_of(bytes.fromhex(CASES[other]["statements"][0]))
        with pytest.raises(InvalidError):
            attach(foreign[0])
    for bad in (dict(receipt=b"not a receipt"), dict(policy={"witnesses": []}), dict(card=b"another card"),
                dict(statement=b"\xa0")):
        with pytest.raises(InvalidError):
            attach(**bad)
    extra = CASES["unprotected-extra"]
    with pytest.raises(InvalidError):
        attach_card_receipt(bytes.fromhex(extra["card"]), extra["holder"], extra["anchor"],
                            bytes.fromhex(extra["statements"][0]), receipts[0], policy)
    statement = bare
    for _ in range(MAX_CARD_RECEIPTS):
        statement = attach(statement=statement)
    with pytest.raises(InvalidError, match="at most 16"):
        attach(statement=statement)


class Registrar:
    def __init__(self, receipt=None, error=None):
        self.receipt, self.error, self.posted = receipt, error, []

    def register_statement(self, statement):
        self.posted.append(statement)
        if self.error:
            raise self.error
        return self.receipt


def test_register_card():
    case = CASES["pass-eddsa-time"]
    card, policy, env = bytes.fromhex(case["card"]), policy_of(case), env_of(case)
    vector = bytes.fromhex(case["statements"][0])
    receipts, bare = receipts_of(vector)
    signer = holder_signer()

    registrar = Registrar(receipts[0])
    statement = register_card(signer, card, env, registrar, policy)
    assert registrar.posted == [bare] and statement == vector
    report = check_card_registered(card, env, [statement], policy)
    assert report.status == "PASS" and report.service == "service-a"

    with pytest.raises(InvalidError, match="no default"):
        register_card(signer, card, env, None, policy)
    other = Registrar(receipts[0])
    with pytest.raises(InvalidError):
        register_card(signer, card, Env("11" * 32, case["anchor"]), other, policy)
    assert other.posted == []
    down = RuntimeError("service unavailable")
    with pytest.raises(RuntimeError):
        register_card(signer, card, env, Registrar(error=down), policy)
    with pytest.raises(InvalidError):
        register_card(signer, card, env, Registrar(b"junk"), policy)
    assert check_card_registered(card, env, None, policy).status == "NOT_PRESENT"


def test_recorded_capsule_anchor_receipt():
    rec = json.loads((DATA / "capsule-anchor-receipt.json").read_text())
    env = Env(rec["holder"], rec["anchor"])
    statements = [bytes.fromhex(rec["statement"])]
    report = check_card_registered(bytes.fromhex(rec["card"]), env, statements, rec["policy"])
    assert report.status == "PASS" and report.service == "local-capsule-anchor"
    assert report.registration_time and report.registration_time > 0
    assert check_card_registered(bytes.fromhex(rec["card"]), env, statements, {"witnesses": []}).status != "PASS"


class ReceiptService:
    """An RFC 9942 Receipt issuer (a one-leaf tree, EdDSA) for the leaf entry of a card statement."""

    def __init__(self, seed: int):
        self.key = Ed25519PrivateKey.from_private_bytes(bytes([seed]) * 32)
        self.iat = 1_795_000_000

    def policy(self):
        raw = self.key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)
        return {"witnesses": [{"name": "test-service", "keys": [("302a300506032b6570032100" + raw.hex())]}]}

    def receipt_for(self, entry: bytes) -> bytes:
        leaf = hashlib.sha256(b"\x00" + entry).digest()
        protected = cbor2.dumps({1: -8, 395: 1, 15: {6: self.iat}}, canonical=True)
        signature = self.key.sign(cbor2.dumps(["Signature1", protected, b"", leaf], canonical=True))
        proof = cbor2.dumps([1, 0, []], canonical=True)
        return cbor2.dumps(cbor2.CBORTag(18, [protected, {396: {-1: [proof]}}, None, signature]), canonical=True)

    def register_statement(self, statement: bytes) -> bytes:
        return self.receipt_for(entry_of(statement))


def entry_of(statement: bytes) -> bytes:
    protected, _, payload, _ = cbor2.loads(statement).value
    return hashlib.sha256(cbor2.dumps(["Signature1", protected, b"", payload], canonical=True)).digest()


def test_attach_checks_the_new_receipt_alone():
    signer, service = holder_signer(), ReceiptService(7)
    anchor, card = "03" * 32, b'{"card":"x"}'
    env = Env(key_id(signer), anchor)
    statement = register_card(signer, card, env, service, service.policy())
    foreign, _ = receipts_of(bytes.fromhex(CASES["pass-eddsa-time"]["statements"][0]))
    for receipt in (b"junk", service.receipt_for(b"\x09" * 32), ReceiptService(8).receipt_for(entry_of(statement)),
                    foreign[0]):
        with pytest.raises(InvalidError):
            attach_card_receipt(card, env.responder, anchor, statement, receipt, service.policy())
    # An earlier Receipt that is not a byte string leaves a statement that can never pass.
    protected, _, payload, signature = cbor2.loads(statement).value
    broken = cbor2.dumps(cbor2.CBORTag(18, [protected, {394: [None]}, payload, signature]), canonical=True)
    with pytest.raises(InvalidError, match="would not pass"):
        attach_card_receipt(card, env.responder, anchor, broken, service.receipt_for(entry_of(statement)),
                            service.policy())
    two = attach_card_receipt(card, env.responder, anchor, statement, service.receipt_for(entry_of(statement)),
                              service.policy())
    assert two != statement
    report = check_card_registered(card, env, [two], service.policy())
    assert report.status == "PASS" and report.registration_time == service.iat
