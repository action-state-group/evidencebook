#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Which pass and shard to run (see below).
pass="${CAPSULE_TEST_PASS:-all}"
shard="${CAPSULE_TEST_SHARD:-1/1}"
budget="${CAPSULE_TEST_BUDGET:-420}"
# go test -timeout for the race pass; the nightly full-case run needs more.
race_timeout="${CAPSULE_TEST_RACE_TIMEOUT:-15m}"
case "$pass" in all | race | plain) ;; *)
  printf 'CAPSULE_TEST_PASS must be race, plain or all, not %s\n' "$pass" >&2
  exit 1
  ;;
esac
if [[ ! "$shard" =~ ^([0-9]+)/([0-9]+)$ ]] || ((BASH_REMATCH[1] < 1 || BASH_REMATCH[1] > BASH_REMATCH[2])); then
  printf 'CAPSULE_TEST_SHARD must be i/n with 1 <= i <= n, not %s\n' "$shard" >&2
  exit 1
fi
shard_index=${BASH_REMATCH[1]}
shard_count=${BASH_REMATCH[2]}

# Arguments are passed to go test. Two passes: the race detector, under which the
# slowest single-goroutine case loops run a fixed sample (see
# raceSample in internal/deal/corehelpers_test.go; CAPSULE_TEST_RACE_FULL=1 runs them all),
# then every case without it.
#
# CI runs the passes as separate jobs and splits the race pass across several:
#   CAPSULE_TEST_PASS=race|plain   one pass only (default: both)
#   CAPSULE_TEST_SHARD=i/n         only the top-level tests in shard i of n
#   CAPSULE_TEST_PKGS="./a ./b"    only these packages (default ./...); a pull
#                                  request's race job passes scripts/race-packages.sh's list
#   CAPSULE_TEST_JOBS=n            run the shard's tests from a queue, in n processes at once
#                                  (CI sets one per CPU; unset: one go test run per pass)
#   CAPSULE_TEST_BATCHES=k         with CAPSULE_TEST_JOBS, k batches per process (default 4)
#
# With CAPSULE_TEST_JOBS set, .github/scripts/go-test-queue.sh runs the pass:
# each package's test binary is built once and its tests are split into
# CAPSULE_TEST_BATCHES batches per process, which n workers take from a queue
# (the deal package's tests run one at a time in a process, so one process per
# CPU is the speed-up). A batch is a fresh process, as a shard is: TestMain and
# the lazily built shared stores run once per batch.
# A shard is every n-th name of the sorted `go test -list` output, so the n
# shards together run every test; a name two packages share runs in each.
# Each pass ends with its per-package times (per-batch times when queued) and
# slowest tests, and warns when a package (a batch, when queued) takes longer
# than CAPSULE_TEST_BUDGET seconds (default 420).
# One module: the repository root.
modules=(.)
read -r -a packages <<<"${CAPSULE_TEST_PKGS:-./...}"
if ((${#packages[@]} == 0)); then
  printf 'CAPSULE_TEST_PKGS names no package\n' >&2
  exit 1
fi
queue=${CAPSULE_TEST_JOBS:+1}
jobs="${CAPSULE_TEST_JOBS:-1}"
if [[ ! "$jobs" =~ ^[1-9][0-9]*$ ]]; then
  printf 'CAPSULE_TEST_JOBS must be a positive number, not %s\n' "$jobs" >&2
  exit 1
fi
per_job="${CAPSULE_TEST_BATCHES:-4}"
if [[ ! "$per_job" =~ ^[1-9][0-9]*$ ]]; then
  printf 'CAPSULE_TEST_BATCHES must be a positive number, not %s\n' "$per_job" >&2
  exit 1
fi
if [[ -n "$queue" ]] && (($# > 0)); then
  printf 'CAPSULE_TEST_JOBS takes no go test arguments\n' >&2
  exit 1
fi
run=() names=""
if ((shard_count > 1)); then
  listed=$(for m in "${modules[@]}"; do (cd "$m" && go test -list '.*' "${packages[@]}"); done)
  names=$(printf '%s\n' "$listed" | grep -E '^(Test|Example|Fuzz)' | sort -u |
    awk -v i="$shard_index" -v n="$shard_count" '(NR - 1) % n == i - 1')
  if [[ -z "$names" ]]; then
    printf 'Shard %s has no tests.\n' "$shard" >&2
    exit 1
  fi
  printf 'Shard %s: %d top-level tests\n' "$shard" "$(printf '%s\n' "$names" | wc -l)"
  run=(-run "^($(printf '%s\n' "$names" | paste -sd '|' -))\$")
fi

# The race pass's flags. modernc.org/sqlite and the modernc.org packages it
# needs are a pure-Go translation of SQLite's C code; under the race detector
# their instrumented code was about a third of the pass's time (evidencebook#1038:
# a 41-test sample of internal/deal took 106 s and 77 s raced, 73 s and 47 s
# with them left out). They are built without race instrumentation, so races
# inside SQLite's own code go unchecked; every package of this module and of
# its other dependencies stays instrumented, and a race in our code is still
# reported (.github/scripts/ci-scripts_test.sh plants one).
race_flags=(-race '-gcflags=modernc.org/...=-race=false')

log=$(mktemp)
trap 'rm -f "$log" "$log.names"' EXIT

# go test with -v, teed to $log; then the pass's per-package times and its
# slowest top-level tests, from that log.
timed() {
  local label=$1 status=0
  shift
  go test -v "$@" 2>&1 | tee "$log" || status=$?
  printf '\n== %s: per-package time (budget %ss) ==\n' "$label" "$budget"
  awk -v budget="$budget" '$1 ~ /^(ok|FAIL)$/ && $2 ~ /\// && $3 ~ /s$/ {
    t = $3; sub(/s$/, "", t); printf "%9.1fs  %s  %s\n", t, $1, $2
    if (t + 0 > budget + 0) printf "::warning::%s took %.0fs, over the %ss budget\n", $2, t, budget
  }' "$log"
  printf '== %s: slowest tests ==\n' "$label"
  awk '/^--- (PASS|FAIL)/ { t = $4; gsub(/[()s]/, "", t); printf "%9.2fs  %s  %s\n", t, $2, $3 }' "$log" |
    sort -rn | awk 'NR <= 15'
  return "$status"
}

# One pass through the queue (.github/scripts/go-test-queue.sh, shared with
# the root module's race job): the pass's flags, its timeout and, when sharded,
# only the shard's test names. GO_TEST_QUEUE names the script elsewhere
# (.github/scripts/ci-scripts_test.sh runs a copy of this one).
queue_sh=${GO_TEST_QUEUE:-$PWD/../.github/scripts/go-test-queue.sh}
queued() { # label, race|plain, go test -timeout value
  local label=$1 kind=$2 timeout=$3 args=() f
  [[ "$kind" == race ]] && for f in "${race_flags[@]}"; do args+=(--flag "$f"); done
  if [[ -n "$names" ]]; then
    printf '%s\n' "$names" >"$log.names" || return 1
    args+=(--names "$log.names")
  fi
  bash "$queue_sh" --label "$label" --timeout "$timeout" --jobs "$jobs" --batches "$per_job" --budget "$budget" \
    ${args[@]+"${args[@]}"} -- "${packages[@]}"
}

status=0
if [[ -n "$queue" ]]; then
  if [[ "$pass" != plain ]]; then
    queued "race $shard" race "$race_timeout" || status=$?
  fi
  if [[ "$pass" != race ]]; then
    queued "plain $shard" plain 10m || status=$?
  fi
  exit "$status"
fi
for m in "${modules[@]}"; do
  if [[ "$pass" != plain ]]; then
    (cd "$m" && timed "race $shard ($m)" ${@+"$@"} ${run[@]+"${run[@]}"} "${race_flags[@]}" -count=1 -timeout="$race_timeout" "${packages[@]}") || status=$?
  fi
  if [[ "$pass" != race ]]; then
    (cd "$m" && timed "plain $shard ($m)" ${@+"$@"} ${run[@]+"${run[@]}"} -count=1 -timeout=10m "${packages[@]}") || status=$?
  fi
done
exit "$status"
