// SPDX-License-Identifier: Apache-2.0

//! The v1 deal book's status value (design §8.1), as Go's
//! `xdealv1.BuildStatus` and `ResolveRow` build and read it
//! (`status_value.go`, `status_select.go`). It holds inputs, never a
//! computed status, and no shared identifier.

use std::collections::BTreeMap;

use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::Value;

use super::names::*;
use super::record::{Body, Record};
use super::start::{act_check, start_input};
use super::start::{BookRecord, StartBooks};
use super::status_rows::{row_inputs_with, BookKind, RecRef, RowBooks, TimedRecord};
use super::types::*;
use crate::deal::{Error, Terms};
use crate::row_status::{self, Input};

/// A time in a status value (design §8.1, "Times"): a record's known time,
/// none (no such record), or the holder's first stamp covering a record
/// whose time is not known yet. It encodes as a string, `null` or
/// `{"stamp": n}`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub enum StatusTime {
    /// No such record.
    #[default]
    None,
    /// A record's known time, RFC 3339 UTC.
    Known(String),
    /// A record first covered by stamp n (n >= 1), its time not known yet.
    Stamp(u64),
}

impl StatusTime {
    /// The time as rowstatus reads it: known, or none.
    pub fn known(&self) -> Option<String> {
        match self {
            StatusTime::Known(t) => Some(t.clone()),
            _ => None,
        }
    }
    fn known_str(&self) -> &str {
        match self {
            StatusTime::Known(t) => t,
            _ => "",
        }
    }
}

impl Serialize for StatusTime {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        match self {
            StatusTime::None => s.serialize_none(),
            StatusTime::Known(t) => s.serialize_str(t),
            StatusTime::Stamp(n) => {
                let mut m = BTreeMap::new();
                m.insert("stamp", *n);
                m.serialize(s)
            }
        }
    }
}

impl<'de> Deserialize<'de> for StatusTime {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<StatusTime, D::Error> {
        let bad = || {
            D::Error::custom(
                "xdealv1: a status time is a string, null or {\"stamp\": n} with n >= 1",
            )
        };
        match Value::deserialize(d)? {
            Value::Null => Ok(StatusTime::None),
            // Go's zero Known is no time: "" reads as null.
            Value::String(s) if s.is_empty() => Ok(StatusTime::None),
            Value::String(s) => Ok(StatusTime::Known(s)),
            Value::Object(m) if m.len() == 1 => match m.get("stamp").and_then(Value::as_u64) {
                Some(n) if n >= 1 => Ok(StatusTime::Stamp(n)),
                _ => Err(bad()),
            },
            _ => Err(bad()),
        }
    }
}

/// A record of a deal book or case book as a status value is built from
/// it: the record, its record id, and its time as the fact states it.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusRecord {
    pub record: Record,
    #[serde(default)]
    pub record_id: String,
    #[serde(default)]
    pub time: StatusTime,
}

/// A payment row's check (design §6.3), as Go's `PaymentResult` writes it.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaymentResult {
    #[serde(default)]
    pub check: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason: String,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub checked_over: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub excluded: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub settlement: Option<Value>,
}

/// What a status value is built from (design §8.1).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusBooks {
    pub offer: Record,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub own: Vec<StatusRecord>,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub counterpart: Vec<StatusRecord>,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub case_book: Vec<StatusRecord>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub conflicting_final_rulings: Vec<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub payments: BTreeMap<String, PaymentResult>,
    #[serde(default)]
    pub proof_held: bool,
    #[serde(default, skip_serializing_if = "is_zero_usize")]
    pub rulings_unnamed: usize,
}

fn is_zero_usize(v: &usize) -> bool {
    *v == 0
}

fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}

fn is_false(v: &bool) -> bool {
    !*v
}

/// The agreed price's currency and power of ten, or none.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ValueBand {
    pub currency: String,
    pub decade: i64,
}

/// The agreement's adjudicator as a status value names it.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusAdjud {
    pub key_id: String,
    pub rules_digest: String,
}

