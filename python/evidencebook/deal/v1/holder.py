# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 holder's receive profile and act gate (design §8.3), as
Go's ``dealprofile/xdealv1`` ``receive.go`` (``ReceiveProfile``) and
``gate.go`` (``GateAct``).

The profile tells the shelf how to read a received record (a sent pair or a
book pair), who may push it, whether an offer is answered, and who sent a
stamp proof; ``can_act`` and ``gate_act`` say whether the holder's first
act may run, which needs a held stamp proof covering the counterparty's
agreement.
"""
from __future__ import annotations

import bisect
import re
import threading
from dataclasses import dataclass, field
from typing import Any

from ...errors import DeletedError, NotFoundError, UnavailableError
from .check import Record, decode, digest
from .names import (
    KIND_AGREEMENT, KIND_CANCELLATION, KIND_DECLINE, LEG_PAYER, PROFILE, TYPE_AGREE, TYPE_PERFORM, TYPE_PROPOSE,
    TYPE_RULING, TYPE_SETTLE,
)
from .rawjson import GoJSONError, string_member, string_value, unmarshal_map
from .sent import SENT_LOG_PREFIX, STAGE_BOOK, BookRead, Sent, verify_book, verify_sent
from ..check import StageError

__all__ = ["GATE_NO_PROOF", "GATE_NO_LAYER", "ActGate", "ActGateInput", "CanActInput", "ReceiveProfile", "version"]

#: No held stamp proof covers the counterparty's agreement.
GATE_NO_PROOF = "no_counterparty_proof"
#: The counterparty runs no layer: the act runs unguarded.
GATE_NO_LAYER = "counterparty_no_layer"


@dataclass
class _Read:
    book: BookRead | None = None
    sent: Sent | None = None

    @property
    def record(self) -> Record:
        return self.book.record if self.book is not None else self.sent.record


def _read_pair(record: bytes) -> _Read:
    """A received pair: a sent pair when it carries agent_input, else a book
    pair (Go ``readPair``)."""
    try:
        members = unmarshal_map(bytes(record))
    except GoJSONError as exc:
        raise StageError(STAGE_BOOK, f"not a pair: {exc}") from None
    if members is not None and "agent_input" in members:
        return _Read(sent=verify_sent(record))
    return _Read(book=verify_book(record))


@dataclass
class _Ours:
    journal: list[Record]
    agreements: list[Record]

    def offer_by_digest(self, d: str) -> tuple[dict, Record] | None:
        for r in self.journal:
            if r.record_type == TYPE_PROPOSE and digest(r) == d:
                return r.body, r
        return None

    def agreement_record(self, deal_id: str) -> Record | None:
        for r in self.agreements:
            if r.record_type == TYPE_AGREE and r.body["kind"] == KIND_AGREEMENT and r.block["deal_id"] == deal_id:
                return r
        return None

    def agreement(self, deal_id: str) -> dict | None:
        for r in self.agreements:
            if r.record_type == TYPE_AGREE and r.block["deal_id"] == deal_id:
                return r.body
        return None


_V1, _OTHER, _UNAVAILABLE = "v1", "other", "unavailable"


def _v1_payload(book: Any, r: Any) -> tuple[Record | None, str]:
    """A book record's one payload read as an x-deal-v1 record (Go
    ``v1Payload``): the record, or why not: another profile, or a payload
    not readable now (withheld, deleted)."""
    commits = r.header.payload_commitments
    if len(commits) != 1:
        return None, _OTHER
    try:
        raw = book.resolve_payload(r.record_id, commits[0])
    except UnavailableError:
        return None, _UNAVAILABLE
    except NotFoundError:
        return None, _OTHER
    if version(raw) != PROFILE:
        return None, _OTHER
    try:
        return decode(raw), _V1
    except (ValueError, StageError):
        return None, _OTHER


_VERSION_NAME = re.compile(r"x-deal-v[0-9]+\Z")
_TYPED_TYPES = frozenset({
    "task-authority/v0", "proposed-action/v0", "action-evaluation/v0", "action-approval/v0", "action-record/v0",
    "action-outcome/v0", "action-report/v0", "check-request/v0", "check-response/v0", "commercial-bounds/v0",
})


def version(record: bytes) -> str:
    """A deal record's profile, as Go's ``dealprofile.Version`` reads it:
    the one top-level ``x-deal-v<n>`` block whose ``profile`` member names
    it, or ``x-deal-v0`` for a typed record; ``""`` for any other shape."""
    try:
        top = unmarshal_map(record)
    except GoJSONError:
        return ""
    top = top or {}
    names = [k for k in top if _VERSION_NAME.match(k)]
    if len(names) > 1:
        return ""
    typed = ""
    if "type" in top:
        try:
            typed = string_value(top["type"])
        except GoJSONError:
            typed = ""
    if names and typed in _TYPED_TYPES:
        return ""
    if names:
        try:
            profile = string_member(top[names[0]], "profile")
        except GoJSONError:
            return ""
        return names[0] if profile == names[0] else ""
    return "x-deal-v0" if typed in _TYPED_TYPES else ""


def _to(offer_body: dict) -> str | None:
    to = offer_body["to"]
    return to if isinstance(to, str) else None


def _counterparty_key(ag: dict) -> str | None:
    k = ag["counterparty_key"]
    return k if isinstance(k, str) else None


@dataclass
class CanActInput:
    """What :meth:`ReceiveProfile.can_act` reads (Go ``CanActInput``): the
    deal, the record id of the counterparty's agreement, and the shelf log
    the commitment names (``""`` for any)."""

    deal_id: str
    counterpart_agreement: str
    commitment_shelf_log: str = ""


@dataclass
class ActGateInput:
    """What :meth:`ReceiveProfile.gate_act` reads (Go ``ActGateInput``): the
    holder's own book so far, the act about to be sealed, the counterparty's
    agreement and the commitment's shelf log."""

    own: list[Any]
    act: Any
    counterpart_agreement: str
    commitment_shelf_log: str = ""


