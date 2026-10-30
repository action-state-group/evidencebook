//! The profile's schemas, vendored byte for byte from Go's
//! `dealprofile/x-deal-v0/` (the files Go's `schema.go` embeds), and the
//! schema stage's validation.

use std::sync::OnceLock;

use boon::{Compiler, InstanceToken, SchemaIndex, Schemas, ValidationError};
use serde_json::Value;

use super::check::Error;
use super::names::*;

const PROFILE_SCHEMA: &[u8] =
    include_bytes!("../../schemas/vendor/x-deal-v0/x-deal-v0.schema.json");
const PROFILE_SCHEMA_ID: &str =
    "https://agentactioncapsule.org/profiles/x-deal-v0/record.schema.json";
const RECORDS_BASE: &str = "https://agentactioncapsule.org/records/";

macro_rules! record_schemas {
    ($($name:literal),* $(,)?) => {
        &[$(($name, include_bytes!(concat!("../../schemas/vendor/x-deal-v0/records/", $name)))),*]
    };
}

/// The records schemas, by file name.
const RECORD_SCHEMAS: &[(&str, &[u8])] = record_schemas!(
    "action-approval-v0.schema.json",
    "action-evaluation-v0.schema.json",
    "action-outcome-v0.schema.json",
    "action-record-v0.schema.json",
    "action-report-v0.schema.json",
    "check-request-v0.schema.json",
    "check-response-v0.schema.json",
    "commercial-bounds-v0.schema.json",
    "proposed-action-v0.schema.json",
    "record-common-v0.schema.json",
    "task-authority-v0.schema.json",
);

/// The typed records a chain may hold (PROFILE.md §9), and the x-deal-v0
/// record type each stands for.
const TYPED_ACTION_TYPES: &[(&str, &str)] = &[
    (TYPE_TASK_AUTHORITY, "task_authority"),
    (TYPE_PROPOSED_ACTION, TYPE_CHECK),
    (TYPE_ACTION_EVALUATION, TYPE_VERDICT),
    (TYPE_ACTION_APPROVAL, TYPE_APPROVAL),
    (TYPE_ACTION_RECORD, TYPE_ACTION),
    (TYPE_ACTION_OUTCOME, TYPE_OUTCOME),
    (TYPE_ACTION_REPORT, "report"),
];

/// The typed records and contract types the records schemas define.
pub(crate) const RECORD_SCHEMA_TYPES: &[&str] = &[
    TYPE_TASK_AUTHORITY,
    TYPE_PROPOSED_ACTION,
    TYPE_ACTION_EVALUATION,
    TYPE_ACTION_APPROVAL,
    TYPE_ACTION_RECORD,
    TYPE_ACTION_OUTCOME,
    TYPE_ACTION_REPORT,
    TYPE_CHECK_REQUEST,
    TYPE_CHECK_RESPONSE,
    TYPE_COMMERCIAL_BOUNDS,
];

pub(crate) fn is_typed_action_type(t: &str) -> bool {
    TYPED_ACTION_TYPES.iter().any(|(k, _)| *k == t)
}

/// The raw x-deal-v0 record schema.
pub fn profile_schema() -> &'static [u8] {
    PROFILE_SCHEMA
}

struct Compiled {
    schemas: Schemas,
    profile: SchemaIndex,
    records: Vec<(&'static str, SchemaIndex)>,
}

fn compile() -> Result<Compiled, String> {
    let mut schemas = Schemas::new();
    let mut c = Compiler::new();
    let doc: Value = serde_json::from_slice(PROFILE_SCHEMA).map_err(|e| e.to_string())?;
    c.add_resource(PROFILE_SCHEMA_ID, doc)
        .map_err(|e| e.to_string())?;
    for (name, raw) in RECORD_SCHEMAS {
        let doc: Value = serde_json::from_slice(raw).map_err(|e| e.to_string())?;
        c.add_resource(&format!("{RECORDS_BASE}{name}"), doc)
            .map_err(|e| e.to_string())?;
    }
    let profile = c
        .compile(PROFILE_SCHEMA_ID, &mut schemas)
        .map_err(|e| e.to_string())?;
    let mut records = Vec::new();
    for t in RECORD_SCHEMA_TYPES {
        let loc = format!("{RECORDS_BASE}{}.schema.json", t.replace('/', "-"));
        let idx = c.compile(&loc, &mut schemas).map_err(|e| e.to_string())?;
        records.push((*t, idx));
    }
    Ok(Compiled {
        schemas,
        profile,
        records,
    })
}

fn compiled() -> Result<&'static Compiled, Error> {
    static C: OnceLock<Result<Compiled, String>> = OnceLock::new();
    C.get_or_init(compile).as_ref().map_err(|e| {
        Error::Invalid(format!(
            "the vendored x-deal-v0 schemas do not compile: {e}"
        ))
    })
}

/// Validates `doc` against the profile schema, or against the typed record
/// schema of `typed`, and returns its failed leaves, if any.
pub(crate) fn validate(doc: &Value, typed: Option<&str>) -> Result<Option<Vec<String>>, Error> {
    let c = compiled()?;
    let idx = match typed {
        None => c.profile,
        Some(t) => c
            .records
            .iter()
            .find(|(k, _)| *k == t)
            .map(|(_, i)| *i)
            .ok_or_else(|| Error::Invalid(format!("unknown record type {t:?}")))?,
    };
    match c.schemas.validate(doc, idx) {
        Ok(()) => Ok(None),
        Err(e) => {
            let mut leaves = Vec::new();
            schema_leaves(&e, &mut leaves);
            Ok(Some(leaves))
        }
    }
}

/// A validation error's failed leaves, depth first: where, and why.
fn schema_leaves(v: &ValidationError, out: &mut Vec<String>) {
    if v.causes.is_empty() {
        let at: Vec<String> = v
            .instance_location
            .tokens
            .iter()
            .map(|t| match t {
                InstanceToken::Prop(p) => p.to_string(),
                InstanceToken::Item(i) => i.to_string(),
            })
            .collect();
        out.push(format!("/{}: {}", at.join("/"), v.kind));
        return;
    }
    for c in &v.causes {
        schema_leaves(c, out);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_schema_compiles() {
        let c = compiled().expect("compiles");
        assert_eq!(c.records.len(), RECORD_SCHEMA_TYPES.len());
        assert!(validate(&serde_json::json!({}), Some("payment/v0")).is_err());
    }
}
