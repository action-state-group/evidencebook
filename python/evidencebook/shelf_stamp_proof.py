# SPDX-License-Identifier: Apache-2.0
"""A stamp proof (evidencebook#461, docs/design/461-shelf-facts.md §3),
member for member with Go's ``shelf_stamp_proof.go``.

A stamp proof shows that a holder's shelf log carries a fact of one of its
books: the fact, the book checkpoint it carries, the fact's inclusion in the
shelf log and the shelf checkpoint that includes it. One checkpoint key
signs both checkpoints: the proof's key. Its JSON form is Go's
``encoding/json`` of ``StampProof``: byte strings as standard base64 with
padding, a status opening ``{salt, value}`` and the inclusion
``{kind, seq, proof}``, each raw value keeping its own member order.
"""
from __future__ import annotations

import base64
import hashlib
import json
from dataclasses import dataclass
from typing import Any

from cll.checkpoint import core

from . import shelf_facts
from ._cll import Checkpoint, read_checkpoint
from ._gojson import Ordered
from .errors import CorruptError, InvalidError, NotFoundError
from .shelf_handover import _NO_VALUE, _hex32, _list, _no_constant, _opening_from, _opening_value, _str, _uint, _unb64, \
    canonical_document

__all__ = ["INCLUSION_KIND", "StampProof", "VerifiedStampProof", "stamp_proof_locked", "verify_stamp_proof"]

#: The kind of a stamp proof's inclusion evidence (Go ``InclusionEvidence.Kind``).
INCLUSION_KIND = "cll-mmr-inclusion"


def _bytes(value: Any, what: str) -> bytes:
    """A Go ``[]byte`` member: JSON null or absent is empty."""
    return b"" if value is None else _unb64(value, what)


@dataclass(frozen=True)
class StampProof:
    """Go ``StampProof``. ``fact`` is the fact document's canonical JSON, a
    book fact; ``status_opening`` the fact's status opening when the
    audience the proof was made for may have it; ``book_checkpoint`` and
    ``shelf_checkpoint`` the COSE statements; ``inclusion`` the fact entry's
    inclusion evidence ``{kind, seq, proof}`` in the shelf log, under the
    shelf checkpoint."""

    fact: bytes
    book_checkpoint: bytes
    inclusion: dict
    shelf_checkpoint: bytes
    status_opening: shelf_facts.Opening | None = None

    def value(self) -> Ordered:
        """Go's ``json.Marshal`` of the proof, as a value for ``go_json``."""
        out = Ordered(fact=base64.b64encode(self.fact).decode())
        if self.status_opening is not None:
            out["status_opening"] = _opening_value(self.status_opening)
        out["book_checkpoint"] = base64.b64encode(self.book_checkpoint).decode()
        out["inclusion"] = Ordered(kind=self.inclusion.get("kind", ""), seq=self.inclusion.get("seq", 0),
                                   proof=self.inclusion.get("proof"))
        out["shelf_checkpoint"] = base64.b64encode(self.shelf_checkpoint).decode()
        return out

    @classmethod
    def from_value(cls, value: Any) -> StampProof:
        """Read a proof's JSON value (parsed with ``object_pairs_hook=Ordered``
        to keep raw values' order), as Go's ``json.Unmarshal`` reads it: an
        absent or null member is its zero value, another type is refused."""
        if not isinstance(value, dict):
            raise InvalidError("stamp proof: not a JSON object")
        inclusion = value.get("inclusion")
        if inclusion is None:
            inclusion = {}
        if not isinstance(inclusion, dict):
            raise InvalidError("stamp proof: the inclusion is an object")
        kind = inclusion.get("kind")
        seq = inclusion.get("seq")
        return cls(
            fact=_bytes(value.get("fact"), "the fact"),
            book_checkpoint=_bytes(value.get("book_checkpoint"), "the book checkpoint"),
            inclusion=Ordered(kind="" if kind is None else _str(kind, "the inclusion's kind"),
                              seq=0 if seq is None else _uint(seq, "the inclusion's seq"),
                              proof=inclusion.get("proof")),
            shelf_checkpoint=_bytes(value.get("shelf_checkpoint"), "the shelf checkpoint"),
            status_opening=_opening_from(value.get("status_opening"), "the status opening"),
        )

    @classmethod
    def loads(cls, text: str | bytes) -> StampProof:
        try:
            value = json.loads(text, object_pairs_hook=Ordered, parse_constant=_no_constant)
        except RecursionError:
            raise InvalidError("stamp proof: nested too deeply") from None
        except (ValueError, TypeError) as exc:
            raise InvalidError(f"stamp proof: {exc}") from None
        return cls.from_value(value)


