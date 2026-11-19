# SPDX-License-Identifier: Apache-2.0
"""VerifyReport (evidencebook#554) held to Go: each case of
testdata/deal-v1/report-verify/vectors.json, which Go's
TestReportVerifyVectors writes, gives Go's signer, or Go's stage and the
fragment of Go's refusal."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook.deal.check import StageError
from evidencebook.deal import v1

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "report-verify" / "vectors.json"
CASES = json.loads(VECTORS.read_text())["cases"]


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_report_verify_vector(case):
    report, first = bytes.fromhex(case["report"]), bytes.fromhex(case["first"])
    want = case["want"]
    if "key_id" in want:
        got = v1.verify_report(report, first)
        assert got.key_id == want["key_id"]
        assert got.report["profile"] == v1.PROFILE
        return
    with pytest.raises(StageError) as exc:
        v1.verify_report(report, first)
    assert exc.value.stage == want["stage"]
    assert want.get("error", "") in str(exc.value)
