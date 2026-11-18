# SPDX-License-Identifier: Apache-2.0
"""x-deal-v1 records, checks and start (evidencebook#461 PR 6d-1), held to
Go by the fixtures and vectors Go writes."""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from evidencebook.deal.v1 import (
    ABSENT, PLACEMENT_JOURNAL, STAGE_AGREEMENT, STAGE_CANONICALIZATION, STAGE_RECORD, STAGE_SCHEMA, BookRecord,
    FirstAct, GoTypeError, SealContext, StageError, StartBooks, StartInput, StartResult, check, check_agreement,
    check_dispute, check_ruling, check_sealed, decode, digest, encode, placement_of, profile_schema, ref_to,
    rows_digest, start, start_from_books,
)
from evidencebook.deal.v1 import gotypes

REPO = Path(__file__).resolve().parents[2]
FIXTURES = REPO / "dealprofile" / "x-deal-v1" / "fixtures"
VECTORS = REPO / "testdata" / "deal-v1"


def text(v) -> bytes:
    return json.dumps(v).encode()


def fixture(path: str) -> dict:
    return json.loads((FIXTURES / path).read_text())


def digests() -> dict[str, str]:
    out = {}
    for line in (FIXTURES / "expected-digests.txt").read_text().splitlines():
        if line and not line.startswith("#"):
            d, p = line.split("  ", 1)
            out[p] = d
    return out


# -- the vendored files ----------------------------------------------------------------


def test_the_vendored_schema_is_the_profiles():
    assert profile_schema() == (REPO / "dealprofile" / "x-deal-v1" / "x-deal-v1.schema.json").read_bytes()


def test_the_types_table_names_every_record_type():
    from evidencebook.deal.v1.names import record_types

    assert sorted(gotypes.table()["bodies"]) == sorted(record_types())


# -- the fixtures ----------------------------------------------------------------------


POSITIVE = sorted(digests())


@pytest.mark.parametrize("path", POSITIVE)
def test_a_positive_fixture_passes_with_its_digest_and_placement(path):
    f = fixture(path)
    raw = text(f["record"])
    if f.get("seal_context"):
        check_sealed(raw, SealContext(**f["seal_context"]))
    else:
        check(raw)
    assert digest(raw) == digests()[path] == f["expected_digest"]
    assert placement_of(raw) == f["placement"]
    payload, d = encode(raw)
    assert d == f["expected_digest"] and json.loads(payload) == f["record"]
    # Decode reads it, and the types write it back as it was.
    assert decode(raw).value == f["record"]


NEGATIVE = sorted(str(p.relative_to(FIXTURES)) for p in (FIXTURES / "negative").glob("*.json"))
HELD = fixture("appendix-a/seller-book/01-agreement.json")["record_id"]


@pytest.mark.parametrize("path", NEGATIVE)
def test_a_negative_fixture_is_refused_at_its_stage(path):
    f = fixture(path)
    # A text no JSON value writes (a float, a member given twice) is held as
    # the text itself.
    raw = f["record_text"].encode() if "record_text" in f else text(f["record"])
    with pytest.raises(StageError) as exc:
        if f["check"] == "record":
            if f.get("seal_context"):
                check_sealed(raw, SealContext(**f["seal_context"]))
            else:
                check(raw)
        else:
            other = text(fixture(f["with"])["record"])
            {"agreement": lambda: check_agreement(raw, other, HELD), "dispute": lambda: check_dispute(raw, other),
             "ruling": lambda: check_ruling(raw, other)}[f["check"]]()
    assert exc.value.stage == f["expected_stage"]
    assert f["expected_error_contains"] in exc.value.detail


def test_every_fixture_is_read():
    assert len(POSITIVE) + len(NEGATIVE) == len([p for p in FIXTURES.rglob("*.json")])


# -- deal_id presence (testdata/deal-v1/records) ---------------------------------------


PRESENCE = json.loads((VECTORS / "records" / "deal-id-presence.json").read_text())["cases"]


@pytest.mark.parametrize("case", PRESENCE, ids=[c["name"] for c in PRESENCE])
def test_deal_id_presence(case):
    raw = text(case["record"])
    ctx = case.get("context")

    def run():
        if ctx:
            check_sealed(raw, SealContext(ctx.get("offer_exists", False), ctx.get("admission", False)))
        else:
            check(raw)

    if case["valid"]:
        run()
    else:
        with pytest.raises(StageError) as exc:
            run()
        assert exc.value.stage == case["stage"]


