# SPDX-License-Identifier: Apache-2.0
"""The standard_history/1 deal entry (agent-action-capsule
spec/standard-history-v0.md §5.2, SH-2) as this profile fills it from a
deal's sealed records, as Go's ``dealprofile/standard_history.go``. The
mapping is here, with the profile, so every producer that counts x-deal-v0
deals counts them the same way. A member with no sealed record behind it is
never guessed:

* A deal is a deal's own log holding an ``action`` (or typed
  ``action-record/v0``) ``commit`` or ``pay``. A sale's log is not a deal; a
  deal that never reached such a step was never agreed.
* ``agreed_at`` is that first step's committed_at, to the minute: a tick's
  anchor time is a minute floor, and a step sealed after it within the same
  minute would sort after the anchor and drop out of every count.
* ``role`` is the baseline intent's party_role: seller is "provided", buyer or
  absent "bought".
* ``amount`` is the amount_minor and currency of the check (typed
  proposed-action/v0) the step rests on, scaled by the currency's ISO 4217
  minor units. The profile lets a check omit them, and SH gives an amount no
  unknown value, so a deal without one raises :class:`Unpriced`: the host
  refuses the card, it never drops the deal.
* ``counterparty_class`` is absent: the profile records no SH §7 class
  (evidencebook#273 will name the record; the one place it lands is
  :func:`_counterparty_class`).
* ``outcome`` is the newest verified ec:deal-reconcile@1 Result's claims: at
  least one claim and every claim met is "held", any not_met is
  "did_not_hold", and anything else (no claim, met with undecided, no
  verified Result) is "undecided".
* ``happened`` is "stamped_by_both" only when the counterparty's half is
  received and verifies (the host's check, as the deal view's); else
  "receipt_only" when the holder's own final close observed a "received"
  outcome; else "no_record".
* ``disputes`` is empty: the profile seals no IP §11.2 disputes statement.

The entry is the dict ``agent_action_capsule.standard_history.derive_card``
takes in ``deals``. Records are read with ``json``: a member name is matched
exactly, where Go's ``encoding/json`` also matches it in another case.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from .check import _float, _go_quote, _refuse_constant, digest
from .names import (
    TYPE_ACTION, TYPE_ACTION_EVALUATION, TYPE_ACTION_OUTCOME, TYPE_ACTION_RECORD, TYPE_APPROVAL, TYPE_BASELINE,
    TYPE_CHECK, TYPE_CLOSE, TYPE_OUTCOME, TYPE_PROPOSED_ACTION, TYPE_SALE, TYPE_VERDICT,
)

__all__ = ["HistoryRecord", "HistoryResult", "HistoryInput", "Unpriced", "standard_history_deal", "minor_units"]


@dataclass(frozen=True)
class HistoryRecord:
    """One sealed record of a deal's own log."""
    #: The record's JSON text, the Capsule's payload.
    payload: bytes
    #: When the holder's book committed it.
    committed_at: datetime


@dataclass(frozen=True)
class HistoryResult:
    """One Result the deal's results book sealed."""
    #: The evidence-result-v0 document (the deal_terms_result payload's
    #: "result").
    result: bytes
    #: The host's check that the Result's record signature and seal verify. A
    #: Result that does not is no Result.
    verified: bool


@dataclass(frozen=True)
class HistoryInput:
    """What the host holds for one deal under the card's anchor."""
    #: The deal log's records the anchor commits, in log order.
    records: tuple[HistoryRecord, ...] = field(default_factory=tuple)
    #: The deal's sealed Results the anchor commits, oldest first. Results live
    #: on the deal's results book (deal/<id>/results), not on its log, so the
    #: host passes only those within the results book's extent in the anchor's
    #: extents: a Result sealed after the anchor would change a card that
    #: claims to be as of it, and the card could not be reproduced.
    results: tuple[HistoryResult, ...] = field(default_factory=tuple)
    #: Under the anchor like every other input, whether the counterparty's
    #: half of the deal is received and its signature verifies.
    counterparty_half_verified: bool = False


class Unpriced(ValueError):
    """A deal whose agreeing step rests on a check with no amount, or a
    currency with no ISO 4217 minor units: SH has no entry for it, so the
    card cannot be built (agent-action-capsule#516)."""


_UNPRICED = "dealprofile: a deal has no recorded amount"


@dataclass(frozen=True)
class _Rec:
    """The members of a record (x-deal-v0 or typed) the entry reads."""
    digest: str
    committed_at: datetime
    kind: str
    deal_id: str
    refs: dict[str, list[str]]
    body: dict[str, Any]

    def str(self, member: str) -> str:
        v = self.body.get(member)
        return v if isinstance(v, str) else ""

    def ref(self, rel: str) -> str:
        """The one digest the record names under ``rel``, or ""."""
        digests = self.refs.get(rel, [])
        return digests[0] if len(digests) == 1 else ""


