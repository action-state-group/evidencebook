// SPDX-License-Identifier: Apache-2.0

//! The holder's side of x-deal-v1 on a shelf (design §7.2, §8.3; Go
//! `receive.go`'s `ReceiveProfile` and `CanAct`, and `gate.go`'s
//! `GateAct`): the shelf's [`crate::shelf::ReceiveProfile`] for x-deal-v1,
//! which reads a received book pair or sent pair, holds EBL's push rule,
//! says whether the holder answered an offer and relates a stamp proof to
//! its sender; and the first-act gate.

use std::collections::HashMap;
use std::sync::Mutex;

use serde_json::Value;

use super::book::verify_book;
use super::case_book::{case_log_id, CASE_LOG_PREFIX};
use super::check::record_digest;
use super::names::*;
use super::record::{decode, Body, Record};
use super::sent::verify_sent;
use super::types::*;
use crate::shelf::{
    ReceiveProfile as ShelfReceiveProfile, ReceivedRecord, Shelf, ShelfBook, ShelfError, WeakShelf,
};

/// Begins the one-record log a sent journal record is held in:
/// `x-deal-v1/sent/<record digest>`. It hides the record's journal position.
pub const SENT_LOG_PREFIX: &str = "x-deal-v1/sent/";

/// The first act was refused: no held stamp proof covers the counterparty's
/// agreement.
pub const GATE_NO_PROOF: &str = "no_counterparty_proof";
/// The counterparty runs no layer: the act runs unguarded.
pub const GATE_NO_LAYER: &str = "counterparty_no_layer";

/// A received record read as either pair.
enum Read {
    Book(super::book::BookRead),
    Sent(super::sent::Sent),
}

impl Read {
    fn record(&self) -> &Record {
        match self {
            Read::Book(b) => &b.record,
            Read::Sent(s) => &s.record,
        }
    }
}

fn read_pair(record: &[u8]) -> Result<Read, String> {
    let members: Value = serde_json::from_slice(record).map_err(|e| format!("not a pair: {e}"))?;
    let Some(members) = members.as_object() else {
        return Err("not a pair: not a JSON object".into());
    };
    if members.contains_key("agent_input") {
        return verify_sent(record, None)
            .map(Read::Sent)
            .map_err(|e| e.to_string());
    }
    verify_book(record)
        .map(Read::Book)
        .map_err(|e| e.to_string())
}

/// The holder's own x-deal-v1 records: its journal's, in order, and the
/// agreement opening each of its deal books.
struct Ours {
    journal: Vec<Record>,
    agreements: Vec<Record>,
    /// The rulings opening each of our case books, when the holder is an
    /// adjudicator, by their log id.
    cases: HashMap<String, Record>,
}

impl Ours {
    /// Whether `sender` is a party of a deal whose agreement cites
    /// `offer_digest` and names `adjudicator_key`, by our case book of that
    /// deal: the case log of that offer and adjudicator is one of ours, and
    /// its first ruling, of the same deal, names `sender` among its keys. An
    /// adjudicator then takes the parties' book records after its ruling, to
    /// check each entered it (Go `caseParty`).
    fn case_party(
        &self,
        deal_id: &str,
        offer_digest: &str,
        adjudicator_key: &str,
        sender: &str,
    ) -> bool {
        let Some(r) = self.cases.get(&case_log_id(offer_digest, adjudicator_key)) else {
            return false;
        };
        r.block.deal_id == deal_id
            && match &r.body {
                Body::Ruling(rb) => rb.keys.buyer == sender || rb.keys.seller == sender,
                _ => false,
            }
    }

    /// Our journal's propose whose record digest is `d`: an offer or a
    /// counter-offer, whichever a reply cites.
    fn offer_by_digest(&self, d: &str) -> Option<(&ProposeBody, &Record)> {
        self.journal.iter().find_map(|r| {
            let b = r.propose()?;
            (record_digest(r).ok()? == d).then_some((b, r))
        })
    }

