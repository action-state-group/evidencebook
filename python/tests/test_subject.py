# SPDX-License-Identifier: Apache-2.0
"""The subject convention v0 (evidencebook#116), as Go's subject_test.go and
subject_readers_test.go check it: subject_root and subject_close and their
refusals, the subject_id index, the correlation reader and the subject
bundle verifier over the shared vectors (testdata/subject)."""
from __future__ import annotations

import copy
import hashlib
import json
from dataclasses import replace
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs

import evidencebook as eb
from evidencebook import _gostruct
from evidencebook._index import SortedMerkleIndex, verify_key_range
from evidencebook.book import _View
from evidencebook.subject import (
    CLOSES,
    UNINDEXED,
    SubjectRefused,
    Subjects,
    correlation_of,
    new_subject_id_index,
    MEMBERSHIP_EXCHANGE_ID,
    MEMBERSHIP_FIELD_PREFIX,
    MEMBERSHIP_LOG,
    RECORD_TYPE_SUBJECT_CLOSE,
    RECORD_TYPE_SUBJECT_ROOT,
    SUBJECT_ID_INDEX_NAME,
    PeerRecord,
    SubjectCloseStatement,
    SubjectRootStatement,
    read_subject_close,
    read_subject_root,
    subjects_of_record,
)
from evidencebook.subject_bundle import (
    SUBJECT_BUNDLE_PROJECTION,
    strict_header,
    verify_subject_bundle,
)

from test_book import Clock, book

VECTORS = Path(__file__).resolve().parents[2] / "testdata" / "subject"
SUBJECT = "exchange-1"


@pytest.fixture
def b():
    return book(Clock(), "holder")


def root_of(b: eb.Book, subject_id: str = SUBJECT, membership: str = MEMBERSHIP_EXCHANGE_ID, kind: str = "exchange"):
    return b.seal_subject_root(SubjectRootStatement(kind, subject_id, membership))


def member(root: eb.Record, subject_id: str = SUBJECT, payload: bytes = b"p") -> eb.Entry:
    return eb.Entry("exchange", eb.OBSERVED_EVENT, correlation=eb.Correlation(exchange_id=subject_id),
                    links=(eb.Link(eb.CITES, root.record_id),), payloads=(payload,))


