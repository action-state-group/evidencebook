// SPDX-License-Identifier: Apache-2.0

//! The adjudicator's case book (PROFILE.md §4, "The case book"; Go
//! `case_book.go`): its log id, and the reader that finds a held ruling.

use sha2::{Digest as _, Sha256};

use super::names::PROFILE;
#[cfg(feature = "shelf")]
use super::{
    book::{verify_book, BookRead},
    check::record_digest,
    names::TYPE_RULING,
};
#[cfg(feature = "shelf")]
use crate::shelf::{Shelf, ShelfError};

/// Begins the log id of an adjudicator's case book (Go `CaseLogPrefix`).
pub const CASE_LOG_PREFIX: &str = "case/";

/// The tag that separates the case log id's hash from any other SHA-256
/// over the same inputs (Go `caseLogTag`: `Profile + "/case-log"`).
fn case_log_tag() -> String {
    format!("{PROFILE}/case-log")
}

/// The log id of the case book in which the adjudicator whose key id is
/// `adjudicator_key_id` seals its rulings of the deal whose agreements cite
/// the offer with record digest `offer_digest` (Go `CaseLogID`). It is one
/// book per deal and adjudicator, so both parties compute the same id and
/// refuse a ruling on any other log: a second, different ruling at the same
/// place is then a fork of this one log, which the status reads
/// (`conflicting_final_rulings`). The id is [`CASE_LOG_PREFIX`] and the
/// first 16 bytes, lowercase hex, of SHA-256 over the tag, the offer digest
/// and the key id, each followed by a zero byte. It does not contain the
/// deal id, and an offer is of one deal only, so two deals never share one.
pub fn case_log_id(offer_digest: &str, adjudicator_key_id: &str) -> String {
    let mut h = Sha256::new();
    for part in [case_log_tag().as_str(), offer_digest, adjudicator_key_id] {
        h.update(part.as_bytes());
        h.update([0u8]);
    }
    format!("{CASE_LOG_PREFIX}{}", hex::encode(&h.finalize()[..16]))
}

/// The ruling of the deal `deal_id` whose record digest is `digest`, sealed
/// by `adjudicator_key`, among the records the holder holds for that deal
/// (Go `HeldRuling`): a ruling a statement names is found by the digest the
/// statement holds. `None` means the ruling is unavailable to this holder:
/// it was never received, or it was deleted under retention. A reader shows
/// that as "ruling unavailable", never as no ruling.
#[cfg(feature = "shelf")]
pub fn held_ruling(
    shelf: &Shelf,
    deal_id: &str,
    adjudicator_key: &str,
    digest: &str,
) -> Result<Option<BookRead>, ShelfError> {
    for h in shelf.held_by_deal(deal_id)? {
        if h.received.sender_key != adjudicator_key
            || !h.received.log_id.starts_with(CASE_LOG_PREFIX)
        {
            continue;
        }
        let Ok(b) = verify_book(&h.record) else {
            continue;
        };
        if b.record.block.record_type != TYPE_RULING || b.record.block.deal_id != deal_id {
            continue;
        }
        if record_digest(&b.record).is_ok_and(|d| d == digest) {
            return Ok(Some(b));
        }
    }
    Ok(None)
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Go `TestCaseLogID`: one id per offer and adjudicator, the prefix and
    /// 32 hex characters, the parts separated, and the pinned value other
    /// implementations check theirs against.
    #[test]
    fn case_log_id_pin_and_shape() {
        let (offer, other) = ("a".repeat(64), "b".repeat(64));
        let (key, other_key) = ("1".repeat(64), "2".repeat(64));
        let id = case_log_id(&offer, &key);
        assert_eq!(id, case_log_id(&offer, &key));
        let hexpart = id.strip_prefix(CASE_LOG_PREFIX).unwrap();
        assert_eq!(hexpart.len(), 32);
        assert!(hexpart
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b)));
        assert_ne!(id, case_log_id(&other, &key), "another offer");
        assert_ne!(id, case_log_id(&offer, &other_key), "another adjudicator");
        assert_ne!(case_log_id("ab", "c"), case_log_id("a", "bc"));
        assert_eq!(id, "case/fe02461334fc17ef81a8494d07d28608");
    }
}
