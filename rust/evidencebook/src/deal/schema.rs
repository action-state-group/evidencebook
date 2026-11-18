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

/// RE2's `\s`, which Go's validator reads a schema pattern's `\s` as. The
/// validator here reads it as ECMA-262 does, which also takes `\v`, U+00A0,
/// U+FEFF and other Unicode white space.
const RE2_SPACE: &str = "\\t\\n\\f\\r ";

/// Rewrites each `pattern` of a loaded schema so its `\s` is RE2's, as Go's
/// validator reads it (evidencebook#546). A pattern's `$` is already the end
/// of the text here, as in RE2. A `\S` inside a character class has no such
/// rewrite and is refused: no profile schema writes one.
pub(crate) fn re2_patterns(v: &mut Value) -> Result<(), String> {
    match v {
        Value::Object(o) => {
            for (k, e) in o.iter_mut() {
                if k == "pattern" {
                    if let Value::String(p) = e {
                        *p = re2_pattern(p)?;
                        continue;
                    }
                }
                re2_patterns(e)?;
            }
        }
        Value::Array(a) => {
            for e in a {
                re2_patterns(e)?;
            }
        }
        _ => {}
    }
    Ok(())
}

fn re2_pattern(p: &str) -> Result<String, String> {
    let mut out = String::with_capacity(p.len());
    let mut in_class = false;
    let mut chars = p.chars();
    while let Some(c) = chars.next() {
        match c {
            '\\' => match chars.next() {
                Some('s') if in_class => out.push_str(RE2_SPACE),
                Some('s') => out.push_str(&format!("[{RE2_SPACE}]")),
                Some('S') if in_class => {
                    return Err(format!("schema pattern {p:?}: \\S inside a class"))
                }
                Some('S') => out.push_str(&format!("[^{RE2_SPACE}]")),
                Some(e) => {
                    out.push('\\');
                    out.push(e);
                }
                None => out.push('\\'),
            },
            '[' if !in_class => {
                in_class = true;
                out.push(c);
            }
            ']' if in_class => {
                in_class = false;
                out.push(c);
            }
            _ => out.push(c),
        }
    }
    Ok(out)
}

fn compile() -> Result<Compiled, String> {
    let mut schemas = Schemas::new();
    let mut c = Compiler::new();
    let mut doc: Value = serde_json::from_slice(PROFILE_SCHEMA).map_err(|e| e.to_string())?;
    re2_patterns(&mut doc)?;
    c.add_resource(PROFILE_SCHEMA_ID, doc)
        .map_err(|e| e.to_string())?;
    for (name, raw) in RECORD_SCHEMAS {
        let mut doc: Value = serde_json::from_slice(raw).map_err(|e| e.to_string())?;
        re2_patterns(&mut doc)?;
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

#[cfg(test)]
mod re2_tests {
    use super::{re2_pattern, re2_patterns};
    use serde_json::json;

    // evidencebook#546: a pattern's \s is RE2's, in and out of a class.
    #[test]
    fn a_pattern_white_space_class_is_re2s() {
        assert_eq!(
            re2_pattern(r"^[^\s/@]+\.[^\s/@]+$").unwrap(),
            r"^[^\t\n\f\r /@]+\.[^\t\n\f\r /@]+$"
        );
        assert_eq!(
            re2_pattern(r"a\sb\Sc").unwrap(),
            r"a[\t\n\f\r ]b[^\t\n\f\r ]c"
        );
        assert_eq!(re2_pattern(r"\\s").unwrap(), r"\\s", "an escaped backslash");
        assert!(re2_pattern(r"[\S]").is_err());
        let mut doc = json!({"a": {"pattern": r"\s"}, "b": [{"pattern": r"x\s"}], "pattern": {"pattern": r"\s"}});
        re2_patterns(&mut doc).unwrap();
        assert_eq!(
            doc,
            json!({"a": {"pattern": r"[\t\n\f\r ]"}, "b": [{"pattern": r"x[\t\n\f\r ]"}], "pattern": {"pattern": r"[\t\n\f\r ]"}})
        );
    }
}
