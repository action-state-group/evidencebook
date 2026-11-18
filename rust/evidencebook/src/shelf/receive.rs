//! The store of what the other side pushes (Go `shelf_receive.go`;
//! evidencebook#461, `docs/design/461-shelf-facts.md` §3): the records a
//! counterparty, an adjudicator or an offerer sends, and the stamp proofs
//! that cover them. The crate knows no record format: a [`ReceiveProfile`]
//! reads each record (its signature, sender, log position, predecessor and
//! deal) and says who may push it. The store owns the rest: the duplicate,
//! the counted prefix and its gaps, forks and their evidence, the bounds,
//! the proofs, retention and tombstones, and the deal ids each call
//! changed. Nothing in the store is appended to a book, and the store needs
//! no shelf of book facts: it works on a version 1 shelf as on a migrated
//! one.
//!
//! Every state the calls report is recomputed from the tables (a restart
//! recomputes the same counted prefix). Two facts are kept rather than
//! derived, because deleting a record must not undo them: each conflict
//! found (a fork at a position, a contradicting checkpoint at a size), with
//! its one evidence row, and whether each held proof's root recomputed from
//! the records (`shelf_received_proofs.verified`).
//!
//! The readers ([`Shelf::counted_prefix`], [`Shelf::received_record_of`],
//! [`Shelf::evidence`]) take no shelf lock, so a [`super::BookStatus`]
//! callback or a [`ReceiveProfile`] may call them from inside a stamp or a
//! [`Shelf::receive`]. The store never calls the profile while it holds the
//! shelf's connection, so a reader there never waits on the store. A call
//! racing [`Shelf::release`] may fail with SQLite's error rather than
//! [`ShelfError::Released`].

use super::log::Checkpoint;
use super::stamp_proof::StampProof;
use super::store::write_tx;
use super::{Shelf, ShelfError, Time};
use cll::mmr;
use rusqlite::{params, Connection, OptionalExtension, Transaction};
use std::collections::btree_map::Entry;
use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

/// What a profile tells the store about received records (Go
/// `ReceiveProfile`). The store calls it outside the shelf's lock and
/// outside any transaction of its own, so it may read the shelf; it must
/// not call [`Shelf::receive`] or [`Shelf::forget`]. An `Err` is the
/// profile's own failure ([`ShelfError::Profile`]), except from `read`.
pub trait ReceiveProfile: Send + Sync {
    /// Check a received record's signature and return what the store needs
    /// of it. An error refuses the record as invalid.
    fn read(&self, record: &[u8]) -> Result<ReceivedRecord, String>;
    /// The push rule (§3, "who may push"): whether this sender may push this
    /// record now. An offer may come from any key; any other record only
    /// from a key a book on the shelf names as its counterparty or
    /// adjudicator, whose offer our side answered, or to which an offer in
    /// the journal was sent. Holding someone's offer grants nothing.
    fn admit(&self, record: &ReceivedRecord) -> Result<bool, String>;
    /// Whether our side has answered a stored offer (a journal reply or our
    /// agreement). The store asks about each offer it would drop before its
    /// write transaction, under a mark of the holder's own books (their
    /// records and log entries); if the mark moved by the time the
    /// transaction holds the write lock, an answer may have been committed
    /// in between, so it asks again ([`ShelfError::Busy`] after three
    /// tries). An answered offer leaves the offer bounds and is never
    /// dropped.
    fn answered(&self, offer: &ReceivedRecord) -> Result<bool, String>;
    /// Relate a stamp proof's key (the checkpoint key id that signed both
    /// its checkpoints) and the book log it names to the sender key its
    /// records are held under ([`ReceivedRecord::sender_key`]), and say
    /// whether that sender may push a proof now: `None` refuses the proof.
    fn proof_sender(&self, checkpoint_key_id: &str, log_id: &str)
        -> Result<Option<String>, String>;
}

/// What the store needs of a received record (Go `ReceivedRecord`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ReceivedRecord {
    /// The record's entry in its sender's book log (for a book record, its
    /// record id): 64 lowercase hex characters, unique across senders. A
    /// book checkpoint's root recomputes from these.
    pub digest: String,
    /// The key the sender signs its records with.
    pub sender_key: String,
    /// The sender's book log and the record's position in it, from 1.
    pub log_id: String,
    pub position: u64,
    /// The digest of the record at `position - 1` this one follows, `""`
    /// exactly when `position` is 1.
    pub prev: String,
    /// The deal the record belongs to: what [`Shelf::receive`] reports as
    /// changed, and what [`Shelf::forget`] deletes by. An unsolicited
    /// offer's deal id is the profile's to choose (its own id, say).
    pub deal_id: String,
    /// An unsolicited offer: one that belongs to no book the shelf holds.
    /// Offers have their own bounds ([`ReceiveBounds`]).
    pub offer: bool,
}

/// The shelf's store of received records (Go `ReceiveConfig`).
#[derive(Clone, Default)]
pub struct ReceiveConfig {
    /// Reads received records; [`Shelf::receive`] refuses every call
    /// without one.
    pub profile: Option<Arc<dyn ReceiveProfile>>,
    /// The store's bounds; a zero member takes its default.
    pub bounds: ReceiveBounds,
    /// How long a received record is kept once its deal is settled
    /// ([`Shelf::forget`]), the holder's own setting. Zero is the default:
    /// the life of the shelf, so nothing is ever deleted.
    pub retention: Duration,
}

/// The store's bounds (§3; Go `ReceiveBounds`). A zero member takes its
/// default ([`ReceiveBounds::DEFAULT`]).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct ReceiveBounds {
    /// A larger record is refused: 1 MiB.
    pub max_record_bytes: u64,
    /// A record past this many held from one sender and log (one book) is
    /// refused: 100000.
    pub max_records_per_log: u64,
    /// Unanswered unsolicited offers, per sender (16, 4 MiB) and in total
    /// (1024, 64 MiB). Reaching one drops the oldest unanswered offer
    /// first; an offer larger than a byte bound alone is refused.
    pub max_offers_per_sender: u64,
    pub max_offer_bytes_per_sender: u64,
    pub max_offers: u64,
    pub max_offer_bytes: u64,
    /// What unsolicited offers leave behind, per sender (16) and in total
    /// (1024) rows: the position of each dropped offer
    /// (`shelf_received_vacated`) and each conflict in a log that holds
    /// only offers (`shelf_received_conflicts`, with its evidence, at most
    /// two records' bytes). An offer belongs to no deal, so past either
    /// bound no row is kept: such a position may later be forgotten, and a
    /// fork there ends the counted prefix only while both its records are
    /// held. Rows [`Shelf::forget`] leaves and conflicts in a log holding
    /// any other record are kept for good and not counted.
    pub max_offer_evidence_per_sender: u64,
    pub max_offer_evidence: u64,
}

impl ReceiveBounds {
    /// The store's bounds when none is set (Go `DefaultReceiveBounds`).
    pub const DEFAULT: ReceiveBounds = ReceiveBounds {
        max_record_bytes: 1 << 20,
        max_records_per_log: 100_000,
        max_offers_per_sender: 16,
        max_offer_bytes_per_sender: 4 << 20,
        max_offers: 1024,
        max_offer_bytes: 64 << 20,
        max_offer_evidence_per_sender: 16,
        max_offer_evidence: 1024,
    };

