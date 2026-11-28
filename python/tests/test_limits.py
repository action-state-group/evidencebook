# SPDX-License-Identifier: Apache-2.0
"""The requester's limits and the witness standing of a verified answer
(evidencebook#209): the vectors in testdata/asker-limits, which Go's
request_limits_test.go runs too, and the same rules through
``Book.record_response``."""
from __future__ import annotations

import base64
import hashlib
import json
import operator
from pathlib import Path

import pytest

import evidencebook as eb
from evidencebook._limits import (
    carried_receipts,
    check_envelope_limits,
    check_reply_octets,
    json_depth_over,
    witness_standing,
)
from agent_action_capsule.canonical import jcs

from evidencebook.request import parse_reply, strict_json
import cbor2
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cll.checkpoint.cose_wire import verify_checkpoint_cose_offline

from test_book import ask, book, clock, key, keys_of, resign, responder  # noqa: F401 (fixtures)

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "asker-limits"
LIMITS = json.loads((VECTORS / "limits.json").read_text(encoding="utf-8"))
WITNESS = json.loads((VECTORS / "witness.json").read_text(encoding="utf-8"))


def octets(build: dict) -> bytes:
    if build.get("utf16le"):
        n = build["nest"]
        return ('{"x":"\\"","y":' + "[" * n + "]" * n + "}").encode("utf-16-le")
    if build.get("nest"):
        n = build["nest"] - 1
        return b'{"x":' + b"[" * n + b"]" * n + b"}"
    if build.get("pad_to"):
        return b'{"x":"' + b"a" * (build["pad_to"] - len(b'{"x":""}')) + b'"}'
    if build.get("list"):
        return b'{"' + build["list"].encode() + b'":[' + b",".join([b"0"] * build["count"]) + b"]}"
    return build["text"].encode()


class Scanned(bytes):
    """Octets that count the passes made over them and the bytes each read,
    so a test bounds the work a limit check does rather than its time on
    this machine (evidencebook#1077). The depth scan iterates the octets;
    each iterator's remaining length, read afterwards, gives how far it got."""

    def __iter__(self):
        it = super().__iter__()
        self.__dict__.setdefault("passes", []).append(it)
        return it

    def reads(self) -> list[int]:
        return [len(self) - operator.length_hint(it) for it in self.__dict__.get("passes", [])]


def over_at(data: bytes, limit: int) -> int:
    """The octets a depth scan reads to refuse a nest vector: up to and
    including its first bracket past ``limit``, however long the rest."""
    openers = [i for i, c in enumerate(data) if c in b"[{"]
    return openers[limit] + 1


def expect(case: dict, check, data: Scanned | None = None, passes: int = 1) -> None:
    """Run the case's check and compare its outcome with the vector. With
    ``data``, also bound the work: a reply over the byte limit is refused
    unread, one over the depth limit after reading only up to the first
    bracket past it, and anything else costs ``passes`` full passes."""
    if case["error"] is None:
        check()
    else:
        with pytest.raises(eb.InvalidError, match=case["error"]):
            check()
    if data is None:
        return
    if "bytes:" in (case["error"] or ""):
        assert data.reads() == [], case["name"]
    elif "depth:" in (case["error"] or ""):
        assert data.reads() == [over_at(data, eb.MAX_REPLY_DEPTH)], case["name"]
    else:
        assert data.reads() == [len(data)] * passes, case["name"]


def test_the_vectors_state_these_limits():
    assert LIMITS["limits"] == {"reply_bytes": eb.MAX_REPLY_BYTES, "depth": eb.MAX_REPLY_DEPTH,
                                "items": eb.MAX_REPLY_ITEMS, "receipts": eb.MAX_REPLY_RECEIPTS}


@pytest.mark.parametrize("case", LIMITS["reply"], ids=lambda c: c["name"])
def test_reply_limit_vectors(case):
    data = Scanned(octets(case["build"]))
    expect(case, lambda: check_reply_octets(data, True), data)
    if case["error"] is None:
        strict_json(data)  # within the limits, the decoder reads it


@pytest.mark.parametrize("case", LIMITS["artifact"], ids=lambda c: c["name"])
def test_artifact_limit_vectors(case):
    data = Scanned(octets(case["build"]))
    # Within the limits the decoder reads it after the limit check: two
    # passes, each its own depth scan; refused, the decoder never runs.
    expect(case, lambda: check_envelope_limits(False, data, 0), data, passes=2)


@pytest.mark.parametrize("case", LIMITS["verification"], ids=lambda c: c["name"])
def test_verification_limit_vectors(case):
    expect(case, lambda: check_envelope_limits(False, b"{}", case["count"]))


@pytest.mark.parametrize("case", LIMITS["receipts"], ids=lambda c: c["name"])
def test_receipt_limit_vectors(case):
    root = {eb.WITNESS_RECEIPTS_MEMBER: [{}] * case["count"]}
    expect(case, lambda: carried_receipts(root, "anchor"))


