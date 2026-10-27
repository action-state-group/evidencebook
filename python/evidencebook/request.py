# SPDX-License-Identifier: Apache-2.0
"""Evidence requests and replies on the wire (draft-mih-agent-evidence-request-00,
profile evidence-interop/0).

The request map, the reply envelope and the signed refusal are
agent-action-capsule's ``evidence_request`` types. This module adds what Go's
``evidencerequest`` does beyond them: the request's ``profile`` member, Go's
stricter derivation token form, and a refusal's optional ``detail``.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass
from typing import Any

import cbor2
from agent_action_capsule.canonical import jcs
from agent_action_capsule.evidence_request import (
    DERIVATION_HISTORY_CARD,
    PROFILE,
    REASON_COVERAGE_UNSATISFIABLE,
    REASON_REQUEST_MALFORMED,
    REFUSAL_REASONS,
    Coverage,
    Envelope,
    Refusal,
    RequestError,
    Subject,
    VerificationError,
    is_digest,
    parse_reply as _parse_reply,
    parse_request,
    parse_utc,
)
from agent_action_capsule.interop_profile import _cbor
from agent_action_capsule.interop_profile._common import is_profile_time

from .errors import InvalidError

__all__ = [
    "PROFILE",
    "DERIVATION_HISTORY_CARD",
    "MEDIA_TYPE_JSON",
    "MEDIA_TYPE_CBOR",
    "Message",
    "Subject",
    "Coverage",
    "EvidenceRequest",
    "Envelope",
    "Refusal",
    "Reply",
    "encode_request",
    "parse_reply",
    "refusal_conforming",
]

MEDIA_TYPE_JSON = "application/json"
MEDIA_TYPE_CBOR = "application/cbor"
_BINDINGS = {MEDIA_TYPE_JSON: "json", MEDIA_TYPE_CBOR: "cbor"}
_SUPPORTED_PROFILES = (PROFILE,)
_DERIVATION_TOKEN = re.compile(r"[a-z0-9_]+/[0-9]+\Z")


@dataclass(frozen=True)
class Message:
    """Octets as carried, with the media type they were carried under."""

    octets: bytes
    media_type: str = MEDIA_TYPE_JSON


@dataclass(frozen=True)
class EvidenceRequest:
    """The request map (ER §3, profile §6.1). ``profile`` empty is answered
    as :data:`PROFILE` (profile §3.3 step 2)."""

    subject: Subject
    coverage: Coverage
    derivation: str | None = None
    deadline: str | None = None
    nonce: str | None = None
    route: str | None = None
    profile: str = ""

    def value(self) -> dict:
        """The request's wire value, checked as Go's ``Request.Value`` checks it."""
        try:
            parse_request(jcs({"subject": self.subject.to_wire(), "coverage": {"expected_pin": "0" * 64}}))
        except (RequestError, ValueError, TypeError):
            raise InvalidError("subject is not one of the six forms with conforming content") from None
        coverage: dict[str, Any] = {}
        pin, floor = self.coverage.expected_pin, self.coverage.min_freshness
        if pin is not None:
            if not is_digest(pin):
                raise InvalidError("expected_pin is not a digest")
            coverage["expected_pin"] = pin
        if floor is not None:
            if type(floor) is int and 0 <= floor <= (1 << 53) - 1 or isinstance(floor, str) and parse_utc(floor):
                coverage["min_freshness"] = floor
            else:
                raise InvalidError("min_freshness is exactly one of a size or an RFC 3339 UTC time")
        if len(coverage) != 1:
            raise InvalidError("coverage carries exactly one of expected_pin or min_freshness")
        out: dict[str, Any] = {"subject": self.subject.to_wire(), "coverage": coverage}
        if self.derivation:
            if not valid_derivation(self.derivation):
                raise InvalidError(f"derivation {self.derivation!r} is neither a registered token form nor a digest")
            out["derivation"] = self.derivation
        if self.deadline:
            if parse_utc(self.deadline) is None:
                raise InvalidError(f"deadline {self.deadline!r} is not an RFC 3339 UTC time")
            out["deadline"] = self.deadline
        for name in ("nonce", "route", "profile"):
            if getattr(self, name):
                out[name] = getattr(self, name)
        return out


def valid_derivation(name: str) -> bool:
    return bool(_DERIVATION_TOKEN.match(name)) or is_digest(name)


def encode(media_type: str, value: Any) -> bytes:
    """JCS for JSON (byte strings as unpadded base64url), core deterministic CBOR for CBOR."""
    if media_type == MEDIA_TYPE_JSON:
        return jcs(_json_model(value))
    if media_type == MEDIA_TYPE_CBOR:
        return _cbor.encode(value)
    raise InvalidError(f"media type {media_type!r} is neither {MEDIA_TYPE_JSON} nor {MEDIA_TYPE_CBOR}")


def _json_model(value: Any) -> Any:
    import base64

    if isinstance(value, (bytes, bytearray)):
        return base64.urlsafe_b64encode(bytes(value)).rstrip(b"=").decode("ascii")
    if isinstance(value, (list, tuple)):
        return [_json_model(item) for item in value]
    if isinstance(value, dict):
        return {key: _json_model(item) for key, item in value.items()}
    return value


def encode_request(request: EvidenceRequest, media_type: str = MEDIA_TYPE_JSON) -> bytes:
    """``request`` encoded in ``media_type`` (profile §5.1, P-9)."""
    return encode(media_type, request.value())


@dataclass(frozen=True)
class RequestCheck:
    """Received request octets read (profile §6.1 steps 1 to 5): the request,
    or the refusal reason and, for an unknown profile only, the detail sent."""

    request: EvidenceRequest | None = None
    reason: str = ""
    why: str = ""
    detail: str = ""


def _member_rules(fields: Any) -> str:
    """Go's member rules agent-action-capsule's reader does not apply: a
    profile member must be text, a derivation Go's token form or a digest."""
    if not isinstance(fields, dict):
        return ""
    if "profile" in fields and not isinstance(fields["profile"], str):
        return "profile is not a text string"
    derivation = fields.get("derivation")
    if isinstance(derivation, str) and not valid_derivation(derivation):
        return "derivation is neither a registered token form nor a definition digest"
    return ""


