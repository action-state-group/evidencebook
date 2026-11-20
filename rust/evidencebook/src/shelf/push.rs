//! The MCP and A2A entries of [`Shelf::receive`] (evidencebook#577): the
//! carriage of the record push and its answers, as Go's `interop_push.go` and
//! agent-action-capsule's `spec/bindings/record-push-v0.md` (RP) specify.
//! Like the Go entries they are carriage only: they move a record's octets
//! and a stamp proof's JSON octets as opaque bytes, hand them to
//! [`Shelf::receive`] and return its answer. They are transport-neutral and
//! take no transport identity: who may push is the receive profile's push
//! rule, which reads the sender from the record, never from the carriage (RP
//! §2.3 rule 2).

use super::{InclusionEvidence, ReceiveError, ReceiveResult, Shelf, ShelfError, StampProof};
use crate::derivation::decode_syntax;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use rand_core::{OsRng, RngCore};
use serde_json::{json, Map, Value};

/// The MCP revision the entry speaks.
pub const MCP_PROTOCOL_VERSION: &str = "2026-07-28";
/// The one tool a receiver that takes pushes over MCP lists (RP §3.1).
pub const MCP_TOOL_RECORD_PUSH: &str = "record_push";
/// The one `A2A-Version` the entry speaks.
pub const A2A_PROTOCOL_VERSION: &str = "1.0";
/// The extension a push is advertised and activated under (RP §4.1). It is
/// an identifier: it resolves only once the Internet-Draft it names is
/// posted, and none is.
pub const A2A_PUSH_EXTENSION_URI: &str =
    "https://datatracker.ietf.org/doc/draft-mih-agent-record-push/";

const PARSE_ERROR: i64 = -32700;
const INVALID_REQUEST: i64 = -32600;
const METHOD_NOT_FOUND: i64 = -32601;
const INVALID_PARAMS: i64 = -32602;
const INTERNAL_ERROR: i64 = -32603;
const UNSUPPORTED_PROTOCOL_VERSION: i64 = -32022;
const TASK_NOT_FOUND: i64 = -32001;
const PUSH_NOT_SUPPORTED: i64 = -32003;
const UNSUPPORTED_OPERATION: i64 = -32004;
const CONTENT_TYPE_NOT_SUPPORTED: i64 = -32005;
const VERSION_NOT_SUPPORTED: i64 = -32009;

const META_VERSION: &str = "io.modelcontextprotocol/protocolVersion";
const META_CAPABILITIES: &str = "io.modelcontextprotocol/clientCapabilities";
const META_SERVER_INFO: &str = "io.modelcontextprotocol/serverInfo";
const LIST_TTL: i64 = 3_600_000;
const JSON: &str = "application/json";

const TOOL_DESCRIPTION: &str = "Carries one record push (a pushed record, a stamp proof, or both) as opaque octets in base64url without padding, and returns the receiver's answer for each part pushed. The answer is an acknowledgment, never evidence of what the receiver holds.";
const A2A_DESCRIPTION: &str = "Takes record pushes. A push is one Message with one data Part holding {\"record\", \"stamp_proof\"}, each base64url octets without padding; the answer is one task Artifact with one data Part holding the answer for each part pushed. The answer is an acknowledgment, never evidence of what the receiver holds.";

