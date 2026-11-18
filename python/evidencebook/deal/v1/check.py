# SPDX-License-Identifier: Apache-2.0
"""A v1 record's strict reading, its checks and its digest, as Go's
``dealprofile/xdealv1`` (``check.go``, ``record.go``).

A refusal matches Go's in its stage and, at the record and agreement
stages, in its text. At the schema stage the detail is ``jsonschema``'s
wording where Go's validator has its own, and at the canonicalization stage
a JCS failure carries the Python JCS implementation's text.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass
from datetime import datetime, timezone
from functools import lru_cache
from pathlib import Path
from typing import Any

from agent_action_capsule.canonical import jcs, json_digest
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

from ..check import StageError, _Float, _go_quote, _go_v, _refuse_constant
from . import gotypes
from .gotypes import ABSENT, GoTypeError
from .names import (
    CANONICALIZATION, CHOICE_CONFIRM, DIGEST_ALG, KIND_AGREEMENT, KIND_DISPUTE, PROFILE, RECORD_REF,
    SIDE_ACCEPTOR, SIDE_OFFERER, TYPE_ADMIT, TYPE_AGENT_MOVE, TYPE_AGREE, TYPE_ANSWER, TYPE_APPROVAL, TYPE_ASK,
    TYPE_CHECK, TYPE_CLAIM, TYPE_COUNTERPARTY_PROFILE, TYPE_DETAIL_CHANGE, TYPE_DISCLOSURE, TYPE_DISPUTE,
    TYPE_EVIDENCE, TYPE_INTENT, TYPE_MESSAGE, TYPE_PERFORM, TYPE_PROPOSE, TYPE_RULES_VERDICT, TYPE_RULING,
    TYPE_SALE, TYPE_SALE_UNPOST, TYPE_SETTLE, TYPE_STATEMENT, TYPE_THREAD, TYPE_THREAD_OPENED, TYPE_VERDICT,
)

__all__ = [
    "STAGE_CANONICALIZATION", "STAGE_SCHEMA", "STAGE_RECORD", "STAGE_DIGEST", "STAGE_AGREEMENT",
    "PLACEMENT_DEAL_BOOK", "PLACEMENT_CASE_BOOK", "PLACEMENT_JOURNAL", "Record", "SealContext", "StageError",
    "check", "check_agreement", "check_dispute", "check_ruling", "check_sealed", "decode", "digest", "encode",
    "placement_of", "profile_schema", "ref_to", "rows_digest",
]

STAGE_CANONICALIZATION = "canonicalization"
STAGE_SCHEMA = "schema"
#: The record checks of design §3: where a record is sealed, deal_id
#: presence, and the rules within one record that the schema does not state.
STAGE_RECORD = "record"
STAGE_DIGEST = "digest"
#: An agreement, dispute or ruling read against another record.
STAGE_AGREEMENT = "agreement"

PLACEMENT_DEAL_BOOK = "deal_book"
PLACEMENT_CASE_BOOK = "case_book"
PLACEMENT_JOURNAL = "journal"

_DEAL_BOOK_TYPES = (TYPE_PERFORM, TYPE_SETTLE, TYPE_STATEMENT, TYPE_DISPUTE, TYPE_RULES_VERDICT)
_NO_DEAL_ID = (TYPE_INTENT, TYPE_ADMIT, TYPE_SALE, TYPE_THREAD, TYPE_THREAD_OPENED, TYPE_SALE_UNPOST,
               TYPE_COUNTERPARTY_PROFILE, TYPE_DISCLOSURE)
_JOURNEY_TYPES = (TYPE_MESSAGE, TYPE_CLAIM, TYPE_EVIDENCE, TYPE_DETAIL_CHANGE, TYPE_AGENT_MOVE)
_ASK_TYPES = (TYPE_ASK, TYPE_ANSWER)

_DATA = Path(__file__).resolve().parent.parent / "_data" / "schemas"


def _err(stage: str, detail: str) -> StageError:
    return StageError(stage, detail)


# -- the record as Go's types hold it --------------------------------------------------


@dataclass(frozen=True)
class Record:
    """A v1 record read into Go's types (Go ``xdealv1.Record``): ``block``
    and ``body`` are the Go values :mod:`gotypes` reads, a ``dict`` per
    struct with every field, zero values included, and ``value`` the
    record's JSON as the types write it."""

    block: dict
    body: dict
    value: Any

    @property
    def record_type(self) -> str:
        return self.block["record_type"]


