#!/usr/bin/env bash
# Puts the action owner/repo at a full commit sha in a per-machine cache and
# prints its directory, for scripts/ci-local.sh to hand act with
# --local-repository (evidencebook#1061): act then reads the action from that
# directory and never fetches it into its own go-git cache, where concurrent
# jobs race.
#
#   .github/act/fetch-action.sh <owner/repo> <sha> <cache directory>
#
# The entry <cache>/<owner>/<repo>@<sha> appears whole or not at all: the files
# are checked out in a temporary directory beside it, from a fetch that must
# resolve to that sha, and renamed into place in one step. When two runs fetch
# the same action at once, the second rename fails, and that run uses the entry
# the first one made. EB_ACT_ACTION_URL (tests) replaces https://github.com as
# the base of the clone URL; EB_ACT_FETCH_TIMEOUT bounds the fetch (default
# 300 seconds).
set -euo pipefail

usage() { echo "usage: fetch-action.sh <owner/repo> <sha> <cache directory>" >&2; exit 2; }
(($# == 3)) || usage
repo=$1 sha=$2 cache=$3
[[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ && "$repo" != *..* ]] || { echo "fetch-action.sh: not owner/repo: $repo" >&2; exit 2; }
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "fetch-action.sh: not a full commit sha: $sha" >&2; exit 2; }

dest="$cache/$repo@$sha"
if [[ -d "$dest" ]]; then
  printf '%s\n' "$dest"
  exit 0
fi
mkdir -p "$cache/${repo%/*}"
tmp=$(mktemp -d "$cache/${repo%/*}/.fetch.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
url="${EB_ACT_ACTION_URL:-https://github.com}/$repo"
export GIT_TERMINAL_PROMPT=0
git init -q --bare "$tmp/git"
if ! timeout "${EB_ACT_FETCH_TIMEOUT:-300}" git -C "$tmp/git" fetch -q --depth 1 "$url" "$sha" < /dev/null; then
  echo "fetch-action.sh: could not fetch $repo at $sha from $url" >&2
  exit 1
fi
got=$(git -C "$tmp/git" rev-parse FETCH_HEAD)
[[ "$got" == "$sha" ]] || { echo "fetch-action.sh: $url gave $got for $sha" >&2; exit 1; }
# A checkout, not git archive, so export-ignore attributes keep nothing out.
mkdir "$tmp/tree"
git -C "$tmp/git" --work-tree="$tmp/tree" checkout -q -f "$sha" -- .
# rename(2) onto an existing entry fails (it is a nonempty directory); that
# entry is another run's complete copy.
perl -e 'rename($ARGV[0], $ARGV[1]) or -d $ARGV[1] or die "rename $ARGV[0]: $!\n"' "$tmp/tree" "$dest"
printf '%s\n' "$dest"
