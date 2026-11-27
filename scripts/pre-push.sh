#!/usr/bin/env bash
# The check to run before every push (evidencebook#925, #1038): every job a
# pull request runs in CI, run here, plus the one check no pull request runs.
#
#   scripts/pre-push.sh [--plan]
#
# 1. scripts/ci-local.sh: every job of every workflow a pull request triggers,
#    from the workflow files, on HEAD merged into origin/main (each workflow's
#    own changes job picks its jobs from that merge, as on GitHub). It names
#    what it cannot run on this machine.
# 2. internal/deal's race pass (capsulectl/scripts/test.sh, queued over
#    PRE_PUSH_SHARDS processes, default the gate's CPU bound): when the change
#    reaches that package (.github/scripts/race-packages.sh with
#    RACE_PACKAGES_LOCAL=1, evidencebook#973). Pull requests never run it; push
#    to main and the nightly do.
#
# --plan prints what it would run and runs nothing. PRE_PUSH_CHANGED=<file>
# reads the changed paths from that file instead of git (for the race
# decision; ci-local reads its own merge), and PRE_PUSH_CI_LOCAL=<command>
# runs that instead of scripts/ci-local.sh (the CI scripts tests use both).
# Each step reports its time; the script exits nonzero if any failed.
set -uo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root" || exit 2
plan=0
for arg in "$@"; do
  case "$arg" in
    --plan) plan=1 ;;
    *) echo "usage: scripts/pre-push.sh [--plan]" >&2; exit 2 ;;
  esac
done

# ci-local tests committed HEAD, so a run over uncommitted changes would pass
# them untested (asg-local-check refuses a dirty tree too).
if [[ -z "${PRE_PUSH_CI_LOCAL:-}" && "$plan" == 0 && -n "$(git status --porcelain)" ]]; then
  echo "the checkout has uncommitted changes; this check tests HEAD, so commit them first" >&2
  exit 2
fi

changed=$(mktemp)
log=$(mktemp -d)
trap 'rm -rf "$changed" "$log"' EXIT
# Selection errors stop the script: a check that silently skipped a step would
# pass without running what the change needs.
if [[ -n "${PRE_PUSH_CHANGED:-}" ]]; then
  cp "$PRE_PUSH_CHANGED" "$changed" || exit 2
else
  base=$(git merge-base HEAD origin/main) || { echo "no merge base with origin/main; fetch it" >&2; exit 2; }
  git diff --no-renames --name-only "$base" HEAD > "$changed" || { echo "git diff failed" >&2; exit 2; }
fi
race=$(RACE_PACKAGES_LOCAL=1 bash .github/scripts/race-packages.sh capsulectl < "$changed") || { echo "race-packages.sh capsulectl failed" >&2; exit 2; }
deal=0
grep -qx ./internal/deal <<<"$race" && deal=1

if ((plan)); then
  printf 'ci-local: %s\n' "${PRE_PUSH_CI_LOCAL:-scripts/ci-local.sh}"
  printf 'internal/deal race: %s\n' "$( ((deal)) && echo yes || echo no)"
  exit 0
fi

results=()
status=0
step() { # name, command...
  local name=$1 start st=0
  shift
  printf '\n== %s ==\n' "$name"
  start=$SECONDS
  "$@" || st=$?
  results+=("$(printf '%-20s %-4s %5ss' "$name" "$([[ $st == 0 ]] && echo ok || echo FAIL)" $((SECONDS - start)))")
  ((st == 0)) || status=1
}

# internal/deal's race pass in PRE_PUSH_SHARDS processes; the queue prints
# only failing batches in full, so a failed pass prints all of its output.
# shellcheck disable=SC2329 # runs through step()
deal_race() {
  local n=${PRE_PUSH_SHARDS:-${ASG_GATE_CPUS:-$(($(getconf _NPROCESSORS_ONLN) / 2))}} st=0
  ((n >= 1)) || n=1
  (cd capsulectl && CAPSULE_TEST_PASS=race CAPSULE_TEST_SHARD=1/1 CAPSULE_TEST_JOBS=$n CAPSULE_TEST_PKGS=./internal/deal \
    CAPSULE_TEST_RACE_TIMEOUT=60m bash scripts/test.sh > "$log/deal-race.log" 2>&1) || st=1
  if ((st)); then cat "$log/deal-race.log"; else grep -E '^== .* (over [0-9]+ processes|tests reported)' "$log/deal-race.log" || true; fi
  return "$st"
}

# shellcheck disable=SC2086 # PRE_PUSH_CI_LOCAL is a command line
step ci-local ${PRE_PUSH_CI_LOCAL:-bash scripts/ci-local.sh}
if ((deal)); then step "internal/deal race" deal_race; else
  results+=("$(printf '%-20s %s' "internal/deal race" 'skipped (the change does not reach it)')")
fi

printf '\n== summary ==\n'
printf '%s\n' "${results[@]}"
exit "$status"
