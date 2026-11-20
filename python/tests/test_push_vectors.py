# SPDX-License-Identifier: Apache-2.0
"""testdata/shelf/receive-carriage: the MCP and A2A entries of Shelf.receive
(evidencebook#577), as Go's interop_push_vectors_test.go runs them. Each track
runs on a fresh shelf with the receive vectors' profile, bounds and retention;
every ``call`` case must return its ``expect`` (compared as JSON, an A2A task's
identifiers masked, an MCP result's server info removed)."""
from __future__ import annotations

import json
import re
from datetime import timedelta
from pathlib import Path

import pytest

from evidencebook.push import (A2A_PUSH_EXTENSION_URI, MCP_PROTOCOL_VERSION, MCP_TOOL_RECORD_PUSH, PushA2AServer,
                               PushMCPServer, a2a_push_extension)
from evidencebook.shelf_receive import ReceiveBounds, ReceiveConfig

from receive_support import ReceiveFixture
from test_shelf_receive_vectors import VectorProfile, _at

ROOT = Path(__file__).resolve().parents[2] / "testdata" / "shelf"
_ID = re.compile(r"[0-9a-f]{32}")


def _script() -> dict:
    return json.loads((ROOT / "receive" / "script.json").read_text())


def _config(script: dict) -> ReceiveConfig:
    return ReceiveConfig(VectorProfile(script["profile"]), ReceiveBounds(**script["bounds"]),
                         timedelta(seconds=script["retention_seconds"]))


def _mask(body: bytes) -> dict:
    m = json.loads(body)
    task = m.get("result", {}).get("task")
    if task:
        for holder, key in [(task, "id"), (task, "contextId")] + [(a, "artifactId") for a in task["artifacts"]]:
            assert _ID.fullmatch(holder[key]), holder[key]
            holder[key] = "<id>"
    return m


def _call(server, carriage: str, case: dict):
    body = json.dumps(case["request"]).encode()
    if carriage == "mcp":
        out = server.handle(body)
        if out is None:
            return None
        m = json.loads(out)
        m.get("result", {}).pop("_meta", None)
        return m
    out, _extensions = server.handle(body, case.get("a2a_version", ""), case.get("a2a_extensions"))
    return _mask(out) if out is not None else None


@pytest.mark.parametrize("carriage", ["mcp", "a2a"])
def test_the_carriage_vectors(tmp_path, carriage):
    script = _script()
    vectors = json.loads((ROOT / "receive-carriage" / "script.json").read_text())
    track = next(t for t in vectors["tracks"] if t["carriage"] == carriage)
    fx = ReceiveFixture(tmp_path, _config(script))
    try:
        shelf = fx.open()
        server = PushMCPServer(shelf, "v") if carriage == "mcp" else PushA2AServer(shelf)
        for i, case in enumerate(track["cases"]):
            fx.clock.t = _at(case["at"])
            if case["op"] == "call":
                assert _call(server, carriage, case) == case["expect"], f"{carriage} case {i}: {case.get('note')}"
            elif case["op"] == "forget":
                try:
                    shelf.forget(case["deal"], fx.clock.t, lambda _deal, s=bool(case.get("settled")): s)
                except Exception:  # noqa: BLE001 -- a refusal is part of the script
                    pass
            elif case["op"] == "reopen":
                shelf.release()
                shelf = fx.open()
                server = PushMCPServer(shelf, "v") if carriage == "mcp" else PushA2AServer(shelf)
    finally:
        fx.close()


def _push(shelf_server, args, carriage="mcp"):
    if carriage == "mcp":
        req = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
            "_meta": {"io.modelcontextprotocol/protocolVersion": MCP_PROTOCOL_VERSION,
                      "io.modelcontextprotocol/clientCapabilities": {}},
            "name": MCP_TOOL_RECORD_PUSH, "arguments": args}}
        return json.loads(shelf_server.handle(json.dumps(req).encode()))
    req = {"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": {"message": {
        "messageId": "m", "role": "ROLE_USER", "extensions": [A2A_PUSH_EXTENSION_URI],
        "parts": [{"mediaType": "application/json", "data": args}]}}}
    body, _ = shelf_server.handle(json.dumps(req).encode(), "1.0", [A2A_PUSH_EXTENSION_URI])
    return json.loads(body)


def test_a_receiver_fault_is_an_internal_error_not_an_answer(tmp_path):
    script = _script()
    fx = ReceiveFixture(tmp_path, _config(script))
    try:
        shelf = fx.open()
        shelf.release()
        import base64
        args = {"record": base64.urlsafe_b64encode(script["records"]["a1"].encode()).rstrip(b"=").decode()}
        for server, carriage in ((PushMCPServer(shelf), "mcp"), (PushA2AServer(shelf), "a2a")):
            out = _push(server, args, carriage)
            assert out["error"]["code"] == -32603 and "result" not in out
    finally:
        fx.close()


def test_the_message_bound_and_over_limit(tmp_path):
    script = _script()
    fx = ReceiveFixture(tmp_path, _config(script))
    try:
        shelf = fx.open()
        server = PushMCPServer(shelf)
        too_big = b'{"x":"' + b"A" * (2 * script["bounds"]["max_record_bytes"] + (64 << 10)) + b'"}'
        assert json.loads(server.handle(too_big))["error"]["code"] == -32600
        body, _ = PushA2AServer(shelf).handle(too_big, "1.0", [A2A_PUSH_EXTENSION_URI])
        assert json.loads(body)["error"]["code"] == -32600
        import base64
        big = base64.urlsafe_b64encode(bytes(script["bounds"]["max_record_bytes"] + 1)).rstrip(b"=").decode()
        out = _push(server, {"record": big})
        assert out["result"]["structuredContent"]["record"]["refused"] == "over_limit"
    finally:
        fx.close()


def test_discovery_is_fixed():
    ext = json.loads(a2a_push_extension())
    assert ext["uri"] == A2A_PUSH_EXTENSION_URI and ext["required"] is False and "params" not in ext
