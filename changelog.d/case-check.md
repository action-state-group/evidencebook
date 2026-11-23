### Added: an adjudicator's case rules in `xdealv1` (#779)

- `xdealv1.CheckCase` reads a dispute and one or both parties' adjudicator copies as a case for one adjudicator. It returns the deal, the row, both sides' keys, what was read of each side's book, the agreement, the forum, the rules digest and the case log. It checks:
  - the dispute is signed by a party of the deal and names the adjudicator;
  - each copy is an adjudicator copy whose agreement names the adjudicator;
  - the copies are one per side, of one offer;
  - with two copies, each agreement names the other copy's signer, and both name the same adjudicator terms and forums;
  - with one copy, the other side's key is that agreement's `counterparty_key`;
  - the dispute is read against its own agreement when that side's copy is given, and otherwise its forum against the agreement's forums.
- `xdealv1.CheckCaseRecord` reads a later withdrawal or challenge of a case. A withdrawal must be of the case's row, and a challenge must name a ruling the case book holds.
- `xdealv1.SealRuling` seals a ruling once. The body must be the case's: its row, keys, read and rules digest. The same body again is the existing record. Once a row has a final ruling, it takes no other ruling (`ErrRulingConflict`): the adjudicator never replaces a final ruling, and a challenge of it records disagreement.
- capsulectl-deal's `deal ruling` and a hosted adjudicator call these functions, so both apply the same rules. Rust and Python: #774, #775.
