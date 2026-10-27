# SPDX-License-Identifier: Apache-2.0
"""The request-draft conformance vectors in testdata/evidence-request, the
same files and the same case counts Go's interop_vectors_test.go and
interop_resolution_test.go run."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest
from agent_action_capsule.evidence_request import verify_refusal_offline

from evidencebook import _decide
from evidencebook._time import parse_ns
from evidencebook.request import MEDIA_TYPE_CBOR, MEDIA_TYPE_JSON, Message, parse_reply, read_request, \
    refusal_conforming
from evidencebook.errors import InvalidError

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "evidence-request"


def cases(name: str) -> list[dict]:
    return json.loads((VECTORS / name).read_text(encoding="utf-8"))["cases"]


def test_vendored_vectors_match_upstream_sums():
    lines = (VECTORS / "SHA256SUMS").read_text().splitlines()
    for line in lines:
        digest, name = line.split("  ")
        assert hashlib.sha256((VECTORS / name).read_bytes()).hexdigest() == digest, name
    assert len(lines) == 4


REQUESTS = cases("request.json")


def test_request_corpus_size():
    assert len(REQUESTS) == 33


@pytest.mark.parametrize("case", REQUESTS, ids=[c["id"] for c in REQUESTS])
def test_request_vectors(case):
    bindings = [(Message(case["request_json"].encode("utf-8"), MEDIA_TYPE_JSON), case.get("request_json_digest"))]
    if case.get("request_cbor_hex"):
        bindings.append((Message(bytes.fromhex(case["request_cbor_hex"]), MEDIA_TYPE_CBOR), case.get("request_cbor_digest")))
    for message, digest in bindings:
        if digest:
            assert hashlib.sha256(message.octets).hexdigest() == digest
        got = read_request(message)
        if case["expect"]["well_formed"]:
            assert got.reason == "", f"{message.media_type}: refused {got.reason} ({got.why})"
        else:
            assert got.reason == case["expect"]["reason"], message.media_type


REFUSALS = cases("refusal.json")


@pytest.mark.parametrize("case", REFUSALS, ids=[c["id"] for c in REFUSALS])
def test_refusal_vectors(case):
    if "file" in case and case["file"]:
        path = VECTORS / case["file"]
        if not path.exists():
            pytest.skip(f"refers to {case['file']}, which is not vendored")
        raw = path.read_bytes()
    else:
        raw = json.dumps(case["refusal"]).encode("utf-8")
    try:
        reply = parse_reply(Message(raw, MEDIA_TYPE_JSON))
    except InvalidError:
        reply = None
    if reply is None or reply.refusal is None:
        assert not case["expect"]["conformant"], "a conforming refusal was not read"
        return
    verified = verify_refusal_offline(reply.refusal)
    assert verified == (case["expect"]["signature"] == "valid")
    assert (verified and refusal_conforming(reply.refusal)) == case["expect"]["conformant"]


class VectorView:
    """resolution.json's declared responder state."""

    def __init__(self, state: dict):
        self.state = state
        self._anchors = [_decide.AnchorView(a["digest"], a["size"], parse_ns(a["issued_at"])) for a in state["anchors"]]

    def anchors(self):
        return self._anchors

    def log_entries(self):
        return self.state["log_size"]

    def leaf_of(self, digest):
        records = self.state["records"]
        return records.index(digest) if digest in records else None

    def servable(self, leaf):
        return leaf < len(self.state["records"])

    def _positions(self, digests):
        return [self.leaf_of(d) for d in digests if self.leaf_of(d) is not None]

    def correlated(self, correlation_id):
        return self._positions(self.state["correlations"].get(correlation_id, []))

    def citing(self, half):
        return self._positions(self.state["exchange_citations"].get(half, []))

    def lapsed(self, subject, now):
        return False


def test_resolution_vectors():
    corpus = json.loads((VECTORS / "resolution.json").read_text(encoding="utf-8"))
    state = corpus["responder"]
    assert state["supported_derivations"] == {"history_card/1": ["full_history", "checkpoints"]}
    view = VectorView(state)
    now = parse_ns("2026-09-28T12:00:00Z")
    ran = skipped = 0
    for case in corpus["cases"]:
        overrides = case.get("responder_overrides") or {}
        if overrides.get("positional_ordering") is False:
            # A Checkpointed Local Log always defines positions.
            skipped += 1
            continue
        uniform = overrides.get("uniform_policy_declined", state["uniform_policy_declined"])
        check = read_request(Message(case["request_json"].encode("utf-8"), MEDIA_TYPE_JSON))
        if check.reason:
            got = check.reason
        else:
            policy = _decide.Policy(derivation=lambda name: None, uniform=uniform,
                                    may_issue=state["issues_new_commitments"])
            d = _decide.decide(view, check.request, policy, now)
            got = policy.disclose("coverage_unsatisfiable") if d.need_anchor else d.reason
        want = "" if case["expect"]["answer"] == "artifact" else case["expect"]["reason"]
        assert got == want, case["id"]
        ran += 1
    assert (ran, skipped) == (18, 1)
