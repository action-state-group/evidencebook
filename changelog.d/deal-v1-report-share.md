### Added: shared copies of an x-deal-v1 report (#626, B4b part 2)

- `deal report --deal ID --share counterparty|adjudicator --to NAME --out FILE` writes a copy of the report for someone else. For an adjudicator's copy, `--to` is the referee it names.
- As for an x-deal-v0 copy, the copy is a disclose act:
  - its exact bytes are checked against the deal's plain values kept only on this device (your words, your limits, the counterparty's details);
  - it is put on record (the audience, the recipient and the digest of the exact file) before the file is written;
  - a refused copy writes and records nothing.
