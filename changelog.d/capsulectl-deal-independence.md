<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: a ruling's stamped time, and "not independent (shared operator)" (#819)

- `deal receive` of a ruling says when it was stamped (`stamped_at`): the earliest time a witness your agreement names registered the adjudicator's stamp, from the receipts the file carries, or "unknown (no receipt from a witness the agreement names)" when none does. It is never the ruling's own time, which is the adjudicator's word.
- When the adjudicator's key and a party's key are bound to one controller at that time, it says `"independence": "not independent (shared operator)"`. It never says a ruling is independent. The bindings checked are those the file carries and those you give with `--binding`, such as ones the adjudicator's operator publishes. A revocation counts only when you give it with `--revocation`: its time is one key's word, so it never comes from the adjudicator's file.
- `deal ruling` writes the adjudicator's witness receipts for the stamp into the file, and takes `--binding` to deliver bindings with it. Run again once the witness's receipt is back, it writes the file again with it.
