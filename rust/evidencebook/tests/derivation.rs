//! Definition-digest derivations at parity with the Go book: the shared
//! fixture `testdata/derivation/vectors.json`, which the Go test
//! `TestDerivationVectors` answers from a real book, is answered here over
//! the same records and the same anchor, with the same identifiers, the same
//! refusals and the same reply octets.

use ed25519_dalek::SigningKey;
use evidence_request::envelope::{Binding, Signer as EnvelopeSigner};
use evidence_request::registry::{Reason, SubjectForm};
use evidence_request::request::{parse_json, Derivation};
use evidence_request::resolve::{self, resolve, Resolution, Responder};
use evidencebook::answer::{
    anchors, build, select, AnswerError, Options, Record, RecordSource, MAX_RECORDS,
};
use evidencebook::derivation::{DerivationDefinition, Derivations};
use evidencebook::request::SubjectKind;
use evidencebook::store::{CheckpointedLog, ConsistencyParts};
use evidencebook::substrate::{Checkpoint, StatedCheckpoint, SubstrateError};
use serde::Deserialize;
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};

#[derive(Deserialize)]
struct Vectors {
    responder_seed: String,
    definitions: Vec<Definition>,
    book: Book,
    rejected: Vec<Definition>,
    cases: Vec<Case>,
}

#[derive(Deserialize)]
struct Definition {
    name: String,
    document: String,
    #[serde(default)]
    document_jcs: String,
    #[serde(default)]
    id: String,
    #[serde(default)]
    recipient: Option<String>,
    #[serde(default)]
    forms: Option<Vec<Value>>,
    #[serde(default)]
    fields: Option<Vec<String>>,
}

#[derive(Deserialize)]
struct Book {
    evidence_stream: String,
    anchor_statement: String,
    anchor: String,
    tree_size: u64,
    entries: u64,
    issued_at: String,
    records: Vec<VectorRecord>,
}

#[derive(Deserialize)]
struct VectorRecord {
    leaf_index: u64,
    digest: String,
    header: Map<String, Value>,
    body: String,
    correlation: String,
}

#[derive(Deserialize)]
struct Case {
    name: String,
    #[serde(default)]
    requester: Option<String>,
    request: String,
    outcome: String,
    #[serde(default)]
    artifact: String,
    #[serde(default)]
    artifact_digest: String,
    #[serde(default)]
    reply: String,
}

fn vectors() -> Vectors {
    serde_json::from_str(include_str!("../../../testdata/derivation/vectors.json")).unwrap()
}

/// The Go book under its one anchor, as the store traits read it.
struct Store {
    checkpoint: StatedCheckpoint,
    records: Vec<(Record, Map<String, Value>, String)>,
    derivations: Derivations,
}

impl CheckpointedLog for Store {
    fn leaf_count(&self) -> Result<u64, SubstrateError> {
        self.checkpoint.checkpoint.leaf_count()
    }
    fn stated_checkpoints(&self) -> Result<Vec<StatedCheckpoint>, SubstrateError> {
        Ok(vec![self.checkpoint.clone()])
    }
    // The profile defines no material for a record set or a derivation
    // (P-13), so the default answer reads no proof.
    fn inclusion_path(&self, _: u64, _: u64) -> Result<Vec<[u8; 32]>, SubstrateError> {
        unreachable!("no proof is read under the profile alone")
    }
    fn consistency_parts(&self, _: u64, _: u64) -> Result<ConsistencyParts, SubstrateError> {
        unreachable!("no proof is read under the profile alone")
    }
}

impl RecordSource for Store {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        self.records
            .iter()
            .map(|r| &r.0)
            .find(|r| r.digest == digest)
            .cloned()
    }
    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        self.records.get(leaf_index as usize).map(|r| r.0.clone())
    }
    fn correlation(&self, id: &str) -> Vec<u64> {
        self.records
            .iter()
            .filter(|r| r.2 == id)
            .map(|r| r.0.leaf_index)
            .collect()
    }
    fn citing(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn header_of(&self, record: &Record) -> Option<Map<String, Value>> {
        self.records
            .get(record.leaf_index as usize)
            .map(|r| r.1.clone())
    }
}

impl Responder for Store {
    fn holds_record(&self, digest: &str) -> bool {
        self.record_by_digest(digest).is_some()
    }
    fn holds_correlation(&self, id: &str) -> bool {
        !self.correlation(id).is_empty()
    }
    #[rustfmt::skip]
    fn cites_exchange(&self, _: &str) -> bool { false } // the "exchange" subject form; vocabulary-gate: request-draft wire token
    fn positional_ordering(&self) -> bool {
        true
    }
    fn anchors(&self) -> Vec<resolve::Anchor> {
        anchors(self).unwrap()
    }
    fn derivation_forms(&self, derivation: &Derivation) -> Option<Vec<SubjectForm>> {
        self.derivations.forms_of(derivation)
    }
}

