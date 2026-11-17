# SPDX-License-Identifier: Apache-2.0
"""parties.buyer_deal tells its caller the moment the rail reports a payment, before the steps that can still fail:
money that moved must be recorded as paid even if the seller's half or the close never comes."""
from __future__ import annotations

import types

import pytest

import parties


def test_on_paid_runs_before_the_steps_after_the_payment(tmp_path, monkeypatch):
    monkeypatch.setattr(parties.deals, "_reconcile", lambda *a, **k: None)
    monkeypatch.setattr(parties, "wait_file", lambda *a, **k: {})
    monkeypatch.setattr(parties, "write_atomic", lambda *a, **k: None)
    maya = types.SimpleNamespace(half=lambda d: {})  # no seal: the step after the payment fails
    peer = types.SimpleNamespace(home=tmp_path, id="alder")
    chain = types.SimpleNamespace(deal_id="d1")
    payment = types.SimpleNamespace(status="succeeded", reference="r")
    rail = types.SimpleNamespace(pay=lambda msat: payment)
    seen = []
    with pytest.raises(AttributeError):
        parties.buyer_deal(maya, peer, chain, rail, 289, on_paid=seen.append)
    assert seen == [payment]
