# SPDX-License-Identifier: Apache-2.0
"""A sent or book pair whose members are not exactly its own is refused
(evidencebook#559), held to Go by testdata/deal-v1/pair-names, which Go's
TestPairNamesVectors writes."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook.deal import v1
from evidencebook.deal.check import StageError

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "pair-names" / "vectors.json"
CASES = json.loads(VECTORS.read_text())["cases"]


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_pair_names_vector(case):
    pair = bytes.fromhex(case["pair"])
    read = v1.verify_sent if case["kind"] == "sent" else v1.verify_book
    if case["valid"]:
        read(pair)
        return
    with pytest.raises(StageError) as exc:
        read(pair)
    assert exc.value.stage == case["stage"]
    assert "its members are not exactly" in str(exc.value)
