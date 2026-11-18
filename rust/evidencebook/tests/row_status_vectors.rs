// SPDX-License-Identifier: Apache-2.0

//! The shared row status vectors (testdata/row-status), as Go's rowstatus
//! test and Python's read them.

use evidencebook::row_status::{compute, parse_input, Outcome};
use serde_json::Value;

fn vectors() -> Value {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../testdata/row-status/vectors.json"
    );
    serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
}

#[test]
fn cases() {
    let v = vectors();
    let cases = v["cases"].as_array().unwrap();
    assert!(!cases.is_empty());
    for c in cases {
        let name = c["name"].as_str().unwrap();
        let input = parse_input(c["input"].to_string().as_bytes())
            .unwrap_or_else(|e| panic!("{name}: {e}"));
        let got = compute(&input).unwrap_or_else(|e| panic!("{name}: {e}"));
        let want: Outcome = serde_json::from_value(c["expect"].clone()).unwrap();
        assert_eq!(got, want, "{name}");
    }
}

#[test]
fn invalid() {
    let v = vectors();
    let cases = v["invalid"].as_array().unwrap();
    assert!(!cases.is_empty());
    for c in cases {
        let name = c["name"].as_str().unwrap();
        let refused = match parse_input(c["input"].to_string().as_bytes()) {
            Err(_) => true,
            Ok(input) => compute(&input).is_err(),
        };
        assert!(refused, "{name} was accepted");
    }
}

/// The late statement and late dispute vectors (testdata/row-status/late.json,
/// evidencebook#461 Q9): compute_late gives exactly expect.
#[test]
fn late() {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../testdata/row-status/late.json"
    );
    let v: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let cases = v["cases"].as_array().unwrap();
    assert!(!cases.is_empty());
    for c in cases {
        let name = c["name"].as_str().unwrap();
        let input = parse_input(c["input"].to_string().as_bytes())
            .unwrap_or_else(|e| panic!("{name}: {e}"));
        let got = evidencebook::row_status::compute_late(&input)
            .unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&got).unwrap(), c["expect"], "{name}");
    }
}
