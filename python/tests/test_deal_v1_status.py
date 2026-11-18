# SPDX-License-Identifier: Apache-2.0
"""x-deal-v1 status values (evidencebook#461 PR 6d-2), held to Go by
testdata/deal-v1/status, which Go's TestStatusVectors writes."""
from __future__ import annotations

import json
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs

from evidencebook import row_status
from evidencebook.deal.v1 import status

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "status" / "vectors.json"
CASES = json.loads(VECTORS.read_text())["cases"]


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_status_vectors(case):
    v = status.build_status(case["books"])
    assert jcs(v) == jcs(case["status"])
    stamps = {int(k): t for k, t in (case.get("stamps") or {}).items()}
    got = {row["id"]: row_status.compute(status.resolve_row(row, case["at"], stamps)).status for row in v["rows"]}
    assert got == case["rows"]


REPORTS = json.loads((VECTORS.parent.parent / "report" / "vectors.json").read_text())["cases"]


@pytest.mark.parametrize("case", REPORTS, ids=[c["name"] for c in REPORTS])
def test_report_vectors(case):
    from evidencebook.deal.v1 import report

    got = report.build_report(case["books"], case["copy"])
    assert jcs(got) == jcs(case["report"])


# -- refusals and edges ----------------------------------------------------------------

import copy as _copy

from evidencebook.deal import v1

BASE = CASES[0]["books"]
REPORT = REPORTS[0]


def books(**change):
    b = _copy.deepcopy(BASE)
    b.update(change)
    return b


@pytest.mark.parametrize("change, detail", [
    ({"own": []}, "the holder's book is empty"),
    ({"offer": None}, "the books hold no offer"),
    ({"proof_held": "yes"}, "proof_held is a boolean"),
    ({"payments": {"no_such_row": {"check": "pass"}}}, "which the terms do not have"),
])
def test_build_status_refuses(change, detail):
    with pytest.raises(v1.StatusError) as exc:
        v1.build_status(books(**change))
    assert detail in str(exc.value)


def test_the_offer_must_be_a_propose():
    with pytest.raises(v1.StatusError) as exc:
        v1.build_status(books(offer=BASE["own"][0]["record"]))
    assert str(exc.value) == "xdealv1: status: the offer is a agree record"


@pytest.mark.parametrize("t, want", [
    (None, None), ("", None), ("2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z"), ({"stamp": 3}, {"stamp": 3}),
])
def test_status_time(t, want):
    assert v1.status_time(t) == want


@pytest.mark.parametrize("t", [{"stamp": 0}, {"stamp": "1"}, {"stamp": 1, "x": 2}, 5, [], {"stamp": True}])
def test_status_time_refuses(t):
    with pytest.raises(v1.StatusError):
        v1.status_time(t)


@pytest.mark.parametrize("owed, want", [
    ({"amount_minor": 6000, "currency": "USD"}, {"value": "6000", "assetCode": "USD", "assetScale": 2}),
    ({"amount_minor": 6000, "currency": "XXX"}, None),
    ({"amount_minor": -1, "currency": "USD"}, None),
    ({"amount_minor": 6000, "Currency": "USD"}, None),
    ({"amount_minor": 6000, "currency": "USD", "x": 1}, None),
    ({"amount_minor": None, "currency": "USD"}, None),
])
def test_owed_amount(owed, want):
    assert v1.owed_amount(owed) == want


@pytest.mark.parametrize("change, detail", [
    ({"audience": "other"}, 'audience "other" is not a copy audience'),
    ({"nonce": "AB"}, "the nonce must be 64 lowercase hex"),
    ({"audience": "adjudicator", "referee": ""}, "names the referee it is handed to"),
    ({"referee": "Lamp Court"}, "only an adjudicator's copy names a referee"),
    ({"as_of": "2026-11-01T12:00:00+01:00"}, "is not RFC 3339 UTC to the second"),
    ({"as_of": "2026-10-01T00:00:00Z"}, "after as_of 2026-10-01T00:00:00Z"),
])
def test_build_report_refuses(change, detail):
    c = dict(REPORT["copy"])
    c.update(change)
    with pytest.raises(v1.StatusError) as exc:
        v1.build_report(REPORT["books"], c)
    assert detail in str(exc.value)


def test_report_payload_is_the_reports_jcs():
    r = v1.build_report(REPORT["books"], REPORT["copy"])
    assert v1.report_payload(r) == jcs(REPORT["report"])
