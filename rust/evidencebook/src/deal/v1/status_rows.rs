// SPDX-License-Identifier: Apache-2.0

//! The two books projected onto one rowstatus input per agreed row (design
//! §6.1), as Go's `xdealv1.RowInputs` does (`status_rows.go`).

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use super::names::*;
use super::record::{Body, Record};
use super::types::*;
use crate::deal::Error;
use crate::row_status::{self, Input, Named as RsNamed};

/// A deal-book or case-book record and its record time, none while it has
/// none.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TimedRecord {
    pub record: Record,
    #[serde(default)]
    pub record_id: String,
    #[serde(default)]
    pub time: Option<String>,
}

/// What the row inputs are built from (design §6.1): the offer whose
/// terms_rows the deal agreed, the holder's deal book, the counted prefix
/// of the counterparty's, and the adjudicator's case book, each in book
/// order; the rows whose case book forked with different first final
/// rulings; and the recomputed rows' check results by row id.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RowBooks {
    pub offer: Record,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub own: Vec<TimedRecord>,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub counterpart: Vec<TimedRecord>,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub case_book: Vec<TimedRecord>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub conflicting_final_rulings: Vec<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub checks: BTreeMap<String, String>,
}

/// Which book a record a row input read is in, and where.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub(crate) struct RecRef {
    pub(crate) book: BookKind,
    pub(crate) i: usize,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub(crate) enum BookKind {
    Own,
    Counterpart,
    Case,
}

/// For one row's input, the record each element came from, in the input's
/// order; named maps a name to the first marked record, when one is.
#[derive(Clone, Debug, Default)]
pub(crate) struct RowSources {
    pub(crate) statements: Vec<RecRef>,
    pub(crate) disputes: Vec<RecRef>,
    pub(crate) withdrawals: Vec<RecRef>,
    pub(crate) rulings: Vec<RecRef>,
    pub(crate) remedies: Vec<RecRef>,
    pub(crate) named: BTreeMap<String, RecRef>,
}

/// Projects the two books onto one rowstatus input per agreed row, as of
/// `at` (Go `RowInputs`).
pub fn row_inputs(b: &RowBooks, at: &str) -> Result<Vec<Input>, Error> {
    Ok(row_inputs_with(b, at)?.0)
}

fn bad(m: String) -> Error {
    Error::Invalid(format!("xdealv1: row inputs: {m}"))
}

pub(crate) fn statement_items(r: &Record) -> &[StatementItem] {
    match &r.body {
        Body::Perform(b) => &b.statements,
        Body::Settle(b) => &b.statements,
        Body::Statement(b) => &b.statements,
        _ => &[],
    }
}

fn row_items<'a>(r: &'a Record, row: &str) -> Vec<&'a StatementItem> {
    statement_items(r)
        .iter()
        .filter(|it| it.row == row)
        .collect()
}

fn project_date(d: &Date) -> row_status::Date {
    row_status::Date {
        at: (!d.at.is_empty()).then(|| d.at.clone()),
        after: d.after.clone(),
        seconds: d.seconds.unwrap_or(0),
    }
}