def _record_of(doc: Any) -> Record:
    """Go ``Record.UnmarshalJSON`` over a decoded value: the two members,
    the block, the body of its type, then the round trip."""
    if not isinstance(doc, dict):
        raise GoTypeError("json: cannot read a non-object into a record")
    for k in doc:
        if k not in (PROFILE, "body"):
            raise GoTypeError(f"json: unknown field {json.dumps(k)}")
    if PROFILE not in doc or "body" not in doc:
        raise GoTypeError("xdealv1: a record has an x-deal-v1 block and a body")
    try:
        block = gotypes.read(doc[PROFILE], gotypes.block_type())
    except GoTypeError as exc:
        raise GoTypeError(f"xdealv1: the x-deal-v1 block: {exc}") from None
    t = gotypes.body_type(block["record_type"])
    if t is None:
        raise GoTypeError(f"xdealv1: record type {_go_quote(block['record_type'])} is not an x-deal-v1 type")
    try:
        body = gotypes.read(doc["body"], t)
    except GoTypeError as exc:
        raise GoTypeError(f"xdealv1: a {block['record_type']} body: {exc}") from None
    value = {PROFILE: gotypes.write(block, gotypes.block_type()), "body": gotypes.write(body, t)}
    if jcs(value) != jcs(doc):
        raise GoTypeError("xdealv1: the record's text does not read as its type: it would encode to other bytes")
    return Record(block, body, value)


def decode(record: Any) -> Record:
    """Reads a v1 record's JSON text (Go ``xdealv1.Decode``): it refuses
    what the text check refuses, a member the types do not name, and text
    the types would not encode back to. It runs no schema or record check:
    :func:`check` does. ``record`` is text (``bytes`` or ``str``) or a
    :class:`Record`."""
    if isinstance(record, Record):
        return record
    return _record_of(_read_text(_raw(record)))


# -- the text --------------------------------------------------------------------------


def _raw(record: Any) -> bytes:
    if isinstance(record, Record):
        record = record.value
    if isinstance(record, (bytes, bytearray)):
        return bytes(record)
    if isinstance(record, str):
        try:
            return record.encode("utf-8")
        except UnicodeEncodeError:
            raise _err(STAGE_CANONICALIZATION, "invalid UTF-8") from None
    return json.dumps(record, ensure_ascii=False, allow_nan=False).encode("utf-8")


def _hex4(b: bytes) -> int:
    r = 0
    for c in b:
        r <<= 4
        if 0x30 <= c <= 0x39:
            r |= c - 0x30
        elif 0x61 <= c <= 0x66:
            r |= c - 0x61 + 10
        elif 0x41 <= c <= 0x46:
            r |= c - 0x41 + 10
    return r


def _check_text(raw: bytes) -> None:
    """Go ``checkText``: invalid UTF-8, an escaped surrogate outside a pair,
    and an object member given twice are refused."""
    try:
        raw.decode("utf-8")
    except UnicodeDecodeError:
        raise _err(STAGE_CANONICALIZATION, "invalid UTF-8") from None
    in_string = False
    i, n = 0, len(raw)
    while i < n:
        c = raw[i]
        if not in_string:
            in_string = c == 0x22
        elif c == 0x22:
            in_string = False
        elif c == 0x5C and i + 1 < n:
            i += 1
            if raw[i] != 0x75 or i + 4 >= n:
                i += 1
                continue
            r = _hex4(raw[i + 1:i + 5])
            i += 4
            if 0xD800 <= r <= 0xDFFF:
                if r >= 0xDC00 or i + 6 >= n or raw[i + 1] != 0x5C or raw[i + 2] != 0x75:
                    raise _err(STAGE_CANONICALIZATION, "an escaped surrogate outside a pair")
                lo = _hex4(raw[i + 3:i + 7])
                if not 0xDC00 <= lo <= 0xDFFF:
                    raise _err(STAGE_CANONICALIZATION, "an escaped surrogate outside a pair")
                i += 6
        i += 1


def _pairs(pairs: list[tuple[str, Any]]) -> dict:
    out: dict = {}
    for k, v in pairs:
        if k in out:
            raise _Repeat(k)
        out[k] = v
    return out


