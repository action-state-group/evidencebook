# SPDX-License-Identifier: Apache-2.0
"""The payment check (design §6.3), as Go's ``dealprofile/xdealv1``
``PaymentCheck``: the recomputed check of a payment row, over AAC's
settlement legs, with AAC's verifier (``verify_settlements``), so no second
join of legs exists here.

The terms leg is the offer's: the offer's sent Capsule carries it as its
settlement member, and each side's payment leg's terms_ref is that
Capsule's id. A terms leg has one amount, so the check runs only on an
offer whose terms have exactly one payment row, and only on that row's own
payment; a refund or another remedy that is a payment is not checked.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

from agent_action_capsule.settlement import verify_settlements

from ..check import StageError, _go_quote
from . import exact, goscan, rawjson
from .check import ref_to
from .names import ROLE_BUYER, ROLE_SELLER, TYPE_PROPOSE, TYPE_SETTLE
from .sent import (
    SETTLEMENT_MEMBER, _marshal_members, _record_members, _role, payment_rows, verify_book, verify_sent, _is_payment_row,
)

__all__ = [
    "STAGE_PAYMENT", "PAYMENT_PASS", "PAYMENT_FAIL", "REASON_SEVERAL_PAYMENTS",
    "REASON_NOT_AGREED", "REASON_UNJOINED", "REASON_PENDING", "REASON_MISMATCH", "REASON_STATUS", "REASON_AMOUNT",
    "PaymentInput", "PaymentResult", "payment_check",
]

STAGE_PAYMENT = "payment"
PAYMENT_PASS = "pass"
PAYMENT_FAIL = "fail"

REASON_SEVERAL_PAYMENTS = "several_payments"
REASON_NOT_AGREED = "not_agreed"
REASON_UNJOINED = "unjoined"
REASON_PENDING = "pending"
REASON_MISMATCH = "mismatch"
REASON_STATUS = "status"
REASON_AMOUNT = "amount"


@dataclass(frozen=True)
class PaymentInput:
    """What :func:`payment_check` reads (Go ``PaymentInput``): the offer's
    sent pair, the started deal's offer digest, the acceptor's record key,
    the payment row's id, and each side's deal-book settle records as book
    pairs."""

    offer: bytes
    offer_digest: str
    acceptor_key: str
    row: str
    legs: list[bytes] = field(default_factory=list)


@dataclass
class PaymentResult:
    """A payment check's result (Go ``PaymentResult``): pass, fail, or
    ``""`` when not run; why it failed or was not run; the record digests it
    ran over (the offer, then the legs AAC's verifier read); the settle
    records that took no part; the refund and remedy legs citing the
    offer's terms leg, not superseded, which the check does not read
    (evidencebook#722); and AAC's derived state of the payment's legs, when
    it ran."""

    check: str = ""
    reason: str = ""
    checked_over: list[str] = field(default_factory=list)
    excluded: list[str] = field(default_factory=list)
    settlement: dict | None = None
    # Last, so positional construction keeps its meaning.
    not_checked: list[str] = field(default_factory=list)

    def value(self) -> dict:
        """The result as Go's ``json.Marshal`` writes it."""
        out: dict[str, Any] = {"check": self.check}
        if self.reason:
            out["reason"] = self.reason
        out["checked_over"] = list(self.checked_over)
        if self.excluded:
            out["excluded"] = list(self.excluded)
        if self.not_checked:
            out["not_checked"] = list(self.not_checked)
        if self.settlement is not None:
            out["settlement"] = self.settlement
        return out


def _bad(detail: str) -> StageError:
    return StageError(STAGE_PAYMENT, detail)


def _settlement(s: dict) -> dict:
    """AAC's settlement entry as Go's ``settlement.Settlement`` writes it."""
    out: dict[str, Any] = {"terms": s.get("terms") or "", "payment_state": s.get("payment_state")}
    for k in ("agreed_status", "terms_amount"):
        if s.get(k):
            out[k] = s[k]
    if s.get("differs"):
        out["differs"] = list(s["differs"])
    if s.get("iso20022"):
        out["iso20022"] = dict(s["iso20022"])
    out["delivery_state"] = s.get("delivery_state") or ""
    return out


_MEMBER = {"leg": "", "sealer_role": "", "terms_ref": ""}


def _settlement_member(raw: str | None) -> dict | None:
    """A leg's settlement member ``{leg, sealer_role, terms_ref}`` as Go
    reads it, each member by its exact name (Go ``exactObject``,
    evidencebook#559); ``None`` where Go errs (no member, text that is not
    one value, or a member of another type). Read by Go's scanner
    (:mod:`.rawjson`), so any nesting reads as Go reads it."""
    if raw is None:
        return None
    b = raw.encode("utf-8", "surrogateescape")
    if not rawjson.valid(b):
        return None
    v = b.strip(b" \t\r\n")
    out = dict(_MEMBER)
    if v == b"null":
        return out
    if not v.startswith(b"{"):
        return None
    pairs = rawjson.members(v)
    if exact.exact_object({k: None for k, _ in pairs}, exact.SETTLEMENT, True, "the settlement member"):
        return None
    for k, e in pairs:
        name = k if k in out else None
        if name is None or e == "null":
            continue
        if not e.startswith('"'):
            return None
        out[name] = goscan.go_unquote(e.encode("utf-8", "surrogateescape"))
    return out


def payment_check(inp: PaymentInput) -> PaymentResult:
    """The check of a payment row (design §6.3; Go ``PaymentCheck``): pass
    when the payer's and the payee's legs answering the offer's terms leg
    are agreed, with status settled and the terms' amount; fail on a
    mismatch, and with agreed legs on a status of failed or reversed or
    another amount; not run otherwise, and when the offer's terms have more
    than one payment row. It refuses an offer that does not verify
    (:func:`.sent.verify_sent`) or is not the started deal's, a row that is
    not a payment row, a leg that does not verify (:func:`.sent.verify_book`),
    is of another deal, is signed by neither party's key or carries a
    chain. A refund or remedy leg citing the offer's terms leg is not read
    and is listed in ``not_checked`` when not superseded."""
    sent = verify_sent(inp.offer, None)
    if sent.digest != inp.offer_digest:
        raise _bad(f"the offer given is not the started deal's offer {inp.offer_digest}")
    r = sent.record
    if r.record_type != TYPE_PROPOSE:
        raise _bad(f"the offer given is a {r.record_type}")
    of = r.body
    to = of["to"]
    if isinstance(to, str) and to != inp.acceptor_key:
        raise _bad("the acceptor's key is not the key the offer was sent to")
    if inp.acceptor_key == "" or inp.acceptor_key == sent.key_id:
        raise _bad("the acceptor's key is empty or the offerer's")
    row = None
    for rw in of["terms_rows"]["rows"] or ():
        if rw["id"] == inp.row:
            row = rw
    if row is None or not _is_payment_row(row):
        raise _bad(f"{_go_quote(inp.row)} is not a payment row of the offer")
    out = PaymentResult(checked_over=[sent.digest])
    if len(payment_rows(of["terms_rows"])) != 1:
        out.reason = REASON_SEVERAL_PAYMENTS
        return out
    offer_members = _record_members(inp.offer)

    offerer = of["role"]
    acceptor = ROLE_SELLER if offerer == ROLE_BUYER else ROLE_BUYER
    key_of = {offerer: sent.key_id, acceptor: inp.acceptor_key}
    role_of: dict[str, str] = {}
    for party, key in key_of.items():
        role_of[key] = _role(party, row["owed_by"])

    legs: list[tuple[Any, dict[str, str], bool]] = []
    # Every settle record's supersedes counts against a refund, whichever
    # terms leg it answers; a payment leg is replaced only by a later payment
    # leg of this terms leg (the flag), so neither a refund nor a record of
    # another terms leg takes a failing payment out of the check
    # (evidencebook#722).
    supersedes: list[list[Any]] = []
    for i, raw in enumerate(inp.legs):
        read = verify_book(raw)
        if read.record.block["deal_id"] != r.block["deal_id"]:
            raise _bad(f"leg {i} is of deal {_go_quote(read.record.block['deal_id'])}, not the offer's")
        if read.record.record_type != TYPE_SETTLE:
            continue
        body = read.record.body
        role = role_of.get(read.key_id)
        if role is None:
            raise _bad(f"leg {i} is signed by neither party's key")
        sp = None
        if body["supersedes"] is not None:
            sp = len(supersedes)
            supersedes.append([read.key_id, body["supersedes"], read.seq, None])
        members = _record_members(raw)
        chain = members.get("chain")
        if chain is not None and chain.strip() != "null":
            raise _bad(f"leg {i}'s Capsule carries a chain; a deal-book record's has none")
        member = _settlement_member(members.get(SETTLEMENT_MEMBER))
        if member is None:
            out.excluded.append(read.digest)
            continue
        if member["terms_ref"] != sent.capsule_id:
            continue  # it answers another terms leg
        refund = body["reverses"] is not None or body["remedy_for"] is not None
        if not refund and (body["leg"] != role or member["leg"] != role + "_observed" or member["sealer_role"] != role):
            out.excluded.append(read.digest)
            continue
        if sp is not None and not refund:
            supersedes[sp][3] = len(legs)  # the superseding payment leg's index
        legs.append((read, members, refund))

    policy: dict[str, list[str]] = {}
    for key, role in role_of.items():
        policy.setdefault(role, []).append(key)
    offer_capsule = json.loads(_marshal_members(offer_members))
    # A payment leg supersedes only when AAC's verifier reads it beside the
    # offer: a leg it refuses takes no part, and so replaces nothing.
    for sp in supersedes:
        if sp[3] is not None:
            pair = [offer_capsule, json.loads(_marshal_members(legs[sp[3]][1]))]
            if verify_settlements(pair, key_policy=policy).failures:
                sp[3] = None
    superseded: set[str] = set()
    for read, _, refund in legs:
        for key, target, seq, payment in supersedes:
            if key == read.key_id and target == ref_to(read.digest) and read.seq < seq and (refund or payment is not None):
                superseded.add(read.digest)
    # A refund or remedy that is a payment needs its own terms leg, which
    # this profile does not define: it is not checked, and the payment's
    # own legs are, so a refund never hides a mismatch (evidencebook#722).
    out.not_checked = [read.digest for read, _, refund in legs if refund and read.digest not in superseded]
    capsules: list[Any] = [offer_capsule]
    digest_of: dict[str, str] = {}
    read_digests: list[str] = []
    for read, members, refund in legs:
        if refund or read.digest in superseded:
            continue
        capsules.append(json.loads(_marshal_members(members)))
        digest_of[read.record_id] = read.digest
        read_digests.append(read.digest)
    res = verify_settlements(capsules, key_policy=policy)
    refused: set[str] = set()
    for f in res.failures:
        for rec in f.get("records") or [None]:
            if rec is None or not digest_of.get(rec):
                raise _bad(f"AAC's verifier refuses the offer's terms leg: {f.get('code')}")
            refused.add(digest_of[rec])
    for d in read_digests:
        (out.excluded if d in refused else out.checked_over).append(d)
    for s in res.settlements:
        if s.get("terms") == sent.capsule_id:
            out.settlement = _settlement(s)
    if out.settlement is None:
        raise _bad(f"AAC's verifier read no terms leg {sent.capsule_id}")
    st = out.settlement
    state = st["payment_state"]
    if state == "unjoined":
        out.reason = REASON_UNJOINED
    elif state is None or state not in ("agreed", "mismatch"):
        out.reason = REASON_NOT_AGREED
    elif state == "mismatch":
        out.check, out.reason = PAYMENT_FAIL, REASON_MISMATCH
    elif st.get("agreed_status") in ("failed", "reversed"):
        out.check, out.reason = PAYMENT_FAIL, REASON_STATUS
    elif st.get("terms_amount") != "equal":
        out.check, out.reason = PAYMENT_FAIL, REASON_AMOUNT
    elif st.get("agreed_status") == "settled":
        out.check = PAYMENT_PASS
    else:
        out.reason = REASON_PENDING
    return out
