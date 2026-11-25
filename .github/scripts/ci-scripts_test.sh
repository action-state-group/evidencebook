#!/usr/bin/env bash
# Tests race-packages.sh against this checkout's package graph and
# paths-changed.sh against this checkout's workflows. Run from anywhere:
# bash .github/scripts/ci-scripts_test.sh (capsulectl.yml's quality job runs it).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
fails=0

run() { # module paths...
  local module=$1
  shift
  printf '%s\n' "$@" | bash "$here/race-packages.sh" "$module"
}
expect() { # description, want ("" for none; space-separated, sorted), module, paths...
  local what=$1 want=$2 got
  shift 2
  got=$(run "$@" | paste -sd ' ' -)
  if [[ "$got" != "$want" ]]; then
    printf 'FAIL %s\n  want: %s\n  got:  %s\n' "$what" "$want" "$got"
    fails=$((fails + 1))
  else
    printf 'ok   %s\n' "$what"
  fi
}
expect_has() { # description, package, module, paths...
  local what=$1 pkg=$2
  shift 2
  if run "$@" | grep -qxF "$pkg"; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s: %s not raced\n' "$what" "$pkg"
    fails=$((fails + 1))
  fi
}
expect_lacks() {
  local what=$1 pkg=$2
  shift 2
  if run "$@" | grep -qxF "$pkg"; then
    printf 'FAIL %s: %s raced\n' "$what" "$pkg"
    fails=$((fails + 1))
  else printf 'ok   %s\n' "$what"; fi
}

cctl_all="./cmd ./cmd/capsulectl-book ./cmd/capsulectl-deal ./internal/book ./internal/deal/assets ./internal/packfile ./scripts/bookdemo ./scripts/genconfusables"

# capsulectl
expect "capsulectl: a deal change races cmd and the deal launcher, never internal/deal" \
  "./cmd ./cmd/capsulectl-deal ./scripts/genconfusables" capsulectl capsulectl/internal/deal/deal.go
expect "capsulectl: a book change" "./cmd ./cmd/capsulectl-book ./internal/book" capsulectl capsulectl/internal/book/book.go
expect_has "capsulectl: a nested book fixture maps to book" ./internal/book capsulectl capsulectl/internal/book/testdata/result/x.json
expect_has "capsulectl: deal/assets races book, which imports it" ./internal/book capsulectl capsulectl/internal/deal/assets/deal-view.css
expect_has "capsulectl: packfile races cmd" ./cmd capsulectl capsulectl/internal/packfile/packfile.go
expect_has "capsulectl: packfile races book" ./internal/book capsulectl capsulectl/internal/packfile/packfile.go
expect "capsulectl: docs, skills and README race nothing" "" capsulectl capsulectl/docs/a.md capsulectl/skills/deal/SKILL.md capsulectl/README.md
expect "capsulectl: root-module and Python changes race nothing" "" capsulectl book.go python/evidencebook/x.py
expect "capsulectl: go.mod races everything but internal/deal" "$cctl_all" capsulectl capsulectl/go.mod
expect "capsulectl: the workflow races everything" "$cctl_all" capsulectl .github/workflows/capsulectl.yml
expect "capsulectl: a shared fixture tree races everything" "$cctl_all" capsulectl testdata/card-registration/vectors.json
expect "capsulectl: a Go file in no known package (new or deleted) races everything" "$cctl_all" capsulectl capsulectl/internal/gone/gone.go
expect "capsulectl: a rename across packages races both sides" "./cmd ./cmd/capsulectl-book ./internal/book ./scripts/bookdemo" \
  capsulectl capsulectl/scripts/bookdemo/main.go capsulectl/internal/book/moved.go
expect_lacks "capsulectl: internal/deal is never raced" ./internal/deal capsulectl capsulectl/go.mod

# root module
root_all=$(cd "$here/../.." && go list ./... | sed "s#^$(go list -m)\$#.#; s#^$(go list -m)/#./#" | sort | paste -sd ' ' -)
expect "root: capsulectl, report, prose and workflow changes race nothing" "" root \
  capsulectl/internal/book/x.go report/report.go docs/GUIDE.md changelog.d/x.md .github/workflows/rust.yml
expect_has "root: an xdealv1 change races xdealv1" ./dealprofile/xdealv1 root dealprofile/xdealv1/types.go
expect_lacks "root: an xdealv1 change does not race adapter/acp" ./adapter/acp root dealprofile/xdealv1/types.go
expect_has "root: a Python file the root tests read races the root package" . root python/evidencebook/deal/_data/x-deal-v1-types.json
expect "root: go.mod races everything" "$root_all" root go.mod
expect "root: testdata races everything" "$root_all" root testdata/shelf/after-continue.json

# paths-changed.sh
paths() { # description, want, workflow, paths...
  local what=$1 want=$2 wf=$3 got
  shift 3
  got=$(printf '%s\n' "$@" | bash "$here/paths-changed.sh" "$here/../workflows/$wf.yml")
  if [[ "$got" == "$want" ]]; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s: want %s, got %s\n' "$what" "$want" "$got"
    fails=$((fails + 1))
  fi
}
paths "paths: a root Go file reaches Rust CI" true rust book.go
paths "paths: a Go file in a subpackage does not reach Rust CI (*.go is root only)" false rust adapter/acp/acp.go
paths "paths: a shelf fixture reaches Rust CI" true rust testdata/shelf/after-continue.json
paths "paths: docs reach no module workflow" false rust docs/GUIDE.md
paths "paths: report/ reaches report" true report report/report.go
paths "paths: capsulectl/ does not reach report" false report capsulectl/go.mod
paths "paths: a capsulectl fixture tree reaches capsulectl" true capsulectl testdata/live16-ask/a.json
paths "paths: the root module does not reach capsulectl" false capsulectl book.go

if ((fails)); then
  printf '%d failed\n' "$fails"
  exit 1
fi