class _Duplicate(ValueError):
    pass


def _object(pairs: list) -> dict:
    out = {}
    for key, value in pairs:
        if key in out:
            raise _Duplicate(f"duplicate member {key!r}")
        out[key] = value
    return out


def _reject_constant(text: str) -> Any:
    raise ValueError(f"{text} is not JSON")


def _lone_surrogate(value: Any) -> bool:
    """Whether any text in ``value``, member names included, holds an
    unpaired surrogate, which has no UTF-8 form (Go refuses it)."""
    stack = [value]
    while stack:
        item = stack.pop()
        if isinstance(item, str):
            try:
                item.encode("utf-8")
            except UnicodeEncodeError:
                return True
        elif isinstance(item, list):
            stack.extend(item)
        elif isinstance(item, dict):
            stack.extend(item)
            stack.extend(item.values())
    return False


def strict_json(octets: bytes) -> Any:
    """JSON read as Go's ``evidencerequest.DecodeJSON`` reads it: UTF-8, no
    duplicate member, no NaN or infinity, no unpaired surrogate. Raises
    ``ValueError``."""
    value = json.loads(octets.decode("utf-8"), object_pairs_hook=_object, parse_constant=_reject_constant)
    if _lone_surrogate(value):
        raise ValueError("a text holds an unpaired surrogate")
    return value


_HEX = re.compile(r"(?:[0-9a-fA-F]{2})*\Z")


def strict_hex(text: Any) -> bytes | None:
    """Hex as Go's ``hex.DecodeString`` reads it (no whitespace), or ``None``."""
    if not isinstance(text, str) or not _HEX.match(text):
        return None
    return bytes.fromhex(text)


