#!/usr/bin/env bash
set -euo pipefail

# Checks the deal skill's book commands against the capsulectl-book plugin (no
# spec generator renders this skill: capsule-cli's skillgen has capsulectl's
# own sections built in):
#
#   - every `book ...` command named in the skill's five-verbs table
#     (SKILL.md) or in the book verb table (references/book.md) is one the
#     capsulectl-book plugin has (`capsulectl-book <words> --help` knows it);
#   - the book verb table lists the 10 book verbs the demo runs.
#
# The five-verbs table's own structure (only the five verbs, no expert command
# in SKILL.md) is checked by the deal package's Go tests. It builds
# capsulectl-book from this checkout, so it needs Go.

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

(cd "$skill_dir/../.." && go build -o "$work/capsulectl-book" ./cmd/capsulectl-book)

# `book result build` in a table cell -> result build; flags are dropped
named=$(grep -h '^|' "$skill_dir/SKILL.md" "$skill_dir/references/book.md" |
  grep -o '`book [^`]*`' | sed -E 's/^`book //; s/`$//; s/ (-|[A-Z]).*$//' | sort -u)
[[ -n "$named" ]] || { echo "FAIL: no book command found in the skill's tables" >&2; exit 1; }

status=0
while IFS= read -r verb; do
  read -r -a words <<<"$verb"
  if ! "$work/capsulectl-book" "${words[@]}" --help >/dev/null 2>&1; then
    echo "FAIL: the skill names \`book $verb\`, which capsulectl-book does not have" >&2
    status=1
  fi
done <<<"$named"

listed=$(grep -c '^| `book [a-z -]*` |' "$skill_dir/references/book.md" || true)
if [[ "$listed" -ne 10 ]]; then
  echo "FAIL: expected the 10 book verbs in references/book.md's table, found $listed" >&2
  status=1
fi
[[ "$status" -eq 0 ]] && echo "skill check passed" >&2
exit "$status"
