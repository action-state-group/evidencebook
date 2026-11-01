// SPDX-License-Identifier: Apache-2.0

//! A step record as one value: its x-deal-v0 block and its body, typed by
//! record type (Go `dealprofile.Record`), and [`decode`], which reads a
//! record into the record types.

use serde::de::{DeserializeOwned, Error as _};
use serde::ser::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::Value;

use super::check::Error;
use super::names::*;
use super::types::*;
use crate::canonical::jcs;

/// A step record's body, one variant per record type (PROFILE.md §3).
#[derive(Clone, Debug, PartialEq)]
pub enum Body {
    Intent(IntentBody),
    Baseline(BaselineBody),
    Message(MessageBody),
    Claim(Claim),
    Evidence(EvidenceBody),
    DetailChange(DetailChangeBody),
    Check(CheckBody),
    Verdict(VerdictBody),
    Approval(ApprovalBody),
    Action(ActionBody),
    Outcome(OutcomeBody),
    Close(CloseBody),
    Disclosure(DisclosureBody),
    Sale(SaleBody),
    CounterpartyProfile(CounterpartyProfileBody),
    Thread(ThreadBody),
    ThreadOpened(ThreadOpenedBody),
    SaleCut(SaleCutBody),
    SaleClose(SaleCloseBody),
    AgentMove(AgentMoveBody),
}

impl Body {
    /// The record type the body is the body of.
    pub fn record_type(&self) -> &'static str {
        match self {
            Body::Intent(_) => TYPE_INTENT,
            Body::Baseline(_) => TYPE_BASELINE,
            Body::Message(_) => TYPE_MESSAGE,
            Body::Claim(_) => TYPE_CLAIM,
            Body::Evidence(_) => TYPE_EVIDENCE,
            Body::DetailChange(_) => TYPE_DETAIL_CHANGE,
            Body::Check(_) => TYPE_CHECK,
            Body::Verdict(_) => TYPE_VERDICT,
            Body::Approval(_) => TYPE_APPROVAL,
            Body::Action(_) => TYPE_ACTION,
            Body::Outcome(_) => TYPE_OUTCOME,
            Body::Close(_) => TYPE_CLOSE,
            Body::Disclosure(_) => TYPE_DISCLOSURE,
            Body::Sale(_) => TYPE_SALE,
            Body::CounterpartyProfile(_) => TYPE_COUNTERPARTY_PROFILE,
            Body::Thread(_) => TYPE_THREAD,
            Body::ThreadOpened(_) => TYPE_THREAD_OPENED,
            Body::SaleCut(_) => TYPE_SALE_CUT,
            Body::SaleClose(_) => TYPE_SALE_CLOSE,
            Body::AgentMove(_) => TYPE_AGENT_MOVE,
        }
    }

    fn from_value(record_type: &str, body: Value) -> Result<Body, serde_json::Error> {
        Ok(match record_type {
            TYPE_INTENT => Body::Intent(serde_json::from_value(body)?),
            TYPE_BASELINE => Body::Baseline(serde_json::from_value(body)?),
            TYPE_MESSAGE => Body::Message(serde_json::from_value(body)?),
            TYPE_CLAIM => Body::Claim(serde_json::from_value(body)?),
            TYPE_EVIDENCE => Body::Evidence(serde_json::from_value(body)?),
            TYPE_DETAIL_CHANGE => Body::DetailChange(serde_json::from_value(body)?),
            TYPE_CHECK => Body::Check(serde_json::from_value(body)?),
            TYPE_VERDICT => Body::Verdict(serde_json::from_value(body)?),
            TYPE_APPROVAL => Body::Approval(serde_json::from_value(body)?),
            TYPE_ACTION => Body::Action(serde_json::from_value(body)?),
            TYPE_OUTCOME => Body::Outcome(serde_json::from_value(body)?),
            TYPE_CLOSE => Body::Close(serde_json::from_value(body)?),
            TYPE_DISCLOSURE => Body::Disclosure(serde_json::from_value(body)?),
            TYPE_SALE => Body::Sale(serde_json::from_value(body)?),
            TYPE_COUNTERPARTY_PROFILE => Body::CounterpartyProfile(serde_json::from_value(body)?),
            TYPE_THREAD => Body::Thread(serde_json::from_value(body)?),
            TYPE_THREAD_OPENED => Body::ThreadOpened(serde_json::from_value(body)?),
            TYPE_SALE_CUT => Body::SaleCut(serde_json::from_value(body)?),
            TYPE_SALE_CLOSE => Body::SaleClose(serde_json::from_value(body)?),
            TYPE_AGENT_MOVE => Body::AgentMove(serde_json::from_value(body)?),
            other => {
                return Err(<serde_json::Error as serde::de::Error>::custom(format!(
                    "record type {other:?} has no body type"
                )))
            }
        })
    }
}

