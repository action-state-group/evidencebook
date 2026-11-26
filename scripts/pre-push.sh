#!/usr/bin/env bash
# shellcheck disable=SC2329 # the suite functions run through suite()
# The check to run before every push (evidencebook#925): pull requests run only
# fast checks in CI, so the heavy suites run here. By default it runs every
# suite the branch's changes reach (against the merge base with origin/main),
# using the same rules as CI; --all runs every suite. Each suite's plain tests
# run in full; the race detector runs over the packages the change reaches
# (evidencebook#973): .github/scripts/race-packages.sh's plan, the one a pull
# request's race job uses, with internal/deal included when the change reaches
# it (RACE_PACKAGES_LOCAL=1). --all races every package. Push to main and the
# nightly race everything in CI.
#
#   scripts/pre-push.sh [--all] [--plan]
#
# --plan prints the suites and race packages it would run, one line each, and
# runs nothing. PRE_PUSH_CHANGED=<file> reads the changed paths from that file
# instead of git (the planning scripts' tests use both).
#
# Suites: root (format, module files, vet, tests, race), capsulectl (format,
# module files, vet, the generated confusables table, every test plain, the
# race pass over its planned packages, both in parallel shards), report (vet,
# tests, race), rust (fmt, clippy, tests), python (pytest, needs python/.venv
# as CLAUDE.md sets it up) and the CI planning scripts' tests. The jobs that
# need a pinned Chrome or capsule-engine (deal pages, presentation pass, the
# Goose loop) run on main and the nightly, and here only as CLAUDE.md lists
# them. Each suite reports its time; the script exits nonzero if any failed.
# PRE_PUSH_SHARDS sets the parallel shards per capsulectl pass (default: half
# the CPUs). --all took 24 minutes on an Apple M5 Pro (CLAUDE.md).
set -uo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root" || exit 2
all=0 plan=0
for arg in "$@"; do
  case "$arg" in
    --all) all=1 ;;
    --plan) plan=1 ;;
    *) echo "usage: scripts/pre-push.sh [--all] [--plan]" >&2; exit 2 ;;
  esac
done

changed=$(mktemp)
log=$(mktemp -d)
trap 'rm -rf "$changed" "$log"' EXIT
# Selection errors stop the script: a check that silently skipped a suite
# would pass without running what the changes need.
if [[ -n "${PRE_PUSH_CHANGED:-}" ]]; then
  cp "$PRE_PUSH_CHANGED" "$changed" || exit 2
elif ((!all)); then
  base=$(git merge-base HEAD origin/main) || { echo "no merge base with origin/main; fetch it or pass --all" >&2; exit 2; }
  git diff --no-renames --name-only "$base" > "$changed" || { echo "git diff failed" >&2; exit 2; }
  git ls-files --others --exclude-standard >> "$changed" || { echo "git ls-files failed" >&2; exit 2; }
fi

# Whether a suite runs: always with --all; otherwise when the changes reach it.
reaches() { # workflow
  ((all)) && return 0
  local r
  r=$(bash .github/scripts/paths-changed.sh "$1" < "$changed") || { echo "paths-changed.sh $1 failed" >&2; exit 2; }
  case "$r" in true) return 0 ;; false) return 1 ;; *) echo "paths-changed.sh $1 printed $r" >&2; exit 2 ;; esac
}
matched() { # grep options and pattern, over the changed paths; exits on a grep error
  local st=0
  grep "$@" "$changed" > /dev/null || st=$?
  ((st < 2)) || { echo "grep failed" >&2; exit 2; }
  ((st == 0))
}
reaches_root() {
  ((all)) && return 0
  matched -vE '^(capsulectl/|report/|changelog\.d/|\.github/workflows/(capsulectl|capsulectl-core-main|rust|report|leak-lint|changelog-fragments|publish-rust|release|prepare-release|tag-release)\.yml$)'
}
reaches_python() {
  ((all)) && return 0
  matched -E '^(python/|testdata/|dealprofile/|[^/]+\.go$|go\.(mod|sum)$)'
}

# The packages of one module the race pass runs, space-separated (empty: none).
race_plan() { # root|capsulectl
  if ((all)); then echo ./...; return; fi
  local out
  out=$(RACE_PACKAGES_LOCAL=1 bash .github/scripts/race-packages.sh "$1" < "$changed") || { echo "race-packages.sh $1 failed" >&2; exit 2; }
  printf '%s\n' "$out" | paste -sd ' ' - | sed 's/^ *$//'
}

results=()
status=0
suite() { # name, command...
  local name=$1 start st=0
  shift
  printf '\n== %s ==\n' "$name"
  start=$SECONDS
  "$@" || st=$?
  results+=("$(printf '%-12s %-4s %5ss' "$name" "$([[ $st == 0 ]] && echo ok || echo FAIL)" $((SECONDS - start)))")
  ((st == 0)) || status=1
}

go_module() { # dir: format, module files, vet
  (cd "$1" && test -z "$(gofmt -l .)" && go mod tidy && git diff --exit-code -- go.mod go.sum && go vet ./...)
}

