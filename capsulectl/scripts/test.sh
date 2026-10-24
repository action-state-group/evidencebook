#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Which pass and shard to run (see below).
pass="${CAPSULE_TEST_PASS:-all}"
shard="${CAPSULE_TEST_SHARD:-1/1}"
budget="${CAPSULE_TEST_BUDGET:-420}"
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
# internal/deal/race_sample_test.go; CAPSULE_TEST_RACE_FULL=1 runs them all),
# then every case without it.
#
# CI runs the passes as separate jobs and splits the race pass across several:
#   CAPSULE_TEST_PASS=race|plain   one pass only (default: both)
#   CAPSULE_TEST_SHARD=i/n         only the top-level tests in shard i of n
# A shard is every n-th name of the sorted `go test -list` output, so the n
# shards together run every test; a name two packages share runs in each.
# Each pass ends with its per-package times and slowest tests, and warns when
# a package takes longer than CAPSULE_TEST_BUDGET seconds (default 420).
# One module: the repository root.
modules=(.)
run=()
if ((shard_count > 1)); then
  listed=$(for m in "${modules[@]}"; do (cd "$m" && go test -list '.*' ./...); done)
  names=$(printf '%s\n' "$listed" | grep -E '^(Test|Example|Fuzz)' | sort -u |
    awk -v i="$shard_index" -v n="$shard_count" '(NR - 1) % n == i - 1')
  if [[ -z "$names" ]]; then
    printf 'Shard %s has no tests.\n' "$shard" >&2
    exit 1
  fi
  printf 'Shard %s: %d top-level tests\n' "$shard" "$(printf '%s\n' "$names" | wc -l)"
  run=(-run "^($(printf '%s\n' "$names" | paste -sd '|'))\$")
fi

log=$(mktemp)
trap 'rm -f "$log"' EXIT

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

status=0
for m in "${modules[@]}"; do
  if [[ "$pass" != plain ]]; then
    (cd "$m" && timed "race $shard ($m)" ${@+"$@"} ${run[@]+"${run[@]}"} -race -count=1 -timeout=15m ./...) || status=$?
  fi
  if [[ "$pass" != race ]]; then
    (cd "$m" && timed "plain $shard ($m)" ${@+"$@"} ${run[@]+"${run[@]}"} -count=1 -timeout=10m ./...) || status=$?
  fi
done
exit "$status"
