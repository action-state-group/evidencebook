<!-- changelog: rust/evidencebook/CHANGELOG.md -->
- `respond::respond` answers `standard_history/1` over `full_history` when `Responder::standard_history` holds a
  `standard_history::StandardHistorySource` (holder kind and the host's inputs), byte for byte with Go's
  `StandardHistorySource` (evidencebook#328): to any authenticated requester and never a stranger; without a source it is
  `derivation_unsupported`. `standard_history::derive_card` ports SH §4's card derivation (the cards in
  `dealprofile/testdata/parity-vectors.json` are derived byte for byte); the card is not checked against its own inputs
  afterwards, as the crate has no card checker. `staple::staple_standard_history` builds a `standard_history/1` staple.
- Breaking: `Responder` has a new field `standard_history` and `answer::Options` a new field `standard_card`; set them to
  `None`.
