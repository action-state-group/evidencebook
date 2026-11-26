# CLAUDE.md

Guidance for coding agents working on this repository.

## Commands

**Before every push, run `scripts/pre-push.sh`** (evidencebook#925). Pull
requests run only fast checks in CI; this runs every suite the branch's
changes reach (`--all`: every suite), each suite's plain tests in full and the
race detector over the packages the change reaches (evidencebook#973): the
plan `.github/scripts/race-packages.sh` makes for a pull request, plus
`capsulectl/internal/deal` in parallel shards when the change reaches it,
which no pull request runs in CI. `--plan` prints what it would run. Push to
main and the nightly race every package. It prints each suite's time and
exits nonzero if any failed; push only when it passes. `--all` took 24 minutes on an Apple M5 Pro (18 cores, 2026-11-25, other
work running): capsulectl 21 minutes in nine parallel shards per pass, the
root module 8 minutes more when its test cache is cold. The jobs that need a pinned
Chrome or capsule-engine (deal pages, presentation pass, the Goose loop) run
on main and the nightly; run them as listed below when you change what they
cover.

```bash
# Go (repository root; the module github.com/action-state-group/evidencebook, Go 1.27)
test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum
go vet ./...
go test ./...
go test -race ./...

# report (report/, a nested module; CI: .github/workflows/report.yml)
(cd report && test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum)
(cd report && go vet ./... && go test ./... && go test -race ./...)

# capsulectl-book and capsulectl-deal (capsulectl/, a nested module; CI: .github/workflows/capsulectl.yml)
(cd capsulectl && test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum)
(cd capsulectl && go vet ./... && go test ./...)

# Rust (rust/, a workspace: the crate evidencebook and the unpublished
# interop programs evidencebook-interop; CI: .github/workflows/rust.yml)
(cd rust && evidencebook/scripts/check-vocabulary.sh)   # the crate carries no deployment vocabulary
(cd rust && cargo fmt --all -- --check)
(cd rust && cargo clippy --locked --all-targets --all-features -- -D warnings)
(cd rust && cargo test --locked --all-features)
(cd rust && RUSTDOCFLAGS="-D warnings" cargo doc --locked --no-deps --all-features)
(cd rust && cargo publish --locked --dry-run -p evidencebook --all-features)
(cd rust && cargo build --locked -p evidencebook-interop)   # the runs the rust.yml interop job checks

# Python (python/, the package capsule-evidencebook, imported as evidencebook;
# CI: the python job in ci.yml)
(cd python && python -m venv .venv && .venv/bin/pip install -e ".[test]" && .venv/bin/python -m pytest -q)

# Third-party verifier guide: every command and value of docs/THIRD-PARTY-VERIFIER.md
# (CI: the verifier-pack job in ci.yml; needs network access)
WORK=$(mktemp -d) EB_DIR=$PWD test/cleanroom/run.sh

# Shelf vectors (testdata/shelf) and the storage interlock (CI: shelf-interlock in ci.yml);
# regenerate the vectors only for an intended change
EB_WRITE_SHELF_VECTORS=1 go test -run 'TestShelfTreeVectors|TestShelfVectors|TestShelfCorrelationVectors|TestShelfFactVectors' . ./internal/shelfscript
dir=$(mktemp -d) && go run ./test/interop/shelf write "$dir/s" && go run ./test/interop/shelf continue "$dir/s" \
  && go run ./test/interop/shelf dump "$dir/s" | cmp testdata/shelf/after-continue.json -
# The migrate phase (evidencebook#461): any language migrates a shelf another wrote
go run ./test/interop/shelf migrate "$dir/s" && go run ./test/interop/shelf dump "$dir/s" | cmp testdata/shelf/after-migrate.json -
# The record time phase (evidencebook#461 §2.6) on the migrated shelf, any language, from the repository root
go run ./test/interop/shelf record-time "$dir/s" && go run ./test/interop/shelf dump "$dir/s" | cmp testdata/shelf/after-record-time.json -
# The Rust side (feature `shelf`): Go writes, Rust continues; the interlock job runs every pair of Go, Python and Rust
(cd rust && cargo build --locked -p evidencebook-interop --bin shelf)
dir=$(mktemp -d) && go run ./test/interop/shelf write "$dir/s" && rust/target/debug/shelf continue "$dir/s" \
  && go run ./test/interop/shelf dump "$dir/s" | cmp testdata/shelf/after-continue.json -

# Row status (rowstatus/, rust row_status, python row_status): the shared vectors, then Go, Rust
# and Python on the same random inputs (CI: row-status-diff in ci.yml)
python3 testdata/row-status/generate.py testdata/row-status/vectors.json && git diff --exit-code testdata/row-status
go build -o /tmp/rs-go ./test/interop/row-status && (cd rust && cargo build --locked -p evidencebook-interop --bin row-status) \
  && python/.venv/bin/python test/interop/row-status/diff.py /tmp/rs-go rust/target/debug/row-status

# x-deal-v1 in Go, Rust and Python (dealprofile/xdealv1, rust deal::v1, python evidencebook.deal.v1):
# records mutated from the fixtures and books from the vectors, on random inputs; Python on the
# kinds it implements (CI: deal-v1-diff in ci.yml)
go build -o /tmp/dv1-go ./test/interop/deal-v1 && (cd rust && cargo build --locked -p evidencebook-interop --bin deal-v1) \
  && python3 test/interop/deal-v1/diff.py /tmp/dv1-go rust/target/debug/deal-v1 --python python/.venv/bin/python --n 4000 --seed 1
# The Go types Python reads x-deal-v1 records into; regenerate only for an intended change
EB_WRITE_TYPES_TABLE=1 go test -run TestTypesTable ./dealprofile/xdealv1

# The Goose loop reference (python/examples/goose; CI: the goose job in ci.yml): its node is a
# capsulectl-deal profile. Needs the plugin built from this checkout and capsule-engine's `capsule-engine`
# command (capsule-engine go/v0.6.0), the scripted two-node run over MCP stdio (about six minutes:
# reports rest on cadence ticks), Go's check of every bundle and of the model-call Capsule, and the
# loop's commitment checker (each check skips when its variable is unset)
(cd python && python -m venv .venv && .venv/bin/pip install pytest cryptography "mcp==2.3.0" "websockets==17.4")
go -C capsulectl build -o /tmp/goose-bin/capsulectl-deal ./cmd/capsulectl-deal
GOOSE_CAPSULECTL_DEAL=/tmp/goose-bin/capsulectl-deal GOOSE_CAPSULE_ENGINE=/path/to/capsule-engine GOOSE_GO_CHECK=1 \
  python/.venv/bin/python -m pytest -q python/examples/goose/tests

# Release helper
python -m pytest -q .github/test_release.py

# Interoperability (test/interop/): Go programs that write or read files, and
# Python checks from the other implementations. Each needs a sibling checkout
# or package pinned in ci.yml; for example, with `agent-action-capsule`,
# `checkpointed-local-log` and `capsule-emit` installed at their pinned versions:
dir=$(mktemp -d)
go run ./test/interop/bundle "$dir/book" "$dir/bundle.json" "$dir/refusal.json"
python test/interop/python-aac/verify_bundle.py "$dir/bundle.json"
python test/interop/python-cll/verify_bundle.py "$dir/bundle.json"
```

CI (`ci.yml`) also runs the evidence interop profile, personal-history and
registry conformance jobs against pinned agent-action-capsule commits; read
the job for the exact pins and steps before changing what they cover.
`rust.yml`'s jobs run only when a path in its `on.push.paths` list changes
(`rust/`, the shared vectors under `testdata/`, `rowstatus/`, `dealprofile/`,
the Go and Python interop programs it compares with, and the root Go package's
`*.go`, `go.mod` and `go.sum`); `capsulectl.yml` and `report.yml` likewise.

**What a pull request runs in CI (evidencebook#925).** Pull requests run fast
checks only; run the heavy suites above locally before pushing. Each workflow
runs on every pull request and skips the jobs its changes do not reach, and
ends in one aggregate job that passes when every job passed or was skipped:
`CI result`, `capsulectl result`, `Rust result` and `report result`, the checks
to require. On a pull request the race detector runs only over the packages
the change reaches (`.github/scripts/race-packages.sh`), never over
`capsulectl/internal/deal`; the Goose loop runs only when its own files
change. Push to main and the nightly (ci.yml and capsulectl.yml) run
everything, and a red run there opens or comments on one `ci-red` issue per
workflow (`.github/scripts/report-failure.sh`), which the coordinator's
watchdog routes the same day. `bash .github/scripts/ci-scripts_test.sh` tests
the planning scripts.
`leak-lint.yml` scans public material for internal workflow
text.

## Layout

| Path | Contents |
|---|---|
| repository root | Go package `evidencebook` (the book, its substrates, indexes, requests and interop carriage) |
| `dealprofile/` | Go package `dealprofile`: the x-deal-v0 deal record profile (names, fingerprints, record payload and checks, report payload, step Capsule members); `x-deal-v0/` beside it holds PROFILE.md, the schemas, fixtures and `check_profile.py` |
| `adapter/` | Go adapters for foreign evidence (`acp`, `ucp`, `x402`) |
| `report/` | the nested module `github.com/action-state-group/evidencebook/report`: package `report`, which renders a verified Result-rooted bundle into an offline page and seals the page's digest (what `capsulectl book report` runs); its own module so the root's graph gains no CLI (`report/v*` tags) |
| `cmd/evidencebook/` | the `evidencebook` command, which runs the adapters |
| `capsulectl/` | the nested module `github.com/action-state-group/evidencebook/capsulectl`: two capsulectl plugins, the launchers `capsulectl-book` (`cmd/capsulectl-book`, `internal/book`, over books) and `capsulectl-deal` (`cmd/capsulectl-deal`, `internal/deal`, `skills/deal`, over deals; moved from evidencebook-deal with its history, evidencebook#610); neither links the other's package; its own CI workflow and one `capsulectl/v*` release for both |
| `rust/evidencebook/` | crate `evidencebook`, a Rust implementation of the same semantics |
| `rust/interop/` | `evidencebook-interop` (not published): the crate's Personal-Agent History and interop-profile runs, which the Rust CI `interop` job checks against the vectors and the Go runs, and the Rust side of the shelf storage interlock (bin `shelf`, `shelf-interlock` in ci.yml) |
| `python/` | the Python package `capsule-evidencebook` (import `evidencebook`): the book, the shelf and its cadence, the evidence request interaction, `evidencebook.deal` (x-deal-v0) and `evidencebook.deal.v1` (x-deal-v1), byte for byte with Go; not yet published |
| `test/interop/` | cross-implementation programs and checks that CI runs |
| `testdata/` | vectors and fixtures the Go, Rust and Python tests read |
| `schemas/`, `docs/` | the record header schema and vendored schemas; design notes, the interop PRD and the adapter and verifier guides |

The Go module stays at the repository root. Moving it into `go/` would change
its module path for every consumer.

The implementations follow the Agent Action Capsule specification and the
evidence request draft in `action-state-group/agent-action-capsule`, and embed
the Checkpointed Local Log from `action-state-group/checkpointed-local-log`.
Use their canonicalization and verification rather than reimplementing them.

## Rules

- Commits use conventional commit messages and a DCO sign-off
  (`git commit -s`). Do not add AI attribution trailers.
- New behavior needs tests in the same change, including error paths.
- A user-visible change adds a fragment `changelog.d/<short-name>.md`, not an edit to a
  changelog, so parallel pull requests never conflict there. It holds the entry for
  `CHANGELOG.md` (Go); start it with `<!-- changelog: rust/evidencebook/CHANGELOG.md -->` (Rust) or
  `<!-- changelog: python/CHANGELOG.md -->` (Python) for those. See
  [changelog.d/README.md](changelog.d/README.md).

## Releases

Follow [RELEASING.md](RELEASING.md). In short: for the crate, run **Release ·
Prepare**, merge its pull request, and the Release · Tag workflow tags and
publishes. For the Go module, merge the CHANGELOG pull request and an admin
pushes `v<version>`. For capsulectl-book and capsulectl-deal (one release), a maintainer pushes
`capsulectl/v<version>` ([capsulectl/RELEASING.md](capsulectl/RELEASING.md)).
Never create release tags by hand, except `v*`, which an admin pushes, and
`capsulectl/v*` and `report/v*`, which a maintainer pushes. Do not rename `publish-rust.yml`: crates.io's trusted publisher
names it, so publishing breaks until crates.io is updated first.
`.github/release.py` and `.github/test_release.py` are identical copies shared
with the other action-state-group repositories; change them in all of them
together.
