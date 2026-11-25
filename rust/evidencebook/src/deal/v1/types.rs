// SPDX-License-Identifier: Apache-2.0

//! The x-deal-v1 record types, as Go's `xdealv1` types them (`types.go`),
//! member for member with `x-deal-v1.schema.json`.
//!
//! A required member is a plain field. An optional member Go leaves out
//! when it is zero (`omitzero`) is an `Option` left out when `None`; an
//! optional list Go holds as a nil slice is an `Option<Vec<_>>`, so a
//! present empty list stays present. A member that may be null is a
//! [`Null`]: absent, null or a value. A member the schema leaves open is a
//! [`Value`]. They carry no rule: [`check`](super::check) reads a record
//! against the schema and the record checks, and [`decode`](super::decode)
//! refuses text the types would not encode back to.

use std::collections::BTreeMap;

use serde::de::Error as _;
use serde::ser::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::{Map, Value};

use crate::deal::{
    ApprovalBody as V0ApprovalBody, CheckBody as V0CheckBody, Counterparty, Producer, Ref, RelRef,
    Terms, VerdictBody as V0VerdictBody,
};

/// A member that is present and may be null, or absent. Its default is
/// absent. It serializes as its value or `null`; an optional one is left
/// out when absent (`skip_serializing_if = "Null::is_absent"`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub enum Null<T> {
    /// The member is absent.
    #[default]
    Absent,
    /// The member is present and null.
    Null,
    /// The member holds a value.
    Some(T),
}

impl<T> Null<T> {
    /// Says the member is absent.
    pub fn is_absent(&self) -> bool {
        matches!(self, Null::Absent)
    }
    /// Says the member is present.
    pub fn is_present(&self) -> bool {
        !self.is_absent()
    }
    /// Says the member is present and null.
    pub fn is_null(&self) -> bool {
        matches!(self, Null::Null)
    }
    /// The value, if the member holds one.
    pub fn value(&self) -> Option<&T> {
        match self {
            Null::Some(v) => Some(v),
            _ => None,
        }
    }
}

impl<T: Serialize> Serialize for Null<T> {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        match self {
            Null::Some(v) => v.serialize(s),
            _ => s.serialize_none(),
        }
    }
}

impl<'de, T: Deserialize<'de>> Deserialize<'de> for Null<T> {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Null<T>, D::Error> {
        Ok(match Option::<T>::deserialize(d)? {
            Some(v) => Null::Some(v),
            None => Null::Null,
        })
    }
}

fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}

fn is_false(v: &bool) -> bool {
    !*v
}

/// The x-deal-v1 block every v1 record carries (§3.1). `seq`, `prev` and
/// `agreement_ref` are a book's, `seen` a deal book's; journal records carry
/// none of them.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Block {
    pub profile: String,
    pub canonicalization: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub deal_id: String,
    pub record_type: String,
    pub at: String,
    #[serde(default, skip_serializing_if = "is_zero_i64")]
    pub seq: i64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub prev: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub agreement_ref: Option<Ref>,
    #[serde(default, skip_serializing_if = "Null::is_absent")]
    pub seen: Null<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<RelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub marks: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty: Option<Counterparty>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub commit_alg: String,
}

/// One statement on a row (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatementItem {
    pub row: String,
    pub value: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub remedy: String,
}

/// A row date: a fixed time, or a number of seconds after the time of a
/// named record (rowstatus's `Date`).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Date {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub after: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub seconds: Option<i64>,
}

/// A remedy a row allows, and the seconds it must be made within.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RowRemedy {
    pub id: String,
    pub within_seconds: i64,
}

/// One term row of an offer (§4.2).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Row {
    pub id: String,
    pub owed_by: String,
    pub owed: Value,
    pub due: Date,
    pub window_end: Date,
    #[serde(default, skip_serializing_if = "is_false")]
    pub recomputed: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub remedies: Option<Vec<RowRemedy>>,
}

/// A record a row date hangs on: the side that writes it and its latest
/// date.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Named {
    pub side: String,
    pub latest: String,
}

/// An offer's rows and the named records they hang on (§4.2).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TermsRows {
    pub rows: Vec<Row>,
    pub named: BTreeMap<String, Named>,
}

/// An adjudicator: its key, and the digest and location of its rules.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Adjudicator {
    pub key_id: String,
    pub rules_digest: String,
    pub rules_url: String,
}

/// A witness, named by its verified key id, with its URL only a hint.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Witness {
    pub key_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub url: String,
}

/// How to ask the offerer for evidence.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AskAddress {
    pub url: String,
    pub digest: String,
}

