"""ER -03 §8.1.2: history_card/2, checked against the vectors Go writes
(testdata/history-card-2) and built from a Python book."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import evidencebook as eb
from evidencebook.book import (
    DEFAULT_HISTORY_WINDOW,
    check_history_card_2,
    history_card_positions,
)
from evidencebook.staple import Staple, check_staple, staple

from test_book import Clock, book

DIR = Path(__file__).resolve().parents[2] / "testdata" / "history-card-2"
STAPLES = json.loads((DIR / "staples.json").read_text())


def test_positions_out_of_window_are_none() -> None:
    assert history_card_positions(10, 0) is None
    assert history_card_positions(10, 65) is None
    assert history_card_positions(10, 3) == [0, 2, 6, 8, 9, 10]


@pytest.mark.parametrize("v", STAPLES, ids=lambda v: v["name"])
def test_check_staple_agrees_with_go(v: dict) -> None:
    s = Staple(eb.Message(v["request"].encode()), eb.Reply(eb.Message(v["reply"].encode())))
    if not v["pass"]:
        with pytest.raises(eb.InvalidError):
            check_staple(s, v["holder"])
        return
    got = check_staple(s, v["holder"])
    assert (got.nonce, got.anchor) == (v["nonce"], v["anchor"])


def many(n: int, window: int = 3, max_answer_items: int = 10_000):
    b = book(Clock(), "card2", responder=eb.ResponderPolicy(
        refusal_policy=eb.REFUSAL_POLICY_SPECIFIC, history_window=window, max_answer_items=max_answer_items))
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.checkpoint()
    for _ in range(n):
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
        b.checkpoint()
    return b


def ask(b, derivation: str, form: str = "checkpoints"):
    pin = b.checkpoints()[-1].anchor_digest
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(
        eb.Subject(form), eb.Coverage(expected_pin=pin), derivation=derivation)))
    return b.respond(request)[0]


@pytest.mark.parametrize("form", ["checkpoints", "full_history"])
def test_a_stranger_is_answered_at_p(form: str) -> None:
    b = many(20)
    e = ask(b, eb.DERIVATION_HISTORY_CARD_2, form).envelope
    assert e is not None
    card = json.loads(e.artifact)
    positions = [entry["position"] for entry in card["history"]]
    assert card["window"] == 3 and positions == history_card_positions(20, 3)
    check_history_card_2(e.artifact, e.anchor_digest)


def test_a_book_over_the_cap_for_card_one_still_serves_card_two() -> None:
    b = many(12, window=4, max_answer_items=5)
    assert ask(b, eb.DERIVATION_HISTORY_CARD).refusal is not None
    e = ask(b, eb.DERIVATION_HISTORY_CARD_2).envelope
    assert e is not None
    check_history_card_2(e.artifact, e.anchor_digest)


def test_one_checkpoint_lists_position_zero() -> None:
    b = book(Clock(), "one")
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.checkpoint()
    e = ask(b, eb.DERIVATION_HISTORY_CARD_2).envelope
    card = json.loads(e.artifact)
    assert [x["position"] for x in card["history"]] == [0] and card["window"] == DEFAULT_HISTORY_WINDOW
    check_history_card_2(e.artifact, e.anchor_digest)


def test_card_two_over_another_subject_is_no_such_subject() -> None:
    b = many(3)
    pin = b.checkpoints()[-1].anchor_digest
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(
        eb.Subject("range", (0, 1)), eb.Coverage(expected_pin=pin), derivation=eb.DERIVATION_HISTORY_CARD_2)))
    assert b.respond(request)[0].refusal.reason == "no_such_subject"


@pytest.mark.parametrize("window", [-1, 65, 1000])
def test_a_window_out_of_range_is_refused(window: int) -> None:
    with pytest.raises(eb.InvalidError):
        eb.ResponderPolicy(history_window=window).validate()


def test_a_python_book_staples_card_two_and_grows_no_record() -> None:
    b = many(20)
    size = b.size()
    s = staple(b, eb.DERIVATION_HISTORY_CARD_2)
    assert b.size() == size
    got = check_staple(s, b.key_id)
    assert got.derivation == eb.DERIVATION_HISTORY_CARD_2
    with pytest.raises(eb.InvalidError):
        check_staple(s, "00" * 32)


def edit(artifact: bytes, change) -> bytes:
    card = json.loads(artifact)
    change(card)
    return json.dumps(card, separators=(",", ":")).encode()


@pytest.mark.parametrize("name,change,match", [
    ("window zero", lambda c: c.update(window=0), "window_out_of_range"),
    ("window 65", lambda c: c.update(window=65), "window_out_of_range"),
    ("window text", lambda c: c.update(window="3"), "card_malformed"),
    ("window changed", lambda c: c.update(window=5), "position_missing"),
    ("no window", lambda c: c.pop("window"), "members"),
    ("no history", lambda c: c.update(history=[]), "positions_not_increasing_from_zero"),
    ("first not zero", lambda c: c["history"][0].update(position=1), "positions_not_increasing_from_zero"),
    ("position missing", lambda c: c["history"].pop(2), "position_missing"),
    ("position negative", lambda c: c["history"][1].update(position=-1), "card_malformed"),
    ("receipts not a list", lambda c: c["history"][1].update(receipts="x"), "card_malformed"),
    ("first carries a proof", lambda c: c["history"][0].update(consistency_from_previous="00"), "proof_invalid"),
    ("later without a proof", lambda c: c["history"][2].update(consistency_from_previous=None), "proof_invalid"),
    ("another derivation", lambda c: c.update(derivation=eb.DERIVATION_HISTORY_CARD), "derivation"),
    ("dropped last", lambda c: c.update(history=c["history"][:-1]), "position_missing"),
])
def test_the_card_checks_refuse(name: str, change, match: str) -> None:
    b = many(20)
    e = ask(b, eb.DERIVATION_HISTORY_CARD_2).envelope
    with pytest.raises(eb.InvalidError, match=match):
        check_history_card_2(edit(e.artifact, change), e.anchor_digest)
    with pytest.raises(eb.InvalidError):
        check_history_card_2(e.artifact, "00" * 32)


def ranged(b, a: int, z: int, derivation: str | None = None):
    pin = b.checkpoints()[-1].anchor_digest
    request = eb.Message(eb.encode_request(eb.EvidenceRequest(
        eb.Subject("checkpoints", (a, z)), eb.Coverage(expected_pin=pin), derivation=derivation)))
    return b.respond(request)[0]


@pytest.mark.parametrize("a,z", [(4, 12), (0, 20), (7, 7)])
def test_a_ranged_checkpoints_request_is_answered_with_entries_alone(a: int, z: int) -> None:
    b = many(20)
    e = ranged(b, a, z).envelope
    assert e is not None
    answer = json.loads(e.artifact)
    assert set(answer) == {"history", "consistency_to_anchor"} and len(answer["history"]) == z - a + 1
    assert (answer["consistency_to_anchor"] is None) == (z == 20)
    for i, entry in enumerate(answer["history"]):
        assert entry["position"] == a + i and (entry["consistency_from_previous"] is None) == (i == 0)
        assert entry["receipts"] == []


def test_a_ranged_checkpoints_request_is_refused_by_reason() -> None:
    b = many(20)
    assert ranged(b, 5, 21).refusal.reason == "coverage_unsatisfiable"
    for derivation in (eb.DERIVATION_HISTORY_CARD, eb.DERIVATION_HISTORY_CARD_2):
        assert ranged(b, 1, 2, derivation).refusal.reason == "no_such_subject"
    capped = many(8, max_answer_items=3)
    assert ranged(capped, 2, 4).envelope is not None
    assert ranged(capped, 0, 7).refusal is not None


def test_a_requester_verifies_a_ranged_answer_and_a_card() -> None:
    from evidencebook.book import _verify_anchor_inclusion
    b = many(20)
    key = b.checkpoints()[0].key_id
    e = ranged(b, 4, 12).envelope
    req = eb.EvidenceRequest(eb.Subject("checkpoints", (4, 12)), eb.Coverage(expected_pin=e.anchor_digest))
    _verify_anchor_inclusion(req, e, key)
    with pytest.raises(eb.InvalidError):
        _verify_anchor_inclusion(req, e, "00" * 32)
    wrong = eb.EvidenceRequest(eb.Subject("checkpoints", (4, 13)), eb.Coverage(expected_pin=e.anchor_digest))
    with pytest.raises(eb.InvalidError):
        _verify_anchor_inclusion(wrong, e, key)
    card = ask(b, eb.DERIVATION_HISTORY_CARD_2).envelope
    _verify_anchor_inclusion(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(expected_pin=card.anchor_digest),
                                                derivation=eb.DERIVATION_HISTORY_CARD_2), card, key)


def test_a_ranged_answer_must_reach_its_anchor() -> None:
    from dataclasses import replace
    from evidencebook.book import _verify_checkpoint_range
    b = many(20)
    key = b.checkpoints()[0].key_id
    e = ranged(b, 4, 12).envelope
    other = ranged(b, 4, 14).envelope
    req = eb.EvidenceRequest(eb.Subject("checkpoints", (4, 12)), eb.Coverage(expected_pin=e.anchor_digest))
    _verify_checkpoint_range(req, e, key)

    def tampered(**changes):
        card = json.loads(e.artifact)
        card.update(changes)
        return replace(e, artifact=json.dumps(card, separators=(",", ":")).encode())

    for bad in (tampered(consistency_to_anchor=None),
                tampered(consistency_to_anchor=json.loads(other.artifact)["consistency_to_anchor"]),
                replace(e, verification=())):
        with pytest.raises(eb.InvalidError):
            _verify_checkpoint_range(req, bad, key)


def test_freshness_and_unread_receipts_are_refused() -> None:
    from dataclasses import replace
    from evidencebook.book import _verify_anchor_inclusion, _verify_checkpoint_range
    b = many(20)
    key = b.checkpoints()[0].key_id
    e = ranged(b, 4, 12).envelope
    req = eb.EvidenceRequest(eb.Subject("checkpoints", (4, 12)), eb.Coverage(expected_pin=e.anchor_digest))
    with pytest.raises(eb.InvalidError, match="freshness"):
        _verify_checkpoint_range(req, replace(e, freshness=1 << 40), key)
    card = ask(b, eb.DERIVATION_HISTORY_CARD_2).envelope
    card_req = eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(expected_pin=card.anchor_digest),
                                  derivation=eb.DERIVATION_HISTORY_CARD_2)
    _verify_anchor_inclusion(card_req, card, key)
    with pytest.raises(eb.InvalidError, match="freshness"):
        _verify_anchor_inclusion(card_req, replace(card, freshness=1 << 40), key)
    body = json.loads(e.artifact)
    body["history"][1]["receipts"] = [{"checkpoint": "00", "witness": "w", "sig": "00"}]
    with pytest.raises(eb.InvalidError, match="receipts"):
        _verify_checkpoint_range(req, replace(e, artifact=json.dumps(body, separators=(",", ":")).encode()), key)