    /// These bounds, each zero member replaced by its default.
    pub fn with_defaults(self) -> Self {
        let d = Self::DEFAULT;
        let or = |v: u64, def: u64| if v == 0 { def } else { v };
        Self {
            max_record_bytes: or(self.max_record_bytes, d.max_record_bytes),
            max_records_per_log: or(self.max_records_per_log, d.max_records_per_log),
            max_offers_per_sender: or(self.max_offers_per_sender, d.max_offers_per_sender),
            max_offer_bytes_per_sender: or(
                self.max_offer_bytes_per_sender,
                d.max_offer_bytes_per_sender,
            ),
            max_offers: or(self.max_offers, d.max_offers),
            max_offer_bytes: or(self.max_offer_bytes, d.max_offer_bytes),
            max_offer_evidence_per_sender: or(
                self.max_offer_evidence_per_sender,
                d.max_offer_evidence_per_sender,
            ),
            max_offer_evidence: or(self.max_offer_evidence, d.max_offer_evidence),
        }
    }
}

/// Two records a sender signed at one position: `a` holds the record held
/// first (or a deleted one's digest), `b` the other. A record whose
/// predecessor is another than the one held at that position is a fork
/// there too. Only the first conflicting pair at a position is kept.
pub const EVIDENCE_FORK: &str = "fork";
/// Two book checkpoints a sender signed for one log at one size with
/// different roots: `a` the one held, `b` the other. Only the first
/// contradiction at a size is kept.
pub const EVIDENCE_CHECKPOINT: &str = "checkpoint";
/// A book checkpoint whose root the records held from that sender and log
/// do not recompute: `a` the checkpoint, `b` the JSON array of the records'
/// digests it was recomputed over. Only the first at a size is kept.
pub const EVIDENCE_CHECKPOINT_RECORDS: &str = "checkpoint_records";

/// What one [`Shelf::receive`] call did, part by part (Go `ReceiveResult`).
#[derive(Debug, Default)]
pub struct ReceiveResult {
    /// The record part's outcome; `None` when no record was given.
    pub record: Option<RecordOutcome>,
    /// The proof part's outcome; `None` when no proof was given.
    pub proof: Option<ProofOutcome>,
    /// The deal ids whose rows the profile recomputes.
    pub changes: ReceiveChanges,
}

/// What became of a received record (Go `RecordOutcome`).
#[derive(Debug, Default)]
pub struct RecordOutcome {
    pub digest: String,
    /// Stored by this call.
    pub stored: bool,
    /// Already held: acknowledged, not stored again.
    pub duplicate: bool,
    /// Held once and deleted under the holder's retention: acknowledged,
    /// not stored again.
    pub deleted: bool,
    /// Held, within its sender's counted prefix.
    pub counted: bool,
    /// Held, past the counted prefix: the sender is asked for its book.
    pub ask_for_book: bool,
    /// Why the record was refused ([`ShelfError::Invalid`],
    /// [`ShelfError::Refused`], [`ShelfError::OverLimit`] or
    /// [`ShelfError::Busy`]); nothing of it is stored.
    pub refused: Option<ShelfError>,
}

/// What became of a received stamp proof (Go `ProofOutcome`).
#[derive(Debug, Default)]
pub struct ProofOutcome {
    /// A proof the store keeps: new, or the same book checkpoint under
    /// another shelf checkpoint, kept beside the first.
    pub accepted: bool,
    /// Already held: acknowledged, not stored again.
    pub duplicate: bool,
    /// Why the proof was refused ([`ShelfError::Invalid`] or
    /// [`ShelfError::Refused`]). A proof that contradicts a held one, or
    /// the records held, is refused and its evidence kept.
    pub refused: Option<ShelfError>,
}

/// The deal ids a [`Shelf::receive`] call changed, each list sorted (Go
/// `ReceiveChanges`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ReceiveChanges {
    /// Deals with records that entered the sender's counted prefix.
    pub grew: Vec<String>,
    /// Deals with records that left it.
    pub shrank: Vec<String>,
    /// Deals with new evidence of a fork or a contradicting checkpoint.
    pub forked: Vec<String>,
    /// Deals whose records gained coverage: a held stamp proof whose size
    /// they reach and whose root they recompute, when the proof arrives or
    /// when the records do. A proof past the records held grants none until
    /// then.
    pub covered: Vec<String>,
}

/// A [`Shelf::receive`] call that failed (boxed, as it carries the call's
/// result) (Go's non-nil error beside its
/// result): a part refused, or a store or profile error. `result` holds what
/// the call did: every part's outcome when only refusals failed it (each
/// refusal is in its outcome), the committed record part's outcome and
/// changes when the proof part failed, and nothing when the record part did.
#[derive(Debug)]
pub struct ReceiveError {
    pub result: ReceiveResult,
    /// The store's or the profile's error; `None` when the call failed only
    /// by its parts' refusals.
    pub error: Option<ShelfError>,
}

impl ReceiveError {
    fn of(error: ShelfError) -> Box<Self> {
        Box::new(Self {
            result: ReceiveResult::default(),
            error: Some(error),
        })
    }

    /// Every error the call reports: the store's or the profile's, else each
    /// part's refusal, the record's first (Go's joined error).
    pub fn errors(&self) -> Vec<&ShelfError> {
        if let Some(e) = &self.error {
            return vec![e];
        }
        let record = self.result.record.as_ref().and_then(|r| r.refused.as_ref());
        let proof = self.result.proof.as_ref().and_then(|p| p.refused.as_ref());
        record.into_iter().chain(proof).collect()
    }
}

impl fmt::Display for ReceiveError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let parts: Vec<String> = self.errors().iter().map(|e| e.to_string()).collect();
        f.write_str(&parts.join("\n"))
    }
}

impl std::error::Error for ReceiveError {}

/// The deal ids one part of a call changed.
#[derive(Default)]
pub(super) struct ChangeSet {
    grew: BTreeSet<String>,
    shrank: BTreeSet<String>,
    pub(super) forked: BTreeSet<String>,
    covered: BTreeSet<String>,
}

impl ChangeSet {
    pub(super) fn merge(&mut self, o: ChangeSet) {
        self.grew.extend(o.grew);
        self.shrank.extend(o.shrank);
        self.forked.extend(o.forked);
        self.covered.extend(o.covered);
    }

    fn result(&self) -> ReceiveChanges {
        let list = |s: &BTreeSet<String>| s.iter().cloned().collect();
        ReceiveChanges {
            grew: list(&self.grew),
            shrank: list(&self.shrank),
            forked: list(&self.forked),
            covered: list(&self.covered),
        }
    }
}

/// The stored time form, fixed width so rows sort by it (Go
/// `receivedTimeFormat`, `2006-01-02T15:04:05.000000000Z`).
pub(super) fn received_time(t: Time) -> String {
    let date = t.rfc3339();
    let nanos = t.unix_nanos().rem_euclid(1_000_000_000);
    format!("{}.{nanos:09}Z", &date[..date.len() - 1])
}

