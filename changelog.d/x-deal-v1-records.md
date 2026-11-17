- **x-deal-v1 records, Go first** (evidencebook#461, `docs/design/461-deal-profile.md` §2.3, §3; §11.1 PR 2a).
  New deals stay x-deal-v0 by default; x-deal-v0 is unchanged and stays readable.
  - **The package:** `dealprofile/xdealv1` defines the ten record types (propose, agree for an agreement,
    decline or cancellation, perform, settle, statement, dispute, ruling, ask, answer, admit), the journal
    overlay (intent, check with `payment_ref`, verdict, approval), `rules_verdict` and the journey overlay.
    The overlays reuse v0's bodies.
  - **Reading a record:** strict `Decode`, and `Encode`, `Check` and `Digest`.
  - **The record checks:** where each record is sealed (deal book, case book or journal), `deal_id` presence,
    `seen` on agreements, and the rows. `CheckAgreement` reads an agreement against its offer.
  - **The schema:** `dealprofile/x-deal-v1/x-deal-v1.schema.json`.
  - **Telling the versions apart:** `dealprofile.Version` says whether a record is x-deal-v0 or x-deal-v1, so a
    reader dispatches on it.
  - **Vector:** `testdata/deal-v1/records/deal-id-presence.json`.
