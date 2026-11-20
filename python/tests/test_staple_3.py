"""standard_history/3 staples (evidencebook#579), checked against the vectors Go
writes (testdata/staple-3). A book builds no /3 card, so there is no builder."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import evidencebook as eb
from evidencebook.staple import DERIVATION_STANDARD_HISTORY_3, Staple, check_staple, staple

from test_book import Clock, book

STAPLES = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "staple-3" / "staples.json").read_text())


@pytest.mark.parametrize("v", STAPLES, ids=lambda v: v["name"])
def test_check_staple_agrees_with_go(v: dict) -> None:
    s = Staple(eb.Message(v["request"].encode()), eb.Reply(eb.Message(v["reply"].encode())))
    if not v["pass"]:
        with pytest.raises(eb.InvalidError):
            check_staple(s, v["holder"])
        return
    got = check_staple(s, v["holder"])
    assert (got.derivation, got.nonce, got.anchor) == (DERIVATION_STANDARD_HISTORY_3, v["nonce"], v["anchor"])


def test_a_book_builds_no_standard_history_3_staple() -> None:
    with pytest.raises(eb.InvalidError):
        staple(book(Clock(), "sh3"), DERIVATION_STANDARD_HISTORY_3)
