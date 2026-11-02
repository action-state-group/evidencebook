// SPDX-License-Identifier: Apache-2.0
//! `standard_history/1` answered by the crate's responder
//! (evidencebook#328), against the vectors Go writes
//! (`testdata/standard-history-respond/vectors.json`): the same recipe
//! (keys, clock, records) gives the same reply octets for each case, and a
//! staple built from the book is one the staple checker accepts.

use ed25519_dalek::SigningKey;
use evidence_request::envelope::{parse_reply, Binding, Reply};
use evidencebook::answer::anchor_digest;
use evidencebook::ask::Entry;
use evidencebook::derivation::Derivations;
use evidencebook::record::EpistemicType;
use evidencebook::respond::{respond, RespondOptions, Responder, ResponderPolicy, SharePolicy};
use evidencebook::standard_history::{Policy, StandardHistoryError, StandardHistorySource, Window};
use evidencebook::staple::{check_staple, staple_standard_history};
use evidencebook_interop::{epoch, Clock, MemBook};
use serde_json::{json, Value};

fn vectors() -> Value {
    serde_json::from_str(include_str!(
        "../../../testdata/standard-history-respond/vectors.json"
    ))
    .unwrap()
}

fn seed(v: &Value, key: &str) -> SigningKey {
    SigningKey::from_bytes(
        &hex::decode(v[key].as_str().unwrap())
            .unwrap()
            .try_into()
            .unwrap(),
    )
}

fn book(v: &Value) -> MemBook {
    let clock = Clock::at(epoch(2026, 9, 26) + 12 * 3600);
    let mut book = MemBook::open(
        v["book_id"].as_str().unwrap(),
        v["operator"].as_str().unwrap(),
        seed(v, "signer_seed"),
        v["log_id"].as_str().unwrap(),
        seed(v, "log_seed"),
        clock,
    );
    for e in v["entries"].as_array().unwrap() {
        let counterparty = e["counterparty_ref"].as_str().map(str::to_string);
        let entry = Entry {
            record_type: e["record_type"].as_str().unwrap().into(),
            epistemic_type: match e["epistemic_type"].as_str().unwrap() {
                "producer_claim" => EpistemicType::ProducerClaim,
                _ => EpistemicType::ObservedEvent,
            },
            links: Vec::new(),
            statement: e.get("statement").cloned().unwrap_or(Value::Null),
        };
        book.append_with(&entry, |h| h.counterparty_ref = counterparty)
            .unwrap();
    }
    book
}

/// The test host of Go's `dealsOf`: one deal per `deal_terms` record, its
/// statement with the record id as the deal and the commit time as the
/// agreement.
fn source(v: &Value, holder_kind: &str) -> StandardHistorySource {
    let digest = v["policy_digest"].as_str().unwrap().to_string();
    let months = v["policy_months"].as_u64().unwrap();
    StandardHistorySource {
        holder_kind: holder_kind.into(),
        inputs: Box::new(move |_, records| {
            let deals = records
                .iter()
                .filter(|r| r.header["record_type"] == "deal_terms")
                .map(|r| {
                    let mut deal = r.header["statement"].clone();
                    deal["deal"] = json!(r.record.digest);
                    deal["agreed_at"] = r.header["committed_at"].clone();
                    deal
                })
                .collect();
            Ok((
                Policy {
                    digest: digest.clone(),
                    window: Window::Months(months),
                },
                deals,
            ))
        }),
    }
}

fn ask(
    book: &mut MemBook,
    responder: &Responder<'_>,
    request: &str,
    requester: Option<&str>,
) -> Vec<u8> {
    let opts = RespondOptions {
        authenticated_requester: requester,
        policy: SharePolicy::default(),
    };
    respond(book, responder, request.as_bytes(), Binding::Json, &opts)
        .unwrap()
        .0
}

#[test]
fn the_crate_answers_as_go_does() {
    let v = vectors();
    for scenario in v["scenarios"].as_array().unwrap() {
        let mut b = book(&v);
        let signer = b.signer().clone();
        let host = source(&v, scenario["holder_kind"].as_str().unwrap());
        let derivations = Derivations::new();
        let policy = ResponderPolicy::default();
        let responder = Responder {
            signer: &signer,
            derivations: &derivations,
            policy: &policy,
            standard_history: scenario["source"].as_bool().unwrap().then_some(&host),
        };
        let pin = anchor_digest(&b.cut().unwrap());
        assert_eq!(pin, scenario["pin"], "{}", scenario["name"]);
        for case in scenario["cases"].as_array().unwrap() {
            let name = format!("{}/{}", scenario["name"], case["name"]);
            let requester = case["requester"].as_str().filter(|r| !r.is_empty());
            let reply = ask(
                &mut b,
                &responder,
                case["request"].as_str().unwrap(),
                requester,
            );
            assert_eq!(
                String::from_utf8(reply.clone()).unwrap(),
                case["reply"].as_str().unwrap(),
                "{name}"
            );
            match parse_reply(&reply, Binding::Json).unwrap() {
                Reply::Envelope(e) => {
                    assert_eq!(case["outcome"], "artifact", "{name}");
                    assert_eq!(
                        String::from_utf8(e.artifact).unwrap(),
                        case["artifact"].as_str().unwrap()
                    );
                }
                Reply::Refusal(r) => assert_eq!(case["outcome"], r["reason"], "{name}"),
            }
        }
    }
}