fn tool() -> Value {
    json!({
        "name": MCP_TOOL_RECORD_PUSH,
        "title": "Record push",
        "description": TOOL_DESCRIPTION,
        "inputSchema": {
            "type": "object",
            "properties": {
                "record": {"type": "string", "pattern": "^[A-Za-z0-9_-]+$"},
                "stamp_proof": {"type": "string", "pattern": "^[A-Za-z0-9_-]+$"},
            },
            "minProperties": 1,
            "additionalProperties": false,
        },
        "outputSchema": {
            "type": "object",
            "properties": {
                "record": {
                    "type": "object",
                    "properties": {
                        "digest": {"type": "string"},
                        "stored": {"type": "boolean"},
                        "duplicate": {"type": "boolean"},
                        "deleted": {"type": "boolean"},
                        "counted": {"type": "boolean"},
                        "ask_for_book": {"type": "boolean"},
                        "refused": {"enum": ["", "invalid", "refused", "over_limit", "busy"]},
                    },
                    "required": ["digest", "stored", "duplicate", "deleted", "counted", "ask_for_book", "refused"],
                    "additionalProperties": false,
                },
                "stamp_proof": {
                    "type": "object",
                    "properties": {
                        "accepted": {"type": "boolean"},
                        "duplicate": {"type": "boolean"},
                        "refused": {"enum": ["", "invalid", "refused"]},
                    },
                    "required": ["accepted", "duplicate", "refused"],
                    "additionalProperties": false,
                },
            },
            "minProperties": 1,
            "additionalProperties": false,
        },
    })
}

fn marshal(v: &Value) -> Vec<u8> {
    // Every value built here is strings, booleans, integers, arrays and
    // objects, which serde_json always writes. Not JCS: a request id of any
    // size an integer may be is echoed as it was read.
    serde_json::to_vec(v).unwrap_or_else(|_| {
        br#"{"error":{"code":-32603,"message":"internal error"},"id":null,"jsonrpc":"2.0"}"#
            .to_vec()
    })
}

fn error(id: &Value, code: i64, message: &str, data: Option<Value>) -> Vec<u8> {
    let mut e = json!({"code": code, "message": message});
    if let Some(d) = data {
        e["data"] = d;
    }
    marshal(&json!({"jsonrpc": "2.0", "id": id, "error": e}))
}

/// A push object read under the input schema (RP §2.1): the octets of each
/// part pushed.
#[derive(Default)]
struct Push {
    record: Option<Vec<u8>>,
    proof: Option<Vec<u8>>,
}

/// A base64url string without padding that decodes strictly to at least one
/// octet.
fn strict_decode(v: &Value) -> Option<Vec<u8>> {
    let text = v.as_str()?;
    if text.is_empty()
        || !text
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
    {
        return None;
    }
    // The URL_SAFE_NO_PAD engine refuses padding, a remainder of one
    // character and trailing bits that are set.
    URL_SAFE_NO_PAD.decode(text).ok().filter(|o| !o.is_empty())
}

fn read_push(object: &Value) -> Option<Push> {
    let members = object.as_object()?;
    if members.is_empty() || members.len() > 2 {
        return None;
    }
    let mut push = Push::default();
    for (name, value) in members {
        let octets = strict_decode(value)?;
        match name.as_str() {
            "record" => push.record = Some(octets),
            "stamp_proof" => push.proof = Some(octets),
            _ => return None,
        }
    }
    Some(push)
}

fn refusal_class(e: Option<&ShelfError>) -> Option<&'static str> {
    match e {
        None => Some(""),
        Some(ShelfError::Refused(_)) => Some("refused"),
        Some(ShelfError::OverLimit(_)) => Some("over_limit"),
        Some(ShelfError::Busy(_)) => Some("busy"),
        Some(ShelfError::Invalid(_)) => Some("invalid"),
        Some(_) => None,
    }
}