@dataclass(frozen=True)
class VerifiedStampProof:
    """What a stamp proof shows once verified; ``key_id`` is the checkpoint
    key that signed both checkpoints."""

    fact: dict
    book_checkpoint: Checkpoint
    shelf_checkpoint: Checkpoint
    key_id: str


def verify_stamp_proof(p: StampProof) -> VerifiedStampProof:
    """Check a stamp proof on its own (§3, Go ``VerifyStampProof``): both
    checkpoints verify and are signed by one key; the fact is a canonical book
    fact whose log and size are the book checkpoint's, and whose
    checkpoint_sha256 is the statement's digest; a status opening given
    recomputes its commitment; and the fact's entry is included under the
    shelf checkpoint. Raises :class:`InvalidError`. What the proof says
    against the records held is the store's (``Shelf.receive``)."""

    def bad(message: str) -> InvalidError:
        return InvalidError(f"stamp proof: {message}")

    try:
        book = read_checkpoint(p.book_checkpoint)
    except InvalidError as exc:
        raise bad(f"the book checkpoint: {exc}") from None
    try:
        shelf = read_checkpoint(p.shelf_checkpoint)
    except InvalidError as exc:
        raise bad(f"the shelf checkpoint: {exc}") from None
    if book.key_id != shelf.key_id:
        raise bad(f"the book checkpoint is signed by {book.key_id}, the shelf checkpoint by {shelf.key_id}")
    try:
        f = canonical_document(p.fact, shelf_facts.FACT_KIND_BOOK)
    except InvalidError as exc:
        raise bad(f"the fact: {exc}") from None
    if f["v"] != 1 or f["kind"] != shelf_facts.FACT_KIND_BOOK:
        raise bad(f"a {json.dumps(f['kind'])} of version {f['v']}, not a book fact")
    if f["log_id"] != book.log_id or f["size"] != book.tree_size:
        raise bad(f"the fact names {f['log_id']} at {f['size']}, its book checkpoint {book.log_id} at {book.tree_size}")
    if hashlib.sha256(p.book_checkpoint).hexdigest() != f["checkpoint_sha256"]:
        raise bad("the book checkpoint does not hash to the fact's checkpoint_sha256")
    if p.status_opening is not None:
        if p.status_opening.value is _NO_VALUE:
            raise bad("the status opening: an opening's value: no value")
        try:
            p.status_opening.verify(f["status_commitment"])
        except shelf_facts.Invalid as exc:
            raise bad(f"the status opening: {exc}") from None
        except RecursionError:
            raise bad("the status opening: nested too deeply") from None
    seq = p.inclusion.get("seq", 0)
    if p.inclusion.get("kind") != INCLUSION_KIND or not shelf.covers(seq):
        raise bad("no CLL inclusion proof of an entry the shelf checkpoint covers")
    wire = p.inclusion.get("proof")
    if wire is None:
        wire = {}
    if not isinstance(wire, dict):
        raise bad("the inclusion proof is not an object")

    # Go decodes the proof into its typed MMRInclusion: JSON null or an
    # absent member is the zero value, any other type is refused.
    def member(name: str, zero: Any) -> Any:
        value = wire.get(name)
        return zero if value is None else value

    try:
        v, kind = _uint(member("v", 0), "v"), _str(member("kind", ""), "kind")
        size, leaf_index = _uint(member("size", 0), "size"), _uint(member("leaf_index", 0), "leaf_index")
        lists = [[_str(h, "a digest") for h in _list(wire.get(name), name)]
                 for name in ("witness", "peaks_left", "peaks_right")]
    except InvalidError as exc:
        raise bad(f"the inclusion proof: {exc}") from None
    if any(_hex32(h) is None for values in lists for h in values) or _hex32(shelf.root) is None:
        raise bad("the inclusion proof is not hex digests")
    proof = core.InclusionProof(v, kind, size, leaf_index, *(tuple(values) for values in lists))
    entry = hashlib.sha256(p.fact).digest()
    if not core.verify_inclusion(bytes.fromhex(shelf.root), shelf.tree_size, seq - 1, entry, proof):
        raise bad("the fact's entry is not included under the shelf checkpoint")
    return VerifiedStampProof(f, book, shelf, book.key_id)


