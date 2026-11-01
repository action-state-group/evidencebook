# SPDX-License-Identifier: Apache-2.0
"""Keys, fingerprints and commitments (PROFILE.md §4), as Go's
``dealprofile/keys.go`` makes them.

Counterparty identifiers never enter a sealed record raw. A record carries a
per-deal HMAC fingerprint of each normalized identifier, and free text that
may contain identifiers is carried as a salted commitment. Raw values, the
per-deal key and the commitment nonces stay in the producer's local store.

Go's string functions are matched rune for rune: ``strings.ToLower`` is the
simple (one-to-one) lowercase mapping, full case folding per CaseFolding.txt (``cases.Fold``, with the Cherokee small forms mapped to the capitals),
``unicode.IsPunct`` the Unicode punctuation categories, ``strings.TrimSpace``
and ``strings.Fields`` the White_Space property (not Python's ``str.strip``,
which also strips U+001C to U+001F), and RE2's ``\\d`` and ``\\s`` are ASCII.

Domains are checked against Go's ``idna.Lookup`` in a differential fuzz (about
180,000 mixed-script, multi-label domains, and every code point alone in a
label): the differences left are characters newer than the running Python's
tables, and a ZERO WIDTH NON-JOINER between an Arabic letter and a digit, which
x/net accepts and this refuses (RFC 5892 A.1).
"""
from __future__ import annotations

import bisect
import hashlib
import hmac
import re
import unicodedata

import idna
from agent_action_capsule.canonical import json_digest

from .publicsuffix import effective_tld_plus_one

__all__ = ["NotNormalizable", "kinds", "normalize", "deal_key", "profile_key", "fingerprint", "commit_text"]


class NotNormalizable(ValueError):
    """An identifier that cannot be normalized (Go ``ErrNotNormalizable``).
    Such a value is not fingerprinted and not recorded."""

    def __init__(self) -> None:
        super().__init__("identifier cannot be normalized")


_KINDS = ("payee", "name", "domain", "phone", "email", "relay_address", "profile_id")


def kinds() -> list[str]:
    """The counterparty identifier kinds a record may carry, a closed set
    (PROFILE.md §2)."""
    return list(_KINDS)


_LEGAL_FORMS = frozenset({"llc", "inc", "ltd", "corp", "co", "company", "gmbh", "llp", "plc"})

# Go's RE2 \s is [\t\n\f\r ] and \d is [0-9]; Python's \Z is Go's $.
_PHONE_EXTENSION = re.compile(r"[\t\n\f\r ]*(?:ext\.?|x|#)[\t\n\f\r ]*[0-9]+[\t\n\f\r ]*\Z",
                                   re.IGNORECASE | re.ASCII)
_NON_DIGIT = re.compile(r"[^0-9]")
_PAYEE_IS_PHONE = re.compile(r"[\t\n\f\r ]*\+?[0-9\t\n\f\r ().-]{7,}[\t\n\f\r ]*", re.ASCII)
_URL_SCHEME = re.compile(r"^[a-z][a-z0-9+.-]*://", re.ASCII)
_DOMAIN_PORT = re.compile(r":[0-9]+\Z", re.ASCII)

# unicode.IsSpace: the White_Space property.
_SPACE = frozenset("\t\n\v\f\r \x85\xa0            "
                   "    　")


def _trim(s: str) -> str:
    """Go ``strings.TrimSpace``."""
    i, j = 0, len(s)
    while i < j and s[i] in _SPACE:
        i += 1
    while j > i and s[j - 1] in _SPACE:
        j -= 1
    return s[i:j]


def _fields(s: str) -> list[str]:
    """Go ``strings.Fields``."""
    out: list[str] = []
    cur: list[str] = []
    for c in s:
        if c in _SPACE:
            if cur:
                out.append("".join(cur))
                cur = []
        else:
            cur.append(c)
    if cur:
        out.append("".join(cur))
    return out


def go_lower(s: str) -> str:
    """Go ``strings.ToLower``: the simple lowercase mapping, rune by rune
    (Python's ``str.lower`` is the full mapping, and context-sensitive for
    a final sigma)."""
    out = []
    for c in s:
        low = c.lower()
        out.append(low if len(low) == 1 else ("i" if c == "İ" else low[0]))
    return "".join(out)


def fold(s: str) -> str:
    """Full case folding per Unicode's CaseFolding.txt, which ``str.casefold`` follows. Go's
    ``cases.Fold`` swaps Cherokee case instead, so ``dealprofile`` maps the small Cherokee forms to
    the capitals after folding and agrees with this."""
    return s.casefold()


def _nfc(s: str) -> str:
    return unicodedata.normalize("NFC", s)


