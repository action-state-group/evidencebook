# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v0 records of a Goose deal: what each step says.

A Goose deal is the shortest chain the profile counts on a
``standard_history/1`` card (x-deal-v0 §11), on either side:

- **the buyer** (``party_role`` absent): the baseline (what was asked, of
  whom, on which terms), the check before paying, its verdict, the approval
  under the user's standing intent, and the ``pay`` act; once the deal ends,
  the outcome and the close. These are the records capsulectl deal seals for
  a closed retail deal (``dealprofile/testdata/baseline-vectors.json``, flow
  ``retail-closed``), without the steps a Goose tool call has no source for.
- **the seller** (``party_role: seller``), in the typed action records the
  profile requires where the user sells (§9): the baseline, the task
  authority, the offer at the listed terms, the buyer's acceptance (the
  buyer's request half, observed by its commitment), the ``commit`` act, and
  once the deal ends a typed outcome and the close (flow
  ``sale-two-threads-1``).

:class:`DealChain` fills each record's chain members (``seq``, ``at``,
``prev``, ``baseline_ref`` or ``chain_root``, and the refs' digests), runs
the profile's record checks (``evidencebook.deal.encode``) and returns the
step to seal. Raw identifiers never enter a record: the counterparty is a
fingerprint under the deal's key (x-deal-v0 §4), and texts are commitments
whose openings stay in the step's ``deal_local`` field.
"""
from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any

from agent_action_capsule.canonical import jcs

from evidencebook import deal

__all__ = ["ROLE_BUYER", "ROLE_SELLER", "TAXONOMY_VERSION", "RULESET_DIGEST", "Terms", "Step", "DealChain"]

ROLE_BUYER = "buyer"
ROLE_SELLER = "seller"
#: capsule-engine's action taxonomy version the classes are taken from
#: (x-deal-v0 §3, "Action classes").
TAXONOMY_VERSION = "6"
#: The rule table a Goose evaluation runs: none, with no materiality
#: predicate (``none_fail_safe``), so the digest covers that (x-deal-v0 §9,
#: "The evaluation's contract fields").
RULESET_DIGEST = hashlib.sha256(jcs({"evaluator": "goose-loop-reference", "rules": [],
                                     "materiality_digest": None})).hexdigest()
#: How long an evaluation covers its step.
_VALID_FOR = timedelta(minutes=15)

_STATUSES = {"received": ("completed", "completed", "agent"), "not_received": ("mismatch", "user_closed", "user")}


@dataclass(frozen=True)
class Terms:
    """What the two parties deal on: one item at a price in minor units, the
    rail it is paid on, and whether a payment comes back."""

    item: str
    price_minor: int
    currency: str
    rail: str
    refundable: bool


@dataclass
class Step:
    """One checked record, ready to seal: its JCS payload and digest, the
    Capsule's decide members when it is an act, and the texts its header's
    fields commit."""

    record_type: str
    payload: bytes
    digest: str
    at: datetime
    decide: deal.Decide | None = None
    local: str = ""
    amount: str = ""
    currency: str = ""


def _ref(digest: str, kind: str) -> dict:
    return {"type": kind, "digest_alg": "SHA-256", "digest": digest}


def _at(at: datetime) -> str:
    return at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _commit(text: str) -> tuple[str, str]:
    """A commitment to ``text`` under a fresh nonce, and the nonce."""
    nonce = os.urandom(32).hex()
    return deal.commit_text(nonce, text), nonce


@dataclass
class DealChain:
    """One party's deal, step by step. ``records`` are the deal's records so
    far, as their digests and record kinds, oldest first: a chain reopened
    from a book passes them back in (:meth:`resume`)."""

    deal_id: str
    role: str
    deal_key: bytes
    peer: str
    terms: Terms
    #: (digest, kind, action): kind is the x-deal-v0 record_type or the typed
    #: record's type; action is the body's action, or "".
    records: list[tuple[str, str, str]] = field(default_factory=list)
    _built: list[Step] = field(default_factory=list, init=False, repr=False)

    @classmethod
    def resume(cls, deal_id: str, role: str, deal_key: bytes, peer: str, terms: Terms,
               payloads: list[bytes]) -> DealChain:
        """The chain of a deal whose records (their JCS payloads, in order)
        a book already holds."""
        chain = cls(deal_id, role, deal_key, peer, terms)
        for payload in payloads:
            record = json.loads(payload)
            kind = record["x-deal-v0"]["record_type"] if "x-deal-v0" in record else record["type"]
            chain.records.append((deal.digest(record), kind, record.get("body", {}).get("action", "")))
        return chain

    # -- the chain members ---------------------------------------------------------------

    def _latest(self, kind: str) -> str:
        for digest, k, _ in reversed(self.records):
            if k == kind:
                return digest
        raise ValueError(f"deal {self.deal_id} has no {kind} record yet")

    def _untyped(self, record_type: str, at: datetime, body: dict, refs=(), **block: Any) -> dict:
        b: dict[str, Any] = {"profile": deal.PROFILE, "canonicalization": deal.CANONICALIZATION,
                             "deal_id": self.deal_id, "record_type": record_type, "seq": len(self.records) + 1,
                             "at": _at(at)}
        if self.records:
            b["prev"] = _ref(self.records[-1][0], deal.RECORD_REF)
            b["baseline_ref"] = _ref(self.records[0][0], deal.RECORD_REF)
        if refs:
            b["refs"] = [{"rel": rel, **_ref(digest, deal.RECORD_REF)} for rel, digest in refs]
        b.update(block)
        return {"x-deal-v0": b, "body": body}

    def _typed(self, kind: str, at: datetime, body: dict, refs=()) -> dict:
        record = {"type": kind, "canonicalization": deal.CANONICALIZATION, "chain_id": self.deal_id,
                  "seq": len(self.records) + 1, "at": _at(at), "prev": _ref(self.records[-1][0], "record"),
                  "chain_root": _ref(self.records[0][0], "record"), "body": body}
        if refs:
            record["refs"] = [{"rel": rel, **_ref(digest, "record")} for rel, digest in refs]
        return record

    def _step(self, record: dict, at: datetime, local: dict, decide: deal.Decide | None = None,
              amount: str = "", currency: str = "") -> Step:
        payload, digest = deal.encode(record, [self.peer])
        kind = record["x-deal-v0"]["record_type"] if "x-deal-v0" in record else record["type"]
        self.records.append((digest, kind, record.get("body", {}).get("action", "")))
        local = {"deal_id": self.deal_id, "n": len(self.records), "kind": kind, "at": _at(at), **local}
        step = Step(kind, payload, digest, at, decide, json.dumps(local, separators=(",", ":"), ensure_ascii=False),
                    amount, currency)
        self._built.append(step)
        return step

    def _counterparty(self, alg: str) -> dict:
        return {"fp_alg": alg, "ids": {"payee": deal.fingerprint(self.deal_key, "payee", self.peer)}}

    @property
    def _seller(self) -> bool:
        return self.role == ROLE_SELLER

    def _class(self, action: str) -> str:
        # x-deal-v0 §3, the purchase column.
        return {"pay": "money.purchase", "offer": "marketplace.offer"}.get(
            action, "agreement.accept" if self._seller else "money.purchase")

    # -- the steps ----------------------------------------------------------------------

    def open(self, at: datetime, asked: str) -> list[Step]:
        """The deal's first steps: the baseline, and for a seller its task
        authority. ``asked`` is the user's own words, committed."""
        if self.records:
            raise ValueError(f"deal {self.deal_id} is already open")
        n = len(self._built)
        self._baseline(at, asked)
        if self._seller:
            self._task_authority(at, asked)
        return self._since(n)

    def _baseline(self, at: datetime, asked: str) -> Step:
        t = self.terms
        commitment, nonce = _commit(asked)
        intent: dict[str, Any] = {"verbatim_commitment": commitment, "asked": {"item": t.item},
                                  "allowed": ["offer", "commit"] if self._seller else ["pay"]}
        if self._seller:
            intent["party_role"] = ROLE_SELLER
        body = {"deal_type": "purchase", "intent": intent,
                "terms": {"item": t.item, "price_minor": t.price_minor, "currency": t.currency},
                "recourse": {"rail": t.rail, "refundable": t.refundable}}
        return self._step(self._untyped(deal.TYPE_BASELINE, at, body, channel="app_chat",
                                        counterparty=self._counterparty(deal.FP_ALG)),
                          at, {"open": {"asked": asked, "peer": self.peer}, "nonces": {"verbatim": nonce}})

    def _task_authority(self, at: datetime, asked: str) -> Step:
        commitment, nonce = _commit(asked)
        return self._step(self._typed(deal.TYPE_TASK_AUTHORITY, at,
                                      {"verbatim_commitment": commitment, "allowed": ["offer", "commit"]},
                                      [("source", self.records[0][0])]),
                          at, {"nonces": {"verbatim": nonce}})

    def _has(self, kind: str, action: str = "") -> bool:
        return any(k == kind and (not action or a == action) for _, k, a in self.records)

    def _evaluated(self, action: str, at: datetime, extra: dict) -> str:
        """A seller's proposed action and its evaluation, each sealed unless
        the deal already holds it: the task authority alone covers the action
        (``DO``). Returns the evaluation's digest."""
        t, ta = self.terms, self._latest(deal.TYPE_TASK_AUTHORITY)
        if not self._has(deal.TYPE_PROPOSED_ACTION, action):
            body = {"action": action, "action_class": self._class(action), "amount_minor": t.price_minor,
                    "currency": t.currency, "terms": {"item": t.item, "price_minor": t.price_minor},
                    "spend_minor": 0, "taxonomy_version": TAXONOMY_VERSION, "task_authority_ref": _ref(ta, "record"),
                    "counterparty": self._counterparty(deal.TYPED_FP_ALG), **extra}
            self._step(self._typed(deal.TYPE_PROPOSED_ACTION, at, body), at, {"proposed": action})
        proposed = self._latest_proposed(action)
        i = next(i for i, (d, _, _) in enumerate(self.records) if d == proposed)
        if i + 1 < len(self.records) and self.records[i + 1][1] == deal.TYPE_ACTION_EVALUATION:
            return self.records[i + 1][0]
        evaluation = {"disposition": "DO", "findings": [], "options": [], "judge": {"kind": "rules"},
                      "proposed_action_digest": proposed, "task_authority_ref": _ref(ta, "record"),
                      "ruleset_digest": RULESET_DIGEST, "materiality": deal.MATERIALITY_MODE_NONE,
                      "materiality_digest": None, "valid_until": _at(at + _VALID_FOR),
                      "authority_basis": [{"type": "task_authority", "ref": _ref(ta, "record")}]}
        return self._step(self._typed(deal.TYPE_ACTION_EVALUATION, at, evaluation, [("checks", proposed)]),
                          at, {"evaluated": action}).digest

    def _typed_act(self, action: str, at: datetime, evaluation: str, refs=()) -> Step:
        t = self.terms
        body = {"action": action, "action_class": self._class(action), "amount_minor": t.price_minor,
                "currency": t.currency, "spend_minor": 0, "taxonomy_version": TAXONOMY_VERSION,
                "evaluation_ref": _ref(evaluation, "record"),
                "authority_basis": [{"type": "task_authority",
                                     "ref": _ref(self._latest(deal.TYPE_TASK_AUTHORITY), "record")}],
                "counterparty": self._counterparty(deal.TYPED_FP_ALG)}
        effect, irreversibility = {"offer": ("make_offer", "two_way")}.get(action, ("accept_agreement",
                                                                                     "one_way_consequential"))
        decide = deal.Decide(deal.Disposition("accept", "policy", False, "executed"), effect, irreversibility)
        return self._step(self._typed(deal.TYPE_ACTION_RECORD, at, body, [("authorized_by", evaluation), *refs]),
                          at, {"act": action}, decide)

    def respond(self, at: datetime, asked: str, request_half: str) -> list[Step]:
        """A seller's whole answer to a buyer's opening half: the baseline
        and task authority, the offer at the listed terms (proposed with no
        proposal of the buyer's before it, x-deal-v0 §6 rule 9), the buyer's
        acceptance (its half, committed by digest) and the commit resting on
        it. Each step is sealed only if the deal does not hold it yet, so an
        answer cut short part-way is finished by answering again; one already
        complete returns no steps."""
        if not self._seller:
            raise ValueError("only a seller responds to a buyer's half")
        self._open_for_more()
        n = len(self._built)
        if not self.records:
            self._baseline(at, asked)
        if not self._has(deal.TYPE_TASK_AUTHORITY):
            self._task_authority(at, asked)
        if not self._has(deal.TYPE_ACTION_RECORD, "offer"):
            self._typed_act("offer", at, self._evaluated("offer", at, {"no_proposal": True}))
        if not self._has(deal.TYPE_ACTION_APPROVAL):
            commitment, nonce = _commit(request_half)
            self._step(self._typed(deal.TYPE_ACTION_APPROVAL, at, {
                "authority": "counterparty_acceptance", "kind": "counterparty-acceptance-observation",
                "channel": "app_chat", "content_commitment": commitment, "observed_at": _at(at),
                "proposed_action_ref": _ref(self._latest_proposed("offer"), "record")}), at,
                {"accepted_by": request_half, "nonces": {"content": nonce}})
        if not self._has(deal.TYPE_ACTION_RECORD, "commit"):
            self._typed_act("commit", at, self._evaluated("commit", at, {}),
                            [("source", self._latest(deal.TYPE_ACTION_APPROVAL))])
        return self._since(n)

    def _open_for_more(self) -> None:
        """A deal finally closed takes no more steps: after a close whose
        outcome is final only confirming evidence may follow (x-deal-v0 §6
        rule 7), and this reference seals none."""
        if self._has(deal.TYPE_CLOSE):
            raise ValueError(f"deal {self.deal_id} is closed")

    def _latest_proposed(self, action: str) -> str:
        for digest, kind, proposed in reversed(self.records):
            if kind == deal.TYPE_PROPOSED_ACTION and proposed == action:
                return digest
        raise ValueError(f"deal {self.deal_id} proposes no {action} yet")

    def check_and_pay(self, at: datetime, reference: str) -> list[Step]:
        """The buyer's check before paying, its verdict, the standing
        intent's approval, and the ``pay`` act naming the payment reference
        by its commitment."""
        if self._seller:
            raise ValueError("only a buyer pays")
        self._open_for_more()
        if self._has(deal.TYPE_ACTION, "pay"):
            raise ValueError(f"deal {self.deal_id} is already paid")
        n, t = len(self._built), self.terms
        body = {"action": "pay", "action_class": "money.purchase", "amount_minor": t.price_minor,
                "authorized_max_minor": t.price_minor, "currency": t.currency, "spend_minor": t.price_minor,
                "spend_authorized_minor": t.price_minor, "taxonomy_version": TAXONOMY_VERSION,
                "terms": {"item": t.item, "price_minor": t.price_minor, "currency": t.currency},
                "recourse": {"rail": t.rail, "refundable": t.refundable}}
        checked = self._step(self._untyped(deal.TYPE_CHECK, at, body, counterparty=self._counterparty(deal.FP_ALG)),
                             at, {"check": "pay"}).digest
        verdict = self._step(self._untyped(deal.TYPE_VERDICT, at, {
            "result": "pass", "differences": [], "options": [], "judge": {"kind": "rules"},
            "rules": {"status": "not_configured"}}, [("checks", checked)]), at, {"verdict": "pass"}).digest
        approval = self._step(self._untyped(deal.TYPE_APPROVAL, at, {
            "choice": "proceed", "proceed": True, "approver": "standing_intent"}, [("approves", verdict)]),
            at, {"approval": "standing_intent"}).digest
        commitment, nonce = _commit(reference)
        decide = deal.Decide(deal.Disposition("accept", "policy", False, "executed"), "send_payment",
                             "one_way_recoverable" if t.refundable else "one_way_consequential")
        self._step(self._untyped(deal.TYPE_ACTION, at, {
            "action": "pay", "action_class": "money.purchase", "amount_minor": t.price_minor,
            "currency": t.currency, "direction": "out", "rail": t.rail, "reference_commitment": commitment,
            "spend_minor": t.price_minor, "taxonomy_version": TAXONOMY_VERSION},
            [("authorized_by", approval)], counterparty=self._counterparty(deal.FP_ALG)),
            at, {"act": {"action": "pay", "reference": reference}, "nonces": {"reference": nonce}}, decide)
        return self._since(n)

    def close(self, at: datetime, status: str) -> list[Step]:
        """The outcome (typed, on a seller's chain) and the close. A received
        close commits the agreed price and currency (x-deal-v0 §10)."""
        if status not in _STATUSES:
            raise ValueError(f"status must be one of {sorted(_STATUSES)}")
        self._open_for_more()
        n, t = len(self._built), self.terms
        outcome, reason, actor = _STATUSES[status]
        differences = [] if status == "received" else [{"question": "delivered", "rule": "not_delivered"}]
        body = {"status": status, "outcome": outcome, "actor": actor, "reason": reason}
        if self._seller:
            observed = self._step(self._typed(deal.TYPE_ACTION_OUTCOME, at, {**body, "findings": differences}),
                                  at, {"outcome": status}).digest
        else:
            observed = self._step(self._untyped(deal.TYPE_OUTCOME, at, {**body, "differences": differences}),
                                  at, {"outcome": status}).digest
        closing = {"outcome": outcome, "unchecked_actions": 0, "actor": actor, "reason": reason}
        if differences:
            closing["differences"] = differences
        amount, currency = (str(t.price_minor), t.currency) if status == "received" else ("", "")
        self._step(self._untyped(deal.TYPE_CLOSE, at, closing, [("outcome", observed)]), at, {"close": status},
                   amount=amount, currency=currency)
        return self._since(n)

    def _since(self, n: int) -> list[Step]:
        return self._built[n:]
