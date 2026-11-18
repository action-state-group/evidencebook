# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 deal record profile (evidencebook#461,
``docs/design/461-deal-profile.md``), the Python counterpart of the Go
package ``dealprofile/xdealv1``, producing the same bytes and refusals: its
names, a record's strict reading and digest, the checks a record must pass,
and when a deal has started. The normative text is
``dealprofile/x-deal-v1/PROFILE.md``; its schema is vendored here byte for
byte.

A record is read as Go's ``encoding/json`` reads it into the profile's
types (:mod:`.gotypes`), from a table ``dealprofile/xdealv1``'s
``TestTypesTable`` writes from the Go types. Like v0's package it seals
nothing: it returns bytes and verdicts. The CI differential check
(``test/interop/deal-v1``) holds Go and this package to the same results on
random inputs.
"""
from .check import (
    PLACEMENT_CASE_BOOK, PLACEMENT_DEAL_BOOK, PLACEMENT_JOURNAL, STAGE_AGREEMENT, STAGE_CANONICALIZATION,
    STAGE_DIGEST, STAGE_RECORD, STAGE_SCHEMA, Record, SealContext, StageError, check, check_agreement,
    check_dispute, check_ruling, check_sealed, decode, digest, encode, placement_of, profile_schema, ref_to,
    rows_digest,
)
from .gotypes import ABSENT, GoTypeError
from .names import *  # noqa: F401,F403
from .names import record_types
from .start import (
    BY_ACCEPTOR_NAMED, BY_FIRST_ACT, BY_OFFERER_IN_TIME, STATE_AWAITING_AGREEMENT, STATE_NOT_STARTED,
    STATE_STARTED, BookRecord, FirstAct, StartBooks, StartInput, StartResult, start, start_from_books,
)
