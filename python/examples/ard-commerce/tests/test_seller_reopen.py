# SPDX-License-Identifier: Apache-2.0
"""A seller window reopened after a deal must not take that deal's request again: the request file stays in the
wire directory, the seller's own record of what it handled is in memory. No engine needed."""
from __future__ import annotations

import threading
import time
from pathlib import Path
from types import SimpleNamespace

import parties


def _seller(tmp_path: Path, handled_deals: list[str]):
    s = object.__new__(parties.Seller)
    s.w = SimpleNamespace(_stop=threading.Event())
    s.name = "alder"
    s.dir = tmp_path
    s.handled = set()
    s.taken: list[str] = []
    s.deal = lambda deal_id, m1: s.taken.append(deal_id)  # type: ignore[method-assign]
    s.say = lambda line: None  # type: ignore[method-assign]
    for d in handled_deals:
        (tmp_path / "wire" / "maya").mkdir(parents=True, exist_ok=True)
        (tmp_path / "wire" / "maya" / f"{d}.s1.json").write_text("{}")
    return s


def _run_once(s) -> None:
    t = threading.Thread(target=s._deals, daemon=True)
    t.start()
    time.sleep(1.2)  # two polls of the 0.5 s loop
    s.w._stop.set()
    t.join(timeout=5)


def test_reopened_seller_skips_a_deal_it_already_answered(tmp_path):
    inbox = tmp_path / "wire" / "alder"
    inbox.mkdir(parents=True)
    (inbox / "deal-old.m1.json").write_text("{}")
    s = _seller(tmp_path, ["deal-old"])
    _run_once(s)
    assert s.taken == []


def test_seller_still_takes_a_new_deal_and_each_once(tmp_path):
    inbox = tmp_path / "wire" / "alder"
    inbox.mkdir(parents=True)
    (inbox / "deal-old.m1.json").write_text("{}")
    (inbox / "deal-new.m1.json").write_text("{}")
    s = _seller(tmp_path, ["deal-old"])
    _run_once(s)
    assert s.taken == ["deal-new"]
