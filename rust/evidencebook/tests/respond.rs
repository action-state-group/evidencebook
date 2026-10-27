//! `respond` end to end over a real Checkpointed Local Log: the requester gate,
//! the `evidence_request_answered` record sealed only for an authenticated
//! requester, the checkpoint rule, the refusals and the cost policy, against
//! Go's `Respond` (`interop_respond.go`, `requester_gate_test.go`,
//! `interop_cost_test.go`, `interop_subject_scope_test.go`).

use ed25519_dalek::SigningKey;
use evidence_request::envelope::{parse_reply, Binding, Reply};
use evidence_request::registry::Reason;
use evidencebook::answer::{Record, RecordSource};
use evidencebook::ask::{Book, Entry, SealError, Sealed};
use evidencebook::derivation::Derivations;
use evidencebook::respond::{
    respond, RefusalPolicy, RequestLimiter, RespondOptions, Responder, ResponderPolicy,
    SharePolicy, WindowLimiter,
};
use evidencebook::store::CheckpointedLog;
use evidencebook::substrate::{CllSubstrate, Signer as CheckpointSigner};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

const LOG_ID: &str = "book-1";

fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

struct Mem {
    substrate: CllSubstrate,
    records: Vec<(Record, Map<String, Value>)>,
    ticks: i64,
    _dir: tempfile::TempDir,
}

impl Mem {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        let (substrate, _) = CllSubstrate::open(
            &dir.path().join("nodes"),
            &dir.path().join("checkpoints.jsonl"),
        )
        .unwrap();
        Mem {
            substrate,
            records: Vec::new(),
            ticks: 0,
            _dir: dir,
        }
    }

    fn clock(&self) -> String {
        let s = self.ticks;
        format!(
            "2026-10-27T{:02}:{:02}:{:02}Z",
            s / 3600,
            s / 60 % 60,
            s % 60
        )
    }

    /// Commit one observation record, optionally naming a counterparty.
    fn add(&mut self, record_type: &str, counterparty: Option<&str>) -> String {
        self.ticks += 1;
        let mut header = json!({
            "v": 1, "book_id": LOG_ID, "seq": self.records.len() + 1,
            "record_type": record_type, "epistemic_type": "observed_event",
            "committed_at": self.clock(), "links": [], "subject_ref": "deal-1",
        });
        if let Some(c) = counterparty {
            header["counterparty_ref"] = json!(c);
        }
        let body = serde_json::to_vec(&header).unwrap();
        let id: [u8; 32] = Sha256::digest(&body).into();
        let leaf = self.substrate.append_leaf(&id).unwrap();
        let record = Record {
            leaf_index: leaf,
            digest: hex::encode(id),
            body,
        };
        let digest = record.digest.clone();
        let Value::Object(map) = header else {
            unreachable!()
        };
        self.records.push((record, map));
        digest
    }

    fn cut(&mut self) {
        self.ticks += 1;
        let leaves = self.substrate.leaf_count().unwrap();
        let ts = self.clock();
        let prepared = self
            .substrate
            .prepare_checkpoint(leaves, LOG_ID, &ts, &key("log") as &dyn CheckpointSigner)
            .unwrap();
        self.substrate.commit_checkpoint(prepared).unwrap();
    }

    fn checkpoints(&self) -> usize {
        self.substrate.stated_checkpoints().unwrap().len()
    }

    fn pin(&self, i: usize) -> String {
        evidencebook::answer::anchor_digest(&self.substrate.stated_checkpoints().unwrap()[i])
    }

    fn answered(&self) -> Vec<Value> {
        self.records
            .iter()
            .filter(|(_, h)| h["record_type"] == "evidence_request_answered")
            .map(|(_, h)| h["statement"].clone())
            .collect()
    }
}

