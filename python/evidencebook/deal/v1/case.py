# SPDX-License-Identifier: Apache-2.0
"""An adjudicator's rules (evidencebook#779), as Go's ``case_check.go``
(``CaseCopy``, ``Case``, ``CheckCase``, ``CheckCaseRecord``, ``SealRuling``,
``SealedRuling``, ``ErrRulingConflict``): what makes a dispute and the
parties' adjudicator copies a case for an adjudicator, what a later record of
that case must be, and how a ruling is sealed in the case book. A host
building an adjudicator calls these, so every adjudicator built on this
library applies the same rules.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

from agent_action_capsule.canonical import jcs

from ...errors import BusyError
from ..check import StageError
from ..names import AUDIENCE_ADJUDICATOR
from . import gotypes
from .check import Record, check, check_dispute, check_ruling, decode, encode, ref_to
from .holder import _V1, case_log_id, _v1_payload
from .names import (
    CANONICALIZATION, KIND_AGREEMENT, KIND_CHALLENGE, KIND_DISPUTE, KIND_WITHDRAWAL, PROFILE, ROLE_BUYER,
    ROLE_SELLER, TYPE_RULING,
)
from .naming import book_entry
from .report_bundle import _read as _read_extension, verify_report_bundle
from .sent import BookRead, book_pair, verify_book

__all__ = ["STAGE_CASE", "Case", "CaseCopy", "RulingConflictError", "SealedRuling", "check_case", "check_case_record",
           "seal_ruling"]

#: A dispute, copy or later record read as a case (Go ``StageCase``).
STAGE_CASE = "case"

#: How many times :func:`seal_ruling` rebuilds a ruling whose place another
#: write took (Go ``sealTries``).
_SEAL_TRIES = 8


class RulingConflictError(ValueError):
    """The case book already holds a different final ruling on this row (Go
    ``ErrRulingConflict``): the first final ruling applies, and its
    adjudicator never replaces it; a challenge of it records disagreement."""

    def __init__(self) -> None:
        super().__init__("xdealv1: the case book already holds a different final ruling on this row")


def _case_err(detail: str) -> StageError:
    return StageError(STAGE_CASE, detail)


@dataclass(frozen=True)
class CaseCopy:
    """One party's adjudicator copy as its bundle carries it (Go
    ``CaseCopy``): the report pair (the bundle's report extension) and the
    x-deal-v1 extension, as :func:`.report_bundle.verify_report_bundle` takes
    them. The bundle itself is the host's to verify."""

    report: bytes
    extension: bytes


@dataclass(frozen=True)
class Case:
    """A dispute and one or both parties' adjudicator copies read as a case
    for one adjudicator (Go ``Case``): the deal, the disputed row, both
    sides' keys (``{"buyer", "seller"}``), what the adjudicator read of each
    side's book (``None`` for a side it holds no copy of), the agreement the
    ruling is read against, the dispute's forum, the agreement's rules digest
    and the deal's case log."""

    deal_id: str
    row: str
    dispute: BookRead
    keys: dict
    read: dict
    agreement: Record
    offer_digest: str
    forum: str
    rules_digest: str
    log_id: str

    def ruling_of(self, value: str, against: str | None = None, remedy: dict | None = None, final: bool = False,
                  forum: str = "") -> dict:
        """The ruling body of this case with the adjudicator's decision
        (Go ``Case.RulingOf``): its value, against whom (``None`` for
        neither), its remedy, whether it is final, and its forum (``""`` for
        the dispute's)."""
        body: dict = {"row": self.row, "keys": dict(self.keys), "against": against, "value": value,
                      "read": dict(self.read), "forum": forum or self.forum, "rules_digest": self.rules_digest,
                      "final": final}
        if remedy is not None:
            body["remedy"] = remedy
        return body


@dataclass(frozen=True)
class SealedRuling:
    """A ruling in the case book (Go ``SealedRuling``): its place, record id,
    record digest, record and book pair, the case book's first record id (the
    book id a stamp proof of it names), and whether it was already there."""

    seq: int
    record_id: str
    digest: str
    record: Record
    pair: bytes
    book_id: str
    existing: bool


@dataclass(frozen=True)
class _CaseCopy:
    role: str
    key: str
    last: str
    agreement: Record
    ag: dict


def _read_case_copy(c: CaseCopy, adjudicator_key: str) -> _CaseCopy:
    """Reads one copy: it passes :func:`verify_report_bundle`, is an
    adjudicator copy, and its agreement names ``adjudicator_key`` (Go
    ``readCaseCopy``)."""
    read = verify_report_bundle(c.report, c.extension)
    audience = read.report["audience"]
    if audience != AUDIENCE_ADJUDICATOR:
        raise _case_err(f"a {audience} copy is not an adjudicator copy")
    first = verify_book(_agreement_pair(c.extension))
    ag = first.record.body
    if first.record.record_type != "agree" or ag["kind"] != KIND_AGREEMENT:
        raise _case_err("the copy carries no agreement")
    if not isinstance(ag["adjudicator"], dict) or ag["adjudicator"]["key_id"] != adjudicator_key:
        raise _case_err("the copy's agreement does not name this adjudicator")
    if ag["role"] not in (ROLE_BUYER, ROLE_SELLER):
        raise _case_err(f"the copy's agreement has role {ag['role']}")
    return _CaseCopy(ag["role"], first.key_id, read.report["rests_on"]["own"]["last"], first.record, ag)


def _agreement_pair(ext: bytes) -> bytes:
    """The extension's ``book.agreement`` as the bytes of its pair (Go
    reads it into a ``json.RawMessage``); the extension verified already."""
    return _read_extension(bytes(ext))[1].encode("utf-8", "surrogateescape")


def _names_counterparty(ag: dict, key: str) -> bool:
    """Whether ``ag`` names ``key`` as its layered counterparty (Go
    ``namesCounterparty``). With two copies both sides keep a deal book, so
    each agreement must name the other copy's signer: an agreement whose
    counterparty runs no layer has no second book, and a second copy beside it
    can only be forged."""
    return ag["counterparty_layer"] is True and ag["counterparty_key"] == key and ag["counterparty_key"] is not None


def _same_adjudicator(a: Any, b: Any) -> bool:
    if a is None or b is None:
        return a is None and b is None
    return a == b


def check_case(dispute: bytes, copies: list[CaseCopy], adjudicator_key: str) -> Case:
    """Reads a dispute (its book pair) and one or both parties' adjudicator
    copies as a case for the adjudicator whose key id is ``adjudicator_key``
    (Go ``CheckCase``):

    - the dispute verifies and is of kind dispute, names ``adjudicator_key``,
      and is signed by a party of the copies' deal;
    - each copy passes ``verify_report_bundle``, is an adjudicator copy, and
      its agreement names ``adjudicator_key``;
    - the copies are of one deal and one offer, one per side, and with two,
      each agreement names the other copy's signer as its layered
      counterparty and both name the same adjudicator terms and forums;
    - with one copy, the other side's key is that agreement's
      counterparty_key, and a copy whose agreement names none is refused;
    - the dispute is read against its own agreement (``check_dispute``) when
      the disputing side's copy is given, and otherwise its forum against the
      agreement's forums."""
    dread = verify_book(dispute)
    d = dread.record.body
    if dread.record.record_type != "dispute" or d["kind"] != KIND_DISPUTE:
        raise _case_err("the record is not a dispute")
    if d["adjudicator"] != adjudicator_key:
        raise _case_err("the dispute goes to another adjudicator")
    if len(copies) < 1 or len(copies) > 2:
        raise _case_err(f"a case takes one or two adjudicator copies, not {len(copies)}")
    cs: list[_CaseCopy] = []
    for c in copies:
        cc = _read_case_copy(c, adjudicator_key)
        if cc.agreement.block["deal_id"] != dread.record.block["deal_id"]:
            raise _case_err(f"a copy is of deal {cc.agreement.block['deal_id']}, "
                            f"the dispute of deal {dread.record.block['deal_id']}")
        if cs:
            o = cs[0]
            if cc.ag["offer"]["digest"] != o.ag["offer"]["digest"]:
                raise _case_err("the two copies answer different offers")
            if cc.role == o.role:
                raise _case_err(f"both copies are the {cc.role}'s: a case takes one copy per side")
            # Each side's agreement names the other's key: a copy under a key
            # the other agreement does not name is not of this deal.
            if not _names_counterparty(cc.ag, o.key) or not _names_counterparty(o.ag, cc.key):
                raise _case_err("the two copies' agreements do not name each other's keys")
            if not _same_adjudicator(cc.ag["adjudicator"], o.ag["adjudicator"]) \
                    or sorted(cc.ag["forums"] or []) != sorted(o.ag["forums"] or []):
                raise _case_err("the two copies' agreements name different adjudicator terms or forums")
        cs.append(cc)
    keys: dict[str, str] = {}
    read: dict[str, str | None] = {"buyer": None, "seller": None}
    for cc in cs:
        keys[cc.role] = cc.key
        read[cc.role] = cc.last
    if len(cs) == 1:
        cc = cs[0]
        other = ROLE_SELLER if cc.role == ROLE_BUYER else ROLE_BUYER
        if cc.ag["counterparty_layer"] is not True or cc.ag["counterparty_key"] is None:
            raise _case_err("the copy's agreement names no counterparty key, so the other side's key is unknown: "
                            "give its copy too")
        keys[other] = cc.ag["counterparty_key"]
    if dread.key_id != keys.get(ROLE_BUYER) and dread.key_id != keys.get(ROLE_SELLER):
        raise _case_err("the dispute is signed by neither party of the deal")
    checked = False
    for cc in cs:
        if cc.key == dread.key_id:
            check_dispute(dread.record, cc.agreement)
            checked = True
    # The ruling is read against the disputing side's agreement when its copy
    # is given; the two agreements agree on the adjudicator and forums anyway.
    first = cs[0]
    for cc in cs:
        if cc.key == dread.key_id:
            first = cc
    if not checked and d["forum"] not in (first.ag["forums"] or []):
        raise _case_err(f"the dispute's forum {d['forum']} is not one the agreement accepts")
    return Case(
        deal_id=dread.record.block["deal_id"], row=d["row"], dispute=dread,
        keys={"buyer": keys.get(ROLE_BUYER, ""), "seller": keys.get(ROLE_SELLER, "")}, read=read,
        agreement=first.agreement, offer_digest=first.ag["offer"]["digest"], forum=d["forum"],
        rules_digest=first.ag["adjudicator"]["rules_digest"], log_id=case_log_id(first.ag["offer"]["digest"],
                                                                                 adjudicator_key))


def check_case_record(record: bytes, c: Case, rulings: list[str]) -> BookRead:
    """Reads a later record of an open case (Go ``CheckCaseRecord``): a
    withdrawal or a challenge, which verifies, is of the case's deal, and is
    signed by one of its parties; a withdrawal is of the case's row, and a
    challenge names a ruling whose record digest is among ``rulings``, the
    case book's."""
    read = verify_book(record)
    d = read.record.body
    if read.record.record_type != "dispute" or d["kind"] not in (KIND_WITHDRAWAL, KIND_CHALLENGE):
        raise _case_err("the record is neither a withdrawal nor a challenge")
    if read.record.block["deal_id"] != c.deal_id:
        raise _case_err(f"the record is of deal {read.record.block['deal_id']}, the case of deal {c.deal_id}")
    if read.key_id != c.keys["buyer"] and read.key_id != c.keys["seller"]:
        raise _case_err("the record is signed by neither party of the case")
    if d["kind"] == KIND_WITHDRAWAL and d["row"] != c.row:
        raise _case_err(f"the withdrawal is of row {d['row']}, the case of row {c.row}")
    if d["kind"] == KIND_CHALLENGE and (d["ruling"] is None or d["ruling"]["digest"] not in rulings):
        raise _case_err("the challenge names a ruling this case book does not hold")
    return read


def _same_read(a: dict, b: dict) -> bool:
    return a["buyer"] == b["buyer"] and a["seller"] == b["seller"]


def _body_bytes(body: dict) -> bytes:
    """A ruling body's canonical bytes: two rulings are the same when these
    are (Go ``bodyBytes``)."""
    return jcs(gotypes.write(body, gotypes.body_type(TYPE_RULING)))


def seal_ruling(shelf: Any, c: Case, body: dict, at: datetime) -> SealedRuling:
    """Seals ``body`` in the case book of ``c`` on ``shelf``, at time ``at``,
    once (Go ``SealRuling``): a ruling of the deal with the same body already
    there is returned as it is (``existing``), and any other ruling on a row
    the book already holds a final ruling on is refused
    (:class:`RulingConflictError`). The body must be ``c``'s: its row, keys,
    read and rules digest (:meth:`Case.ruling_of` builds one). The ruling is
    checked (``check``, ``check_ruling`` against ``c.agreement``, the
    case-book placement) before it is appended at the book's next place."""
    book = shelf.open(c.log_id)
    if body["row"] != c.row:
        raise _case_err(f"the ruling is on row {body['row']}, the case on row {c.row}")
    if body["keys"] != c.keys:
        raise _case_err("the ruling names other keys than the case's parties")
    if not _same_read(body["read"], c.read):
        raise _case_err("the ruling's read is not what the case read")
    if body["rules_digest"] != c.rules_digest:
        raise _case_err("the ruling names other rules than the agreement's")
    want = _body_bytes(_full(body))
    for _ in range(_SEAL_TRIES):
        rows = book.records()
        size = len(rows)
        book_id = prev_id = ""
        for i, r in enumerate(rows):
            if i == 0:
                book_id = r.record_id
            prev_id = r.record_id
            rec, kind = _v1_payload(book, r)
            if kind != _V1:
                raise ValueError(f"xdealv1: the case book's record {i + 1} is not readable")
            if rec.record_type != TYPE_RULING or rec.block["deal_id"] != c.deal_id:
                continue
            if _body_bytes(rec.body) == want:
                return _sealed_at(book, r, rec, i + 1, book_id, True)
            if rec.body["final"] and rec.body["row"] == body["row"]:
                raise RulingConflictError()
        block = {"profile": PROFILE, "canonicalization": CANONICALIZATION, "deal_id": c.deal_id,
                 "record_type": TYPE_RULING, "at": at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                 "seq": size + 1}
        if size > 0:
            last = rows[-1]
            if len(last.header.payload_commitments) != 1:
                raise ValueError("xdealv1: the case book's last record has no record digest")
            block["prev"] = ref_to(last.header.payload_commitments[0])
        rec = decode(jcs({PROFILE: block, "body": gotypes.write(_full(body), gotypes.body_type(TYPE_RULING))}))
        check(rec)
        check_ruling(rec, c.agreement)
        entry = book_entry(rec, prev_id)
        try:
            stored = book.append_at(size + 1, entry)
        except BusyError:
            continue
        return _sealed_at(book, stored, rec, size + 1, book_id or stored.record_id, False)
    raise BusyError("the case book kept changing while the ruling was sealed")


def _full(body: dict) -> dict:
    """``body`` with every member of the ruling type present (the remedy
    absent when none), as a record's body holds it."""
    out = dict(body)
    out.setdefault("remedy", None)
    return out


def _sealed_at(book: Any, r: Any, rec: Record, seq: int, book_id: str, existing: bool) -> SealedRuling:
    payload, d = encode(rec)
    stored = book._stored[r.record_id]
    pair = book_pair(jcs(stored.capsule), stored.envelope, stored.header, payload)
    return SealedRuling(seq, r.record_id, d, rec, pair, book_id, existing)
