//! The x-deal-v0 chain stage (PROFILE.md §6) against the reference checker's
//! verdicts (`dealprofile/testdata/chain-vectors.json`, written by
//! `check_profile.py --chain-vectors`, which CI regenerates and compares):
//! every prefix of each positive chain, each negative fixture's chain, and
//! single-member mutations of the positive chains, each with the refusal's
//! stage and text. Go's `CheckChain` reads the same file.

#![cfg(feature = "deal")]

use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};

use evidencebook::canonical::json_digest;
use evidencebook::deal::{check_chain_values, Error, STAGE_CHAIN};
use serde_json::Value;

fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../dealprofile")
}

fn read(path: &Path) -> Value {
    serde_json::from_slice(&fs::read(path).unwrap_or_else(|e| panic!("{}: {e}", path.display())))
        .unwrap()
}

fn fixtures(dir: &str) -> Vec<PathBuf> {
    let mut files: Vec<PathBuf> = fs::read_dir(root().join("x-deal-v0/fixtures").join(dir))
        .unwrap()
        .map(|e| e.unwrap().path())
        .filter(|p| p.extension().is_some_and(|x| x == "json"))
        .collect();
    files.sort();
    files
}

fn record(path: &Path) -> Value {
    read(path)["record"].clone()
}

fn chain_of(name: &str) -> Vec<Value> {
    let (dir, rest) = name.split_once('/').unwrap();
    if dir == "negative" {
        let f = read(&root().join("x-deal-v0/fixtures/negative").join(rest));
        let chain_dir = f["chain_dir"].as_str().unwrap_or("positive");
        let mut recs: Vec<Value> = f["chain_prefix"]
            .as_array()
            .into_iter()
            .flatten()
            .map(|n| {
                record(
                    &root()
                        .join("x-deal-v0/fixtures")
                        .join(chain_dir)
                        .join(n.as_str().unwrap()),
                )
            })
            .collect();
        recs.extend(f["chain_between"].as_array().cloned().unwrap_or_default());
        recs.push(f["record"].clone());
        return recs;
    }
    let n: usize = rest.parse().unwrap();
    fixtures(dir)[..n].iter().map(|p| record(p)).collect()
}

/// A mutation case's chain: the mutation applied to one record, and each
/// later record re-pointed at the digests the mutation changed (the checker's
/// `_relink`), so the rules after it read it.
fn mutated(c: &Value) -> Vec<Value> {
    let dir = c["chain"].as_str().unwrap();
    let mut docs = chain_of(&format!("{dir}/{}", fixtures(dir).len()));
    let k = c["index"].as_u64().unwrap() as usize;
    let old = json_digest(&docs[k]).unwrap();
    let path = c["path"].as_array().unwrap();
    let mut node = &mut docs[k];
    for step in &path[..path.len() - 1] {
        node = match step {
            Value::String(s) => node.get_mut(s.as_str()).unwrap(),
            n => node.get_mut(n.as_u64().unwrap() as usize).unwrap(),
        };
    }
    match (c["op"].as_str().unwrap(), path.last().unwrap()) {
        ("delete", Value::String(s)) => {
            node.as_object_mut().unwrap().shift_remove(s.as_str());
        }
        (_, Value::String(s)) => {
            *node.get_mut(s.as_str()).unwrap() = c["value"].clone();
        }
        (_, n) => {
            *node.get_mut(n.as_u64().unwrap() as usize).unwrap() = c["value"].clone();
        }
    }
    let mut mapping = HashMap::from([(old, json_digest(&docs[k]).unwrap())]);
    fn sub(v: &mut Value, m: &HashMap<String, String>) {
        match v {
            Value::String(s) => {
                if let Some(n) = m.get(s.as_str()) {
                    *s = n.clone();
                }
            }
            Value::Array(a) => a.iter_mut().for_each(|x| sub(x, m)),
            Value::Object(o) => o.iter_mut().for_each(|(_, x)| sub(x, m)),
            _ => {}
        }
    }
    for d in &mut docs[k + 1..] {
        let od = json_digest(d).unwrap();
        sub(d, &mapping);
        mapping.insert(od, json_digest(d).unwrap());
    }
    docs
}

#[test]
fn chain_vectors() {
    let v = read(&root().join("testdata/chain-vectors.json"));
    let cases = v["cases"].as_array().unwrap();
    assert!(!cases.is_empty());
    let mut refused = 0;
    for c in cases {
        let name = c["name"].as_str().unwrap();
        let recs = if c.get("chain").is_some() {
            mutated(c)
        } else {
            chain_of(name)
        };
        let got = check_chain_values(&recs);
        match c["stage"].as_str().unwrap() {
            "" => assert!(got.is_ok(), "{name}: {got:?}"),
            stage => {
                refused += 1;
                let Err(Error::Stage(se)) = got else {
                    panic!("{name}: not refused")
                };
                assert_eq!((se.stage, stage), (STAGE_CHAIN, stage), "{name}");
                assert_eq!(se.detail, c["error"].as_str().unwrap(), "{name}");
            }
        }
    }
    assert!(refused > 200, "the vectors hold the chain stage's refusals");
}

#[test]
fn unreadable_record() {
    let got = evidencebook::deal::check_chain(&[b"[]"]);
    let Err(Error::Stage(se)) = got else {
        panic!("not refused")
    };
    assert_eq!(
        se.to_string(),
        "[chain] seq position 1: a record is a JSON object"
    );
}

#[test]
fn edge_inputs() {
    assert_eq!(
        check_chain_values(&[]).unwrap_err().to_string(),
        "[chain] a record the chain rules cannot read: no records"
    );
    assert_eq!(
        check_chain_values(&[serde_json::json!([])])
            .unwrap_err()
            .to_string(),
        "[chain] seq position 1: a record is a JSON object"
    );
}
