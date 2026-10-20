//! Answering from a store this crate does not own: a caller whose own
//! checkpointer wrote the log hands the answer its index through the store
//! traits, and the crate reads no file of the caller's. The store here
//! commits an entry that is not the SHA-256 of the record (as a capsule
//! ledger commits each line's `capsule_id`), keeps its own in-memory MMR,
//! and answers after the files its checkpoints were cut from are deleted.

use cll::mmr::{add_leaf, consistency_proof, inclusion_proof, leaf_hash, MemoryNodeStore};
use ed25519_dalek::SigningKey;
use evidence_request::cll_material::{KIND_CHECKPOINT, KIND_CONSISTENCY, KIND_INCLUSION};
use evidence_request::envelope::Signer as EnvelopeSigner;
use evidence_request::request::{parse_json, Request, Subject};
use evidence_request::resolve::{self, ResolvedAnchor};
use evidencebook::answer::{
    anchor_digest, build, select, serving_checkpoint, AnswerError, Options, Selection,
};
use evidencebook::index::committed_records;
use evidencebook::reconcile::HalfSet;
use evidencebook::store::{CheckpointedLog, ConsistencyParts, Record, RecordSource};
use evidencebook::substrate::{
    CllSubstrate, Signer as CheckpointSigner, StatedCheckpoint, SubstrateError,
};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::cell::RefCell;

const ISSUED_AT: &str = "2026-10-19T00:00:00Z";

fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

/// The interaction half records cite in these tests.
fn half() -> String {
    hex::encode(Sha256::digest(b"h"))
}

/// This store's committed entry for a record: not the SHA-256 of its octets.
fn entry(body: &[u8]) -> String {
    let mut h = Sha256::new();
    h.update(b"entry:");
    h.update(body);
    hex::encode(h.finalize())
}

/// A caller's own index: records, stated checkpoints read once, and an
/// in-memory MMR over the same leaves.
struct Foreign {
    records: RefCell<Vec<Record>>,
    citing: std::collections::BTreeMap<String, Vec<u64>>,
    stated: Vec<StatedCheckpoint>,
    nodes: MemoryNodeStore,
}

impl RecordSource for Foreign {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        self.records
            .borrow()
            .iter()
            .find(|r| r.digest == digest)
            .cloned()
    }
    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        self.records.borrow().get(leaf_index as usize).cloned()
    }
    fn correlation(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn citing(&self, half: &str) -> Vec<u64> {
        self.citing.get(half).cloned().unwrap_or_default()
    }
    fn entry_of(&self, body: &[u8]) -> Option<String> {
        Some(entry(body))
    }
}

fn hashes(list: &[String]) -> Vec<[u8; 32]> {
    list.iter()
        .map(|h| hex::decode(h).unwrap().try_into().unwrap())
        .collect()
}

impl CheckpointedLog for Foreign {
    fn leaf_count(&self) -> Result<u64, SubstrateError> {
        Ok(self.records.borrow().len() as u64)
    }
    fn stated_checkpoints(&self) -> Result<Vec<StatedCheckpoint>, SubstrateError> {
        Ok(self.stated.clone())
    }
    fn inclusion_path(&self, leaf: u64, size: u64) -> Result<Vec<[u8; 32]>, SubstrateError> {
        let p = inclusion_proof(&self.nodes, leaf, size)
            .map_err(|e| SubstrateError::Mmr(e.to_string()))?;
        Ok(hashes(&p.witness))
    }
    fn consistency_parts(&self, from: u64, to: u64) -> Result<ConsistencyParts, SubstrateError> {
        let p = consistency_proof(&self.nodes, from, to)
            .map_err(|e| SubstrateError::Mmr(e.to_string()))?;
        Ok((
            hashes(&p.old_peaks),
            p.witness.iter().map(|w| hashes(w)).collect(),
            hashes(&p.new_peaks),
        ))
    }
}

/// Six records, checkpoints after 2 and 5, cut by a checkpointer that then
/// goes away with its files. Records 1 and 3 cite [`half`].
fn foreign() -> Foreign {
    foreign_with(|i| i, &[1, 3])
}

/// [`foreign`], record `i` being the `body_of(i)`-th distinct body (so two
/// positions can commit the same entry), the records at `citing` citing [`half`].
fn foreign_with(body_of: impl Fn(u64) -> u64, citing: &[u64]) -> Foreign {
    let dir = tempfile::tempdir().unwrap();
    let (mut s, _) = CllSubstrate::open(
        &dir.path().join("mmr_nodes.dat"),
        &dir.path().join("checkpoints.jsonl"),
    )
    .unwrap();
    let mut nodes = MemoryNodeStore::new();
    let mut records = Vec::new();
    for i in 0..6u64 {
        let n = body_of(i);
        let body = json!({"v": 1, "book_id": "b", "seq": n + 1, "record_type": "claim",
            "epistemic_type": "producer_claim", "committed_at": "2026-10-19T00:00:00Z",
            "links": [], "correlation": {"k": format!("c-{n}")}})
        .to_string()
        .into_bytes();
        let digest = entry(&body);
        let id: [u8; 32] = hex::decode(&digest).unwrap().try_into().unwrap();
        s.append_leaf(&id).unwrap();
        add_leaf(&mut nodes, leaf_hash(&id)).unwrap();
        records.push(Record {
            leaf_index: i,
            digest,
            body,
        });
        if i == 1 || i == 4 {
            let ts = format!("2026-10-19T00:00:0{i}Z");
            let p = s
                .prepare_checkpoint(i + 1, "log", &ts, &key("log") as &dyn CheckpointSigner)
                .unwrap();
            s.commit_checkpoint(p).unwrap();
        }
    }
    let stated = s.stated_checkpoints().unwrap();
    drop(s);
    drop(dir);
    Foreign {
        records: RefCell::new(records),
        citing: [(half(), citing.to_vec())].into(),
        stated,
        nodes,
    }
}

