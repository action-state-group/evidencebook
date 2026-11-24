### Changed: a placed deal's coverage line and findings name the deal book once (#797)

- `DealCoverage.Line` reads "deal/…, placed in window 2026-09, is answered over its whole book, …", in place of "deal deal/…, placed in window …, answered over its whole book, …", which named the deal twice. The coverage and chain findings for a placed deal read the same way: "deal/… of window 2026-09 has no answer", in place of "deal deal/… of window 2026-09 has no answer", and likewise for two answers, an unproven tree and a deal whose tree does not extend its close.
