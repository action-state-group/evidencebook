### Added

- Package `deal` completes the Integrated Design's Deal API (#899 phase 3):
  - `Store.Check` (deal.check) seals the caller's check and verdict. It evaluates nothing, refuses a verdict with no rules block, and treats a verdict's rules members as the caller's claim.
  - `Store.SealJournal` and `Store.Steps` are the one writer and reader of a deal's journal records and local step list. Each record is held to the profile's checks and the caller's local values, and to x-deal-v1's authorization walk, before it is sealed. While a payment is in flight, a record that changes the deal's details is refused (`CodeInFlight`).
  - `Store.SendPayment` and `Store.Settle` with a check seal a payer leg: the send opens before the rail, and the leg is sealed when the rail answers. `Store.SendOf`, `Store.CheckRecord` and `Store.CloseSend` support them.
  - The package doc and docs/GUIDE.md index every call against the design's names, including `book.card`, `book.ask` and `book.answer`, which live on the shelf. A runnable Example opens a deal and seals an act with this module alone.
- `dealprofile.CheckText` runs the personal-data and wording scans over a record of any profile. `xdealv1.CheckJournal` runs the authorization walk on its own.

### Changed

- `xdealv1.CheckActs`: an act under a standing intent whose verdict carries no rules block now needs the person's signature over its approval digest (`ActAuthorization.RulesNotRecorded`; Sherlock2's ruling u234, ops evidence-network/inbox.md:669, 2026-11-25). capsulectl's verdicts always carry a rules block, so none of them is affected.
