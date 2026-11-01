# SPDX-License-Identifier: Apache-2.0
"""The record-level checks a sealed record must pass, a record's payload and
its digest, as Go's ``dealprofile/check.go`` (and ``record.go``'s ``Decode``).

The stages run in the order the reference checker
(``dealprofile/x-deal-v0/check_profile.py``, ``check_record``) runs them. The
chain stage, which reads a record against the records before it, is not here;
the reference checker runs it.

A refusal matches Go's in its stage. Two diagnostics are worded differently:
a JCS failure carries the Python JCS implementation's text, and a schema
refusal carries ``jsonschema``'s wording (the reference checker's, which the
negative fixtures' ``expected_error_contains`` is written in) where Go's
validator has its own. The Unicode tables are Python's, not Go 1.27's
(Unicode 17), so a character newer than ``unicodedata`` may be read
differently.
"""
from __future__ import annotations

import json
import re
from typing import Any, Iterable

from agent_action_capsule.canonical import jcs, json_digest

from .keys import fold, go_lower
from .names import (
    CANONICALIZATION, PROFILE, TYPE_ACTION_APPROVAL, TYPE_ACTION_EVALUATION, TYPE_ACTION_OUTCOME,
    TYPE_ACTION_RECORD, TYPE_ACTION_REPORT, TYPE_APPROVAL, TYPE_CHECK, TYPE_OUTCOME, TYPE_PROPOSED_ACTION,
    TYPE_TASK_AUTHORITY, TYPE_VERDICT,
)
from .schema import profile_validator, record_validator

__all__ = [
    "STAGE_CANONICALIZATION", "STAGE_PERSONAL_DATA", "STAGE_WORDING", "STAGE_SCHEMA", "STAGE_DIGEST",
    "StageError", "encode", "check", "digest", "check_digest", "decode",
]

STAGE_CANONICALIZATION = "canonicalization"
STAGE_PERSONAL_DATA = "personal_data"
STAGE_WORDING = "wording"
STAGE_SCHEMA = "schema"
STAGE_DIGEST = "digest"


class StageError(ValueError):
    """A record refused at one stage."""

    def __init__(self, stage: str, detail: str, causes: list[str] | None = None) -> None:
        super().__init__(f"[{stage}] {detail}")
        self.stage = stage
        self.detail = detail
        #: At the schema stage, every failed leaf of the validation
        #: ("/location: why"); ``detail`` is the first.
        self.causes = causes or []


#: The typed records a chain may hold (PROFILE.md §9).
_TYPED_ACTION_TYPES = {
    TYPE_TASK_AUTHORITY: "task_authority", TYPE_PROPOSED_ACTION: TYPE_CHECK, TYPE_ACTION_EVALUATION: TYPE_VERDICT,
    TYPE_ACTION_APPROVAL: TYPE_APPROVAL, TYPE_ACTION_RECORD: "action", TYPE_ACTION_OUTCOME: TYPE_OUTCOME,
    TYPE_ACTION_REPORT: "report",
}


class _Float(float):
    """A JSON number written with a fraction or an exponent. Numbers are read
    as written (Go ``json.Number``); a float is refused by name at the
    canonicalization stage."""

    text: str


def _float(text: str) -> _Float:
    f = _Float(text)
    f.text = text
    return f


def _refuse_constant(name: str) -> Any:
    raise ValueError(f"invalid JSON constant {name}")


def _go_quote(s: str) -> str:
    """Go ``strconv.Quote`` (``%q``)."""
    out = ['"']
    for c in s:
        if c == '"' or c == "\\":
            out.append("\\" + c)
        elif c in "\a\b\f\n\r\t\v":
            out.append({"\a": "\\a", "\b": "\\b", "\f": "\\f", "\n": "\\n", "\r": "\\r", "\t": "\\t",
                        "\v": "\\v"}[c])
        elif c.isprintable():
            out.append(c)
        elif ord(c) < 0x80:
            out.append("\\x%02x" % ord(c))
        elif ord(c) < 0x10000:
            out.append("\\u%04x" % ord(c))
        else:
            out.append("\\U%08x" % ord(c))
    out.append('"')
    return "".join(out)


def _go_v(v: Any) -> str:
    """Go ``%v`` of a decoded JSON value."""
    if v is None:
        return "<nil>"
    if v is True:
        return "true"
    if v is False:
        return "false"
    if isinstance(v, _Float):
        return v.text
    if isinstance(v, list):
        return "[" + " ".join(_go_v(e) for e in v) + "]"
    if isinstance(v, dict):
        return "map[" + " ".join(f"{k}:{_go_v(v[k])}" for k in sorted(v)) + "]"
    return str(v)


