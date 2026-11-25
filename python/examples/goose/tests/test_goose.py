# SPDX-License-Identifier: Apache-2.0
"""The Goose loop reference on capsulectl: one scripted run of two nodes over MCP stdio (each a real
``capsulectl-deal`` profile), whose files are then checked by Go (``capsulectl-deal verify``, ``goose-capsule``)
and by the loop's own commitment checker; and the refusals a tool makes before it seals anything.

``GOOSE_CAPSULECTL_DEAL`` and ``GOOSE_CAPSULE_ENGINE`` name the binaries (every test skips without them);
``GOOSE_GO_CHECK=1`` runs Go's check of the model-call Capsule (``go run ./test/interop/goose-capsule``) and
``GOOSE_LOOP_SCRIPTS`` is the loop checkout's ``scripts/`` (the commitment checker); each skips when unset."""
from __future__ import annotations

import base64
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
from conftest import needs_binaries

import commitment as cm
import goose_node as gn
from ctl import Refused

pytestmark = needs_binaries
HERE = Path(__file__).resolve().parents[1]
ROOT = HERE.parents[2]
LOOP = os.environ.get("GOOSE_LOOP_SCRIPTS")
GO = os.environ.get("GOOSE_GO_CHECK")


@pytest.fixture(scope="module")
def run(tmp_path_factory):
    """The scripted run (about six minutes: a report rests on a cadence tick, due every five)."""
    out = tmp_path_factory.mktemp("goose-run")
    done = subprocess.run([sys.executable, str(HERE / "scripted_run.py"), str(out)], capture_output=True, text=True,
                          timeout=1500)
    assert done.returncode == 0, done.stdout[-3000:] + done.stderr[-3000:]
    return out


def verify_bundle(run: Path, node: str, file: Path) -> dict:
    """Go's offline check of an Evidence Bundle (``capsulectl-deal verify --bundle``), as the node's profile."""
    done = subprocess.run([os.environ.get("GOOSE_CAPSULECTL_DEAL", "capsulectl-deal"), "--profile", f"goose-{node}",
                           "verify", "--bundle", str(file)], capture_output=True, text=True,
                          env={**os.environ, "HOME": str(run / node)})
    assert done.returncode == 0, done.stdout + done.stderr
    return json.loads(done.stdout)


@pytest.mark.parametrize("node, name", [("buyer", "buyer-card.json"), ("seller", "seller-card.json"),
                                        ("buyer", "buyer-report.json"), ("seller", "seller-report.json"),
                                        ("buyer", "buyer-opening-bundle.json"),
                                        ("buyer", "buyer-model-call-bundle.json")])
def test_go_verifies_each_bundle_the_run_wrote(run, node, name):
    assert verify_bundle(run, node, run / name)["verdict"] == "VALID"


def test_the_run_wrote_the_files_of_the_exchange(run):
    for name in ("offer", "seller-agreement", "buyer-agreement", "buyer-leg", "seller-leg", "seller-delivery",
                 "buyer-statement", "seller-statement", "buyer-history-card", "seller-history-card"):
        assert (run / f"{name}.json").stat().st_size > 0, name
    assert json.loads((run / "buyer-history-card.json").read_text())["reply"]["anchor"]["digest"]


def _cards(value):
    """Every standard_history card object in a bundle, whether inline, as JSON text or as base64url JSON."""
    import base64
    if isinstance(value, dict):
        if str(value.get("derivation", "")).startswith("standard_history/") and "bands" in value:
            yield value
        for v in value.values():
            yield from _cards(v)
    elif isinstance(value, list):
        for v in value:
            yield from _cards(v)
    elif isinstance(value, str) and len(value) > 20:
        for decode in (lambda t: t, lambda t: base64.urlsafe_b64decode(t + "=" * (-len(t) % 4)).decode()):
            try:
                yield from _cards(json.loads(decode(value)))
                return
            except Exception:
                continue


@pytest.mark.parametrize("node", ["buyer", "seller"])
def test_a_goose_deal_counts_with_a_value_band_on_the_card(run, node):
    """The offer carries the agreed price in its terms, which the card's value band reads (evidencebook#888): the
    run's deal of 50 USD minor units is in a band, not counted as unpriced."""
    cards = list(_cards(json.loads((run / f"{node}-card.json").read_text())))
    assert cards, "the bundle carries the card"
    assert any(card["bands"] for card in cards), cards
    assert json.loads((run / "offer.json").read_text())["agent_input"]["body"]["terms"]["price_minor"] == 50


