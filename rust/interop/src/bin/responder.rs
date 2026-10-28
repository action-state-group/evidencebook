// SPDX-License-Identifier: Apache-2.0
//! The responder side of the evidence interoperability profile's
//! conformance vectors, answered by this crate.
//!
//! ```text
//! cargo run -p evidencebook-interop --bin responder -- <dir> <out.json>
//! ```
//!
//! It makes the calls `test/interop/responder/main.go` makes, in the same
//! order, with the same keys and clock, and writes the same members of its
//! output for `test/interop/python-profile/verify_responder.py` to check.
//! The crate answers over the native binding only, so each request Go sends
//! over A2A or MCP is answered natively here (Go's A2A and MCP handlers make
//! that one Respond call), the book's state stays Go's, and the output
//! carries no carriage, manifest, Agent Card or MCP members: the verifier
//! skips the carriage cases. Every reply it does write is the bytes Go's run
//! writes. `test/interop/python-evidencebook/responder.py` is the same port
//! in Python.
//!
//! The keys are test keys. Never use them for anything else.

use std::path::Path;

use ciborium::Value as Cbor;
use evidence_request::envelope::{parse_reply, Binding, Reply};
use evidence_request::time::UtcTime;
use evidencebook::answer::anchor_digest;
use evidencebook::ask::{self, AskedRequest, Entry, SentRequest};
use evidencebook::derivation::Derivations;
use evidencebook::record::{EpistemicType, Link, LinkType};
use evidencebook::respond::{
    respond, RequestLimiter, RespondOptions, Responder, ResponderPolicy, SharePolicy,
};
use evidencebook_interop::{epoch, go_marshal_indent, rfc3339, seeded, sha256_hex, Clock, MemBook};
use serde_json::{json, Map, Value};

const RESPONDER_LABEL: &str =
    "draft-mih-agent-evidence-request-00 conformance vectors: primary responder key";
const LOG_LABEL: &str = "evidence-interop/0 worked example: CLL log key";
const LOG_ID: &str = "evidence-stream-1";
const PROFILE: &str = "evidence-interop/0";
const MEDIA_TYPE_JSON: &str = "application/json";
const MEDIA_TYPE_CBOR: &str = "application/cbor";

struct LimitNone;

impl RequestLimiter for LimitNone {
    fn allow(&self, _: Option<&str>, _: UtcTime) -> bool {
        false
    }
}

/// A JSON value in core deterministic CBOR (RFC 8949 §4.2.1): map keys in
/// the bytewise order of their encodings, so a shorter key first.
fn det_cbor(v: &Value) -> Cbor {
    match v {
        Value::Null => Cbor::Null,
        Value::Bool(b) => Cbor::Bool(*b),
        Value::Number(n) => Cbor::Integer(n.as_u64().expect("an unsigned integer").into()),
        Value::String(s) => Cbor::Text(s.clone()),
        Value::Array(items) => Cbor::Array(items.iter().map(det_cbor).collect()),
        Value::Object(members) => {
            let mut keys: Vec<&String> = members.keys().collect();
            keys.sort_by(|a, b| (a.len(), a.as_bytes()).cmp(&(b.len(), b.as_bytes())));
            Cbor::Map(
                keys.into_iter()
                    .map(|k| (Cbor::Text(k.clone()), det_cbor(&members[k])))
                    .collect(),
            )
        }
    }
}

/// A request's octets in `media_type`, and that media type.
struct Message {
    octets: Vec<u8>,
    media_type: &'static str,
}

impl Message {
    fn binding(&self) -> Binding {
        if self.media_type == MEDIA_TYPE_JSON {
            Binding::Json
        } else {
            Binding::Cbor
        }
    }

