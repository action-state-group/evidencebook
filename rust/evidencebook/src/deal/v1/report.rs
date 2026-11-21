// SPDX-License-Identifier: Apache-2.0

//! The v1 sealed deal report (design §9, PROFILE §10), as Go's
//! `xdealv1.BuildReport` and `ReportPayload` build it (`report.go`): a
//! snapshot the holder signs of each row's status as computed at `as_of`
//! from what it held then, which a page displays and never recomputes.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use super::check::record_digest;
use super::names::*;
use super::record::Body;
use super::start::{start, FirstAct, StartInput, StartResult, STATE_STARTED};
use super::status_rows::{row_inputs_with, BookKind, RecRef, RowBooks, RowSources, TimedRecord};
use super::status_value::{
    build_status, first_remedy, payment_checks, resolve_time, StatusBooks, StatusDispute,
    StatusRecord, StatusValue,
};
use super::types::*;
use crate::canonical::jcs;
use crate::deal::{Error, AUDIENCE_ADJUDICATOR, AUDIENCE_COUNTERPARTY, AUDIENCE_KEEP, REPORT_TYPE};
use crate::row_status::{self, Input};

// The report's closed words (design §9).
pub const ROWS_LISTED: &str = "listed";
pub const ROWS_NOT_STARTED: &str = "not_started";
pub const ROWS_WITHHELD: &str = "withheld";
pub const PROOF_HELD: &str = "held";
pub const PROOF_NOT_YET: &str = "not_yet";
pub const PROOF_NO_LAYER: &str = "no_layer";
/// A ruled row's remedy when the ruling orders a payment, which rowstatus
/// does not count until the profile defines its terms leg.
pub const REMEDY_NOT_COUNTED: &str = "not_counted";
pub const CHECK_PASSED: &str = "passed";
pub const CHECK_FAILED: &str = "failed";
pub const CHECK_NOT_RUN: &str = "not_run";
pub const CHECK_NOT_RECOMPUTED: &str = "not_recomputed";
/// A row date rowstatus leaves unset.
pub const NOT_SET: &str = "not_set";
/// A case book record's side in relied_on.
pub const SIDE_ADJUDICATOR: &str = "adjudicator";

/// The deal checkpoint a report is made at.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportCheckpoint {
    pub log_id: String,
    pub mmr_size: u64,
    pub root: String,
}

/// The holder's own book as the report rests on it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportLog {
    pub log_id: String,
    pub size: u64,
    pub last: String,
}

/// The counterparty's counted prefix the report used.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportCounterpartLog {
    pub log_id: String,
    pub counted: u64,
    pub last: String,
    pub fork_at: u64,
}

/// The book's last status fact against the status computed now.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportFact {
    pub current: bool,
}

/// What a v1 report is computed from (Go `ReportBooks`, whose JSON names
/// are Go's field names).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportBooks {
    #[serde(rename = "Books")]
    pub books: StatusBooks,
    #[serde(rename = "Stamps", default)]
    pub stamps: Option<BTreeMap<u64, String>>,
    #[serde(rename = "Own", default)]
    pub own: ReportLog,
    #[serde(rename = "Counterpart", default)]
    pub counterpart: Option<ReportCounterpartLog>,
    #[serde(rename = "Checkpoint", default)]
    pub checkpoint: ReportCheckpoint,
    #[serde(rename = "Fact", default)]
    pub fact: Option<ReportFact>,
}

/// What one copy names: its audience, a fresh nonce, the referee an
/// adjudicator's copy is handed to, as_of, and the producer's text.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ReportCopy {
    pub audience: String,
    pub nonce: String,
    pub referee: String,
    pub as_of: String,
    pub text: Option<Map<String, Value>>,
}

