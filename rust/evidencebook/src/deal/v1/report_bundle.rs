//! The x-deal-v1 bundle extension (Go `ReportBundle`, `ReportBook` and
//! `VerifyReportBundle`; design §9 "The bundle extension", PROFILE §10): what
//! a bundle carrying a v1 report adds so a reader can verify the report and
//! read the records its rows rely on, the payment legs a payment check read
//! included. The host builds and seals the bundle; this module defines the
//! extension and verifies it.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

use super::book::verify_book;
use super::check::{check_text, stage_err};
use super::exact::{exact_object, Spec};
use super::names::*;
use super::report::{ROWS_LISTED, ROWS_NOT_STARTED, ROWS_WITHHELD, SIDE_ADJUDICATOR};
use super::report_verify::{verify_report, ReportRead, STAGE_REPORT};
use super::sent::verify_sent;
use super::start::STATE_STARTED;
use crate::deal::names::{AUDIENCE_ADJUDICATOR, AUDIENCE_COUNTERPARTY, AUDIENCE_KEEP};
use crate::deal::Error;

/// A member kept as written when it is `null`, as Go's `json.RawMessage`
/// keeps it (a null is the bytes `null`, not an absent member).
fn keep_null<'de, D: Deserializer<'de>>(d: D) -> Result<Option<Value>, D::Error> {
    Value::deserialize(d).map(Some)
}

/// The extension's book (Go `ReportBook`): the holder's deal book record at
/// seq 1, the agreement [`verify_report`] reads the report against, and every
/// record the report's rows rely on, by record id, each as its book pair
/// `{record, header, payload}`. The relied-on records are those of every row
/// the holder computed, so a copy whose rows are withheld (an adjudicator's)
/// or not started carries the same records as one that lists them.
///
/// A member left out or `null` is `None` (a `null` relied_on is a nil map in
/// Go), and a verified bundle carries both.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportBook {
    #[serde(default, deserialize_with = "keep_null")]
    pub agreement: Option<Value>,
    #[serde(default)]
    pub relied_on: Option<BTreeMap<String, Value>>,
    /// The offer the agreement cites, as its sent pair `{record,
    /// agent_input}` (Go `ReportBook.Offer`); a `null` is kept, and refused.
    #[serde(
        default,
        deserialize_with = "keep_null",
        skip_serializing_if = "Option::is_none"
    )]
    pub offer: Option<Value>,
}

/// The x-deal-v1 bundle extension (Go `ReportBundle`): the sealed report's
/// Capsule id, as x-deal-v0's `sealed_report`, and the book.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReportBundle {
    #[serde(default)]
    pub sealed_report: Option<String>,
    #[serde(default)]
    pub book: Option<ReportBook>,
}

const EXTENSION: Spec = Spec::Obj(&[
    ("sealed_report", Spec::Leaf),
    (
        "book",
        Spec::Obj(&[
            ("agreement", Spec::Leaf),
            ("relied_on", Spec::Leaf),
            ("offer", Spec::Leaf),
        ]),
    ),
]);

fn bad(m: String) -> Error {
    stage_err(STAGE_REPORT, format!("the x-deal-v1 extension: {m}"))
}

