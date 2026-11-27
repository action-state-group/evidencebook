# SPDX-License-Identifier: Apache-2.0
"""VerifyReportBundle (evidencebook#598) held to Go: each case of
testdata/deal-v1/report-bundle/vectors.json, which Go's TestReportBundleVectors
writes, gives Go's signer, or Go's stage and the fragment of Go's refusal; and
the book a shelf gives with its report (Go's ReportWithBook)."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook.deal.check import StageError
from evidencebook.deal import v1

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "report-bundle" / "vectors.json"
CASES = json.loads(VECTORS.read_text())["cases"]


def _ext(case: dict) -> bytes:
    return json.dumps(case["extension"], separators=(",", ":"), ensure_ascii=False).encode()


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_report_bundle_vector(case):
    report, ext = bytes.fromhex(case["report"]), _ext(case)
    want = case["want"]
    if "key_id" in want:
        assert v1.verify_report_bundle(report, ext).key_id == want["key_id"]
        return
    with pytest.raises(StageError) as exc:
        v1.verify_report_bundle(report, ext)
    assert exc.value.stage == want["stage"]
    assert want.get("error", "") in str(exc.value)


def test_a_bundle_is_written_as_go_writes_it():
    case = next(c for c in CASES if c["name"] == "keep-copy")
    ext = case["extension"]
    book = v1.ReportBook(
        agreement=json.dumps(ext["book"]["agreement"], separators=(",", ":")).encode(),
        relied_on={k: json.dumps(p, separators=(",", ":")).encode() for k, p in ext["book"]["relied_on"].items()},
        offer=json.dumps(ext["book"]["offer"], separators=(",", ":")).encode() if "offer" in ext["book"] else None)
    text = v1.ReportBundle(ext["sealed_report"], book).json()
    assert json.loads(text) == ext
    assert text.startswith(b'{"sealed_report":"') and b'"book":{"agreement":{' in text
    assert v1.verify_report_bundle(bytes.fromhex(case["report"]), text).key_id == case["want"]["key_id"]


def test_report_with_book_from_a_shelf(tmp_path):
    from test_deal_v1_holder import _report_scenario

    buyer, log, copy = _report_scenario(tmp_path)
    report, book = buyer.status.report_with_book(log, copy)
    assert report == buyer.status.report(log, copy), "report is the report alone"
    first = buyer.shelf.open(log).records()[0]
    assert v1.verify_book(book.agreement).record_id == first.record_id, "the holder's record at seq 1"
    rows = {src["record"] for row in report["rows"] for src in row["relied_on"]}
    assert rows and set(book.relied_on) == rows, "exactly the records the rows rely on"
    for rid, pair in book.relied_on.items():
        assert v1.verify_book(pair).record_id == rid, "each key is its pair's record id"
    adj = dict(copy, audience="adjudicator", referee="Lamp Court")
    withheld, wbook = buyer.status.report_with_book(log, adj)
    assert withheld["rows_state"] == v1.ROWS_WITHHELD and withheld["rows"] == []
    assert book.offer is not None and v1.verify_sent(book.offer).key_id, "the keep copy carries the offer's sent pair (u561)"
    assert wbook.offer is None, "the producer leaves the offer off an adjudicator's copy (unruled)"
    assert (wbook.agreement, wbook.relied_on) == (book.agreement, book.relied_on), \
        "a withheld copy carries the keep copy's records"


def test_shapes_go_refuses():
    """A list where Go reads a struct, and a number no float64 holds (refused
    by the text check before the extension is read)."""
    case = next(c for c in CASES if c["name"] == "keep-copy")
    report, ext = bytes.fromhex(case["report"]), case["extension"]
    sealed, book = ext["sealed_report"], ext["book"]

    def big(n):
        return json.dumps(ext, separators=(",", ":")).replace(f'"sealed_report":"{sealed}"',
                                                               f'"sealed_report":{n}', 1)

    for name, text, stage in [
        ("a list for the extension", json.dumps([sealed, book]), "report"),
        ("a list for the book", json.dumps({"sealed_report": sealed,
                                              "book": [book["agreement"], book["relied_on"]]}), "report"),
        ("a number past float64", big("1e400"), "canonicalization"),
        ("a negative one", big("-1e999"), "canonicalization"),
        ("one that does not overflow", big("1e308"), "report"),
    ]:
        with pytest.raises(StageError) as exc:
            v1.verify_report_bundle(report, text.encode())
        assert exc.value.stage == stage, name


@pytest.mark.parametrize("bad", [b"", b"1 2", b"{", b"not json"])
def test_a_pair_that_is_not_one_json_value_is_not_written(bad):
    """Go's json.Marshal of a json.RawMessage refuses text that is not one
    JSON value; compacting it would write something else (1 2 as 12)."""
    ok = b'{"a":1}'
    for book in (v1.ReportBook(bad, {}), v1.ReportBook(ok, {"x": bad})):
        with pytest.raises(ValueError):
            v1.ReportBundle("id", book).json()
