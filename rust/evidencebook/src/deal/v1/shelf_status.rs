// SPDX-License-Identifier: Apache-2.0

//! A v1 deal book's status on a shelf (design §8.1, §8.2; Go
//! `xdealv1`'s `StatusProvider`, `name_rulings.go` and
//! `StatusProvider.Report`): the entry that seals a record in its deal book
//! ([`book_entry`]), the naming of each counted ruling the holder's book
//! does not name yet ([`name_rulings`], [`name_all_rulings`]), the book
//! status a stamp asks for each book ([`DealStatus`]), and the report of a
//! deal book from the shelf ([`DealStatus::report`]).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::{Arc, Mutex};

use serde_json::{json, Value};

use super::book::{book_pair, verify_book};
use super::check::{check, encode, record_digest, ref_to};
use super::holder::{v1_payload, Payload, SENT_LOG_PREFIX};
use super::names::*;
use super::payment::{payment_check, PaymentInput};
use super::record::{Body, Record};
use super::report::{
    build_report_rows, Report, ReportBooks, ReportCheckpoint, ReportCopy, ReportCounterpartLog,
    ReportFact, ReportLog,
};
use super::report_bundle::ReportBook;
use super::sent::{payment_rows, verify_sent};
use super::status_value::{build_status, PaymentResult, StatusBooks, StatusRecord, StatusTime};
use super::types::*;
use crate::canonical::jcs;
use crate::deal::Error;
use crate::record::{EpistemicType, Link, LinkType};
use crate::shelf::{
    BookEntry, BookStatus, CountedPrefix, HeldRecord, Shelf, ShelfBook, ShelfError, StatusInput,
    StatusValues, Time, WeakShelf,
};

fn invalid(m: impl Into<String>) -> ShelfError {
    ShelfError::Invalid(m.into())
}

fn deal_err(e: Error) -> ShelfError {
    ShelfError::Invalid(e.to_string())
}

/// The entry that seals `r` in its deal book (design §3.1; Go
/// `BookEntry`): its JCS bytes as the one payload, whose digest is the
/// record digest; header record_type `deal_step`, the payload's own
/// record_type saying which x-deal-v1 type it is; the deal id as the
/// subject; and, past seq 1, a cites link to `prev_record_id`, the record
/// before it, which [`verify_book`] requires. Append it with
/// [`ShelfBook::append_at`] at `r`'s seq, and on [`ShelfError::Busy`] read
/// the book again and rebuild `r`: a receive names rulings in the same
/// books, so a plain append could seal `r` at a place other than the one it
/// names.
pub fn book_entry(r: &Record, prev_record_id: &str) -> Result<BookEntry, Error> {
    let (payload, _) = encode(&serde_json::to_vec(r)?)?;
    let mut e = BookEntry::new(crate::deal::STEP_RECORD_TYPE, EpistemicType::ProducerClaim);
    e.payloads = vec![payload];
    e.subject_ref = Some(r.block.deal_id.clone());
    if r.block.seq > 1 {
        if prev_record_id.is_empty() {
            return Err(Error::Invalid(
                "xdealv1: a deal-book record past seq 1 cites the record before it".into(),
            ));
        }
        e.links = vec![Link {
            link_type: LinkType::Cites,
            target: prev_record_id.into(),
        }];
    }
    Ok(e)
}

/// A received book record, verified, with its pair.
#[derive(Clone)]
struct HeldBook {
    rec: StatusRecord,
    pair: Vec<u8>,
}

/// The logs of `sender`'s records held for the deal whose first held
/// record is opened (`first`) as such a book's first record must be, in
/// log id order (Go `logsOf`).
fn logs_of(held: &[HeldRecord], sender: &str, first: impl Fn(&Record) -> bool) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for h in held {
        let r = &h.received;
        if r.sender_key != sender
            || r.position != 1
            || r.log_id.starts_with(SENT_LOG_PREFIX)
            || out.contains(&r.log_id)
        {
            continue;
        }
        if verify_book(&h.record).is_ok_and(|b| first(&b.record)) {
            out.push(r.log_id.clone());
        }
    }
    out.sort();
    out
}

/// `sender`'s counted prefix of `log_id`, as far as this deal's held
/// records reach it, with the prefix it walked (Go `countedWith`): from
/// position 1, each record the one held record that follows the last, up
/// to the prefix's size and before any fork. It stops, never fails, at a
/// counted record not held for the deal.
fn counted_with(
    shelf: &Shelf,
    held: &[HeldRecord],
    sender: &str,
    log_id: &str,
) -> Result<(Vec<HeldBook>, CountedPrefix), ShelfError> {
    let prefix = shelf.counted_prefix(sender, log_id)?;
    let mut limit = prefix.size;
    if prefix.fork_at > 0 && prefix.fork_at - 1 < limit {
        limit = prefix.fork_at - 1;
    }
    Ok((walk(held, sender, log_id, limit), prefix))
}

/// The walk of [`counted_with`] over the first `limit` positions.
fn walk(held: &[HeldRecord], sender: &str, log_id: &str, limit: u64) -> Vec<HeldBook> {
    let mut next: HashMap<&str, Vec<&HeldRecord>> = HashMap::new();
    for h in held {
        if h.received.sender_key == sender && h.received.log_id == log_id {
            next.entry(h.received.prev.as_str()).or_default().push(h);
        }
    }
    let mut out = Vec::new();
    let mut prev = String::new();
    for pos in 1..=limit {
        let Some([h]) = next.get(prev.as_str()).map(Vec::as_slice) else {
            break;
        };
        if h.received.position != pos {
            break;
        }
        let Ok(b) = verify_book(&h.record) else {
            break;
        };
        out.push(HeldBook {
            rec: StatusRecord {
                record: b.record,
                record_id: h.received.digest.clone(),
                time: StatusTime::None,
            },
            pair: h.record.clone(),
        });
        prev = h.received.digest.clone();
    }
    out
}

fn counted(
    shelf: &Shelf,
    held: &[HeldRecord],
    sender: &str,
    log_id: &str,
) -> Result<Vec<HeldBook>, ShelfError> {
    Ok(counted_with(shelf, held, sender, log_id)?.0)
}

fn is_agreement(r: &Record) -> bool {
    r.agree().is_some_and(|a| a.kind == KIND_AGREEMENT)
}

fn is_ruling(r: &Record) -> bool {
    r.block.record_type == TYPE_RULING
}

fn named_ruling(r: &Record) -> Option<&str> {
    match &r.body {
        Body::Statement(s) => s
            .names
            .as_ref()
            .and_then(|n| n.ruling.as_ref())
            .map(|d| d.digest.as_str()),
        _ => None,
    }
}

