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
#                                  (unset: one go test run per pass, as CI runs it)
#   CAPSULE_TEST_BATCHES=k         with CAPSULE_TEST_JOBS, k batches per process (default 4)
#
# With CAPSULE_TEST_JOBS set, each package's test binary is built once and
# its tests are split into CAPSULE_TEST_BATCHES batches per process, which n workers take
# from a queue as they finish: the deal package's tests run one at a time in a
# process (none calls t.Parallel), so one process per CPU is the speed-up, and
# a queue keeps a few slow tests from holding one worker while the others idle.
# A batch is a fresh process, as a shard is: TestMain and the lazily built shared
# stores run once per batch.
# A shard is every n-th name of the sorted `go test -list` output, so the n
# shards together run every test; a name two packages share runs in each.
# Each pass ends with its per-package times and slowest tests, and warns when
# a package takes longer than CAPSULE_TEST_BUDGET seconds (default 420).
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

# One pass through the queue: build each package's test binary (with the
# pass's flags), list its tests (only those named in run, when sharded), cut
# them into batches and let $jobs workers run them; then the same per-package
# times and slowest tests as timed, from the batches' logs.
queued() { # label, race|plain, go test -timeout value
  local dir st=0
  dir=$(mktemp -d) || return 1
  run_queue "$dir" "$@" || st=$?
  rm -rf "$dir"
  return "$st"
}

run_queue() { # work dir, label, race|plain, go test -timeout value
  local dir=$1 label=$2 kind=$3 timeout=$4 status=0 pkg pdir bin n=0 b total listing pkgs
  local build=(go test -c) flags=()
  [[ "$kind" == race ]] && flags=("${race_flags[@]}")
  build+=(${flags[@]+"${flags[@]}"})
  # Every package builds with the pass's flags, those without tests too, as
  # go test builds them.
  local built
  built=$(go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' "${packages[@]}") || return 1
  if [[ -n "$built" ]]; then
    # go build discards what it builds for several packages; a lone package
    # would leave its binary here, so it goes to /dev/null. (-o into a
    # directory would skip the packages that are not main.)
    local out=()
    (($(wc -l <<<"$built") == 1)) && out=(-o /dev/null)
    # shellcheck disable=SC2086 # import paths, one per line
    go build ${flags[@]+"${flags[@]}"} ${out[@]+"${out[@]}"} $built || return 1
  fi
  pkgs=$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}} {{.Dir}}{{end}}' "${packages[@]}") || return 1
  while read -r pkg pdir; do
    [[ -n "$pkg" ]] || continue
    bin="$dir/bin/$n.test"
    "${build[@]}" -o "$bin" "$pkg" || return 1
    printf '%s\t%s\t%s\n' "$n" "$bin" "$pdir" >>"$dir/bins"
    listing=$(cd "$pdir" && "$bin" -test.list '.*') || return 1
    # A shard keeps only its names; grep finding none is not an error.
    { grep -E '^(Test|Example|Fuzz)' <<<"$listing" || true; } |
      { if [[ -n "$names" ]]; then grep -xFf <(printf '%s\n' "$names") || true; else cat; fi; } |
      awk -v n="$n" '{ print n "\t" $0 }' >>"$dir/tests"
    n=$((n + 1))
  done <<<"$pkgs"
  if [[ ! -s "$dir/tests" ]]; then
    printf '== %s: no tests ==\n' "$label"
    return 0
  fi
  total=$(wc -l <"$dir/tests")
  b=$((jobs * per_job))
  ((b <= total)) || b=$total
  # Each package's tests get a share of the b batches in proportion to their
  # number (at least one), and batch k of a package takes every k-th of its
  # sorted tests, so each batch mixes tests from across the package as a shard
  # does. A batch holds one package's tests: they run in its binary.
  sort -t$'\t' -k1,1n -k2,2 "$dir/tests" >"$dir/sorted"
  awk -F'\t' -v b="$b" -v total="$total" -v d="$dir" '
    NR == FNR { count[$1]++; next }
    {
      if (!($1 in batches)) { batches[$1] = int(b * count[$1] / total + 0.5); if (batches[$1] < 1) batches[$1] = 1 }
      k = seen[$1]++ % batches[$1]; f = d "/batch-" $1 "-" k ".list"
      print $1 "\t" $2 >> f; close(f)
    }' "$dir/sorted" "$dir/sorted"
  b=$(find "$dir" -name 'batch-*.list' | wc -l | tr -d ' ')
  printf '%s: %d tests in %d batches over %d processes\n' "$label" "$total" "$b" "$jobs"
  local start=$SECONDS
  # A worker runs one batch and adds "ok|FAIL <batch> <seconds> <package dir>"
  # to the summary; go test runs a test binary in its package's directory.
  cat >"$dir/worker.sh" <<'WORKER'
dir=$1 timeout=$2 batch=$3
idx=$(head -1 "$batch" | cut -f1)
line=$(awk -F'\t' -v i="$idx" '$1 == i' "$dir/bins")
bin=$(cut -f2 <<<"$line") pdir=$(cut -f3 <<<"$line")
re="^($(cut -f2 "$batch" | paste -sd '|' -))\$"
name=${batch##*/} name=${name%.list}
s=$SECONDS st=ok
(cd "$pdir" && "$bin" -test.paniconexit0 -test.v -test.count=1 -test.timeout="$timeout" -test.run "$re") >"$dir/$name.log" 2>&1 || st=FAIL
printf '%s %s %s %s\n' "$st" "$name" $((SECONDS - s)) "$pdir" >>"$dir/summary"
WORKER
  find "$dir" -name 'batch-*.list' -print0 | xargs -0 -P "$jobs" -n 1 bash "$dir/worker.sh" "$dir" "$timeout" || true
  cat "$dir"/batch-*.log >"$log"
  # A worker that died before its summary line counts as a failure.
  if (($(cat "$dir/summary" 2>/dev/null | wc -l) != b)); then
    printf '== %s: %d of %d batches reported ==\n' "$label" "$(cat "$dir/summary" 2>/dev/null | wc -l)" "$b"
    status=1
  fi
  # Every queued test must report a result: a name its binary did not run
  # (one of another package, say) would otherwise pass unnoticed.
  local ran
  ran=$(grep -cE '^--- (PASS|FAIL|SKIP): ' "$log" || true)
  printf '== %s: %d of %d tests reported a result ==\n' "$label" "$ran" "$total"
  ((ran == total)) || status=1
  if grep -q '^FAIL ' "$dir/summary" 2>/dev/null; then
    status=1
    while read -r st batch secs pdir; do
      [[ "$st" == FAIL ]] || continue
      printf '\n== %s: %s failed (%s, %ss) ==\n' "$label" "$batch" "$pdir" "$secs"
      cat "$dir/$batch.log"
    done <"$dir/summary"
  fi
  printf '\n== %s: %ds over %d processes; batch times ==\n' "$label" $((SECONDS - start)) "$jobs"
  sort -k3,3nr "$dir/summary" | awk 'NR <= 5'
  printf '== %s: slowest tests ==\n' "$label"
  awk '/^--- (PASS|FAIL)/ { t = $4; gsub(/[()s]/, "", t); printf "%9.2fs  %s  %s\n", t, $2, $3 }' "$log" |
    sort -rn | awk 'NR <= 15'
  return "$status"
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