pub(super) fn parse_received_time(text: &str, what: &str) -> Result<Time, ShelfError> {
    Time::parse(text).ok_or_else(|| ShelfError::Corrupt(format!("{what}'s time {text:?}")))
}

/// A position, size or count as SQLite stores it: a signed 64-bit integer.
/// One past `i64::MAX` is refused, never wrapped.
pub(super) fn sql_int(v: u64) -> Result<i64, ShelfError> {
    i64::try_from(v).map_err(|_| ShelfError::Invalid(format!("{v} is past 2^63 - 1")))
}

/// An unsigned column: a negative value is out of range, never wrapped.
pub(super) fn unsigned(r: &rusqlite::Row<'_>, i: usize) -> rusqlite::Result<u64> {
    let v: i64 = r.get(i)?;
    u64::try_from(v).map_err(|_| rusqlite::Error::IntegralValueOutOfRange(i, v))
}

fn is_digest(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f'))
}

/// One sender's book log: (sender key, log id).
pub(super) type LogKey = (String, String);

/// Why a write transaction of the store did not commit.
pub(super) enum TxError {
    /// The holder's books changed since the offer plan (Go `errStaleMark`).
    Stale,
    /// A refusal, carried out of the transaction, which it rolls back.
    Refusal(ShelfError),
    Store(ShelfError),
}

impl From<ShelfError> for TxError {
    fn from(e: ShelfError) -> Self {
        TxError::Store(e)
    }
}

impl From<rusqlite::Error> for TxError {
    fn from(e: rusqlite::Error) -> Self {
        TxError::Store(e.into())
    }
}

/// A test's hook before a part's commit; `true` fails the commit.
#[cfg(test)]
pub(super) type CommitHook = Box<dyn Fn(&str) -> bool>;

#[cfg(test)]
thread_local! {
    /// When set (tests), runs at the end of each part's transaction, before
    /// its commit; `true` fails the commit (Go `receiveBeforeCommit`, whose
    /// test cancels the commit's context).
    pub(super) static BEFORE_COMMIT: std::cell::RefCell<Option<CommitHook>> =
        const { std::cell::RefCell::new(None) };
}

pub(super) fn before_commit(_part: &str) -> Result<(), TxError> {
    #[cfg(test)]
    {
        let fail = BEFORE_COMMIT.with(|h| h.borrow().as_ref().is_some_and(|f| f(_part)));
        if fail {
            return Err(TxError::Store(ShelfError::Invalid(
                "the commit was interrupted".into(),
            )));
        }
    }
    Ok(())
}

/// What a call compares before and after for one sender's log: its counted
/// prefix and how far a verified proof covers it.
pub(super) struct LogState {
    pub(super) prefix: PrefixState,
    coverage: u64,
}

pub(super) fn read_log_state(conn: &Connection, k: &LogKey) -> Result<LogState, ShelfError> {
    let prefix = received_prefix(conn, k)?;
    let coverage = verified_coverage(conn, k, &prefix)?;
    Ok(LogState { prefix, coverage })
}

/// Add what changed from `before` to `after` to `changes`: deals with
/// records that entered or left the counted prefix, and deals with counted
/// records the verified coverage newly reaches.
pub(super) fn report(before: &LogState, after: &LogState, changes: &mut ChangeSet) {
    let was: BTreeSet<&str> = before
        .prefix
        .counted
        .iter()
        .map(|c| c.digest.as_str())
        .collect();
    let now: BTreeSet<&str> = after
        .prefix
        .counted
        .iter()
        .map(|c| c.digest.as_str())
        .collect();
    for c in &after.prefix.counted {
        if !was.contains(c.digest.as_str()) {
            changes.grew.insert(c.deal.clone());
        }
    }
    for c in &before.prefix.counted {
        if !now.contains(c.digest.as_str()) {
            changes.shrank.insert(c.deal.clone());
        }
    }
    for i in before.coverage..after.coverage {
        changes
            .covered
            .insert(after.prefix.counted[i as usize].deal.clone());
    }
}

/// How many times [`Shelf::receive`] makes room for an offer while the
/// holder's books change under it before it refuses ([`ShelfError::Busy`]).
const OFFER_ATTEMPTS: u32 = 3;

/// A monotonic mark of the holder's own books (the journal and every deal
/// book): their records and log entries. Received records are not among
/// them.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
struct OwnMark {
    records: i64,
    entries: i64,
}

fn read_own_mark(conn: &Connection) -> Result<OwnMark, ShelfError> {
    let (records, logs): (i64, i64) = conn.query_row(
        "SELECT (SELECT COUNT(*) FROM book_records), (SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'cll_entries')",
        [],
        |r| Ok((r.get(0)?, r.get(1)?)),
    )?;
    let mut m = OwnMark {
        records,
        entries: 0,
    };
    // A file whose logs have no table yet has no entry.
    if logs > 0 {
        m.entries = conn.query_row(
            "SELECT COUNT(*) FROM cll_entries WHERE log_id IN (SELECT log_id FROM shelf_logs WHERE kind IN ('book', 'journal'))",
            [],
            |r| r.get(0),
        )?;
    }
    Ok(m)
}

/// A stored record's columns but its bytes.
#[derive(Clone, Debug)]
pub(super) struct ReceivedRow {
    digest: String,
    sender: String,
    log: String,
    position: u64,
    prev: String,
    deal: String,
    offer: bool,
}

impl ReceivedRow {
    pub(super) fn record(&self) -> ReceivedRecord {
        ReceivedRecord {
            digest: self.digest.clone(),
            sender_key: self.sender.clone(),
            log_id: self.log.clone(),
            position: self.position,
            prev: self.prev.clone(),
            deal_id: self.deal.clone(),
            offer: self.offer,
        }
    }

    pub(super) fn from_row(r: &rusqlite::Row<'_>) -> rusqlite::Result<Self> {
        Ok(Self {
            digest: r.get(0)?,
            sender: r.get(1)?,
            log: r.get(2)?,
            position: unsigned(r, 3)?,
            prev: r.get(4)?,
            deal: r.get(5)?,
            offer: r.get(6)?,
        })
    }
}

const RECEIVED_COLUMNS: &str = "digest, sender_key, log_id, position, prev, deal_id, offer";

/// What making room for an offer does: the offers the profile said were
/// answered, which leave the offer bounds, and the unanswered ones to drop,
/// oldest first.
#[derive(Default)]
struct OfferPlan {
    answered: Vec<String>,
    drop: Vec<ReceivedRow>,
}

/// One record of a counted prefix.
#[derive(Clone, Debug)]
pub(super) struct CountedRecord {
    digest: String,
    deal: String,
}

/// A sender's counted prefix in one log: the longest run of positions from
/// the first, each holding one record, before the first fork.
#[derive(Clone, Debug, Default)]
pub(super) struct PrefixState {
    /// The counted records, by position from 1.
    pub(super) counted: Vec<CountedRecord>,
    /// The lowest position with a fork, 0 for none: the prefix ends before
    /// it for good.
    fork_at: u64,
    /// The deal ids of every record held from the sender in the log.
    pub(super) deals: Vec<String>,
}

