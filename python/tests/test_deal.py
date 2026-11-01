# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v0 profile in Python (``evidencebook.deal``) against what Go's
``dealprofile`` is checked against, byte for byte: the profile's own fixtures
(``dealprofile/x-deal-v0/fixtures``), the deal plugin's frozen baseline
(``dealprofile/testdata/baseline-vectors.json``), and Go's answers for inputs
the fixtures do not reach (``dealprofile/testdata/parity-vectors.json``)."""
from __future__ import annotations

import json
import os
import unicodedata
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from agent_action_capsule.canonical import jcs

from evidencebook import deal
from evidencebook.deal.keys import fold

REPO = Path(__file__).resolve().parents[2]
PROFILE = REPO / "dealprofile" / "x-deal-v0"
FIXTURES = PROFILE / "fixtures"
TESTDATA = REPO / "dealprofile" / "testdata"
PACKAGE = Path(deal.__file__).resolve().parent

# The Unicode tables are Python's, not Go 1.27's (Unicode 17): the parity
# vectors hold only characters Unicode 13 (Python 3.10) assigns.
assert tuple(int(x) for x in unicodedata.unidata_version.split(".")) >= (13, 0, 0)


def load(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def stage_of(call, *args) -> str:
    with pytest.raises(deal.StageError) as e:
        call(*args)
    return e.value.stage


# --- vendored files ---------------------------------------------------------

def test_vendored_schemas_are_the_profiles():
    pairs = [(PROFILE / "x-deal-v0.schema.json", PACKAGE / "_data/schemas/x-deal-v0.schema.json")]
    for f in sorted((PROFILE / "records").glob("*.json")):
        pairs.append((f, PACKAGE / "_data/schemas/records" / f.name))
    for want, got in pairs:
        assert got.read_bytes() == want.read_bytes(), f"{want} is vendored byte for byte"
    assert len(list((PACKAGE / "_data/schemas/records").glob("*.json"))) == len(pairs) - 1
    assert deal.profile_schema() == pairs[0][0].read_bytes()


def test_public_suffix_tables_are_the_rust_crates():
    # The Rust crate vendors the same x/net tables the Go module reads.
    rust = REPO / "rust" / "evidencebook" / "vendor" / "x-net-publicsuffix"
    for name in ("text", "nodes", "children", "LICENSE"):
        assert (PACKAGE / "_data/publicsuffix" / name).read_bytes() == (rust / name).read_bytes(), name


def test_effective_tld_plus_one_as_x_net():
    from evidencebook.deal.publicsuffix import effective_tld_plus_one as e
    # Cases from x/net v0.59.0 publicsuffix/list_test.go.
    for domain, want in [
        ("com", None), ("example.com", "example.com"), ("b.example.com", "example.com"), ("uk.com", None),
        ("example.uk.com", "example.uk.com"), ("a.b.example.uk.com", "example.uk.com"), ("jp", None),
        ("kyoto.jp", None), ("test.kyoto.jp", "test.kyoto.jp"), ("ide.kyoto.jp", None),
        ("b.ide.kyoto.jp", "b.ide.kyoto.jp"), ("c.kobe.jp", None), ("b.c.kobe.jp", "b.c.kobe.jp"),
        ("city.kobe.jp", "city.kobe.jp"), ("www.city.kobe.jp", "city.kobe.jp"), ("cromulent", None),
        ("a.cromulent", "a.cromulent"), ("b.a.cromulent", "a.cromulent"), ("co.uk", None),
        ("example.co.uk", "example.co.uk"), ("www.example.co.uk", "example.co.uk"), (".example.com", None),
        ("example.com.", None), ("a..example.com", None), ("192.0.2.1", None),
    ]:
        assert e(domain) == want, domain


# --- fingerprints and commitments ------------------------------------------

def test_fingerprint_vectors():
    v = load(FIXTURES / "fingerprint-vectors.json")
    key = deal.deal_key(bytes.fromhex(v["store_secret_hex"]), v["deal_id"])
    assert key.hex() == v["deal_key_hex"]
    for x in v["vectors"]:
        assert deal.normalize(x["kind"], x["raw"]) == x["normalized"], x["raw"]
        assert deal.fingerprint(key, x["kind"], x["raw"]) == x["fp"], x["raw"]
    for x in v["commitments"]:
        assert deal.commit_text(x["nonce"], x["text"]) == x["commitment"], x["label"]
    with pytest.raises(deal.NotNormalizable):
        deal.fingerprint(key, "nickname", "x")  # a kind outside the closed set
    with pytest.raises(deal.NotNormalizable):
        deal.fingerprint(key, "profile_id", "   ")


def test_profile_key_is_not_the_deal_key():
    secret = bytes(32)
    assert deal.profile_key(secret) != deal.deal_key(secret, "deal-0123456789abcdef")
    assert len(deal.profile_key(secret)) == 32


def test_kinds_is_a_copy():
    k = deal.kinds()
    k[0] = "changed"
    assert deal.kinds()[0] == "payee"


def test_commercial_bounds_vectors():
    v = load(FIXTURES / "commercial-bounds-vectors.json")
    assert v["vectors"]
    for x in v["vectors"]:
        text = jcs(deal.decode(x["document"]))
        assert text.decode() == x["text"]
        assert deal.commit_text(x["nonce"], x["text"]) == x["bounds_commitment"]


def test_record_types_are_the_schemas():
    enum = json.loads(deal.profile_schema())["$defs"]["record_type"]["enum"]
    assert deal.record_types() == enum
    deal.record_types()[0] = "changed"
    assert deal.record_types()[0] == deal.TYPE_INTENT, "a caller cannot change the closed set"
    for t in deal.RECORD_SCHEMA_TYPES:
        deal.record_validator(t)
    with pytest.raises(ValueError):
        deal.record_validator("payment/v0")


# --- the profile's fixtures -------------------------------------------------

def local_store_values() -> list[str]:
    v = load(FIXTURES / "fingerprint-vectors.json")
    return [x["raw"] for x in v["vectors"]] + [x["text"] for x in v["commitments"] if len(x["text"]) >= 24]


def expected_digests() -> dict[str, str]:
    out = {}
    for line in (FIXTURES / "expected-digests.txt").read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            digest, name = line.split()
            out[name] = digest
    assert out
    return out


POSITIVE_CHAINS = ["positive", "positive-typed", "positive-typed-seller", "positive-sale"]
POSITIVE = [(d, f) for d in POSITIVE_CHAINS for f in sorted((FIXTURES / d).glob("*.json"))]


@pytest.mark.parametrize("chain,path", POSITIVE, ids=[f"{d}/{f.name}" for d, f in POSITIVE])
def test_positive_fixtures(chain, path):
    fx = load(path)
    payload, digest = deal.encode(json.dumps(fx["record"]), local_store_values())
    assert digest == fx["expected_digest"]
    want = expected_digests().get(f"{chain}/{path.name}")
    if want:
        assert digest == want
    deal.check_digest(payload, digest)


def test_positive_corpus_size():
    assert len(POSITIVE) == 49


# Schema-stage negatives whose expected text is the reference validator's own
# wording (jsonschema 4.23): this package reports the same wording, so none
# needs another text.
NEGATIVE = sorted((FIXTURES / "negative").glob("*.json"))


@pytest.mark.parametrize("path", NEGATIVE, ids=[p.name for p in NEGATIVE])
def test_negative_fixtures(path):
    fx = load(path)
    record = json.dumps(fx["record"])
    err = None
    try:
        deal.check(record, local_store_values())
        if fx.get("claimed_digest") is not None:
            deal.check_digest(record, fx["claimed_digest"])
    except deal.StageError as e:
        err = e
    if fx["expected_stage"] == "chain":
        # The chain stage is the reference checker's; every record-level stage passes.
        assert err is None, f"{path.name} passes the record-level stages"
        return
    assert err is not None, path.name
    assert err.stage == fx["expected_stage"], f"{path.name}: {err}"
    assert fx["expected_error_contains"] in str(err) + "\n" + "\n".join(err.causes), path.name


def test_negative_corpus_size():
    assert len(NEGATIVE) >= 50


# --- refusals ---------------------------------------------------------------

def first_positive() -> dict:
    return load(FIXTURES / "positive" / "01-baseline.json")["record"]


def test_encode_refusals():
    rec = first_positive()
    _, digest = deal.encode(rec)
    assert len(digest) == 64
    some = next(v for k in ("deal_type", "channel") if len(v := rec["body"].get(k, "")) > 2)
    assert stage_of(deal.encode, rec, [some.upper()]) == deal.STAGE_PERSONAL_DATA, "matched case-insensitively"
    err = pytest.raises(deal.StageError, deal.encode, b'{"x-deal-v0":{"canonicalization":"jcs","seq":1.5},"body":{}}').value
    assert err.stage == deal.STAGE_CANONICALIZATION
    assert "floating-point value at $.x-deal-v0.seq" in str(err), "worded as the reference checker words it"
    for text in ('call +١٢١٢٥٥٥٠١٢٣', "call 415 555 0199"):
        raw = json.dumps({"x-deal-v0": {"canonicalization": "jcs"}, "body": {"text": text}}, ensure_ascii=False)
        assert stage_of(deal.encode, raw) == deal.STAGE_PERSONAL_DATA, text
    for raw in (r'{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\ud800"}}',
                r'{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\udc00x"}}',
                r'{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\ud800A"}}',
                b'{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\xff"}}'):
        assert stage_of(deal.encode, raw) == deal.STAGE_CANONICALIZATION, raw
    # A surrogate pair, and an escaped backslash before a u, are text.
    for raw in (r'{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"😀"}}',
                r'{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\\ud800"}}'):
        assert stage_of(deal.encode, raw) == deal.STAGE_SCHEMA, raw
    assert stage_of(deal.encode, '{"x-deal-v0":{"canonicalization":"jcs-n"},"body":{}}') == deal.STAGE_CANONICALIZATION
    assert stage_of(deal.check, "[1]") == deal.STAGE_CANONICALIZATION
    assert stage_of(deal.encode, '{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"a Scam"}}') == deal.STAGE_WORDING
    assert stage_of(deal.encode, '{"x-deal-v0":{"canonicalization":"jcs"},"body":{}}') == deal.STAGE_SCHEMA
    assert stage_of(deal.encode, '{"type":"payment/v0","canonicalization":"jcs"}') == deal.STAGE_SCHEMA
    with pytest.raises(ValueError):
        deal.encode("{")
    with pytest.raises(ValueError):
        deal.encode('{"x-deal-v0":{"canonicalization":"jcs"},"n":NaN}')
    deal.check_digest(rec, digest)
    assert stage_of(deal.check_digest, rec, "0" * 64) == deal.STAGE_DIGEST


def test_a_python_value_is_written_with_json_first():
    # A float64 that is a whole number is a float in the record text, and is refused.
    assert stage_of(deal.encode, {"x-deal-v0": {"canonicalization": "jcs", "seq": 1.0}, "body": {}}) \
        == deal.STAGE_CANONICALIZATION
    assert deal.digest(first_positive()) == deal.digest(json.dumps(first_positive()))


def test_the_diagnostics_read_as_go_prints_them():
    err = pytest.raises(deal.StageError, deal.check, '{"x-deal-v0":{"canonicalization":5},"body":{}}').value
    assert 'canonicalization must be exactly "jcs" (got 5); fail closed' in str(err)
    err = pytest.raises(deal.StageError, deal.check, '{"x-deal-v0":{},"body":{}}').value
    assert "(got <nil>)" in str(err)
    err = pytest.raises(deal.StageError, deal.check, '{"type":"pay\\u00e9/v0","canonicalization":"jcs"}').value
    assert 'type: "payé/v0" is not a typed action record' in str(err)
    err = pytest.raises(deal.StageError, deal.check, '{"x-deal-v0":{"canonicalization":"jcs"},"body":{"t":"a Scam"}}').value
    assert 'prohibited word "scam" at $.body.t' in str(err)


# --- preimage ---------------------------------------------------------------

def test_preimage_vectors():
    v = load(FIXTURES / "preimage-vectors.json")
    assert v["vectors"]
    for x in v["vectors"]:
        assert deal.preimage_matches(x["preimage"], x["payment_hash"]) == x["matches"], x["label"]


def test_evidence_preimage_schema():
    rec = load(FIXTURES / "positive" / "04-evidence.json")["record"]

    def with_body(body):
        r = json.loads(json.dumps(rec))
        r["body"] = body
        return r

    received_to = {"fp_alg": "hmac-sha256-deal-key",
                   "payee": "d147f30a815fa71535d1a5249e87a8762defb852d4cf64aa10c0d24622a0a9a6"}
    pre = "ab" * 32
    deal.check(with_body({"source": "lightning_wallet", "verified": False, "received_to": received_to, "preimage": pre}))
    assert stage_of(deal.check, with_body({"source": "lightning_wallet", "verified": False, "preimage": pre})) \
        == "schema"
    assert stage_of(deal.check, with_body({"source": "lightning_wallet", "verified": False,
                                           "received_to": received_to, "preimage": "AB" + pre[2:]})) == "schema"


# --- reports and steps ------------------------------------------------------

def test_report_payload_refusals():
    r = deal.Report("deal-0123456789abcdef", deal.AUDIENCE_KEEP, "a" * 64, {"n": 1})
    payload = deal.report_payload(r)
    assert json.loads(payload) == {"audience": "keep", "deal_id": "deal-0123456789abcdef", "nonce": r.nonce,
                                   "profile": "x-deal-v0", "report": {"n": 1}, "type": "deal_report"}
    assert payload.decode() == ('{"audience":"keep","deal_id":"deal-0123456789abcdef","nonce":"' + r.nonce
                                + '","profile":"x-deal-v0","report":{"n":1},"type":"deal_report"}')
    with pytest.raises(ValueError, match="not a copy audience"):
        deal.report_payload(deal.Report(r.deal_id, "history", r.nonce, r.body))
    with pytest.raises(ValueError, match="64 lowercase hex"):
        deal.report_payload(deal.Report(r.deal_id, r.audience, "A" * 64, r.body))
    # Go writes a whole-number float64 as an integer; a fraction stays a float, which JCS refuses.
    assert b'"report":{"n":2}' in deal.report_payload(deal.Report(r.deal_id, r.audience, r.nonce, {"n": 2.0}))
    with pytest.raises(ValueError):
        deal.report_payload(deal.Report(r.deal_id, r.audience, r.nonce, {"n": 1.5}))
    with pytest.raises(ValueError):
        deal.report_payload(deal.Report(r.deal_id, r.audience, r.nonce, {"n": deal.decode("[1.0]")[0]}))
    cp = deal.Checkpoint("deal/x", 18, "ab" * 32)
    assert b'"deal_checkpoint":{"log_id":"deal/x","mmr_size":18,"root":"' in deal.report_payload(
        deal.Report(r.deal_id, r.audience, r.nonce, r.body, cp))


def test_report_payload_referee():
    r = deal.Report("deal-0123456789abcdef", deal.AUDIENCE_ADJUDICATOR, "a" * 64, {"n": 1}, referee="  Cafe\u0301 Disputes\n")
    payload = deal.report_payload(r)
    assert json.loads(payload)["referee"] == "Caf\u00e9 Disputes"
    assert deal.report_referee(payload) == "Caf\u00e9 Disputes"
    for referee in ("", " \t\n"):
        with pytest.raises(ValueError, match="names the referee"):
            deal.report_payload(deal.Report(r.deal_id, r.audience, r.nonce, r.body, referee=referee))
    for audience in (deal.AUDIENCE_KEEP, deal.AUDIENCE_COUNTERPARTY):
        with pytest.raises(ValueError, match="only an adjudicator's report names a referee"):
            deal.report_payload(deal.Report(r.deal_id, audience, r.nonce, r.body, referee="the platform"))
        payload = deal.report_payload(deal.Report(r.deal_id, audience, r.nonce, r.body))
        assert b"referee" not in payload
        assert deal.report_referee(payload) == ""


def test_shared_referee_errors():
    payload = deal.report_payload(deal.Report("deal-0123456789abcdef", deal.AUDIENCE_ADJUDICATOR, "a" * 64, {},
                                              referee="the platform"))
    buyer, seller = deal.ReportCopy(payload, "b"), deal.ReportCopy(payload, "s")
    assert deal.shared_referee(buyer, seller) == "the platform"
    with pytest.raises(deal.RefereeError, match="no referee both sides chose") as e:
        deal.shared_referee(buyer)
    assert e.value.reason == deal.REFEREE_ONE_COPY
    with pytest.raises(deal.RefereeError) as e:
        deal.shared_referee(buyer, deal.ReportCopy(payload.replace(b"platform", b"platform\xff"), "s"))
    assert e.value.reason == deal.REFEREE_MALFORMED, "a payload that is not UTF-8"
    with pytest.raises(deal.RefereeError) as e:
        deal.report_referee(b"[]")
    assert e.value.reason == deal.REFEREE_MALFORMED


def test_report_capsule_is_to_the_second():
    at = datetime(2026, 10, 30, 1, 2, 3, 999, tzinfo=timezone(timedelta(hours=1)))
    c = deal.report_capsule("deal", "capsulectl-deal", at)
    assert c.developer == "capsulectl-deal"
    assert c.timestamp == "2026-10-30T00:02:03Z"
    assert c.action_id == deal.REPORT_ACTION_ID
    assert c.action_type == deal.ACTION_TYPE_FYI
    assert c.chain is None, "a report is not chained"
    with pytest.raises(ValueError, match="developer name"):
        deal.report_capsule("deal", "", at)


def test_step_capsule_refusals():
    inp = deal.StepInput("deal-0123456789abcdef", 1, "deal", "a-node", datetime.fromtimestamp(7, timezone.utc))
    c, h = deal.step_capsule(inp)
    assert c.developer == "a-node"
    assert c.timestamp == h.committed_at == "1970-01-01T00:00:07Z"
    assert c.action_id == "deal-0123456789abcdef/1"
    assert c.action_type == deal.ACTION_TYPE_FYI
    assert c.chain is None and h.cites == ()
    assert [e.name for e in c.extensions] == [deal.DEAL_ID_MEMBER]
    from dataclasses import replace
    for bad, msg in [(replace(inp, seq=0), "seq"), (replace(inp, deal_id=""), "deal id"),
                     (replace(inp, developer=""), "developer name"),
                     (replace(inp, parent=deal.Chain("a" * 64, "supersedes")), "not follows or confirms")]:
        with pytest.raises(ValueError, match=msg):
            deal.step_capsule(bad)


# --- the frozen baseline ----------------------------------------------------

BASELINE = load(TESTDATA / "baseline-vectors.json")["deals"]


def parse_time(s: str) -> datetime:
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


@pytest.mark.parametrize("d", BASELINE, ids=[d["flow"] for d in BASELINE])
def test_baseline_steps(d):
    """Rebuilds every step. With EVIDENCEBOOK_DEAL_BUILT_CHAINS set to a
    directory, it also writes each deal's chain as this package encoded it (one
    JSON array per deal, the bytes Go's TestBaselineSteps writes), for the
    reference checker's chain stage, which CI runs over them and compares with
    Go's files."""
    chain = []
    for s in d["steps"]:
        payload, digest = deal.encode(s["payload"])
        chain.append(payload.decode())
        assert payload.decode() == s["payload"], f"step {s['n']} payload"
        assert digest == s["digest"], f"step {s['n']} digest"

        sealed = json.loads(s["capsule"])
        parent = None
        if sealed.get("chain"):
            parent = deal.Chain(sealed["chain"]["parent_capsule_id"], sealed["chain"]["relation"])
        decide = None
        if sealed["action_type"] == "decide":
            disp, eff = sealed["disposition"], sealed["effect"]
            decide = deal.Decide(
                deal.Disposition(disp["decision"], disp["approver"], disp["human_disposed"],
                                 disp.get("verdict_class", ""), disp.get("reason_digest", "")),
                eff["type"], eff["irreversibility_class"])
        h0 = s["header"]
        capsule, header = deal.step_capsule(deal.StepInput(
            d["deal_id"], s["n"], d["operator"], "capsulectl-deal", parse_time(sealed["timestamp"]), parent,
            sealed.get("settlement"), decide, h0["fields"]["deal_local"], h0["fields"]["amount"],
            h0["fields"]["currency"]))

        # The Capsule's members, as the sealer lays them out in Go's sealed bytes.
        assert capsule.action_id == sealed["action_id"]
        assert capsule.action_type == sealed["action_type"]
        assert capsule.operator == sealed["operator"] and capsule.developer == sealed["developer"]
        assert capsule.timestamp == sealed["timestamp"]
        assert (capsule.chain is not None) == ("chain" in sealed)
        if capsule.chain:
            assert {"parent_capsule_id": capsule.chain.parent_capsule_id,
                    "relation": capsule.chain.relation} == sealed["chain"]
        ext = {e.name: e.value for e in capsule.extensions}
        assert ext.get("settlement") == sealed.get("settlement")
        assert ext[deal.DEAL_ID_MEMBER] == sealed["deal_id"] == d["deal_id"]
        if capsule.disposition:
            disp = capsule.disposition
            want = {"decision": disp.decision, "approver": disp.approver, "human_disposed": disp.human_disposed}
            if disp.verdict_class:
                want["verdict_class"] = disp.verdict_class
            if disp.reason_digest:
                want["reason_digest"] = disp.reason_digest
            assert want == sealed["disposition"]
            e = capsule.effect
            assert {"type": e.type, "status": e.status, "irreversibility_class": e.irreversibility_class,
                    "effect_attestation": e.effect_attestation} == sealed["effect"]
        else:
            assert "disposition" not in sealed and "effect" not in sealed

        assert header.record_type == h0["record_type"]
        assert header.epistemic_type == h0["epistemic_type"]
        assert header.committed_at == h0["committed_at"]
        assert header.subject_ref == h0["subject_ref"]
        assert not h0["correlation"], "a step names no correlation"
        assert list(header.cites) == [link["target"] for link in h0["links"]]
        assert all(link["type"] == "cites" for link in h0["links"])
        assert header.fields == h0["fields"]
    out = os.environ.get("EVIDENCEBOOK_DEAL_BUILT_CHAINS")
    if out:
        (Path(out) / f"{d['flow']}.json").write_text("[" + ",".join(chain) + "]", encoding="utf-8")