#[test]
fn a_stranger_never_reaches_the_hosts_inputs() {
    let v = vectors();
    let mut b = book(&v);
    let signer = b.signer().clone();
    let reads = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let counted = reads.clone();
    let inner = source(&v, "person");
    let host = StandardHistorySource {
        holder_kind: "person".into(),
        inputs: Box::new(move |anchor, records| {
            counted.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            (inner.inputs)(anchor, records)
        }),
    };
    let derivations = Derivations::new();
    let policy = ResponderPolicy::default();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: Some(&host),
    };
    let pin = anchor_digest(&b.cut().unwrap());
    let request = format!(
        r#"{{"coverage":{{"expected_pin":"{pin}"}},"derivation":"standard_history/1","profile":"evidence-interop/0","subject":{{"full_history":null}}}}"#
    );
    let refused = ask(&mut b, &responder, &request, None);
    assert!(matches!(
        parse_reply(&refused, Binding::Json).unwrap(),
        Reply::Refusal(r) if r["reason"] == "not_authorized"
    ));
    assert_eq!(reads.load(std::sync::atomic::Ordering::SeqCst), 0);
    let answered = ask(&mut b, &responder, &request, Some("mallory"));
    assert!(matches!(
        parse_reply(&answered, Binding::Json).unwrap(),
        Reply::Envelope(_)
    ));
    assert_eq!(reads.load(std::sync::atomic::Ordering::SeqCst), 1);
}

#[test]
fn inputs_that_make_no_standard_card_are_an_error_never_a_card() {
    let v = vectors();
    let mut b = book(&v);
    let signer = b.signer().clone();
    let host = StandardHistorySource {
        holder_kind: "person".into(),
        inputs: Box::new(|_, _| Err(StandardHistoryError("no inputs".into()))),
    };
    let derivations = Derivations::new();
    let policy = ResponderPolicy::default();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: Some(&host),
    };
    let pin = anchor_digest(&b.cut().unwrap());
    let request = format!(
        r#"{{"coverage":{{"expected_pin":"{pin}"}},"derivation":"standard_history/1","profile":"evidence-interop/0","subject":{{"full_history":null}}}}"#
    );
    let opts = RespondOptions {
        authenticated_requester: Some("carol"),
        policy: SharePolicy::default(),
    };
    let err = respond(&mut b, &responder, request.as_bytes(), Binding::Json, &opts).unwrap_err();
    assert!(err.to_string().contains("no inputs"), "{err}");
}

#[test]
fn a_source_needs_a_holder_kind() {
    let v = vectors();
    assert!(source(&v, "person").validate().is_ok());
    assert!(source(&v, "node").validate().is_ok());
    assert!(source(&v, "agent").validate().is_err());
    assert!(source(&v, "").validate().is_err());
}

#[test]
fn a_staple_of_the_standard_card_passes_the_checker() {
    let v = vectors();
    let mut b = book(&v);
    let signer = b.signer().clone();
    let host = source(&v, "node");
    let derivations = Derivations::new();
    let policy = ResponderPolicy::default();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
        standard_history: Some(&host),
    };
    let staple = staple_standard_history(&mut b, &responder).unwrap();
    let holder = hex::encode(signer.verifying_key().to_bytes());
    let checked = check_staple(&staple.request, &staple.reply, &holder).unwrap();
    assert_eq!(checked.derivation, "standard_history/1");
    let card: Value = serde_json::from_slice(&checked.artifact).unwrap();
    assert_eq!(card["holder_kind"], "node");
    assert_eq!(card["controller_class"], "node_declared");

    // A responder with no source refuses its own request.
    let bare = Responder {
        standard_history: None,
        ..responder
    };
    let err = staple_standard_history(&mut b, &bare).unwrap_err();
    assert!(err.to_string().contains("refused its own"), "{err}");
}
