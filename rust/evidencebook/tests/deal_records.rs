//! The x-deal-v0 record types (`evidencebook::deal::{Record, Body}` and
//! the typed record types) against every record the profile's fixtures and
//! the deal plugin's frozen baseline hold, as Go's `dealprofile` is checked
//! (`record_test.go`): each decodes with no member left over and encodes to
//! the same bytes.

#![cfg(feature = "deal")]

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use evidencebook::canonical::jcs;
use evidencebook::deal::{self, Block, Body, CloseBody, OutcomeBody, Record, SaleCloseBody};
use serde_json::Value;

fn repo() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../..")
}

fn fixtures_dir() -> PathBuf {
    repo().join("dealprofile/x-deal-v0/fixtures")
}

fn read(path: &Path) -> Value {
    let raw = fs::read(path).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    serde_json::from_slice(&raw).unwrap_or_else(|e| panic!("{}: {e}", path.display()))
}

/// Valid samples of the types no fixture or vector holds, each with its
/// optional and nullable members both present and absent (the same samples
/// as Go's `handBuilt`).
const HAND_BUILT: &[(&str, &str)] = &[
    (
        "action-report minimal",
        r#"{"type":"action-report/v0","canonicalization":"jcs","chain_id":"deal-0123456789abcdef","seq":7,"at":"2026-10-30T01:02:03Z","prev":{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"chain_root":{"type":"record","digest_alg":"SHA-256","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"body":{"presents":[{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}"#,
    ),
    (
        "action-report full",
        r#"{"type":"action-report/v0","canonicalization":"jcs","chain_id":"deal-0123456789abcdef","seq":7,"at":"2026-10-30T01:02:03Z","prev":{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"chain_root":{"type":"record","digest_alg":"SHA-256","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"refs":[{"rel":"observes","type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"producer":{"name":"p","version":"1","commit":"c"},"body":{"presents":[{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"authority":[]}}"#,
    ),
    (
        "check-request minimal",
        r#"{"type":"check-request/v0","phase":"proposed","ruleset_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","task_authority_ref":null,"proposed_action":{"action":"pay"}}"#,
    ),
    (
        "check-request full",
        r#"{"type":"check-request/v0","phase":"executed","platform":{"id":"a-platform","display_name":""},"ruleset_ref":"","ruleset_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","task_authority_ref":{"type":"record","digest_alg":"SHA-256","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"root_intent_ref":{"type":"record","digest_alg":"SHA-256","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"parent_delegation_ref":null,"proposed_action":{},"history_refs":[],"agent_assessment":{"n":1},"platform_approval_refs":[]}"#,
    ),
    (
        "check-response predicate",
        r#"{"type":"check-response/v0","disposition":"ASK","valid_until":"2026-10-30T01:02:03Z","proposed_action_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","findings":[],"authority_basis":[{"type":"platform_approval","ref":{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"scope":"mismatch"}],"evaluation_ref":{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"ruleset_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","task_authority_ref":{"type":"record","digest_alg":"SHA-256","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"materiality":"predicate","materiality_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}"#,
    ),
    (
        "check-response none",
        r#"{"type":"check-response/v0","disposition":"DO","valid_until":"2026-10-30T01:02:03Z","proposed_action_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","findings":[{"question":"payee","rule":"payee_matches"}],"authority_basis":[{"type":"task_authority","ref":{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}],"evaluation_ref":{"type":"record","digest_alg":"SHA-256","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"ruleset_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","task_authority_ref":{"type":"record","digest_alg":"SHA-256","digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"materiality":"none_fail_safe","materiality_digest":null}"#,
    ),
];

