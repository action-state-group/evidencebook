//! A record's payload, digest and record-level checks, as Go's `dealprofile`
//! runs them (`check.go`).
//!
//! The stages run in the order the reference checker
//! (`x-deal-v0/check_profile.py`, `check_record`) runs them. The chain
//! stage, which reads a record against the records before it, is not here:
//! CI runs the reference checker over the chains the implementations build.

use std::sync::OnceLock;

use regex::Regex;
use serde_json::{Map, Value};

use super::keys::{fold, go_lower};
use super::names::{CANONICALIZATION, PROFILE};
use super::schema::validate;
use crate::canonical::{jcs, json_digest, JcsError, MAX_SAFE_INTEGER};

pub const STAGE_CANONICALIZATION: &str = "canonicalization";
pub const STAGE_PERSONAL_DATA: &str = "personal_data";
pub const STAGE_WORDING: &str = "wording";
pub const STAGE_SCHEMA: &str = "schema";
/// The stage a claimed digest is checked at ([`check_digest`]).
pub const STAGE_DIGEST: &str = "digest";

/// A record refused at one stage.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[error("[{stage}] {detail}")]
pub struct StageError {
    /// One of the `STAGE_*` names.
    pub stage: &'static str,
    pub detail: String,
    /// At the schema stage, every failed leaf of the validation
    /// (`/location: why`); `detail` is the first.
    pub causes: Vec<String>,
}

fn stage_err(stage: &'static str, detail: String) -> Error {
    Error::Stage(StageError {
        stage,
        detail,
        causes: Vec::new(),
    })
}

