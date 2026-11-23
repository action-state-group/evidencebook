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
//! - a definition that names recipients (`recipient` and `recipients`, one
//!   grant set: a Terms Pack's digest served to several requesters) is
//!   answered to those authenticated requesters only; anyone else is refused
//!   `not_authorized`. One that names none is answered under the responder's
//!   own share policy;
//! - a declared field may be a path into a header member
//!   (`statement.generated_at`), which reveals that member only;
//! - with `proofs` the answer carries the anchor's checkpoint and a proof per
//!   record ([`crate::derived`] checks them).
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
//! ([`crate::store::RecordSource::header_of`]), and nothing else; with
//! `proofs`, the material goes beside the artifact in the envelope.

use crate::request::SubjectKind;
use evidence_request::registry::{Reason, SubjectForm};
use evidence_request::request::{Derivation, Request};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

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
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct DerivationDefinition {
    /// The definition document, a JSON object.
    pub document: Vec<u8>,
    /// The authenticated requester the projection is prepared for, as the
    /// definition names it. `None` (or an empty name) names no one: with no
    /// [`Self::recipients`] either, it is answered under the responder's
    /// share policy like any protected subject.
    pub recipient: Option<String>,
    /// Further requesters the holder grants the same derivation to, so one
    /// definition digest (a Terms Pack's) is served to several of them. The
    /// grant is `recipient` and `recipients` together, as a set; an empty
    /// name here is [`DerivationError::EmptyRecipient`]. Every one is
    /// answered the same bytes.
    pub recipients: Vec<String>,
    /// The subject forms it is defined over; any other form is refused
    /// `no_such_subject` (-00 §3.3).
    pub forms: Vec<SubjectKind>,
    /// The declared field set: what the projection reveals of each record,
    /// and nothing else (-00 §8). A field is a record header member
    /// (`statement`) or a path into one (`statement.generated_at`, names
    /// joined by `.`; the Plan IR's field-path grammar over objects). A path
    /// reveals that member only, nested as the record holds it. A path whose
    /// step is not an object in a record is left out of that record's
    /// projection. A path and a field it extends, or one of its own
    /// prefixes, are [`DerivationError::FieldConflict`] together.
    pub fields: Vec<String>,
    /// Make the answer carry the anchor's signed checkpoint, one inclusion
    /// proof per record in record order and, when the checkpoint extends an
    /// earlier one, the proof that it does (Go `cllSetMaterial`). The rows
    /// are then the holder's log's records at the anchor, whatever values
    /// the holder attests for them: membership of the rows given, never that
    /// no row was left out. Off, the answer carries no verification
    /// material (P-13).
    pub proofs: bool,
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
    #[error("{0:?} is not a record header member or a path into one")]
    NotAHeaderMember(String),
    /// A declared field extends another, or is extended by it: they would
    /// say two things about one member.
    #[error("{0:?} and {1:?} declare the same member twice")]
    FieldConflict(String, String),
    #[error("a derivation recipient has a name")]
    EmptyRecipient,
    /// The document is registered already with a different grant, different
    /// sets of forms or fields, or a different proofs setting. Its
    /// identifier binds only the document, so registering it again must not
    /// change what it answers.
    #[error(
        "derivation {0} is registered with a different recipient, forms, fields or proofs setting"
    )]
    Redeclared(String),
}

/// One registered derivation.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Registered {
    id: String,
    /// `recipient` and `recipients` as one sorted set; empty when the holder
    /// named no one.
    grants: Vec<String>,
    forms: Vec<SubjectKind>,
    fields: Vec<String>,
    paths: Vec<Vec<String>>,
    proofs: bool,
}

impl Registered {
    /// The derivation's identifier: the digest of its definition's JCS.
    pub fn id(&self) -> &str {
        &self.id
    }

    /// The requesters it is answered to, sorted, when the definition names
    /// any.
    pub fn grants(&self) -> &[String] {
        &self.grants
    }

