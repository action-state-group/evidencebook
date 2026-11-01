# SPDX-License-Identifier: Apache-2.0
"""The profile's names (PROFILE.md §1-§3, §9), as Go's ``dealprofile/names.go``."""
from __future__ import annotations

PROFILE = "x-deal-v0"
CANONICALIZATION = "jcs"
FP_ALG = "hmac-sha256-deal-key"
PROFILE_FP_ALG = "hmac-sha256-profile-key"
RECORD_REF = "deal-record"
COMMIT_ALG = "sha256-jcs-nonce256"
#: The top-level Capsule member a step carries its deal id in.
DEAL_ID_MEMBER = "deal_id"

TYPE_INTENT = "intent"
TYPE_BASELINE = "baseline"
TYPE_MESSAGE = "message"
TYPE_CLAIM = "claim"
TYPE_EVIDENCE = "evidence"
TYPE_DETAIL_CHANGE = "detail_change"
TYPE_CHECK = "check"
TYPE_VERDICT = "verdict"
TYPE_APPROVAL = "approval"
TYPE_ACTION = "action"
TYPE_OUTCOME = "outcome"
TYPE_CLOSE = "close"
TYPE_DISCLOSURE = "disclosure"
TYPE_SALE = "sale"
TYPE_COUNTERPARTY_PROFILE = "counterparty_profile"
TYPE_THREAD = "thread"
TYPE_THREAD_OPENED = "thread_opened"
TYPE_SALE_CUT = "sale_cut"
TYPE_SALE_CLOSE = "sale_close"
TYPE_AGENT_MOVE = "agent_move"

_RECORD_TYPES = (
    TYPE_INTENT, TYPE_BASELINE, TYPE_MESSAGE, TYPE_CLAIM, TYPE_EVIDENCE, TYPE_DETAIL_CHANGE, TYPE_CHECK,
    TYPE_VERDICT, TYPE_APPROVAL, TYPE_ACTION, TYPE_OUTCOME, TYPE_CLOSE, TYPE_DISCLOSURE, TYPE_SALE,
    TYPE_COUNTERPARTY_PROFILE, TYPE_THREAD, TYPE_THREAD_OPENED, TYPE_SALE_CUT, TYPE_SALE_CLOSE, TYPE_AGENT_MOVE,
)


def record_types() -> list[str]:
    """The x-deal-v0 record types, in the schema's order (a copy)."""
    return list(_RECORD_TYPES)


TYPE_TASK_AUTHORITY = "task-authority/v0"
TYPE_PROPOSED_ACTION = "proposed-action/v0"
TYPE_ACTION_EVALUATION = "action-evaluation/v0"
TYPE_ACTION_APPROVAL = "action-approval/v0"
TYPE_ACTION_RECORD = "action-record/v0"
TYPE_ACTION_OUTCOME = "action-outcome/v0"
TYPE_ACTION_REPORT = "action-report/v0"
TYPE_CHECK_REQUEST = "check-request/v0"
TYPE_CHECK_RESPONSE = "check-response/v0"
TYPE_COMMERCIAL_BOUNDS = "commercial-bounds/v0"
TYPED_RECORD_REF = "record"
TYPED_FP_ALG = "hmac-sha256-chain-key"

MATERIALITY_MODE_PREDICATE = "predicate"
MATERIALITY_MODE_NONE = "none_fail_safe"

REPORT_ACTION_ID = "capsulectl-deal-report"
REPORT_TYPE = "deal_report"
REPORT_POINTER = "sealed_report"
REPORT_EXTENSION = "x-deal-report/v0"

AUDIENCE_KEEP = "keep"
AUDIENCE_COUNTERPARTY = "counterparty"
AUDIENCE_ADJUDICATOR = "adjudicator"

STEP_RECORD_TYPE = "deal_step"
REPORT_RECORD_TYPE = "deal_report"
LOCAL_FIELD = "deal_local"
AMOUNT_FIELD = "amount"
CURRENCY_FIELD = "currency"
