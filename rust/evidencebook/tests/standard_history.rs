//! The `standard_history/1` card against agent-action-capsule's `derive_card`
//! and Go's `standardhistory.Derive`: the cards in
//! `dealprofile/testdata/parity-vectors.json` (derived by Go's
//! `TestParityVectors`, and by the Python package) are derived here, byte
//! for byte, and the inputs that make no card are refused.

use evidencebook::standard_history::{derive_card, AsOf, Policy, Window};
use serde_json::{json, Value};

fn vectors() -> Value {
    serde_json::from_str(include_str!(
        "../../../dealprofile/testdata/parity-vectors.json"
    ))
    .unwrap()
}

fn as_of() -> AsOf {
    AsOf {
        anchor: "ab".repeat(32),
        anchor_time: "2026-10-31T00:00:00Z".into(),
        first_checkpoint_time: "2026-09-30T08:00:00Z".into(),
    }
}

fn policy(window: Window) -> Policy {
    Policy {
        digest: "cd".repeat(32),
        window,
    }
}

fn deal() -> Value {
    json!({"deal": "d1", "agreed_at": "2026-10-06T09:09:00Z", "role": "bought",
        "amount": {"value": "55880", "assetCode": "USD", "assetScale": 2},
        "outcome": "held", "happened": "stamped_by_both", "disputes": []})
}

fn derive(deals: &[Value]) -> Result<Vec<u8>, String> {
    derive_card(
        "person",
        "person_declared",
        &as_of(),
        &policy(Window::All),
        deals,
    )
    .map_err(|e| e.to_string())
}

#[test]
fn a_card_is_the_one_go_derives() {
    let v = vectors();
    let cards = v["cards"].as_array().unwrap();
    assert!(cards.len() >= 5);
    for c in cards {
        let window = &c["policy"]["window"];
        let window = match window["months"].as_u64() {
            Some(n) => Window::Months(n),
            None => {
                assert_eq!(window, &json!({"all": true}));
                Window::All
            }
        };
        let deals: Vec<Value> = c["deals"]
            .as_array()
            .into_iter()
            .flatten()
            .map(|d| serde_json::from_str(d.as_str().unwrap()).unwrap())
            .collect();
        let got = derive_card(
            "person",
            "holder",
            &AsOf {
                anchor: c["as_of"]["anchor"].as_str().unwrap().into(),
                anchor_time: c["as_of"]["anchor_time"].as_str().unwrap().into(),
                first_checkpoint_time: c["as_of"]["first_checkpoint_time"].as_str().unwrap().into(),
            },
            &Policy {
                digest: c["policy"]["digest"].as_str().unwrap().into(),
                window,
            },
            &deals,
        )
        .unwrap();
        assert_eq!(
            String::from_utf8(got).unwrap(),
            c["card"].as_str().unwrap(),
            "{}",
            c["name"]
        );
    }
}

#[test]
fn the_window_runs_back_by_calendar_months_clamped_to_a_short_month() {
    // 2026-03-31 minus one month is 2026-02-28 (clamped): a deal on the 28th
    // is in the window, one on the 27th is not.
    let at = |day: &str| {
        json!({"deal": "d", "agreed_at": format!("2026-02-{day}T00:00:00Z"), "role": "bought",
        "amount": {"value": "0", "assetCode": "USD", "assetScale": 0},
        "outcome": "held", "happened": "no_record", "disputes": []})
    };
    let as_of = AsOf {
        anchor: "ab".repeat(32),
        anchor_time: "2026-03-31T00:00:00Z".into(),
        first_checkpoint_time: "2026-01-01T00:00:00Z".into(),
    };
    let card = |deals: &[Value]| -> Value {
        let octets = derive_card(
            "node",
            "node_declared",
            &as_of,
            &policy(Window::Months(1)),
            deals,
        )
        .unwrap();
        serde_json::from_slice(&octets).unwrap()
    };
    assert_eq!(card(&[at("28")])["in_window"]["of"], 1);
    assert_eq!(card(&[at("27")])["in_window"]["of"], 0);
    // A deal after the anchor is in no window.
    let later = json!({"deal": "d", "agreed_at": "2026-04-01T00:00:00Z", "role": "bought",
        "amount": {"value": "1", "assetCode": "USD", "assetScale": 0},
        "outcome": "held", "happened": "no_record", "disputes": []});
    assert_eq!(card(&[later])["in_window"]["of"], 0);
    // A window reaching before year 1 holds every deal.
    let huge = derive_card(
        "node",
        "node_declared",
        &as_of,
        &policy(Window::Months((1 << 53) - 1)),
        &[at("27")],
    )
    .unwrap();
    assert_eq!(
        serde_json::from_slice::<Value>(&huge).unwrap()["in_window"]["of"],
        1
    );
}

#[test]
fn a_shop_preauth_deal_is_in_the_window_and_not_counted() {
    let mut d = deal();
    d["counterparty_class"] = json!("shop_preauth");
    let card: Value = serde_json::from_slice(&derive(&[d, deal()]).unwrap()).unwrap();
    assert_eq!(
        card["in_window"],
        json!({"counted": 1, "not_counted": 1, "of": 2})
    );
    assert_eq!(card["class_missing"], json!({"count": 1, "of": 1}));
}

#[test]
fn inputs_that_make_no_card_are_refused() {
    let with = |member: &str, value: Value| {
        let mut d = deal();
        d[member] = value;
        d
    };
    let cases = [
        ("deal without a reference", with("deal", json!(""))),
        (
            "a time that is not IP 5.2",
            with("agreed_at", json!("2026-10-06")),
        ),
        (
            "an impossible date",
            with("agreed_at", json!("2026-02-30T00:00:00Z")),
        ),
        ("an unknown role", with("role", json!("sold"))),
        ("an unknown outcome", with("outcome", json!("won"))),
        ("an unknown happened", with("happened", json!("both"))),
        (
            "an unknown class",
            with("counterparty_class", json!("robot")),
        ),
        (
            "a bad asset code",
            with(
                "amount",
                json!({"value": "1", "assetCode": "usd", "assetScale": 0}),
            ),
        ),
        (
            "a leading zero",
            with(
                "amount",
                json!({"value": "01", "assetCode": "USD", "assetScale": 0}),
            ),
        ),
        (
            "a scale out of range",
            with(
                "amount",
                json!({"value": "1", "assetCode": "USD", "assetScale": 256}),
            ),
        ),
        (
            "disputes that are not an array",
            with("disputes", json!({})),
        ),
        (
            "a malformed dispute",
            with(
                "disputes",
                json!([{"side": "raised", "outcome": "open", "forum": "court"}]),
            ),
        ),
        ("not an object", json!("deal")),
    ];
    for (name, deal) in cases {
        assert!(derive(&[deal]).is_err(), "{name}");
    }
    let bad_window = |w| derive_card("person", "person_declared", &as_of(), &policy(w), &[]);
    assert!(bad_window(Window::Months(0)).is_err());
    assert!(bad_window(Window::Months(1 << 53)).is_err());
    let mut late = as_of();
    late.first_checkpoint_time = "soon".into();
    assert!(derive_card(
        "person",
        "person_declared",
        &late,
        &policy(Window::All),
        &[]
    )
    .is_err());
}

#[test]
fn a_caip_19_asset_is_a_currency() {
    let mut d = deal();
    d["amount"]["assetCode"] = json!("eip155:1/erc20:0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48");
    assert!(derive(&[d.clone()]).is_ok());
    d["amount"]["assetCode"] = json!("eip155:1/erc20");
    assert!(derive(&[d]).is_err());
}
