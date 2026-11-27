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

# ci.yml's Unit tests job leaves the packages the Race tests job runs to it on a
# pull request, which holds only while no root-module code changes under the
# race detector: no race build constraint, race.Enabled or race sampling.
race_aware=$(cd "$here/../.." && git ls-files '*.go' | grep -vE '^(capsulectl|report)/' |
  xargs grep -lE '^//go:build.*race|race\.Enabled|raceDetector|raceSample|raceRounds|raceSkip' 2> /dev/null || true)
if [[ -z "$race_aware" ]]; then printf 'ok   ci.yml unit/race split: no root-module code changes under the race detector\n'; else
  printf 'FAIL ci.yml unit/race split: these files change under the race detector, so Unit tests must run them too:\n%s\n' "$race_aware"
  fails=$((fails + 1))
fi

# scripts/pre-push.sh --plan: ci-local, and whether internal/deal's race pass runs.
prepush_plan() { # description, want (lines joined by "|"), paths...
  local what=$1 want=$2 got ch
  shift 2
  ch=$(mktemp)
  printf '%s\n' "$@" > "$ch"
  if ! got=$(PRE_PUSH_CHANGED=$ch bash "$here/../../scripts/pre-push.sh" --plan); then
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
prepush_plan "pre-push: a deal change runs ci-local and internal/deal's race pass" \
  "ci-local: scripts/ci-local.sh|internal/deal race: yes" capsulectl/internal/deal/deal.go
prepush_plan "pre-push: a packfile change reaches internal/deal (its tests import it)" \
  "ci-local: scripts/ci-local.sh|internal/deal race: yes" capsulectl/internal/packfile/packfile.go
prepush_plan "pre-push: capsulectl's go.mod reaches internal/deal" \
  "ci-local: scripts/ci-local.sh|internal/deal race: yes" capsulectl/go.mod
prepush_plan "pre-push: a book change runs ci-local only" \
  "ci-local: scripts/ci-local.sh|internal/deal race: no" capsulectl/internal/book/book.go
prepush_plan "pre-push: a doc change runs ci-local only" \
  "ci-local: scripts/ci-local.sh|internal/deal race: no" docs/GUIDE.md
if PRE_PUSH_CHANGED=/dev/null bash "$here/../../scripts/pre-push.sh" --bogus > /dev/null 2>&1; then
  printf 'FAIL pre-push: an unknown flag exited 0\n'
  fails=$((fails + 1))
else printf 'ok   pre-push: an unknown flag fails\n'; fi
# A dirty checkout is refused before anything runs: ci-local tests committed HEAD.
dirty=$(mktemp -d)
git clone -q --shared "$here/../.." "$dirty/repo" 2> /dev/null
printf 'x\n' > "$dirty/repo/uncommitted.txt"
if (cd "$dirty/repo" && bash "$here/../../scripts/pre-push.sh") > "$dirty/out" 2>&1; then
  printf 'FAIL pre-push: a checkout with uncommitted changes passed\n'
  fails=$((fails + 1))
elif grep -q 'uncommitted changes' "$dirty/out"; then printf 'ok   pre-push: a checkout with uncommitted changes is refused\n'; else
  printf 'FAIL pre-push: a dirty checkout failed for another reason: %s\n' "$(tail -3 "$dirty/out")"
  fails=$((fails + 1))
fi
rm -rf "$dirty"
if PRE_PUSH_CHANGED=/dev/null bash "$here/../../scripts/pre-push.sh" --all > /dev/null 2>&1; then
  printf 'FAIL pre-push: --all (gone with the suites) exited 0\n'
  fails=$((fails + 1))
else printf 'ok   pre-push: --all is refused\n'; fi

# scripts/pre-push.sh's run, against a fake ci-local and a fake go that log
# their calls. `go test -c` writes a fake test binary that answers -test.list with
# $TEST_NAMES (FAIL_LIST fails it) and, for -test.run, logs "ran <race|plain>
# <package dir> <name>" and prints its PASS line for each name, and fails a race run when FAIL_RACE is
# set. go list fails when FAIL_PLAN is set and is real otherwise, so the
# planner and the queue see this checkout's graph.
gofake=$(mktemp -d)
real_go_exec=$(command -v go)
cat > "$gofake/go" <<FAKE
#!/usr/bin/env bash
echo "\$*" >> "\$GO_LOG"
case "\$1" in
  test)
    if [[ "\$2" == -c ]]; then
      kind=plain out=""
      shift 2
      while ((\$#)); do
        case "\$1" in -race) kind=race ;; -o) out=\$2; shift ;; esac
        shift
      done
      mkdir -p "\$(dirname "\$out")"
      cat > "\$out" <<BIN
