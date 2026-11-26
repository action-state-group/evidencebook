// SPDX-License-Identifier: Apache-2.0

//! Authorization of acts (design §3.2, invariant D), as Go's
//! `xdealv1.CheckAuthorizations` checks it (`authorization.go`): an act is
//! a deal-book perform or settle that cites, by digest, the check in the
//! holder's journal it rests on, under v0's authorization conditions. A
//! journal record's nonce is drawn fresh (PROFILE §5): two records sharing
//! one are refused. [`new_nonce`] draws one.

use std::collections::BTreeMap;

use rand_core::{OsRng, RngCore};

use super::check::{record_digest, stage_err};
use super::names::*;
use super::record::{Body, Record};
use super::start::{act_check, ActKind};
use super::types::*;
use crate::deal::{
    ApprovalBody as V0ApprovalBody, Error, IntentBody, LimitSet, VerdictBody as V0VerdictBody,
    VerdictBodyRules,
};

/// The stage of the authorization of acts.
pub const STAGE_AUTHORIZATION: &str = "authorization";

/// Draws a fresh nonce for a journal record whose digest leaves the holder
/// (PROFILE §5): 256 bits from the OS, as 64 lowercase hex.
pub fn new_nonce() -> String {
    let mut b = [0u8; 32];
    OsRng.fill_bytes(&mut b);
    hex::encode(b)
}

/// What authorizes one act (Go `ActAuthorization`).
#[derive(Clone, Debug, PartialEq)]
pub struct ActAuthorization {
    pub act: Record,
    pub check: Record,
    pub verdict: Record,
    pub approval: Record,
    /// The approval's record digest.
    pub approval_digest: String,
    /// The approval is the person's (approver user or agent_card), so it
    /// needs the person's signature over `approval_digest`. It is set too for
    /// a standing-intent approval whose verdict carries no rules block
    /// (`rules_not_recorded`) or rules that did not allow the act on their
    /// own (`rules_not_evaluable`).
    pub needs_person_signature: bool,
    /// The act rests on a standing-intent approval whose verdict carries no
    /// rules block: nothing records which rules, if any, evaluated the
    /// person's caps for it, so it escalates to the person (Sherlock2's
    /// ruling ops bb59237c, after u503; evidencebook#983; Go
    /// `RulesNotRecorded`).
    pub rules_not_recorded: bool,
    /// The act rests on a standing-intent approval whose verdict's rules
    /// did not allow the act on their own ([`rules_unevaluated`]): it
    /// escalates to the person whatever the verdict's result says (u503;
    /// evidencebook#982; Go `RulesNotEvaluable`).
    pub rules_not_evaluable: bool,
    /// The digests of the confirm_limits approvals a standing-intent
    /// approval's allowed rests on.
    pub limit_approvals: Vec<String>,
    /// The digest of the first intent a standing-intent approval rests on,
    /// empty for any other approval.
    pub intent: String,
}

/// Whether a verdict's rules did not allow the act on their own (PROFILE
/// §8; Go `RulesUnevaluated`): their status is not_evaluated; or they were
/// evaluated and the checker's own verdict is not allow; or, whatever their
/// status, a measured finding is not_evaluable. A finding is measured when it
/// names its check; one that names none is a rule its ruleset only declares,
/// which never moves the verdict.
pub fn rules_unevaluated(r: Option<&VerdictBodyRules>) -> bool {
    let Some(r) = r else { return false };
    let measured_unevaluable =
        r.findings.iter().flatten().any(|f| {
            f.verdict == "not_evaluable" && f.check.as_deref().is_some_and(|c| !c.is_empty())
        });
    measured_unevaluable
        || r.status == "not_evaluated"
        || (r.status == "evaluated" && r.verdict.as_deref() != Some("allow"))
}

fn person_approver(approver: &str) -> bool {
    approver == "user" || approver == "agent_card"
}

fn check_action(k: ActKind) -> &'static str {
    match k {
        ActKind::Pay => "pay",
        ActKind::Refund => "cancel",
        ActKind::Perform => "commit",
    }
}

fn kind_name(k: ActKind) -> &'static str {
    match k {
        ActKind::Pay => "payer settle",
        ActKind::Refund => "refund",
        ActKind::Perform => "perform",
    }
}

