//! The `standard_history/1` card (agent-action-capsule
//! `spec/standard-history-v0.md`, cited as SH): a card, as of one anchor,
//! that counts the holder's deals in the window its policy sets. It is
//! recipient-free: every requester answered gets the same bytes.
//!
//! A port of `agent_action_capsule.standard_history.derive_card` (Python) and
//! the Go `standardhistory.Derive`, byte for byte, and of what a Book does
//! with it: [`StandardHistorySource`] is Go's `StandardHistorySource`, the
//! host's inputs that [`crate::respond::respond`] builds the card from.
//!
//! One departure from Go and Python, stated: the card is not checked against
//! its own inputs afterwards (SH §6, `Check`). The crate has no card checker;
//! the card's properties are the reader's verifier in every language, and a
//! card derived here has its form, integrality and denominators by
//! construction. Inputs that make no card are [`StandardHistoryError`]
//! before any count, as in Go.

use crate::answer::Record;
use crate::canonical::jcs;
use crate::substrate::StatedCheckpoint;
use serde_json::{json, Map, Value};
use std::collections::BTreeMap;
use std::fmt;

/// The registered token of the standard card.
pub const DERIVATION: &str = "standard_history/1";

/// Inputs that make no standard card.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StandardHistoryError(pub String);

impl fmt::Display for StandardHistoryError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "standard history: {}", self.0)
    }
}

impl std::error::Error for StandardHistoryError {}

fn bad<T>(message: impl Into<String>) -> Result<T, StandardHistoryError> {
    Err(StandardHistoryError(message.into()))
}

/// The most a count or a window's months can be (2^53 - 1, SH §6).
const MAX_COUNT: u64 = (1 << 53) - 1;

/// SH §7: the counterparty classes an entry may name.
const COUNTERPARTY_CLASSES: [&str; 3] = ["person_agent", "business_agent", "shop_preauth"];
const NOT_COUNTED: &str = "shop_preauth";
const ROLES: [&str; 2] = ["provided", "bought"];
const OUTCOMES: [&str; 3] = ["held", "did_not_hold", "undecided"];
const HAPPENED: [&str; 3] = ["stamped_by_both", "receipt_only", "no_record"];
const SIDES: [&str; 2] = ["raised", "received"];
const DISPUTE_OUTCOMES: [&str; 6] = ["open", "for", "against", "split", "no_finding", "withdrawn"];
const FORUMS: [&str; 4] = ["auto", "network", "rail", "external"];

/// SH §5.1: the policy's window.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Window {
    /// Every deal up to the anchor.
    All,
    /// The last `n` calendar months, `1 <= n <= 2^53 - 1`.
    Months(u64),
}

/// The two members of the policy in force at the anchor (SH §5.1): its
/// digest and its window.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Policy {
    pub digest: String,
    pub window: Window,
}

/// The anchor the card is as of: its digest, its time and the time of the
/// stream's first checkpoint, each time in the profile's form
/// (`YYYY-MM-DDTHH:MM:SSZ`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AsOf {
    pub anchor: String,
    pub anchor_time: String,
    pub first_checkpoint_time: String,
}

/// What the host's inputs return: the policy and one deal entry (SH §5.2)
/// per deal the stream holds at the anchor.
pub type Inputs = (Policy, Vec<Value>);

/// The host's function from the anchor and the servable records it covers
/// to its [`Inputs`].
pub type InputsFn = Box<
    dyn Fn(&StatedCheckpoint, &[HeldRecord]) -> Result<Inputs, StandardHistoryError> + Send + Sync,
>;

/// A servable record the anchor covers, as the host's inputs read it: the
/// record and its header's members (`record_type`, `committed_at`,
/// `statement` and the rest).
#[derive(Clone, Debug)]
pub struct HeldRecord {
    pub record: Record,
    pub header: Map<String, Value>,
}