#!/usr/bin/env bash
for a in "\\\$@"; do
  case "\\\$a" in
    -test.list) [[ -z "\\\${FAIL_LIST:-}" ]] || exit 1; [[ -z "\\\${TEST_NAMES:-}" ]] || printf '%s\n' \\\$TEST_NAMES; exit 0 ;;
  esac
done
re=\\\${@: -1}; re=\\\${re#^(}; re=\\\${re%)\\\$}
for n in \\\${re//|/ }; do echo "ran \$kind \\\${PWD##*/capsulectl} \\\$n" >> "\\\$GO_LOG"; echo "--- PASS: \\\$n (0.00s)"; done
if [[ \$kind == race && -n "\\\${FAIL_RACE:-}" ]]; then printf -- '--- FAIL: TestInjected (0.00s)\\n    x_test.go:9: the injected reason\\n'; exit 1; fi
exit 0
BIN
      chmod +x "\$out"
      exit 0
    fi
    exit 0 ;;
  vet | mod | generate | build) exit 0 ;;
  list) [[ -z "\${FAIL_PLAN:-}" ]] || { echo "go: injected failure" >&2; exit 1; } ;;
esac
exec "$real_go_exec" "\$@"
FAKE
chmod +x "$gofake/go"
# The capsulectl packages that have test files, as the package dirs the fake logs.
test_dirs_of() { # packages (space-separated, ./x form)
  # shellcheck disable=SC2086 # a space-separated package list
  (cd "$here/../../capsulectl" && go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.Dir}}{{end}}' $1 | sed "s#^$PWD##" | sort)
}
cat > "$gofake/ci-local" <<'FAKE'
echo "ci-local ran" >> "$GO_LOG"
exit "${FAKE_CI_LOCAL_EXIT:-0}"
FAKE
# prepush_run description, want_exit, want_deal (1: internal/deal's race pass
# runs), changed path, env... ci-local must run once; the race pass, when it
# runs, must run every name of $TEST_NAMES once in internal/deal and nothing else.
prepush_run() {
  local what=$1 want_exit=$2 want_deal=$3 path=$4 st=0 bad="" want got n names
  shift 4
  printf '%s\n' "$path" > "$gofake/changed"
  : > "$gofake/log"
  env PATH="$gofake:$PATH" GO_LOG="$gofake/log" PRE_PUSH_CHANGED="$gofake/changed" PRE_PUSH_SHARDS=4 \
    PRE_PUSH_CI_LOCAL="bash $gofake/ci-local" "$@" bash "$here/../../scripts/pre-push.sh" > "$gofake/out" 2>&1 || st=$?
  ((st == want_exit)) || bad+=" exit $st (want $want_exit)"
  [[ "$(grep -c '^ci-local ran$' "$gofake/log")" == 1 ]] || bad+=" ci-local did not run once"
  # shellcheck disable=SC2016 # expanded by the inner shell, under the run's env
  names=$(env "$@" bash -c 'printf "%s\n" ${TEST_NAMES:-}')
  want=""
  if ((want_deal)) && [[ -n "$names" ]]; then
    want=$(for n in $names; do echo "ran race /internal/deal $n"; done | sort)
  fi
  got=$(grep '^ran ' "$gofake/log" | sort || true)
  [[ "$got" == "$want" ]] || bad+=" runs differ: $(diff <(echo "$want") <(echo "$got") | grep '^[<>]' | head -3 | tr '\n' ';')"
  if [[ -z "$bad" ]]; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s:%s\n' "$what" "$bad"
    sed 's/^/    /' "$gofake/out" | tail -15
    fails=$((fails + 1))
  fi
}
prepush_run "pre-push run: a deal change runs ci-local and every internal/deal test once under race" \
  0 1 capsulectl/internal/deal/deal.go TEST_NAMES="TestA TestB TestC"
