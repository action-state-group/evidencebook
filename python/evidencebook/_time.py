# SPDX-License-Identifier: Apache-2.0
"""Instants as Go's evidencebook writes them.

A book keeps instants as aware UTC ``datetime`` values (microsecond
precision, as Go truncates ``committed_at``). Received times may carry up to
nine fraction digits, so comparisons with them run on integer nanoseconds.
"""
from __future__ import annotations

import re
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


# RFC 3339 §5.6 date-time; "T" and "Z" may be lower case (§5.6 NOTE).
_RFC3339 = re.compile(r"([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?"
                      r"([Zz]|[+-][0-9]{2}:[0-9]{2})\Z")
_SECOND = 1_000_000_000
_DAY = 86_400 * _SECOND


def days_from_civil(year: int, month: int, day: int) -> int:
    """Days from 1970-01-01 in the proleptic Gregorian calendar, any year
    (H. Hinnant's algorithm), so year 0000 has an instant."""
    year -= month <= 2
    era = year // 400
    yoe = year - era * 400
    doy = (153 * (month + (-3 if month > 2 else 9)) + 2) // 5 + day - 1
    return era * 146097 + yoe * 365 + yoe // 4 - yoe // 100 + doy - 719468


def civil_from_days(days: int) -> tuple[int, int, int]:
    days += 719468
    era = days // 146097
    doe = days - era * 146097
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    day = doy - (153 * mp + 2) // 5 + 1
    month = mp + (3 if mp < 10 else -9)
    return yoe + era * 400 + (month <= 2), month, day


def _days_in_month(year: int, month: int) -> int:
    if month == 2:
        return 29 if year % 4 == 0 and (year % 100 != 0 or year % 400 == 0) else 28
    return 30 if month in (4, 6, 9, 11) else 31


# The UTC days that ended in a leap second (IERS Bulletin C; none has been
# announced since 2016-12-31).
LEAP_SECOND_DAYS = frozenset(days_from_civil(*d) for d in (
    (1972, 6, 30), (1972, 12, 31), (1973, 12, 31), (1974, 12, 31), (1975, 12, 31), (1976, 12, 31), (1977, 12, 31),
    (1978, 12, 31), (1979, 12, 31), (1981, 6, 30), (1982, 6, 30), (1983, 6, 30), (1985, 6, 30), (1987, 12, 31),
    (1989, 12, 31), (1990, 12, 31), (1992, 6, 30), (1993, 6, 30), (1994, 6, 30), (1995, 12, 31), (1997, 6, 30),
    (1998, 12, 31), (2005, 12, 31), (2008, 12, 31), (2012, 6, 30), (2015, 6, 30), (2016, 12, 31)))


def parse_rfc3339_ns(text: object) -> int | None:
    """An RFC 3339 date-time (§5.6 grammar, §5.7 ranges) as nanoseconds since
    the epoch, any year from 0000: lower case "t" and "z" allowed, "."
    fractions of any length (truncated to the nanosecond), offsets up to
    23:59. A second of 60 is a leap second, valid only where one occurred:
    23:59:60 UTC at the end of a day in LEAP_SECOND_DAYS (§5.7, §5.8). Its
    instant is the end of that day, the start of the next."""
    m = _RFC3339.match(text) if isinstance(text, str) else None
    if m is None:
        return None
    year, month, day, hour, minute, second = (int(m.group(i)) for i in range(1, 7))
    if not (1 <= month <= 12 and 1 <= day <= _days_in_month(year, month) and hour <= 23 and minute <= 59
            and second <= 60):
        return None
    zone = m.group(8)
    offset = 0
    if zone not in ("Z", "z"):
        hours, minutes = int(zone[1:3]), int(zone[4:6])
        if hours > 23 or minutes > 59:
            return None
        offset = (-1 if zone[0] == "-" else 1) * (hours * 3600 + minutes * 60)
    local = days_from_civil(year, month, day) * _DAY + (hour * 3600 + minute * 60 + second) * _SECOND
    instant = local - offset * _SECOND
    if second == 60:
        utc_day, utc_time = divmod(instant - _SECOND, _DAY)
        if utc_day not in LEAP_SECOND_DAYS or utc_time != _DAY - _SECOND:
            return None
    return instant + int((m.group(7) or "0")[:9].ljust(9, "0"))


def profile_time_ns(ns: int) -> str:
    """The profile's seconds form of an instant held as nanoseconds."""
    days, rest = divmod(ns, _DAY)
    year, month, day = civil_from_days(days)
    seconds = rest // _SECOND
    return f"{year:04d}-{month:02d}-{day:02d}T{seconds // 3600:02d}:{seconds // 60 % 60:02d}:{seconds % 60:02d}Z"