    /// Octets as the vectors write them: JSON as text, CBOR as hex.
    fn carry(&self) -> Value {
        if self.media_type == MEDIA_TYPE_JSON {
            json!({"media_type": self.media_type, "octets_text": String::from_utf8(self.octets.clone()).unwrap()})
        } else {
            json!({"media_type": self.media_type, "octets_hex": hex::encode(&self.octets)})
        }
    }
}

fn encode(value: &Value, media_type: &'static str) -> Message {
    let octets = if media_type == MEDIA_TYPE_JSON {
        evidencebook::canonical::jcs(value).expect("a JCS value")
    } else {
        let mut out = Vec::new();
        ciborium::into_writer(&det_cbor(value), &mut out).unwrap();
        out
    };
    Message { octets, media_type }
}

fn request(
    subject: Value,
    pin: &str,
    nonce: &str,
    profile: &str,
    media_type: &'static str,
) -> Message {
    encode(
        &json!({"subject": subject, "coverage": {"expected_pin": pin}, "nonce": nonce, "profile": profile}),
        media_type,
    )
}

struct Answered {
    message: Message,
    artifact: Option<Vec<u8>>,
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let [dir, out_path] = args.as_slice() else {
        eprintln!("usage: responder <dir> <out.json>");
        std::process::exit(2);
    };
    let out_dir = Path::new(dir);
    let clock = Clock::at(epoch(2026, 10, 12));
    let open_book = |id: &str| {
        MemBook::open(
            id,
            &format!("{id}-operator"),
            seeded(RESPONDER_LABEL),
            LOG_ID,
            seeded(LOG_LABEL),
            clock.clone(),
        )
    };
    let observation = |i: usize, links: Vec<Link>| {
        (
            Entry {
                record_type: "observation".into(),
                epistemic_type: EpistemicType::ObservedEvent,
                links,
                statement: Value::Null,
            },
            format!("order-{i}"),
        )
    };

    let mut book = open_book("responder");
    let mut records = Vec::new();
    for i in 0..3 {
        let (entry, subject_ref) = observation(i, Vec::new());
        records.push(
            book.append_with(&entry, |h| h.subject_ref = Some(subject_ref))
                .unwrap()
                .record_id,
        );
    }
    let earlier = book.cut().unwrap();
    // The later records cite the third, so the exchange of the third is a record set.
    for i in 3..7 {
        let (entry, subject_ref) = observation(
            i,
            vec![Link {
                link_type: LinkType::Cites,
                target: records[2].clone(),
            }],
        );
        records.push(
            book.append_with(&entry, |h| h.subject_ref = Some(subject_ref))
                .unwrap()
                .record_id,
        );
    }
    let pin = anchor_digest(&book.cut().unwrap());
    let subject = json!({"record": records[2]});

    let signer = seeded(RESPONDER_LABEL);
    let derivations = Derivations::new();
    let default_policy = ResponderPolicy::default();
    let declining_policy = ResponderPolicy {
        limiter: Some(Box::new(LimitNone)),
        ..ResponderPolicy::default()
    };
    // The vectors' requesters are no record's counterparty: answer under the peers tier.
    let peers = SharePolicy {
        history_segments: "peers".into(),
        ..SharePolicy::default()
    };
    let answer =
        |on: &mut MemBook, policy: &ResponderPolicy, m: &Message, requester: &str| -> Answered {
            let responder = Responder {
                signer: &signer,
                derivations: &derivations,
                policy,
            };
            let opts = RespondOptions {
                authenticated_requester: Some(requester),
                policy: peers.clone(),
            };
            let (octets, _) =
                respond(on, &responder, &m.octets, m.binding(), &opts).expect("respond");
            let artifact = match parse_reply(&octets, m.binding()).expect("a reply") {
                Reply::Envelope(e) => Some(e.artifact),
                Reply::Refusal(_) => None,
            };
            Answered {
                message: Message {
                    octets,
                    media_type: m.media_type,
                },
                artifact,
            }
        };

