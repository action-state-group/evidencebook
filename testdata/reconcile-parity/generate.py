#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Generate the reconcile correlation-parity fixture.

Seals real exchange halves with capsule-emit-mesh's `capsule_sidecar` and runs
its `served_request_join.join_served_request` correlator on each pair, then
writes both the halves and the Python outcome to `fixture.json`. The Go test
`TestReconcileParityWithMeshCorrelator` replays the same halves through
`ReconcileHalves` and must reach the same outcome for every pair.

Usage:
    python generate.py <capsule-emit-mesh checkout> fixture.json

The checkout must contain the request_digest fallback in
served_request_join.py (commit af1e969503d44f66b7598d5b26186e114cf8e760 or
later). Run it with an interpreter that has that repository's dependencies
installed.
"""
from __future__ import annotations

import copy
import json
import subprocess
import sys
import tempfile
from pathlib import Path


def main() -> None:
    checkout = Path(sys.argv[1]).resolve()
    sys.path.insert(0, str(checkout))
    import capsule_sidecar as cs
    import served_request_join as srj
    from agent_action_capsule.canonical import compute_capsule_id

    commit = subprocess.run(["git", "-C", str(checkout), "rev-parse", "HEAD"], capture_output=True, text=True, check=True).stdout.strip()
    tmp = Path(tempfile.mkdtemp())

    def state(role: str, node_id: str):
        manifest = tmp / f"manifest-{node_id}.json"
        manifest.write_text(json.dumps({"model_id": "test-model", "source_model": {"sha256": "a" * 64}}))
        return cs.default_state(
            ledger_dir=tmp / f"ledger-{node_id}",
            manifest_path=manifest,
            keys_dir=tmp / f"keys-{node_id}",
            runtime_label="test-runtime",
            runtime_digest="0" * 64,
            role=role,
            node_id=node_id,
        )

    provider = state(cs.ROLE_PROVIDER, "prov-1")
    requester = state(cs.ROLE_REQUESTER, "req-1")

    def seal(node, exchange_id: str, request_digest: str, twin_bracket_id: str | None = None) -> dict:
        response = {
            "id": exchange_id,
            "object": "chat.completion",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
        }
        eid, source = cs.exchange_id_from_response(response)
        capsule = cs.build_capsule(
            node,
            client_nonce="n" * 32,
            client_nonce_source="client_supplied",
            request_json={"model": "test-model", "messages": [{"role": "user", "content": "hi"}], "temperature": 0.2},
            request_digest=request_digest,
            status="confirmed",
            response_digest=cs.digest_json(response),
            verdict_class="executed",
            disposition_decision="accept",
            latency_ms=1.0,
            exchange_id=eid,
            exchange_id_source=source,
        )
        if twin_bracket_id is not None:
            capsule = copy.deepcopy(capsule)
            poc = capsule["model_attestation"]["compute_attestation"]["x-mesh-poc-v1"]
            poc["serving_provenance"]["twin_bracket_id"] = twin_bracket_id
            capsule["capsule_id"] = compute_capsule_id(capsule)
        return capsule

    cases = [
        # Two nodes, each minting its own exchange_id for one real request.
        ("distinct-exchange-id-shared-request-digest", ("chatcmpl-node-a", "d" * 64, None), ("chatcmpl-node-b", "d" * 64, None)),
        ("shared-exchange-id-shared-request-digest", ("chatcmpl-shared", "a" * 64, None), ("chatcmpl-shared", "a" * 64, None)),
        ("shared-exchange-id-conflicting-request-digest", ("chatcmpl-conflict", "a" * 64, None), ("chatcmpl-conflict", "b" * 64, None)),
        ("no-correlator-agrees", ("chatcmpl-x", "a" * 64, None), ("chatcmpl-y", "b" * 64, None)),
        ("twin-bracket-recorded-when-both-carry", ("chatcmpl-t", "c" * 64, "twin-1"), ("chatcmpl-t", "c" * 64, "twin-1")),
        ("twin-bracket-never-a-join-key", ("chatcmpl-p", "e" * 64, "twin-2"), ("chatcmpl-q", "f" * 64, "twin-2")),
    ]
    pairs = []
    for name, (r_eid, r_digest, r_twin), (p_eid, p_digest, p_twin) in cases:
        requester_half = seal(requester, r_eid, r_digest, r_twin)
        provider_half = seal(provider, p_eid, p_digest, p_twin)
        try:
            joined = srj.join_served_request(provider_half, requester_half, joiner_node_id="req-1")
            block = joined["model_attestation"]["compute_attestation"]["served_request_join"]
            outcome = {"outcome": "joined", "join_key": block["join_key"], "twin_bracket_id": block.get("twin_bracket_id", "")}
        except srj.ConflictingCorrelationError:
            outcome = {"outcome": "conflicting"}
        except srj.ExchangeIdMismatchError:
            outcome = {"outcome": "no_correlation"}
        pairs.append({"name": name, "requester": requester_half, "provider": provider_half, "python": outcome})

    with open(sys.argv[2], "w") as out:
        json.dump(
            {
                "source": {"repository": "action-state-group/capsule-emit-mesh", "commit": commit, "correlator": "served_request_join.join_served_request"},
                "pairs": pairs,
            },
            out,
            indent=1,
            sort_keys=True,
        )
        out.write("\n")


if __name__ == "__main__":
    main()
