# SPDX-License-Identifier: Apache-2.0
"""The words the audience reads, in one place so the terminal and the popup say the same thing.

Wording rules (design §8, Sherlock2 11-17): the screen says *assessment*, never *admission*; every
amount is msat from the invoice; a seller's history is shown, never scored, and never called lost,
failed or dishonest; the sample and demo labels are always there."""
from __future__ import annotations

from buyer import Assessment, Evidence

BANNER = ("DEMO · sample history, run by the demo operator · test nodes with real Lightning (the buyer's node pays, "
          "the sellers' node receives; both sellers settle to the one test node)")
BANNER_FAKE = "DEMO · sample history, run by the demo operator · SIMULATED payment: no money moves"


BANNER_LOCAL = ("DEMO · sample history, run by the demo operator · real Lightning (real money, mainnet) between two demo wallets "
                "(the buyer's wallet pays, the seller's wallet receives; both sellers settle to the one test wallet)")


def banner(rail: str) -> str:
    return {"lightning": BANNER, "local": BANNER_LOCAL}.get(rail, BANNER_FAKE)
PACK_NOTE = "demo copy, not published · placeholder thresholds, for this demo only"
CLAIMS_NOTE = "claims-held is not read today: no card carries claims yet"
ASSESSMENT_NOTE = ("the agent's assessment of each seller; the presenter's pick decides who is paid, "
                   "and nothing is paid above a seller's limit")


def fee_note(fee_msat: int | None, real: bool) -> str:
    """The fee on Maya's payment line: on a real rail it is the routing fee her own wallet reports for the
    send. It says nothing about what the seller's wallet was credited (Maya has no record of that)."""
    if fee_msat is None:
        return ""
    return f" (routing fee {fee_msat} msat, as her wallet reports it)" if real else f" (fee {fee_msat} msat)"


def seller_credit_note(price_msat: int) -> str:
    """The seller's receipt line on a real rail: the agreed price is in its records, the amount its wallet
    was credited is not (the wallet provider may keep a receiving fee)."""
    return (f"Agreed price {price_msat} msat. What this seller's wallet was credited is not in these records: "
            "its wallet provider may deduct a receiving fee.")


def msat(n: int) -> str:
    return f"{n:,} msat"


def _totals(ev: Evidence) -> tuple[int, int]:
    """Deals counted and deals that did not hold, over every band of the card (its own ``of``)."""
    outcomes = ev.card["outcomes"]  # the card's own totals
    return outcomes["of"], outcomes["did_not_hold"]


def card_line(ev: Evidence, sample_card: bool) -> str:
    """The card summary: deals counted and how many did not hold, with its label."""
    of, bad = _totals(ev)
    label = ("sample card, written for this demo; not recomputed from records" if sample_card
             else "sample history, run by the demo operator")
    if bad and not sample_card:  # the owner's own outcome_note: not confirmed by the buyer or by a ruling
        label += "; did not hold counted from the seller's own note, not confirmed by the buyer or a ruling"
    return f"{bad} of {of} deals did not hold ({label})"


def decision_line(a: Assessment, ev: Evidence, max_permille: int, ceiling_msat_for_tier: int | None = None) -> str:
    """One line: the decision and why, for this seller and this price."""
    if a.verdict == "GO":
        within = f" ({a.tier}, under {msat(ceiling_msat_for_tier)})" if ceiling_msat_for_tier else ""
        return f"GO: {msat(a.price_msat)} is within this pack's limit for it{within}"
    if a.verdict == "LIMIT" and a.cap_msat is not None:
        why = (f"; the pack lets at most {max_permille} of every 1,000 deals not hold (placeholder)"
               if "band-history" in a.reasons else "")
        return f"LIMIT: this history supports deals under {msat(a.cap_msat)}; the {msat(a.price_msat)} price is over that{why}"
    return f"{a.verdict}: see the assessment rows"


def limit_outcome(a: Assessment) -> str:
    """What a tap on a seller that is not GO shows: nothing opens and nothing is paid."""
    if a.verdict == "LIMIT" and a.cap_msat:
        return (f"LIMIT: {msat(a.price_msat)} is over this seller's limit (deals under {msat(a.cap_msat)}); "
                "no deal opens and nothing is paid")
    return f"{a.verdict}: no deal opens and nothing is paid"


def button(name: str, a: Assessment) -> str:
    """The popup button: it says what a tap does."""
    if a.verdict == "GO":
        return f"Pay {name} {msat(a.price_msat)}"
    if a.verdict == "LIMIT":
        return f"Pick {name} (over its limit: nothing is paid)"
    return f"Pick {name} ({a.verdict}: nothing is paid)"


def check_lines(ev: Evidence) -> list[str]:
    return [f"{'–' if c.stated else '✓' if c.ok else '✗'} {c.name}: {c.detail}" for c in ev.checks]