/// Why the module refused or could not make something.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// A record refused at one stage.
    #[error(transparent)]
    Stage(#[from] StageError),
    /// The record is not JSON text.
    #[error("not JSON: {0}")]
    Json(#[from] serde_json::Error),
    /// A value that cannot be canonicalized (outside a record check).
    #[error(transparent)]
    Jcs(#[from] JcsError),
    /// An input the profile does not allow, such as a report audience that
    /// is not a copy audience or a step with no developer name.
    #[error("{0}")]
    Invalid(String),
    /// A deal whose agreeing step rests on a check with no amount, or a
    /// currency with no ISO 4217 minor units: SH has no entry for it, so the
    /// card cannot be built ([`standard_history_deal`](super::standard_history_deal)).
    #[error("{0}")]
    Unpriced(String),
}

impl Error {
    /// The stage refusal, when this is one.
    pub fn stage(&self) -> Option<&StageError> {
        match self {
            Error::Stage(se) => Some(se),
            _ => None,
        }
    }
}

/// A record's payload, the JCS bytes a step seals, and its digest, once the
/// record passes [`check`]. `record` is the record's JSON text, read as
/// written. `local_values` are the raw local-store values the record must
/// not carry (every identifier seen in the deal, and every committed text
/// long enough not to collide with a token); they are used only for that
/// scan and never sealed.
pub fn encode(record: &[u8], local_values: &[&str]) -> Result<(Vec<u8>, String), Error> {
    let doc = decode(record)?;
    check_doc(&doc, local_values)?;
    let payload = jcs(&doc)?;
    let digest = json_digest(&doc)?;
    Ok((payload, digest))
}

/// Runs the record-level stages over `record` (canonicalization,
/// personal_data, wording, schema) and returns the first refusal as
/// [`Error::Stage`].
pub fn check(record: &[u8], local_values: &[&str]) -> Result<(), Error> {
    check_doc(&decode(record)?, local_values)
}

/// A record's digest: lowercase hex SHA-256 over its JCS bytes
/// (PROFILE.md §5).
pub fn digest(record: &[u8]) -> Result<String, Error> {
    Ok(json_digest(&decode(record)?)?)
}

/// Refuses, at the digest stage, a claimed digest that is not the record's.
pub fn check_digest(record: &[u8], claimed: &str) -> Result<(), Error> {
    if digest(record)? != claimed {
        return Err(stage_err(
            STAGE_DIGEST,
            "claimed digest is not SHA-256 over the JCS bytes".into(),
        ));
    }
    Ok(())
}

/// Reads `record` as JSON. JSON text must be valid UTF-8 with no lone
/// surrogate escape, which a decoder would replace or refuse unseen; such
/// text is refused at the canonicalization stage. Numbers are kept as
/// written (serde_json's `arbitrary_precision`), as Go's decoder keeps them
/// (`json.Number`).
pub(crate) fn decode(record: &[u8]) -> Result<Value, Error> {
    check_text(record)?;
    Ok(serde_json::from_slice(record)?)
}

/// Refuses JSON text that is not valid UTF-8, or whose strings escape a lone
/// surrogate (RFC 8785 §3.2.2.2: every string is valid Unicode). A port of
/// Go's `checkText`, scan for scan.
fn check_text(raw: &[u8]) -> Result<(), Error> {
    if std::str::from_utf8(raw).is_err() {
        return Err(stage_err(
            STAGE_CANONICALIZATION,
            "not JCS-serializable: the JSON text is not valid UTF-8".into(),
        ));
    }
    let hex4 = |at: usize| -> Option<u16> {
        let s = std::str::from_utf8(raw.get(at..at + 4)?).ok()?;
        u16::from_str_radix(s, 16).ok()
    };
    let mut in_string = false;
    let mut i = 0;
    while i < raw.len() {
        let c = raw[i];
        if !in_string {
            in_string = c == b'"';
            i += 1;
            continue;
        }
        match c {
            b'"' => in_string = false,
            b'\\' => {
                if i + 1 < raw.len() && raw[i + 1] == b'u' && i + 5 < raw.len() {
                    if let Some(u) = hex4(i + 2).filter(|u| (0xD800..=0xDFFF).contains(u)) {
                        let low = u >= 0xDC00;
                        let paired = !low
                            && i + 11 < raw.len()
                            && raw[i + 6] == b'\\'
                            && raw[i + 7] == b'u'
                            && hex4(i + 8).is_some_and(|l| (0xDC00..=0xDFFF).contains(&l));
                        if !paired {
                            return Err(stage_err(
                                STAGE_CANONICALIZATION,
                                "not JCS-serializable: lone surrogate".into(),
                            ));
                        }
                        i += 12;
                        continue;
                    }
                    i += 6;
                    continue;
                }
                i += 1; // the escaped character
            }
            _ => {}
        }
        i += 1;
    }
    Ok(())
}

fn check_doc(doc: &Value, local_values: &[&str]) -> Result<(), Error> {
    stage_canonicalization(doc)?;
    stage_personal_data(doc, local_values)?;
    stage_wording(doc)?;
    stage_schema(doc)
}

pub(crate) fn is_typed(rec: &Map<String, Value>) -> bool {
    rec.get("type").is_some_and(Value::is_string) && !rec.contains_key(PROFILE)
}

/// Go's `%v` of a decoded JSON value, for the values a record names.
fn go_v(v: Option<&Value>) -> String {
    match v {
        None | Some(Value::Null) => "<nil>".into(),
        Some(Value::String(s)) => s.clone(),
        Some(other) => other.to_string(),
    }
}

/// Go's `%q` of a string (`strconv.Quote`), for the strings a refusal names.
pub(crate) fn go_q(s: &str) -> String {
    let mut out = String::from("\"");
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\t' => out.push_str("\\t"),
            '\r' => out.push_str("\\r"),
            '\u{7}' => out.push_str("\\a"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            '\u{b}' => out.push_str("\\v"),
            c if (c as u32) < 0x20 || c == '\u{7f}' => {
                out.push_str(&format!("\\x{:02x}", c as u32))
            }
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

fn stage_canonicalization(doc: &Value) -> Result<(), Error> {
    let rec = doc.as_object();
    let blk = match rec {
        Some(r) if is_typed(r) => Some(r),
        Some(r) => r.get(PROFILE).and_then(Value::as_object),
        None => None,
    };
    let Some(blk) = blk else {
        return Err(stage_err(
            STAGE_CANONICALIZATION,
            "no x-deal-v0 block".into(),
        ));
    };
    let c = blk.get("canonicalization");
    if c.and_then(Value::as_str) != Some(CANONICALIZATION) {
        return Err(stage_err(
            STAGE_CANONICALIZATION,
            format!(
                "canonicalization must be exactly \"jcs\" (got {}); fail closed",
                go_v(c)
            ),
        ));
    }
    // A float is refused by name first, as the reference checker words it;
    // JCS would refuse it too, less plainly.
    no_floats(doc, "$")?;
    match jcs(doc) {
        Ok(_) => Ok(()),
        // Go's canonical.JCS words an unsafe integer so. An integer beyond
        // the 64-bit range, which `no_floats` has let through, is one too.
        Err(JcsError::UnsafeInteger(_) | JcsError::FloatInDigest) => Err(stage_err(
            STAGE_CANONICALIZATION,
            format!("not JCS-serializable: unsafe integer at : outside ±{MAX_SAFE_INTEGER} (§5.1)"),
        )),
        Err(e) => Err(stage_err(
            STAGE_CANONICALIZATION,
            format!("not JCS-serializable: {e}"),
        )),
    }
}

fn sorted_keys(m: &Map<String, Value>) -> Vec<&String> {
    let mut keys: Vec<&String> = m.keys().collect();
    keys.sort();
    keys
}

// A number written with a fraction or an exponent is a float, as Go's
// `noFloats` reads `json.Number`; numbers are kept as written.
fn no_floats(v: &Value, path: &str) -> Result<(), Error> {
    match v {
        Value::Number(n) if n.to_string().contains(['.', 'e', 'E']) => Err(stage_err(
            STAGE_CANONICALIZATION,
            format!("floating-point value at {path}; money is integer minor units"),
        )),
        Value::Object(m) => {
            for k in sorted_keys(m) {
                no_floats(&m[k], &format!("{path}.{k}"))?;
            }
            Ok(())
        }
        Value::Array(a) => {
            for (i, e) in a.iter().enumerate() {
                no_floats(e, &format!("{path}[{i}]"))?;
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

/// Every string a record carries, a value or a member name, and where.
fn record_strings<'a>(v: &'a Value, path: &str, out: &mut Vec<(String, &'a str)>) {
    match v {
        Value::String(s) => out.push((path.to_string(), s)),
        Value::Object(m) => {
            for k in sorted_keys(m) {
                out.push((format!("{path}.<key>"), k));
                record_strings(&m[k], &format!("{path}.{k}"), out);
            }
        }
        Value::Array(a) => {
            for (i, e) in a.iter().enumerate() {
                record_strings(e, &format!("{path}[{i}]"), out);
            }
        }
        _ => {}
    }
}

struct Scans {
    exempt: Regex,
    phone: Regex,
    email: Regex,
}

// The scans read digits and white space as the reference checker's Python
// patterns do, in Unicode: \d is any decimal digit (Nd), and \s any white
// space. Python's $ also matches before one final newline.
fn scans() -> &'static Scans {
    const D: &str = r"\p{Nd}";
    const S: &str = r"\t\n\x0B\x0C\r \x1C-\x1F\x{85}\p{Z}";
    static P: OnceLock<Scans> = OnceLock::new();
    P.get_or_init(|| Scans {
        exempt: Regex::new(&format!(
            r"^([0-9a-f]{{16,}}|(deal|sale)-[0-9a-f]+|{D}{{4}}-{D}{{2}}-{D}{{2}}(T{D}{{2}}:{D}{{2}}:{D}{{2}}Z)?|[a-z0-9-]+/[a-z0-9-]+/{D}+\.{D}+\.{D}+)\n?$"
        ))
        .expect("pattern"),
        phone: Regex::new(&format!(r"\+?\(?{D}[{D}{S}().-]{{6,}}{D}")).expect("pattern"),
        email: Regex::new(&format!(r"[^{S}@]+@[^{S}@]+\.[^{S}@]+")).expect("pattern"),
    })
}

/// Labels or grades of a counterparty; a record never carries them.
const BANNED_WORDS: &[&str] = &[
    "scam",
    "scams",
    "scammer",
    "scammers",
    "scammy",
    "fraud",
    "frauds",
    "fraudster",
    "fraudulent",
    "score",
    "scores",
    "scored",
    "scoring",
    "rating",
    "ratings",
    "reputation",
    "reputational",
    "blacklist",
    "blacklisted",
    "blocklist",
    "trustworthiness",
];

/// Refuses a raw phone number, email address or local-store value anywhere
/// in the record, a member name included.
fn stage_personal_data(doc: &Value, local_values: &[&str]) -> Result<(), Error> {
    let p = scans();
    let mut strs = Vec::new();
    record_strings(doc, "$", &mut strs);
    let folded: Vec<String> = local_values
        .iter()
        .filter(|raw| !raw.is_empty())
        .map(|raw| fold(raw))
        .collect();
    for (path, s) in strs {
        if p.exempt.is_match(s) {
            continue;
        }
        for m in p.phone.find_iter(s) {
            if m.as_str().chars().filter(|c| is_nd(*c)).count() >= 7 {
                return Err(stage_err(
                    STAGE_PERSONAL_DATA,
                    format!("raw phone number at {path}"),
                ));
            }
        }
        if p.email.is_match(s) {
            return Err(stage_err(
                STAGE_PERSONAL_DATA,
                format!("raw email address at {path}"),
            ));
        }
        let low = fold(s);
        if folded.iter().any(|raw| low.contains(raw.as_str())) {
            return Err(stage_err(
                STAGE_PERSONAL_DATA,
                format!("raw local-store identifier at {path}"),
            ));
        }
    }
    Ok(())
}

fn is_nd(c: char) -> bool {
    use icu_properties::props::GeneralCategory;
    icu_properties::CodePointMapData::<GeneralCategory>::new().get(c)
        == GeneralCategory::DecimalNumber
}

fn stage_wording(doc: &Value) -> Result<(), Error> {
    let mut strs = Vec::new();
    record_strings(doc, "$", &mut strs);
    for (path, s) in strs {
        let low = go_lower(s);
        for tok in low.split(|c: char| !c.is_ascii_lowercase()) {
            if BANNED_WORDS.contains(&tok) {
                return Err(stage_err(
                    STAGE_WORDING,
                    format!("prohibited word {} at {path}", go_q(tok)),
                ));
            }
        }
    }
    Ok(())
}

fn stage_schema(doc: &Value) -> Result<(), Error> {
    let typed = match doc.as_object() {
        Some(rec) if is_typed(rec) => {
            let t = rec["type"].as_str().unwrap_or_default();
            if !super::schema::is_typed_action_type(t) {
                return Err(stage_err(
                    STAGE_SCHEMA,
                    format!("type: {} is not a typed action record", go_q(t)),
                ));
            }
            Some(t)
        }
        _ => None,
    };
    match validate(doc, typed)? {
        None => Ok(()),
        Some(leaves) => Err(Error::Stage(StageError {
            stage: STAGE_SCHEMA,
            detail: leaves[0].clone(),
            causes: leaves,
        })),
    }
}
