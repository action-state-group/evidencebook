# SPDX-License-Identifier: Apache-2.0
"""Verifying a subject bundle (``subject_bundle/0``, the subject convention v0,
evidencebook#116 design note section 4), check for check with Go's
``VerifySubjectBundle``.

A full form is an Evidence Bundle v2 with the subject's membership in its
``evidencebook/subject`` extension, proven at the level it names:
``holder_commitment`` (the ``subject_id`` index's key range proof) or
``log_prefix`` (every record from seq 1 to the anchor disclosed). A
projection carries the visible members only, with no proof: its completeness
is the holder's assertion. This package verifies subject bundles; it does not
build them (Go's ``Book.SubjectBundle``).

The Evidence Bundle itself is checked by agent-action-capsule's neutral
verifier, as Go's ``VerifyBundle`` checks it with that verifier's Go port;
:func:`read_bundle` reads the evidence-layer content out of what verified.
"""
from __future__ import annotations

import base64
import binascii
import hashlib
import json
import re
from dataclasses import dataclass, field
from typing import Any, Callable, Optional

from agent_action_capsule.bundle import MATCH, verify_bundle
from agent_action_capsule.canonical import jcs

from . import _gostruct as _go
from ._gofold import fold_key
from ._cll import read_checkpoint
from ._index import verify_key_range
from ._limits import MAX_REPLY_BYTES, MAX_REPLY_DEPTH, MAX_REPLY_ITEMS, MAX_REPLY_RECEIPTS, json_depth_over
from .errors import InvalidError
from .record import ADJUDICATION, EPISTEMIC_TYPES, FIELD_NAME, LINK_TYPES, RECORD_TYPE_INDEX_ROOT, Header, Record
from .subject import (
    CLOSES,
    MEMBERSHIP_LOG,
    RECORD_TYPE_SUBJECT_CLOSE,
    RECORD_TYPE_SUBJECT_ROOT,
    ROOT_SPEC,
    SUBJECT_ID_INDEX_NAME,
    PeerRecord,
    SubjectOf,
    SubjectRootStatement,
    read_subject_close,
    read_subject_root,
)

__all__ = [
    "DERIVATION_SUBJECT_BUNDLE",
    "SUBJECT_BUNDLE_FULL",
    "SUBJECT_BUNDLE_PROJECTION",
    "SUBJECT_LEVEL_HOLDER_COMMITMENT",
    "SUBJECT_LEVEL_LOG_PREFIX",
    "SUBJECT_LEVEL_HOLDER_ASSERTION",
    "EXTENSION_SUBJECT",
    "VerifiedBundle",
    "VerifiedSubject",
    "read_bundle",
    "verify_subject_bundle",
]

DERIVATION_SUBJECT_BUNDLE = "subject_bundle/0"
SUBJECT_BUNDLE_FULL = "full"
SUBJECT_BUNDLE_PROJECTION = "projection"
SUBJECT_LEVEL_HOLDER_COMMITMENT = "holder_commitment"
SUBJECT_LEVEL_LOG_PREFIX = "log_prefix"
SUBJECT_LEVEL_HOLDER_ASSERTION = "holder_assertion"

EXTENSION_SUBJECT = "evidencebook/subject"
EXTENSION_PAYLOADS = "evidencebook/payloads"
#: The Capsule member carrying a record's header inline (Go RecordHeaderMember).
RECORD_HEADER_MEMBER = "evidence_record_header"
#: The disclosure member a record's header is disclosed under (Go HeaderMember).
HEADER_MEMBER = "agent_input"

_HEX64 = re.compile(r"[0-9a-f]{64}\Z")
_RETENTION_STATES = ("AVAILABLE", "PARTIAL", "WITHHELD", "DELETED", "LEGAL_HOLD")
_COMMIT_ALG = "sha256-jcs-nonce256"


# -- the record header, as Go's strictHeader reads it ------------------------------------

