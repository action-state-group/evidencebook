// SPDX-License-Identifier: Apache-2.0

//! The status of one term row of a deal, computed from the two sides'
//! statements on it and the dates (evidencebook#461, acceptance 6). No record
//! holds a status and nobody declares it: whoever holds the inputs computes
//! it, as of any time, and gets the same answer. This is the Go package
//! `rowstatus`, result for result; `testdata/row-status/vectors.json` holds
//! the shared vectors, and the Go package comment states the rule.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

/// Inputs the rule cannot be applied to.
#[derive(Debug, thiserror::Error)]
#[error("row status: invalid input: {0}")]
pub struct Invalid(String);

fn invalid(m: impl Into<String>) -> Invalid {
    Invalid(m.into())
}

/// A row date: a fixed time, or a number of seconds after a named record's
/// time.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Date {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub at: Option<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub after: String,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub seconds: i64,
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

fn is_false(v: &bool) -> bool {
    !*v
}

/// A record a date hangs on: its time once it has one, and the latest time
/// by which it must have one.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Named {
    #[serde(default)]
    pub time: Option<String>,
    #[serde(default)]
    pub latest: String,
}

/// One term row.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Row {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub owed_by: String,
    #[serde(default)]
    pub due: Date,
    #[serde(default)]
    pub window_end: Date,
    #[serde(default, skip_serializing_if = "is_false")]
    pub recomputed: bool,
}

/// What one side said about the row, in its book's order.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Statement {
    #[serde(default)]
    pub side: String,
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub time: Option<String>,
}

/// A dispute on the row; `seen_in_time` says the claimant's book named the
/// statement it did not contest before the contest window ended.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Dispute {
    #[serde(default)]
    pub side: String,
    #[serde(default)]
    pub time: Option<String>,
    #[serde(default)]
    pub seen_in_time: bool,
}

/// A claimant's withdrawal of its disputes on the row.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Withdrawal {
    #[serde(default)]
    pub side: String,
    #[serde(default)]
    pub time: Option<String>,
}

/// A ruling by the adjudicator the agreement names, in its case book's
/// order.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Ruling {
    #[serde(default)]
    pub r#final: bool,
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub time: Option<String>,
    #[serde(default)]
    pub remedy_due: Option<String>,
}

/// A record that carries out a ruling's remedy.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Remedy {
    #[serde(default)]
    pub time: Option<String>,
}

/// Everything the status of one row is computed from.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Input {
    #[serde(default)]
    pub at: String,
    #[serde(default)]
    pub parties: Vec<String>,
    pub row: Row,
    #[serde(default, deserialize_with = "unique_map")]
    pub named: BTreeMap<String, Named>,
    #[serde(default)]
    pub no_concession: bool,
    #[serde(default)]
    pub statements: Vec<Statement>,
    #[serde(default)]
    pub check: String,
    #[serde(default)]
    pub disputes: Vec<Dispute>,
    #[serde(default)]
    pub withdrawals: Vec<Withdrawal>,
    #[serde(default)]
    pub rulings: Vec<Ruling>,
    #[serde(default)]
    pub conflicting_final_rulings: bool,
    #[serde(default)]
    pub remedies: Vec<Remedy>,
}

/// A map whose keys are each named once: serde would keep the last of a
/// repeated key, where Go and Python refuse it.
fn unique_map<'de, D>(d: D) -> Result<BTreeMap<String, Named>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    struct V;
    impl<'de> serde::de::Visitor<'de> for V {
        type Value = BTreeMap<String, Named>;
        fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
            f.write_str("an object of named records")
        }
        fn visit_map<A: serde::de::MapAccess<'de>>(
            self,
            mut m: A,
        ) -> Result<Self::Value, A::Error> {
            let mut out = BTreeMap::new();
            while let Some((k, v)) = m.next_entry::<String, Named>()? {
                if out.insert(k.clone(), v).is_some() {
                    return Err(serde::de::Error::custom(format!("member {k:?} repeated")));
                }
            }
            Ok(out)
        }
    }
    d.deserialize_map(V)
}

/// A row's status and the value stated, read together.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Outcome {
    pub status: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub value: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub remedy: String,
    pub local: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub due: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub window_end: String,
}

