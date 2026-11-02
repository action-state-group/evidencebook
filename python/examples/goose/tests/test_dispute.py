# SPDX-License-Identifier: Apache-2.0
"""Step 09: a referee's Result delivered into each judged party's book (evidencebook#310).

Two Goose nodes (buyer, seller) and a referee, each with its own key. The
parties' halves are signed with their node's key and open to terms that name
the referee, its Nostr mailbox key, its relay and ``answers_within``; the referee
seals its Result with capsule-engine and answers each party's request over the
mailbox (a local NIP-01 relay stands in for a public one). Each node polls, then
seals the Result as received, citing both halves it holds in its own book; every
refusal is sealed too. The Result's content is one claim built with capsule-engine's public result types, so
what is under test is delivery, not the referee's judgments (capsule-engine's
tests hold those)."""
from __future__ import annotations

import copy
import hashlib
import json
import os
import sys
import threading
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE / "tests"))

import goose_node as gn  # noqa: E402
from agent_action_capsule.canonical import jcs  # noqa: E402
from capsule_emit.bilateral import seal_action, seal_request  # noqa: E402
from capsule_emit.signing import resolve_signer  # noqa: E402
import nostr_mailbox as mb  # noqa: E402
from fake_relay import FakeRelay  # noqa: E402

websockets_server = pytest.importorskip("websockets.sync.server")

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

    def __init__(self, tmp_path: Path, monkeypatch, *, named_referee: str | None = None, signer: str = "referee",
                 answers_within: int = 3600, route: dict | None = None) -> None:
        self.relay = FakeRelay()
        self.mailbox_secret = mb.new_secret()
        self.answered: set[str] = set()
        self.buyer = gn.Node.open(tmp_path / "buyer", operator="buyer-co")
        self.seller = gn.Node.open(tmp_path / "seller", operator="seller-co")
        self.outsider = gn.Node.open(tmp_path / "outsider", operator="outsider-co")
        self.books = {"referee": str(tmp_path / "referee" / "book"), "impostor": str(tmp_path / "impostor" / "book")}
        keys = {name: resolve_signer(book).key_id for name, book in self.books.items()}
        terms = {
            "item": "synthetic widget box",
            "buyer": {"operator": "buyer-co", "book_key_id": self.buyer.key_id, "deal_id": "deal-b"},
            "seller": {"operator": "seller-co", "book_key_id": self.seller.key_id, "deal_id": "deal-s"},
            "referee": {"key_id": named_referee or keys["referee"], "mailbox_key": mb.public_key(self.mailbox_secret),
                        "relays": [self.relay.url], "private_relays": True, "answers_within": answers_within, **(route or {})},
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

    def delivery(self) -> bytes:
        return jcs({"result": self.doc, "capsule": self.result})

    def serve(self, answer=None) -> list[str]:
        """The referee's poll: answer every request addressed to its mailbox once."""
        return mb.serve_round([mb.Relay(self.relay.url)], self.mailbox_secret,
                              answer or (lambda _request: self.delivery()), self.answered)

    def close(self) -> None:
        for node in (self.buyer, self.seller, self.outsider):
            node.close()
        self.relay.close()


@pytest.fixture
def deal(tmp_path, monkeypatch):
    d = Deal(tmp_path, monkeypatch)
    yield d
    d.close()


def _held(node: gn.Node, record_type: str) -> list[dict]:
    return [st for _, st in node.notes(record_type)]


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


def _links(node: gn.Node, record) -> list[str]:
    return [link.target for link in record.header.links]


def _fetch(deal: Deal, node: gn.Node) -> dict:
    asked = node.ask_dispute_result(deal.parties(), poll_interval=1)
    deal.serve()
    return node.poll_dispute_result(asked["request_event_id"], deal.parties())


def test_both_parties_receive_the_result_over_the_mailbox_and_cite_the_halves_in_their_books(deal):
    for node in (deal.buyer, deal.seller):
        assert _fetch(deal, node)["status"] == "received"
    digest = hashlib.sha256(deal.octets).hexdigest()
    assert digest == referee.result_digest(deal.doc)
    held = [_held(n, gn.DISPUTE_RECEIVED) for n in (deal.buyer, deal.seller)]
    assert held[0] == held[1] and len(held[0]) == 1
    assert held[0][0]["result_digest"] == digest
    assert held[0][0]["referee"] == deal.terms["referee"]["key_id"]
    for node in (deal.buyer, deal.seller):
        assert _held(node, gn.DISPUTE_REFUSED) == []
        # Each book holds both halves as records of its own, and the received record cites them.
        halves = {r.record_id: st["capsule_id"] for r, st in node.notes(gn.DISPUTE_HALF)}
        assert sorted(halves.values()) == sorted([deal.buyer_half["capsule_id"], deal.seller_half["capsule_id"]])
        (record, _), = node.notes(gn.DISPUTE_RECEIVED)
        assert sorted(_links(node, record)) == sorted(halves)
        assert {link.type for link in record.header.links} == {gn.CITES}


def test_the_request_is_sealed_with_a_deadline_the_terms_set(deal):
    now = 1_800_000_000
    asked = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=60, now=lambda: now)
    assert asked["deadline"] == now + 3600
    (_, st), = deal.buyer.notes(gn.DISPUTE_ASKED)
    assert st["request_event_id"] == asked["request_event_id"] and st["poll_interval"] == 60
    (request,) = deal.relay.events
    assert request["kind"] == mb.REQUEST_KIND and ["expiration", str(now + 3600)] in request["tags"]
    assert ["p", deal.terms["referee"]["mailbox_key"]] in request["tags"]