def _norm_phone(v: str) -> str:
    v = _trim(unicodedata.normalize("NFKC", v))
    m = _PHONE_EXTENSION.search(v)
    if m:
        v = v[:m.start()]
    plus = v.startswith("+")
    digits = _NON_DIGIT.sub("", v)
    if not plus:
        if len(digits) == 10:  # v0 default region: NANP
            digits = "1" + digits
        elif not (len(digits) == 11 and digits.startswith("1")):
            raise NotNormalizable()
    if len(digits) < 8 or len(digits) > 15:
        raise NotNormalizable()
    return "+" + digits


def _norm_email(v: str) -> str:
    v = go_lower(_nfc(_trim(v)))
    if v.count("@") != 1 or v.startswith("@") or v.endswith("@"):
        raise NotNormalizable()
    return v


def _validate_label(label: str) -> None:
    """x/net ``idna.Lookup``'s ``validateLabel``: UTS 46 validity (the
    characters are valid or deviations by construction once mapped), not the
    IDNA 2008 character classes ``idna.check_label`` also applies. Hyphens
    are read by byte, as Go indexes the UTF-8 string."""
    if not label:
        return
    b = label.encode("utf-8")
    if len(b) > 4 and b[2:4] == b"--":
        raise idna.IDNAError("V2")
    if b[:1] == b"-" or b[-1:] == b"-":
        raise idna.IDNAError("V3")
    if unicodedata.category(label[0]).startswith("M"):
        raise idna.IDNAError("V5")
    for pos, c in enumerate(label):
        if c in "\u200c\u200d" and not idna.core.valid_contextj(label, pos):
            raise idna.IDNAError("C")


def _punycode_decode(encoded: str) -> str:
    """x/net ``idna``'s punycode ``decode``: RFC 3492, except that the part
    before the last hyphen is copied rune for rune, whatever the runes are."""
    if not encoded:
        return ""
    pos = 1 + encoded.rfind("-")
    if pos == 1:
        raise idna.IDNAError("punycode")
    if pos == len(encoded):
        return encoded[:-1]
    out = list(encoded[:pos - 1]) if pos else []
    i, n, bias = 0, 128, 72
    limit = 2 ** 31 - 1
    while pos < len(encoded):
        old_i, w = i, 1
        k = 36
        while True:
            if pos == len(encoded):
                raise idna.IDNAError("punycode")
            c = encoded[pos]
            if "a" <= c <= "z":
                digit = ord(c) - 97
            elif "A" <= c <= "Z":
                digit = ord(c) - 65
            elif "0" <= c <= "9":
                digit = ord(c) - 22
            else:
                raise idna.IDNAError("punycode")
            pos += 1
            i += digit * w
            if i > limit:
                raise idna.IDNAError("punycode")
            t = 1 if k <= bias else 26 if k >= bias + 26 else k - bias
            if digit < t:
                break
            w *= 36 - t
            if w > limit:
                raise idna.IDNAError("punycode")
            k += 36
        if len(out) >= 1024:
            raise idna.IDNAError("punycode")
        x = len(out) + 1
        delta = i - old_i
        delta = delta // 700 if old_i == 0 else delta // 2
        delta += delta // x
        kk = 0
        while delta > (35 * 26) // 2:
            delta //= 35
            kk += 36
        bias = kk + 36 * delta // (delta + 38)
        n += i // x
        i %= x
        if n > 0x10FFFF or 0xD800 <= n <= 0xDFFF:
            raise idna.IDNAError("punycode")
        out.insert(i, chr(n))
        i += 1
    return "".join(out)


def _from_punycode(label: str) -> str:
    """A label starting ``xn--``, decoded and validated (``validateFromPunycode``
    then ``validateLabel``)."""
    u = _punycode_decode(label[4:])
    if not u or u.isascii():
        raise idna.IDNAError("V6")
    if not unicodedata.is_normalized("NFC", u):
        raise idna.IDNAError("V1")
    for c in u:
        if _uts46_status(c) not in "VD":
            raise idna.IDNAError("V6")
    _validate_label(u)
    return u


def _uts46_status(c: str) -> str:
    from idna.uts46data import uts46_starts, uts46_statuses
    cp = ord(c)
    return chr(uts46_statuses[cp if cp < 256 else bisect.bisect_right(uts46_starts, cp) - 1])


def _alabel(label: str) -> str:
    """One label's ASCII form, validated, with no length limit (Go's
    ``idna.Lookup`` does not verify DNS length)."""
    if label.startswith("xn--"):
        # x/net keeps the decoded label and encodes it again, so the public
        # suffix list is matched against the standard spelling.
        return "xn--" + _from_punycode(label).encode("punycode").decode("ascii")
    if label.isascii():
        _validate_label(label)
        return label
    _validate_label(label)
    return "xn--" + label.encode("punycode").decode("ascii")


_BIDI_TRIGGERS = frozenset({"R", "AL", "AN"})


