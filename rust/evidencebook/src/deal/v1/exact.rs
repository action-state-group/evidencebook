//! The one rule for every object a verify path reads, as Go's `exactObject`
//! (`dealprofile/xdealv1/record.go`; evidencebook#559): a pair, a signed
//! Capsule, a settlement member, a record header and the links in it.
//!
//! Go's reader matches a member name in another case (U+017F for `s` and
//! U+212A for `k` included), a later spelling replacing an earlier one, so a
//! signed object could otherwise carry a second spelling of a member that Go
//! reads in place of the one an exact reader takes. Each object must name
//! its members exactly as its type does: a name that differs from one of its
//! fields only by Go's case folding is refused, and so is any other name
//! unless the object is open (it carries members the profile does not
//! read). A member given twice is the text check's to refuse.

use serde_json::Value;

/// A type as the rule reads it: a value read as it is, an object of named
/// members, or a list of a type.
pub(crate) enum Spec {
    Leaf,
    Obj(&'static [(&'static str, Spec)]),
    List(&'static Spec),
    /// An object whose member names this crate does not name (a header's
    /// correlation: its keys are a deployment's vocabulary, which the crate
    /// carries none of): its values must be strings, and no two of its names
    /// may be one name spelled two ways.
    Opaque,
}

/// Go's `encoding/json` name match: ASCII case, U+017F as `s` and U+212A as
/// `k`.
pub(crate) fn go_name_matches(key: &str, field: &str) -> bool {
    let fold = |c: char| match c {
        '\u{017F}' => 's',
        '\u{212A}' => 'k',
        c => c.to_ascii_lowercase(),
    };
    key.chars().count() == field.chars().count()
        && key
            .chars()
            .zip(field.chars())
            .all(|(a, b)| fold(a) == fold(b))
}

/// The first way `v` breaks the rule, as a message.
pub(crate) fn exact_object(v: &Value, spec: &Spec, open: bool, at: &str) -> Result<(), String> {
    match spec {
        Spec::Leaf => Ok(()),
        Spec::Opaque => {
            let Some(o) = v.as_object() else {
                return Ok(());
            };
            let keys: Vec<&String> = o.keys().collect();
            for (i, a) in keys.iter().enumerate() {
                if let Some(b) = keys[i + 1..].iter().find(|b| go_name_matches(a, b)) {
                    return Err(format!("{at} member {b:?} is {a:?} spelled otherwise"));
                }
            }
            Ok(())
        }
        Spec::List(item) => {
            if let Some(l) = v.as_array() {
                for (i, e) in l.iter().enumerate() {
                    exact_object(e, item, open, &format!("{at}[{i}]"))?;
                }
            }
            Ok(())
        }
        Spec::Obj(fields) => {
            let Some(o) = v.as_object() else {
                return Ok(());
            };
            for (k, e) in o {
                if let Some((_, s)) = fields.iter().find(|(n, _)| *n == k.as_str()) {
                    exact_object(e, s, open, &format!("{at}.{k}"))?;
                    continue;
                }
                if let Some((n, _)) = fields.iter().find(|(n, _)| go_name_matches(k, n)) {
                    return Err(format!("{at} member {k:?} is {n:?} spelled otherwise"));
                }
                if !open {
                    return Err(format!("{at} member {k:?} is not one it has"));
                }
            }
            Ok(())
        }
    }
}

/// What the profile reads of a verified Capsule (Go `capsuleWire`), open.
pub(crate) const CAPSULE: Spec = Spec::Obj(&[
    ("action_id", Spec::Leaf),
    ("action_type", Spec::Leaf),
    ("references", Spec::Leaf),
    (
        "model_attestation", // vocabulary-gate: AAC capsule member
        Spec::Obj(&[(
            "compute_attestation",
            Spec::Obj(&[("agent_input_digest", Spec::Leaf)]),
        )]),
    ),
]);

/// What the payment check reads of a settlement member (Go
/// `settlementWire`), open.
pub(crate) const SETTLEMENT: Spec = Spec::Obj(&[
    ("leg", Spec::Leaf),
    ("sealer_role", Spec::Leaf),
    ("terms_ref", Spec::Leaf),
]);

/// Go's `evidencebook.Header` and the structs in it, as the types table
/// (`python/evidencebook/deal/_data`) lists them; a test holds this to it.
pub(crate) const HEADER: Spec = Spec::Obj(&[
    ("v", Spec::Leaf),
    ("book_id", Spec::Leaf),
    ("seq", Spec::Leaf),
    ("record_type", Spec::Leaf),
    ("epistemic_type", Spec::Leaf),
    ("committed_at", Spec::Leaf),
    ("event_time_claim", Spec::Leaf),
    ("payload_commitments", Spec::Leaf),
    ("field_commitments", Spec::Leaf),
    ("commit_alg", Spec::Leaf),
    ("retention_state", Spec::Leaf),
    (
        "links",
        Spec::List(&Spec::Obj(&[("type", Spec::Leaf), ("target", Spec::Leaf)])),
    ),
    ("subject_ref", Spec::Leaf),
    ("principal_ref", Spec::Leaf),
    ("counterparty_ref", Spec::Leaf),
    ("correlation", Spec::Opaque),
    ("reproduction", Spec::Obj(&[("clock", Spec::Leaf)])),
    ("statement", Spec::Leaf),
]);

/// A pair's own members, closed.
pub(crate) const SENT_PAIR: Spec =
    Spec::Obj(&[("record", Spec::Leaf), ("agent_input", Spec::Leaf)]);
pub(crate) const BOOK_PAIR: Spec = Spec::Obj(&[
    ("record", Spec::Leaf),
    ("header", Spec::Leaf),
    ("payload", Spec::Leaf),
]);

#[cfg(test)]
mod tests {
    use super::{Spec, HEADER};
    use serde_json::Value;

    fn names(spec: &Spec) -> Vec<&'static str> {
        match spec {
            Spec::Obj(f) => f.iter().map(|(n, _)| *n).collect(),
            _ => Vec::new(),
        }
    }

    fn field<'a>(spec: &'a Spec, name: &str) -> &'a Spec {
        match spec {
            Spec::Obj(f) => &f.iter().find(|(n, _)| *n == name).unwrap().1,
            _ => unreachable!(),
        }
    }

    /// HEADER is the Go Header's, as the types table lists it.
    #[test]
    fn header_is_the_go_headers() {
        let p = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../python/evidencebook/deal/_data/x-deal-v1-types.json");
        let t: Value = serde_json::from_slice(&std::fs::read(&p).unwrap()).unwrap();
        let go = |s: &str| -> Vec<String> {
            t["structs"][format!("github.com/action-state-group/evidencebook.{s}")]
                .as_array()
                .unwrap()
                .iter()
                .map(|f| f["name"].as_str().unwrap().to_string())
                .collect()
        };
        assert_eq!(go("Header"), names(&HEADER));
        let Spec::List(link) = field(&HEADER, "links") else {
            panic!("links is a list");
        };
        assert_eq!(go("Link"), names(link));
        assert!(matches!(field(&HEADER, "correlation"), Spec::Opaque));
        assert_eq!(go("Reproduction"), names(field(&HEADER, "reproduction")));
    }
}
