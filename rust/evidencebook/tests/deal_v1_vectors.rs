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

/// report/vectors.json: BuildReport over each case's books and copy gives
/// its report, and its payload is the JCS of that report.
#[test]
fn report_vectors() {
    for c in vectors("report/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let books: v1::ReportBooks = serde_json::from_value(c["books"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let cp = &c["copy"];
        let copy = v1::ReportCopy {
            audience: cp["audience"].as_str().unwrap().into(),
            nonce: cp["nonce"].as_str().unwrap().into(),
            referee: cp.get("referee").and_then(Value::as_str).unwrap_or("").into(),
            as_of: cp["as_of"].as_str().unwrap().into(),
            text: cp.get("report").and_then(Value::as_object).cloned(),
        };
        let got = v1::build_report(&books, &copy).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&got).unwrap(), c["report"], "{name}");
        let payload = v1::report_payload(&got).unwrap();
        assert_eq!(payload, evidencebook::canonical::jcs(&c["report"]).unwrap(), "{name}: payload");
    }
}

/// settled/vectors.json: each case's rows settle as given.
#[test]
fn settled_vectors() {
    for c in vectors("settled/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let rows: Vec<v1::ReportRow> = c["rows"]
            .as_array()
            .unwrap()
            .iter()
            .map(|s| {
                let s = s.as_str().unwrap();
                let (s, made) = match s.strip_suffix("+made") {
                    Some(rest) => (rest, true),
                    None => (s, false),
                };
                let (status, extra) = s.split_once(':').unwrap_or((s, ""));
                v1::ReportRow {
                    id: String::new(),
                    owed_by: String::new(),
                    owed: Value::Null,
                    due: String::new(),
                    window_end: String::new(),
                    status: status.into(),
                    value: (status != "ruled" && !extra.is_empty()).then(|| extra.into()),
                    remedy: (status == "ruled" && !extra.is_empty()).then(|| extra.into()),
                    remedy_made: made,
                    local: false,
                    check: String::new(),
                    late: Default::default(),
                    ruling: None,
                    relied_on: Vec::new(),
                }
            })
            .collect();
        let start = v1::StartResult {
            state: if c["started"].as_bool().unwrap() { v1::STATE_STARTED } else { v1::STATE_NOT_STARTED }.into(),
            local: false,
            by: None,
        };
        assert_eq!(v1::settled(&start, &rows), c["settled"].as_bool().unwrap(), "{name}");
    }
}
