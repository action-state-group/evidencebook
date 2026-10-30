//! The x-deal-v0 deal record profile: an opt-in reference profile, built
//! only with the crate's `deal` feature.
//!
//! This module is the Rust counterpart of the Go package `dealprofile` in
//! this repository, and produces the same bytes: the profile's names, the
//! keys and fingerprints that keep raw identifiers out of a record, a
//! record's payload and digest, the record-level checks a sealed record must
//! pass, the sealed `deal_report`'s payload, and the members of a step's
//! Capsule. The normative text is `dealprofile/x-deal-v0/PROFILE.md`; its
//! schemas are vendored here byte for byte.
//!
//! It is a profile the crate carries, not part of the Evidence Layer
//! semantics: nothing else in the crate depends on it. It holds no deal
//! logic: what a step says, what it is approved by and what a report's lines
//! read are the producer's. It seals nothing either: it returns the bytes
//! and plain Capsule members ([`Capsule`]), and the producer seals them with
//! its own sealer and key, on its own book.
//!
//! The chain stage, which reads a record against the records before it, is
//! not here; the reference checker (`check_profile.py`) runs it.

mod capsule;
mod check;
mod keys;
mod names;
mod publicsuffix;
mod report;
mod schema;
mod step;

pub use capsule::{
    Capsule, Chain, Disposition, Effect, Extension, ACTION_TYPE_DECIDE, ACTION_TYPE_FYI,
    ATTESTATION_RUNTIME_CLAIMED, EFFECT_DISPATCHED, RELATION_CONFIRMS, RELATION_FOLLOWS,
};
pub use check::{
    check, check_digest, digest, encode, Error, StageError, STAGE_CANONICALIZATION, STAGE_DIGEST,
    STAGE_PERSONAL_DATA, STAGE_SCHEMA, STAGE_WORDING,
};
pub use keys::{
    commit_text, deal_key, fingerprint, kinds, normalize, profile_key, NotNormalizable,
};
pub use names::*;
pub use report::{report_capsule, report_payload, Checkpoint, Report};
pub use schema::profile_schema;
pub use step::{step_capsule, Decide, StepHeader, StepInput};
