<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: an adjudicator's profile key (#814)

- `deal ruling`'s help used to say to switch an existing profile's checkpoint key to the record key with `profile update`. A profile's store has checkpointed from the start, so after that switch the stamp failed, behind a generic "operation failed". The help and the refusal now say to make the adjudicator's profile with one key for both: `capsulectl --profile NAME deal init --dir DIR --signing-key-file KEY --checkpoint-key-file KEY`. A profile that has checkpointed with another key cannot switch: make a new one in a new `--dir`, and stop sealing with the old one (one key, one live store).
- A profile whose store checkpointed with another key is refused before anything is sealed, with that advice.
- If you followed the old help: switch the profile's checkpoint key back to its earlier file (`capsulectl --profile NAME profile update --checkpoint-signing-key-file DIR/checkpoint.seed`), and the store works again. A ruling the failed run sealed stays in that store, unstamped and never sent: make the ruling again from the new profile.
