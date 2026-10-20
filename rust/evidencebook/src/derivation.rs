//! Definition-digest derivations: the derivations a responder serves by the
//! digest of their definition document (request draft -00 §3.3).
//!
//! I-D section: *Answering a Request*. The port of the Go book's
//! `Book.RegisterDerivation` (`interop_derivation.go`), with the same rules
//! and the same artifact (`testdata/derivation/vectors.json` is answered by
//! both):
//!
//! - the identifier is the SHA-256, in lowercase hex, of RFC 8785 of the
//!   definition document, which must be a JSON object;
//! - a definition declares the subject forms it is defined over, never
//!   `checkpoints`, and its field set, which only names record header
//!   members ([`HEADER_FIELDS`]);
//! - a definition that names a recipient is answered to that authenticated
//!   requester only; anyone else is refused `not_authorized`. One that names
//!   none is answered under the responder's own share policy.
//!
//! A recipient-specific disclosure is such a derivation (interop profile
//! §6.5): a new identity, never a variant of the canonical artifact of the
//! underived subject, which registering a derivation does not change.
//!
//! The interop profile leaves the definition's member names open (P-14), so
//! this module reads none of them: the holder states what the definition
//! declares in [`DerivationDefinition`] as well.
//!
//! [`Derivations`] holds registrations in memory only. A host registers its
//! definitions each time it opens its book; they are never written into the
//! book's history and never listed in a manifest, because a
//! recipient-specific definition names its recipient.
//!
//! An answer to a registered derivation is [`crate::answer::build`] with
//! [`crate::answer::Options::derivations`] set: RFC 8785 of `{anchor,
//! derivation, evidence_stream, records, subject}`, each record carrying its
//! `digest`, its `leaf_index` and the declared header members it has
//! ([`crate::store::RecordSource::header_of`]), and nothing else.

use crate::request::SubjectKind;
use evidence_request::registry::{Reason, SubjectForm};
use evidence_request::request::{Derivation, Request};
use serde::de::{self, DeserializeSeed, MapAccess, SeqAccess, Visitor};
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::fmt;

/// The record header members a projection may declare, as the Go book names
/// them (`headerFields`).
pub const HEADER_FIELDS: [&str; 15] = [
    "book_id",
    "committed_at",
    "correlation",
    "counterparty_ref",
    "epistemic_type",
    "event_time_claim",
    "links",
    "payload_commitments",
    "principal_ref",
    "record_type",
    "retention_state",
    "seq",
    "statement",
    "subject_ref",
    "v",
];

/// The deepest a definition document nests, as the Go request reader counts
/// it: the document itself is at depth 0, and no value may be deeper than
/// this.
const MAX_NESTING: usize = 32;

/// A derivation a responder serves by the digest of its definition.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DerivationDefinition {
    /// The definition document, a JSON object.
    pub document: Vec<u8>,
    /// The authenticated requester the projection is prepared for, as the
    /// definition names it. Only that requester is answered. `None` (or an
    /// empty name) answers it under the responder's share policy like any
    /// protected subject.
    pub recipient: Option<String>,
    /// The subject forms it is defined over; any other form is refused
    /// `no_such_subject` (-00 §3.3).
    pub forms: Vec<SubjectKind>,
    /// The declared field set: the record header members the projection
    /// reveals, and nothing else (-00 §8).
    pub fields: Vec<String>,
}

/// Why a definition was not registered.
#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum DerivationError {
    /// Not one well-formed JSON value: a syntax error, a duplicate member,
    /// or deeper than the request reader allows.
    #[error("derivation definition: {0}")]
    NotJson(String),
    #[error("a derivation definition is a JSON object")]
    NotAnObject,
    /// The document has no RFC 8785 form here: a number that is not an
    /// integer, or an integer outside +/-(2^53 - 1).
    #[error("derivation definition: {0}")]
    Canonical(String),
    #[error("a derivation declares its subject forms and its field set")]
    Undeclared,
    #[error("a derivation over records cannot be defined over checkpoints")]
    OverCheckpoints,
    #[error("{0:?} is not a record header member")]
    NotAHeaderMember(String),
}

/// One registered derivation.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Registered {
    id: String,
    recipient: Option<String>,
    forms: Vec<SubjectKind>,
    fields: Vec<String>,
}

impl Registered {
    /// The derivation's identifier: the digest of its definition's JCS.
    pub fn id(&self) -> &str {
        &self.id
    }

