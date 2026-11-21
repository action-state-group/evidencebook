// SPDX-License-Identifier: Apache-2.0

//! The x-deal-v1 deal record profile (evidencebook#461,
//! `docs/design/461-deal-profile.md`), the Rust counterpart of the Go
//! package `dealprofile/xdealv1`, producing the same bytes and refusals: its
//! names, record types and bodies, a record's strict encoding and digest,
//! and the checks a record must pass. The normative text is
//! `dealprofile/x-deal-v1/PROFILE.md`; its schema is vendored here byte for
//! byte.
//!
//! v1 keeps v0's canonicalization, digest, keys and fingerprints, and v0's
//! body types for the journal and journey overlays. Like v0's module it
//! seals nothing: it returns bytes and verdicts.
//!
//! The CI differential check (`test/interop/deal-v1`) holds Go and this
//! module to the same results on random inputs. Where they still differ, it
//! is on input no checked record can carry, except the first case below,
//! which a report can carry:
//!
//! - a record text, or a report pair, with a member named
//!   `"$serde_json::private::Number"` or `"$serde_json::private::RawValue"`,
//!   which serde_json would read as something else, is refused here at the
//!   canonicalization stage; Go reads it as written. This is a limitation of
//!   this crate: a report's free `report` text is the producer's own (Go's
//!   `BuildReport` copies it as given), so a report Go builds and verifies
//!   can carry such a member, and this crate refuses it;
//! - a record inside status, row or report books is read from a parsed
//!   value, so a member given twice there is not seen (every text entry
//!   point, [`decode`], [`check`] and [`verify_sent`], refuses it as Go
//!   does); a required list written `null`, a member the types do not name
//!   or a member name in another case is refused there, where Go's reader
//!   takes it.
//!
//! [`verify_book`] decodes the header as Go's typed `Header` does (each
//! member's Go type, null as the zero value), once both have refused a
//! member name the header's types do not have exactly (evidencebook#559).

mod authorization;
mod book;
mod check;
mod exact;
mod gotime;
#[cfg(feature = "shelf")]
mod holder;
mod names;
mod payment;
mod record;
mod report;
mod report_bundle;
mod report_verify;
mod sent;
#[cfg(feature = "shelf")]
mod shelf_status;
mod start;
mod status_rows;
mod status_value;
mod types;

pub use authorization::{check_authorizations, new_nonce, ActAuthorization, STAGE_AUTHORIZATION};
pub use book::{book_pair, verify_book, BookRead, STAGE_BOOK};
pub use check::{
    check, check_agreement, check_dispute, check_ruling, check_sealed, digest, encode,
    placement_of, profile_schema, record_digest, ref_to, rows_digest, Placement, SealContext,
    STAGE_AGREEMENT, STAGE_RECORD,
};
#[cfg(feature = "shelf")]
pub use holder::{
    ActGate, ActGateInput, CanActInput, ReceiveProfile, GATE_NO_LAYER, GATE_NO_PROOF,
    SENT_LOG_PREFIX,
};
pub use names::*;
pub use payment::{
    payment_check, PaymentInput, PAYMENT_FAIL, PAYMENT_PASS, REASON_AMOUNT, REASON_MISMATCH,
    REASON_NOT_AGREED, REASON_PENDING, REASON_REFUND_CITES_OFFER, REASON_SEVERAL_PAYMENTS,
    REASON_STATUS, REASON_UNJOINED, STAGE_PAYMENT,
};
pub use record::{decode, Body, Record};
pub use report::{
    build_report, report_payload, settled, Report, ReportBooks, ReportCheckpoint, ReportCopy,
    ReportCounterpartLog, ReportFact, ReportLate, ReportLateDispute, ReportLateStatement,
    ReportLog, ReportRestsOn, ReportRow, ReportRuling, ReportSource, CHECK_FAILED,
    CHECK_NOT_RECOMPUTED, CHECK_NOT_RUN, CHECK_PASSED, NOT_SET, PROOF_HELD, PROOF_NOT_YET,
    PROOF_NO_LAYER, REMEDY_NOT_COUNTED, ROWS_LISTED, ROWS_NOT_STARTED, ROWS_WITHHELD,
    SIDE_ADJUDICATOR,
};
pub use report_bundle::{verify_report_bundle, ReportBook, ReportBundle};
pub use report_verify::{report_pair, verify_report, ReportRead, STAGE_REPORT};
pub use sent::{
    pair, payment_rows, terms_leg, verify_sent, OfferContext, Sent, SENT_SUFFIX, SETTLEMENT_MEMBER,
    SETTLEMENT_VERSION, STAGE_SENT,
};
#[cfg(feature = "shelf")]
pub use shelf_status::{
    book_entry, name_all_rulings, name_rulings, DealStatus, NamingError, OfferPair,
};
pub use start::{
    start, start_from_books, BookRecord, FirstAct, StartBooks, StartInput, StartResult,
    BY_ACCEPTOR_NAMED, BY_FIRST_ACT, BY_OFFERER_IN_TIME, STATE_AWAITING_AGREEMENT,
    STATE_NOT_STARTED, STATE_STARTED,
};
pub use status_rows::{row_inputs, RowBooks, TimedRecord};
pub use status_value::{
    build_status, resolve_row, PaymentResult, StatusAdjud, StatusBooks, StatusDispute,
    StatusFirstAct, StatusNamed, StatusRecord, StatusRow, StatusRowDispute, StatusRowInput,
    StatusRuling, StatusSideTime, StatusStart, StatusStatement, StatusTime, StatusTimeOnly,
    StatusValue, ValueBand, OUTCOME_AGAINST_CLAIMANT, OUTCOME_FOR_CLAIMANT, OUTCOME_NO_FINDING,
    OUTCOME_OPEN, OUTCOME_WITHDRAWN,
};
pub use types::*;