impl Serialize for Body {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        match self {
            Body::Intent(b) => b.serialize(s),
            Body::Baseline(b) => b.serialize(s),
            Body::Message(b) => b.serialize(s),
            Body::Claim(b) => b.serialize(s),
            Body::Evidence(b) => b.serialize(s),
            Body::DetailChange(b) => b.serialize(s),
            Body::Check(b) => b.serialize(s),
            Body::Verdict(b) => b.serialize(s),
            Body::Approval(b) => b.serialize(s),
            Body::Action(b) => b.serialize(s),
            Body::Outcome(b) => b.serialize(s),
            Body::Close(b) => b.serialize(s),
            Body::Disclosure(b) => b.serialize(s),
            Body::Sale(b) => b.serialize(s),
            Body::CounterpartyProfile(b) => b.serialize(s),
            Body::Thread(b) => b.serialize(s),
            Body::ThreadOpened(b) => b.serialize(s),
            Body::SaleCut(b) => b.serialize(s),
            Body::SaleClose(b) => b.serialize(s),
            Body::AgentMove(b) => b.serialize(s),
        }
    }
}

/// A step record: the x-deal-v0 block and its body. Its JSON is
/// `{"x-deal-v0": block, "body": body}`; [`encode`](super::encode) takes
/// that text (`serde_json::to_vec(&record)`). Serializing refuses a body
/// whose record type is not the block's.
#[derive(Clone, Debug, PartialEq)]
pub struct Record {
    pub block: Block,
    pub body: Body,
    /// SH §7's `counterparty_class`, a top-level member of the one record
    /// that recorded how the deal was paid: only a pay check carries it
    /// (PROFILE.md section 11). `None` is absent.
    pub counterparty_class: Option<String>,
}

#[derive(Serialize)]
struct RecordOut<'a> {
    #[serde(rename = "x-deal-v0")]
    block: &'a Block,
    body: &'a Body,
    #[serde(skip_serializing_if = "Option::is_none")]
    counterparty_class: &'a Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordIn {
    #[serde(rename = "x-deal-v0")]
    block: Block,
    body: Value,
    #[serde(default)]
    counterparty_class: Option<String>,
}

impl Serialize for Record {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        let t = self.body.record_type();
        if t != self.block.record_type {
            return Err(S::Error::custom(format!(
                "{t} body on a {} record",
                self.block.record_type
            )));
        }
        RecordOut {
            block: &self.block,
            body: &self.body,
            counterparty_class: &self.counterparty_class,
        }
        .serialize(s)
    }
}

impl<'de> Deserialize<'de> for Record {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Record, D::Error> {
        let r = RecordIn::deserialize(d)?;
        // Go's Record cannot hold an empty class (omitempty drops it, so its
        // decode refuses the text): neither can this one.
        if r.counterparty_class.as_deref() == Some("") {
            return Err(D::Error::custom("counterparty_class is empty"));
        }
        let body = Body::from_value(&r.block.record_type, r.body).map_err(D::Error::custom)?;
        Ok(Record {
            block: r.block,
            body,
            counterparty_class: r.counterparty_class,
        })
    }
}

/// Reads a record's JSON text into `T`: a [`Record`], or one of the typed
/// record or contract document types. It refuses a member `T` does not
/// name, and text `T` would not encode back to (such as a null where `T`
/// holds a required value). A key repeated in an object is read last-wins
/// everywhere, as Go's `Decode` and `Check` and the reference checker read
/// it. A record's digest is still that of its sealed bytes
/// ([`digest`](super::digest)); `decode` only says `T` holds the same
/// record.
pub fn decode<T: DeserializeOwned + Serialize>(data: &[u8]) -> Result<T, Error> {
    let doc: Value = serde_json::from_slice(data)?;
    let want = jcs(&doc)?;
    let v: T = serde_json::from_value(doc)?;
    if jcs(&serde_json::to_value(&v)?)? != want {
        return Err(Error::Invalid(
            "the record's text does not read as this type: it would encode to other bytes".into(),
        ));
    }
    Ok(v)
}
