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

mod check;
mod names;
mod record;
mod types;

pub use check::{
    check, check_agreement, check_dispute, check_ruling, check_sealed, digest, encode,
    placement_of, profile_schema, record_digest, ref_to, rows_digest, Placement, SealContext,
    STAGE_AGREEMENT, STAGE_RECORD,
};
pub use names::*;
pub use record::{decode, Body, Record};
pub use types::*;