    /// Whether its answer carries the anchor's checkpoint and a proof per
    /// record.
    pub fn proofs(&self) -> bool {
        self.proofs
    }

    /// The subject forms it is defined over.
    pub fn forms(&self) -> &[SubjectKind] {
        &self.forms
    }

    /// The declared fields, as written.
    pub fn fields(&self) -> &[String] {
        &self.fields
    }

    /// The declared fields, each as its names: a header member and the
    /// object members below it.
    pub fn paths(&self) -> &[Vec<String>] {
        &self.paths
    }

    /// Whether it is defined over this subject form.
    pub fn declares(&self, form: SubjectForm) -> bool {
        self.forms.iter().any(|&kind| subject_form(kind) == form)
    }

    /// Whether `requester` (`None` when unauthenticated) is answered:
    /// `Some(true)` or `Some(false)` when the definition names recipients,
    /// `None` when it names none and the responder's share policy decides.
    pub fn authorizes(&self, requester: Option<&str>) -> Option<bool> {
        if self.grants.is_empty() {
            return None;
        }
        Some(requester.is_some_and(|r| self.grants.iter().any(|g| g == r)))
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
    /// Registering a document again with the same recipient and the same
    /// sets of forms and fields returns the same identifier and changes
    /// nothing; with anything else it is [`DerivationError::Redeclared`],
    /// and the first registration stands.
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
        let paths = check_field_set(&definition.fields)?;
        if definition.recipients.iter().any(String::is_empty) {
            return Err(DerivationError::EmptyRecipient);
        }
        let mut grants: Vec<String> = definition
            .recipient
            .filter(|r| !r.is_empty())
            .into_iter()
            .chain(definition.recipients)
            .collect();
        grants.sort();
        grants.dedup();
        let id = hex::encode(Sha256::digest(&canonical));
        if let Some(prev) = self.by_id.get(&id) {
            return if prev.grants == grants
                && prev.proofs == definition.proofs
                && same_set(&prev.forms, &definition.forms)
                && same_set(&prev.fields, &definition.fields)
            {
                Ok(id)
            } else {
                Err(DerivationError::Redeclared(id))
            };
        }
        let registered = Registered {
            id: id.clone(),
            grants,
            forms: definition.forms,
            fields: definition.fields,
            paths,
            proofs: definition.proofs,
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

    /// The derivation steps of a responder's decision, in the order the Go
    /// responder takes them (`evidencerequest.Decide`): a definition digest
    /// not registered here is `derivation_unsupported`; then authorization,
    /// which for a definition naming a recipient is whether `requester`
    /// (`None` when unauthenticated) is that recipient, and for one naming
    /// none is `share_policy_allows`, the caller's own share policy for this
    /// requester; anyone not authorized is `not_authorized`. Only then is a
    /// subject form the definition does not declare `no_such_subject`.
    /// Otherwise the registered derivation, which the caller answers after
    /// its remaining checks. A request with no derivation, or a token
    /// derivation, is `Ok(None)`: not this registry's to decide, and
    /// `share_policy_allows` is not read.
    ///
    /// A responder with the uniform refusal policy maps the reason as it
    /// maps every other.
    ///
    /// A digest the holder answers with its own `answer::Derive` is not
    /// registered here, so this refuses it: a caller serving one decides it
    /// before calling this.
    pub fn check(
        &self,
        request: &Request,
        requester: Option<&str>,
        share_policy_allows: bool,
    ) -> Result<Option<&Registered>, Reason> {
        let Some(Derivation::Digest(id)) = &request.derivation else {
            return Ok(None);
        };
        let registered = self.get(id).ok_or(Reason::DerivationUnsupported)?;
        if !registered
            .authorizes(requester)
            .unwrap_or(share_policy_allows)
        {
            return Err(Reason::NotAuthorized);
        }
        if !registered.declares(request.subject.form()) {
            return Err(Reason::NoSuchSubject);
        }
        Ok(Some(registered))
    }
}

/// A declared field's names, or `None` when it is not a header member
/// followed by object member names (`^[A-Za-z_][A-Za-z0-9_]*$`, the Plan
/// IR's field-path grammar).
fn field_path(field: &str) -> Option<Vec<String>> {
    let names: Vec<String> = field.split('.').map(str::to_owned).collect();
    if !HEADER_FIELDS.contains(&names[0].as_str()) {
        return None;
    }
    let name_ok = |n: &str| {
        let mut chars = n.chars();
        chars
            .next()
            .is_some_and(|c| c.is_ascii_alphabetic() || c == '_')
            && chars.all(|c| c.is_ascii_alphanumeric() || c == '_')
    };
    names[1..].iter().all(|n| name_ok(n)).then_some(names)
}

/// Each declared field as its names, or the first field that is not a path
/// or that extends (or is extended by) an earlier one. A field declared
/// twice is one field.
fn check_field_set(fields: &[String]) -> Result<Vec<Vec<String>>, DerivationError> {
    let mut paths = Vec::with_capacity(fields.len());
    for (i, field) in fields.iter().enumerate() {
        paths.push(
            field_path(field).ok_or_else(|| DerivationError::NotAHeaderMember(field.clone()))?,
        );
        for other in &fields[..i] {
            if field.starts_with(&format!("{other}.")) || other.starts_with(&format!("{field}.")) {
                return Err(DerivationError::FieldConflict(other.clone(), field.clone()));
            }
        }
    }
    Ok(paths)
}

/// Whether `a` and `b` hold the same members, in any order and with any
/// repetition.
fn same_set<T: PartialEq>(a: &[T], b: &[T]) -> bool {
    a.iter().all(|x| b.contains(x)) && b.iter().all(|x| a.contains(x))
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
/// (`evidencerequest.DecodeJSON`) and then canonicalized as the Go JCS
/// canonicalizes it (`evidencerequest.EncodeJCS`).
///
/// Reading refuses ([`DerivationError::NotJson`]) invalid UTF-8, an
/// unpaired surrogate escape, a duplicate member (after unescaping), any
/// value deeper than [`MAX_NESTING`] (the document is depth 0), text that is
/// not RFC 8259 JSON, and trailing data. Then a number written with a
/// fraction or an exponent, or an integer outside +/-(2^53 - 1), has no
/// canonical form ([`DerivationError::Canonical`]); `-0` is the integer 0.
///
/// The reader is this module's own rather than `serde_json`'s, so what it
/// accepts does not depend on `serde_json`'s features (`arbitrary_precision`
/// reads numbers through a private member name a document may also use).
pub(crate) fn decode_strict(document: &[u8]) -> Result<Value, DerivationError> {
    let text = std::str::from_utf8(document)
        .map_err(|_| DerivationError::NotJson("JSON text is not valid UTF-8".into()))?;
    let mut reader = Reader {
        text: text.as_bytes(),
        at: 0,
        canonical: None,
        keep_numbers: false,
        refuse_lossy: false,
    };
    reader.space();
    let value = reader.value(0)?;
    reader.space();
    if reader.at != reader.text.len() {
        return Err(reader.malformed("trailing data after the JSON value"));
    }
    match reader.canonical {
        Some(reason) => Err(DerivationError::Canonical(reason)),
        None => Ok(value),
    }
}

/// One JSON value read as [`decode_strict`] reads it (UTF-8, no duplicate
/// member, no unpaired surrogate, bounded nesting, no trailing data), but with
/// every number kept as written, so a document that is not to be
/// canonicalized (a JSON-RPC message) is not refused for a number with no
/// canonical form. An integer of at most 18 digits is an integer; any other
/// number is a float, which no JSON-RPC id may be.
pub(crate) fn decode_syntax(document: &[u8]) -> Result<Value, DerivationError> {
    decode_numbers_as_written(document, false)
}

/// [`decode_syntax`] for a document whose values are read back as they are
/// (a holder's answer): a number the model cannot hold, an infinite one such
/// as `1e400` or an integer of more than 18 digits, is refused instead of
/// becoming `null` or a rounded float.
pub(crate) fn decode_syntax_exact(document: &[u8]) -> Result<Value, DerivationError> {
    decode_numbers_as_written(document, true)
}

fn decode_numbers_as_written(document: &[u8], exact: bool) -> Result<Value, DerivationError> {
    let text = std::str::from_utf8(document)
        .map_err(|_| DerivationError::NotJson("JSON text is not valid UTF-8".into()))?;
    let mut reader = Reader {
        text: text.as_bytes(),
        at: 0,
        canonical: None,
        keep_numbers: true,
        refuse_lossy: exact,
    };
    reader.space();
    let value = reader.value(0)?;
    reader.space();
    if reader.at != reader.text.len() {
        return Err(reader.malformed("trailing data after the JSON value"));
    }
    Ok(value)
}

/// A strict RFC 8259 reader over valid UTF-8.
struct Reader<'a> {
    text: &'a [u8],
    at: usize,
    /// The first number with no canonical form: reported only once the
    /// whole document reads, as Go reads before it canonicalizes.
    canonical: Option<String>,
    /// Read a number with no canonical form as the number it is, instead of
    /// as `null` with `canonical` set ([`decode_syntax`]).
    keep_numbers: bool,
    /// With `keep_numbers`, refuse a number kept lossily (see
    /// [`decode_syntax_exact`]).
    refuse_lossy: bool,
}

impl Reader<'_> {
    fn malformed(&self, what: &str) -> DerivationError {
        DerivationError::NotJson(format!("{what} at byte {}", self.at))
    }

    fn peek(&self) -> Option<u8> {
        self.text.get(self.at).copied()
    }

    fn space(&mut self) {
        while matches!(self.peek(), Some(b' ' | b'\t' | b'\n' | b'\r')) {
            self.at += 1;
        }
    }

    fn expect(&mut self, byte: u8) -> Result<(), DerivationError> {
        if self.peek() != Some(byte) {
            return Err(self.malformed(&format!("expected {:?}", byte as char)));
        }
        self.at += 1;
        Ok(())
    }

    fn value(&mut self, depth: usize) -> Result<Value, DerivationError> {
        if depth > MAX_NESTING {
            return Err(self.malformed("nested too deeply"));
        }
        match self.peek() {
            Some(b'{') => self.object(depth),
            Some(b'[') => self.array(depth),
            Some(b'"') => self.string().map(Value::String),
            Some(b't') => self.literal("true", Value::Bool(true)),
            Some(b'f') => self.literal("false", Value::Bool(false)),
            Some(b'n') => self.literal("null", Value::Null),
            Some(b'-' | b'0'..=b'9') => self.number(),
            _ => Err(self.malformed("expected a JSON value")),
        }
    }

    fn literal(&mut self, word: &str, value: Value) -> Result<Value, DerivationError> {
        if !self.text[self.at..].starts_with(word.as_bytes()) {
            return Err(self.malformed("expected a JSON value"));
        }
        self.at += word.len();
        Ok(value)
    }

    fn object(&mut self, depth: usize) -> Result<Value, DerivationError> {
        self.expect(b'{')?;
        let mut members = serde_json::Map::new();
        self.space();
        if self.peek() == Some(b'}') {
            self.at += 1;
            return Ok(Value::Object(members));
        }
        loop {
            self.space();
            let name = self.string()?;
            if members.contains_key(&name) {
                return Err(self.malformed(&format!("duplicate member {name:?}")));
            }
            self.space();
            self.expect(b':')?;
            self.space();
            let value = self.value(depth + 1)?;
            members.insert(name, value);
            self.space();
            match self.peek() {
                Some(b',') => self.at += 1,
                Some(b'}') => {
                    self.at += 1;
                    return Ok(Value::Object(members));
                }
                _ => return Err(self.malformed("expected ',' or '}'")),
            }
        }
    }

    fn array(&mut self, depth: usize) -> Result<Value, DerivationError> {
        self.expect(b'[')?;
        let mut items = Vec::new();
        self.space();
        if self.peek() == Some(b']') {
            self.at += 1;
            return Ok(Value::Array(items));
        }
        loop {
            self.space();
            items.push(self.value(depth + 1)?);
            self.space();
            match self.peek() {
                Some(b',') => self.at += 1,
                Some(b']') => {
                    self.at += 1;
                    return Ok(Value::Array(items));
                }
                _ => return Err(self.malformed("expected ',' or ']'")),
            }
        }
    }

    fn string(&mut self) -> Result<String, DerivationError> {
        self.expect(b'"')?;
        let mut out = String::new();
        loop {
            let start = self.at;
            while matches!(self.peek(), Some(c) if c != b'"' && c != b'\\' && c >= 0x20) {
                self.at += 1;
            }
            // The text is valid UTF-8 and the run ends at an ASCII byte, so
            // the run is valid UTF-8 on its own.
            out.push_str(std::str::from_utf8(&self.text[start..self.at]).expect("valid UTF-8"));
            match self.peek() {
                Some(b'"') => {
                    self.at += 1;
                    return Ok(out);
                }
                Some(b'\\') => {
                    self.at += 1;
                    out.push(self.escape()?);
                }
                Some(_) => return Err(self.malformed("control character in a string")),
                None => return Err(self.malformed("unterminated string")),
            }
        }
    }

    /// One escape after its backslash; a surrogate escape must pair.
    fn escape(&mut self) -> Result<char, DerivationError> {
        let c = self
            .peek()
            .ok_or_else(|| self.malformed("unterminated string"))?;
        self.at += 1;
        Ok(match c {
            b'"' => '"',
            b'\\' => '\\',
            b'/' => '/',
            b'b' => '\u{8}',
            b'f' => '\u{c}',
            b'n' => '\n',
            b'r' => '\r',
            b't' => '\t',
            b'u' => {
                let unit = self.hex4()?;
                match unit {
                    0xd800..=0xdbff => {
                        if !self.text[self.at..].starts_with(b"\\u") {
                            return Err(self.malformed("unpaired high surrogate escape"));
                        }
                        self.at += 2;
                        let low = self.hex4()?;
                        if !(0xdc00..=0xdfff).contains(&low) {
                            return Err(self.malformed("unpaired high surrogate escape"));
                        }
                        let scalar = 0x10000 + ((unit - 0xd800) << 10) + (low - 0xdc00);
                        char::from_u32(scalar).expect("a surrogate pair is a scalar value")
                    }
                    0xdc00..=0xdfff => return Err(self.malformed("unpaired low surrogate escape")),
                    _ => char::from_u32(unit).expect("a non-surrogate unit is a scalar value"),
                }
            }
            _ => return Err(self.malformed("invalid escape")),
        })
    }

    fn hex4(&mut self) -> Result<u32, DerivationError> {
        let digits = self
            .text
            .get(self.at..self.at + 4)
            .and_then(|d| std::str::from_utf8(d).ok())
            .filter(|d| d.bytes().all(|b| b.is_ascii_hexdigit()))
            .ok_or_else(|| self.malformed("invalid \\u escape"))?;
        self.at += 4;
        Ok(u32::from_str_radix(digits, 16).expect("four hex digits"))
    }

    /// A number token by RFC 8259's grammar. One with a fraction or an
    /// exponent, or an integer outside +/-(2^53 - 1), reads (as Go reads
    /// it), but has no canonical form.
    fn number(&mut self) -> Result<Value, DerivationError> {
        let start = self.at;
        let digits = |r: &mut Self| {
            let from = r.at;
            while matches!(r.peek(), Some(b'0'..=b'9')) {
                r.at += 1;
            }
            r.at - from
        };
        if self.peek() == Some(b'-') {
            self.at += 1;
        }
        match self.peek() {
            Some(b'0') => self.at += 1,
            Some(b'1'..=b'9') => {
                digits(self);
            }
            _ => return Err(self.malformed("invalid number")),
        }
        let mut integer = true;
        if self.peek() == Some(b'.') {
            self.at += 1;
            integer = false;
            if digits(self) == 0 {
                return Err(self.malformed("invalid number"));
            }
        }
        if matches!(self.peek(), Some(b'e' | b'E')) {
            self.at += 1;
            integer = false;
            if matches!(self.peek(), Some(b'+' | b'-')) {
                self.at += 1;
            }
            if digits(self) == 0 {
                return Err(self.malformed("invalid number"));
            }
        }
        let token = std::str::from_utf8(&self.text[start..self.at]).expect("ASCII");
        let reason = if !integer {
            Some(format!(
                "{token} is not an integer and has no canonical form"
            ))
        } else if unsafe_integer(token) {
            Some(format!("integer {token} is outside +/-(2^53 - 1)"))
        } else {
            None
        };
        if let Some(reason) = reason {
            if self.keep_numbers {
                let whole = token
                    .parse::<i64>()
                    .ok()
                    .filter(|_| token.trim_start_matches('-').len() <= 18)
                    .map(Value::from);
                // A long integer is read as the float it rounds to, which an
                // exact reader does not accept as the number it is.
                if self.refuse_lossy && whole.is_none() && integer {
                    return Err(self.malformed("integer of more than 18 digits"));
                }
                let kept = whole.or_else(|| {
                    token
                        .parse::<f64>()
                        .ok()
                        .and_then(serde_json::Number::from_f64)
                        .map(Value::Number)
                });
                return match kept {
                    Some(value) => Ok(value),
                    None if self.refuse_lossy => Err(self.malformed("number out of range")),
                    None => Ok(Value::Null),
                };
            }
            self.canonical.get_or_insert(reason);
            return Ok(Value::Null);
        }
        Ok(Value::from(token.parse::<i64>().expect("a safe integer")))
    }
}

/// Whether an integer token's magnitude exceeds 2^53 - 1, compared as text
/// as the Go canonicalizer compares it (`IsUnsafeInt`).
fn unsafe_integer(token: &str) -> bool {
    let digits = token.strip_prefix('-').unwrap_or(token);
    match digits.len().cmp(&16) {
        std::cmp::Ordering::Greater => true,
        std::cmp::Ordering::Less => false,
        std::cmp::Ordering::Equal => digits > "9007199254740991",
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
            ..Default::default()
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
            r#"{"a":-9007199254740992}"#,
            r#"{"a":18446744073709551616}"#,
            r#"{"a":1,"a":2.5}"#,
            r#"{} {}"#,
            "[1]",
            "\"text\"",
        ] {
            assert!(d.register(define(bad)).is_err(), "{bad}");
        }
        // A member may be named as serde_json names its private number:
        // it is an ordinary member here, read, checked and kept.
        let private = "$serde_json::private::Number";
        assert!(d
            .register(define(&format!(r#"{{"{private}":1,"b":2}}"#)))
            .is_ok());
        assert!(d
            .register(define(&format!(r#"{{"x":{{"{private}":"1.5"}}}}"#)))
            .is_ok());
        assert_eq!(
            d.register(define(&format!(r#"{{"x":{{"{private}":"1.5"}}}}"#)))
                .unwrap(),
            hex::encode(Sha256::digest(format!(r#"{{"x":{{"{private}":"1.5"}}}}"#)))
        );
        for bad in [
            format!(r#"{{"{private}":{{"a":1,"a":2}}}}"#),
            format!(r#"{{"{private}":{}0{}}}"#, "[".repeat(40), "]".repeat(40)),
        ] {
            assert!(
                matches!(d.register(define(&bad)), Err(DerivationError::NotJson(_))),
                "{bad}"
            );
        }
        // Grammar Go's decoder refuses.
        for bad in [
            r#"{"a":01}"#,
            r#"{"a":1.}"#,
            r#"{"a":-}"#,
            r#"{"a":tru}"#,
            r#"{"a":1,}"#,
            "{\"a\":\"\t\"}",
            r#"{"a":"\x"}"#,
            "{'a':1}",
            "",
        ] {
            assert!(
                matches!(d.register(define(bad)), Err(DerivationError::NotJson(_))),
                "{bad}"
            );
        }
        // Read before canonicalized, as in Go: malformed is NotJson even
        // when it also holds a fraction.
        for malformed in [r#"{"a":1,"a":2.5}"#, r#"{"a":1.5"#] {
            assert!(matches!(
                d.register(define(malformed)),
                Err(DerivationError::NotJson(_))
            ));
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

    /// Authorization comes before the declared forms, as in
    /// `evidencerequest.Decide`; with no recipient it is the share policy.
    #[test]
    fn check_authorizes_before_it_reads_the_form() {
        let mut d = Derivations::new();
        let mut def = define(r#"{"recipient":"alice"}"#);
        def.recipient = Some("alice".into());
        let named = d.register(def).unwrap();
        let open = d.register(define(r#"{"open":true}"#)).unwrap();
        let ask = |id: &str, subject: &str| {
            let wire = format!(
                r#"{{"subject":{subject},"coverage":{{"min_freshness":1}},"derivation":"{id}"}}"#
            );
            evidence_request::request::parse_json(wire.as_bytes()).unwrap()
        };
        let record = format!(r#"{{"record":"{}"}}"#, "ab".repeat(32));
        let range = r#"{"range":[0,1]}"#;
        let refused = |r: Result<Option<&Registered>, Reason>| r.err();
        assert_eq!(
            refused(d.check(&ask(&named, range), Some("bob"), true)),
            Some(Reason::NotAuthorized)
        );
        assert_eq!(
            refused(d.check(&ask(&named, range), Some("alice"), false)),
            Some(Reason::NoSuchSubject)
        );
        assert!(d
            .check(&ask(&named, &record), Some("alice"), false)
            .unwrap()
            .is_some());
        assert_eq!(
            refused(d.check(&ask(&open, range), None, false)),
            Some(Reason::NotAuthorized)
        );
        assert_eq!(
            refused(d.check(&ask(&open, range), None, true)),
            Some(Reason::NoSuchSubject)
        );
        assert!(d.check(&ask(&open, &record), None, true).unwrap().is_some());
        let unknown = "cd".repeat(32);
        assert_eq!(
            refused(d.check(&ask(&unknown, &record), None, true)),
            Some(Reason::DerivationUnsupported)
        );
        let plain = evidence_request::request::parse_json(
            br#"{"subject":{"checkpoints":null},"coverage":{"min_freshness":1}}"#,
        )
        .unwrap();
        assert!(d.check(&plain, None, false).unwrap().is_none());
    }

    fn with(f: impl FnOnce(&mut DerivationDefinition)) -> DerivationDefinition {
        let mut def = define(r#"{"pack":"terms"}"#);
        f(&mut def);
        def
    }

    /// The grant is `recipient` and `recipients` together, as one sorted set;
    /// only an empty entry of `recipients` is invalid, an empty `recipient`
    /// names no one.
    #[test]
    fn a_grant_is_a_set_of_requesters() {
        let mut d = Derivations::new();
        let id = d
            .register(with(|def| {
                def.recipient = Some("bob".into());
                def.recipients = vec!["carol".into(), "alice".into(), "bob".into()];
            }))
            .unwrap();
        let r = d.get(&id).unwrap();
        assert_eq!(r.grants(), ["alice", "bob", "carol"]);
        for who in ["alice", "bob", "carol"] {
            assert_eq!(r.authorizes(Some(who)), Some(true), "{who}");
        }
        assert_eq!(r.authorizes(Some("dave")), Some(false));
        assert_eq!(r.authorizes(None), Some(false));
        assert_eq!(r.authorizes(Some("")), Some(false));

        let mut d = Derivations::new();
        let id = d
            .register(with(|def| {
                def.recipient = Some(String::new());
                def.recipients = vec!["alice".into()];
            }))
            .unwrap();
        assert_eq!(d.get(&id).unwrap().grants(), ["alice"]);
        let id = Derivations::new()
            .register(with(|def| def.recipient = Some(String::new())))
            .unwrap();
        assert_eq!(id.len(), 64);
        assert_eq!(
            Derivations::new().register(with(
                |def| def.recipients = vec!["alice".into(), String::new()]
            )),
            Err(DerivationError::EmptyRecipient)
        );
    }

    /// Registering again must not change what it answers: the same grant as a
    /// set, forms, fields and proofs setting, or the first registration stands.
    #[test]
    fn registering_again_with_another_grant_or_proofs_setting_is_refused() {
        let mut d = Derivations::new();
        let first = |def: &mut DerivationDefinition| {
            def.recipients = vec!["alice".into(), "bob".into()];
            def.proofs = true;
        };
        let id = d.register(with(first)).unwrap();
        let again = d
            .register(with(|def| {
                first(def);
                def.recipients.reverse();
                def.recipient = Some("alice".into());
            }))
            .unwrap();
        assert_eq!(again, id);
        for changed in [
            with(|def| def.recipients = vec!["alice".into()]),
            with(|def| {
                first(def);
                def.proofs = false;
            }),
            with(|def| {
                first(def);
                def.recipients.push("carol".into());
            }),
        ] {
            assert_eq!(
                d.register(changed),
                Err(DerivationError::Redeclared(id.clone()))
            );
        }
        assert_eq!(d.get(&id).unwrap().grants(), ["alice", "bob"]);
        assert!(d.get(&id).unwrap().proofs());
    }

    #[test]
    fn a_field_is_a_header_member_or_a_path_into_one() {
        let ok = |fields: &[&str]| {
            Derivations::new().register(with(|def| {
                def.fields = fields.iter().map(|f| (*f).to_owned()).collect();
            }))
        };
        let id = ok(&["statement.generated_at", "record_type", "seq"]).unwrap();
        let mut d = Derivations::new();
        let id2 = d
            .register(with(|def| {
                def.fields = vec!["statement.generated_at".into(), "statement.v1.x_2".into()];
            }))
            .unwrap();
        assert_eq!(id, id2, "the identifier binds the document only");
        assert_eq!(
            d.get(&id2).unwrap().paths(),
            [
                vec!["statement".to_owned(), "generated_at".to_owned()],
                vec!["statement".to_owned(), "v1".to_owned(), "x_2".to_owned()]
            ]
        );
        // A field declared twice is one field.
        assert!(ok(&["seq", "seq"]).is_ok());
        for bad in [
            "payload",
            "statement.",
            ".statement",
            "statement..a",
            "statement.a-b",
            "statement.1a",
            "statement.a b",
            "nope.x",
            "",
        ] {
            assert_eq!(
                ok(&[bad]),
                Err(DerivationError::NotAHeaderMember(bad.to_owned())),
                "{bad:?}"
            );
        }
        // A path and a field it extends, or one of its own prefixes, say two
        // things about one member; a sibling path does not extend it.
        for (a, b) in [
            ("statement", "statement.generated_at"),
            ("statement.generated_at", "statement"),
            ("statement.a", "statement.a.b"),
        ] {
            assert_eq!(
                ok(&[a, b]),
                Err(DerivationError::FieldConflict(a.to_owned(), b.to_owned())),
                "{a} {b}"
            );
        }
        assert!(
            ok(&["statement.a", "statement.ab", "statement2"]).is_err_and(
                |e| matches!(e, DerivationError::NotAHeaderMember(f) if f == "statement2")
            )
        );
        assert!(ok(&["statement.a", "statement.ab"]).is_ok());
    }
}
