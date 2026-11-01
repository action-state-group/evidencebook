# SPDX-License-Identifier: Apache-2.0
"""evidencebook: an evidence book over a Checkpointed Local Log, and the
evidence request interaction (draft-mih-agent-evidence-request-00, profiles
evidence-interop/0 and evidence-interop/1) on both sides of it.

This is the Python implementation of the same semantics as the Go module
``github.com/action-state-group/evidencebook``: for the same calls it seals
the same records (``evidence_request``, ``evidence_response``,
``recorded_absence``, ``evidence_request_answered``), issues the same
checkpoints and answers with the same reply octets.
"""
from ._cll import Checkpoint
from .book import (
    DEFAULT_DEVELOPER,
    MINIMUM_ABSENCE_WINDOW,
    MAX_REPLY_BYTES,
    MAX_REPLY_DEPTH,
    MAX_REPLY_ITEMS,
    MAX_REPLY_RECEIPTS,
    WITNESSED,
    UNWITNESSED,
    WITNESS_RECEIPTS_MEMBER,
    WitnessRow,
    OUTCOME_ARTIFACT,
    OUTCOME_ARTIFACT_FAILED,
    OUTCOME_REFUSAL,
    OUTCOME_REFUSAL_NONCONFORMING,
    REFUSAL_POLICY_SPECIFIC,
    REFUSAL_POLICY_UNIFORM,
    RELATIONSHIP_COUNTERPARTY,
    RELATIONSHIP_IDENTIFIED,
    RELATIONSHIP_STRANGER,
    OPENING_PREFIX,
    RECORD_HEADER_MEMBER,
    Book,
    CapsuleSealer,
    DerivationDefinition,
    Opening,
    RequestLimiter,
    ResponderKeys,
    ResponderPolicy,
    RespondOptions,
    SentRequest,
    SharePolicy,
    WindowLimiter,
    new_opening,
    verify_opening,
)
from .cadence import (
    Cadence,
    CadenceConfig,
    CadenceTick,
    LegacyCadence,
    StandardHistoryAnchor,
    TickResult,
    WitnessCheck,
    WitnessReceipt,
    WitnessRejected,
)
from .errors import (BookError, CorruptError, InvalidError, NotCoveredError, NotFoundError, ReleasedError,
                     UnavailableError, UnsupportedError)
from .record import (
    CITES,
    COMMIT_ALG_NONCE256,
    EPISTEMIC_TYPES,
    LINK_TYPES,
    OBSERVED_EVENT,
    PRODUCER_CLAIM,
    RECORD_TYPE_ABSENCE,
    RECORD_TYPE_REQUEST,
    RECORD_TYPE_REQUEST_ANSWERED,
    RECORD_TYPE_RESPONSE,
    Correlation,
    Entry,
    Header,
    Link,
    Record,
)
from .request import (
    DERIVATION_HISTORY_CARD,
    MEDIA_TYPE_CBOR,
    MEDIA_TYPE_JSON,
    PROFILE,
    PROFILE_V1,
    Coverage,
    EvidenceRequest,
    Message,
    Reply,
    Subject,
    encode_request,
    parse_reply,
)
from .signer import Ed25519Signer, Signer
from .shelf import HeldLock, HostLog, HostLogStores, Shelf, ShelfRecord, ShelfRecords, SqliteStore, SqliteSubstrate
from .store import FileStore, MemoryStore, Store, StoredRecord

__all__ = [
    "Book", "CapsuleSealer", "Opening", "new_opening", "verify_opening", "RECORD_HEADER_MEMBER", "OPENING_PREFIX",
    "COMMIT_ALG_NONCE256", "Checkpoint", "Entry", "Header", "Link", "Correlation", "Record",
    "EvidenceRequest", "Subject", "Coverage", "Message", "Reply", "SentRequest", "ResponderKeys",
    "RespondOptions", "SharePolicy", "ResponderPolicy", "RequestLimiter", "WindowLimiter", "DerivationDefinition",
    "Signer", "Ed25519Signer", "Store", "MemoryStore", "FileStore", "StoredRecord",
    "Shelf", "ShelfRecord", "ShelfRecords", "SqliteStore", "SqliteSubstrate", "HostLog", "HostLogStores", "HeldLock",
    "Cadence", "CadenceConfig", "CadenceTick", "LegacyCadence", "StandardHistoryAnchor", "TickResult", "WitnessCheck",
    "WitnessReceipt", "WitnessRejected",
    "encode_request", "parse_reply",
    "PROFILE", "PROFILE_V1", "DERIVATION_HISTORY_CARD", "MEDIA_TYPE_JSON", "MEDIA_TYPE_CBOR", "DEFAULT_DEVELOPER",
    "MINIMUM_ABSENCE_WINDOW", "MAX_REPLY_BYTES", "MAX_REPLY_DEPTH", "MAX_REPLY_ITEMS", "MAX_REPLY_RECEIPTS",
    "WITNESSED", "UNWITNESSED", "WITNESS_RECEIPTS_MEMBER", "WitnessRow", "OUTCOME_ARTIFACT", "OUTCOME_ARTIFACT_FAILED", "OUTCOME_REFUSAL",
    "OUTCOME_REFUSAL_NONCONFORMING", "REFUSAL_POLICY_SPECIFIC", "REFUSAL_POLICY_UNIFORM",
    "RELATIONSHIP_STRANGER", "RELATIONSHIP_IDENTIFIED", "RELATIONSHIP_COUNTERPARTY",
    "CITES", "EPISTEMIC_TYPES", "LINK_TYPES", "OBSERVED_EVENT", "PRODUCER_CLAIM",
    "RECORD_TYPE_REQUEST", "RECORD_TYPE_RESPONSE", "RECORD_TYPE_ABSENCE", "RECORD_TYPE_REQUEST_ANSWERED",
    "BookError", "InvalidError", "NotFoundError", "CorruptError", "NotCoveredError", "ReleasedError", "UnsupportedError", "UnavailableError",
]
