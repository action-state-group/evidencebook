"""evidencebook#275: a staple, checked against the vectors Go writes
(testdata/staple/staples.json) and built from a Python book."""

from __future__ import annotations

import json
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
    # The Python responder does not answer standard_history/1 yet; the book
    # refuses its own request rather than stapling something else.
    b = book(Clock(), "nostandard")
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    with pytest.raises(eb.InvalidError, match="refused its own"):
        staple(b, DERIVATION_STANDARD_HISTORY)
