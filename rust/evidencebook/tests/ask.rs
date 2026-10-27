//! The requester's records: `evidence_request` sealed before sending and
//! `recorded_absence` when the window closes, against Go's `Request` and
//! `RecordAbsence` (`request.go`, `request_test.go`).

use evidencebook::answer::{Record, RecordSource};
use evidencebook::ask::{
    record_absence, request, AskError, AskedRequest, Book, Entry, SealError, Sealed,
};
use evidencebook::record::{EpistemicType, Link, LinkType};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};

/// A book that holds the entries it sealed, with a clock the test moves.
struct Mem {
    clock: String,
    records: Vec<(String, Entry, String)>,
}

impl Mem {
    fn at(clock: &str) -> Self {
        Mem {
            clock: clock.into(),
            records: Vec::new(),
        }
    }
}

impl RecordSource for Mem {
    fn record_by_digest(&self, digest: &str) -> Option<Record> {
        let i = self.records.iter().position(|(id, ..)| id == digest)?;
        self.record_at(i as u64)
    }
    fn record_at(&self, leaf_index: u64) -> Option<Record> {
        let (id, entry, _) = self.records.get(leaf_index as usize)?;
        Some(Record {
            leaf_index,
            digest: id.clone(),
            body: serde_json::to_vec(&entry.statement).unwrap(),
        })
    }
    fn correlation(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn citing(&self, _: &str) -> Vec<u64> {
        Vec::new()
    }
    fn header_of(&self, record: &Record) -> Option<Map<String, Value>> {
        let (_, entry, at) = self.records.get(record.leaf_index as usize)?;
        let links: Vec<Value> = entry
            .links
            .iter()
            .map(|l| json!({"type": l.link_type.as_str(), "target": l.target}))
            .collect();
        match json!({
            "record_type": entry.record_type,
            "committed_at": at,
            "links": links,
            "statement": entry.statement,
        }) {
            Value::Object(m) => Some(m),
            _ => None,
        }
    }
}

impl Book for Mem {
    fn len(&self) -> u64 {
        self.records.len() as u64
    }
    fn now(&self) -> String {
        self.clock.clone()
    }
    fn seal(&mut self, entry: &Entry) -> Result<Sealed, SealError> {
        let id = hex::encode(Sha256::digest(
            format!("{}#{}", self.records.len(), serde_json::to_string(&entry.statement).unwrap())
                .as_bytes(),
        ));
        self.records
            .push((id.clone(), entry.clone(), self.clock.clone()));
        Ok(Sealed {
            record_id: id,
            committed_at: self.clock.clone(),
        })
    }
    fn checkpoint(&mut self) -> Result<(), SealError> {
        Ok(())
    }
}

fn digest(label: &str) -> String {
    hex::encode(Sha256::digest(label.as_bytes()))
}

fn ask(nonce: &str) -> AskedRequest {
    AskedRequest {
        subject: json!({"record": digest("r")}),
        coverage: json!({"expected_pin": digest("pin")}),
        derivation: None,
        deadline: None,
        route: None,
        nonce: Some(nonce.into()),
    }
}

#[test]
fn asking_seals_the_request_before_it_is_sent() {
    let mut book = Mem::at("2026-10-27T00:00:00Z");
    let sent = request(&mut book, &ask("n-1"), "https://responder.example").unwrap();
    assert_eq!(
        String::from_utf8(sent.bytes.clone()).unwrap(),
        format!(
            r#"{{"coverage":{{"expected_pin":"{}"}},"nonce":"n-1","profile":"evidence-interop/0","subject":{{"record":"{}"}}}}"#,
            digest("pin"),
            digest("r")
        )
    );
    assert_eq!(sent.digest, hex::encode(Sha256::digest(&sent.bytes)));
    let (id, entry, _) = &book.records[0];
    assert_eq!(*id, sent.record_id);
    assert_eq!(entry.record_type, "evidence_request");
    assert_eq!(entry.epistemic_type, EpistemicType::ObservedEvent);
    assert!(entry.links.is_empty());
    // The responder is named in the statement, not as a counterparty.
    assert_eq!(entry.statement["responder_ref"], "https://responder.example");
    assert_eq!(
        serde_json::to_vec(&entry.statement["request"]).unwrap().len(),
        sent.bytes.len()
    );
}

#[test]
fn an_empty_nonce_is_filled_and_a_used_one_is_refused() {
    let mut book = Mem::at("2026-10-27T00:00:00Z");
    let mut open = ask("");
    open.nonce = None;
    let a = request(&mut book, &open, "").unwrap();
    let b = request(&mut book, &open, "").unwrap();
    assert_ne!(a.bytes, b.bytes, "each request gets a fresh nonce");
    request(&mut book, &ask("n-1"), "").unwrap();
    let err = request(&mut book, &ask("n-1"), "").unwrap_err();
    assert!(matches!(err, AskError::Invalid(m) if m.contains("nonce already used")));
    assert_eq!(book.records.len(), 3, "a refused request is not recorded");
}

#[test]
fn a_request_that_would_not_be_read_as_well_formed_is_not_sent() {
    let mut book = Mem::at("2026-10-27T00:00:00Z");
    let mut bad = ask("n-1");
    bad.coverage = json!({"expected_pin": "not-a-digest"});
    assert!(matches!(
        request(&mut book, &bad, "").unwrap_err(),
        AskError::Invalid(_)
    ));
    assert!(book.records.is_empty());
}

#[test]
fn an_absence_is_recorded_once_the_window_has_closed() {
    let mut book = Mem::at("2026-10-27T00:00:00Z");
    let mut asked = ask("n-1");
    asked.route = Some("https://responder.example/evidence".into());
    let sent = request(&mut book, &asked, "").unwrap();

    book.clock = "2026-10-27T00:00:30Z".into();
    assert!(matches!(
        record_absence(&mut book, &sent.record_id, "2026-10-27T00:00:30Z", "").unwrap_err(),
        AskError::Invalid(m) if m.contains("at least")
    ));
    assert!(matches!(
        record_absence(&mut book, &sent.record_id, "2026-10-27T00:05:00Z", "").unwrap_err(),
        AskError::Invalid(m) if m.contains("pending")
    ));

    book.clock = "2026-10-27T00:10:00Z".into();
    assert!(matches!(
        record_absence(&mut book, &sent.record_id, "2026-10-27T00:05:00Z", "https://other.example").unwrap_err(),
        AskError::Invalid(m) if m.contains("route")
    ));
    let sealed = record_absence(&mut book, &sent.record_id, "2026-10-27T00:05:00Z", "").unwrap();
    let (_, entry, _) = book.records.last().unwrap();
    assert_eq!(entry.record_type, "recorded_absence");
    assert_eq!(
        entry.links,
        vec![Link {
            link_type: LinkType::Cites,
            target: sent.record_id.clone()
        }]
    );
    assert_eq!(
        entry.statement,
        json!({
            "outcome": "recorded_absence",
            "request_digest": sent.digest,
            "route": "https://responder.example/evidence",
            "window": {"opened": "2026-10-27T00:00:00Z", "closed": "2026-10-27T00:05:00Z"},
            "recorded_at": "2026-10-27T00:10:00Z",
        })
    );
    assert_eq!(sealed.committed_at, "2026-10-27T00:10:00Z");

    // An outcome is never converted into another.
    let again = record_absence(&mut book, &sent.record_id, "2026-10-27T00:05:00Z", "").unwrap_err();
    assert!(matches!(again, AskError::Invalid(m) if m.contains("already recorded")));
}

#[test]
fn the_window_may_not_close_before_the_requests_own_deadline() {
    let mut book = Mem::at("2026-10-27T00:00:00Z");
    let mut asked = ask("n-1");
    asked.deadline = Some("2026-10-27T00:30:00Z".into());
    let sent = request(&mut book, &asked, "").unwrap();
    book.clock = "2026-10-27T01:00:00Z".into();
    assert!(matches!(
        record_absence(&mut book, &sent.record_id, "2026-10-27T00:10:00Z", "").unwrap_err(),
        AskError::Invalid(m) if m.contains("deadline")
    ));
    record_absence(&mut book, &sent.record_id, "2026-10-27T00:30:00Z", "").unwrap();
}

#[test]
fn only_a_recorded_request_can_be_given_an_absence() {
    let mut book = Mem::at("2026-10-27T00:10:00Z");
    assert!(matches!(
        record_absence(&mut book, &digest("none"), "2026-10-27T00:05:00Z", "").unwrap_err(),
        AskError::NotFound(_)
    ));
}
