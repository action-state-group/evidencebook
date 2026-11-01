# SPDX-License-Identifier: Apache-2.0
r"""JSON text as Go's ``encoding/json`` writes it, for the shelf's stored
values and the shelf vectors' dumps (testdata/shelf/README.md).

A Go struct writes its members in declaration order and a Go map in key
byte order, so callers build a struct as an :class:`Ordered` dict in that
order and a map as a plain dict, which :func:`go_json_indent` sorts. Go
escapes ``<``, ``>``, ``&``, U+2028 and U+2029, writes U+0008 and U+000C as
``\u0008`` and ``\u000c``, and other non-ASCII text as it is.
"""
from __future__ import annotations

import json
import re
from typing import Any

__all__ = ["go_json", "go_json_indent", "Ordered"]


class Ordered(dict):
    """A dict whose member order is kept even under :func:`sorted_maps`: a
    Go struct, or a ``json.RawMessage`` whose text keeps its own order."""


# Python writes U+0008 and U+000C as \b and \f. An escape starts after an even
# run of backslashes, so a literal "\\b" is left alone.
_SHORT = re.compile(r"(?<!\\)((?:\\\\)*)\\([bf])")


def _escape(text: str) -> str:
    # These occur only inside strings, where encoding/json escapes them.
    text = (text.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026")
            .replace("\u2028", "\\u2028").replace("\u2029", "\\u2029"))
    return _SHORT.sub(lambda m: m.group(1) + ("\\u0008" if m.group(2) == "b" else "\\u000c"), text)


def sorted_maps(value: Any) -> Any:
    """``value`` with every plain dict's members in key byte order (a Go
    map) and every :class:`Ordered` one as it is (a Go struct)."""
    if isinstance(value, Ordered):
        return Ordered((k, sorted_maps(v)) for k, v in value.items())
    if isinstance(value, dict):
        return {k: sorted_maps(value[k]) for k in sorted(value, key=lambda k: k.encode("utf-8"))}
    if isinstance(value, (list, tuple)):
        return [sorted_maps(v) for v in value]
    return value


def go_json(value: Any) -> str:
    """``json.Marshal``: compact, members in the order given."""
    return _escape(json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False))


def go_json_indent(value: Any) -> str:
    """``json.MarshalIndent(value, "", "  ")`` of a value whose plain dicts
    are Go maps, plus the final newline the dumps carry."""
    return _escape(json.dumps(sorted_maps(value), ensure_ascii=False, indent=2, allow_nan=False)) + "\n"
