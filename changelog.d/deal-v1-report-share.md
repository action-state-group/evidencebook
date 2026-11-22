### Added: shared copies of an x-deal-v1 report (#626, B4b part 2)

- `deal report --deal ID --share counterparty|adjudicator --to NAME --out FILE` writes a copy of the report for someone else.
- An adjudicator's copy:
  - is only for a deal whose agreement names an adjudicator;
  - names `--to` as its referee, and its record names the agreement's adjudicator by key;
  - lists no rows, and carries no agent claims (an x-deal-v1 report has no claim fields).
- As for an x-deal-v0 copy, the copy is a disclose act:
  1. its exact bytes are checked to contain none of the plain values kept only on this device (your words, your limits, the counterparty's details);
  2. they are written to a temporary file beside `--out`;
  3. it is put on record: the audience, the recipient and the digest of the exact file;
  4. only then is it linked into place.
- A refused copy writes and records nothing. If a file appears at `--out` after the copy is put on record, nothing is overwritten, and the error says the copy is on record but was not written.