/// Start's input without at, its offerer's agreement time a [`StatusTime`].
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusStart {
    pub side: String,
    pub counterparty_layer: bool,
    pub agree_by: String,
    pub counterpart_agreement_held: bool,
    pub same_offer_and_rows: Option<bool>,
    pub offerer_names_acceptor: Option<bool>,
    pub offerer_agreement_time: StatusTime,
    pub acceptor_names_offerer: bool,
    pub cancellation_named_first: bool,
    /// The holder's first act with a counterparty that runs no layer; null
    /// before it, and when the counterparty runs the layer (#575).
    pub first_act: Option<StatusFirstAct>,
}

/// The holder's first perform or settle record in a status value: its time.
/// None says the record has no time yet, which `start` reads as local.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusFirstAct {
    pub time: StatusTime,
}

/// A named record's time and latest date.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusNamed {
    pub time: StatusTime,
    pub latest: String,
}

/// A statement and its record's time.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusStatement {
    pub side: String,
    pub value: String,
    pub time: StatusTime,
}

/// A dispute, its time and seen_in_time.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusRowDispute {
    pub side: String,
    pub time: StatusTime,
    pub seen_in_time: bool,
}

/// A withdrawal's side and time.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusSideTime {
    pub side: String,
    pub time: StatusTime,
}

/// A ruling, its time and remedy due date.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusRuling {
    pub r#final: bool,
    pub value: String,
    pub time: StatusTime,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub remedy_due: Option<String>,
}

/// A remedy's time.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusTimeOnly {
    pub time: StatusTime,
}

/// rowstatus's input without at, its times [`StatusTime`]s.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusRowInput {
    pub parties: Vec<String>,
    pub row: row_status::Row,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub named: Option<BTreeMap<String, StatusNamed>>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub statements: Vec<StatusStatement>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub check: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub disputes: Vec<StatusRowDispute>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub withdrawals: Vec<StatusSideTime>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub rulings: Vec<StatusRuling>,
    #[serde(default, skip_serializing_if = "is_false")]
    pub conflicting_final_rulings: bool,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub remedies: Vec<StatusTimeOnly>,
}

/// One agreed row of the status value (design §8.1).
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusRow {
    pub id: String,
    pub input: StatusRowInput,
    pub statement_named_by_other: Vec<StatusTime>,
    pub ruling_against: Option<String>,
    pub remedy_made: StatusTime,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub check_reason: String,
    #[serde(default, skip_serializing_if = "is_zero_i64")]
    pub check_excluded: i64,
}

/// One dispute record of either side, with its outcome.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusDispute {
    pub row: String,
    pub claimant: String,
    pub outcome: String,
    pub forum: String,
}

/// Dispute outcomes in a status value (design §8.1).
pub const OUTCOME_OPEN: &str = "open";
pub const OUTCOME_FOR_CLAIMANT: &str = "for_claimant";
pub const OUTCOME_AGAINST_CLAIMANT: &str = "against_claimant";
pub const OUTCOME_NO_FINDING: &str = "no_finding";
pub const OUTCOME_WITHDRAWN: &str = "withdrawn";

/// The v1 deal book's status value (design §8.1).
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatusValue {
    pub v: i64,
    pub role: String,
    pub side: String,
    pub agreed_at: String,
    pub counterparty_layer: bool,
    pub counterparty_kind: Option<String>,
    pub value_band: Option<ValueBand>,
    pub witnesses: Vec<Witness>,
    pub adjudicator: Option<StatusAdjud>,
    pub counterparty_proof_held: bool,
    pub start: StatusStart,
    pub rows: Vec<StatusRow>,
    pub disputes: Vec<StatusDispute>,
    #[serde(default, skip_serializing_if = "is_zero_usize")]
    pub rulings_unnamed: usize,
}

fn status_err(m: &str) -> Error {
    Error::Invalid(format!("xdealv1: status: {m}"))
}

fn timed(rs: &[StatusRecord]) -> Vec<TimedRecord> {
    rs.iter()
        .map(|r| TimedRecord {
            record: r.record.clone(),
            record_id: r.record_id.clone(),
            time: r.time.known(),
        })
        .collect()
}

/// The payment rows' checks that ran, by row id: a check not run is left
/// out, so RowInputs reads that row as not run.
pub(crate) fn payment_checks(
    payments: &BTreeMap<String, PaymentResult>,
) -> BTreeMap<String, String> {
    payments
        .iter()
        .filter(|(_, p)| !p.check.is_empty())
        .map(|(k, p)| (k.clone(), p.check.clone()))
        .collect()
}