@pytest.mark.parametrize("d", BASELINE, ids=[d["flow"] for d in BASELINE])
def test_baseline_reports(d):
    for r in d.get("reports", []):
        inp = deal.decode(r["agent_input"])
        cp = None
        if "deal_checkpoint" in inp:
            c = inp["deal_checkpoint"]
            cp = deal.Checkpoint(c["log_id"], c["mmr_size"], c["root"])
        payload = deal.report_payload(deal.Report(inp["deal_id"], inp["audience"], inp["nonce"], inp["report"], cp))
        assert payload.decode() == r["payload"], f"{r['form']} payload"
        assert (r["form"] == "standalone") == (cp is not None), "only a standalone report binds a checkpoint"
        sealed = json.loads(r["capsule"])
        capsule = deal.report_capsule(d["operator"], "capsulectl-deal", parse_time(r["timestamp"]))
        assert (capsule.action_id, capsule.action_type, capsule.operator, capsule.developer, capsule.timestamp) \
            == (sealed["action_id"], sealed["action_type"], sealed["operator"], sealed["developer"],
                sealed["timestamp"])
        assert "chain" not in sealed


def test_baseline_has_both_report_forms():
    forms = {r["form"] for d in BASELINE for r in d.get("reports", [])}
    assert forms == {"standalone", "in_log"}


