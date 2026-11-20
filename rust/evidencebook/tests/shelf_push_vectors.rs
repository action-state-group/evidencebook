//! `testdata/shelf/receive-carriage`: the MCP and A2A entries of
//! `Shelf::receive` (evidencebook#577), as Go's `interop_push_vectors_test.go`
//! runs them. Each track runs on a fresh shelf under the receive vectors'
//! profile, bounds and retention (`testdata/shelf/receive/script.json`); every
//! `call` case must return its `expect`, compared as JSON, with an A2A task's
//! identifiers masked and an MCP result's server info removed. The vector
//! profile (the receive README's "The vector profile") is a test profile, not a
//! record format.
#![cfg(feature = "shelf")]

use capsule_emit_artifact::sqlite::SqliteStore as ArtifactSqlite;
use capsule_emit_artifact::Store as _;
use ed25519_dalek::SigningKey;
use evidencebook::canonical::jcs;
use evidencebook::shelf::{
    push_a2a_extension, PushA2aRequest, ReceiveBounds, ReceiveConfig, ReceiveProfile,
    ReceivedRecord, SeededStream, Shelf, ShelfConfig, Time, A2A_PUSH_EXTENSION_URI,
    MCP_PROTOCOL_VERSION, MCP_TOOL_RECORD_PUSH,
};
use rusqlite::Connection;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../testdata/shelf")
}

fn read(path: &str) -> Value {
    serde_json::from_slice(&std::fs::read(root().join(path)).unwrap()).unwrap()
}

fn key(label: &str) -> SigningKey {
    SigningKey::from_bytes(&Sha256::digest(label.as_bytes()).into())
}

/// The vector profile.
struct VectorProfile {
    secrets: HashMap<String, String>,
    admitted: HashSet<String>,
    answered: HashSet<String>,
    proof_keys: HashMap<String, String>,
}

impl VectorProfile {
    fn new(spec: &Value) -> Self {
        let map = |name: &str| -> HashMap<String, String> {
            spec[name]
                .as_object()
                .unwrap()
                .iter()
                .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_owned()))
                .collect()
        };
        let list = |name: &str| -> HashSet<String> {
            spec[name]
                .as_array()
                .unwrap()
                .iter()
                .map(|v| v.as_str().unwrap().to_owned())
                .collect()
        };
        VectorProfile {
            secrets: map("secrets"),
            admitted: list("admitted"),
            answered: list("answered"),
            proof_keys: map("proof_keys"),
        }
    }
}

impl ReceiveProfile for VectorProfile {
    fn read(&self, record: &[u8]) -> Result<ReceivedRecord, String> {
        let value: Value = serde_json::from_slice(record).map_err(|e| e.to_string())?;
        let obj = value.as_object().ok_or("not an object")?;
        const FIELDS: [&str; 9] = [
            "v", "sender", "log", "position", "prev", "digest", "deal", "offer", "sig",
        ];
        if obj.keys().any(|k| !FIELDS.contains(&k.as_str())) {
            return Err("a member the profile does not know".into());
        }
        // Canonical JSON of version 1, signed under the sender's secret.
        if jcs(&value).map_err(|e| e.to_string())? != record
            || obj.get("v").and_then(Value::as_u64) != Some(1)
        {
            return Err("not a canonical vector record of version 1".into());
        }
        let text = |n: &str| obj.get(n).and_then(Value::as_str).unwrap_or("").to_owned();
        let sender = text("sender");
        let secret = self.secrets.get(&sender).ok_or("an unknown sender")?;
        let mut body = obj.clone();
        let sig = body
            .remove("sig")
            .and_then(|s| s.as_str().map(str::to_owned));
        let mut hasher = Sha256::new();
        hasher.update(secret.as_bytes());
        hasher.update(jcs(&Value::Object(body)).map_err(|e| e.to_string())?);
        if sig.as_deref() != Some(hex::encode(hasher.finalize()).as_str()) {
            return Err("a bad signature".into());
        }
        Ok(ReceivedRecord {
            digest: text("digest"),
            sender_key: sender,
            log_id: text("log"),
            position: obj.get("position").and_then(Value::as_u64).unwrap_or(0),
            prev: text("prev"),
            deal_id: text("deal"),
            offer: obj.get("offer").and_then(Value::as_bool).unwrap_or(false),
        })
    }

