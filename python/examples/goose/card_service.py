# SPDX-License-Identifier: Apache-2.0
"""The one transparency service a Goose node registers its History card on,
when its host names one (agent-action-capsule ``standard-history-v0`` §13).

Registration is opt-in and has no default: with no service file there is no
URL here, nothing registers and nothing calls out. The service file is the
host's own choice:

    {"name": "capsule-anchor (Action State Group)",
     "operator": "Action State Group",
     "url": "https://witness.agentactioncapsule.org",
     "keys": ["<hex DER SubjectPublicKeyInfo of the key it signs Receipts under>"]}

``keys`` is the relying party's transparency policy for this service (IP1
§6.2.2's witness policy form); the service is named in the policy and in its
Receipt, never in the card. ``operator`` says who runs the service: a service
the holder operates confers nothing (§13.2), so a demo whose holder and
service are both ours must say so.

The post is capsule-anchor's ``POST /transparency/register-statement``, a JSON
body ``{"signed_statement_b64"}`` answered with ``{"receipt_b64", ...}`` (not
SCRAPI's ``POST /entries``). The service's Receipt is checked by
:func:`card_registration.attach_card_receipt` before it is kept,
whatever the service says about its own entry hash.
"""
from __future__ import annotations

import base64
import binascii
import http.client
import json
import os
import socket
import threading
import urllib.parse
from dataclasses import dataclass
from pathlib import Path
from typing import Any

#: A bound on what a service may send back for one registration.
MAX_RESPONSE_BYTES = 1 << 20
#: Each socket read, and the whole registration (a service that trickles bytes still ends).
TIMEOUT_SECONDS = 30
TOTAL_SECONDS = 60


class ServiceError(ValueError):
    """The service file is not usable, or the service did not give a Receipt."""


@dataclass(frozen=True)
class Service:
    name: str
    operator: str
    url: str
    keys: tuple[str, ...]

    @property
    def policy(self) -> dict[str, Any]:
        """The relying party's transparency policy: this one service and its keys."""
        return {"witnesses": [{"name": self.name, "keys": list(self.keys)}]}


def load_service(path: str | Path) -> Service:
    """Read a service file; every member is required, so nothing is assumed."""
    try:
        doc = json.loads(Path(path).read_text())
    except (OSError, ValueError) as exc:
        raise ServiceError(f"service file {path}: {exc}") from None
    if not isinstance(doc, dict):
        raise ServiceError("the service file is not a JSON object")
    name, operator, url, keys = (doc.get(k) for k in ("name", "operator", "url", "keys"))
    if not all(isinstance(v, str) and v for v in (name, operator, url)):
        raise ServiceError("the service file needs name, operator and url, each a non-empty string")
    if not isinstance(keys, list) or not keys or not all(isinstance(k, str) and k for k in keys):
        raise ServiceError("the service file needs keys: the hex DER public keys its Receipts verify under")
    scheme = urllib.parse.urlsplit(url).scheme
    if scheme not in ("https", "http"):
        raise ServiceError("the service url is not http or https")
    return Service(name, operator, url.rstrip("/"), tuple(keys))


class AnchorRegistrar:
    """A :class:`card_registration.CardStatementRegistrar` for a
    capsule-anchor service."""

    def __init__(self, service: Service):
        self._service = service

    def register_statement(self, statement: bytes) -> bytes:
        url = urllib.parse.urlsplit(self._service.url)
        make = http.client.HTTPSConnection if url.scheme == "https" else http.client.HTTPConnection
        conn = make(url.netloc, timeout=TIMEOUT_SECONDS)
        path = (url.path.rstrip("/") or "") + "/transparency/register-statement"
        body = json.dumps({"signed_statement_b64": base64.b64encode(statement).decode()}).encode()
        outcome: list[Any] = []
        sockets: list[socket.socket] = []  # http.client lets go of conn.sock once a response owns it
        cancelled = threading.Event()  # set at the deadline: a worker still connecting then must not post

        def post() -> None:
            try:
                conn.connect()
                sockets.append(conn.sock)
                if cancelled.is_set():
                    raise ServiceError("cancelled at the deadline, before anything was sent")
                conn.request("POST", path, body=body,
                             headers={"Content-Type": "application/json", "Accept": "application/json"})
                response = conn.getresponse()
                if response.status != 200:  # a redirect is not followed: the one URL the host named
                    raise ServiceError(f"{self._service.url}: HTTP {response.status}")
                raw = b""
                while len(raw) <= MAX_RESPONSE_BYTES:
                    chunk = response.read(8192)
                    if not chunk:
                        break
                    raw += chunk
                outcome.append(raw)
            except BaseException as exc:  # handed to the caller below
                outcome.append(exc)

        # The whole registration, connect to last byte, ends at TOTAL_SECONDS: a socket timeout bounds one
        # read, not a service that drips bytes. At the deadline the connection is shut down, which ends the
        # worker's blocked read, and the worker is joined.
        worker = threading.Thread(target=post, name="card-registration", daemon=True)
        worker.start()
        worker.join(TOTAL_SECONDS)
        if worker.is_alive():
            cancelled.set()
            for sock in sockets:
                try:
                    sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
            conn.close()
            worker.join(5)
            for sock in sockets:  # a connect that finished while the deadline passed
                try:
                    sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
            raise ServiceError(f"{self._service.url}: no complete answer in {TOTAL_SECONDS} seconds")
        conn.close()
        raw = outcome[0]
        if isinstance(raw, ServiceError):
            raise raw
        if isinstance(raw, (http.client.HTTPException, OSError)):
            raise ServiceError(f"{self._service.url}: {raw}") from None
        if isinstance(raw, BaseException):
            raise raw
        if len(raw) > MAX_RESPONSE_BYTES:
            raise ServiceError("the service's answer is over 1 MiB")
        try:
            return base64.b64decode(json.loads(raw)["receipt_b64"], validate=True)
        except (ValueError, KeyError, TypeError, binascii.Error):
            raise ServiceError("the service's answer holds no receipt_b64") from None


def reserve_out_file(path: str | Path):
    """Create the file the card statement will be written to, exclusively and *before* anything is posted,
    and return it open for writing. A registration is permanent, so a file that cannot be created (a missing
    directory, no permission) or that already exists (its Receipt would be lost; another call may hold it) is
    refused up front, never after the post, and two calls for one path cannot both post."""
    out = Path(path).expanduser()
    try:
        return out, open(out, "xb")
    except FileExistsError:
        raise ServiceError(f"{out} already exists; a registered statement is never overwritten") from None
    except OSError as exc:
        raise ServiceError(f"{out} cannot be written: {exc}") from None