/// A propose record's body: an offer or a counter-offer (§3.3). `to` and
/// `adjudicator` are required and may be null.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProposeBody {
    pub role: String,
    pub to: Null<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counter_to: Option<Ref>,
    pub terms: Terms,
    pub pack_digest: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub pack_url: String,
    pub stated_at: String,
    pub agree_by: String,
    pub terms_rows: TermsRows,
    pub adjudicator: Null<Adjudicator>,
    pub forums: Vec<String>,
    pub witnesses: Vec<Witness>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ask: Option<AskAddress>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub profiles: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub listing_digest: String,
}

/// An agree record's body: an agreement, a decline or a cancellation, told
/// apart by `kind` (§3.3, §3.5).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AgreeBody {
    pub kind: String,
    pub offer: Ref,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub rows_digest: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub side: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub role: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub admit: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub intent: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub witnesses: Option<Vec<Witness>>,
    #[serde(default, skip_serializing_if = "Null::is_absent")]
    pub adjudicator: Null<Adjudicator>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub forums: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty_layer: Option<bool>,
    #[serde(default, skip_serializing_if = "Null::is_absent")]
    pub counterparty_key: Null<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub checkpoint_key: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub shelf_log_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason_commitment: String,
}

impl AgreeBody {
    /// The agreement's witnesses, none when absent.
    pub fn witness_list(&self) -> &[Witness] {
        self.witnesses.as_deref().unwrap_or(&[])
    }
    /// The agreement's forums, none when absent.
    pub fn forum_list(&self) -> &[String] {
        self.forums.as_deref().unwrap_or(&[])
    }
    /// Says the counterparty runs the layer.
    pub fn layer(&self) -> bool {
        self.counterparty_layer == Some(true)
    }
}

/// The row a remedy is for and, optionally, the ruling it points at.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RemedyFor {
    pub row: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ruling: Option<Ref>,
}

/// A perform record's body (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PerformBody {
    pub act: String,
    pub observed: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub detail_commitment: String,
    pub statements: Vec<StatementItem>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub check: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub remedy_for: Option<RemedyFor>,
}

/// A settle record's body: a payment leg, a refund or another remedy that
/// is a payment (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SettleBody {
    pub leg: String,
    pub statements: Vec<StatementItem>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub check: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub supersedes: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reverses: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub remedy_for: Option<RemedyFor>,
}

/// The arrived record a statement names: a cancellation or a ruling.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatementNames {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancellation: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ruling: Option<Ref>,
}

/// A statement record's body (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StatementBody {
    pub statements: Vec<StatementItem>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub names: Option<StatementNames>,
}

/// A dispute record's body: a dispute, a withdrawal or a challenge (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DisputeBody {
    pub kind: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub row: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub statements: Option<Vec<Ref>>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub adjudicator: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub forum: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ruling: Option<Ref>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub grounds_commitment: String,
}

/// The record keys of a deal's two sides.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RulingKeys {
    pub buyer: String,
    pub seller: String,
}

/// The remedy a ruling orders.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RulingRemedy {
    pub kind: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub currency: String,
    pub due: String,
}

/// The last record id of each deal book the adjudicator read, null for a
/// book it did not read.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RulingRead {
    pub buyer: Null<String>,
    pub seller: Null<String>,
}

/// A ruling record's body (§3.3; design §8).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RulingBody {
    pub row: String,
    pub keys: RulingKeys,
    pub against: Null<String>,
    pub value: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub remedy: Option<RulingRemedy>,
    pub read: RulingRead,
    pub forum: String,
    pub rules_digest: String,
    pub r#final: bool,
}

/// An ask record's body (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AskBody {
    pub request: Ref,
    pub to: String,
}

/// An answer record's body (§3.3).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AnswerBody {
    pub request: Ref,
    pub outcome: String,
    pub answer: Ref,
}

/// An admit record's body: an admission decision (§3.3), with its nonce
/// (u528).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AdmitBody {
    pub outcome: String,
    pub tier: String,
    pub rows: Vec<String>,
    pub asks: Vec<Ref>,
    pub pack_digest: String,
    pub card_digest: String,
    pub answers: Vec<Ref>,
    pub nonce: String,
}

/// Splits `extra` members off a body object and reads the rest as v0's
/// closed body `T`; Go embeds v0's body type and adds the members.
fn split_v0<'de, D: Deserializer<'de>, T: serde::de::DeserializeOwned>(
    d: D,
    extra: &[&str],
) -> Result<(T, Map<String, Value>), D::Error> {
    let mut m = Map::<String, Value>::deserialize(d)?;
    let mut out = Map::new();
    for k in extra {
        if let Some(v) = m.remove(*k) {
            out.insert((*k).to_string(), v);
        }
    }
    let v0 = serde_json::from_value(Value::Object(m)).map_err(D::Error::custom)?;
    Ok((v0, out))
}

