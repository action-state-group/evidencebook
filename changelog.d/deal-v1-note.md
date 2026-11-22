### Added: `capsulectl deal note` on an x-deal-v1 deal (#626, B1b part 1)

- An x-deal-v1 deal keeps a local list of its plain steps, in the form an x-deal-v0 deal folds, and `deal note` runs the same note code over it. It seals the x-deal-v1 journal record of the same body: `intent`, `message`, `claim`, `evidence`, `change` (as `detail_change`), `disclosure`, and the agent moves (`searched`, `options`, `recommended`, `chose`, `couldnt`, as `agent_move`). Each record carries `deal_id` where the profile puts it.
- The first intent note opens the deal. `--input` is the intent, and the new `--opening FILE` gives the deal's type, an optional channel, who and recourse; the terms are the offer's, and the party role is the deal's own side.
- A step that stopped between its local event and its journal record is finished by the next read.
- Note kinds with no x-deal-v1 form refuse and name the verb that records them: `act`, `payment_received` and `delivered` (`deal perform`, `deal settle`), `dispute`, `ruling`, `acceptance` and `platform_approval`.
- The local list never leaves the store: only the journal's records, with their commitments, go out. The cross-deal counterparty memory reads x-deal-v1 deals' steps too.
- `deal check` and `deal note --kind approval` on an x-deal-v1 deal follow in part 2.