    /// The holder's own agreement of a deal, as sealed.
    fn agreement_record(&self, deal_id: &str) -> Option<&Record> {
        self.agreements.iter().find(|r| {
            r.agree().is_some_and(|a| a.kind == KIND_AGREEMENT) && r.block.deal_id == deal_id
        })
    }

    fn agreement(&self, deal_id: &str) -> Option<&AgreeBody> {
        self.agreements
            .iter()
            .filter(|r| r.block.deal_id == deal_id)
            .find_map(Record::agree)
    }
}

/// What a holder's book record carries.
pub(super) enum Payload {
    V1(Box<Record>),
    Other,
    /// Not readable now (withheld under retention, which may make it
    /// available again).
    Unavailable,
}

/// The ten typed record and contract types a typed v0 record's `type`
/// names (PROFILE.md §9).
const TYPED_VERSION_TYPES: &[&str] = &[
    crate::deal::TYPE_TASK_AUTHORITY,
    crate::deal::TYPE_PROPOSED_ACTION,
    crate::deal::TYPE_ACTION_EVALUATION,
    crate::deal::TYPE_ACTION_APPROVAL,
    crate::deal::TYPE_ACTION_RECORD,
    crate::deal::TYPE_ACTION_OUTCOME,
    crate::deal::TYPE_ACTION_REPORT,
    crate::deal::TYPE_CHECK_REQUEST,
    crate::deal::TYPE_CHECK_RESPONSE,
    crate::deal::TYPE_COMMERCIAL_BOUNDS,
];

/// Whether a payload's shape is an x-deal-v1 record (Go
/// `dealprofile.Version(raw) == xdealv1.Profile`): its one top-level member
/// named `x-deal-v<n>` is `x-deal-v1`, whose block's `profile` is
/// `x-deal-v1`, and its `type` is not a typed v0 type.
fn is_v1(raw: &[u8]) -> bool {
    let Ok(Value::Object(top)) = serde_json::from_slice::<Value>(raw) else {
        return false;
    };
    // Go's `^x-deal-v[0-9]+$`.
    let versioned = |k: &&String| {
        k.strip_prefix("x-deal-v")
            .is_some_and(|n| !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()))
    };
    let mut names = top.keys().filter(versioned);
    let (Some(only), None) = (names.next(), names.next()) else {
        return false;
    };
    let typed = top
        .get("type")
        .and_then(Value::as_str)
        .is_some_and(|t| TYPED_VERSION_TYPES.contains(&t));
    // Go reads the block's profile into a struct field, so its name matches
    // in any ASCII case, the later spelling winning.
    let profile = match &top[only] {
        Value::Object(block) => block
            .iter()
            .filter(|(k, _)| k.eq_ignore_ascii_case("profile"))
            .filter_map(|(_, v)| v.as_str())
            .next_back(),
        _ => None,
    };
    only == PROFILE && !typed && profile == Some(PROFILE)
}

/// The x-deal-v1 record a book record of the holder's carries (Go
/// `v1Payload`).
pub(super) fn v1_payload(
    book: &ShelfBook,
    r: &crate::shelf::BookRecord,
) -> Result<Payload, ShelfError> {
    let commitments = r
        .header
        .get("payload_commitments")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let [digest] = commitments.as_slice() else {
        return Ok(Payload::Other);
    };
    let raw = match book.resolve_payload(&r.record_id, digest.as_str().unwrap_or("")) {
        Ok(raw) => raw,
        Err(ShelfError::Unavailable(_)) => return Ok(Payload::Unavailable),
        Err(ShelfError::NotFound(_)) => return Ok(Payload::Other),
        Err(e) => return Err(e),
    };
    if !is_v1(&raw) {
        return Ok(Payload::Other);
    }
    Ok(decode(&raw).map_or(Payload::Other, |r| Payload::V1(Box::new(r))))
}

/// What the profile kept of the holder's records, so a call reads only what
/// is new: the journal records past those read, and the first record of a
/// deal book not read yet (Go `holderCache`). A book's first record never
/// changes once sealed. A payload not readable now is read again on the next
/// call, so a record withheld and made available again counts once it is.
#[derive(Default)]
struct Cache {
    journal_seen: u64,
    /// The v1 journal records read, by journal seq.
    journal: Vec<(u64, Record)>,
    /// Journal seqs whose payload was not readable.
    retry: Vec<u64>,
    /// By log id; `None` for a book that opens with no v1 agreement.
    firsts: HashMap<String, Option<Record>>,
}

