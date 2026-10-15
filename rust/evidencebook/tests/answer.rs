//! `answer` end to end: records appended to a real substrate, checkpoints cut
//! with their wire statements, requests resolved by `evidence_request`, the
//! envelope built here, and every envelope accepted by the `evidence-request`
//! verifier: the signature and digests, and for a record the Checkpointed
//! Local Log material (`anchor_inclusion` PASS).

use ed25519_dalek::{SigningKey, VerifyingKey};
use evidence_request::cll_material::{check_anchor_inclusion, KeyDecision, LogKeyPolicy, Status};
use evidence_request::envelope::{
    parse_reply, AnchorFreshness, Binding, Envelope, Reply, Signer as EnvelopeSigner,
};
use evidence_request::registry::SubjectForm;
use evidence_request::request::{parse_json, Derivation, Request};
use evidence_request::resolve::{self, resolve, Resolution, Responder};
use evidencebook::answer::{anchors, answer, AnswerError, Record, RecordSource, MAX_RECORDS};
use evidencebook::substrate::{CllSubstrate, Signer as CheckpointSigner};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

const LOG_ID: &str = "book-1";
const ISSUED_AT: &str = "2026-10-15T01:00:00Z";

fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

struct Book {
    substrate: CllSubstrate,
    records: Vec<Record>,
    correlation: BTreeMap<String, Vec<String>>,
    citing: BTreeMap<String, Vec<String>>,
    _dir: tempfile::TempDir,
}

fn half() -> String {
    hex::encode(Sha256::digest(b"the requester's own half"))
}

/// Ten records, checkpoints after 3, 7 and 10. Records 1 and 5 carry
/// correlation c-1; records 2 and 8 cite the requester's half.
fn book() -> Book {
    let dir = tempfile::tempdir().unwrap();
    let (mut substrate, _) = CllSubstrate::open(
        &dir.path().join("nodes"),
        &dir.path().join("checkpoints.jsonl"),
    )
    .unwrap();
    let log_key = key("log");
    let mut records = Vec::new();
    let mut correlation: BTreeMap<String, Vec<String>> = BTreeMap::new();
    let mut citing: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for i in 0..10u64 {
        let body = format!("record-{i}").into_bytes();
        let id: [u8; 32] = Sha256::digest(&body).into();
        assert_eq!(substrate.append_leaf(&id).unwrap(), i);
        let digest = hex::encode(id);
        if i == 1 || i == 5 {
            correlation
                .entry("c-1".into())
                .or_default()
                .push(digest.clone());
        }
        if i == 2 || i == 8 {
            citing.entry(half()).or_default().push(digest.clone());
        }
        records.push(Record {
            leaf_index: i,
            digest,
            body,
        });
        if [2, 6, 9].contains(&i) {
            let ts = format!("2026-10-15T00:00:0{i}Z");
            let prepared = substrate
                .prepare_checkpoint(i + 1, LOG_ID, &ts, &log_key as &dyn CheckpointSigner)
                .unwrap();
            assert!(prepared.cose().is_some(), "{:?}", prepared.cose_error());
            substrate.commit_checkpoint(prepared).unwrap();
        }
    }
    Book {
        substrate,
        records,
        correlation,
        citing,
        _dir: dir,
    }
}

impl RecordSource for Book {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        self.records.iter().find(|r| r.digest == digest).cloned()
    }
    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        self.records.get(leaf_index as usize).cloned()
    }
    fn correlation(&self, id: &str) -> Vec<String> {
        self.correlation.get(id).cloned().unwrap_or_default()
    }
    fn citing(&self, half_digest: &str) -> Vec<String> {
        self.citing.get(half_digest).cloned().unwrap_or_default()
    }
}

impl Responder for Book {
    fn holds_record(&self, digest: &str) -> bool {
        self.record_by_digest(digest).is_some()
    }
    fn holds_correlation(&self, id: &str) -> bool {
        self.correlation.contains_key(id)
    }
    #[rustfmt::skip]
    fn cites_exchange(&self, half: &str) -> bool { self.citing.contains_key(half) } // the "exchange" subject form; vocabulary-gate: request-draft wire token
    fn positional_ordering(&self) -> bool {
        true
    }
    fn anchors(&self) -> Vec<resolve::Anchor> {
        anchors(&self.substrate).unwrap()
    }
    fn derivation_forms(&self, derivation: &Derivation) -> Option<Vec<SubjectForm>> {
        match derivation {
            Derivation::Token(t) if t == "history_card/1" => {
                Some(vec![SubjectForm::FullHistory, SubjectForm::Checkpoints])
            }
            _ => None,
        }
    }
}

/// The book's log key, for the checkpoints' (iss, sub) only.
struct LogKey(VerifyingKey);

