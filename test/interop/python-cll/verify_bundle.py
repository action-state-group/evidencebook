# SPDX-License-Identifier: Apache-2.0
"""Verify an evidencebook bundle's checkpoint and memberships with the Python
Checkpointed Local Log reference.

    python verify_bundle.py <bundle.json>

Checks, using only the Python `cll` package:
  1. the checkpoint COSE statement verifies offline (signature and claims);
  2. its decoded log id, MMR size and root equal the bundle's checkpoint and
     completeness certificate;
  3. the interval range proof verifies over the certificate's body digests;
  4. every membership inclusion proof verifies against that root.
"""

import base64
import json
import pathlib
import sys

from cll.checkpoint.core import InclusionProof, RangeProof, verify_inclusion, verify_range
from cll.checkpoint.cose_wire import verify_checkpoint_cose_offline


def fail(message: str) -> None:
    raise SystemExit(f"Python CLL rejected the bundle: {message}")


if len(sys.argv) != 2:
    raise SystemExit("usage: verify_bundle.py <bundle.json>")

bundle = json.loads(pathlib.Path(sys.argv[1]).read_text())
checkpoint = bundle["checkpoint"]
certificate = bundle["completeness_certificate"]
cose = base64.urlsafe_b64decode(checkpoint["cose"] + "=" * (-len(checkpoint["cose"]) % 4))

result = verify_checkpoint_cose_offline(cose)
if not result.ok:
    fail(f"checkpoint statement: {result.errors}")
decoded = result.decoded
if decoded.log_id != certificate["log_id"]:
    fail(f"log id {decoded.log_id!r} != certificate {certificate['log_id']!r}")
if decoded.mmr_size != checkpoint["mmr_size"] or decoded.root != checkpoint["root"] or decoded.root != certificate["range_root"]:
    fail("checkpoint root/size differ from the bundle's claims")

root = bytes.fromhex(decoded.root)
rp = certificate["range_proof"]
range_proof = RangeProof(v=1, kind="range", size=rp["size"], from_index=rp["from_index"], to_index=rp["to_index"], witness=tuple(rp["witness"]))
if rp["size"] != decoded.mmr_size or not verify_range(root, rp["size"], rp["from_index"], rp["to_index"], [bytes.fromhex(d) for d in certificate["body_digests"]], range_proof):
    fail("interval range proof")
memberships = certificate["memberships"]
if len(memberships) != certificate["last_seq"] - certificate["first_seq"] + 1:
    fail("membership count does not cover the interval")
for record_id, member in memberships.items():
    p = member["inclusion_proof"]
    proof = InclusionProof(
        v=p["v"], kind=p["kind"], size=p["size"], leaf_index=p["leaf_index"],
        witness=tuple(p["witness"]), peaks_left=tuple(p["peaks_left"]), peaks_right=tuple(p["peaks_right"]),
    )
    if not verify_inclusion(root, p["size"], member["log_coordinates"]["leaf_index"], bytes.fromhex(record_id), proof):
        fail(f"inclusion proof for {record_id}")

print(f"ok: checkpoint {decoded.root}:{decoded.mmr_size}, the interval range proof and {len(memberships)} memberships verified by Python cll")
