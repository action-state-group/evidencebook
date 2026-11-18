//! `deal::v1`'s holder side on a shelf (`holder.rs`): the x-deal-v1
//! receive profile, `can_act` and `gate_act`, case for case with Go's
//! `dealprofile/xdealv1` `receive_test.go` and `gate_test.go`, on two
//! parties' shelves built from Appendix A's records.

#![cfg(all(feature = "deal", feature = "shelf"))]

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ed25519_dalek::SigningKey;
use evidencebook::canonical::jcs;
use evidencebook::deal::v1::{self, ActGate, ActGateInput, CanActInput, Record};
use evidencebook::record::{EpistemicType, Link, LinkType};
use evidencebook::shelf::{
    BookEntry, Cadence, CadenceConfig, Indexes, ReceiveConfig, ReceiveResult, SeededStream, Shelf,
    ShelfConfig, ShelfError, StampProof, Time,
};
use rusqlite::Connection;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

const CADENCE_LOG: &str = "deal-cadence/0123456789abcdef";
const DEAL: &str = "deal-6a1f0c93b27d4e58";

fn seed(label: &str) -> [u8; 32] {
    Sha256::digest(format!("xdealv1 receive tests\0{label}").as_bytes()).into()
}

fn key_of(label: &str) -> SigningKey {
    SigningKey::from_bytes(&seed(label))
}

fn log_of(label: &str) -> String {
    format!("deal/{}", hex::encode(&seed(label)[..16]))
}

/// An Appendix A record, as its fixture holds it.
fn fixture(rel: &str) -> Value {
    let p = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../dealprofile/x-deal-v1/fixtures/appendix-a")
        .join(rel);
    let v: Value = serde_json::from_slice(&std::fs::read(&p).unwrap()).unwrap();
    v["record"].clone()
}

fn record(v: &Value) -> Record {
    let raw = jcs(v).unwrap();
    v1::check(&raw).unwrap_or_else(|e| panic!("{e}: {v}"));
    v1::decode(&raw).unwrap()
}

fn digest(v: &Value) -> String {
    v1::digest(&jcs(v).unwrap()).unwrap()
}

fn rec_ref(d: &str) -> Value {
    json!({"type": "deal-record", "digest_alg": "SHA-256", "digest": d})
}

/// The offer, sent to `to`.
fn offer(to: &str) -> Value {
    let mut o = fixture("buyer-journal/05-offer.json");
    o["body"]["to"] = json!(to);
    o
}

/// An agreement on `offer_digest`: the acceptor's (seen null) or the
/// offerer's (seen `seen`), naming `counterparty` and the holder's
/// checkpoint key.
fn agreement(
    offer_digest: &str,
    acceptor: bool,
    counterparty: &str,
    checkpoint_key: &str,
    seen: Option<&str>,
) -> Value {
    let mut a = if acceptor {
        fixture("seller-book/01-agreement.json")
    } else {
        fixture("buyer-book/01-agreement.json")
    };
    a["x-deal-v1"]["seen"] = json!(seen);
    a["body"]["offer"] = rec_ref(offer_digest);
    a["body"]["counterparty_key"] = json!(counterparty);
    a["body"]["checkpoint_key"] = json!(checkpoint_key);
    a["body"]["shelf_log_id"] = json!(CADENCE_LOG);
    a
}

/// A payee settle at `seq` after `prev`, of the deal `agreement` opens.
fn settle(seq: u64, prev: &Value, agreement: &Value, seen: &str) -> Value {
    let mut s = fixture("seller-book/02-payment-received.json");
    s["x-deal-v1"]["seq"] = json!(seq);
    s["x-deal-v1"]["prev"] = rec_ref(&digest(prev));
    s["x-deal-v1"]["agreement_ref"] = rec_ref(&digest(agreement));
    s["x-deal-v1"]["seen"] = json!(seen);
    s
}

/// One side's shelf, with the x-deal-v1 receive profile.
struct Party {
    _dir: Arc<tempfile::TempDir>,
    name: String,
    path: PathBuf,
    signer: SigningKey,
    checkpoint: SigningKey,
    clock: Arc<Mutex<Time>>,
    profile: Arc<v1::ReceiveProfile>,
    shelf: Shelf,
    cadence: Cadence,
}

impl Party {
    fn new(name: &str) -> Party {
        Party::with_signer(name, &format!("{name} record key"))
    }

