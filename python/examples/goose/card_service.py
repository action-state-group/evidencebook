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
import json
import os
import threading
import urllib.error
import urllib.parse
import urllib.request
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


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):  # a registration goes to the one URL the host named
        return None


class AnchorRegistrar:
    """A :class:`card_registration.CardStatementRegistrar` for a
    capsule-anchor service."""

    def __init__(self, service: Service):
        self._service = service
        self._opener = urllib.request.build_opener(_NoRedirect)

    def register_statement(self, statement: bytes) -> bytes:
        body = json.dumps({"signed_statement_b64": base64.b64encode(statement).decode()}).encode()
        request = urllib.request.Request(
            self._service.url + "/transparency/register-statement", data=body, method="POST",
            headers={"Content-Type": "application/json", "Accept": "application/json"})
        outcome: list[Any] = []

        def post() -> None:
            try:
                with self._opener.open(request, timeout=TIMEOUT_SECONDS) as response:
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
        # read, not a service that drips bytes. The worker is a daemon, so a stuck one never holds the process.
        worker = threading.Thread(target=post, daemon=True)
        worker.start()
        worker.join(TOTAL_SECONDS)
        if not outcome:
            raise ServiceError(f"{self._service.url}: no complete answer in {TOTAL_SECONDS} seconds")
        raw = outcome[0]
        if isinstance(raw, (urllib.error.URLError, OSError)):
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
