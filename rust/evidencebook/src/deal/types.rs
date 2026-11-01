// SPDX-License-Identifier: Apache-2.0

//! The x-deal-v0 record types: the x-deal-v0 block, one body type per
//! record type, and the typed action records and contract documents
//! (PROFILE.md §3, §9), as Go's `dealprofile` types them (`types.go`).
//!
//! They follow the profile's schemas member for member: a required member
//! is a plain field, an optional one is an `Option` that is left out when
//! `None` (an empty `Vec` or map is a present-empty member), and a member
//! the schemas leave open is a [`Value`] (an optional one keeps an explicit
//! `null` as `Some(Value::Null)`). They carry no rule: [`encode`] checks a
//! record against the schemas. The tests check that every record the
//! fixtures and baseline vectors hold decodes into them and encodes to the
//! same bytes.
//!
//! [`encode`]: super::encode

use std::collections::BTreeMap;

use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

/// Reads an optional open member as present, keeping an explicit `null`.
fn present<'de, D: Deserializer<'de>>(d: D) -> Result<Option<Value>, D::Error> {
    Value::deserialize(d).map(Some)
}

/// Ref is `digest_ref` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Ref {
    pub r#type: String,
    pub digest_alg: String,
    pub digest: String,
}

/// Counterparty is `counterparty` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Counterparty {
    pub fp_alg: String,
    pub ids: BTreeMap<String, String>,
}

/// CounterpartyProfileIDs is the `ids` member of [`CounterpartyProfile`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CounterpartyProfileIDs {
    pub payee: String,
}

/// CounterpartyProfile is `counterparty_profile` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CounterpartyProfile {
    pub fp_alg: String,
    pub ids: CounterpartyProfileIDs,
}

/// RelRef is `rel_ref` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RelRef {
    pub rel: String,
    pub r#type: String,
    pub digest_alg: String,
    pub digest: String,
}

/// Block is `block` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Block {
    pub profile: String,
    pub canonicalization: String,
    pub deal_id: String,
    pub record_type: String,
    pub seq: i64,
    pub at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub prev: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub baseline_ref: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub channel: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty: Option<Counterparty>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty_profile: Option<CounterpartyProfile>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<RelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
}

/// Terms is `terms` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Terms {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub item: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub quantity: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub price_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub deposit_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub when: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub place: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub conditions: Option<BTreeMap<String, String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub handover: Option<String>,
}

/// IntentBody is `body_intent` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct IntentBody {
    pub verbatim_commitment: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub party_role: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub asked: Option<Terms>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_total_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub bounds_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub allowed: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ordered_representations: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub delegated: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub delegated_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub r#move: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub proposed: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub why_commitment: Option<String>,
}

/// MaterialityRef is `materiality_ref` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MaterialityRef {
    pub digest: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub label_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
}

/// TypedRef is `record_ref` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TypedRef {
    pub r#type: String,
    pub digest_alg: String,
    pub digest: String,
}

/// AuthorityEntry is `authority_entry` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorityEntry {
    pub r#type: String,
    pub r#ref: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub scope: Option<String>,
}

/// Claim is `claim` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Claim {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_kind: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_ref_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub class: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub authority_basis: Option<Vec<AuthorityEntry>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub evaluation_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unchecked: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rule: Option<String>,
}

/// Recourse is `recourse` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Recourse {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rail: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refundable: Option<bool>,
}

/// CounterpartyFacts is `counterparty_facts` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CounterpartyFacts {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub domain_age_days: Option<i64>,
}

/// BaselineBodySkill is the `skill` member of [`BaselineBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BaselineBodySkill {
    pub skill_md_digest: String,
    pub other_copies: i64,
}

/// BaselineBody is `body_baseline` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BaselineBody {
    pub deal_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub materiality: Option<MaterialityRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub demo: Option<bool>,
    pub intent: IntentBody,
    pub terms: Terms,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub claims: Option<Vec<Claim>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recourse: Option<Recourse>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty_facts: Option<CounterpartyFacts>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub skill: Option<BaselineBodySkill>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expect_close_by: Option<String>,
}

/// MessageBody is `body_message` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MessageBody {
    pub from: String,
    pub content_commitment: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sent_at: Option<String>,
}

