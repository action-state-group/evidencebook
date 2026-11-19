// SPDX-License-Identifier: Apache-2.0

//! The Rust side of the x-deal-v1 differential check
//! (test/interop/deal-v1/diff.py): one JSON request per line on stdin and one
//! line out for each, as the Go side (test/interop/deal-v1/main.go) writes it.

use std::io::{self, BufRead, BufWriter, Write};

use evidencebook::canonical::jcs;
use evidencebook::deal::v1;
use serde_json::Value;

const REFUSED: &str = "{\"error\":true}";

/// The refusal line, with the refusal's text when DEAL_V1_DEBUG is set.
fn refused(e: impl std::fmt::Display) -> String {
    if std::env::var_os("DEAL_V1_DEBUG").is_some() {
        return serde_json::json!({"error": e.to_string()}).to_string();
    }
    REFUSED.into()
}

fn answer(line: &str) -> Result<String, String> {
    let req: Value = serde_json::from_str(line).map_err(|e| e.to_string())?;
    let kind = req["kind"].as_str().unwrap_or_default();
    match kind {
        "check" | "sealed" => {
            let text = req["record"].as_str().unwrap_or_default();
            let got = match req.get("context") {
                Some(c) if kind == "sealed" => {
                    let ctx: v1::SealContext =
                        serde_json::from_value(c.clone()).map_err(|e| e.to_string())?;
                    v1::check_sealed(text.as_bytes(), ctx)
                }
                _ => v1::check(text.as_bytes()),
            };
            Ok(match got {
                Ok(()) => "\"ok\"".into(),
                Err(e) => match e.stage() {
                    Some(se) if se.stage == v1::STAGE_RECORD || se.stage == v1::STAGE_AGREEMENT => {
                        String::from_utf8(jcs(&serde_json::json!([se.stage, se.detail])).unwrap())
                            .unwrap()
                    }
                    Some(se) => format!("{:?}", se.stage),
                    None => refused(e),
                },
            })
        }
        "against" => {
            // As the Go side: a record that does not decode is reported as
            // its record check refuses it.
            let (rt, wt) = (
                req["record"].as_str().unwrap_or_default(),
                req["with"].as_str().unwrap_or_default(),
            );
            let (r, w) = match (v1::decode(rt.as_bytes()), v1::decode(wt.as_bytes())) {
                (Ok(r), Ok(w)) => (r, w),
                (r, _) => {
                    let text = if r.is_err() { rt } else { wt };
                    return Ok(
                        match v1::check(text.as_bytes())
                            .err()
                            .and_then(|e| e.stage().cloned())
                        {
                            Some(se) => format!("{:?}", se.stage),
                            None => REFUSED.into(),
                        },
                    );
                }
            };
            let got = match req["check"].as_str().unwrap_or_default() {
                "agreement" => {
                    v1::check_agreement(&r, &w, req["held"].as_str().unwrap_or_default())
                }
                "dispute" => v1::check_dispute(&r, &w),
                "ruling" => v1::check_ruling(&r, &w),
                other => return Err(format!("unknown check {other:?}")),
            };
            Ok(match got {
                Ok(()) => "\"ok\"".into(),
                Err(e) => match e.stage() {
                    Some(se) if se.stage == v1::STAGE_RECORD || se.stage == v1::STAGE_AGREEMENT => {
                        String::from_utf8(jcs(&serde_json::json!([se.stage, se.detail])).unwrap())
                            .unwrap()
                    }
                    Some(se) => format!("{:?}", se.stage),
                    None => refused(e),
                },
            })
        }
        "status" => {
            let b = match serde_json::from_value::<v1::StatusBooks>(req["books"].clone()) {
                Ok(b) => b,
                Err(e) => return Ok(refused(e)),
            };
            Ok(match v1::build_status(&b) {
                Ok(v) => {
                    String::from_utf8(jcs(&serde_json::to_value(v).unwrap()).unwrap()).unwrap()
                }
                Err(e) => refused(e),
            })
        }
        "report" => {
            let b = match serde_json::from_value::<v1::ReportBooks>(req["books"].clone()) {
                Ok(b) => b,
                Err(e) => return Ok(refused(e)),
            };
            let c = &req["copy"];
            let copy = v1::ReportCopy {
                audience: c["audience"].as_str().unwrap_or_default().into(),
                nonce: c["nonce"].as_str().unwrap_or_default().into(),
                referee: c["referee"].as_str().unwrap_or_default().into(),
                as_of: c["as_of"].as_str().unwrap_or_default().into(),
                text: None,
            };
            Ok(match v1::build_report(&b, &copy) {
                Ok(r) => String::from_utf8(v1::report_payload(&r).unwrap()).unwrap(),
                Err(e) => refused(e),
            })
        }
        "verify_report" => {
            verify_report(&req["payload"], req["first"].as_str().unwrap_or_default())
        }
        other => Err(format!("unknown kind {other:?}")),
    }
}

/// The record key of the seller in dealprofile/xdealv1's receive tests,
/// which signed the first record of testdata/deal-v1/report-verify.
fn seller_key() -> ed25519_dalek::SigningKey {
    use sha2::{Digest as _, Sha256};
    let seed: [u8; 32] = Sha256::digest(b"xdealv1 receive tests\x00seller record key").into();
    ed25519_dalek::SigningKey::from_bytes(&seed)
}

/// Seals `payload` as a report Capsule under the seller's key, as the Go
/// side does, and reads it against `first`.
fn verify_report(payload: &Value, first: &str) -> Result<String, String> {
    let payload = match jcs(payload) {
        Ok(p) => p,
        Err(e) => return Ok(refused(e)),
    };
    let key = seller_key();
    let producer = evidencebook::seal::Producer {
        operator: "seller",
        developer: "xdealv1-test",
        signer: &key,
    };
    let sealed = evidencebook::seal::seal_payload(
        evidencebook::deal::REPORT_ACTION_ID,
        "2026-11-21T12:00:00Z",
        &payload,
        &Default::default(),
        &producer,
    )
    .map_err(|e| e.to_string())?;
    let pair =
        v1::report_pair(&sealed.capsule, &sealed.envelope, &payload).map_err(|e| e.to_string())?;
    let first = hex::decode(first).map_err(|e| e.to_string())?;
    Ok(match v1::verify_report(&pair, &first) {
        Ok(r) => {
            let out = serde_json::json!({"key_id": r.key_id, "report": r.report});
            String::from_utf8(jcs(&out).map_err(|e| e.to_string())?).unwrap()
        }
        Err(e) => refused(e),
    })
}

fn main() {
    let stdin = io::stdin();
    let mut out = BufWriter::new(io::stdout().lock());
    for line in stdin.lock().lines() {
        let line = line.expect("stdin");
        match answer(&line) {
            Ok(a) => writeln!(out, "{a}").expect("stdout"),
            Err(e) => {
                eprintln!("{e}");
                std::process::exit(1);
            }
        }
    }
}
