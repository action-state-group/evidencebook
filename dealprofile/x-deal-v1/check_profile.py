#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 reference checker (PROFILE.md section 9).

Stdlib only, apart from `jsonschema` for the schema stage. Without it the schema
stage is skipped: the negatives that expect it are listed, and the run ends
"OK, reduced coverage" (exit 0) instead of "ALL OK". CI installs jsonschema.

    python check_profile.py            run the fixtures (fixtures/)
    python check_profile.py FILE...    check each file's record on its own

It applies sections 1 to 6 of PROFILE.md: the canonicalization, schema and record
stages, an agreement read against its offer and a dispute or ruling against its
agreement, and each positive book's seq, prev and agreement_ref. It reuses
x-deal-v0's JCS (check_profile.py beside it), as the profile reuses v0's
canonicalization. The authorization of acts (section 8) and a pair sent whole
(section 7) are not checked here; the Go package xdealv1 checks them.
"""
from __future__ import annotations

import datetime
import hashlib
import importlib.util
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
FIX = HERE / "fixtures"
SCHEMA_PATH = HERE / "x-deal-v1.schema.json"
V0_DIR = HERE.parent / "x-deal-v0"

_spec = importlib.util.spec_from_file_location("x_deal_v0_check_profile", V0_DIR / "check_profile.py")
v0 = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(v0)

PROFILE = "x-deal-v1"
RECORD_REF = "deal-record"

DEAL_BOOK_TYPES = {"perform", "settle", "statement", "dispute", "rules_verdict"}
NO_DEAL_ID = {"intent", "admit", "sale", "thread", "thread_opened", "sale_unpost", "counterparty_profile", "disclosure"}
JOURNEY_TYPES = {"message", "claim", "evidence", "detail_change", "agent_move"}
ASK_TYPES = {"ask", "answer"}


class StageError(Exception):
    def __init__(self, stage: str, msg: str):
        super().__init__(f"[{stage}] {msg}")
        self.stage = stage


def bad(stage, msg):
    return StageError(stage, msg)


# --- canonicalization -------------------------------------------------------

def _no_repeats(pairs):
    seen = set()
    for k, _ in pairs:
        if k in seen:
            raise bad("canonicalization", f"member {k!r} given twice")
        seen.add(k)
    return dict(pairs)


def _float(text):
    raise bad("canonicalization", f"a float at {text}; numbers are integers")


def parse(raw) -> dict:
    """A record's JSON text, read as written: valid UTF-8, no lone surrogate,
    no member given twice, integers only (PROFILE.md section 1)."""
    if isinstance(raw, bytes):
        try:
            raw = raw.decode("utf-8")
        except UnicodeDecodeError:
            raise bad("canonicalization", "invalid UTF-8") from None
    try:
        doc = json.loads(raw, object_pairs_hook=_no_repeats, parse_float=_float)
    except StageError:
        raise
    except ValueError as e:
        raise bad("canonicalization", f"not one JSON value: {e}") from None
    _no_surrogates(doc)
    return doc


def _no_surrogates(v):
    if isinstance(v, str):
        try:
            v.encode("utf-8")
        except UnicodeEncodeError:
            raise bad("canonicalization", "an escaped surrogate outside a pair") from None
    elif isinstance(v, dict):
        for k, e in v.items():
            _no_surrogates(k)
            _no_surrogates(e)
    elif isinstance(v, list):
        for e in v:
            _no_surrogates(e)


def stage_canonicalization(rec):
    blk = rec.get(PROFILE) if isinstance(rec, dict) else None
    if not isinstance(blk, dict):
        raise bad("canonicalization", "no x-deal-v1 block")
    if blk.get("canonicalization") != "jcs":
        raise bad("canonicalization", f'canonicalization must be exactly "jcs" (got {blk.get("canonicalization")!r}); fail closed')
    try:
        v0.jcs(rec)
    except v0.JCSError as e:
        raise bad("canonicalization", f"not JCS-serializable: {e}") from None


def digest(rec) -> str:
    return hashlib.sha256(v0.jcs(rec)).hexdigest()


# --- schema -----------------------------------------------------------------

SCHEMA_MODE = "unknown"
_VALIDATORS = {}


def _validators():
    """The v1 record validator and v0's body_check validator, or None
    without jsonschema."""
    global SCHEMA_MODE
    if not _VALIDATORS:
        try:
            from jsonschema import Draft202012Validator
            from referencing import Registry, Resource
        except ImportError:
            SCHEMA_MODE = "skipped (jsonschema not installed)"
            _VALIDATORS["record"] = None
            return _VALIDATORS
        v1s = json.loads(SCHEMA_PATH.read_text())
        v0s = json.loads(v0.SCHEMA_PATH.read_text())
        registry = Registry().with_resources([(s["$id"], Resource.from_contents(s)) for s in (v1s, v0s)])
        _VALIDATORS["record"] = Draft202012Validator(v1s, registry=registry)
        _VALIDATORS["body_check"] = Draft202012Validator({"$ref": v0s["$id"] + "#/$defs/body_check"}, registry=registry)
        SCHEMA_MODE = "jsonschema (Draft 2020-12)"
    return _VALIDATORS


def stage_schema(rec):
    v = _validators()["record"]
    if v is None:
        return False
    errs = sorted(v.iter_errors(rec), key=lambda e: list(e.absolute_path))
    if errs:
        e = errs[0]
        raise bad("schema", "/" + "/".join(str(p) for p in e.absolute_path) + ": " + e.message)
    blk = rec[PROFILE]
    if blk["record_type"] == "check":
        body = {k: x for k, x in rec["body"].items() if k != "payment_ref"}
        errs = list(_validators()["body_check"].iter_errors(body))
        if errs:
            raise bad("schema", f"the check body is not v0's body_check: {errs[0].message}")
    return True


# --- record -----------------------------------------------------------------

def placement(rec) -> str:
    """Where a record is sealed (PROFILE.md section 4)."""
    blk, body = rec[PROFILE], rec["body"]
    t = blk["record_type"]
    if t == "ruling":
        return "case_book"
    if t in DEAL_BOOK_TYPES:
        return "deal_book"
    if t == "agree":
        return "deal_book" if body.get("kind") == "agreement" else "journal"
    if t in ASK_TYPES and blk.get("seq", 0) > 0:
        return "deal_book"
    return "journal"


def _instant(s):
    try:
        t = datetime.datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError:
        return False
    return t.strftime("%Y-%m-%dT%H:%M:%SZ") == s


def check_dates(rec):
    blk, body = rec[PROFILE], rec["body"]
    times = [blk["at"]]
    t = blk["record_type"]
    if t == "propose":
        times += [body["stated_at"], body["agree_by"]]
        tr = body["terms_rows"]
        for row in tr["rows"]:
            times += [row["due"].get("at", ""), row["window_end"].get("at", "")]
        times += [n["latest"] for n in tr["named"].values()]
    elif t == "ruling" and "remedy" in body:
        times.append(body["remedy"]["due"])
    for s in times:
        if s and not _instant(s):
            raise bad("record", f"{s!r} is not a UTC time to the second that exists")


def check_deal_id(rec, place):
    blk, body = rec[PROFILE], rec["body"]
    t, has = blk["record_type"], bool(blk.get("deal_id"))
    required = absent = False
    if place != "journal":
        required = True
    elif t in JOURNEY_TYPES or t in ASK_TYPES:
        pass
    elif t in NO_DEAL_ID:
        absent = True
    elif t == "approval":
        if body.get("choice") == "confirm_limits":
            absent = True
        else:
            required = True
    else:
        required = True  # propose, agree (decline, cancellation), check, verdict
    if required and not has:
        raise bad("record", f"a {t} record carries deal_id")
    if absent and has:
        raise bad("record", f"a {t} record carries no deal_id: it predates or spans deals")


def _check_witnesses(ws):
    seen = set()
    for w in ws or []:
        if w["key_id"] in seen:
            raise bad("record", f"witness {w['key_id']} is named twice")
        seen.add(w["key_id"])


def _check_forums(adj, forums):
    if adj is None and forums:
        raise bad("record", "an offer or agreement with no adjudicator names no forum")
    if adj is not None and not forums:
        raise bad("record", "an offer or agreement with an adjudicator names the forums both sides accept")


def _check_rows(tr):
    seen = set()
    for row in tr["rows"]:
        if row["id"] in seen:
            raise bad("record", f"row {row['id']} is given twice")
        seen.add(row["id"])
        for d in (row["due"], row["window_end"]):
            if "after" in d and d["after"] not in tr["named"]:
                raise bad("record", f"row {row['id']} hangs on {d['after']}, which is not named")


def check_body(rec):
    blk, body = rec[PROFILE], rec["body"]
    t = blk["record_type"]
    if t == "propose":
        _check_witnesses(body["witnesses"])
        _check_forums(body["adjudicator"], body["forums"])
        _check_rows(body["terms_rows"])
    elif t == "agree":
        _check_witnesses(body.get("witnesses"))
        if body["kind"] == "agreement":
            _check_forums(body["adjudicator"], body["forums"])
            layer = body["counterparty_layer"]
            if not layer and (blk.get("seen") is not None or body["counterparty_key"] is not None):
                raise bad("record", "an agreement with a counterparty that runs no layer has seen null and counterparty_key null")
            if layer and body["counterparty_key"] is None:
                raise bad("record", "an agreement with a counterparty that runs the layer names its record key in counterparty_key")
            if body["side"] == "acceptor" and blk.get("seen") is not None:
                raise bad("record", "the acceptor's agreement comes first: its seen is null")


def stage_record(rec):
    """PROFILE.md sections 2 to 6 on one record."""
    blk, body = rec[PROFILE], rec["body"]
    t = blk["record_type"]
    place = placement(rec)
    seq = blk.get("seq", 0)
    if place in ("deal_book", "case_book"):
        if seq < 1:
            raise bad("record", f"a {t} record in a {place} carries its seq")
        if ("prev" in blk) != (seq > 1):
            raise bad("record", "a book record carries prev exactly when its seq is past 1")
        if place == "case_book":
            if "seen" in blk or "agreement_ref" in blk:
                raise bad("record", "a ruling carries no seen and no agreement_ref")
        else:
            if "seen" not in blk:
                raise bad("record", "a deal-book record carries seen (null when nothing is held from the other side)")
            if ("agreement_ref" in blk) != (seq > 1):
                raise bad("record", "a deal-book record carries agreement_ref exactly when its seq is past 1")
            if t == "agree" and body.get("kind") == "agreement" and seq != 1:
                raise bad("record", "an agreement opens its deal book: seq 1")
            if t != "agree" and seq == 1:
                raise bad("record", f"a deal book opens with its agreement, not a {t}")
    else:
        if seq != 0 or any(k in blk for k in ("prev", "agreement_ref", "seen")):
            raise bad("record", "a journal record carries no seq, prev, agreement_ref or seen, so a record sent whole shows no place in the journal")
        if t == "agree" and any(k in blk for k in ("refs", "marks", "counterparty")):
            raise bad("record", f"a {body.get('kind')} carries no refs, marks or counterparty fingerprint: it names nothing beyond the offer it answers")
    check_dates(rec)
    check_deal_id(rec, place)
    check_body(rec)


def check_record(rec, seal_context=None) -> bool:
    """Every stage on one parsed record; with seal_context, the deal_id rule
    for a journey record and a journal ask or answer (CheckSealed). Returns
    False when the schema stage was skipped."""
    stage_canonicalization(rec)
    full = stage_schema(rec)
    stage_record(rec)
    if seal_context is not None and placement(rec) == "journal":
        t, has = rec[PROFILE]["record_type"], bool(rec[PROFILE].get("deal_id"))
        if t in JOURNEY_TYPES or t in ASK_TYPES:
            journey = t in JOURNEY_TYPES
            want = seal_context.get("offer_exists", False) if journey else not seal_context.get("admission", False)
            if has != want:
                why = "when its offer exists" if journey else "between the two parties of a deal"
                raise bad("record", f"a {t} record sealed here carries deal_id only {why}")
    return full


# --- an agreement, a dispute and a ruling read against another record --------

def _ref_digest(r):
    return r["digest"] if r and r.get("type") == RECORD_REF else None


def check_agreement(ag, offer, held_acceptor_agreement=""):
    """PROFILE.md section 5: an agreement against the offer it cites."""
    for r in (ag, offer):
        check_record(r)
    a, ab = ag[PROFILE], ag["body"]
    o, ob = offer[PROFILE], offer["body"]
    if a["record_type"] != "agree" or ab.get("kind") != "agreement":
        raise bad("agreement", "not an agreement")
    if o["record_type"] != "propose":
        raise bad("agreement", f"the agreement cites a {o['record_type']}, not an offer")
    if _ref_digest(ab["offer"]) != digest(offer):
        raise bad("agreement", "the agreement does not cite this offer's record digest")
    if a.get("deal_id") != o.get("deal_id"):
        raise bad("agreement", "the agreement's deal_id is not the offer's")
    if ab["rows_digest"] != hashlib.sha256(v0.jcs(ob["terms_rows"])).hexdigest():
        raise bad("agreement", "rows_digest is not the offer's rows digest")
    if sorted(w["key_id"] for w in ab["witnesses"]) != sorted(w["key_id"] for w in ob["witnesses"]):
        raise bad("agreement", "the agreement's witnesses are not the offer's")
    if ab["adjudicator"] != ob["adjudicator"]:
        raise bad("agreement", "the agreement's adjudicator is not the offer's")
    if sorted(ab["forums"]) != sorted(ob["forums"]):
        raise bad("agreement", "the agreement's forums are not the offer's")
    if ab["side"] == "offerer":
        if ab["role"] != ob["role"]:
            raise bad("agreement", "the offerer's role is the offer's")
        if ab["counterparty_layer"] and a.get("seen") != held_acceptor_agreement:
            raise bad("agreement", "the offerer's agreement names the acceptor's agreement it holds in seen")
    elif ab["role"] == ob["role"]:
        raise bad("agreement", "the acceptor's role is the other side of the offer's")


def _agreement_of(ag):
    if ag[PROFILE]["record_type"] != "agree" or ag["body"].get("kind") != "agreement":
        raise bad("agreement", "not an agreement")
    return ag["body"]


def check_dispute(dispute, ag):
    """PROFILE.md section 5: a dispute against its book's agreement."""
    d = dispute["body"]
    if dispute[PROFILE]["record_type"] != "dispute" or d.get("kind") != "dispute":
        raise bad("agreement", "not a dispute of kind dispute")
    ab = _agreement_of(ag)
    if dispute[PROFILE].get("deal_id") != ag[PROFILE].get("deal_id"):
        raise bad("agreement", "the dispute is of another deal")
    if _ref_digest(dispute[PROFILE].get("agreement_ref")) != digest(ag):
        raise bad("agreement", "the dispute is bound to another agreement (agreement_ref)")
    if ab["adjudicator"] is None:
        raise bad("agreement", "the agreement names no adjudicator: no dispute is possible")
    if d["adjudicator"] != ab["adjudicator"]["key_id"]:
        raise bad("agreement", "the dispute goes to another adjudicator than the agreement names")
    if d["forum"] not in ab["forums"]:
        raise bad("agreement", f"the dispute's forum {d['forum']} is not one the agreement accepts")


