//! The x-deal-v0 profile (`evidencebook::deal`, feature `deal`) against
//! what Go's `dealprofile` is checked against: the profile's own fixtures
//! (`dealprofile/x-deal-v0/fixtures`) and the deal plugin's frozen baseline
//! (`dealprofile/testdata/baseline-vectors.json`), byte for byte.

#![cfg(feature = "deal")]

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use evidencebook::canonical::{jcs, json_digest};
use evidencebook::deal::{self, Capsule, Chain, Decide, Disposition, Report, StepInput};
use serde::Deserialize;
use serde_json::value::RawValue;
use serde_json::{json, Map, Value};

fn repo() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../..")
}

fn profile_dir() -> PathBuf {
    repo().join("dealprofile/x-deal-v0")
}

fn fixtures_dir() -> PathBuf {
    profile_dir().join("fixtures")
}

fn read_json<T: for<'de> Deserialize<'de>>(path: &Path) -> T {
    let raw = fs::read(path).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    serde_json::from_slice(&raw).unwrap_or_else(|e| panic!("{}: {e}", path.display()))
}

fn sorted_json_files(dir: &Path) -> Vec<PathBuf> {
    let mut files: Vec<PathBuf> = fs::read_dir(dir)
        .unwrap_or_else(|e| panic!("{}: {e}", dir.display()))
        .map(|e| e.unwrap().path())
        .filter(|p| p.extension().is_some_and(|x| x == "json"))
        .collect();
    files.sort();
    assert!(!files.is_empty(), "{}", dir.display());
    files
}

fn stage_of(err: &deal::Error) -> &'static str {
    err.stage()
        .unwrap_or_else(|| panic!("not a stage refusal: {err}"))
        .stage
}

// --- vendored files ---------------------------------------------------------

#[test]
fn vendored_schemas_are_the_profiles() {
    let vendored = Path::new(env!("CARGO_MANIFEST_DIR")).join("schemas/vendor/x-deal-v0");
    let mut pairs = vec![(
        profile_dir().join("x-deal-v0.schema.json"),
        vendored.join("x-deal-v0.schema.json"),
    )];
    for f in sorted_json_files(&profile_dir().join("records")) {
        pairs.push((
            f.clone(),
            vendored.join("records").join(f.file_name().unwrap()),
        ));
    }
    for (want, got) in &pairs {
        assert_eq!(
            fs::read(want).unwrap(),
            fs::read(got).unwrap_or_else(|e| panic!("{}: {e}", got.display())),
            "{} is vendored byte for byte",
            want.display()
        );
    }
    assert_eq!(
        sorted_json_files(&vendored.join("records")).len(),
        pairs.len() - 1,
        "no other records schema is vendored"
    );
    assert_eq!(deal::profile_schema(), fs::read(&pairs[0].0).unwrap());
}

#[test]
fn the_public_suffix_tables_are_the_go_modules() {
    // The tables are golang.org/x/net v0.59.0's; a Go module on another
    // x/net needs that version's tables here.
    let go_mod = fs::read_to_string(repo().join("go.mod")).unwrap();
    assert!(
        go_mod
            .lines()
            .any(|l| l.trim() == "golang.org/x/net v0.59.0"),
        "go.mod moved off golang.org/x/net v0.59.0: vendor that version's publicsuffix/data"
    );
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("vendor/x-net-publicsuffix");
    for (name, sha256) in [
        ("text", TEXT_SHA256),
        ("nodes", NODES_SHA256),
        ("children", CHILDREN_SHA256),
    ] {
        use sha2::{Digest, Sha256};
        let raw = fs::read(dir.join(name)).unwrap();
        assert_eq!(hex::encode(Sha256::digest(&raw)), sha256, "{name}");
    }
}

// SHA-256 of golang.org/x/net@v0.59.0/publicsuffix/data/{text,nodes,children}.
const TEXT_SHA256: &str = "9b649c8a6aeabdb8971286d07683a20dc00e406a99631f960e0a47bb85df4af8";
const NODES_SHA256: &str = "47ed63d4f3763b7e4515345e1f06054d3bff97c6175a6f53466218e633a0b9cb";
const CHILDREN_SHA256: &str = "761402a681757153843a161c71955577e76a5ef96abc1a845d20a9a96304bfb7";

