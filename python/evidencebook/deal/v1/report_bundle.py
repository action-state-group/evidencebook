# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 bundle extension, as Go's ``report_bundle.go`` (``ReportBundle``,
``ReportBook``, ``VerifyReportBundle``; design note §9, "The bundle extension";
PROFILE §10), held to Go by ``testdata/deal-v1/report-bundle`` and the x-deal-v1
differential (``test/interop/deal-v1``).

What a bundle carrying a v1 report adds so a reader can verify the report and
read the records its rows rely on, the payment legs a payment check read
included. The host builds and seals the bundle; this module defines the
extension and verifies it.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field

from ..check import StageError, _go_quote
from . import exact, rawjson
from ..names import AUDIENCE_ADJUDICATOR, AUDIENCE_COUNTERPARTY, AUDIENCE_KEEP
from .names import ROLE_BUYER, ROLE_SELLER
from .report import ROWS_LISTED, ROWS_NOT_STARTED, ROWS_WITHHELD, SIDE_ADJUDICATOR
from .report_verify import STAGE_REPORT, ReportRead, verify_report
from .sent import _compact, _err, _raw_message, verify_book
from .start import STATE_STARTED

__all__ = ["ReportBook", "ReportBundle", "verify_report_bundle"]

_EXTENSION = {"sealed_report": None, "book": None}
_BOOK = {"agreement": None, "relied_on": None}


@dataclass(frozen=True)
class ReportBook:
    """The extension's book (Go ``ReportBook``): the holder's deal book record
    at seq 1, the agreement :func:`.report_verify.verify_report` reads the
    report against, and every record the report's rows rely on, by record id,
    each as its book pair ``{record, header, payload}`` (its bytes as the book
    sealed them). The relied-on records are those of every row the holder
    computed, so a copy whose rows are withheld (an adjudicator's) or not
    started carries the same records as one that lists them."""

    agreement: bytes
    relied_on: dict[str, bytes] = field(default_factory=dict)


@dataclass(frozen=True)
class ReportBundle:
    """The x-deal-v1 bundle extension (Go ``ReportBundle``): the sealed
    report's Capsule id, as x-deal-v0's ``sealed_report``, and the book."""

    sealed_report: str
    book: ReportBook

    def json(self) -> bytes:
        """The extension as Go's ``json.Marshal`` writes it: the members in
        field order, the relied_on keys in byte order, each pair compacted as
        a ``json.RawMessage`` is. Raises :class:`ValueError` for a pair that
        is not one JSON value, as Go's ``json.Marshal`` errs."""

        def text(b: bytes) -> str:
            # json.Marshal of a json.RawMessage: text that is not one JSON
            # value is refused, then compacted.
            return _compact(_raw_message(b))

        def key(s: str) -> str:
            return _compact(json.dumps(s, ensure_ascii=False))

        relied = ",".join(f"{key(k)}:{text(self.book.relied_on[k])}"
                          for k in sorted(self.book.relied_on, key=lambda k: k.encode("utf-8")))
        out = (f'{{"sealed_report":{key(self.sealed_report)},"book":{{"agreement":{text(self.book.agreement)},'
               f'"relied_on":{{{relied}}}}}}}')
        return out.encode("utf-8", "surrogateescape")


def _bad(detail: str) -> StageError:
    return _err(STAGE_REPORT, f"the x-deal-v1 extension: {detail}")


def _read(ext: bytes) -> tuple[str, str | None, dict[str, str] | None]:
    """The extension as Go's ``strictUnmarshal`` into ``ReportBundle`` and
    ``exactObject`` read it: the sealed report id, the agreement's text (a
    null kept as the text ``null``, as ``json.RawMessage`` keeps it) and the
    relied_on pairs' texts (``None`` for a missing member or a null)."""
    text = rawjson.text_check(bytes(ext))
    try:
        top = rawjson.strict_struct(text, {"sealed_report": "raw", "book": "raw"})
        sealed = "" if top["sealed_report"] is None else rawjson.string_value(top["sealed_report"])
        book = top["book"]
        book_members = rawjson.strict_struct(book, {"agreement": "raw", "relied_on": "map"}) \
            if book is not None else {"agreement": None, "relied_on": None}
        # Go's exactObject (evidencebook#559): the reader above also takes a
        # member in another case.
        why = exact.exact_object(rawjson.unmarshal_map(text) or {}, _EXTENSION, False, "the extension")
        if not why and book is not None:
            why = exact.exact_object(rawjson.unmarshal_map(book) or {}, _BOOK, False, "the extension.book")
    except rawjson.GoJSONError as exc:
        raise _bad(str(exc)) from None
    if why:
        raise _bad(why)
    return sealed, book_members["agreement"], book_members["relied_on"]


