- **x-deal-v1 acts are authorized as v0's were** (evidencebook#461, `docs/design/461-deal-profile.md` §3.2,
  invariant D; §11.1 PR 2c). `xdealv1.CheckAuthorizations(journal, acts)` holds a deal-book perform or settle to
  v0's rules 5 and 6, through the check it cites:
  - the check has exactly one verdict, the verdict's first approval proceeds, and a deny authorizes nothing;
  - a standing intent answers only a passing verdict, for an action in the allowed in force;
  - one approval authorizes at most one act;
  - a change of identifiers between the check and the act needs a new check.

  Each act's `ApprovalDigest` is defined and binds the check and the card that was answered, and
  `NeedsPersonSignature` marks a person's own approval, which the agent's record key never stands for.
  Vectors: `testdata/deal-v1/authorization/approval-migration.json`.