    fn with_signer(name: &str, signer: &str) -> Party {
        let dir = Arc::new(tempfile::tempdir().unwrap());
        let path = dir.path().join("shelf.db");
        let clock = Arc::new(Mutex::new(Time::parse("2026-11-01T09:00:00Z").unwrap()));
        let profile = Arc::new(v1::ReceiveProfile::new());
        let (shelf, cadence) = open(
            name,
            &path,
            &key_of(signer),
            &key_of(&format!("{name} checkpoint key")),
            &clock,
            &profile,
        );
        let p = Party {
            _dir: dir,
            name: name.into(),
            path,
            signer: key_of(signer),
            checkpoint: key_of(&format!("{name} checkpoint key")),
            clock,
            profile,
            shelf,
            cadence,
        };
        // The journal and seal_sent need a shelf of book facts.
        if !p.cadence.migrated().unwrap() {
            p.cadence.tick(p.now()).unwrap();
            p.tick();
            p.cadence.migrate(p.now()).unwrap();
        }
        p
    }

    fn now(&self) -> Time {
        *self.clock.lock().unwrap()
    }

    fn tick(&self) {
        let mut c = self.clock.lock().unwrap();
        *c = c.plus(Duration::from_secs(60));
    }

    fn reopen(&mut self) {
        self.shelf.release().unwrap();
        let (shelf, cadence) = open(
            &self.name,
            &self.path,
            &self.signer,
            &self.checkpoint,
            &self.clock,
            &self.profile,
        );
        self.shelf = shelf;
        self.cadence = cadence;
    }

    fn key(&self) -> String {
        hex::encode(self.signer.verifying_key().to_bytes())
    }

    fn checkpoint_key(&self) -> String {
        hex::encode(self.checkpoint.verifying_key().to_bytes())
    }

    fn entry(r: &Value) -> BookEntry {
        let mut e = BookEntry::new("deal_step", EpistemicType::ProducerClaim);
        e.payloads = vec![jcs(r).unwrap()];
        e.subject_ref = r["x-deal-v1"]["deal_id"].as_str().map(String::from);
        e
    }

    /// Seals `r` in the journal and its standalone Capsule, with the terms
    /// leg an offer of one payment row carries, and returns the sent pair.
    fn send(&self, r: &Value) -> Vec<u8> {
        self.tick();
        let rec = record(r);
        let mut members = BTreeMap::new();
        if rec.propose().is_some() {
            if let Ok(leg) = v1::terms_leg(&rec) {
                members.insert(
                    v1::SETTLEMENT_MEMBER.to_string(),
                    serde_json::from_slice(&leg).unwrap(),
                );
            }
        }
        let deal = r["x-deal-v1"]["deal_id"].as_str().unwrap();
        let sent = self
            .shelf
            .seal_sent(
                &Party::entry(r),
                &members,
                &format!("{deal}{}", v1::SENT_SUFFIX),
            )
            .unwrap();
        let c = sent.capsule;
        v1::pair(&c.capsule, &c.envelope, &c.payload).unwrap()
    }

    /// Appends `r` to the book `log`, citing the record before it, and
    /// returns its record id and book pair.
    fn seal(&self, log: &str, r: &Value) -> (String, Vec<u8>) {
        self.tick();
        let book = self.shelf.open(log).unwrap();
        let mut e = Party::entry(r);
        if let Some(last) = book.records().last() {
            e.links = vec![Link {
                link_type: LinkType::Cites,
                target: last.record_id.clone(),
            }];
        }
        let rec = book.append(&e).unwrap();
        let pair = v1::book_pair(
            &rec.header_bytes,
            &rec.capsule,
            &rec.envelope,
            &jcs(r).unwrap(),
        )
        .unwrap();
        (rec.record_id, pair)
    }

    /// Takes a stamp and returns the counterparty's proof for the book
    /// whose first record is `first`.
    fn stamp(&self, first: &str) -> StampProof {
        self.tick();
        self.cadence.stamp(self.now()).unwrap();
        self.cadence.stamp_proof(first, 0, "counterparty").unwrap()
    }

    fn receive(&self, record: Option<&[u8]>, proof: Option<&StampProof>) -> ReceiveResult {
        self.shelf.receive(record, proof).unwrap()
    }

    fn refused(&self, record: &[u8]) -> bool {
        let e = self.shelf.receive(Some(record), None).unwrap_err();
        e.errors()
            .iter()
            .any(|e| matches!(e, ShelfError::Refused(_)))
    }

    fn can_act(&self, input: CanActInput) -> bool {
        self.profile.can_act(&input).unwrap()
    }
}