/// The v1 report payload (design §9).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Report {
    pub r#type: String,
    pub profile: String,
    pub deal_id: String,
    pub audience: String,
    pub nonce: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub referee: String,
    pub as_of: String,
    pub deal_checkpoint: ReportCheckpoint,
    pub pack_digest: String,
    pub start: StartResult,
    pub settled: bool,
    pub rows_state: String,
    pub rows: Vec<ReportRow>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub disputes: Vec<StatusDispute>,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub rulings_unnamed: usize,
    pub rests_on: ReportRestsOn,
    pub status_fact: Option<ReportFact>,
    #[serde(rename = "report")]
    pub text: Map<String, Value>,
}

fn is_zero(v: &usize) -> bool {
    *v == 0
}

/// What the whole report rests on (design §9).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportRestsOn {
    pub own: ReportLog,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty: Option<ReportCounterpartLog>,
    pub counterparty_layer: bool,
    pub counterparty_kind: Option<String>,
    pub counterparty_proof: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub witnesses: Vec<Witness>,
}

/// One agreed row (design §9, "One row").
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportRow {
    pub id: String,
    pub owed_by: String,
    pub owed: Value,
    pub due: String,
    pub window_end: String,
    pub status: String,
    pub value: Option<String>,
    pub remedy: Option<String>,
    pub remedy_made: bool,
    pub local: bool,
    pub check: String,
    pub late: ReportLate,
    pub ruling: Option<ReportRuling>,
    pub relied_on: Vec<ReportSource>,
}

/// The row's late statements and disputes, each by its record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportLate {
    pub statements: Vec<ReportLateStatement>,
    pub disputes: Vec<ReportLateDispute>,
}

/// A late statement.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportLateStatement {
    pub side: String,
    pub value: String,
    pub record: String,
}

/// A late dispute.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportLateDispute {
    pub side: String,
    pub record: String,
}

/// The effective ruling rowstatus applied to a ruled row.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportRuling {
    pub record: String,
    pub value: String,
    pub against: Option<String>,
    pub r#final: bool,
    pub remedy_due: Option<String>,
}

/// A record a row's inputs came from.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportSource {
    pub record: String,
    pub side: String,
    pub time: Option<String>,
    pub witnessed: bool,
}

fn report_err(m: String) -> Error {
    Error::Invalid(format!("xdealv1: report: {m}"))
}

/// A referee's name as a report seals it: NFC, with surrounding white space
/// removed.
fn normalize_referee(v: &str) -> String {
    crate::deal::keys::nfc(v).trim().to_string()
}

/// Computes one copy of the v1 report from `b` at `c.as_of` (Go
/// `BuildReport`), with Go's refusals.
pub fn build_report(b: &ReportBooks, c: &ReportCopy) -> Result<Report, Error> {
    build_report_rows(b, c).map(|(r, _)| r)
}

