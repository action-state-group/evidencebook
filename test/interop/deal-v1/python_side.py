# SPDX-License-Identifier: Apache-2.0
"""The Python side of the x-deal-v1 differential check (diff.py): one JSON
request per line on stdin, and one answer line for each, as the Go side
(main.go) answers. It answers the kinds the Python package implements
(check, sealed, against, status, report, verify_report, verify_report_bundle) and "skip" for
any other.

    python3 test/interop/deal-v1/python_side.py < requests
"""

import hashlib
import json
import sys
from datetime import datetime, timezone

from agent_action_capsule.canonical import jcs

import evidencebook.deal.v1 as v1
from evidencebook.deal.v1 import report as v1_report
from evidencebook.deal.v1 import status as v1_status
from evidencebook._capsule import seal_payload
from evidencebook.deal.names import REPORT_ACTION_ID
from evidencebook.signer import Ed25519Signer

# The record key of the seller in dealprofile/xdealv1's receive tests, which
# signed the first record of testdata/deal-v1/report-verify.
SELLER = Ed25519Signer(hashlib.sha256(b"xdealv1 receive tests\x00seller record key").digest())

COMPARED = {v1.STAGE_RECORD, v1.STAGE_AGREEMENT}


def refusal(e):
    if e.stage in COMPARED:
        return jcs([e.stage, e.detail]).decode()
    return json.dumps(e.stage)


def answer(req):
    kind = req["kind"]
    if kind in ("check", "sealed"):
        try:
            if kind == "sealed" and req.get("context") is not None:
                ctx = req["context"]
                v1.check_sealed(req["record"].encode(), v1.SealContext(ctx.get("offer_exists", False),
                                                                      ctx.get("admission", False)))
            else:
                v1.check(req["record"].encode())
        except v1.StageError as e:
            return refusal(e)
        return '"ok"'
    if kind == "against":
        records = []
        for text in (req["record"], req["with"]):
            try:
                records.append(v1.decode(text.encode()))
            except (v1.GoTypeError, v1.StageError):
                try:
                    v1.check(text.encode())
                except v1.StageError as e:
                    return json.dumps(e.stage)
                return '{"error":true}'
        r, w = records
        try:
            {"agreement": lambda: v1.check_agreement(r, w, req["held"]),
             "dispute": lambda: v1.check_dispute(r, w),
             "ruling": lambda: v1.check_ruling(r, w)}[req["check"]]()
        except v1.StageError as e:
            return refusal(e)
        return '"ok"'
    if kind == "status":
        try:
            return jcs(v1_status.build_status(req["books"])).decode()
        except ValueError:
            return '{"error":true}'
    if kind == "report":
        if not isinstance(req.get("books"), dict) or req.get("copy") is None:
            return '{"error":true}'
        try:
            return jcs(v1_report.build_report(req["books"], req["copy"])).decode()
        except ValueError:
            return '{"error":true}'
    if kind == "verify_report":
        return verify_report(req["payload"], req["first"])
    if kind == "verify_report_bundle":
        return verify_report_bundle(req["report"], req["extension"])
    return '"skip"'


def verify_report(payload, first):
    """Seals payload as a report Capsule under the seller's key, as main.go
    does, and reads it against first."""
    try:
        payload = jcs(payload)
    except Exception:  # noqa: BLE001 - JCS refuses it, before sealing
        return '{"error":true}'
    _, capsule, envelope = seal_payload(REPORT_ACTION_ID, payload, {}, operator="seller", developer="xdealv1-test",
                                        at=datetime(2026, 11, 21, 12, 0, 0, tzinfo=timezone.utc), signer=SELLER)
    try:
        r = v1.verify_report(v1.report_pair(capsule, envelope, payload), bytes.fromhex(first))
    except v1.StageError as e:
        return jcs({"stage": e.stage}).decode()
    return jcs({"key_id": r.key_id, "report": r.report}).decode()


def verify_report_bundle(report, ext):
    """Reads the sealed report pair (hex) with the extension's text, as main.go does."""
    try:
        r = v1.verify_report_bundle(bytes.fromhex(report), ext.encode("utf-8", "surrogateescape"))
    except v1.StageError as e:
        return jcs({"stage": e.stage}).decode()
    return jcs({"key_id": r.key_id, "report": r.report}).decode()


def main():
    out = sys.stdout
    for line in sys.stdin:
        out.write(answer(json.loads(line)) + "\n")


if __name__ == "__main__":
    main()