/// Whether `a` is an earlier naming time than `b` (Go `earlier`): a known
/// time before a stamp, else the earlier known time or the lower stamp.
fn earlier(a: &StatusTime, b: &StatusTime) -> bool {
    match (a, b) {
        (StatusTime::Known(x), StatusTime::Known(y)) => x < y,
        (StatusTime::Known(_), _) => true,
        (_, StatusTime::Known(_)) => false,
        (StatusTime::Stamp(x), StatusTime::Stamp(y)) => x < y,
        (StatusTime::Stamp(_), StatusTime::None) => false,
        (StatusTime::None, StatusTime::Stamp(y)) => 0 < *y,
        (StatusTime::None, StatusTime::None) => false,
    }
}

/// The rulings either book names arriving, in case book order, each timed
/// by the earliest naming (Go `namedRulings`).
fn named_rulings(
    rulings: &[StatusRecord],
    own: &[StatusRecord],
    theirs: &[StatusRecord],
) -> Result<Vec<StatusRecord>, ShelfError> {
    let mut named_at: HashMap<String, StatusTime> = HashMap::new();
    for r in own.iter().chain(theirs) {
        if let Some(d) = named_ruling(&r.record) {
            let keep = named_at.get(d).is_some_and(|t| !earlier(&r.time, t));
            if !keep {
                named_at.insert(d.to_string(), r.time.clone());
            }
        }
    }
    let mut out = Vec::new();
    for r in rulings {
        let d = record_digest(&r.record).map_err(deal_err)?;
        if let Some(t) = named_at.get(&d) {
            let mut r = r.clone();
            r.time = t.clone();
            out.push(r);
        }
    }
    Ok(out)
}

/// The rows whose first final ruling differs between two branches of the
/// adjudicator's case book the holder holds (Go `forkedFinals`).
fn forked_finals(held: &[HeldRecord], adjudicator: &str, log_id: &str) -> Vec<String> {
    struct Node {
        prev: String,
        ruling: Option<RulingBody>,
    }
    let mut nodes: HashMap<String, Node> = HashMap::new();
    let mut followed: HashSet<String> = HashSet::new();
    for h in held {
        if h.received.sender_key != adjudicator || h.received.log_id != log_id {
            continue;
        }
        let Ok(b) = verify_book(&h.record) else {
            continue;
        };
        let ruling = match b.record.body {
            Body::Ruling(rb) => Some(rb),
            _ => None,
        };
        nodes.insert(
            h.received.digest.clone(),
            Node {
                prev: h.received.prev.clone(),
                ruling,
            },
        );
        followed.insert(h.received.prev.clone());
    }
    let decision = |rb: &RulingBody| {
        serde_json::to_string(
            &json!({"value": rb.value, "against": rb.against, "remedy": rb.remedy}),
        )
        .unwrap_or_default()
    };
    let mut firsts: BTreeMap<String, HashSet<String>> = BTreeMap::new();
    for leaf in nodes.keys() {
        if followed.contains(leaf) {
            continue;
        }
        let mut chain: Vec<&Node> = Vec::new();
        let mut whole = false;
        let mut d = leaf.clone();
        while let Some(n) = nodes.get(&d) {
            chain.push(n);
            if n.prev.is_empty() {
                whole = true;
                break;
            }
            d = n.prev.clone();
        }
        // Only a branch from position 1 applies its own first final.
        if !whole {
            continue;
        }
        let mut seen: HashSet<&str> = HashSet::new();
        for n in chain.iter().rev() {
            let Some(rb) = &n.ruling else {
                continue;
            };
            if !rb.r#final || !seen.insert(rb.row.as_str()) {
                continue;
            }
            firsts
                .entry(rb.row.clone())
                .or_default()
                .insert(decision(rb));
        }
    }
    firsts
        .into_iter()
        .filter(|(_, ds)| ds.len() > 1)
        .map(|(row, _)| row)
        .collect()
}

/// The adjudicator's rulings of the deal either side has named arriving,
/// each timed by the earliest naming, in case book order; the rows whose
/// case book forked with different first final rulings; and how many
/// counted rulings neither book names (Go `caseBook`).
fn case_book(
    shelf: &Shelf,
    held: &[HeldRecord],
    adjudicator: &str,
    own: &[StatusRecord],
    theirs: &[StatusRecord],
) -> Result<(Vec<StatusRecord>, Vec<String>, usize), ShelfError> {
    let mut rulings = Vec::new();
    let mut conflicting = Vec::new();
    for log_id in logs_of(held, adjudicator, is_ruling) {
        rulings.extend(
            counted(shelf, held, adjudicator, &log_id)?
                .into_iter()
                .map(|r| r.rec),
        );
        conflicting.extend(forked_finals(held, adjudicator, &log_id));
    }
    conflicting.sort();
    conflicting.dedup();
    let out = named_rulings(&rulings, own, theirs)?;
    let unnamed = rulings.len() - out.len();
    Ok((out, conflicting, unnamed))
}

/// The holder's book as status records, and whether every record read as
/// x-deal-v1 (Go `readOwnBook`).
fn read_own_book(book: &ShelfBook) -> Result<Option<Vec<StatusRecord>>, ShelfError> {
    let mut own = Vec::new();
    for r in book.records() {
        match v1_payload(book, &r)? {
            Payload::V1(rec) => own.push(StatusRecord {
                record: *rec,
                record_id: r.record_id,
                time: StatusTime::None,
            }),
            _ => return Ok(None),
        }
    }
    Ok((!own.is_empty()).then_some(own))
}

/// The record digest of the first counted ruling of the deal by the
/// adjudicator, over every case book, that `own` does not name (Go
/// `firstUnnamed`).
fn first_unnamed(
    shelf: &Shelf,
    held: &[HeldRecord],
    adjudicator: &str,
    deal: &str,
    own: &[StatusRecord],
) -> Result<Option<String>, ShelfError> {
    let named: HashSet<&str> = own.iter().filter_map(|r| named_ruling(&r.record)).collect();
    for log_id in logs_of(held, adjudicator, is_ruling) {
        for r in counted(shelf, held, adjudicator, &log_id)? {
            if r.rec.record.block.deal_id != deal {
                continue;
            }
            let d = record_digest(&r.rec.record).map_err(deal_err)?;
            if !named.contains(d.as_str()) {
                return Ok(Some(d));
            }
        }
    }
    Ok(None)
}