const MET: &str = "met";
const NOT_MET: &str = "not_met";
const CANNOT_TELL: &str = "cannot_tell";
const MAX_OFFSET: i64 = 100 * 366 * 24 * 3600;
/// 9999-12-31T23:59:59Z.
const MAX_TIME: i64 = 253_402_300_799;

/// Reads one input as the vectors write it, refusing unknown and repeated
/// members (member names match exactly).
pub fn parse_input(bytes: &[u8]) -> Result<Input, Invalid> {
    serde_json::from_slice(bytes).map_err(|e| invalid(e.to_string()))
}

struct Stmt {
    /// The statement's index in the input.
    idx: usize,
    side: usize,
    value: String,
    t: Option<i64>,
    floor: Option<i64>,
}

struct Calc<'a> {
    input: &'a Input,
    at: i64,
    due: Option<i64>,
    end: Option<i64>,
    unset: bool,
    reached: bool,
    local: bool,
    stmts: Vec<Stmt>,
    /// Each held dispute: its side, time, seen_in_time and input index.
    disputes: Vec<(usize, Option<i64>, bool, usize)>,
    rulings: Vec<(&'a Ruling, Option<i64>)>,
    remedies: Vec<Option<i64>>,
    withdrawn: [bool; 2],
    w_timed: [bool; 2],
}

/// Applies the rule to one row as of `input.at`.
pub fn compute(input: &Input) -> Result<Outcome, Invalid> {
    let mut c = Calc::new(input)?;
    Ok(c.compute())
}

/// What the rule set aside as late on a row as of `at` (evidencebook#461,
/// design §9, Q9): indices into the input's statements and disputes, in
/// input order (Go `rowstatus.Late`). It changes no [`Outcome`].
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Late {
    /// The statements held as of `at` that are after the end of the
    /// contest window.
    #[serde(rename = "late_statements")]
    pub statements: Vec<usize>,
    /// The held disputes rule 2 does not count because they are late.
    #[serde(rename = "late_disputes")]
    pub disputes: Vec<usize>,
}

/// The late statements and late disputes of one row as of `input.at`
/// (Go `ComputeLate`). Both are empty while the row is not reached or a
/// date is unset.
pub fn compute_late(input: &Input) -> Result<Late, Invalid> {
    let mut c = Calc::new(input)?;
    let mut out = Late::default();
    if !c.reached || c.unset {
        return Ok(out);
    }
    for s in &c.stmts {
        if c.late(s) {
            out.statements.push(s.idx);
        }
    }
    let in_time = c.values();
    let failed = c.input.check == "fail";
    for &(_, t, seen, idx) in &c.disputes.clone() {
        if c.dispute_late(t, seen, &in_time, failed) {
            out.disputes.push(idx);
        }
    }
    Ok(out)
}