fn open(
    name: &str,
    path: &Path,
    signer: &SigningKey,
    checkpoint: &SigningKey,
    clock: &Arc<Mutex<Time>>,
    profile: &Arc<v1::ReceiveProfile>,
) -> (Shelf, Cadence) {
    let conn = Connection::open(path).unwrap();
    conn.busy_timeout(Duration::from_secs(5)).unwrap();
    let originals =
        ArtifactSqlite::new(conn, "x-deal-v1", [signer.verifying_key().to_bytes()]).unwrap();
    originals.init().unwrap();
    let now = clock.clone();
    let shelf = Shelf::open_sqlite(ShelfConfig {
        path: path.to_path_buf(),
        lock_path: None,
        held_lock: None,
        lock_timeout: Some(Duration::from_secs(5)),
        random: Some(Box::new(SeededStream::new(name))),
        operator: name.into(),
        developer: String::new(),
        signer: Arc::new(signer.clone()),
        checkpoint_key: checkpoint.clone(),
        originals: Arc::new(originals),
        now: Some(Arc::new(move || *now.lock().unwrap())),
        // A deal book's log holds its own records only, so the counterparty
        // can recompute its checkpoints from the records it receives.
        authenticated_indexes: Some(Arc::new(|_: &str| Indexes::None)),
        correlation_members: Vec::new(),
        receive: ReceiveConfig {
            profile: Some(profile.clone()),
            ..Default::default()
        },
    })
    .unwrap();
    profile.set_shelf(&shelf);
    let cadence = shelf
        .cadence(CadenceConfig {
            log_id: CADENCE_LOG.into(),
            interval: Duration::from_secs(300),
            jitter: Duration::ZERO,
            no_jitter: true,
            witness_ids: Vec::new(),
            witnesses: None,
            pad_bucket: 1,
            ended: Arc::new(|_| Ok(false)),
            status: None,
        })
        .unwrap();
    (shelf, cadence)
}

/// Appendix A's offer and the seller's agreement with its stamp proof, up
/// to the buyer receiving them (Go `newHandshake`).
struct Handshake {
    buyer: Party,
    seller: Party,
    offer: Value,
    seller_log: String,
    agreement: Value,
    agreement_id: String,
    agreement_pair: Vec<u8>,
    proof: StampProof,
}

fn handshake(edit: impl FnOnce(&mut Value)) -> Handshake {
    let (buyer, seller) = (Party::new("buyer"), Party::new("seller"));
    let o = offer(&seller.key());
    let res = seller.receive(Some(&buyer.send(&o)), None);
    assert!(res.record.unwrap().stored, "the seller holds the offer");
    let mut a = agreement(
        &digest(&o),
        true,
        &buyer.key(),
        &seller.checkpoint_key(),
        None,
    );
    edit(&mut a);
    record(&a);
    let seller_log = log_of("seller log");
    let (agreement_id, agreement_pair) = seller.seal(&seller_log, &a);
    let proof = seller.stamp(&agreement_id);
    Handshake {
        buyer,
        seller,
        offer: o,
        seller_log,
        agreement: a,
        agreement_id,
        agreement_pair,
        proof,
    }
}

impl Handshake {
    fn input(&self) -> CanActInput {
        CanActInput {
            deal_id: DEAL.into(),
            counterpart_agreement: self.agreement_id.clone(),
            commitment_shelf_log: String::new(),
        }
    }
}

/// The sender a stamp proof under `ck` for `log` is related to, at `p`.
fn sender(p: &Party, ck: &str, log: &str) -> Option<String> {
    use evidencebook::shelf::ReceiveProfile as _;
    p.profile.proof_sender(ck, log).unwrap()
}

#[test]
fn handshake_from_offer() {
    let mut h = handshake(|_| {});
    assert!(!h.buyer.can_act(h.input()), "nothing held yet");
    let res = h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    assert!(res.record.unwrap().stored);
    assert!(res.proof.unwrap().accepted);
    assert!(h.buyer.can_act(h.input()));
    // The gate is of one deal, and a commitment naming another shelf log
    // closes it.
    let mut other_deal = h.input();
    other_deal.deal_id = "deal-9999999999999999".into();
    assert!(!h.buyer.can_act(other_deal), "another deal");
    let mut other_log = h.input();
    other_log.commitment_shelf_log = "deal-cadence/ffffffffffffffff".into();
    assert!(!h.buyer.can_act(other_log), "another commitment shelf log");
    let mut named = h.input();
    named.commitment_shelf_log = CADENCE_LOG.into();
    assert!(
        h.buyer.can_act(named),
        "the commitment names the agreement's shelf log"
    );
    // duplicate-proof and restart.
    let res = h.buyer.receive(None, Some(&h.proof));
    assert!(res.proof.unwrap().duplicate);
    h.buyer.reopen();
    assert!(h.buyer.can_act(h.input()), "after a restart");
}