#[derive(Default)]
struct State {
    /// The record [`ShelfReceiveProfile::read`] read last, by digest: the
    /// store asks `admit` about it before it holds it.
    last: Option<(String, Record)>,
    cache: Cache,
}

/// x-deal-v1's [`crate::shelf::ReceiveProfile`] (design §8.3; Go
/// `ReceiveProfile`). Give it the holder's shelf with
/// [`ReceiveProfile::set_shelf`] once the shelf is open: the profile reads
/// the holder's journal and deal books, and the store, through it. It keeps
/// a weak handle, so the shelf that owns the profile is not kept alive by
/// it. One profile serves one shelf.
///
/// Each side recomputes the other's book checkpoints from the records it
/// receives, so a deal book's log must hold the deal's records only: the
/// shelf gives deal books no authenticated indexes.
#[derive(Default)]
pub struct ReceiveProfile {
    shelf: Mutex<Option<WeakShelf>>,
    state: Mutex<State>,
}

/// The deal a first act is of, the record id of the counterparty's
/// agreement for it, and, when the counterparty has published an evidence
/// commitment, the shelf log id that commitment names (`""` when none is
/// held) (Go `CanActInput`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CanActInput {
    pub deal_id: String,
    pub counterpart_agreement: String,
    pub commitment_shelf_log: String,
}

/// What [`ReceiveProfile::gate_act`] reads (Go `ActGateInput`): `own` is
/// the holder's deal book before the act, in book order, opening with the
/// holder's agreement; `act` is the act to seal; the rest are
/// [`CanActInput`]'s.
#[derive(Clone, Debug, PartialEq)]
pub struct ActGateInput {
    pub own: Vec<Record>,
    pub act: Record,
    pub counterpart_agreement: String,
    pub commitment_shelf_log: String,
}

/// [`ReceiveProfile::gate_act`]'s answer (Go `ActGate`): `gated` says the
/// act is a first perform or payer settle, `allowed` that it may be sealed,
/// `unguarded` that it is allowed only because the counterparty runs no
/// layer, which the holder records; `reason` names why an act was refused
/// or is unguarded.
#[derive(Clone, Debug, Default, PartialEq, Eq, serde::Serialize)]
pub struct ActGate {
    pub gated: bool,
    pub allowed: bool,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub unguarded: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reason: String,
}

fn invalid(m: impl Into<String>) -> ShelfError {
    ShelfError::Invalid(m.into())
}

impl ReceiveProfile {
    /// A profile with no shelf yet.
    pub fn new() -> Self {
        Self::default()
    }

    /// The holder's shelf, set once it is open.
    pub fn set_shelf(&self, shelf: &Shelf) {
        *self.shelf.lock().unwrap_or_else(|p| p.into_inner()) = Some(shelf.downgrade());
    }

    fn shelf(&self) -> Result<Shelf, ShelfError> {
        self.shelf
            .lock()
            .unwrap_or_else(|p| p.into_inner())
            .as_ref()
            .and_then(WeakShelf::upgrade)
            .ok_or_else(|| invalid("xdealv1: the receive profile has no shelf"))
    }

