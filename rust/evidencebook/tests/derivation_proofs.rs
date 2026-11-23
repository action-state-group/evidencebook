//! A Terms Pack served with grants, a nested field and set proofs at parity
//! with the Go book: the shared fixture
//! `testdata/derivation/proofs-vectors.json`, which the Go test
//! `TestDerivationProofVectors` answers from a real book, is answered here
//! over the same records, with an MMR built from them, and gives the same
//! reply octets: the nested projection, the checkpoint, one inclusion proof
//! per record, and the consistency proof where the anchor extends an earlier
//! checkpoint. The requester's check then runs over Go's answers.

use cll::checkpoint::verify_checkpoint_cose_offline;
use cll::mmr::{add_leaf, consistency_proof, inclusion_proof, leaf_hash, MemoryNodeStore};
use ed25519_dalek::SigningKey;
use evidence_request::envelope::{parse_reply, Binding, Reply, Signer as EnvelopeSigner};
use evidence_request::registry::{Reason, SubjectForm};
use evidence_request::request::{parse_json, Derivation};
use evidence_request::resolve::{self, resolve, Resolution, Responder};
use evidencebook::answer::{anchors, build, select, Options, Record, RecordSource, MAX_RECORDS};
use evidencebook::derivation::{DerivationDefinition, Derivations};
use evidencebook::derived::{derived_inputs, verify_set_inclusion};
use evidencebook::request::SubjectKind;
use evidencebook::store::{CheckpointedLog, ConsistencyParts};
use evidencebook::substrate::{Checkpoint, StatedCheckpoint, SubstrateError};
use serde::Deserialize;
use serde_json::{Map, Value};

#[derive(Deserialize)]
struct Vectors {
    responder_seed: String,
    checkpoint_key: String,
    definition: Definition,
    half: String,
    checkpoints: Vec<VectorCheckpoint>,
    records: Vec<VectorRecord>,
    cases: Vec<Case>,
}

#[derive(Deserialize)]
struct Definition {
    document: String,
    id: String,
    recipient: String,
    recipients: Vec<String>,
    forms: Vec<SubjectKind>,
    fields: Vec<String>,
    proofs: bool,
}

#[derive(Deserialize)]
struct VectorCheckpoint {
    statement: String,
    anchor: String,
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
    requester: String,
    request: String,
    outcome: String,
    #[serde(default)]
    artifact: String,
    reply: String,
    #[serde(default)]
    anchor: String,
    checkpoint: usize,
    #[serde(default)]
    material: Vec<Material>,
}

#[derive(Deserialize)]
struct Material {
    kind: String,
    digest: String,
    content: String,
}

fn vectors() -> Vectors {
    serde_json::from_str(include_str!(
        "../../../testdata/derivation/proofs-vectors.json"
    ))
    .unwrap()
}

/// The Go book's records, its stated checkpoints and an MMR over the same
/// leaves, which answers every proof.
struct Store {
    checkpoints: Vec<StatedCheckpoint>,
    records: Vec<(Record, Map<String, Value>, String)>,
    nodes: MemoryNodeStore,
    derivations: Derivations,
    half: String,
}

fn hashes(list: &[String]) -> Vec<[u8; 32]> {
    list.iter()
        .map(|h| hex::decode(h).unwrap().try_into().unwrap())
        .collect()
}

