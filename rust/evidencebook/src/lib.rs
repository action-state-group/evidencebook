//! `evidencebook` -- evidence semantics over an embedded commitment history.
//!
//! This crate implements the store-level semantics of the Evidence Layer
//! Internet-Draft, `draft-mih-agent-evidence-layer-00` ("the I-D"; source:
//! <https://github.com/action-state-group/agent-action-capsule/blob/main/spec/draft-mih-agent-evidence-layer-00.md>).
//! Each public item names the I-D section it implements. The I-D is
//! implementation-independent: a store conforms by meeting its requirements,
//! not by using this crate.
//!
//! Two layers, kept distinct:
//!
//! ```text
//! evidencebook   records · epistemic types · links · retention · disclosure
//!                index classes · request answers · reconcile/close
//!   embeds
//! cll            append order · MMR · checkpoint · inclusion · consistency
//! ```
//!
//! The Checkpointed Local Log (`cll`, published as `checkpointed-local-log`)
//! is the reference commitment substrate. It is embedded, never exposed: no
//! public item of this crate takes or returns a `cll` type
//! (`tests/public_api.rs` enforces this), except the CLL backend a shelf's
//! host log hands its host, whose own log layer runs on it. Callers ask the book's substrate for
//! checkpoints and inclusion evidence in this crate's own types.
//!
//! Answers, reconcile inputs and index rebuilds read a book through the
//! read-only [`store`] traits, which the substrate implements. A caller whose
//! own checkpointer owns the log files implements them over its own index
//! instead, and this crate never opens those files.
//!
//! The crate carries no deployment vocabulary. Record kinds, correlation key
//! names and content comparators belong to the calling profile and are
//! supplied by it.
//!
//! | I-D section | Module |
//! |---|---|
//! | Record header (anchor `record`); Epistemic Type; Typed Links | [`record`]; sealing a record as Go's book does, [`seal`] |
//! | Record/Payload Separation; Retention States | [`retention`] |
//! | Disclosure Records | [`disclosure`] |
//! | Index Classes | [`index`] |
//! | The Commitment-Substrate Interface | [`substrate`]; the read-only store an answer reads, [`store`] |
//! | Answering a Request; Three Kinds of "No" | [`request`] |
//! | Answering a Request: the artifact response | [`answer`]; the requests a book was asked, [`received`]; definition-digest derivations, [`derivation`]; the served-summary derivation, [`served_summary`] |
//! | Answering a Request: the responder (gate, decision, the record of having answered) and the requester (the record of having asked, recorded absence) | [`respond`], [`ask`] |
//! | Reconcile and Close | [`reconcile`] |
//! | Privacy Considerations: What a Checkpoint Reveals | [`padding`] |
//! | (record identity: JCS and JSON-DIGEST) | [`canonical`] |
//!
//! One module is not Evidence Layer semantics but an opt-in reference
//! profile, built only with the cargo feature `deal`: `deal`, the x-deal-v0
//! deal record profile (its names, identifier fingerprints, record checks
//! and the members of the Capsules a deal's steps and reports are sealed
//! with), byte for byte with the Go package `dealprofile`. Nothing else in
//! the crate depends on it, and without the feature none of its dependencies
//! is built.
//!
//! The cargo feature `shelf` adds [`shelf`] (evidencebook#254): one subject
//! key's books in one SQLite file, the same file the Go and Python shelves write, with books that write, host logs, the writer lock and the
//! heartbeat cadence. Without the feature none of its dependencies is
//! built.

pub mod answer;
pub mod ask;
pub mod canonical;
#[cfg(feature = "deal")]
pub mod deal;
pub mod derivation;
pub mod disclosure;
mod ed25519;
pub mod index;
pub mod padding;
pub mod received;
pub mod reconcile;
pub mod record;
pub mod request;
pub mod respond;
pub mod retention;
pub mod seal;
pub mod served_summary;
#[cfg(feature = "shelf")]
pub mod shelf;
pub mod staple;
pub mod store;
pub mod substrate;
