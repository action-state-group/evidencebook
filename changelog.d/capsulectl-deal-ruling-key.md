<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: an adjudicator's profile key (#814)

- `deal ruling`'s help used to say to switch an existing profile's checkpoint key to the record key with `profile update`. That left a store that had already checkpointed unusable, behind a generic "operation failed". The help, and the refusal when the keys differ, now say to make the adjudicator's profile with one key for both, before it checkpoints anything: `capsulectl --profile NAME deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY`.
- A profile whose store already checkpointed with another key is refused before anything is sealed, with that advice.
