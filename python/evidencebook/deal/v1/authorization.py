# SPDX-License-Identifier: Apache-2.0
"""Authorization (design §3.2, invariant D), as Go's ``dealprofile/xdealv1``
``authorization.go``: an act is a deal-book perform or settle that cites, by
digest, the check in the holder's journal it rests on. v0's authorization
conditions hold unchanged (x-deal-v0 PROFILE.md §6, rules 5 and 6), with
that citation in place of v0's authorized_by. The journal is read in order:
the limits in force change only by an intent that narrows them and by a
confirmed proposal, and each approval is judged at its own position.

A refusal is a :class:`~evidencebook.deal.check.StageError` at stage
``authorization``.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from ..check import StageError, _go_quote
from . import gotime, gotypes
from .check import Record, decode, digest
from .names import (
    CHOICE_CONFIRM, TYPE_ADMIT, TYPE_AGENT_MOVE, TYPE_APPROVAL, TYPE_CHECK, TYPE_DETAIL_CHANGE, TYPE_EVIDENCE,
    TYPE_INTENT, TYPE_MESSAGE, TYPE_PERFORM, TYPE_SETTLE, TYPE_VERDICT,
)

__all__ = ["STAGE_AUTHORIZATION", "ActAuthorization", "check_authorizations", "rules_unevaluated"]

STAGE_AUTHORIZATION = "authorization"

_LIMIT_SET = {"struct": "dealprofile.LimitSet"}


@dataclass
class ActAuthorization:
    """What authorizes one act (Go ``ActAuthorization``): the act, its
    check, verdict and approval; the approval's record digest, which a
    person's signature covers; whether the approval is the person's and so
    needs the person's signature; the digests of the confirm_limits
    approvals a standing-intent approval relied on, each also needing it;
    and the digest of the first intent a standing-intent approval rests on
    (``""`` otherwise). ``rules_not_recorded`` marks a standing-intent
    approval whose verdict carries no rules block: nothing records which
    rules, if any, evaluated the person's caps, so the act needs the person's
    signature too (Sherlock2's ruling ops bb59237c, after u503;
    evidencebook#983; Go ``RulesNotRecorded``). ``rules_not_evaluable`` marks a
    standing-intent approval whose verdict's rules did not allow the act on
    their own (:func:`rules_unevaluated`): the act then needs the person's signature
    too, whatever the verdict's result says (u503; evidencebook#982)."""

    act: Record
    check: Record
    verdict: Record
    approval: Record
    approval_digest: str
    needs_person_signature: bool
    limit_approvals: list[str] = field(default_factory=list)
    intent: str = ""
    rules_not_recorded: bool = False
    rules_not_evaluable: bool = False


def rules_unevaluated(rules: dict | None) -> bool:
    """Whether a verdict's rules did not allow the act on their own (PROFILE
    §8; Go ``RulesUnevaluated``): their status is not_evaluated; or they were
    evaluated and the checker's own verdict is not allow; or, whatever their
    status, a measured finding is not_evaluable. A finding is measured when it
    names its check; one that names none is a rule its ruleset only declares,
    which never moves the verdict."""
    if rules is None:
        return False
    if any(f.get("verdict") == "not_evaluable" and f.get("check") for f in rules.get("findings") or []):
        return True
    status = rules.get("status")
    return status == "not_evaluated" or (status == "evaluated" and rules.get("verdict") != "allow")


def _err(detail: str) -> StageError:
    return StageError(STAGE_AUTHORIZATION, detail)


# -- times -----------------------------------------------------------------------------


def _at(r: Record) -> tuple[int, int]:
    """A record's at as Go's ``parseAt`` reads it (``time.Parse(time.RFC3339,
    at)``, :mod:`.gotime`): the instant, or a ``ValueError`` with Go's
    text."""
    try:
        return gotime.parse(r.block["at"])
    except gotime.GoTimeError as exc:
        raise ValueError(f"its at {_go_quote(r.block['at'])}: {exc}") from None


# -- acts ------------------------------------------------------------------------------

_PAY, _REFUND, _PERFORM = "payer settle", "refund", "perform"


def _check_action(kind: str) -> str:
    return {_PAY: "pay", _REFUND: "cancel"}.get(kind, "commit")


def _act_check(r: Record) -> tuple[Any, str, bool]:
    """The check an act cites (``None`` for none) and its kind; ok is false
    for a record that is not an act (Go ``actCheck``)."""
    if r.record_type == TYPE_PERFORM:
        return r.body["check"], _PERFORM, True
    if r.record_type == TYPE_SETTLE:
        return r.body["check"], _REFUND if r.body["reverses"] is not None else _PAY, True
    return None, "", False


def _person_approver(approver: str) -> bool:
    return approver in ("user", "agent_card")


def check_authorizations(journal: list[Any], acts: list[Any]) -> list[ActAuthorization]:
    """Checks every act that cites a check (Go ``CheckAuthorizations``).
    ``journal`` is the holder's journal records of the deal's task, in
    journal order; ``acts`` are its deal-book perform and settle records, in
    book order (each a :class:`Record` or its JSON). A journal record counts
    for an act when its at is not after the act's. It returns one
    :class:`ActAuthorization` per act that cites a check, in order."""
    journal = [decode(r) if not isinstance(r, Record) else r for r in journal]
    acts = [decode(r) if not isinstance(r, Record) else r for r in acts]
    w = _walk(journal)
    used: dict[int, int] = {}
    out = []
    for i, act in enumerate(acts):
        def bad(detail: str) -> StageError:
            return _err(f"act {i + 1}: {detail}")

        check_ref, kind, ok = _act_check(act)
        if not ok:
            raise bad(f"a {act.record_type} is not an act")
        if check_ref is None:
            continue
        try:
            act_at = _at(act)
        except ValueError as exc:
            raise bad(str(exc)) from None
        ci = w.digests.index(check_ref["digest"]) if check_ref["digest"] in w.digests else -1
        if ci < 0 or journal[ci].record_type != TYPE_CHECK:
            raise bad("it cites no check the journal holds")
        if journal[ci].block["deal_id"] != act.block["deal_id"]:
            raise bad("its check serves another deal")
        cb = w.check[ci]
        want = _check_action(kind)
        if cb["action"] != want:
            raise bad(f"the act differs from the one checked: a {kind} rests on a {_go_quote(want)} check, not "
                      f"{_go_quote(cb['action'])}")
        if ci not in w.verdict_of:
            raise bad("its check has no verdict")
        vi = w.verdict_of[ci]
        if vi not in w.first_answer:
            raise bad("its verdict has no sealed approval")
        ai = w.first_answer[vi]
        ab = w.approval[ai]
        if _at(journal[ai]) > act_at:
            raise bad("its approval was sealed after it")
        if ab["choice"] == CHOICE_CONFIRM:
            raise bad("a confirm_limits approval authorizes no act")
        if not ab["proceed"]:
            raise bad("its verdict's first approval does not proceed")
        if w.verdict[vi]["result"] == "deny":
            raise bad("its verdict denies")
        if ai in used:
            raise bad(f"its approval already authorized act {used[ai] + 1}")
        used[ai] = i
        for k in range(ci + 1, len(journal)):
            if not _at(journal[k]) > act_at and w.changes_details(k):
                raise bad(f"journal record {k + 1} ({journal[k].record_type}) changed the details after its check: "
                          "a new check is needed")
        a = ActAuthorization(act, journal[ci], journal[vi], journal[ai], w.digests[ai],
                             _person_approver(ab["approver"]))
        if ab["approver"] == "standing_intent":
            rules = w.verdict[vi].get("rules")
            if rules is None:
                a.rules_not_recorded = a.needs_person_signature = True
            elif rules_unevaluated(rules):
                a.rules_not_evaluable = a.needs_person_signature = True
            a.limit_approvals = [w.digests[j] for j in w.limits_at[ai]]
            a.intent = w.digests[w.intent_at[ai]]
        out.append(a)
    return out


# -- the journal -----------------------------------------------------------------------


class _Walk:
    """The journal read in order, as v0 reads a chain (Go ``journalWalk``)."""

    def __init__(self, journal: list[Record]) -> None:
        self.journal = journal
        self.digests = [digest(r) for r in journal]
        self.check: dict[int, dict] = {}
        self.verdict: dict[int, dict] = {}
        self.approval: dict[int, dict] = {}
        self.verdict_of: dict[int, int] = {}
        self.first_answer: dict[int, int] = {}
        self.limits_at: dict[int, list[int]] = {}
        self.intent_at: dict[int, int] = {}

    def check_of_verdict(self, vi: int) -> dict | None:
        for ci, v in self.verdict_of.items():
            if v == vi:
                return self.check.get(ci)
        return None

    def changes_details(self, k: int) -> bool:
        """A journal record after which no earlier check covers an act (v0
        §6 rules 5 and 13; Go ``changesDetails``)."""
        r = self.journal[k]
        t, b = r.record_type, r.body
        if t == TYPE_DETAIL_CHANGE:
            return True
        if t == TYPE_MESSAGE:
            return r.block["counterparty"] is not None
        if t == TYPE_EVIDENCE:
            return r.block["counterparty"] is not None or b["counterparty_facts"] is not None
        if t == TYPE_INTENT:
            asked = b["asked"]
            return r.block["counterparty"] is not None or (b["move"] != "" and asked is not None and asked["item"] != "")
        if t == TYPE_AGENT_MOVE:
            return b["move"] == "chose"
        return False


def _nonce_of(r: Record) -> str:
    """A journal record's own secret: its nonce, or an intent's
    verbatim_commitment; ``""`` for a type that carries none."""
    if r.record_type == TYPE_INTENT:
        return r.body["verbatim_commitment"]
    if r.record_type in (TYPE_ADMIT, TYPE_CHECK, TYPE_VERDICT, TYPE_APPROVAL):
        return r.body["nonce"]
    return ""


def _intent_between(journal: list[Record], j: int, i: int) -> bool:
    return any(journal[k].record_type == TYPE_INTENT for k in range(j + 1, i))


def _walk(journal: list[Record]) -> _Walk:
    w = _Walk(journal)
    by_digest = {d: i for i, d in enumerate(w.digests)}
    lim = _Limits()
    confirmed: dict[int, bool] = {}
    confirmations: list[int] = []
    first_intent = -1
    # Go's zero Time: at never decreases from it, so a year-0 time is refused.
    last: tuple[int, int] = (gotime.ZERO, 0)
    nonces: dict[str, int] = {}
    for i, r in enumerate(journal):
        def bad(detail: str) -> StageError:
            return _err(f"journal record {i + 1}: {detail}")

        n = _nonce_of(r)
        if n != "":
            if n in nonces:
                raise bad(f"its nonce (or verbatim_commitment) is journal record {nonces[n] + 1}'s: a nonce is drawn "
                          "fresh for each record")
            nonces[n] = i
        try:
            at = _at(r)
        except ValueError as exc:
            raise bad(str(exc)) from None
        if at < last:
            raise bad("its at is earlier than the record before it: at never decreases")
        last = at

        def one(rel: str, t: str) -> int:
            js = []
            for ref in r.block["refs"] or ():
                if ref["rel"] == rel:
                    j = by_digest.get(ref["digest"])
                    if j is None or j >= i or journal[j].record_type != t:
                        raise bad(f"its {rel} ref names no earlier {t}")
                    js.append(j)
            if len(js) != 1:
                raise bad(f"a {r.record_type} carries exactly one {rel} ref")
            return js[0]

        t, b = r.record_type, r.body
        if t == TYPE_INTENT:
            lim.narrow(b)
            if first_intent < 0:
                first_intent = i
        elif t == TYPE_CHECK:
            w.check[i] = b
        elif t == TYPE_VERDICT:
            j = one("checks", TYPE_CHECK)
            if journal[j].block["deal_id"] != r.block["deal_id"]:
                raise bad("a verdict answers a check of its own deal")
            if j in w.verdict_of:
                raise bad("a check has one verdict")
            w.verdict_of[j] = i
            w.verdict[i] = b
        elif t == TYPE_APPROVAL:
            w.approval[i] = b
            if b["choice"] == CHOICE_CONFIRM:
                j = one("approves", TYPE_INTENT)
                prop = journal[j].body
                late = confirmed.get(j, False) or _intent_between(journal, j, i)
                try:
                    lim.confirm(b, prop, late)
                except ValueError as exc:
                    raise bad(str(exc)) from None
                if b["proceed"] and not late:
                    confirmed[j] = True
                    confirmations.append(i)
                continue
            j = one("approves", TYPE_VERDICT)
            if journal[j].block["deal_id"] != r.block["deal_id"]:
                raise bad("an approval answers a verdict of its own deal")
            w.first_answer.setdefault(j, i)
            v = w.verdict[j]
            chk = w.check_of_verdict(j)
            if b["approver"] == "standing_intent":
                if v["result"] != "pass":
                    raise bad("a standing-intent approval needs a passing verdict; a pause needs the person's answer")
                if chk is not None and not lim.allows(chk["action"]):
                    raise bad("standing-intent approval for an action the limits in force do not allow")
                if first_intent < 0:
                    raise bad("a standing-intent approval rests on an intent; the journal holds none before it")
                w.limits_at[i] = list(confirmations)
                w.intent_at[i] = first_intent
            elif v["result"] in ("pause", "deny"):
                if b["choice"] not in (v["options"] or ()):
                    raise bad("the choice is not one of the verdict's options")
            if b["card_commitment"] != "" and v["card_commitment"] == "":
                raise bad("the approval commits to a shown card, but the verdict it answers rendered none")
            if b["card_commitment"] != "" and b["card_commitment"] != v["card_commitment"]:
                raise bad("the card shown is not the card checked: the approval's card_commitment differs from the "
                          "verdict's")
            if _person_approver(b["approver"]) and b["card_commitment"] == "":
                raise bad("the person's approval commits to the card it answered (card_commitment)")
    return w


# -- the limits in force ---------------------------------------------------------------


class _Limits:
    """The limits in force (v0 rule 6; Go ``limits``): the first intent's,
    narrowed by later intents, and widened only by a confirmed proposal. A
    floor is held only as its commitment, and an intent that states another
    one makes the floor in force unknown until a confirmation states it.
    ``allowed`` is ``None`` for nil and a list otherwise, as Go's slices."""

    def __init__(self) -> None:
        self.seen = False
        self.allowed: list[str] | None = None
        self.set = False
        self.max_total: int | None = None
        self.delegated: list[str] | None = None
        self.bounds = ""
        self.bounds_known = False

    def narrow(self, b: dict) -> None:
        if not self.seen:
            self.seen, self.bounds_known = True, True
            self.allowed = None if b["allowed"] is None else list(b["allowed"])
            self.set = b["allowed"] is not None
            self.max_total = b["max_total_minor"]
            self.delegated = None if b["delegated"] is None else list(b["delegated"])
            self.bounds = b["bounds_commitment"]
            return
        if b["max_total_minor"] is not None and (self.max_total is None or b["max_total_minor"] < self.max_total):
            self.max_total = b["max_total_minor"]
        if b["bounds_commitment"] != "" and b["bounds_commitment"] != self.bounds:
            self.bounds_known = False
        if b["allowed"] is not None:
            self.allowed = [a for a in b["allowed"] if not self.set or a in (self.allowed or ())]
            self.set = True
        asked = b["asked"]
        if (asked is not None and asked["item"] != "") or b["move"] == "picked":
            self.delegated = [d for d in (self.delegated or ()) if d != "item"]

    def allows(self, action: str) -> bool:
        return not self.set or action in (self.allowed or ())

    def _allowed_or_nil(self) -> list[str] | None:
        if not self.set:
            return None
        return self.allowed if self.allowed is not None else []

    def in_force(self) -> dict:
        """The limits in force as a confirmation's limits.previous states
        them, a Go ``LimitSet`` value."""
        out = gotypes.zero(_LIMIT_SET)
        out["max_total_minor"] = self.max_total
        out["allowed"] = self._allowed_or_nil()
        if self.delegated:
            out["delegated"] = self.delegated
        if self.bounds_known:
            out["bounds_commitment"] = self.bounds
        return out

    def _without_unknown_bounds(self, s: dict) -> dict:
        if not self.bounds_known:
            s = dict(s, bounds_commitment="")
        return s

    def confirm(self, b: dict, prop: dict | None, late: bool) -> None:
        """Applies a confirm_limits approval of the proposal ``prop`` (Go
        ``limits.confirm``); raises ``ValueError`` with Go's text."""
        if late:
            if b["proceed"]:
                raise ValueError("a proposal replaced by a later intent, or already confirmed, cannot be confirmed "
                                 "(proceed must be false)")
            return
        if not b["proceed"]:
            raise ValueError("a confirmation of a standing proposal proceeds")
        if b["limits"] is None or prop is None:
            raise ValueError("a confirmation states the limits it replaces and puts in force")
        if not _same(self._without_unknown_bounds(b["limits"]["previous"]), self.in_force()):
            raise ValueError("limits.previous is not the limits in force")
        nxt = self.in_force()
        if prop["max_total_minor"] is not None:
            nxt["max_total_minor"] = prop["max_total_minor"]
        if prop["allowed"] is not None:
            nxt["allowed"] = prop["allowed"]
        if prop["bounds_commitment"] != "":
            nxt["bounds_commitment"] = prop["bounds_commitment"]
        added = [d for d in (prop["delegated"] or ()) if d not in (self.delegated or ())]
        if added:
            nxt["delegated"] = list(self.delegated or []) + added
        got = b["limits"]["new"]
        if prop["bounds_commitment"] == "":
            got = self._without_unknown_bounds(got)
        if not _same(got, nxt):
            raise ValueError("limits.new is not what the intent proposed")
        more = (prop["max_total_minor"] is not None and self.max_total is not None
                and prop["max_total_minor"] > self.max_total) \
            or (prop["allowed"] is not None and self.set and not all(a in (self.allowed or ()) for a in prop["allowed"])) \
            or (prop["bounds_commitment"] != "" and prop["bounds_commitment"] != self.bounds) \
            or bool(added)
        if not more:
            raise ValueError("the intent asks for no higher limit and no new action: nothing to confirm")
        self.max_total = nxt["max_total_minor"]
        self.allowed = None if nxt["allowed"] is None else list(nxt["allowed"])
        self.set = nxt["allowed"] is not None
        self.delegated = None if nxt["delegated"] is None else list(nxt["delegated"])
        self.bounds, self.bounds_known = b["limits"]["new"]["bounds_commitment"], True


def _same(a: dict, b: dict) -> bool:
    """Go ``sameJSON`` over two ``LimitSet`` values: the same JSON as
    ``json.Marshal`` writes them."""
    return gotypes.write(a, _LIMIT_SET) == gotypes.write(b, _LIMIT_SET)