fn request(subject: Value, coverage: Value) -> (Vec<u8>, Request) {
    let bytes = serde_json::to_vec(&json!({"subject": subject, "coverage": coverage})).unwrap();
    let parsed = parse_json(&bytes).unwrap();
    (bytes, parsed)
}

fn pinned(f: &Foreign, i: usize) -> ResolvedAnchor {
    let cp = &f.stated[i];
    ResolvedAnchor::Anchor(resolve::Anchor {
        digest: anchor_digest(cp),
        size: cp.checkpoint.leaf_count().unwrap(),
        issued_at: cp.checkpoint.timestamp.clone(),
    })
}

fn go(
    f: &Foreign,
    subject: Value,
    anchor: &ResolvedAnchor,
    options: Options<'_>,
) -> Result<evidence_request::envelope::Envelope, AnswerError> {
    let (bytes, req) = request(subject, json!({"expected_pin": anchor.digest()}));
    let selection = select(f, f, &req, anchor, 100)?;
    build(
        f,
        f,
        &req,
        &bytes,
        anchor,
        &selection,
        options,
        &key("responder") as &dyn EnvelopeSigner,
        ISSUED_AT,
    )
}

fn kinds(env: &evidence_request::envelope::Envelope) -> Vec<&str> {
    env.verification.iter().map(|v| v.kind.as_str()).collect()
}

#[test]
fn a_record_named_by_its_committed_entry_is_served_from_the_callers_index() {
    let f = foreign();
    let at = pinned(&f, 1);
    let digest = f.records.borrow()[3].digest.clone();
    let env = go(&f, json!({"record": digest}), &at, Options::default()).unwrap();
    assert_eq!(env.artifact, f.records.borrow()[3].body);
    assert_ne!(env.artifact_digest, digest, "the entry is not the SHA-256");
    assert_eq!(
        kinds(&env),
        [KIND_CHECKPOINT, KIND_INCLUSION, KIND_CONSISTENCY]
    );
    env.verify_for(
        &key("responder").verifying_key(),
        &request(
            json!({"record": digest}),
            json!({"expected_pin": at.digest()}),
        )
        .0,
    )
    .unwrap();
}

#[test]
fn a_record_whose_octets_do_not_recompute_its_entry_is_never_served() {
    let f = foreign();
    let at = pinned(&f, 1);
    let digest = f.records.borrow()[2].digest.clone();
    f.records.borrow_mut()[2].body.push(b' ');
    assert!(matches!(
        go(
            &f,
            json!({"record": digest.clone()}),
            &at,
            Options::default()
        ),
        Err(AnswerError::RecordDigest)
    ));
    assert!(matches!(
        go(&f, json!({"range": [0, 3]}), &at, Options::default()),
        Err(AnswerError::RecordDigest)
    ));
}

#[test]
fn beyond_the_profile_a_record_set_carries_its_checkpoint_and_each_inclusion() {
    let f = foreign();
    let at = pinned(&f, 1);
    let profile = go(&f, json!({"range": [1, 3]}), &at, Options::default()).unwrap();
    assert!(profile.verification.is_empty(), "P-13: none by default");
    let beyond = Options {
        prove_beyond_profile: true,
        derive: None,
        derivations: None,
    };
    let env = go(&f, json!({"range": [1, 3]}), &at, beyond).unwrap();
    assert_eq!(
        kinds(&env),
        [
            KIND_CHECKPOINT,
            KIND_INCLUSION,
            KIND_INCLUSION,
            KIND_INCLUSION
        ]
    );
    assert_eq!(env.artifact, profile.artifact, "the artifact is unchanged");
    assert_eq!(env.verification_content[0], f.stated[1].statement);
    // The checkpoint list and the history card carry their statements in
    // the artifact and no material.
    let card = go(&f, json!({"checkpoints": null}), &at, beyond).unwrap();
    assert!(card.verification.is_empty());
}

