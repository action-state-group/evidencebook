### Added: `deal report --bundle` on an x-deal-v1 deal (#626, B4c)

- `deal report --deal ID --bundle FILE` writes your deal book's Evidence Bundle, for `capsulectl verify --bundle`. It covers every record of your book at the checkpoint the report rests on, with their payloads.
- The sealed report travels beside the book, in `x-deal-report/v0`, with `x-deal-v1` naming it and carrying the records its rows rely on, the other side's included.
- Every audience's bundle carries the same records. An adjudicator's lists no rows.
- The bundle is checked as a reader checks it before it is written.
- Making it writes nothing to your book: no disclosure record goes into an x-deal-v1 book.
- With `--share counterparty|adjudicator --to NAME`, the bundle is a disclose act, as for `--out`: checked to contain none of the plain values kept only on this device, then put on record (the records it carries and the digest of the exact file), then written.
- None of your plain values (your words, the counterparty's details) is in any bundle.
- `--out` and `--bundle` are one or the other.
- `deal export` on an x-deal-v1 deal points at `--bundle`.
- capsulectl-deal now needs capsule-cli v0.1.0-rc47, whose `verify --bundle` finds an x-deal-v1 deal's last step through its disclosed header (capsule-cli#605).
