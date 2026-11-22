### Added: shared copies of an x-deal-v1 report (#626, B4b part 2)

- `deal report --deal ID --share counterparty|adjudicator --to NAME --out FILE` writes a copy of the report for someone else.
- An adjudicator's copy:
  - is only for a deal whose agreement names an adjudicator;
  - names `--to` as its referee, and its record names the agreement's adjudicator by key;
  - lists no rows, and carries no agent claims (an x-deal-v1 report has no claim fields).
- As for an x-deal-v0 copy, the copy is a disclose act:
  1. its exact bytes are checked to contain none of the plain values kept only on this device (your words, your limits, the counterparty's details);
  2. `--out`'s name is reserved, and the bytes are written to a temporary file beside it;
  3. it is put on record: the audience, the recipient and the digest of the exact file;
  4. only then is it moved into place.
- A refused copy writes and records nothing, and so does an existing `--out` or a folder that cannot be written to. If the final move fails after the copy is put on record, the error says so.
