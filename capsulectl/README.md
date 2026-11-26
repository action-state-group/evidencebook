# capsulectl-book and capsulectl-deal

`capsulectl-book`, the `capsulectl` plugin over evidence books: the book verbs
(`capsulectl book close`, `reconcile`, `request`, `respond`, `remote`,
`result`, `report`, `backfill`, `migrate`, `doctor`, `policy`, `bundle-diff`, `card`) and a book profile's log, which the
core hands to this plugin (`publish`, `cll`, `store init`, `bundle`). It is
this library's command-line face, a nested Go module,
`github.com/action-state-group/evidencebook/capsulectl`, with its own
releases.

The module also holds `capsulectl-deal` (`cmd/capsulectl-deal`, `internal/deal`,
`skills/deal`, `examples/`): the deal verbs (`capsulectl deal ...`) and a deal
profile's SQLite store, which `capsulectl verify` cannot check alone: the
core reports the x-deal-v1 report rule `NOT_CHECKED` until `capsulectl-deal` is
installed and runs it. The two are separate launchers built from one source
tree and released together under one tag, and neither links the other's
package, so each carries only its own commands, hooks and wording. It moved
here from [evidencebook-deal](https://github.com/action-state-group/evidencebook-deal)
(evidencebook#610) with its history; the deal changelog up to that move is
[CHANGELOG-deal.md](CHANGELOG-deal.md), and its earlier issue and PR numbers are evidencebook-deal’s.

capsulectl-book moved here from [capsule-cli](https://github.com/action-state-group/capsule-cli)
(capsule-cli#326); its history is capsule-cli's. The `cli-plugin/v1`
contract it implements is capsule-cli's
[docs/PLUGINS.md](https://github.com/action-state-group/capsule-cli/blob/main/docs/PLUGINS.md).

## Install a release

One release carries capsulectl. It holds the core and both plugin launchers at
one version `$V`, and it is tagged `capsulectl/$V` on this repository
([RELEASING.md](RELEASING.md)). The core in it is capsule-cli's own published
build, carried unchanged. The launchers are plugins that the core installs.
A release whose assets include `capsulectl-$V-$OS-$ARCH` carries the core.
Releases made before this change do not. For one of those, take the core of
the same version from
[capsule-cli](https://github.com/action-state-group/capsule-cli#prebuilt-binaries).

Each release has, for linux/amd64, linux/arm64 and darwin/arm64:

- `capsulectl-$V-$OS-$ARCH`, the core, with its own attestation bundle
  `capsulectl-$V.sigstore.json` (built by capsule-cli);
- `capsulectl-book-$V-$OS-$ARCH` and `capsulectl-deal-$V-$OS-$ARCH`, the two
  launchers;
- `standard-history-$V.tar.gz`, the History card's files, which `book card`
  pages read;
- `deal-view-$V.tar.gz`, the deal view's files;
- `deal-skill-$V.tar.gz`, the `skills/deal` tree, which is the one agent
  skill for both plugins' commands that an agent's host reads. The commands below do not need it, and
  [actionstate.ai/install](https://actionstate.ai/install) places it for an agent;
- one `SHA256SUMS` over every file;
- the plugins' attestation bundle, published under two names,
  `capsulectl-book-$V.sigstore.json` and `capsulectl-deal-$V.sigstore.json`.

Download the files, check them, then install:

```bash
# V: the release actionstate.ai/install names, e.g. v0.1.0-rcNN. P: linux-amd64, linux-arm64 or darwin-arm64.
REL=https://github.com/action-state-group/evidencebook/releases/download/capsulectl/$V
curl -fsSLO "$REL/capsulectl-$V-$P"
curl -fsSLO "$REL/capsulectl-book-$V-$P"
curl -fsSLO "$REL/capsulectl-deal-$V-$P"
curl -fsSLO "$REL/standard-history-$V.tar.gz"
curl -fsSLO "$REL/deal-view-$V.tar.gz"
curl -fsSLO "$REL/SHA256SUMS"
curl -fsSLO "$REL/capsulectl-$V.sigstore.json"
curl -fsSLO "$REL/capsulectl-book-$V.sigstore.json"
sha256sum --ignore-missing -c SHA256SUMS   # macOS: shasum -a 256 --ignore-missing -c SHA256SUMS
# Provenance, with no GitHub account (with --bundle). The core, built by capsule-cli:
gh attestation verify "capsulectl-$V-$P" --bundle "capsulectl-$V.sigstore.json" \
  --repo action-state-group/capsule-cli \
  --signer-workflow action-state-group/capsule-cli/.github/workflows/release.yml
# The plugins and archives, built here, each with the plugins' bundle:
gh attestation verify "capsulectl-book-$V-$P" --bundle "capsulectl-book-$V.sigstore.json" \
  --repo action-state-group/evidencebook \
  --signer-workflow action-state-group/evidencebook/.github/workflows/release.yml
# ...and the same for capsulectl-deal-$V-$P, standard-history-$V.tar.gz and deal-view-$V.tar.gz.
mkdir -p ~/.local/bin && install -m 0755 "capsulectl-$V-$P" ~/.local/bin/capsulectl
export PATH="$HOME/.local/bin:$PATH"   # add this line to ~/.bashrc or ~/.zshrc to keep it
capsulectl --version                    # capsulectl $V (commit <40-hex commit>)
chmod +x "capsulectl-book-$V-$P" "capsulectl-deal-$V-$P"
capsulectl plugin install "./capsulectl-book-$V-$P"
capsulectl plugin install "./capsulectl-deal-$V-$P"
# After the installs, which create the plugin folder, the archives unpack into it
# beside the launchers (standard-history-module/ and capsulectl-deal.d/):
tar -xzf "standard-history-$V.tar.gz" -C ~/.local/lib/capsulectl/plugins
tar -xzf "deal-view-$V.tar.gz" -C ~/.local/lib/capsulectl/plugins
```

`gh attestation` needs gh 2.49 or later (Ubuntu 24.04's packaged gh, 2.45,
has no `attestation` command). Without it, `SHA256SUMS` is the only check
(capsule-cli's README says which check applies).

`capsulectl plugin install` names a plugin by its handshake, not by the file
name, checks that it is built for the installed core, and refuses a mismatch.
`capsulectl plugin ls` then lists both plugins. A core up to
`v0.1.0-rc53` also lists `capsulectl-deal.d` under `refused` ("not a regular
file"): that is the deal view's folder, not a plugin, and nothing is wrong
(capsule-cli#635).

## The History card page

The plugin offers capsule-viewer's History card module
(`capsuleviewer.standard-history/v0`) to `capsulectl presentation list|build`
in its handshake. The release's `standard-history-VERSION.tar.gz` holds the
module's files; unpack it beside the launcher, and a standard_history/1 bundle
resolves to the module. Where the files came from is in
`internal/book/assets/standard-history/SOURCE.txt`; a release of capsule-viewer
is re-vendored by copying its `standard-history-module` bundle there.

## Conduct packs

A conduct pack is private to the book's owner. Any pack in `packs/*/pack-source.yaml` of the current
directory with `scope.kind: conduct` is private with no further step, and its `pack_id` may also be
listed (no `@version`) on its own line in `<profile database>/book/conduct-packs`. `bundle`,
`disclose`, `permalink` and a Close's `--bundle-out` then refuse a record that cites such a pack, in
its `subject_ref` or a `contract_ref` in its statement, whatever the record's type. `book respond` treats such a record as it does an unknown id. Run from a directory with no `packs/`, the verb warns on stderr that no workspace conduct packs were read. The list is local policy and is written into no record.

## Agent skill

[skills/deal](skills/deal/SKILL.md) is the one agent skill for both plugins.
It teaches five verbs, check, record, ask, verify and show, in one table that
maps each to the existing `deal` or `book` command; the book commands (`request`,
`respond`, `reconcile`, `result build`, `report derive`, `report build`) are in
[references/book.md](skills/deal/references/book.md) in full, with `close`,
`result derived-inputs` and the window commands as expert commands there.
`holder-page` writes the holder's own sharing pages, and `card register` and `card check` register a History card on a service the holder names and check `card_registered`; neither is in the skill yet.
It extends capsule-cli's `capsulectl` skill, which keeps the core verbs. It is
written by hand: capsule-cli's `skillgen` has `capsulectl`'s own sections built
in, so `skills/deal/scripts/check-book-verbs.sh` keeps the book commands it
names to the plugin's, and `skills/deal/scripts/run-book-demo.sh` runs them
against `capsulectl` and a `capsulectl-book` built here; CI runs both.

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

`backfill` is documented in [BACKFILL.md](BACKFILL.md). CI
(`.github/workflows/capsulectl.yml`) also runs the Go and Python interop
round trip at the versions this module resolves, and a dry run of the release
build. Releases: [RELEASING.md](RELEASING.md).