#[test]
fn a_profile_derivation_is_answered_only_by_the_profiles_derive() {
    let f = foreign();
    let at = pinned(&f, 1);
    let subject = json!({"full_history": null});
    let (bytes, mut req) = request(subject.clone(), json!({"expected_pin": at.digest()}));
    req.derivation = Some(evidence_request::request::Derivation::Token(
        "count/1".into(),
    ));
    let ask = |options: Options<'_>| {
        let selection = select(&f, &f, &req, &at, 100)?;
        assert_eq!(
            selection,
            Selection {
                at: 1,
                records: vec![]
            }
        );
        build(
            &f,
            &f,
            &req,
            &bytes,
            &at,
            &selection,
            options,
            &key("responder") as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
    };
    assert!(matches!(
        ask(Options::default()),
        Err(AnswerError::DerivationUnsupported)
    ));
    let derive = |token: &str, subject: &Subject, cp: &StatedCheckpoint| {
        (token == "count/1" && *subject == Subject::FullHistory).then(|| {
            (
                "count".to_string(),
                json!(cp.checkpoint.leaf_count().unwrap()),
            )
        })
    };
    let env = ask(Options {
        prove_beyond_profile: true,
        derive: Some(&derive),
        derivations: None,
    })
    .unwrap();
    let art: Value = serde_json::from_slice(&env.artifact).unwrap();
    assert_eq!(art["count"], json!(5));
    assert_eq!(art["derivation"], json!("count/1"));
    assert_eq!(art["anchor"], json!(at.digest()));
    assert_eq!(kinds(&env), [KIND_CHECKPOINT]);
}

#[test]
fn the_citing_anchor_reads_the_callers_index() {
    let f = foreign();
    let pin = ResolvedAnchor::ExchangeHalf(half());
    assert_eq!(serving_checkpoint(&f, &f.stated, &pin).unwrap(), 1);
    let none = ResolvedAnchor::ExchangeHalf("other".into());
    assert!(matches!(
        serving_checkpoint(&f, &f.stated, &none),
        Err(AnswerError::CitingNotAnchored)
    ));
}

#[test]
fn the_index_and_reconcile_read_the_same_store() {
    let f = foreign();
    let indexed = committed_records(&f, &f).unwrap();
    assert_eq!(indexed.len(), 6);
    assert_eq!(indexed[2].seq, 3);
    assert_eq!(indexed[2].record_id, f.records.borrow()[2].digest);

    let ids: Vec<String> = f
        .records
        .borrow()
        .iter()
        .map(|r| r.digest.clone())
        .collect();
    let set = HalfSet::from_store(&f, &f, &ids, true).unwrap();
    assert_eq!(set.halves.len(), 6);
    // The latest stated checkpoint covers five leaves.
    assert!(set.halves[4].covered && !set.halves[5].covered);
    assert!(set.halves.iter().all(|h| h.trusted));
    assert_eq!(set.halves[0].correlation["k"], "c-0");

    // A record that no longer recomputes its entry is an untrusted half with
    // nothing to pair on, and a gap in the store fails the index rebuild.
    f.records.borrow_mut()[0].body.push(b' ');
    let set = HalfSet::from_store(&f, &f, &ids, true).unwrap();
    assert!(!set.halves[0].trusted && set.halves[0].correlation.is_empty());
    assert!(matches!(
        committed_records(&f, &f),
        Err(SubstrateError::Record { leaf_index: 0, .. })
    ));
    f.records.borrow_mut()[0].body.pop();
    f.records.borrow_mut().remove(3);
    assert!(matches!(
        committed_records(&f, &f),
        Err(SubstrateError::Record { leaf_index: 3, .. })
    ));
}

#[test]
fn an_entry_committed_twice_is_cited_at_each_position() {
    // Records 1 and 4 are the same record committed twice; both cite the half.
    // The later one is what the latest checkpoint must cover, so the answer
    // is served under the second checkpoint, not the first.
    let f = foreign_with(|i| if i == 4 { 1 } else { i }, &[1, 4]);
    assert_eq!(f.leaf_index_of(&f.records.borrow()[4].digest), Some(1));
    let pin = ResolvedAnchor::ExchangeHalf(half());
    assert_eq!(serving_checkpoint(&f, &f.stated, &pin).unwrap(), 1);
    let (_, req) = request(json!({"exchange": half()}), json!({"expected_pin": half()})); // vocabulary-gate: request-draft wire token
    let selection = select(&f, &f, &req, &pin, 100).unwrap();
    assert_eq!(
        selection,
        Selection {
            at: 1,
            records: vec![1, 4]
        }
    );
}

#[test]
fn a_derived_value_with_no_canonical_form_is_an_error_not_a_panic() {
    let f = foreign();
    let at = pinned(&f, 1);
    let (bytes, mut req) = request(
        json!({"full_history": null}),
        json!({"expected_pin": at.digest()}),
    );
    req.derivation = Some(evidence_request::request::Derivation::Token(
        "ratio/1".into(),
    ));
    let derive =
        |_: &str, _: &Subject, _: &StatedCheckpoint| Some(("ratio".to_string(), json!(1.5)));
    let r = build(
        &f,
        &f,
        &req,
        &bytes,
        &at,
        &Selection {
            at: 1,
            records: vec![],
        },
        Options {
            prove_beyond_profile: false,
            derive: Some(&derive),
            derivations: None,
        },
        &key("responder") as &dyn EnvelopeSigner,
        ISSUED_AT,
    );
    assert!(matches!(r, Err(AnswerError::Artifact(_))));
}
