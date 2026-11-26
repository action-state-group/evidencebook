#!/usr/bin/env bash
# Tests race-packages.sh against this checkout's package graph and
# paths-changed.sh against this checkout's workflows. Run from anywhere:
# bash .github/scripts/ci-scripts_test.sh (ci.yml's CI scripts job runs it on
# every pull request).
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
  if ! got=$(run "$@"); then
    printf 'FAIL %s: race-packages.sh exited nonzero\n' "$what"
    fails=$((fails + 1))
    return
  fi
  got=${got//$'\n'/ }
  if [[ "$got" != "$want" ]]; then
    printf 'FAIL %s\n  want: %s\n  got:  %s\n' "$what" "$want" "$got"
    fails=$((fails + 1))
  else
    printf 'ok   %s\n' "$what"
  fi
}
# The raced packages; the helpers count a failure of the script itself (in
# the caller's shell, not a command substitution's).
expect_has() { # description, package, module, paths...
  local what=$1 pkg=$2 out
  shift 2
  if ! out=$(run "$@"); then
    printf 'FAIL %s: race-packages.sh exited nonzero\n' "$what"
    fails=$((fails + 1))
  elif grep -qxF "$pkg" <<<"$out"; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s: %s not raced\n' "$what" "$pkg"
    fails=$((fails + 1))
  fi
}
expect_lacks() {
  local what=$1 pkg=$2 out
  shift 2
  if ! out=$(run "$@"); then
    printf 'FAIL %s: race-packages.sh exited nonzero\n' "$what"
    fails=$((fails + 1))
  elif grep -qxF "$pkg" <<<"$out"; then
    printf 'FAIL %s: %s raced\n' "$what" "$pkg"
    fails=$((fails + 1))
  else printf 'ok   %s\n' "$what"; fi
}

# Expectations that depend on the package graph come from go list, so a later
# change to which package imports which does not break these tests.
pkgs_of() { # module dir; prints its packages, ./x form, sorted, one line
  (cd "$here/../../$1" && go list ./... | sed "s#^$(go list -m)\$#.#; s#^$(go list -m)/#./#" | sort | paste -sd ' ' -)
}
cctl_all=$(pkgs_of capsulectl | tr ' ' '\n' | grep -vx ./internal/deal | paste -sd ' ' -)
# A package book's tests import from this module, other than book itself.
book_dep=$(cd "$here/../../capsulectl" && go list -test -deps -f '{{.ImportPath}}' ./internal/book |
  sed 's/ \[.*\]$//; s/\.test$//' | grep "^$(go list -m)/internal/" | grep -vx "$(go list -m)/internal/book" | sort -u | head -n 1)
