//! The scripted shelf of the cross-language shelf conformance
//! (evidencebook#254 §5), Rust's side: Go's `internal/shelfscript`, run from
//! `testdata/shelf/script.json`. The writer and continuer phases, the lock
//! exchange, and a dump of everything the contract fixes in the file, which
//! must equal Go's `after-write.json` and `after-continue.json` byte for
//! byte.

use crate::go_marshal_indent;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ciborium::Value as Cbor;
use ed25519_dalek::{Signer as _, SigningKey};
use evidencebook::record::EpistemicType;
use evidencebook::shelf::{
    BookEntry, Cadence, CadenceConfig, SeededStream, Shelf, ShelfConfig, ShelfError, Time,
    WitnessReceipt, WITNESS_VERIFIED,
};
use rusqlite::Connection;
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

/// The script: `testdata/shelf/script.json`, as Go wrote it.
pub const SCRIPT: &str = include_str!("../../../testdata/shelf/script.json");

/// Why a script step failed.
pub type Error = Box<dyn std::error::Error + Send + Sync>;

fn script() -> Value {
    serde_json::from_str(SCRIPT).expect("script.json is JSON")
}

fn text<'a>(v: &'a Value, pointer: &str) -> &'a str {
    v.pointer(pointer)
        .and_then(Value::as_str)
        .unwrap_or_else(|| panic!("script.json{pointer}"))
}

fn number(v: &Value, pointer: &str) -> u64 {
    v.pointer(pointer)
        .and_then(Value::as_u64)
        .unwrap_or_else(|| panic!("script.json{pointer}"))
}

fn key(v: &Value, name: &str) -> SigningKey {
    let seed: [u8; 32] = hex::decode(text(v, &format!("/keys/{name}_seed")))
        .unwrap()
        .try_into()
        .unwrap();
    let key = SigningKey::from_bytes(&seed);
    assert_eq!(
        hex::encode(key.verifying_key().to_bytes()),
        text(v, &format!("/keys/{name}_public"))
    );
    key
}

/// The script's start time.
pub fn start() -> Time {
    Time::parse(text(&script(), "/start")).expect("start")
}

/// The shelf's file in `dir`.
pub fn file(dir: &Path) -> PathBuf {
    dir.join(text(&script(), "/file"))
}

/// The script's clock: the time a step sets; reads never advance it.
#[derive(Clone)]
struct Clock(Arc<Mutex<Time>>);

impl Clock {
    fn now(&self) -> Time {
        *self.0.lock().unwrap()
    }
    fn set(&self, t: Time) {
        *self.0.lock().unwrap() = t;
    }
}

/// One open shelf of the script.
pub struct Env {
    clock: Clock,
    pub shelf: Shelf,
    pub cadence: Cadence,
}

fn open_db(path: &Path) -> rusqlite::Result<Connection> {
    let conn = Connection::open(path)?;
    conn.busy_timeout(Duration::from_millis(5000))?;
    conn.pragma_update(None, "journal_mode", "WAL")?;
    conn.pragma_update(None, "synchronous", "FULL")?;
    Ok(conn)
}

/// The witness directory: the script's one witness.
fn directory(v: &Value) -> Value {
    json!({"witnesses": [{
        "name": "test witness",
        "endpoint": text(v, "/host_parameters/cadence/witness_url"),
        "key_ids": [text(v, "/keys/witness_public")],
    }]})
}