    /// The one requester it is answered to, when the definition names one.
    pub fn recipient(&self) -> Option<&str> {
        self.recipient.as_deref()
    }

    /// The subject forms it is defined over.
    pub fn forms(&self) -> &[SubjectKind] {
        &self.forms
    }

    /// The header members each record of its artifact may carry.
    pub fn fields(&self) -> &[String] {
        &self.fields
    }

    /// Whether it is defined over this subject form.
    pub fn declares(&self, form: SubjectForm) -> bool {
        self.forms.iter().any(|&kind| subject_form(kind) == form)
    }

    /// Whether `requester` (`None` when unauthenticated) is answered:
    /// `Some(true)` or `Some(false)` when the definition names a recipient,
    /// `None` when it names none and the responder's share policy decides.
    pub fn authorizes(&self, requester: Option<&str>) -> Option<bool> {
        self.recipient
            .as_deref()
            .map(|recipient| requester == Some(recipient))
    }
}

/// The derivations a responder has registered, in memory only.
#[derive(Clone, Debug, Default)]
pub struct Derivations {
    by_id: BTreeMap<String, Registered>,
}

impl Derivations {
    pub fn new() -> Self {
        Self::default()
    }

    /// Make `definition` answerable by its identifier, which is returned.
    /// Registering a definition again replaces what was declared for it.
    pub fn register(
        &mut self,
        definition: DerivationDefinition,
    ) -> Result<String, DerivationError> {
        let document = decode_strict(&definition.document)?;
        if !document.is_object() {
            return Err(DerivationError::NotAnObject);
        }
        let canonical = crate::canonical::jcs(&document)
            .map_err(|e| DerivationError::Canonical(e.to_string()))?;
        if definition.forms.is_empty() || definition.fields.is_empty() {
            return Err(DerivationError::Undeclared);
        }
        if definition.forms.contains(&SubjectKind::Checkpoints) {
            return Err(DerivationError::OverCheckpoints);
        }
        if let Some(field) = definition
            .fields
            .iter()
            .find(|f| !HEADER_FIELDS.contains(&f.as_str()))
        {
            return Err(DerivationError::NotAHeaderMember(field.clone()));
        }
        let id = hex::encode(Sha256::digest(&canonical));
        let registered = Registered {
            id: id.clone(),
            recipient: definition.recipient.filter(|r| !r.is_empty()),
            forms: definition.forms,
            fields: definition.fields,
        };
        self.by_id.insert(id.clone(), registered);
        Ok(id)
    }

    /// The registered derivation with this identifier.
    pub fn get(&self, id: &str) -> Option<&Registered> {
        self.by_id.get(id)
    }

    /// The subject forms a registered derivation is defined over, for
    /// `evidence_request::resolve::Responder::derivation_forms`; `None` for
    /// a derivation not registered here.
    pub fn forms_of(&self, derivation: &Derivation) -> Option<Vec<SubjectForm>> {
        match derivation {
            Derivation::Digest(id) => self
                .get(id)
                .map(|r| r.forms.iter().map(|&kind| subject_form(kind)).collect()),
            Derivation::Token(_) => None,
        }
    }

    /// The derivation steps of a responder's decision, in the request
    /// draft's order, as the Go responder takes them: a definition digest
    /// not registered here is `derivation_unsupported`; a registered one not
    /// defined over the subject's form is `no_such_subject`; one prepared
    /// for a recipient other than `requester` (`None` when unauthenticated)
    /// is `not_authorized`. Otherwise the registered derivation, which the
    /// caller answers after its own remaining checks (a derivation naming
    /// no recipient still passes its share policy). A request with no
    /// derivation, or a token derivation, is `Ok(None)`: not this
    /// registry's to decide.
    ///
    /// A responder with the uniform refusal policy maps the reason as it
    /// maps every other.
    pub fn check(
        &self,
        request: &Request,
        requester: Option<&str>,
    ) -> Result<Option<&Registered>, Reason> {
        let Some(Derivation::Digest(id)) = &request.derivation else {
            return Ok(None);
        };
        let registered = self.get(id).ok_or(Reason::DerivationUnsupported)?;
        if !registered.declares(request.subject.form()) {
            return Err(Reason::NoSuchSubject);
        }
        if registered.authorizes(requester) == Some(false) {
            return Err(Reason::NotAuthorized);
        }
        Ok(Some(registered))
    }
}

