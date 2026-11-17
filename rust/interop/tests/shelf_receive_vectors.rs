// SPDX-License-Identifier: Apache-2.0
//! The received store's vectors (`testdata/shelf/receive`, evidencebook#461
//! §3 and §7; Go `shelf_receive_vectors_test.go`): `script.json` run on a
//! fresh Rust shelf with the vector profile gives every step's `expect`, and
//! the store's dump after it is `after.json`, byte for byte.

use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ed25519_dalek::SigningKey;
use evidencebook::shelf::{
    ReceiveBounds, ReceiveConfig, ReceiveProfile, ReceivedRecord, Shelf, ShelfConfig, ShelfError,
    StampProof, Time,
};
use evidencebook_interop::go_marshal_indent;
use rusqlite::types::Value as Sql;
use rusqlite::Connection;
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

fn vector(name: &str) -> String {
    let path = format!(
        "{}/../../testdata/shelf/receive/{name}",
        env!("CARGO_MANIFEST_DIR")
    );
    std::fs::read_to_string(path).unwrap()
}

/// The vector profile (`receive/README.md`): a test profile, not a format.
struct VectorProfile {
    secrets: HashMap<String, String>,
    admitted: Vec<String>,
    answered: Vec<String>,
    proof_keys: HashMap<String, String>,
}

fn jcs(v: &Value) -> Vec<u8> {
    evidencebook::canonical::jcs(v).unwrap()
}

/// The vector record's members (Go `vectorRecord`), each with its JSON type.
const MEMBERS: [(&str, &str); 9] = [
    ("v", "int"),
    ("sender", "str"),
    ("log", "str"),
    ("position", "uint"),
    ("prev", "str"),
    ("digest", "str"),
    ("deal", "str"),
    ("offer", "bool"),
    ("sig", "str"),
];

impl ReceiveProfile for VectorProfile {
    fn read(&self, record: &[u8]) -> Result<ReceivedRecord, String> {
        // Go decodes into its struct, refusing an unknown member, and
        // requires the bytes to be that struct's canonical JSON (sig
        // omitted when empty): every member present, of its type.
        let mut r: Map<String, Value> =
            serde_json::from_slice(record).map_err(|e| e.to_string())?;
        for k in r.keys() {
            if !MEMBERS.iter().any(|(m, _)| m == k) {
                return Err(format!("an unknown member {k:?}"));
            }
        }
        for (m, kind) in MEMBERS {
            let ok = match (r.get(m), kind) {
                (None, _) => m == "sig",
                (Some(v), "int") => v.is_i64(),
                (Some(v), "uint") => v.is_u64(),
                (Some(v), "bool") => v.is_boolean(),
                (Some(v), _) => v.as_str().is_some_and(|s| m != "sig" || !s.is_empty()),
            };
            if !ok {
                return Err("not a canonical vector record of version 1".into());
            }
        }
        if jcs(&Value::Object(r.clone())) != record || r["v"] != json!(1) {
            return Err("not a canonical vector record of version 1".into());
        }
        let sig = r
            .remove("sig")
            .and_then(|s| s.as_str().map(String::from))
            .unwrap_or_default();
        let text = |m: &str| r[m].as_str().unwrap().to_string();
        let secret = self
            .secrets
            .get(&text("sender"))
            .ok_or("an unknown sender")?;
        let mut h = Sha256::new();
        h.update(secret.as_bytes());
        h.update(jcs(&Value::Object(r.clone())));
        if hex::encode(h.finalize()) != sig {
            return Err("a bad signature".into());
        }
        Ok(ReceivedRecord {
            digest: text("digest"),
            sender_key: text("sender"),
            log_id: text("log"),
            position: r["position"].as_u64().unwrap(),
            prev: text("prev"),
            deal_id: text("deal"),
            offer: r["offer"].as_bool().unwrap(),
        })
    }

    fn admit(&self, r: &ReceivedRecord) -> Result<bool, String> {
        Ok(r.offer || self.admitted.contains(&r.sender_key))
    }

    fn answered(&self, r: &ReceivedRecord) -> Result<bool, String> {
        Ok(self.answered.contains(&r.digest))
    }

    fn proof_sender(&self, key_id: &str, _log_id: &str) -> Result<Option<String>, String> {
        Ok(self.proof_keys.get(key_id).cloned())
    }
}

fn strings(v: &Value) -> Vec<String> {
    v.as_array()
        .map(|a| a.iter().map(|s| s.as_str().unwrap().to_string()).collect())
        .unwrap_or_default()
}

fn map(v: &Value) -> HashMap<String, String> {
    v.as_object()
        .unwrap()
        .iter()
        .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
        .collect()
}