/// The answer to a push (RP §2.2), one member for each part pushed; `None`
/// when the receiver could not complete it for a reason of its own (RP §2.3
/// rule 4): nothing is answered.
fn answer(shelf: &Shelf, push: &Push) -> Option<Value> {
    let proof = push.proof.as_ref().map(|octets| {
        // Read by SF §5.2; octets that are not that JSON read as an empty
        // proof, which SF §6.3 step 1 refuses as invalid.
        serde_json::from_slice::<StampProof>(octets).unwrap_or_else(|_| StampProof {
            fact: Vec::new(),
            status_opening: None,
            book_checkpoint: Vec::new(),
            inclusion: InclusionEvidence::default(),
            shelf_checkpoint: Vec::new(),
        })
    });
    let result: ReceiveResult = match shelf.receive(push.record.as_deref(), proof.as_ref()) {
        Ok(r) => r,
        Err(e) => {
            let ReceiveError {
                result,
                error,
                after,
            } = *e;
            if error.is_some() || after.is_some() {
                return None;
            }
            result
        }
    };
    let mut out = Map::new();
    if push.record.is_some() {
        let r = result.record.as_ref()?;
        let refused = refusal_class(r.refused.as_ref())?;
        out.insert(
            "record".into(),
            json!({"digest": r.digest, "stored": r.stored, "duplicate": r.duplicate,
                   "deleted": r.deleted, "counted": r.counted, "ask_for_book": r.ask_for_book,
                   "refused": refused}),
        );
    }
    if push.proof.is_some() {
        let p = result.proof.as_ref()?;
        let refused = refusal_class(p.refused.as_ref())?;
        if refused == "over_limit" || refused == "busy" {
            return None;
        }
        out.insert(
            "stamp_proof".into(),
            json!({"accepted": p.accepted, "duplicate": p.duplicate, "refused": refused}),
        );
    }
    Some(Value::Object(out))
}

/// A message is bounded before it is parsed (RP §2.3 rule 6): twice the
/// receiver's record bound plus 64 KiB.
fn max_message(shelf: &Shelf) -> usize {
    (2 * shelf
        .shared()
        .receive
        .bounds
        .with_defaults()
        .max_record_bytes) as usize
        + (64 << 10)
}

/// A JSON-RPC request id MCP and A2A allow: a string or an integer of at most
/// 18 digits, never null.
fn request_id(v: Option<&Value>) -> Option<Value> {
    match v? {
        Value::String(_) => v.cloned(),
        Value::Number(n) => n
            .as_i64()
            .filter(|i| i.unsigned_abs() < 1_000_000_000_000_000_000)
            .map(|_| v.cloned().unwrap_or(Value::Null)),
        _ => None,
    }
}

/// The JSON-RPC envelope of a received message, or the bytes to send back.
enum Envelope {
    Reply(Option<Vec<u8>>),
    Request {
        id: Value,
        method: String,
        message: Map<String, Value>,
    },
}

fn envelope(shelf: &Shelf, message: &[u8]) -> Envelope {
    if message.len() > max_message(shelf) {
        return Envelope::Reply(Some(error(
            &Value::Null,
            INVALID_REQUEST,
            "message too large",
            None,
        )));
    }
    let Ok(root) = decode_syntax(message) else {
        return Envelope::Reply(Some(error(&Value::Null, PARSE_ERROR, "parse error", None)));
    };
    let Value::Object(msg) = root else {
        return Envelope::Reply(Some(error(
            &Value::Null,
            INVALID_REQUEST,
            "not a JSON-RPC request object",
            None,
        )));
    };
    let method = msg.get("method").and_then(Value::as_str).map(str::to_owned);
    if !msg.contains_key("id") && method.is_some() {
        return Envelope::Reply(None); // a notification: never answered
    }
    let id = request_id(msg.get("id"));
    match (id, method) {
        (Some(id), Some(method)) if msg.get("jsonrpc") == Some(&json!("2.0")) => {
            Envelope::Request {
                id,
                method,
                message: msg,
            }
        }
        _ => Envelope::Reply(Some(error(
            &Value::Null,
            INVALID_REQUEST,
            "invalid JSON-RPC request",
            None,
        ))),
    }
}

/// The record push over MCP (RP §3): the tool `record_push`, and the
/// `server/discover` and `tools/list` requests MCP requires around it.
pub struct PushMcp<'a> {
    shelf: &'a Shelf,
    info: Value,
}

