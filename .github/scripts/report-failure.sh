#!/usr/bin/env bash
# Reports a red run on main or a nightly so it reaches a person the same day
# (evidencebook#925): comments the run link $RUN on the one open issue titled
# $TITLE, or opens it. Either way the issue carries the ci-red label (the
# coordinator's watchdog lists open ci-red issues) and is assigned to
# $ASSIGNEE when that is set. Two reporters racing can both open one: after
# opening, the reporter waits for search to settle, keeps the lowest-numbered
# open issue, puts its own run there and closes the others. Needs GH_TOKEN
# with issues: write.
set -euo pipefail
: "${TITLE:?}" "${RUN:?}"
repo=$GITHUB_REPOSITORY

open_issues() {
  gh issue list --repo "$repo" --state open --search "\"$TITLE\" in:title" --json number,title \
    --jq "map(select(.title == env.TITLE) | .number) | sort | .[]"
}
# Label and assign an issue, also one an earlier reporter opened without them.
mark() {
  local args=(--add-label ci-red)
  [[ -z "${ASSIGNEE:-}" ]] || args+=(--add-assignee "$ASSIGNEE")
  gh issue edit "$1" --repo "$repo" "${args[@]}" > /dev/null
}

gh label create ci-red --repo "$repo" --color B60205 \
  --description "A workflow failed on main or a nightly" 2>/dev/null || true

found=$(open_issues)
n=${found%%$'\n'*}
if [[ -n "$n" ]]; then
  mark "$n"
  gh issue comment "$n" --repo "$repo" --body "Failed again: $RUN"
  exit 0
fi
url=$(gh issue create --repo "$repo" --title "$TITLE" \
  --body "Failed: $RUN. Close this issue once the workflow passes again.")
echo "$url"
mine=${url##*/}
mark "$mine"

# Search is eventually consistent: give a racing reporter's issue time to show.
sleep "${REPORT_SETTLE_SECONDS:-20}"
found=$(open_issues)
keep=${found%%$'\n'*}
# Search may still miss our own issue; then nothing else is known to exist.
if [[ -z "$keep" || "$keep" == "$mine" ]]; then
  exit 0
fi
# Another reporter's issue is older: our run goes there and ours closes.
mark "$keep"
gh issue comment "$keep" --repo "$repo" --body "Also failed: $RUN"
gh issue close "$mine" --repo "$repo" --comment "Duplicate of #$keep."