root_suite() {
  go_module . && go test ./... || return 1
  if [[ -z "$root_race" ]]; then echo "race: no package the change reaches"; return 0; fi
  # shellcheck disable=SC2086 # the plan is a space-separated package list
  go test -race $root_race
}

# One pass of capsulectl's tests in parallel shards (scripts/test.sh) over the
# given packages; a shard's failures are printed only when it fails. One
# process per pass is too slow: internal/deal alone ran past the plain pass's
# ten-minute timeout on a laptop. A pass over few tests gets at most one shard
# per top-level test, since scripts/test.sh fails an empty shard.
capsulectl_pass() { # plain|race, packages (space-separated)
  local pass=$1 pkgs=$2 n=${PRE_PUSH_SHARDS:-$(($(getconf _NPROCESSORS_ONLN) / 2))} i pids=() st=0 tests
  ((n >= 1)) || n=1
  # shellcheck disable=SC2086 # the plan is a space-separated package list
  tests=$(cd capsulectl && go test -list '.*' $pkgs | awk '/^(Test|Example|Fuzz)/ && !seen[$0]++ { n++ } END { print n + 0 }') || return 1
  # Packages without tests (scripts/genconfusables, internal/packfile) leave nothing to run.
  ((tests >= 1)) || { echo "$pass pass: no tests in $pkgs"; return 0; }
  ((n <= tests)) || n=$tests
  for ((i = 1; i <= n; i++)); do
    (cd capsulectl && CAPSULE_TEST_PASS=$pass CAPSULE_TEST_SHARD="$i/$n" CAPSULE_TEST_PKGS="$pkgs" CAPSULE_TEST_RACE_TIMEOUT=60m \
      bash scripts/test.sh > "$log/capsulectl-$pass-$i.log" 2>&1) &
    pids+=($!)
  done
  for i in "${!pids[@]}"; do
    if ! wait "${pids[$i]}"; then
      st=1
      printf -- '-- %s shard %d/%d failed:\n' "$pass" $((i + 1)) "$n"
      grep -E '^(--- FAIL|FAIL|WARNING: DATA RACE|panic:)' "$log/capsulectl-$pass-$((i + 1)).log" | head -40 ||
        tail -20 "$log/capsulectl-$pass-$((i + 1)).log"
    fi
  done
  printf '%s pass over %s: %d shards, %s\n' "$pass" "$pkgs" "$n" "$( ((st == 0)) && echo ok || echo FAIL)"
  return "$st"
}

capsulectl_suite() {
  go_module capsulectl || return 1
  (cd capsulectl && go generate ./internal/deal && git diff --exit-code -- internal/deal/confusables_table.go) || return 1
  local st=0
  capsulectl_pass plain ./... || st=1
  if [[ -z "$capsulectl_race" ]]; then
    echo "race: no package the change reaches"
  else
    capsulectl_pass race "$capsulectl_race" || st=1
  fi
  return "$st"
}

report_suite() {
  go_module report && (cd report && go test -count=1 ./... && go test -race -count=1 ./...)
}

rust_suite() {
  (cd rust && cargo fmt --all -- --check &&
    cargo clippy --locked --all-targets --all-features -- -D warnings &&
    cargo test --locked --all-features)
}

python_suite() {
  if [[ ! -x python/.venv/bin/python ]]; then
    echo "python/.venv is missing; set it up as CLAUDE.md says" >&2
    return 1
  fi
  (cd python && .venv/bin/python -m pytest -q)
}

skipped() { results+=("$(printf '%-12s %s' "$1" 'skipped (the changes do not reach it)')"); }
run_root=0 run_capsulectl=0 run_report=0 run_rust=0 run_python=0
reaches_root && run_root=1
reaches .github/workflows/capsulectl.yml && run_capsulectl=1
reaches .github/workflows/report.yml && run_report=1
reaches .github/workflows/rust.yml && run_rust=1
reaches_python && run_python=1
root_race="" capsulectl_race=""
# race_plan runs in a command substitution, so its failure is checked here.
if ((run_root)); then root_race=$(race_plan root) || exit 2; fi
if ((run_capsulectl)); then capsulectl_race=$(race_plan capsulectl) || exit 2; fi

if ((plan)); then
  printf 'suites:'
  for s in root capsulectl report rust python; do v=run_$s; ((${!v})) && printf ' %s' "$s"; done
  printf '\nroot race: %s\ncapsulectl race: %s\n' "${root_race:-none}" "${capsulectl_race:-none}"
  exit 0
fi

suite ci-scripts bash .github/scripts/ci-scripts_test.sh
if ((run_root)); then suite root root_suite; else skipped root; fi
if ((run_capsulectl)); then suite capsulectl capsulectl_suite; else skipped capsulectl; fi
if ((run_report)); then suite report report_suite; else skipped report; fi
if ((run_rust)); then suite rust rust_suite; else skipped rust; fi
if ((run_python)); then suite python python_suite; else skipped python; fi

printf '\n== summary (%s) ==\n' "$( ((all)) && echo all suites || echo suites the changes reach)"
printf '%s\n' "${results[@]}"
exit "$status"