impl RecordSource for Mem {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        self.records
            .iter()
            .map(|(r, _)| r)
            .find(|r| r.digest == digest)
            .cloned()
    }
    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        self.records
            .get(leaf_index as usize)
            .map(|(r, _)| r.clone())
    }
    fn correlation(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn citing(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn header_of(&self, record: &Record) -> Option<Map<String, Value>> {
        self.records
            .get(record.leaf_index as usize)
            .map(|(_, h)| h.clone())
    }
}

impl CheckpointedLog for Mem {
    fn leaf_count(&self) -> Result<u64, evidencebook::substrate::SubstrateError> {
        self.substrate.leaf_count()
    }
    fn stated_checkpoints(
        &self,
    ) -> Result<
        Vec<evidencebook::substrate::StatedCheckpoint>,
        evidencebook::substrate::SubstrateError,
    > {
        self.substrate.stated_checkpoints()
    }
    fn inclusion_path(
        &self,
        i: u64,
        size: u64,
    ) -> Result<Vec<[u8; 32]>, evidencebook::substrate::SubstrateError> {
        self.substrate.inclusion_path(i, size)
    }
    fn consistency_parts(
        &self,
        from: u64,
        to: u64,
    ) -> Result<evidencebook::store::ConsistencyParts, evidencebook::substrate::SubstrateError>
    {
        self.substrate.consistency_parts(from, to)
    }
}

impl Book for Mem {
    fn len(&self) -> u64 {
        self.records.len() as u64
    }
    fn now(&self) -> String {
        self.clock()
    }
    fn seal(&mut self, entry: &Entry) -> Result<Sealed, SealError> {
        self.ticks += 1;
        let mut header = json!({
            "v": 1, "book_id": LOG_ID, "seq": self.records.len() + 1,
            "record_type": entry.record_type, "epistemic_type": entry.epistemic_type.as_str(),
            "committed_at": self.clock(),
            "links": entry.links.iter().map(|l| json!({"type": l.link_type.as_str(), "target": l.target})).collect::<Vec<_>>(),
            "statement": entry.statement,
        });
        header["statement"] = entry.statement.clone();
        let body = serde_json::to_vec(&header).unwrap();
        let id: [u8; 32] = Sha256::digest(&body).into();
        let leaf = self
            .substrate
            .append_leaf(&id)
            .map_err(|e| SealError(e.to_string()))?;
        let Value::Object(map) = header else {
            unreachable!()
        };
        self.records.push((
            Record {
                leaf_index: leaf,
                digest: hex::encode(id),
                body,
            },
            map,
        ));
        Ok(Sealed {
            record_id: hex::encode(id),
            committed_at: self.clock(),
        })
    }
    fn checkpoint(&mut self) -> Result<(), SealError> {
        self.cut();
        Ok(())
    }
}

/// Four observations (two naming alice, two bob) and a checkpoint after
/// each pair.
fn book() -> (Mem, Vec<String>) {
    let mut b = Mem::new();
    let mut ids = Vec::new();
    ids.push(b.add("observation", Some("alice")));
    ids.push(b.add("observation", Some("alice")));
    b.cut();
    ids.push(b.add("observation", Some("bob")));
    ids.push(b.add("observation", Some("bob")));
    b.cut();
    (b, ids)
}

struct Ask<'a> {
    requester: Option<&'a str>,
    policy: SharePolicy,
}

fn ask(requester: Option<&str>) -> Ask<'_> {
    Ask {
        requester,
        policy: SharePolicy::default(),
    }
}

fn run(
    b: &mut Mem,
    policy: &ResponderPolicy,
    request: &[u8],
    binding: Binding,
    who: &Ask<'_>,
) -> (Reply, Option<Sealed>) {
    let signer = key("responder");
    let derivations = Derivations::new();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy,
    };
    let opts = RespondOptions {
        authenticated_requester: who.requester,
        policy: who.policy.clone(),
    };
    let (octets, sealed) = respond(b, &responder, request, binding, &opts).unwrap();
    let reply = parse_reply(&octets, binding).expect("a reply reads back");
    if let Reply::Envelope(e) = &reply {
        e.verify_for(&signer.verifying_key(), request).unwrap();
    }
    (reply, sealed)
}

fn req(subject: Value, coverage: Value) -> Vec<u8> {
    serde_json::to_vec(&json!({"subject": subject, "coverage": coverage, "profile": "evidence-interop/0", "nonce": "n"})).unwrap()
}

fn reason(reply: &Reply) -> Reason {
    match reply {
        Reply::Refusal(r) => Reason::from_token(r["reason"].as_str().unwrap()).unwrap(),
        Reply::Envelope(_) => panic!("expected a refusal"),
    }
}