/// Open the shelf at `path` with the random stream `stream`, its clock at
/// `at`, waiting at most `lock_timeout` for its writer lock.
pub fn open(
    path: &Path,
    stream: &str,
    at: Time,
    lock_timeout: Option<Duration>,
) -> Result<Env, Error> {
    let v = script();
    let hp = |p: &str| format!("/host_parameters{p}");
    let signer = key(&v, "signer");
    let originals = ArtifactSqlite::new(
        open_db(path)?,
        text(&v, &hp("/originals/namespace")),
        [signer.verifying_key().to_bytes()],
    )?;
    originals.init()?;
    let clock = Clock(Arc::new(Mutex::new(at)));
    let now = clock.clone();
    let shelf = Shelf::open_sqlite(ShelfConfig {
        path: path.to_path_buf(),
        lock_path: None,
        held_lock: None,
        lock_timeout,
        random: Some(Box::new(SeededStream::new(stream))),
        operator: text(&v, &hp("/operator")).to_string(),
        developer: text(&v, &hp("/developer")).to_string(),
        signer: Arc::new(signer),
        checkpoint_key: key(&v, "checkpoint"),
        originals: Arc::new(originals),
        now: Some(Arc::new(move || now.now())),
        authenticated_indexes: None,
    })?;
    let ended: Vec<String> = v
        .pointer(&hp("/cadence/ended"))
        .and_then(Value::as_array)
        .unwrap()
        .iter()
        .map(|e| e.as_str().unwrap().to_string())
        .collect();
    let cadence = shelf.cadence(CadenceConfig {
        log_id: text(&v, &hp("/cadence/log_id")).to_string(),
        interval: Duration::from_secs(number(&v, &hp("/cadence/interval_s"))),
        jitter: Duration::from_secs(number(&v, &hp("/cadence/jitter_s"))),
        no_jitter: false,
        witness_ids: v
            .pointer(&hp("/cadence/witness_ids"))
            .and_then(Value::as_array)
            .unwrap()
            .iter()
            .map(|w| w.as_str().unwrap().to_string())
            .collect(),
        witnesses: Some(directory(&v)),
        pad_bucket: number(&v, &hp("/cadence/pad_bucket")),
        ended: Arc::new(move |id| Ok(ended.iter().any(|e| e == id))),
    });
    let cadence = match cadence {
        Ok(c) => c,
        Err(e) => {
            let _ = shelf.release();
            return Err(e.into());
        }
    };
    Ok(Env {
        clock,
        shelf,
        cadence,
    })
}

impl Env {
    /// Release the shelf.
    pub fn release(self) -> Result<(), ShelfError> {
        self.shelf.release()
    }

    fn append(&self, book: &str, entry: &BookEntry) -> Result<(), Error> {
        self.shelf.open(book)?.append(entry)?;
        Ok(())
    }

    fn append_host(&self, n: u8) -> Result<(), Error> {
        let v = script();
        let host = self
            .shelf
            .host_log(text(&v, "/host_parameters/host_log/log_id"), true)?;
        let at = self.clock.now();
        host.write(|stores| -> Result<(), Error> {
            stores.log.append(&cll::log::AppendInput {
                value: host_value(n).to_vec(),
                appended_at: cll::log::Timestamp::from_unix_millis(
                    (at.unix_nanos() / 1_000_000) as i64,
                ),
            })?;
            Ok(())
        })
    }

    /// Cut the next tick at its due time (the first at start + 30 s).
    fn tick(&self) -> Result<evidencebook::shelf::CadenceTick, Error> {
        let now = match self.cadence.last()? {
            Some(last) => last.due_next,
            None => start().add(Duration::from_secs(30)),
        };
        self.clock.set(now);
        let result = self.cadence.tick(now)?;
        result
            .tick
            .filter(|_| result.ticked)
            .ok_or_else(|| format!("no tick due at {now} (due {})", result.due_next).into())
    }

    fn witness(&self, tick: &evidencebook::shelf::CadenceTick) -> Result<(), Error> {
        let receipt = mint_receipt(&tick.checkpoint)?;
        let check = self.cadence.witnessed(tick.n, receipt)?;
        if check.state != WITNESS_VERIFIED {
            return Err(format!(
                "tick {}'s receipt is {}: {}",
                tick.n, check.state, check.reason
            )
            .into());
        }
        Ok(())
    }
}

/// The n-th (from 1) entry the script appends to its host log:
/// SHA-256("legacy/old" ‖ uint8(n)).
pub fn host_value(n: u8) -> [u8; 32] {
    let mut h = Sha256::new();
    h.update(b"legacy/old");
    h.update([n]);
    h.finalize().into()
}

/// The script's book record: an observed event about `subject` with one
/// payload, or none.
fn observation(subject: &str, payload: Option<&str>) -> BookEntry {
    let mut entry = BookEntry::new("observation", EpistemicType::ObservedEvent);
    entry.subject_ref = Some(subject.to_string());
    entry.payloads = payload.map(|p| p.as_bytes().to_vec()).into_iter().collect();
    entry
}