    /// The holder's own x-deal-v1 records (Go `ours`), reading only what is
    /// new since the last call.
    fn ours(&self) -> Result<Ours, ShelfError> {
        let shelf = self.shelf()?;
        let mut state = self.state.lock().unwrap_or_else(|p| p.into_inner());
        let c = &mut state.cache;
        let journal = shelf.journal()?;
        // Read the new journal records, and those not readable before, into
        // a batch committed only once every read succeeded.
        let size = journal.size();
        let mut seqs = c.retry.clone();
        seqs.extend(c.journal_seen + 1..=size);
        let (mut added, mut retry) = (Vec::new(), Vec::new());
        for &n in &seqs {
            let Some(r) = journal.record_by_seq(n) else {
                continue;
            };
            match v1_payload(&journal, &r)? {
                Payload::V1(rec) => added.push((n, *rec)),
                Payload::Unavailable => retry.push(n),
                Payload::Other => {}
            }
        }
        if !seqs.is_empty() {
            for (n, rec) in added {
                let at = c.journal.partition_point(|(seq, _)| *seq < n);
                c.journal.insert(at, (n, rec));
            }
            c.journal_seen = size;
            c.retry = retry;
        }
        let mut o = Ours {
            journal: c.journal.iter().map(|(_, r)| r.clone()).collect(),
            agreements: Vec::new(),
            cases: HashMap::new(),
        };
        for log_id in shelf.books()? {
            let first = match c.firsts.get(&log_id) {
                Some(first) => first.clone(),
                None => {
                    let book = shelf.open(&log_id)?;
                    let (mut first, mut unavailable) = (None, false);
                    if let Some(r) = book.record_by_seq(1) {
                        match v1_payload(&book, &r)? {
                            Payload::V1(rec)
                                if rec.agree().is_some_and(|a| a.kind == KIND_AGREEMENT)
                                    || (rec.block.record_type == TYPE_RULING
                                        && log_id.starts_with(CASE_LOG_PREFIX)) =>
                            {
                                first = Some(*rec)
                            }
                            Payload::Unavailable => unavailable = true,
                            _ => {}
                        }
                    }
                    if !unavailable {
                        c.firsts.insert(log_id.clone(), first.clone());
                    }
                    first
                }
            };
            match first {
                Some(first) if first.block.record_type == TYPE_RULING => {
                    o.cases.insert(log_id, first);
                }
                Some(first) => o.agreements.push(first),
                None => {}
            }
        }
        Ok(o)
    }

    /// The record held in the one-record log of the sent record whose
    /// record digest is `digest`, and its sender (Go `heldSent`).
    fn held_sent(
        &self,
        shelf: &Shelf,
        digest: &str,
    ) -> Result<Option<(Record, String)>, ShelfError> {
        for h in shelf.held_first(&format!("{SENT_LOG_PREFIX}{digest}"))? {
            if let Ok(s) = verify_sent(&h.record, None) {
                if s.digest == digest {
                    return Ok(Some((s.record, h.received.sender_key)));
                }
            }
        }
        Ok(None)
    }

    /// The record held at position 1 of `log_id` from `sender_key`, read as
    /// a book pair; `None` when none, or a fork of two, is held (Go
    /// `heldGenesis`).
    fn held_genesis(
        &self,
        shelf: &Shelf,
        sender_key: &str,
        log_id: &str,
    ) -> Result<Option<Record>, ShelfError> {
        let mut out = None;
        for h in shelf.held_first(log_id)? {
            if h.received.sender_key != sender_key {
                continue;
            }
            if out.is_some() {
                return Ok(None);
            }
            match verify_book(&h.record) {
                Ok(b) => out = Some(b.record),
                Err(_) => return Ok(None),
            }
        }
        Ok(out)
    }

    /// The record `r` names: the one `read` read last, which the store does
    /// not hold yet when it asks `admit`, or the one it holds (Go
    /// `heldOrPending`).
    fn held_or_pending(
        &self,
        shelf: &Shelf,
        r: &ReceivedRecord,
    ) -> Result<Option<Record>, ShelfError> {
        let last = self
            .state
            .lock()
            .unwrap_or_else(|p| p.into_inner())
            .last
            .clone();
        if let Some((digest, rec)) = last {
            if digest == r.digest {
                return Ok(Some(rec));
            }
        }
        let h = match shelf.received_record_of(&r.digest) {
            Ok(h) => h,
            Err(ShelfError::NotFound(_) | ShelfError::Deleted(_)) => return Ok(None),
            Err(e) => return Err(e),
        };
        Ok(read_pair(&h.record).ok().map(|rd| rd.record().clone()))
    }