impl PrefixState {
    fn counts(&self, position: u64, digest: &str) -> bool {
        position >= 1
            && position <= self.counted.len() as u64
            && self.counted[(position - 1) as usize].digest == digest
    }

    pub(super) fn digests(&self, n: u64) -> Vec<String> {
        self.counted[..n as usize]
            .iter()
            .map(|c| c.digest.clone())
            .collect()
    }
}

/// A counted prefix from the store. The forks are the ones recorded
/// (`shelf_received_conflicts`), which deleting a record never undoes, and
/// any the records held show: a position whose claimed digests differ (the
/// records held there, and the predecessor each record at the next position
/// names).
pub(super) fn received_prefix(conn: &Connection, k: &LogKey) -> Result<PrefixState, ShelfError> {
    let held: Vec<ReceivedRow> = {
        let mut stmt = conn.prepare(&format!(
            "SELECT {RECEIVED_COLUMNS} FROM shelf_received WHERE sender_key = ? AND log_id = ? ORDER BY position, digest"
        ))?;
        let rows = stmt.query_map(params![k.0, k.1], ReceivedRow::from_row)?;
        rows.collect::<Result<_, _>>()?
    };
    let recorded: u64 = conn.query_row(
        "SELECT COALESCE(MIN(at), 0) FROM shelf_received_conflicts WHERE sender_key = ? AND log_id = ? AND kind = ?",
        params![k.0, k.1, EVIDENCE_FORK],
        |r| unsigned(r, 0),
    )?;
    let mut out = PrefixState {
        fork_at: recorded,
        ..Default::default()
    };
    let mut at: BTreeMap<u64, Vec<&ReceivedRow>> = BTreeMap::new();
    let mut claims: BTreeMap<u64, BTreeSet<&str>> = BTreeMap::new();
    let mut deals = BTreeSet::new();
    for r in &held {
        at.entry(r.position).or_default().push(r);
        claims.entry(r.position).or_default().insert(&r.digest);
        if r.position > 1 {
            claims.entry(r.position - 1).or_default().insert(&r.prev);
        }
        deals.insert(r.deal.clone());
    }
    for (p, c) in &claims {
        if c.len() > 1 && (out.fork_at == 0 || *p < out.fork_at) {
            out.fork_at = *p;
        }
    }
    let mut p = 1;
    while at.get(&p).is_some_and(|rows| rows.len() == 1) && (out.fork_at == 0 || p < out.fork_at) {
        let r = at[&p][0];
        out.counted.push(CountedRecord {
            digest: r.digest.clone(),
            deal: r.deal.clone(),
        });
        p += 1;
    }
    out.deals = deals.into_iter().collect();
    Ok(out)
}

/// A held record a new one forks from, at a position.
struct HeldFork {
    position: u64,
    /// The held record, or the digest of a deleted one.
    held: Vec<u8>,
    deal: String,
}

/// The held records `r` contradicts (§3): another record at its position,
/// or a record at the next position naming another predecessor; and, at
/// the position before, a record other than the predecessor `r` names, or
/// another record there naming another one. A record deleted at either
/// position (`shelf_received_vacated`) claims its digest there still, so
/// another record arriving there is a fork.
fn fork_evidence(tx: &Transaction<'_>, r: &ReceivedRecord) -> Result<Vec<HeldFork>, ShelfError> {
    let mut checks = vec![(r.position, r.digest.as_str())];
    if r.position > 1 {
        checks.push((r.position - 1, r.prev.as_str()));
    }
    let mut out = Vec::new();
    for (position, digest) in checks {
        // The records that claim a digest at `position`: those held there,
        // and those at the next position, by the predecessor they name.
        // The next position, -1 (no row) when it is past SQLite's range.
        let next = position
            .checked_add(1)
            .and_then(|n| i64::try_from(n).ok())
            .unwrap_or(-1);
        type Row = (String, String, i64, String, Vec<u8>);
        let rows: Vec<Row> = {
            let mut stmt = tx.prepare(
                "SELECT digest, prev, position, deal_id, record FROM shelf_received WHERE sender_key = ? AND log_id = ? AND position IN (?, ?) ORDER BY position, digest",
            )?;
            let rows = stmt.query_map(
                params![r.sender_key, r.log_id, sql_int(position)?, next],
                |row| {
                    Ok((
                        row.get(0)?,
                        row.get(1)?,
                        row.get(2)?,
                        row.get(3)?,
                        row.get(4)?,
                    ))
                },
            )?;
            rows.collect::<Result<_, _>>()?
        };
        let mut found = rows.into_iter().find_map(|(d, prev, at, deal, b)| {
            let claimed = if at == next { prev } else { d };
            (claimed != digest).then_some(HeldFork {
                position,
                held: b,
                deal,
            })
        });
        if found.is_none() {
            found = tx
                .query_row(
                    "SELECT digest, deal_id FROM shelf_received_vacated WHERE sender_key = ? AND log_id = ? AND position = ? AND digest != ? ORDER BY digest LIMIT 1",
                    params![r.sender_key, r.log_id, sql_int(position)?, digest],
                    |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
                )
                .optional()?
                .map(|(d, deal)| HeldFork {
                    position,
                    held: d.into_bytes(),
                    deal,
                });
        }
        out.extend(found);
    }
    Ok(out)
}

/// Whether one more row an unsolicited offer leaves behind fits the offer
/// bounds: the dropped offers' positions and the conflicts in logs of
/// offers alone, per sender and in total.
fn offer_room(tx: &Transaction<'_>, sender: &str, b: &ReceiveBounds) -> Result<bool, ShelfError> {
    let (mine, all): (i64, i64) = tx.query_row(
        "SELECT COALESCE(SUM(sender_key = ?), 0), COUNT(*) FROM (
            SELECT sender_key FROM shelf_received_vacated WHERE offer = 1
            UNION ALL SELECT sender_key FROM shelf_received_conflicts WHERE offer = 1)",
        [sender],
        |r| Ok((r.get(0)?, r.get(1)?)),
    )?;
    Ok((mine as u64) < b.max_offer_evidence_per_sender && (all as u64) < b.max_offer_evidence)
}

/// Record a conflict of one kind at a position or size of a sender's log the
/// first time only, with its evidence: a sender sending ever new conflicts
/// at one place adds no evidence. A conflict in a log of unsolicited offers
/// alone (`offers`) counts under the offer bounds; past them neither the
/// conflict nor its evidence is kept, so the counted prefix ends there only
/// while both records are held. It reports whether the conflict is new:
/// recorded by this call, or past the bounds.
#[allow(clippy::too_many_arguments)]
pub(super) fn note_conflict(
    tx: &Transaction<'_>,
    k: &LogKey,
    kind: &str,
    at: u64,
    a: &[u8],
    b: &[u8],
    now: &str,
    offers: bool,
    bounds: &ReceiveBounds,
) -> Result<bool, ShelfError> {
    let n: i64 = tx.query_row(
        "SELECT COUNT(*) FROM shelf_received_conflicts WHERE sender_key = ? AND log_id = ? AND kind = ? AND at = ?",
        params![k.0, k.1, kind, sql_int(at)?],
        |r| r.get(0),
    )?;
    if n > 0 {
        return Ok(false);
    }
    if offers && !offer_room(tx, &k.0, bounds)? {
        return Ok(true);
    }
    tx.execute(
        "INSERT INTO shelf_evidence (kind, a, b, noted_at) VALUES (?, ?, ?, ?)",
        params![kind, a, b, now],
    )?;
    let id = tx.last_insert_rowid();
    tx.execute(
        "INSERT INTO shelf_received_conflicts (sender_key, log_id, kind, at, evidence_id, offer) VALUES (?, ?, ?, ?, ?, ?)",
        params![k.0, k.1, kind, sql_int(at)?, id, offers],
    )?;
    Ok(true)
}