impl LogKeyPolicy for LogKey {
    fn resolve(&self, iss: &str, sub: &str, kid: &[u8], _at: &str) -> KeyDecision {
        if iss == LOG_ID && sub.starts_with(&format!("{LOG_ID}#")) && kid == self.0.to_bytes() {
            KeyDecision::Accept(self.0)
        } else {
            KeyDecision::Reject
        }
    }
}

fn request(subject: Value, coverage: Value, extra: Value) -> (Vec<u8>, Request) {
    let mut map = json!({"subject": subject, "coverage": coverage});
    if let Value::Object(e) = extra {
        for (k, v) in e {
            map[k] = v;
        }
    }
    let bytes = serde_json::to_vec(&map).unwrap();
    let parsed = parse_json(&bytes).unwrap();
    (bytes, parsed)
}

fn pin(b: &Book, i: usize) -> Value {
    json!({"expected_pin": b.anchors()[i].digest})
}

/// Resolve and answer as the responder; accept as the requester.
fn round_trip(b: &Book, bytes: &[u8], req: &Request) -> Envelope {
    let Resolution::Artifact(anchor) = resolve(req, b) else {
        panic!("expected an artifact");
    };
    let responder = key("responder");
    let env = answer(
        &b.substrate,
        b,
        req,
        bytes,
        &anchor,
        MAX_RECORDS,
        &responder as &dyn EnvelopeSigner,
        ISSUED_AT,
    )
    .unwrap();
    for binding in [Binding::Json, Binding::Cbor] {
        let Ok(Reply::Envelope(got)) = parse_reply(&env.encode(binding), binding) else {
            panic!("the reply does not read back");
        };
        assert_eq!(*got, env);
    }
    env.verify_for(&responder.verifying_key(), bytes).unwrap();
    env
}

fn artifact_json(env: &Envelope) -> Value {
    serde_json::from_slice(&env.artifact).unwrap()
}

fn leaves(env: &Envelope) -> Vec<u64> {
    artifact_json(env)["records"]
        .as_array()
        .unwrap()
        .iter()
        .map(|r| r["leaf_index"].as_u64().unwrap())
        .collect()
}

#[test]
fn a_record_is_its_own_octets_and_its_material_passes() {
    let b = book();
    let policy = LogKey(key("log").verifying_key());
    for (anchor, leaf, kinds) in [(0usize, 1usize, 2usize), (1, 3, 3), (2, 9, 3), (2, 0, 3)] {
        let (bytes, req) = request(
            json!({"record": b.records[leaf].digest}),
            pin(&b, anchor),
            json!({"nonce": "n"}),
        );
        let env = round_trip(&b, &bytes, &req);
        assert_eq!(env.artifact, b.records[leaf].body);
        assert_eq!(env.artifact_digest, b.records[leaf].digest);
        assert_eq!(
            env.verification.len(),
            kinds,
            "a checkpoint that extends another adds consistency"
        );
        let r = check_anchor_inclusion(&env, &policy);
        assert_eq!(
            r.status,
            Status::Pass,
            "anchor {anchor} leaf {leaf}: {}",
            r.detail
        );
    }
    // A record no checkpoint up to the pinned one covers.
    let (bytes, req) = request(
        json!({"record": b.records[8].digest}),
        pin(&b, 1),
        json!({}),
    );
    let Resolution::Artifact(anchor) = resolve(&req, &b) else {
        panic!()
    };
    let r = answer(
        &b.substrate,
        &b,
        &req,
        &bytes,
        &anchor,
        MAX_RECORDS,
        &key("responder") as &dyn EnvelopeSigner,
        ISSUED_AT,
    );
    assert!(matches!(r, Err(AnswerError::RecordNotFound)));
}

