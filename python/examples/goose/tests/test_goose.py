# SPDX-License-Identifier: Apache-2.0
"""The Goose loop reference: deal chains the profile's reference checker
accepts, two nodes dealing and deriving their cards, a counterparty half
refused when it does not verify, the flat ledger import, and the MCP server
driven over stdio as Goose drives it (scripted_run.py)."""
from __future__ import annotations

import copy
import json
import os
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import deal_records as dr  # noqa: E402
import goose_node as gn  # noqa: E402
from agent_action_capsule import standard_history as sh  # noqa: E402

from capsule_emit.store_backed_ledger import LedgerError  # noqa: E402

from evidencebook import deal  # noqa: E402

CHECKER = HERE.parents[2] / "dealprofile" / "x-deal-v0" / "check_profile.py"
AT = datetime(2026, 11, 1, 10, tzinfo=timezone.utc)
TERMS = dr.Terms("inference: 1k tokens", 500, "USD", "lightning", False)
DEAL = "deal-0123456789abcdef"


class Clock:
    def __init__(self) -> None:
        self.at = AT

    def __call__(self) -> datetime:
        return self.at


@pytest.fixture
def clock() -> Clock:
    return Clock()


@pytest.fixture
def nodes(tmp_path, clock):
    buyer = gn.Node.open(tmp_path / "buyer", operator="goose-buyer", now=clock)
    seller = gn.Node.open(tmp_path / "seller", operator="goose-seller", now=clock)
    yield buyer, seller
    buyer.close()
    seller.close()


def deal_through(buyer: gn.Node, seller: gn.Node, clock: Clock, status: str = "received") -> None:
    """One deal, both sides, closed with ``status``; each side verifies the
    other's final half."""
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "buy 1k tokens"))
    opened = gn.verify_half(buyer.half(DEAL), buyer.checkpoint_key_id)
    chain = seller.new_deal(DEAL, dr.ROLE_SELLER, buyer.key_id, TERMS)
    seller.seal(chain, chain.respond(clock.at, "sell 1k tokens", opened["records"][-1]["digest"]))
    clock.at += timedelta(seconds=10)
    chain = buyer.deal(DEAL)
    buyer.seal(chain, chain.check_and_pay(clock.at, "ln:payment-hash"))
    clock.at += timedelta(seconds=10)
    for node in (buyer, seller):
        chain = node.deal(DEAL)
        node.seal(chain, chain.close(clock.at, status))
    for me, peer in ((buyer, seller), (seller, buyer)):
        verified = gn.verify_half(me.half(DEAL), me.checkpoint_key_id)
        peer.note(gn.HALF_RECEIVED, {"deal_id": DEAL, "key_id": verified["key_id"],
                                     "close_half_digest": verified["close_half_digest"]}, subject_ref=DEAL)
    clock.at += timedelta(minutes=1)


def test_both_sides_chains_pass_the_reference_checker(nodes, clock, tmp_path):
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    files = []
    for node in (buyer, seller):
        chain = [step["payload"] for step in node.half(DEAL)["steps"]]
        path = tmp_path / f"{node.operator}.json"
        path.write_text(json.dumps(chain))
        files.append(str(path))
    run = subprocess.run([sys.executable, str(CHECKER), *files], capture_output=True, text=True)
    assert run.returncode == 0 and "ALL OK" in run.stdout, run.stdout + run.stderr


def test_each_step_is_its_books_record_carrying_its_header(nodes, clock):
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    for node, kinds in ((buyer, ["baseline", "check", "verdict", "approval", "action", "outcome", "close"]),
                        (seller, ["baseline", deal.TYPE_TASK_AUTHORITY, deal.TYPE_PROPOSED_ACTION,
                                  deal.TYPE_ACTION_EVALUATION, deal.TYPE_ACTION_RECORD, deal.TYPE_ACTION_APPROVAL,
                                  deal.TYPE_PROPOSED_ACTION, deal.TYPE_ACTION_EVALUATION, deal.TYPE_ACTION_RECORD,
                                  deal.TYPE_ACTION_OUTCOME, "close"])):
        half = node.half(DEAL)
        assert [k for _, k, _ in node.deal(DEAL).records] == kinds
        for n, step in enumerate(half["steps"], start=1):
            capsule = step["capsule"]
            assert capsule["action_id"] == f"{DEAL}/{n}" and capsule["deal_id"] == DEAL
            header = capsule["evidence_record_header"]
            assert header["record_type"] == deal.STEP_RECORD_TYPE and header["subject_ref"] == f"{DEAL}/{n}"
            assert header["payload_commitments"] == [deal.digest(step["payload"])]
        book = node.shelf.open(f"deal/{DEAL}")
        close = book.records()[-1]
        assert book.opening(close.record_id, deal.AMOUNT_FIELD).text == "500"
        assert book.opening(close.record_id, deal.CURRENCY_FIELD).text == "USD"
    pay = buyer.half(DEAL)["steps"][4]["capsule"]
    assert pay["action_type"] == "decide" and pay["effect"]["type"] == "send_payment"


