# SPDX-License-Identifier: Apache-2.0
"""The profile's schemas, as they ship in ``dealprofile/x-deal-v0``
(vendored byte for byte under ``_data/schemas``; a test compares them)."""
from __future__ import annotations

import json
from functools import lru_cache
from pathlib import Path

from jsonschema import Draft202012Validator
from referencing import Registry, Resource

from .names import (
    TYPE_ACTION_APPROVAL, TYPE_ACTION_EVALUATION, TYPE_ACTION_OUTCOME, TYPE_ACTION_RECORD, TYPE_ACTION_REPORT,
    TYPE_CHECK_REQUEST, TYPE_CHECK_RESPONSE, TYPE_COMMERCIAL_BOUNDS, TYPE_PROPOSED_ACTION, TYPE_TASK_AUTHORITY,
)

__all__ = ["profile_schema", "profile_validator", "record_validator", "RECORD_SCHEMA_TYPES"]

_DIR = Path(__file__).resolve().parent / "_data" / "schemas"

#: The typed records and contract types the records schemas define.
RECORD_SCHEMA_TYPES = (
    TYPE_TASK_AUTHORITY, TYPE_PROPOSED_ACTION, TYPE_ACTION_EVALUATION, TYPE_ACTION_APPROVAL, TYPE_ACTION_RECORD,
    TYPE_ACTION_OUTCOME, TYPE_ACTION_REPORT, TYPE_CHECK_REQUEST, TYPE_CHECK_RESPONSE, TYPE_COMMERCIAL_BOUNDS,
)


def profile_schema() -> bytes:
    """The raw x-deal-v0 record schema."""
    return (_DIR / "x-deal-v0.schema.json").read_bytes()


@lru_cache(maxsize=None)
def profile_validator() -> Draft202012Validator:
    return Draft202012Validator(json.loads(profile_schema()))


@lru_cache(maxsize=None)
def _registry() -> Registry:
    registry = Registry()
    for p in sorted((_DIR / "records").glob("*.schema.json")):
        doc = json.loads(p.read_bytes())
        registry = registry.with_resource(doc["$id"], Resource.from_contents(doc))
    return registry


@lru_cache(maxsize=None)
def record_validator(type_name: str) -> Draft202012Validator:
    """The validator of a typed record or contract type (PROFILE.md §9), such
    as ``"task-authority/v0"``; raises ``ValueError`` for another name."""
    if type_name not in RECORD_SCHEMA_TYPES:
        raise ValueError(f"unknown record type {type_name!r}")
    doc = json.loads((_DIR / "records" / (type_name.replace("/", "-") + ".schema.json")).read_bytes())
    return Draft202012Validator(doc, registry=_registry())
