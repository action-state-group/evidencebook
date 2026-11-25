#!/usr/bin/env bash
# Prints the packages of one Go module a pull request's race job runs, one per
# line (./internal/book form, relative to the module), from the changed paths
# read on stdin (repository-root relative, as `git diff --no-renames
# --name-only` prints them). Empty output means no race job (evidencebook#925).
#
#   race-packages.sh root|capsulectl < changed-paths
#
# A changed path inside the module belongs to the deepest package directory
# holding it, its testdata and embedded files included; at the root module, any
# other path belongs to the root package, whose tests read schemas/, docs/,
# python/ and rust/ files. A Go file in no package directory (a new or deleted
# package) races everything. A package is raced when it, or any package its
# tests import, changed. Per module below:
#   full    a path matching it (a shared input: the module files, this script,
#           the workflow, a fixture tree tests read from outside their package)
#           races every package
#   ignore  a path matching it changes nothing here (nested modules, prose)
#   also    GLOB=PKG: a path matching it also marks PKG changed (a package
#           whose tests read another package's files)
#   exclude never raced on a pull request: internal/deal's race pass is ten
#           shards of about twelve minutes; it runs on push to main, the
#           nightly, and locally before push (CLAUDE.md)
# Globs match the repository-root relative path; `*` also matches `/`.
set -euo pipefail

full_globs=() ignore_globs=() also=() exclude=()
case "${1:-}" in
  root)
    module_dir=.
    full_globs=(go.mod go.sum .github/scripts/race-packages.sh .github/workflows/ci.yml 'testdata/*')
    ignore_globs=('capsulectl/*' 'report/*' '.github/*' 'changelog.d/*' '*.md' LICENSE)
    ;;
  capsulectl)
    module_dir=capsulectl
    full_globs=(capsulectl/go.mod capsulectl/go.sum capsulectl/scripts/test.sh .github/scripts/race-packages.sh
      .github/workflows/capsulectl.yml 'capsulectl/testdata/*' 'capsulectl/third_party/*' 'report/testdata/*'
      'testdata/card-registration/*' 'testdata/live16-ask/*')
    # cmd's tests build the launchers and read internal/deal/testdata.
    also=('capsulectl/internal/*=./cmd')
    exclude=(./internal/deal)
    ;;
  *)
    echo 'usage: race-packages.sh root|capsulectl < changed-paths' >&2
    exit 2
    ;;
esac

cd "$(git rev-parse --show-toplevel)/$module_dir"
prefix=""
[[ "$module_dir" == . ]] || prefix="${module_dir%/}/"
module=$(go list -m)
# Each go list runs in a checked assignment, so a resolution error fails the
# script instead of yielding an empty plan.
listed=$(go list -f '{{.ImportPath}}' ./...)
declare -A known=()
while IFS= read -r p; do [[ -z "$p" ]] || known["$p"]=1; done <<<"$listed"
if ((${#known[@]} == 0)); then
  echo "go list found no packages in $module_dir" >&2
  exit 1
fi

matches() { # path glob...
  local path=$1 g
  shift
  for g in "$@"; do
    # shellcheck disable=SC2053 # the glob is meant to match as a pattern
    [[ "$path" == $g ]] && return 0
  done
  return 1
}

declare -A changed=()
full=0
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  if ((${#full_globs[@]})) && matches "$path" "${full_globs[@]}"; then
    full=1
    continue
  fi
  if ((${#ignore_globs[@]})) && matches "$path" "${ignore_globs[@]}"; then
    continue
  fi
  for a in ${also[@]+"${also[@]}"}; do
    if matches "$path" "${a%%=*}"; then
      target=${a#*=}
      changed["$module/${target#./}"]=1
    fi
  done
  [[ -z "$prefix" || "$path" == "$prefix"* ]] || continue
  dir=$(dirname "${path#"$prefix"}")
  while :; do
    if [[ "$dir" == . ]]; then
      if [[ -n "${known[$module]+x}" ]]; then
        changed["$module"]=1
      elif [[ "$path" == *.go ]]; then
        full=1
      fi
      break
    fi
    if [[ -n "${known[$module/$dir]+x}" ]]; then
      changed["$module/$dir"]=1
      break
    fi
    dir=$(dirname "$dir")
  done
done

raced=()
for pkg in "${!known[@]}"; do
  if [[ "$pkg" == "$module" ]]; then rel=.; else rel="./${pkg#"$module"/}"; fi
  if ((${#exclude[@]})) && matches "$rel" "${exclude[@]}"; then
    continue
  fi
  if ((full)); then
    raced+=("$rel")
    continue
  fi
  deps=$(go list -test -deps -f '{{.ImportPath}}' "$rel")
  while IFS= read -r dep; do
    dep=${dep%% \[*}
    dep=${dep%.test}
    if [[ -n "${changed[$dep]+x}" ]]; then
      raced+=("$rel")
      break
    fi
  done <<<"$deps"
done
((${#raced[@]} == 0)) || printf '%s\n' "${raced[@]}" | sort