#[test]
fn no_proof_no_act() {
    let h = handshake(|_| {});
    h.buyer.receive(Some(&h.agreement_pair), None);
    assert!(!h.buyer.can_act(h.input()));
}

#[test]
fn proof_under_another_shelf_log() {
    let mut h = handshake(|a| a["body"]["shelf_log_id"] = json!("deal-cadence/ffffffffffffffff"));
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let proofs = h
        .buyer
        .shelf
        .held_proofs(&h.seller.key(), &h.seller_log)
        .unwrap();
    assert_eq!(proofs.len(), 1);
    assert_eq!(
        proofs[0].covers, 1,
        "held and covering; only its shelf log differs"
    );
    assert!(!h.buyer.can_act(h.input()));
    h.buyer.reopen();
    assert!(!h.buyer.can_act(h.input()), "after a restart");
}

#[test]
fn proof_by_another_checkpoint_key() {
    let h = handshake(|a| a["body"]["checkpoint_key"] = json!("0".repeat(64)));
    let e = h
        .buyer
        .shelf
        .receive(Some(&h.agreement_pair), Some(&h.proof))
        .unwrap_err();
    assert!(matches!(
        e.result.proof.as_ref().and_then(|p| p.refused.as_ref()),
        Some(ShelfError::Refused(_))
    ));
    assert!(!h.buyer.can_act(h.input()));
}

#[test]
fn genesis_fork_after_proof() {
    let mut h = handshake(|_| {});
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let mut other = h.agreement.clone();
    other["body"]["checkpoint_key"] = json!("0".repeat(64));
    other["x-deal-v1"]["at"] = json!("2026-11-01T09:46:00Z");
    let fork = Party::with_signer("fork", "seller record key");
    let (_, fork_pair) = fork.seal(&h.seller_log, &other);
    assert!(
        h.buyer
            .receive(Some(&fork_pair), None)
            .record
            .unwrap()
            .stored,
        "kept as a fork"
    );
    assert!(!h.buyer.can_act(h.input()));
    h.buyer.reopen();
    assert!(!h.buyer.can_act(h.input()), "after a restart");
    let sender = sender(&h.buyer, &h.seller.checkpoint_key(), &h.seller_log);
    assert_eq!(
        sender, None,
        "a sender with two records at position 1 is refused"
    );
}

/// The buyer's agreement on the handshake's offer, naming the seller's.
fn buyer_agreement(h: &Handshake, layer: bool) -> Value {
    let mut a = agreement(
        &digest(&h.offer),
        false,
        &h.seller.key(),
        &h.buyer.checkpoint_key(),
        Some(&h.agreement_id),
    );
    if !layer {
        a["body"]["counterparty_layer"] = json!(false);
        a["body"]["counterparty_key"] = Value::Null;
        a["x-deal-v1"]["seen"] = Value::Null;
    }
    a
}

#[test]
fn proof_sender_both_sides() {
    let h = handshake(|_| {});
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    assert_eq!(
        sender(&h.buyer, &h.seller.checkpoint_key(), &h.seller_log),
        Some(h.seller.key())
    );
    let ba = buyer_agreement(&h, true);
    let buyer_log = log_of("buyer log");
    let (buyer_id, buyer_pair) = h.buyer.seal(&buyer_log, &ba);
    let buyer_proof = h.buyer.stamp(&buyer_id);
    let res = h.seller.receive(Some(&buyer_pair), Some(&buyer_proof));
    assert!(res.record.unwrap().stored && res.proof.unwrap().accepted);
    assert_eq!(
        sender(&h.seller, &h.buyer.checkpoint_key(), &buyer_log),
        Some(h.buyer.key())
    );
    assert!(h.seller.can_act(CanActInput {
        deal_id: DEAL.into(),
        counterpart_agreement: buyer_id,
        commitment_shelf_log: String::new(),
    }));
}

