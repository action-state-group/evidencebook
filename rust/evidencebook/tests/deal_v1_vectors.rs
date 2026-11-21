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

/// A payment result that does not begin with the offer's digest, or names a
/// record the books do not hold, is refused (Go's TestBuildReportPaymentLegs).
#[test]
fn report_refuses_payment_legs_not_held() {
    let c = vectors("report/vectors.json")
        .into_iter()
        .find(|c| c["name"] == "appendix-a-payee-leg-unstated")
        .expect("the case");
    let copy = v1::ReportCopy {
        audience: "keep".into(),
        nonce: c["copy"]["nonce"].as_str().unwrap().into(),
        referee: String::new(),
        as_of: c["copy"]["as_of"].as_str().unwrap().into(),
        text: None,
    };
    let over = c["books"]["Books"]["payments"]["paid_as_agreed"]["checked_over"].clone();
    let mut not_held = over.clone();
    not_held
        .as_array_mut()
        .unwrap()
        .push(Value::from("e".repeat(64)));
    for (name, list) in [
        ("empty", Value::Array(vec![])),
        (
            "not the offer",
            Value::Array(vec![Value::from("0".repeat(64))]),
        ),
        ("not held", not_held),
    ] {
        let mut books = c["books"].clone();
        books["Books"]["payments"]["paid_as_agreed"]["checked_over"] = list;
        let books: v1::ReportBooks = serde_json::from_value(books).unwrap();
        let err = v1::build_report(&books, &copy).expect_err(name);
        assert!(err.to_string().contains("payment check"), "{name}: {err}");
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
            if let Some(want) = c["error"].as_str() {
                assert_eq!(err.to_string(), want, "{name}: Go's text");
            }
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

// evidencebook#546: x-deal-v1 reads the x-deal-v0 platform pattern as Go's
// RE2 does: a no-break space or a vertical tab is not its \s, a tab is.
// Go (TestSchemaPatternWhiteSpaceIsRE2 in dealprofile/xdealv1) and the Python package's
// test_a_schema_white_space_class_is_re2s give the same.
#[test]
fn a_schema_white_space_class_is_re2s() {
    let record = |platform: &str| {
        serde_json::to_vec(&serde_json::json!({
            "x-deal-v1": {"profile": "x-deal-v1", "canonicalization": "jcs", "record_type": "sale_unpost",
                          "at": "2026-11-01T10:00:00Z"},
            "body": {"platform": platform},
        }))
        .unwrap()
    };
    for p in ["shop\u{a0}x.example", "a\u{b}b.example", "market.test"] {
        assert!(v1::check(&record(p)).is_ok(), "{p:?} passes, as in Go");
    }
    match v1::check(&record("a\tb.example")) {
        Err(evidencebook::deal::Error::Stage(e)) => assert_eq!(e.stage, "schema"),
        other => panic!("a tab is RE2 white space: {other:?}"),
    }
}

/// report-verify/vectors.json: VerifyReport accepts each valid report with
/// the signer Go gives, and refuses each other one at Go's stage with Go's
/// words (design §9, "Verifying a report").
#[test]
fn report_verify_vectors() {
    for c in vectors("report-verify/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let report = hex::decode(c["report"].as_str().unwrap()).unwrap();
        let first = hex::decode(c["first"].as_str().unwrap()).unwrap();
        let got = v1::verify_report(&report, &first);
        let want = &c["want"];
        if let Some(key) = want.get("key_id").and_then(Value::as_str) {
            let read = got.unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(read.key_id, key, "{name}");
            assert_eq!(read.report.profile, v1::PROFILE, "{name}");
            continue;
        }
        let err = got.expect_err(name);
        assert_eq!(
            err.stage().map(|s| s.stage),
            want["stage"].as_str(),
            "{name}: {err}"
        );
        let fragment = want.get("error").and_then(Value::as_str).unwrap_or("");
        assert!(
            err.to_string().contains(fragment),
            "{name}: {err} lacks {fragment:?}"
        );
    }
}

/// pair-names/vectors.json: a sent or book pair whose members are not
/// exactly its own is refused at Go's stage (evidencebook#559).
#[test]
fn pair_names_vectors() {
    for c in vectors("pair-names/vectors.json") {
        let name = c["name"].as_str().unwrap();
        let pair = hex::decode(c["pair"].as_str().unwrap()).unwrap();
        let got = if c["kind"] == "sent" {
            v1::verify_sent(&pair, None).map(|_| ())
        } else {
            v1::verify_book(&pair).map(|_| ())
        };
        if c["valid"].as_bool().unwrap() {
            got.unwrap_or_else(|e| panic!("{name}: {e}"));
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

/// check_authorizations reads each at as Go's time.Parse(time.RFC3339, at)
/// does (evidencebook#551): a one-digit hour, a comma fraction and an
/// offset are taken on records that were never checked, and a refusal is
/// Go's text.
#[test]
fn authorization_reads_at_as_go() {
    let c = vectors("authorization/approval-migration.json")
        .into_iter()
        .find(|c| c["valid"].as_bool() == Some(true))
        .unwrap();
    let read = |k: &str, at: &dyn Fn(usize, &str) -> String| -> Vec<v1::Record> {
        c[k].as_array()
            .unwrap()
            .iter()
            .enumerate()
            .map(|(i, r)| {
                let mut r = r.clone();
                let was = r["x-deal-v1"]["at"].as_str().unwrap().to_string();
                r["x-deal-v1"]["at"] = Value::String(at(i, &was));
                v1::decode(&serde_json::to_vec(&r).unwrap()).unwrap()
            })
            .collect()
    };
    let journal = read("journal", &|_, was| was.to_string());
    // The acts, which no record cites, in Go's lenient forms.
    for at in [
        "2026-12-01T1:00:00Z",
        "2026-12-01T10:00:00,5Z",
        "2026-12-01T10:00:00.5+01:00",
    ] {
        let acts = read("acts", &|_, _| at.to_string());
        v1::check_authorizations(&journal, &acts).unwrap_or_else(|e| panic!("Go takes {at}: {e}"));
    }
    let acts = read("acts", &|_, was| was.to_string());
    let bad = read("journal", &|i, was| {
        if i == 0 {
            "bad".into()
        } else {
            was.to_string()
        }
    });
    // Before Go's zero Time: at never decreases from it.
    let year0 = read("journal", &|i, was| {
        if i == 0 {
            "0000-12-31T23:59:59Z".into()
        } else {
            was.to_string()
        }
    });
    let err = v1::check_authorizations(&year0, &acts).expect_err("Go refuses a year-0 at");
    assert_eq!(
        err.to_string(),
        "[authorization] journal record 1: its at is earlier than the record before it: at never decreases"
    );
    // Go's %q escapes what strconv.IsPrint refuses: a no-break space.
    let nbsp = read("journal", &|i, was| {
        if i == 0 {
            "2026-11-01T10:01:00Z\u{a0}".into()
        } else {
            was.to_string()
        }
    });
    let err = v1::check_authorizations(&nbsp, &acts).expect_err("Go refuses it");
    assert_eq!(
        err.to_string(),
        "[authorization] journal record 1: its at \"2026-11-01T10:01:00Z\\u00a0\": parsing time \"2026-11-01T10:01:00Z\\xc2\\xa0\": extra text: \"\\xc2\\xa0\""
    );
    let err = v1::check_authorizations(&bad, &acts).expect_err("Go refuses it");
    assert_eq!(
        err.to_string(),
        "[authorization] journal record 1: its at \"bad\": parsing time \"bad\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"bad\" as \"2006\""
    );
}

/// Times compare with their nanoseconds: an approval sealed within the
/// act's own second is before the act only by its fraction.
#[test]
fn authorization_compares_nanoseconds() {
    let c = vectors("authorization/approval-migration.json")
        .into_iter()
        .find(|c| c["valid"].as_bool() == Some(true))
        .unwrap();
    let read = |k: &str, at: Option<&str>| -> Vec<v1::Record> {
        c[k].as_array()
            .unwrap()
            .iter()
            .enumerate()
            .map(|(i, r)| {
                let mut r = r.clone();
                if let Some(at) = at {
                    // The approval (journal 2) at .5; the act at the given at.
                    if (k == "journal" && i == 2) || k == "acts" {
                        r["x-deal-v1"]["at"] = Value::String(at.to_string());
                    }
                }
                v1::decode(&serde_json::to_vec(&r).unwrap()).unwrap()
            })
            .collect()
    };
    let journal = read("journal", Some("2026-11-01T10:03:00.5Z"));
    for (act_at, ok) in [
        ("2026-11-01T10:03:00.4Z", false),
        ("2026-11-01T10:03:00.5Z", true),
        ("2026-11-01T10:03:00.6Z", true),
    ] {
        let acts = read("acts", Some(act_at));
        let got = v1::check_authorizations(&journal, &acts);
        assert_eq!(got.is_ok(), ok, "{act_at}: {got:?}");
        if !ok {
            assert!(got
                .unwrap_err()
                .to_string()
                .contains("its approval was sealed after it"));
        }
    }
}