/// The script's witness receipt for a cadence checkpoint (Go
/// `MintReceipt`): an RFC 9162 COSE receipt from the witness key over a
/// one-entry log holding the checkpoint, so its inclusion path is empty.
pub fn mint_receipt(statement: &[u8]) -> Result<WitnessReceipt, Error> {
    let v = script();
    let verified = cll::checkpoint::verify_checkpoint_cose_offline(statement);
    let decoded = verified
        .decoded
        .filter(|_| verified.ok)
        .ok_or("checkpoint statement")?;
    let digest = agent_action_capsule::witness::checkpoint_digest(
        &agent_action_capsule::witness::SignedCheckpoint::from(&decoded),
    );
    let entry: [u8; 32] = Sha256::digest(hex::decode(digest)?).into();
    let encode = |value: &Cbor| {
        let mut out = Vec::new();
        ciborium::into_writer(value, &mut out).expect("CBOR encodes");
        out
    };
    let proof = encode(&Cbor::Array(vec![
        Cbor::Integer(1.into()),
        Cbor::Integer(0.into()),
        Cbor::Array(Vec::new()),
    ]));
    let mut leaf_input = vec![0u8];
    leaf_input.extend_from_slice(&entry);
    let leaf = Sha256::digest(&leaf_input).to_vec();
    let protected = encode(&Cbor::Map(vec![
        (Cbor::Integer(1.into()), Cbor::Integer((-8).into())),
        (Cbor::Integer(395.into()), Cbor::Integer(1.into())),
    ]));
    let to_be_signed = encode(&Cbor::Array(vec![
        Cbor::Text("Signature1".into()),
        Cbor::Bytes(protected.clone()),
        Cbor::Bytes(Vec::new()),
        Cbor::Bytes(leaf),
    ]));
    let signature = key(&v, "witness").sign(&to_be_signed).to_bytes().to_vec();
    let receipt = encode(&Cbor::Tag(
        18,
        Box::new(Cbor::Array(vec![
            Cbor::Bytes(protected),
            Cbor::Map(vec![(
                Cbor::Integer(396.into()),
                Cbor::Map(vec![(
                    Cbor::Integer((-1).into()),
                    Cbor::Array(vec![Cbor::Bytes(proof)]),
                )]),
            )]),
            Cbor::Null,
            Cbor::Bytes(signature),
        ])),
    ));
    Ok(WitnessReceipt {
        ts_url: text(&v, "/host_parameters/cadence/witness_url").to_string(),
        entry_hash: hex::encode(entry),
        receipt,
        leaf_index: 0,
        tree_size: 1,
    })
}

/// Run the writer phase on a new `dir/shelf.db`.
pub fn write(dir: &Path) -> Result<(), Error> {
    let path = file(dir);
    if path.exists() {
        return Err(format!("{} exists", path.display()).into());
    }
    run(&path, script()["write_phase"].as_array().unwrap())
}

/// Run the continuer phase on `dir/shelf.db`.
pub fn continue_(dir: &Path) -> Result<(), Error> {
    run(&file(dir), script()["continue_phase"].as_array().unwrap())
}

/// Run `steps` on the shelf file at `path`, as Go's `shelfscript.Run`.
pub fn run(path: &Path, steps: &[Value]) -> Result<(), Error> {
    let mut env: Option<Env> = None;
    let mut released_at = start();
    for (i, step) in steps.iter().enumerate() {
        let op = step["op"].as_str().unwrap_or_default();
        let str_of = |k: &str| step.get(k).and_then(Value::as_str).unwrap_or_default();
        let at = step.get("at").and_then(Value::as_i64).unwrap_or(0);
        let result = (|| -> Result<(), Error> {
            if env.is_none() != (op == "open" || op == "plant_pending") {
                return Err(format!(
                    "{op} with the shelf {}",
                    if env.is_none() { "released" } else { "open" }
                )
                .into());
            }
            match op {
                "open" => {
                    env = Some(open(
                        path,
                        str_of("stream"),
                        start().add(Duration::from_secs(at as u64)),
                        None,
                    )?);
                }
                "release" => {
                    let e = env.take().unwrap();
                    released_at = e.clock.now();
                    e.release()?;
                }
                "host_append" => env
                    .as_ref()
                    .unwrap()
                    .append_host(step["n"].as_u64().unwrap() as u8)?,
                "append" => env.as_ref().unwrap().append(
                    str_of("book"),
                    &observation(str_of("subject"), Some(str_of("payload"))),
                )?,
                "tick" => {
                    let e = env.as_ref().unwrap();
                    let tick = e.tick()?;
                    if step
                        .get("witness")
                        .and_then(Value::as_bool)
                        .unwrap_or(false)
                    {
                        e.witness(&tick)?;
                    }
                }
                "plant_pending" => plant_pending(
                    path,
                    str_of("book"),
                    &observation(str_of("subject"), None),
                    released_at.add(Duration::from_secs(at as u64)),
                )?,
                other => return Err(format!("unknown step {other:?}").into()),
            }
            Ok(())
        })();
        if let Err(e) = result {
            if let Some(env) = env.take() {
                let _ = env.release();
            }
            return Err(format!("step {} ({op}): {e}", i + 1).into());
        }
    }
    match env {
        Some(env) => {
            let _ = env.release();
            Err("the script left the shelf open".into())
        }
        None => Ok(()),
    }
}