    let log_keys: Vec<Value> = book
        .checkpoints()
        .iter()
        .map(|cp| {
            let c = &cp.checkpoint;
            json!({"iss": c.log_id, "kid": c.key_id, "sub": format!("{}#{}", c.log_id, c.mmr_size)})
        })
        .collect();
    let mut exchanges = Map::new();
    let mut invariance = Map::new();
    let mut record_sets = Map::new();
    let mut others = Map::new();

    // Request C (JSON) by alice and request D (CBOR, another nonce) by bob.
    let req_c = request(
        subject.clone(),
        &pin,
        "2026-10-12T00:00:10Z/c",
        PROFILE,
        MEDIA_TYPE_JSON,
    );
    let req_d = request(
        subject.clone(),
        &pin,
        "2026-10-12T00:00:20Z/d",
        PROFILE,
        MEDIA_TYPE_CBOR,
    );
    let reply_c = answer(&mut book, &default_policy, &req_c, "alice");
    let reply_d = answer(&mut book, &default_policy, &req_d, "bob");
    let (Some(artifact_c), Some(artifact_d)) = (&reply_c.artifact, &reply_d.artifact) else {
        panic!("requests C and D were refused");
    };
    exchanges.insert(
        "valid-exchange".into(),
        json!({"requests": [req_c.carry()], "replies": [reply_c.message.carry()]}),
    );
    exchanges.insert(
        "invariance-two-nonces-two-transports".into(),
        json!({"requests": [req_c.carry(), req_d.carry()], "replies": [reply_c.message.carry(), reply_d.message.carry()]}),
    );
    invariance.insert("alice-json-nonce-c".into(), json!(hex::encode(artifact_c)));
    invariance.insert("bob-cbor-nonce-d".into(), json!(hex::encode(artifact_d)));
    std::fs::write(out_dir.join("artifact-alice-json-nonce-c.bin"), artifact_c).unwrap();
    std::fs::write(out_dir.join("artifact-bob-cbor-nonce-d.bin"), artifact_d).unwrap();

    // Request R, request C in CBOR: Go sends it natively, over MCP and over A2A.
    let r = request(
        subject.clone(),
        &pin,
        "2026-10-12T00:00:10Z/c",
        PROFILE,
        MEDIA_TYPE_CBOR,
    );
    for _ in 0..3 {
        assert!(
            answer(&mut book, &default_policy, &r, "alice")
                .artifact
                .is_some(),
            "request R was refused"
        );
    }

    // The same two variations for a record-set subject: an exchange.
    for (name, nonce, media_type, requester) in [
        (
            "exchange-alice-json-nonce-c",
            "2026-10-12T00:00:10Z/c",
            MEDIA_TYPE_JSON,
            "alice",
        ),
        (
            "exchange-bob-cbor-nonce-d",
            "2026-10-12T00:00:20Z/d",
            MEDIA_TYPE_CBOR,
            "bob",
        ),
    ] {
        let m = request(
            json!({"exchange": records[2]}),
            &pin,
            nonce,
            PROFILE,
            media_type,
        );
        let artifact = answer(&mut book, &default_policy, &m, requester)
            .artifact
            .expect("an exchange request was refused");
        record_sets.insert(name.into(), json!(hex::encode(artifact)));
    }

    // A record this book does not hold: no_such_subject.
    let missing = request(
        json!({"record": sha256_hex(b"never recorded")}),
        &pin,
        "2026-10-12T00:00:10Z/c",
        PROFILE,
        MEDIA_TYPE_JSON,
    );
    let reply = answer(&mut book, &default_policy, &missing, "alice");
    exchanges.insert(
        "missing-evidence-no-such-subject".into(),
        json!({"requests": [missing.carry()], "replies": [reply.message.carry()]}),
    );