/// [`build_report`] with every row it computed, listed in the copy or not:
/// the records they rely on are the ones the x-deal-v1 bundle extension
/// carries for every copy (Go `buildReport`).
pub(crate) fn build_report_rows(
    b: &ReportBooks,
    c: &ReportCopy,
) -> Result<(Report, Vec<ReportRow>), Error> {
    if ![AUDIENCE_KEEP, AUDIENCE_COUNTERPARTY, AUDIENCE_ADJUDICATOR].contains(&c.audience.as_str())
    {
        return Err(report_err(format!(
            "audience {:?} is not a copy audience",
            c.audience
        )));
    }
    if c.nonce.len() != 64
        || !c
            .nonce
            .bytes()
            .all(|x| matches!(x, b'0'..=b'9' | b'a'..=b'f'))
    {
        return Err(report_err("the nonce must be 64 lowercase hex".into()));
    }
    let referee = normalize_referee(&c.referee);
    let adj = c.audience == AUDIENCE_ADJUDICATOR;
    if adj && referee.is_empty() {
        return Err(report_err(
            "an adjudicator's copy names the referee it is handed to".into(),
        ));
    }
    if !adj && !referee.is_empty() {
        return Err(report_err(
            "only an adjudicator's copy names a referee".into(),
        ));
    }
    if crate::row_status::parse_time(&c.as_of).is_err() {
        return Err(report_err(format!(
            "as_of {:?} is not RFC 3339 UTC to the second",
            c.as_of
        )));
    }
    let v = build_status(&b.books)?;
    let of = b.books.offer.propose().cloned().unwrap_or_default();
    let own = b.books.own[0].record.agree().cloned().unwrap_or_default();
    let stamps = b.stamps.clone().unwrap_or_default();
    let resolve = |rs: &[StatusRecord]| -> Vec<TimedRecord> {
        rs.iter()
            .map(|r| TimedRecord {
                record: r.record.clone(),
                record_id: r.record_id.clone(),
                time: resolve_time(&r.time, &stamps),
            })
            .collect()
    };
    let rb = RowBooks {
        offer: b.books.offer.clone(),
        own: resolve(&b.books.own),
        counterpart: resolve(&b.books.counterpart),
        case_book: resolve(&b.books.case_book),
        conflicting_final_rulings: b.books.conflicting_final_rulings.clone(),
        checks: payment_checks(&b.books.payments),
    };
    for rs in [&rb.own, &rb.counterpart, &rb.case_book] {
        for r in rs {
            if let Some(t) = &r.time {
                if t.as_str() > c.as_of.as_str() {
                    return Err(report_err(format!(
                        "record {} is timed {t}, after as_of {}",
                        r.record_id, c.as_of
                    )));
                }
            }
        }
    }
    let (ins, srcs) = row_inputs_with(&rb, &c.as_of)?;
    let st = &v.start;
    let started = start(&StartInput {
        at: c.as_of.clone(),
        side: st.side.clone(),
        counterparty_layer: st.counterparty_layer,
        agree_by: st.agree_by.clone(),
        counterpart_agreement_held: st.counterpart_agreement_held,
        same_offer_and_rows: st.same_offer_and_rows,
        offerer_names_acceptor: st.offerer_names_acceptor,
        offerer_agreement_time: resolve_time(&st.offerer_agreement_time, &stamps),
        acceptor_names_offerer: st.acceptor_names_offerer,
        cancellation_named_first: st.cancellation_named_first,
        first_act: st.first_act.as_ref().map(|a| FirstAct {
            time: resolve_time(&a.time, &stamps),
        }),
    })?;
    let legs = payment_legs(&b.books, &rb)?;
    let mut rows = Vec::new();
    for (i, input) in ins.iter().enumerate() {
        let agreed = &of.terms_rows.rows[i];
        let row_legs = legs.get(&agreed.id).map(Vec::as_slice).unwrap_or(&[]);
        rows.push(report_row(&rb, agreed, input, &srcs[i], row_legs)?);
    }
    let mut r = Report {
        r#type: REPORT_TYPE.into(),
        profile: PROFILE.into(),
        deal_id: b.books.offer.block.deal_id.clone(),
        audience: c.audience.clone(),
        nonce: c.nonce.clone(),
        referee,
        as_of: c.as_of.clone(),
        deal_checkpoint: b.checkpoint.clone(),
        pack_digest: of.pack_digest.clone(),
        settled: settled(&started, &rows),
        start: started.clone(),
        rows_state: String::new(),
        rows: Vec::new(),
        disputes: Vec::new(),
        rulings_unnamed: 0,
        rests_on: ReportRestsOn {
            own: b.own.clone(),
            counterparty: None,
            counterparty_layer: v.counterparty_layer,
            counterparty_kind: v.counterparty_kind.clone(),
            counterparty_proof: counterparty_proof(&v).into(),
            witnesses: Vec::new(),
        },
        status_fact: b.fact.clone(),
        text: c.text.clone().unwrap_or_default(),
    };
    if adj {
        r.rows_state = ROWS_WITHHELD.into();
    } else if started.state != STATE_STARTED {
        r.rows_state = ROWS_NOT_STARTED.into();
    } else {
        r.rows_state = ROWS_LISTED.into();
        r.rows = rows.clone();
    }
    if !adj {
        r.rests_on.counterparty = b.counterpart.clone();
        r.rests_on.witnesses = own.witness_list().to_vec();
        r.disputes = v.disputes.clone();
        r.rulings_unnamed = v.rulings_unnamed;
    }
    Ok((r, rows))
}