/// The statement naming `ruling` as `own`'s next record, and the record id
/// it cites (Go `namingStatement`): seq after the book's last, prev that
/// record's digest, agreement_ref the agreement's, and seen the last record
/// of the counterparty's counted prefix held now.
fn naming_statement(
    shelf: &Shelf,
    held: &[HeldRecord],
    ag: &AgreeBody,
    deal: &str,
    own: &[StatusRecord],
    ruling: &str,
    at: Time,
) -> Result<(Record, String), ShelfError> {
    let mut seen = Value::Null;
    if let Some(key) = ag.counterparty_key.value() {
        if let Some(log) = logs_of(held, key, is_agreement).first() {
            if let Some(last) = counted(shelf, held, key, log)?.last() {
                seen = json!(last.rec.record_id);
            }
        }
    }
    let ag_digest = record_digest(&own[0].record).map_err(deal_err)?;
    let last = own.last().expect("a read book is not empty");
    let last_digest = record_digest(&last.record).map_err(deal_err)?;
    let doc = json!({
        PROFILE: {
            "profile": PROFILE, "canonicalization": CANONICALIZATION, "deal_id": deal,
            "record_type": TYPE_STATEMENT, "at": at.rfc3339(), "seq": own.len() + 1,
            "prev": ref_to(&last_digest), "agreement_ref": ref_to(&ag_digest), "seen": seen,
        },
        "body": {"statements": [], "names": {"ruling": ref_to(ruling)}},
    });
    let raw = jcs(&doc).map_err(|e| invalid(e.to_string()))?;
    check(&raw).map_err(deal_err)?;
    let r = super::record::decode(&raw).map_err(deal_err)?;
    Ok((r, last.record_id.clone()))
}

/// Seals the statements one deal book needs, each from a fresh read of the
/// book and of the counterparty's counted prefix, appended at the place it
/// was built for: a write in between makes it read again, up to three
/// times (Go `nameRulingsIn`). A book with a record not readable now as
/// x-deal-v1 is never written to.
fn name_rulings_in(
    shelf: &Shelf,
    book: &ShelfBook,
    ag: &AgreeBody,
    adjudicator: &str,
    deal: &str,
    at: Time,
) -> Result<Vec<String>, (Vec<String>, ShelfError)> {
    let mut out = Vec::new();
    let mut attempt = 0;
    loop {
        let own = match read_own_book(book) {
            Ok(Some(own)) => own,
            Ok(None) => return Ok(out),
            Err(e) => return Err((out, e)),
        };
        let step = || -> Result<Option<(Record, String)>, ShelfError> {
            let held = shelf.held_by_deal(deal)?;
            let Some(ruling) = first_unnamed(shelf, &held, adjudicator, deal, &own)? else {
                return Ok(None);
            };
            naming_statement(shelf, &held, ag, deal, &own, &ruling, at).map(Some)
        };
        let (r, prev) = match step() {
            Ok(Some(next)) => next,
            Ok(None) => return Ok(out),
            Err(e) => return Err((out, e)),
        };
        let entry = match book_entry(&r, &prev) {
            Ok(e) => e,
            Err(e) => return Err((out, deal_err(e))),
        };
        match book.append_at(r.block.seq as u64, &entry) {
            Ok(sealed) => {
                out.push(sealed.record_id);
                attempt = 0;
            }
            Err(ShelfError::Busy(_)) if attempt < 3 => attempt += 1,
            Err(e) => return Err((out, e)),
        }
    }
}

/// A ruling naming that failed (Go `NameRulings`'s error beside its ids):
/// the statements it sealed before the failure, and the failure.
#[derive(Debug, thiserror::Error)]
#[error("{error}")]
pub struct NamingError {
    /// The record ids sealed before the failure, in order.
    pub sealed: Vec<String>,
    pub error: ShelfError,
}

fn name_rulings_where(
    shelf: &Shelf,
    want: impl Fn(&str) -> bool,
    at: Time,
) -> Result<Vec<String>, NamingError> {
    let fail = |sealed: Vec<String>, error: ShelfError| NamingError { sealed, error };
    let mut sealed = Vec::new();
    let logs = shelf.books().map_err(|e| fail(Vec::new(), e))?;
    for log_id in logs {
        let book = match shelf.open(&log_id) {
            Ok(book) => book,
            Err(e) => return Err(fail(sealed, e)),
        };
        // The first record decides whether the book is one of these deals'.
        let Some(first) = book.record_by_seq(1) else {
            continue;
        };
        let rec = match v1_payload(&book, &first) {
            Ok(Payload::V1(rec)) => rec,
            Ok(_) => continue,
            Err(e) => return Err(fail(sealed, e)),
        };
        let Some(ag) = rec.agree().filter(|a| a.kind == KIND_AGREEMENT) else {
            continue;
        };
        let Some(adjudicator) = ag.adjudicator.value().map(|a| a.key_id.clone()) else {
            continue;
        };
        if !want(&rec.block.deal_id) {
            continue;
        }
        match name_rulings_in(shelf, &book, ag, &adjudicator, &rec.block.deal_id, at) {
            Ok(ids) => sealed.extend(ids),
            Err((ids, e)) => {
                sealed.extend(ids);
                return Err(fail(sealed, e));
            }
        }
    }
    Ok(sealed)
}

/// Seals, in the holder's deal book of each deal given, a statement naming
/// each counted ruling of that deal's adjudicator the book does not name
/// yet (PROFILE §5, "statement": a receiving library MUST; evidencebook#527;
/// Go `NameRulings`), returning the record ids it sealed, in order. A deal
/// the holder has no v1 deal book for, or whose agreement names no
/// adjudicator, is passed over. It takes the shelf's locks, so never call
/// it from a [`BookStatus`]'s values or from a receive profile's `read`,
/// `admit`, `answered` or `proof_sender`. A failure returns, beside its
/// error, the ids sealed before it ([`NamingError`]).
pub fn name_rulings(
    shelf: &Shelf,
    deal_ids: &[String],
    at: Time,
) -> Result<Vec<String>, NamingError> {
    if deal_ids.is_empty() {
        return Ok(Vec::new());
    }
    name_rulings_where(shelf, |d| deal_ids.iter().any(|w| w == d), at)
}

/// [`name_rulings`] over every v1 deal book the shelf holds (Go
/// `NameAllRulings`): [`DealStatus`]'s `before_stamp` runs it before each
/// stamp, since a ruling is often a deal's last record and nothing may
/// arrive to retry a naming that failed.
pub fn name_all_rulings(shelf: &Shelf, at: Time) -> Result<Vec<String>, NamingError> {
    name_rulings_where(shelf, |_| true, at)
}

