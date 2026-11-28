# SPDX-License-Identifier: Apache-2.0
"""The Python side of the subject bundle differential check
(test/interop/subject/diff.py, evidencebook#1013): one JSON request per line
on stdin, {"kind": "verify", "bundle": <the bundle as text>}, and one line out
for each: the JCS of what verify_subject_bundle makes of it (main.go's
Verdict), or {"valid":false} when it is refused."""

import json
import sys

from agent_action_capsule.canonical import jcs

from evidencebook.errors import InvalidError
from evidencebook.subject import subjects_of_record
from evidencebook.subject_bundle import verify_subject_bundle


def verdict(data: bytes) -> dict:
    try:
        got = verify_subject_bundle(data)
    except InvalidError:
        return {"valid": False}
    subjects_of = {}
    if got.bundle is not None:
        for r in got.bundle.records:
            ids = subjects_of_record(got.bundle.records, r.record_id)
            if ids:
                subjects_of[r.record_id] = ids
    return {
        "valid": True, "form": got.form, "level": got.level, "subject_kind": got.subject.subject_kind,
        "subject_id": got.subject.subject_id, "membership": got.subject.membership, "root": got.root,
        "members": list(got.members), "member_count": got.member_count, "close": got.close, "closed": got.closed,
        "held_by_digest": list(got.held_by_digest),
        "withheld": sorted((f"{record_id} {digest}" for record_id, digest in got.withheld), key=lambda s: s.encode()),
        "subjects_of": subjects_of,
    }


def main() -> None:
    for line in sys.stdin.buffer:
        request = json.loads(line)
        if request.get("kind") != "verify":
            sys.exit(f"python_side: unknown kind {request.get('kind')!r}")
        sys.stdout.write(jcs(verdict(request["bundle"].encode("utf-8"))).decode("utf-8") + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
