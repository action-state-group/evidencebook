# SPDX-License-Identifier: Apache-2.0
"""Records that leave the holder, and their checks (design §3.1, §3.5,
§6.3, §8.3), as Go's ``dealprofile/xdealv1`` ``sent.go``, ``receive.go``
(``BookPair``, ``VerifyBook``) and ``payment.go`` (``TermsLeg``,
``PaymentRows``) read them:

- a sent pair: an offer, a decline or a cancellation sent whole, as a
  standalone Capsule with its signature and key_id beside the record
  (:func:`pair`, :func:`verify_sent`);
- a book pair: a deal-book record as its book sealed it, with its header
  and payload (:func:`book_pair`, :func:`verify_book`).

Pairs are written as Go's ``json.Marshal`` writes a map of raw members:
keys in byte order, each member's text as written, compacted, with
``<``, ``>``, ``&``, U+2028 and U+2029 escaped.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any

from agent_action_capsule.canonical import jcs
from capsule_emit.produce import verify_capsule, verify_envelope

from ..check import StageError, _go_quote
from . import golenient, goscan, gotypes, rawjson
from .check import Record, _read_text, check, decode, digest, ref_to
from .check import PLACEMENT_JOURNAL, placement_of
from .names import KIND_CANCELLATION, KIND_DECLINE, TYPE_AGREE, TYPE_PROPOSE
from .status import owed_amount

__all__ = [
    "SENT_SUFFIX", "SENT_LOG_PREFIX", "SETTLEMENT_MEMBER", "SETTLEMENT_VERSION", "STAGE_SENT", "STAGE_BOOK",
    "BookRead", "OfferContext", "Sent", "book_pair", "pair", "payment_rows", "terms_leg", "verify_book",
    "verify_sent",
]

#: A sent Capsule's action_id is its record's deal_id followed by this.
SENT_SUFFIX = "/sent"
#: The log id prefix a received sent record is held under.
SENT_LOG_PREFIX = "x-deal-v1/sent/"
STAGE_SENT = "sent"
STAGE_BOOK = "book"
#: The Capsule member a settlement leg travels in (AAC settlement.Member).
SETTLEMENT_MEMBER = "settlement"
SETTLEMENT_VERSION = "0"
_RECORD_HEADER_MEMBER = "evidence_record_header"
_FYI = "fyi"


def _err(stage: str, detail: str) -> StageError:
    return StageError(stage, detail)


# -- Go's json.Marshal of a map of raw members -----------------------------------------


def _compact(text: str) -> str:
    """Go's ``compact`` with HTML escaping, as ``json.Marshal`` writes a
    ``json.RawMessage``: white space between tokens dropped, each string as
    written but ``<``, ``>``, ``&``, U+2028 and U+2029 escaped."""
    out, i, n = [], 0, len(text)
    while i < n:
        c = text[i]
        if c == '"':
            j = i + 1
            buf = ['"']
            while j < n:
                d = text[j]
                if d == "\\":
                    buf.append(text[j:j + 2])
                    j += 2
                    continue
                if d == '"':
                    buf.append('"')
                    j += 1
                    break
                buf.append({"<": "\\u003c", ">": "\\u003e", "&": "\\u0026", " ": "\\u2028",
                            " ": "\\u2029"}.get(d, d))
                j += 1
            out.append("".join(buf))
            i = j
        elif c in " \t\n\r":
            i += 1
        else:
            out.append(c)
            i += 1
    return "".join(out)


def _members(text: str) -> dict[str, str] | None:
    """``json.Unmarshal`` into ``map[string]json.RawMessage``
    (:func:`.rawjson.unmarshal_map`)."""
    try:
        return rawjson.unmarshal_map(text)
    except rawjson.GoJSONError as exc:
        raise ValueError(str(exc)) from None


def _marshal_members(members: dict[str, str]) -> bytes:
    """``json.Marshal(map[string]json.RawMessage)``: keys in byte order."""
    parts = [f"{_compact(json.dumps(k, ensure_ascii=False))}:{_compact(members[k])}"
             for k in sorted(members, key=lambda k: k.encode("utf-8"))]
    return ("{" + ",".join(parts) + "}").encode("utf-8", "surrogateescape")


def _text(v: Any) -> str:
    return rawjson.raw_text(bytes(v)) if isinstance(v, (bytes, bytearray)) else v


def _string(raw: str | None) -> str | None:
    """``json.Unmarshal(raw, &s)`` into a string: ``None`` where Go errs
    (no member, text that is not one value, or not a string); a null leaves
    it empty. Read by Go's scanner, so any nesting reads as Go reads it."""
    if raw is None:
        return None
    b = raw.encode("utf-8", "surrogateescape")
    if not goscan.valid(b):
        return None
    v = b.strip(b" \t\r\n")
    if v == b"null":
        return ""
    return goscan.go_unquote(v) if v.startswith(b'"') else None