    fn admit(&self, r: &ReceivedRecord) -> Result<bool, String> {
        Ok(r.offer || self.admitted.contains(&r.sender_key))
    }

    fn answered(&self, offer: &ReceivedRecord) -> Result<bool, String> {
        Ok(self.answered.contains(&offer.digest))
    }

    fn proof_sender(&self, key_id: &str, _log_id: &str) -> Result<Option<String>, String> {
        Ok(self.proof_keys.get(key_id).cloned())
    }
}

#[derive(Clone)]
struct Clock(Arc<Mutex<Time>>);

fn open(dir: &Path, script: &Value, clock: &Clock) -> Shelf {
    let signer = key("signer");
    let conn = Connection::open(dir.join("shelf.db")).unwrap();
    conn.busy_timeout(Duration::from_secs(5)).unwrap();
    let originals = ArtifactSqlite::new(conn, "test", [signer.verifying_key().to_bytes()]).unwrap();
    originals.init().unwrap();
    let b = &script["bounds"];
    let n = |name: &str| b[name].as_u64().unwrap();
    let now = clock.clone();
    Shelf::open_sqlite(ShelfConfig {
        path: dir.join("shelf.db"),
        lock_path: None,
        held_lock: None,
        lock_timeout: Some(Duration::from_secs(5)),
        random: Some(Box::new(SeededStream::new("push vectors"))),
        operator: "test-operator".into(),
        developer: String::new(),
        signer: Arc::new(signer),
        checkpoint_key: key("checkpoint"),
        originals: Arc::new(originals),
        now: Some(Arc::new(move || *now.0.lock().unwrap())),
        authenticated_indexes: None,
        correlation_members: Vec::new(),
        receive: ReceiveConfig {
            profile: Some(Arc::new(VectorProfile::new(&script["profile"]))),
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
            retention: Duration::from_secs(script["retention_seconds"].as_u64().unwrap()),
        },
    })
    .unwrap()
}

fn is_id(s: &str) -> bool {
    s.len() == 32 && s.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f'))
}

fn mask(body: &[u8]) -> Value {
    let mut m: Value = serde_json::from_slice(body).unwrap();
    if let Some(task) = m.pointer_mut("/result/task") {
        for pointer in ["/id", "/contextId", "/artifacts/0/artifactId"] {
            let slot = task.pointer_mut(pointer).unwrap();
            assert!(is_id(slot.as_str().unwrap()), "{slot}");
            *slot = json!("<id>");
        }
    }
    m
}

fn call(shelf: &Shelf, carriage: &str, case: &Value) -> Option<Value> {
    let body = serde_json::to_vec(&case["request"]).unwrap();
    if carriage == "mcp" {
        let mut m: Value = serde_json::from_slice(&shelf.push_mcp("v").handle(&body)?).unwrap();
        if let Some(result) = m.get_mut("result").and_then(Value::as_object_mut) {
            result.remove("_meta"); // the server info is the host's version
        }
        return Some(m);
    }
    let extensions: Vec<String> = case["a2a_extensions"]
        .as_array()
        .map(|a| a.iter().map(|e| e.as_str().unwrap().to_owned()).collect())
        .unwrap_or_default();
    let version = case["a2a_version"].as_str().unwrap_or("");
    let response = shelf.push_a2a().handle(&PushA2aRequest {
        version,
        extensions: &extensions,
        body: &body,
    });
    response.body.map(|b| mask(&b))
}

#[test]
fn the_carriage_vectors() {
    let script = read("receive/script.json");
    let vectors = read("receive-carriage/script.json");
    for track in vectors["tracks"].as_array().unwrap() {
        let carriage = track["carriage"].as_str().unwrap();
        let dir = tempfile::tempdir().unwrap();
        let clock = Clock(Arc::new(Mutex::new(
            Time::parse("2026-10-19T12:00:00Z").unwrap(),
        )));
        let mut shelf = open(dir.path(), &script, &clock);
        for (i, case) in track["cases"].as_array().unwrap().iter().enumerate() {
            *clock.0.lock().unwrap() = Time::parse(case["at"].as_str().unwrap()).unwrap();
            match case["op"].as_str().unwrap() {
                "call" => assert_eq!(
                    call(&shelf, carriage, case),
                    Some(case["expect"].clone()),
                    "{carriage} case {i}: {}",
                    case["note"]
                ),
                "forget" => {
                    // A refusal (a deal not yet settled) is part of the script.
                    let settled = case["settled"].as_bool().unwrap_or(false);
                    let at = *clock.0.lock().unwrap();
                    let _ = shelf.forget(case["deal"].as_str().unwrap(), at, |_| Ok(settled));
                }
                "reopen" => {
                    shelf.release().unwrap();
                    shelf = open(dir.path(), &script, &clock);
                }
                op => panic!("unknown op {op}"),
            }
        }
    }
}