/// Open the scripted shelf in `dir`, holding its writer lock until the
/// returned shelf is released; wait at most `timeout` for another holder.
pub fn hold(dir: &Path, timeout: Option<Duration>) -> Result<Env, Error> {
    open(
        &file(dir),
        "evidencebook shelf vectors/hold",
        start(),
        timeout,
    )
}

/// Leave `book`'s next record stored and not in its log, as a crash between
/// the two writes does: the record the next append at `at` would seal, made
/// on a copy of the file; only its row (and the standing the same
/// transaction writes) is copied back (Go `plantPending`).
fn plant_pending(path: &Path, book: &str, entry: &BookEntry, at: Time) -> Result<(), Error> {
    let scratch = tempfile::Builder::new()
        .prefix("pending-")
        .tempdir_in(path.parent().unwrap())?;
    let copy = scratch.path().join(path.file_name().unwrap());
    let db = open_db(path)?;
    db.execute("VACUUM INTO ?", [copy.to_str().unwrap()])?;
    let e = open(&copy, "evidencebook shelf vectors/pending", at, None)?;
    let appended = e.append(book, entry);
    e.release()?;
    appended?;
    let copied = open_db(&copy)?;
    let (seq, record_id, header, capsule, envelope): (i64, String, Vec<u8>, Vec<u8>, Vec<u8>) = copied.query_row(
        "SELECT seq, record_id, header, capsule, envelope FROM book_records WHERE book_id = ? ORDER BY seq DESC LIMIT 1",
        [book],
        |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
    )?;
    let mut db = db;
    let tx = db.transaction()?;
    tx.execute(
        "INSERT INTO book_records (book_id, seq, record_id, header, capsule, envelope) VALUES (?, ?, ?, ?, ?, ?)",
        rusqlite::params![book, seq, record_id, header, capsule, envelope],
    )?;
    tx.execute(
        "INSERT OR IGNORE INTO shelf_logs (log_id, kind) VALUES (?, 'book')",
        [book],
    )?;
    tx.execute(
        "INSERT OR IGNORE INTO cadence_logs (log_id) VALUES (?)",
        [book],
    )?;
    tx.execute(
        "INSERT OR IGNORE INTO cadence_live (log_id) VALUES (?)",
        [book],
    )?;
    tx.commit()?;
    Ok(())
}

fn sha256_hex(b: &[u8]) -> String {
    hex::encode(Sha256::digest(b))
}

fn object(members: Vec<(&str, Value)>) -> Value {
    let mut sorted: Vec<(&str, Value)> = members;
    sorted.sort_by(|a, b| a.0.cmp(b.0));
    Value::Object(
        sorted
            .into_iter()
            .map(|(k, v)| (k.to_string(), v))
            .collect::<Map<_, _>>(),
    )
}