def test_an_artifact_with_a_duplicate_member_is_left_to_the_content_checks():
    # Go's decoder refuses it, so neither port charges a limit for it.
    data = b'{"checkpoints":[],"checkpoints":[' + b",".join([b"0"] * (eb.MAX_REPLY_ITEMS + 1)) + b"]}"
    check_envelope_limits(False, data, 0)


def test_a_bare_record_artifact_is_not_decoded_for_limits():
    check_envelope_limits(True, octets({"nest": eb.MAX_REPLY_DEPTH + 1}), 0)


@pytest.mark.parametrize("case", WITNESS["cases"], ids=lambda c: c["name"])
def test_witness_vectors(case):
    statement = bytes.fromhex(WITNESS["statement"])
    anchor = hashlib.sha256(statement).hexdigest()
    directory = [eb.WitnessRow.from_json(row) for row in WITNESS["directory"]]
    root = {eb.WITNESS_RECEIPTS_MEMBER: [{"checkpoint": anchor, "witness": w} for w in case["carried"] or []]}
    carried = carried_receipts(root, anchor)
    if case.get("no_anchor"):
        statement = None
    assert witness_standing(statement, carried, directory) == case["want"]
    assert witness_standing(statement, carried, []) == eb.UNWITNESSED


@pytest.mark.parametrize("text, over", [
    ("[]", False), ("[[]]", True), ('{"a":[]}', True), ('"[[[[["', False), ('["\\"[[[["]', False),
    ('["\\\\",[]]', True), ('{"a":{"b":0},"c":{"d":1}}', True), ("[],{}", False),
])
def test_json_depth_over(text, over):
    assert json_depth_over(text.encode(), 1) is over


def test_a_deeply_nested_reply_is_refused_not_a_recursion_error():
    deep = b"[" * 100_000 + b"]" * 100_000
    with pytest.raises(eb.InvalidError, match="limit: reply depth"):
        parse_reply(eb.Message(deep, eb.MEDIA_TYPE_JSON))
    with pytest.raises(ValueError, match="limit: depth"):
        strict_json(deep)


def with_member(reply: eb.Message, member: str, value) -> eb.Message:
    wire = json.loads(reply.octets)
    wire[member] = value
    return eb.Message(json.dumps(wire).encode(), reply.media_type)


def test_a_reply_over_an_octet_limit_is_refused_and_the_request_stays_open(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("checkpoints"))
    # Each hostile reply also carries a signature that does not verify, so a
    # refusal naming the limit shows no signature was checked before it.
    forged = with_member(reply.message, "sig", "00" * 64)
    deep = b"[" * 100_000 + b"]" * 100_000
    prefix = forged.octets[:-1] + b',"x":'  # at depth 1, inside the reply object
    hostile = {
        "bytes": with_member(forged, "x", "a" * eb.MAX_REPLY_BYTES),
        "depth": eb.Message(prefix + deep + b"}", eb.MEDIA_TYPE_JSON),
        "receipts": with_member(forged, eb.WITNESS_RECEIPTS_MEMBER, [{}] * (eb.MAX_REPLY_RECEIPTS + 1)),
    }
    for name, message in hostile.items():
        size = q.size()
        data = Scanned(message.octets)
        with pytest.raises(eb.InvalidError, match="limit: "):
            q.record_response(sent.record_id, eb.Message(data, message.media_type), keys_of(r))
        assert q.size() == size, name
        if name == "bytes":
            assert data.reads() == [], name  # refused on its length, unread
        elif name == "depth":  # read up to the nest's bracket past the limit
            assert data.reads() == [len(prefix) + eb.MAX_REPLY_DEPTH], name
    with pytest.raises(eb.InvalidError, match="envelope signature does not verify"):
        q.record_response(sent.record_id, forged, keys_of(r))
    record = q.record_response(sent.record_id, reply.message, keys_of(r))
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT
    assert record.header.statement["witness"] == eb.UNWITNESSED


def test_a_signed_envelope_over_a_count_limit_is_received_and_failed(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("checkpoints"))
    envelope = json.loads(reply.message.octets)
    artifact = json.loads(base64.urlsafe_b64decode(envelope["artifact"] + "=="))
    # The entries need not be well formed: the count is checked first.
    artifact["checkpoints"] = [""] * (eb.MAX_REPLY_ITEMS + 1)
    data = jcs(artifact)
    envelope["artifact"] = base64.urlsafe_b64encode(data).rstrip(b"=").decode()
    envelope["artifact_digest"] = hashlib.sha256(data).hexdigest()
    record = q.record_response(sent.record_id, eb.Message(resign(envelope, key("responder signer"))), keys_of(r))
    statement = record.header.statement
    assert statement["outcome"] == eb.OUTCOME_ARTIFACT_FAILED
    assert f"limit: checkpoints: {eb.MAX_REPLY_ITEMS + 1} over {eb.MAX_REPLY_ITEMS}" in statement["failure"]
    assert "witness" not in statement