# --- Go's answers ----------------------------------------------------------

PARITY = load(TESTDATA / "parity-vectors.json")


def test_parity_corpus_sizes():
    assert (len(PARITY["normalize"]), len(PARITY["check"]), len(PARITY["history"])) == (135, 95, 77)


@pytest.mark.parametrize("x", PARITY["normalize"], ids=[f"{x['kind']}:{x['raw']!r}" for x in PARITY["normalize"]])
def test_parity_normalize(x):
    key = deal.deal_key(bytes.fromhex(PARITY["store_secret_hex"]), PARITY["deal_id"])
    if x["normalized"] is None:
        assert x["fp"] is None
        with pytest.raises(deal.NotNormalizable):
            deal.normalize(x["kind"], x["raw"])
        return
    assert deal.normalize(x["kind"], x["raw"]) == x["normalized"]
    assert deal.fingerprint(key, x["kind"], x["raw"]) == x["fp"]


@pytest.mark.parametrize("x", PARITY["check"], ids=[x["name"] for x in PARITY["check"]])
def test_parity_check(x):
    local = x.get("local_values")
    if x["stage"] in ("", None):
        payload, digest = deal.encode(x["record"], local or ())
        assert payload.decode() == x["payload"]
        assert digest == x["digest"]
        return
    if x["stage"] == "error":
        with pytest.raises(ValueError) as e:
            deal.encode(x["record"], local or ())
        assert not isinstance(e.value, deal.StageError)
        return
    assert stage_of(deal.encode, x["record"], local or ()) == x["stage"]


