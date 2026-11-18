# SPDX-License-Identifier: Apache-2.0
"""The Python side of the x-deal-v1 differential check (diff.py): one JSON
request per line on stdin, and one answer line for each, as the Go side
(main.go) answers. It answers the kinds the Python package implements
(check, sealed, against) and "skip" for any other.

    python3 test/interop/deal-v1/python_side.py < requests
"""

import json
import sys

from agent_action_capsule.canonical import jcs

import evidencebook.deal.v1 as v1

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
    return '"skip"'


def main():
    out = sys.stdout
    for line in sys.stdin:
        out.write(answer(json.loads(line)) + "\n")


if __name__ == "__main__":
    main()