fn forms(values: &Option<Vec<Value>>) -> Option<Vec<SubjectKind>> {
    values
        .clone()
        .unwrap_or_default()
        .into_iter()
        .map(serde_json::from_value)
        .collect::<Result<_, _>>()
        .ok()
}

fn store(v: &Vectors) -> Store {
    let checkpoint = StatedCheckpoint {
        checkpoint: Checkpoint {
            v: 1,
            kind: "cll-checkpoint".into(),
            log_id: v.book.evidence_stream.clone(),
            mmr_size: v.book.tree_size,
            root: String::new(),
            prev_size: 0,
            prev_root: String::new(),
            key_id: String::new(),
            timestamp: v.book.issued_at.clone(),
            signature: String::new(),
            witnesses: Vec::new(),
        },
        statement: hex::decode(&v.book.anchor_statement).unwrap(),
    };
    assert_eq!(checkpoint.checkpoint.leaf_count().unwrap(), v.book.entries);
    assert_eq!(
        hex::encode(Sha256::digest(&checkpoint.statement)),
        v.book.anchor
    );
    let records = v
        .book
        .records
        .iter()
        .map(|r| {
            let record = Record {
                leaf_index: r.leaf_index,
                digest: r.digest.clone(),
                body: hex::decode(&r.body).unwrap(),
            };
            (record, r.header.clone(), r.correlation.clone())
        })
        .collect();
    Store {
        checkpoint,
        records,
        derivations: Derivations::new(),
    }
}

#[test]
fn a_definition_has_the_go_identifier() {
    let v = vectors();
    let mut derivations = Derivations::new();
    for def in &v.definitions {
        let id = derivations
            .register(DerivationDefinition {
                document: def.document.as_bytes().to_vec(),
                recipient: def.recipient.clone(),
                forms: forms(&def.forms).unwrap(),
                fields: def.fields.clone().unwrap(),
            })
            .unwrap();
        assert_eq!(id, def.id, "{}", def.name);
        assert_eq!(
            hex::encode(Sha256::digest(def.document_jcs.as_bytes())),
            def.id
        );
    }
}

#[test]
fn a_definition_go_refuses_is_refused() {
    let v = vectors();
    let mut derivations = Derivations::new();
    for def in &v.rejected {
        // A form outside the subject registry has no `SubjectKind`: it
        // cannot be declared at all.
        let Some(forms) = forms(&def.forms) else {
            continue;
        };
        let registered = derivations.register(DerivationDefinition {
            document: def.document.as_bytes().to_vec(),
            recipient: def.recipient.clone(),
            forms,
            fields: def.fields.clone().unwrap_or_default(),
        });
        assert!(registered.is_err(), "{} was registered", def.name);
    }
}

/// Every case answered as the Go responder answers it: the derivation
/// steps, the share policy (here, every requester may have an unprotected
/// subject, as under the Go test's policy for these subjects), resolution,
/// then the artifact.
#[test]
fn every_case_is_the_go_answer_byte_for_byte() {
    let v = vectors();
    let mut s = store(&v);
    for def in &v.definitions {
        s.derivations
            .register(DerivationDefinition {
                document: def.document.as_bytes().to_vec(),
                recipient: def.recipient.clone(),
                forms: forms(&def.forms).unwrap(),
                fields: def.fields.clone().unwrap(),
            })
            .unwrap();
    }
    let responder =
        SigningKey::from_bytes(&hex::decode(&v.responder_seed).unwrap().try_into().unwrap());
    let mut answered = 0;
    for case in &v.cases {
        let request = parse_json(case.request.as_bytes()).unwrap();
        let requester = case.requester.as_deref();
        let outcome = (|| {
            s.derivations.check(&request, requester)?;
            let anchor = match resolve(&request, &s) {
                Resolution::Artifact(anchor) => anchor,
                Resolution::Refuse(reason) => return Err(reason),
            };
            Ok(anchor)
        })();
        let anchor = match outcome {
            Err(reason) => {
                assert_eq!(reason.token(), case.outcome, "{}", case.name);
                continue;
            }
            Ok(anchor) => anchor,
        };
        assert_eq!(case.outcome, "artifact", "{}", case.name);
        let go: Value = serde_json::from_str(&case.reply).unwrap();
        let selection = select(&s, &s, &request, &anchor, MAX_RECORDS).unwrap();
        let env = build(
            &s,
            &s,
            &request,
            case.request.as_bytes(),
            &anchor,
            &selection,
            Options {
                derivations: Some(&s.derivations),
                ..Options::default()
            },
            &responder as &dyn EnvelopeSigner,
            go["issued_at"].as_str().unwrap(),
        )
        .unwrap();
        assert_eq!(
            String::from_utf8(env.artifact.clone()).unwrap(),
            case.artifact,
            "{}",
            case.name
        );
        assert_eq!(env.artifact_digest, case.artifact_digest, "{}", case.name);
        assert_eq!(
            String::from_utf8(env.encode(Binding::Json)).unwrap(),
            case.reply,
            "{}",
            case.name
        );
        env.verify_for(&responder.verifying_key(), case.request.as_bytes())
            .unwrap();
        answered += 1;
    }
    assert_eq!(answered, 5);
}

