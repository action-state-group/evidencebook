//! A History card stapled to the holder's own message (evidencebook#275):
//! the check a reader runs on a staple it received, as the Go
//! `CheckStaple` and the Python `check_staple` do, and a `history_card/1`
//! staple built from a book.
//!
//! A staple is the holder's own evidence request (subject, derivation,
//! coverage pinned to its newest checkpoint, a fresh nonce) and the signed
//! envelope the holder's book answers it with. It carries `history_card/1`
//! over `checkpoints` or `standard_history/1` over `full_history` and
//! nothing else, so it never carries more than the standard card. Where it
//! sits, and the witness Receipts beside a card, are the carrier's.
//!
//! One departure from Go and Python, stated: for `standard_history/1` this
//! checks that the card has exactly the standard card's members and names
//! its derivation, not the rest of the card's form (SH §6 `card_form`),
//! which has no Rust checker yet. The card's properties are the reader's
//! verifier in every language.

use crate::answer::{self, DERIVATION_HISTORY_CARD};
use crate::ask::Book;
use crate::canonical::jcs;
use crate::respond::{respond, RespondOptions, Responder, ResponderPolicy, SharePolicy};
use crate::store::CheckpointedLog;
use evidence_request::envelope::{parse_reply, Binding, Reply, PROFILE};
use evidence_request::request::{self, Coverage, Derivation, Subject};
use rand_core::{OsRng, RngCore};
use serde_json::json;
use serde_json::Value;
use sha2::{Digest, Sha256};

/// The registered token of the standard card (agent-action-capsule
/// `spec/standard-history-v0.md`).
pub const DERIVATION_STANDARD_HISTORY: &str = "standard_history/1";

/// The standard card's members (SH §4).
const CARD_MEMBERS: [&str; 14] = [
    "anchor",
    "bands",
    "class_missing",
    "controller_class",
    "derivation",
    "disputes",
    "happened",
    "holder_kind",
    "in_window",
    "outcomes",
    "policy",
    "roles",
    "since",
    "window",
];

/// What [`check_staple`] read from a staple that passed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StapleCheck {
    pub derivation: String,
    /// The digest of the holder checkpoint the card is as of.
    pub anchor: String,
    /// The holder's nonce, for the reader's replay check.
    pub nonce: String,
    pub issued_at: String,
    /// The card, in its canonical octets.
    pub artifact: Vec<u8>,
}

/// Why a staple is refused.
#[derive(Debug, thiserror::Error, PartialEq, Eq)]
#[error("staple: {0}")]
pub struct StapleError(String);

fn refuse<T>(why: impl Into<String>) -> Result<T, StapleError> {
    Err(StapleError(why.into()))
}

