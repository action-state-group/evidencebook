# SPDX-License-Identifier: Apache-2.0
"""evidencebook#332: the evidence facade over the book, the request interaction and the staple."""
from __future__ import annotations

import base64
import hashlib
import json
from pathlib import Path
from datetime import timedelta

import pytest
from agent_action_capsule.evidence_request import sign_refusal

import evidencebook as eb
from evidencebook import evidence

from evidencebook.signer import EvidenceSigner
from test_book import Clock, PEERS, book, key, keys_of, resign


@pytest.fixture
def responder() -> eb.Book:
    clock = Clock()
    b = book(clock, "responder")
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.checkpoint()
    return b


def request_for(r: eb.Book) -> eb.EvidenceRequest:
    return eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(expected_pin=r.checkpoints()[-1].anchor_digest))


def test_seal_commits_a_record() -> None:
    b = book(Clock())
    record = evidence.seal(b, "observation", {"k": "v"}, subject_ref="s")
    assert b.get(record.record_id).header.statement == {"k": "v"} and b.size() == 1
    assert record.header.epistemic_type == eb.OBSERVED_EVENT


def test_ask_an_artifact_is_verified(responder: eb.Book) -> None:
    q = book(Clock(), "requester")
    out = evidence.ask(q, request_for(responder), "r", keys_of(responder), lambda m: responder.respond(m, PEERS)[0].message)
    assert out.state == evidence.ARTIFACT and out.artifact and out.record is not None


def test_ask_a_refusal_is_a_refusal(responder: eb.Book) -> None:
    q = book(Clock(), "requester")
    req = eb.EvidenceRequest(eb.Subject("record", "c" * 64), eb.Coverage(expected_pin=responder.checkpoints()[-1].anchor_digest))
    out = evidence.ask(q, req, "r", keys_of(responder), lambda m: responder.respond(m, PEERS)[0].message)
    assert (out.state, out.reason) == (evidence.REFUSAL, "no_such_subject")


@pytest.mark.parametrize("reason", ["capacity_exceeded", "request_unauthenticated"])
def test_ask_capacity_and_unauthenticated_are_status_unknown(reason: str, responder: eb.Book) -> None:
    q = book(Clock(), "requester")

    def refuse(m: eb.Message) -> eb.Message:
        digest = hashlib.sha256(m.octets).hexdigest()
        refusal = sign_refusal(digest, reason, signer=EvidenceSigner(responder._signer), issued_at="2026-10-31T12:00:00Z")
        return eb.request.encode_refusal(refusal, eb.MEDIA_TYPE_JSON).message

    out = evidence.ask(q, request_for(responder), "r", keys_of(responder), refuse)
    assert (out.state, out.reason) == (evidence.STATUS_UNKNOWN, reason) and out.record is not None


def test_ask_a_failed_artifact_check_is_its_own_state(responder: eb.Book) -> None:
    q = book(Clock(), "requester")

    def tamper(m: eb.Message) -> eb.Message:
        envelope = json.loads(responder.respond(m, PEERS)[0].message.octets)
        artifact = base64.urlsafe_b64decode(envelope["artifact"] + "==") + b" "
        envelope["artifact"] = base64.urlsafe_b64encode(artifact).rstrip(b"=").decode()
        return eb.Message(resign(envelope, key("responder signer")))

    out = evidence.ask(q, request_for(responder), "r", keys_of(responder), tamper)
    assert out.state == evidence.ARTIFACT_FAILED and out.reason.startswith("artifact_digest") and not out.artifact


def test_ask_a_nonconformant_refusal_is_its_own_state(responder: eb.Book) -> None:
    q = book(Clock(), "requester")

    def refuse(m: eb.Message) -> eb.Message:  # signed, but issued_at is not the profile's seconds form
        refusal = sign_refusal(hashlib.sha256(m.octets).hexdigest(), "policy_declined",
                               signer=EvidenceSigner(responder._signer), issued_at="2026-10-31T12:00:00.5Z")
        return eb.request.encode_refusal(refusal, eb.MEDIA_TYPE_JSON).message

    out = evidence.ask(q, request_for(responder), "r", keys_of(responder), refuse)
    assert (out.state, out.reason) == (evidence.REFUSAL_NONCONFORMANT, "policy_declined")