/// Whether a stamp proof held from `sender` for `log_id` covers the
/// counterparty's agreement `agreement_id` (`ag`) in the sender's counted
/// prefix now (Go `proofCovers`). It takes no shelf lock.
pub(crate) fn proof_covers(
    shelf: &Shelf,
    sender: &str,
    log_id: &str,
    agreement_id: &str,
    ag: &AgreeBody,
) -> Result<bool, ShelfError> {
    Ok(shelf.held_proofs(sender, log_id)?.iter().any(|hp| {
        let v = &hp.verified;
        v.fact.book_id == agreement_id
            && v.key_id == ag.checkpoint_key
            && v.shelf_checkpoint.log_id == ag.shelf_log_id
            && hp.covers >= 1
    }))
}

/// The stamp a record's time is read at: the first stamp covering it, else
/// the stamp `n` being taken, which is the first to; none when no stamp is
/// being taken (`n` 0, a report), as Go's zero `StatusTime` is none.
fn stamp_time(first: Option<u64>, n: u64) -> StatusTime {
    match first {
        Some(s) => StatusTime::Stamp(s),
        None if n == 0 => StatusTime::None,
        None => StatusTime::Stamp(n),
    }
}

/// Gives the sent pair of the offer whose record digest is given, as the
/// host sent or received it, `None` when the host holds none: the library
/// keeps no sent Capsule.
pub type OfferPair = Arc<dyn Fn(&str) -> Result<Option<Vec<u8>>, String> + Send + Sync>;

/// A v1 deal book's status at each stamp (design §8.1, §8.2; Go's
/// `xdealv1.StatusProvider`): a [`BookStatus`] the shelf hands each book and
/// its journal, which reads the received books through the shelf's
/// lock-free readers only. Give it its shelf with [`DealStatus::set_shelf`]
/// once the shelf is open; it keeps a weak handle. `offer_pair`, when
/// given, lets a payment row's check run.
#[derive(Default)]
pub struct DealStatus {
    shelf: Mutex<Option<WeakShelf>>,
    offer_pair: Option<OfferPair>,
}

/// One snapshot of what a report rests on, with the book pair of each record
/// read, by record id.
type ReportSnapshot = (ReportBooks, BTreeMap<String, Vec<u8>>);

/// What a deal book's status is built from (Go `statusBooks`): its books, the
/// last received value, the digests the payment checks read, the
/// counterparty's counted prefix and the book pair of each record read, by
/// record id.
type StatusParts = (
    StatusBooks,
    Option<Value>,
    Vec<String>,
    Option<ReportCounterpartLog>,
    BTreeMap<String, Vec<u8>>,
);

/// A held record of the holder's own book, with its sealed pair.
struct OwnRecord {
    rec: StatusRecord,
    pair: Vec<u8>,
}

impl DealStatus {
    /// A status with no shelf yet; `offer_pair` gives a payment row's
    /// check its offer.
    pub fn new(offer_pair: Option<OfferPair>) -> Self {
        DealStatus {
            shelf: Mutex::new(None),
            offer_pair,
        }
    }

    /// The shelf the status serves, set once it is open.
    pub fn set_shelf(&self, shelf: &Shelf) {
        *self.shelf.lock().unwrap_or_else(|p| p.into_inner()) = Some(shelf.downgrade());
    }

    fn shelf(&self) -> Result<Shelf, ShelfError> {
        self.shelf
            .lock()
            .unwrap_or_else(|p| p.into_inner())
            .as_ref()
            .and_then(WeakShelf::upgrade)
            .ok_or_else(|| invalid("xdealv1: the deal status has no shelf"))
    }

    /// The holder's book read through its handle, `None` when it is not
    /// opened by a v1 agreement; a record whose payload is not readable now
    /// ends the read there (Go `ownBook`).
    fn own_book(&self, book: &ShelfBook) -> Result<Option<Vec<OwnRecord>>, ShelfError> {
        let mut out = Vec::new();
        for (i, r) in book.records().into_iter().enumerate() {
            let rec = match v1_payload(book, &r)? {
                Payload::V1(rec) => *rec,
                _ if i == 0 => return Ok(None),
                _ => break,
            };
            if i == 0 && !is_agreement(&rec) {
                return Ok(None);
            }
            let (payload, _) =
                encode(&serde_json::to_vec(&rec).map_err(|e| invalid(e.to_string()))?)
                    .map_err(deal_err)?;
            let pair =
                book_pair(&r.header_bytes, &r.capsule, &r.envelope, &payload).map_err(deal_err)?;
            out.push(OwnRecord {
                rec: StatusRecord {
                    record: rec,
                    record_id: r.record_id,
                    time: StatusTime::None,
                },
                pair,
            });
        }
        Ok(Some(out))
    }

    /// The offer whose record digest is `digest`: the holder's own, in its
    /// journal, or the one it received sent whole (Go `offer`).
    fn offer(
        &self,
        shelf: &Shelf,
        journal: &ShelfBook,
        digest: &str,
    ) -> Result<Record, ShelfError> {
        for r in journal.records() {
            if let Payload::V1(rec) = v1_payload(journal, &r)? {
                if rec.propose().is_some() && record_digest(&rec).is_ok_and(|d| d == digest) {
                    return Ok(*rec);
                }
            }
        }
        for h in shelf.held_first(&format!("{SENT_LOG_PREFIX}{digest}"))? {
            if let Ok(s) = verify_sent(&h.record, None) {
                if s.digest == digest {
                    return Ok(s.record);
                }
            }
        }
        Err(invalid(format!(
            "xdealv1: status: the deal's offer {digest} is not held"
        )))
    }

    /// The time of the holder's record at `seq`: its record time from a
    /// witness the agreement names, else the first stamp covering it, else
    /// this stamp (Go `ownTime`).
    fn own_time(
        shelf: &Shelf,
        input: &StatusInput<'_>,
        seq: u64,
        keys: &[String],
    ) -> Result<StatusTime, ShelfError> {
        if let Some(t) = shelf.record_time_by_key(input.book_id, seq, keys)? {
            return Ok(StatusTime::Known(t.rfc3339()));
        }
        Ok(stamp_time(shelf.record_stamp(input.book_id, seq)?, input.n))
    }

    /// The time the holder's stamps give the counterparty's record at `pos`
    /// of `log_id` (Go `receivedTime`).
    fn received_time(
        shelf: &Shelf,
        input: &StatusInput<'_>,
        log_id: &str,
        pos: u64,
        keys: &[String],
    ) -> Result<StatusTime, ShelfError> {
        if let Some(t) = shelf.received_record_time_by_key(input.book_id, log_id, pos, keys)? {
            return Ok(StatusTime::Known(t.rfc3339()));
        }
        Ok(stamp_time(
            shelf.received_record_stamp(input.book_id, log_id, pos)?,
            input.n,
        ))
    }