impl<'a> Calc<'a> {
    fn new(input: &'a Input) -> Result<Self, Invalid> {
        let at = parse_time(&input.at)?;
        let p = &input.parties;
        if p.len() != 2 || p[0].is_empty() || p[1].is_empty() || p[0] == p[1] {
            return Err(invalid("two distinct parties are required"));
        }
        let mut c = Calc {
            input,
            at,
            due: None,
            end: None,
            unset: false,
            reached: true,
            local: false,
            stmts: Vec::new(),
            disputes: Vec::new(),
            rulings: Vec::new(),
            remedies: Vec::new(),
            withdrawn: [false; 2],
            w_timed: [false; 2],
        };
        c.side(&input.row.owed_by)?;
        if !matches!(input.check.as_str(), "" | "pass" | "fail") {
            return Err(invalid("check is pass, fail or empty"));
        }
        if !input.check.is_empty() && !input.row.recomputed {
            return Err(invalid(
                "a check result on a row its terms do not mark recomputed",
            ));
        }
        c.due = c.date(&input.row.due)?;
        c.end = c.date(&input.row.window_end)?;
        if let (Some(d), Some(e)) = (c.due, c.end) {
            if e < d {
                return Err(invalid("the contest window ends before the due date"));
            }
        }
        let mut floors: [Option<i64>; 2] = [None, None];
        for (idx, s) in input.statements.iter().enumerate() {
            let i = c.side(&s.side)?;
            if !matches!(s.value.as_str(), MET | NOT_MET | CANNOT_TELL) {
                return Err(invalid(format!("statement value {:?}", s.value)));
            }
            let t = s.time.as_deref().map(parse_time).transpose()?;
            let floor = floors[i];
            if let Some(t) = t {
                if floors[i].is_none_or(|f| t > f) {
                    floors[i] = Some(t);
                }
            }
            let after_t = match t {
                Some(t) => t > c.at,
                None => floor.is_some_and(|f| f > c.at),
            };
            if after_t {
                continue;
            }
            c.stmts.push(Stmt {
                idx,
                side: i,
                value: s.value.clone(),
                t,
                floor,
            });
        }
        for w in &input.withdrawals {
            let i = c.side(&w.side)?;
            if let Some(t) = c.held(&w.time)? {
                c.withdrawn[i] = true;
                c.w_timed[i] |= t.is_some();
            }
        }
        for (idx, d) in input.disputes.iter().enumerate() {
            let i = c.side(&d.side)?;
            if let Some(t) = c.held(&d.time)? {
                c.disputes.push((i, t, d.seen_in_time, idx));
            }
        }
        for r in &input.rulings {
            if r.value != MET && r.value != NOT_MET {
                return Err(invalid("a ruling's value is met or not_met"));
            }
            if let Some(d) = &r.remedy_due {
                parse_time(d)?;
            }
            if let Some(t) = c.held(&r.time)? {
                c.rulings.push((r, t));
            }
        }
        for r in &input.remedies {
            if let Some(t) = c.held(&r.time)? {
                c.remedies.push(t);
            }
        }
        Ok(c)
    }

    fn side(&self, s: &str) -> Result<usize, Invalid> {
        let p = &self.input.parties;
        if s == p[0] {
            Ok(0)
        } else if s == p[1] {
            Ok(1)
        } else {
            Err(invalid(format!("{s:?} is not a party")))
        }
    }

    /// `None` when the record does not exist yet as of T; `Some(None)` for a
    /// record with no time.
    #[allow(clippy::option_option)]
    fn held(&self, s: &Option<String>) -> Result<Option<Option<i64>>, Invalid> {
        match s {
            None => Ok(Some(None)),
            Some(s) => {
                let t = parse_time(s)?;
                Ok((t <= self.at).then_some(Some(t)))
            }
        }
    }

    fn date(&mut self, d: &Date) -> Result<Option<i64>, Invalid> {
        match (&d.at, d.after.is_empty(), d.seconds) {
            (Some(at), true, 0) => return parse_time(at).map(Some),
            (None, false, _) => {}
            _ => return Err(invalid("a date is either at or after")),
        }
        if d.seconds < 0 || d.seconds > MAX_OFFSET {
            return Err(invalid("a date's offset is out of range"));
        }
        let n = self.input.named.get(&d.after).ok_or_else(|| {
            invalid(format!(
                "the date hangs on {:?}, which is not named",
                d.after
            ))
        })?;
        let latest = parse_time(&n.latest)?;
        let mut t = None;
        if let Some(s) = &n.time {
            let v = parse_time(s)?;
            if v <= latest && v <= self.at {
                t = Some(v);
            }
        }
        match t {
            None => {
                if self.at > latest {
                    self.reached = false;
                }
                self.unset = true;
                Ok(None)
            }
            Some(t) if t + d.seconds > MAX_TIME => Err(invalid("a date past year 9999")),
            Some(t) => Ok(Some(t + d.seconds)),
        }
    }

    fn use_time(&mut self, t: Option<i64>) {
        if t.is_none() {
            self.local = true;
        }
    }

    fn end(&self) -> i64 {
        self.end.expect("dates are set")
    }

    fn late(&self, s: &Stmt) -> bool {
        match s.t {
            Some(t) => t > self.end(),
            None => s.floor.is_some_and(|f| f > self.end()),
        }
    }

    fn values(&mut self) -> [String; 2] {
        let mut idx: [Option<usize>; 2] = [None, None];
        for (i, s) in self.stmts.iter().enumerate() {
            if !self.late(s) {
                idx[s.side] = Some(i);
            }
        }
        let mut v = [String::new(), String::new()];
        for side in 0..2 {
            if let Some(i) = idx[side] {
                let t = self.stmts[i].t;
                self.use_time(t);
                if self.stmts[i].value != CANNOT_TELL {
                    v[side] = self.stmts[i].value.clone();
                }
            }
        }
        v
    }

