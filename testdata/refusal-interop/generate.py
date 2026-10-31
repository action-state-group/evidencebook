#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Sign a refusal with agent-action-capsule's -00 evidence_request and write it to refusal.json.

    python generate.py refusal.json

TestRefusalSignedByCapsuleEmitVerifies checks it with the Go Refusal.Verify.
The key is generated fresh and discarded; only the signed refusal is kept.
"""

import json
import sys
import tempfile
from pathlib import Path

from agent_action_capsule.evidence_request import sign_refusal, verify_refusal_offline
from capsule_emit.signing import LocalKeypairSigner

key_path = Path(tempfile.mkdtemp()) / "key.pem"
refusal = sign_refusal(
    "ab" * 32, "no_such_subject", signer=LocalKeypairSigner(key_path), issued_at="2026-09-26T12:00:00Z"
)
assert verify_refusal_offline(refusal)
Path(sys.argv[1]).write_text(json.dumps(refusal.to_dict(), indent=1, sort_keys=True) + "\n")