class _Repeat(ValueError):
    def __init__(self, name: str) -> None:
        super().__init__(f"member {_go_quote(name)} given twice")


def _read_text(raw: bytes) -> Any:
    """The text check, then one JSON value with numbers as written."""
    _check_text(raw)
    try:
        return json.loads(raw.decode("utf-8"), object_pairs_hook=_pairs, parse_float=_float_of,
                          parse_constant=_refuse_constant)
    except _Repeat as exc:
        raise _err(STAGE_CANONICALIZATION, str(exc)) from None
    except ValueError as exc:
        raise _err(STAGE_CANONICALIZATION, f"not one JSON value: {exc}") from None


def _float_of(text: str) -> _Float:
    f = _Float(text)
    f.text = text
    return f


# -- the stages ------------------------------------------------------------------------


def check(record: Any) -> None:
    """Runs the stages over ``record`` (canonicalization, schema, record);
    raises the first refusal as a :class:`StageError` (Go ``Check``)."""
    raw = _raw(record)
    _check(_read_text(raw))


def encode(record: Any) -> tuple[bytes, str]:
    """A record's payload, the JCS bytes it seals, and its record digest,
    once it passes :func:`check` (Go ``Encode``)."""
    doc = _read_text(_raw(record))
    _check(doc)
    return jcs(doc), json_digest(doc)


def digest(record: Any) -> str:
    """A record's record digest: lowercase hex SHA-256 over its JCS bytes,
    with no normalization pass (Go ``Digest``)."""
    return json_digest(_read_text(_raw(record)))


def rows_digest(terms_rows: Any) -> str:
    """The rows digest of an offer's terms_rows: SHA-256 over the JCS bytes
    of rows and named together (§4.2). ``terms_rows`` is the Go value of a
    :class:`Record` body or a JSON value as written."""
    return json_digest(_read_text(_raw(terms_rows)))


def ref_to(d: str) -> dict:
    """The digest reference to a record with digest ``d`` (Go ``RefTo``)."""
    return {"type": RECORD_REF, "digest_alg": DIGEST_ALG, "digest": d}


def _check(doc: Any) -> None:
    rec = doc if isinstance(doc, dict) else {}
    blk = rec.get(PROFILE)
    if not isinstance(blk, dict):
        raise _err(STAGE_CANONICALIZATION, "no x-deal-v1 block")
    c = blk.get("canonicalization")
    if not (isinstance(c, str) and c == CANONICALIZATION):
        raise _err(STAGE_CANONICALIZATION, f'canonicalization must be exactly "jcs" (got {_go_v(c)}); fail closed')
    _no_floats(doc, "$")
    try:
        jcs(doc)
    except Exception as exc:  # noqa: BLE001 - every JCS refusal is this stage's
        raise _err(STAGE_CANONICALIZATION, f"not JCS-serializable: {exc}") from None
    _stage_schema(doc)
    try:
        r = _record_of(doc)
    except GoTypeError as exc:
        raise _err(STAGE_SCHEMA, str(exc)) from None
    _check_record(r)


def _no_floats(v: Any, path: str) -> None:
    if isinstance(v, _Float):
        raise _err(STAGE_CANONICALIZATION, f"a float at {path}; numbers are integers")
    if isinstance(v, dict):
        for k in sorted(v):
            _no_floats(v[k], f"{path}.{k}")
    elif isinstance(v, list):
        for i, e in enumerate(v):
            _no_floats(e, f"{path}[{i}]")


def profile_schema() -> bytes:
    """The raw x-deal-v1 record schema."""
    return (_DATA / "x-deal-v1.schema.json").read_bytes()


def _go_patterns(v: Any) -> Any:
    """The schema with each pattern's ``$`` anchored at the very end, as Go's
    validator reads it: Python's ``$`` also matches before a final newline."""
    if isinstance(v, dict):
        out = {k: _go_patterns(e) for k, e in v.items()}
        p = out.get("pattern")
        if isinstance(p, str) and p.endswith("$") and not p.endswith("\\$"):
            out["pattern"] = p[:-1] + r"\Z"
        return out
    if isinstance(v, list):
        return [_go_patterns(e) for e in v]
    return v


