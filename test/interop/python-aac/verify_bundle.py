# SPDX-License-Identifier: Apache-2.0
"""Verify an evidencebook bundle with the Python Agent Action Capsule bundle
verifier, the reference the Go verifier this module uses is a port of.

    python verify_bundle.py <bundle.json>

Requires, using only the Python `agent_action_capsule` package:
  1. graph closure, interval coverage and per-record membership all pass,
     with no finding (a "checkpoint_unverified" finding would mean the
     interval was checked against an unauthenticated checkpoint);
  2. every record's capsule verifies;
  3. the same bundle with one record changed fails.
"""

import copy
import json
import pathlib
import sys

from agent_action_capsule.bundle import verify_bundle


def fail(message: str) -> None:
    raise SystemExit(f"Python AAC rejected the bundle: {message}")


if len(sys.argv) != 2:
    raise SystemExit("usage: verify_bundle.py <bundle.json>")

bundle = json.loads(pathlib.Path(sys.argv[1]).read_text())
if bundle.get("bundle_kind") != "evidence-bundle/v2":
    fail(f"bundle_kind {bundle.get('bundle_kind')!r}")

result = verify_bundle(bundle)
for name in ("graph_closure", "interval_coverage", "per_record_membership"):
    claim = getattr(result, name)
    if claim.status != "pass" or claim.findings:
        fail(f"{name}: {claim.status} {list(claim.findings)}")
for capsule_id, outcome in result.capsule_results.items():
    if not outcome.ok:
        fail(f"record {capsule_id}: {outcome}")
if len(result.capsule_results) != len(bundle["records"]):
    fail("not every record was checked")

tampered = copy.deepcopy(bundle)
record = tampered["records"][0]
record["action_type"] = (record.get("action_type") or "") + "x"
changed = verify_bundle(tampered)
if all(getattr(changed, n).status == "pass" for n in ("graph_closure", "interval_coverage", "per_record_membership")) and all(
    o.ok for o in changed.capsule_results.values()
):
    fail("a changed record still verifies")

print(f"Python AAC verified the bundle: {len(bundle['records'])} records, all three claims pass; a changed record fails")
