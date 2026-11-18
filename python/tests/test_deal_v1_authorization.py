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
        assert f"[{exc.value.stage}] {exc.value.detail}" == case["error"], "Go's text, word for word"
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


TIMES = json.loads((VECTORS.parent.parent / "time" / "vectors.json").read_text())["cases"]


@pytest.mark.parametrize("case", TIMES, ids=[repr(c["in"]) for c in TIMES])
def test_go_time_parse(case):
    from evidencebook.deal.v1 import gotime

    if case["ok"]:
        sec, ns = gotime.parse(case["in"])
        assert (sec, ns) == (case.get("unix", 0), case.get("nanos", 0))
        assert (sec < gotime.ZERO) == case.get("before_zero", False)
    else:
        with pytest.raises(gotime.GoTimeError) as exc:
            gotime.parse(case["in"])
        assert str(exc.value) == case["error"]


def a_journal(at: str) -> list[bytes]:
    case = next(c for c in CASES if c["valid"])
    rec = json.loads(json.dumps(case["journal"][0]))
    rec["x-deal-v1"]["at"] = at
    return [text(rec)]


@pytest.mark.parametrize("at", ["2026-11-01T10:00:00,5Z", "2026-11-01T1:00:00Z", "2026-11-01T10:00:00+24:00"])
def test_a_time_go_reads_is_read(at):
    # Go's time.Parse takes these from a record that was never checked (#550 review).
    assert check_authorizations(a_journal(at), []) == []


def test_a_time_before_go_s_zero_time_is_refused():
    with pytest.raises(StageError) as exc:
        check_authorizations(a_journal("0000-01-01T00:00:00Z"), [])
    assert exc.value.detail == "journal record 1: its at is earlier than the record before it: at never decreases"


def test_an_unreadable_time_carries_go_s_text():
    with pytest.raises(StageError) as exc:
        check_authorizations(a_journal("bad"), [])
    assert exc.value.detail == ('journal record 1: its at "bad": parsing time "bad" as "2006-01-02T15:04:05Z07:00": '
                                'cannot parse "bad" as "2006"')


def test_a_quoted_action_is_quoted_as_go_quotes_it():
    import hashlib

    from agent_action_capsule.canonical import jcs

    case = next(c for c in CASES if c["name"] == "user-approves-a-pass")
    journal = json.loads(json.dumps(case["journal"]))
    acts = json.loads(json.dumps(case["acts"]))
    old = {}
    for r in journal:
        old[hashlib.sha256(jcs(r)).hexdigest()] = r
    journal[0]["body"]["action"] = "pay\n"
    # Re-chain: each later record cites the new digests of the ones before it.
    new_of = {}
    for r in journal:
        for ref in r["x-deal-v1"].get("refs") or ():
            ref["digest"] = new_of.get(ref["digest"], ref["digest"])
        before = next(d for d, o in old.items() if o is r)
        new_of[before] = hashlib.sha256(jcs(r)).hexdigest()
    acts[0]["body"]["check"]["digest"] = new_of[acts[0]["body"]["check"]["digest"]]
    with pytest.raises(StageError) as exc:
        check_authorizations([text(r) for r in journal], [text(r) for r in acts])
    assert exc.value.detail == 'act 1: the act differs from the one checked: a payer settle rests on a "pay" check, not "pay\\n"'