# -- signing ---------------------------------------------------------------------------


def _signed(capsule: bytes, envelope: bytes) -> dict[str, str]:
    """The Capsule's members with its signature and key_id beside them (Go
    ``signedCapsule``)."""
    text = _text(capsule)
    try:
        members = rawjson.unmarshal_map(text)
        # Go reads the id into struct{CapsuleID string}: the member in any case.
        capsule_id = rawjson.string_member(text, "capsule_id")
    except rawjson.GoJSONError as exc:
        raise ValueError(f"xdealv1: the Capsule is not a JSON object: {exc}") from None
    if members is None:
        # Go writes into the nil map Unmarshal leaves for null, which panics.
        raise ValueError("xdealv1: the Capsule is null")
    try:
        verified = verify_envelope(capsule_id, bytes(envelope))
    except Exception as exc:  # noqa: BLE001 - the envelope's refusal
        raise ValueError(f"xdealv1: the envelope: {exc}") from None
    members["signature"] = json.dumps(bytes(envelope).hex())
    members["key_id"] = json.dumps(bytes(verified.public_key).hex())
    return members


def pair(capsule: bytes, envelope: bytes, payload: bytes) -> bytes:
    """The sent pair of a standalone Capsule (Go ``Pair``):
    ``{"agent_input": payload, "record": the Capsule with its signature
    and key_id}``."""
    members = _signed(capsule, envelope)
    return _marshal_members({"record": _marshal_members(members).decode("utf-8"),
                             "agent_input": _raw_message(payload)})


def book_pair(capsule: bytes, envelope: bytes, header: bytes, payload: bytes) -> bytes:
    """The book pair of a sealed book record (Go ``BookPair``):
    ``{"header", "payload", "record": the Capsule with its signature and
    key_id}``."""
    members = _signed(capsule, envelope)
    return _marshal_members({"record": _marshal_members(members).decode("utf-8"), "header": _raw_message(header),
                             "payload": _raw_message(payload)})


def _raw_message(v: Any) -> str:
    """A ``json.RawMessage`` as ``json.Marshal`` takes it: nil writes null,
    and text that is not one JSON value is refused."""
    if v is None:
        return "null"
    text = _text(v)
    # Inside the pair's map: Go's json.Marshal refuses it nested 10,000 deep.
    if not rawjson.valid(text, depth=9999):
        raise ValueError("json: error calling MarshalJSON for type json.RawMessage: invalid JSON")
    return text