impl Shelf {
    /// The MCP entry of [`Shelf::receive`]. `server_version` is the version
    /// in the serverInfo each result carries; empty is `"unversioned"`.
    pub fn push_mcp(&self, server_version: &str) -> PushMcp<'_> {
        let version = if server_version.is_empty() {
            "unversioned"
        } else {
            server_version
        };
        PushMcp {
            shelf: self,
            info: json!({"name": "evidencebook", "version": version}),
        }
    }

    /// The A2A entry of [`Shelf::receive`].
    pub fn push_a2a(&self) -> PushA2a<'_> {
        PushA2a { shelf: self }
    }
}

impl PushMcp<'_> {
    fn result(&self, id: &Value, mut result: Value) -> Vec<u8> {
        result["resultType"] = json!("complete");
        result["_meta"] = json!({META_SERVER_INFO: self.info});
        marshal(&json!({"jsonrpc": "2.0", "id": id, "result": result}))
    }

    /// Answer one JSON-RPC message received over MCP: the message to send
    /// back, or `None` for a notification. Anything that is not a push under
    /// RP §3 gets a JSON-RPC error, a transport condition and never an
    /// answer; a push the receiver could not complete for a reason of its
    /// own gets an internal error and no answer (RP §2.3 rule 4).
    pub fn handle(&self, message: &[u8]) -> Option<Vec<u8>> {
        let (id, method, msg) = match envelope(self.shelf, message) {
            Envelope::Reply(r) => return r,
            Envelope::Request {
                id,
                method,
                message,
            } => (id, method, message),
        };
        let params = match msg.get("params") {
            None => Map::new(),
            Some(Value::Object(p)) => p.clone(),
            Some(_) => return Some(error(&id, INVALID_PARAMS, "params is not an object", None)),
        };
        if method == "server/discover" {
            return Some(self.result(
                &id,
                json!({"supportedVersions": [MCP_PROTOCOL_VERSION],
                       "capabilities": {"tools": {"listChanged": false}},
                       "ttlMs": LIST_TTL, "cacheScope": "public"}),
            ));
        }
        if method != "tools/list" && method != "tools/call" {
            return Some(error(&id, METHOD_NOT_FOUND, "method not found", None));
        }
        let meta = params.get("_meta").and_then(Value::as_object);
        let version = meta
            .and_then(|m| m.get(META_VERSION))
            .and_then(Value::as_str);
        let declared = meta
            .and_then(|m| m.get(META_CAPABILITIES))
            .is_some_and(Value::is_object);
        let Some(version) = version.filter(|_| declared) else {
            return Some(error(
                &id,
                INVALID_PARAMS,
                "_meta lacks the protocol version or client capabilities",
                None,
            ));
        };
        if version != MCP_PROTOCOL_VERSION {
            return Some(error(
                &id,
                UNSUPPORTED_PROTOCOL_VERSION,
                "unsupported protocol version",
                Some(json!({"supported": [MCP_PROTOCOL_VERSION], "requested": version})),
            ));
        }
        if method == "tools/list" {
            return Some(self.result(
                &id,
                json!({"tools": [tool()], "ttlMs": LIST_TTL, "cacheScope": "public"}),
            ));
        }
        if params.get("name").and_then(Value::as_str) != Some(MCP_TOOL_RECORD_PUSH) {
            return Some(error(&id, INVALID_PARAMS, "unknown tool", None));
        }
        let arguments = match params.get("arguments") {
            None => Value::Object(Map::new()),
            Some(a) if a.is_object() => a.clone(),
            Some(_) => {
                return Some(error(
                    &id,
                    INVALID_PARAMS,
                    "arguments is not an object",
                    None,
                ))
            }
        };
        let Some(push) = read_push(&arguments) else {
            return Some(error(
                &id,
                INVALID_PARAMS,
                "arguments do not match the record_push input schema",
                None,
            ));
        };
        let Some(answer) = answer(self.shelf, &push) else {
            return Some(error(&id, INTERNAL_ERROR, "internal error", None));
        };
        Some(self.result(
            &id,
            json!({"content": [], "isError": false, "structuredContent": answer}),
        ))
    }
}