/// MerchantEmailParsed is the `parsed` member of [`MerchantEmail`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MerchantEmailParsed {
    pub method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub total_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub order_id_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancel_by: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sent_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub item_count: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<String>,
}

/// MerchantEmail is `merchant_email` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MerchantEmail {
    pub message_digest: String,
    pub key_records_digest: String,
    pub key_source: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub key_resolver: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub key_dnssec: Option<String>,
    pub dkim: String,
    pub merchant_signed: bool,
    pub dmarc_policy: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub dmarc_record_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub dmarc_source: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub signer_matches_baseline: Option<bool>,
    pub parsed: MerchantEmailParsed,
}

/// Obligation is `obligation` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Obligation {
    pub kind: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancel_by: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub due_by: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub takes_effect: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub period: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub terms_commitment: Option<String>,
}

/// EvidenceBodyWrappedItem is an item of the `wrapped` member of [`EvidenceBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EvidenceBodyWrappedItem {
    pub r#type: String,
    pub digest_alg: String,
    pub digest: String,
}

/// EvidenceBodyReceivedTo is the `received_to` member of [`EvidenceBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EvidenceBodyReceivedTo {
    pub fp_alg: String,
    pub payee: String,
}

/// EvidenceBody is `body_evidence` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EvidenceBody {
    pub source: String,
    pub verified: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub detail_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty_facts: Option<CounterpartyFacts>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub merchant_email: Option<MerchantEmail>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub obligation: Option<Obligation>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub resolves_obligation: Option<Ref>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub wrapped: Option<Vec<EvidenceBodyWrappedItem>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub wrapped_octets: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub wrapped_held: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub carrier_headers_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub received_to: Option<EvidenceBodyReceivedTo>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub preimage: Option<String>,
}

/// DetailChangeBody is `body_detail_change` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DetailChangeBody {
    pub source: String,
    pub changed: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub terms: Option<Terms>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recourse: Option<Recourse>,
}

/// CheckBodyPosting is the `posting` member of [`CheckBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckBodyPosting {
    pub text_commitment: String,
    pub classes: Vec<String>,
    pub platform: String,
}

/// CheckBodyRepresenting is the `representing` member of [`CheckBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckBodyRepresenting {
    pub class: String,
    pub text_commitment: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_ref_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub attributed_to: Option<String>,
}

/// CheckBody is `body_check` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckBody {
    pub action: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub posting: Option<CheckBodyPosting>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub listing_classes: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub representing: Option<CheckBodyRepresenting>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub item_ref_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub action_class: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub taxonomy_version: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub spend_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub authorized_max_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub spend_authorized_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub direction: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reverses_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fee_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancelled_amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub seen_item: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disclosing: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disclosing_to: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub terms: Option<Terms>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recipient_role: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub repeat_disclosures: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recipient_told_before: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub channel: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub first_contact_channel: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub material_fields_changed: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub material_fields_basis: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rule_inputs_version: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub offer_fields_changed: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub offer_fields_basis: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub upfront_amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recourse: Option<Recourse>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub no_proposal: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pack_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pack_digest: Option<String>,
}

/// VerdictBodyRulesHistory is the `history` member of [`VerdictBodyRules`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerdictBodyRulesHistory {
    pub days: i64,
    pub acts: i64,
    pub complete: bool,
}

/// VerdictBodyRulesFindingsItem is an item of the `findings` member of [`VerdictBodyRules`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerdictBodyRulesFindingsItem {
    pub id: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub check: Option<String>,
    pub verdict: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disposition: Option<String>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub limit: Option<Value>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub value: Option<Value>,
}

/// VerdictBodyRules is the `rules` member of [`VerdictBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerdictBodyRules {
    pub status: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cause: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ruleset_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub definition_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub checker_sha256: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub verdict: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tier: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub grade: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub history: Option<VerdictBodyRulesHistory>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub findings: Option<Vec<VerdictBodyRulesFindingsItem>>,
}

/// Difference is `difference` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Difference {
    pub question: String,
    pub rule: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub field: Option<String>,
}

/// VerdictBodyUnverifiedClaimsItem is an item of the `unverified_claims` member of [`VerdictBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerdictBodyUnverifiedClaimsItem {
    pub claim: Ref,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub index: Option<i64>,
}