def _check_text(raw: bytes) -> None:
    """Refuse JSON text that is not valid UTF-8, or whose strings escape a
    lone surrogate (RFC 8785 §3.2.2.2: every string is valid Unicode)."""
    try:
        raw.decode("utf-8")
    except UnicodeDecodeError:
        raise StageError(STAGE_CANONICALIZATION, "not JCS-serializable: the JSON text is not valid UTF-8") from None
    in_string = False
    i, n = 0, len(raw)
    while i < n:
        c = raw[i]
        if not in_string:
            in_string = c == 0x22
            i += 1
            continue
        if c == 0x22:
            in_string = False
        elif c == 0x5C:  # backslash
            if i + 1 < n and raw[i + 1] == 0x75 and i + 5 < n:  # \u
                u = _hex4(raw[i + 2:i + 6])
                if u is not None and 0xD800 <= u <= 0xDFFF:
                    paired = False
                    if u < 0xDC00 and i + 11 < n and raw[i + 6] == 0x5C and raw[i + 7] == 0x75:
                        low = _hex4(raw[i + 8:i + 12])
                        paired = low is not None and 0xDC00 <= low <= 0xDFFF
                    if not paired:
                        raise StageError(STAGE_CANONICALIZATION, "not JCS-serializable: lone surrogate")
                    i += 12
                    continue
                i += 6
                continue
            i += 1  # the escaped character
        i += 1


def _hex4(b: bytes) -> int | None:
    try:
        return int(b.decode("ascii"), 16) if len(b) == 4 and re.fullmatch(rb"[0-9a-fA-F]{4}", b) else None
    except ValueError:
        return None


def decode(record: Any) -> Any:
    """A record read as generic JSON with numbers kept as written, as a
    verifier reads the sealed bytes. ``record`` is JSON text (``bytes`` or
    ``str``, read as written) or any other value, which is written with
    ``json`` first. Text must be valid UTF-8 with no lone surrogate escape,
    which a decoder would replace unseen; such text is refused at the
    canonicalization stage. Text that is not JSON raises ``ValueError``."""
    if isinstance(record, (bytes, bytearray)):
        raw = bytes(record)
    elif isinstance(record, str):
        try:
            raw = record.encode("utf-8")
        except UnicodeEncodeError:
            raise StageError(STAGE_CANONICALIZATION, "not JCS-serializable: lone surrogate") from None
    else:
        raw = json.dumps(record, ensure_ascii=False, allow_nan=False).encode("utf-8")
    _check_text(raw)
    return json.loads(raw.decode("utf-8"), parse_float=_float, parse_constant=_refuse_constant)


def encode(record: Any, local_values: Iterable[str] = ()) -> tuple[bytes, str]:
    """A record's payload, the JCS bytes a step seals, and its digest, once
    the record passes :func:`check`. ``local_values`` are the raw local-store
    values the record must not carry (every identifier seen in the deal, and
    every committed text long enough not to collide with a token); they are
    used only for that scan and never sealed."""
    doc = decode(record)
    _check_doc(doc, local_values)
    return jcs(doc), json_digest(doc)


def check(record: Any, local_values: Iterable[str] = ()) -> None:
    """Runs the record-level stages over ``record`` (canonicalization,
    personal_data, wording, schema); raises the first refusal as a
    :class:`StageError`."""
    _check_doc(decode(record), local_values)


def digest(record: Any) -> str:
    """A record's digest: lowercase hex SHA-256 over its JCS bytes
    (PROFILE.md §5)."""
    return json_digest(decode(record))


def check_digest(record: Any, claimed: str) -> None:
    """Refuses, at the digest stage, a claimed digest that is not the
    record's."""
    if digest(record) != claimed:
        raise StageError(STAGE_DIGEST, "claimed digest is not SHA-256 over the JCS bytes")


def _check_doc(doc: Any, local_values: Iterable[str]) -> None:
    _stage_canonicalization(doc)
    _stage_personal_data(doc, list(local_values))
    _stage_wording(doc)
    _stage_schema(doc)


def _is_typed(rec: Any) -> bool:
    return isinstance(rec, dict) and isinstance(rec.get("type"), str) and PROFILE not in rec


def _stage_canonicalization(doc: Any) -> None:
    blk = None
    if isinstance(doc, dict):
        blk = doc if _is_typed(doc) else doc.get(PROFILE)
    if not isinstance(blk, dict):
        raise StageError(STAGE_CANONICALIZATION, "no x-deal-v0 block")
    c = blk.get("canonicalization")
    if not (isinstance(c, str) and c == CANONICALIZATION):
        raise StageError(STAGE_CANONICALIZATION,
                         f'canonicalization must be exactly "jcs" (got {_go_v(c)}); fail closed')
    # A float is refused by name first, as the reference checker words it;
    # JCS would refuse it too, less plainly.
    _no_floats(doc, "$")
    try:
        jcs(doc)
    except Exception as e:  # noqa: BLE001 - every JCS refusal is this stage's
        raise StageError(STAGE_CANONICALIZATION, f"not JCS-serializable: {e}") from None


