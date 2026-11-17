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
ASSESSMENT_NOTE = "the agent's assessment of each seller; the presenter's pick decides who is paid"


def msat(n: int) -> str:
    return f"{n:,} msat"


def card_line(ev: Evidence, sample_card: bool) -> str:
    """The card summary: deals counted and how many did not hold, with its label."""
    band = ev.card["bands"][0]
    decided = band["held"] + band["did_not_hold"]
    label = ("sample card, written for this demo; not recomputed from records" if sample_card
             else "sample history, run by the demo operator")
    return f"{band['did_not_hold']} of {decided} deals did not hold ({label})"


def permille(ev: Evidence) -> float:
    band = ev.card["bands"][0]
    decided = band["held"] + band["did_not_hold"]
    return 1000 * band["did_not_hold"] / decided if decided else 0.0


def decision_line(a: Assessment, ev: Evidence, max_permille: int, ceiling_msat_for_tier: int | None = None) -> str:
    """One line: the decision and why, for this seller and this price."""
    if a.verdict == "GO":
        return f"GO: {msat(a.price_msat)} is within this pack's limit for it ({a.tier}, under {msat(ceiling_msat_for_tier or 0)})"
    if a.verdict == "LIMIT" and a.cap_msat is not None:
        why = (f"; more than {max_permille} per thousand did not hold (placeholder)"
               if "band-history" in a.reasons else "")
        return f"LIMIT: deals under {msat(a.cap_msat)}; {msat(a.price_msat)} is outside it{why}"
    return f"{a.verdict}: {', '.join(a.reasons) or 'see the rows'}"


def check_lines(ev: Evidence) -> list[str]:
    return [f"{'✓' if c.ok else '✗'} {c.name}: {c.detail}" for c in ev.checks]