/// Builds the status value from the books (Go `BuildStatus`).
pub fn build_status(b: &StatusBooks) -> Result<StatusValue, Error> {
    let Some(of) = b.offer.propose() else {
        return Err(status_err(&format!(
            "the offer is a {} record",
            b.offer.block.record_type
        )));
    };
    let Some(first) = b.own.first() else {
        return Err(status_err("the holder's book is empty"));
    };
    let own = match first.record.agree() {
        Some(a) if a.kind == KIND_AGREEMENT => a,
        _ => {
            return Err(status_err(
                "the holder's book does not open with its agreement",
            ))
        }
    };
    let time_of = |r: RecRef| -> StatusTime {
        match r.book {
            BookKind::Own => b.own[r.i].time.clone(),
            BookKind::Counterpart => b.counterpart[r.i].time.clone(),
            BookKind::Case => b.case_book[r.i].time.clone(),
        }
    };
    let rb = RowBooks {
        offer: b.offer.clone(),
        own: timed(&b.own),
        counterpart: timed(&b.counterpart),
        case_book: timed(&b.case_book),
        conflicting_final_rulings: b.conflicting_final_rulings.clone(),
        checks: payment_checks(&b.payments),
    };
    let agreed_at = first.record.block.at.clone();
    let mut as_of = agreed_at.clone();
    for rs in [&b.own, &b.counterpart, &b.case_book] {
        for r in rs {
            if r.time.known_str() > as_of.as_str() {
                as_of = r.time.known_str().to_string();
            }
        }
    }
    let (ins, srcs) = row_inputs_with(&rb, &as_of)?;
    let mut v = StatusValue {
        v: 1,
        role: own.role.clone(),
        side: own.side.clone(),
        agreed_at,
        counterparty_layer: own.layer(),
        counterparty_kind: None,
        value_band: value_band(&of.terms),
        witnesses: own.witness_list().to_vec(),
        adjudicator: own.adjudicator.value().map(|a| StatusAdjud {
            key_id: a.key_id.clone(),
            rules_digest: a.rules_digest.clone(),
        }),
        counterparty_proof_held: b.proof_held,
        start: status_start(b)?,
        rows: Vec::new(),
        disputes: Vec::new(),
        rulings_unnamed: b.rulings_unnamed,
    };
    let own_role = own.role.as_str();
    let other_role = if own_role == ROLE_BUYER {
        ROLE_SELLER
    } else {
        ROLE_BUYER
    };
    // Go's bookOfRole map: a role that is neither side's reads no book.
    let book_of_role = |role: &str| -> (&[StatusRecord], BookKind) {
        if role == own_role {
            (&b.own, BookKind::Own)
        } else if role == other_role {
            (&b.counterpart, BookKind::Counterpart)
        } else {
            (&[], BookKind::Counterpart)
        }
    };
    for (k, input) in ins.iter().enumerate() {
        let src = &srcs[k];
        let res = row_status::compute(input).map_err(|e| Error::Invalid(e.to_string()))?;
        let kept = carried_statements(&input.statements, &res.window_end);
        let mut row = StatusRow {
            id: input.row.id.clone(),
            input: StatusRowInput {
                parties: input.parties.clone(),
                row: input.row.clone(),
                named: None,
                statements: Vec::new(),
                check: input.check.clone(),
                disputes: Vec::new(),
                withdrawals: Vec::new(),
                rulings: Vec::new(),
                conflicting_final_rulings: input.conflicting_final_rulings,
                remedies: Vec::new(),
            },
            statement_named_by_other: Vec::new(),
            ruling_against: None,
            remedy_made: StatusTime::None,
            check_reason: String::new(),
            check_excluded: 0,
        };
        if !input.named.is_empty() {
            let mut named = BTreeMap::new();
            for (name, n) in &input.named {
                let t = src.named.get(name).map(|r| time_of(*r)).unwrap_or_default();
                named.insert(
                    name.clone(),
                    StatusNamed {
                        time: t,
                        latest: n.latest.clone(),
                    },
                );
            }
            row.input.named = Some(named);
        }
        for i in kept {
            let s = &input.statements[i];
            let rf = src.statements[i];
            row.input.statements.push(StatusStatement {
                side: s.side.clone(),
                value: s.value.clone(),
                time: time_of(rf),
            });
            row.statement_named_by_other
                .push(named_by_other(&b.own, &b.counterpart, rf));
        }
        for (i, d) in input.disputes.iter().enumerate() {
            row.input.disputes.push(StatusRowDispute {
                side: d.side.clone(),
                time: time_of(src.disputes[i]),
                seen_in_time: d.seen_in_time,
            });
        }
        for (i, w) in input.withdrawals.iter().enumerate() {
            row.input.withdrawals.push(StatusSideTime {
                side: w.side.clone(),
                time: time_of(src.withdrawals[i]),
            });
        }
        for (i, r) in input.rulings.iter().enumerate() {
            row.input.rulings.push(StatusRuling {
                r#final: r.r#final,
                value: r.value.clone(),
                time: time_of(src.rulings[i]),
                remedy_due: r.remedy_due.clone(),
            });
        }
        for rf in src.remedies.iter().take(input.remedies.len()) {
            row.input
                .remedies
                .push(StatusTimeOnly { time: time_of(*rf) });
        }
        let effective = effective_ruling(b, &input.row.id, input.conflicting_final_rulings);
        if let Some(e) = effective {
            row.ruling_against = e.against.value().cloned();
        }
        let (owing, _) = book_of_role(&input.row.owed_by);
        row.remedy_made = remedy_made(owing, &input.row.id);
        if let Some(p) = b.payments.get(&input.row.id) {
            row.check_reason = p.reason.clone();
            row.check_excluded = p.excluded.len() as i64;
        }
        v.rows.push(row);
        v.disputes
            .extend(disputes_of(&book_of_role, &input.row.id, effective));
    }
    Ok(v)
}