pub(crate) fn row_inputs_with(
    b: &RowBooks,
    at: &str,
) -> Result<(Vec<Input>, Vec<RowSources>), Error> {
    let Some(of) = b.offer.propose() else {
        return Err(bad(format!(
            "the offer is a {} record",
            b.offer.block.record_type
        )));
    };
    let Some(first) = b.own.first() else {
        return Err(bad("the holder's book is empty".into()));
    };
    let own = match first.record.agree() {
        Some(a) if a.kind == KIND_AGREEMENT => a,
        _ => {
            return Err(bad(
                "the holder's book does not open with its agreement".into()
            ))
        }
    };
    let own_role = own.role.as_str();
    let other_role = if own_role == ROLE_BUYER {
        ROLE_SELLER
    } else {
        ROLE_BUYER
    };
    // Go's map {own role: own book, other role: counterpart}: a role that
    // is neither reads no book.
    let book = |role: &str| -> (&[TimedRecord], BookKind) {
        if role == own_role {
            (&b.own, BookKind::Own)
        } else if role == other_role {
            (&b.counterpart, BookKind::Counterpart)
        } else {
            (&[], BookKind::Counterpart)
        }
    };
    let tr = &of.terms_rows;

    let mut named = BTreeMap::new();
    let mut named_src = BTreeMap::new();
    for (name, n) in &tr.named {
        let mut t = None;
        let (recs, kind) = book(&n.side);
        for (i, r) in recs.iter().enumerate() {
            if r.record
                .block
                .marks
                .as_ref()
                .is_some_and(|m| m.contains(name))
            {
                t = r.time.clone();
                named_src.insert(name.clone(), RecRef { book: kind, i });
                break;
            }
        }
        named.insert(
            name.clone(),
            RsNamed {
                time: t,
                latest: n.latest.clone(),
            },
        );
    }

    let mut rulings: Vec<(usize, &RulingBody, &TimedRecord)> = Vec::new();
    if own.adjudicator.value().is_some() {
        for (i, r) in b.case_book.iter().enumerate() {
            if r.record.block.record_type == TYPE_RULING
                && r.record.block.deal_id == b.offer.block.deal_id
            {
                if let Body::Ruling(rb) = &r.record.body {
                    rulings.push((i, rb, r));
                }
            }
        }
    }

    for id in b.checks.keys() {
        if !tr.rows.iter().any(|r| &r.id == id) {
            return Err(bad(format!(
                "a check given for row {id:?}, which the terms do not have"
            )));
        }
    }
    let mut out = Vec::new();
    let mut srcs = Vec::new();
    for row in &tr.rows {
        let mut src = RowSources {
            named: named_src.clone(),
            ..Default::default()
        };
        let mut input = Input {
            at: at.into(),
            parties: vec![ROLE_BUYER.into(), ROLE_SELLER.into()],
            row: row_status::Row {
                id: row.id.clone(),
                owed_by: row.owed_by.clone(),
                due: project_date(&row.due),
                window_end: project_date(&row.window_end),
                recomputed: row.recomputed,
            },
            named: named.clone(),
            no_concession: false,
            statements: Vec::new(),
            check: String::new(),
            disputes: Vec::new(),
            withdrawals: Vec::new(),
            rulings: Vec::new(),
            conflicting_final_rulings: b.conflicting_final_rulings.contains(&row.id),
            remedies: Vec::new(),
        };
        if let Some(c) = b.checks.get(&row.id) {
            if !row.recomputed {
                return Err(bad(format!(
                    "a check given for row {:?}, which is not recomputed",
                    row.id
                )));
            }
            input.check = c.clone();
        }
        for side in [ROLE_BUYER, ROLE_SELLER] {
            let (recs, kind) = book(side);
            for (i, r) in recs.iter().enumerate() {
                let rf = RecRef { book: kind, i };
                for it in statement_items(&r.record) {
                    if it.row == row.id {
                        input.statements.push(row_status::Statement {
                            side: side.into(),
                            value: it.value.clone(),
                            time: r.time.clone(),
                        });
                        src.statements.push(rf);
                    }
                }
                if let Body::Dispute(d) = &r.record.body {
                    if d.row == row.id {
                        if d.kind == KIND_DISPUTE {
                            input.disputes.push(row_status::Dispute {
                                side: side.into(),
                                time: r.time.clone(),
                                seen_in_time: false,
                            });
                            src.disputes.push(rf);
                        } else if d.kind == KIND_WITHDRAWAL {
                            input.withdrawals.push(row_status::Withdrawal {
                                side: side.into(),
                                time: r.time.clone(),
                            });
                            src.withdrawals.push(rf);
                        }
                    }
                }
            }
        }
        let mut effective: Option<&RulingBody> = None;
        for (i, rb, r) in &rulings {
            if rb.row != row.id {
                continue;
            }
            input.rulings.push(row_status::Ruling {
                r#final: rb.r#final,
                value: rb.value.clone(),
                time: r.time.clone(),
                remedy_due: rb.remedy.as_ref().map(|m| m.due.clone()),
            });
            src.rulings.push(RecRef {
                book: BookKind::Case,
                i: *i,
            });
            if rb.r#final && effective.is_none() && !input.conflicting_final_rulings {
                effective = Some(rb);
            }
        }
        if let Some(eff) = effective {
            if let (Some(against), Some(remedy)) = (eff.against.value(), &eff.remedy) {
                let (recs, kind) = book(against);
                for (i, r) in recs.iter().enumerate() {
                    if let Body::Perform(p) = &r.record.body {
                        if p.remedy_for.as_ref().is_some_and(|f| f.row == row.id)
                            && p.act == remedy.kind
                        {
                            input.remedies.push(row_status::Remedy {
                                time: r.time.clone(),
                            });
                            src.remedies.push(RecRef { book: kind, i });
                        }
                    }
                }
            }
        }
        seen_in_time(&mut input, &b.own, &b.counterpart, own_role, &row.id)?;
        out.push(input);
        srcs.push(src);
    }
    Ok((out, srcs))
}

/// Sets each dispute's seen_in_time (design §6.2), as Go's `seenInTime`.
fn seen_in_time(
    input: &mut Input,
    own: &[TimedRecord],
    counterpart: &[TimedRecord],
    own_role: &str,
    row_id: &str,
) -> Result<(), Error> {
    if input.disputes.is_empty() {
        return Ok(());
    }
    let mut plain = input.clone();
    plain.disputes.clear();
    plain.withdrawals.clear();
    plain.rulings.clear();
    plain.remedies.clear();
    let res = row_status::compute(&plain).map_err(|e| Error::Invalid(e.to_string()))?;
    if res.window_end.is_empty() {
        return Ok(());
    }
    let end = res.window_end;
    let other_role = if own_role == ROLE_BUYER {
        ROLE_SELLER
    } else {
        ROLE_BUYER
    };
    let books = |role: &str| -> &[TimedRecord] {
        if role == own_role {
            own
        } else if role == other_role {
            counterpart
        } else {
            &[]
        }
    };
    for d in input.disputes.iter_mut() {
        let other = if d.side == ROLE_BUYER {
            ROLE_SELLER
        } else {
            ROLE_BUYER
        };
        let mut standing: Option<usize> = None;
        let mut floor: Option<&String> = None;
        for (j, r) in books(other).iter().enumerate() {
            if row_items(&r.record, row_id).is_empty() {
                continue;
            }
            let late = match &r.time {
                Some(t) => *t > end,
                None => floor.is_some_and(|f| *f > end),
            };
            if !late {
                standing = Some(j);
            }
            if let Some(t) = &r.time {
                if floor.is_none_or(|f| t > f) {
                    floor = Some(t);
                }
            }
        }
        let Some(standing) = standing else {
            continue;
        };
        let ids: BTreeMap<&str, usize> = books(other)
            .iter()
            .enumerate()
            .map(|(j, r)| (r.record_id.as_str(), j))
            .collect();
        for r in books(&d.side) {
            let (Some(s), Some(t)) = (r.record.block.seen.value(), &r.time) else {
                continue;
            };
            if *t > end {
                continue;
            }
            if ids.get(s.as_str()).is_some_and(|&j| j >= standing) {
                d.seen_in_time = true;
                break;
            }
        }
    }
    Ok(())
}
