- **When an x-deal-v1 deal has started** (evidencebook#461 step 5, PR 3a). `xdealv1.Start(StartInput)`
  computes `started`, `awaiting_agreement` or `not_started`, whether the result is local, and what
  started it (`offerer_in_time`, `acceptor_named`, `first_act`), from the values of design note §5.2: the
  two agreements' match, the offerer's agreement naming the acceptor's, its record time against
  `agree_by`, the acceptor's book naming it or a cancellation first, and, with a counterparty that
  runs no layer, the holder's first act. `testdata/deal-v1/start/vectors.json` holds 16 cases.
  `StartFromBooks`, which reads these values from the two books, follows.
