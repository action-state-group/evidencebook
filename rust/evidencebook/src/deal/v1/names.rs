// SPDX-License-Identifier: Apache-2.0

//! The x-deal-v1 profile's names (design §2.2, §3.1), as Go's `xdealv1`
//! names them (`names.go`).

use crate::deal::names as v0;

/// The record block's member name and its profile value.
pub const PROFILE: &str = "x-deal-v1";
/// The only canonicalization a record may name.
pub const CANONICALIZATION: &str = v0::CANONICALIZATION;
/// The type of a digest reference to a deal record, as v0's.
pub const RECORD_REF: &str = v0::RECORD_REF;
/// The digest algorithm of every reference.
pub const DIGEST_ALG: &str = "SHA-256";
/// The salted commitment `commit_text` makes.
pub const COMMIT_ALG: &str = v0::COMMIT_ALG;
/// A record's counterparty fingerprints, as v0's.
pub const FP_ALG: &str = v0::FP_ALG;

// The ten record types (design §7; §3.2).
pub const TYPE_PROPOSE: &str = "propose";
pub const TYPE_AGREE: &str = "agree";
pub const TYPE_PERFORM: &str = "perform";
pub const TYPE_SETTLE: &str = "settle";
pub const TYPE_STATEMENT: &str = "statement";
pub const TYPE_DISPUTE: &str = "dispute";
pub const TYPE_RULING: &str = "ruling";
pub const TYPE_ASK: &str = "ask";
pub const TYPE_ANSWER: &str = "answer";
pub const TYPE_ADMIT: &str = "admit";

// The journal overlay (§3.2): sealed in the journal only, v0's bodies (check
// adds payment_ref; check, verdict and approval add nonce).
pub const TYPE_INTENT: &str = v0::TYPE_INTENT;
pub const TYPE_CHECK: &str = v0::TYPE_CHECK;
pub const TYPE_VERDICT: &str = v0::TYPE_VERDICT;
pub const TYPE_APPROVAL: &str = v0::TYPE_APPROVAL;

/// The deal-book overlay type, v0's body (§3.3).
pub const TYPE_RULES_VERDICT: &str = v0::TYPE_RULES_VERDICT;

// The journey overlay (§3.2): v0's bodies, sealed in the journal always.
pub const TYPE_MESSAGE: &str = v0::TYPE_MESSAGE;
pub const TYPE_CLAIM: &str = v0::TYPE_CLAIM;
pub const TYPE_EVIDENCE: &str = v0::TYPE_EVIDENCE;
pub const TYPE_DETAIL_CHANGE: &str = v0::TYPE_DETAIL_CHANGE;
pub const TYPE_AGENT_MOVE: &str = v0::TYPE_AGENT_MOVE;
pub const TYPE_DISCLOSURE: &str = v0::TYPE_DISCLOSURE;
pub const TYPE_SALE: &str = v0::TYPE_SALE;
pub const TYPE_THREAD: &str = v0::TYPE_THREAD;
pub const TYPE_THREAD_OPENED: &str = v0::TYPE_THREAD_OPENED;
pub const TYPE_SALE_UNPOST: &str = v0::TYPE_SALE_UNPOST;
pub const TYPE_COUNTERPARTY_PROFILE: &str = v0::TYPE_COUNTERPARTY_PROFILE;

const RECORD_TYPES: &[&str] = &[
    TYPE_PROPOSE,
    TYPE_AGREE,
    TYPE_PERFORM,
    TYPE_SETTLE,
    TYPE_STATEMENT,
    TYPE_DISPUTE,
    TYPE_RULING,
    TYPE_ASK,
    TYPE_ANSWER,
    TYPE_ADMIT,
    TYPE_INTENT,
    TYPE_CHECK,
    TYPE_VERDICT,
    TYPE_APPROVAL,
    TYPE_RULES_VERDICT,
    TYPE_MESSAGE,
    TYPE_CLAIM,
    TYPE_EVIDENCE,
    TYPE_DETAIL_CHANGE,
    TYPE_AGENT_MOVE,
    TYPE_DISCLOSURE,
    TYPE_SALE,
    TYPE_THREAD,
    TYPE_THREAD_OPENED,
    TYPE_SALE_UNPOST,
    TYPE_COUNTERPARTY_PROFILE,
];

/// Every x-deal-v1 record type: the ten, the journal overlay,
/// rules_verdict and the journey overlay, in Go's `RecordTypes` order.
pub fn record_types() -> &'static [&'static str] {
    RECORD_TYPES
}

// Kinds of an agree body (§3.3, §3.5).
pub const KIND_AGREEMENT: &str = "agreement";
pub const KIND_DECLINE: &str = "decline";
pub const KIND_CANCELLATION: &str = "cancellation";

// Kinds of a dispute body (§3.3).
pub const KIND_DISPUTE: &str = "dispute";
pub const KIND_WITHDRAWAL: &str = "withdrawal";
pub const KIND_CHALLENGE: &str = "challenge";

// Roles, sides, legs, values and reasons.
pub const ROLE_BUYER: &str = "buyer";
pub const ROLE_SELLER: &str = "seller";
pub const SIDE_OFFERER: &str = "offerer";
pub const SIDE_ACCEPTOR: &str = "acceptor";
pub const LEG_PAYER: &str = "payer";
pub const LEG_PAYEE: &str = "payee";
pub const VALUE_MET: &str = "met";
pub const VALUE_NOT_MET: &str = "not_met";
pub const VALUE_CANNOT: &str = "cannot_tell";
pub const REASON_NOT_SELECTED: &str = "not_selected";
pub const REASON_OTHER: &str = "other";
pub const CHOICE_CONFIRM: &str = "confirm_limits";

// Forums (evidencebook#404; #482 Q4).
pub const FORUM_AUTO_TWIN: &str = "auto:twin";
pub const FORUM_AUTO_REF: &str = "auto:ref";
