// SPDX-License-Identifier: Apache-2.0

//! A v1 record as one value: its x-deal-v1 block and its body, typed by
//! record type (Go `xdealv1.Record`), and [`decode`], which reads a record's
//! text into it.

use serde::de::Error as _;
use serde::ser::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::Value;

use super::check::{check_text, stage_err};
use super::names::*;
use super::types::*;
use crate::canonical::jcs;
use crate::deal::{
    AgentMoveBody, Claim, CounterpartyProfileBody, DetailChangeBody, DisclosureBody, Error,
    EvidenceBody, IntentBody, MessageBody, RulesVerdictBody, SaleBody, SaleUnpostBody, ThreadBody,
    ThreadOpenedBody, STAGE_CANONICALIZATION,
};

/// A v1 record's body, one variant per record type (§3.2): this module's
/// body types, and v0's for the journey overlay and rules_verdict.
#[derive(Clone, Debug, PartialEq)]
pub enum Body {
    Propose(ProposeBody),
    Agree(AgreeBody),
    Perform(PerformBody),
    Settle(SettleBody),
    Statement(StatementBody),
    Dispute(DisputeBody),
    Ruling(RulingBody),
    Ask(AskBody),
    Answer(AnswerBody),
    Admit(AdmitBody),
    Intent(IntentBody),
    Check(CheckBody),
    Verdict(VerdictBody),
    Approval(ApprovalBody),
    RulesVerdict(RulesVerdictBody),
    Message(MessageBody),
    Claim(Claim),
    Evidence(EvidenceBody),
    DetailChange(DetailChangeBody),
    AgentMove(AgentMoveBody),
    Disclosure(DisclosureBody),
    Sale(SaleBody),
    Thread(ThreadBody),
    ThreadOpened(ThreadOpenedBody),
    SaleUnpost(SaleUnpostBody),
    CounterpartyProfile(CounterpartyProfileBody),
}

macro_rules! bodies {
    ($($variant:ident => $ty:ident),* $(,)?) => {
        impl Body {
            /// The record type the body is the body of.
            pub fn record_type(&self) -> &'static str {
                match self { $(Body::$variant(_) => $ty),* }
            }

            fn from_value(record_type: &str, body: Value) -> Result<Body, serde_json::Error> {
                Ok(match record_type {
                    $($ty => Body::$variant(serde_json::from_value(body)?),)*
                    other => {
                        return Err(<serde_json::Error as serde::de::Error>::custom(format!(
                            "record type {other:?} is not an x-deal-v1 type"
                        )))
                    }
                })
            }
        }

        impl Serialize for Body {
            fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
                match self { $(Body::$variant(b) => b.serialize(s)),* }
            }
        }
    };
}

bodies! {
    Propose => TYPE_PROPOSE,
    Agree => TYPE_AGREE,
    Perform => TYPE_PERFORM,
    Settle => TYPE_SETTLE,
    Statement => TYPE_STATEMENT,
    Dispute => TYPE_DISPUTE,
    Ruling => TYPE_RULING,
    Ask => TYPE_ASK,
    Answer => TYPE_ANSWER,
    Admit => TYPE_ADMIT,
    Intent => TYPE_INTENT,
    Check => TYPE_CHECK,
    Verdict => TYPE_VERDICT,
    Approval => TYPE_APPROVAL,
    RulesVerdict => TYPE_RULES_VERDICT,
    Message => TYPE_MESSAGE,
    Claim => TYPE_CLAIM,
    Evidence => TYPE_EVIDENCE,
    DetailChange => TYPE_DETAIL_CHANGE,
    AgentMove => TYPE_AGENT_MOVE,
    Disclosure => TYPE_DISCLOSURE,
    Sale => TYPE_SALE,
    Thread => TYPE_THREAD,
    ThreadOpened => TYPE_THREAD_OPENED,
    SaleUnpost => TYPE_SALE_UNPOST,
    CounterpartyProfile => TYPE_COUNTERPARTY_PROFILE,
}

/// A v1 record: the x-deal-v1 block and its body. Its JSON is
/// `{"x-deal-v1": block, "body": body}`. Serializing refuses a body whose
/// record type is not the block's.
#[derive(Clone, Debug, PartialEq)]
pub struct Record {
    pub block: Block,
    pub body: Body,
}

impl Record {
    /// The agree body, if the record is an agree record.
    pub fn agree(&self) -> Option<&AgreeBody> {
        match &self.body {
            Body::Agree(b) => Some(b),
            _ => None,
        }
    }
    /// The propose body, if the record is a propose record.
    pub fn propose(&self) -> Option<&ProposeBody> {
        match &self.body {
            Body::Propose(b) => Some(b),
            _ => None,
        }
    }
}

#[derive(Serialize)]
struct RecordOut<'a> {
    #[serde(rename = "x-deal-v1")]
    block: &'a Block,
    body: &'a Body,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordIn {
    #[serde(rename = "x-deal-v1")]
    block: Block,
    body: Value,
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
        }
        .serialize(s)
    }
}

impl<'de> Deserialize<'de> for Record {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Record, D::Error> {
        let r = RecordIn::deserialize(d)?;
        let body = Body::from_value(&r.block.record_type, r.body)
            .map_err(|e| D::Error::custom(format!("a {} body: {e}", r.block.record_type)))?;
        Ok(Record {
            block: r.block,
            body,
        })
    }
}

/// Reads a v1 record's JSON text (Go `xdealv1.Decode`). It refuses what
/// the text check refuses (invalid UTF-8, a lone escaped surrogate, a
/// member given twice), a member the types do not name, and text the types
/// would not encode back to. It runs no schema or record check:
/// [`check`](super::check) does.
pub fn decode(data: &[u8]) -> Result<Record, Error> {
    check_text(data)?;
    let doc: Value = serde_json::from_slice(data)
        .map_err(|e| stage_err(STAGE_CANONICALIZATION, format!("not one JSON value: {e}")))?;
    decode_value(&doc)
}

/// Reads a record already held as a JSON value, refusing one the types
/// would not encode back to.
pub(crate) fn decode_value(doc: &Value) -> Result<Record, Error> {
    let want = jcs(doc)?;
    let r: Record = serde_json::from_value(doc.clone())?;
    if jcs(&serde_json::to_value(&r)?)? != want {
        return Err(Error::Invalid(
            "the record's text does not read as its type: it would encode to other bytes".into(),
        ));
    }
    Ok(r)
}
