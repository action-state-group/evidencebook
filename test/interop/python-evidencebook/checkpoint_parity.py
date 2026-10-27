# SPDX-License-Identifier: Apache-2.0
"""Differential test of the CLL checkpoint reader: the Python package's
against Go's (checkpointed-local-log checkpoint.ParseRecord, as
checkpointFromStatement applies it).

    python checkpoint_parity.py cases cases.json
    go run ./test/interop/checkpoint-parity < cases.json > go.json
    python checkpoint_parity.py compare cases.json go.json

Each case is one field of a real checkpoint statement changed by one rule
(its type, range, null, absence, encoding), signed again under the log key,
so the signature never decides it.

The spec decides (draft-mih-scitt-checkpointed-local-log-01 and the RFCs it
cites); where it is silent, Go's reader does. So the Python reader must
agree with Go on every case, except the ones GO_DEPARTS lists, where Go
departs from the spec: there Python must give the spec's verdict and Go the
other one (a pinned gap: the comparison fails the day Go is fixed, and the
mark must then go).
"""
from __future__ import annotations

import hashlib
import json
import sys
from datetime import datetime, timedelta, timezone

import cbor2

import evidencebook as eb
from evidencebook._cll import read_checkpoint

LOG_KEY = eb.Ed25519Signer(hashlib.sha256(b"checkpoint parity: log key").digest())

GO_ISSUE = "https://github.com/action-state-group/checkpointed-local-log/issues/99"
_MUST_IGNORE = "-01 The Checkpoint: other claims MAY be present; verifiers MUST ignore claims they do not understand"
_PROOF_CLAIM = "-01 defines no consistency_proof claim (the proof is made available with the checkpoint or on request); ignored"
_ENCODING = "RFC 8949 §3, §4: valid CBOR; RFC 9052 §9 requires deterministic encoding of the Sig_structure only"
_PEAKS = "-01 Commitment Representation: the peak list of the MMR at that size"
#: (case name, without its first-/second- prefix when the departure holds for
#: both checkpoints, the spec's verdict, the basis).
GO_DEPARTS: list[tuple[str, str, str]] = [
    ("claims-extra-member", "accept", _MUST_IGNORE),
    ("claims-consistency_proof-missing", "accept", _PROOF_CLAIM),
    ("claims-consistency_proof-null", "accept", _PROOF_CLAIM),
    ("claims-consistency_proof-bool", "accept", _PROOF_CLAIM),
    ("claims-consistency_proof-text", "accept", _PROOF_CLAIM),
    ("claims-consistency_proof-int", "accept", _PROOF_CLAIM),
    ("claims-proof-on-first", "accept", _PROOF_CLAIM),
    ("claims-proof-size-a-text", "accept", _PROOF_CLAIM),
    ("claims-proof-size-a-wrong", "accept", _PROOF_CLAIM),
    ("claims-proof-old-peaks-int", "accept", _PROOF_CLAIM),
    ("claims-proof-witness-text", "accept", _PROOF_CLAIM),
    ("claims-proof-witness-null-path", "accept", _PROOF_CLAIM),
    ("claims-proof-witness-tampered", "accept", _PROOF_CLAIM),
    ("claims-proof-extra-member", "accept", _PROOF_CLAIM),
    ("claims-proof-missing-member", "accept", _PROOF_CLAIM),
    ("second-claims-prev-commitment-of-commitment", "accept",
     "-01 Required constraints 2: the relation is checked with the proof conveyed beside the checkpoint"),
    ("claims-prev-size-equal", "accept", "-01 Required constraints 2: log_size MUST be greater than or equal to prev_size"),
    ("claims-indefinite", "accept", _ENCODING),
    ("claims-noncanonical-order", "accept", _ENCODING),
    ("protected-indefinite", "accept", _ENCODING),
    ("outer-payload-long-length", "accept", _ENCODING),
    ("protected-extra-label", "accept", "RFC 9052 §3: other header parameters; none is critical"),
    ("protected-crit-understood", "accept", "RFC 9052 §3.1: crit lists only labels understood"),
    ("protected-cwt-extra-claim", "accept", "RFC 8392 §3; -01 Witnessing requires iss and sub only"),
    ("outer-unprotected-label", "accept", "-01 Witnessing: a countersignature in the unprotected header"),
    ("claims-issued-at-lowercase", "accept", "RFC 3339 §5.6 NOTE: t and z may be lower case"),
    ("claims-issued-at-leap-second-1990", "accept", "RFC 3339 §5.7, §5.8: 23:59:60 UTC on a leap-second day"),
    ("claims-issued-at-leap-second-1990-offset", "accept", "RFC 3339 §5.8: the same leap second at -08:00"),
    ("claims-issued-at-leap-second-2016", "accept", "RFC 3339 §5.7: 23:59:60 UTC on a leap-second day"),
    ("claims-extra-nonscalar-keys", "accept", _MUST_IGNORE + "; RFC 8949 §3.1: any data item is a map key"),
    ("claims-extra-depth-32", "accept", _MUST_IGNORE + "; 32 levels, Go's decoder limit (the spec sets none)"),
    ("protected-text-label-1-beside-alg", "accept", "RFC 9052 §3: text \"1\" and integer 1 are different labels"),
    ("claims-issued-at-comma-fraction", "refuse", "RFC 3339 §5.6: time-secfrac is \".\" 1*DIGIT"),
    ("claims-issued-at-offset-plus-24", "refuse", "RFC 3339 §5.6: time-numoffset's time-hour is 00-23"),
    ("claims-issued-at-offset-minus-24", "refuse", "RFC 3339 §5.6: time-numoffset's time-hour is 00-23"),
    ("first-claims-log_size-int", "refuse", _PEAKS),
    ("second-claims-prev_size-int", "refuse", _PEAKS),
    ("first-claims-commitment-empty-array", "refuse", _PEAKS),
    ("first-claims-prev_commitment-null", "refuse", "-01 The Checkpoint: prev_commitment is a bstr, zero-length for the first"),
    ("first-claims-prev-commitment-empty-array", "refuse", "-01 The Checkpoint: prev_commitment is zero-length for the first"),
]


