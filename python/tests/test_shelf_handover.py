# SPDX-License-Identifier: Apache-2.0
"""The whole-shelf handover and its replay (evidencebook#461 §2.7), as Go's
shelf_handover_test.go and shelf_handover_tie_test.go check them, and
testdata/shelf/handover.json replayed to handover-result.json byte for byte."""
from __future__ import annotations

import dataclasses
import hashlib
import json
from datetime import timedelta

import pytest

from evidencebook._gojson import go_json, go_json_indent
from evidencebook.errors import InvalidError
from evidencebook.shelf_facts import FACT_KIND_BOOK, Opening
from evidencebook._cll import read_checkpoint
from evidencebook._gojson import Ordered
from evidencebook.shelf_handover import Handover, canonical_document, check_fact_chain, verify_handover

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
        # A receipt with a registration time: only such a receipt gives the
        # handover the witnessed level (§2.7).
        iat = int((START + timedelta(minutes=5)).timestamp())
        assert c.witnessed(last.n, script.mint_receipt(last.checkpoint, iat)).state == "VERIFIED"
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
    assert len(got.untied) == 2, "no witnessed tree tick held either log"
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


def test_a_handover_of_another_version_is_refused(fx):
    h = handed_over(fx)
    for v in (0, 2):
        with pytest.raises(InvalidError, match="version"):
            verify_handover(dataclasses.replace(h, v=v), KEY, script.DIRECTORY)
    value = h.value()
    del value["v"]
    with pytest.raises(InvalidError, match="version 0"):
        verify_handover(Handover.loads(go_json(value)), KEY, script.DIRECTORY)


def test_a_fact_chain_refuses_a_replaced_book(fx):
    """Go ``TestAFactChainRefusesAReplacedBook``, on deal/a's two facts."""
    h = handed_over(fx)
    verify_handover(h, KEY, script.DIRECTORY)
    statements = {hashlib.sha256(b).hexdigest(): read_checkpoint(b) for b in h.book_checkpoints}
    proofs = {(p.log_id, p.from_size, p.to_size): p.proof for p in h.consistency}
    a = [f for f in (json.loads(d.doc) for d in h.documents) if f["kind"] == FACT_KIND_BOOK and f["log_id"] == "deal/a"]
    assert len(a) == 2
    prev, nxt = a
    assert nxt["size"] > prev["size"]
    check_fact_chain(prev, nxt, statements, proofs)
    same = {**nxt, "prev_fact_size": nxt["size"]}
    check_fact_chain(nxt, same, statements, proofs)  # a later fact at the same size and checkpoint (a changed value)

    key = ("deal/a", prev["size"], nxt["size"])
    forged = json.loads(go_json(proofs[key]), object_pairs_hook=Ordered)
    forged["proof"]["old_peaks"][0] = "00" * 32
    wrong_proof = {**proofs, key: forged}
    cases = {
        "another log": (prev, {**nxt, "log_id": "deal/b"}, proofs),
        "a wrong previous size": (prev, {**nxt, "prev_fact_size": prev["size"] + 1}, proofs),
        "a shrinking book": (nxt, {**prev, "prev_fact_size": nxt["size"]}, proofs),
        "the same size, another checkpoint": (
            prev, {**prev, "prev_fact_size": prev["size"], "checkpoint_sha256": nxt["checkpoint_sha256"]}, proofs),
        "no proof": (prev, nxt, {}),
        "a wrong proof": (prev, nxt, wrong_proof),
    }
    for name, (p, f, ps) in cases.items():
        with pytest.raises(InvalidError, match=r"\(a replaced book\)"):
            check_fact_chain(p, f, statements, ps)
            pytest.fail(name)
    # A checkpoint the handover does not hold is a replaced book too.
    held = {k: v for k, v in statements.items() if k != nxt["checkpoint_sha256"]}
    with pytest.raises(InvalidError, match=r"\(a replaced book\)"):
        check_fact_chain(prev, nxt, held, proofs)


@pytest.mark.parametrize("member,value", [("v", True), ("v", 1.0), ("v", "1"), ("kind", 1)])
def test_a_consistency_proof_of_the_wrong_type_is_refused(fx, member, value):
    h = handed_over(fx)
    raw = h.value()
    raw["consistency"][0]["proof"]["proof"][member] = value
    with pytest.raises(InvalidError, match="replaced book"):
        verify_handover(Handover.loads(go_json(raw)), KEY, script.DIRECTORY)


def test_a_value_nested_too_deeply_is_refused(fx):
    """A status opening value 5000 deep parses (Python's parser and Go's take
    it) and is refused by the replay, never a RecursionError; JSON past the
    parser's own limit is refused where it is read."""
    h = handed_over(fx)
    text = go_json(h.value()).replace('"value":{}', '"value":' + "[" * 5000 + "]" * 5000, 1)
    assert "[" * 5000 in text
    with pytest.raises(InvalidError, match="nested too deeply"):
        verify_handover(Handover.loads(text), KEY, script.DIRECTORY)
    deeper = 1_000_000
    with pytest.raises(InvalidError):
        Handover.loads(go_json(h.value()).replace('"value":{}', '"value":' + "[" * deeper + "]" * deeper, 1))
    with pytest.raises(InvalidError):
        canonical_document(b"[" * deeper + b"]" * deeper, FACT_KIND_BOOK)


# -- ties (Go shelf_handover_tie_test.go) ---------------------------------------------


