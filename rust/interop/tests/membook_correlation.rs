// SPDX-License-Identifier: Apache-2.0
//! The interop crate's `MemBook::correlation` join, checked against the vector
//! the Go correlation index and the Rust and Python shelves also read
//! (testdata/shelf/correlation.json).

use evidencebook::ask::Entry;
use evidencebook::record::EpistemicType;
use evidencebook::store::RecordSource;
use evidencebook_interop::{seeded, Clock, MemBook};
use serde_json::Value;

#[test]
fn a_membook_correlation_subject_is_answered_as_go_answers_it() {
    let path = format!(
        "{}/../../testdata/shelf/correlation.json",
        env!("CARGO_MANIFEST_DIR")
    );
    let v: Value = serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
    let mut book = MemBook::open(
        "correlation",
        "operator",
        seeded("correlation-signer"),
        "correlation-log",
        seeded("correlation-log-key"),
        Clock::at(1_700_000_000),
    );
    for r in v["records"].as_array().unwrap() {
        let entry = Entry {
            record_type: "observation".into(),
            epistemic_type: EpistemicType::ObservedEvent,
            links: Vec::new(),
            statement: Value::Null,
        };
        book.append_with(&entry, |h| {
            h.subject_ref = Some(r["subject_ref"].as_str().unwrap().into());
            for (member, value) in r["correlation"].as_object().into_iter().flatten() {
                // The seal refuses an empty member, so a MemBook never holds
                // one: the vector's empty exchange_id is left unset here, and
                // the record is held under its subject_ref either way.
                let value = value.as_str().unwrap();
                if !value.is_empty() {
                    h.correlation.insert(member.clone(), value.into());
                }
            }
        })
        .unwrap();
    }
    let answers = v["answers"].as_object().unwrap();
    assert!(answers.len() > 1);
    for (id, want) in answers {
        let want: Vec<u64> = want
            .as_array()
            .unwrap()
            .iter()
            .map(|p| p.as_u64().unwrap())
            .collect();
        assert_eq!(book.correlation(id), want, "correlation subject {id:?}");
    }
}