    /// What a v1 deal book's status value is built from, with its link
    /// value and the counterparty's counted prefix; `None` for a book that
    /// is not a v1 deal book (Go `statusBooks`).
    #[allow(clippy::type_complexity)]
    fn status_books(
        &self,
        shelf: &Shelf,
        input: &StatusInput<'_>,
    ) -> Result<Option<StatusParts>, ShelfError> {
        let Some(journal) = input.journal else {
            return Ok(None);
        };
        let Some(own) = self.own_book(input.book)? else {
            return Ok(None);
        };
        let Some(first) = own.first() else {
            return Ok(None);
        };
        let ag = first
            .rec
            .record
            .agree()
            .cloned()
            .expect("an agreement opens the book");
        let deal = first.rec.record.block.deal_id.clone();
        let offer = self.offer(shelf, journal, &ag.offer.digest)?;
        let keys: Vec<String> = ag.witness_list().iter().map(|w| w.key_id.clone()).collect();
        let mut own = own;
        for (i, o) in own.iter_mut().enumerate() {
            o.rec.time = Self::own_time(shelf, input, i as u64 + 1, &keys)?;
        }
        let held = shelf.held_by_deal(&deal)?;
        let mut b = StatusBooks {
            offer: offer.clone(),
            own: own.iter().map(|o| o.rec.clone()).collect(),
            counterpart: Vec::new(),
            case_book: Vec::new(),
            conflicting_final_rulings: Vec::new(),
            payments: BTreeMap::new(),
            proof_held: false,
            rulings_unnamed: 0,
        };
        let mut last_received = None;
        let mut theirs: Vec<HeldBook> = Vec::new();
        let mut counterpart_log = None;
        if let Some(key) = ag.counterparty_key.value() {
            if let Some(log) = logs_of(&held, key, is_agreement).first() {
                let (recs, prefix) = counted_with(shelf, &held, key, log)?;
                theirs = recs;
                for (i, t) in theirs.iter_mut().enumerate() {
                    t.rec.time = Self::received_time(shelf, input, log, i as u64 + 1, &keys)?;
                }
                if let Some(last) = theirs.last() {
                    last_received = Some(
                        json!({"log_id": log, "size": theirs.len(), "record_digest": last.rec.record_id}),
                    );
                    let their_ag = theirs[0]
                        .rec
                        .record
                        .agree()
                        .cloned()
                        .expect("an agreement opens their book");
                    b.proof_held =
                        proof_covers(shelf, key, log, &theirs[0].rec.record_id, &their_ag)?;
                    counterpart_log = Some(ReportCounterpartLog {
                        log_id: log.clone(),
                        counted: theirs.len() as u64,
                        last: last.rec.record_id.clone(),
                        fork_at: prefix.fork_at,
                    });
                }
            }
            b.counterpart = theirs.iter().map(|t| t.rec.clone()).collect();
        }
        if let Some(adj) = ag.adjudicator.value() {
            let (cases, conflicting, unnamed) =
                case_book(shelf, &held, &adj.key_id, &b.own, &b.counterpart)?;
            b.case_book = cases;
            b.conflicting_final_rulings = conflicting;
            b.rulings_unnamed = unnamed;
        }
        b.payments = self.payments(&offer, &ag, &own, &theirs)?;
        let mut checked_over: Vec<String> = b
            .payments
            .values()
            .flat_map(|r| r.checked_over.iter().cloned())
            .collect();
        checked_over.sort();
        checked_over.dedup();
        // The book pair of each record read, by record id: the holder's own
        // records and every record held for the deal (Go `statusBooks`).
        let mut pairs: BTreeMap<String, Vec<u8>> = own
            .iter()
            .map(|o| (o.rec.record_id.clone(), o.pair.clone()))
            .collect();
        for h in &held {
            pairs
                .entry(h.received.digest.clone())
                .or_insert_with(|| h.record.clone());
        }
        Ok(Some((
            b,
            last_received,
            checked_over,
            counterpart_log,
            pairs,
        )))
    }

    /// Each payment row's check over both books, when the host gives the
    /// offer's sent pair (Go `payments`).
    fn payments(
        &self,
        offer: &Record,
        ag: &AgreeBody,
        own: &[OwnRecord],
        theirs: &[HeldBook],
    ) -> Result<BTreeMap<String, PaymentResult>, ShelfError> {
        let mut out = BTreeMap::new();
        let Some(of) = offer.propose() else {
            return Ok(out);
        };
        let rows = payment_rows(&of.terms_rows);
        let Some(offer_pair) = &self.offer_pair else {
            return Ok(out);
        };
        if rows.is_empty() {
            return Ok(out);
        }
        let od = record_digest(offer).map_err(deal_err)?;
        let Some(pair) = offer_pair(&od).map_err(ShelfError::Profile)? else {
            return Ok(out);
        };
        let acceptor = if ag.side == SIDE_OFFERER {
            ag.counterparty_key.value().cloned().unwrap_or_default()
        } else {
            verify_book(&own[0].pair)
                .map(|b| b.key_id)
                .unwrap_or_default()
        };
        let legs: Vec<Vec<u8>> = own
            .iter()
            .map(|o| o.pair.clone())
            .chain(theirs.iter().map(|t| t.pair.clone()))
            .collect();
        for row in rows {
            let res = payment_check(&PaymentInput {
                offer: pair.clone(),
                offer_digest: od.clone(),
                acceptor_key: acceptor.clone(),
                row: row.clone(),
                legs: legs.clone(),
            });
            match res {
                Ok(r) => {
                    out.insert(row, r);
                }
                // No acceptor key known yet: not run.
                Err(e) if e.stage().is_some() && acceptor.is_empty() => {}
                Err(e) => return Err(deal_err(e)),
            }
        }
        Ok(out)
    }

    /// The report of the holder's v1 deal book `log_id` (design §8.2; Go
    /// `StatusProvider.Report`): its status computed now over the book's
    /// committed records as its latest checkpoint covers them, and whether
    /// the book's last status fact is that status. It refuses a book that
    /// is not a v1 deal book or holds a record not readable as one, a shelf
    /// with no journal (it never creates one), and a book that grew past its
    /// checkpoint or kept changing while it was read. It takes the shelf's
    /// lock, so never call it from a stamp or a receive.
    pub fn report(&self, log_id: &str, copy: &ReportCopy) -> Result<Report, ShelfError> {
        self.report_with_book(log_id, copy).map(|(r, _)| r)
    }