# -- Decode as Go's types read a record ------------------------------------------------


def a_record() -> dict:
    return json.loads(json.dumps(fixture(POSITIVE[0])["record"]))


@pytest.mark.parametrize("change, why", [
    (lambda r: r["x-deal-v1"].__setitem__("Profile", "x"), "a member in another case"),
    (lambda r: r["x-deal-v1"].__setitem__("unknown", 1), "a member the types do not name"),
    (lambda r: r["x-deal-v1"].__setitem__("seq", 0), "a zero value omitzero leaves out"),
    (lambda r: r["x-deal-v1"].__setitem__("at", None), "a null where a string is held"),
    (lambda r: r["x-deal-v1"].__setitem__("seq", 1 << 63), "a number past int64"),
    (lambda r: r.__setitem__("extra", {}), "a third top-level member"),
    (lambda r: r.pop("body"), "no body"),
])
def test_decode_refuses_text_the_types_would_not_write_back(change, why):
    r = a_record()
    change(r)
    with pytest.raises(GoTypeError):
        decode(text(r))


def test_a_case_variant_member_is_refused_at_the_schema_stage():
    r = a_record()
    r["x-deal-v1"]["Profile"] = r["x-deal-v1"]["profile"]
    with pytest.raises(StageError) as exc:
        check(text(r))
    assert exc.value.stage == STAGE_SCHEMA


def test_null_and_zero_values_are_read_as_go_reads_them():
    t = gotypes.block_type()
    blk = gotypes.read({"profile": "x-deal-v1", "seen": None, "refs": None, "seq": None}, t)
    assert blk["seen"] is None, "Null[string]: present and null"
    assert blk["refs"] is None and blk["seq"] == 0
    assert gotypes.read({"profile": "x"}, t)["seen"] is ABSENT
    written = gotypes.write(blk, t)
    assert written["seen"] is None and "refs" not in written and "seq" not in written
    assert gotypes.write(gotypes.read({"refs": []}, t), t)["refs"] == [], "an empty list is not nil"


# -- the text --------------------------------------------------------------------------


@pytest.mark.parametrize("raw, detail", [
    (b'{"a": 1, "a": 2}', 'member "a" given twice'),
    (b'{"a": "\\ud800"}', "an escaped surrogate outside a pair"),
    (b'{"a": "\\udc00\\ud800"}', "an escaped surrogate outside a pair"),
    (b'{"a": "\xff"}', "invalid UTF-8"),
])
def test_the_text_check_refuses_what_a_decoder_would_read_otherwise(raw, detail):
    with pytest.raises(StageError) as exc:
        check(raw)
    assert exc.value.stage == STAGE_CANONICALIZATION and exc.value.detail == detail


def test_a_float_and_a_missing_block_are_canonicalization_refusals():
    r = a_record()
    r["x-deal-v1"]["seq"] = 1.5
    with pytest.raises(StageError) as exc:
        check(text(r))
    assert exc.value.stage == STAGE_CANONICALIZATION and exc.value.detail.startswith("a float at $.x-deal-v1.seq")
    with pytest.raises(StageError) as exc:
        check(b'{"body": {}}')
    assert exc.value.detail == "no x-deal-v1 block"


def test_a_pattern_end_is_the_text_end_as_go_reads_it():
    r = a_record()
    r["x-deal-v1"]["at"] += "\n"
    with pytest.raises(StageError) as exc:
        check(text(r))
    assert exc.value.stage == STAGE_SCHEMA


@pytest.mark.parametrize("at", ["2026-02-30T10:00:00Z", "2026-11-01T24:00:00Z", "0000-01-01T00:00:00Z"])
def test_a_time_that_names_no_instant_is_refused(at):
    r = a_record()
    r["x-deal-v1"]["at"] = at
    with pytest.raises(StageError) as exc:
        check(text(r))
    assert exc.value.stage == STAGE_RECORD
    assert exc.value.detail == f'"{at}" is not a UTC time to the second that exists'


