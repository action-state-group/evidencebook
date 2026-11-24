//! `deal::v1`'s holder side held to Go by
//! `testdata/deal-v1/holder/transcripts.json` (evidencebook#461): each
//! scenario Go's `TestHolderTranscripts` recorded, replayed step by step on
//! Rust shelves with the same keys, clock and random stream, each output
//! compared with Go's (the pairs and proofs a party seals, byte for byte,
//! and every answer of the shelf and the profile). Python's
//! `test_deal_v1_holder.py` replays the same file.

#![cfg(all(feature = "deal", feature = "shelf"))]

use std::collections::{BTreeMap, HashMap};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ed25519_dalek::SigningKey;
use evidencebook::canonical::jcs;
use evidencebook::deal::v1::{self, ActGateInput, CanActInput, Record};
use evidencebook::record::{EpistemicType, Link, LinkType};
use evidencebook::shelf::{
    BookEntry, Cadence, CadenceConfig, Indexes, RandomSource, ReceiveConfig, ReceiveProfile as _,
    SeededStream, Shelf, ShelfConfig, StampProof, Time,
};
use rusqlite::Connection;
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

const CADENCE_LOG: &str = "deal-cadence/0123456789abcdef";

fn seed(label: &str) -> [u8; 32] {
    Sha256::digest(format!("xdealv1 receive tests\0{label}").as_bytes()).into()
}

/// One random stream a party keeps across reopens (Go's `cfg.Random`).
#[derive(Clone)]
struct Stream(Arc<Mutex<SeededStream>>);

impl RandomSource for Stream {
    fn fill(&mut self, buf: &mut [u8]) -> Result<(), String> {
        self.0.lock().unwrap().fill(buf)
    }
}

/// The offer pairs a deal status's payment check reads, by digest.
type Offers = Arc<HashMap<String, Vec<u8>>>;

struct Party {
    name: String,
    dir: PathBuf,
    path: PathBuf,
    signer: SigningKey,
    checkpoint: SigningKey,
    random: Stream,
    clock: Arc<Mutex<Time>>,
    profile: Arc<v1::ReceiveProfile>,
    status: Option<Arc<v1::DealStatus>>,
    shelf: Shelf,
    cadence: Cadence,
}

impl Party {
    fn new(name: &str, root: &Path) -> Party {
        let dir = root.join(name.replace(' ', "_"));
        std::fs::create_dir_all(&dir).unwrap();
        let signer = SigningKey::from_bytes(&seed(&format!("{name} record key")));
        let checkpoint = SigningKey::from_bytes(&seed(&format!("{name} checkpoint key")));
        let random = Stream(Arc::new(Mutex::new(SeededStream::new(&format!(
            "xdealv1 receive tests\0{name} random"
        )))));
        let clock = Arc::new(Mutex::new(Time::parse("2026-11-01T09:00:00Z").unwrap()));
        let profile = Arc::new(v1::ReceiveProfile::new());
        let path = dir.join("shelf.db");
        let (shelf, cadence) = open(
            name,
            &path,
            &signer,
            &checkpoint,
            &random,
            &clock,
            &profile,
            None,
        );
        let p = Party {
            name: name.into(),
            dir,
            path,
            signer,
            checkpoint,
            random,
            clock,
            profile,
            status: None,
            shelf,
            cadence,
        };
        p.migrate();
        p
    }

    fn migrate(&self) {
        if !self.cadence.migrated().unwrap() {
            self.cadence.tick(self.now()).unwrap();
            self.tick();
            self.cadence.migrate(self.now()).unwrap();
        }
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
            &self.random,
            &self.clock,
            &self.profile,
            self.status.as_ref(),
        );
        self.shelf = shelf;
        self.cadence = cadence;
        self.migrate();
    }

    /// Go's `sealOnNewShelf`: the same party, another party's record key, a
    /// new shelf file.
    fn reopen_as(&mut self, signer: SigningKey) {
        self.signer = signer;
        self.path = self.dir.join("fork.db");
        self.shelf.release().unwrap();
        let (shelf, cadence) = open(
            &self.name,
            &self.path,
            &self.signer,
            &self.checkpoint,
            &self.random,
            &self.clock,
            &self.profile,
            self.status.as_ref(),
        );
        self.shelf = shelf;
        self.cadence = cadence;
        self.migrate();
    }
}

