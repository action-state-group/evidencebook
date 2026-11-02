<!-- changelog: rust/evidencebook/CHANGELOG.md -->
### Changed: deal records accept rule_inputs_version 4 (#363)

- The x-deal-v0 schemas accept `rule_inputs_version` 3 or 4 (`enum [3, 4]`, was `const 3`); 4 is the rule inputs version in which a rule's `channel` comes from the latest counterparty message only. A positive fixture (`positive-rule-inputs-4`) seals a pay check under 4 (#363).