def dg(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()


# -- admission (TestSubjectRootRefusals and its neighbours) -----------------------------------


def test_subject_root_refusals(b):
    root = root_of(b)
    assert root.header.statement == {"subject_kind": "exchange", "subject_id": SUBJECT, "membership": MEMBERSHIP_EXCHANGE_ID}
    assert root.header.epistemic_type == eb.PRODUCER_CLAIM and root.header.links == ()
    with pytest.raises(eb.InvalidError, match="already holds"):
        root_of(b)
    with pytest.raises(eb.InvalidError, match="first record"):
        root_of(b, "deal-1", MEMBERSHIP_LOG, "deal")
    for membership in ("", "field:", "field:x", "field:/a~2", "field:/a~", "logs"):
        with pytest.raises(eb.InvalidError):
            root_of(b, "x-" + membership, membership)
    with pytest.raises(eb.InvalidError, match="unknown field"):
        b.append(eb.Entry(RECORD_TYPE_SUBJECT_ROOT, eb.PRODUCER_CLAIM,
                          statement={"subject_kind": "exchange", "subject_id": "y", "membership": "log", "extra": 1}))
    with pytest.raises(eb.InvalidError, match="carries the subject id"):
        b.append(member(root, "another-exchange"))

    other = root_of(b, "exchange-2")
    both = member(root)
    both = replace(both, links=both.links + (eb.Link(eb.CITES, other.record_id),), subject_ref="exchange-2")
    with pytest.raises(eb.InvalidError, match="carries the subject id"):
        b.append(both)

    m1 = b.append(member(root, payload=b"one"))
    with pytest.raises(eb.InvalidError, match="the book holds 1"):
        b.append(eb.Entry(RECORD_TYPE_SUBJECT_CLOSE, eb.PRODUCER_CLAIM, links=(eb.Link(CLOSES, root.record_id),),
                          statement={"members": 2, "last": m1.record_id}))
    closed = b.close_subject(root.record_id)
    assert read_subject_close(closed.header.statement) == SubjectCloseStatement(1, m1.record_id)
    assert closed.header.links == (eb.Link(CLOSES, root.record_id),)
    with pytest.raises(eb.InvalidError, match="already closed"):
        b.close_subject(root.record_id)
    with pytest.raises(eb.NotFoundError):
        b.close_subject(dg("no-such-root"))
    # Worked case (iv): a member sealed after the close is refused.
    with pytest.raises(eb.InvalidError, match="after its close"):
        b.append(member(root, payload=b"late"))
    # The holder's own bookkeeping still runs on a closed subject.
    b.checkpoint()
    b.checkpoint()


def test_a_close_names_one_root_of_this_book(b):
    root = root_of(b)
    for links in ((), (eb.Link(CLOSES, root.record_id), eb.Link(CLOSES, dg("x")))):
        with pytest.raises(eb.InvalidError, match="exactly one"):
            b.append(eb.Entry(RECORD_TYPE_SUBJECT_CLOSE, eb.PRODUCER_CLAIM, links=links,
                              statement={"members": 0, "last": ""}))
    with pytest.raises(eb.InvalidError, match="not a subject_root of this book"):
        b.append(eb.Entry(RECORD_TYPE_SUBJECT_CLOSE, eb.PRODUCER_CLAIM, links=(eb.Link(CLOSES, dg("x")),),
                          statement={"members": 0, "last": ""}))
    with pytest.raises(eb.InvalidError, match="statement"):
        b.append(eb.Entry(RECORD_TYPE_SUBJECT_CLOSE, eb.PRODUCER_CLAIM, links=(eb.Link(CLOSES, root.record_id),)))


def test_a_close_statement_is_decoded_as_go_decodes_it(b):
    # Go's struct decoding: missing and null members are zero, names match
    # case-insensitively, an unknown member is refused.
    root = root_of(b)
    closed = b.append(eb.Entry(RECORD_TYPE_SUBJECT_CLOSE, eb.PRODUCER_CLAIM, links=(eb.Link(CLOSES, root.record_id),),
                               statement={"MEMBERS": 0, "last": None}))
    assert closed.header.record_type == RECORD_TYPE_SUBJECT_CLOSE
    assert read_subject_close({}) == SubjectCloseStatement(0, "")
    # Canonical order puts "LAST" before "last"; a later null leaves it.
    assert read_subject_close({"last": None, "LAST": "x"}) == SubjectCloseStatement(0, "x")
    assert read_subject_root({"SUBJECT_ID": "a", "ſubject_kind": "k", "membership": "log"}) == \
        SubjectRootStatement("k", "a", "log")
    for bad in ({"members": -1}, {"members": "2"}, {"members": True}, {"x": 1}, {"members": 2.5}, [], "s"):
        with pytest.raises(eb.InvalidError):
            read_subject_close(bad)


def test_a_record_is_a_member_of_one_subject(b):
    a = root_of(b, "a")
    other = root_of(b, "b", "field:/subject_ref", "session")
    for links in ((eb.Link(eb.CITES, a.record_id), eb.Link(eb.CITES, other.record_id)),
                  (eb.Link(eb.CITES, other.record_id), eb.Link(eb.CITES, a.record_id))):
        with pytest.raises(eb.InvalidError, match="one subject"):
            b.append(eb.Entry("turn", eb.OBSERVED_EVENT, correlation=eb.Correlation(exchange_id="a"),
                              subject_ref="b", links=links))
    b.append(eb.Entry("turn", eb.OBSERVED_EVENT, subject_ref="b", links=(eb.Link(eb.CITES, other.record_id),)))


def test_a_per_subject_book_roots_one_subject():
    b = book(Clock(), "deal")
    root = b.seal_subject_root(SubjectRootStatement("deal", "deal-1", MEMBERSHIP_LOG))
    assert root.seq == 1
    with pytest.raises(eb.InvalidError, match="per-subject book"):
        root_of(b, "other")
    m = b.append(eb.Entry("turn", eb.OBSERVED_EVENT))
    b.checkpoint()
    closed = b.close_subject(root.record_id)
    # The index root the checkpoint wrote is bookkeeping, not a member.
    assert read_subject_close(closed.header.statement) == SubjectCloseStatement(1, m.record_id)
    with pytest.raises(eb.InvalidError, match="after its close"):
        b.append(eb.Entry("turn", eb.OBSERVED_EVENT))
    b.checkpoint()


def test_membership_pointers_follow_rfc_6901(b):
    for pointer, statement, subject_id in (
            ("/statement/", {"": "s-empty"}, "s-empty"),
            ("/statement/a~1b", {"a/b": "s-slash"}, "s-slash"),
            ("/statement/a~0b", {"a~b": "s-tilde"}, "s-tilde"),
            ("/statement/list/1", {"list": ["x", "s-index"]}, "s-index")):
        root = root_of(b, subject_id, MEMBERSHIP_FIELD_PREFIX + pointer, "session")
        b.append(eb.Entry("turn", eb.OBSERVED_EVENT, statement=statement, links=(eb.Link(eb.CITES, root.record_id),)))
        with pytest.raises(eb.InvalidError):
            b.append(eb.Entry("turn", eb.OBSERVED_EVENT, statement={"other": 1},
                              links=(eb.Link(eb.CITES, root.record_id),)))
    for token in ("01", "+1", "-0", "1 "):
        root = root_of(b, f"t{token}", f"field:/statement/list/{token}", "session")
        with pytest.raises(eb.InvalidError):
            b.append(eb.Entry("turn", eb.OBSERVED_EVENT, statement={"list": [f"t{token}", f"t{token}"]},
                              links=(eb.Link(eb.CITES, root.record_id),)))


def test_close_subject_counts_a_pending_member(b):
    root = root_of(b)
    real = b._substrate.append
    failures = [1]

    def flaky(record_id, at):
        if failures[0]:
            failures[0] -= 1
            raise OSError("substrate down")
        return real(record_id, at)

    b._substrate.append = flaky
    with pytest.raises(eb.CorruptError):
        b.append(member(root, payload=b"pending"))
    closed = b.close_subject(root.record_id)
    assert read_subject_close(closed.header.statement).members == 1

    failures[0] = 1
    with pytest.raises(eb.CorruptError):
        root_of(b, "late-root")
    pending = b._pending[0].record_id
    b.close_subject(pending)  # a pending root joins the log before the close looks it up


# -- the subject_id index and checkpoints ------------------------------------------------------


def _committed(b: eb.Book) -> dict[str, dict]:
    out = {}
    for r in b.records():
        if r.header.record_type == "index_root":
            out[r.header.statement["index"]] = r.header.statement
    return out


def test_a_book_with_no_subject_commits_what_it_did(clock=None):
    def write(indexes):
        b = book(Clock(), "holder", indexes=indexes)
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref="s"))
        b.append(eb.Entry("exchange", eb.OBSERVED_EVENT, correlation=eb.Correlation(exchange_id="e")))
        b.checkpoint()
        b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref="s"))
        cp = b.checkpoint()
        return [r.record_id for r in b.records()], cp

    two = [SortedMerkleIndex("record_id", lambda r: r.record_id),
           SortedMerkleIndex("subject_ref", lambda r: r.header.subject_ref or None)]
    before, before_cp = write(two)
    after, after_cp = write(None)
    assert before == after
    assert (before_cp.root, before_cp.entries) == (after_cp.root, after_cp.entries)


