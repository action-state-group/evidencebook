# SPDX-License-Identifier: Apache-2.0
"""The demo as three separate processes, as three Goose windows run it: Maya, Alder and Kestrel each
a desktop_server.py of their own role (own keys and book under the demo home), meeting only through the
stage (relay, catalogue, wire files). Driven over MCP stdio on a fake rail.

Needs ``CAPSULE_ENGINE`` and ``ARD_SKILLS`` like test_demo.py; about three minutes."""
from __future__ import annotations

import asyncio
import contextlib
import os
import socket
import subprocess
import sys
from pathlib import Path

import pytest

from conftest import engine_only

pytestmark = engine_only

HERE = Path(__file__).resolve().parents[1]


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture(scope="module")
def stage_env(tmp_path_factory):
    import seed
    from world import World

    home = tmp_path_factory.mktemp("three-windows")
    with World(home) as world:
        seed.seed(world, say=lambda *_: None, kestrel_card="sentinel")
    env = {**os.environ, "ARD_HOME": str(home), "ARD_RAIL": "fake", "ARD_APPROVAL": "none", "ARD_KESTREL_CARD": "sentinel",
           "ARD_RELAY_PORT": str(_free_port()), "ARD_STAGE_PORT": str(_free_port())}
    stage = subprocess.Popen([sys.executable, str(HERE / "stage.py"), str(home)], env=env, stdout=subprocess.DEVNULL)
    yield env
    stage.terminate()
    stage.wait(timeout=10)


async def _call(session, tool: str, **args) -> str:
    return (await session.call_tool(tool, args)).content[0].text


def test_three_windows_run_the_story(stage_env):
    from mcp import ClientSession, StdioServerParameters
    from mcp.client.stdio import stdio_client

    async def run() -> dict[str, str]:
        out: dict[str, str] = {}
        async with contextlib.AsyncExitStack() as stack:
            sessions = {}
            for role in ("alder", "kestrel", "maya"):
                params = StdioServerParameters(command=sys.executable, args=[str(HERE / "desktop_server.py")],
                                               env={**stage_env, "ARD_ROLE": role})
                read, write = await stack.enter_async_context(stdio_client(params))
                s = await stack.enter_async_context(ClientSession(read, write))
                await s.initialize()
                sessions[role] = s
            tools = {r: sorted(t.name for t in (await s.list_tools()).tools) for r, s in sessions.items()}
            assert tools["alder"] == tools["kestrel"] == ["ard_seller_activity"]
            assert "ard_pay" in tools["maya"] and "ard_seller_activity" not in tools["maya"]
            maya = sessions["maya"]
            out["find"] = await _call(maya, "ard_find_sellers")
            out["history"] = await _call(maya, "ard_show_history")
            out["assess"] = await _call(maya, "ard_assess", max_adverse_permille=50)
            out["kestrel"] = await _call(maya, "ard_pick", seller="kestrel")
            out["kestrel_side"] = await _call(sessions["kestrel"], "ard_seller_activity")
            out["pick"] = await _call(maya, "ard_pick", seller="alder")
            out["pay"] = await _call(maya, "ard_pay", seller="alder")
            await asyncio.sleep(1)
            out["alder_side"] = await _call(sessions["alder"], "ard_seller_activity")
        return out

    out = asyncio.run(run())
    assert "Alder Print Co." in out["find"] and "Kestrel Print" in out["find"] and "commitment verified" in out["find"]
    assert "3 of 48 deals did not hold" in out["history"] and "✗" not in out["history"]
    assert "Kestrel Print      LIMIT:" in out["assess"].split("4b  Assessment again")[1]
    assert "no deal opens and nothing is paid" in out["kestrel"]
    assert "answered a signed evidence request" in out["kestrel_side"] and "a buyer asks to buy" not in out["kestrel_side"]
    assert "approval gate off" in out["pay"] and "invoice 289 msat" in out["pay"] and "deal closed with Alder Print Co." in out["pay"]
    assert "a buyer asks to buy" in out["alder_side"] and "deal closed: both closes name 289 msat" in out["alder_side"]
