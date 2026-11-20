# SPDX-License-Identifier: Apache-2.0
"""The MCP and A2A entries of ``Shelf.receive`` (evidencebook#577): the
carriage of the record push and its answers, as Go's ``interop_push.go`` and
agent-action-capsule's ``spec/bindings/record-push-v0.md`` (RP) specify. Like
the Go entries they are carriage only: they move a record's octets and a stamp
proof's JSON octets as opaque bytes, hand them to ``Shelf.receive`` and return
its answer. They are transport-neutral and take no transport identity: who may
push is the receive profile's push rule, which reads the sender from the
record, never from the carriage (RP §2.3 rule 2)."""
from __future__ import annotations

import base64
import binascii
import json
import re
import secrets
from typing import Any

from .errors import BusyError, InvalidError, OverLimitError, RefusedError
from .request import strict_json
from .shelf_stamp_proof import StampProof

__all__ = ["A2A_PUSH_EXTENSION_URI", "MCP_PROTOCOL_VERSION", "MCP_TOOL_RECORD_PUSH", "PushA2AServer", "PushMCPServer",
           "a2a_push_extension"]

MCP_PROTOCOL_VERSION = "2026-07-28"
MCP_TOOL_RECORD_PUSH = "record_push"
A2A_PROTOCOL_VERSION = "1.0"
# An identifier: it resolves only once the Internet-Draft it names is posted,
# and none is (RP §4.1).
A2A_PUSH_EXTENSION_URI = "https://datatracker.ietf.org/doc/draft-mih-agent-record-push/"

_PARSE_ERROR, _INVALID_REQUEST, _METHOD_NOT_FOUND, _INVALID_PARAMS, _INTERNAL_ERROR = -32700, -32600, -32601, -32602, -32603
_UNSUPPORTED_PROTOCOL_VERSION = -32022
_TASK_NOT_FOUND, _PUSH_NOT_SUPPORTED, _UNSUPPORTED_OPERATION, _CONTENT_TYPE_NOT_SUPPORTED, _VERSION_NOT_SUPPORTED = (
    -32001, -32003, -32004, -32005, -32009)
_META_VERSION = "io.modelcontextprotocol/protocolVersion"
_META_CAPABILITIES = "io.modelcontextprotocol/clientCapabilities"
_META_SERVER_INFO = "io.modelcontextprotocol/serverInfo"
_LIST_TTL = 3_600_000
_JSON = "application/json"
_OPAQUE = re.compile(r"[A-Za-z0-9_-]+")
_INTEGER = re.compile(r"-?(0|[1-9][0-9]{0,17})")

_TOOL_DESCRIPTION = ("Carries one record push (a pushed record, a stamp proof, or both) as opaque octets in base64url "
                     "without padding, and returns the receiver's answer for each part pushed. The answer is an "
                     "acknowledgment, never evidence of what the receiver holds.")
_A2A_DESCRIPTION = ("Takes record pushes. A push is one Message with one data Part holding {\"record\", "
                    "\"stamp_proof\"}, each base64url octets without padding; the answer is one task Artifact with one "
                    "data Part holding the answer for each part pushed. The answer is an acknowledgment, never "
                    "evidence of what the receiver holds.")

_TOOL = {
    "name": MCP_TOOL_RECORD_PUSH,
    "title": "Record push",
    "description": _TOOL_DESCRIPTION,
    "inputSchema": {
        "type": "object",
        "properties": {"record": {"type": "string", "pattern": "^[A-Za-z0-9_-]+$"},
                       "stamp_proof": {"type": "string", "pattern": "^[A-Za-z0-9_-]+$"}},
        "minProperties": 1,
        "additionalProperties": False,
    },
    "outputSchema": {
        "type": "object",
        "properties": {
            "record": {
                "type": "object",
                "properties": {"digest": {"type": "string"}, "stored": {"type": "boolean"},
                               "duplicate": {"type": "boolean"}, "deleted": {"type": "boolean"},
                               "counted": {"type": "boolean"}, "ask_for_book": {"type": "boolean"},
                               "refused": {"enum": ["", "invalid", "refused", "over_limit", "busy"]}},
                "required": ["digest", "stored", "duplicate", "deleted", "counted", "ask_for_book", "refused"],
                "additionalProperties": False,
            },
            "stamp_proof": {
                "type": "object",
                "properties": {"accepted": {"type": "boolean"}, "duplicate": {"type": "boolean"},
                               "refused": {"enum": ["", "invalid", "refused"]}},
                "required": ["accepted", "duplicate", "refused"],
                "additionalProperties": False,
            },
        },
        "minProperties": 1,
        "additionalProperties": False,
    },
}