_HEADER_SPEC = _go.Struct({
    "v": _go.INT,
    "book_id": _go.STRING,
    "seq": _go.UINT64,
    "record_type": _go.STRING,
    "epistemic_type": _go.STRING,
    "committed_at": _go.STRING,
    "event_time_claim": _go.STRING,
    "payload_commitments": _go.List(_go.STRING),
    "field_commitments": _go.Map(_go.STRING),
    "commit_alg": _go.STRING,
    "retention_state": _go.STRING,
    "links": _go.List(_go.Struct({"type": _go.STRING, "target": _go.STRING})),
    "subject_ref": _go.STRING,
    "principal_ref": _go.STRING,
    "counterparty_ref": _go.STRING,
    "correlation": _go.Ptr(_go.Struct({"exchange_id": _go.STRING, "request_digest": _go.STRING,
                                       "twin_bracket_id": _go.STRING})),
    "reproduction": _go.Ptr(_go.Struct({"clock": _go.STRING})),
    "statement": _go.RAW,
})


def _marshal_header(h: dict) -> dict:
    """``json.Marshal`` of a decoded Go ``Header``: its omitempty members left
    out when empty, ``links`` kept (``null`` when nil)."""
    out: dict[str, Any] = {"v": h["v"], "book_id": h["book_id"], "seq": h["seq"], "record_type": h["record_type"],
                           "epistemic_type": h["epistemic_type"], "committed_at": h["committed_at"]}
    for name in ("event_time_claim", "commit_alg", "retention_state", "subject_ref", "principal_ref", "counterparty_ref"):
        if h[name]:
            out[name] = h[name]
    if h["payload_commitments"]:
        out["payload_commitments"] = h["payload_commitments"]
    if h["field_commitments"]:
        out["field_commitments"] = h["field_commitments"]
    out["links"] = h["links"]
    if h["correlation"] is not None:
        out["correlation"] = {k: v for k, v in h["correlation"].items() if v}
    if h["reproduction"] is not None:
        out["reproduction"] = h["reproduction"]
    if h["statement"] is _go.JSON_NULL:
        out["statement"] = None
    elif h["statement"] is not None:
        out["statement"] = h["statement"]
    return out


def _validate_header(h: dict) -> None:
    """Go's ``validateHeader``."""
    if h["v"] != 1:
        raise InvalidError(f"header version {h['v']}")
    if not h["book_id"] or not h["seq"] or not h["record_type"] or not h["committed_at"]:
        raise InvalidError("book_id, seq, record_type and committed_at are required")
    if h["epistemic_type"] not in EPISTEMIC_TYPES:
        raise InvalidError(f"epistemic_type {h['epistemic_type']!r} is not registered")
    if (h["payload_commitments"] or h["field_commitments"]) and h["retention_state"] not in _RETENTION_STATES:
        raise InvalidError("retention_state is required with payload_commitments or field_commitments")
    if bool(h["field_commitments"]) != bool(h["commit_alg"]):
        raise InvalidError("commit_alg is present exactly when field_commitments are")
    if h["commit_alg"] and h["commit_alg"] != _COMMIT_ALG:
        raise InvalidError(f"commit_alg {h['commit_alg']!r} is not {_COMMIT_ALG}")
    for name, digest in (h["field_commitments"] or {}).items():
        if not FIELD_NAME.match(name) or not _HEX64.match(digest):
            raise InvalidError(f"field commitment {name!r} is not a field name with a lowercase SHA-256 digest")
    for digest in h["payload_commitments"] or ():
        if not _HEX64.match(digest):
            raise InvalidError(f"payload commitment {digest!r} is not a lowercase SHA-256 digest")
    if h["reproduction"] is not None and h["reproduction"]["clock"] != "caller":
        raise InvalidError(f"reproduction clock {h['reproduction']['clock']!r} is not 'caller'")
    if h["links"] is None:
        raise InvalidError("links must be present, possibly empty")
    for link in h["links"]:
        if link["type"] not in LINK_TYPES:
            raise InvalidError(f"link type {link['type']!r} is not registered")
        if not _HEX64.match(link["target"]):
            raise InvalidError(f"link target {link['target']!r} is not a record id")
        if link["type"] == "adjudicates" and h["epistemic_type"] != ADJUDICATION:
            raise InvalidError("a record carrying an adjudicates link must have epistemic_type adjudication")


def strict_header(disclosed: Any) -> tuple[Header, dict]:
    """A disclosed header read as Go's ``strictHeader`` reads it: decoded into
    the Go ``Header`` struct, re-encoded to exactly what was disclosed (so no
    member is ignored, merged or matched by case), then validated. Returns
    the header and its JSON value (an explicit ``"statement": null`` kept)."""
    h = _go.decode(disclosed, _HEADER_SPEC, "header")
    value = _marshal_header(h)
    if jcs(value) != jcs(disclosed):
        raise InvalidError("header: value does not re-encode to what was disclosed")
    _validate_header(h)
    return Header.from_value(value), value