def _no_floats(v: Any, path: str) -> None:
    if isinstance(v, float):
        raise StageError(STAGE_CANONICALIZATION, f"floating-point value at {path}; money is integer minor units")
    if isinstance(v, dict):
        for k in sorted(v):
            _no_floats(v[k], f"{path}.{k}")
    elif isinstance(v, list):
        for i, e in enumerate(v):
            _no_floats(e, f"{path}[{i}]")


def _record_strings(v: Any, path: str, out: list[tuple[str, str]]) -> None:
    """Every string a record carries, a value or a member name, and where."""
    if isinstance(v, str):
        out.append((path, v))
    elif isinstance(v, dict):
        for k in sorted(v):
            out.append((path + ".<key>", k))
            _record_strings(v[k], f"{path}.{k}", out)
    elif isinstance(v, list):
        for i, e in enumerate(v):
            _record_strings(e, f"{path}[{i}]", out)


# The scans read digits and white space as the reference checker's Python
# patterns do, in Unicode: \d is any decimal digit (Nd), and \s any white
# space. $ also matches before one final newline.
_SCAN_EXEMPT = re.compile(r"([0-9a-f]{16,}|(deal|sale)-[0-9a-f]+|\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2}Z)?|"
                          r"[a-z0-9-]+/[a-z0-9-]+/\d+\.\d+\.\d+)$")
_SCAN_PHONE = re.compile(r"\+?\(?\d[\d\s().-]{6,}\d")
_SCAN_NON_DIGIT = re.compile(r"\D")
_SCAN_EMAIL = re.compile(r"[^\s@]+@[^\s@]+\.[^\s@]+")
_SCAN_WORDS = re.compile(r"[^a-z]+")
#: Words that label or grade a counterparty; a record never carries them.
_BANNED_WORDS = frozenset({
    "scam", "scams", "scammer", "scammers", "scammy", "fraud", "frauds", "fraudster", "fraudulent", "score",
    "scores", "scored", "scoring", "rating", "ratings", "reputation", "reputational", "blacklist", "blacklisted",
    "blocklist", "trustworthiness",
})


def _stage_personal_data(doc: Any, local_values: list[str]) -> None:
    """Refuses a raw phone number, email address or local-store value
    anywhere in the record, a member name included."""
    strs: list[tuple[str, str]] = []
    _record_strings(doc, "$", strs)
    for path, s in strs:
        if _SCAN_EXEMPT.match(s):
            continue
        for m in _SCAN_PHONE.finditer(s):
            if len(_SCAN_NON_DIGIT.sub("", m.group())) >= 7:
                raise StageError(STAGE_PERSONAL_DATA, f"raw phone number at {path}")
        if _SCAN_EMAIL.search(s):
            raise StageError(STAGE_PERSONAL_DATA, f"raw email address at {path}")
        low = fold(s)
        for raw in local_values:
            if raw and fold(raw) in low:
                raise StageError(STAGE_PERSONAL_DATA, f"raw local-store identifier at {path}")


def _stage_wording(doc: Any) -> None:
    strs: list[tuple[str, str]] = []
    _record_strings(doc, "$", strs)
    for path, s in strs:
        for tok in _SCAN_WORDS.split(go_lower(s)):
            if tok in _BANNED_WORDS:
                raise StageError(STAGE_WORDING, f"prohibited word {_go_quote(tok)} at {path}")


def _leaves(e: Any, out: list[str]) -> None:
    if e.context:
        for c in e.context:
            _leaves(c, out)
        return
    out.append("/" + "/".join(str(p) for p in e.absolute_path) + ": " + e.message[:200])


def _stage_schema(doc: Any) -> None:
    if _is_typed(doc):
        t = doc["type"]
        if t not in _TYPED_ACTION_TYPES:
            raise StageError(STAGE_SCHEMA, f"type: {_go_quote(t)} is not a typed action record")
        validator = record_validator(t)
    else:
        validator = profile_validator()
    errs = sorted(validator.iter_errors(doc), key=lambda e: [str(p) for p in e.absolute_path])
    if errs:
        leaves: list[str] = []
        for e in errs:
            _leaves(e, leaves)
        raise StageError(STAGE_SCHEMA, leaves[0], leaves)
