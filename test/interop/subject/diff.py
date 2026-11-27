# SPDX-License-Identifier: Apache-2.0
"""Subject bundle differential check: Go, Rust and Python on the same random inputs.

The shared vectors (testdata/subject) pin subject_bundle/0 by hand; this check
pins that the implementations agree with each other on inputs nobody wrote by
hand, valid and not (evidencebook#1012, #1013). Each input is a bundle of
testdata/subject, unchanged or mutated: the subject extension's members, close,
level, closure depth, held digests and key range proof edited, records dropped
or reordered, disclosed headers removed, a projection's members edited, or any
member of the bundle replaced. A signed record cannot be changed without
failing the Evidence Bundle checks first, so most mutations reach the subject
rules through the extension, which the bundle's signatures do not cover.

Every side answers each input with the same JCS text: what its verifier makes
of the bundle (test/interop/subject/main.go's Verdict), or {"valid":false}.

    python3 test/interop/subject/diff.py GO_BIN [--rust RUST_BIN] [--python CMD] [--n N] [--seed S]

With --rust, RUST_BIN is the evidencebook-interop bin `subject`. With
--python, CMD is a Python interpreter with the package installed, which runs
python_side.py next to this file. At least one of them is required; the
check fails on any disagreement with Go.
"""

import argparse
import copy
import json
import random
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
VECTORS = ROOT / "testdata" / "subject"
EXT = "evidencebook/subject"
LEVELS = ["holder_commitment", "log_prefix", "holder_assertion", "", "full"]
STRINGS = ["", "x", "exchange-1", "deal-1", "log", "field:/correlation/exchange_id", "field:/subject_ref",
           "field:x", "full", "projection", "subject_bundle/0", "a" * 64, "0" * 64]


def bundles():
    """Every bundle file of the vectors, parsed, by name."""
    manifest = json.loads((VECTORS / "vectors.json").read_text())
    return {v["file"]: json.loads((VECTORS / v["file"]).read_text()) for v in manifest["vectors"]}


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


def put(v, p, value):
    get(v, p[:-1])[p[-1]] = value


def record_ids(b):
    return [r.get("capsule_id") for r in b.get("records", []) if isinstance(r, dict)]


def value_for(rng, b):
    ids = record_ids(b) or ["0" * 64]
    return rng.choice([None, True, False, 0, 1, 2, -1, 2 ** 53 - 1, 2 ** 64, "", [], {},
                       rng.choice(STRINGS), rng.choice(ids), [rng.choice(ids)]])


def mutate_extension(rng, b):
    ext = b.get("extensions", {}).get(EXT)
    if not isinstance(ext, dict):
        return
    ids = record_ids(b)
    members = ext.get("members") if isinstance(ext.get("members"), list) else []
    op = rng.randrange(14)
    if op == 0 and members:
        members.pop(rng.randrange(len(members)))
    elif op == 1 and len(members) > 1:
        i, j = rng.sample(range(len(members)), 2)
        members[i], members[j] = members[j], members[i]
    elif op == 2 and ids:
        members.insert(rng.randrange(len(members) + 1), rng.choice(ids))
    elif op == 3:
        ext["close"] = rng.choice(["", *ids]) if ids else ""
    elif op == 4:
        ext["closed"] = not ext.get("closed")
    elif op == 5:
        ext["level"] = rng.choice(LEVELS)
    elif op == 6:
        ext["closure_depth"] = rng.choice([0, 1, 2, 3, 2 ** 53 - 1, -1, "1"])
    elif op == 7:
        held = ext.get("held_by_digest") if isinstance(ext.get("held_by_digest"), list) else []
        if held and rng.random() < 0.5:
            held.pop()
        else:
            held.append(rng.choice(ids + ["0" * 64]) if ids else "0" * 64)
        ext["held_by_digest"] = sorted(held) if rng.random() < 0.8 else held
    elif op == 8:
        subject = ext.get("subject")
        if isinstance(subject, dict):
            subject[rng.choice(["subject_kind", "subject_id", "membership"])] = rng.choice(STRINGS)
    elif op == 9:
        ext["root"] = rng.choice(ids) if ids else ""
    elif op == 10 and isinstance(ext.get("key_range"), dict):
        kr = ext["key_range"]
        p = rng.choice(paths(kr)[1:] or [("root_record_id",)])
        if p:
            put(kr, p, value_for(rng, b))
    elif op == 11:
        ext.pop(rng.choice(list(ext)), None)
    elif op == 12:
        ext[rng.choice(["extra", "Members", "LEVEL"])] = value_for(rng, b)
    else:
        ext["form"], ext["derivation"] = rng.choice([("full", "subject_bundle/0"), ("projection", "subject_bundle/0"),
                                                     ("full", "subject_bundle/1")])


