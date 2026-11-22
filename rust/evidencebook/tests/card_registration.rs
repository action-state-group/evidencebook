//! Card registration (agent-action-capsule `spec/standard-history-v0.md` §13)
//! against the vectors the Go side runs (`testdata/card-registration/`, a copy
//! of AAC's `card-registration-v0`) and the Go tests' cases.

use ciborium::value::Value as Cbor;
use ed25519_dalek::SigningKey;
use evidence_request::envelope::{Anchor, Envelope, Signer};
use evidencebook::card_registration::{
    attach_card_receipt, build_card_statement, check_card_registered, register_card,
    CardRegistrationError, CardStatementRegistrar, CARD_REGISTERED_METHOD, MAX_CARD_RECEIPTS,
};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::cell::RefCell;

fn corpus() -> Value {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../testdata/card-registration/vectors.json"
    );
    serde_json::from_slice(&std::fs::read(path).expect("vectors")).expect("valid JSON")
}

fn hex_of(v: &Value) -> Vec<u8> {
    hex::decode(v.as_str().expect("hex string")).expect("hex")
}

fn policy_of(corpus: &Value, case: &Value) -> Value {
    match &case["policy"] {
        Value::String(name) => corpus["policies"][name.as_str()].clone(),
        inline => inline.clone(),
    }
}

fn find(corpus: &Value, name: &str) -> Value {
    corpus["cases"]
        .as_array()
        .unwrap()
        .iter()
        .find(|c| c["name"] == name)
        .unwrap_or_else(|| panic!("no vector case {name}"))
        .clone()
}

fn envelope(responder: &str, anchor: &str) -> Envelope {
    Envelope {
        profile: String::new(),
        request_digest: String::new(),
        subject: evidence_request::request::Subject::FullHistory,
        derivation: None,
        anchor: Anchor {
            digest: anchor.to_string(),
            freshness: None,
        },
        artifact_digest: String::new(),
        verification: Vec::new(),
        responder: responder.to_string(),
        issued_at: String::new(),
        sig: String::new(),
        artifact: Vec::new(),
        verification_content: Vec::new(),
    }
}

/// The vectors' holder key, derived from a public label.
fn vector_holder() -> SigningKey {
    let seed: [u8; 32] = Sha256::digest(b"card-registration-v0 key holder").into();
    SigningKey::from_bytes(&seed)
}

fn decode(octets: &[u8]) -> Cbor {
    ciborium::de::from_reader(octets).expect("cbor")
}

fn encode(v: &Cbor) -> Vec<u8> {
    let mut out = Vec::new();
    ciborium::ser::into_writer(v, &mut out).expect("cbor");
    out
}

fn parts(statement: &[u8]) -> Vec<Cbor> {
    match decode(statement) {
        Cbor::Tag(18, inner) => match *inner {
            Cbor::Array(items) => items,
            _ => panic!("not an array"),
        },
        _ => panic!("not tag 18"),
    }
}

fn rebuild(items: Vec<Cbor>) -> Vec<u8> {
    encode(&Cbor::Tag(18, Box::new(Cbor::Array(items))))
}

/// A vector statement's Receipts, and the same statement with an empty
/// unprotected map, the form `build_card_statement` signs.
fn split_receipts(statement: &[u8]) -> (Vec<Vec<u8>>, Vec<u8>) {
    let mut items = parts(statement);
    let receipts = match &items[1] {
        Cbor::Map(m) => match m.first() {
            Some((_, Cbor::Array(list))) => list
                .iter()
                .map(|r| match r {
                    Cbor::Bytes(b) => b.clone(),
                    _ => panic!("receipt"),
                })
                .collect(),
            _ => Vec::new(),
        },
        _ => panic!("unprotected"),
    };
    items[1] = Cbor::Map(Vec::new());
    (receipts, rebuild(items))
}