fn open(
    path: &Path,
    profile: Arc<VectorProfile>,
    receive: &Value,
    clock: Arc<Mutex<Time>>,
) -> Shelf {
    let signer = SigningKey::from_bytes(&Sha256::digest(b"signer").into());
    let originals = ArtifactSqlite::new(
        Connection::open(path).unwrap(),
        "test",
        [signer.verifying_key().to_bytes()],
    )
    .unwrap();
    originals.init().unwrap();
    let b = &receive["bounds"];
    let n = |k: &str| b[k].as_u64().unwrap();
    Shelf::open_sqlite(ShelfConfig {
        path: path.to_path_buf(),
        lock_path: None,
        held_lock: None,
        lock_timeout: Some(Duration::from_secs(5)),
        random: None,
        operator: "test-operator".into(),
        developer: String::new(),
        signer: Arc::new(signer),
        checkpoint_key: SigningKey::from_bytes(&Sha256::digest(b"checkpoint").into()),
        originals: Arc::new(originals),
        now: Some(Arc::new(move || *clock.lock().unwrap())),
        authenticated_indexes: None,
        correlation_members: Vec::new(),
        receive: ReceiveConfig {
            profile: Some(profile),
            bounds: ReceiveBounds {
                max_record_bytes: n("max_record_bytes"),
                max_records_per_log: n("max_records_per_log"),
                max_offers_per_sender: n("max_offers_per_sender"),
                max_offer_bytes_per_sender: n("max_offer_bytes_per_sender"),
                max_offers: n("max_offers"),
                max_offer_bytes: n("max_offer_bytes"),
                max_offer_evidence_per_sender: n("max_offer_evidence_per_sender"),
                max_offer_evidence: n("max_offer_evidence"),
            },
            retention: Duration::from_secs(receive["retention_seconds"].as_u64().unwrap()),
        },
    })
    .unwrap()
}

/// A store error's class, by its kind (Go `errorClass`).
fn class(e: &ShelfError) -> Option<&'static str> {
    match e {
        ShelfError::Refused(_) => Some("refused"),
        ShelfError::OverLimit(_) => Some("over_limit"),
        ShelfError::Busy(_) => Some("busy"),
        ShelfError::Deleted(_) => Some("deleted"),
        ShelfError::Invalid(_) => Some("invalid"),
        _ => None,
    }
}

/// The class of a call's errors, in Go's `errors.Is` order over a joined
/// error.
fn class_of(errors: &[&ShelfError]) -> String {
    if errors.is_empty() {
        return String::new();
    }
    for want in ["refused", "over_limit", "busy", "deleted", "invalid"] {
        if errors.iter().any(|e| class(e) == Some(want)) {
            return want.into();
        }
    }
    format!(
        "other: {}",
        errors
            .iter()
            .map(|e| e.to_string())
            .collect::<Vec<_>>()
            .join("\n")
    )
}

fn hex_or_text(v: Sql, blob: bool) -> Value {
    match v {
        Sql::Null => Value::Null,
        Sql::Integer(i) => json!(i),
        Sql::Real(f) => json!(f),
        Sql::Text(t) if blob => json!(hex::encode(t.as_bytes())),
        Sql::Text(t) => json!(t),
        Sql::Blob(b) if blob => json!(hex::encode(b)),
        Sql::Blob(b) => json!(String::from_utf8_lossy(&b)),
    }
}

/// The store's dump (Go `dumpReceived`): each table's rows as objects with
/// their keys in byte order (Go's maps), every BLOB as lowercase hex, then
/// each sender's counted prefix in every log the tables name.
fn dump(path: &Path, shelf: &Shelf) -> String {
    let db = Connection::open(path).unwrap();
    let table = |query: &str, blobs: &[&str]| -> Vec<Value> {
        let mut stmt = db.prepare(query).unwrap();
        let columns: Vec<String> = stmt.column_names().iter().map(|c| c.to_string()).collect();
        let rows = stmt
            .query_map([], |r| {
                let mut row = BTreeMap::new();
                for (i, c) in columns.iter().enumerate() {
                    row.insert(
                        c.clone(),
                        hex_or_text(r.get(i)?, blobs.contains(&c.as_str())),
                    );
                }
                Ok(Value::Object(row.into_iter().collect::<Map<_, _>>()))
            })
            .unwrap();
        rows.collect::<Result<_, _>>().unwrap()
    };
    let received = table("SELECT digest, sender_key, log_id, position, prev, deal_id, offer, record, received_at FROM shelf_received ORDER BY digest", &["record"]);
    let proofs = table("SELECT sender_key, log_id, size, shelf_checkpoint_sha256, book_checkpoint, fact, status_opening, inclusion_proof, shelf_checkpoint, received_at, verified FROM shelf_received_proofs ORDER BY sender_key, log_id, size, shelf_checkpoint_sha256", &["book_checkpoint", "fact", "status_opening", "inclusion_proof", "shelf_checkpoint"]);
    let tombstones = table("SELECT digest, sender_key, received_at, deleted_at FROM shelf_received_tombstones ORDER BY digest", &[]);
    let evidence = table(
        "SELECT id, kind, a, b, noted_at FROM shelf_evidence ORDER BY id",
        &["a", "b"],
    );
    let conflicts = table("SELECT sender_key, log_id, kind, at, evidence_id, offer FROM shelf_received_conflicts ORDER BY sender_key, log_id, kind, at", &[]);
    let vacated = table("SELECT sender_key, log_id, position, digest, deal_id, offer FROM shelf_received_vacated ORDER BY sender_key, log_id, position, digest", &[]);
    let mut keys = BTreeSet::new();
    for rows in [&received, &conflicts, &vacated] {
        for r in rows {
            keys.insert((
                r["sender_key"].as_str().unwrap().to_string(),
                r["log_id"].as_str().unwrap().to_string(),
            ));
        }
    }
    let prefixes: Vec<Value> = keys
        .iter()
        .map(|(s, l)| {
            let p = shelf.counted_prefix(s, l).unwrap();
            json!({"fork_at": p.fork_at, "last": p.last, "log_id": l, "sender_key": s, "size": p.size})
        })
        .collect();
    let mut out = Map::new();
    out.insert("shelf_received".into(), Value::Array(received));
    out.insert("shelf_received_proofs".into(), Value::Array(proofs));
    out.insert("shelf_received_tombstones".into(), Value::Array(tombstones));
    out.insert("shelf_evidence".into(), Value::Array(evidence));
    out.insert("shelf_received_conflicts".into(), Value::Array(conflicts));
    out.insert("shelf_received_vacated".into(), Value::Array(vacated));
    out.insert("counted_prefixes".into(), Value::Array(prefixes));
    let mut text = go_marshal_indent(&Value::Object(out), "  ");
    text.push('\n');
    text
}