def mutate_records(rng, b):
    records = b.get("records")
    if not isinstance(records, list) or not records:
        return
    op = rng.randrange(4)
    if op == 0:
        records.pop(rng.randrange(len(records)))
    elif op == 1:
        rng.shuffle(records)
    elif op == 2:
        disclosures = b.get("disclosures")
        if isinstance(disclosures, dict) and disclosures:
            disclosures.pop(rng.choice(sorted(disclosures)))
    else:
        r = rng.choice(records)
        if isinstance(r, dict) and r:
            r.pop(rng.choice(sorted(r)), None)


def mutate_projection(rng, b):
    op = rng.randrange(8)
    members = b.get("members") if isinstance(b.get("members"), list) else None
    if op == 0 and members:
        members.pop(rng.randrange(len(members)))
    elif op == 1 and members:
        rng.choice(members)["seq"] = rng.choice([1, "1", None])
    elif op == 2:
        b["root"] = rng.choice([None, {}, {"seq": 1}, {"record_type": "subject_root"}])
    elif op == 3:
        b["members"] = rng.choice([None, [], [{}], "x"])
    elif op == 4:
        b["completeness"] = rng.choice(["holder_assertion", "holder_commitment", "", None])
    elif op == 5:
        b[rng.choice(["subject_id", "subject_kind", "definition"])] = rng.choice(STRINGS + [None, 1])
    elif op == 6:
        b[rng.choice(["extra", "Members", "SUBJECT_ID"])] = value_for(rng, b)
    else:
        b.pop(rng.choice(sorted(b)), None)


def mutate_anywhere(rng, b):
    ps = paths(b)[1:]
    if ps:
        put(b, rng.choice(ps), value_for(rng, b))


def requests(rng, n):
    base = bundles()
    names = sorted(base)
    out = [{"kind": "verify", "bundle": json.dumps(base[name], separators=(",", ":"))} for name in names]
    while len(out) < n:
        b = copy.deepcopy(base[rng.choice(names)])
        for _ in range(rng.choice([1, 1, 1, 2, 3])):
            if b.get("form") == "projection":
                rng.choice([mutate_projection, mutate_projection, mutate_anywhere])(rng, b)
            else:
                rng.choice([mutate_extension] * 6 + [mutate_records, mutate_anywhere])(rng, b)
        text = json.dumps(b, separators=(",", ":"))
        if rng.random() < 0.05:
            text = rng.choice([text[: rng.randrange(len(text))], text + " ", "[" + text + "]", "null"])
        out.append({"kind": "verify", "bundle": text})
    return out


def run(cmd, lines):
    cmd = cmd if isinstance(cmd, list) else [cmd]
    p = subprocess.run(cmd, input="\n".join(lines) + "\n", capture_output=True, text=True, check=True)
    return p.stdout.splitlines()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("go")
    ap.add_argument("--rust", help="the evidencebook-interop bin subject")
    ap.add_argument("--python", help="a Python interpreter with the package installed")
    ap.add_argument("--n", type=int, default=2000)
    ap.add_argument("--seed", type=int, default=1)
    a = ap.parse_args()
    if not a.rust and not a.python:
        sys.exit("name --rust, --python or both")
    reqs = requests(random.Random(a.seed), a.n)
    lines = [json.dumps(q) for q in reqs]
    go = run(a.go, lines)
    if len(go) != len(lines):
        sys.exit(f"answer counts differ: {len(lines)} asked, Go {len(go)}")
    valid = sum(1 for g in go if g != '{"valid":false}')
    print(f"seed {a.seed}: {len(lines)} inputs, Go finds {valid} valid")
    failed = False
    sides = [("rust", a.rust)] if a.rust else []
    if a.python:
        sides.append(("python", [a.python, str(Path(__file__).with_name("python_side.py"))]))
    for name, cmd in sides:
        got = run(cmd, lines)
        if len(got) != len(lines):
            sys.exit(f"answer counts differ: {len(lines)} asked, {name} {len(got)}")
        bad = [i for i, (g, s) in enumerate(zip(go, got)) if g != s]
        print(f"seed {a.seed}: {name}: {len(bad)} disagreements with Go")
        for i in bad[:5]:
            print(f"  input {i}:\n    bundle {reqs[i]['bundle'][:300]}\n    go     {go[i][:300]}\n    {name:6} {got[i][:300]}")
        failed = failed or bool(bad)
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