/// Check that a staple, its request and reply both JSON, is presented by
/// `holder` (lowercase hex Ed25519) and stays inside the cap: the envelope
/// verifies under the holder's key and answers this very request, the
/// derivation and subject are one of the two pairs, and the anchor is the
/// one the request pinned, as is the profile. It does not check the
/// anchor's checkpoint, the card's properties or the nonce's freshness:
/// those are the reader's. A `history_card/1` artifact must have the card's
/// members and nothing else, and end at its anchor.
pub fn check_staple(
    request: &[u8],
    reply: &[u8],
    holder: &str,
) -> Result<StapleCheck, StapleError> {
    let req = match request::parse_json(request) {
        Ok(req) => req,
        Err(e) => return refuse(format!("the request is malformed: {e}")),
    };
    let derivation = match &req.derivation {
        Some(Derivation::Token(t)) => t.as_str(),
        _ => "",
    };
    let capped = matches!(
        (derivation, &req.subject),
        (DERIVATION_HISTORY_CARD, Subject::Checkpoints)
            | (DERIVATION_STANDARD_HISTORY, Subject::FullHistory)
    );
    if !capped {
        return refuse(format!(
            "a staple carries {DERIVATION_HISTORY_CARD} over checkpoints or {DERIVATION_STANDARD_HISTORY} over full_history, not {derivation:?}"
        ));
    }
    let (Coverage::ExpectedPin(pin), Some(nonce)) = (&req.coverage, &req.nonce) else {
        return refuse("the request pins its anchor and carries a nonce");
    };
    if nonce.is_empty() {
        return refuse("the request pins its anchor and carries a nonce");
    }
    let e = match parse_reply(reply, Binding::Json) {
        Ok(Reply::Envelope(e)) => e,
        Ok(Reply::Refusal(_)) => return refuse("the reply is a refusal"),
        Err(e) => return refuse(format!("the reply: {e}")),
    };
    if let Err(err) = e.verify() {
        return refuse(format!("the envelope: {err}"));
    }
    if e.responder != holder {
        return refuse("the staple is not presented by its holder");
    }
    if e.request_digest != hex::encode(Sha256::digest(request)) {
        return refuse("the envelope answers another request");
    }
    if e.derivation != req.derivation || e.subject != req.subject {
        return refuse("the envelope signs another derivation or subject");
    }
    if &e.anchor.digest != pin {
        return refuse("the envelope is as of another anchor");
    }
    // `verify` leaves the profile to its caller (profile §3.3 step 5); a
    // request naming none is answered as `evidence-interop/0`.
    let asked = serde_json::from_slice::<Value>(request)
        .ok()
        .and_then(|v| v.get("profile").and_then(Value::as_str).map(str::to_owned))
        .unwrap_or_else(|| PROFILE.to_owned());
    if e.profile != asked {
        return refuse("the envelope names another profile");
    }
    if derivation == DERIVATION_HISTORY_CARD {
        history_card_form(&e.artifact, pin)?;
    }
    if derivation == DERIVATION_STANDARD_HISTORY {
        let card: Value = match serde_json::from_slice(&e.artifact) {
            Ok(card) => card,
            Err(_) => return refuse("the stapled card is not JSON"),
        };
        let Some(members) = card.as_object() else {
            return refuse("the stapled card is not an object");
        };
        let mut names: Vec<&str> = members.keys().map(String::as_str).collect();
        names.sort_unstable();
        if names != CARD_MEMBERS || members["derivation"] != DERIVATION_STANDARD_HISTORY {
            return refuse("the stapled card is not the standard card's members");
        }
    }
    Ok(StapleCheck {
        derivation: derivation.to_owned(),
        anchor: pin.clone(),
        nonce: nonce.clone(),
        issued_at: e.issued_at.clone(),
        artifact: e.artifact.clone(),
    })
}

/// A `history_card/1` artifact (ER §8.1) with the card's members and nothing
/// else, as Go's `historyCardForm` checks it: anchor, derivation,
/// evidence_stream, history and subject; each history entry exactly a
/// checkpoint and the consistency proof from the one before (null for the
/// first), as lowercase hex; the last checkpoint is the anchor. No signature
/// or proof is checked: the checkpoint key is the reader's.
fn history_card_form(artifact: &[u8], anchor: &str) -> Result<(), StapleError> {
    let bad = |why: &str| StapleError(format!("the stapled history_card/1 {why}"));
    let hex_text = |v: &Value| {
        v.as_str().is_some_and(|s| {
            !s.is_empty()
                && s.len() % 2 == 0
                && s.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f'))
        })
    };
    let card: Value = serde_json::from_slice(artifact).map_err(|_| bad("is not a JSON object"))?;
    let Some(members) = card.as_object() else {
        return Err(bad("is not a JSON object"));
    };
    let mut names: Vec<&str> = members.keys().map(String::as_str).collect();
    names.sort_unstable();
    if names
        != [
            "anchor",
            "derivation",
            "evidence_stream",
            "history",
            "subject",
        ]
    {
        return Err(bad("has members other than the card's"));
    }
    let stream_ok = members["evidence_stream"]
        .as_str()
        .is_some_and(|s| !s.is_empty());
    if members["derivation"] != DERIVATION_HISTORY_CARD
        || members["anchor"] != anchor
        || !stream_ok
        || members["subject"] != serde_json::json!({"checkpoints": null})
    {
        return Err(bad(
            "does not name its derivation, anchor, stream and subject",
        ));
    }
    let history = match members["history"].as_array() {
        Some(h) if !h.is_empty() => h,
        _ => return Err(bad("has no history")),
    };
    for (i, entry) in history.iter().enumerate() {
        let Some(entry) = entry.as_object() else {
            return Err(bad(&format!(
                "entry {i} has members other than a checkpoint and its consistency proof"
            )));
        };
        let mut names: Vec<&str> = entry.keys().map(String::as_str).collect();
        names.sort_unstable();
        if names != ["checkpoint", "consistency_from_previous"] {
            return Err(bad(&format!(
                "entry {i} has members other than a checkpoint and its consistency proof"
            )));
        }
        let proof = &entry["consistency_from_previous"];
        if !hex_text(&entry["checkpoint"])
            || proof.is_null() != (i == 0)
            || (i > 0 && !hex_text(proof))
        {
            return Err(bad(&format!(
                "entry {i} is not a hex checkpoint with its consistency proof"
            )));
        }
    }
    let last = history[history.len() - 1]["checkpoint"]
        .as_str()
        .unwrap_or_default();
    let raw = hex::decode(last).map_err(|_| bad("does not end at its anchor"))?;
    if hex::encode(Sha256::digest(raw)) != anchor {
        return Err(bad("does not end at its anchor"));
    }
    Ok(())
}