def departure(name: str) -> tuple[str, str] | None:
    """The spec's verdict and basis where Go departs from it, for this case."""
    bare = name.split("-", 1)[1]
    for case, verdict, basis in GO_DEPARTS:
        if case in (name, bare):
            return verdict, basis
    return None


def _head(major: int, n: int) -> bytes:
    if n < 24:
        return bytes([major << 5 | n])
    for width, code in ((1, 24), (2, 25), (4, 26), (8, 27)):
        if n < 1 << (8 * width):
            return bytes([major << 5 | code]) + n.to_bytes(width, "big")
    raise ValueError(n)


def raw_map(pairs: list[tuple[object, object]], indefinite: bool = False) -> bytes:
    """A CBOR map in exactly this order, duplicates kept; a value that is
    bytes already encoded is passed as ("raw", bytes)."""
    def item(v):
        return v[1] if isinstance(v, tuple) and v and v[0] == "raw" else cbor2.dumps(v)
    body = b"".join(item(k) + item(v) for k, v in pairs)
    return (b"\xbf" + body + b"\xff") if indefinite else _head(5, len(pairs)) + body


def sign(protected: bytes, payload: bytes, *, unprotected=None, tag: int | None = 18, extra=None,
         signature=None) -> bytes:
    to_be_signed = cbor2.dumps(["Signature1", protected, b"", payload])
    sig = LOG_KEY.sign(to_be_signed) if signature is None else signature
    body = [protected, {} if unprotected is None else unprotected, payload, sig]
    if extra is not None:
        body.append(extra)
    return cbor2.dumps(cbor2.CBORTag(tag, body) if tag is not None else body)


def base_book():
    clock = [datetime(2026, 10, 12, tzinfo=timezone.utc)]

    def now():
        clock[0] += timedelta(seconds=1)
        return clock[0]

    book = eb.Book("parity", "parity-operator", signer=LOG_KEY, log_id="parity-log", log_key=LOG_KEY, now=now)
    book.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    first = book.checkpoint()
    book.append(eb.Entry("observation", eb.OBSERVED_EVENT))
    second = book.checkpoint()
    return first, second


