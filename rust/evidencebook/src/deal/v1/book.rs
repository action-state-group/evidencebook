// SPDX-License-Identifier: Apache-2.0

//! A book pair (design §8.3; Go `receive.go`'s `BookPair` and `VerifyBook`):
//! a deal-book or case-book record travels as its Capsule with its
//! signature and key_id, the record header the Capsule commits as its
//! agent_input_digest, and the x-deal-v1 record the header commits as its
//! one payload.

use super::exact::{exact_object, go_name_matches, BOOK_PAIR, HEADER};
use serde_json::{json, Map, Value};
use sha2::{Digest as _, Sha256};

use super::check::{check, check_text, digest, placement_of, stage_err, Placement};
use super::record::{decode, Record};
use super::sent::{go_marshal, verify_capsule};
use crate::canonical::jcs;
use crate::deal::Error;

/// The stage a book pair is read at on its own ([`verify_book`]).
pub const STAGE_BOOK: &str = "book";

/// The top-level Capsule member a caller-sealed record carries its header
/// in; a book pair's Capsule commits its header instead (the shelf's
/// `RECORD_HEADER_MEMBER`).
const INLINE_HEADER: &str = "evidence_record_header";

fn bad(m: impl Into<String>) -> Error {
    stage_err(STAGE_BOOK, m.into())
}

/// What a verified book pair shows (Go `BookRead`): the record, its record
/// digest, the Capsule id (the record's entry in its book log), the key
/// that signed it, and its place in its book: the header's book log id,
/// its seq, and the record id it cites before it (`""` at seq 1).
#[derive(Clone, Debug, PartialEq)]
pub struct BookRead {
    pub record: Record,
    pub digest: String,
    pub record_id: String,
    pub key_id: String,
    pub log_id: String,
    pub seq: u64,
    pub prev: String,
}

/// A book record as it is sent (Go `BookPair`): `header`, `capsule` and
/// `envelope` as its book sealed them (the shelf's `StoredRecord`), and
/// `payload` the x-deal-v1 record's JCS bytes, its one payload. The bytes
/// are Go's: members in name order, as Go marshals a map, escaped as Go
/// escapes them.
pub fn book_pair(
    header: &[u8],
    capsule: &[u8],
    envelope: &[u8],
    payload: &[u8],
) -> Result<Vec<u8>, Error> {
    let mut members: Map<String, Value> = match serde_json::from_slice(capsule)? {
        Value::Object(m) => m,
        _ => {
            return Err(Error::Invalid(
                "xdealv1: the Capsule is not a JSON object".into(),
            ))
        }
    };
    let id = members
        .get("capsule_id")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();
    let verified = agent_action_capsule::envelope::verify(&id, envelope);
    if !verified.ok {
        return Err(Error::Invalid(
            "xdealv1: the envelope does not verify".into(),
        ));
    }
    members.insert("signature".into(), json!(hex::encode(envelope)));
    members.insert("key_id".into(), json!(hex::encode(&verified.public_key)));
    let mut sorted: Vec<(String, Value)> = members.into_iter().collect();
    sorted.sort_by(|a, b| a.0.cmp(&b.0));
    let record: Map<String, Value> = sorted.into_iter().collect();
    let mut out = Map::new();
    out.insert("header".into(), serde_json::from_slice(header)?);
    out.insert("payload".into(), serde_json::from_slice(payload)?);
    out.insert("record".into(), Value::Object(record));
    Ok(go_marshal(&Value::Object(out))?)
}