def test_a_poll_interval_longer_than_answers_within_is_refused(tmp_path, monkeypatch):
    d = Deal(tmp_path, monkeypatch, answers_within=30)
    try:
        with pytest.raises(gn.Refused, match="answers_within"):
            d.buyer.ask_dispute_result(d.parties(), poll_interval=31)
        assert d.relay.events == [] and _held(d.buyer, gn.DISPUTE_ASKED) == []
    finally:
        d.close()


@pytest.mark.parametrize("route,message", [
    ({"relays": []}, "names no relays"),
    ({"relays": ["https://relay.example"]}, "names no relays"),
    ({"mailbox_key": "AB" * 32}, "mailbox_key"),
    ({"mailbox_key": "ab" * 31}, "mailbox_key"),
    ({"private_relays": False}, "private_relays"),
    ({"private_relays": "yes"}, "private_relays"),
    ({"answers_within": 0}, "answers_within"),
    ({"answers_within": "3600"}, "answers_within"),
])
def test_terms_that_name_no_usable_mailbox_are_refused(tmp_path, monkeypatch, route, message):
    d = Deal(tmp_path, monkeypatch, route=route)
    try:
        with pytest.raises(gn.Refused, match=message):
            d.buyer.ask_dispute_result(d.parties(), poll_interval=1)
        assert d.relay.events == [] and _held(d.buyer, gn.DISPUTE_ASKED) == []
    finally:
        d.close()


def test_the_two_parties_terms_must_name_one_referee_route(deal):
    parties = deal.parties()
    parties["seller"]["terms"] = {**deal.terms, "referee": {**deal.terms["referee"], "answers_within": 7}}
    # The seller's opening no longer recomputes its half, which is refused first; the route check is
    # the second line of defence for terms that both open their halves yet differ.
    with pytest.raises(gn.Refused, match="opening"):
        deal.buyer.ask_dispute_result(parties, poll_interval=1)
    with pytest.raises(gn.Refused, match="one referee route"):
        gn._referee_route({"buyer": deal.terms, "seller": parties["seller"]["terms"]})


def test_a_poll_with_another_deals_file_is_refused(deal):
    asked = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    deal.serve()
    other = deal.parties()
    other["buyer"]["half"] = {**other["buyer"]["half"], "capsule_id": "sha256:" + "0" * 64}
    with pytest.raises(gn.Refused, match="not the one this request was sent for"):
        deal.buyer.poll_dispute_result(asked["request_event_id"], other)
    assert _held(deal.buyer, gn.DISPUTE_RECEIVED) == []