fn report_json(r: &agent_action_capsule::witness::CardRegisteredReport) -> Value {
    json!({
        "status": r.status,
        "statements": r.statements.iter().map(|s| json!({
            "status": s.status,
            "service": s.service,
            "registration_time": s.registration_time,
        })).collect::<Vec<_>>(),
        "service": r.service,
        "registration_time": r.registration_time,
    })
}

#[test]
fn card_registered_vectors() {
    let corpus = corpus();
    let cases = corpus["cases"].as_array().expect("cases");
    assert!(!cases.is_empty());
    for case in cases {
        let statements: Vec<Vec<u8>> = case["statements"]
            .as_array()
            .unwrap()
            .iter()
            .map(hex_of)
            .collect();
        let env = envelope(
            case["holder"].as_str().unwrap(),
            case["anchor"].as_str().unwrap(),
        );
        let report = check_card_registered(
            &hex_of(&case["card"]),
            &env,
            &statements,
            &policy_of(&corpus, case),
        );
        assert_eq!(report.method, CARD_REGISTERED_METHOD);
        // Compare as plain JSON values: arbitrary_precision keeps number text.
        let expected: Value = serde_json::from_str(&case["expected"].to_string()).unwrap();
        let observed: Value = serde_json::from_str(&report_json(&report).to_string()).unwrap();
        assert_eq!(observed, expected, "{}", case["name"]);
    }
}

/// Signs the vectors' card with the vectors' holder key and gets, byte for
/// byte, the vector statement with its Receipts removed; attaching the
/// vector's Receipt gives the vector statement.
#[test]
fn build_matches_the_vector_byte_for_byte() {
    let corpus = corpus();
    let c = find(&corpus, "pass-eddsa-time");
    let card = hex_of(&c["card"]);
    let vector = hex_of(&c["statements"][0]);
    let (receipts, bare) = split_receipts(&vector);
    assert_eq!(receipts.len(), 1);
    let signer = vector_holder();
    let holder = c["holder"].as_str().unwrap();
    let anchor = c["anchor"].as_str().unwrap();
    assert_eq!(Signer::key_id(&signer), holder);

    let built = build_card_statement(&signer, &card, anchor).unwrap();
    assert_eq!(built, bare);

    let attached = attach_card_receipt(
        &card,
        holder,
        anchor,
        &built,
        &receipts[0],
        &policy_of(&corpus, &c),
    )
    .unwrap();
    assert_eq!(attached, vector);
}

#[test]
fn build_errors() {
    let signer = vector_holder();
    let anchor = "00".repeat(32);
    for (card, anchor) in [
        (&b""[..], anchor.as_str()),
        (b"card", "NOT-HEX"),
        (b"card", &anchor[..62]),
        (b"card", &"AB".repeat(32)),
    ] {
        let err = build_card_statement(&signer, card, anchor).unwrap_err();
        assert!(matches!(err, CardRegistrationError::Invalid(_)), "{err}");
    }
}

fn attach_err(
    card: &[u8],
    holder: &str,
    anchor: &str,
    statement: &[u8],
    receipt: &[u8],
    policy: &Value,
) -> String {
    match attach_card_receipt(card, holder, anchor, statement, receipt, policy) {
        Err(CardRegistrationError::Invalid(m)) => m,
        other => panic!("want Invalid, got {other:?}"),
    }
}