def parts(statement: bytes) -> tuple[dict, dict]:
    protected_bstr, _, payload, _ = cbor2.loads(statement).value
    return cbor2.loads(protected_bstr), cbor2.loads(payload)


def canonical(claims: dict) -> bytes:
    return cbor2.dumps(claims, canonical=True)


def cases() -> dict[str, bytes]:
    first, second = base_book()
    out: dict[str, bytes] = {"base-first": first.statement, "base-second": second.statement}
    for label, cp in (("first", first), ("second", second)):
        out.update(mutations(label, cp, second))
    return out


def mutations(label: str, cp, second) -> dict[str, bytes]:
    """Every case for one checkpoint (a first one, or one extending another)."""
    out: dict[str, bytes] = {}
    prot, claims = parts(cp.statement)
    prot_bstr = cbor2.dumps(prot)

    def claims_case(name: str, changed: dict, payload: bytes | None = None) -> None:
        body = canonical(changed) if payload is None else payload
        p = dict(prot)
        if isinstance(changed.get("log_size"), int) and not isinstance(changed["log_size"], bool):
            p[15] = {1: prot[15][1], 2: f"{prot[15][1]}#{changed['log_size']}"}
        out[f"{label}-claims-{name}"] = sign(cbor2.dumps(p), body)

    # Each claim missing, null, or of another type.
    for name in [*claims, "cadence"]:
        if name in claims:
            claims_case(f"{name}-missing", {k: v for k, v in claims.items() if k != name})
        claims_case(f"{name}-null", dict(claims, **{name: None}))
        claims_case(f"{name}-bool", dict(claims, **{name: True}))
        claims_case(f"{name}-text", dict(claims, **{name: "x"}))
        claims_case(f"{name}-int", dict(claims, **{name: 1}))
    claims_case("extra-member", dict(claims, extra=1))
    claims_case("indefinite", claims, raw_map(sorted(claims.items(), key=lambda kv: (len(kv[0]), kv[0])),
                                            indefinite=True))
    claims_case("noncanonical-order", claims, raw_map(list(reversed(list(claims.items())))))
    claims_case("duplicate-kind", claims,
                raw_map([("kind", "cll-checkpoint"), *sorted(claims.items(), key=lambda kv: (len(kv[0]), kv[0]))]))
    claims_case("kind-other", dict(claims, kind="checkpoint"))
    claims_case("log-size-zero", dict(claims, log_size=0))
    claims_case("log-size-float", dict(claims, log_size=float(claims["log_size"])))
    claims_case("log-size-uint64-max", dict(claims, log_size=(1 << 64) - 1))
    claims_case("prev-size-above-log-size", dict(claims, prev_size=claims["log_size"] + 1))
    claims_case("commitment-empty-array", dict(claims, commitment=b"\x80"))
    claims_case("commitment-indefinite", dict(claims, commitment=b"\x9f" + claims["commitment"][1:] + b"\xff"))
    claims_case("commitment-short-peak", dict(claims, commitment=cbor2.dumps([b"\x00" * 31])))
    claims_case("commitment-not-array", dict(claims, commitment=cbor2.dumps(b"\x00" * 32)))
    claims_case("prev-commitment-empty", dict(claims, prev_commitment=b""))
    claims_case("prev-commitment-empty-array", dict(claims, prev_commitment=b"\x80"))
    claims_case("prev-commitment-of-commitment", dict(claims, prev_commitment=claims["commitment"]))
    for name, text in (("offset", claims["issued_at"][:-1] + "+01:00"), ("lowercase", claims["issued_at"].lower()),
                       ("fraction-10", claims["issued_at"][:-1] + ".1234567890Z"),
                       ("fraction", claims["issued_at"][:-1] + ".5Z"), ("empty", ""),
                       ("no-zone", claims["issued_at"][:-1]), ("date-only", claims["issued_at"][:10]),
                       ("leap-second", claims["issued_at"][:17] + "60Z"), ("hour-24", claims["issued_at"][:11] + "24:00:00Z")):
        claims_case(f"issued-at-{name}", dict(claims, issued_at=text))
    for name, value in (("zero", 0), ("one", 1), ("2^53-1", (1 << 53) - 1), ("2^53", 1 << 53)):
        claims_case(f"cadence-{name}", dict(claims, cadence=value))
    for name, text in (("comma-fraction", claims["issued_at"][:-1] + ",5Z"),
                       ("offset-plus-24", claims["issued_at"][:-1] + "+24:00"),
                       ("offset-minus-24", claims["issued_at"][:-1] + "-24:00"),
                       ("year-0000", "0000" + claims["issued_at"][4:]),
                       ("year-0000-offset", "0000-01-01T05:00:00+05:00"),
                       ("leap-second-end-of-day", claims["issued_at"][:11] + "23:59:60Z"),
                       ("february-30", claims["issued_at"][:5] + "02-30" + claims["issued_at"][10:])):
        claims_case(f"issued-at-{name}", dict(claims, issued_at=text))
    # A one-mountain MMR beyond cll Python's 2^50 limit, and one past the portable range.
    first_of_size = {k: v for k, v in claims.items() if k != "consistency_proof"}
    claims_case("log-size-2^51-1", dict(first_of_size, log_size=(1 << 51) - 1, prev_size=0, prev_commitment=b"",
                                        commitment=cbor2.dumps([claims_peak(claims)])))
    claims_case("log-size-2^54-1", dict(first_of_size, log_size=(1 << 54) - 1, prev_size=0, prev_commitment=b"",
                                        commitment=cbor2.dumps([claims_peak(claims)])))
    claims_case("oversized", dict(claims, padding=bytes(70000)))
    for name, text in (("leap-second-1990", "1990-12-31T23:59:60Z"), ("leap-second-1990-offset", "1990-12-31T15:59:60-08:00"),
                       ("leap-second-2016", "2016-12-31T23:59:60Z"), ("leap-second-not-at-day-end", "1990-12-31T23:58:60Z")):
        claims_case(f"issued-at-{name}", dict(claims, issued_at=text))
    claims_case("extra-nonscalar-keys", dict(claims, extra={(1, 2): 0, True: [1], "k": {(): b""}}))
    nested: object = 0
    for _ in range(31):
        nested = [nested]
    claims_case("extra-depth-32", dict(claims, extra=nested))  # the claims map is the 32nd level
    claims_case("extra-depth-33", dict(claims, extra=[nested]))
    claims_case("log-size-bignum", claims, payload_with(claims, "log_size", "c2" + cbor2.dumps(claims["log_size"].to_bytes(2, "big")).hex()))
    claims_case("extra-simple-two-byte", claims, payload_with(claims, "extra", "f800"))
    claims_case("extra-break-in-tag", claims, payload_with(claims, "extra", "c1ff"))
    if claims["prev_size"]:
        claims_case("prev-size-equal", dict(claims, prev_size=claims["log_size"], prev_commitment=claims["commitment"]))
    if "consistency_proof" in claims:
        proof = claims["consistency_proof"]
        for name, changed in (("size-a-text", dict(proof, size_a=str(proof["size_a"]))),
                              ("size-a-wrong", dict(proof, size_a=proof["size_a"] + 1)),
                              ("old-peaks-int", dict(proof, old_peaks=[1])),
                              ("witness-text", dict(proof, witness=[["x"]])),
                              ("witness-tampered", dict(proof, witness=[[b"\x00" * 32 for _ in path]
                                                                        for path in proof["witness"]])),
                              ("extra-member", dict(proof, extra=1)),
                              ("witness-null-path", dict(proof, witness=[None for _ in proof["witness"]])),
                              ("missing-member", {k: v for k, v in proof.items() if k != "new_peaks"})):
            claims_case(f"proof-{name}", dict(claims, consistency_proof=changed))
    else:
        _, later = parts(second.statement)
        claims_case("proof-on-first", dict(claims, consistency_proof=later["consistency_proof"]))

    payload = canonical(claims)

    def header_case(name: str, protected_bstr: bytes) -> None:
        out[f"{label}-protected-{name}"] = sign(protected_bstr, payload)

    cwt, kid = prot[15], prot[4]
    header_case("noncanonical-order", raw_map([(3, prot[3]), (1, prot[1]), (4, kid), (15, cwt)]))
    header_case("duplicate-alg", raw_map([(1, prot[1]), (1, prot[1]), (3, prot[3]), (4, kid), (15, cwt)]))
    header_case("indefinite", raw_map(list(prot.items()), indefinite=True))
    header_case("extra-label", cbor2.dumps({**prot, 99: "x"}))
    header_case("missing-kid", cbor2.dumps({k: v for k, v in prot.items() if k != 4}))
    header_case("alg-es256", cbor2.dumps({**prot, 1: -7}))
    header_case("alg-text", cbor2.dumps({**prot, 1: "EdDSA"}))
    header_case("alg-label-text", raw_map([("1", prot[1]), (3, prot[3]), (4, kid), (15, cwt)]))
    header_case("alg-label-bool", raw_map([(True, prot[1]), (3, prot[3]), (4, kid), (15, cwt)]))
    header_case("content-type-other", cbor2.dumps({**prot, 3: "application/cbor"}))
    header_case("content-type-int", cbor2.dumps({**prot, 3: 60}))
    header_case("kid-short", cbor2.dumps({**prot, 4: kid[:31]}))
    header_case("kid-text", cbor2.dumps({**prot, 4: kid.hex()}))
    header_case("kid-other-key", cbor2.dumps({**prot, 4: hashlib.sha256(b"other").digest()}))
    header_case("empty", b"")
    header_case("cwt-extra-claim", cbor2.dumps({**prot, 15: {**cwt, 3: "x"}}))
    header_case("cwt-iss-int", cbor2.dumps({**prot, 15: {1: 7, 2: cwt[2]}}))
    header_case("cwt-sub-mismatch", cbor2.dumps({**prot, 15: {1: cwt[1], 2: cwt[1] + "#1"}}))
    header_case("cwt-label-bool", cbor2.dumps({**prot, 15: {True: cwt[1], 2: cwt[2]}}))
    header_case("cwt-duplicate-iss", raw_map([(1, prot[1]), (3, prot[3]), (4, kid),
                                              (15, ("raw", raw_map([(1, cwt[1]), (1, cwt[1]), (2, cwt[2])])))]))
    header_case("cwt-not-map", cbor2.dumps({**prot, 15: [cwt[1], cwt[2]]}))
    header_case("cwt-iss-not-identifier",
                cbor2.dumps({**prot, 15: {1: "not an id!", 2: "not an id!#" + cwt[2].split("#")[1]}}))

    header_case("alg-ed25519-fully-specified", cbor2.dumps({**prot, 1: -19}))
    header_case("crit-understood", cbor2.dumps({**prot, 2: [1]}))
    header_case("crit-not-understood", cbor2.dumps({**prot, 2: [99], 99: "x"}))
    header_case("crit-empty", cbor2.dumps({**prot, 2: []}))
    header_case("iv-and-partial-iv", cbor2.dumps({**prot, 5: b"\x00" * 16, 6: b"\x00"}))
    header_case("text-label-1-beside-alg", cbor2.dumps({**prot, "1": -8}))
    sig = LOG_KEY.sign(cbor2.dumps(["Signature1", prot_bstr, b"", payload]))
    for name, unprotected in (("crit", {2: [1]}), ("kid-again", {4: kid}), ("partial-iv-beside-protected-iv", {6: b"\x00"}),
                              ("iv-text", {5: "x"})):
        base = cbor2.dumps({**prot, 5: b"\x00" * 16}) if name == "partial-iv-beside-protected-iv" else prot_bstr
        out[f"{label}-unprotected-{name}"] = sign(base, payload, unprotected=unprotected)
    out[f"{label}-outer-payload-nested-indefinite"] = b"\xd2\x84" + cbor2.dumps(prot_bstr) + b"\xa0" + b"\x5f\x5f" + \
        cbor2.dumps(payload) + b"\xff\xff" + cbor2.dumps(sig)
    out[f"{label}-outer-unprotected-noncanonical-empty"] = b"\xd2\x84" + cbor2.dumps(prot_bstr) + b"\xb8\x00" + \
        cbor2.dumps(payload) + cbor2.dumps(LOG_KEY.sign(cbor2.dumps(["Signature1", prot_bstr, b"", payload])))
    out[f"{label}-outer-untagged"] = sign(prot_bstr, payload, tag=None)
    out[f"{label}-outer-tag-17"] = sign(prot_bstr, payload, tag=17)
    out[f"{label}-outer-unprotected-label"] = sign(prot_bstr, payload, unprotected={33: b"x"})
    sig = LOG_KEY.sign(cbor2.dumps(["Signature1", prot_bstr, b"", payload]))
    out[f"{label}-outer-unprotected-null"] = cbor2.dumps(cbor2.CBORTag(18, [prot_bstr, None, payload, sig]))
    out[f"{label}-outer-detached"] = cbor2.dumps(cbor2.CBORTag(18, [prot_bstr, {}, None, sig]))
    out[f"{label}-outer-five-elements"] = sign(prot_bstr, payload, extra=b"")
    out[f"{label}-outer-empty-signature"] = sign(prot_bstr, payload, signature=b"")
    out[f"{label}-outer-bad-signature"] = sign(prot_bstr, payload, signature=bytes(64))
    long_head = b"\x59" + len(payload).to_bytes(2, "big") if len(payload) < 256 else None
    if long_head:
        out[f"{label}-outer-payload-long-length"] = b"\xd2\x84" + cbor2.dumps(prot_bstr) + b"\xa0" + long_head + \
            payload + cbor2.dumps(sig)
    return out