fn changes_of(c: &evidencebook::shelf::ReceiveChanges) -> Value {
    json!({"grew": c.grew, "shrank": c.shrank, "forked": c.forked, "covered": c.covered})
}

#[test]
fn the_receive_script_gives_each_expect_and_the_dump() {
    let v: Value = serde_json::from_str(&vector("script.json")).unwrap();
    let p = &v["profile"];
    let profile = Arc::new(VectorProfile {
        secrets: map(&p["secrets"]),
        admitted: strings(&p["admitted"]),
        answered: strings(&p["answered"]),
        proof_keys: map(&p["proof_keys"]),
    });
    let records = map(&v["records"]);
    let proofs: HashMap<String, StampProof> = serde_json::from_value(v["proofs"].clone()).unwrap();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shelf.db");
    let clock = Arc::new(Mutex::new(Time::from_unix_seconds(0)));
    let mut shelf = open(&path, profile.clone(), &v, clock.clone());
    for (i, step) in v["steps"].as_array().unwrap().iter().enumerate() {
        let at = Time::parse(step["at"].as_str().unwrap()).unwrap();
        *clock.lock().unwrap() = at;
        let mut expect = Map::new();
        match step["op"].as_str().unwrap() {
            "receive" => {
                let record = step["record"].as_str().map(|n| records[n].as_bytes());
                let proof = step["proof"].as_str().map(|n| &proofs[n]);
                let (result, errors_class) = match shelf.receive(record, proof) {
                    Ok(r) => (r, String::new()),
                    Err(e) => {
                        let class = class_of(&e.errors());
                        (e.result, class)
                    }
                };
                expect.insert("error".into(), json!(errors_class));
                if let Some(r) = &result.record {
                    let refused = class_of(&r.refused.iter().collect::<Vec<_>>());
                    expect.insert(
                        "record".into(),
                        json!({"digest": r.digest, "stored": r.stored, "duplicate": r.duplicate,
                            "deleted": r.deleted, "counted": r.counted, "ask_for_book": r.ask_for_book,
                            "refused": refused}),
                    );
                }
                if let Some(p) = &result.proof {
                    let refused = class_of(&p.refused.iter().collect::<Vec<_>>());
                    expect.insert(
                        "proof".into(),
                        json!({"accepted": p.accepted, "duplicate": p.duplicate, "refused": refused}),
                    );
                }
                expect.insert("changes".into(), changes_of(&result.changes));
            }
            "forget" => {
                let settled = step["settled"].as_bool().unwrap_or(false);
                let got = shelf.forget(step["deal"].as_str().unwrap(), at, |_| Ok(settled));
                let (deleted, error) = match got {
                    Ok(d) => (d, String::new()),
                    Err(e) => (Vec::new(), class_of(&[&e])),
                };
                expect.insert("error".into(), json!(error));
                if !deleted.is_empty() {
                    expect.insert("deleted".into(), json!(deleted));
                }
            }
            "reopen" => {
                shelf.release().unwrap();
                shelf = open(&path, profile.clone(), &v, clock.clone());
                expect.insert("error".into(), json!(""));
            }
            other => panic!("step {i}: unknown op {other:?}"),
        }
        assert_eq!(
            Value::Object(expect),
            step["expect"],
            "step {i} ({})",
            step["op"]
        );
    }
    let got = dump(&path, &shelf);
    shelf.release().unwrap();
    assert!(got == vector("after.json"), "after.json differs:\n{got}");
}