prepush_run "pre-push run: a book change runs ci-local only" \
  0 0 capsulectl/internal/book/book.go TEST_NAMES="TestA TestB TestC"
prepush_run "pre-push run: a failed ci-local fails the push check, and the race pass still runs" \
  1 1 capsulectl/internal/deal/deal.go TEST_NAMES="TestA TestB" FAKE_CI_LOCAL_EXIT=1
prepush_run "pre-push run: a failed race pass fails the push check" \
  1 1 capsulectl/internal/deal/deal.go TEST_NAMES="TestA TestB TestC" FAIL_RACE=1
if grep -q -- '--- FAIL: TestInjected' "$gofake/out" && grep -q 'the injected reason' "$gofake/out"; then
  printf 'ok   pre-push run: a failed race pass prints the failing test and its reason\n'
else
  printf 'FAIL pre-push run: the failing test is not printed\n'
  fails=$((fails + 1))
fi
prepush_run "pre-push run: a shard in the caller's environment does not narrow the race pass" \
  0 1 capsulectl/internal/deal/deal.go TEST_NAMES="TestA TestB TestC TestD" CAPSULE_TEST_SHARD=1/2
prepush_run "pre-push run: a bound of one process runs every test" \
  0 1 capsulectl/internal/deal/deal.go TEST_NAMES="TestA TestB TestC" PRE_PUSH_SHARDS=1
prepush_run "pre-push run: a failed test listing fails the push check" \
  1 0 capsulectl/internal/deal/deal.go FAIL_LIST=1
: > "$gofake/log"
if PATH="$gofake:$PATH" GO_LOG="$gofake/log" PRE_PUSH_CHANGED="$gofake/changed" FAIL_PLAN=1 PRE_PUSH_CI_LOCAL="bash $gofake/ci-local" \
  bash "$here/../../scripts/pre-push.sh" > "$gofake/out" 2>&1; then
  printf 'FAIL pre-push run: a failed race plan did not stop the script\n'
  fails=$((fails + 1))
elif grep -q '^ci-local ran$' "$gofake/log"; then
  printf 'FAIL pre-push run: a failed race plan still ran ci-local\n'
  fails=$((fails + 1))
else printf 'ok   pre-push run: a failed race plan stops the script before anything runs\n'; fi
rm -rf "$gofake"

# A go list error fails the planner instead of yielding an empty plan.
fake=$(mktemp -d)
trap 'rm -rf "$fake"' EXIT
real_go=$(command -v go)
cat > "$fake/go" <<FAKE
#!/usr/bin/env bash
case "\$FAIL_ON \$*" in
  "all list -f"*) echo "go: injected failure" >&2; exit 1 ;;
  "deps "*-test*) echo "go: injected failure" >&2; exit 1 ;;
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