@pytest.mark.skipif(not GO, reason="GOOSE_GO_CHECK is not set")
def test_go_accepts_the_model_call_capsule(run):
    done = subprocess.run(["go", "run", "./test/interop/goose-capsule", str(run / "buyer-model-call.json")],
                          cwd=ROOT, capture_output=True, text=True)
    assert done.returncode == 0, done.stderr
    assert json.loads(done.stdout)["effect_attestation"] == "runtime_claimed"


@pytest.mark.skipif(not LOOP, reason="GOOSE_LOOP_SCRIPTS (the loop's scripts/) is not set")
def test_the_loops_checker_accepts_each_nodes_commitment(run):
    done = subprocess.run([sys.executable, str(Path(LOOP) / "check_commitment.py"), str(run / "commitments")],
                          capture_output=True, text=True, env={**os.environ, "PYTHONPATH": LOOP})
    assert done.returncode == 0, done.stdout + done.stderr
    assert "2 commitments ok" in done.stdout


@pytest.mark.skipif(not LOOP, reason="GOOSE_LOOP_SCRIPTS (the loop's scripts/) is not set")
@pytest.mark.parametrize("name, expected", [("extra", "count"), ("tampered", "signature")])
def test_the_loops_checker_refuses_an_extra_member_and_a_tampered_commitment(run, tmp_path, name, expected):
    """Each case is a complete commitment and policy that differs in one thing, so only that can be what refuses it."""
    key = (run / "buyer.key").read_text()
    doc = json.loads((run / "commitments" / f"{key}.json").read_text())
    n = gn.Node.open(run / "buyer", operator="goose-buyer")
    try:
        if name == "extra":
            doc["count"] = 3
            doc = cm._signed(n, doc)  # signed by the node's own key: only the extra member is wrong
        else:
            doc["answers_within"] = "PT1H"  # changed after it was signed
    finally:
        n.close()
    out = tmp_path / name
    (out / "policies").mkdir(parents=True)
    (out / f"{key}.json").write_text(json.dumps(doc))
    (out / "policies" / f"{key}.json").write_bytes((run / "commitments" / "policies" / f"{key}.json").read_bytes())
    done = subprocess.run([sys.executable, str(Path(LOOP) / "check_commitment.py"), str(out)],
                          capture_output=True, text=True, env={**os.environ, "PYTHONPATH": LOOP})
    assert done.returncode != 0 and expected in done.stdout + done.stderr, done.stdout + done.stderr


# -- refusals: a tool that refuses seals nothing -----------------------------------------------


@pytest.fixture
def node(tmp_path):
    n = gn.Node.open(tmp_path / "node", operator="goose-test")
    n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id}, once="key_id")
    yield n
    n.close()


def test_two_tool_calls_never_share_a_scratch_file_and_none_is_left(node):
    first = node.note(gn.ADMITTED, {"peer": "c" * 64, "decision": "GO"}, once="peer")
    second = node.note(gn.ADMITTED, {"peer": "d" * 64, "decision": "NO"}, once="peer")
    assert first["record_id"] != second["record_id"]
    assert node._file("a", "x") != node._file("a", "x")
    node.seal_model_call("rehearsal", "m", "one", "one")
    node.seal_model_call("rehearsal", "m", "two", "two")  # the second call's output file is its own
    assert not list(node._tmp.glob("out-*")), "a verb's output is read and removed"


def test_a_note_with_the_same_identifying_member_is_found_not_sealed_again(node):
    first = node.note(gn.ADMITTED, {"peer": "a" * 64, "decision": "GO"}, once="peer")
    again = node.note(gn.ADMITTED, {"peer": "a" * 64, "decision": "GO"}, once="peer")
    assert again["record_id"] == first["record_id"]
    assert [r["record_id"] for r in node.notes(gn.ADMITTED)] == [first["record_id"]]


def test_a_deal_needs_a_sealed_go_on_the_peer(node, tmp_path):
    with pytest.raises(Refused, match="loop_admit first"):
        node.open_deal("b" * 64, "item", 50, "USD", "lightning", "buy", False, str(tmp_path / "offer.json"))
    node.note(gn.ADMITTED, {"peer": "b" * 64, "decision": "NO", "reason": "no"}, once="peer")
    with pytest.raises(Refused, match="is NO: no deal"):
        node.open_deal("b" * 64, "item", 50, "USD", "lightning", "buy", False, str(tmp_path / "offer.json"))
    assert not (tmp_path / "offer.json").exists()
    assert node.notes(gn.DEAL_CONTEXT) == [], "nothing was sealed"


