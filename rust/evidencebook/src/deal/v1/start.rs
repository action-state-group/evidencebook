// SPDX-License-Identifier: Apache-2.0

//! When a deal has started (design §5), as Go's `xdealv1.Start` and
//! `StartFromBooks` compute it (`start.go`, `start_books.go`).

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use super::check::record_digest;
use super::names::*;
use super::record::{Body, Record};
use super::types::*;
use crate::deal::{Error, Ref};

/// A start state ([`StartResult::state`]).
pub const STATE_STARTED: &str = "started";
pub const STATE_AWAITING_AGREEMENT: &str = "awaiting_agreement";
pub const STATE_NOT_STARTED: &str = "not_started";

/// What started a deal ([`StartResult::by`]).
pub const BY_OFFERER_IN_TIME: &str = "offerer_in_time";
pub const BY_ACCEPTOR_NAMED: &str = "acceptor_named";
pub const BY_FIRST_ACT: &str = "first_act";

/// The holder's first act, for a deal whose counterparty runs no layer: its
/// record time, or none yet.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FirstAct {
    pub time: Option<String>,
}

/// What [`start`] reads (design §5.2).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StartInput {
    pub at: String,
    pub side: String,
    pub counterparty_layer: bool,
    pub agree_by: String,
    pub counterpart_agreement_held: bool,
    pub same_offer_and_rows: Option<bool>,
    pub offerer_names_acceptor: Option<bool>,
    pub offerer_agreement_time: Option<String>,
    pub acceptor_names_offerer: bool,
    pub cancellation_named_first: bool,
    pub first_act: Option<FirstAct>,
}

/// Whether a deal has started (design §5.2).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StartResult {
    pub state: String,
    pub local: bool,
    pub by: Option<String>,
}

fn started(local: bool, by: &str) -> StartResult {
    StartResult {
        state: STATE_STARTED.into(),
        local,
        by: Some(by.into()),
    }
}

fn not_started() -> StartResult {
    StartResult {
        state: STATE_NOT_STARTED.into(),
        ..Default::default()
    }
}

fn start_err(msg: String) -> Error {
    Error::Invalid(format!("xdealv1: start: {msg}"))
}

/// The instant of an RFC 3339 UTC time to the second, as seconds.
fn start_time(name: &str, s: &str) -> Result<i64, Error> {
    crate::row_status::parse_time_from(s, 0)
        .map_err(|_| start_err(format!("{name} {s:?} is not a UTC time to the second")))
}

/// Computes whether a deal has started from `input`, in design §5.2's
/// order (Go `Start`), with Go's refusals.
pub fn start(input: &StartInput) -> Result<StartResult, Error> {
    let at = start_time("at", &input.at)?;
    if input.side != SIDE_OFFERER && input.side != SIDE_ACCEPTOR {
        return Err(start_err(format!(
            "side {:?} is neither offerer nor acceptor",
            input.side
        )));
    }
    if !input.counterparty_layer {
        let Some(act) = &input.first_act else {
            return Ok(not_started());
        };
        if let Some(t) = &act.time {
            start_time("first_act.time", t)?;
        }
        return Ok(started(act.time.is_none(), BY_FIRST_ACT));
    }
    let agree_by = start_time("agree_by", &input.agree_by)?;
    let late = at > agree_by;
    let awaiting = || {
        if late {
            not_started()
        } else {
            StartResult {
                state: STATE_AWAITING_AGREEMENT.into(),
                ..Default::default()
            }
        }
    };
    if input.cancellation_named_first {
        return Ok(not_started());
    }
    if input.counterpart_agreement_held
        && (input.same_offer_and_rows.is_none() || input.offerer_names_acceptor.is_none())
    {
        return Err(start_err(
            "a held counterpart agreement gives same_offer_and_rows and offerer_names_acceptor"
                .into(),
        ));
    }
    if !input.counterpart_agreement_held || input.same_offer_and_rows == Some(false) {
        if input.side == SIDE_OFFERER {
            return Err(start_err(
                "an offerer writes its agreement after the acceptor's it holds".into(),
            ));
        }
        return Ok(awaiting());
    }
    if input.offerer_names_acceptor != Some(true) {
        return Ok(awaiting());
    }
    if input.acceptor_names_offerer {
        return Ok(started(false, BY_ACCEPTOR_NAMED));
    }
    if let Some(t) = &input.offerer_agreement_time {
        let t = start_time("offerer_agreement_time", t)?;
        return Ok(if t <= agree_by {
            started(false, BY_OFFERER_IN_TIME)
        } else {
            not_started()
        });
    }
    if !late {
        return Ok(StartResult {
            state: STATE_STARTED.into(),
            local: true,
            by: None,
        });
    }
    Ok(not_started())
}

/// A deal-book record as [`start_from_books`] reads it: the record, its
/// record id, and the times witnesses gave it, by witness key id.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BookRecord {
    pub record: Record,
    pub record_id: String,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub times: BTreeMap<String, String>,
}