/// The Agent Card extension entry that advertises a push (RP §4.1), the same
/// for every caller: a host that also serves the evidence request carriage
/// adds it to the one card's `capabilities.extensions`.
pub fn push_a2a_extension() -> Vec<u8> {
    marshal(
        &json!({"uri": A2A_PUSH_EXTENSION_URI, "description": A2A_DESCRIPTION, "required": false}),
    )
}

/// A task, context or artifact identifier: 128 random bits, so it encodes no
/// count and no time (RP §4.3).
fn identifier() -> String {
    let mut b = [0u8; 16];
    OsRng.fill_bytes(&mut b);
    hex::encode(b)
}

/// What a message received over A2A carries besides its body (A2A §3.2.6).
pub struct PushA2aRequest<'a> {
    /// The `A2A-Version` value; empty when absent.
    pub version: &'a str,
    /// The `A2A-Extensions` URIs.
    pub extensions: &'a [String],
    /// The JSON-RPC message.
    pub body: &'a [u8],
}

/// What [`PushA2a::handle`] returns: the JSON-RPC message to send back (`None`
/// for a notification) and the extensions it activated, to send as the
/// response's `A2A-Extensions`.
pub struct PushA2aResponse {
    pub body: Option<Vec<u8>>,
    pub extensions: Vec<String>,
}

/// The record push over A2A (RP §4): `SendMessage` answered with a completed
/// Task holding the answer in one Artifact. It keeps no task and mints
/// unpredictable identifiers.
pub struct PushA2a<'a> {
    shelf: &'a Shelf,
}

/// A `SendMessage` Message under RP §4.2: role `ROLE_USER`, the extension URI
/// in its extensions, exactly one Part whose content is `data` of media type
/// `application/json`, holding a push object. The error code otherwise.
fn a2a_push_message(message: &Map<String, Value>) -> Result<Push, i64> {
    let extensions = message.get("extensions").and_then(Value::as_array);
    let parts = message.get("parts").and_then(Value::as_array);
    let activated = extensions.is_some_and(|e| e.contains(&json!(A2A_PUSH_EXTENSION_URI)));
    let (true, Some(parts)) = (
        message.get("role") == Some(&json!("ROLE_USER")) && activated,
        parts,
    ) else {
        return Err(INVALID_PARAMS);
    };
    let [part] = parts.as_slice() else {
        return Err(INVALID_PARAMS);
    };
    let Some(part) = part.as_object() else {
        return Err(INVALID_PARAMS);
    };
    if ["text", "url", "raw"].iter().any(|k| part.contains_key(*k)) {
        return Err(INVALID_PARAMS);
    }
    let Some(data) = part.get("data").filter(|d| d.is_object()) else {
        return Err(INVALID_PARAMS);
    };
    if part.get("mediaType").and_then(Value::as_str) != Some(JSON) {
        return Err(CONTENT_TYPE_NOT_SUPPORTED);
    }
    read_push(data).ok_or(INVALID_PARAMS)
}