fn rows(
    db: &Connection,
    sql: &str,
    row: impl Fn(&rusqlite::Row<'_>) -> rusqlite::Result<Value>,
) -> Result<Value, Error> {
    let mut stmt = db.prepare(sql)?;
    let out = stmt
        .query_map([], |r| row(r))?
        .collect::<Result<Vec<_>, _>>()?;
    Ok(Value::Array(out))
}

/// The dump of the shelf in `dir` (Go `shelfscript.Dump`): its tables row
/// for row, the CLL entries and node digest of every log, and what the
/// shelf derives, in the encoding `testdata/shelf/README.md` states. It
/// reads a copy of the file.
pub fn dump(dir: &Path) -> Result<String, Error> {
    let scratch = tempfile::Builder::new().prefix("shelf-dump-").tempdir()?;
    let path = scratch.path().join(file(dir).file_name().unwrap());
    {
        let original = open_db(&file(dir))?;
        original.execute("VACUUM INTO ?", [path.to_str().unwrap()])?;
    }
    let db = open_db(&path)?;
    let mut state: Vec<(&str, Value)> = Vec::new();
    let version: i64 = db.query_row("SELECT version FROM shelf_meta WHERE id = 1", [], |r| {
        r.get(0)
    })?;
    state.push(("shelf_meta", json!({"version": version})));
    state.push((
        "shelf_logs",
        rows(
            &db,
            "SELECT log_id, kind FROM shelf_logs ORDER BY log_id",
            |r| {
                Ok(object(vec![
                    ("log_id", json!(r.get::<_, String>(0)?)),
                    ("kind", json!(r.get::<_, String>(1)?)),
                ]))
            },
        )?,
    ));
    state.push(("book_records", rows(&db, "SELECT book_id, seq, record_id, header, capsule, envelope FROM book_records ORDER BY book_id, seq", |r| {
        Ok(object(vec![
            ("book_id", json!(r.get::<_, String>(0)?)),
            ("seq", json!(r.get::<_, i64>(1)?)),
            ("record_id", json!(r.get::<_, String>(2)?)),
            ("header", json!(String::from_utf8_lossy(&r.get::<_, Vec<u8>>(3)?))),
            ("capsule_sha256", json!(sha256_hex(&r.get::<_, Vec<u8>>(4)?))),
            ("envelope_sha256", json!(sha256_hex(&r.get::<_, Vec<u8>>(5)?))),
        ]))
    })?));
    state.push(("book_checkpoints", rows(&db, "SELECT book_id, n, checkpoint_id, checkpoint FROM book_checkpoints ORDER BY book_id, n", |r| {
        Ok(object(vec![
            ("book_id", json!(r.get::<_, String>(0)?)),
            ("n", json!(r.get::<_, i64>(1)?)),
            ("checkpoint_id", json!(r.get::<_, String>(2)?)),
            ("checkpoint", json!(String::from_utf8_lossy(&r.get::<_, Vec<u8>>(3)?))),
        ]))
    })?));
    state.push((
        "cadence_logs",
        rows(
            &db,
            "SELECT log_id FROM cadence_logs ORDER BY log_id",
            |r| Ok(json!(r.get::<_, String>(0)?)),
        )?,
    ));
    state.push((
        "cadence_live",
        rows(
            &db,
            "SELECT log_id FROM cadence_live ORDER BY log_id",
            |r| Ok(json!(r.get::<_, String>(0)?)),
        )?,
    ));
    state.push(("cadence_witnessed", rows(&db, "SELECT log_id, checkpoint_sha256, mmr_size, tick FROM cadence_witnessed ORDER BY log_id, checkpoint_sha256", |r| {
        Ok(object(vec![
            ("log_id", json!(r.get::<_, String>(0)?)),
            ("checkpoint_sha256", json!(r.get::<_, String>(1)?)),
            ("mmr_size", json!(r.get::<_, i64>(2)?)),
            ("tick", json!(r.get::<_, i64>(3)?)),
        ]))
    })?));
    state.push(("cadence_ticks", rows(&db, "SELECT tick, at, due_next, entry_seq, checkpoint_size, checkpoint, leaves, witnessed, receipt FROM cadence_ticks ORDER BY tick", |r| {
        Ok(object(vec![
            ("tick", json!(r.get::<_, i64>(0)?)),
            ("at", json!(r.get::<_, String>(1)?)),
            ("due_next", json!(r.get::<_, String>(2)?)),
            ("entry_seq", json!(r.get::<_, i64>(3)?)),
            ("checkpoint_size", json!(r.get::<_, i64>(4)?)),
            ("checkpoint", json!(URL_SAFE_NO_PAD.encode(r.get::<_, Vec<u8>>(5)?))),
            ("leaves", json!(r.get::<_, String>(6)?)),
            ("witnessed", json!(r.get::<_, i64>(7)?)),
            ("receipt", r.get::<_, Option<String>>(8)?.map_or(Value::Null, Value::String)),
        ]))
    })?));
    state.push((
        "cll_entries",
        rows(
            &db,
            "SELECT log_id, seq, value, appended_at FROM cll_entries ORDER BY log_id, seq",
            |r| {
                Ok(object(vec![
                    ("log_id", json!(r.get::<_, String>(0)?)),
                    ("seq", json!(r.get::<_, i64>(1)?)),
                    ("value", json!(hex::encode(r.get::<_, Vec<u8>>(2)?))),
                    ("appended_at", json!(r.get::<_, String>(3)?)),
                ]))
            },
        )?,
    ));
    let mut nodes: BTreeMap<String, Vec<u8>> = BTreeMap::new();
    {
        let mut stmt =
            db.prepare("SELECT log_id, node FROM cll_nodes ORDER BY log_id, position")?;
        for row in stmt.query_map([], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, Vec<u8>>(1)?))
        })? {
            let (id, node) = row?;
            nodes.entry(id).or_default().extend(node);
        }
    }
    state.push((
        "cll_nodes_sha256",
        Value::Object(
            nodes
                .iter()
                .map(|(k, v)| (k.clone(), json!(sha256_hex(v))))
                .collect(),
        ),
    ));
    drop(db);
    let e = open(
        &path,
        "evidencebook shelf vectors/dump",
        start().add(Duration::from_secs(3 * 3600)),
        None,
    )?;
    let derived = derive(&e);
    e.release()?;
    let (records, roots, anchors, chains) = derived?;
    state.push(("records", records));
    state.push(("tick_roots", roots));
    state.push(("standard_history_anchors", anchors));
    state.push(("chains", chains));
    let mut out = go_marshal_indent(&object(state), "  ");
    out.push('\n');
    Ok(out)
}