def test_a_carried_receipt_that_does_not_verify_never_fails_the_answer(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("checkpoints"))
    good = WITNESS["cases"][1]["carried"][0]  # a genuine receipt, but over another checkpoint
    carrier = [
        "x", {"checkpoint": 1}, {"witness": "y"},
        {"checkpoint": reply.envelope.anchor_digest, "witness": good},
        {"checkpoint": reply.envelope.anchor_digest, "witness": {"ts_url": WITNESS["directory"][0]["endpoint"]}},
    ]
    keys = eb.ResponderKeys(keys_of(r).signer, keys_of(r).checkpoint,
                            tuple(eb.WitnessRow.from_json(row) for row in WITNESS["directory"]))
    record = q.record_response(sent.record_id, with_member(reply.message, eb.WITNESS_RECEIPTS_MEMBER, carrier), keys)
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT
    assert record.header.statement["witness"] == eb.UNWITNESSED


def mint_receipt(statement: bytes, witness_key: Ed25519PrivateKey, url: str) -> dict:
    """A checkpoint.witnesses entry: a genuine RFC 9162 COSE receipt over the
    checkpoint statement from a single-entry witness log (Go's mintReceipt)."""
    digest = verify_checkpoint_cose_offline(statement).decoded.to_checkpoint_record().digest()
    entry = hashlib.sha256(bytes.fromhex(digest)).digest()
    leaf = hashlib.sha256(b"\x00" + entry).digest()
    protected = cbor2.dumps({1: -8, 395: 1})
    signature = witness_key.sign(cbor2.dumps(["Signature1", protected, b"", leaf]))
    receipt = cbor2.dumps(cbor2.CBORTag(18, [protected, {396: {-1: [cbor2.dumps([1, 0, []])]}}, None, signature]))
    return {"ts_url": url, "entry_hash": entry.hex(), "receipt_b64": base64.b64encode(receipt).decode(),
            "leaf_index": 0, "tree_size": 1}


@pytest.mark.parametrize("subject", ["checkpoints", "record"])
def test_a_directory_witness_receipt_over_the_anchor_is_witnessed(clock, responder, subject):
    r, records = responder
    q = book(clock, "requester")
    if subject == "record":
        sent, reply = ask(q, r, eb.Subject("record", records[0].record_id))
    else:
        sent, reply = ask(q, r, eb.Subject("checkpoints"))
    anchor = r.checkpoints()[-1]
    assert reply.envelope.anchor_digest == anchor.anchor_digest
    witness = Ed25519PrivateKey.from_private_bytes(bytes(range(32)))
    url = "https://witness.test"
    row = eb.WitnessRow(endpoint=url, key_ids=(witness.public_key().public_bytes_raw().hex(),))
    keys = eb.ResponderKeys(keys_of(r).signer, keys_of(r).checkpoint, (row,))
    carrier = [{"checkpoint": anchor.anchor_digest, "witness": mint_receipt(anchor.statement, witness, url)}]
    record = q.record_response(sent.record_id, with_member(reply.message, eb.WITNESS_RECEIPTS_MEMBER, carrier), keys)
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT
    assert record.header.statement["witness"] == eb.WITNESSED


def test_a_refusal_over_the_receipt_limit_is_refused_unrecorded(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("full_history"))
    assert reply.refusal is not None
    size = q.size()
    with pytest.raises(eb.InvalidError, match="limit: witness receipts"):
        q.record_response(sent.record_id, with_member(reply.message, eb.WITNESS_RECEIPTS_MEMBER,
                                                      [{}] * (eb.MAX_REPLY_RECEIPTS + 1)), keys_of(r))
    assert q.size() == size
    q.record_response(sent.record_id, reply.message, keys_of(r))


def test_a_substituted_artifact_fails_on_its_digest_not_on_a_limit(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("checkpoints"))
    envelope = json.loads(reply.message.octets)
    data = b'{"checkpoints":[' + b",".join([b'""'] * (eb.MAX_REPLY_ITEMS + 1)) + b"]}"
    envelope["artifact"] = base64.urlsafe_b64encode(data).rstrip(b"=").decode()  # the signature is untouched
    record = q.record_response(sent.record_id, eb.Message(jcs(envelope)), keys_of(r))
    failure = record.header.statement["failure"]
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT_FAILED
    assert failure.startswith("artifact_digest") and "limit" not in failure


def test_a_signed_utf16_artifact_nested_deep_fails_without_a_recursion_error(clock, responder):
    r, _ = responder
    q = book(clock, "requester")
    sent, reply = ask(q, r, eb.Subject("checkpoints"))
    envelope = json.loads(reply.message.octets)
    data = octets({"utf16le": True, "nest": 100_000})
    envelope["artifact"] = base64.urlsafe_b64encode(data).rstrip(b"=").decode()
    envelope["artifact_digest"] = hashlib.sha256(data).hexdigest()
    record = q.record_response(sent.record_id, eb.Message(resign(envelope, key("responder signer"))), keys_of(r))
    assert record.header.statement["outcome"] == eb.OUTCOME_ARTIFACT_FAILED
