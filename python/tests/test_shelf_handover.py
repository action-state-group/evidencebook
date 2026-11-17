# SPDX-License-Identifier: Apache-2.0
"""The whole-shelf handover and its replay (evidencebook#461 §2.7), as Go's
shelf_handover_test.go checks them, and testdata/shelf/handover.json replayed
to handover-result.json byte for byte."""
from __future__ import annotations

import dataclasses
import json
from datetime import timedelta

import pytest

from evidencebook._gojson import go_json, go_json_indent
from evidencebook.errors import InvalidError
from evidencebook.shelf_facts import FACT_KIND_BOOK, Opening
from evidencebook.shelf_handover import Handover, canonical_document, verify_handover

from test_shelf_stamp import START, VECTORS, fx, obs, script, stamp_cadence  # noqa: F401  (fx is a fixture)

KEY = script.CHECKPOINT_KEY.public_key().hex()


def handed_over(fx) -> Handover:
    """A migrated shelf with two books, one grown after migration and one with
    a received-only change, a quiet stamp and a witnessed one, handed over at
    its newest stamp (Go ``handedOver``)."""
    with fx.open() as shelf:
        c, values = stamp_cadence(shelf)
        a = shelf.open("deal/a")
        a.append(obs("a", "1"))
        shelf.open("deal/b").append(obs("b", "1"))
        c.tick(START)
        c.migrate(START + timedelta(minutes=1))
        a.append(obs("a", "2"))
        c.stamp(START + timedelta(minutes=2))
        values.status["deal/b"] = {"rows": []}
        c.stamp(START + timedelta(minutes=3))
        last = c.stamp(START + timedelta(minutes=4)).tick
        assert c.witnessed(last.n, script.mint_receipt(last.checkpoint)).state == "VERIFIED"
        return c.handover(0)


def copy_of(h: Handover) -> Handover:
    return Handover.loads(go_json(h.value()))


def fact_at(h: Handover, log: str, nth: int) -> int:
    seen = 0
    for i, d in enumerate(h.documents):
        f = json.loads(d.doc)
        if f["kind"] == FACT_KIND_BOOK and f["log_id"] == log:
            if seen == nth:
                return i
            seen += 1
    raise AssertionError(f"no fact {nth} of {log}")


def test_a_handover_verifies_and_returns_the_latest_facts(fx):
    h = handed_over(fx)
    got = verify_handover(h, KEY, script.DIRECTORY)
    assert got.recomputed and got.witnessed
    assert got.coverage_start
    assert len(got.latest) == 2
    assert len(got.untied) == 2, "no genesis fact carries a tie yet"
    by_log = {x.fact["log_id"]: x for x in got.latest}
    assert by_log["deal/a"].fact["size"] > by_log["deal/a"].fact["prev_fact_size"], "deal/a grew after migration"
    assert by_log["deal/b"].fact["size"] == by_log["deal/b"].fact["prev_fact_size"], "deal/b changed only its values"
    assert by_log["deal/b"].status_opening.value == {"rows": []}
    # Round trip: the handover is JSON a reader in another language reads.
    again = verify_handover(copy_of(h), KEY, script.DIRECTORY)
    assert go_json(again.value()) == go_json(got.value())
    # A dropped book is caught by the count: the replay counts every book.
    assert sorted(by_log) == ["deal/a", "deal/b"]


def test_a_shelf_with_no_witness_is_recomputed_and_unwitnessed(fx):
    h = dataclasses.replace(handed_over(fx), receipts=[])
    got = verify_handover(h, KEY, script.DIRECTORY)
    assert got.recomputed and not got.witnessed


def _drop(items: list, i: int) -> list:
    return items[:i] + items[i + 1:]


def _set_document(h: Handover, i: int, **changes) -> Handover:
    documents = list(h.documents)
    documents[i] = dataclasses.replace(documents[i], **changes)
    return dataclasses.replace(h, documents=documents)


PLANTS = {
    "an entry held back": lambda h: dataclasses.replace(h, entries=h.entries[:-1]),
    "an entry changed": lambda h: dataclasses.replace(h, entries=[h.entries[1]] + h.entries[1:]),
    "a fact left out": lambda h: dataclasses.replace(h, documents=_drop(h.documents, fact_at(h, "deal/a", 1))),
    "a changed opening": lambda h: _set_document(
        h, fact_at(h, "deal/b", 1),
        status_opening=Opening(h.documents[fact_at(h, "deal/b", 1)].status_opening.salt, {"rows": [1]})),
    "a missing opening": lambda h: _set_document(h, fact_at(h, "deal/a", 0), status_opening=None),
    "a replaced book (no proof)": lambda h: dataclasses.replace(h, consistency=[]),
    "a book checkpoint left out": lambda h: dataclasses.replace(h, book_checkpoints=h.book_checkpoints[:-1]),
    "another holder's anchor": lambda h: dataclasses.replace(h, log_id="deal-cadence/ffffffffffffffff"),
}


def test_a_handover_refuses_each_planted_failure(fx):
    base = handed_over(fx)
    verify_handover(base, KEY, script.DIRECTORY)
    for name, plant in PLANTS.items():
        with pytest.raises(InvalidError):
            verify_handover(plant(copy_of(base)), KEY, script.DIRECTORY)
            pytest.fail(name)
    with pytest.raises(InvalidError):
        verify_handover(base, "not-the-key", script.DIRECTORY)


def test_an_entry_document_must_be_canonical(fx):
    h = handed_over(fx)
    doc = next(d.doc for d in h.documents if d.status_opening is not None)
    canonical_document(doc, FACT_KIND_BOOK)
    body = doc[1:-1]
    for name, changed in {
        "a duplicate member": b'{"v":2,' + body + b"}",
        "an unknown member": b'{"extra":1,' + body + b"}",
        "whitespace": b"{ " + body + b"}",
    }.items():
        with pytest.raises(InvalidError):
            canonical_document(changed, FACT_KIND_BOOK)
            pytest.fail(name)


def test_a_handover_needs_a_finished_stamp(fx):
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        shelf.open("deal/a").append(obs("a", "1"))
        old = c.tick(START).tick
        with pytest.raises(InvalidError):
            c.handover(old.n)


def test_the_vector_replays_to_its_result():
    """handover.json, Go's handover of the scripted shelf, reads and writes
    back byte for byte and replays to handover-result.json."""
    raw = (VECTORS / "handover.json").read_text()
    h = Handover.loads(raw)
    assert go_json_indent(h.value()) == raw
    got = verify_handover(h, KEY, script.DIRECTORY)
    assert go_json_indent(got.value()) == (VECTORS / "handover-result.json").read_text()
