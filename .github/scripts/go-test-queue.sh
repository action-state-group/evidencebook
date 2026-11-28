#!/usr/bin/env bash
# Runs the tests of the Go module in the current directory from a queue, in
# several processes at once (evidencebook#1038, #1079): each package's test
# binary is built once, its top-level tests are split into batches, and N
# workers take batches from the queue as they finish. A package's tests run
# one at a time inside a process, as `go test` runs them, so a package whose
# tests never call t.Parallel still spreads over N CPUs instead of one, and a
# few slow tests do not hold one worker while the others idle. A batch is a
# fresh process: TestMain and any lazily built shared fixture run once per
# batch.
#
#   go-test-queue.sh [options] -- package...
#
#   --label L       names the pass in the output (default: tests)
#   --timeout T     each batch's go test -timeout (default 10m)
#   --jobs N        worker processes (default 1)
#   --batches K     batches per worker (default 4)
#   --names FILE    run only the top-level tests named in FILE, one per line
#                   (a shard's share)
#   --flag F        a build flag, such as -race; repeat for several
#   --budget S      warn (a GitHub Actions ::warning::) for each batch that
#                   takes longer than S seconds
#
# Every package builds with the flags, those without tests too, as go test
# builds them. Afterwards it prints the per-batch times and the slowest tests,
# and fails when a batch fails or dies, a package does not build or list, or a
# queued test reports no result. Callers: the root module's Race tests job
# (.github/workflows/ci.yml) and capsulectl/scripts/test.sh's queued mode.
set -euo pipefail

label=tests timeout=10m jobs=1 per_job=4 names_file="" budget="" flags=()
while (($#)); do
  case "$1" in
    --label) label=$2; shift 2 ;;
    --timeout) timeout=$2; shift 2 ;;
    --jobs) jobs=$2; shift 2 ;;
    --batches) per_job=$2; shift 2 ;;
    --names) names_file=$2; shift 2 ;;
    --flag) flags+=("$2"); shift 2 ;;
    --budget) budget=$2; shift 2 ;;
    --) shift; break ;;
    *) printf 'go-test-queue.sh: unknown argument %s\n' "$1" >&2; exit 2 ;;
  esac
done
packages=("$@")
if ((${#packages[@]} == 0)); then
  printf 'go-test-queue.sh: no package named\n' >&2
  exit 2
fi
for v in "$jobs" "$per_job"; do
  if [[ ! "$v" =~ ^[1-9][0-9]*$ ]]; then
    printf 'go-test-queue.sh: --jobs and --batches take a positive number, not %s\n' "$v" >&2
    exit 2
  fi
done

if [[ -n "$budget" && ! "$budget" =~ ^[0-9]+$ ]]; then
  printf 'go-test-queue.sh: --budget takes a number of seconds, not %s\n' "$budget" >&2
  exit 2
fi

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT

run_queue() {
  local status=0 pkg pdir bin n=0 b total listing pkgs built
  local build=(go test -c ${flags[@]+"${flags[@]}"})
  local start=$SECONDS
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
  mkdir -p "$dir/bin"
  while read -r pkg pdir; do
    [[ -n "$pkg" ]] || continue
    bin="$dir/bin/$n.test"
    "${build[@]}" -o "$bin" "$pkg" || return 1
    printf '%s\t%s\t%s\n' "$n" "$bin" "$pdir" >>"$dir/bins"
    listing=$(cd "$pdir" && "$bin" -test.list '.*') || return 1
    # A shard keeps only its names; grep finding none is not an error.
    { grep -E '^(Test|Example|Fuzz)' <<<"$listing" || true; } |
      { if [[ -n "$names_file" ]]; then grep -xFf "$names_file" || true; else cat; fi; } |
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
  printf '%s: %d tests in %d batches over %d processes (built and listed in %ds)\n' \
    "$label" "$total" "$b" "$jobs" $((SECONDS - start))
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
  cat "$dir"/batch-*.log >"$dir/all.log"
  # A worker that died before its summary line counts as a failure.
  if (($(cat "$dir/summary" 2>/dev/null | wc -l) != b)); then
    printf '== %s: %d of %d batches reported ==\n' "$label" "$(cat "$dir/summary" 2>/dev/null | wc -l)" "$b"
    status=1
  fi
  # Every queued test must report a result: a name its binary did not run
  # (one of another package, say) would otherwise pass unnoticed.
  local ran
  ran=$(grep -cE '^--- (PASS|FAIL|SKIP): ' "$dir/all.log" || true)
  printf '== %s: %d of %d tests reported a result ==\n' "$label" "$ran" "$total"
  ((ran == total)) || status=1
  if grep -q '^FAIL ' "$dir/summary" 2>/dev/null; then
    status=1
    local st batch secs
    while read -r st batch secs pdir; do
      [[ "$st" == FAIL ]] || continue
      printf '\n== %s: %s failed (%s, %ss) ==\n' "$label" "$batch" "$pdir" "$secs"
      cat "$dir/$batch.log"
    done <"$dir/summary"
  fi
  printf '\n== %s: %ds over %d processes, build and listing included; batch times ==\n' "$label" $((SECONDS - start)) "$jobs"
  sort -k3,3nr "$dir/summary" | awk 'NR <= 5'
  if [[ -n "$budget" ]]; then
    awk -v budget="$budget" '$3 + 0 > budget + 0 {
      printf "::warning::%s (%s) took %ds, over the %ss budget\n", $2, $4, $3, budget }' "$dir/summary"
  fi
  printf '== %s: slowest tests ==\n' "$label"
  awk '/^--- (PASS|FAIL)/ { t = $4; gsub(/[()s]/, "", t); printf "%9.2fs  %s  %s\n", t, $2, $3 }' "$dir/all.log" |
    sort -rn | awk 'NR <= 15'
  return "$status"
}

run_queue
