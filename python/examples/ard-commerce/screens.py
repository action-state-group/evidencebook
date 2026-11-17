# SPDX-License-Identifier: Apache-2.0
"""The words the audience reads, in one place so the terminal and the popup say the same thing.

Wording rules (design §8, Sherlock2 11-17): the screen says *assessment*, never *admission*; every
amount is msat from the invoice; a seller's history is shown, never scored, and never called lost,
failed or dishonest; the sample and demo labels are always there."""
from __future__ import annotations

from buyer import Assessment, Evidence

BANNER = ("DEMO · sample history, run by the demo operator · test nodes with real Lightning (Brattain pays, "
          "Shockley receives; both sellers settle to the one test node Shockley)")
BANNER_FAKE = "DEMO · sample history, run by the demo operator · SIMULATED payment: no money moves"


def banner(rail: str) -> str:
    return BANNER if rail == "lightning" else BANNER_FAKE
PACK_NOTE = "demo copy, not published · placeholder thresholds, for this demo only"
CLAIMS_NOTE = "claims-held is not read today: no card carries claims yet"
ASSESSMENT_NOTE = ("the agent's assessment of each seller; the presenter's pick decides who is paid, "
                   "and nothing is paid above a seller's limit")


def msat(n: int) -> str:
    return f"{n:,} msat"


def _totals(ev: Evidence) -> tuple[int, int]:
    """Deals counted and deals that did not hold, over every band of the card (its own ``of``)."""
    bands = ev.card["bands"]
    return sum(b["of"] for b in bands), sum(b["did_not_hold"] for b in bands)


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
        why = (f"; the pack's limit is {max_permille} in every 1,000 deals did not hold (placeholder)"
               if "band-history" in a.reasons else "")
        return f"LIMIT: deals under {msat(a.cap_msat)}; {msat(a.price_msat)} is outside it{why}"
    return f"{a.verdict}: see the assessment rows"


def limit_outcome(a: Assessment) -> str:
    """What a tap on a LIMIT seller shows: nothing opens and nothing is paid."""
    return (f"LIMIT: {msat(a.price_msat)} is over this seller's limit (deals under {msat(a.cap_msat or 0)}); "
            "no deal opens and nothing is paid")


def check_lines(ev: Evidence) -> list[str]:
    return [f"{'–' if c.stated else '✓' if c.ok else '✗'} {c.name}: {c.detail}" for c in ev.checks]
