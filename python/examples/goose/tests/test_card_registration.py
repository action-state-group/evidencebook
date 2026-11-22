# SPDX-License-Identifier: Apache-2.0
"""Registering a Goose node's standard_history/1 card on a transparency
service its host names (evidencebook#620): opt-in with no default, the
card complete without it, a failed registration changing nothing, and Go's
card_registered over the statement the node keeps beside the card."""
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import card_service as cs  # noqa: E402
import goose_node as gn  # noqa: E402
from card_registration import check_card_registered  # noqa: E402
from fake_anchor import IAT, FakeAnchor  # noqa: E402
from test_goose import Clock, clock, deal_through, nodes  # noqa: E402,F401


@pytest.fixture
def anchor():
    service = FakeAnchor()
    yield service
    service.close()


def service_file(tmp_path: Path, anchor: FakeAnchor, **over) -> Path:
    doc = {"name": "test-anchor", "operator": "the test", "url": anchor.url, "keys": [anchor.public_spki_hex], **over}
    path = tmp_path / "service.json"
    path.write_text(json.dumps(doc))
    return path


def ticked(nodes, clock):
    buyer, seller = nodes
    deal_through(buyer, seller, clock)
    assert buyer.tick().ticked
    return buyer


def test_a_card_is_registered_and_card_registered_passes(nodes, clock, anchor, tmp_path):
    buyer = ticked(nodes, clock)
    service = cs.load_service(service_file(tmp_path, anchor))
    before = buyer.standard_history()
    result = buyer.register_card(cs.AnchorRegistrar(service), service.policy)
    assert result["card"] == before["card"], "registration changes nothing about the card"
    assert len(anchor.posted) == 1
    report = check_card_registered(
        result["card"].encode(), gn._Holder(buyer.key_id, result["anchor"]), [bytes.fromhex(result["statement"])],
        service.policy)
    assert report.status == "PASS" and report.service == "test-anchor" and report.registration_time == IAT
    assert buyer.standard_history()["card"] == before["card"]
    if os.environ.get("GOOSE_GO_CHECK"):
        card, statement, policy = tmp_path / "card.json", tmp_path / "statement.json", tmp_path / "policy.json"
        card.write_text(json.dumps(before))
        statement.write_text(json.dumps(result))
        policy.write_text(json.dumps(service.policy))
        check = subprocess.run(["go", "run", "./test/interop/goose-card", str(buyer.home / "shelf.db"), str(card),
                                buyer.key_id, str(statement), str(policy)], cwd=HERE.parents[2],
                               capture_output=True, text=True, timeout=600)
        assert check.returncode == 0, check.stdout + check.stderr
        out = json.loads(check.stdout)
        assert out["accepted"] and out["card_registered"]["status"] == "PASS"
        assert out["anchor_inclusion"] == "NOT_CHECKED"
        assert out["registration"].startswith("the holder registered this card with test-anchor at ")


def test_no_service_means_nothing_is_sent(nodes, clock):
    buyer = ticked(nodes, clock)
    with pytest.raises(gn.Refused, match="no default"):
        buyer.register_card(None, {"witnesses": []})


def test_a_service_file_must_say_who_runs_it_and_its_keys(tmp_path):
    for doc in ({}, {"name": "n", "url": "https://x"}, {"name": "n", "operator": "o", "url": "ftp://x", "keys": ["aa"]},
                {"name": "n", "operator": "o", "url": "https://x", "keys": []}, []):
        path = tmp_path / "bad.json"
        path.write_text(json.dumps(doc))
        with pytest.raises(cs.ServiceError):
            cs.load_service(path)
    with pytest.raises(cs.ServiceError):
        cs.load_service(tmp_path / "missing.json")


@pytest.mark.parametrize("mode", ["redirect", "junk", "huge"])
def test_a_service_that_misbehaves_leaves_the_card_as_it_was(nodes, clock, tmp_path, mode):
    buyer = ticked(nodes, clock)
    service_misbehaving = FakeAnchor(mode=mode)
    try:
        service = cs.load_service(service_file(tmp_path, service_misbehaving))
        before = buyer.standard_history()["card"]
        with pytest.raises(cs.ServiceError):
            buyer.register_card(cs.AnchorRegistrar(service), service.policy)
        assert buyer.standard_history()["card"] == before
    finally:
        service_misbehaving.close()


def test_a_service_that_trickles_its_answer_is_cut_off(nodes, clock, tmp_path, monkeypatch):
    monkeypatch.setattr(cs, "TOTAL_SECONDS", 1)
    buyer = ticked(nodes, clock)
    slow = FakeAnchor(mode="slow")
    try:
        service = cs.load_service(service_file(tmp_path, slow))
        with pytest.raises(cs.ServiceError, match="no complete answer"):
            buyer.register_card(cs.AnchorRegistrar(service), service.policy)
    finally:
        slow.close()


def test_nothing_is_posted_when_the_receipt_cannot_be_checked(nodes, clock, anchor, tmp_path, monkeypatch):
    """With an agent-action-capsule that lacks the card checks, registration refuses before the
    permanent post, not after it."""
    import card_registration as cr

    buyer = ticked(nodes, clock)
    service = cs.load_service(service_file(tmp_path, anchor))
    monkeypatch.delattr(cr._witness, "check_card_statement")
    with pytest.raises(gn.Refused, match="0.19.0"):
        buyer.register_card(cs.AnchorRegistrar(service), service.policy)
    assert anchor.posted == []