def _check_bidi(labels: list[str]) -> None:
    """x/net's ``process``: when any label of the domain (decoded, if it is
    ``xn--``) holds a right-to-left character, RFC 5893's Bidi Rule applies to
    *every* label, a left-to-right one included. ``idna.core.check_bidi``
    alone applies it per label."""
    if any(unicodedata.bidirectional(c) in _BIDI_TRIGGERS for label in labels for c in label):
        for label in labels:
            if label:
                idna.core.check_bidi(label, check_ltr=True)


def _to_ascii(v: str) -> str:
    mapped = idna.core.uts46_remap(v, std3_rules=True, transitional=False)
    labels = mapped.split(".")
    out = [_alabel(label) if label else "" for label in labels]
    _check_bidi([_from_punycode(label) if label.startswith("xn--") else label for label in out])
    return ".".join(out)


def _to_unicode(v: str) -> str:
    out = []
    for label in v.split("."):
        if label.startswith("xn--"):
            out.append(_from_punycode(label))
        else:
            _validate_label(label)
            out.append(label)
    _check_bidi(out)
    return ".".join(out)


def _norm_domain(v: str) -> str:
    v = go_lower(_nfc(_trim(v)))
    v = _URL_SCHEME.sub("", v)
    for i, c in enumerate(v):
        if c in "/?#":
            v = v[:i]
            break
    i = v.rfind("@")
    if i >= 0:
        v = v[i + 1:]
    v = _DOMAIN_PORT.sub("", v).rstrip(".")
    try:
        ascii_ = _to_ascii(v)
    except (idna.IDNAError, UnicodeError, ValueError):
        raise NotNormalizable() from None
    labels = ascii_.split(".")
    if len(labels) < 2 or any(not label for label in labels):
        raise NotNormalizable()
    registrable = effective_tld_plus_one(ascii_)
    if registrable is None:
        raise NotNormalizable()
    try:
        return _to_unicode(registrable)
    except (idna.IDNAError, UnicodeError, ValueError):
        raise NotNormalizable() from None


def _norm_name(v: str) -> str:
    v = fold(_nfc(v))
    v = "".join(" " if unicodedata.category(c).startswith("P") else c for c in v)
    toks = _fields(v)
    if toks and toks[0] == "the":
        toks = toks[1:]
    while toks and toks[-1] in _LEGAL_FORMS:
        toks = toks[:-1]
    if not toks:
        raise NotNormalizable()
    return " ".join(toks)


def normalize(kind: str, v: str) -> str:
    """One raw identifier of ``kind`` in the form its fingerprint is taken
    over (PROFILE.md §4); raises :class:`NotNormalizable`."""
    if kind == "phone":
        return _norm_phone(v)
    if kind == "email":
        return _norm_email(v)
    if kind == "domain":
        return _norm_domain(v)
    if kind == "name":
        return _norm_name(v)
    if kind == "payee":
        if "@" in v:
            return _norm_email(v)
        if _PAYEE_IS_PHONE.fullmatch(v):
            return _norm_phone(v)
        return _norm_name(v)
    if kind == "relay_address":
        return go_lower(_nfc(_trim(v)))
    if kind == "profile_id":
        out = _nfc(_trim(v))
        if not out:
            raise NotNormalizable()
        return out
    raise NotNormalizable()


def _hmac_sha256(key: bytes, *parts: str) -> bytes:
    m = hmac.new(key, digestmod=hashlib.sha256)
    for p in parts:
        m.update(p.encode("utf-8"))
    return m.digest()


def deal_key(store_secret: bytes, deal_id: str) -> bytes:
    """A deal's key, derived from the store secret and the deal id. A
    producer stores it with the deal at open, so a later rotation of the
    store secret cannot change a deal in progress."""
    return _hmac_sha256(store_secret, "x-deal-v0/deal-key\x00", deal_id)


def profile_key(store_secret: bytes) -> bytes:
    """The key of a profile's own fingerprints (``PROFILE_FP_ALG``): one
    merchant has one such fingerprint across the profile's deals. Like the
    store secret, it never leaves the device."""
    return _hmac_sha256(store_secret, "x-deal-v0/profile-key\x00")


def fingerprint(key: bytes, kind: str, raw: str) -> str:
    """The fingerprint of one raw identifier of ``kind`` under ``key`` (a
    deal key, or a profile key); raises :class:`NotNormalizable`, in which
    case the value is not recorded."""
    n = normalize(kind, raw)
    return _hmac_sha256(key, "x-deal-v0/fp\x00", kind, "\x00", n).hex()


def commit_text(nonce_hex: str, text: str) -> str:
    """The salted commitment ``COMMIT_ALG`` names: SHA-256 over the JCS bytes
    of ``{"nonce", "text"}``, with ``nonce_hex`` a fresh random 256-bit
    value (64 lowercase hex) for each commitment. Whoever holds its opening
    (nonce and text) recomputes it, and nobody else can test a guess against
    it."""
    return json_digest({"nonce": nonce_hex, "text": text})
