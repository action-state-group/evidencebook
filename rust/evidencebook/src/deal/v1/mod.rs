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

mod authorization;
mod check;
mod names;
mod record;
mod report;
mod sent;
mod start;
mod status_rows;
mod status_value;
mod types;

pub use authorization::{check_authorizations, new_nonce, ActAuthorization, STAGE_AUTHORIZATION};
pub use check::{
    check, check_agreement, check_dispute, check_ruling, check_sealed, digest, encode,
    placement_of, profile_schema, record_digest, ref_to, rows_digest, Placement, SealContext,
    STAGE_AGREEMENT, STAGE_RECORD,
};
pub use names::*;
pub use record::{decode, Body, Record};
pub use report::{
    build_report, report_payload, settled, Report, ReportBooks, ReportCheckpoint, ReportCopy,
    ReportCounterpartLog, ReportFact, ReportLate, ReportLateDispute, ReportLateStatement,
    ReportLog, ReportRestsOn, ReportRow, ReportRuling, ReportSource, CHECK_FAILED,
    CHECK_NOT_RECOMPUTED, CHECK_NOT_RUN, CHECK_PASSED, NOT_SET, PROOF_HELD, PROOF_NOT_YET,
    PROOF_NO_LAYER, REMEDY_NOT_COUNTED, ROWS_LISTED, ROWS_NOT_STARTED, ROWS_WITHHELD,
    SIDE_ADJUDICATOR,
};
pub use sent::{
    pair, payment_rows, terms_leg, verify_sent, OfferContext, Sent, SENT_SUFFIX, SETTLEMENT_MEMBER,
    SETTLEMENT_VERSION, STAGE_SENT,
};
pub use start::{
    start, start_from_books, BookRecord, FirstAct, StartBooks, StartInput, StartResult,
    BY_ACCEPTOR_NAMED, BY_FIRST_ACT, BY_OFFERER_IN_TIME, STATE_AWAITING_AGREEMENT,
    STATE_NOT_STARTED, STATE_STARTED,
};
pub use status_rows::{row_inputs, RowBooks, TimedRecord};
pub use status_value::{
    build_status, resolve_row, PaymentResult, StatusAdjud, StatusBooks, StatusDispute, StatusNamed,
    StatusRecord, StatusRow, StatusRowDispute, StatusRowInput, StatusRuling, StatusSideTime,
    StatusStart, StatusStatement, StatusTime, StatusTimeOnly, StatusValue, ValueBand,
    OUTCOME_AGAINST_CLAIMANT, OUTCOME_FOR_CLAIMANT, OUTCOME_NO_FINDING, OUTCOME_OPEN,
    OUTCOME_WITHDRAWN,
};
pub use types::*;
