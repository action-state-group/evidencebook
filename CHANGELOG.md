# Changelog

## Unreleased

### Security

- `Respond` now fails closed. Protected subjects (everything except
  `checkpoints`) are answered only to a requester the transport has
  authenticated, passed in the new `RespondOptions.AuthenticatedRequester`.
  An unauthenticated caller gets only public subjects; every other request is
  answered with a signed refusal, reason `not_authorized`. This holds under
  every `history_segments` tier, including `peers`.
- A declared requester id is no longer trusted. `RespondOptions.RequesterID`
  is deprecated and ignored: it selects no relationship and grants no scope.
  Counterparty scope comes only from an authenticated identity that matches a
  `counterparty_ref`; a `counterparty_ref` value alone grants nothing.
- Requester ids are no longer stored in answerable records. The
  `evidence_request_answered` record no longer carries `requester_ref` (the
  field is removed from `AnsweredStatement`), in any form. It still records the
  request digest, subject kind, outcome, reason, relationship class and anchor.

### Breaking

- Callers that relied on `RequesterID` to unlock answers must now
  authenticate the requester at the transport and set
  `AuthenticatedRequester`. Without that change, those callers receive
  `not_authorized` for every subject except `checkpoints`.
- Records sealed before this change are immutable and still contain the
  `requester_ref` they were written with. Operators of books with such
  history should take that into account before serving `full_history` or
  `range` subjects that cover it.
