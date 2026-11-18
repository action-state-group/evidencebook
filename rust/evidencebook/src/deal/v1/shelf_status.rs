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
    build_report, Report, ReportBooks, ReportCheckpoint, ReportCopy, ReportCounterpartLog,
    ReportFact, ReportLog,
};
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
    Ok((out, prefix))
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

fn name_rulings_where(
    shelf: &Shelf,
    want: impl Fn(&str) -> bool,
    at: Time,
) -> Result<Vec<String>, ShelfError> {
    let mut sealed = Vec::new();
    for log_id in shelf.books()? {
        let book = shelf.open(&log_id)?;
        // The first record decides whether the book is one of these deals'.
        let Some(first) = book.record_by_seq(1) else {
            continue;
        };
        let Payload::V1(rec) = v1_payload(&book, &first)? else {
            continue;
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
                return Err(e);
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
/// `admit`, `answered` or `proof_sender`.
pub fn name_rulings(
    shelf: &Shelf,
    deal_ids: &[String],
    at: Time,
) -> Result<Vec<String>, ShelfError> {
    if deal_ids.is_empty() {
        return Ok(Vec::new());
    }
    name_rulings_where(shelf, |d| deal_ids.iter().any(|w| w == d), at)
}

/// [`name_rulings`] over every v1 deal book the shelf holds (Go
/// `NameAllRulings`): [`DealStatus`]'s `before_stamp` runs it before each
/// stamp, since a ruling is often a deal's last record and nothing may
/// arrive to retry a naming that failed.
pub fn name_all_rulings(shelf: &Shelf, at: Time) -> Result<Vec<String>, ShelfError> {
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
        Ok(StatusTime::Stamp(
            shelf.record_stamp(input.book_id, seq)?.unwrap_or(input.n),
        ))
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
        Ok(StatusTime::Stamp(
            shelf
                .received_record_stamp(input.book_id, log_id, pos)?
                .unwrap_or(input.n),
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
    ) -> Result<
        Option<(
            StatusBooks,
            Option<Value>,
            Vec<String>,
            Option<ReportCounterpartLog>,
        )>,
        ShelfError,
    > {
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
        Ok(Some((b, last_received, checked_over, counterpart_log)))
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
            if let Some(rb) = self.report_books(&shelf, log_id, &book_id, &book, &journal)? {
                return build_report(&rb, copy).map_err(deal_err);
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
    ) -> Result<Option<ReportBooks>, ShelfError> {
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
        let Some((b, _, _, counterpart)) = self.status_books(shelf, &input)? else {
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
        Ok(Some(ReportBooks {
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
        }))
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
        let Some((b, last_received, checked_over, _)) = self
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
            .map_err(|e| e.to_string())
    }
}