    fn concede(&self, mut v: [String; 2]) -> [String; 2] {
        if self.input.no_concession || self.at <= self.end() {
            return v;
        }
        let mut first: [Option<usize>; 2] = [None, None];
        for (i, s) in self.stmts.iter().enumerate() {
            let other = &v[1 - s.side];
            if first[s.side].is_some() || s.t.is_none() || !self.late(s) || other.is_empty() {
                continue;
            }
            if &s.value == other && v[s.side] != *other {
                first[s.side] = Some(i);
            }
        }
        let pick = match (first[0], first[1]) {
            (None, None) => return v,
            (Some(a), Some(b)) => {
                let (ta, tb) = (self.stmts[a].t, self.stmts[b].t);
                if ta == tb {
                    return v;
                }
                if tb < ta {
                    b
                } else {
                    a
                }
            }
            (Some(a), None) => a,
            (None, Some(b)) => b,
        };
        let s = &self.stmts[pick];
        v[s.side] = s.value.clone();
        v
    }

    fn compute(&mut self) -> Outcome {
        let due = self.due.map(format_time).unwrap_or_default();
        let window_end = self.end.map(format_time).unwrap_or_default();
        let mut out = Outcome {
            status: String::new(),
            value: String::new(),
            remedy: String::new(),
            local: false,
            due,
            window_end,
        };
        let finish = |mut out: Outcome, status: &str, value: &str, local: bool| {
            out.status = status.into();
            out.value = value.into();
            out.local = local;
            out
        };
        if !self.reached {
            return finish(out, "not_reached", "", self.local);
        }
        if !self.input.conflicting_final_rulings {
            let rulings = self.rulings.clone();
            if let Some((r, t)) = rulings.into_iter().find(|(r, _)| r.r#final) {
                self.use_time(t);
                out.remedy = self.remedy(r).into();
                return finish(out, "ruled", &r.value, self.local);
            }
        }
        if self.unset {
            return finish(out, "not_due", "", self.local);
        }
        let in_time = self.values();
        let stated = self.concede(in_time.clone());
        let failed = self.input.check == "fail";
        let after = self.at > self.end();
        if self.input.conflicting_final_rulings {
            return finish(out, "in_dispute", "", self.local);
        }
        let disputes = self.disputes.clone();
        // A withdrawal with no time is relied on only when no other dispute
        // counts: it is what keeps the row out of dispute.
        let mut rests_on_untimed_withdrawal = false;
        for (side, t, seen, _) in disputes {
            if self.dispute_late(t, seen, &in_time, failed) {
                continue;
            }
            if self.withdrawn[side] {
                rests_on_untimed_withdrawal |= !self.w_timed[side];
                continue;
            }
            self.use_time(t);
            return finish(out, "in_dispute", "", self.local);
        }
        if rests_on_untimed_withdrawal {
            self.local = true;
        }
        let (a, b) = (&stated[0], &stated[1]);
        let local = self.local;
        if failed || (!a.is_empty() && !b.is_empty() && a != b) {
            finish(out, "in_disagreement", "", local)
        } else if !a.is_empty() && !b.is_empty() {
            finish(
                out,
                if after { "agreed" } else { "agreed_so_far" },
                a,
                local,
            )
        } else if !a.is_empty() || !b.is_empty() {
            let v = format!("{a}{b}");
            finish(
                out,
                if after { "uncontested" } else { "waiting" },
                &v,
                local,
            )
        } else if self.at > self.due.expect("dates are set") {
            finish(out, "no_statement", "", local)
        } else {
            finish(out, "not_due", "", local)
        }
    }

    fn dispute_late(
        &self,
        t: Option<i64>,
        seen: bool,
        in_time: &[String; 2],
        failed: bool,
    ) -> bool {
        let Some(t) = t else { return false };
        if t <= self.end() || failed || !seen {
            return false;
        }
        let (a, b) = (&in_time[0], &in_time[1]);
        let agreed = !a.is_empty() && !b.is_empty() && a == b;
        let uncontested = a.is_empty() != b.is_empty();
        agreed || uncontested
    }