def test_the_subject_id_index_commits_root_members_and_close(b):
    root = root_of(b)
    m = b.append(member(root))
    other = b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref=SUBJECT))
    closed = b.close_subject(root.record_id)
    b.checkpoint()
    index = next(i for i in b._indexes if i.name == SUBJECT_ID_INDEX_NAME)
    committed = _committed(b)[SUBJECT_ID_INDEX_NAME]
    assert committed["size"] == 3 and {k: committed[k] for k in ("index", "root", "size")} == index.root()
    proof = index.prove_range(SUBJECT, SUBJECT)
    assert sorted(verify_key_range(index.root(), SUBJECT, SUBJECT, proof)) == \
        sorted([root.record_id, m.record_id, closed.record_id])
    assert other.record_id not in [x["record_id"] for x in proof["matches"]]
    # The proof survives the JSON the extension carries it in, and a changed one fails.
    wire = json.loads(json.dumps(proof))
    assert verify_key_range(index.root(), SUBJECT, SUBJECT, wire)
    for edit in (lambda p: p["matches"].pop(), lambda p: p.__setitem__("first", 1),
                 lambda p: p["matches"][0].__setitem__("record_id", dg("x")), lambda p: p.__setitem__("lo", "a")):
        bad = copy.deepcopy(wire)
        edit(bad)
        with pytest.raises(eb.InvalidError):
            verify_key_range(index.root(), SUBJECT, SUBJECT, bad)
    empty = index.prove_range("zzz", "zzz")
    assert verify_key_range(index.root(), "zzz", "zzz", empty) == [] and "left" in empty