# capsulectl/scripts/test.sh's queued mode (CAPSULE_TEST_JOBS, evidencebook#1038),
# on a fixture module that holds its own copy of the script: two packages share
# a test name, one has an Example, one has no tests, and FAIL_TEST names a
# test that fails. Every test must run exactly once, in its package's directory.
qmod=$(mktemp -d)
mkdir -p "$qmod/scripts" "$qmod/a" "$qmod/b" "$qmod/c"
cp "$here/../../capsulectl/scripts/test.sh" "$qmod/scripts/test.sh"
printf 'module example.com/q\n\ngo 1.21\n' > "$qmod/go.mod"
qtest() { # package, test names...
  local pkg=$1 name
  shift
  printf 'package %s\n\nimport (\n\t"fmt"\n\t"os"\n\t"path/filepath"\n\t"testing"\n)\n\n' "$pkg"
  printf 'func record(t *testing.T) {\n\twd, _ := os.Getwd()\n\tf, err := os.OpenFile(os.Getenv("RECORD"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)\n'
  printf '\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tdefer f.Close()\n\tfmt.Fprintf(f, "%%s %%s\\n", filepath.Base(wd), t.Name())\n'
  printf '\tif os.Getenv("FAIL_TEST") == filepath.Base(wd)+"."+t.Name() {\n\t\tt.Fatal("asked to fail")\n\t}\n}\n\n'
  for name in "$@"; do printf 'func %s(t *testing.T) { record(t) }\n' "$name"; done
}
qtest a TestShared TestA1 TestA2 TestA3 TestA4 TestA5 > "$qmod/a/a_test.go"
qtest b TestShared TestB1 TestB2 > "$qmod/b/b_test.go"
printf 'package b\n\nimport "fmt"\n\nfunc ExampleHello() {\n\tfmt.Println("hello")\n\t// Output: hello\n}\n' > "$qmod/b/example_test.go"
printf 'package a\n' > "$qmod/a/a.go"
printf 'package b\n\nfunc Hello() {}\n' > "$qmod/b/b.go"
printf 'package c\n\nfunc C() {}\n' > "$qmod/c/c.go"
mkdir -p "$qmod/cmdx"
printf 'package main\n\nfunc main() {}\n' > "$qmod/cmdx/main.go"
want_q=$(printf '%s\n' "a TestA1" "a TestA2" "a TestA3" "a TestA4" "a TestA5" "a TestShared" "b TestB1" "b TestB2" "b TestShared" | sort)
queued_run() { # description, want exit (0|1), record file, env...
  local what=$1 want=$2 rec=$3 st=0
  shift 3
  (cd "$qmod" && env RECORD="$rec" CAPSULE_TEST_PASS=plain "$@" bash scripts/test.sh > "$rec.out" 2>&1) || st=1
  if ((st != want)); then
    printf 'FAIL %s: exit %d, want %d\n%s\n' "$what" "$st" "$want" "$(tail -20 "$rec.out")"
    fails=$((fails + 1))
    return 1
  fi
}
rec="$qmod/rec1"
if queued_run "queued: every test once" 0 "$rec" CAPSULE_TEST_JOBS=3; then
  if [[ "$(sort "$rec")" == "$want_q" ]] && grep -q '10 of 10 tests reported a result' "$rec.out"; then
    printf 'ok   queued: every test and example runs once, in its package directory\n'
  else
    printf 'FAIL queued: tests run:\n%s\n' "$(diff <(echo "$want_q") <(sort "$rec"))"
    fails=$((fails + 1))
  fi
fi
rec="$qmod/rec2"
if queued_run "queued: one failing test fails the pass" 1 "$rec" CAPSULE_TEST_JOBS=2 FAIL_TEST=b.TestShared; then
  if grep -q -- '--- FAIL: TestShared' "$rec.out" && grep -q 'asked to fail' "$rec.out" && [[ "$(sort "$rec")" == "$want_q" ]]; then
    printf 'ok   queued: a failing test fails the pass, prints its output, and every other test still runs\n'
  else
    printf 'FAIL queued: the failure is not reported, or tests were skipped\n%s\n' "$(tail -20 "$rec.out")"
    fails=$((fails + 1))
  fi
fi
rec="$qmod/rec3"
for shard in 1/2 2/2; do
  queued_run "queued: shard $shard" 0 "$rec" CAPSULE_TEST_JOBS=2 CAPSULE_TEST_SHARD=$shard || true
done
if [[ "$(sort "$rec")" == "$want_q" ]]; then printf 'ok   queued: two shards together run every test once\n'; else
  printf 'FAIL queued: shards ran:\n%s\n' "$(diff <(echo "$want_q") <(sort "$rec"))"
  fails=$((fails + 1))
fi
before=$(cd "$qmod" && find . -path ./rec\* -prune -o -print | sort)
queued_run "queued: a lone command package" 0 "$qmod/rec5" CAPSULE_TEST_JOBS=1 CAPSULE_TEST_PKGS=./cmdx || true
if [[ "$(cd "$qmod" && find . -path ./rec\* -prune -o -print | sort)" == "$before" ]]; then
  printf 'ok   queued: building a lone command package leaves no binary in the module\n'
else
  printf 'FAIL queued: the run left files in the module: %s\n' "$(diff <(echo "$before") <(cd "$qmod" && find . -path ./rec\* -prune -o -print | sort) | grep '^>')"
  fails=$((fails + 1))