def test_ref_to_and_rows_digest():
    offer = fixture("appendix-a/buyer-journal/05-offer.json")["record"]
    assert ref_to("ab") == {"type": "deal-record", "digest_alg": "SHA-256", "digest": "ab"}
    agreement = fixture("appendix-a/buyer-book/01-agreement.json")["record"]
    assert rows_digest(text(offer["body"]["terms_rows"])) == agreement["body"]["rows_digest"]
    assert placement_of(text(offer)) == PLACEMENT_JOURNAL


def test_check_agreement_reads_the_held_acceptor_agreement():
    offer = text(fixture("appendix-a/buyer-journal/05-offer.json")["record"])
    offerer = text(fixture("appendix-a/buyer-book/01-agreement.json")["record"])
    check_agreement(offerer, offer, HELD)
    with pytest.raises(StageError) as exc:
        check_agreement(offerer, offer, "another")
    assert exc.value.stage == STAGE_AGREEMENT


# -- start (testdata/deal-v1/start) ----------------------------------------------------


def start_input(v: dict) -> StartInput:
    v = dict(v)
    if v.get("first_act") is not None:
        v["first_act"] = FirstAct(v["first_act"].get("time"))
    return StartInput(**v)


START = json.loads((VECTORS / "start" / "vectors.json").read_text())["cases"]


@pytest.mark.parametrize("case", START, ids=[c["name"] for c in START])
def test_start_vectors(case):
    assert start(start_input(case["input"])).value() == case["expect"]


BOOKS = json.loads((VECTORS / "start" / "books.json").read_text())["cases"]


@pytest.mark.parametrize("case", BOOKS, ids=[c["name"] for c in BOOKS])
def test_start_from_books_vectors(case):
    b = case["books"]

    def book(rs):
        return [BookRecord(r["record"], r["record_id"], r.get("times") or {}) for r in rs]

    inp, res = start_from_books(StartBooks(b["at"], b["offer"], book(b["own"]), book(b.get("counterpart") or [])))
    assert inp.value() == case["input"]
    assert res.value() == case["expect"]


@pytest.mark.parametrize("inp, detail", [
    (StartInput(at="2026-11-01T10:00:00+01:00", side="offerer"), 'at "2026-11-01T10:00:00+01:00" is not'),
    (StartInput(at="2026-11-01T10:00:00Z", side="lender"), 'side "lender" is neither offerer nor acceptor'),
    (StartInput(at="2026-11-01T10:00:00Z", side="acceptor", counterparty_layer=True, agree_by="x"),
     'agree_by "x" is not'),
    (StartInput(at="2026-11-01T10:00:00Z", side="acceptor", counterparty_layer=True,
                agree_by="2026-11-02T00:00:00Z", counterpart_agreement_held=True), "a held counterpart agreement"),
    (StartInput(at="2026-11-01T10:00:00Z", side="offerer", counterparty_layer=True,
                agree_by="2026-11-02T00:00:00Z"), "an offerer writes its agreement after"),
    (StartInput(at="2026-11-01T10:00:00Z", side="offerer", first_act=FirstAct("late")),
     'first_act.time "late" is not'),
])
def test_start_refuses(inp, detail):
    with pytest.raises(ValueError) as exc:
        start(inp)
    assert detail in str(exc.value)


def test_start_without_the_layer_is_the_first_act():
    base = StartInput(at="2026-11-01T10:00:00Z", side="acceptor")
    assert start(base) == StartResult("not_started")
    assert start(StartInput(at=base.at, side="acceptor", first_act=FirstAct())) == StartResult(
        "started", True, "first_act")


def test_start_from_books_refuses_books_out_of_order():
    b = BOOKS[0]["books"]
    own = [BookRecord(r["record"], r["record_id"]) for r in b["own"]]
    with pytest.raises(ValueError) as exc:
        start_from_books(StartBooks(b["at"], b["offer"], list(reversed(own)) if len(own) > 1 else own * 2))
    assert "start from books" in str(exc.value)
    with pytest.raises(ValueError) as exc:
        start_from_books(StartBooks(b["at"], b["offer"], []))
    assert str(exc.value) == "xdealv1: start from books: the holder's book is empty"
    with pytest.raises(ValueError) as exc:
        start_from_books(StartBooks(b["at"], b["own"][0]["record"], own))
    assert "the offer is not a propose but a agree record" in str(exc.value)