def test_the_subject_index_survives_reopen():
    store = eb.MemoryStore()
    b = book(Clock(), "holder", store=store)
    root = root_of(b)
    b.append(member(root, payload=b"one"))
    b.close_subject(root.record_id)
    b.checkpoint()
    before = next(i for i in b._indexes if i.name == SUBJECT_ID_INDEX_NAME).root()
    again = book(Clock(), "holder", store=store)
    assert next(i for i in again._indexes if i.name == SUBJECT_ID_INDEX_NAME).root() == before
    with pytest.raises(eb.InvalidError, match="after its close"):
        again.append(member(root, payload=b"late"))


def test_window_and_book_close_records_are_not_indexed(b):
    b.append(eb.Entry("window_open", eb.PRODUCER_CLAIM, subject_ref="w"))
    b.checkpoint()
    assert _committed(b)["record_id"]["size"] == 0
    assert _committed(b)["subject_ref"]["size"] == 0
    b.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    b.append(eb.Entry("book_close", eb.PRODUCER_CLAIM))
    n = b.size()
    b.checkpoint()
    assert b.size() == n, "a book closed into a windowed store commits no index root after its close"


# -- the correlation reader ------------------------------------------------------------------------


def test_correlation_reads_a_cited_root_first(b):
    root = root_of(b, "s-1", "field:/subject_ref", "session")
    m = b.append(eb.Entry("turn", eb.OBSERVED_EVENT, subject_ref="s-1", correlation=eb.Correlation(exchange_id="e-9"),
                          links=(eb.Link(eb.CITES, root.record_id),)))
    old = b.append(eb.Entry("turn", eb.OBSERVED_EVENT, correlation=eb.Correlation(exchange_id="e-9")))
    view = _View(b)
    assert view.correlation_of(m.header) == "s-1"
    assert view.correlation_of(old.header) == "e-9", "a record citing no root reads as before"
    assert view.correlated("s-1") == [m.seq - 1]


# -- subjects_of_record (subject_readers_test.go) -----------------------------------------------------


def peers(b: eb.Book) -> list[PeerRecord]:
    return [PeerRecord(r.record_id, r.seq, r.header, True, True) for r in b.records()]


def edited(records, record_id, **change):
    return [replace(r, **change) if r.record_id == record_id else r for r in records]


def test_subjects_of_record_reads_a_field_member(b):
    root = root_of(b)
    m = b.append(member(root, payload=b"half"))
    plain = b.append(eb.Entry("observation", eb.OBSERVED_EVENT, subject_ref="other"))
    records = peers(b)
    assert subjects_of_record(records, m.record_id) == [SUBJECT]
    assert subjects_of_record(records, root.record_id) == []
    assert subjects_of_record(records, plain.record_id) == []
    assert subjects_of_record(records, "not-a-record") == []
    assert subjects_of_record(edited(records, m.record_id, header_verified=False), m.record_id) == []
    assert subjects_of_record(edited(records, root.record_id, header_verified=False), m.record_id) == []
    assert subjects_of_record([r for r in records if r.record_id != root.record_id], m.record_id) == []
    # A root of another book, or after the member, decides nothing.
    foreign = replace(root.header, book_id="other")
    assert subjects_of_record(edited(records, root.record_id, header=foreign), m.record_id) == []
    later = replace(root.header, seq=m.seq + 1)
    assert subjects_of_record(edited(records, root.record_id, header=later), m.record_id) == []