def test_an_offer_carries_its_price_in_its_terms_and_a_mismatched_one_is_refused(pair):
    """The card's value band reads terms.price_minor (evidencebook#888): the node's offer carries it, equal to the
    payment row's amount, and the seller refuses an offer whose two differ before it keeps or seals anything."""
    buyer, seller, deal, offer, tmp = pair
    sent = json.loads(offer.read_text())
    body = sent["agent_input"]["body"]
    assert body["terms"]["price_minor"] == body["terms_rows"]["rows"][0]["owed"]["amount_minor"] == 50
    body["terms"]["price_minor"] = 49
    altered = tmp / "altered.json"
    altered.write_text(json.dumps(sent))
    with pytest.raises(Refused, match="price_minor"):
        seller.respond(str(altered), "sell tokens", str(tmp / "s.json"))
    assert not (tmp / "s.json").exists()


def test_a_price_the_pack_does_not_admit_is_refused_with_the_engines_verdict(node, tmp_path):
    node.note(gn.ADMITTED, {"peer": "b" * 64, "decision": "GO"}, once="peer")
    with pytest.raises(Refused, match="no deal"):
        node.open_deal("b" * 64, "item", 50000, "USD", "lightning", "buy", False, str(tmp_path / "offer.json"))


def test_a_file_that_is_not_an_offer_is_refused_on_receive(node, tmp_path):
    bad = tmp_path / "bad.json"
    bad.write_text('{"not": "an offer"}')
    with pytest.raises(Refused):
        node.respond(str(bad), "sell", str(tmp_path / "agreement.json"))


def test_a_model_call_is_sealed_with_its_provider_and_its_capsule_is_signed_by_the_node(node):
    record, capsule = node.seal_model_call("rehearsal", "m", "the prompt", "the completion")
    assert capsule["key_id"] == node.key_id and capsule["model_attestation"]["model_id"] == "m"
    assert [r["record_id"] for r in node.notes(gn.MODEL_CALL)] == [record["record_id"]]


def test_the_commitment_staples_the_profiles_card_and_carries_no_count(node):
    doc, _ = cm.build(node, "")
    assert doc["history_cards"][0]["derivation"] == "history_card/2"
    assert not any("count" in k for k in doc)


def test_what_the_checker_would_refuse_is_refused_before_signing(node):
    for node_id, settings in (("nothex", cm.Settings()), (node.key_id, cm.Settings(answers_within="24h")),
                              (node.key_id, cm.Settings(bindings=("Bad Name",)))):
        with pytest.raises(Refused):
            cm.build(node, node_id, settings)


def test_registering_a_card_without_a_service_sends_nothing(node, monkeypatch, tmp_path):
    import loop_server as ls
    monkeypatch.delenv("GOOSE_CARD_SERVICE", raising=False)
    monkeypatch.setattr(ls, "_node", node)
    out = json.loads(ls.loop_register_card(str(tmp_path / "statement.json")))
    assert out["ok"] is False and "opt-in" in out["error"]
    assert not (tmp_path / "statement.json").exists()


@pytest.fixture
def pair(tmp_path):
    buyer, seller = (gn.Node.open(tmp_path / n, operator=f"goose-{n}") for n in ("buyer", "seller"))
    buyer.note(gn.ADMITTED, {"peer": seller.key_id, "decision": "GO"}, once="peer")
    seller.note(gn.ADMITTED, {"peer": buyer.key_id, "decision": "GO"}, once="peer")
    offer = tmp_path / "offer.json"
    opened = buyer.open_deal(seller.key_id, "tokens", 50, "USD", "lightning", "buy tokens", False, str(offer))
    yield buyer, seller, opened["deal_id"], offer, tmp_path
    buyer.close()
    seller.close()


def test_a_retried_agree_reuses_the_decision_and_the_intent_it_sealed(pair):
    buyer, seller, deal, offer, tmp = pair
    seller.respond(str(offer), "sell tokens", str(tmp / "s.json"))
    with pytest.raises(Refused):  # the seller's agreement is not kept yet: nothing may be stuck by this
        buyer.agree(deal, str(tmp / "b.json"))
    buyer.receive(str(tmp / "s.json"))
    assert buyer.agree(deal, str(tmp / "b.json"))["agreement"]
    assert len(buyer.notes(gn.DEAL_INTENT)) == 1, "the intent was sealed once"
    assert buyer.agree(deal, str(tmp / "b2.json"))["agreement"], "agreeing again rewrites the same agreement"
    seller.respond(str(offer), "sell tokens", str(tmp / "s2.json"))  # a retry of the seller's answer