#[allow(clippy::too_many_arguments)]
fn open(
    name: &str,
    path: &Path,
    signer: &SigningKey,
    checkpoint: &SigningKey,
    random: &Stream,
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
        random: Some(Box::new(random.clone())),
        operator: name.into(),
        developer: String::new(),
        signer: Arc::new(signer.clone()),
        checkpoint_key: checkpoint.clone(),
        originals: Arc::new(originals),
        now: Some(Arc::new(move || *now.lock().unwrap())),
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
            // Go's defaults: a tick every 5 m give or take 1 m, drawn from the
            // party's random stream.
            interval: Duration::ZERO,
            jitter: Duration::ZERO,
            no_jitter: false,
            witness_ids: Vec::new(),
            witnesses: None,
            pad_bucket: 0,
            ended: Arc::new(|_| Ok(false)),
            status: status.map(|s| s.clone() as Arc<dyn evidencebook::shelf::BookStatus>),
        })
        .unwrap();
    (shelf, cadence)
}

fn entry_of(record: &Value) -> BookEntry {
    let mut e = BookEntry::new("deal_step", EpistemicType::ProducerClaim);
    e.payloads = vec![jcs(record).unwrap()];
    e.subject_ref = record["x-deal-v1"]["deal_id"].as_str().map(String::from);
    e
}

fn decode(v: &Value) -> Record {
    v1::decode(&jcs(v).unwrap()).unwrap()
}

/// Go's JSON of a `ReceiveResult` outcome: an empty list as null.
fn nulls(v: &Value) -> Value {
    match v {
        Value::Object(m) => Value::Object(m.iter().map(|(k, e)| (k.clone(), nulls(e))).collect()),
        Value::Array(a) if a.is_empty() => Value::Null,
        Value::Array(a) => Value::Array(a.iter().map(nulls).collect()),
        o => o.clone(),
    }
}

fn outcome(res: &evidencebook::shelf::ReceiveResult, error: bool) -> Value {
    let mut out = Map::new();
    out.insert("error".into(), json!(error));
    if let Some(r) = &res.record {
        out.insert(
            "record".into(),
            json!({"digest": r.digest, "deal_id": r.deal_id, "stored": r.stored,
                   "duplicate": r.duplicate, "deleted": r.deleted, "counted": r.counted,
                   "ask_for_book": r.ask_for_book, "refused": r.refused.is_some()}),
        );
    }
    if let Some(p) = &res.proof {
        out.insert(
            "proof".into(),
            json!({"accepted": p.accepted, "duplicate": p.duplicate, "refused": p.refused.is_some()}),
        );
    }
    let c = &res.changes;
    out.insert(
        "changes".into(),
        json!({"grew": c.grew, "shrank": c.shrank, "forked": c.forked, "covered": c.covered}),
    );
    nulls(&Value::Object(out))
}

/// Makes every book append of the shelf at `path` fail before it stores the
/// record, until dropped (Go's `nameRulingsBeforeAppend` returning an error;
/// the hook runs only in the naming, so it is installed around the steps
/// that run it: a receive, a naming call and a stamp).
struct BlockAppends(Connection);

