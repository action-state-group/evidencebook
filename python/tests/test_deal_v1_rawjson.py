# SPDX-License-Identifier: Apache-2.0
"""The raw-JSON reader (deal/v1/rawjson.py, goscan.py) against Go's
encoding/json, by testdata/deal-v1/rawjson/vectors.json, which Go's
TestRawJSONVectors writes (#552)."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from evidencebook.deal.check import StageError
from evidencebook.deal.v1 import rawjson
from evidencebook.deal.v1.sent import _marshal_members, _raw_message

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "deal-v1" / "rawjson" / "vectors.json"
CASES = json.loads(VECTORS.read_text())["cases"]


def text_of(case: dict) -> bytes:
    if "gen" in case:
        g = case["gen"]
        a = "[" * g["depth"] + "]" * g["depth"]
        return {"array": a, "pair": '{"record":{},"agent_input":' + a + "}", "after_string": '""' + a}[g["kind"]].encode()
    return bytes.fromhex(case.get("in", ""))


def ident(case: dict) -> str:
    return json.dumps(case["gen"]) if "gen" in case else case.get("in", "")[:40]


def go_str(v):
    """A raw text as Go's vector writer gives it: invalid bytes as U+FFFD."""
    from evidencebook.deal.v1.goscan import go_utf8

    return None if v is None else go_utf8(v.encode("utf-8", "surrogateescape"))


def go_map(m):
    return {k: go_str(v) for k, v in (m or {}).items()}


def ok(f):
    try:
        return True, f()
    except (ValueError, StageError):
        return False, None


@pytest.mark.parametrize("case", CASES, ids=[ident(c) for c in CASES])
def test_raw_json_is_read_and_written_as_go_does(case):
    b = text_of(case)
    assert rawjson.valid(b) == case["valid"], "json.Valid"
    assert ok(lambda: rawjson.text_check(b))[0] == case["check_text"], "checkText"
    got, pair = ok(lambda: rawjson.strict_struct(b, {"record": "map", "agent_input": "raw"}))
    if case["pair"] is None:
        assert not got, "strictUnmarshal refuses"
    else:
        assert got, "strictUnmarshal reads it"
        assert (pair["record"] is None) == case["pair"]["record_nil"]
        assert go_map(pair["record"]) == (case["pair"].get("record") or {})
        assert go_str(pair["agent_input"]) == case["pair"]["agent_input"]
    got, m = ok(lambda: rawjson.unmarshal_map(b))
    if case["map"] is None:
        assert not got, "json.Unmarshal into a map refuses"
    else:
        assert got and (m is None) == case["map"]["nil"]
        assert go_map(m) == (case["map"].get("members") or {})
    got, out = ok(lambda: _marshal_members(rawjson.unmarshal_map(b) or {}) if rawjson.unmarshal_map(b) is not None
                  else b"null")
    if case["remarshal"] is None:
        assert case["map"] is None or not got, "json.Marshal of the map refuses"
    else:
        assert got, "json.Marshal of the map writes it"
        want = case["remarshal"]
        assert (hashlib.sha256(out).hexdigest() == want[7:]) if want.startswith("sha256:") else out == bytes.fromhex(want)
    got, cid = ok(lambda: rawjson.string_member(b, "capsule_id"))
    assert (cid if got else None) == case["capsule_id"], "struct{CapsuleID}"
    got, out = ok(lambda: _marshal_members({"p": _raw_message(b)}))
    if case["marshal"] is None:
        assert not got, "json.Marshal refuses"
    else:
        assert got, "json.Marshal writes it"
        want = case["marshal"]
        if want.startswith("sha256:"):
            assert hashlib.sha256(out).hexdigest() == want[7:]
        else:
            assert out == bytes.fromhex(want)


def test_a_pair_builder_keeps_a_capsule_member_s_raw_bytes():
    # Go's BookPair keeps a RawMessage's bytes, invalid UTF-8 included (#552 review).
    from evidencebook.deal.v1 import sent

    pc = json.loads((VECTORS.parent.parent / "payment" / "vectors.json").read_text())["pair"][0]
    h = bytes.fromhex
    cap = h(pc["capsule"])
    cap = cap[:-1] + b',"x":"\xff"}'
    out = sent.book_pair(cap, h(pc["envelope"]), h(pc["header"]), h(pc["payload"]))
    assert b'"x":"\xff"' in out
    out = sent.pair(cap, h(pc["envelope"]), h(pc["payload"]))
    assert b'"x":"\xff"' in out