#[test]
fn answered() {
    use evidencebook::shelf::ReceiveProfile as _;
    let h = handshake(|_| {});
    let held = h
        .seller
        .shelf
        .held_first(&format!("{}{}", v1::SENT_LOG_PREFIX, digest(&h.offer)))
        .unwrap();
    assert_eq!(held.len(), 1);
    assert!(
        h.seller.profile.answered(&held[0].received).unwrap(),
        "the seller's agreement cites it"
    );
    let mut other = offer(&h.seller.key());
    other["x-deal-v1"]["deal_id"] = json!("deal-1111111111111111");
    other["x-deal-v1"]["at"] = json!("2026-11-01T09:31:00Z");
    h.seller.receive(Some(&h.buyer.send(&other)), None);
    let held = h
        .seller
        .shelf
        .held_first(&format!("{}{}", v1::SENT_LOG_PREFIX, digest(&other)))
        .unwrap();
    assert!(!h.seller.profile.answered(&held[0].received).unwrap());
}

fn decline(offer_digest: &str) -> Value {
    json!({
        "x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs", "deal_id": DEAL,
            "record_type": "agree", "at": "2026-11-01T09:40:00Z"},
        "body": {"kind": "decline", "offer": rec_ref(offer_digest), "reason": "other"}
    })
}

#[test]
fn admit_refuses_strangers() {
    let h = handshake(|_| {});
    let stranger = Party::new("stranger");
    let (_, pair) = stranger.seal(&log_of("stranger log"), &h.agreement);
    assert!(h.buyer.refused(&pair), "a stranger's deal-book record");
    let d = decline(&digest(&h.offer));
    assert!(
        h.buyer.refused(&stranger.send(&d)),
        "a decline from a key the offer was not sent to"
    );
    let res = h.buyer.receive(Some(&h.seller.send(&d)), None);
    assert!(
        res.record.unwrap().stored,
        "the decline from the key the offer was sent to"
    );
}

#[test]
fn later_records() {
    let h = handshake(|_| {});
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let next = settle(2, &h.agreement, &h.agreement, &h.agreement_id);
    let (id, pair) = h.seller.seal(&h.seller_log, &next);
    let b = v1::verify_book(&pair).unwrap();
    assert_eq!((b.seq, b.prev.as_str()), (2, h.agreement_id.as_str()));
    let out = h.buyer.receive(Some(&pair), None).record.unwrap();
    assert!(out.stored && out.counted);
    assert_eq!(out.digest, id);
    assert_eq!(out.deal_id, DEAL);
    // Another log of the seller's, whose agreement the buyer never received.
    let other_log = log_of("seller other log");
    h.seller.seal(&other_log, &h.agreement);
    let (_, orphan) = h.seller.seal(&other_log, &next);
    assert!(
        h.buyer.refused(&orphan),
        "no admitted first record on that log"
    );
}

#[test]
fn agreement_on_a_counter_offer() {
    let (buyer, seller) = (Party::new("buyer"), Party::new("seller"));
    let o1 = offer(&seller.key());
    seller.receive(Some(&buyer.send(&o1)), None);
    let mut o2 = offer(&seller.key());
    o2["x-deal-v1"]["at"] = json!("2026-11-01T09:35:00Z");
    o2["body"]["counter_to"] = rec_ref(&hex::encode(Sha256::digest(b"the seller's counter-offer")));
    o2["body"]["terms"]["price_minor"] = json!(5500);
    seller.receive(Some(&buyer.send(&o2)), None);
    let a = agreement(
        &digest(&o2),
        true,
        &buyer.key(),
        &seller.checkpoint_key(),
        None,
    );
    let (_, pair) = seller.seal(&log_of("counter log"), &a);
    assert!(
        buyer.receive(Some(&pair), None).record.unwrap().stored,
        "the agreement on our counter-offer"
    );
}

#[test]
fn cancellation() {
    let h = handshake(|_| {});
    let cancel = |offer_digest: &str, at: &str| {
        json!({
            "x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs", "deal_id": DEAL,
                "record_type": "agree", "at": at},
            "body": {"kind": "cancellation", "offer": rec_ref(offer_digest)}
        })
    };
    let c = cancel(&digest(&h.offer), "2026-11-01T09:50:00Z");
    assert!(
        h.seller
            .receive(Some(&h.buyer.send(&c)), None)
            .record
            .unwrap()
            .stored
    );
    let mut other = offer(&h.seller.key());
    other["x-deal-v1"]["deal_id"] = json!("deal-2222222222222222");
    other["x-deal-v1"]["at"] = json!("2026-11-01T09:31:00Z");
    h.seller.receive(Some(&h.buyer.send(&other)), None);
    let wrong = cancel(&digest(&other), "2026-11-01T09:51:00Z");
    assert!(
        h.seller.refused(&h.buyer.send(&wrong)),
        "a cancellation of another deal's offer"
    );
}

