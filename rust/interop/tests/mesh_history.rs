// SPDX-License-Identifier: Apache-2.0
//! The Mesh node's History, `inference-service-history/0` (docs/GUIDE.md,
//! path 4), derived and checked outside the plugin: a Mesh node keeps one
//! stream, answers `history_card/1` at a checkpoint on a period boundary, and
//! counts the exchanges it served per closed week and model. The stream here
//! is a book of this crate; the counts, the artifact and the checks are
//! agent-action-capsule's `inference_history`, under the definition a Mesh
//! node serves (`testdata/mesh-history/service-history-1.json`, named on the
//! wire by `cbc96d80…`).
//!
//! 12 exchanges are served in the first week (9 of `model-a`, 3 of
//! `model-b`) and 18 in the second (6 and 12). The floor is 5, so the first
//! week's `model-b` is withheld, and its `model-a` with it: published beside
//! a published total, either would give the other away.

use agent_action_capsule::inference_history::{
    artifact, check_artifact, period_counts, Cell, Counts, Cut, Definition, Refusal,
};
use evidence_request::envelope::{parse_reply, Binding, Reply};
use evidencebook::answer::anchor_digest;
use evidencebook::ask::Entry;
use evidencebook::derivation::Derivations;
use evidencebook::record::EpistemicType;
use evidencebook::respond::{respond, RespondOptions, Responder, ResponderPolicy, SharePolicy};
use evidencebook_interop::{epoch, seeded, Clock, MemBook};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

const DEFINITION: &str = include_str!("../../../testdata/mesh-history/service-history-1.json");
const WEEK: i64 = 604_800;
const LOG_ID: &str = "mesh-node-stream";

/// A Mesh node's stream: `served` records in two weeks, a checkpoint at each
/// week's end, and the node's `history_card/1` at the second. The models are
/// the records' own statements, read back leaf by leaf, as the plugin reads
/// them from its ledger.
struct Node {
    book: MemBook,
    models: Vec<Option<String>>,
    anchor: evidencebook::substrate::StatedCheckpoint,
}

fn node() -> Node {
    let week_0 = epoch(2026, 1, 5);
    let clock = Clock::at(week_0 + 86_400);
    let mut book = MemBook::open(
        "mesh-node",
        "mesh-node-operator",
        seeded("mesh history example: node key"),
        LOG_ID,
        seeded("mesh history example: log key"),
        clock.clone(),
    );
    let served = |book: &mut MemBook, model: &str, n: u32| {
        for _ in 0..n {
            let entry = Entry {
                record_type: "served".into(),
                epistemic_type: EpistemicType::ObservedEvent,
                links: Vec::new(),
                statement: json!({"model": model}),
            };
            book.append_with(&entry, |_| {}).unwrap();
        }
    };
    // The checkpoint's time is the clock's last reading: one tick for each
    // index root it commits, then its own. Set the clock so that is the
    // boundary.
    let cut_at = |book: &mut MemBook, boundary: i64, ticks: i64| {
        clock.advance(boundary - ticks - clock.get());
        let cp = book.cut().unwrap();
        assert_eq!(
            cp.checkpoint.timestamp,
            evidencebook_interop::rfc3339(boundary)
        );
        cp
    };
    served(&mut book, "model-a", 9);
    served(&mut book, "model-b", 3);
    cut_at(&mut book, week_0 + WEEK, 3);
    clock.advance(86_400);
    served(&mut book, "model-a", 6);
    served(&mut book, "model-b", 12);
    let anchor = cut_at(&mut book, week_0 + 2 * WEEK, 2);
    let models = book
        .records
        .iter()
        .map(|held| {
            (held.header.record_type == "served").then(|| {
                held.header.statement.as_ref().unwrap()["model"]
                    .as_str()
                    .unwrap()
                    .to_string()
            })
        })
        .collect();
    Node {
        book,
        models,
        anchor,
    }
}

/// The node's `history_card/1` at its anchor, as a requester receives it.
fn history_card(node: &mut Node) -> Vec<u8> {
    let pin = anchor_digest(&node.anchor);
    let request = serde_json::to_vec(&json!({
        "subject": {"full_history": null},
        "derivation": "history_card/1",
        "coverage": {"expected_pin": pin},
        "nonce": "2026-01-19T00:10:00Z/mesh",
        "profile": "evidence-interop/0",
    }))
    .unwrap();
    let signer = seeded("mesh history example: node key");
    let derivations = Derivations::new();
    let policy = ResponderPolicy::default();
    let responder = Responder {
        signer: &signer,
        derivations: &derivations,
        policy: &policy,
    };
    let options = RespondOptions {
        authenticated_requester: Some("relying-party"),
        policy: SharePolicy {
            history_segments: "peers".into(),
            ..SharePolicy::default()
        },
    };
    let (octets, _) = respond(
        &mut node.book,
        &responder,
        &request,
        Binding::Json,
        &options,
    )
    .expect("respond");
    match parse_reply(&octets, Binding::Json).expect("a reply") {
        Reply::Envelope(e) => e.artifact,
        Reply::Refusal(_) => panic!("the node refused its history card"),
    }
}

/// The checkpoints of the stream, as the counts are derived from them: time
/// and records covered, the first to the anchor.
fn chain(node: &Node) -> Vec<(String, u64)> {
    node.book
        .checkpoints()
        .iter()
        .map(|cp| {
            (
                cp.checkpoint.timestamp.clone(),
                cll::mmr::leaf_count(cp.checkpoint.mmr_size).unwrap(),
            )
        })
        .collect()
}

