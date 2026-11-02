# SPDX-License-Identifier: Apache-2.0
"""Step 03 Advertise: the commitment ``loop_advertise`` writes is one the
loop's checker (evidence-network/loop ``scripts/check_commitment.py``)
accepts, and the same commitment with an extra member, or signed by another
key, is one it refuses.

``GOOSE_LOOP_SCRIPTS`` names the loop checkout's ``scripts/`` directory (CI
checks the loop out at the commit it pins); without it, the checker's tests
skip."""
from __future__ import annotations

import base64
import hashlib
import json
import os
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import commitment as cm  # noqa: E402
import goose_node as gn  # noqa: E402
from agent_action_capsule.canonical import jcs  # noqa: E402

AT = datetime(2026, 11, 1, 10, tzinfo=timezone.utc)
LOOP = os.environ.get("GOOSE_LOOP_SCRIPTS")
needs_loop = pytest.mark.skipif(not LOOP, reason="GOOSE_LOOP_SCRIPTS (the loop's scripts/) is not set")


@pytest.fixture
def node(tmp_path):
    n = gn.Node.open(tmp_path / "node", operator="goose-test", now=lambda: AT)
    n.note(gn.JOINED, {"operator": n.operator, "key_id": n.key_id})
    yield n
    n.close()


def check(directory: Path) -> subprocess.CompletedProcess:
    return subprocess.run([sys.executable, "-B", str(Path(LOOP) / "check_commitment.py"), str(directory)],
                          capture_output=True, text=True, timeout=120)


def resign(doc: dict, key: Ed25519PrivateKey) -> dict:
    doc = {k: v for k, v in doc.items() if k != "sig"}
    value = base64.urlsafe_b64encode(key.sign(jcs(doc))).rstrip(b"=").decode()
    return {**doc, "sig": {"alg": "Ed25519", "canon": "jcs", "value": value}}


def test_the_commitment_staples_the_node_books_card_and_carries_no_count(node):
    doc, policy_bytes = cm.build(node, now=AT)
    assert doc["profile"] == "evidence-commitment/1" and doc["node_id"] == doc["key_id"] == node.key_id
    assert doc["issued_at"] == "2026-11-01T10:00:00Z" and doc["human_gated"] is True
    [card] = doc["history_cards"]
    assert card["stream"] == gn.NODE_BOOK and card["community"] == cm.COMMUNITY
    answer = json.loads(base64.b64decode(card["bytes"]))
    assert answer["derivation"] == "history_card/1" and answer["responder"] == node.key_id
    # The checkpoint is the one the card is anchored on.
    assert doc["checkpoint"]["sha256"] == answer["anchor"]["digest"] == \
        hashlib.sha256(base64.b64decode(doc["checkpoint"]["bytes"])).hexdigest()
    assert doc["lcap"]["policy_digest"] == hashlib.sha256(policy_bytes).hexdigest()
    policy = json.loads(policy_bytes)
    assert policy["profile"] == "disclosure-policy/0" and policy["key_id"] == node.key_id

    def numbers(v):
        if isinstance(v, dict):
            return [x for k, w in v.items() if k != "bytes" for x in numbers(w)]
        if isinstance(v, list):
            return [x for w in v for x in numbers(w)]
        return [v] if isinstance(v, (int, float)) and not isinstance(v, bool) else []
    assert numbers(doc) == []


def test_a_node_with_a_listed_id_names_it_and_its_policy(node):
    listed = "ab" * 32
    doc, _ = cm.build(node, listed)
    assert doc["node_id"] == listed and doc["key_id"] == node.key_id
    assert doc["lcap"]["policy_uri"] == f"policies/{listed}.json"


@pytest.mark.parametrize("node_id, settings", [
    ("", cm.Settings(answers_within="24")),
    ("", cm.Settings(answers_within="24H")),
    ("", cm.Settings(answers_within="P1W")),
    ("", cm.Settings(answers_within="PT24h")),
    ("", cm.Settings(bindings=("Evidencebook/Respond",))),
    ("", cm.Settings(bindings=())),
    ("", cm.Settings(endpoint=" ")),
    ("../../x", cm.Settings()),
    ("AB" * 32, cm.Settings()),
])
def test_what_the_checker_would_refuse_is_refused_before_signing(node, tmp_path, node_id, settings):
    with pytest.raises(gn.Refused):
        cm.build(node, node_id, settings)


@needs_loop
def test_the_loops_checker_accepts_the_commitment(node, tmp_path):
    doc, policy_bytes = cm.build(node)
    commitment, policy = cm.write(tmp_path / "commitments", doc, policy_bytes)
    assert commitment.name == f"{node.key_id}.json" and policy.parent.name == "policies"
    r = check(tmp_path / "commitments")
    assert r.returncode == 0, r.stdout + r.stderr


@needs_loop
def test_the_loops_checker_refuses_an_extra_member(node, tmp_path):
    doc, policy_bytes = cm.build(node)
    # Signed again by the node's own key, so the extra member is the only fault.
    key = gn._key(node.home / "producer.key")
    cm.write(tmp_path / "c", resign({**doc, "deals": "several"}, key), policy_bytes)
    r = check(tmp_path / "c")
    assert r.returncode == 1 and "unknown field deals" in r.stderr, r.stdout + r.stderr


@needs_loop
def test_the_loops_checker_refuses_a_commitment_signed_by_another_key(node, tmp_path):
    doc, policy_bytes = cm.build(node)
    cm.write(tmp_path / "c", resign(doc, Ed25519PrivateKey.generate()), policy_bytes)
    r = check(tmp_path / "c")
    assert r.returncode == 1 and "signature does not verify" in r.stderr, r.stdout + r.stderr


@needs_loop
def test_the_loops_checker_accepts_a_commitment_that_names_the_mailbox(node, tmp_path):
    doc, policy_bytes = cm.build(node, settings=cm.Settings(relays=("wss://relay.example",), endpoint="nostr:mailbox",
                                                            answers_within="PT1H", poll_interval=60))
    assert doc["ask"]["nostr"] == {"mailbox_key": node.mailbox_key, "relays": ["wss://relay.example"]}
    cm.write(tmp_path / "commitments", doc, policy_bytes)
    r = check(tmp_path / "commitments")
    assert r.returncode == 0, r.stdout + r.stderr
