### Added: x-deal-v1 reports whether a disputed row is decided by the disputed side's own word

- `xdealv1.DecideCase` reads a gathered case (`GatherCase`) for its row and reports one of two things. It reports `Decisive` when the side the dispute is raised against (the side that did not sign the dispute) has itself stated `not_met` on the row, in a record it signed, and has not stated otherwise since. Otherwise it reports `NeedsPerson`, with the reason. The decisive result names the deciding record (digest, record id, seq, record time and what timed it).
- Anything else needs a person:
  - no statement on the row, or a later statement other than `not_met`;
  - none of that side's records read;
  - a fork in its book;
  - a sealed ruling's `read` that does not reach that side's book.
- Free text, such as a perform record's `observed`, decides nothing, and neither does the other side's word. Given a sealed ruling's `read` for that side, the check reads only that far, so checking a sealed ruling again gives the answer it was sealed on.
- It only reports: the ruling is still sealed by `SealRuling`, and the ruling record does not carry the decision (no wire change).
- The rule is Ethan's (2026-11-25), with Sherlock2's tightenings (buzz 4ec88700 and 319f98a4).
- Rust and Python port it with `GatherCase` (#875, #876).