#[test]
fn attach_refuses() {
    let corpus = corpus();
    let c = find(&corpus, "pass-eddsa-time");
    let card = hex_of(&c["card"]);
    let holder = c["holder"].as_str().unwrap();
    let anchor = c["anchor"].as_str().unwrap();
    let policy = policy_of(&corpus, &c);
    let (receipts, bare) = split_receipts(&hex_of(&c["statements"][0]));
    let other_receipt =
        |name: &str| split_receipts(&hex_of(&find(&corpus, name)["statements"][0])).0[0].clone();

    attach_err(
        &card,
        holder,
        anchor,
        &bare,
        &other_receipt("receipt-unlisted-service"),
        &policy,
    );
    attach_err(
        &card,
        holder,
        anchor,
        &bare,
        &other_receipt("receipt-over-card-digest"),
        &policy,
    );
    attach_err(&card, holder, anchor, &bare, b"not a receipt", &policy);
    attach_err(&card, holder, anchor, &bare, &receipts[0], &json!({}));
    attach_err(
        &card,
        holder,
        anchor,
        &bare,
        &receipts[0],
        &json!({"witnesses": []}),
    );
    attach_err(
        b"another card",
        holder,
        anchor,
        &bare,
        &receipts[0],
        &policy,
    );
    attach_err(&card, holder, anchor, &[0xa0], &receipts[0], &policy);
    // Octets after the statement are not a statement.
    let mut trailing = bare.clone();
    trailing.push(0x00);
    attach_err(&card, holder, anchor, &trailing, &receipts[0], &policy);
    // A statement over the size bound.
    let big = vec![0u8; evidencebook::card_registration::CARD_STATEMENT_MAX_LEN + 1];
    assert!(attach_err(&card, holder, anchor, &big, &receipts[0], &policy).contains("over"));

    let extra = find(&corpus, "unprotected-extra");
    let m = attach_err(
        &hex_of(&extra["card"]),
        extra["holder"].as_str().unwrap(),
        extra["anchor"].as_str().unwrap(),
        &hex_of(&extra["statements"][0]),
        &receipts[0],
        &policy,
    );
    assert!(m.contains("unprotected label"), "{m}");

    let mut statement = bare;
    for i in 0..MAX_CARD_RECEIPTS {
        statement = attach_card_receipt(&card, holder, anchor, &statement, &receipts[0], &policy)
            .unwrap_or_else(|e| panic!("Receipt {}: {e}", i + 1));
    }
    let m = attach_err(&card, holder, anchor, &statement, &receipts[0], &policy);
    assert!(m.contains("at most 16"), "{m}");
}

struct Stub {
    receipt: Result<Vec<u8>, String>,
    posted: RefCell<Vec<Vec<u8>>>,
}

impl CardStatementRegistrar for Stub {
    fn register_statement(
        &self,
        statement: &[u8],
    ) -> Result<Vec<u8>, Box<dyn std::error::Error + Send + Sync>> {
        self.posted.borrow_mut().push(statement.to_vec());
        self.receipt.clone().map_err(Into::into)
    }
}

fn stub(receipt: Result<Vec<u8>, String>) -> Stub {
    Stub {
        receipt,
        posted: RefCell::new(Vec::new()),
    }
}

#[test]
fn register_card_cases() {
    let corpus = corpus();
    let c = find(&corpus, "pass-eddsa-time");
    let card = hex_of(&c["card"]);
    let holder = c["holder"].as_str().unwrap();
    let anchor = c["anchor"].as_str().unwrap();
    let policy = policy_of(&corpus, &c);
    let vector = hex_of(&c["statements"][0]);
    let (receipts, bare) = split_receipts(&vector);
    let signer = vector_holder();
    let env = envelope(holder, anchor);

    // Registers, posts the statement with no Receipt, and the check passes.
    let registrar = stub(Ok(receipts[0].clone()));
    let statement = register_card(&signer, &card, &env, &registrar, &policy).unwrap();
    assert_eq!(*registrar.posted.borrow(), vec![bare]);
    assert_eq!(statement, vector);
    let report = check_card_registered(&card, &env, &[statement], &policy);
    assert_eq!(report.status, "PASS");
    assert_eq!(report.service.as_deref(), Some("service-a"));

    // A signer that is not the responder posts nothing.
    let registrar = stub(Ok(receipts[0].clone()));
    let other = envelope(&"01".repeat(32), anchor);
    let err = register_card(&signer, &card, &other, &registrar, &policy).unwrap_err();
    assert!(matches!(err, CardRegistrationError::Invalid(_)));
    assert!(registrar.posted.borrow().is_empty());

    // A service that is down leaves nothing.
    let down = stub(Err("service unavailable".into()));
    let err = register_card(&signer, &card, &env, &down, &policy).unwrap_err();
    assert!(matches!(&err, CardRegistrationError::Registrar(_)));
    assert_eq!(
        std::error::Error::source(&err).unwrap().to_string(),
        "service unavailable"
    );

    // A Receipt that does not pass is refused.
    let junk = stub(Ok(b"junk".to_vec()));
    let err = register_card(&signer, &card, &env, &junk, &policy).unwrap_err();
    assert!(matches!(err, CardRegistrationError::Invalid(_)));

    // No statement is NOT_PRESENT, not a finding.
    let report = check_card_registered(&card, &env, &[], &policy);
    assert_eq!(report.status, "NOT_PRESENT");
}