def test_both_cards_count_the_deal_from_their_own_side(nodes, clock):
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    for node, role in ((buyer, "bought"), (seller, "provided")):
        assert node.tick().ticked
        result = node.standard_history()
        card = json.loads(result["card"])
        assert card["roles"][role] == 1 and card["happened"]["stamped_by_both"] == 1
        assert result["deals"][0]["amount"] == {"value": "500", "assetCode": "USD", "assetScale": 2}
        assert sh.is_standard(sh.check_card(card, policy=result["policy"], as_of=result["as_of"],
                                            deals=result["deals"]))
        assert result["card"] == node.standard_history(result["tick"])["card"], "a card is reproducible"


def test_a_card_counts_only_what_its_tick_commits(nodes, clock):
    buyer, seller = nodes
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "buy"))
    assert buyer.tick().ticked
    first = buyer.standard_history()
    assert first["deals"] == [], "a deal with no commit or pay is not a deal yet"
    chain = buyer.deal(DEAL)
    buyer.seal(chain, chain.check_and_pay(clock.at, "ref"))
    assert buyer.standard_history(first["tick"])["card"] == first["card"]
    clock.at += timedelta(minutes=6)
    assert buyer.tick().ticked
    later = buyer.standard_history()
    assert len(later["deals"]) == 1 and later["deals"][0]["happened"] == "no_record"


def test_a_half_that_does_not_verify_is_refused(nodes, clock):
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    half = seller.half(DEAL)
    gn.verify_half(half, seller.checkpoint_key_id)
    with pytest.raises(gn.Refused, match="pinned checkpoint key"):
        gn.verify_half(half, buyer.checkpoint_key_id)

    def broken(change) -> None:
        bad = copy.deepcopy(half)
        change(bad)
        with pytest.raises(gn.Refused):
            gn.verify_half(bad, seller.checkpoint_key_id)

    broken(lambda h: h["steps"][3]["payload"]["body"].update(disposition="ASK"))     # payload not committed
    broken(lambda h: h["steps"][1]["capsule"].update(key_id=buyer.key_id))           # another signer
    broken(lambda h: h["steps"].pop(2))                                             # a step missing
    broken(lambda h: h["steps"].pop())                                              # the checkpoint covers more
    broken(lambda h: h["steps"][0]["capsule"].update(deal_id="deal-ffffffffffffffff"))
    broken(lambda h: h.update(half="something else"))


def test_a_peer_half_needs_a_go_admission_pinning_its_checkpoint_key(nodes, clock):
    buyer, seller = nodes
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "buy"))
    half = buyer.half(DEAL)
    with pytest.raises(gn.Refused, match="no admission"):
        seller.verify_peer_half(half)
    seller.note(gn.ADMITTED, {"peer": buyer.key_id, "checkpoint_key_id": buyer.checkpoint_key_id, "decision": "NO"})
    with pytest.raises(gn.Refused, match="is NO"):
        seller.verify_peer_half(half)
    seller.note(gn.ADMITTED, {"peer": buyer.key_id, "checkpoint_key_id": seller.checkpoint_key_id, "decision": "GO"})
    with pytest.raises(gn.Refused, match="pinned checkpoint key"):
        seller.verify_peer_half(half)
    seller.note(gn.ADMITTED, {"peer": buyer.key_id, "checkpoint_key_id": buyer.checkpoint_key_id, "decision": "GO"})
    assert seller.verify_peer_half(half)["deal_id"] == DEAL


def test_a_deal_needs_its_own_role_and_an_open_book(nodes, clock):
    buyer, seller = nodes
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "buy"))
    with pytest.raises(gn.Refused):
        buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    with pytest.raises(ValueError):
        buyer.deal(DEAL).respond(clock.at, "sell", "00" * 32)
    with pytest.raises(gn.Refused):
        seller.deal(DEAL)
    with pytest.raises(ValueError):
        buyer.deal(DEAL).close(clock.at, "lost")


def flat_ledger(path: Path, n: int, operator: str = "goose-old") -> Path:
    from capsule_emit import seal

    os.environ["CAPSULE_WITNESS"] = "off"
    for i in range(n):
        seal({"step": i}, action="loop.admit", operator=operator, developer="goose-agent@v1", ledger=str(path))
    return path


def test_an_existing_ledger_is_imported_as_a_host_log(tmp_path, clock):
    from capsule_emit import read_ledger

    source = flat_ledger(tmp_path / "capsule-ledger", 3)
    before = [r["capsule_id"] for r in read_ledger(str(source))]
    node = gn.Node.open(tmp_path / "home", operator="goose-old", now=clock)
    report = node.import_ledger(source)
    assert report == {"reopen": True, "trusted": report["trusted"]}, "the ledger's own key is trusted first"
    node.close()
    node = gn.Node.open(tmp_path / "home", operator="goose-old", now=clock)
    try:
        report = node.import_ledger(source)
        assert report["records"] == 3 and report["resumed"] == 0
        assert not source.exists() and Path(report["archived_to"]).exists(), "moved only after the check"
        held = node.ledger_capsules()
        assert [c["capsule_id"] for c in held] == before, "every record, in order, as sealed"
        assert all(c["key_id"] == report_key for c in held for report_key in [held[0]["key_id"]])
        assert node.tick().ticked and gn.LEDGER_LOG in node.cadence.live()
    finally:
        node.close()