def payload_with(claims: dict, name: str, value_hex: str) -> bytes:
    """The claims map with ``name``'s value replaced by raw CBOR octets."""
    items = [(k, ("raw", bytes.fromhex(value_hex)) if k == name else v) for k, v in claims.items()]
    if name not in claims:
        items.append((name, ("raw", bytes.fromhex(value_hex))))
    return raw_map(items)


def claims_peak(claims: dict) -> bytes:
    return cbor2.loads(claims["commitment"])[0]


def python_verdict(statement: bytes) -> str:
    try:
        read_checkpoint(statement)
    except eb.BookError as exc:
        return str(exc)
    except Exception as exc:  # noqa: BLE001 -- any other failure is a reader defect, reported as such
        return f"UNEXPECTED {type(exc).__name__}: {exc}"
    return "accept"


def main() -> int:
    if sys.argv[1:2] == ["cases"] and len(sys.argv) == 3:
        rows = [{"name": name, "statement_hex": statement.hex(), "python": python_verdict(statement)}
                for name, statement in cases().items()]
        with open(sys.argv[2], "w", encoding="utf-8") as f:
            json.dump(rows, f, indent=1)
        return 0
    if sys.argv[1:2] == ["compare"] and len(sys.argv) == 4:
        rows = json.load(open(sys.argv[2], encoding="utf-8"))
        go = json.load(open(sys.argv[3], encoding="utf-8"))
        wrong, pinned = 0, 0
        for row in rows:
            py, g = row["python"], go[row["name"]]
            py_v, go_v = ("accept" if v == "accept" else "refuse" for v in (py, g))
            departs = departure(row["name"])
            if departs is None:
                ok, mark = py_v == go_v and not py.startswith("UNEXPECTED"), ""
            else:
                spec = departs[0]
                ok = py_v == spec and go_v != spec
                pinned += ok
                mark = f"  [spec-required, Go to fix: {GO_ISSUE}; {departs[1]}]"
            wrong += not ok
            print(f"{'ok  ' if ok else 'FAIL'} {row['name']}: go={go_v} python={py_v}{mark}"
                  + ("" if ok else f"\n     go: {g}\n     py: {py}"))
        print(f"\n{len(rows)} cases: {len(rows) - wrong} as required ({pinned} where Go departs from the spec, "
              f"pinned), {wrong} not")
        return 1 if wrong else 0
    raise SystemExit("usage: checkpoint_parity.py cases OUT | compare CASES GO_VERDICTS")


if __name__ == "__main__":
    sys.exit(main())