/// A staple as built: the holder's own request and the reply its book
/// answered it with, both JSON.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Staple {
    pub request: Vec<u8>,
    pub reply: Vec<u8>,
}

/// Build a `history_card/1` staple from `book`, as of its newest checkpoint
/// (cut now if the book grew past it), as Go's `Book.Staple` does. A public
/// card grows no record. `standard_history/1` is not stapled here: this
/// crate's responder does not answer it yet.
pub fn staple_history_card<B: Book + CheckpointedLog>(
    book: &mut B,
    responder: &Responder<'_>,
) -> Result<Staple, StapleError> {
    let fail = |e: &dyn std::fmt::Display| StapleError(e.to_string());
    let covered = answer::anchors(book)
        .map_err(|e| fail(&e))?
        .last()
        .map(|a| a.size);
    let leaves = book.leaf_count().map_err(|e| fail(&e))?;
    if covered != Some(leaves) {
        book.checkpoint().map_err(|e| fail(&e))?;
    }
    let Some(newest) = book.stated_checkpoints().map_err(|e| fail(&e))?.pop() else {
        return refuse("the book has no checkpoint to staple");
    };
    let mut nonce = [0u8; 16];
    OsRng.fill_bytes(&mut nonce);
    let request = jcs(&json!({
        "coverage": {"expected_pin": answer::anchor_digest(&newest)},
        "derivation": DERIVATION_HISTORY_CARD,
        "nonce": hex::encode(nonce),
        "profile": PROFILE,
        "subject": {"checkpoints": null},
    }))
    .map_err(|e| fail(&e))?;
    let opts = RespondOptions {
        authenticated_requester: None,
        policy: SharePolicy::default(),
    };
    // The holder's own request is not counted by the limiter, which is for
    // askers; the rest of the caller's policy applies.
    let own = ResponderPolicy {
        refusal_policy: responder.policy.refusal_policy,
        limiter: None,
        max_request_bytes: responder.policy.max_request_bytes,
        max_answer_items: responder.policy.max_answer_items,
    };
    let responder = Responder {
        signer: responder.signer,
        derivations: responder.derivations,
        policy: &own,
    };
    let (reply, _) =
        respond(book, &responder, &request, Binding::Json, &opts).map_err(|e| fail(&e))?;
    if !matches!(parse_reply(&reply, Binding::Json), Ok(Reply::Envelope(_))) {
        return refuse("the book refused its own history_card/1 request");
    }
    Ok(Staple { request, reply })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(serde::Deserialize)]
    struct Vector {
        name: String,
        holder: String,
        request: String,
        reply: String,
        pass: bool,
        #[serde(default)]
        nonce: String,
        #[serde(default)]
        anchor: String,
    }

    /// The vectors Go writes (testdata/staple/staples.json).
    #[test]
    fn agrees_with_the_go_vectors() {
        let path = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../testdata/staple/staples.json"
        );
        let vectors: Vec<Vector> =
            serde_json::from_slice(&std::fs::read(path).expect("vectors")).expect("parse");
        assert!(vectors.len() >= 2);
        for v in vectors {
            let got = check_staple(v.request.as_bytes(), v.reply.as_bytes(), &v.holder);
            assert_eq!(got.is_ok(), v.pass, "{}: {got:?}", v.name);
            if let Ok(c) = got {
                assert_eq!((c.nonce, c.anchor), (v.nonce, v.anchor), "{}", v.name);
            }
        }
    }

    #[test]
    fn card_members_are_sorted() {
        let mut sorted = CARD_MEMBERS;
        sorted.sort_unstable();
        assert_eq!(sorted, CARD_MEMBERS);
    }
}