def test_an_import_that_does_not_match_keeps_the_flat_files(tmp_path, clock):
    first = flat_ledger(tmp_path / "one" / "capsule-ledger", 2)
    other = flat_ledger(tmp_path / "two" / "capsule-ledger", 2, operator="another")
    home = tmp_path / "home"
    for source in (first, other):
        node = gn.Node.open(home, operator="goose-old", now=clock)
        if node.import_ledger(source).get("reopen"):
            node.close()
            node = gn.Node.open(home, operator="goose-old", now=clock)
            if source is first:
                node.import_ledger(source)
        if source is other:
            with pytest.raises(LedgerError):
                node.import_ledger(source)
            assert other.exists(), "a refused import moves nothing"
        node.close()


def test_the_server_over_mcp_stdio_writes_cards_go_accepts(tmp_path):
    """The scripted Goose run (scripted_run.py) end to end; with Go on the
    path, Go's check of each card (test/interop/goose-card) too."""
    pytest.importorskip("mcp")
    run = subprocess.run([sys.executable, str(HERE / "scripted_run.py"), str(tmp_path)], capture_output=True,
                         text=True, timeout=600)
    assert run.returncode == 0, run.stdout + run.stderr
    for node in ("buyer", "seller"):
        result = json.loads((tmp_path / f"{node}-card.json").read_text())
        assert len(result["deals"]) == 1 and result["deals"][0]["happened"] == "stamped_by_both"
        assert json.loads((tmp_path / f"{node}-history-card.json").read_text())["derivation"] == "history_card/1"
    # D3 condition 2: each final half passes the checks a Mesh node on
    # capsules-plugin v0.1.8 runs over a counterparty's half, with the crates
    # that release pins (test/interop/goose-mesh-check).
    if os.environ.get("GOOSE_MESH_CHECK"):
        halves = [str(tmp_path / f"{node}-final-half.json") for node in ("buyer", "seller")]
        mesh = subprocess.run([os.environ["GOOSE_MESH_CHECK"], *halves], cwd=HERE.parents[2], capture_output=True,
                              text=True, timeout=120)
        assert mesh.returncode == 0, mesh.stdout + mesh.stderr
    # 01: the Capsule loop_seal wrote names the model and claims no more than runtime_claimed.
    call = json.loads((tmp_path / "buyer-model-call.json").read_text())
    assert call["effect"]["effect_attestation"] == "runtime_claimed" and call["model_attestation"]["model_id"] == \
        "rehearsal-model" and call["key_id"] == (tmp_path / "buyer.key").read_text()
    # 04: the first step of the deal cites the admission, which the opening bundle carries beside the
    # node's record of the deal, both under the buyer's key.
    opening = json.loads((tmp_path / "buyer-opening-bundle.json").read_text())
    first = json.loads((tmp_path / "buyer-half.json").read_text())["steps"][0]["capsule"]
    cited = [l["target"] for l in first["evidence_record_header"]["links"] if l["type"] == "cites"]
    ids = {r["capsule_id"] for r in opening["records"]}
    assert len(cited) == 1 and cited[0] in ids and opening["root"] in ids and cited[0] != opening["root"]
    assert {r["key_id"] for r in opening["records"]} == {(tmp_path / "buyer.key").read_text()}
    bundle = json.loads((tmp_path / "buyer-model-call-bundle.json").read_text())
    assert bundle["bundle_kind"] == "evidence-bundle/v2" and bundle["root"] == call["capsule_id"]
    if os.environ.get("GOOSE_GO_CHECK"):
        check = subprocess.run(["go", "run", "./test/interop/verify", str(tmp_path / "buyer-opening-bundle.json")],
                               cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
        assert check.returncode == 0, check.stdout + check.stderr
        check = subprocess.run(["go", "run", "./test/interop/verify", str(tmp_path / "buyer-model-call-bundle.json")],
                               cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
        assert check.returncode == 0, check.stdout + check.stderr
        check = subprocess.run(["go", "run", "./test/interop/goose-capsule", str(tmp_path / "buyer-model-call.json")],
                               cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
        assert check.returncode == 0 and json.loads(check.stdout)["accepted"], check.stdout + check.stderr
        for node in ("buyer", "seller"):
            check = subprocess.run(["go", "run", "./test/interop/goose-card", str(tmp_path / node / "shelf.db"),
                                    str(tmp_path / f"{node}-card.json"), (tmp_path / f"{node}.key").read_text()],
                                   cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
            assert check.returncode == 0 and json.loads(check.stdout)["accepted"], check.stdout + check.stderr


def test_an_import_refuses_while_the_ledgers_writer_holds_its_lock(tmp_path, clock):
    import fcntl

    source = flat_ledger(tmp_path / "capsule-ledger", 1)
    with open(source.with_name(source.name + ".lock"), "a+") as held:
        fcntl.flock(held.fileno(), fcntl.LOCK_EX)
        node = gn.Node.open(tmp_path / "home", operator="goose-old", now=clock)
        try:
            with pytest.raises(gn.Refused, match="another writer"):
                node.import_ledger(source)
        finally:
            node.close()
    assert source.exists()


def test_an_import_through_a_symlink_takes_the_stores_own_lock(tmp_path, clock):
    import fcntl

    source = flat_ledger(tmp_path / "capsule-ledger", 1)
    link = tmp_path / "linked-ledger"
    link.symlink_to(source)
    with open(source.with_name(source.name + ".lock"), "a+") as held:
        fcntl.flock(held.fileno(), fcntl.LOCK_EX)
        node = gn.Node.open(tmp_path / "home", operator="goose-old", now=clock)
        try:
            with pytest.raises(gn.Refused, match="another writer"):
                node.import_ledger(link)
        finally:
            node.close()


@pytest.fixture
def server(tmp_path, monkeypatch):
    """loop_server's tools, called as Goose calls them, on one node per home."""
    pytest.importorskip("mcp")
    import loop_server

    def use(home: str):
        if loop_server._node is not None:
            loop_server._node.close()
        monkeypatch.setattr(loop_server, "_node", None)
        monkeypatch.setattr(loop_server, "HOME", str(tmp_path / home))
        monkeypatch.setattr(loop_server, "OPERATOR", f"goose-{home}")
        return loop_server

    yield use
    if loop_server._node is not None:
        loop_server._node.close()
        loop_server._node = None


def call(tool, **arguments) -> dict:
    return json.loads(tool(**arguments))


def test_the_server_refuses_unadmitted_peers_and_contradicting_halves(server, tmp_path):
    ls = server("seller")
    seller = call(ls.loop_join)
    ls = server("buyer")
    buyer = call(ls.loop_join)
    assert call(ls.loop_deal_open, peer_key_id=seller["key_id"], item="x", price_minor=500, currency="USD",
                rail="lightning", asked="buy")["error"].startswith("no admission")
    assert call(ls.loop_admit, peer_key_id=seller["key_id"], checkpoint_key_id="zz", decision="GO")["ok"] is False
    assert call(ls.loop_admit, peer_key_id=seller["key_id"], checkpoint_key_id=seller["checkpoint_key_id"],
                decision="GO")["ok"]
    opened = call(ls.loop_deal_open, peer_key_id=seller["key_id"], item="x", price_minor=500, currency="USD",
                  rail="lightning", asked="buy")
    half = str(tmp_path / "half.json")
    assert call(ls.loop_deal_push, deal_id=opened["deal_id"], out_path=half)["ok"]

    ls = server("seller")
    refused = call(ls.loop_deal_respond, request_file=half, asked="sell")
    assert refused == {"ok": False, "error": f"no admission sealed for {buyer['key_id']}: call loop_admit first"}
    call(ls.loop_admit, peer_key_id=buyer["key_id"], checkpoint_key_id=buyer["checkpoint_key_id"], decision="GO")
    assert call(ls.loop_deal_respond, request_file=half, asked="sell")["ok"]
    assert call(ls.loop_deal_respond, request_file=half, asked="sell")["error"].endswith("already answered")
    reply = str(tmp_path / "reply.json")
    call(ls.loop_deal_push, deal_id=opened["deal_id"], out_path=reply)

    # A seller half stating other terms reads contradicted, and nothing is recorded.
    contradicting = json.loads(Path(reply).read_text())
    ls = server("buyer")
    assert call(ls.loop_deal_reconcile, peer_file=reply)["state"] == "matched"
    contradicting["steps"][0]["payload"]["body"]["terms"]["price_minor"] = 400
    Path(reply).write_text(json.dumps(contradicting))
    assert call(ls.loop_deal_reconcile, peer_file=reply)["ok"] is False, "a changed payload no longer verifies"
    # A seller that signs other terms than the buyer opened with reads contradicted.
    second = call(ls.loop_deal_open, peer_key_id=seller["key_id"], item="x", price_minor=500, currency="USD",
                  rail="lightning", asked="buy again")["deal_id"]
    call(ls.loop_deal_push, deal_id=second, out_path=half)
    ls = server("seller")
    node = ls.node()
    opened = node.verify_peer_half(json.loads(Path(half).read_text()))
    chain = node.new_deal(second, dr.ROLE_SELLER, buyer["key_id"], dr.Terms("x", 400, "USD", "lightning", False))
    at = datetime.now(timezone.utc)
    node.seal(chain, chain.respond(at, "sell", opened["records"][-1]["digest"]))
    call(ls.loop_deal_push, deal_id=second, out_path=reply)
    ls = server("buyer")
    assert call(ls.loop_deal_reconcile, peer_file=reply)["state"] == "contradicted"
    # A refused import comes back as JSON, not a tool error.
    assert call(ls.loop_import, ledger=str(tmp_path / "nothing"))["error"].startswith("no flat ledger")


def test_go_checks_a_card_as_of_an_older_tick(nodes, clock, tmp_path):
    if not os.environ.get("GOOSE_GO_CHECK"):
        pytest.skip("GOOSE_GO_CHECK unset")
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    assert buyer.tick().ticked
    old = buyer.standard_history()
    clock.at += timedelta(minutes=6)
    chain = buyer.new_deal("deal-ffffffffffffffff", dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "again") + chain.check_and_pay(clock.at, "ref2"))
    assert buyer.tick().ticked and buyer.standard_history()["tick"] == old["tick"] + 1
    card = tmp_path / "old-card.json"
    card.write_text(json.dumps(old))
    check = subprocess.run(["go", "run", "./test/interop/goose-card", str(buyer.home / "shelf.db"), str(card),
                            buyer.key_id], cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
    assert check.returncode == 0 and json.loads(check.stdout)["accepted"], check.stdout + check.stderr


def test_a_closed_deal_takes_no_more_steps(nodes, clock):
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    for node in (buyer, seller):
        with pytest.raises(ValueError, match="is closed"):
            node.deal(DEAL).close(clock.at, "received")
    with pytest.raises(ValueError, match="is closed"):
        buyer.deal(DEAL).check_and_pay(clock.at, "again")


def test_a_half_the_profiles_chain_stage_refuses_is_refused(nodes, clock):
    """A half honestly signed but whose steps break a profile chain rule (a
    pay after a final close) verifies on every record and fails the chain."""
    buyer, seller = nodes
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "buy"))
    chain = buyer.deal(DEAL)
    buyer.seal(chain, chain.close(clock.at, "received"))
    chain = buyer.deal(DEAL)
    chain.records = [r for r in chain.records if r[1] != deal.TYPE_CLOSE]   # step around the guard
    buyer.seal(chain, chain.check_and_pay(clock.at, "late"))
    with pytest.raises(gn.Refused, match="not a deal the profile allows"):
        gn.verify_half(buyer.half(DEAL), buyer.checkpoint_key_id)


def test_an_answer_cut_short_is_finished_by_answering_again(server, tmp_path):
    ls = server("seller")
    seller = call(ls.loop_join)
    ls = server("buyer")
    buyer = call(ls.loop_join)
    call(ls.loop_admit, peer_key_id=seller["key_id"], checkpoint_key_id=seller["checkpoint_key_id"], decision="GO")
    deal_id = call(ls.loop_deal_open, peer_key_id=seller["key_id"], item="x", price_minor=500, currency="USD",
                   rail="lightning", asked="buy")["deal_id"]
    half = str(tmp_path / "half.json")
    call(ls.loop_deal_push, deal_id=deal_id, out_path=half)
    ls = server("seller")
    call(ls.loop_admit, peer_key_id=buyer["key_id"], checkpoint_key_id=buyer["checkpoint_key_id"], decision="GO")
    node = ls.node()
    chain = node.new_deal(deal_id, dr.ROLE_SELLER, buyer["key_id"], dr.Terms("x", 500, "USD", "lightning", False))
    steps = chain.respond(datetime.now(timezone.utc), "sell", "00" * 32)
    node.seal(chain, steps[:4], admission=node.admission(buyer["key_id"])["record_id"])  # a crash after four of the eight steps
    answered = call(ls.loop_deal_respond, request_file=half, asked="sell")
    assert answered["ok"] and len(answered["records"]) == 5   # task authority's evaluation onwards, not again
    kinds = [k for _, k, _ in node.deal(deal_id).records]
    assert kinds.count(deal.TYPE_PROPOSED_ACTION) == 2 and kinds.count(deal.TYPE_ACTION_RECORD) == 2
    assert call(ls.loop_deal_respond, request_file=half, asked="sell")["error"].endswith("already answered")
    gn.verify_half(node.half(deal_id), node.checkpoint_key_id)


def test_a_sealed_model_call_is_a_runtime_claimed_record_naming_its_model(nodes, clock, tmp_path):
    from agent_action_capsule.canonical import jcs
    from capsule_emit import produce

    buyer, _ = nodes
    sealed, capsule = buyer.seal_model_call("rehearsal", "rehearsal-model", "the prompt", "the completion", clock.at)
    assert sealed.record_type == gn.MODEL_CALL and sealed.record_id == capsule["capsule_id"]
    assert capsule["effect"]["effect_attestation"] == "runtime_claimed"
    assert capsule["effect"]["type"] == "inference_completion"
    assert {k: capsule["model_attestation"][k] for k in ("provider", "model_id")} == {
        "provider": "rehearsal", "model_id": "rehearsal-model"}
    assert capsule["key_id"] == buyer.key_id
    # It is a record of the node book, and verifies as any Capsule does.
    assert [r.record_id for r in buyer.shelf.open(gn.NODE_BOOK).records()] == [sealed.record_id]
    body = dict(capsule)
    signature = body.pop("signature")
    body.pop("key_id")
    produce.verify_capsule(jcs(body))
    assert bytes(produce.verify_envelope(body["capsule_id"], bytes.fromhex(signature)).public_key).hex() == buyer.key_id
    if os.environ.get("GOOSE_GO_CHECK"):
        path = tmp_path / "model-call.json"
        path.write_text(json.dumps(capsule))
        check = subprocess.run(["go", "run", "./test/interop/goose-capsule", str(path)], cwd=HERE.parents[2],
                               capture_output=True, text=True, timeout=600)
        assert check.returncode == 0 and json.loads(check.stdout)["accepted"], check.stdout + check.stderr


def test_a_model_call_without_a_model_is_refused(nodes, clock):
    buyer, _ = nodes
    for provider, model in (("", "m"), ("p", "")):
        with pytest.raises(gn.Refused, match="provider and model_id"):
            buyer.seal_model_call(provider, model, "p", "c", clock.at)
    assert buyer.shelf.open(gn.NODE_BOOK).records() == []


def test_go_refuses_a_sealed_model_call_that_was_altered(nodes, clock, tmp_path):
    if not os.environ.get("GOOSE_GO_CHECK"):
        pytest.skip("GOOSE_GO_CHECK unset")
    buyer, _ = nodes
    _, capsule = buyer.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
    capsule["model_attestation"]["model_id"] = "another-model"
    path = tmp_path / "altered.json"
    path.write_text(json.dumps(capsule))
    check = subprocess.run(["go", "run", "./test/interop/goose-capsule", str(path)], cwd=HERE.parents[2],
                           capture_output=True, text=True, timeout=600)
    assert check.returncode == 1 and "class 1" in check.stderr, check.stdout + check.stderr


def _witness_script():
    import importlib.util

    path = HERE.parents[2] / "test" / "interop" / "python-evidencebook" / "shelf.py"
    spec = importlib.util.spec_from_file_location("shelfscript", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_a_book_bundle_proves_a_record_and_go_accepts_it(nodes, clock, tmp_path):
    from agent_action_capsule import bundle as aac

    buyer, _ = nodes
    buyer.note(gn.JOINED, {"operator": buyer.operator, "key_id": buyer.key_id})
    sealed, _ = buyer.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
    built = buyer.book_bundle(gn.NODE_BOOK, sealed.record_id)
    assert built["root"] == sealed.record_id and "extensions" not in built, "no witnessed tick yet"
    assert aac.verify_bundle(built, strict=True).verdict == "VALID"
    # The bundle is the one record's place in the book, so the book's other record is carried too.
    assert len(built["records"]) == 2
    if os.environ.get("GOOSE_GO_CHECK"):
        path = tmp_path / "bundle.json"
        path.write_text(json.dumps(built))
        check = subprocess.run(["go", "run", "./test/interop/verify", str(path)], cwd=HERE.parents[2],
                               capture_output=True, text=True, timeout=600)
        assert check.returncode == 0, check.stdout + check.stderr


class _Witness:
    """The test witness (the shelf script's key) as a ``cll.checkpoint.witness`` submitter."""

    def __init__(self, script, endpoint: str) -> None:
        self.script, self.asked = script, 0
        assert endpoint == script.DIRECTORY["witnesses"][0]["endpoint"]

    def submit(self, statement: bytes):
        from types import SimpleNamespace

        self.asked += 1
        r = self.script.mint_receipt(statement)
        return SimpleNamespace(bytes=r.receipt, entry_hash=r.entry_hash, leaf_index=r.leaf_index, tree_size=r.tree_size)


def test_a_bundle_carries_the_witnesss_receipt_for_the_books_own_checkpoint(tmp_path, clock):
    from agent_action_capsule import bundle as aac

    script, made = _witness_script(), []

    def client(endpoint):
        made.append(_Witness(script, endpoint))
        return made[-1]

    with gn.Node.open(tmp_path / "w", operator="goose-w", now=clock, witnesses=script.DIRECTORY,
                      witness_client=client) as node:
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        built = node.book_bundle(gn.NODE_BOOK, sealed.record_id)
        [receipt] = built["checkpoint"]["witnesses"]
        result = aac.verify_bundle(built, strict=True, witness_directory=script.DIRECTORY)
        assert [r.status for r in result.witness_receipts] == ["pass"]
        assert receipt["ts_url"] == script.DIRECTORY["witnesses"][0]["endpoint"] and receipt["receipt_b64"]
        # The same checkpoint is asked once; a bundle without the directory's witness would not verify.
        assert node.book_bundle(gn.NODE_BOOK, sealed.record_id)["checkpoint"]["witnesses"] == [receipt]
        assert sum(w.asked for w in made) == 1
        # A tick that the witness also saw adds the cadence chain.
        assert node.tick().ticked
        tick = node.cadence.last()
        assert node.cadence.witnessed(tick.n, script.mint_receipt(tick.checkpoint)).state == "VERIFIED"
        again = node.book_bundle(gn.NODE_BOOK, sealed.record_id)
        assert again["extensions"][gn.CADENCE_EXTENSION]["state"] == "witnessed"


def test_a_bad_receipt_is_not_kept_and_a_bad_endpoint_is_refused(tmp_path, clock):
    script = _witness_script()
    good = _Witness(script, script.DIRECTORY["witnesses"][0]["endpoint"])
    answers = []

    class Flaky:
        def submit(self, statement):
            answers.append(1)
            if len(answers) == 1:
                bad = good.submit(statement)
                bad.entry_hash = "00" * 32  # a receipt that does not bind this checkpoint
                return bad
            return good.submit(statement)

    with gn.Node.open(tmp_path / "w", operator="goose-w", now=clock, witnesses=script.DIRECTORY,
                      witness_client=lambda _: Flaky()) as node:
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        with pytest.raises(gn.Refused, match="does not verify"):
            node.book_bundle(gn.NODE_BOOK, sealed.record_id)
        assert node.book_bundle(gn.NODE_BOOK, sealed.record_id)["checkpoint"]["witnesses"]
        assert len(answers) == 2
    directory = {"witnesses": [{**script.DIRECTORY["witnesses"][0], "endpoint": "witness.example"}]}
    with gn.Node.open(tmp_path / "x", operator="goose-x", now=clock, witnesses=directory) as node:
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        with pytest.raises(gn.Refused, match="gave no receipt"):
            node.book_bundle(gn.NODE_BOOK, sealed.record_id)


def test_a_receipt_the_directory_cannot_verify_and_a_two_witness_directory_are_refused(tmp_path, clock):
    script = _witness_script()
    row = script.DIRECTORY["witnesses"][0]

    class Forged(_Witness):
        def submit(self, statement):
            got = super().submit(statement)
            got.bytes = bytes([got.bytes[0] ^ 1]) + got.bytes[1:]
            return got

    with gn.Node.open(tmp_path / "w", operator="goose-w", now=clock, witnesses=script.DIRECTORY,
                      witness_client=lambda e: Forged(script, e)) as node:
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        with pytest.raises(gn.Refused):
            node.book_bundle(gn.NODE_BOOK, sealed.record_id)
        assert not node._db.execute("SELECT 1 FROM goose_book_receipts").fetchall(), "a bad receipt is not kept"
    two = {"witnesses": [row, {**row, "name": "another", "endpoint": "https://other.test"}]}
    with gn.Node.open(tmp_path / "x", operator="goose-x", now=clock, witnesses=two) as node:
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        with pytest.raises(gn.Refused, match="exactly one witness"):
            node.book_bundle(gn.NODE_BOOK, sealed.record_id)


def test_a_witness_that_fails_leaves_no_bundle(tmp_path, clock):
    from cll.log import ContentionError

    script = _witness_script()

    class Down:
        def submit(self, statement):
            raise ContentionError("connection refused")

    with gn.Node.open(tmp_path / "w", operator="goose-w", now=clock, witnesses=script.DIRECTORY,
                      witness_client=lambda _: Down()) as node:
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        with pytest.raises(gn.Refused, match="gave no receipt"):
            node.book_bundle(gn.NODE_BOOK, sealed.record_id)


def test_a_book_bundle_refuses_what_the_node_does_not_hold(nodes):
    buyer, _ = nodes
    sealed, _ = buyer.seal_model_call("rehearsal", "rehearsal-model", "p", "c", AT)
    with pytest.raises(gn.Refused, match="holds no book"):
        buyer.book_bundle("deal/none", sealed.record_id)
    with pytest.raises(gn.Refused, match="holds no record"):
        buyer.book_bundle(gn.NODE_BOOK, "00" * 32)


def test_the_bundle_tool_writes_the_checkpoint_and_the_witness_files_beside_it(tmp_path, clock, monkeypatch):
    pytest.importorskip("mcp")
    import hashlib

    import loop_server

    script = _witness_script()
    directory = tmp_path / "witnesses.json"
    directory.write_text(json.dumps(script.DIRECTORY))
    monkeypatch.setenv("GOOSE_WITNESSES", str(directory))
    with gn.Node.open(tmp_path / "w", operator="goose-w", now=clock, witnesses=script.DIRECTORY,
                      witness_client=lambda endpoint: _Witness(script, endpoint)) as node:
        monkeypatch.setattr(loop_server, "_node", node)
        sealed, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p", "c", clock.at)
        out = tmp_path / "call.json"
        done = json.loads(loop_server.loop_book_bundle(gn.NODE_BOOK, sealed.record_id, str(out)))
        assert done["ok"] and done["witnessed"] and done["cadence_extent"] is None
        statement = (tmp_path / "call.checkpoint.cose").read_bytes()
        assert statement == node.book_checkpoint(gn.NODE_BOOK)
        receipt = json.loads((tmp_path / "call.witness-receipt.json").read_text())
        assert receipt == json.loads(out.read_text())["checkpoint"]["witnesses"][0]
        # The receipt is over this very checkpoint: its entry hash binds the statement the file holds.
        assert receipt["entry_hash"] == script.mint_receipt(statement).entry_hash
        assert json.loads((tmp_path / "call.witnesses.json").read_text()) == script.DIRECTORY
        if os.environ.get("GOOSE_GO_CHECK"):
            check = subprocess.run(["go", "run", "./test/interop/verify", str(out), node.checkpoint_key_id,
                                    str(tmp_path / "call.witnesses.json")], cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
            assert check.returncode == 0 and "witnessed" in check.stdout, check.stdout + check.stderr
        assert not (tmp_path / "call.cadence-checkpoint.cose").exists()
        assert node.tick().ticked
        tick = node.cadence.last()
        node.cadence.witnessed(tick.n, script.mint_receipt(tick.checkpoint))
        again = json.loads(loop_server.loop_book_bundle(gn.NODE_BOOK, sealed.record_id, str(out)))
        assert again["cadence_extent"] == "all"
        # A record sealed after the tick is under a newer checkpoint: the chain is of the earlier one ("part").
        later, _ = node.seal_model_call("rehearsal", "rehearsal-model", "p2", "c2", clock.at)
        part = json.loads(loop_server.loop_book_bundle(gn.NODE_BOOK, later.record_id, str(out)))
        assert part["witnessed"] and part["cadence_extent"] == "part"
        refused = json.loads(loop_server.loop_book_bundle(gn.NODE_BOOK, "00" * 32, str(out)))
        assert not refused["ok"] and "holds no record" in refused["error"]
        assert (tmp_path / "call.cadence-checkpoint.cose").read_bytes() == tick.checkpoint
        assert (tmp_path / "call.cadence-witness-receipt.json").is_file()

        receipt = json.loads((tmp_path / "call.witness-receipt.json").read_text())
        assert receipt["receipt_b64"] and (tmp_path / "call.witnesses.json").read_bytes() == directory.read_bytes()
        assert (tmp_path / "call.checkpoint.cose").read_bytes() == node.book_checkpoint(gn.NODE_BOOK)

def _admit(node: gn.Node, peer: gn.Node, decision: str = "GO") -> str:
    return node.note(gn.ADMITTED, {"peer": peer.key_id, "checkpoint_key_id": peer.checkpoint_key_id,
                                   "decision": decision, "reason": ""}).record_id


def test_the_first_deal_step_cites_the_admission_and_the_node_book_records_the_opening(nodes, clock):
    buyer, seller = nodes
    admission = _admit(buyer, seller)
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    sealed = buyer.open_deal(chain, chain.open(clock.at, "buy"))
    first = buyer.shelf.open(f"deal/{DEAL}").records()[0]
    assert first.record_id == sealed[0].record_id
    assert first.header.links_to("cites") == [admission]
    opened = buyer.deal_opened(DEAL)
    record = buyer.shelf.open(gn.NODE_BOOK).get(opened)
    assert record.header.links_to("cites") == [admission] and record.seq > buyer.shelf.open(gn.NODE_BOOK).get(admission).seq
    assert record.header.statement["first_step"] == first.record_id
    # Ordered by time as well as by seq: the admission, then the deal's first step, then the opening record.
    node_book = buyer.shelf.open(gn.NODE_BOOK)
    times = [node_book.get(admission).header.committed_at, first.header.committed_at, record.header.committed_at]
    assert times == sorted(times) and times[0] <= times[1] <= times[2]
    # The half still verifies as a counterparty checks it.
    gn.verify_half(buyer.half(DEAL), buyer.checkpoint_key_id)


def test_no_deal_opens_without_a_go_and_a_no_is_cited_by_nothing(nodes, clock):
    buyer, seller = nodes
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    steps = chain.open(clock.at, "buy")
    with pytest.raises(gn.Refused, match="no admission"):
        buyer.open_deal(chain, steps)
    no = _admit(buyer, seller, "NO")
    with pytest.raises(gn.Refused, match="is NO"):
        buyer.open_deal(chain, steps)
    assert DEAL not in "".join(buyer.shelf.books()) and not buyer.notes(gn.DEAL_OPENED)
    assert all(no not in r.header.links_to("cites") for r in buyer.shelf.open(gn.NODE_BOOK).records())
    with pytest.raises(gn.Refused, match="sealed no opening"):
        buyer.deal_opened(DEAL)


def test_the_admission_and_the_deal_opening_export_as_one_bundle(nodes, clock, tmp_path):
    from agent_action_capsule import bundle as aac

    buyer, seller = nodes
    admission = _admit(buyer, seller)
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.open_deal(chain, chain.open(clock.at, "buy"))
    built = buyer.book_bundle(gn.NODE_BOOK, buyer.deal_opened(DEAL))
    assert str(aac.verify_bundle(built, strict=True).verdict).endswith("VALID")
    by_id = {r["capsule_id"]: r for r in built["records"]}
    assert admission in by_id and built["root"] in by_id
    keys = {r["key_id"] for r in built["records"]}
    assert keys == {buyer.key_id}
    if os.environ.get("GOOSE_GO_CHECK"):
        path = tmp_path / "opening.json"
        path.write_text(json.dumps(built))
        check = subprocess.run(["go", "run", "./test/interop/verify", str(path)], cwd=HERE.parents[2],
                               capture_output=True, text=True, timeout=600)
        assert check.returncode == 0, check.stdout + check.stderr


def test_the_seller_cites_its_own_admission_too(nodes, clock):
    buyer, seller = nodes
    _admit(buyer, seller)
    admission = _admit(seller, buyer)
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.open_deal(chain, chain.open(clock.at, "buy"))
    opened = gn.verify_half(buyer.half(DEAL), buyer.checkpoint_key_id)
    chain = seller.new_deal(DEAL, dr.ROLE_SELLER, buyer.key_id, TERMS)
    seller.open_deal(chain, chain.respond(clock.at, "sell", opened["records"][-1]["digest"]))
    records = seller.shelf.open(f"deal/{DEAL}").records()
    assert records[0].header.links_to("cites") == [admission]
    assert all(admission not in r.header.links_to("cites") for r in records[1:])
    assert seller.shelf.open(gn.NODE_BOOK).get(seller.deal_opened(DEAL)).header.links_to("cites") == [admission]


def test_a_later_no_refuses_a_deal_and_a_cut_short_open_is_finished(nodes, clock):
    buyer, seller = nodes
    _admit(buyer, seller)
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    admission = buyer.admission(seller.key_id)["record_id"]
    buyer.seal(chain, chain.open(clock.at, "buy"), admission=admission)  # the open stops before its record
    with pytest.raises(gn.Refused, match="sealed no opening"):
        buyer.deal_opened(DEAL)
    opened = buyer.ensure_opening(DEAL)
    assert buyer.ensure_opening(DEAL) == opened and buyer.deal_opened(DEAL) == opened
    assert buyer.shelf.open(gn.NODE_BOOK).get(opened).header.links_to("cites") == [admission]
    other = buyer.new_deal("deal-ffffffffffffffff", dr.ROLE_BUYER, seller.key_id, TERMS)
    with pytest.raises(gn.Refused, match="at least one step"):
        buyer.open_deal(other, [])
    _admit(buyer, seller, "NO")
    with pytest.raises(gn.Refused, match="is NO"):
        buyer.open_deal(other, other.open(clock.at, "again"))