/// What [`start_from_books`] reads: the holder's deal book, the counted
/// prefix of the counterparty's, and the offer both agreements cite.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StartBooks {
    #[serde(default)]
    pub at: String,
    pub offer: Record,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub own: Vec<BookRecord>,
    #[serde(default, deserialize_with = "null_as_empty")]
    pub counterpart: Vec<BookRecord>,
}

/// What an act does (Go `actKind`).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum ActKind {
    Pay,
    Refund,
    Perform,
}

/// The check an act cites and its kind; `None` for a record that is not an
/// act (Go `actCheck`).
pub(crate) fn act_check(r: &Record) -> Option<(Option<&Ref>, ActKind)> {
    match &r.body {
        Body::Perform(b) => Some((b.check.as_ref(), ActKind::Perform)),
        Body::Settle(b) if b.reverses.is_some() => Some((b.check.as_ref(), ActKind::Refund)),
        Body::Settle(b) => Some((b.check.as_ref(), ActKind::Pay)),
        _ => None,
    }
}

/// Derives Start's input from the two books (design §5.2) and computes it
/// (Go `StartFromBooks`).
pub fn start_from_books(b: &StartBooks) -> Result<(StartInput, StartResult), Error> {
    let input = start_input(b)?;
    let res = start(&input)?;
    Ok((input, res))
}

pub(crate) fn start_input(b: &StartBooks) -> Result<StartInput, Error> {
    let bad = |m: String| Error::Invalid(format!("xdealv1: start from books: {m}"));
    let Some(of) = b.offer.propose() else {
        return Err(bad(format!(
            "the offer is not a propose but a {} record",
            b.offer.block.record_type
        )));
    };
    let offer_digest = record_digest(&b.offer)?;
    // Go ranges over a map: either book's error may come first. The
    // vectors hold books with at most one fault.
    for (name, book) in [("own", &b.own), ("counterpart", &b.counterpart)] {
        for (i, r) in book.iter().enumerate() {
            if r.record.block.seq != (i + 1) as i64 {
                return Err(bad(format!(
                    "the {name} book's record {} has seq {}",
                    i + 1,
                    r.record.block.seq
                )));
            }
            if r.record.block.deal_id != b.offer.block.deal_id {
                return Err(bad(format!(
                    "the {name} book's record {} is of another deal",
                    i + 1
                )));
            }
        }
    }
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
    if own.offer.digest != offer_digest {
        return Err(bad("the holder's agreement does not cite the offer".into()));
    }
    let mut input = StartInput {
        at: b.at.clone(),
        side: own.side.clone(),
        agree_by: of.agree_by.clone(),
        counterparty_layer: own.layer(),
        ..Default::default()
    };
    if !input.counterparty_layer {
        for r in &b.own {
            if act_check(&r.record).is_some() {
                input.first_act = Some(FirstAct {
                    time: record_time(r, own.witness_list())?,
                });
                break;
            }
        }
        return Ok(input);
    }
    let (offerer_book, acceptor_book) = if own.side == SIDE_ACCEPTOR {
        (&b.counterpart, &b.own)
    } else {
        (&b.own, &b.counterpart)
    };
    let offerer_ids: Vec<&str> = offerer_book.iter().map(|r| r.record_id.as_str()).collect();
    for r in acceptor_book.iter() {
        if let Body::Statement(st) = &r.record.body {
            if st.names.as_ref().is_some_and(|n| n.cancellation.is_some())
                && !input.acceptor_names_offerer
            {
                input.cancellation_named_first = true;
            }
        }
        if let Some(s) = r.record.block.seen.value() {
            if offerer_ids.contains(&s.as_str()) {
                input.acceptor_names_offerer = true;
            }
        }
    }
    let Some(cp_first) = b.counterpart.first() else {
        return Ok(input);
    };
    let cp = match cp_first.record.agree() {
        Some(a) if a.kind == KIND_AGREEMENT => a,
        _ => {
            return Err(bad(
                "the counterpart's book does not open with its agreement".into(),
            ))
        }
    };
    input.counterpart_agreement_held = true;
    input.same_offer_and_rows =
        Some(cp.offer.digest == offer_digest && cp.rows_digest == own.rows_digest);
    let seen = offerer_book[0].record.block.seen.value();
    input.offerer_names_acceptor =
        Some(seen.map(String::as_str) == Some(acceptor_book[0].record_id.as_str()));
    let offerer = offerer_book[0].record.agree().cloned().unwrap_or_default();
    input.offerer_agreement_time = record_time(&offerer_book[0], offerer.witness_list())?;
    Ok(input)
}

/// A record's record time from the witnesses named: the earliest time one
/// of them gave it, or none (Go `recordTime`).
fn record_time(r: &BookRecord, witnesses: &[Witness]) -> Result<Option<String>, Error> {
    let mut earliest: Option<&String> = None;
    for w in witnesses {
        let Some(t) = r.times.get(&w.key_id) else {
            continue;
        };
        start_time(
            &format!("the time witness {} gave record {}", w.key_id, r.record_id),
            t,
        )?;
        if earliest.is_none_or(|e| t < e) {
            earliest = Some(t);
        }
    }
    Ok(earliest.cloned())
}
