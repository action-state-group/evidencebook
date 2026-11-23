### Added: an adjudicator's case rules in `xdealv1` (#779)

- `xdealv1.CheckCase` reads a dispute and one or both parties' adjudicator copies as a case for one adjudicator, and returns the deal, the row, both sides' keys, what was read of each side's book, the agreement, the forum, the rules digest and the case log. It checks:
  - the dispute is signed by a party of the deal and names the adjudicator;
  - each copy is an adjudicator copy whose agreement names the adjudicator;
  - the copies are one per side, of one offer;
  - with one copy, the other side's key is that agreement's `counterparty_key`.
- `xdealv1.CheckCaseRecord` reads a later withdrawal or challenge of a case. A challenge must name a ruling the case book holds.
- `xdealv1.SealRuling` seals a ruling in the case book once: the same body again is the existing record, and a different final ruling on a row is refused (`ErrRulingConflict`).
- capsulectl-deal's `deal ruling` and a hosted adjudicator (ledger-cloud#35) call these functions, so both apply the same rules. Rust and Python: #774, #775.
