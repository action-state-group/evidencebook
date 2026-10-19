//! The served summary (`served_summary/1`): what a book's owner served, per
//! group, over its checkpointed range. Never who asked.
//!
//! I-D section: *Answering a Request* (a derivation the responder answers;
//! -00 registers only `history_card/1`, so this one is answered through
//! [`crate::answer::Options::derive`]).
//!
//! A port of the Python reference's served-summary fold and its published
//! value (`build_served_summary`, `ServedSummary.to_value`). The fold is
//! here; what a group is, what a variant is, and the published labels are
//! the calling profile's ([`Labels`]), so this crate names no deployment's
//! concepts. The profile reads its facts from each record once
//! ([`ServedFacts`]), and never a requester-identity field.
//!
//! **Floor.** A group with fewer than [`FLOOR`] served records publishes
//! [`FLOOR_LABEL`] for every count and no latency figures, so a rarely served
//! group cannot be used to infer one interaction.
//!
//! One departure from the Python, stated: a latency that is not a finite
//! number (`NaN`, `inf`) is not counted ([`latency`]). The Python would fold
//! it into the percentiles and publish `nan`.

use crate::substrate::Checkpoint;
use serde_json::{json, Map, Value};
use std::collections::{BTreeMap, BTreeSet};

/// The derivation token the summary is answered under.
pub const DERIVATION: &str = "served_summary/1";
/// The fewest served records a group publishes exact figures for.
pub const FLOOR: u64 = 5;
/// What a group under the floor publishes for every count.
pub const FLOOR_LABEL: &str = "fewer than 5";

/// How one served record ended.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Outcome {
    Completed,
    Failed,
    /// Neither: counted in no outcome total.
    Unknown,
}

/// The fold's facts about one record the owner served.
#[derive(Clone, Debug, PartialEq)]
pub struct ServedFacts {
    /// The group it is counted under.
    pub group: String,
    /// The variant it was served as, listed per group.
    pub variant: String,
    pub outcome: Outcome,
    /// Milliseconds, finite ([`latency`]).
    pub latency_ms: Option<f64>,
    /// Whether the record states the digest the profile tracks.
    pub digest_present: bool,
}

/// What the summary needs from one log entry.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct LeafFacts {
    /// `Some` for a record the owner served (never padding).
    pub served: Option<ServedFacts>,
    /// For an adjudication record: the committed entries of the two halves
    /// it judges.
    pub adjudicated_halves: Option<(Option<String>, Option<String>)>,
}

/// The labels a profile publishes its summary under. Each is a fixed text of
/// the profile's published form.
#[derive(Clone, Copy, Debug)]
pub struct Labels<'a> {
    /// The value's `schema`.
    pub schema: &'a str,
    /// The digest of the fold's definition document.
    pub definition_digest: &'a str,
    /// The member of `derivation` holding the per-group figures.
    pub groups_member: &'a str,
    /// The member of a group holding its present/absent digest counts.
    pub digest_member: &'a str,
    /// The member of a group listing its variants.
    pub variants_member: &'a str,
    /// Why `refused` is always 0, on a group that served any record.
    pub refused_note: &'a str,
    /// Why no record states the digest, on a group where none does.
    pub digest_absent_note: &'a str,
    pub selection_note: &'a str,
    pub derivation_note: &'a str,
    pub coverage_note: &'a str,
    pub adjudications_note: &'a str,
    pub no_requester_identifiers: &'a str,
    pub not_a_score: &'a str,
}

/// The checkpoint a summary is bounded by.
pub struct Coverage<'a> {
    pub checkpoint: &'a Checkpoint,
    /// Records the checkpoint covers (its leaf count).
    pub covered: u64,
}

/// `float(raw)`: a number, a numeric string or a bool; anything else, and a
/// value that is not finite, is no latency.
pub fn latency(raw: &Value) -> Option<f64> {
    let v = match raw {
        Value::Number(n) => n.as_f64()?,
        Value::String(s) => s.trim().parse::<f64>().ok()?,
        Value::Bool(b) => f64::from(u8::from(*b)),
        _ => return None,
    };
    v.is_finite().then_some(v)
}

/// Linear-interpolation percentile over a sorted, non-empty list
/// (`served_summary._percentile`, the same operations in the same order).
fn percentile(sorted: &[f64], pct: f64) -> f64 {
    if sorted.len() == 1 {
        return sorted[0];
    }
    let k = (sorted.len() - 1) as f64 * pct;
    let (lo, hi) = (k.floor(), k.ceil());
    if lo == hi {
        return sorted[k as usize];
    }
    sorted[lo as usize] * (hi - k) + sorted[hi as usize] * (k - lo)
}

#[derive(Default)]
struct Bucket {
    completed: u64,
    failed: u64,
    latencies: Vec<f64>,
    digest_present: u64,
    digest_absent: u64,
    variants: BTreeSet<String>,
}