const TYPED: &[&str] = &[
    deal::TYPE_TASK_AUTHORITY,
    deal::TYPE_PROPOSED_ACTION,
    deal::TYPE_ACTION_EVALUATION,
    deal::TYPE_ACTION_APPROVAL,
    deal::TYPE_ACTION_RECORD,
    deal::TYPE_ACTION_OUTCOME,
    deal::TYPE_ACTION_REPORT,
    deal::TYPE_CHECK_REQUEST,
    deal::TYPE_CHECK_RESPONSE,
    deal::TYPE_COMMERCIAL_BOUNDS,
];

/// Decodes `raw` as its type and returns the JCS bytes it encodes to.
fn round_trip(name: &str, raw: &[u8]) -> (String, Vec<u8>) {
    let doc: Value = serde_json::from_slice(raw).unwrap();
    let (typ, enc) = if doc.get("x-deal-v0").is_some() {
        let r: Record = deal::decode(raw).unwrap_or_else(|e| panic!("{name}: {e}"));
        (
            r.block.record_type.clone(),
            serde_json::to_value(&r).unwrap(),
        )
    } else {
        let t = doc["type"].as_str().unwrap().to_string();
        macro_rules! typed {
            ($($c:ident => $ty:ty),* $(,)?) => {
                match t.as_str() {
                    $(x if x == deal::$c => serde_json::to_value(
                        deal::decode::<$ty>(raw).unwrap_or_else(|e| panic!("{name}: {e}")),
                    ).unwrap(),)*
                    other => panic!("{name}: no type for {other}"),
                }
            };
        }
        let v = typed!(
            TYPE_TASK_AUTHORITY => deal::TaskAuthority,
            TYPE_PROPOSED_ACTION => deal::ProposedAction,
            TYPE_ACTION_EVALUATION => deal::ActionEvaluation,
            TYPE_ACTION_APPROVAL => deal::ActionApproval,
            TYPE_ACTION_RECORD => deal::ActionRecord,
            TYPE_ACTION_OUTCOME => deal::ActionOutcome,
            TYPE_ACTION_REPORT => deal::ActionReport,
            TYPE_CHECK_REQUEST => deal::CheckRequest,
            TYPE_CHECK_RESPONSE => deal::CheckResponse,
            TYPE_COMMERCIAL_BOUNDS => deal::CommercialBounds,
        );
        (t, v)
    };
    (typ, jcs(&enc).unwrap())
}

#[test]
fn records_round_trip() {
    let mut samples: Vec<(String, Vec<u8>)> = Vec::new();
    for dir in [
        "positive",
        "positive-typed",
        "positive-typed-seller",
        "positive-sale",
    ] {
        let mut files: Vec<PathBuf> = fs::read_dir(fixtures_dir().join(dir))
            .unwrap()
            .map(|e| e.unwrap().path())
            .filter(|p| p.extension().is_some_and(|x| x == "json"))
            .collect();
        files.sort();
        assert!(!files.is_empty(), "{dir}");
        for f in files {
            let fx = read(&f);
            samples.push((
                format!("{dir}/{}", f.file_name().unwrap().to_string_lossy()),
                serde_json::to_vec(&fx["record"]).unwrap(),
            ));
        }
    }
    let baseline = read(&repo().join("dealprofile/testdata/baseline-vectors.json"));
    for d in baseline["deals"].as_array().unwrap() {
        for s in d["steps"].as_array().unwrap() {
            samples.push((
                format!("baseline {} step {}", d["flow"], s["n"]),
                s["payload"].as_str().unwrap().as_bytes().to_vec(),
            ));
        }
    }
    let bounds = read(&fixtures_dir().join("commercial-bounds-vectors.json"));
    for (i, v) in bounds["vectors"].as_array().unwrap().iter().enumerate() {
        samples.push((
            format!("commercial-bounds vector {i}"),
            serde_json::to_vec(&v["document"]).unwrap(),
        ));
    }
    for (name, text) in HAND_BUILT {
        samples.push((format!("hand-built {name}"), text.as_bytes().to_vec()));
    }

    let mut types = BTreeSet::new();
    for (name, raw) in &samples {
        let (typ, got) = round_trip(name, raw);
        let want = jcs(&serde_json::from_slice(raw).unwrap()).unwrap();
        assert_eq!(
            String::from_utf8(want).unwrap(),
            String::from_utf8(got).unwrap(),
            "{name}"
        );
        types.insert(typ);
    }
    let missing: Vec<&str> = deal::record_types()
        .iter()
        .chain(TYPED)
        .copied()
        .filter(|t| !types.contains(*t))
        .collect();
    assert!(missing.is_empty(), "no sample of {missing:?}");
}

