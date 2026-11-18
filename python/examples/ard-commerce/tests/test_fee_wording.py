# SPDX-License-Identifier: Apache-2.0
"""What the demo says about fees on a real rail: Maya's line gives only the routing fee her own wallet
reports, and a seller's receipt says its credited amount is not in its records. No engine needed."""
from __future__ import annotations

import json
from types import SimpleNamespace

import parties
import screens


def test_maya_line_on_a_real_rail_names_only_her_wallets_routing_fee():
    assert screens.fee_note(0, real=True) == " (routing fee 0 msat, as her wallet reports it)"
    assert "seller" not in screens.fee_note(0, real=True)


def test_maya_line_on_the_simulated_rail_is_unchanged_and_a_missing_fee_says_nothing():
    assert screens.fee_note(0, real=False) == " (fee 0 msat)"
    assert screens.fee_note(None, real=True) == ""


def _node(price: int):
    first = {"body": {"terms": {"item": "200 conference badges", "price_minor": price},
                      "recourse": {"rail": "lightning"}}, "x-deal-v0": {"record_type": "open", "at": "t0"}}
    return SimpleNamespace(deal_payloads=lambda deal_id: [json.dumps(first).encode()], notes=lambda kind: [])


LINE = ("Agreed price 289 msat. What this seller's wallet was credited is not in these records: "
        "its wallet provider may deduct a receiving fee.")


def test_a_sellers_receipt_on_a_real_rail_says_the_credited_amount_is_not_in_its_records():
    assert LINE in parties.receipt_view(_node(289), "Alder Print Co.", "deal-x", seller=True, real=True)


def test_the_buyers_receipt_and_the_simulated_rail_carry_no_credit_line():
    assert "credited" not in parties.receipt_view(_node(289), "Maya", "deal-x", real=True)
    out = parties.receipt_view(_node(289), "Alder Print Co.", "deal-x", seller=True, real=False)
    assert "credited" not in out and "simulated rail" not in out  # no payment step in this stub