def verify_report_bundle(report: bytes, ext: bytes) -> ReportRead:
    """Checks a sealed v1 report (its pair, as :func:`.report_verify.verify_report`
    reads it) with the x-deal-v1 bundle extension that carries it, ``ext`` (Go
    ``VerifyReportBundle``), so every host and page applies one rule. ``ext``
    is exactly ``{sealed_report, book: {agreement, relied_on}}``, relied_on an
    object (empty when no row relies on a record). It is refused unless:

    - the report passes ``verify_report`` against ``book.agreement``, and
      ``sealed_report`` is the report Capsule's id;
    - the copy's audience and rows agree: an adjudicator's copy is withheld
      with no rows, no other copy is withheld, rows appear only when listed,
      and a keep or counterparty copy is listed exactly when its deal has
      started;
    - each relied_on pair passes ``verify_book``, is of the report's deal, has
      its record id as its key, and is signed by a party of the agreement: the
      agreement's own key (the holder), its counterparty_key, or its
      adjudicator's key; the holder's sit in rests_on.own's log at a seq no
      greater than its size, and, where the copy names rests_on.counterparty,
      the counterparty's sit in that log at a seq no greater than its counted;
      a keep or counterparty copy that names none carries no counterparty
      record, and in an adjudicator's copy the counterparty's records sit in
      one log;
    - an adjudicator's copy, and only it, names a referee;
    - when the rows are listed, the keys are exactly the records the rows name
      in relied_on, each named with the side of its pair's signer.

    A bundle that passes shows the records the holder says its rows rest on,
    signed by the deal's parties; like the report, it is not a recomputation.
    In a copy whose rows are not listed nothing ties the keys to rows; an
    adjudicator's records have no log bound, and in an adjudicator's copy,
    which names no counterparty prefix, the counterparty's records are bound
    only to one log. Raises :class:`StageError` where Go refuses."""
    sealed, agreement, relied_on = _read(ext)
    if agreement is None or relied_on is None:
        raise _bad("the book carries an agreement and a relied_on object")
    first_bytes = rawjson._b(agreement)
    read = verify_report(report, first_bytes)
    r = read.report
    if sealed != read.capsule_id:
        raise _bad(f"sealed_report {_go_quote(sealed)} is not the report Capsule's id {read.capsule_id}")
    adj = r["audience"] == AUDIENCE_ADJUDICATOR
    if r["audience"] not in (AUDIENCE_KEEP, AUDIENCE_COUNTERPARTY) and not adj:
        raise _bad(f"audience {_go_quote(r['audience'])} is not a copy audience")
    if adj != (r.get("referee", "") != ""):
        raise _bad("an adjudicator's copy, and only it, names a referee")
    if adj != (r["rows_state"] == ROWS_WITHHELD):
        raise _bad(f"an adjudicator's copy, and only it, is withheld; this {r['audience']} copy is "
                   f"{_go_quote(r['rows_state'])}")
    listed = r["rows_state"] == ROWS_LISTED
    if not listed and r["rows"]:
        raise _bad(f"a {r['rows_state']} copy lists no rows")
    if not adj and listed != (r["start"]["state"] == STATE_STARTED):
        raise _bad("a keep or counterparty copy is listed exactly when its deal has started; this one is "
                   f"{_go_quote(r['rows_state'])} at start {_go_quote(r['start']['state'])}")
    if not adj and not listed and r["rows_state"] != ROWS_NOT_STARTED:
        raise _bad(f"rows_state {_go_quote(r['rows_state'])} is not one of listed, not_started, withheld")

    first = verify_book(first_bytes)
    ag = first.record.body  # verify_report checked it is the agreement
    other = ROLE_SELLER if ag["role"] == ROLE_BUYER else ROLE_BUYER
    side_of = {first.key_id: ag["role"]}
    if isinstance(ag.get("counterparty_key"), str):
        side_of[ag["counterparty_key"]] = other
    if isinstance(ag.get("adjudicator"), dict):
        side_of[ag["adjudicator"]["key_id"]] = SIDE_ADJUDICATOR
    own, cp = r["rests_on"]["own"], r["rests_on"].get("counterparty")
    sides: dict[str, str] = {}
    counterpart_log = ""
    for rid in sorted(relied_on, key=lambda k: k.encode("utf-8", "surrogateescape")):
        b = verify_book(rawjson._b(relied_on[rid]))
        if b.record_id != rid:
            raise _bad(f"relied_on {rid} holds the record {b.record_id}")
        if b.record.block["deal_id"] != r["deal_id"]:
            raise _bad(f"relied_on {rid} is of deal {_go_quote(b.record.block['deal_id'])}, not the report's")
        side = side_of.get(b.key_id)
        if side is None:
            raise _bad(f"relied_on {rid} is signed by no party of the agreement")
        theirs = side == other
        if side == ag["role"] and (b.log_id != own["log_id"] or b.seq > own["size"]):
            raise _bad(f"relied_on {rid} is not in the holder's book the report rests on")
        if theirs and cp is not None and (b.log_id != cp["log_id"] or b.seq > cp["counted"]):
            raise _bad(f"relied_on {rid} is not in the counterparty's prefix the report rests on")
        if theirs and cp is None and not adj:
            raise _bad(f"relied_on {rid} is the counterparty's, and the report counted none of its book")
        # An adjudicator's copy names no counterparty prefix: its records sit
        # in one book at least.
        if theirs and counterpart_log != "" and b.log_id != counterpart_log:
            raise _bad(f"relied_on {rid} is in another counterparty book than {counterpart_log}")
        if theirs:
            counterpart_log = b.log_id
        sides[rid] = side
    if not listed:
        return read
    named: set[str] = set()
    for row in r["rows"]:
        for src in row["relied_on"]:
            side = sides.get(src["record"])
            if side is None:
                raise _bad(f"row {_go_quote(row['id'])} relies on {src['record']}, which relied_on does not carry")
            if side != src["side"]:
                raise _bad(f"row {_go_quote(row['id'])} names {src['record']} as the {src['side']}'s, "
                           f"and it is signed by the {side}")
            named.add(src["record"])
    for rid in sorted(sides, key=lambda k: k.encode("utf-8", "surrogateescape")):
        if rid not in named:
            raise _bad(f"relied_on carries {rid}, on which no row relies")
    return read
