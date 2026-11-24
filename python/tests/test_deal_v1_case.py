# SPDX-License-Identifier: Apache-2.0
"""An adjudicator's rules (evidencebook#779), ported from Go's
``case_check_test.go``: ``check_case``, ``check_case_record`` and
``seal_ruling``, over a deal built from the shared holder transcripts."""
from __future__ import annotations

import json
import sys
from datetime import datetime, timezone

import pytest
from agent_action_capsule.canonical import jcs

from evidencebook._capsule import seal_payload
from evidencebook.deal.v1 import (
    RulingConflictError, check_case, check_case_record, seal_ruling, verify_book,
)
from evidencebook.deal import v1
from evidencebook.deal.check import StageError
from evidencebook.deal.v1 import report_pair
from evidencebook.deal.names import REPORT_ACTION_ID
from evidencebook.deal.v1.check import decode, digest

import test_deal_v1_holder as th

ADJ = "adjudicator"
FORUM = "auto:ref"


def _copy(party, log, audience, referee="Lamp Court") -> v1.CaseCopy:
    """The party's adjudicator copy as its bundle carries it (Go's
    ``holderBundle``): the report sealed by the party's record key, and the
    x-deal-v1 extension."""
    at = party.now.strftime("%Y-%m-%dT%H:%M:%SZ")
    c = {"as_of": at, "audience": audience, "nonce": "a" * 64, "referee": referee if audience == "adjudicator" else ""}
    report, book = party.status.report_with_book(log, c)
    payload = v1.report_payload(report)
    capsule_id, capsule, envelope = seal_payload(
        REPORT_ACTION_ID, payload, {}, operator=party.name, developer="xdealv1-test",
        at=datetime(2026, 11, 21, 12, 0, 0, tzinfo=timezone.utc), signer=party.signer)
    pair = report_pair(capsule, envelope, payload)
    read = v1.verify_report(pair, book.agreement)
    return v1.CaseCopy(pair, v1.ReportBundle(read.capsule_id, book).json())


def _fixture(tmp_path, monkeypatch):
    """Go's ``caseFixture``, with the seller as the disputing side: a started
    deal naming an adjudicator that checkpoints with its record key; the
    seller sealed a dispute on as_described at seq 2 of its book and wrote
    its adjudicator copy. Returns the parties, the dispute's pair, the
    seller's copy and the deal book's log."""
    c = th.case("name-rulings")
    real = th.seed
    monkeypatch.setattr(sys.modules["test_deal_v1_holder"], "seed",
                        lambda label: real("adjudicator record key") if label == "adjudicator checkpoint key"
                        else real(label))
    parties = th.replay(c, tmp_path, 7)
    seller, adj = parties["seller"], parties["adjudicator"]
    steps = c["steps"]
    log, agreement = steps[5]["log"], steps[5]["record"]
    ad = digest(decode(jcs(agreement)))
    dispute = {"x-deal-v1": {
        "at": "2026-11-02T10:00:00Z", "canonicalization": "jcs", "deal_id": agreement["x-deal-v1"]["deal_id"],
        "profile": "x-deal-v1", "record_type": "dispute", "seq": 2, "seen": steps[6]["first"],
        "prev": v1.ref_to(ad), "agreement_ref": v1.ref_to(ad)},
        "body": {"kind": "dispute", "row": "as_described", "statements": [], "adjudicator": adj.signer.public_key().hex(),
                 "forum": FORUM}}
    seller.tick()
    book = seller.shelf.open(log)
    rec = book.append(v1.book_entry(decode(jcs(dispute)), book.records()[-1].record_id))
    st = book._stored[rec.record_id]
    pair = v1.book_pair(jcs(st.capsule), st.envelope, st.header, jcs(dispute))
    offer = steps[3]
    seller.status = v1.StatusProvider(offer_pair={digest(decode(jcs(offer["record"]))): bytes.fromhex(offer["out"])}.get)
    seller.reopen()
    seller.shelf.journal()  # a report reads the offer's journal
    seller.tick()
    seller.cadence.stamp(seller.now)
    return parties, pair, log