/// The checkpoints of the card a relying party holds, read from the card
/// itself: each `history` entry's signed statement gives the time and the
/// records covered, the stream's first to the anchor. (The statements'
/// signatures are the `history_card/1` check's; this reads what they state.)
fn card_chain(card: &Value) -> Vec<(String, u64)> {
    card["history"]
        .as_array()
        .expect("the card lists its checkpoints")
        .iter()
        .map(|entry| {
            let statement = hex::decode(entry["checkpoint"].as_str().unwrap()).unwrap();
            let verified = cll::checkpoint::verify_checkpoint_cose_offline(&statement);
            assert!(verified.ok, "a checkpoint of the card verifies");
            let cp = verified.decoded.expect("a checkpoint");
            (cp.timestamp, cll::mmr::leaf_count(cp.mmr_size).unwrap())
        })
        .collect()
}

fn cuts_of(list: &[(String, u64)]) -> Vec<Cut<'_>> {
    list.iter()
        .map(|(timestamp, leaves)| Cut {
            timestamp,
            leaves: *leaves,
        })
        .collect()
}

#[test]
fn the_mesh_history_is_derived_and_checked_outside_the_plugin() {
    let definition: Value = serde_json::from_str(DEFINITION).unwrap();
    let def = Definition::from_document(&definition).expect("definition_form");
    // definition_binding: the History is named by the definition's digest.
    assert_eq!(
        def.digest(),
        "cbc96d800ad206791db7e0793b80a2720c1908468ba99692b739aa8e74aab2d8"
    );

    let mut node = node();
    let card = history_card(&mut node);
    let times = chain(&node);
    let cuts = cuts_of(&times);
    let anchor = anchor_digest(&node.anchor);
    let log_size = cuts.last().unwrap().leaves;

    // The derivation: period counts over the stream, then the artifact that
    // embeds the card unchanged.
    let models = node.models.clone();
    let counts = period_counts(&def, &cuts, &|leaf| models[leaf as usize].clone()).unwrap();
    let octets = artifact(&def, &card, counts, &anchor, LOG_ID, log_size).unwrap();
    let art: Value = serde_json::from_slice(&octets).unwrap();
    assert_eq!(art["derivation"], json!(def.digest()));
    assert_eq!(
        art["history_card_digest"],
        json!(hex::encode(Sha256::digest(&card)))
    );

    // The checks: the members and the card's digest, the coverage
    // declaration, period_alignment, period_anchor and period_floor.
    let art = art.as_object().unwrap();
    // The checker reads the checkpoints from the card the History embeds, not
    // from the node's own state.
    let from_card = card_chain(&art["history_card"]);
    assert_eq!(from_card, times, "the card lists the stream's checkpoints");
    let held = cuts_of(&from_card);
    let checked: Counts =
        check_artifact(&def, art, &held, &anchor, LOG_ID).expect("History checks");
    assert_eq!(checked.periods.len(), 2, "both closed weeks, none left out");
    let week_0 = &checked.periods[0];
    assert_eq!(
        (week_0.from.as_str(), week_0.to.as_str()),
        ("2026-01-05T00:00:00Z", "2026-01-12T00:00:00Z")
    );
    assert_eq!(
        week_0.cells,
        vec![
            Cell {
                model: Some("model-a".into()),
                value: Err("complementary".into())
            },
            Cell {
                model: Some("model-b".into()),
                value: Err("below_floor".into())
            },
            Cell {
                model: None,
                value: Ok(12)
            },
        ]
    );
    let week_1 = &checked.periods[1];
    assert_eq!(
        week_1.cells,
        vec![
            Cell {
                model: Some("model-a".into()),
                value: Ok(6)
            },
            Cell {
                model: Some("model-b".into()),
                value: Ok(12)
            },
            Cell {
                model: None,
                value: Ok(18)
            },
        ]
    );
    for period in &checked.periods {
        for cell in &period.cells {
            println!(
                "{} {}: {}",
                period.from,
                cell.model.as_deref().unwrap_or("total"),
                match &cell.value {
                    Ok(n) => n.to_string(),
                    Err(why) => format!("withheld ({why})"),
                }
            );
        }
    }

    // A History with the first week dropped does not check.
    let mut dropped = art.clone();
    dropped["period_counts"]["periods"]
        .as_array_mut()
        .unwrap()
        .remove(0);
    let why = check_artifact(&def, &dropped, &held, &anchor, LOG_ID).unwrap_err();
    assert!(why.starts_with("period_alignment"), "{why}");

    // An anchor off a period boundary is refused when deriving
    // (`policy_declined`, the same for every requester), and a checker handed
    // such an anchor checkpoint fails `period_anchor`.
    let mut off = times.clone();
    off.last_mut().unwrap().0 = "2026-01-19T00:01:00Z".into();
    let off_cuts = cuts_of(&off);
    assert_eq!(
        period_counts(&def, &off_cuts, &|leaf| models[leaf as usize].clone()),
        Err(Refusal::PolicyDeclined)
    );
    let why = check_artifact(&def, art, &off_cuts, &anchor, LOG_ID).unwrap_err();
    assert!(why.starts_with("period_anchor"), "{why}");
}
