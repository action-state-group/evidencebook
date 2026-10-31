# SPDX-License-Identifier: Apache-2.0
"""Verify an evidencebook refusal with agent-action-capsule's offline -00 refusal verifier.

    python verify_refusal.py <refusal.json>
"""

import json
import pathlib
import sys

from agent_action_capsule.evidence_request import Refusal, verify_refusal_offline

if len(sys.argv) != 2:
    raise SystemExit("usage: verify_refusal.py <refusal.json>")
refusal = Refusal(**json.loads(pathlib.Path(sys.argv[1]).read_text()))
if not verify_refusal_offline(refusal):
    raise SystemExit("agent_action_capsule rejected the refusal")
tampered = Refusal(**{**refusal.to_dict(), "reason": "policy_declined"})
if verify_refusal_offline(tampered):
    raise SystemExit("agent_action_capsule accepted a tampered refusal; the check is not biting")
print(f"ok: refusal {refusal.reason} verified by agent_action_capsule")