@lru_cache(maxsize=None)
def _validator() -> Draft202012Validator:
    v0 = _go_patterns(json.loads((_DATA / "x-deal-v0.schema.json").read_bytes()))
    v1 = _go_patterns(json.loads(profile_schema()))
    registry = Registry().with_resources([
        ("https://agentactioncapsule.org/profiles/x-deal-v0/record.schema.json", Resource.from_contents(v0)),
        ("https://agentactioncapsule.org/profiles/x-deal-v1/record.schema.json", Resource.from_contents(v1)),
    ])
    return Draft202012Validator(v1, registry=registry)


@lru_cache(maxsize=None)
def _v0_body_validator(name: str) -> Draft202012Validator:
    v0 = _go_patterns(json.loads((_DATA / "x-deal-v0.schema.json").read_bytes()))
    schema = {"$ref": f"https://agentactioncapsule.org/profiles/x-deal-v0/record.schema.json#/$defs/{name}"}
    registry = Registry().with_resource(
        "https://agentactioncapsule.org/profiles/x-deal-v0/record.schema.json", Resource.from_contents(v0))
    return Draft202012Validator(schema, registry=registry)


def _leaves(e: Any, out: list[str]) -> None:
    if e.context:
        for c in e.context:
            _leaves(c, out)
        return
    out.append("/" + "/".join(str(p) for p in e.absolute_path) + ": " + e.message[:200])


def _schema_errors(validator: Draft202012Validator, doc: Any) -> list[str]:
    errs = sorted(validator.iter_errors(doc), key=lambda e: [str(p) for p in e.absolute_path])
    leaves: list[str] = []
    for e in errs:
        _leaves(e, leaves)
    return leaves


def _stage_schema(doc: Any) -> None:
    leaves = _schema_errors(_validator(), doc)
    if leaves:
        raise StageError(STAGE_SCHEMA, leaves[0], leaves)


# -- where a record is sealed ----------------------------------------------------------


def placement_of(record: Any) -> str:
    """Where a record of this type and block is sealed (§3.2, Go
    ``PlacementOf``): an agreement, perform, settle, statement, dispute and
    rules_verdict in a deal book; a ruling in a case book; an ask or answer
    in a deal book when it has a seq, else in the journal; every other
    record in the journal."""
    r = decode(record)
    t = r.record_type
    if t == TYPE_RULING:
        return PLACEMENT_CASE_BOOK
    if t in _DEAL_BOOK_TYPES:
        return PLACEMENT_DEAL_BOOK
    if t == TYPE_AGREE:
        return PLACEMENT_DEAL_BOOK if r.body["kind"] == KIND_AGREEMENT else PLACEMENT_JOURNAL
    if t in _ASK_TYPES and r.block["seq"] > 0:
        return PLACEMENT_DEAL_BOOK
    return PLACEMENT_JOURNAL


def _check_record(r: Record) -> None:
    b, t = r.block, r.record_type
    place = placement_of(r)
    seen_present = b["seen"] is not ABSENT
    if place in (PLACEMENT_DEAL_BOOK, PLACEMENT_CASE_BOOK):
        if b["seq"] < 1:
            raise _err(STAGE_RECORD, f"a {t} record in a {place} carries its seq")
        if (b["prev"] is not None) != (b["seq"] > 1):
            raise _err(STAGE_RECORD, "a book record carries prev exactly when its seq is past 1")
        if place == PLACEMENT_CASE_BOOK:
            if seen_present or b["agreement_ref"] is not None:
                raise _err(STAGE_RECORD, "a ruling carries no seen and no agreement_ref")
        else:
            if not seen_present:
                raise _err(STAGE_RECORD,
                           "a deal-book record carries seen (null when nothing is held from the other side)")
            if b["refs"] is not None:
                raise _err(STAGE_RECORD,
                           "a deal-book record carries no refs: it cites only through its type's own members")
            if (b["agreement_ref"] is not None) != (b["seq"] > 1):
                raise _err(STAGE_RECORD, "a deal-book record carries agreement_ref exactly when its seq is past 1")
            if t == TYPE_AGREE and r.body["kind"] == KIND_AGREEMENT and b["seq"] != 1:
                raise _err(STAGE_RECORD, "an agreement opens its deal book: seq 1")
            if t != TYPE_AGREE and b["seq"] == 1:
                raise _err(STAGE_RECORD, f"a deal book opens with its agreement, not a {t}")
    else:
        if b["seq"] != 0 or b["prev"] is not None or b["agreement_ref"] is not None or seen_present:
            raise _err(STAGE_RECORD, "a journal record carries no seq, prev, agreement_ref or seen, so a record "
                                     "sent whole shows no place in the journal")
        if t == TYPE_PROPOSE and b["refs"] is not None:
            raise _err(STAGE_RECORD, "an offer, sent whole, carries no refs: it cites only through counter_to")
        if t == TYPE_AGREE and (b["refs"] is not None or b["marks"] is not None or b["counterparty"] is not None):
            raise _err(STAGE_RECORD, f"a {r.body['kind']} carries no refs, marks or counterparty fingerprint: it "
                                     "names nothing beyond the offer it answers")
    _check_dates(r)
    _check_deal_id(r, place)
    _check_body(r)


