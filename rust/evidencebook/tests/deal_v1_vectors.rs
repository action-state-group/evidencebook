//! The x-deal-v1 vectors under `testdata/deal-v1/` through
//! `evidencebook::deal::v1`, each read from the file alone, as Go's
//! `xdealv1` writes them.

#![cfg(feature = "deal")]

use std::fs;
use std::path::{Path, PathBuf};

use evidencebook::deal::v1;
use serde_json::Value;

fn vectors(rel: &str) -> Vec<Value> {
    let p: PathBuf = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../testdata/deal-v1")
        .join(rel);
    let raw = fs::read(&p).unwrap_or_else(|e| panic!("{}: {e}", p.display()));
    let v: Value = serde_json::from_slice(&raw).unwrap();
    let cases = v["cases"].as_array().unwrap().clone();
    assert!(!cases.is_empty(), "{rel} has cases");
    cases
}

/// start/vectors.json: Start over each input gives its result.
#[test]
fn start_vectors() {
    for c in vectors("start/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let input: v1::StartInput =
            serde_json::from_value(c["input"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let got = v1::start(&input).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&got).unwrap(), c["expect"], "{name}");
    }
}

/// start/books.json: StartFromBooks over each case's books gives its input
/// and its result.
#[test]
fn start_book_vectors() {
    for c in vectors("start/books.json") {
        let name = c["name"].as_str().unwrap();
        let books: v1::StartBooks =
            serde_json::from_value(c["books"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let (input, got) = v1::start_from_books(&books).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(
            serde_json::to_value(&input).unwrap(),
            c["input"],
            "{name}: input"
        );
        assert_eq!(
            serde_json::to_value(&got).unwrap(),
            c["expect"],
            "{name}: result"
        );
    }
}

/// status/vectors.json: BuildStatus over each case's books gives its status
/// value, and rowstatus over each row resolved at `at` with `stamps` gives
/// its row result.
#[test]
fn status_vectors() {
    use std::collections::BTreeMap;
    for c in vectors("status/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let books: v1::StatusBooks =
            serde_json::from_value(c["books"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let got = v1::build_status(&books).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(
            serde_json::to_value(&got).unwrap(),
            c["status"],
            "{name}: status value"
        );
        let at = c["at"].as_str().unwrap();
        let stamps: BTreeMap<u64, String> = match c.get("stamps") {
            Some(s) => serde_json::from_value(s.clone()).unwrap(),
            None => BTreeMap::new(),
        };
        for row in &got.rows {
            let input = v1::resolve_row(row, at, &stamps).unwrap_or_else(|e| panic!("{name}: {e}"));
            let res =
                evidencebook::row_status::compute(&input).unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(
                Some(res.status.as_str()),
                c["rows"][&row.id].as_str(),
                "{name}, row {}",
                row.id
            );
        }
    }
}

/// report/vectors.json: BuildReport over each case's books and copy gives
/// its report, and its payload is the JCS of that report.
#[test]
fn report_vectors() {
    for c in vectors("report/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let books: v1::ReportBooks =
            serde_json::from_value(c["books"].clone()).unwrap_or_else(|e| panic!("{name}: {e}"));
        let cp = &c["copy"];
        let copy = v1::ReportCopy {
            audience: cp["audience"].as_str().unwrap().into(),
            nonce: cp["nonce"].as_str().unwrap().into(),
            referee: cp
                .get("referee")
                .and_then(Value::as_str)
                .unwrap_or("")
                .into(),
            as_of: cp["as_of"].as_str().unwrap().into(),
            text: cp.get("report").and_then(Value::as_object).cloned(),
        };
        let got = v1::build_report(&books, &copy).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(&got).unwrap(), c["report"], "{name}");
        let payload = v1::report_payload(&got).unwrap();
        assert_eq!(
            payload,
            evidencebook::canonical::jcs(&c["report"]).unwrap(),
            "{name}: payload"
        );
    }
}

/// settled/vectors.json: each case's rows settle as given.
#[test]
fn settled_vectors() {
    for c in vectors("settled/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let rows: Vec<v1::ReportRow> = c["rows"]
            .as_array()
            .unwrap()
            .iter()
            .map(|s| {
                let s = s.as_str().unwrap();
                let (s, made) = match s.strip_suffix("+made") {
                    Some(rest) => (rest, true),
                    None => (s, false),
                };
                let (status, extra) = s.split_once(':').unwrap_or((s, ""));
                v1::ReportRow {
                    id: String::new(),
                    owed_by: String::new(),
                    owed: Value::Null,
                    due: String::new(),
                    window_end: String::new(),
                    status: status.into(),
                    value: (status != "ruled" && !extra.is_empty()).then(|| extra.into()),
                    remedy: (status == "ruled" && !extra.is_empty()).then(|| extra.into()),
                    remedy_made: made,
                    local: false,
                    check: String::new(),
                    late: Default::default(),
                    ruling: None,
                    relied_on: Vec::new(),
                }
            })
            .collect();
        let start = v1::StartResult {
            state: if c["started"].as_bool().unwrap() {
                v1::STATE_STARTED
            } else {
                v1::STATE_NOT_STARTED
            }
            .into(),
            local: false,
            by: None,
        };
        assert_eq!(
            v1::settled(&start, &rows),
            c["settled"].as_bool().unwrap(),
            "{name}"
        );
    }
}

/// decline/vectors.json: VerifySent passes each valid pair with its digest
/// and refuses the others at their stage.
#[test]
fn sent_vectors() {
    // Each pair as written: one case gives a member twice, which a parsed
    // value would drop.
    #[derive(serde::Deserialize)]
    struct Raw {
        cases: Vec<RawCase>,
    }
    #[derive(serde::Deserialize)]
    struct RawCase {
        pair: Box<serde_json::value::RawValue>,
    }
    let p =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../testdata/deal-v1/decline/vectors.json");
    let raw: Raw = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap();
    for (k, c) in vectors("decline/vectors.json").into_iter().enumerate() {
        let name = c["name"].as_str().unwrap();
        let pair = raw.cases[k].pair.get().as_bytes().to_vec();
        let ctx = c.get("offer").map(|o| v1::OfferContext {
            offer: v1::decode(&serde_json::to_vec(o).unwrap()).unwrap(),
            signer: c["offer_signer"].as_str().unwrap().into(),
        });
        let got = v1::verify_sent(&pair, ctx.as_ref());
        if c["valid"].as_bool().unwrap() {
            let sent = got.unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(Some(sent.digest.as_str()), c["digest"].as_str(), "{name}");
        } else {
            let err = got.expect_err(name);
            assert_eq!(
                err.stage().map(|s| s.stage),
                c["stage"].as_str(),
                "{name}: {err}"
            );
        }
    }
}

/// authorization/approval-migration.json: CheckAuthorizations passes the
/// valid cases with each act's person signature, limit approvals and intent
/// signature, and refuses the others at the authorization stage.
#[test]
fn approval_migration_vectors() {
    for c in vectors("authorization/approval-migration.json") {
        let name = c["name"].as_str().unwrap();
        let read = |k: &str| -> Vec<v1::Record> {
            c[k].as_array()
                .cloned()
                .unwrap_or_default()
                .iter()
                .map(|r| {
                    v1::decode(&serde_json::to_vec(r).unwrap())
                        .unwrap_or_else(|e| panic!("{name}: {k}: {e}"))
                })
                .collect()
        };
        let got = v1::check_authorizations(&read("journal"), &read("acts"));
        if c["valid"].as_bool().unwrap() {
            let auths = got.unwrap_or_else(|e| panic!("{name}: {e}"));
            let person: Vec<bool> = auths.iter().map(|a| a.needs_person_signature).collect();
            let limits: Vec<usize> = auths.iter().map(|a| a.limit_approvals.len()).collect();
            let intent: Vec<bool> = auths.iter().map(|a| !a.intent.is_empty()).collect();
            assert_eq!(
                serde_json::to_value(person).unwrap(),
                c["person_signature"],
                "{name}: person_signature"
            );
            assert_eq!(
                serde_json::to_value(limits).unwrap(),
                c["limit_approvals"],
                "{name}: limit_approvals"
            );
            assert_eq!(
                serde_json::to_value(intent).unwrap(),
                c["intent_signature"],
                "{name}: intent_signature"
            );
        } else {
            let err = got.expect_err(name);
            assert_eq!(
                err.stage().map(|s| s.stage),
                Some(v1::STAGE_AUTHORIZATION),
                "{name}: {err}"
            );
        }
    }
}

#[test]
fn new_nonce_is_fresh_256_bit_hex() {
    let (a, b) = (v1::new_nonce(), v1::new_nonce());
    assert_eq!(a.len(), 64);
    assert!(a
        .bytes()
        .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase()));
    assert_ne!(a, b);
}

/// `pair` from a sent vector's Capsule, envelope and record gives a pair
/// `verify_sent` reads as the vector's, with Go's member order.
#[test]
fn pair_round_trips_through_verify_sent() {
    let c = &vectors("decline/vectors.json")[0];
    let mut record = c["pair"]["record"].as_object().unwrap().clone();
    let envelope = hex::decode(record.remove("signature").unwrap().as_str().unwrap()).unwrap();
    record.remove("key_id");
    let capsule = serde_json::to_vec(&Value::Object(record)).unwrap();
    let payload = evidencebook::canonical::jcs(&c["pair"]["agent_input"]).unwrap();
    let pair = v1::pair(&capsule, &envelope, &payload).unwrap();
    let got: Value = serde_json::from_slice(&pair).unwrap();
    assert_eq!(got, c["pair"], "the same pair, read back");
    assert!(
        std::str::from_utf8(&pair)
            .unwrap()
            .starts_with("{\"agent_input\":"),
        "Go's order"
    );
    let sent = v1::verify_sent(&pair, None).unwrap();
    assert_eq!(Some(sent.digest.as_str()), c["digest"].as_str());
}

/// payment/vectors.json: PaymentCheck over each input gives its result (as
/// Go marshals it) or refuses at its stage, and VerifyBook reads each book
/// pair as given or refuses it at its stage.
#[test]
fn payment_vectors() {
    // Pairs as written, so a reader sees Go's bytes.
    #[derive(serde::Deserialize)]
    struct Raw {
        payment: Vec<RawPayment>,
        book: Vec<RawBook>,
        pair: Vec<RawPair>,
    }
    #[derive(serde::Deserialize)]
    struct RawPair {
        name: String,
        header: String,
        capsule: String,
        envelope: String,
        payload: String,
        pair: String,
    }
    #[derive(serde::Deserialize)]
    struct RawPayment {
        name: String,
        input: RawInput,
        result: Option<Value>,
        #[serde(default)]
        refused: bool,
        #[serde(default)]
        stage: String,
    }
    #[derive(serde::Deserialize)]
    struct RawInput {
        offer: Box<serde_json::value::RawValue>,
        offer_digest: String,
        acceptor_key: String,
        row: String,
        legs: Vec<Box<serde_json::value::RawValue>>,
    }
    #[derive(serde::Deserialize)]
    struct RawBook {
        name: String,
        pair: Box<serde_json::value::RawValue>,
        valid: bool,
        #[serde(default)]
        stage: String,
        #[serde(default)]
        digest: String,
        #[serde(default)]
        record_id: String,
        #[serde(default)]
        key_id: String,
        #[serde(default)]
        log_id: String,
        #[serde(default)]
        seq: u64,
        #[serde(default)]
        prev: String,
    }
    let p =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../testdata/deal-v1/payment/vectors.json");
    let raw: Raw = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap();
    assert!(!raw.payment.is_empty() && !raw.book.is_empty() && raw.pair.len() > 1);
    for c in &raw.payment {
        let input = v1::PaymentInput {
            offer: c.input.offer.get().as_bytes().to_vec(),
            offer_digest: c.input.offer_digest.clone(),
            acceptor_key: c.input.acceptor_key.clone(),
            row: c.input.row.clone(),
            legs: c
                .input
                .legs
                .iter()
                .map(|l| l.get().as_bytes().to_vec())
                .collect(),
        };
        let got = v1::payment_check(&input);
        if c.refused {
            let err = got.expect_err(&c.name);
            assert_eq!(
                err.stage().map(|s| s.stage).unwrap_or(""),
                c.stage,
                "{}: {err}",
                c.name
            );
        } else {
            let res = got.unwrap_or_else(|e| panic!("{}: {e}", c.name));
            assert_eq!(
                Some(&serde_json::to_value(&res).unwrap()),
                c.result.as_ref(),
                "{}",
                c.name
            );
        }
    }
    for p in &raw.pair {
        let x = |s: &str| hex::decode(s).unwrap();
        let got = v1::book_pair(
            &x(&p.header),
            &x(&p.capsule),
            &x(&p.envelope),
            &x(&p.payload),
        )
        .unwrap_or_else(|e| panic!("{}: {e}", p.name));
        assert_eq!(hex::encode(&got), p.pair, "{}: Go's bytes", p.name);
        v1::verify_book(&got).unwrap_or_else(|e| panic!("{}: {e}", p.name));
        // The Capsule as the book sealed it, with an envelope of another
        // Capsule, and a Capsule that is not an object, are refused.
        let other = raw.pair.iter().find(|o| o.name != p.name).unwrap();
        assert!(v1::book_pair(
            &x(&p.header),
            &x(&p.capsule),
            &x(&other.envelope),
            &x(&p.payload)
        )
        .is_err());
        assert!(v1::book_pair(&x(&p.header), b"[]", &x(&p.envelope), &x(&p.payload)).is_err());
    }
    for b in &raw.book {
        let got = v1::verify_book(b.pair.get().as_bytes());
        if b.valid {
            let r = got.unwrap_or_else(|e| panic!("{}: {e}", b.name));
            assert_eq!(
                (
                    &r.digest,
                    &r.record_id,
                    &r.key_id,
                    &r.log_id,
                    r.seq,
                    &r.prev
                ),
                (
                    &b.digest,
                    &b.record_id,
                    &b.key_id,
                    &b.log_id,
                    b.seq,
                    &b.prev
                ),
                "{}",
                b.name
            );
        } else {
            let err = got.expect_err(&b.name);
            assert_eq!(
                err.stage().map(|s| s.stage).unwrap_or(""),
                b.stage,
                "{}: {err}",
                b.name
            );
        }
    }
}