/// VerdictBodyJudge is the `judge` member of [`VerdictBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerdictBodyJudge {
    pub kind: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub status: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub response_digest: Option<String>,
}

/// VerdictBody is `body_verdict` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerdictBody {
    pub result: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub materiality: Option<MaterialityRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rules: Option<VerdictBodyRules>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pack_id: Option<String>,
    pub differences: Vec<Difference>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unverified: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unverified_claims: Option<Vec<VerdictBodyUnverifiedClaimsItem>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub notes: Option<Vec<String>>,
    pub options: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub card_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub judge: Option<VerdictBodyJudge>,
}

/// LimitSet is `limit_set` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LimitSet {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_total_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub bounds_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub allowed: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ordered_representations: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub delegated: Option<Vec<String>>,
}

/// ApprovalBodyLimits is the `limits` member of [`ApprovalBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApprovalBodyLimits {
    pub previous: LimitSet,
    pub new: LimitSet,
}

/// ApprovalBody is `body_approval` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApprovalBody {
    pub choice: String,
    pub proceed: bool,
    pub approver: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub said_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub card_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub limits: Option<ApprovalBodyLimits>,
}

/// ActionBody is `body_action` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionBody {
    pub action: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub action_class: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub taxonomy_version: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub spend_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fee_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancelled_amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rail: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub direction: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reference_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description_commitment: Option<String>,
}

/// OutcomeBody is `body_outcome` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OutcomeBody {
    pub status: String,
    pub outcome: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub delivered: Option<Terms>,
    pub differences: Vec<Difference>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unchecked: Option<ActionBody>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub note_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
}

/// CloseBodyCarriedObligationsItem is an item of the `carried_obligations` member of [`CloseBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CloseBodyCarriedObligationsItem {
    pub obligation: Ref,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancel_by: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub due_by: Option<String>,
}

/// CloseBody is `body_close` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CloseBody {
    pub outcome: String,
    pub unchecked_actions: i64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub differences: Option<Vec<Difference>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub carried_obligations: Option<Vec<CloseBodyCarriedObligationsItem>>,
}

/// DisclosureBodyFieldsItem is an item of the `fields` member of [`DisclosureBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DisclosureBodyFieldsItem {
    pub class: String,
    pub value_commitment: String,
}

/// DisclosureBody is `body_disclosure` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DisclosureBody {
    pub to: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub action_class: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub taxonomy_version: Option<String>,
    pub fields: Vec<DisclosureBodyFieldsItem>,
    pub authority: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rule: Option<String>,
}

/// SaleBody is `body_sale` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SaleBody {
    pub deal_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub demo: Option<bool>,
    pub intent: IntentBody,
    pub terms: Terms,
    pub recourse: Recourse,
    pub item_ref_commitment: String,
}

/// CounterpartyProfileBody is `body_counterparty_profile` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CounterpartyProfileBody {}

/// ThreadBody is `body_thread` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ThreadBody {
    pub thread_ref_commitment: String,
}

/// ThreadOpenedBody is `body_thread_opened` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ThreadOpenedBody {
    pub task_authority_commitment: String,
}

/// SaleCutBodyThreadHeadsItem is an item of the `thread_heads` member of [`SaleCutBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SaleCutBodyThreadHeadsItem {
    pub registration: Ref,
    pub head_commitment: String,
}

/// SaleCutBody is `body_sale_cut` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SaleCutBody {
    pub thread_heads: Vec<SaleCutBodyThreadHeadsItem>,
}

/// SaleCloseBody is `body_sale_close` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SaleCloseBody {}

/// AgentMoveBodyOptionsItem is an item of the `options` member of [`AgentMoveBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AgentMoveBodyOptionsItem {
    pub item_commitment: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub price_minor: Option<i64>,
}

/// AgentMoveBody is `body_agent_move` in the schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AgentMoveBody {
    pub r#move: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub where_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub results: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub options: Option<Vec<AgentMoveBodyOptionsItem>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub found: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub option: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub item_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub price_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub detail_commitment: Option<String>,
}

/// TypedRelRef is `rel_ref` in record-common-v0.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TypedRelRef {
    pub rel: String,
    pub r#type: String,
    pub digest_alg: String,
    pub digest: String,
}

