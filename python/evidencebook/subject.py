# SPDX-License-Identifier: Apache-2.0
"""The subject convention v0 (evidencebook#116, design note section 4; ruling
D-28: "A new record kind roots the subject; the profile's shared exchange
identifier is canonical; sessions and conversations are members"), byte for
byte with Go's ``subject.go``.

A ``subject_root`` record declares a subject: its kind, its shared id and how
its members are found. Members are the records the root's membership names.
A ``subject_close`` record, when the subject kind has one, ends it: it counts
the members and names the last, and the book refuses a member sealed after
it. The book's admission rules live in :class:`~evidencebook.book.Book`; this
module holds the statements, the membership test, the ``subject_id`` index
and :func:`subjects_of_record`.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional, Sequence

from . import _gostruct as _go
from ._index import SortedMerkleIndex
from .errors import InvalidError
from .record import CITES, RECORD_TYPE_DISCLOSURE, RECORD_TYPE_INDEX_ROOT, RECORD_TYPE_REQUEST_ANSWERED, Header, Record

__all__ = [
    "RECORD_TYPE_SUBJECT_ROOT",
    "RECORD_TYPE_SUBJECT_CLOSE",
    "CLOSES",
    "MEMBERSHIP_LOG",
    "MEMBERSHIP_FIELD_PREFIX",
    "MEMBERSHIP_EXCHANGE_ID",
    "SUBJECT_ID_INDEX_NAME",
    "SubjectRootStatement",
    "SubjectCloseStatement",
    "read_subject_root",
    "read_subject_close",
    "subjects_of_record",
    "new_subject_id_index",
]

RECORD_TYPE_SUBJECT_ROOT = "subject_root"
RECORD_TYPE_SUBJECT_CLOSE = "subject_close"
CLOSES = "closes"

#: Every record of the book after the root is a member (a per-subject book),
#: the holder's own bookkeeping aside. The root is then the book's first record.
MEMBERSHIP_LOG = "log"
#: ``field:<pointer>``: members cite the root and carry the subject id at the
#: RFC 6901 pointer into their header.
MEMBERSHIP_FIELD_PREFIX = "field:"
MEMBERSHIP_EXCHANGE_ID = MEMBERSHIP_FIELD_PREFIX + "/correlation/exchange_id"

SUBJECT_ID_INDEX_NAME = "subject_id"

# Record types of the holder's own bookkeeping Go names (subjectBookkeeping):
# an index root, a disclosure, an answered request, the disclosure policy, a
# lifecycle record, a book's close and a window's open and close. Python
# writes few of them, but classifies a foreign one as Go does.
RECORD_TYPE_POLICY = "disclosure_policy"
RECORD_TYPE_LIFECYCLE = "lifecycle"
RECORD_TYPE_BOOK_CLOSE = "book_close"
RECORD_TYPE_WINDOW_OPEN = "window_open"
RECORD_TYPE_WINDOW_CLOSE = "window_close"

_BOOKKEEPING = frozenset({
    RECORD_TYPE_INDEX_ROOT, RECORD_TYPE_DISCLOSURE, RECORD_TYPE_REQUEST_ANSWERED, RECORD_TYPE_POLICY,
    RECORD_TYPE_LIFECYCLE, RECORD_TYPE_BOOK_CLOSE, RECORD_TYPE_WINDOW_OPEN, RECORD_TYPE_WINDOW_CLOSE,
})

#: Records a book leaves out of its authenticated indexes besides its index
#: roots (Go ``addAuthenticated``): a window's open and close and a book's
#: close are found by position.
UNINDEXED = frozenset({RECORD_TYPE_WINDOW_OPEN, RECORD_TYPE_WINDOW_CLOSE, RECORD_TYPE_BOOK_CLOSE})


def subject_bookkeeping(record_type: str) -> bool:
    return record_type in _BOOKKEEPING


def subject_control(record_type: str) -> bool:
    """The record types that are never a subject's members: a root, a close
    and the holder's bookkeeping."""
    return record_type in (RECORD_TYPE_SUBJECT_ROOT, RECORD_TYPE_SUBJECT_CLOSE) or subject_bookkeeping(record_type)


# -- statements -----------------------------------------------------------------------


@dataclass(frozen=True)
class SubjectRootStatement:
    """The statement of a ``subject_root`` record."""

    subject_kind: str
    subject_id: str
    membership: str

    def value(self) -> dict:
        return {"subject_kind": self.subject_kind, "subject_id": self.subject_id, "membership": self.membership}

    def pointer(self) -> Optional[list[str]]:
        """The membership's JSON pointer split into its reference tokens, or
        ``None`` for log membership or a malformed one."""
        if not self.membership.startswith(MEMBERSHIP_FIELD_PREFIX):
            return None
        raw = self.membership[len(MEMBERSHIP_FIELD_PREFIX):]
        if not raw.startswith("/"):
            return None
        tokens = raw[1:].split("/")
        for i, token in enumerate(tokens):
            # RFC 6901 §3: "~" is escaped as "~0" and "/" as "~1"; any other
            # "~" is malformed. An empty token names the member "".
            for j, c in enumerate(token):
                if c == "~" and (j + 1 == len(token) or token[j + 1] not in "01"):
                    return None
            tokens[i] = token.replace("~1", "/").replace("~0", "~")
        return tokens

    def validate(self) -> None:
        if not self.subject_kind or not self.subject_id:
            raise InvalidError("a subject_root names its subject_kind and subject_id")
        if self.membership == MEMBERSHIP_LOG:
            return
        if self.pointer() is None:
            raise InvalidError(f"subject_root membership {self.membership!r} is neither {MEMBERSHIP_LOG!r} "
                               f"nor {MEMBERSHIP_FIELD_PREFIX!r} followed by a JSON pointer")


