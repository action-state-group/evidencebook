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
    status: Option<Arc<v1::DealStatus>>,
    shelf: Shelf,
    cadence: Cadence,
}

impl Party {
    fn new(name: &str) -> Party {
        Party::with_signer(name, &format!("{name} record key"))
    }

    fn with_signer(name: &str, signer: &str) -> Party {
        Party::build(name, signer, None, None)
    }

    /// A party that checkpoints with its record key, as an adjudicator does
    /// (Go `newAdjudicator`): the key its agreements name is the key its
    /// case book's stamp proof is signed with.
    fn adjudicator(name: &str) -> Party {
        let signer = format!("{name} record key");
        Party::build(name, &signer, None, Some(&signer))
    }

    /// A party whose cadence asks a [`v1::DealStatus`] for each book.
    fn with_status(name: &str) -> Party {
        Party::build(
            name,
            &format!("{name} record key"),
            Some(Arc::new(v1::DealStatus::new(None))),
            None,
        )
    }

    fn build(
        name: &str,
        signer: &str,
        status: Option<Arc<v1::DealStatus>>,
        checkpoint: Option<&str>,
    ) -> Party {
        let checkpoint = checkpoint.map_or_else(|| format!("{name} checkpoint key"), String::from);
        let dir = Arc::new(tempfile::tempdir().unwrap());
        let path = dir.path().join("shelf.db");
        let clock = Arc::new(Mutex::new(Time::parse("2026-11-01T09:00:00Z").unwrap()));
        let profile = Arc::new(v1::ReceiveProfile::new());
        let (shelf, cadence) = open(
            name,
            &path,
            &key_of(signer),
            &key_of(&checkpoint),
            &clock,
            &profile,
            status.as_ref(),
        );
        let p = Party {
            _dir: dir,
            name: name.into(),
            path,
            signer: key_of(signer),
            checkpoint: key_of(&checkpoint),
            clock,
            profile,
            status,
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
            self.status.as_ref(),
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
    status: Option<&Arc<v1::DealStatus>>,
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
    if let Some(s) = status {
        s.set_shelf(&shelf);
    }
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
            status: status.map(|s| s.clone() as Arc<dyn evidencebook::shelf::BookStatus>),
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
    handshake_of(Party::new("buyer"), Party::new("seller"), edit)
}

fn handshake_of(buyer: Party, seller: Party, edit: impl FnOnce(&mut Value)) -> Handshake {
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

/// The deal's case log for `adjudicator` (Go `caseLogOf`).
fn case_log_of(h: &Handshake, adjudicator: &Party) -> String {
    v1::case_log_id(&digest(&h.offer), &adjudicator.key())
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
    let adjudicator = Party::adjudicator("adjudicator");
    let h = handshake(|a| a["body"]["adjudicator"]["key_id"] = json!(adjudicator.key()));
    let mut ruling = fixture("case-book/01-ruling.json");
    ruling["body"]["keys"] = json!({"buyer": h.buyer.key(), "seller": h.seller.key()});
    ruling["body"]["read"] = json!({"buyer": null, "seller": h.agreement_id});
    let case_log = case_log_of(&h, &adjudicator);
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    assert!(h.seller.receive(Some(&pair), None).record.unwrap().stored);
    // On any log but the deal's case log a ruling is refused (Go
    // `TestRulingOnAnotherLogIsRefused`).
    let other_log = format!("case/{}", hex::encode(&seed("another case log")[..16]));
    let (_, elsewhere) = adjudicator.seal(&other_log, &ruling);
    assert!(h.seller.refused(&elsewhere));
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

/// A handshake whose agreements name `adjudicator`, and a final ruling of
/// the deal against the seller, in the adjudicator's case book (Go
/// `rulingCase`).
fn ruling_case(buyer: Party, seller: Party) -> (Handshake, Party, Value, String) {
    let adjudicator = Party::adjudicator("adjudicator");
    let key = adjudicator.key();
    let h = handshake_of(buyer, seller, |a| {
        a["body"]["adjudicator"]["key_id"] = json!(key)
    });
    let mut ruling = fixture("case-book/01-ruling.json");
    ruling["body"]["keys"] = json!({"buyer": h.buyer.key(), "seller": h.seller.key()});
    ruling["body"]["read"] = json!({"buyer": null, "seller": h.agreement_id});
    ruling["body"]["final"] = json!(true);
    record(&ruling);
    let case_log = case_log_of(&h, &adjudicator);
    (h, adjudicator, ruling, case_log)
}

/// The x-deal-v1 record at `seq` of `book`, as a verified book pair.
fn read_back(book: &evidencebook::shelf::ShelfBook, seq: u64) -> v1::BookRead {
    let r = book.record_by_seq(seq).unwrap();
    let digest = r.header["payload_commitments"][0]
        .as_str()
        .unwrap()
        .to_string();
    let payload = book.resolve_payload(&r.record_id, &digest).unwrap();
    let pair = v1::book_pair(&r.header_bytes, &r.capsule, &r.envelope, &payload).unwrap();
    v1::verify_book(&pair).unwrap()
}

/// Makes every book append fail before it stores the record, until dropped
/// (Go's `nameRulingsBeforeAppend` returning an error).
struct BlockAppends(Connection);

impl BlockAppends {
    fn on(p: &Party) -> BlockAppends {
        let side = Connection::open(&p.path).unwrap();
        side.busy_timeout(Duration::from_secs(5)).unwrap();
        side.execute_batch(
            "CREATE TRIGGER block_appends BEFORE INSERT ON book_records BEGIN SELECT RAISE(ABORT, 'the disk is full'); END;",
        )
        .unwrap();
        BlockAppends(side)
    }
}

impl Drop for BlockAppends {
    fn drop(&mut self) {
        self.0.execute_batch("DROP TRIGGER block_appends;").unwrap();
    }
}

/// A counted ruling the holder's book does not name is named by the
/// receive call itself, after the record before it, once (PROFILE §5;
/// evidencebook#527; Go `TestNameRulings`).
#[test]
fn name_rulings_on_receive() {
    let (h, adjudicator, ruling, case_log) = ruling_case(Party::new("buyer"), Party::new("seller"));
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    let res = h.seller.receive(Some(&pair), None);
    assert!(res.record.unwrap().stored);
    assert!(res.changes.grew.contains(&DEAL.to_string()));
    let book = h.seller.shelf.open(&h.seller_log).unwrap();
    assert_eq!(
        book.size(),
        2,
        "named in the seller's own book by the receive call"
    );
    let r2 = book.record_by_seq(2).unwrap();
    assert_eq!(r2.header["record_type"], json!("deal_step"));
    assert_eq!(r2.header["epistemic_type"], json!("producer_claim"));
    let read = read_back(&book, 2);
    let v1::Body::Statement(st) = &read.record.body else {
        panic!("a statement");
    };
    assert_eq!(
        st.names.as_ref().unwrap().ruling.as_ref().unwrap().digest,
        digest(&ruling)
    );
    assert_eq!(read.prev, h.agreement_id);
    let again = v1::name_rulings(&h.seller.shelf, &[DEAL.to_string()], h.seller.now()).unwrap();
    assert!(again.is_empty(), "named once");
    h.seller.receive(Some(&pair), None);
    assert_eq!(book.size(), 2, "the same ruling again names nothing");
    let other = v1::name_rulings(
        &h.seller.shelf,
        &["deal-ffffffffffffffff".to_string()],
        h.seller.now(),
    )
    .unwrap();
    assert!(other.is_empty(), "another deal's book is passed over");
}

#[test]
fn book_entry_is_the_deal_book_entry() {
    let first = record(&fixture("buyer-book/01-agreement.json"));
    let second = record(&fixture("buyer-book/02-payment.json"));
    assert!(
        v1::book_entry(&second, "").is_err(),
        "past seq 1 a record cites the one before it"
    );
    let e = v1::book_entry(&second, "ab").unwrap();
    assert_eq!(e.links.len(), 1);
    assert_eq!(
        (e.links[0].link_type, e.links[0].target.as_str()),
        (LinkType::Cites, "ab")
    );
    assert_eq!(e.record_type, "deal_step");
    assert_eq!(e.subject_ref.as_deref(), Some(DEAL));
    let raw = jcs(&fixture("buyer-book/02-payment.json")).unwrap();
    let (payload, d) = v1::encode(&raw).unwrap();
    assert_eq!(e.payloads, vec![payload]);
    assert_eq!(
        d,
        v1::digest(&raw).unwrap(),
        "the one payload's digest is the record digest"
    );
    assert!(
        v1::book_entry(&first, "").unwrap().links.is_empty(),
        "seq 1 cites nothing"
    );
}

/// The naming statement's block (Go `TestNameRulingsBlock`): prev and
/// agreement_ref the buyer's agreement, seen the seller's agreement, the
/// last of its counted prefix.
#[test]
fn name_rulings_block() {
    let (h, adjudicator, ruling, case_log) = ruling_case(Party::new("buyer"), Party::new("seller"));
    let mut ba = buyer_agreement(&h, true);
    ba["body"]["adjudicator"]["key_id"] = json!(adjudicator.key());
    record(&ba);
    let log = "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0";
    h.buyer.seal(log, &ba);
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    h.buyer.receive(Some(&pair), None);
    let book = h.buyer.shelf.open(log).unwrap();
    assert_eq!(book.size(), 2);
    let b = read_back(&book, 2).record.block;
    let ag = digest(&ba);
    assert_eq!(b.prev.unwrap().digest, ag);
    assert_eq!(b.agreement_ref.unwrap().digest, ag);
    assert_eq!(
        b.seen.value(),
        Some(&h.agreement_id),
        "seen: the seller's agreement"
    );
    assert_eq!(b.seq, 2);
}

/// Marks a record of `book` withheld with a lifecycle record.
fn withhold(book: &evidencebook::shelf::ShelfBook, record_id: &str) {
    let mut lifecycle = BookEntry::new("lifecycle", EpistemicType::ProducerClaim);
    lifecycle.links = vec![Link {
        link_type: LinkType::Cites,
        target: record_id.into(),
    }];
    lifecycle.statement = json!({"retention_state": "WITHHELD", "reason": "test"});
    book.append(&lifecycle).unwrap();
}

/// A book with a record not readable whole is left alone (Go
/// `TestNameRulingsLeavesAPartBook`).
#[test]
fn name_rulings_leaves_a_part_book() {
    let (h, adjudicator, ruling, case_log) = ruling_case(Party::new("buyer"), Party::new("seller"));
    let book = h.seller.shelf.open(&h.seller_log).unwrap();
    let next = record(&settle(2, &h.agreement, &h.agreement, &h.agreement_id));
    let r2 = book
        .append_at(2, &v1::book_entry(&next, &h.agreement_id).unwrap())
        .unwrap();
    withhold(&book, &r2.record_id);
    let size = book.size();
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    h.seller.receive(Some(&pair), None);
    assert_eq!(book.size(), size, "no statement from a part read");
}

/// A ruling of another deal in the same case log is not this deal's.
#[test]
fn name_rulings_only_the_deals_rulings() {
    let (h, adjudicator, mut ruling, case_log) =
        ruling_case(Party::new("buyer"), Party::new("seller"));
    ruling["x-deal-v1"]["deal_id"] = json!("deal-ffffffffffffffff");
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    let _ = h.seller.shelf.receive(Some(&pair), None);
    assert_eq!(h.seller.shelf.open(&h.seller_log).unwrap().size(), 1);
}

/// A naming that fails is the receive call's error and the ruling stays
/// held; the same ruling sent again (a duplicate) names it; and
/// name_all_rulings seals a naming left behind, once (Go
/// `TestNameRulingsRetriedOnADuplicate`, `TestNameAllRulings`).
#[test]
fn a_failed_naming_is_retried() {
    let (h, adjudicator, ruling, case_log) = ruling_case(Party::new("buyer"), Party::new("seller"));
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    let book = h.seller.shelf.open(&h.seller_log).unwrap();
    {
        let _block = BlockAppends::on(&h.seller);
        let e = h.seller.shelf.receive(Some(&pair), None).unwrap_err();
        assert!(e.to_string().contains("after receive"), "{e}");
        let out = e.result.record.as_ref().unwrap();
        assert!(out.stored, "the ruling stays committed");
        assert_eq!(out.deal_id, DEAL);
        assert_eq!(book.size(), 1);
    }
    let again = h.seller.receive(Some(&pair), None);
    assert!(again.record.unwrap().duplicate);
    assert!(again.changes.grew.is_empty(), "nothing grew");
    assert_eq!(book.size(), 2, "the duplicate retried the naming");

    let (h, adjudicator, ruling, case_log) = ruling_case(Party::new("buyer"), Party::new("seller"));
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    {
        let _block = BlockAppends::on(&h.seller);
        assert!(h.seller.shelf.receive(Some(&pair), None).is_err());
    }
    let sealed = v1::name_all_rulings(&h.seller.shelf, h.seller.now()).unwrap();
    assert_eq!(sealed.len(), 1);
    assert!(v1::name_all_rulings(&h.seller.shelf, h.seller.now())
        .unwrap()
        .is_empty());
}

/// The last status opening of the book whose first record is `book_id`.
fn last_status(p: &Party, book_id: &str) -> Value {
    p.shelf
        .openings(book_id, "counterparty")
        .unwrap()
        .into_iter()
        .filter_map(|f| f.status_opening)
        .next_back()
        .expect("a status opening")
        .value
}

/// A naming that failed, with nothing received after it, is sealed by the
/// holder's next stamp: DealStatus's before_stamp runs before the stamp
/// takes its values (Go `TestNameRulingsAtTheNextStamp`).
#[test]
fn name_rulings_at_the_next_stamp() {
    let (h, adjudicator, ruling, case_log) =
        ruling_case(Party::new("buyer"), Party::with_status("seller"));
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    {
        let _block = BlockAppends::on(&h.seller);
        assert!(h.seller.shelf.receive(Some(&pair), None).is_err());
    }
    h.seller.tick();
    let res = h.seller.cadence.stamp(h.seller.now()).unwrap();
    assert_eq!(res.before_stamp, None);
    assert_eq!(h.seller.shelf.open(&h.seller_log).unwrap().size(), 2);
    let v = last_status(&h.seller, &h.agreement_id);
    assert!(
        v.get("rulings_unnamed").is_none(),
        "that stamp's value reads the ruling named: {v}"
    );
}

/// A book not readable whole is never written to, and the stamp's status
/// value counts the ruling unnamed (Go
/// `TestNameRulingsUnreadableBookIsCounted`).
#[test]
fn an_unreadable_book_counts_its_ruling_unnamed() {
    let (h, adjudicator, ruling, case_log) =
        ruling_case(Party::new("buyer"), Party::with_status("seller"));
    let book = h.seller.shelf.open(&h.seller_log).unwrap();
    let next = record(&settle(2, &h.agreement, &h.agreement, &h.agreement_id));
    let r2 = book
        .append_at(2, &v1::book_entry(&next, &h.agreement_id).unwrap())
        .unwrap();
    withhold(&book, &r2.record_id);
    let size = book.size();
    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    h.seller.receive(Some(&pair), None);
    h.seller.tick();
    let res = h.seller.cadence.stamp(h.seller.now()).unwrap();
    assert_eq!(res.before_stamp, None);
    assert_eq!(
        book.size(),
        size,
        "nothing sealed into a book not readable whole"
    );
    assert_eq!(
        last_status(&h.seller, &h.agreement_id)["rulings_unnamed"],
        json!(1)
    );
}

/// A v1 deal book's status at a stamp (Go `TestStatusProviderAtAStamp`,
/// without the payment check, which `testdata/deal-v1/payment` holds):
/// the fact's profile, the value's role and proof, the stamp a record not
/// timed yet is read at, and the link value naming the counterparty's last
/// counted record.
#[test]
fn deal_status_at_a_stamp() {
    let h = handshake_of(Party::with_status("buyer"), Party::new("seller"), |a| {
        a["body"]["witnesses"] = json!([])
    });
    let mut ba = buyer_agreement(&h, true);
    ba["body"]["witnesses"] = json!([]);
    let (buyer_id, _) = h.buyer.seal(&log_of("buyer log"), &ba);
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let next = settle(2, &h.agreement, &h.agreement, &h.agreement_id);
    let (next_id, next_pair) = h.seller.seal(&h.seller_log, &next);
    h.buyer.receive(Some(&next_pair), None);
    h.buyer.tick();
    let tick = h.buyer.cadence.stamp(h.buyer.now()).unwrap().tick.unwrap();
    let facts = h.buyer.shelf.openings(&buyer_id, "counterparty").unwrap();
    let last = facts.last().unwrap();
    assert_eq!(last.fact["profile"], json!(v1::PROFILE));
    let v = &last.status_opening.as_ref().unwrap().value;
    assert_eq!(v["role"], json!("buyer"));
    assert_eq!(
        v["counterparty_proof_held"],
        json!(true),
        "the seller's proof covers its agreement"
    );
    let raw = v.to_string();
    assert!(raw.contains(&format!("{{\"stamp\":{}}}", tick.n)), "{raw}");
    assert!(!raw.contains(&h.agreement_id) && !raw.contains(&buyer_id));
    let link = &last.link_opening.as_ref().unwrap().value;
    assert_eq!(
        link["last_received"],
        json!({"log_id": h.seller_log, "size": 2, "record_digest": next_id})
    );
}

/// The report of a deal book from the shelf (Go `TestStatusProviderReport`):
/// refused before a stamp checkpoints the book; then resting on the
/// checkpoint, current until a record arrives that no stamp has stated;
/// refused for no such book, and for a book holding a record not readable
/// as x-deal-v1. A report writes nothing.
#[test]
fn deal_status_report() {
    let h = handshake_of(Party::with_status("buyer"), Party::new("seller"), |a| {
        a["body"]["witnesses"] = json!([])
    });
    let mut ba = buyer_agreement(&h, true);
    ba["body"]["witnesses"] = json!([]);
    let buyer_log = log_of("buyer log");
    h.buyer.seal(&buyer_log, &ba);
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let status = h.buyer.status.clone().unwrap();
    let book = h.buyer.shelf.open(&buyer_log).unwrap();
    let size = book.size();
    let copy = v1::ReportCopy {
        audience: "keep".into(),
        nonce: "a".repeat(64),
        referee: String::new(),
        as_of: h.buyer.now().rfc3339(),
        text: None,
    };
    let err = status.report(&buyer_log, &copy).unwrap_err();
    assert!(
        err.to_string().contains("no checkpoint at its size"),
        "{err}"
    );
    assert_eq!(book.size(), size, "a report writes nothing");
    h.buyer.tick();
    h.buyer.cadence.stamp(h.buyer.now()).unwrap();
    let r = serde_json::to_value(status.report(&buyer_log, &copy).unwrap()).unwrap();
    assert_eq!(book.size(), size, "a report writes nothing");
    let cp = book.checkpoints().unwrap().pop().unwrap();
    assert_eq!(
        r["deal_checkpoint"],
        json!({"log_id": buyer_log, "mmr_size": cp.tree_size, "root": cp.root})
    );
    assert_eq!(r["rests_on"]["own"]["size"], json!(size));
    assert_eq!(r["status_fact"]["current"], json!(true));
    assert_eq!(r["rests_on"]["counterparty"]["log_id"], json!(h.seller_log));
    let next = settle(2, &h.agreement, &h.agreement, &h.agreement_id);
    let (_, next_pair) = h.seller.seal(&h.seller_log, &next);
    h.buyer.receive(Some(&next_pair), None);
    let r = serde_json::to_value(status.report(&buyer_log, &copy).unwrap()).unwrap();
    assert_eq!(
        r["status_fact"]["current"],
        json!(false),
        "a record no stamp has stated yet"
    );
    assert_eq!(r["rests_on"]["counterparty"]["counted"], json!(2));
    assert!(status
        .report("deal/ffffffffffffffffffffffffffffffff", &copy)
        .is_err());
    withhold(&book, &book.record_by_seq(1).unwrap().record_id);
    h.buyer.tick();
    h.buyer.cadence.stamp(h.buyer.now()).unwrap();
    let err = status.report(&buyer_log, &copy).unwrap_err();
    assert!(!matches!(err, ShelfError::Busy(_)), "{err}");
}

/// The report with its book from the shelf (Go `TestReportWithBook`, without
/// the payment leg): the book carries the holder's agreement and exactly the
/// records the report's rows rely on, each its own record's pair, read in the
/// report's snapshot; every copy carries the same records, so one whose rows
/// are withheld still does; `report` is the report alone.
#[test]
fn deal_status_report_with_book() {
    let h = handshake_of(Party::with_status("buyer"), Party::new("seller"), |a| {
        a["body"]["witnesses"] = json!([])
    });
    let mut ba = buyer_agreement(&h, true);
    ba["body"]["witnesses"] = json!([]);
    let buyer_log = log_of("buyer log");
    h.buyer.seal(&buyer_log, &ba);
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let status = h.buyer.status.clone().unwrap();
    let copy = |audience: &str, referee: &str| v1::ReportCopy {
        audience: audience.into(),
        nonce: "a".repeat(64),
        referee: referee.into(),
        as_of: h.buyer.now().rfc3339(),
        text: None,
    };
    let err = status
        .report_with_book(&buyer_log, &copy("keep", ""))
        .unwrap_err();
    assert!(
        err.to_string().contains("no checkpoint at its size"),
        "{err}"
    );
    h.buyer.tick();
    h.buyer.cadence.stamp(h.buyer.now()).unwrap();
    let next = settle(2, &h.agreement, &h.agreement, &h.agreement_id);
    let (_, next_pair) = h.seller.seal(&h.seller_log, &next);
    h.buyer.receive(Some(&next_pair), None);
    h.buyer.tick();
    h.buyer.cadence.stamp(h.buyer.now()).unwrap();
    let book = h.buyer.shelf.open(&buyer_log).unwrap();
    let (keep, kb) = status
        .report_with_book(&buyer_log, &copy("keep", ""))
        .unwrap();
    assert_eq!(keep.rows_state, v1::ROWS_LISTED);
    let first = book.record_by_seq(1).unwrap();
    let agreement = kb.agreement.clone().unwrap();
    assert_eq!(
        v1::verify_book(&serde_json::to_vec(&agreement).unwrap())
            .unwrap()
            .record_id,
        first.record_id,
        "the agreement is the holder's record at seq 1"
    );
    let relied = kb.relied_on.clone().unwrap();
    let rows: std::collections::BTreeSet<&str> = keep
        .rows
        .iter()
        .flat_map(|r| r.relied_on.iter().map(|s| s.record.as_str()))
        .collect();
    assert_eq!(
        relied.keys().map(String::as_str).collect::<Vec<_>>(),
        rows.iter().copied().collect::<Vec<_>>(),
        "exactly the records the rows rely on"
    );
    assert!(!relied.is_empty(), "the fixture's rows rely on records");
    for (id, pair) in &relied {
        let b = v1::verify_book(&serde_json::to_vec(pair).unwrap()).unwrap();
        assert_eq!(&b.record_id, id, "each key is its pair's record id");
    }
    let adj = status
        .report_with_book(&buyer_log, &copy("adjudicator", "Lamp Court"))
        .unwrap();
    assert_eq!(adj.0.rows_state, v1::ROWS_WITHHELD);
    assert!(adj.0.rows.is_empty());
    assert_eq!(adj.1, kb, "a withheld copy carries the keep copy's book");
    let alone = status.report(&buyer_log, &copy("keep", "")).unwrap();
    assert_eq!(alone, keep, "report is the report alone");

    // Each copy's book verifies with its report sealed by the holder, as Go's
    // TestReportWithBook does for all three.
    let seal = |r: &v1::Report| -> Vec<u8> {
        let payload = v1::report_payload(r).unwrap();
        let producer = evidencebook::seal::Producer {
            operator: "buyer",
            developer: "xdealv1-test",
            signer: &h.buyer.signer,
        };
        let sealed = evidencebook::seal::seal_payload(
            evidencebook::deal::REPORT_ACTION_ID,
            "2026-11-21T12:00:00Z",
            &payload,
            &Default::default(),
            &producer,
        )
        .unwrap();
        v1::report_pair(&sealed.capsule, &sealed.envelope, &payload).unwrap()
    };
    for (name, (r, b)) in [
        ("keep", (keep, kb)),
        (
            "counterparty",
            status
                .report_with_book(&buyer_log, &copy("counterparty", ""))
                .unwrap(),
        ),
        ("adjudicator", adj),
    ] {
        let pair = seal(&r);
        let agreement = serde_json::to_vec(b.agreement.as_ref().unwrap()).unwrap();
        let sealed_report = v1::verify_report(&pair, &agreement).unwrap().capsule_id;
        let ext = v1::ReportBundle {
            sealed_report: Some(sealed_report),
            book: Some(b),
        };
        let read = v1::verify_report_bundle(&pair, &serde_json::to_vec(&ext).unwrap())
            .unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(read.key_id, h.buyer.key(), "{name}");
    }
}

/// A handshake between `buyer` and `seller` whose agreement names
/// `adjudicator` (`edit` changes it further), and its final ruling against
/// the seller, ready to seal on the deal's case log (Go `caseBookHolder`).
fn case_book_holder_of(
    buyer: Party,
    seller: Party,
    adjudicator: Party,
    edit: impl FnOnce(&mut Value),
) -> (Handshake, Party, Value, String) {
    let key = adjudicator.key();
    let h = handshake_of(buyer, seller, |a| {
        a["body"]["adjudicator"]["key_id"] = json!(key);
        edit(a);
    });
    let mut ruling = fixture("case-book/01-ruling.json");
    ruling["body"]["keys"] = json!({"buyer": h.buyer.key(), "seller": h.seller.key()});
    ruling["body"]["read"] = json!({"buyer": null, "seller": h.agreement_id});
    ruling["body"]["final"] = json!(true);
    record(&ruling);
    let case_log = case_log_of(&h, &adjudicator);
    (h, adjudicator, ruling, case_log)
}

fn case_book_holder(adjudicator: Party) -> (Handshake, Party, Value, String) {
    case_book_holder_of(
        Party::new("buyer"),
        Party::new("seller"),
        adjudicator,
        |_| {},
    )
}

/// A case book's stamp proof is bound to the adjudicator key the agreement
/// names: the adjudicator checkpoints with that key, and the holder keeps
/// the proof beside the ruling (Go `TestCaseBookProofBindsToTheAdjudicatorKey`).
#[test]
fn case_book_proof_binds_to_the_adjudicator_key() {
    let (h, adjudicator, ruling, case_log) = case_book_holder(Party::adjudicator("adjudicator"));
    let (id, pair) = adjudicator.seal(&case_log, &ruling);
    let proof = adjudicator.stamp(&id);
    let res = h.seller.receive(Some(&pair), Some(&proof));
    let p = res.proof.expect("a proof result");
    assert!(p.accepted, "refused: {:?}", p.refused);
}

/// A case book proof under a checkpoint key that is not the adjudicator's
/// record key binds to nothing and is refused; the ruling itself is kept
/// (Go `TestCaseBookProofUnderAnotherKeyIsRefused`).
#[test]
fn case_book_proof_under_another_key_is_refused() {
    let (h, adjudicator, ruling, case_log) = case_book_holder(Party::new("adjudicator"));
    let (id, pair) = adjudicator.seal(&case_log, &ruling);
    let proof = adjudicator.stamp(&id);
    let e = h
        .seller
        .shelf
        .receive(Some(&pair), Some(&proof))
        .unwrap_err();
    assert!(e
        .errors()
        .iter()
        .any(|e| matches!(e, ShelfError::Refused(_))));
    assert!(e.to_string().contains("may not push a stamp proof"), "{e}");
    let res = h.seller.receive(Some(&pair), None);
    let r = res.record.unwrap();
    assert!(r.stored || r.duplicate);
    assert!(r.counted);
}

/// A judge that gives each side another ruling at the same place of the
/// case log shows as a fork once one side forwards what it received: the
/// holder then holds both, its proofs for that log are refused, and the row
/// has no effective ruling (Go `TestForwardedRulingShowsAFork`).
#[test]
fn forwarded_ruling_shows_a_fork() {
    let (h, adjudicator, ruling, case_log) = case_book_holder_of(
        Party::with_status("buyer"),
        Party::new("seller"),
        Party::adjudicator("adjudicator"),
        |a| a["body"]["witnesses"] = json!([]),
    );
    // The buyer holds both agreements, so its deal has started.
    let mut ba = buyer_agreement(&h, true);
    ba["body"]["witnesses"] = json!([]);
    ba["body"]["adjudicator"]["key_id"] = json!(adjudicator.key());
    let buyer_log = log_of("buyer log");
    h.buyer.seal(&buyer_log, &ba);
    h.buyer.receive(Some(&h.agreement_pair), Some(&h.proof));
    let status = h.buyer.status.clone().unwrap();
    let report_row = |now: String| {
        let copy = v1::ReportCopy {
            audience: "keep".into(),
            nonce: "a".repeat(64),
            referee: String::new(),
            as_of: now,
            text: None,
        };
        let r = status.report(&buyer_log, &copy).unwrap();
        r.rows
            .into_iter()
            .find(|row| row.id == "as_described")
            .expect("row as_described")
    };

    let (id_a, pair_a) = adjudicator.seal(&case_log, &ruling);
    let proof_a = adjudicator.stamp(&id_a);
    let res = h.buyer.receive(Some(&pair_a), Some(&proof_a));
    let p = res.proof.expect("a proof result");
    assert!(p.accepted, "refused: {:?}", p.refused);
    h.buyer.tick();
    h.buyer.cadence.stamp(h.buyer.now()).unwrap();
    assert!(
        report_row(h.buyer.now().rfc3339()).ruling.is_some(),
        "one ruling held: it is effective"
    );

    // The seller got ruling B at the same place, and forwards it.
    let mut other = ruling.clone();
    other["body"]["against"] = json!("buyer");
    other["body"]["value"] = json!("met");
    let second = Party::adjudicator("adjudicator");
    let (_, pair_b) = second.seal(&case_log, &other);
    h.buyer.receive(Some(&pair_b), None);
    h.buyer.tick();
    h.buyer.cadence.stamp(h.buyer.now()).unwrap();
    assert!(
        report_row(h.buyer.now().rfc3339()).ruling.is_none(),
        "two different first final rulings: none is effective"
    );

    // A forked case book makes no checkpoint claim.
    assert_eq!(sender(&h.buyer, &adjudicator.key(), &case_log), None);
}

/// The adjudicator, once it ruled on a deal, takes each party's book: the
/// agreement with its stamp proof, and the statement naming its ruling.
/// Before it ruled, or for a deal it did not rule on, it takes nothing (Go
/// `TestAdjudicatorTakesThePartiesBooksAfterItsRuling`).
#[test]
fn adjudicator_takes_the_parties_books_after_its_ruling() {
    let (h, adjudicator, ruling, case_log) = case_book_holder(Party::adjudicator("adjudicator"));

    // Not yet ruled: the seller's agreement is refused.
    assert!(adjudicator.refused(&h.agreement_pair), "no case book yet");

    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    h.seller.receive(Some(&pair), None); // names the ruling at seq 2 of the seller's book
    let seller_book = h.seller.shelf.open(&h.seller_log).unwrap();
    assert_eq!(seller_book.size(), 2);

    let res = adjudicator.receive(Some(&h.agreement_pair), Some(&h.proof));
    assert!(res.record.unwrap().stored);
    let p = res.proof.expect("a proof result");
    assert!(p.accepted, "refused: {:?}", p.refused);
    let naming = read_back(&seller_book, 2);
    let pair_2 = book_pair_at(&seller_book, 2);
    let res = adjudicator.receive(Some(&pair_2), None);
    assert!(res.record.unwrap().stored);
    let st = match &naming.record.body {
        v1::Body::Statement(s) => s,
        b => panic!("not a statement: {b:?}"),
    };
    assert_eq!(
        st.names
            .as_ref()
            .expect("names")
            .ruling
            .as_ref()
            .expect("ruling ref")
            .digest,
        digest(&ruling),
        "the seller entered this ruling"
    );

    // The buyer's agreement and its proof are taken too: either party the
    // ruling names.
    let buyer_agreement_of = |p: &Party| {
        let mut ba = buyer_agreement(&h, true);
        ba["body"]["checkpoint_key"] = json!(p.checkpoint_key());
        ba["body"]["adjudicator"]["key_id"] = json!(adjudicator.key());
        ba
    };
    let (buyer_id, buyer_pair) = h.buyer.seal(
        "deal/b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0",
        &buyer_agreement_of(&h.buyer),
    );
    let buyer_proof = h.buyer.stamp(&buyer_id);
    let res = adjudicator.receive(Some(&buyer_pair), Some(&buyer_proof));
    assert!(res.record.unwrap().stored);
    let p = res.proof.expect("a proof result");
    assert!(p.accepted, "refused: {:?}", p.refused);

    // A third key's agreement of the same deal, naming the same adjudicator,
    // is no party's: refused, and so is its proof.
    let intruder = Party::new("intruder");
    let (intruder_id, intruder_pair) = intruder.seal(
        "deal/c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0",
        &buyer_agreement_of(&intruder),
    );
    let intruder_proof = intruder.stamp(&intruder_id);
    assert!(
        adjudicator.refused(&intruder_pair),
        "a key the ruling does not name"
    );
    let e = adjudicator
        .shelf
        .receive(Some(&intruder_pair), Some(&intruder_proof))
        .unwrap_err();
    assert!(e
        .errors()
        .iter()
        .any(|e| matches!(e, ShelfError::Refused(_))));

    // Another adjudicator, which ruled on nothing, takes none of it.
    let stranger = Party::adjudicator("another adjudicator");
    assert!(stranger.refused(&h.agreement_pair));
}

/// The book pair of the record at `seq` of `book`, as it is sent (Go
/// `bookPairAt`).
fn book_pair_at(book: &evidencebook::shelf::ShelfBook, seq: u64) -> Vec<u8> {
    let r = book.record_by_seq(seq).unwrap();
    let digest = r.header["payload_commitments"][0].as_str().unwrap();
    let payload = book.resolve_payload(&r.record_id, digest).unwrap();
    v1::book_pair(&r.header_bytes, &r.capsule, &r.envelope, &payload).unwrap()
}

/// `held_ruling` finds a received ruling by the record digest a statement
/// names, and reports one the holder does not hold as unavailable (Go
/// `TestHeldRuling`).
#[test]
fn held_ruling_finds_a_received_ruling() {
    let (h, adjudicator, ruling, case_log) = case_book_holder(Party::adjudicator("adjudicator"));
    let rd = digest(&ruling);
    let find = |deal: &str, key: &str| v1::held_ruling(&h.seller.shelf, deal, key, &rd).unwrap();
    assert!(
        find(DEAL, &adjudicator.key()).is_none(),
        "not received yet: unavailable"
    );

    let (_, pair) = adjudicator.seal(&case_log, &ruling);
    h.seller.receive(Some(&pair), None);
    let read = find(DEAL, &adjudicator.key()).expect("held");
    assert_eq!(read.record.block.record_type, v1::TYPE_RULING);

    assert!(find(DEAL, &h.buyer.key()).is_none(), "from another key");
    assert!(
        find("deal-ffffffffffffffff", &adjudicator.key()).is_none(),
        "of another deal"
    );
}