def _loads(data: bytes | str) -> Any:
    return json.loads(data, parse_float=_float, parse_constant=_refuse_constant)


def _string_member(obj: dict, name: str) -> str:
    v = obj.get(name)
    if v is None:
        return ""
    if not isinstance(v, str):
        raise ValueError(f"member {name} is not a string")
    return v


def _refs(obj: dict) -> list[tuple[str, str]]:
    v = obj.get("refs")
    if v is None:
        return []
    if not isinstance(v, list):
        raise ValueError("refs is not an array")
    out = []
    for r in v:
        if r is None:
            out.append(("", ""))
            continue
        if not isinstance(r, dict):
            raise ValueError("a ref is not an object")
        out.append((_string_member(r, "rel"), _string_member(r, "digest")))
    return out


def _read_record(r: HistoryRecord) -> _Rec | None:
    """A step record of either kind; None for a record that is no step, such
    as a sealed report, which says nothing the entry reads."""
    try:
        top = _loads(r.payload)
        if not isinstance(top, dict):
            raise ValueError("not an object")
        block = top.get("x-deal-v0")
        if block is not None and not isinstance(block, dict):
            raise ValueError("the x-deal-v0 block is not an object")
        typ = _string_member(top, "type")
        chain_id = _string_member(top, "chain_id")
        body = top.get("body")
        if body is not None and not isinstance(body, dict):
            raise ValueError("body is not an object")
        top_refs = _refs(top)
        refs = _refs(block) if block is not None else top_refs
        if block is not None:
            kind, deal_id = _string_member(block, "record_type"), _string_member(block, "deal_id")
        elif typ.endswith("/v0"):
            kind, deal_id = typ, chain_id
        else:
            return None
    except ValueError as e:
        raise ValueError(f"dealprofile: a deal record is not JSON: {e}") from None
    by_rel: dict[str, list[str]] = {}
    for rel, d in refs:
        by_rel.setdefault(rel, []).append(d)
    return _Rec(digest(r.payload), r.committed_at, kind, deal_id, by_rel, body or {})


def _counterparty_class(records: list[_Rec]) -> str | None:
    """The SH §7 counterparty_class of the deal. The profile records none yet
    (evidencebook#273), so the entry carries no member."""
    return None


def standard_history_deal(inp: HistoryInput) -> dict[str, Any] | None:
    """One deal's SH §5.2 entry, or None for a log that is not an agreed deal
    (it is not on the card). :class:`Unpriced`, or any other error, means the
    card cannot be built."""
    records: list[_Rec] = []
    by_digest: dict[str, _Rec] = {}
    for r in inp.records:
        h = _read_record(r)
        if h is None:
            continue
        records.append(h)
        by_digest[h.digest] = h
    if not records or records[0].deal_id.startswith("sale-") or records[0].kind == TYPE_SALE:
        return None
    agreed = next((r for r in records if r.kind in (TYPE_ACTION, TYPE_ACTION_RECORD)
                   and r.str("action") in ("commit", "pay")), None)
    if agreed is None:
        return None
    deal_id = records[0].deal_id
    try:
        amount = _agreed_amount(agreed, by_digest)
    except Unpriced as e:
        raise Unpriced(f"deal {deal_id}: {e}") from None
    entry: dict[str, Any] = {
        "deal": deal_id,
        "agreed_at": _minute(agreed.committed_at),
        "role": _deal_role(records),
        "amount": amount,
        "outcome": _reconcile_outcome(inp.results),
        "happened": _happened(records, by_digest, inp.counterparty_half_verified),
        "disputes": [],
    }
    cls = _counterparty_class(records)
    if cls is not None:
        entry = {**{k: entry[k] for k in ("deal", "agreed_at")}, "counterparty_class": cls,
                 **{k: v for k, v in entry.items() if k not in ("deal", "agreed_at")}}
    return entry


def _minute(at: datetime) -> str:
    if at.tzinfo is None:
        at = at.replace(tzinfo=timezone.utc)
    return at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:00Z")