#[test]
fn a_stranger_has_only_the_public_questions_answered_and_leaves_no_record() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    let pin = json!({"expected_pin": b.pin(1)});
    let before = b.records.len();

    let (reply, sealed) = run(
        &mut b,
        &policy,
        &req(json!({"checkpoints": null}), pin.clone()),
        Binding::Json,
        &ask(None),
    );
    assert!(matches!(reply, Reply::Envelope(_)));
    assert!(sealed.is_none());

    let (reply, sealed) = run(
        &mut b,
        &policy,
        &req(json!({"record": ids[0]}), pin),
        Binding::Json,
        &ask(None),
    );
    assert_eq!(reason(&reply), Reason::NotAuthorized);
    assert!(sealed.is_none());
    assert_eq!(
        b.records.len(),
        before,
        "unauthenticated traffic never grows the log"
    );
}

#[test]
fn a_counterparty_is_answered_and_the_answer_is_sealed_without_naming_it() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    let pin = b.pin(1);
    let request = req(json!({"record": ids[0]}), json!({"expected_pin": pin}));
    let (reply, sealed) = run(
        &mut b,
        &policy,
        &request,
        Binding::Json,
        &ask(Some("alice")),
    );
    let Reply::Envelope(envelope) = reply else {
        panic!("alice was refused")
    };
    let sealed = sealed.expect("an authenticated requester's answer is sealed");

    let (record, header) = b.records.last().unwrap();
    assert_eq!(record.digest, sealed.record_id);
    assert_eq!(header["record_type"], "evidence_request_answered");
    assert_eq!(header["epistemic_type"], "observed_event");
    assert_eq!(header["links"], json!([]));
    assert_eq!(
        header["statement"],
        json!({
            "request_digest": hex::encode(Sha256::digest(&request)),
            "subject_kind": "record",
            "outcome": "artifact",
            "relationship": "counterparty",
            "anchor": envelope.anchor.digest,
            "artifact_digest": envelope.artifact_digest,
        })
    );
    assert!(
        !Value::Object(header.clone()).to_string().contains("alice"),
        "the record names no requester"
    );
}

#[test]
fn a_record_is_refused_to_a_requester_it_does_not_name_but_the_asking_is_recorded() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    let request = req(json!({"record": ids[0]}), json!({"expected_pin": b.pin(1)}));

    // Bob is a counterparty of this book, but record 0 names alice.
    let (reply, sealed) = run(&mut b, &policy, &request, Binding::Json, &ask(Some("bob")));
    assert_eq!(reason(&reply), Reason::NotAuthorized);
    assert!(sealed.is_some());
    // Carol is known to nobody: identified, not a counterparty.
    let (reply, _) = run(
        &mut b,
        &policy,
        &request,
        Binding::Json,
        &ask(Some("carol")),
    );
    assert_eq!(reason(&reply), Reason::NotAuthorized);
    let statements = b.answered();
    assert_eq!(statements[0]["relationship"], "counterparty");
    assert_eq!(statements[0]["outcome"], "refusal");
    assert_eq!(statements[0]["reason"], "not_authorized");
    assert_eq!(statements[1]["relationship"], "identified");

    // Declaring a counterparty_ref as the id grants nothing: only the
    // authenticated identity is read.
    let peers = Ask {
        requester: Some("carol"),
        policy: SharePolicy {
            history_segments: "peers".into(),
            ..SharePolicy::default()
        },
    };
    let (reply, _) = run(&mut b, &policy, &request, Binding::Json, &peers);
    assert!(matches!(reply, Reply::Envelope(_)));
    let off = Ask {
        requester: Some("alice"),
        policy: SharePolicy {
            history_segments: "off".into(),
            ..SharePolicy::default()
        },
    };
    let (reply, _) = run(&mut b, &policy, &request, Binding::Json, &off);
    assert_eq!(reason(&reply), Reason::NotAuthorized);
}