def test_subjects_of_record_reads_a_log_member_with_no_citation():
    b = book(Clock(), "deal")
    root = b.seal_subject_root(SubjectRootStatement("deal", "deal-1", MEMBERSHIP_LOG))
    m = b.append(eb.Entry("turn", eb.OBSERVED_EVENT))
    b.checkpoint()
    records = peers(b)
    assert subjects_of_record(records, m.record_id) == ["deal-1"]
    index_root = next(r for r in records if r.header.record_type == "index_root")
    assert subjects_of_record(records, index_root.record_id) == [], "bookkeeping is no member"
    moved = replace(root.header, seq=2)
    assert subjects_of_record(edited(records, root.record_id, header=moved), m.record_id) == []


def test_subjects_of_record_names_every_subject_of_a_dishonest_book(b):
    a = root_of(b, "a")
    other = root_of(b, "b", "field:/subject_ref", "session")
    m = b.append(eb.Entry("turn", eb.OBSERVED_EVENT, subject_ref="b", links=(eb.Link(eb.CITES, other.record_id),)))
    # The book refuses a record in two subjects; a forged one is read as it is.
    forged = replace(m.header, links=(eb.Link(eb.CITES, a.record_id), eb.Link(eb.CITES, other.record_id)),
                     correlation=eb.Correlation(exchange_id="a"))
    assert subjects_of_record(edited(peers(b), m.record_id, header=forged), m.record_id) == ["a", "b"]


# -- the subject bundle verifier ---------------------------------------------------------------------


def _vectors():
    return json.loads((VECTORS / "vectors.json").read_text())["vectors"]


@pytest.mark.parametrize("case", _vectors(), ids=lambda c: c["file"])
def test_subject_vectors(case):
    data = (VECTORS / case["file"]).read_bytes()
    if not case["valid"]:
        with pytest.raises(eb.InvalidError):
            verify_subject_bundle(data)
        return
    got = verify_subject_bundle(data)
    assert (got.form, got.level, got.subject.subject_id, got.member_count, got.closed) == \
        (case["form"], case["level"], case["subject_id"], case["member_count"], case.get("closed", False))
    if got.form != SUBJECT_BUNDLE_PROJECTION:
        assert got.root and len(got.members) == got.member_count
        assert got.closed == bool(got.close)


def _full() -> dict:
    return json.loads((VECTORS / "full-field.json").read_bytes())


def _edit_extension(bundle: dict, edit) -> bytes:
    ext = bundle["extensions"]["evidencebook/subject"]
    edit(ext)
    return jcs(bundle)


@pytest.mark.parametrize("edit", [
    lambda e: e["members"].reverse(),
    lambda e: e["members"].pop(),
    lambda e: e.__setitem__("closed", False),
    lambda e: e.__setitem__("close", ""),
    lambda e: e.__setitem__("level", "log_prefix"),
    lambda e: e.__setitem__("level", "nonsense"),
    lambda e: e.__setitem__("closure_depth", 0),
    lambda e: e.__setitem__("held_by_digest", ["0" * 64]),
    lambda e: e["subject"].__setitem__("subject_id", "exchange-2"),
    lambda e: e.__setitem__("derivation", "subject_bundle/1"),
    lambda e: e.__setitem__("unknown", 1),
    lambda e: e.__setitem__("key_range", None),
    lambda e: e["key_range"]["proof"]["matches"].pop(),
    lambda e: e["key_range"].__setitem__("root_record_id", e["root"]),
], ids=["reordered", "omits", "closed-false", "close-empty", "wrong-level", "unknown-level", "depth-0", "held-by",
        "subject-id", "derivation", "unknown-member", "no-key-range", "proof-short", "root-record"])
def test_a_changed_extension_is_refused(edit):
    verify_subject_bundle(jcs(_full()))
    with pytest.raises(eb.InvalidError):
        verify_subject_bundle(_edit_extension(_full(), edit))


def test_an_extension_member_matched_by_case_is_read_as_go_reads_it():
    data = _edit_extension(_full(), lambda e: e.__setitem__("Closed", e.pop("closed")))
    assert verify_subject_bundle(data).closed