/// A record's at as Go's `parseAt` reads it (`time.Parse(time.RFC3339,
/// at)`, [`super::gotime`]): the instant as (Unix seconds, nanoseconds), or
/// Go's error text.
fn at_of(r: &Record) -> Result<(i64, u32), String> {
    super::gotime::parse(&r.block.at)
        .map_err(|e| format!("its at {}: {e}", crate::deal::check::go_q(&r.block.at)))
}

/// Checks every act that cites a check (Go `CheckAuthorizations`). `journal`
/// is the holder's journal records of the deal's task in journal order;
/// `acts` its deal-book perform and settle records in book order.
pub fn check_authorizations(
    journal: &[Record],
    acts: &[Record],
) -> Result<Vec<ActAuthorization>, Error> {
    let w = walk_journal(journal)?;
    let mut used: BTreeMap<usize, usize> = BTreeMap::new();
    let mut out = Vec::new();
    for (i, act) in acts.iter().enumerate() {
        let bad = |m: String| stage_err(STAGE_AUTHORIZATION, format!("act {}: {m}", i + 1));
        let Some((check_ref, kind)) = act_check(act) else {
            return Err(bad(format!("a {} is not an act", act.block.record_type)));
        };
        let Some(check_ref) = check_ref else {
            continue;
        };
        let act_at = at_of(act).map_err(bad)?;
        let ci = w.digests.iter().position(|d| *d == check_ref.digest);
        let Some(ci) = ci.filter(|&c| journal[c].block.record_type == TYPE_CHECK) else {
            return Err(bad("it cites no check the journal holds".into()));
        };
        if journal[ci].block.deal_id != act.block.deal_id {
            return Err(bad("its check serves another deal".into()));
        }
        let cb = &w.check[&ci];
        let want = check_action(kind);
        if cb.v0.action != want {
            return Err(bad(format!(
                "the act differs from the one checked: a {} rests on a {want:?} check, not {:?}",
                kind_name(kind),
                cb.v0.action
            )));
        }
        let Some(&vi) = w.verdict_of.get(&ci) else {
            return Err(bad("its check has no verdict".into()));
        };
        let Some(&ai) = w.first_answer.get(&vi) else {
            return Err(bad("its verdict has no sealed approval".into()));
        };
        let ab = &w.approval[&ai];
        let approval_at = at_of(&journal[ai]).unwrap_or((super::gotime::ZERO, 0));
        if approval_at > act_at {
            return Err(bad("its approval was sealed after it".into()));
        }
        if ab.choice == CHOICE_CONFIRM {
            return Err(bad("a confirm_limits approval authorizes no act".into()));
        }
        if !ab.proceed {
            return Err(bad("its verdict's first approval does not proceed".into()));
        }
        if w.verdict.get(&vi).is_some_and(|v| v.result == "deny") {
            return Err(bad("its verdict denies".into()));
        }
        if let Some(prev) = used.get(&ai) {
            return Err(bad(format!(
                "its approval already authorized act {}",
                prev + 1
            )));
        }
        used.insert(ai, i);
        for (k, rec) in journal.iter().enumerate().skip(ci + 1) {
            let at = at_of(rec).unwrap_or((super::gotime::ZERO, 0));
            if at <= act_at && changes_details(rec) {
                return Err(bad(format!(
                    "journal record {} ({}) changed the details after its check: a new check is needed",
                    k + 1,
                    rec.block.record_type
                )));
            }
        }
        let mut a = ActAuthorization {
            act: act.clone(),
            check: journal[ci].clone(),
            verdict: journal[vi].clone(),
            approval: journal[ai].clone(),
            approval_digest: w.digests[ai].clone(),
            needs_person_signature: person_approver(&ab.approver),
            rules_not_recorded: false,
            rules_not_evaluable: false,
            limit_approvals: Vec::new(),
            intent: String::new(),
        };
        if ab.approver == "standing_intent" {
            match w.verdict[&vi].rules.as_ref() {
                None => {
                    a.rules_not_recorded = true;
                    a.needs_person_signature = true;
                }
                Some(r) if rules_unevaluated(Some(r)) => {
                    a.rules_not_evaluable = true;
                    a.needs_person_signature = true;
                }
                Some(_) => {}
            }
            a.limit_approvals = w.limits_at[&ai]
                .iter()
                .map(|&j| w.digests[j].clone())
                .collect();
            a.intent = w.digests[w.intent_at[&ai]].clone();
        }
        out.push(a);
    }
    Ok(out)
}