    /// [`report`](Self::report) with the book pairs the report rests on, read
    /// in the same snapshot (Go `StatusProvider.ReportWithBook`; design §9,
    /// "The bundle extension"): the holder's agreement at seq 1 and every
    /// record the report's rows rely on, whether the copy lists its rows or
    /// not, and, on a keep or counterparty copy, the offer's sent pair when
    /// the offer pair gives one. The host carries them as the x-deal-v1 bundle extension's book
    /// ([`ReportBook`](super::ReportBook)) beside the sealed report.
    pub fn report_with_book(
        &self,
        log_id: &str,
        copy: &ReportCopy,
    ) -> Result<(Report, ReportBook), ShelfError> {
        let shelf = self.shelf()?;
        let book = shelf.open(log_id)?;
        let not_v1 = || {
            invalid(format!(
                "xdealv1: report: book {log_id} is not an x-deal-v1 deal book"
            ))
        };
        let first = book.record_by_seq(1).ok_or_else(not_v1)?;
        match v1_payload(&book, &first)? {
            Payload::V1(rec) if is_agreement(&rec) => {}
            _ => return Err(not_v1()),
        }
        let journal = shelf.existing_journal()?.ok_or_else(|| {
            invalid("xdealv1: report: the shelf has no journal, which holds the deal's offer")
        })?;
        let book_id = first.record_id.clone();
        for _ in 0..3 {
            if let Some((rb, pairs)) =
                self.report_books(&shelf, log_id, &book_id, &book, &journal)?
            {
                // The offer the agreement cites, as the holder holds its
                // sent pair, for a keep or counterparty copy (u561): none
                // when the host gives no pair.
                let mut offer = None;
                if let Some(offer_pair) = &self.offer_pair {
                    if copy.audience != crate::deal::AUDIENCE_ADJUDICATOR {
                        let od = record_digest(&rb.books.offer).map_err(deal_err)?;
                        if let Some(p) = offer_pair(&od).map_err(ShelfError::Profile)? {
                            offer = Some(serde_json::from_slice(&p).map_err(|e| {
                                invalid(format!("xdealv1: report: the offer's sent pair: {e}"))
                            })?);
                        }
                    }
                }
                return report_with_book(&rb, copy, &pairs, offer);
            }
        }
        Err(ShelfError::Busy(format!(
            "report: book {log_id} kept changing while it was read"
        )))
    }

    /// One snapshot of what a report rests on; `None` when the book moved
    /// during the read (Go `reportBooks`).
    fn report_books(
        &self,
        shelf: &Shelf,
        log_id: &str,
        book_id: &str,
        book: &ShelfBook,
        journal: &ShelfBook,
    ) -> Result<Option<ReportSnapshot>, ShelfError> {
        let size = book.size();
        let cp = book
            .checkpoints()?
            .pop()
            .filter(|cp| cp.entries == size)
            .ok_or_else(|| {
                invalid(format!(
                    "xdealv1: report: book {log_id} has no checkpoint at its size {size}; a stamp checkpoints it"
                ))
            })?;
        let input = StatusInput {
            n: 0,
            book_id,
            log_id,
            book,
            journal: Some(journal),
        };
        let Some((b, _, _, counterpart, pairs)) = self.status_books(shelf, &input)? else {
            return Err(invalid(format!(
                "xdealv1: report: book {log_id} is not an x-deal-v1 deal book"
            )));
        };
        if book.size() != size {
            return Ok(None);
        }
        if b.own.len() as u64 != size {
            return Err(invalid(format!(
                "xdealv1: report: book {log_id} holds a record past seq {} that is not a readable x-deal-v1 record",
                b.own.len()
            )));
        }
        let fact = self.fact_current(shelf, book_id, &b)?;
        Ok(Some((
            ReportBooks {
                own: ReportLog {
                    log_id: log_id.into(),
                    size,
                    last: b
                        .own
                        .last()
                        .map(|r| r.record_id.clone())
                        .unwrap_or_default(),
                },
                books: b,
                stamps: None,
                counterpart,
                checkpoint: ReportCheckpoint {
                    log_id: log_id.into(),
                    mmr_size: cp.tree_size,
                    root: cp.root,
                },
                fact,
            },
            pairs,
        )))
    }

    /// The book's last status fact against the status value `b` builds now,
    /// compared in JCS form; `None` before the book's first fact (Go
    /// `factCurrent`).
    fn fact_current(
        &self,
        shelf: &Shelf,
        book_id: &str,
        b: &StatusBooks,
    ) -> Result<Option<ReportFact>, ShelfError> {
        let Some(last) = shelf
            .openings(book_id, "owner")?
            .into_iter()
            .filter_map(|f| f.status_opening)
            .next_back()
        else {
            return Ok(None);
        };
        let v = build_status(b).map_err(deal_err)?;
        let now = jcs(&serde_json::to_value(&v).map_err(|e| invalid(e.to_string()))?)
            .map_err(|e| invalid(e.to_string()))?;
        let was = jcs(&last.value).map_err(|e| {
            ShelfError::Corrupt(format!("book {book_id}'s last status opening: {e}"))
        })?;
        Ok(Some(ReportFact {
            current: now == was,
        }))
    }
}

impl BookStatus for DealStatus {
    /// A v1 deal book's values, `Ok(None)` for any other book (a
    /// journal-less shelf, a book not opened by a v1 agreement) (Go
    /// `BookValues`).
    fn values(&self, input: &StatusInput<'_>) -> Result<Option<StatusValues>, String> {
        let shelf = self.shelf().map_err(|e| e.to_string())?;
        let Some((b, last_received, checked_over, _, _)) = self
            .status_books(&shelf, input)
            .map_err(|e| e.to_string())?
        else {
            return Ok(None);
        };
        let v = build_status(&b).map_err(|e| e.to_string())?;
        Ok(Some(StatusValues {
            profile: PROFILE.into(),
            last_received,
            checked_over,
            status: Some(serde_json::to_value(&v).map_err(|e| e.to_string())?),
        }))
    }

    /// Names, before each stamp, every counted ruling the holder holds that
    /// its deal book does not name yet ([`name_all_rulings`]; Go
    /// `BeforeStamp`).
    fn before_stamp(&self, at: Time) -> Result<(), String> {
        let shelf = self.shelf().map_err(|e| e.to_string())?;
        name_all_rulings(&shelf, at)
            .map(drop)
            .map_err(|e| e.error.to_string())
    }
}