#[test]
fn record_types_are_the_schemas() {
    let s: Value = serde_json::from_slice(deal::profile_schema()).unwrap();
    let want: Vec<&str> = s["$defs"]["record_type"]["enum"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap())
        .collect();
    assert_eq!(want, deal::record_types());
}

// --- keys -------------------------------------------------------------------

#[derive(Deserialize)]
struct FingerprintVectors {
    store_secret_hex: String,
    deal_id: String,
    deal_key_hex: String,
    vectors: Vec<FpVector>,
    commitments: Vec<Commitment>,
}

#[derive(Deserialize)]
struct FpVector {
    kind: String,
    raw: String,
    normalized: String,
    fp: String,
}

#[derive(Deserialize)]
struct Commitment {
    label: String,
    nonce: String,
    text: String,
    commitment: String,
}

fn fingerprints() -> FingerprintVectors {
    read_json(&fixtures_dir().join("fingerprint-vectors.json"))
}

/// The values the checker scans every fixture for: the identifiers' raw
/// values, and each committed text long enough not to collide with a token.
fn local_store_values(v: &FingerprintVectors) -> Vec<String> {
    let mut out: Vec<String> = v.vectors.iter().map(|x| x.raw.clone()).collect();
    out.extend(
        v.commitments
            .iter()
            .filter(|x| x.text.chars().count() >= 24)
            .map(|x| x.text.clone()),
    );
    out
}

#[test]
fn fingerprint_vectors() {
    let v = fingerprints();
    let secret = hex::decode(&v.store_secret_hex).unwrap();
    let key = deal::deal_key(&secret, &v.deal_id);
    assert_eq!(hex::encode(key), v.deal_key_hex);
    for x in &v.vectors {
        assert_eq!(
            deal::normalize(&x.kind, &x.raw).as_deref(),
            Ok(x.normalized.as_str()),
            "{}",
            x.raw
        );
        assert_eq!(
            deal::fingerprint(&key, &x.kind, &x.raw).as_deref(),
            Ok(x.fp.as_str()),
            "{}",
            x.raw
        );
    }
    for x in &v.commitments {
        assert_eq!(
            deal::commit_text(&x.nonce, &x.text),
            x.commitment,
            "{}",
            x.label
        );
    }
    assert_eq!(
        deal::fingerprint(&key, "nickname", "x"),
        Err(deal::NotNormalizable),
        "a kind outside the closed set"
    );
    assert_eq!(
        deal::fingerprint(&key, "profile_id", "   "),
        Err(deal::NotNormalizable)
    );
    assert_ne!(deal::profile_key(&secret), key);
}

#[test]
fn normalize_refusals_and_go_string_semantics() {
    let n = |kind: &str, v: &str| deal::normalize(kind, v);
    assert_eq!(
        n("phone", "(415) 555-0199 ext. 12").as_deref(),
        Ok("+14155550199")
    );
    assert_eq!(n("phone", "555-0199"), Err(deal::NotNormalizable));
    assert_eq!(n("phone", "+1234567"), Err(deal::NotNormalizable));
    assert_eq!(n("email", "@a.example"), Err(deal::NotNormalizable));
    assert_eq!(n("email", "a@b@c.example"), Err(deal::NotNormalizable));
    // Go's strings.ToLower is the simple mapping: U+0130 is "i", not "i̇".
    assert_eq!(
        n("email", "\u{130}@x.example").as_deref(),
        Ok("i@x.example")
    );
    assert_eq!(n("domain", "localhost"), Err(deal::NotNormalizable));
    assert_eq!(n("domain", "co.uk"), Err(deal::NotNormalizable));
    assert_eq!(
        n("domain", "a_b.example"),
        Err(deal::NotNormalizable),
        "STD3"
    );
    assert_eq!(
        n("domain", "http://user@WWW.Shop.Example.co.uk:8080/x?y").as_deref(),
        Ok("example.co.uk")
    );
    assert_eq!(
        n("domain", "xn--bcher-kva.example").as_deref(),
        Ok("bücher.example")
    );
    assert_eq!(n("name", "The Inc."), Err(deal::NotNormalizable));
    assert_eq!(n("name", "The Straße Co., LLC").as_deref(), Ok("strasse"));
    assert_eq!(n("payee", "+1 415 555 0199").as_deref(), Ok("+14155550199"));
    assert_eq!(
        n("relay_address", " R@Relay.Example ").as_deref(),
        Ok("r@relay.example")
    );
}

