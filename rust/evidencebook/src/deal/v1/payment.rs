// SPDX-License-Identifier: Apache-2.0

//! The payment check (design §6.3; Go `payment.go`'s `PaymentCheck`): the
//! recomputed check of a payment row over AAC's settlement legs
//! (draft-mih-agent-settlement-records-01), with AAC's verifier
//! ([`agent_action_capsule::settlement::verify_settlements`]), so no second
//! join of legs exists here.
//!
//! The terms leg is the offer's: the offer's sent Capsule carries it as its
//! top-level settlement member, sealed by the offerer in the role its side
//! has in the payment. Each side's payment leg's terms_ref is the sent
//! Capsule's id. A terms leg has one amount, so the check runs only on an
//! offer with exactly one payment row, and on that row's own payment.

use std::collections::{BTreeMap, HashMap, HashSet};

use agent_action_capsule::settlement::{verify_settlements, SettlementOptions};
use serde_json::{Map, Value};

use super::book::{verify_book, BookRead};
use super::check::{ref_to, stage_err};
use super::names::*;
use super::record::Body;
use super::sent::{is_payment_row, payment_rows, verify_sent, SETTLEMENT_MEMBER};
use super::status_value::PaymentResult;
use crate::deal::{Error, Ref};

/// The stage a payment check's inputs are read at ([`payment_check`]).
pub const STAGE_PAYMENT: &str = "payment";

/// A payment check that passed.
pub const PAYMENT_PASS: &str = "pass";
/// A payment check that failed.
pub const PAYMENT_FAIL: &str = "fail";

/// The terms have more than one payment row.
pub const REASON_SEVERAL_PAYMENTS: &str = "several_payments";
/// A refund or remedy leg cites the offer's terms leg.
pub const REASON_REFUND_CITES_OFFER: &str = "refund_cites_offer";
/// One side's legs, or none: AAC's terms_only, payer_stated, payee_stated.
pub const REASON_NOT_AGREED: &str = "not_agreed";
/// Both sides' legs, which AAC cannot join (payment_ref types, fees).
pub const REASON_UNJOINED: &str = "unjoined";
/// Agreed, with status pending.
pub const REASON_PENDING: &str = "pending";
/// The two sides' legs differ.
pub const REASON_MISMATCH: &str = "mismatch";
/// Agreed, with status failed or reversed.
pub const REASON_STATUS: &str = "status";
/// Agreed, with an amount other than the terms.
pub const REASON_AMOUNT: &str = "amount";

fn bad(m: impl Into<String>) -> Error {
    stage_err(STAGE_PAYMENT, m.into())
}

/// What [`payment_check`] reads (Go `PaymentInput`).
#[derive(Clone, Debug, Default, PartialEq)]
pub struct PaymentInput {
    /// The started deal's offer as sent: its pair `{record, agent_input}`,
    /// whose Capsule carries the terms leg.
    pub offer: Vec<u8>,
    /// The record digest of the offer both agreements cite.
    pub offer_digest: String,
    /// The acceptor's key (lowercase hex): the key its agreement and its
    /// deal book are signed by. An offer sent to a key names it in `to`,
    /// and then it must be that key.
    pub acceptor_key: String,
    /// The payment row checked.
    pub row: String,
    /// Records of both deal books as book pairs
    /// ([`super::book_pair`]): the holder's own and the counted prefix of
    /// the counterparty's. Records other than settle legs are passed over.
    pub legs: Vec<Vec<u8>>,
}

/// The settlement role of `party` (buyer or seller) for a row `owed_by`
/// owes (Go `settlementRole`).
fn settlement_role(party: &str, owed_by: &str) -> &'static str {
    if party == owed_by {
        "payer"
    } else {
        "payee"
    }
}

/// A pair's `record` member, the signed Capsule, as an object.
fn record_of(pair: &[u8]) -> Result<Map<String, Value>, Error> {
    let p: Value = serde_json::from_slice(pair)?;
    Ok(p.get("record")
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_default())
}