def history_input(h: dict) -> deal.HistoryInput:
    return deal.HistoryInput(
        tuple(deal.HistoryRecord(r["payload"].encode(), parse_time(r["committed_at"])) for r in h["records"] or []),
        tuple(deal.HistoryResult(r["result"].encode(), r["verified"]) for r in h.get("results", [])),
        h.get("counterparty_half_verified", False))


@pytest.mark.parametrize("h", PARITY["history"], ids=[h["name"] for h in PARITY["history"]])
def test_parity_standard_history(h):
    inp = history_input(h)
    if h.get("error") == "unpriced":
        with pytest.raises(deal.Unpriced, match="deal deal-[0-9a-f]+: dealprofile: a deal has no recorded amount"):
            deal.standard_history_deal(inp)
    elif h.get("error"):
        with pytest.raises(ValueError) as e:
            deal.standard_history_deal(inp)
        assert not isinstance(e.value, deal.Unpriced)
    else:
        got = deal.standard_history_deal(inp)
        assert got == h["entry"]
        if got is not None:
            # The same bytes as Go writes the entry, member for member.
            assert jcs(got) == jcs(h["entry"])
            assert list(got) == list(h["entry"]), "members in Go's struct order"


# Go's parityReferees seals these reports under this nonce and body.
REFEREE_NONCE = "5a0f6c1e9b2d4387a6c5e1f0b9d8c7a6e5f4d3c2b1a09f8e7d6c5b4a39281706"


