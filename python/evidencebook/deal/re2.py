# SPDX-License-Identifier: Apache-2.0
r"""Schema patterns read as Go's validator reads them (RE2), for the
x-deal-v0 and x-deal-v1 schema stages (evidencebook#546).

``jsonschema`` compiles a ``pattern`` as a Python regular expression, whose
``$`` also matches before a final newline and whose ``\s``, ``\d`` and
``\w`` take Unicode characters. Go's validator compiles it as RE2, where
they do not. :func:`go_patterns` rewrites a loaded schema so the two read
every pattern the profiles' schemas write alike.
"""
from __future__ import annotations

from typing import Any

#: RE2's ``\s``: Python's also takes ``\v`` and Unicode white space.
_RE2_SPACE = "\\t\\n\\f\\r "


def _re2(p: str) -> str:
    r"""A schema pattern read as Go's validator (RE2) reads it: ``\d``, ``\w``
    and ``\b`` in ASCII, ``\s`` as RE2's four spaces and the space, and a
    final ``$`` at the very end of the text (Python's also matches before a
    final newline)."""
    out, i, in_class = [], 0, False
    while i < len(p):
        c = p[i]
        if c == "\\" and i + 1 < len(p):
            e = p[i + 1]
            if e in "sS":
                body = _RE2_SPACE
                if in_class:
                    out.append(body if e == "s" else None)  # \S in a class: not written by the schemas
                else:
                    out.append(f"[{body}]" if e == "s" else f"[^{body}]")
            else:
                out.append(p[i:i + 2])
            i += 2
            continue
        if c == "[" and not in_class:
            in_class = True
        elif c == "]" and in_class:
            in_class = False
        out.append(c)
        i += 1
    if None in out:
        raise ValueError(f"schema pattern {p!r}: \\S inside a class")
    s = "".join(out)
    if s.endswith("$") and not s.endswith("\\$"):
        s = s[:-1] + r"\Z"
    return "(?a)" + s


def go_patterns(v: Any) -> Any:
    """A schema with each pattern read as Go's validator reads it (:func:`_re2`)."""
    if isinstance(v, dict):
        out = {k: go_patterns(e) for k, e in v.items()}
        p = out.get("pattern")
        if isinstance(p, str):
            out["pattern"] = _re2(p)
        return out
    if isinstance(v, list):
        return [go_patterns(e) for e in v]
    return v
