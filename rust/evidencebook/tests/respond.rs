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
    SharePolicy, WindowLimiter, PROFILE_ID, PROFILE_V1,
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
    headerless: bool,
    typeless: bool,
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
            headerless: false,
            typeless: false,
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

    /// Commit one observation record, optionally naming a counterparty,
    /// held under the correlation key `deal-<counterparty>`.
    fn add(&mut self, record_type: &str, counterparty: Option<&str>) -> String {
        let key = format!("deal-{}", counterparty.unwrap_or("none"));
        self.add_under(record_type, counterparty, &key)
    }

    /// Commit one observation record held under the correlation key `key`.
    fn add_under(&mut self, record_type: &str, counterparty: Option<&str>, key: &str) -> String {
        self.ticks += 1;
        let mut header = json!({
            "v": 1, "book_id": LOG_ID, "seq": self.records.len() + 1,
            "record_type": record_type, "epistemic_type": "observed_event",
            "committed_at": self.clock(), "links": [], "subject_ref": key,
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
    fn correlation(&self, id: &str) -> Vec<u64> {
        // Exactly the key, as the store contract asks.
        self.records
            .iter()
            .filter(|(_, h)| h.get("subject_ref").and_then(Value::as_str) == Some(id))
            .map(|(r, _)| r.leaf_index)
            .collect()
    }
    fn citing(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn header_of(&self, record: &Record) -> Option<Map<String, Value>> {
        if self.headerless {
            return None;
        }
        if self.typeless {
            return Some(Map::new());
        }
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
        standard_history: None,
    };
    let opts = RespondOptions {
        authenticated_requester: who.requester,
        policy: who.policy.clone(),
    };
    let (octets, sealed) = respond(b, &responder, request, binding, &opts).unwrap();
    let reply = parse_reply(&octets, binding).expect("a reply reads back");
    if let Reply::Envelope(e) = &reply {
        e.verify_for_profile(
            asked_profile(request, binding),
            &signer.verifying_key(),
            request,
        )
        .unwrap();
    }
    (reply, sealed)
}

/// The profile a request asks for, /0 when it names none (profile §3.3
/// step 2), so an envelope is verified as its requester would.
fn asked_profile(request: &[u8], binding: Binding) -> &'static str {
    let value: Option<Value> = match binding {
        Binding::Json => serde_json::from_slice(request).ok(),
        Binding::Cbor => ciborium::from_reader(request).ok(),
    };
    match value.as_ref().and_then(|v| v["profile"].as_str()) {
        Some(p) if p == PROFILE_V1 => PROFILE_V1,
        _ => PROFILE_ID,
    }
}

fn req(subject: Value, coverage: Value) -> Vec<u8> {
    serde_json::to_vec(&json!({"subject": subject, "coverage": coverage, "profile": "evidence-interop/0", "nonce": "n"})).unwrap()
}

/// A request naming `profile`, or none when it is empty.
fn req_under(subject: Value, coverage: Value, profile: &str, nonce: &str) -> Vec<u8> {
    let mut map = json!({"subject": subject, "coverage": coverage, "nonce": nonce});
    if !profile.is_empty() {
        map["profile"] = json!(profile);
    }
    serde_json::to_vec(&map).unwrap()
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
fn range_and_full_history_are_answered_to_no_one() {
    let (mut b, _) = book();
    let policy = ResponderPolicy::default();
    let pin = json!({"expected_pin": b.pin(1)});
    for subject in [json!({"range": [0, 1]}), json!({"full_history": null})] {
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

fn tier<'a>(requester: Option<&'a str>, history_segments: &str) -> Ask<'a> {
    Ask {
        requester,
        policy: SharePolicy {
            history_segments: history_segments.into(),
            ..SharePolicy::default()
        },
    }
}

/// A correlation subject is an exact-key lookup, so it is answered under the
/// rule of a record or citing-records subject (Steven's Q1 amendment on
/// evidencebook#114): by default only to a counterparty every matching
/// record names, under `peers` to any authenticated peer, and to a stranger
/// never. Go: `TestCorrelationIsAnsweredPerRecord`.
#[test]
fn a_correlation_is_answered_per_record() {
    let (mut b, _) = book();
    b.add_under("observation", Some("alice"), "deal-both");
    b.add_under("observation", Some("bob"), "deal-both");
    b.cut();
    let policy = ResponderPolicy::default();
    let pin = json!({"expected_pin": b.pin(2)});
    let mut ask_for = |who: &Ask<'_>, key: &str| {
        run(
            &mut b,
            &policy,
            &req(json!({"correlation": key}), pin.clone()),
            Binding::Json,
            who,
        )
        .0
    };
    for t in ["counterparties", "prospective"] {
        assert!(matches!(
            ask_for(&tier(Some("alice"), t), "deal-alice"),
            Reply::Envelope(_)
        ));
        assert!(matches!(
            ask_for(&tier(Some("bob"), t), "deal-bob"),
            Reply::Envelope(_)
        ));
        for (who, key) in [
            ("alice", "deal-bob"),
            ("bob", "deal-alice"),
            ("alice", "deal-both"),
            ("bob", "deal-both"),
            ("carol", "deal-alice"),
            ("carol", "deal-never"),
        ] {
            assert_eq!(
                reason(&ask_for(&tier(Some(who), t), key)),
                Reason::NotAuthorized,
                "{t}: {who} {key}"
            );
        }
    }
    assert!(matches!(
        ask_for(&tier(Some("carol"), "peers"), "deal-both"),
        Reply::Envelope(_)
    ));
    // The key matches exactly: a prefix of every key names nothing.
    assert_eq!(
        reason(&ask_for(&tier(Some("carol"), "peers"), "deal-")),
        Reason::NoSuchSubject
    );
    assert_eq!(
        reason(&ask_for(&tier(Some("alice"), "off"), "deal-alice")),
        Reason::NotAuthorized
    );
    for t in ["off", "counterparties", "prospective", "peers"] {
        assert_eq!(
            reason(&ask_for(&tier(None, t), "deal-alice")),
            Reason::NotAuthorized
        );
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
        standard_history: None,
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
    assert_eq!(
        object["detail"],
        "profiles supported: evidence-interop/0, evidence-interop/1"
    );
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

/// evidencebook#248: a request naming `evidence-interop/1` gets the artifact
/// it would under /0 (v1 §3.3 step 3), in an envelope naming /1; one naming
/// /0 or no profile is answered under /0.
#[test]
fn evidence_interop_1_is_answered_as_0_is() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    let mut artifacts = Vec::new();
    for (asked, named) in [
        ("", PROFILE_ID),
        (PROFILE_ID, PROFILE_ID),
        (PROFILE_V1, PROFILE_V1),
    ] {
        let request = req_under(
            json!({"record": ids[0]}),
            json!({"expected_pin": b.pin(1)}),
            asked,
            &format!("n{asked}"),
        );
        let (reply, _) = run(
            &mut b,
            &policy,
            &request,
            Binding::Json,
            &ask(Some("alice")),
        );
        let Reply::Envelope(e) = reply else {
            panic!("asked {asked:?}: refused")
        };
        assert_eq!(e.profile, named, "asked {asked:?}");
        artifacts.push(e.artifact);
    }
    assert!(
        artifacts.windows(2).all(|w| w[0] == w[1]),
        "the profile changed the artifact"
    );
}

/// A limit of the book's own, the request size, the rate limiter or the
/// answer item cap, is `capacity_exceeded` under `evidence-interop/1`
/// whatever the refusal policy (ER -02 §11), and `policy_declined` under /0,
/// which registers no such token.
#[test]
fn a_limit_is_refused_for_the_requested_profile() {
    for refusal_policy in [RefusalPolicy::Specific, RefusalPolicy::Uniform] {
        for (profile, want) in [
            (PROFILE_ID, Reason::PolicyDeclined),
            (PROFILE_V1, Reason::CapacityExceeded),
            ("", Reason::PolicyDeclined),
        ] {
            let (mut b, ids) = book();
            let request = req_under(
                json!({"record": ids[0]}),
                json!({"expected_pin": b.pin(1)}),
                profile,
                "n",
            );
            let small = ResponderPolicy {
                refusal_policy,
                max_request_bytes: 10,
                ..Default::default()
            };
            let (reply, _) = run(&mut b, &small, &request, Binding::Json, &ask(Some("alice")));
            assert_eq!(reason(&reply), want, "size, {profile:?}");
            let limited = ResponderPolicy {
                refusal_policy,
                limiter: Some(Box::new(WindowLimiter::new(0, 60)) as Box<dyn RequestLimiter>),
                ..Default::default()
            };
            let (reply, _) = run(
                &mut b,
                &limited,
                &request,
                Binding::Json,
                &ask(Some("alice")),
            );
            assert_eq!(reason(&reply), want, "rate, {profile:?}");
            let capped = ResponderPolicy {
                refusal_policy,
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
            let checkpoints = req_under(
                json!({"checkpoints": null}),
                json!({"expected_pin": b.pin(1)}),
                profile,
                "n",
            );
            let (reply, _) = run(&mut b, &capped, &checkpoints, Binding::Json, &peers);
            assert_eq!(reason(&reply), want, "items, {profile:?}");
            // The same in CBOR, in core deterministic encoding (keys
            // length-first): the profile is read from the CBOR map.
            let cbor = {
                use ciborium::Value as C;
                let text = |s: &str| C::Text(s.into());
                let mut map = vec![(text("nonce"), text("n"))];
                if !profile.is_empty() {
                    map.push((text("profile"), text(profile)));
                }
                map.push((
                    text("subject"),
                    C::Map(vec![(text("checkpoints"), C::Null)]),
                ));
                map.push((
                    text("coverage"),
                    C::Map(vec![(text("min_freshness"), C::Integer(1.into()))]),
                ));
                let mut out = Vec::new();
                ciborium::into_writer(&C::Map(map), &mut out).unwrap();
                out
            };
            let (reply, _) = run(&mut b, &small, &cbor, Binding::Cbor, &ask(Some("alice")));
            assert_eq!(reason(&reply), want, "size in CBOR, {profile:?}");
        }
    }
    // A request the reader does not read as well formed asks for /0, as Go
    // reads it: one that repeats a member, or names /1 and nothing else.
    let (mut b, _) = book();
    let small = ResponderPolicy {
        max_request_bytes: 10,
        ..Default::default()
    };
    for malformed in [
        &br#"{"coverage":{"min_freshness":1},"profile":"evidence-interop/1","profile":"evidence-interop/1","subject":{"checkpoints":null}}"#[..],
        br#"{"profile":"evidence-interop/1"}"#,
    ] {
        let (reply, _) = run(&mut b, &small, malformed, Binding::Json, &ask(Some("alice")));
        assert_eq!(reason(&reply), Reason::PolicyDeclined);
    }
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
        standard_history: None,
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

fn respond_raw(b: &mut Mem, policy: &ResponderPolicy, request: &[u8], who: Option<&str>) -> Value {
    let signer = key("responder");
    let derivations = Derivations::new();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy,
        standard_history: None,
    };
    let opts = RespondOptions {
        authenticated_requester: who,
        policy: SharePolicy::default(),
    };
    let (octets, _) = respond(b, &responder, request, Binding::Json, &opts).unwrap();
    serde_json::from_slice(&octets).unwrap()
}

#[test]
fn a_request_go_would_not_read_as_well_formed_is_refused_before_anything_is_recorded() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy::default();
    let pin = b.pin(1);
    let before = (b.records.len(), b.checkpoints());
    let base = |extra: &str| {
        format!(
            r#"{{"subject":{{"record":"{}"}},"coverage":{{"expected_pin":"{pin}"}}{extra}}}"#,
            ids[0]
        )
    };
    let repeated = format!(
        r#"{{"subject":{{"record":"{}"}},"subject":{{"record":"{}"}},"coverage":{{"expected_pin":"{pin}"}}}}"#,
        ids[0], ids[0]
    );
    for bad in [
        repeated,
        base(r#","profile":7"#),
        base(r#","profile":null"#),
        base(r#","derivation":"Foo/1""#),
        base(r#","derivation":"a/b/1""#),
        base(r#","derivation":"history_card/x""#),
    ] {
        let reply = respond_raw(&mut b, &policy, bad.as_bytes(), Some("alice"));
        assert_eq!(reply["reason"], "request_malformed", "{bad}");
    }
    assert_eq!((b.records.len(), b.checkpoints()), before);
    // An empty profile names nothing, as an absent one; a registered token form is read.
    for ok in [
        base(r#","profile":"""#),
        base(r#","derivation":"history_card/1""#),
    ] {
        let reply = respond_raw(&mut b, &policy, ok.as_bytes(), Some("alice"));
        assert_ne!(reply["reason"], "request_malformed", "{ok}");
    }
}

#[test]
fn the_uniform_policy_leaves_the_derivation_checks_before_it_as_they_are() {
    let (mut b, ids) = book();
    let policy = ResponderPolicy {
        refusal_policy: RefusalPolicy::Uniform,
        ..Default::default()
    };
    let pin = b.pin(1);
    // A history card over a record is no_such_subject, not folded (Go Decide).
    let card = format!(
        r#"{{"subject":{{"record":"{}"}},"coverage":{{"expected_pin":"{pin}"}},"derivation":"history_card/1"}}"#,
        ids[0]
    );
    assert_eq!(
        respond_raw(&mut b, &policy, card.as_bytes(), Some("alice"))["reason"],
        "no_such_subject"
    );
    // An unknown token is derivation_unsupported.
    let unknown = format!(
        r#"{{"subject":{{"checkpoints":null}},"coverage":{{"expected_pin":"{pin}"}},"derivation":"other/1"}}"#
    );
    assert_eq!(
        respond_raw(&mut b, &policy, unknown.as_bytes(), Some("alice"))["reason"],
        "derivation_unsupported"
    );
}

#[test]
fn a_book_without_headers_is_an_error_not_a_leak() {
    let (mut b, ids) = book();
    b.headerless = true;
    let policy = ResponderPolicy::default();
    let signer = key("responder");
    let derivations = Derivations::new();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: None,
    };
    let request = req(json!({"record": ids[0]}), json!({"expected_pin": b.pin(1)}));
    let opts = RespondOptions {
        authenticated_requester: Some("alice"),
        policy: SharePolicy::default(),
    };
    let err = respond(&mut b, &responder, &request, Binding::Json, &opts).unwrap_err();
    assert!(err.to_string().contains("header_of"), "{err}");
}

#[test]
fn an_unreadable_clock_is_an_error() {
    let (mut b, _) = book();
    b.ticks = -1; // formats as a time no RFC 3339 reader accepts
    let policy = ResponderPolicy::default();
    let signer = key("responder");
    let derivations = Derivations::new();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: None,
    };
    let opts = RespondOptions {
        authenticated_requester: None,
        policy: SharePolicy::default(),
    };
    assert!(respond(&mut b, &responder, b"{}", Binding::Json, &opts).is_err());
}

#[test]
fn a_header_without_a_record_type_is_an_error_too() {
    let (mut b, ids) = book();
    b.typeless = true;
    let policy = ResponderPolicy::default();
    let signer = key("responder");
    let derivations = Derivations::new();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: None,
    };
    let request = req(json!({"record": ids[0]}), json!({"expected_pin": b.pin(1)}));
    let opts = RespondOptions {
        authenticated_requester: Some("alice"),
        policy: SharePolicy::default(),
    };
    assert!(respond(&mut b, &responder, &request, Binding::Json, &opts).is_err());
}

#[test]
fn a_bad_member_wins_over_a_bad_coverage_count() {
    let (mut b, _) = book();
    let policy = ResponderPolicy::default();
    for (extra, want) in [
        (r#","profile":7"#, "request_malformed"),
        (r#","derivation":"Foo/1""#, "request_malformed"),
        (r#","derivation":7"#, "request_malformed"),
        ("", "coverage_unsatisfiable"),
        (
            r#","derivation":"history_card/1""#,
            "coverage_unsatisfiable",
        ),
    ] {
        // No coverage member at all.
        let request = format!(r#"{{"subject":{{"checkpoints":null}}{extra}}}"#);
        assert_eq!(
            respond_raw(&mut b, &policy, request.as_bytes(), Some("alice"))["reason"],
            want,
            "{extra}"
        );
    }
}

/// evidencebook#275: a `history_card/1` staple, built from the book and
/// passed by the checker a reader runs; it grows no record.
#[test]
fn a_history_card_staple_is_the_books_own_answer_and_grows_no_record() {
    use evidencebook::staple::{check_staple, staple_history_card};
    let (mut b, _) = book();
    b.add("observation", Some("carol")); // past the last checkpoint: stapling cuts one
    let signer = key("responder");
    let derivations = Derivations::new();
    let policy = ResponderPolicy::default();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: None,
    };
    let holder = hex::encode(signer.verifying_key().to_bytes());
    let first = staple_history_card(&mut b, &responder).unwrap();
    let len = b.len();
    let second = staple_history_card(&mut b, &responder).unwrap();
    assert_eq!(b.len(), len, "a public card grows no record");
    let (a, c) = (
        check_staple(&first.request, &first.reply, &holder).unwrap(),
        check_staple(&second.request, &second.reply, &holder).unwrap(),
    );
    let newest = b.stated_checkpoints().unwrap().pop().unwrap();
    assert_eq!(a.anchor, evidencebook::answer::anchor_digest(&newest));
    assert_eq!(a.anchor, c.anchor);
    assert_ne!(a.nonce, c.nonce);
    assert!(check_staple(&first.request, &first.reply, &"00".repeat(32)).is_err());
}

/// The holder's own staple is not counted by the caller's limiter.
#[test]
fn a_history_card_staple_is_not_counted_by_the_limiter() {
    use evidencebook::staple::{check_staple, staple_history_card};
    let (mut b, _) = book();
    let signer = key("responder");
    let derivations = Derivations::new();
    let policy = ResponderPolicy {
        limiter: Some(Box::new(WindowLimiter::new(0, 60)) as Box<dyn RequestLimiter>),
        ..ResponderPolicy::default()
    };
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: None,
    };
    let holder = hex::encode(signer.verifying_key().to_bytes());
    let s = staple_history_card(&mut b, &responder).unwrap();
    check_staple(&s.request, &s.reply, &holder).unwrap();
}