/// The report and its book from `pairs`, the book pair of each record the
/// snapshot read, by record id, and `offer`, the offer's sent pair a keep or
/// counterparty copy carries (Go `reportWithBook`).
fn report_with_book(
    rb: &ReportBooks,
    copy: &ReportCopy,
    pairs: &BTreeMap<String, Vec<u8>>,
    offer: Option<Value>,
) -> Result<(Report, ReportBook), ShelfError> {
    let (r, rows) = build_report_rows(rb, copy).map_err(deal_err)?;
    let pair = |id: &str| -> Result<Value, ShelfError> {
        let p = pairs.get(id).ok_or_else(|| {
            invalid(format!(
                "xdealv1: report: no book pair read for record {id}"
            ))
        })?;
        serde_json::from_slice(p)
            .map_err(|e| invalid(format!("xdealv1: report: the book pair of {id}: {e}")))
    };
    let agreement = pair(&rb.books.own[0].record_id)?;
    let mut relied_on = BTreeMap::new();
    for row in &rows {
        for src in &row.relied_on {
            relied_on.insert(src.record.clone(), pair(&src.record)?);
        }
    }
    let book = ReportBook {
        agreement: Some(agreement),
        relied_on: Some(relied_on),
        offer,
    };
    Ok((r, book))
}

#[cfg(test)]
mod tests {
    //! Go's `TestForkedFinals`, `TestRulingNamedByTheCounterparty` and
    //! `TestCountedStopsAtARecordNotHeld`, on book pairs sealed with an
    //! adjudicator's key and held as a receive would hold them.

    use super::*;
    use crate::retention::RetentionState;
    use crate::seal::{header, seal_record, Producer, DEFAULT_DEVELOPER};
    use crate::shelf::ReceivedRecord;
    use ed25519_dalek::SigningKey;
    use sha2::{Digest as _, Sha256};

    const LOG: &str = "case/0123456789abcdef0123456789abcdef";

    fn fixture(rel: &str) -> Value {
        let p = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../dealprofile/x-deal-v1/fixtures/appendix-a")
            .join(rel);
        let v: Value = serde_json::from_slice(&std::fs::read(p).unwrap()).unwrap();
        v["record"].clone()
    }

    fn signer() -> SigningKey {
        SigningKey::from_bytes(&Sha256::digest(b"adjudicator record key").into())
    }

    /// `record` sealed at `seq` of `log` by `signer`, citing `prev`: its
    /// record id and book pair.
    fn seal(
        signer: &SigningKey,
        log: &str,
        seq: u64,
        record: &Value,
        prev: &str,
    ) -> (String, Vec<u8>) {
        let payload = jcs(record).unwrap();
        check(&payload).unwrap_or_else(|e| panic!("{e}: {record}"));
        let entry = crate::ask::Entry {
            record_type: crate::deal::STEP_RECORD_TYPE.into(),
            epistemic_type: EpistemicType::ProducerClaim,
            links: if prev.is_empty() {
                Vec::new()
            } else {
                vec![Link {
                    link_type: LinkType::Cites,
                    target: prev.into(),
                }]
            },
            statement: Value::Null,
        };
        let mut h = header(log, seq, "2026-11-13T10:00:00Z", &entry).unwrap();
        h.payload_commitments = vec![hex::encode(Sha256::digest(&payload))];
        h.retention_state = Some(RetentionState::Available);
        let sealed = seal_record(
            &h,
            &Producer {
                operator: "adjudicator",
                developer: DEFAULT_DEVELOPER,
                signer,
            },
        )
        .unwrap();
        let pair = book_pair(&sealed.header, &sealed.capsule, &sealed.envelope, &payload).unwrap();
        (sealed.record_id, pair)
    }

    fn held(pair: Vec<u8>, key: &str, log: &str, pos: u64, prev: &str, digest: &str) -> HeldRecord {
        HeldRecord {
            received: ReceivedRecord {
                digest: digest.into(),
                sender_key: key.into(),
                log_id: log.into(),
                position: pos,
                prev: prev.into(),
                deal_id: "deal-6a1f0c93b27d4e58".into(),
                offer: false,
            },
            record: pair,
            received_at: Time::parse("2026-11-13T10:00:00Z").unwrap(),
        }
    }

    /// A ruling of `row`, as Go's test helper `ruling` builds it.
    fn ruling(row: &str, against: &str, value: &str, fin: bool, remedy: Option<&str>) -> Value {
        let mut r = fixture("case-book/01-ruling.json");
        let b = &mut r["body"];
        b["row"] = json!(row);
        b["against"] = if against.is_empty() {
            Value::Null
        } else {
            json!(against)
        };
        b["value"] = json!(value);
        b["final"] = json!(fin);
        b["read"] = json!({"buyer": null, "seller": null});
        match remedy {
            Some(kind) => b["remedy"] = json!({"kind": kind, "due": "2026-11-20T23:59:59Z"}),
            None => {
                b.as_object_mut().unwrap().remove("remedy");
            }
        }
        r
    }

    /// A case book sealed on two branches that share `common` from position
    /// 1 and then hold `left` and `right`, every record held once (Go
    /// `forkedCaseBook`).
    fn forked_case_book(
        common: &[Value],
        left: &[Value],
        right: &[Value],
    ) -> (Vec<HeldRecord>, String) {
        let key = signer();
        let key_id = hex::encode(key.verifying_key().to_bytes());
        let mut out: Vec<HeldRecord> = Vec::new();
        // Per branch: the last record and its record id.
        let mut last: [Option<(Value, String)>; 2] = [None, None];
        let mut add = |branch: usize, body: &Value, seq: u64, out: &mut Vec<HeldRecord>| {
            let mut r = body.clone();
            r["x-deal-v1"]["seq"] = json!(seq);
            let prev_id = match &last[branch] {
                Some((prev, id)) => {
                    let d =
                        record_digest(&super::super::record::decode(&jcs(prev).unwrap()).unwrap())
                            .unwrap();
                    r["x-deal-v1"]["prev"] = json!(ref_to(&d));
                    id.clone()
                }
                None => String::new(),
            };
            let (id, pair) = seal(&key, LOG, seq, &r, &prev_id);
            last[branch] = Some((r, id.clone()));
            if !out.iter().any(|h| h.received.digest == id) {
                out.push(held(pair, &key_id, LOG, seq, &prev_id, &id));
            }
        };
        let mut seq = 0;
        for c in common {
            seq += 1;
            add(0, c, seq, &mut out);
            add(1, c, seq, &mut out);
        }
        for (i, l) in left.iter().enumerate() {
            add(0, l, seq + i as u64 + 1, &mut out);
        }
        for (i, r) in right.iter().enumerate() {
            add(1, r, seq + i as u64 + 1, &mut out);
        }
        (out, key_id)
    }