def check_ruling(ruling, ag):
    """PROFILE.md section 5: a ruling rules in a forum the agreement accepts."""
    if ruling[PROFILE]["record_type"] != "ruling":
        raise bad("agreement", "not a ruling")
    ab = _agreement_of(ag)
    if ruling[PROFILE].get("deal_id") != ag[PROFILE].get("deal_id"):
        raise bad("agreement", "the ruling is of another deal")
    if ruling["body"]["forum"] not in ab["forums"]:
        raise bad("agreement", f"the ruling's forum {ruling['body']['forum']} is not one the agreement accepts")


def check_book(records):
    """A book: seq from 1 with no gaps, prev the previous record's digest,
    agreement_ref the agreement's, one deal id."""
    for i, rec in enumerate(records):
        blk = rec[PROFILE]
        if blk.get("seq") != i + 1:
            raise bad("book", f"record {i + 1} has seq {blk.get('seq')}")
        if blk.get("deal_id") != records[0][PROFILE].get("deal_id"):
            raise bad("book", f"record {i + 1} is of another deal")
        if i > 0:
            if _ref_digest(blk.get("prev")) != digest(records[i - 1]):
                raise bad("book", f"record {i + 1}'s prev is not record {i}'s digest")
            if "agreement_ref" in blk and _ref_digest(blk["agreement_ref"]) != digest(records[0]):
                raise bad("book", f"record {i + 1}'s agreement_ref is not the agreement's digest")


