//! The Capsule members a step or a report is sealed with: plain data, one
//! member for each field Go's `dealprofile` fills in capsule-emit's
//! `emit.Input`. The producer seals them with the payload as the agent
//! input, with its own sealer and key, as Go's producer seals an
//! `emit.Input` with `emit.Seal`.

use serde_json::Value;

/// `action_type` of a Capsule that records, without deciding.
pub const ACTION_TYPE_FYI: &str = "fyi";
/// `action_type` of a Capsule that records an action taken under a decision.
pub const ACTION_TYPE_DECIDE: &str = "decide";
/// A decide step's effect status: the agent's own report that it acted, with
/// no response in hand.
pub const EFFECT_DISPATCHED: &str = "dispatched";
/// A decide step's effect attestation.
pub const ATTESTATION_RUNTIME_CLAIMED: &str = "runtime_claimed";
/// A step follows the one before it (ordering only).
pub const RELATION_FOLLOWS: &str = "follows";
/// A record sealed after a terminal close confirms that close.
pub const RELATION_CONFIRMS: &str = "confirms";

/// A Capsule's members before sealing (Go `emit.Input`, the fields a step or
/// report sets).
#[derive(Clone, Debug, PartialEq)]
pub struct Capsule {
    pub action_id: String,
    /// [`ACTION_TYPE_FYI`] or [`ACTION_TYPE_DECIDE`].
    pub action_type: String,
    pub operator: String,
    pub developer: String,
    /// RFC 3339 UTC, to the second (`YYYY-MM-DDTHH:MM:SSZ`).
    pub timestamp: String,
    /// Set on a decide Capsule only.
    pub disposition: Option<Disposition>,
    /// Set on a decide Capsule only.
    pub effect: Option<Effect>,
    /// The Capsule this one chains to; `None` for an unchained Capsule.
    pub chain: Option<Chain>,
    /// Further top-level members, in order (Go `emit.Input.Extensions`).
    pub extensions: Vec<Extension>,
}

/// How a decision was disposed (Go `emit.Disposition`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Disposition {
    pub decision: String,
    pub approver: String,
    pub human_disposed: bool,
    /// Empty when the decision names none; emit then omits the member.
    pub verdict_class: String,
    /// Empty when the decision names none; emit then omits the member.
    pub reason_digest: String,
}

/// The effect a decide Capsule records (Go `emit.Effect`, the fields a step
/// sets).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Effect {
    /// The effect's `type` member.
    pub effect_type: String,
    /// Always [`EFFECT_DISPATCHED`] for a step.
    pub status: String,
    pub irreversibility_class: String,
    /// Always [`ATTESTATION_RUNTIME_CLAIMED`] for a step.
    pub effect_attestation: String,
}

/// A Capsule's link to its parent (Go `emit.Chain`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Chain {
    pub parent_capsule_id: String,
    /// [`RELATION_FOLLOWS`] or [`RELATION_CONFIRMS`] for a step.
    pub relation: String,
}

/// One further top-level Capsule member (Go `emit.Extension`).
#[derive(Clone, Debug, PartialEq)]
pub struct Extension {
    pub name: String,
    pub value: Value,
}

/// `at` (seconds since the Unix epoch) as RFC 3339 UTC, to the second.
pub(crate) fn rfc3339_seconds(at: i64) -> String {
    let days = at.div_euclid(86_400);
    let secs = at.rem_euclid(86_400);
    // Howard Hinnant's civil_from_days.
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z",
        secs / 3600,
        secs % 3600 / 60,
        secs % 60
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc3339_seconds_formats_utc() {
        assert_eq!(rfc3339_seconds(0), "1970-01-01T00:00:00Z");
        assert_eq!(rfc3339_seconds(7), "1970-01-01T00:00:07Z");
        assert_eq!(rfc3339_seconds(1_790_532_000), "2026-09-27T18:00:00Z");
        assert_eq!(rfc3339_seconds(951_782_400), "2000-02-29T00:00:00Z");
        assert_eq!(rfc3339_seconds(-1), "1969-12-31T23:59:59Z");
    }
}
