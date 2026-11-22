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
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

#: A bound on what a service may send back for one registration.
MAX_RESPONSE_BYTES = 1 << 20
TIMEOUT_SECONDS = 30


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
        try:
            with self._opener.open(request, timeout=TIMEOUT_SECONDS) as response:
                raw = response.read(MAX_RESPONSE_BYTES + 1)
        except (urllib.error.URLError, OSError) as exc:
            raise ServiceError(f"{self._service.url}: {exc}") from None
        if len(raw) > MAX_RESPONSE_BYTES:
            raise ServiceError("the service's answer is over 1 MiB")
        try:
            return base64.b64decode(json.loads(raw)["receipt_b64"], validate=True)
        except (ValueError, KeyError, TypeError, binascii.Error):
            raise ServiceError("the service's answer holds no receipt_b64") from None