/// What a host supplies for [`crate::respond::respond`] to answer
/// `standard_history/1` over the `full_history` subject. With it set, the
/// responder answers the card to any authenticated requester, whatever the
/// share policy's tier; a stranger still has only the public questions
/// answered (checkpoints and `history_card/1`). Without it,
/// `standard_history/1` is `derivation_unsupported`.
///
/// The book defines neither a disclosure policy format nor how its records
/// make deals: the host reads both (SH §5.1, SH-2).
pub struct StandardHistorySource {
    /// `person` or `node`: the kind of holder the book's key answers for, as
    /// the holder's own controller declaration states it.
    pub holder_kind: String,
    /// Returns the policy in force at the anchor and one deal entry per deal
    /// the stream holds at the anchor, read from the records: the book's
    /// servable records the anchor covers, in log order (each as the
    /// header's JSON members and the record). Its result must depend on its
    /// arguments only, so the card does not depend on who asks (ER §5).
    pub inputs: InputsFn,
}

impl StandardHistorySource {
    /// The holder kind must be `person` or `node`.
    pub fn validate(&self) -> Result<(), StandardHistoryError> {
        controller_class(&self.holder_kind).map(|_| ())
    }
}

/// SH §4: the one controller class `/1` allows for each holder kind.
pub fn controller_class(holder_kind: &str) -> Result<&'static str, StandardHistoryError> {
    match holder_kind {
        "person" => Ok("person_declared"),
        "node" => Ok("node_declared"),
        _ => bad(format!(
            "holder kind {holder_kind:?} is neither person nor node"
        )),
    }
}

impl StandardHistorySource {
    /// The card as of `anchor`, from the host's inputs over `records` (the
    /// servable records the anchor covers, in log order) and the time of the
    /// stream's first checkpoint, in its JCS octets.
    pub fn card(
        &self,
        anchor: &StatedCheckpoint,
        first_checkpoint_time: &str,
        records: &[HeldRecord],
    ) -> Result<Vec<u8>, StandardHistoryError> {
        let (policy, deals) = (self.inputs)(anchor, records)?;
        let as_of = AsOf {
            anchor: crate::answer::anchor_digest(anchor),
            anchor_time: crate::ask::profile_time(&anchor.checkpoint.timestamp),
            first_checkpoint_time: crate::ask::profile_time(first_checkpoint_time),
        };
        derive_card(
            &self.holder_kind,
            controller_class(&self.holder_kind)?,
            &as_of,
            &policy,
            &deals,
        )
    }
}

/// The card SH §4 states for these inputs, in its JCS octets. `controller_class`
/// is the card's member as given (see [`controller_class`] for the one a
/// holder kind allows).
pub fn derive_card(
    holder_kind: &str,
    controller_class: &str,
    as_of: &AsOf,
    policy: &Policy,
    deals: &[Value],
) -> Result<Vec<u8>, StandardHistoryError> {
    if let Window::Months(n) = policy.window {
        if !(1..=MAX_COUNT).contains(&n) {
            return bad(format!("not a policy window: {n} months"));
        }
    }
    let first = time(&as_of.first_checkpoint_time)?;
    let anchor_time = time(&as_of.anchor_time)?;
    let window = match policy.window {
        Window::All => json!({"all": true}),
        Window::Months(n) => json!({"months": n}),
    };
    let mut card = Map::new();
    card.insert("derivation".into(), json!(DERIVATION));
    card.insert("holder_kind".into(), json!(holder_kind));
    card.insert("controller_class".into(), json!(controller_class));
    card.insert("anchor".into(), json!(as_of.anchor));
    card.insert("since".into(), json!(&first.text[..7]));
    card.insert("window".into(), window);
    card.insert("policy".into(), json!(policy.digest));
    counts(deals, policy.window, &anchor_time, &mut card)?;
    jcs(&Value::Object(card)).map_err(|e| StandardHistoryError(e.to_string()))
}

