# SPDX-License-Identifier: Apache-2.0
"""The demo's two wallets: Shockley's (the seller side) issues an invoice for the picked price and
Brattain's (the buyer side) pays it, both through ``mesh-llm plugins call pay-lightning control`` on
the two nodes over ``gcloud compute ssh``.

Only the control calls ``balance``, ``fund`` and ``send`` are made here; pricing, wallet policy and
config are never touched. The amount of the invoice is the picked price and is what every screen shows
(never a balance difference). A preimage is never printed or kept: output is scrubbed before it is
returned. :class:`FakeRail` is the same interface with no money, for the tests and a dry run."""
from __future__ import annotations

import hashlib
import json
import re
import subprocess
from dataclasses import dataclass
from typing import Any

PROJECT, ZONE = "fluxxom", "us-east1-b"
BIN = "/home/intangible/ml-b0a8538-cap81bdcc6/mesh-llm"
MAX_FEE_MSAT = 1000
#: The units an amount may take: msat only.
_INVOICE = re.compile(r"\bln(?:bc|tb|bcrt)[0-9a-z]+\b")


class RailError(RuntimeError):
    pass


_HEX64 = re.compile(r"[0-9a-fA-F]{64}")
_MULT_MSAT = {"": 10**11, "m": 10**8, "u": 10**5, "n": 10**2}  # msat per unit of the amount's multiplier; p is 0.1 msat
_HRP = re.compile(r"^ln(?:bc|tb|bcrt)(\d+)([munp]?)1")


def invoice_msat(invoice: str) -> int | None:
    """The amount a BOLT11 invoice states, in msat, read from its human-readable part ("lnbc2890p1…" is 289 msat);
    ``None`` for an invoice with no amount or one that is not a whole number of msat."""
    m = _HRP.match(invoice.lower())
    if not m:
        return None
    n, unit = int(m.group(1)), m.group(2)
    if unit == "p":
        return n // 10 if n % 10 == 0 else None
    return n * _MULT_MSAT[unit]


def redact(text: str) -> str:
    """``text`` with every 64-hex string (a preimage, a hash or a key) replaced, cut to 200 characters."""
    return _HEX64.sub("<hex>", text)[-200:]


def scrub(value: Any) -> Any:
    """``value`` without any member whose name says preimage (secret material is never kept)."""
    if isinstance(value, dict):
        return {k: scrub(v) for k, v in value.items() if "preimage" not in k.lower()}
    if isinstance(value, list):
        return [scrub(v) for v in value]
    return value


@dataclass
class Payment:
    invoice_msat: int
    reference: str          # the payment hash or id the send returned (never a preimage)
    status: str
    fee_msat: int | None
    raw: dict


class Rail:
    def balances(self) -> dict[str, dict]:
        raise NotImplementedError

    def pay(self, amount_msat: int) -> Payment:
        """Issue an invoice for exactly ``amount_msat`` on the seller's wallet and pay it from the buyer's."""
        raise NotImplementedError


class LightningRail(Rail):
    SELLER, BUYER = "mc53-u755-provider", "mc53-u755-payer"  # Shockley, Brattain

    def _control(self, host: str, payload: dict, timeout: int = 90) -> dict:
        remote = (f"sudo -u intangible {BIN} plugins call pay-lightning control --port 3131 "
                  f"--input '{json.dumps(payload, separators=(',', ':'))}'")
        run = subprocess.run(["gcloud", "compute", "ssh", host, "--zone", ZONE, "--project", PROJECT, "--command", remote],
                             capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=timeout)
        out = run.stdout.strip()
        if run.returncode != 0 or not out:
            raise RailError(f"{host} {payload['command']}: exit {run.returncode}: {redact((run.stderr or 'no output').strip())}")
        try:
            return scrub(json.loads(out))
        except ValueError:
            raise RailError(f"{host} {payload['command']}: unreadable answer") from None

    def balances(self) -> dict[str, dict]:
        return {"shockley": self._control(self.SELLER, {"command": "balance"}),
                "brattain": self._control(self.BUYER, {"command": "balance"})}

    def pay(self, amount_msat: int) -> Payment:
        made = self._control(self.SELLER, {"command": "fund", "amount_msat": amount_msat})
        invoice = _find_invoice(made)
        if invoice is None:
            raise RailError("Shockley's fund call returned no invoice")
        stated = invoice_msat(invoice)  # the invoice itself, not the wallet's account of it
        if stated != amount_msat:
            raise RailError(f"the invoice is for {stated} msat, not {amount_msat}: not paying it")
        sent = self._control(self.BUYER, {"command": "send", "invoice": invoice, "max_fee_msat": MAX_FEE_MSAT})
        status = str(sent.get("status", ""))
        if status != "succeeded":
            raise RailError(f"Brattain's send did not succeed: status {status or 'unknown'}")
        if sent.get("amount_msat") is not None and int(sent["amount_msat"]) != amount_msat:
            raise RailError(f"Brattain sent {sent['amount_msat']} msat, not {amount_msat}")
        ref = str(sent.get("payment_hash") or sent.get("id") or hashlib.sha256(invoice.encode()).hexdigest())
        return Payment(amount_msat, ref, status, sent.get("fee_msat"), {"fund": made, "send": sent})


def _find_invoice(doc: Any) -> str | None:
    found = _INVOICE.search(json.dumps(doc))
    return found.group(0) if found else None


class FakeRail(Rail):
    """No money: an invoice of the right amount and a payment that always settles."""

    def __init__(self) -> None:
        self.paid: list[int] = []

    def balances(self) -> dict[str, dict]:
        return {"shockley": {"spendable_msat": 0, "fake": True}, "brattain": {"spendable_msat": 0, "fake": True}}

    def pay(self, amount_msat: int) -> Payment:
        self.paid.append(amount_msat)
        ref = hashlib.sha256(f"fake:{amount_msat}:{len(self.paid)}".encode()).hexdigest()
        return Payment(amount_msat, ref, "succeeded", 0, {"fake": True})