# -- review round 1 (#545) -------------------------------------------------------------


def a_verdict_with_a_null_limit() -> dict:
    v = fixture("appendix-a/buyer-journal/07-verdict-pass.json")["record"]
    v["body"]["rules"] = {"status": "ran", "findings": [{"id": "r1", "verdict": "pass", "limit": None}]}
    return v


def test_a_raw_member_written_null_is_kept_as_go_keeps_it():
    # json.RawMessage holds the text null, which is not zero: omitzero keeps it.
    v = a_verdict_with_a_null_limit()
    assert decode(text(v)).value == v
    del v["x-deal-v1"]["deal_id"]
    with pytest.raises(StageError) as exc:
        check(text(v))
    assert (exc.value.stage, exc.value.detail) == (STAGE_RECORD, "a verdict record carries deal_id")
    t = {"struct": "dealprofile.VerdictBodyRulesFindingsItem"}
    assert "limit" not in gotypes.write(gotypes.read({"id": "x", "verdict": "pass"}, t), t), "absent stays absent"


def test_rows_digest_reads_rows_through_their_type():
    offer = fixture("appendix-a/buyer-journal/05-offer.json")["record"]
    want = fixture("appendix-a/buyer-book/01-agreement.json")["record"]["body"]["rows_digest"]
    assert rows_digest(decode(text(offer)).body["terms_rows"]) == want, "the Go value, zero fields included"
    rows = json.loads(json.dumps(offer["body"]["terms_rows"]))
    rows["rows"][1]["recomputed"] = False  # a row with no recomputed member
    assert rows_digest(rows) == want, "a zero value omitzero leaves out is left out"
    assert rows_digest(text(offer["body"]["terms_rows"])) == want


def test_a_record_is_checked_and_digested_as_it_is_now():
    offer = fixture("appendix-a/buyer-journal/05-offer.json")["record"]
    r = decode(text(offer))
    before = digest(r)
    r.body["agree_by"] = "2026-11-03T23:59:59Z"
    changed = json.loads(json.dumps(offer))
    changed["body"]["agree_by"] = "2026-11-03T23:59:59Z"
    assert digest(r) == digest(text(changed)) != before
    assert encode(r)[1] == digest(r)
    r.body["agree_by"] = "2026-02-30T00:00:00Z"
    with pytest.raises(StageError) as exc:
        check(r)
    assert exc.value.stage == STAGE_RECORD


def test_text_nested_past_the_bound_is_a_canonicalization_refusal():
    deep = '{"x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs"}, "body": ' + "[" * 200 + "]" * 200 + "}"
    with pytest.raises(StageError) as exc:
        check(deep.encode())
    assert exc.value.stage == STAGE_CANONICALIZATION
    with pytest.raises(StageError):
        decode(deep.encode())


@pytest.mark.parametrize("platform", ["shop x.example", "a\u000bb.example"])
def test_a_schema_white_space_class_is_re2s(platform):
    # RE2's \s is [\t\n\f\r ]: Go passes a platform with another space in it.
    rec = {"x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs", "record_type": "sale_unpost",
                         "at": "2026-11-01T10:00:00Z"}, "body": {"platform": platform}}
    check(text(rec))
    rec["body"]["platform"] = "shop x.example"
    with pytest.raises(StageError) as exc:
        check(text(rec))
    assert exc.value.stage == STAGE_SCHEMA


def test_decode_refuses_a_float_as_go_s_round_trip_does():
    offer = fixture("appendix-a/buyer-journal/05-offer.json")["record"]
    raw = text(offer).replace(b'"amount_minor": 6000', b'"amount_minor": 1e400', 1)
    assert raw != text(offer)
    with pytest.raises(GoTypeError):
        decode(raw)


def test_a_float_set_on_a_record_by_hand_is_a_canonicalization_refusal():
    r = decode(text(fixture("appendix-a/buyer-journal/05-offer.json")["record"]))
    r.body["terms_rows"]["rows"][0]["owed"] = {"a": float("inf")}
    with pytest.raises(StageError) as exc:
        check(r)
    assert exc.value.stage == STAGE_CANONICALIZATION
