# SPDX-License-Identifier: Apache-2.0
"""A NIP-01 relay in memory for the mailbox tests."""
from __future__ import annotations

import json
import threading

import pytest

websockets_server = pytest.importorskip("websockets.sync.server")


class FakeRelay:
    """A NIP-01 relay in memory: it stores every event it is sent and answers a REQ
    with the stored events that match its filter, then EOSE. It checks nothing."""

    def __init__(self) -> None:
        self.events: list[dict] = []
        self._server = websockets_server.serve(self._handle, "127.0.0.1", 0)
        self.url = f"ws://127.0.0.1:{self._server.socket.getsockname()[1]}"
        threading.Thread(target=self._server.serve_forever, daemon=True).start()

    def _handle(self, ws) -> None:
        for raw in ws:
            msg = json.loads(raw)
            if msg[0] == "EVENT":
                if all(e["id"] != msg[1]["id"] for e in self.events):
                    self.events.append(msg[1])
                ws.send(json.dumps(["OK", msg[1]["id"], True, ""]))
            elif msg[0] == "REQ":
                for ev in self.events:
                    if self._matches(ev, msg[2]):
                        ws.send(json.dumps(["EVENT", msg[1], ev]))
                ws.send(json.dumps(["EOSE", msg[1]]))

    @staticmethod
    def _matches(ev: dict, flt: dict) -> bool:
        if "kinds" in flt and ev["kind"] not in flt["kinds"]:
            return False
        return all(any(t[0] == key[1:] and t[1] in want for t in ev["tags"])
                   for key, want in flt.items() if key.startswith("#"))

    def close(self) -> None:
        self._server.shutdown()
