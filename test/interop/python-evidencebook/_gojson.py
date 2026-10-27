# SPDX-License-Identifier: Apache-2.0
"""Go's json.MarshalIndent output for the values the interop programs write,
so a Python run's file can be compared with a Go run's byte for byte."""
from __future__ import annotations

import json


def marshal_indent(value: object, indent: str) -> bytes:
    text = json.dumps(value, ensure_ascii=False, indent=indent, separators=(",", ": "))
    # encoding/json escapes these inside strings, and they occur nowhere else.
    for char, escaped in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"),
                          (" ", "\\u2028"), (" ", "\\u2029")):
        text = text.replace(char, escaped)
    return text.encode("utf-8")