def test_the_users_words_are_in_no_record_the_deal_bundle_discloses(pair):
    buyer, seller, deal, offer, tmp = pair
    bundle = tmp / "bundle.json"
    buyer.bundle(gn.NODE_BOOK, buyer.context(deal)["record_id"], str(bundle), disclose=True)
    assert "buy tokens" not in bundle.read_text()
    assert json.loads(bundle.read_text())["disclosures"], "the context and the decision it cites are disclosed"


def test_a_payment_resumed_with_another_reference_is_refused(pair):
    buyer, seller, deal, offer, tmp = pair
    seller.respond(str(offer), "sell tokens", str(tmp / "s.json"))
    buyer.receive(str(tmp / "s.json"))
    buyer.agree(deal, str(tmp / "b.json"))
    seller.receive(str(tmp / "b.json"))
    paused = buyer.pay(deal, "reference-a", str(tmp / "leg.json"))
    assert paused["paused"]
    buyer.approve(deal, paused["check_id"], "proceed", "yes")
    with pytest.raises(Refused, match="not the reference"):
        buyer.pay(deal, "reference-b", str(tmp / "leg.json"), paused["check_id"])
    paid = buyer.pay(deal, "reference-a", str(tmp / "leg.json"), paused["check_id"])
    assert paid["leg"] == "payer"
    assert paid["person_signature"] == "missing", "the leg says the person's signatures are still to come, and the tool says so"
    # a call that stopped after the leg was sealed (its file never written) is called again and writes it again
    (tmp / "leg.json").unlink()
    again = buyer.pay(deal, "reference-a", str(tmp / "leg.json"), paused["check_id"])
    assert again["digest"] == paid["digest"] and (tmp / "leg.json").exists()


def test_an_intent_sealed_before_its_index_was_noted_is_not_a_dead_end(pair):
    """A call that stopped after sealing the deal's first intent (the one that opens it) and before noting it."""
    buyer, seller, deal, offer, tmp = pair
    said = buyer._file("intent.json", {"verbatim": "buy tokens", "allowed": ["pay"]})
    opening = buyer._file("opening.json", {"type": "purchase", "who": {"name": "the seller", "domain": "the seller.invalid"},
                                           "recourse": {"rail": "lightning", "refundable": False}})
    buyer._run("note", "--deal", deal, "--kind", "intent", "--input", said, "--opening", opening)
    assert not buyer.notes(gn.DEAL_INTENT)
    assert buyer._intent(deal, "buy tokens", "lightning", "the seller", "purchase", ["pay"], 50) == "", \
        "the intent is sealed and its digest is not printed again: the agreement goes without citing it"
    assert not buyer.notes(gn.DEAL_INTENT)


# -- step 06: the settlement disclosure ---------------------------------------------------------


