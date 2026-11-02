"""evidencebook#328: a Python book answers ``standard_history/1`` as Go does,
against the vectors Go writes (testdata/standard-history-respond/vectors.json):
the same recipe (keys, clock, records) gives the same reply octets."""

from __future__ import annotations

import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

import pytest

import evidencebook as eb
from evidencebook.staple import check_staple, staple

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "standard-history-respond" / "vectors.json"
V = json.loads(VECTORS.read_text())
EPISTEMIC = {"producer_claim": eb.PRODUCER_CLAIM, "observed_event": eb.OBSERVED_EVENT}


def _host_inputs(anchor: eb.Checkpoint, records: list[eb.Record]) -> tuple[dict, list[dict]]:
    """The test host of Go's ``dealsOf``: one deal per ``deal_terms`` record,
    its statement with the record id as the deal and the commit time as the
    agreement."""
    deals = []
    for r in records:
        if r.header.record_type == "deal_terms":
            deals.append({**r.header.statement, "deal": r.record_id, "agreed_at": r.header.committed_at})
    return {"digest": V["policy_digest"], "window": {"months": V["policy_months"]}}, deals


def _book(holder_kind: str, source: bool) -> eb.Book:
    at = [datetime.fromisoformat(V["start"].replace("Z", "+00:00"))]

    def now() -> datetime:
        at[0] += timedelta(seconds=1)
        return at[0]

    b = eb.Book(V["book_id"], V["operator"], signer=eb.Ed25519Signer(bytes.fromhex(V["signer_seed"])),
                log_id=V["log_id"], log_key=eb.Ed25519Signer(bytes.fromhex(V["log_seed"])), now=now,
                standard_history=eb.StandardHistorySource(holder_kind, _host_inputs) if source else None)
    for e in V["entries"]:
        b.append(eb.Entry(e["record_type"], EPISTEMIC[e["epistemic_type"]],
                          counterparty_ref=e.get("counterparty_ref", ""), statement=e.get("statement")))
    return b


@pytest.mark.parametrize("scenario", V["scenarios"], ids=lambda s: s["name"])
def test_python_answers_as_go_does(scenario: dict[str, Any]) -> None:
    b = _book(scenario["holder_kind"], scenario["source"])
    pin = b.checkpoint().anchor_digest
    assert pin == scenario["pin"]
    for case in scenario["cases"]:
        request = eb.Message(case["request"].encode())
        reply, _ = b.respond(request, eb.RespondOptions(authenticated_requester=case.get("requester", "")))
        assert reply.message.octets.decode() == case["reply"], case["name"]
        if case["outcome"] == "artifact":
            assert reply.envelope is not None and reply.envelope.artifact.decode() == case["artifact"]
        else:
            assert reply.refusal is not None and reply.refusal.reason == case["outcome"]


def test_a_stranger_never_reaches_the_hosts_inputs() -> None:
    read: list[list[str]] = []

    def inputs(anchor, records):
        read.append([r.header.record_type for r in records])
        return _host_inputs(anchor, records)

    b = _book("person", True)
    b._standard_history = eb.StandardHistorySource("person", inputs)
    pin = b.checkpoint().anchor_digest
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(
        eb.Subject("full_history"), eb.Coverage(expected_pin=pin), derivation=eb.DERIVATION_STANDARD_HISTORY)))
    reply, sealed = b.respond(request)
    assert reply.refusal is not None and reply.refusal.reason == "not_authorized" and sealed is None
    assert read == []
    reply, sealed = b.respond(request, eb.RespondOptions(authenticated_requester="mallory"))
    assert reply.envelope is not None and sealed is not None
    # The host reads the servable records the anchor covers, and none of the responder's bookkeeping.
    assert len(read) == 1 and read[0].count("deal_terms") == 3 and "evidence_request_answered" not in read[0]


def test_the_card_does_not_depend_on_who_asks() -> None:
    b = _book("person", True)
    pin = b.checkpoint().anchor_digest
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(
        eb.Subject("full_history"), eb.Coverage(expected_pin=pin), derivation=eb.DERIVATION_STANDARD_HISTORY)))
    cards = {b.respond(request, eb.RespondOptions(authenticated_requester=who,
                                                 policy=eb.SharePolicy(history_segments=tier)))[0].envelope.artifact
             for who in ("carol", "mallory") for tier in ("off", "counterparties", "prospective", "peers")}
    assert len(cards) == 1


def test_inputs_that_make_no_standard_card_are_invalid() -> None:
    def inputs(anchor, records):
        policy, deals = _host_inputs(anchor, records)
        return policy, [{**deals[0], "outcome": "bogus"}]

    b = _book("person", True)
    b._standard_history = eb.StandardHistorySource("person", inputs)
    pin = b.checkpoint().anchor_digest
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(
        eb.Subject("full_history"), eb.Coverage(expected_pin=pin), derivation=eb.DERIVATION_STANDARD_HISTORY)))
    with pytest.raises(eb.InvalidError, match="standard history"):
        b.respond(request, eb.RespondOptions(authenticated_requester="carol"))


@pytest.mark.parametrize("kind", ["agent", ""])
def test_a_source_needs_a_holder_kind_and_inputs(kind: str) -> None:
    with pytest.raises(eb.InvalidError):
        eb.StandardHistorySource(kind, _host_inputs).validate()
    with pytest.raises(eb.InvalidError):
        eb.StandardHistorySource("person", None).validate()  # type: ignore[arg-type]


def test_a_python_book_staples_its_standard_card() -> None:
    b = _book("node", True)
    s = staple(b, eb.DERIVATION_STANDARD_HISTORY)
    assert b.records()[-1].header.record_type == "evidence_request_answered"  # sealed, as Go seals it
    got = check_staple(s, b.key_id)
    assert got.derivation == eb.DERIVATION_STANDARD_HISTORY
    card = json.loads(got.artifact)
    assert card["holder_kind"] == "node" and card["controller_class"] == "node_declared"