/// Writes v0's body with the extra members beside it.
fn join_v0<S: Serializer, T: Serialize>(
    s: S,
    v0: &T,
    extra: Vec<(&str, Option<Value>)>,
) -> Result<S::Ok, S::Error> {
    let mut m = match serde_json::to_value(v0).map_err(S::Error::custom)? {
        Value::Object(m) => m,
        _ => return Err(S::Error::custom("a v0 body is an object")),
    };
    for (k, v) in extra {
        if let Some(v) = v {
            m.insert(k.to_string(), v);
        }
    }
    Value::Object(m).serialize(s)
}

fn string_member<E: serde::de::Error>(
    m: &mut Map<String, Value>,
    k: &str,
    required: bool,
) -> Result<String, E> {
    match m.remove(k) {
        Some(Value::String(s)) => Ok(s),
        Some(_) => Err(E::custom(format!("{k} is a string"))),
        None if required => Err(E::custom(format!("missing field `{k}`"))),
        None => Ok(String::new()),
    }
}

/// The journal overlay's check: v0's body, `payment_ref` (§3.2) and its
/// nonce (u528).
#[derive(Clone, Debug, Default, PartialEq)]
pub struct CheckBody {
    pub v0: V0CheckBody,
    /// Empty when absent.
    pub payment_ref: String,
    pub nonce: String,
}

impl Serialize for CheckBody {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        let pr = (!self.payment_ref.is_empty()).then(|| Value::String(self.payment_ref.clone()));
        join_v0(
            s,
            &self.v0,
            vec![
                ("payment_ref", pr),
                ("nonce", Some(Value::String(self.nonce.clone()))),
            ],
        )
    }
}

impl<'de> Deserialize<'de> for CheckBody {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<CheckBody, D::Error> {
        let (v0, mut m) = split_v0::<D, V0CheckBody>(d, &["payment_ref", "nonce"])?;
        Ok(CheckBody {
            v0,
            payment_ref: string_member::<D::Error>(&mut m, "payment_ref", false)?,
            nonce: string_member::<D::Error>(&mut m, "nonce", true)?,
        })
    }
}

/// The journal overlay's verdict: v0's body, `received_history` when the
/// rules checker read the counterparty's History (capsule-cli#327), and a
/// nonce (u528).
#[derive(Clone, Debug, Default, PartialEq)]
pub struct VerdictBody {
    pub v0: V0VerdictBody,
    /// `None` when absent.
    pub received_history: Option<ReceivedHistoryDigests>,
    pub nonce: String,
}

/// A verdict's `received_history`: each History document's SHA-256, in the
/// order the checker was given them, and `input_sha256`, the SHA-256 of the
/// JCS form of the checker input's `received_history` member (Go
/// `xdealv1.ReceivedHistoryDigests`).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReceivedHistoryDigests {
    pub history_sha256: Vec<String>,
    pub input_sha256: String,
    /// Set exactly when the checker denied given the History and did not
    /// deny without it (D1 condition 1): the verdict asks over rules that
    /// keep the checker's deny. Absent when false, as Go writes it.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub alone_deny: bool,
}

impl Serialize for VerdictBody {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        let rh = match &self.received_history {
            Some(h) => Some(serde_json::to_value(h).map_err(S::Error::custom)?),
            None => None,
        };
        join_v0(
            s,
            &self.v0,
            vec![
                ("received_history", rh),
                ("nonce", Some(Value::String(self.nonce.clone()))),
            ],
        )
    }
}

impl<'de> Deserialize<'de> for VerdictBody {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<VerdictBody, D::Error> {
        let (v0, mut m) = split_v0::<D, V0VerdictBody>(d, &["received_history", "nonce"])?;
        let received_history = match m.remove("received_history") {
            Some(v) => Some(serde_json::from_value(v).map_err(D::Error::custom)?),
            None => None,
        };
        Ok(VerdictBody {
            v0,
            received_history,
            nonce: string_member::<D::Error>(&mut m, "nonce", true)?,
        })
    }
}

/// The journal overlay's approval: v0's body and a nonce (u528).
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ApprovalBody {
    pub v0: V0ApprovalBody,
    pub nonce: String,
}

impl Serialize for ApprovalBody {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        join_v0(
            s,
            &self.v0,
            vec![("nonce", Some(Value::String(self.nonce.clone())))],
        )
    }
}

impl<'de> Deserialize<'de> for ApprovalBody {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<ApprovalBody, D::Error> {
        let (v0, mut m) = split_v0::<D, V0ApprovalBody>(d, &["nonce"])?;
        Ok(ApprovalBody {
            v0,
            nonce: string_member::<D::Error>(&mut m, "nonce", true)?,
        })
    }
}

/// Reads a list Go may write as `null` (a nil slice) as an empty list.
pub(crate) fn null_as_empty<'de, D: Deserializer<'de>, T: Deserialize<'de>>(
    d: D,
) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}