def tied_handover(fx) -> tuple[Handover, dict[str, str]]:
    """A shelf whose deal/a grew after its witnessed tree tick (a tie with a
    consistency proof), whose deal/b did not (a tie of one size), and whose
    deal/c was never in a witnessed tick (untied), migrated, stamped and
    handed over (Go ``tiedHandover``). Returns the handover and each log's
    book id."""
    with fx.open() as shelf:
        c, _ = stamp_cadence(shelf)
        a = shelf.open("deal/a")
        a.append(obs("a", "1"))
        shelf.open("deal/b").append(obs("b", "1"))
        tree = c.tick(START).tick
        assert c.witnessed(tree.n, script.mint_receipt(tree.checkpoint)).state == "VERIFIED"
        a.append(obs("a", "2"))
        shelf.open("deal/c").append(obs("c", "1"))
        c.migrate(START + timedelta(minutes=1))
        last = c.stamp(START + timedelta(minutes=2)).tick
        iat = int((START + timedelta(minutes=3)).timestamp())
        assert c.witnessed(last.n, script.mint_receipt(last.checkpoint, iat)).state == "VERIFIED"
        h = c.handover(0)
    books = {}
    for d in h.documents:
        f = json.loads(d.doc)
        if f["kind"] == FACT_KIND_BOOK:
            books[f["log_id"]] = f["book_id"]
    return h, books


def test_a_genesis_fact_with_its_tie_is_tied(fx):
    """A genesis fact handed over with its tie verifies and is not untied;
    one whose log no witnessed tick held is untied."""
    h, books = tied_handover(fx)
    assert len(h.ties) == 2
    by_log = {t.log_id: t for t in h.ties}
    assert by_log["deal/a"].consistency is not None, "deal/a grew after its witnessed tick"
    assert by_log["deal/b"].consistency is None, "deal/b did not"
    assert [t.book_id for t in h.ties] == sorted(t.book_id for t in h.ties), "ties are in book id order"
    got = verify_handover(h, KEY, script.DIRECTORY)
    assert got.untied == [books["deal/c"]]
    # Round trip: the ties are JSON a reader in another language reads.
    again = copy_of(h)
    assert go_json(again.value()) == go_json(h.value())
    assert go_json(verify_handover(again, KEY, script.DIRECTORY).value()) == go_json(got.value())

    # Without its ties every genesis fact is untied, and still recomputed.
    got = verify_handover(dataclasses.replace(h, ties=[]), KEY, script.DIRECTORY)
    assert len(got.untied) == 3


def _flip(s: str) -> str:
    return ("1" if s[0] == "0" else "0") + s[1:]


def test_a_tie_that_does_not_verify_fails_the_handover(fx):
    """Each change to a tie fails the handover, naming the tie."""
    h, books = tied_handover(fx)
    index = {t.log_id: i for i, t in enumerate(h.ties)}
    a, b = index["deal/a"], index["deal/b"]
    ta, tb = h.ties[a], h.ties[b]
    changes = {
        "salt": (a, {"salt": _flip(ta.salt)}),
        "index": (a, {"index": ta.index ^ 1}),
        "path": (a, {"path": ta.path[:3] + [_flip(ta.path[3])] + ta.path[4:]}),
        "short path": (a, {"path": ta.path[1:]}),
        "entry": (a, {"entry_seq": ta.entry_seq + 1}),
        "size": (b, {"size": tb.size + 1}),
        "old checkpoint": (a, {"old_checkpoint": tb.old_checkpoint}),
        "no consistency": (a, {"consistency": None}),
        "consistency on a size": (b, {"consistency": ta.consistency}),
        "cadence checkpoint": (a, {"cadence_checkpoint": h.anchor}),
        "no receipt": (a, {"receipts": []}),
        "another book": (a, {"book_id": books["deal/b"]}),
        "no genesis": (a, {"book_id": "no-such-book"}),
    }
    for name, (i, change) in changes.items():
        ties = list(h.ties)
        ties[i] = dataclasses.replace(ties[i], **change)
        with pytest.raises(InvalidError, match="tie"):
            verify_handover(dataclasses.replace(h, ties=ties), KEY, script.DIRECTORY)
            pytest.fail(name)
    # A reader whose directory names no witness verifies no tie.
    with pytest.raises(InvalidError, match="tie"):
        verify_handover(h, KEY, {"witnesses": []})


@pytest.mark.parametrize("member,value", [("ties", 1), ("ties", [1]), ("consistency", 1), ("path", "00"),
                                          ("size", -1), ("cadence_checkpoint", "not base64!")])
def test_a_tie_of_the_wrong_type_is_refused(fx, member, value):
    h, _ = tied_handover(fx)
    raw = json.loads(go_json(h.value()))
    if member == "ties":
        raw["ties"] = value
    else:
        raw["ties"][0][member] = value
    with pytest.raises(InvalidError, match="handover"):
        Handover.loads(json.dumps(raw))


def test_a_tie_path_in_uppercase_hex_is_malformed(fx):
    """Go decodes the path's hex and requires it to re-encode as given."""
    h, _ = tied_handover(fx)
    ties = list(h.ties)
    ties[0] = dataclasses.replace(ties[0], path=[ties[0].path[0].upper()] + ties[0].path[1:])
    with pytest.raises(InvalidError, match="a malformed path"):
        verify_handover(dataclasses.replace(h, ties=ties), KEY, script.DIRECTORY)
