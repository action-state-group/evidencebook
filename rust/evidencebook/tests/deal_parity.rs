//! The x-deal-v0 profile against Go's answers
//! (`dealprofile/testdata/parity-vectors.json`, written by Go's
//! `TestParityVectors`): identifier normalization and fingerprints, the
//! record-level stages, the `standard_history/1` deal entry and ISO 4217
//! minor units. The Python package reads the same file. The cards the file
//! also holds are derived by agent-action-capsule's `derive_card`, which the
//! crate does not carry, so they are checked by Go and Python only.

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
        (126, 95, 72)
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
fn cherokee_fold_is_casefolding_txt_not_gos() {
    // A recorded divergence (evidencebook#283): Go's `cases.Fold` swaps
    // Cherokee case (capitals to small forms, small forms to capitals) where
    // CaseFolding.txt folds both to the capitals, which is what ICU4X (and so
    // this crate) does. The vectors do not pin it; a name with Cherokee letters
    // fingerprints differently in Rust until #283 settles which fold the
    // profile means. This test fails, and is to be updated, when it does.
    let name = |s: &str| deal::normalize("name", s).unwrap();
    assert_eq!(name("Ꭰ"), "Ꭰ");
    assert_eq!(name("ꭰ"), "Ꭰ");
}