book_dep=${book_dep#github.com/action-state-group/evidencebook/}

# capsulectl
expect_has "capsulectl: a deal change races cmd" ./cmd capsulectl capsulectl/internal/deal/deal.go
expect_lacks "capsulectl: a deal change never races internal/deal" ./internal/deal capsulectl capsulectl/internal/deal/deal.go
expect_has "capsulectl: a book change races book" ./internal/book capsulectl capsulectl/internal/book/book.go
expect_has "capsulectl: a book change races cmd" ./cmd capsulectl capsulectl/internal/book/book.go
expect_lacks "capsulectl: a book change does not race bookdemo, which does not import it" ./scripts/bookdemo capsulectl capsulectl/internal/book/book.go
expect_has "capsulectl: a nested book fixture maps to book" ./internal/book capsulectl capsulectl/internal/book/testdata/result/x.json
if [[ -n "$book_dep" ]]; then
  expect_has "capsulectl: a change to ${book_dep#capsulectl/} races book, whose tests import it" ./internal/book capsulectl "$book_dep/x.go"
else
  printf 'FAIL capsulectl: book imports no other package of the module; pick another reverse-dependency case\n'
  fails=$((fails + 1))
fi
expect "capsulectl: docs, skills and README race nothing" "" capsulectl capsulectl/docs/a.md capsulectl/skills/deal/SKILL.md capsulectl/README.md
expect "capsulectl: root-module and Python changes race nothing" "" capsulectl book.go python/evidencebook/x.py
expect "capsulectl: go.mod races everything but internal/deal" "$cctl_all" capsulectl capsulectl/go.mod
expect "capsulectl: the workflow races everything" "$cctl_all" capsulectl .github/workflows/capsulectl.yml
expect "capsulectl: a shared fixture tree races everything" "$cctl_all" capsulectl testdata/card-registration/vectors.json
expect "capsulectl: a Go file in no known package (new or deleted) races everything" "$cctl_all" capsulectl capsulectl/internal/gone/gone.go
expect_has "capsulectl: a rename across packages races the old side" ./scripts/bookdemo \
  capsulectl capsulectl/scripts/bookdemo/main.go capsulectl/internal/book/moved.go
expect_has "capsulectl: a rename across packages races the new side" ./internal/book \
  capsulectl capsulectl/scripts/bookdemo/main.go capsulectl/internal/book/moved.go
expect_lacks "capsulectl: internal/deal is never raced" ./internal/deal capsulectl capsulectl/go.mod

# root module
root_all=$(pkgs_of .)
expect "root: capsulectl, report, prose and workflow changes race nothing" "" root \
  capsulectl/internal/book/x.go report/report.go docs/GUIDE.md changelog.d/x.md .github/workflows/rust.yml
expect_has "root: an xdealv1 change races xdealv1" ./dealprofile/xdealv1 root dealprofile/xdealv1/types.go
expect_lacks "root: an xdealv1 change does not race adapter/acp" ./adapter/acp root dealprofile/xdealv1/types.go
expect_has "root: a Python file the root tests read races the root package" . root python/evidencebook/deal/_data/x-deal-v1-types.json
expect "root: go.mod races everything" "$root_all" root go.mod
expect "root: testdata races everything" "$root_all" root testdata/shelf/after-continue.json

# The local policy (scripts/pre-push.sh, evidencebook#973) drops the
# exclusions, so internal/deal is raced when a change reaches it, through its
# own files, a package its tests import or a shared input, and only then.
run_local() { # module paths...
  local module=$1
  shift
  printf '%s\n' "$@" | RACE_PACKAGES_LOCAL=1 bash "$here/race-packages.sh" "$module"
}
local_has() { # description, want (0 raced, 1 not), package, module, paths...
  local what=$1 want=$2 pkg=$3 out got=1
  shift 3
  if ! out=$(run_local "$@"); then
    printf 'FAIL %s: race-packages.sh exited nonzero\n' "$what"
    fails=$((fails + 1))
    return
  fi
  grep -qxF "$pkg" <<<"$out" && got=0
  if ((got == want)); then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s: %s raced is %s\n' "$what" "$pkg" "$( ((got == 0)) && echo true || echo false)"
    fails=$((fails + 1))
  fi
}
local_has "local: a deal change races internal/deal" 0 ./internal/deal capsulectl capsulectl/internal/deal/deal.go
local_has "local: a packfile change races internal/deal (its tests import it)" 0 ./internal/deal capsulectl capsulectl/internal/packfile/packfile.go
local_has "local: go.mod races internal/deal" 0 ./internal/deal capsulectl capsulectl/go.mod
local_has "local: a book change does not race internal/deal" 1 ./internal/deal capsulectl capsulectl/internal/book/book.go
local_has "local: a bookdemo change does not race internal/deal" 1 ./internal/deal capsulectl capsulectl/scripts/bookdemo/main.go

# scripts/pre-push.sh --plan: which suites run and what each races.
prepush_plan() { # description, want (lines joined by "|"), paths... ("--all" alone: no paths, --all)
  local what=$1 want=$2 got ch
  shift 2
  ch=$(mktemp)
  local args=(--plan)
  if [[ "${1:-}" == --all ]]; then args+=(--all); else printf '%s\n' "$@" > "$ch"; fi
  if ! got=$(PRE_PUSH_CHANGED=$ch bash "$here/../../scripts/pre-push.sh" "${args[@]}"); then
    printf 'FAIL %s: pre-push.sh --plan exited nonzero\n' "$what"
    fails=$((fails + 1))
    rm -f "$ch"
    return
  fi
  rm -f "$ch"
  got=${got//$'\n'/|}
  if [[ "$got" == "$want" ]]; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s\n  want: %s\n  got:  %s\n' "$what" "$want" "$got"
    fails=$((fails + 1))
  fi
}
prepush_plan "pre-push: a bookdemo change races only bookdemo" \
  "suites: capsulectl|root race: none|capsulectl race: ./scripts/bookdemo" capsulectl/scripts/bookdemo/main.go
prepush_plan "pre-push: a book change races book and its launchers, not internal/deal" \
  "suites: capsulectl|root race: none|capsulectl race: ./cmd ./cmd/capsulectl-book ./internal/book" capsulectl/internal/book/book.go
prepush_plan "pre-push: a doc change runs the root suite and races nothing" \
  "suites: root|root race: none|capsulectl race: none" docs/GUIDE.md
prepush_plan "pre-push: capsulectl's go.mod races every capsulectl package" \
  "suites: capsulectl|root race: none|capsulectl race: $(pkgs_of capsulectl)" capsulectl/go.mod
prepush_plan "pre-push: --all races everything" \
  "suites: root capsulectl report rust python|root race: ./...|capsulectl race: ./..." --all
if PRE_PUSH_CHANGED=/dev/null bash "$here/../../scripts/pre-push.sh" --bogus > /dev/null 2>&1; then
  printf 'FAIL pre-push: an unknown flag exited 0\n'
  fails=$((fails + 1))
else printf 'ok   pre-push: an unknown flag fails\n'; fi

# scripts/pre-push.sh's capsulectl suite, run against a fake go that logs its
# calls: go test answers -list with $TEST_NAMES and fails a race run when
# FAIL_RACE is set (FAIL_LIST fails -list); go list fails when FAIL_PLAN is
# set and is real otherwise, so the planner sees this checkout's graph.
gofake=$(mktemp -d)
real_go_exec=$(command -v go)
cat > "$gofake/go" <<FAKE
#!/usr/bin/env bash
echo "\$*" >> "\$GO_LOG"
case "\$1" in
  test)
    if [[ " \$* " == *" -list "* ]]; then
      [[ -z "\${FAIL_LIST:-}" ]] || exit 1
      [[ -z "\${TEST_NAMES:-}" ]] || printf '%s\n' \$TEST_NAMES
      exit 0
    fi
    if [[ " \$* " == *" -race "* && -n "\${FAIL_RACE:-}" ]]; then echo "--- FAIL: TestInjected (0.00s)"; exit 1; fi
    exit 0 ;;
  vet | mod | generate) exit 0 ;;
  list) [[ -z "\${FAIL_PLAN:-}" ]] || { echo "go: injected failure" >&2; exit 1; } ;;