/// Runs the check of a payment row (Go `PaymentCheck`), returning the
/// [`PaymentResult`] a status value carries: pass when the
/// payer's and the payee's legs answering the offer's terms leg are agreed,
/// with status settled and the terms' amount; fail on a mismatch, and with
/// agreed legs on a status of failed or reversed or another amount; not run
/// otherwise, and when the offer's terms have more than one payment row.
///
/// It refuses an offer that does not verify ([`verify_sent`]) or is not the
/// one both agreements cite, a row that is not a payment row, a leg that
/// does not verify ([`verify_book`]), is of another deal, is signed by
/// neither party's key or carries a chain. A leg whose side, leg or role
/// disagree, or that AAC refuses, takes no part (`excluded`) rather than
/// stopping the check. A leg superseded by a later leg of its side is not
/// read; a refund or remedy leg that cites the offer's terms leg, and is not
/// superseded, leaves the check not run.
pub fn payment_check(input: &PaymentInput) -> Result<PaymentResult, Error> {
    let sent = verify_sent(&input.offer, None)?;
    if sent.digest != input.offer_digest {
        return Err(bad(format!(
            "the offer given is not the started deal's offer {}",
            input.offer_digest
        )));
    }
    let Some(of) = sent.record.propose() else {
        return Err(bad(format!(
            "the offer given is a {}",
            sent.record.block.record_type
        )));
    };
    if of.to.value().is_some_and(|to| *to != input.acceptor_key) {
        return Err(bad(
            "the acceptor's key is not the key the offer was sent to",
        ));
    }
    if input.acceptor_key.is_empty() || input.acceptor_key == sent.key_id {
        return Err(bad("the acceptor's key is empty or the offerer's"));
    }
    let row = of.terms_rows.rows.iter().rev().find(|r| r.id == input.row);
    let Some(row) = row.filter(|r| is_payment_row(r)) else {
        return Err(bad(format!(
            "{:?} is not a payment row of the offer",
            input.row
        )));
    };
    let mut out = PaymentResult {
        checked_over: vec![sent.digest.clone()],
        ..Default::default()
    };
    if payment_rows(&of.terms_rows).len() != 1 {
        out.reason = REASON_SEVERAL_PAYMENTS.into();
        return Ok(out);
    }
    let offer_record = record_of(&input.offer)?;

    // The parties' keys and payment roles: the payer owes the payment row.
    let offerer = of.role.as_str();
    let acceptor = if offerer == ROLE_BUYER {
        ROLE_SELLER
    } else {
        ROLE_BUYER
    };
    let mut role_of: HashMap<String, &'static str> = HashMap::new();
    role_of.insert(sent.key_id.clone(), settlement_role(offerer, &row.owed_by));
    role_of.insert(
        input.acceptor_key.clone(),
        settlement_role(acceptor, &row.owed_by),
    );

    struct LegRead {
        read: BookRead,
        capsule: Map<String, Value>,
        refund: bool,
    }
    let mut legs: Vec<LegRead> = Vec::new();
    // Every settle record's supersedes counts, whichever terms leg it
    // answers: a refund replaced by one under its own terms leg is replaced.
    let mut supersedes: Vec<(String, Ref, u64)> = Vec::new();
    for (i, raw) in input.legs.iter().enumerate() {
        let read = verify_book(raw)?;
        if read.record.block.deal_id != sent.record.block.deal_id {
            return Err(bad(format!(
                "leg {i} is of deal {:?}, not the offer's",
                read.record.block.deal_id
            )));
        }
        let Body::Settle(body) = &read.record.body else {
            continue;
        };
        let body = body.clone();
        let Some(&role) = role_of.get(&read.key_id) else {
            return Err(bad(format!("leg {i} is signed by neither party's key")));
        };
        if let Some(target) = &body.supersedes {
            supersedes.push((read.key_id.clone(), target.clone(), read.seq));
        }
        let capsule = record_of(raw)?;
        // A deal-book record's Capsule has no chain. AAC's verifier reads a
        // chain's supersedes to drop a leg, which would replace the deal's
        // own supersedes rule below.
        if capsule.get("chain").is_some_and(|c| !c.is_null()) {
            return Err(bad(format!(
                "leg {i}'s Capsule carries a chain; a deal-book record's has none"
            )));
        }
        // From here a leg's own content is the counterparty's to get wrong:
        // a leg that is not a leg of this payment takes no part.
        let Some(member) = capsule.get(SETTLEMENT_MEMBER).and_then(member_of) else {
            out.excluded.push(read.digest.clone());
            continue;
        };
        if member.terms_ref != sent.capsule_id {
            continue; // it answers another terms leg
        }
        let refund = body.reverses.is_some() || body.remedy_for.is_some();
        if !refund
            && (body.leg != role
                || member.leg != format!("{role}_observed")
                || member.sealer_role != role)
        {
            out.excluded.push(read.digest.clone());
            continue;
        }
        legs.push(LegRead {
            read,
            capsule,
            refund,
        });
    }

    // A leg superseded by a later leg of its side is not read.
    let superseded: HashSet<String> = legs
        .iter()
        .filter(|l| {
            supersedes.iter().any(|(key, target, seq)| {
                *key == l.read.key_id && *target == ref_to(&l.read.digest) && l.read.seq < *seq
            })
        })
        .map(|l| l.read.digest.clone())
        .collect();
    // A refund or remedy that is a payment needs its own terms leg; one
    // that cites the offer's leaves the check not run.
    if legs
        .iter()
        .any(|l| l.refund && !superseded.contains(&l.read.digest))
    {
        out.reason = REASON_REFUND_CITES_OFFER.into();
        return Ok(out);
    }
    let mut capsules = vec![Value::Object(offer_record)];
    let mut digest_of: HashMap<String, String> = HashMap::new(); // capsule id -> record digest
    let mut read = Vec::new();
    for l in &legs {
        if superseded.contains(&l.read.digest) {
            continue;
        }
        capsules.push(Value::Object(l.capsule.clone()));
        digest_of.insert(l.read.record_id.clone(), l.read.digest.clone());
        read.push(l.read.digest.clone());
    }
    let mut policy: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for (key, role) in &role_of {
        policy
            .entry((*role).to_string())
            .or_default()
            .push(key.clone());
    }
    for keys in policy.values_mut() {
        keys.sort();
    }
    let res = verify_settlements(
        &capsules,
        &SettlementOptions {
            key_policy: Some(policy),
            ..Default::default()
        },
    );
    // A leg AAC's verifier refuses takes no part in its state.
    let mut refused = HashSet::new();
    for f in &res.failures {
        for r in &f.records {
            match r.as_ref().and_then(|id| digest_of.get(id)) {
                Some(d) => {
                    refused.insert(d.clone());
                }
                None => {
                    return Err(bad(format!(
                        "AAC's verifier refuses the offer's terms leg: {}",
                        f.code
                    )))
                }
            }
        }
    }
    for d in read {
        if refused.contains(&d) {
            out.excluded.push(d);
        } else {
            out.checked_over.push(d);
        }
    }
    let Some(s) = res
        .settlements
        .iter()
        .rev()
        .find(|s| s.terms == sent.capsule_id)
    else {
        return Err(bad(format!(
            "AAC's verifier read no terms leg {}",
            sent.capsule_id
        )));
    };
    out.settlement = Some(s.to_json());
    let agreed = s.agreed_status.as_deref().unwrap_or("");
    match s.payment_state.as_deref() {
        Some("unjoined") => out.reason = REASON_UNJOINED.into(),
        Some("mismatch") => {
            out.check = PAYMENT_FAIL.into();
            out.reason = REASON_MISMATCH.into();
        }
        Some("agreed") if agreed == "failed" || agreed == "reversed" => {
            out.check = PAYMENT_FAIL.into();
            out.reason = REASON_STATUS.into();
        }
        Some("agreed") if s.terms_amount.as_deref() != Some("equal") => {
            out.check = PAYMENT_FAIL.into();
            out.reason = REASON_AMOUNT.into();
        }
        Some("agreed") if agreed == "settled" => out.check = PAYMENT_PASS.into(),
        Some("agreed") => out.reason = REASON_PENDING.into(),
        _ => out.reason = REASON_NOT_AGREED.into(),
    }
    Ok(out)
}

/// A settlement member's `leg`, `sealer_role` and `terms_ref`, as Go reads
/// them into strings: a null member, or an absent or null one of these, is
/// `""`; `None` when the member is not an object, spells one of these
/// otherwise (Go's `exactObject`, evidencebook#559), or holds one that is
/// not a string (Go's unmarshal fails).
struct Member {
    leg: String,
    sealer_role: String,
    terms_ref: String,
}

fn member_of(v: &Value) -> Option<Member> {
    if v.is_null() {
        return Some(Member {
            leg: String::new(),
            sealer_role: String::new(),
            terms_ref: String::new(),
        });
    }
    let o = v.as_object()?;
    super::exact::exact_object(v, &super::exact::SETTLEMENT, true, "the settlement member").ok()?;
    let text = |k: &str| match o.get(k) {
        None | Some(Value::Null) => Some(String::new()),
        Some(Value::String(s)) => Some(s.clone()),
        Some(_) => None,
    };
    Some(Member {
        leg: text("leg")?,
        sealer_role: text("sealer_role")?,
        terms_ref: text("terms_ref")?,
    })
}