def _decoded(message: Message) -> Any:
    if message.media_type == MEDIA_TYPE_JSON:
        return json.loads(message.octets)
    return cbor2.loads(message.octets)


def read_request(message: Message) -> RequestCheck:
    """Validate received request octets, refusing as Go's ``ParseRequest`` does."""
    binding = _BINDINGS.get(message.media_type)
    if binding is None:
        return RequestCheck(reason=REASON_REQUEST_MALFORMED, why="not a request media type")
    try:
        parsed = parse_request(message.octets, binding=binding)
    except RequestError as err:
        if err.reason == REASON_COVERAGE_UNSATISFIABLE:
            # The bytes decoded; a malformed member wins over the coverage rule.
            fields = _decoded(message)
            why = _member_rules(fields)
            if message.media_type == MEDIA_TYPE_JSON and _lone_surrogate(fields):
                why = "a text holds an unpaired surrogate"
            if why:
                return RequestCheck(reason=REASON_REQUEST_MALFORMED, why=why)
        return RequestCheck(reason=err.reason, why=str(err))
    fields = _decoded(message)
    if message.media_type == MEDIA_TYPE_JSON and _lone_surrogate(fields):
        return RequestCheck(reason=REASON_REQUEST_MALFORMED, why="a text holds an unpaired surrogate")
    why = _member_rules(fields)
    if why:
        return RequestCheck(reason=REASON_REQUEST_MALFORMED, why=why)
    profile = fields.get("profile", "")
    if profile and profile not in _SUPPORTED_PROFILES:
        return RequestCheck(reason=REASON_REQUEST_MALFORMED, why="profile is not supported",
                            detail="profiles supported: " + ", ".join(_SUPPORTED_PROFILES))
    request = EvidenceRequest(
        subject=parsed.subject, coverage=parsed.coverage, derivation=parsed.derivation, deadline=parsed.deadline,
        nonce=parsed.nonce, route=parsed.route, profile=profile or PROFILE,
    )
    return RequestCheck(request=request)


# --- replies ------------------------------------------------------------------


@dataclass(frozen=True)
class Reply:
    """A reply as carried: exactly one of an envelope or a refusal."""

    message: Message
    envelope: Envelope | None = None
    refusal: Refusal | None = None
    #: A refusal's optional human-readable text, outside its signature.
    detail: str = ""


def refusal_value(refusal: Refusal, detail: str = "") -> dict:
    out = refusal.to_dict()
    if detail:
        out["detail"] = detail
    return out


def encode_refusal(refusal: Refusal, media_type: str, detail: str = "") -> Reply:
    return Reply(Message(encode(media_type, refusal_value(refusal, detail)), media_type), refusal=refusal, detail=detail)


def encode_envelope(envelope: Envelope, media_type: str) -> Reply:
    return Reply(Message(encode(media_type, envelope.to_value()), media_type), envelope=envelope)


def parse_reply(message: Message) -> Reply:
    """Read a received reply: exactly one of an envelope or a refusal. Every
    failure is :class:`InvalidError`. Parsing authenticates nothing."""
    binding = _BINDINGS.get(message.media_type)
    if binding is None:
        raise InvalidError(f"a reply is carried as {MEDIA_TYPE_JSON} or {MEDIA_TYPE_CBOR}")
    try:
        parsed = _parse_reply(message.octets, binding)
    except (VerificationError, ValueError) as err:
        raise InvalidError(f"reply: {err}") from None
    if isinstance(parsed, Refusal):
        detail = _decoded(message).get("detail", "")
        if not isinstance(detail, str):
            raise InvalidError("reply: detail is not a text string")
        return Reply(message, refusal=parsed, detail=detail)
    return Reply(message, envelope=parsed)


def refusal_conforming(refusal: Refusal) -> bool:
    """A conforming refusal (profile §7.2 reason_registered, issued_at_form):
    one registered reason, the profile's seconds time form, a digest."""
    return refusal.reason in REFUSAL_REASONS and is_profile_time(refusal.issued_at) and is_digest(refusal.request_digest)
