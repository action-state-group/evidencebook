# SPDX-License-Identifier: Apache-2.0
"""The parts of the ARD demo that need no evaluator and no network: the pack edit, the screen
wording, the wallet rail's refusals and the one-way scrub of secret material."""
from __future__ import annotations

import json
import os

import pytest

import pack as packs
import pay
import screens
from buyer import Assessment, Check, Evidence

#: The demo pack lives in evidencebook-skills (packs/ard-demo-buyer); these tests read it from the ARD_SKILLS checkout.
with_skills = pytest.mark.skipif(not os.environ.get("ARD_SKILLS"), reason="ARD_SKILLS names the evidencebook-skills checkout that holds the demo pack")


def assessment(verdict: str, tier: str, price: int, cap: int | None = None, reasons=()) -> Assessment:
    return Assessment("kestrel", price, verdict, tier, cap, "demo/ard_admission/1.1.0", "ab" * 32, list(reasons), [], "r", True, "cd" * 32)


def card(held: int, bad: int) -> Evidence:
    ev = Evidence("kestrel")
    ev.card = {"outcomes": {"of": held + bad, "did_not_hold": bad}, "bands": [{"currency": "BTC", "decade": -9, "held": held, "did_not_hold": bad, "undecided": 0, "of": held + bad}]}
    return ev


@with_skills
def test_edit_changes_threshold_version_and_id_suffix_only():
    source = packs.source_path().read_text()
    out = packs.edited(source, 50, "1.1.0")
    assert packs.threshold(source) == 100 and packs.threshold(out) == 50
    assert "pack_id: demo/ard_admission/1.1.0" in out and "pack_version: 1.1.0" in out
    changed = [(a, b) for a, b in zip(source.splitlines(), out.splitlines()) if a != b]
    assert len(changed) == 3, changed


def test_threshold_refuses_a_pack_without_the_row():
    with pytest.raises(packs.PackError):
        packs.threshold("pack_id: demo/x/1.0.0\n")


@with_skills
def test_the_pack_is_a_demo_fork_that_reads_no_claims():
    source = packs.source_path().read_text()
    assert "pack_id: demo/ard_admission/1.0.0" in source and "asg/" not in source.split("rows:")[0].replace("evidencebook-skills", "")
    assert "claims-held" not in source.split("rows:")[1]
    assert "card.band_history_or_stake" in source and "BTC: {band: sat, scale: 8}" in source


def test_screen_lines_say_assessment_and_never_score_or_blame():
    go = screens.decision_line(assessment("GO", "T2", 289), card(48, 0), 100, 1000)
    limit = screens.decision_line(assessment("LIMIT", "T1", 268, 100, ["band-history"]), card(45, 3), 50)
    assert go == "GO: 289 msat is within this pack's limit for it (T2, under 1,000 msat)"
    assert limit == "LIMIT: this history supports deals under 100 msat; the 268 msat price is over that; the pack lets at most 50 of every 1,000 deals not hold (placeholder)"
    text = " ".join([go, limit, screens.card_line(card(45, 3), True), screens.card_line(card(48, 0), False), screens.BANNER,
                    screens.ASSESSMENT_NOTE, screens.PACK_NOTE]).lower()
    for word in ("lost", "failed", "dishonest", "usd", "$"):
        assert word not in text
    assert "3 of 48 deals did not hold (sample card, written for this demo; not recomputed from records)" in text
    assert "did not hold counted from the seller's own note" in screens.card_line(card(45, 3), False)
    assert "sample" in text and "test nodes" in text


def test_scrub_drops_preimages_at_any_depth():
    out = pay.scrub({"status": "ok", "payment": {"preimage": "aa", "payment_preimage_hex": "bb", "hash": "cc"}, "l": [{"Preimage": "dd", "n": 1}]})
    assert json.dumps(out) == '{"status": "ok", "payment": {"hash": "cc"}, "l": [{"n": 1}]}'


class Scripted(pay.LightningRail):
    def __init__(self, fund, send):
        self.fund, self.send, self.calls = fund, send, []

    def _control(self, host, payload, timeout=90):
        self.calls.append((host, payload["command"]))
        return scrub_copy(self.fund if payload["command"] == "fund" else self.send)


def scrub_copy(d):
    return pay.scrub(json.loads(json.dumps(d)))


INVOICE = "lnbc2890p1pnqexample"
OTHER = "lnbc2900p1pnqexample"


def test_the_rail_pays_the_invoice_amount_and_makes_only_fund_and_send_calls():
    rail = Scripted({"invoice": INVOICE, "amount_msat": 289}, {"status": "succeeded", "amount_msat": 289, "fee_msat": 0, "payment_hash": "ph", "preimage": "secret"})
    p = rail.pay(289)
    assert (p.invoice_msat, p.reference, p.status) == (289, "ph", "succeeded")
    assert "secret" not in json.dumps(p.raw)
    assert rail.calls == [(pay.LightningRail.SELLER, "fund"), (pay.LightningRail.BUYER, "send")]