def _agreed_amount(step: _Rec, by_digest: dict[str, _Rec]) -> dict[str, Any]:
    """The amount of the check the agreeing step rests on: action → approval
    (authorized_by) → verdict (approves) → check (checks), or action-record/v0
    → action-evaluation/v0 (evaluation_ref) → proposed-action/v0
    (proposed_action_digest)."""
    check: _Rec | None = None
    if step.kind == TYPE_ACTION_RECORD:
        ref = step.body.get("evaluation_ref")
        d = ref.get("digest") if isinstance(ref, dict) else ""
        evaluation = by_digest.get(d if isinstance(d, str) else "")
        if evaluation is not None and evaluation.kind == TYPE_ACTION_EVALUATION:
            check = by_digest.get(evaluation.str("proposed_action_digest"))
            if check is not None and check.kind != TYPE_PROPOSED_ACTION:
                check = None
    else:
        approval = by_digest.get(step.ref("authorized_by"))
        if approval is not None and approval.kind == TYPE_APPROVAL:
            verdict = by_digest.get(approval.ref("approves"))
            if verdict is not None and verdict.kind == TYPE_VERDICT:
                check = by_digest.get(verdict.ref("checks"))
                if check is not None and check.kind != TYPE_CHECK:
                    check = None
    if check is None:
        raise Unpriced(f"{_UNPRICED}: the {step.str('action')} step's check is not on its log")
    minor = check.body.get("amount_minor")
    currency = check.str("currency")
    # Go reads *int64: a whole number only, and within int64.
    if (isinstance(minor, bool) or not isinstance(minor, int) or not -2 ** 63 <= minor < 2 ** 63
            or minor < 0 or not currency):
        raise Unpriced(f"{_UNPRICED}: its check states no amount and currency")
    scale = minor_units(currency)
    if scale is None:
        raise Unpriced(f'{_UNPRICED}: currency {_go_quote(currency)} has no ISO 4217 minor units')
    return {"value": str(minor), "assetCode": currency, "assetScale": scale}


def _deal_role(records: list[_Rec]) -> str:
    for r in records:
        if r.kind == TYPE_BASELINE:
            intent = r.body.get("intent")
            if isinstance(intent, dict) and intent.get("party_role") == "seller":
                return "provided"
            return "bought"
    return "bought"


def _reconcile_outcome(results: tuple[HistoryResult, ...]) -> str:
    """The newest verified Result's claims."""
    for res in reversed(results):
        if not res.verified:
            continue
        try:
            doc = _loads(res.result)
            if not isinstance(doc, dict):
                return "undecided"
            claims = doc.get("claims")
            if claims is None:
                return "undecided"
            if not isinstance(claims, list):
                return "undecided"
            verdicts = []
            for c in claims:
                if c is None:
                    verdicts.append("")
                    continue
                if not isinstance(c, dict):
                    return "undecided"
                v = c.get("verdict")
                if v is not None and not isinstance(v, str):
                    return "undecided"
                verdicts.append(v or "")
        except ValueError:
            return "undecided"
        if not verdicts:
            return "undecided"
        met = 0
        for v in verdicts:
            if v == "not_met":
                return "did_not_hold"
            if v == "met":
                met += 1
        return "held" if met == len(verdicts) else "undecided"
    return "undecided"


def _happened(records: list[_Rec], by_digest: dict[str, _Rec], counterparty_half: bool) -> str:
    """SH §5.5 over the holder's records: the counterparty's half, else the
    holder's own final close of a received outcome."""
    if counterparty_half:
        return "stamped_by_both"
    final = None
    for r in records:
        if r.kind == TYPE_CLOSE and r.str("outcome") != "open":
            final = r
    if final is not None:
        outcome = by_digest.get(final.ref("outcome"))
        if (outcome is not None and outcome.kind in (TYPE_OUTCOME, TYPE_ACTION_OUTCOME)
                and outcome.str("status") == "received"):
            return "receipt_only"
    return "no_record"


#: ISO 4217 codes with minor units other than two.
_MINOR_UNIT_EXCEPTIONS = {
    "BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0, "KRW": 0, "PYG": 0,
    "RWF": 0, "UGX": 0, "UYI": 0, "VND": 0, "VUV": 0, "XAF": 0, "XOF": 0, "XPF": 0,
    "BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
    "CLF": 4, "UYW": 4,
}

#: ISO 4217 codes with two minor units, including ANG and BGN, withdrawn for
#: XCG and EUR but still on older deals.
_TWO_DECIMAL_CURRENCIES = (
    " AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BMD BND BOB BOV BRL BSD BTN BWP BYN BZD"
    " CAD CDF CHE CHF CHW CNY COP COU CRC CUP CVE CZK DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GTQ GYD"
    " HKD HNL HTG HUF IDR ILS INR IRR JMD KES KGS KHR KPW KYD KZT LAK LBP LKR LRD LSL MAD MDL MGA MKD MMK MNT MOP MRU"
    " MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD PAB PEN PGK PHP PKR PLN QAR RON RSD RUB SAR SBD SCR SDG SEK"
    " SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TOP TRY TTD TWD TZS UAH USD USN UYU UZS VED VES WST XCD XCG"
    " YER ZAR ZMW ZWG "
)


def minor_units(currency: str) -> int | None:
    """A currency's ISO 4217 minor units: the assetScale of an amount in minor
    units. None for a code with none (a metal, a fund code without them) or an
    unknown one."""
    if currency in _MINOR_UNIT_EXCEPTIONS:
        return _MINOR_UNIT_EXCEPTIONS[currency]
    if f" {currency} " in _TWO_DECIMAL_CURRENCIES and len(currency) == 3:
        return 2
    return None