/// The agreed price's band: its currency and decade, none for a zero or
/// missing price or a currency with no known scale (Go `valueBand`).
fn value_band(t: &Terms) -> Option<ValueBand> {
    let minor = t.price_minor?;
    let currency = t.currency.as_deref()?;
    if minor <= 0 || currency.is_empty() {
        return None;
    }
    let scale = crate::deal::standard_history::history_asset_scale(currency)? as i64;
    Some(ValueBand {
        currency: currency.to_string(),
        decade: minor.to_string().len() as i64 - scale - 1,
    })
}

fn status_start(b: &StatusBooks) -> Result<StatusStart, Error> {
    let conv = |rs: &[StatusRecord]| -> Vec<BookRecord> {
        rs.iter()
            .map(|r| BookRecord {
                record: r.record.clone(),
                record_id: r.record_id.clone(),
                times: BTreeMap::new(),
            })
            .collect()
    };
    let input = start_input(&StartBooks {
        at: b.own[0].record.block.at.clone(),
        offer: b.offer.clone(),
        own: conv(&b.own),
        counterpart: conv(&b.counterpart),
    })?;
    let offerer_time = if input.side == SIDE_OFFERER {
        b.own[0].time.clone()
    } else {
        b.counterpart
            .first()
            .map(|r| r.time.clone())
            .unwrap_or_default()
    };
    let first_act = if input.counterparty_layer {
        None
    } else {
        b.own
            .iter()
            .find(|r| act_check(&r.record).is_some())
            .map(|r| StatusFirstAct {
                time: r.time.clone(),
            })
    };
    Ok(StatusStart {
        side: input.side,
        counterparty_layer: input.counterparty_layer,
        agree_by: input.agree_by,
        counterpart_agreement_held: input.counterpart_agreement_held,
        same_offer_and_rows: input.same_offer_and_rows,
        offerer_names_acceptor: input.offerer_names_acceptor,
        offerer_agreement_time: offerer_time,
        acceptor_names_offerer: input.acceptor_names_offerer,
        cancellation_named_first: input.cancellation_named_first,
        first_act,
    })
}

/// When the other side's book first named the record at `rf`: the time of
/// its first record whose seen is that record or a later one of `rf`'s book
/// (Go `namedByOther`).
fn named_by_other(own: &[StatusRecord], counterpart: &[StatusRecord], rf: RecRef) -> StatusTime {
    let (mine, other) = match rf.book {
        BookKind::Counterpart => (counterpart, own),
        _ => (own, counterpart),
    };
    let at: BTreeMap<&str, usize> = mine
        .iter()
        .enumerate()
        .map(|(i, r)| (r.record_id.as_str(), i))
        .collect();
    for r in other {
        if let Some(s) = r.record.block.seen.value() {
            if at.get(s.as_str()).is_some_and(|&i| i >= rf.i) {
                return r.time.clone();
            }
        }
    }
    StatusTime::None
}