    fn admit_record(&self, r: &ReceivedRecord) -> Result<bool, ShelfError> {
        if r.offer {
            return Ok(true);
        }
        let shelf = self.shelf()?;
        let Some(rec) = self.held_or_pending(&shelf, r)? else {
            return Ok(false);
        };
        let o = self.ours()?;
        if r.log_id.starts_with(SENT_LOG_PREFIX) {
            let Some(ag) = rec.agree() else {
                return Ok(false);
            };
            if ag.kind == KIND_DECLINE {
                return Ok(o
                    .offer_by_digest(&ag.offer.digest)
                    .is_some_and(|(of, offer)| {
                        offer.block.deal_id == rec.block.deal_id
                            && of.to.value() == Some(&r.sender_key)
                    }));
            }
            if ag.kind == KIND_CANCELLATION {
                return Ok(self.held_sent(&shelf, &ag.offer.digest)?.is_some_and(
                    |(offer, signer)| {
                        signer == r.sender_key
                            && offer.block.record_type == TYPE_PROPOSE
                            && offer.block.deal_id == rec.block.deal_id
                    },
                ));
            }
            return Ok(false);
        }
        if rec.block.record_type == TYPE_RULING {
            // From our agreement's adjudicator, on the deal's one case book
            // (case_log_id), whoever carried it: a party forwards the ruling
            // it received, so a judge that gave each side another ruling
            // shows as a fork of that log.
            return Ok(o.agreement(&rec.block.deal_id).is_some_and(|a| {
                a.adjudicator
                    .value()
                    .is_some_and(|adj| adj.key_id == r.sender_key)
                    && r.log_id == case_log_id(&a.offer.digest, &r.sender_key)
            }));
        }
        // The log's first record from this sender: this record, or the one
        // held.
        let genesis = if r.position > 1 {
            match self.held_genesis(&shelf, &r.sender_key, &r.log_id)? {
                Some(g) => g,
                None => return Ok(false),
            }
        } else {
            rec
        };
        let Some(gag) = genesis
            .agree()
            .filter(|g| g.kind == KIND_AGREEMENT && genesis.block.deal_id == r.deal_id)
        else {
            return Ok(false);
        };
        if gag.adjudicator.value().is_some_and(|adj| {
            o.case_party(&r.deal_id, &gag.offer.digest, &adj.key_id, &r.sender_key)
        }) {
            // We are the deal's adjudicator and ruled on it: a party's book.
            return Ok(true);
        }
        if let Some(a) = o.agreement(&r.deal_id) {
            // The counterparty of our deal, on a book that answers the same
            // offer.
            return Ok(a.counterparty_key.value() == Some(&r.sender_key)
                && gag.offer.digest == a.offer.digest);
        }
        // At an offerer holding only its offer: the acceptor's book answers
        // it, from the key it was sent to.
        Ok(o.offer_by_digest(&gag.offer.digest)
            .is_some_and(|(of, offer)| {
                offer.block.deal_id == r.deal_id && of.to.value() == Some(&r.sender_key)
            }))
    }

    fn answered_offer(&self, offer: &ReceivedRecord) -> Result<bool, ShelfError> {
        let shelf = self.shelf()?;
        let h = shelf.received_record_of(&offer.digest)?;
        let Ok(s) = verify_sent(&h.record, None) else {
            return Ok(false);
        };
        let o = self.ours()?;
        if o.agreements
            .iter()
            .filter_map(Record::agree)
            .any(|a| a.offer.digest == s.digest)
        {
            return Ok(true);
        }
        Ok(o.journal.iter().any(|r| match &r.body {
            Body::Agree(a) => a.kind == KIND_DECLINE && a.offer.digest == s.digest,
            Body::Propose(b) => b.counter_to.as_ref().is_some_and(|c| c.digest == s.digest),
            _ => false,
        }))
    }

