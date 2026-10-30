# SPDX-License-Identifier: Apache-2.0
"""The requester's limits and the witness standing of a verified answer
(evidencebook#209): the vectors in testdata/asker-limits, which Go's
request_limits_test.go runs too, and the same rules through
``Book.record_response``."""
from __future__ import annotations

import base64
import hashlib
import json
import time
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

from test_book import ask, book, clock, key, keys_of, resign, responder  # noqa: F401 (fixtures)

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "asker-limits"
LIMITS = json.loads((VECTORS / "limits.json").read_text(encoding="utf-8"))
WITNESS = json.loads((VECTORS / "witness.json").read_text(encoding="utf-8"))


def octets(build: dict) -> bytes:
    if build.get("nest"):
        n = build["nest"] - 1
        return b'{"x":' + b"[" * n + b"]" * n + b"}"
    if build.get("pad_to"):
        return b'{"x":"' + b"a" * (build["pad_to"] - len(b'{"x":""}')) + b'"}'
    if build.get("list"):
        return b'{"' + build["list"].encode() + b'":[' + b",".join([b"0"] * build["count"]) + b"]}"
    return build["text"].encode()


def expect(case: dict, check) -> None:
    start = time.monotonic()
    if case["error"] is None:
        check()
    else:
        with pytest.raises(eb.InvalidError, match=case["error"]):
            check()
    assert time.monotonic() - start < 1, case["name"]


def test_the_vectors_state_these_limits():
    assert LIMITS["limits"] == {"reply_bytes": eb.MAX_REPLY_BYTES, "depth": eb.MAX_REPLY_DEPTH,
                                "items": eb.MAX_REPLY_ITEMS, "receipts": eb.MAX_REPLY_RECEIPTS}


@pytest.mark.parametrize("case", LIMITS["reply"], ids=lambda c: c["name"])
def test_reply_limit_vectors(case):
    data = octets(case["build"])
    expect(case, lambda: check_reply_octets(data, True))
    if case["error"] is None:
        strict_json(data)  # within the limits, the decoder reads it


@pytest.mark.parametrize("case", LIMITS["artifact"], ids=lambda c: c["name"])
def test_artifact_limit_vectors(case):
    data = octets(case["build"])
    expect(case, lambda: check_envelope_limits(False, data, 0))


@pytest.mark.parametrize("case", LIMITS["verification"], ids=lambda c: c["name"])
def test_verification_limit_vectors(case):
    expect(case, lambda: check_envelope_limits(False, b"{}", case["count"]))


@pytest.mark.parametrize("case", LIMITS["receipts"], ids=lambda c: c["name"])
def test_receipt_limit_vectors(case):
    root = {eb.WITNESS_RECEIPTS_MEMBER: [{}] * case["count"]}
    expect(case, lambda: carried_receipts(root, "anchor"))


def test_a_bare_record_artifact_is_not_decoded_for_limits():
    check_envelope_limits(True, octets({"nest": eb.MAX_REPLY_DEPTH + 1}), 0)


@pytest.mark.parametrize("case", WITNESS["cases"], ids=lambda c: c["name"])
def test_witness_vectors(case):
    statement = None if case.get("no_anchor") else bytes.fromhex(WITNESS["statement"])
    directory = [eb.WitnessRow.from_json(row) for row in WITNESS["directory"]]
    carried = case["carried"] or []
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
    deep = b"[" * 100_000 + b"]" * 100_000
    hostile = {
        "bytes": with_member(reply.message, "x", "a" * eb.MAX_REPLY_BYTES),
        "depth": eb.Message(reply.message.octets[:-1] + b',"x":' + deep + b"}", eb.MEDIA_TYPE_JSON),
        "receipts": with_member(reply.message, eb.WITNESS_RECEIPTS_MEMBER, [{}] * (eb.MAX_REPLY_RECEIPTS + 1)),
    }
    for name, message in hostile.items():
        size = q.size()
        start = time.monotonic()
        with pytest.raises(eb.InvalidError, match="limit: "):
            q.record_response(sent.record_id, message, keys_of(r))
        assert time.monotonic() - start < 2, name
        assert q.size() == size, name
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
    start = time.monotonic()
    record = q.record_response(sent.record_id, eb.Message(resign(envelope, key("responder signer"))), keys_of(r))
    assert time.monotonic() - start < 2
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