#[derive(Deserialize)]
struct BoundsVectors {
    vectors: Vec<BoundsVector>,
}

#[derive(Deserialize)]
struct BoundsVector {
    document: Value,
    nonce: String,
    text: String,
    bounds_commitment: String,
}

#[test]
fn commercial_bounds_vectors() {
    let v: BoundsVectors = read_json(&fixtures_dir().join("commercial-bounds-vectors.json"));
    assert!(!v.vectors.is_empty());
    for x in &v.vectors {
        assert_eq!(
            String::from_utf8(jcs(&x.document).unwrap()).unwrap(),
            x.text
        );
        assert_eq!(deal::commit_text(&x.nonce, &x.text), x.bounds_commitment);
    }
}

// --- fixtures ---------------------------------------------------------------

#[derive(Deserialize)]
struct Positive {
    record: Box<RawValue>,
    expected_digest: String,
}

#[derive(Deserialize)]
struct Negative {
    record: Box<RawValue>,
    expected_stage: String,
    expected_error_contains: String,
    claimed_digest: Option<String>,
}

/// expected-digests.txt: `<digest>  <dir>/<file>`.
fn expected_digests() -> BTreeMap<String, String> {
    let text = fs::read_to_string(fixtures_dir().join("expected-digests.txt")).unwrap();
    let mut out = BTreeMap::new();
    for line in text.lines().map(str::trim) {
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let fields: Vec<&str> = line.split_whitespace().collect();
        assert_eq!(fields.len(), 2, "{line}");
        out.insert(fields[1].to_string(), fields[0].to_string());
    }
    assert!(!out.is_empty());
    out
}

#[test]
fn positive_fixtures() {
    let store = local_store_values(&fingerprints());
    let store: Vec<&str> = store.iter().map(String::as_str).collect();
    let digests = expected_digests();
    let mut n = 0;
    let mut listed = 0;
    for dir in [
        "positive",
        "positive-typed",
        "positive-typed-seller",
        "positive-sale",
    ] {
        for f in sorted_json_files(&fixtures_dir().join(dir)) {
            let name = format!("{dir}/{}", f.file_name().unwrap().to_string_lossy());
            let fx: Positive = read_json(&f);
            let (payload, digest) = deal::encode(fx.record.get().as_bytes(), &store)
                .unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(digest, fx.expected_digest, "{name}");
            if let Some(want) = digests.get(&name) {
                assert_eq!(&digest, want, "{name} in expected-digests.txt");
                listed += 1;
            }
            deal::check_digest(&payload, &digest).unwrap();
            n += 1;
        }
    }
    assert!(n > 0);
    assert_eq!(
        listed,
        digests.len(),
        "every listed digest is a positive fixture"
    );
}

/// The schema-stage negatives whose expected text is the reference
/// validator's own wording (jsonschema 4.23), which no member name or
/// location in another validator's report carries, and the text this
/// crate's validator (boon) reports for the same failure.
const SCHEMA_WORDING_ONLY: &[(&str, &str)] = &[
    ("neg-agent-card-with-words.json", "/body: not failed"),
    (
        "neg-typed-seller-offer-no-proposal-beside-source.json",
        "/body: not failed",
    ),
    (
        "neg-typed-seller-source-on-a-commit.json",
        "/body/action: value must be 'offer'",
    ),
];

