//! "Asked of you": a book owner's log of the requests others made of it, and
//! what it answered. The responder's half of the double entry.
//!
//! I-D section: *Answering a Request* (the responder keeps its own account
//! of what it was asked; this log is that account, never evidence).
//!
//! One JSON object per line in [`RECEIVED_LOG_FILE`]: `{ts, path,
//! requester_id, subject_kind, status, reason}`, plus who proved they asked
//! for a signed request: `{request_digest, principal_key_id,
//! principal_node_id, principal_binding, audience, replay}`, `null` where
//! nothing was proven. The member names and their meaning are those of the
//! Python reference's `received_log.jsonl`.
//!
//! **Not the book's directory.** The log is not evidence, so it never lives
//! beside the records. Which directory it is, and whether this process may
//! write it at all (a lock it holds), is the caller's decision: [`ReceivedLog`]
//! writes into whatever directory it is handed, and only one that exists.
//!
//! **Best-effort.** An answer is decided before it is logged, and a logging
//! failure never changes it: [`ReceivedLog::append`] reports what happened
//! and the caller decides whether to say so.
//!
//! **Bounded.** The requester's id is its own word, so it is cut to
//! [`MAX_REQUESTER_ID_CHARS`] characters, and a line is never longer than the
//! few fields it names. The file stops growing at the log's byte cap
//! ([`MAX_RECEIVED_LOG_BYTES`] by default): past it a line is not written
//! and is counted ([`ReceivedLog::dropped`]).

use serde_json::{json, Value};
use std::io::Write;
use std::path::Path;
use std::sync::atomic::{AtomicU64, Ordering};

/// The log's file name in its directory.
pub const RECEIVED_LOG_FILE: &str = "received_log.jsonl";

/// The default cap: 4 MiB, some 20,000 lines.
pub const MAX_RECEIVED_LOG_BYTES: u64 = 4 * 1024 * 1024;

/// The longest self-declared requester id kept, in characters.
pub const MAX_REQUESTER_ID_CHARS: usize = 128;

/// One line: what was asked, by whom (as they said), and the outcome.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Entry<'a> {
    pub ts: &'a str,
    /// The route asked, in the caller's words.
    pub path: &'a str,
    /// The id the request states: its word, not who asked.
    pub requester_id: Option<&'a str>,
    pub subject_kind: Option<&'a str>,
    /// `answered`, `refused`, `received` or `issued`.
    pub status: &'a str,
    pub reason: Option<&'a str>,
    /// The digest of the request bytes as received.
    pub request_digest: Option<&'a str>,
    /// The key the request's signature verified under, if it did.
    pub principal_key_id: Option<&'a str>,
    /// The node that key is bound to, if the binding verified.
    pub principal_node_id: Option<&'a str>,
    /// `bound`, or `unbound` for a key that verified but nothing binds.
    pub principal_binding: Option<&'a str>,
    /// The audience a signed request named, as checked.
    pub audience: Option<&'a str>,
    /// `checked`, or `unchecked` for a nonce that could not be held for the
    /// replay check.
    pub replay: Option<&'a str>,
}

impl Entry<'_> {
    /// The line's JSON object, the requester's id cut to
    /// [`MAX_REQUESTER_ID_CHARS`].
    pub fn to_json(&self) -> Value {
        json!({
            "ts": self.ts,
            "path": self.path,
            "requester_id": self.requester_id.map(|id| id.chars().take(MAX_REQUESTER_ID_CHARS).collect::<String>()),
            "subject_kind": self.subject_kind,
            "status": self.status,
            "reason": self.reason,
            "request_digest": self.request_digest,
            "principal_key_id": self.principal_key_id,
            "principal_node_id": self.principal_node_id,
            "principal_binding": self.principal_binding,
            "audience": self.audience,
            "replay": self.replay,
        })
    }
}

/// What one [`ReceivedLog::append`] did.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Appended {
    /// The line was written.
    Written,
    /// No directory, or it does not exist: nothing to write to.
    Off,
    /// The log is at its cap; the line was dropped. `dropped` counts every
    /// line this log has dropped, this one included.
    Full { dropped: u64 },
}

/// A capped received log. One per process is the usual shape (a `static`),
/// so its drop count covers the run.
#[derive(Debug)]
pub struct ReceivedLog {
    max_bytes: u64,
    dropped: AtomicU64,
}