impl PushA2a<'_> {
    /// Answer one JSON-RPC message received over A2A. It speaks the methods
    /// of the evidence request carriage's A2A entry; of those only
    /// `SendMessage` carries a push.
    pub fn handle(&self, req: &PushA2aRequest<'_>) -> PushA2aResponse {
        let reply = |body: Vec<u8>| PushA2aResponse {
            body: Some(body),
            extensions: Vec::new(),
        };
        let (id, method, msg) = match envelope(self.shelf, req.body) {
            Envelope::Reply(r) => {
                return PushA2aResponse {
                    body: r,
                    extensions: Vec::new(),
                }
            }
            Envelope::Request {
                id,
                method,
                message,
            } => (id, method, message),
        };
        let fail =
            |code: i64, message: &str, data: Option<Value>| reply(error(&id, code, message, data));
        if req.version != A2A_PROTOCOL_VERSION {
            return fail(
                VERSION_NOT_SUPPORTED,
                "unsupported A2A version",
                Some(json!({"supported": [A2A_PROTOCOL_VERSION], "requested": req.version})),
            );
        }
        let params = match msg.get("params") {
            None => Map::new(),
            Some(Value::Object(p)) => p.clone(),
            Some(_) => return fail(INVALID_PARAMS, "params is not an object", None),
        };
        match method.as_str() {
            "SendMessage" => {}
            "SendStreamingMessage" | "SubscribeToTask" | "GetExtendedAgentCard" => {
                return fail(UNSUPPORTED_OPERATION, "not supported by this agent", None)
            }
            "GetTask" | "CancelTask" => return fail(TASK_NOT_FOUND, "task not found", None),
            "ListTasks" => {
                return reply(marshal(&json!({"jsonrpc": "2.0", "id": id, "result": {
                    "tasks": [], "nextPageToken": "", "pageSize": 0, "totalSize": 0}})))
            }
            "CreateTaskPushNotificationConfig"
            | "GetTaskPushNotificationConfig"
            | "ListTaskPushNotificationConfigs"
            | "DeleteTaskPushNotificationConfig" => {
                return fail(
                    PUSH_NOT_SUPPORTED,
                    "push notifications are not supported",
                    None,
                )
            }
            _ => return fail(METHOD_NOT_FOUND, "method not found", None),
        }
        if !req.extensions.iter().any(|e| e == A2A_PUSH_EXTENSION_URI) {
            return fail(
                INVALID_PARAMS,
                "the record push extension is not activated in A2A-Extensions",
                None,
            );
        }
        let Some(message) = params.get("message").and_then(Value::as_object) else {
            return fail(INVALID_PARAMS, "params has no message", None);
        };
        if message.contains_key("taskId") {
            return fail(TASK_NOT_FOUND, "task not found", None);
        }
        let push = match a2a_push_message(message) {
            Ok(p) => p,
            Err(code) => {
                return fail(
                    code,
                    "the message is not a record push under the extension",
                    None,
                )
            }
        };
        if let Some(configuration) = params.get("configuration") {
            let Some(c) = configuration.as_object() else {
                return fail(INVALID_PARAMS, "configuration is not an object", None);
            };
            if c.contains_key("taskPushNotificationConfig") {
                return fail(
                    PUSH_NOT_SUPPORTED,
                    "push notifications are not supported",
                    None,
                );
            }
            if let Some(modes) = c.get("acceptedOutputModes") {
                let Some(list) = modes.as_array() else {
                    return fail(INVALID_PARAMS, "acceptedOutputModes is not an array", None);
                };
                if !list.contains(&json!(JSON)) {
                    return fail(
                        CONTENT_TYPE_NOT_SUPPORTED,
                        "the answer is only application/json",
                        None,
                    );
                }
            }
        }
        let Some(answer) = answer(self.shelf, &push) else {
            return fail(INTERNAL_ERROR, "internal error", None);
        };
        let context_id = match message.get("contextId").and_then(Value::as_str) {
            Some(c) if !c.is_empty() => c.to_owned(),
            _ => identifier(),
        };
        let task = json!({
            "id": identifier(),
            "contextId": context_id,
            "status": {"state": "TASK_STATE_COMPLETED"},
            "artifacts": [{
                "artifactId": identifier(),
                "extensions": [A2A_PUSH_EXTENSION_URI],
                "parts": [{"data": answer, "mediaType": JSON}],
            }],
        });
        PushA2aResponse {
            body: Some(marshal(
                &json!({"jsonrpc": "2.0", "id": id, "result": {"task": task}}),
            )),
            extensions: vec![A2A_PUSH_EXTENSION_URI.to_owned()],
        }
    }
}