def test_the_tool_checks_where_it_writes_before_it_posts(nodes, clock, anchor, tmp_path, monkeypatch):
    """A registration is permanent, so an out_path that cannot be written, or that already holds a
    statement, is refused before anything is sent (the Receipt would otherwise be lost and a retry
    would add a second entry)."""
    import loop_server

    buyer = ticked(nodes, clock)
    monkeypatch.setattr(loop_server, "_node", buyer)
    monkeypatch.setenv("GOOSE_CARD_SERVICE", str(service_file(tmp_path, anchor)))
    taken = tmp_path / "taken.json"
    taken.write_text("an earlier statement")
    for bad in (tmp_path / "missing-dir" / "card.json", taken):
        refused = json.loads(loop_server.loop_register_card(out_path=str(bad)))
        assert refused["ok"] is False
        assert anchor.posted == []
    assert taken.read_text() == "an earlier statement"
    good = tmp_path / "card.json"
    done = json.loads(loop_server.loop_register_card(out_path=str(good)))
    assert done["ok"] is True and len(anchor.posted) == 1
    assert json.loads(good.read_text())["statement"]


def test_a_service_that_is_down_leaves_the_card_as_it_was(nodes, clock, tmp_path):
    buyer = ticked(nodes, clock)
    down = FakeAnchor()
    path = service_file(tmp_path, down)
    down.close()
    service = cs.load_service(path)
    before = buyer.standard_history()["card"]
    with pytest.raises(cs.ServiceError):
        buyer.register_card(cs.AnchorRegistrar(service), service.policy)
    assert buyer.standard_history()["card"] == before


def test_a_receipt_that_does_not_cover_the_statement_is_refused(nodes, clock, tmp_path):
    buyer = ticked(nodes, clock)
    liar = FakeAnchor(mode="bad-receipt")
    try:
        service = cs.load_service(service_file(tmp_path, liar))
        with pytest.raises(gn.Refused, match="not registered"):
            buyer.register_card(cs.AnchorRegistrar(service), service.policy)
    finally:
        liar.close()


def test_a_receipt_of_a_service_the_policy_does_not_list_is_refused(nodes, clock, anchor, tmp_path):
    buyer = ticked(nodes, clock)
    service = cs.load_service(service_file(tmp_path, anchor, keys=[FakeAnchor.spki_for(12)]))
    with pytest.raises(gn.Refused, match="not registered"):
        buyer.register_card(cs.AnchorRegistrar(service), service.policy)


def test_no_tick_means_no_card_to_register(nodes, anchor, tmp_path):
    buyer, _ = nodes
    service = cs.load_service(service_file(tmp_path, anchor))
    with pytest.raises(gn.Refused, match="loop_tick"):
        buyer.register_card(cs.AnchorRegistrar(service), service.policy)
    assert anchor.posted == []


def test_the_scripted_run_registers_each_card_only_when_a_service_is_named(tmp_path, anchor):
    """scripted_run.py spoken to over MCP stdio, as Goose speaks to it: with no service file it
    sends nothing, and with one each node's card is registered and card_registered passes."""
    run = subprocess.run([sys.executable, str(HERE / "scripted_run.py"), str(tmp_path / "plain")],
                         capture_output=True, text=True, timeout=600, env={k: v for k, v in os.environ.items()
                                                                            if k != "GOOSE_CARD_SERVICE"})
    assert run.returncode == 0, run.stdout + run.stderr
    assert anchor.posted == [] and not list((tmp_path / "plain").glob("*-card-statement.json"))
    service = service_file(tmp_path, anchor)
    run = subprocess.run([sys.executable, str(HERE / "scripted_run.py"), str(tmp_path / "registered")],
                         capture_output=True, text=True, timeout=600, env={**os.environ, "GOOSE_CARD_SERVICE": str(service)})
    assert run.returncode == 0, run.stdout + run.stderr
    assert len(anchor.posted) == 2
    policy = cs.load_service(service).policy
    for node in ("buyer", "seller"):
        out = tmp_path / "registered"
        written = json.loads((out / f"{node}-card-statement.json").read_text())
        card = json.loads((out / f"{node}-card.json").read_text())
        assert written["card"] == card["card"] and written["service"]["operator"] == "the test"
        report = check_card_registered(written["card"].encode(), gn._Holder(written["responder"], written["anchor"]),
                                       [bytes.fromhex(written["statement"])], policy)
        assert report.status == "PASS"
        if os.environ.get("GOOSE_GO_CHECK"):
            (out / "policy.json").write_text(json.dumps(policy))
            check = subprocess.run(["go", "run", "./test/interop/goose-card", str(out / node / "shelf.db"),
                                    str(out / f"{node}-card.json"), (out / f"{node}.key").read_text(),
                                    str(out / f"{node}-card-statement.json"), str(out / "policy.json")],
                                   cwd=HERE.parents[2], capture_output=True, text=True, timeout=600)
            assert check.returncode == 0, check.stdout + check.stderr
            assert json.loads(check.stdout)["card_registered"]["status"] == "PASS"


def test_the_scripted_run_goes_on_when_a_registration_fails(tmp_path):
    """The card is complete without registration: a service that is down does not stop the run, and
    every card and half is still written."""
    down = FakeAnchor()
    path = service_file(tmp_path, down)
    down.close()
    run = subprocess.run([sys.executable, str(HERE / "scripted_run.py"), str(tmp_path / "out")],
                         capture_output=True, text=True, timeout=600, env={**os.environ, "GOOSE_CARD_SERVICE": str(path)})
    assert run.returncode == 0, run.stdout + run.stderr
    assert "NOT DONE (the run continues)" in run.stdout
    out = tmp_path / "out"
    for node in ("buyer", "seller"):
        assert (out / f"{node}-card.json").exists() and (out / f"{node}-history-card.json").exists()
        assert not (out / f"{node}-card-statement.json").exists()