fi
# A go whose test-package listing fails: the pass fails rather than running nothing.
qgo=$(mktemp -d)
real_go_q=$(command -v go)
cat > "$qgo/go" <<FAKE
#!/usr/bin/env bash
[[ "\$1 \$2" == "list -f" && "\$3" == *TestGoFiles* ]] && { echo "go: injected failure" >&2; exit 1; }
exec "$real_go_q" "\$@"
FAKE
chmod +x "$qgo/go"
if queued_run "queued: a failed package listing fails the pass" 1 "$qmod/rec6" CAPSULE_TEST_JOBS=2 PATH="$qgo:$PATH"; then
  printf 'ok   queued: a failed package listing fails the pass\n'
fi
rm -rf "$qgo"
# The race pass leaves only modernc.org packages uninstrumented: a race planted
# in the module's own code still fails it, queued and as one go test run.
cat > "$qmod/a/race_test.go" <<'GO'
package a

import "testing"

func TestPlantedRace(t *testing.T) {
	n := 0
	done := make(chan bool)
	go func() { n++; done <- true }()
	n++
	<-done
	_ = n
}
GO
for jobs in 2 ""; do
  if queued_run "race pass with CAPSULE_TEST_JOBS=${jobs:-unset}" 1 "$qmod/rec-race" CAPSULE_TEST_PASS=race CAPSULE_TEST_JOBS="$jobs" \
    CAPSULE_TEST_PKGS=./a; then
    if grep -q 'WARNING: DATA RACE' "$qmod/rec-race.out"; then
      printf 'ok   race pass (CAPSULE_TEST_JOBS=%s): a race in the module'"'"'s own code is reported\n' "${jobs:-unset}"
    else
      printf 'FAIL race pass (CAPSULE_TEST_JOBS=%s): failed without a DATA RACE report\n' "${jobs:-unset}"
      fails=$((fails + 1))
    fi
  fi
done
rm "$qmod/a/race_test.go"
qtmp=$(mktemp -d)
printf 'package c\n\nfunc C() { undefined() }\n' > "$qmod/c/c.go"
if queued_run "queued: a package without tests still builds" 1 "$qmod/rec4" CAPSULE_TEST_JOBS=2 TMPDIR="$qtmp"; then
  printf 'ok   queued: a package without tests that does not build fails the pass\n'
fi
if [[ -z "$(find "$qtmp" -mindepth 1 -maxdepth 1)" ]]; then printf 'ok   queued: a failed build leaves no temporary files\n'; else
  printf 'FAIL queued: a failed build left %s\n' "$(find "$qtmp" -mindepth 1 -maxdepth 1 | head -3 | tr '\n' ' ')"
  fails=$((fails + 1))
fi
rm -rf "$qtmp"
rm -rf "$qmod"

# scripts/ci-local.sh (evidencebook#1038) against a fake act, in a fixture
# repository with its own origin: two synthetic workflows, one with a
# pull_request paths list, a Linux-only step and a windows job.
cl=$(mktemp -d)
mkdir -p "$cl/bin"
git init -q --bare "$cl/origin.git"
git clone -q "$cl/origin.git" "$cl/repo" 2> /dev/null
mkdir -p "$cl/repo/.github/workflows" "$cl/repo/scripts" "$cl/repo/docs"
cp "$here/../../scripts/ci-local.sh" "$cl/repo/scripts/"
cp -R "$here/../act" "$cl/repo/.github/act"
cp "$here/../leak_lint.py" "$here/../leak_lint_allowlist.txt" "$cl/repo/.github/"
cat > "$cl/repo/.github/workflows/always.yml" <<'WF'
on:
  pull_request:
  push:
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Everywhere
        run: true
      - uses: actions/setup-go@0000000000000000000000000000000000000000
      - name: Linux only
        if: env.ACT != 'true'
        run: true
  win:
    runs-on: windows-latest
    steps:
      - run: true
WF
cat > "$cl/repo/.github/workflows/docs.yml" <<'WF'
on:
  pull_request:
    paths:
      - "docs/**"
jobs:
  docs:
    runs-on: ubuntu-latest
    steps:
      - run: true
WF
cat > "$cl/repo/.github/workflows/flow.yml" <<'WF'
on:
  pull_request:
    paths: ['docs/**', "nowhere/**", 'odd/a,b.md']
jobs:
  flow:
    runs-on: ubuntu-latest
    steps:
      - run: true
