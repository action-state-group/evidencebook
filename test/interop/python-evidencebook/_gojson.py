# SPDX-License-Identifier: Apache-2.0
"""Go's json.MarshalIndent output for the values the interop programs write,
so a Python run's file can be compared with a Go run's byte for byte."""
from __future__ import annotations

import json
import re

# Python writes U+0008 and U+000C as \b and \f; Go writes \u0008 and \u000c. An
# escape starts after an even run of backslashes, so a literal "\\b" is left alone.
_SHORT = re.compile(r"(?<!\\)((?:\\\\)*)\\([bf])")


def marshal_indent(value: object, indent: str) -> bytes:
    text = json.dumps(value, ensure_ascii=False, indent=indent, separators=(",", ": "))
    # encoding/json escapes these inside strings, and they occur nowhere else.
    for char, escaped in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"),
                          (" ", "\\u2028"), (" ", "\\u2029")):
        text = text.replace(char, escaped)
    text = _SHORT.sub(lambda m: m.group(1) + ("\\u0008" if m.group(2) == "b" else "\\u000c"), text)
    return text.encode("utf-8")
