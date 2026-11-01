# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v0 deal record profile, the Python counterpart of the Go
package ``dealprofile`` in this repository, at byte parity with it: the
profile's names, the keys and fingerprints that keep raw identifiers out of a
record, a record's payload and digest, the record-level checks a sealed
record must pass, the sealed ``deal_report``'s payload, the members of a
step's Capsule, and the standard_history/1 deal entry. The normative text is
``dealprofile/x-deal-v0/PROFILE.md``; its schemas are vendored here byte for
byte.

The package holds no deal logic: what a step says, what it is approved by and
what a report's lines read are the producer's. It seals nothing either: it
returns the bytes and plain Capsule members (:class:`Capsule`), and the
producer seals them with its own key, on its own book.

Go's typed record structs have no counterpart: a record is a ``dict``, read
by :func:`decode` and refused or accepted by :func:`check` against the
profile's schemas. The chain stage, which reads a record against the records
before it, is not here; the reference checker (``check_profile.py``) runs it.
"""
from .capsule import (
    ACTION_TYPE_DECIDE, ACTION_TYPE_FYI, ATTESTATION_RUNTIME_CLAIMED, EFFECT_DISPATCHED, RELATION_CONFIRMS,
    RELATION_FOLLOWS, Capsule, Chain, Disposition, Effect, Extension, emit_input,
)
from .check import (
    STAGE_CANONICALIZATION, STAGE_DIGEST, STAGE_PERSONAL_DATA, STAGE_SCHEMA, STAGE_WORDING, StageError, check,
    check_digest, decode, digest, encode,
)
from .keys import NotNormalizable, commit_text, deal_key, fingerprint, kinds, normalize, profile_key
from .names import *  # noqa: F401,F403
from .names import record_types
from .preimage import preimage_matches
from .report import Checkpoint, Report, report_capsule, report_payload
from .schema import RECORD_SCHEMA_TYPES, profile_schema, profile_validator, record_validator
from .standard_history import (
    HistoryInput, HistoryRecord, HistoryResult, Unpriced, minor_units, standard_history_deal,
)
from .step import EPISTEMIC_PRODUCER_CLAIM, Decide, StepHeader, StepInput, step_capsule
