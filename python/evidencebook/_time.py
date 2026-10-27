# SPDX-License-Identifier: Apache-2.0
"""Instants as Go's evidencebook writes them.

A book keeps instants as aware UTC ``datetime`` values (microsecond
precision, as Go truncates ``committed_at``). Received times may carry up to
nine fraction digits, so comparisons with them run on integer nanoseconds.
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

from agent_action_capsule.evidence_request import parse_utc

_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def utc(moment: datetime) -> datetime:
    if moment.tzinfo is None:
        raise ValueError("a book instant must be timezone-aware")
    return moment.astimezone(timezone.utc)


def nanos(moment: datetime) -> int:
    """Nanoseconds since the epoch."""
    delta = utc(moment) - _EPOCH
    return (delta.days * 86_400 + delta.seconds) * 1_000_000_000 + delta.microseconds * 1_000


def from_nanos(ns: int) -> datetime:
    return _EPOCH + timedelta(microseconds=ns // 1_000)


def parse_ns(text: object) -> int | None:
    """An RFC 3339 UTC time (``Z``, up to nine fraction digits) as nanoseconds."""
    parsed = parse_utc(text)
    if parsed is None:
        return None
    seconds, fraction = parsed
    return seconds * 1_000_000_000 + fraction


def rfc3339_nano(moment: datetime) -> str:
    """Go's ``time.RFC3339Nano`` in UTC: the fraction with trailing zeros
    dropped, and none at all when it is zero."""
    moment = utc(moment)
    text = moment.strftime("%Y-%m-%dT%H:%M:%S")
    if moment.microsecond:
        text += "." + f"{moment.microsecond:06d}".rstrip("0")
    return text + "Z"


def capsule_timestamp(moment: datetime) -> str:
    """capsule-emit's timestamp form: whole seconds bare, otherwise six fraction digits."""
    moment = utc(moment)
    if moment.microsecond:
        return moment.strftime("%Y-%m-%dT%H:%M:%S.%fZ")
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def profile_time(moment: datetime) -> str:
    """The profile's seconds form (profile §5.2): the fraction dropped."""
    return utc(moment).strftime("%Y-%m-%dT%H:%M:%SZ")


def truncate_ms(moment: datetime) -> datetime:
    moment = utc(moment)
    return moment.replace(microsecond=moment.microsecond - moment.microsecond % 1000)


def truncate_s(moment: datetime) -> datetime:
    return utc(moment).replace(microsecond=0)
