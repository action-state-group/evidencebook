# SPDX-License-Identifier: Apache-2.0
"""The shared row status vectors (testdata/row-status), as Go and Rust read them."""

import json
from pathlib import Path

import pytest

from evidencebook.row_status import Invalid, compute, parse_input

VECTORS = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "row-status" / "vectors.json").read_text())


@pytest.mark.parametrize("case", VECTORS["cases"], ids=lambda c: c["name"])
def test_case(case):
    got = compute(parse_input(json.dumps(case["input"])))
    assert got.to_json() == case["expect"]


@pytest.mark.parametrize("case", VECTORS["invalid"], ids=lambda c: c["name"])
def test_invalid(case):
    with pytest.raises(Invalid):
        compute(parse_input(json.dumps(case["input"])))


def test_repeated_member_refused():
    raw = '{"at": "2026-11-02T00:00:00Z", "at": "2026-11-03T00:00:00Z", "parties": ["a", "b"], "row": {}}'
    with pytest.raises(Invalid):
        parse_input(raw)


@pytest.mark.parametrize(
    "raw",
    [
        '{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}}]',
        '{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}, "named": {"x": {"latest": "2026-11-02T00:00:00Z"}, "x": {"latest": "2026-11-03T00:00:00Z"}}}',
        '{"at": "2026-11-02T00:00:00Z", "parties": ["\\ud800", "b"], "row": {}}',
        '{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {"id": "\\udc00"}}',
    ],
)
def test_raw_refused(raw):
    with pytest.raises(Invalid):
        compute(parse_input(raw))


def test_compute_without_row_is_invalid():
    with pytest.raises(Invalid):
        compute({"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"]})


@pytest.mark.parametrize(
    "raw",
    [
        '{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}}'.encode("utf-16"),
        b'{"at": "2026-11-02T00:00:00Z", "parties": ["a\xff", "b"], "row": {}}',
        b'{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}, "named": {"\\ud800": {}}}',
    ],
)
def test_raw_bytes_refused(raw):
    with pytest.raises(Invalid):
        compute(parse_input(raw))