def _check_deal_id(r: Record, place: str) -> None:
    t, has = r.record_type, r.block["deal_id"] != ""
    required = absent = False
    if place != PLACEMENT_JOURNAL:
        required = True
    elif t in _JOURNEY_TYPES or t in _ASK_TYPES:
        pass
    elif t in _NO_DEAL_ID:
        absent = True
    elif t == TYPE_APPROVAL:
        if r.body["choice"] == CHOICE_CONFIRM:
            absent = True
        else:
            required = True
    else:
        required = True
    if required and not has:
        raise _err(STAGE_RECORD, f"a {t} record carries deal_id")
    if absent and has:
        raise _err(STAGE_RECORD, f"a {t} record carries no deal_id: it predates or spans deals")


def _check_body(r: Record) -> None:
    t, body = r.record_type, r.body
    if t == TYPE_PROPOSE:
        _check_witnesses(body["witnesses"])
        _check_forums(body["adjudicator"], body["forums"])
        _check_rows(body["terms_rows"])
    elif t == TYPE_AGREE:
        _check_witnesses(body["witnesses"])
        if body["kind"] == KIND_AGREEMENT:
            _check_forums(body["adjudicator"], body["forums"])
            layer = body["counterparty_layer"] is True
            seen_null = r.block["seen"] is None
            key_null = body["counterparty_key"] is None
            if not layer and (not seen_null or not key_null):
                raise _err(STAGE_RECORD, "an agreement with a counterparty that runs no layer has seen null and "
                                         "counterparty_key null")
            if layer and key_null:
                raise _err(STAGE_RECORD, "an agreement with a counterparty that runs the layer names its record key "
                                         "in counterparty_key")
            if body["side"] == SIDE_ACCEPTOR and not seen_null:
                raise _err(STAGE_RECORD, "the acceptor's agreement comes first: its seen is null")
    elif t == TYPE_CHECK:
        _check_v0_body(r, ("payment_ref", "nonce"), "body_check")
    elif t == TYPE_VERDICT:
        _check_v0_body(r, ("nonce",), "body_verdict")
    elif t == TYPE_APPROVAL:
        _check_v0_body(r, ("nonce",), "body_approval")


def _check_v0_body(r: Record, own: tuple[str, ...], name: str) -> None:
    """Go ``checkV0Body``: the body without v1's own members, as Go writes
    the embedded v0 struct, against v0's closed ``$defs/<name>``."""
    v0 = {k: v for k, v in r.value["body"].items() if k not in own}
    leaves = _schema_errors(_v0_body_validator(name), v0)
    if leaves:
        raise _err(STAGE_SCHEMA, f"the {name.removeprefix('body_')} body is not v0's {name}: {leaves[0]}")


def _check_dates(r: Record) -> None:
    times = [r.block["at"]]
    if r.record_type == TYPE_PROPOSE:
        body = r.body
        times += [body["stated_at"], body["agree_by"]]
        for row in body["terms_rows"]["rows"] or ():
            times += [row["due"]["at"], row["window_end"]["at"]]
        for n in (body["terms_rows"]["named"] or {}).values():
            times.append(n["latest"])
    elif r.record_type == TYPE_RULING and r.body["remedy"] is not None:
        times.append(r.body["remedy"]["due"])
    for s in times:
        if s != "" and not _utc_second(s):
            raise _err(STAGE_RECORD, f"{_go_quote(s)} is not a UTC time to the second that exists")


_RFC3339 = re.compile(r"(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(\.\d+)?([Zz]|[+-]\d{2}:\d{2})\Z",
                      re.ASCII)