@pytest.mark.parametrize("x", PARITY["referee_payloads"], ids=[f"{x['audience']}:{x['referee']!r}" for x in PARITY["referee_payloads"]])
def test_parity_referee_payload(x):
    r = deal.Report(PARITY["deal_id"], x["audience"], REFEREE_NONCE, {"lines": ["one"]}, referee=x["referee"])
    if x.get("error"):
        with pytest.raises(ValueError):
            deal.report_payload(r)
        return
    assert deal.report_payload(r).decode() == x["payload"]


@pytest.mark.parametrize("x", PARITY["shared_referees"], ids=[x["name"] for x in PARITY["shared_referees"]])
def test_parity_shared_referee(x):
    copies = [deal.ReportCopy(c["payload"].encode(), c["party"]) for c in x["copies"]]
    if x.get("reason"):
        with pytest.raises(deal.RefereeError) as e:
            deal.shared_referee(*copies)
        assert e.value.reason == x["reason"]
        return
    assert deal.shared_referee(*copies) == x["referee"]


def test_parity_minor_units():
    for code, want in PARITY["minor_units"].items():
        assert deal.minor_units(code) == want, code


@pytest.mark.parametrize("c", PARITY["cards"], ids=[c["name"] for c in PARITY["cards"]])
def test_parity_standard_history_cards(c):
    """The card agent-action-capsule derives from these entries is the one Go's derives, byte for byte."""
    from agent_action_capsule.standard_history import derive_card
    card = derive_card(holder_kind="person", controller_class="holder", anchor=c["as_of"]["anchor"],
                       anchor_time=c["as_of"]["anchor_time"],
                       first_checkpoint_time=c["as_of"]["first_checkpoint_time"], policy=c["policy"],
                       deals=[json.loads(d) for d in c["deals"] or []])
    assert jcs(card).decode() == c["card"]


