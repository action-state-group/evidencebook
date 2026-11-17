# SPDX-License-Identifier: Apache-2.0
"""testdata/shelf/facts/vectors.json, which Go writes: every entry document, entry id and opening."""

import json
from pathlib import Path

import pytest

from evidencebook import shelf_facts as sf

V = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "shelf" / "facts" / "vectors.json").read_text())


def _build(inp):
    salts = tuple(bytes.fromhex(s) for s in inp.get("salts", []))
    kind = inp["kind"]
    if kind == sf.FACT_KIND_BOOK:
        f = inp["fact"]
        fact = sf.book_fact(profile=f["profile"], book_id=f["book_id"], log_id=f["log_id"], size=f["size"],
                            checkpoint_sha256=f["checkpoint_sha256"], prev_fact_size=f["prev_fact_size"],
                            last_received=f["link"]["last_received"], checked_over=f["link"]["checked_over"],
                            status=f["status"] if "status" in f else sf._ABSENT, salts=salts)
        return fact.doc, fact
    if kind == sf.FACT_KIND_JOURNAL:
        j = inp["journal"]
        st = j["status"]
        fact = sf.journal_fact(j["log_id"], st["size"], st["checkpoint_sha256"], st["prev_fact_size"], salts)
        return fact.doc, fact
    if kind == sf.ENTRY_KIND_STAMP:
        return sf.stamp_entry(inp["stamp"]["stamp"], inp["stamp"]["facts"]), None
    if kind == sf.ENTRY_KIND_FORMAT_SWITCH:
        m = inp["marker"]
        return sf.format_switch(m["last_tick"], [(x["log_id"], x["size"]) for x in m["logs"]]), None
    raise AssertionError(kind)


@pytest.mark.parametrize("e", V["entries"], ids=lambda e: e["name"])
def test_entry(e):
    doc, fact = _build(e["input"])
    eid, body = sf.entry_id(doc)
    assert json.loads(body) == e["doc"]
    assert eid.hex() == e["entry_id"]
    if fact is not None:
        assert fact.link_opening.to_json() == e["link_opening"]
        assert fact.status_opening.to_json() == e["status_opening"]
        sf.verify_openings(doc, fact.link_opening, fact.status_opening)
        sf.verify_openings(doc, status=fact.status_opening)
        with pytest.raises(sf.Invalid):
            sf.verify_openings(doc, link=fact.status_opening)


@pytest.mark.parametrize("a", V["audiences"], ids=lambda a: f"{a['kind']}-{a['audience']}")
def test_audience(a):
    assert sf.openings_for(a["kind"], a["audience"]) == (a["link"], a["status"])


@pytest.mark.parametrize("b", V["bad_openings"], ids=lambda b: b["name"])
def test_bad_opening(b):
    with pytest.raises(sf.Invalid):
        sf.Opening(b["opening"]["salt"], b["opening"]["value"]).verify(b["commitment"])


def test_errors():
    z = (bytes(32), bytes(32))
    with pytest.raises(sf.Invalid):
        sf.book_fact(profile="deal", book_id="b", log_id="l", size=1, checkpoint_sha256="c", prev_fact_size=2, salts=z)
    with pytest.raises(sf.Invalid):
        sf.book_fact(profile="deal", book_id="b", log_id="l", size=2, checkpoint_sha256="c", prev_fact_size=0, checked_over=["a", "a"], salts=z)
    with pytest.raises(sf.Invalid):
        sf.openings_for("stamp", "owner")
    with pytest.raises(sf.Invalid):
        sf.openings_for(sf.FACT_KIND_BOOK, "stranger")


@pytest.mark.parametrize("c", V["invalid_facts"], ids=lambda c: c["name"])
def test_invalid_fact(c):
    with pytest.raises(sf.Invalid):
        _build(c["input"])


def test_lone_surrogate_and_bad_counts():
    with pytest.raises(sf.Invalid):
        sf.commit(bytes(32), {"x": "\ud800"})
    with pytest.raises(sf.Invalid):
        sf.stamp_entry(True, 1)
    with pytest.raises(sf.Invalid):
        sf.format_switch(-1, [])


@pytest.mark.parametrize("m", V["invalid_markers"], ids=lambda m: m["name"])
def test_invalid_marker(m):
    with pytest.raises(sf.Invalid):
        sf.format_switch(m["marker"]["last_tick"], [(x["log_id"], x["size"]) for x in m["marker"]["logs"]])


def test_journal_errors():
    z = (bytes(32), bytes(32))
    for args in [("journal/x", 1, "nothex", 0), ("journal/x", 1, "a" * 64, 2), ("", 1, "a" * 64, 0)]:
        with pytest.raises(sf.Invalid):
            sf.journal_fact(*args, z)
    with pytest.raises(sf.Invalid):
        sf.book_fact(profile="deal", book_id="a" * 64, log_id="l", size=1, checkpoint_sha256="b" * 64, prev_fact_size=0, status=[1], salts=z)
