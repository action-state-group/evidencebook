# CLAUDE.md

Guidance for coding agents working on this repository.

## Commands

```bash
# Go (repository root; the module github.com/action-state-group/evidencebook, Go 1.27)
test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum
go vet ./...
go test ./...
go test -race ./...

# Rust (rust/, a workspace with the one crate evidencebook)
(cd rust && evidencebook/scripts/check-vocabulary.sh)   # the crate carries no deployment vocabulary
(cd rust && cargo fmt --all -- --check)
(cd rust && cargo clippy --locked --all-targets --all-features -- -D warnings)
(cd rust && cargo test --locked --all-features)
(cd rust && RUSTDOCFLAGS="-D warnings" cargo doc --locked --no-deps)
(cd rust && cargo publish --locked --dry-run -p evidencebook)

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
`.github/scripts/check_crate_metadata.py` or the workflow itself changes. `leak-lint.yml` scans public material for internal workflow
text.

## Layout

| Path | Contents |
|---|---|
| repository root | Go package `evidencebook` (the book, its substrates, indexes, requests and interop carriage) |
| `adapter/` | Go adapters for foreign evidence (`acp`, `ucp`, `x402`) |
| `cmd/evidencebook/` | the `evidencebook` command, which runs the adapters |
| `rust/evidencebook/` | crate `evidencebook`, a Rust implementation of the same semantics |
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
- A user-visible change adds an entry under Unreleased in `CHANGELOG.md` (Go)
  or `rust/evidencebook/CHANGELOG.md` (Rust).

## Releases

Follow [RELEASING.md](RELEASING.md). In short: for the crate, run **Release ·
Prepare**, merge its pull request, and the Release · Tag workflow tags and
publishes. For the Go module, merge the CHANGELOG pull request and an admin
pushes `v<version>`. Never create release tags by hand, except `v*`, which an
admin pushes. Do not rename `publish-rust.yml`: crates.io's trusted publisher
names it, so publishing breaks until crates.io is updated first.
`.github/release.py` and `.github/test_release.py` are identical copies shared
with the other action-state-group repositories; change them in all of them
together.