impl BlockAppends {
    fn on(path: &Path) -> BlockAppends {
        let side = Connection::open(path).unwrap();
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

/// The sealed record's pair (Go's `recBook` and `seal` outputs).
fn pair_of(r: &evidencebook::shelf::BookRecord, payload: &[u8]) -> Vec<u8> {
    v1::book_pair(&r.header_bytes, &r.capsule, &r.envelope, payload).unwrap()
}

fn book_dump(book: &evidencebook::shelf::ShelfBook) -> Value {
    let mut out = Vec::new();
    for r in book.records() {
        let mut entry = Map::new();
        entry.insert("record_id".into(), json!(r.record_id));
        let commitments = r
            .header
            .get("payload_commitments")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        if let [digest] = commitments.as_slice() {
            if let Ok(raw) = book.resolve_payload(&r.record_id, digest.as_str().unwrap_or("")) {
                if v1::check(&raw).is_ok() {
                    entry.insert("pair".into(), json!(hex::encode(pair_of(&r, &raw))));
                }
            }
        }
        out.push(Value::Object(entry));
    }
    Value::Array(out)
}

fn load() -> Vec<Value> {
    let p = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../testdata/deal-v1/holder/transcripts.json");
    let v: Value = serde_json::from_slice(&std::fs::read(p).unwrap()).unwrap();
    v["transcripts"].as_array().unwrap().clone()
}

fn same(got: &Value, want: &Value, at: &str) {
    assert_eq!(
        String::from_utf8(jcs(got).unwrap()).unwrap(),
        String::from_utf8(jcs(want).unwrap()).unwrap(),
        "{at}"
    );
}

fn replay(case: &Value) {
    let name = case["name"].as_str().unwrap();
    let root = tempfile::tempdir().unwrap();
    let mut parties: BTreeMap<String, Party> = BTreeMap::new();
    let mut naming_fails = false;
    for (i, step) in case["steps"].as_array().unwrap().iter().enumerate() {
        let op = step["op"].as_str().unwrap();
        let who = step.get("party").and_then(Value::as_str).unwrap_or("");
        let at = format!("{name}: step {i} ({op} by {who})");
        match op {
            "party" => {
                let p = Party::new(who, &root.path().join(i.to_string()));
                parties.insert(who.into(), p);
                continue;
            }
            "naming_fails" => {
                naming_fails = step["in"].as_bool() == Some(true);
                continue;
            }
            _ => {}
        }
        if op == "reopen_as" {
            let other = parties[step["in"].as_str().unwrap()].signer.clone();
            parties.get_mut(who).unwrap().reopen_as(other);
            continue;
        }
        let p = parties.get_mut(who).unwrap();
        let _block = (naming_fails
            && matches!(op, "receive" | "name_rulings" | "stamp" | "stamp_tick"))
        .then(|| BlockAppends::on(&p.path));
        match op {
            "reopen" => p.reopen(),
            "send_offer" => {
                p.tick();
                let members: BTreeMap<String, Value> = step["members"]
                    .as_object()
                    .map(|m| m.iter().map(|(k, v)| (k.clone(), v.clone())).collect())
                    .unwrap_or_default();
                let deal = step["record"]["x-deal-v1"]["deal_id"].as_str().unwrap();
                let sent = p
                    .shelf
                    .seal_sent(
                        &entry_of(&step["record"]),
                        &members,
                        &format!("{deal}/sent"),
                    )
                    .unwrap_or_else(|e| panic!("{at}: {e}"));
                let c = sent.capsule;
                let pair = v1::pair(&c.capsule, &c.envelope, &c.payload).unwrap();
                assert_eq!(hex::encode(pair), step["out"].as_str().unwrap(), "{at}");
            }
            "seal" => {
                p.tick();
                let book = p.shelf.open(step["log"].as_str().unwrap()).unwrap();
                let mut e = entry_of(&step["record"]);
                e.members = step["members"]
                    .as_object()
                    .map(|m| m.iter().map(|(k, v)| (k.clone(), v.clone())).collect())
                    .unwrap_or_default();
                if let Some(last) = book.records().last() {
                    e.links = vec![Link {
                        link_type: LinkType::Cites,
                        target: last.record_id.clone(),
                    }];
                }
                let rec = book.append(&e).unwrap();
                let pair = pair_of(&rec, &jcs(&step["record"]).unwrap());
                assert_eq!(
                    rec.record_id,
                    step["out"]["record_id"].as_str().unwrap(),
                    "{at}"
                );
                assert_eq!(
                    hex::encode(pair),
                    step["out"]["pair"].as_str().unwrap(),
                    "{at}"
                );
            }
            "stamp" => {
                p.tick();
                p.cadence.stamp(p.now()).unwrap();
                let proof = p
                    .cadence
                    .stamp_proof(step["first"].as_str().unwrap(), 0, "counterparty")
                    .unwrap();
                same(&serde_json::to_value(&proof).unwrap(), &step["out"], &at);
            }
            "retention" => {
                // Go's SetRetention: a lifecycle record citing the record.
                let journal = match step.get("log").and_then(Value::as_str) {
                    Some(log) => p.shelf.open(log).unwrap(),
                    None => p.shelf.journal().unwrap(),
                };
                let seq = step["in"]["seq"].as_u64().unwrap();
                let target = journal.record_by_seq(seq).unwrap();
                let mut e = BookEntry::new("lifecycle", EpistemicType::ObservedEvent);
                e.links = vec![Link {
                    link_type: LinkType::Cites,
                    target: target.record_id,
                }];
                e.statement =
                    json!({"retention_state": step["in"]["state"], "reason": step["in"]["reason"]});
                let rec = journal.append(&e).unwrap();
                assert_eq!(rec.record_id, step["out"].as_str().unwrap(), "{at}");
            }
            "use_status" => {
                let offers: Offers = Arc::new(
                    step["in"]
                        .as_object()
                        .map(|m| {
                            m.iter()
                                .map(|(d, h)| {
                                    (d.clone(), hex::decode(h.as_str().unwrap()).unwrap())
                                })
                                .collect()
                        })
                        .unwrap_or_default(),
                );
                p.status = Some(Arc::new(v1::DealStatus::new(Some(Arc::new(
                    move |d: &str| Ok(offers.get(d).cloned()),
                )))));
            }
            "stamp_tick" => {
                p.tick();
                let got = match p.cadence.stamp(p.now()) {
                    Ok(res) => json!({
                        "n": res.tick.map_or(0, |t| t.n),
                        "before_stamp": res.before_stamp.unwrap_or_default(),
                        "unstated": res.unstated,
                        "error": null,
                    }),
                    Err(_) => json!({"n": 0, "before_stamp": "", "unstated": {}, "error": true}),
                };
                same(&got, &step["out"], &at);
            }
            "openings" => {
                let [book_id, audience] = [0, 1].map(|i| step["in"][i].as_str().unwrap());
                let got: Vec<Value> = p
                    .shelf
                    .openings(book_id, audience)
                    .unwrap()
                    .into_iter()
                    .map(|f| {
                        json!({"entry_seq": f.entry_seq, "fact": f.fact,
                               "link": f.link_opening.map(|o| o.to_json()),
                               "status": f.status_opening.map(|o| o.to_json())})
                    })
                    .collect();
                same(&Value::Array(got), &step["out"], &at);
            }
            "report" => {
                let i_ = &step["in"];
                let copy = v1::ReportCopy {
                    audience: i_["audience"].as_str().unwrap().into(),
                    nonce: i_["nonce"].as_str().unwrap().into(),
                    referee: i_["referee"].as_str().unwrap_or("").into(),
                    as_of: i_["as_of"].as_str().unwrap().into(),
                    text: i_.get("text").and_then(Value::as_object).cloned(),
                };
                let status = p.status.clone().expect("a deal status");
                let got = match status.report(step["log"].as_str().unwrap(), &copy) {
                    Ok(r) => json!({"report": serde_json::to_value(r).unwrap(), "error": null}),
                    Err(_) => json!({"report": null, "error": true}),
                };
                same(&got, &step["out"], &at);
            }
            "book" => same(
                &book_dump(&p.shelf.open(step["log"].as_str().unwrap()).unwrap()),
                &step["out"],
                &at,
            ),
            "name_rulings" => {
                let res = if step["in"].is_null() {
                    v1::name_all_rulings(&p.shelf, p.now())
                } else {
                    let ids: Vec<String> = serde_json::from_value(step["in"].clone()).unwrap();
                    v1::name_rulings(&p.shelf, &ids, p.now())
                };
                let (ids, err) = match res {
                    Ok(ids) => (ids, Value::Null),
                    Err(e) => (e.sealed, json!(true)),
                };
                let got = json!({"sealed": if ids.is_empty() { Value::Null } else { json!(ids) }, "error": err});
                same(&got, &step["out"], &at);
            }
            "receive" => {
                let record = step
                    .get("pair")
                    .and_then(Value::as_str)
                    .map(|h| hex::decode(h).unwrap());
                let proof: Option<StampProof> = step
                    .get("proof")
                    .filter(|v| !v.is_null())
                    .map(|v| serde_json::from_value(v.clone()).unwrap());
                let got = match p.shelf.receive(record.as_deref(), proof.as_ref()) {
                    Ok(res) => outcome(&res, false),
                    Err(e) => outcome(&e.result, true),
                };
                same(&got, &nulls(&step["out"]), &at);
            }
            "can_act" => {
                let i_ = &step["in"];
                let ok = p
                    .profile
                    .can_act(&CanActInput {
                        deal_id: i_["DealID"].as_str().unwrap().into(),
                        counterpart_agreement: i_["CounterpartAgreement"].as_str().unwrap().into(),
                        commitment_shelf_log: i_["CommitmentShelfLog"].as_str().unwrap().into(),
                    })
                    .unwrap();
                same(&json!({"ok": ok, "error": null}), &step["out"], &at);
            }
            "proof_sender" => {
                let s = p
                    .profile
                    .proof_sender(
                        step["in"][0].as_str().unwrap(),
                        step["in"][1].as_str().unwrap(),
                    )
                    .unwrap();
                same(
                    &json!({"sender": s.clone().unwrap_or_default(), "ok": s.is_some(), "error": null}),
                    &step["out"],
                    &at,
                );
            }
            "answered" => {
                let held = p
                    .shelf
                    .received_record_of(step["in"].as_str().unwrap())
                    .unwrap();
                let ok = p.profile.answered(&held.received).unwrap();
                same(&json!({"ok": ok, "error": null}), &step["out"], &at);
            }
            "gate" => {
                let i_ = &step["in"];
                let input = ActGateInput {
                    own: i_["own"].as_array().unwrap().iter().map(decode).collect(),
                    act: decode(&i_["act"]),
                    counterpart_agreement: i_["counterpart_agreement"].as_str().unwrap().into(),
                    commitment_shelf_log: i_["commitment_shelf_log"].as_str().unwrap().into(),
                };
                let got = match p.profile.gate_act(&input) {
                    Ok(g) => {
                        let mut m = Map::new();
                        m.insert("gated".into(), json!(g.gated));
                        m.insert("allowed".into(), json!(g.allowed));
                        if g.unguarded {
                            m.insert("unguarded".into(), json!(true));
                        }
                        if !g.reason.is_empty() {
                            m.insert("reason".into(), json!(g.reason));
                        }
                        json!({"gate": m, "error": null})
                    }
                    Err(_) => json!({"gate": {"gated": false, "allowed": false}, "error": true}),
                };
                same(&got, &step["out"], &at);
            }
            other => panic!("unknown op {other}"),
        }
    }
}

#[test]
fn holder_transcripts_replay_as_go_recorded_them() {
    let cases = load();
    assert_eq!(cases.len(), 33);
    for case in &cases {
        replay(case);
    }
}
