### Added: x-deal-v1 reports whether a disputed row is decided by the disputed side's own word

- `xdealv1.DecideCase` reads a gathered case (`GatherCase`) for its row and reports one of two things. It reports `Decisive` when the side the dispute is raised against (the side that did not sign the dispute) has itself stated `not_met` on the row, in a record it signed, and has not stated otherwise since. Otherwise it reports `NeedsPerson`, with the reason. The decisive result names the deciding record (digest, record id, seq, record time and what timed it).
- Anything else needs a person:
  - no statement on the row, a later statement other than `not_met`, or a latest record stating two values on the row;
  - none of that side's records read;
  - a fork in its book, or the copy of its book the other side holds not read (GatherCase finds a fork against that copy);
  - a sealed ruling's `read` that does not reach that side's book.
- Free text, such as a perform record's `observed`, decides nothing, and neither does the other side's word. Given a sealed ruling's `read` for that side, the check reads only that far, so checking a sealed ruling again gives the answer it was sealed on.
- It only reports: the ruling is still sealed by `SealRuling`, and the ruling record does not carry the decision (no wire change).
- The rule is Ethan's (2026-11-25), with Sherlock2's tightenings (buzz 4ec88700 and 319f98a4).
- Rust and Python port it with `GatherCase` (#875, #876).
- `GatherCase` also reads, for each side, the records of that side the other side's opening copy carries. A record there that differs from the side's book at its seq, or two records at one seq, is a fork: it shows in `fork_at` and in the ruling's `sources`. A record past the book read is kept in the new `CaseSide.ShownPast`. Before this, a fork shown only in the other side's opening copy was missed (#944).