/// Producer is `producer` in record-common-v0.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Producer {
    pub name: String,
    pub version: String,
    pub commit: String,
}

/// ActionApprovalBody is the `body` member of [`ActionApproval`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionApprovalBody {
    pub authority: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub choice: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub proceed: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub said_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rendering_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub platform: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub mechanism: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub displayed_text_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub returned_user_text_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub proposed_action_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub observed_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub effective_policy_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub semantic_diff_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub content_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub channel: Option<String>,
}

/// ActionApproval is an `action-approval/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionApproval {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<TypedRelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: ActionApprovalBody,
}

/// TypedFinding is `finding` in record-common-v0.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TypedFinding {
    pub question: String,
    pub rule: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub field: Option<String>,
}

/// ActionEvaluationBodyUnverifiedClaimsItem is an item of the `unverified_claims` member of [`ActionEvaluationBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionEvaluationBodyUnverifiedClaimsItem {
    pub claim: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub index: Option<i64>,
}

/// ActionEvaluationBodyRulesChecksItem is an item of the `rules_checks` member of [`ActionEvaluationBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionEvaluationBodyRulesChecksItem {
    pub ruleset_id: String,
    pub definition_digest: String,
    pub verdict: String,
}

/// ActionEvaluationBodyAuthorityBasisItem is an item of the `authority_basis` member of [`ActionEvaluationBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionEvaluationBodyAuthorityBasisItem {
    pub r#type: String,
    pub r#ref: TypedRef,
}

/// ActionEvaluationBody is the `body` member of [`ActionEvaluation`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionEvaluationBody {
    pub disposition: String,
    pub findings: Vec<TypedFinding>,
    pub options: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unverified: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub unverified_claims: Option<Vec<ActionEvaluationBodyUnverifiedClaimsItem>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub notes: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rendering_commitment: Option<String>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub judge: Option<Value>,
    pub proposed_action_digest: String,
    pub task_authority_ref: TypedRef,
    pub ruleset_digest: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rules_status: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rules_checks: Option<Vec<ActionEvaluationBodyRulesChecksItem>>,
    pub materiality: String,
    pub materiality_digest: Option<String>,
    pub valid_until: String,
    pub authority_basis: Vec<ActionEvaluationBodyAuthorityBasisItem>,
}

/// ActionEvaluation is an `action-evaluation/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionEvaluation {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    pub refs: Vec<TypedRelRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: ActionEvaluationBody,
}

/// ActionOutcomeBody is the `body` member of [`ActionOutcome`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionOutcomeBody {
    pub status: String,
    pub outcome: String,
    pub findings: Vec<TypedFinding>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub attempted: Option<Value>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub delivered: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub note_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
}

/// ActionOutcome is an `action-outcome/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionOutcome {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<TypedRelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: ActionOutcomeBody,
}

/// TypedDisclosedFieldsItem is an item of the `fields` member of [`TypedDisclosed`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TypedDisclosedFieldsItem {
    pub class: String,
    pub value_commitment: String,
}

/// TypedDisclosed is `disclosed` in record-common-v0.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TypedDisclosed {
    pub to: String,
    pub fields: Vec<TypedDisclosedFieldsItem>,
}

/// TypedCounterparty is `counterparty` in record-common-v0.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TypedCounterparty {
    pub fp_alg: String,
    pub ids: BTreeMap<String, String>,
}

/// ActionRecordBody is the `body` member of [`ActionRecord`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionRecordBody {
    pub action: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub action_class: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub taxonomy_version: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub spend_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fee_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancelled_amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rail: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub direction: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reference_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disclosed: Option<TypedDisclosed>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub channel: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty: Option<TypedCounterparty>,
    pub evaluation_ref: TypedRef,
    pub authority_basis: Vec<AuthorityEntry>,
}

/// ActionRecord is an `action-record/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionRecord {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    pub refs: Vec<TypedRelRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: ActionRecordBody,
}

/// ActionReportBodyAuthorityItem is an item of the `authority` member of [`ActionReportBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionReportBodyAuthorityItem {
    pub layer: String,
    pub r#ref: TypedRef,
    pub at: String,
}

/// ActionReportBody is the `body` member of [`ActionReport`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionReportBody {
    pub presents: Vec<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub authority: Option<Vec<ActionReportBodyAuthorityItem>>,
}