/// The adjudicator's first final ruling on the row in the case book, none
/// when its case book forked with different ones.
pub(crate) fn effective_ruling<'a>(
    b: &'a StatusBooks,
    row: &str,
    conflicting: bool,
) -> Option<&'a RulingBody> {
    if conflicting {
        return None;
    }
    b.case_book.iter().find_map(|r| match &r.record.body {
        Body::Ruling(rb)
            if rb.row == row && rb.r#final && r.record.block.deal_id == b.offer.block.deal_id =>
        {
            Some(rb)
        }
        _ => None,
    })
}

/// The time of the owing side's first remedy for the row (Go `remedyMade`).
fn remedy_made(book: &[StatusRecord], row: &str) -> StatusTime {
    match first_remedy(book.iter().map(|r| &r.record), row) {
        Some(i) => book[i].time.clone(),
        None => StatusTime::None,
    }
}

/// The index of the first record that is a settle or perform whose
/// remedy_for is the row (Go `firstRemedy`).
pub(crate) fn first_remedy<'a>(
    records: impl Iterator<Item = &'a Record>,
    row: &str,
) -> Option<usize> {
    for (i, r) in records.enumerate() {
        let rf = match &r.body {
            Body::Perform(p) => p.remedy_for.as_ref(),
            Body::Settle(s) => s.remedy_for.as_ref(),
            _ => None,
        };
        if rf.is_some_and(|f| f.row == row) {
            return Some(i);
        }
    }
    None
}

/// Each dispute record of kind dispute on the row, of either side, with its
/// outcome (Go `disputesOf`).
fn disputes_of<'a>(
    book_of_role: &dyn Fn(&str) -> (&'a [StatusRecord], BookKind),
    row: &str,
    effective: Option<&RulingBody>,
) -> Vec<StatusDispute> {
    let mut out = Vec::new();
    for side in [ROLE_BUYER, ROLE_SELLER] {
        let (book, _) = book_of_role(side);
        for (i, r) in book.iter().enumerate() {
            let Body::Dispute(d) = &r.record.body else {
                continue;
            };
            if d.kind != KIND_DISPUTE || d.row != row {
                continue;
            }
            let withdrawn = book[i + 1..].iter().any(|w| {
                matches!(&w.record.body, Body::Dispute(wd) if wd.kind == KIND_WITHDRAWAL && wd.row == row)
            });
            let outcome = if withdrawn {
                OUTCOME_WITHDRAWN
            } else {
                match effective {
                    None => OUTCOME_OPEN,
                    Some(e) => match e.against.value() {
                        None => OUTCOME_NO_FINDING,
                        Some(a) if a == side => OUTCOME_AGAINST_CLAIMANT,
                        Some(_) => OUTCOME_FOR_CLAIMANT,
                    },
                }
            };
            out.push(StatusDispute {
                row: row.into(),
                claimant: side.into(),
                outcome: outcome.into(),
                forum: d.forum.clone(),
            });
        }
    }
    out
}

/// A status time as a reader resolves it: a known time, a stamp's time
/// from `stamps`, or none.
pub(crate) fn resolve_time(t: &StatusTime, stamps: &BTreeMap<u64, String>) -> Option<String> {
    match t {
        StatusTime::Known(k) => Some(k.clone()),
        StatusTime::Stamp(n) => stamps.get(n).cloned(),
        StatusTime::None => None,
    }
}