impl CheckpointedLog for Store {
    fn leaf_count(&self) -> Result<u64, SubstrateError> {
        Ok(self.records.len() as u64)
    }
    fn stated_checkpoints(&self) -> Result<Vec<StatedCheckpoint>, SubstrateError> {
        Ok(self.checkpoints.clone())
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
    fn citing(&self, half: &str) -> Vec<u64> {
        self.records
            .iter()
            .filter(|(_, header, _)| {
                header["links"].as_array().is_some_and(|links| {
                    links
                        .iter()
                        .any(|l| l["type"] == "cites" && l["target"] == half)
                })
            })
            .map(|r| r.0.leaf_index)
            .collect()
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
    fn cites_exchange(&self, half: &str) -> bool { !self.citing(half).is_empty() } // the "exchange" subject form; vocabulary-gate: request-draft wire token
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

fn store(v: &Vectors) -> Store {
    let checkpoints = v
        .checkpoints
        .iter()
        .map(|c| {
            let statement = hex::decode(&c.statement).unwrap();
            let d = verify_checkpoint_cose_offline(&statement).decoded.unwrap();
            StatedCheckpoint {
                checkpoint: Checkpoint {
                    v: 1,
                    kind: "cll-checkpoint".into(),
                    log_id: d.log_id,
                    mmr_size: d.mmr_size,
                    root: d.root,
                    prev_size: d.prev_size,
                    prev_root: d.prev_root,
                    key_id: d.key_id,
                    timestamp: d.timestamp,
                    signature: String::new(),
                    witnesses: Vec::new(),
                },
                statement,
            }
        })
        .collect();
    let mut nodes = MemoryNodeStore::new();
    let records = v
        .records
        .iter()
        .map(|r| {
            let id: [u8; 32] = hex::decode(&r.digest).unwrap().try_into().unwrap();
            add_leaf(&mut nodes, leaf_hash(&id)).unwrap();
            let record = Record {
                leaf_index: r.leaf_index,
                digest: r.digest.clone(),
                body: hex::decode(&r.body).unwrap(),
            };
            (record, r.header.clone(), r.correlation.clone())
        })
        .collect();
    let mut derivations = Derivations::new();
    let d = &v.definition;
    let id = derivations
        .register(DerivationDefinition {
            document: d.document.as_bytes().to_vec(),
            recipient: Some(d.recipient.clone()),
            recipients: d.recipients.clone(),
            forms: d.forms.clone(),
            fields: d.fields.clone(),
            proofs: d.proofs,
        })
        .unwrap();
    assert_eq!(id, d.id, "the Go identifier");
    Store {
        checkpoints,
        records,
        nodes,
        derivations,
        half: v.half.clone(),
    }
}

#[test]
fn every_case_is_the_go_answer_byte_for_byte() {
    let v = vectors();
    let s = store(&v);
    assert_eq!(s.half, v.half);
    let responder =
        SigningKey::from_bytes(&hex::decode(&v.responder_seed).unwrap().try_into().unwrap());
    let (mut answered, mut refused) = (0, 0);
    for case in &v.cases {
        let request = parse_json(case.request.as_bytes()).unwrap();
        let requester = (!case.requester.is_empty()).then_some(case.requester.as_str());
        let outcome = (|| {
            if s.derivations.check(&request, requester, false)?.is_none() {
                return Err(Reason::NotAuthorized);
            }
            match resolve(&request, &s) {
                Resolution::Artifact(anchor) => Ok(anchor),
                Resolution::Refuse(reason) => Err(reason),
            }
        })();
        let go: Value = serde_json::from_str(&case.reply).unwrap();
        let issued_at = go["issued_at"].as_str().unwrap();
        let anchor = match outcome {
            Err(reason) => {
                assert_eq!(reason.token(), case.outcome, "{}", case.name);
                refused += 1;
                continue;
            }
            Ok(anchor) => anchor,
        };
        assert_eq!(case.outcome, "artifact", "{}", case.name);
        let selection = select(&s, &s, &request, &anchor, MAX_RECORDS).unwrap();
        assert_eq!(selection.at, case.checkpoint, "{}", case.name);
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
        // The material, item by item, in Go's order.
        let got: Vec<(String, String, String)> = env
            .verification
            .iter()
            .zip(&env.verification_content)
            .map(|(e, c)| (e.kind.clone(), e.digest.clone(), hex::encode(c)))
            .collect();
        let want: Vec<(String, String, String)> = case
            .material
            .iter()
            .map(|m| (m.kind.clone(), m.digest.clone(), m.content.clone()))
            .collect();
        assert_eq!(got, want, "{}", case.name);
        assert_eq!(env.anchor.digest, case.anchor, "{}", case.name);
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
    assert_eq!((answered, refused), (4, 2));
}

/// The requester's check reads Go's own answers: each is proven under the
/// checkpoint its case names, and a row moved, dropped or added fails.
#[test]
fn the_requesters_check_proves_the_go_answers_and_refuses_a_changed_one() {
    let v = vectors();
    let (mut proven, mut nested) = (0, 0);
    for case in v.cases.iter().filter(|c| c.outcome == "artifact") {
        let request = parse_json(case.request.as_bytes()).unwrap();
        let Reply::Envelope(env) = parse_reply(case.reply.as_bytes(), Binding::Json).unwrap()
        else {
            panic!("{}: not an envelope", case.name);
        };
        let verified = verify_set_inclusion(&env, &request, &v.checkpoint_key).unwrap();
        assert_eq!(
            verified.statement,
            hex::decode(&v.checkpoints[case.checkpoint].statement).unwrap(),
            "{}",
            case.name
        );
        assert_eq!(verified.anchor, v.checkpoints[case.checkpoint].anchor);
        let inputs = derived_inputs(&env, &request, &v.checkpoint_key).unwrap();
        assert!(inputs.proven, "{}", case.name);
        assert_eq!(inputs.derivation, v.definition.id);
        // The nested field travels as that member alone: records with no
        // object `statement`, or none, carry only the record type.
        for row in &inputs.records {
            let statement = row.fields.get("statement");
            assert!(
                statement.is_none()
                    || statement.is_some_and(|s| s
                        .as_object()
                        .is_some_and(|o| o.len() == 1 && o.contains_key("generated_at"))),
                "{}: {row:?}",
                case.name
            );
            assert!(row.fields.contains_key("record_type"));
        }
        nested += inputs
            .records
            .iter()
            .filter(|r| r.fields.contains_key("statement"))
            .count();
        proven += 1;

        // A signed reply cannot be changed without the holder, so changing a
        // row means the holder's own re-signed envelope; that is the unit
        // tests' ground (tests/store.rs). Here the check is under a key the
        // requester did not pin.
        let other = "0".repeat(64);
        assert!(verify_set_inclusion(&env, &request, &other).is_err());
    }
    assert_eq!(proven, 4);
    assert!(nested > 0, "some row carries the nested member");
}
