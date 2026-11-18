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
    ({"offer": None}, "a record has an x-deal-v1 block and a body"),
    ({"proof_held": "yes"}, "cannot unmarshal string into a Go value of type bool"),
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


# -- books read as Go's json.Unmarshal reads them (#549 review r1) ---------------------
# Each outcome below is Go's, from the deal-v1 runner on the same input.

ROW = BASE["offer"]["body"]["terms_rows"]["rows"][0]["id"]


@pytest.mark.parametrize("change", [
    {"payments": {ROW: {"reason": 7}}},
    {"payments": {ROW: {"excluded": "abc"}}},
    {"payments": {ROW: {"settlement": 5}}},
    {"rulings_unnamed": 1 << 63},
    {"case_book": [{"record_id": "x", "record": None}]},
    {"own": "x"},
])
def test_build_status_refuses_what_go_s_decode_refuses(change):
    with pytest.raises(v1.StatusError):
        v1.build_status(books(**change))


@pytest.mark.parametrize("change", [
    {"payments": {ROW: None}},
    {"case_book": (BASE.get("case_book") or []) + [{"record_id": "x"}]},
    {"conflicting_final_rulings": [None]},
])
def test_build_status_takes_what_go_s_decode_takes(change):
    assert v1.build_status(books(**change)) == v1.build_status(books())


def test_a_null_bool_is_its_zero_value():
    assert v1.build_status(books(proof_held=None)) == v1.build_status(books(proof_held=False))


def test_status_books_match_members_in_any_case():
    assert v1.build_status({k.upper(): v for k, v in BASE.items()}) == v1.build_status(BASE)


def test_a_report_s_books_and_copy_are_read_as_go_reads_them():
    rb = _copy.deepcopy(REPORT["books"])
    rb["Stamps"] = {"999": None}
    assert v1.build_report(rb, REPORT["copy"]) == v1.build_report(REPORT["books"], REPORT["copy"])
    rb = _copy.deepcopy(REPORT["books"])
    rb["own"] = rb.pop("Own")
    assert v1.build_report(rb, REPORT["copy"]) == v1.build_report(REPORT["books"], REPORT["copy"])
    rb = _copy.deepcopy(REPORT["books"])
    rb["Counterpart"] = {"counted": "bad"}
    adj = dict(REPORT["copy"], audience="adjudicator", referee="Lamp Court")
    with pytest.raises(v1.StatusError):
        v1.build_report(rb, adj)
    rb["Counterpart"] = None
    rb["Stamps"] = {"x": "2026-11-01T00:00:00Z"}
    with pytest.raises(v1.StatusError):
        v1.build_report(rb, REPORT["copy"])


@pytest.mark.parametrize("change", [{"audience": 5}, {"nonce": 5}, {"as_of": 5}, {"referee": 5}, {"text": []},
                                    {"text": "x"}])
def test_a_copy_member_of_another_type_is_refused(change):
    with pytest.raises(v1.StatusError):
        v1.build_report(REPORT["books"], dict(REPORT["copy"], **change))


def test_a_copy_s_text_is_the_report_s_text():
    got = v1.build_report(REPORT["books"], dict(REPORT["copy"], text={"line": "x"}))
    assert got["report"] == {"line": "x"}


def test_row_inputs_reads_the_books_checks():
    b = {k: BASE[k] for k in ("offer", "own", "counterpart", "case_book") if k in BASE}
    for k in ("own", "counterpart", "case_book"):
        b[k] = [{"record": r["record"], "record_id": r["record_id"],
                 "time": r["time"] if isinstance(r.get("time"), str) else None} for r in b.get(k) or []]
    recomputed = [r["id"] for r in BASE["offer"]["body"]["terms_rows"]["rows"] if r.get("recomputed")]
    assert recomputed
    ins = v1.row_inputs(dict(b, checks={recomputed[0]: "fail"}), "2026-11-26T12:00:00Z")
    assert [i.get("check") for i in ins if i["row"]["id"] == recomputed[0]] == ["fail"]
    with pytest.raises(v1.StatusError):
        v1.row_inputs(dict(b, checks={"no_such_row": "fail"}), "2026-11-26T12:00:00Z")


# -- a later member decodes into what an earlier one left (#549 review r2) -------------
# Each outcome below is Go's, from the deal-v1 runner on the same input.


def test_a_later_spelling_of_a_pointer_merges_into_it():
    rb = _copy.deepcopy(REPORT["books"])
    rb["Fact"] = {"current": True}
    rb["fact"] = {}
    assert v1.build_report(rb, REPORT["copy"])["status_fact"] == {"current": True}


def test_a_later_null_books_keeps_the_books():
    rb = _copy.deepcopy(REPORT["books"])
    rb["books"] = None
    assert v1.build_report(rb, REPORT["copy"]) == v1.build_report(REPORT["books"], REPORT["copy"])


def test_a_later_null_stamp_clears_it():
    with pytest.raises(v1.StatusError):
        v1.status_time({"stamp": 1, "STAMP": None})
    assert v1.status_time({"STAMP": None, "stamp": 2}) == {"stamp": 2}


def test_text_entries_merge_across_spellings():
    got = v1.build_report(REPORT["books"], dict(REPORT["copy"], text={"a": 1}, TEXT={"b": 2}))
    assert got["report"] == {"a": 1, "b": 2}


@pytest.mark.parametrize("settlement", [{"terms": 7}, {"payment_state": 1}, {"differs": "x"}, {"iso20022": []}, 5])
def test_a_settlement_of_another_type_is_refused(settlement):
    with pytest.raises(v1.StatusError):
        v1.build_status(books(payments={ROW: {"settlement": settlement}}))


def test_a_settlement_of_its_types_is_taken():
    s = {"terms": "t", "payment_state": None, "differs": ["a"], "iso20022": {"x": "y"}, "delivery_state": "d"}
    assert v1.build_status(books(payments={ROW: {"settlement": s}}))["rows"]
