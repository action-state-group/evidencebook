//! The x-deal-v1 vectors under `testdata/deal-v1/` through
//! `evidencebook::deal::v1`, each read from the file alone, as Go's
//! `xdealv1` writes them.

#![cfg(feature = "deal")]

use std::fs;
use std::path::{Path, PathBuf};

use evidencebook::deal::v1;
use serde_json::Value;

fn vectors(rel: &str) -> Vec<Value> {
    let p: PathBuf = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../testdata/deal-v1").join(rel);
    let raw = fs::read(&p).unwrap_or_else(|e| panic!("{}: {e}", p.display()));
    let v: Value = serde_json::from_slice(&raw).unwrap();
    let cases = v["cases"].as_array().unwrap().clone();
    assert!(!cases.is_empty(), "{rel} has cases");
    cases
}

/// start/vectors.json: Start over each input gives its result.
#[test]
fn start_vectors() {
    for c in vectors("start/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let input: v1::StartInput = serde_json::from_value(c["input"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let got = v1::start(&input).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&got).unwrap(), c["expect"], "{name}");
    }
}

/// start/books.json: StartFromBooks over each case's books gives its input
/// and its result.
#[test]
fn start_book_vectors() {
    for c in vectors("start/books.json") {
        let name = c["name"].as_str().unwrap();
        let books: v1::StartBooks = serde_json::from_value(c["books"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let (input, got) = v1::start_from_books(&books).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&input).unwrap(), c["input"], "{name}: input");
        assert_eq!(serde_json::to_value(&got).unwrap(), c["expect"], "{name}: result");
    }
}

/// status/vectors.json: BuildStatus over each case's books gives its status
/// value, and rowstatus over each row resolved at `at` with `stamps` gives
/// its row result.
#[test]
fn status_vectors() {
    use std::collections::BTreeMap;
    for c in vectors("status/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let books: v1::StatusBooks = serde_json::from_value(c["books"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let got = v1::build_status(&books).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&got).unwrap(), c["status"], "{name}: status value");
        let at = c["at"].as_str().unwrap();
        let stamps: BTreeMap<u64, String> = match c.get("stamps") {
            Some(s) => serde_json::from_value(s.clone()).unwrap(),
            None => BTreeMap::new(),
        };
        for row in &got.rows {
            let input = v1::resolve_row(row, at, &stamps).unwrap_or_else(|e| panic!("{name}: {e}"));
            let res = evidencebook::row_status::compute(&input).unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(Some(res.status.as_str()), c["rows"][&row.id].as_str(), "{name}, row {}", row.id);
        }
    }
}