/// A status row's input as a reader computes it at `at`, each stamp it can
/// resolve given its time, and each dispute's seen_in_time recomputed from
/// those times (Go `ResolveRow`).
pub fn resolve_row(
    row: &StatusRow,
    at: &str,
    stamps: &BTreeMap<u64, String>,
) -> Result<Input, Error> {
    let r = |st: &StatusTime| resolve_time(st, stamps);
    let input = &row.input;
    let mut out = Input {
        at: at.into(),
        parties: input.parties.clone(),
        row: input.row.clone(),
        named: BTreeMap::new(),
        no_concession: false,
        statements: Vec::new(),
        check: input.check.clone(),
        disputes: Vec::new(),
        withdrawals: Vec::new(),
        rulings: Vec::new(),
        conflicting_final_rulings: input.conflicting_final_rulings,
        remedies: Vec::new(),
    };
    if let Some(named) = &input.named {
        for (k, n) in named {
            out.named.insert(
                k.clone(),
                row_status::Named {
                    time: r(&n.time),
                    latest: n.latest.clone(),
                },
            );
        }
    }
    for s in &input.statements {
        out.statements.push(row_status::Statement {
            side: s.side.clone(),
            value: s.value.clone(),
            time: r(&s.time),
        });
    }
    for w in &input.withdrawals {
        out.withdrawals.push(row_status::Withdrawal {
            side: w.side.clone(),
            time: r(&w.time),
        });
    }
    for ru in &input.rulings {
        out.rulings.push(row_status::Ruling {
            r#final: ru.r#final,
            value: ru.value.clone(),
            time: r(&ru.time),
            remedy_due: ru.remedy_due.clone(),
        });
    }
    for m in &input.remedies {
        out.remedies.push(row_status::Remedy { time: r(&m.time) });
    }
    if input.disputes.is_empty() {
        return Ok(out);
    }
    let mut plain = out.clone();
    plain.withdrawals.clear();
    plain.rulings.clear();
    plain.remedies.clear();
    let res = row_status::compute(&plain).map_err(|e| Error::Invalid(e.to_string()))?;
    let end = res.window_end;
    for d in &input.disputes {
        let mut seen = d.seen_in_time;
        if !end.is_empty() {
            seen = false;
            let mut standing: Option<usize> = None;
            let mut floor: Option<&String> = None;
            for (i, s) in out.statements.iter().enumerate() {
                if s.side == d.side {
                    continue;
                }
                let late = match &s.time {
                    Some(t) => *t > end,
                    None => floor.is_some_and(|f| *f > end),
                };
                if !late {
                    standing = Some(i);
                }
                if let Some(t) = &s.time {
                    if floor.is_none_or(|f| t > f) {
                        floor = Some(t);
                    }
                }
            }
            if let Some(st) = standing {
                if let Some(t) = row.statement_named_by_other.get(st).and_then(r) {
                    if t <= end {
                        seen = true;
                    }
                }
            }
        }
        out.disputes.push(row_status::Dispute {
            side: d.side.clone(),
            time: r(&d.time),
            seen_in_time: seen,
        });
    }
    Ok(out)
}

/// The indexes, in order, of the statements of one row a fact carries
/// (design §8.1, invariant A; Go `carriedStatements`).
fn carried_statements(stmts: &[row_status::Statement], window_end: &str) -> Vec<usize> {
    if window_end.is_empty() {
        return (0..stmts.len()).collect();
    }
    let mut keep = vec![false; stmts.len()];
    let mut possible: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    for side in [ROLE_BUYER, ROLE_SELLER] {
        let mut latest_in: Option<usize> = None;
        let pos = possible.entry(side).or_default();
        for (i, s) in stmts.iter().enumerate() {
            if s.side != side {
                continue;
            }
            match &s.time {
                None => {
                    keep[i] = true;
                    pos.push(&s.value);
                    let mut best: Option<usize> = None;
                    for (j, sj) in stmts.iter().enumerate().take(i) {
                        if sj.side == side {
                            if let Some(tj) = &sj.time {
                                if best.is_none_or(|b| tj >= stmts[b].time.as_ref().unwrap()) {
                                    best = Some(j);
                                }
                            }
                        }
                    }
                    if let Some(b) = best {
                        keep[b] = true;
                    }
                }
                Some(t) if t.as_str() <= window_end => latest_in = Some(i),
                Some(_) => {}
            }
        }
        if let Some(i) = latest_in {
            keep[i] = true;
            pos.push(&stmts[i].value);
        }
    }
    for (i, s) in stmts.iter().enumerate() {
        let Some(t) = &s.time else { continue };
        if t.as_str() <= window_end {
            continue;
        }
        let other = if s.side == ROLE_BUYER {
            ROLE_SELLER
        } else {
            ROLE_BUYER
        };
        if possible
            .get(other)
            .is_some_and(|p| p.contains(&s.value.as_str()))
        {
            keep[i] = true;
        }
    }
    keep.iter()
        .enumerate()
        .filter(|(_, k)| **k)
        .map(|(i, _)| i)
        .collect()
}