impl ReceivedLog {
    /// A log that stops growing at `max_bytes`.
    pub const fn new(max_bytes: u64) -> Self {
        Self {
            max_bytes,
            dropped: AtomicU64::new(0),
        }
    }

    /// Lines dropped so far because the log was full.
    pub fn dropped(&self) -> u64 {
        self.dropped.load(Ordering::Relaxed)
    }

    /// Append `entry` to `<dir>/`[`RECEIVED_LOG_FILE`] when `dir` is given and
    /// exists, unless the line would take the file past the cap.
    pub fn append(&self, dir: Option<&Path>, entry: &Entry<'_>) -> std::io::Result<Appended> {
        let Some(dir) = dir.filter(|d| d.is_dir()) else {
            return Ok(Appended::Off);
        };
        let line = format!("{}\n", entry.to_json());
        let mut file = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(dir.join(RECEIVED_LOG_FILE))?;
        if file.metadata()?.len() + line.len() as u64 > self.max_bytes {
            let dropped = self.dropped.fetch_add(1, Ordering::Relaxed) + 1;
            return Ok(Appended::Full { dropped });
        }
        file.write_all(line.as_bytes())?;
        Ok(Appended::Written)
    }
}

impl Default for ReceivedLog {
    fn default() -> Self {
        Self::new(MAX_RECEIVED_LOG_BYTES)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry() -> Entry<'static> {
        Entry {
            ts: "2026-09-29T00:00:00Z",
            path: "evidence-request",
            requester_id: Some("node-a"),
            subject_kind: Some("record"),
            status: "refused",
            reason: Some("no_such_subject"),
            request_digest: Some("ab"),
            principal_key_id: Some("cd"),
            principal_node_id: Some("node-a"),
            principal_binding: Some("bound"),
            audience: Some("node-r"),
            replay: Some("checked"),
        }
    }

    #[test]
    fn a_line_reads_back_as_written() {
        let dir = tempfile::tempdir().unwrap();
        let log = ReceivedLog::default();
        assert_eq!(
            log.append(Some(dir.path()), &entry()).unwrap(),
            Appended::Written
        );
        let line = std::fs::read_to_string(dir.path().join(RECEIVED_LOG_FILE)).unwrap();
        assert_eq!(
            line,
            "{\"ts\":\"2026-09-29T00:00:00Z\",\"path\":\"evidence-request\",\"requester_id\":\"node-a\",\"subject_kind\":\"record\",\"status\":\"refused\",\"reason\":\"no_such_subject\",\"request_digest\":\"ab\",\"principal_key_id\":\"cd\",\"principal_node_id\":\"node-a\",\"principal_binding\":\"bound\",\"audience\":\"node-r\",\"replay\":\"checked\"}\n",
            "member order and names are the reference's"
        );
    }

    #[test]
    fn nothing_is_written_without_the_directory() {
        let dir = tempfile::tempdir().unwrap();
        let missing = dir.path().join("received-log");
        let log = ReceivedLog::default();
        assert_eq!(log.append(Some(&missing), &entry()).unwrap(), Appended::Off);
        assert_eq!(log.append(None, &entry()).unwrap(), Appended::Off);
        assert!(!missing.exists());
    }

    #[test]
    fn the_log_stops_growing_at_its_cap_and_counts_what_it_drops() {
        let dir = tempfile::tempdir().unwrap();
        let line_len = entry().to_json().to_string().len() as u64 + 1;
        let log = ReceivedLog::new(3 * line_len);
        let outcomes: Vec<_> = (0..10)
            .map(|_| log.append(Some(dir.path()), &entry()).unwrap())
            .collect();
        assert_eq!(outcomes[..3], [Appended::Written; 3]);
        assert_eq!(outcomes[9], Appended::Full { dropped: 7 });
        let len = std::fs::metadata(dir.path().join(RECEIVED_LOG_FILE))
            .unwrap()
            .len();
        assert_eq!(len, 3 * line_len, "exactly the lines that fit");
        assert_eq!(log.dropped(), 7);
    }

    #[test]
    fn a_requester_id_is_cut_to_the_limit() {
        let long = "x".repeat(10_000);
        let e = Entry {
            requester_id: Some(&long),
            ..entry()
        };
        assert_eq!(
            e.to_json()["requester_id"].as_str().unwrap().len(),
            MAX_REQUESTER_ID_CHARS
        );
    }
}
