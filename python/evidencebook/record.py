# SPDX-License-Identifier: Apache-2.0
"""Records: the header a book commits for each entry, and its vocabulary.

The header's JCS is what a record capsule commits (its
``agent_input_digest``), so every member here is fixed at commit. Member
names and omission rules are those of Go's ``evidencebook.Header``.
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from .errors import InvalidError

__all__ = [
    "HEADER_VERSION",
    "EPISTEMIC_TYPES",
    "LINK_TYPES",
    "OBSERVED_EVENT",
    "PRODUCER_CLAIM",
    "DERIVED_METRIC",
    "ADJUDICATION",
    "CITES",
    "AVAILABLE",
    "RECORD_TYPE_REQUEST",
    "RECORD_TYPE_REQUEST_ANSWERED",
    "RECORD_TYPE_RESPONSE",
    "RECORD_TYPE_ABSENCE",
    "RECORD_TYPE_INDEX_ROOT",
    "RECORD_TYPE_DISCLOSURE",
    "RECORD_TYPE_RETENTION_COMMITMENT",
    "Link",
    "Correlation",
    "Entry",
    "Header",
    "Record",
]

HEADER_VERSION = 1

OBSERVED_EVENT = "observed_event"
PRODUCER_CLAIM = "producer_claim"
DERIVED_METRIC = "derived_metric"
ADJUDICATION = "adjudication"
#: The closed epistemic_type set, in registry order (Evidence Layer §4.1).
EPISTEMIC_TYPES = (
    OBSERVED_EVENT,
    "system_of_record_fact",
    PRODUCER_CLAIM,
    "human_report",
    "semantic_judgment",
    DERIVED_METRIC,
    ADJUDICATION,
    "obligation_reference",
)

CITES = "cites"
#: The closed typed-link vocabulary, in registry order (Evidence Layer §5).
LINK_TYPES = (CITES, "adjudicates", "supersedes", "acknowledges", "rebuts", "closes")

AVAILABLE = "AVAILABLE"

# Record types this package writes itself; record_type is open vocabulary.
RECORD_TYPE_REQUEST = "evidence_request"
RECORD_TYPE_REQUEST_ANSWERED = "evidence_request_answered"
RECORD_TYPE_RESPONSE = "evidence_response"
RECORD_TYPE_ABSENCE = "recorded_absence"
RECORD_TYPE_INDEX_ROOT = "index_root"
RECORD_TYPE_DISCLOSURE = "disclosure"
RECORD_TYPE_RETENTION_COMMITMENT = "retention_commitment"

_HEX64 = re.compile(r"[0-9a-f]{64}\Z")


@dataclass(frozen=True)
class Link:
    """A typed, directed reference to another record by its record id."""

    type: str
    target: str

    def value(self) -> dict:
        return {"type": self.type, "target": self.target}


@dataclass(frozen=True)
class Correlation:
    """The exchange correlation keys pairing two halves of one interaction."""

    exchange_id: str = ""
    request_digest: str = ""
    twin_bracket_id: str = ""

    def empty(self) -> bool:
        return not (self.exchange_id or self.request_digest or self.twin_bracket_id)

    def value(self) -> dict:
        out = {}
        for name in ("exchange_id", "request_digest", "twin_bracket_id"):
            if getattr(self, name):
                out[name] = getattr(self, name)
        return out


@dataclass(frozen=True)
class Entry:
    """What a caller asks the book to record. ``statement`` is a JSON value;
    ``payloads`` are octets the record commits by digest and the store keeps."""

    record_type: str
    epistemic_type: str
    event_time_claim: datetime | None = None
    payloads: tuple[bytes, ...] = ()
    links: tuple[Link, ...] = ()
    subject_ref: str = ""
    principal_ref: str = ""
    counterparty_ref: str = ""
    correlation: Correlation = field(default_factory=Correlation)
    statement: Any = None


@dataclass(frozen=True)
class Header:
    """The durable, privacy-minimized record header (Evidence Layer §4)."""

    book_id: str
    seq: int
    record_type: str
    epistemic_type: str
    committed_at: str
    links: tuple[Link, ...] = ()
    event_time_claim: str = ""
    payload_commitments: tuple[str, ...] = ()
    retention_state: str = ""
    subject_ref: str = ""
    principal_ref: str = ""
    counterparty_ref: str = ""
    correlation: Correlation | None = None
    statement: Any = None
    v: int = HEADER_VERSION
    #: Salted per-field commitments, as ``(name, commitment)`` pairs in name
    #: order, under ``commit_alg``. This package reads and keeps them (a
    #: shelf holds books Go wrote with them) but seals none.
    field_commitments: tuple[tuple[str, str], ...] = ()
    commit_alg: str = ""

    def value(self) -> dict:
        """The header as a JSON value, members omitted as Go omits them."""
        out: dict[str, Any] = {
            "v": self.v,
            "book_id": self.book_id,
            "seq": self.seq,
            "record_type": self.record_type,
            "epistemic_type": self.epistemic_type,
            "committed_at": self.committed_at,
            "links": [link.value() for link in self.links],
        }
        for name in ("event_time_claim", "retention_state", "subject_ref", "principal_ref", "counterparty_ref"):
            if getattr(self, name):
                out[name] = getattr(self, name)
        if self.payload_commitments:
            out["payload_commitments"] = list(self.payload_commitments)
        if self.field_commitments:
            out["field_commitments"] = dict(self.field_commitments)
        if self.commit_alg:
            out["commit_alg"] = self.commit_alg
        if self.correlation is not None:
            out["correlation"] = self.correlation.value()
        if self.statement is not None:
            out["statement"] = self.statement
        return out

    @classmethod
    def from_value(cls, value: dict) -> Header:
        correlation = value.get("correlation")
        return cls(
            v=value.get("v", 0),
            book_id=value.get("book_id", ""),
            seq=value.get("seq", 0),
            record_type=value.get("record_type", ""),
            epistemic_type=value.get("epistemic_type", ""),
            committed_at=value.get("committed_at", ""),
            links=tuple(Link(link["type"], link["target"]) for link in value.get("links") or ()),
            event_time_claim=value.get("event_time_claim", ""),
            payload_commitments=tuple(value.get("payload_commitments") or ()),
            retention_state=value.get("retention_state", ""),
            subject_ref=value.get("subject_ref", ""),
            principal_ref=value.get("principal_ref", ""),
            counterparty_ref=value.get("counterparty_ref", ""),
            correlation=Correlation(**correlation) if correlation is not None else None,
            statement=value.get("statement"),
            field_commitments=tuple(sorted((value.get("field_commitments") or {}).items())),
            commit_alg=value.get("commit_alg", ""),
        )

    def links_to(self, link_type: str) -> list[str]:
        """The targets of every link of type ``link_type``."""
        return [link.target for link in self.links if link.type == link_type]

    def validate(self) -> None:
        if self.v != HEADER_VERSION:
            raise InvalidError(f"header version {self.v}")
        if not (self.book_id and self.seq and self.record_type and self.committed_at):
            raise InvalidError("book_id, seq, record_type and committed_at are required")
        if self.epistemic_type not in EPISTEMIC_TYPES:
            raise InvalidError(f"epistemic_type {self.epistemic_type!r} is not registered")
        if self.payload_commitments and self.retention_state != AVAILABLE:
            raise InvalidError("retention_state is required with payload_commitments")
        for digest in self.payload_commitments:
            if not _HEX64.match(digest):
                raise InvalidError(f"payload commitment {digest!r} is not a lowercase SHA-256 digest")
        for link in self.links:
            if link.type not in LINK_TYPES:
                raise InvalidError(f"link type {link.type!r} is not registered")
            if not isinstance(link.target, str) or not _HEX64.match(link.target):
                raise InvalidError(f"link target {link.target!r} is not a record id")
            if link.type == "adjudicates" and self.epistemic_type != ADJUDICATION:
                raise InvalidError("a record carrying an adjudicates link must have epistemic_type adjudication")


@dataclass(frozen=True)
class Record:
    """One committed entry: its identity (the record capsule's id, the value
    the log commits), its position, and its header."""

    record_id: str
    seq: int
    header: Header
