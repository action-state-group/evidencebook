// SPDX-License-Identifier: Apache-2.0
//! The scripted shelf (testdata/shelf/script.json), run by the Rust shelf:
//! each phase's dump equals the Go run's vector byte for byte
//! (`after-write.json`, `after-continue.json`, `after-migrate.json`,
//! `after-record-time.json`). CI's `shelf-interlock` job
//! runs the same phases in separate processes, Rust continuing Go's file and
//! Go continuing Rust's.

use evidencebook_interop::shelf;

fn vector(name: &str) -> String {
    let path = format!("{}/../../testdata/shelf/{name}", env!("CARGO_MANIFEST_DIR"));
    std::fs::read_to_string(path).unwrap()
}

#[test]
fn the_rust_shelf_writes_continues_migrates_and_records_time_byte_for_byte() {
    let dir = tempfile::tempdir().unwrap();
    shelf::write(dir.path()).unwrap();
    assert!(
        shelf::dump(dir.path()).unwrap() == vector("after-write.json"),
        "after-write.json differs"
    );
    shelf::continue_(dir.path()).unwrap();
    assert!(
        shelf::dump(dir.path()).unwrap() == vector("after-continue.json"),
        "after-continue.json differs"
    );
    shelf::migrate(dir.path()).unwrap();
    assert!(
        shelf::dump(dir.path()).unwrap() == vector("after-migrate.json"),
        "after-migrate.json differs"
    );
    // The record time phase (evidencebook#461 §2.6): every step's expect
    // matches, then the dump.
    shelf::record_time(dir.path()).unwrap();
    assert!(
        shelf::dump(dir.path()).unwrap() == vector("after-record-time.json"),
        "after-record-time.json differs"
    );
    assert!(
        shelf::write(dir.path()).is_err(),
        "the writer phase needs a new file"
    );
}

#[test]
fn the_minted_receipts_are_the_vectors() {
    let dump: serde_json::Value = serde_json::from_str(&vector("after-continue.json")).unwrap();
    for tick in dump["cadence_ticks"].as_array().unwrap() {
        let Some(stored) = tick["receipt"].as_str() else {
            continue;
        };
        use base64::Engine as _;
        let statement = base64::engine::general_purpose::URL_SAFE_NO_PAD
            .decode(tick["checkpoint"].as_str().unwrap())
            .unwrap();
        assert_eq!(shelf::mint_receipt(&statement).unwrap().to_json(), stored);
    }
}

#[test]
fn a_correlation_subject_is_answered_as_go_answers_it() {
    use evidencebook::record::EpistemicType;
    use evidencebook::shelf::BookEntry;
    use evidencebook::store::RecordSource;
    let v: serde_json::Value = serde_json::from_str(&vector("correlation.json")).unwrap();
    let dir = tempfile::tempdir().unwrap();
    let env = shelf::open(
        &shelf::file(dir.path()),
        "correlation",
        shelf::start(),
        None,
    )
    .unwrap();
    let book = env.shelf.open("deal/a").unwrap();
    for r in v["records"].as_array().unwrap() {
        let mut e = BookEntry::new("observation", EpistemicType::ObservedEvent);
        e.subject_ref = Some(r["subject_ref"].as_str().unwrap().into());
        for (member, value) in r["correlation"].as_object().into_iter().flatten() {
            e.correlation
                .insert(member.clone(), value.as_str().unwrap().into());
        }
        book.append(&e).unwrap();
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
    env.release().unwrap();
}
