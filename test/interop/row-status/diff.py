# SPDX-License-Identifier: Apache-2.0
"""Row status differential check: Go, Rust and Python on the same random inputs.

The shared vectors (testdata/row-status) pin the rule by hand; this check pins
that the three readers agree with each other on inputs nobody wrote by hand,
valid and not: the same result, or all three refuse.

    python3 test/interop/row-status/diff.py GO_BIN RUST_BIN [--n N] [--seed S]
"""

import argparse
import json
import random
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "python"))
from evidencebook import row_status  # noqa: E402

PARTIES = ["buyer", "seller"]
VALUES = ["met", "not_met", "cannot_tell"]


def t(r, day_from=1, day_to=8):
    return f"2026-11-{r.randint(day_from, day_to):02d}T{r.randint(0, 23):02d}:{r.choice([0, 30, 59]):02d}:{r.choice([0, 59]):02d}Z"


def maybe_time(r):
    return None if r.random() < 0.15 else t(r)


def date(r, named):
    if r.random() < 0.5 or not named:
        return {"at": t(r, 1, 5)}
    return {"after": r.choice(list(named)), "seconds": r.choice([0, 3600, 86400, 4 * 86400, 7 * 86400])}


def gen(r):
    named = {}
    for name in r.sample(["pay", "rcpt"], r.randint(0, 2)):
        named[name] = {"time": maybe_time(r), "latest": t(r, 3, 8)}
    row = {"id": "r", "owed_by": r.choice(PARTIES), "due": date(r, named), "window_end": date(r, named)}
    inp = {"at": t(r, 1, 12) if r.random() < 0.9 else t(r, 20, 28), "parties": PARTIES, "row": row}
    if named:
        inp["named"] = named
    if r.random() < 0.3:
        row["recomputed"] = True
        if r.random() < 0.7:
            inp["check"] = r.choice(["pass", "fail"])
    if r.random() < 0.2:
        inp["no_concession"] = True
    inp["statements"] = [{"side": r.choice(PARTIES), "value": r.choice(VALUES), "time": maybe_time(r)} for _ in range(r.randint(0, 5))]
    if r.random() < 0.4:
        inp["disputes"] = [{"side": r.choice(PARTIES), "time": maybe_time(r), "seen_in_time": r.random() < 0.5} for _ in range(r.randint(1, 2))]
    if r.random() < 0.2:
        inp["withdrawals"] = [{"side": r.choice(PARTIES), "time": maybe_time(r)}]
    if r.random() < 0.3:
        rulings = []
        for _ in range(r.randint(1, 2)):
            ru = {"final": r.random() < 0.6, "value": r.choice(["met", "not_met"]), "time": maybe_time(r)}
            if r.random() < 0.5:
                ru["remedy_due"] = t(r, 5, 12)
            rulings.append(ru)
        inp["rulings"] = rulings
        if r.random() < 0.1:
            inp["conflicting_final_rulings"] = True
    if r.random() < 0.2:
        inp["remedies"] = [{"time": maybe_time(r)}]
    text = json.dumps(inp)
    if r.random() < 0.15:
        text = mutate(r, text)
    return text


def mutate(r, text):
    """A small change that often makes the input one the readers must refuse alike."""
    choices = [
        lambda s: s.replace('"at"', '"At"', 1),
        lambda s: s.replace("Z\"", "+00:00\"", 1),
        lambda s: s.replace('"buyer"', '"carol"', 1),
        lambda s: s.replace('"met"', '"yes"', 1),
        lambda s: s.replace("true", '"true"', 1),
        lambda s: s.replace('"seconds": 0', '"seconds": -1', 1),
        lambda s: s.replace('"seconds": 3600', '"seconds": 3600.5', 1),
        lambda s: s[:-1] + ', "x": 1}',
        lambda s: s + "]",
        lambda s: s.replace('"id": "r"', '"id": "\\ud800"', 1),
        lambda s: s.replace('"id": "r"', '"id": "r", "id": "q"', 1),
        lambda s: s.replace('"named": {', '"named": {"\\ud800": {"latest": "2026-11-05T00:00:00Z"}, ', 1),
        lambda s: s.replace('"parties": ["buyer", "seller"]', '"parties": ["buyer", "buyer"]', 1),
        lambda s: s.replace(":00Z", ":60Z", 1),
        lambda s: s.replace("2026-11-0", "2026-02-3", 1),
        lambda s: s.replace('"statements": [', '"statements": null, "s": [', 1),
    ]
    return r.choice(choices)(text)


def python_line(text):
    try:
        return row_status.compute(row_status.parse_input(text)).to_json()
    except row_status.Invalid:
        return {"error": True}


def run(binary, inputs):
    p = subprocess.run([binary], input="\n".join(inputs) + "\n", capture_output=True, text=True, check=True)
    return [json.loads(x) for x in p.stdout.splitlines()]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("go")
    ap.add_argument("rust")
    ap.add_argument("--n", type=int, default=20000)
    ap.add_argument("--seed", type=int, default=461)
    a = ap.parse_args()
    r = random.Random(a.seed)
    inputs = [gen(r) for _ in range(a.n)]
    go, rs = run(a.go, inputs), run(a.rust, inputs)
    if len(go) != a.n or len(rs) != a.n:
        sys.exit(f"expected {a.n} lines, got Go {len(go)}, Rust {len(rs)}")
    bad = 0
    for i, text in enumerate(inputs):
        py = python_line(text)
        if not (go[i] == rs[i] == py):
            bad += 1
            if bad <= 10:
                print(f"input: {text}\n  go:   {go[i]}\n  rust: {rs[i]}\n  py:   {py}")
    refused = sum(1 for x in go if x.get("error"))
    statuses = sorted({x["status"] for x in go if "status" in x})
    print(f"{a.n} inputs (seed {a.seed}), {refused} refused by all, statuses seen: {', '.join(statuses)}; {bad} differ")
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
