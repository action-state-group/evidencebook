# SPDX-License-Identifier: Apache-2.0
"""Go's ``time.Parse(time.RFC3339, s)``, its result and its error text, for
times the x-deal-v1 authorization reads from records that were never
checked (Go ``xdealv1.parseAt``).

Go parses this layout with its general parser when the strict RFC 3339 path
fails, so it also takes a one-digit hour, a comma before a fraction of any
length, and a zone offset of up to 24 hours and 60 minutes; it refuses
anything else with the error :func:`parse` raises. CI holds this to Go with
``test_go_time_parse`` over cases Go answered (``testdata/deal-v1/time``).
"""
from __future__ import annotations

_LAYOUT = "2006-01-02T15:04:05Z07:00"

#: Go's zero Time, 0001-01-01T00:00:00Z, as Unix seconds.
ZERO = -62135596800


class GoTimeError(ValueError):
    """A value Go's ``time.Parse`` refuses; ``str()`` is Go's error text."""


def _quote(s: bytes) -> str:
    """Go's ``time.quote``: ``"`` and ``\\`` escaped, each byte of a
    non-ASCII or control character written as ``\\x`` hex."""
    out = ['"']
    for b in s:
        if b >= 0x80 or b < 0x20:
            out.append(f"\\x{b:02x}")
        else:
            if b in b'"\\':
                out.append("\\")
            out.append(chr(b))
    out.append('"')
    return "".join(out)


def _cannot(value: bytes, elem_value: bytes, elem_layout: str) -> GoTimeError:
    return GoTimeError(f"parsing time {_quote(value)} as {_quote(_LAYOUT.encode())}: cannot parse "
                       f"{_quote(elem_value)} as {_quote(elem_layout.encode())}")


def _range(value: bytes, what: str) -> GoTimeError:
    return GoTimeError(f"parsing time {_quote(value)}: {what} out of range")


def _digit(s: bytes, i: int) -> bool:
    return len(s) > i and 0x30 <= s[i] <= 0x39


def _getnum(s: bytes, fixed: bool) -> tuple[int, bytes] | None:
    if not _digit(s, 0):
        return None
    if not _digit(s, 1):
        if fixed:
            return None
        return s[0] - 0x30, s[1:]
    return int(s[:2]), s[2:]


def _days_from_civil(y: int, m: int, d: int) -> int:
    """Days from 1970-01-01 to y-m-d in the proleptic Gregorian calendar."""
    y -= m <= 2
    era = (y if y >= 0 else y - 399) // 400
    yoe = y - era * 400
    doy = (153 * (m + (-3 if m > 2 else 9)) + 2) // 5 + d - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - 719468


def _days_in(month: int, year: int) -> int:
    if month == 2:
        return 29 if year % 4 == 0 and (year % 100 != 0 or year % 400 == 0) else 28
    return 30 if month in (4, 6, 9, 11) else 31


def parse(value: str) -> tuple[int, int]:
    """``value`` as Go's ``time.Parse(time.RFC3339, value)`` reads it: the
    instant as (Unix seconds, nanoseconds). Raises :class:`GoTimeError`
    with Go's text where Go refuses it."""
    value_bytes = value.encode("utf-8", "surrogatepass")
    v = value = value_bytes  # Go reads the bytes
    # 2006: four characters, the first a digit, all digits.
    if len(v) < 4 or not _digit(v, 0):
        raise _cannot(value, v, "2006")
    if not all(_digit(v, i) for i in range(4)):
        raise _cannot(value, v, "2006")
    year, v = int(v[:4]), v[4:]
    fields: list[int] = []
    for prefix, std, fixed, name in (("-", "01", True, "month"), ("-", "02", True, None), ("T", "15", False, "hour"),
                                     (":", "04", True, "minute"), (":", "05", True, "second")):
        if not v.startswith(prefix.encode()):
            raise _cannot(value, v, prefix)
        v = v[len(prefix):]
        got = _getnum(v, fixed)
        if got is None:
            raise _cannot(value, v, std)
        n, v = got
        if name == "month" and not 1 <= n <= 12:
            raise _range(value, "month")
        if name == "hour" and n >= 24:
            raise _range(value, "hour")
        if name == "minute" and n >= 60:
            raise _range(value, "minute")
        if name == "second" and n >= 60:
            raise _range(value, "second")
        fields.append(n)
    month, day, hour, minute, sec = fields
    nanos = 0
    if len(v) >= 2 and v[0] in b".," and _digit(v, 1):
        n = 2
        while n < len(v) and _digit(v, n):
            n += 1
        digits = v[1:min(n, 10)]
        nanos = int(digits) * 10 ** (9 - len(digits))
        v = v[n:]
    if v.startswith(b"Z"):
        offset, v = 0, v[1:]
    else:
        if len(v) < 6 or v[3] != 0x3A:
            raise _cannot(value, v, "Z07:00")
        sign, hh, mm, rest = v[0:1], v[1:3], v[4:6], v[6:]
        # Go reads the minutes only when the hour reads, and checks the
        # ranges before it reports a part that did not read.
        h = _getnum(hh, True)
        m = _getnum(mm, True) if h is not None else None
        hr, mi = h[0] if h else 0, m[0] if m else 0
        # Go sets each range error in turn, so the minute's overrides the hour's.
        if mi > 60:
            raise _range(value, "time zone offset minute")
        if hr > 24:
            raise _range(value, "time zone offset hour")
        if h is None or m is None or sign not in (b"+", b"-"):
            raise _cannot(value, v, "Z07:00")
        offset = (hr * 60 + mi) * 60 * (1 if sign == b"+" else -1)
        v = rest
    if v:
        raise GoTimeError(f"parsing time {_quote(value)}: extra text: {_quote(v)}")
    if not 1 <= day <= _days_in(month, year):
        raise _range(value, "day")
    seconds = _days_from_civil(year, month, day) * 86400 + hour * 3600 + minute * 60 + sec - offset
    return seconds, nanos
