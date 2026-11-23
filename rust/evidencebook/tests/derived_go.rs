//! The Go holder's answer in `report/testdata/derived-report/bundle.json`
//! (`TestTermsPackReportEndToEnd`, evidencebook#606): the reply octets Go's
//! responder signed for a Terms Pack with proofs, carried whole in the
//! requester's bundle. This crate's requester reads it as Go's does.

use base64::Engine;
use evidence_request::envelope::{parse_reply, Binding, Envelope, Reply};
use evidence_request::request::{Coverage, Request};
use evidencebook::derived::{derived_inputs, verify_set_inclusion};
use serde_json::Value;

/// The reply the bundle carries, as the holder signed it.
fn go_reply() -> Envelope {
    let bundle: Value = serde_json::from_str(include_str!(
        "../../../report/testdata/derived-report/bundle.json"
    ))
    .unwrap();
    let payloads = bundle["extensions"]["evidencebook/payloads"]
        .as_object()
        .unwrap();
    payloads
        .values()
        .filter_map(|p| {
            let octets = base64::engine::general_purpose::STANDARD
                .decode(p.as_str()?)
                .ok()?;
            match parse_reply(&octets, Binding::Json).ok()? {
                Reply::Envelope(e) => Some(*e),
                Reply::Refusal(_) => None,
            }
        })
        .next()
        .expect("the bundle carries the holder's reply")
}

fn request_of(e: &Envelope) -> Request {
    Request {
        subject: e.subject.clone(),
        coverage: Coverage::ExpectedPin(e.anchor.digest.clone()),
        derivation: e.derivation.clone(),
        deadline: None,
        nonce: None,
        route: None,
    }
}

/// The key the checkpoint is signed under: the requester pins it out of band;
/// here it is the one the statement names.
fn checkpoint_key(e: &Envelope) -> String {
    let statement = e
        .verification
        .iter()
        .zip(&e.verification_content)
        .find(|(v, _)| v.kind == "cll-checkpoint")
        .map(|(_, octets)| octets)
        .unwrap();
    cll::checkpoint::verify_checkpoint_cose_offline(statement)
        .decoded
        .unwrap()
        .key_id
}

#[test]
fn the_go_holders_answer_is_proven_and_reads_back_to_its_rows() {
    let reply = go_reply();
    reply.verify().unwrap();
    let kinds: Vec<&str> = reply.verification.iter().map(|v| v.kind.as_str()).collect();
    assert_eq!(kinds[0], "cll-checkpoint");
    assert_eq!(
        kinds
            .iter()
            .filter(|k| **k == "cll-inclusion-proof")
            .count(),
        7
    );
    let key = checkpoint_key(&reply);
    let request = request_of(&reply);
    verify_set_inclusion(&reply, &request, &key).unwrap();

    let inputs = derived_inputs(&reply, &request, &key).unwrap();
    assert!(inputs.proven);
    assert_eq!(inputs.records.len(), 7);
    assert_eq!(inputs.derivation.len(), 64);
    for record in &inputs.records {
        let mut names: Vec<&str> = record.fields.keys().map(String::as_str).collect();
        names.sort_unstable();
        assert_eq!(
            names,
            ["committed_at", "record_type"],
            "only the declared fields travel"
        );
    }
    // Rows are in leaf order, as the holder's proofs are.
    let leaves: Vec<u64> = inputs.records.iter().map(|r| r.leaf_index).collect();
    assert!(leaves.windows(2).all(|w| w[0] < w[1]), "{leaves:?}");
}

#[test]
fn the_go_answer_fails_under_another_key_and_with_a_row_missing() {
    let reply = go_reply();
    let request = request_of(&reply);
    assert!(verify_set_inclusion(&reply, &request, &"0".repeat(64)).is_err());
    // Dropping a proof item changes what the envelope signed, so the envelope
    // check refuses it before any material is read.
    let mut short = reply.clone();
    let last = short.verification.len() - 1;
    short.verification.remove(last);
    short.verification_content.remove(last);
    assert!(verify_set_inclusion(&short, &request, &checkpoint_key(&reply)).is_err());
}