/// The recorded statement and Receipt of a real capsule-anchor run.
#[test]
fn recorded_anchor_receipt() {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../testdata/card-registration/capsule-anchor-receipt.json"
    );
    let rec: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let env = envelope(
        rec["holder"].as_str().unwrap(),
        rec["anchor"].as_str().unwrap(),
    );
    let statements = vec![hex_of(&rec["statement"])];
    let card = hex_of(&rec["card"]);
    let report = check_card_registered(&card, &env, &statements, &rec["policy"]);
    assert_eq!(report.status, "PASS");
    assert_eq!(report.service.as_deref(), Some("local-capsule-anchor"));
    assert!(report.registration_time.unwrap() > 0);
    // A policy that does not list the service is not a pass.
    let report = check_card_registered(&card, &env, &statements, &json!({}));
    assert_ne!(report.status, "PASS");
}

// ---- A test Receipt service: RFC 9942 Receipts in the one form of IP1
// §6.2.2 (a one-leaf tree, EdDSA) for the statement entry, so a statement
// over a card of the test's own can be registered without a network.

fn head(out: &mut Vec<u8>, major: u8, v: u64) {
    let m = major << 5;
    if v < 24 {
        out.push(m | v as u8);
    } else if v <= 0xff {
        out.extend([m | 24, v as u8]);
    } else if v <= 0xffff {
        out.push(m | 25);
        out.extend((v as u16).to_be_bytes());
    } else {
        out.push(m | 26);
        out.extend((v as u32).to_be_bytes());
    }
}

struct Service {
    key: SigningKey,
    iat: u64,
}

impl Service {
    fn new(seed: u8) -> Self {
        Service {
            key: SigningKey::from_bytes(&[seed; 32]),
            iat: 1_795_000_000,
        }
    }

    fn policy(&self) -> Value {
        let spki = format!(
            "302a300506032b6570032100{}",
            hex::encode(self.key.verifying_key().to_bytes())
        );
        json!({"witnesses": [{"name": "test-service", "keys": [spki]}]})
    }

    /// A Receipt over `entry`, the SHA-256 of a statement's Sig_structure.
    fn receipt_for(&self, entry: &[u8]) -> Vec<u8> {
        let leaf: [u8; 32] = Sha256::digest([&[0u8][..], entry].concat()).into();
        // {1: -8, 15: {6: iat}, 395: 1}, keys in encoding order.
        let mut protected = Vec::new();
        head(&mut protected, 5, 3);
        head(&mut protected, 0, 1);
        protected.push(0x27);
        head(&mut protected, 0, 15);
        head(&mut protected, 5, 1);
        head(&mut protected, 0, 6);
        head(&mut protected, 0, self.iat);
        head(&mut protected, 0, 395);
        head(&mut protected, 0, 1);
        let mut sig_structure = Vec::new();
        head(&mut sig_structure, 4, 4);
        head(&mut sig_structure, 3, 10);
        sig_structure.extend(b"Signature1");
        head(&mut sig_structure, 2, protected.len() as u64);
        sig_structure.extend(&protected);
        head(&mut sig_structure, 2, 0);
        head(&mut sig_structure, 2, 32);
        sig_structure.extend(leaf);
        let signature = ed25519_dalek::Signer::sign(&self.key, &sig_structure).to_bytes();
        // The inclusion proof [1, 0, []] as a byte string, in {396: {-1: [proof]}}.
        let proof = encode(&Cbor::Array(vec![1.into(), 0.into(), Cbor::Array(vec![])]));
        let mut out = vec![0xd2];
        head(&mut out, 4, 4);
        head(&mut out, 2, protected.len() as u64);
        out.extend(&protected);
        head(&mut out, 5, 1);
        head(&mut out, 0, 396);
        head(&mut out, 5, 1);
        out.push(0x20);
        head(&mut out, 4, 1);
        head(&mut out, 2, proof.len() as u64);
        out.extend(&proof);
        out.push(0xf6);
        head(&mut out, 2, 64);
        out.extend(signature);
        out
    }
}