esac
exec "$real_go_exec" "\$@"
FAKE
chmod +x "$gofake/go"
# prepush_run description, want_exit, want_race_runs, want_plain_runs, race package ("" none), changed path, env...
prepush_run() {
  local what=$1 want_exit=$2 want_race=$3 want_plain=$4 pkg=$5 path=$6 st=0 race plain
  shift 6
  printf '%s\n' "$path" > "$gofake/changed"
  : > "$gofake/log"
  env PATH="$gofake:$PATH" GO_LOG="$gofake/log" PRE_PUSH_CHANGED="$gofake/changed" PRE_PUSH_NESTED=1 PRE_PUSH_SHARDS=4 "$@" \
    bash "$here/../../scripts/pre-push.sh" > "$gofake/out" 2>&1 || st=$?
  race=$(grep -E '^test -v .*-race' "$gofake/log" || true)
  plain=$(grep -E '^test -v ' "$gofake/log" | grep -v -- -race || true)
  local nrace nplain bad=""
  nrace=$(grep -c . <<<"$race" || true)
  nplain=$(grep -c . <<<"$plain" || true)
  ((st == want_exit)) || bad+=" exit $st (want $want_exit)"
  ((nrace == want_race)) || bad+=" race runs $nrace (want $want_race)"
  ((nplain == want_plain)) || bad+=" plain runs $nplain (want $want_plain)"
  if [[ -n "$pkg" ]] && grep -v -- " $pkg\$" <<<"$race" | grep -q .; then bad+=" a race run not over $pkg"; fi
  if [[ -z "$bad" ]]; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s:%s\n' "$what" "$bad"
    sed 's/^/    /' "$gofake/out" | tail -15
    fails=$((fails + 1))
  fi
}
prepush_run "pre-push run: three tests get three shards, each over the planned package" \
  0 3 3 ./scripts/bookdemo capsulectl/scripts/bookdemo/main.go TEST_NAMES="TestA TestB TestC"
prepush_run "pre-push run: several tests run in the configured shards" \
  0 4 4 "./cmd ./cmd/capsulectl-book ./internal/book" capsulectl/internal/book/book.go TEST_NAMES="TestA TestB TestC TestD TestE TestF"
prepush_run "pre-push run: packages with no tests still build under race, in one unfiltered shard" \
  0 1 1 ./scripts/genconfusables capsulectl/scripts/genconfusables/main.go TEST_NAMES=
prepush_run "pre-push run: an empty race plan runs only the plain pass" \
  0 0 3 "" capsulectl/README.md TEST_NAMES="TestA TestB TestC"
prepush_run "pre-push run: a failed race run fails the push check" \
  1 3 3 ./scripts/bookdemo capsulectl/scripts/bookdemo/main.go TEST_NAMES="TestA TestB TestC" FAIL_RACE=1
prepush_run "pre-push run: a failed test listing fails the push check" \
  1 0 0 "" capsulectl/scripts/bookdemo/main.go FAIL_LIST=1
prepush_run "pre-push run: a failed race plan stops the script" \
  2 0 0 "" capsulectl/scripts/bookdemo/main.go FAIL_PLAN=1
rm -rf "$gofake"

# A go list error fails the planner instead of yielding an empty plan.
fake=$(mktemp -d)
trap 'rm -rf "$fake"' EXIT
real_go=$(command -v go)
cat > "$fake/go" <<FAKE
#!/usr/bin/env bash
case "\$FAIL_ON \$*" in
  "all list -f"*) echo "go: injected failure" >&2; exit 1 ;;
  "deps "*-deps*) echo "go: injected failure" >&2; exit 1 ;;