/// The request draft's subject form for a subject kind: the registry form
/// with the same wire token.
pub(crate) fn subject_form(kind: SubjectKind) -> SubjectForm {
    serde_json::to_value(kind)
        .ok()
        .as_ref()
        .and_then(Value::as_str)
        .and_then(SubjectForm::from_token)
        .expect("every subject kind is a registry form")
}

/// One JSON value, read as the Go request reader reads it
/// (`evidencerequest.DecodeJSON`): a duplicate member, invalid UTF-8, an
/// unpaired surrogate escape, trailing data or nesting deeper than
/// [`MAX_NESTING`] is refused; a number that is not an integer (any number
/// written with a fraction or an exponent) has no canonical form and is
/// refused; `-0` is the integer 0.
fn decode_strict(document: &[u8]) -> Result<Value, DerivationError> {
    let text = std::str::from_utf8(document)
        .map_err(|_| DerivationError::NotJson("JSON text is not valid UTF-8".into()))?;
    // serde_json reads `-0` as a float and a fraction as a float alike, so
    // the number tokens are read here first, as text, as the Go JCS reads
    // them: a fraction or exponent is no integer.
    if number_tokens(text).any(|t| t.contains(['.', 'e', 'E'])) {
        return Err(DerivationError::Canonical(
            "a number that is not an integer has no canonical form".into(),
        ));
    }
    let mut reader = serde_json::Deserializer::from_str(text);
    let value = Strict { depth: 0 }
        .deserialize(&mut reader)
        .map_err(|e| DerivationError::NotJson(e.to_string()))?;
    reader
        .end()
        .map_err(|e| DerivationError::NotJson(e.to_string()))?;
    Ok(value)
}

/// The number tokens of a JSON text, outside its strings.
fn number_tokens(text: &str) -> impl Iterator<Item = &str> {
    let bytes = text.as_bytes();
    let mut i = 0;
    let mut in_string = false;
    std::iter::from_fn(move || {
        while i < bytes.len() {
            let c = bytes[i];
            if in_string {
                match c {
                    b'\\' => i += 1,
                    b'"' => in_string = false,
                    _ => {}
                }
                i += 1;
            } else if c == b'"' {
                in_string = true;
                i += 1;
            } else if c == b'-' || c.is_ascii_digit() {
                let start = i;
                while i < bytes.len()
                    && matches!(bytes[i], b'0'..=b'9' | b'-' | b'+' | b'.' | b'e' | b'E')
                {
                    i += 1;
                }
                return Some(&text[start..i]);
            } else {
                i += 1;
            }
        }
        None
    })
}

/// A JSON value with no duplicate member and no value deeper than
/// [`MAX_NESTING`].
struct Strict {
    depth: usize,
}

impl<'de> DeserializeSeed<'de> for Strict {
    type Value = Value;

    fn deserialize<D: de::Deserializer<'de>>(self, deserializer: D) -> Result<Value, D::Error> {
        if self.depth > MAX_NESTING {
            return Err(de::Error::custom("nested too deeply"));
        }
        deserializer.deserialize_any(self)
    }
}

impl<'de> Visitor<'de> for Strict {
    type Value = Value;

    fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str("a JSON value")
    }

    fn visit_unit<E>(self) -> Result<Value, E> {
        Ok(Value::Null)
    }

    fn visit_bool<E>(self, v: bool) -> Result<Value, E> {
        Ok(Value::Bool(v))
    }

    fn visit_i64<E>(self, v: i64) -> Result<Value, E> {
        Ok(Value::from(v))
    }

    fn visit_u64<E>(self, v: u64) -> Result<Value, E> {
        Ok(Value::from(v))
    }

    /// Every fraction was refused before parsing, so a float here is `-0`
    /// (the integer 0) or an integer too large for 64 bits, which keeps its
    /// float form and is refused as unsafe by the canonical encoder.
    fn visit_f64<E: de::Error>(self, v: f64) -> Result<Value, E> {
        if v == 0.0 {
            return Ok(Value::from(0));
        }
        serde_json::Number::from_f64(v)
            .map(Value::Number)
            .ok_or_else(|| E::custom("a number with no JSON form"))
    }

    fn visit_str<E>(self, v: &str) -> Result<Value, E> {
        Ok(Value::String(v.to_owned()))
    }

    fn visit_string<E>(self, v: String) -> Result<Value, E> {
        Ok(Value::String(v))
    }

    fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> Result<Value, A::Error> {
        let mut items = Vec::new();
        while let Some(item) = seq.next_element_seed(Strict {
            depth: self.depth + 1,
        })? {
            items.push(item);
        }
        Ok(Value::Array(items))
    }

    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<Value, A::Error> {
        let mut members = Map::new();
        while let Some(name) = map.next_key::<String>()? {
            if members.contains_key(&name) {
                return Err(de::Error::custom(format!("duplicate member {name:?}")));
            }
            let value = map.next_value_seed(Strict {
                depth: self.depth + 1,
            })?;
            members.insert(name, value);
        }
        Ok(Value::Object(members))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn define(document: &str) -> DerivationDefinition {
        DerivationDefinition {
            document: document.as_bytes().to_vec(),
            recipient: None,
            forms: vec![SubjectKind::Record],
            fields: vec!["seq".into()],
        }
    }

    #[test]
    fn the_identifier_is_the_digest_of_the_jcs() {
        let mut d = Derivations::new();
        let a = d.register(define(r#"{ "b": [1, "x"], "a": -0 }"#)).unwrap();
        let b = d.register(define(r#"{"a":0,"b":[1,"x"]}"#)).unwrap();
        assert_eq!(a, b);
        assert_eq!(a, hex::encode(Sha256::digest(br#"{"a":0,"b":[1,"x"]}"#)));
    }

    #[test]
    fn the_reader_refuses_what_the_go_reader_refuses() {
        let mut d = Derivations::new();
        for bad in [
            r#"{"a":1,"a":2}"#,
            r#"{"a":1.0}"#,
            r#"{"a":1e2}"#,
            r#"{"a":-0.0}"#,
            r#"{"a":9007199254740992}"#,
            r#"{"a":123456789012345678901234567890}"#,
            r#"{"a":"\ud800"}"#,
            r#"{} {}"#,
            "[1]",
            "\"text\"",
        ] {
            assert!(d.register(define(bad)).is_err(), "{bad}");
        }
        // Inside a string, a dot or an exponent letter is text.
        assert!(d.register(define(r#"{"a":"1.5e3","b\"":"-0.1"}"#)).is_ok());
        let nested = |n: usize| format!("{}{}", "[".repeat(n), "]".repeat(n));
        // The document is depth 0 and no value may be deeper than 32: an
        // object holding 32 nested arrays reaches depth 32.
        assert!(d
            .register(define(&format!(r#"{{"a":{}}}"#, nested(32))))
            .is_ok());
        assert!(d
            .register(define(&format!(r#"{{"a":{}}}"#, nested(33))))
            .is_err());
        assert!(d
            .register(define(&format!(
                r#"{{"a":{}0{}}}"#,
                "[".repeat(32),
                "]".repeat(32)
            )))
            .is_err());
    }

    #[test]
    fn a_definition_declares_record_forms_and_header_fields() {
        let mut d = Derivations::new();
        let mut def = define("{}");
        def.forms.clear();
        assert_eq!(d.register(def), Err(DerivationError::Undeclared));
        let mut def = define("{}");
        def.fields.clear();
        assert_eq!(d.register(def), Err(DerivationError::Undeclared));
        let mut def = define("{}");
        def.forms.push(SubjectKind::Checkpoints);
        assert_eq!(d.register(def), Err(DerivationError::OverCheckpoints));
        let mut def = define("{}");
        def.fields.push("payload".into());
        assert_eq!(
            d.register(def),
            Err(DerivationError::NotAHeaderMember("payload".into()))
        );
    }

    #[test]
    fn a_recipient_is_the_only_requester_answered() {
        let mut d = Derivations::new();
        let mut def = define(r#"{"recipient":"alice"}"#);
        def.recipient = Some("alice".into());
        let id = d.register(def).unwrap();
        let r = d.get(&id).unwrap();
        assert_eq!(r.authorizes(Some("alice")), Some(true));
        assert_eq!(r.authorizes(Some("bob")), Some(false));
        assert_eq!(r.authorizes(None), Some(false));
        let mut def = define("{}");
        def.recipient = Some(String::new());
        let open = d.register(def).unwrap();
        assert_eq!(d.get(&open).unwrap().authorizes(None), None);
    }
}
