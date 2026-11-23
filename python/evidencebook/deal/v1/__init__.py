# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 deal record profile (evidencebook#461,
``docs/design/461-deal-profile.md``), the Python counterpart of the Go
package ``dealprofile/xdealv1``, producing the same bytes and refusals: its
names, a record's strict reading and digest, the checks a record must pass,
when a deal has started, the deal book's status value and a copy of the
report (:mod:`.status`, :mod:`.report`). The normative text is
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
from .report import (
    CHECK_FAILED, CHECK_NOT_RECOMPUTED, CHECK_NOT_RUN, CHECK_PASSED, NOT_SET, PROOF_HELD, PROOF_NO_LAYER, PROOF_NOT_YET,
    REMEDY_NOT_COUNTED, ROWS_LISTED, ROWS_NOT_STARTED, ROWS_WITHHELD, SIDE_ADJUDICATOR, build_report, report_payload,
    settled,
)
from .status import (
    OUTCOME_AGAINST_CLAIMANT, OUTCOME_FOR_CLAIMANT, OUTCOME_NO_FINDING, OUTCOME_OPEN, OUTCOME_WITHDRAWN, StatusError,
    TimedRecord, build_status, owed_amount, resolve_row, row_inputs, status_time, value_band,
)
from .authorization import STAGE_AUTHORIZATION, ActAuthorization, check_authorizations
from .payment import (
    PAYMENT_FAIL, PAYMENT_PASS, REASON_AMOUNT, REASON_MISMATCH, REASON_NOT_AGREED, REASON_PENDING,
    REASON_SEVERAL_PAYMENTS, REASON_STATUS, REASON_UNJOINED, STAGE_PAYMENT, PaymentInput,
    PaymentResult, payment_check,
)
from .sent import (
    SENT_LOG_PREFIX, SENT_SUFFIX, SETTLEMENT_MEMBER, SETTLEMENT_VERSION, STAGE_BOOK, STAGE_SENT, BookRead,
    OfferContext, Sent, book_pair, pair, payment_rows, terms_leg, verify_book, verify_sent,
)
from .holder import GATE_NO_LAYER, GATE_NO_PROOF, ActGate, ActGateInput, CanActInput, ReceiveProfile, version
from .report_bundle import ReportBook, ReportBundle, verify_report_bundle
from .report_verify import STAGE_REPORT, ReportRead, report_pair, verify_report
from .naming import book_entry, name_all_rulings, name_rulings
from .status_provider import StatusProvider