# -- the Evidence Bundle --------------------------------------------------------------------


@dataclass
class VerifiedBundle:
    """What a relying party learns from a bundle the neutral Evidence Bundle
    verifier checked (Go ``VerifiedBundle``), the members a subject bundle
    needs."""

    digest: str = ""
    log_id: str = ""
    anchor_root: str = ""
    anchor_mmr_size: int = 0
    anchor_cose: str = ""
    anchor_log_id: str = ""
    anchor_authenticated: bool = False
    anchor_key_id: str = ""
    anchor_statement: bytes = b""
    witnesses: list[bytes] = field(default_factory=list)
    interval_first: int = 0
    interval_last: int = 0
    records: list[PeerRecord] = field(default_factory=list)
    payloads: dict[str, bytes] = field(default_factory=dict)
    extensions: dict[str, bytes] = field(default_factory=dict)
    findings: list[str] = field(default_factory=list)
    claims: dict[str, str] = field(default_factory=dict)

    def fully_verified(self) -> None:
        """Raise unless all three claims passed and every record's capsule
        verified (Go ``FullyVerified``)."""
        for name, key in (("graph closure", "graph_closure"), ("interval coverage", "interval_coverage"),
                          ("per-record membership", "per_record_membership")):
            if self.claims.get(key) != "pass":
                raise InvalidError(f"bundle {name} is {self.claims.get(key)!r}, not pass")
        for record in self.records:
            if not record.capsule_ok:
                raise InvalidError(f"bundle record {record.record_id} does not verify as a capsule")

    def covers(self, seq: int) -> bool:
        return self.anchor_authenticated and self.interval_first <= seq <= self.interval_last


def _reject_constant(name: str) -> Any:
    raise ValueError(f"{name} is not JSON")


_SURROGATE = re.compile("[\ud800-\udfff]")


