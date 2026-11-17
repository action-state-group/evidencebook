// SPDX-License-Identifier: Apache-2.0

//! testdata/shelf/facts/vectors.json, which Go writes: every entry document,
//! entry id and opening, byte for byte.

use evidencebook::shelf_facts::{self as sf, FactInput, Opening};
use serde_json::Value;

fn vectors() -> Value {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../testdata/shelf/facts/vectors.json"
    );
    serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
}

fn salts(input: &Value) -> Vec<Vec<u8>> {
    input["salts"]
        .as_array()
        .unwrap()
        .iter()
        .map(|s| hex::decode(s.as_str().unwrap()).unwrap())
        .collect()
}

fn s(v: &Value) -> &str {
    v.as_str().unwrap()
}

fn opening(v: &Value) -> Opening {
    Opening {
        salt: s(&v["salt"]).to_string(),
        value: v["value"].clone(),
    }
}

#[test]
fn entries() {
    let v = vectors();
    for e in v["entries"].as_array().unwrap() {
        let name = s(&e["name"]);
        let input = &e["input"];
        let (doc, openings) = match s(&input["kind"]) {
            sf::FACT_KIND_BOOK => {
                let f = &input["fact"];
                let salt = salts(input);
                let link = &f["link"];
                let fact = sf::book_fact(
                    FactInput {
                        profile: s(&f["profile"]),
                        book_id: s(&f["book_id"]),
                        log_id: s(&f["log_id"]),
                        size: f["size"].as_u64().unwrap(),
                        checkpoint_sha256: s(&f["checkpoint_sha256"]),
                        prev_fact_size: f["prev_fact_size"].as_u64().unwrap(),
                        last_received: (!link["last_received"].is_null())
                            .then(|| link["last_received"].clone()),
                        checked_over: link["checked_over"]
                            .as_array()
                            .unwrap()
                            .iter()
                            .map(|x| s(x).to_string())
                            .collect(),
                        status: f.get("status").cloned(),
                    },
                    [&salt[0], &salt[1]],
                )
                .unwrap();
                (fact.doc, Some((fact.link_opening, fact.status_opening)))
            }
            sf::FACT_KIND_JOURNAL => {
                let j = &input["journal"];
                let st = &j["status"];
                let salt = salts(input);
                let fact = sf::journal_fact(
                    s(&j["log_id"]),
                    st["size"].as_u64().unwrap(),
                    s(&st["checkpoint_sha256"]),
                    st["prev_fact_size"].as_u64().unwrap(),
                    [&salt[0], &salt[1]],
                )
                .unwrap();
                (fact.doc, Some((fact.link_opening, fact.status_opening)))
            }
            sf::ENTRY_KIND_STAMP => {
                let st = &input["stamp"];
                (
                    sf::stamp_entry(st["stamp"].as_u64().unwrap(), st["facts"].as_u64().unwrap()),
                    None,
                )
            }
            sf::ENTRY_KIND_FORMAT_SWITCH => {
                let m = &input["marker"];
                let logs = m["logs"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|l| (s(&l["log_id"]).to_string(), l["size"].as_u64().unwrap()))
                    .collect();
                (
                    sf::format_switch(m["last_tick"].as_u64().unwrap(), logs),
                    None,
                )
            }
            k => panic!("{name}: unknown kind {k}"),
        };
        let (id, bytes) = sf::entry_id(&doc).unwrap();
        assert_eq!(
            serde_json::from_slice::<Value>(&bytes).unwrap(),
            e["doc"],
            "{name}: doc"
        );
        assert_eq!(hex::encode(id), s(&e["entry_id"]), "{name}: entry id");
        if let Some((lo, so)) = openings {
            assert_eq!(lo, opening(&e["link_opening"]), "{name}: link opening");
            assert_eq!(so, opening(&e["status_opening"]), "{name}: status opening");
            lo.verify(s(&doc["link_commitment"])).unwrap();
            so.verify(s(&doc["status_commitment"])).unwrap();
        }
    }
}

#[test]
fn audiences_and_bad_openings() {
    let v = vectors();
    for a in v["audiences"].as_array().unwrap() {
        let got = sf::openings_for(s(&a["kind"]), s(&a["audience"])).unwrap();
        assert_eq!(
            got,
            (a["link"].as_bool().unwrap(), a["status"].as_bool().unwrap()),
            "{a}"
        );
    }
    for b in v["bad_openings"].as_array().unwrap() {
        assert!(
            opening(&b["opening"]).verify(s(&b["commitment"])).is_err(),
            "{}",
            b["name"]
        );
    }
    assert!(sf::openings_for("stamp", "owner").is_err());
    assert!(sf::openings_for(sf::FACT_KIND_BOOK, "stranger").is_err());
}

#[test]
fn invalid_facts() {
    let v = vectors();
    for c in v["invalid_facts"].as_array().unwrap() {
        let name = s(&c["name"]);
        let input = &c["input"];
        let f = &input["fact"];
        let salt = salts(input);
        let link = &f["link"];
        let (Some(size), Some(prev)) = (f["size"].as_u64(), f["prev_fact_size"].as_u64()) else {
            continue; // a size that is no u64 cannot be expressed: refused by the type
        };
        let got = sf::book_fact(
            FactInput {
                profile: s(&f["profile"]),
                book_id: s(&f["book_id"]),
                log_id: s(&f["log_id"]),
                size,
                checkpoint_sha256: s(&f["checkpoint_sha256"]),
                prev_fact_size: prev,
                last_received: (!link["last_received"].is_null())
                    .then(|| link["last_received"].clone()),
                checked_over: link["checked_over"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|x| s(x).to_string())
                    .collect(),
                status: f.get("status").cloned(),
            },
            [&salt[0], &salt[1]],
        );
        assert!(got.is_err(), "{name} was accepted");
    }
}

#[test]
fn raw_opening_with_lone_surrogate_is_refused() {
    // serde_json refuses the lone surrogate itself, so such an opening never reaches verify.
    assert!(serde_json::from_slice::<Value>(br#"{"x":"\ud800"}"#).is_err());
}