def _verify_capsule(stage: str, members: dict[str, str]) -> tuple[bytes, str, str]:
    """A signed Capsule's JCS bytes, id and key (Go ``verifyCapsule``)."""
    sig, key_id = _string(members.get("signature")), _string(members.get("key_id"))
    if not sig or not key_id:
        raise _err(stage, "the Capsule carries no signature and key_id")
    rest = {k: v for k, v in members.items() if k not in ("signature", "key_id")}
    try:
        capsule = jcs(_read_text(_marshal_members(rest)))
    except StageError as exc:
        raise _err(stage, f"the Capsule: {exc.detail}") from None
    except Exception as exc:  # noqa: BLE001 - JCS refuses it
        raise _err(stage, f"the Capsule: {exc}") from None
    try:
        result = verify_capsule(capsule)
    except Exception as exc:  # noqa: BLE001 - the verifier's refusal
        raise _err(stage, f"the Capsule does not verify: {exc}") from None
    if not result.ok or result.capsule_id is None:
        raise _err(stage, "the Capsule does not verify: <nil>")
    # Go's hex.DecodeString: an even number of hex digits and nothing else.
    if len(sig) % 2 or not all(c in "0123456789abcdefABCDEF" for c in sig):
        raise _err(stage, "the signature is not hex")
    envelope = bytes.fromhex(sig)
    try:
        verified = verify_envelope(result.capsule_id, envelope)
    except Exception as exc:  # noqa: BLE001 - the envelope's refusal
        raise _err(stage, f"the signature does not verify: {exc}") from None
    if bytes(verified.public_key).hex() != key_id:
        raise _err(stage, "the Capsule is signed by another key than its key_id")
    return capsule, result.capsule_id, key_id


def _agent_input_digest(capsule: bytes) -> str:
    c = json.loads(capsule)
    v = (((c.get("model_attestation") or {}).get("compute_attestation") or {}).get("agent_input_digest"))
    return v if isinstance(v, str) else ""


def _pair_members(raw: bytes, stage: str, names: tuple[str, ...], what: str) -> dict[str, Any]:
    """The pair's members as Go reads them: its text check, then
    ``strictUnmarshal`` into a struct of these fields, ``record`` a map of
    raw members and the others raw (:mod:`.rawjson`)."""
    text = rawjson.text_check(bytes(raw))
    try:
        out = rawjson.strict_struct(text, {n: "map" if n == "record" else "raw" for n in names})
    except rawjson.GoJSONError as exc:
        raise _err(stage, f"not a {what}: {exc}") from None
    if any(out[n] is None for n in names):
        raise _err(stage, f"not a {what}: <nil>")
    return out


def _record_members(raw: bytes) -> dict[str, str]:
    """A verified pair's record members, as Go's ``json.Unmarshal`` into
    ``struct{Record map[string]json.RawMessage}`` reads them."""
    out = rawjson.strict_struct(bytes(raw).decode("utf-8"), {"record": "map", "agent_input": "raw", "header": "raw",
                                                                 "payload": "raw"})
    return out["record"] or {}


# -- sent pairs ------------------------------------------------------------------------


@dataclass(frozen=True)
class Sent:
    """A sent record read (Go ``Sent``): the record, its record digest, the
    Capsule's id and the key that signed it."""

    record: Record
    digest: str
    capsule_id: str
    key_id: str


@dataclass(frozen=True)
class OfferContext:
    """The offer a decline or cancellation answers, and the key that signed
    the offer's sent Capsule."""

    offer: Any
    signer: str


