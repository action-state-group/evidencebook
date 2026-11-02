//! The x-deal-v0 profile against Go's answers
//! (`dealprofile/testdata/parity-vectors.json`, written by Go's
//! `TestParityVectors`): identifier normalization and fingerprints, the
//! record-level stages, the `standard_history/1` deal entry and ISO 4217
//! minor units. The Python package reads the same file. The cards the file
//! also holds are derived by `standard_history::derive_card`
//! (`tests/standard_history.rs`).

#![cfg(feature = "deal")]

use std::fs;
use std::path::Path;

use evidencebook::canonical::jcs;
use evidencebook::deal::{self, HistoryInput, HistoryRecord, HistoryResult};
use serde_json::Value;

fn vectors() -> Value {
    let path = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../dealprofile/testdata/parity-vectors.json");
    serde_json::from_slice(&fs::read(&path).unwrap_or_else(|e| panic!("{}: {e}", path.display())))
        .unwrap()
}

fn list<'a>(v: &'a Value, key: &str) -> &'a Vec<Value> {
    v[key].as_array().unwrap_or_else(|| panic!("{key}"))
}

fn s<'a>(v: &'a Value, key: &str) -> &'a str {
    v[key].as_str().unwrap_or_else(|| panic!("{key} in {v}"))
}

#[test]
fn corpus_sizes() {
    let v = vectors();
    assert_eq!(
        (
            list(&v, "normalize").len(),
            list(&v, "check").len(),
            list(&v, "history").len()
        ),
        (135, 95, 77)
    );
}

#[test]
fn normalize_and_fingerprint() {
    let v = vectors();
    let secret = hex::decode(s(&v, "store_secret_hex")).unwrap();
    let key = deal::deal_key(&secret, s(&v, "deal_id"));
    let mut failures = Vec::new();
    for x in list(&v, "normalize") {
        let (kind, raw) = (s(x, "kind"), s(x, "raw"));
        let got = deal::normalize(kind, raw);
        if x["normalized"].is_null() {
            assert!(x["fp"].is_null());
            if got.is_ok() {
                failures.push(format!("{kind}:{raw:?} normalized to {got:?}"));
            }
            continue;
        }
        let fp = deal::fingerprint(&key, kind, raw);
        if got.as_deref().ok() != x["normalized"].as_str() || fp.as_deref().ok() != x["fp"].as_str()
        {
            failures.push(format!(
                "{kind}:{raw:?}: got {got:?}, want {}",
                x["normalized"]
            ));
        }
    }
    assert!(
        failures.is_empty(),
        "{} divergences:\n{}",
        failures.len(),
        failures.join("\n")
    );
}

#[test]
fn record_checks() {
    let v = vectors();
    let mut failures = Vec::new();
    for x in list(&v, "check") {
        let name = s(x, "name");
        let local: Vec<&str> = x["local_values"]
            .as_array()
            .map(|a| a.iter().map(|l| l.as_str().unwrap()).collect())
            .unwrap_or_default();
        let record = s(x, "record").as_bytes();
        let got = deal::encode(record, &local);
        let stage = x["stage"].as_str().unwrap_or("");
        let ok = match (stage, &got) {
            ("", Ok((payload, digest))) => {
                payload == s(x, "payload").as_bytes() && digest == s(x, "digest")
            }
            ("error", Err(e)) => e.stage().is_none(),
            (want, Err(e)) if !want.is_empty() && want != "error" => {
                e.stage().is_some_and(|se| se.stage == want)
            }
            _ => false,
        };
        if !ok {
            failures.push(format!(
                "{name}: want stage {stage:?}, got {:?}",
                got.map(|(_, d)| d)
            ));
        }
    }
    assert!(
        failures.is_empty(),
        "{} divergences:\n{}",
        failures.len(),
        failures.join("\n")
    );
}