/// Proof states (`shelf_received_proofs.verified`): not yet reached by the
/// records held, its root recomputed from them, or contradicted by them.
pub(super) const PROOF_UNCHECKED: i64 = 0;
pub(super) const PROOF_RECOMPUTED: i64 = 1;
pub(super) const PROOF_CONTRADICTED: i64 = 2;

/// Remember the positions deleted records held (dropped offers, or records
/// [`Shelf::forget`] deleted), so another record arriving there later is a
/// fork, and return every proof of their logs that reaches a deleted
/// position to unchecked: its coverage needs the records again. A dropped
/// offer's row (`offers`) counts under the offer bounds, and past them is
/// not kept; a row forget leaves is kept for good.
fn vacate(
    tx: &Transaction<'_>,
    rows: &[ReceivedRow],
    offers: bool,
    bounds: &ReceiveBounds,
) -> Result<(), ShelfError> {
    let mut lowest: BTreeMap<LogKey, u64> = BTreeMap::new();
    for r in rows {
        let keep = !offers || offer_room(tx, &r.sender, bounds)?;
        if keep {
            tx.execute(
                "INSERT OR IGNORE INTO shelf_received_vacated (sender_key, log_id, position, digest, deal_id, offer) VALUES (?, ?, ?, ?, ?, ?)",
                params![r.sender, r.log, sql_int(r.position)?, r.digest, r.deal, offers],
            )?;
        }
        let low = lowest
            .entry((r.sender.clone(), r.log.clone()))
            .or_insert(r.position);
        *low = (*low).min(r.position);
    }
    for (k, position) in lowest {
        let sizes: Vec<u64> = {
            let mut stmt = tx.prepare(
                "SELECT DISTINCT size FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND verified = ?",
            )?;
            let rows = stmt.query_map(params![k.0, k.1, PROOF_RECOMPUTED], |r| unsigned(r, 0))?;
            rows.collect::<Result<_, _>>()?
        };
        for size in sizes {
            // Go's LeafCount reports 0 for a size that is no complete MMR.
            let leaves = mmr::leaf_count(size).unwrap_or(0);
            if leaves >= position {
                tx.execute(
                    "UPDATE shelf_received_proofs SET verified = ? WHERE sender_key = ? AND log_id = ? AND size = ?",
                    params![PROOF_UNCHECKED, k.0, k.1, sql_int(size)?],
                )?;
            }
        }
    }
    Ok(())
}

/// Check every unchecked held proof of `k` whose size the prefix now
/// reaches against the records: one whose root they recompute grants
/// coverage, one whose root they do not is kept as evidence and its deals
/// reported forked.
fn check_held_proofs(
    tx: &Transaction<'_>,
    k: &LogKey,
    p: &PrefixState,
    now: &str,
    changes: &mut ChangeSet,
) -> Result<(), ShelfError> {
    let proofs: Vec<(u64, Vec<u8>)> = {
        let mut stmt = tx.prepare(
            "SELECT DISTINCT size, book_checkpoint FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND verified = ? ORDER BY size",
        )?;
        let rows = stmt.query_map(params![k.0, k.1, PROOF_UNCHECKED], |r| {
            Ok((unsigned(r, 0)?, r.get(1)?))
        })?;
        rows.collect::<Result<_, _>>()?
    };
    for (size, statement) in proofs {
        let cp = Checkpoint::from_statement(&statement)?;
        if cp.entries > p.counted.len() as u64 {
            continue;
        }
        let (matched, over) = root_recomputes(&cp, &p.digests(cp.entries))?;
        let mut state = PROOF_RECOMPUTED;
        if !matched {
            state = PROOF_CONTRADICTED;
            let fresh = note_conflict(
                tx,
                k,
                EVIDENCE_CHECKPOINT_RECORDS,
                size,
                &statement,
                &over,
                now,
                false,
                &ReceiveBounds::default(),
            )?;
            if fresh {
                changes.forked.extend(p.deals.iter().cloned());
            }
        }
        tx.execute(
            "UPDATE shelf_received_proofs SET verified = ? WHERE sender_key = ? AND log_id = ? AND size = ?",
            params![state, k.0, k.1, sql_int(size)?],
        )?;
    }
    Ok(())
}

/// How many counted records of `k` a held proof whose root they recomputed
/// covers.
fn verified_coverage(conn: &Connection, k: &LogKey, p: &PrefixState) -> Result<u64, ShelfError> {
    let size: u64 = conn.query_row(
        "SELECT COALESCE(MAX(size), 0) FROM shelf_received_proofs WHERE sender_key = ? AND log_id = ? AND verified = ?",
        params![k.0, k.1, PROOF_RECOMPUTED],
        |r| unsigned(r, 0),
    )?;
    let leaves = mmr::leaf_count(size).map_err(|_| {
        ShelfError::Corrupt(format!("a held proof's size {size} is not a complete MMR"))
    })?;
    Ok(leaves.min(p.counted.len() as u64))
}

/// Whether records recompute a book checkpoint's root, given exactly its
/// entries' digests, and the digests as a JSON array.
pub(super) fn root_recomputes(
    cp: &Checkpoint,
    digests: &[String],
) -> Result<(bool, Vec<u8>), ShelfError> {
    let encoded = serde_json::to_vec(digests).map_err(|e| ShelfError::Corrupt(e.to_string()))?;
    let mut tree = mmr::MemoryNodeStore::new();
    for d in digests {
        let value: mmr::Hash = hex::decode(d)
            .ok()
            .and_then(|b| b.try_into().ok())
            .ok_or_else(|| ShelfError::Corrupt(format!("a held record's digest {d:?}")))?;
        mmr::add_leaf(&mut tree, mmr::leaf_hash(&value))
            .map_err(|e| ShelfError::Corrupt(e.to_string()))?;
    }
    let peaks = if cp.tree_size == 0 {
        Vec::new()
    } else {
        if cp.tree_size > mmr::NodeReader::size(&tree) {
            return Err(ShelfError::Corrupt(format!(
                "invalid historical MMR size {}",
                cp.tree_size
            )));
        }
        mmr::peaks(cp.tree_size)
            .map_err(|e| ShelfError::Corrupt(e.to_string()))?
            .into_iter()
            .map(|p| mmr::NodeReader::node(&tree, p))
            .collect()
    };
    Ok((
        hex::encode(mmr::root_from_peaks(&peaks)) == cp.root,
        encoded,
    ))
}

fn tombstoned(conn: &Connection, digest: &str) -> Result<bool, ShelfError> {
    let n: i64 = conn.query_row(
        "SELECT COUNT(*) FROM shelf_received_tombstones WHERE digest = ?",
        [digest],
        |r| r.get(0),
    )?;
    Ok(n > 0)
}

