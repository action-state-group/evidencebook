#!/usr/bin/env bash
# Runs this branch's pull request CI on this machine (evidencebook#1038): every
# job of every workflow a pull request triggers, from the workflow files
# themselves, with act (https://nektosact.com) running the steps on the host.
# The workflows stay the one description of what a pull request checks.
#
#   scripts/ci-local.sh [--list] [workflow ...]
#
# It clones the repository (sharing its objects) into a temporary directory and
# makes the commit GitHub tests for a pull request: HEAD merged into
# origin/main, so a job's `git diff HEAD^1 HEAD` sees the pull request's
# changes, and a workflow whose pull_request trigger has a paths list runs only
# when a changed path matches it (act itself ignores those lists). The setup
# actions are replaced by the stand-ins in .github/act/ (this machine's go, the
# workflow's Rust toolchain through rustup, a uv virtual environment per job,
# Chrome for Testing at the pinned version).
#
# Not run here, and listed by --list and in every summary: steps that need
# Linux (they carry `if: env.ACT != 'true'`; their jobs report "partial"),
# jobs on other runners (Rust's windows job), and leak-lint while this machine
# has no term list (LEAK_LINT_TERMS, a repository secret; decided on evidencebook#1038
# 2026-11-27: CI-only until a local list exists). With LEAK_LINT_TERMS set, the
# base revision's scanner and allowlist read the merged tree; act never sees
# the variable.
#
# Workflows: the arguments (file names under .github/workflows), else every
# workflow with a pull_request trigger. CI_LOCAL_JOBS caps concurrent jobs
# (default: the gate's CPU bound, ASG_GATE_CPUS, else half the CPUs); a job's
# matrix runs inside one of those slots, so a matrix of k adds up to k-1 more.
# CI_LOCAL_TIMEOUT bounds the whole act run (default 40m; act does not apply
# timeout-minutes). Exit status: 0 when every job that ran passed; 1 when a
# job failed or the run timed out; 2 when the setup failed.
set -uo pipefail

root=$(git rev-parse --show-toplevel) || exit 2
cd "$root" || exit 2
list=0
wfs=()
for arg in "$@"; do
  case "$arg" in
    --list) list=1 ;;
    -*) echo "usage: scripts/ci-local.sh [--list] [workflow ...]" >&2; exit 2 ;;
    *) wfs+=("$arg") ;;
  esac
