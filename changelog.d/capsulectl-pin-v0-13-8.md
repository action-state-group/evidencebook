### Changed: `capsulectl-book` requires `evidencebook` v0.13.8 (#418)

- The conduct refusal now covers every record type, not only a Result: `bundle`, `disclose`, `permalink` and a Close's `--bundle-out` refuse an observation or an evidence_request that cites a conduct pack. `book respond` hands the pack list to the book, so a record that cites one is refused as an unknown id is.