/// The underived subject's artifact is the same with and without
/// registrations, and is never a projection's.
#[test]
fn registering_a_derivation_leaves_the_underived_artifact() {
    let v = vectors();
    let underived = v
        .cases
        .iter()
        .find(|c| c.name == "underived-correlation")
        .unwrap();
    let projected = v
        .cases
        .iter()
        .find(|c| c.name == "recipient-correlation")
        .unwrap();
    assert_ne!(underived.artifact_digest, projected.artifact_digest);
    let empty = store(&v);
    let request = parse_json(underived.request.as_bytes()).unwrap();
    let Resolution::Artifact(anchor) = resolve(&request, &empty) else {
        panic!("the underived subject resolves");
    };
    let selection = select(&empty, &empty, &request, &anchor, MAX_RECORDS).unwrap();
    let responder = SigningKey::from_bytes(&[7; 32]);
    let env = build(
        &empty,
        &empty,
        &request,
        underived.request.as_bytes(),
        &anchor,
        &selection,
        Options::default(),
        &responder as &dyn EnvelopeSigner,
        "2026-10-20T00:00:00Z",
    )
    .unwrap();
    assert_eq!(String::from_utf8(env.artifact).unwrap(), underived.artifact);
}

/// A registered derivation is answered only through the registry it was
/// registered in, over a store that holds its records' headers.
#[test]
fn a_derivation_needs_its_registry_and_its_headers() {
    let v = vectors();
    let mut s = store(&v);
    let def = &v.definitions[0];
    s.derivations
        .register(DerivationDefinition {
            document: def.document.as_bytes().to_vec(),
            recipient: def.recipient.clone(),
            forms: forms(&def.forms).unwrap(),
            fields: def.fields.clone().unwrap(),
        })
        .unwrap();
    let case = v
        .cases
        .iter()
        .find(|c| c.name == "recipient-correlation")
        .unwrap();
    let request = parse_json(case.request.as_bytes()).unwrap();
    assert_eq!(
        s.derivations
            .check(&request, Some("alice"))
            .unwrap()
            .unwrap()
            .id(),
        def.id
    );
    assert_eq!(
        s.derivations.check(&request, Some("bob")),
        Err(Reason::NotAuthorized)
    );
    let Resolution::Artifact(anchor) = resolve(&request, &s) else {
        panic!("the subject resolves");
    };
    let selection = select(&s, &s, &request, &anchor, MAX_RECORDS).unwrap();
    assert_eq!(selection.records, vec![0, 1, 2]);
    let responder = SigningKey::from_bytes(&[7; 32]);
    let go = |s: &Store, options: Options<'_>| {
        build(
            s,
            s,
            &request,
            case.request.as_bytes(),
            &anchor,
            &selection,
            options,
            &responder as &dyn EnvelopeSigner,
            "2026-10-20T00:00:00Z",
        )
    };
    assert!(matches!(
        go(&s, Options::default()),
        Err(AnswerError::DerivationUnsupported)
    ));
    let mut headless = store(&v);
    for r in &mut headless.records {
        r.1.clear();
    }
    // An empty header projects to digest and position only; no header at all
    // is an error, never an answer.
    struct NoHeaders<'a>(&'a Store);
    impl RecordSource for NoHeaders<'_> {
        fn record_by_digest(&self, d: &str) -> Option<Record> {
            self.0.record_by_digest(d)
        }
        fn record_at(&self, i: u64) -> Option<Record> {
            self.0.record_at(i)
        }
        fn correlation(&self, id: &str) -> Vec<u64> {
            self.0.correlation(id)
        }
        fn citing(&self, h: &str) -> Vec<u64> {
            self.0.citing(h)
        }
    }
    let result = build(
        &s,
        &NoHeaders(&s),
        &request,
        case.request.as_bytes(),
        &anchor,
        &selection,
        Options {
            derivations: Some(&s.derivations),
            ..Options::default()
        },
        &responder as &dyn EnvelopeSigner,
        "2026-10-20T00:00:00Z",
    );
    assert!(matches!(result, Err(AnswerError::RecordHeader)));
    let env = go(
        &headless,
        Options {
            derivations: Some(&s.derivations),
            ..Options::default()
        },
    )
    .unwrap();
    let artifact: Value = serde_json::from_slice(&env.artifact).unwrap();
    for entry in artifact["records"].as_array().unwrap() {
        let mut keys: Vec<&String> = entry.as_object().unwrap().keys().collect();
        keys.sort();
        assert_eq!(keys, ["digest", "leaf_index"]);
    }
}