#[test]
fn ruling_from_the_adjudicator() {
    let adjudicator = Party::new("adjudicator");
    let h = handshake(|a| a["body"]["adjudicator"]["key_id"] = json!(adjudicator.key()));
    let mut ruling = fixture("case-book/01-ruling.json");
    ruling["body"]["keys"] = json!({"buyer": h.buyer.key(), "seller": h.seller.key()});
    ruling["body"]["read"] = json!({"buyer": null, "seller": h.agreement_id});
    let case_log = format!("case/{}", hex::encode(&seed("case log")[..16]));
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    assert!(h.seller.receive(Some(&pair), None).record.unwrap().stored);
    let stranger = Party::new("stranger");
    let (_, forged) = stranger.seal(&case_log, &ruling);
    assert!(h.seller.refused(&forged));
}

#[test]
fn shared_log_id() {
    for both in [false, true] {
        let h = handshake(|_| {});
        h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
        let seller2 = Party::new("seller two");
        let mut o2 = offer(&seller2.key());
        o2["x-deal-v1"]["deal_id"] = json!("deal-3333333333333333");
        o2["x-deal-v1"]["at"] = json!("2026-11-01T09:32:00Z");
        seller2.receive(Some(&h.buyer.send(&o2)), None);
        let ck = if both {
            h.seller.checkpoint_key()
        } else {
            seller2.checkpoint_key()
        };
        let mut a = agreement(&digest(&o2), true, &h.buyer.key(), &ck, None);
        a["x-deal-v1"]["deal_id"] = json!("deal-3333333333333333");
        a["x-deal-v1"]["at"] = json!("2026-11-01T09:46:00Z");
        let (_, pair) = seller2.seal(&h.seller_log, &a);
        assert!(
            h.buyer.receive(Some(&pair), None).record.unwrap().stored,
            "the second seller's agreement, on the same log id"
        );
        let sender = sender(&h.buyer, &h.seller.checkpoint_key(), &h.seller_log);
        if both {
            assert_eq!(sender, None, "two candidates name the key");
        } else {
            assert_eq!(sender, Some(h.seller.key()));
        }
    }
}

/// Marks a journal record's retention state with a lifecycle record (Go
/// `SetRetention`).
fn set_retention(p: &Party, record_id: &str, state: &str) {
    let journal = p.shelf.journal().unwrap();
    let mut lifecycle = BookEntry::new("lifecycle", EpistemicType::ProducerClaim);
    lifecycle.links = vec![Link {
        link_type: LinkType::Cites,
        target: record_id.into(),
    }];
    lifecycle.statement = json!({"retention_state": state, "reason": "test"});
    journal.append(&lifecycle).unwrap();
}

#[test]
fn withheld_offer_read_again() {
    let h = handshake(|_| {});
    let journal = h.buyer.shelf.journal().unwrap();
    let offer_id = journal
        .records()
        .iter()
        .rev()
        .find(|r| r.header.get("record_type") == Some(&json!("deal_step")))
        .unwrap()
        .record_id
        .clone();
    set_retention(&h.buyer, &offer_id, "WITHHELD");
    let pair = h.seller.send(&decline(&digest(&h.offer)));
    assert!(
        h.buyer.refused(&pair),
        "the offer cannot be read while withheld"
    );
    set_retention(&h.buyer, &offer_id, "AVAILABLE");
    assert!(
        h.buyer.receive(Some(&pair), None).record.unwrap().stored,
        "read again once available"
    );
}

#[test]
fn proof_the_records_do_not_recompute() {
    let h = handshake(|_| {});
    let mut other = h.agreement.clone();
    other["x-deal-v1"]["at"] = json!("2026-11-01T09:44:00Z");
    let fork = Party::with_signer("fork", "seller record key");
    let (other_id, other_pair) = fork.seal(&h.seller_log, &other);
    assert_ne!(other_id, h.agreement_id);
    assert!(
        h.buyer
            .receive(Some(&other_pair), None)
            .record
            .unwrap()
            .stored
    );
    let e = h.buyer.shelf.receive(None, Some(&h.proof)).unwrap_err();
    assert!(matches!(
        e.result.proof.as_ref().and_then(|p| p.refused.as_ref()),
        Some(ShelfError::Invalid(_))
    ));
    let kinds: Vec<String> = h
        .buyer
        .shelf
        .evidence()
        .unwrap()
        .into_iter()
        .map(|e| e.kind)
        .collect();
    assert!(
        kinds
            .iter()
            .any(|k| k == evidencebook::shelf::EVIDENCE_CHECKPOINT_RECORDS),
        "{kinds:?}"
    );
    assert!(!h.buyer.can_act(CanActInput {
        deal_id: DEAL.into(),
        counterpart_agreement: other_id,
        commitment_shelf_log: String::new(),
    }));
}

