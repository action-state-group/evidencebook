#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Sign a refusal with capsule_emit and write it to refusal.json.

    python generate.py refusal.json

TestRefusalSignedByCapsuleEmitVerifies checks it with the Go Refusal.Verify.
The key is generated fresh and discarded; only the signed refusal is kept.
"""

import json
import sys
import tempfile
from pathlib import Path

from capsule_emit.evidence_request import Refusal, verify_refusal_offline
from capsule_emit.signing import LocalKeypairSigner

key_path = Path(tempfile.mkdtemp()) / "key.pem"
signer = LocalKeypairSigner(key_path)
stub = Refusal(request_digest="ab" * 32, reason="no_such_subject", issued_at="2026-09-26T12:00:00Z", key_id="", sig="")
sig, key_id = signer.sign(stub.signing_body())
refusal = Refusal(request_digest=stub.request_digest, reason=stub.reason, issued_at=stub.issued_at, key_id=key_id, sig=sig)
assert verify_refusal_offline(refusal)
Path(sys.argv[1]).write_text(json.dumps(refusal.to_dict(), indent=1, sort_keys=True) + "\n")
