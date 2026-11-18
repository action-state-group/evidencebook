//! The x-deal-v1 fixtures (`dealprofile/x-deal-v1/fixtures`) through
//! `evidencebook::deal::v1`, as Go's `TestFixtures` runs them: each positive
//! decodes, encodes to the same bytes, has its expected digest and
//! placement and passes the record checks (and, for a journal record with a
//! seal context, the seal-time deal_id rule); each negative is refused by
//! its check at its stage, with its text past the schema stage.

#![cfg(feature = "deal")]

use std::fs;
use std::path::{Path, PathBuf};

use evidencebook::canonical::jcs;
use evidencebook::deal::v1;
use serde_json::Value;

fn fixtures() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../dealprofile/x-deal-v1/fixtures")
}

fn read(path: &Path) -> Value {
    let raw = fs::read(path).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    serde_json::from_slice(&raw).unwrap_or_else(|e| panic!("{}: {e}", path.display()))
}

fn json_files(dir: &Path, out: &mut Vec<PathBuf>) {
    for e in fs::read_dir(dir).unwrap() {
        let p = e.unwrap().path();
        if p.is_dir() {
            json_files(&p, out);
        } else if p.extension().is_some_and(|x| x == "json") {
            out.push(p);
        }
    }
    out.sort();
}

fn record_of(rel: &str) -> v1::Record {
    let f = read(&fixtures().join(rel));
    v1::decode(&serde_json::to_vec(&f["record"]).unwrap()).unwrap_or_else(|e| panic!("{rel}: {e}"))
}

#[test]
fn positives_pass_and_keep_their_bytes() {
    let mut files = Vec::new();
    json_files(&fixtures(), &mut files);
    let mut n = 0;
    for path in files {
        let rel = path
            .strip_prefix(fixtures())
            .unwrap()
            .to_string_lossy()
            .to_string();
        if rel.starts_with("negative/") || !rel.contains('/') {
            continue;
        }
        let f = read(&path);
        if f.get("expect").and_then(Value::as_str) != Some("valid") {
            continue;
        }
        n += 1;
        let text = serde_json::to_vec(&f["record"]).unwrap();
        let r = v1::decode(&text).unwrap_or_else(|e| panic!("{rel}: decode: {e}"));
        assert_eq!(
            jcs(&serde_json::to_value(&r).unwrap()).unwrap(),
            jcs(&f["record"]).unwrap(),
            "{rel}: encodes to the same bytes"
        );
        let (_, digest) = v1::encode(&text).unwrap_or_else(|e| panic!("{rel}: {e}"));
        assert_eq!(
            digest,
            f["expected_digest"].as_str().unwrap(),
            "{rel}: digest"
        );
        assert_eq!(
            v1::placement_of(&r).as_str(),
            f["placement"].as_str().unwrap(),
            "{rel}: placement"
        );
        if let Some(ctx) = f.get("seal_context") {
            let ctx: v1::SealContext = serde_json::from_value(ctx.clone()).unwrap();
            v1::check_sealed(&text, ctx).unwrap_or_else(|e| panic!("{rel}: sealed: {e}"));
        }
    }
    assert!(n >= 38, "every positive fixture is run ({n})");
}

#[test]
fn negatives_are_refused_at_their_stage() {
    let mut files = Vec::new();
    json_files(&fixtures().join("negative"), &mut files);
    assert!(!files.is_empty());
    for path in files {
        let f = read(&path);
        let name = f["fixture"].as_str().unwrap().to_string();
        let text: Vec<u8> = match f.get("record_text") {
            Some(t) => t.as_str().unwrap().as_bytes().to_vec(),
            None => serde_json::to_vec(&f["record"]).unwrap(),
        };
        let stage = f["expected_stage"].as_str().unwrap();
        let contains = f["expected_error_contains"].as_str().unwrap_or("");
        let err = match f["check"].as_str().unwrap() {
            "record" => match f.get("seal_context") {
                Some(ctx) => v1::check_sealed(&text, serde_json::from_value(ctx.clone()).unwrap()),
                None => v1::check(&text),
            },
            kind => {
                let r = v1::decode(&text).unwrap_or_else(|e| panic!("{name}: decode: {e}"));
                let with = record_of(f["with"].as_str().unwrap());
                match kind {
                    "agreement" => v1::check_agreement(
                        &r,
                        &with,
                        f.get("held_acceptor_agreement")
                            .and_then(Value::as_str)
                            .unwrap_or(""),
                    ),
                    "dispute" => v1::check_dispute(&r, &with),
                    "ruling" => v1::check_ruling(&r, &with),
                    other => panic!("{name}: unknown check {other}"),
                }
            }
        }
        .expect_err(&name);
        let se = err
            .stage()
            .unwrap_or_else(|| panic!("{name}: not a stage refusal: {err}"));
        assert_eq!(se.stage, stage, "{name}: stage ({err})");
        if stage != "canonicalization" && stage != "schema" {
            assert!(
                se.detail.contains(contains),
                "{name}: {:?} contains {contains:?}",
                se.detail
            );
        }
    }
}