@dataclass(frozen=True)
class SubjectCloseStatement:
    """The statement of a ``subject_close`` record: how many members the
    subject has (the root and the close are not members) and the record id
    of the last, ``""`` when it has none."""

    members: int
    last: str

    def value(self) -> dict:
        return {"members": self.members, "last": self.last}


ROOT_SPEC = _go.Struct({"subject_kind": _go.STRING, "subject_id": _go.STRING, "membership": _go.STRING})
CLOSE_SPEC = _go.Struct({"members": _go.UINT64, "last": _go.STRING})


def read_subject_root(statement: Any) -> SubjectRootStatement:
    """Decode a ``subject_root`` statement as Go's ``readSubjectRoot`` does
    (``decodeStrict``, :mod:`evidencebook._gostruct`), then validate it."""
    s = SubjectRootStatement(**_go.decode(statement, ROOT_SPEC, "subject_root statement"))
    s.validate()
    return s


def read_subject_close(statement: Any) -> SubjectCloseStatement:
    """Decode a ``subject_close`` statement as Go's ``decodeStrict`` does."""
    return SubjectCloseStatement(**_go.decode(statement, CLOSE_SPEC, "subject_close statement"))


def _try_root(statement: Any) -> Optional[SubjectRootStatement]:
    try:
        return read_subject_root(statement)
    except InvalidError:
        return None


# -- membership -----------------------------------------------------------------------


def value_at(header_value: Any, tokens: Sequence[str]) -> Optional[str]:
    """The string at ``tokens`` into a header's JSON value, or ``None`` when a
    step is missing or the value is not a string (Go ``headerValueAt``)."""
    value = header_value
    for token in tokens:
        if isinstance(value, dict):
            if token not in value:
                return None
            value = value[token]
        elif isinstance(value, list):
            if not token.isascii() or not token.isdigit() or str(int(token)) != token or int(token) >= len(value):
                return None
            value = value[int(token)]
        else:
            return None
    return value if isinstance(value, str) else None


@dataclass
class SubjectOf:
    """One subject a book holds: its root and, once it has one, its close."""

    root: Record
    statement: SubjectRootStatement
    close: Optional[Record] = None

    def is_member(self, header: Header, header_value: Optional[dict] = None) -> bool:
        """Whether ``header``, of a record after the root, is a member.
        ``header_value`` is its JSON value when the caller holds it as read
        (a verified bundle's header), else ``header.value()``."""
        if subject_control(header.record_type):
            return False
        if self.statement.membership == MEMBERSHIP_LOG:
            return True
        if self.root.record_id not in header.links_to(CITES):
            return False
        found = value_at(header.value() if header_value is None else header_value, self.statement.pointer() or [])
        return found == self.statement.subject_id


@dataclass(frozen=True)
class PeerRecord:
    """One record read from a verified bundle (Go ``PeerRecord``). ``header``
    is ``None`` when the bundle withheld it; ``header_verified`` is true only
    when the disclosed header is the one the record's capsule commits.
    ``header_value`` is the verified header's JSON value as disclosed."""

    record_id: str
    seq: int
    header: Optional[Header] = None
    header_verified: bool = False
    capsule_ok: bool = False
    header_value: Optional[dict] = None

    def value(self) -> Optional[dict]:
        if self.header is None:
            return None
        return self.header_value if self.header_value is not None else self.header.value()


def subjects_of_record(records: Sequence[PeerRecord], record_id: str) -> list[str]:
    """The subject ids of which ``record_id`` of a verified bundle is a
    member, read from its verified headers alone (Go ``SubjectsOfRecord``).
    The root is a record of the member's own book with a lower seq (seq 1 for
    a log root). Sorted, without duplicates; empty for a record that is no
    verified member. Two ids mean the book that sealed it broke the
    one-subject rule; the caller decides."""
    member: Optional[PeerRecord] = None
    for r in records:
        if r.record_id == record_id and r.header_verified and r.header is not None:
            member = r
    if member is None:
        return []
    out: list[str] = []
    for r in records:
        h = r.header
        if not r.header_verified or h is None or h.record_type != RECORD_TYPE_SUBJECT_ROOT or r.record_id == record_id:
            continue
        statement = _try_root(h.statement)
        if statement is None:
            continue
        s = SubjectOf(Record(r.record_id, h.seq, h), statement)
        if member.header.book_id != h.book_id or member.header.seq <= h.seq or \
                (statement.membership == MEMBERSHIP_LOG and h.seq != 1):
            continue
        if s.is_member(member.header, member.value()) and statement.subject_id not in out:
            out.append(statement.subject_id)
    return sorted(out, key=lambda s: s.encode("utf-8"))


def new_subject_id_index() -> SortedMerkleIndex:
    """The ``subject_id`` authenticated index (Go ``NewSubjectIDIndex``):
    every subject's root, members and close under the subject's declared id.
    It learns the roots from the records it is given in log order. Its root
    is committed only once it holds a leaf (lazy), so a book that declares no
    subject commits exactly the index roots it did before the index existed."""
    subjects: list[SubjectOf] = []

    def key_of(r: Record) -> Optional[str]:
        h = r.header
        if h.record_type == RECORD_TYPE_SUBJECT_ROOT:
            statement = _try_root(h.statement)
            if statement is None:
                return None
            subjects.append(SubjectOf(r, statement))
            return statement.subject_id
        if h.record_type == RECORD_TYPE_SUBJECT_CLOSE:
            for s in subjects:
                if s.root.record_id in h.links_to(CLOSES):
                    return s.statement.subject_id
            return None
        for s in subjects:
            if s.is_member(h):
                return s.statement.subject_id
        return None

    return SortedMerkleIndex(SUBJECT_ID_INDEX_NAME, key_of, lazy=True)