/// A time in the profile's form (IP §5.2), read as its parts.
struct Time {
    text: String,
    year: i64,
    month: i64,
    day: i64,
}

fn time(text: &str) -> Result<Time, StandardHistoryError> {
    let b = text.as_bytes();
    let digits = |from: usize, to: usize| -> Option<i64> {
        let part = b.get(from..to)?;
        part.iter()
            .all(u8::is_ascii_digit)
            .then(|| part.iter().fold(0, |n, d| n * 10 + i64::from(d - b'0')))
    };
    let shape = b.len() == 20
        && [
            (4, b'-'),
            (7, b'-'),
            (10, b'T'),
            (13, b':'),
            (16, b':'),
            (19, b'Z'),
        ]
        .iter()
        .all(|&(i, c)| b[i] == c);
    let parts = shape.then(|| {
        Some((
            digits(0, 4)?,
            digits(5, 7)?,
            digits(8, 10)?,
            digits(11, 13)?,
            digits(14, 16)?,
            digits(17, 19)?,
        ))
    });
    let Some(Some((year, month, day, hour, minute, second))) = parts else {
        return bad(format!("not an IP §5.2 time: {text:?}"));
    };
    if year < 1
        || !(1..=12).contains(&month)
        || day < 1
        || day > days_in_month(year, month)
        || hour > 23
        || minute > 59
        || second > 59
    {
        return bad(format!("not an IP §5.2 time: {text:?}"));
    }
    Ok(Time {
        text: text.to_string(),
        year,
        month,
        day,
    })
}

fn days_in_month(year: i64, month: i64) -> i64 {
    match month {
        2 if year % 4 == 0 && (year % 100 != 0 || year % 400 == 0) => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// SH §5.1: the same time of day and day of the month, `months` calendar
/// months earlier, clamped to the last day of a shorter month. `None` when
/// that is before year 1, the first year a time can name: every deal is then
/// after the start. Times of one form compare as their text does.
fn window_start(anchor: &Time, months: u64) -> Option<String> {
    let total = i128::from(anchor.year) * 12 + i128::from(anchor.month - 1) - i128::from(months);
    let (year, month) = (total.div_euclid(12), total.rem_euclid(12) + 1);
    if year < 1 {
        return None;
    }
    let (year, month) = (year as i64, month as i64);
    let day = anchor.day.min(days_in_month(year, month));
    Some(format!(
        "{year:04}-{month:02}-{day:02}{}",
        &anchor.text[10..]
    ))
}

fn in_window(agreed_at: &str, window: Window, anchor: &Time) -> bool {
    if agreed_at > anchor.text.as_str() {
        return false;
    }
    match window {
        Window::All => true,
        Window::Months(n) => {
            window_start(anchor, n).is_none_or(|start| agreed_at >= start.as_str())
        }
    }
}

/// SH §5.3: floor(log10(value / 10^assetScale)), exactly
/// `digits(value) - assetScale - 1`; `None` for a zero amount.
fn decade(amount: &Value) -> Result<Option<i64>, StandardHistoryError> {
    let value = amount.get("value").and_then(Value::as_str);
    let exact = value.is_some_and(|v| {
        v == "0" || (!v.starts_with('0') && !v.is_empty() && v.bytes().all(|c| c.is_ascii_digit()))
    });
    let Some(value) = value.filter(|_| exact) else {
        return bad(format!(
            "amount value is not an exact integer string: {:?}",
            amount.get("value")
        ));
    };
    let scale = amount.get("assetScale").and_then(Value::as_u64);
    let Some(scale) = scale.filter(|s| *s <= 255) else {
        return bad(format!(
            "amount assetScale out of range: {:?}",
            amount.get("assetScale")
        ));
    };
    Ok((value != "0").then(|| value.len() as i64 - scale as i64 - 1))
}

/// SR "Amounts" assetCode: an ISO 4217 alphabetic code (BTC included), or a
/// CAIP-19 asset type.
fn currency(code: &str) -> bool {
    if code.len() == 3 && code.bytes().all(|c| c.is_ascii_uppercase()) {
        return true;
    }
    let part = |s: &str, min: usize, max: usize, extra: &str| {
        (min..=max).contains(&s.len())
            && s.bytes()
                .all(|c| c.is_ascii_alphanumeric() || extra.as_bytes().contains(&c))
    };
    let lower = |s: &str| s.bytes().all(|c| !c.is_ascii_uppercase());
    let Some((chain, asset)) = code.split_once('/') else {
        return false;
    };
    let (Some((namespace, reference)), Some((asset_namespace, asset_reference))) =
        (chain.split_once(':'), asset.split_once(':'))
    else {
        return false;
    };
    part(namespace, 3, 8, "-")
        && lower(namespace)
        && part(reference, 1, 32, "-_")
        && part(asset_namespace, 3, 8, "-")
        && lower(asset_namespace)
        && part(asset_reference, 1, 128, "-.%")
}

fn member<'a>(
    deal: &'a Value,
    name: &str,
    allowed: &[&str],
) -> Result<&'a str, StandardHistoryError> {
    match deal.get(name).and_then(Value::as_str) {
        Some(v) if allowed.contains(&v) => Ok(v),
        other => bad(format!(
            "deal {}: {name} {other:?}",
            deal.get("deal").and_then(Value::as_str).unwrap_or_default()
        )),
    }
}

