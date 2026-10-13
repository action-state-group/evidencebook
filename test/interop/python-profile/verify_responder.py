# SPDX-License-Identifier: Apache-2.0
"""Run the evidence interoperability profile's reference verifier over this
responder's output, case by case against the profile's conformance vectors.

    python verify_responder.py <agent-action-capsule checkout> <responder.json>

<responder.json> is written by `go run ./test/interop/responder`. The checkout
supplies the verifier (python/agent_action_capsule/interop_profile) and the
vectors (vectors/interop-profile-v0/vectors.json); both are read, never
modified. Every vector case is accounted for in exactly one of three ways:

- run: the responder answered the vector's request (made against its own log)
  and the verifier's result matches the case's expected block;
- mutation: the vector's corruption was applied to this responder's own reply
  (re-signed where the vector re-signs) and the verifier's result matches;
- skipped, with the reason.

The carriage cases (§11.4) are run over the native stream and MCP. This
repository has no A2A carriage, so each carriage case is compared with its
expected block less its A2A half (the A2A reply, digest and classification),
and the cases that exist only for A2A are skipped. Digests the vectors write
for their own request are compared as "equal to the request digest".

The vectors' trust pins are used as written, except log_keys, which name this
responder's log (its iss and sub) under the same test log key. Row 7's status
is read from the vectors file, never assumed.
"""
from __future__ import annotations

import base64
import copy
import hashlib
import json
import sys
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit("usage: verify_responder.py <agent-action-capsule checkout> <responder.json>")
AAC = Path(sys.argv[1]).resolve()
sys.path.insert(0, str(AAC / "python"))

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey  # noqa: E402

from agent_action_capsule.canonical import jcs  # noqa: E402
from agent_action_capsule.interop_profile import carriage as carriage_mod  # noqa: E402
from agent_action_capsule.interop_profile._cbor import decode as cbor_decode, encode_head  # noqa: E402
from agent_action_capsule.interop_profile.carriage import manifest_consistency, manifest_form  # noqa: E402
from agent_action_capsule.interop_profile import (  # noqa: E402
    Exchange,
    InteropVerifier,
    Message,
    TrustPolicy,
    VerificationPolicy,
)

VECTORS = json.loads((AAC / "vectors" / "interop-profile-v0" / "vectors.json").read_text(encoding="utf-8"))
CASES = {c["id"]: c for c in VECTORS["cases"]}
CONTRACT = VECTORS["contract"]["document"]
PRODUCED = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))

RUN = ["valid-exchange", "invariance-two-nonces-two-transports", "declined", "missing-evidence-no-such-subject",
       "unsupported-version-refused", "missing-evidence-recorded-absence"]
MUTATIONS = ["tampered-artifact", "wrong-request-digest", "envelope-bad-signature", "envelope-untrusted-responder",
             "envelope-subject-mismatch", "envelope-anchor-not-the-pin", "verification-material-tampered",
             "inclusion-proof-forged-and-signed", "forged-refusal-untrusted-key", "forged-refusal-bad-signature"]
CARRIAGE_RUN = ["carriage-invariance"]
CARRIAGE_MUTATIONS = ["carriage-mcp-rehashes-json", "carriage-mcp-error-no-reply", "carriage-mcp-error-with-reply",
                      "carriage-manifest-count-member"]
SKIP_PREFIX = {
    "unsupported-version-mismatch": "verifier-side: an envelope naming a profile other than the one requested; a "
                                    "conforming evidence-interop/0 responder never emits one",
    "unsupported-version-absent": "verifier-side: an envelope with no profile member, from a responder implementing "
                                  "the request draft alone; this responder always sends profile",
    "unsupported-version-unimplemented": "verifier-side: an envelope naming a profile the verifier does not "
                                         "implement; this responder implements evidence-interop/0 only",
    "foreign-native-check-": "the case's Artifact carries a foreign signed object at a member path; this "
                             "responder's canonical Artifacts are its own AAC records and carry none",
    "acceptance-": "§11.1 acceptance records and receipts: not responder output, and not in this item",
    "lifecycle-": "§11.2 lifecycle statements: not responder output, and not in this item",
    "carriage-a2a-": "§11.4.4 A2A carriage: this repository has no A2A adapter, so there is no A2A Task of its "
                     "own to classify; the native stream and MCP halves of the carriage cases are run",
}


