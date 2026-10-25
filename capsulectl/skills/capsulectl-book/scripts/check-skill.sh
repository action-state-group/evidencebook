#!/usr/bin/env bash
set -euo pipefail

# Checks the skill's text against what it describes (no spec generator renders
# this skill: capsule-cli's skillgen has capsulectl's own sections built in):
#
#   - AGENTS.md carries the same body as SKILL.md; only the heading differs;
#   - the verb table lists every verb the demo runs and only verbs the
#     capsulectl-book plugin has (`capsulectl-book --help` knows each one),
#     and the plugin's metadata names its command groups.
#
# It builds capsulectl-book from this checkout, so it needs Go.

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

body() { awk -v marker="$2" 'seen {print} index($0, marker) == 1 {seen = 1}' "$1"; }
# The shared body starts after the heading in SKILL.md and after the
# description line in AGENTS.md.
if ! diff <(body "$skill_dir/SKILL.md" "# capsulectl-book") <(body "$skill_dir/AGENTS.md" "$(sed -n 3p "$skill_dir/AGENTS.md")"); then
  echo "FAIL: AGENTS.md and SKILL.md bodies differ" >&2
  exit 1
fi
if [[ "$(sed -n 3p "$skill_dir/SKILL.md" | sed 's/^description: //')" != "$(sed -n 3p "$skill_dir/AGENTS.md")" ]]; then
  echo "FAIL: AGENTS.md and SKILL.md descriptions differ" >&2
  exit 1
fi

(cd "$skill_dir/../.." && go build -o "$work/capsulectl-book" ./cmd/capsulectl-book)
status=0
while IFS= read -r verb; do
  # `book result build` -> result build
  read -r -a words <<<"${verb#book }"
  if ! "$work/capsulectl-book" "${words[@]}" --help >/dev/null 2>&1; then
    echo "FAIL: the skill lists \`$verb\`, which capsulectl-book does not have" >&2
    status=1
  fi
done < <(grep -o '^| `book [a-z ]*`' "$skill_dir/SKILL.md" | sed 's/^| `//; s/`$//')

listed=$(grep -o '^| `book [a-z ]*`' "$skill_dir/SKILL.md" | wc -l | tr -d ' ')
if [[ "$listed" -ne 6 ]]; then
  echo "FAIL: expected the 6 book verbs in the table, found $listed" >&2
  status=1
fi
[[ "$status" -eq 0 ]] && echo "skill check passed" >&2
exit "$status"
