# SPDX-License-Identifier: Apache-2.0
"""The demo's local web server: the ARD catalogue and the sellers' commitments (what Maya's lookup
fetches) and the popup the presenter picks in. It listens on 127.0.0.1 only."""
from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent


class Server:
    def __init__(self, port: int = 0):
        self.files: dict[str, bytes] = {}
        self.state: dict = {"phase": "waiting"}
        self.picked: str | None = None
        self.pick_event = threading.Event()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_a):  # the terminal is the audience's screen
                pass

            def _send(self, body: bytes, kind: str = "application/json", code: int = 200) -> None:
                self.send_response(code)
                self.send_header("Content-Type", kind)
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self) -> None:  # noqa: N802
                path = self.path.split("?")[0]
                if path in outer.files:
                    self._send(outer.files[path])
                elif path == "/state":
                    self._send(json.dumps(outer.state).encode())
                elif path == "/":
                    self._send((HERE / "popup.html").read_bytes(), "text/html; charset=utf-8")
                else:
                    self._send(b"{}", code=404)

            def do_POST(self) -> None:  # noqa: N802
                if self.path == "/pick" and outer.state.get("phase") == "popup" and not outer.pick_event.is_set():
                    seller = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0)) or 0) or b"{}").get("seller")
                    if seller in {o["id"] for o in outer.state.get("options", [])}:
                        outer.picked = seller
                        outer.pick_event.set()
                        self._send(b'{"ok":true}')
                        return
                self._send(b'{"ok":false}', code=400)

        self.httpd = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()

    def popup(self, options: list[dict], header: dict) -> None:
        """Show the popup: ``options`` side by side. The pick comes back through :attr:`pick_event`."""
        self.picked = None
        self.pick_event.clear()
        self.state = {"phase": "popup", "options": options, **header}

    def finish(self, message: str) -> None:
        self.state = {**self.state, "phase": "done", "message": message}