    fn remedy(&mut self, r: &Ruling) -> &'static str {
        let Some(due) = &r.remedy_due else {
            return "none";
        };
        let due = parse_time(due).expect("checked when read");
        for t in self.remedies.clone() {
            match t {
                None => {
                    self.local = true;
                    return "made";
                }
                Some(t) if t <= due => return "made",
                Some(_) => {}
            }
        }
        if self.at > due {
            "not_honoured"
        } else {
            "due"
        }
    }
}

/// Seconds since the Unix epoch of `YYYY-MM-DDTHH:MM:SSZ`, the one form the
/// three implementations read.
pub(crate) fn parse_time(s: &str) -> Result<i64, Invalid> {
    parse_time_from(s, 1)
}

/// [`parse_time`] with the earliest year it reads: Go's `time.Parse`
/// reads year 0000, which rowstatus refuses and Start takes.
pub(crate) fn parse_time_from(s: &str, min_year: i64) -> Result<i64, Invalid> {
    let bad = || invalid(format!("time {s:?} is not YYYY-MM-DDTHH:MM:SSZ"));
    let b = s.as_bytes();
    if b.len() != 20
        || b[4] != b'-'
        || b[7] != b'-'
        || b[10] != b'T'
        || b[13] != b':'
        || b[16] != b':'
        || b[19] != b'Z'
    {
        return Err(bad());
    }
    let num = |r: std::ops::Range<usize>| -> Result<i64, Invalid> {
        let d = &s[r];
        if !d.bytes().all(|c| c.is_ascii_digit()) {
            return Err(bad());
        }
        d.parse().map_err(|_| bad())
    };
    let (y, mo, d) = (num(0..4)?, num(5..7)?, num(8..10)?);
    let (h, mi, sec) = (num(11..13)?, num(14..16)?, num(17..19)?);
    let leap = (y % 4 == 0 && y % 100 != 0) || y % 400 == 0;
    let days_in = [
        31,
        if leap { 29 } else { 28 },
        31,
        30,
        31,
        30,
        31,
        31,
        30,
        31,
        30,
        31,
    ];
    if y < min_year
        || !(1..=12).contains(&mo)
        || d < 1
        || d > days_in[(mo - 1) as usize]
        || h > 23
        || mi > 59
        || sec > 59
    {
        return Err(bad());
    }
    Ok(days_from_civil(y, mo, d) * 86400 + h * 3600 + mi * 60 + sec)
}

fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146097 + doe - 719468
}

fn format_time(t: i64) -> String {
    let days = t.div_euclid(86400);
    let rem = t.rem_euclid(86400);
    let z = days + 719468;
    let era = z.div_euclid(146097);
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn times_round_trip() {
        for s in [
            "1970-01-01T00:00:00Z",
            "2026-11-04T23:59:59Z",
            "2000-02-29T12:00:00Z",
            "2100-03-01T00:00:00Z",
        ] {
            assert_eq!(format_time(parse_time(s).unwrap()), s);
        }
        assert!(parse_time("2100-02-29T00:00:00Z").is_err());
    }

    #[test]
    fn repeated_member_refused() {
        let raw = br#"{"at": "2026-11-02T00:00:00Z", "at": "2026-11-03T00:00:00Z", "parties": ["a", "b"], "row": {}}"#;
        assert!(parse_input(raw).is_err());
        let raw = br#"{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}} {}"#;
        assert!(parse_input(raw).is_err());
        let raw = br#"{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}}]"#;
        assert!(parse_input(raw).is_err());
        let raw = br#"{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}, "named": {"x": {"latest": "2026-11-02T00:00:00Z"}, "x": {"latest": "2026-11-03T00:00:00Z"}}}"#;
        assert!(parse_input(raw).is_err());
        let raw = br#"{"at": "2026-11-02T00:00:00Z", "parties": ["\ud800", "b"], "row": {}}"#;
        assert!(parse_input(raw).is_err());
        let raw = br#"{"at": "2026-11-02T00:00:00Z", "parties": ["a", "b"], "row": {}, "named": {"\ud800": {}}}"#;
        assert!(parse_input(raw).is_err());
    }
}
