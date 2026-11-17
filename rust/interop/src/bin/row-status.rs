// SPDX-License-Identifier: Apache-2.0

//! The Rust side of the row status differential check
//! (test/interop/row-status/diff.py): one JSON input per line on stdin, one
//! line out for each, the result or {"error":true}.

use std::io::{BufRead, Write};

use evidencebook::row_status::{compute, parse_input};

fn main() {
    let stdin = std::io::stdin();
    let mut out = std::io::BufWriter::new(std::io::stdout());
    for line in stdin.lock().lines() {
        let line = line.expect("stdin is read");
        let result = parse_input(line.as_bytes()).and_then(|input| compute(&input));
        let text = match result {
            Ok(r) => serde_json::to_string(&r).expect("an outcome serializes"),
            Err(_) => "{\"error\":true}".to_string(),
        };
        writeln!(out, "{text}").expect("stdout is written");
    }
}