#[test]
fn can_act_without_a_layer_is_false() {
    let h = handshake(|_| {});
    assert!(!h.seller.can_act(CanActInput {
        deal_id: DEAL.into(),
        ..Default::default()
    }));
}

/// The buyer's agreement sealed in its book (Go `gateBook`).
fn gate_book(h: &Handshake, layer: bool) -> (Value, String, Vec<u8>) {
    let a = buyer_agreement(h, layer);
    record(&a);
    let (id, pair) = h.buyer.seal("deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", &a);
    (a, id, pair)
}

/// An act at `seq`: `kind` is payer, payee, perform or statement.
fn act(seq: u64, kind: &str) -> Record {
    let mut r = match kind {
        "payer" => fixture("buyer-book/02-payment.json"),
        "payee" => fixture("seller-book/02-payment-received.json"),
        "perform" => fixture("seller-book/03-delivery.json"),
        _ => fixture("buyer-book/03-receipt.json"),
    };
    r["x-deal-v1"]["seq"] = json!(seq);
    v1::decode(&jcs(&r).unwrap()).unwrap()
}

fn gate(h: &Handshake, own: &[Record], act: Record) -> ActGate {
    h.buyer
        .profile
        .gate_act(&ActGateInput {
            own: own.to_vec(),
            act,
            counterpart_agreement: h.agreement_id.clone(),
            commitment_shelf_log: String::new(),
        })
        .unwrap()
}

fn gated(reason: &str) -> ActGate {
    ActGate {
        gated: true,
        reason: reason.into(),
        ..Default::default()
    }
}

fn allowed() -> ActGate {
    ActGate {
        allowed: true,
        ..Default::default()
    }
}

#[test]
fn gate_act_holds_the_first_act() {
    let mut h = handshake(|_| {});
    h.buyer.receive(Some(&h.agreement_pair), None);
    let (ag, _, _) = gate_book(&h, true);
    let own = vec![record(&ag)];
    assert_eq!(
        gate(&h, &own, act(2, "payer")),
        gated(v1::GATE_NO_PROOF),
        "a first payer leg waits"
    );
    assert_eq!(
        gate(&h, &own, act(2, "perform")),
        gated(v1::GATE_NO_PROOF),
        "a first perform waits"
    );
    assert_eq!(
        gate(&h, &own, act(2, "payee")),
        allowed(),
        "a payee leg never waits"
    );
    assert_eq!(
        gate(&h, &own, act(2, "statement")),
        allowed(),
        "a statement never waits"
    );
    let mut later = own.clone();
    later.extend([act(2, "payee"), act(3, "statement"), act(4, "payer")]);
    assert_eq!(
        gate(&h, &later, act(5, "perform")),
        allowed(),
        "only the first act waits"
    );
    let mut before = own.clone();
    before.extend([act(2, "payee"), act(3, "statement")]);
    assert_eq!(gate(&h, &before, act(4, "payer")), gated(v1::GATE_NO_PROOF));
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let open = ActGate {
        gated: true,
        allowed: true,
        ..Default::default()
    };
    assert_eq!(gate(&h, &own, act(2, "payer")), open);
    h.buyer.reopen();
    assert_eq!(gate(&h, &own, act(2, "perform")), open, "after a restart");
}

#[test]
fn gate_act_without_a_layer_is_unguarded() {
    let h = handshake(|_| {});
    let (ag, _, _) = gate_book(&h, false);
    let g = h
        .buyer
        .profile
        .gate_act(&ActGateInput {
            own: vec![record(&ag)],
            act: act(2, "payer"),
            counterpart_agreement: String::new(),
            commitment_shelf_log: String::new(),
        })
        .unwrap();
    assert_eq!(
        g,
        ActGate {
            gated: true,
            allowed: true,
            unguarded: true,
            reason: v1::GATE_NO_LAYER.into()
        }
    );
    assert_eq!(
        serde_json::to_value(&g).unwrap(),
        json!({"gated": true, "allowed": true, "unguarded": true, "reason": "counterparty_no_layer"})
    );
    assert_eq!(
        serde_json::to_value(allowed()).unwrap(),
        json!({"gated": false, "allowed": true})
    );
}