    fn proof_sender_of(
        &self,
        checkpoint_key_id: &str,
        log_id: &str,
    ) -> Result<Option<String>, ShelfError> {
        let shelf = self.shelf()?;
        let held = shelf.held_first(log_id)?;
        let mut first: HashMap<&str, &str> = HashMap::new();
        let mut forked: Vec<&str> = Vec::new();
        for h in &held {
            let key = h.received.sender_key.as_str();
            if first.get(key).is_some_and(|d| *d != h.received.digest) {
                forked.push(key);
            }
            first.insert(key, h.received.digest.as_str());
        }
        let o = self.ours()?;
        let mut candidates: Vec<String> = Vec::new();
        for h in &held {
            let key = &h.received.sender_key;
            if forked.contains(&key.as_str()) || candidates.contains(key) {
                continue;
            }
            let Ok(b) = verify_book(&h.record) else {
                continue;
            };
            if b.record.block.record_type == TYPE_RULING {
                // A case book: the adjudicator checkpoints it with the key
                // our agreement names it by, so the proof binds to that key.
                if o.agreement(&b.record.block.deal_id).is_some_and(|a| {
                    a.adjudicator.value().is_some_and(|adj| adj.key_id == *key)
                        && checkpoint_key_id == key
                        && log_id == case_log_id(&a.offer.digest, key)
                }) {
                    candidates.push(key.clone());
                }
                continue;
            }
            let Some(ag) = b
                .record
                .agree()
                .filter(|a| a.kind == KIND_AGREEMENT && a.checkpoint_key == checkpoint_key_id)
            else {
                continue;
            };
            let deal = &b.record.block.deal_id;
            let mut expected = match o.agreement(deal).and_then(|a| a.counterparty_key.value()) {
                Some(k) => Some(k.clone()),
                None => o
                    .offer_by_digest(&ag.offer.digest)
                    .filter(|(_, offer)| offer.block.deal_id == *deal)
                    .and_then(|(of, _)| of.to.value().cloned()),
            };
            if expected.is_none()
                && ag
                    .adjudicator
                    .value()
                    .is_some_and(|adj| o.case_party(deal, &ag.offer.digest, &adj.key_id, key))
            {
                expected = Some(key.clone());
            }
            if expected.as_ref() == Some(key) {
                candidates.push(key.clone());
            }
        }
        Ok(match candidates.as_slice() {
            [one] => Some(one.clone()),
            _ => None,
        })
    }

    /// The coverage gate before the holder's first act of a deal (design
    /// §7.2, gate B; Go `CanAct`): the counterparty's agreement must be of
    /// this deal, signed by the key the deal expects (our agreement's
    /// counterparty_key, or the key our offer was sent to) and citing the
    /// offer the deal answers; it must be in its sender's counted prefix as
    /// the store computes it now; and a held stamp proof bound to it must
    /// cover it: its fact's book id is the agreement's record id, its
    /// checkpoints are signed by the agreement's checkpoint_key, and its
    /// shelf log is the agreement's shelf_log_id and, when given, the log
    /// the counterparty's evidence commitment names. A deal whose
    /// counterparty runs no layer holds no counterpart agreement, so it is
    /// false there and [`ReceiveProfile::gate_act`] marks the act unguarded.
    pub fn can_act(&self, input: &CanActInput) -> Result<bool, ShelfError> {
        let shelf = self.shelf()?;
        let h = match shelf.received_record_of(&input.counterpart_agreement) {
            Ok(h) => h,
            Err(ShelfError::NotFound(_) | ShelfError::Deleted(_)) => return Ok(false),
            Err(e) => return Err(e),
        };
        let Ok(b) = verify_book(&h.record) else {
            return Ok(false);
        };
        if h.received.position != 1 {
            return Ok(false);
        }
        let Some(ag) = b
            .record
            .agree()
            .filter(|a| a.kind == KIND_AGREEMENT && b.record.block.deal_id == input.deal_id)
        else {
            return Ok(false);
        };
        let sender = &h.received.sender_key;
        let o = self.ours()?;
        let expected = match o.agreement(&input.deal_id) {
            Some(a) => {
                a.counterparty_key.value() == Some(sender) && ag.offer.digest == a.offer.digest
            }
            None => o
                .offer_by_digest(&ag.offer.digest)
                .is_some_and(|(of, offer)| {
                    offer.block.deal_id == input.deal_id && of.to.value() == Some(sender)
                }),
        };
        if !expected
            || (!input.commitment_shelf_log.is_empty()
                && input.commitment_shelf_log != ag.shelf_log_id)
        {
            return Ok(false);
        }
        let log_id = &h.received.log_id;
        if shelf.counted_prefix(sender, log_id)?.size < 1 {
            return Ok(false);
        }
        super::shelf_status::proof_covers(&shelf, sender, log_id, &input.counterpart_agreement, ag)
    }

