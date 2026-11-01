// SPDX-License-Identifier: Apache-2.0

//! Run a Goose node's deal half (`goose-deal-half/1`, python/examples/goose)
//! through the checks a Mesh node on capsules-plugin v0.1.8 runs over a
//! counterparty's half, with the crates that release pins:
//!
//! - the record's Class 1 checks on its own bytes, which recompute its
//!   `capsule_id` (agent-action-capsule 0.4.1 `verify::verify`, as the
//!   plugin's `record_push_receive::record_id`);
//! - its producer envelope, made by the half's one key (capsule-emit 0.0.9
//!   `cose::verify_producer_envelope`, as `record_push_receive::signed_by`);
//! - its x-deal-v0 record, which must pass the profile's record checks and be
//!   the record the Capsule commits (evidencebook 0.0.11 `deal::check` and
//!   `deal::check_digest`, the crate and feature the plugin builds its own
//!   deal records with).
//!
//!     goose-mesh-check HALF...
//!
//! Prints one line per half and exits non-zero if any step of any half fails.

use std::process::ExitCode;

use serde_json::Value;

fn main() -> ExitCode {
    let paths: Vec<String> = std::env::args().skip(1).collect();
    if paths.is_empty() {
        eprintln!("usage: goose-mesh-check HALF...");
        return ExitCode::from(2);
    }
    let mut failed = false;
    for path in &paths {
        match check(path) {
            Ok(steps) => println!("ok   {path}: {steps} steps verify as a v0.1.8 Mesh node checks them"),
            Err(why) => {
                println!("FAIL {path}: {why}");
                failed = true;
            }
        }
    }
    if failed {
        ExitCode::FAILURE
    } else {
        ExitCode::SUCCESS
    }
}

fn check(path: &str) -> Result<usize, String> {
    let text = std::fs::read_to_string(path).map_err(|e| e.to_string())?;
    let half: Value = serde_json::from_str(&text).map_err(|e| e.to_string())?;
    if half.get("half").and_then(Value::as_str) != Some("goose-deal-half/1") {
        return Err("not a goose-deal-half/1".into());
    }
    let steps = half.get("steps").and_then(Value::as_array).ok_or("no steps")?;
    let key = steps
        .first()
        .and_then(|s| s.pointer("/capsule/key_id"))
        .and_then(Value::as_str)
        .ok_or("no key_id")?;
    for (i, step) in steps.iter().enumerate() {
        let n = i + 1;
        let capsule = step.get("capsule").ok_or(format!("step {n}: no capsule"))?;
        if !agent_action_capsule::verify::verify(capsule, None).ok {
            return Err(format!("step {n}: the Capsule fails Class 1"));
        }
        match capsule_emit::cose::verify_producer_envelope(capsule) {
            Ok(signer) if signer == key => {}
            Ok(signer) => return Err(format!("step {n}: signed by {signer}, not the half's key {key}")),
            Err(e) => return Err(format!("step {n}: producer envelope: {e}")),
        }
        let payload = serde_json::to_vec(step.get("payload").ok_or(format!("step {n}: no payload"))?)
            .map_err(|e| e.to_string())?;
        evidencebook::deal::check(&payload, &[]).map_err(|e| format!("step {n}: record: {e}"))?;
        let committed = capsule
            .pointer("/model_attestation/compute_attestation/agent_input_digest")
            .and_then(Value::as_str)
            .ok_or(format!("step {n}: no agent_input_digest"))?;
        evidencebook::deal::check_digest(&payload, committed)
            .map_err(|e| format!("step {n}: the Capsule does not commit its record: {e}"))?;
    }
    Ok(steps.len())
}