def test_an_event_with_a_non_string_member_is_no_reply_and_breaks_no_poll(deal):
    asked = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    hostile = {"id": "0" * 64, "pubkey": 123, "created_at": 1, "kind": mb.REPLY_KIND, "content": "",
               "tags": [["e", asked["request_event_id"]], ["m", gn.MAILBOX_MEDIA]], "sig": "0" * 128}
    deal.relay.events.append(hostile)
    assert deal.buyer.poll_dispute_result(asked["request_event_id"], deal.parties())["status"] == "pending"
    for bad in (hostile, {**hostile, "kind": True}, {**hostile, "tags": [["e", 5]]}):
        with pytest.raises(mb.MailboxError):
            mb.Event.from_json(bad)
    assert not mb.check_event(mb.Event("0" * 64, 123, 1, 4701, (), "", "0" * 128))


def test_a_failed_reply_publish_leaves_the_request_to_be_answered_next_round(deal, monkeypatch):
    deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    real = mb.Relay.publish
    monkeypatch.setattr(mb.Relay, "publish", lambda self, ev: (_ for _ in ()).throw(mb.MailboxError("down")))
    assert deal.serve() == [] and deal.answered == set()
    monkeypatch.setattr(mb.Relay, "publish", real)
    assert len(deal.serve()) == 1


def test_a_relay_that_cuts_the_connection_is_a_transport_condition(deal):
    from websockets.sync.server import serve
    server = serve(lambda ws: ws.close(), "127.0.0.1", 0)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    url = f"ws://127.0.0.1:{server.socket.getsockname()[1]}"
    try:
        for call in (lambda: mb.Relay(url, timeout=2).query({"kinds": [4701]}),
                     lambda: mb.Relay(url, timeout=2).publish(mb.sign_event(mb.new_secret(), 4700, [], "", 1))):
            with pytest.raises(mb.MailboxError):
                call()
        assert mb.poll_replies([mb.Relay(url, timeout=2), mb.Relay(deal.relay.url)], "ab" * 32,
                               gn.MAILBOX_MEDIA, "cd" * 32) == []
    finally:
        server.shutdown()


def test_no_reply_is_pending_then_an_absence_sealed_once_and_a_late_reply_is_still_taken(deal):
    now = 1_800_000_000
    asked = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=60, now=lambda: now)
    rid = asked["request_event_id"]
    assert deal.buyer.poll_dispute_result(rid, deal.parties(), now=lambda: now + 10)["status"] == "pending"
    late = lambda: now + 3601  # noqa: E731
    for _ in range(2):
        assert deal.buyer.poll_dispute_result(rid, deal.parties(), now=late)["status"] == "absent"
    (absence,) = _held(deal.buyer, gn.DISPUTE_ABSENT)
    assert absence["window_closed"] == now + 3600 and absence["route"].startswith("nostr:nprofile1")
    deal.serve()
    assert deal.buyer.poll_dispute_result(rid, deal.parties(), now=late)["status"] == "received"
    assert _held(deal.buyer, gn.DISPUTE_ABSENT) == [absence]  # the absence stays as written


def test_a_reply_not_signed_by_the_mailbox_key_is_not_a_reply(deal):
    asked = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    deal.serve(answer=lambda _request: None)
    other = mb.new_secret()
    forged = mb.sign_event(other, mb.REPLY_KIND, [("e", asked["request_event_id"]), ("p", "00" * 32),
                                                  ("m", gn.MAILBOX_MEDIA)], mb.encode(deal.delivery()), 1)
    deal.relay.events.append(forged.to_json())
    assert deal.buyer.poll_dispute_result(asked["request_event_id"], deal.parties())["status"] == "pending"
    assert _held(deal.buyer, gn.DISPUTE_RECEIVED) == []


def test_a_reply_that_names_another_request_is_not_a_reply(deal):
    first = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    second = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    deal.relay.events[:] = [e for e in deal.relay.events if e["id"] != second["request_event_id"]]
    deal.serve()  # answers only the first
    assert deal.buyer.poll_dispute_result(second["request_event_id"], deal.parties())["status"] == "pending"
    assert deal.buyer.poll_dispute_result(first["request_event_id"], deal.parties())["status"] == "received"


def test_a_reply_that_is_no_result_is_refused_and_recorded(deal):
    asked = deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)
    deal.serve(answer=lambda _request: b"not json")
    with pytest.raises(gn.Refused, match="cannot be read|not a Result|not_a_result"):
        deal.buyer.poll_dispute_result(asked["request_event_id"], deal.parties())
    assert [r["code"] for r in _held(deal.buyer, gn.DISPUTE_REFUSED)] == ["not_a_result"]
    assert _held(deal.buyer, gn.DISPUTE_RECEIVED) == []