impl Bucket {
    fn to_value(&self, labels: &Labels<'_>) -> Value {
        let served = self.completed + self.failed;
        let below_floor = served < FLOOR;
        let count = |n: u64| {
            if below_floor {
                json!(FLOOR_LABEL)
            } else {
                json!(n)
            }
        };
        let mut latencies = self.latencies.clone();
        latencies.sort_by(f64::total_cmp);
        let figure = |f: &dyn Fn(&[f64]) -> f64| {
            if below_floor || latencies.is_empty() {
                Value::Null
            } else {
                json!(format!("{:.3}", f(&latencies)))
            }
        };
        let mut v = Map::new();
        v.insert("served".into(), count(served));
        v.insert("completed".into(), count(self.completed));
        v.insert("failed".into(), count(self.failed));
        v.insert("refused".into(), count(0));
        v.insert(
            "refused_note".into(),
            if served > 0 {
                json!(labels.refused_note)
            } else {
                Value::Null
            },
        );
        v.insert("latency_p50_ms".into(), figure(&|l| percentile(l, 0.50)));
        v.insert("latency_p95_ms".into(), figure(&|l| percentile(l, 0.95)));
        v.insert("latency_max_ms".into(), figure(&|l| l[l.len() - 1]));
        v.insert(
            labels.digest_member.into(),
            json!({
                "present": count(self.digest_present),
                "absent": count(self.digest_absent),
                "note": if self.digest_present == 0 { json!(labels.digest_absent_note) } else { Value::Null },
            }),
        );
        v.insert(
            labels.variants_member.into(),
            if below_floor {
                json!([])
            } else {
                json!(self.variants)
            },
        );
        v.insert("floor_applied".into(), json!(below_floor));
        Value::Object(v)
    }
}