fn entry_of(statement: &[u8]) -> Vec<u8> {
    let items = parts(statement);
    let (Cbor::Bytes(protected), Cbor::Bytes(payload)) = (&items[0], &items[2]) else {
        panic!("statement")
    };
    let mut sig = Vec::new();
    head(&mut sig, 4, 4);
    head(&mut sig, 3, 10);
    sig.extend(b"Signature1");
    head(&mut sig, 2, protected.len() as u64);
    sig.extend(protected);
    head(&mut sig, 2, 0);
    head(&mut sig, 2, payload.len() as u64);
    sig.extend(payload);
    Sha256::digest(sig).to_vec()
}

struct ServiceRegistrar<'a>(&'a Service);

impl CardStatementRegistrar for ServiceRegistrar<'_> {
    fn register_statement(
        &self,
        statement: &[u8],
    ) -> Result<Vec<u8>, Box<dyn std::error::Error + Send + Sync>> {
        Ok(self.0.receipt_for(&entry_of(statement)))
    }
}

/// An existing passing Receipt must not let a bad one be kept:
/// `card_registered` passes when any Receipt does, so the new one is checked
/// alone.
#[test]
fn attach_checks_the_new_receipt_alone() {
    let signer = vector_holder();
    let holder = Signer::key_id(&signer);
    let service = Service::new(7);
    let anchor = hex::encode([3u8; 32]);
    let card = br#"{"card":"x"}"#.to_vec();
    let policy = service.policy();
    let env = envelope(&holder, &anchor);
    let statement =
        register_card(&signer, &card, &env, &ServiceRegistrar(&service), &policy).unwrap();
    let entry = entry_of(&statement);

    let corpus = corpus();
    let foreign = split_receipts(&hex_of(&find(&corpus, "pass-eddsa-time")["statements"][0])).0;
    for (name, receipt) in [
        ("junk", b"junk".to_vec()),
        (
            "a Receipt over another entry",
            service.receipt_for(&[9u8; 32]),
        ),
        (
            "a Receipt of an unlisted service",
            Service::new(8).receipt_for(&entry),
        ),
        ("a vector's Receipt", foreign[0].clone()),
    ] {
        attach_err(&card, &holder, &anchor, &statement, &receipt, &policy);
        let _ = name;
    }

    // An earlier Receipt that is not a byte string leaves a statement that can
    // never pass, so a valid new Receipt is still refused.
    let mut items = parts(&statement);
    items[1] = Cbor::Map(vec![(394.into(), Cbor::Array(vec![Cbor::Null]))]);
    let broken = rebuild(items);
    let m = attach_err(
        &card,
        &holder,
        &anchor,
        &broken,
        &service.receipt_for(&entry),
        &policy,
    );
    assert!(m.contains("would not pass"), "{m}");

    // A second good Receipt is kept beside the first.
    let two = attach_card_receipt(
        &card,
        &holder,
        &anchor,
        &statement,
        &service.receipt_for(&entry),
        &policy,
    )
    .unwrap();
    assert_ne!(two, statement);
    let report = check_card_registered(&card, &env, &[two], &policy);
    assert_eq!(report.status, "PASS");
    assert_eq!(report.registration_time, Some(service.iat));

    // A statement over this card does not pass for another card.
    let other = check_card_registered(b"{\"another\":\"card\"}", &env, &[statement], &policy);
    assert_eq!(other.status, "FAIL");
}
