- **A standing-intent act rests on a signed first intent** (evidencebook#500, Steven's option b).
  `xdealv1.CheckAuthorizations` returns, for an act a `standing_intent` approval authorizes, the record
  digest of the first intent it rests on (`ActAuthorization.Intent`). The caller refuses the act until the
  person's signature over that digest is held, as for the approval and the `confirm_limits` approvals.
  A `standing_intent` approval with no intent before it in the journal is refused. The approval-migration
  vector gains `intent_signature` per authorized act, and a refused case.
