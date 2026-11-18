# SPDX-License-Identifier: Apache-2.0
"""x-deal-v1 differential check: Go and Rust on the same random inputs.

The shared vectors (testdata/deal-v1, dealprofile/x-deal-v1/fixtures) pin the
profile by hand; this check pins that the implementations agree with each
other on inputs nobody wrote by hand, valid and not: each record mutated from
a fixture is accepted by both or refused by both at the same stage (with the
same text at the record and agreement stages), alone or with a seal context, and each
status value and report built from mutated books is the same JCS text, or both
refuse.

    python3 test/interop/deal-v1/diff.py GO_BIN RUST_BIN [--n N] [--seed S]
"""

import argparse
import copy
import json
import random
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
FIXTURES = ROOT / "dealprofile" / "x-deal-v1" / "fixtures"
VECTORS = ROOT / "testdata" / "deal-v1"

STRINGS = ["", "x", "buyer", "seller", "met", "not_met", "cannot_tell", "agreement", "decline", "dispute",
           "withdrawal", "auto:twin", "rail:card", "deal-0123456789abcdef", "2026-11-05T10:00:00Z",
           "2026-02-30T10:00:00Z", "a" * 64, "jcs"]
TIMES = ["2026-11-01T09:00:00Z", "2026-11-04T23:59:59Z", "2026-11-10T12:00:00Z", "2026-11-21T00:00:00Z",
         "2026-11-30T23:59:59Z"]
TYPES = ["propose", "agree", "perform", "settle", "statement", "dispute", "ruling", "ask", "answer", "admit",
         "intent", "check", "verdict", "approval"]


def paths(v, at=()):
    """Every member path of v, objects and arrays."""
    out = [at]
    if isinstance(v, dict):
        for k, e in v.items():
            out += paths(e, at + (k,))
    elif isinstance(v, list):
        for i, e in enumerate(v):
            out += paths(e, at + (i,))
    return out


def get(v, p):
    for k in p:
        v = v[k]
    return v


def mutate(r, v):
    """One random change to a JSON value, in place where it can be."""
    ps = [p for p in paths(v) if p]
    if not ps:
        return v
    p = r.choice(ps)
    parent, key = get(v, p[:-1]), p[-1]
    old = parent[key]
    op = r.random()
    if op < 0.2 and isinstance(parent, dict):
        del parent[key]
    elif op < 0.2 and isinstance(parent, list):
        parent.pop(key)
    elif op < 0.35:
        parent[key] = None
    elif isinstance(old, str):
        parent[key] = r.choice(STRINGS + TIMES + TYPES)
    elif isinstance(old, bool):
        parent[key] = not old
    elif isinstance(old, int):
        parent[key] = r.choice([0, 1, 2, -1, old + 1, old - 1])
    elif isinstance(old, list) and old:
        parent[key] = old + [copy.deepcopy(r.choice(old))]
    else:
        parent[key] = r.choice(STRINGS)
    return v


def fixtures():
    out = []
    for f in sorted(FIXTURES.rglob("*.json")):
        d = json.loads(f.read_text())
        if isinstance(d, dict) and "record" in d:
            out.append(d["record"])
    return out


def cases(name):
    return json.loads((VECTORS / name).read_text())["cases"]


def mutate_books(r, books):
    """A change to status books: a book cut short, a record's time changed,
    a proof flag flipped, or one change inside a record."""
    op = r.random()
    lists = [k for k in ("own", "counterpart", "case_book") if books.get(k)]
    if op < 0.25 and lists:
        k = r.choice(lists)
        books[k] = books[k][: r.randint(0 if k != "own" else 1, len(books[k]))]
    elif op < 0.5 and lists:
        k = r.choice(lists)
        rec = r.choice(books[k])
        rec["time"] = r.choice([None, {"stamp": r.randint(1, 4)}] + TIMES)
    elif op < 0.6:
        books["proof_held"] = not books.get("proof_held")
    elif lists:
        # A change inside a record that keeps it a valid record: a status
        # value and a report read records already checked, so a record the
        # schema refuses is no input to them (each implementation's reading
        # of one is its own).
        k = r.choice(lists)
        rec = r.choice(books[k])["record"]
        items = [it for it in rec["body"].get("statements") or [] if isinstance(it, dict)]
        op = r.random()
        if items and op < 0.6:
            r.choice(items)["value"] = r.choice(["met", "not_met", "cannot_tell"])
        elif "role" in rec["body"] and op < 0.75:
            # A role neither side has: each reads the books Go's role map gives.
            rec["body"]["role"] = r.choice(["buyer", "seller", "lender"])
        else:
            rec["x-deal-v1"]["at"] = r.choice(TIMES)
    return books


def requests(r, n):
    recs, status, report = fixtures(), cases("status/vectors.json"), cases("report/vectors.json")
    out = []
    for _ in range(n):
        kind = r.random()
        if kind < 0.5:
            rec = copy.deepcopy(r.choice(recs))
            for _ in range(r.randint(0, 3)):
                rec = mutate(r, rec)
            if r.random() < 0.3:
                ctx = {"offer_exists": r.random() < 0.5, "admission": r.random() < 0.5}
                out.append({"kind": "sealed", "record": json.dumps(rec), "context": ctx})
            else:
                out.append({"kind": "check", "record": json.dumps(rec)})
        elif kind < 0.75:
            books = copy.deepcopy(r.choice(status)["books"])
            for _ in range(r.randint(0, 2)):
                mutate_books(r, books)
            out.append({"kind": "status", "books": books})
        else:
            c = copy.deepcopy(r.choice(report))
            books = c["books"]
            for _ in range(r.randint(0, 2)):
                mutate_books(r, books["Books"])
            cp = c["copy"]
            if r.random() < 0.3:
                cp["as_of"] = r.choice(TIMES)
            if r.random() < 0.1:
                cp["audience"] = r.choice(["keep", "counterparty", "adjudicator", "other"])
                cp["referee"] = "Lamp Court" if cp["audience"] == "adjudicator" else ""
            out.append({"kind": "report", "books": books, "copy": cp})
    return out


def run(binary, lines):
    p = subprocess.run([binary], input="\n".join(lines) + "\n", capture_output=True, text=True, check=True)
    return p.stdout.splitlines()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("go")
    ap.add_argument("rust")
    ap.add_argument("--n", type=int, default=2000)
    ap.add_argument("--seed", type=int, default=1)
    a = ap.parse_args()
    reqs = requests(random.Random(a.seed), a.n)
    lines = [json.dumps(q) for q in reqs]
    go, rs = run(a.go, lines), run(a.rust, lines)
    if len(go) != len(lines) or len(rs) != len(lines):
        sys.exit(f"answer counts differ: {len(lines)} asked, Go {len(go)}, Rust {len(rs)}")
    bad = [(i, reqs[i]["kind"], g, s) for i, (g, s) in enumerate(zip(go, rs)) if g != s]
    kinds = {}
    for q, g in zip(reqs, go):
        k = (q["kind"], g.startswith("{\"error\"") or g.startswith('"') and g != '"ok"')
        kinds[k] = kinds.get(k, 0) + 1
    print(f"seed {a.seed}: {len(lines)} inputs, {len(bad)} disagreements; by kind and refused: {kinds}")
    for i, kind, g, s in bad[:5]:
        print(f"  input {i} ({kind}):\n    request {lines[i][:600]}\n    go   {g[:300]}\n    rust {s[:300]}")
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
