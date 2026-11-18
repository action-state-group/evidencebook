# SPDX-License-Identifier: Apache-2.0
"""The x-deal-v1 profile's names (design §2.2, §3.1), as Go's
``dealprofile/xdealv1/names.go``."""
from __future__ import annotations

from .. import names as v0

PROFILE = "x-deal-v1"
CANONICALIZATION = v0.CANONICALIZATION
RECORD_REF = v0.RECORD_REF
DIGEST_ALG = "SHA-256"
COMMIT_ALG = v0.COMMIT_ALG
FP_ALG = v0.FP_ALG

TYPE_PROPOSE = "propose"
TYPE_AGREE = "agree"
TYPE_PERFORM = "perform"
TYPE_SETTLE = "settle"
TYPE_STATEMENT = "statement"
TYPE_DISPUTE = "dispute"
TYPE_RULING = "ruling"
TYPE_ASK = "ask"
TYPE_ANSWER = "answer"
TYPE_ADMIT = "admit"

# The journal overlay (§3.2): sealed in the journal only and never sent.
TYPE_INTENT = v0.TYPE_INTENT
TYPE_CHECK = v0.TYPE_CHECK
TYPE_VERDICT = v0.TYPE_VERDICT
TYPE_APPROVAL = v0.TYPE_APPROVAL
# The deal-book overlay type (§3.3).
TYPE_RULES_VERDICT = v0.TYPE_RULES_VERDICT
# The journey overlay (§3.2).
TYPE_MESSAGE = v0.TYPE_MESSAGE
TYPE_CLAIM = v0.TYPE_CLAIM
TYPE_EVIDENCE = v0.TYPE_EVIDENCE
TYPE_DETAIL_CHANGE = v0.TYPE_DETAIL_CHANGE
TYPE_AGENT_MOVE = v0.TYPE_AGENT_MOVE
TYPE_DISCLOSURE = v0.TYPE_DISCLOSURE
TYPE_SALE = v0.TYPE_SALE
TYPE_THREAD = v0.TYPE_THREAD
TYPE_THREAD_OPENED = v0.TYPE_THREAD_OPENED
TYPE_SALE_UNPOST = v0.TYPE_SALE_UNPOST
TYPE_COUNTERPARTY_PROFILE = v0.TYPE_COUNTERPARTY_PROFILE

_RECORD_TYPES = (
    TYPE_PROPOSE, TYPE_AGREE, TYPE_PERFORM, TYPE_SETTLE, TYPE_STATEMENT, TYPE_DISPUTE, TYPE_RULING, TYPE_ASK,
    TYPE_ANSWER, TYPE_ADMIT,
    TYPE_INTENT, TYPE_CHECK, TYPE_VERDICT, TYPE_APPROVAL, TYPE_RULES_VERDICT,
    TYPE_MESSAGE, TYPE_CLAIM, TYPE_EVIDENCE, TYPE_DETAIL_CHANGE, TYPE_AGENT_MOVE, TYPE_DISCLOSURE, TYPE_SALE,
    TYPE_THREAD, TYPE_THREAD_OPENED, TYPE_SALE_UNPOST, TYPE_COUNTERPARTY_PROFILE,
)


def record_types() -> list[str]:
    """Every x-deal-v1 record type: the ten, the journal overlay,
    rules_verdict and the journey overlay (a copy)."""
    return list(_RECORD_TYPES)


KIND_AGREEMENT = "agreement"
KIND_DECLINE = "decline"
KIND_CANCELLATION = "cancellation"

KIND_DISPUTE = "dispute"
KIND_WITHDRAWAL = "withdrawal"
KIND_CHALLENGE = "challenge"

ROLE_BUYER = "buyer"
ROLE_SELLER = "seller"
SIDE_OFFERER = "offerer"
SIDE_ACCEPTOR = "acceptor"
LEG_PAYER = "payer"
LEG_PAYEE = "payee"
VALUE_MET = "met"
VALUE_NOT_MET = "not_met"
VALUE_CANNOT = "cannot_tell"
REASON_NOT_SEL = "not_selected"
REASON_OTHER = "other"
CHOICE_CONFIRM = "confirm_limits"

#: Forums (evidencebook#404): auto:twin and auto:ref, or network:<id>,
#: rail:<program> or external:<class>.
FORUM_AUTO_TWIN = "auto:twin"
FORUM_AUTO_REF = "auto:ref"