/// The report's payload bytes: its JCS form (Go `ReportPayload`).
pub fn report_payload(r: &Report) -> Result<Vec<u8>, Error> {
    Ok(jcs(&serde_json::to_value(r)?)?)
}

fn counterparty_proof(v: &StatusValue) -> &'static str {
    if !v.counterparty_layer {
        PROOF_NO_LAYER
    } else if v.counterparty_proof_held {
        PROOF_HELD
    } else {
        PROOF_NOT_YET
    }
}

/// Each payment row's `PaymentResult` mapped to the book records it read
/// (Go `paymentLegs`): `checked_over`, whose first entry is the offer's
/// digest and whose others are settle legs of the holder's or the
/// counterparty's book. Refuses a result whose first entry is not the
/// offer's digest, or that names a record the books given do not hold.
fn payment_legs(b: &StatusBooks, rb: &RowBooks) -> Result<BTreeMap<String, Vec<RecRef>>, Error> {
    let mut out = BTreeMap::new();
    if b.payments.is_empty() {
        return Ok(out);
    }
    let od = record_digest(&b.offer)?;
    let mut held: BTreeMap<String, RecRef> = BTreeMap::new();
    for (book, recs) in [
        (BookKind::Own, &rb.own),
        (BookKind::Counterpart, &rb.counterpart),
    ] {
        for (i, r) in recs.iter().enumerate() {
            held.entry(record_digest(&r.record)?)
                .or_insert(RecRef { book, i });
        }
    }
    for (row, res) in &b.payments {
        let over = &res.checked_over;
        if over.first() != Some(&od) {
            return Err(report_err(format!(
                "row {row}'s payment check does not begin with the offer's digest"
            )));
        }
        let mut refs = Vec::new();
        for d in &over[1..] {
            let Some(rf) = held.get(d) else {
                return Err(report_err(format!(
                    "row {row}'s payment check read record {d}, which the books given do not hold"
                )));
            };
            refs.push(*rf);
        }
        out.insert(row.clone(), refs);
    }
    Ok(out)
}

