### Added: `xdealv1.ChangesDetails` (#626)

- `ChangesDetails(Record)` exports the test the authorization applies to each journal record between a check and its act. It matches a `detail_change`; a `message` with a counterparty; an `evidence` with a counterparty or `counterparty_facts`; an `intent` that starts a leg or picks the item; and an `agent_move` that is `chose`.
- A writer holding a payment in flight runs it on each journal record it would seal, and refuses one that matches until the payer leg is sealed, so the payment already sent stays authorized. `CheckAuthorizations` uses the same function.