def test_a_bundle_without_the_extension_or_anchor_is_refused():
    bundle = _full()
    del bundle["extensions"]["evidencebook/subject"]
    with pytest.raises(eb.InvalidError, match="no evidencebook/subject"):
        verify_subject_bundle(jcs(bundle))
    with pytest.raises(eb.InvalidError):
        verify_subject_bundle(b"[]")
    with pytest.raises(eb.InvalidError):
        verify_subject_bundle(b"{")


def test_a_projection_is_checked_for_its_shape():
    data = json.loads((VECTORS / "projection.json").read_bytes())
    got = verify_subject_bundle(jcs(data))
    assert (got.form, got.subject.subject_id) == (SUBJECT_BUNDLE_PROJECTION, data["subject_id"])
    for edit in (lambda p: p.__setitem__("completeness", "holder_commitment"), lambda p: p.__setitem__("subject_id", ""),
                 lambda p: p.__setitem__("members", None), lambda p: p.__delitem__("root"),
                 lambda p: p["root"].__setitem__("seq", 1), lambda p: p.__setitem__("extra", 1)):
        bad = copy.deepcopy(data)
        edit(bad)
        with pytest.raises(eb.InvalidError):
            verify_subject_bundle(jcs(bad))
    # Go decodes the projection's own text: a fraction in a projected field is read.
    fractional = copy.deepcopy(data)
    fractional["root"]["statement"] = {"score": 1.5}
    assert verify_subject_bundle(json.dumps(fractional).encode()).form == SUBJECT_BUNDLE_PROJECTION
    # Go's probe matches the form by case-insensitive name.
    folded = {("FORM" if k == "form" else k): v for k, v in data.items()}
    assert verify_subject_bundle(jcs(folded)).form == SUBJECT_BUNDLE_PROJECTION


# -- the strict header reader ----------------------------------------------------------------------


def _header(**change) -> dict:
    value = {"v": 1, "book_id": "b", "seq": 1, "record_type": "turn", "epistemic_type": "observed_event",
             "committed_at": "2026-11-28T00:00:00Z", "links": []}
    value.update(change)
    return value


def test_the_strict_header_reader_is_gos():
    header, value = strict_header(_header(statement=None))
    assert value["statement"] is None and header.statement is None
    strict_header(_header(payload_commitments=["a" * 64], retention_state="WITHHELD"))
    for bad in (_header(seq=-1), _header(seq=True), _header(book_id=7), _header(seq=0), _header(links=None),
                _header(SEQ=2), _header(extra=1), _header(retention_state="NONE", payload_commitments=["a" * 64]),
                _header(v=2), _header(links=[{"type": "nope", "target": "a" * 64}])):
        with pytest.raises(eb.InvalidError):
            strict_header(bad)


def test_go_struct_decoding_of_nested_values():
    spec = _gostruct.Struct({"a": _gostruct.List(_gostruct.UINT64), "p": _gostruct.Ptr(_gostruct.Struct({"x": _gostruct.STRING})),
                             "t": _gostruct.TIME, "b": _gostruct.BYTES, "r": _gostruct.RAW})
    got = _gostruct.decode({"a": [1, 2], "P": {"X": "y"}, "t": "2026-11-28T00:00:00.5+01:00", "b": "AAE=", "r": None}, spec, "v")
    assert got == {"a": [1, 2], "p": {"x": "y"}, "t": "2026-11-28T00:00:00.5+01:00", "b": b"\x00\x01", "r": _gostruct.JSON_NULL}
    assert _gostruct.decode({}, spec, "v")["p"] is None
    for bad in ({"t": "2026-11-28t00:00:00Z"}, {"t": "2026-02-30T00:00:00Z"}, {"t": "2016-12-31T23:59:60Z"},
                {"b": "AAE"}, {"a": [1, -1]}, {"a": {}}):
        with pytest.raises(eb.InvalidError):
            _gostruct.decode(bad, spec, "v")


# -- the admission vectors (testdata/subject/admission.json, Go-written) -------------------------------


def _admission_cases():
    return json.loads((VECTORS / "admission.json").read_text())["cases"]