/// Every negative the reference checker rejects before the chain stage is
/// rejected here at the same stage, for the same reason: its expected text
/// is in the refusal (at the schema stage, in one of the failed leaves). A
/// negative it rejects at the chain stage passes every record-level stage
/// here, as in Go.
#[test]
fn negative_fixtures() {
    let store = local_store_values(&fingerprints());
    let store: Vec<&str> = store.iter().map(String::as_str).collect();
    let mut chain = 0;
    let mut wording_only = 0;
    let files = sorted_json_files(&fixtures_dir().join("negative"));
    for f in &files {
        let name = f.file_name().unwrap().to_string_lossy().to_string();
        let fx: Negative = read_json(f);
        let raw = fx.record.get().as_bytes();
        let mut res = deal::check(raw, &store);
        if res.is_ok() {
            if let Some(claimed) = &fx.claimed_digest {
                res = deal::check_digest(raw, claimed);
            }
        }
        if fx.expected_stage == "chain" {
            assert!(
                res.is_ok(),
                "{name} passes the record-level stages: {res:?}"
            );
            chain += 1;
            continue;
        }
        let err = res.expect_err(&name);
        let se = err.stage().unwrap_or_else(|| panic!("{name}: {err}"));
        assert_eq!(se.stage, fx.expected_stage, "{name}: {err}");
        let mut want = fx.expected_error_contains.as_str();
        if let Some((_, text)) = SCHEMA_WORDING_ONLY.iter().find(|(n, _)| *n == name) {
            assert_eq!(fx.expected_stage, deal::STAGE_SCHEMA);
            want = text;
            wording_only += 1;
        }
        let all = format!("{se}\n{}", se.causes.join("\n"));
        assert!(all.contains(want), "{name}: {want:?} not in\n{all}");
    }
    assert!(chain > 0 && chain < files.len());
    assert_eq!(wording_only, SCHEMA_WORDING_ONLY.len());
}

// --- refusals ---------------------------------------------------------------

fn first_positive() -> Value {
    let fx: Value = read_json(&fixtures_dir().join("positive/01-baseline.json"));
    fx["record"].clone()
}