#[test]
fn hand_built_records_pass_the_record_checks() {
    // The action-report samples are records and pass every record-level
    // stage here. The contract documents are checked against their own
    // schemas by Go's record_test.go, which holds the same texts.
    for (name, text) in HAND_BUILT {
        if name.starts_with("action-report") {
            deal::check(text.as_bytes(), &[]).unwrap_or_else(|e| panic!("{name}: {e}"));
        }
    }
}

fn block(record_type: &str) -> Block {
    Block {
        profile: deal::PROFILE.into(),
        canonicalization: deal::CANONICALIZATION.into(),
        deal_id: "deal-0123456789abcdef".into(),
        record_type: record_type.into(),
        seq: 3,
        at: "2026-10-30T01:02:03Z".into(),
        ..Block::default()
    }
}

#[test]
fn record_refusals() {
    let err = serde_json::to_vec(&Record {
        block: block(deal::TYPE_CLOSE),
        body: Body::Outcome(OutcomeBody::default()),
    })
    .unwrap_err();
    assert!(
        err.to_string().contains("outcome body on a close record"),
        "{err}"
    );

    let sale = r#"{"profile":"x-deal-v0","canonicalization":"jcs","deal_id":"sale-0123456789abcdef","record_type":"sale_close","seq":3,"at":"2026-10-30T01:02:03Z"}"#;
    let ok: Record =
        deal::decode(format!(r#"{{"x-deal-v0":{sale},"body":{{}}}}"#).as_bytes()).unwrap();
    assert_eq!(ok.body, Body::SaleClose(SaleCloseBody {}));
    for (why, text) in [
        ("unknown record type", r#"{"x-deal-v0":{"profile":"x-deal-v0","canonicalization":"jcs","deal_id":"deal-0123456789abcdef","record_type":"deal_note","seq":1,"at":"2026-10-30T01:02:03Z"},"body":{}}"#.to_string()),
        ("a member in another case", format!(r#"{{"x-deal-v0":{sale},"Body":{{}}}}"#)),
        ("an unknown body member", format!(r#"{{"x-deal-v0":{sale},"body":{{"note":"x"}}}}"#)),
        ("an unknown record member", format!(r#"{{"x-deal-v0":{sale},"body":{{}},"extra":1}}"#)),
        ("a duplicate member", format!(r#"{{"x-deal-v0":{sale},"body":{{}},"body":{{}}}}"#)),
    ] {
        assert!(deal::decode::<Record>(text.as_bytes()).is_err(), "{why}");
    }
    // A required nullable member read from an absent member would encode
    // as null: the text is not this record.
    let text = r#"{"type":"check-request/v0","phase":"proposed","ruleset_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","proposed_action":{}}"#;
    let err = deal::decode::<deal::CheckRequest>(text.as_bytes()).unwrap_err();
    assert!(
        err.to_string().contains("would encode to other bytes"),
        "{err}"
    );

    // A record built from the types passes or fails the schema like its
    // text: a close needs its outcome reference and differences.
    let rec = Record {
        block: block(deal::TYPE_CLOSE),
        body: Body::Close(CloseBody::default()),
    };
    let err = deal::encode(&serde_json::to_vec(&rec).unwrap(), &[]).unwrap_err();
    assert_eq!(err.stage().map(|s| s.stage), Some(deal::STAGE_SCHEMA));
}