@pytest.mark.parametrize("case", _admission_cases(), ids=lambda c: c["name"])
def test_admission_vectors(case):
    subjects, log, index = Subjects(), [], new_subject_id_index()
    for i, step in enumerate(case["steps"]):
        header = eb.Header.from_value(step["header"])
        record = eb.Record(step["record_id"], header.seq, header)
        try:
            subjects.check(header, lambda seq: log[seq:])
        except SubjectRefused as exc:
            got = exc.reason
        else:
            got = "ok"
            subjects.track(record)
            log.append(record)
            if header.record_type != "index_root" and header.record_type not in UNINDEXED:
                index.add(record)
        assert got == step["expect"], f"step {i}: {step.get('note')}"
        assert index.root() == step["subject_id_index"], f"step {i}: {step.get('note')}"
        assert (index.root()["size"] > 0) == step["checkpoint_commits"], f"step {i}"
    roots = {r.record_id: s.statement.subject_id for s in subjects for r in (s.root,)}
    assert [{"record_id": r.record_id, "held_under": correlation_of(r.header, roots)} for r in log] == case["correlation"]


# -- Go's decoding, case by case (review round 1) ---------------------------------------------------------


def test_admission_reads_a_statement_in_the_order_given(b):
    # Go decodes the entry's statement text in order: the later member wins.
    with pytest.raises(eb.InvalidError, match="subject_id"):
        b.append(eb.Entry(RECORD_TYPE_SUBJECT_ROOT, eb.PRODUCER_CLAIM, statement={
            "subject_kind": "k", "subject_id": "s", "SUBJECT_ID": "", "membership": "log"}))
    assert read_subject_root({"SUBJECT_ID": "", "subject_id": "s", "subject_kind": "k", "membership": "log"}).subject_id == "s"


@pytest.mark.parametrize("name,ok", [("subject_\u0131d", False), ("subject_\u0130d", False), ("\u017fubject_id", True),
                                     ("SUBJECT_ID", True), ("subject_\u212ad", False)])
def test_struct_fields_fold_as_go_folds_them(name, ok):
    statement = {"subject_kind": "k", json.loads(f'"{name}"'): "s", "membership": "log"}
    if ok:
        assert read_subject_root(statement).subject_id == "s"
    else:
        with pytest.raises(eb.InvalidError):
            read_subject_root(statement)


@pytest.mark.parametrize("pair,collide", [(("\u00b5", "\u03bc"), True), (("k", "\u212a"), True), (("s", "\u017f"), True),
                                          (("A", "a"), True), (("\u01c4", "\u01c5"), True), (("\u0130", "i"), False),
                                          (("\u0131", "I"), False)])
def test_keys_folding_equal_under_go_simple_fold_are_refused(pair, collide):
    # Go's foldKey: the least rune of each unicode.SimpleFold orbit.
    bundle = _full()
    a, c = pair
    bundle["extensions"]["x/" + a], bundle["extensions"]["x/" + c] = 1, 2
    if collide:
        with pytest.raises(eb.InvalidError, match="differ only by case"):
            verify_subject_bundle(jcs(bundle))
    else:
        verify_subject_bundle(jcs(bundle))


@pytest.mark.parametrize("text,ok", [
    ("2026-11-28T0:00:00Z", True), ("2026-11-28T00:00:00,5Z", True), ("2026-11-28T00:00:00+24:00", True),
    ("2026-11-28T00:00:00-00:00", True), ("2026-11-28T00:00:00.1234567891234Z", True), ("0000-01-01T00:00:00Z", True),
    ("2026-11-28T00:00:00.Z", False), ("2026-11-28T24:00:00Z", False), ("2026-11-28T00:00:60Z", False),
    ("\uff12026-11-28T00:00:00Z", False), ("2026-11-28t00:00:00Z", False), ("2026-11-28T00:00:00z", False),
    ("-026-11-28T00:00:00Z", False), ("2026-02-29T00:00:00Z", False), ("2026-11-28T00:00:00+0100", False),
    ("2026-11-28T00:00:00+25:00", False), ("2026-11-28T00:00:00+01:61", False),
])
def test_anchor_times_are_read_as_go_reads_them(text, ok):
    text = json.loads(f'"{text}"')
    assert _gostruct.go_time(text) is ok
    bundle = _full()
    bundle["extensions"]["evidencebook/subject"]["key_range"]["anchor"]["issued_at"] = text
    if ok:
        verify_subject_bundle(jcs(bundle))
    else:
        with pytest.raises(eb.InvalidError):
            verify_subject_bundle(jcs(bundle))


