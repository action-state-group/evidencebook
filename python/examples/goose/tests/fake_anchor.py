# SPDX-License-Identifier: Apache-2.0
"""A local stand-in for capsule-anchor's ``POST /transparency/register-statement``:
it logs the statement's entry (SHA-256 over its Sig_structure) in a one-leaf
tree and answers an RFC 9942 Receipt under its own EdDSA key, in the JSON
form capsule-anchor uses. For tests only; it keeps no log."""
from __future__ import annotations

import base64
import hashlib
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

import cbor2
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

IAT = 1_795_000_000


class FakeAnchor:
    def __init__(self, seed: int = 11, mode: str = "ok"):
        self.key = Ed25519PrivateKey.from_private_bytes(bytes([seed]) * 32)
        self.mode = mode  # "ok", "redirect", "junk", "huge", "slow", "slow-headers" or "bad-receipt"
        self.posted: list[bytes] = []
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                if self.path != "/transparency/register-statement":
                    self.send_response(404)
                    self.end_headers()
                    return
                outer.posted.append(base64.b64decode(json.loads(body)["signed_statement_b64"]))
                if outer.mode == "redirect":
                    self.send_response(302)
                    self.send_header("Location", "http://127.0.0.1:1/elsewhere")
                    self.end_headers()
                    return
                if outer.mode == "slow-headers":
                    time.sleep(6)
                    return
                if outer.mode == "slow":
                    # headers promise a body, then one byte at a time, longer than the total deadline
                    self.send_response(200)
                    self.send_header("Content-Length", "100000")
                    self.end_headers()
                    try:
                        for _ in range(200):
                            self.wfile.write(b"x")
                            self.wfile.flush()
                            time.sleep(0.1)
                    except OSError:
                        pass
                    return
                if outer.mode == "huge":
                    payload = b" " * (2 << 20)
                elif outer.mode == "junk":
                    payload = b"not json"
                else:
                    receipt = outer.receipt_for(outer.entry_of(outer.posted[-1]))
                    if outer.mode == "bad-receipt":
                        receipt = outer.receipt_for(b"\x01" * 32)
                    payload = json.dumps({"receipt_b64": base64.b64encode(receipt).decode(),
                                          "entry_hash_scheme": "sig_structure"}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        self._server = HTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self._server.server_port}"
        threading.Thread(target=self._server.serve_forever, daemon=True).start()

    def close(self) -> None:
        self._server.shutdown()
        self._server.server_close()

    @staticmethod
    def spki_for(seed: int) -> str:
        """The hex DER public key of the service ``FakeAnchor(seed)`` is, without starting one."""
        key = Ed25519PrivateKey.from_private_bytes(bytes([seed]) * 32)
        return "302a300506032b6570032100" + key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw).hex()

    @property
    def public_spki_hex(self) -> str:
        return self.spki_for_key(self.key)

    @staticmethod
    def spki_for_key(key: Ed25519PrivateKey) -> str:
        return "302a300506032b6570032100" + key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw).hex()

    @staticmethod
    def entry_of(statement: bytes) -> bytes:
        protected, _, payload, _ = cbor2.loads(statement).value
        return hashlib.sha256(cbor2.dumps(["Signature1", protected, b"", payload], canonical=True)).digest()

    def receipt_for(self, entry: bytes) -> bytes:
        leaf = hashlib.sha256(b"\x00" + entry).digest()
        protected = cbor2.dumps({1: -8, 395: 1, 15: {6: IAT}}, canonical=True)
        signature = self.key.sign(cbor2.dumps(["Signature1", protected, b"", leaf], canonical=True))
        proof = cbor2.dumps([1, 0, []], canonical=True)
        return cbor2.dumps(cbor2.CBORTag(18, [protected, {396: {-1: [proof]}}, None, signature]), canonical=True)