struct JournalWalk {
    digests: Vec<String>,
    check: BTreeMap<usize, CheckBody>,
    verdict: BTreeMap<usize, V0VerdictBody>,
    approval: BTreeMap<usize, V0ApprovalBody>,
    verdict_of: BTreeMap<usize, usize>,
    first_answer: BTreeMap<usize, usize>,
    limits_at: BTreeMap<usize, Vec<usize>>,
    intent_at: BTreeMap<usize, usize>,
}

fn intent_of(r: &Record) -> Option<&IntentBody> {
    match &r.body {
        Body::Intent(b) => Some(b),
        _ => None,
    }
}

fn walk_journal(journal: &[Record]) -> Result<JournalWalk, Error> {
    let mut w = JournalWalk {
        digests: Vec::new(),
        check: BTreeMap::new(),
        verdict: BTreeMap::new(),
        approval: BTreeMap::new(),
        verdict_of: BTreeMap::new(),
        first_answer: BTreeMap::new(),
        limits_at: BTreeMap::new(),
        intent_at: BTreeMap::new(),
    };
    let mut by_digest: BTreeMap<String, usize> = BTreeMap::new();
    for (i, r) in journal.iter().enumerate() {
        let d = record_digest(r)?;
        by_digest.insert(d.clone(), i);
        w.digests.push(d);
    }
    let mut lim = Limits::default();
    let mut confirmed: BTreeMap<usize, bool> = BTreeMap::new();
    let mut confirmations: Vec<usize> = Vec::new();
    let mut first_intent: Option<usize> = None;
    // Go's zero Time: at never decreases from it, so a year-0 time is refused.
    let mut last: Option<(i64, u32)> = Some((super::gotime::ZERO, 0));
    let mut nonces: BTreeMap<String, usize> = BTreeMap::new();
    for (i, r) in journal.iter().enumerate() {
        let bad = |m: String| {
            stage_err(
                STAGE_AUTHORIZATION,
                format!("journal record {}: {m}", i + 1),
            )
        };
        let n = nonce_of(r);
        if !n.is_empty() {
            if let Some(j) = nonces.get(n) {
                return Err(bad(format!(
                    "its nonce (or verbatim_commitment) is journal record {}'s: a nonce is drawn fresh for each record",
                    j + 1
                )));
            }
            nonces.insert(n.to_string(), i);
        }
        let at = at_of(r).map_err(bad)?;
        if last.is_some_and(|l| at < l) {
            return Err(bad(
                "its at is earlier than the record before it: at never decreases".into(),
            ));
        }
        last = Some(at);
        let one = |rel: &str, t: &str| -> Result<usize, Error> {
            let mut js = Vec::new();
            for rf in r.block.refs.as_deref().unwrap_or(&[]) {
                if rf.rel == rel {
                    match by_digest.get(&rf.digest) {
                        Some(&j) if j < i && journal[j].block.record_type == t => js.push(j),
                        _ => return Err(bad(format!("its {rel} ref names no earlier {t}"))),
                    }
                }
            }
            if js.len() != 1 {
                return Err(bad(format!(
                    "a {} carries exactly one {rel} ref",
                    r.block.record_type
                )));
            }
            Ok(js[0])
        };
        match r.block.record_type.as_str() {
            TYPE_INTENT => {
                lim.narrow(intent_of(r));
                if first_intent.is_none() {
                    first_intent = Some(i);
                }
            }
            TYPE_CHECK => {
                if let Body::Check(b) = &r.body {
                    w.check.insert(i, b.clone());
                }
            }
            TYPE_VERDICT => {
                let j = one("checks", TYPE_CHECK)?;
                if journal[j].block.deal_id != r.block.deal_id {
                    return Err(bad("a verdict answers a check of its own deal".into()));
                }
                if w.verdict_of.contains_key(&j) {
                    return Err(bad("a check has one verdict".into()));
                }
                w.verdict_of.insert(j, i);
                if let Body::Verdict(b) = &r.body {
                    w.verdict.insert(i, b.v0.clone());
                }
            }
            TYPE_APPROVAL => {
                let Body::Approval(v1) = &r.body else {
                    return Err(bad("an approval's body is not an approval".into()));
                };
                let b = v1.v0.clone();
                w.approval.insert(i, b.clone());
                if b.choice == CHOICE_CONFIRM {
                    let j = one("approves", TYPE_INTENT)?;
                    let prop = intent_of(&journal[j]);
                    let replaced = journal[j + 1..i]
                        .iter()
                        .any(|k| k.block.record_type == TYPE_INTENT);
                    let late = confirmed.get(&j).copied().unwrap_or(false) || replaced;
                    lim.confirm(&b, prop, late).map_err(bad)?;
                    if b.proceed && !late {
                        confirmed.insert(j, true);
                        confirmations.push(i);
                    }
                    continue;
                }
                let j = one("approves", TYPE_VERDICT)?;
                if journal[j].block.deal_id != r.block.deal_id {
                    return Err(bad("an approval answers a verdict of its own deal".into()));
                }
                w.first_answer.entry(j).or_insert(i);
                let v = w.verdict.get(&j).cloned().unwrap_or_default();
                let chk = w
                    .verdict_of
                    .iter()
                    .find(|(_, &vi)| vi == j)
                    .and_then(|(ci, _)| w.check.get(ci));
                if b.approver == "standing_intent" {
                    if v.result != "pass" {
                        return Err(bad("a standing-intent approval needs a passing verdict; a pause needs the person's answer".into()));
                    }
                    if chk.is_some_and(|c| !lim.allows(&c.v0.action)) {
                        return Err(bad("standing-intent approval for an action the limits in force do not allow".into()));
                    }
                    let Some(fi) = first_intent else {
                        return Err(bad("a standing-intent approval rests on an intent; the journal holds none before it".into()));
                    };
                    w.limits_at.insert(i, confirmations.clone());
                    w.intent_at.insert(i, fi);
                } else if (v.result == "pause" || v.result == "deny")
                    && !v.options.contains(&b.choice)
                {
                    return Err(bad("the choice is not one of the verdict's options".into()));
                }
                let bc = b.card_commitment.as_deref().unwrap_or("");
                let vc = v.card_commitment.as_deref().unwrap_or("");
                if !bc.is_empty() && vc.is_empty() {
                    return Err(bad("the approval commits to a shown card, but the verdict it answers rendered none".into()));
                }
                if !bc.is_empty() && bc != vc {
                    return Err(bad("the card shown is not the card checked: the approval's card_commitment differs from the verdict's".into()));
                }
                if person_approver(&b.approver) && bc.is_empty() {
                    return Err(bad(
                        "the person's approval commits to the card it answered (card_commitment)"
                            .into(),
                    ));
                }
            }
            _ => {}
        }
    }
    Ok(w)
}

