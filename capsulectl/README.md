# capsulectl-book

`capsulectl-book`, the `capsulectl` plugin over evidence books: the book verbs
(`capsulectl book close`, `reconcile`, `request`, `respond`, `remote`,
`result`, `report`, `backfill`, `migrate`) and a book profile's log, which the
core hands to this plugin (`publish`, `cll`, `store init`, `bundle`). It is
this library's command-line face, a nested Go module,
`github.com/action-state-group/evidencebook/capsulectl`, with its own
releases.

It moved here from [capsule-cli](https://github.com/action-state-group/capsule-cli)
(capsule-cli#326); its history is capsule-cli's. The `cli-plugin/v1`
contract it implements is capsule-cli's
[docs/PLUGINS.md](https://github.com/action-state-group/capsule-cli/blob/main/docs/PLUGINS.md).

## Build and test

The plugin embeds the `capsulectl` core it builds against, and requires this
repository's library at a version, not the working tree: a change that needs
both lands as a library release first.

```sh
cd capsulectl
go build -o capsulectl-book ./cmd/capsulectl-book
capsulectl plugin install ./capsulectl-book
go test ./...
```

CI (`.github/workflows/capsulectl.yml`) also runs the Go and Python interop
round trip at the versions this module resolves, and a dry run of the release
build. Releases: [RELEASING.md](RELEASING.md).
