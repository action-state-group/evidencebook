# SPDX-License-Identifier: Apache-2.0
"""The one rule for every object a verify path reads into Go's types, as
Go's ``exactObject`` (``dealprofile/xdealv1/record.go``; evidencebook#559):
a pair, a signed Capsule, a settlement member, a record header and the
links in it.

Go's ``json.Unmarshal`` matches a member name in another case (U+017F for
``s`` and U+212A for ``k`` included), a later spelling replacing an earlier
one, so a signed object could otherwise carry a second spelling of a member
that Go reads in place of the one an exact reader takes. Each object must
name its members exactly as its type does: a name that differs from one of
its fields only by case folding is refused, and so is any other name unless
the object is open (one that carries members the profile does not read). A
member given twice is the text check's to refuse.

A type is a ``Spec``: ``None`` for a value read as it is, a ``dict`` of
member names to specs for an object, or ``[spec]`` for a list of them.
:func:`table_spec` makes one from the Go types table.
"""
from __future__ import annotations

from typing import Any

from . import golenient, gotypes

Spec = Any


def exact_object(v: Any, spec: Spec, open_: bool, at: str) -> str | None:
    """The first way ``v`` breaks the rule (a message), or ``None``."""
    if isinstance(spec, list):
        if isinstance(v, list):
            for i, e in enumerate(v):
                why = exact_object(e, spec[0], open_, f"{at}[{i}]")
                if why:
                    return why
        return None
    if not isinstance(spec, dict) or not isinstance(v, dict):
        return None
    for k, e in v.items():
        if k in spec:
            why = exact_object(e, spec[k], open_, f"{at}.{k}")
            if why:
                return why
            continue
        near = next((name for name in spec if golenient.fold(name) == golenient.fold(k)), None)
        if near is not None:
            return f"{at} member {k!r} is {near!r} spelled otherwise"
        if not open_:
            return f"{at} member {k!r} is not one it has"
    return None


def table_spec(t: Any) -> Spec:
    """The spec of a type of the Go types table (a struct, list, pointer or
    map of them; a map's own keys are its data, not member names)."""
    if isinstance(t, dict) and "ptr" in t:
        return table_spec(t["ptr"])
    if isinstance(t, dict) and "slice" in t:
        return [table_spec(t["slice"])]
    if isinstance(t, dict) and "struct" in t:
        return {f["name"]: table_spec(f["type"]) for f in gotypes.table()["structs"][t["struct"]]}
    return None


#: What the profile reads of a verified Capsule (Go ``capsuleWire``), open.
CAPSULE: Spec = {"action_id": None, "action_type": None, "references": None,
                 "model_attestation": {"compute_attestation": {"agent_input_digest": None}}}

#: What the payment check reads of a settlement member (Go
#: ``settlementWire``), open.
SETTLEMENT: Spec = {"leg": None, "sealer_role": None, "terms_ref": None}