# --- runs -------------------------------------------------------------------

def _load(path: Path):
    return json.loads(path.read_text())


def run_fixtures() -> int:
    fails, reduced = [], []
    manifest = {}
    for line in (FIX / "expected-digests.txt").read_text().splitlines():
        if line and not line.startswith("#"):
            d, p = line.split("  ", 1)
            manifest[p] = d
    positives = {}
    for path in sorted(FIX.rglob("*.json")):
        rel = path.relative_to(FIX).as_posix()
        if rel.startswith("negative/"):
            continue
        f = _load(path)
        rec = parse(json.dumps(f["record"]))
        positives[rel] = f
        try:
            full = check_record(rec, f.get("seal_context"))
            got = digest(rec)
            if got != f["expected_digest"] or manifest.get(rel) != got:
                raise bad("digest", f"digest {got}, fixture says {f['expected_digest']}, manifest {manifest.get(rel)}")
            if placement(rec) != f["placement"]:
                raise bad("record", f"placement {placement(rec)}, fixture says {f['placement']}")
            if not full:
                reduced.append(rel)
        except StageError as e:
            fails.append(f"{rel}: {e}")
    missing = set(manifest) - set(positives)
    fails += [f"{p}: in expected-digests.txt, no file" for p in sorted(missing)]
    rec = {p: f["record"] for p, f in positives.items()}

    # The relations the positives hold: each agreement against the offer, the
    # dispute and the rulings against the agreements, each book's links.
    pairs = [
        ("agreement", "appendix-a/seller-book/01-agreement.json", "appendix-a/buyer-journal/05-offer.json", ""),
        ("agreement", "appendix-a/buyer-book/01-agreement.json", "appendix-a/buyer-journal/05-offer.json",
         positives["appendix-a/seller-book/01-agreement.json"]["record_id"]),
        ("dispute", "appendix-a/buyer-book/05-dispute.json", "appendix-a/buyer-book/01-agreement.json", None),
    ]
    for r in ("appendix-a/case-book/01-ruling.json", "appendix-a/case-book/02-ruling-final.json"):
        for ag in ("appendix-a/buyer-book/01-agreement.json", "appendix-a/seller-book/01-agreement.json"):
            pairs.append(("ruling", r, ag, None))
    for kind, a, b, held in pairs:
        try:
            {"agreement": lambda: check_agreement(rec[a], rec[b], held),
             "dispute": lambda: check_dispute(rec[a], rec[b]),
             "ruling": lambda: check_ruling(rec[a], rec[b])}[kind]()
        except StageError as e:
            fails.append(f"{a} against {b}: {e}")
    books = {}
    for p in positives:
        if positives[p]["placement"] != "journal":
            books.setdefault(p.rsplit("/", 1)[0], []).append(p)
    for book, paths in sorted(books.items()):
        if book.startswith("settled/"):
            main = "appendix-a/" + book.split("/", 1)[1]
            first = rec[paths[0]][PROFILE]["seq"]
            paths = sorted(books[main])[: first - 1] + paths
        try:
            check_book([rec[p] for p in sorted(paths, key=lambda p: rec[p][PROFILE]["seq"])])
        except StageError as e:
            fails.append(f"{book}: {e}")

    skipped = []
    negatives = sorted((FIX / "negative").glob("*.json"))
    for path in negatives:
        n = _load(path)
        name = n["fixture"]
        if n["expected_stage"] == "schema" and _validators()["record"] is None:
            skipped.append(name)
            continue
        try:
            raw = n.get("record_text") or json.dumps(n["record"])
            r = parse(raw)
            check = n["check"]
            if check == "record":
                check_record(r, n.get("seal_context"))
            elif check == "agreement":
                check_agreement(r, rec[n["with"]], n.get("held_acceptor_agreement", ""))
            elif check == "dispute":
                check_record(r)
                check_dispute(r, rec[n["with"]])
            elif check == "ruling":
                check_record(r)
                check_ruling(r, rec[n["with"]])
            fails.append(f"negative/{name}: accepted")
        except StageError as e:
            if e.stage != n["expected_stage"] or n["expected_error_contains"] not in str(e):
                fails.append(f"negative/{name}: refused {e}, expected [{n['expected_stage']}] containing {n['expected_error_contains']!r}")
    print(f"x-deal-v1 check_profile.py: schema {SCHEMA_MODE}")
    print(f"positives: {len(positives)}, negatives: {len(negatives)}")
    for f in fails:
        print("FAIL", f)
    if fails:
        return 1
    if skipped or reduced:
        print(f"schema stage skipped: {len(reduced)} positives checked without it; "
              f"{len(skipped)} negatives that expect it not run: {', '.join(skipped)}")
        print("OK, reduced coverage")
        return 0
    print("ALL OK")
    return 0


def check_files(paths) -> int:
    bad_files = 0
    for p in paths:
        try:
            doc = parse(Path(p).read_bytes())
            check_record(doc.get("record", doc) if "record" in doc and PROFILE not in doc else doc)
            print(f"OK   {p}")
        except StageError as e:
            bad_files += 1
            print(f"FAIL {p}: {e}")
    return 1 if bad_files else 0


def main(argv) -> int:
    if argv:
        return check_files(argv)
    return run_fixtures()


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
