# SPDX-License-Identifier: Apache-2.0
"""The error classes every book operation raises, as Go's sentinel errors."""
from __future__ import annotations

__all__ = ["BookError", "InvalidError", "NotFoundError", "CorruptError", "NotCoveredError", "ReleasedError",
           "UnsupportedError"]


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