/// A journal record after which no earlier check covers an act (Go
/// `changesDetails`).
fn changes_details(r: &Record) -> bool {
    match &r.body {
        Body::DetailChange(_) => true,
        Body::Message(_) => r.block.counterparty.is_some(),
        Body::Evidence(b) => r.block.counterparty.is_some() || b.counterparty_facts.is_some(),
        Body::Intent(b) => {
            r.block.counterparty.is_some()
                || (b.r#move.as_deref().is_some_and(|m| !m.is_empty())
                    && b.asked
                        .as_ref()
                        .and_then(|a| a.item.as_deref())
                        .is_some_and(|i| !i.is_empty()))
        }
        Body::AgentMove(b) => b.r#move == "chose",
        _ => false,
    }
}

/// The limits in force (Go `limits`).
#[derive(Default)]
struct Limits {
    seen: bool,
    allowed: Vec<String>,
    set: bool,
    max_total: Option<i64>,
    delegated: Vec<String>,
    bounds: String,
    bounds_known: bool,
}

impl Limits {
    fn narrow(&mut self, b: Option<&IntentBody>) {
        let Some(b) = b else { return };
        if !self.seen {
            self.seen = true;
            self.bounds_known = true;
            self.allowed = b.allowed.clone().unwrap_or_default();
            self.set = b.allowed.is_some();
            self.max_total = b.max_total_minor;
            self.delegated = b.delegated.clone().unwrap_or_default();
            self.bounds = b.bounds_commitment.clone().unwrap_or_default();
            return;
        }
        if let Some(m) = b.max_total_minor {
            if self.max_total.is_none_or(|cur| m < cur) {
                self.max_total = Some(m);
            }
        }
        if let Some(bc) = b.bounds_commitment.as_deref().filter(|s| !s.is_empty()) {
            if bc != self.bounds {
                self.bounds_known = false;
            }
        }
        if let Some(allowed) = &b.allowed {
            let next: Vec<String> = allowed
                .iter()
                .filter(|a| !self.set || self.allowed.contains(a))
                .cloned()
                .collect();
            self.allowed = next;
            self.set = true;
        }
        let names_item = b
            .asked
            .as_ref()
            .and_then(|a| a.item.as_deref())
            .is_some_and(|i| !i.is_empty());
        if names_item || b.r#move.as_deref() == Some("picked") {
            self.delegated.retain(|d| d != "item");
        }
    }

    fn allows(&self, action: &str) -> bool {
        !self.set || self.allowed.iter().any(|a| a == action)
    }

    fn allowed_or_none(&self) -> Option<Vec<String>> {
        self.set.then(|| self.allowed.clone())
    }

    fn in_force(&self) -> LimitSet {
        LimitSet {
            max_total_minor: self.max_total,
            allowed: self.allowed_or_none(),
            delegated: (!self.delegated.is_empty()).then(|| self.delegated.clone()),
            bounds_commitment: (self.bounds_known && !self.bounds.is_empty())
                .then(|| self.bounds.clone()),
            ..Default::default()
        }
    }

    fn without_unknown_bounds(&self, mut set: LimitSet) -> LimitSet {
        if !self.bounds_known {
            set.bounds_commitment = None;
        }
        set
    }

    fn confirm(
        &mut self,
        b: &V0ApprovalBody,
        prop: Option<&IntentBody>,
        late: bool,
    ) -> Result<(), String> {
        if late {
            if b.proceed {
                return Err("a proposal replaced by a later intent, or already confirmed, cannot be confirmed (proceed must be false)".into());
            }
            return Ok(());
        }
        if !b.proceed {
            return Err("a confirmation of a standing proposal proceeds".into());
        }
        let (Some(limits), Some(prop)) = (&b.limits, prop) else {
            return Err("a confirmation states the limits it replaces and puts in force".into());
        };
        if !same_json(
            &self.without_unknown_bounds(limits.previous.clone()),
            &self.in_force(),
        ) {
            return Err("limits.previous is not the limits in force".into());
        }
        let mut next = self.in_force();
        if prop.max_total_minor.is_some() {
            next.max_total_minor = prop.max_total_minor;
        }
        if prop.allowed.is_some() {
            next.allowed = prop.allowed.clone();
        }
        let prop_bounds = prop.bounds_commitment.clone().unwrap_or_default();
        if !prop_bounds.is_empty() {
            next.bounds_commitment = Some(prop_bounds.clone());
        }
        let added: Vec<String> = prop
            .delegated
            .as_deref()
            .unwrap_or(&[])
            .iter()
            .filter(|d| !self.delegated.contains(d))
            .cloned()
            .collect();
        if !added.is_empty() {
            let mut d = self.delegated.clone();
            d.extend(added.iter().cloned());
            next.delegated = Some(d);
        }
        let mut got = limits.new.clone();
        if prop_bounds.is_empty() {
            got = self.without_unknown_bounds(got);
        }
        if !same_json(&got, &next) {
            return Err("limits.new is not what the intent proposed".into());
        }
        let more = (prop.max_total_minor.is_some()
            && self.max_total.is_some()
            && prop.max_total_minor > self.max_total)
            || (prop.allowed.is_some()
                && self.set
                && !prop
                    .allowed
                    .as_deref()
                    .unwrap()
                    .iter()
                    .all(|a| self.allowed.contains(a)))
            || (!prop_bounds.is_empty() && prop_bounds != self.bounds)
            || !added.is_empty();
        if !more {
            return Err(
                "the intent asks for no higher limit and no new action: nothing to confirm".into(),
            );
        }
        self.max_total = next.max_total_minor;
        self.allowed = next.allowed.clone().unwrap_or_default();
        self.set = next.allowed.is_some();
        self.delegated = next.delegated.clone().unwrap_or_default();
        self.bounds = limits.new.bounds_commitment.clone().unwrap_or_default();
        self.bounds_known = true;
        Ok(())
    }
}

fn same_json(a: &LimitSet, b: &LimitSet) -> bool {
    serde_json::to_vec(a).ok() == serde_json::to_vec(b).ok()
}

/// A journal record's own secret: its nonce, or an intent's
/// verbatim_commitment; empty for a type that carries none (Go `nonceOf`).
fn nonce_of(r: &Record) -> &str {
    match &r.body {
        Body::Intent(b) => &b.verbatim_commitment,
        Body::Admit(b) => &b.nonce,
        Body::Check(b) => &b.nonce,
        Body::Verdict(b) => &b.nonce,
        Body::Approval(b) => &b.nonce,
        _ => "",
    }
}
