# SPDX-License-Identifier: Apache-2.0
"""Step 09: a referee's Result delivered into each judged party's book (evidencebook#310).

Two Goose nodes (buyer, seller) and a referee, each with its own key. The
parties' halves are signed with their node's key and open to terms that name
the referee; the referee seals its Result with capsule-engine. Each node then
receives the Result and seals it as received; every refusal is sealed too.
The Result's content is one claim built with capsule-engine's public result types, so
what is under test is delivery, not the referee's judgments (capsule-engine's
tests hold those)."""
from __future__ import annotations

import copy
import hashlib
import json
import os
import sys
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import goose_node as gn  # noqa: E402
from agent_action_capsule.canonical import jcs  # noqa: E402
from capsule_emit.bilateral import seal_action, seal_request  # noqa: E402
from capsule_emit.signing import resolve_signer  # noqa: E402

# The goose CI job installs capsule-engine and sets GOOSE_REQUIRE_REFEREE, so
# there this file fails rather than skips when it is missing.
if os.environ.get("GOOSE_REQUIRE_REFEREE"):
    from capsule_engine import referee
else:
    referee = pytest.importorskip("capsule_engine.referee")
from capsule_engine.report.result import Claim, DigestRef, DisclosureCarrier, build_result  # noqa: E402

ACTION = "buy"
AS_OF = "2026-11-01T12:00:00Z"


@pytest.fixture(autouse=True)
def _offline(monkeypatch):
    monkeypatch.setenv("CAPSULE_WITNESS", "off")
    monkeypatch.delenv("CAPSULE_LEDGER", raising=False)


class Deal:
    """One deal: two nodes, a referee book, both halves and the sealed Result."""

    def __init__(self, tmp_path: Path, monkeypatch, *, named_referee: str | None = None, signer: str = "referee") -> None:
        self.buyer = gn.Node.open(tmp_path / "buyer", operator="buyer-co")
        self.seller = gn.Node.open(tmp_path / "seller", operator="seller-co")
        self.outsider = gn.Node.open(tmp_path / "outsider", operator="outsider-co")
        self.books = {"referee": str(tmp_path / "referee" / "book"), "impostor": str(tmp_path / "impostor" / "book")}
        keys = {name: resolve_signer(book).key_id for name, book in self.books.items()}
        terms = {
            "item": "synthetic widget box",
            "buyer": {"operator": "buyer-co", "book_key_id": self.buyer.key_id, "deal_id": "deal-b"},
            "seller": {"operator": "seller-co", "book_key_id": self.seller.key_id, "deal_id": "deal-s"},
            "referee": {"key_id": named_referee or keys["referee"], "mailbox": "nostr:referee-test"},
        }
        self.terms = terms
        monkeypatch.setenv("CAPSULE_SIGNING_KEY_PATH", str(tmp_path / "buyer" / "producer.key"))
        req = seal_request("buyer-co", "goose@test", ACTION, referee.action_digest(ACTION, terms),
                           ledger=str(tmp_path / "buyer-book"), extra_compute={"peer": "seller-co"})
        monkeypatch.setenv("CAPSULE_SIGNING_KEY_PATH", str(tmp_path / "seller" / "producer.key"))
        resp = seal_action("seller-co", "goose@test", ACTION, referee.action_digest(ACTION, terms),
                           req.capsule_id, ledger=str(tmp_path / "seller-book"), extra_compute={"peer": "buyer-co"})
        monkeypatch.delenv("CAPSULE_SIGNING_KEY_PATH")
        self.buyer_half, self.seller_half = req.capsule, resp.capsule
        evidence = (DigestRef(self.buyer_half["capsule_id"]), DigestRef(self.seller_half["capsule_id"]))
        claim = Claim(id="deal-terms/item-agreed", contract_ref=referee.DEAL_TERMS_CONTRACT,
                      requirement_ref="item-agreed", tier="recomputed", grade="self-attested",
                      sufficiency="SATISFIED", verdict="met", evidence=evidence, proofs=(),
                      presentation=DisclosureCarrier(status="SATISFIED", evidence=evidence))
        self.doc = build_result([claim], generated_at=AS_OF).to_dict()
        self.octets = jcs(self.doc)
        self.result = referee.seal_result(self.doc, buyer_half=self.buyer_half, seller_half=self.seller_half,
                                          ledger=self.books[signer], operator="referee-co", developer="referee@test").capsule

    def parties(self, terms: dict | None = None) -> dict:
        terms = terms or self.terms
        return {"buyer": {"half": self.buyer_half, "action": ACTION, "terms": terms},
                "seller": {"half": self.seller_half, "action": ACTION, "terms": terms}}

    def close(self) -> None:
        for node in (self.buyer, self.seller, self.outsider):
            node.close()


@pytest.fixture
def deal(tmp_path, monkeypatch):
    d = Deal(tmp_path, monkeypatch)
    yield d
    d.close()


def _held(node: gn.Node, record_type: str) -> list[dict]:
    return [st for _, st in node.notes(record_type)]


