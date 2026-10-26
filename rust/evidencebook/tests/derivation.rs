//! Definition-digest derivations at parity with the Go book: the shared
//! fixture `testdata/derivation/vectors.json`, which the Go test
//! `TestDerivationVectors` answers from a real book, is answered here over
//! the same records and the same anchor, with the same identifiers, the same
//! refusals and the same reply octets.

use ed25519_dalek::SigningKey;
use evidence_request::envelope::{Binding, Signer as EnvelopeSigner};
use evidence_request::refusal;
use evidence_request::registry::{Reason, SubjectForm};
use evidence_request::request::{parse_json, Derivation};
use evidence_request::resolve::{self, resolve, Resolution, Responder};
use evidencebook::answer::{
    anchors, build, select, AnswerError, Options, Record, RecordSource, MAX_RECORDS,
};
use evidencebook::derivation::{DerivationDefinition, DerivationError, Derivations};
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
    reregistered: Vec<Reregistration>,
    cases: Vec<Case>,
}

/// A registered definition's document registered again (#86): `outcome` is
/// `registered` (the same identifier) or `refused`.
#[derive(Deserialize)]
struct Reregistration {
    name: String,
    definition: String,
    document: String,
    #[serde(default)]
    recipient: Option<String>,
    forms: Option<Vec<Value>>,
    fields: Vec<String>,
    outcome: String,
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
fn a_definition_go_refuses_is_refused_for_the_same_reason() {
    let v = vectors();
    let mut derivations = Derivations::new();
    let mut checked = Vec::new();
    for def in &v.rejected {
        // A form outside the subject registry has no `SubjectKind`: it
        // cannot be declared at all.
        let Some(forms) = forms(&def.forms) else {
            assert_eq!(def.name, "unknown-form");
            continue;
        };
        let got = derivations
            .register(DerivationDefinition {
                document: def.document.as_bytes().to_vec(),
                recipient: def.recipient.clone(),
                forms,
                fields: def.fields.clone().unwrap_or_default(),
            })
            .expect_err(&def.name);
        let want = match def.name.as_str() {
            "not-an-object" => matches!(got, DerivationError::NotAnObject),
            "not-json"
            | "duplicate-member"
            | "duplicate-member-escaped"
            | "too-deep"
            | "trailing-data"
            | "leading-zero"
            | "duplicate-in-private-member" => {
                matches!(got, DerivationError::NotJson(_))
            }
            "fraction" | "unsafe-integer" => matches!(got, DerivationError::Canonical(_)),
            "no-forms" | "no-fields" => got == DerivationError::Undeclared,
            "over-checkpoints" => got == DerivationError::OverCheckpoints,
            "not-a-header-member" => got == DerivationError::NotAHeaderMember("payload".into()),
            other => panic!("no expected reason for {other}"),
        };
        assert!(want, "{}: {got:?}", def.name);
        checked.push(def.name.as_str());
    }
    assert_eq!(checked.len(), v.rejected.len() - 1);
}

/// Registers each of the fixture's re-registrations, requiring Go's outcome.
fn reregister(v: &Vectors, derivations: &mut Derivations) {
    assert!(!v.reregistered.is_empty());
    for r in &v.reregistered {
        let first = &v
            .definitions
            .iter()
            .find(|d| d.name == r.definition)
            .unwrap()
            .id;
        let got = derivations.register(DerivationDefinition {
            document: r.document.as_bytes().to_vec(),
            recipient: r.recipient.clone(),
            forms: forms(&r.forms).unwrap(),
            fields: r.fields.clone(),
        });
        match r.outcome.as_str() {
            "registered" => assert_eq!(got.as_ref(), Ok(first), "{}", r.name),
            "refused" => assert_eq!(
                got,
                Err(DerivationError::Redeclared(first.clone())),
                "{}",
                r.name
            ),
            other => panic!("{}: no outcome {other}", r.name),
        }
        assert_eq!(derivations.get(first).unwrap().id(), first);
    }
}

/// The issue's example (#86): `{}` registered for alice, then for bob, is
/// refused, and alice is still the one it is answered to. A re-registration
/// whose declarations are themselves invalid is refused for that first.
#[test]
fn registering_a_document_again_never_changes_it() {
    let mut derivations = Derivations::new();
    let def =
        |recipient: Option<&str>, forms: Vec<SubjectKind>, fields: &[&str]| DerivationDefinition {
            document: b"{}".to_vec(),
            recipient: recipient.map(Into::into),
            forms,
            fields: fields.iter().map(|f| f.to_string()).collect(),
        };
    let id = derivations
        .register(def(Some("alice"), vec![SubjectKind::Correlation], &["seq"]))
        .unwrap();
    let mut same = def(
        Some("alice"),
        vec![SubjectKind::Correlation, SubjectKind::Correlation],
        &["seq", "seq"],
    );
    same.document = b" { } ".to_vec();
    assert_eq!(derivations.register(same), Ok(id.clone()));
    for other in [
        def(Some("bob"), vec![SubjectKind::Correlation], &["seq"]),
        def(None, vec![SubjectKind::Correlation], &["seq"]),
        def(Some(""), vec![SubjectKind::Correlation], &["seq"]),
        def(
            Some("alice"),
            vec![SubjectKind::Correlation, SubjectKind::Record],
            &["seq"],
        ),
        def(Some("alice"), vec![SubjectKind::Correlation], &["v"]),
    ] {
        assert_eq!(
            derivations.register(other),
            Err(DerivationError::Redeclared(id.clone()))
        );
    }
    assert_eq!(
        derivations.register(def(
            Some("alice"),
            vec![SubjectKind::Correlation],
            &["payload"]
        )),
        Err(DerivationError::NotAHeaderMember("payload".into()))
    );
    let registered = derivations.get(&id).unwrap();
    assert_eq!(registered.authorizes(Some("alice")), Some(true));
    assert_eq!(registered.authorizes(Some("bob")), Some(false));
    assert_eq!(registered.forms(), [SubjectKind::Correlation]);
    assert_eq!(registered.fields(), ["seq"]);
}

/// Every case answered as the Go responder answers it: the derivation
/// steps with Go's default share policy, resolution, then the artifact or
/// the signed refusal, reply octets for reply octets.
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
    // Go registers these before answering the cases, so a refused one must
    // leave each case answered as the first registration declared.
    reregister(&v, &mut s.derivations);
    let responder =
        SigningKey::from_bytes(&hex::decode(&v.responder_seed).unwrap().try_into().unwrap());
    let (mut answered, mut refused) = (0, 0);
    for case in &v.cases {
        let request = parse_json(case.request.as_bytes()).unwrap();
        let requester = case.requester.as_deref();
        let outcome = (|| {
            // Go's default share policy (evidencebook#114): an underived
            // subject goes only to a requester every record it names names
            // as its other party, and no record here names one, while range,
            // correlation and full_history go to no one. Only a
            // recipient-specific derivation grants its recipient more.
            if s.derivations.check(&request, requester, false)?.is_none() {
                return Err(Reason::NotAuthorized);
            }
            let anchor = match resolve(&request, &s) {
                Resolution::Artifact(anchor) => anchor,
                Resolution::Refuse(reason) => return Err(reason),
            };
            Ok(anchor)
        })();
        let go: Value = serde_json::from_str(&case.reply).unwrap();
        let issued_at = go["issued_at"].as_str().unwrap();
        let anchor = match outcome {
            Err(reason) => {
                assert_eq!(reason.token(), case.outcome, "{}", case.name);
                let request_digest = hex::encode(Sha256::digest(case.request.as_bytes()));
                let refusal =
                    refusal::sign(&request_digest, reason, issued_at, &responder).unwrap();
                let octets = evidence_request::jcs::to_string(&refusal.to_json()).unwrap();
                assert_eq!(octets, case.reply, "{}", case.name);
                refused += 1;
                continue;
            }
            Ok(anchor) => anchor,
        };
        assert_eq!(case.outcome, "artifact", "{}", case.name);
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
                requester,
                ..Options::default()
            },
            &responder as &dyn EnvelopeSigner,
            issued_at,
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
    assert_eq!((answered, refused), (2, 10));
}