/// Checks a book pair on its own (Go `VerifyBook`): the Capsule verifies
/// under its key_id and commits the header as its agent_input_digest; the
/// header commits the record's digest as its one payload and names its book
/// log, seq and, past seq 1, exactly one record it cites; and the record
/// passes [`check`], is sealed in a deal book or a case book, and is at the
/// header's seq.
pub fn verify_book(pair: &[u8]) -> Result<BookRead, Error> {
    check_text(pair)?;
    let p: Value = serde_json::from_slice(pair)
        .map_err(|e| bad(format!("not a book pair {{record, header, payload}}: {e}")))?;
    exact_object(&p, &BOOK_PAIR, false, "the pair")
        .map_err(|e| bad(format!("not a book pair {{record, header, payload}}: {e}")))?;
    let shape = p.as_object().filter(|o| {
        o.get("record").is_some_and(Value::is_object)
            && o.contains_key("header")
            && o.contains_key("payload")
    });
    let Some(o) = shape else {
        return Err(bad("not a book pair {record, header, payload}"));
    };
    let record = o["record"].as_object().cloned().unwrap_or_default();
    if record.contains_key(INLINE_HEADER) {
        return Err(bad(
            "the Capsule carries its header inline; a book record commits it",
        ));
    }
    let (capsule, _, record_id, key_id) = verify_capsule(STAGE_BOOK, &record)?;
    let header_bytes = jcs(&o["header"]).map_err(|e| bad(format!("the header: {e}")))?;
    let committed = capsule
        .get("model_attestation") // vocabulary-gate: AAC capsule member
        .and_then(|a| a.pointer("/compute_attestation/agent_input_digest"))
        .and_then(Value::as_str);
    if committed != Some(hex::encode(Sha256::digest(&header_bytes)).as_str()) {
        return Err(bad("the Capsule does not commit this header"));
    }
    // Go reads the header's JCS into its typed Header: decoded the same
    // way here, from the same bytes, so a member the types refuse refuses
    // the pair and a member Go reads in another case is read.
    let header: Value = serde_json::from_slice(&header_bytes)?;
    exact_object(&header, &HEADER, false, "the header").map_err(bad)?;
    let h = go_header(&header).map_err(|e| bad(format!("the header: {e}")))?;
    let payload_text = serde_json::to_vec(&o["payload"])?;
    let d = digest(&payload_text)?;
    // One payload, this record: a header committing two would let one
    // record id be read as either.
    if h.payload_commitments != [d.as_str()] {
        return Err(bad(
            "the header does not commit this record as its one payload",
        ));
    }
    check(&payload_text)?;
    let r = decode(&payload_text)?;
    if placement_of(&r) == Placement::Journal {
        return Err(bad(format!(
            "a {} is sealed in the journal, not sent from a book",
            r.block.record_type
        )));
    }
    let (log_id, seq) = (h.book_id, h.seq);
    if log_id.is_empty() || seq == 0 || u64::try_from(r.block.seq).ok() != Some(seq) {
        return Err(bad(format!(
            "the record's seq {} is not its header's {seq}",
            r.block.seq
        )));
    }
    let cites: Vec<&str> = h
        .links
        .iter()
        .filter(|(t, _)| t == "cites")
        .map(|(_, target)| target.as_str())
        .collect();
    let prev = match (seq, cites.as_slice()) {
        (1, []) => String::new(),
        (1, _) => return Err(bad("the first record of a book cites none before it")),
        (_, [one]) => one.to_string(),
        _ => return Err(bad("a book record cites exactly the record before it")),
    };
    Ok(BookRead {
        record: r,
        digest: d,
        record_id,
        key_id,
        log_id,
        seq,
        prev,
    })
}

/// What [`verify_book`] reads of a header, as Go's `json.Unmarshal` into
/// `evidencebook.Header` gives it.
struct GoHeader {
    book_id: String,
    seq: u64,
    payload_commitments: Vec<String>,
    /// Each link's type and target.
    links: Vec<(String, String)>,
}

/// `v` decoded into Go's type, as `json.Unmarshal` does: `null` leaves the
/// zero value, a value of another JSON type is an error.
fn go_string(v: &Value) -> Result<Option<String>, String> {
    match v {
        Value::Null => Ok(None),
        Value::String(s) => Ok(Some(s.clone())),
        _ => Err("a string member is not a string".into()),
    }
}

fn go_int(v: &Value, unsigned: bool) -> Result<(), String> {
    let ok = match v {
        Value::Null => true,
        Value::Number(n) if unsigned => n.as_u64().is_some(),
        Value::Number(n) => n.as_i64().is_some(),
        _ => false,
    };
    if ok {
        Ok(())
    } else {
        Err("an integer member is not an integer of its type".into())
    }
}

/// An object of string members, as Go decodes a struct of strings (or a
/// map of strings): `null` is none, and a member's `null` is `None`, which
/// leaves the field it names as it was.
fn go_strings_object(
    v: &Value,
    fields: Option<&[&str]>,
) -> Result<Vec<(String, Option<String>)>, String> {
    match v {
        Value::Null => Ok(Vec::new()),
        Value::Object(o) => {
            let mut out = Vec::new();
            for (k, item) in o {
                let known = fields.is_none_or(|f| f.iter().any(|f| go_name_matches(k, f)));
                if known {
                    out.push((k.clone(), go_string(item)?));
                }
            }
            Ok(out)
        }
        _ => Err("an object member is not an object".into()),
    }
}

