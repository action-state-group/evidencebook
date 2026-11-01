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
    opened = gn.verify_half(buyer.half(DEAL))
    chain = seller.new_deal(DEAL, dr.ROLE_SELLER, buyer.key_id, TERMS)
    seller.seal(chain, chain.open(clock.at, "sell 1k tokens") + chain.offer(clock.at)
                + chain.accept_and_commit(clock.at, opened["records"][-1]["digest"]))
    clock.at += timedelta(seconds=10)
    chain = buyer.deal(DEAL)
    buyer.seal(chain, chain.check_and_pay(clock.at, "ln:payment-hash"))
    clock.at += timedelta(seconds=10)
    for node in (buyer, seller):
        chain = node.deal(DEAL)
        node.seal(chain, chain.close(clock.at, status))
    for me, peer in ((buyer, seller), (seller, buyer)):
        verified = gn.verify_half(me.half(DEAL))
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
    gn.verify_half(half)

    def broken(change) -> None:
        bad = copy.deepcopy(half)
        change(bad)
        with pytest.raises(gn.Refused):
            gn.verify_half(bad)

    broken(lambda h: h["steps"][3]["payload"]["body"].update(disposition="ASK"))     # payload not committed
    broken(lambda h: h["steps"][1]["capsule"].update(key_id=buyer.key_id))           # another signer
    broken(lambda h: h["steps"].pop(2))                                             # a step missing
    broken(lambda h: h["steps"].pop())                                              # the checkpoint covers more
    broken(lambda h: h["steps"][0]["capsule"].update(deal_id="deal-ffffffffffffffff"))
    broken(lambda h: h.update(half="something else"))


def test_a_deal_needs_its_own_role_and_an_open_book(nodes, clock):
    buyer, seller = nodes
    chain = buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    buyer.seal(chain, chain.open(clock.at, "buy"))
    with pytest.raises(gn.Refused):
        buyer.new_deal(DEAL, dr.ROLE_BUYER, seller.key_id, TERMS)
    with pytest.raises(ValueError):
        buyer.deal(DEAL).offer(clock.at)
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
            with pytest.raises(Exception):
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
    if os.environ.get("GOOSE_GO_CHECK"):
        for node in ("buyer", "seller"):
            check = subprocess.run(["go", "run", "./test/interop/goose-card", str(tmp_path / node / "shelf.db"),
                                    str(tmp_path / f"{node}-card.json"), (tmp_path / f"{node}.key").read_text()],
                                   cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
            assert check.returncode == 0 and json.loads(check.stdout)["accepted"], check.stdout + check.stderr