@dataclass(frozen=True)
class ActGate:
    """Whether an act may be sealed (Go ``ActGate``): ``gated`` when it is
    the holder's first act, ``allowed`` when it may run, ``unguarded`` when
    it runs with no proof because the counterparty runs no layer, and why
    not."""

    gated: bool = False
    allowed: bool = False
    unguarded: bool = False
    reason: str = ""

    def value(self) -> dict:
        out: dict[str, Any] = {"gated": self.gated, "allowed": self.allowed}
        if self.unguarded:
            out["unguarded"] = True
        if self.reason:
            out["reason"] = self.reason
        return out


@dataclass
class _Cache:
    journal_seen: int = 0
    journal: list[Record] = field(default_factory=list)
    journal_seq: list[int] = field(default_factory=list)
    retry: list[int] = field(default_factory=list)
    firsts: dict[str, Record | None] = field(default_factory=dict)


class ReceiveProfile:
    """The x-deal-v1 receive profile of a holder's shelf (Go
    ``ReceiveProfile``). Set :attr:`shelf` before the shelf receives."""

    def __init__(self, shelf: Any = None) -> None:
        self.shelf = shelf
        self._mu = threading.Lock()
        self._last: tuple[str, Record | None] = ("", None)
        self._cache = _Cache()

    # -- what the shelf asks -----------------------------------------------------------

    def read(self, record: bytes):
        """Reads a received pair (Go ``Read``): a book pair is held under its
        book's log, a sent pair under ``x-deal-v1/sent/<digest>``."""
        from ...shelf_receive import ReceivedRecord

        rd = _read_pair(bytes(record))
        with self._mu:
            self._last = (rd.book.record_id if rd.book is not None else rd.sent.capsule_id, rd.record)
        if rd.book is not None:
            b = rd.book
            return ReceivedRecord(b.record_id, b.key_id, b.log_id, b.seq, b.prev, b.record.block["deal_id"])
        s = rd.sent
        return ReceivedRecord(s.capsule_id, s.key_id, SENT_LOG_PREFIX + s.digest, 1, "", s.record.block["deal_id"],
                              offer=s.record.record_type == TYPE_PROPOSE)

    def admit(self, r: Any) -> bool:
        """The push rule (Go ``Admit``): an offer from any key; a decline
        from the key the offer was sent to and a cancellation from the
        offer's signer; a ruling from the adjudicator the agreement names;
        and a deal-book record from the counterparty the agreement names,
        or, before it, the key the offer was sent to."""
        if r.offer:
            return True
        held = self._held_or_pending(r)
        if held is None:
            return False
        rec = held
        o = self._ours()
        if r.log_id.startswith(SENT_LOG_PREFIX):
            if rec.record_type != TYPE_AGREE:
                return False
            ag = rec.body
            if ag["kind"] == KIND_DECLINE:
                found = o.offer_by_digest(ag["offer"]["digest"])
                return found is not None and found[1].block["deal_id"] == rec.block["deal_id"] \
                    and _to(found[0]) == r.sender_key
            if ag["kind"] == KIND_CANCELLATION:
                sent = self._held_sent(ag["offer"]["digest"])
                return sent is not None and sent[1] == r.sender_key and sent[0].record_type == TYPE_PROPOSE \
                    and sent[0].block["deal_id"] == rec.block["deal_id"]
            return False
        if rec.record_type == TYPE_RULING:
            a = o.agreement(rec.block["deal_id"])
            return a is not None and isinstance(a["adjudicator"], dict) and a["adjudicator"]["key_id"] == r.sender_key
        genesis = rec
        if r.position > 1:
            genesis = self._held_genesis(r.sender_key, r.log_id)
            if genesis is None:
                return False
        if genesis.record_type != TYPE_AGREE or genesis.body["kind"] != KIND_AGREEMENT \
                or genesis.block["deal_id"] != r.deal_id:
            return False
        gag = genesis.body
        a = o.agreement(r.deal_id)
        if a is not None:
            return _counterparty_key(a) == r.sender_key and gag["offer"]["digest"] == a["offer"]["digest"]
        found = o.offer_by_digest(gag["offer"]["digest"])
        return found is not None and found[1].block["deal_id"] == r.deal_id and _to(found[0]) == r.sender_key

    def answered(self, offer: Any) -> bool:
        """Whether the holder answered a stored offer (Go ``Answered``): an
        agreement citing it, a decline of it, or a counter-offer to it."""
        h = self.shelf.received_record_of(offer.digest)
        try:
            s = verify_sent(h.record)
        except (ValueError, StageError):
            return False
        o = self._ours()
        for r in o.agreements:
            if r.record_type == TYPE_AGREE and r.body["offer"]["digest"] == s.digest:
                return True
        for r in o.journal:
            if r.record_type == TYPE_AGREE and r.body["kind"] == KIND_DECLINE and r.body["offer"]["digest"] == s.digest:
                return True
            if r.record_type == TYPE_PROPOSE and r.body["counter_to"] is not None \
                    and r.body["counter_to"]["digest"] == s.digest:
                return True
        return False

    def proof_sender(self, checkpoint_key_id: str, log_id: str) -> tuple[str, bool]:
        """Who sent a stamp proof of ``log_id`` under ``checkpoint_key_id``
        (Go ``ProofSender``): the one sender whose held first record of the
        log is an agreement naming that checkpoint key, sent by the key the
        holder expects for that deal; none when it forked or is not one."""
        held = self.shelf.held_first(log_id)
        first: dict[str, str] = {}
        forked: set[str] = set()
        for h in held:
            k = h.received.sender_key
            if k in first and first[k] != h.received.digest:
                forked.add(k)
            first[k] = h.received.digest
        o = self._ours()
        candidates: list[str] = []
        for h in held:
            k = h.received.sender_key
            if k in forked or k in candidates:
                continue
            try:
                b = verify_book(h.record)
            except (ValueError, StageError):
                continue
            if b.record.record_type != TYPE_AGREE:
                continue
            ag = b.record.body
            if ag["kind"] != KIND_AGREEMENT or ag["checkpoint_key"] != checkpoint_key_id:
                continue
            deal = b.record.block["deal_id"]
            expected = ""
            a = o.agreement(deal)
            if a is not None and _counterparty_key(a) is not None:
                expected = _counterparty_key(a)
            else:
                found = o.offer_by_digest(ag["offer"]["digest"])
                if found is not None and found[1].block["deal_id"] == deal and _to(found[0]) is not None:
                    expected = _to(found[0])
            if expected and expected == k:
                candidates.append(k)
        if len(candidates) != 1:
            return "", False
        return candidates[0], True

    def after_receive(self, result: Any, at: Any) -> None:
        """Names, before the receive call returns, each counted ruling of
        the deals the call touched (Go ``AfterReceive``): those whose
        counted records grew, and the received record's own deal, stored or
        held already, so a record sent again retries a naming an earlier
        call failed."""
        from .naming import name_rulings

        deals = list(result.changes.grew or [])
        r = result.record
        if r is not None and r.refused is None and (r.stored or r.duplicate) and r.deal_id and r.deal_id not in deals:
            deals.append(r.deal_id)
        name_rulings(self.shelf, deals, at)

    # -- acting ------------------------------------------------------------------------

    def can_act(self, inp: CanActInput) -> bool:
        """Whether the holder holds a stamp proof covering the counterparty's
        agreement (Go ``CanAct``): the agreement is held, at position 1, of
        this deal, sent by the key the holder expects, under the shelf log
        the commitment names; its sender's counted prefix holds it; and a
        held proof by its checkpoint key, of its shelf log, covers it."""
        try:
            h = self.shelf.received_record_of(inp.counterpart_agreement)
        except (NotFoundError, DeletedError):
            return False
        try:
            b = verify_book(h.record)
        except (ValueError, StageError):
            return False
        if h.received.position != 1 or b.record.record_type != TYPE_AGREE:
            return False
        ag = b.record.body
        if ag["kind"] != KIND_AGREEMENT or b.record.block["deal_id"] != inp.deal_id:
            return False
        sender = h.received.sender_key
        o = self._ours()
        a = o.agreement(inp.deal_id)
        if a is not None:
            if _counterparty_key(a) != sender or ag["offer"]["digest"] != a["offer"]["digest"]:
                return False
        else:
            found = o.offer_by_digest(ag["offer"]["digest"])
            if found is None or found[1].block["deal_id"] != inp.deal_id or _to(found[0]) != sender:
                return False
        if inp.commitment_shelf_log and inp.commitment_shelf_log != ag["shelf_log_id"]:
            return False
        if self.shelf.counted_prefix(sender, h.received.log_id).size < 1:
            return False
        for hp in self.shelf.held_proofs(sender, h.received.log_id):
            v = hp.verified
            if v.fact.get("book_id") == inp.counterpart_agreement and v.key_id == ag["checkpoint_key"] \
                    and v.shelf_checkpoint.log_id == ag["shelf_log_id"] and hp.covers >= 1:
                return True
        return False

    def gate_act(self, inp: ActGateInput) -> ActGate:
        """Whether the holder may seal ``inp.act`` (Go ``GateAct``): an act
        that is not the book's first perform or payer settle is not gated;
        a first one runs unguarded when the counterparty runs no layer, and
        otherwise only when :meth:`can_act` holds. It refuses
        (``ValueError``) a book that is not the holder's own agreement's, and
        an act or record of another deal."""
        own = [r if isinstance(r, Record) else decode(r) for r in inp.own]
        act = inp.act if isinstance(inp.act, Record) else decode(inp.act)
        if not own:
            raise ValueError("xdealv1: gate: the holder's book is empty")
        first = own[0]
        if first.record_type != TYPE_AGREE or first.body["kind"] != KIND_AGREEMENT:
            raise ValueError("xdealv1: gate: the holder's book does not open with its agreement")
        ag = first.body
        deal = first.block["deal_id"]
        o = self._ours()
        mine = o.agreement_record(deal)
        if mine is None:
            raise ValueError(f'xdealv1: gate: the holder has sealed no agreement of deal "{deal}"')
        if digest(first) != digest(mine):
            raise ValueError(f'xdealv1: gate: the book does not open with the holder\'s own agreement of deal "{deal}"')
        if act.block["deal_id"] != deal:
            raise ValueError(f'xdealv1: gate: the act is of deal "{act.block["deal_id"]}", the book of "{deal}"')
        acted = False
        for r in own[1:]:
            if r.block["deal_id"] != deal:
                raise ValueError(f'xdealv1: gate: a record of deal "{r.block["deal_id"]}" in the book of "{deal}"')
            acted = acted or _gated_act(r)
        if not _gated_act(act) or acted:
            return ActGate(allowed=True)
        if ag["counterparty_layer"] is not True:
            return ActGate(gated=True, allowed=True, unguarded=True, reason=GATE_NO_LAYER)
        if not self.can_act(CanActInput(deal, inp.counterpart_agreement, inp.commitment_shelf_log)):
            return ActGate(gated=True, reason=GATE_NO_PROOF)
        return ActGate(gated=True, allowed=True)

    # -- what the holder holds ---------------------------------------------------------

    def _ours(self) -> _Ours:
        """The holder's v1 journal records and the agreements its books open
        with (Go ``ours``), read incrementally: a payload not readable now is
        read again next time."""
        if self.shelf is None:
            raise ValueError("xdealv1: the receive profile has no shelf")
        with self._mu:
            c = self._cache
            journal = self.shelf.journal()
            size = journal.size()
            seqs = list(c.retry) + list(range(c.journal_seen + 1, size + 1))
            added: list[tuple[int, Record]] = []
            retry: list[int] = []
            wanted = set(seqs)
            by_seq = {r.seq: r for r in journal.records() if r.seq in wanted} if seqs else {}
            for n in seqs:
                r = by_seq.get(n)
                if r is None:
                    continue
                rec, kind = _v1_payload(journal, r)
                if kind == _V1:
                    added.append((n, rec))
                elif kind == _UNAVAILABLE:
                    retry.append(n)
            if seqs:
                for n, rec in added:
                    at = bisect.bisect_left(c.journal_seq, n)
                    c.journal_seq.insert(at, n)
                    c.journal.insert(at, rec)
                c.journal_seen, c.retry = size, retry
            out = _Ours(list(c.journal), [])
            for log_id in self.shelf.books():
                if log_id in c.firsts:
                    first = c.firsts[log_id]
                else:
                    book = self.shelf.open(log_id)
                    first, unavailable = None, False
                    recs = [r for r in book.records() if r.seq == 1]
                    if len(recs) == 1:
                        rec, kind = _v1_payload(book, recs[0])
                        if kind == _V1 and rec.record_type == TYPE_AGREE and rec.body["kind"] == KIND_AGREEMENT:
                            first = rec
                        unavailable = kind == _UNAVAILABLE
                    if not unavailable:
                        c.firsts[log_id] = first
                if first is not None:
                    out.agreements.append(first)
            return out

    def _held_sent(self, d: str) -> tuple[Record, str] | None:
        for h in self.shelf.held_first(SENT_LOG_PREFIX + d):
            try:
                s = verify_sent(h.record)
            except (ValueError, StageError):
                continue
            if s.digest == d:
                return s.record, h.received.sender_key
        return None

    def _held_genesis(self, sender_key: str, log_id: str) -> Record | None:
        out = None
        for h in self.shelf.held_first(log_id):
            if h.received.sender_key != sender_key:
                continue
            if out is not None:
                return None
            try:
                out = verify_book(h.record).record
            except (ValueError, StageError):
                return None
        return out

    def _held_or_pending(self, r: Any) -> Record | None:
        with self._mu:
            last_digest, last_rec = self._last
        if last_digest == r.digest:
            return last_rec
        try:
            h = self.shelf.received_record_of(r.digest)
        except (NotFoundError, DeletedError):
            return None
        try:
            return _read_pair(h.record).record
        except (ValueError, StageError):
            return None


def _gated_act(r: Record) -> bool:
    if r.record_type == TYPE_PERFORM:
        return True
    return r.record_type == TYPE_SETTLE and r.body["leg"] == LEG_PAYER