def _utc_second(s: str) -> bool:
    """Whether Go's ``time.Parse(time.RFC3339, s)`` reads ``s`` and its UTC
    form, printed in RFC 3339, is ``s`` (a year of 1 or later): RFC 3339
    UTC to the second, naming a time that exists."""
    m = _RFC3339.match(s)
    if not m or m.group(7) or m.group(8) != "Z" or s[10] != "T":
        return False
    y, mo, d, h, mi, sec = (int(m.group(i)) for i in range(1, 7))
    if y < 1:
        return False
    try:
        datetime(y, mo, d, h, mi, sec, tzinfo=timezone.utc)
    except ValueError:
        return False
    return True


def _check_forums(adjudicator: Any, forums: Any) -> None:
    none = adjudicator is None
    if none and forums:
        raise _err(STAGE_RECORD, "an offer or agreement with no adjudicator names no forum")
    if not none and not forums:
        raise _err(STAGE_RECORD, "an offer or agreement with an adjudicator names the forums both sides accept")


def _check_witnesses(ws: Any) -> None:
    seen: set[str] = set()
    for w in ws or ():
        if w["key_id"] in seen:
            raise _err(STAGE_RECORD, f"witness {w['key_id']} is named twice")
        seen.add(w["key_id"])


def _witness_keys(ws: Any) -> list[str]:
    return sorted(w["key_id"] for w in ws or ())


def _check_rows(tr: dict) -> None:
    seen: set[str] = set()
    named = tr["named"] or {}
    for row in tr["rows"] or ():
        if row["id"] in seen:
            raise _err(STAGE_RECORD, f"row {row['id']} is given twice")
        seen.add(row["id"])
        for d in (row["due"], row["window_end"]):
            if d["after"] != "" and d["after"] not in named:
                raise _err(STAGE_RECORD, f"row {row['id']} hangs on {d['after']}, which is not named")


# -- the seal context ------------------------------------------------------------------


@dataclass(frozen=True)
class SealContext:
    """What the holder knows when it seals a journal record that the record
    alone does not show (§3.1): whether the offer of its deal exists, and
    whether an ask or answer is of an admission decision."""

    offer_exists: bool = False
    admission: bool = False


def check_sealed(record: Any, ctx: SealContext) -> None:
    """:func:`check`, and then §3.1's deal_id rule for the journal records
    whose presence depends on when they are sealed: a journey record carries
    deal_id exactly when its offer exists, and an ask or answer in the
    journal exactly when it is not of an admission (Go ``CheckSealed``)."""
    check(record)
    r = _record_of(_read_text(_raw(record)))
    if placement_of(r) != PLACEMENT_JOURNAL:
        return
    t, has = r.record_type, r.block["deal_id"] != ""
    if t in _JOURNEY_TYPES:
        want, when = ctx.offer_exists, "when its offer exists"
    elif t in _ASK_TYPES:
        want, when = not ctx.admission, "between the two parties of a deal"
    else:
        return
    if has != want:
        raise _err(STAGE_RECORD, f"a {t} record sealed here carries deal_id only {when}")


# -- one record read against another ---------------------------------------------------


def check_dispute(dispute: Any, agreement: Any) -> None:
    """Reads a dispute of kind dispute against the agreement of its deal
    book (Go ``CheckDispute``): it goes to the adjudicator the agreement
    names, in one of the forums the agreement accepts, and its agreement_ref
    cites this agreement."""
    d, a = decode(dispute), decode(agreement)
    if d.record_type != TYPE_DISPUTE or d.body["kind"] != KIND_DISPUTE:
        raise _err(STAGE_AGREEMENT, "not a dispute of kind dispute")
    if a.record_type != TYPE_AGREE or a.body["kind"] != KIND_AGREEMENT:
        raise _err(STAGE_AGREEMENT, "not an agreement")
    if d.block["deal_id"] != a.block["deal_id"]:
        raise _err(STAGE_AGREEMENT, "the dispute is of another deal")
    ref = d.block["agreement_ref"]
    if ref is None or ref["digest"] != digest(a):
        raise _err(STAGE_AGREEMENT, "the dispute is bound to another agreement (agreement_ref)")
    adj = a.body["adjudicator"]
    if adj is None or adj is ABSENT:
        raise _err(STAGE_AGREEMENT, "the agreement names no adjudicator: no dispute is possible")
    if d.body["adjudicator"] != adj["key_id"]:
        raise _err(STAGE_AGREEMENT, "the dispute goes to another adjudicator than the agreement names")
    if d.body["forum"] not in (a.body["forums"] or ()):
        raise _err(STAGE_AGREEMENT, f"the dispute's forum {d.body['forum']} is not one the agreement accepts")