#[test]
fn gate_act_refuses_a_proof_of_another_shelf() {
    let h = handshake(|a| a["body"]["shelf_log_id"] = json!("deal-cadence/ffffffffffffffff"));
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let (ag, _, _) = gate_book(&h, true);
    assert_eq!(
        gate(&h, &[record(&ag)], act(2, "payer")),
        gated(v1::GATE_NO_PROOF)
    );
}

#[test]
fn gate_act_refuses_another_book() {
    let h = handshake(|_| {});
    let (ag, _, _) = gate_book(&h, true);
    let own = vec![record(&ag)];
    let mut other = act(2, "payer");
    other.block.deal_id = "deal-ffffffffffffffff".into();
    let cases: Vec<(&str, Vec<Record>, Record)> = vec![
        ("an empty book", vec![], act(2, "payer")),
        (
            "a book not opening an agreement",
            vec![act(1, "statement")],
            act(2, "payer"),
        ),
        ("an act of another deal", own.clone(), other.clone()),
        (
            "a record of another deal",
            [own.clone(), vec![other.clone()]].concat(),
            act(2, "payer"),
        ),
        (
            "another deal after the first act",
            [own.clone(), vec![act(2, "payer"), other.clone()]].concat(),
            act(4, "payer"),
        ),
        (
            "another deal, a payee leg to seal",
            [own.clone(), vec![other.clone()]].concat(),
            act(3, "payee"),
        ),
    ];
    for (name, own, act) in cases {
        let got = h.buyer.profile.gate_act(&ActGateInput {
            own,
            act,
            counterpart_agreement: h.agreement_id.clone(),
            commitment_shelf_log: String::new(),
        });
        assert!(got.is_err(), "{name}");
    }
}

#[test]
fn gate_act_holds_the_acceptor() {
    let h = handshake(|_| {});
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let (_, buyer_id, buyer_pair) = gate_book(&h, true);
    let input = ActGateInput {
        own: vec![record(&h.agreement)],
        act: act(2, "perform"),
        counterpart_agreement: buyer_id.clone(),
        commitment_shelf_log: String::new(),
    };
    h.seller.receive(Some(&buyer_pair), None);
    assert_eq!(
        h.seller.profile.gate_act(&input).unwrap(),
        gated(v1::GATE_NO_PROOF),
        "no offerer proof yet"
    );
    let proof = h.buyer.stamp(&buyer_id);
    h.seller.receive(Some(&buyer_pair), Some(&proof));
    assert_eq!(
        h.seller.profile.gate_act(&input).unwrap(),
        ActGate {
            gated: true,
            allowed: true,
            ..Default::default()
        }
    );
}

#[test]
fn gate_act_refuses_without_an_agreement_or_the_counterpartys() {
    let h = handshake(|_| {});
    let err = h
        .buyer
        .profile
        .gate_act(&ActGateInput {
            own: vec![record(&h.agreement)],
            act: act(2, "payer"),
            counterpart_agreement: String::new(),
            commitment_shelf_log: String::new(),
        })
        .unwrap_err();
    assert!(err.to_string().contains("sealed no agreement"), "{err}");
    gate_book(&h, true);
    let mut theirs = h.agreement.clone();
    theirs["body"]["counterparty_layer"] = json!(false);
    theirs["body"]["counterparty_key"] = Value::Null;
    let err = h
        .buyer
        .profile
        .gate_act(&ActGateInput {
            own: vec![record(&theirs)],
            act: act(2, "payer"),
            counterpart_agreement: h.agreement_id.clone(),
            commitment_shelf_log: String::new(),
        })
        .unwrap_err();
    assert!(
        err.to_string().contains("the holder's own agreement"),
        "{err}"
    );
}

#[test]
fn a_profile_with_no_shelf_or_a_released_one_errs() {
    let p = v1::ReceiveProfile::new();
    assert!(p
        .can_act(&CanActInput {
            deal_id: DEAL.into(),
            ..Default::default()
        })
        .is_err());
    let party = Party::new("solo");
    let weak = Arc::new(v1::ReceiveProfile::new());
    weak.set_shelf(&party.shelf);
    let Party { shelf, cadence, .. } = party;
    drop(cadence);
    drop(shelf);
    assert!(
        weak.can_act(&CanActInput {
            deal_id: DEAL.into(),
            ..Default::default()
        })
        .is_err(),
        "the profile does not keep its shelf alive"
    );
}
