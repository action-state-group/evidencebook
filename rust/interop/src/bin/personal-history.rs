// SPDX-License-Identifier: Apache-2.0
//! The Personal-Agent History Profile v0 positive run, produced with this
//! crate instead of Go's `Book.Respond`.
//!
//! ```text
//! cargo run -p evidencebook-interop --bin personal-history [-- --correlated] > responder-run.json
//! ```
//!
//! It makes exactly the calls `test/interop/personal-history/main.go` makes,
//! in the same order, against a fresh book with the same keys and the same
//! clock, and writes the same JSON. The corpus pins that run's SHA-256
//! (`vectors/personal-history-basic-v0/manifest.json` `responder.run_sha256`
//! in agent-action-capsule), so an equal hash means every record, checkpoint,
//! derivation and reply this crate produced is Go's, byte for byte.
//!
//! The keys are test keys. Never use them for anything else.

use evidence_request::envelope::Binding;
use evidencebook::answer::anchor_digest;
use evidencebook::ask::Entry;
use evidencebook::derivation::{DerivationDefinition, Derivations};
use evidencebook::record::EpistemicType;
use evidencebook::request::SubjectKind;
use evidencebook::respond::{respond, RespondOptions, Responder, ResponderPolicy, SharePolicy};
use evidencebook::seal;
use evidencebook_interop::{epoch, go_marshal_indent, rfc3339, seeded, sha256_hex, Clock, MemBook};
use serde_json::{json, Value};

const HOLDER_LABEL: &str = "personal-agent-history-profile-v0 conformance vectors: holder key";
const LOG_LABEL: &str = "personal-agent-history-profile-v0 conformance vectors: holder log key";
const LOG_ID: &str = "holder-stream-1";
const RECIPIENT: &str = "recipient-1";
const OTHER: &str = "requester-2";
const PROFILE: &str = "evidence-interop/0";
const MEDIA_TYPE_JSON: &str = "application/json";

const BASIC_READS: [&str; 4] = [
    "committed_at",
    "epistemic_type",
    "event_time_claim",
    "record_type",
];
const WIDE_READS: [&str; 6] = [
    "committed_at",
    "counterparty_ref",
    "epistemic_type",
    "event_time_claim",
    "record_type",
    "statement",
];

fn deal_ref(n: u32) -> String {
    sha256_hex(
        format!("personal-agent-history-profile-v0 conformance vectors: deal {n}").as_bytes(),
    )[..32]
        .to_string()
}

fn jcs(v: &Value) -> Vec<u8> {
    evidencebook::canonical::jcs(v).expect("a JCS value")
}

