# SPDX-License-Identifier: Apache-2.0
"""Step 03 Advertise: the node's signed ``evidence-commitment/1``.

The commitment says how the node can be asked for evidence and what it can
answer, never what it has done: it carries no counts. Its schema is the loop's
(evidence-network/loop ``steps/03-advertise.md``), exactly those members, and
``scripts/check_commitment.py`` there is the checker. The operator publishes it
by adding the two files :func:`write` lays out under the loop repository's
``nodes/commitments/`` by pull request; nothing here publishes anything.

Both the commitment and its ``disclosure-policy/0`` are signed with the node's
own key (its ``key_id``, the producer key) over their RFC 8785 (JCS) form
without ``sig``. One ``history_card/1`` is stapled, the node book's: one card
per deal book would publish each deal id. The node book's card shows the
book's size, which grows with every record it holds, deal openings included.
"""
from __future__ import annotations

import base64
import hashlib
import json
import re
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path

from agent_action_capsule.canonical import jcs

import goose_node as gn

PROFILE = "evidence-commitment/1"
POLICY_PROFILE = "disclosure-policy/0"
#: The community a Goose node's cards belong to in the loop's list.
COMMUNITY = "goose"
#: DER SubjectPublicKeyInfo prefix of an Ed25519 key (RFC 8410).
ED25519_SPKI = bytes.fromhex("302a300506032b6570032100")
#: The forms the loop's checker requires of the members a caller sets
#: (``scripts/check_commitment.py``: ``HEX64``, ``DURATION``, ``TOKEN``),
#: refused here before anything is signed or written.
HEX64 = re.compile(r"^[0-9a-f]{64}$")
DURATION = re.compile(r"^P(?=\d|T\d)(?:\d+D)?(?:T(?=\d)(?:\d+H)?(?:\d+M)?(?:\d+S)?)?$")
TOKEN = re.compile(r"^[a-z0-9][a-z0-9_./:+-]*$")
#: What the node discloses, and to whom: its node book's card to anyone, a
#: deal's half to that deal's counterparty only (``loop_deal_push``).
POLICY = {"stranger_safe": ["history_card/1"], "share_deal_half": "counterparty"}


@dataclass(frozen=True)
class Settings:
    """How the node is asked. The defaults say what it does today: a request
    reaches its book through the operator, who answers by running a tool, so
    a person approves each answer (``human_gated``)."""

    endpoint: str = f"book:{gn.NODE_BOOK}"
    bindings: tuple[str, ...] = ("evidencebook/respond",)
    subjects: tuple[str, ...] = ("full_history",)
    derivations: tuple[str, ...] = ("history_card/1",)
    predicate_classes: tuple[str, ...] = ("performed:counterparty_half",)
    human_gated: bool = True
    answers_within: str = "PT24H"
    policy: dict = field(default_factory=lambda: dict(POLICY))


def _b64(raw: bytes) -> str:
    return base64.b64encode(raw).decode("ascii")


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def _signed(node: gn.Node, doc: dict) -> dict:
    """``doc`` with ``sig``: Ed25519 by the node key over its JCS form without ``sig``."""
    doc = {k: v for k, v in doc.items() if k != "sig"}
    doc["sig"] = {"alg": "Ed25519", "canon": "jcs", "value": _b64url(node.signer.sign(jcs(doc)))}
    return doc


def _dump(doc: dict) -> bytes:
    return (json.dumps(doc, indent=2, sort_keys=True) + "\n").encode()


def check(node_id: str, s: Settings) -> None:
    """Refuse what the loop's checker would refuse of the members a caller sets."""
    if not HEX64.fullmatch(node_id):
        raise gn.Refused("node_id is the node's id in the loop's list: 64 lowercase hex")
    if not DURATION.fullmatch(s.answers_within):
        raise gn.Refused("answers_within is an ISO 8601 duration in days, hours, minutes or seconds, e.g. PT24H")
    if not s.endpoint.strip():
        raise gn.Refused("the endpoint is not empty")
    for name, values in (("bindings", s.bindings), ("subjects", s.subjects), ("derivations", s.derivations)):
        if not values or not all(TOKEN.fullmatch(v) for v in values):
            raise gn.Refused(f"{name} are lowercase names, e.g. evidencebook/respond")


def build(node: gn.Node, node_id: str = "", settings: Settings | None = None,
          now: datetime | None = None) -> tuple[dict, bytes]:
    """The node's commitment and its policy file's bytes, both signed by the
    node key. ``node_id`` is the node's id in the loop's list (its key id when
    empty)."""
    s = settings or Settings()
    node_id = node_id or node.key_id
    check(node_id, s)
    policy_bytes = _dump(_signed(node, {"profile": POLICY_PROFILE, "key_id": node.key_id, "policy": s.policy}))
    card, statement = node.stapled_card(gn.NODE_BOOK)
    at = (now or datetime.now(timezone.utc)).astimezone(timezone.utc)
    doc = {
        "profile": PROFILE,
        "node_id": node_id,
        "key_id": node.key_id,
        "spki": _b64(ED25519_SPKI + bytes.fromhex(node.key_id)),
        "issued_at": at.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "ask": {"endpoint": s.endpoint, "bindings": list(s.bindings)},
        "answers": {"subjects": list(s.subjects), "derivations": list(s.derivations)},
        "lcap": {"policy_digest": hashlib.sha256(policy_bytes).hexdigest(),
                 "policy_uri": f"policies/{node_id}.json",
                 "predicate_classes": list(s.predicate_classes)},
        "checkpoint": {"sha256": hashlib.sha256(statement).hexdigest(), "bytes": _b64(statement)},
        "human_gated": s.human_gated,
        "answers_within": s.answers_within,
        "history_cards": [{
            "stream": gn.NODE_BOOK,
            "community": COMMUNITY,
            "checkpoint_key": _b64url(ED25519_SPKI + bytes.fromhex(node.checkpoint_key_id)),
            "derivation": "history_card/1",
            "sha256": hashlib.sha256(card).hexdigest(),
            "bytes": _b64(card),
        }],
    }
    return _signed(node, doc), policy_bytes


def write(out_dir: str | Path, doc: dict, policy_bytes: bytes) -> tuple[Path, Path]:
    """Write ``<node_id>.json`` and ``policies/<node_id>.json`` under
    ``out_dir``, laid out as they go under the loop's ``nodes/commitments/``."""
    out = Path(out_dir).expanduser()
    (out / "policies").mkdir(parents=True, exist_ok=True)
    commitment, policy = out / f"{doc['node_id']}.json", out / "policies" / f"{doc['node_id']}.json"
    commitment.write_bytes(_dump(doc))
    policy.write_bytes(policy_bytes)
    return commitment, policy