done
# The workflows a run reads: this checkout's for --list, the merged tree's
# (what GitHub would run, base changes included) for a run.
wfdir=.github/workflows
choose() {
  local f
  if ((${#wfs[@]} == 0)); then
    while read -r f; do wfs+=("${f##*/}"); done < <(grep -lE '^  pull_request:' "$wfdir"/*.yml | sort)
  fi
  for f in "${wfs[@]}"; do [[ -f "$wfdir/$f" ]] || { echo "no workflow $wfdir/$f" >&2; exit 2; }; done
}

# What does not run here: jobs on a runner other than Ubuntu, and steps that
# skip themselves under act. Lines: "runner <workflow>/<job> (<runs-on>)" and
# "step <workflow>/<job>: <step name>".
exclusions() {
  local f
  for f in "${wfs[@]}"; do
    awk -v wf="$f" '
      /^jobs:/ { jobs = 1; next }
      jobs && /^  [a-z0-9_-]+:$/ { job = $1; sub(/:$/, "", job); step = "" }
      jobs && /^    runs-on:/ && $2 !~ /^ubuntu/ { print "runner " wf "/" job " (" $2 ")" }
      jobs && /^      - name: / { step = $0; sub(/^      - name: /, "", step) }
      jobs && /^        if: env\.ACT != .true.$/ { print "step " wf "/" job ": " step }' "$wfdir/$f"
  done
}
if ((list)); then
  choose
  printf 'workflows: %s\n' "${wfs[*]}"
  printf 'not run here:\n'
  exclusions | sed 's/^/  /'
  printf '  leak-lint (%s)\n' "$([[ -n "${LEAK_LINT_TERMS:-}" ]] && echo 'runs: LEAK_LINT_TERMS is set' || echo 'no local term list')"
  exit 0
fi
command -v act > /dev/null || { echo "act is not installed (brew install act)" >&2; exit 2; }

n=${CI_LOCAL_JOBS:-${ASG_GATE_CPUS:-$(($(getconf _NPROCESSORS_ONLN) / 2))}}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
head=$(git rev-parse HEAD) || exit 2
base=$(git rev-parse origin/main) || { echo "no origin/main; fetch it" >&2; exit 2; }
# A plain clone, not a worktree: act's git library rejects worktree config.
git clone -q --shared --no-checkout "$root" "$work/repo" || exit 2
(
  cd "$work/repo" &&
    git -c advice.detachedHead=false checkout -q "$base" &&
    git -c user.name=ci-local -c user.email=ci-local@localhost merge -q --no-ff --no-edit "$head" &&
    # A branch, so .git/refs is not empty: act copies the checkout without
    # empty directories, and a .git without refs/ is no repository at all (a
    # clone of a detached HEAD has no branch of its own).
    git checkout -q -B ci-local
) > "$work/merge.log" 2>&1 || {
  echo "HEAD does not merge cleanly into origin/main; rebase first" >&2
  cat "$work/merge.log" >&2
  exit 2
}
git -C "$work/repo" diff --no-renames --name-only HEAD^1 HEAD > "$work/changed" || exit 2
# act names the repository (github.repository, an actions/checkout default) after
# origin's URL, which for this clone is a local path.
git -C "$work/repo" remote set-url origin https://github.com/action-state-group/evidencebook.git || exit 2
wfdir="$work/repo/.github/workflows"
choose

# act's stand-ins: every `uses:` of these actions, at whatever ref a workflow
# pins, is replaced by the local action of the same purpose.
declare -A standin=(
  [actions/setup-go]=setup-go
  [actions/setup-python]=setup-python
  [dtolnay/rust-toolchain]=rust-toolchain
  [browser-actions/setup-chrome]=setup-chrome
  [actions/upload-artifact]=noop
)
locals=()
while read -r use; do
  [[ -n "${standin[${use%@*}]:-}" ]] && locals+=(--local-repository "$use=$work/repo/.github/act/${standin[${use%@*}]}")
done < <(grep -hoE 'uses: [^ ]+@[0-9a-f]{40}' "$wfdir"/*.yml | sed 's/^uses: //' | sort -u)


# A pull_request trigger's paths list, as GitHub applies it: the workflow runs
# when a changed path matches a glob (`**` any characters, `*` any but `/`).
# Only `paths` is supported; any other filter under pull_request is an error,
# so a reshaped trigger fails here rather than running the wrong set.
selected=()
for f in "${wfs[@]}"; do
  globs=$(perl -ne '
    if (/^  pull_request:\s*$/) { $in = 1; next }
    if ($in && /^  \S/) { $in = 0 }
    next unless $in;
    if (/^    paths:\s*\[(.*)\]\s*$/) {
      # Items: "double", \x27single\x27 or bare, split on the commas between them.
      my $l = $1;
      while ($l =~ /\G\s*(?:"([^"]*)"|\x27([^\x27]*)\x27|([^,"\x27]*?))\s*(,|$)/gc) {
        my $g = defined $1 ? $1 : defined $2 ? $2 : $3;
        print "$g\n" if length $g;
        last if $4 eq "";
      }
      if (pos($l) != length($l)) { print STDERR "unparsed paths list in $ARGV: $_"; exit 3 }
      next
    }
    if (/^    paths:\s*$/) { $list = 1; next }
    if ($list && /^      - (.+?)\s*$/) { (my $g = $1) =~ s/^(["\x27])(.*)\1$/$2/; print "$g\n"; next }
    if (/^    [a-z_-]+:/) { print STDERR "unsupported pull_request filter in $ARGV: $_"; exit 3 }
    $list = 0 if /^    \S/;' "$wfdir/$f") || exit 2
  if [[ -z "$globs" ]]; then selected+=("$f"); continue; fi
  regex=$(printf '%s\n' "$globs" | perl -ne 'chomp; my $g = quotemeta($_); $g =~ s/\\\*\\\*/\x01/g; $g =~ s/\\\*/[^\/]*/g; $g =~ s/\x01/.*/g; push @r, $g; END { print "^(", join("|", @r), ")\$" }')
  if REGEX=$regex perl -ne 'if (/$ENV{REGEX}/) { $hit = 1 } END { exit($hit ? 0 : 1) }' "$work/changed"; then
    selected+=("$f")
  else
    printf 'not triggered (no changed path matches its pull_request paths): %s\n' "$f"
  fi
done

# Steps that run `google-chrome` before a job sets up its pinned Chrome get
# the pinned Chrome for Testing (setup-chrome's version) under that name.
mkdir -p "$work/bin" "$work/workflows"
chrome_version=""
if ((${#selected[@]})) && grep -q 'google-chrome' "${selected[@]/#/$wfdir/}"; then
  chrome_version=$(grep -hA3 'browser-actions/setup-chrome@' "$wfdir"/*.yml | awk '/chrome-version:/ {print $2; exit}')
  [[ -n "$chrome_version" ]] || { echo "no setup-chrome chrome-version to pin google-chrome to" >&2; exit 2; }
fi
if [[ -n "$chrome_version" ]]; then
  chrome=$("$work/repo/.github/act/chrome.sh" "$chrome_version") || { echo "could not install Chrome $chrome_version" >&2; exit 2; }
  # A wrapper, not a symlink: Chrome finds its framework next to its own path.
  printf '#!/bin/sh\nexec "%s" "$@"\n' "$chrome" > "$work/bin/google-chrome"
  chmod +x "$work/bin/google-chrome"
fi
export PATH="$work/bin:$PATH"

status=0
start=$SECONDS
: > "$work/jobs"
if ((${#selected[@]})); then
  # act reads the chosen workflows from their own directory; the checkout it
  # runs in stays whole.
  # act does not evaluate an expression in strategy.max-parallel (it then runs
  # the matrix one entry at a time: capsulectl's three plain shards back to
  # back). Here the bound is --concurrent-jobs, so the copy states the same
  # number.
  for f in "${selected[@]}"; do
    sed -E "s/^( +max-parallel: )\\$\{\{.*\}\}[[:space:]]*$/\1$n/" "$wfdir/$f" > "$work/workflows/$f"
  done
  cat > "$work/event.json" <<EOF
{"pull_request": {"number": 0, "head": {"sha": "$head", "ref": "local", "repo": {"full_name": "action-state-group/evidencebook"}},
 "base": {"sha": "$base", "ref": "main", "repo": {"full_name": "action-state-group/evidencebook"}}},
 "repository": {"full_name": "action-state-group/evidencebook"}}
EOF
  # The leak-lint term list never reaches a job: act imports this
  # environment into every job it runs on the host. No pager either: a step's
  # `git diff` would otherwise wait in one for input that never comes.
  # act keeps a copy of the checkout per job under $XDG_CACHE_HOME/act and never
  # removes it; this run's copies live in its work directory and go with it.
  (cd "$work/repo" && env -u LEAK_LINT_TERMS XDG_CACHE_HOME="$work/xdg" timeout --kill-after=30 "${CI_LOCAL_TIMEOUT:-40m}" \
    act pull_request -W "$work/workflows" -e "$work/event.json" \
    -P ubuntu-latest=-self-hosted -P ubuntu-24.04=-self-hosted --concurrent-jobs "$n" \
    "${locals[@]}" --env EB_CI_CHROME_SH="$work/repo/.github/act/chrome.sh" \
    --env EB_CI_CACHE="${EB_CI_CACHE:-$HOME/Library/Caches/evidencebook-ci}" \
    --env GIT_PAGER=cat --env PAGER=cat --env UV_CACHE_DIR="${UV_CACHE_DIR:-$HOME/.cache/uv}") < /dev/null > "$work/act.log" 2>&1 || status=$?
  if ((status == 124 || status == 137)); then
    printf 'the act run passed CI_LOCAL_TIMEOUT (%s) and was stopped\n' "${CI_LOCAL_TIMEOUT:-40m}"
  fi
  ((status == 0)) || status=1
  # Each job's verdict and time: act prints "[<workflow>/<job>] 🏁  Job
  # succeeded|failed" and each step's duration in brackets; a job's time is
  # the sum of its steps' (act prints no job time).
  perl -ne '
    next unless /^\[(.+?)\s*\]\s+(?:✅  Success|❌  Failure) - .*\[(\S+)\]\s*$/;
    my ($j, $d) = ($1, $2); my $s = 0;
    while ($d =~ /([\d.]+)(h|ms|µs|us|ns|m|s)/g) {
      $s += $1 * { h => 3600, m => 60, s => 1, ms => 1e-3, "µs" => 1e-6, us => 1e-6, ns => 1e-9 }->{$2};
    }
    $t{$j} += $s;
    END { printf "%s\t%d\n", $_, $t{$_} for keys %t }' "$work/act.log" > "$work/times"
  grep -E '🏁  Job (succeeded|failed)' "$work/act.log" |
    sed -E 's/^\[([^]]*)\] *🏁  Job (succeeded|failed)$/\2  \1/; s/ +$//' |
    awk -F'  ' 'FILENAME == ARGV[1] { split($0, a, "\t"); t[a[1]] = a[2]; next } { printf "%s  %5ss  %s\n", $1, t[$2] + 0, $2 }' "$work/times" - |
    sort -k2,2nr > "$work/jobs"
  if ((status)) || grep -q '^failed' "$work/jobs"; then
    status=1
    printf '== failing steps ==\n'
    grep -B60 -E '❌  Failure - ' "$work/act.log" | grep -vE 'level=(warning|info)' | tail -150
    grep -q '❌' "$work/act.log" || tail -40 "$work/act.log"
    # The whole log outlives the run's directory, so a failure can be read in full.
    kept=$(mktemp "${TMPDIR:-/tmp}/ci-local-act-XXXXXX") && cp "$work/act.log" "$kept" &&
      printf 'the whole act log: %s\n' "$kept"
  fi
fi

# leak-lint (pull_request_target on GitHub): the base revision's scanner and
# allowlist over the merged tree, as the workflow runs them.
leak="skipped: no local term list (CI-only until one exists, evidencebook#1038)"
if [[ -n "${LEAK_LINT_TERMS:-}" ]]; then
  mkdir -p "$work/trusted/.github"
  for f in leak_lint.py leak_lint_allowlist.txt; do
    git -C "$work/repo" show "$base:.github/$f" > "$work/trusted/.github/$f" || exit 2
  done
  if python3 "$work/trusted/.github/leak_lint.py" "$work/repo" > "$work/leak.log" 2>&1; then leak=ok; else
    leak=FAIL
    status=1
    cat "$work/leak.log"
  fi
fi

printf '\n== jobs (%ds) ==\n' $((SECONDS - start))
sed 's/^succeeded/ok  /; s/^failed/FAIL/' "$work/jobs"   # slowest first
printf 'leak-lint: %s\n' "$leak"
printf 'not run here (a job with a step listed passed only in part):\n'
exclusions | sed 's/^/  /'
exit "$status"
