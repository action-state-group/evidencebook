### Added: x-deal-v1 status value and `StatusProvider` (#461 step 5, PR 4c-2b-2)

- `xdealv1.BuildStatus(StatusBooks)` builds a v1 deal book's status value (design §8.1). Its contents:
  - the deal's inputs, with no shared identifier;
  - each row's rowstatus input, carrying only the statements invariant A keeps;
  - times as a string, `null` or `{"stamp": n}` (`StatusTime`);
  - when the other side first named each statement;
  - the effective ruling's `against` and the first remedy made;
  - the payment check's result, reason and excluded count;
  - one entry per dispute record;
  - `counterparty_proof_held`.
- `xdealv1.StatusProvider` is a `BookStatusProvider` that gives the profile, link value and status value at each stamp:
  - it reads the counterparty's counted prefix and the adjudicator's case book lock-free (`HeldByDeal`, `CountedPrefix`);
  - it runs the payment check when the host gives the offer's sent pair (`OfferPair`).
- `Shelf.HeldByDeal(ctx, dealID)` lists the received records of a deal, lock-free.
- `testdata/deal-v1/status/vectors.json` holds the cases `appendix-a`, `times-three-cases`, `unwitnessed-then-witnessed`, `carry-forward` and `carry-forward-lower-bound`. Each case holds the books, the status value they build, and the row results a reader resolves from the fact; those results equal the full books' results.