def _case(tmp_path, monkeypatch):
    parties, dispute, log = _fixture(tmp_path, monkeypatch)
    adj_key = parties["adjudicator"].signer.public_key().hex()
    copy = _copy(parties["seller"], log, "adjudicator")
    return parties, dispute, copy, adj_key, check_case(dispute, [copy], adj_key)


def test_check_case(tmp_path, monkeypatch):
    parties, dispute, copy, adj_key, c = _case(tmp_path, monkeypatch)
    seller, buyer = parties["seller"], parties["buyer"]
    assert c.row == "as_described"
    assert c.keys == {"buyer": buyer.signer.public_key().hex(), "seller": seller.signer.public_key().hex()}
    assert c.read["seller"] is not None and c.read["buyer"] is None, "no buyer copy: not read"
    assert c.forum == FORUM
    assert c.log_id == v1.holder.case_log_id(c.offer_digest, adj_key)
    with pytest.raises(StageError, match="another adjudicator"):
        check_case(dispute, [copy], buyer.signer.public_key().hex())
    with pytest.raises(StageError, match="one or two adjudicator copies"):
        check_case(dispute, [], adj_key)
    with pytest.raises(StageError, match="both copies are the seller's"):
        check_case(dispute, [copy, copy], adj_key)
    agreement_pair = bytes.fromhex(th.case("name-rulings")["steps"][5]["out"]["pair"])
    with pytest.raises(StageError, match="not a dispute"):
        check_case(agreement_pair, [copy], adj_key)
    keep = _copy(seller, th.case("name-rulings")["steps"][5]["log"], "keep")
    with pytest.raises(StageError, match="not an adjudicator copy"):
        check_case(dispute, [keep], adj_key)


def test_a_dispute_signed_by_neither_party_is_refused(tmp_path, monkeypatch):
    parties, dispute, copy, adj_key, _ = _case(tmp_path, monkeypatch)
    # The adjudicator re-seals the dispute's record on its own shelf: a key that is neither party's.
    read = verify_book(dispute)
    adj = parties["adjudicator"]
    adj.tick()
    book = adj.shelf.open("deal/c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0")
    first = book.append(v1.book_entry(decode(jcs(th.case("name-rulings")["steps"][5]["record"]))))
    rec = book.append(v1.book_entry(read.record, first.record_id))
    st = book._stored[rec.record_id]
    forged = v1.book_pair(jcs(st.capsule), st.envelope, st.header, v1.encode(read.record)[0])
    with pytest.raises(StageError, match="signed by neither party"):
        check_case(forged, [copy], adj_key)


def test_names_counterparty():
    from evidencebook.deal.v1.case import _names_counterparty as names

    named = {"counterparty_layer": True, "counterparty_key": "seller-key"}
    assert names(named, "seller-key")
    assert not names(named, "stranger-key"), "a copy under a key the other agreement does not name"
    assert not names({"counterparty_layer": False, "counterparty_key": None}, "any"), "no layer: no second book"
    assert not names({"counterparty_layer": None, "counterparty_key": None}, "any")