#[test]
fn vendored_schema_is_the_profiles() {
    let go = fs::read(fixtures().join("../x-deal-v1.schema.json")).unwrap();
    assert_eq!(
        v1::profile_schema(),
        go.as_slice(),
        "x-deal-v1.schema.json is vendored byte for byte"
    );
}

/// deal-id-presence (`testdata/deal-v1/records/`): each record passes or is
/// refused at its stage, checked with its seal context when it has one.
#[test]
fn deal_id_presence_vectors() {
    let v = read(
        &Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../testdata/deal-v1/records/deal-id-presence.json"),
    );
    let cases = v["cases"].as_array().unwrap();
    assert!(!cases.is_empty());
    for c in cases {
        let name = c["name"].as_str().unwrap();
        let text = serde_json::to_vec(&c["record"]).unwrap();
        let got = match c.get("context") {
            Some(ctx) => v1::check_sealed(&text, serde_json::from_value(ctx.clone()).unwrap()),
            None => v1::check(&text),
        };
        if c["valid"].as_bool().unwrap() {
            got.unwrap_or_else(|e| panic!("{name}: {e}"));
        } else {
            let err = got.expect_err(name);
            assert_eq!(
                err.stage().map(|s| s.stage),
                c["stage"].as_str(),
                "{name}: {err}"
            );
        }
    }
}

/// A member named as serde_json's arbitrary_precision names its private
/// number would be read as a number, not as written: it is refused at the
/// canonicalization stage, so no two texts share one digest (Codex, #533).
#[test]
fn reserved_number_member_is_refused() {
    let f = read(&fixtures().join("appendix-a/buyer-journal/05-offer.json"));
    // serde_json itself would fold the member: the text is written by hand.
    let raw = serde_json::to_string(&f["record"]).unwrap().replacen(
        "\"owed\":{",
        "\"owed\":{\"detail\":{\"$serde_json::private::Number\":\"1\"},",
        1,
    );
    let err = v1::check(raw.as_bytes()).expect_err("refused");
    assert_eq!(
        err.stage().map(|s| s.stage),
        Some("canonicalization"),
        "{err}"
    );
    assert!(
        v1::digest(raw.as_bytes()).is_err(),
        "no digest for text not read as written"
    );
}

/// Go's zero values: a status time "" is no time, and books may leave out
/// the counterpart, the case book and proof_held.
#[test]
fn status_books_read_go_zero_values() {
    let v: Value = serde_json::from_slice(
        &fs::read(
            Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("../../testdata/deal-v1/status/vectors.json"),
        )
        .unwrap(),
    )
    .unwrap();
    let mut books = v["cases"][0]["books"].clone();
    let o = books.as_object_mut().unwrap();
    o.remove("proof_held");
    o.remove("case_book");
    let b: v1::StatusBooks = serde_json::from_value(books.clone()).expect("zero values");
    assert!(!b.proof_held);
    assert!(b.case_book.is_empty());
    books["own"][1]["time"] = Value::String(String::new());
    let b: v1::StatusBooks = serde_json::from_value(books).unwrap();
    assert_eq!(b.own[1].time, v1::StatusTime::None);
    v1::build_status(&b).expect("an untimed record, as Go reads it");
}