def verify_sent(raw: bytes, offer: OfferContext | None = None) -> Sent:
    """Reads a sent pair (Go ``VerifySent``): the Capsule verifies and is
    signed by its key_id; it is a standalone fyi Capsule with no chain,
    references or record header; its agent_input_digest is the record's
    digest; the record passes :func:`.check.check`; its settlement member is
    its terms leg exactly when it is an offer with one payment row; its
    action_id is its deal_id followed by ``/sent``; and it is an offer, a
    decline or a cancellation. Given the offer, a decline must be signed by
    the key the offer was sent to and a cancellation by the offer's
    signer."""
    raw = bytes(raw)
    p = _pair_members(raw, STAGE_SENT, ("record", "agent_input"), "pair {record, agent_input}")
    members, agent_input = p["record"], p["agent_input"]
    capsule, capsule_id, key_id = _verify_capsule(STAGE_SENT, members)
    c = json.loads(capsule)
    chain = members.get("chain")
    chained = chain is not None and chain.strip() != "null"
    refs = c.get("references")
    if _RECORD_HEADER_MEMBER in members or chained or c.get("action_type") != _FYI or (isinstance(refs, list) and refs):
        raise _err(STAGE_SENT, "not a standalone sent Capsule: fyi, no chain, no references, no evidence_record_header")
    payload = agent_input.encode("utf-8")
    try:
        d = digest(payload)
    except StageError as exc:
        raise _err(STAGE_SENT, f"agent_input: {exc}") from None
    if _agent_input_digest(capsule) != d:
        raise _err(STAGE_SENT, "agent_input is not the record the Capsule commits (agent_input_digest)")
    check(payload)
    r = decode(payload)
    _check_terms_member(members.get(SETTLEMENT_MEMBER), r)
    action_id = c.get("action_id") if isinstance(c.get("action_id"), str) else ""
    if action_id != r.block["deal_id"] + SENT_SUFFIX:
        raise _err(STAGE_SENT, f"action_id {_go_quote(action_id)} is not the record's deal_id followed by {SENT_SUFFIX}")
    is_agree = r.record_type == TYPE_AGREE
    kind = r.body["kind"] if is_agree else ""
    if not (r.record_type == TYPE_PROPOSE or (is_agree and kind in (KIND_DECLINE, KIND_CANCELLATION))):
        raise _err(STAGE_SENT, f"a {r.record_type} is not sent whole: only an offer, a decline or a cancellation is")
    out = Sent(r, d, capsule_id, key_id)
    if offer is None or not is_agree:
        return out
    check(offer.offer)
    of = decode(offer.offer)
    if of.record_type != TYPE_PROPOSE:
        raise _err(STAGE_SENT, f"the offer given is a {of.record_type}")
    if r.body["offer"] != ref_to(digest(of)) or r.block["deal_id"] != of.block["deal_id"]:
        raise _err(STAGE_SENT, f"the {kind} does not answer this offer")
    if kind == KIND_DECLINE:
        to = of.body["to"]
        if not isinstance(to, str) or to != key_id:
            raise _err(STAGE_SENT, "a decline is signed by the key the offer was sent to")
    elif kind == KIND_CANCELLATION and offer.signer != key_id:
        raise _err(STAGE_SENT, "a cancellation is signed by the offer's signer")
    return out


# -- the terms leg ---------------------------------------------------------------------


def _is_payment_row(row: dict) -> bool:
    return row["recomputed"] and owed_amount(row["owed"]) is not None


def payment_rows(terms_rows: Any) -> list[str]:
    """The ids of the payment rows of an offer's terms, in order (Go
    ``PaymentRows``): the recomputed rows whose owed is exactly
    ``{"amount_minor", "currency"}`` in a currency with a known scale.
    ``terms_rows`` is the Go value of a decoded offer's ``terms_rows``."""
    return [r["id"] for r in terms_rows["rows"] or () if _is_payment_row(r)]


def _role(party: str, owed_by: str) -> str:
    return "payer" if party == owed_by else "payee"


def terms_leg(offer: Any) -> bytes:
    """The terms leg an offer's sent Capsule carries as its settlement
    member, as JCS bytes (Go ``TermsLeg``): version "0", leg terms, the
    offerer's role in the payment and the payment row's owed as its amount.
    It refuses (``ValueError``) an offer whose terms have no payment row or
    more than one."""
    r = offer if isinstance(offer, Record) else decode(offer)
    if r.record_type != TYPE_PROPOSE:
        raise ValueError(f"xdealv1: a terms leg is an offer's; this is a {r.record_type}")
    found = [row for row in r.body["terms_rows"]["rows"] or () if _is_payment_row(row)]
    if len(found) != 1:
        raise ValueError(f"xdealv1: a terms leg carries one payment's amount; the terms have {len(found)} payment rows")
    row = found[0]
    return jcs({"version": SETTLEMENT_VERSION, "leg": "terms", "sealer_role": _role(r.body["role"], row["owed_by"]),
                "amount": owed_amount(row["owed"])})