def stamp_proof_locked(c: Any, book_id: str, n: int, audience: str) -> StampProof:
    """Go ``Cadence.StampProof``, under the shelf's lock: the proof of a
    book's latest fact at stamp ``n`` (the newest finished stamp when ``n``
    is 0), for an audience. The status opening is included when
    :func:`shelf_facts.openings_for` lets that audience have it, and the link
    opening never is. The journal's facts are proved to nobody."""
    shelf = c._shelf
    try:
        _, with_status = shelf_facts.openings_for(shelf_facts.FACT_KIND_BOOK, audience)
    except shelf_facts.Invalid as exc:
        raise InvalidError(str(exc)) from None
    if n == 0:
        with shelf._read() as db:
            n = db.execute("SELECT COALESCE(MAX(tick), 0) FROM cadence_ticks WHERE checkpoint_size > 0").fetchone()[0]
    tick = c._tick_locked(n)
    if tick.leaves.stamp is None or tick.checkpoint_size == 0:
        raise InvalidError(f"tick {n} is not a finished stamp")
    try:
        anchor = read_checkpoint(tick.checkpoint)
    except InvalidError as exc:
        raise CorruptError(str(exc)) from None
    with shelf._read() as db:
        row = db.execute("SELECT entry_seq, kind, doc, status_opening FROM shelf_entries WHERE book_id = ? "
                         "AND kind IN (?, ?) AND entry_seq <= ? ORDER BY entry_seq DESC LIMIT 1",
                         (book_id, shelf_facts.FACT_KIND_BOOK, shelf_facts.FACT_KIND_JOURNAL, anchor.entries)).fetchone()
    if row is None:
        raise NotFoundError(f"no fact of book {book_id} at stamp {n}")
    seq, kind, doc, so = row
    if kind != shelf_facts.FACT_KIND_BOOK:
        raise InvalidError("the journal's facts are proved to nobody")
    doc = bytes(doc)
    try:
        f = json.loads(doc)
        log_id, checkpoint_sha256 = f["log_id"], f["checkpoint_sha256"]
    except (ValueError, TypeError, KeyError) as exc:
        raise CorruptError(f"stored fact {seq}: {exc}") from None
    opening = None
    if with_status:
        try:
            stored = json.loads(bytes(so), object_pairs_hook=Ordered)
            opening = shelf_facts.Opening(stored["salt"], stored["value"])
        except (ValueError, TypeError, KeyError) as exc:
            raise CorruptError(f"stored status opening {seq}: {exc}") from None
    # The statement is in the leaves of the stamp that cut the checkpoint:
    # the newest one up to n that names it.
    statement = None
    for t in range(n, 0, -1):
        got = c._tick_locked(t)
        leaf = got.leaves.deals.get(log_id)
        if leaf is not None and got.leaves.stamp is not None and leaf.checkpoint_sha256 == checkpoint_sha256 \
                and leaf.statement:
            try:
                statement = base64.urlsafe_b64decode(leaf.statement + "=" * (-len(leaf.statement) % 4))
            except ValueError as exc:
                raise CorruptError(f"tick {t}'s statement of {log_id}: {exc}") from None
            break
    if statement is None:
        raise CorruptError(f"no stored statement for {log_id}'s checkpoint {checkpoint_sha256}")
    inclusion = Ordered(kind=INCLUSION_KIND, seq=seq, proof=c._log.inclusion(seq, anchor))
    return StampProof(doc, statement, inclusion, tick.checkpoint, opening)
