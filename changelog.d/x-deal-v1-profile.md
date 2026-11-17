- **x-deal-v1's normative text and fixtures** (evidencebook#461 step 5, PR 2d). `dealprofile/x-deal-v1/PROFILE.md`
  states the record rules the `xdealv1` checks apply: the block, where `deal_id` is present, the record
  types and where each is sealed, the bodies, `seen`, records sent whole, the authorization of acts, and
  the stages a refusal names. `dealprofile/x-deal-v1/fixtures/` holds the integrated design's Appendix A
  deal record by record (both journals, both deal books, the adjudicator's case book, the path settled
  without a ruling, and a declined and a cancelled offer), each with its record digest, and 45 negatives,
  each one positive record with one change and the check and stage that refuse it. `TestFixtures` builds
  and checks them (`EB_WRITE_DEAL_V1_FIXTURES=1` writes them). The Python reference checker for v1 follows.