def _check_terms_member(member: str | None, r: Record) -> None:
    """Go ``checkTermsMember``: an offer with one payment row carries its
    terms leg as its settlement member, and no other sent record carries
    one."""
    if r.record_type != TYPE_PROPOSE or len(payment_rows(r.body["terms_rows"])) != 1:
        if member is not None:
            raise _err(STAGE_SENT, f"this sent {r.record_type} carries no settlement member; only an offer with one "
                                   "payment row carries its terms leg")
        return
    try:
        want = terms_leg(r)
    except ValueError as exc:
        raise _err(STAGE_SENT, f"the terms leg: {exc}") from None
    if member is None:
        raise _err(STAGE_SENT, "an offer with one payment row carries its terms leg as the settlement member")
    try:
        got = jcs(_read_text(member.encode("utf-8")))
    except StageError as exc:
        raise _err(STAGE_SENT, f"the settlement member: {exc.detail}") from None
    except Exception:  # noqa: BLE001 - not JCS-serializable
        got = None
    if got != want:
        raise _err(STAGE_SENT, f"the settlement member is not the offer's terms leg {want.decode('utf-8')}")


# -- book pairs ------------------------------------------------------------------------


@dataclass(frozen=True)
class BookRead:
    """A book pair read (Go ``BookRead``): the record, its record digest,
    the book record's id, the key that signed it, its book's log id, its seq
    and the record id it cites before it (``""`` at seq 1)."""

    record: Record
    digest: str
    record_id: str
    key_id: str
    log_id: str
    seq: int
    prev: str


def verify_book(raw: bytes) -> BookRead:
    """Reads a book pair (Go ``VerifyBook``): the Capsule verifies, is
    signed by its key_id and commits the header (its agent_input_digest is
    the header's JCS digest); the header commits the record as its one
    payload; the record passes :func:`.check.check`, is sealed in a book,
    and its seq is its header's; the first record cites none before it,
    each later one exactly the record before it."""
    raw = bytes(raw)
    p = _pair_members(raw, STAGE_BOOK, ("record", "header", "payload"), "book pair {record, header, payload}")
    members = p["record"]
    if _RECORD_HEADER_MEMBER in members:
        raise _err(STAGE_BOOK, "the Capsule carries its header inline; a book record commits it")
    capsule, record_id, key_id = _verify_capsule(STAGE_BOOK, members)
    try:
        hjcs = jcs(_read_text(p["header"].encode("utf-8")))
    except StageError as exc:
        raise _err(STAGE_BOOK, f"the header: {exc.detail}") from None
    except Exception as exc:  # noqa: BLE001 - not JCS-serializable
        raise _err(STAGE_BOOK, f"the header: {exc}") from None
    if _agent_input_digest(capsule) != hashlib.sha256(hjcs).hexdigest():
        raise _err(STAGE_BOOK, "the Capsule does not commit this header")
    try:
        h = golenient.read(json.loads(hjcs), gotypes.table()["header"], {})
    except golenient.GoDecodeError as exc:
        raise _err(STAGE_BOOK, f"the header: {exc}") from None
    payload = p["payload"].encode("utf-8")
    d = digest(payload)
    if len(h["payload_commitments"] or ()) != 1 or h["payload_commitments"][0] != d:
        raise _err(STAGE_BOOK, "the header does not commit this record as its one payload")
    check(payload)
    r = decode(payload)
    if placement_of(r) == PLACEMENT_JOURNAL:
        raise _err(STAGE_BOOK, f"a {r.record_type} is sealed in the journal, not sent from a book")
    if h["book_id"] == "" or h["seq"] == 0 or r.block["seq"] != h["seq"]:
        raise _err(STAGE_BOOK, f"the record's seq {r.block['seq']} is not its header's {h['seq']}")
    cites = [link["target"] for link in h["links"] or () if link["type"] == "cites"]
    prev = ""
    if h["seq"] == 1 and cites:
        raise _err(STAGE_BOOK, "the first record of a book cites none before it")
    if h["seq"] > 1 and len(cites) != 1:
        raise _err(STAGE_BOOK, "a book record cites exactly the record before it")
    if h["seq"] > 1:
        prev = cites[0]
    return BookRead(r, d, record_id, key_id, h["book_id"], h["seq"], prev)