/// What the shelf derives: `Records` (first, so it lists a pending record
/// before a chain's book open recovers it), each tick's root and anchor,
/// and the chain of every checkpoint a tick carries.
fn derive(e: &Env) -> Result<(Value, Value, Value, Value), Error> {
    let listing = e.shelf.records(&[])?;
    let records: Vec<Value> = listing
        .records
        .iter()
        .map(|r| {
            object(vec![
                ("book_id", json!(r.book_id)),
                ("seq", json!(r.seq)),
                ("record_id", json!(r.record_id)),
                (
                    "record_type",
                    r.header.get("record_type").cloned().unwrap_or(Value::Null),
                ),
                ("retention", json!(r.retention)),
            ])
        })
        .collect();
    let pending: Map<String, Value> = listing
        .pending
        .iter()
        .map(|(k, v)| (k.clone(), json!(v)))
        .collect();
    let last = e
        .cadence
        .last()?
        .ok_or("the scripted cadence has no tick")?;
    let (mut roots, mut anchors, mut chains) = (Vec::new(), Vec::new(), Map::new());
    for n in 1..=last.n {
        let tick = e.cadence.get(n)?;
        let (root, _) = tick.leaves.tree(tick.leaves.filler_index)?;
        roots.push(json!(hex::encode(root)));
        let anchor = e.cadence.standard_history_anchor(n)?;
        anchors.push(object(vec![
            ("tick", json!(anchor.tick.n)),
            ("anchor", json!(anchor.anchor)),
            ("anchor_time", json!(anchor.anchor_time)),
            ("first_checkpoint_time", json!(anchor.first_checkpoint_time)),
            (
                "extents",
                Value::Object(
                    anchor
                        .extents
                        .iter()
                        .map(|(k, v)| (k.clone(), json!(v)))
                        .collect(),
                ),
            ),
        ]));
        for (id, leaf) in &tick.leaves.deals {
            let statement = URL_SAFE_NO_PAD.decode(&leaf.statement)?;
            let key = format!("{n} {id}");
            let chain = match e.cadence.chain(&statement) {
                Err(ShelfError::NotFound(_)) => json!({"error": "not_found"}),
                Err(err) => return Err(format!("chain of {key}: {err}").into()),
                Ok(chain) => chain,
            };
            chains.insert(key, chain);
        }
    }
    let mut chains: Vec<(String, Value)> = chains.into_iter().collect();
    chains.sort_by(|a, b| a.0.cmp(&b.0));
    Ok((
        object(vec![
            ("records", Value::Array(records)),
            ("pending", Value::Object(pending)),
        ]),
        Value::Array(roots),
        Value::Array(anchors),
        Value::Object(chains.into_iter().collect()),
    ))
}
