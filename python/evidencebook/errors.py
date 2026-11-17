# SPDX-License-Identifier: Apache-2.0
"""The error classes every book operation raises, as Go's sentinel errors."""
from __future__ import annotations

__all__ = ["BookError", "InvalidError", "NotFoundError", "CorruptError", "NotCoveredError", "ReleasedError",
           "UnsupportedError", "UnavailableError", "OverLimitError", "RefusedError", "DeletedError", "BusyError"]


class BookError(Exception):
    """The base of every error this package raises on purpose."""


class InvalidError(BookError, ValueError):
    """The input is not acceptable (Go ``ErrInvalid``)."""


class NotFoundError(BookError, LookupError):
    """The record, request or checkpoint asked for is not held (Go ``ErrNotFound``)."""


class CorruptError(BookError):
    """Stored state contradicts itself (Go ``ErrCorrupt``)."""


class NotCoveredError(BookError):
    """A position is not committed under the checkpoint named (Go ``ErrNotCovered``)."""


class ReleasedError(BookError):
    """The book was released; it takes no more writes (Go ``ErrReleased``)."""


class UnsupportedError(BookError):
    """The operation or stored format is not supported here (Go ``ErrUnsupported``)."""


class UnavailableError(BookError):
    """A record's originals are not available: its retention state is
    WITHHELD or DELETED (Go ``UnavailableError``)."""


class OverLimitError(BookError):
    """An answer would carry more items than the cost policy allows, or a
    received record is past one of the store's bounds (Go ``ErrOverLimit``)."""


class RefusedError(BookError):
    """A received record or stamp proof whose sender may not push it
    (``Shelf.receive``): nothing of it is stored (Go ``ErrRefused``)."""


class DeletedError(BookError):
    """A received record deleted under the holder's retention: the store
    keeps its tombstone, so it is deleted, not missing (Go ``ErrDeleted``)."""


class BusyError(BookError):
    """A received offer refused because the holder's books kept changing
    while room was made for it (``Shelf.receive``): sending it again later
    may succeed (Go ``ErrBusy``)."""