    /// Applies the first-act gate (Steven, u610; design §7.2; Go
    /// `GateAct`) to an act the holder is about to seal: a first perform or
    /// payer settle of a deal is refused until the counterparty's stamp proof
    /// covers its agreement ([`ReceiveProfile::can_act`]). A payee leg and a
    /// statement are never gated, nor is any act after the first. A deal
    /// whose counterparty runs no layer lets its first act go ahead,
    /// unguarded (u611). It refuses input that is not the holder's book of
    /// the act's deal: an empty book, one that does not open with the
    /// agreement the holder has sealed for that deal, or an act or record of
    /// another deal.
    pub fn gate_act(&self, input: &ActGateInput) -> Result<ActGate, ShelfError> {
        let Some(head) = input.own.first() else {
            return Err(invalid("xdealv1: gate: the holder's book is empty"));
        };
        let Some(ag) = head.agree().filter(|a| a.kind == KIND_AGREEMENT) else {
            return Err(invalid(
                "xdealv1: gate: the holder's book does not open with its agreement",
            ));
        };
        let deal = &head.block.deal_id;
        // The flags read below are the holder's own agreement's: the
        // counterparty's says whether the holder runs the layer, the reverse.
        let o = self.ours()?;
        let Some(mine) = o.agreement_record(deal) else {
            return Err(invalid(format!(
                "xdealv1: gate: the holder has sealed no agreement of deal {deal:?}"
            )));
        };
        let digest = |r: &Record| record_digest(r).map_err(|e| invalid(e.to_string()));
        if digest(head)? != digest(mine)? {
            return Err(invalid(format!(
                "xdealv1: gate: the book does not open with the holder's own agreement of deal {deal:?}"
            )));
        }
        if input.act.block.deal_id != *deal {
            return Err(invalid(format!(
                "xdealv1: gate: the act is of deal {:?}, the book of {deal:?}",
                input.act.block.deal_id
            )));
        }
        let mut acted = false;
        for r in &input.own[1..] {
            if r.block.deal_id != *deal {
                return Err(invalid(format!(
                    "xdealv1: gate: a record of deal {:?} in the book of {deal:?}",
                    r.block.deal_id
                )));
            }
            acted = acted || gated_act(r);
        }
        if !gated_act(&input.act) || acted {
            return Ok(ActGate {
                allowed: true,
                ..Default::default()
            });
        }
        if ag.counterparty_layer != Some(true) {
            return Ok(ActGate {
                gated: true,
                allowed: true,
                unguarded: true,
                reason: GATE_NO_LAYER.into(),
            });
        }
        let covered = self.can_act(&CanActInput {
            deal_id: deal.clone(),
            counterpart_agreement: input.counterpart_agreement.clone(),
            commitment_shelf_log: input.commitment_shelf_log.clone(),
        })?;
        Ok(if covered {
            ActGate {
                gated: true,
                allowed: true,
                ..Default::default()
            }
        } else {
            ActGate {
                gated: true,
                reason: GATE_NO_PROOF.into(),
                ..Default::default()
            }
        })
    }
}

/// Whether `r` is an act the gate holds: a perform, or a payer leg.
fn gated_act(r: &Record) -> bool {
    match &r.body {
        Body::Perform(_) => true,
        Body::Settle(s) => s.leg == LEG_PAYER,
        _ => false,
    }
}

fn profile_err(e: ShelfError) -> String {
    e.to_string()
}