def check_ruling(ruling: Any, agreement: Any) -> None:
    """Reads a ruling against the agreement of the deal: it rules in one of
    the forums the agreement accepts (Go ``CheckRuling``)."""
    r, a = decode(ruling), decode(agreement)
    if r.record_type != TYPE_RULING:
        raise _err(STAGE_AGREEMENT, "not a ruling")
    if a.record_type != TYPE_AGREE or a.body["kind"] != KIND_AGREEMENT:
        raise _err(STAGE_AGREEMENT, "not an agreement")
    if r.block["deal_id"] != a.block["deal_id"]:
        raise _err(STAGE_AGREEMENT, "the ruling is of another deal")
    if r.body["forum"] not in (a.body["forums"] or ()):
        raise _err(STAGE_AGREEMENT, f"the ruling's forum {r.body['forum']} is not one the agreement accepts")


def check_agreement(agreement: Any, offer: Any, held_acceptor_agreement: str) -> None:
    """Reads an agreement against the offer it cites (§3.3, §3.4; Go
    ``CheckAgreement``), after :func:`check` on both: the offer is a propose
    of the same deal whose record digest the agreement cites; the rows
    digest, the witnesses, the adjudicator and the forums are the offer's;
    the role is the offerer's on the offerer's side and the other on the
    acceptor's. ``held_acceptor_agreement`` is the record id of the
    acceptor's agreement the holder holds, which an offerer's agreement with
    a counterparty that runs the layer names in seen."""
    ag_r, of_r = decode(agreement), decode(offer)
    for r in (ag_r, of_r):
        check(r)
    ag = ag_r.body
    if ag_r.record_type != TYPE_AGREE or ag["kind"] != KIND_AGREEMENT:
        raise _err(STAGE_AGREEMENT, "not an agreement")
    if of_r.record_type != TYPE_PROPOSE:
        raise _err(STAGE_AGREEMENT, f"the agreement cites a {of_r.record_type}, not an offer")
    of = of_r.body
    if ag["offer"] != ref_to(digest(of_r)):
        raise _err(STAGE_AGREEMENT, "the agreement does not cite this offer's record digest")
    if ag_r.block["deal_id"] != of_r.block["deal_id"]:
        raise _err(STAGE_AGREEMENT, "the agreement's deal_id is not the offer's")
    rows = json_digest(gotypes.write(of["terms_rows"], gotypes.terms_rows_type()))
    if ag["rows_digest"] != rows:
        raise _err(STAGE_AGREEMENT, "rows_digest is not the offer's rows digest")
    if _witness_keys(ag["witnesses"]) != _witness_keys(of["witnesses"]):
        raise _err(STAGE_AGREEMENT, "the agreement's witnesses are not the offer's")
    if not _same_adjudicator(ag["adjudicator"], of["adjudicator"]):
        raise _err(STAGE_AGREEMENT, "the agreement's adjudicator is not the offer's")
    if sorted(ag["forums"] or ()) != sorted(of["forums"] or ()):
        raise _err(STAGE_AGREEMENT, "the agreement's forums are not the offer's")
    if ag["side"] == SIDE_OFFERER:
        if ag["role"] != of["role"]:
            raise _err(STAGE_AGREEMENT, "the offerer's role is the offer's")
        layer = ag["counterparty_layer"] is True
        seen = ag_r.block["seen"]
        if layer and (seen is None or seen is ABSENT or seen != held_acceptor_agreement):
            raise _err(STAGE_AGREEMENT, "the offerer's agreement names the acceptor's agreement it holds in seen")
    elif ag["side"] == SIDE_ACCEPTOR:
        if ag["role"] == of["role"]:
            raise _err(STAGE_AGREEMENT, "the acceptor's role is the other side of the offer's")


def _same_adjudicator(a: Any, b: Any) -> bool:
    if a is ABSENT or b is ABSENT:
        return (a is ABSENT) == (b is ABSENT)
    if a is None or b is None:
        return a is None and b is None
    return a == b