fn report_row(
    b: &RowBooks,
    agreed: &Row,
    input: &Input,
    src: &RowSources,
    legs: &[RecRef],
) -> Result<ReportRow, Error> {
    let res = row_status::compute(input).map_err(|e| Error::Invalid(e.to_string()))?;
    let late = row_status::compute_late(input).map_err(|e| Error::Invalid(e.to_string()))?;
    let own_role = b.own[0]
        .record
        .agree()
        .map(|a| a.role.clone())
        .unwrap_or_default();
    let rec = |rf: RecRef| -> (&TimedRecord, String) {
        match rf.book {
            BookKind::Own => (&b.own[rf.i], own_role.clone()),
            BookKind::Counterpart => (
                &b.counterpart[rf.i],
                if own_role == ROLE_BUYER {
                    ROLE_SELLER
                } else {
                    ROLE_BUYER
                }
                .into(),
            ),
            BookKind::Case => (&b.case_book[rf.i], SIDE_ADJUDICATOR.into()),
        }
    };
    let or_not_set = |s: &str| {
        if s.is_empty() {
            NOT_SET.to_string()
        } else {
            s.to_string()
        }
    };
    let mut row = ReportRow {
        id: agreed.id.clone(),
        owed_by: agreed.owed_by.clone(),
        owed: agreed.owed.clone(),
        due: or_not_set(&res.due),
        window_end: or_not_set(&res.window_end),
        status: res.status.clone(),
        value: (!res.value.is_empty()).then(|| res.value.clone()),
        remedy: (!res.remedy.is_empty()).then(|| res.remedy.clone()),
        remedy_made: false,
        local: res.local,
        check: report_check(agreed, &input.check).into(),
        late: ReportLate::default(),
        ruling: None,
        relied_on: Vec::new(),
    };
    for &i in &late.statements {
        let (r, _) = rec(src.statements[i]);
        row.late.statements.push(ReportLateStatement {
            side: input.statements[i].side.clone(),
            value: input.statements[i].value.clone(),
            record: r.record_id.clone(),
        });
    }
    for &i in &late.disputes {
        let (r, _) = rec(src.disputes[i]);
        row.late.disputes.push(ReportLateDispute {
            side: input.disputes[i].side.clone(),
            record: r.record_id.clone(),
        });
    }
    if res.status == "ruled" {
        for (k, ru) in input.rulings.iter().enumerate() {
            if !ru.r#final {
                continue;
            }
            let (r, _) = rec(src.rulings[k]);
            if let Body::Ruling(body) = &r.record.body {
                row.ruling = Some(ReportRuling {
                    record: r.record_id.clone(),
                    value: ru.value.clone(),
                    against: body.against.value().cloned(),
                    r#final: true,
                    remedy_due: ru.remedy_due.clone(),
                });
                if body
                    .remedy
                    .as_ref()
                    .is_some_and(|m| m.amount_minor.is_some())
                {
                    row.remedy = Some(REMEDY_NOT_COUNTED.into());
                }
            }
            break;
        }
    }
    let (owing, owing_book) = if own_role == agreed.owed_by {
        (&b.own, BookKind::Own)
    } else {
        (&b.counterpart, BookKind::Counterpart)
    };
    let mut remedy_ref = None;
    if let Some(i) = first_remedy(owing.iter().map(|r| &r.record), &agreed.id) {
        row.remedy_made = true;
        remedy_ref = Some(RecRef {
            book: owing_book,
            i,
        });
    }
    let mut refs: Vec<RecRef> = src.named.values().copied().collect();
    for list in [
        &src.statements,
        &src.disputes,
        &src.withdrawals,
        &src.rulings,
        &src.remedies,
    ] {
        refs.extend(list.iter().copied());
    }
    refs.extend(remedy_ref);
    // The settle legs the payment check read (Steven's ruling on #588).
    refs.extend(legs.iter().copied());
    let mut seen = BTreeSet::new();
    for rf in refs {
        if !seen.insert(rf) {
            continue;
        }
        let (r, side) = rec(rf);
        row.relied_on.push(ReportSource {
            record: r.record_id.clone(),
            side,
            time: r.time.clone(),
            witnessed: r.time.is_some(),
        });
    }
    Ok(row)
}

fn report_check(agreed: &Row, check: &str) -> &'static str {
    if !agreed.recomputed {
        CHECK_NOT_RECOMPUTED
    } else if check == "pass" {
        CHECK_PASSED
    } else if check == "fail" {
        CHECK_FAILED
    } else {
        CHECK_NOT_RUN
    }
}

/// Design §9's settled: the deal has started, and every row that is not
/// not_reached is agreed (met, or not met with a remedy made), uncontested,
/// or ruled with remedy none or made (Go `settled`).
pub fn settled(start: &StartResult, rows: &[ReportRow]) -> bool {
    if start.state != STATE_STARTED {
        return false;
    }
    rows.iter().all(|r| match r.status.as_str() {
        "not_reached" | "uncontested" => true,
        "agreed" => !(r.value.as_deref() == Some(VALUE_NOT_MET) && !r.remedy_made),
        "ruled" => matches!(r.remedy.as_deref(), Some("none") | Some("made")),
        _ => false,
    })
}