WF
printf 'leak me\n' > "$cl/repo/docs/a.md"
printf 'readme\n' > "$cl/repo/README.md"
(cd "$cl/repo" && git add .github scripts docs README.md && git -c user.name=t -c user.email=t@t commit -qm base &&
  git push -q origin HEAD:main 2> /dev/null && git fetch -q origin)
# The fake act logs whether it saw LEAK_LINT_TERMS, the workflows it was given
# and the checkout's workflow files, then reports each given workflow's job.
cat > "$cl/bin/act" <<'FAKE'
#!/usr/bin/env bash
dir=""
locals=""
while (($#)); do
  [[ "$1" == -W ]] && dir=$2
  [[ "$1" == --local-repository ]] && locals+="$2 "
  shift
done
{
  echo "locals=$locals"
  echo "terms=${LEAK_LINT_TERMS:+seen}"
  echo "given=$(cd "$dir" && ls | sort | paste -sd ' ' -)"
  echo "checkout=$(ls .github/workflows | sort | paste -sd ' ' -)"
} >> "$ACT_LOG"
[[ -z "${FAKE_ACT_SLEEP:-}" ]] || sleep "$FAKE_ACT_SLEEP"
for f in "$dir"/*.yml; do
  if [[ -n "${FAKE_ACT_FAIL:-}" ]]; then echo "[${f##*/}/build]   ❌  Failure - Main step"; echo "[${f##*/}/build] 🏁  Job failed"; exit 1; fi
  echo "[${f##*/}/build]   ✅  Success - Main step [1m2.5s]"
  echo "[${f##*/}/build]   ✅  Success - Post step [500ms]"
  echo "[${f##*/}/build] 🏁  Job succeeded"
done
FAKE
chmod +x "$cl/bin/act"
cl_run() { # description, want exit, changed file on the branch, env...
  local what=$1 want=$2 change=$3 st=0
  shift 3
  (cd "$cl/repo" && git checkout -q -B t origin/main && mkdir -p "$(dirname "$change")" && echo x >> "$change" && git add "$change" &&
    git -c user.name=t -c user.email=t@t commit -qm change)
  : > "$cl/act.log"
  (cd "$cl/repo" && env PATH="$cl/bin:$PATH" ACT_LOG="$cl/act.log" "$@" bash scripts/ci-local.sh) > "$cl/out" 2>&1 || st=$?
  if ((st != want)); then
    printf 'FAIL %s: exit %d, want %d\n%s\n' "$what" "$st" "$want" "$(tail -15 "$cl/out")"
    fails=$((fails + 1))
    return 1
  fi
}
check() { # description, command...
  local what=$1
  shift
  if "$@"; then printf 'ok   %s\n' "$what"; else
    printf 'FAIL %s\n%s\n' "$what" "$(tail -15 "$cl/out")"
    fails=$((fails + 1))
  fi
}
standins_from_clone() {
  local l
  l=$(grep '^locals=' "$cl/act.log")
  [[ "$l" == *"actions/setup-go@0000000000000000000000000000000000000000="*"/.github/act/setup-go "* && "$l" != *"=$cl/repo/"* ]]
}
if cl_run "ci-local: a change outside docs/" 0 README.md LEAK_LINT_TERMS='{"x":["never-present-term"]}'; then
  check "ci-local: a paths-filtered workflow does not run for a change outside its paths" grep -qx 'given=always.yml' "$cl/act.log"
  check "ci-local: the leak-lint term list never reaches act" grep -qx 'terms=' "$cl/act.log"
  check "ci-local: the checkout act runs in keeps every workflow file" grep -qx 'checkout=always.yml docs.yml flow.yml' "$cl/act.log"
  check "ci-local: the summary names the Linux-only step" grep -q 'step always.yml/build: Linux only' "$cl/out"
  check "ci-local: the summary names the job on another runner" grep -q 'runner always.yml/win (windows-latest)' "$cl/out"
  check "ci-local: the base revision's scanner ran with the given terms" grep -qx 'leak-lint: ok' "$cl/out"
  check "ci-local: the summary gives each job's time, the sum of its steps" grep -qE '^ok +63s  always\.yml/build$' "$cl/out"
  check "ci-local: the stand-ins come from the merged tree, not the checkout" standins_from_clone
fi
if cl_run "ci-local: a docs/ change" 0 docs/a.md; then
  check "ci-local: a docs/ change runs the block-list and the quoted flow-list workflows too" \
    grep -qx 'given=always.yml docs.yml flow.yml' "$cl/act.log"
  check "ci-local: without a term list, leak-lint is reported skipped" grep -q '^leak-lint: skipped: no local term list' "$cl/out"
fi
cl_run "ci-local: a change to a quoted path holding a comma" 0 'odd/a,b.md' &&
  check "ci-local: a quoted flow-list path holding a comma matches whole" grep -qx 'given=always.yml flow.yml' "$cl/act.log"
cl_run "ci-local: leak-lint finds a term in a tracked file" 1 README.md LEAK_LINT_TERMS='{"x":["leak me"]}' &&
  check "ci-local: leak-lint finds a term in a tracked file and fails" grep -qx 'leak-lint: FAIL' "$cl/out"
cl_run "ci-local: a failing job fails the run" 1 README.md FAKE_ACT_FAIL=1 &&
  check "ci-local: a failing job's step is printed" grep -q 'Failure - Main step' "$cl/out"
check "ci-local: a failed job with no step time still has its summary line" grep -qE '^FAIL +0s  always\.yml/build$' "$cl/out"
cl_run "ci-local: a hung act run is stopped at CI_LOCAL_TIMEOUT" 1 README.md FAKE_ACT_SLEEP=30 CI_LOCAL_TIMEOUT=2s &&
  check "ci-local: the timeout is reported" grep -q 'passed CI_LOCAL_TIMEOUT' "$cl/out"
# A workflow main gained after the branch left it runs: the merged tree's
# workflows are the ones GitHub would run.
(cd "$cl/repo" && git checkout -q -B late origin/main && sed 's/docs:/late:/' .github/workflows/docs.yml |
  sed 's/"docs\/\*\*"/"README.md"/' > .github/workflows/late.yml && git add .github/workflows/late.yml &&
  git -c user.name=t -c user.email=t@t commit -qm late && git push -q origin HEAD:main 2> /dev/null && git fetch -q origin &&
  git checkout -q -B t origin/main~1 && echo x >> README.md && git add README.md &&
  git -c user.name=t -c user.email=t@t commit -qm change)
: > "$cl/act.log"
if (cd "$cl/repo" && env PATH="$cl/bin:$PATH" ACT_LOG="$cl/act.log" bash scripts/ci-local.sh) > "$cl/out" 2>&1; then
  check "ci-local: a workflow main gained after the branch diverged runs" grep -qx 'given=always.yml late.yml' "$cl/act.log"
else
  printf 'FAIL ci-local: the run with a late base workflow exited nonzero\n%s\n' "$(tail -15 "$cl/out")"
  fails=$((fails + 1))
fi
# chrome.sh leaves a lock another install holds, and its install, alone.
if [[ "$(uname -s)" == Darwin ]]; then
  mkdir -p "$cl/cache/chrome/0.0.0-mac-$(uname -m | sed 's/x86_64/x64/').lock"
  if EB_CI_CACHE="$cl/cache" CHROME_LOCK_WAIT=1 bash "$cl/repo/.github/act/chrome.sh" 0.0.0 > /dev/null 2>&1; then
    printf 'FAIL chrome.sh: ran while another install held the lock\n'
    fails=$((fails + 1))
  elif [[ -d "$cl/cache/chrome/0.0.0-mac-$(uname -m | sed 's/x86_64/x64/').lock" ]]; then
    printf 'ok   chrome.sh: a held lock makes it stop and stays in place\n'
  else
    printf 'FAIL chrome.sh: removed a lock it did not take\n'
    fails=$((fails + 1))
  fi
fi
if (cd "$cl/repo" && bash scripts/ci-local.sh --list) > "$cl/out" 2>&1; then
  check "ci-local --list: names what does not run here" grep -q 'runner always.yml/win (windows-latest)' "$cl/out"
else
  printf 'FAIL ci-local --list exited nonzero\n'
  fails=$((fails + 1))
fi
rm -rf "$cl"

if ((fails)); then
  printf '%d failed\n' "$fails"
  exit 1
fi
