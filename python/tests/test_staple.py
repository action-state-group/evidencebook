"""evidencebook#275: a staple, checked against the vectors Go writes
(testdata/staple/staples.json) and built from a Python book."""

from __future__ import annotations

import json
from datetime import timedelta
from pathlib import Path

import pytest

import evidencebook as eb
from evidencebook.staple import DERIVATION_STANDARD_HISTORY, Staple, check_staple, staple

from test_book import Clock, book

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "staple" / "staples.json"


def _vectors() -> list[dict]:
    return json.loads(VECTORS.read_text())


@pytest.mark.parametrize("v", _vectors(), ids=lambda v: v["name"])
def test_check_staple_agrees_with_go(v: dict) -> None:
    s = Staple(eb.Message(v["request"].encode()), eb.Reply(eb.Message(v["reply"].encode())))
    if not v["pass"]:
        with pytest.raises(eb.InvalidError):
            check_staple(s, v["holder"])
        return
    got = check_staple(s, v["holder"])
    assert (got.nonce, got.anchor) == (v["nonce"], v["anchor"])


def test_a_python_book_staples_its_history_card() -> None:
    b = book(Clock(), "stapler")
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.checkpoint()  # a Python checkpoint seals the book's index roots
    size = b.size()
    first, second = staple(b, eb.DERIVATION_HISTORY_CARD), staple(b, eb.DERIVATION_HISTORY_CARD)
    assert b.size() == size  # a public card grows no record
    a, c = check_staple(first, b.key_id), check_staple(second, b.key_id)
    assert a.derivation == eb.DERIVATION_HISTORY_CARD and a.anchor == b.checkpoints()[-1].anchor_digest
    assert a.nonce != c.nonce
    with pytest.raises(eb.InvalidError):
        check_staple(first, "00" * 32)


@pytest.mark.parametrize("derivation", ["", "served_summary/1", "exchange_corroboration/1"])
def test_staple_is_capped_at_the_standard_card(derivation: str) -> None:
    b = book(Clock(), "capped")
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    with pytest.raises(eb.InvalidError):
        staple(b, derivation)


def test_standard_history_needs_a_responder_that_serves_it() -> None:
    # Without a StandardHistorySource the book refuses its own request
    # rather than stapling something else.
    b = book(Clock(), "nostandard")
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    with pytest.raises(eb.InvalidError, match="refused its own"):
        staple(b, DERIVATION_STANDARD_HISTORY)


def test_the_holders_staple_is_not_counted_by_the_limiter() -> None:
    b = book(Clock(), "limited", responder=eb.ResponderPolicy(limiter=eb.WindowLimiter(1, timedelta(hours=1))))
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    pin = b.checkpoint().anchor_digest
    stranger = eb.Message(eb.encode_request(eb.EvidenceRequest(eb.Subject("checkpoints"), eb.Coverage(expected_pin=pin),
                                                               derivation=eb.DERIVATION_HISTORY_CARD)))
    assert b.respond(stranger)[0].envelope is not None
    assert b.respond(stranger)[0].refusal is not None  # the strangers' bucket is spent
    for _ in range(2):
        check_staple(staple(b, eb.DERIVATION_HISTORY_CARD), b.key_id)


def test_an_empty_pin_is_refused() -> None:
    v = next(v for v in _vectors() if v["name"] == "history_card/1")
    request = json.loads(v["request"])
    request["coverage"]["expected_pin"] = ""
    with pytest.raises(eb.InvalidError, match="expected_pin is not a digest"):
        check_staple(Staple(eb.Message(json.dumps(request).encode()), eb.Reply(eb.Message(v["reply"].encode()))),
                     v["holder"])