def test_a_result_signed_by_a_key_the_terms_do_not_name_is_refused_over_the_mailbox(tmp_path, monkeypatch):
    d = Deal(tmp_path, monkeypatch, signer="impostor")
    try:
        asked = d.buyer.ask_dispute_result(d.parties(), poll_interval=1)
        d.serve()
        with pytest.raises(gn.Refused):
            d.buyer.poll_dispute_result(asked["request_event_id"], d.parties())
        assert [r["code"] for r in _held(d.buyer, gn.DISPUTE_REFUSED)] == ["not_named_referee"]
        assert _held(d.buyer, gn.DISPUTE_RECEIVED) == []
    finally:
        d.close()


def test_a_party_the_result_does_not_name_is_refused_over_the_mailbox(deal):
    # The outsider's key signed neither half, so it cannot even ask.
    with pytest.raises(gn.Refused, match="neither half"):
        deal.outsider.ask_dispute_result(deal.parties(), poll_interval=1)
    assert _held(deal.outsider, gn.DISPUTE_ASKED) == []


def test_an_unreachable_relay_refuses_the_ask(deal):
    deal.relay.close()
    with pytest.raises(gn.Refused, match="no relay accepted"):
        deal.buyer.ask_dispute_result(deal.parties(), poll_interval=1)


def test_the_tools_ask_and_poll_the_mailbox(deal, tmp_path):
    loop_server = pytest.importorskip("loop_server")
    (tmp_path / "deal.json").write_text(json.dumps(deal.parties()))
    loop_server._node = deal.buyer
    try:
        asked = json.loads(loop_server.loop_dispute_ask(str(tmp_path / "deal.json"), 1))
        assert asked.get("ok"), asked
        assert asked["step"] == "09"
        poll = lambda: json.loads(loop_server.loop_dispute_poll(asked["request_event_id"],  # noqa: E731
                                                                 str(tmp_path / "deal.json")))
        assert poll()["status"] == "pending"
        deal.serve()
        assert poll()["status"] == "received"
        again = poll()
        assert again["ok"] is False and "already received" in again["error"]
    finally:
        loop_server._node = None


def test_the_transport_matches_the_binding_vectors_primitives():
    """BIP-340 test vector 0, and the NIP-19 nprofile example (binding §9)."""
    sk = (3).to_bytes(32, "big")
    assert mb.public_key(sk) == "f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9"
    sig = mb.schnorr_sign(bytes(32), sk, bytes(32))
    assert sig.hex() == ("e907831f80848d1069a5371b402410364bdf1c5f8307b0084c55f1ce2dca8215"
                         "25f66a4a85ea8b71e482a74f382d2ce5ebeee8fdb2172f477df4900d310536c0")
    assert mb.schnorr_verify(bytes(32), bytes.fromhex(mb.public_key(sk)), sig)
    assert not mb.schnorr_verify(bytes(31) + b"\x01", bytes.fromhex(mb.public_key(sk)), sig)
    assert mb.nprofile("3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d",
                       ["wss://r.x.com", "wss://djbas.sadkb.com"]) == (
        "nostr:nprofile1qqsrhuxx8l9ex335q7he0f09aej04zpazpl0ne2cgukyawd24mayt8gpp4mhxue69uhhytnc9e3k7mgpz4mhxue69uhkg6nzv9ejuumpv34kytnrdaksjlyr9p")


def test_an_event_with_a_changed_content_or_signature_fails_the_check():
    sk = mb.new_secret()
    ev = mb.sign_event(sk, mb.REPLY_KIND, [("e", "ab" * 32)], mb.encode(b"x"), 5)
    assert mb.check_event(ev)
    assert not mb.check_event(mb.Event(**{**ev.__dict__, "content": mb.encode(b"y")}))
    assert not mb.check_event(mb.Event(**{**ev.__dict__, "sig": "00" * 64}))
    assert mb.decode("not base64url!") is None and mb.decode(mb.encode(b"x") + "=") is None