def test_both_parties_hold_the_identical_result(deal):
    for node in (deal.buyer, deal.seller):
        node.receive_dispute_result(deal.result, deal.octets, deal.parties())
    digest = hashlib.sha256(deal.octets).hexdigest()
    assert digest == referee.result_digest(deal.doc)
    held = [_held(n, gn.DISPUTE_RECEIVED) for n in (deal.buyer, deal.seller)]
    assert held[0] == held[1] and len(held[0]) == 1
    assert held[0][0]["result_digest"] == digest
    assert held[0][0]["referee"] == deal.terms["referee"]["key_id"]
    assert _held(deal.buyer, gn.DISPUTE_REFUSED) == []


def _refused(node: gn.Node, deal: Deal, code: str, **overrides) -> None:
    args = {"capsule": deal.result, "octets": deal.octets, "parties": deal.parties(), **overrides}
    with pytest.raises(gn.Refused, match=code):
        node.receive_dispute_result(args["capsule"], args["octets"], args["parties"])
    refusals = _held(node, gn.DISPUTE_REFUSED)
    assert refusals[-1] == {"result_digest": hashlib.sha256(args["octets"]).hexdigest(), "code": code}
    assert _held(node, gn.DISPUTE_RECEIVED) == []


def test_a_result_not_signed_by_the_named_referee_is_refused_and_recorded(tmp_path, monkeypatch):
    d = Deal(tmp_path, monkeypatch, signer="impostor")
    try:
        _refused(d.buyer, d, "not_named_referee")
    finally:
        d.close()


def test_terms_forged_to_name_the_signer_do_not_pass(tmp_path, monkeypatch):
    """An impostor-signed Result with terms forged to name the impostor: verify_referee_result
    alone would accept it, so the openings are bound to the signed halves first."""
    d = Deal(tmp_path, monkeypatch, signer="impostor")
    try:
        forged = copy.deepcopy(d.terms)
        forged["referee"]["key_id"] = d.result["key_id"]
        _refused(d.buyer, d, "opening_mismatch", parties=d.parties(forged))
    finally:
        d.close()


def test_a_half_that_does_not_verify_is_refused(deal):
    parties = deal.parties()
    parties["buyer"]["half"] = {**parties["buyer"]["half"], "action": "buy-more"}
    _refused(deal.buyer, deal, "half_unverified", parties=parties)


def test_halves_signed_by_one_key_are_refused(deal):
    parties = deal.parties()
    parties["seller"]["half"] = parties["buyer"]["half"]
    _refused(deal.buyer, deal, "halves_unpaired", parties=parties)


def test_a_float_in_the_octets_is_refused_and_recorded(deal):
    _refused(deal.buyer, deal, "not_a_result", octets=b'{"x":1.5}')


def test_terms_that_do_not_open_the_half_are_refused_and_recorded(deal):
    parties = deal.parties()
    parties["buyer"]["terms"] = ["not", "an", "object"]
    _refused(deal.buyer, deal, "opening_mismatch", parties=parties)


def test_a_node_that_sealed_neither_half_refuses(deal):
    _refused(deal.outsider, deal, "not_a_party")


def test_a_tampered_result_is_refused(deal):
    tampered = copy.deepcopy(deal.doc)
    tampered["generated_at"] = "2026-11-01T13:00:00Z"
    _refused(deal.buyer, deal, "result_mismatch", octets=jcs(tampered))


def test_octets_that_are_not_the_referees_jcs_are_refused(deal):
    _refused(deal.buyer, deal, "result_mismatch", octets=json.dumps(deal.doc, indent=2).encode())


def test_a_result_that_names_other_halves_is_refused(deal):
    """The halves swapped between the roles are the same two halves in another order:
    the Result names them as buyer and seller, so it is not the Result of this pair."""
    parties = deal.parties()
    parties["buyer"]["half"], parties["seller"]["half"] = parties["seller"]["half"], parties["buyer"]["half"]
    _refused(deal.buyer, deal, "other_deal", parties=parties)


def test_a_result_capsule_that_does_not_verify_is_refused(deal):
    broken = copy.deepcopy(deal.result)
    broken["action"] = "referee.other"
    _refused(deal.buyer, deal, "result_unverified", capsule=broken)


def test_a_malformed_delivery_is_refused(deal):
    _refused(deal.buyer, deal, "not_a_result", parties={"buyer": {}})


def test_delivering_the_same_result_twice_seals_it_once(deal):
    deal.buyer.receive_dispute_result(deal.result, deal.octets, deal.parties())
    with pytest.raises(gn.Refused, match="already received"):
        deal.buyer.receive_dispute_result(deal.result, deal.octets, deal.parties())
    assert len(_held(deal.buyer, gn.DISPUTE_RECEIVED)) == 1


def test_the_tool_reads_the_files_and_seals_the_receipt(deal, tmp_path):
    loop_server = pytest.importorskip("loop_server")
    files = {"result": deal.octets, "capsule": json.dumps(deal.result).encode(),
             "deal": json.dumps(deal.parties()).encode()}
    for name, data in files.items():
        (tmp_path / name).write_bytes(data)
    loop_server._node = deal.buyer
    try:
        got = json.loads(loop_server.loop_dispute_receive(str(tmp_path / "result"), str(tmp_path / "capsule"),
                                                          str(tmp_path / "deal")))
        assert got["ok"] and got["step"] == "09"
        again = json.loads(loop_server.loop_dispute_receive(str(tmp_path / "result"), str(tmp_path / "capsule"),
                                                            str(tmp_path / "deal")))
        assert again["ok"] is False and "already received" in again["error"]
    finally:
        loop_server._node = None