/// The deal entry's form (SH §5.2). A malformed entry is the caller's error,
/// never a count.
fn check_entry(deal: &Value) -> Result<(), StandardHistoryError> {
    if !deal.is_object() {
        return bad("a deal entry is not an object");
    }
    let reference = match deal.get("deal").and_then(Value::as_str) {
        Some(r) if !r.is_empty() => r,
        _ => return bad("deal entry without a deal reference"),
    };
    time(
        deal.get("agreed_at")
            .and_then(Value::as_str)
            .unwrap_or_default(),
    )?;
    if let Some(class) = deal.get("counterparty_class") {
        if !class
            .as_str()
            .is_some_and(|c| COUNTERPARTY_CLASSES.contains(&c))
        {
            return bad(format!("unknown counterparty_class: {class}"));
        }
    }
    member(deal, "role", &ROLES)?;
    member(deal, "outcome", &OUTCOMES)?;
    member(deal, "happened", &HAPPENED)?;
    let amount = deal.get("amount");
    let code = amount
        .and_then(|a| a.get("assetCode"))
        .and_then(Value::as_str);
    if !code.is_some_and(currency) {
        return bad(format!("deal {reference}: amount without an SR assetCode"));
    }
    decade(amount.expect("an amount with an assetCode is an object"))?;
    let Some(disputes) = deal.get("disputes").and_then(Value::as_array) else {
        return bad(format!("deal {reference}: disputes is not an array"));
    };
    for d in disputes {
        let named = |name: &str, allowed: &[&str]| {
            d.get(name)
                .and_then(Value::as_str)
                .is_some_and(|v| allowed.contains(&v))
        };
        if !(named("side", &SIDES)
            && named("outcome", &DISPUTE_OUTCOMES)
            && named("forum", &FORUMS))
        {
            return bad(format!("deal {reference}: malformed dispute {d}"));
        }
    }
    Ok(())
}

