# SPDX-License-Identifier: Apache-2.0
"""Verify an evidencebook refusal with capsule_emit's offline refusal verifier.

    python verify_refusal.py <refusal.json>
"""

import json
import pathlib
import sys

from capsule_emit.evidence_request import Refusal, verify_refusal_offline

if len(sys.argv) != 2:
    raise SystemExit("usage: verify_refusal.py <refusal.json>")
refusal = Refusal(**json.loads(pathlib.Path(sys.argv[1]).read_text()))
if not verify_refusal_offline(refusal):
    raise SystemExit("capsule_emit rejected the refusal")
tampered = Refusal(**{**refusal.to_dict(), "reason": "policy_declined"})
if verify_refusal_offline(tampered):
    raise SystemExit("capsule_emit accepted a tampered refusal; the check is not biting")
print(f"ok: refusal {refusal.reason} verified by capsule_emit")
