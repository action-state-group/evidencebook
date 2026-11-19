// SPDX-License-Identifier: Apache-2.0

//! A sent record (design §3.1, §3.5): an offer, a decline or a
//! cancellation leaves the shelf whole as a standalone Capsule, and travels
//! as a pair `{"record": Capsule + signature + key_id, "agent_input":
//! record}`. [`verify_sent`] checks a pair on its own, as Go's
//! `xdealv1.VerifySent` does (`sent.go`); [`terms_leg`] is the settlement
//! terms leg an offer with one payment row carries (`payment.go`).

use serde_json::{json, Map, Value};

use super::check::{check, digest, record_digest, ref_to, stage_err};
use super::names::*;
use super::record::{decode, Record};
use super::types::*;
use crate::canonical::jcs;
use crate::deal::Error;

/// Ends a sent record's action_id: `<deal_id>/sent`.
pub const SENT_SUFFIX: &str = "/sent";
/// The stage a sent pair is read at on its own.
pub const STAGE_SENT: &str = "sent";
/// The Capsule member a settlement leg travels in.
pub const SETTLEMENT_MEMBER: &str = "settlement";
/// The settlement records version a terms leg carries.
pub const SETTLEMENT_VERSION: &str = "0";

fn bad(m: String) -> Error {
    stage_err(STAGE_SENT, m)
}

/// What a verified sent pair shows: the record, its record digest, the
/// Capsule's id and the key that signed it (lowercase hex).
#[derive(Clone, Debug, PartialEq)]
pub struct Sent {
    pub record: Record,
    pub digest: String,
    pub capsule_id: String,
    pub key_id: String,
}

/// The offer a sent decline or cancellation answers, when the reader holds
/// it: the offer record and its signer's key (lowercase hex).
#[derive(Clone, Debug, PartialEq)]
pub struct OfferContext {
    pub offer: Record,
    pub signer: String,
}

/// A sent record's pair from its sealed Capsule, its Producer Envelope and
/// its payload (Go `Pair`).
pub fn pair(capsule: &[u8], envelope: &[u8], payload: &[u8]) -> Result<Vec<u8>, Error> {
    let mut members: Map<String, Value> = match serde_json::from_slice(capsule)? {
        Value::Object(m) => m,
        _ => {
            return Err(Error::Invalid(
                "xdealv1: the Capsule is not a JSON object".into(),
            ))
        }
    };
    let id = members
        .get("capsule_id")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();
    let verified = agent_action_capsule::envelope::verify(&id, envelope);
    if !verified.ok {
        return Err(Error::Invalid(
            "xdealv1: the envelope does not verify".into(),
        ));
    }
    members.insert("signature".into(), json!(hex::encode(envelope)));
    members.insert("key_id".into(), json!(hex::encode(&verified.public_key)));
    let agent_input: Value = serde_json::from_slice(payload)?;
    // Go marshals a map: members sorted, agent_input first, and escapes as
    // Go does (go_marshal).
    let mut sorted: Vec<(String, Value)> = members.into_iter().collect();
    sorted.sort_by(|a, b| a.0.cmp(&b.0));
    let record: Map<String, Value> = sorted.into_iter().collect();
    let mut out = Map::new();
    out.insert("agent_input".into(), agent_input);
    out.insert("record".into(), Value::Object(record));
    Ok(go_marshal(&Value::Object(out))?)
}

