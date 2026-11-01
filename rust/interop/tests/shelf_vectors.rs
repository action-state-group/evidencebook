// SPDX-License-Identifier: Apache-2.0
//! The scripted shelf (testdata/shelf/script.json), run by the Rust shelf:
//! each phase's dump equals the Go run's vector byte for byte
//! (`after-write.json`, `after-continue.json`). CI's `shelf-interlock` job
//! runs the same phases in separate processes, Rust continuing Go's file and
//! Go continuing Rust's.

use evidencebook_interop::shelf;

fn vector(name: &str) -> String {
    let path = format!("{}/../../testdata/shelf/{name}", env!("CARGO_MANIFEST_DIR"));
    std::fs::read_to_string(path).unwrap()
}

#[test]
fn the_rust_shelf_writes_and_continues_the_vectors_byte_for_byte() {
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