/// The underived subject's artifact is the same with and without
/// registrations, is never a projection's, and carries each record's
/// octets as Go holds them. Go answers it to no one under its default share
/// policy (evidencebook#114), so it is built here from the case's request.
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
    let request = parse_json(underived.request.as_bytes()).unwrap();
    let responder = SigningKey::from_bytes(&[7; 32]);
    let artifact = |s: &Store, derivations: Option<&Derivations>| {
        let Resolution::Artifact(anchor) = resolve(&request, s) else {
            panic!("the underived subject resolves");
        };
        let selection = select(s, s, &request, &anchor, MAX_RECORDS).unwrap();
        let env = build(
            s,
            s,
            &request,
            underived.request.as_bytes(),
            &anchor,
            &selection,
            Options {
                derivations,
                ..Options::default()
            },
            &responder as &dyn EnvelopeSigner,
            "2026-10-20T00:00:00Z",
        )
        .unwrap();
        String::from_utf8(env.artifact).unwrap()
    };
    let empty = store(&v);
    let mut registered = store(&v);
    for def in &v.definitions {
        registered
            .derivations
            .register(DerivationDefinition {
                document: def.document.as_bytes().to_vec(),
                recipient: def.recipient.clone(),
                forms: forms(&def.forms).unwrap(),
                fields: def.fields.clone().unwrap(),
            })
            .unwrap();
    }
    let plain = artifact(&empty, None);
    assert_eq!(plain, artifact(&registered, Some(&registered.derivations)));
    assert_ne!(plain, projected.artifact);
    let built: Value = serde_json::from_str(&plain).unwrap();
    let records = built["records"].as_array().unwrap();
    assert!(!records.is_empty());
    for entry in records {
        let held = v
            .book
            .records
            .iter()
            .find(|r| r.digest == entry["digest"].as_str().unwrap())
            .unwrap();
        assert_eq!(entry["body"].as_str().unwrap(), held.body);
    }
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
            .check(&request, Some("alice"), false)
            .unwrap()
            .unwrap()
            .id(),
        def.id
    );
    assert_eq!(
        s.derivations.check(&request, Some("bob"), true),
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
    // The recipient is enforced by `build` itself: a caller that skipped
    // `check` still cannot hand alice's projection to anyone else.
    for requester in [None, Some("bob")] {
        assert!(matches!(
            go(
                &s,
                Options {
                    derivations: Some(&s.derivations),
                    requester,
                    ..Options::default()
                }
            ),
            Err(AnswerError::NotAuthorized)
        ));
    }
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
            requester: Some("alice"),
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
            requester: Some("alice"),
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