def test_ask_no_answer_is_pending_then_a_recorded_absence() -> None:
    clock = Clock()
    q = book(clock, "requester")
    r = book(clock, "r")
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    r.checkpoint()
    def silent(m):
        raise OSError("down")
    assert evidence.ask(q, request_for(r), "r", keys_of(r), silent).state == evidence.PENDING
    def slow(m):
        clock.at += timedelta(minutes=5)  # the waiting window runs out while the transport waits
    out = evidence.ask(q, request_for(r), "r", keys_of(r), slow, window_end=clock.at + timedelta(minutes=3))
    assert out.state == evidence.ABSENCE and out.record.header.record_type == eb.RECORD_TYPE_ABSENCE


def test_a_pending_outcome_is_closed_without_asking_again() -> None:
    clock = Clock()
    q = book(clock, "requester")
    r = book(clock, "r")
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    r.checkpoint()
    pending = evidence.ask(q, request_for(r), "r", keys_of(r), lambda m: None)
    assert pending.state == evidence.PENDING and pending.request_id
    size = q.size()
    clock.at += timedelta(minutes=5)
    closed = evidence.ask(q, pending, "r", keys_of(r), lambda m: pytest.fail("sent again"), window_end=clock.at)
    assert closed.state == evidence.ABSENCE and closed.request_id == pending.request_id and q.size() == size + 1
    with pytest.raises(eb.InvalidError):
        evidence.ask(q, pending, "r", keys_of(r), lambda m: None)  # no window_end


def test_a_window_that_is_still_open_stays_pending_and_resumes() -> None:
    clock = Clock()
    q = book(clock, "requester")
    r = book(clock, "r")
    r.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    r.checkpoint()
    pending = evidence.ask(q, request_for(r), "r", keys_of(r), lambda m: None, window_end=clock.at)  # under the minimum
    assert pending.state == evidence.PENDING and pending.request_id
    clock.at += timedelta(minutes=5)
    assert evidence.ask(q, pending, "r", keys_of(r), lambda m: None, window_end=clock.at).state == evidence.ABSENCE


def test_a_refused_reply_keeps_the_open_requests_id(responder: eb.Book) -> None:
    q = book(Clock(), "requester")
    other = book(Clock(), "other")  # signs under a key the requester did not pin
    other.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    other.checkpoint()
    with pytest.raises(eb.InvalidError) as caught:
        evidence.ask(q, request_for(responder), "r", keys_of(responder), lambda m: other.respond(m, PEERS)[0].message)
    assert q.get(caught.value.request_id).header.record_type == eb.RECORD_TYPE_REQUEST


def test_history_reads_a_staple(responder: eb.Book) -> None:
    h = evidence.history(eb.staple(responder, eb.DERIVATION_HISTORY_CARD), responder.key_id)
    assert h.derivation == eb.DERIVATION_HISTORY_CARD and h.properties is None
    with pytest.raises(eb.InvalidError):
        evidence.history(eb.staple(responder, eb.DERIVATION_HISTORY_CARD), "00" * 32)


def test_history_reads_a_standard_card_from_the_vectors() -> None:
    vectors = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "staple" / "staples.json").read_text())
    v = next(v for v in vectors if v["name"] == "standard_history/1")
    h = evidence.history(eb.Staple(eb.Message(v["request"].encode()), eb.Reply(eb.Message(v["reply"].encode()))), v["holder"])
    assert h.derivation == eb.DERIVATION_STANDARD_HISTORY and h.properties["card_form"] == "PASS"
    assert "FAIL" not in h.properties.values()


@pytest.mark.parametrize("call", [lambda: evidence.serve(None, None), lambda: evidence.find("u"),
                                  lambda: evidence.evaluate(None, None)])
def test_unavailable_calls_name_what_they_wait_on(call) -> None:
    with pytest.raises(evidence.NotYetAvailable, match="#"):
        call()
