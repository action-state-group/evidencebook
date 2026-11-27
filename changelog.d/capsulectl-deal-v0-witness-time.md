<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: an x-deal-v0 receipt page can say your book's witness time (#869)

- `capsulectl deal report --html --witness-directory DIR` on an x-deal-v0 deal puts on your own page the time a witness in `DIR` signed for the checkpoint your copy rests on, as `capsulectl deal verify` reported it, in capsulectl's voice (u219). The copy carries no witness evidence; a shared copy's page never gets the time, and `--witness-directory` with `--share` is refused. A chain witnessed only for an earlier checkpoint of the deal gives no time.
- `capsulectl deal checkpoint proof --copy COPY --out FILE` on an x-deal-v0 deal writes your book's witnessed cadence chain for that copy's checkpoint, and `capsulectl deal verify --bundle COPY --stamp-proof FILE --witness-directory DIR` reports the copy's `witnessed` from it (verified, failed or withheld), which never changes the verdict.