#[test]
fn every_subject_form_builds_and_verifies() {
    let b = book();
    let policy = LogKey(key("log").verifying_key());

    let (x, q) = request(json!({"range": [2, 5]}), pin(&b, 1), json!({}));
    let env = round_trip(&b, &x, &q);
    assert_eq!(leaves(&env), vec![2, 3, 4, 5]);
    assert!(
        env.verification.is_empty(),
        "the profile defines no material for a record set (P-13)"
    );
    assert_eq!(
        check_anchor_inclusion(&env, &policy).status,
        Status::NotChecked
    );

    let (x, q) = request(
        json!({"full_history": null}),
        json!({"min_freshness": 7}),
        json!({}),
    );
    let env = round_trip(&b, &x, &q);
    assert_eq!(
        leaves(&env).len(),
        10,
        "min_freshness picks the freshest anchor"
    );
    let last = &b.substrate.stated_checkpoints().unwrap()[2];
    assert_eq!(
        env.anchor.freshness,
        Some(AnchorFreshness::Size(last.checkpoint.mmr_size))
    );

    let (x, q) = request(json!({"correlation": "c-1"}), pin(&b, 2), json!({}));
    assert_eq!(leaves(&round_trip(&b, &x, &q)), vec![1, 5]);
    let (x, q) = request(json!({"correlation": "c-1"}), pin(&b, 0), json!({}));
    assert_eq!(
        leaves(&round_trip(&b, &x, &q)),
        vec![1],
        "only records under the pinned anchor"
    );

    // A citing-records subject pinned by the requester's own half: the earliest
    // checkpoint covering every checkpointed citing record.
    let (x, q) = request(
        json!({"exchange": half()}), // vocabulary-gate: request-draft wire token
        json!({"expected_pin": half()}),
        json!({}),
    );
    let env = round_trip(&b, &x, &q);
    assert_eq!(leaves(&env), vec![2, 8]);
    assert_eq!(env.anchor.digest, b.anchors()[2].digest);

    let (x, q) = request(json!({"checkpoints": null}), pin(&b, 1), json!({}));
    let env = round_trip(&b, &x, &q);
    let stated = b.substrate.stated_checkpoints().unwrap();
    let listed: Vec<String> = artifact_json(&env)["checkpoints"]
        .as_array()
        .unwrap()
        .iter()
        .map(|c| c.as_str().unwrap().into())
        .collect();
    assert_eq!(
        listed,
        stated[..2]
            .iter()
            .map(|c| hex::encode(&c.statement))
            .collect::<Vec<_>>()
    );

    let (x, q) = request(
        json!({"checkpoints": null}),
        pin(&b, 2),
        json!({"derivation": "history_card/1"}),
    );
    let env = round_trip(&b, &x, &q);
    let history = artifact_json(&env)["history"].as_array().unwrap().clone();
    assert_eq!(history.len(), 3);
    assert!(history[0]["consistency_from_previous"].is_null());
    assert!(
        history[1]["consistency_from_previous"].is_string()
            && history[2]["consistency_from_previous"].is_string()
    );
    assert_eq!(artifact_json(&env)["derivation"], "history_card/1");
}

#[test]
fn freshness_names_what_the_checkpoint_commits() {
    let b = book();
    let policy = LogKey(key("log").verifying_key());
    let r3 = b.records[3].digest.clone();
    for coverage in [
        json!({"min_freshness": 4}),
        json!({"min_freshness": "2026-10-15T00:00:05Z"}),
    ] {
        let (x, q) = request(json!({"record": r3}), coverage.clone(), json!({}));
        let env = round_trip(&b, &x, &q);
        assert!(env.anchor.freshness.is_some(), "{coverage}");
        let r = check_anchor_inclusion(&env, &policy);
        assert_eq!(r.status, Status::Pass, "{coverage}: {}", r.detail);
    }
}

#[test]
fn artifacts_are_caller_invariant_and_envelopes_are_per_request() {
    let b = book();
    let (x1, q1) = request(
        json!({"range": [0, 3]}),
        pin(&b, 1),
        json!({"nonce": "a", "route": "stream"}),
    );
    let (x2, q2) = request(
        json!({"range": [0, 3]}),
        pin(&b, 1),
        json!({"nonce": "b", "route": "http"}),
    );
    let (e1, e2) = (round_trip(&b, &x1, &q1), round_trip(&b, &x2, &q2));
    assert_eq!(e1.artifact, e2.artifact);
    assert_ne!(e1.request_digest, e2.request_digest);
    assert!(e1
        .verify_for(&key("responder").verifying_key(), &x2)
        .is_err());
}

#[test]
fn limits_and_unsupported_derivations_are_refused() {
    let b = book();
    let responder = key("responder");
    let go = |subject: Value, extra: Value, limit: u64| {
        let (x, q) = request(subject, pin(&b, 2), extra);
        let anchor = match resolve(&q, &b) {
            Resolution::Artifact(a) => a,
            other => panic!("{other:?}"),
        };
        answer(
            &b.substrate,
            &b,
            &q,
            &x,
            &anchor,
            limit,
            &responder as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
    };
    assert!(matches!(
        go(json!({"range": [0, 5]}), json!({}), 5),
        Err(AnswerError::OverLimit)
    ));
    assert!(go(json!({"range": [0, 4]}), json!({}), 5).is_ok());
    assert!(matches!(
        go(json!({"full_history": null}), json!({}), 9),
        Err(AnswerError::OverLimit)
    ));
    assert!(matches!(
        go(json!({"checkpoints": null}), json!({}), 2),
        Err(AnswerError::OverLimit)
    ));
    assert!(matches!(
        go(json!({"correlation": "c-1"}), json!({}), 1),
        Err(AnswerError::OverLimit)
    ));
}