/// A tally: `of`, then each part.
#[derive(Clone)]
struct Tally(BTreeMap<&'static str, u64>);

impl Tally {
    fn new(parts: &[&'static str]) -> Tally {
        Tally(
            std::iter::once("of")
                .chain(parts.iter().copied())
                .map(|p| (p, 0))
                .collect(),
        )
    }
    fn add(&mut self, part: &str) {
        for (name, count) in self.0.iter_mut() {
            if *name == part {
                *count += 1;
            }
        }
    }
    fn value(&self) -> Value {
        Value::Object(
            self.0
                .iter()
                .map(|(k, v)| ((*k).to_string(), json!(v)))
                .collect(),
        )
    }
}

/// The tallies SH §5 derives from the deal entries, written into `card`.
fn counts(
    deals: &[Value],
    window: Window,
    anchor: &Time,
    card: &mut Map<String, Value>,
) -> Result<(), StandardHistoryError> {
    let mut in_window_tally = Tally::new(&["counted", "not_counted"]);
    let mut class_missing = Tally::new(&["count"]);
    let mut outcomes = Tally::new(&OUTCOMES);
    let mut roles = Tally::new(&ROLES);
    let mut happened = Tally::new(&HAPPENED);
    // (currency, decade) -> its outcome tally; a zero amount's decade is None,
    // which sorts first (SH §4).
    let mut bands: BTreeMap<(String, Option<i64>), Tally> = BTreeMap::new();
    let mut disputes: BTreeMap<&str, (Tally, Tally, Tally)> = SIDES
        .iter()
        .map(|s| {
            (
                *s,
                (
                    Tally::new(&[]),
                    Tally::new(&DISPUTE_OUTCOMES),
                    Tally::new(&FORUMS),
                ),
            )
        })
        .collect();
    for deal in deals {
        check_entry(deal)?;
        let agreed_at = deal["agreed_at"].as_str().unwrap_or_default();
        if !in_window(agreed_at, window, anchor) {
            continue;
        }
        in_window_tally.add("of");
        let class = deal.get("counterparty_class").and_then(Value::as_str);
        if class == Some(NOT_COUNTED) {
            in_window_tally.add("not_counted");
            continue;
        }
        in_window_tally.add("counted");
        class_missing.add("of");
        if class.is_none() {
            class_missing.add("count");
        }
        for (tally, name) in [
            (&mut outcomes, "outcome"),
            (&mut roles, "role"),
            (&mut happened, "happened"),
        ] {
            tally.add("of");
            tally.add(deal[name].as_str().unwrap_or_default());
        }
        let amount = &deal["amount"];
        let key = (
            amount["assetCode"].as_str().unwrap_or_default().to_string(),
            decade(amount)?,
        );
        let band = bands.entry(key).or_insert_with(|| Tally::new(&OUTCOMES));
        band.add("of");
        band.add(deal["outcome"].as_str().unwrap_or_default());
        for d in deal["disputes"].as_array().into_iter().flatten() {
            let (of, by_outcome, by_forum) = disputes
                .get_mut(d["side"].as_str().unwrap_or_default())
                .expect("a checked side");
            of.add("of");
            by_outcome.add("of");
            by_outcome.add(d["outcome"].as_str().unwrap_or_default());
            by_forum.add("of");
            by_forum.add(d["forum"].as_str().unwrap_or_default());
        }
    }
    card.insert("in_window".into(), in_window_tally.value());
    card.insert("class_missing".into(), class_missing.value());
    card.insert("outcomes".into(), outcomes.value());
    card.insert("roles".into(), roles.value());
    card.insert("happened".into(), happened.value());
    let bands: Vec<Value> = bands
        .into_iter()
        .map(|((code, decade), tally)| {
            let Value::Object(mut band) = tally.value() else {
                unreachable!("a tally is an object")
            };
            band.insert("currency".into(), json!(code));
            band.insert("decade".into(), json!(decade));
            Value::Object(band)
        })
        .collect();
    card.insert("bands".into(), Value::Array(bands));
    let disputes: Map<String, Value> = disputes
        .into_iter()
        .map(|(side, (of, by_outcome, by_forum))| {
            (
                side.to_string(),
                json!({
                    "of": of.0["of"],
                    "by_outcome": by_outcome.value(),
                    "by_forum": by_forum.value(),
                }),
            )
        })
        .collect();
    card.insert("disputes".into(), Value::Object(disputes));
    Ok(())
}