fn b64(octets: &[u8]) -> String {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine as _;
    URL_SAFE_NO_PAD.encode(octets)
}

fn mcp_push(args: Value) -> Vec<u8> {
    serde_json::to_vec(
        &json!({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
        "_meta": {"io.modelcontextprotocol/protocolVersion": MCP_PROTOCOL_VERSION,
                  "io.modelcontextprotocol/clientCapabilities": {}},
        "name": MCP_TOOL_RECORD_PUSH, "arguments": args}}),
    )
    .unwrap()
}

fn code(body: &[u8]) -> i64 {
    let m: Value = serde_json::from_slice(body).unwrap();
    assert!(m.get("result").is_none(), "{m}");
    m["error"]["code"].as_i64().unwrap()
}

/// A receiver fault is an internal error and no answer (RP §2.3 rule 4): a
/// released shelf cannot complete a push.
#[test]
fn a_receiver_fault_is_an_internal_error_not_an_answer() {
    let script = read("receive/script.json");
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock(Arc::new(Mutex::new(
        Time::parse("2026-10-20T00:01:00Z").unwrap(),
    )));
    let shelf = open(dir.path(), &script, &clock);
    shelf.release().unwrap();
    let a1 = b64(script["records"]["a1"].as_str().unwrap().as_bytes());
    assert_eq!(
        code(
            &shelf
                .push_mcp("")
                .handle(&mcp_push(json!({"record": a1})))
                .unwrap()
        ),
        -32603
    );
    let body = serde_json::to_vec(
        &json!({"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": {"message": {
        "messageId": "m", "role": "ROLE_USER", "extensions": [A2A_PUSH_EXTENSION_URI],
        "parts": [{"mediaType": "application/json", "data": {"record": a1}}]}}}),
    )
    .unwrap();
    let response = shelf.push_a2a().handle(&PushA2aRequest {
        version: "1.0",
        extensions: &[A2A_PUSH_EXTENSION_URI.to_owned()],
        body: &body,
    });
    assert_eq!(code(&response.body.unwrap()), -32603);
    assert!(response.extensions.is_empty());
}

/// A message over the bound is -32600 before it is parsed, and a record one
/// byte over the record bound is answered `over_limit`, not met with a
/// transport condition (RP §2.3 rule 6).
#[test]
fn the_message_bound_and_over_limit() {
    let script = read("receive/script.json");
    let dir = tempfile::tempdir().unwrap();
    let clock = Clock(Arc::new(Mutex::new(
        Time::parse("2026-10-20T00:01:00Z").unwrap(),
    )));
    let shelf = open(dir.path(), &script, &clock);
    let bound = script["bounds"]["max_record_bytes"].as_u64().unwrap() as usize;
    let too_big = format!("{{\"x\":\"{}\"}}", "A".repeat(2 * bound + (64 << 10)));
    assert_eq!(
        code(&shelf.push_mcp("").handle(too_big.as_bytes()).unwrap()),
        -32600
    );
    let big = b64(&vec![0u8; bound + 1]);
    let out: Value = serde_json::from_slice(
        &shelf
            .push_mcp("")
            .handle(&mcp_push(json!({"record": big})))
            .unwrap(),
    )
    .unwrap();
    assert_eq!(
        out["result"]["structuredContent"]["record"]["refused"],
        "over_limit"
    );
}

#[test]
fn discovery_is_fixed() {
    let ext: Value = serde_json::from_slice(&push_a2a_extension()).unwrap();
    assert_eq!(ext["uri"], A2A_PUSH_EXTENSION_URI);
    assert_eq!(ext["required"], false);
    assert!(ext.get("params").is_none());
}