/// A sender's counted prefix in one book log, as the store computes it now
/// (Go `CountedPrefix`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CountedPrefix {
    /// How many records, from position 1, are counted.
    pub size: u64,
    /// The digest of the last counted record (`""` for none): what a
    /// record's seen member names.
    pub last: String,
    /// The lowest position with a fork, 0 for none; the prefix ends before
    /// it for good.
    pub fork_at: u64,
}

/// A received record the store holds (Go `HeldRecord`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HeldRecord {
    pub received: ReceivedRecord,
    /// The record's bytes as received.
    pub record: Vec<u8>,
    pub received_at: Time,
}

/// One row of the store's evidence (Go `Evidence`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Evidence {
    pub kind: String,
    pub a: Vec<u8>,
    pub b: Vec<u8>,
    pub noted_at: Time,
}

impl Shelf {
    /// The released check of the received store, without the shelf's lock.
    pub(super) fn live(&self) -> Result<(), ShelfError> {
        if self.shared().closed.load(Ordering::SeqCst) {
            return Err(ShelfError::Released);
        }
        Ok(())
    }

    pub(super) fn receive_now(&self) -> String {
        received_time(self.shared().now())
    }

    /// Run `f` in one IMMEDIATE write transaction, committing it unless `f`
    /// fails.
    pub(super) fn receive_tx<T>(
        &self,
        f: impl FnOnce(&Transaction<'_>) -> Result<T, TxError>,
    ) -> Result<T, TxError> {
        let mut db = self.shared().db();
        let tx = write_tx(&mut db)?;
        let v = f(&tx)?;
        tx.commit()?;
        Ok(v)
    }

    /// Take a pushed record, a stamp proof, or both (§3; Go `Receive`), and
    /// handle each part on its own, the record first: a record sent again
    /// with a proof it lacked the first time still delivers the proof. A part
    /// refused is reported in its outcome, and the call fails
    /// ([`ReceiveError`] with no `error`) with every part's outcome. Any
    /// other error is the store's or the profile's: one in the record part
    /// reports nothing, since nothing of it was committed; one in the proof
    /// part returns the record part's outcome and changes beside it. A call
    /// racing [`Shelf::release`] may fail with SQLite's error rather than
    /// [`ShelfError::Released`].
    pub fn receive(
        &self,
        record: Option<&[u8]>,
        proof: Option<&StampProof>,
    ) -> Result<ReceiveResult, Box<ReceiveError>> {
        let Some(profile) = self.shared().receive.profile.clone() else {
            return Err(ReceiveError::of(ShelfError::Invalid(
                "the shelf has no receive profile".into(),
            )));
        };
        if record.is_none() && proof.is_none() {
            return Err(ReceiveError::of(ShelfError::Invalid(
                "nothing to receive".into(),
            )));
        }
        let _serial = self
            .shared()
            .receive_mu
            .lock()
            .unwrap_or_else(|p| p.into_inner());
        self.live().map_err(ReceiveError::of)?;
        // Each part writes its changes to its own set, merged only once that
        // part's transaction committed.
        let mut changes = ChangeSet::default();
        let mut result = ReceiveResult::default();
        if let Some(record) = record {
            let mut part = ChangeSet::default();
            let out = self
                .receive_record(profile.as_ref(), record, &mut part)
                .map_err(ReceiveError::of)?;
            changes.merge(part);
            result.record = Some(out);
        }
        if let Some(proof) = proof {
            let mut part = ChangeSet::default();
            match self.receive_proof(profile.as_ref(), proof, &mut part) {
                Ok(out) => {
                    changes.merge(part);
                    result.proof = Some(out);
                }
                Err(e) => {
                    // The record part, if any, is committed: report it.
                    result.changes = changes.result();
                    return Err(Box::new(ReceiveError {
                        result,
                        error: Some(e),
                    }));
                }
            }
        }
        result.changes = changes.result();
        let refused = result.record.as_ref().is_some_and(|r| r.refused.is_some())
            || result.proof.as_ref().is_some_and(|p| p.refused.is_some());
        if refused {
            return Err(Box::new(ReceiveError {
                result,
                error: None,
            }));
        }
        Ok(result)
    }

    fn receive_record(
        &self,
        profile: &dyn ReceiveProfile,
        record: &[u8],
        changes: &mut ChangeSet,
    ) -> Result<RecordOutcome, ShelfError> {
        let bounds = self.shared().receive.bounds.with_defaults();
        let mut out = RecordOutcome::default();
        fn refuse(mut out: RecordOutcome, e: ShelfError) -> Result<RecordOutcome, ShelfError> {
            out.refused = Some(e);
            Ok(out)
        }
        if record.len() as u64 > bounds.max_record_bytes {
            return refuse(
                out,
                ShelfError::OverLimit(format!(
                    "a received record of {} bytes, over the store's {}",
                    record.len(),
                    bounds.max_record_bytes
                )),
            );
        }
        let r = match profile.read(record) {
            Ok(r) => r,
            Err(e) => return refuse(out, ShelfError::Invalid(format!("a received record: {e}"))),
        };
        if let Err(e) = validate(&r) {
            return refuse(out, ShelfError::Invalid(format!("a received record: {e}")));
        }
        out.digest = r.digest.clone();
        let key: LogKey = (r.sender_key.clone(), r.log_id.clone());

        let held: Option<Vec<u8>> = {
            let db = self.shared().db();
            if tombstoned(&db, &r.digest)? {
                out.deleted = true;
                return Ok(out);
            }
            db.query_row(
                "SELECT record FROM shelf_received WHERE digest = ?",
                [&r.digest],
                |row| row.get(0),
            )
            .optional()?
        };
        if let Some(held) = held {
            if held != record {
                let digest = r.digest.clone();
                return refuse(
                    out,
                    ShelfError::Invalid(format!("another record is held under digest {digest}")),
                );
            }
            out.duplicate = true;
            let p = received_prefix(&self.shared().db(), &key)?;
            out.counted = p.counts(r.position, &r.digest);
            out.ask_for_book = !out.counted;
            return Ok(out);
        }
        if !profile.admit(&r).map_err(ShelfError::Profile)? {
            let sender = r.sender_key.clone();
            return refuse(
                out,
                ShelfError::Refused(format!("{sender} may not push this record")),
            );
        }
        let now = self.receive_now();
        let mut attempt = 1;
        let done = loop {
            let mut plan = OfferPlan::default();
            let mut mark = OwnMark::default();
            if r.offer {
                // The profile is asked about the offers to drop before the
                // transaction, never inside it, under a mark of the holder's
                // own books: an answer committed after the mark changes it.
                mark = read_own_mark(&self.shared().db())?;
                match self.plan_offer_room(profile, &bounds, &r.sender_key, record.len() as u64)? {
                    Ok(p) => plan = p,
                    Err(refusal) => return refuse(out, refusal),
                }
            }
            let mut part = ChangeSet::default();
            match self
                .receive_record_tx(&r, record, &bounds, &plan, mark, &now, &mut out, &mut part)
            {
                Err(TxError::Stale) if attempt < OFFER_ATTEMPTS => attempt += 1,
                Err(TxError::Stale) => {
                    break Err(TxError::Refusal(ShelfError::Busy(
                        "the holder's books kept changing while an offer was being made room for"
                            .into(),
                    )))
                }
                Ok(()) => {
                    changes.merge(part);
                    break Ok(());
                }
                Err(e) => break Err(e),
            }
        };
        match done {
            Ok(()) => Ok(out),
            Err(TxError::Refusal(e)) => {
                out.stored = false;
                out.counted = false;
                out.ask_for_book = false;
                refuse(out, e)
            }
            Err(TxError::Store(e)) => Err(e),
            Err(TxError::Stale) => unreachable!("a stale mark is retried or refused as busy"),
        }
    }

    /// Store an admitted record in one write transaction, with the offer
    /// plan's drops.
    #[allow(clippy::too_many_arguments)]
    fn receive_record_tx(
        &self,
        r: &ReceivedRecord,
        record: &[u8],
        bounds: &ReceiveBounds,
        plan: &OfferPlan,
        mark: OwnMark,
        now: &str,
        out: &mut RecordOutcome,
        changes: &mut ChangeSet,
    ) -> Result<(), TxError> {
        let key: LogKey = (r.sender_key.clone(), r.log_id.clone());
        self.receive_tx(|tx| {
            if r.offer && read_own_mark(tx)? != mark {
                return Err(TxError::Stale);
            }
            let count: i64 = tx.query_row(
                "SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ?",
                params![r.sender_key, r.log_id],
                |row| row.get(0),
            )?;
            if count as u64 >= bounds.max_records_per_log {
                return Err(TxError::Refusal(ShelfError::OverLimit(format!(
                    "{count} records held from {} in {}, the store's bound",
                    r.sender_key, r.log_id
                ))));
            }
            let mut before: BTreeMap<LogKey, LogState> = BTreeMap::new();
            before.insert(key.clone(), read_log_state(tx, &key)?);
            for digest in &plan.answered {
                tx.execute(
                    "UPDATE shelf_received SET offer = 0 WHERE digest = ?",
                    [digest],
                )?;
            }
            for row in &plan.drop {
                let k = (row.sender.clone(), row.log.clone());
                if let Entry::Vacant(slot) = before.entry(k) {
                    let st = read_log_state(tx, slot.key())?;
                    slot.insert(st);
                }
                tx.execute("DELETE FROM shelf_received WHERE digest = ?", [&row.digest])?;
            }
            vacate(tx, &plan.drop, true, bounds)?;
            let forks = fork_evidence(tx, r)?;
            tx.execute(
                "INSERT INTO shelf_received (digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                params![r.digest, r.sender_key, r.log_id, sql_int(r.position)?, r.prev, r.deal_id, r.offer, record, now],
            )?;
            // A conflict in a log of offers alone counts under the offer
            // bounds; one in a log that holds, or held, any other record is
            // kept for good.
            let mut offers_only = false;
            if !forks.is_empty() && r.offer {
                let others: i64 = tx.query_row(
                    "SELECT (SELECT COUNT(*) FROM shelf_received WHERE sender_key = ? AND log_id = ? AND offer = 0) + (SELECT COUNT(*) FROM shelf_received_vacated WHERE sender_key = ? AND log_id = ? AND offer = 0)",
                    params![r.sender_key, r.log_id, r.sender_key, r.log_id],
                    |row| row.get(0),
                )?;
                offers_only = others == 0;
            }
            for f in &forks {
                let fresh = note_conflict(
                    tx,
                    &key,
                    EVIDENCE_FORK,
                    f.position,
                    &f.held,
                    record,
                    now,
                    offers_only,
                    bounds,
                )?;
                if fresh {
                    changes.forked.insert(r.deal_id.clone());
                    if !f.deal.is_empty() {
                        changes.forked.insert(f.deal.clone());
                    }
                }
            }
            for (k, was) in &before {
                let prefix = received_prefix(tx, k)?;
                // Held proofs the prefix now reaches are checked against it.
                check_held_proofs(tx, k, &prefix, now, changes)?;
                let after = read_log_state(tx, k)?;
                report(was, &after, changes);
                if *k == key {
                    out.counted = after.prefix.counts(r.position, &r.digest);
                }
            }
            out.stored = true;
            out.ask_for_book = !out.counted;
            before_commit("record")
        })
    }

    /// Plan the room for one more offer of `size` bytes from `sender` (§3;
    /// Go `planOfferRoom`): the oldest unanswered offers are dropped until it
    /// fits its sender's bounds and the total ones. Each offer it would drop
    /// is first put to the profile's `answered`, outside any transaction and
    /// with the shelf's connection free; an answered one leaves the bounds
    /// instead. Dropping an offer nobody answered is not a retention
    /// decision, so it leaves no tombstone. An offer larger than a byte bound
    /// alone is refused (the inner `Err`). Receive serializes every writer
    /// of the store, so the plan holds until its transaction applies it.
    fn plan_offer_room(
        &self,
        profile: &dyn ReceiveProfile,
        b: &ReceiveBounds,
        sender: &str,
        size: u64,
    ) -> Result<Result<OfferPlan, ShelfError>, ShelfError> {
        if size > b.max_offer_bytes_per_sender || size > b.max_offer_bytes {
            return Ok(Err(ShelfError::OverLimit(format!(
                "an offer of {size} bytes, over the store's offer bound"
            ))));
        }
        struct Held {
            row: ReceivedRow,
            size: u64,
            gone: bool,
        }
        let mut offers: Vec<Held> = {
            let db = self.shared().db();
            let mut stmt = db.prepare(&format!(
                "SELECT {RECEIVED_COLUMNS}, length(record) FROM shelf_received WHERE offer = 1 ORDER BY received_at, rowid"
            ))?;
            let rows = stmt.query_map([], |r| {
                Ok(Held {
                    row: ReceivedRow::from_row(r)?,
                    size: unsigned(r, 7)?,
                    gone: false,
                })
            })?;
            rows.collect::<Result<_, _>>()?
        };
        let mut plan = OfferPlan::default();
        let scopes: [(bool, u64, u64); 2] = [
            (true, b.max_offers_per_sender, b.max_offer_bytes_per_sender),
            (false, b.max_offers, b.max_offer_bytes),
        ];
        for (per_sender, max_count, max_bytes) in scopes {
            loop {
                let (mut count, mut bytes) = (0u64, 0u64);
                let mut oldest: Option<usize> = None;
                for (i, h) in offers.iter().enumerate() {
                    if !h.gone && (!per_sender || h.row.sender == sender) {
                        count += 1;
                        bytes += h.size;
                        oldest.get_or_insert(i);
                    }
                }
                if count < max_count && bytes + size <= max_bytes {
                    break;
                }
                let Some(i) = oldest else {
                    return Err(ShelfError::Corrupt(
                        "the offer bounds count offers the store does not list".into(),
                    ));
                };
                offers[i].gone = true;
                let row = offers[i].row.clone();
                if profile
                    .answered(&row.record())
                    .map_err(ShelfError::Profile)?
                {
                    plan.answered.push(row.digest);
                } else {
                    plan.drop.push(row);
                }
            }
        }
        Ok(Ok(plan))
    }

    /// A sender's counted prefix in one log, from the store (Go
    /// `CountedPrefix`). It takes no shelf lock: a [`super::BookStatus`]
    /// callback or a [`ReceiveProfile`] may call it.
    pub fn counted_prefix(
        &self,
        sender_key: &str,
        log_id: &str,
    ) -> Result<CountedPrefix, ShelfError> {
        self.live()?;
        let p = received_prefix(
            &self.shared().db(),
            &(sender_key.to_string(), log_id.to_string()),
        )?;
        Ok(CountedPrefix {
            size: p.counted.len() as u64,
            last: p
                .counted
                .last()
                .map(|c| c.digest.clone())
                .unwrap_or_default(),
            fork_at: p.fork_at,
        })
    }

    /// A received record by its digest (Go `ReceivedRecordOf`): one deleted
    /// under retention is [`ShelfError::Deleted`], one never held
    /// [`ShelfError::NotFound`]. It takes no shelf lock: a
    /// [`super::BookStatus`] callback or a [`ReceiveProfile`] may call it.
    pub fn received_record_of(&self, digest: &str) -> Result<HeldRecord, ShelfError> {
        self.live()?;
        let db = self.shared().db();
        let row = db
            .query_row(
                "SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at FROM shelf_received WHERE digest = ?",
                [digest],
                |r| {
                    Ok((
                        ReceivedRow::from_row(r)?,
                        r.get::<_, Vec<u8>>(7)?,
                        r.get::<_, String>(8)?,
                    ))
                },
            )
            .optional()?;
        let Some((row, record, at)) = row else {
            let deleted: Option<String> = db
                .query_row(
                    "SELECT deleted_at FROM shelf_received_tombstones WHERE digest = ?",
                    [digest],
                    |r| r.get(0),
                )
                .optional()?;
            return Err(match deleted {
                None => ShelfError::NotFound(format!("received record {digest}")),
                Some(at) => ShelfError::Deleted(format!("received record {digest}, at {at}")),
            });
        };
        Ok(HeldRecord {
            received: row.record(),
            record,
            received_at: parse_received_time(&at, &format!("received record {digest}"))?,
        })
    }

    /// The store's evidence of forks and contradicting checkpoints, oldest
    /// first (Go `Evidence`). It takes no shelf lock: a
    /// [`super::BookStatus`] callback or a [`ReceiveProfile`] may call it.
    pub fn evidence(&self) -> Result<Vec<Evidence>, ShelfError> {
        self.live()?;
        let rows: Vec<(String, Vec<u8>, Vec<u8>, String)> = {
            let db = self.shared().db();
            let mut stmt =
                db.prepare("SELECT kind, a, b, noted_at FROM shelf_evidence ORDER BY id")?;
            let rows = stmt.query_map([], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)))?;
            rows.collect::<Result<_, _>>()?
        };
        rows.into_iter()
            .map(|(kind, a, b, at)| {
                Ok(Evidence {
                    kind,
                    a,
                    b,
                    noted_at: parse_received_time(&at, "evidence")?,
                })
            })
            .collect()
    }

    /// Delete a deal's received records under the holder's retention (§3;
    /// Go `Forget`): only when the shelf's retention is set (by default
    /// nothing is ever deleted), only once `settled` reports the deal
    /// settled (every row agreed, uncontested, or ruled with its remedy
    /// done, as the row status function computes it), and only the records
    /// received at least the retention before `now`. Each deleted record
    /// leaves a tombstone, so reading it reports it deleted, not missing. The
    /// deal's proofs, evidence and the forks found stay: a fork's position
    /// still ends its sender's counted prefix, another record arriving at a
    /// deleted position is a fork there, and a proof reaching a deleted
    /// position grants no coverage until the records it was checked against
    /// are held again. It returns the digests deleted.
    pub fn forget(
        &self,
        deal_id: &str,
        now: Time,
        settled: impl FnOnce(&str) -> Result<bool, String>,
    ) -> Result<Vec<String>, ShelfError> {
        if deal_id.is_empty() {
            return Err(ShelfError::Invalid(
                "forget needs a deal id and its settled check".into(),
            ));
        }
        let retention = self.shared().receive.retention;
        if retention.is_zero() {
            return Err(ShelfError::Invalid(
                "the holder keeps received records for the life of the shelf".into(),
            ));
        }
        let _serial = self
            .shared()
            .receive_mu
            .lock()
            .unwrap_or_else(|p| p.into_inner());
        self.live()?;
        if !settled(deal_id).map_err(ShelfError::Profile)? {
            return Err(ShelfError::Invalid(format!(
                "deal {deal_id} is not settled; nothing of it is deleted"
            )));
        }
        let cutoff = received_time(now.add_nanos(-(retention.as_nanos() as i128)));
        let deleted_at = self.receive_now();
        let done = self.receive_tx(|tx| {
            let all: Vec<(ReceivedRow, String)> = {
                let mut stmt = tx.prepare(&format!(
                    "SELECT {RECEIVED_COLUMNS}, received_at FROM shelf_received WHERE deal_id = ? AND received_at <= ? ORDER BY digest"
                ))?;
                let rows = stmt.query_map(params![deal_id, cutoff], |r| {
                    Ok((ReceivedRow::from_row(r)?, r.get(7)?))
                })?;
                rows.collect::<Result<_, _>>()?
            };
            let mut deleted = Vec::with_capacity(all.len());
            for (r, at) in &all {
                tx.execute(
                    "INSERT INTO shelf_received_tombstones (digest, sender_key, received_at, deleted_at) VALUES (?, ?, ?, ?)",
                    params![r.digest, r.sender, at, deleted_at],
                )?;
                tx.execute("DELETE FROM shelf_received WHERE digest = ?", [&r.digest])?;
                deleted.push(r.digest.clone());
            }
            let gone: Vec<ReceivedRow> = all.into_iter().map(|(r, _)| r).collect();
            vacate(tx, &gone, false, &ReceiveBounds::default())?;
            Ok(deleted)
        });
        match done {
            Ok(deleted) => Ok(deleted),
            Err(TxError::Store(e) | TxError::Refusal(e)) => Err(e),
            Err(TxError::Stale) => unreachable!("forget plans no offer"),
        }
    }
}

/// What the store requires of a profile's reading (Go `validate`).
fn validate(r: &ReceivedRecord) -> Result<(), &'static str> {
    if !is_digest(&r.digest) {
        return Err("its digest is not 64 lowercase hex characters");
    }
    if r.sender_key.is_empty() || r.log_id.is_empty() || r.deal_id.is_empty() {
        return Err("it names no sender, log or deal");
    }
    if r.position == 0 {
        return Err("its position is 0; the first is 1");
    }
    if i64::try_from(r.position).is_err() {
        // SQLite stores a signed 64-bit integer; every language refuses a
        // position past it as invalid.
        return Err("its position is past 2^63 - 1");
    }
    if r.position == 1 && !r.prev.is_empty() {
        return Err("the first record names a predecessor");
    }
    if r.position > 1 && !is_digest(&r.prev) {
        return Err("it names no predecessor digest");
    }
    Ok(())
}