/// The published summary (`ServedSummary.to_value()`), over `leaves` (every
/// log entry's facts, in log order) bounded by `coverage`. `is_own_record`
/// says whether a committed entry is one of the owner's own records (for the
/// adjudications-received count, which reads the whole log).
pub fn summary_value(
    leaves: &[&LeafFacts],
    coverage: &Coverage<'_>,
    is_own_record: &dyn Fn(&str) -> bool,
    labels: &Labels<'_>,
) -> Value {
    let cp = coverage.checkpoint;
    let covered = coverage.covered.min(leaves.len() as u64);
    let mut groups: BTreeMap<&str, Bucket> = BTreeMap::new();
    for facts in leaves.iter().take(covered as usize) {
        let Some(s) = &facts.served else { continue };
        let bucket = groups.entry(&s.group).or_default();
        match s.outcome {
            Outcome::Completed => bucket.completed += 1,
            Outcome::Failed => bucket.failed += 1,
            Outcome::Unknown => {}
        }
        if let Some(l) = s.latency_ms {
            bucket.latencies.push(l);
        }
        if s.digest_present {
            bucket.digest_present += 1;
        } else {
            bucket.digest_absent += 1;
        }
        bucket.variants.insert(s.variant.clone());
    }
    let groups: Map<String, Value> = groups
        .iter()
        .map(|(group, bucket)| (group.to_string(), bucket.to_value(labels)))
        .collect();
    let adjudications_received = leaves
        .iter()
        .filter_map(|f| f.adjudicated_halves.as_ref())
        .filter(|(a, b)| {
            a.as_deref().is_some_and(is_own_record) || b.as_deref().is_some_and(is_own_record)
        })
        .count();
    let witnesses: BTreeSet<&str> = cp.witnesses.iter().map(|w| w.ts_url.as_str()).collect();
    let mut derivation = Map::new();
    derivation.insert("kind".into(), json!("served_summary_fold"));
    derivation.insert("definition_digest".into(), json!(labels.definition_digest));
    derivation.insert(labels.groups_member.into(), Value::Object(groups));
    derivation.insert("note".into(), json!(labels.derivation_note));
    json!({
        "schema": labels.schema,
        "node_id": format!("node:{}", cp.key_id.get(..16).unwrap_or(&cp.key_id)),
        "selection": {
            "from_entry": u64::from(covered > 0),
            "to_entry": covered,
            "covered_entries": covered,
            "note": labels.selection_note,
        },
        "derivation": derivation,
        "coverage": {
            "checkpoint_root": cp.root,
            "mmr_size": cp.mmr_size,
            "log_id": cp.log_id,
            "timestamp": cp.timestamp,
            "witnesses": witnesses,
            "witnessed": !witnesses.is_empty(),
            "note": labels.coverage_note,
        },
        "adjudications_received": {
            "value": adjudications_received,
            "source": "self_held",
            "note": labels.adjudications_note,
        },
        "no_requester_identifiers": labels.no_requester_identifiers,
        "not_a_score": labels.not_a_score,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::substrate::{WitnessEntry, CHECKPOINT_KIND};

    const LABELS: Labels<'static> = Labels {
        schema: "s/1",
        definition_digest: "dd",
        groups_member: "by_group",
        digest_member: "digest",
        variants_member: "variants",
        refused_note: "rn",
        digest_absent_note: "dn",
        selection_note: "sn",
        derivation_note: "vn",
        coverage_note: "cn",
        adjudications_note: "an",
        no_requester_identifiers: "nri",
        not_a_score: "nas",
    };

    fn checkpoint() -> Checkpoint {
        Checkpoint {
            v: 1,
            kind: CHECKPOINT_KIND.into(),
            log_id: "log".into(),
            mmr_size: 1,
            root: "ab".into(),
            prev_size: 0,
            prev_root: String::new(),
            key_id: "0123456789abcdef0123".into(),
            timestamp: "2026-09-29T00:00:00Z".into(),
            signature: String::new(),
            witnesses: Vec::new(),
        }
    }

    fn served(group: &str, outcome: Outcome, latency_ms: Option<f64>) -> LeafFacts {
        LeafFacts {
            served: Some(ServedFacts {
                group: group.into(),
                variant: "q4".into(),
                outcome,
                latency_ms,
                digest_present: false,
            }),
            adjudicated_halves: None,
        }
    }

    fn summary(facts: &[LeafFacts], covered: u64, cp: &Checkpoint) -> Value {
        let refs: Vec<&LeafFacts> = facts.iter().collect();
        summary_value(
            &refs,
            &Coverage {
                checkpoint: cp,
                covered,
            },
            &|id| id == "mine",
            &LABELS,
        )
    }

    #[test]
    fn percentiles_interpolate_like_the_python() {
        let v = [1.0, 2.0, 3.0, 4.0];
        assert_eq!(percentile(&v, 0.5), 2.5);
        assert!((percentile(&v, 0.95) - 3.85).abs() < 1e-12);
        assert_eq!(percentile(&[7.0], 0.95), 7.0);
    }

    #[test]
    fn a_latency_is_a_finite_number_numeric_text_or_bool() {
        assert_eq!(latency(&json!(10.5)), Some(10.5));
        assert_eq!(latency(&json!(" 3 ")), Some(3.0));
        assert_eq!(latency(&json!(true)), Some(1.0));
        assert_eq!(latency(&json!("inf")), None);
        assert_eq!(latency(&json!("NaN")), None);
        assert_eq!(latency(&json!([1])), None);
        assert_eq!(latency(&Value::Null), None);
    }

    #[test]
    fn a_group_below_the_floor_publishes_no_exact_counts_or_latency() {
        let cp = checkpoint();
        let facts: Vec<_> = (0..3)
            .map(|_| served("g", Outcome::Completed, Some(10.5)))
            .collect();
        let v = summary(&facts, 3, &cp);
        let g = &v["derivation"]["by_group"]["g"];
        assert_eq!(g["served"], json!(FLOOR_LABEL));
        assert_eq!(g["digest"]["present"], json!(FLOOR_LABEL));
        assert_eq!(g["latency_p50_ms"], Value::Null);
        assert_eq!(g["variants"], json!([]));
        assert_eq!(g["floor_applied"], json!(true));
        assert_eq!(v["node_id"], json!("node:0123456789abcdef"));
        assert_eq!(v["schema"], json!("s/1"));
    }

    #[test]
    fn only_the_covered_range_is_folded() {
        let mut cp = checkpoint();
        cp.witnesses.push(WitnessEntry {
            ts_url: "https://w.example".into(),
            entry_hash: String::new(),
            receipt_b64: String::new(),
            leaf_index: 0,
            tree_size: 1,
            is_stub: false,
        });
        let mut facts: Vec<_> = (0..7)
            .map(|i| {
                served(
                    "g",
                    if i == 0 {
                        Outcome::Failed
                    } else {
                        Outcome::Completed
                    },
                    Some(i as f64),
                )
            })
            .collect();
        facts.push(served("g", Outcome::Unknown, None));
        let v = summary(&facts, 6, &cp);
        let g = &v["derivation"]["by_group"]["g"];
        assert_eq!(g["served"], json!(6));
        assert_eq!(g["failed"], json!(1));
        assert_eq!(g["refused"], json!(0));
        assert_eq!(g["refused_note"], json!("rn"));
        assert_eq!(g["latency_max_ms"], json!("5.000"));
        assert_eq!(g["latency_p50_ms"], json!("2.500"));
        assert_eq!(
            g["digest"],
            json!({"present": 0, "absent": 6, "note": "dn"})
        );
        assert_eq!(g["variants"], json!(["q4"]));
        assert_eq!(v["selection"]["to_entry"], json!(6));
        assert_eq!(v["coverage"]["witnessed"], json!(true));
        assert_eq!(v["coverage"]["witnesses"], json!(["https://w.example"]));
    }

    #[test]
    fn adjudications_count_verdicts_on_own_records_across_the_whole_log() {
        let cp = checkpoint();
        let verdict = |a: Option<&str>, b: Option<&str>| LeafFacts {
            served: None,
            adjudicated_halves: Some((a.map(str::to_string), b.map(str::to_string))),
        };
        let facts = vec![
            verdict(Some("mine"), None),
            verdict(Some("theirs"), Some("mine")),
            verdict(Some("theirs"), None),
        ];
        let v = summary(&facts, 0, &cp);
        assert_eq!(v["adjudications_received"]["value"], json!(2));
        assert_eq!(v["selection"]["from_entry"], json!(0));
        assert_eq!(v["derivation"]["by_group"], json!({}));
    }
}