esac
exec "$real_go" "\$@"
FAKE
chmod +x "$fake/go"
fails_closed() { # description, FAIL_ON value
  if printf 'capsulectl/internal/book/book.go\n' | PATH="$fake:$PATH" FAIL_ON=$2 bash "$here/race-packages.sh" capsulectl > /dev/null 2>&1; then
    printf 'FAIL %s: exited 0\n' "$1"
    fails=$((fails + 1))
  else printf 'ok   %s\n' "$1"; fi
}
fails_closed "race-packages: a failed package listing fails the script" all
fails_closed "race-packages: a failed dependency listing fails the script" deps

# report-failure.sh against a fake gh that logs its calls.
cat > "$fake/gh" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >> "$MOCK_LOG"
case "$1 $2" in
  "label create") exit "${LABEL_CREATE_RC:-0}" ;;
  "label list") printf '%s' "${LABELS:-}" ;;
  "issue list")
    n=$(cat "$MOCK_LOG.lists" 2>/dev/null || echo 0)
    echo $((n + 1)) > "$MOCK_LOG.lists"
    if ((n == 0)); then printf '%s' "${FIRST_LIST:-}"; else printf '%s' "${SECOND_LIST:-}"; fi
    ;;
  "issue create") echo "https://github.com/o/r/issues/${CREATED:-41}" ;;
esac
FAKE
chmod +x "$fake/gh"
reports() { # description, wanted issue calls ("verb number", ;-separated), env...
  local what=$1 want=$2 got
  shift 2
  rm -f "$fake/log" "$fake/log.lists"
  if ! env PATH="$fake:$PATH" MOCK_LOG="$fake/log" GITHUB_REPOSITORY=o/r TITLE=T RUN=R REPORT_SETTLE_SECONDS=0 \
    "$@" bash "$here/report-failure.sh" > /dev/null 2>&1; then
    printf 'FAIL %s: exited nonzero\n' "$what"
    fails=$((fails + 1))
    return
  fi
  got=$(grep -E '^issue (comment|edit|close|create)' "$fake/log" | awk '{n = ""; for (i = 3; i <= NF; i++) if ($i ~ /^[0-9]+$/) { n = " " $i; break }; print $2 n}' | paste -sd ';' -)
  if [[ "$got" == "$want" ]]; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s\n  want: %s\n  got:  %s\n' "$what" "$want" "$got"
    fails=$((fails + 1))
  fi
}
reports "report: an open issue gets the run, then the label" "comment 7;edit 7" FIRST_LIST=7 LABELS=ci-red
reports "report: a label that cannot be created does not stop the report" "comment 7" FIRST_LIST=7 LABEL_CREATE_RC=1 LABELS=
reports "report: a newer duplicate leaves ours open" "create;edit 41" CREATED=41 SECOND_LIST=42 LABELS=ci-red
reports "report: an older duplicate takes our run and ours closes" \
  "create;edit 42;comment 41;edit 41;close 42" \
  CREATED=42 SECOND_LIST=$'41\n42' LABELS=ci-red

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
if printf 'a\n' | bash "$here/paths-changed.sh" "$here/../workflows/ci.yml" > /dev/null 2>&1; then
  printf 'FAIL paths: a workflow without a one-line push paths list exited 0\n'
  fails=$((fails + 1))
else printf 'ok   paths: a workflow without a one-line push paths list fails\n'; fi

# Every job of a gated workflow is in its aggregate's and its reporter's needs,
# so a job added later is neither ungated nor unreported on main.
for wf in ci capsulectl rust report; do
  file="$here/../workflows/$wf.yml"
  jobs=$(awk '/^jobs:/{j=1; next} j && /^  [a-z0-9-]+:$/{sub(/:$/,""); sub(/^  /,""); print}' "$file" |
    grep -vxE 'result|report-failure' | sort)
  for agg in result report-failure; do
    needs=$(awk -v job="  $agg:" '$0 == job {f=1; next} f && /^  [a-z0-9-]+:$/ {f=0} f && /^    needs:/ {print; exit}' "$file" |
      sed -E 's/^    needs: \[(.*)\]$/\1/' | tr -d ' ' | tr ',' '\n' | sort)
    if [[ "$needs" == "$jobs" ]]; then printf 'ok   %s.yml: %s needs every job\n' "$wf" "$agg"; else
      printf 'FAIL %s.yml: %s needs differ from the jobs:\n%s\n' "$wf" "$agg" "$(diff <(echo "$jobs") <(echo "$needs"))"
      fails=$((fails + 1))
    fi
  done
done

if ((fails)); then
  printf '%d failed\n' "$fails"
  exit 1
fi