def _verifies(doc: dict) -> bool:
    """Step 06's signature check, done independently: Ed25519 by ``key_id`` over the JCS form without ``sig``."""
    from cryptography.exceptions import InvalidSignature
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

    body = {k: v for k, v in doc.items() if k != "sig"}
    raw = json.dumps(body, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    sig = doc["sig"]
    assert (sig["alg"], sig["canon"]) == ("Ed25519", "jcs")
    try:
        Ed25519PublicKey.from_public_bytes(bytes.fromhex(doc["key_id"])).verify(
            base64.urlsafe_b64decode(sig["value"] + "=" * (-len(sig["value"]) % 4)), raw)
        return True
    except InvalidSignature:
        return False


def test_each_node_discloses_its_one_settlement_signed_by_its_own_key(run):
    for name in ("buyer", "seller"):
        doc = json.loads((run / f"{name}-settlement-disclosure.json").read_text())
        assert set(doc) == {"type", "node", "key_id", "rail", "through_sequence", "settlements", "sig"}, \
            "a count only: no amount, counterparty or payment reference"
        assert (doc["type"], doc["rail"], doc["through_sequence"], doc["settlements"]) == \
            ("settlement-disclosure/v1", "lightning", 1, 1)
        assert doc["key_id"] == (run / f"{name}.key").read_text().strip()
        assert _verifies(doc)


@pytest.mark.skipif(not LOOP, reason="GOOSE_LOOP_SCRIPTS (the loop's scripts/) is not set")
def test_the_loops_signature_check_accepts_each_disclosure_and_refuses_a_changed_count(run):
    sys.path.insert(0, LOOP)
    import check_commitment as cc

    for name in ("buyer", "seller"):
        doc = json.loads((run / f"{name}-settlement-disclosure.json").read_text())
        spki = cc.ED25519_SPKI + bytes.fromhex(doc["key_id"])
        assert cc.verify_sig(doc, spki)
        assert not cc.verify_sig({**doc, "settlements": 2}, spki)


def _settled(pair):
    """The pair's deal through both payment legs: the buyer's payer leg, then the seller's payee leg."""
    buyer, seller, deal, offer, tmp = pair
    seller.respond(str(offer), "sell tokens", str(tmp / "s.json"))
    buyer.receive(str(tmp / "s.json"))
    buyer.agree(deal, str(tmp / "b.json"))
    seller.receive(str(tmp / "b.json"))
    paused = buyer.pay(deal, "reference-a", str(tmp / "leg.json"))
    buyer.approve(deal, paused["check_id"], "proceed", "yes")
    buyer.pay(deal, "reference-a", str(tmp / "leg.json"), paused["check_id"])
    return paused["check_id"]


def test_a_node_counts_the_leg_it_sealed_once_and_only_on_its_rail(pair):
    import settlement as st

    buyer, seller, deal, offer, tmp = pair
    assert st.disclosure(buyer, "lightning", 0)["settlements"] == 0, "an offer is not a settlement"
    check_id = _settled(pair)
    # paid: the buyer's payer leg is its settlement; the seller has sealed nothing yet
    assert [st.disclosure(n, "lightning", 1)["settlements"] for n in (buyer, seller)] == [1, 0]
    seller.receive(str(tmp / "leg.json"))
    seller.received(deal, 50, "reference-a", str(tmp / "payee.json"))
    assert st.disclosure(seller, "lightning", 1)["settlements"] == 1
    # a call written again (its file lost) finds its note: still one settlement, one note
    buyer.pay(deal, "reference-a", str(tmp / "leg2.json"), check_id)
    seller.received(deal, 50, "reference-a", str(tmp / "payee2.json"))
    assert [st.disclosure(n, "lightning", 1)["settlements"] for n in (buyer, seller)] == [1, 1]
    assert len(buyer.notes(gn.SETTLED)) == 1 and len(seller.notes(gn.SETTLED)) == 1
    # another rail's settlement never enters this rail's count (Sherlock2's condition on capsule-emit#408)
    buyer.note(gn.SETTLED, {"deal_id": "deal-fedcba9876543210", "rail": "mock", "leg": "payer", "digest": "0" * 64},
               once="digest")
    assert [st.disclosure(buyer, r, 1)["settlements"] for r in ("lightning", "mock", "other")] == [1, 1, 0]
    # an unaccepted offer beside the paid deal stops nothing
    buyer.open_deal(seller.key_id, "more tokens", 50, "USD", "lightning", "buy more", False, str(tmp / "offer2.json"))
    assert st.disclosure(buyer, "lightning", 1)["settlements"] == 1
    # each disclosure is sealed in the node book
    sealed = [r["statement"] for r in buyer.notes(gn.SETTLEMENT_DISCLOSURE)]
    assert sealed[-1] == {k: v for k, v in st.disclosure(buyer, "lightning", 1).items() if k != "sig"}


@pytest.mark.parametrize("rail, through", [("", 0), (" lightning", 0), ("lightning", -1), ("lightning", True),
                                           ("lightning", 2 ** 53), ("lightning", "1"), (None, 0)])
def test_a_disclosure_that_is_not_well_formed_is_refused_before_anything_is_sealed(node, rail, through):
    import settlement as st

    with pytest.raises(Refused):
        st.disclosure(node, rail, through)
    assert not node.notes(gn.SETTLEMENT_DISCLOSURE)


def test_the_tool_writes_the_signed_disclosure(node, tmp_path, monkeypatch):
    import loop_server

    monkeypatch.setattr(loop_server, "_node", node)
    out = json.loads(loop_server.loop_settlement_disclosure("lightning", 0, str(tmp_path / "d.json")))
    doc = json.loads((tmp_path / "d.json").read_text())
    assert out["ok"] and out["settlements"] == doc["settlements"] == 0 and _verifies(doc)
    assert json.loads(loop_server.loop_settlement_disclosure("lightning", -1, str(tmp_path / "e.json")))["ok"] is False
    assert not (tmp_path / "e.json").exists()
