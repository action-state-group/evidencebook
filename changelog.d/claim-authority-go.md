- `dealprofile`: a seller's verdict on its agent's claim moves off the claim into a new x-deal-v0
  record, `claim_authority` (`TypeClaimAuthority`, `ClaimAuthorityBody`), sealed right after it,
  so a counterparty copy can carry the claim's words without saying whether the claim was
  authorized (evidencebook#607). `CheckChain` holds section 6 rule 12 on
  the new record, compares the check's `valid_until` with the claim's time, accepts a deal that
  ends at a claim whose verdict is not sealed yet, keeps the claims sealed before (verdict on the
  claim) valid, and refuses such a claim once the deal holds a `claim_authority`. PROFILE.md rule
  12 says what each copy carries. A checker older than this refuses a book holding a
  `claim_authority`. Copies already shared still carry the verdicts they carried. New fixtures in
  `x-deal-v0/fixtures/positive-typed-seller-represent/` and `testdata/chain-vectors.json`.
  Known limit, accepted by Steven: a copy never carries the verdict, but a withheld record keeps
  its place in the log, so counting the withheld records before a claim can suggest, never prove,
  that the claim had no check.
