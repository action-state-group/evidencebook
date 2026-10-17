# SPDX-License-Identifier: Apache-2.0
"""Count the agent-action-capsule History vector test's results per case class.

Usage: count_by_class.py <junit.xml> <vectors.json>

The test (python/tests/test_personal_history_basic_v0_vectors.py in
agent-action-capsule) runs some checks once per case, as test_x[<case id>],
and the rest over the whole corpus. Each per-case result is counted under its
case's source class: ``responder`` (the run's octets, unchanged) or
``derived`` (changed from the run). test_refusal_to_another_requester checks
the one refusal case, a responder case. Everything else is ``corpus``.

Prints pass/skip/fail per class and exits non-zero if anything failed or
errored, or if no per-case test ran for a class.
"""
from __future__ import annotations

import json
import re
import sys
import xml.etree.ElementTree as ET


def main() -> int:
    junit, vectors = sys.argv[1:3]
    cases = json.load(open(vectors, encoding="utf-8"))["cases"]
    source = {c["id"]: ("responder" if c["source"] == "responder" else "derived") for c in cases}
    refusal = [c["id"] for c in cases if "definition" not in c["inputs"]]
    counts: dict[str, dict[str, int]] = {k: {"pass": 0, "skip": 0, "fail": 0} for k in ("responder", "derived", "corpus")}
    for tc in ET.parse(junit).getroot().iter("testcase"):
        name = tc.get("name", "")
        m = re.fullmatch(r"\w+\[(.+)\]", name)
        if m:
            cls = source[m.group(1)]
        elif name == "test_refusal_to_another_requester" and len(refusal) == 1:
            cls = source[refusal[0]]
        else:
            cls = "corpus"
        if tc.find("failure") is not None or tc.find("error") is not None:
            outcome = "fail"
        elif tc.find("skipped") is not None:
            outcome = "skip"
        else:
            outcome = "pass"
        counts[cls][outcome] += 1
    cases_per_class = {k: sum(1 for v in source.values() if v == k) for k in ("responder", "derived")}
    for cls, c in counts.items():
        n = f" ({cases_per_class[cls]} cases)" if cls in cases_per_class else ""
        print(f"{cls}{n}: {c['pass']} passed, {c['skip']} skipped, {c['fail']} failed")
    failed = sum(c["fail"] for c in counts.values())
    empty = [k for k in ("responder", "derived") if sum(counts[k].values()) == 0]
    if empty:
        print(f"no per-case test ran for: {', '.join(empty)}")
    return 1 if failed or empty else 0


if __name__ == "__main__":
    sys.exit(main())
