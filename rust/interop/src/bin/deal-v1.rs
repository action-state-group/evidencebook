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
        "check" => {
            let text = req["record"].as_str().unwrap_or_default();
            Ok(match v1::check(text.as_bytes()) {
                Ok(()) => "\"ok\"".into(),
                Err(e) => match e.stage() {
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
        other => Err(format!("unknown kind {other:?}")),
    }
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