def _go_strings(value: Any) -> Any:
    """Go's decoder writes an unpaired surrogate escape as U+FFFD."""
    if isinstance(value, str):
        return _SURROGATE.sub("�", value)
    if isinstance(value, dict):
        return {_go_strings(k): _go_strings(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_go_strings(v) for v in value]
    return value


def _decode_json(data: bytes) -> Any:
    """JSON as Go's ``decodeJSON`` reads it: invalid UTF-8 replaced, a later
    duplicate member winning, trailing data refused."""
    return _go_strings(json.loads(data.decode("utf-8", errors="replace"), parse_constant=_reject_constant))


def _reject_folded_keys(value: Any, path: str) -> None:
    if isinstance(value, dict):
        seen: dict[str, str] = {}
        for key in sorted(value, key=lambda k: k.encode("utf-8")):
            folded = fold_key(key)
            if folded in seen:
                raise InvalidError(f"{path} holds keys {seen[folded]!r} and {key!r} that differ only by case")
            seen[folded] = key
            _reject_folded_keys(value[key], f"{path}.{key}")
    elif isinstance(value, list):
        for i, item in enumerate(value):
            _reject_folded_keys(item, f"{path}[{i}]")


def _get(value: Any, key: str) -> Any:
    return value.get(key) if isinstance(value, dict) else None


def _items(value: Any) -> list:
    return value if isinstance(value, list) else []


def _uint(value: Any) -> Optional[int]:
    # Go jsonNode.uint: an integer literal, safe, not negative.
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= (1 << 53) - 1:
        return None
    return value


def _raw_url(text: str) -> bytes:
    """base64.RawURLEncoding.DecodeString: no padding; CR and LF ignored."""
    text = text.replace("\r", "").replace("\n", "")
    if "=" in text or len(text) % 4 == 1 or not re.fullmatch(r"[A-Za-z0-9_-]*", text):
        raise ValueError("not unpadded base64url")
    return base64.urlsafe_b64decode(text + "=" * (-len(text) % 4))


def read_bundle(data: bytes) -> VerifiedBundle:
    """Check a bundle with agent-action-capsule's neutral Evidence Bundle
    verifier and read the evidence-layer content out of what verified (Go
    ``VerifyBundle``). It never trusts the producer's own verification
    member. Raises :class:`InvalidError` on a refusal."""
    if len(data) > MAX_REPLY_BYTES:
        raise InvalidError(f"limit: bundle bytes: {len(data)} over {MAX_REPLY_BYTES}")
    if json_depth_over(data, MAX_REPLY_DEPTH):
        raise InvalidError(f"limit: bundle depth: over {MAX_REPLY_DEPTH}")
    try:
        tree = _decode_json(data)
    except ValueError as exc:
        raise InvalidError(f"bundle JSON: {exc}") from None
    if not isinstance(tree, dict):
        raise InvalidError("bundle is not a JSON object")
    _reject_folded_keys(tree, "$")
    if len(_items(tree.get("records"))) > MAX_REPLY_ITEMS:
        raise InvalidError(f"limit: records: {len(tree['records'])} over {MAX_REPLY_ITEMS}")
    if len(_items(_get(tree.get("checkpoint"), "witnesses"))) > MAX_REPLY_RECEIPTS:
        raise InvalidError(f"limit: witness receipts: over {MAX_REPLY_RECEIPTS}")
    result = verify_bundle(tree)
    out = VerifiedBundle(claims={"graph_closure": result.graph_closure.status,
                                 "interval_coverage": result.interval_coverage.status,
                                 "per_record_membership": result.per_record_membership.status})
    if result.bundle_digest is not None:
        out.digest = result.bundle_digest
    for claim in (result.graph_closure, result.interval_coverage, result.per_record_membership):
        if claim.status == "fail":
            raise InvalidError(f"bundle verification failed: {list(claim.findings)}")
        out.findings.extend(claim.findings)
    certificate, checkpoint = tree.get("completeness_certificate"), tree.get("checkpoint")
    if isinstance(certificate, dict) and isinstance(checkpoint, dict):
        log_id = certificate.get("log_id")
        out.log_id = log_id if isinstance(log_id, str) else ""
        out.interval_first = _uint(certificate.get("first_seq")) or 0
        out.interval_last = _uint(certificate.get("last_seq")) or 0
        stated = "log_id" in checkpoint
        if stated:
            out.anchor_log_id = checkpoint["log_id"] if isinstance(checkpoint["log_id"], str) else ""
            if not out.anchor_log_id:
                raise InvalidError("checkpoint log_id is not a non-empty string")
        root, cose = checkpoint.get("root"), checkpoint.get("cose")
        out.anchor_root = root if isinstance(root, str) else ""
        out.anchor_mmr_size = _uint(checkpoint.get("mmr_size")) or 0
        out.anchor_cose = cose if isinstance(cose, str) else ""
        out.anchor_authenticated = result.interval_coverage.status == "pass" and \
            "checkpoint_unverified" not in result.interval_coverage.findings
        if out.anchor_authenticated:
            try:
                statement = _raw_url(out.anchor_cose)
            except (ValueError, binascii.Error) as exc:
                raise InvalidError(f"checkpoint statement: {exc}") from None
            cp = read_checkpoint(statement)
            if cp.root != out.anchor_root or cp.tree_size != out.anchor_mmr_size or cp.log_id != out.log_id or \
                    (stated and out.anchor_log_id != cp.log_id):
                raise InvalidError("checkpoint statement does not name the verified interval")
            out.anchor_key_id, out.anchor_statement = cp.key_id, statement
        if "witnesses" in checkpoint and not isinstance(checkpoint["witnesses"], list):
            raise InvalidError("checkpoint.witnesses is not an array")
        for entry in _items(checkpoint.get("witnesses")):
            try:
                out.witnesses.append(jcs(entry))
            except (TypeError, ValueError) as exc:
                raise InvalidError(f"witness receipt: {exc}") from None
    extensions = tree.get("extensions")
    for key in sorted(extensions, key=lambda k: k.encode("utf-8")) if isinstance(extensions, dict) else ():
        try:
            out.extensions[key] = jcs(extensions[key])
        except (TypeError, ValueError) as exc:
            raise InvalidError(f"extension {key}: {exc}") from None
    matched = {d.capsule_id for d in result.disclosures if d.member == HEADER_MEMBER and d.status == MATCH}
    disclosures = tree.get("disclosures")
    memberships = _get(certificate, "memberships")
    for record in _items(tree.get("records")):
        record_id = _get(record, "capsule_id")
        if not isinstance(record_id, str):
            raise InvalidError("bundle record has no capsule_id")
        outcome = result.capsule_results.get(record_id)
        capsule_ok = bool(outcome is not None and outcome.ok)
        seq = _uint(_get(_get(_get(memberships, record_id), "log_coordinates"), "seq")) or 0
        header, value, verified = None, None, False
        inline = _get(record, RECORD_HEADER_MEMBER)
        disclosed = _get(_get(disclosures, record_id), HEADER_MEMBER)
        if inline is not None:
            # capsule_id covers the member, so it is the record's header
            # exactly when the capsule verifies.
            _canonical(inline, "carried header")
            if capsule_ok:
                header, value, verified = _try_header(inline)
        elif disclosed is not None and record_id in matched:
            _canonical(disclosed, "disclosed header")
            header, value, verified = _try_header(disclosed)
        out.records.append(PeerRecord(record_id, seq, header, verified, capsule_ok, value))
    payloads = _get(extensions, EXTENSION_PAYLOADS)
    for digest in sorted(payloads, key=lambda k: k.encode("utf-8")) if isinstance(payloads, dict) else ():
        text = payloads[digest]
        if not isinstance(text, str):
            raise InvalidError(f"disclosed payload {digest} is not a string")
        try:
            octets = _raw_url(text)
        except (ValueError, binascii.Error):
            octets = None
        if octets is None or hashlib.sha256(octets).hexdigest() != digest:
            raise InvalidError(f"disclosed payload does not hash to {digest}")
        out.payloads[digest] = octets
    return out


def _canonical(value: Any, what: str) -> None:
    try:
        jcs(value)
    except (TypeError, ValueError) as exc:
        raise InvalidError(f"{what}: {exc}") from None


def _try_header(value: Any) -> tuple[Optional[Header], Optional[dict], bool]:
    try:
        header, read = strict_header(value)
    except (InvalidError, KeyError, TypeError, ValueError):
        return None, None, False
    return header, read, True


# -- the subject bundle ---------------------------------------------------------------------


@dataclass
class VerifiedSubject:
    """What a relying party learns from a subject bundle (Go
    ``VerifiedSubject``). ``root``, ``members`` and ``close`` are record ids
    in log order; a projection carries none, only ``member_count``."""

    form: str
    level: str
    subject: SubjectRootStatement
    root: str = ""
    members: list[str] = field(default_factory=list)
    member_count: int = 0
    close: str = ""
    closed: bool = False
    #: The full form's disclosed records' payloads it does not carry, as
    #: ``(record_id, payload_commitment)``: availability, apart from membership.
    withheld: list[tuple[str, str]] = field(default_factory=list)
    held_by_digest: list[str] = field(default_factory=list)
    bundle: Optional[VerifiedBundle] = None


_INDEX_ROOT_SPEC = _go.Struct({"index": _go.STRING, "root": _go.STRING, "size": _go.UINT64})
_LEAF_PROOF_SPEC = _go.Struct({"key": _go.STRING, "record_id": _go.STRING, "index": _go.UINT64,
                               "path": _go.List(_go.STRING)})
_EXTENSION_SPEC = _go.Struct({
    "derivation": _go.STRING,
    "form": _go.STRING,
    "subject": ROOT_SPEC,
    "root": _go.STRING,
    "members": _go.List(_go.STRING),
    "close": _go.STRING,
    "closed": _go.BOOL,
    "level": _go.STRING,
    "key_range": _go.Ptr(_go.Struct({
        "root": _INDEX_ROOT_SPEC,
        "root_record_id": _go.STRING,
        "anchor": _go.Struct({"kind": _go.STRING, "log_id": _go.STRING, "id": _go.STRING, "entries": _go.UINT64,
                              "root": _go.STRING, "tree_size": _go.UINT64, "key_id": _go.STRING,
                              "issued_at": _go.TIME, "statement": _go.BYTES}),
        "proof": _go.Struct({"lo": _go.STRING, "hi": _go.STRING, "first": _go.UINT64,
                             "matches": _go.List(_LEAF_PROOF_SPEC), "left": _go.Ptr(_LEAF_PROOF_SPEC),
                             "right": _go.Ptr(_LEAF_PROOF_SPEC)}),
    })),
    "closure_depth": _go.INT,
    "held_by_digest": _go.List(_go.STRING),
})

_PROJECTION_SPEC = _go.Struct({
    "derivation": _go.STRING,
    "form": _go.STRING,
    "definition": _go.STRING,
    "subject_kind": _go.STRING,
    "subject_id": _go.STRING,
    "completeness": _go.STRING,
    "root": _go.Map(_go.RAW),
    "members": _go.List(_go.Map(_go.RAW)),
})


def _same_set(a: list[str], b: list[str]) -> bool:
    return sorted(a) == sorted(b)


def verify_subject_bundle(data: bytes) -> VerifiedSubject:
    """Check a subject bundle of either form (Go ``VerifySubjectBundle``).

    A full form must verify as an Evidence Bundle with every claim passed
    under an authenticated anchor, and its subject extension must agree with
    the records it carries: the root, each member, the close's count and last
    member, no member after the close. At ``holder_commitment`` the
    ``subject_id`` index's key range proof must list exactly the root, the
    members and the close under an index root the bundle's ``index_root``
    record commits. At ``log_prefix`` the bundle must disclose every record
    from seq 1 to its anchor, and the members must be exactly the records of
    the prefix the membership names. A projection is checked for its shape
    only. Raises :class:`InvalidError` on a refusal."""
    probe = _probe(data)
    if probe is not None and probe["derivation"] == DERIVATION_SUBJECT_BUNDLE and probe["form"] == SUBJECT_BUNDLE_PROJECTION:
        return _verify_projection(data)
    verified = read_bundle(data)
    verified.fully_verified()
    if not verified.anchor_authenticated:
        raise InvalidError("a subject bundle's anchor is not authenticated")
    raw = verified.extensions.get(EXTENSION_SUBJECT)
    if raw is None:
        raise InvalidError(f"the bundle carries no {EXTENSION_SUBJECT} extension")
    ext = _go.decode_text(raw, _EXTENSION_SPEC, f"{EXTENSION_SUBJECT} extension")
    if ext["derivation"] != DERIVATION_SUBJECT_BUNDLE or ext["form"] != SUBJECT_BUNDLE_FULL:
        raise InvalidError(f"the {EXTENSION_SUBJECT} extension is not a full {DERIVATION_SUBJECT_BUNDLE}")
    ext_subject = SubjectRootStatement(**ext["subject"])
    ext_subject.validate()
    members_listed = ext["members"] or []
    by_id = {r.record_id: r for r in verified.records}

    def disclosed(record_id: str, what: str) -> PeerRecord:
        r = by_id.get(record_id)
        if r is None or not r.header_verified or r.header is None or not verified.covers(r.seq):
            raise InvalidError(f"the subject's {what} {record_id} is not a disclosed, covered record of the bundle")
        return r

    root = disclosed(ext["root"], "root")
    try:
        stated = read_subject_root(_statement(root))
    except InvalidError:
        stated = None
    if stated is None or root.header.record_type != RECORD_TYPE_SUBJECT_ROOT or stated != ext_subject:
        raise InvalidError(f"record {ext['root']} is not the subject_root the extension names")
    subject = SubjectOf(Record(root.record_id, root.seq, root.header), stated)
    out = VerifiedSubject(form=SUBJECT_BUNDLE_FULL, level=ext["level"], subject=stated, root=ext["root"],
                          members=list(members_listed), member_count=len(members_listed), close=ext["close"],
                          closed=ext["closed"], held_by_digest=ext["held_by_digest"] or [], bundle=verified)
    last = root.seq
    for record_id in members_listed:
        m = disclosed(record_id, "member")
        if m.seq <= last or not subject.is_member(m.header, m.value()):
            raise InvalidError(f"record {record_id} is not the subject's next member")
        last = m.seq
    for r in verified.records:
        if not r.header_verified or r.header is None:
            continue
        for digest in r.header.payload_commitments:
            if digest not in verified.payloads:
                out.withheld.append((r.record_id, digest))
    listed = [ext["root"], *members_listed]
    if ext["closed"] != (ext["close"] != ""):
        raise InvalidError("the extension's closed and close disagree")
    if ext["close"]:
        c = disclosed(ext["close"], "close")
        want_last = members_listed[-1] if members_listed else ""
        try:
            count = read_subject_close(_statement(c))
        except InvalidError:
            count = None
        if c.header.record_type != RECORD_TYPE_SUBJECT_CLOSE or c.header.links_to(CLOSES) != [ext["root"]] or \
                count is None or (count.members, count.last) != (len(members_listed), want_last) or c.seq <= last:
            raise InvalidError(f"record {ext['close']} is not a close of the subject counting its "
                               f"{len(members_listed)} members")
        listed.append(ext["close"])
    _verify_held_by_digest(ext, members_listed, by_id)
    level = ext["level"]
    if level == SUBJECT_LEVEL_HOLDER_COMMITMENT:
        _verify_key_range(ext, ext_subject, listed, disclosed, verified)
    elif level == SUBJECT_LEVEL_LOG_PREFIX:
        if stated.membership != MEMBERSHIP_LOG:
            _verify_key_range(ext, ext_subject, listed, disclosed, verified)
        if verified.interval_first != 1 or len(verified.records) != verified.interval_last:
            raise InvalidError(f"a log-prefix subject bundle carries every record from seq 1, not "
                               f"{len(verified.records)} records of {verified.interval_first} to {verified.interval_last}")
        if stated.membership == MEMBERSHIP_LOG and root.seq != 1:
            raise InvalidError(f"a log subject_root is its book's first record, not seq {root.seq}")
        found = []
        for r in verified.records:
            if not r.header_verified or r.header is None:
                raise InvalidError(f"record {r.record_id} of the log prefix is not disclosed, so its kind is unknown")
            if stated.membership == MEMBERSHIP_LOG and r.header.record_type == RECORD_TYPE_SUBJECT_ROOT and \
                    r.record_id != ext["root"]:
                raise InvalidError(f"a per-subject book roots one subject; the prefix holds root {r.record_id} too")
            if r.seq > root.seq and subject.is_member(r.header, r.value()):
                found.append(r.record_id)
            if r.header.record_type == RECORD_TYPE_SUBJECT_CLOSE and ext["root"] in r.header.links_to(CLOSES) and \
                    r.record_id != ext["close"]:
                raise InvalidError(f"the log prefix holds close {r.record_id} the extension does not name")
        if found != members_listed:
            raise InvalidError(f"the log prefix holds {len(found)} members; the extension lists {len(members_listed)}")
    else:
        raise InvalidError(f"subject bundle level {level!r}")
    return out


def _statement(r: PeerRecord) -> Any:
    """A verified header's statement as Go's ``decodeStrict`` would read its
    ``json.RawMessage``: absent is no JSON at all, refused; ``null`` decodes
    to the zero value."""
    value = r.value() or {}
    if "statement" not in value:
        raise InvalidError(f"record {r.record_id} carries no statement")
    return value["statement"]


def _probe(data: bytes) -> Optional[dict]:
    """Go's ``json.Unmarshal`` of a bundle into ``{form, derivation}``: the
    whole text must be JSON; members matched by case-insensitive name in
    document order, the last winning; ``None`` when Unmarshal fails."""
    try:
        tree = _go.read_text(data)
    except ValueError:
        return None
    out = {"form": "", "derivation": ""}
    if tree is None:
        return out
    if not _go.is_object(tree):
        return None
    folded = {fold_key(name): name for name in out}
    for key, value in tree:
        name = key if key in out else folded.get(fold_key(key))
        if name is None or value is None:
            continue
        if not isinstance(value, str):
            return None
        out[name] = _SURROGATE.sub("\ufffd", value)
    return out


def _read_index_root(r: PeerRecord) -> dict:
    """An ``index_root`` record's statement (Go ``readIndexRoot``:
    ``decodeStrict``, member names exact and none unknown)."""
    if r.header is None or r.header.record_type != RECORD_TYPE_INDEX_ROOT:
        raise InvalidError("not an index_root record")
    spec = _go.Struct({"index": _go.STRING, "root": _go.STRING, "size": _go.UINT64, "as_of_seq": _go.UINT64})
    return _go.decode(_statement(r), spec, "index_root statement")


def _verify_key_range(ext: dict, subject: SubjectRootStatement, listed: list[str],
                      disclosed: Callable[[str, str], PeerRecord], verified: VerifiedBundle) -> None:
    """The full form's key range proof proves exactly the listed records
    under the subject id, and its index root is the one a disclosed, covered
    ``index_root`` record commits (Go ``verifySubjectKeyRange``)."""
    kr = ext["key_range"]
    if kr is None or kr["root"]["index"] != SUBJECT_ID_INDEX_NAME:
        raise InvalidError(f"a field subject's bundle carries the {SUBJECT_ID_INDEX_NAME} index's key range proof")
    proof = dict(kr["proof"])
    proof["matches"] = proof["matches"] or []
    ids = verify_key_range(kr["root"], subject.subject_id, subject.subject_id, proof)
    if len(ids) != len(listed) or not _same_set(ids, listed):
        raise InvalidError(f"the key range proves {len(ids)} records for {subject.subject_id!r}; "
                           f"the extension lists {len(listed)}")
    r = disclosed(kr["root_record_id"], "index root")
    try:
        committed = _read_index_root(r)
    except InvalidError:
        committed = None
    if committed is None or {k: committed[k] for k in ("index", "root", "size")} != kr["root"] or \
            committed["as_of_seq"] >= r.seq:
        raise InvalidError(f"record {kr['root_record_id']} does not commit the key range proof's index root")
    # The proof is the index as of as_of_seq. Every record after that, to the
    # anchor, must be disclosed and be an index root committed with it.
    tail = 0
    for t in verified.records:
        if t.seq <= committed["as_of_seq"]:
            continue
        tail += 1
        if not t.header_verified or t.header is None or t.header.record_type != RECORD_TYPE_INDEX_ROOT:
            raise InvalidError(f"record {t.record_id} follows the key range proof's index root and is not a "
                               "disclosed index root")
        try:
            other = _read_index_root(t)
        except InvalidError:
            other = None
        if other is None or (other["index"] == SUBJECT_ID_INDEX_NAME and t.record_id != kr["root_record_id"]):
            raise InvalidError(f"record {t.record_id} commits a newer {SUBJECT_ID_INDEX_NAME} index root than the proof's")
    if verified.interval_last < committed["as_of_seq"] or tail != verified.interval_last - committed["as_of_seq"]:
        raise InvalidError("the bundle does not carry every record after the key range proof's index root")


def _verify_held_by_digest(ext: dict, members: list[str], by_id: dict[str, PeerRecord]) -> None:
    """Recompute the members' outward closure over the bundle's disclosed
    records; its targets the bundle does not carry must be exactly the
    extension's ``held_by_digest`` (Go ``verifyHeldByDigest``)."""
    depth = ext["closure_depth"]
    if depth < 1:
        raise InvalidError("a subject bundle's closure depth is at least 1")
    foreign: set[str] = set()
    for m in members:
        seen, frontier = {m}, [m]
        hop = 0
        while hop < depth and frontier:
            nxt = []
            for record_id in frontier:
                r = by_id.get(record_id)
                for link in (r.header.links if r is not None and r.header is not None else ()):
                    target = by_id.get(link.target)
                    if target is None:
                        foreign.add(link.target)
                    elif not target.header_verified or target.header is None:
                        raise InvalidError(f"record {link.target} of member {m}'s closure is not disclosed")
                    elif link.target not in seen:
                        seen.add(link.target)
                        nxt.append(link.target)
            frontier = nxt
            hop += 1
    derived = sorted(foreign, key=lambda s: s.encode("utf-8"))
    held = ext["held_by_digest"] or []
    if derived != held and not (not derived and not held):
        raise InvalidError(f"the members' closure reaches {len(derived)} records the bundle does not carry; "
                           f"held_by_digest lists {len(held)}")


def _verify_projection(data: bytes) -> VerifiedSubject:
    """A projection's shape: what it states of its members is the holder's
    assertion (Go ``verifySubjectProjection``)."""
    # Go decodes the projection's own bytes, not their canonical form, so
    # member values JCS would refuse (a fraction) are read as Go reads them.
    p = _go.decode_text(data, _PROJECTION_SPEC, "subject bundle projection")
    if p["completeness"] != SUBJECT_LEVEL_HOLDER_ASSERTION or not p["subject_id"] or p["root"] is None or \
            p["members"] is None:
        raise InvalidError("a subject bundle projection names its subject, its root and its members, "
                           "as the holder's assertion")
    for m in [p["root"], *p["members"]]:
        if m is not None and "seq" in m:
            raise InvalidError("a subject bundle projection carries no position")
    return VerifiedSubject(form=SUBJECT_BUNDLE_PROJECTION, level=SUBJECT_LEVEL_HOLDER_ASSERTION,
                           subject=SubjectRootStatement(p["subject_kind"], p["subject_id"], ""),
                           member_count=len(p["members"]))