#[test]
fn range_correlation_and_full_history_are_answered_to_no_one() {
    let (mut b, _) = book();
    let policy = ResponderPolicy::default();
    let pin = json!({"expected_pin": b.pin(1)});
    for subject in [
        json!({"range": [0, 1]}),
        json!({"full_history": null}),
        json!({"correlation": "deal-1"}),
    ] {
        let (reply, _) = run(
            &mut b,
            &policy,
            &req(subject, pin.clone()),
            Binding::Json,
            &ask(Some("alice")),
        );
        assert_eq!(reason(&reply), Reason::NotAuthorized);
    }
}

#[test]
fn the_answered_records_are_never_served() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    // Seal one, then ask for it by digest as a peer: not held.
    let request = req(json!({"record": ids[0]}), json!({"expected_pin": b.pin(1)}));
    run(
        &mut b,
        &policy,
        &request,
        Binding::Json,
        &ask(Some("alice")),
    );
    let answered = b.records.last().unwrap().0.digest.clone();
    b.cut();
    let peers = Ask {
        requester: Some("alice"),
        policy: SharePolicy {
            history_segments: "peers".into(),
            ..SharePolicy::default()
        },
    };
    let latest = b.checkpoints() - 1;
    let pin = json!({"expected_pin": b.pin(latest)});
    let (reply, _) = run(
        &mut b,
        &policy,
        &req(json!({"record": answered}), pin),
        Binding::Json,
        &peers,
    );
    assert_eq!(reason(&reply), Reason::NoSuchSubject);
}

#[test]
fn only_an_authenticated_well_formed_request_may_force_a_checkpoint() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    b.add("observation", Some("alice"));
    let floor = json!({"min_freshness": 5});
    let request = req(json!({"record": ids[0]}), floor);
    let before = b.checkpoints();

    let (reply, _) = run(&mut b, &policy, &request, Binding::Json, &ask(None));
    assert_eq!(reason(&reply), Reason::NotAuthorized);
    assert_eq!(b.checkpoints(), before);

    // A floor more entries than the log holds is refused without work.
    let (reply, _) = run(
        &mut b,
        &policy,
        &req(json!({"record": ids[0]}), json!({"min_freshness": 99})),
        Binding::Json,
        &ask(Some("alice")),
    );
    assert_eq!(reason(&reply), Reason::CoverageUnsatisfiable);
    assert_eq!(b.checkpoints(), before);

    let (reply, sealed) = run(
        &mut b,
        &policy,
        &request,
        Binding::Json,
        &ask(Some("alice")),
    );
    assert!(matches!(reply, Reply::Envelope(_)));
    assert_eq!(
        b.checkpoints(),
        before + 1,
        "the authenticated request cut the checkpoint it needed"
    );
    // The answered record follows the checkpoint: the artifact it describes
    // was served under the new anchor, which does not contain it.
    assert_eq!(
        b.records.last().unwrap().0.digest,
        sealed.unwrap().record_id
    );
}

#[test]
fn a_malformed_request_is_refused_signed_and_leaves_no_record() {
    let (mut b, _) = book();
    let policy = ResponderPolicy::default();
    let before = b.records.len();
    for binding in [Binding::Json, Binding::Cbor] {
        let (reply, sealed) = run(
            &mut b,
            &policy,
            b"\xff not a request",
            binding,
            &ask(Some("alice")),
        );
        assert_eq!(reason(&reply), Reason::RequestMalformed);
        assert!(sealed.is_none());
    }
    // An unknown profile is malformed, and the refusal lists the supported ones.
    let other = serde_json::to_vec(&json!({"subject": {"checkpoints": null}, "coverage": {"min_freshness": 1}, "profile": "evidence-interop/9"})).unwrap();
    let signer = key("responder");
    let derivations = Derivations::new();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
    };
    let (octets, sealed) = respond(
        &mut b,
        &responder,
        &other,
        Binding::Json,
        &RespondOptions {
            authenticated_requester: Some("alice"),
            policy: SharePolicy::default(),
        },
    )
    .unwrap();
    let object: Value = serde_json::from_slice(&octets).unwrap();
    assert_eq!(object["reason"], "request_malformed");
    assert_eq!(object["detail"], "profiles supported: evidence-interop/0");
    assert!(sealed.is_none());
    assert_eq!(b.records.len(), before);
}

