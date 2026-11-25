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
