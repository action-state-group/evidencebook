- **x-deal-v1 offers, declines and cancellations travel whole** (evidencebook#461,
  `docs/design/461-deal-profile.md` §3.1, §3.5; §11.1 PR 2b).
  - `xdealv1.Pair` builds the pair a sent record travels as from its standalone Capsule, envelope and
    record: `{"record": <the Capsule with its signature and key_id>, "agent_input": <the record>}`.
  - `xdealv1.VerifySent` checks such a pair on its own:
    - the Capsule and its signature, and that it is a standalone `"<deal_id>/sent"` fyi Capsule;
    - that `agent_input` is the record it commits;
    - that the record is a valid offer, decline or cancellation.
  - Given the offer, a decline must answer it and be signed by the key the offer was sent to, and a
    cancellation by the offer's signer.
  - Vectors: `testdata/deal-v1/decline/vectors.json`.