#[test]
fn a_cbor_request_is_answered_in_cbor() {
    let (mut b, _) = book();
    let policy = ResponderPolicy::default();
    let (reply, _) = run(&mut b, &policy, &[0xa0], Binding::Cbor, &ask(None));
    assert_eq!(reason(&reply), Reason::RequestMalformed);
}

#[test]
fn the_cost_policy_declines_before_any_work() {
    let (mut b, ids) = book();
    let request = req(json!({"record": ids[0]}), json!({"expected_pin": b.pin(1)}));

    let small = ResponderPolicy {
        max_request_bytes: 10,
        ..Default::default()
    };
    let (reply, sealed) = run(&mut b, &small, &request, Binding::Json, &ask(Some("alice")));
    assert_eq!(reason(&reply), Reason::PolicyDeclined);
    assert!(
        sealed.is_none(),
        "declined before the request was read: nothing is known of it"
    );

    let limited = ResponderPolicy {
        limiter: Some(Box::new(WindowLimiter::new(1, 60)) as Box<dyn RequestLimiter>),
        ..Default::default()
    };
    let (reply, _) = run(
        &mut b,
        &limited,
        &request,
        Binding::Json,
        &ask(Some("alice")),
    );
    assert!(matches!(reply, Reply::Envelope(_)));
    let (reply, _) = run(
        &mut b,
        &limited,
        &request,
        Binding::Json,
        &ask(Some("alice")),
    );
    assert_eq!(reason(&reply), Reason::PolicyDeclined);
    // Another requester has its own bucket.
    let (reply, _) = run(&mut b, &limited, &request, Binding::Json, &ask(Some("bob")));
    assert_eq!(reason(&reply), Reason::NotAuthorized);

    let capped = ResponderPolicy {
        max_answer_items: 1,
        ..Default::default()
    };
    let peers = Ask {
        requester: Some("alice"),
        policy: SharePolicy {
            history_segments: "peers".into(),
            ..SharePolicy::default()
        },
    };
    let pin = json!({"expected_pin": b.pin(1)});
    let (reply, _) = run(
        &mut b,
        &capped,
        &req(json!({"checkpoints": null}), pin),
        Binding::Json,
        &peers,
    );
    assert_eq!(
        reason(&reply),
        Reason::PolicyDeclined,
        "two checkpoints are more than one item"
    );
}

#[test]
fn the_uniform_policy_discloses_nothing_a_specific_token_would() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy {
        refusal_policy: RefusalPolicy::Uniform,
        ..Default::default()
    };
    let pin = json!({"expected_pin": b.pin(1)});
    for who in [ask(None), ask(Some("bob"))] {
        let (reply, _) = run(
            &mut b,
            &policy,
            &req(json!({"record": ids[0]}), pin.clone()),
            Binding::Json,
            &who,
        );
        assert_eq!(reason(&reply), Reason::PolicyDeclined);
    }
    let missing = hex::encode(Sha256::digest(b"missing"));
    let (reply, _) = run(
        &mut b,
        &policy,
        &req(json!({"record": missing}), pin),
        Binding::Json,
        &ask(Some("alice")),
    );
    assert_eq!(reason(&reply), Reason::PolicyDeclined);
}

#[test]
fn a_deadline_that_has_passed_is_unmet() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    let request = serde_json::to_vec(&json!({
        "subject": {"record": ids[0]}, "coverage": {"expected_pin": b.pin(1)},
        "deadline": "2026-10-27T00:00:01Z", "profile": "evidence-interop/0",
    }))
    .unwrap();
    let (reply, _) = run(
        &mut b,
        &policy,
        &request,
        Binding::Json,
        &ask(Some("alice")),
    );
    assert_eq!(reason(&reply), Reason::DeadlineUnmet);
}

#[test]
fn an_invalid_share_policy_is_an_error_not_a_refusal() {
    let (mut b, _) = book();
    let signer = key("responder");
    let derivations = Derivations::new();
    let policy = ResponderPolicy::default();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
    };
    let bad = SharePolicy {
        history_segments: "everyone".into(),
        ..SharePolicy::default()
    };
    let err = respond(
        &mut b,
        &responder,
        b"{}",
        Binding::Json,
        &RespondOptions {
            authenticated_requester: None,
            policy: bad,
        },
    )
    .unwrap_err();
    assert!(err.to_string().contains("history_segments"));
}