def seed_key(label: str) -> Ed25519PrivateKey:
    return Ed25519PrivateKey.from_private_bytes(hashlib.sha256(label.encode()).digest())


KEYS = VECTORS["test_keys"]["keys"]
RESPONDER = seed_key(KEYS["responder"]["seed_label"])
IMPOSTOR = seed_key(KEYS["impostor"]["seed_label"])
IMPOSTOR_PUB = KEYS["impostor"]["public"]
if PRODUCED["responder_key"] != KEYS["responder"]["public"]:
    raise SystemExit("the responder did not sign with the vectors' responder test key")


def sha(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


def b64(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def unb64(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def octets(o: dict) -> bytes:
    return o["octets_text"].encode("utf-8") if "octets_text" in o else bytes.fromhex(o["octets_hex"])


def message(o: dict) -> Message:
    return Message(octets(o), o["media_type"])


def json_reply(obj: dict) -> dict:
    return {"media_type": "application/json", "octets_text": jcs(obj).decode("utf-8")}


SIGNED = ("profile", "request_digest", "subject", "derivation", "anchor", "artifact_digest", "verification",
          "responder", "issued_at")


def resign(env: dict, key: Ed25519PrivateKey) -> dict:
    env = copy.deepcopy(env)
    env["sig"] = key.sign(jcs({k: env[k] for k in SIGNED if k in env})).hex()
    return env


def resign_refusal(ref: dict, key: Ed25519PrivateKey) -> dict:
    ref = copy.deepcopy(ref)
    ref["sig"] = key.sign(jcs({k: ref[k] for k in ("issued_at", "reason", "request_digest")})).hex()
    return ref


def flip_last(b: bytes) -> bytes:
    return b[:-1] + bytes([b[-1] ^ 0x01])


def policies(case: dict):
    pins = copy.deepcopy(case["pins"])
    pins["trust_policy"]["log_keys"] = PRODUCED["log_keys"]
    return (TrustPolicy.from_dict(pins["trust_policy"]), VerificationPolicy.from_dict(pins["verification_policy"]),
            pins["evaluation_time"])


def render(result, multi: bool) -> dict:
    out: dict = {"replies": []}
    for k, ex in enumerate(result.exchanges):
        for r in ex.replies:
            rendered = {"message_type": r.message_type, "properties": {p.property: p.status for p in r.properties},
                        "requirements": {q: {"row": x.row, "status": x.status, "sufficiency": x.sufficiency,
                                             "verdict": x.verdict} for q, x in r.requirements.items()}}
            allowed = {tuple(x.presentation_kinds_allowed) for x in r.requirements.values() if x.presentation_kinds_allowed}
            if allowed:
                rendered["presentation_kinds_allowed"] = list(next(iter(allowed)))
            if multi:
                rendered["request_index"] = k
            out["replies"].append(rendered)
        if ex.absence is not None:
            a = ex.absence
            out["absence"] = {"form": a.form, "requirements": {q: {"row": x.row, "status": x.status,
                                                                   "sufficiency": x.sufficiency, "verdict": x.verdict}
                                                               for q, x in a.requirements.items()},
                              "presentation_kind_recommended": a.presentation_kind_recommended, "detail": a.detail}
    if multi:
        artifacts = [r.artifact for ex in result.exchanges for r in ex.replies]
        out["artifact_octets_identical"] = len(set(artifacts)) == 1
    out["set_properties"] = {"caller_invariance": result.caller_invariance.status}
    return out


def expected_view(expected: dict) -> dict:
    """The parts of a case's expected block that do not depend on the vectors' own bytes."""
    out: dict = {"replies": []}
    for r in expected.get("replies", []):
        e = {"message_type": r["message_type"], "properties": r["properties"],
             "requirements": {q: {k: x[k] for k in ("row", "status", "sufficiency", "verdict")}
                              for q, x in r["requirements"].items()}}
        if "presentation_kinds_allowed" in r:
            e["presentation_kinds_allowed"] = r["presentation_kinds_allowed"]
        if "request_index" in r:
            e["request_index"] = r["request_index"]
        out["replies"].append(e)
    if "absence" in expected:
        a = expected["absence"]
        out["absence"] = {"form": a["form"], "requirements": {q: {k: x[k] for k in ("row", "status", "sufficiency", "verdict")}
                                                              for q, x in a["requirements"].items()},
                          "presentation_kind_recommended": a["presentation_kind_recommended"]}
    if "artifact_octets_identical" in expected:
        out["artifact_octets_identical"] = expected["artifact_octets_identical"]
    out["set_properties"] = expected.get("set_properties", {"caller_invariance": "NOT_CHECKED"})
    return out


def verify(case: dict, requests: list, replies: list, absence: dict | None = None) -> dict:
    trust, vpolicy, now = policies(case)
    verifier = InteropVerifier(trust, vpolicy, now, contract=CONTRACT)
    exchanges = [Exchange(message(r)) for r in requests]
    for i, rep in enumerate(replies):
        ex = exchanges[min(i, len(exchanges) - 1)]
        ex.replies = [*ex.replies, message(rep)]
    if absence is not None:
        exchanges[0].absence_record = message(absence)
    result = verifier.verify(exchanges, requirements=["r-record"])
    got = render(result, len(requests) > 1)
    if absence is not None and got.get("absence", {}).get("form") != "conforming":
        print(f"   absence detail: {got['absence'].get('detail')}")
    got.get("absence", {}).pop("detail", None)
    return got


def cbor_encode(v) -> bytes:
    """Deterministic CBOR (RFC 8949 §4.2.1) of the envelope's data model."""
    if isinstance(v, bool) or v is None or isinstance(v, float):
        raise TypeError(f"no {type(v).__name__} in an envelope")
    if isinstance(v, int):
        return encode_head(0, v) if v >= 0 else encode_head(1, -1 - v)
    if isinstance(v, (bytes, bytearray)):
        return encode_head(2, len(v)) + bytes(v)
    if isinstance(v, str):
        b = v.encode("utf-8")
        return encode_head(3, len(b)) + b
    if isinstance(v, list):
        return encode_head(4, len(v)) + b"".join(cbor_encode(x) for x in v)
    if isinstance(v, dict):
        items = sorted((cbor_encode(k), cbor_encode(x)) for k, x in v.items())
        return encode_head(5, len(v)) + b"".join(k + x for k, x in items)
    raise TypeError(type(v).__name__)


def cbor_value(b: bytes):
    decoded = cbor_decode(b)
    if not decoded.well_formed or not decoded.deterministic or cbor_encode(decoded.value) != b:
        raise SystemExit("this responder's CBOR reply does not round-trip as deterministic CBOR")
    return decoded.value


def render_carriage(case: dict, request: dict, native: dict | None = None, mcp: dict | None = None,
                    absence: dict | None = None, manifests: dict | None = None) -> dict:
    """The carriage case's checks, as the AAC vector test runs them, over what this responder carried."""
    trust, vpolicy, now = policies(case)
    req = message(request)
    mt, r_digest = req.media_type, sha(req.octets)
    same = lambda d: "= request digest" if d == r_digest else d  # noqa: E731
    digests, classes, replies = {}, {}, []
    if native is not None:
        digests["native"] = same(sha(bytes.fromhex(native["request_frame_hex"])))
        classes["native"] = "reply"
        replies.append(bytes.fromhex(native["response_frame_hex"]))
    if mcp is not None:
        sent = carriage_mod.mcp_request_octets(mcp["call"])
        if sent is not None:
            digests["mcp"] = same(sha(sent[0]))
        got = carriage_mod.classify_mcp_result(mcp["result"], mt)
        classes["mcp"] = got.outcome
        replies += got.replies
    exchange = Exchange(req, [Message(r, mt) for r in replies], message(absence) if absence else None)
    result = InteropVerifier(trust, vpolicy, now, contract=CONTRACT).verify([exchange], requirements=["r-record"])
    ex = result.exchanges[0]
    out: dict = {"request_digest": same(ex.request_digest), "carriage_request_digests": digests,
                 "classification": classes,
                 "replies": [{"message_type": r.message_type, "properties": {p.property: p.status for p in r.properties},
                              "requirements": {q: {"row": x.row, "status": x.status, "sufficiency": x.sufficiency,
                                                   "verdict": x.verdict} for q, x in r.requirements.items()}}
                             for r in ex.replies]}
    if ex.replies:
        out["set_properties"] = {"caller_invariance": result.caller_invariance.status}
    if ex.absence is not None:
        out["absence"] = {"form": ex.absence.form, "requirements": {
            q: {"row": x.row, "status": x.status, "sufficiency": x.sufficiency, "verdict": x.verdict}
            for q, x in ex.absence.requirements.items()},
            "presentation_kind_recommended": ex.absence.presentation_kind_recommended}
    if manifests is not None:
        implemented = vpolicy.profiles_implemented
        out["manifest_properties"] = {name: {"manifest_form": manifest_form(m, implemented).status}
                                      for name, m in manifests.items()}
        out["manifest_properties"]["manifest_consistency"] = manifest_consistency(list(manifests.values())).status
    return out


def expected_carriage(case: dict, keep_a2a_manifest: bool = False) -> dict:
    """The case's expected block less its A2A half, with its own request digest written as such."""
    e, inp = case["expected"], case["inputs"]
    r_digest = e["request_digest"]
    same = lambda d: "= request digest" if d == r_digest else d  # noqa: E731
    replies = list(e.get("replies", []))
    if "a2a" in inp:
        start = 1 if "native" in inp else 0
        count = len(carriage_mod.classify_a2a_task(inp["a2a"]["task"], inp["request"]["media_type"]).replies)
        del replies[start:start + count]
    out: dict = {"request_digest": same(r_digest),
                 "carriage_request_digests": {k: same(v) for k, v in e["carriage_request_digests"].items() if k != "a2a"},
                 "classification": {k: v for k, v in e["classification"].items() if k != "a2a"},
                 "replies": [{"message_type": r["message_type"], "properties": r["properties"],
                              "requirements": {q: {k: x[k] for k in ("row", "status", "sufficiency", "verdict")}
                                               for q, x in r["requirements"].items()}} for r in replies]}
    if "set_properties" in e:
        out["set_properties"] = e["set_properties"]
    if "absence" in e:
        a = e["absence"]
        out["absence"] = {"form": a["form"], "requirements": {
            q: {k: x[k] for k in ("row", "status", "sufficiency", "verdict")} for q, x in a["requirements"].items()},
            "presentation_kind_recommended": a["presentation_kind_recommended"]}
    if "manifest_properties" in e:
        mp = dict(e["manifest_properties"])
        if not keep_a2a_manifest:
            mp.pop("a2a_extension", None)
        out["manifest_properties"] = mp
    return out


def carriage_case(case_id: str) -> tuple[dict, dict]:
    """(got, want) for a carriage case, run over this responder's native and MCP carriage."""
    case = CASES[case_id]
    inv = PRODUCED["carriage"]["invariance"]
    mcp_manifest = PRODUCED["mcp_manifest"]["structuredContent"]
    if case_id == "carriage-invariance":
        # The MCP manifest and the manifest the book serves natively, before
        # and after answering: one manifest over every carriage.
        manifests = {"mcp_evidence_manifest": mcp_manifest}
        got = render_carriage(case, inv["request"], inv["native"], inv["mcp"], manifests=manifests)
        got["manifest_properties"]["manifest_consistency"] = manifest_consistency(
            [mcp_manifest, *PRODUCED["manifests"]]).status
        return got, expected_carriage(case)
    if case_id == "carriage-mcp-rehashes-json":
        # The MCP reply re-signed over the digest of a JSON re-encoding of R.
        request = cbor_value(octets(inv["request"]))
        reply = copy.deepcopy(inv["mcp"])
        env = cbor_value(unb64(reply["result"]["structuredContent"]["reply"]))
        env["request_digest"] = sha(jcs(request))
        env["sig"] = RESPONDER.sign(jcs({k: env[k] for k in SIGNED if k in env})).hex()
        reply["result"]["structuredContent"]["reply"] = b64(cbor_encode(env))
        return render_carriage(case, inv["request"], inv["native"], reply), expected_carriage(case)
    if case_id.startswith("carriage-mcp-error-"):
        absent = PRODUCED["carriage"]["mcp-absence"]
        if case_id == "carriage-mcp-error-no-reply":
            # The result replaced by an isError result with no reply and text
            # saying the caller is not authorized; the requester's absence
            # record once its window closed.
            mcp = {"call": absent["mcp"]["call"], "result": copy.deepcopy(case["inputs"]["mcp"]["result"])}
            return render_carriage(case, absent["request"], mcp=mcp, absence=absent["absence_record"]), \
                expected_carriage(case)
        mcp = copy.deepcopy(inv["mcp"])
        mcp["result"]["isError"] = True
        return render_carriage(case, inv["request"], mcp=mcp), expected_carriage(case)
    if case_id == "carriage-manifest-count-member":
        # This responder's manifest with the vector's count member added,
        # standing where the A2A extension params would be.
        counted = dict(mcp_manifest, record_count=42)
        manifests = {"a2a_extension": counted, "mcp_evidence_manifest": mcp_manifest}
        got = render_carriage(case, inv["request"], manifests=manifests)
        return got, expected_carriage(case, keep_a2a_manifest=True)
    raise KeyError(case_id)


def mutate(case_id: str):
    base = PRODUCED["exchanges"]["valid-exchange"]
    request, reply = base["requests"][0], base["replies"][0]
    env = json.loads(octets(reply))
    if case_id == "tampered-artifact":
        env["artifact"] = b64(flip_last(unb64(env["artifact"])))
    elif case_id == "wrong-request-digest":
        env["request_digest"] = sha(octets(PRODUCED["exchanges"]["invariance-two-nonces-two-transports"]["requests"][1]))
        env = resign(env, RESPONDER)
    elif case_id == "envelope-bad-signature":
        env = resign(env, IMPOSTOR)
    elif case_id == "envelope-untrusted-responder":
        env["responder"] = IMPOSTOR_PUB
        env = resign(env, IMPOSTOR)
    elif case_id in ("envelope-subject-mismatch", "envelope-anchor-not-the-pin"):
        request = PRODUCED["other_requests"]["another-record" if case_id == "envelope-subject-mismatch" else "another-pin"]
        env["request_digest"] = sha(octets(request))
        env = resign(env, RESPONDER)
    elif case_id == "verification-material-tampered":
        env["verification_content"][1] = b64(flip_last(unb64(env["verification_content"][1])))
    elif case_id == "inclusion-proof-forged-and-signed":
        forged = flip_last(unb64(env["verification_content"][1]))
        env["verification_content"][1] = b64(forged)
        env["verification"][1]["digest"] = sha(forged)
        env = resign(env, RESPONDER)
    elif case_id.startswith("forged-refusal-"):
        declined = PRODUCED["exchanges"]["declined"]
        request = declined["requests"][0]
        ref = json.loads(octets(declined["replies"][0]))
        if case_id == "forged-refusal-untrusted-key":
            ref["key_id"] = IMPOSTOR_PUB
        return request, json_reply(resign_refusal(ref, IMPOSTOR))
    else:
        raise KeyError(case_id)
    return request, json_reply(env)


def main() -> int:
    ran, mutated, skipped, failed = [], [], [], []
    for case_id, case in CASES.items():
        want = expected_view(case["expected"])
        if case_id in CARRIAGE_RUN or case_id in CARRIAGE_MUTATIONS:
            got, want = carriage_case(case_id)
            bucket = ran if case_id in CARRIAGE_RUN else mutated
        elif case_id in RUN:
            ex = PRODUCED["exchanges"][case_id]
            got = verify(case, ex["requests"], ex["replies"], ex.get("absence_record"))
            bucket = ran
        elif case_id in MUTATIONS:
            request, reply = mutate(case_id)
            got = verify(case, [request], [reply])
            bucket = mutated
        else:
            reason = next((r for p, r in SKIP_PREFIX.items() if case_id == p or case_id.startswith(p)), None)
            if reason is None:
                print(f"UNCLASSIFIED {case_id}")
                failed.append(case_id)
                continue
            skipped.append((case_id, reason))
            continue
        if got == want:
            bucket.append(case_id)
            print(f"PASS {case_id}")
        else:
            failed.append(case_id)
            print(f"FAIL {case_id}\n   got  {json.dumps(got, sort_keys=True)}\n   want {json.dumps(want, sort_keys=True)}")
    print(f"\nvectors: {len(CASES)}; run against this responder's replies: {len(ran)} passed; "
          f"run as mutations of its replies: {len(mutated)} passed; skipped: {len(skipped)}; failed: {len(failed)}")
    for case_id, reason in skipped:
        print(f"SKIP {case_id}: {reason}")
    identical = True
    for group in ("invariance_artifacts_hex", "invariance_record_set_artifacts_hex"):
        artifacts = PRODUCED[group]
        same = len(artifacts) == 2 and len(set(artifacts.values())) == 1
        identical = identical and same
        print(f"invariance: canonical Artifact octets across {sorted(artifacts)}: "
              f"{'byte-identical' if same else 'DIFFERENT'} (sha256 {sha(bytes.fromhex(next(iter(artifacts.values()))))})")
    native_mcp = [PRODUCED["carriage"]["invariance"]["native"]["response_frame_hex"],
                  PRODUCED["carriage"]["invariance"]["mcp"]["result"]["structuredContent"]["reply"]]
    a_native = cbor_value(bytes.fromhex(native_mcp[0]))["artifact"]
    a_mcp = cbor_value(unb64(native_mcp[1]))["artifact"]
    identical = identical and a_native == a_mcp
    print(f"invariance: canonical Artifact octets across ['mcp', 'native'] for request R (CBOR): "
          f"{'byte-identical' if a_native == a_mcp else 'DIFFERENT'} (sha256 {sha(a_native)})")
    manifests = [*PRODUCED["manifests"], PRODUCED["mcp_manifest"]["structuredContent"]]
    form = manifest_form(manifests[0], frozenset({"evidence-interop/0"}))
    consistency = manifest_consistency(manifests)
    print(f"manifest: manifest_form {form.status}{' (' + form.detail + ')' if form.detail else ''}; "
          f"manifest_consistency across {len(manifests)} copies (native before and after answering, and MCP "
          f"evidence_manifest) {consistency.status}")
    ok = form.status == "PASS" and consistency.status == "PASS"
    return 0 if ok and not failed and identical and len(ran) + len(mutated) + len(skipped) == len(CASES) else 1


if __name__ == "__main__":
    sys.exit(main())