def _marshal(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def _error(id_: Any, code: int, message: str, data: Any = None) -> bytes:
    e: dict = {"code": code, "message": message}
    if data is not None:
        e["data"] = data
    return _marshal({"jsonrpc": "2.0", "id": id_, "error": e})


def _decode(message: bytes) -> Any:
    return strict_json(message)  # as Go's evidencerequest.DecodeJSON


def _request_id(value: Any) -> tuple[Any, bool]:
    if isinstance(value, str):
        return value, True
    if isinstance(value, int) and not isinstance(value, bool) and _INTEGER.fullmatch(str(value)):
        return value, True
    return None, False


def _strict_decode(text: Any) -> bytes | None:
    """One push member: a base64url string without padding that decodes
    strictly to at least one octet (RP §2.1)."""
    if not isinstance(text, str) or not _OPAQUE.fullmatch(text) or len(text) % 4 == 1:
        return None
    try:
        octets = base64.urlsafe_b64decode(text + "=" * (-len(text) % 4))
    except (binascii.Error, ValueError):
        return None
    if not octets or base64.urlsafe_b64encode(octets).rstrip(b"=").decode() != text:
        return None  # not canonical: trailing bits set
    return octets


def _read_push(obj: Any) -> dict | None:
    if not isinstance(obj, dict) or not 1 <= len(obj) <= 2:
        return None
    out = {}
    for name, value in obj.items():
        if name not in ("record", "stamp_proof"):
            return None
        octets = _strict_decode(value)
        if octets is None:
            return None
        out[name] = octets
    return out


def _class(error: Exception | None) -> str | None:
    """A part's refusal class (SF §6.5); ``None`` for an error that is not one."""
    if error is None:
        return ""
    for cls, name in ((RefusedError, "refused"), (OverLimitError, "over_limit"), (BusyError, "busy"),
                      (InvalidError, "invalid")):
        if isinstance(error, cls):
            return name
    return None


class _Fault(Exception):
    """The receiver could not complete a push for a reason of its own."""


def _answer(shelf: Any, push: dict) -> dict:
    record, proof_octets = push.get("record"), push.get("stamp_proof")
    proof = None
    if proof_octets is not None:
        # Read by SF §5.2; octets that are not that JSON read as an empty
        # proof, which SF §6.3 step 1 refuses as invalid.
        try:
            proof = StampProof.loads(proof_octets)
        except InvalidError:
            proof = StampProof(b"", b"", {}, b"")
    try:
        result = shelf.receive(record, proof)
    except Exception as exc:  # noqa: BLE001 - a store, profile or release fault: no answer (RP §2.3 rule 4)
        raise _Fault from exc
    answer: dict = {}
    if result.record is not None:
        r = result.record
        refused = _class(r.refused)
        if refused is None:
            raise _Fault
        answer["record"] = {"digest": r.digest, "stored": r.stored, "duplicate": r.duplicate, "deleted": r.deleted,
                            "counted": r.counted, "ask_for_book": r.ask_for_book, "refused": refused}
    if result.proof is not None:
        p = result.proof
        refused = _class(p.refused)
        if refused is None or refused in ("over_limit", "busy"):
            raise _Fault
        answer["stamp_proof"] = {"accepted": p.accepted, "duplicate": p.duplicate, "refused": refused}
    return answer


def _max_message(shelf: Any) -> int:
    return 2 * shelf._receive.bounds.with_defaults().max_record_bytes + (64 << 10)


def _envelope(message: bytes, shelf: Any) -> tuple[dict | None, bytes | None]:
    """The JSON-RPC message of a received one, or the bytes to send back."""
    if len(message) > _max_message(shelf):
        return None, _error(None, _INVALID_REQUEST, "message too large")
    try:
        root = _decode(message)
    except (ValueError, RecursionError):
        return None, _error(None, _PARSE_ERROR, "parse error")
    if not isinstance(root, dict):
        return None, _error(None, _INVALID_REQUEST, "not a JSON-RPC request object")
    return root, None


class PushMCPServer:
    """The record push over MCP (RP §3): the tool ``record_push``, and the
    ``server/discover`` and ``tools/list`` requests MCP requires around it.
    ``handle`` takes one received JSON-RPC message and returns the message to
    send back, or ``None`` for a notification."""

    def __init__(self, shelf: Any, server_version: str = ""):
        self._shelf = shelf
        self._info = {"name": "evidencebook", "version": server_version or "unversioned"}

    def _result(self, id_: Any, result: dict) -> bytes:
        result = dict(result, resultType="complete", _meta={_META_SERVER_INFO: self._info})
        return _marshal({"jsonrpc": "2.0", "id": id_, "result": result})

    def handle(self, message: bytes) -> bytes | None:
        msg, reply = _envelope(message, self._shelf)
        if msg is None:
            return reply
        method = msg.get("method") if isinstance(msg.get("method"), str) else None
        if "id" not in msg and method is not None:
            return None
        id_, ok = _request_id(msg.get("id"))
        if not ok or msg.get("jsonrpc") != "2.0" or method is None:
            return _error(None, _INVALID_REQUEST, "invalid JSON-RPC request")
        params: Any = {}
        if "params" in msg:
            params = msg["params"]
            if not isinstance(params, dict):
                return _error(id_, _INVALID_PARAMS, "params is not an object")
        if method == "server/discover":
            return self._result(id_, {"supportedVersions": [MCP_PROTOCOL_VERSION],
                                      "capabilities": {"tools": {"listChanged": False}}, "ttlMs": _LIST_TTL,
                                      "cacheScope": "public"})
        if method not in ("tools/list", "tools/call"):
            return _error(id_, _METHOD_NOT_FOUND, "method not found")
        meta = params.get("_meta") if isinstance(params.get("_meta"), dict) else {}
        version = meta.get(_META_VERSION)
        if not isinstance(version, str) or not isinstance(meta.get(_META_CAPABILITIES), dict):
            return _error(id_, _INVALID_PARAMS, "_meta lacks the protocol version or client capabilities")
        if version != MCP_PROTOCOL_VERSION:
            return _error(id_, _UNSUPPORTED_PROTOCOL_VERSION, "unsupported protocol version",
                          {"supported": [MCP_PROTOCOL_VERSION], "requested": version})
        if method == "tools/list":
            return self._result(id_, {"tools": [_TOOL], "ttlMs": _LIST_TTL, "cacheScope": "public"})
        if params.get("name") != MCP_TOOL_RECORD_PUSH:
            return _error(id_, _INVALID_PARAMS, "unknown tool")
        arguments: Any = {}
        if "arguments" in params:
            arguments = params["arguments"]
            if not isinstance(arguments, dict):
                return _error(id_, _INVALID_PARAMS, "arguments is not an object")
        push = _read_push(arguments)
        if push is None:
            return _error(id_, _INVALID_PARAMS, "arguments do not match the record_push input schema")
        try:
            answer = _answer(self._shelf, push)
        except _Fault:
            return _error(id_, _INTERNAL_ERROR, "internal error")
        return self._result(id_, {"content": [], "isError": False, "structuredContent": answer})


def a2a_push_extension() -> bytes:
    """The Agent Card extension entry that advertises a push (RP §4.1), the
    same for every caller: a host that also serves the evidence request
    carriage adds it to the one card's ``capabilities.extensions``."""
    return _marshal({"uri": A2A_PUSH_EXTENSION_URI, "description": _A2A_DESCRIPTION, "required": False})


def _identifier() -> str:
    return secrets.token_hex(16)  # 128 random bits: no count, no time (RP §4.3)


def _a2a_push_message(message: dict) -> tuple[dict | None, int]:
    extensions, parts = message.get("extensions"), message.get("parts")
    if (message.get("role") != "ROLE_USER" or not isinstance(extensions, list)
            or A2A_PUSH_EXTENSION_URI not in extensions or not isinstance(parts, list) or len(parts) != 1):
        return None, _INVALID_PARAMS
    part = parts[0]
    if not isinstance(part, dict) or any(k in part for k in ("text", "url", "raw")):
        return None, _INVALID_PARAMS
    if not isinstance(part.get("data"), dict):
        return None, _INVALID_PARAMS
    if part.get("mediaType") != _JSON:
        return None, _CONTENT_TYPE_NOT_SUPPORTED
    push = _read_push(part["data"])
    return (push, 0) if push is not None else (None, _INVALID_PARAMS)


class PushA2AServer:
    """The record push over A2A (RP §4): ``SendMessage`` answered with a
    completed Task holding the answer in one Artifact. It keeps no task and
    mints unpredictable identifiers. ``handle`` takes the JSON-RPC body with
    the ``A2A-Version`` value (``""`` when absent) and the ``A2A-Extensions``
    URIs, and returns the body to send back (``None`` for a notification) and
    the extensions it activated."""

    def __init__(self, shelf: Any):
        self._shelf = shelf

    def handle(self, body: bytes, version: str = "", extensions: list[str] | None = None) -> tuple[bytes | None, list[str]]:
        msg, reply = _envelope(body, self._shelf)
        if msg is None:
            return reply, []
        method = msg.get("method") if isinstance(msg.get("method"), str) else None
        if "id" not in msg and method is not None:
            return None, []
        id_, ok = _request_id(msg.get("id"))
        if not ok or msg.get("jsonrpc") != "2.0" or method is None:
            return _error(None, _INVALID_REQUEST, "invalid JSON-RPC request"), []

        def fail(code: int, message: str, data: Any = None) -> tuple[bytes, list[str]]:
            return _error(id_, code, message, data), []

        if version != A2A_PROTOCOL_VERSION:
            return fail(_VERSION_NOT_SUPPORTED, "unsupported A2A version",
                        {"supported": [A2A_PROTOCOL_VERSION], "requested": version})
        params: Any = {}
        if "params" in msg:
            params = msg["params"]
            if not isinstance(params, dict):
                return fail(_INVALID_PARAMS, "params is not an object")
        if method in ("SendStreamingMessage", "SubscribeToTask", "GetExtendedAgentCard"):
            return fail(_UNSUPPORTED_OPERATION, "not supported by this agent")
        if method in ("GetTask", "CancelTask"):
            return fail(_TASK_NOT_FOUND, "task not found")
        if method == "ListTasks":
            return _marshal({"jsonrpc": "2.0", "id": id_, "result": {
                "tasks": [], "nextPageToken": "", "pageSize": 0, "totalSize": 0}}), []
        if method in ("CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
                      "ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig"):
            return fail(_PUSH_NOT_SUPPORTED, "push notifications are not supported")
        if method != "SendMessage":
            return fail(_METHOD_NOT_FOUND, "method not found")
        if A2A_PUSH_EXTENSION_URI not in (extensions or []):
            return fail(_INVALID_PARAMS, "the record push extension is not activated in A2A-Extensions")
        message = params.get("message")
        if not isinstance(message, dict):
            return fail(_INVALID_PARAMS, "params has no message")
        if "taskId" in message:
            return fail(_TASK_NOT_FOUND, "task not found")
        push, code = _a2a_push_message(message)
        if push is None:
            return fail(code, "the message is not a record push under the extension")
        if "configuration" in params:
            c = params["configuration"]
            if not isinstance(c, dict):
                return fail(_INVALID_PARAMS, "configuration is not an object")
            if "taskPushNotificationConfig" in c:
                return fail(_PUSH_NOT_SUPPORTED, "push notifications are not supported")
            if "acceptedOutputModes" in c:
                modes = c["acceptedOutputModes"]
                if not isinstance(modes, list):
                    return fail(_INVALID_PARAMS, "acceptedOutputModes is not an array")
                if _JSON not in modes:
                    return fail(_CONTENT_TYPE_NOT_SUPPORTED, "the answer is only application/json")
        try:
            answer = _answer(self._shelf, push)
        except _Fault:
            return fail(_INTERNAL_ERROR, "internal error")
        context_id = message.get("contextId")
        task = {
            "id": _identifier(),
            "contextId": context_id if isinstance(context_id, str) and context_id else _identifier(),
            "status": {"state": "TASK_STATE_COMPLETED"},
            "artifacts": [{"artifactId": _identifier(), "extensions": [A2A_PUSH_EXTENSION_URI],
                           "parts": [{"data": answer, "mediaType": _JSON}]}],
        }
        return _marshal({"jsonrpc": "2.0", "id": id_, "result": {"task": task}}), [A2A_PUSH_EXTENSION_URI]