def test_seal_ruling(tmp_path, monkeypatch):
    parties, _, _, _, c = _case(tmp_path, monkeypatch)
    adj = parties["adjudicator"]
    at = datetime(2026, 11, 3, 10, 0, 0, tzinfo=timezone.utc)
    body = c.ruling_of("not_met", "seller", final=True)
    first = seal_ruling(adj.shelf, c, body, at)
    assert not first.existing and first.seq == 1 and first.record_id == first.book_id
    assert verify_book(first.pair).key_id == adj.signer.public_key().hex()

    again = seal_ruling(adj.shelf, c, body, at.replace(hour=11))
    assert again.existing and again.digest == first.digest

    with pytest.raises(RulingConflictError):
        seal_ruling(adj.shelf, c, c.ruling_of("met", None, final=True), at)
    # After a final ruling the row takes no other ruling, final or not.
    with pytest.raises(RulingConflictError):
        seal_ruling(adj.shelf, c, c.ruling_of("met", None, final=False), at)

    # A body that is not the case's is refused before anything is sealed.
    other = c.ruling_of("met")
    other["row"] = "delivered"
    with pytest.raises(StageError, match="the case on row as_described"):
        seal_ruling(adj.shelf, c, other, at)
    other = c.ruling_of("met")
    other["keys"] = {**other["keys"], "seller": adj.signer.public_key().hex()}
    with pytest.raises(StageError, match="other keys"):
        seal_ruling(adj.shelf, c, other, at)
    other = c.ruling_of("met")
    other["read"] = {**other["read"], "seller": "someone else's record"}
    with pytest.raises(StageError, match="read"):
        seal_ruling(adj.shelf, c, other, at)
    other = c.ruling_of("met")
    other["rules_digest"] = "7" * 64
    with pytest.raises(StageError, match="other rules"):
        seal_ruling(adj.shelf, c, other, at)


def test_seal_ruling_non_final(tmp_path, monkeypatch):
    parties, _, _, _, c = _case(tmp_path, monkeypatch)
    adj = parties["adjudicator"]
    at = datetime(2026, 11, 3, 10, 0, 0, tzinfo=timezone.utc)
    first = seal_ruling(adj.shelf, c, c.ruling_of("not_met", "seller"), at)
    with pytest.raises(StageError, match="not one the agreement accepts"):
        seal_ruling(adj.shelf, c, c.ruling_of("met", forum="external:elsewhere"), at)
    later = seal_ruling(adj.shelf, c, c.ruling_of("met", None, final=True), at)
    assert later.seq == 2 and later.book_id == first.record_id


def test_check_case_record(tmp_path, monkeypatch):
    parties, dispute, copy, adj_key, c = _case(tmp_path, monkeypatch)
    seller, adj = parties["seller"], parties["adjudicator"]
    sealed = seal_ruling(adj.shelf, c, c.ruling_of("not_met", "seller", final=True),
                         datetime(2026, 11, 3, 10, 0, 0, tzinfo=timezone.utc))
    log = th.case("name-rulings")["steps"][5]["log"]
    block = dict(json.loads(jcs(c.dispute.record.value))["x-deal-v1"])
    prev = c.dispute.digest

    def seal_seller(body: dict, seq: int, prev_digest: str) -> bytes:
        nonlocal block
        b = {**block, "seq": seq, "prev": v1.ref_to(prev_digest)}
        seller.tick()
        book = seller.shelf.open(log)
        rec = decode(jcs({"x-deal-v1": b, "body": body}))
        r = book.append(v1.book_entry(rec, book.records()[-1].record_id))
        st = book._stored[r.record_id]
        return v1.book_pair(jcs(st.capsule), st.envelope, st.header, v1.encode(rec)[0])

    challenge = {"kind": "challenge", "ruling": v1.ref_to(sealed.digest), "grounds_commitment": "9" * 64}
    pair = seal_seller(challenge, 3, prev)
    got = check_case_record(pair, c, [sealed.digest])
    assert got.record.body["kind"] == "challenge"
    with pytest.raises(StageError, match="does not hold"):
        check_case_record(pair, c, [])
    with pytest.raises(StageError, match="neither a withdrawal nor a challenge"):
        check_case_record(dispute, c, [])
    other = c.__class__(**{**c.__dict__, "deal_id": "deal-ffffffffffffffff"})
    with pytest.raises(StageError, match="the case of deal deal-ffffffffffffffff"):
        check_case_record(pair, other, [sealed.digest])

    d3 = got.digest
    with pytest.raises(StageError, match="the withdrawal is of row delivered"):
        check_case_record(seal_seller({"kind": "withdrawal", "row": "delivered"}, 4, d3), c, [])
    assert check_case_record(seal_seller({"kind": "withdrawal", "row": "as_described"}, 5, d3), c, []) is not None