@pytest.mark.parametrize("fund,send,why", [
    ({"note": "no invoice"}, {}, "no invoice"),
    ({"invoice": OTHER, "amount_msat": 289}, {}, "not 289"),
    ({"invoice": "lnbc1pnqnoamount", "amount_msat": 289}, {}, "None msat"),
    ({"invoice": INVOICE, "amount_msat": 289}, {"status": "failed"}, "did not succeed"),
    ({"invoice": INVOICE, "amount_msat": 289}, {"status": "succeeded", "amount_msat": 290}, "not 289"),
])
def test_the_rail_refuses_a_wrong_invoice_or_payment(fund, send, why):
    with pytest.raises(pay.RailError, match=why):
        Scripted(fund, send).pay(289)


def test_a_refused_invoice_is_never_paid():
    rail = Scripted({"invoice": OTHER, "amount_msat": 289}, {"status": "succeeded"})
    with pytest.raises(pay.RailError):
        rail.pay(289)
    assert [c for _, c in rail.calls] == ["fund"]


def test_fake_rail_moves_no_money():
    rail = pay.FakeRail()
    assert rail.pay(289).status == "succeeded" and rail.paid == [289]


def test_invoice_amounts_are_read_from_the_invoice_text():
    assert pay.invoice_msat("lnbc2890p1pabc") == 289 and pay.invoice_msat("lnbc2680p1pabc") == 268
    assert pay.invoice_msat("lnbc10n1pabc") == 1000 and pay.invoice_msat("lnbc1pabc") is None
    assert pay.invoice_msat("lnbc2895p1pabc") is None  # not a whole msat


def test_a_failed_wallet_call_never_echoes_secret_looking_output(monkeypatch):
    class Run:
        returncode, stdout, stderr = 1, "", "boom " + "ab" * 32

    monkeypatch.setattr(pay.subprocess, "run", lambda *a, **k: Run())
    with pytest.raises(pay.RailError) as e:
        pay.LightningRail()._control("h", {"command": "balance"})
    assert "ab" * 32 not in str(e.value) and "<hex>" in str(e.value)


def test_an_entry_whose_commitment_failed_is_a_failed_check_not_a_crash():
    import buyer
    ev = buyer.gather(None, None, {"id": "x", "problem": "commitment: bad signature"}, "aa")
    assert [(c.name, c.ok) for c in ev.checks] == [("commitment", False)] and ev.card is None


def test_limit_tap_line_is_english_and_pays_nothing():
    line = screens.limit_outcome(assessment("LIMIT", "T1", 268, 100, ["band-history"]))
    assert line == "LIMIT: 268 msat is over this seller's limit (deals under 100 msat); no deal opens and nothing is paid"
    assert line.isascii()


def test_card_line_uses_the_cards_own_total_and_a_stated_check_is_not_a_tick():
    ev = card(40, 3)
    ev.card["bands"][0].update(undecided=5, of=48)
    ev.card["outcomes"].update(of=48)
    assert screens.card_line(ev, False).startswith("3 of 48 deals did not hold")
    ev.stated("selection", "the seller states 0 unanswered disputes")
    assert screens.check_lines(ev)[-1].startswith("– selection")


def test_a_non_limit_seller_that_is_not_go_never_prints_a_zero_cap():
    assert screens.limit_outcome(assessment("ASK", "T0", 268)) == "ASK: no deal opens and nothing is paid"


def test_popup_buttons_say_what_a_tap_does():
    assert screens.button("Alder", assessment("GO", "T2", 289)) == "Alder".join(["Pay ", " 289 msat"])
    assert "over its limit: nothing is paid" in screens.button("Kestrel", assessment("LIMIT", "T1", 268, 100))
    assert screens.button("Kestrel", assessment("ASK", "T0", 268)) == "Pick Kestrel (ASK: nothing is paid)"


def test_the_owners_note_makes_a_real_card_count_did_not_hold(tmp_path):
    import deal_records as dr
    import deals
    from demo_node import DemoNode
    buyer, seller = DemoNode.open(tmp_path / "b", operator="b"), DemoNode.open(tmp_path / "s", operator="s")
    try:
        deals.admit(buyer, seller)
        deals.admit(seller, buyer)
        ids = [deals.run_deal(buyer, seller, dr.Terms("badges", 268, "BTC", "lightning", False), "x", deals.SAMPLE_PAYMENT) for _ in range(4)]
        for deal_id in ids[:3]:
            deals.note_did_not_hold(seller, deal_id, "other")
        seller.tick(__import__("datetime").datetime.now(__import__("datetime").timezone.utc))
        card = json.loads(seller.standard_history()["card"])
        assert (card["outcomes"]["held"], card["outcomes"]["did_not_hold"]) == (1, 3)
        with pytest.raises(ValueError):
            deals.note_did_not_hold(seller, ids[3], "made up")
    finally:
        buyer.close()
        seller.close()


def test_a_skills_checkout_without_the_demo_pack_is_refused(tmp_path, monkeypatch):
    (tmp_path / "scripts").mkdir()
    (tmp_path / "scripts" / "pack_compile.py").write_text("")
    monkeypatch.setenv("ARD_SKILLS", str(tmp_path))
    with pytest.raises(packs.PackError, match="ard-demo-buyer"):
        packs.source_path()