/// ActionReport is an `action-report/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ActionReport {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<TypedRelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: ActionReportBody,
}

/// CheckRequestPlatform is the `platform` member of [`CheckRequest`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckRequestPlatform {
    pub id: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub display_name: Option<String>,
}

/// CheckRequest is a `check-request/v0` contract document.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckRequest {
    pub r#type: String,
    pub phase: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub platform: Option<CheckRequestPlatform>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ruleset_ref: Option<String>,
    pub ruleset_digest: String,
    pub task_authority_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub root_intent_ref: Option<TypedRef>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub parent_delegation_ref: Option<Value>,
    pub proposed_action: Value,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub history_refs: Option<Vec<TypedRef>>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub agent_assessment: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub platform_approval_refs: Option<Vec<TypedRef>>,
}

/// CheckResponse is a `check-response/v0` contract document.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckResponse {
    pub r#type: String,
    pub disposition: String,
    pub valid_until: String,
    pub proposed_action_digest: String,
    pub findings: Vec<TypedFinding>,
    pub authority_basis: Vec<AuthorityEntry>,
    pub evaluation_ref: TypedRef,
    pub ruleset_digest: String,
    pub task_authority_ref: TypedRef,
    pub materiality: String,
    pub materiality_digest: Option<String>,
}

/// CommercialBounds is a `commercial-bounds/v0` contract document.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CommercialBounds {
    pub r#type: String,
    pub min_total_minor: i64,
}

/// ProposedActionBodyRepresenting is the `representing` member of [`ProposedActionBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProposedActionBodyRepresenting {
    pub class: String,
    pub text_commitment: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source_ref_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub attributed_to: Option<String>,
}

/// ProposedActionBodyPosting is the `posting` member of [`ProposedActionBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProposedActionBodyPosting {
    pub text_commitment: String,
    pub classes: Vec<String>,
    pub platform: String,
}

/// ProposedActionBody is the `body` member of [`ProposedAction`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProposedActionBody {
    pub action: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub action_class: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub taxonomy_version: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub spend_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub authorized_max_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub spend_authorized_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fee_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancelled_amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub direction: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub currency: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub seen_item: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disclosing: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disclosing_to: Option<String>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub terms: Option<Value>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub recourse: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub upfront_amount_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recipient_role: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub repeat_disclosures: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub recipient_told_before: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub channel: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub first_contact_channel: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub material_fields_changed: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub material_fields_basis: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rule_inputs_version: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub offer_fields_changed: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub offer_fields_basis: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reverses_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub task_authority_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub counterparty: Option<TypedCounterparty>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub item_ref_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub representing: Option<ProposedActionBodyRepresenting>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub no_proposal: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub posting: Option<ProposedActionBodyPosting>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub listing_classes: Option<Vec<String>>,
}

/// ProposedAction is a `proposed-action/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProposedAction {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<TypedRelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: ProposedActionBody,
}

/// TaskAuthorityBodyPreconditionsItem is an item of the `preconditions` member of [`TaskAuthorityBody`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TaskAuthorityBodyPreconditionsItem {
    pub action: String,
    pub citing: String,
}

/// TaskAuthorityBody is the `body` member of [`TaskAuthority`].
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TaskAuthorityBody {
    pub verbatim_commitment: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_total_minor: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub bounds_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub allowed: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ordered_representations: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub outcome_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub allowed_actions: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub preconditions: Option<Vec<TaskAuthorityBodyPreconditionsItem>>,
    #[serde(
        default,
        deserialize_with = "present",
        skip_serializing_if = "Option::is_none"
    )]
    pub asked: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub previous_ref: Option<TypedRef>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub said_commitment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sale_authority_commitment: Option<String>,
}

/// TaskAuthority is a `task-authority/v0` record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TaskAuthority {
    pub r#type: String,
    pub canonicalization: String,
    pub chain_id: String,
    pub seq: i64,
    pub at: String,
    pub prev: TypedRef,
    pub chain_root: TypedRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refs: Option<Vec<TypedRelRef>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub producer: Option<Producer>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub commit_alg: Option<String>,
    pub body: TaskAuthorityBody,
}
