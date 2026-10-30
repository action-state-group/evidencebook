//! The profile's names (PROFILE.md §1-§3, §9), as Go's `dealprofile`
//! (`names.go`) names them.

/// The record block's member name and its profile value.
pub const PROFILE: &str = "x-deal-v0";
/// The only canonicalization a record may name.
pub const CANONICALIZATION: &str = "jcs";
/// Names a record's counterparty fingerprints: fingerprints under the deal
/// key ([`deal_key`](super::deal_key)).
pub const FP_ALG: &str = "hmac-sha256-deal-key";
/// Names the fingerprints a `counterparty_profile` record seals: under the
/// profile key ([`profile_key`](super::profile_key)).
pub const PROFILE_FP_ALG: &str = "hmac-sha256-profile-key";
/// The type of a digest reference to a deal record.
pub const RECORD_REF: &str = "deal-record";
/// Names the salted commitment [`commit_text`](super::commit_text) makes.
pub const COMMIT_ALG: &str = "sha256-jcs-nonce256";
/// The top-level Capsule member a step carries its deal id in, which a
/// composition joins the parties' copies on.
pub const DEAL_ID_MEMBER: &str = "deal_id";

// The x-deal-v0 record types (PROFILE.md §3), in the schema's order.
pub const TYPE_INTENT: &str = "intent";
pub const TYPE_BASELINE: &str = "baseline";
pub const TYPE_MESSAGE: &str = "message";
pub const TYPE_CLAIM: &str = "claim";
pub const TYPE_EVIDENCE: &str = "evidence";
pub const TYPE_DETAIL_CHANGE: &str = "detail_change";
pub const TYPE_CHECK: &str = "check";
pub const TYPE_VERDICT: &str = "verdict";
pub const TYPE_APPROVAL: &str = "approval";
pub const TYPE_ACTION: &str = "action";
pub const TYPE_OUTCOME: &str = "outcome";
pub const TYPE_CLOSE: &str = "close";
pub const TYPE_DISCLOSURE: &str = "disclosure";
pub const TYPE_SALE: &str = "sale";
pub const TYPE_COUNTERPARTY_PROFILE: &str = "counterparty_profile";
pub const TYPE_THREAD: &str = "thread";
pub const TYPE_THREAD_OPENED: &str = "thread_opened";
pub const TYPE_SALE_CUT: &str = "sale_cut";
pub const TYPE_SALE_CLOSE: &str = "sale_close";
pub const TYPE_AGENT_MOVE: &str = "agent_move";

/// The x-deal-v0 record types, in the schema's order.
pub fn record_types() -> &'static [&'static str] {
    &[
        TYPE_INTENT,
        TYPE_BASELINE,
        TYPE_MESSAGE,
        TYPE_CLAIM,
        TYPE_EVIDENCE,
        TYPE_DETAIL_CHANGE,
        TYPE_CHECK,
        TYPE_VERDICT,
        TYPE_APPROVAL,
        TYPE_ACTION,
        TYPE_OUTCOME,
        TYPE_CLOSE,
        TYPE_DISCLOSURE,
        TYPE_SALE,
        TYPE_COUNTERPARTY_PROFILE,
        TYPE_THREAD,
        TYPE_THREAD_OPENED,
        TYPE_SALE_CUT,
        TYPE_SALE_CLOSE,
        TYPE_AGENT_MOVE,
    ]
}

// The typed action records and contract types (PROFILE.md §9).
pub const TYPE_TASK_AUTHORITY: &str = "task-authority/v0";
pub const TYPE_PROPOSED_ACTION: &str = "proposed-action/v0";
pub const TYPE_ACTION_EVALUATION: &str = "action-evaluation/v0";
pub const TYPE_ACTION_APPROVAL: &str = "action-approval/v0";
pub const TYPE_ACTION_RECORD: &str = "action-record/v0";
pub const TYPE_ACTION_OUTCOME: &str = "action-outcome/v0";
pub const TYPE_ACTION_REPORT: &str = "action-report/v0";
pub const TYPE_CHECK_REQUEST: &str = "check-request/v0";
pub const TYPE_CHECK_RESPONSE: &str = "check-response/v0";
pub const TYPE_COMMERCIAL_BOUNDS: &str = "commercial-bounds/v0";
/// The type of a digest reference to a typed record.
pub const TYPED_RECORD_REF: &str = "record";
/// Names a typed record's counterparty fingerprints.
pub const TYPED_FP_ALG: &str = "hmac-sha256-chain-key";

/// The materiality mode an evaluation states when it names a predicate, so
/// that no predicate is told apart from a missing field.
pub const MATERIALITY_MODE_PREDICATE: &str = "predicate";
/// The materiality mode an evaluation states when it names no predicate.
pub const MATERIALITY_MODE_NONE: &str = "none_fail_safe";

// The sealed report's names (PROFILE.md §1, "Sealed reports are not steps").

/// The `action_id` of every report Capsule.
pub const REPORT_ACTION_ID: &str = "capsulectl-deal-report";
/// The report payload's own `type` member (PROFILE.md §1), not the book
/// header's record type, which is [`REPORT_RECORD_TYPE`].
pub const REPORT_TYPE: &str = "deal_report";
/// The bundle's x-deal-v0 extension member that names the copy's report
/// Capsule.
pub const REPORT_POINTER: &str = "sealed_report";
/// The bundle extension carrying a standalone report: `{"record": the
/// Capsule with its signature and key_id, "agent_input": its payload}`.
pub const REPORT_EXTENSION: &str = "x-deal-report/v0";

// The audiences a copy, and its report, are cut for.
pub const AUDIENCE_KEEP: &str = "keep";
pub const AUDIENCE_COUNTERPARTY: &str = "counterparty";
pub const AUDIENCE_ADJUDICATOR: &str = "adjudicator";

// The evidence record header a step or report carries on a book.

/// The `record_type` of a step's evidence record header.
pub const STEP_RECORD_TYPE: &str = "deal_step";
/// The `record_type` of a report's evidence record header on a book, not the
/// payload's `type` member ([`REPORT_TYPE`]).
pub const REPORT_RECORD_TYPE: &str = "deal_report";
/// Commits a step's local event (its raw values and the nonces of its
/// commitments); its opening never leaves the device.
pub const LOCAL_FIELD: &str = "deal_local";
/// Commits the deal's closed amount.
pub const AMOUNT_FIELD: &str = "amount";
/// Commits the deal's closed currency.
pub const CURRENCY_FIELD: &str = "currency";
/// The epistemic type of every step and report record: the producer's own
/// claim.
pub const EPISTEMIC_PRODUCER_CLAIM: &str = "producer_claim";