def test_an_anchor_statement_may_be_an_array_of_bytes():
    for statement, ok in (([0, 1], True), ([None, 255], True), ([256], False), ([True], False), ("AAE", False)):
        bundle = _full()
        bundle["extensions"]["evidencebook/subject"]["key_range"]["anchor"]["statement"] = statement
        if ok:
            verify_subject_bundle(jcs(bundle))
        else:
            with pytest.raises(eb.InvalidError):
                verify_subject_bundle(jcs(bundle))


def test_an_overlong_pointer_index_matches_nothing(b):
    token = "1" * 5000
    root = root_of(b, "deep", f"field:/statement/list/{token}", "session")
    with pytest.raises(eb.InvalidError, match="carries the subject id"):
        b.append(eb.Entry("turn", eb.OBSERVED_EVENT, statement={"list": ["deep"]}, links=(eb.Link(eb.CITES, root.record_id),)))


def _projection() -> dict:
    return json.loads((VECTORS / "projection.json").read_bytes())


def test_a_projection_is_read_from_its_own_text():
    text = json.dumps(_projection(), separators=(",", ":"))
    # A duplicate map merges into the first, as Go decodes it: the position survives.
    merged = text[:-1] + ',"root":{"x":1}}'
    seq_first = text.replace('"root":{', '"root":{"seq":1},"root":{', 1)
    with pytest.raises(eb.InvalidError, match="no position"):
        verify_subject_bundle(seq_first.encode())
    verify_subject_bundle(merged.encode())
    # A later null empties the members; a later case-variant one replaces them.
    with pytest.raises(eb.InvalidError):
        verify_subject_bundle((text[:-1] + ',"Members":null}').encode())
    assert verify_subject_bundle((text[:-1] + ',"Members":[]}').encode()).member_count == 0
    # Nesting Go's decoder takes, and past it.
    for depth, ok in ((1500, True), (10000 - 3, True), (10001, False)):
        p = _projection()
        p["root"]["deep"] = "@"
        deep = json.dumps(p).replace('"@"', "[" * depth + "]" * depth)
        if ok:
            assert verify_subject_bundle(deep.encode()).form == SUBJECT_BUNDLE_PROJECTION
        else:
            with pytest.raises(eb.InvalidError):
                verify_subject_bundle(deep.encode())


def _projection_with(tail: str) -> bytes:
    return (json.dumps(_projection(), separators=(",", ":"))[:-1] + tail).encode()


@pytest.mark.parametrize("tail,ok", [
    # Go keeps a slice's elements past its length and decodes into them when it grows again.
    (',"members":[{},{"seq":1}],"members":[{}],"members":[{},{}]}', False),
    (',"members":[{},{},{},{"seq":1}],"members":[{},{},{}],"members":[{},{},{},{}]}', False),
    # An empty array is a new slice: nothing is kept.
    (',"members":[{},{"seq":1}],"members":[],"members":[{},{}]}', True),
    # A number out of float64 range fails in an interface value; -0 and a long integer do not.
    (',"members":[{"x":1e400}]}', False),
    (',"root":{"x":[1e999]}}', False),
    (',"members":[{"x":-0}]}', True),
    (',"root":{"x":123456789012345678901234567890}}', True),
])
def test_a_projection_is_decoded_as_go_decodes_it(tail, ok):
    if ok:
        verify_subject_bundle(_projection_with(tail))
    else:
        with pytest.raises(eb.InvalidError):
            verify_subject_bundle(_projection_with(tail))


def test_an_unpaired_surrogate_reads_as_the_replacement_character():
    got = verify_subject_bundle(_projection_with(',"subject_id":"\\ud800","subject_kind":"a\\ud83d\\ude00"}'))
    assert (got.subject.subject_id, got.subject.subject_kind) == ("\ufffd", "a\U0001f600")


def test_a_deep_object_in_a_projection_is_read_without_recursion():
    p = _projection()
    p["root"]["deep"] = "@"
    deep = json.dumps(p).replace('"@"', '{"a":' * 3000 + "1" + "}" * 3000)
    assert verify_subject_bundle(deep.encode()).form == SUBJECT_BUNDLE_PROJECTION