def test_cards_over_python_entries_are_go_cards():
    """The entries Python makes (not the ones Go wrote) derive the same card."""
    from agent_action_capsule.standard_history import derive_card
    entries = [e for h in PARITY["history"] if not h.get("error") and (e := deal.standard_history_deal(history_input(h)))]
    c = next(c for c in PARITY["cards"] if c["name"] == "every entry, all time")
    card = derive_card(holder_kind="person", controller_class="holder", anchor=c["as_of"]["anchor"],
                       anchor_time=c["as_of"]["anchor_time"],
                       first_checkpoint_time=c["as_of"]["first_checkpoint_time"], policy=c["policy"], deals=entries)
    assert jcs(card).decode() == c["card"]


def test_the_counterparty_class_seam():
    # The profile records no SH §7 class yet: the entry has no member for it.
    entry = deal.standard_history_deal(history_input(PARITY["history"][0]))
    assert "counterparty_class" not in entry


def test_fold_takes_both_cherokee_cases_to_the_capitals():
    # CaseFolding.txt; Go (dealprofile) and Rust agree, and the parity vectors pin it.
    assert fold("Ꭰ") == "Ꭰ" and fold("ꭰ") == "Ꭰ" and fold("ᏸ") == "Ᏸ"


def test_parity_vectors_hold_only_characters_the_running_unicode_assigns():
    """Python 3.10 reads Unicode 13: a vector with a newer character would pass only on newer Pythons."""
    def texts(v):
        if isinstance(v, str):
            yield v
        elif isinstance(v, dict):
            for k, x in v.items():
                if k not in ("versions", "note"):
                    yield from texts(x)
        elif isinstance(v, list):
            for x in v:
                yield from texts(x)
    for t in texts(PARITY):
        assert all(unicodedata.category(c) != "Cn" for c in t), t
