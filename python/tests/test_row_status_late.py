# SPDX-License-Identifier: Apache-2.0
"""row_status.compute_late against testdata/row-status/late.json, which Go's
rowstatus tests read too (Go ``ComputeLate``)."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook import row_status

LATE = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "row-status" / "late.json").read_text())


@pytest.mark.parametrize("case", LATE["cases"], ids=[c["name"] for c in LATE["cases"]])
def test_late_vectors(case):
    assert row_status.compute_late(case["input"]) == case["expect"]


def test_late_is_empty_before_the_window_is_known():
    inp = {"at": "2026-11-06T00:00:00Z", "parties": ["buyer", "seller"],
           "row": {"id": "r", "owed_by": "buyer", "due": {"after": "x"}, "window_end": {"after": "x"}},
           "named": {"x": {"time": None, "latest": "2026-12-01T00:00:00Z"}},
           "statements": [{"side": "buyer", "value": "met", "time": "2026-11-05T00:00:00Z"}]}
    assert row_status.compute_late(inp) == {"late_statements": [], "late_disputes": []}
