//! The read-only store a book's answers, reconcile inputs and indexes read.
//!
//! I-D sections: *The Commitment-Substrate Interface* (checkpoints, inclusion,
//! consistency) and *Record/Payload Separation* (records by id).
//!
//! A store is two traits, because the reference implementation keeps them in
//! two places: [`CheckpointedLog`] is the commitment side (stated checkpoints
//! and the proofs under them), which [`CllSubstrate`] implements over its own
//! files; [`RecordSource`] is the record side, which the book's record store
//! implements. A caller with its own checkpointer implements both over its own
//! index and hands them to [`crate::answer`], [`crate::reconcile`] and
//! [`crate::index`] without this crate opening, recovering or writing any
//! file of that caller's.
//!
//! **What a record's id is.** The log commits one entry per record, and the
//! store says which: a book commits the SHA-256 of the record's octets (the
//! default of [`RecordSource::entry_of`]); a capsule ledger commits each
//! line's `capsule_id`, which is not the SHA-256 of the line. Every lookup by
//! id ([`RecordSource::record_by_digest`], [`RecordSource::leaf_index_of`],
//! [`RecordSource::correlation`], [`RecordSource::citing`]) uses that committed entry, and every record an
//! answer serves is checked against it with [`RecordSource::entry_of`].
//!
//! Every method is read-only (`&self`) and none blocks on another writer: an
//! implementation answers from what it has already committed.
//!
//! [`CllSubstrate`]: crate::substrate::CllSubstrate

use crate::substrate::{StatedCheckpoint, SubstrateError};
use sha2::{Digest, Sha256};

/// One record, as a store serves it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Record {
    /// 0-based position in the log.
    pub leaf_index: u64,
    /// The entry the log commits for this record, 64 lowercase hex: the
    /// SHA-256 of `body` for a book, the `capsule_id` for a capsule ledger
    /// (see [`RecordSource::entry_of`]).
    pub digest: String,
    /// The record's octets.
    pub body: Vec<u8>,
}

/// The record side of a store.
pub trait RecordSource {
    /// The record whose committed entry is exactly `digest`.
    fn record_by_digest(&self, digest: &str) -> Option<Record>;
    /// The record at this leaf index.
    fn record_at(&self, leaf_index: u64) -> Option<Record>;
    /// The leaf index of the record whose committed entry is exactly
    /// `digest`, without reading it. The default reads it; an index that
    /// knows positions answers from them.
    fn leaf_index_of(&self, digest: &str) -> Option<u64> {
        self.record_by_digest(digest)
            .filter(|r| r.digest == digest)
            .map(|r| r.leaf_index)
    }
    /// The committed entries of the records carrying exactly this
    /// correlation identifier.
    fn correlation(&self, id: &str) -> Vec<String>;
    /// The committed entries of the records citing exactly this interaction
    /// half.
    fn citing(&self, half_digest: &str) -> Vec<String>;
    /// The entry the log commits for a record with these octets, recomputed
    /// from them; `None` when they are not a record of this store's format.
    /// The default is the book's: the SHA-256 of the octets, lowercase hex.
    fn entry_of(&self, body: &[u8]) -> Option<String> {
        Some(hex::encode(Sha256::digest(body)))
    }
}

/// Old peaks, each one's path into the new MMR, and the new peaks: what a
/// consistency proof between two checkpoint sizes is made of.
pub type ConsistencyParts = (Vec<[u8; 32]>, Vec<Vec<[u8; 32]>>, Vec<[u8; 32]>);

/// The commitment side of a store: its checkpoints and the proofs under them.
pub trait CheckpointedLog {
    /// Leaves committed so far, checkpointed or not.
    fn leaf_count(&self) -> Result<u64, SubstrateError>;

    /// Every checkpoint that carries its COSE wire statement and may anchor
    /// an answer, oldest first. Each one extends the one before it.
    fn stated_checkpoints(&self) -> Result<Vec<StatedCheckpoint>, SubstrateError>;

    /// The inclusion path (sibling hashes, leaf upward) of the leaf at
    /// `leaf_index` in the MMR of `mmr_size` nodes.
    fn inclusion_path(
        &self,
        leaf_index: u64,
        mmr_size: u64,
    ) -> Result<Vec<[u8; 32]>, SubstrateError>;

    /// The parts of the consistency proof from the MMR of `from` nodes to the
    /// MMR of `to` nodes.
    fn consistency_parts(&self, from: u64, to: u64) -> Result<ConsistencyParts, SubstrateError>;
}

#[cfg(test)]
mod tests {
    use super::*;

    struct NoRecords;

    impl RecordSource for NoRecords {
        fn record_by_digest(&self, _: &str) -> Option<Record> {
            None
        }
        fn record_at(&self, _: u64) -> Option<Record> {
            None
        }
        fn correlation(&self, _: &str) -> Vec<String> {
            Vec::new()
        }
        fn citing(&self, _: &str) -> Vec<String> {
            Vec::new()
        }
    }

    #[test]
    fn a_books_entry_is_the_sha256_of_the_octets() {
        assert_eq!(
            NoRecords.entry_of(b"abc").as_deref(),
            Some("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
        );
    }
}