/// Checks a sealed v1 report (its pair, as [`verify_report`] reads it) with
/// the x-deal-v1 bundle extension that carries it, `ext` (Go
/// `VerifyReportBundle`), so every host and page applies one rule. `ext` is
/// exactly `{sealed_report, book: {agreement, relied_on}}`, `relied_on` an
/// object (empty when no row relies on a record), its book optionally also
/// carrying `offer`. It is refused unless:
///
/// - the report passes [`verify_report`] against `book.agreement`, and
///   `sealed_report` is the report Capsule's id;
/// - the copy's audience and rows agree: an adjudicator's copy is withheld
///   with no rows, no other copy is withheld, rows appear only when listed,
///   and a keep or counterparty copy is listed exactly when its deal has
///   started;
/// - each relied_on pair passes [`verify_book`], is of the report's deal, has
///   its record id as its key, and is signed by a party of the agreement: the
///   agreement's own key (the holder), its counterparty_key, or its
///   adjudicator's key; the holder's sit in rests_on.own's log at a seq no
///   greater than its size, and, where the copy names rests_on.counterparty,
///   the counterparty's sit in that log at a seq no greater than its
///   counted; a keep or counterparty copy that names none carries no
///   counterparty record, and in an adjudicator's copy the counterparty's
///   records sit in one log;
/// - an adjudicator's copy, and only it, names a referee;
/// - when the rows are listed, the keys are exactly the records the rows name
///   in relied_on, each named with the side of its pair's signer;
/// - an offer, carried only in a keep or counterparty copy, passes
///   [`verify_sent`], is a propose whose record digest is the offer the
///   agreement cites, and is signed by the offerer: the agreement's own key
///   when the holder made the offer, else its counterparty_key.
///
/// A bundle that passes shows the records the holder says its rows rest on,
/// signed by the deal's parties; like the report, it is not a recomputation.
/// In a copy whose rows are not listed nothing ties the keys to rows; an
/// adjudicator's records have no log bound, and in an adjudicator's copy,
/// which names no counterparty prefix, the counterparty's records are bound
/// only to one log.
pub fn verify_report_bundle(report: &[u8], ext: &[u8]) -> Result<ReportRead, Error> {
    check_text(ext)?;
    let doc: Value = serde_json::from_slice(ext).map_err(|e| bad(e.to_string()))?;
    // serde reads a list into a struct by field order; Go reads only an
    // object (or null) into a struct, the extension and its book both.
    let struct_like = |v: &Value| matches!(v, Value::Object(_) | Value::Null);
    if !struct_like(&doc) || !doc.get("book").is_none_or(struct_like) {
        return Err(bad(
            "json: cannot unmarshal a value that is not an object into a Go struct".into(),
        ));
    }
    let x = ReportBundle::deserialize(&doc).map_err(|e| bad(e.to_string()))?;
    exact_object(&doc, &EXTENSION, false, "the extension").map_err(bad)?;
    let book = x.book.unwrap_or_default();
    let (Some(agreement), Some(relied_on)) = (book.agreement, book.relied_on) else {
        return Err(bad(
            "the book carries an agreement and a relied_on object".into()
        ));
    };
    let agreement = to_bytes(&agreement)?;
    let read = verify_report(report, &agreement)?;
    let r = &read.report;
    let sealed = x.sealed_report.unwrap_or_default();
    if sealed != read.capsule_id {
        return Err(bad(format!(
            "sealed_report {sealed:?} is not the report Capsule's id {}",
            read.capsule_id
        )));
    }
    let adj = r.audience == AUDIENCE_ADJUDICATOR;
    if r.audience != AUDIENCE_KEEP && r.audience != AUDIENCE_COUNTERPARTY && !adj {
        return Err(bad(format!(
            "audience {:?} is not a copy audience",
            r.audience
        )));
    }
    if adj != !r.referee.is_empty() {
        return Err(bad(
            "an adjudicator's copy, and only it, names a referee".into()
        ));
    }
    if adj != (r.rows_state == ROWS_WITHHELD) {
        return Err(bad(format!(
            "an adjudicator's copy, and only it, is withheld; this {} copy is {:?}",
            r.audience, r.rows_state
        )));
    }
    if r.rows_state != ROWS_LISTED && !r.rows.is_empty() {
        return Err(bad(format!("a {} copy lists no rows", r.rows_state)));
    }
    let listed = r.rows_state == ROWS_LISTED;
    if !adj && listed != (r.start.state == STATE_STARTED) {
        return Err(bad(format!(
            "a keep or counterparty copy is listed exactly when its deal has started; this one is {:?} at start {:?}",
            r.rows_state, r.start.state
        )));
    }
    if !adj && !listed && r.rows_state != ROWS_NOT_STARTED {
        return Err(bad(format!(
            "rows_state {:?} is not one of listed, not_started, withheld",
            r.rows_state
        )));
    }

    let first = verify_book(&agreement)?;
    // verify_report checked it is the agreement.
    let ag = first.record.agree().cloned().unwrap_or_default();
    let other = if ag.role == ROLE_BUYER {
        ROLE_SELLER
    } else {
        ROLE_BUYER
    };
    let mut side_of: BTreeMap<String, &str> = BTreeMap::new();
    side_of.insert(first.key_id.clone(), ag.role.as_str());
    if let Some(k) = ag.counterparty_key.value() {
        side_of.insert(k.clone(), other);
    }
    if let Some(a) = ag.adjudicator.value() {
        side_of.insert(a.key_id.clone(), SIDE_ADJUDICATOR);
    }
    if let Some(offer) = &book.offer {
        if adj {
            return Err(bad("an adjudicator's copy carries no offer".into()));
        }
        let sent =
            verify_sent(&to_bytes(offer)?, None).map_err(|e| bad(format!("the offer: {e}")))?;
        let offerer = if ag.side == SIDE_OFFERER {
            first.key_id.clone()
        } else {
            ag.counterparty_key.value().cloned().unwrap_or_default()
        };
        if sent.record.block.record_type != TYPE_PROPOSE {
            return Err(bad(format!(
                "the offer is a {} record, not a propose",
                sent.record.block.record_type
            )));
        }
        if sent.record.block.deal_id != r.deal_id {
            return Err(bad(format!(
                "the offer is of deal {:?}, not the report's",
                sent.record.block.deal_id
            )));
        }
        if sent.digest != ag.offer.digest {
            return Err(bad(format!(
                "the offer {} is not the offer {} the agreement cites",
                sent.digest, ag.offer.digest
            )));
        }
        if sent.key_id != offerer {
            return Err(bad("the offer is not signed by the deal's offerer".into()));
        }
    }
    let mut sides: BTreeMap<&str, &str> = BTreeMap::new();
    let mut counterpart_log = String::new();
    for (id, pair) in &relied_on {
        let b = verify_book(&to_bytes(pair)?)?;
        if &b.record_id != id {
            return Err(bad(format!(
                "relied_on {id} holds the record {}",
                b.record_id
            )));
        }
        if b.record.block.deal_id != r.deal_id {
            return Err(bad(format!(
                "relied_on {id} is of deal {:?}, not the report's",
                b.record.block.deal_id
            )));
        }
        let Some(&side) = side_of.get(&b.key_id) else {
            return Err(bad(format!(
                "relied_on {id} is signed by no party of the agreement"
            )));
        };
        let theirs = side == other;
        if side == ag.role && (b.log_id != r.rests_on.own.log_id || b.seq > r.rests_on.own.size) {
            return Err(bad(format!(
                "relied_on {id} is not in the holder's book the report rests on"
            )));
        }
        let cp = &r.rests_on.counterparty;
        if theirs
            && cp
                .as_ref()
                .is_some_and(|c| b.log_id != c.log_id || b.seq > c.counted)
        {
            return Err(bad(format!(
                "relied_on {id} is not in the counterparty's prefix the report rests on"
            )));
        }
        if theirs && cp.is_none() && !adj {
            return Err(bad(format!(
                "relied_on {id} is the counterparty's, and the report counted none of its book"
            )));
        }
        // An adjudicator's copy names no counterparty prefix: its records
        // sit in one book at least.
        if theirs && !counterpart_log.is_empty() && b.log_id != counterpart_log {
            return Err(bad(format!(
                "relied_on {id} is in another counterparty book than {counterpart_log}"
            )));
        }
        if theirs {
            counterpart_log = b.log_id.clone();
        }
        sides.insert(id, side);
    }
    if !listed {
        return Ok(read);
    }
    let mut named: BTreeSet<&str> = BTreeSet::new();
    for row in &r.rows {
        for src in &row.relied_on {
            let Some(&side) = sides.get(src.record.as_str()) else {
                return Err(bad(format!(
                    "row {:?} relies on {}, which relied_on does not carry",
                    row.id, src.record
                )));
            };
            if side != src.side {
                return Err(bad(format!(
                    "row {:?} names {} as the {}'s, and it is signed by the {}",
                    row.id, src.record, src.side, side
                )));
            }
            named.insert(src.record.as_str());
        }
    }
    for id in sides.keys() {
        if !named.contains(id) {
            return Err(bad(format!(
                "relied_on carries {id}, on which no row relies"
            )));
        }
    }
    Ok(read)
}

fn to_bytes(v: &Value) -> Result<Vec<u8>, Error> {
    serde_json::to_vec(v).map_err(|e| bad(e.to_string()))
}