/// The header as Go's `json.Unmarshal(hjcs, &evidencebook.Header{})` reads
/// it: every member Go's `Header` types is checked against its Go type, a
/// member of no field is ignored, names match as Go matches them, a member
/// given twice under two spellings is read in the JCS order the bytes give,
/// the later one winning, and `null` leaves a scalar as it was (a list or
/// object it empties). Go decodes a second non-empty list into the slice the
/// first left, reusing its elements; that is refused here
/// (the `deal::v1` module docs).
fn go_header(header: &Value) -> Result<GoHeader, String> {
    let mut h = GoHeader {
        book_id: String::new(),
        seq: 0,
        payload_commitments: Vec::new(),
        links: Vec::new(),
    };
    let o = match header {
        Value::Null => return Ok(h),
        Value::Object(o) => o,
        _ => return Err("not an object".into()),
    };
    const STRINGS: &[&str] = &[
        "record_type",
        "epistemic_type",
        "committed_at",
        "event_time_claim",
        "commit_alg",
        "retention_state",
        "subject_ref",
        "principal_ref",
        "counterparty_ref",
    ];
    // Whether the last value a list field took was a non-empty list, whose
    // slice a later list would be decoded into.
    let (mut commitments_set, mut links_set) = (false, false);
    let reused = |set: &mut bool, v: &Value| -> Result<(), String> {
        let non_empty = v.as_array().is_some_and(|a| !a.is_empty());
        if *set && non_empty {
            return Err("a list member is given twice under two spellings".into());
        }
        *set = non_empty;
        Ok(())
    };
    for (k, v) in o {
        let is = |f: &str| go_name_matches(k, f);
        if is("v") {
            go_int(v, false)?;
        } else if is("book_id") {
            if let Some(s) = go_string(v)? {
                h.book_id = s;
            }
        } else if is("seq") {
            go_int(v, true)?;
            if let Some(n) = v.as_u64() {
                h.seq = n;
            }
        } else if STRINGS.iter().any(|f| is(f)) {
            go_string(v)?;
        } else if is("payload_commitments") {
            reused(&mut commitments_set, v)?;
            h.payload_commitments = match v {
                Value::Null => Vec::new(),
                Value::Array(a) => a
                    .iter()
                    .map(|x| go_string(x).map(Option::unwrap_or_default))
                    .collect::<Result<_, _>>()?,
                _ => return Err("payload_commitments is not a list".into()),
            };
        } else if is("field_commitments") {
            go_strings_object(v, None)?;
        } else if is("links") {
            reused(&mut links_set, v)?;
            h.links = match v {
                Value::Null => Vec::new(),
                Value::Array(a) => a
                    .iter()
                    .map(|l| {
                        let mut link = (String::new(), String::new());
                        for (name, value) in go_strings_object(l, Some(&["type", "target"]))? {
                            let Some(value) = value else { continue };
                            if go_name_matches(&name, "type") {
                                link.0 = value;
                            } else {
                                link.1 = value;
                            }
                        }
                        Ok::<_, String>(link)
                    })
                    .collect::<Result<_, _>>()?,
                _ => return Err("links is not a list".into()),
            };
        } else if is("correlation") {
            // Every member a string, as each of Go's three is; the names are
            // held to the schema's by `exact_object` before this reads.
            go_strings_object(v, None)?;
        } else if is("reproduction") {
            go_strings_object(v, Some(&["clock"]))?;
        }
        // `statement` is raw JSON in Go: any value.
    }
    Ok(h)
}

#[cfg(test)]
mod tests {
    use super::go_header;
    use serde_json::json;

    // A list given twice under two spellings, both non-empty, is refused:
    // Go would decode the second into the first's slice. One empty or
    // null, the later wins, as in Go.
    #[test]
    fn a_list_given_twice_is_refused_unless_one_is_empty() {
        let cite = json!([{"type": "cites", "target": "x"}]);
        let header = |links_upper: serde_json::Value, links: serde_json::Value| json!({"Links": links_upper, "book_id": "b", "links": links, "seq": 2});
        assert!(go_header(&header(cite.clone(), json!([null]))).is_err());
        assert!(go_header(&header(cite.clone(), cite.clone())).is_err());
        assert!(go_header(&header(cite.clone(), json!(null)))
            .unwrap()
            .links
            .is_empty());
        assert!(
            go_header(&header(json!([]), cite.clone()))
                .unwrap()
                .links
                .len()
                == 1
        );
        let h = go_header(&json!({"PAYLOAD_COMMITMENTS": ["a"], "payload_commitments": ["a"]}));
        assert!(h.is_err());
    }

    // null leaves a scalar as an earlier spelling set it.
    #[test]
    fn null_keeps_the_earlier_value() {
        let h = go_header(&json!({"Seq": 3, "book_id": "b", "links": [{"Type": "cites", "target": "x", "type": null}], "seq": null}))
            .unwrap();
        assert_eq!((h.seq, h.links[0].0.as_str()), (3, "cites"));
    }
}