    // A profile this book does not implement: request_malformed.
    let v9 = request(
        subject.clone(),
        &pin,
        "2026-10-12T00:00:40Z/v9",
        "evidence-interop/9",
        MEDIA_TYPE_JSON,
    );
    let reply = answer(&mut book, &default_policy, &v9, "alice");
    exchanges.insert(
        "unsupported-version-refused".into(),
        json!({"requests": [v9.carry()], "replies": [reply.message.carry()]}),
    );

    // The requests the envelope mutations are paired with.
    others.insert(
        "another-record".into(),
        request(
            json!({"record": records[1]}),
            &pin,
            "2026-10-12T00:00:10Z/c",
            PROFILE,
            MEDIA_TYPE_JSON,
        )
        .carry(),
    );
    others.insert(
        "another-pin".into(),
        request(
            subject.clone(),
            &anchor_digest(&earlier),
            "2026-10-12T00:00:10Z/c",
            PROFILE,
            MEDIA_TYPE_JSON,
        )
        .carry(),
    );

    // The same request C to a responder whose cost policy declines it.
    let mut declining = open_book("responder");
    let reply = answer(&mut declining, &declining_policy, &req_c, "alice");
    exchanges.insert(
        "declined".into(),
        json!({"requests": [req_c.carry()], "replies": [reply.message.carry()]}),
    );

    // A requester's own absence record: request C sent from another book, no reply in a one-minute window.
    let mut requester = open_book("responder");
    let ask_for = |requester: &mut MemBook, nonce: &str, route: &str| -> SentRequest {
        let asked = AskedRequest {
            subject: subject.clone(),
            coverage: json!({"expected_pin": pin}),
            derivation: None,
            deadline: None,
            route: Some(route.into()),
            nonce: Some(nonce.into()),
        };
        ask::request(requester, &asked, "responder").expect("request")
    };
    let absence_after_window = |requester: &mut MemBook, sent: &SentRequest| -> Value {
        clock.advance(120);
        let sealed = ask::record_absence(requester, &sent.record_id, &rfc3339(clock.get() - 1), "")
            .expect("absence");
        let held = requester
            .records
            .iter()
            .find(|h| h.record.digest == sealed.record_id)
            .unwrap();
        let statement = held.header.statement.clone().unwrap_or(Value::Null);
        encode(&statement, MEDIA_TYPE_JSON).carry()
    };
    let sent = ask_for(
        &mut requester,
        "2026-10-12T00:00:10Z/c",
        "peer:example-responder",
    );
    let sent_message = Message {
        octets: sent.bytes.clone(),
        media_type: MEDIA_TYPE_JSON,
    };
    let absence = absence_after_window(&mut requester, &sent);
    exchanges.insert(
        "missing-evidence-recorded-absence".into(),
        json!({"requests": [sent_message.carry()], "replies": [], "absence_record": absence}),
    );
    // Go sends the next two requests over MCP and A2A, then records their absences.
    for (nonce, route) in [
        ("2026-10-12T00:00:30Z/m", "mcp:example-responder"),
        ("2026-10-12T00:00:30Z/a", "a2a:example-responder"),
    ] {
        let sent = ask_for(&mut requester, nonce, route);
        answer(
            &mut book,
            &default_policy,
            &Message {
                octets: sent.bytes.clone(),
                media_type: MEDIA_TYPE_JSON,
            },
            "alice",
        );
        absence_after_window(&mut requester, &sent);
    }

    // Go writes its maps in key order.
    let sorted = |m: Map<String, Value>| {
        Value::Object(
            m.into_iter()
                .collect::<std::collections::BTreeMap<_, _>>()
                .into_iter()
                .collect(),
        )
    };
    let out = json!({
        "responder_key": hex::encode(signer.verifying_key().to_bytes()),
        "log_keys": log_keys,
        "exchanges": sorted(exchanges),
        "invariance_artifacts_hex": sorted(invariance),
        "invariance_record_set_artifacts_hex": sorted(record_sets),
        "other_requests": sorted(others),
    });
    std::fs::write(out_path, go_marshal_indent(&out, " ")).unwrap();
}
