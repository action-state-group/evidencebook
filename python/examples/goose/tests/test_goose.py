# SPDX-License-Identifier: Apache-2.0
"""The Goose loop reference on capsulectl: one scripted run of two nodes over MCP stdio (each a real
``capsulectl-deal`` profile), whose files are then checked by Go (``capsulectl-deal verify``, ``goose-capsule``)
and by the loop's own commitment checker; and the refusals a tool makes before it seals anything.

``GOOSE_CAPSULECTL_DEAL`` and ``GOOSE_CAPSULE_ENGINE`` name the binaries (every test skips without them);
``GOOSE_GO_CHECK=1`` runs Go's check of the model-call Capsule (``go run ./test/interop/goose-capsule``) and
``GOOSE_LOOP_SCRIPTS`` is the loop checkout's ``scripts/`` (the commitment checker); each skips when unset."""
from __future__ import annotations

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
def test_the_loops_checker_refuses_an_extra_member_and_another_signer(run, tmp_path):
    key = (run / "buyer.key").read_text()
    doc = json.loads((run / "commitments" / f"{key}.json").read_text())
    for name, change in (("extra", lambda d: d.update(count=3)), ("signer", lambda d: d.update(node_id="0" * 64))):
        d = json.loads(json.dumps(doc))
        change(d)
        (tmp_path / name).mkdir()
        (tmp_path / name / f"{d['node_id']}.json").write_text(json.dumps(d))
        done = subprocess.run([sys.executable, str(Path(LOOP) / "check_commitment.py"), str(tmp_path / name)],
                              capture_output=True, text=True, env={**os.environ, "PYTHONPATH": LOOP})
        assert done.returncode != 0, name


# -- refusals: a tool that refuses seals nothing -----------------------------------------------


@pytest.fixture
def node(tmp_path):
    n = gn.Node.open(tmp_path / "node", operator="goose-test")
    n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id}, once="key_id")
    yield n
    n.close()


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