impl ShelfReceiveProfile for ReceiveProfile {
    /// Verifies a received pair and returns what the store needs (design
    /// §8.3; Go `Read`): a book record is placed at its book log id and
    /// seq, with the record id it cites before it; a sent record is the
    /// only record of `x-deal-v1/sent/<record digest>`, and an offer is
    /// marked so.
    fn read(&self, record: &[u8]) -> Result<ReceivedRecord, String> {
        let rd = read_pair(record)?;
        let digest = match &rd {
            Read::Book(b) => b.record_id.clone(),
            Read::Sent(s) => s.capsule_id.clone(),
        };
        self.state.lock().unwrap_or_else(|p| p.into_inner()).last =
            Some((digest, rd.record().clone()));
        Ok(match rd {
            Read::Book(b) => ReceivedRecord {
                digest: b.record_id,
                sender_key: b.key_id,
                log_id: b.log_id,
                position: b.seq,
                prev: b.prev,
                deal_id: b.record.block.deal_id,
                offer: false,
            },
            Read::Sent(s) => ReceivedRecord {
                digest: s.capsule_id,
                sender_key: s.key_id,
                log_id: format!("{SENT_LOG_PREFIX}{}", s.digest),
                position: 1,
                prev: String::new(),
                offer: s.record.block.record_type == TYPE_PROPOSE,
                deal_id: s.record.block.deal_id,
            },
        })
    }

    /// EBL's current push rule (Go `Admit`): an offer from any key; a
    /// decline of an offer of ours from the key it was sent to; a
    /// cancellation of an offer of this deal from that offer's signer; a
    /// ruling from our agreement's adjudicator, on the deal's case log
    /// ([`case_log_id`]), whoever carries it; at an adjudicator holding a
    /// deal's case book, a deal-book record of either party it names; and a
    /// deal-book record from the counterparty of the deal, on a log whose
    /// first record from that sender is the counterparty's agreement of this
    /// deal, citing the offer it answers.
    fn admit(&self, record: &ReceivedRecord) -> Result<bool, String> {
        self.admit_record(record).map_err(profile_err)
    }

    /// Whether our side answered a held offer (Go `Answered`): an agreement
    /// opening one of our deal books cites it, or a decline or a
    /// counter-offer in our journal does.
    fn answered(&self, offer: &ReceivedRecord) -> Result<bool, String> {
        self.answered_offer(offer).map_err(profile_err)
    }

    /// The counterparty whose agreement opens the book log a stamp proof
    /// names (Go `ProofSender`): among every record held at position 1 of
    /// that log, a sender holding two different ones is refused; of the
    /// rest, an x-deal-v1 agreement under this checkpoint key, of a deal of
    /// ours, signed by the key that deal expects (at an adjudicator, a party
    /// its case book names); or a case book's first ruling, when the
    /// checkpoint key is the adjudicator key our agreement names and the log
    /// is the deal's case log. Exactly one candidate gives its signer; none
    /// or more refuses the proof.
    fn proof_sender(
        &self,
        checkpoint_key_id: &str,
        log_id: &str,
    ) -> Result<Option<String>, String> {
        self.proof_sender_of(checkpoint_key_id, log_id)
            .map_err(profile_err)
    }

    /// Names, before the receive returns, each counted ruling of the deals
    /// the call touched ([`super::name_rulings`]; Go `AfterReceive`): those
    /// whose counted records grew, and the received record's own deal,
    /// stored or held already, so a record sent again retries a naming an
    /// earlier call failed (PROFILE §5; evidencebook#527).
    fn after_receive(
        &self,
        result: &crate::shelf::ReceiveResult,
        at: crate::shelf::Time,
    ) -> Result<(), String> {
        let mut deals = result.changes.grew.clone();
        if let Some(r) = &result.record {
            if r.refused.is_none()
                && (r.stored || r.duplicate)
                && !r.deal_id.is_empty()
                && !deals.contains(&r.deal_id)
            {
                deals.push(r.deal_id.clone());
            }
        }
        let shelf = self.shelf().map_err(profile_err)?;
        super::shelf_status::name_rulings(&shelf, &deals, at)
            .map(drop)
            .map_err(|e| profile_err(e.error))
    }
}