    // A forked case book conflicts on a row only when two branches' first
    // final rulings differ in their decision; a run after a gap is no
    // branch.
    #[test]
    fn forked_finals_compare_first_final_decisions() {
        let (all, key) = forked_case_book(
            &[
                ruling("as_described", "seller", "not_met", true, None),
                ruling("delivered", "", "met", false, None),
                ruling("as_described", "buyer", "met", true, None),
            ],
            &[],
            &[],
        );
        let gapped: Vec<HeldRecord> = all
            .into_iter()
            .filter(|h| h.received.position != 2)
            .collect();
        assert!(
            forked_finals(&gapped, &key, LOG).is_empty(),
            "a run after a gap is not a branch"
        );
        let s = |r: &str, a: &str, v: &str, f: bool, m: Option<&str>| ruling(r, a, v, f, m);
        // name, common, left, right, the rows that conflict
        type Case<'a> = (&'a str, Vec<Value>, Vec<Value>, Vec<Value>, Vec<&'a str>);
        let cases: Vec<Case> = vec![
            (
                "a linear book",
                vec![s("as_described", "seller", "not_met", true, None)],
                vec![],
                vec![],
                vec![],
            ),
            (
                "against differs, value the same",
                vec![],
                vec![s("as_described", "seller", "not_met", true, None)],
                vec![s("as_described", "buyer", "not_met", true, None)],
                vec!["as_described"],
            ),
            (
                "remedy differs",
                vec![],
                vec![s("as_described", "seller", "not_met", true, Some("refund"))],
                vec![s(
                    "as_described",
                    "seller",
                    "not_met",
                    true,
                    Some("replace"),
                )],
                vec!["as_described"],
            ),
            (
                "a common first final, later finals differ",
                vec![s("as_described", "seller", "not_met", true, None)],
                vec![s("as_described", "buyer", "met", true, None)],
                vec![s("as_described", "", "met", true, None)],
                vec![],
            ),
            (
                "one branch has no final",
                vec![],
                vec![s("as_described", "seller", "not_met", true, None)],
                vec![s("as_described", "buyer", "met", false, None)],
                vec![],
            ),
            (
                "the same decision on both",
                vec![],
                vec![
                    s("as_described", "seller", "not_met", true, Some("refund")),
                    s("delivered", "", "met", false, None),
                ],
                vec![s("as_described", "seller", "not_met", true, Some("refund"))],
                vec![],
            ),
        ];
        for (name, common, left, right, want) in cases {
            let (held, key) = forked_case_book(&common, &left, &right);
            assert_eq!(forked_finals(&held, &key, LOG), want, "{name}");
        }
    }

    fn status_records(dir: &str, files: &[&str], known: bool) -> Vec<StatusRecord> {
        files
            .iter()
            .map(|f| {
                let v = fixture(&format!("{dir}/{f}"));
                let record = super::super::record::decode(&jcs(&v).unwrap()).unwrap();
                let time = if known {
                    StatusTime::Known(record.block.at.clone())
                } else {
                    StatusTime::None
                };
                StatusRecord {
                    record,
                    record_id: String::new(),
                    time,
                }
            })
            .collect()
    }

    // The side a ruling goes against cannot keep it off its own status by
    // not naming it: the counterparty's naming times it. Named by neither, a
    // ruling is not an input yet; the earliest naming wins.
    #[test]
    fn a_ruling_named_by_the_counterparty() {
        let rulings = status_records(
            "case-book",
            &["01-ruling.json", "02-ruling-final.json"],
            false,
        );
        let mut seller = status_records(
            "seller-book",
            &[
                "01-agreement.json",
                "02-payment-received.json",
                "03-delivery.json",
                "04-reply.json",
                "05-ruling-received.json",
                "06-final-ruling-received.json",
                "07-refund.json",
            ],
            true,
        );
        let mut dropped = 0;
        for r in &mut seller {
            if let Body::Statement(st) = &mut r.record.body {
                if st.names.as_ref().is_some_and(|n| n.ruling.is_some()) {
                    st.names = None;
                    dropped += 1;
                }
            }
        }
        assert!(dropped > 0, "the seller's book named the rulings");
        let buyer = status_records(
            "buyer-book",
            &[
                "01-agreement.json",
                "02-payment.json",
                "03-receipt.json",
                "04-not-as-described.json",
                "05-dispute.json",
                "06-ruling-received.json",
                "07-final-ruling-received.json",
                "08-refund-received.json",
            ],
            true,
        );
        let named = named_rulings(&rulings, &seller, &buyer).unwrap();
        assert_eq!(named.len(), 2, "the buyer named both");
        assert!(named.iter().all(|r| r.time != StatusTime::None));
        assert!(
            named_rulings(&rulings, &seller, &[]).unwrap().is_empty(),
            "named by neither"
        );
        let k = |t: &str| StatusTime::Known(t.into());
        assert!(earlier(&k("2026-11-20T00:00:00Z"), &StatusTime::Stamp(1)));
        assert!(earlier(
            &k("2026-11-19T00:00:00Z"),
            &k("2026-11-20T00:00:00Z")
        ));
        assert!(earlier(&StatusTime::Stamp(2), &StatusTime::Stamp(3)));
        assert!(!earlier(&StatusTime::Stamp(3), &k("2026-11-20T00:00:00Z")));
    }

    // The counted walk stops, never fails, at a counted record not held
    // for the deal.
    #[test]
    fn the_counted_walk_stops_at_a_record_not_held() {
        let key = signer();
        let key_id = hex::encode(key.verifying_key().to_bytes());
        let log = "deal/0123456789abcdef0123456789abcdef";
        let ag = fixture("seller-book/01-agreement.json");
        let next = fixture("seller-book/02-payment-received.json");
        let (ag_id, ag_pair) = seal(&key, log, 1, &ag, "");
        let (next_id, next_pair) = seal(&key, log, 2, &next, &ag_id);
        let all = vec![
            held(ag_pair, &key_id, log, 1, "", &ag_id),
            held(next_pair, &key_id, log, 2, &ag_id, &next_id),
        ];
        assert_eq!(walk(&all, &key_id, log, 2).len(), 2);
        let first: Vec<HeldRecord> = all
            .iter()
            .filter(|h| h.received.position == 1)
            .cloned()
            .collect();
        let part = walk(&first, &key_id, log, 2);
        assert_eq!(
            part.len(),
            1,
            "a counted record missing from the held records stops the walk"
        );
        assert_eq!(part[0].rec.record_id, ag_id);
        assert_eq!(
            walk(&all, &key_id, log, 1).len(),
            1,
            "the counted prefix's size bounds it"
        );
        assert_eq!(logs_of(&all, &key_id, is_agreement), vec![log.to_string()]);
    }

    #[test]
    fn a_time_with_no_stamp_taken_is_none() {
        assert_eq!(stamp_time(None, 0), StatusTime::None);
        assert_eq!(stamp_time(None, 4), StatusTime::Stamp(4));
        assert_eq!(stamp_time(Some(2), 0), StatusTime::Stamp(2));
    }
}
