# CLAUDE.md

Guidance for coding agents working on this repository.

## Commands

```bash
# Go (repository root; the module github.com/action-state-group/evidencebook, Go 1.27)
test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum
go vet ./...
go test ./...
go test -race ./...

# report (report/, a nested module; CI: .github/workflows/report.yml)
(cd report && test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum)
(cd report && go vet ./... && go test ./... && go test -race ./...)

# capsulectl-book (capsulectl/, a nested module; CI: .github/workflows/capsulectl.yml)
(cd capsulectl && test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum)
(cd capsulectl && go vet ./... && go test ./...)

# Rust (rust/, a workspace: the crate evidencebook and the unpublished
# interop programs evidencebook-interop; CI: .github/workflows/rust.yml)
(cd rust && evidencebook/scripts/check-vocabulary.sh)   # the crate carries no deployment vocabulary
(cd rust && cargo fmt --all -- --check)
(cd rust && cargo clippy --locked --all-targets --all-features -- -D warnings)
(cd rust && cargo test --locked --all-features)
(cd rust && RUSTDOCFLAGS="-D warnings" cargo doc --locked --no-deps)
(cd rust && cargo publish --locked --dry-run -p evidencebook)
(cd rust && cargo build --locked -p evidencebook-interop)   # the runs the rust.yml interop job checks

# Python (python/, the package capsule-evidencebook, imported as evidencebook;
# CI: the python job in ci.yml)
(cd python && python -m venv .venv && .venv/bin/pip install -e ".[test]" && .venv/bin/python -m pytest -q)

# Release helper
python -m pytest -q .github/test_release.py

# Interoperability (test/interop/): Go programs that write or read files, and
# Python checks from the other implementations. Each needs a sibling checkout
# or package pinned in ci.yml; for example, with checkpointed-local-log at the
# pinned commit and `agent-action-capsule` and `capsule-emit` installed at
# their pinned versions:
dir=$(mktemp -d)
go run ./test/interop/bundle "$dir/book" "$dir/bundle.json" "$dir/refusal.json"
python test/interop/python-aac/verify_bundle.py "$dir/bundle.json"
python test/interop/python-cll/verify_bundle.py "$dir/bundle.json"
```

CI (`ci.yml`) also runs the evidence interop profile, personal-history and
registry conformance jobs against pinned agent-action-capsule commits; read
the job for the exact pins and steps before changing what they cover.
`rust.yml` runs only when `rust/`, `testdata/refusal-interop/`,
`testdata/evidence-request/`, the Go and Python interop programs it compares
with (`test/interop/personal-history/`, `test/interop/responder/`,
`test/interop/python-profile/`, `test/interop/python-personal/`, and the root
Go package's `*.go`, `go.mod` and `go.sum`), `.github/scripts/check_crate_metadata.py`
or the workflow itself changes. `leak-lint.yml` scans public material for internal workflow
text.

## Layout

| Path | Contents |
|---|---|
| repository root | Go package `evidencebook` (the book, its substrates, indexes, requests and interop carriage) |
| `adapter/` | Go adapters for foreign evidence (`acp`, `ucp`, `x402`) |
| `report/` | the nested module `github.com/action-state-group/evidencebook/report`: package `report`, which renders a verified Result-rooted bundle into an offline page and seals the page's digest (what `capsulectl book report` runs); its own module so the root's graph gains no CLI (`report/v*` tags) |
| `cmd/evidencebook/` | the `evidencebook` command, which runs the adapters |
| `capsulectl/` | the nested module `github.com/action-state-group/evidencebook/capsulectl`: `capsulectl-book`, the capsulectl plugin over books (its own CI workflow and `capsulectl/v*` releases) |
| `rust/evidencebook/` | crate `evidencebook`, a Rust implementation of the same semantics |
| `rust/interop/` | `evidencebook-interop` (not published): the crate's Personal-Agent History and interop-profile runs, which the Rust CI `interop` job checks against the vectors and the Go runs |
| `python/` | the Python package `capsule-evidencebook` (import `evidencebook`): the book and the evidence request interaction, byte for byte with Go; not yet published |
| `test/interop/` | cross-implementation programs and checks that CI runs |
| `testdata/` | vectors and fixtures the Go and Rust tests read |
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
- A user-visible change adds an entry under Unreleased in `CHANGELOG.md` (Go),
  `rust/evidencebook/CHANGELOG.md` (Rust) or `python/CHANGELOG.md` (Python).

## Releases

Follow [RELEASING.md](RELEASING.md). In short: for the crate, run **Release ·
Prepare**, merge its pull request, and the Release · Tag workflow tags and
publishes. For the Go module, merge the CHANGELOG pull request and an admin
pushes `v<version>`. For capsulectl-book, a maintainer pushes
`capsulectl/v<version>` ([capsulectl/RELEASING.md](capsulectl/RELEASING.md)).
Never create release tags by hand, except `v*`, which an admin pushes, and
`capsulectl/v*` and `report/v*`, which a maintainer pushes. Do not rename `publish-rust.yml`: crates.io's trusted publisher
names it, so publishing breaks until crates.io is updated first.
`.github/release.py` and `.github/test_release.py` are identical copies shared
with the other action-state-group repositories; change them in all of them
together.
