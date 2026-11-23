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
use evidencebook::derivation::{DerivationDefinition, Derivations};
use evidencebook::index::committed_records;
use evidencebook::reconcile::HalfSet;
use evidencebook::request::SubjectKind;
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
    /// Each record's octets are its header.
    fn header_of(&self, record: &Record) -> Option<serde_json::Map<String, Value>> {
        serde_json::from_slice(&record.body).ok()
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

/// The envelope names the profile the request asked for (profile §3.3): a
/// request with none is `evidence-interop/0`, one naming
/// `evidence-interop/1` is signed under /1 with the same artifact (ER §5),
/// and one naming a profile evidence-request does not implement is not
/// signed.
#[test]
fn the_envelope_is_signed_under_the_profile_the_request_names() {
    let f = foreign();
    let at = pinned(&f, 1);
    let under = |profile: Option<&str>| {
        let mut map =
            json!({"subject": {"range": [1, 3]}, "coverage": {"expected_pin": at.digest()}});
        if let Some(p) = profile {
            map["profile"] = json!(p);
        }
        let bytes = serde_json::to_vec(&map).unwrap();
        let req = parse_json(&bytes).unwrap();
        let selection = select(&f, &f, &req, &at, 100).unwrap();
        build(
            &f,
            &f,
            &req,
            &bytes,
            &at,
            &selection,
            Options::default(),
            &key("responder") as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
    };
    let v0 = under(None).unwrap();
    assert_eq!(v0.profile, evidence_request::envelope::PROFILE);
    let v1 = under(Some(evidence_request::envelope::PROFILE_V1)).unwrap();
    assert_eq!(v1.profile, evidence_request::envelope::PROFILE_V1);
    assert_eq!(v1.artifact, v0.artifact);
    assert!(v1.verify().is_ok());
    assert!(matches!(
        under(Some("evidence-interop/9")),
        Err(AnswerError::Sign(_))
    ));
    // evidence-request signs for /2, which the book does not answer.
    assert!(matches!(
        under(Some("evidence-interop/2")),
        Err(AnswerError::Sign(_))
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
        requester: None,
        standard_card: None,
        history_window: 0,
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

/// Beyond the profile, a registered derivation carries the material its
/// subject's record set would: the serving checkpoint and one inclusion
/// proof per record, in the artifact's order, under an anchor or a half pin.
#[test]
fn beyond_the_profile_a_registered_derivation_carries_its_records_material() {
    let f = foreign();
    let mut derivations = Derivations::new();
    let id = derivations
        .register(DerivationDefinition {
            document: br#"{"project":["seq"]}"#.to_vec(),
            recipient: None,
            forms: vec![SubjectKind::Range, SubjectKind::CitingRecords],
            fields: vec!["seq".into()],
            ..Default::default()
        })
        .unwrap();
    let at = pinned(&f, 1);
    let half_pin = ResolvedAnchor::ExchangeHalf(half());
    #[rustfmt::skip]
    let cases = [(json!({"range": [1, 3]}), &at, 3), (json!({"exchange": half()}), &half_pin, 2)]; // vocabulary-gate: request-draft wire token
    for (subject, anchor, n) in cases {
        let ask = |derived: bool, prove: bool| {
            let mut wire =
                json!({"subject": subject, "coverage": {"expected_pin": anchor.digest()}});
            if derived {
                wire["derivation"] = json!(id);
            }
            let bytes = serde_json::to_vec(&wire).unwrap();
            let req = parse_json(&bytes).unwrap();
            let selection = select(&f, &f, &req, anchor, 100).unwrap();
            build(
                &f,
                &f,
                &req,
                &bytes,
                anchor,
                &selection,
                Options {
                    prove_beyond_profile: prove,
                    derivations: Some(&derivations),
                    ..Options::default()
                },
                &key("responder") as &dyn EnvelopeSigner,
                ISSUED_AT,
            )
            .unwrap()
        };
        assert!(
            ask(true, false).verification.is_empty(),
            "P-13: none by default"
        );
        let derived = ask(true, true);
        let set = ask(false, true);
        let mut want = vec![KIND_CHECKPOINT];
        want.extend(std::iter::repeat_n(KIND_INCLUSION, n));
        assert_eq!(kinds(&derived), want);
        assert_eq!(derived.verification_content, set.verification_content);
        let art: Value = serde_json::from_slice(&derived.artifact).unwrap();
        let leaves: Vec<u64> = art["records"]
            .as_array()
            .unwrap()
            .iter()
            .map(|r| r["leaf_index"].as_u64().unwrap())
            .collect();
        let seqs: Vec<u64> = art["records"]
            .as_array()
            .unwrap()
            .iter()
            .map(|r| r["seq"].as_u64().unwrap())
            .collect();
        assert_eq!(seqs, leaves.iter().map(|l| l + 1).collect::<Vec<_>>());
        assert_eq!(leaves.len(), n);
    }
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
        requester: None,
        standard_card: None,
        history_window: 0,
    })
    .unwrap();
    let art: Value = serde_json::from_slice(&env.artifact).unwrap();
    assert_eq!(art["count"], json!(5));
    assert_eq!(art["derivation"], json!("count/1"));
    assert_eq!(art["anchor"], json!(at.digest()));
    assert_eq!(kinds(&env), [KIND_CHECKPOINT]);
}

/// A digest derivation of the holder's own, which `Options::derivations`
/// does not register, is answered by the profile's `Derive` like a token;
/// a registered digest is answered from its registration even when `Derive`
/// would answer it, and an unregistered digest `Derive` does not answer is
/// unsupported.
#[test]
fn an_unregistered_digest_derivation_is_answered_by_the_profiles_derive() {
    let f = foreign();
    let at = pinned(&f, 1);
    let mut derivations = Derivations::new();
    let registered = derivations
        .register(DerivationDefinition {
            document: br#"{"project":["seq"]}"#.to_vec(),
            recipient: None,
            forms: vec![SubjectKind::FullHistory],
            fields: vec!["seq".into()],
            ..Default::default()
        })
        .unwrap();
    let own = "ab".repeat(32);
    let derive = |id: &str, subject: &Subject, cp: &StatedCheckpoint| {
        (*subject == Subject::FullHistory).then(|| {
            (
                "entries".to_string(),
                json!({"for": id, "leaves": cp.checkpoint.leaf_count().unwrap()}),
            )
        })
    };
    let ask = |digest: &str, subject: Value, derive: Option<evidencebook::answer::Derive<'_>>| {
        let (bytes, mut req) = request(subject, json!({"expected_pin": at.digest()}));
        req.derivation = Some(evidence_request::request::Derivation::Digest(
            digest.to_string(),
        ));
        build(
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
                prove_beyond_profile: true,
                derive,
                derivations: Some(&derivations),
                requester: None,
                standard_card: None,
                history_window: 0,
            },
            &key("responder") as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
    };
    let env = ask(&own, json!({"full_history": null}), Some(&derive)).unwrap();
    let art: Value = serde_json::from_slice(&env.artifact).unwrap();
    assert_eq!(art["derivation"], json!(own));
    assert_eq!(art["entries"], json!({"for": own, "leaves": 5}));
    assert_eq!(kinds(&env), [KIND_CHECKPOINT]);

    // Derive declines this subject: unsupported, as without Derive.
    for derive in [Some(&derive as evidencebook::answer::Derive<'_>), None] {
        assert!(matches!(
            ask(&own, json!({"range": [1, 2]}), derive),
            Err(AnswerError::DerivationUnsupported)
        ));
    }
    // A registered digest is never Derive's, even where its registration
    // refuses: a form it does not declare, or a requester its recipient
    // is not.
    let mut restricted = Derivations::new();
    let range_only = restricted
        .register(DerivationDefinition {
            document: br#"{"project":["seq"],"for":"alice"}"#.to_vec(),
            recipient: Some("alice".into()),
            forms: vec![SubjectKind::Range],
            fields: vec!["seq".into()],
            ..Default::default()
        })
        .unwrap();
    let answers_all = |_: &str, _: &Subject, _: &StatedCheckpoint| {
        Some(("entries".to_string(), json!("from Derive")))
    };
    let refused = |subject: Value, requester: Option<&str>| {
        let (bytes, mut req) = request(subject, json!({"expected_pin": at.digest()}));
        req.derivation = Some(evidence_request::request::Derivation::Digest(
            range_only.clone(),
        ));
        build(
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
                prove_beyond_profile: true,
                derive: Some(&answers_all),
                derivations: Some(&restricted),
                requester,
                standard_card: None,
                history_window: 0,
            },
            &key("responder") as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
    };
    assert!(matches!(
        refused(json!({"full_history": null}), Some("alice")),
        Err(AnswerError::DerivationUnsupported)
    ));
    assert!(matches!(
        refused(json!({"range": [1, 2]}), Some("bob")),
        Err(AnswerError::NotAuthorized)
    ));

    // The registration answers it, from the records the selection names
    // (none here), never Derive.
    assert!(matches!(
        ask(&registered, json!({"full_history": null}), Some(&derive)),
        Err(AnswerError::NoRecords)
    ));
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
            requester: None,
            standard_card: None,
            history_window: 0,
        },
        &key("responder") as &dyn EnvelopeSigner,
        ISSUED_AT,
    );
    assert!(matches!(r, Err(AnswerError::Artifact(_))));
}

// ---- Terms Pack derivations: grants, nested fields and set proofs (#746) ----

mod set_proofs {
    use super::*;
    use evidence_request::envelope::{sign_with_profile, Anchor, Envelope, EnvelopeParts, PROFILE};
    use evidence_request::request::{Coverage, Derivation, Freshness};
    use evidencebook::derived::{derived_inputs, verify_set_inclusion};

    fn log_key() -> String {
        hex::encode(key("log").verifying_key().to_bytes())
    }

    fn pack(proofs: bool, fields: &[&str], forms: Vec<SubjectKind>) -> (Derivations, String) {
        let mut derivations = Derivations::new();
        let id = derivations
            .register(DerivationDefinition {
                document: br#"{"pack":"terms"}"#.to_vec(),
                recipients: vec!["bob".into(), "alice".into()],
                forms,
                fields: fields.iter().map(|f| (*f).into()).collect(),
                proofs,
                ..Default::default()
            })
            .unwrap();
        (derivations, id)
    }

    struct Answer {
        bytes: Vec<u8>,
        req: Request,
        env: Envelope,
    }

    fn answer(
        f: &Foreign,
        derivations: &Derivations,
        id: &str,
        subject: Value,
        anchor: &ResolvedAnchor,
    ) -> Answer {
        let wire = json!({"subject": subject, "derivation": id,
            "coverage": {"expected_pin": anchor.digest()}});
        let bytes = serde_json::to_vec(&wire).unwrap();
        let req = parse_json(&bytes).unwrap();
        let selection = select(f, f, &req, anchor, 100).unwrap();
        let env = build(
            f,
            f,
            &req,
            &bytes,
            anchor,
            &selection,
            Options {
                derivations: Some(derivations),
                requester: Some("alice"),
                ..Options::default()
            },
            &key("responder") as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
        .unwrap();
        Answer { bytes, req, env }
    }

    /// The holder signs `artifact` and `material` for the same request: a
    /// responder that lies is still a signed envelope.
    fn resign(a: &Answer, artifact: &Value, material: Vec<(String, Vec<u8>)>) -> Envelope {
        let artifact = evidence_request::jcs::to_string(artifact)
            .unwrap()
            .into_bytes();
        sign_with_profile(
            PROFILE,
            &a.bytes,
            EnvelopeParts {
                subject: a.req.subject.clone(),
                derivation: a.req.derivation.clone(),
                anchor: Anchor {
                    digest: a.env.anchor.digest.clone(),
                    freshness: None,
                },
                artifact,
                verification: material,
                issued_at: ISSUED_AT.into(),
            },
            &key("responder") as &dyn EnvelopeSigner,
        )
        .unwrap()
    }

    fn material(env: &Envelope) -> Vec<(String, Vec<u8>)> {
        env.verification
            .iter()
            .map(|v| v.kind.clone())
            .zip(env.verification_content.clone())
            .collect()
    }

    fn artifact(env: &Envelope) -> Value {
        serde_json::from_slice(&env.artifact).unwrap()
    }

    #[test]
    fn a_grant_to_several_requesters_with_a_nested_field_and_proofs_verifies() {
        let f = foreign();
        let (derivations, id) = pack(
            true,
            &["seq", "correlation.k"],
            vec![SubjectKind::Range, SubjectKind::CitingRecords],
        );
        let registered = derivations.get(&id).unwrap();
        assert_eq!(registered.grants(), ["alice", "bob"]);
        assert_eq!(registered.authorizes(Some("bob")), Some(true));
        assert_eq!(registered.authorizes(Some("carol")), Some(false));
        assert_eq!(registered.authorizes(None), Some(false));

        // Under the first checkpoint there is no earlier one: a checkpoint and a
        // proof per record; under the second, its consistency proof last.
        for (checkpoint, range, want_kinds) in [
            (
                0,
                [0, 1],
                vec![KIND_CHECKPOINT, KIND_INCLUSION, KIND_INCLUSION],
            ),
            (
                1,
                [0, 4],
                vec![
                    KIND_CHECKPOINT,
                    KIND_INCLUSION,
                    KIND_INCLUSION,
                    KIND_INCLUSION,
                    KIND_INCLUSION,
                    KIND_INCLUSION,
                    KIND_CONSISTENCY,
                ],
            ),
        ] {
            let at = pinned(&f, checkpoint);
            let a = answer(&f, &derivations, &id, json!({"range": range}), &at);
            assert_eq!(kinds(&a.env), want_kinds);
            let art = artifact(&a.env);
            // Only the declared member of correlation travels, nested.
            let first = &art["records"][0];
            assert_eq!(
                first["correlation"],
                json!({"k": format!("c-{}", range[0])})
            );
            assert_eq!(
                first.as_object().unwrap().keys().collect::<Vec<_>>(),
                ["correlation", "digest", "leaf_index", "seq"]
            );
            let verified = verify_set_inclusion(&a.env, &a.req, &log_key()).unwrap();
            assert_eq!(verified.statement, f.stated[checkpoint].statement);
            assert_eq!(verified.anchor, at.digest());
            let inputs = derived_inputs(&a.env, &a.req, &log_key()).unwrap();
            assert!(inputs.proven);
            assert_eq!(inputs.derivation, id);
            assert_eq!(inputs.records.len(), (range[1] - range[0] + 1) as usize);
            assert_eq!(
                inputs.records[0].fields.keys().collect::<Vec<_>>(),
                ["correlation", "seq"]
            );
        }
    }

    #[test]
    fn without_proofs_the_answer_carries_no_material_and_is_not_proven() {
        let f = foreign();
        let (derivations, id) = pack(false, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let a = answer(&f, &derivations, &id, json!({"range": [0, 1]}), &at);
        assert!(a.env.verification.is_empty());
        let inputs = derived_inputs(&a.env, &a.req, &log_key()).unwrap();
        assert!(!inputs.proven, "proven only from a successful check");
        assert_eq!(inputs.records.len(), 2);
        assert!(verify_set_inclusion(&a.env, &a.req, &log_key()).is_err());
    }

    #[test]
    fn a_row_that_is_added_dropped_moved_or_misplaced_fails_a_check() {
        let f = foreign();
        let (derivations, id) = pack(true, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let a = answer(&f, &derivations, &id, json!({"range": [0, 3]}), &at);
        let (art, mat) = (artifact(&a.env), material(&a.env));
        let check = |art: &Value, mat: Vec<(String, Vec<u8>)>| {
            let env = resign(&a, art, mat);
            verify_set_inclusion(&env, &a.req, &log_key())
                .unwrap_err()
                .0
        };
        // Each case is signed by the holder, so only the material can refuse it.
        let mut moved = art.clone();
        moved["records"].as_array_mut().unwrap().swap(0, 1);
        assert!(check(&moved, mat.clone()).contains("record 0"), "moved");
        let mut dropped = art.clone();
        dropped["records"].as_array_mut().unwrap().pop();
        assert!(
            check(&dropped, mat.clone()).contains("one per record"),
            "dropped"
        );
        let mut added = art.clone();
        let extra = added["records"][0].clone();
        added["records"].as_array_mut().unwrap().push(extra);
        assert!(
            check(&added, mat.clone()).contains("one per record"),
            "added"
        );
        let mut leaf = art.clone();
        leaf["records"][2]["leaf_index"] = json!(3);
        assert!(check(&leaf, mat.clone()).contains("record 2"), "wrong leaf");
        let mut digest = art.clone();
        digest["records"][1]["digest"] = art["records"][2]["digest"].clone();
        assert!(
            check(&digest, mat.clone()).contains("record 1"),
            "wrong digest"
        );
        let mut no_records = art.clone();
        no_records.as_object_mut().unwrap().remove("records");
        assert!(check(&no_records, mat.clone()).contains("no records list"));
        // The material.
        let mut flipped = mat.clone();
        let last = flipped.len() - 1;
        assert_eq!(flipped[last].0, KIND_CONSISTENCY);
        let n = flipped[last].1.len();
        flipped[last].1[n - 1] ^= 1;
        assert!(
            check(&art, flipped).contains("consistency"),
            "tampered consistency"
        );
        let mut twice = mat.clone();
        twice.push(mat[0].clone());
        assert!(check(&art, twice).contains("more than one checkpoint"));
        let mut other = mat.clone();
        other.push(("cll-unknown".into(), vec![1]));
        assert!(check(&art, other).contains("not one this requester implements"));
        // A checkpoint that is not the anchor.
        let mut elsewhere = mat.clone();
        elsewhere[0].1 = f.stated[0].statement.clone();
        assert!(check(&art, elsewhere).contains("not the anchor"));
    }

    #[test]
    fn the_pinned_key_is_the_checkpoints_and_the_envelope_is_checked_first() {
        let f = foreign();
        let (derivations, id) = pack(true, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let a = answer(&f, &derivations, &id, json!({"range": [0, 3]}), &at);
        let other_key = hex::encode(key("other").verifying_key().to_bytes());
        for pinned_key in [other_key.as_str(), ""] {
            let err = verify_set_inclusion(&a.env, &a.req, pinned_key).unwrap_err();
            assert!(err.0.contains("not the pinned key"), "{err}");
        }
        // A verification item swapped after signing breaks the envelope's own
        // binding, which is read before any material.
        let mut swapped = a.env.clone();
        swapped.verification_content[1] = swapped.verification_content[2].clone();
        let err = verify_set_inclusion(&swapped, &a.req, &log_key()).unwrap_err();
        assert!(err.0.contains("envelope does not verify"), "{err}");
        // An envelope that answers another request.
        let mut other = a.req.clone();
        other.subject = Subject::Range(0, 2);
        let err = verify_set_inclusion(&a.env, &other, &log_key()).unwrap_err();
        assert!(err.0.contains("different subject"), "{err}");
        // A stated freshness the checkpoint does not commit.
        let mut lying = a.env.clone();
        lying.anchor.freshness = Some(evidence_request::envelope::AnchorFreshness::Size(1));
        assert!(verify_set_inclusion(&lying, &a.req, &log_key()).is_err());
    }

    /// A request pinning the requester's own half is answered with the
    /// half's digest as the envelope's anchor and the serving checkpoint's in
    /// the artifact; the proofs are under the artifact's.
    #[test]
    fn a_request_pinning_its_own_half_is_checked_under_the_artifacts_anchor() {
        let f = foreign();
        let (derivations, id) = pack(true, &["seq"], vec![SubjectKind::CitingRecords]);
        let half_pin = ResolvedAnchor::ExchangeHalf(half());
        #[rustfmt::skip]
        let subject = json!({"exchange": half()}); // vocabulary-gate: request-draft wire token
        let a = answer(&f, &derivations, &id, subject, &half_pin);
        assert_eq!(a.env.anchor.digest, half());
        let art = artifact(&a.env);
        assert_ne!(art["anchor"], json!(half()));
        let verified = verify_set_inclusion(&a.env, &a.req, &log_key()).unwrap();
        assert_eq!(json!(verified.anchor), art["anchor"]);
        // A substituted artifact anchor is not the checkpoint the proofs carry.
        let mut other = art.clone();
        other["anchor"] = json!(anchor_digest(&f.stated[0]));
        let env = resign(&a, &other, material(&a.env));
        let err = verify_set_inclusion(&env, &a.req, &log_key()).unwrap_err();
        assert!(err.0.contains("not the anchor"), "{err}");
    }

    /// What Go refuses before it reads a proof (`verifyEnvelopeContent`): an
    /// answer that is not the pinned anchor or does not meet the floor, and one
    /// over the item limits.
    #[test]
    fn an_answer_that_is_not_the_requested_one_is_refused_before_its_proofs() {
        let f = foreign();
        let (derivations, id) = pack(true, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let a = answer(&f, &derivations, &id, json!({"range": [0, 3]}), &at);
        // A newer checkpoint was asked for: the holder re-signed an older one.
        let mut pin = a.req.clone();
        pin.coverage = Coverage::ExpectedPin(anchor_digest(&f.stated[0]));
        let err = verify_set_inclusion(&a.env, &pin, &log_key()).unwrap_err();
        assert!(err.0.contains("not the pinned one"), "{err}");
        assert!(derived_inputs(&a.env, &pin, &log_key()).is_err());
        // A floor above what the answer states, or not stated at all.
        let mut floor = a.req.clone();
        floor.coverage = Coverage::MinFreshness(Freshness::Size(1000));
        let err = verify_set_inclusion(&a.env, &floor, &log_key()).unwrap_err();
        assert!(err.0.contains("size meeting the floor"), "{err}");
        let mut time = a.req.clone();
        time.coverage = Coverage::MinFreshness(Freshness::Time(
            evidence_request::time::parse_utc("2030-01-01T00:00:00Z").unwrap(),
        ));
        let err = verify_set_inclusion(&a.env, &time, &log_key()).unwrap_err();
        assert!(err.0.contains("time meeting the floor"), "{err}");

        // Over the item limits: refused before one proof is checked.
        let art = artifact(&a.env);
        let mut many = art.clone();
        let row = art["records"][0].clone();
        many["records"] = Value::Array(vec![row; evidencebook::derived::MAX_ITEMS + 1]);
        let err = verify_set_inclusion(&resign(&a, &many, material(&a.env)), &a.req, &log_key())
            .unwrap_err();
        assert!(err.0.contains("limit: records"), "{err}");
        let mut padded = material(&a.env);
        padded.resize(
            evidencebook::derived::MAX_ITEMS + 1,
            (KIND_INCLUSION.into(), vec![0]),
        );
        let err = verify_set_inclusion(&resign(&a, &art, padded), &a.req, &log_key()).unwrap_err();
        assert!(err.0.contains("limit: verification"), "{err}");
    }

    /// An answer is read only once its envelope is: with no proofs at all, a
    /// row edited after signing, an envelope for another derivation or a
    /// `body` that is not the record are refused.
    #[test]
    fn rows_are_read_only_from_a_verified_envelope_and_a_body_must_be_its_record() {
        let f = foreign();
        let (derivations, id) = pack(false, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let a = answer(&f, &derivations, &id, json!({"range": [0, 1]}), &at);
        assert!(!derived_inputs(&a.env, &a.req, &log_key()).unwrap().proven);
        let mut edited = a.env.clone();
        edited.artifact = String::from_utf8(edited.artifact.clone())
            .unwrap()
            .replace("\"seq\":1", "\"seq\":9")
            .into_bytes();
        assert_ne!(edited.artifact, a.env.artifact);
        let err = derived_inputs(&edited, &a.req, &log_key()).unwrap_err();
        assert!(err.0.contains("envelope does not verify"), "{err}");
        let mut other = a.req.clone();
        other.derivation = Some(Derivation::Digest("ab".repeat(32)));
        assert!(derived_inputs(&a.env, &other, &log_key()).is_err());
        // A holder's own re-signed row with a body that is not its record.
        let mut art = artifact(&a.env);
        art["records"][0]["body"] = json!("00ff");
        let err = derived_inputs(&resign(&a, &art, vec![]), &a.req, &log_key()).unwrap_err();
        assert!(err.0.contains("does not hash to its digest"), "{err}");
        art["records"][0]["body"] = json!("not hex");
        assert!(derived_inputs(&resign(&a, &art, vec![]), &a.req, &log_key()).is_err());
        // A fraction in a field is the holder's value, read as written.
        let mut art = artifact(&a.env);
        art["records"][0]["seq"] = json!(1.5);
        let text = String::from_utf8(serde_json::to_vec(&art).unwrap()).unwrap();
        let env = {
            let mut e = a.env.clone();
            e.artifact = text.into_bytes();
            e
        };
        // Signed again as the holder would: the artifact's text, not its JCS.
        let env = sign_with_profile(
            PROFILE,
            &a.bytes,
            EnvelopeParts {
                subject: a.req.subject.clone(),
                derivation: a.req.derivation.clone(),
                anchor: Anchor {
                    digest: a.env.anchor.digest.clone(),
                    freshness: None,
                },
                artifact: env.artifact,
                verification: vec![],
                issued_at: ISSUED_AT.into(),
            },
            &key("responder") as &dyn EnvelopeSigner,
        )
        .unwrap();
        let inputs = derived_inputs(&env, &a.req, &log_key()).unwrap();
        assert_eq!(inputs.records[0].fields["seq"], json!(1.5));
    }

    /// Go reads a proof that need not be deterministic, and checks a
    /// consistency proof's right peaks exactly.
    #[test]
    fn a_proof_is_read_as_go_reads_it() {
        use ciborium::value::Value as Cbor;
        let f = foreign();
        let (derivations, id) = pack(true, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let a = answer(&f, &derivations, &id, json!({"range": [0, 3]}), &at);
        let (art, mat) = (artifact(&a.env), material(&a.env));
        let decode = |octets: &[u8]| ciborium::from_reader::<Cbor, _>(octets).unwrap();
        let verdict = |mat: Vec<(String, Vec<u8>)>| {
            verify_set_inclusion(&resign(&a, &art, mat), &a.req, &log_key())
        };
        // The first inclusion proof with its index written in two octets.
        let Cbor::Array(parts) = decode(&mat[1].1) else {
            panic!("not an array")
        };
        let (Cbor::Integer(index), path) = (&parts[0], &parts[1]) else {
            panic!("not [index, path]")
        };
        let index = u64::try_from(*index).unwrap();
        let mut wide = vec![0x82, 0x19, (index >> 8) as u8, index as u8];
        ciborium::into_writer(path, &mut wide).unwrap();
        assert_ne!(wide, mat[1].1);
        let mut widened = mat.clone();
        widened[1].1 = wide;
        verdict(widened).expect("Go accepts a proof that is not deterministic");
        // The index of another leaf is refused.
        let mut other = mat.clone();
        other[1].1 = mat[2].1.clone();
        assert!(verdict(other).unwrap_err().0.contains("record 0"));
        // Right peaks that are not the ones the paths leave out.
        let last = mat.len() - 1;
        assert_eq!(mat[last].0, KIND_CONSISTENCY);
        let Cbor::Array(mut parts) = decode(&mat[last].1) else {
            panic!("not an array")
        };
        let Cbor::Array(right) = parts[3].clone() else {
            panic!("not a list")
        };
        for changed in [
            Vec::new(),
            [right.clone(), vec![Cbor::Bytes(vec![7; 32])]].concat(),
        ] {
            parts[3] = Cbor::Array(changed);
            let mut octets = Vec::new();
            ciborium::into_writer(&Cbor::Array(parts.clone()), &mut octets).unwrap();
            let mut tampered = mat.clone();
            tampered[last].1 = octets;
            let err = verdict(tampered).unwrap_err();
            assert!(err.0.contains("consistency"), "{err}");
        }
    }

    #[test]
    fn the_proofs_and_the_checkpoint_count_against_the_item_limit() {
        let f = foreign();
        let (derivations, id) = pack(true, &["seq"], vec![SubjectKind::Range]);
        let at = pinned(&f, 1);
        let wire = json!({"subject": {"range": [0, 3]}, "derivation": id,
            "coverage": {"expected_pin": at.digest()}});
        let bytes = serde_json::to_vec(&wire).unwrap();
        let req = parse_json(&bytes).unwrap();
        let mut selection = select(&f, &f, &req, &at, 100).unwrap();
        selection.records = (0..evidencebook::answer::MAX_RECORDS - 1).collect();
        let err = build(
            &f,
            &f,
            &req,
            &bytes,
            &at,
            &selection,
            Options {
                derivations: Some(&derivations),
                requester: Some("alice"),
                ..Options::default()
            },
            &key("responder") as &dyn EnvelopeSigner,
            ISSUED_AT,
        )
        .unwrap_err();
        assert!(matches!(err, AnswerError::OverLimit), "{err:?}");
    }
}
