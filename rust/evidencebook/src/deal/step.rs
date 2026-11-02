//! A step's Capsule members and record header fields, as Go's `dealprofile`
//! lays them out (`step.go`).
//!
//! Each step of a deal is sealed as one Capsule (PROFILE.md §1) whose
//! payload is the step's record: action_id `<deal_id>/<seq>`, chained to the
//! step before it, with the deal id as its top-level `deal_id` member. On a
//! book it also carries an evidence record header of type
//! [`STEP_RECORD_TYPE`] whose subject_ref is the action_id, whose one link
//! cites the parent, and whose field commitments are the step's local event
//! and the deal's closed money terms. The producer decides what the step
//! says; [`step_capsule`] only lays it out.

use std::collections::BTreeMap;

use serde_json::Value;

use super::capsule::*;
use super::check::Error;
use super::names::*;

/// Marks a step as a decide Capsule: an action taken under a sealed approval
/// whose action has an effect type. The producer decides that, and how the
/// action was disposed and how recoverable it is; the effect is always the
/// agent's own report that it acted, with no response in hand.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Decide {
    pub disposition: Disposition,
    pub effect_type: String,
    pub irreversibility_class: String,
}

/// What a step's Capsule is made from.
#[derive(Clone, Debug, PartialEq)]
pub struct StepInput {
    pub deal_id: String,
    pub seq: i64,
    pub operator: String,
    /// The producer's own developer name (the capsulectl deal plugin names
    /// "capsulectl-deal").
    pub developer: String,
    /// The step's seal time, the record's block `at`, in seconds since the
    /// Unix epoch (a step is stated to the second).
    pub at: i64,
    /// The Capsule the step chains to: the step before it
    /// ([`RELATION_FOLLOWS`]), or the close it confirms
    /// ([`RELATION_CONFIRMS`]); `None` for the first step.
    pub parent: Option<Chain>,
    /// A settlement leg the step carries, as built and stored with the step;
    /// it rides as the Capsule's top-level `settlement` member.
    pub settlement: Option<Value>,
    /// Set for a decide step, `None` for fyi.
    pub decide: Option<Decide>,
    /// The values the header's field commitments commit: the producer's
    /// local event (opaque here), and the deal's closed amount and currency
    /// ("" when none).
    pub local: String,
    pub amount: String,
    pub currency: String,
}

/// The evidence record header fields a step carries on a book: all but what
/// the book assigns. A step names no correlation.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StepHeader {
    pub record_type: String,
    pub epistemic_type: String,
    /// The step's seal time, the record's `committed_at` (RFC 3339 UTC, to
    /// the second).
    pub committed_at: String,
    pub subject_ref: String,
    /// The parent Capsule, which the header's one `cites` link targets, when
    /// the step has one. A producer may add the admission a first step rests
    /// on, or the counterparty checkpoint a close cites (PROFILE.md §1); the
    /// step builder does not.
    pub cites: Vec<String>,
    pub fields: BTreeMap<String, String>,
}

/// A step's Capsule members and its record header fields. The producer
/// seals the Capsule with the record's payload as JSON and its own key.
pub fn step_capsule(input: &StepInput) -> Result<(Capsule, StepHeader), Error> {
    if input.deal_id.is_empty() || input.seq < 1 {
        return Err(Error::Invalid(
            "a step needs a deal id and a seq >= 1".into(),
        ));
    }
    if input.developer.is_empty() {
        return Err(Error::Invalid(
            "a step needs the producer's developer name".into(),
        ));
    }
    if let Some(p) = &input.parent {
        if p.relation != RELATION_FOLLOWS && p.relation != RELATION_CONFIRMS {
            return Err(Error::Invalid(format!(
                "chain relation {} is not follows or confirms",
                super::check::go_q(&p.relation)
            )));
        }
    }
    let at = rfc3339_seconds(input.at);
    let mut c = Capsule {
        action_id: format!("{}/{}", input.deal_id, input.seq),
        action_type: ACTION_TYPE_FYI.into(),
        operator: input.operator.clone(),
        developer: input.developer.clone(),
        timestamp: at.clone(),
        disposition: None,
        effect: None,
        chain: input.parent.clone(),
        extensions: Vec::new(),
    };
    if let Some(s) = &input.settlement {
        c.extensions.push(Extension {
            name: "settlement".into(),
            value: s.clone(),
        });
    }
    c.extensions.push(Extension {
        name: DEAL_ID_MEMBER.into(),
        value: Value::String(input.deal_id.clone()),
    });
    if let Some(d) = &input.decide {
        c.action_type = ACTION_TYPE_DECIDE.into();
        c.disposition = Some(d.disposition.clone());
        c.effect = Some(Effect {
            effect_type: d.effect_type.clone(),
            status: EFFECT_DISPATCHED.into(),
            irreversibility_class: d.irreversibility_class.clone(),
            effect_attestation: ATTESTATION_RUNTIME_CLAIMED.into(),
        });
    }
    let h = StepHeader {
        record_type: STEP_RECORD_TYPE.into(),
        epistemic_type: EPISTEMIC_PRODUCER_CLAIM.into(),
        committed_at: at,
        subject_ref: c.action_id.clone(),
        cites: input
            .parent
            .iter()
            .map(|p| p.parent_capsule_id.clone())
            .collect(),
        fields: BTreeMap::from([
            (LOCAL_FIELD.to_string(), input.local.clone()),
            (AMOUNT_FIELD.to_string(), input.amount.clone()),
            (CURRENCY_FIELD.to_string(), input.currency.clone()),
        ]),
    };
    Ok((c, h))
}
