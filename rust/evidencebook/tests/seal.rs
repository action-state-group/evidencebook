//! `seal`: the header and record capsule a Go book seals, byte for byte, and
//! the checks that refuse anything else.

use ed25519_dalek::SigningKey;
use evidencebook::ask::Entry;
use evidencebook::record::{EpistemicType, Link, LinkType};
use evidencebook::seal::{self, Producer, SealRecordError, DEFAULT_DEVELOPER};
use evidencebook::substrate::{SignError, Signer};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

const HOLDER_LABEL: &str = "personal-agent-history-profile-v0 conformance vectors: holder key";

fn seeded(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

fn entry(kind: &str) -> Entry {
    Entry {
        record_type: kind.into(),
        epistemic_type: EpistemicType::ProducerClaim,
        links: Vec::new(),
        statement: Value::Null,
    }
}

/// The first record of the Personal-Agent History corpus run
/// (agent-action-capsule `vectors/personal-history-basic-v0/responder-run.json`
/// at 9d653f41, `records[0]`), which Go's book sealed.
#[test]
fn seals_the_first_history_record_as_go_did() {
    let key = seeded(HOLDER_LABEL);
    let mut header = seal::header(
        "holder",
        1,
        "2026-10-15T00:00:01Z",
        &entry("step_completed"),
    )
    .unwrap();
    header.subject_ref = Some("a34e091973f2480c793ad9b9bc5aa118".into());
    header.event_time_claim = Some("2026-10-14T23:20:01Z".into());
    let producer = Producer {
        operator: "holder-operator",
        developer: DEFAULT_DEVELOPER,
        signer: &key,
    };
    let sealed = seal::seal_record(&header, &producer).unwrap();
    assert_eq!(
        sealed.record_id,
        "053075efd0faa67bb2e4a26eb0178622b5bb71c442e5a92d7ad5e926bbae7f1a"
    );
    assert_eq!(hex::encode(Sha256::digest(&sealed.body)), sealed.record_id);
    assert_eq!(
        sealed.header,
        br#"{"book_id":"holder","committed_at":"2026-10-15T00:00:01Z","epistemic_type":"producer_claim","event_time_claim":"2026-10-14T23:20:01Z","links":[],"record_type":"step_completed","seq":1,"subject_ref":"a34e091973f2480c793ad9b9bc5aa118","v":1}"#
    );
    let capsule: Value = serde_json::from_slice(&sealed.capsule).unwrap();
    assert_eq!(capsule["capsule_id"], json!(sealed.record_id));
    assert_eq!(capsule["action_id"], json!("holder#1"));
    assert_eq!(capsule["timestamp"], json!("2026-10-15T00:00:01Z"));
    assert!(
        capsule.get("references").is_none(),
        "a record with no links carries no references"
    );
    sealed.verify(&key.verifying_key().to_bytes()).unwrap();
}

#[test]
fn links_are_references_and_fractions_are_written_as_go_writes_them() {
    let key = seeded("links");
    let target = "ab".repeat(32);
    let mut e = entry("observation");
    e.links = vec![Link {
        link_type: LinkType::Cites,
        target: target.clone(),
    }];
    e.statement = json!({"n": 1});
    let header = seal::header("b", 2, "2026-10-15T00:00:01.1200009Z", &e).unwrap();
    assert_eq!(header.committed_at, "2026-10-15T00:00:01.12Z");
    let producer = Producer {
        operator: "op",
        developer: "dev",
        signer: &key,
    };
    let sealed = seal::seal_record(&header, &producer).unwrap();
    let capsule: Value = serde_json::from_slice(&sealed.capsule).unwrap();
    assert_eq!(capsule["timestamp"], json!("2026-10-15T00:00:01.120000Z"));
    assert_eq!(
        capsule["references"],
        json!([{"type": "agent-action-capsule", "digest_alg": "SHA-256", "digest": target}])
    );
    let header_value: Value = serde_json::from_slice(&sealed.header).unwrap();
    assert_eq!(header_value["statement"], json!({"n": 1}));
}

#[test]
fn the_envelope_is_capsule_emits_producer_envelope() {
    let key = seeded("envelope");
    let header = seal::header("b", 1, "2026-10-15T00:00:01Z", &entry("observation")).unwrap();
    let sealed = seal::seal_record(
        &header,
        &Producer {
            operator: "op",
            developer: "dev",
            signer: &key,
        },
    )
    .unwrap();
    let ciborium::Value::Tag(18, inner) =
        ciborium::from_reader(sealed.envelope.as_slice()).unwrap()
    else {
        panic!("not a tagged COSE_Sign1");
    };
    let ciborium::Value::Array(parts) = *inner else {
        panic!()
    };
    let mut protected = vec![0xa3, 0x03, 0x78, 0x23];
    protected.extend_from_slice(b"application/agent-action-capsule-id");
    protected.extend_from_slice(&[0x04, 0x58, 0x20]);
    protected.extend_from_slice(&key.verifying_key().to_bytes());
    protected.extend_from_slice(&[0x01, 0x27]);
    assert_eq!(parts[0], ciborium::Value::Bytes(protected));
    assert_eq!(parts[1], ciborium::Value::Map(Vec::new()));
    assert_eq!(
        parts[2],
        ciborium::Value::Bytes(hex::decode(&sealed.record_id).unwrap())
    );
}

fn sealed_record(key: &SigningKey) -> seal::SealedRecord {
    let header = seal::header("b", 1, "2026-10-15T00:00:01Z", &entry("observation")).unwrap();
    seal::seal_record(
        &header,
        &Producer {
            operator: "op",
            developer: "dev",
            signer: key,
        },
    )
    .unwrap()
}

/// `capsule` re-sealed under `key` as if its producer had signed it: the id
/// recomputed, and a valid Producer Envelope over it.
fn resealed(mut capsule: Value, header: &[u8], key: &SigningKey) -> seal::SealedRecord {
    capsule.as_object_mut().unwrap().remove("capsule_id");
    let body = evidencebook::canonical::jcs(&capsule).unwrap();
    let record_id = hex::encode(Sha256::digest(&body));
    capsule["capsule_id"] = json!(record_id);
    let id = hex::decode(&record_id).unwrap();
    let mut protected = vec![0xa3, 0x03, 0x78, 0x23];
    protected.extend_from_slice(b"application/agent-action-capsule-id");
    protected.extend_from_slice(&[0x04, 0x58, 0x20]);
    protected.extend_from_slice(&key.verifying_key().to_bytes());
    protected.extend_from_slice(&[0x01, 0x27]);
    let cbor = |v: &ciborium::Value| {
        let mut out = Vec::new();
        ciborium::into_writer(v, &mut out).unwrap();
        out
    };
    use ciborium::Value as C;
    let tbs = cbor(&C::Array(vec![
        C::Text("Signature1".into()),
        C::Bytes(protected.clone()),
        C::Bytes(Vec::new()),
        C::Bytes(id.clone()),
    ]));
    let sig = ed25519_dalek::Signer::sign(key, &tbs).to_bytes();
    let envelope = cbor(&C::Tag(
        18,
        Box::new(C::Array(vec![
            C::Bytes(protected),
            C::Map(Vec::new()),
            C::Bytes(id),
            C::Bytes(sig.to_vec()),
        ])),
    ));
    seal::SealedRecord {
        record_id,
        header: header.to_vec(),
        capsule: evidencebook::canonical::jcs(&capsule).unwrap(),
        body,
        envelope,
    }
}

#[test]
fn verify_refuses_anything_but_the_seal() {
    let key = seeded("verify");
    let pk = key.verifying_key().to_bytes();
    let s = sealed_record(&key);
    s.verify(&pk).unwrap();
    let refused = |r: &seal::SealedRecord, pk: &[u8; 32]| {
        matches!(r.verify(pk), Err(SealRecordError::Mismatch(_)))
    };

    let with = |f: &dyn Fn(&mut seal::SealedRecord)| {
        let mut r = s.clone();
        f(&mut r);
        r
    };
    assert!(
        refused(&with(&|r| r.record_id = "00".repeat(32)), &pk),
        "another id"
    );
    assert!(
        refused(&with(&|r| r.header = b"{}".to_vec()), &pk),
        "not a header"
    );
    let pretty =
        serde_json::to_vec_pretty(&serde_json::from_slice::<Value>(&s.header).unwrap()).unwrap();
    assert!(
        refused(&with(&|r| r.header = pretty.clone()), &pk),
        "a header not in JCS"
    );
    let pretty =
        serde_json::to_vec_pretty(&serde_json::from_slice::<Value>(&s.capsule).unwrap()).unwrap();
    assert!(
        refused(&with(&|r| r.capsule = pretty.clone()), &pk),
        "a capsule not in JCS"
    );
    assert!(
        refused(&with(&|r| r.body = b"{}".to_vec()), &pk),
        "another body"
    );
    let other = seeded("other");
    assert!(
        refused(&s, &other.verifying_key().to_bytes()),
        "another key"
    );
    let mut forged = s.envelope.clone();
    let last = forged.len() - 1;
    forged[last] ^= 1;
    assert!(
        refused(&with(&|r| r.envelope = forged.clone()), &pk),
        "a changed signature"
    );
    let theirs = sealed_record(&other);
    assert!(
        refused(&with(&|r| r.envelope = theirs.envelope.clone()), &pk),
        "another record's envelope"
    );
    assert!(
        refused(&with(&|r| r.envelope = b"not cbor".to_vec()), &pk),
        "not an envelope"
    );
    let mut trailing = s.envelope.clone();
    trailing.push(0);
    assert!(
        refused(&with(&|r| r.envelope = trailing.clone()), &pk),
        "bytes after the envelope"
    );

    // Capsules its producer did sign, with a valid envelope, that are not
    // the record capsule of this header.
    let capsule: Value = serde_json::from_slice(&s.capsule).unwrap();
    assert!(
        resealed(capsule.clone(), &s.header, &key)
            .verify(&pk)
            .is_ok(),
        "the helper reseals faithfully"
    );
    let mut inline = capsule.clone();
    inline["evidence_record_header"] = serde_json::from_slice(&s.header).unwrap();
    assert!(
        refused(&resealed(inline, &s.header, &key), &pk),
        "the header carried inline too"
    );
    let mut action = capsule.clone();
    action["action_type"] = json!("decision");
    assert!(
        refused(&resealed(action, &s.header, &key), &pk),
        "another action type"
    );
    let mut stamped = capsule.clone();
    stamped["timestamp"] = json!("2026-10-15T00:00:02Z");
    assert!(
        refused(&resealed(stamped, &s.header, &key), &pk),
        "a timestamp other than committed_at"
    );
    let mut operator = capsule;
    operator["operator"] = json!("someone else");
    assert!(
        resealed(operator, &s.header, &key).verify(&pk).is_ok(),
        "the operator is the producer's to name"
    );
}

#[test]
fn empty_refs_and_correlation_values_are_refused() {
    let key = seeded("empty");
    let producer = Producer {
        operator: "op",
        developer: "dev",
        signer: &key,
    };
    let good = seal::header("b", 1, "2026-10-15T00:00:01Z", &entry("observation")).unwrap();
    for set in [
        (|h: &mut evidencebook::record::Header| h.subject_ref = Some(String::new())) as fn(&mut _),
        |h| h.principal_ref = Some(String::new()),
        |h| h.counterparty_ref = Some(String::new()),
        |h| {
            h.correlation.insert("request_digest".into(), String::new());
        },
    ] {
        let mut h = good.clone();
        set(&mut h);
        assert!(matches!(
            seal::seal_record(&h, &producer),
            Err(SealRecordError::Invalid(_))
        ));
    }
}

/// A signer that answers with a key id that is not an Ed25519 key, or
/// signs under a key other than the one it names.
struct Odd {
    key_id: String,
    key: SigningKey,
}

impl Signer for Odd {
    fn sign(&self, message: &[u8]) -> Result<[u8; 64], SignError> {
        Ok(ed25519_dalek::Signer::sign(&self.key, message).to_bytes())
    }
    fn key_id(&self) -> String {
        self.key_id.clone()
    }
}

#[test]
fn seal_record_refuses_what_a_go_book_would_not_seal() {
    let key = seeded("refuse");
    let good = seal::header("b", 1, "2026-10-15T00:00:01Z", &entry("observation")).unwrap();
    let producer = Producer {
        operator: "op",
        developer: "dev",
        signer: &key,
    };
    let invalid = |r: Result<seal::SealedRecord, SealRecordError>| {
        matches!(r, Err(SealRecordError::Invalid(_)))
    };

    assert!(invalid(seal::seal_record(
        &good,
        &Producer {
            operator: " ",
            ..producer
        }
    )));
    assert!(invalid(seal::seal_record(
        &good,
        &Producer {
            developer: "",
            ..producer
        }
    )));

    let mut h = good.clone();
    h.committed_at = "2026-10-15T00:00:01.000Z".into();
    assert!(
        invalid(seal::seal_record(&h, &producer)),
        "a fraction Go would not write"
    );
    h.committed_at = "2026-10-15T00:00:01.1234567Z".into();
    assert!(
        invalid(seal::seal_record(&h, &producer)),
        "finer than a microsecond"
    );
    let mut h = good.clone();
    h.event_time_claim = Some("2026-10-15T00:00:01+00:00".into());
    assert!(invalid(seal::seal_record(&h, &producer)), "not UTC");

    let mut h = good.clone();
    h.seq = 0;
    assert!(matches!(
        seal::seal_record(&h, &producer),
        Err(SealRecordError::Record(_))
    ));

    let not_a_key = Odd {
        key_id: "zz".into(),
        key: key.clone(),
    };
    assert!(invalid(seal::seal_record(
        &good,
        &Producer {
            signer: &not_a_key,
            ..producer
        }
    )));
    let lying = Odd {
        key_id: hex::encode(seeded("named").verifying_key().to_bytes()),
        key: key.clone(),
    };
    assert!(
        invalid(seal::seal_record(
            &good,
            &Producer {
                signer: &lying,
                ..producer
            }
        )),
        "signs under another key"
    );

    assert!(matches!(
        seal::header("b", 1, "yesterday", &entry("observation")),
        Err(SealRecordError::Invalid(_))
    ));
}
