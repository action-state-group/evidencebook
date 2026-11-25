#!/usr/bin/env bash
# Prints true when any changed path read on stdin (repository-root relative,
# as `git diff --name-only` prints them) matches the workflow's own
# `on.push.paths` list, else false (evidencebook#925). A workflow that runs on
# every pull request, so its aggregate can be a required check, uses this to
# skip its jobs when the pull request touches none of the paths its push
# trigger watches; the push list stays the one source of those paths.
#
#   paths-changed.sh .github/workflows/rust.yml < changed-paths
#
# Globs follow GitHub's filter patterns as these workflows use them: `**`
# matches any characters, `*` any characters but `/`. The list must be the
# one-line flow sequence `paths: ["a/**", "b"]` under `push:`; anything else
# is an error, so a reshaped trigger fails the job rather than skipping it.
set -euo pipefail

workflow=${1:?usage: paths-changed.sh WORKFLOW < changed-paths}
list=$(perl -ne '$in = 1 if /^  push:\s*$/; $in = 0 if /^  [a-z_]+:/ && !/^  push:/;
  if ($in && /^    paths:\s*\[(.*)\]\s*$/) { print $1; exit }' "$workflow")
if [[ -z "$list" ]]; then
  printf '%s: no one-line on.push.paths list\n' "$workflow" >&2
  exit 1
fi
regex=$(printf '%s\n' "$list" | perl -ne '
  my @globs = /"([^"]+)"/g;
  my @res = map {
    my $g = quotemeta($_);
    $g =~ s/\\\*\\\*/\x01/g;
    $g =~ s/\\\*/[^\/]*/g;
    $g =~ s/\x01/.*/g;
    $g
  } @globs;
  print "^(", join("|", @res), ")\$";')
REGEX=$regex perl -ne 'if (/$ENV{REGEX}/) { $hit = 1 } END { print $hit ? "true\n" : "false\n" }'