#[test]
fn encode_refusals() {
    let rec = first_positive();
    let raw = serde_json::to_vec(&rec).unwrap();
    let (_, digest) = deal::encode(&raw, &[]).unwrap();
    assert_eq!(digest.len(), 64);

    // Any string the record carries, taken as a raw local value, is refused,
    // matched case-insensitively.
    let some = ["deal_type", "channel"]
        .iter()
        .filter_map(|k| rec["body"][k].as_str())
        .find(|v| v.len() > 2)
        .expect("the fixture names a deal type or channel")
        .to_uppercase();
    let err = deal::encode(&raw, &[some.as_str()]).unwrap_err();
    assert_eq!(stage_of(&err), deal::STAGE_PERSONAL_DATA);

    let enc = |s: &str| deal::encode(s.as_bytes(), &[]).unwrap_err();
    let err = enc(r#"{"x-deal-v0":{"canonicalization":"jcs","seq":1.5},"body":{}}"#);
    assert_eq!(stage_of(&err), deal::STAGE_CANONICALIZATION);
    assert!(
        err.to_string()
            .contains("floating-point value at $.x-deal-v0.seq"),
        "worded as the reference checker words it: {err}"
    );
    let err = enc(r#"{"x-deal-v0":{"canonicalization":"jcs","seq":9007199254740992},"body":{}}"#);
    assert_eq!(
        err.to_string(),
        "[canonicalization] not JCS-serializable: unsafe integer at : outside ±9007199254740991 (§5.1)",
        "worded as Go's canonical.JCS words it"
    );
    let err =
        enc(r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"call +١٢١٢٥٥٥٠١٢٣"}}"#);
    assert_eq!(
        stage_of(&err),
        deal::STAGE_PERSONAL_DATA,
        "Arabic-Indic digits are digits, as in the reference checker"
    );
    let err = enc("{\"x-deal-v0\":{\"canonicalization\":\"jcs\"},\"body\":{\"text\":\"call 415\u{a0}555\u{2009}0199\"}}");
    assert_eq!(
        stage_of(&err),
        deal::STAGE_PERSONAL_DATA,
        "Unicode white space separates a number's digits"
    );
    let err =
        enc(r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"write to a@b.example"}}"#);
    assert_eq!(
        err.to_string(),
        "[personal_data] raw email address at $.body.text"
    );

    for raw in [
        r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\ud800"}}"#,
        r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\udc00x"}}"#,
        r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\ud800A"}}"#,
    ] {
        assert_eq!(stage_of(&enc(raw)), deal::STAGE_CANONICALIZATION, "{raw}");
    }
    let mut bad = br#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"#.to_vec();
    bad.extend_from_slice(b"\"\xff\"}}");
    let err = deal::encode(&bad, &[]).unwrap_err();
    assert_eq!(stage_of(&err), deal::STAGE_CANONICALIZATION);
    // A surrogate pair, and an escaped backslash before a u, are text.
    for raw in [
        r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"😀"}}"#,
        r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"\\ud800"}}"#,
    ] {
        assert_eq!(stage_of(&enc(raw)), deal::STAGE_SCHEMA, "{raw}");
    }

    let err = enc(r#"{"x-deal-v0":{"canonicalization":"jcs-n"},"body":{}}"#);
    assert_eq!(
        err.to_string(),
        "[canonicalization] canonicalization must be exactly \"jcs\" (got jcs-n); fail closed"
    );
    let err = enc(r#"{"x-deal-v0":{},"body":{}}"#);
    assert!(err.to_string().contains("(got <nil>)"), "{err}");
    let err = deal::check(b"[1]", &[]).unwrap_err();
    assert_eq!(err.to_string(), "[canonicalization] no x-deal-v0 block");
    let err = enc(r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{"text":"a Scam"}}"#);
    assert_eq!(
        err.to_string(),
        "[wording] prohibited word \"scam\" at $.body.text"
    );
    let err = enc(r#"{"x-deal-v0":{"canonicalization":"jcs"},"body":{}}"#);
    assert_eq!(stage_of(&err), deal::STAGE_SCHEMA);
    let se = err.stage().unwrap();
    assert_eq!(se.detail, se.causes[0]);
    let err = enc(r#"{"type":"payment/v0","canonicalization":"jcs"}"#);
    assert_eq!(
        err.to_string(),
        "[schema] type: \"payment/v0\" is not a typed action record"
    );
    assert!(matches!(enc("{"), deal::Error::Json(_)), "not JSON");

    deal::check_digest(&raw, &digest).unwrap();
    let err = deal::check_digest(&raw, &"0".repeat(64)).unwrap_err();
    assert_eq!(stage_of(&err), deal::STAGE_DIGEST);
    assert_eq!(deal::digest(&raw).unwrap(), digest);
}

#[test]
fn report_payload_refusals() {
    let r = Report {
        deal_id: "deal-0123456789abcdef".into(),
        audience: deal::AUDIENCE_KEEP.into(),
        nonce: "a".repeat(64),
        body: Map::from_iter([("n".to_string(), json!(1))]),
        checkpoint: None,
    };
    let payload = deal::report_payload(&r).unwrap();
    assert_eq!(
        String::from_utf8(payload).unwrap(),
        format!(
            r#"{{"audience":"keep","deal_id":"deal-0123456789abcdef","nonce":"{}","profile":"x-deal-v0","report":{{"n":1}},"type":"deal_report"}}"#,
            r.nonce
        )
    );
    let mut bad = r.clone();
    bad.audience = "history".into();
    let err = deal::report_payload(&bad).unwrap_err();
    assert!(err.to_string().contains("not a copy audience"), "{err}");
    let mut bad = r.clone();
    bad.nonce = "A".repeat(64);
    let err = deal::report_payload(&bad).unwrap_err();
    assert!(err.to_string().contains("64 lowercase hex"), "{err}");
    let mut bad = r;
    bad.body.insert("x".into(), json!(1.5));
    assert!(matches!(
        deal::report_payload(&bad),
        Err(deal::Error::Jcs(_))
    ));
}

#[test]
fn report_capsule_is_to_the_second() {
    let c = deal::report_capsule("deal", "capsulectl-deal", 1_790_532_000).unwrap();
    assert_eq!(
        c.developer, "capsulectl-deal",
        "the producer names its own developer"
    );
    assert_eq!(c.timestamp, "2026-09-27T18:00:00Z");
    assert_eq!(c.action_id, deal::REPORT_ACTION_ID);
    assert_eq!(c.action_type, deal::ACTION_TYPE_FYI);
    assert!(c.chain.is_none(), "a report is not chained");
    assert!(c.extensions.is_empty());
    let err = deal::report_capsule("deal", "", 0).unwrap_err();
    assert!(err.to_string().contains("developer name"), "{err}");
}

#[test]
fn step_capsule_refusals() {
    let input = StepInput {
        deal_id: "deal-0123456789abcdef".into(),
        seq: 1,
        operator: "deal".into(),
        developer: "a-node".into(),
        at: 7,
        parent: None,
        settlement: None,
        decide: None,
        local: String::new(),
        amount: String::new(),
        currency: String::new(),
    };
    let (c, h) = deal::step_capsule(&input).unwrap();
    assert_eq!(
        c.developer, "a-node",
        "the producer names its own developer"
    );
    assert_eq!(c.timestamp, "1970-01-01T00:00:07Z");
    assert_eq!(c.timestamp, h.committed_at);
    assert_eq!(c.action_id, "deal-0123456789abcdef/1");
    assert_eq!(c.action_type, deal::ACTION_TYPE_FYI);
    assert!(c.chain.is_none());
    assert!(h.cites.is_empty(), "the first step cites nothing");
    assert_eq!(c.extensions.len(), 1);
    assert_eq!(c.extensions[0].name, deal::DEAL_ID_MEMBER);

    let refused = |f: &dyn Fn(&mut StepInput)| {
        let mut bad = input.clone();
        f(&mut bad);
        deal::step_capsule(&bad).unwrap_err().to_string()
    };
    assert!(refused(&|b| b.seq = 0).contains("seq >= 1"));
    assert!(refused(&|b| b.deal_id.clear()).contains("deal id"));
    assert!(refused(&|b| b.developer.clear()).contains("developer name"));
    let err = refused(&|b| {
        b.parent = Some(Chain {
            parent_capsule_id: "a".repeat(64),
            relation: "supersedes".into(),
        })
    });
    assert!(err.contains("not follows or confirms"), "{err}");
}

// --- the deal plugin's baseline ---------------------------------------------

#[derive(Deserialize)]
struct Baseline {
    deals: Vec<BaselineDeal>,
}

#[derive(Deserialize)]
struct BaselineDeal {
    flow: String,
    deal_id: String,
    operator: String,
    steps: Vec<BaselineStep>,
    #[serde(default)]
    reports: Vec<BaselineReport>,
}

#[derive(Deserialize)]
struct BaselineStep {
    n: i64,
    payload: String,
    digest: String,
    capsule_id: String,
    capsule: String,
    header: BaselineHeader,
}

#[derive(Deserialize)]
struct BaselineHeader {
    record_type: String,
    epistemic_type: String,
    committed_at: String,
    subject_ref: String,
    correlation: BTreeMap<String, String>,
    links: Vec<BaselineLink>,
    fields: BTreeMap<String, String>,
}

#[derive(Deserialize)]
struct BaselineLink {
    #[serde(rename = "type")]
    link_type: String,
    target: String,
}

#[derive(Deserialize)]
struct BaselineReport {
    form: String,
    timestamp: String,
    agent_input: String,
    payload: String,
    capsule_id: String,
    capsule: String,
}

/// The developer the capsulectl deal plugin names.
const PLUGIN_DEVELOPER: &str = "capsulectl-deal";

fn baseline() -> Baseline {
    let b: Baseline = read_json(&repo().join("dealprofile/testdata/baseline-vectors.json"));
    assert!(!b.deals.is_empty());
    b
}

fn unix_seconds(rfc3339: &str) -> i64 {
    let t = evidence_request::time::parse_utc(rfc3339).unwrap_or_else(|| panic!("{rfc3339}"));
    assert_eq!(t.nanos, 0, "{rfc3339} is to the second");
    t.seconds
}

/// The Capsule capsule-emit's Go `emit.Seal` builds from these members and
/// a payload digest (capsule-emit/go v0.3.0 `Build`), without its Producer
/// Envelope: what the baseline's `capsule` holds. It stands in for the
/// producer's sealer, to show the members are the ones Go seals.
fn seal_like_go(c: &Capsule, agent_input_digest: &str) -> (String, Vec<u8>) {
    let effect_mode = match &c.effect {
        Some(e) if e.status == "confirmed" => "confirmed",
        Some(e) if e.status != "planned" => "dispatched_unconfirmed",
        _ => "not_applicable",
    };
    let mut p = json!({
        "spec_version": "draft-mih-scitt-agent-action-capsule-05",
        "format_version": "4",
        "canonicalization_id": "jcs",
        "action_id": c.action_id,
        "action_type": c.action_type,
        "operator": c.operator,
        "developer": c.developer,
        "timestamp": c.timestamp,
        "assurance": {
            "effect_mode": effect_mode,
            "attestation_mode": "self_attested",
            "ledger_mode": if c.chain.is_some() { "chained" } else { "standalone" },
        },
    });
    if let Some(d) = &c.disposition {
        let mut m = json!({"decision": d.decision, "approver": d.approver, "human_disposed": d.human_disposed});
        if !d.verdict_class.is_empty() {
            m["verdict_class"] = json!(d.verdict_class);
        }
        if !d.reason_digest.is_empty() {
            m["reason_digest"] = json!(d.reason_digest);
        }
        p["disposition"] = m;
    }
    if let Some(e) = &c.effect {
        let mut m = json!({"type": e.effect_type, "status": e.status, "irreversibility_class": e.irreversibility_class});
        if !e.effect_attestation.is_empty() {
            m["effect_attestation"] = json!(e.effect_attestation);
        }
        p["effect"] = m;
    }
    if let Some(ch) = &c.chain {
        p["chain"] = json!({"parent_capsule_id": ch.parent_capsule_id, "relation": ch.relation});
    }
    let attestation = "model_attestation"; // vocabulary-gate: AAC capsule member
    p[attestation] = json!({"compute_attestation": {"agent_input_digest": agent_input_digest}});
    for x in &c.extensions {
        p[x.name.as_str()] = x.value.clone();
    }
    let id = json_digest(&p).unwrap();
    p["capsule_id"] = json!(id);
    (id, jcs(&p).unwrap())
}

/// Rebuilds every step: payload, digest, Capsule members (and the Capsule
/// they seal to: capsule_id and bytes) and header fields. With
/// `EVIDENCEBOOK_DEAL_BUILT_CHAINS` set to a directory, it also writes each
/// deal's chain as this crate encoded it (one JSON array per deal), for the
/// reference checker's chain stage (check_profile.py FILE...), as Go's
/// TestBaselineSteps does with DEALPROFILE_BUILT_CHAINS.
#[test]
fn baseline_steps() {
    let out = std::env::var_os("EVIDENCEBOOK_DEAL_BUILT_CHAINS");
    let mut decides = 0;
    for d in baseline().deals {
        let mut chain = Vec::new();
        for s in &d.steps {
            let at = format!("{} step {}", d.flow, s.n);
            let (payload, digest) =
                deal::encode(s.payload.as_bytes(), &[]).unwrap_or_else(|e| panic!("{at}: {e}"));
            assert_eq!(
                String::from_utf8(payload.clone()).unwrap(),
                s.payload,
                "{at} payload"
            );
            assert_eq!(digest, s.digest, "{at} digest");
            chain.push(String::from_utf8(payload).unwrap());

            let facts: Value = serde_json::from_str(&s.capsule).unwrap();
            let mut input = StepInput {
                deal_id: d.deal_id.clone(),
                seq: s.n,
                operator: d.operator.clone(),
                developer: PLUGIN_DEVELOPER.into(),
                at: unix_seconds(facts["timestamp"].as_str().unwrap()),
                parent: None,
                settlement: facts.get("settlement").cloned(),
                decide: None,
                local: s.header.fields[deal::LOCAL_FIELD].clone(),
                amount: s.header.fields[deal::AMOUNT_FIELD].clone(),
                currency: s.header.fields[deal::CURRENCY_FIELD].clone(),
            };
            if let Some(c) = facts.get("chain") {
                input.parent = Some(Chain {
                    parent_capsule_id: c["parent_capsule_id"].as_str().unwrap().into(),
                    relation: c["relation"].as_str().unwrap().into(),
                });
            }
            if facts["action_type"] == deal::ACTION_TYPE_DECIDE {
                let (disp, eff) = (&facts["disposition"], &facts["effect"]);
                input.decide = Some(Decide {
                    disposition: Disposition {
                        decision: disp["decision"].as_str().unwrap().into(),
                        approver: disp["approver"].as_str().unwrap().into(),
                        human_disposed: disp["human_disposed"].as_bool().unwrap(),
                        verdict_class: disp["verdict_class"].as_str().unwrap_or_default().into(),
                        reason_digest: String::new(),
                    },
                    effect_type: eff["type"].as_str().unwrap().into(),
                    irreversibility_class: eff["irreversibility_class"].as_str().unwrap().into(),
                });
                decides += 1;
            }
            let (capsule, header) =
                deal::step_capsule(&input).unwrap_or_else(|e| panic!("{at}: {e}"));
            let (id, bytes) = seal_like_go(&capsule, &digest);
            assert_eq!(id, s.capsule_id, "{at} capsule_id");
            assert_eq!(String::from_utf8(bytes).unwrap(), s.capsule, "{at} Capsule");

            assert_eq!(header.record_type, s.header.record_type);
            assert_eq!(header.epistemic_type, s.header.epistemic_type);
            assert_eq!(header.committed_at, s.header.committed_at);
            assert_eq!(header.subject_ref, s.header.subject_ref);
            assert!(
                s.header.correlation.is_empty(),
                "a step names no correlation"
            );
            let cites: Vec<String> = s
                .header
                .links
                .iter()
                .map(|l| {
                    assert_eq!(l.link_type, "cites");
                    l.target.clone()
                })
                .collect();
            assert_eq!(header.cites, cites, "{at} cites");
            assert_eq!(header.fields, s.header.fields, "{at} fields");
        }
        if let Some(dir) = &out {
            let path = Path::new(dir).join(format!("{}.json", d.flow));
            fs::write(&path, format!("[{}]", chain.join(","))).unwrap();
        }
    }
    assert!(decides > 0, "a decide step is frozen");
}

#[derive(Deserialize)]
struct ReportInput {
    deal_id: String,
    audience: String,
    nonce: String,
    report: Map<String, Value>,
    deal_checkpoint: Option<CheckpointInput>,
}

#[derive(Deserialize)]
struct CheckpointInput {
    log_id: String,
    mmr_size: u64,
    root: String,
}

#[test]
fn baseline_reports() {
    let mut forms = BTreeMap::<String, usize>::new();
    for d in baseline().deals {
        for r in &d.reports {
            *forms.entry(r.form.clone()).or_default() += 1;
            let input: ReportInput = serde_json::from_str(&r.agent_input).unwrap();
            assert_eq!(
                r.form == "standalone",
                input.deal_checkpoint.is_some(),
                "only a standalone report binds a checkpoint"
            );
            let payload = deal::report_payload(&Report {
                deal_id: input.deal_id,
                audience: input.audience,
                nonce: input.nonce,
                body: input.report,
                checkpoint: input.deal_checkpoint.map(|c| deal::Checkpoint {
                    log_id: c.log_id,
                    mmr_size: c.mmr_size,
                    root: c.root,
                }),
            })
            .unwrap();
            assert_eq!(
                String::from_utf8(payload.clone()).unwrap(),
                r.payload,
                "{} payload",
                r.form
            );
            let capsule =
                deal::report_capsule(&d.operator, PLUGIN_DEVELOPER, unix_seconds(&r.timestamp))
                    .unwrap();
            let doc: Value = serde_json::from_slice(&payload).unwrap();
            let (id, bytes) = seal_like_go(&capsule, &json_digest(&doc).unwrap());
            assert_eq!(id, r.capsule_id, "{} capsule_id", r.form);
            assert_eq!(
                String::from_utf8(bytes).unwrap(),
                r.capsule,
                "{} Capsule",
                r.form
            );
        }
    }
    assert!(
        forms.get("standalone").is_some_and(|n| *n > 0),
        "a standalone report is frozen"
    );
    assert!(
        forms.get("in_log").is_some_and(|n| *n > 0),
        "an in-log report is frozen"
    );
}
