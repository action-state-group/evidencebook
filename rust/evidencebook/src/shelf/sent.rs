// SPDX-License-Identifier: Apache-2.0

//! A sent journal record (Go `shelf_sent.go`; evidencebook#461,
//! `docs/design/461-deal-profile.md` §3.1): a journal record that leaves the
//! shelf whole, such as an offer, must not show its place in the journal,
//! which a book record's Capsule does (its `action_id` is `<log id>#<seq>`).
//! So it is sealed twice, in one call: as a journal record whose one payload
//! commitment is the SHA-256 of the record's bytes, and as a standalone
//! Capsule, appended to no log, that carries the same bytes as its payload
//! (its `agent_input_digest` is the bytes' SHA-256, the record digest) under
//! the caller's `action_id`, `fyi`, with no chain, signed by the journal's
//! key. The standalone Capsule is what travels. The call is profile-neutral:
//! the bytes are the profile's, and sending them is the host's.

use std::collections::BTreeMap;
use std::time::Duration;

use serde_json::Value;

use super::book::{BookEntry, BookRecord, RECORD_HEADER_MEMBER};
use super::{Shelf, ShelfError};
use crate::canonical;
use crate::seal::{self, Producer, DEFAULT_DEVELOPER};

/// A sent record's standalone Capsule (Go `SentCapsule`): its id, its bytes,
/// its Producer Envelope (which carries the signature) and the payload it
/// commits, the record's bytes, which travel with it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SentCapsule {
    pub capsule_id: String,
    pub capsule: Vec<u8>,
    pub envelope: Vec<u8>,
    pub payload: Vec<u8>,
}

/// A sent record in the journal and its Capsule ([`Shelf::seal_sent`]).
#[derive(Debug)]
pub struct SealedSent {
    /// The journal record.
    pub record: BookRecord,
    /// The Capsule that travels.
    pub capsule: SentCapsule,
    /// A step after the record was stored that failed: its log append, which
    /// the journal retries first on its next write or open. The record is
    /// in the journal all the same, so the caller sends `capsule` and does
    /// not seal the record again.
    pub error: Option<ShelfError>,
}

impl Shelf {
    /// Append `entry` to the shelf's journal and seal its standalone Capsule
    /// under `action_id`, with `members` as further top-level members of
    /// that Capsule only (Go `SealSent`; the journal record's Capsule carries
    /// none: the record stays in the journal, and the members travel).
    /// `entry` carries exactly one payload, the record's canonical JSON
    /// (JCS) bytes, so the journal record's payload commitment and the
    /// Capsule's payload are the same bytes. The Capsule is sealed first and
    /// the journal record appended under the journal's lock.
    ///
    /// An `Err` left no journal record, and the call may be retried. A
    /// [`SealedSent`] whose `error` is set holds a record that was stored
    /// and whose log append is pending: it is in the journal, so the caller
    /// sends its Capsule and does not seal the record again.
    pub fn seal_sent(
        &self,
        entry: &BookEntry,
        members: &BTreeMap<String, Value>,
        action_id: &str,
    ) -> Result<SealedSent, ShelfError> {
        if action_id.is_empty() {
            return Err(ShelfError::Invalid(
                "a sent record needs an action_id".into(),
            ));
        }
        let [payload] = entry.payloads.as_slice() else {
            return Err(ShelfError::Invalid(
                "a sent record carries exactly one payload, its record".into(),
            ));
        };
        if !entry.members.is_empty() {
            return Err(ShelfError::Invalid(
                "a sent record's journal Capsule carries no members: its Capsule's are the members argument".into(),
            ));
        }
        if members.contains_key(RECORD_HEADER_MEMBER) {
            return Err(ShelfError::Invalid(format!(
                "a sent Capsule carries no {RECORD_HEADER_MEMBER}"
            )));
        }
        let decoded: Value = serde_json::from_slice(payload)
            .map_err(|e| ShelfError::Invalid(format!("a sent record's payload is JSON: {e}")))?;
        if canonical::jcs(&decoded).ok().as_deref() != Some(payload.as_slice()) {
            return Err(ShelfError::Invalid(
                "a sent record's payload is its canonical JSON (JCS) bytes".into(),
            ));
        }
        let journal = self.journal()?;
        let core = &journal.0;
        let mut state = core.lock();
        if state.released {
            return Err(ShelfError::Released);
        }
        let shared = self.shared();
        let developer = if shared.developer.is_empty() {
            DEFAULT_DEVELOPER
        } else {
            &shared.developer
        };
        let at = core.now().truncate(Duration::from_secs(1));
        let sealed = seal::seal_payload(
            action_id,
            &at.rfc3339(),
            payload,
            members,
            &Producer {
                operator: &shared.operator,
                developer,
                signer: shared.signer.as_ref(),
            },
        )
        .map_err(|e| match e {
            // An extension the Capsule cannot carry is the caller's input.
            seal::SealRecordError::Invalid(why) => {
                ShelfError::Invalid(format!("seal the sent record: {why}"))
            }
            other => ShelfError::Seal(other),
        })?;
        let capsule = SentCapsule {
            capsule_id: sealed.capsule_id,
            capsule: sealed.capsule,
            envelope: sealed.envelope,
            payload: payload.clone(),
        };
        let pending_before = state.pending.as_ref().map(|r| r.record_id.clone());
        match core.append_locked(&mut state, entry) {
            Ok(record) => Ok(SealedSent {
                record,
                capsule,
                error: None,
            }),
            Err(e) => match &state.pending {
                // Stored, but its log append failed: the journal keeps it
                // pending and commits it first on its next write or open, so
                // it is in the journal as surely as a committed one.
                Some(pending) if Some(&pending.record_id) != pending_before.as_ref() => {
                    Ok(SealedSent {
                        record: pending.clone(),
                        capsule,
                        error: Some(e),
                    })
                }
                _ => Err(e),
            },
        }
    }
}
