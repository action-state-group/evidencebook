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
POSITIONS = json.loads((DIR / "positions.json").read_text())


@pytest.mark.parametrize("v", POSITIONS, ids=lambda v: f"P({v['n']},{v['window']})")
def test_positions_agree_with_go(v: dict) -> None:
    assert history_card_positions(v["n"], v["window"]) == v["positions"]


def test_positions_out_of_window_are_none() -> None:
    assert history_card_positions(10, 0) is None
    assert history_card_positions(10, 65) is None
    assert len(history_card_positions(1_000_000, 16)) <= 16 + 19 + 2


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
    ("window zero", lambda c: c.update(window=0), "window"),
    ("window 65", lambda c: c.update(window=65), "window"),
    ("window text", lambda c: c.update(window="3"), "window"),
    ("window changed", lambda c: c.update(window=5), "P\\(n, window\\)"),
    ("no window", lambda c: c.pop("window"), "members"),
    ("no history", lambda c: c.update(history=[]), "no history"),
    ("first not zero", lambda c: c["history"][0].update(position=1), "strictly increase"),
    ("position missing", lambda c: c["history"].pop(2), "P\\(n, window\\)"),
    ("position negative", lambda c: c["history"][1].update(position=-1), "unsigned"),
    ("receipts not a list", lambda c: c["history"][1].update(receipts="x"), "receipts"),
    ("first carries a proof", lambda c: c["history"][0].update(consistency_from_previous="00"), "consistency proof"),
    ("later without a proof", lambda c: c["history"][2].update(consistency_from_previous=None), "consistency proof"),
    ("another derivation", lambda c: c.update(derivation=eb.DERIVATION_HISTORY_CARD), "derivation"),
    ("dropped last", lambda c: c.update(history=c["history"][:-1]), None),
])
def test_the_card_checks_refuse(name: str, change, match: str) -> None:
    b = many(20)
    e = ask(b, eb.DERIVATION_HISTORY_CARD_2).envelope
    with pytest.raises(eb.InvalidError, match=match):
        check_history_card_2(edit(e.artifact, change), e.anchor_digest)
    with pytest.raises(eb.InvalidError):
        check_history_card_2(e.artifact, "00" * 32)