/// `v` as Go's `json.Marshal` writes it, for a value whose strings came
/// from JCS: serde_json's compact text, with `<`, `>`, `&`, U+2028 and
/// U+2029 escaped as Go escapes them. Those characters occur only inside
/// strings, so escaping them in the whole text escapes them there.
pub(crate) fn go_marshal(v: &Value) -> Result<Vec<u8>, serde_json::Error> {
    let text = serde_json::to_string(v)?;
    let mut out = String::with_capacity(text.len());
    for c in text.chars() {
        match c {
            '<' => out.push_str("\\u003c"),
            '>' => out.push_str("\\u003e"),
            '&' => out.push_str("\\u0026"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            c => out.push(c),
        }
    }
    Ok(out.into_bytes())
}

/// Checks a sent pair on its own (Go `VerifySent`): the Capsule verifies
/// and is signed by its key_id; it is a standalone sent Capsule; its
/// agent_input_digest is agent_input's record digest; agent_input is a v1
/// propose, decline or cancellation that passes [`check`] and whose deal_id
/// is the action_id's; and, given the offer, a decline answers it signed by
/// the key it was sent to, a cancellation signed by its signer. Only an
/// offer with one payment row carries a settlement member, its terms leg.
pub fn verify_sent(pair: &[u8], offer: Option<&OfferContext>) -> Result<Sent, Error> {
    super::check::check_text(pair)?;
    let p: Value = serde_json::from_slice(pair)
        .map_err(|e| bad(format!("not a pair {{record, agent_input}}: {e}")))?;
    super::exact::exact_object(&p, &super::exact::SENT_PAIR, false, "the pair")
        .map_err(|e| bad(format!("not a pair {{record, agent_input}}: {e}")))?;
    let ok_shape = p.as_object().is_some_and(|o| {
        o.get("record").is_some_and(Value::is_object) && o.contains_key("agent_input")
    });
    if !ok_shape {
        return Err(bad("not a pair {record, agent_input}".into()));
    }
    let record = p["record"].as_object().cloned().unwrap_or_default();
    let agent_input = p["agent_input"].clone();
    let (capsule, _, capsule_id, key_id) = verify_capsule(STAGE_SENT, &record)?;
    let record = capsule.as_object().cloned().unwrap_or_default();
    let header = record.contains_key("evidence_record_header");
    let chained = record.get("chain").is_some_and(|c| !c.is_null());
    let fyi = capsule.get("action_type").and_then(Value::as_str) == Some("fyi");
    let references = capsule
        .get("references")
        .and_then(Value::as_array)
        .is_some_and(|a| !a.is_empty());
    if header || chained || !fyi || references {
        return Err(bad("not a standalone sent Capsule: fyi, no chain, no references, no evidence_record_header".into()));
    }
    let input_text = serde_json::to_vec(&agent_input)?;
    let d = digest(&input_text).map_err(|e| bad(format!("agent_input: {e}")))?;
    let attestation = capsule.get("model_attestation"); // vocabulary-gate: AAC capsule member
    let committed = attestation
        .and_then(|a| a.pointer("/compute_attestation/agent_input_digest"))
        .and_then(Value::as_str);
    if committed != Some(d.as_str()) {
        return Err(bad(
            "agent_input is not the record the Capsule commits (agent_input_digest)".into(),
        ));
    }
    check(&input_text)?;
    let r = decode(&input_text)?;
    check_terms_member(record.get(SETTLEMENT_MEMBER), &r)?;
    let action_id = capsule
        .get("action_id")
        .and_then(Value::as_str)
        .unwrap_or("");
    if action_id != format!("{}{SENT_SUFFIX}", r.block.deal_id) {
        return Err(bad(format!(
            "action_id {action_id:?} is not the record's deal_id followed by {SENT_SUFFIX}"
        )));
    }
    let ag = r.agree().cloned();
    let sendable = r.block.record_type == TYPE_PROPOSE
        || ag
            .as_ref()
            .is_some_and(|a| a.kind == KIND_DECLINE || a.kind == KIND_CANCELLATION);
    if !sendable {
        return Err(bad(format!(
            "a {} is not sent whole: only an offer, a decline or a cancellation is",
            r.block.record_type
        )));
    }
    let out = Sent {
        record: r.clone(),
        digest: d,
        capsule_id,
        key_id: key_id.clone(),
    };
    let (Some(ctx), Some(ag)) = (offer, ag) else {
        return Ok(out);
    };
    check(&serde_json::to_vec(&ctx.offer)?)?;
    let Some(of) = ctx.offer.propose() else {
        return Err(bad(format!(
            "the offer given is a {}",
            ctx.offer.block.record_type
        )));
    };
    let od = record_digest(&ctx.offer)?;
    if ag.offer != ref_to(&od) || r.block.deal_id != ctx.offer.block.deal_id {
        return Err(bad(format!("the {} does not answer this offer", ag.kind)));
    }
    if ag.kind == KIND_DECLINE && of.to.value() != Some(&key_id) {
        return Err(bad(
            "a decline is signed by the key the offer was sent to".into()
        ));
    }
    if ag.kind == KIND_CANCELLATION && ctx.signer != key_id {
        return Err(bad("a cancellation is signed by the offer's signer".into()));
    }
    Ok(out)
}

/// Checks a signed Capsule's members (Go `verifyCapsule`): the Capsule
/// verifies, and its `signature` verifies under its `key_id`. It returns the
/// Capsule without `signature` and `key_id`, its JCS bytes, its id and the
/// key, refusing at `stage`.
pub(crate) fn verify_capsule(
    stage: &'static str,
    members: &Map<String, Value>,
) -> Result<(Value, Vec<u8>, String, String), Error> {
    let bad = |m: &str| stage_err(stage, m.to_string());
    let text = |k: &str| members.get(k).and_then(Value::as_str).unwrap_or("");
    let (sig_hex, key_id) = (text("signature").to_string(), text("key_id").to_string());
    if sig_hex.is_empty() || key_id.is_empty() {
        return Err(bad("the Capsule carries no signature and key_id"));
    }
    let mut rest = members.clone();
    rest.remove("signature");
    rest.remove("key_id");
    let capsule = Value::Object(rest);
    let capsule_bytes = jcs(&capsule).map_err(|e| stage_err(stage, format!("the Capsule: {e}")))?;
    let format_ok = capsule.get("format_version").and_then(Value::as_str)
        == Some(crate::seal::FORMAT_VERSION)
        && capsule.get("canonicalization_id").and_then(Value::as_str)
            == Some(crate::canonical::CANONICALIZATION_JCS);
    let strict = agent_action_capsule::verify::decode_capsule_json(&capsule_bytes);
    let result = strict
        .as_ref()
        .ok()
        .map(|s| agent_action_capsule::verify::verify(s, None));
    let capsule_id = match &result {
        Some(r) if format_ok && r.ok && r.capsule_id.is_some() => r.capsule_id.clone().unwrap(),
        _ => return Err(bad("the Capsule does not verify")),
    };
    let Ok(envelope) = hex::decode(&sig_hex) else {
        return Err(bad("the signature is not hex"));
    };
    let verified = agent_action_capsule::envelope::verify(&capsule_id, &envelope);
    if !verified.ok {
        return Err(bad("the signature does not verify"));
    }
    if hex::encode(&verified.public_key) != key_id {
        return Err(bad("the Capsule is signed by another key than its key_id"));
    }
    // What the profile reads of it, by exact names (Go readCapsule).
    super::exact::exact_object(&capsule, &super::exact::CAPSULE, true, "the Capsule")
        .map_err(|e| stage_err(stage, e))?;
    Ok((capsule, capsule_bytes, capsule_id, key_id))
}

/// A row's owed as a settlement amount: amount_minor as the value, the
/// currency as the assetCode, and its assetScale (Go `owedAmount`).
pub(crate) fn owed_amount(row: &Row) -> Option<Value> {
    let owed = row.owed.as_object()?;
    if owed.len() != 2 {
        return None;
    }
    let minor = owed.get("amount_minor")?.as_i64()?;
    let currency = owed.get("currency")?.as_str()?;
    if minor < 0 {
        return None;
    }
    let scale = crate::deal::standard_history::history_asset_scale(currency)?;
    Some(json!({"value": minor.to_string(), "assetCode": currency, "assetScale": scale}))
}

pub(crate) fn is_payment_row(row: &Row) -> bool {
    row.recomputed && owed_amount(row).is_some()
}

/// The ids of the payment rows of an offer's terms, in order (Go
/// `PaymentRows`).
pub fn payment_rows(tr: &TermsRows) -> Vec<String> {
    tr.rows
        .iter()
        .filter(|r| is_payment_row(r))
        .map(|r| r.id.clone())
        .collect()
}

/// The terms leg an offer's sent Capsule carries as its settlement member,
/// as JCS bytes (Go `TermsLeg`). It refuses an offer whose terms have no
/// payment row or more than one.
pub fn terms_leg(offer: &Record) -> Result<Vec<u8>, Error> {
    let Some(of) = offer.propose() else {
        return Err(Error::Invalid(format!(
            "xdealv1: a terms leg is an offer's; this is a {}",
            offer.block.record_type
        )));
    };
    let found: Vec<&Row> = of
        .terms_rows
        .rows
        .iter()
        .filter(|r| is_payment_row(r))
        .collect();
    if found.len() != 1 {
        return Err(Error::Invalid(format!(
            "xdealv1: a terms leg carries one payment's amount; the terms have {} payment rows",
            found.len()
        )));
    }
    let row = found[0];
    let role = if of.role == row.owed_by {
        "payer"
    } else {
        "payee"
    };
    Ok(jcs(&json!({
        "version": SETTLEMENT_VERSION,
        "leg": "terms",
        "sealer_role": role,
        "amount": owed_amount(row),
    }))?)
}

fn check_terms_member(member: Option<&Value>, r: &Record) -> Result<(), Error> {
    let one_payment = r
        .propose()
        .is_some_and(|of| payment_rows(&of.terms_rows).len() == 1);
    if !one_payment {
        if member.is_some() {
            return Err(bad(format!(
                "this sent {} carries no settlement member; only an offer with one payment row carries its terms leg",
                r.block.record_type
            )));
        }
        return Ok(());
    }
    let want = terms_leg(r).map_err(|e| bad(format!("the terms leg: {e}")))?;
    let Some(member) = member else {
        return Err(bad(
            "an offer with one payment row carries its terms leg as the settlement member".into(),
        ));
    };
    if jcs(member).ok().as_deref() != Some(want.as_slice()) {
        return Err(bad(format!(
            "the settlement member is not the offer's terms leg {}",
            String::from_utf8_lossy(&want)
        )));
    }
    Ok(())
}
