- **Fixed (`dealprofile/xdealv1`):** a pair and what it signs are read by their exact member names (evidencebook#559). Go's reader also took a member in another case, including U+017F for `s` and U+212A for `k`, with a later spelling replacing an earlier one. So a pair, its signed Capsule or its signed header could carry a second, unverified spelling that Go read in place of the one an exact or schema-checking reader takes.
  - `VerifySent`, `VerifyBook` and `VerifyReport` refuse a pair whose members are not exactly its own.
  - They read a Capsule's `action_id`, `action_type`, `references` and `agent_input_digest` by exact name.
  - `VerifyBook` refuses a header (or a link, correlation or reproduction in it) carrying a member its type does not have exactly, as `schemas/record-header.schema.json` does.
  - `PaymentCheck` reads a settlement member's `leg`, `sealer_role` and `terms_ref` by exact name.

  The vectors are in `testdata/deal-v1/pair-names/` and `testdata/deal-v1/payment/`.
