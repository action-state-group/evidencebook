# SPDX-License-Identifier: Apache-2.0
"""x-deal-v1 authorization (evidencebook#461 PR 6d-2b), held to Go by
testdata/deal-v1/authorization, which Go's TestApprovalMigration writes."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook.deal.v1 import StageError
from evidencebook.deal.v1.authorization import STAGE_AUTHORIZATION, check_authorizations

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "authorization" / "approval-migration.json"
CASES = json.loads(VECTORS.read_text())["cases"]


def text(v) -> bytes:
    return json.dumps(v).encode()


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_approval_migration(case):
    journal = [text(r) for r in case["journal"] or ()]
    acts = [text(r) for r in case["acts"] or ()]
    if not case["valid"]:
        with pytest.raises(StageError) as exc:
            check_authorizations(journal, acts)
        assert exc.value.stage == STAGE_AUTHORIZATION
        return
    got = check_authorizations(journal, acts)
    assert [a.needs_person_signature for a in got] == case["person_signature"]
    assert [len(a.limit_approvals) for a in got] == case["limit_approvals"]
    assert [a.intent != "" for a in got] == case["intent_signature"]


def test_an_act_that_cites_no_check_is_not_gated():
    case = next(c for c in CASES if c["valid"])
    act = json.loads(json.dumps(case["acts"][0]))
    act["body"].pop("check", None)
    assert check_authorizations([text(r) for r in case["journal"]], [text(act)]) == []


def test_a_record_that_is_not_an_act_is_refused():
    case = next(c for c in CASES if c["valid"])
    with pytest.raises(StageError) as exc:
        check_authorizations([text(r) for r in case["journal"]], [text(case["journal"][0])])
    assert exc.value.detail.startswith("act 1: a ") and exc.value.detail.endswith(" is not an act")


def test_the_refusal_texts_are_go_s():
    # Each refusal's text is Go's: a throwaway Go probe printed CheckAuthorizations'
    # error for every case of this file, and Python's matches it line for line.
    names = {c["name"]: c for c in CASES if not c["valid"]}
    with pytest.raises(StageError) as exc:
        c = names["check-not-held"]
        check_authorizations([text(r) for r in c["journal"] or ()], [text(r) for r in c["acts"] or ()])
    assert exc.value.detail == "act 1: it cites no check the journal holds"
