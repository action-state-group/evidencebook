# window-store

A windowed deal store (`deal init --window month`) with two ended calendar months and one
deal placed in each: 2026-09 (agreed 2026-09-14T15:00:00Z) and 2026-10 (agreed
2026-10-12T15:00:00Z), rolled at 2026-11-02T09:00:00Z. Made for evidencebook#852, so a test
outside Go can run `deal window chain`, `deal window respond`, `book report derive --chain`
and `book report coverage` on released binaries: capsulectl has no clock flag, and a store
made in real time has only its open window.

- Generator: `TestWriteWindowStoreFixture` in `capsulectl/internal/deal/deal_window_store_gen_test.go`,
  which sets `cli.DealClock` and runs the commands in process.
- Command, from `capsulectl/`: `EB_WRITE_WINDOW_STORE=1 go test -run TestWriteWindowStoreFixture ./internal/deal`
- capsulectl: this module (`capsulectl/go.mod`) at the commit that last changed this directory (`git log -1 -- <this directory>`), against capsule-cli `v0.1.0-rc53`.
- Files: `deal.yaml` is the deal profile with the store directory written as `@STORE@`;
  `store/` holds the SQLite store and its seeds. The seeds are made for this fixture and protect
  nothing.
- Use a copy: `deal window chain` and `deal window respond` write to the store. Copy `store/`
  to a new directory, replace `@STORE@` in `deal.yaml` with it, and save the result as
  `$XDG_CONFIG_HOME/capsule/profiles/deal.yaml`.
- Fetch at a tag: `git archive <tag> capsulectl/internal/deal/testdata/deal/window-store | tar -x`
  (a clone of action-state-group/evidencebook; the module's own tags are `capsulectl/v<version>`).
- The test that uses it: `capsulectl/cmd/window_store_cli_test.go`.