fn history_input(h: &Value) -> HistoryInput {
    HistoryInput {
        records: h["records"]
            .as_array()
            .map(|a| {
                a.iter()
                    .map(|r| HistoryRecord {
                        payload: s(r, "payload").as_bytes().to_vec(),
                        committed_at: s(r, "committed_at").to_string(),
                    })
                    .collect()
            })
            .unwrap_or_default(),
        results: h["results"]
            .as_array()
            .map(|a| {
                a.iter()
                    .map(|r| HistoryResult {
                        result: s(r, "result").as_bytes().to_vec(),
                        verified: r["verified"].as_bool().unwrap(),
                    })
                    .collect()
            })
            .unwrap_or_default(),
        counterparty_half_verified: h["counterparty_half_verified"].as_bool().unwrap_or(false),
    }
}

#[test]
fn standard_history_entries() {
    let v = vectors();
    let mut failures = Vec::new();
    for h in list(&v, "history") {
        let name = s(h, "name");
        let got = deal::standard_history_deal(&history_input(h));
        match (h["error"].as_str(), got) {
            (Some("unpriced"), Err(deal::Error::Unpriced(m))) => {
                if !(m.starts_with("deal deal-")
                    && m.contains(": dealprofile: a deal has no recorded amount"))
                {
                    failures.push(format!("{name}: unpriced text {m:?}"));
                }
            }
            (Some("other"), Err(e)) if !matches!(e, deal::Error::Unpriced(_)) => {}
            (None, Ok(entry)) => {
                let want = &h["entry"];
                let same = match &entry {
                    None => want.is_null(),
                    // The same bytes as Go writes the entry, and its members in Go's order.
                    Some(e) => {
                        jcs(e).unwrap() == jcs(want).unwrap()
                            && e.as_object()
                                .unwrap()
                                .keys()
                                .eq(want.as_object().unwrap().keys())
                    }
                };
                if !same {
                    failures.push(format!("{name}: got {entry:?}, want {want}"));
                }
            }
            (want, got) => failures.push(format!("{name}: want error {want:?}, got {got:?}")),
        }
    }
    assert!(
        failures.is_empty(),
        "{} divergences:\n{}",
        failures.len(),
        failures.join("\n")
    );
}

#[test]
fn minor_units_are_gos() {
    let v = vectors();
    for (code, want) in v["minor_units"].as_object().unwrap() {
        assert_eq!(
            deal::minor_units(code).map(i64::from),
            want.as_i64(),
            "{code:?}"
        );
    }
}

#[test]
fn non_ascii_alabels_are_read_as_go_reads_them() {
    let n = |s: &str| deal::normalize("domain", s);
    // UTS46 maps an ideographic full stop before it splits the labels.
    assert_eq!(n("xn--zcaé-。com").unwrap(), "zcaé.com");
    // The whole name is mapped before an xn-- label is decoded (Go, Python).
    assert_eq!(n("xn--ﬁcaé-.com").unwrap(), "ficaé.com");
    assert_eq!(n("xn--µcaé-.com").unwrap(), "μcaé.com");
    // A right-to-left label, whose Punycode body ends in a hyphen.
    assert_eq!(n("xn--שלום-.com").unwrap(), "שלום.com");
    assert_eq!(n("xn--שלום-.co.il").unwrap(), "שלום.co.il");
}

#[test]
fn a_null_record_is_no_step_and_invalid_utf8_is_replaced() {
    let at = "2026-10-06T09:00:00Z".to_string();
    let input = |payload: &[u8]| deal::HistoryInput {
        records: vec![deal::HistoryRecord {
            payload: payload.to_vec(),
            committed_at: at.clone(),
        }],
        ..Default::default()
    };
    assert!(deal::standard_history_deal(&input(b"null"))
        .unwrap()
        .is_none());
    // Not an object and not a step in Go either way; invalid bytes in a string are U+FFFD.
    assert!(deal::standard_history_deal(&input(b"{\"a\":\"\xff\"}"))
        .unwrap()
        .is_none());
    assert!(deal::standard_history_deal(&input(b"[]")).is_err());
}