fn main() {
    let correlated = std::env::args()
        .skip(1)
        .any(|a| a == "--correlated" || a == "-correlated");
    let clock = Clock::at(epoch(2026, 10, 15));
    let signer = seeded(HOLDER_LABEL);
    let mut book = MemBook::open(
        "holder",
        "holder-operator",
        signer.clone(),
        LOG_ID,
        seeded(LOG_LABEL),
        clock.clone(),
    );

    let d: Vec<String> = (1..=4).map(deal_ref).collect();
    // Deal 1 completes; deal 2 fails its second step after the first
    // checkpoint; deal 3 completes one step; deal 4 is never selected.
    let steps: [(usize, &str, i64); 8] = [
        (0, "step_completed", 40),
        (1, "step_completed", 35),
        (3, "step_completed", 30),
        (0, "step_completed", 25),
        (2, "step_completed", 20),
        (3, "step_failed", 15),
        (1, "step_failed", 10),
        (3, "step_completed", 5),
    ];
    let mut selection = vec![d[0].clone(), d[1].clone(), d[2].clone()];
    selection.sort();
    let mut records = Vec::new();
    let mut checkpoints = Vec::new();
    let mut numbered = std::collections::BTreeMap::<usize, u32>::new();
    for (i, (deal, kind, claimed_minutes)) in steps.into_iter().enumerate() {
        let claimed = rfc3339(clock.get() + 1 - claimed_minutes * 60);
        let (subject_ref, exchange_id) = if correlated {
            let n = numbered.entry(deal).or_default();
            *n += 1;
            (format!("{}/{n}", d[deal]), Some(d[deal].clone()))
        } else {
            (d[deal].clone(), None)
        };
        let entry = Entry {
            record_type: kind.into(),
            epistemic_type: EpistemicType::ProducerClaim,
            links: Vec::new(),
            statement: Value::Null,
        };
        let sealed = book
            .append_with(&entry, |h| {
                h.subject_ref = Some(subject_ref.clone());
                h.event_time_claim = seal::rfc3339_nano(&claimed);
                if let Some(e) = &exchange_id {
                    h.correlation.insert("exchange_id".into(), e.clone());
                }
            })
            .expect("append");
        let mut row = json!({"digest": sealed.record_id, "deal": d[deal]});
        if correlated {
            row["subject_ref"] = json!(subject_ref);
        }
        row["record_type"] = json!(kind);
        records.push(row);
        if i == 5 || i == 7 {
            let cp = book.cut().expect("checkpoint");
            let c = &cp.checkpoint;
            checkpoints.push(json!({
                "expected_pin": anchor_digest(&cp), "tree_size": c.mmr_size,
                "log_key": {"iss": c.log_id, "sub": format!("{}#{}", c.log_id, c.mmr_size), "kid": c.key_id},
            }));
        }
    }

    let mut derivations = Derivations::new();
    let mut definitions = serde_json::Map::new();
    let mut define = |name: &str, reads: &[&str]| -> String {
        let text = jcs(&json!({
            "completeness": "not_asserted", "kind": "projection", "name": "personal_history_basic/1",
            "reads": reads, "recipient": RECIPIENT, "selection": selection, "subject_form": "correlation",
        }));
        definitions.insert(name.into(), json!(String::from_utf8(text.clone()).unwrap()));
        derivations
            .register(DerivationDefinition {
                document: text,
                recipient: Some(RECIPIENT.into()),
                forms: vec![SubjectKind::Correlation],
                fields: reads.iter().map(|s| s.to_string()).collect(),
            })
            .expect("register")
    };
    let basic = define("basic", &BASIC_READS);
    let wide = define("wide_reads", &WIDE_READS);

    let pin_a = checkpoints[0]["expected_pin"].as_str().unwrap().to_string();
    let pin_b = checkpoints[1]["expected_pin"].as_str().unwrap().to_string();
    let policy = ResponderPolicy::default();
    let responder_signer = book.signer().clone();
    let responder = Responder {
        signer: &responder_signer,
        derivations: &derivations,
        policy: &policy,
    };
    let mut exchanges = Vec::new();
    let mut ask = |name: &str, requester: &str, deal: &str, derivation: &str, pin: &str| {
        let text = jcs(&json!({
            "coverage": {"expected_pin": pin}, "derivation": derivation,
            "nonce": format!("{requester}/{name}"), "profile": PROFILE, "subject": {"correlation": deal},
        }));
        let opts = RespondOptions {
            authenticated_requester: Some(requester),
            policy: SharePolicy::default(),
        };
        let (reply, _) =
            respond(&mut book, &responder, &text, Binding::Json, &opts).expect("respond");
        exchanges.push(json!({
            "name": name, "requester": requester,
            "request": {"media_type": MEDIA_TYPE_JSON, "octets_text": String::from_utf8(text).unwrap()},
            "reply": {"media_type": MEDIA_TYPE_JSON, "octets_text": String::from_utf8(reply).unwrap()},
        }));
    };
    for (i, deal) in selection.iter().enumerate() {
        ask(
            &format!("history-{}", i + 1),
            RECIPIENT,
            deal,
            &basic,
            &pin_b,
        );
    }
    ask("not-selected", RECIPIENT, &d[3], &basic, &pin_b);
    ask("other-anchor", RECIPIENT, &d[0], &basic, &pin_a);
    ask("other-requester", OTHER, &d[0], &basic, &pin_b);
    for (i, deal) in selection.iter().enumerate() {
        ask(
            &format!("wide-reads-{}", i + 1),
            RECIPIENT,
            deal,
            &wide,
            &pin_b,
        );
    }

    let out = json!({
        "note": "Test keys from public labels; never use them for anything else.",
        "holder_key": hex::encode(signer.verifying_key().to_bytes()),
        "recipient": RECIPIENT,
        "deals": d,
        "selection": selection,
        "records": records,
        "checkpoints": checkpoints,
        "definitions": definitions,
        "exchanges": exchanges,
    });
    println!("{}", go_marshal_indent(&out, "  "));
}
