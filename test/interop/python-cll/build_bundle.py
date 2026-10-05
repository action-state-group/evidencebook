# SPDX-License-Identifier: Apache-2.0
"""Build an Evidence Bundle v2 in Python, for evidencebook (Go) to verify.

    python build_bundle.py <bundle.json> <tampered.json>

A test-only producer, independent of this module: the records are sealed by
the Python Agent Action Capsule library (`agent_action_capsule.emit`), and
the log, the signed checkpoint (COSE), the interval range proof and every
record's inclusion proof are built by the Python Checkpointed Local Log
(`cll`). The checkpoint is signed with an Ed25519 key through
`capsule_emit.signing.LocalKeypairSigner`, used only as a signer. CI runs
`go run ./test/interop/verify` on both outputs: the bundle must fully
verify, and the tampered copy (one record changed) must not.
"""

import base64
import copy
import json
import pathlib
import sys
import tempfile
from types import SimpleNamespace

import agent_action_capsule
from capsule_emit.signing import LocalKeypairSigner
from cll.checkpoint import MmrLedger, checkpoint_to_cose, emit_checkpoint

LOG_ID = "interop/python-producer"


class _Log:
    """The minimal log the MMR index reads: sequential appends."""

    def __init__(self) -> None:
        self.n = 0

    def append(self, capsule: dict, *, consequential: bool = True):  # noqa: ARG002
        self.n += 1
        return SimpleNamespace(seq=self.n, capsule_id=capsule["capsule_id"])

    def scan(self, *a, **k):  # noqa: ARG002
        return []


class _CheckpointSigner:
    """The checkpoint's JSON signature: a hex digest in, a hex signature out."""

    def __init__(self, inner: LocalKeypairSigner) -> None:
        self._inner = inner
        self.key_id = inner.key_id

    def sign(self, digest_hex: str) -> str:
        return self._inner.sign(digest_hex.encode("ascii"))[0]


def _proof_json(proof) -> dict:
    return {k: list(v) if isinstance(v, tuple) else v for k, v in proof.__dict__.items()}


def build() -> dict:
    first = agent_action_capsule.emit("interop.first", "fyi", "interop-operator", "python-producer", timestamp="2026-10-05T00:00:00Z")
    second = agent_action_capsule.emit("interop.second", "fyi", "interop-operator", "python-producer", timestamp="2026-10-05T00:00:01Z")
    third = agent_action_capsule.emit(
        "interop.third", "fyi", "interop-operator", "python-producer", timestamp="2026-10-05T00:00:02Z",
        prior_capsule_id=second["capsule_id"], chain_relation="follows",
    )
    records = [first, second, third]

    signer = LocalKeypairSigner(pathlib.Path(tempfile.mkdtemp()) / "checkpoint-key.pem")
    mmr = MmrLedger(_Log())
    for record in records:
        mmr.append({"capsule_id": record["capsule_id"]})
    cp = emit_checkpoint(mmr, _CheckpointSigner(signer), log_id=LOG_ID, timestamp="2026-10-05T00:00:03Z")
    cose = checkpoint_to_cose(cp, signer, mmr.peak_hashes_at(cp.mmr_size))

    n = len(records)
    rp = mmr.range_proof(1, n)
    range_proof = _proof_json(rp)
    range_proof.update(from_seq=1, to_seq=n)

    return {
        "bundle_version": "2",
        "bundle_kind": "evidence-bundle/v2",
        "root": third["capsule_id"],
        "records": records,
        "completeness": {
            "closure_depth": 1,
            "missing": [],
            "payloads_mode": "none",
            "records_mode": "complete",
            "suppressed_fields": [],
        },
        "completeness_certificate": {
            "log_id": LOG_ID,
            "range_root": cp.root,
            "first_seq": 1,
            "last_seq": n,
            "body_digests": [r["capsule_id"] for r in records],
            "range_proof": range_proof,
            "memberships": {
                r["capsule_id"]: {
                    "log_coordinates": {"log_id": LOG_ID, "seq": seq, "leaf_index": seq - 1},
                    "inclusion_proof": _proof_json(mmr.inclusion_proof(seq, size=cp.mmr_size)),
                }
                for seq, r in enumerate(records, 1)
            },
        },
        "checkpoint": {
            "log_id": cp.log_id,
            "mmr_size": cp.mmr_size,
            "root": cp.root,
            "cose": base64.urlsafe_b64encode(cose).rstrip(b"=").decode(),
        },
    }


if len(sys.argv) != 3:
    raise SystemExit("usage: build_bundle.py <bundle.json> <tampered.json>")
bundle = build()
pathlib.Path(sys.argv[1]).write_text(json.dumps(bundle))
tampered = copy.deepcopy(bundle)
tampered["records"][1]["action_id"] = "interop.changed"
pathlib.Path(sys.argv[2]).write_text(json.dumps(tampered))
print(f"wrote a {len(bundle['records'])}-record bundle and a tampered copy")
